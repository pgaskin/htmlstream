package htmlutil

import (
	"strings"

	"github.com/pgaskin/htmlstream"
	"github.com/pgaskin/htmlstream/ascii"
	"github.com/pgaskin/htmlstream/internal"
)

// NamedElement is an element with a name, which the Is* functions classify:
// a start or end tag ([*htmlstream.Token]), an open element
// ([htmlstream.Element]), or an HTML element name ([HTMLName]). Other tokens
// have no namespace, and aren't in any category. The Is* functions don't
// allocate for these types.
type NamedElement interface {
	NS() string                        // the namespace URI
	AppendLocalName(dst []byte) []byte // the local name, as HTML parsers give it for HTML elements
}

// HTMLName is the name of an HTML element, for classifying elements by name
// (e.g., IsBlock(HTMLName("div"))). Like HTML parsers, it ASCII-lowercases
// the name and replaces NUL with U+FFFD.
type HTMLName string

// NS returns [htmlstream.NamespaceXHTML].
func (n HTMLName) NS() string { return htmlstream.NamespaceXHTML }

// AppendLocalName appends the name, ASCII-lowercased and with NUL replaced
// with U+FFFD, to dst.
func (n HTMLName) AppendLocalName(dst []byte) []byte {
	for i := 0; i < len(n); i++ {
		switch c := n[i]; {
		case c == 0:
			dst = append(dst, "\uFFFD"...)
		case 'A' <= c && c <= 'Z':
			dst = append(dst, c+'a'-'A')
		default:
			dst = append(dst, c)
		}
	}
	return dst
}

// IsVoid reports whether e is an HTML void element, which has no end tag
// and which HTML parsers never open: those which [serialize as void] in the
// HTML Standard (the void elements, e.g., br and img, and basefont, bgsound,
// frame, keygen and param), and image under HTML rules, which HTML parsers
// turn into img.
//
// It classifies e by its name and namespace only: whether a start tag opens
// an element is [htmlstream.Token.Opens]. Under XML rules and with XML
// syntax, a void element can open one: a br start tag in [htmlstream.HTML]
// mode with [htmlstream.Reader.SetXMLSyntax], or in the XHTML namespace in a
// document read like in XML mode in [htmlstream.Universal] mode, is void, but
// unless it is self-closed, it opens an element, closed by its end tag or an
// implied one.
//
// [serialize as void]: https://html.spec.whatwg.org/multipage/parsing.html#serializes-as-void
func IsVoid[E NamedElement](e E) bool { return elem(e)&internal.Void != 0 }

// IsHeadContent reports whether e is an HTML element allowed in the head:
// those the [in head] insertion mode of HTML parsers handles (base,
// basefont, bgsound, link, meta, noframes, noscript, script, style, template
// and title). Other elements, and text other than whitespace, end the head.
// HTML parsers also put these elements in the head when they come after it,
// except noscript.
//
// [in head]: https://html.spec.whatwg.org/multipage/parsing.html#parsing-main-inhead
func IsHeadContent[E NamedElement](e E) bool { return elem(e)&internal.Head != 0 }

// IsBlock reports whether e is a block-level HTML element: text before and
// after it is on separate lines when rendered. These are the elements the
// [rendering section] of the HTML Standard gives a block-level display
// (block, list-item, table or table-caption; e.g., p, li, table and
// caption), and table rows and row groups (tr, tbody, thead and tfoot),
// whose rows are on separate lines. Table cells and inline-block elements
// (e.g., button and select) aren't block-level. Styles set by the document
// aren't taken into account.
//
// [rendering section]: https://html.spec.whatwg.org/multipage/rendering.html#non-replaced-elements
func IsBlock[E NamedElement](e E) bool { return elem(e)&internal.Block != 0 }

// IsTextBreak reports whether e separates words in the text of an
// element: a block-level element (see [IsBlock]), a table cell (td or th),
// or br.
func IsTextBreak[E NamedElement](e E) bool {
	return IsBlock(e) || e.NS() == htmlstream.NamespaceXHTML && (isLocal(e, "br") || isLocal(e, "td") || isLocal(e, "th"))
}

