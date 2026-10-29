// Package htmlstream reads and transforms HTML/XHTML as a context-aware stream
// of tokens.
//
// By default, it is byte-exact unless modified, even for malformed input. It
// is safe to use with untrusted content without using unbounded memory or
// crashing. It supports multiple modes, including a universal mode
// ([Universal]) which is able to parse HTML while tolerating XMLisms (or
// parsing it as XML if it has an explicit <?xml declaration and xmlns) (it is
// not intended for arbitrary XML, there's a specific mode for that, [XML]).
// It will automatically balance unclosed or misnested HTML5 tags almost
// exactly the same as browsers (some cases are slightly different since it
// works in a single pass without building the full tree). There are some
// helpers for matching elements.
//
// It is also capable of transforming XML, but provides limited XML-specific
// functionality (e.g., no doctype processing, very limited namespace
// awareness, no checks for well-formedness, no check for XML name validity).
// Notably, it does not currently support custom entities declared in the
// doctype. This is intended to be used for doing minimally intrusive
// modifications to XML documents, or efficiently extracting content from them.
//
// Unlike golang.org/x/net/html, it does not build a tree, and unlike
// encoding/xml, it accepts malformed markup and preserves the exact input
// bytes: every byte of the input belongs to exactly one token, so a
// transformation which makes no changes outputs the input unchanged.
//
// # Reading
//
// A [Reader] (see [NewReader]) reads the input one [Token] at a time:
// [Reader.Next] advances to the next token and [Reader.Token] returns it, or
// [Reader.All] iterates over the tokens. Afterwards, [Reader.Err] returns the
// error which stopped it, if any. A token has a [Kind], its bytes in the input
// ([Token.Raw]), and depending on its kind, a name and attributes (see
// [Token.Name] and [Token.Attr]) or text, decoded on request (see
// [Token.AppendText] and [Attr.AppendValue]). It is valid until the next
// token is read (see [Token.Clone] and [TokenBuffer] for copies).
//
// [Reader.Find] advances to the next start tag matching a [Matcher] (e.g.,
// [Tag], [AttrEquals] and [HasClass]). At a start tag, [Reader.Skip] advances
// to the element's end tag, and [Reader.Children] iterates over the tokens
// inside the element.
//
// [github.com/pgaskin/htmlstream/sax] delivers balanced element events (and
// encoding/xml tokens) instead of tokens,
// [github.com/pgaskin/htmlstream/htmlutil] reads the text of elements and
// classifies them, and [github.com/pgaskin/htmlstream/polyglot] converts HTML
// to polyglot markup. For a tree, use golang.org/x/net/html.
//
// # Transforming
//
// A [Transformer] (see [NewTransformer]) reads tokens like a Reader and
// writes each one to an [io.Writer] when it advances past it, with the edits
// made while it was the current token (e.g., [Transformer.SetAttr],
// [Transformer.Remove], [Transformer.Replace] and [Transformer.Wrap]).
// Tokens without edits are written unchanged. Output can be held back until
// the tokens which follow are known (see [Transformer.Hold]).
// [Transformer.Close] writes the rest of the input and flushes the output.
// An edit which doesn't apply to the current token returns an error wrapping
// [ErrInvalidOp], and the Transformer continues.
//
// The Append functions write escaped text and attribute values, tags and
// comments which HTML and XML parsers read the same way, and CDATA sections
// and processing instructions for XML, for markup inserted by a Transformer.
// Text and values passed on their own are a string or a []byte, and tag names
// and attributes (name and value pairs) are strings. [ValidXMLName] and
// [ValidXMLChars] check names and text for XML, and [AppendXMLChars] replaces
// the characters XML doesn't allow. [AdjustSVGTagName], [AdjustSVGAttrName]
// and [AdjustMathMLAttrName] give SVG and MathML names the case HTML parsers
// give them.
//
// # Modes
//
// A Reader reads HTML by default ([HTML] mode). [XML] mode reads XML,
// including XHTML, and [Universal] mode reads HTML or XHTML of unknown type,
// choosing the rules of each document (see [Reader.SetMode]).
//
// Browsers choose the parser by the media type: HTML for text/html, whatever
// the markup looks like, and XML for application/xhtml+xml and other XML
// types. Choose the mode the same way:
//   - HTML mode for documents which browsers parse as HTML: web pages and
//     anything else served as text/html, including XHTML-like markup served
//     that way.
//   - HTML mode with XML syntax ([Reader.SetXMLSyntax]) for markup written
//     as XHTML (an XML declaration or the XHTML namespace declaration, and
//     self-closing tags meant as empty elements) which may be malformed or
//     ends up rendered by an HTML engine (e.g., EPUB content documents
//     written by hand or converted from HTML), and for polyglot conversion
//     ([github.com/pgaskin/htmlstream/polyglot] only accepts HTML mode). It
//     reads the content like browsers read text/html, but closes
//     self-closed elements (e.g., <script src="a.js"/>, which otherwise
//     contains the rest of the document) and void elements at their end tag
//     (<br></br> is one br element), and reads processing instructions, and
//     CDATA sections outside raw text and RCDATA elements.
//   - XML mode for documents which XML parsers read: XML formats (e.g., RSS
//     and Atom feeds, EPUB package documents, SVG files), and XHTML known to
//     be well-formed (e.g., served as application/xhtml+xml), where the
//     result should be what an XML reading system sees (namespaces
//     resolved, XML constructs passed through, a p start tag doesn't close
//     an open p, and script contents are markup). XML mode doesn't recover
//     malformed documents like HTML mode does, and XML parsers reject them.
//   - Universal mode for HTML or XHTML of unknown type, e.g., a mix of HTML
//     pages and XHTML files. It reads a document starting with an XML
//     declaration whose first start tag declares the XHTML namespace (e.g.,
//     <html xmlns="http://www.w3.org/1999/xhtml">, as in most EPUB content
//     documents) like XML mode, and other documents like HTML mode with XML
//     syntax (see [Universal]). It is only for HTML: never use it for other
//     XML, which it reads as HTML (e.g., the link element of RSS is void,
//     and its URL is text after it).
//
// Without XML syntax, HTML mode reads HTML as browsers read text/html:
// self-closing tags on non-void elements are start tags (e.g., everything
// after <div class="clear"/> is in the div), </br> after <br> is another br
// element, CDATA sections are bogus comments whose contents are hidden,
// processing instructions are comments, and a doctype ends at the first ">",
// also inside an internal subset.
//
// In XML mode, elements named like the HTML elements with special parsing
// rules (e.g., script, title, br and p) are ordinary elements, and names are
// case-sensitive. A Transformer in XML mode transforms XML documents,
// including malformed ones, without changing what it doesn't edit: the XML
// declaration and other processing instructions, doctypes with an internal
// subset, CDATA sections, comments, character and entity references, namespace
// prefixes, and the case, order, quoting and whitespace of names and
// attributes are written as they are in the input. Edits to elements (e.g.,
// [Transformer.Remove] on a start tag, [Transformer.Unwrap],
// [Transformer.Wrap] and [Transformer.Empty]) and attributes keep a
// well-formed document well-formed, as long as the names and markup passed to
// them are (see the Append functions), except that removing or unwrapping the
// root element can leave a document without exactly one. Removing or replacing
// an end tag on its own, or a part of a comment or CDATA section split into
// multiple tokens (see [Token.Partial]), doesn't. Edits which join text ending
// with "]]" and text starting with ">" (e.g., removing an element between
// them, or replacing a part of a text split into multiple tokens) form "]]>",
// which XML doesn't allow in text. Elements closed without an end tag in
// malformed documents are closed by an implied end tag, which writes nothing
// (see Element model).
//
// The XML support doesn't process doctypes: entities declared in them aren't
// expanded (only numeric character references and the named character
// references of HTML are decoded, see [XML]), and default attributes aren't
// added. Markup isn't checked for well-formedness or namespace
// well-formedness: malformed markup is read like other markup, without an
// error (e.g., an end tag which doesn't match an open element is stray), and
// edits don't add or check namespace declarations (see [Transformer.Rename]).
// [github.com/pgaskin/htmlstream/xmlutil.CheckWellFormed] checks a document
// separately.
// XML documents in UTF-16 or other encodings in which ASCII characters
// don't have their ASCII bytes must be decoded first (see Input).
//
// The Reader follows [XML 1.0], except that in documents declared as XML 1.1
// (by an XML declaration with the version 1.1 at the start of the input,
// after an optional UTF-8 byte order mark), decoded text follows the
// [end-of-line handling] of XML 1.1: NEL (U+0085), U+2028, and CR followed by
// NEL are line ends too, which [Token.AppendText] decodes as a newline, like
// CR and CR LF, and [Attr.AppendValue] as a space. This applies in XML mode,
// and in [Universal] mode to documents read like in XML mode (and to the
// processing instructions before the choice, see Universal). NEL and U+2028
// are only recognized in UTF-8, and only as line ends in decoded text: in
// markup (e.g., between attributes, or between the target and the data of a
// processing instruction), they aren't whitespace. Character references to
// them (e.g., &#x85;) are decoded to the character, as in XML 1.1. Otherwise,
// XML 1.1 documents are read like XML 1.0 ones (the Reader doesn't check the
// characters and names which XML 1.1 allows differently).
//
// The functions which write and check XML ([AppendEscapeText],
// [AppendEscapeAttr], [AppendXMLChars], [ValidXMLChars], [ValidXMLName] and
// [github.com/pgaskin/htmlstream/xmlutil.AppendEscape]) follow XML 1.0, and
// [github.com/pgaskin/htmlstream/polyglot] writes XML 1.0 documents. In an
// XML 1.1 document, NEL and U+2028 written by them are read as line ends,
// and the control characters U+007F to U+009F other than NEL, which XML 1.1
// only allows as character references, aren't escaped. The attribute values
// set by a Transformer are escaped for XML 1.1 in documents declared as XML
// 1.1 (see [Transformer]).
//
// # Input
//
// A Reader reads bytes without decoding them: markup is recognized by the
// bytes of its ASCII characters, and other bytes are kept as they are. It
// reads UTF-8, and other encodings in which ASCII characters have their ASCII
// bytes (e.g., windows-1252 and EUC-JP), without converting them. In
// Shift_JIS, Big5, GBK and gb18030, the second byte of some characters is
// that of "[" or "]", which can start or end the internal subset of a doctype
// or end a CDATA section early. In those and EUC-KR, it can be an ASCII
// letter, which is compared ASCII case-insensitively and lowercased in names
// under HTML rules (see [Token.HTMLRules]).
// Long text is split into tokens between UTF-8 sequences (see
// [Token.Partial]), which can split characters of other multibyte encodings.
// The encoding declared by the document (by a meta element or the XML
// declaration) isn't used.
//
// Decoding text (see [Token.AppendText] and [Attr.AppendValue]) gives UTF-8
// for character references and for the U+FFFD replacing NUL, so with input in
// another encoding, decoded text mixes encodings. Input in another encoding
// can be decoded to UTF-8 before reading it, e.g., with
// golang.org/x/text/encoding/htmlindex and golang.org/x/text/transform.
// [github.com/pgaskin/htmlstream/htmlutil.PrescanCharset] finds the encoding
// an HTML document declares at its start, and
// [github.com/pgaskin/htmlstream/htmlutil.MetaCharset] the one declared by a
// meta element. [github.com/pgaskin/htmlstream/xmlutil.DetectEncoding]
// detects the encoding of an XML document from its byte order mark, its
// first bytes and its XML declaration, and
// [github.com/pgaskin/htmlstream/xmlutil.Encoding.ASCIICompatible] reports
// whether a Reader can read it without decoding it.
// [github.com/pgaskin/htmlstream/polyglot]
// requires UTF-8 input, since its output declares UTF-8.
//
// A Reader returns [ErrUnsupportedEncoding] for input starting with a UTF-16
// or UTF-32 byte order mark, or with "<" followed by another ASCII character
// in UTF-16. A UTF-8 byte order mark is kept, as the start of the first
// token (a Text token).
//
// # Limits and strict mode
//
// Text, comments and CDATA sections are not limited in length. Long ones are
// split into multiple tokens at about the chunk size (see
// [Reader.SetChunkSize]) and marked by [Token.Partial] and
// [Token.Continued]. Splits never fall inside a UTF-8 sequence, or between
// CR and LF or NEL (a line end in XML 1.1), or inside a character reference
// of up to the maximum token size (see [Reader.SetMaxTokenSize]), so the
// tokens can be decoded separately. A numeric character reference, which
// can have any number of leading zeros, is kept whole in one text token up to
// the maximum token size, whatever the chunk size. A longer one is split
// there, and the parts are decoded separately: the first as a reference
// ending at the split, and the rest as text.
// [Reader.ReadText] reads the decoded text of all of them, up to a limit.
//
// Tags, doctypes and processing instructions are limited in size (see
// [Reader.SetMaxTokenSize]): a larger one is an error ([ErrTokenTooLarge]),
// except for a processing instruction read with XML syntax in HTML mode (see
// [Reader.SetXMLSyntax]) whose first ">" is past the maximum token size: it
// is read as a bogus comment, as HTML parsers read it. In [Universal] mode,
// such an XML declaration isn't recognized, so the document is read like in
// HTML mode. The open elements kept are limited in number and size (see
// [Reader.SetMaxDepth] and [Reader.SetMaxStackBytes], and [Token.Untracked]
// for the elements past the limits). The output held back by a Transformer
// is limited too (see [Transformer.SetMaxHold]).
//
// The memory used by a Reader is proportional to its limits. Its buffer
// holds the largest token read, and the attributes of a tag use memory for
// each attribute in addition to their bytes, so a tag of many short
// attributes uses many times its size. The default maximum token size allows
// large tags, as legitimate tags with data: URLs in attribute values can be
// several megabytes long. For untrusted input, set a lower maximum token
// size where tags this large aren't expected.
//
// In [HTML] mode, and in documents read like in HTML mode in [Universal]
// mode, end tags and some start tags (e.g., li, dd, dt, a, nobr, the ruby
// elements, and start tags closing a p) check the open elements, which can
// take time proportional to the number of open elements. For untrusted
// input, set a lower maximum depth (see [Reader.SetMaxDepth]).
//
// [Reader.ReadText], [github.com/pgaskin/htmlstream/htmlutil.InnerText],
// [github.com/pgaskin/htmlstream/htmlutil.TextContent] and
// [github.com/pgaskin/htmlstream/sax.Reader.ReadText] collect the text of
// several tokens, and limit it only when given a positive limit: pass one
// for untrusted input.
//
// In strict mode (see [Reader.SetStrict]), the operations which depend on
// the structure of the document (e.g., [Reader.Skip]) return a [SyntaxError]
// instead of guessing where the markup is structurally unsound (see
// [Token.Unsound]), e.g., where an element whose end tag may not be omitted
// is closed without one.
//
// Strict mode doesn't check that the input is structurally sound according to
// the HTML or XML specifications, only that the operations used on it are
// unambiguous.
//
// # Element model
//
// A Reader keeps a stack of the open elements (see [Reader.Stack] and
// [Reader.Depth]) with their names, namespaces and namespace declarations,
// and with [Reader.SetKeepAttrs], their start tags. Start and end tags are
// balanced: every element opened by a StartTag token (see [Token.Opens]) is
// closed by exactly one EndTag token. Elements closed without an end tag
// (optional end tags, misnesting, the end of the input) are closed by an
// implied EndTag with an empty Raw (see [Token.Implied]). Void elements and
// self-closed elements are not opened and have no EndTag, except for void
// elements in HTML mode with XML syntax (see [Reader.SetXMLSyntax]), and in
// the XHTML namespace in documents read like in XML mode in [Universal] mode
// (e.g., <br> in <html xmlns="http://www.w3.org/1999/xhtml">): they are
// opened, and closed by their end tag if it is the next token or follows
// whitespace (e.g., <br> </br>, where the whitespace is in the element), and
// otherwise by an implied end tag before the next token, which isn't unsound
// (see [Token.Unsound]). Other content before the end tag (e.g., a comment or
// a character reference) closes them first, so the end tag is stray. End tags
// which don't close an element are stray (see [Token.Stray]).
//
// Elements nested past the stack limits (see [Reader.SetMaxDepth] and
// [Reader.SetMaxStackBytes]) are not tracked (see [Token.Untracked]): they
// are counted by Depth, but their names aren't kept, and when one is the
// innermost open element, the next end tag closes it, whatever its name (a
// tracked element can be open inside an element untracked past the stack
// size limit), except that a void element under XML rules or with XML syntax
// is closed like a tracked one (by its end tag only if it is the next token
// or follows whitespace). Their end tags are still balanced, but have a
// StartPos of -1, and the implied ones an empty Name. Their contents are read
// as if they were in the innermost tracked element, with its namespace and
// rules (e.g., the elements in an untracked svg element aren't SVG elements),
// and the namespace declarations of untracked elements don't apply to their
// contents, except that the contents of untracked raw text and RCDATA
// elements (e.g., script) are still raw text.
//
// In [XML] mode, an end tag closes the nearest open element with its name. In
// [HTML] mode, and in documents read like in HTML mode in [Universal] mode
// (see [Reader.EffectiveMode]), elements are closed like the [HTML Standard]
// tree construction stage closes them: end tags are implied where HTML
// parsers imply them (e.g., a div start tag closes an open p, and a tr start
// tag an open td), an end tag closes the nearest open element with its name
// unless an element which limits its scope (e.g., table or td) is open inside
// it, and SVG and MathML elements are opened and closed like foreign content.
// The Reader doesn't build a tree, and differs from tree construction mostly
// in the steps which add elements, move content or ignore tags:
//
//   - html, head and body elements are only opened by their start tags.
//     Their start tags inside other elements (except head in html, and body
//     in html or head), which HTML parsers ignore, don't open an element, and
//     their attributes aren't merged into the open html or body element. A
//     body start tag after content which HTML parsers put in an implied body
//     (e.g., the text in <html><head></head>x<body>) opens an element too,
//     while HTML parsers ignore it. A head opened by a start tag is closed by
//     the start tags and text which HTML parsers put after it.
//   - The end tags of body and html close them, while HTML parsers keep them
//     open and put the content after them in the body.
//   - There are no implied tbody, tr or colgroup elements, and no foster
//     parenting: content in a table outside a cell stays in the table.
//   - There is no list of active formatting elements: formatting elements
//     closed by a misnested end tag are not reopened, there is no adoption
//     agency algorithm, and an a or nobr start tag closes an open a or nobr
//     only if no special element (e.g., div) is open inside it.
//   - An end tag closes the elements open inside its element even where
//     HTML parsers ignore it (e.g., </span> with a div open in the span).
//     </br> (unless it closes a br opened with XML syntax), and </p>
//     without an open p in scope, are stray, while HTML parsers handle them
//     as <br> and <p></p>.
//   - Start tags which HTML parsers ignore or handle as an end tag, such as
//     nested form and select elements and table parts outside a table, open
//     an element.
//   - Template contents are read like other content, while HTML parsers use
//     the template insertion modes (e.g., parsing them like table rows).
//   - Quirks mode isn't detected: a table start tag closes an open p in
//     quirks mode documents too.
//   - With scripting disabled (the default), noscript in a head without a
//     start tag is read like in the body (see [Reader.SetScripting]).
//
// [github.com/pgaskin/htmlstream/htmlutil.Document] tracks the html, head
// and body elements, including the ones HTML parsers imply, and quirks mode.
// [github.com/pgaskin/htmlstream/polyglot] writes the implied elements,
// reopens misnested formatting elements with a simplified adoption agency
// algorithm, and warns about the other differences which change its output.
//
// [HTML Standard]: https://html.spec.whatwg.org/multipage/parsing.html#tree-construction
// [XML 1.0]: https://www.w3.org/TR/xml/
// [end-of-line handling]: https://www.w3.org/TR/xml11/#sec-line-ends
package htmlstream

import (
	"bytes"
	"errors"
	"fmt"
	"hash/maphash"
	"io"
	"iter"
	"slices"
	"strconv"
	"unicode/utf8"

	"github.com/pgaskin/htmlstream/ascii"
	"github.com/pgaskin/htmlstream/internal"
)

// Mode selects the markup syntax accepted by a Reader.
type Mode uint8

