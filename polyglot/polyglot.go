// Package polyglot converts HTML to polyglot XHTML/HTML4/HTML5 markup,
// parseable by XML parsers in almost all cases (warning if not).
//
// It does its best if the input is malformed, and has an optional strict mode.
// It emulates most of the HTML5 "adoption agency" algorithm and error
// recovery, so the final output ends up more or less equivalent to how a
// browser would parse it. There are hooks to allow a subset of transformations
// to be made in a single pass. There is also a CLI version
// ([github.com/pgaskin/htmlstream/cmd/polyglottify]).
//
// Polyglot markup is markup which HTML parsers and XML parsers parse to the
// same document. The rules for the output are those of the W3C note
// [Polyglot Markup: A robust profile of the HTML5 vocabulary], with the
// differences listed in [Convert]. HTML parsers parse the output to the same
// document as the input, apart from the changes listed in Convert (e.g., the
// doctype). XML parsers parse it to the same document as HTML parsers, except
// where [Options.Warn] reports input which can't be represented exactly.
// Converting the output again usually gives the same output, but this isn't
// guaranteed, especially for input with warnings.
//
// # Usage
//
// [Convert] converts a document read from an [io.Reader]. [Writer] converts
// the tokens of the caller's [htmlstream.Reader], which can use other Reader
// options and leave out elements. The hooks ([Options.OnTag] and
// [Options.OnText]) edit the output as it is written, and [AppendEscapeText]
// and [AppendEscapeAttr] escape text and attribute values in the markup they
// insert.
//
// Input which can't be represented exactly is converted anyway, and reported
// to [Options.Warn]. Errors stop the conversion and leave the output
// incomplete: structurally unsound input with [Options.Strict], contents
// exceeding [Options.MaxRawText], and held output exceeding
// [Options.MaxHold].
//
// # Well-formedness
//
// The output is well-formed XML, which meets the namespace constraints of
// Namespaces in XML 1.0, except where the contents of a script element whose
// type is neither JavaScript nor JSON (see [htmlutil.IsJavaScriptType] and
// [htmlutil.IsJSONType]) contain <, & or "]]>". They are written as they are,
// with a warning (see [Options.Warn]), since there is no comment syntax of the
// script's language to put a CDATA section in, and escaping them would change
// them for HTML parsers. With [Options.Strict], this is an error (an [*Error])
// instead.
//
// Well-formed means syntactically and structurally valid XML, which XML
// parsers read without errors. It doesn't mean valid against a DTD or schema
// (e.g., the XHTML DTDs): the output isn't validated. This is generally how
// browsers and non-validating parsing libraries (i.e., most of them) work.
//
// Markup provided by the caller is written as it is, without checks, and can
// make the output not well-formed: [Options.Doctype], the markup inserted by
// the hooks ([Options.OnTag] and [Options.OnText]), and the contents of
// scripts of other types set with [Text.SetText].
//
// # Serving
//
// The media type decides which parser reads the output: browsers read a
// document served as text/html with their HTML parser, and one served as
// application/xhtml+xml with their XML parser. Opened from a file, they go by
// the file extension instead (e.g., .html for HTML and .xhtml for XHTML). Only
// without a media type does the content decide (see [XMLDeclMode]). The output
// is always UTF-8, so the charset parameter can be left out for XML media
// types. For text/html, the charset meta element the output starts the head
// with is enough, unless the conversion warned that it is past the first 1024
// bytes, where HTML parsers look for it before parsing: serve such output as
// text/html; charset=utf-8.
//
// # Encodings
//
// The input must be UTF-8, which the output declares with
// <meta charset="UTF-8" />, and converting documents in other encodings to
// UTF-8 is up to the caller: the input is read as UTF-8 whatever it
// declares. Invalid UTF-8 is replaced with U+FFFD, with a warning. Meta
// elements declaring another encoding are replaced or removed like the
// others, and so is the first XML declaration before the root element (other
// XML declarations are written as comments). To convert a document, detect
// its encoding, e.g., with [htmlutil.PrescanCharset] (after the byte order
// mark and the charset parameter of the Content-Type header, as its
// documentation explains), and decode it with
// golang.org/x/text/encoding/htmlindex, golang.org/x/text/encoding/unicode
// and golang.org/x/text/transform, or with golang.org/x/net/html/charset,
// which does both:
//
//	br := bufio.NewReader(r)
//	head, _ := br.Peek(1024) // the whole input if it is shorter
//	name, ok := htmlutil.PrescanCharset(head)
//	if !ok {
//		name = "windows-1252" // or another default
//	}
//	enc, err := htmlindex.Get(name)
//	if err != nil {
//		return err
//	}
//	// BOMOverride decodes UTF-8 or UTF-16 instead if there is a byte order
//	// mark, and removes it.
//	err = polyglot.Convert(w, transform.NewReader(br, unicode.BOMOverride(enc.NewDecoder())), polyglot.Options{})
//
// [Polyglot Markup: A robust profile of the HTML5 vocabulary]: https://www.w3.org/TR/html-polyglot/
package polyglot

import (
	"bufio"
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"hash/maphash"
	"io"
	"iter"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	"github.com/pgaskin/htmlstream"
	"github.com/pgaskin/htmlstream/ascii"
	"github.com/pgaskin/htmlstream/htmlutil"
	"github.com/pgaskin/htmlstream/internal"
)

// Properties the output has to preserve beyond the rules of polyglot markup:
//
//   - Idempotence: converting the output again should give the same output.
//     TestConvert checks every case (the nonidempotent flag marks the
//     exceptions) and FuzzConvert every output without warnings. Anything
//     written must be read back the same way, e.g., the xmlns attribute is
//     written where the input had it (not after the other attributes, which
//     moved it after an xmlns:xlink written by the previous pass), and an
//     existing CDATA wrapper in script and style is removed before wrapping,
//     joining the "]]>" split in it, which is split again.
//   - Outputs with warnings for structure HTML parsers change are usually
//     not idempotent: a script ending inside an escaped script grows on every
//     pass, a second leading newline in pre is lost on every pass, and nested
//     a and nobr elements are restructured. Their warnings settle after the
//     first pass, but may name the output's elements instead of the input's
//     (e.g., text in a table wrapped in a reopened a is reported as an a the
//     second time).
//   - With scripting enabled in the Reader, noscript contents containing
//     markup (<, & or ]]>) are written as escaped text, with a warning, and
//     escaped again on every pass.
//   - CDATA wrappers: //<![CDATA[ is only removed at the end of its line (the
//     code after it is commented out), and the // forms aren't removed from
//     styles (// isn't a CSS comment, so removing them would change which
//     rules CSS parsers drop). The forms with HTML comment markers
//     (htmlutil.CDATAHTMLComment) aren't removed from modules, where the
//     HTML-like comments are syntax errors, or from script contents
//     containing <script, which starts a nested script after their <!--.
//   - Raw text: an incomplete end tag at the end of script or style contents
//     would be completed by the newline written after it, and "<script" at
//     the end of an escaped script would start a double escaped one.
//   - Leading newlines in pre, textarea and listing: HTML parsers drop the
//     first one and XML parsers don't, so it's removed, and another one can't
//     be represented. Neither can one in xmp and plaintext, which become pre.
//   - Attributes: lang and xml:lang have the same value on HTML elements (the
//     lang value wins), duplicates are removed keeping the first, attributes
//     with the same namespace and local name for XML parsers are dropped, and
//     the xlink prefix is declared where it's used. U+00A0 is written as
//     &#160; so it stays visible.
//   - Convert's Reader has scripting disabled (unless Options.Scripting is
//     set), so noscript contents are markup.

// DefaultMaxRawText is the default of [Options.MaxRawText], 64 MiB.
const DefaultMaxRawText = 64 << 20

// Options configures [Convert] and [Writer].
type Options struct {
	// Doctype is the document type declaration written at the start of the
	// document with [ReplaceDoctype], the default DoctypeMode. If empty,
	// "<!DOCTYPE html>" is used.
	Doctype string

	// DoctypeMode selects the document type declaration written.
	DoctypeMode DoctypeMode

	// XMLDecl selects the XML declaration written at the start of the
	// document (see [XMLDeclMode] for why to write one or not). The XML
	// declaration of the input isn't copied: [AddXMLDecl] and [KeepXMLDecl]
	// write a fixed one.
	XMLDecl XMLDeclMode

	// Strict makes structurally unsound input an error instead of converting
	// it on a best-effort basis. It also causes conversion to fail if the
	// output is not representable as well-formed XML (see Well-formedness in
	// the package documentation). Convert also reads the input in strict mode
	// (see [htmlstream.Reader.SetStrict]). Other input which can't be
	// represented exactly (e.g., invalid UTF-8, dropped attributes, and the
	// differences from HTML parsing listed in [Convert]) is still converted,
	// with a warning (see Warn). To fail on warnings, call [Writer.Fail] from
	// Warn.
	Strict bool

	// XMLSyntax and Scripting enable XML syntax and scripting in the Reader
	// of [Convert] (see [htmlstream.Reader.SetXMLSyntax] and
	// [htmlstream.Reader.SetScripting]). A [Writer] ignores them: its
	// caller's Reader decides them.
	XMLSyntax, Scripting bool

	// Warn, if not nil, is called for input which can't be represented
	// exactly as polyglot markup. The output converted so far is flushed to
	// the destination writer before each call, so warnings written to an
	// unbuffered stream (e.g., a terminal showing both) appear after the
	// output before them. Output still held back comes after the warning:
	// the contents of script and style elements, of noscript elements read
	// as raw text and of title and textarea elements read with XML syntax
	// starting with a CDATA section (see [Writer]), which are buffered until
	// their end tag, the whitespace at the start of other title and textarea
	// contents read with XML syntax, buffered until the text after it, the
	// whitespace at the start of text before which formatting elements are
	// reopened if other text follows (see [Convert]), buffered until that
	// text or the end of the text (up to 64 KiB), the whitespace at the
	// start of the head, buffered until the next token (up to 256 bytes),
	// the whitespace and comments between the head end tag and the body,
	// buffered until the body starts (up to 64 KiB), with [Options.Indent],
	// the whitespace and comments at block boundaries, buffered until the
	// next token (up to 64 KiB), the output before the root element with
	// [KeepXMLDecl], and the output held by [Writer.Hold].
	Warn func(w Warning)

	// MaxRawText limits the size of script and style contents, noscript
	// contents read as raw text, and title and textarea contents read with
	// XML syntax starting with a CDATA section (after whitespace) and the
	// whitespace at the start of other ones, which are buffered to decide
	// how to write them, and of the output before the root element with
	// [KeepXMLDecl] (the comments and the doctype of the input, without the
	// doctype written with [ReplaceDoctype]). If zero, [DefaultMaxRawText] is
	// used, and if negative, any contents exceed it. Exceeding it is an error (an
	// [*Error]), except for the output before the root element: then the XML
	// declaration is dropped with a warning instead (also with a negative
	// limit, for any output before the root element).
	MaxRawText int

	// MaxDepth limits the depth of the output (an element with the elements
	// around it), as [htmlstream.Reader.SetMaxDepth] does that of the input.
	// Formatting elements are reopened after misnested end tags (see
	// [Convert]) only up to it, and other elements nested past it are
	// dropped, keeping their contents, with a warning for each. Reopened
	// formatting elements can nest much deeper than the elements of the
	// input (e.g., <div><b></div>x repeated nests a b in the b before it),
	// and XML parsers often limit the depth (e.g., to 256). If zero,
	// [htmlstream.DefaultMaxDepth] is used, and values below 3 (the charset
	// meta element in the head) are treated as 3. [Convert] also limits the
	// depth of the input to it.
	MaxDepth int

	// MaxHold limits the size of the output held back by [Writer.Hold]:
	// exceeding it is an error ([htmlstream.ErrHoldLimit]). If zero,
	// [htmlstream.DefaultMaxHold] is used, and if negative, holding back any
	// output is an error.
	MaxHold int

	// FinalNewline ends the output with a line break after the html end tag,
	// which HTML parsers append to the body and XML parsers ignore. A line
	// break ending whitespace at the end of the body (e.g., the line break
	// at the end of the input, which HTML parsers put in the body) is written
	// there instead of in the body, and so is the line break [Options.Indent]
	// writes before the body end tag, so HTML parsers read the same body;
	// otherwise, the body HTML parsers read ends with a line break more than
	// the one XML parsers read. No Text event is reported for it.
	FinalNewline bool

	// Indent, if not empty, indents the output: the whitespace between block
	// boundaries (the start and end tags of block-level elements, see
	// [htmlutil.IsBlock], and of table cells, and the elements in the head)
	// is replaced with a line break followed by Indent once for each level
	// of block-level elements around it (up to 64 levels), and the comments
	// there are put on lines of their own. A line break is added where
	// there is no whitespace, and one empty line is kept where the input
	// has one or more between two elements. The spaces and tabs starting
	// the lines of text are replaced with the indentation of the contents
	// of the block-level element around it. The children of the html
	// element aren't indented, and there is no line break between the html
	// start tag and the head, and after the body (see [Convert]). With
	// [Options.FinalNewline], the line break before the body end tag is
	// written after the html end tag instead.
	//
	// Other whitespace is written as it is: in inline content (where adding
	// or removing whitespace would change the rendered text), in
	// preformatted elements (pre, listing, textarea), in elements whose
	// contents are text (title, script, style, and noscript, which HTML
	// parsers with scripting enabled read as text), in SVG and MathML
	// content, and whitespace before which HTML parsers reopen formatting
	// elements (see [Convert]), which changes the structure. The
	// classification of elements is that of the default styles of HTML:
	// style sheets which make block-level elements inline, or set
	// white-space to keep the whitespace in other elements, make the
	// changed whitespace visible.
	//
	// Indentation changes the whitespace text of the document for HTML and
	// XML parsers alike. The whitespace held back until the next token to
	// decide (see [Options.Warn], up to 64 KiB) is reported to the Text hook
	// when it is written as it is, and the whitespace replaced and the line
	// breaks and indentation written aren't. The Text hook gets the text
	// before the spaces and tabs starting its lines are replaced. Markup
	// inserted by the hooks (and written by [Writer.HoldDiscard]) is
	// written as it is, and doesn't change the indentation.
	Indent string

	// IndentPrefix, if not empty, is written at the start of each line of
	// the output after the first one, except in comments and in the
	// contents written as they are with Indent (preformatted elements,
	// elements whose contents are text, and SVG and MathML content). It
	// doesn't start empty lines. It can be used without Indent, to indent a
	// document as a whole, and then replaces the spaces and tabs starting
	// the lines of text, as Indent does. Like Indent, it changes the
	// whitespace text of the document.
	//
	// Indent and IndentPrefix must contain only spaces and tabs: other
	// characters fail the conversion with an error wrapping [ErrInvalidOp].
	IndentPrefix string

	// OnTag, if not nil, is called before each element tag is written, with a
	// description of the tag (see [Tag]). It is called for the tags of the
	// output: those written for the input, and those of the elements the
	// conversion adds. A hook which does nothing leaves the output unchanged;
	// the methods of the event change what is written.
	OnTag func(e *Tag)

	// OnText, if not nil, is called for each part of the text written (see
	// [Text]). A hook which does nothing leaves the output unchanged (the
	// text escaped, or written as contents when Raw); the methods of the
	// event change what is written.
	OnText func(t *Text)
}

// Warning describes input which can't be represented exactly as polyglot
// markup, for [Options.Warn].
type Warning struct {
	// Pos is the position of the input token the warning is about (for
	// text, comments and CDATA sections split into multiple tokens, see
	// [htmlstream.Token.Partial], the position of the first one), so it
	// doesn't depend on the Reader's chunk size.
	Pos htmlstream.Pos

	// Msg describes the input and how it is converted.
	Msg string
}

// ErrInvalidOp is the error of a [Writer] method or a hook event method (see
// [Tag] and [Text]) called in a state which doesn't allow it or with
// arguments it doesn't accept, and of tokens breaking the rules of the
// Writer. The error wraps it with details. Like other errors of the Writer
// except [htmlstream.ErrHoldReleased], it is sticky, unlike
// [htmlstream.ErrInvalidOp], after which a Transformer continues.
var ErrInvalidOp = errors.New("polyglot: invalid operation")

// Error is an error at a position in the input which stops the conversion:
// buffered contents exceeding [Options.MaxRawText] (of script and style
// elements, of noscript elements read as raw text, and of title and
// textarea elements read with XML syntax, see there), and with
// [Options.Strict], a script which can't be represented in XML (see
// Well-formedness in the package documentation).
type Error struct {
	Pos htmlstream.Pos // the position of the token
	Msg string         // the problem
}

// Error returns the message with the position, as "polyglot: error at offset
// Pos: Msg".
func (e *Error) Error() string {
	return fmt.Sprintf("polyglot: error at offset %d: %s", e.Pos, e.Msg)
}

// Tag describes an element tag about to be written, for [Options.OnTag]. The
// tags are those of the output: the element structure HTML parsers give the
// input, with the elements the conversion adds (implied html, head and body
// elements, the charset meta element, tbody, tr and colgroup implied in
// tables, formatting elements reopened after a misnested end tag, and the
// elements written for stray </p> and </br> end tags), and without those it
// drops. Start and end tag events are paired (with the same ID, Name, Local,
// NS and Reopened) and nested, and no tag event comes in the middle of a text
// node (see [Text]).
//
// The event is owned by the writer and valid during the call: the hook must
// not keep it or call its methods after returning. Its fields are for
// reading, and assigning them changes nothing: its methods change what is
// written for the tag; without them, the tag is written unchanged. Called in
// a way they don't allow (also after the hook returns), they make the
// conversion fail with an error wrapping [ErrInvalidOp], which
// [Writer.WriteToken] and [Writer.Close] (and [Convert]) return; a hook can
// check [Writer.Err] to stop early. Markup passed to a method is written when
// the hook returns, and not kept.
//
// Inserted markup (InsertBefore, InsertAfter, [Text.SetMarkup] and
// [Writer.HoldDiscard]) is written as is, and isn't checked: it must keep
// the output polyglot markup, well-formed XML which HTML parsers parse to the
// same document as XML parsers (see [Convert]). This means:
//
//   - It is balanced: well-formed on its own, or balanced across the start
//     and end tag events of one element (wrapping the element), or within one
//     text node (opened in a part and closed by the Last call).
//   - XML parsers read inserted elements in the namespace in scope (the
//     default namespace of the enclosing element) and HTML parsers read them
//     as HTML elements in HTML content and as foreign elements in svg and
//     math content: insert HTML markup only where NS is the XHTML namespace
//     and not inside svg or math (except in an integration point like
//     foreignObject, where the enclosing element is an HTML element), and
//     declare any prefix it uses.
//   - It avoids the places where HTML parsers restructure or ignore
//     content: table content outside cells (moved before the table), select,
//     the head (elements other than head content start the body), before
//     the html start tag and after the body end tag, title and textarea
//     (markup is text for HTML parsers), script and style (the contents are
//     wrapped in a CDATA section when needed, but </script or </style inside
//     them ends the element, and so does an incomplete one at their end,
//     completed by the markup after it; HTML parsers ignore the script end
//     tag after <!--<script without a later -->), and block elements inside
//     p (which closes the p).
//   - Text and attribute values are escaped ([AppendEscapeText],
//     [AppendEscapeAttr]) and have only characters allowed in XML (see
//     [htmlstream.AppendXMLChars]), script and style contents have no ]]>,
//     and comments have no --.
//   - The contents of pre, listing and textarea don't start with a newline
//     (inserted after the start tag, or at the start of the first text):
//     HTML parsers drop it, and XML parsers keep it.
type Tag struct {
	// Start is true for a start tag and false for an end tag.
	Start bool

	// Void is true for both events of an HTML void element (e.g., br). Its
	// end tag event comes right after the start tag event, with no tag
	// written: markup inserted for it is written after the start tag.
	// Other self-closed elements aren't void: they are written with an end
	// tag (e.g., <circle /> as <circle></circle>).
	Void bool

	// Reopened is set for the start tag of a formatting element reopened
	// after a misnested end tag closed it (<b><i>x</b>y</i> is written as
	// <b><i>x</i></b><i>y</i>), and for its end tag. The start tag written
	// is the element's start tag as written the first time, with its
	// attribute edits and name (see Rename).
	Reopened bool

	// Name is the element name as written (lowercase for HTML elements; see
	// Rename). Local and NS are the local name and namespace XML parsers give
	// the element (the XHTML namespace for HTML elements).
	Name, Local, NS string

	// ID numbers the elements of the output from 1, in the order of their
	// start tags. An element's start and end tag events have the same ID,
	// and so do the reopened copies of a formatting element and their end
	// tags, so a hook can keep what it found out about an element by ID.
	ID int

	// Depth is the number of elements open in the output around the element
	// (0 for the html element), the same for its start and end tags.
	Depth int

	// Token is the input start tag of the element, for start tags written
	// for the input. It is nil for end tags, for the tags of the elements
	// the conversion adds and for reopened elements (see ID).
	Token *htmlstream.Token

	p      *polyglot
	w      tagInfo // the fields as the writer uses them
	before []byte  // markup inserted before the tag
	after  []byte  // and after it
	fixed  bool    // the charset meta element: no attribute edits
	tfirst bool    // the first child element of a template (see templateChild)
	omit   bool    // the tag isn't written (Unwrap, Remove)
	remove bool    // Remove was called
	edited bool    // the start tag bytes in p.buf were edited (attributes, name)
}

// tagInfo is a copy of the fields of a Tag the writer and the methods use, so
// that assigning the fields doesn't change the output. Rename changes name
// and local.
type tagInfo struct {
	name, local, ns string
	start, reopened bool
	input           bool // Token isn't nil
}

// InsertBefore writes raw before the tag. Markup inserted by several calls is
// written in order. See [Tag] for the rules inserted markup follows.
func (e *Tag) InsertBefore(raw []byte) {
	if e.check() == nil {
		e.before = append(e.before, raw...)
	}
}

// InsertAfter writes raw after the tag (for the end tag event of a void
// element, after its start tag). Markup inserted by several calls is written
// in order. Markup inserted after the head start tag is written after the
// charset meta element (see [Convert]). See [Tag] for the rules inserted
// markup follows.
func (e *Tag) InsertAfter(raw []byte) {
	if e.check() == nil {
		e.after = append(e.after, raw...)
	}
}

// SetAttr adds the attribute name to the start tag with the value, or
// replaces the value of an attribute already written. On HTML elements, lang
// and xml:lang are set together, to the same value, like for attributes of
// the input. The value is escaped like a value of the input.
//
// It fails the conversion for an end tag, a reopened element (edit the first
// start tag, which the reopened copies repeat) and the charset meta element,
// for a name which isn't an XML name, has an unbound prefix or is xmlns or
// xmlns:* (changing namespace declarations changes how XML parsers read the
// contents, and HTML parsers ignore them), and for a value with characters
// not allowed in XML.
//
// The name must be the one HTML parsers give the attribute: lowercase on HTML
// elements, and with the case adjustments of SVG and MathML attributes
// ([htmlstream.AdjustSVGAttrName], [htmlstream.AdjustMathMLAttrName]) on
// those; otherwise HTML and XML parsers would give the attribute different
// names, and SetAttr fails the conversion. Attributes which change how the
// document is parsed or converted aren't checked, and must not be changed:
// type on input (hidden inputs stay in table content, other inputs are moved
// before the table), encoding on annotation-xml (an integration point for
// text/html and application/xhtml+xml), charset on meta, and http-equiv and
// content on a meta whose http-equiv is Content-Type (these encoding
// declarations are removed, and the encoding is declared by the meta element
// the conversion adds), and type and language on script (the CDATA wrapper
// and JSON escaping of the contents are chosen from the type in the input).
// Other attributes don't affect the document structure.
//
// Prefixed names other than xml:lang on HTML elements (e.g., xml:space or
// epub:type) aren't checked either: HTML parsers put these attributes in no
// namespace, and XML parsers in the namespace of the prefix, which gives a
// warning for such attributes of the input (see [Convert]).
func (e *Tag) SetAttr(name, value string) {
	if e.checkAttrEdit(name) != nil {
		return
	}
	p := e.p
	p.raw = append(p.raw[:0], value...)
	if !htmlstream.ValidXMLChars(p.raw) {
		p.fail("attribute " + strconv.Quote(name) + " set to a value with characters XML doesn't allow")
		return
	}
	p.setAttr(name, p.raw)
	if e.w.ns == htmlstream.NamespaceXHTML {
		switch name {
		case "lang":
			p.setAttr("xml:lang", p.raw)
		case "xml:lang":
			p.setAttr("lang", p.raw)
		}
	}
	e.edited = true
}

// RemoveAttr removes the attribute name from the start tag, if it has it. On
// HTML elements, lang and xml:lang are removed together. It fails the
// conversion like SetAttr (for a tag which can't be edited, and for a name
// which isn't an XML name or is a namespace declaration).
func (e *Tag) RemoveAttr(name string) {
	if e.checkAttrEdit(name) != nil {
		return
	}
	p := e.p
	p.removeAttr(name)
	if e.w.ns == htmlstream.NamespaceXHTML {
		switch name {
		case "lang":
			p.removeAttr("xml:lang")
		case "xml:lang":
			p.removeAttr("lang")
		}
	}
	e.edited = true
}

// checkAttrEdit checks that the tag's attributes can be edited (see SetAttr),
// and that name can be an attribute of it. The error is sticky.
func (e *Tag) checkAttrEdit(name string) error {
	if err := e.check(); err != nil {
		return err
	}
	p := e.p
	switch {
	case !e.w.start:
		return p.fail("attribute edit of an end tag")
	case e.w.reopened:
		return p.fail("attribute edit of a reopened " + e.w.name + " (edit its first start tag)")
	case e.fixed:
		return p.fail("attribute edit of the charset meta element")
	case name == "xmlns" || strings.HasPrefix(name, "xmlns:"):
		return p.fail("attribute edit of the namespace declaration " + strconv.Quote(name))
	case !htmlstream.ValidXMLName(name):
		return p.fail("attribute name " + strconv.Quote(name) + " isn't an XML name")
	case !p.prefixBound(name):
		return p.fail("attribute name " + strconv.Quote(name) + " has an unbound prefix")
	}
	lower := ascii.ToLower(name)
	switch e.w.ns {
	case htmlstream.NamespaceSVG:
		lower = htmlstream.AdjustSVGAttrName(lower)
	case htmlstream.NamespaceMathML:
		lower = htmlstream.AdjustMathMLAttrName(lower)
	}
	if lower != name {
		return p.fail("attribute name " + strconv.Quote(name) + " isn't the name HTML parsers give it (" + strconv.Quote(lower) + ")")
	}
	return nil
}

// Rename renames the element to name: its start and end tags are written
// with the name, and so are its copies reopened after a misnested end tag
// (see Reopened). The event's Name and Local are changed, and the events of
// the end tag and of the reopened copies have the name too. The conversion
// still applies the rules of the element's name in the input (e.g., which
// elements are reopened), which are the same for both names.
//
// Only renames which keep the element structure HTML parsers build are
// allowed: both names must be in one of these groups of HTML elements, whose
// members the tree construction rules of the [HTML Standard] handle with the
// same steps everywhere, and which are in the same categories (special,
// formatting, closing an open p, ending foreign content, scope boundaries):
//
//   - h1, h2, h3, h4, h5, h6
//   - pre, listing
//   - article, aside, details, dir, fieldset, figcaption, figure, footer,
//     header, hgroup, main, nav, section, summary
//   - blockquote, center, dl, menu
//   - ol, ul
//   - applet, marquee, object
//   - b, big, code, em, i, s, small, strike, strong, tt, u
//   - span, sub, sup, var
//   - abbr, acronym, bdi, bdo, cite, data, del, dfn, ins, kbd, mark, q,
//     samp, time
//   - area, wbr
//   - embed, img
//   - param, source, track
//
// Elements with steps of their own are left out, and so are those named in
// the steps for other elements: e.g., address, div and p (an li, dd or dt
// start tag closes an open li, dd or dt through them, but not through other
// special elements), a, font and nobr, ruby and its parts, button, form,
// select and its contents, list items, table parts and head content. So are
// elements whose handling differs between the versions of the standard HTML
// parsers implement: search and dialog (which close an open p in recent
// versions) and keygen (in a select).
//
// It fails the conversion for an end tag, a reopened element (rename its
// first start tag, which the reopened copies repeat), a foreign element, and
// names which aren't in the same group. Renaming an element to its own name
// does nothing, even for elements outside the groups.
//
// Renaming formatting elements (e.g., b to i) changes which of them HTML
// parsers consider identical (the same name and attributes). With more than
// three identical formatting elements, HTML parsers keep only the last three
// to reopen after misnested markup (see the Noah's Ark clause of
// [push onto the list of active formatting elements]), so the renamed output
// can nest differently than the renamed input would.
//
// [HTML Standard]: https://html.spec.whatwg.org/multipage/parsing.html#parsing-main-inbody
// [push onto the list of active formatting elements]: https://html.spec.whatwg.org/multipage/parsing.html#push-onto-the-list-of-active-formatting-elements
func (e *Tag) Rename(name string) {
	if e.check() != nil {
		return
	}
	p := e.p
	switch g, ok := renameGroup[e.w.name]; {
	case !e.w.start:
		p.fail("rename of an end tag")
		return
	case e.w.reopened:
		p.fail("rename of a reopened " + e.w.name + " (rename its first start tag)")
		return
	case name == e.w.name:
		return
	case e.w.ns != htmlstream.NamespaceXHTML:
		p.fail("rename of the foreign element " + e.w.name)
		return
	case !ok || renameGroup[name] != g:
		p.fail("rename of " + e.w.name + " to " + strconv.Quote(name) + " (HTML parsers may parse them differently)")
		return
	}
	// the start tag in p.buf starts with "<" and the name
	delta := len(name) - len(e.w.name)
	p.buf = slices.Replace(p.buf, 1, 1+len(e.w.name), []byte(name)...)
	for i := range p.attrs {
		p.attrs[i].off += delta
		p.attrs[i].end += delta
	}
	e.w.name, e.w.local = name, name
	e.Name, e.Local = name, name
	e.edited = true
}

