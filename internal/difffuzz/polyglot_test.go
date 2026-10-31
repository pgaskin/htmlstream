package difffuzz

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"testing/iotest"

	"golang.org/x/net/html"

	"github.com/pgaskin/htmlstream"
	"github.com/pgaskin/htmlstream/ascii"
	"github.com/pgaskin/htmlstream/htmlutil"
	"github.com/pgaskin/htmlstream/polyglot"
	polyglot_testdata "github.com/pgaskin/htmlstream/testdata/polyglot"
)

type polyglotTest struct {
	Name     string
	In       string
	Out      string // if not empty, the expected output
	Warn     string // the expected warnings, one "pos: message" per line
	HTMLDiff bool   // the HTML DOM of the output is expected to differ from the input

	XMLSyntax, Scripting bool // the polyglot.Options set by the xs and scripting flags
}

// polyglotTests returns the test cases with expected outputs in the
// htmlstream module's testdata/polyglot, and polyglotLongTests.
//
// The DOM comparison doesn't distinguish xlink:href in the XLink namespace
// from xlink:href in no namespace, and ignores xml:lang, so for the cases
// with prefixed names (namespaces.txtar), only the expected outputs and
// warnings check the namespaces.
func polyglotTests() []polyglotTest {
	var cases []polyglotTest
	for _, tc := range polyglotCases("*.txtar") {
		if tc.Out != "" {
			cases = append(cases, tc)
		}
	}
	return append(cases, polyglotLongTests...)
}

// polyglotLongTests are inputs split into multiple tokens by the Reader, too
// large for the test data files. Only the DOMs and the absence of warnings
// are checked.
var polyglotLongTests = []polyglotTest{
	{
		Name: "LongText",
		In:   "<p>" + strings.Repeat("a&amp;b\u00e9&#8364;&#x1F600;&lt;\r\n", 20000),
	},
	{
		Name: "LongScript",
		In:   "<script>" + strings.Repeat("if (a < b && c) {}\r\n", 20000) + "</script>",
	},
	{
		// whitespace split into multiple tokens by the Reader
		Name: "ImpliedHeadWhitespace",
		In:   `<title>x</title>` + strings.Repeat(" ", 70000) + `abc`,
	},
}

// polyglotCases returns the test cases in the files of the htmlstream
// module's testdata/polyglot matching pattern (see [polyglot_testdata.Cases]).
// The htmldiff, xs and scripting flags set HTMLDiff, XMLSyntax and Scripting.
func polyglotCases(pattern string) []polyglotTest {
	var cases []polyglotTest
	for name, c := range polyglot_testdata.Cases(pattern, "in", "out", "warn") {
		in, _ := c.Section("in")
		out, _ := c.Section("out")
		warn, _ := c.Section("warn")
		cases = append(cases, polyglotTest{Name: name, In: in, Out: out, Warn: warn, HTMLDiff: c.HasFlag("htmldiff"), XMLSyntax: c.HasFlag("xs"), Scripting: c.HasFlag("scripting")})
	}
	return cases
}

