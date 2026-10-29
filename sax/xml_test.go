package sax

import (
	"encoding/xml"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/pgaskin/htmlstream"
	xmltest_testdata "github.com/pgaskin/htmlstream/testdata/xmltest"
)

// xmlTokens returns the tokens of d as strings, with adjacent character data
// merged.
func xmlTokens(d *xml.Decoder) ([]string, error) {
	var toks []string
	text := false
	for {
		tok, err := d.Token()
		if err == io.EOF {
			return toks, nil
		}
		if err != nil {
			return toks, err
		}
		var s string
		switch tok := tok.(type) {
		case xml.StartElement:
			s = fmt.Sprintf("<{%s}%s", tok.Name.Space, tok.Name.Local)
			for _, a := range tok.Attr {
				s += fmt.Sprintf(" {%s}%s=%q", a.Name.Space, a.Name.Local, a.Value)
			}
			s += ">"
		case xml.EndElement:
			s = fmt.Sprintf("</{%s}%s>", tok.Name.Space, tok.Name.Local)
		case xml.CharData:
			if text {
				toks[len(toks)-1] += string(tok)
				continue
			}
			s = string(tok)
		case xml.Comment:
			s = fmt.Sprintf("<!--%s-->", tok)
		case xml.ProcInst:
			s = fmt.Sprintf("<?%s %s?>", tok.Target, tok.Inst)
		case xml.Directive:
			s = fmt.Sprintf("<!%s>", tok)
		}
		_, text = tok.(xml.CharData)
		toks = append(toks, s)
	}
}

// TestTokenReaderXMLTest checks that encoding/xml reads the same tokens
// through a TokenReader as from the documents of the XML test cases it
// accepts, except for the doctypes, whose whitespace isn't kept, and where
// encoding/xml doesn't normalize whitespace in attribute values and newlines
// in processing instructions like XML parsers do (see
// [attribute-value normalization] and [end-of-line handling]).
//
// [attribute-value normalization]: https://www.w3.org/TR/xml/#AVNormalize
// [end-of-line handling]: https://www.w3.org/TR/xml/#sec-line-ends
func TestTokenReaderXMLTest(t *testing.T) {
	files := map[string]string{}
	for name, data := range xmltest_testdata.Files() {
		if strings.HasSuffix(name, ".xml") {
			files[name] = data
		}
	}
	differ := map[string]bool{
		"xmltest/valid/sa/043.xml": true, // CRLF in an attribute value
		"xmltest/valid/sa/058.xml": true, // tabs in an attribute value
		"xmltest/valid/sa/098.xml": true, // CRLF in a processing instruction
		"xmltest/valid/sa/104.xml": true, // tab in an attribute value
	}
	compared := 0
	for _, name := range slices.Sorted(maps.Keys(files)) {
		in := files[name]
		want, err := xmlTokens(xml.NewDecoder(strings.NewReader(in)))
		if err != nil {
			continue
		}
		compared++
		r := htmlstream.NewReader(strings.NewReader(in))
		r.SetMode(htmlstream.XML)
		got, err := xmlTokens(xml.NewTokenDecoder(NewTokenReader(NewReader(r))))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		for i := range max(len(got), len(want)) {
			var g, w string
			if i < len(got) {
				g = got[i]
			}
			if i < len(want) {
				w = want[i]
			}
			if strings.HasPrefix(g, "<!DOCTYPE") && strings.HasPrefix(w, "<!DOCTYPE") {
				continue
			}
			if g != w {
				if !differ[name] {
					t.Errorf("%s: token %d: %q, want %q", name, i, g, w)
				}
				delete(differ, name)
				break
			}
		}
	}
	for name := range differ {
		t.Errorf("%s: no difference", name)
	}
	if compared < 100 {
		t.Errorf("compared %d documents", compared)
	}
	t.Logf("compared %d of %d documents", compared, len(files))
}