const (
	// HTML tokenizes like the [HTML Standard] tokenizer does for text/html,
	// which browsers use for all HTML documents, including HTML 4 ones. Use
	// it for documents which browsers parse as HTML: anything served as
	// text/html, whatever the markup looks like.
	//
	// Raw text elements (script, style, etc.) and RCDATA elements (title,
	// textarea) are recognized, self-closing syntax only closes elements
	// inside svg and math (elsewhere the slash is ignored: void elements have
	// no contents anyway, and other elements are opened), and CDATA sections
	// are only recognized inside svg and math.
	//
	// [HTML Standard]: https://html.spec.whatwg.org/multipage/parsing.html#tokenization
	HTML Mode = iota

	// XML tokenizes XML syntax, including XHTML. Use it for documents which
	// XML parsers read: XML formats, and XHTML served as an XML media type
	// (e.g., application/xhtml+xml).
	//
	// There are no raw text elements, CDATA sections and processing
	// instructions are recognized everywhere, self-closing syntax is always
	// honored, names are case-sensitive, and doctypes may contain an internal
	// subset, which isn't processed. Comments end at the first "-->" (HTML
	// parsers also end them at "--!>"), and the internal subset ends at the
	// first "]" outside its markup declarations, comments, processing
	// instructions and conditional sections.
	//
	// Character references (see [Token.AppendText] and [Attr.AppendValue])
	// are decoded if they end with a semicolon: numeric ones, and all the
	// named character references of HTML (e.g., &nbsp;), not only those of
	// the entities XML predefines (e.g., &amp;), which are the only ones XML
	// parsers accept without a declaration in the doctype (most other XML
	// parsers throw an error in this case). Numeric references to U+0080 to
	// U+009F aren't replaced with windows-1252 characters like in HTML. Other
	// references (e.g., to entities declared in the doctype) are left as they
	// are.
	//
	// A Reader in XML mode follows [XML 1.0], with the [end-of-line handling]
	// of XML 1.1 in documents declared as XML 1.1 (see Modes in the package
	// documentation).
	//
	// [XML 1.0]: https://www.w3.org/TR/xml/
	// [end-of-line handling]: https://www.w3.org/TR/xml11/#sec-line-ends
	XML

	// Universal reads HTML or XHTML of unknown type, choosing the rules of
	// each document at its start (see [Reader.EffectiveMode]): a document
	// starting with an XML declaration (after an optional UTF-8 byte order
	// mark) whose first start tag declares the XHTML namespace for its own
	// name on that tag (e.g., <html xmlns="http://www.w3.org/1999/xhtml">, or
	// <h:html xmlns:h="http://www.w3.org/1999/xhtml">) is read like in [XML]
	// mode, and every other document like in [HTML] mode with
	// [Reader.SetXMLSyntax]. XHTML files usually start with an XML
	// declaration, and XHTML 1.x pages served as text/html usually don't. The
	// XML declaration is recognized as a processing instruction starting with
	// <?xml followed by XML whitespace or ?>, without validating it (e.g.,
	// <?xml?> and the text declaration <?xml encoding="utf-8"?> are recognized
	// too).
	//
	// It is a fallback for HTML or XHTML of unknown type: if the parser which
	// reads the document is known (HTML for text/html, XML for XML media
	// types), use [HTML] or [XML] mode (see Modes in the package
	// documentation). Never use it for documents which aren't HTML: XML
	// formats (e.g., feeds, EPUB package documents and SVG files) go to XML
	// mode, as Universal mode reads them as HTML (e.g., the link element of
	// RSS is void).
	//
	// In documents read like in XML mode, the HTML void elements in the XHTML
	// namespace (with their names in lowercase, e.g., br and meta, but not
	// image, which only HTML parsers turn into img) are closed like with XML
	// syntax in HTML mode: one without self-closing syntax is closed by its
	// end tag if it is the next token or follows whitespace, and otherwise by
	// an implied end tag before the next token (see Element model in the
	// package documentation), so <br>, <br></br>, <br> </br> and <br/> are all
	// a br element. XML parsers read <br> as the start of an element
	// containing the rest of its parent.
	//
	// The rules are chosen at the first token, or with an XML declaration, at
	// the first token after it other than whitespace, a comment, a processing
	// instruction or a doctype. The tokens before that are read like in HTML
	// mode with XML syntax, except that processing instructions and doctypes
	// after the XML declaration are read like in XML mode whatever the rules
	// chosen (e.g., <?pi a>b?> is one processing instruction), with the line
	// ends of XML 1.1 in documents declared as XML 1.1. One of them which
	// reaches the end of the input that way ends at the first ">" in it after
	// all, as in HTML mode with XML syntax (a processing instruction is then a
	// bogus comment, and an incomplete one without a ">"), and the rules are
	// chosen after it as usual. Before the rules are chosen, and in documents
	// read like in HTML mode, only the first doctype of the input is read with
	// an internal subset or quoted literals, as with XML syntax; in documents
	// read like in XML mode, the doctypes after the first start tag are read
	// like in XML mode. As in HTML mode, comments also end at "--!>", and a
	// start tag whose name doesn't start with an ASCII letter (e.g., <_x>) is
	// text.
	Universal
)

// String returns the name of the constant for m, e.g., "HTML".
func (m Mode) String() string {
	switch m {
	case HTML:
		return "HTML"
	case XML:
		return "XML"
	case Universal:
		return "Universal"
	}
	return "Mode(" + strconv.Itoa(int(m)) + ")"
}

// Kind is the kind of a Token.
type Kind uint8

const (
	// Text is character data, including the contents of raw text and RCDATA
	// elements.
	Text Kind = iota + 1

	// StartTag is a start tag, including self-closing tags.
	StartTag

	// EndTag is an end tag. A Reader also emits an implied EndTag with an
	// empty Raw for every element which is closed without an end tag.
	EndTag

	// Comment is a comment, including bogus comments like <!foo>, </ foo>,
	// and <?foo> in HTML (see [Token.Bogus]).
	Comment

	// CDATA is a CDATA section.
	CDATA

	// Doctype is a document type declaration.
	Doctype

	// ProcInst is a processing instruction, including the XML declaration.
	ProcInst
)

// String returns the name of the constant for k, e.g., "StartTag".
func (k Kind) String() string {
	switch k {
	case Text:
		return "Text"
	case StartTag:
		return "StartTag"
	case EndTag:
		return "EndTag"
	case Comment:
		return "Comment"
	case CDATA:
		return "CDATA"
	case Doctype:
		return "Doctype"
	case ProcInst:
		return "ProcInst"
	}
	return "Kind(" + strconv.Itoa(int(k)) + ")"
}

// Pos is the byte offset of a token in the input. It identifies a token
// across multiple passes over the same input. Implied end tags have the
// position of the token which caused them, or the input length at EOF. A
// line and column, if needed, are counted from the input by the caller.
type Pos int64

// Errors returned by a Reader, and by a Transformer reading with it.
var (
	// ErrTokenTooLarge is returned for a tag, doctype or processing
	// instruction larger than the maximum token size (see
	// [Reader.SetMaxTokenSize]). The returned error wraps it with the maximum
	// size.
	ErrTokenTooLarge = errors.New("htmlstream: token too large")

	// ErrUnsupportedEncoding is returned for input starting with a UTF-16 or
	// UTF-32 byte order mark, or with "<" followed by another ASCII character
	// in UTF-16.
	ErrUnsupportedEncoding = errors.New("htmlstream: unsupported encoding (utf-16 or utf-32)")

	// ErrUntrackedLimit is returned when an operation depends on the end of
	// an untracked element (see [Token.Untracked]) while as many untracked
	// elements as the maximum depth (see [Reader.SetMaxDepth]) already have
	// operations pending on their ends. These operations are the
	// Transformer's element operations, and in strict mode, Skip and
	// Children. It is sticky. The returned error wraps it with the maximum
	// depth.
	ErrUntrackedLimit = errors.New("htmlstream: too many untracked elements (MaxDepth) with pending operations")
)

// limitError is an error for a limit, with the limit's value in its message,
// wrapping the sentinel error err.
type limitError struct {
	err error
	msg string
}

func (e *limitError) Error() string { return e.msg }
func (e *limitError) Unwrap() error { return e.err }

// tokenTooLarge returns an error wrapping ErrTokenTooLarge.
func (r *Reader) tokenTooLarge() error {
	return &limitError{ErrTokenTooLarge, "htmlstream: token too large (MaxTokenSize is " + strconv.Itoa(r.maxToken) + " bytes)"}
}

// untrackedLimit returns an error wrapping ErrUntrackedLimit.
func (r *Reader) untrackedLimit() error {
	return &limitError{ErrUntrackedLimit, "htmlstream: too many untracked elements (MaxDepth is " + strconv.Itoa(r.maxDepth) + ") with pending operations"}
}

// SyntaxError is a syntax error at a position in the input. The Reader
// returns one in strict mode when an operation depends on structurally
// unsound markup (see [Reader.SetStrict]); other packages also use it in
// their strict modes, e.g., for input they would drop (see
// [github.com/pgaskin/htmlstream/sax.Reader.SetStrict]), and for XML which
// isn't well-formed (see
// [github.com/pgaskin/htmlstream/xmlutil.CheckWellFormed]).
type SyntaxError struct {
	Pos Pos    // the position of the token
	Msg string // the problem, e.g., why the token is unsound
}

// Error returns the message with the position, as "htmlstream: syntax error
// at offset Pos: Msg".
func (e *SyntaxError) Error() string {
	return fmt.Sprintf("htmlstream: syntax error at offset %d: %s", e.Pos, e.Msg)
}

// The defaults of the Reader's limits and chunk size (see
// [Reader.SetMaxTokenSize], [Reader.SetMaxDepth], [Reader.SetMaxStackBytes]
// and [Reader.SetChunkSize]).
const (
	// DefaultMaxTokenSize is the default maximum size of a tag, doctype or
	// processing instruction, 32 MiB.
	DefaultMaxTokenSize = 32 << 20

	// DefaultMaxDepth is the default maximum number of open elements
	// tracked.
	DefaultMaxDepth = 8192

	// DefaultMaxStackBytes is the default maximum number of bytes kept for
	// the open elements, 8 MiB.
	DefaultMaxStackBytes = 8 << 20

	// DefaultChunkSize is the default size at which text, comments and
	// CDATA sections are split into multiple tokens, 64 KiB.
	DefaultChunkSize = 64 << 10
)

const defaultBufSize = 4096

// Reader reads tokens from HTML, XHTML or XML markup. It isn't safe for
// concurrent use, and reads a single input: it can't be reset to read
// another one.
type Reader struct {
	src    io.Reader
	srcErr error // error from src, returned once buffered input is exhausted
	err    error // sticky error

	tokSource       // buf, start and scratch
	off       int64 // input offset of buf[0]
	p         int   // scan position in buf
	end       int   // end of buffered input in buf

	mode      Mode // the rules followed: in Universal mode, Universal until decided (see Reader.decide)
	universal bool // the mode set is Universal
	xmlDecl   bool // Universal mode: the input starts with an XML declaration
	xhtmlVoid bool // HTML void elements in the XHTML namespace open an element under XML rules (see Reader.decide)
	xmlSyntax bool
	scripting bool
	strict    bool
	keepAttrs bool
	chunk     int
	maxToken  int
	maxDepth  int
	maxStack  int
	started   bool
	bom       bool  // the input starts with a UTF-8 byte order mark
	xml11     bool  // the XML declaration declares version 1.1
	doctype   bool  // a doctype was lexed (see Reader.lexDoctype)
	piNoEnd   int64 // the input offset from which the input has no "?>", or 0 if unknown (see Reader.procInstBogus)

	// lexer state
	raw     rawKind     // raw text state for the next Text token
	rawName string      // lowercase name of the raw text element
	script  scriptState // script data escape state
	cont    contKind    // character data construct continued from the previous token

	// tokens
	tok     Token // lexed token
	itok    Token // implied end tag
	cur     *Token
	staged  bool // tok is lexed but not yet delivered
	pops    int  // implied end tags to deliver before tok
	replan  bool // tok is to be planned when delivered (see Reader.next)
	tokPops bool // tok is an end tag matching the frame below the implied pops
	popNext bool // pop the top element at the next call to Next
	push    bool // tok is a start tag to push when delivered
	eof     bool

	// classification of tok if push
	pushNS    nsKind
	pushFlags frameFlags
	pushInfo  internal.Elem

	// element stack
	stack     []frame
	names     []byte // arena for frame names, kept start tags and namespace declarations
	decls     []nsDecl
	uriBytes  int // the length of the URIs in decls, counted against maxStack again for nsDecl.str
	keptAttrs []attrSpan
	untracked int              // open elements past the depth or memory limit (see frame.untracked)
	voidName  []byte           // the name of the innermost untracked element, if it is a void element under XML rules (see Reader.next)
	voidOpen  bool             // voidName is set
	watched   []int            // depths of the untracked elements an operation depends on, in strict mode
	open      map[uint64]int32 // the index in stack of the innermost element with each openHash of its name (see frame.same)
	seed      maphash.Seed     // for openHash

	// the index in decls of the innermost declaration of each prefix (see
	// nsKey), once there are more than internal.IndexMin declarations
	nsIndex  map[string]int32
	nsKeyBuf []byte

	// the URIs of the namespace declarations on the current start tag (if it
	// doesn't open an element), by attribute index, made by lookupNSString;
	// with more than internal.IndexMin attributes, selfIndex maps their
	// prefixes (see nsKey) to their index in selfNS once selfIndexed
	selfNS      []selfNS
	selfIndex   internal.Map[string, int32]
	selfIndexed bool

	sink func(*Token) // receives each token before advancing (Transformer)
}

type selfNS struct {
	attr int
	uri  string
	made bool // uri was made
}

// tokSource holds the bytes of a token: the Reader's buffer for the tokens
// it delivers (embedded in Reader, so the lexer uses buf and start directly),
// or a copy's own bytes (see [Token.Clone] and [TokenBuffer]).
type tokSource struct {
	buf     []byte
	start   int     // offset of the current token in buf
	scratch []byte  // decoded attribute values for AttrFunc
	rd      *Reader // the Reader, or nil for a copy

	// copies only: the namespace of the token (modes other than HTML), and
	// those of the prefixes of its attributes, resolved when the copy was
	// made
	nsURI    string
	prefixNS []prefixNS
}

// prefixNS is the namespace a prefix was bound to when a token was copied
// (empty if unbound). The prefix is in the token's Raw.
type prefixNS struct {
	prefix span
	uri    string
}

type rawKind uint8

const (
	rawNone rawKind = iota
	rawRCDATA
	rawText
	rawScript
	rawPlaintext
)

type scriptState struct {
	escaped, double bool
	dashes          uint8
}

type contKind uint8

const (
	contNone contKind = iota
	contText
	contSpace // leading whitespace in the head or a colgroup (see lexText)
	contComment
	contBogus
	contCDATA
)

// NewReader returns a Reader reading from r in [HTML] mode. It reads bytes
// without decoding them, which works for UTF-8 and other encodings in which
// ASCII characters have their ASCII bytes, and returns
// [ErrUnsupportedEncoding] for UTF-16 and UTF-32 input (see Input in the
// package documentation).
func NewReader(r io.Reader) *Reader {
	rd := &Reader{
		src:      r,
		chunk:    DefaultChunkSize,
		maxToken: DefaultMaxTokenSize,
		maxDepth: DefaultMaxDepth,
		maxStack: DefaultMaxStackBytes,
		seed:     maphash.MakeSeed(),
	}
	rd.rd = rd
	return rd
}

// SetMode sets the markup syntax. It must be called before the first call
// to Next, and panics otherwise.
func (r *Reader) SetMode(m Mode) {
	r.mustNotStart("SetMode")
	r.mode, r.universal = m, m == Universal
}

// Mode returns the markup syntax (see [Reader.SetMode]).
func (r *Reader) Mode() Mode {
	if r.universal {
		return Universal
	}
	return r.mode
}

// EffectiveMode returns the mode whose rules the Reader follows. In [HTML]
// and [XML] modes, it is the mode. In [Universal] mode, it is Universal
// until the Reader chooses the rules of the document, and then HTML (read
// like in HTML mode with [Reader.SetXMLSyntax]) or XML. The choice is made
// at a token before or at the first start or end tag (see Universal), and
// applies to that token.
func (r *Reader) EffectiveMode() Mode { return r.mode }

// SetXMLSyntax makes [HTML] mode accept XML syntax: self-closing tags on
// non-void elements, CDATA sections (outside raw text and RCDATA elements,
// e.g., script and title, whose contents are text up to their end tag, as
// in HTML), processing instructions, and doctypes with an internal subset
// (which isn't processed, see [XML]). A processing instruction ends at the
// first ">", where HTML parsers end the bogus comment they read it as, and
// is one only if that ">" follows "?": <?php $a->b ?> and Word's
// <?xml:namespace ... /> are bogus comments ending at that ">". The ">" is
// looked for only up to the maximum token size (see
// [Reader.SetMaxTokenSize]), so a processing instruction whose first ">" is
// past it is a bogus comment too, and in [Universal] mode, an XML
// declaration this long isn't recognized, so the document is read like in
// HTML mode. A doctype ends at the first ">" outside its internal subset,
// also in a quoted literal, as in HTML parsers, and one whose internal
// subset reaches the end of the input at the first ">" in it. Only the first
// doctype of the input is read with an internal subset: later ones, which
// HTML parsers ignore, end at the first ">". Void elements without
// self-closing syntax open an element, closed by their end tag if it is the
// next token or follows whitespace, as in XML, and otherwise by an implied
// end tag before the next token (see Element model in the package
// documentation): <br>, <br></br>, <br> </br> and <br/> are all one br
// element, while HTML parsers read </br> as another one. It has no effect in
// [XML] mode, which always accepts XML syntax, and in [Universal] mode, which
// applies it to the documents it reads like in HTML mode. It must be called
// before the first call to Next, and panics otherwise.
//
// Enable it for documents written as XHTML (e.g., with an XML declaration or
// the XHTML namespace declaration, and self-closing tags meant as empty
// elements) or read as XML. Leave it disabled to read HTML as browsers read
// text/html, where <div/> is a start tag, CDATA sections are bogus comments
// and processing instructions are comments (see Modes in the package
// documentation).
func (r *Reader) SetXMLSyntax(v bool) {
	r.mustNotStart("SetXMLSyntax")
	r.xmlSyntax = v
}

// SetScripting sets whether scripting is enabled, like the
// [parser scripting mode] of HTML parsers. The default is false. With
// scripting enabled, noscript elements contain raw text, as in browsers
// running scripts. With scripting disabled, their contents are markup, and a
// noscript element in a head element opened by a start tag is closed by
// content other than link, meta, style, etc., like in HTML parsers; in a head
// without a start tag, which the Reader doesn't track, it is parsed like in
// the body. Under XML rules (see [Token.HTMLRules]), noscript contents are
// markup whatever the setting. It must be called before the first call to
// Next, and panics otherwise.
//
// Scripting is generally better left disabled. With scripting enabled, the
// contents of a noscript element are raw text, so [Reader.Find], the
// [Transformer], [github.com/pgaskin/htmlstream/sax] and
// [github.com/pgaskin/htmlstream/htmlutil] don't see the elements in them
// (e.g., the img fallback of a lazily loaded image), while XML parsers,
// readers which don't run scripts and
// [github.com/pgaskin/htmlstream/polyglot.Convert] read them as markup.
// Enable it to read documents as a browser running scripts does, e.g., for
// [github.com/pgaskin/htmlstream/htmlutil.InnerText] and
// [github.com/pgaskin/htmlstream/htmlutil.IsHidden] to follow what such a
// browser renders.
//
// [parser scripting mode]: https://html.spec.whatwg.org/multipage/parsing.html#parser-scripting-mode
func (r *Reader) SetScripting(v bool) {
	r.mustNotStart("SetScripting")
	r.scripting = v
}

// Scripting reports whether scripting is enabled (see
// [Reader.SetScripting]).
func (r *Reader) Scripting() bool { return r.scripting }

// SetStrict sets whether operations which depend on structurally unsound
// markup (see [Token.Unsound]) return a [SyntaxError] instead of guessing,
// which also stops the Reader. The default is false, and it may be called at
// any time. These operations are:
//
//   - [Reader.Find], at an unsound start tag it matches;
//   - [Reader.Skip] and [Reader.Children], at an unsound start tag which
//     opens an element, and at the unsound implied end tag of the element;
//   - the Transformer's methods which require a start tag (e.g.,
//     [Transformer.SetAttr]), at an unsound start tag;
//   - the Transformer's element operations ([Transformer.Remove],
//     [Transformer.Unwrap], [Transformer.Wrap], [Transformer.Rename] and
//     [Transformer.Empty]), like Skip; for those which don't advance to the
//     end of the element, Next returns the error at its implied end tag.
//
// Otherwise, unsound tokens are read like the others.
//
// Strict mode doesn't check that the input is structurally sound according to
// the HTML or XML specifications, only that the operations used on it are
// unambiguous.
func (r *Reader) SetStrict(v bool) {
	r.strict = v
}

// SetKeepAttrs sets whether the attributes of open elements are kept, so
// they are available from [Element.Attrs]. The start tags count against the
// stack size limit (see [Reader.SetMaxStackBytes]), so with large
// attributes, fewer elements are tracked. It must be called before the
// first call to Next, and panics otherwise.
func (r *Reader) SetKeepAttrs(v bool) {
	r.mustNotStart("SetKeepAttrs")
	r.keepAttrs = v
}

// SetMaxTokenSize sets the maximum size of a tag, doctype or processing
// instruction. Character data (text, comments and CDATA sections) is split
// into multiple tokens instead. The default is [DefaultMaxTokenSize], and
// values below 64 are treated as 64. It must be called before the first call
// to Next, and panics otherwise.
//
// The memory used by the Reader is proportional to it, and a tag of many
// short attributes uses many times its size (see Limits and strict mode
// in the package documentation). For untrusted input, set it as low as the
// largest tags expected allow.
func (r *Reader) SetMaxTokenSize(n int) {
	r.mustNotStart("SetMaxTokenSize")
	r.maxToken = max(n, 64)
	r.chunk = min(r.chunk, r.maxToken)
}

// SetMaxDepth sets the maximum number of open elements tracked. Elements
// nested deeper are not tracked (see [Token.Untracked]): their names and
// attributes aren't kept, but their ends are still known. Elements are also
// untracked past the stack size limit (see [Reader.SetMaxStackBytes]). At
// most that many untracked elements can have operations pending on their
// ends (see [ErrUntrackedLimit]). The default is
// [DefaultMaxDepth], and values below 1 are treated as 1. It must be called
// before the first call to Next, and panics otherwise.
//
// In [HTML] mode, and in documents read like in HTML mode in [Universal]
// mode, end tags and some start tags (e.g., li, dd, dt, a, nobr, the ruby
// elements, and start tags closing a p) check the open elements, which can
// take time proportional to the number of open elements. For untrusted
// input, set it lower.
func (r *Reader) SetMaxDepth(n int) {
	r.mustNotStart("SetMaxDepth")
	r.maxDepth = max(n, 1)
}

// SetMaxStackBytes sets the maximum number of bytes kept for the open
// elements: their names, namespace declarations, and with
// [Reader.SetKeepAttrs], their start tags. Namespace URIs count twice, as
// they are also kept as the strings returned by [Token.NS] and the other
// methods returning namespace URIs. An element which would exceed it
// is not tracked (see [Token.Untracked]), like one nested past the maximum
// depth. The default is [DefaultMaxStackBytes], and negative values are
// treated as 0. It must be called before the first call to Next, and panics
// otherwise.
func (r *Reader) SetMaxStackBytes(n int) {
	r.mustNotStart("SetMaxStackBytes")
	r.maxStack = max(n, 0)
}

// SetChunkSize sets the size at which text, comments and CDATA sections are
// split into multiple tokens (see [Token.Partial]). A text token can be
// longer, up to the maximum token size, to keep a numeric character reference
// whole (see Limits and strict mode in the package documentation). It may be
// called at any time and applies from the next token. The default is
// [DefaultChunkSize]. Values below 1 are treated as 1, and it is limited to
// the maximum token size.
func (r *Reader) SetChunkSize(n int) {
	r.chunk = min(max(n, 1), r.maxToken)
}