func TestPolyglot(t *testing.T) {
	for _, tc := range polyglotTests() {
		t.Run(tc.Name, func(t *testing.T) {
			var warnings []string
			var b bytes.Buffer
			err := polyglot.Convert(&b, iotest.OneByteReader(strings.NewReader(tc.In)), polyglot.Options{
				XMLSyntax: tc.XMLSyntax,
				Scripting: tc.Scripting,
				Warn: func(w polyglot.Warning) {
					warnings = append(warnings, fmt.Sprintf("%d: %s", w.Pos, w.Msg))
				},
			})
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			out := b.String()
			t.Logf("output: %s", out)
			for _, w := range warnings {
				t.Logf("warning: %s", w)
			}
			if tc.Out != "" && out != tc.Out {
				t.Errorf("output:\n got %s\nwant %s", out, tc.Out)
			}
			if w := strings.Join(warnings, "\n"); w != tc.Warn {
				t.Errorf("warnings:\n%s\nwant:\n%s", w, tc.Warn)
			}

			// with XML syntax, the input as HTML parsers would have to read
			// it (see xmlSyntaxInput), if it can be written
			html := specXMLDecl(tc.In)
			if tc.XMLSyntax {
				html = xmlSyntaxInput(t, tc.In, tc.Scripting)
			}
			inDOM := htmlDOMScripting(t, html, tc.Scripting, true)
			outDOM := htmlDOMScripting(t, out, tc.Scripting, true)
			if !tc.HTMLDiff && html != "" && inDOM != outDOM {
				t.Errorf("HTML DOM differs (input -, output +):\n%s", domDiff(inDOM, outDOM))
			}
			if tc.Warn == "" {
				xmlDOM, err := xmlDOM(out, true)
				if err != nil {
					t.Fatalf("output is not well-formed XML: %v", err)
				}
				if outDOM := htmlDOMScripting(t, out, tc.Scripting, true); xmlDOM != outDOM {
					t.Errorf("XML DOM differs from HTML DOM (XML -, HTML +):\n%s", domDiff(xmlDOM, outDOM))
				}
			}

			// without a doctype, the output is in quirks mode, like an input
			// without one
			b.Reset()
			warnings = warnings[:0]
			err = polyglot.Convert(&b, strings.NewReader(tc.In), polyglot.Options{
				DoctypeMode: polyglot.NoDoctype,
				XMLSyntax:   tc.XMLSyntax,
				Scripting:   tc.Scripting,
				Warn: func(w polyglot.Warning) {
					warnings = append(warnings, fmt.Sprintf("%d: %s", w.Pos, w.Msg))
				},
			})
			if err != nil {
				t.Fatalf("NoDoctype: error: %v", err)
			}
			if out = b.String(); len(warnings) != 0 {
				return
			}
			if outDOM = htmlDOMScripting(t, out, tc.Scripting, true); html != "" && inDOM != outDOM {
				t.Errorf("NoDoctype: HTML DOM differs (input -, output +):\n%s\noutput: %s", domDiff(inDOM, outDOM), out)
			}
			xmlDOM, err := xmlDOM(out, true)
			if err != nil {
				t.Fatalf("NoDoctype: output is not well-formed XML: %v", err)
			}
			if outDOM := htmlDOMScripting(t, out, tc.Scripting, true); xmlDOM != outDOM {
				t.Errorf("NoDoctype: XML DOM differs from HTML DOM (XML -, HTML +):\n%s", domDiff(xmlDOM, outDOM))
			}
		})
	}
}

// TestPolyglotFinalNewline checks that with FinalNewline and Indent, the
// HTML DOM of the output of a body ending at a block boundary is the same
// as without FinalNewline, and differs from the XML DOM only by the line
// break after the html end tag.
func TestPolyglotFinalNewline(t *testing.T) {
	for _, in := range []string{
		"<p>a</p>",
		"<p>a</p>\n</body>\n</html>\n",
		"<div><p>x",
		"<p>x</p>\n<!-- a -->\n",
		"<p>x</p><script>s</script>\n",
		"<pre>x\n</pre>\n",
	} {
		var outs [2]string
		for i, final := range []bool{false, true} {
			var b bytes.Buffer
			if err := polyglot.Convert(&b, strings.NewReader(in), polyglot.Options{Indent: "\t", FinalNewline: final}); err != nil {
				t.Fatalf("%q: %v", in, err)
			}
			outs[i] = b.String()
		}
		out, ok := strings.CutSuffix(outs[1], "</html>\n")
		if !ok {
			t.Errorf("%q: output doesn't end with a line break after the html end tag: %q", in, outs[1])
			continue
		}
		if want, got := htmlDOMScripting(t, outs[0], false, true), htmlDOMScripting(t, outs[1], false, true); got != want {
			t.Errorf("%q: HTML DOM differs with FinalNewline:\noutput: %q\nDOM (without -, with +):\n%s", in, outs[1], domDiff(want, got))
		}
		xml, err := xmlDOM(outs[1], true)
		if err != nil {
			t.Fatalf("%q: output is not well-formed XML: %v", in, err)
		}
		if html := htmlDOMScripting(t, out+"</html>", false, true); xml != html {
			t.Errorf("%q: XML DOM differs from the HTML DOM without the last line break:\noutput: %q\nDOM (XML -, HTML +):\n%s", in, outs[1], domDiff(xml, html))
		}
	}
}