// IsHidden reports whether e is an HTML element whose contents aren't
// rendered as text. These are the elements with contents which the
// [rendering section] of the HTML Standard hides (datalist, head, noembed,
// noframes, rp, script, style, template and title, and noscript when
// scripting is enabled), and those with contents which browsers render as
// [replaced elements] or widgets instead: audio, canvas (when scripting is
// enabled; otherwise it renders its fallback content), iframe, meter,
// progress, textarea (whose contents are the initial value of the form
// control) and video. It includes noscript and canvas whether scripting is
// enabled or not: callers check [htmlstream.Reader.Scripting], as [InnerText]
// does. The void elements it hides (e.g., area and meta) have no contents and
// aren't included. Elements hidden by their attributes (e.g., hidden) aren't
// included either, since end tags have no attributes (see [InnerText], which
// leaves them out). The fallback content of object elements, which browsers
// render when they can't load the object, is not hidden.
//
// To skip the contents, call [htmlstream.Reader.Skip] at the start tag.
//
// [rendering section]: https://html.spec.whatwg.org/multipage/rendering.html#hidden-elements
// [replaced elements]: https://html.spec.whatwg.org/multipage/rendering.html#replaced-elements
func IsHidden[E NamedElement](e E) bool { return elem(e)&internal.Hidden != 0 }

// IsHeading reports whether e is an HTML heading element, one of the
// [h1 to h6 elements]. Heading content in the HTML Standard also
// includes hgroup, which IsHeading doesn't.
//
// [h1 to h6 elements]: https://html.spec.whatwg.org/multipage/sections.html#the-h1,-h2,-h3,-h4,-h5,-and-h6-elements
func IsHeading[E NamedElement](e E) bool { return elem(e)&internal.Heading != 0 }

// IsPreformatted reports whether e is an HTML element whose contents keep
// their whitespace when rendered: those the [rendering section] of the HTML
// Standard gives white-space: pre (pre, listing, xmp and plaintext) or
// pre-wrap (textarea). HTML parsers also drop a newline directly after the
// start tag of pre, listing and textarea.
//
// [rendering section]: https://html.spec.whatwg.org/multipage/rendering.html#flow-content-3
func IsPreformatted[E NamedElement](e E) bool { return elem(e)&internal.Preformatted != 0 }

// IsFormatting reports whether e is an HTML element in the [formatting]
// category of the HTML Standard's parsing rules (a, b, big, code, em, font,
// i, nobr, s, small, strike, strong, tt and u), which HTML parsers keep in
// the list of active formatting elements and reopen after misnested end
// tags.
//
// [formatting]: https://html.spec.whatwg.org/multipage/parsing.html#formatting
func IsFormatting[E NamedElement](e E) bool { return elem(e)&internal.Formatting != 0 }

// IsSpecial reports whether e is an HTML element in the [special] category
// of the HTML Standard's parsing rules (block-level elements, table parts,
// head content, form controls, etc.), whose end tag rules differ from those
// of other elements: an end tag of another element inside an open special
// element is ignored. The SVG and MathML elements in the category (e.g.,
// foreignObject and mi) aren't included.
//
// [special]: https://html.spec.whatwg.org/multipage/parsing.html#special
func IsSpecial[E NamedElement](e E) bool { return elem(e)&internal.Special != 0 }

func isLocal[E NamedElement](e E, lname string) bool {
	var buf [16]byte
	return string(appendLocalName(buf[:0], e)) == lname
}

// elem returns the flags of an HTML element.
func elem[E NamedElement](e E) internal.Elem {
	if e.NS() != htmlstream.NamespaceXHTML {
		return 0
	}
	var buf [16]byte
	name := appendLocalName(buf[:0], e)
	if len(name) > len(buf) {
		return 0
	}
	if string(name) == "image" && !htmlRules(e) {
		return 0 // only HTML parsers turn image into img
	}
	return internal.HTMLElem(name)
}

// htmlRules reports whether e follows HTML rules (see
// [htmlstream.Token.HTMLRules]); names do.
func htmlRules[E NamedElement](e E) bool {
	switch e := any(e).(type) {
	case *htmlstream.Token:
		return e.HTMLRules()
	case htmlstream.Element:
		return e.HTMLRules()
	}
	return true
}

// appendLocalName calls e.AppendLocalName. The methods of a type parameter
// are called indirectly, so dst would escape to the heap; calling them on
// the concrete types lets callers append to a buffer on the stack. Other
// types append to a new slice.
func appendLocalName[E NamedElement](dst []byte, e E) []byte {
	switch e := any(e).(type) {
	case *htmlstream.Token:
		return e.AppendLocalName(dst)
	case htmlstream.Element:
		return e.AppendLocalName(dst)
	case HTMLName:
		return e.AppendLocalName(dst)
	}
	return append(dst, e.AppendLocalName(nil)...)
}

