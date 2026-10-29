package htmlutil

import (
	"strings"
	"testing"

	"github.com/pgaskin/htmlstream"
)

func TestText(t *testing.T) {
	if got := CollapseSpace("  a \n\t b\u00a0 c  "); got != "a b\u00a0 c" {
		t.Errorf("CollapseSpace: %q", got)
	}
	for _, tc := range []struct {
		in    string
		opt   TextOptions
		want  string
		trunc bool
	}{
		{`<section>Hello <b>big</b>
		world<br>!<script>x</script><img alt="pic"><div>block</div>end</section>`, TextOptions{}, "Hello big world\n!\nblock\nend", false},
		{`<p>a<img alt="pic">b</p>`, TextOptions{AltText: true}, "apicb", false},
		{`<p>a<image alt="pic">b<svg><image alt="no"/></svg></p>`, TextOptions{AltText: true}, "apicb", false},
		{`<p> a  b <style>s</style></p>`, TextOptions{KeepWhitespace: true}, " a  b ", false},
		{`<p> a  b <style>s</style></p>`, TextOptions{Hidden: true}, "a b s", false},
		{`<p>abcdef€</p>`, TextOptions{Max: 7}, "abcdef", true},
		{`<p>abcdef</p>`, TextOptions{Max: 6}, "abcdef", false},
		{`<p>abc<b>def</b> <i> </i></p>`, TextOptions{Max: 6}, "abcdef", false},
		{`<p>abc<b>def</b> <i>g</i></p>`, TextOptions{Max: 6}, "abcdef", true},
		{`<p>abcdef<br></p>`, TextOptions{Max: 6}, "abcdef", true},
		{`<p>abcdef </p>`, TextOptions{Max: 6, KeepWhitespace: true}, "abcdef", true},
		{`<div>a<p>b</p><p>c</p>d<br>e</div>`, TextOptions{}, "a\n\nb\n\nc\n\nd\ne", false},
		{"<div>a <pre>\n x  y\n</pre> <listing>\n\nz</listing></div>", TextOptions{}, "a\n x  y\n\n\nz", false},
		{"<pre>\nx  y</pre>", TextOptions{}, "x  y", false},
		{"<div><pre></>\nx</pre><pre><!---->\ny</pre></div>", TextOptions{}, "x\n\ny", false},
		{"<div>a <textarea>\n t </textarea> b</div>", TextOptions{}, "a b", false},
		{"<div>a <textarea>\n t </textarea> b</div>", TextOptions{Hidden: true}, "a  t  b", false},
		{`<div><table><tr><td>a<td> b </td></tr><tr><th>c</table>d</div>`, TextOptions{}, "a\tb\nc\nd", false},
		{`<div>a<template>t</template>b</div>`, TextOptions{Hidden: true}, "ab", false},
		{`<template>t</template>`, TextOptions{KeepWhitespace: true}, "", false},
		{`<div>a</br>b</div>`, TextOptions{}, "a\nb", false},
		{`<div>a<svg><p>x</p><desc>y</desc></svg>b</div>`, TextOptions{}, "a\n\nx\n\nyb", false},
		{`<div>a<span hidden>h</span><span hidden=UNTIL-FOUND>u</span><dialog>d</dialog><dialog open>o</dialog><div popover>p</div><dialog open popover>q</dialog><datalist><option>l</datalist><ruby>r<rp>(</rp><rt>t</rt><rp>)</rp></ruby></div>`, TextOptions{}, "au\no\nq\nrt", false},
		{`<div>a<span hidden>h</span><dialog>d</dialog></div>`, TextOptions{Hidden: true}, "ah\nd", false},
		{`<div hidden> a <b>b</b><script>s</script></div>`, TextOptions{}, " a bs", false},
		{`<script> x  y </script>`, TextOptions{}, " x  y ", false},
		{`<table><tr><td>a<table><tr><td>x<td>y</table><td>b</table>`, TextOptions{}, "a\nx\ty\n\tb", false},
		{`<table><thead><tr><td>a</thead><tbody><tr><td>b<tr><td>c</table>`, TextOptions{}, "a\nb\nc", false},
		{`<table><tr><td><p>a</p><tr><td>b</table>`, TextOptions{}, "a\n\n\nb", false},
		{`<div><select><option>a<option>b</select></div>`, TextOptions{}, "a\nb", false},
		{`<div>a<div hidden=until-found>d</div><li hidden=until-found>l</li><button hidden=until-found>b</button><table><tr hidden=until-found><td>r<td hidden=until-found>c</table>z</div>`, TextOptions{}, "a\nr\nz", false},
		{`<div>a<video>v</video><audio>u</audio><canvas>c</canvas><progress>p</progress><meter>m</meter><object>o</object>b</div>`, TextOptions{}, "acob", false},
		{`<div>a<canvas>c</canvas>b</div>`, TextOptions{Hidden: true}, "acb", false},
		{`<div>a<details>t<summary>S</summary>u<summary>T</summary>v</details>b</div>`, TextOptions{}, "a\nS\nb", false},
		{`<div>a<details><p>x</p></details>b</div>`, TextOptions{}, "a\nb", false},
		{`<div>a<details><span><summary>S</summary></span>x</details>b</div>`, TextOptions{}, "a\nb", false},
		{`<div>a<details><summary>S<details><summary>N</summary>n</details></summary>x</details>b</div>`, TextOptions{}, "a\nS\nN\nb", false},
		{`<div>a<details open><summary>S</summary>x</details>b</div>`, TextOptions{}, "a\nS\nx\nb", false},
		{`<div>a<details><summary>S</summary>x</details>b</div>`, TextOptions{Hidden: true}, "a\nS\nx\nb", false},
		{`<details>t<summary>S</summary>x</details>`, TextOptions{}, "S", false},
		{`<div>a<svg><text>t</text><style>s{}</style><script>sc</script><title>ti</title><desc>d</desc><metadata>m</metadata></svg>b</div>`, TextOptions{}, "atb", false},
		{`<div>a<svg><g><title>ti</title><text>t</text></g><defs><text>dt</text></defs><symbol><text>st</text></symbol><switch><text>t1</text><text>t2</text></switch></svg>b</div>`, TextOptions{}, "att1t2b", false},
		{`<div>a<svg><foreignObject><div>f<svg><title>t</title></svg>g</div></foreignObject><defs><foreignObject><div>d</div></foreignObject></defs></svg>b</div>`, TextOptions{}, "a\nfg\nb", false},
		{`<div>a<svg><title>ti</title><defs><text>dt</text></defs></svg>b</div>`, TextOptions{Hidden: true}, "atidtb", false},
		{`<div>a<math><semantics>t<mi>x</mi>u<annotation>an</annotation>v</semantics></math>b</div>`, TextOptions{}, "atxuvb", false},
		{`<div>a<math><semantics><annotation>an</annotation><mi>x</mi></semantics></math>b</div>`, TextOptions{}, "aanb", false},
		{`<div>a<math><semantics><mrow><semantics><mi>x</mi><mi>y</mi></semantics><mi>w</mi></mrow><mi>z</mi></semantics></math>b</div>`, TextOptions{}, "axwb", false},
		{`<div>a<math><semantics><mi>x</mi><annotation-xml encoding=text/html><p>ax</p></annotation-xml></semantics></math>b</div>`, TextOptions{}, "axb", false},
		{`<div>a<math><semantics><mi>x</mi><annotation>an</annotation></semantics></math>b</div>`, TextOptions{Hidden: true}, "axanb", false},
		{`<math><semantics><mi>x</mi><mi>y</mi></semantics></math>`, TextOptions{}, "x", false},
		{`<div>a<math><maction actiontype=toggle>t<mi>x</mi>u<mi>y</mi>v</maction></math>b</div>`, TextOptions{}, "atxuvb", false},
		{`<div>a<svg><clipPath><text>c</text></clipPath><mask><text>m</text></mask><marker><text>k</text></marker><pattern><text>p</text></pattern><linearGradient><text>l</text></linearGradient><radialGradient><text>r</text></radialGradient><filter><text>f</text></filter><text>t</text></svg>b</div>`, TextOptions{}, "atb", false},
		{`<div>a<svg><CLIPPATH><text>c</text></CLIPPATH><Filter><text>f</text></Filter></svg>b</div>`, TextOptions{}, "ab", false},
		{`<div>a<svg><clipPath><text>c</text></clipPath><filter><text>f</text></filter></svg>b</div>`, TextOptions{Hidden: true}, "acfb", false},
		{"<div>a\x00b<svg><text>c\x00d<![CDATA[e\x00]]></text><foreignObject>f\x00<p>\x00g</p><![CDATA[h\x00]]></foreignObject></svg><math><mi>i\x00</mi><mrow>j\x00</mrow><annotation-xml encoding=text/html>k\x00</annotation-xml></math></div>", TextOptions{}, "abc\ufffdde\ufffdf\n\ng\n\nhij\ufffdk", false},
		{"<div><pre>\x00\nk</pre><listing>\x00\x00\n\nl</listing><pre>\x00 \nm</pre><textarea>\x00\nn\x00</textarea></div>", TextOptions{Hidden: true}, "k\n\nl\n \nm\n\ufffd\nn\ufffd", false},
	} {
		r := htmlstream.NewReader(strings.NewReader(tc.in))
		r.Next()
		if got, trunc := InnerText(r, tc.opt); got != tc.want || trunc != tc.trunc {
			t.Errorf("%q %+v: got %q, %v, want %q, %v", tc.in, tc.opt, got, trunc, tc.want, tc.trunc)
		}
		if tok := r.Token(); tok == nil || tok.Kind() != htmlstream.EndTag || tok.StartPos() != 0 {
			t.Errorf("%q: current token %v", tc.in, tok)
		}
	}

	// Under XML rules (XHTML with an XML declaration in Universal mode), br
	// may have an end tag, and the newline after a pre start tag is kept. A
	// stray </br> is a br only under HTML rules. SVG and MathML elements are
	// those in their namespaces, and SVG names are case-sensitive.
	r := htmlstream.NewReader(strings.NewReader("<?xml version=\"1.0\"?><div xmlns=\"http://www.w3.org/1999/xhtml\">a<br></br>b<br/>c<br>d</br>e<pre>\nx</pre><svg xmlns=\"http://www.w3.org/2000/svg\"><title>t</title><text>s</text><clipPath>c</clipPath><clippath>C</clippath></svg><m:math xmlns:m=\"http://www.w3.org/1998/Math/MathML\"><m:semantics><m:mi>m</m:mi><m:annotation>n</m:annotation></m:semantics></m:math><desc>T</desc></div>"))
	r.SetMode(htmlstream.Universal)
	r.Next()
	r.Next()
	if got, _ := InnerText(r, TextOptions{}); got != "a\nb\nc\nde\n\nx\nsCmT" {
		t.Errorf("XHTML in Universal mode: got %q", got)
	}
	r = htmlstream.NewReader(strings.NewReader("<div>f</BR>g</div>"))
	r.SetMode(htmlstream.Universal)
	r.Next()
	if got, _ := InnerText(r, TextOptions{}); got != "f\ng" {
		t.Errorf("Universal mode: got %q", got)
	}
	// NUL is kept under XML rules.
	r = htmlstream.NewReader(strings.NewReader("<div>a\x00b</div>"))
	r.SetMode(htmlstream.XML)
	r.Next()
	if got, _ := InnerText(r, TextOptions{}); got != "a\x00b" {
		t.Errorf("NUL in XML mode: got %q", got)
	}
	// A NUL in its own token is left out like in the same token, and so is
	// the newline after it at the start of pre.
	for chunk := 1; chunk <= 8; chunk++ {
		r = htmlstream.NewReader(strings.NewReader("<pre>\x00\x00\nx\x00\x00\ny</pre>"))
		r.SetChunkSize(chunk)
		r.Next()
		if got, _ := InnerText(r, TextOptions{}); got != "x\ny" {
			t.Errorf("NUL in pre, chunk size %d: got %q", chunk, got)
		}
	}
	// With XML syntax, so may void elements in HTML mode, also after
	// whitespace.
	r = htmlstream.NewReader(strings.NewReader("<div>a<br></br>b<br/>c<BR>d</br>e<br>\n</br>f<br> <!----></br>g</div>"))
	r.SetXMLSyntax(true)
	r.Next()
	if got, _ := InnerText(r, TextOptions{}); got != "a\nb\nc\nd\ne\nf\n\ng" {
		t.Errorf("XML syntax: got %q", got)
	}
}

