package htmlutil

import (
	"bytes"

	"github.com/pgaskin/htmlstream"
	"github.com/pgaskin/htmlstream/ascii"
	"github.com/pgaskin/htmlstream/internal"
)

// TextBuilder collects text, collapsing runs of ASCII whitespace (see
// [ascii.IsSpace]) into single spaces and trimming leading and trailing
// whitespace. Non-breaking spaces are kept. Unlike CSS, which collapses
// spaces, tabs and line breaks, it also collapses form feeds. The zero value
// is an empty TextBuilder ready to use.
type TextBuilder struct {
	b     []byte
	tmp   []byte
	space bool
}

// Write appends text. It never returns an error, so a TextBuilder can be
// used as an [io.Writer].
func (t *TextBuilder) Write(p []byte) (int, error) {
	for _, c := range p {
		if ascii.IsSpace(c) {
			t.space = true
			continue
		}
		if t.space && len(t.b) != 0 {
			t.b = append(t.b, ' ')
		}
		t.space = false
		t.b = append(t.b, c)
	}
	return len(p), nil
}

// WriteString appends text, like [TextBuilder.Write].
func (t *TextBuilder) WriteString(s string) (int, error) {
	return t.Write([]byte(s))
}

// WriteText appends the decoded text of a Text or CDATA token (see
// [htmlstream.Token.AppendText]).
func (t *TextBuilder) WriteText(tok *htmlstream.Token) {
	if k := tok.Kind(); k == htmlstream.Text || k == htmlstream.CDATA {
		t.tmp = tok.AppendText(t.tmp[:0])
		t.Write(t.tmp)
	}
}

// Break separates words, like whitespace.
func (t *TextBuilder) Break() { t.space = true }

// Len returns the length of the text.
func (t *TextBuilder) Len() int { return len(t.b) }

// Bytes returns the text. It is valid until the next change.
func (t *TextBuilder) Bytes() []byte { return t.b }

// String returns the text.
func (t *TextBuilder) String() string { return string(t.b) }

// Reset clears the text.
func (t *TextBuilder) Reset() { t.b, t.space = t.b[:0], false }

// CollapseSpace returns s with runs of ASCII whitespace collapsed into
// single spaces, and leading and trailing whitespace removed, like the Infra
// Standard's [strip and collapse ASCII whitespace] (see [TextBuilder]).
//
// [strip and collapse ASCII whitespace]: https://infra.spec.whatwg.org/#strip-and-collapse-ascii-whitespace
func CollapseSpace(s string) string {
	var t TextBuilder
	t.WriteString(s)
	return t.String()
}

// TextOptions configures [InnerText].
type TextOptions struct {
	// KeepWhitespace keeps the text as it is, like the DOM's textContent:
	// whitespace isn't collapsed, and no line breaks are added.
	KeepWhitespace bool

	// AltText includes the alt text of img and area elements (and image
	// elements under HTML rules, which HTML parsers turn into img).
	AltText bool

	// Hidden includes the text in elements whose contents aren't rendered
	// (see [InnerText]).
	Hidden bool

	// ChildrenOnly only includes text directly inside the element, not in
	// descendant elements (like the DOM's HTMLScriptElement.text).
	ChildrenOnly bool

	// Max limits the length of the text in bytes, if positive. The rest of
	// the element is skipped, and InnerText reports the truncation.
	Max int
}

