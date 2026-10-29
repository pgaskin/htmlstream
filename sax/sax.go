// Package sax provides a simplified SAX-style wrapper for parsing
// HTML/XHTML/XML.
//
// It also provides an [encoding/xml.TokenReader] adapter ([TokenReader]) so it
// can be used with the stdlib [encoding/xml.Decoder] (see
// [encoding/xml.NewTokenDecoder]) or other libraries. It supports all
// htmlstream reader modes ([htmlstream.Universal], [htmlstream.HTML],
// [htmlstream.XML]), and behaves accordingly. It also supports reading a
// partial fragment (see [Reader.SetFragment]).
//
// A [Reader] (see [NewReader]) reads the tokens of an [htmlstream.Reader] and
// delivers them as events: [Reader.Next] advances to the next [Event] and
// [Reader.Event] returns it, or [Reader.All] iterates over the events, and
// [Reader.Walk] calls the functions of a [Handler] for them. Afterwards,
// [Reader.Err] returns the error that stopped it, if any. Unless an error
// stops the Reader, every [StartElement] event is followed by exactly one
// [EndElement] event with the same [Name], including for void and
// self-closed elements, and [Reader.Path] returns the names of the open
// elements. Text is decoded, and text split into several tokens and
// adjacent CDATA sections are delivered as one [Text] event, up to a
// maximum size (see [Reader.SetMaxText]). [Reader.Skip] skips the contents
// of an element, and [Reader.ReadText] reads its text.
//
// It is simpler to use than the [htmlstream.Reader], which is more efficient
// and better for edits which must leave the rest of the input unchanged (see
// [htmlstream.Transformer]): the events don't keep the markup as written, and
// the Reader drops some tags to keep the elements balanced (see Warnings and
// strict mode). It doesn't check that XML documents are well-formed, even
// in strict mode: references to entities other than the named character
// references of HTML are left as they are (see [htmlstream.XML]), and
// duplicate attributes, unbound namespace prefixes, several root elements
// and text outside the root element aren't errors.
//
// # Elements
//
// The elements are those of the element model of the htmlstream.Reader (see
// Element model in the htmlstream package documentation): end tags are
// implied where HTML parsers imply them, and elements are closed by the end
// of the input, by an implied [EndElement] event (see [Event.Implied]).
// Element names are as parsers give them: in HTML (see
// [htmlstream.Token.HTMLRules]), local names are lowercase, except for SVG
// elements with mixed-case names (see [htmlstream.AdjustSVGTagName]), and
// the namespace is that of HTML parsers, and so is the local name of an
// element with a prefix under HTML rules: the qualified name, lowercase
// (e.g., o:p, see [htmlstream.Token.AppendLocalName]). The local name of an
// element with a namespace prefix not bound to a namespace is the qualified
// name, with the prefix (see [Name]).
//
// In [htmlstream.HTML] mode, and in documents read like in HTML mode in
// [htmlstream.Universal] mode, the implied html, head and body elements (see
// the tree construction stage of the [HTML Standard]) are delivered as
// StartElement and EndElement events without tags in the input (see
// [Event.Implied]), where [htmlutil.Document] reports them. Like HTML
// parsers, the Reader keeps the html and body elements open after their end
// tags (see the [after body insertion mode]), so the content after them is in
// the body, and their EndElement events are delivered at the end of the
// input. This also applies when the Reader starts in the middle of the input,
// but not to fragments. The paths of the elements match the document tree of
// HTML parsers, with these differences:
//
//   - The differences between the element model of the htmlstream.Reader
//     and the tree construction stage of HTML parsers, e.g., there is no
//     adoption agency algorithm, no implied tbody element and no foster
//     parenting, template contents are read like other content, and a body
//     or html end tag closes the elements open in the body.
//   - Comments after a body or html end tag, before any other content except
//     whitespace, are delivered in the elements open there, in the body.
//     HTML parsers put them after the body element, or after the html
//     element once an html end tag was read.
//   - Head content after the end of the head (e.g., a meta element) stays
//     where it is, outside the head. HTML parsers move it into the head.
//   - Whitespace before the html element and before the head is delivered,
//     while HTML parsers drop it.
//   - Misplaced html, head and body start tags are dropped (see Warnings and
//     strict mode). HTML parsers add the attributes of html and body start
//     tags to the element.
//   - Frameset documents aren't recognized: a frameset element after the
//     head is put in a body element.
//   - With scripting disabled (the default, see
//     [htmlstream.Reader.SetScripting]), a noscript element in an implied
//     head contains the content following it, which HTML parsers put in the
//     body, and the head ends after it.
//
// The html, head and body elements are only synthesized if the
// htmlstream.Reader is in HTML or Universal mode and hasn't read any token
// when the Reader starts, unless the input is read as a fragment (see
// [Reader.SetFragment]). A Reader can also start in the middle of the input,
// e.g., after [htmlstream.Reader.Find]: its path then starts with the
// elements open in the htmlstream.Reader (see [htmlstream.Reader.Stack]), its
// first event is that of the current token, and the EndElement events of the
// elements open at the start have no StartElement events. The end tags of
// html and body elements implied before the start aren't for elements open in
// the htmlstream.Reader, so they are stray, and are dropped with a Warning
// event like other stray end tags. Under XML rules (see
// [htmlstream.Token.HTMLRules]), and for fragments, html and body elements
// are closed by their end tags, as in the htmlstream.Reader.
//
// Like HTML parsers, the Reader drops a newline directly after the start
// tag of a pre, listing or textarea element following HTML rules (see the
// [in body insertion mode]), but not one at the start of a CDATA section
// there (read with XML syntax), which HTML parsers read as a bogus comment,
// and XML parsers keep. It drops a byte order mark at the start of the
// input, like HTML and XML parsers (see [decode] in the Encoding Standard,
// and [Autodetection of Character Encodings] in XML 1.0). Text events keep
// NUL as the tokenizer emits it in text (see [htmlstream.Token.AppendText]),
// while HTML parsers drop it in HTML content and replace it with U+FFFD in
// SVG and MathML content.
//
// With XML syntax (with [htmlstream.Reader.SetXMLSyntax] in HTML mode, and in
// documents read like in HTML mode in Universal mode), the contents of title,
// textarea, script and style elements are text up to their end tag, as in
// HTML parsers, so a CDATA section in them is text, markers included (e.g.,
// <title><![CDATA[a<b]]></title> is the text "<![CDATA[a<b]]>"), with
// character references decoded in title and textarea. XML parsers read it
// as a CDATA section, without the markers, and the polyglot package removes
// a CDATA section around all of the contents. [htmlutil.CutCDATAWrapper]
// with [htmlutil.CDATABare] removes such a section from the text.
//
// # Warnings and strict mode
//
// To keep the elements balanced, the Reader drops some tags, and delivers a
// [Warning] event instead: for stray end tags (see [htmlstream.Token.Stray])
// other than those of the implied or kept-open head, body and html elements,
// and for ignored or misplaced html, head and body start tags (their end tags
// are dropped too, and the contents are kept). HTML parsers ignore these
// start tags, except that they add the attributes of an html or body start
// tag to the existing element. They ignore most stray end tags too, but read
// a stray </p> as an empty p element and a stray </br> as a br element. Under
// HTML rules, and before the rules are chosen in Universal mode, the Reader
// also drops, like HTML parsers, start tags cut off by the end of the input
// (see [htmlstream.Token.Incomplete]), and doctypes in a fragment or after
// another doctype or content other than whitespace, comments and processing
// instructions (since the Reader started). Under XML rules, these are
// delivered, as the Reader doesn't check that XML documents are well-formed:
// a start tag cut off by the end of the input is an element without contents.
// The text of the event says what was dropped. A Warning event also follows
// the Text event of a CDATA section in HTML content (with
// [htmlstream.Reader.SetXMLSyntax] in HTML mode, and in documents read like
// in HTML mode in Universal mode) which reaches the end of the input, as its
// end is missing: its text is the rest of the input, while HTML parsers read
// a bogus comment ending at the first ">". Its position is the end of the
// input.
//
// In strict mode (see [Reader.SetStrict]), a [SyntaxError] event is
// delivered instead of a Warning event, and for every structurally unsound
// token (see [htmlstream.Token.Unsound]). It stops the Reader: the next call
// to Next returns false, and Err returns the error, an
// [*htmlstream.SyntaxError].
//
// Elements nested past the stack limits of the htmlstream.Reader (see
// [htmlstream.Token.Untracked]) have no name in the element model, so the
// Reader can't deliver them as balanced elements. Instead, their tokens,
// from their start tag to their end tag, are delivered as [Untracked]
// events, and their text isn't lost.
//
// # Limits
//
// Like the htmlstream.Reader, a Reader reads input of any size with bounded
// memory: the text of a Text or Comment event is limited in size (see
// [Reader.SetMaxText]), longer text being delivered in several events (see
// [Event.Partial]), and that of other events is limited by the maximum
// token size of the htmlstream.Reader (see
// [htmlstream.Reader.SetMaxTokenSize]); the path has at most the elements
// tracked by the htmlstream.Reader (see [htmlstream.Reader.SetMaxDepth]) and
// the html, head and body elements; and at most 4096 names of up to 64 bytes
// are interned (other names are allocated for each event).
//
// To merge text, the Reader reads the token after it before delivering it,
// and it keeps track of all the tokens: after the first call to Next, the
// htmlstream.Reader must not be advanced by other means (e.g., by its own
// Next or Skip method) while the Reader is used. If the current event has a
// token (see [Event.Token]), it is the current token of the
// htmlstream.Reader, which can then be read further without the Reader
// (e.g., to decode the elements found by [htmlstream.Reader.Find] one at a
// time, with a new Reader for each). A Reader can't be used with a
// Transformer's Reader (see [htmlstream.Transformer.Reader]), since edits
// apply to the current token of the htmlstream.Reader, which isn't always
// the one of the current event.
//
// [HTML Standard]: https://html.spec.whatwg.org/multipage/parsing.html#tree-construction
// [after body insertion mode]: https://html.spec.whatwg.org/multipage/parsing.html#parsing-main-afterbody
// [in body insertion mode]: https://html.spec.whatwg.org/multipage/parsing.html#parsing-main-inbody
// [decode]: https://encoding.spec.whatwg.org/#decode
// [Autodetection of Character Encodings]: https://www.w3.org/TR/xml/#sec-guessing
package sax

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"iter"
	"strconv"
	"unicode/utf8"

	"github.com/pgaskin/htmlstream"
	"github.com/pgaskin/htmlstream/ascii"
	"github.com/pgaskin/htmlstream/htmlutil"
	"github.com/pgaskin/htmlstream/internal"
)