// Script and meta elements.

// ScriptType returns the type of a script start tag, ASCII-lowercased, as
// the [HTML Standard] determines it (the script block's type string):
// "text/javascript" if the type attribute is empty, or if it is missing and
// the language attribute is empty or missing; otherwise the type attribute
// with leading and trailing ASCII whitespace removed (so " module " is
// "module", and a type attribute of only whitespace gives "", which is
// neither JavaScript nor JSON); otherwise "text/" followed by the language
// attribute.
//
// Parameters are kept, so "text/javascript; charset=utf-8" isn't a
// JavaScript type, as in browsers, which don't run such scripts.
//
// [HTML Standard]: https://html.spec.whatwg.org/multipage/scripting.html#prepare-the-script-element
func ScriptType(tok *htmlstream.Token) string {
	if a, ok := tok.Attr("type"); ok {
		v := a.Value()
		if v == "" {
			return "text/javascript"
		}
		return ascii.ToLower(ascii.TrimSpace(v))
	}
	if a, ok := tok.Attr("language"); ok && a.Value() != "" {
		return "text/" + ascii.ToLower(a.Value())
	}
	return "text/javascript"
}

// IsJavaScriptType reports whether a script type (see [ScriptType]) is
// JavaScript, compared ASCII case-insensitively: module, or one of the
// JavaScript MIME types listed in the [MIME Sniffing Standard].
//
// [MIME Sniffing Standard]: https://mimesniff.spec.whatwg.org/#javascript-mime-type
func IsJavaScriptType(t string) bool {
	t = ascii.ToLower(t)
	return t == "module" || isJavaScriptMIME(t)
}

// isJavaScriptMIME reports whether the lowercase t is a JavaScript MIME type
// essence (see IsJavaScriptType).
func isJavaScriptMIME(t string) bool {
	switch t {
	case "application/ecmascript", "application/javascript", "application/x-ecmascript", "application/x-javascript",
		"text/ecmascript", "text/javascript", "text/javascript1.0", "text/javascript1.1", "text/javascript1.2",
		"text/javascript1.3", "text/javascript1.4", "text/javascript1.5", "text/jscript", "text/livescript",
		"text/x-ecmascript", "text/x-javascript":
		return true
	}
	return false
}

// IsJSONType reports whether a script type (see [ScriptType]) is JSON: the
// importmap and speculationrules types (compared ASCII case-insensitively),
// or a JSON MIME type as defined by the [MIME Sniffing Standard]: one whose
// essence (the MIME type without parameters, lowercased) is
// application/json or text/json, or whose subtype ends with +json (e.g.,
// application/ld+json).
//
// [MIME Sniffing Standard]: https://mimesniff.spec.whatwg.org/#json-mime-type
func IsJSONType(t string) bool {
	if ascii.EqualFold(t, "importmap") || ascii.EqualFold(t, "speculationrules") {
		return true
	}
	essence, _, _ := strings.Cut(t, ";")
	essence = ascii.ToLower(strings.Trim(essence, " \t\n\r")) // HTTP whitespace
	typ, sub, ok := strings.Cut(essence, "/")
	if !ok || !isHTTPToken(typ) || !isHTTPToken(sub) {
		return false
	}
	return essence == "application/json" || essence == "text/json" || strings.HasSuffix(sub, "+json")
}

// isHTTPToken reports whether s is a non-empty string of HTTP token code
// points, which MIME types and subtypes are made of.
func isHTTPToken(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		c := s[i]
		if !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0) {
			return false
		}
	}
	return true
}

// CDATAWrapper is a set of forms of the CDATA section wrapper removed by
// [CutCDATAWrapper].
type CDATAWrapper uint8

const (
	// CDATALineComment is the form with JavaScript line comments:
	// //<![CDATA[ at the end of a line, and //]]>.
	CDATALineComment CDATAWrapper = 1 << iota

	// CDATABlockComment is the form with JavaScript and CSS block comments:
	// /*<![CDATA[*/ and /*]]>*/.
	CDATABlockComment

	// CDATAHTMLComment adds the forms which also have HTML comment markers,
	// written by CMSs such as Drupal 7. With CDATALineComment, it adds the
	// form for JavaScript, <!--//--><![CDATA[//><!-- and //--><!]]>, where
	// the line starting with <!-- is a comment (an HTML-like comment, which
	// JavaScript modules don't allow). With CDATABlockComment, it adds the
	// form for CSS, <!--/*--><![CDATA[/*><!--*/ and /*]]>*/-->, whose
	// markers are also JavaScript comments (except in modules). CSS ignores
	// <!-- and --> at the top level of a style sheet; after contents leaving
	// a block open or a rule unfinished (e.g., @import "a.css" without a
	// semicolon), --> is part of the last declaration or rule instead, which
	// isn't checked.
	CDATAHTMLComment

	// CDATABare is the form without comments, <![CDATA[ and ]]>, which
	// XHTML documents use. Only XML parsers read it as a CDATA section:
	// HTML parsers read the markers as part of the script or style, where
	// they are syntax errors, so it is only for contents read as XML (e.g.,
	// with [htmlstream.Reader.SetXMLSyntax]), where removing it gives the
	// contents XML parsers read, without the whitespace around them.
	CDATABare
)