// InnerText reads the contents of the element opened by the current start
// tag, returning its text, and whether it was truncated to
// [TextOptions.Max] bytes. Afterwards, the current token is the element's
// end tag. If the current token doesn't open an element, it returns an empty
// string. Read errors are returned by the Reader's Err method.
//
// Unless [TextOptions.KeepWhitespace] is set, the text approximates the DOM's
// [innerText] (the rendered text collection steps) with the default styles of
// HTML elements:
//
//   - The contents of elements which aren't rendered are left out: those of
//     [IsHidden] (canvas and noscript only if the Reader has scripting
//     enabled, which isn't the default, see
//     [htmlstream.Reader.SetScripting]), HTML elements other than embed with
//     a hidden attribute, dialog elements without an open attribute, and
//     other elements with a popover attribute, which the rendering section
//     hides, and the contents of details elements without an open attribute
//     other than their first summary child.
//   - The contents of SVG clipPath, defs, desc, filter, linearGradient,
//     marker, mask, metadata, pattern, radialGradient, script, style, symbol
//     and title elements, which aren't rendered, are left out, and so are
//     the children of MathML semantics and maction elements after their
//     first child element (e.g., annotation and annotation-xml elements
//     after the presentation markup). SVG and MathML elements are those in
//     their namespaces (see [htmlstream.Token.NS]).
//   - The contents of elements with hidden="until-found" are left out where
//     browsers hide them with content-visibility: hidden, which applies to
//     block-level elements, table cells, and button, select and object
//     elements, but not to other inline elements or to table rows and row
//     groups.
//   - Whitespace is collapsed except in preformatted elements (see
//     [IsPreformatted]), and removed at the start and end of lines.
//   - Block-level elements (see [IsBlock]) other than table rows and row
//     groups are on lines of their own, and p elements are separated from
//     the text around them by a blank line.
//   - br is a line break, the cells of a table row are separated by tabs,
//     and the rows of a table by line breaks.
//
// It differs from innerText where the result depends on the styles of the
// document (e.g., display and visibility) or on layout, and in details of
// the white space processing of CSS (e.g., CSS doesn't collapse form
// feeds). The fallback content of object elements is included, and so is
// content which HTML parsers move elsewhere (e.g., text in a table outside
// the cells, which they move before the table). Where browsers differ, it
// follows Chromium for drop-down select elements (Firefox leaves out their
// options) and table cells with hidden="until-found" (Firefox doesn't hide
// them), and Firefox for svg and math elements (Chromium puts them on lines
// of their own, and renders mi elements in mathematical italic), the
// contents of clipPath, defs, marker, mask, pattern and symbol elements
// (Chromium includes their text, but not the contents of foreignObject
// elements in them), switch elements, whose children are all included
// (Chromium only renders the first), and semantics and maction elements,
// whose text and first child element are included (Chromium leaves out the
// text in them, and the annotation elements in semantics elements). If the
// element itself isn't rendered (see above), the text is its text content,
// as innerText returns for elements which aren't rendered; whether the
// enclosing elements are rendered isn't known.
//
// Template contents are never included, since HTML parsers put them in a
// separate document fragment, and a newline directly after the start tag of a
// pre, listing or textarea element is left out, like HTML parsers do (it is
// kept under XML rules, like XML parsers do; see
// [htmlstream.Token.HTMLRules]).
//
// Under HTML rules, NUL in text and CDATA sections is left out in HTML
// content, including integration points like foreignObject, and replaced
// with U+FFFD in SVG and MathML content, as in browsers (the tokenizer
// replaces it in title, textarea, script and style contents, see
// [htmlstream.Token.AppendText]). Like browsers, InnerText also leaves out
// a newline after NUL directly after the start tag of a pre or listing
// element. Under XML rules, NUL is kept.
//
// With XML syntax (see [htmlstream.Reader.SetXMLSyntax], which applies to
// the documents read like in HTML mode in [htmlstream.Universal] mode), the
// contents of title, textarea, script and style elements are text up to
// their end tag, as in HTML parsers, so a CDATA section in them is text,
// markers included, with character references decoded in title and
// textarea. XML parsers read it as a CDATA section, without the markers, and
// the polyglot package removes a CDATA section around all of the contents.
// [CutCDATAWrapper] with [CDATABare] removes such a section from the text.
//
// [TextContent] returns the text like the DOM's textContent instead, and
// [CollapseSpace] puts the result on one line.
//
// [innerText]: https://html.spec.whatwg.org/multipage/dom.html#rendered-text-collection-steps
func InnerText(r *htmlstream.Reader, opt TextOptions) (s string, truncated bool) {
	w := textWriter{raw: opt.KeepWhitespace}
	var only []onlyChild // open elements rendering only their first child
	pre := 0             // open preformatted elements
	stripNL := false     // the next token directly follows a pre, listing or textarea start tag
	foreign := false     // text in the innermost open element is in foreign content (see foreignText)
	var outer []bool     // foreign for the elements around it
	if tok := r.Token(); tok != nil && tok.Kind() == htmlstream.StartTag && tok.Opens() {
		foreign = foreignText(tok)
		if isHTML(tok, "template") {
			r.Skip()
			return "", false
		}
		if !opt.Hidden && hidden(r, tok) {
			// innerText returns the text content of an element which isn't
			// rendered
			w.raw, opt.Hidden = true, true
		}
		if IsPreformatted(tok) {
			pre, stripNL = 1, stripsNewline(tok)
		}
		if summary, ok := rendersOneChild(tok); !opt.Hidden && ok {
			only = append(only, onlyChild{r.Depth(), summary})
		}
	}
	first := false // the rendered child of the innermost element in only was read
	var buf []byte
	for tok := range r.Children() {
		// Text is collected until it is longer than the limit, so
		// truncation can be detected; collapsed whitespace and line breaks
		// aren't written until more text follows.
		if opt.Max > 0 && len(w.b) > opt.Max {
			continue // skip the rest
		}
		if tok.Kind() == htmlstream.Comment && !tok.Continued() && string(tok.Raw()) == "</>" {
			continue // HTML parsers ignore it (see htmlstream.Token.Bogus)
		}
		nl := stripNL
		stripNL = false
		inOnly := len(only) != 0 && r.Depth() == only[len(only)-1].depth
		switch tok.Kind() {
		case htmlstream.Text, htmlstream.CDATA:
			if inOnly && only[len(only)-1].summary {
				continue
			}
			buf = tok.AppendText(buf[:0])
			b := buf
			if tok.HTMLRules() && bytes.IndexByte(b, 0) >= 0 {
				// browsers ignore NUL in HTML content, also before the
				// newline they drop, and replace it with U+FFFD in foreign
				// content
				b = nulText(buf[len(buf):], b, foreign)
				if len(b) == 0 {
					stripNL = nl
					continue
				}
			}
			if nl && tok.Kind() == htmlstream.Text && len(b) != 0 && b[0] == '\n' {
				b = b[1:]
			}
			w.text(b, pre > 0)
		case htmlstream.StartTag:
			if opt.ChildrenOnly || !opt.Hidden && hidden(r, tok) || isHTML(tok, "template") {
				r.Skip()
				continue
			}
			if n := len(only); n != 0 && r.Depth() == only[n-1].depth+1 {
				if first || only[n-1].summary && !isHTML(tok, "summary") {
					r.Skip()
					continue
				}
				first = true
			}
			if summary, ok := rendersOneChild(tok); !opt.Hidden && tok.Opens() && ok {
				only = append(only, onlyChild{r.Depth(), summary})
				first = false
			}
			if tok.Opens() && IsPreformatted(tok) {
				pre++
				stripNL = stripsNewline(tok)
			}
			if tok.Opens() {
				outer = append(outer, foreign)
				foreign = foreignText(tok)
			}
			if opt.AltText && hasAltText(tok) {
				if a, ok := tok.Attr("alt"); ok {
					w.text(a.AppendValue(buf[:0]), false)
				}
			}
			w.tag(tok)
		case htmlstream.EndTag:
			if pre > 0 && IsPreformatted(tok) {
				pre--
			}
			if n := len(outer); n != 0 && !tok.Stray() {
				foreign, outer = outer[n-1], outer[:n-1]
			}
			if inOnly && !tok.Stray() {
				// the rendered child of the enclosing element was read,
				// since this one is inside it
				only, first = only[:len(only)-1], true
			}
			w.tag(tok)
		}
	}
	s = string(w.b)
	if opt.Max > 0 && len(s) > opt.Max {
		s, truncated = truncateUTF8(s, opt.Max), true
	}
	return s, truncated
}