func (r *Reader) mustNotStart(fn string) {
	if r.started {
		panic("htmlstream: " + fn + " called after Next")
	}
}

// Err returns the first error encountered, or nil at the end of the input.
// An error of the [io.Reader] is returned as it is, after the tokens for the
// input read before it, and without implied end tags for the open elements.
// Errors are sticky: Next, Find, Skip and the other methods stop at them.
func (r *Reader) Err() error {
	return r.err
}

// Token returns the current token. It is valid until the next call to Next.
// It returns nil before the first call to Next and after Next returns false.
func (r *Reader) Token() *Token {
	return r.cur
}

// All returns an iterator over the remaining tokens. Each token is valid
// until the next iteration. Check Err after iterating.
func (r *Reader) All() iter.Seq[*Token] {
	return func(yield func(*Token) bool) {
		for r.Next() {
			if !yield(r.cur) {
				return
			}
		}
	}
}

// Depth returns the number of open elements, including the current start or
// end tag's element. The element of a start tag is counted whether the tag
// opens it or not (e.g., a void or self-closed element, see [Token.Opens]),
// so sibling elements have the same depth, and a start tag has Depth()-1
// ancestors. A stray end tag (see [Token.Stray]) has the same depth as the
// enclosing element's end tag would; use [Token.StartPos] to identify the
// element an end tag closes.
func (r *Reader) Depth() int {
	d := r.depth()
	if t := r.cur; t != nil && t.kind == StartTag && !t.Opens() {
		d++ // the element of a start tag which doesn't open one
	}
	return d
}

// depth returns the number of open elements, tracked or not.
func (r *Reader) depth() int { return len(r.stack) + r.untracked }

// Next advances to the next token. It returns false at the end of the input
// or on error.
func (r *Reader) Next() bool {
	r.started = true
	if r.sink != nil && r.cur != nil && r.err == nil {
		r.sink(r.cur)
	}
	if len(r.selfNS) != 0 {
		clear(r.selfNS)
		r.selfNS = r.selfNS[:0]
	}
	r.selfIndexed = false
	if !r.next() {
		r.cur = nil
		return false
	}
	if r.err != nil {
		// strict mode error from an implied end tag
		r.cur = nil
		return false
	}
	return true
}

func (r *Reader) next() bool {
	if r.err != nil {
		return false
	}
	if r.popNext {
		r.popNext = false
		r.popTop()
	}
	if r.pops > 0 {
		r.pops--
		r.deliverImplied()
		return true
	}
	if r.staged {
		if r.replan {
			r.replan = false
			if r.planTok() {
				return true
			}
		}
		r.staged = false
		r.deliver()
		return true
	}
	if r.eof {
		return false
	}
	// A void element under XML rules or with XML syntax on top of the stack
	// is closed before the next token unless it is its end tag or whitespace
	// followed by its end tag (see Reader.endTagFollows), so the token is
	// lexed in the context of the parent element (e.g., text with HTML
	// rules), with the void element hidden from the lexer. Tags, including
	// its end tag, are lexed the same way in any context. An untracked void
	// element is closed the same way, by name (see Reader.pushFrame).
	voidTop, voidUntracked := false, r.voidOpen
	switch n := len(r.stack); {
	case voidUntracked:
		voidTop = true
		r.untracked--
	case n != 0 && r.untrackedAbove() == 0 && r.stack[n-1].flags&frameXMLVoid != 0:
		voidTop = true
		r.stack = r.stack[:n-1]
	}
	ok := r.lex()
	switch {
	case voidUntracked:
		r.untracked++
	case voidTop:
		r.stack = r.stack[:len(r.stack)+1]
	}
	if !ok {
		if r.err != nil {
			return false
		}
		if r.srcErr != io.EOF {
			// read error, reported after the input read before it
			r.err = r.srcErr
			return false
		}
		// EOF: close remaining elements.
		r.eof = true
		r.pops = len(r.stack) + r.untracked
		if r.pops == 0 {
			return false
		}
		r.pops--
		r.deliverImplied()
		return true
	}
	if voidTop {
		name, fold := r.voidName, r.mode == HTML
		if !voidUntracked {
			name = r.frameName(&r.stack[len(r.stack)-1])
		}
		switch t := &r.tok; {
		case t.kind == EndTag && t.flags&tokIncomplete == 0 && namesEqual(name, t.Name(), fold):
			// its end tag
		case t.kind == Text && t.flags&tokContinued != 0:
			// whitespace before its end tag, split into several tokens (see
			// below)
		case t.kind == Text && ascii.IsBlank(t.Data()) && r.endTagFollows(name, fold):
			// whitespace before its end tag, in the element
		default:
			// The token is planned once the void element is closed.
			r.staged, r.replan = true, true
			r.deliverImplied()
			return true
		}
	}
	if r.planTok() {
		return true
	}
	r.deliver()
	return true
}

// endTagFollows reports whether the input after the current token starts with
// optional whitespace (the rest of whitespace split into several Text tokens)
// and an end tag named name without attributes (e.g., "</br>" or "</br >"),
// compared like htmlNamesEqual if fold is set. It reads ahead at most up to
// the maximum token size from the start of the current token, so the result
// doesn't depend on the chunk size.
func (r *Reader) endTagFollows(name []byte, fold bool) bool {
	j := 0 // the start of the end tag
	for i := 0; ; i++ {
		if r.p+i >= r.end && (r.p+i-r.start >= r.maxToken || r.avail(i+1) <= i) {
			return false
		}
		c := r.buf[r.p+i]
		switch {
		case i == j && ascii.IsSpace(c):
			j++
		case i == j:
			if c != '<' {
				return false
			}
		case i == j+1:
			if c != '/' {
				return false
			}
		case i < j+2+len(name):
			// void element names are ASCII, without NUL
			if d := name[i-j-2]; c != d && (!fold || ascii.ToLowerByte(c) != ascii.ToLowerByte(d)) {
				return false
			}
		case c == '>':
			return true
		case !ascii.IsSpace(c):
			return false
		}
	}
}

// planTok plans the lexed token. If it closes elements first, it stages it
// and delivers the first implied end tag, and reports true.
func (r *Reader) planTok() bool {
	if r.mode == Universal {
		r.decide()
	}
	if r.xml11 && (r.mode == XML || r.tok.kind == ProcInst) {
		// The line ends of XML 1.1 apply under XML rules, and to the
		// processing instructions after an XML declaration in Universal mode
		// (see Reader.xmlProlog).
		r.tok.flags |= tokXML11
	}
	r.plan()
	if r.pops > 0 {
		r.staged = true
		r.pops--
		r.deliverImplied()
		return true
	}
	return false
}

// decide chooses the rules of a document in Universal mode at the current
// token, unless it can be in the prolog of an XML document: the byte order
// mark before an XML declaration, the XML declaration, and after it,
// whitespace, comments, processing instructions and doctypes. The document
// follows XML rules if it starts with an XML declaration, and its first start
// tag declares the XHTML namespace for its own name; it then also gets the
// void elements of XHTML (see Reader.next). Otherwise, it is read like in
// HTML mode with XML syntax. The tokens before it are lexed like in HTML mode
// with XML syntax, except that after an XML declaration, processing
// instructions and doctypes are lexed like in XML mode (see
// Reader.xmlProlog).
func (r *Reader) decide() {
	t := &r.tok
	switch {
	case t.pos == 0 && r.bom && t.kind == Text && len(t.Raw()) == 3 && r.lookingAt("<?xml"):
		return // the byte order mark
	case !r.xmlDecl:
	case t.kind == Comment || t.kind == ProcInst || t.kind == Doctype:
		return
	case t.kind == Text && r.textIsSpace(t):
		return
	case t.kind == StartTag:
		t.mode = XML // for its attributes
		if uri, ok := r.lookupNS(t.Prefix(), t); ok && string(uri) == NamespaceXHTML {
			r.mode, r.xhtmlVoid = XML, true
			return
		}
	}
	r.mode, r.xmlSyntax, r.xml11 = HTML, true, false
	t.mode = HTML
	t.flags |= tokXMLSyntax
}

// xmlProlog reports whether processing instructions and doctypes are lexed
// like in XML mode before the document's rules are chosen in Universal mode
// (see Reader.decide): after an XML declaration. One reaching the end of the
// input ends at the first '>' in it after all, as in HTML mode with XML
// syntax, and a processing instruction without one is an incomplete bogus
// comment (see Reader.lexDoctype and Reader.procInstBogus).
func (r *Reader) xmlProlog() bool {
	return r.mode == Universal && r.xmlDecl
}

func (r *Reader) deliverImplied() {
	t := &r.itok
	if r.untrackedAbove() > 0 {
		// An untracked element has no name or position.
		*t = Token{
			src:   &r.tokSource,
			kind:  EndTag,
			spos:  -1,
			flags: tokImplied | tokUnsound,
			pos:   r.tok.pos,
			mode:  r.mode,
		}
		if r.eof {
			t.pos = Pos(r.off + int64(r.end))
		}
		if r.voidOpen {
			// a void element under XML rules (see Reader.next)
			t.flags &^= tokUnsound
		} else if n := len(r.watched); r.strict && n != 0 && r.watched[n-1] == r.depth() {
			// Whether its end tag may be omitted isn't known.
			r.err = t.UnsoundError()
		}
		if r.universal {
			t.flags |= tokUniversal
		}
		r.cur = t
		r.popNext = true
		return
	}
	f := &r.stack[len(r.stack)-1]
	*t = Token{
		src:   &r.tokSource,
		kind:  EndTag,
		ns:    f.ns,
		spos:  f.pos,
		flags: tokImplied,
		pos:   r.tok.pos,
		name:  r.names[f.name : f.name+f.nameLen],
		mode:  r.mode,
	}
	if r.eof {
		t.pos = Pos(r.off + int64(r.end))
	}
	if r.universal {
		t.flags |= tokUniversal
	}
	if !f.omitEnd() {
		t.flags |= tokUnsound
		if r.strict && f.flags&frameWatch != 0 {
			r.err = t.UnsoundError()
		}
	}
	r.cur = t
	r.popNext = true
}

func (r *Reader) deliver() {
	t := &r.tok
	r.cur = t
	switch t.kind {
	case StartTag:
		t.ns = r.pushNS
		if r.push {
			switch {
			case r.pushFrame(t):
				t.flags |= tokPushed
			default:
				t.flags |= tokUntracked
			}
		}
	case EndTag:
		t.spos = -1
		if r.tokPops {
			t.ns = r.stack[len(r.stack)-1].ns
			t.spos = r.stack[len(r.stack)-1].pos
			r.popNext = true
		} else if t.flags&tokIncomplete == 0 && r.untrackedAbove() > 0 {
			// closes the innermost untracked element
			r.popNext = true
		} else {
			t.flags |= tokStray
		}
	}
}

// untrackedAbove returns the number of untracked elements above the top
// tracked element.
func (r *Reader) untrackedAbove() int {
	if n := len(r.stack); n != 0 {
		return r.untracked - int(r.stack[n-1].untracked)
	}
	return r.untracked
}

// popTop closes the innermost open element, tracked or not.
func (r *Reader) popTop() {
	if r.untrackedAbove() > 0 {
		if n := len(r.watched); n != 0 && r.watched[n-1] == r.depth() {
			r.watched = r.watched[:n-1]
		}
		r.untracked--
		r.voidOpen = false // the void element is the innermost one
	} else {
		r.popFrame()
	}
}

// totalPops returns the number of elements to pop to close the top pops
// tracked elements, including the untracked elements above them.
func (r *Reader) totalPops(pops int) int {
	if pops == 0 {
		return 0
	}
	return pops + r.untracked - int(r.stack[len(r.stack)-pops].untracked)
}

// Buffer management.

// fill reads more input into the buffer, moving the current token to the
// start of the buffer and growing it if needed. It returns false if no more
// input is available, setting r.err if the token is too large.
func (r *Reader) fill() bool {
	if r.srcErr != nil {
		return false
	}
	if r.start > 0 && r.end == len(r.buf) {
		n := copy(r.buf, r.buf[r.start:r.end])
		r.off += int64(r.start)
		r.p -= r.start
		r.end = n
		r.start = 0
	}
	if r.end == len(r.buf) {
		if len(r.buf) >= r.maxToken+16 {
			r.err = r.tokenTooLarge()
			return false
		}
		nb := make([]byte, min(max(2*len(r.buf), defaultBufSize), r.maxToken+16))
		copy(nb, r.buf[r.start:r.end])
		r.off += int64(r.start)
		r.p -= r.start
		r.end -= r.start
		r.start = 0
		r.buf = nb
	}
	for range 100 {
		n, err := r.src.Read(r.buf[r.end:])
		r.end += n
		if err != nil {
			r.srcErr = err
		}
		if n > 0 {
			return true
		}
		if err != nil {
			return false
		}
	}
	r.srcErr = io.ErrNoProgress
	return false
}

// avail ensures at least n bytes are buffered after r.p if possible, and
// returns the number of bytes available after r.p.
func (r *Reader) avail(n int) int {
	for r.end-r.p < n && r.fill() {
	}
	return r.end - r.p
}

func (r *Reader) readByte() (byte, bool) {
	if r.p >= r.end && !r.fill() {
		return 0, false
	}
	c := r.buf[r.p]
	r.p++
	return c, true
}

// Lexer.

// lex reads the next token into r.tok. It returns false at EOF or on error.
func (r *Reader) lex() bool {
	r.start = r.p
	if r.p == r.end && !r.fill() {
		return false
	}
	if !r.checkEncoding() {
		return false
	}
	t := &r.tok
	t.reset(r)
	t.pos = Pos(r.off + int64(r.start))

	switch r.cont {
	case contText, contSpace:
		space := r.cont == contSpace
		r.cont = contNone
		t.flags |= tokContinued
		r.lexText(space)
		return r.err == nil
	case contComment:
		r.cont = contNone
		t.flags |= tokContinued
		r.lexComment(r.p)
		return r.err == nil
	case contBogus:
		r.cont = contNone
		t.flags |= tokContinued
		r.lexBogus(r.p)
		return r.err == nil
	case contCDATA:
		r.cont = contNone
		t.flags |= tokContinued
		r.lexCDATA(r.p)
		return r.err == nil
	}

	if r.raw != rawNone && r.raw != rawPlaintext && r.buf[r.p] == '<' && !r.script.double && r.atRawEnd() {
		r.raw, r.script = rawNone, scriptState{}
	}
	if r.raw == rawNone && r.buf[r.p] == '<' && r.lexMarkup() {
		return r.err == nil
	}
	// Text which isn't whitespace closes the head or a colgroup, so leading
	// whitespace in them is a token of its own, which ends where other text
	// starts whatever the chunk size. The same applies where HTML parsers
	// imply the head (no open element, or only html), so consumers tracking
	// the implied head can keep the whitespace in it.
	r.lexText(r.raw == rawNone && r.mode != XML && r.untrackedAbove() == 0 && r.splitSpace())
	return r.err == nil
}

// checkEncoding checks the start of the input for unsupported encodings.
func (r *Reader) checkEncoding() bool {
	if r.off != 0 || r.start != 0 {
		return true
	}
	// UTF-16 and UTF-32 BOMs, or '<' followed by another ASCII character as
	// UTF-16.
	b := r.buf[:min(r.avail(4), 4)]
	r.bom = bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF})
	if bytes.HasPrefix(b, []byte{0xFE, 0xFF}) ||
		bytes.HasPrefix(b, []byte{0xFF, 0xFE}) ||
		bytes.HasPrefix(b, []byte{0x00, 0x00, 0xFE, 0xFF}) ||
		len(b) == 4 && b[0] == 0 && b[1] == '<' && b[2] == 0 && b[3] != 0 && b[3] < 0x80 ||
		len(b) == 4 && b[0] == '<' && b[1] == 0 && b[2] != 0 && b[2] < 0x80 && b[3] == 0 {
		r.err = ErrUnsupportedEncoding
		return false
	}
	return true
}

func (r *Reader) cdataAllowed() bool {
	if r.mode != HTML || r.xmlSyntax {
		return true
	}
	if n := len(r.stack); n != 0 {
		return r.stack[n-1].foreign()
	}
	return false
}

func (r *Reader) piAllowed() bool {
	return r.mode != HTML || r.xmlSyntax
}

func (r *Reader) isNameStart(c byte) bool {
	if r.mode != XML {
		return isASCIIAlpha(c)
	}
	return isASCIIAlpha(c) || c == '_' || c == ':' || c >= 0x80
}

// lexMarkup lexes markup starting with '<' at r.p. It returns false if the
// '<' does not start markup, in which case it is text.
func (r *Reader) lexMarkup() bool {
	if r.avail(2) < 2 {
		return false
	}
	switch c := r.buf[r.p+1]; {
	case r.isNameStart(c):
		r.p++
		r.lexTag(StartTag)
		return true
	case c == '/':
		if r.avail(3) < 3 {
			return false // "</" at EOF is text
		}
		switch c := r.buf[r.p+2]; {
		case r.isNameStart(c):
			r.p += 2
			r.lexTag(EndTag)
		case c == '>':
			// </>, which HTML parsers ignore (see Token.Bogus)
			r.p += 3
			r.tok.kind = Comment
			r.tok.flags |= tokBogus
			r.tok.data = span{2, 2}
			r.tok.raw = span{0, 3}
		default:
			r.p += 2
			r.lexBogus(r.p)
		}
		return true
	case c == '!':
		r.p += 2
		n := r.avail(7)
		switch b := r.buf[r.p : r.p+n]; {
		case bytes.HasPrefix(b, []byte("--")):
			r.p += 2
			r.lexComment(r.p)
		case len(b) >= 7 && ascii.EqualFold(b[:7], "doctype"):
			r.p += 7
			r.lexDoctype()
		case len(b) >= 7 && string(b[:7]) == "[CDATA[" && r.cdataAllowed():
			r.p += 7
			r.lexCDATA(r.p)
		default:
			r.lexBogus(r.p)
		}
		return true
	case c == '?':
		r.p += 2
		if r.mode == XML || r.xmlProlog() || r.piAllowed() && r.procInstEnds() {
			r.lexProcInst()
		} else {
			// TODO: maybe preserve proc insts for html now since the spec allows it, but probably behind an option since it'll break many parsers
			r.lexBogus(r.p - 1)
		}
		return true
	}
	return false
}

// procInstEnds reports whether the first '>' after "<?" (r.p is after it)
// follows a '?' other than the one of "<?", so that a processing instruction
// read like HTML parsers read a bogus comment, which ends at the first '>',
// ends at "?>". It reads ahead at most up to the maximum token size from the
// start of the current token, so the result doesn't depend on the chunk
// size.
func (r *Reader) procInstEnds() bool {
	for i := 0; ; {
		if j := bytes.IndexByte(r.buf[r.p+i:r.end], '>'); j >= 0 {
			i += j
			return i > 0 && r.buf[r.p+i-1] == '?'
		}
		i = r.end - r.p
		if r.end-r.start >= r.maxToken || r.avail(i+1) <= i {
			return false
		}
	}
}

func (r *Reader) rel(i int) int32 {
	return int32(i - r.start)
}

// lexTag lexes a start or end tag. r.p is at the first byte of the name.
func (r *Reader) lexTag(kind Kind) {
	t := &r.tok
	t.kind = kind
	// Positions are kept relative to the token start, since fill may move
	// the buffer.
	nameStart := r.rel(r.p)
	for {
		c, ok := r.readByte()
		if !ok {
			t.nspan = span{nameStart, r.rel(r.p)}
			r.incomplete()
			return
		}
		if ascii.IsSpace(c) || c == '/' || c == '>' {
			r.p--
			break
		}
		if c == '<' {
			t.flags |= tokUnsound
		}
	}
	t.nspan = span{nameStart, r.rel(r.p)}

	for {
		// before attribute name
		c, ok := r.readByte()
		if !ok {
			r.incomplete()
			return
		}
		switch {
		case ascii.IsSpace(c):
			continue
		case c == '/':
			c, ok := r.readByte()
			if !ok {
				r.incomplete()
				return
			}
			if c == '>' {
				t.flags |= tokSelfClosing
				r.finishTag()
				return
			}
			r.p--
			continue
		case c == '>':
			r.finishTag()
			return
		}

		// attribute name
		a := attrSpan{name: span{r.rel(r.p - 1), 0}}
		if c == '"' || c == '\'' || c == '<' {
			t.flags |= tokUnsound
		}
		for {
			c, ok = r.readByte()
			if !ok {
				r.incomplete()
				return
			}
			if ascii.IsSpace(c) || c == '/' || c == '>' || c == '=' {
				break
			}
			if c == '"' || c == '\'' || c == '<' {
				t.flags |= tokUnsound
			}
		}
		a.name.end = r.rel(r.p - 1)

		// after attribute name
		for ascii.IsSpace(c) {
			if c, ok = r.readByte(); !ok {
				r.incomplete()
				return
			}
		}
		if c != '=' {
			r.p--
			t.attrs = append(t.attrs, a)
			continue
		}

		// before attribute value
		for {
			if c, ok = r.readByte(); !ok {
				r.incomplete()
				return
			}
			if !ascii.IsSpace(c) {
				break
			}
		}
		a.hasVal = true
		switch c {
		case '"', '\'':
			a.quote = c
			vs := r.rel(r.p)
			for {
				d, ok := r.readByte()
				if !ok {
					r.incomplete()
					return
				}
				if d == c {
					break
				}
			}
			a.val = span{vs, r.rel(r.p - 1)}
			t.attrs = append(t.attrs, a)
			// after quoted value: whitespace is not required before the
			// next attribute
		case '>':
			a.val = span{r.rel(r.p - 1), r.rel(r.p - 1)}
			t.attrs = append(t.attrs, a)
			r.finishTag()
			return
		default:
			vs := r.rel(r.p - 1)
			for {
				if c == '"' || c == '\'' || c == '<' {
					t.flags |= tokUnsound
				}
				if c, ok = r.readByte(); !ok {
					r.incomplete()
					return
				}
				if ascii.IsSpace(c) || c == '>' {
					r.p--
					break
				}
			}
			a.val = span{vs, r.rel(r.p)}
			t.attrs = append(t.attrs, a)
		}
	}
}

func (r *Reader) finishTag() {
	r.tok.raw = span{0, r.rel(r.p)}
}

