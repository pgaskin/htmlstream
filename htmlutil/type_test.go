package htmlutil

import (
	"slices"
	"strings"
	"testing"

	"github.com/pgaskin/htmlstream"
)

func TestCategories(t *testing.T) {
	r := htmlstream.NewReader(strings.NewReader(`<br><img><DIV><b><title></title><svg><title></title><br/></svg></b></DIV><H2>h</H2><pre>p</pre><textarea></textarea><style></style>`))
	var got []string
	for r.Next() {
		tok := r.Token()
		if tok.Kind() != htmlstream.StartTag {
			continue
		}
		s := string(tok.Name()) + ":"
		for _, c := range []struct {
			name string
			fn   func(*htmlstream.Token) bool
		}{{"void", IsVoid[*htmlstream.Token]}, {"head", IsHeadContent[*htmlstream.Token]}, {"block", IsBlock[*htmlstream.Token]}, {"break", IsTextBreak[*htmlstream.Token]}, {"hidden", IsHidden[*htmlstream.Token]}, {"heading", IsHeading[*htmlstream.Token]}, {"pre", IsPreformatted[*htmlstream.Token]}, {"fmt", IsFormatting[*htmlstream.Token]}} {
			if c.fn(tok) {
				s += " " + c.name
			}
		}
		got = append(got, s)
	}
	want := []string{"br: void break", "img: void", "DIV: block break", "b: fmt", "title: head hidden", "svg:", "title:", "br: void break", "H2: block break heading", "pre: block break pre", "textarea: hidden pre", "style: head hidden"} // br breaks out of svg
	if !slices.Equal(got, want) {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestCategoriesRendering(t *testing.T) {
	// The categories based on the rendering section of the HTML Standard.
	r := htmlstream.NewReader(strings.NewReader(`<html><body><table><colgroup><col></colgroup><tr><td>a<th>b</table><select><option>o</select><ruby>r<rp>(</rp><rt>t</rt></ruby><datalist></datalist><button></button>`))
	var got []string
	for r.Next() {
		tok := r.Token()
		if tok.Kind() != htmlstream.StartTag {
			continue
		}
		s := string(tok.Name()) + ":"
		if IsBlock(tok) {
			s += " block"
		}
		if IsTextBreak(tok) {
			s += " break"
		}
		if IsHidden(tok) {
			s += " hidden"
		}
		got = append(got, s)
	}
	want := []string{"html: block break", "body: block break", "table: block break", "colgroup:", "col:", "tr: block break", "td: break", "th: break", "select:", "option: block break", "ruby:", "rp: hidden", "rt:", "datalist: hidden", "button:"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestScriptTypes(t *testing.T) {
	for _, tc := range []struct {
		tag      string
		typ      string
		js, json bool
	}{
		{`<script>`, "text/javascript", true, false},
		{`<script type="">`, "text/javascript", true, false},
		{`<script type=" Text/JavaScript&#10;">`, "text/javascript", true, false},
		{`<script type="text/javascript; charset=utf-8">`, "text/javascript; charset=utf-8", false, false},
		{`<script type=text/x-javascript>`, "text/x-javascript", true, false},
		{`<script type=MODULE>`, "module", true, false},
		{`<script type=" module ">`, "module", true, false},
		{`<script type=" &#9;">`, "", false, false},
		{`<script type="importmap ">`, "importmap", false, true},
		{`<script type="&#10;Application/LD+JSON ">`, "application/ld+json", false, true},
		{`<script type=" text/template">`, "text/template", false, false},
		{`<script language=JavaScript1.2>`, "text/javascript1.2", true, false},
		{`<script language=vbscript>`, "text/vbscript", false, false},
		{`<script language="">`, "text/javascript", true, false},
		{`<script type=module language=vbscript>`, "module", true, false},
		{`<script type=importmap>`, "importmap", false, true},
		{`<script type="application/ld+json">`, "application/ld+json", false, true},
		{`<script type="Application/JSON; charset=utf-8">`, "application/json; charset=utf-8", false, true},
		{`<script type="text/json">`, "text/json", false, true},
		{`<script type="foo/json">`, "foo/json", false, false},
		{`<script type="application/json/x">`, "application/json/x", false, false},
		{`<script type="text/template">`, "text/template", false, false},
	} {
		r := htmlstream.NewReader(strings.NewReader(tc.tag))
		r.Next()
		typ := ScriptType(r.Token())
		if typ != tc.typ || IsJavaScriptType(typ) != tc.js || IsJSONType(typ) != tc.json {
			t.Errorf("%s: got %q, js %v, json %v, want %q, %v, %v", tc.tag, typ, IsJavaScriptType(typ), IsJSONType(typ), tc.typ, tc.js, tc.json)
		}
	}
}

// TestCategoryAllocs checks that classifying tokens, open elements and names
// doesn't allocate.
func TestCategoryAllocs(t *testing.T) {
	r := htmlstream.NewReader(strings.NewReader(`<DIV><Pre>x`))
	for r.Next() && !r.Token().Is("pre") {
	}
	tok := r.Token()
	var e htmlstream.Element
	for e = range r.Stack() {
		break
	}
	upper := strings.ToUpper(string([]byte("textarea"))) // not a constant
	var n int
	for _, tc := range []struct {
		name string
		fn   func() bool
	}{
		{"Token", func() bool { return IsPreformatted(tok) && IsBlock(tok) && IsTextBreak(tok) }},
		{"Element", func() bool { return IsPreformatted(e) && IsBlock(e) && IsTextBreak(e) }},
		{"HTMLName", func() bool { return IsPreformatted(HTMLName("pre")) && IsBlock(HTMLName("pre")) }},
		{"HTMLName/runtime", func() bool { return IsPreformatted(HTMLName(upper)) && IsHidden(HTMLName(upper)) }},
	} {
		if !tc.fn() {
			t.Errorf("%s: not classified", tc.name)
		}
		if a := testing.AllocsPerRun(100, func() {
			if tc.fn() {
				n++
			}
		}); a != 0 {
			t.Errorf("%s: %v allocations", tc.name, a)
		}
	}
}

// customElement is a NamedElement other than the package's own types.
type customElement string

func (customElement) NS() string { return htmlstream.NamespaceXHTML }
func (e customElement) AppendLocalName(dst []byte) []byte {
	return append(dst, e...)
}

func TestNamedElement(t *testing.T) {
	if got := string(HTMLName("Pre\x00").AppendLocalName(nil)); got != "pre�" {
		t.Errorf("HTMLName.AppendLocalName: got %q", got)
	}
	if !IsPreformatted(HTMLName("PRE")) || IsPreformatted(HTMLName("pre\x00")) || !IsTextBreak(HTMLName("TD")) {
		t.Error("HTMLName not classified like the token")
	}
	if !IsBlock(customElement("div")) || IsBlock(customElement("span")) || !IsTextBreak(customElement("br")) {
		t.Error("customElement not classified")
	}
	// the open elements are classified like their start tags
	r := htmlstream.NewReader(strings.NewReader(`<DIV><b><svg><title><p></svg><H2><pre><textarea></textarea>`))
	for r.Next() {
		tok := r.Token()
		if tok.Kind() != htmlstream.StartTag || !tok.Opens() {
			continue
		}
		var e htmlstream.Element
		for e = range r.Stack() {
			break
		}
		for _, fn := range []struct {
			name string
			tok  func(*htmlstream.Token) bool
			e    func(htmlstream.Element) bool
		}{
			{"IsBlock", IsBlock[*htmlstream.Token], IsBlock[htmlstream.Element]},
			{"IsTextBreak", IsTextBreak[*htmlstream.Token], IsTextBreak[htmlstream.Element]},
			{"IsHidden", IsHidden[*htmlstream.Token], IsHidden[htmlstream.Element]},
			{"IsHeading", IsHeading[*htmlstream.Token], IsHeading[htmlstream.Element]},
			{"IsPreformatted", IsPreformatted[*htmlstream.Token], IsPreformatted[htmlstream.Element]},
			{"IsFormatting", IsFormatting[*htmlstream.Token], IsFormatting[htmlstream.Element]},
			{"IsSpecial", IsSpecial[*htmlstream.Token], IsSpecial[htmlstream.Element]},
		} {
			if fn.tok(tok) != fn.e(e) {
				t.Errorf("%v: %s: token %v, element %v", tok, fn.name, fn.tok(tok), fn.e(e))
			}
		}
	}
}

func TestCutCDATAWrapper(t *testing.T) {
	js := CDATALineComment | CDATABlockComment
	jsHTML, cssHTML := js|CDATAHTMLComment, CDATABlockComment|CDATAHTMLComment
	for _, tc := range []struct {
		in    string
		w     CDATAWrapper
		out   string
		found bool
	}{
		{"//<![CDATA[\na < b\n//]]>", js, "a < b", true},
		{"\n  // <![CDATA[  \n  a < b\n  // ]]>  \n", js, "  a < b", true},
		{"/*<![CDATA[*/\na\n/*]]>*/", js, "a", true},
		{"/* <![CDATA[ */ a /* ]]> */", js, "a", true},
		{"//<![CDATA[\r\na\r\n//]]>", js, "a", true},
		{"//<![CDATA[\n\na\n\n//]]>", js, "\na\n", true},
		{"//<![CDATA[\na\n/*]]>*/", js, "a", true},
		{"/*<![CDATA[*/\na\n//]]>", js, "a", true},
		{"//<![CDATA[\n//]]>", js, "", true},
		{"/*<![CDATA[*/\np{}\n/*]]>*/", CDATABlockComment, "p{}", true},
		{"//<![CDATA[\np{}\n//]]>", CDATABlockComment, "//<![CDATA[\np{}\n//]]>", false},
		{"/*<![CDATA[*/\np{}\n//]]>", CDATABlockComment, "/*<![CDATA[*/\np{}\n//]]>", false},
		{"//<![CDATA[ a();\nb\n//]]>", js, "//<![CDATA[ a();\nb\n//]]>", false},
		{"//<![CDATA[\na", js, "//<![CDATA[\na", false},
		{"a\n//]]>", js, "a\n//]]>", false},
		{"<![CDATA[a]]>", js, "<![CDATA[a]]>", false},
		{"//<![CDATA[\na\n]]>", js, "//<![CDATA[\na\n]]>", false},
		{"", js, "", false},
		{"//<![CDATA[\na\n//]]>\n//<![CDATA[\nb\n//]]>", js, "//<![CDATA[\na\n//]]>\n//<![CDATA[\nb\n//]]>", false},
		{"//<![CDATA[\n//<![CDATA[\na\n//]]>\n//]]>", js, "//<![CDATA[\n//<![CDATA[\na\n//]]>\n//]]>", false},
		{"x();\n//<![CDATA[\na\n//]]>", js, "x();\n//<![CDATA[\na\n//]]>", false},
		{"//<![CDATA[\na = '<![CDATA[';\n//]]>", js, "a = '<![CDATA[';", true},
		{"//<![CDATA[\na = ']]]]><![CDATA[>';\n//]]>", js, "a = ']]]]><![CDATA[>';", true},
		{"//<![CDATA[\n]]]]]]><![CDATA[><![CDATA[>\n//]]>", js, "]]]]]]><![CDATA[><![CDATA[>", true},
		{"//<![CDATA[\na]]><![CDATA[b\n//]]>", js, "//<![CDATA[\na]]><![CDATA[b\n//]]>", false},
		{"/*<![CDATA[*/\n]]>]]]]><![CDATA[>\n/*]]>*/", js, "/*<![CDATA[*/\n]]>]]]]><![CDATA[>\n/*]]>*/", false},

		// CDATAHTMLComment
		{"\n<!--//--><![CDATA[//><!--\na < b\n//--><!]]>\n", jsHTML, "a < b", true},
		{"\n<!--//--><![CDATA[//><!--\na < b\n//--><!]]>\n", js, "\n<!--//--><![CDATA[//><!--\na < b\n//--><!]]>\n", false},
		{"\n<!--/*--><![CDATA[/*><!--*/\np{}\n/*]]>*/-->\n", cssHTML, "p{}", true},
		{"\n<!--/*--><![CDATA[/*><!--*/\na < b\n/*]]>*/-->\n", jsHTML, "a < b", true},
		{"<!--/*--><![CDATA[/*><!--*/\np{}\n/*]]>*/-->", CDATABlockComment, "<!--/*--><![CDATA[/*><!--*/\np{}\n/*]]>*/-->", false},
		{"<!--//--><![CDATA[//><!--\np{}\n//--><!]]>", cssHTML, "<!--//--><![CDATA[//><!--\np{}\n//--><!]]>", false},
		{"<!--/*--><![CDATA[/*><!--*/\np{}\n//--><!]]>", cssHTML, "<!--/*--><![CDATA[/*><!--*/\np{}\n//--><!]]>", false},
		{"<!--//--><![CDATA[//><!--\n//--><!]]>", jsHTML, "", true},
		{"<!--//--><![CDATA[//><!--\r\n  a\r\n  //--><!]]>", jsHTML, "  a", true},
		{"<!--//--><![CDATA[//><!-- a();\nb\n//--><!]]>", jsHTML, "<!--//--><![CDATA[//><!-- a();\nb\n//--><!]]>", false},
		{"<!--/*--><![CDATA[/*><!--*/ p{}\n/*]]>*/-->", cssHTML, "<!--/*--><![CDATA[/*><!--*/ p{}\n/*]]>*/-->", false},
		{"<!--//--><![CDATA[//><!--\na; //--><!]]>", jsHTML, "<!--//--><![CDATA[//><!--\na; //--><!]]>", false},
		{"<!--/*--><![CDATA[/*><!--*/\np{} /*]]>*/-->", cssHTML, "<!--/*--><![CDATA[/*><!--*/\np{} /*]]>*/-->", false},
		{"<!-- //--><![CDATA[//><!--\na\n//--><!]]>", jsHTML, "<!-- //--><![CDATA[//><!--\na\n//--><!]]>", false},
		{"<!--//--><![CDATA[//><!--\na\n//]]>", jsHTML, "a", true},
		{"//<![CDATA[\na\n//--><!]]>", jsHTML, "a", true},
		{"/*<![CDATA[*/\n/*]]>*/-->", cssHTML, "", true},
		{"/*<![CDATA[*/ /*]]>*/-->", cssHTML, "/*<![CDATA[*/ /*]]>*/-->", false},
		{"<!--//--><![CDATA[//><!--\na = ']]]]><![CDATA[>';\n//--><!]]>", jsHTML, "a = ']]]]><![CDATA[>';", true},
		{"<!--//--><![CDATA[//><!--\ndocument.write('<SCRIPT>x<\\/script>');\n//--><!]]>", jsHTML, "<!--//--><![CDATA[//><!--\ndocument.write('<SCRIPT>x<\\/script>');\n//--><!]]>", false},
		{"//<![CDATA[\ndocument.write('<SCRIPT>x<\\/script>');\n//--><!]]>", jsHTML, "//<![CDATA[\ndocument.write('<SCRIPT>x<\\/script>');\n//--><!]]>", false},
		{"//<![CDATA[\ndocument.write('<SCRIPT>x<\\/script>');\n//]]>", jsHTML, "document.write('<SCRIPT>x<\\/script>');", true},
		{"<!--/*--><![CDATA[/*><!--*/\ndocument.write('<script>x<\\/script>');\n/*]]>*/-->", jsHTML, "<!--/*--><![CDATA[/*><!--*/\ndocument.write('<script>x<\\/script>');\n/*]]>*/-->", false},
		{"/*<![CDATA[*/\ndocument.write('<script>x<\\/script>');\n/*]]>*/-->", jsHTML, "/*<![CDATA[*/\ndocument.write('<script>x<\\/script>');\n/*]]>*/-->", false},

		// CDATABare
		{"\n<![CDATA[\na < b\n]]>\n", js | CDATABare, "a < b", true},
		{" <![CDATA[ a < b ]]> ", CDATABare, "a < b", true},
		{"<![CDATA[a]]>", CDATABlockComment | CDATABare, "a", true},
		{"<![CDATA[]]>", CDATABare, "", true},
		{"<![CDATA[\n\na\n\n]]>", CDATABare, "\na\n", true},
		{"<![CDATA[\na\n//]]>", js | CDATABare, "a", true},
		{"<![CDATA[\na\n//]]>", CDATABare, "a\n//", true},
		{"//<![CDATA[\na\n]]>", js | CDATABare, "a", true},
		{"/*<![CDATA[*/\np{}\n]]>", CDATABlockComment | CDATABare, "p{}", true},
		{"<![CDATA[\np{}\n/*]]>*/", CDATABlockComment | CDATABare, "p{}", true},
		{"<![CDATA[\na\n//--><!]]>", jsHTML | CDATABare, "a", true},
		{"<![CDATA[a]]]]><![CDATA[>b]]>", CDATABare, "a]]]]><![CDATA[>b", true},
		{"<![CDATA[a]]>b]]>", CDATABare, "<![CDATA[a]]>b]]>", false},
		{"<![CDATA[a]]><![CDATA[b]]>", CDATABare, "<![CDATA[a]]><![CDATA[b]]>", false},
		{"x <![CDATA[a]]>", CDATABare, "x <![CDATA[a]]>", false},
		{"<![CDATA[a]]> x", CDATABare, "<![CDATA[a]]> x", false},
		{"<![CDATA[a", CDATABare, "<![CDATA[a", false},
		{"<![cdata[a]]>", CDATABare, "<![cdata[a]]>", false},
	} {
		if out, found := CutCDATAWrapper(tc.in, tc.w); out != tc.out || found != tc.found {
			t.Errorf("%q, %d: got %q, %v, want %q, %v", tc.in, tc.w, out, found, tc.out, tc.found)
		}
		if out, found := CutCDATAWrapper([]byte(tc.in), tc.w); string(out) != tc.out || found != tc.found {
			t.Errorf("%q, %d: []byte: got %q, %v, want %q, %v", tc.in, tc.w, out, found, tc.out, tc.found)
		}
	}
}

func TestIsVoidImage(t *testing.T) {
	// HTML parsers turn image into img; under XML rules it is an ordinary
	// element. Read as HTML in Universal mode, with XML syntax, the void
	// element opens one.
	var got []bool
	for _, in := range []string{`<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml"><image></image></html>`, `<image>`} {
		r := htmlstream.NewReader(strings.NewReader(in))
		r.SetMode(htmlstream.Universal)
		for r.Next() {
			if tok := r.Token(); tok.Kind() == htmlstream.StartTag && tok.Is("image") {
				got = append(got, IsVoid(tok), tok.Opens())
			}
		}
	}
	if want := []bool{false, true, true, true}; !slices.Equal(got, want) {
		t.Errorf("IsVoid, Opens: got %v, want %v", got, want)
	}
	if !IsVoid(HTMLName("image")) {
		t.Errorf("IsVoid(HTMLName(image)) is false")
	}
}
