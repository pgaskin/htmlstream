package htmlstream

import (
	"bytes"
	"slices"
	"strings"
	"testing"
	"testing/iotest"
	"unicode/utf16"

	xmltest_testdata "github.com/pgaskin/htmlstream/testdata/xmltest"
)

// TestXMLTestPassthrough checks that every file of the XML test cases
// (well-formed or not, documents and external entities) is read as tokens
// which partition the input, and written unchanged by a Transformer without
// edits, in all modes, with several chunk and read sizes.
func TestXMLTestPassthrough(t *testing.T) {
	for name, in := range xmltest_testdata.Files() {
		for _, mode := range modes {
			want := mergedTokens(t, in, mode, false, 0, nil)
			for _, chunk := range []int{0, 1, 3, -5} {
				readAll(t, in, mode, false, chunk, iotest.OneByteReader)
				got := mergedTokens(t, in, mode, false, chunk, iotest.HalfReader)
				if strings.Join(got, "\n") != strings.Join(want, "\n") {
					t.Errorf("%s %s (chunk %d): tokens differ:\n got %q\nwant %q", name, mode, chunk, got, want)
				}
			}
			var b bytes.Buffer
			x := NewTransformer(&b, strings.NewReader(in))
			x.SetMode(mode)
			for x.Next() {
			}
			if err := x.Close(); err == ErrUnsupportedEncoding {
				continue
			} else if err != nil {
				t.Errorf("%s %s: %v", name, mode, err)
			}
			if b.String() != in {
				t.Errorf("%s %s: output differs", name, mode)
			}
		}
	}
}

// xmltestCanonDiffs lists the valid documents whose canonical form, written
// from the tokens of a Reader in XML mode, differs from the expected one, by
// reason. The doctype isn't processed, so declared entities, default
// attribute values and attribute types have no effect.
var xmltestCanonDiffs = func() map[string]string {
	m := make(map[string]string)
	for reason, names := range map[string][]string{
		"entity declared in the doctype or an external entity": {
			"valid/ext-sa/001.xml", "valid/ext-sa/002.xml", "valid/ext-sa/003.xml", "valid/ext-sa/004.xml",
			"valid/ext-sa/005.xml", "valid/ext-sa/006.xml", "valid/ext-sa/007.xml", "valid/ext-sa/008.xml",
			"valid/ext-sa/009.xml", "valid/ext-sa/010.xml", "valid/ext-sa/011.xml", "valid/ext-sa/012.xml",
			"valid/ext-sa/013.xml", "valid/ext-sa/014.xml", "valid/not-sa/031.xml", "valid/sa/023.xml",
			"valid/sa/024.xml", "valid/sa/053.xml", "valid/sa/066.xml", "valid/sa/068.xml",
			"valid/sa/085.xml", "valid/sa/086.xml", "valid/sa/087.xml", "valid/sa/088.xml",
			"valid/sa/089.xml", "valid/sa/108.xml", "valid/sa/110.xml", "valid/sa/114.xml",
			"valid/sa/115.xml",
		},
		"entity declared in the doctype with the name of an HTML named character reference (rsqb), which is decoded instead": {
			"valid/sa/118.xml",
		},
		"default attribute value declared in the doctype or the external DTD": {
			"valid/not-sa/003.xml", "valid/not-sa/004.xml", "valid/not-sa/005.xml", "valid/not-sa/006.xml",
			"valid/not-sa/007.xml", "valid/not-sa/008.xml", "valid/not-sa/009.xml", "valid/not-sa/010.xml",
			"valid/not-sa/011.xml", "valid/not-sa/012.xml", "valid/not-sa/013.xml", "valid/not-sa/014.xml",
			"valid/not-sa/015.xml", "valid/not-sa/016.xml", "valid/not-sa/017.xml", "valid/not-sa/018.xml",
			"valid/not-sa/019.xml", "valid/not-sa/020.xml", "valid/not-sa/021.xml", "valid/not-sa/022.xml",
			"valid/not-sa/023.xml", "valid/not-sa/024.xml", "valid/not-sa/025.xml", "valid/not-sa/026.xml",
			"valid/not-sa/028.xml", "valid/not-sa/029.xml", "valid/sa/044.xml", "valid/sa/045.xml",
			"valid/sa/046.xml", "valid/sa/080.xml", "valid/sa/091.xml", "valid/sa/094.xml",
			"valid/sa/096.xml", "valid/sa/097.xml",
		},
		"attribute value normalized for an attribute type declared in the doctype (NMTOKENS)": {
			"valid/sa/058.xml", "valid/sa/111.xml",
		},
	} {
		for _, name := range names {
			m["xmltest/"+name] = reason
		}
	}
	return m
}()