func TestInnerTextNoscript(t *testing.T) {
	// noscript and canvas fallback content are rendered with scripting
	// disabled.
	for _, scripting := range []bool{true, false} {
		r := htmlstream.NewReader(strings.NewReader(`<div>a<noscript><b>n</b></noscript><canvas>c</canvas>b</div>`))
		r.SetScripting(scripting)
		r.Next()
		want := "ab"
		if !scripting {
			want = "ancb"
		}
		if got, _ := InnerText(r, TextOptions{}); got != want {
			t.Errorf("scripting %v: got %q, want %q", scripting, got, want)
		}
	}
}

func TestInnerTextUntracked(t *testing.T) {
	// The hidden script is untracked, and skipped like a tracked one.
	r := htmlstream.NewReader(strings.NewReader(`<div><p>a<script>s</script>b</p>c</div>d`))
	r.SetMaxDepth(2)
	r.Next()
	if got, _ := InnerText(r, TextOptions{}); got != "ab\n\nc" || r.Err() != nil {
		t.Errorf("got %q, %v", got, r.Err())
	}
	if r.Next(); string(r.Token().Raw()) != "d" {
		t.Errorf("after InnerText: %v", r.Token())
	}
}

func TestTextChildrenOnly(t *testing.T) {
	r := htmlstream.NewReader(strings.NewReader(`<div>a<b>b</b>c<![CDATA[x]]></div>`))
	r.SetMode(htmlstream.XML)
	r.Next()
	if got, _ := InnerText(r, TextOptions{ChildrenOnly: true, KeepWhitespace: true}); got != "acx" {
		t.Errorf("got %q", got)
	}
	var tb TextBuilder
	r = htmlstream.NewReader(strings.NewReader("a &amp;\n b"))
	for r.Next() {
		tb.WriteText(r.Token())
	}
	if tb.String() != "a & b" {
		t.Errorf("WriteText: %q", tb.String())
	}
}