// DefaultMaxText is the default maximum size of the text of an event (see
// [Reader.SetMaxText]), 1 MiB.
const DefaultMaxText = 1 << 20

const (
	maxNames   = 4096 // the number of names interned
	maxNameLen = 64   // the size of the longest name interned
)

// Kind is the kind of an [Event].
type Kind uint8

const (
	StartElement Kind = iota + 1 // the start of an element
	EndElement                   // the end of an element
	Text                         // text, including CDATA sections
	Comment                      // a comment
	ProcInst                     // a processing instruction
	Doctype                      // a document type declaration

	Warning     // dropped input, or a CDATA section reaching the end of the input
	SyntaxError // unsound or dropped input in strict mode
	Untracked   // a token of an element nested past the stack limits
)

var kinds = [...]string{"", "StartElement", "EndElement", "Text", "Comment", "ProcInst", "Doctype", "Warning", "SyntaxError", "Untracked"}

// String returns the name of the constant for k, e.g., "StartElement".
func (k Kind) String() string {
	if k != 0 && int(k) < len(kinds) {
		return kinds[k]
	}
	return "Kind(" + strconv.Itoa(int(k)) + ")"
}

// Name is the name of an element, as given by parsers (see Elements in the
// package documentation).
//
// If the name has a namespace prefix not bound to a namespace (e.g., in XML
// mode, x:a without a declaration of the prefix x), Space is empty and Local
// is the qualified name, with the prefix (e.g., Name{"", "x:a"}), so that
// the element isn't confused with one without a prefix. There are no prefixes
// under HTML rules (see [htmlstream.Token.HTMLRules]): HTML parsers read a
// name like x:a as a whole.
type Name struct {
	Space string // the namespace URI (see [htmlstream.Token.NS])
	Local string // the local name, or the qualified name if the prefix isn't bound
}

var (
	nameHTML = Name{htmlstream.NamespaceXHTML, "html"}
	nameHead = Name{htmlstream.NamespaceXHTML, "head"}
	nameBody = Name{htmlstream.NamespaceXHTML, "body"}

	nameTemplate = Name{htmlstream.NamespaceXHTML, "template"}
)

// Event is an event delivered by a [Reader]. It is only valid until the
// Reader advances, and its Text and token alias the Reader's buffers.
//
// It implements [htmlutil.NamedElement], so the element categories of
// htmlutil classify StartElement and EndElement events (e.g.,
// htmlutil.IsBlock(ev)).
type Event struct {
	// Kind is the kind of event.
	Kind Kind

	// Name is the name of the element of a StartElement or EndElement event
	// (see [Name]), or the target of a ProcInst event (in Local, as
	// written).
	Name Name

	// Text is the text of the event:
	//   - for a Text event, the decoded text (see [htmlstream.Token.AppendText]),
	//     which is never empty;
	//   - for a Comment event, the decoded comment, which can be empty (</>,
	//     which HTML parsers ignore, gives no event, see
	//     [htmlstream.Token.Bogus]);
	//   - for a ProcInst event, the data after the target, as XML parsers
	//     read it (see [htmlstream.Token.AppendText]);
	//   - for a Doctype event, the contents of the doctype after "DOCTYPE"
	//     and the whitespace after it, as written (e.g., "html");
	//   - for a Warning or SyntaxError event, a description of the problem
	//     (e.g., "dropped stray </span>");
	//   - for an Untracked event, the text of a Text, CDATA, Comment,
	//     ProcInst or Doctype token, as for those events.
	Text []byte

	// Partial reports that the text of a Text or Comment event continues in
	// the next event, of the same kind, because it is longer than the
	// maximum text size (see [Reader.SetMaxText]), or that the token of an
	// Untracked event is continued by the token of the next one (see
	// [htmlstream.Token.Partial]), because it is longer than the chunk size
	// of the htmlstream.Reader (see [htmlstream.Reader.SetChunkSize]).
	// In strict mode, the next event can be a SyntaxError event instead.
	// Without Partial, the next event doesn't continue the text: text events
	// end at other markup (e.g., a comment), at a dropped tag, at a body or
	// html end tag, where HTML parsers start the body, and in strict mode, at
	// an unsound token.
	Partial bool

	// Implied reports that the event has no tag in the input: a StartElement
	// event for an implied html, head or body element (see Elements in the
	// package documentation), or an EndElement event for an element closed
	// without an end tag (see [htmlstream.Token.Implied]), a void or
	// self-closed element, an implied html, head or body element, or an html
	// or body element kept open after its end tag.
	Implied bool

	// Pos is the position in the input of the first token of the event. For
	// a Text or Comment event continuing a Partial one, it is that of the
	// token whose text was split. For the EndElement event of an element
	// whose start tag doesn't open one (a void or self-closed element, see
	// [htmlstream.Token.Opens]), it is that of the start tag. For other
	// implied StartElement and EndElement events, it is that of the token
	// before which it is delivered, or the end of the input. For the Warning
	// event of a CDATA section reaching the end of the input, it is the end
	// of the input.
	Pos htmlstream.Pos

	tok *htmlstream.Token
}