// renameGroup numbers the groups of the HTML elements Tag.Rename renames to
// each other, from 1.
var renameGroup = func() map[string]int {
	m := map[string]int{}
	for i, g := range []string{
		"h1 h2 h3 h4 h5 h6",
		"pre listing",
		"article aside details dir fieldset figcaption figure footer header hgroup main nav section summary",
		"blockquote center dl menu",
		"ol ul",
		"applet marquee object",
		"b big code em i s small strike strong tt u",
		"span sub sup var",
		"abbr acronym bdi bdo cite data del dfn ins kbd mark q samp time",
		"area wbr",
		"embed img",
		"param source track",
	} {
		for _, name := range strings.Fields(g) {
			m[name] = i + 1
		}
	}
	return m
}()

// Unwrap leaves out the tag and its end tag, keeping the contents of the
// element (like leaving out both tags of the input, see [Writer]). Both tag
// events are still reported. A formatting element unwrapped isn't reopened
// after a misnested end tag, and a leading newline isn't removed from an
// unwrapped pre, textarea or listing.
//
// It fails the conversion for an end tag, a reopened element, html, head and
// body (also those of the input), an element the conversion adds (tbody, tr,
// colgroup, the charset meta element and the elements written for stray end
// tags), the first child element of a template (HTML parsers parse the
// template contents like a table, a row, a cell, etc. or like body content
// depending on it), an svg, math or other foreign element (whose contents are
// foreign content for HTML parsers because of it), an element in foreign
// content (an integration point's child, whose start tag declares the XHTML
// namespace) and an element declaring namespace prefixes (which its contents
// may use). As for the input, it isn't checked that the contents are parsed
// the same without the element: e.g., unwrapping the b in <option><b><option>
// gives <option><option>, where HTML parsers close the first option;
// unwrapping a table, select, template or p changes how HTML parsers read the
// contents.
func (e *Tag) Unwrap() {
	if e.checkStructEdit("unwrap") != nil {
		return
	}
	p := e.p
	switch {
	case e.w.ns != htmlstream.NamespaceXHTML:
		p.fail("unwrap of the foreign element " + e.w.name)
	case p.inForeign():
		p.fail("unwrap of " + e.w.name + " in foreign content")
	case len(p.tagDecls) != 0:
		p.fail("unwrap of " + e.w.name + " declaring namespace prefixes")
	default:
		e.omit = true
	}
}

// Remove leaves out the element: the tag, the contents and the end tag (like
// leaving out all of its tokens, see [Writer]). No events are reported for
// the contents, and the end tag event is still reported, with no tag written.
// Markup inserted before or after either tag (in this event or in the end tag
// event) is still written, in that order, where the element was. Text nodes
// separated only by a removed element are still reported as two nodes. It
// fails the conversion for an end tag, a reopened element, html, head and
// body (also those of the input), an element the conversion adds and the
// first child element of a template (see Unwrap). Removing other elements
// doesn't change how HTML parsers read the rest of the document.
func (e *Tag) Remove() {
	if e.checkStructEdit("removal") == nil {
		e.omit, e.remove = true, true
	}
}

// checkStructEdit checks that the element can be unwrapped or removed, for
// the operation op ("unwrap" or "removal").
func (e *Tag) checkStructEdit(op string) error {
	if err := e.check(); err != nil {
		return err
	}
	p := e.p
	switch {
	case !e.w.start:
		return p.fail(op + " of an end tag")
	case e.w.reopened:
		return p.fail(op + " of a reopened " + e.w.name)
	case !e.w.input:
		return p.fail(op + " of " + e.w.name + " added by the conversion")
	case e.w.ns == htmlstream.NamespaceXHTML && (e.w.name == "html" || e.w.name == "head" || e.w.name == "body"):
		return p.fail(op + " of " + e.w.name)
	case e.tfirst:
		return p.fail(op + " of " + e.w.name + ", the first child element of a template (it selects how HTML parsers parse the template contents)")
	}
	return nil
}

// check checks that the event is the one of the current Tag hook call. The
// error is sticky.
func (e *Tag) check() error {
	if e.p.hook != tagHook {
		return e.p.setErr(errHook)
	}
	return nil
}

// Writer returns the writer, for its holds, Fail and Written, also for hooks
// of [Convert].
func (e *Tag) Writer() *Writer { return e.p.pw }

// errHook is the error of event methods called outside the hook call the
// event is for.
var errHook = fmt.Errorf("%w: edit of a hook event after its hook returned", ErrInvalidOp)

// hook call kinds (polyglot.hook)
const (
	noHook = iota
	tagHook
	textHook
)

// Text describes text about to be written, for [Options.OnText]. A text node
// in the output (text between two tags or comments, which may come from
// several input tokens, including text around dropped elements) is reported
// in parts, in order: the first part has First set, and after the last part,
// a call with Last set and no Data is made before the markup following the
// text, or at Close. Elements the conversion adds between parts of the input
// text (reopened formatting elements) end the text node like other tags, and
// so do removed elements (see [Tag.Remove]). Whitespace held back (see
// [Options.Warn]) is reported when it is written, so the whitespace between
// the head end tag and the body is reported after the tag events of the head
// content after it, and the whitespace at the start of the head after the
// head start tag event, before or after the event of the charset meta
// element (see [Convert]). With [Options.Indent], whitespace replaced by the
// indentation isn't reported (see there).
//
// The event is owned by the writer and valid during the call, like [Tag]. Its
// fields are for reading, like those of Tag, and the bytes of Data must not
// be modified: its methods change what is written for the part; without them,
// the text is written unchanged (escaped, or as contents when Raw). Called in
// a way they don't allow, they make the conversion fail, like the methods of
// Tag.
type Text struct {
	// Data is the text as written, before escaping: decoded, without the
	// NUL characters Convert drops, and with the other characters not
	// allowed in XML replaced. It is valid during the call.
	Data []byte

	// First is set for the first part of a text node, and Last for the call
	// ending it, which has no Data.
	First, Last bool

	// Raw is set for the contents of a script or style element and for
	// noscript contents read as raw text written unchanged (see [Writer]):
	// the text is written as contents, not as markup (for script and style,
	// wrapped in a CDATA section if needed, like the contents would be).
	// These contents, noscript contents read as raw text written as escaped
	// text, and title and textarea contents read with XML syntax starting
	// with a CDATA section (after whitespace, see [Writer]), which aren't
	// Raw, are reported in one call.
	Raw bool

	// Token is the input token the text comes from (for the contents
	// reported in one call, the last one), or nil when Last.
	Token *htmlstream.Token

	// ID is the ID of the element the text is in (see [Tag.ID]): the
	// innermost element open in the output, also when it was unwrapped
	// ([Tag.Unwrap]). Depth is the number of elements open in the output
	// around the text, one more than the Depth of that element. Both are the
	// same for all parts of a text node.
	ID, Depth int

	p      *polyglot
	raw    bool   // Raw, as the writer uses it
	data   []byte // the replacement (SetText or SetMarkup)
	set    bool
	isText bool // data is text (SetText), to escape
}

// SetText writes data instead of the text, escaped like the text would be
// (for Raw, as the new contents of the element, wrapped in a CDATA section if
// needed; the contents of script and style must not have the end tag of the
// element in them, nor end with an incomplete one (e.g., </script), and
// those of script must not contain <!--<script without a later -->, after
// which HTML parsers ignore the end tag). For the Last call, data is written
// at the end of the text node. The data must have only characters allowed in XML (see
// [htmlstream.AppendXMLChars]), and must not start with a newline at the
// start of the contents of pre, listing or textarea (see [Tag]).
//
// Noscript contents read as raw text containing <, & or ]]> are written
// escaped, without the warning given for such contents of the input: XML
// parsers and HTML parsers with scripting disabled read them as text, and
// HTML parsers with scripting enabled read the escapes (e.g., &lt;) as
// they are.
//
// Script and style contents are written like the contents of the input
// (see Well-formedness in the package documentation), without the warnings
// for such contents of the input, and without the error of [Options.Strict]:
// "]]>" in JavaScript and CSS is written as "]]]]><![CDATA[>" in the CDATA
// section, which HTML parsers read as it is, and the contents of scripts of
// types other than JavaScript and JSON are written as they are: if they
// contain <, & or ]]>, keeping the output well-formed is up to the caller.
// A CDATA wrapper in data (e.g., //<![CDATA[ ... //]]>) isn't removed like
// that of the contents of the input, whose Data is without it: it is written
// as part of the contents. The whitespace around the wrapper of the input is
// written around the new contents.
func (t *Text) SetText(data []byte) {
	if t.check() == nil {
		t.data, t.set, t.isText = data, true, true
	}
}

// SetMarkup writes raw, markup, instead of the text (nothing, if raw is
// empty). For the Last call, it is written at the end of the text node. See
// [Tag] for the rules inserted markup follows. It fails the conversion for
// Raw text, which is written as contents.
func (t *Text) SetMarkup(raw []byte) {
	switch {
	case t.check() != nil:
	case t.raw:
		t.p.fail("markup in raw text")
	default:
		t.data, t.set, t.isText = raw, true, false
	}
}

// check checks that the event is the one of the current Text hook call. The
// error is sticky.
func (t *Text) check() error {
	if t.p.hook != textHook {
		return t.p.setErr(errHook)
	}
	return nil
}

// Writer returns the writer, like [Tag.Writer].
func (t *Text) Writer() *Writer { return t.p.pw }

// XMLDeclMode selects the XML declaration written by [Convert] and [Writer].
//
// The output doesn't need an XML declaration: XML parsers read a document
// without one as XML 1.0 in UTF-8 (see [character encoding in entities]),
// which the output is. HTML parsers read a declaration as a comment (a parse
// error), so the documents HTML and XML parsers build differ by that comment,
// and the W3C polyglot markup note forbids it (see [Convert]). A declaration
// only matters when the output is read without a media type (e.g., served
// without a Content-Type header): [MIME sniffing] then identifies a resource
// starting with "<?xml" as XML, and one starting with a doctype or an html tag
// as HTML. With a media type, the document is read as that, whatever the
// declaration says (see Serving in the package documentation). [KeepXMLDecl]
// writes one only if the input has one.
//
// [character encoding in entities]: https://www.w3.org/TR/xml/#charencoding
// [MIME sniffing]: https://mimesniff.spec.whatwg.org/#identifying-a-resource-with-an-unknown-mime-type
type XMLDeclMode uint8

const (
	// NoXMLDecl writes no XML declaration.
	NoXMLDecl XMLDeclMode = iota

	// AddXMLDecl writes <?xml version="1.0" encoding="UTF-8"?> at the start
	// of the document.
	AddXMLDecl

	// KeepXMLDecl writes the XML declaration of [AddXMLDecl] if the input has
	// one before the root element (HTML parsers read it as a bogus comment,
	// which may follow other comments and the doctype). The output before the
	// root element (comments and the doctype) is held back until the root
	// element starts, up to [Options.MaxRawText]; an XML declaration after
	// more output than that is removed with a warning.
	KeepXMLDecl
)

// DoctypeMode selects the document type declaration written by [Convert] and
// [Writer].
//
// The default, <!DOCTYPE html>, selects no-quirks mode in HTML parsers, like
// XML parsers, which always use it. Without a doctype ([NoDoctype]), or with a
// kept doctype selecting quirks or limited-quirks mode, HTML parsers use that
// mode while XML parsers don't, so browsers can lay out the document
// differently. In quirks mode, HTML parsers also put a table after an open p
// in the p, and the output keeps the p open around it then (see [Convert]).
// A doctype with an XHTML public identifier doesn't change how XML parsers
// read the output, which has no named character references other than those
// XML predefines. EPUB 3.3 allows no external identifier in the doctype of
// XHTML content documents (see [EPUB XML conformance]), so the default is the
// one to use there.
//
// [EPUB XML conformance]: https://www.w3.org/TR/epub-33/#sec-xml-constraints
type DoctypeMode uint8

const (
	// ReplaceDoctype writes [Options.Doctype] at the start of the document,
	// and removes the doctype of the input.
	ReplaceDoctype DoctypeMode = iota

	// KeepDoctype writes the doctype of the input where it is (after the
	// comments before it), as HTML parsers read it: the name lowercased, and
	// the public and system identifiers, in double quotes (or single quotes
	// for a system identifier containing double quotes). A public
	// identifier without a system identifier gets an empty one, which XML
	// requires. Doctypes HTML parsers ignore (after other content) are
	// removed. A doctype which can't be represented in XML (the name isn't
	// an XML name, the public identifier has characters XML doesn't allow in
	// it, or the system identifier has both kinds of quotes) or has a > in
	// its system identifier, where HTML parsers end it, is removed with a
	// warning, and so is one selecting another document mode
	// (quirks, limited-quirks or no-quirks) in HTML parsers as written than
	// in the input (e.g., a malformed one).
	KeepDoctype

	// NoDoctype writes no doctype, and removes the doctype of the input.
	NoDoctype
)

// Convert converts an HTML document to polyglot markup (see the package
// documentation). On error, the output is incomplete: w has the output
// written before the error, which can end anywhere (e.g., in a tag).
//
// The input must be UTF-8, which the output declares. Invalid UTF-8 is
// replaced with U+FFFD, with a warning, and so are the other characters XML
// doesn't allow, except NUL in text in HTML content, which HTML parsers
// ignore and Convert drops (see the conversion list below). Convert
// documents in other encodings to UTF-8 first (see Encodings in the package
// documentation).
//
// Convert reads the input with an [htmlstream.Reader] in HTML mode with the
// Reader's defaults, the maximum depth of Options.MaxDepth, strict mode with
// Options.Strict, and XML syntax and scripting with Options.XMLSyntax and
// Options.Scripting. Without Options.Scripting, noscript contents are
// converted as markup, as XML parsers read them. To use other Reader options
// (e.g., lower limits for untrusted input), pass the tokens of a Reader to a
// [Writer].
//
// Besides its size, converting a token takes time proportional to at most
// the number of elements open in the output, for the formatting elements
// reopened before it and for the checks of the open elements HTML parsers
// also make (e.g., at an end tag of a formatting element which isn't open).
// Options.MaxDepth limits that number: lower it for untrusted input. The
// output can be much larger than the input, since formatting elements
// reopened after misnested end tags are copies, as in the DOM of HTML
// parsers (e.g., formatting elements left open in a div are copied around
// the text in each div after it).
//
// Documents written as XHTML, with self-closing tags meant as empty elements
// (usually with an XML declaration or the XHTML xmlns attribute, e.g., EPUB
// content documents), need XML syntax (Options.XMLSyntax, see
// [htmlstream.Reader.SetXMLSyntax]). Without it, they are read as HTML
// parsers read them: a self-closing tag of a non-void element is a start tag,
// so <a id="p1"/> contains the content after it and <script src="a.js"/>
// takes the rest of the input as its contents (with a warning for raw text
// and RCDATA elements like script, style, title and textarea), </br> after
// <br> is another br element, CDATA sections are bogus comments, processing
// instructions are bogus comments (as HTML parsers read them before the
// current HTML Standard, which reads most of them as processing instructions;
// see the conversion list below), and a doctype with an internal subset ends
// at the first ">" in it. XML syntax is for documents written as XHTML: for
// HTML, it only changes the meaning of loosely written markup, such as <div
// class="clear"/>, a start tag for browsers reading text/html and an empty
// div with XML syntax.
//
// Convert reads the input in HTML mode, and [Writer] only accepts tokens
// read in HTML mode. For documents of unknown type, HTML or XHTML, which
// would otherwise be read in [htmlstream.Universal] mode, HTML mode with XML
// syntax is usually good enough. Well-formed XHTML is then read with the
// HTML parsing rules rather than as XML parsers read it: elements are closed
// and restructured as in HTML parsers (e.g., a div start tag closes an open
// p, and misnested formatting elements are reopened), script and style
// contents are raw text, with a warning for character references and
// comments in them, title and textarea contents are text, a newline at the
// start of pre, textarea and listing is removed, with a warning, names are
// case-insensitive and prefixes don't bind namespaces, with a warning for
// prefixed names, and character references are decoded as in HTML (e.g.,
// &copy without a semicolon is ©, and &#150; is U+2013).
//
// The conversion is a single streaming pass:
//
//   - The doctype is replaced with [Options.Doctype] (see
//     [Options.DoctypeMode] for the alternatives), the XML declaration
//     before the root element is removed (see [Options.XMLDecl] for writing
//     one), and
//     <meta charset="UTF-8" /> is added at the start of the head (before
//     the markup hooks insert after the head start tag), replacing the
//     charset declarations of the input, so the output has one encoding
//     declaration: the charset attribute, and http-equiv="Content-Type"
//     with its content attribute, are removed from meta elements, including
//     the declarations HTML parsers don't recognize (see
//     [htmlutil.MetaCharset]), and a meta element without other
//     attributes is dropped. If such a meta element is the first element of
//     the head, the added one takes its place, after the whitespace before
//     it (up to 256 bytes); otherwise, and if the Tag hook of the head start
//     tag inserts markup after it or starts a hold, the added one comes
//     right after the head start tag.
//   - The XML declaration, the doctype and each comment before the root
//     element are followed by a line break, which HTML and XML parsers
//     ignore there. Other whitespace before the root element, which they
//     ignore too, is dropped.
//   - Whitespace between the html start tag and the head, which HTML
//     parsers drop (and XML parsers would keep), is dropped. Whitespace and
//     comments between the head end tag and the body, which HTML parsers put
//     in the html element, while they put head content after them in the
//     head, are written between the head end tag and the body start tag.
//     Past 64 KiB of them, they are written in the head instead.
//   - Implied elements (html, head, body, tbody, tr, colgroup) and implied end
//     tags are written explicitly.
//   - In quirks mode documents (no doctype, or a legacy one), HTML parsers
//     put a table after an open p in the p, which the Reader closes before
//     the table. When the output is in quirks mode too (with [NoDoctype],
//     with a doctype selecting quirks mode in [Options.Doctype], or with
//     [KeepDoctype]), the p is kept open around the table, and after it
//     until the end tag of an element around it, a p end tag, or a start
//     tag which closes a p, as in HTML parsers. Only the start tags of void
//     elements, tables and elements whose contents are text (e.g., script
//     and title) keep it open: before other start tags, it is closed with a
//     warning, since HTML parsers would put the element in the p.
//   - HTML element and attribute names are lowercased, and SVG and MathML
//     names get the case HTML parsers give them. The tags of elements whose
//     names aren't XML names (see [htmlstream.ValidXMLName]) are left out,
//     keeping their contents, and attributes whose names aren't XML names are
//     dropped, with a warning. This includes names with NUL, which HTML
//     parsers replace with U+FFFD. Attributes are quoted, duplicates are
//     removed. On HTML elements, lang is mirrored to xml:lang
//     and xml:lang to lang, so HTML and XML parsers use the same language.
//   - Void elements are written as <br />, and all other elements have end
//     tags.
//   - The required xmlns attributes are added to html, svg and math.
//   - Text and attribute values are escaped for both HTML and XML, and
//     character references are replaced with the characters (except for
//     U+00A0, written as &#160;).
//   - NUL in text in HTML content, including integration points like
//     foreignObject, is dropped, since HTML parsers ignore it. Elsewhere,
//     and in CDATA sections, it is replaced with U+FFFD, with the warning
//     for characters XML doesn't allow: HTML parsers replace it in SVG and
//     MathML content, and the tokenizer does in comments, attribute values
//     and the contents of elements like script, title and textarea (see
//     [htmlstream.Token.AppendText]). A newline after NUL directly after
//     the start tag of a pre or listing, which the HTML Standard keeps
//     (browsers drop it), can't be represented (with a warning).
//   - Script and style contents containing < or & are wrapped in a CDATA
//     section inside a comment of the script or style language, after
//     removing such a wrapper if the contents already have one, including
//     the forms with HTML comment markers written by CMSs such as Drupal
//     (see [htmlutil.CutCDATAWrapper]; not in modules), and keeping the
//     whitespace around it. The start of the wrapper is followed by a line
//     break and its end preceded by one, unless the contents start or end
//     with a single line break, which takes its place. For JSON
//     scripts, the characters are escaped as JSON instead. Scripts of other
//     types are written as they are, with a warning (see Well-formedness in
//     the package documentation).
//   - Formatting elements closed by misnested end tags are reopened like
//     HTML parsers do (a simplified version of the adoption agency
//     algorithm), so <b><i>x</b>y</i> becomes <b><i>x</i></b><i>y</i>.
//   - Leading newlines in pre, textarea and listing, which HTML parsers drop,
//     are removed.
//   - xmp and plaintext are converted to pre. A leading newline in them,
//     which HTML parsers keep, is removed with a warning, since pre can't
//     represent it.
//   - Tags cut off by the end of the input, which HTML parsers drop, are
//     dropped.
//   - xmlns attributes are only written on html, svg and math and on HTML
//     elements in foreign content (e.g., in foreignObject), with the
//     namespace HTML parsers give the element, replacing another value
//     (which HTML parsers ignore). They are dropped from other elements.
//   - "--" in comments, which XML doesn't allow, is written as "- -", and a
//     space is added at the start of a comment starting with > or -> and at
//     the end of one ending with -, which changes the comment for HTML
//     parsers.
//   - Processing instructions are written as comments, and so are XML
//     declarations other than the first one before the root element, as
//     HTML parsers read them. The current HTML Standard reads most
//     processing instructions (<?target data>) as processing instructions,
//     while polyglot writes them as comments, which all HTML parsers (older
//     ones too) and XML parsers read the same.
//
// Known differences from HTML parsing which aren't handled are reported
// through [Options.Warn]:
//
//   - content in tables outside cells, which HTML parsers move before the
//     table, and elements inside head content which they move to the body;
//   - misnested formatting elements where the adoption agency algorithm
//     restructures the elements, and end tags HTML parsers ignore;
//   - content after a body or html end tag which closed other elements,
//     which HTML parsers keep open and put the content in, and likewise
//     after a noscript end tag which closed a special element, or an end tag
//     of an element already closed. Whitespace and comments after a body or
//     html end tag are written in the elements, as HTML parsers put the
//     whitespace there (they put the comments after the body), up to the
//     first other token (e.g., content, or a stray end tag) and 64 KiB of
//     whitespace in one text; whitespace after that and after the other end
//     tags isn't reported;
//   - template contents which HTML parsers parse differently: they parse
//     them like the contents of a table, a table row, etc., or body content,
//     depending on the first child element; and template end tags closing a
//     cell or another marker element open inside the template, after which
//     HTML parsers reopen formatting elements differently;
//   - start tags HTML parsers ignore: table parts outside a table, frame,
//     frameset, nested forms and nested selects in the body, and noscript
//     in a noscript in the head;
//   - element and attribute names with a namespace prefix, which XML parsers
//     resolve, and HTML parsers don't (except for some xlink and xml
//     attributes of SVG and MathML elements): x:a in <p xmlns:x="u"
//     x:a="1"> is in namespace u for XML parsers and in no namespace for
//     HTML parsers. This is reported once for each prefix and namespace, so
//     the epub:type attributes of EPUB content give one warning. HTML parsers
//     keep the prefix in element names, so they also differ when the
//     namespaces are the same;
//   - namespace declarations which are invalid in XML (of the xmlns prefix,
//     of the xml prefix to another namespace, and of other prefixes to an
//     empty namespace name or to the XML or XMLNS namespace), which are
//     dropped, leaving the prefix unbound; element and attribute names with
//     an unbound prefix, element names with the xmlns prefix, and attributes
//     with the same namespace and local name as another one for XML parsers,
//     which are dropped;
//   - xml:lang on HTML elements with a value other than lang's: HTML parsers
//     use lang for the language, and XML parsers xml:lang, which is written
//     with the value of lang;
//   - elements nested past the Reader's stack limits or the output depth
//     limit (Options.MaxDepth), which are dropped, and formatting elements
//     which would be reopened past the output depth limit, which aren't;
//   - incomplete script and style end tags at the end of their contents
//     (cut off by the end of the input, or followed by the end tag), which
//     HTML parsers keep as text, and are dropped, and scripts cut off inside
//     an escaped script (<!--<script), which can't be closed for HTML
//     parsers;
//   - noscript in an implied head or after a misplaced head start tag,
//     where the Reader's rules for noscript in the head differ from HTML
//     parsers (with scripting disabled);
//   - tables after an open p in quirks mode documents, where HTML parsers
//     put the table in the p (or in the elements open in it), unless the
//     output is in quirks mode too and only the p is open (see above): the
//     output closes the p before the table explicitly; and with the p kept
//     open, start tags of other elements after the table, and end tags of
//     elements around the p which HTML parsers ignore or apply differently
//     with the p open;
//   - script and style contents containing "]]>", which is written as
//     "]]]]><![CDATA[>" to keep the CDATA section open, which changes the
//     contents for HTML parsers ("]]]]><![CDATA[>" in a CDATA wrapper of the
//     input is read as "]]>", and written as it was);
//   - the charset meta element written past the first 1024 bytes of the
//     output (after long comments before the root element, or long
//     attributes on html or head), where HTML parsers which aren't told the
//     encoding don't look for it (see Serving in the package documentation),
//     unless the output starts with an XML declaration ([AddXMLDecl], or
//     [KeepXMLDecl] with one in the input), from which they take UTF-8
//     instead. Its position is taken when it is written: markup put before
//     it later by [Writer.HoldDiscard] isn't counted.
//
// A warning message describes the input or what the conversion did with it,
// often followed in parentheses by what HTML parsers would have done with
// the input instead ("HTML parsers would have ..."), how they will read the
// output ("HTML parsers will ... when parsing the output") or what the
// conversion matches ("to match ..."), and by "(will not warn again)" for a
// warning given only once.
//
// The rules of the output are those of the W3C [polyglot markup] note:
// UTF-8 declared with a meta element (with the charset attribute: the
// current HTML Standard doesn't allow the http-equiv form in XML documents,
// see [encoding declaration]), the HTML5 doctype, the html, svg and
// math namespace declarations (and that of the xlink prefix), no omitted
// tags, tbody and colgroup elements, lowercase names except for the SVG and
// MathML ones with mixed case, void elements as empty-element tags, safe
// text content or safe CDATA content in script and style, no leading
// newline in pre and textarea, quoted attribute values with tabs and
// newlines as character references, lang and xml:lang with the same value,
// no named character references other than those of XML, and comments
// which don't start with > or ->. The output differs from the note where
// it doesn't match the current HTML Standard, or asks for what a conversion
// can't decide:
//
//   - SVG and MathML names get the case HTML parsers give them (see
//     [htmlstream.AdjustSVGTagName] and [htmlstream.AdjustSVGAttrName]): the
//     list of [element names] doesn't have feDropShadow, and the list of
//     [attribute names] has contentScriptType, contentStyleType,
//     externalResourcesRequired and filterRes, which HTML parsers lowercase.
//   - noscript elements, which the note [excludes], are kept.
//   - [Options.XMLDecl] can write an XML declaration, which the note
//     forbids, and [Options.Doctype] another doctype.
//   - A title element, which the note requires in a [minimal document], isn't
//     added.
//   - xml:space and xml:base attributes on HTML elements, which the note
//     [disallows], are kept, with the warning for prefixed attributes.
//   - The case of attribute values which HTML compares case-insensitively
//     isn't changed (see [attribute values]), and id attributes aren't
//     checked to be unique XML names (see [the id attribute]).
//
// [polyglot markup]: https://www.w3.org/TR/html-polyglot/#writing
// [encoding declaration]: https://html.spec.whatwg.org/multipage/semantics.html#attr-meta-http-equiv-content-type
// [element names]: https://www.w3.org/TR/html-polyglot/#element-names
// [attribute names]: https://www.w3.org/TR/html-polyglot/#attribute-names
// [excludes]: https://www.w3.org/TR/html-polyglot/#excluded-elements
// [minimal document]: https://www.w3.org/TR/html-polyglot/#minimal-polyglot-html-document
// [disallows]: https://www.w3.org/TR/html-polyglot/#disallowed-attributes
// [attribute values]: https://www.w3.org/TR/html-polyglot/#attribute-values
// [the id attribute]: https://www.w3.org/TR/html-polyglot/#id-attribute
func Convert(w io.Writer, r io.Reader, opt Options) error {
	pw := NewWriter(w, opt)
	rd := htmlstream.NewReader(r)
	rd.SetStrict(opt.Strict)
	rd.SetXMLSyntax(opt.XMLSyntax)
	rd.SetScripting(opt.Scripting)
	rd.SetMaxDepth(pw.p.opt.MaxDepth)
	for rd.Next() {
		if err := pw.WriteToken(rd.Token()); err != nil {
			return err
		}
	}
	if err := rd.Err(); err != nil {
		return err
	}
	return pw.Close()
}