// TextContent reads the contents of the element opened by the current start
// tag like [InnerText], returning its text like the DOM's [textContent]: all
// the text in the element as it is (including CDATA sections, and with XML
// syntax, the markers of those in title, textarea, script and style elements,
// see InnerText, which also describes how NUL is left out or replaced),
// including the contents of hidden elements like script and style, but not
// template contents. It is InnerText with
// TextOptions{KeepWhitespace: true, Hidden: true, Max: limit}: if limit is
// positive, the text is truncated to limit bytes, and TextContent reports it.
// Read errors are returned by the Reader's Err method.
//
// [textContent]: https://dom.spec.whatwg.org/#dom-node-textcontent
func TextContent(r *htmlstream.Reader, limit int) (s string, truncated bool) {
	return InnerText(r, TextOptions{KeepWhitespace: true, Hidden: true, Max: limit})
}

// textWriter collects the text of [InnerText].
type textWriter struct {
	b      []byte
	raw    bool
	space  bool // collapsed whitespace before the next text
	breaks int  // line breaks before the next text (the innerText "required line break count")
	tables []tableText
}

// tableText is the state of an open table in [textWriter].
type tableText struct {
	cell bool // a table cell ended in the current row
	row  bool // a table row ended
}

// table returns the state of the innermost open table.
func (w *textWriter) table() *tableText {
	if len(w.tables) == 0 {
		w.tables = append(w.tables, tableText{}) // cells outside a table
	}
	return &w.tables[len(w.tables)-1]
}