// domDiff returns the lines of the DOM dumps a and b (see htmlDOM) between
// their common leading and trailing lines, with a line of context on each
// side, prefixed with "-" for a and "+" for b.
func domDiff(a, b string) string {
	al := strings.Split(strings.TrimSuffix(a, "\n"), "\n")
	bl := strings.Split(strings.TrimSuffix(b, "\n"), "\n")
	pre := 0
	for pre < len(al) && pre < len(bl) && al[pre] == bl[pre] {
		pre++
	}
	suf := 0
	for suf < len(al)-pre && suf < len(bl)-pre && al[len(al)-1-suf] == bl[len(bl)-1-suf] {
		suf++
	}
	var s strings.Builder
	if pre > 0 {
		s.WriteString(" " + al[pre-1] + "\n")
	}
	for _, l := range al[pre : len(al)-suf] {
		s.WriteString("-" + l + "\n")
	}
	for _, l := range bl[pre : len(bl)-suf] {
		s.WriteString("+" + l + "\n")
	}
	if suf > 0 {
		s.WriteString(" " + al[len(al)-suf] + "\n")
	}
	return s.String()
}

// The DOMs are compared as indented text, ignoring empty comments (x/net
// reads </> as one, and specPreNewlines inserts them), doctypes,
// whitespace-only text (unless space is set, which the comparisons of an
// output with its input and with its XML DOM do; checkIndent doesn't),
// namespace declarations, xml:lang, charset declarations, and the CDATA
// wrappers in scripts and styles. The data of comments is compared as
// polyglot.Convert writes it (see domComment), and the XML declaration it
// removes from the input is removed before parsing it (see specXMLDecl). An
// HTML element with xml:lang and without lang is compared as having a lang
// attribute with the value of xml:lang, since polyglot.Convert adds it. The
// SVG element fedropshadow is compared as feDropShadow, the case the HTML
// Standard gives it and x/net doesn't.

func htmlDOM(t *testing.T, s string) string {
	return htmlDOMScripting(t, s, false, false)
}

// htmlDOMScripting is htmlDOM with the scripting flag of the parser set,
// keeping whitespace-only text if space is set.
func htmlDOMScripting(t *testing.T, s string, scripting, space bool) string {
	doc, err := html.ParseWithOptions(strings.NewReader(specCaptionTh(specPreNewlines(specDoctypes(s)))), html.ParseOptionEnableScripting(scripting))
	if err != nil {
		t.Fatalf("html parse: %v", err)
	}
	var b strings.Builder
	var text strings.Builder
	var walk func(n *html.Node, depth int)
	walk = func(n *html.Node, depth int) {
		flush := func() {
			domText(&b, depth, text.String(), space)
			text.Reset()
		}
		defer flush()
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			switch c.Type {
			case html.CommentNode:
				if c.Data != "" {
					flush()
					domComment(&b, depth, c.Data)
				}
			case html.ElementNode:
				if dropCharsetDecl(c) {
					continue
				}
				flush()
				var attrs []string
				for _, a := range c.Attr {
					k := a.Key
					if a.Namespace != "" {
						k = a.Namespace + ":" + k
					}
					attrs = append(attrs, k+"="+a.Val)
				}
				name := c.Data
				if c.Namespace == "svg" && name == "fedropshadow" {
					name = "feDropShadow"
				}
				domElem(&b, depth, htmlNS(c.Namespace), name, attrs)
				walk(c, depth+1)
			case html.TextNode:
				t := c.Data
				if c.Parent != nil && c.Parent.Namespace == "" && (c.Parent.Data == "script" || c.Parent.Data == "style") {
					t = normalizeRawText(t, c.Parent.Data == "script")
				}
				text.WriteString(t)
			}
		}
	}
	walk(doc, 0)
	return b.String()
}