// Writer converts a document to polyglot markup like [Convert], one token at
// a time: the caller reads the document with its own [htmlstream.Reader],
// with the options it needs (e.g., [htmlstream.Reader.SetXMLSyntax], which
// the Reader of Convert takes from Options.XMLSyntax), and passes the tokens
// to WriteToken, leaving out those it wants removed.
// Warnings have the positions of the tokens in the caller's input.
//
// The tokens must be from one Reader in [htmlstream.HTML] mode, in order.
// Tokens read in [htmlstream.Universal] mode aren't accepted, also in the
// documents it reads like in HTML mode with XML syntax, since it reads the
// processing instructions and doctypes after an XML declaration like XML
// mode. The tokens left out must keep the stream nested like the Reader's: an
// element's start tag and end tag (explicit or implied) are both written or
// both left out, and the parts of text, comments and other tokens split into
// multiple tokens (see [htmlstream.Token.Partial]) are all written or all
// left out. Leaving out an element's start and end tags keeps its contents
// (like [htmlstream.Transformer.Unwrap]), and leaving out all of its tokens
// removes it. The tags of an element which sets the rules for its contents
// (svg and math, and the integration points in them, like foreignObject) can
// only be left out with the contents. WriteToken and Close return an error
// wrapping [ErrInvalidOp] for tokens breaking these rules: a token out of
// order or written twice, an end tag whose start tag was left out, a start
// tag whose end tag was left out (found at the end tag of the enclosing
// element, or at Close), a token continuing one left out or not continued, an
// element in SVG or MathML content whose root element was left out, an HTML
// element in foreign content whose integration point was left out, and a
// token not read in HTML mode. Other edits of the input (renames, attribute
// edits, inserted markup) aren't supported, since they would need the
// Reader's rules applied again (e.g., renaming a span containing a div to p
// would write <p><div>, which HTML parsers restructure); attribute edits,
// renames (restricted to elements HTML parsers handle the same way), removals
// and inserted markup are done on the output, through the hooks (see below).
//
// Implied end tags are converted with the token after them, and the elements
// closed by implied end tags before a body or html end tag are closed after
// the whitespace and comments after the end tag (see [Convert]): the hooks
// are called, the output written and the errors for the tokens returned when
// they are converted.
//
// When the conversion fails (see [Writer.Err]), the output is incomplete:
// the destination has the output written before the error, which can end
// anywhere (e.g., in a tag), and Close doesn't flush the rest.
//
// The conversion relies on the Reader's element model for the tokens it is
// given: the structure written is the Reader's, without the start and end
// tags left out, and the warnings about HTML parsing differences are for that
// structure. Removing an element with its contents keeps the structure of
// the rest consistent, and the warnings take the elements left out into
// account (e.g., a tr whose table was left out is reported as outside a
// table). Leaving out only the tags of an element is consistent when its
// contents are parsed the same without it, which isn't checked beyond the
// namespace rules above: e.g., leaving out the b in <option><b><option>
// gives <option><option>, where HTML parsers close the first option and
// the Reader's structure has the second one inside it.
//
// With scripting enabled in the Reader ([htmlstream.Reader.SetScripting]),
// noscript contents are raw text, which is written unchanged if it has no
// <, & or ]]>, so that HTML parsers with scripting disabled and XML parsers
// read it the same, and otherwise as escaped text, with a warning. Like
// script and style contents, it is buffered until the end tag to decide
// this (see [Options.MaxRawText]).
//
// With XML syntax ([htmlstream.Reader.SetXMLSyntax]), self-closing tags of
// non-void HTML elements are written as empty elements, a void element
// followed by its end tag (e.g., <br></br>, also with whitespace in between)
// as one element, and other end tags of void elements (e.g., after a
// comment) as HTML parsers read them, with a warning, CDATA sections in HTML
// content as text (with a warning for one reaching the end of the input,
// whose text is the rest of the input, while HTML parsers end it at the
// first >), and processing instructions (which the Reader ends at the
// first >, like HTML parsers end a bogus comment) as comments: the current
// HTML Standard reads most processing instructions (<?target data>) as
// processing instructions, while polyglot writes them as comments, which all
// HTML parsers (older ones too) and XML parsers read the same. A first
// doctype with an internal subset is read to the end of the subset, past a >
// in it, where HTML parsers end the doctype, and the declarations in it
// aren't applied, with a warning for each.
//
// The Reader reads the contents of script, style, title and textarea as text
// with XML syntax too, with the markers of CDATA sections in them: a CDATA
// section around all of the contents (with only whitespace around it), as
// XHTML writes it, is removed, leaving its contents, which XML parsers read
// (see [htmlutil.CDATABare]; not in scripts of types other than JavaScript
// and JSON), and other CDATA sections there are kept as text, with a
// warning. Like script and style contents, title and textarea contents
// starting with a CDATA section (after whitespace) are buffered until the
// end tag for this, and the whitespace at the start of other title and
// textarea contents until the text after it. Outside CDATA sections,
// character references and comments in JavaScript, JSON and CSS contents,
// which XML parsers read as markup (e.g., they decode &amp;), are kept as
// text, as HTML parsers read them, with a warning. A newline directly after
// a pre, textarea or listing start tag, which XML parsers keep, is removed
// like HTML parsers remove it, with a warning, also at the start of a CDATA
// section directly after a pre, textarea or listing start tag. See [Convert]
// for which documents need XML syntax.
//
// The output can be edited without parsing it again: [Options.OnTag] and
// [Options.OnText] report the element tags and text nodes as written (the
// structure HTML parsers give the input, with the elements the conversion
// adds), and the methods of the events insert markup around the tags, edit
// attributes, rename and leave out elements and replace the text (see [Tag]
// and [Text]). Hold, HoldRelease and HoldDiscard hold back the output, so a
// hook can decide what to write before the held output once it has seen what
// follows, and Fail makes the conversion fail. The hooks must not call
// WriteToken or Close.
//
// [Options.MaxDepth] limits the depth of the output, and the Reader's
// maximum depth ([htmlstream.Reader.SetMaxDepth]) that of the input; see
// [Convert] for the time a token takes and the size of the output.
//
// A Writer isn't safe for concurrent use, and converts a single document.
type Writer struct {
	p polyglot
}

// NewWriter returns a Writer writing to w.
func NewWriter(w io.Writer, opt Options) *Writer {
	pw := &Writer{}
	p := &pw.p
	p.pw = pw
	p.opt = opt
	p.cw = &countWriter{w: w}
	p.w = bufio.NewWriter(p.cw)
	p.removeAt, p.pClose, p.keptP = -1, -1, -1
	p.tagEv.p, p.textEv.p = p, p
	in := &p.initial
	p.buf, p.before, p.after = in.buf[:0], in.insert[:0:128], in.insert[128:128]
	p.attrs, p.seen = in.attrs[:0], in.seen[:0]
	if p.opt.Doctype == "" {
		p.opt.Doctype = "<!DOCTYPE html>"
	}
	if p.opt.MaxRawText == 0 {
		p.opt.MaxRawText = DefaultMaxRawText
	}
	if p.opt.MaxHold == 0 {
		p.opt.MaxHold = htmlstream.DefaultMaxHold
	}
	if p.opt.MaxDepth == 0 {
		p.opt.MaxDepth = htmlstream.DefaultMaxDepth
	}
	p.opt.MaxDepth = max(p.opt.MaxDepth, 3)
	if strings.Trim(p.opt.Indent, " \t") != "" || strings.Trim(p.opt.IndentPrefix, " \t") != "" {
		p.fail("Indent and IndentPrefix must contain only spaces and tabs")
	}
	switch p.opt.XMLDecl {
	case AddXMLDecl:
		p.w.WriteString(xmlDeclaration + "\n")
		p.lay.line, p.lay.depth = p.layout(), 0
		p.declOut = true
	case KeepXMLDecl:
		// written before the output held back, if the input has one
		p.prolog = true
		p.cw.holding, p.cw.limit = true, p.opt.MaxRawText
	}
	switch p.opt.DoctypeMode {
	case ReplaceDoctype:
		if p.lay.line {
			p.lay.line = false
			p.w.WriteString(p.opt.IndentPrefix)
		}
		p.w.WriteString(p.opt.Doctype)
		p.w.WriteByte('\n')
		p.lay.line, p.lay.depth = p.layout(), 0
		p.cw.free = len(p.opt.Doctype) + 1 // not counted for MaxRawText
		var d htmlutil.Document
		r := htmlstream.NewReader(strings.NewReader(p.opt.Doctype))
		for r.Next() {
			d.Update(r.Token())
		}
		p.quirksOut = d.Quirks()
	case KeepDoctype, NoDoctype:
		// a kept doctype selects the mode of the input (see keepDoctype),
		// and no doctype selects quirks mode
		p.quirksOut = true
	}
	return pw
}

// xmlDeclaration is the XML declaration written with AddXMLDecl and
// KeepXMLDecl.
const xmlDeclaration = `<?xml version="1.0" encoding="UTF-8"?>`

// WriteToken converts the next token (see [Writer] for the tokens which can
// be left out). Errors are sticky.
func (pw *Writer) WriteToken(tok *htmlstream.Token) error {
	p := &pw.p
	if p.err != nil {
		return p.err
	}
	if p.done {
		return p.fail("token written after Close")
	}
	if p.hook != noHook {
		return p.fail("token written from a hook")
	}
	if err := p.check(tok); err != nil {
		return err
	}
	if p.opt.Strict && tok.Unsound() {
		p.err = tok.UnsoundError()
		return p.err
	}
	p.queueToken(tok)
	return p.err
}

// queueToken converts tok, or holds it back: implied end tags are held
// back until the next token, and if it is a body or html end tag, which
// doesn't close the elements for HTML parsers, until the next token other
// than text of whitespace and NUL, comments, and body and html end tags,
// which are converted inside the elements, where HTML parsers put them.
func (p *polyglot) queueToken(tok *htmlstream.Token) {
	k := tok.Kind()
	switch {
	case k == htmlstream.EndTag && tok.Implied():
		if p.keptByEnd {
			p.flushImplied()
		}
		p.implied.Append(tok)
		return
	case p.implied.Len() == 0:
	case k == htmlstream.EndTag && (tok.Is("body") || tok.Is("html")) && (tok.Stray() && p.keptByEnd || tok.NS() == htmlstream.NamespaceXHTML && (p.keptByEnd || p.closesKept())):
		// (a stray one after the first one, which HTML parsers ignore)
		p.implied.Append(tok)
		p.keptByEnd = true
		return
	case p.keptByEnd && (k == htmlstream.Comment || k == htmlstream.ProcInst):
		p.convert(tok)
		return
	case p.keptByEnd && k == htmlstream.Text:
		// held back until it is known to be whitespace or NUL (up to
		// maxSpaceHold bytes): in the elements, HTML parsers ignore NUL in
		// HTML content and replace it with U+FFFD in foreign content, as
		// converting it in them does
		p.afterEnd.Append(tok)
		p.sbuf = tok.AppendText(p.sbuf[:0])
		blank := !slices.ContainsFunc(p.sbuf, func(c byte) bool { return c != 0 && !ascii.IsSpace(c) })
		p.afterEndText = p.afterEndText || !blank || p.afterEnd.Size() > maxSpaceHold
		if !p.afterEndText && tok.Partial() {
			return
		}
		if p.afterEndText {
			p.flushImplied()
		}
		for t := range p.afterEnd.All() {
			p.convert(t)
		}
		p.afterEnd.Reset()
		p.afterEndText = false
		return
	}
	p.flushImplied()
	p.convert(tok)
}

// closesKept reports whether the implied end tags held back close an
// element other than body and html, which HTML parsers keep open after a
// body or html end tag.
func (p *polyglot) closesKept() bool {
	for t := range p.implied.All() {
		if !t.Is("body") && !t.Is("html") {
			return true
		}
	}
	return false
}

// flushImplied converts the implied end tags held back by queueToken, and
// the body and html end tags after them.
func (p *polyglot) flushImplied() {
	for t := range p.implied.All() {
		p.convert(t)
	}
	p.implied.Reset()
	p.keptByEnd = false
}

// convert converts tok.
func (p *polyglot) convert(tok *htmlstream.Token) {
	if p.err != nil || p.checkNesting(tok) != nil {
		return
	}
	p.token(tok)
	if !tok.Implied() {
		p.closed = closed{names: p.closed.names[:0]}
	}
}

// Close ends the document: it writes the end tags of the elements still open
// (and the elements HTML parsers create at the end, like the body) and
// flushes the output. It returns an error if an element's start tag was
// written without its end tag, or the last token continues in a token which
// wasn't written. On error (also before Close), it returns the error
// without flushing the output, which is incomplete (see [Writer]). Calling
// Close again returns the same result, and methods called after Close (event
// methods, Fail, and WriteToken, which returns its own error) don't change
// the output or the result.
func (pw *Writer) Close() error {
	p := &pw.p
	if p.done {
		return p.closeErr
	}
	if p.err != nil {
		return p.err
	}
	if p.hook != noHook {
		return p.fail("Close called from a hook")
	}
	p.done = true
	p.closeErr = p.close()
	return p.closeErr
}

// close ends the document for Close.
func (p *polyglot) close() error {
	if p.partial {
		return p.fail("Close called without the rest of the last token (the caller left out its continuation)")
	}
	if p.flushImplied(); p.err != nil {
		return p.err
	}
	if f, ok := p.frames.Top(); ok {
		return p.fail("Close called with " + f.name + " open (the caller wrote its start tag but left out its end tag)")
	}
	if p.prolog {
		p.endProlog()
	}
	if p.charsetWait {
		p.placeCharset(nil)
	}
	p.structure(p.doc.End())
	if p.opt.FinalNewline {
		p.bodySpace.cutNewline() // written after the html end tag instead
	}
	p.bodySpace.write(p)
	for len(p.out) > 0 {
		p.popOut()
	}
	p.endText()
	if p.opt.FinalNewline {
		p.emitString("\n")
	}
	p.holds = p.holds[:0]
	p.flushHeld()
	if p.err == nil {
		p.err = p.w.Flush()
	}
	return p.err
}

// Hold starts holding back the output, like [htmlstream.Transformer.Hold]:
// the output from here on is written to a buffer until the hold ends with
// [Writer.HoldRelease] or [Writer.HoldDiscard]. The output held back is
// limited to [Options.MaxHold]; to give up on a hold at a smaller size, end
// it when [Writer.Held] gets too long. Called from [Options.OnTag], the hold
// starts after the tag and the markup inserted after it; called from
// [Options.OnText], it starts with the text (or its replacement). Holds
// started for the charset meta element start after the markup inserted after
// the head start tag, like those started for the head start tag. A hold
// started in a Tag hook can end in the same hook: HoldRelease holds back
// nothing, and HoldDiscard writes raw after the tag and the markup inserted
// after it. Holds still open at Close are released.
//
// A hold started while another is open is inside it, and must end first: its
// output then stays in the enclosing hold. HoldRelease and HoldDiscard of a
// hold with an open hold inside it fail with an error wrapping
// [ErrInvalidOp], and so do those of the zero Hold and of a hold of another
// Writer. Those of a hold which has ended return
// [htmlstream.ErrHoldReleased], after which the conversion continues.
//
// After an error, and once Close has returned, Hold returns the zero Hold.
// The errors of HoldRelease and HoldDiscard other than ErrHoldReleased are
// sticky, and also returned by WriteToken and Close, so a hook calling them
// doesn't need to report them.
func (pw *Writer) Hold() Hold {
	p := &pw.p
	if p.err != nil || p.done && p.hook == noHook {
		// after an error, or after Close (but not from the hooks it calls)
		return Hold{}
	}
	if p.holdID == 0 {
		p.holdID = holdIDs.Add(1)
	}
	p.holdSeq++
	if p.hook == tagHook {
		// started after the tag (see startPending)
		p.holds = append(p.holds, hold{len(p.pendRaw), p.holdSeq})
		p.pendHolds++
	} else {
		p.holds = append(p.holds, hold{len(p.hbuf), p.holdSeq})
	}
	return Hold{p.holdID, p.holdSeq}
}

// A Hold identifies a hold started by [Writer.Hold], for
// [Writer.HoldRelease], [Writer.HoldDiscard] and [Writer.Held]. It is only
// valid with the Writer which returned it. The zero Hold is no hold.
type Hold struct {
	owner uint64 // polyglot.holdID
	seq   uint64 // hold.seq
}

// holdIDs numbers the Writers which have started a hold, from 1, so a Hold
// can't be used with another Writer.
var holdIDs atomic.Uint64

// hold is the start of the output held back by a call to Hold.
type hold struct {
	off int    // in hbuf, or in pendRaw for the pending holds
	seq uint64 // numbers the holds of the Writer, increasing up the stack
}

// Written returns the number of bytes written to the output so far,
// including those held back.
func (pw *Writer) Written() int64 { return pw.p.written() }

// Fail makes the conversion fail with err, for errors of the hooks: it is
// sticky, and returned by WriteToken and Close (and by the current one, if
// called from a hook). An earlier error is kept, and a nil err does nothing.
func (pw *Writer) Fail(err error) {
	if err != nil {
		pw.p.setErr(err)
	}
}

// Err returns the error which failed the conversion so far, the one
// WriteToken and Close return, or nil; after Close, the result of Close.
// Hooks can use it to stop early after an event method failed the
// conversion.
func (pw *Writer) Err() error {
	if pw.p.done {
		return pw.p.closeErr
	}
	return pw.p.err
}

// Holding returns the number of holds which haven't ended.
func (pw *Writer) Holding() int { return len(pw.p.holds) }

// Held returns the output held back by the hold h so far, including that of
// the holds inside it, or nil if h isn't open. It is valid until the next
// write or until a hold ends.
func (pw *Writer) Held(h Hold) []byte {
	p := &pw.p
	i := p.holdAt(h)
	switch {
	case i < 0:
		return nil
	case i >= len(p.holds)-p.pendHolds:
		return p.pendRaw[p.holds[i].off:]
	}
	return p.hbuf[p.holds[i].off:]
}

// HoldRelease ends the hold h, writing the output held back (or keeping it
// in the enclosing hold). See [Writer.Hold] for the errors.
func (pw *Writer) HoldRelease(h Hold) error {
	p := &pw.p
	i, err := p.holdEnd("HoldRelease", h)
	if err != nil {
		return err
	}
	p.holds = p.holds[:i]
	if p.pendHolds != 0 {
		p.pendHolds--
		return nil
	}
	p.flushHeld()
	return nil
}

// HoldDiscard ends the hold h, writing raw (which may be nil) instead of the
// output held back. Markup is inserted before the held output with
// HoldDiscard(h, append(markup, pw.Held(h)...)). If a hold encloses h, raw
// is held back in it, and counts toward [Options.MaxHold]. See [Writer.Hold]
// for the errors.
func (pw *Writer) HoldDiscard(h Hold, raw []byte) error {
	p := &pw.p
	i, err := p.holdEnd("HoldDiscard", h)
	if err != nil {
		return err
	}
	off := p.holds[i].off
	p.holds = p.holds[:i]
	if p.pendHolds != 0 {
		p.pendHolds--
		p.pendRaw = append(p.pendRaw[:off], raw...)
		return nil
	}
	p.hbuf = append(p.hbuf[:off], raw...)
	if len(p.holds) != 0 {
		p.checkHold()
		return p.err
	}
	p.flushHeld()
	return nil
}

// holdAt returns the index of the open hold h in p.holds, or -1.
func (p *polyglot) holdAt(h Hold) int {
	if h.owner != p.holdID {
		return -1
	}
	if n := len(p.holds); n != 0 && p.holds[n-1].seq == h.seq {
		return n - 1
	}
	// the seqs increase up the stack
	if i, ok := slices.BinarySearchFunc(p.holds, h.seq, func(x hold, seq uint64) int { return cmp.Compare(x.seq, seq) }); ok {
		return i
	}
	return -1
}

// holdEnd returns the index in p.holds of the hold h for the operation op,
// HoldRelease or HoldDiscard, checking that it is the innermost open hold.
func (p *polyglot) holdEnd(op string, h Hold) (int, error) {
	if p.err != nil {
		return 0, p.err
	}
	switch i := p.holdAt(h); {
	case i >= 0 && i == len(p.holds)-1:
		return i, nil
	case i >= 0:
		return 0, p.fail(op + " called with an outer Hold (a hold inside it is open)")
	case h == Hold{}:
		return 0, p.fail(op + " called with the zero Hold")
	case h.owner != p.holdID:
		return 0, p.fail(op + " called with a hold created by a different Writer")
	}
	return 0, htmlstream.ErrHoldReleased
}

// checkHold fails if the output held back exceeds MaxHold.
func (p *polyglot) checkHold() {
	if len(p.hbuf) > p.opt.MaxHold && p.err == nil {
		p.err = &holdLimitError{len(p.hbuf), p.opt.MaxHold}
	}
}

// holdLimitError is the error for held output of n bytes, exceeding MaxHold,
// wrapping htmlstream.ErrHoldLimit.
type holdLimitError struct{ n, limit int }

func (e *holdLimitError) Error() string {
	return fmt.Sprintf("polyglot: held output of %d bytes exceeds MaxHold (%d bytes)", e.n, e.limit)
}

func (e *holdLimitError) Unwrap() error { return htmlstream.ErrHoldLimit }

// removing reports whether the output is inside a removed element (see
// Tag.Remove): nothing is written, and the hooks aren't called.
func (p *polyglot) removing() bool {
	return p.removeAt >= 0 && len(p.out) > p.removeAt
}

// flushHeld writes the output held back once no hold is open.
func (p *polyglot) flushHeld() {
	if len(p.holds) == 0 && len(p.hbuf) != 0 {
		p.w.Write(p.hbuf)
		p.hbuf = p.hbuf[:0]
	}
}

// emit writes markup or text to the output, or to the output held back, or
// nowhere inside a removed element. All output goes through it, emitString,
// markup or tag.
func (p *polyglot) emit(b []byte) {
	if p.removing() {
		return
	}
	if p.lay.line && len(b) != 0 && b[0] != '\n' {
		p.lineStart()
	}
	if len(p.holds) > p.pendHolds {
		p.hbuf = append(p.hbuf, b...)
		p.checkHold()
		return
	}
	p.w.Write(b)
}

func (p *polyglot) emitString(s string) {
	if p.removing() {
		return
	}
	if p.lay.line && s != "" && s[0] != '\n' {
		p.lineStart()
	}
	if len(p.holds) > p.pendHolds {
		p.hbuf = append(p.hbuf, s...)
		p.checkHold()
		return
	}
	p.w.WriteString(s)
}

// markup writes markup other than element tags (comments, the doctype),
// ending the text node before it.
func (p *polyglot) markup(b []byte) {
	p.endText()
	p.emit(b)
}

// tag writes the element tag b described by e, ending the text node before it
// and calling the Tag hook. A start tag gets the next ID (a reopened one has
// the ID of the element), kept with the element by pushOut with the namespace
// reported, the name written and whether the tag was left out. Local is
// derived from Name, and the namespace of a prefixed start tag name is
// resolved with the declarations in scope (the end tag is given the namespace
// reported for the start tag). A void start tag is followed by its end tag
// event. Inside a removed element, nothing is written and the hook isn't
// called. A start tag whose attributes were edited is in p.buf after the hook.
func (p *polyglot) tag(b []byte, e Tag) {
	p.endText()
	if e.Start && !e.Reopened {
		p.nextID++
		e.ID = p.nextID
	}
	if e.Start {
		p.startNS, p.startID, p.startOmit, p.startName = e.NS, e.ID, false, e.Name
	}
	if p.removing() {
		return
	}
	if p.opt.OnTag == nil {
		if p.layout() && !(e.Void && !e.Start) {
			p.layoutTag(e.Name, e.NS, e.Start, e.Void)
		}
		p.emit(b)
		if e.fixed && e.Start {
			p.charsetEnd = p.written()
		}
		return
	}
	e.Local = e.Name
	if prefix, local, ok := strings.Cut(e.Name, ":"); ok {
		e.Local = local
		if e.Start {
			e.NS, _ = p.lookupPrefix(prefix)
			p.startNS = e.NS
		}
	}
	if !e.Start && p.removeAt == len(p.out) {
		// the end tag of the removed element
		p.removeAt = -1
		e.omit = true
	}
	e.Depth = len(p.out)
	e.p = p
	e.w = tagInfo{e.Name, e.Local, e.NS, e.Start, e.Reopened, e.Token != nil}
	e.before, e.after = p.before[:0], p.after[:0]
	ev := &p.tagEv
	*ev = e
	p.hook = tagHook
	p.opt.OnTag(ev)
	p.hook = noHook
	p.before, p.after = ev.before[:0], ev.after[:0]
	if ev.edited {
		b = p.buf
	}
	if p.layout() && !ev.omit && !(e.Void && !e.Start) {
		p.layoutTag(ev.w.name, e.NS, e.Start, e.Void)
	}
	p.emit(ev.before)
	if !ev.omit {
		p.emit(b)
	}
	switch {
	case e.fixed && e.Start:
		p.charsetEnd = p.written()
		p.emit(ev.after)
	case p.charsetNext && e.Start:
		// the head start tag: the charset meta element is written first
		// (see writeCharset)
		p.headAfter = append(p.headAfter[:0], ev.after...)
	default:
		p.emit(ev.after)
	}
	if !p.charsetNext && (p.pendHolds != 0 || len(p.pendRaw) != 0) {
		p.startPending()
	}
	if e.Start {
		p.startOmit, p.startName = ev.omit, ev.w.name
		if ev.remove && !e.Void {
			// the element is pushed next
			p.removeAt, p.removeID = len(p.out), e.ID
		}
	}
	if e.Void && e.Start {
		e.Start, e.Token, e.Name, e.Local = false, nil, ev.w.name, ev.w.local
		p.tag(nil, e)
	}
}

// startPending starts the holds started in the Tag hook (see Writer.Hold)
// and writes the markup of those discarded in it, after the tag.
func (p *polyglot) startPending() {
	for i := len(p.holds) - p.pendHolds; i < len(p.holds); i++ {
		p.holds[i].off += len(p.hbuf)
	}
	p.pendHolds = 0
	p.emit(p.pendRaw)
	p.pendRaw = p.pendRaw[:0]
}

// tagString is tag for a constant tag, which has no attributes to edit.
func (p *polyglot) tagString(s string, e Tag) {
	p.buf = append(p.buf[:0], s...)
	p.attrs = p.attrs[:0]
	p.tag(p.buf, e)
}

// htmlTag returns the Tag of a tag of the HTML element name added by the
// conversion.
func htmlTag(name string, start bool) Tag {
	return Tag{Start: start, Name: name, NS: htmlstream.NamespaceXHTML}
}

// endText ends the text node being written, if any, with the Text hook's
// Last call.
func (p *polyglot) endText() {
	if !p.textOpen {
		return
	}
	p.textOpen = false
	if p.opt.OnText != nil {
		if ev := p.callText(Text{Last: true}); ev.set {
			if ev.isText {
				p.raw = AppendEscapeText(p.raw[:0], ev.data)
				p.emit(p.raw)
			} else {
				p.emit(ev.data)
			}
		}
	}
}

// callText calls the Text hook with the event t, and returns the event, with
// the replacement set by the hook, if any. The element the text is in is the
// innermost one in p.out (see popOut).
func (p *polyglot) callText(t Text) *Text {
	ev := &p.textEv
	*ev = t
	ev.p, ev.raw = p, t.Raw
	if n := len(p.out); n != 0 {
		ev.ID, ev.Depth = p.out[n-1].id, n
	}
	p.hook = textHook
	p.opt.OnText(ev)
	p.hook = noHook
	return ev
}

// setAttr sets the attribute name of the start tag in p.buf to value (see
// Tag.SetAttr), replacing its value if it has it, or adding it before the end
// of the tag.
func (p *polyglot) setAttr(name string, value []byte) {
	a := append(p.ebuf[:0], ' ')
	a = append(a, name...)
	a = append(a, `="`...)
	a = AppendEscapeAttr(a, value)
	a = append(a, '"')
	p.ebuf = a
	b := p.buf
	if i := slices.IndexFunc(p.attrs, func(a attrRange) bool { return a.name == name }); i >= 0 {
		r := p.attrs[i]
		delta := len(a) - (r.end - r.off)
		p.buf = slices.Replace(b, r.off, r.end, a...)
		p.attrs[i].end += delta
		for j := i + 1; j < len(p.attrs); j++ {
			p.attrs[j].off += delta
			p.attrs[j].end += delta
		}
		return
	}
	end := len(b) - 1 // ">"
	if bytes.HasSuffix(b, []byte(" />")) {
		end = len(b) - 3
	}
	p.buf = slices.Insert(b, end, a...)
	p.attrs = append(p.attrs, attrRange{name: name, off: end, end: end + len(a)})
}

