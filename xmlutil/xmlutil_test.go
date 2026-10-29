package xmlutil

import (
	"encoding/binary"
	"encoding/json"
	"encoding/xml"
	"errors"
	"os"
	"testing"
	"unicode/utf16"
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