// specDoctypes replaces the first doctype in s, if only comments and
// whitespace precede it, with <!DOCTYPE html> or <!DOCTYPE quirks>, as
// htmlutil.Document decides the document is in no-quirks or quirks mode, and
// removes the other doctypes, unless they follow text not ending in
// whitespace, which could join what follows them (e.g., <<!DOCTYPE>a); those
// are replaced like the first. x/net's choice of mode differs from the HTML5
// spec: it compares the name "html" case-sensitively and selects quirks mode
// for any public identifier without a system identifier. The spec ignores
// doctypes in all insertion modes but the initial one, while x/net returns to
// the "in body" insertion mode for a doctype after a body end tag, which
// moves a comment after it from the html element into the body (e.g.,
// </body><!DOCTYPE><!--x-->). Doctypes aren't part of the compared DOM.
// Document's choice is checked against the spec by TestDocumentQuirks in
// htmlutil.
func specDoctypes(s string) string {
	if !strings.Contains(strings.ToLower(s), "<!doctype") {
		return s
	}
	r := htmlstream.NewReader(strings.NewReader(s))
	var d htmlutil.Document
	var doctypes [][3]int // start, end, and 1 if replaced rather than removed
	first := -1           // the index of the doctype which sets the mode
	content := false      // a token other than a comment, a doctype or whitespace was read
	afterText := false    // the last token was text not ending in whitespace
	for r.Next() {
		tok := r.Token()
		d.Update(tok)
		prevText := afterText
		afterText = tok.Kind() == htmlstream.Text && len(tok.Raw()) != 0 && !strings.ContainsRune(" \t\n\f\r", rune(tok.Raw()[len(tok.Raw())-1]))
		switch {
		case tok.Kind() == htmlstream.Doctype:
			replace := 0
			if !content && first < 0 {
				first = len(doctypes)
			}
			if prevText {
				replace = 1
			}
			doctypes = append(doctypes, [3]int{int(tok.Pos()), int(tok.Pos()) + len(tok.Raw()), replace})
		case tok.Kind() == htmlstream.Comment, len(tok.Raw()) == 0:
		case tok.Kind() == htmlstream.Text && strings.Trim(string(tok.Raw()), " \t\n\f\r") == "":
		default:
			content = true
		}
	}
	if r.Err() != nil {
		return s
	}
	doctype := "<!DOCTYPE html>"
	if d.Quirks() {
		doctype = "<!DOCTYPE quirks>"
	}
	var b strings.Builder
	last := 0
	for i, dt := range doctypes {
		b.WriteString(s[last:dt[0]])
		if i == first || dt[2] == 1 {
			b.WriteString(doctype)
		}
		last = dt[1]
	}
	b.WriteString(s[last:])
	return b.String()
}

// specXMLDecl removes the first XML declaration before the root element from
// s, which polyglot.Convert removes (or writes as an XML declaration with
// KeepXMLDecl) rather than writing it as a comment like other bogus comments
// (see its xmlDecl). HTML parsers read it as a bogus comment, a child of the
// document before the html element.
func specXMLDecl(s string) string {
	if !strings.Contains(s, "?xml") {
		return s
	}
	r := htmlstream.NewReader(strings.NewReader(s))
	var d htmlutil.Document
	start := -1 // the start of the comment being read, if it is an XML declaration
	var data []byte
	for r.Next() {
		tok := r.Token()
		d.Update(tok)
		if d.Part() != htmlutil.BeforeHTML {
			break
		}
		if tok.Kind() != htmlstream.Comment || !tok.Bogus() {
			continue
		}
		if !tok.Continued() {
			start, data = int(tok.Pos()), data[:0]
		}
		data = tok.AppendText(data)
		if tok.Partial() {
			continue
		}
		if isXMLDecl(string(data)) {
			return s[:start] + s[int(tok.Pos())+len(tok.Raw()):]
		}
	}
	return s
}