// removeAttr removes the attribute name from the start tag in p.buf, if it
// has it.
func (p *polyglot) removeAttr(name string) {
	i := slices.IndexFunc(p.attrs, func(a attrRange) bool { return a.name == name })
	if i < 0 {
		return
	}
	r := p.attrs[i]
	p.buf = slices.Delete(p.buf, r.off, r.end)
	p.attrs = slices.Delete(p.attrs, i, i+1)
	for j := i; j < len(p.attrs); j++ {
		p.attrs[j].off -= r.end - r.off
		p.attrs[j].end -= r.end - r.off
	}
}

// fail records an error wrapping ErrInvalidOp with the message, unless an
// error was recorded before, and returns the error recorded.
func (p *polyglot) fail(msg string) error {
	return p.setErr(fmt.Errorf("%w: %s", ErrInvalidOp, msg))
}

func (p *polyglot) setErr(err error) error {
	if p.err == nil {
		p.err = err
	}
	return p.err
}

// check checks that tok can follow the tokens written (see Writer).
func (p *polyglot) check(tok *htmlstream.Token) error {
	mode, _ := internal.TokenMode(tok)
	switch pos := tok.Pos(); {
	case htmlstream.Mode(mode) != htmlstream.HTML:
		return p.fail(fmt.Sprintf("token at %d not read in HTML mode", pos))
	case p.fed && (pos < p.last || pos == p.last && !tok.Implied() && !p.lastImplied):
		// implied end tags have the position of the token after them
		return p.fail(fmt.Sprintf("token at %d out of order", pos))
	case tok.Continued() != p.partial:
		if p.partial {
			return p.fail(fmt.Sprintf("token at %d doesn't continue the previous one", pos))
		}
		return p.fail(fmt.Sprintf("token at %d continues a token the caller left out", pos))
	}
	p.fed, p.last, p.lastImplied, p.partial = true, tok.Pos(), tok.Implied(), tok.Partial()
	return nil
}

// checkNesting checks that tok keeps the stream nested (see Writer), before
// it is converted (which can be after later tokens are written, see
// queueToken).
func (p *polyglot) checkNesting(tok *htmlstream.Token) error {
	if tok.Kind() == htmlstream.EndTag && !tok.Stray() && tok.StartPos() >= 0 && !p.frames.Closes(tok) {
		pos := tok.Pos()
		if f, ok := p.frames.Top(); ok && tok.StartPos() < f.pos {
			return p.fail(fmt.Sprintf("end tag at %d with %s open inside its element (the caller left out the end tag of %s)", pos, f.name, f.name))
		}
		return p.fail(fmt.Sprintf("end tag at %d without its start tag (the caller left out the start tag at %d)", pos, tok.StartPos()))
	}
	if tok.Kind() == htmlstream.StartTag && !tok.Incomplete() {
		if msg := p.checkNS(tok); msg != "" {
			return p.fail(msg)
		}
	}
	return nil
}

// checkNS checks that the namespace the Reader gives the element opened by
// the start tag tok is the one HTML parsers give an element at this point in
// the output: an element in SVG or MathML content must be inside an element
// in that namespace, unless it is an svg or math element where HTML parsers
// start that content (in HTML content, an integration point, or
// annotation-xml for svg), and an HTML element in foreign content must be in
// an integration point, and not be an mglyph or malignmark directly in a
// MathML text integration point, which HTML parsers read as MathML.
// Otherwise the tags of the element which set the namespace were left out.
// It returns the error message, or an empty string. The element is checked
// against its parent in the Reader's structure as written, so elements
// dropped by the conversion itself (reported by warnings) don't count.
func (p *polyglot) checkNS(tok *htmlstream.Token) string {
	var t *outElem
	if f, ok := p.frames.Top(); ok {
		if f.out < 0 || f.out >= len(p.out) {
			return "" // dropped, or closed by the conversion
		}
		t = &p.out[f.out]
	}
	switch ns := tok.NS(); ns {
	case htmlstream.NamespaceXHTML:
		if t == nil || t.ns == "" {
			return ""
		}
		name := htmlName(tok)
		switch {
		case !t.integration:
			return fmt.Sprintf("HTML element %s at %d in %s content (the caller left out its integration point)", name, tok.Pos(), foreignLabel(t.ns))
		case t.ns == htmlstream.NamespaceMathML && (name == "mglyph" || name == "malignmark"):
			switch t.name {
			case "mi", "mo", "mn", "ms", "mtext":
				return fmt.Sprintf("HTML element %s at %d in a MathML text integration point (the caller left out the element it was in, and HTML parsers will read it as a MathML element when parsing the output)", name, tok.Pos())
			}
		}
	case htmlstream.NamespaceSVG, htmlstream.NamespaceMathML:
		if t != nil && t.ns == ns {
			return ""
		}
		root := "math"
		if ns == htmlstream.NamespaceSVG {
			root = "svg"
		}
		switch {
		case !tok.Is(root):
			return fmt.Sprintf("%s element %s at %d outside %s content (the caller left out its root element)", foreignLabel(ns), p.elemName(tok, ns), tok.Pos(), foreignLabel(ns))
		case t == nil || t.ns == "" || t.integration:
		case ns == htmlstream.NamespaceSVG && t.name == "annotation-xml":
		default:
			return fmt.Sprintf("%s at %d in %s content (the caller left out the element it was in, and HTML parsers will read it as %s element when parsing the output)", root, tok.Pos(), foreignLabel(t.ns), foreignArticle(t.ns))
		}
	}
	return ""
}

// foreignLabel returns "SVG" or "MathML" for the namespace.
func foreignLabel(ns string) string {
	if ns == htmlstream.NamespaceSVG {
		return "SVG"
	}
	return "MathML"
}

// foreignArticle returns foreignLabel(ns) with an indefinite article.
func foreignArticle(ns string) string {
	if ns == htmlstream.NamespaceSVG {
		return "an SVG"
	}
	return "a MathML"
}

type polyglot struct {
	opt Options
	w   *bufio.Writer
	cw  *countWriter // under w
	doc htmlutil.Document

	out     []outElem             // open elements in the output
	frames  htmlutil.Stack[frame] // open elements in the input
	reopens int                   // frames with a start tag to reopen (frame.start)
	markers int                   // open marker elements (td, th, caption, etc.) in the output

	tentative []reopen // formatting elements closed by implied end tags, until the next token, innermost first
	pending   []reopen // formatting elements to reopen before the next phrasing content

	closed closed // elements closed by the implied end tags before the current token

	// an element closed by the implied end tags before an end tag which
	// doesn't close elements for HTML parsers (body, html, or an element
	// they closed before), and the end tag
	keptOpen, keptBy string
	kept             []string // the names of all elements closed before the end tag

	// a noscript after a misplaced head start tag was closed by an implied
	// end tag, which the Reader gives with the head noscript rules
	misplacedNoscript bool

	form     bool  // a form element is open for HTML parsers, which ignore nested forms
	stripNL  bool  // remove a leading newline from the next text (pre, textarea)
	keptNL   bool  // HTML parsers keep that newline in the input (xmp and plaintext, converted to pre)
	preEnd   int64 // output position after the last pre, textarea or listing start tag
	nlWarned bool  // the leading newline of the last pre, textarea or listing was reported
	skip     bool  // drop the rest of a comment split into multiple tokens
	dash     bool  // the last character written in the current comment is '-'
	warned   bool  // a warning was given for the current text
	raw      []byte
	buf      []byte
	sbuf     []byte // for space
	tbuf     []byte // the current text (see textToken)

	// the leading whitespace of the current text, held back (see
	// spaceHold), and whether it was written
	held     []byte
	heldDone bool
	err      error

	// the position of the current token, or of the first token of text, a
	// comment or a CDATA section split into multiple tokens, which warnings
	// about them use
	pos htmlstream.Pos

	// characters not allowed in XML (replacedChars) or invalid UTF-8
	// (replacedUTF8) were replaced in the current text or comment, which is
	// reported after its last token
	replaced replacedKind

	// the start of the current comment, collected until it is long enough
	// to check (see commentToken), and whether it was written
	cstart   []byte
	cstarted bool

	once map[onceKey]bool // keys of the warnings given once (see firstWarning)
	seq  int              // formatting elements opened from the input

	tagDecls []nsDecl      // namespace declarations of the current start tag
	seen     []string      // attribute names written for the current start tag
	expanded []xmlAttrName // their namespaces and local names for XML parsers

	// lookups by name in start tags with more than internal.IndexMin
	// attributes: seen and expanded (see writeStart), the index in tagDecls
	// of the last declaration of each prefix (while tagIndexed), and the sets
	// of boundPrefixes and formattingKey
	seenSet     internal.Map[string, struct{}]
	expandedSet internal.Map[xmlAttrName, struct{}]
	tagIndex    internal.Map[string, int]
	tagIndexed  bool
	boundSet    internal.Map[string, struct{}]
	keySet      internal.Map[string, struct{}]

	// the declarations of the output elements, in order, and once there are
	// more than internal.IndexMin, the index in outRefs of the innermost one
	// of each prefix (see lookupPrefix)
	outRefs  []outRef
	outIndex map[string]int32

	// the index in out of the innermost formatting element with each key
	// hash above the last marker which noahsArk hasn't removed (see
	// outElem.fmtPrev)
	fmtChains map[fmtKey]int32
	fmtSeed   maphash.Seed

	// the start tags, keys and bound prefixes of the formatting elements
	// open in the input (frame.start, outElem.key, frame.bound), removed
	// when they are closed
	starts []byte
	bounds []nsDecl

	// formattingKey's attributes
	kbuf   []byte
	kattrs []attrRange // without name

	// script and style contents and noscript contents read as raw text,
	// collected until their last token (see collectRaw), and title and
	// textarea contents read with XML syntax, collected until it is known
	// that they don't start with a CDATA section, or until their last token
	// (see textToken), with their undecoded data
	rawBuf   []byte
	rawData  []byte
	rawLimit bool // the contents exceed MaxRawText

	// title and textarea contents read with XML syntax not starting with a
	// CDATA section, written token by token (see textToken): the end of
	// their undecoded data, to find the CDATA section markers split across
	// tokens, and whether they have some
	rcStream  bool
	rcTail    []byte
	rcMarkers bool

	// the output before the root element is held back (see KeepXMLDecl)
	prolog  bool
	decl    bool           // the input has an XML declaration before the root element (see xmlDecl)
	declPos htmlstream.Pos // of the first one
	declOut bool           // the output starts with an XML declaration

	// output held back (see Writer.Hold)
	holds     []hold
	hbuf      []byte
	pendHolds int    // the holds on top started in the current Tag hook (see charsetNext)
	pendRaw   []byte // markup written after the tag by HoldDiscard of pending holds
	holdID    uint64 // identifies the Writer in its Holds (0 before the first one)
	holdSeq   uint64 // the seq of the last Hold

	// the hooks (see Writer)
	pw       *Writer
	hook     uint8       // the hook being called (tagHook, textHook) or noHook
	tagEv    Tag         // the event of the current Tag hook call
	textEv   Text        // the event of the current Text hook call
	before   []byte      // buffers for the markup inserted around a tag
	after    []byte      //
	attrs    []attrRange // the attributes of the start tag in buf, for edits
	ebuf     []byte      // attribute edits
	textOpen bool        // text was written, and no markup since (see endText)
	nextID   int         // the ID of the last start tag
	removeAt int         // index in out of the element being removed, or -1
	removeID int         // its ID

	// p elements HTML parsers keep open around a table in quirks mode (see
	// keepP): the index in out of a p closed by an implied end tag, closed
	// at the next token unless it is a table start tag, and of the innermost
	// p kept open after its implied end tag, with the number of input
	// elements open around it, or -1; keptOuter holds keptP and keptDepth
	// of the kept p elements around it
	pClose    int
	keptP     int
	keptDepth int
	keptOuter [][2]int
	quirksOut bool // the output is in quirks mode for HTML parsers if the input is

	// the head start tag is written, and the charset meta element comes
	// next (after the whitespace at the start of the head, see
	// placeCharset), before the markup inserted after the head start tag
	// and the holds started in the hooks of both (see writeCharset)
	charsetNext bool
	headAfter   []byte // the markup inserted after the head start tag
	charsetEnd  int64  // the output written up to the end of the charset meta element

	// the charset meta element waits for the whitespace at the start of the
	// head (headSpace), to be written in place of a charset meta element of
	// the input after it (see placeCharset)
	charsetWait bool
	headSpace   heldSpace
	headPos     htmlstream.Pos // the position of the head start tag, or of the token implying the head

	// the whitespace and comments after the head end tag, written after it
	// when the body starts (see closeHead), or in the head once they exceed
	// maxAfterHead (afterHeadFull)
	afterHead     heldSpace
	afterHeadFull bool

	// implied end tags held back until the next token, followed by body and
	// html end tags if keptByEnd (see queueToken), and the text after them,
	// held back while it is whitespace (afterEndText is set once it isn't)
	implied      htmlstream.TokenBuffer
	keptByEnd    bool
	afterEnd     htmlstream.TokenBuffer
	afterEndText bool

	// with FinalNewline, the whitespace directly in the body, until the next
	// token, for a line break at its end (see bodySpaceToken)
	bodySpace heldSpace

	// with Indent and IndentPrefix (see layoutTag)
	lay struct {
		edge   bool      // the output is at a block boundary
		start  bool      // the boundary is a start tag
		hidden bool      // edge before the hidden element open (see layoutKind)
		held   heldSpace // whitespace and comments at the boundary, until the next token
		line   bool      // the next output starts a line (see lineStart), with depth levels of indentation
		depth  int32
	}

	// the last start tag event, kept with the element by pushOut
	startNS   string // the namespace reported
	startName string // the name written (see Tag.Rename)
	startID   int
	startOmit bool // the tag wasn't written (Unwrap, Remove)
	tfirst    bool // the start tag is the first child element of a template (see templateChild)

	// token stream checks (see Writer)
	fed         bool           // a token was written
	last        htmlstream.Pos // position of the last token
	lastImplied bool           // the last token is an implied end tag
	partial     bool           // the last token continues in the next one
	done        bool           // closed
	closeErr    error          // the result of Close, once done

	// the initial storage of buf, before, after, attrs and seen, which most
	// documents don't outgrow (see NewWriter)
	initial struct {
		buf    [512]byte
		insert [256]byte
		attrs  [8]attrRange
		seen   [8]string
	}
}

// onceKey identifies a warning given once (see warnOnce).
type onceKey struct {
	kind, prefix, ns string
}

// attrRange is an attribute written in a start tag: its name and the range
// of its bytes (from the space before the name to the closing quote) in the
// tag.
type attrRange struct {
	name     string
	off, end int
}

// xmlAttrName is the namespace and local name XML parsers give a prefixed
// attribute.
type xmlAttrName struct {
	ns, local string
}

// nsDecl is a namespace declaration of a start tag in the output.
type nsDecl struct {
	prefix, uri string
	dropped     bool // invalid in XML, so not written: the prefix is unbound
	added       bool // not from the input (xlink), written after the attributes
}

// outRef is a declaration of an output element, p.out[elem].decls[decl], in
// polyglot.outRefs.
type outRef struct {
	elem, decl int32
	prev       int32 // with outIndex, the index in outRefs of the previous declaration of the prefix, or -1
}

// closed describes the elements closed by the implied end tags before a
// token.
type closed struct {
	// HTML elements: the first one, the first special (block-level) one, and
	// the first one whose end tag can't be omitted
	html, special, unsound string
	names                  []string // the names of all HTML elements
	first                  string   // the first element, in any namespace
	p                      bool     // a p element
	reopened               bool     // reopened formatting elements
}

type outElem struct {
	ctx         outCtx
	name        string // the name the conversion's rules use (see written)
	synthetic   bool   // not from the input
	reopened    bool   // formatting element reopened after a misnested end tag
	marker      bool
	ns          string
	integration bool   // SVG or MathML integration point, whose contents follow the HTML rules
	start       []byte // start tag of a reopened element, to reopen it again
	hookNS      string // the namespace reported for its start tag event
	written     string // the name written, which Tag.Rename changes
	id          int    // its ID for the Tag hook (see Tag)
	unwrap      bool   // the tags aren't written (Tag.Unwrap, Remove)
	level       int    // p.markers when a reopened element was first opened
	key         []byte // formatting elements: name and attributes (see noahsArk), in p.starts for elements from the input
	seq         int    // formatting elements: order in the input, kept when reopened
	noah        bool   // removed from the active formatting elements by noahsArk
	fmt         fmtKey // formatting elements: see noahsArk
	fmtPrev     int32  // formatting elements: the index in p.out of the previous one in p.fmtChains, or -1
	tmode       byte   // template: the kind of contents (see templateChild)
	tfirst      string // template: the first child element which set tmode
	decls       []nsDecl
	bound       []nsDecl // reopened formatting elements: see boundPrefixes
}

// outCtx describes the output elements open at and below an output element,
// for the checks of the open elements which would otherwise scan them: the
// indices in p.out of the innermost ones of some kinds, or -1, and the
// numbers of some elements.
type outCtx struct {
	tmpl     int32 // a template, table, caption, colgroup, tbody, thead, tfoot, tr, td or th, or a foreign element (see templateChild)
	a, nobr  int32 // an HTML a or nobr element above the last marker (see closeFormatting)
	reopened int32 // a reopened formatting element above the last marker (see strayFormattingEnd)
	input    int32 // an element which isn't a reopened formatting element

	templates, tables int32 // HTML template and table elements
	implied           int32 // tbody, tr and colgroup elements written by tableFixup
	nreopened         int32 // reopened formatting elements

	// HTML select elements in the default scope of HTML parsers: above the
	// innermost element which limits it (a table cell, an integration
	// point, etc.)
	selects int32

	// with Indent and IndentPrefix: the indentation level of the contents,
	// whether the whitespace in them is kept as it is (see layoutOff), and
	// whether they are in the head (outside a template)
	layDepth int32
	layOff   bool
	layHead  bool
}

// noCtx is the outCtx of the empty output stack.
var noCtx = outCtx{tmpl: -1, a: -1, nobr: -1, reopened: -1, input: -1}

// fmtKey identifies the formatting elements with the same key (see
// formattingKey) above a marker, or a hash collision.
type fmtKey struct {
	hash    uint64
	markers int
}

type frame struct {
	pos        htmlstream.Pos // of the start tag
	out        int            // index in p.out, or -1 if the element wasn't written
	start      []byte         // written start tag of formatting elements (nil if unwrapped: not reopened)
	name       string
	marker     int      // p.markers when the element was opened
	scriptType string   // for script elements
	text       bool     // HTML parsers parse the contents with the text insertion mode (title, textarea, xmp, etc.)
	selfClosed string   // the input name of a raw text or RCDATA element with a self-closing start tag, until its contents (see selfClosedText)
	void       bool     // a void element, opened with XML syntax
	bound      []nsDecl // formatting elements: see boundPrefixes

	// a formatting element: start and the key of its output element are in
	// p.starts and bound in p.bounds, from these offsets
	formatting         bool
	startOff, boundOff int
}

type reopen struct {
	name    string
	written string // see outElem
	start   []byte
	id      int // see outElem
	marker  int
	key     []byte
	seq     int
	implied bool // closed by the Reader's implied end tag (not a reopened element)
	decls   []nsDecl
	bound   []nsDecl // see boundPrefixes
}

func (p *polyglot) warn(pos htmlstream.Pos, format string, args ...any) {
	if p.opt.Warn != nil {
		p.w.Flush() // a write error is sticky, and returned by later writes and Close
		p.opt.Warn(Warning{Pos: pos, Msg: fmt.Sprintf(format, args...)})
	}
}

// firstWarning reports whether the warning with key wasn't given yet, and
// records it as given. It is false if there are no warnings, so callers can
// skip formatting the message.
func (p *polyglot) firstWarning(key onceKey) bool {
	if p.opt.Warn == nil || p.once[key] {
		return false
	}
	if p.once == nil {
		p.once = map[onceKey]bool{}
	}
	p.once[key] = true
	return true
}

func (p *polyglot) token(tok *htmlstream.Token) {
	if !tok.Continued() {
		p.pos, p.replaced = tok.Pos(), 0
	}
	if tok.Kind() == htmlstream.Comment && !tok.Continued() && string(tok.Raw()) == "</>" {
		// HTML parsers ignore it (see htmlstream.Token.Bogus); a Continued
		// token with this Raw is the end of a longer bogus comment
		return
	}
	if p.charsetWait && !p.headSpaceToken(tok) {
		p.placeCharset(tok)
	}
	if p.bodySpace.size() != 0 && !p.bodySpaceToken(tok) {
		p.bodySpace.write(p)
	}
	if p.pClose >= 0 || p.keptP >= 0 {
		p.keepP(tok)
	}
	if k := tok.Kind(); tok.Incomplete() && (k == htmlstream.StartTag || k == htmlstream.EndTag) {
		p.warn(tok.Pos(), "dropped incomplete tag at the end of the input (to match HTML parsers)")
		return
	}
	if k := tok.Kind(); k != htmlstream.Text && k != htmlstream.CDATA {
		p.stripNL = false // HTML parsers drop a newline only directly after the start tag
	}
	if p.keptOpen != "" {
		switch k := tok.Kind(); {
		case k == htmlstream.StartTag, (k == htmlstream.Text || k == htmlstream.CDATA) && !p.space(tok),
			k == htmlstream.EndTag && tok.Stray() && (tok.Is("p") || tok.Is("br")): // written as elements
			p.warn(p.pos, "wrote content after </%s> outside the %s it closed (HTML parsers would have kept %s open and put the content in it)", p.keptBy, p.keptOpen, p.keptOpen)
			p.keptOpen = ""
		// Whitespace after a body or html end tag is written in the
		// elements, like HTML parsers append it to them (see queueToken).
		// Whitespace written after them (after content, or longer than
		// maxSpaceHold) isn't reported: only whitespace differs.
		// case k == htmlstream.Text || k == htmlstream.CDATA:
		// 	if p.firstWarning(onceKey{kind: "space after", prefix: p.keptBy}) {
		// 		p.warn(tok.Pos(), "wrote whitespace after </%s> outside the %s it closed (HTML parsers would have kept %s open and put the whitespace in it)", p.keptBy, p.keptOpen, p.keptOpen)
		// 	}
		case k == htmlstream.EndTag && tok.Stray() && slices.ContainsFunc(p.kept, tok.Is):
			p.warn(tok.Pos(), "applied </%s> after </%s> closed %s (HTML parsers would have kept %s open when applying it)", htmlName(tok), p.keptBy, p.keptOpen, p.keptOpen)
			p.keptOpen = ""
		}
	}
	if !tok.Implied() {
		p.resolveTentative(tok)
	}
	if p.misplacedNoscript && !tok.Implied() {
		// not at the end of the input, where HTML parsers close it too
		p.warn(tok.Pos(), "closed noscript after a misplaced <head> at the content after it (HTML parsers would have kept it open as body content)")
		p.misplacedNoscript = false
	}
	if tok.Kind() == htmlstream.EndTag && !tok.Implied() && (tok.Is("body") || tok.Is("html")) && p.impliedHeadNoscript() {
		// htmlutil.Document starts the body for it
		p.warn(tok.Pos(), "applied </%s> in a noscript in the implied head (HTML parsers would have ignored it)", htmlName(tok))
	}
	initial := !p.doc.Started() // the initial insertion mode, before the doctype
	// The Reader reads the leading whitespace of text before the body as a
	// Text token of its own, which HTML parsers drop before the head or put
	// in the head, but not that of a CDATA section read with XML syntax, so
	// it is written before the structure changes for the rest of the text.
	lead := 0
	if tok.Kind() == htmlstream.CDATA && p.doc.Part() < htmlutil.InBody {
		p.tbuf = tok.AppendText(p.tbuf[:0])
		if i := spaceLen(p.tbuf); i < len(p.tbuf) {
			lead = i
		}
		if lead != 0 {
			p.textToken(tok, 0, lead)
		}
	}
	ev := p.doc.Update(tok)
	if p.prolog && p.doc.Part() != htmlutil.BeforeHTML {
		p.endProlog()
	}
	if ev&htmlutil.MisplacedTag != 0 {
		if tok.Kind() == htmlstream.StartTag && !tok.Is("head") && p.ctx().templates == 0 {
			for range tok.Attrs() {
				// HTML parsers add them to the element (except in a template)
				p.warn(tok.Pos(), "dropped attributes of misplaced <%s> (HTML parsers would have added them to the existing element)", htmlName(tok))
				break
			}
		}
		if tok.Kind() == htmlstream.StartTag && !tok.Is("html") && p.topIs("colgroup", true) {
			// HTML parsers close the colgroup, like the Reader does an
			// explicit one
			p.popOut()
		}
		if t := p.top(); tok.Kind() == htmlstream.StartTag && t != nil && t.name == "template" && t.ns == "" && t.tmode == 0 {
			// HTML parsers parse the template contents as body content, where
			// they ignore it
			t.tmode, t.tfirst = 'b', htmlName(tok)
		}
		p.dropElement(tok)
		return
	}
	p.structure(ev)
	switch tok.Kind() {
	case htmlstream.Doctype:
		if _, xmlSyntax := internal.TokenMode(tok); xmlSyntax {
			// read with an internal subset, which ends at its "]" (and the
			// doctype at the next ">") like in XML parsers, unless it
			// reached the end of the input
			data := tok.Data()
			if bytes.IndexByte(data, '>') >= 0 {
				p.warn(tok.Pos(), "read the doctype past the > in its internal subset (HTML parsers would have ended it there and read the rest as content)")
			}
			if initial && hasInternalSubset(data) {
				p.warn(tok.Pos(), "dropped the internal subset of the doctype without applying its declarations (XML parsers would have applied them, e.g., expanded the entities it declares)")
			}
		}
		if initial && p.opt.DoctypeMode == KeepDoctype {
			p.keepDoctype(tok)
		}
	case htmlstream.Comment:
		p.commentToken(tok)
	case htmlstream.ProcInst:
		// with XML syntax
		p.procInst(tok)
	case htmlstream.Text, htmlstream.CDATA:
		// CDATA sections are in foreign content, or in HTML content with XML
		// syntax
		if t := p.top(); tok.Kind() == htmlstream.CDATA && tok.Incomplete() && (t == nil || t.ns == "") {
			p.warn(p.pos, "read the rest of the input as a CDATA section (HTML parsers would have ended it at the first >)")
		}
		p.textToken(tok, lead, -1)
	case htmlstream.StartTag:
		p.startTag(tok)
	case htmlstream.EndTag:
		p.endTag(tok)
	}
	if p.replaced != 0 && !tok.Partial() {
		p.warnReplaced(p.pos, p.replaced)
		p.replaced = 0
	}
}

// space reports whether a text token is whitespace once decoded, ignoring a
// byte order mark at the start of the input and NUL where HTML parsers
// ignore it (see dropsNUL).
func (p *polyglot) space(tok *htmlstream.Token) bool {
	p.sbuf = tok.AppendText(p.sbuf[:0])
	b := p.sbuf
	if tok.Pos() == 0 {
		b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})
	}
	nul := p.dropsNUL(tok)
	for _, c := range b {
		if !ascii.IsSpace(c) && (c != 0 || !nul) {
			return false
		}
	}
	return true
}

// countWriter counts the bytes written to w, and holds them back while
// holding, up to limit bytes not counting the first free bytes, the markup
// the writer adds (see KeepXMLDecl). A negative limit is exceeded by any
// byte after the free ones.
type countWriter struct {
	w       io.Writer
	n       int64
	holding bool
	hold    []byte
	limit   int
	free    int
}

func (c *countWriter) Write(b []byte) (int, error) {
	c.n += int64(len(b))
	if c.holding {
		if len(c.hold)+len(b)-c.free <= max(c.limit, 0) {
			c.hold = append(c.hold, b...)
			return len(b), nil
		}
		c.holding = false
		if _, err := c.w.Write(c.hold); err != nil {
			return 0, err
		}
		c.hold = nil
	}
	return c.w.Write(b)
}

// written returns the number of bytes written to the output, including those
// held back.
func (p *polyglot) written() int64 { return p.cw.n + int64(p.w.Buffered()) + int64(len(p.hbuf)) }

// xmlDecl handles an XML declaration in the input. It reports whether the
// declaration is removed: the first one before the root element, which
// KeepXMLDecl writes at the start of the output (see endProlog). Others are
// written as comments, like other processing instructions.
func (p *polyglot) xmlDecl(pos htmlstream.Pos) bool {
	if p.decl || p.doc.Part() != htmlutil.BeforeHTML {
		return false
	}
	p.decl, p.declPos = true, pos
	return true
}

