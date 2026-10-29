package xmlutil

import (
	"encoding/binary"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/pgaskin/htmlstream"
	xmltest_testdata "github.com/pgaskin/htmlstream/testdata/xmltest"
)

func TestParseDecl(t *testing.T) {
	for _, tc := range []struct {
		in  string
		d   Decl
		n   int    // -1 for the length of in
		err string // empty if none
	}{
		{`<?xml version="1.0"?>`, Decl{Version: "1.0"}, -1, ""},
		{`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><a/>`, Decl{"1.0", "UTF-8", "yes"}, 55, ""},
		{"<?xml\tversion = '1.1'\r\nencoding\n=\"x-y_z.1\" standalone='no' ?>", Decl{"1.1", "x-y_z.1", "no"}, -1, ""},
		{`<?xml encoding="EUC-JP"?>`, Decl{Encoding: "EUC-JP"}, -1, ""},
		{`<?xml version="1.0"?>?>`, Decl{Version: "1.0"}, 21, ""},

		// not a declaration
		{``, Decl{}, 0, "xmlutil: no XML declaration"},
		{`<?xml-stylesheet href="a"?>`, Decl{}, 0, "xmlutil: no XML declaration"},
		{`<?XML version="1.0"?>`, Decl{}, 0, "xmlutil: no XML declaration"},
		{` <?xml version="1.0"?>`, Decl{}, 0, "xmlutil: no XML declaration"},
		{`<?xml?x version="1.1"?>`, Decl{}, 0, "xmlutil: no XML declaration"},

		// cut off
		{`<?xml`, Decl{}, 0, "xmlutil: XML declaration: declaration is cut off at offset 5"},
		{`<?xml?`, Decl{}, 0, "xmlutil: XML declaration: declaration is cut off at offset 5"},
		{`<?xml version="1.0"?`, Decl{Version: "1.0"}, 0, "xmlutil: XML declaration: declaration is cut off at offset 19"},
		{`<?xml version="1.0" encoding="UTF`, Decl{Version: "1.0"}, 0, "xmlutil: XML declaration: declaration is cut off at offset 33"},
		{`<?xml version="1.0" encoding=`, Decl{Version: "1.0"}, 0, "xmlutil: XML declaration: declaration is cut off at offset 29"},
		{`<?xml version="1.0">`, Decl{Version: "1.0"}, 0, `xmlutil: XML declaration: unexpected '>' at offset 19`},

		// malformed, read leniently
		{`<?xml?>`, Decl{}, -1, "xmlutil: XML declaration: missing version at offset 5"},
		{`<?xml standalone="yes" encoding="UTF-8"?>`, Decl{"", "UTF-8", "yes"}, -1, `xmlutil: XML declaration: pseudo-attribute "encoding" is out of order or repeated at offset 23`},
		{`<?xml encoding="UTF-8" standalone="yes"?>`, Decl{"", "UTF-8", "yes"}, -1, "xmlutil: XML declaration: standalone without a version at offset 39"},
		{`<?xml version="1.0"encoding="UTF-8"?>`, Decl{"1.0", "UTF-8", ""}, -1, `xmlutil: XML declaration: missing whitespace before "encoding" at offset 19`},
		{`<?xml version=1.0 encoding=latin1?>`, Decl{"1.0", "latin1", ""}, -1, `xmlutil: XML declaration: value of "version" isn't quoted at offset 14`},
		{`<?xml vers="1.0" encoding="latin1"?>`, Decl{"", "latin1", ""}, -1, `xmlutil: XML declaration: unknown pseudo-attribute "vers" at offset 6`},
		{`<?xml version="1.0" version="2.0"?>`, Decl{"1.0", "", ""}, -1, `xmlutil: XML declaration: pseudo-attribute "version" is out of order or repeated at offset 20`},
		{`<?xml version="2.0" encoding="UTF 8" standalone="maybe"?>`, Decl{"2.0", "UTF 8", "maybe"}, -1, `xmlutil: XML declaration: invalid version "2.0" at offset 6`},
		{`<?xml version="1.0" encoding="-x"?>`, Decl{"1.0", "-x", ""}, -1, `xmlutil: XML declaration: invalid encoding "-x" at offset 20`},
		{`<?xml version="1." ?>`, Decl{"1.", "", ""}, -1, `xmlutil: XML declaration: invalid version "1." at offset 6`},
		{`<?xml version="1.0" "x" encoding='y'?>`, Decl{"1.0", "y", ""}, -1, `xmlutil: XML declaration: unexpected '"' at offset 20`},
		{`<?xml version ?>`, Decl{}, -1, `xmlutil: XML declaration: missing "=" after "version" at offset 14`},
	} {
		d, n, err := ParseDecl([]byte(tc.in))
		if tc.n < 0 {
			tc.n = len(tc.in)
		}
		var msg string
		if err != nil {
			msg = err.Error()
		}
		if d != tc.d || n != tc.n || msg != tc.err {
			t.Errorf("%q: got %+v, %d, %q\nwant %+v, %d, %q", tc.in, d, n, msg, tc.d, tc.n, tc.err)
		}
		var derr *DeclError
		if err != nil && err != ErrNoDecl && !errors.As(err, &derr) {
			t.Errorf("%q: error %T", tc.in, err)
		}
	}
}