// incomplete marks the current token as cut off by the end of the input.
func (r *Reader) incomplete() {
	t := &r.tok
	t.flags |= tokIncomplete | tokUnsound
	t.raw = span{0, r.rel(r.p)}
}

// lexComment lexes a comment whose data starts at ds.
func (r *Reader) lexComment(ds int) {
	t := &r.tok
	t.kind = Comment
	dsr := r.rel(ds)
	if t.flags&tokContinued == 0 && r.mode != XML {
		// <!--> and <!---> (in XML, <!-->-->" is a comment containing ">")
		n := r.avail(2)
		if n >= 1 && r.buf[r.p] == '>' {
			r.p++
			t.data = span{dsr, dsr}
			t.raw = span{0, r.rel(r.p)}
			return
		}
		if n >= 2 && r.buf[r.p] == '-' && r.buf[r.p+1] == '>' {
			r.p += 2
			t.data = span{dsr, dsr}
			t.raw = span{0, r.rel(r.p)}
			return
		}
	}
	for {
		// Find "--" followed by any number of '-', then ">" or, except in
		// XML mode, "!>".
		b := r.buf[r.p:r.end]
		for i := 0; ; {
			j := bytes.Index(b[i:], []byte("--"))
			if j < 0 {
				break
			}
			k := i + j + 2
			for k < len(b) && b[k] == '-' {
				k++
			}
			if k < len(b) && b[k] == '>' {
				t.data = span{dsr, r.rel(r.p + k - 2)}
				r.p += k + 1
				t.raw = span{0, r.rel(r.p)}
				return
			}
			if k+1 < len(b) && b[k] == '!' && b[k+1] == '>' && r.mode != XML {
				t.data = span{dsr, r.rel(r.p + k - 2)}
				r.p += k + 2
				t.raw = span{0, r.rel(r.p)}
				return
			}
			i = k
		}
		if split := r.splitPoint(r.end-max(trailingCommentEnd(b), 1), dsr); split > 0 {
			r.p = split
			t.data = span{dsr, r.rel(r.p)}
			t.raw = span{0, r.rel(r.p)}
			t.flags |= tokPartial
			r.cont = contComment
			return
		}
		// Rescan the end of the buffer, which may contain part of the end.
		r.p = max(r.p, r.end-3)
		if !r.fill() {
			if r.err != nil {
				return
			}
			r.p = r.end
			b := r.buf[r.start:r.end]
			t.data = span{dsr, max(dsr, int32(len(b)-trailingCommentEnd(b[dsr:])))}
			t.raw = span{0, r.rel(r.p)}
			t.flags |= tokIncomplete | tokUnsound
			return
		}
	}
}

// trailingCommentEnd returns the length of a possible partial comment end
// ("-", "--", "--!") at the end of b.
func trailingCommentEnd(b []byte) int {
	switch {
	case bytes.HasSuffix(b, []byte("--!")):
		return 3
	case bytes.HasSuffix(b, []byte("--")):
		return 2
	case bytes.HasSuffix(b, []byte("-")):
		return 1
	}
	return 0
}

// splitPoint returns where to split the current comment or CDATA token,
// which is long enough to be split if the chunk size was reached, at or
// before split: not between CR and LF or NEL or inside a UTF-8 sequence, and
// after the start of the data at dsr. It returns 0 if the token should not
// be split yet.
func (r *Reader) splitPoint(split int, dsr int32) int {
	if r.end-r.start < r.chunk {
		return 0
	}
	ds := int(dsr) + r.start
	if split > ds {
		split -= splitBackoff(r.buf[ds:split])
	}
	if split <= ds {
		return 0
	}
	return split
}

// lexBogus lexes a bogus comment whose data starts at ds, ending at the
// first '>'.
func (r *Reader) lexBogus(ds int) {
	t := &r.tok
	t.kind = Comment
	t.flags |= tokBogus
	dsr := r.rel(ds)
	for {
		if i := bytes.IndexByte(r.buf[r.p:r.end], '>'); i >= 0 {
			t.data = span{dsr, r.rel(r.p + i)}
			r.p += i + 1
			t.raw = span{0, r.rel(r.p)}
			return
		}
		if split := r.splitPoint(r.end-1, dsr); split > 0 {
			r.p = split
			t.data = span{dsr, r.rel(r.p)}
			t.raw = span{0, r.rel(r.p)}
			t.flags |= tokPartial
			r.cont = contBogus
			return
		}
		r.p = r.end
		if !r.fill() {
			if r.err != nil {
				return
			}
			t.data = span{dsr, r.rel(r.p)}
			t.raw = span{0, r.rel(r.p)}
			t.flags |= tokIncomplete | tokUnsound
			return
		}
	}
}

// lexCDATA lexes a CDATA section whose data starts at ds.
func (r *Reader) lexCDATA(ds int) {
	t := &r.tok
	t.kind = CDATA
	dsr := r.rel(ds)
	for {
		if i := bytes.Index(r.buf[r.p:r.end], []byte("]]>")); i >= 0 {
			t.data = span{dsr, r.rel(r.p + i)}
			r.p += i + 3
			t.raw = span{0, r.rel(r.p)}
			return
		}
		b := r.buf[r.p:r.end]
		n := 1
		if bytes.HasSuffix(b, []byte("]]")) {
			n = 2
		}
		if split := r.splitPoint(r.end-n, dsr); split > 0 {
			r.p = split
			t.data = span{dsr, r.rel(r.p)}
			t.raw = span{0, r.rel(r.p)}
			t.flags |= tokPartial
			r.cont = contCDATA
			return
		}
		// keep up to 2 bytes for a "]]>" split across reads
		r.p = max(r.p, r.end-2)
		if !r.fill() {
			if r.err != nil {
				return
			}
			r.p = r.end
			t.data = span{dsr, r.rel(r.p)}
			t.raw = span{0, r.rel(r.p)}
			t.flags |= tokIncomplete | tokUnsound
			return
		}
	}
}

// lexDoctype lexes a doctype. r.p is after "<!DOCTYPE".
func (r *Reader) lexDoctype() {
	t := &r.tok
	t.kind = Doctype
	ds := r.rel(r.p)
	xml := r.mode == XML || r.xmlProlog()
	first := !r.doctype
	r.doctype = true
	// Except in XML mode, only the first doctype is read with an internal
	// subset or quoted literals, and later ones, which HTML parsers ignore,
	// end at the first ">": otherwise each of them could be read to the end
	// of the input before ending there (see below).
	if r.mode == HTML && !r.xmlSyntax || r.mode != XML && !first {
		for {
			c, ok := r.readByte()
			if !ok {
				break
			}
			if c == '>' {
				t.data = span{ds, r.rel(r.p - 1)}
				t.raw = span{0, r.rel(r.p)}
				return
			}
		}
	} else {
		// The internal subset ends at a "]" outside the markup declarations,
		// comments, processing instructions and conditional sections in it,
		// and the doctype at the next ">" outside quotes. Except in XML mode
		// and after an XML declaration in Universal mode (see
		// Reader.xmlProlog), a ">" outside the internal subset ends the
		// doctype also in a quoted literal, as in HTML parsers, which end it
		// at the first ">", so a malformed literal doesn't take the rest of
		// the input.
		var q byte // the quote of the literal being read, if any
		subset := false
		decl := false // in a markup declaration in the internal subset
		cond := 0     // conditional sections open in the internal subset
		for {
			c, ok := r.readByte()
			if !ok {
				break
			}
			switch {
			case c == '>' && !subset && !xml:
				t.data = span{ds, r.rel(r.p - 1)}
				t.raw = span{0, r.rel(r.p)}
				return
			case q != 0:
				if c == q {
					q = 0
				}
			case subset && !decl:
				switch {
				case c == ']' && cond == 0:
					subset = false
				case c == ']':
					if r.avail(2) >= 2 && string(r.buf[r.p:r.p+2]) == "]>" {
						r.p += 2
						cond--
					}
				case c != '<':
				case r.lookingAt("!--"):
					r.p += 3
					r.skipPast("-->")
				case r.lookingAt("?"):
					r.p++
					r.skipPast("?>")
				case r.lookingAt("!["):
					r.p += 2
					cond++
				case r.lookingAt("!"):
					r.p++
					decl = true
				}
			case c == '"' || c == '\'':
				q = c
			case decl:
				if c == '>' {
					decl = false
				}
			case c == '[':
				subset = true
			case c == '>':
				t.data = span{ds, r.rel(r.p - 1)}
				t.raw = span{0, r.rel(r.p)}
				return
			}
		}
	}
	if r.err != nil {
		return
	}
	// Except in XML mode, a doctype reaching the end of the input with a
	// ">" in it (in an internal subset, or in a quoted literal after an XML
	// declaration in Universal mode) ends at the first one after all, as in
	// HTML parsers.
	if i := bytes.IndexByte(r.buf[r.start+int(ds):r.p], '>'); i >= 0 && r.mode != XML {
		r.p = r.start + int(ds) + i + 1
		t.data = span{ds, r.rel(r.p - 1)}
		t.raw = span{0, r.rel(r.p)}
		return
	}
	t.data = span{ds, r.rel(r.p)}
	r.incomplete()
}

// lookingAt reports whether the input at r.p starts with s.
func (r *Reader) lookingAt(s string) bool {
	return r.avail(len(s)) >= len(s) && string(r.buf[r.p:r.p+len(s)]) == s
}

// skipPast advances r.p past the next occurrence of delim, or to the end of
// the input.
func (r *Reader) skipPast(delim string) {
	for {
		if i := bytes.Index(r.buf[r.p:r.end], []byte(delim)); i >= 0 {
			r.p += i + len(delim)
			return
		}
		// keep a delimiter split across reads
		r.p = max(r.p, r.end-len(delim)+1)
		if !r.fill() {
			r.p = r.end
			return
		}
	}
}

// lexProcInst lexes a processing instruction. r.p is after "<?".
func (r *Reader) lexProcInst() {
	t := &r.tok
	t.kind = ProcInst
	if r.piNoEnd != 0 && r.off+int64(r.p) >= r.piNoEnd && r.procInstBogus() {
		return
	}
	ns := r.rel(r.p)
	for {
		c, ok := r.readByte()
		if !ok {
			if r.procInstBogus() {
				return
			}
			t.nspan = span{ns, r.rel(r.p)}
			t.data = span{r.rel(r.p), r.rel(r.p)}
			r.incomplete()
			return
		}
		if ascii.IsSpace(c) || c == '?' {
			r.p--
			break
		}
	}
	t.nspan = span{ns, r.rel(r.p)}
	ds := r.rel(r.p)
	for {
		c, ok := r.readByte()
		if !ok {
			if r.procInstBogus() {
				return
			}
			t.data = span{ds, r.rel(r.p)}
			r.incomplete()
			return
		}
		if c == '?' {
			if r.avail(1) >= 1 && r.buf[r.p] == '>' {
				t.data = span{ds, r.rel(r.p - 1)}
				r.p++
				t.raw = span{0, r.rel(r.p)}
				if r.mode != HTML && (t.pos == 0 || t.pos == 3 && r.bom) {
					// the XML declaration, if any: "<?xml" followed by XML
					// whitespace or "?>" (see Universal)
					decl := r.buf[r.start:r.p]
					d, _, _, ok := internal.ParseDecl(decl)
					r.xml11 = ok && d.Version == "1.1"
					r.xmlDecl = ok && r.mode == Universal
				}
				return
			}
		}
	}
}

// procInstBogus handles a processing instruction reaching the end of the
// input. Before the rules are chosen after an XML declaration in Universal
// mode (see Reader.xmlProlog), it is a bogus comment after all, as in HTML
// mode with XML syntax: one with a '>' in it ends at the first one, so that
// it doesn't take the rest of the input, and one without is an incomplete
// bogus comment. It reports whether it lexed the bogus comment.
//
// The input then has no "?>" after the "<?" of the processing instruction,
// which is recorded in r.piNoEnd: a later processing instruction past it is
// a bogus comment without searching the rest of the input again.
func (r *Reader) procInstBogus() bool {
	if r.err != nil || !r.xmlProlog() {
		return false
	}
	r.piNoEnd = r.off + int64(r.start) + 2
	r.tok.nspan = span{}
	r.p = r.start + 2
	r.lexBogus(r.p - 1)
	return true
}

// lexText lexes character data in the current raw text state. If space is
// set, leading whitespace is a token of its own.
func (r *Reader) lexText(space bool) {
	t := &r.tok
	t.kind = Text
	switch r.raw {
	case rawNone:
		t.text = textData
		if r.mode == XML {
			t.text = textXML
		}
	case rawRCDATA:
		t.text = textRCDATA
	default:
		t.text = textRaw
	}
	limit := r.chunk
	for {
		if space {
			done := r.scanSpace(t.text == textXML, limit)
			if r.err != nil {
				return
			}
			if done && r.p == r.start {
				space = false
				continue
			}
			if done {
				break
			}
			// The chunk size was reached and more whitespace follows.
			t.flags |= tokPartial
			r.cont = contSpace
			break
		} else if r.scanText(limit) {
			if r.raw != rawNone {
				r.raw, r.script = rawNone, scriptState{}
			}
			break
		}
		if r.p < r.end {
			// The chunk size was reached and more text follows, so split the
			// token. Only stateless text is rescanned after backing off. If
			// backing off would leave nothing (the chunk size is smaller
			// than a character reference), scan further: once, or while the
			// token is a numeric reference, up to the maximum token size.
			b := r.buf[r.start:r.p]
			if n := textBackoff(b, t.text); r.p-n > r.start {
				r.p -= n
			} else if n > 0 && limit < r.maxToken && (limit == r.chunk || isNumericRefPrefix(b[1:])) {
				limit = min(max(limit+internal.EntityMaxName+2, 2*limit), r.maxToken)
				continue
			}
			t.flags |= tokPartial
			r.cont = contText
			break
		}
		if !r.fill() {
			if r.err != nil {
				return
			}
			if r.raw != rawNone && r.raw != rawPlaintext {
				t.flags |= tokUnsound // unclosed raw text element
			}
			break
		}
	}
	t.data = span{0, r.rel(r.p)}
	t.raw = t.data
}

// textBackoff returns the number of bytes at the end of b which should be
// left for the next chunk so a character reference, a UTF-8 sequence, CR LF
// or CR NEL isn't split. Numeric references are kept whole whatever their
// length (leading zeros are allowed).
func textBackoff(b []byte, mode textMode) int {
	if mode != textRaw {
		if i := bytes.LastIndexByte(b, '&'); i >= 0 && (len(b)-i <= internal.EntityMaxName+2 || isNumericRefPrefix(b[i+1:])) {
			return len(b) - i
		}
	}
	return splitBackoff(b)
}

// splitBackoff returns the number of bytes at the end of b which should be
// left for the next chunk so a UTF-8 sequence isn't split, and neither is CR
// LF or CR NEL (a line end in XML 1.1).
func splitBackoff(b []byte) int {
	n := 0
	for i := len(b) - 1; i >= 0 && i > len(b)-utf8.UTFMax; i-- {
		if utf8.RuneStart(b[i]) {
			if b[i] >= utf8.RuneSelf && !utf8.FullRune(b[i:]) {
				n = len(b) - i
			}
			break
		}
	}
	if n < len(b) && b[len(b)-n-1] == '\r' {
		n++
	}
	return n
}

// isNumericRefPrefix reports whether b, which follows '&', is the start of a
// numeric character reference without its terminator.
func isNumericRefPrefix(b []byte) bool {
	if len(b) == 0 || b[0] != '#' {
		return false
	}
	b = b[1:]
	if len(b) > 0 && (b[0] == 'x' || b[0] == 'X') {
		b = b[1:]
	}
	for _, c := range b {
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F') {
			return false
		}
	}
	return true
}

// scanSpace advances r.p over whitespace, including character references
// which decode to whitespace. It returns true if the whitespace ended at
// other text, markup or the end of the input, or false if the token reached
// limit bytes.
func (r *Reader) scanSpace(xml bool, limit int) bool {
	const maxRef = internal.EntityMaxName + 2
	for {
		if r.p == r.end && !r.fill() {
			return true
		}
		n := 0
		c := r.buf[r.p]
		switch {
		case ascii.IsSpace(c):
			n = 1
		case c == '&':
			// A reference which fills the window may be longer, so it isn't
			// counted as whitespace. A numeric reference can be longer than
			// any named one (leading zeros), so the window grows to its end,
			// up to the maximum token size.
			w := maxRef
			k := min(r.avail(w), w)
			for k == w && w < r.maxToken-(r.p-r.start) && isNumericRefPrefix(r.buf[r.p+1:r.p+k]) {
				w = min(2*w, r.maxToken-(r.p-r.start))
				k = min(r.avail(w), w)
			}
			var b [8]byte
			if d, m := internal.DecodeRef(b[:0], r.buf[r.p:r.p+k], false, xml); m > 0 && m < w && ascii.IsBlank(d) {
				n = m
			}
		}
		if n == 0 {
			return true
		}
		if r.p-r.start >= limit && !(c == '\n' && r.p > r.start && r.buf[r.p-1] == '\r') {
			return false // but don't split CRLF
		}
		r.p += n
	}
}

// scanText advances r.p over text. It returns true if the text ended at
// markup or the raw text element's end tag, or false if all buffered input
// was consumed or the token reached limit bytes.
func (r *Reader) scanText(limit int) bool {
	for {
		// r.start may change when the buffer is filled
		lim := min(r.end, r.start+limit)
		if r.p >= lim {
			// The token is split here unless the text ends here. A
			// continuation token is not checked for markup at its start.
			return r.p < r.end && r.buf[r.p] == '<' && r.endsText()
		}
		var i int
		switch {
		case r.raw == rawPlaintext:
			r.p = lim
			continue
		case r.raw == rawScript && r.script.escaped:
			i = bytes.IndexAny(r.buf[r.p:lim], "-<>")
			if i != 0 {
				r.script.dashes = 0
			}
		default:
			i = bytes.IndexByte(r.buf[r.p:lim], '<')
		}
		if i < 0 {
			r.p = lim
			continue
		}
		r.p += i
		switch r.raw {
		case rawNone:
			if r.p != r.start && r.avail(2) >= 2 && r.startsMarkup() {
				return true
			}
			r.p++ // '<' which doesn't start markup
		case rawRCDATA, rawText:
			if r.atRawEnd() {
				return true
			}
			r.p++
		case rawScript:
			if r.scanScript() {
				return true
			}
		}
	}
}

// endsText reports whether the '<' at r.p ends the current text: it starts
// markup, or the end tag of the raw text element.
func (r *Reader) endsText() bool {
	switch r.raw {
	case rawNone:
		return r.avail(2) >= 2 && r.startsMarkup()
	case rawPlaintext:
		return false
	}
	return !r.script.double && r.atRawEnd()
}

// scanScript handles the '<', '-' or '>' at r.p in script data, advancing r.p
// past it. It returns true if r.p is at the script element's end tag.
func (r *Reader) scanScript() bool {
	s := &r.script
	switch r.buf[r.p] {
	case '-':
		s.dashes = min(s.dashes+1, 2)
		r.p++
		return false
	case '>':
		if s.dashes == 2 {
			*s = scriptState{}
		}
		s.dashes = 0
		r.p++
		return false
	}
	s.dashes = 0
	if !s.double && r.atRawEnd() {
		*s = scriptState{}
		return true
	}
	if !s.escaped {
		if r.avail(4) >= 4 && string(r.buf[r.p:r.p+4]) == "<!--" {
			s.escaped = true
			s.dashes = 2
			r.p += 4
			return false
		}
		r.p++
		return false
	}
	// "<script" or (when double escaped) "</script", followed by a delimiter
	off := 1
	if s.double {
		off = 2
	}
	n := off + len("script") + 1
	if r.avail(n) >= n {
		b := r.buf[r.p : r.p+n]
		if (!s.double || b[1] == '/') && ascii.EqualFold(b[off:n-1], "script") && isRawEndDelim(b[n-1]) {
			s.double = !s.double
			r.p += n - 1
			return false
		}
	}
	r.p++
	return false
}

// startsMarkup reports whether the '<' at r.p starts markup. At least 2 bytes
// must be available.
func (r *Reader) startsMarkup() bool {
	switch c := r.buf[r.p+1]; {
	case r.isNameStart(c), c == '!', c == '?':
		return true
	case c == '/':
		return r.avail(3) >= 3
	}
	return false
}

// atRawEnd reports whether r.p, which must be at '<', is at the end tag of
// the raw text element.
func (r *Reader) atRawEnd() bool {
	n := 2 + len(r.rawName) + 1
	if r.avail(n) < n {
		return false
	}
	b := r.buf[r.p : r.p+n]
	return b[1] == '/' && ascii.EqualFold(b[2:n-1], r.rawName) && isRawEndDelim(b[n-1])
}

func isRawEndDelim(c byte) bool {
	return ascii.IsSpace(c) || c == '/' || c == '>'
}

func isASCIIAlpha(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
}

// Tokens.

type span struct{ start, end int32 }

type attrSpan struct {
	name, val span
	quote     byte
	hasVal    bool
}

type tokFlags uint16

const (
	tokSelfClosing tokFlags = 1 << iota
	tokPartial              // continues in the next token
	tokContinued            // continues the previous token
	tokIncomplete           // cut off by the end of the input
	tokImplied              // implied end tag
	tokStray                // end tag without a matching open element
	tokBogus                // bogus comment
	tokUnsound              // structurally unsound
	tokPushed               // start tag opened an element on the stack
	tokUntracked            // start tag opened an element past the stack limits
	tokXML11                // XML 1.1 line ends (see Reader.planTok)
	tokXMLSyntax            // read with Reader.SetXMLSyntax or as HTML in Universal mode (lexed tokens, not implied end tags)
	tokUniversal            // read in Universal mode
)

type textMode uint8

const (
	textData   textMode = iota // HTML character data
	textRCDATA                 // title, textarea
	textRaw                    // script, style, etc.
	textXML                    // XML character data
)

// Token is a token read by a Reader. It is only valid until the next call to
// Reader.Next, and the byte slices it returns alias the Reader's buffer (see
// [Token.Clone] and [TokenBuffer] for copies which stay valid).
type Token struct {
	src   *tokSource
	kind  Kind
	flags tokFlags
	text  textMode
	mode  Mode
	pos   Pos
	raw   span
	name  []byte // set directly for implied end tags
	nspan span
	data  span
	ns    nsKind
	spos  Pos // start tag of the element closed by an end tag, or -1
	attrs []attrSpan
}