// endProlog writes the output held back before the root element (see
// KeepXMLDecl), after the XML declaration if the input has one.
func (p *polyglot) endProlog() {
	p.prolog = false
	if err := p.w.Flush(); err != nil {
		return // reported by Close
	}
	c := p.cw
	if !c.holding {
		// the output exceeded the limit and was written
		if p.decl {
			p.warn(p.declPos, "dropped XML declaration (output before the root element exceeds MaxRawText)")
		}
		return
	}
	// The declaration and the output held back by the counting writer go
	// directly to the output. A hold can be open here (one the caller
	// started outside the hooks, which aren't called before the root
	// element's start tag): its output is in hbuf, written after them.
	c.holding = false
	if p.decl {
		p.w.WriteString(xmlDeclaration + "\n")
		if p.opt.IndentPrefix != "" {
			// the output held back starts on the next line
			if len(c.hold) != 0 {
				p.w.WriteString(p.opt.IndentPrefix)
			} else {
				p.lay.line, p.lay.depth = true, 0
			}
		}
		p.declOut = true
		if err := p.w.Flush(); err != nil {
			return
		}
	}
	if _, err := c.w.Write(c.hold); err != nil && p.err == nil {
		p.err = err
	}
	c.hold = nil
}

// keepDoctype writes the doctype of the input (see KeepDoctype).
func (p *polyglot) keepDoctype(tok *htmlstream.Token) {
	d := internal.ParseDoctype(tok.Data())
	if tok.Incomplete() {
		d.ForceQuirks = true // as in HTML parsers
	}
	out := d
	out.ForceQuirks = false
	if out.HasPublic && !out.HasSystem {
		out.HasSystem = true
	}
	var why string
	switch {
	case !htmlstream.ValidXMLName(out.Name):
		why = fmt.Sprintf("name %q isn't an XML name", out.Name)
	case strings.Trim(out.Public, pubidChars) != "":
		why = fmt.Sprintf("public identifier %q has characters XML doesn't allow", out.Public)
	case strings.Contains(out.System, `"`) && strings.Contains(out.System, "'"):
		why = "system identifier has both kinds of quotes"
	case strings.Contains(out.System, ">"):
		why = "system identifier has >, which ends the doctype in HTML parsers"
	case !htmlstream.ValidXMLChars(out.System):
		why = "system identifier has characters XML doesn't allow"
	case out.QuirksMode() != d.QuirksMode():
		why = fmt.Sprintf("selects %s mode in HTML parsers, and %s mode as written", modeNames[d.QuirksMode()], modeNames[out.QuirksMode()])
	}
	if why != "" {
		p.warn(tok.Pos(), "dropped doctype (%s)", why)
		return
	}
	b := append(p.buf[:0], "<!DOCTYPE "...)
	b = append(b, out.Name...)
	switch {
	case out.HasPublic:
		b = append(b, ` PUBLIC "`...)
		b = append(b, out.Public...)
		b = append(b, `" `...)
		b = appendQuoted(b, out.System)
	case out.HasSystem:
		b = append(b, " SYSTEM "...)
		b = appendQuoted(b, out.System)
	}
	p.buf = append(b, ">\n"...)
	p.markup(p.buf)
	p.lay.line, p.lay.depth = p.layout(), 0
}

// hasInternalSubset reports whether the data of a doctype read with XML
// syntax has an internal subset (after a [ outside the quoted literals,
// ending at a ] at the end) which isn't empty. A doctype whose internal
// subset reached the end of the input ends at the first > in it, so it
// isn't one.
func hasInternalSubset(data []byte) bool {
	var q byte
	for i, c := range data {
		switch {
		case q != 0:
			if c == q {
				q = 0
			}
		case c == '"' || c == '\'':
			q = c
		case c == '[':
			subset, ok := bytes.CutSuffix(ascii.TrimSpace(data[i+1:]), []byte("]"))
			return ok && !ascii.IsBlank(subset)
		}
	}
	return false
}

var modeNames = [...]string{internal.NoQuirks: "no-quirks", internal.LimitedQuirks: "limited-quirks", internal.Quirks: "quirks"}

// pubidChars are the characters XML allows in a public identifier.
const pubidChars = " \r\nabcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-'()+,./:=?;!*#@$_%"

// appendQuoted appends s in double quotes, or single quotes if it contains
// double quotes.
func appendQuoted(b []byte, s string) []byte {
	q := byte('"')
	if strings.Contains(s, `"`) {
		q = '\''
	}
	return append(append(append(b, q), s...), q)
}

// isXMLDecl reports whether the data of a bogus comment (or of a processing
// instruction, without the ">") is an XML declaration: "?xml" followed by
// whitespace, or "?xml?" (<?xml?>), as the Reader recognizes one in
// Universal mode. Other processing instructions, like xml-stylesheet or
// <?xml?a>, aren't.
func isXMLDecl(data []byte) bool {
	return len(data) > 4 && bytes.HasPrefix(data, []byte("?xml")) && (ascii.IsSpace(data[4]) || string(data) == "?xml?")
}

// structure writes the implied html, head and body elements. The head is
// closed when the body starts, since HTML parsers put head content after the
// end of the head in it.
func (p *polyglot) structure(ev htmlutil.DocumentEvent) {
	if ev&htmlutil.ImpliedHTML != 0 {
		p.tagString(`<html xmlns="`+htmlstream.NamespaceXHTML+`">`, htmlTag("html", true))
		p.pushOut(outElem{name: "html", synthetic: true})
	}
	if ev&htmlutil.ImpliedHead != 0 {
		p.charsetNext, p.headPos = true, p.pos
		p.tagString("<head>", htmlTag("head", true))
		p.pushOut(outElem{name: "head", synthetic: true})
		p.writeCharset()
	}
	if ev&htmlutil.ImpliedBody != 0 {
		p.closeHead()
		p.tagString("<body>", htmlTag("body", true))
		p.pushOut(outElem{name: "body", synthetic: true})
	}
}

// closeHead closes the head and anything still open in it.
func (p *polyglot) closeHead() {
	for i := len(p.out) - 1; i >= 0; i-- {
		if p.out[i].name == "head" && p.out[i].ns == "" {
			for len(p.out) > i {
				p.popOut()
			}
			p.afterHead.write(p)
			return
		}
	}
}

// writeCharset writes the charset meta element after the head start tag,
// followed by the markup inserted after the head start tag, and starts the
// holds started in the hooks of both. HTML parsers look for it in the first
// 1024 bytes before parsing (see
// [prescan a byte stream to determine its encoding]).
//
// [prescan a byte stream to determine its encoding]: https://html.spec.whatwg.org/multipage/parsing.html#prescan-a-byte-stream-to-determine-its-encoding
func (p *polyglot) writeCharset() {
	e := htmlTag("meta", true)
	e.Void, e.fixed = true, true
	p.tagString(`<meta charset="UTF-8" />`, e)
	p.charsetNext = false
	p.emit(p.headAfter)
	p.headAfter = p.headAfter[:0]
	if p.pendHolds != 0 || len(p.pendRaw) != 0 {
		p.startPending()
	}
	if p.charsetEnd > 1024 && !p.declOut {
		// without an XML declaration at the start, from which the prescan
		// takes UTF-8 if it finds no meta element
		p.warn(p.headPos, "wrote the charset meta element past the first 1024 bytes (HTML parsers which aren't told the encoding will not find it before parsing the output)")
	}
}

// placeCharset writes the charset meta element and the whitespace at the
// start of the head held back before tok (nil at the end of the input): if
// tok is the start tag of a meta element which is dropped (see
// metaCharsetAttrs), the meta element takes its place after the whitespace,
// and otherwise, it comes first, right after the head start tag.
func (p *polyglot) placeCharset(tok *htmlstream.Token) {
	p.charsetWait = false
	inPlace := false
	if tok != nil && tok.Kind() == htmlstream.StartTag && tok.HTMLRules() && tok.NS() == htmlstream.NamespaceXHTML && tok.Is("meta") && !tok.Incomplete() {
		_, keep := metaCharsetAttrs(tok, htmlstream.NamespaceXHTML)
		inPlace = !keep
	}
	if inPlace {
		p.headSpace.write(p)
		p.writeCharset()
	} else {
		p.writeCharset()
		p.headSpace.write(p)
	}
}

// headSpaceToken reports whether tok is whitespace at the start of the head
// which the charset meta element waits for (see placeCharset), up to
// maxHeadSpace bytes.
func (p *polyglot) headSpaceToken(tok *htmlstream.Token) bool {
	if tok.Kind() != htmlstream.Text || p.doc.Part() != htmlutil.InHead || !p.inHeadTop() {
		return false
	}
	p.sbuf = tok.AppendText(p.sbuf[:0])
	return ascii.IsBlank(p.sbuf) && p.headSpace.size()+len(p.sbuf) <= maxHeadSpace
}

// holdAfterHead holds back whitespace (text of tok) or a comment (markup,
// with a nil tok) after the head end tag, which HTML parsers put after the
// head, while they put head content after it in the head: they are written
// when the body starts. It reports whether b was held back: past
// maxAfterHead bytes, the parts held back are written in the head instead,
// and so are b and the rest.
func (p *polyglot) holdAfterHead(tok *htmlstream.Token, b []byte, first, last bool) bool {
	if p.afterHead.size()+len(b) > maxAfterHead {
		p.afterHeadFull = true
		p.afterHead.write(p)
		return false
	}
	p.afterHead.add(tok, b, first, last)
	return true
}

// bodySpaceToken reports whether tok is whitespace directly in the body held
// back with FinalNewline, in case it ends the body: its last line break is
// then written after the html end tag. Whitespace before which formatting
// elements are reopened isn't.
func (p *polyglot) bodySpaceToken(tok *htmlstream.Token) bool {
	if tok.Kind() != htmlstream.Text || tok.Continued() && p.bodySpace.size() == 0 || p.doc.Part() != htmlutil.InBody || len(p.pending) != 0 {
		// (the rest of a text whose start wasn't whitespace)
		return false
	}
	if t := p.top(); t == nil || t.name != "body" || t.ns != "" {
		return false
	}
	p.sbuf = tok.AppendText(p.sbuf[:0])
	return ascii.IsBlank(p.sbuf)
}

// inHeadTop reports whether the innermost element open in the output is the
// head.
func (p *polyglot) inHeadTop() bool {
	t := p.top()
	return t != nil && t.name == "head" && t.ns == ""
}

// maxHeadSpace is the size of the whitespace at the start of the head held
// back by placeCharset, and maxAfterHead that of the whitespace and comments
// after the head held back by holdAfterHead.
const (
	maxHeadSpace = 256
	maxAfterHead = 64 << 10
)

// heldSpace is whitespace and comments written later. Its memory is reused.
type heldSpace struct {
	toks  htmlstream.TokenBuffer // the tokens of the whitespace
	parts []heldPart
	data  []byte // the whitespace and comments, in order
}

// heldPart is whitespace (text of a token) or a comment (markup, the first
// or last part of a comment split into multiple tokens if first or last
// isn't set), ending at end in data.
type heldPart struct {
	tok         int // the index of the token in toks, or -1 for a comment
	end         int
	first, last bool
}

func (h *heldSpace) size() int { return len(h.data) }

// add holds back the text b of tok, or the markup of a comment if tok is
// nil (first and last are those of commentPart).
func (h *heldSpace) add(tok *htmlstream.Token, b []byte, first, last bool) {
	i := -1
	if tok != nil {
		h.toks.Append(tok)
		i = h.toks.Len() - 1
	}
	h.data = append(h.data, b...)
	h.parts = append(h.parts, heldPart{i, len(h.data), first, last})
}

// all returns the parts held back: the token (nil for a comment) and the
// data.
func (h *heldSpace) all() iter.Seq2[heldPart, []byte] {
	return func(yield func(heldPart, []byte) bool) {
		start := 0
		for _, part := range h.parts {
			if !yield(part, h.data[start:part.end]) {
				return
			}
			start = part.end
		}
	}
}

// comments reports whether h has comments.
func (h *heldSpace) comments() bool {
	return slices.ContainsFunc(h.parts, func(part heldPart) bool { return part.tok < 0 })
}

// reset empties h.
func (h *heldSpace) reset() {
	h.toks.Reset()
	h.parts, h.data = h.parts[:0], h.data[:0]
}

// cutNewline removes a line break at the end of the whitespace, and reports
// whether there was one.
func (h *heldSpace) cutNewline() bool {
	n := len(h.parts)
	if n == 0 || h.parts[n-1].tok < 0 || !bytes.HasSuffix(h.data, []byte("\n")) {
		return false
	}
	h.data = h.data[:len(h.data)-1]
	if h.parts[n-1].end--; n > 1 && h.parts[n-1].end == h.parts[n-2].end || n == 1 && h.parts[0].end == 0 {
		h.parts = h.parts[:n-1]
	}
	return true
}

// write writes the parts held back, and empties h.
func (h *heldSpace) write(p *polyglot) {
	for part, b := range h.all() {
		if part.tok < 0 {
			p.writeComment(b, part.first, part.last)
		} else {
			p.text(h.toks.At(part.tok), b)
		}
	}
	h.reset()
}

// Layout (Options.Indent and Options.IndentPrefix).

// layout reports whether Indent or IndentPrefix is set.
func (p *polyglot) layout() bool { return p.opt.Indent != "" || p.opt.IndentPrefix != "" }

// maxIndent is the number of levels of block-level elements indented.
const maxIndent = 64

// The kinds of elements for the layout.
const (
	layoutInline = iota // whitespace around them is written as it is
	layoutBlock         // block-level: whitespace around them is indentation
	layoutHidden        // not rendered, on a line of its own at a block boundary
)

// layoutKind returns the kind of the element with the name (lowercase for
// HTML elements, html set), in the head if head is set.
func layoutKind(name string, html, head bool) int {
	switch {
	case !html:
		return layoutInline
	case head:
		return layoutBlock
	}
	switch name {
	case "html", "head", "td", "th", "col", "colgroup", "template":
		return layoutBlock
	case "script", "style", "link", "meta", "base", "noscript":
		return layoutHidden
	}
	if htmlutil.IsBlock(htmlutil.HTMLName(name)) {
		return layoutBlock
	}
	return layoutInline
}

// layoutOff reports whether the whitespace in the HTML element with the name
// is written as it is (see Options.Indent).
func layoutOff(name string) bool {
	switch name {
	case "pre", "listing", "textarea", "title", "script", "style", "noscript", "xmp", "plaintext":
		return true
	}
	return false
}

// layoutTag writes the layout before an element tag (not the end tag event
// of a void element) about to be written, with the name written and the
// namespace reported.
func (p *polyglot) layoutTag(name, ns string, start, void bool) {
	c := p.ctx() // for an end tag, the element is already closed
	html := ns == htmlstream.NamespaceXHTML
	l := &p.lay
	switch kind := layoutKind(name, html, c.layHead); {
	case c.layOff:
		p.layoutFlush()
		l.edge = false
		return
	case html && (name == "html" || name == "head" && start):
		// no whitespace between the html start tag and the head
		p.layoutFlush()
		l.edge, l.start = true, true
		return
	case html && name == "html":
		return // after the body
	case kind == layoutHidden:
		// on a line of its own at a block boundary, which it doesn't end
		if start {
			l.hidden = l.edge
		}
		if l.edge && start && p.opt.Indent != "" {
			p.layoutBreak(c.layDepth, start, true)
		} else {
			p.layoutFlush()
		}
		l.edge = l.hidden && (void || !start)
		return
	case kind == layoutBlock:
		switch {
		case l.edge && p.opt.Indent != "" && !start && l.start && !l.held.comments():
			l.held.reset() // an empty element on one line
		case l.edge && p.opt.Indent != "" && !start && html && name == "body" && p.opt.FinalNewline:
			// the line break is written after the html end tag instead
			p.layoutBreak(c.layDepth, start, false)
		case l.edge && p.opt.Indent != "":
			p.layoutBreak(c.layDepth, start, true)
		default:
			p.layoutFlush()
		}
		l.edge, l.start = true, start && !void
	default:
		p.layoutFlush()
		l.edge = false
	}
	if start && !void && (!html || layoutOff(name)) {
		l.edge = false // contents written as they are
	}
}

// layoutText holds back whitespace text b of tok at a block boundary, and
// reports whether it did.
func (p *polyglot) layoutText(tok *htmlstream.Token, b []byte) bool {
	l := &p.lay
	// (form feed, which XML doesn't allow, is replaced: not whitespace)
	if l.edge && p.opt.Indent != "" && !p.ctx().layOff && ascii.IsBlank(b) && bytes.IndexByte(b, '\f') < 0 && l.held.size()+len(b) <= maxSpaceHold {
		l.held.add(tok, b, false, false)
		return true
	}
	p.layoutFlush()
	if len(b) != 0 {
		l.edge = false
	}
	return false
}

// layoutComment holds back the markup b of a comment (first and last are
// those of commentPart) at a block boundary, and reports whether it did.
func (p *polyglot) layoutComment(b []byte, first, last bool) bool {
	l := &p.lay
	part := p.doc.Part()
	if l.edge && p.opt.Indent != "" && first && last && len(p.out) != 0 && part != htmlutil.BeforeHead && !p.ctx().layOff && l.held.size()+len(b) <= maxSpaceHold {
		l.held.add(nil, b, true, true)
		return true
	}
	p.layoutFlush()
	l.edge = l.edge && (part == htmlutil.BeforeHTML || part == htmlutil.BeforeHead)
	return false
}

// layoutFlush writes the whitespace and comments held back as they are.
func (p *polyglot) layoutFlush() {
	h := &p.lay.held
	if h.size() == 0 {
		return
	}
	for part, b := range h.all() {
		if part.tok < 0 {
			p.markup(b)
		} else {
			p.writeText(h.toks.At(part.tok), b)
		}
	}
	h.reset()
}

// layoutBreak replaces the whitespace held back at a block boundary before
// a tag (a start tag if start) with a line break and the indentation for the
// depth (if line is set), writing the comments held back on lines of their
// own, with an empty line where the input has one between them or between
// elements.
func (p *polyglot) layoutBreak(depth int32, start, line bool) {
	h := &p.lay.held
	inner := depth // the comments before an end tag are in the element
	if !start {
		inner++
	}
	lines := 0 // the line breaks of the whitespace before the next part
	for part, b := range h.all() {
		if part.tok >= 0 {
			lines += bytes.Count(b, []byte("\n"))
			continue
		}
		p.layoutLine(inner, lines > 1 && !p.lay.start)
		p.markup(b)
		p.lay.start, lines = false, 0
	}
	h.reset()
	if line {
		p.layoutLine(depth, lines > 1 && !p.lay.start && start)
	}
}

// layoutLine writes a line break (two for an empty line) and the
// indentation for the depth.
func (p *polyglot) layoutLine(depth int32, empty bool) {
	p.endText()
	if empty {
		p.emitString("\n")
	}
	p.emitString("\n")
	p.lay.line, p.lay.depth = true, depth
}

// Output stack.

// pushOut pushes an element whose start tag was just written with tag.
func (p *polyglot) pushOut(e outElem) {
	if e.marker {
		p.markers++
	}
	e.hookNS, e.written, e.id, e.unwrap = p.startNS, p.startName, p.startID, p.startOmit
	i := int32(len(p.out))
	c := p.ctx()
	if e.marker {
		c.a, c.nobr, c.reopened = -1, -1, -1
	}
	if e.ns != "" {
		c.tmpl, c.selects = i, 0
	} else {
		switch {
		case e.name == "select":
			c.selects++
		case internal.HTMLElem([]byte(e.name))&internal.Scope != 0:
			c.selects = 0
		}
		switch e.name {
		case "template":
			c.tmpl = i
			c.templates++
		case "table":
			c.tmpl = i
			c.tables++
		case "colgroup", "tbody", "tr":
			c.tmpl = i
			if e.synthetic {
				c.implied++
			}
		case "caption", "thead", "tfoot", "td", "th":
			c.tmpl = i
		case "a":
			c.a = i
		case "nobr":
			c.nobr = i
		}
	}
	if e.reopened {
		c.reopened = i
		c.nreopened++
	} else {
		c.input = i
	}
	if p.layout() {
		if !e.unwrap && e.name != "html" && layoutKind(e.name, e.ns == "", c.layHead) == layoutBlock {
			c.layDepth++
		}
		c.layOff = c.layOff || e.ns != "" || layoutOff(e.name)
		c.layHead = e.ns == "" && (e.name == "head" || e.name != "template" && c.layHead)
	}
	e.ctx = c
	if e.key != nil {
		if p.fmtChains == nil {
			p.fmtChains, p.fmtSeed = map[fmtKey]int32{}, maphash.MakeSeed()
		}
		e.fmt = fmtKey{maphash.Bytes(p.fmtSeed, e.key), p.markers}
		e.fmtPrev = -1
		if j, ok := p.fmtChains[e.fmt]; ok {
			e.fmtPrev = j
		}
		p.fmtChains[e.fmt] = i
	}
	p.out = append(p.out, e)
	for j, d := range e.decls {
		r := outRef{elem: i, decl: int32(j), prev: -1}
		if p.outIndex != nil {
			if prev, ok := p.outIndex[d.prefix]; ok {
				r.prev = prev
			}
			p.outIndex[d.prefix] = int32(len(p.outRefs))
		}
		p.outRefs = append(p.outRefs, r)
	}
	if p.outIndex == nil && len(p.outRefs) > internal.IndexMin {
		p.outIndex = make(map[string]int32, len(p.outRefs))
		for k := range p.outRefs {
			r := &p.outRefs[k]
			prefix := p.out[r.elem].decls[r.decl].prefix
			if prev, ok := p.outIndex[prefix]; ok {
				r.prev = prev
			}
			p.outIndex[prefix] = int32(k)
		}
	}
}

// ctx returns the outCtx of the innermost output element.
func (p *polyglot) ctx() outCtx {
	if len(p.out) == 0 {
		return noCtx
	}
	return p.out[len(p.out)-1].ctx
}

func (p *polyglot) popOut() outElem {
	p.endText() // in the element (see callText)
	e := p.out[len(p.out)-1]
	for range e.decls {
		r := p.outRefs[len(p.outRefs)-1]
		if p.outIndex != nil {
			if prefix := e.decls[r.decl].prefix; r.prev >= 0 {
				p.outIndex[prefix] = r.prev
			} else {
				delete(p.outIndex, prefix)
			}
		}
		p.outRefs = p.outRefs[:len(p.outRefs)-1]
	}
	if e.key != nil && !e.noah {
		if e.fmtPrev >= 0 {
			p.fmtChains[e.fmt] = e.fmtPrev
		} else {
			delete(p.fmtChains, e.fmt)
		}
	}
	p.out = p.out[:len(p.out)-1]
	if len(p.out) == p.keptP {
		p.keptP = -1
		if n := len(p.keptOuter); n != 0 {
			p.keptP, p.keptDepth = p.keptOuter[n-1][0], p.keptOuter[n-1][1]
			p.keptOuter = p.keptOuter[:n-1]
		}
	}
	if len(p.out) == p.pClose {
		p.pClose = -1
	}
	p.buf = htmlstream.AppendEndTag(p.buf[:0], e.written)
	p.tag(p.buf, Tag{Name: e.written, NS: e.hookNS, Reopened: e.reopened, ID: e.id, omit: e.unwrap})
	if e.marker {
		p.markers--
		// formatting elements closed inside the marker aren't reopened
		// outside it
		p.pending = dropAbove(p.pending, p.markers)
		p.tentative = dropAbove(p.tentative, p.markers)
	}
	return e
}

func dropAbove(rs []reopen, marker int) []reopen {
	n := 0
	for _, r := range rs {
		if r.marker <= marker {
			rs[n] = r
			n++
		}
	}
	return rs[:n]
}

// popTo closes the output elements above index n. Reopened formatting
// elements are reopened again (see resolveTentative), outermost first.
func (p *polyglot) popTo(n int) {
	for len(p.out) > n {
		e := p.popOut()
		if e.reopened && !e.noah && !p.removed(e.id) {
			p.tentative = append(p.tentative, reopen{name: e.name, written: e.written, start: e.start, id: e.id, marker: e.level, key: e.key, seq: e.seq, decls: e.decls, bound: e.bound})
		}
	}
}

// removed reports whether the formatting element with the ID, closed inside
// the element being removed, was opened inside it: it isn't reopened after
// the removed element, as if its tokens had been left out.
func (p *polyglot) removed(id int) bool {
	return p.removing() && id > p.removeID
}

func (p *polyglot) top() *outElem {
	if len(p.out) == 0 {
		return nil
	}
	return &p.out[len(p.out)-1]
}

// htmlAbove returns the name of the innermost HTML element open in the
// output above index i, or an empty string.
func (p *polyglot) htmlAbove(i int) string {
	for j := len(p.out) - 1; j > i; j-- {
		if p.out[j].ns == "" {
			return p.out[j].name
		}
	}
	return ""
}

func (p *polyglot) topIs(name string, synthetic bool) bool {
	t := p.top()
	return t != nil && t.name == name && t.synthetic == synthetic && t.ns == ""
}

// inForeign reports whether the next element written is a child of a foreign
// element (e.g., inside an integration point), where HTML elements need a
// namespace declaration.
func (p *polyglot) inForeign() bool {
	t := p.top()
	return t != nil && t.ns != ""
}

const xmlnsXHTML = ` xmlns="` + htmlstream.NamespaceXHTML + `"`

// Formatting element reconstruction.

// resolveTentative decides whether formatting elements closed by implied end
// tags are reopened, based on the token which caused the implied end tags.
func (p *polyglot) resolveTentative(tok *htmlstream.Token) {
	if len(p.tentative) == 0 {
		return
	}
	slices.Reverse(p.tentative) // outermost first
	for _, r := range p.tentative {
		// An a start tag closes a previous a for good (adoption agency
		// algorithm, see closeFormatting), and so does a nobr start tag
		// which closed an open nobr. Other formatting elements are reopened
		// before the start tag.
		if tok.Kind() == htmlstream.StartTag && tok.Is(r.name) && (r.name == "a" || r.name == "nobr" && r.implied) {
			continue
		}
		p.pending = append(p.pending, r)
	}
	p.tentative = p.tentative[:0]
}

// reconstruct reopens pending formatting elements before phrasing content.
func (p *polyglot) reconstruct() {
	if t := p.top(); t != nil && t.ns != "" && !t.integration {
		return // HTML parsers don't reconstruct in foreign content
	}
	// in the order they were opened (the list of active formatting elements)
	slices.SortStableFunc(p.pending, func(a, b reopen) int { return cmp.Compare(a.seq, b.seq) })
	// the declarations of a start tag being written don't apply to the
	// elements reopened before it
	tagDecls, tagIndexed := p.tagDecls, p.tagIndexed
	p.tagDecls, p.tagIndexed = nil, false
	n := 0
	for _, r := range p.pending {
		if r.marker != p.markers {
			p.pending[n] = r
			n++
			continue
		}
		if len(p.out) >= p.opt.MaxDepth {
			p.warn(p.pos, "stopped reopening %s at the output depth limit (Options.MaxDepth)", r.name)
			continue
		}
		if p.inForeign() && !bytes.Contains(r.start, []byte(xmlnsXHTML)) {
			// reopened in an integration point
			r.start = slices.Concat(r.start[:len(r.start)-1], []byte(xmlnsXHTML+">"))
		}
		var bound []nsDecl // r.bound without the prefixes declared below
		for i, d := range r.bound {
			if uri, ok := p.lookupPrefix(d.prefix); ok && uri == d.uri {
				if bound != nil {
					bound = append(bound, d)
				}
				continue
			}
			if bound == nil {
				// r.start, r.decls and r.bound are shared with the element
				// as first written
				bound = append(make([]nsDecl, 0, len(r.bound)), r.bound[:i]...)
				r.start = slices.Clone(r.start[:len(r.start)-1]) // without ">"
				r.decls = slices.Clone(r.decls)
			}
			// the element declares the prefix itself from now on
			r.start = append(r.start, ` xmlns:`...)
			r.start = append(r.start, d.prefix...)
			r.start = append(r.start, `="`...)
			r.start = AppendEscapeAttr(r.start, d.uri)
			r.start = append(r.start, '"')
			r.decls = append(r.decls, nsDecl{prefix: d.prefix, uri: d.uri, added: true})
			p.warn(p.pos, "reopened %s declares the prefix %q of its attributes (the prefix isn't bound to %s where it's reopened, and HTML parsers will see an extra xmlns:%s attribute when parsing the output)", r.name, d.prefix, nsDesc(d.uri), d.prefix)
		}
		if bound != nil {
			r.start = append(r.start, '>')
			r.bound = bound
		}
		p.tag(r.start, Tag{Start: true, Reopened: true, Name: r.written, NS: htmlstream.NamespaceXHTML, ID: r.id})
		p.pushOut(outElem{name: r.name, reopened: true, start: r.start, level: r.marker, key: r.key, seq: r.seq, decls: r.decls, bound: r.bound})
	}
	p.pending = p.pending[:n]
	p.tagDecls, p.tagIndexed = tagDecls, tagIndexed
}

