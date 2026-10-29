package htmlstream

import "testing"

func TestAppend(t *testing.T) {
	for _, tc := range []struct{ got, want string }{
		{string(AppendEscapeText(nil, []byte(`<a & "b">`))), `&lt;a &amp; "b"&gt;`},
		{string(AppendEscapeText(nil, "a\r\nb\r")), "a&#13;\nb&#13;"},
		{string(AppendEscapeAttr(nil, []byte("<a & \"b'\t\n"))), `&lt;a &amp; &quot;b&#39;&#9;&#10;`},
		{string(AppendStartTag(nil, "a", false, "href", "x&y", "title", `"`)), `<a href="x&amp;y" title="&quot;">`},
		{string(AppendStartTag(nil, "br", true)), `<br />`},
		{string(AppendElement(nil, "title", "a < b")), `<title>a &lt; b</title>`},
		{string(AppendComment(nil, []byte("> a -- b ---"))), `<!-- > a - - b - - - -->`},
		{string(AppendComment(nil, "->x")), `<!-- ->x-->`},
		{string(AppendEndTag(nil, "a")), `</a>`},
		{string(AppendEscapeAttr(nil, "a\rb\u00e9")), "a&#13;b\u00e9"},
		{string(AppendElement(nil, "p", []byte("&"))), `<p>&amp;</p>`},
		{string(AppendComment(nil, ">-")), `<!-- >- -->`},
		{string(AppendCDATA(nil, "")), `<![CDATA[]]>`},
		{string(AppendCDATA(nil, "a<b&]]")), `<![CDATA[a<b&]]]]>`},
		{string(AppendCDATA(nil, []byte("]]>]]]>>"))), `<![CDATA[]]]]><![CDATA[>]]]]]><![CDATA[>>]]>`},
		{string(AppendProcInst(nil, "pi", "")), `<?pi?>`},
		{string(AppendProcInst(nil, "pi", []byte("a?>b?"))), `<?pi a? >b??>`},
		{string(AppendProcInst(nil, "xml-stylesheet", `href="a.xsl"`)), `<?xml-stylesheet href="a.xsl"?>`},
	} {
		if tc.got != tc.want {
			t.Errorf("got %q, want %q", tc.got, tc.want)
		}
	}
	if b, changed := AppendXMLChars(nil, []byte("a\x01\fb\xffc")); !changed || string(b) != "a\uFFFD b\uFFFDc" {
		t.Errorf("AppendXMLChars: %q %v", b, changed)
	}
	if b, changed := AppendXMLChars([]byte("x"), "\u00e9\U0001F600\xed\xa0\x80\xf0\x9f"); !changed || string(b) != "x\u00e9\U0001F600\uFFFD\uFFFD\uFFFD\uFFFD\uFFFD" {
		t.Errorf("AppendXMLChars: %q %v", b, changed)
	}
	if b, changed := AppendXMLChars(nil, "\u00e9\t"); changed || string(b) != "\u00e9\t" {
		t.Errorf("AppendXMLChars: %q %v", b, changed)
	}
}

// TestAppendAllocs checks that the Append functions don't allocate when dst
// has room, for strings and byte slices.
func TestAppendAllocs(t *testing.T) {
	const s = "a < b & \"c\"\r\né \x01 -- end"
	bs := []byte(s)
	buf := make([]byte, 0, 1024)
	if n := testing.AllocsPerRun(100, func() {
		b := AppendEscapeText(buf[:0], s)
		b = AppendEscapeText(b, bs)
		b = AppendEscapeAttr(b, s)
		b = AppendEscapeAttr(b, bs)
		b = AppendStartTag(b, "a", false, "href", s)
		b = AppendElement(b, "p", bs, "title", s)
		b = AppendComment(b, s)
		b = AppendComment(b, bs)
		b = AppendCDATA(b, s+"]]>")
		b = AppendCDATA(b, bs)
		b = AppendProcInst(b, "pi", s+"?>")
		b = AppendProcInst(b, "pi", bs)
		b, _ = AppendXMLChars(b, s)
		b, _ = AppendXMLChars(b, bs)
		_ = ValidXMLChars(s) && ValidXMLChars(bs)
	}); n != 0 {
		t.Errorf("%v allocations", n)
	}
}