func (t *Token) reset(r *Reader) {
	*t = Token{
		src:   &r.tokSource,
		mode:  r.mode,
		attrs: t.attrs[:0],
	}
	if r.xmlSyntax {
		t.flags = tokXMLSyntax
	}
	if r.universal {
		t.flags |= tokUniversal
	}
}

func (t *Token) xmlRules() bool { return t.mode == XML }

// attrIn returns the attribute s of the start tag t, with tag as its bytes
// (Raw, or a copy with edits).
func (t *Token) attrIn(tag []byte, s attrSpan) Attr {
	xml := t.xmlRules()
	return Attr{tag, s, t.HTMLRules(), xml, xml && t.flags&tokXML11 != 0}
}

// HTMLRules reports whether a start or end tag follows HTML rules: names are
// compared ASCII case-insensitively, and the HTML rules for void, raw text
// and RCDATA elements, optional end tags and character references apply.
// This is the case for the tokens read in [HTML] mode, and in [Universal]
// mode for the tokens of documents read like in HTML mode (see
// [Reader.EffectiveMode]), whatever the kind of token: it reports false for
// the tokens read in Universal mode before the rules are chosen.
func (t *Token) HTMLRules() bool { return t.mode == HTML }

// Kind returns the kind of token.
func (t *Token) Kind() Kind { return t.kind }

// Pos returns the position of the token in the input.
func (t *Token) Pos() Pos { return t.pos }

// Raw returns the exact bytes of the token in the input. It is empty for
// implied end tags.
func (t *Token) Raw() []byte {
	if t.flags&tokImplied != 0 {
		return nil
	}
	return t.sub(t.raw)
}

func (t *Token) sub(s span) []byte {
	return t.src.buf[t.src.start+int(s.start) : t.src.start+int(s.end)]
}

// Name returns the tag name or processing instruction target as written.
// Under HTML rules (see [Token.HTMLRules]), HTML parsers ASCII-lowercase tag
// names and replace NUL with U+FFFD in them: [Token.Is] compares names like
// they do, and [Token.AppendLocalName] returns the name they give the
// element.
func (t *Token) Name() []byte {
	if t.name != nil {
		return t.name
	}
	switch t.kind {
	case StartTag, EndTag, ProcInst:
		return t.sub(t.nspan)
	}
	return nil
}

// Is reports whether the token is a start or end tag named name. Under HTML
// rules (see [Token.HTMLRules]), the tag name is read like the HTML
// tokenizer reads it in the [tag name state], with NUL replaced with U+FFFD,
// and compared ASCII case-insensitively; otherwise, names are compared
// exactly. HTML parsers ASCII-lowercase tag names, so a lowercase name
// matches the name they give the element.
//
// [tag name state]: https://html.spec.whatwg.org/multipage/parsing.html#tag-name-state
func (t *Token) Is(name string) bool {
	if t.kind != StartTag && t.kind != EndTag {
		return false
	}
	if t.HTMLRules() {
		return htmlNameIs(t.Name(), name)
	}
	return string(t.Name()) == name
}

// Data returns the undecoded contents of a Text, Comment, CDATA, Doctype or
// ProcInst token (excluding the delimiters). For a ProcInst token, it is the
// contents after the target (see [Token.Name]), including the whitespace
// separating them, which XML parsers skip (see [Token.AppendText]).
func (t *Token) Data() []byte {
	switch t.kind {
	case Text, Comment, CDATA, Doctype, ProcInst:
		return t.sub(t.data)
	}
	return nil
}

// AppendText appends the decoded contents of a Text, Comment, CDATA or
// ProcInst token to dst, like the HTML tokenizer (or an XML parser) reads
// them: character references are decoded in text (except in raw text elements
// like script; see [XML] for those decoded under XML rules), and newlines are
// normalized: CR and CR LF become LF, and so do NEL, U+2028 and CR NEL under
// XML rules in documents declared as XML 1.1 (see Modes in the package
// documentation). NUL is replaced with U+FFFD in RCDATA, raw text and
// comments (except in [XML] mode, see [Reader.EffectiveMode]), like the
// tokenizer does; in other text and in CDATA sections, it is kept, as the
// tokenizer emits it (tree construction drops it from text in HTML content,
// and replaces it in SVG and MathML content). XML doesn't allow NUL anywhere.
//
// For a ProcInst token, it appends the data like an XML parser reads it
// ([processing instruction]): the contents after the target, without the
// whitespace separating them, with newlines normalized. Character references
// aren't decoded, and NUL is kept.
//
// [processing instruction]: https://www.w3.org/TR/xml/#sec-pi
func (t *Token) AppendText(dst []byte) []byte {
	d := t.Data()
	var nl normFlags
	if t.flags&tokXML11 != 0 {
		nl = normXML11
	}
	switch t.kind {
	case Text:
		switch t.text {
		case textData:
			return appendNormalized(dst, d, refText, nl)
		case textRCDATA:
			return appendNormalized(dst, d, refText, nl|normNUL)
		case textXML:
			return appendNormalized(dst, d, refText, nl|normXMLRefs)
		default:
			return appendNormalized(dst, d, refNone, nl|normNUL)
		}
	case Comment:
		if t.mode != XML {
			nl |= normNUL
		}
		return appendNormalized(dst, d, refNone, nl)
	case CDATA:
		return appendNormalized(dst, d, refNone, nl)
	case ProcInst:
		for len(d) > 0 {
			if d[0] == ' ' || d[0] == '\t' || d[0] == '\n' || d[0] == '\r' {
				d = d[1:]
			} else if n := xml11LineEnd(d); n != 0 && nl != 0 {
				d = d[n:]
			} else {
				break
			}
		}
		return appendNormalized(dst, d, refNone, nl)
	}
	return dst
}

// SelfClosing reports whether a start tag uses self-closing syntax. In
// [HTML] mode, it only closes elements inside svg and math, like in HTML
// parsers, unless [Reader.SetXMLSyntax] is enabled (see [Token.Opens]).
func (t *Token) SelfClosing() bool { return t.flags&tokSelfClosing != 0 }

// Partial reports whether the text, comment or CDATA section continues in
// the next token.
func (t *Token) Partial() bool { return t.flags&tokPartial != 0 }

// Continued reports whether the token continues the text, comment or CDATA
// section of the previous token.
func (t *Token) Continued() bool { return t.flags&tokContinued != 0 }

// Incomplete reports whether the token was cut off by the end of the input.
// An incomplete start tag opens no element, and leaves out the attribute
// being read when the input ended (e.g., <b class=x has no attributes).
func (t *Token) Incomplete() bool { return t.flags&tokIncomplete != 0 }

// StartPos returns the position of the start tag of the element closed by an
// end tag (explicit or implied), or -1 for stray end tags and other tokens.
func (t *Token) StartPos() Pos {
	if t.kind != EndTag {
		return -1
	}
	return t.spos
}

// Implied reports whether the token is an implied end tag, which closes an
// element without an end tag in the input. Its Raw is empty and its Pos is
// the position of the token which caused it, or the end of the input.
func (t *Token) Implied() bool { return t.flags&tokImplied != 0 }

// Stray reports whether the token is an end tag without a matching open
// element.
func (t *Token) Stray() bool { return t.flags&tokStray != 0 }

// Bogus reports whether the token is a bogus comment, like <!foo> or <?foo>
// in HTML, which the HTML tokenizer reads in the [bogus comment state].
//
// The HTML Standard has since added processing instructions to the HTML
// tokenizer: <?target data> with a target other than xml and xml-stylesheet
// is a processing instruction ending at the first ">" (see the
// [processing instruction open state]). In HTML mode, the Reader still reads
// all of them as bogus comments, as the HTML tokenizer did before, except
// that with XML syntax, those whose first ">" follows "?" are processing
// instructions (see [Reader.SetXMLSyntax]).
//
// </> is a bogus comment too, with no data and a Raw of "</>", so a
// [Transformer] writes it unchanged, but HTML parsers read it as nothing:
// the HTML tokenizer ignores it in the [end tag open state], and gives no
// comment. The last chunk of a longer bogus comment (see [Token.Continued])
// can have the same Raw.
//
// [bogus comment state]: https://html.spec.whatwg.org/multipage/parsing.html#bogus-comment-state
// [processing instruction open state]: https://html.spec.whatwg.org/multipage/parsing.html#processing-instruction-open-state
// [end tag open state]: https://html.spec.whatwg.org/multipage/parsing.html#end-tag-open-state
func (t *Token) Bogus() bool { return t.flags&tokBogus != 0 }

// InRawText reports whether a Text token is the contents of a raw text
// element (script, style, xmp, iframe, noembed, noframes, plaintext, and
// noscript if scripting is enabled), where character references aren't
// decoded.
func (t *Token) InRawText() bool { return t.kind == Text && t.text == textRaw }

// Unsound reports whether the token is structurally unsound: an implied end
// tag for an element whose end tag may not be omitted (it was closed by an
// ancestor's end tag, a misnested end tag, or the end of the input), a tag
// which was cut off or contains quotes or '<' in attribute names or unquoted
// values (usually a missing quote), a construct cut off by the end of the
// input, or the text of a raw text element without an end tag.
func (t *Token) Unsound() bool { return t.flags&tokUnsound != 0 }

// UnsoundError returns a [SyntaxError] saying why the token is structurally
// unsound (see [Token.Unsound]), or nil if it isn't. These are the errors
// the Reader and the Transformer return in strict mode.
func (t *Token) UnsoundError() error {
	if !t.Unsound() {
		return nil
	}
	var msg string
	name := strconv.Quote(string(t.Name()))
	switch {
	case t.kind == EndTag && t.Implied() && t.spos < 0:
		msg = "can't tell where an element nested past the stack limits ends"
	case t.kind == EndTag && t.Implied():
		msg = "element " + name + " at offset " + strconv.FormatInt(int64(t.spos), 10) + " closed without its end tag"
	case t.Incomplete():
		var what string
		switch t.kind {
		case StartTag:
			what = "start tag " + name
		case EndTag:
			what = "end tag " + name
		case Comment:
			what = "comment"
		case CDATA:
			what = "CDATA section"
		case Doctype:
			what = "doctype"
		case ProcInst:
			what = "processing instruction"
		default:
			what = "text"
		}
		msg = what + " is cut off by the end of the input"
	case t.kind == StartTag:
		msg = "start tag " + name + " has < in its name, or a quote or < in an attribute name or unquoted attribute value"
	case t.kind == EndTag:
		msg = "end tag " + name + " has < in its name, or a quote or < in an attribute name or unquoted attribute value"
	case t.kind == Text:
		msg = "raw text or RCDATA element cut off by the end of the input"
	default:
		msg = "unsound " + t.kind.String() + " token"
	}
	return &SyntaxError{t.pos, msg}
}

// Attrs returns an iterator over the attributes of a tag in source order,
// including duplicates. Ranging over it only avoids allocating if the
// compiler inlines the iterator, which it doesn't do in large functions;
// [Token.NumAttrs] and [Token.AttrAt] never allocate.
func (t *Token) Attrs() iter.Seq[Attr] {
	return func(yield func(Attr) bool) {
		raw := t.Raw()
		for _, s := range t.attrs {
			if !yield(t.attrIn(raw, s)) {
				return
			}
		}
	}
}

// NumAttrs returns the number of attributes of a tag, including duplicates.
func (t *Token) NumAttrs() int { return len(t.attrs) }

// AttrAt returns the attribute of a tag at index i in source order (see
// [Token.Attrs]). It panics if i is out of the range [0, NumAttrs).
func (t *Token) AttrAt(i int) Attr {
	return t.attrIn(t.Raw(), t.attrs[i])
}

// Attr returns the first attribute named name, compared like [Attr.Is].
// HTML parsers also keep the first of the attributes with the same name and
// drop the others (see the [attribute name state]); in XML, duplicate
// attributes are an error.
//
// [attribute name state]: https://html.spec.whatwg.org/multipage/parsing.html#attribute-name-state
func (t *Token) Attr(name string) (Attr, bool) {
	raw := t.Raw()
	for _, s := range t.attrs {
		a := t.attrIn(raw, s)
		if a.Is(name) {
			return a, true
		}
	}
	return Attr{}, false
}

// LocalIs reports whether the local name of a start or end tag (see
// [Token.Local]) is name, compared like [Token.Is].
func (t *Token) LocalIs(name string) bool {
	if t.kind != StartTag && t.kind != EndTag {
		return false
	}
	if t.HTMLRules() {
		return htmlNameIs(t.Local(), name)
	}
	return string(t.Local()) == name
}

// HasAttr reports whether the tag has an attribute named name (see
// [Attr.Is]).
func (t *Token) HasAttr(name string) bool {
	_, ok := t.Attr(name)
	return ok
}

// AttrNS returns the first attribute with the local name local (compared like
// [Attr.Is]) in the namespace ns (see [Token.AttrNamespace]); ns is empty for
// unprefixed attributes.
func (t *Token) AttrNS(ns, local string) (Attr, bool) {
	for a := range t.Attrs() {
		if a.localIs(local) && t.AttrNamespace(a) == ns {
			return a, true
		}
	}
	return Attr{}, false
}

// localIs reports whether the local name of the attribute is name, compared
// like Is.
func (a Attr) localIs(name string) bool {
	if a.fold {
		return htmlNameIs(a.Local(), name)
	}
	return string(a.Local()) == name
}

// String returns a description of the token for debugging.
func (t *Token) String() string {
	if t == nil {
		return "<nil>"
	}
	var b []byte
	b = append(b, t.kind.String()...)
	if t.kind == StartTag || t.kind == EndTag || t.kind == ProcInst {
		b = append(b, ' ')
		b = append(b, t.Name()...)
	}
	for _, f := range [...]struct {
		f tokFlags
		s string
	}{
		{tokSelfClosing, "self-closing"},
		{tokPartial, "partial"},
		{tokContinued, "continued"},
		{tokIncomplete, "incomplete"},
		{tokImplied, "implied"},
		{tokStray, "stray"},
		{tokBogus, "bogus"},
		{tokUnsound, "unsound"},
	} {
		if t.flags&f.f != 0 {
			b = append(b, " ["+f.s+"]"...)
		}
	}
	b = append(b, ' ')
	b = strconv.AppendQuote(b, string(t.Raw()))
	return string(b)
}

// Copies.

// Clone returns a copy of the token which stays valid after the Reader
// advances: its byte slices don't alias the Reader's buffer, and NS and
// AttrNamespace use the namespace declarations in scope when the copy was
// made. Everything else observable about the copy is the same as for the
// token, including Pos and StartPos, so copies can be given to consumers of
// tokens later (e.g., held back until the tokens after them are known). A
// [TokenBuffer] copies tokens without allocating for each.
func (t *Token) Clone() *Token {
	c := new(tokClone)
	c.tok = *t
	c.tok.src = &c.src
	c.tok.attrs = append([]attrSpan(nil), t.attrs...)
	c.src.buf = append([]byte(nil), t.Raw()...)
	if t.name != nil {
		// an implied end tag
		c.tok.name = append([]byte(nil), t.name...)
	}
	c.src.snapshot(t)
	return &c.tok
}

// tokClone is a copy made by Clone.
type tokClone struct {
	tok Token
	src tokSource
}

// snapshot records the namespaces a copy of t resolves (see tokSource).
func (s *tokSource) snapshot(t *Token) {
	s.rd, s.nsURI = nil, ""
	if t.mode == XML {
		s.nsURI = t.NS()
	}
	s.prefixNS = s.prefixNS[:0]
	raw := t.Raw()
	for _, as := range t.attrs {
		_, prefix := fixedAttrNS(t.attr(as), t.htmlForeign())
		if prefix != nil {
			// the prefix starts the name
			ps := span{as.name.start, as.name.start + int32(len(prefix))}
			s.prefixNS = append(s.prefixNS, prefixNS{prefix: ps})
		}
	}
	// sorted by prefix, without duplicates, for Token.prefixNS
	cmpPrefix := func(a, b prefixNS) int {
		return bytes.Compare(raw[a.prefix.start:a.prefix.end], raw[b.prefix.start:b.prefix.end])
	}
	slices.SortFunc(s.prefixNS, cmpPrefix)
	s.prefixNS = slices.CompactFunc(s.prefixNS, func(a, b prefixNS) bool { return cmpPrefix(a, b) == 0 })
	for i := range s.prefixNS {
		p := &s.prefixNS[i]
		p.uri = t.prefixNS(raw[p.prefix.start:p.prefix.end])
	}
}

// A TokenBuffer holds copies of tokens (see [Token.Clone]), e.g., to decide
// what to do with them after seeing the tokens which follow. The copies share
// the buffer's memory, which is reused after Reset, so appending to a buffer
// allocates only when it grows. The zero value is an empty buffer ready to
// use.
type TokenBuffer struct {
	toks  []bufTok
	raw   []byte     // the raw bytes of the tokens, and the names of implied end tags
	attrs []attrSpan // the attributes of all tokens
}

// bufTok is a token in a TokenBuffer. The slices of tok and src into the
// buffer's memory are set by At, since the memory moves when it grows.
type bufTok struct {
	tok   Token
	src   tokSource
	attrs int  // offset in TokenBuffer.attrs
	named bool // an implied end tag with a name, at name in TokenBuffer.raw
	name  span
}

// Append appends a copy of t.
func (b *TokenBuffer) Append(t *Token) {
	bt := bufTok{tok: *t, attrs: len(b.attrs)}
	bt.src.start = len(b.raw)
	if n := len(b.toks); n < cap(b.toks) {
		// reuse the memory of the token which was in this slot
		old := &b.toks[:n+1][n]
		bt.src.prefixNS, bt.src.scratch = old.src.prefixNS[:0], old.src.scratch[:0]
	}
	b.raw = append(b.raw, t.Raw()...)
	b.attrs = append(b.attrs, t.attrs...)
	if t.name != nil {
		bt.named, bt.name = true, span{int32(len(b.raw)), int32(len(b.raw) + len(t.name))}
		b.raw = append(b.raw, t.name...)
	}
	bt.src.snapshot(t)
	b.toks = append(b.toks, bt)
}

// Len returns the number of tokens in the buffer.
func (b *TokenBuffer) Len() int { return len(b.toks) }

// Size returns the number of bytes of raw markup in the buffer.
func (b *TokenBuffer) Size() int { return len(b.raw) }

// Reset removes all tokens, keeping the memory. The tokens returned before
// are no longer valid.
func (b *TokenBuffer) Reset() {
	b.toks, b.raw, b.attrs = b.toks[:0], b.raw[:0], b.attrs[:0]
}

// At returns the i-th token (0 is the first appended). It is valid until the
// buffer is reset. It panics if i is out of range [0, Len()).
func (b *TokenBuffer) At(i int) *Token {
	bt := &b.toks[i]
	t := &bt.tok
	t.src = &bt.src
	bt.src.buf = b.raw
	end := len(b.attrs)
	if i+1 < len(b.toks) {
		end = b.toks[i+1].attrs
	}
	t.attrs = b.attrs[bt.attrs:end:end]
	if bt.named {
		t.name = b.raw[bt.name.start:bt.name.end:bt.name.end]
	}
	return t
}

// All returns an iterator over the tokens, in order.
func (b *TokenBuffer) All() iter.Seq[*Token] {
	return func(yield func(*Token) bool) {
		for i := range b.toks {
			if !yield(b.At(i)) {
				return
			}
		}
	}
}

// Attr is an attribute of a tag. It is only valid as long as the token it
// came from.
type Attr struct {
	tag   []byte
	s     attrSpan
	fold  bool
	xml   bool
	xml11 bool // XML 1.1 line ends, if xml
}

// Name returns the attribute name as written (see [Attr.AppendName]).
func (a Attr) Name() []byte { return a.tag[a.s.name.start:a.s.name.end] }

// AppendName appends the attribute name to dst. Under HTML rules (see
// [Token.HTMLRules]), it is the name HTML parsers give the attribute before
// adjusting the case of SVG and MathML attribute names (see
// [AdjustSVGAttrName]): ASCII-lowercased, with NUL replaced with U+FFFD, like
// the HTML tokenizer does in the [attribute name state]. Otherwise, it is the
// name as written.
//
// [attribute name state]: https://html.spec.whatwg.org/multipage/parsing.html#attribute-name-state
func (a Attr) AppendName(dst []byte) []byte {
	if a.fold {
		return appendHTMLName(dst, a.Name())
	}
	return append(dst, a.Name()...)
}

// Is reports whether the attribute is named name. Under HTML rules (see
// [Attr.AppendName]), the name is read like the HTML tokenizer reads it in
// the [attribute name state], with NUL replaced with U+FFFD, and compared
// ASCII case-insensitively; otherwise, names are compared exactly.
//
// [attribute name state]: https://html.spec.whatwg.org/multipage/parsing.html#attribute-name-state
func (a Attr) Is(name string) bool {
	if a.fold {
		return htmlNameIs(a.Name(), name)
	}
	return string(a.Name()) == name
}

// HasValue reports whether the attribute has a value (possibly empty).
func (a Attr) HasValue() bool { return a.s.hasVal }

// Quote returns the quote character around the value, or 0 if the value is
// unquoted or missing.
func (a Attr) Quote() byte { return a.s.quote }

// Prefix returns the namespace prefix of the attribute name as written, or
// nil. A name starting with a colon has none, as in XML parsers, and other
// names are split at the first colon (see [Token.Prefix]).
func (a Attr) Prefix() []byte {
	if i := bytes.IndexByte(a.Name(), ':'); i > 0 {
		return a.Name()[:i]
	}
	return nil
}

// Local returns the attribute name as written without the namespace prefix.
func (a Attr) Local() []byte {
	n := a.Name()
	if i := bytes.IndexByte(n, ':'); i > 0 {
		return n[i+1:]
	}
	return n
}

// Raw returns the exact bytes of the attribute, from the start of the name to
// the end of the value (including the closing quote).
func (a Attr) Raw() []byte {
	end := a.s.name.end
	if a.s.hasVal {
		end = a.s.val.end
		if a.s.quote != 0 {
			end++
		}
	}
	return a.tag[a.s.name.start:end]
}