// Token returns the token of the event: the start tag of a StartElement
// event, and of the EndElement event of a void or self-closed element; the
// end tag (possibly implied) of other EndElement events, including the head
// end tag of an implied head element, which is stray for the
// htmlstream.Reader; the token of a ProcInst, Doctype or Untracked event;
// and the dropped or unsound token of a Warning or SyntaxError event. It
// returns nil for the StartElement and EndElement events of implied html,
// head and body elements and the EndElement events of html and body elements
// kept open after their end tags (see [Event.Implied]), for Text and Comment
// events, which can have several tokens, and for the Warning event of a
// CDATA section reaching the end of the input (see Warnings and strict mode
// in the package documentation). It is valid as long as the event.
func (e *Event) Token() *htmlstream.Token { return e.tok }

// Attr returns the decoded value of the first attribute of a StartElement
// event named name (see [htmlstream.Token.Attr]). It allocates the value: to
// avoid that, use the attribute of the token (see [Event.Token] and
// [htmlstream.Attr.AppendValue]).
func (e *Event) Attr(name string) (string, bool) {
	if e.Kind != StartElement || e.tok == nil {
		return "", false
	}
	a, ok := e.tok.Attr(name)
	if !ok {
		return "", false
	}
	return a.Value(), true
}

// AttrNS returns the decoded value of the first attribute of a StartElement
// event with the local name local in the namespace ns (see
// [htmlstream.Token.AttrNS]). It allocates the value, like [Event.Attr].
func (e *Event) AttrNS(ns, local string) (string, bool) {
	if e.Kind != StartElement || e.tok == nil {
		return "", false
	}
	a, ok := e.tok.AttrNS(ns, local)
	if !ok {
		return "", false
	}
	return a.Value(), true
}

// Blank reports whether the event is a Text event with only ASCII
// whitespace (see [ascii.IsBlank]).
func (e *Event) Blank() bool { return e.Kind == Text && ascii.IsBlank(e.Text) }

// NS returns the namespace URI of the element of a StartElement or
// EndElement event, or an empty string for other events.
func (e *Event) NS() string {
	if e.Kind != StartElement && e.Kind != EndElement {
		return ""
	}
	return e.Name.Space
}

// AppendLocalName appends the local name of the element of a StartElement or
// EndElement event to dst.
func (e *Event) AppendLocalName(dst []byte) []byte {
	if e.Kind != StartElement && e.Kind != EndElement {
		return dst
	}
	return append(dst, e.Name.Local...)
}

// Reader reads events from an [htmlstream.Reader]. Like the
// htmlstream.Reader, it isn't safe for concurrent use, and reads a single
// input.
type Reader struct {
	r        *htmlstream.Reader
	ev       Event
	err      error
	strict   bool
	fragment bool
	keep     bool // html and body elements stay open after their end tags (HTML rules, not a fragment)
	maxText  int
	seq      uint64 // the number of calls to Next, for Walk
	textRead uint64 // seq when ReadText read the Text event and those continuing it, for Walk

	path      []Name
	starts    []htmlstream.Pos // the positions of the start tags of the elements in path, or -1 for implied ones
	head      int              // the index of the head element in path plus one, or 0
	dropped   []htmlstream.Pos // the positions of the start tags of the open misplaced elements dropped
	untracked int              // the open elements in untracked content

	doc     *htmlutil.Document     // tracks the html, head and body elements, in HTML and Universal mode
	docEv   htmlutil.DocumentEvent // the changes before tok, not delivered yet
	tokEv   htmlutil.DocumentEvent // the changes before tok, as reported for it
	headEnd bool                   // the head element ends once the elements in it are closed
	body    bool                   // the body element starts once the head is closed

	tok     *htmlstream.Token // the current token of r, not delivered yet
	end     htmlstream.Pos    // the end of the last token read
	started bool
	eof     bool
	done    bool
	voidEnd bool // the next event is the end of the element of the current one
	dropLF  bool // a newline at the start of the next token is dropped, if it is a Text token

	begun       bool   // a token ended the initial insertion mode of HTML parsers (see htmlutil.Document.Started)
	lateDoctype bool   // tok is a doctype HTML parsers ignore
	scratch     []byte // the decoded text of a token before begun is set

	cdataEOF bool // the next event is a Warning for a CDATA section reaching the end of the input, at eofPos
	eofPos   htmlstream.Pos

	buf      []byte // the text of the event, followed by that of the next one (see cont)
	split    int    // the end of the text of the event in buf, if cont is set
	cont     Kind   // the kind of the next event, which continues the text of this one
	contMore bool   // the token of the next event continues
	contPos  htmlstream.Pos

	names map[string]string
}

// NewReader returns a Reader reading the tokens of r, starting with its
// current token if it has read some already (see the package documentation).
func NewReader(r *htmlstream.Reader) *Reader {
	return &Reader{r: r, maxText: DefaultMaxText}
}

// SetMaxText sets the maximum size of the text of a Text or Comment event
// (see Limits in the package documentation). Longer text is delivered in
// several events (see [Event.Partial]), split between UTF-8 sequences,
// except for a character longer than the maximum size, which is delivered
// whole. It may be called at any time. The default is
// [DefaultMaxText], and values below 1 are treated as 1.
func (s *Reader) SetMaxText(n int) { s.maxText = max(n, 1) }

// SetFragment sets whether the input is read as the contents of a body
// element, like HTML parsers parse a fragment with a body context element
// (see the [fragment parsing algorithm]). Under HTML rules (see
// [htmlstream.Token.HTMLRules]), the implied html, head and body elements
// aren't synthesized then, and html, head and body start tags are dropped,
// since HTML parsers ignore them in a body (see Warnings and strict mode in
// the package documentation). It has no effect under XML rules. It must be
// called before the first call to Next, and panics otherwise. The default is
// false.
//
// [fragment parsing algorithm]: https://html.spec.whatwg.org/multipage/parsing.html#html-fragment-parsing-algorithm
func (s *Reader) SetFragment(v bool) {
	if s.started {
		panic("sax: SetFragment called after Next")
	}
	s.fragment = v
}

// SetStrict sets whether dropped and structurally unsound input is a
// [SyntaxError] event, which stops the Reader (see the package
// documentation). The default is false. It may be called at any time, and
// applies to the events after the current one. It is independent of
// [htmlstream.Reader.SetStrict], which doesn't change the events.
func (s *Reader) SetStrict(v bool) { s.strict = v }