// specPreNewlines inserts an empty comment before text in a pre or listing
// element starting with a newline after end tags which HTML parsers ignore
// (e.g., <pre></b>\nx, or <pre>\n</b>\nx), or starting with a carriage
// return from a character reference (<pre>&#13;x). The HTML5 spec drops a
// line feed only directly after the start tag, while x/net drops a carriage
// return and a line feed at the start of any text while the element has no
// children, and the comment is a child. Empty comments aren't part of the
// compared DOM. It also removes </> after the start tag, which x/net
// reads as an empty comment, a child, while the HTML5 spec ignores it
// (<pre></>\nx).
func specPreNewlines(s string) string {
	if l := strings.ToLower(s); !strings.Contains(l, "<pre") && !strings.Contains(l, "<listing") {
		return s
	}
	r := htmlstream.NewReader(strings.NewReader(s))
	var at, cut []int
	inPre, ignored := false, false
	for r.Next() {
		tok := r.Token()
		switch {
		case tok.Kind() == htmlstream.StartTag && tok.NS() == htmlstream.NamespaceXHTML && (tok.Is("pre") || tok.Is("listing")) && tok.Opens():
			inPre, ignored = true, false
			continue
		case !inPre:
			continue
		case tok.Kind() == htmlstream.Comment && !tok.Continued() && string(tok.Raw()) == "</>":
			// after a tag, so removing it doesn't make a tag of a < before it
			cut = append(cut, int(tok.Pos()))
			continue
		case tok.Kind() == htmlstream.EndTag && tok.Stray() && !tok.Is("p") && !tok.Is("br"):
			ignored = true
			continue
		case tok.Kind() == htmlstream.Text && !ignored && slices.Contains([]string{"\n", "\r", "\r\n"}, string(tok.Raw())):
			// only the newline dropped by HTML parsers
			continue
		case tok.Kind() == htmlstream.Text && !tok.Continued():
			if d := tok.AppendText(nil); len(d) != 0 && (d[0] == '\r' || ignored && d[0] == '\n') {
				at = append(at, int(tok.Pos()))
			}
		}
		inPre = false
	}
	if r.Err() != nil || len(at) == 0 && len(cut) == 0 {
		return s
	}
	var b strings.Builder
	last := 0
	for len(at) != 0 || len(cut) != 0 {
		if len(cut) != 0 && (len(at) == 0 || cut[0] < at[0]) {
			b.WriteString(s[last:cut[0]])
			last = cut[0] + len("</>")
			cut = cut[1:]
			continue
		}
		b.WriteString(s[last:at[0]])
		b.WriteString("<!---->")
		last = at[0]
		at = at[1:]
	}
	b.WriteString(s[last:])
	return b.String()
}

// specCaptionTh inserts a caption end tag before th start tags which close a
// caption (as implied end tags of the Reader). The HTML5 spec closes the
// caption for th like for td, while x/net ignores th in a caption.
func specCaptionTh(s string) string {
	if l := strings.ToLower(s); !strings.Contains(l, "<caption") || !strings.Contains(l, "<th") {
		return s
	}
	r := htmlstream.NewReader(strings.NewReader(s))
	var at []int
	closed := false // a caption was closed by the implied end tags before the token
	for r.Next() {
		tok := r.Token()
		switch {
		case tok.Implied():
			closed = closed || tok.NS() == htmlstream.NamespaceXHTML && tok.Is("caption")
			continue
		case closed && tok.Kind() == htmlstream.StartTag && tok.NS() == htmlstream.NamespaceXHTML && tok.Is("th"):
			at = append(at, int(tok.Pos()))
		}
		closed = false
	}
	if r.Err() != nil || len(at) == 0 {
		return s
	}
	var b strings.Builder
	last := 0
	for _, i := range at {
		b.WriteString(s[last:i])
		b.WriteString("</caption>")
		last = i
	}
	b.WriteString(s[last:])
	return b.String()
}