// TestTokenReaderMalformed checks that encoding/xml decodes malformed XML and
// HTML through a TokenReader, which it rejects as they are.
func TestTokenReaderMalformed(t *testing.T) {
	type doc struct {
		Title string   `xml:"head>title"`
		Items []string `xml:"body>div>p"`
		Links []string `xml:"body>div>a"`
	}
	for _, c := range []struct {
		mode htmlstream.Mode
		in   string
		want doc
	}{
		{htmlstream.XML, `<html><head><title>a &mdash; b & c</title></head><body><div><p>1</p><p>2<br></div><div><a>x</a></div></body></html>`,
			doc{"a — b & c", []string{"1", "2"}, []string{"x"}}},
		{htmlstream.HTML, `<title>a &mdash; b & c</title><div><p>1<p>2<br></div><DIV><a>x<a>y</div>`,
			doc{"a — b & c", []string{"1", "2"}, []string{"x", "y"}}},
		{htmlstream.Universal, `<html><head><title>a &mdash; b & c</title><body><div><p>1<p>2<br></div><div><a>x</a><a>y</div>`,
			doc{"a — b & c", []string{"1", "2"}, []string{"x", "y"}}},
	} {
		if err := xml.Unmarshal([]byte(c.in), new(doc)); err == nil {
			t.Errorf("%v %q: encoding/xml reads it", c.mode, c.in)
		}
		var v doc
		_, s := newReader(c.mode, c.in)
		if err := xml.NewTokenDecoder(NewTokenReader(s)).Decode(&v); err != nil || !reflectEqual(v, c.want) {
			t.Errorf("%v %q: %+v, %v", c.mode, c.in, v, err)
		}
	}

	// the tags and doctypes HTML parsers drop are left out, so the XML
	// written is well-formed
	for _, in := range []string{`<p>x<!DOCTYPE html>y<b class="z`, `<p>x<!DOCTYPE html>y<b class=z `} {
		_, s := newReader(htmlstream.HTML, in)
		s.SetFragment(true)
		var b strings.Builder
		enc := xml.NewEncoder(&b)
		tr := NewTokenReader(s)
		for {
			tok, err := tr.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("%q: %v", in, err)
			}
			if err := enc.EncodeToken(tok); err != nil {
				t.Fatalf("%q: %v", in, err)
			}
		}
		if err := enc.Flush(); err != nil {
			t.Fatal(err)
		}
		if got, want := b.String(), `<p xmlns="http://www.w3.org/1999/xhtml">xy</p>`; got != want {
			t.Errorf("%q: wrote %s, want %s", in, got, want)
		}
	}
}

func reflectEqual(a, b any) bool { return fmt.Sprint(a) == fmt.Sprint(b) }

func TestTokenReaderManyAttrs(t *testing.T) {
	// Converting a tag takes time linear in its attributes and namespace
	// declarations, and so does each later tag.
	const n = 100000
	var b strings.Builder
	b.WriteString("<r")
	for i := range n {
		fmt.Fprintf(&b, " xmlns:p%[1]d='u%[1]d' p%[1]d:a=''", i)
	}
	b.WriteString(" xmlns:p0='dup' p0:a=''>")
	for range n / 10 {
		b.WriteString(`<s b0 b1 b2 b3 b4 b5 b6 b7 b8 b9 b0></s>`)
	}
	b.WriteString("</r>")
	for _, mode := range []htmlstream.Mode{htmlstream.HTML, htmlstream.XML} {
		_, s := newReader(mode, b.String())
		tr := NewTokenReader(s)
		var elems int
		for {
			tok, err := tr.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("%v: %v", mode, err)
			}
			se, ok := tok.(xml.StartElement)
			if !ok {
				continue
			}
			if se.Name.Local == "s" {
				elems++
			}
			if se.Name.Local != "r" {
				continue
			}
			attrs := 2 * n
			if mode == htmlstream.XML {
				attrs += 2 // duplicates are kept
			}
			if len(se.Attr) != attrs {
				t.Fatalf("%v: %d attributes, want %d", mode, len(se.Attr), attrs)
			}
			for i := range n {
				a := se.Attr[2*i+1]
				if want := fmt.Sprint("u", i); a.Name.Space != want {
					t.Fatalf("%v: %s:%s in namespace %q, want %q", mode, a.Name.Space, a.Name.Local, a.Name.Space, want)
				}
			}
		}
		if elems != n/10 {
			t.Errorf("%v: %d elements", mode, elems)
		}
	}
}