// TestXMLTestCanonical checks the tokens of the valid documents of the XML
// test cases in XML mode by writing them in James Clark's canonical XML form
// (see xmltest/canonxml.html in the zip) and comparing them with the
// expected output, except for the documents in xmltestCanonDiffs. UTF-16
// documents are decoded first.
func TestXMLTestCanonical(t *testing.T) {
	var n int
	for d := range xmltest_testdata.Documents() {
		if d.Kind != "valid" {
			continue
		}
		n++
		got := xmltestCanonical(t, decodeUTF16(d.Data))
		reason, diff := xmltestCanonDiffs[d.Name]
		switch {
		case got != d.Canonical && !diff:
			t.Errorf("%s: canonical form differs:\n got %q\nwant %q", d.Name, got, d.Canonical)
		case got == d.Canonical && diff:
			t.Errorf("%s: canonical form matches, but is listed as differing (%s)", d.Name, reason)
		}
	}
	if n == 0 {
		t.Fatal("no valid documents")
	}
	t.Logf("%d valid documents, %d differ as listed", n, len(xmltestCanonDiffs))
}

// decodeUTF16 decodes s to UTF-8 if it starts with a UTF-16 byte order mark.
func decodeUTF16(s string) string {
	var be bool
	switch {
	case strings.HasPrefix(s, "\xfe\xff"):
		be = true
	case strings.HasPrefix(s, "\xff\xfe"):
	default:
		return s
	}
	u := make([]uint16, 0, len(s)/2)
	for i := 2; i+1 < len(s); i += 2 {
		if be {
			u = append(u, uint16(s[i])<<8|uint16(s[i+1]))
		} else {
			u = append(u, uint16(s[i+1])<<8|uint16(s[i]))
		}
	}
	return string(utf16.Decode(u))
}

// xmltestCanonical writes the canonical XML form of in, read in XML mode:
// the root element and the processing instructions outside it, without the
// XML declaration, doctype and comments, with attributes sorted by name, an
// end tag for every element, and the characters of Datachar escaped.
func xmltestCanonical(t testing.TB, in string) string {
	r := NewReader(strings.NewReader(in))
	r.SetMode(XML)
	var b strings.Builder
	var buf []byte
	type attr struct{ name, value string }
	var attrs []attr
	for r.Next() {
		tok := r.Token()
		switch tok.Kind() {
		case Text, CDATA:
			if r.Depth() > 0 {
				buf = tok.AppendText(buf[:0])
				xmltestEscape(&b, buf)
			}
		case StartTag:
			attrs = attrs[:0]
			for a := range tok.Attrs() {
				attrs = append(attrs, attr{string(a.Name()), a.Value()})
			}
			slices.SortFunc(attrs, func(a, b attr) int { return strings.Compare(a.name, b.name) })
			b.WriteString("<")
			b.Write(tok.Name())
			for _, a := range attrs {
				b.WriteString(" ")
				b.WriteString(a.name)
				b.WriteString(`="`)
				xmltestEscape(&b, []byte(a.value))
				b.WriteString(`"`)
			}
			b.WriteString(">")
			if tok.SelfClosing() {
				b.WriteString("</")
				b.Write(tok.Name())
				b.WriteString(">")
			}
		case EndTag:
			b.WriteString("</")
			b.Write(tok.Name())
			b.WriteString(">")
		case ProcInst:
			if string(tok.Name()) == "xml" {
				continue
			}
			b.WriteString("<?")
			b.Write(tok.Name())
			b.WriteString(" ")
			buf = tok.AppendText(buf[:0])
			b.Write(buf)
			b.WriteString("?>")
		}
	}
	if err := r.Err(); err != nil {
		t.Errorf("%q: %v", in, err)
	}
	return b.String()
}

// xmltestEscape writes s escaped as canonical XML Datachar.
func xmltestEscape(b *strings.Builder, s []byte) {
	for _, c := range s {
		switch c {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		case '\t':
			b.WriteString("&#9;")
		case '\n':
			b.WriteString("&#10;")
		case '\r':
			b.WriteString("&#13;")
		default:
			b.WriteByte(c)
		}
	}
}