func TestTextContent(t *testing.T) {
	for _, tc := range []struct {
		in    string
		max   int
		want  string
		trunc bool
	}{
		{"<div> a <script>s</script><p>b</p><template>t</template><pre>\nc</pre>\n</div>", 0, " a sbc\n", false},
		{"<div>a<!--c-->b<img alt=x></div>", 0, "ab", false},
		{"<div>abc<b>def</b></div>", 4, "abcd", true},
		{"<div><svg><title>t</title><defs>d</defs></svg><math><semantics><mi>x</mi><annotation>a</annotation></semantics></math></div>", 0, "tdxa", false},
		{"<div>a\x00b<pre>\x00\nc</pre><svg><text>d\x00</text><desc><![CDATA[e\x00f]]></desc></svg><script>g\x00</script></div>", 0, "abcd\ufffdefg\ufffd", false},
	} {
		r := htmlstream.NewReader(strings.NewReader(tc.in))
		r.Next()
		if got, trunc := TextContent(r, tc.max); got != tc.want || trunc != tc.trunc {
			t.Errorf("%q: got %q, %v, want %q, %v", tc.in, got, trunc, tc.want, tc.trunc)
		}
	}
}

// TestInnerTextTransformer checks that InnerText on the Reader of a
// Transformer writes the tokens it reads.
func TestInnerTextTransformer(t *testing.T) {
	const in = `<p>a <b>b</b></p><i>x</i>`
	var b strings.Builder
	x := htmlstream.NewTransformer(&b, strings.NewReader(in))
	var got string
	for x.Next() {
		tok := x.Token()
		switch {
		case tok.Kind() == htmlstream.StartTag && tok.Is("p"):
			got, _ = InnerText(x.Reader(), TextOptions{})
			if err := x.InsertBefore([]byte("<!--p-->")); err != nil {
				t.Fatal(err)
			}
		case tok.Kind() == htmlstream.StartTag && tok.Is("i"):
			if err := x.SetAttr("c", "1"); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := x.Close(); err != nil {
		t.Fatal(err)
	}
	if got != "a b" {
		t.Errorf("InnerText: got %q", got)
	}
	if want := `<p>a <b>b</b><!--p--></p><i c="1">x</i>`; b.String() != want {
		t.Errorf("got %q, want %q", b.String(), want)
	}
}