// Err returns the first error encountered: an [*htmlstream.SyntaxError] in
// strict mode, or an error of the htmlstream.Reader. It is nil at the end of
// the input.
func (s *Reader) Err() error { return s.err }

// Event returns the current event. It is valid until the next call to Next,
// and its Kind is zero before the first call to Next and after Next returns
// false.
func (s *Reader) Event() *Event { return &s.ev }

// All returns an iterator over the remaining events. Each event is valid
// until the next iteration. Check Err after iterating.
func (s *Reader) All() iter.Seq[*Event] {
	return func(yield func(*Event) bool) {
		for s.Next() {
			if !yield(&s.ev) {
				return
			}
		}
	}
}

// Path returns the names of the open elements, outermost first, including
// the element of the current StartElement or EndElement event. The elements
// of Untracked events aren't included. It is valid until the next call to
// Next, and must not be modified.
func (s *Reader) Path() []Name { return s.path }

// Depth returns the number of open elements, len(Path()). Unlike
// [htmlstream.Reader.Depth], it includes the implied html, head and body
// elements and doesn't include untracked elements.
func (s *Reader) Depth() int { return len(s.path) }

// Next advances to the next event. It returns false at the end of the input
// or on error.
func (s *Reader) Next() bool {
	if !s.started {
		s.start()
	}
	if s.err != nil || s.done {
		s.ev = Event{}
		return false
	}
	s.seq++
	prev := s.ev
	s.ev = Event{}
	if prev.Kind == EndElement {
		s.pop()
	}
	if s.voidEnd {
		s.voidEnd = false
		s.ev = Event{Kind: EndElement, Name: prev.Name, Implied: true, Pos: prev.Pos, tok: prev.tok}
		return true
	}
	if k := s.cont; k != 0 {
		s.cont = 0
		s.buf = s.buf[:copy(s.buf, s.buf[s.split:])]
		if s.collect(k, s.contPos, s.contMore) {
			return true
		}
	}
	if s.cdataEOF {
		s.cdataEOF = false
		s.buf = append(s.buf[:0], cdataEOFWarning...)
		s.ev = Event{Kind: Warning, Text: s.text(), Pos: s.eofPos}
		return true
	}
	for {
		if s.tok == nil && !s.read() {
			return s.finish()
		}
		if s.strict && s.tok.Unsound() {
			return s.unsound(s.tok.UnsoundError())
		}
		if s.structure() || s.token() {
			return true
		}
	}
}

// start sets up the Reader before the first event.
func (s *Reader) start() {
	s.started = true
	s.keep = s.r.Mode() != htmlstream.XML && !s.fragment
	t := s.r.Token()
	if t == nil {
		if s.keep {
			// Document implies no elements in documents read under XML rules
			// in Universal mode.
			s.doc = new(htmlutil.Document)
		}
		return
	}
	// start in the middle of the input, at the current token
	opens := t.Kind() == htmlstream.StartTag && t.Opens()
	skip := opens && !t.Untracked() // the element of t is pushed by t
	for e := range s.r.Stack() {
		if skip {
			skip = false
			continue
		}
		var buf [maxNameLen]byte
		ns := e.NS()
		b := appendPrefix(buf[:0], ns, e.Prefix())
		s.path = append(s.path, s.name(ns, e.HTMLRules(), e.AppendLocalName(b)))
		s.starts = append(s.starts, e.Pos())
	}
	for i, j := 0, len(s.path)-1; i < j; i, j = i+1, j-1 {
		s.path[i], s.path[j] = s.path[j], s.path[i]
		s.starts[i], s.starts[j] = s.starts[j], s.starts[i]
	}
	depth := s.r.Depth()
	if opens {
		depth--
	}
	s.untracked = max(depth-len(s.path), 0)
	if t.Kind() != htmlstream.Comment || t.Continued() || string(t.Raw()) != "</>" {
		s.tok = t // a </> gives no event, as in read
		s.initial(t)
	}
}

// read reads the next token into tok.
func (s *Reader) read() bool {
	if s.eof {
		return false
	}
	var t *htmlstream.Token
	for {
		if !s.r.Next() {
			s.eof = true
			return false
		}
		t = s.r.Token()
		s.end = max(s.end, t.Pos()+htmlstream.Pos(len(t.Raw())))
		if t.Kind() != htmlstream.Comment || t.Continued() || string(t.Raw()) != "</>" {
			break
		}
		// HTML parsers ignore it (see htmlstream.Token.Bogus)
	}
	s.tok = t
	s.initial(t)
	if s.doc != nil {
		s.tokEv = s.doc.Update(t)
		s.docEv = s.tokEv
	}
	return true
}

// initial sets lateDoctype for t, the next token: whether it is a doctype
// HTML parsers ignore, after the [initial insertion mode] or in a fragment,
// under HTML rules or before the rules are chosen in Universal mode.
//
// [initial insertion mode]: https://html.spec.whatwg.org/multipage/parsing.html#the-initial-insertion-mode
func (s *Reader) initial(t *htmlstream.Token) {
	s.lateDoctype = t.Kind() == htmlstream.Doctype && (s.begun || s.fragment) && (t.HTMLRules() || s.r.EffectiveMode() == htmlstream.Universal)
	if s.begun {
		return
	}
	switch t.Kind() {
	case htmlstream.Comment, htmlstream.ProcInst:
	case htmlstream.Text, htmlstream.CDATA:
		s.scratch = t.AppendText(s.scratch[:0])
		b := s.scratch
		if t.Pos() == 0 {
			b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})
		}
		s.begun = !ascii.IsBlank(b)
	default:
		s.begun = true
	}
}

// finish delivers the events at the end of the input.
func (s *Reader) finish() bool {
	if err := s.r.Err(); err != nil {
		s.err = err
		return false
	}
	if s.doc != nil {
		s.docEv |= s.doc.End()
		if s.structure() {
			return true
		}
	}
	if n := len(s.path); n > 0 {
		s.ev = Event{Kind: EndElement, Name: s.path[n-1], Implied: true, Pos: s.end}
		return true
	}
	s.done = true
	return false
}

// structure delivers the implied html, head and body events before tok, and
// closes the implied elements in the element closed by tok.
func (s *Reader) structure() bool {
	if s.doc == nil {
		return false
	}
	if s.docEv&htmlutil.ImpliedHTML != 0 {
		s.docEv &^= htmlutil.ImpliedHTML
		return s.implied(nameHTML)
	}
	if s.docEv&htmlutil.ImpliedHead != 0 {
		s.docEv &^= htmlutil.ImpliedHead
		return s.implied(nameHead)
	}
	s.headEnd = s.headEnd || s.docEv&htmlutil.HeadEnd != 0
	s.body = s.body || s.docEv&htmlutil.ImpliedBody != 0
	s.docEv &= htmlutil.MisplacedTag
	n := len(s.path)
	if s.headEnd {
		// an implied head is closed here, and one opened by a start tag by
		// its end tag (explicit or implied)
		switch {
		case s.head == 0:
			s.headEnd = false
		case s.head == n && s.starts[n-1] < 0:
			s.headEnd = false
			s.ev = Event{Kind: EndElement, Name: nameHead, Implied: true, Pos: s.pos()}
			if t := s.tok; t != nil && t.Kind() == htmlstream.EndTag && t.Stray() && t.Is("head") && s.tokEv&htmlutil.HeadEnd != 0 {
				// the end tag of the implied head, stray for the
				// htmlstream.Reader
				s.tok, s.docEv, s.dropLF = nil, 0, false
				s.ev.Implied, s.ev.tok = false, t
			}
			return true
		}
	}
	if s.body && !s.headEnd && n > 0 && s.path[n-1] == nameHTML {
		s.body = false
		return s.implied(nameBody)
	}
	if t := s.tok; t != nil && n > 0 && s.starts[n-1] < 0 && s.untracked == 0 && t.Kind() == htmlstream.EndTag && !t.Stray() && !s.isDropped(t) && !s.keptEnd(t) {
		s.ev = Event{Kind: EndElement, Name: s.path[n-1], Implied: true, Pos: t.Pos()}
		return true
	}
	return false
}

