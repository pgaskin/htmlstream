package htmlutil

import (
	"bytes"
	"strconv"

	"github.com/pgaskin/htmlstream"
	"github.com/pgaskin/htmlstream/ascii"
	"github.com/pgaskin/htmlstream/internal"
)

// DocumentPart is the part of an HTML document a token is in. The parts are
// named after the [insertion modes] of HTML parsers.
//
// [insertion modes]: https://html.spec.whatwg.org/multipage/parsing.html#the-insertion-mode
type DocumentPart uint8

const (
	BeforeHTML DocumentPart = iota // before the html element
	BeforeHead                     // in the html element, before the head
	InHead                         // in the head element
	AfterHead                      // after the end of the head, before the body
	InBody                         // in the body
)

var documentParts = [...]string{"BeforeHTML", "BeforeHead", "InHead", "AfterHead", "InBody"}

// String returns the name of the constant for p, e.g., "InHead".
func (p DocumentPart) String() string {
	if int(p) < len(documentParts) {
		return documentParts[p]
	}
	return "DocumentPart(" + strconv.Itoa(int(p)) + ")"
}

// DocumentEvent is a set of changes to the document structure before a
// token, as bits.
type DocumentEvent uint8

const (
	ImpliedHTML  DocumentEvent = 1 << iota // the html element starts without a start tag
	ImpliedHead                            // the head element starts without a start tag
	HeadEnd                                // the head element ends (explicitly, implied, or without a start tag)
	ImpliedBody                            // the body element starts without a start tag
	MisplacedTag                           // an html, head or body start tag after the element started, which HTML parsers ignore, adding only the attributes of html and body start tags to the element
)

var documentEvents = [...]string{"ImpliedHTML", "ImpliedHead", "HeadEnd", "ImpliedBody", "MisplacedTag"}

// String returns the names of the constants for the bits set in e, separated
// by "|", e.g., "ImpliedHTML|ImpliedHead", or "0" if none are set.
func (e DocumentEvent) String() string {
	if e == 0 {
		return "0"
	}
	var b []byte
	for i, name := range documentEvents {
		if e&(1<<i) != 0 {
			if len(b) != 0 {
				b = append(b, '|')
			}
			b = append(b, name...)
			e &^= 1 << i
		}
	}
	if e != 0 {
		if len(b) != 0 {
			b = append(b, '|')
		}
		b = append(b, "0x"...)
		b = strconv.AppendUint(b, uint64(e), 16)
	}
	return string(b)
}

// Document tracks which part of an HTML document the tokens are in, like the
// first insertion modes of HTML parsers (initial, before html, before head,
// in head and after head, as the [HTML Standard] defines them): where the
// html, head and body elements start and end, including the ones HTML parsers
// imply. It follows them for documents with a body: it doesn't handle
// frameset documents, and a frameset start tag after the head starts the
// body. With scripting disabled (the Reader's default), it follows the Reader
// for noscript in a head without a start tag, which differs from HTML parsers
// (see [htmlstream.Reader.SetScripting]), and with XML syntax for a
// self-closed head start tag, which ends the head (see
// [htmlstream.Reader.SetXMLSyntax]). Template contents don't change the part,
// and html, head and body start tags in them are reported as [MisplacedTag].
// Like HTML parsers, it ignores a start tag cut off by the end of the input
// (see [htmlstream.Token.Incomplete]).
//
// Documents read under XML rules (see [htmlstream.Token.HTMLRules]: in
// [htmlstream.XML] mode, and documents read like in XML mode in
// [htmlstream.Universal] mode) have no implied elements, as XML parsers don't
// imply any. For them, Part follows the html, head and body elements in the
// input: the first html element at the top level, and the first head and
// body elements at the top level or in it, with the head before the body.
// Their names are compared exactly, and they must be in the XHTML namespace
// (see [htmlstream.Token.NS]), so an html element without a namespace
// declaration is an ordinary element. Other elements, including other html,
// head and body elements, don't change the part. Update reports only
// [HeadEnd], at the end of the head element, and End reports nothing. Once
// a start or end tag is read, Quirks reports false for them, as XML parsers
// never use quirks mode.
//
// The zero value is ready to use, at the start of a document.
//
// [HTML Standard]: https://html.spec.whatwg.org/multipage/parsing.html#the-initial-insertion-mode
type Document struct {
	part     DocumentPart
	head     bool // the head element was opened by a start tag
	nested   int  // open elements in the head (e.g., title), whose text isn't body content; under XML rules, open elements other than the html, head and body elements
	template int  // open elements in a template, including the template
	started  bool // a doctype or other content ended the initial insertion mode
	noQuirks bool // the doctype selects no-quirks (or limited-quirks) mode
	xml      bool // the document is read under XML rules
	buf      []byte
}

