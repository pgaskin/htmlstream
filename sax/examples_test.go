package sax_test

import (
	"encoding/xml"
	"fmt"
	"strings"

	"github.com/pgaskin/htmlstream"
	"github.com/pgaskin/htmlstream/htmlutil"
	"github.com/pgaskin/htmlstream/sax"
)

// opf is an EPUB package document with an undeclared entity (&mdash;), an
// unescaped ampersand and an item without "/>", which encoding/xml rejects.
const opf = `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:identifier id="uid">urn:uuid:1234</dc:identifier>
    <dc:title>Pride &amp; Prejudice &mdash; Vol. 1</dc:title>
    <dc:creator>Jane Austen & Co.</dc:creator>
    <meta property="dcterms:modified">2020-01-01T00:00:00Z</meta>
  </metadata>
  <manifest>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
    <item id="c1" href="c1.xhtml" media-type="application/xhtml+xml"></item>
    <item id="css" href="style.css" media-type="text/css">
  </manifest>
  <spine><itemref idref="c1"/></spine>
</package>`

const (
	nsOPF = "http://www.idpf.org/2007/opf"
	nsDC  = "http://purl.org/dc/elements/1.1/"
)

// Extract the metadata and manifest of an EPUB package document.
func Example() {
	r := htmlstream.NewReader(strings.NewReader(opf))
	r.SetMode(htmlstream.XML)
	s := sax.NewReader(r)
	for ev := range s.All() {
		if ev.Kind != sax.StartElement {
			continue
		}
		switch ev.Name {
		case sax.Name{Space: nsDC, Local: "title"}, sax.Name{Space: nsDC, Local: "creator"}:
			name := ev.Name.Local
			text, _ := s.ReadText(nil, 1024)
			fmt.Printf("%s: %s\n", name, text)
		case sax.Name{Space: nsOPF, Local: "item"}:
			path := s.Path()
			if len(path) < 2 || path[len(path)-2] != (sax.Name{Space: nsOPF, Local: "manifest"}) {
				break
			}
			id, _ := ev.Attr("id")
			href, _ := ev.Attr("href")
			fmt.Printf("item %s: %s\n", id, href)
		}
	}
	if err := s.Err(); err != nil {
		fmt.Println(err)
	}
	// Output:
	// title: Pride & Prejudice — Vol. 1
	// creator: Jane Austen & Co.
	// item nav: nav.xhtml
	// item c1: c1.xhtml
	// item css: style.css
}

// Decode a malformed EPUB package document into structs with encoding/xml.
func ExampleTokenReader() {
	var pkg struct {
		Title    string `xml:"metadata>title"`
		Manifest []struct {
			ID   string `xml:"id,attr"`
			Href string `xml:"href,attr"`
		} `xml:"manifest>item"`
	}
	r := htmlstream.NewReader(strings.NewReader(opf))
	r.SetMode(htmlstream.XML)
	d := xml.NewTokenDecoder(sax.NewTokenReader(sax.NewReader(r)))
	if err := d.Decode(&pkg); err != nil {
		fmt.Println(err)
	}
	fmt.Println(pkg.Title)
	fmt.Println(pkg.Manifest)

	// Output:
	// Pride & Prejudice — Vol. 1
	// [{nav nav.xhtml} {c1 c1.xhtml} {css style.css}]
}

// Decode an HTML document into structs with encoding/xml. The implied html,
// head and body elements are delivered too.
func ExampleTokenReader_html() {
	var page struct {
		Title string `xml:"head>title"`
		Links []struct {
			Href string `xml:"href,attr"`
			Text string `xml:",chardata"`
		} `xml:"body>ul>li>a"`
	}
	r := htmlstream.NewReader(strings.NewReader(`<!DOCTYPE html>
<title>Links &amp; more</title>
<ul>
  <li><a href=/one>One</a>
  <li><a href=/two>Two</a>
</ul>`))
	d := xml.NewTokenDecoder(sax.NewTokenReader(sax.NewReader(r)))
	if err := d.Decode(&page); err != nil {
		fmt.Println(err)
	}
	fmt.Printf("%+v\n", page)

	// Output:
	// {Title:Links & more Links:[{Href:/one Text:One} {Href:/two Text:Two}]}
}

// Print the headings of an HTML document as an outline, with the text of
// hidden elements left out.
func ExampleReader_Walk() {
	r := htmlstream.NewReader(strings.NewReader(`<h1>Title</h1>
<p>Text<h2>One <script>x()</script>more</h2>
<p>Text</p></p>
<h2>Two</h2>`))
	s := sax.NewReader(r)
	var (
		in      bool
		heading strings.Builder
	)
	err := s.Walk(sax.Handler{
		StartElement: func(s *sax.Reader, ev *sax.Event) error {
			if in && htmlutil.IsHidden(ev) {
				return sax.SkipElement
			}
			if htmlutil.IsHeading(ev) {
				in = true
				heading.WriteString(strings.Repeat("  ", int(ev.Name.Local[1]-'1')))
			}
			return nil
		},
		Text: func(s *sax.Reader, ev *sax.Event) error {
			if in {
				heading.Write(ev.Text)
			}
			return nil
		},
		EndElement: func(s *sax.Reader, ev *sax.Event) error {
			if htmlutil.IsHeading(ev) {
				fmt.Println(heading.String())
				heading.Reset()
				in = false
			}
			return nil
		},
		Warning: func(s *sax.Reader, ev *sax.Event) error {
			fmt.Printf("warning at %d: %s\n", ev.Pos, ev.Text)
			return nil
		},
	})
	if err != nil {
		fmt.Println(err)
	}
	// Output:
	// Title
	//   One more
	// warning at 71: dropped stray </p>
	//   Two
}

// Decode the entries of a feed one at a time, without reading the whole
// document into memory.
func ExampleTokenReader_find() {
	r := htmlstream.NewReader(strings.NewReader(`<?xml version="1.0"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Example</title>
  <entry><title>One &amp; two</title><link href="/1"/></entry>
  <entry><title>Three</title><link href="/3"></entry>
</feed>`))
	r.SetMode(htmlstream.XML)
	type entry struct {
		Title string `xml:"title"`
		Link  struct {
			Href string `xml:"href,attr"`
		} `xml:"link"`
	}
	for r.Find(htmlstream.TagNS("http://www.w3.org/2005/Atom", "entry")) {
		var e entry
		if err := xml.NewTokenDecoder(sax.NewTokenReader(sax.NewReader(r))).Decode(&e); err != nil {
			fmt.Println(err)
			return
		}
		fmt.Printf("%s: %s\n", e.Title, e.Link.Href)
	}
	if err := r.Err(); err != nil {
		fmt.Println(err)
	}
	// Output:
	// One & two: /1
	// Three: /3
}