// The markers of the CDATAHTMLComment forms.
const (
	htmlLineStart  = "<!--//--><![CDATA[//><!--"
	htmlLineEnd    = "//--><!]]>"
	htmlBlockStart = "<!--/*--><![CDATA[/*><!--*/"
	htmlBlockEnd   = "/*]]>*/-->"
)

// splitCDATAEnd is "]]>" split across two CDATA sections, the usual way to
// write it in one.
const splitCDATAEnd = "]]]]><![CDATA[>"

// CutCDATAWrapper removes a CDATA section wrapped in comments around the
// contents s of a script or style element, which XHTML and polyglot markup
// use so that XML parsers read the contents without escaping and HTML
// parsers ignore the markers (e.g., //<![CDATA[ and //]]> in a script). The
// forms removed are given by w: CDATALineComment|CDATABlockComment for
// JavaScript, and CDATABlockComment for CSS, each with CDATAHTMLComment to
// also remove the forms with HTML comment markers (not for JavaScript
// modules), and with CDATABare to also remove a CDATA section without
// comments from contents read as XML. The start and the end may be in
// different forms.
//
// Only a single wrapper around all of s is removed: s must start with the
// start marker and end with the end marker, with only whitespace before and
// after them, and the contents between them must not contain "]]>", which
// ends a CDATA section, other than in "]]]]><![CDATA[>" (the usual way to
// write "]]>" in a CDATA section, which XML parsers read as "]]>", and
// which is left as it is). Otherwise (e.g., with code before the start
// marker or after the end marker, several wrapped parts, or nested
// wrappers), nothing is removed.
//
// Spaces and tabs are allowed inside the comments of the CDATALineComment
// and CDATABlockComment forms; the markers of the CDATAHTMLComment and
// CDATABare forms must be as written above. The whitespace before the start and after the
// end is removed, and so are the spaces and tabs next to the start and the
// end on their lines, and one line end after the start and before the end.
// A line comment start is only removed at the end of its line, so that the
// code after it stays commented out, and so is a CDATAHTMLComment start.
// A CDATAHTMLComment end is only removed at the start of its line, where
// JavaScript allows -->.
//
// The <!-- of the CDATAHTMLComment forms puts script contents in the
// escaped state of the HTML tokenizer, where "<script" starts a nested
// script whose end tag doesn't end the element, so these forms aren't
// removed from contents containing "<script" (in any case).
//
// If s doesn't start and end with a wrapper, CutCDATAWrapper returns s and
// false.
func CutCDATAWrapper[T ~string | ~[]byte](s T, w CDATAWrapper) (inner T, found bool) {
	t := ascii.TrimSpace(s)
	var ok bool
	html := w&CDATAHTMLComment != 0
	escaped := false // a CDATAHTMLComment marker
	var nl bool      // a line end follows the start
	switch {
	case html && w&CDATALineComment != 0 && hasPrefix(t, htmlLineStart):
		if t, nl = cutLineEnd(trimLeftBlank(t[len(htmlLineStart):])); !nl {
			return s, false
		}
		escaped = true
	case html && w&CDATABlockComment != 0 && hasPrefix(t, htmlBlockStart):
		if t, nl = cutLineEnd(trimLeftBlank(t[len(htmlBlockStart):])); !nl {
			return s, false
		}
		escaped = true
	case w&CDATALineComment != 0 && hasPrefix(t, "//"):
		t = trimLeftBlank(t[2:])
		if t, ok = cutPrefix(t, "<![CDATA["); !ok {
			return s, false
		}
		if t, nl = cutLineEnd(trimLeftBlank(t)); !nl {
			return s, false
		}
	case w&CDATABlockComment != 0 && hasPrefix(t, "/*"):
		t = trimLeftBlank(t[2:])
		if t, ok = cutPrefix(t, "<![CDATA["); !ok {
			return s, false
		}
		if t, ok = cutPrefix(trimLeftBlank(t), "*/"); !ok {
			return s, false
		}
		t, nl = cutLineEnd(trimLeftBlank(t))
	case w&CDATABare != 0 && hasPrefix(t, "<![CDATA["):
		t, nl = cutLineEnd(trimLeftBlank(t[len("<![CDATA["):]))
	default:
		return s, false
	}
	switch {
	case html && w&CDATALineComment != 0 && hasSuffix(t, htmlLineEnd):
		if t, ok = cutLineStart(t[:len(t)-len(htmlLineEnd)], nl); !ok {
			return s, false
		}
		escaped = true
	case html && w&CDATABlockComment != 0 && hasSuffix(t, htmlBlockEnd):
		if t, ok = cutLineStart(t[:len(t)-len(htmlBlockEnd)], nl); !ok {
			return s, false
		}
		escaped = true
	case w&CDATABlockComment != 0 && hasSuffix(t, "*/"):
		t = trimRightBlank(t[:len(t)-2])
		if t, ok = cutSuffix(t, "]]>"); !ok {
			return s, false
		}
		if t, ok = cutSuffix(trimRightBlank(t), "/*"); !ok {
			return s, false
		}
	case w&(CDATALineComment|CDATABare) != 0 && hasSuffix(t, "]]>"):
		t = trimRightBlank(t[:len(t)-3])
		if u, ok := cutSuffix(t, "//"); ok && w&CDATALineComment != 0 {
			t = u
		} else if w&CDATABare == 0 {
			return s, false
		}
	default:
		return s, false
	}
	t = trimRightBlank(t)
	switch {
	case hasSuffix(t, "\r\n"):
		t = t[:len(t)-2]
	case hasSuffix(t, "\n"), hasSuffix(t, "\r"):
		t = t[:len(t)-1]
	}
	for i := 0; i+3 <= len(t); i++ {
		if hasPrefix(t[i:], "]]>") && (i < 2 || !hasPrefix(t[i-2:], splitCDATAEnd)) {
			return s, false
		}
	}
	if escaped {
		for i := 0; i+7 <= len(t); i++ {
			if t[i] == '<' && ascii.EqualFold(t[i+1:i+7], "script") {
				return s, false
			}
		}
	}
	return t, true
}