// RawValue returns the undecoded value without quotes.
func (a Attr) RawValue() []byte { return a.tag[a.s.val.start:a.s.val.end] }

// AppendValue appends the decoded value to dst. Character references are
// decoded (under HTML rules, with the rules for attribute values; see [XML]
// for those decoded under XML rules) and newlines are normalized. In HTML,
// NUL is replaced with U+FFFD, like the HTML tokenizer does. In XML,
// whitespace is normalized to spaces like XML parsers do for attributes
// without a declared type ([attribute-value normalization]), and so are
// NEL, U+2028 and CR NEL, which are line ends in documents declared as
// XML 1.1 (see Modes in the package documentation).
//
// [attribute-value normalization]: https://www.w3.org/TR/2006/REC-xml-20060816/#AVNormalize
func (a Attr) AppendValue(dst []byte) []byte {
	v := a.RawValue()
	if !a.xml {
		return appendNormalized(dst, v, refAttr, normNUL)
	}
	chars := "\t\n\r&"
	if a.xml11 {
		chars = "\t\n\r&\u0085\u2028"
	}
	for len(v) > 0 {
		i := bytes.IndexAny(v, chars)
		if i < 0 {
			return append(dst, v...)
		}
		dst = append(dst, v[:i]...)
		v = v[i:]
		switch v[0] {
		case '&':
			var n int
			if dst, n = internal.DecodeRef(dst, v, true, true); n == 0 {
				dst = append(dst, '&')
				n = 1
			}
			v = v[n:]
		case '\r':
			dst = append(dst, ' ')
			v = v[1:]
			if len(v) > 0 && v[0] == '\n' {
				v = v[1:]
			} else if a.xml11 && xml11LineEnd(v) == 2 {
				v = v[2:] // NEL
			}
		case '\t', '\n':
			dst = append(dst, ' ')
			v = v[1:]
		default:
			// NEL or U+2028
			dst = append(dst, ' ')
			v = v[xml11LineEnd(v):]
		}
	}
	return dst
}

// Value returns the decoded value.
func (a Attr) Value() string {
	v := a.RawValue()
	if a.isDecoded(v) {
		return string(v)
	}
	return string(a.AppendValue(nil))
}

// isDecoded reports whether the raw value v of a is the same as the decoded
// value.
func (a Attr) isDecoded(v []byte) bool {
	return !bytes.ContainsAny(v, "&\r\x00\t\n") && !(a.xml11 && bytes.ContainsAny(v, "\u0085\u2028"))
}

type refMode uint8

const (
	refNone refMode = iota
	refText         // decode character references in text
	refAttr         // decode character references in an attribute value
)

type normFlags uint8

const (
	normXMLRefs normFlags = 1 << iota // decode character references under XML rules
	normNUL                           // replace NUL with U+FFFD
	normXML11                         // XML 1.1 line ends
)

// appendNormalized appends s to dst, normalizing CR and CRLF to LF (and with
// normXML11, CR NEL, NEL and U+2028 too), decoding character references
// according to refs, and replacing NUL with U+FFFD with normNUL.
func appendNormalized(dst, s []byte, refs refMode, flags normFlags) []byte {
	chars := "\r&\x00"
	if flags&normXML11 != 0 {
		chars = "\r&\x00\u0085\u2028"
	}
	for len(s) > 0 {
		i := bytes.IndexAny(s, chars)
		if i < 0 {
			return append(dst, s...)
		}
		dst = append(dst, s[:i]...)
		s = s[i:]
		switch s[0] {
		case '\r':
			dst = append(dst, '\n')
			s = s[1:]
			if len(s) > 0 && s[0] == '\n' {
				s = s[1:]
			} else if flags&normXML11 != 0 && xml11LineEnd(s) == 2 {
				s = s[2:] // NEL
			}
		case '&':
			n := 0
			if refs != refNone {
				dst, n = internal.DecodeRef(dst, s, refs == refAttr, flags&normXMLRefs != 0)
			}
			if n == 0 {
				dst = append(dst, '&')
				n = 1
			}
			s = s[n:]
		case 0:
			if flags&normNUL != 0 {
				dst = append(dst, "\uFFFD"...)
			} else {
				dst = append(dst, 0)
			}
			s = s[1:]
		default:
			// NEL or U+2028
			dst = append(dst, '\n')
			s = s[xml11LineEnd(s):]
		}
	}
	return dst
}

// xml11LineEnd returns the length of the NEL or U+2028 at the start of s, or 0
// if there is none. Both are line ends in XML 1.1 ([end-of-line handling]).
//
// [end-of-line handling]: https://www.w3.org/TR/xml11/#sec-line-ends
func xml11LineEnd(s []byte) int {
	switch {
	case len(s) >= 2 && s[0] == 0xC2 && s[1] == 0x85:
		return 2
	case len(s) >= 3 && s[0] == 0xE2 && s[1] == 0x80 && s[2] == 0xA8:
		return 3
	}
	return 0
}

// Element stack.

type nsKind uint8

const (
	nsOther nsKind = iota
	nsHTML
	nsSVG
	nsMathML
)

// uri returns the namespace URI, or an empty string for nsOther.
func (k nsKind) uri() string {
	switch k {
	case nsHTML:
		return NamespaceXHTML
	case nsSVG:
		return NamespaceSVG
	case nsMathML:
		return NamespaceMathML
	}
	return ""
}

// Namespace URIs, as returned by [Token.NS], [Token.AttrNamespace] and
// [Reader.LookupPrefix].
const (
	NamespaceXHTML  = "http://www.w3.org/1999/xhtml"
	NamespaceSVG    = "http://www.w3.org/2000/svg"
	NamespaceMathML = "http://www.w3.org/1998/Math/MathML"
	NamespaceXLink  = "http://www.w3.org/1999/xlink"
	NamespaceXML    = "http://www.w3.org/XML/1998/namespace"
	NamespaceXMLNS  = "http://www.w3.org/2000/xmlns/"
)

type frameFlags uint8

const (
	frameHTML        frameFlags = 1 << iota // HTML element
	frameIntegration                        // HTML or MathML text integration point
	frameWatch                              // an operation depends on the element's end
	frameXMLVoid                            // HTML void element under XML rules or with XML syntax (see Reader.next)
)

type frame struct {
	pos       Pos
	name      int32 // offset of the name in r.names
	nameLen   int32
	tag       int32 // offset of the kept start tag in r.names, or -1
	tagLen    int32
	attrs     int32 // len(r.keptAttrs) before this frame
	names     int32 // len(r.names) before this frame
	decls     int32 // len(r.decls) before this frame
	untracked int32 // r.untracked before this frame: the untracked elements below it
	same      int32 // the index in r.stack of the next open element below with the same openHash, or -1
	ns        nsKind
	info      internal.Elem
	flags     frameFlags
	act       frameAct // Transformer actions at the element's end
	aux       int32    // Transformer data index
}

type nsDecl struct {
	prefix, uri span   // in r.names
	str         string // uri as a string, made by lookupNSString, or empty
	prev        int32  // the previous declaration of the prefix in r.decls, or -1 (with r.nsIndex)
}

func (f *frame) html() bool { return f.flags&frameHTML != 0 }

// omitEnd reports whether the element's end tag may be omitted (plaintext
// can't be closed at all).
func (f *frame) omitEnd() bool {
	return f.html() && f.info&(internal.OmitEnd|internal.Plaintext) != 0 || f.flags&frameXMLVoid != 0
}

// foreign reports whether the element is in the SVG or MathML namespace.
func (f *frame) foreign() bool { return f.ns == nsSVG || f.ns == nsMathML }

func (r *Reader) frameName(f *frame) []byte {
	return r.names[f.name : f.name+f.nameLen]
}

// is reports whether f is an HTML element named lname.
func (r *Reader) is(f *frame, lname string) bool {
	return f != nil && f.html() && ascii.EqualFold(r.frameName(f), lname)
}

type scope uint8

const (
	scopeDefault scope = iota
	scopeButton
	scopeListItem
	scopeTable
	scopeAll // no barriers (template end tags)
)

// barrier reports whether f limits the search for an open element in the
// scope.
func (r *Reader) barrier(f *frame, sc scope) bool {
	if sc == scopeAll {
		return false
	}
	if f.html() {
		switch sc {
		case scopeTable:
			return f.info&internal.TableScope != 0
		case scopeButton:
			if r.is(f, "button") {
				return true
			}
		case scopeListItem:
			if r.is(f, "ol") || r.is(f, "ul") {
				return true
			}
		}
		return f.info&internal.Scope != 0
	}
	return sc != scopeTable && f.flags&frameIntegration != 0
}

// frameAt returns the k-th tracked open element from the top, or nil.
// Untracked elements are skipped: they are closed along with the tracked
// elements below them.
func (r *Reader) frameAt(k int) *frame {
	if i := len(r.stack) - 1 - k; i >= 0 {
		return &r.stack[i]
	}
	return nil
}

// inHead reports whether text is in the head element (or a noscript in it,
// with scripting disabled), or where HTML parsers imply it: with no open
// element, or only html.
func (r *Reader) inHead() bool {
	switch n := len(r.stack); n {
	case 0:
		return true
	case 1:
		return r.is(&r.stack[0], "html") || r.is(&r.stack[0], "head")
	default:
		return r.is(&r.stack[n-1], "head") || r.headNoscript(0)
	}
}

// inColgroup reports whether the current element is a colgroup.
func (r *Reader) inColgroup() bool {
	f := r.frameAt(0)
	return f != nil && int(f.nameLen) == len("colgroup") && r.is(f, "colgroup")
}

// splitSpace reports whether leading whitespace in text is a token of its
// own (see lexText): in the head (see inHead) or a colgroup.
func (r *Reader) splitSpace() bool {
	return r.maybeSplitSpace() && (r.inHead() || r.inColgroup())
}

// maybeSplitSpace is a quick check for splitSpace, and for text closing the
// head or a colgroup: it is false if the current element can't be head,
// noscript or colgroup.
func (r *Reader) maybeSplitSpace() bool {
	n := len(r.stack)
	if n <= 1 {
		return true
	}
	l := int(r.stack[n-1].nameLen)
	return l == len("head") || l == len("noscript") || l == len("colgroup")
}

// headNoscript reports whether the k-th tracked open element from the top is
// a noscript element in the head with scripting disabled, whose contents
// HTML parsers parse with the "in head noscript" rules.
func (r *Reader) headNoscript(k int) bool {
	return !r.scripting && r.is(r.frameAt(k), "noscript") && r.is(r.frameAt(k+1), "head")
}

// openHash returns a hash of name for r.open: of the name with NUL replaced
// with U+FFFD and ASCII-lowercased in HTML mode, so that names equal under
// HTML rules (see htmlNamesEqual) have the same hash, and of the name itself
// otherwise (see Reader.matches).
func (r *Reader) openHash(name []byte) uint64 {
	if r.mode != HTML {
		return maphash.Bytes(r.seed, name)
	}
	var h maphash.Hash
	h.SetSeed(r.seed)
	var buf [64]byte
	b := buf[:0]
	for _, c := range name {
		if len(b) > len(buf)-len("\uFFFD") {
			h.Write(b)
			b = buf[:0]
		}
		if c == 0 {
			b = append(b, "\uFFFD"...)
		} else {
			b = append(b, ascii.ToLowerByte(c))
		}
	}
	h.Write(b)
	return h.Sum64()
}

// isOpen reports whether a tracked element named name (compared like
// Reader.matches) is open.
func (r *Reader) isOpen(name []byte) bool {
	i, ok := r.open[r.openHash(name)]
	for ok && i >= 0 {
		f := &r.stack[i]
		if r.matches(f, name) {
			return true
		}
		i = f.same
	}
	return false
}

// popsThrough returns the number of elements to pop to close the nearest
// open HTML element named lname in the scope, starting k elements from the
// top, or -1 if there is none.
func (r *Reader) popsThrough(k int, lname string, sc scope) int {
	if !r.isOpen([]byte(lname)) {
		return -1
	}
	for ; ; k++ {
		f := r.frameAt(k)
		if f == nil {
			return -1
		}
		if r.is(f, lname) {
			return k + 1
		}
		if r.barrier(f, sc) {
			return -1
		}
	}
}

// popsUntil returns the number of elements to pop, starting k elements from
// the top, until the current element is an HTML element with one of the
// names (which must include table or template), or -1 if there is none.
func (r *Reader) popsUntil(k int, lnames ...string) int {
	if !r.isOpen([]byte("table")) && !r.isOpen([]byte("template")) {
		return -1
	}
	for ; ; k++ {
		f := r.frameAt(k)
		if f == nil {
			return -1
		}
		for _, n := range lnames {
			if r.is(f, n) {
				return k
			}
		}
	}
}

// plan decides how the lexed token affects the element stack.
func (r *Reader) plan() {
	t := &r.tok
	r.pops, r.tokPops, r.push = 0, false, false
	if t.flags&tokIncomplete != 0 {
		if t.kind == StartTag {
			var lb [16]byte
			r.pushNS, r.pushFlags, r.pushInfo = r.classify(t, lowerASCII(lb[:0], t.Name()), r.frameAt(0))
		}
		return
	}
	switch t.kind {
	case StartTag:
		r.planStart(t)
	case EndTag:
		r.planEnd(t)
	case Text:
		// Text which isn't whitespace closes the head or a colgroup. Leading
		// whitespace in them is a separate token (see lexText), and the
		// tokens of split text are never separated.
		if t.flags&tokContinued == 0 && r.mode != XML && r.untrackedAbove() == 0 && r.maybeSplitSpace() {
			switch {
			case r.is(r.frameAt(0), "head"), r.inColgroup():
				if !r.textIsSpace(t) {
					r.pops = 1
				}
			case r.headNoscript(0):
				if !r.textIsSpace(t) {
					r.pops = 2
				}
			}
		}
	}
}

// textIsSpace reports whether the decoded text of t is whitespace.
func (r *Reader) textIsSpace(t *Token) bool {
	d := t.Data()
	if bytes.IndexByte(d, '&') < 0 {
		return ascii.IsBlank(d)
	}
	r.scratch = t.AppendText(r.scratch[:0])
	return ascii.IsBlank(r.scratch)
}

func (r *Reader) planStart(t *Token) {
	var lb [16]byte
	lname := lowerASCII(lb[:0], t.Name())

	pops := 0
	parent := r.frameAt(0)
	if r.mode != XML && parent != nil && parent.foreign() && parent.flags&frameIntegration == 0 && r.breakout(t, lname) {
		for f := r.frameAt(pops); f != nil && f.foreign() && f.flags&frameIntegration == 0; f = r.frameAt(pops) {
			pops++
		}
		parent = r.frameAt(pops)
	}
	ns, flags, info := r.classify(t, lname, parent)
	r.pushNS, r.pushFlags, r.pushInfo = ns, flags, info
	if r.mode != XML {
		// HTML parsers close a head, and a noscript in it, for elements other
		// than head content (e.g., svg).
		hname, hinfo := string(lname), info
		if flags&frameHTML == 0 {
			hname, hinfo = "", 0
		}
		if r.headNoscript(pops) {
			switch hname {
			case "basefont", "bgsound", "link", "meta", "noframes", "style":
			case "head", "noscript", "html":
				// ignored by HTML parsers (html is misplaced)
			default:
				pops++
			}
		}
		if r.is(r.frameAt(pops), "head") && hinfo&internal.Head == 0 && hname != "head" && hname != "html" {
			// html is misplaced and ignored instead
			pops++
		}
	}
	if flags&frameHTML != 0 {
		pops = r.impliedEnds(string(lname), info, pops)
	}
	r.pops = r.totalPops(pops)

	switch {
	case info&internal.Void != 0 && flags&frameHTML != 0 && !r.xmlSyntax: // HTML void element
	case t.flags&tokSelfClosing != 0 && (r.mode != HTML || r.xmlSyntax || ns == nsSVG || ns == nsMathML):
	case flags&frameHTML != 0 && r.misplaced(string(lname), pops):
		// HTML parsers ignore html, head and body start tags after the
		// element started (merging attributes into html and body), so they
		// don't open an element.
	default:
		r.push = true
		if info&internal.Void != 0 {
			// HTML void element under XML rules or with XML syntax (see
			// Reader.next)
			r.pushFlags |= frameXMLVoid
		}
	}
}

// misplaced reports whether an html, head or body start tag is in a place
// where HTML parsers ignore it, with pops tracked elements (and the untracked
// elements above them) popped before it.
func (r *Reader) misplaced(lname string, pops int) bool {
	depth := len(r.stack) - pops
	untracked := r.untracked
	if pops > 0 {
		untracked = int(r.stack[depth].untracked)
	}
	switch lname {
	case "html":
		return depth+untracked > 0
	case "head":
		return depth+untracked > 1 || depth+untracked == 1 && (depth == 0 || !r.is(&r.stack[0], "html"))
	case "body":
		if untracked > 0 {
			return true
		}
		for i := range depth {
			if !r.is(&r.stack[i], "html") && !r.is(&r.stack[i], "head") {
				return true
			}
		}
	}
	return false
}

// impliedEnds returns the number of elements to pop before an HTML start tag
// with the name lname, starting with pops elements already popped (including
// the head, see planStart).
func (r *Reader) impliedEnds(lname string, info internal.Elem, pops int) int {
	cur := func() *frame { return r.frameAt(pops) }
	if r.is(cur(), "colgroup") && lname != "col" && lname != "template" && lname != "html" {
		// HTML parsers keep col and template in a colgroup (html is
		// misplaced and ignored instead)
		pops++
	}
	switch lname {
	case "li", "dd", "dt":
		if lname == "li" && !r.isOpen([]byte("li")) || lname != "li" && !r.isOpen([]byte("dd")) && !r.isOpen([]byte("dt")) {
			break
		}
		for k := pops; ; k++ {
			f := r.frameAt(k)
			if f == nil || !f.html() {
				break
			}
			if lname == "li" && r.is(f, "li") || lname != "li" && (r.is(f, "dd") || r.is(f, "dt")) {
				pops = k + 1
				break
			}
			if f.info&internal.Special != 0 && !r.is(f, "address") && !r.is(f, "div") && !r.is(f, "p") {
				break
			}
		}
	case "option", "optgroup":
		// inside a select, generate implied end tags (except for optgroup
		// for option)
		if r.popsThrough(pops, "select", scopeDefault) < 0 {
			if r.is(cur(), "option") {
				pops++
			}
			break
		}
		for f := cur(); r.impliedEnd(f) && (lname == "optgroup" || !r.is(f, "optgroup")); f = cur() {
			pops++
		}
	case "input":
		if k := r.popsThrough(pops, "select", scopeDefault); k >= 0 {
			pops = k
		}
	case "hr":
		// close a p, then generate implied end tags inside a select
		if r.popsThrough(pops, "select", scopeDefault) >= 0 {
			if k := r.popsThrough(pops, "p", scopeButton); k >= 0 {
				pops = k
			}
			for r.impliedEnd(cur()) {
				pops++
			}
		}
	case "tr":
		if k := r.popsUntil(pops, "tbody", "thead", "tfoot", "table", "template"); k >= 0 {
			pops = k
		}
	case "td", "th":
		if k := r.popsUntil(pops, "tr", "tbody", "thead", "tfoot", "table", "template"); k >= 0 {
			pops = k
		}
	case "thead", "tbody", "tfoot", "caption", "colgroup":
		if k := r.popsUntil(pops, "table", "template"); k >= 0 {
			pops = k
		}
	case "col":
		// in a cell, row or caption, close it like the other table parts
		if k := r.popsUntil(pops, "colgroup", "table", "template"); k >= 0 {
			pops = k
		}
	case "rb", "rtc", "rt", "rp":
		if r.popsThrough(pops, "ruby", scopeDefault) < 0 {
			break // only inside a ruby element
		}
		// generate implied end tags (except for rtc for rt and rp)
		rtc := lname == "rtc" || lname == "rb"
		for f := cur(); r.impliedEnd(f) && (rtc || !r.is(f, "rtc")); f = cur() {
			pops++
		}
	case "button":
		if k := r.popsThrough(pops, lname, scopeDefault); k >= 0 {
			pops = k
		}
	case "a", "nobr":
		// close an open a or nobr, but not through block elements (HTML
		// parsers use the adoption agency algorithm, which keeps them)
		if r.isOpen([]byte(lname)) {
			for k := pops; ; k++ {
				f := r.frameAt(k)
				if f == nil || !f.html() {
					break
				}
				if r.is(f, lname) {
					pops = k + 1
					break
				}
				if f.info&internal.Special != 0 || r.barrier(f, scopeDefault) {
					break
				}
			}
		}
	case "table":
		// a table directly in a table (not in a cell) closes it
		if f := cur(); r.is(f, "table") || r.is(f, "tbody") || r.is(f, "thead") || r.is(f, "tfoot") || r.is(f, "tr") {
			if k := r.popsThrough(pops, "table", scopeTable); k >= 0 {
				pops = k
			}
		}
	}
	if info&internal.ClosesP != 0 {
		if k := r.popsThrough(pops, "p", scopeButton); k >= 0 {
			pops = k
		}
	}
	if info&internal.Heading != 0 {
		if f := cur(); f != nil && f.html() && f.info&internal.Heading != 0 {
			pops++
		}
	}
	return pops
}

// impliedEnd reports whether f is an HTML element closed by the "generate
// implied end tags" steps of HTML parsers (dd, dt, li, optgroup, option, p,
// rb, rp, rt, rtc).
func (r *Reader) impliedEnd(f *frame) bool {
	return f != nil && f.html() && f.info&internal.ImpliedEnd != 0
}

// breakout reports whether a start tag closes foreign content.
func (r *Reader) breakout(t *Token, lname []byte) bool {
	info := internal.HTMLElem(lname)
	if info&internal.Breakout != 0 {
		return true
	}
	if string(lname) == "font" {
		for a := range t.Attrs() {
			if a.Is("color") || a.Is("face") || a.Is("size") {
				return true
			}
		}
	}
	return false
}