// implied delivers the start of an implied element.
func (s *Reader) implied(name Name) bool {
	s.push(name, -1)
	s.ev = Event{Kind: StartElement, Name: name, Implied: true, Pos: s.pos()}
	return true
}

// pos returns the position of tok, or the end of the input.
func (s *Reader) pos() htmlstream.Pos {
	if s.tok != nil {
		return s.tok.Pos()
	}
	return s.end
}

func (s *Reader) push(name Name, start htmlstream.Pos) {
	s.path = append(s.path, name)
	s.starts = append(s.starts, start)
	if s.doc != nil && name == nameHead {
		s.head = len(s.path)
	}
}

func (s *Reader) pop() {
	n := len(s.path) - 1
	if s.head == n+1 {
		s.head = 0
	}
	s.path, s.starts = s.path[:n], s.starts[:n]
}

// isDropped reports whether the end tag t closes a dropped element.
func (s *Reader) isDropped(t *htmlstream.Token) bool {
	n := len(s.dropped)
	return n > 0 && s.dropped[n-1] == t.StartPos()
}

// keptEnd reports whether the end tag t is a body or html end tag which
// HTML parsers don't ignore, under HTML rules, except for fragments: the body and
// html elements are then kept open until the end of the input. HTML parsers
// ignore one if the body element isn't in scope (see the in body insertion
// mode); the htmlstream.Reader closes an element opened by a start tag with
// one, and reports it as stray otherwise.
func (s *Reader) keptEnd(t *htmlstream.Token) bool {
	if !s.keep || t.Kind() != htmlstream.EndTag || !t.HTMLRules() || !t.Is("body") && !t.Is("html") {
		return false
	}
	if !t.Stray() {
		return t.NS() == htmlstream.NamespaceXHTML && !s.isDropped(t)
	}
	if s.tokEv&htmlutil.ImpliedBody != 0 {
		return true // the body starts before it
	}
	for i := len(s.path) - 1; i >= 0; i-- {
		switch n := s.path[i]; {
		case n == nameBody:
			return true
		case scopeBoundary(n):
			return false
		}
	}
	return false
}

// scopeBoundary reports whether HTML parsers stop at an element named n
// when they look for an element in scope (see [has an element in scope]).
//
// [has an element in scope]: https://html.spec.whatwg.org/multipage/parsing.html#has-an-element-in-scope
func scopeBoundary(n Name) bool {
	switch n.Space {
	case htmlstream.NamespaceXHTML:
		switch n.Local {
		case "applet", "caption", "html", "table", "td", "th", "marquee", "object", "template":
			return true
		}
	case htmlstream.NamespaceMathML:
		switch n.Local {
		case "mi", "mo", "mn", "ms", "mtext", "annotation-xml":
			return true
		}
	case htmlstream.NamespaceSVG:
		switch n.Local {
		case "foreignObject", "desc", "title":
			return true
		}
	}
	return false
}

// token delivers the event of tok, if any, and consumes it.
func (s *Reader) token() bool {
	t := s.tok
	misplaced := s.docEv&htmlutil.MisplacedTag != 0
	s.docEv = 0
	dropLF := s.dropLF
	s.dropLF = false
	if s.untracked > 0 || t.Kind() == htmlstream.StartTag && t.Untracked() {
		return s.untrackedToken(t)
	}
	s.tok = nil
	switch t.Kind() {
	case htmlstream.Text, htmlstream.CDATA:
		s.buf = s.buf[:0]
		s.appendText(t, dropLF)
		return s.collect(Text, t.Pos(), true)
	case htmlstream.Comment:
		s.buf = t.AppendText(s.buf[:0])
		return s.collect(Comment, t.Pos(), t.Partial())
	case htmlstream.StartTag:
		return s.startTag(t, misplaced)
	case htmlstream.EndTag:
		return s.endTag(t)
	case htmlstream.ProcInst:
		s.buf = t.AppendText(s.buf[:0])
		s.ev = Event{Kind: ProcInst, Name: Name{Local: s.intern(t.Name())}, Text: s.text(), Pos: t.Pos(), tok: t}
		return true
	case htmlstream.Doctype:
		if s.lateDoctype {
			return s.warn(t, "dropped misplaced doctype", "", "")
		}
		s.ev = Event{Kind: Doctype, Text: doctype(t), Pos: t.Pos(), tok: t}
		return true
	}
	return false
}

func (s *Reader) startTag(t *htmlstream.Token, misplaced bool) bool {
	var buf [maxNameLen]byte
	ns := t.NS()
	b := appendPrefix(buf[:0], ns, t.Prefix())
	name := s.name(ns, t.HTMLRules(), t.AppendLocalName(b))
	html := name.Space == htmlstream.NamespaceXHTML && t.HTMLRules()
	if t.Incomplete() && t.HTMLRules() {
		// HTML parsers drop a tag cut off by the end of the input (it opens
		// no element)
		return s.warn(t, "dropped incomplete <", name.Local, ">")
	}
	if html && (name.Local == "html" || name.Local == "head" || name.Local == "body") {
		ignored := !t.Opens() && !t.SelfClosing()
		misplaced = misplaced || s.fragment
		if ignored || misplaced {
			if t.Opens() {
				s.dropped = append(s.dropped, t.Pos())
			}
			if ignored && !misplaced && s.doc != nil && name.Local == "body" {
				// the body starts here for HTML parsers (e.g., in a
				// noscript element in an implied head, with scripting
				// disabled)
				s.body = true
			}
			return s.warn(t, "dropped misplaced ", name.Local, " start tag")
		}
	}
	s.push(name, t.Pos())
	s.ev = Event{Kind: StartElement, Name: name, Pos: t.Pos(), tok: t}
	if !t.Opens() {
		s.voidEnd = true
	} else if html && (name.Local == "pre" || name.Local == "listing" || name.Local == "textarea") {
		s.dropLF = true
	}
	return true
}

func (s *Reader) endTag(t *htmlstream.Token) bool {
	if s.isDropped(t) {
		s.dropped = s.dropped[:len(s.dropped)-1]
		return false
	}
	if s.keptEnd(t) {
		if !t.Stray() {
			// the htmlstream.Reader closes the element, but the Reader
			// keeps it open
			for i, pos := range s.starts {
				if pos == t.StartPos() {
					s.starts[i] = -1
				}
			}
		}
		return false
	}
	if t.Stray() {
		name := string(t.Name())
		if t.HTMLRules() {
			name = ascii.ToLower(name)
		}
		if t.Incomplete() {
			return s.warn(t, "dropped incomplete </", name, ">")
		}
		return s.warn(t, "dropped stray </", name, ">")
	}
	n := len(s.path)
	if n == 0 {
		return false
	}
	s.ev = Event{Kind: EndElement, Name: s.path[n-1], Implied: t.Implied(), Pos: t.Pos(), tok: t}
	return true
}