func TestAppendDecl(t *testing.T) {
	for _, tc := range []struct {
		d    Decl
		want string
	}{
		{Decl{"1.0", "UTF-8", "yes"}, `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`},
		{Decl{Version: "1.0"}, `<?xml version="1.0"?>`},
		{Decl{Encoding: "UTF-8"}, `<?xml encoding="UTF-8"?>`},
	} {
		if got := string(AppendDecl([]byte("x"), tc.d)); got != "x"+tc.want {
			t.Errorf("%+v: got %q, want %q", tc.d, got, "x"+tc.want)
		}
	}
}

// encode encodes the ASCII string s in an encoding with a code unit size of
// unit bytes, holding the character in the byte at sig.
func encode(s string, unit, sig int) []byte {
	var b []byte
	for _, c := range []byte(s) {
		u := make([]byte, unit)
		u[sig] = c
		b = append(b, u...)
	}
	return b
}

func TestDetectEncoding(t *testing.T) {
	const decl = `<?xml version="1.0" encoding="x-enc"?><a/>`
	utf16be := func(s string) []byte {
		var b []byte
		for _, c := range utf16.Encode([]rune(s)) {
			b = binary.BigEndian.AppendUint16(b, c)
		}
		return b
	}
	ebcdic := []byte{
		0x4C, 0x6F, 0xA7, 0x94, 0x93, 0x40, // <?xml
		0xA5, 0x85, 0x99, 0xA2, 0x89, 0x96, 0x95, 0x7E, 0x7F, 0xF1, 0x4B, 0xF0, 0x7F, 0x25, // version="1.0"\n
		0x85, 0x95, 0x83, 0x96, 0x84, 0x89, 0x95, 0x87, 0x7E, 0x7D, 0xC9, 0xC2, 0xD4, 0x60, 0xF0, 0xF3, 0xF7, 0x7D, // encoding='IBM-037'
		0x6F, 0x6E, 0x4C, 0x81, 0x61, 0x6E, // ?><a/>
	}
	for _, tc := range []struct {
		name string
		in   []byte
		want Encoding
	}{
		{"Empty", nil, Encoding{Name: "UTF-8"}},
		{"NoDecl", []byte("<a/>"), Encoding{Name: "UTF-8"}},
		{"NoEncoding", []byte(`<?xml version="1.0"?><a/>`), Encoding{Name: "UTF-8"}},
		{"Declared", []byte(`<?xml version="1.0" encoding="ISO-8859-2"?>`), Encoding{Name: "ISO-8859-2", Declared: "ISO-8859-2"}},
		{"Alias", []byte(`<?xml version='1.0' encoding='latin1'?>`), Encoding{Name: "windows-1252", Declared: "latin1"}},
		{"Unknown", []byte(decl), Encoding{Declared: "x-enc"}},
		{"Replacement", []byte(`<?xml version="1.0" encoding="ISO-2022-KR"?>`), Encoding{Name: "replacement", Declared: "ISO-2022-KR"}},
		{"UTF16InASCII", []byte(`<?xml version="1.0" encoding="UTF-16"?>`), Encoding{Name: "UTF-8", Declared: "UTF-16"}},
		{"Malformed", []byte(`<?xml encoding=Shift_JIS version="1.0"?>`), Encoding{Name: "Shift_JIS", Declared: "Shift_JIS"}},
		{"CutOff", []byte(`<?xml version="1.0" encoding="EUC-JP"`), Encoding{Name: "EUC-JP", Declared: "EUC-JP"}},
		{"Stylesheet", []byte(`<?xml-stylesheet encoding="EUC-JP"?>`), Encoding{Name: "UTF-8"}},
		{"UTF8BOM", []byte("\xEF\xBB\xBF" + `<?xml version="1.0" encoding="ISO-8859-2"?>`), Encoding{Name: "UTF-8", Declared: "ISO-8859-2", BOM: 3}},
		{"UTF16BEBOM", append([]byte{0xFE, 0xFF}, utf16be(decl)...), Encoding{Name: "UTF-16BE", Declared: "x-enc", BOM: 2}},
		{"UTF16LEBOM", append([]byte{0xFF, 0xFE}, encode(decl, 2, 0)...), Encoding{Name: "UTF-16LE", Declared: "x-enc", BOM: 2}},
		{"UTF16BEBOMOnly", []byte{0xFE, 0xFF}, Encoding{Name: "UTF-16BE", BOM: 2}},
		{"UTF16BEBOMText", append([]byte{0xFE, 0xFF}, utf16be("é<?xml version='1.0'?>")...), Encoding{Name: "UTF-16BE", BOM: 2}},
		{"UTF16BE", utf16be(decl), Encoding{Name: "UTF-16BE", Declared: "x-enc"}},
		{"UTF16LE", encode(decl, 2, 0), Encoding{Name: "UTF-16LE", Declared: "x-enc"}},
		{"UTF32BEBOM", append([]byte{0, 0, 0xFE, 0xFF}, encode(decl, 4, 3)...), Encoding{Name: "UTF-32BE", Declared: "x-enc", BOM: 4}},
		{"UTF32LEBOM", append([]byte{0xFF, 0xFE, 0, 0}, encode(decl, 4, 0)...), Encoding{Name: "UTF-32LE", Declared: "x-enc", BOM: 4}},
		{"UCS4_2143BOM", append([]byte{0, 0, 0xFF, 0xFE}, encode(decl, 4, 2)...), Encoding{Declared: "x-enc", BOM: 4}},
		{"UCS4_3412BOM", append([]byte{0xFE, 0xFF, 0, 0}, encode(decl, 4, 1)...), Encoding{Declared: "x-enc", BOM: 4}},
		{"UTF32BE", encode(decl, 4, 3), Encoding{Name: "UTF-32BE", Declared: "x-enc"}},
		{"UTF32LE", encode(decl, 4, 0), Encoding{Name: "UTF-32LE", Declared: "x-enc"}},
		{"UCS4_2143", encode(decl, 4, 2), Encoding{Declared: "x-enc"}},
		{"UCS4_3412", encode(decl, 4, 1), Encoding{Declared: "x-enc"}},
		{"UTF32BENoDecl", encode("<a/>", 4, 3), Encoding{Name: "UTF-32BE"}},
		{"EBCDIC", ebcdic, Encoding{Declared: "IBM-037"}},
		{"EBCDICCutOff", ebcdic[:30], Encoding{}},
	} {
		if got := DetectEncoding(tc.in); got != tc.want {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func TestASCIICompatible(t *testing.T) {
	b, err := os.ReadFile("../internal/encodings.json")
	if err != nil {
		t.Fatal(err)
	}
	var groups []struct {
		Encodings []struct {
			Name   string
			Labels []string
		}
	}
	if err := json.Unmarshal(b, &groups); err != nil {
		t.Fatal(err)
	}
	incompatible := map[string]bool{
		"UTF-16BE": true, "UTF-16LE": true, "ISO-2022-JP": true, "replacement": true,
		"UTF-32BE": true, "UTF-32LE": true, "": true,
		"x-enc": true, "utf-8": true, "IBM037": true,
	}
	names := []string{"UTF-32BE", "UTF-32LE", "", "x-enc", "utf-8", "IBM037"}
	for _, g := range groups {
		for _, e := range g.Encodings {
			names = append(names, e.Name)
		}
	}
	if len(names) < 40 {
		t.Fatalf("only %d names", len(names))
	}
	for _, name := range names {
		if got := (Encoding{Name: name}).ASCIICompatible(); got == incompatible[name] {
			t.Errorf("%q: got %v", name, got)
		}
	}
	// the names DetectEncoding returns for declarations
	for _, decl := range []string{"Shift_JIS", "euc-kr", "big5", "gb2312", "gb18030", "EUC-JP", "latin1", "x-user-defined", "UTF-16", "ISO-2022-JP", "ISO-2022-KR", "x-enc"} {
		e := DetectEncoding([]byte(`<?xml version="1.0" encoding="` + decl + `"?>`))
		if got, want := e.ASCIICompatible(), !incompatible[e.Name]; got != want {
			t.Errorf("%q (%+v): got %v", decl, e, got)
		}
	}
}

func TestAppendEscape(t *testing.T) {
	const in = "a&b<c>d\"e'f\tg\nh\r\ni]]>\u00e9"
	const want = "a&amp;b&lt;c&gt;d&quot;e'f&#9;g&#10;h&#13;&#10;i]]&gt;\u00e9"
	if got := string(AppendEscape([]byte("x"), in)); got != "x"+want {
		t.Errorf("got %q, want %q", got, "x"+want)
	}
	if got := string(AppendEscape(nil, []byte(in))); got != want {
		t.Errorf("bytes: got %q, want %q", got, want)
	}
	// encoding/xml reads it back unchanged, as text and as an attribute value
	var v struct {
		A    string `xml:"a,attr"`
		Text string `xml:",chardata"`
	}
	doc := `<r a="` + want + `">` + want + `</r>`
	if err := xml.Unmarshal([]byte(doc), &v); err != nil {
		t.Fatal(err)
	}
	if v.A != in || v.Text != in {
		t.Errorf("read back as %q and %q", v.A, v.Text)
	}
	if n := testing.AllocsPerRun(100, func() {
		b := make([]byte, 0, 128)
		b = AppendEscape(b, in)
		_ = AppendEscape(b, []byte(in))
	}); n != 0 {
		t.Errorf("%v allocations", n)
	}
}

// FuzzParseDecl checks that ParseDecl doesn't panic, that a declaration
// without an error is written by AppendDecl to one it reads the same way,
// and that DetectEncoding returns the encoding name it reads.
func FuzzParseDecl(f *testing.F) {
	f.Add([]byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`))
	f.Add([]byte(`<?xml version='1.0' encoding=x?>`))
	f.Add([]byte(`<?xml encoding="EUC-JP"?>`))
	f.Fuzz(func(t *testing.T, b []byte) {
		d, n, err := ParseDecl(b)
		if n < 0 || n > len(b) || err == nil && n == 0 {
			t.Fatalf("length %d of %d bytes, error %v", n, len(b), err)
		}
		if err == ErrNoDecl {
			return
		}
		if err == nil {
			out := AppendDecl(nil, d)
			d2, n2, err := ParseDecl(out)
			if err != nil || d2 != d || n2 != len(out) {
				t.Fatalf("%q written as %q: got %+v, %d, %v", b, out, d2, n2, err)
			}
		}
		if e := DetectEncoding(b); e.Declared != d.Encoding {
			t.Fatalf("%q: declared encoding %q, ParseDecl read %q", b, e.Declared, d.Encoding)
		}
	})
}

func TestCheckWellFormed(t *testing.T) {
	for _, tc := range []struct {
		in  string
		err string // a part of the message, or empty for none
	}{
		{`<a/>`, ""},
		{"\xef\xbb\xbf<?xml version=\"1.0\"?>\n<a/>\n", ""},
		{`<?xml version="1.0" encoding="ISO-8859-1" standalone="yes"?><!DOCTYPE a><a/>`, ""},
		{`<!DOCTYPE a [<!ENTITY e "x">]><a b="&e;">&e;</a>`, ""},
		{`<!DOCTYPE a SYSTEM "a.dtd"><a>&e;</a>`, ""},
		{"<!DOCTYPE a PUBLIC \"-//A//EN\" 'a.dtd' [\n]\n><a/>", ""},
		{`<!-- c --><?pi data?><a xmlns="u" xmlns:p="v" p:b="1" b="2" xml:lang="en"><p:c/><![CDATA[<&]]>&lt;&#60;&#x3C;&#x10FFFF;</a><!-- c -->`, ""},
		{"<a b = '1'\tc=\"2\" ></a >", ""},
		{`<a xmlns:p="u" xmlns:q="v" p:b="1" q:b="2"><?pi?><?pix?></a>`, ""},

		// document
		{``, "no root element"},
		{`<!-- c -->`, "no root element"},
		{`<a/><b/>`, "second root element"},
		{`x<a/>`, "text outside the root element"},
		{`<a/>&amp;`, "text outside the root element"},
		{`<a/><![CDATA[x]]>`, "CDATA section outside the root element"},
		{"<a>\x01</a>", "character not allowed in XML"},
		{"<a>\xff</a>", "invalid UTF-8"},

		// elements
		{`<a></b></a>`, "end tag b without a start tag"},
		{`<a><b></a>`, "no end tag for b"},
		{`<a>`, "no end tag for a"},
		{`<a><b`, "StartTag cut off by the end of the input"},
		{`<a></a b>`, "malformed end tag"},
		{`<a><b:c:d/></a>`, `malformed element name "b:c:d"`},
		{`<a b="1" b="2"/>`, "duplicate attribute b"},
		{`<a b=1/>`, "attribute b without a quoted value"},
		{`<a b/>`, "attribute b without a quoted value"},
		{`<a b="1"c="2"/>`, "malformed start tag"},
		{`<a/ >`, "malformed start tag"},
		{`<a b="<"/>`, `"<" in the value of the attribute b`},
		{`<a b="&"/>`, `"&" not starting a reference`},

		// text and references
		{`<a>&e;</a>`, "reference to the undeclared entity e"},
		{`<?xml version="1.0" standalone="yes"?><!DOCTYPE a SYSTEM "a.dtd"><a>&e;</a>`, "reference to the undeclared entity e"},
		{`<a>&#0;</a>`, "malformed character reference &#0;"},
		{`<a>&#X41;</a>`, "malformed character reference &#X41;"},
		{`<a>&#xD800;</a>`, "malformed character reference &#xD800;"},
		{`<a>& b</a>`, `"&" not starting a reference`},
		{`<a>&b c;</a>`, "malformed reference &b c;"},
		{`<a>]]></a>`, `"]]>" in text`},
		{`<a>a < b</a>`, `"<" in text`},

		// comments and processing instructions
		{`<!-- a -- b --><a/>`, `"--" in a comment`},
		{`<a><!-- a ---></a>`, `"--" in a comment`},
		{`<!--><a/>`, "Comment cut off by the end of the input"},
		{` <?xml version="1.0"?><a/>`, "reserved target xml"},
		{`<?XML version="1.0"?><a/>`, "reserved target XML"},
		{`<?xml encoding="UTF-8"?><a/>`, "XML declaration without a version"},
		{`<?xml version="1.0" a="b"?><a/>`, "malformed XML declaration"},
		{`<?p:i?><a/>`, `malformed processing instruction target "p:i"`},
		{`<? pi?><a/>`, `malformed processing instruction target ""`},
		{`<a/><!a>`, "malformed markup"},

		// doctypes
		{`<a/><!DOCTYPE a>`, "doctype after the root element"},
		{`<!DOCTYPE a><!DOCTYPE a><a/>`, "second doctype"},
		{`<!DOCTYPE><a/>`, "malformed doctype name"},
		{`<!DOCTYPE a SYSTEM><a/>`, "malformed doctype"},
		{`<!DOCTYPE a PUBLIC "{" "x"><a/>`, "malformed doctype"},
		{`<!DOCTYPE a [<!ENTITY e "x">] x><a/>`, "malformed doctype internal subset"},

		// namespaces
		{`<p:a/>`, "element p:a with the undeclared prefix p"},
		{`<xmlns:a/>`, "reserved prefix xmlns"},
		{`<a p:b="1"/>`, "attribute p:b with the undeclared prefix p"},
		{`<a xmlns:p="u" xmlns:q="u" p:b="1" q:b="2"/>`, "duplicate attribute b in the namespace u"},
		{`<a xmlns:xmlns="u"/>`, "declaration of the reserved prefix xmlns"},
		{`<a xmlns:xml="u"/>`, "declaration of the reserved prefix xml"},
		{`<a xmlns:p="http://www.w3.org/XML/1998/namespace"/>`, "declaration of the prefix p with a reserved namespace"},
		{`<a xmlns:p=""/>`, "declaration of the prefix p with an empty namespace"},
		{`<a xmlns="http://www.w3.org/2000/xmlns/"/>`, "default namespace declared as a reserved namespace"},
	} {
		err := CheckWellFormed(strings.NewReader(tc.in))
		if tc.err == "" && err != nil || tc.err != "" && (err == nil || !strings.Contains(err.Error(), tc.err)) {
			t.Errorf("%q: got %v, want %q", tc.in, err, tc.err)
		}
		if _, ok := err.(*htmlstream.SyntaxError); err != nil && !ok {
			t.Errorf("%q: %T, want a *htmlstream.SyntaxError", tc.in, err)
		}
	}

	// elements nested past the stack limits
	if err := CheckWellFormed(strings.NewReader(strings.Repeat("<a>", htmlstream.DefaultMaxDepth+1))); err == nil || !strings.Contains(err.Error(), "nested past the stack limits") {
		t.Errorf("past the stack limits: got %v", err)
	}

	// text and comments split into several tokens (see
	// htmlstream.Token.Partial) at each offset around the chunk size
	for _, tc := range []struct {
		in, err string
	}{
		{"<a>%s&amp;&#x10FFFF;]]&gt;</a>", ""},
		{"<!DOCTYPE a [ ]><a>%s&a" + strings.Repeat("b", 100) + ";</a>", ""},
		{"<!DOCTYPE a [ ]><a>%s&a" + strings.Repeat("b", 100) + " c;</a>", "reference"},
		{"<a>%s]]></a>", `"]]>" in text`},
		{"<a>%s&ab c;</a>", "malformed reference"},
		{"<!--%s- -  -- --><a/>", `"--" in a comment`},
		{"<!--%s- - -  - --><a/>", ""},
		{"<!--%s-  - --><a/>", ""},
		{"<a><!--%s---></a>", `"--" in a comment`},
	} {
		for n := htmlstream.DefaultChunkSize - 128; n < htmlstream.DefaultChunkSize+4; n++ {
			in := fmt.Sprintf(tc.in, strings.Repeat("x", n))
			err := CheckWellFormed(strings.NewReader(in))
			if tc.err == "" && err != nil || tc.err != "" && (err == nil || !strings.Contains(err.Error(), tc.err)) {
				t.Errorf("%q with %d bytes of padding: got %v, want %q", tc.in, n, err, tc.err)
			}
		}
	}
}

// TestCheckWellFormedXMLTest checks CheckWellFormed with the documents of
// James Clark's XML test cases: well-formed documents (valid and invalid) are
// accepted, decoding UTF-16 documents first, except for a document which
// isn't namespace-well-formed, and documents which aren't well-formed are
// rejected, except those whose error is in the doctype's internal subset or
// in an external entity or DTD, which aren't checked.
func TestCheckWellFormedXMLTest(t *testing.T) {
	var unchecked int
	for d := range xmltest_testdata.Documents() {
		in := d.Data
		if b := []byte(in); len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE {
			u := make([]uint16, len(b)/2-1)
			for i := range u {
				u[i] = binary.LittleEndian.Uint16(b[2+2*i:])
			}
			in = string(utf16.Decode(u))
		}
		err := CheckWellFormed(strings.NewReader(in))
		switch {
		case d.Kind != "not-wf" && d.Name == "xmltest/valid/sa/012.xml":
			// an attribute named ":"
			if err == nil {
				t.Errorf("%s: not namespace-well-formed, but accepted", d.Name)
			}
		case d.Kind != "not-wf" && err != nil:
			t.Errorf("%s: %v", d.Name, err)
		case d.Kind == "not-wf" && err == nil:
			unchecked++
			if !regexp.MustCompile(`<!DOCTYPE[^>\[]*(\[|SYSTEM|PUBLIC)`).MatchString(in) {
				t.Errorf("%s: accepted, without an internal subset or an external DTD", d.Name)
			}
		}
	}
	if want := 85; unchecked != want {
		t.Errorf("%d documents which aren't well-formed accepted, want %d", unchecked, want)
	}
}

// FuzzCheckWellFormed checks that CheckWellFormed doesn't panic, and that
// encoding/xml reads XML 1.0 documents without a doctype which it accepts.
func FuzzCheckWellFormed(f *testing.F) {
	f.Add(`<?xml version="1.0"?><!-- c --><a xmlns:p="u" p:b="&amp;">x<![CDATA[y]]><p:c/><?pi?></a>`)
	f.Add(`<a b="1" b="2">]]></b>`)
	for d := range xmltest_testdata.Documents() {
		f.Add(d.Data)
	}
	f.Fuzz(func(t *testing.T, in string) {
		if CheckWellFormed(strings.NewReader(in)) != nil || strings.Contains(in, "<!DOCTYPE") {
			return
		}
		if d, _, err := ParseDecl([]byte(in)); err == nil && d.Version != "1.0" {
			return
		}
		dec := xml.NewDecoder(strings.NewReader(in))
		dec.Strict = true
		dec.CharsetReader = func(label string, r io.Reader) (io.Reader, error) { return r, nil }
		for {
			_, err := dec.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("%q: accepted, but encoding/xml: %v", in, err)
			}
		}
	})
}