// dropCharsetDecl removes the attributes of the character encoding
// declaration from a meta element (see charsetDecl), and reports whether
// polyglot drops the element.
func dropCharsetDecl(n *html.Node) bool {
	if n.Namespace != "" || n.Data != "meta" {
		return false
	}
	var attrs [][2]string
	for _, a := range n.Attr {
		if a.Namespace == "" {
			attrs = append(attrs, [2]string{a.Key, a.Val})
		}
	}
	decl, drop := charsetDecl(attrs)
	n.Attr = slices.DeleteFunc(n.Attr, func(a html.Attribute) bool { return a.Namespace == "" && slices.Contains(decl, a.Key) })
	return drop
}

// charsetDecl returns the names of the attributes of a meta element with the
// attributes attrs (name, value) which form a character encoding
// declaration, which polyglot replaces with its own: charset, and
// http-equiv="Content-Type" (whatever its value) with content. It reports
// whether polyglot drops the element: if it has a declaration, and no other
// attributes except xmlns.
func charsetDecl(attrs [][2]string) (decl []string, drop bool) {
	for _, a := range attrs {
		switch {
		case a[0] == "charset" && !slices.Contains(decl, "charset"):
			decl = append(decl, "charset")
		case a[0] == "http-equiv" && strings.EqualFold(a[1], "content-type") && !slices.Contains(decl, "content"):
			decl = append(decl, "http-equiv", "content")
		}
	}
	return decl, len(decl) != 0 && !slices.ContainsFunc(attrs, func(a [2]string) bool { return a[0] != "xmlns" && !slices.Contains(decl, a[0]) })
}

func htmlNS(ns string) string {
	switch ns {
	case "":
		return htmlstream.NamespaceXHTML
	case "svg":
		return htmlstream.NamespaceSVG
	case "math":
		return htmlstream.NamespaceMathML
	}
	return ns
}

// normalizeRawText removes the JSON escapes and the CDATA wrappers added to
// script and style contents, as seen by HTML parsers ("//<![CDATA[") and XML
// parsers ("//"). The wrappers which polyglot removes from the input are
// removed too, so that an input with one has the same contents as the
// output. The whitespace at the start and the end of the contents is
// removed: polyglot keeps the whitespace around a wrapper it removes, while
// CutCDATAWrapper removes it, and a single line end at the start or the end
// of the contents takes the place of the one polyglot writes after the start
// of a wrapper or before its end.
func normalizeRawText(s string, script bool) string {
	s = strings.NewReplacer(`\u003c`, "<", `\u003e`, ">", `\u0026`, "&").Replace(s)
	// polyglot doesn't remove the CDATAHTMLComment forms from modules, but
	// they contain "]]>", which gives a warning
	w := htmlutil.CDATABlockComment | htmlutil.CDATAHTMLComment
	if script {
		w |= htmlutil.CDATALineComment
	}
	if t, ok := htmlutil.CutCDATAWrapper(s, w); ok {
		s = t
	} else {
		t := strings.Trim(s, " \t\n\f\r")
		for _, w := range [][2]string{{"//\n", "\n//"}, {"/**/\n", "\n/**/"}} {
			if strings.HasPrefix(t, w[0]) && strings.HasSuffix(t, w[1]) {
				s = t[len(w[0]) : len(t)-len(w[1])]
				break
			}
		}
	}
	return strings.Trim(s, " \t\n\f\r")
}

func domElem(b *strings.Builder, depth int, ns, name string, attrs []string) {
	if ns == htmlstream.NamespaceXHTML && !slices.ContainsFunc(attrs, func(a string) bool { return strings.HasPrefix(a, "lang=") }) {
		for _, a := range attrs {
			if v, ok := strings.CutPrefix(a, "xml:lang="); ok {
				attrs = append(attrs, "lang="+v)
				break
			}
		}
	}
	attrs = slices.DeleteFunc(attrs, func(a string) bool {
		return strings.HasPrefix(a, "xmlns=") || strings.HasPrefix(a, "xmlns:") || strings.HasPrefix(a, "xml:lang=")
	})
	slices.Sort(attrs)
	fmt.Fprintf(b, "%s<{%s}%s %q>\n", strings.Repeat("  ", depth), ns, name, attrs)
}

func domText(b *strings.Builder, depth int, text string, space bool) {
	if text == "" || !space && strings.TrimSpace(text) == "" {
		return
	}
	fmt.Fprintf(b, "%s%q\n", strings.Repeat("  ", depth), text)
}