// space reports whether a text token is whitespace once decoded, ignoring a
// byte order mark at the start of the input.
func (d *Document) space(tok *htmlstream.Token) bool {
	d.buf = tok.AppendText(d.buf[:0])
	b := d.buf
	if tok.Pos() == 0 {
		b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})
	}
	return ascii.IsBlank(b)
}

// Part returns the part of the document the last token passed to Update is
// in.
func (d *Document) Part() DocumentPart { return d.part }

// Started reports whether the tokens passed to Update so far ended the
// initial insertion mode of HTML parsers: a doctype, or content other than
// whitespace, comments and processing instructions. A doctype after that
// is ignored by HTML parsers.
func (d *Document) Started() bool { return d.started }

// Quirks reports whether HTML parsers parse the document in quirks mode,
// based on the tokens passed to Update so far: the first content isn't a
// doctype, or the doctype sets quirks mode in the initial insertion mode
// (e.g., it is a legacy one, or cut off by the end of the input).
// Limited-quirks mode (e.g., XHTML 1.0 Transitional) is reported as
// no-quirks mode, since it doesn't change parsing. HTML parsers don't use
// quirks mode for iframe srcdoc documents, which Document can't tell
// apart, and XML parsers never use it (see Document).
func (d *Document) Quirks() bool { return !d.noQuirks && !d.xml }