// boundPrefixes returns the prefixes of the attributes of a formatting
// element's start tag which outer declarations bind, with their namespaces,
// appended to p.bounds, or nil. If the element is reopened where a prefix
// isn't bound to the same namespace, the reopened start tag declares it (see
// reconstruct).
func (p *polyglot) boundPrefixes(tok *htmlstream.Token, ns string) []nsDecl {
	off := len(p.bounds)
	big := tok.NumAttrs() > internal.IndexMin
	if big {
		p.boundSet.Reset(tok.NumAttrs())
	}
	for a := range tok.Attrs() {
		if bytes.IndexByte(a.Name(), ':') < 0 {
			continue
		}
		an := attrName(a, ns)
		prefix, _, ok := strings.Cut(an, ":")
		if !ok || prefix == "xml" || prefix == "xmlns" || !htmlstream.ValidXMLName(an) {
			continue
		}
		if _, _, found := p.tagDecl(prefix); found {
			continue // declared by the element itself
		}
		if big {
			if _, ok := p.boundSet.M[prefix]; ok {
				continue
			}
			p.boundSet.M[prefix] = struct{}{}
		} else if slices.ContainsFunc(p.bounds[off:], func(d nsDecl) bool { return d.prefix == prefix }) {
			continue
		}
		if uri, ok := p.lookupPrefix(prefix); ok {
			p.bounds = append(p.bounds, nsDecl{prefix: prefix, uri: uri})
		}
	}
	if len(p.bounds) == off {
		return nil
	}
	return p.bounds[off:len(p.bounds):len(p.bounds)]
}

// formattingKey returns the name and attributes of a formatting element's
// start tag, which HTML parsers compare in the Noah's Ark clause: the name,
// followed by name=value for each attribute, sorted, each after a NUL byte.
// It is appended to p.starts.
func (p *polyglot) formattingKey(tok *htmlstream.Token, name string) []byte {
	// name=value for each attribute, in p.kbuf
	b, attrs := p.kbuf[:0], p.kattrs[:0]
	big := tok.NumAttrs() > internal.IndexMin
	if big {
		p.keySet.Reset(tok.NumAttrs())
	}
	for a := range tok.Attrs() {
		off := len(b)
		b = a.AppendName(b)
		n := b[off:]
		var dup bool
		if big {
			if _, dup = p.keySet.M[string(n)]; !dup {
				p.keySet.M[string(n)] = struct{}{}
			}
		} else {
			dup = slices.ContainsFunc(attrs, func(r attrRange) bool { return bytes.HasPrefix(b[r.off:r.end], n) && b[r.off+len(n)] == '=' })
		}
		if dup {
			b = b[:off] // HTML parsers drop duplicate attributes
			continue
		}
		b = append(b, '=')
		b = a.AppendValue(b)
		attrs = append(attrs, attrRange{off: off, end: len(b)})
	}
	p.kbuf, p.kattrs = b, attrs
	slices.SortFunc(attrs, func(x, y attrRange) int { return bytes.Compare(b[x.off:x.end], b[y.off:y.end]) })
	off := len(p.starts)
	p.starts = append(p.starts, name...)
	for _, r := range attrs {
		p.starts = append(p.starts, 0)
		p.starts = append(p.starts, b[r.off:r.end]...)
	}
	return p.starts[off:len(p.starts):len(p.starts)]
}

// noahsArk applies the Noah's Ark clause of HTML parsers before a formatting
// element is opened: if three elements with the same name and attributes
// (key) are in the list of active formatting elements after the last marker,
// the earliest one is removed from it, so it isn't reopened after a
// misnested end tag. The open formatting elements with the same key hash
// above the last marker are linked through outElem.fmtPrev, from
// p.fmtChains, and noahsArk unlinks the removed one.
func (p *polyglot) noahsArk(key []byte) {
	if p.fmtChains == nil {
		return
	}
	k := fmtKey{maphash.Bytes(p.fmtSeed, key), p.markers}
	i, ok := p.fmtChains[k]
	if !ok {
		return
	}
	n, first, next := 0, int32(-1), int32(-1) // next: the one linked to first
	for prev := int32(-1); i >= 0; prev, i = i, p.out[i].fmtPrev {
		if bytes.Equal(p.out[i].key, key) {
			n++
			first, next = i, prev
		}
	}
	if n < 3 {
		return
	}
	e := &p.out[first]
	e.noah = true
	switch {
	case next >= 0:
		p.out[next].fmtPrev = e.fmtPrev
	case e.fmtPrev >= 0:
		p.fmtChains[k] = e.fmtPrev
	default:
		delete(p.fmtChains, k)
	}
}

// closeFormatting handles an a or nobr start tag while an element with the
// same name is open above the last marker, which HTML parsers close first
// (adoption agency algorithm). A reopened one with only reopened elements
// above it is closed (and those are reopened again); otherwise the open
// element is from the input and the nesting differs, so a warning is given.
func (p *polyglot) closeFormatting(tok *htmlstream.Token, name string) {
	// a closed a isn't reopened (a closed nobr was reopened before)
	p.pending = slices.DeleteFunc(p.pending, func(r reopen) bool {
		return r.name == name && r.marker == p.markers
	})
	c := p.ctx()
	i := int(c.a)
	if name == "nobr" {
		i = int(c.nobr)
	}
	switch {
	case i < 0:
	case int(c.input) >= i:
		p.warn(tok.Pos(), "wrote %s inside an open %s (HTML parsers would have closed the open one first)", name, name)
	default:
		p.popTo(i + 1)
		p.popOut()
		p.resolveTentative(tok)
	}
}

// Tokens.

// commentToken writes a comment like htmlstream.AppendComment, one token at
// a time. A bogus comment which is the XML declaration before the root
// element is removed (see xmlDecl). The start of a comment split into
// multiple tokens is collected until it is long enough for these checks and
// those of commentPart.
func (p *polyglot) commentToken(tok *htmlstream.Token) {
	if !tok.Continued() {
		p.skip, p.cstart, p.cstarted = false, p.cstart[:0], false
	}
	if p.skip {
		return
	}
	// newlines normalized and NUL replaced, as in HTML parsers
	b := tok.AppendText(p.raw[:0])
	p.raw = b
	first := !p.cstarted
	if first {
		p.cstart = append(p.cstart, b...)
		if len(p.cstart) <= len("?xml?") && tok.Partial() {
			return
		}
		p.cstarted = true
		b = p.cstart
		if tok.Bogus() && isXMLDecl(b) && p.xmlDecl(p.pos) {
			p.skip = true
			return
		}
	}
	p.commentPart(b, first, !tok.Partial())
}

// procInst writes a processing instruction (read with XML syntax) as a
// comment, like a bogus comment. The XML declaration before the root element
// is removed (see xmlDecl).
func (p *polyglot) procInst(tok *htmlstream.Token) {
	raw := tok.Raw()
	if !tok.Incomplete() {
		raw = raw[:len(raw)-1] // ">"
	}
	data := raw[1:] // "?..."
	if isXMLDecl(data) && p.xmlDecl(p.pos) {
		return
	}
	// TODO: maybe preserve proc insts for html now since the spec allows it, but probably behind an option since it'll break many parsers
	b := p.raw[:0]
	for i := 0; i < len(data); i++ {
		switch c := data[i]; c {
		case '\r':
			b = append(b, '\n')
			if i+1 < len(data) && data[i+1] == '\n' {
				i++
			}
		case 0:
			b = utf8.AppendRune(b, utf8.RuneError)
		default:
			b = append(b, c)
		}
	}
	p.raw = b
	p.commentPart(b, true, true)
}

// commentPart writes part of a comment with the text b.
func (p *polyglot) commentPart(b []byte, first, last bool) {
	b = p.replaceChars(b)
	out := p.buf[:0]
	if first {
		out = append(out, "<!--"...)
		if bytes.HasPrefix(b, []byte(">")) || bytes.HasPrefix(b, []byte("->")) {
			out = append(out, ' ')
		}
		p.dash = false
	}
	for _, c := range b {
		if c == '-' && p.dash {
			out = append(out, ' ')
		}
		out = append(out, c)
		p.dash = c == '-'
	}
	if last {
		if p.dash {
			out = append(out, ' ')
		}
		out = append(out, "-->"...)
	}
	p.buf = out
	if p.doc.Part() == htmlutil.AfterHead && p.inHeadTop() && !p.afterHeadFull && p.holdAfterHead(nil, out, first, last) {
		return
	}
	p.writeComment(out, first, last)
}

// writeComment writes the markup of a comment (or of part of one, see
// commentPart), followed by a line break before the root element.
func (p *polyglot) writeComment(b []byte, first, last bool) {
	if p.layout() && p.layoutComment(b, first, last) {
		return
	}
	p.markup(b)
	if last && p.doc.Part() == htmlutil.BeforeHTML {
		p.emitString("\n")
		p.lay.line, p.lay.depth = p.layout(), 0
	}
}

// textToken writes a text or CDATA token. Text split into multiple tokens is
// written one token at a time, except for the contents of script and style
// elements and noscript contents read as raw text, which are collected first
// (see collectRaw).
// textToken writes the decoded text of a text or CDATA token from offset
// start, up to end if not negative.
func (p *polyglot) textToken(tok *htmlstream.Token, start, end int) {
	f, ok := p.frames.Top()
	if ok && f.selfClosed != "" {
		p.selfClosedText(tok, f)
	}
	if ok && f.out >= 0 && f.out < len(p.out) && (f.name == "script" || f.name == "style") && p.out[f.out].ns == "" {
		p.rawText(tok, f)
		return
	}
	if !tok.Continued() {
		p.warned, p.held, p.heldDone = false, p.held[:0], false
	}
	b := tok.AppendText(p.tbuf[:0])
	p.tbuf = b
	if end >= 0 {
		b = b[:end]
	}
	b = b[start:]
	switch part := p.doc.Part(); {
	case part == htmlutil.BeforeHTML, part == htmlutil.BeforeHead:
		return // whitespace before the html element or the head, which HTML parsers drop
	case p.charsetWait:
		p.headSpace.add(tok, b, false, false) // see placeCharset
		return
	case p.opt.FinalNewline && p.bodySpaceToken(tok):
		p.bodySpace.add(tok, b, false, false)
		return
	case part == htmlutil.AfterHead && p.inHeadTop() && !p.afterHeadFull && p.holdAfterHead(tok, b, false, false):
		return
	}
	if top := p.top(); top != nil && (top.name == "iframe" || top.name == "noembed" || top.name == "noframes") && top.ns == "" {
		if !ascii.IsBlank(b) && !p.warned {
			p.warned = true
			p.warn(p.pos, "dropped contents of %s", top.name)
		}
		return
	}
	// noscript contents read as raw text (with scripting enabled) are
	// written unchanged when both kinds of HTML parsers and XML parsers read
	// them the same, and otherwise escaped, for parsers reading markup
	collected := ok && f.name == "noscript" && tok.InRawText()
	unchanged := collected
	// title and textarea contents read with XML syntax starting with a
	// CDATA section (after whitespace) are collected to remove a CDATA
	// section around them (see cutCDATA), and other contents are written
	// token by token, once the start shows they don't start with one
	_, xmlSyntax := internal.TokenMode(tok)
	rcdata := xmlSyntax && ok && f.text && (f.name == "title" || f.name == "textarea") && f.out >= 0 && f.out < len(p.out) && p.out[f.out].ns == ""
	if !tok.Continued() {
		p.rcStream, p.rcTail, p.rcMarkers = false, p.rcTail[:0], false
	}
	const cdataStart = "<![CDATA["
	markers := false
	switch {
	case rcdata && p.rcStream:
		if !p.rcMarkers {
			// the markers may be split across tokens
			d := append(p.rcTail, tok.Data()...)
			markers = bytes.Contains(d, []byte(cdataStart))
			p.rcTail = append(d[:0], d[max(len(d)-len(cdataStart)+1, 0):]...)
		}
	case rcdata:
		// collected like by collectRaw, and limited the same way once known
		// to start with a CDATA section, while the whitespace at the start
		// is limited for all contents
		if !tok.Continued() {
			p.rawBuf, p.rawData, p.rawLimit = p.rawBuf[:0], p.rawData[:0], false
		}
		if !p.rawLimit {
			p.rawBuf = tok.AppendText(p.rawBuf)
			p.rawData = append(p.rawData, tok.Data()...)
		}
		last := !tok.Partial()
		start := bytes.TrimLeft(p.rawData, "\t\n\f\r ")
		cdata := bytes.HasPrefix(start, []byte(cdataStart))
		p.rawLimit = p.rawLimit || spaceLen(p.rawBuf) > max(p.opt.MaxRawText, 0) || cdata && len(p.rawBuf) > p.opt.MaxRawText
		switch {
		case p.rawLimit:
			if last {
				p.err = &Error{p.pos, f.name + " contents exceed MaxRawText (" + strconv.Itoa(p.opt.MaxRawText) + " bytes)"}
			}
			return
		case cdata:
			if !last {
				return
			}
			var found bool
			if b, found = cutCDATA(p.rawBuf, p.rawData); !found {
				markers = true
			}
		case !last && strings.HasPrefix(cdataStart, string(start)):
			return // not known yet
		default:
			b, p.rcStream = p.rawBuf, !last
			d := p.rawData
			markers = bytes.Contains(d, []byte(cdataStart))
			p.rcTail = append(p.rcTail[:0], d[max(len(d)-len(cdataStart)+1, 0):]...)
		}
	}
	if markers && !p.removing() {
		p.rcMarkers = true
		p.warn(p.pos, "kept CDATA section markers in %s as text (XML parsers would have read a CDATA section)", f.name)
	}
	if collected {
		if !p.collectRaw(tok, f.name) {
			return
		}
		b = p.rawBuf
		switch {
		case bytes.ContainsAny(b, "<&") || bytes.Contains(b, []byte("]]>")):
			unchanged = false
			p.warned = true
			p.warn(p.pos, "escaped raw text noscript contents containing <, & or ]]> (HTML parsers with scripting enabled will read them as raw text when parsing the output)")
		case p.doc.Part() < htmlutil.InBody && !ascii.IsBlank(b):
			p.warned = true
			p.warn(p.pos, "kept raw text noscript contents in the head (HTML parsers with scripting disabled will move them to the body when parsing the output)")
		}
	}
	if p.stripNL {
		// with XML syntax, XML parsers keep it, also at the start of a
		// CDATA section
		p.stripNL = false
		if (p.keptNL || xmlSyntax) && bytes.HasPrefix(b, []byte("\n")) && !p.removing() {
			p.nlWarned = true
			p.warn(p.pos, "leading newline in pre, textarea or listing can't be represented")
		}
		b = bytes.TrimPrefix(b, []byte("\n"))
		if len(b) == 0 {
			return // HTML parsers ignore it, without reopening formatting elements
		}
	}
	switch {
	case collected:
	case p.topIs("colgroup", true):
		// HTML parsers insert leading whitespace in an implied colgroup
		// before the other text closes it, like the Reader does in an
		// explicit one
		if i := spaceLen(b); i != 0 && i < len(b) {
			p.textPart(tok, b[:i], false)
			b = b[i:]
		}
	case len(p.pending) != 0 && !(ok && f.text) && !p.spaceReconstructs():
		// HTML parsers reopen the formatting elements for whitespace only
		// if other text follows it (e.g., in a table), so the leading
		// whitespace is held back until other text or the end of the text
		// (see spaceHold), after removing the NUL they ignore
		var dropped bool
		b, dropped = p.dropNUL(tok, b)
		if b = p.spaceHold(tok, b); b == nil || dropped && len(b) == 0 {
			return
		}
	}
	p.textPart(tok, b, unchanged)
}

// dropNUL removes NUL from the text b of a Text token where HTML parsers
// ignore it: under HTML rules, in HTML content, including integration
// points (see isIntegrationPoint). In foreign content, they replace it with
// U+FFFD, as replaceChars does. The tokenizer already replaces it in RCDATA,
// raw text and comments (see htmlstream.Token.AppendText). In CDATA
// sections, which aren't text for the tokenizer's data state, it is replaced
// too, as in x/net/html (the HTML Standard ignores it in integration
// points). It modifies b, and reports whether it removed all of it. If b
// isn't valid UTF-8, it replaces the invalid UTF-8 and the other characters
// XML doesn't allow (as replaceChars does) in a new slice instead.
func (p *polyglot) dropNUL(tok *htmlstream.Token, b []byte) ([]byte, bool) {
	i := bytes.IndexByte(b, 0)
	if i < 0 || !p.dropsNUL(tok) {
		return b, false
	}
	if !utf8.Valid(b) {
		// HTML parsers decode the input before ignoring NUL, so the bytes
		// around it don't form a character
		var out []byte
		for seg := range bytes.SplitSeq(b, []byte{0}) {
			if !p.removing() {
				p.replaced |= replaced(seg)
			}
			out, _ = htmlstream.AppendXMLChars(out, seg)
		}
		return out, len(out) == 0
	}
	out := b[:i]
	for _, c := range b[i+1:] {
		if c != 0 {
			out = append(out, c)
		}
	}
	return out, len(out) == 0
}

// dropsNUL reports whether dropNUL removes NUL from the text of tok. In
// title and textarea contents, the tokenizer replaces it, and the contents
// of a CDATA section around them read with XML syntax (see cutCDATA) are
// replaced like other CDATA sections.
func (p *polyglot) dropsNUL(tok *htmlstream.Token) bool {
	t := p.top()
	return tok.Kind() == htmlstream.Text && tok.HTMLRules() && (t == nil || t.ns == "" && t.name != "title" && t.name != "textarea" || t.integration)
}

// cutCDATA removes a CDATA section around the contents b of a title or
// textarea element read with XML syntax, given undecoded as data. If data
// is a CDATA section with only whitespace around it, it returns the
// contents XML parsers read: the whitespace and the contents of the
// section. Otherwise it returns b and false.
func cutCDATA(b, data []byte) ([]byte, bool) {
	inner, ok := bytes.CutPrefix(ascii.TrimSpace(data), []byte("<![CDATA["))
	if !ok {
		return b, false
	}
	if inner, ok = bytes.CutSuffix(inner, []byte("]]>")); !ok || bytes.Contains(inner, []byte("]]>")) {
		return b, false
	}
	// the whitespace is the same in b, without CR
	lead, trail := spaceLen(b), len(b)-len(bytes.TrimRight(b, "\t\n\f\r "))
	out := append(make([]byte, 0, len(data)), b[:lead]...)
	for i := 0; i < len(inner); i++ {
		switch c := inner[i]; {
		case c != '\r':
			out = append(out, c)
		case i+1 == len(inner) || inner[i+1] != '\n':
			out = append(out, '\n') // XML parsers read a CR LF or a CR as a LF
		}
	}
	return append(out, b[len(b)-trail:]...), true
}

// selfClosedText warns about the contents of a raw text or RCDATA element
// (script, style, title, textarea, etc., and noscript read as raw text)
// whose start tag is self-closing, at the first text token which isn't
// whitespace. Without XML syntax, HTML parsers ignore the slash, so the
// contents run to the end tag or the end of the input, which happens with
// XHTML read as HTML (e.g., <script src="a.js"/>).
func (p *polyglot) selfClosedText(tok *htmlstream.Token, f *frame) {
	if f.name == "noscript" && !tok.InRawText() {
		f.selfClosed = "" // markup, as XML parsers read it
		return
	}
	if p.space(tok) {
		return
	}
	p.warn(f.pos, "read self-closing <%s/> as a start tag, with the input up to </%s> or the end of the input as its contents (to match HTML parsers, which ignore the slash)", f.selfClosed, f.selfClosed)
	f.selfClosed = ""
}

// maxSpaceHold is the size of the leading whitespace held back by spaceHold.
const maxSpaceHold = 64 << 10

// spaceHold holds back the leading whitespace of the current text, and
// returns the text to write for the token, or nil. If other text follows the
// whitespace, the formatting elements are reopened before the whitespace,
// like HTML parsers do. Whitespace longer than maxSpaceHold is written
// before them instead, without being held back further. The output depends
// only on the length of the whitespace, not on how the text is split into
// tokens.
func (p *polyglot) spaceHold(tok *htmlstream.Token, b []byte) []byte {
	i := spaceLen(b)
	if p.heldDone {
		// like for longer whitespace
		if i != 0 && i < len(b) {
			p.textPart(tok, b[:i], false)
			b = b[i:]
		}
		return b
	}
	p.held = append(p.held, b[:i]...)
	switch {
	case i < len(b) && len(p.held) <= maxSpaceHold:
		p.heldDone = true
		p.held = append(p.held, b[i:]...)
		return p.held
	case i < len(b):
		p.heldDone = true
		p.textPart(tok, p.held, false)
		return b[i:]
	case len(p.held) > maxSpaceHold || !tok.Partial():
		p.heldDone = true
		return p.held
	}
	return nil
}

// spaceLen returns the length of the leading whitespace of b.
func spaceLen(b []byte) int {
	for i, c := range b {
		if !ascii.IsSpace(c) {
			return i
		}
	}
	return len(b)
}

// textPart writes text of the current text or CDATA token, or noscript
// contents read as raw text, which are written as contents if unchanged is
// set (see textToken).
func (p *polyglot) textPart(tok *htmlstream.Token, b []byte, unchanged bool) {
	if p.topIs("colgroup", true) && !ascii.IsBlank(b) {
		// HTML parsers close it, like the Reader does an explicit one, also
		// at a NUL they then ignore
		p.popOut()
	}
	var dropped bool
	if b, dropped = p.dropNUL(tok, b); dropped {
		return
	}
	// before formatting elements are reopened in the table
	foster := p.fosterParent()
	if f, ok := p.frames.Top(); ok && f.text {
		// HTML parsers don't reopen them in text-only elements
	} else if !ascii.IsBlank(b) || len(p.pending) != 0 && p.spaceReconstructs() {
		p.reconstruct()
	}
	if p.written() == p.preEnd && bytes.HasPrefix(b, []byte("\n")) && !p.removing() && !p.nlWarned {
		// HTML parsers would drop it
		p.nlWarned = true
		p.warn(p.pos, "leading newline in pre, textarea or listing can't be represented")
	}
	if p.impliedHeadNoscript() && !ascii.IsBlank(b) && !p.warned {
		p.warned = true
		p.warn(p.pos, "kept text in a noscript in the implied head (HTML parsers would have closed the noscript and moved the text to the body)")
	}
	if t := p.top(); t != nil && t.name == "template" && t.ns == "" && t.tmode == 'c' && !ascii.IsBlank(b) && !p.warned {
		p.warned = true
		p.warn(p.pos, "kept text after col in a template (HTML parsers would have ignored it)")
	}
	if foster && !ascii.IsBlank(b) && !p.warned {
		p.warned = true
		p.warn(p.pos, "kept text in a table outside a cell (HTML parsers would have moved it before the table)")
	}
	if unchanged {
		if p.removing() {
			return
		}
		b = p.replaceChars(b)
		if p.opt.OnText != nil {
			if ev := p.callText(Text{Data: b, First: !p.textOpen, Raw: true, Token: tok}); ev.set {
				b = ev.data
				if bytes.ContainsAny(b, "<&") || bytes.Contains(b, []byte("]]>")) {
					// like such contents of the input
					p.raw = AppendEscapeText(p.raw[:0], b)
					b = p.raw
				}
			}
		}
		p.textOpen = true
		p.emit(b)
		return
	}
	p.text(tok, b)
}

// text writes escaped text, as part of the current text node.
func (p *polyglot) text(tok *htmlstream.Token, b []byte) {
	if p.removing() || p.layout() && p.layoutText(tok, b) {
		return
	}
	p.writeText(tok, b)
}

// writeText writes text like text, without the layout.
func (p *polyglot) writeText(tok *htmlstream.Token, b []byte) {
	b = p.replaceChars(b)
	if p.opt.OnText != nil {
		if ev := p.callText(Text{Data: b, First: !p.textOpen, Token: tok}); ev.set {
			if !ev.isText {
				p.textOpen = true
				p.emit(ev.data)
				return
			}
			b = ev.data
		}
	}
	p.raw = AppendEscapeText(p.raw[:0], b)
	p.textOpen = true
	c := p.ctx()
	if !p.layout() || c.layOff {
		p.emit(p.raw)
		return
	}
	// the prefix and the indentation start the lines of the text, replacing
	// its spaces and tabs there (also at the start of a token continuing a
	// line)
	for b := p.raw; len(b) != 0; {
		if p.lay.line {
			b = bytes.TrimLeft(b, " \t")
		}
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			p.emit(b)
			break
		}
		p.emit(b[:i+1])
		b = b[i+1:]
		p.lay.line, p.lay.depth = true, c.layDepth
	}
}

// lineStart writes the prefix and the indentation at the start of a line
// (see Options.Indent and Options.IndentPrefix).
func (p *polyglot) lineStart() {
	p.lay.line = false
	p.emitString(p.opt.IndentPrefix)
	if p.opt.Indent != "" {
		for range min(p.lay.depth, maxIndent) {
			p.emitString(p.opt.Indent)
		}
	}
}

// AppendEscapeText appends s to dst, escaped for use as text like [Convert]
// escapes it: like [htmlstream.AppendEscapeText], with U+00A0 written as
// &#160; so it stays visible in the markup.
func AppendEscapeText[S ~string | ~[]byte](dst []byte, s S) []byte {
	return appendEscape(dst, s, internal.EscapeText)
}

// AppendEscapeAttr appends s to dst, escaped for use as a quoted attribute
// value like [Convert] escapes it: like [htmlstream.AppendEscapeAttr], with
// U+00A0 written as &#160;.
func AppendEscapeAttr[S ~string | ~[]byte](dst []byte, s S) []byte {
	return appendEscape(dst, s, internal.EscapeAttr)
}

// appendEscape appends s to dst escaped with t, and with U+00A0 written as
// &#160;.
func appendEscape[S ~string | ~[]byte](dst []byte, s S, t *internal.EscapeTable) []byte {
	start := 0
	for i := 0; i+1 < len(s); i++ {
		if s[i] == 0xC2 && s[i+1] == 0xA0 {
			dst = internal.AppendEscape(dst, s[start:i], t)
			dst = append(dst, "&#160;"...)
			start = i + 2
			i++
		}
	}
	return internal.AppendEscape(dst, s[start:], t)
}

// sanitize replaces characters which aren't allowed in XML, with a warning.
func (p *polyglot) sanitize(pos htmlstream.Pos, b []byte) []byte {
	if htmlstream.ValidXMLChars(b) {
		return b
	}
	p.warnReplaced(pos, replaced(b))
	s, _ := htmlstream.AppendXMLChars(nil, b)
	return s
}

// replaceChars replaces characters which aren't allowed in XML in the
// current text or comment, which is reported after its last token (see
// token).
func (p *polyglot) replaceChars(b []byte) []byte {
	if htmlstream.ValidXMLChars(b) {
		return b
	}
	p.replaced |= replaced(b)
	s, _ := htmlstream.AppendXMLChars(nil, b)
	return s
}

// replacedKind is what AppendXMLChars replaces in a string.
type replacedKind uint8

const (
	replacedChars replacedKind = 1 << iota // characters not allowed in XML
	replacedUTF8                           // invalid UTF-8
)

// replaced returns what AppendXMLChars replaces in b.
func replaced(b []byte) (k replacedKind) {
	for len(b) != 0 {
		r, n := utf8.DecodeRune(b)
		switch {
		case r == utf8.RuneError && n == 1:
			k |= replacedUTF8
		case !htmlstream.ValidXMLChars(b[:n]):
			k |= replacedChars
		}
		b = b[n:]
	}
	return k
}

// warnReplaced reports the replacements of AppendXMLChars.
func (p *polyglot) warnReplaced(pos htmlstream.Pos, k replacedKind) {
	if k&replacedUTF8 != 0 {
		p.warn(pos, "invalid UTF-8 replaced with U+FFFD")
	}
	if k&replacedChars != 0 {
		p.warn(pos, "replaced characters XML doesn't allow")
	}
}

// collectRaw collects the contents of a script or style element or of a
// noscript element read as raw text in rawBuf, and reports whether tok is
// their last token. Contents exceeding MaxRawText are an error.
func (p *polyglot) collectRaw(tok *htmlstream.Token, name string) bool {
	if !tok.Continued() {
		p.rawBuf, p.rawLimit = p.rawBuf[:0], false
	}
	if !p.rawLimit {
		p.rawBuf = tok.AppendText(p.rawBuf)
		p.rawLimit = len(p.rawBuf) > p.opt.MaxRawText
	}
	if tok.Partial() {
		return false
	}
	if p.rawLimit {
		p.err = &Error{p.pos, name + " contents exceed MaxRawText (" + strconv.Itoa(p.opt.MaxRawText) + " bytes)"}
		return false
	}
	return true
}