// classify determines the namespace of a start tag (in HTML mode), its
// flags and its element info. lname is the ASCII-lowercased name.
func (r *Reader) classify(t *Token, lname []byte, parent *frame) (ns nsKind, flags frameFlags, info internal.Elem) {
	if r.mode == XML {
		// The XHTML void elements of a document in Universal mode (see
		// Reader.decide) are those with the exact name, and image isn't one
		// (HTML parsers turn it into img).
		if l := t.Local(); r.xhtmlVoid && internal.HTMLElem(l)&internal.Void != 0 && string(l) != "image" {
			if uri, ok := r.lookupNS(t.Prefix(), t); ok && string(uri) == NamespaceXHTML {
				info = internal.Void
			}
		}
		return nsOther, 0, info
	}
	ns = r.htmlNS(lname, parent)
	switch ns {
	case nsHTML:
		flags |= frameHTML
		info = internal.HTMLElem(lname)
	case nsSVG:
		switch string(lname) {
		case "foreignobject", "desc", "title":
			flags |= frameIntegration
		}
	case nsMathML:
		switch string(lname) {
		case "mi", "mo", "mn", "ms", "mtext":
			flags |= frameIntegration
		case "annotation-xml":
			if a, ok := t.Attr("encoding"); ok {
				v := a.RawValue()
				if ascii.EqualFold(v, "text/html") || ascii.EqualFold(v, "application/xhtml+xml") {
					flags |= frameIntegration
				}
			}
		}
	}
	return ns, flags, info
}

// htmlNS determines the namespace of an element like an HTML parser. lname
// is the ASCII-lowercased name.
func (r *Reader) htmlNS(lname []byte, parent *frame) nsKind {
	if parent != nil && parent.foreign() {
		annotationXML := parent.ns == nsMathML && ascii.EqualFold(r.frameName(parent), "annotation-xml")
		if annotationXML && string(lname) == "svg" {
			// with or without an encoding making it an integration point
			return nsSVG
		}
		if parent.flags&frameIntegration == 0 {
			return parent.ns
		}
		if parent.ns == nsMathML && !annotationXML {
			// MathML text integration points
			switch string(lname) {
			case "mglyph", "malignmark":
				return nsMathML
			}
		}
	}
	switch string(lname) {
	case "svg":
		return nsSVG
	case "math":
		return nsMathML
	}
	return nsHTML
}

// lookupNS resolves a namespace prefix (nil for the default namespace)
// using the declarations on the start tag t (if not nil) and the open
// elements.
func (r *Reader) lookupNS(prefix []byte, t *Token) ([]byte, bool) {
	return r.lookupNSUpTo(prefix, len(r.decls), t)
}

// lookupNSUpTo is like lookupNS, using only the first n declarations of the
// open elements (those in scope at an element).
func (r *Reader) lookupNSUpTo(prefix []byte, n int, t *Token) ([]byte, bool) {
	// HTML mode compares attribute names, and so declarations and prefixes,
	// like htmlNamesEqual.
	fold := r.mode == HTML
	if prefixIs(prefix, "xml", fold) {
		return []byte(NamespaceXML), true
	}
	if t != nil {
		for a := range t.Attrs() {
			if p, ok := xmlnsPrefix(a.Name(), fold); ok && namesEqual(p, prefix, fold) {
				return declURI(a), true
			}
		}
	}
	if i := r.findDecl(prefix, n); i >= 0 {
		d := &r.decls[i]
		return r.names[d.uri.start:d.uri.end], true
	}
	return nil, false
}

// findDecl returns the index of the innermost of the first n declarations of
// the open elements which declares prefix, or -1.
func (r *Reader) findDecl(prefix []byte, n int) int {
	if r.nsIndex != nil {
		i, ok := r.nsIndex[string(r.nsKey(prefix))]
		if !ok {
			return -1
		}
		// each element declares a prefix at most once
		for int(i) >= n {
			i = r.decls[i].prev
		}
		return int(i)
	}
	fold := r.mode == HTML
	for i := n - 1; i >= 0; i-- {
		d := &r.decls[i]
		if namesEqual(r.names[d.prefix.start:d.prefix.end], prefix, fold) {
			return i
		}
	}
	return -1
}

// nsKey returns the key of a prefix in r.nsIndex and r.selfIndex: in HTML
// mode, where prefixes are compared like htmlNamesEqual, it is lowercased
// with NUL replaced, in r.nsKeyBuf.
func (r *Reader) nsKey(prefix []byte) []byte {
	if r.mode != HTML {
		return prefix
	}
	r.nsKeyBuf = appendHTMLName(r.nsKeyBuf[:0], prefix)
	return r.nsKeyBuf
}

// indexDecl adds the declaration i, the last one in r.decls, to r.nsIndex.
func (r *Reader) indexDecl(i int) {
	d := &r.decls[i]
	key := r.nsKey(r.names[d.prefix.start:d.prefix.end])
	d.prev = -1
	if j, ok := r.nsIndex[string(key)]; ok {
		d.prev = j
	}
	r.nsIndex[string(key)] = int32(i)
}

// lookupNSString is like lookupNSUpTo, returning the URI as a string. The
// strings are made once for each declaration: those on the stack are kept in
// nsDecl.str, and those on the current start tag t, if it didn't open an
// element, in r.selfNS.
func (r *Reader) lookupNSString(prefix []byte, n int, t *Token) (string, bool) {
	fold := r.mode == HTML
	if prefixIs(prefix, "xml", fold) {
		return NamespaceXML, true
	}
	if t != nil && t.flags&tokPushed == 0 {
		// The declarations of a start tag which opened an element are on
		// the stack.
		if len(t.attrs) > internal.IndexMin {
			if uri, ok := r.selfDecl(t, prefix); ok {
				return uri, true
			}
		} else {
			for i, s := range t.attrs {
				a := t.attr(s)
				if p, ok := xmlnsPrefix(a.Name(), fold); ok && namesEqual(p, prefix, fold) {
					uri, made := "", false
					for _, c := range r.selfNS {
						if c.attr == i {
							uri, made = c.uri, true
							break
						}
					}
					if !made {
						uri = nsString(declURI(a))
						r.selfNS = append(r.selfNS, selfNS{i, uri, true})
					}
					return uri, true
				}
			}
		}
	}
	if i := r.findDecl(prefix, n); i >= 0 {
		d := &r.decls[i]
		if d.str == "" {
			d.str = nsString(r.names[d.uri.start:d.uri.end])
		}
		return d.str, true
	}
	return "", false
}

// selfDecl is like the search of lookupNSString in the declarations of a
// start tag t which didn't open an element, using r.selfIndex.
func (r *Reader) selfDecl(t *Token, prefix []byte) (string, bool) {
	fold := r.mode == HTML
	if !r.selfIndexed {
		r.selfIndexed = true
		r.selfIndex.Reset(len(t.attrs))
		for i, s := range t.attrs {
			if p, ok := xmlnsPrefix(t.attr(s).Name(), fold); ok {
				key := r.nsKey(p)
				if _, ok := r.selfIndex.M[string(key)]; !ok {
					// the first declaration of a prefix wins
					r.selfIndex.M[string(key)] = int32(len(r.selfNS))
					r.selfNS = append(r.selfNS, selfNS{attr: i})
				}
			}
		}
	}
	j, ok := r.selfIndex.M[string(r.nsKey(prefix))]
	if !ok {
		return "", false
	}
	c := &r.selfNS[j]
	if !c.made {
		c.uri, c.made = nsString(declURI(t.attr(t.attrs[c.attr]))), true
	}
	return c.uri, true
}

// declURI returns the URI of the namespace declaration a: its value,
// decoded only if needed, as declarations with character references or
// whitespace to normalize are rare.
func declURI(a Attr) []byte {
	v := a.RawValue()
	if !a.isDecoded(v) {
		v = a.AppendValue(nil)
	}
	return v
}

// xmlnsPrefix returns the prefix declared by an attribute named name, which
// is empty for the default namespace. If fold is set, "xmlns" is matched
// ASCII case-insensitively.
func xmlnsPrefix(name []byte, fold bool) ([]byte, bool) {
	if len(name) < len("xmlns") || !prefixIs(name[:len("xmlns")], "xmlns", fold) {
		return nil, false
	}
	switch p := name[len("xmlns"):]; {
	case len(p) == 0:
		return []byte{}, true
	case len(p) > 1 && p[0] == ':':
		return p[1:], true
	}
	return nil, false
}

// namesEqual compares names, like htmlNamesEqual if fold is set.
func namesEqual(a, b []byte, fold bool) bool {
	if fold {
		return htmlNamesEqual(a, b)
	}
	return bytes.Equal(a, b)
}

// htmlNameIs reports whether name is s under HTML rules: compared ASCII
// case-insensitively, with NUL in name replaced with U+FFFD, like the HTML
// tokenizer reads tag and attribute names. A name s with NUL never matches.
func htmlNameIs(name []byte, s string) bool {
	if len(name) == len(s) {
		return ascii.EqualFold(name, s) && bytes.IndexByte(name, 0) < 0
	}
	// each NUL becomes 3 bytes
	if d := len(s) - len(name); d < 0 || d%2 != 0 || bytes.IndexByte(name, 0) < 0 {
		return false
	}
	for _, c := range name {
		if c == 0 {
			if len(s) < len("\uFFFD") || s[:len("\uFFFD")] != "\uFFFD" {
				return false
			}
			s = s[len("\uFFFD"):]
			continue
		}
		if len(s) == 0 || ascii.ToLowerByte(c) != ascii.ToLowerByte(s[0]) {
			return false
		}
		s = s[1:]
	}
	return len(s) == 0
}

// htmlNamesEqual reports whether the names a and b are equal under HTML
// rules (see htmlNameIs).
func htmlNamesEqual(a, b []byte) bool {
	if ascii.EqualFold(a, b) {
		return true
	}
	if bytes.IndexByte(a, 0) < 0 && bytes.IndexByte(b, 0) < 0 {
		return false
	}
	return htmlNameIs(a, string(bytes.ReplaceAll(b, []byte{0}, []byte("\uFFFD"))))
}

// appendHTMLName appends name to dst ASCII-lowercased and with NUL replaced
// with U+FFFD, as the HTML tokenizer reads tag and attribute names.
func appendHTMLName(dst, name []byte) []byte {
	if bytes.IndexByte(name, 0) < 0 {
		return ascii.AppendLower(dst, name)
	}
	for _, c := range name {
		if c == 0 {
			dst = append(dst, "\uFFFD"...)
		} else {
			dst = append(dst, ascii.ToLowerByte(c))
		}
	}
	return dst
}

func (r *Reader) planEnd(t *Token) {
	if r.untrackedAbove() > 0 {
		return // closes the innermost untracked element, whatever its name
	}
	var lb [16]byte
	lname := lowerASCII(lb[:0], t.Name())
	if r.mode != XML && r.headNoscript(0) && string(lname) != "noscript" && string(lname) != "br" {
		return // ignored by HTML parsers
	}
	k := 0
	if r.mode != XML && r.inColgroup() {
		switch string(lname) {
		case "colgroup", "col", "template":
		default:
			// HTML parsers close the colgroup, then handle the end tag in the
			// table
			k = 1
			r.pops = r.totalPops(1)
		}
	}
	if f := r.frameAt(0); r.mode != XML && f != nil && f.foreign() {
		// HTML parsers apply the foreign content rules when the current node
		// is foreign, integration points included.
		switch {
		case string(lname) == "br" || string(lname) == "p":
			// br and p end tags close foreign content, like the start tags
			// which break out of it, and are then handled by the HTML rules.
			for ; f != nil && f.foreign() && f.flags&frameIntegration == 0; f = r.frameAt(k) {
				k++
			}
			r.pops = r.totalPops(k)
		default:
			// Any other end tag closes the nearest foreign element with its
			// name, through integration points. The HTML rules apply from the
			// current node if there is none before the first HTML element.
			for ; f != nil && f.foreign(); f = r.frameAt(k) {
				if r.matches(f, t.Name()) {
					r.pops = k + r.untracked - int(f.untracked)
					r.tokPops = true
					return
				}
				k++
			}
			k = 0
		}
	}
	// A heading end tag closes any open heading, as in HTML parsers.
	heading := r.mode != XML && len(lname) == 2 && lname[0] == 'h' && '1' <= lname[1] && lname[1] <= '6'
	if !r.isOpen(t.Name()) && !(heading && r.headingOpen()) {
		return
	}
	sc := scopeDefault
	switch string(lname) {
	case "p":
		sc = scopeButton
	case "li":
		sc = scopeListItem
	case "td", "th", "tr", "tbody", "thead", "tfoot", "table", "caption", "colgroup", "html":
		sc = scopeTable
	case "template":
		// HTML parsers close the innermost template, whatever is open in it
		sc = scopeAll
	}
	for ; ; k++ {
		f := r.frameAt(k)
		if f == nil {
			return
		}
		if r.matches(f, t.Name()) || heading && f.html() && f.info&internal.Heading != 0 {
			// pop the k tracked elements above f, and the untracked ones
			r.pops = k + r.untracked - int(f.untracked)
			r.tokPops = true
			return
		}
		if r.mode != XML && r.barrier(f, sc) {
			return
		}
	}
}

// headingOpen reports whether an element named h1 to h6 is open.
func (r *Reader) headingOpen() bool {
	for _, h := range [...]string{"h1", "h2", "h3", "h4", "h5", "h6"} {
		if r.isOpen([]byte(h)) {
			return true
		}
	}
	return false
}

// matches reports whether an end tag named name closes f.
func (r *Reader) matches(f *frame, name []byte) bool {
	if r.mode == HTML {
		return htmlNamesEqual(r.frameName(f), name)
	}
	return bytes.Equal(r.frameName(f), name)
}

// pushFrame opens the element started by t, and reports whether it is
// tracked. Untracked elements are only counted, but still switch the lexer to
// raw text.
func (r *Reader) pushFrame(t *Token) bool {
	name := t.Name()
	tracked := len(r.stack) < r.maxDepth && len(r.names)+r.uriBytes+len(name) <= r.maxStack && r.pushTracked(t)
	if !tracked {
		if r.pushFlags&frameXMLVoid != 0 {
			// closed by its end tag only if it follows (see Reader.next), so
			// its name is kept
			r.voidName, r.voidOpen = append(r.voidName[:0], name...), true
		}
		r.untracked++
	}
	if r.pushFlags&frameHTML != 0 && r.pushInfo&(internal.Script|internal.RCDATA|internal.RawText|internal.Noscript|internal.Plaintext) != 0 {
		var lb [16]byte
		switch lname := lowerASCII(lb[:0], name); {
		case r.pushInfo&internal.Script != 0:
			r.raw, r.rawName, r.script = rawScript, "script", scriptState{}
		case r.pushInfo&internal.RCDATA != 0:
			r.raw, r.rawName = rawRCDATA, rawElemName(lname)
		case r.pushInfo&internal.RawText != 0, r.pushInfo&internal.Noscript != 0 && r.scripting:
			r.raw, r.rawName = rawText, rawElemName(lname)
		case r.pushInfo&internal.Plaintext != 0:
			r.raw, r.rawName = rawPlaintext, "plaintext"
		}
	}
	return tracked
}

// pushTracked pushes a frame for t, unless its declarations or kept start
// tag exceed the stack limit.
func (r *Reader) pushTracked(t *Token) bool {
	name := t.Name()
	f := frame{
		pos:       t.pos,
		names:     int32(len(r.names)),
		decls:     int32(len(r.decls)),
		attrs:     int32(len(r.keptAttrs)),
		untracked: int32(r.untracked),
		tag:       -1,
		ns:        r.pushNS,
		info:      r.pushInfo,
		flags:     r.pushFlags,
	}
	f.name = int32(len(r.names))
	r.names = append(r.names, name...)
	f.nameLen = int32(len(name))
	{
		first := len(r.decls)
		fold := r.mode == HTML
	decls:
		for a := range t.Attrs() {
			p, ok := xmlnsPrefix(a.Name(), fold)
			if !ok {
				continue
			}
			// the first declaration of a prefix wins, as in lookupNS
			if r.nsIndex != nil {
				if i, ok := r.nsIndex[string(r.nsKey(p))]; ok && int(i) >= first {
					continue
				}
			} else {
				for _, d := range r.decls[first:] {
					if namesEqual(r.names[d.prefix.start:d.prefix.end], p, fold) {
						continue decls
					}
				}
			}
			var d nsDecl
			d.prefix.start = int32(len(r.names))
			r.names = append(r.names, p...)
			d.prefix.end = int32(len(r.names))
			d.uri.start = d.prefix.end
			r.names = a.AppendValue(r.names)
			d.uri.end = int32(len(r.names))
			r.decls = append(r.decls, d)
			r.uriBytes += int(d.uri.end - d.uri.start)
			switch {
			case r.nsIndex != nil:
				r.indexDecl(len(r.decls) - 1)
			case len(r.decls) > internal.IndexMin:
				r.nsIndex = make(map[string]int32, len(r.decls))
				for i := range r.decls {
					r.indexDecl(i)
				}
			}
			if len(r.names)+r.uriBytes > r.maxStack {
				break // the element isn't tracked (see below)
			}
		}
	}
	if r.keepAttrs {
		raw := t.Raw()
		f.tag = int32(len(r.names))
		r.names = append(r.names, raw...)
		f.tagLen = int32(len(raw))
		r.keptAttrs = append(r.keptAttrs, t.attrs...)
	}
	if len(r.names)+r.uriBytes > r.maxStack {
		r.popDecls(int(f.decls))
		r.names = r.names[:f.names]
		r.keptAttrs = r.keptAttrs[:f.attrs]
		return false
	}
	h := r.openHash(name)
	f.same = -1
	if i, ok := r.open[h]; ok {
		f.same = i
	} else if r.open == nil {
		r.open = make(map[uint64]int32)
	}
	r.open[h] = int32(len(r.stack))
	r.stack = append(r.stack, f)
	return true
}

func (r *Reader) popFrame() {
	n := len(r.stack)
	if n == 0 {
		return
	}
	f := r.stack[n-1]
	if h := r.openHash(r.frameName(&f)); f.same >= 0 {
		r.open[h] = f.same
	} else {
		delete(r.open, h)
	}
	r.popDecls(int(f.decls))
	r.names = r.names[:f.names]
	r.keptAttrs = r.keptAttrs[:f.attrs]
	r.stack = r.stack[:n-1]
}

// popDecls removes the namespace declarations after the first n, releasing
// their strings.
func (r *Reader) popDecls(n int) {
	for i := len(r.decls) - 1; i >= n; i-- {
		d := &r.decls[i]
		r.uriBytes -= int(d.uri.end - d.uri.start)
		if r.nsIndex != nil {
			key := r.nsKey(r.names[d.prefix.start:d.prefix.end])
			if d.prev >= 0 {
				r.nsIndex[string(key)] = d.prev
			} else {
				delete(r.nsIndex, string(key))
			}
		}
	}
	clear(r.decls[n:])
	r.decls = r.decls[:n]
}

func rawElemName(lname []byte) string {
	for _, n := range [...]string{"style", "xmp", "iframe", "noembed", "noframes", "noscript", "textarea", "title"} {
		if string(lname) == n {
			return n
		}
	}
	panic("htmlstream: not a raw text element: " + string(lname))
}

// lowerASCII appends the ASCII-lowercased b to dst if it fits in its
// capacity, and otherwise returns dst.
func lowerASCII(dst, b []byte) []byte {
	if len(b) > cap(dst) {
		return dst
	}
	return ascii.AppendLower(dst, b)
}

// Traversal.

// Untracked reports whether a start tag opened an element nested past the
// stack limits (see [Reader.SetMaxDepth] and [Reader.SetMaxStackBytes]). An
// untracked element is counted in [Reader.Depth] but its name isn't kept: it
// is closed by the next end tag (whatever its name) when it is the innermost
// open element (a tracked element can be open inside it when it is untracked
// past the stack size limit), by the end tag of a tracked element below it,
// or at the end of the input. A void element under XML rules or with XML
// syntax is closed like a tracked one instead: by its end tag if it is the
// next token or follows whitespace, and otherwise by an implied end tag
// before the next token, which isn't unsound (see Element model in the
// package documentation). Its EndTag has a StartPos of -1 and isn't stray;
// when implied, it also has an empty Name. Its end is still known, so
// [Reader.Skip], [Reader.Children] and the Transformer's element operations
// work on it: it is the first EndTag which isn't stray with the same
// [Reader.Depth] as the start tag. In strict mode, they return a
// [SyntaxError] if it is closed without an end tag (except for a void
// element), since whether its end tag may be omitted isn't known. Its
// contents are read as if they were in the innermost tracked element (its
// namespace and rules), without the namespace declarations of the untracked
// elements, except for the contents of raw text and RCDATA elements (see
// Element model in the package documentation).
func (t *Token) Untracked() bool { return t.flags&tokUntracked != 0 }

// Opens reports whether a start tag opened an element, which is closed by
// a later (possibly implied) EndTag token. Void elements, and self-closing
// tags where self-closing syntax is honored, don't open an element, except
// for void elements in [HTML] mode with [Reader.SetXMLSyntax] and in
// documents read like in XML mode in [Universal] mode (see Element model in
// the package documentation).
func (t *Token) Opens() bool { return t.flags&(tokPushed|tokUntracked) != 0 }

// elementStart returns the depth (see Reader.Depth) of the element opened by
// the current start tag, marking it as watched in strict mode, or 0 if the
// current token doesn't open an element.
func (r *Reader) elementStart() (int, error) {
	t := r.cur
	if t == nil || t.kind != StartTag || !t.Opens() {
		return 0, nil
	}
	if r.strict && t.flags&tokUnsound != 0 {
		return 0, t.UnsoundError()
	}
	depth := r.depth()
	if t.flags&tokUntracked != 0 {
		if n := len(r.watched); r.strict && (n == 0 || r.watched[n-1] != depth) {
			if n >= r.maxDepth {
				return 0, r.untrackedLimit()
			}
			r.watched = append(r.watched, depth)
		}
	} else {
		r.stack[len(r.stack)-1].flags |= frameWatch
	}
	return depth, nil
}

// atEnd reports whether the current token is the end tag of the element at
// depth: each EndTag which isn't stray closes the innermost open element, so
// the first one at that depth closes it.
func (r *Reader) atEnd(depth int) bool {
	t := r.cur
	return t != nil && t.kind == EndTag && t.flags&tokStray == 0 && r.depth() == depth
}