// untrackedToken delivers a token of untracked content.
func (s *Reader) untrackedToken(t *htmlstream.Token) bool {
	s.tok = nil
	switch t.Kind() {
	case htmlstream.StartTag:
		if t.Opens() {
			s.untracked++
		}
	case htmlstream.EndTag:
		if n := len(s.starts); n > 0 && t.StartPos() >= 0 && s.starts[n-1] == t.StartPos() {
			// a tracked element in untracked content, open when the Reader
			// started
			return s.endTag(t)
		}
		if !t.Stray() && s.untracked > 0 {
			s.untracked--
		}
	}
	s.ev = Event{Kind: Untracked, Partial: t.Partial(), Pos: t.Pos(), tok: t}
	switch t.Kind() {
	case htmlstream.Text, htmlstream.CDATA, htmlstream.Comment, htmlstream.ProcInst:
		s.buf = t.AppendText(s.buf[:0])
		s.ev.Text = s.text()
	case htmlstream.Doctype:
		s.ev.Text = doctype(t)
	}
	return true
}

// appendText appends the decoded text of a Text or CDATA token to buf.
func (s *Reader) appendText(t *htmlstream.Token, dropLF bool) {
	if t.Kind() == htmlstream.CDATA {
		s.cdataEOF = t.Incomplete() && t.HTMLRules() && s.htmlContent()
		s.eofPos = t.Pos() + htmlstream.Pos(len(t.Raw()))
	}
	n := len(s.buf)
	s.buf = t.AppendText(s.buf)
	b := s.buf[n:]
	switch {
	case dropLF && t.Kind() == htmlstream.Text && len(b) != 0 && b[0] == '\n':
		s.buf = append(s.buf[:n], b[1:]...)
	case t.Pos() == 0 && len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF:
		s.buf = append(s.buf[:n], b[3:]...)
	}
}

// cdataEOFWarning is the text of the Warning event for a CDATA section in
// HTML content reaching the end of the input.
const cdataEOFWarning = "read the rest of the input as a CDATA section (HTML parsers would have ended it at the first >)"

// htmlContent reports whether the current token of r is in HTML content:
// in an HTML element following HTML rules, or outside the elements.
func (s *Reader) htmlContent() bool {
	for e := range s.r.Stack() {
		return e.HTMLRules() && e.NS() == htmlstream.NamespaceXHTML
	}
	return true
}

// collect delivers a Text or Comment event with the text in buf, followed by
// that of the tokens continuing it, up to the maximum text size. For a
// Comment, more reports whether the last token continues.
func (s *Reader) collect(k Kind, pos htmlstream.Pos, more bool) bool {
	last := pos
	for {
		if len(s.buf) > s.maxText {
			if cut := s.cut(); cut < len(s.buf) {
				s.split, s.cont, s.contMore, s.contPos = cut, k, more, last
				s.ev = Event{Kind: k, Text: s.buf[:cut:cut], Partial: true, Pos: pos}
				return true
			}
		}
		if !more || !s.continues(k) {
			break
		}
		t := s.tok
		s.tok = nil
		last = t.Pos()
		if k == Comment {
			s.buf = t.AppendText(s.buf)
			more = t.Partial()
		} else {
			s.appendText(t, false)
		}
	}
	if k == Text && len(s.buf) == 0 {
		return false
	}
	s.ev = Event{Kind: k, Text: s.text(), Pos: pos}
	return true
}

// continues reports whether the next token continues the text of an event
// of kind k, reading it into tok.
func (s *Reader) continues(k Kind) bool {
	if s.tok == nil && !s.read() {
		return false
	}
	t := s.tok
	if s.strict && t.Unsound() || s.docEv != 0 {
		return false
	}
	if k == Comment {
		return t.Kind() == htmlstream.Comment && t.Continued()
	}
	return t.Kind() == htmlstream.Text || t.Kind() == htmlstream.CDATA
}

// cut returns where to split the text in buf, which is longer than the
// maximum text size: at most at that size, between UTF-8 sequences.
func (s *Reader) cut() int {
	n := s.maxText
	for i := n; i > 0 && i > n-utf8.UTFMax; i-- {
		if utf8.RuneStart(s.buf[i]) {
			return i
		}
	}
	if n < utf8.UTFMax {
		_, size := utf8.DecodeRune(s.buf)
		return size
	}
	return n // not UTF-8
}

// text returns buf, with its capacity limited so that appending to it
// doesn't change the buffer.
func (s *Reader) text() []byte { return s.buf[:len(s.buf):len(s.buf)] }

// warn delivers a Warning event for the dropped token t, or a SyntaxError
// event in strict mode.
func (s *Reader) warn(t *htmlstream.Token, msg, name, suffix string) bool {
	s.buf = append(append(append(s.buf[:0], msg...), name...), suffix...)
	if s.strict {
		s.err = &htmlstream.SyntaxError{Pos: t.Pos(), Msg: string(s.buf)}
		s.ev = Event{Kind: SyntaxError, Text: s.text(), Pos: t.Pos(), tok: t}
		return true
	}
	s.ev = Event{Kind: Warning, Text: s.text(), Pos: t.Pos(), tok: t}
	return true
}

// unsound delivers a SyntaxError event for tok.
func (s *Reader) unsound(err error) bool {
	t := s.tok
	s.tok, s.err = nil, err
	s.buf = s.buf[:0]
	pos := t.Pos()
	var se *htmlstream.SyntaxError
	if errors.As(err, &se) {
		s.buf, pos = append(s.buf, se.Msg...), se.Pos
	}
	s.ev = Event{Kind: SyntaxError, Text: s.text(), Pos: pos, tok: t}
	return true
}

// name returns the name of an element, interned.
func (s *Reader) name(ns string, htmlRules bool, local []byte) Name {
	if htmlRules && ns == htmlstream.NamespaceSVG {
		return Name{ns, htmlstream.AdjustSVGTagName(s.intern(local))}
	}
	return Name{ns, s.intern(local)}
}

// appendPrefix appends the prefix of an element name and a colon to dst if
// it isn't bound to a namespace (ns is empty), so that the name is the
// qualified name. Under HTML rules, names have no prefix.
func appendPrefix(dst []byte, ns string, prefix []byte) []byte {
	if prefix == nil || ns != "" {
		return dst
	}
	return append(append(dst, prefix...), ':')
}

// intern returns b as a string, from the names seen before if possible.
func (s *Reader) intern(b []byte) string {
	if v, ok := s.names[string(b)]; ok {
		return v
	}
	v := string(b)
	if len(b) <= maxNameLen && len(s.names) < maxNames {
		if s.names == nil {
			s.names = make(map[string]string)
		}
		s.names[v] = v
	}
	return v
}

// doctype returns the contents of a doctype after the whitespace following
// "DOCTYPE".
func doctype(t *htmlstream.Token) []byte {
	b := t.Data()
	for len(b) != 0 && ascii.IsSpace(b[0]) {
		b = b[1:]
	}
	return b[:len(b):len(b)]
}