// domComment writes a comment with its data as polyglot.Convert writes it:
// with a space between two dashes and after a trailing dash, and before a
// leading ">" or "->" (which would end the comment early for XML parsers).
// Applying it to the data of a comment written that way changes nothing, so
// the comments of an input and its output compare equal.
func domComment(b *strings.Builder, depth int, data string) {
	b.WriteString(strings.Repeat("  ", depth))
	b.WriteString("<!--")
	if strings.HasPrefix(data, ">") || strings.HasPrefix(data, "->") {
		b.WriteByte(' ')
	}
	dash := false
	for i := 0; i < len(data); i++ {
		if data[i] == '-' && dash {
			b.WriteByte(' ')
		}
		b.WriteByte(data[i])
		dash = data[i] == '-'
	}
	if dash {
		b.WriteByte(' ')
	}
	b.WriteString("-->\n")
}

// isXMLDecl reports whether the data of a bogus comment is an XML
// declaration as the polyglot package recognizes one (see its isXMLDecl):
// "?xml" followed by whitespace, or "?xml?".
func isXMLDecl(data string) bool {
	return len(data) > 4 && strings.HasPrefix(data, "?xml") && (ascii.IsSpace(data[4]) || data == "?xml?")
}

// xmlNSCheck checks the namespace constraints of Namespaces in XML 1.0,
// which encoding/xml doesn't check: prefixes are declared (except xml and
// xmlns) and not undeclared, the xml prefix is only bound to the XML
// namespace, the xmlns prefix isn't declared, no other prefix (or the default
// namespace) is bound to the XML or XMLNS namespace, element names don't have
// the xmlns prefix, and no two attributes have the same namespace and local
// name.
func xmlNSCheck(s string) error {
	d := xml.NewDecoder(strings.NewReader(s))
	d.Strict = true
	var scopes [][]xml.Attr // the namespace declarations of each open element
	lookup := func(prefix string) (string, bool) {
		switch prefix {
		case "xml":
			return htmlstream.NamespaceXML, true
		case "xmlns":
			return htmlstream.NamespaceXMLNS, true
		}
		for i := len(scopes) - 1; i >= 0; i-- {
			for _, a := range scopes[i] {
				if a.Name.Local == prefix {
					return a.Value, true
				}
			}
		}
		return "", false
	}
	for {
		tok, err := d.RawToken()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		switch tok := tok.(type) {
		case xml.StartElement:
			var decls []xml.Attr
			for _, a := range tok.Attr {
				switch {
				case a.Name.Space == "xmlns":
					switch p, v := a.Name.Local, a.Value; {
					case p == "xmlns":
						return fmt.Errorf("namespace declaration of the xmlns prefix in <%s:%s>", tok.Name.Space, tok.Name.Local)
					case p == "xml" && v != htmlstream.NamespaceXML:
						return fmt.Errorf("xml prefix bound to %q", v)
					case p != "xml" && (v == "" || v == htmlstream.NamespaceXML || v == htmlstream.NamespaceXMLNS):
						return fmt.Errorf("prefix %s bound to %q", p, v)
					}
					decls = append(decls, a)
				case a.Name.Space == "" && a.Name.Local == "xmlns":
					if a.Value == htmlstream.NamespaceXML || a.Value == htmlstream.NamespaceXMLNS {
						return fmt.Errorf("default namespace bound to %q", a.Value)
					}
				}
			}
			scopes = append(scopes, decls)
			if strings.Contains(tok.Name.Local, ":") {
				return fmt.Errorf("element name %s:%s with more than one colon", tok.Name.Space, tok.Name.Local)
			}
			if tok.Name.Space == "xmlns" {
				return fmt.Errorf("element name with the xmlns prefix: %s:%s", tok.Name.Space, tok.Name.Local)
			}
			if _, ok := lookup(tok.Name.Space); tok.Name.Space != "" && !ok {
				return fmt.Errorf("undeclared prefix in element name %s:%s", tok.Name.Space, tok.Name.Local)
			}
			var expanded []string
			for _, a := range tok.Attr {
				if strings.Contains(a.Name.Local, ":") {
					return fmt.Errorf("attribute name %s:%s with more than one colon", a.Name.Space, a.Name.Local)
				}
				if a.Name.Space == "" {
					continue
				}
				uri, ok := lookup(a.Name.Space)
				if !ok {
					return fmt.Errorf("undeclared prefix in attribute name %s:%s", a.Name.Space, a.Name.Local)
				}
				k := uri + " " + a.Name.Local
				if slices.Contains(expanded, k) {
					return fmt.Errorf("attribute {%s}%s repeated in <%s>", uri, a.Name.Local, tok.Name.Local)
				}
				expanded = append(expanded, k)
			}
		case xml.EndElement:
			if len(scopes) != 0 {
				scopes = scopes[:len(scopes)-1]
			}
		}
	}
}