// text writes text, collapsing whitespace unless pre.
func (w *textWriter) text(s []byte, pre bool) {
	if w.raw || pre {
		if len(s) != 0 {
			w.flush()
			w.b = append(w.b, s...)
		}
		return
	}
	for _, c := range s {
		if ascii.IsSpace(c) {
			w.space = true
			continue
		}
		w.flush()
		w.b = append(w.b, c)
	}
}

// str writes s, which replaces collapsed whitespace before it.
func (w *textWriter) str(s string) {
	w.space = false
	w.flush()
	w.b = append(w.b, s...)
}

// flush writes the line breaks or collapsed whitespace before more text.
// None are written at the start.
func (w *textWriter) flush() {
	if n := len(w.b); n != 0 {
		if w.breaks != 0 {
			for range w.breaks {
				w.b = append(w.b, '\n')
			}
		} else if w.space && w.b[n-1] != '\n' && w.b[n-1] != '\t' {
			w.b = append(w.b, ' ')
		}
	}
	w.space, w.breaks = false, 0
}

// tag writes the line breaks and tabs for a start or end tag.
func (w *textWriter) tag(tok *htmlstream.Token) {
	if w.raw || tok.NS() != htmlstream.NamespaceXHTML && !tok.Stray() {
		return
	}
	start := tok.Kind() == htmlstream.StartTag
	var buf [16]byte
	name := string(tok.AppendLocalName(buf[:0]))
	if tok.Stray() && (!tok.HTMLRules() || name != "br" && name != "p") {
		return // other stray end tags are ignored by HTML parsers
	}
	switch name {
	case "br":
		if start || tok.Stray() { // HTML parsers read </br> as <br>
			w.str("\n")
		}
		return
	case "td", "th":
		if t := w.table(); start {
			if t.cell {
				w.str("\t")
			}
		} else {
			t.cell = true
		}
		return
	case "tr":
		if t := w.table(); start {
			if t.row {
				w.str("\n")
			}
			t.cell = false
		} else {
			t.row = true
		}
		return
	case "tbody", "thead", "tfoot":
		return // their rows are separated
	case "table":
		if start {
			w.tables = append(w.tables, tableText{})
		} else if len(w.tables) != 0 {
			w.tables = w.tables[:len(w.tables)-1]
		}
	case "p":
		w.breaks = max(w.breaks, 2)
		return
	}
	if IsBlock(tok) {
		w.breaks = max(w.breaks, 1)
	}
}