// rawText writes the contents of a script or style element.
func (p *polyglot) rawText(tok *htmlstream.Token, f *frame) {
	if !p.collectRaw(tok, f.name) || p.removing() {
		return
	}
	pos := p.pos
	b := p.sanitize(pos, p.rawBuf)
	script := f.name == "script"
	js := !script || htmlutil.IsJavaScriptType(f.scriptType) // or CSS
	_, xmlSyntax := internal.TokenMode(tok)
	var w htmlutil.CDATAWrapper
	if js {
		// wrapped in a CDATA section if it contains < or &, after removing
		// an existing wrapper (modules don't allow HTML-like comments)
		w = htmlutil.CDATABlockComment
		if script {
			w |= htmlutil.CDATALineComment
		}
		if f.scriptType != "module" {
			w |= htmlutil.CDATAHTMLComment
		}
	}
	if xmlSyntax && (js || htmlutil.IsJSONType(f.scriptType)) {
		// a CDATA section as XHTML writes it, which XML parsers read the
		// contents of
		w |= htmlutil.CDATABare
	}
	var found bool
	var lead, trail []byte // the whitespace around a wrapper of the input, which is kept
	whole := b
	if b, found = htmlutil.CutCDATAWrapper(b, w); found {
		lead, trail = whole[:spaceLen(whole)], whole[len(bytes.TrimRight(whole, "\t\n\f\r ")):]
	}
	if found && bytes.Contains(b, []byte(splitCDATAEnd)) {
		// "]]>" as written in a CDATA section, split again below
		b = bytes.ReplaceAll(b, []byte(splitCDATAEnd), []byte("]]>"))
	}
	if !found && w&htmlutil.CDATABare != 0 {
		// written unchanged, as HTML parsers read them
		ref, comment := xmlMarkup(b)
		if ref && p.firstWarning(onceKey{kind: "script reference"}) {
			p.warn(pos, "kept character references in %s as text (XML parsers would have decoded them) (will not warn again)", f.name)
		}
		if comment && p.firstWarning(onceKey{kind: "script comment"}) {
			p.warn(pos, "kept comments in %s as text (XML parsers would have read them as comments) (will not warn again)", f.name)
		}
	}
	if js {
		b = p.rawTextEnd(pos, f.name, b, tok.Unsound())
	}
	// a CDATA section left in the contents, which HTML parsers read as text
	cdata := w&htmlutil.CDATABare != 0 && bytes.Contains(b, []byte("<![CDATA["))
	set := false // the contents are from Text.SetText, without the warnings and errors for the input
	if p.opt.OnText != nil {
		if ev := p.callText(Text{Data: b, First: true, Raw: true, Token: tok}); ev.set {
			b, cdata, set = ev.data, false, true
		}
	}
	p.textOpen = true
	p.emit(lead)
	defer p.emit(trail)
	if !bytes.ContainsAny(b, "<&") && !bytes.Contains(b, []byte("]]>")) {
		p.emit(b)
		return
	}
	if cdata {
		// its ]]> is split below
		p.warn(pos, "kept CDATA section markers in %s as text (XML parsers would have read a CDATA section)", f.name)
	}
	switch {
	case script && htmlutil.IsJSONType(f.scriptType):
		// < > & can only appear in JSON strings, where they can be escaped
		out := p.buf[:0]
		for _, c := range b {
			switch c {
			case '<':
				out = append(out, `\u003c`...)
			case '>':
				out = append(out, `\u003e`...)
			case '&':
				out = append(out, `\u0026`...)
			default:
				out = append(out, c)
			}
		}
		p.buf = out
		p.emit(out)
		return
	case !js && set:
		p.emit(b)
		return
	case !js:
		if p.opt.Strict {
			p.err = &Error{pos, fmt.Sprintf("script of type %q containing <, & or ]]> can't be represented in XML", f.scriptType)}
			return
		}
		p.warn(pos, "script of type %q containing <, & or ]]> can't be represented in XML", f.scriptType)
		p.emit(b)
		return
	}
	if bytes.Contains(b, []byte("]]>")) {
		if !cdata && !set {
			p.warn(pos, "%s containing ]]> can't be represented in XML", f.name)
		}
		b = bytes.ReplaceAll(b, []byte("]]>"), []byte(splitCDATAEnd))
	}
	// A line end follows the start marker and precedes the end marker
	// (needed after a line comment), unless the contents start or end with
	// a single one, which takes its place: CutCDATAWrapper removes one.
	start, end := "/*<![CDATA[*/", "/*]]>*/"
	if script {
		start, end = "//<![CDATA[", "//]]>"
	}
	p.emitString(start)
	if !bytes.HasPrefix(b, []byte("\n")) || bytes.HasPrefix(b, []byte("\n\n")) {
		p.emitString("\n")
	}
	p.emit(b)
	if !bytes.HasSuffix(b, []byte("\n")) || bytes.HasSuffix(b, []byte("\n\n")) {
		p.emitString("\n")
	}
	p.emitString(end)
}

// xmlMarkup reports whether the contents b of a script or style element
// have character references and comments outside CDATA sections, which XML
// parsers read as markup.
func xmlMarkup(b []byte) (ref, comment bool) {
	for i := 0; ; i++ {
		j := bytes.IndexAny(b[i:], "&<")
		if j < 0 {
			return
		}
		i += j
		switch r := b[i:]; {
		case r[0] == '&':
			ref = ref || isCharRef(r[1:])
		case bytes.HasPrefix(r, []byte("<!--")):
			comment = true
		case bytes.HasPrefix(r, []byte("<![CDATA[")):
			k := bytes.Index(r, []byte("]]>"))
			if k < 0 {
				return
			}
			i += k + 2
		}
	}
}

// isCharRef reports whether b starts with the rest of a character or
// entity reference after "&" (e.g., "amp;" or "#38;").
func isCharRef(b []byte) bool {
	// up to the first byte which isn't in a name
	i := 0
	for i < len(b) && (b[i] >= 0x80 || i == 0 && b[i] == '#' || strings.IndexByte(xmlNameASCII, b[i]) >= 0) {
		i++
	}
	if i == 0 || i == len(b) || b[i] != ';' {
		return false
	}
	switch name := b[:i]; {
	case name[0] != '#':
		return htmlstream.ValidXMLName(string(name))
	case len(name) > 2 && name[1] == 'x':
		return strings.Trim(string(name[2:]), "0123456789abcdefABCDEF") == ""
	default:
		return len(name) > 1 && strings.Trim(string(name[1:]), "0123456789") == ""
	}
}

// xmlNameASCII are the ASCII characters XML allows in names.
const xmlNameASCII = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_:.-"

// splitCDATAEnd is "]]>" split across two CDATA sections, which XML parsers
// read as "]]>".
const splitCDATAEnd = "]]]]><![CDATA[>"

// rawTextEnd checks the end of the contents of a script or style element,
// before they are wrapped in a CDATA section. An incomplete end tag at the
// end ("</script" followed by "</script>" or the end of the input), which
// HTML parsers keep as text, would be completed by the newline written after
// it, so it is dropped, and so is "<script" in an escaped script (after
// <!--), which would start a double escaped one. A script cut off by the end
// of the input (eof)
// inside an escaped script (<!--<script) can't be closed for HTML parsers:
// they don't end the script at its end tag.
func (p *polyglot) rawTextEnd(pos htmlstream.Pos, name string, b []byte, eof bool) []byte {
	end := "</" + name
	script := name == "script"
	if n := len(b) - len(end); n >= 0 && ascii.EqualFold(b[n:], end) {
		if _, double := scriptEscapeState(b[:n]); !script || !double {
			p.warn(pos, "dropped incomplete end tag at the end of the %s contents (HTML parsers would have kept it as text)", name)
			b = b[:n]
		}
	}
	// "<script" in an escaped script would start a double escaped one
	if n := len(b) - len("<script"); script && n >= 0 && ascii.EqualFold(b[n:], "<script") {
		if escaped, double := scriptEscapeState(b[:n]); escaped && !double {
			p.warn(pos, "dropped incomplete <script> at the end of the script contents (HTML parsers would have kept it as text)")
			b = b[:n]
		}
	}
	if script && eof {
		// the end of the CDATA section is written before the end tag
		if _, double := scriptEscapeState(append(b[:len(b):len(b)], "\n//]]>"...)); double {
			p.warn(pos, "script cut off by the end of the input after <!--<script (HTML parsers will leave it open when parsing the output)")
		}
	}
	return b
}

// scriptEscapeState returns the escape state of the HTML tokenizer after the
// script contents b: escaped (after <!--) and double escaped (after
// <!--<script), as tracked by the Reader.
func scriptEscapeState(b []byte) (escaped, double bool) {
	dashes := 0
	for i := 0; i < len(b); i++ {
		switch c := b[i]; c {
		case '-':
			dashes = min(dashes+1, 2)
			continue
		case '>':
			if dashes == 2 {
				escaped, double = false, false
			}
			dashes = 0
			continue
		case '<':
		default:
			dashes = 0
			continue
		}
		dashes = 0
		if !escaped {
			if bytes.HasPrefix(b[i:], []byte("<!--")) {
				escaped, dashes = true, 2
				i += 3
			}
			continue
		}
		// "<script" or (when double escaped) "</script", followed by a
		// delimiter
		off := 1
		if double {
			off = 2
		}
		if n := off + len("script") + 1; i+n <= len(b) {
			t := b[i : i+n]
			if (!double || t[1] == '/') && ascii.EqualFold(t[off:n-1], "script") && (ascii.IsSpace(t[n-1]) || t[n-1] == '/' || t[n-1] == '>') {
				double = !double
				i += n - 2
			}
		}
	}
	return escaped, double
}

func (p *polyglot) startTag(tok *htmlstream.Token) {
	ns := tok.NS()
	name := p.elemName(tok, ns)
	pos := tok.Pos()
	var info internal.Elem
	var drop []string // attributes left out: the charset declaration of a meta
	p.tfirst = false

	if tok.Untracked() && (ns != htmlstream.NamespaceXHTML || internal.HTMLElem([]byte(name))&internal.Void == 0) {
		// its end tag can't be matched, so the contents are written without
		// the element (a void element, opened with XML syntax, has none)
		p.warn(pos, "dropped %s nested past the stack limits (MaxDepth or MaxStackBytes)", name)
		return
	}
	p.startDecls(tok, name, ns)
	fits := true // in the output depth limit
	if ns == htmlstream.NamespaceXHTML {
		info = internal.HTMLElem([]byte(name))
		switch name {
		case "html":
			p.writeStart(tok, "html", ns, false, nil)
			p.pushOut(outElem{name: "html", decls: p.outDecls()})
			p.pushFrame(tok, frame{out: len(p.out) - 1, name: "html"})
			return
		case "head":
			p.charsetNext, p.headPos = true, p.pos
			p.writeStart(tok, "head", ns, false, nil)
			p.pushOut(outElem{name: "head", decls: p.outDecls()})
			p.pushFrame(tok, frame{out: len(p.out) - 1, name: "head"})
			if p.pendHolds == 0 && len(p.headAfter) == 0 {
				p.charsetWait = true // see placeCharset
			} else {
				p.writeCharset()
			}
			return
		case "body":
			p.closeHead()
			p.writeStart(tok, "body", ns, false, nil)
			p.pushOut(outElem{name: "body", decls: p.outDecls()})
			p.pushFrame(tok, frame{out: len(p.out) - 1, name: "body"})
			return
		case "meta":
			// charset declarations are replaced by the meta element written
			// with the head, keeping the other attributes
			var keep bool
			if drop, keep = metaCharsetAttrs(tok, ns); !keep {
				p.dropElement(tok) // opened with XML syntax
				return
			}
		}
		// before formatting elements are reopened in the table
		foster := p.fosterParent() && (info&internal.TableContent == 0 || name == "input" && !hiddenInput(tok))
		switch name {
		case "basefont", "bgsound", "link", "meta", "noframes", "style", "noscript":
		default:
			if p.impliedHeadNoscript() {
				p.warn(pos, "kept %s in a noscript in the implied head (HTML parsers would have closed the noscript before it)", name)
			}
		}
		if name == "nobr" {
			// HTML parsers reopen formatting elements before looking for an
			// open nobr
			p.reconstruct()
		}
		if name == "a" || name == "nobr" {
			p.closeFormatting(tok, name)
		}
		if info&internal.NoReconstruct == 0 || name == "noscript" {
			// with scripting disabled, noscript is like other elements
			p.reconstruct()
		}
		fits = p.tableFixup(name)
		p.templateChild(pos, name)
		if foster {
			p.warn(pos, "kept %s in a table outside a cell (HTML parsers would have moved it before the table)", name)
		}
		// Start tags HTML parsers ignore in the body, which XML keeps.
		switch name {
		case "td", "th", "tr", "tbody", "thead", "tfoot", "caption", "col", "colgroup":
			if c := p.ctx(); c.tables == 0 && c.templates == 0 {
				p.warn(pos, "kept %s outside a table (HTML parsers would have ignored it)", name)
			}
		case "frame":
			p.warn(pos, "kept frame outside a frameset (HTML parsers would have ignored it)")
		case "frameset":
			p.warn(pos, "wrote frameset in the body (frameset documents aren't supported, and HTML parsers would have ignored it after body content)")
		case "form":
			switch {
			case p.form && p.ctx().templates == 0:
				p.warn(pos, "kept form inside an open form (HTML parsers would have ignored it)")
			case p.inTableBody():
				p.warn(pos, "kept form in a table outside a cell open (HTML parsers would have closed it immediately)")
			}
			p.form = true
		case "table":
			if p.closed.p && p.doc.Quirks() && p.keptP != len(p.out)-1 {
				p.warn(pos, "closed p before table (HTML parsers would have put the table in the p in quirks mode)")
			}
		case "noscript":
			if n := len(p.out); n >= 2 && p.topIs("noscript", false) && p.out[n-2].name == "head" && p.out[n-2].ns == "" {
				p.warn(pos, "kept noscript inside a noscript in the head (HTML parsers would have ignored it)")
			}
		case "option", "optgroup", "hr", "rb", "rtc", "rt", "rp":
			if p.closed.reopened {
				// generate implied end tags, from the current node
				p.warn(pos, "closed elements with reopened formatting elements inside them at <%s> (HTML parsers would have stopped at the reopened elements)", name)
			}
		case "select":
			if p.ctx().selects != 0 {
				p.warn(pos, "kept select inside an open select (HTML parsers would have ignored it and closed the open one)")
			}
		case "xmp", "plaintext":
			p.warn(pos, "converted %s to pre", name)
			name = "pre"
		case "image":
			name = "img"
		}
	} else {
		if p.fosterParent() {
			p.warn(pos, "kept %s in a table outside a cell (HTML parsers would have moved it before the table)", name)
		}
		p.templateChild(pos, name)
		p.reconstruct()
		fits = len(p.out) < p.opt.MaxDepth
	}

	if !fits {
		p.warn(pos, "dropped %s nested past the output depth limit (Options.MaxDepth)", name)
		p.dropElement(tok)
		return
	}
	// XML forbids the xmlns prefix in element names
	if !htmlstream.ValidXMLName(name) || !p.prefixBound(name) || strings.HasPrefix(name, "xmlns:") {
		p.warn(pos, "dropped element %q (can't be represented in XML)", name)
		p.dropElement(tok)
		return
	}
	if prefix, _, ok := strings.Cut(name, ":"); ok {
		// HTML parsers don't resolve prefixes of element names
		if xmlNS, _ := p.lookupPrefix(prefix); xmlNS != ns {
			p.warnNS(pos, "element", name, prefix, xmlNS, ns)
		} else {
			// the local name still differs
			if p.firstWarning(onceKey{"element name", prefix, xmlNS}) {
				p.warn(pos, "element %q is named %q for XML parsers and %q for HTML parsers (will not warn again for this prefix and namespace)", name, name[len(prefix)+1:], name)
			}
		}
	}

	void := info&internal.Void != 0
	p.writeStart(tok, name, ns, void, drop)
	if void || !tok.Opens() {
		if void {
			// with XML syntax, it opens an element, closed by its end tag or an
			// implied one
			p.pushFrame(tok, frame{out: -1, name: name, void: true})
		} else {
			// self-closed (a foreign element, or with XML syntax): its end
			// tag follows
			e := outElem{name: name}
			if ns != htmlstream.NamespaceXHTML {
				e.ns = ns
			}
			p.pushOut(e)
			p.popOut()
		}
		return
	}
	e := outElem{name: name, marker: info&internal.Marker != 0, decls: p.outDecls()}
	if ns != htmlstream.NamespaceXHTML {
		e.ns = ns
		e.integration = isIntegrationPoint(tok, name, ns)
	}
	f := frame{name: name, text: info&(internal.RCDATA|internal.RawText) != 0}
	if tok.SelfClosing() && ns == htmlstream.NamespaceXHTML && info&(internal.RCDATA|internal.RawText|internal.Script|internal.Noscript) != 0 {
		// only without XML syntax, which makes the tag not open the element
		// (see selfClosedText)
		f.selfClosed = htmlName(tok)
	}
	if info&internal.Formatting != 0 {
		f.formatting, f.startOff, f.boundOff = true, len(p.starts), len(p.bounds)
		if !p.startOmit {
			// unwrapped and removed elements aren't reopened
			p.starts = append(p.starts, p.buf...)
			f.start = p.starts[f.startOff:len(p.starts):len(p.starts)]
			f.bound = p.boundPrefixes(tok, ns)
		}
		e.key = p.formattingKey(tok, name)
		p.seq++
		e.seq = p.seq
		p.noahsArk(e.key)
	}
	p.pushOut(e)
	f.out, f.marker = len(p.out)-1, p.markers
	if name == "script" && ns == htmlstream.NamespaceXHTML {
		f.scriptType = htmlutil.ScriptType(tok)
	}
	p.pushFrame(tok, f)
	if ns == htmlstream.NamespaceXHTML && (name == "pre" || name == "textarea" || name == "listing") && !p.startOmit {
		p.stripNL, p.nlWarned = true, false
		p.keptNL = info&(internal.RawText|internal.Plaintext) != 0
		p.preEnd = p.written()
	}
}

// impliedHeadNoscript reports whether the current output element is a
// noscript in a head without a start tag. HTML parsers (with scripting
// disabled) close it for content other than link, meta, style, etc. The
// Reader follows these rules only in an explicit head, since it doesn't track
// whether an implied head has ended. htmlutil.Document starts the body
// for other elements, which closes the head and the noscript, but not for
// text or head content.
func (p *polyglot) impliedHeadNoscript() bool {
	n := len(p.out)
	return n >= 2 && p.topIs("noscript", false) && p.out[n-2].name == "head" && p.out[n-2].ns == "" && p.out[n-2].synthetic
}

// isIntegrationPoint reports whether a start tag for an element named name
// (with the SVG case adjustments) in namespace ns opens an HTML or MathML
// text integration point.
func isIntegrationPoint(tok *htmlstream.Token, name, ns string) bool {
	switch ns {
	case htmlstream.NamespaceSVG:
		return name == "foreignObject" || name == "desc" || name == "title"
	case htmlstream.NamespaceMathML:
		switch name {
		case "mi", "mo", "mn", "ms", "mtext":
			return true
		case "annotation-xml":
			if a, ok := tok.Attr("encoding"); ok {
				v := a.Value()
				return ascii.EqualFold(v, "text/html") || ascii.EqualFold(v, "application/xhtml+xml")
			}
		}
	}
	return false
}

// templateChild checks a start tag in a template. HTML parsers parse
// template contents like the contents of a table, a colgroup, a table section
// or a row, or like body content, depending on the first child element. Table
// parts which don't fit it are ignored or put in implied elements (e.g., td
// after tr gets an implied tr), and nothing but col and template is allowed
// after col. In table contents, table and form are ignored. The same rules
// apply in other elements in the template (e.g., an a after tr), until a
// table part or another template changes them (the Reader closes the other
// elements for table parts).
func (p *polyglot) templateChild(pos htmlstream.Pos, name string) {
	i := p.ctx().tmpl
	if i < 0 {
		return
	}
	t := &p.out[i]
	if t.ns != "" || t.name != "template" {
		return // the foreign content rules or the rules for t apply
	}
	var kind byte
	switch name {
	case "base", "basefont", "bgsound", "link", "meta", "noframes", "script", "style", "template", "title":
		if t.tmode != 'c' || name == "template" {
			return // head content, which doesn't select the kind
		}
		kind = 'b' // ignored after col
	case "caption", "colgroup", "tbody", "tfoot", "thead":
		kind = 't'
	case "col":
		kind = 'c'
	case "tr":
		kind = 'r'
	case "td", "th":
		kind = 'd'
	default:
		kind = 'b'
	}
	switch {
	case t.tmode == 0:
		t.tmode, t.tfirst = kind, name
		p.tfirst = true
	case kind == t.tmode:
	case kind == 'b' && t.tmode != 'c' && (name == "table" || name == "form"):
		// the table rules ignore them in a template
		p.warn(pos, "kept %s after %s in a template (HTML parsers would have ignored it)", name, t.tfirst)
	case kind == 'b' && t.tmode != 'c':
		// put in the template like other content in a table
	default:
		p.warn(pos, "kept %s after %s in a template (HTML parsers would have ignored it or put it in implied table elements)", name, t.tfirst)
	}
}

// templateIgnoresEnd reports whether the current output element is a
// template where HTML parsers ignore end tags other than template: before
// its first child element, or after col.
func (p *polyglot) templateIgnoresEnd() bool {
	t := p.top()
	return t != nil && t.name == "template" && t.ns == "" && (t.tmode == 0 || t.tmode == 'c')
}

// spaceReconstructs reports whether HTML parsers reopen the active formatting
// elements for whitespace at the current position. They insert it as is when
// the current node is a table, table section, row or colgroup, or a template
// parsed like a colgroup, and otherwise apply the "in body" rules in the body
// and in template contents (also in the head), which reopen them.
func (p *polyglot) spaceReconstructs() bool {
	if p.fosterParent() {
		return false
	}
	if t := p.top(); t != nil && t.name == "template" && t.ns == "" && t.tmode == 'c' {
		return false
	}
	return p.doc.Part() == htmlutil.InBody || p.ctx().templates != 0
}

// fosterParent reports whether HTML parsers move content other than table
// content at the current position before the table (foster parenting): the
// innermost open element is a table, table section or row, or a colgroup,
// which they close first.
func (p *polyglot) fosterParent() bool {
	t := p.top()
	if t == nil || t.ns != "" {
		return false
	}
	switch t.name {
	case "table", "tbody", "thead", "tfoot", "tr", "colgroup":
		return true
	}
	return false
}

// hiddenInput reports whether an input start tag has the type hidden, which
// HTML parsers keep in a table.
func hiddenInput(tok *htmlstream.Token) bool {
	a, ok := tok.Attr("type")
	return ok && ascii.EqualFold(a.Value(), "hidden")
}

// inTableBody reports whether the innermost open element is a table, table
// section or row.
func (p *polyglot) inTableBody() bool {
	t := p.top()
	if t == nil || t.ns != "" {
		return false
	}
	switch t.name {
	case "table", "tbody", "thead", "tfoot", "tr":
		return true
	}
	return false
}

// tableFixup writes implied table elements before a start tag. It reports
// whether the element fits in the output depth limit (Options.MaxDepth)
// after them; if not, they aren't written.
func (p *polyglot) tableFixup(name string) bool {
	switch name {
	case "caption", "colgroup", "col", "tbody", "thead", "tfoot", "tr", "td", "th":
		// HTML parsers clear the stack back to the table (or template)
		// context, which closes reopened formatting elements in it (e.g., in
		// a template, where they aren't foster parented); they are reopened
		// again before the next phrasing content
		n := len(p.out)
		for n > 0 && p.out[n-1].reopened {
			n--
		}
		p.popTo(n)
	}
	// close implied elements, and count those to write
	n := 0
	switch name {
	case "thead", "tbody", "tfoot", "caption", "colgroup":
		for p.topIs("tbody", true) || p.topIs("tr", true) || p.topIs("colgroup", true) {
			p.popOut()
		}
	case "tr":
		for p.topIs("tr", true) || p.topIs("colgroup", true) {
			p.popOut()
		}
		if p.topIs("table", false) {
			n = 1 // tbody
		}
	case "td", "th":
		if p.topIs("colgroup", true) {
			p.popOut()
		}
		if p.topIs("table", false) {
			n = 2 // tbody and tr
		} else if t := p.top(); t != nil && t.ns == "" && (t.name == "tbody" || t.name == "thead" || t.name == "tfoot") {
			n = 1 // tr
		}
	case "col":
		for p.topIs("tbody", true) || p.topIs("tr", true) {
			p.popOut()
		}
		if p.topIs("table", false) {
			n = 1 // colgroup
		}
	case "template":
		// HTML parsers keep it in a colgroup
	default:
		if p.topIs("colgroup", true) {
			p.popOut()
		}
	}
	if len(p.out)+n >= p.opt.MaxDepth {
		return false
	}
	switch {
	case n == 0:
	case name == "col":
		p.tagString("<colgroup>", htmlTag("colgroup", true))
		p.pushOut(outElem{name: "colgroup", synthetic: true})
	default:
		if p.topIs("table", false) {
			p.tagString("<tbody>", htmlTag("tbody", true))
			p.pushOut(outElem{name: "tbody", synthetic: true})
		}
		if name == "td" || name == "th" {
			p.tagString("<tr>", htmlTag("tr", true))
			p.pushOut(outElem{name: "tr", synthetic: true})
		}
	}
	return true
}

// pushFrame pushes the frame of the element opened by tok.
func (p *polyglot) pushFrame(tok *htmlstream.Token, f frame) {
	f.pos = tok.Pos()
	if p.frames.Push(tok, f) && f.start != nil {
		p.reopens++
	}
}

// dropElement skips writing a start tag, and the end tag of the element it
// opens.
// metaCharsetAttrs returns the attributes of the meta start tag tok which
// declare the charset, which are dropped, and whether the element has other
// attributes, so that it is kept.
func metaCharsetAttrs(tok *htmlstream.Token, ns string) (drop []string, keep bool) {
	if _, ok := tok.Attr("charset"); ok {
		drop = append(drop, "charset")
	}
	if a, ok := tok.Attr("http-equiv"); ok && ascii.EqualFold(a.Value(), "content-type") {
		drop = append(drop, "http-equiv", "content")
	}
	keep = len(drop) == 0
	for a := range tok.Attrs() {
		if keep {
			break
		}
		an := attrName(a, ns)
		keep = an != "xmlns" && !slices.Contains(drop, an)
	}
	return drop, keep
}

func (p *polyglot) dropElement(tok *htmlstream.Token) {
	if tok.Kind() == htmlstream.StartTag {
		p.pushFrame(tok, frame{out: -1, name: htmlName(tok)})
	}
}