func TestTokenReaderAttrs(t *testing.T) {
	for _, c := range []struct {
		mode htmlstream.Mode
		in   string
		want string
	}{
		{htmlstream.HTML, `<p ID=a id=b Class=c xml:lang=en xmlns="urn:x" xmlns:Foo="urn:y">`, `<{http://www.w3.org/1999/xhtml}p {}id="a" {}class="c" {http://www.w3.org/XML/1998/namespace}lang="en" {http://www.w3.org/2000/xmlns/}xmlns="urn:x" {http://www.w3.org/2000/xmlns/}foo="urn:y">`},
		{htmlstream.HTML, `<svg viewbox="0 0 1 1" XLink:Href=#a><foreignobject><math definitionurl=x>`, `<{http://www.w3.org/2000/svg}svg {}viewBox="0 0 1 1" {http://www.w3.org/1999/xlink}href="#a">`},
		{htmlstream.Universal, `<svg viewbox="0 0 1 1" XLink:Href=#a XML:LANG=en>`, `<{http://www.w3.org/2000/svg}svg {}viewBox="0 0 1 1" {http://www.w3.org/1999/xlink}href="#a" {http://www.w3.org/XML/1998/namespace}lang="en">`},
		// attributes with different names are kept, even in the same
		// namespace, also with more than 8 attributes
		{htmlstream.Universal, `<svg xmlns:l="http://www.w3.org/1999/xlink"><use l:href="#a" xlink:href="#b" XLINK:HREF="#c"/></svg>`, `<{http://www.w3.org/2000/svg}use {http://www.w3.org/1999/xlink}href="#a" {http://www.w3.org/1999/xlink}href="#b">`},
		{htmlstream.HTML, `<svg xmlns:l="http://www.w3.org/1999/xlink"><use x1 x2 x3 x4 x5 x6 x7 l:href="#a" xlink:href="#b" XLINK:HREF="#c"/></svg>`, `<{http://www.w3.org/2000/svg}use {}x1="" {}x2="" {}x3="" {}x4="" {}x5="" {}x6="" {}x7="" {http://www.w3.org/1999/xlink}href="#a" {http://www.w3.org/1999/xlink}href="#b">`},
		{htmlstream.XML, `<a xmlns="urn:x" xmlns:p="urn:y" p:b="1" b="2" xml:lang="en"/>`, `<{urn:x}a {http://www.w3.org/2000/xmlns/}xmlns="urn:x" {http://www.w3.org/2000/xmlns/}p="urn:y" {urn:y}b="1" {}b="2" {http://www.w3.org/XML/1998/namespace}lang="en">`},
	} {
		_, s := newReader(c.mode, c.in)
		toks, err := xmlTokens(xml.NewTokenDecoder(NewTokenReader(s)))
		if err != nil || !slices.ContainsFunc(toks, func(s string) bool { return s == c.want }) {
			t.Errorf("%v %q: tokens %q, %v; want %s", c.mode, c.in, toks, err, c.want)
		}
	}
	_, s := newReader(htmlstream.HTML, `<math definitionurl=x>`)
	tr := NewTokenReader(s)
	for {
		tok, err := tr.Token()
		if err != nil {
			t.Fatal(err)
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "math" {
			if len(se.Attr) != 1 || se.Attr[0].Name.Local != "definitionURL" {
				t.Errorf("math attributes %v", se.Attr)
			}
			break
		}
	}
}