// hidden reports whether the contents of the element opened by the start tag
// tok aren't rendered (see InnerText).
func hidden(r *htmlstream.Reader, tok *htmlstream.Token) bool {
	if IsHidden(tok) {
		return r.Scripting() || !isHTML(tok, "noscript") && !isHTML(tok, "canvas")
	}
	switch tok.NS() {
	case htmlstream.NamespaceXHTML:
	case htmlstream.NamespaceSVG:
		var buf [16]byte
		name := tok.AppendLocalName(buf[:0])
		if n, ok := internal.SVGTagNames[string(name)]; ok && tok.HTMLRules() {
			name = append(name[:0], n...)
		}
		switch string(name) {
		case "clipPath", "defs", "desc", "filter", "linearGradient", "marker", "mask", "metadata", "pattern", "radialGradient", "script", "style", "symbol", "title":
			return true
		}
		return false
	default:
		return false
	}
	if a, ok := tok.Attr("hidden"); ok && !isLocal(tok, "embed") {
		if !ascii.EqualFold(a.Value(), "until-found") {
			return true
		}
		// content-visibility: hidden, which doesn't apply to inline and
		// table-internal boxes other than cells
		switch {
		case isLocal(tok, "tr"), isLocal(tok, "tbody"), isLocal(tok, "thead"), isLocal(tok, "tfoot"):
		case IsBlock(tok), isLocal(tok, "td"), isLocal(tok, "th"), isLocal(tok, "button"), isLocal(tok, "select"), isLocal(tok, "object"):
			return true
		}
	}
	if isLocal(tok, "dialog") {
		return !tok.HasAttr("open")
	}
	return tok.HasAttr("popover")
}

// onlyChild is an open element rendering only its first child (see
// rendersOneChild).
type onlyChild struct {
	depth   int  // the Reader's depth inside the element
	summary bool // a closed details element
}

// rendersOneChild reports whether the start tag tok is that of an element
// rendering only its first child: a details element without an open
// attribute, which renders only its first summary child (summary is set),
// or a MathML semantics or maction element, which renders only its first
// child element (and the text in it, as in Firefox).
func rendersOneChild(tok *htmlstream.Token) (summary, ok bool) {
	switch {
	case isHTML(tok, "details"):
		return true, !tok.HasAttr("open")
	case tok.NS() == htmlstream.NamespaceMathML && (isLocal(tok, "semantics") || isLocal(tok, "maction")):
		return false, true
	}
	return false, false
}

// isHTML reports whether tok is a start or end tag of the HTML element with
// the lowercase name lname.
func isHTML(tok *htmlstream.Token, lname string) bool {
	return tok.NS() == htmlstream.NamespaceXHTML && isLocal(tok, lname)
}

// stripsNewline reports whether HTML parsers drop a newline directly after
// the start tag tok.
func stripsNewline(tok *htmlstream.Token) bool {
	return tok.HTMLRules() && (isHTML(tok, "pre") || isHTML(tok, "listing") || isHTML(tok, "textarea"))
}

// foreignText reports whether text directly in the element opened by the
// start tag tok is in SVG or MathML content outside the integration points,
// where HTML parsers replace NUL with U+FFFD instead of ignoring it (see
// [tree construction]).
//
// [tree construction]: https://html.spec.whatwg.org/multipage/parsing.html#tree-construction
func foreignText(tok *htmlstream.Token) bool {
	ns := tok.NS()
	if ns != htmlstream.NamespaceSVG && ns != htmlstream.NamespaceMathML {
		return false
	}
	var buf [16]byte
	switch string(tok.AppendLocalName(buf[:0])) { // lowercase under HTML rules
	case "foreignobject", "desc", "title":
		return ns != htmlstream.NamespaceSVG
	case "mi", "mo", "mn", "ms", "mtext":
		return ns != htmlstream.NamespaceMathML
	case "annotation-xml":
		if a, ok := tok.Attr("encoding"); ok && ns == htmlstream.NamespaceMathML {
			return !ascii.EqualFold(a.Value(), "text/html") && !ascii.EqualFold(a.Value(), "application/xhtml+xml")
		}
	}
	return true
}

// nulText appends b to dst without NUL, or with NUL replaced with U+FFFD if
// replace is set.
func nulText(dst, b []byte, replace bool) []byte {
	for _, c := range b {
		switch {
		case c != 0:
			dst = append(dst, c)
		case replace:
			dst = append(dst, "\uFFFD"...)
		}
	}
	return dst
}

// hasAltText reports whether a start tag is an img or area element, or an
// image element which HTML parsers turn into img.
func hasAltText(tok *htmlstream.Token) bool {
	if tok.NS() != htmlstream.NamespaceXHTML {
		return false
	}
	var buf [16]byte
	switch string(tok.AppendLocalName(buf[:0])) {
	case "img", "area":
		return true
	case "image":
		return tok.HTMLRules()
	}
	return false
}

func truncateUTF8(s string, n int) string {
	for n > 0 && n < len(s) && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}