// xmlDOM returns the DOM of the XML document s like htmlDOMScripting, without
// the text outside the root element.
func xmlDOM(s string, space bool) (string, error) {
	if err := xmlNSCheck(s); err != nil {
		return "", err
	}
	d := xml.NewDecoder(strings.NewReader(s))
	d.Strict = true
	var b strings.Builder
	var text strings.Builder
	var stack []bool // whether each open element is skipped
	var names []string
	var defaultNS []string // the default namespace in each open element
	depth := 0
	flush := func() {
		t := text.String()
		text.Reset()
		if len(names) == 0 {
			return
		}
		if n := len(names); names[n-1] == "script" || names[n-1] == "style" {
			t = normalizeRawText(t, names[n-1] == "script")
		}
		domText(&b, depth, t, space)
	}
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return b.String(), err
		}
		switch tok := tok.(type) {
		case xml.StartElement:
			var attrs []string
			var metaAttrs [][2]string
			dns := ""
			if n := len(defaultNS); n != 0 {
				dns = defaultNS[n-1]
			}
			for _, a := range tok.Attr {
				k := a.Name.Local
				switch a.Name.Space {
				case "":
					if k == "xmlns" {
						dns = a.Value
					}
				case "xmlns":
					k = "xmlns:" + k
				case htmlstream.NamespaceXLink:
					k = "xlink:" + k
				case htmlstream.NamespaceXML, "xml":
					k = "xml:" + k
				default:
					k = a.Name.Space + ":" + k
				}
				if a.Name.Space == "" {
					metaAttrs = append(metaAttrs, [2]string{k, a.Value})
				}
				attrs = append(attrs, k+"="+a.Value)
			}
			defaultNS = append(defaultNS, dns)
			if tok.Name.Space == "" && tok.Name.Local == "xmlns" {
				// encoding/xml leaves an element named xmlns in no namespace,
				// unlike the namespaces spec
				tok.Name.Space = dns
			}
			_, drop := charsetDecl(metaAttrs)
			skip := tok.Name.Space == htmlstream.NamespaceXHTML && tok.Name.Local == "meta" && drop
			if !skip {
				// the text around a skipped element is one text node, as
				// in htmlDOMScripting
				flush()
				domElem(&b, depth, tok.Name.Space, tok.Name.Local, attrs)
				depth++
			}
			stack = append(stack, skip)
			name := tok.Name.Local
			if tok.Name.Space != htmlstream.NamespaceXHTML {
				// only the contents of HTML scripts and styles are
				// normalized, as in htmlDOMScripting
				name = ""
			}
			names = append(names, name)
		case xml.EndElement:
			if !stack[len(stack)-1] {
				flush()
				depth--
			}
			stack = stack[:len(stack)-1]
			names = names[:len(names)-1]
			defaultNS = defaultNS[:len(defaultNS)-1]
		case xml.Comment:
			if len(tok) != 0 {
				// x/net decodes character references in comments, unlike
				// the HTML5 spec
				flush()
				domComment(&b, depth, html.UnescapeString(string(tok)))
			}
		case xml.CharData:
			text.Write(tok)
		}
	}
	flush()
	return b.String(), nil
}