// Skip advances to the end tag of the element opened by the current start
// tag, which may be untracked (see [Token.Untracked]). If the current token
// doesn't open an element, Skip does nothing. It returns the error which
// stopped the Reader, if any (see [Reader.Err]), including the SyntaxError of
// strict mode.
func (r *Reader) Skip() error {
	depth, err := r.elementStart()
	if err != nil {
		r.err = err
		return err
	}
	if depth == 0 {
		return r.err
	}
	for r.Next() {
		if r.atEnd(depth) {
			return nil
		}
	}
	return r.err
}

// Children returns an iterator over the tokens inside the element opened by
// the current start tag, which may be untracked (see [Token.Untracked]).
// After the iteration completes, the current token is the element's end tag.
// If the current token doesn't open an element, the iterator yields nothing.
// Check [Reader.Err] after iterating.
func (r *Reader) Children() iter.Seq[*Token] {
	return func(yield func(*Token) bool) {
		depth, err := r.elementStart()
		if err != nil {
			r.err = err
			return
		}
		if depth == 0 {
			return
		}
		for r.Next() {
			if r.atEnd(depth) || !yield(r.cur) {
				return
			}
		}
	}
}

// Find advances to the next start tag matching m. It returns false at the end
// of the input or on error.
func (r *Reader) Find(m Matcher) bool {
	for r.Next() {
		t := r.cur
		if t.kind == StartTag && m(t) {
			if r.strict && t.flags&tokUnsound != 0 {
				r.err = t.UnsoundError()
				r.cur = nil
				return false
			}
			return true
		}
	}
	return false
}

// Matcher reports whether a token matches, for [Reader.Find]. Matchers can be
// combined with [And], [Or] and [Not].
//
// The attribute matchers ([HasAttr], [AttrEquals], [AttrFunc], etc.) also
// match end tags with attributes (e.g., </p class="x">), whose attributes
// HTML parsers ignore. [Reader.Find] only returns start tags.
type Matcher func(*Token) bool

// Tag matches start and end tags named name (see [Token.Is]) which match
// all of ms.
func Tag(name string, ms ...Matcher) Matcher {
	return func(t *Token) bool {
		if !t.Is(name) {
			return false
		}
		for _, m := range ms {
			if !m(t) {
				return false
			}
		}
		return true
	}
}

// TagNS matches start and end tags with the local name local in the
// namespace ns (see [Token.NS]) which match all of ms. Unlike [Tag], it
// doesn't match stray end tags, which have no namespace.
func TagNS(ns, local string, ms ...Matcher) Matcher {
	return func(t *Token) bool {
		if t.kind != StartTag && t.kind != EndTag || t.NS() != ns {
			return false
		}
		if l := t.Local(); t.HTMLRules() {
			if !htmlNameIs(l, local) {
				return false
			}
		} else if string(l) != local {
			return false
		}
		for _, m := range ms {
			if !m(t) {
				return false
			}
		}
		return true
	}
}

// HasAttr matches tags with an attribute named name.
func HasAttr(name string) Matcher {
	return func(t *Token) bool {
		_, ok := t.Attr(name)
		return ok
	}
}

// AttrEquals matches tags with an attribute named name whose decoded value
// is value.
func AttrEquals(name, value string) Matcher {
	return AttrFunc(name, func(v []byte) bool {
		return string(v) == value
	})
}

// AttrFunc matches tags with an attribute named name whose decoded value
// matches fn. The value is only valid during the call. It is in a buffer of
// the token shared with the other matchers of decoded values ([AttrEquals],
// [AttrHasWord], [HasClass], [AttrNSEquals], [AttrNSFunc] and [HasAttrNS]),
// so fn must not call them on the same token.
func AttrFunc(name string, fn func(value []byte) bool) Matcher {
	return func(t *Token) bool {
		a, ok := t.Attr(name)
		if !ok {
			return false
		}
		t.src.scratch = a.AppendValue(t.src.scratch[:0])
		return fn(t.src.scratch)
	}
}

// HasClass matches tags with class in their whitespace-separated class
// attribute. Classes are compared exactly, as browsers do in no-quirks and
// limited-quirks mode documents. In quirks mode, browsers compare classes
// ASCII case-insensitively.
func HasClass(class string) Matcher {
	return AttrHasWord("class", class)
}

// AttrHasWord matches tags with word in the whitespace-separated value of the
// attribute named name (e.g., class or rel). Words are compared exactly;
// for attributes whose keywords are ASCII case-insensitive, like rel, use
// [AttrFunc].
func AttrHasWord(name, word string) Matcher {
	return AttrFunc(name, func(v []byte) bool { return hasWord(v, word) })
}

func hasWord(v []byte, word string) bool {
	for w := range bytes.FieldsFuncSeq(v, isHTMLSpace) {
		if string(w) == word {
			return true
		}
	}
	return false
}

// HasAttrNS matches tags with an attribute with the local name local in the
// namespace ns (see [Token.AttrNamespace]).
func HasAttrNS(ns, local string) Matcher {
	return AttrNSFunc(ns, local, func([]byte) bool { return true })
}

// AttrNSEquals matches tags with an attribute with the local name local
// (compared like [Attr.Is]) in the namespace ns (see [Token.AttrNamespace])
// whose decoded value is value.
func AttrNSEquals(ns, local, value string) Matcher {
	return AttrNSFunc(ns, local, func(v []byte) bool {
		return string(v) == value
	})
}

// AttrNSFunc matches tags with an attribute with the local name local
// (compared like [Attr.Is]) in the namespace ns (see [Token.AttrNamespace])
// whose decoded value matches fn. The value is only valid during the call,
// and fn must not call the other matchers of decoded values on the same
// token, as for [AttrFunc].
func AttrNSFunc(ns, local string, fn func(value []byte) bool) Matcher {
	return func(t *Token) bool {
		for a := range t.Attrs() {
			if a.localIs(local) && t.AttrNamespace(a) == ns {
				t.src.scratch = a.AppendValue(t.src.scratch[:0])
				if fn(t.src.scratch) {
					return true
				}
			}
		}
		return false
	}
}

// And matches tokens which match all of ms.
func And(ms ...Matcher) Matcher {
	return func(t *Token) bool {
		for _, m := range ms {
			if !m(t) {
				return false
			}
		}
		return true
	}
}

// Or matches tokens which match any of ms.
func Or(ms ...Matcher) Matcher {
	return func(t *Token) bool {
		for _, m := range ms {
			if m(t) {
				return true
			}
		}
		return false
	}
}

// Not matches tokens which don't match m.
func Not(m Matcher) Matcher {
	return func(t *Token) bool { return !m(t) }
}

func isHTMLSpace(c rune) bool {
	return c < utf8.RuneSelf && ascii.IsSpace(byte(c))
}

// Namespaces.

// Prefix returns the namespace prefix of a tag name as written, or nil
// (always under HTML rules, see Local). A name starting with a colon (e.g.,
// :x, under XML rules) has none, as in XML parsers. Other names are split at
// the first colon, also where they aren't namespace-well-formed, which
// encoding/xml reads as local names without a prefix: <x:/> has the prefix
// x and an empty local name, and <a:b:c> the prefix a and the local name
// b:c.
func (t *Token) Prefix() []byte {
	if t.mode == HTML {
		return nil
	}
	if i := bytes.IndexByte(t.Name(), ':'); i > 0 {
		return t.Name()[:i]
	}
	return nil
}

// Local returns the tag name as written without the namespace prefix (see
// [Token.AppendLocalName]). Under HTML rules (see [Token.HTMLRules]), where
// HTML parsers treat a name like "o:p" as a whole, it is the whole name, and
// Prefix returns nil.
func (t *Token) Local() []byte {
	n := t.Name()
	if i := bytes.IndexByte(n, ':'); i > 0 && t.mode != HTML {
		return n[i+1:]
	}
	return n
}

// NS returns the namespace URI of a start or end tag, or an empty string.
// A stray end tag (see [Token.Stray]) has no namespace, since it closes no
// element (HTML parsers handle a stray </p> or </br> under HTML rules as an
// element in the XHTML namespace, see Element model in the package
// documentation).
//
// Under HTML rules (see [Token.HTMLRules]), this is the namespace an HTML
// parser would put the element in (XHTML, SVG or MathML); namespace
// declarations are ignored (see [Reader.LookupPrefix]). Under XML rules, it
// is resolved from the namespace declarations.
func (t *Token) NS() string {
	if t.kind != StartTag && t.kind != EndTag || t.flags&tokStray != 0 {
		return ""
	}
	if t.mode == XML {
		if t.src.rd == nil {
			return t.src.nsURI // a copy
		}
		var self *Token
		if t.kind == StartTag {
			self = t
		}
		uri, _ := t.src.rd.lookupNSString(t.Prefix(), len(t.src.rd.decls), self)
		return uri
	}
	return t.ns.uri()
}

// AttrNamespace returns the namespace URI of an attribute of the token, which
// is empty for unprefixed attributes other than xmlns.
//
// The xml and xmlns prefixes are always bound, and xmlns is in the XMLNS
// namespace. On SVG and MathML elements under HTML rules (see
// [Token.HTMLRules]), xlink:actuate, xlink:arcrole, xlink:href, xlink:role,
// xlink:show, xlink:title and xlink:type are in the XLink namespace whatever
// the namespace declarations say, as in HTML parsers (see
// [adjust foreign attributes]). Under HTML rules, these prefixes and names
// are matched ASCII case-insensitively, like attribute names.
//
// Other prefixed attributes, including other xlink ones, are resolved using
// the namespace declarations in scope, in all modes. Under HTML rules, this
// is an extension: HTML parsers put them in no namespace. Likewise, HTML
// parsers only put xml:lang and xml:space (in the XML namespace) and xmlns
// and xmlns:xlink (in the XMLNS namespace) in a namespace on SVG and MathML
// elements, and other xml and xmlns attributes, and those of HTML elements,
// in no namespace. Under HTML rules, prefixes and namespace declarations are
// matched ASCII case-insensitively, like attribute names. On a copy (see
// [Token.Clone]), the declarations in scope when the copy was made are used.
//
// [adjust foreign attributes]: https://html.spec.whatwg.org/multipage/parsing.html#adjust-foreign-attributes
func (t *Token) AttrNamespace(a Attr) string {
	ns, prefix := fixedAttrNS(a, t.htmlForeign())
	if prefix == nil {
		return ns
	}
	return t.prefixNS(prefix)
}

// fixedAttrNS returns the namespace of an attribute which doesn't depend on
// the namespace declarations in scope (empty for unprefixed attributes other
// than xmlns), or the prefix to resolve with them. htmlForeign reports
// whether the attribute is on an SVG or MathML element parsed like HTML
// parsers do.
func fixedAttrNS(a Attr, htmlForeign bool) (ns string, prefix []byte) {
	prefix = a.Prefix()
	switch {
	case prefix == nil:
		if a.Is("xmlns") {
			return NamespaceXMLNS, nil
		}
		return "", nil
	case prefixIs(prefix, "xmlns", a.fold):
		return NamespaceXMLNS, nil
	case prefixIs(prefix, "xml", a.fold):
		return NamespaceXML, nil
	case prefixIs(prefix, "xlink", a.fold) && htmlForeign && adjustedXLinkAttr(a):
		return NamespaceXLink, nil
	}
	return "", prefix
}

// prefixNS returns the namespace of an attribute prefix (see fixedAttrNS):
// the one the declarations in scope bind it to, or for a copy, the one
// recorded when it was copied.
func (t *Token) prefixNS(prefix []byte) string {
	if t.src.rd == nil {
		raw := t.Raw()
		i, ok := slices.BinarySearchFunc(t.src.prefixNS, prefix, func(p prefixNS, prefix []byte) int {
			return bytes.Compare(raw[p.prefix.start:p.prefix.end], prefix)
		})
		if !ok {
			return ""
		}
		return t.src.prefixNS[i].uri
	}
	var self *Token
	if t.kind == StartTag {
		self = t
	}
	uri, _ := t.src.rd.lookupNSString(prefix, len(t.src.rd.decls), self)
	return uri
}

// htmlForeign reports whether the token is an SVG or MathML element parsed
// like HTML parsers do (not following XML rules).
func (t *Token) htmlForeign() bool {
	return t.ns == nsSVG || t.ns == nsMathML
}

// adjustedXLinkAttr reports whether the local name of an attribute with the
// xlink prefix is one HTML parsers put in the XLink namespace on SVG and
// MathML elements.
func adjustedXLinkAttr(a Attr) bool {
	for _, l := range [...]string{"actuate", "arcrole", "href", "role", "show", "title", "type"} {
		if a.localIs(l) {
			return true
		}
	}
	return false
}

func prefixIs(prefix []byte, name string, fold bool) bool {
	if fold {
		return ascii.EqualFold(prefix, name)
	}
	return string(prefix) == name
}

// AppendLocalName appends the tag name without the namespace prefix to dst.
// If the tag follows HTML rules (see [Token.HTMLRules]), it is the name HTML
// parsers give the element: ASCII-lowercased and with NUL replaced with
// U+FFFD, like the HTML tokenizer does in the [tag name state]. This includes
// SVG and MathML elements; HTML parsers then give some SVG elements a name
// with uppercase letters (e.g., foreignObject), which [AdjustSVGTagName]
// returns. This avoids allocating to switch on tag names:
//
//	var buf [16]byte
//	switch string(tok.AppendLocalName(buf[:0])) {
//	case "p":
//	}
//
// [tag name state]: https://html.spec.whatwg.org/multipage/parsing.html#tag-name-state
func (t *Token) AppendLocalName(dst []byte) []byte {
	if t.HTMLRules() {
		return appendHTMLName(dst, t.Name())
	}
	return append(dst, t.Local()...)
}

func nsString(uri []byte) string {
	for _, s := range [...]string{NamespaceXHTML, NamespaceSVG, NamespaceMathML, NamespaceXLink, NamespaceXML} {
		if string(uri) == s {
			return s
		}
	}
	return string(uri)
}

// LookupPrefix resolves a namespace prefix ("" for the default namespace)
// using the namespace declarations in scope, including those on the current
// start tag. It works in all modes, even though namespace declarations are
// otherwise ignored under HTML rules (see [Token.HTMLRules]), where prefixes
// and declarations are matched ASCII case-insensitively, like attribute
// names. It only reports declarations: [Token.AttrNamespace] ignores them for
// xlink:href and the other xlink attributes HTML parsers put in the XLink
// namespace on SVG and MathML elements.
func (r *Reader) LookupPrefix(prefix string) (string, bool) {
	var self *Token
	if r.cur != nil && r.cur.kind == StartTag {
		self = r.cur
	}
	p := []byte(prefix)
	if p == nil {
		p = []byte{}
	}
	return r.lookupNSString(p, len(r.decls), self)
}

// Element is an open element. It is only valid until the next call to
// Reader.Next.
type Element struct {
	r *Reader
	i int // index in r.stack
}

// Stack returns an iterator over the open elements, innermost first. While
// the current token is a start tag which opens a tracked element, or that
// element's end tag, it is the innermost element. Elements nested past the
// stack limits (see [Token.Untracked]) aren't included, though [Reader.Depth]
// counts them.
func (r *Reader) Stack() iter.Seq[Element] {
	return func(yield func(Element) bool) {
		for i := len(r.stack) - 1; i >= 0; i-- {
			if !yield(Element{r, i}) {
				return
			}
		}
	}
}

func (e Element) frame() *frame { return &e.r.stack[e.i] }

// Name returns the element name as written in the start tag (see
// [Token.Name]).
func (e Element) Name() []byte { return e.r.frameName(e.frame()) }

// Pos returns the position of the element's start tag.
func (e Element) Pos() Pos { return e.frame().pos }

// NS returns the namespace URI of the element (see [Token.NS]).
func (e Element) NS() string {
	r := e.r
	if r.mode == XML {
		prefix := e.Prefix()
		if prefix == nil {
			prefix = []byte{}
		}
		uri, _ := r.lookupNSString(prefix, e.declsEnd(), nil)
		return uri
	}
	return e.frame().ns.uri()
}

// HTMLRules reports whether the element follows HTML rules (see
// [Token.HTMLRules]).
func (e Element) HTMLRules() bool { return e.r.mode == HTML }

// xmlRules is like Token.xmlRules.
func (e Element) xmlRules() bool { return e.r.mode == XML }

// Prefix returns the namespace prefix of the element name as written, or nil
// (see [Token.Prefix]).
func (e Element) Prefix() []byte {
	if e.r.mode == HTML {
		return nil
	}
	n := e.Name()
	if i := bytes.IndexByte(n, ':'); i > 0 {
		return n[:i]
	}
	return nil
}

// Local returns the element name as written without the namespace prefix
// (see [Token.Local]).
func (e Element) Local() []byte {
	n := e.Name()
	if i := bytes.IndexByte(n, ':'); i > 0 && e.r.mode != HTML {
		return n[i+1:]
	}
	return n
}

// AppendLocalName appends the element name without the namespace prefix to
// dst, as [Token.AppendLocalName] does.
func (e Element) AppendLocalName(dst []byte) []byte {
	if e.HTMLRules() {
		return appendHTMLName(dst, e.Name())
	}
	return append(dst, e.Local()...)
}

// Is reports whether the element is named name (see [Token.Is]).
func (e Element) Is(name string) bool {
	if e.HTMLRules() {
		return htmlNameIs(e.Name(), name)
	}
	return string(e.Name()) == name
}

// LocalIs reports whether the local name of the element (see [Element.Local])
// is name, compared like [Element.Is].
func (e Element) LocalIs(name string) bool {
	if e.HTMLRules() {
		return htmlNameIs(e.Local(), name)
	}
	return string(e.Local()) == name
}

// Attrs returns an iterator over the element's attributes. It is empty unless
// [Reader.SetKeepAttrs] was enabled. Like [Token.Attrs], ranging over it only
// avoids allocating if the compiler inlines the iterator; [Element.NumAttrs]
// and [Element.AttrAt] never allocate.
func (e Element) Attrs() iter.Seq[Attr] {
	return func(yield func(Attr) bool) {
		for i, n := 0, e.NumAttrs(); i < n; i++ {
			if !yield(e.AttrAt(i)) {
				return
			}
		}
	}
}

// keptAttrs returns the kept start tag of the element and its attributes.
func (e Element) keptAttrs() ([]byte, []attrSpan) {
	r, f := e.r, e.frame()
	if f.tag < 0 {
		return nil, nil
	}
	end := len(r.keptAttrs)
	if e.i+1 < len(r.stack) {
		end = int(r.stack[e.i+1].attrs)
	}
	return r.names[f.tag : f.tag+f.tagLen], r.keptAttrs[f.attrs:end]
}

// NumAttrs returns the number of the element's attributes, including
// duplicates. It is zero unless [Reader.SetKeepAttrs] was enabled.
func (e Element) NumAttrs() int {
	_, spans := e.keptAttrs()
	return len(spans)
}

// AttrAt returns the element's attribute at index i in source order (see
// [Element.Attrs]). It panics if i is out of the range [0, NumAttrs).
func (e Element) AttrAt(i int) Attr {
	tag, spans := e.keptAttrs()
	xml := e.xmlRules()
	return Attr{tag, spans[i], e.HTMLRules(), xml, xml && e.r.xml11}
}

// Attr returns the element's first attribute named name. It requires
// [Reader.SetKeepAttrs]: without it, it reports false.
func (e Element) Attr(name string) (Attr, bool) {
	for a := range e.Attrs() {
		if a.Is(name) {
			return a, true
		}
	}
	return Attr{}, false
}

// HasAttr reports whether the element has an attribute named name (see
// [Attr.Is]). It requires [Reader.SetKeepAttrs]: without it, it reports
// false.
func (e Element) HasAttr(name string) bool {
	_, ok := e.Attr(name)
	return ok
}

// AttrNS returns the element's first attribute with the local name local in
// the namespace ns, like [Token.AttrNS]. It requires [Reader.SetKeepAttrs]:
// without it, it reports false.
func (e Element) AttrNS(ns, local string) (Attr, bool) {
	for a := range e.Attrs() {
		if a.localIs(local) && e.AttrNamespace(a) == ns {
			return a, true
		}
	}
	return Attr{}, false
}

// AttrNamespace returns the namespace URI of an attribute of the element, like
// [Token.AttrNamespace], using the namespace declarations in scope at the
// element.
func (e Element) AttrNamespace(a Attr) string {
	ns, prefix := fixedAttrNS(a, e.frame().foreign())
	if prefix == nil {
		return ns
	}
	uri, _ := e.r.lookupNSString(prefix, e.declsEnd(), nil)
	return uri
}

// declsEnd returns the number of namespace declarations in scope at the
// element.
func (e Element) declsEnd() int {
	if e.i+1 < len(e.r.stack) {
		return int(e.r.stack[e.i+1].decls)
	}
	return len(e.r.decls)
}

// ReadText appends the decoded text (see [Token.AppendText]) of the current
// Text, Comment, CDATA or ProcInst token to dst and returns the result. At
// other tokens (e.g., a start tag), it appends nothing:
// [github.com/pgaskin/htmlstream/htmlutil.TextContent] and
// [github.com/pgaskin/htmlstream/htmlutil.InnerText] read the text of an
// element. If limit is positive, at most limit bytes are appended, without
// splitting a UTF-8 sequence; the rest of the text is skipped and truncated
// is true.
//
// If the text is split into several tokens (see [Token.Partial]), ReadText
// calls Next until the last of them, which becomes the current token. Check
// Err afterwards, since Next can fail. With a [Transformer], each token
// advanced over is written as by Next: edits made before ReadText apply to
// the first token, and edits made after it to the last one. To replace all
// of the text, hold back the output from the first token (see
// [Transformer.Hold]), replace the last token, and end the hold with
// [Transformer.HoldDiscard].
func (r *Reader) ReadText(dst []byte, limit int) (b []byte, truncated bool) {
	start := len(dst)
	for t := r.cur; t != nil; t = r.cur {
		if !truncated {
			dst = t.AppendText(dst)
			if limit > 0 && len(dst)-start > limit {
				truncated = true
				dst = dst[:start+limit]
				// don't cut a UTF-8 sequence
				for i := len(dst) - 1; i >= start && i >= len(dst)-utf8.UTFMax; i-- {
					if utf8.RuneStart(dst[i]) {
						if !utf8.FullRune(dst[i:]) {
							dst = dst[:i]
						}
						break
					}
				}
			}
		}
		if !t.Partial() || !r.Next() {
			break
		}
	}
	return dst, truncated
}