func (p *polyglot) endTag(tok *htmlstream.Token) {
	if tok.Is("form") && !tok.Implied() && tok.NS() != htmlstream.NamespaceSVG && tok.NS() != htmlstream.NamespaceMathML {
		p.form = false // HTML parsers clear the form element pointer even for a stray end tag
	}
	if tok.Stray() {
		if _, xmlSyntax := internal.TokenMode(tok); xmlSyntax {
			// the Reader closes a void element before its contents other
			// than whitespace (see htmlstream.Reader.SetXMLSyntax)
			switch name := htmlName(tok); {
			case name == "br":
				p.warn(tok.Pos(), "wrote br for </br> (XML parsers would have read it as an end tag)")
			case internal.HTMLElem([]byte(name))&internal.Void != 0:
				p.warn(tok.Pos(), "dropped </%s> (XML parsers would have read it as an end tag)", name)
			}
		}
		if p.topIs("colgroup", true) && !tok.Is("col") && !tok.Is("colgroup") && !tok.Is("template") {
			// HTML parsers close the implied colgroup, like the Reader does an
			// explicit one, then handle the end tag in the table
			p.popOut()
		}
		switch {
		case (tok.Is("br") || tok.Is("p")) && p.templateIgnoresEnd():
			// ignored by HTML parsers
		case tok.Is("br"):
			if p.fosterParent() {
				p.warn(tok.Pos(), "wrote br for </br> in a table outside a cell (HTML parsers would have put it before the table)")
			}
			p.reconstruct()
			if len(p.out) >= p.opt.MaxDepth {
				p.warn(tok.Pos(), "dropped br for </br> nested past the output depth limit (Options.MaxDepth)")
				break
			}
			br := htmlTag("br", true)
			br.Void = true
			if p.inForeign() {
				p.tagString("<br"+xmlnsXHTML+" />", br)
			} else {
				p.tagString("<br />", br)
			}
		case tok.Is("p") && p.keptP >= 0 && p.frames.Len() <= p.keptDepth:
			// closes the p kept open around a table (see keepP)
			p.popTo(p.keptP)
		case tok.Is("p") && (p.doc.Part() == htmlutil.InBody || p.ctx().templates != 0):
			if len(p.out) >= p.opt.MaxDepth {
				p.warn(tok.Pos(), "dropped p for </p> nested past the output depth limit (Options.MaxDepth)")
				break
			}
			if p.fosterParent() {
				p.warn(tok.Pos(), "wrote p for </p> in a table outside a cell (HTML parsers would have put it before the table)")
			}
			if p.inForeign() {
				p.tagString("<p"+xmlnsXHTML+">", htmlTag("p", true))
			} else {
				p.tagString("<p>", htmlTag("p", true))
			}
			end := htmlTag("p", false)
			end.ID = p.startID
			p.tagString("</p>", end)
		case tok.Is("tbody") || tok.Is("tr") || tok.Is("colgroup"):
			p.impliedTableEnd(tok)
		case tok.Is("table"):
			p.templateTableEnd(tok)
		default:
			p.strayFormattingEnd(tok)
		}
		return
	}
	if f, ok := p.frames.Top(); ok && f.void && p.frames.Closes(tok) {
		// a void element opened with XML syntax, written with its start tag
		p.frames.Pop(tok)
		return
	}
	if tok.Implied() && p.closed.first == "" {
		p.closed.first = htmlName(tok)
	}
	if tok.Implied() && tok.NS() == htmlstream.NamespaceXHTML {
		// record what the implied end tags before the next token close, to
		// warn if HTML parsers handle that token differently
		if p.closed.html == "" {
			p.closed.html = htmlName(tok)
		}
		p.closed.names = append(p.closed.names, htmlName(tok))
		var buf [16]byte
		if p.closed.special == "" && internal.HTMLElem(tok.AppendLocalName(buf[:0]))&internal.Special != 0 {
			p.closed.special = htmlName(tok)
		}
		if p.closed.unsound == "" && tok.Unsound() {
			p.closed.unsound = htmlName(tok)
		}
		if tok.Is("p") {
			p.closed.p = true
		}
	}
	f, ok := p.frames.Pop(tok)
	if ok && f.start != nil {
		p.reopens--
	}
	if ok && f.formatting {
		// after the reopen entry below copies them
		defer func() { p.starts, p.bounds = p.starts[:f.startOff], p.bounds[:f.boundOff] }()
	}
	if ok && tok.Implied() && f.name == "noscript" {
		if h, ok := p.frames.Top(); ok && h.out < 0 && h.name == "head" {
			p.misplacedNoscript = true
		}
	}
	if ok && !tok.Implied() && tok.NS() == htmlstream.NamespaceXHTML && (tok.Is("body") || tok.Is("html")) {
		// HTML parsers put content after body and html end tags in the
		// body, in the elements still open: the end tags (also of misplaced
		// start tags) don't close them
		p.keepOpen(tok)
	}
	if !ok || f.out < 0 {
		return
	}
	switch f.name {
	case "head":
		return // written before the body
	case "body", "html":
		return // written at the end
	}
	if f.out >= len(p.out) {
		// already closed (e.g., a noscript in the head, closed with it):
		// HTML parsers ignore the end tag
		if !tok.Implied() {
			p.keepOpen(tok)
		}
		return
	}
	if !tok.Implied() && f.start != nil {
		for i := len(p.out) - 1; i > f.out && !p.out[i].marker; i-- {
			if e := &p.out[i]; e.reopened && e.name == f.name && e.ns == "" {
				// the adoption agency algorithm finds the reopened one first
				p.warn(tok.Pos(), "closed an open %s with a reopened %s inside it at </%s> (HTML parsers would have closed only the reopened one)", f.name, f.name, f.name)
				break
			}
		}
		for i, r := range p.pending {
			if r.name == f.name && r.marker == p.markers {
				// the adoption agency algorithm finds the closed element first
				p.warn(tok.Pos(), "closed an open %s at </%s> (HTML parsers would have stopped reopening a closed %s and kept the open one)", f.name, f.name, f.name)
				p.pending = slices.Delete(p.pending, i, i+1)
				break
			}
		}
	}
	if !tok.Implied() && f.name == "template" && p.out[f.out].ns == "" {
		p.templateMarkerEnd(tok, f)
	}
	if !tok.Implied() {
		if p.out[f.out].ns == "" {
			p.checkEndTag(tok, f.name)
		} else if crossed := cmp.Or(p.closed.html, p.htmlAbove(f.out)); crossed != "" {
			// With an HTML current node (from the input, or a reopened
			// formatting element), HTML parsers apply the HTML rules, which
			// never match a foreign element and stop at the integration point
			// (a special element).
			p.warn(tok.Pos(), "closed HTML element %s at </%s> of %s element (HTML parsers would have ignored the end tag)", crossed, f.name, foreignArticle(p.out[f.out].ns))
		}
	}
	if tok.Implied() && p.closed.html == "" {
		// reopened elements closed by the implied end tag (e.g., of an
		// integration point)
		p.closed.html = p.htmlAbove(f.out)
	}
	if tok.Implied() {
		for _, e := range p.out[f.out+1:] {
			p.closed.reopened = p.closed.reopened || e.reopened
		}
	}
	if tok.Implied() && f.name == "p" && p.out[f.out].ns == "" && f.out == len(p.out)-1 && len(p.closed.names) == 1 && p.closed.first == "p" &&
		p.quirksOut && p.doc.Quirks() && p.removeAt != f.out {
		// closed at the next token, unless it is a table start tag (see
		// keepP)
		p.pClose = f.out
		return
	}
	// close reopened formatting elements and implied table elements inside
	// the element; the element itself is reopened if it is a formatting
	// element closed by a misnested end tag
	p.popTo(f.out + 1)
	e := p.popOut()
	if tok.Implied() && tok.Unsound() && f.start != nil && !e.noah && !p.removed(e.id) {
		p.tentative = append(p.tentative, reopen{name: f.name, written: e.written, start: slices.Clone(f.start), id: e.id, marker: f.marker, key: slices.Clone(e.key), seq: e.seq, implied: true, decls: e.decls, bound: slices.Clone(f.bound)})
	}
}

// strayFormattingEnd handles a stray end tag which may be of a formatting
// element. HTML parsers apply it to the last element with its name in the
// list of active formatting elements after the last marker (adoption agency
// algorithm): a reopened element in the output, which is closed (and the
// reopened elements above it are reopened again), or a closed element still to
// be reopened, which isn't reopened. Other stray end tags are ignored.
func (p *polyglot) strayFormattingEnd(tok *htmlstream.Token) {
	seq, oi, pi := 0, -1, -1 // the last one in the list of active formatting elements
	for i := int(p.ctx().reopened); i >= 0; i = int(p.out[i-1].ctx.reopened) {
		if e := &p.out[i]; tok.Is(e.name) && e.seq > seq {
			seq, oi = e.seq, i
		}
		if i == 0 {
			break
		}
	}
	for i, r := range p.pending {
		if r.marker == p.markers && tok.Is(r.name) && r.seq > seq {
			seq, oi, pi = r.seq, -1, i
		}
	}
	switch {
	case pi >= 0:
		p.pending = slices.Delete(p.pending, pi, pi+1)
	case oi >= 0:
		if int(p.ctx().input) > oi {
			// the element from the input inside it would be closed too
			p.warn(tok.Pos(), "ignored </%s> of a reopened element with %s open inside it (HTML parsers would have closed %s)", p.out[oi].name, p.out[oi+1].name, p.out[oi].name)
			return
		}
		p.popTo(oi + 1)
		p.popOut()
	}
}

// keepOpen records the elements closed by the implied end tags before an end
// tag which doesn't close them for HTML parsers. They are open for HTML
// parsers, so they aren't reopened (for whitespace after them), and other
// content after them is reported.
func (p *polyglot) keepOpen(tok *htmlstream.Token) {
	if p.closed.first != "" && p.closed.first != "body" {
		p.keptOpen, p.keptBy = p.closed.first, htmlName(tok)
		p.kept = append(p.kept[:0], p.closed.names...)
		p.pending, p.tentative = p.pending[:0], p.tentative[:0]
	}
}

// keepP keeps a p open around a table like HTML parsers in quirks mode, when
// the output is in quirks mode too: a table start tag doesn't close an open
// p, which the Reader closes with an implied end tag before it. The p stays
// open after the table, until the end tag of an element around it (with
// the warnings for the p closed by it, see checkEndTag), a p end tag, or a
// start tag which closes a p. It is closed before the start tag of
// another element which can contain elements, which would be in the p for
// HTML parsers, with a warning: such an element can contain a start tag
// which closes the p for HTML parsers, and the elements open in it, which
// the Reader keeps open.
func (p *polyglot) keepP(tok *htmlstream.Token) {
	if p.pClose >= 0 {
		if tok.Kind() == htmlstream.StartTag && tok.NS() == htmlstream.NamespaceXHTML && tok.Is("table") && !tok.Untracked() {
			if p.keptP >= 0 {
				// in a table cell inside the kept p
				p.keptOuter = append(p.keptOuter, [2]int{p.keptP, p.keptDepth})
			}
			p.keptP, p.keptDepth = p.pClose, p.frames.Len()
		} else {
			p.popTo(p.pClose)
		}
		p.pClose = -1
	}
	if p.keptP < 0 || p.frames.Len() > p.keptDepth {
		return
	}
	if tok.Kind() == htmlstream.EndTag && !tok.Implied() && !tok.Stray() && !tok.Is("body") && !tok.Is("html") && p.closed.special == "" {
		// the end tag of an element around it closes it, like an implied end
		// tag (see checkEndTag)
		p.closed.special = "p"
	}
	if tok.Kind() != htmlstream.StartTag {
		return
	}
	var buf [16]byte
	info := internal.HTMLElem(tok.AppendLocalName(buf[:0]))
	switch {
	case tok.NS() != htmlstream.NamespaceXHTML:
	case tok.Is("table"), tok.Is("html"), tok.Is("head"), tok.Is("body"):
		// in the p for HTML parsers, or merged with the open element
		return
	case info&internal.ClosesP != 0:
		p.popTo(p.keptP)
		return
	case info&(internal.Void|internal.RawText|internal.RCDATA|internal.Script) != 0:
		// can't contain elements
		return
	}
	p.warn(tok.Pos(), "closed p at <%s> after a table in it (HTML parsers would have kept the p open in quirks mode)", htmlName(tok))
	p.popTo(p.keptP)
}

// impliedTableEnd handles a stray tbody, tr or colgroup end tag, which HTML
// parsers match to the element they imply (written by tableFixup). It closes
// the implied element if only implied elements are open inside it; otherwise
// HTML parsers also close the cell or other elements open inside it, which
// the Reader keeps open (also from foreign content in a cell, where they
// apply the HTML rules to the end tag).
func (p *polyglot) impliedTableEnd(tok *htmlstream.Token) {
	if p.ctx().implied == 0 {
		return
	}
	for i := len(p.out) - 1; i >= 0; i-- {
		e := &p.out[i]
		if e.ns == "" && (e.name == "table" || e.name == "template") {
			return
		}
		if e.ns != "" || !e.synthetic || !tok.Is(e.name) {
			continue
		}
		for j := i + 1; j < len(p.out); j++ {
			if !p.out[j].synthetic {
				p.warn(tok.Pos(), "kept %s open at </%s> of an implied element (HTML parsers would have closed it)", p.out[j].name, e.name)
				return
			}
		}
		p.popTo(i + 1)
		p.popOut()
		return
	}
}

// templateMarkerEnd warns about a template end tag which closes a marker
// element (a cell, caption, applet, marquee, object or template) open inside
// the template. HTML parsers clear the list of active formatting elements up
// to the last marker, which is the inner element's, so the template's marker
// stays in the list: formatting elements closed in the template before the
// inner element are reopened after the template, and formatting elements
// from before the template are no longer reopened once closed. Nothing
// differs without formatting elements open or waiting to be reopened.
func (p *polyglot) templateMarkerEnd(tok *htmlstream.Token, f frame) {
	inner := ""
	for _, name := range p.closed.names {
		if internal.HTMLElem([]byte(name))&internal.Marker != 0 {
			inner = name
			break
		}
	}
	if inner == "" {
		return
	}
	// the reopened elements below the template (which isn't one)
	if len(p.pending) != 0 || p.reopens != 0 || p.out[f.out].ctx.nreopened != 0 {
		p.warn(tok.Pos(), "closed %s at </template> (HTML parsers would have kept the template's formatting marker, changing which formatting elements are reopened)", inner)
	}
}

// templateTableEnd handles a stray table end tag. In template contents parsed
// like a table, HTML parsers close the row, table section or caption it is
// in (and the elements inside it) like for the end of a table, and then
// ignore it, while the Reader keeps them open. In a cell, they ignore it.
func (p *polyglot) templateTableEnd(tok *htmlstream.Token) {
	if p.ctx().templates == 0 {
		return
	}
	for i := len(p.out) - 1; i >= 0; i-- {
		e := &p.out[i]
		if e.ns != "" {
			continue
		}
		switch e.name {
		case "td", "th", "table", "template":
			return
		case "tr", "tbody", "thead", "tfoot", "caption":
			p.warn(tok.Pos(), "kept %s open at </table> in a template (HTML parsers would have closed it)", e.name)
			return
		}
	}
}

// checkEndTag warns when HTML parsers handle an explicit end tag of the
// HTML element name differently from the Reader, which closes the elements
// inside it (recorded in p.closed.special and p.closed.unsound):
//
//   - a formatting element's end tag closing a special (block-level) element
//     runs the adoption agency algorithm, which keeps the special element
//     open and clones the formatting element into it;
//   - the end tag of other non-special elements is ignored when a special
//     element is open inside it, and so is the end tag of noscript (special,
//     but without its own end tag rules), which is reported for content
//     after it (see keepOpen);
//   - a form end tag removes only the form from the stack of open elements
//     (other elements inside it are closed by their own implied end tags).
func (p *polyglot) checkEndTag(tok *htmlstream.Token, name string) {
	info := internal.HTMLElem([]byte(name))
	switch {
	case info&internal.Formatting != 0 && p.closed.special != "":
		p.warn(tok.Pos(), "closed %s at </%s> (HTML parsers would have kept %s open and cloned %s into it)", p.closed.special, name, p.closed.special, name)
	case info&(internal.Formatting|internal.Special) == 0 && p.closed.special != "":
		p.warn(tok.Pos(), "closed %s at </%s> (HTML parsers would have ignored the end tag)", p.closed.special, name)
	case name == "noscript" && p.closed.special != "":
		p.keepOpen(tok)
	case name == "form" && p.closed.unsound != "":
		p.warn(tok.Pos(), "closed %s at </form> (HTML parsers would have kept it open)", p.closed.unsound)
	}
}

// Names and attributes.

func (p *polyglot) elemName(tok *htmlstream.Token, ns string) string {
	name := htmlName(tok)
	if ns == htmlstream.NamespaceSVG {
		return htmlstream.AdjustSVGTagName(name)
	}
	return name
}

// writeStart writes a start tag, which is also kept in p.buf, with the ranges
// of its attributes in p.attrs for the edits of the Tag hook, leaving out the
// attributes named in drop. The namespace declarations were collected by
// startDecls.
func (p *polyglot) writeStart(tok *htmlstream.Token, name, ns string, void bool, drop []string) {
	b := append(p.buf[:0], '<')
	b = append(b, name...)
	html := ns == htmlstream.NamespaceXHTML
	seen, expanded, attrs := p.seen[:0], p.expanded[:0], p.attrs[:0]
	// polyglot markup has both lang and xml:lang on HTML elements, with the
	// same value (the first attribute with the name, like the attributes
	// written)
	var lang, xmlLang htmlstream.Attr
	hasLang, hasXMLLang := false, false
	if html {
		lang, hasLang = tok.Attr("lang")
		xmlLang, hasXMLLang = tok.Attr("xml:lang")
	}
	// the default namespace declaration of the tag, written in place of an
	// xmlns attribute, or after the attributes
	var xmlns string
	switch {
	case html && p.inForeign(), name == "html" && html:
		xmlns = xmlnsXHTML
	case name == "svg" && ns == htmlstream.NamespaceSVG:
		xmlns = ` xmlns="` + htmlstream.NamespaceSVG + `"`
	case name == "math" && ns == htmlstream.NamespaceMathML:
		xmlns = ` xmlns="` + htmlstream.NamespaceMathML + `"`
	}
	big := tok.NumAttrs() > internal.IndexMin
	if big {
		p.seenSet.Reset(tok.NumAttrs())
		p.expandedSet.Reset(tok.NumAttrs())
	}
	for a := range tok.Attrs() {
		an := attrName(a, ns)
		if big {
			if _, ok := p.seenSet.M[an]; ok {
				continue
			}
			p.seenSet.M[an] = struct{}{}
		} else {
			if slices.Contains(seen, an) {
				continue
			}
			seen = append(seen, an)
		}
		if slices.Contains(drop, an) {
			continue
		}
		if an == "xmlns" {
			b = append(b, xmlns...)
			xmlns = ""
			continue
		}
		if !htmlstream.ValidXMLName(an) || !p.prefixBound(an) {
			p.warn(tok.Pos(), "dropped attribute %q (can't be represented in XML)", an)
			continue
		}
		p.raw = a.AppendValue(p.raw[:0])
		v := p.sanitize(tok.Pos(), p.raw)
		if prefix, local, ok := strings.Cut(an, ":"); ok && prefix == "xmlns" {
			if why := invalidDecl(local, string(v)); why != "" {
				p.warn(tok.Pos(), "dropped namespace declaration %q (invalid in XML: %s)", an, why)
				continue
			}
		} else if ok {
			xmlNS, _ := p.lookupPrefix(prefix)
			// XML parsers reject two attributes with the same namespace and
			// local name
			k := xmlAttrName{xmlNS, local}
			var dup bool
			if big {
				_, dup = p.expandedSet.M[k]
				p.expandedSet.M[k] = struct{}{}
			} else {
				dup = slices.Contains(expanded, k)
				expanded = append(expanded, k)
			}
			if dup {
				p.warn(tok.Pos(), "dropped attribute %q (same namespace and local name as another attribute for XML parsers)", an)
				continue
			}
			switch htmlNS := foreignAttrNS(an, ns); {
			case xmlNS == htmlNS:
			case an == "xml:lang" && html:
				// HTML parsers use lang for the language, and XML parsers
				// xml:lang
				if hasLang && !ascii.EqualFold(a.Value(), lang.Value()) {
					if p.firstWarning(onceKey{kind: "xml:lang"}) {
						p.warn(tok.Pos(), "wrote xml:lang differing from lang with the value of lang (to match the lang HTML parsers use) (will not warn again)")
					}
					// replaced like the lang value, which is reported for lang
					v = p.xmlValue(lang)
				}
			default:
				p.warnNS(tok.Pos(), "attribute", an, prefix, xmlNS, htmlNS)
			}
		}
		off := len(b)
		b = append(b, ' ')
		b = append(b, an...)
		b = append(b, `="`...)
		b = AppendEscapeAttr(b, v)
		b = append(b, '"')
		attrs = append(attrs, attrRange{name: an, off: off, end: len(b)})
	}
	b = append(b, xmlns...)
	for _, d := range p.tagDecls {
		if d.added {
			b = append(b, ` xmlns:`...)
			b = append(b, d.prefix...)
			b = append(b, `="`...)
			b = AppendEscapeAttr(b, d.uri)
			b = append(b, '"')
		}
	}
	switch {
	case hasLang && !hasXMLLang:
		off := len(b)
		b = append(b, ` xml:lang="`...)
		// replaced like the lang value, which was already reported
		b = AppendEscapeAttr(b, p.xmlValue(lang))
		b = append(b, '"')
		attrs = append(attrs, attrRange{name: "xml:lang", off: off, end: len(b)})
	case hasXMLLang && !hasLang:
		off := len(b)
		b = append(b, ` lang="`...)
		b = AppendEscapeAttr(b, p.xmlValue(xmlLang))
		b = append(b, '"')
		attrs = append(attrs, attrRange{name: "lang", off: off, end: len(b)})
	}
	if void {
		b = append(b, " />"...)
	} else {
		b = append(b, '>')
	}
	p.buf, p.seen, p.expanded, p.attrs = b, seen, expanded, attrs
	p.tag(b, Tag{Start: true, Void: void, Name: name, NS: ns, Token: tok, tfirst: p.tfirst})
}

// xmlValue returns the value of an attribute with the characters not allowed
// in XML replaced, in p.raw or p.ebuf.
func (p *polyglot) xmlValue(a htmlstream.Attr) []byte {
	p.raw = a.AppendValue(p.raw[:0])
	if htmlstream.ValidXMLChars(p.raw) {
		return p.raw
	}
	p.ebuf, _ = htmlstream.AppendXMLChars(p.ebuf[:0], p.raw)
	return p.ebuf
}

// htmlName returns the name HTML parsers give the element of a tag, before
// adjusting the case of SVG element names: ASCII-lowercased, with NUL
// replaced with U+FFFD.
func htmlName(tok *htmlstream.Token) string {
	var buf [32]byte
	return intern(tok.AppendLocalName(buf[:0]))
}

// attrName returns the name of an attribute as written: the name HTML parsers
// give it (see [htmlstream.Attr.AppendName]), with the case they give SVG and
// MathML attribute names.
func attrName(a htmlstream.Attr, ns string) string {
	var buf [32]byte
	an := intern(a.AppendName(buf[:0]))
	switch ns {
	case htmlstream.NamespaceSVG:
		an = htmlstream.AdjustSVGAttrName(an)
	case htmlstream.NamespaceMathML:
		an = htmlstream.AdjustMathMLAttrName(an)
	}
	return an
}

// intern returns name as a string, without allocating for the names in
// knownNames.
func intern(name []byte) string {
	if s, ok := knownNames[string(name)]; ok {
		return s
	}
	return string(name)
}

// knownNames maps common element and attribute names (as HTML parsers give
// them, before adjusting the case of SVG and MathML names) to themselves.
var knownNames = func() map[string]string {
	m := map[string]string{}
	for _, name := range strings.Fields(`
		a abbr acronym address applet area article aside audio b base basefont
		bdi bdo bgsound big blockquote body br button canvas caption center cite
		code col colgroup data datalist dd del details dfn dialog dir div dl dt
		em embed fieldset figcaption figure font footer form frame frameset h1
		h2 h3 h4 h5 h6 head header hgroup hr html i iframe image img input ins
		kbd label legend li link listing main map mark marquee menu meta meter
		nav nobr noembed noframes noscript object ol optgroup option output p
		param picture plaintext pre progress q rb rp rt rtc ruby s samp script
		search section select slot small source span strike strong style sub
		summary sup table tbody td template textarea tfoot th thead time title
		tr track tt u ul var video wbr xmp

		svg g path rect circle ellipse line polyline polygon text tspan defs
		use symbol stop pattern mask filter marker desc clippath lineargradient
		radialgradient foreignobject
		math mi mo mn ms mtext mrow msup msub msubsup mfrac msqrt mroot mtable
		mtr mtd mspace mstyle munder mover munderover semantics annotation
		annotation-xml

		id class style title lang dir hidden tabindex accesskey role
		xml:lang xml:space xml:base xmlns xmlns:epub xmlns:xlink
		epub:type epub:prefix xlink:href
		href src srcset sizes alt width height rel type media name content
		charset http-equiv colspan rowspan headers scope span align valign
		border cellpadding cellspacing summary start value reversed datetime
		cite target download hreflang action method for placeholder checked
		disabled selected multiple readonly required maxlength size rows cols
		label poster controls autoplay loop muted preload async defer
		integrity crossorigin loading decoding
		viewbox preserveaspectratio version x y x1 y1 x2 y2 cx cy r rx ry d
		fill stroke stroke-width transform points offset display mathvariant
		encoding
	`) {
		m[name] = name
	}
	return m
}()

// startDecls collects the namespace declarations of a start tag in
// p.tagDecls, before its name and attributes are checked. Declarations which
// are invalid in XML are dropped by writeStart, and leave the prefix unbound
// in the element (unlike in the input, where it can be bound by an outer
// declaration), so names using it are dropped. The xlink prefix is declared
// on svg elements, and on other elements with xlink attributes where it
// isn't bound.
func (p *polyglot) startDecls(tok *htmlstream.Token, name, ns string) {
	p.tagDecls = p.tagDecls[:0]
	p.tagIndexed = tok.NumAttrs() > internal.IndexMin
	if p.tagIndexed {
		p.tagIndex.Reset(tok.NumAttrs())
	}
	xlink := false
	for a := range tok.Attrs() {
		if bytes.IndexByte(a.Name(), ':') < 0 {
			continue
		}
		an := attrName(a, ns)
		prefix, local, ok := strings.Cut(an, ":")
		switch {
		case !ok || !htmlstream.ValidXMLName(an):
		case prefix == "xlink":
			xlink = true
		case prefix == "xmlns":
			if _, _, found := p.tagDecl(local); found {
				break // the first one, like writeStart
			}
			v, _ := htmlstream.AppendXMLChars(nil, a.AppendValue(nil))
			p.addTagDecl(nsDecl{prefix: local, uri: string(v), dropped: invalidDecl(local, string(v)) != ""})
		}
	}
	_, declared, _ := p.tagDecl("xlink")
	if _, bound := p.lookupPrefix("xlink"); name == "svg" && ns == htmlstream.NamespaceSVG && !declared || xlink && !bound {
		p.addTagDecl(xlinkDecls[0])
	}
}

// addTagDecl appends d to p.tagDecls.
func (p *polyglot) addTagDecl(d nsDecl) {
	if p.tagIndexed {
		p.tagIndex.M[d.prefix] = len(p.tagDecls)
	}
	p.tagDecls = append(p.tagDecls, d)
}

// tagDecl is findDecl for p.tagDecls.
func (p *polyglot) tagDecl(prefix string) (uri string, ok, found bool) {
	if !p.tagIndexed {
		return findDecl(p.tagDecls, prefix)
	}
	i, found := p.tagIndex.M[prefix]
	if !found {
		return "", false, false
	}
	d := &p.tagDecls[i]
	return d.uri, !d.dropped, true
}

// invalidDecl returns why a declaration of prefix as uri is invalid in XML
// (by the namespace constraints of Namespaces in XML 1.0), or an empty
// string.
func invalidDecl(prefix, uri string) string {
	switch {
	case prefix == "xmlns":
		return "the xmlns prefix can't be declared"
	case prefix == "xml":
		if uri != htmlstream.NamespaceXML {
			return "the xml prefix can't be bound to another namespace"
		}
	case uri == "":
		return "a prefix can't be undeclared with an empty namespace name"
	case uri == htmlstream.NamespaceXML:
		return "only the xml prefix can be bound to the XML namespace"
	case uri == htmlstream.NamespaceXMLNS:
		return "no prefix can be bound to the xmlns namespace"
	}
	return ""
}

// outDecls returns a copy of the namespace declarations of the current start
// tag, kept with the element in the output.
func (p *polyglot) outDecls() []nsDecl {
	switch {
	case len(p.tagDecls) == 0:
		return nil
	case len(p.tagDecls) == 1 && p.tagDecls[0] == xlinkDecls[0]:
		return xlinkDecls // the declaration added to svg elements
	}
	return slices.Clone(p.tagDecls)
}

// xlinkDecls has the declaration of the xlink prefix added by startDecls.
// The declarations of the output elements aren't modified, so they can share
// it.
var xlinkDecls = []nsDecl{{prefix: "xlink", uri: htmlstream.NamespaceXLink, added: true}}

// lookupPrefix returns the namespace an XML parser binds a prefix to at the
// current start tag in the output, using the declarations written (see
// startDecls). The Reader's declarations (LookupPrefix) also include those
// which aren't written.
func (p *polyglot) lookupPrefix(prefix string) (string, bool) {
	switch prefix {
	case "xml":
		return htmlstream.NamespaceXML, true
	case "xmlns":
		return htmlstream.NamespaceXMLNS, true
	}
	if uri, ok, found := p.tagDecl(prefix); found {
		return uri, ok
	}
	if p.outIndex != nil {
		i, ok := p.outIndex[prefix]
		if !ok {
			return "", false
		}
		r := p.outRefs[i]
		d := &p.out[r.elem].decls[r.decl]
		return d.uri, !d.dropped
	}
	for i := len(p.outRefs) - 1; i >= 0; i-- {
		r := p.outRefs[i]
		if d := &p.out[r.elem].decls[r.decl]; d.prefix == prefix {
			return d.uri, !d.dropped
		}
	}
	return "", false
}

// findDecl looks up a prefix in the declarations of a start tag (the last one
// found, as an added xlink declaration replaces a dropped one). If found,
// ok reports whether the prefix is bound.
func findDecl(decls []nsDecl, prefix string) (uri string, ok, found bool) {
	for i := len(decls) - 1; i >= 0; i-- {
		if d := &decls[i]; d.prefix == prefix {
			return d.uri, !d.dropped, true
		}
	}
	return "", false, false
}

// prefixBound reports whether the prefix of name, if any, is bound for an
// XML parser.
func (p *polyglot) prefixBound(name string) bool {
	prefix, _, ok := strings.Cut(name, ":")
	if !ok {
		return true
	}
	_, ok = p.lookupPrefix(prefix)
	return ok
}

// foreignAttrNS returns the namespace HTML parsers put an attribute named
// name (lowercased, not a namespace declaration) in, on an element in
// namespace ns. Only some xlink and xml attributes of SVG and MathML
// elements are in a namespace ("adjust foreign attributes" in the HTML tree
// construction rules).
func foreignAttrNS(name, ns string) string {
	if ns != htmlstream.NamespaceSVG && ns != htmlstream.NamespaceMathML {
		return ""
	}
	switch name {
	case "xlink:actuate", "xlink:arcrole", "xlink:href", "xlink:role", "xlink:show", "xlink:title", "xlink:type":
		return htmlstream.NamespaceXLink
	case "xml:lang", "xml:space":
		return htmlstream.NamespaceXML
	}
	return ""
}

// warnNS warns that XML and HTML parsers put a prefixed element or attribute
// name in different namespaces, once for each kind, prefix and namespace
// (e.g., once for all epub:type attributes).
func (p *polyglot) warnNS(pos htmlstream.Pos, kind, name, prefix, xmlNS, htmlNS string) {
	if p.firstWarning(onceKey{kind, prefix, xmlNS}) {
		p.warn(pos, "%s %q is in %s for XML parsers and in %s for HTML parsers (will not warn again for this prefix and namespace)", kind, name, nsDesc(xmlNS), nsDesc(htmlNS))
	}
}

func nsDesc(ns string) string {
	if ns == "" {
		return "no namespace"
	}
	return fmt.Sprintf("namespace %q", ns)
}