// cutLineStart removes the spaces and tabs at the end of s, the text before
// an end marker, and reports whether the marker is at the start of its line:
// s ends with a line end, or is empty and nl reports that the start marker
// was followed by a line end (removed with it).
func cutLineStart[T ~string | ~[]byte](s T, nl bool) (T, bool) {
	s = trimRightBlank(s)
	if len(s) == 0 {
		return s, nl
	}
	return s, s[len(s)-1] == '\n' || s[len(s)-1] == '\r'
}

func hasPrefix[T ~string | ~[]byte](s T, prefix string) bool {
	if len(s) < len(prefix) {
		return false
	}
	for i := 0; i < len(prefix); i++ {
		if s[i] != prefix[i] {
			return false
		}
	}
	return true
}

func hasSuffix[T ~string | ~[]byte](s T, suffix string) bool {
	return len(s) >= len(suffix) && hasPrefix(s[len(s)-len(suffix):], suffix)
}

func cutPrefix[T ~string | ~[]byte](s T, prefix string) (T, bool) {
	if !hasPrefix(s, prefix) {
		return s, false
	}
	return s[len(prefix):], true
}

func cutSuffix[T ~string | ~[]byte](s T, suffix string) (T, bool) {
	if !hasSuffix(s, suffix) {
		return s, false
	}
	return s[:len(s)-len(suffix)], true
}

// cutLineEnd removes a line end (LF, CR LF or CR) at the start of s.
func cutLineEnd[T ~string | ~[]byte](s T) (T, bool) {
	if t, ok := cutPrefix(s, "\r\n"); ok {
		return t, true
	}
	if t, ok := cutPrefix(s, "\n"); ok {
		return t, true
	}
	return cutPrefix(s, "\r")
}

// trimLeftBlank removes the spaces and tabs at the start of s.
func trimLeftBlank[T ~string | ~[]byte](s T) T {
	for len(s) != 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	return s
}

// trimRightBlank removes the spaces and tabs at the end of s.
func trimRightBlank[T ~string | ~[]byte](s T) T {
	for len(s) != 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}