// Update processes the next token, returning the structure changes before it
// (and the head end, if tok is the end of the head). It must be called with
// every token, in order.
func (d *Document) Update(tok *htmlstream.Token) DocumentEvent {
	switch tok.Kind() {
	case htmlstream.StartTag, htmlstream.EndTag:
		if !tok.HTMLRules() {
			d.xml, d.started = true, true
		}
	case htmlstream.Text, htmlstream.CDATA:
		if !tok.HTMLRules() {
			// whitespace before the first start tag in Universal mode, or
			// text under XML rules
			return 0
		}
	}
	if tok.Kind() == htmlstream.StartTag && tok.Incomplete() && tok.HTMLRules() {
		// HTML parsers drop a tag cut off by the end of the input (it opens
		// no element)
		return 0
	}
	if d.xml {
		// only the html, head and body elements in the input change the
		// part
		switch {
		case tok.Kind() == htmlstream.StartTag:
			var buf [16]byte
			var name string
			if d.nested == 0 && tok.NS() == htmlstream.NamespaceXHTML {
				name = string(tok.AppendLocalName(buf[:0]))
			}
			switch {
			case name == "html" && d.part == BeforeHTML:
				d.part = BeforeHead
			case name == "head" && d.part <= BeforeHead:
				d.part = InHead
				if !tok.Opens() {
					d.part = AfterHead
					return HeadEnd
				}
			case name == "body" && d.part != InHead && d.part != InBody:
				d.part = InBody
			case tok.Opens():
				d.nested++
			}
		case tok.Kind() == htmlstream.EndTag && !tok.Stray():
			if d.nested > 0 {
				d.nested--
			} else if d.part == InHead {
				d.part = AfterHead
				return HeadEnd
			}
		}
		return 0
	}
	var ev DocumentEvent
	// stray end tags have no namespace
	html := tok.NS() == htmlstream.NamespaceXHTML || tok.Stray()
	var buf [16]byte
	var name string
	if html {
		name = string(tok.AppendLocalName(buf[:0]))
	}
	if !d.started {
		// the initial insertion mode
		switch tok.Kind() {
		case htmlstream.Doctype:
			d.started = true
			d.noQuirks = !tok.Incomplete() && !internal.QuirksDoctype(tok.Data())
		case htmlstream.Comment, htmlstream.ProcInst:
		case htmlstream.Text, htmlstream.CDATA:
			d.started = !d.space(tok)
		default:
			d.started = true
		}
	}
	if d.template > 0 {
		switch k := tok.Kind(); {
		case k == htmlstream.StartTag:
			if html && (name == "html" || name == "head" || name == "body") {
				return MisplacedTag // ignored by HTML parsers
			}
			if tok.Opens() {
				d.template++
			}
			return 0
		case k == htmlstream.EndTag && !tok.Stray():
			if d.template--; d.template > 0 {
				return 0
			}
			// the end of the template
		default:
			return 0
		}
	}
	switch tok.Kind() {
	case htmlstream.Text, htmlstream.CDATA:
		if d.part < InBody && d.nested == 0 && !d.space(tok) {
			ev |= d.body()
		}
	case htmlstream.StartTag:
		switch {
		case html && name == "html":
			if d.part == BeforeHTML {
				d.part = BeforeHead
			} else {
				ev |= MisplacedTag
			}
		case html && name == "head":
			if d.part > BeforeHead {
				ev |= MisplacedTag
				break
			}
			ev |= d.htmlStart()
			d.part, d.head = InHead, true
			if !tok.Opens() {
				// self-closed with XML syntax
				d.part = AfterHead
				ev |= HeadEnd
			}
		case html && name == "body":
			if d.part == InBody {
				ev |= MisplacedTag
				break
			}
			ev |= d.headStart()
			if d.part == InHead {
				ev |= HeadEnd
			}
			d.part = InBody
		case html && elem(tok)&internal.Head != 0 && d.part < AfterHead:
			ev |= d.headStart()
			if tok.Opens() {
				d.nested++
			}
		case html && elem(tok)&internal.Head != 0 && d.part == AfterHead && name != "noscript":
			// HTML parsers put head content after the head in the head,
			// except noscript
			if tok.Opens() {
				d.nested++
			}
		case d.part < InBody:
			ev |= d.body()
		}
		if html && name == "template" && tok.Opens() {
			d.template = 1
		}
	case htmlstream.EndTag:
		switch {
		case html && name == "head" && d.part <= InHead && (d.head && !tok.Stray() || !d.head && d.nested == 0):
			// HTML parsers also close an implied head (the end tag is stray
			// for the Reader), implying one first if needed, but not from
			// inside head content (e.g., a noscript)
			ev |= d.headStart() | HeadEnd
			d.part = AfterHead
		case html && (name == "br" || name == "body" || name == "html") && d.part < InBody:
			// HTML parsers treat </br> as <br>, and </body> and </html>
			// start the body like other content
			ev |= d.body()
		case d.nested > 0 && !tok.Stray():
			d.nested--
		}
	}
	return ev
}

// End returns the structure changes at the end of the input: HTML parsers
// always create html, head and body elements.
func (d *Document) End() DocumentEvent {
	if d.part == InBody || d.xml {
		return 0
	}
	return d.body()
}

func (d *Document) htmlStart() DocumentEvent {
	if d.part == BeforeHTML {
		d.part = BeforeHead
		return ImpliedHTML
	}
	return 0
}

func (d *Document) headStart() DocumentEvent {
	ev := d.htmlStart()
	if d.part == BeforeHead {
		d.part = InHead
		ev |= ImpliedHead
	}
	return ev
}

func (d *Document) body() DocumentEvent {
	ev := d.headStart()
	if d.part == InHead {
		ev |= HeadEnd
	}
	d.part, d.nested = InBody, 0
	return ev | ImpliedBody
}