// Skip advances to the EndElement event of the element of the current
// StartElement event, skipping the events in it, including Warning events.
// At an Untracked event of a start tag, it likewise advances to the
// Untracked event of the end tag of its element. It stops at a SyntaxError
// event in strict mode, returning the error. Otherwise, it returns Err. At
// other events, Skip does nothing.
func (s *Reader) Skip() error {
	switch s.ev.Kind {
	case StartElement:
		return s.skipTo(len(s.path))
	case Untracked:
		if t := s.ev.tok; t.Kind() == htmlstream.StartTag && t.Opens() {
			return s.skipUntracked()
		}
	}
	return nil
}

// skipTo advances to the EndElement event of the element at depth in the
// path, or to a SyntaxError event.
func (s *Reader) skipTo(depth int) error {
	for s.Next() {
		switch {
		case s.ev.Kind == EndElement && len(s.path) == depth:
			return nil
		case s.ev.Kind == SyntaxError:
			return s.err
		}
	}
	return s.err
}

// skipUntracked advances from the Untracked event of a start tag to the
// Untracked event of the end tag of its element, or to a SyntaxError event.
func (s *Reader) skipUntracked() error {
	level := s.untracked - 1
	for s.Next() {
		switch ev := &s.ev; ev.Kind {
		case Untracked:
			if t := ev.tok; t.Kind() == htmlstream.EndTag && !t.Stray() && s.untracked == level {
				return nil
			}
		case SyntaxError:
			return s.err
		}
	}
	return s.err
}

// ReadText appends the text of the element of the current StartElement
// event to dst and returns the result: that of the Text events in it, and
// of the Text and CDATA tokens of the Untracked events in it, like the DOM's
// [textContent]. The contents of template elements in the XHTML namespace
// are left out, since parsers put them in a separate document fragment (see
// [template contents] and [parsing XML documents]), like
// [htmlutil.TextContent] does. Afterwards, the current event is the
// element's EndElement event, or a SyntaxError event in it in strict mode.
// At a Text event, it appends its text and that of the events continuing it
// (see [Event.Partial]), and the current event is the last of them, or a
// SyntaxError event after them in strict mode. At other events, it does
// nothing.
//
// If limit is positive, at most limit bytes are appended, without splitting
// a UTF-8 sequence; the rest of the text is skipped and truncated is true.
// Check Err afterwards.
//
// [textContent]: https://dom.spec.whatwg.org/#dom-node-textcontent
// [template contents]: https://html.spec.whatwg.org/multipage/scripting.html#template-contents
// [parsing XML documents]: https://html.spec.whatwg.org/multipage/xhtml.html#parsing-xhtml-documents
func (s *Reader) ReadText(dst []byte, limit int) (b []byte, truncated bool) {
	start := len(dst)
	add := func(text []byte) {
		if truncated {
			return
		}
		if n := limit - (len(dst) - start); limit > 0 && len(text) > n {
			// at the start of the UTF-8 sequence at n
			for i := n; i >= 0 && i > n-utf8.UTFMax; i-- {
				if utf8.RuneStart(text[i]) {
					n = i
					break
				}
			}
			text, truncated = text[:n], true
		}
		dst = append(dst, text...)
	}
	switch s.ev.Kind {
	case Text:
		add(s.ev.Text)
		for s.ev.Partial && s.Next() && s.ev.Kind == Text {
			add(s.ev.Text)
		}
		if s.ev.Kind == Text {
			s.textRead = s.seq
		}
	case StartElement:
		depth := len(s.path)
		if s.ev.Name == nameTemplate {
			s.skipTo(depth)
			return dst, false
		}
		for s.Next() {
			switch ev := &s.ev; ev.Kind {
			case StartElement:
				if ev.Name == nameTemplate {
					s.skipTo(len(s.path))
				}
			case EndElement:
				if len(s.path) == depth {
					return dst, truncated
				}
			case Text:
				add(ev.Text)
			case Untracked:
				switch t := ev.tok; t.Kind() {
				case htmlstream.Text, htmlstream.CDATA:
					add(ev.Text)
				case htmlstream.StartTag:
					if t.Opens() && t.NS() == htmlstream.NamespaceXHTML && t.Is("template") {
						s.skipUntracked()
					}
				}
			}
			if s.ev.Kind == SyntaxError {
				break
			}
		}
	}
	return dst, truncated
}

// SkipElement is used as a return value from the functions of a [Handler]
// to indicate that the element of the event is to be skipped (see
// [Reader.Walk]). It is not returned as an error by any function.
var SkipElement = errors.New("skip this element")

// Handler holds the functions [Reader.Walk] calls for the events of each
// kind. A nil function ignores the events.
type Handler struct {
	StartElement func(r *Reader, ev *Event) error
	EndElement   func(r *Reader, ev *Event) error
	Text         func(r *Reader, ev *Event) error
	Comment      func(r *Reader, ev *Event) error
	ProcInst     func(r *Reader, ev *Event) error
	Doctype      func(r *Reader, ev *Event) error
	Warning      func(r *Reader, ev *Event) error
	SyntaxError  func(r *Reader, ev *Event) error
	Untracked    func(r *Reader, ev *Event) error
}

// Walk reads the remaining events, calling the function of h for the kind
// of each event, until the end of the input, an error, or a function
// returning an error. It returns the error returned by a function, or else
// Err.
//
// If a function returns [SkipElement], Walk skips the element of the event:
// the element of a StartElement event, and the element containing other
// events (for an EndElement event, the parent of its element). The events
// in it are skipped, including Warning events, and EndElement is called next
// for the element. At the top level, Walk returns nil instead.
//
// A function may advance the Reader itself (e.g., with [Reader.Skip] or
// [Reader.ReadText]). Walk then continues with the current event, without
// calling Next (so EndElement is called for an element after StartElement
// read its contents), and a SkipElement returned by the function has no
// further effect. Reading the text of a Text event with ReadText doesn't
// count as advancing: Walk continues after the last event read.
func (s *Reader) Walk(h Handler) error {
	if !s.Next() {
		return s.err
	}
	for {
		var fn func(*Reader, *Event) error
		switch s.ev.Kind {
		case StartElement:
			fn = h.StartElement
		case EndElement:
			fn = h.EndElement
		case Text:
			fn = h.Text
		case Comment:
			fn = h.Comment
		case ProcInst:
			fn = h.ProcInst
		case Doctype:
			fn = h.Doctype
		case Warning:
			fn = h.Warning
		case SyntaxError:
			fn = h.SyntaxError
		case Untracked:
			fn = h.Untracked
		default:
			return s.err // at the end
		}
		seq, kind, depth := s.seq, s.ev.Kind, len(s.path)
		var err error
		if fn != nil {
			err = fn(s, &s.ev)
		}
		advanced := s.seq != seq && s.textRead != s.seq
		switch {
		case err == SkipElement && !advanced:
			if kind == EndElement {
				depth--
			}
			if depth == 0 {
				return nil
			}
			s.skipTo(depth)
		case err != nil && err != SkipElement:
			return err
		case !advanced:
			if !s.Next() {
				return s.err
			}
		}
	}
}

// TokenReader reads the events of a [Reader] as the tokens of
// [encoding/xml], implementing [encoding/xml.TokenReader], so that
// [encoding/xml.NewTokenDecoder] can decode malformed XML and HTML (e.g.,
// into structs with [encoding/xml.Decoder.Decode]).
//
// Element names are those of the events (see [Name]), with the namespace URI
// in Space. Attribute names also have the namespace URI in Space (see
// [htmlstream.Token.AttrNamespace]), and the local name in Local; for an
// attribute with a prefix not bound to a namespace, Space is empty and
// Local is the qualified name (e.g., x:y), like for elements. Namespace
// declarations are attributes in the [htmlstream.NamespaceXMLNS] namespace,
// named xmlns for the default namespace and after the prefix otherwise, so
// that an encoding/xml.Decoder doesn't resolve the names again (it still
// replaces an empty Space with its DefaultSpace in element names, and the
// Space "xml" with [htmlstream.NamespaceXML]). Under HTML rules, attribute
// names are lowercase, except for the mixed-case SVG and MathML attribute
// names of HTML parsers (see [htmlstream.AdjustSVGAttrName]), and only the
// first of the attributes with the same name as HTML parsers read it is
// kept, like HTML parsers do (e.g., of xlink:href and XLINK:HREF, but not of
// l:href and xlink:href, even with both in the XLink namespace).
// An [encoding/xml.Encoder] writes the namespace declarations as attributes
// in a namespace with a generated prefix (e.g., xmlns:_xmlns="..."
// _xmlns:b="urn:b"), like those read by an encoding/xml.Decoder.
//
// Text events are [encoding/xml.CharData]. With XML syntax, a CDATA section
// in a title, textarea, script or style element is text, markers included,
// unlike in an encoding/xml.Decoder reading the input (see the package
// documentation).
//
// Warning events are left out: those for the dropped tags and doctypes, and
// for a CDATA section reaching the end of the input (see Warnings and strict
// mode in the package documentation). Some of them mark differences from HTML
// parsers: a stray </p> or </br>, which HTML parsers read as a p or br
// element, a dropped html or body start tag with attributes, which HTML
// parsers add to the existing element, and a CDATA section read with XML
// syntax reaching the end of the input, which they end at the first ">". In
// strict mode (see [Reader.SetStrict]), they are errors. The text, comments
// and processing instructions of Untracked events are kept, but their tags
// can't be delivered as elements, so Token returns [ErrUntracked] at the
// first one. A doctype is an [encoding/xml.Directive] starting with "DOCTYPE
// ".
type TokenReader struct {
	r     *Reader
	err   error
	attrs []xml.Attr
	seen  internal.Map[string, struct{}] // the attribute names under HTML rules (see htmlstream.Attr.AppendName), for tags with more than 8 attributes
	buf   []byte
}

// ErrUntracked is returned by [TokenReader.Token] at the [Untracked] event
// of a start or end tag, since the tag can't be delivered as an element
// (the Reader's current event is that event).
var ErrUntracked = errors.New("sax: can't decode element nested past the stack limits (MaxDepth or MaxStackBytes)")

// NewTokenReader returns a TokenReader reading the events of r.
func NewTokenReader(r *Reader) *TokenReader {
	return &TokenReader{r: r}
}

// Token returns the next token, or [io.EOF] at the end of the input. The
// token data is valid until the next call to Token. After an error
// (including io.EOF), it returns the error again: the
// [*htmlstream.SyntaxError] of a SyntaxError event, ErrUntracked, or an error
// of the Reader.
func (tr *TokenReader) Token() (xml.Token, error) {
	if tr.err != nil {
		return nil, tr.err
	}
	r := tr.r
	for r.Next() {
		ev := &r.ev
		switch ev.Kind {
		case StartElement:
			return xml.StartElement{Name: xml.Name(ev.Name), Attr: tr.appendAttrs(ev)}, nil
		case EndElement:
			return xml.EndElement{Name: xml.Name(ev.Name)}, nil
		case Text:
			return xml.CharData(ev.Text), nil
		case Comment:
			return xml.Comment(ev.Text), nil
		case ProcInst:
			return xml.ProcInst{Target: ev.Name.Local, Inst: ev.Text}, nil
		case Doctype:
			tr.buf = append(append(tr.buf[:0], "DOCTYPE "...), ev.Text...)
			return xml.Directive(tr.buf), nil
		case SyntaxError:
			tr.err = r.err
			return nil, tr.err
		case Untracked:
			switch t := ev.tok; t.Kind() {
			case htmlstream.StartTag, htmlstream.EndTag:
				tr.err = ErrUntracked
				return nil, tr.err
			case htmlstream.Text, htmlstream.CDATA:
				return xml.CharData(ev.Text), nil
			case htmlstream.Comment:
				return xml.Comment(ev.Text), nil
			case htmlstream.ProcInst:
				return xml.ProcInst{Target: r.intern(t.Name()), Inst: ev.Text}, nil
			}
		}
	}
	tr.err = r.err
	if tr.err == nil {
		tr.err = io.EOF
	}
	return nil, tr.err
}

// appendAttrs returns the attributes of a StartElement event.
func (tr *TokenReader) appendAttrs(ev *Event) []xml.Attr {
	t := ev.tok
	if t == nil || t.NumAttrs() == 0 {
		return nil
	}
	tr.attrs = tr.attrs[:0]
	n := t.NumAttrs()
	htmlRules := t.HTMLRules()
	if htmlRules && n > 8 {
		tr.seen.Reset(n)
	}
	for i := range n {
		a := t.AttrAt(i)
		var buf [maxNameLen]byte
		name := a.AppendName(buf[:0])
		if htmlRules && tr.duplicate(t, i, name) {
			continue
		}
		var xn xml.Name
		prefix := a.Prefix()
		switch {
		case prefix == nil && a.Is("xmlns"):
			xn = xml.Name{Space: htmlstream.NamespaceXMLNS, Local: "xmlns"}
		case prefix != nil && (string(prefix) == "xmlns" || htmlRules && ascii.EqualFold(prefix, "xmlns")):
			xn = xml.Name{Space: htmlstream.NamespaceXMLNS, Local: tr.r.intern(local(buf[:0], a, htmlRules))}
		default:
			xn.Space = t.AttrNamespace(a)
			if xn.Space != "" && prefix != nil {
				name = local(buf[:0], a, htmlRules)
			}
			switch {
			case !htmlRules || prefix != nil:
				xn.Local = tr.r.intern(name)
			case ev.Name.Space == htmlstream.NamespaceSVG:
				xn.Local = htmlstream.AdjustSVGAttrName(tr.r.intern(name))
			case ev.Name.Space == htmlstream.NamespaceMathML:
				xn.Local = htmlstream.AdjustMathMLAttrName(tr.r.intern(name))
			default:
				xn.Local = tr.r.intern(name)
			}
		}
		tr.attrs = append(tr.attrs, xml.Attr{Name: xn, Value: a.Value()})
	}
	return tr.attrs
}

// local returns the local name of a prefixed attribute, lowercase under HTML
// rules.
func local(dst []byte, a htmlstream.Attr, htmlRules bool) []byte {
	if htmlRules {
		return ascii.AppendLower(dst, a.Local())
	}
	return a.Local()
}

// duplicate reports whether an attribute before the attribute i of t, which
// follows HTML rules, has the name name (see htmlstream.Attr.AppendName): HTML
// parsers keep the first one, whatever the namespaces of the attributes
// (e.g., they keep both l:href and xlink:href).
func (tr *TokenReader) duplicate(t *htmlstream.Token, i int, name []byte) bool {
	if t.NumAttrs() > 8 {
		key := tr.r.intern(name)
		if _, ok := tr.seen.M[key]; ok {
			return true
		}
		tr.seen.M[key] = struct{}{}
		return false
	}
	for j := range i {
		var buf [maxNameLen]byte
		if bytes.Equal(t.AttrAt(j).AppendName(buf[:0]), name) {
			return true
		}
	}
	return false
}
