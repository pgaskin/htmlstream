package sax

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/pgaskin/htmlstream"
	"github.com/pgaskin/htmlstream/ascii"
	html5libgo_testdata "github.com/pgaskin/htmlstream/testdata/html5libgo"
	misc_testdata "github.com/pgaskin/htmlstream/testdata/misc"
	polyglot_testdata "github.com/pgaskin/htmlstream/testdata/polyglot"
	xml_testdata "github.com/pgaskin/htmlstream/testdata/xml"
)

// vectors returns the edge-case inputs in testdata/misc (see
// misc_testdata).
func vectors() []string {
	var vs []string
	for c := range misc_testdata.Categories() {
		vs = slices.AppendSeq(vs, misc_testdata.Items(c))
	}
	return vs
}

// dump returns the events of a Reader in a compact form: <name> and </name>
// for elements (with a ? for implied ones and an svg:, math: or {uri}
// prefix for namespaces other than XHTML), quoted text, ~raw for Untracked
// events, and !kind(text) for the others, with a trailing + for Partial
// events and "ERR err" if it fails.
func dump(s *Reader) string {
	var b strings.Builder
	for ev := range s.All() {
		if b.Len() != 0 {
			b.WriteByte(' ')
		}
		switch ev.Kind {
		case StartElement, EndElement:
			b.WriteByte('<')
			if ev.Kind == EndElement {
				b.WriteByte('/')
			}
			switch ev.Name.Space {
			case htmlstream.NamespaceXHTML:
			case htmlstream.NamespaceSVG:
				b.WriteString("svg:")
			case htmlstream.NamespaceMathML:
				b.WriteString("math:")
			case "":
				b.WriteString("{}")
			default:
				fmt.Fprintf(&b, "{%s}", ev.Name.Space)
			}
			b.WriteString(ev.Name.Local)
			if ev.Implied {
				b.WriteByte('?')
			}
			b.WriteByte('>')
		case Text:
			fmt.Fprintf(&b, "%q", ev.Text)
		case Untracked:
			if raw := ev.Token().Raw(); len(raw) != 0 {
				fmt.Fprintf(&b, "~%s", raw)
			} else {
				b.WriteString("~implied")
			}
		default:
			fmt.Fprintf(&b, "!%s(%s)", ev.Kind, ev.Text)
		}
		if ev.Partial {
			b.WriteByte('+')
		}
	}
	if err := s.Err(); err != nil {
		fmt.Fprintf(&b, " ERR %v", err)
	}
	return b.String()
}

func newReader(mode htmlstream.Mode, in string) (*htmlstream.Reader, *Reader) {
	r := htmlstream.NewReader(strings.NewReader(in))
	r.SetMode(mode)
	return r, NewReader(r)
}

func TestEvents(t *testing.T) {
	for _, c := range []struct {
		mode htmlstream.Mode
		in   string
		want string
	}{
		// implied html, head and body
		{htmlstream.HTML, ``, `<html?> <head?> </head?> <body?> </body?> </html?>`},
		{htmlstream.HTML, `x`, `<html?> <head?> </head?> <body?> "x" </body?> </html?>`},
		{htmlstream.HTML, `<!DOCTYPE html><title>a &amp; b</title><p>one<p>two`, `!Doctype(html) <html?> <head?> <title> "a & b" </title> </head?> <body?> <p> "one" </p?> <p> "two" </p?> </body?> </html?>`},
		{htmlstream.HTML, `<!--a--><html><!--b--><head></head><!--c--><body></body><!--d--></html><!--e-->`, `!Comment(a) <html> !Comment(b) <head> </head> !Comment(c) <body> !Comment(d) !Comment(e) </body?> </html?>`},
		{htmlstream.HTML, `<html><title>t</title></html>`, `<html> <head?> <title> "t" </title> </head?> <body?> </body?> </html?>`},
		// end tags of implied elements, and html and body elements kept open
		{htmlstream.HTML, `<!DOCTYPE html><title>t</title><p>x</p></body></html>`, `!Doctype(html) <html?> <head?> <title> "t" </title> </head?> <body?> <p> "x" </p> </body?> </html?>`},
		{htmlstream.HTML, `<html><title>t</title></head><body>x</body></html>`, `<html> <head?> <title> "t" </title> </head> <body> "x" </body?> </html?>`},
		{htmlstream.HTML, `</head><title>x</title>`, `<html?> <head?> </head> <title> "x" </title> <body?> </body?> </html?>`},
		{htmlstream.HTML, `<html><body></body></html><!--x--><p>y</p></body>z`, `<html> <head?> </head?> <body> !Comment(x) <p> "y" </p> "z" </body?> </html?>`},
		{htmlstream.HTML, `<body><div>x</body>y</html>`, `<html?> <head?> </head?> <body> <div> "x" </div?> "y" </body?> </html?>`},
		{htmlstream.HTML, `<p>x</html><html><body><head>y`, `<html?> <head?> </head?> <body?> <p> "x" !Warning(dropped misplaced html start tag) !Warning(dropped misplaced body start tag) !Warning(dropped misplaced head start tag) "y" </p?> </body?> </html?>`},
		{htmlstream.HTML, `<table></body><td></html><template></body></template><svg></body>`, `<html?> <head?> </head?> <body?> <table> !Warning(dropped stray </body>) <td> !Warning(dropped stray </html>) <template> !Warning(dropped stray </body>) </template> <svg:svg> !Warning(dropped stray </body>) </svg:svg?> </td?> </table?> </body?> </html?>`},
		{htmlstream.HTML, `<svg></body>x`, `<html?> <head?> </head?> <body?> <svg:svg> "x" </svg:svg?> </body?> </html?>`},
		{htmlstream.HTML, `<svg><html></html></svg>`, `<html?> <head?> </head?> <body?> <svg:svg> <svg:html> </svg:html> </svg:svg> </body?> </html?>`},
		{htmlstream.HTML, `<html><p>x`, `<html> <head?> </head?> <body?> <p> "x" </p?> </body?> </html?>`},
		{htmlstream.HTML, `<head></body>x`, `<html?> <head> </head?> <body?> "x" </body?> </html?>`},
		{htmlstream.HTML, `<head></head></head>x`, `<html?> <head> </head> !Warning(dropped stray </head>) <body?> "x" </body?> </html?>`},
		{htmlstream.HTML, `<title>t</title>  x`, `<html?> <head?> <title> "t" </title> "  " </head?> <body?> "x" </body?> </html?>`},
		{htmlstream.HTML, `x<html><p>y</html>`, `<html?> <head?> </head?> <body?> "x" !Warning(dropped misplaced html start tag) <p> "y" </p?> </body?> </html?>`},
		{htmlstream.HTML, `x<body><p>y</p></body>z`, `<html?> <head?> </head?> <body?> "x" !Warning(dropped misplaced body start tag) <p> "y" </p> "z" </body?> </html?>`},
		{htmlstream.HTML, `<p>x<body class=a>y`, `<html?> <head?> </head?> <body?> <p> "x" !Warning(dropped misplaced body start tag) "y" </p?> </body?> </html?>`},
		{htmlstream.HTML, `<template><body>x</template>y`, `<html?> <head?> <template> !Warning(dropped misplaced body start tag) "x" </template> </head?> <body?> "y" </body?> </html?>`},
		{htmlstream.HTML, "\uFEFF<p>x", `<html?> <head?> </head?> <body?> <p> "x" </p?> </body?> </html?>`},
		{htmlstream.HTML, "\uFEFFx", `<html?> <head?> </head?> <body?> "x" </body?> </html?>`},
		// leading newline in pre, listing and textarea
		{htmlstream.HTML, "<body><pre>\n\nx</pre><listing>\r\ny</listing><textarea>&#10;z</textarea><pre>\n</pre><div>\nw</div>", `<html?> <head?> </head?> <body> <pre> "\nx" </pre> <listing> "y" </listing> <textarea> "z" </textarea> <pre> </pre> <div> "\nw" </div> </body?> </html?>`},
		{htmlstream.Universal, "<pre>\nx</pre>", `<html?> <head?> </head?> <body?> <pre> "x" </pre> </body?> </html?>`},
		{htmlstream.Universal, "<pre><![CDATA[\nx]]></pre>", `<html?> <head?> </head?> <body?> <pre> "\nx" </pre> </body?> </html?>`},
		{htmlstream.XML, "<pre>\nx</pre>", `<{}pre> "\nx" </{}pre>`},
		// merged text, void elements, strays and SVG names
		{htmlstream.HTML, `<body><p>one<p>two</span>three<br>four<svg><foreignObject/><![CDATA[x]]>y</svg>`, `<html?> <head?> </head?> <body> <p> "one" </p?> <p> "two" !Warning(dropped stray </span>) "three" <br> </br?> "four" <svg:svg> <svg:foreignObject> </svg:foreignObject?> "xy" </svg:svg> </p?> </body?> </html?>`},
		{htmlstream.HTML, `<ul><li>a<li>b</ul></div><body class=x>z`, `<html?> <head?> </head?> <body?> <ul> <li> "a" </li?> <li> "b" </li?> </ul> !Warning(dropped stray </div>) !Warning(dropped misplaced body start tag) "z" </body?> </html?>`},
		{htmlstream.XML, `<?xml version="1.0"?><a xmlns="urn:a"><b>x<![CDATA[<y>]]>z</b><c/><d>unclosed</a>`, `!ProcInst(version="1.0") <{urn:a}a> <{urn:a}b> "x<y>z" </{urn:a}b> <{urn:a}c> </{urn:a}c?> <{urn:a}d> "unclosed" </{urn:a}d?> </{urn:a}a>`},
		{htmlstream.Universal, `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml"><body><p>x<br/>y<p>z</p></p><script src="a"/></body></html>`, `!ProcInst(version="1.0") <html> <body> <p> "x" <br> </br?> "y" <p> "z" </p> </p> <script> </script?> </body> </html>`},
		{htmlstream.Universal, `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml"><head><meta charset="utf-8"></meta></head><body><p>a<br></br>b<br>c</p></body></html>`, `!ProcInst(version="1.0") <html> <head> <meta> </meta> </head> <body> <p> "a" <br> </br> "b" <br> </br?> "c" </p> </body> </html>`},
		{htmlstream.Universal, `<SVG><linearGradient></lineargradient><foreignobject><p>x</p></foreignObject></svg><math><mi>y</MI></math>`, `<html?> <head?> </head?> <body?> <svg:svg> <svg:linearGradient> </svg:linearGradient> <svg:foreignObject> <p> "x" </p> </svg:foreignObject> </svg:svg> <math:math> <math:mi> "y" </math:mi> </math:math> </body?> </html?>`},
		{htmlstream.Universal, `<p>x</p></p><body>`, `<html?> <head?> </head?> <body?> <p> "x" </p> !Warning(dropped stray </p>) !Warning(dropped misplaced body start tag) </body?> </html?>`},
		{htmlstream.Universal, `<p><body>`, `<html?> <head?> </head?> <body?> <p> !Warning(dropped misplaced body start tag) </p?> </body?> </html?>`},
		{htmlstream.Universal, `<p>a<![CDATA[x`, `<html?> <head?> </head?> <body?> <p> "ax" !Warning(read the rest of the input as a CDATA section (HTML parsers would have ended it at the first >)) </p?> </body?> </html?>`},
		{htmlstream.Universal, `<x:p xmlns:x="urn:x"><![CDATA[x`, `<html?> <head?> </head?> <body?> <x:p> "x" !Warning(read the rest of the input as a CDATA section (HTML parsers would have ended it at the first >)) </x:p?> </body?> </html?>`},
		{htmlstream.Universal, `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml"><x:p xmlns:x="urn:x"><![CDATA[x`, `!ProcInst(version="1.0") <html> <{urn:x}p> "x" </{urn:x}p?> </html?>`},
		{htmlstream.XML, `<!DOCTYPE a [<!ENTITY x "y">]><a>&x;&amp;</a><!-- c -->`, `!Doctype(a [<!ENTITY x "y">]) <{}a> "&x;&" </{}a> !Comment( c )`},
		{htmlstream.XML, "<?pi  a\r\nb?><a/>", "!ProcInst(a\nb) <{}a> </{}a?>"},
		// empty comments, and </>, which HTML parsers ignore
		{htmlstream.HTML, `<p>a<!---->b<!>c</p>`, `<html?> <head?> </head?> <body?> <p> "a" !Comment() "b" !Comment() "c" </p> </body?> </html?>`},
		{htmlstream.XML, `<r><!----></r>`, `<{}r> !Comment() </{}r>`},
		{htmlstream.HTML, "<p>a</>b</p></><pre></>\nc</pre>", `<html?> <head?> </head?> <body?> <p> "ab" </p> <pre> "c" </pre> </body?> </html?>`},
		{htmlstream.XML, `<r>a</>b</r>`, `<{}r> "ab" </{}r>`},
		// tags cut off by the end of the input, and doctypes HTML parsers
		// ignore, kept under XML rules
		{htmlstream.HTML, `<p>a<b class="x`, `<html?> <head?> </head?> <body?> <p> "a" !Warning(dropped incomplete <b>) </p?> </body?> </html?>`},
		{htmlstream.HTML, `<p>a</p`, `<html?> <head?> </head?> <body?> <p> "a" !Warning(dropped incomplete </p>) </p?> </body?> </html?>`},
		{htmlstream.HTML, `<p>x<!DOCTYPE html>y`, `<html?> <head?> </head?> <body?> <p> "x" !Warning(dropped misplaced doctype) "y" </p?> </body?> </html?>`},
		{htmlstream.HTML, `<!DOCTYPE a><!DOCTYPE b>`, `!Doctype(a) !Warning(dropped misplaced doctype) <html?> <head?> </head?> <body?> </body?> </html?>`},
		{htmlstream.HTML, "\uFEFF <!--c--><!DOCTYPE html>", `" " !Comment(c) !Doctype(html) <html?> <head?> </head?> <body?> </body?> </html?>`},
		{htmlstream.Universal, `<!DOCTYPE a><!DOCTYPE b><p>`, `!Doctype(a) !Warning(dropped misplaced doctype) <html?> <head?> </head?> <body?> <p> </p?> </body?> </html?>`},
		{htmlstream.XML, `<!DOCTYPE a><!DOCTYPE b><a>x<!DOCTYPE c><b c="y`, `!Doctype(a) !Doctype(b) <{}a> "x" !Doctype(c) <{}b> </{}b?> </{}a?>`},
		// unbound prefixes
		{htmlstream.XML, `<x:a xmlns:y="urn:y"><x:b/><y:c/><a/></x:a>`, `<{}x:a> <{}x:b> </{}x:b?> <{urn:y}c> </{urn:y}c?> <{}a> </{}a?> </{}x:a>`},
		{htmlstream.Universal, `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml"><X:A><Svg:Rect/></X:A></html>`, `!ProcInst(version="1.0") <html> <{}X:A> <{}Svg:Rect> </{}Svg:Rect?> </{}X:A> </html>`},
		{htmlstream.Universal, `<X:A><Svg:Rect/></X:A>`, `<html?> <head?> </head?> <body?> <x:a> <svg:rect> </svg:rect?> </x:a> </body?> </html?>`},
		{htmlstream.HTML, `<x:a>`, `<html?> <head?> </head?> <body?> <x:a> </x:a?> </body?> </html?>`},
	} {
		_, s := newReader(c.mode, c.in)
		if got := dump(s); got != c.want {
			t.Errorf("%v %q:\n got  %s\n want %s", c.mode, c.in, got, c.want)
		}
		// a TokenReader returns the comments, also the empty ones
		_, s = newReader(c.mode, c.in)
		tr := NewTokenReader(s)
		comments := 0
		for {
			tok, err := tr.Token()
			if err != nil {
				break
			}
			if _, ok := tok.(xml.Comment); ok {
				comments++
			}
		}
		if want := strings.Count(c.want, "!Comment("); comments != want {
			t.Errorf("%v %q: TokenReader: %d comments, want %d", c.mode, c.in, comments, want)
		}
	}

	// the differences from HTML parsers listed in the package documentation,
	// with scripting disabled, and HTML mode with XML syntax
	for _, c := range []struct {
		xmlSyntax bool
		in, want  string
	}{
		{false, `<html> <head></head> <meta charset=x><title>t</title><p>x`, `<html> " " <head> </head> " " <meta> </meta?> <title> "t" </title> <body?> <p> "x" </p?> </body?> </html?>`},
		{false, `<body>x</body> y<p>z</p></html> w<!--c-->`, `<html?> <head?> </head?> <body> "x" " y" <p> "z" </p> " w" !Comment(c) </body?> </html?>`},
		{false, `<frameset><frame></frameset>`, `<html?> <head?> </head?> <body?> <frameset> <frame> </frame?> </frameset> </body?> </html?>`},
		{false, `<noscript><p>x</p></noscript>y`, `<html?> <head?> <noscript> <p> "x" </p> </noscript> </head?> <body?> "y" </body?> </html?>`},
		{false, `<noscript><body>x`, `<html?> <head?> <noscript> !Warning(dropped misplaced body start tag) "x" </noscript?> </head?> <body?> </body?> </html?>`},
		{false, `<html><head><noscript><body>x`, `<html> <head> <noscript> </noscript?> </head?> <body> "x" </body?> </html?>`},
		{true, "<pre><![CDATA[\nx]]>y</pre><pre>\n<![CDATA[\nz]]></pre>", `<html?> <head?> </head?> <body?> <pre> "\nxy" </pre> <pre> "\nz" </pre> </body?> </html?>`},
		// a CDATA section reaching the end of the input
		{true, `<p>a<![CDATA[ x <b>y</p>`, `<html?> <head?> </head?> <body?> <p> "a x <b>y</p>" !Warning(read the rest of the input as a CDATA section (HTML parsers would have ended it at the first >)) </p?> </body?> </html?>`},
		{true, `<svg><![CDATA[x`, `<html?> <head?> </head?> <body?> <svg:svg> "x" </svg:svg?> </body?> </html?>`},
	} {
		r, s := newReader(htmlstream.HTML, c.in)
		r.SetScripting(false)
		r.SetXMLSyntax(c.xmlSyntax)
		if got := dump(s); got != c.want {
			t.Errorf("%q, scripting disabled, XML syntax %v:\n got  %s\n want %s", c.in, c.xmlSyntax, got, c.want)
		}
	}
}

func TestFragment(t *testing.T) {
	for _, c := range []struct {
		mode htmlstream.Mode
		in   string
		want string
	}{
		{htmlstream.HTML, ``, ``},
		{htmlstream.HTML, " <!--c--><title>t</title><meta charset=x>a</body>b", `" " !Comment(c) <title> "t" </title> <meta> </meta?> "a" !Warning(dropped stray </body>) "b"`},
		{htmlstream.HTML, `<!DOCTYPE html>x`, `!Warning(dropped misplaced doctype) "x"`},
		{htmlstream.HTML, `<html><head></head><body><p>x`, `!Warning(dropped misplaced html start tag) !Warning(dropped misplaced head start tag) !Warning(dropped misplaced body start tag) <p> "x" </p?>`},
		{htmlstream.HTML, `<p>x</p><body class=a><head><title>t</title></head>y</body></html>z`, `<p> "x" </p> !Warning(dropped misplaced body start tag) !Warning(dropped misplaced head start tag) <title> "t" </title> !Warning(dropped stray </head>) "y" !Warning(dropped stray </html>) "z"`},
		{htmlstream.Universal, `<html><body>x</body></html>`, `!Warning(dropped misplaced html start tag) !Warning(dropped misplaced body start tag) "x"`},
		{htmlstream.Universal, `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml"><body>x</body></html>`, `!ProcInst(version="1.0") <html> <body> "x" </body> </html>`},
	} {
		_, s := newReader(c.mode, c.in)
		s.SetFragment(true)
		if got := dump(s); got != c.want {
			t.Errorf("%v %q:\n got  %s\n want %s", c.mode, c.in, got, c.want)
		}
	}
	_, s := newReader(htmlstream.HTML, `x`)
	s.Next()
	defer func() {
		if r := recover(); r != "sax: SetFragment called after Next" {
			t.Errorf("SetFragment after Next: panic %v", r)
		}
	}()
	s.SetFragment(true)
}

// options are the Reader options selected by the bits of opts in the fuzz
// target.
const (
	optStrict      = 1 << iota // SetStrict
	optNoScripting             // SetScripting(false)
	optXMLSyntax               // SetXMLSyntax
	optDepth                   // SetMaxDepth(3)
	optStackBytes              // SetMaxStackBytes(16)
	optChunk                   // SetChunkSize(3)
	optMaxText                 // SetMaxText(5)
	optTokenReader             // also decode with encoding/xml through a TokenReader
)

// fuzzCase is the input and the options of a FuzzSAX run.
type fuzzCase struct {
	in       string
	mode     htmlstream.Mode
	opts     byte
	split    int  // the calls to the Next method of the htmlstream.Reader before wrapping it
	fragment bool // SetFragment(true)
	valid    bool // the input has no parse errors, for HTML parsers
}

func (c *fuzzCase) String() string {
	return fmt.Sprintf("%v %q opts %#x split %d fragment %v", c.mode, c.in, c.opts, c.split, c.fragment)
}

// reader returns an htmlstream.Reader with the options of c, after c.split
// calls to its Next method.
func (c *fuzzCase) reader() *htmlstream.Reader {
	r := c.newReader()
	for range c.split {
		r.Next()
	}
	return r
}

// newReader returns an htmlstream.Reader with the options of c.
func (c *fuzzCase) newReader() *htmlstream.Reader {
	r := htmlstream.NewReader(strings.NewReader(c.in))
	r.SetMode(c.mode)
	r.SetScripting(c.opts&optNoScripting == 0)
	r.SetXMLSyntax(c.opts&optXMLSyntax != 0)
	if c.opts&optDepth != 0 {
		r.SetMaxDepth(3)
	}
	if c.opts&optStackBytes != 0 {
		r.SetMaxStackBytes(16)
	}
	if c.opts&optChunk != 0 {
		r.SetChunkSize(3)
	}
	return r
}

// sax returns a Reader of r with the options of c.
func (c *fuzzCase) sax(r *htmlstream.Reader) *Reader {
	s := NewReader(r)
	s.SetStrict(c.opts&optStrict != 0)
	s.SetFragment(c.fragment)
	if c.opts&optMaxText != 0 {
		s.SetMaxText(5)
	}
	return s
}

// refTokens returns, from the token after the first skip ones of r:
//   - the decoded text of the Text and CDATA tokens, without a byte order
//     mark at the start of the input, and without the newline HTML parsers
//     drop after a pre, listing or textarea start tag, also after a </>
//     there, which they ignore (except after a start tag before the first
//     token read, at the start of a CDATA section, and in untracked
//     content);
//   - the decoded text of the Comment tokens outside untracked content;
//   - the start tags, end tags, processing instructions and doctypes with
//     markup.
func refTokens(r *htmlstream.Reader, skip int) (text, comments []byte, tags []*htmlstream.Token) {
	untracked, dropLF := 0, false
	for i := 0; r.Next(); i++ {
		t := r.Token()
		if t.Kind() == htmlstream.Comment && !t.Continued() && string(t.Raw()) == "</>" {
			continue
		}
		lf := dropLF
		dropLF = false
		if i >= skip && len(t.Raw()) != 0 {
			switch t.Kind() {
			case htmlstream.StartTag, htmlstream.EndTag, htmlstream.ProcInst, htmlstream.Doctype:
				tags = append(tags, t.Clone())
			}
		}
		if untracked > 0 || t.Kind() == htmlstream.StartTag && t.Untracked() {
			switch t.Kind() {
			case htmlstream.StartTag:
				if t.Opens() {
					untracked++
				}
			case htmlstream.EndTag:
				if !t.Stray() && untracked > 0 {
					untracked--
				}
			case htmlstream.Text, htmlstream.CDATA:
				if i >= skip {
					text = t.AppendText(text)
				}
			}
			continue
		}
		switch t.Kind() {
		case htmlstream.Text, htmlstream.CDATA:
			if i < skip {
				break
			}
			n := len(text)
			text = t.AppendText(text)
			if lf && t.Kind() == htmlstream.Text && len(text) > n && text[n] == '\n' {
				text = append(text[:n], text[n+1:]...)
			} else if t.Pos() == 0 && strings.HasPrefix(string(text[n:]), "\uFEFF") {
				text = append(text[:n], text[n+3:]...)
			}
		case htmlstream.Comment:
			if i >= skip {
				comments = t.AppendText(comments)
			}
		case htmlstream.StartTag:
			dropLF = i >= skip && t.Opens() && t.HTMLRules() && t.NS() == htmlstream.NamespaceXHTML && (t.Is("pre") || t.Is("listing") || t.Is("textarea"))
		}
	}
	return text, comments, tags
}

// stackPath returns the names of the open elements of r, outermost first,
// without the element of the current token.
func stackPath(r *htmlstream.Reader) []Name {
	t := r.Token()
	if t == nil {
		return nil
	}
	skip := t.Kind() == htmlstream.StartTag && t.Opens() && !t.Untracked()
	var path []Name
	for e := range r.Stack() {
		if skip {
			skip = false
			continue
		}
		local := string(e.AppendLocalName(nil))
		if p := e.Prefix(); p != nil && e.NS() == "" {
			if e.HTMLRules() {
				p = ascii.AppendLower(nil, p)
			}
			local = string(p) + ":" + local
		}
		if e.HTMLRules() && e.NS() == htmlstream.NamespaceSVG {
			local = htmlstream.AdjustSVGTagName(local)
		}
		path = append(path, Name{e.NS(), local})
	}
	slices.Reverse(path)
	return path
}

type openElem struct {
	name Name
	pos  htmlstream.Pos // the start tag, -1 if implied, -2 if open at the start
}

// record is an event, as recorded by checkEvents.
type record struct {
	kind      Kind
	name      Name
	data      string
	pos       htmlstream.Pos
	partial   bool
	depth     int  // len(Path())
	untracked int  // the open elements in untracked content
	tag       bool // an Untracked event of a start or end tag
	text      bool // an Untracked event of a Text or CDATA token
	template  bool // an Untracked event of a template start tag
}

func (r record) String() string {
	return fmt.Sprintf("%v %v %q @%d", r.kind, r.name, r.data, r.pos)
}

// checkEvents reads the events of c and checks that:
//   - every StartElement event has exactly one EndElement event with the
//     same name, whose token (if any) closes the element of the start tag,
//     and Path has the open elements, starting with those of the
//     htmlstream.Reader when wrapped;
//   - Partial events are continued by events of the same kind (or stopped by
//     a SyntaxError event), and positions don't decrease;
//   - without an error, the text of the Text events and of the Text and
//     CDATA tokens of the Untracked events is the decoded text of the input,
//     the comments are those of the input, and every start tag, end tag,
//     processing instruction and doctype is the token of an event, except
//     for the end tags of dropped elements and the body and html end tags
//     in HTML mode, except for fragments;
//   - valid input gives no Warning or SyntaxError event, and every Warning
//     event has a token, except for that of a CDATA section reaching the
//     end of the input;
//   - a TokenReader returns the same elements and text, up to an error at
//     the first untracked tag, which encoding/xml reads without an error;
//   - Skip, ReadText and Walk agree with the events.
func checkEvents(t *testing.T, c *fuzzCase) {
	r := c.reader()
	seed := stackPath(r)
	keep := c.mode != htmlstream.XML && !c.fragment // html and body kept open
	synth := keep && r.Token() == nil
	s := c.sax(r)
	strict := c.opts&optStrict != 0
	maxText := DefaultMaxText
	if c.opts&optMaxText != 0 {
		maxText = 5
	}
	var (
		stack     []openElem
		text      []byte
		comments  []byte
		prev      Kind
		partial   bool
		started   bool
		n         int
		lastPos   htmlstream.Pos
		accounted = map[htmlstream.Pos]bool{} // the positions of the tokens of events
		dropped   = map[htmlstream.Pos]bool{} // the positions of dropped start tags
		records   []record
	)
	fail := func(format string, args ...any) {
		t.Helper()
		t.Fatalf("%v: event %d: %s", c, n, fmt.Sprintf(format, args...))
	}
	checkPath := func() {
		t.Helper()
		path := s.Path()
		if len(path) != len(stack) || s.Depth() != len(path) {
			fail("path %v, open %v", path, stack)
		}
		for i, e := range stack {
			if path[i] != e.name {
				fail("path %v, open %v", path, stack)
			}
		}
	}
	for ev := range s.All() {
		n++
		if !started {
			started = true
			for _, name := range seed {
				stack = append(stack, openElem{name, -2})
			}
		}
		if partial && ev.Kind != prev && ev.Kind != SyntaxError {
			fail("%v after a Partial %v", ev.Kind, prev)
		}
		prev, partial = ev.Kind, ev.Partial
		if ev.Partial && ev.Kind != Text && ev.Kind != Comment && ev.Kind != Untracked {
			fail("Partial %v", ev.Kind)
		}
		if ev.Pos < lastPos {
			fail("position %d after %d", ev.Pos, lastPos)
		}
		lastPos = ev.Pos
		if tok := ev.Token(); tok != nil {
			accounted[tok.Pos()] = true
		}
		rec := record{kind: ev.Kind, name: ev.Name, data: string(ev.Text), pos: ev.Pos, partial: ev.Partial, depth: s.Depth(), untracked: s.untracked}
		switch ev.Kind {
		case StartElement:
			e := openElem{ev.Name, -1}
			if ev.Token() == nil && s.untracked > 0 {
				fail("implied %v in untracked content", ev.Name)
			}
			if tok := ev.Token(); ev.Implied {
				if !synth || tok != nil || ev.Name != nameHTML && ev.Name != nameHead && ev.Name != nameBody {
					fail("implied %v", ev.Name)
				}
			} else if tok == nil || tok.Kind() != htmlstream.StartTag || tok.Pos() != ev.Pos {
				fail("start %v has token %v", ev.Name, tok)
			} else {
				e.pos = tok.Pos()
			}
			stack = append(stack, e)
			checkPath()
		case EndElement:
			checkPath()
			if len(stack) == 0 {
				fail("end %v without start", ev.Name)
			}
			e := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if ev.Name != e.name {
				fail("end %v closes %v", ev.Name, e.name)
			}
			switch tok := ev.Token(); {
			case tok == nil:
				if s.untracked > 0 {
					fail("implied end %v in untracked content", ev.Name)
				}
				// the html and body elements are kept open until the end
				kept := keep && (e.name == nameHTML || e.name == nameBody)
				if e.pos != -1 && !kept || !ev.Implied {
					fail("end %v without a token", ev.Name)
				}
			case tok.Kind() == htmlstream.StartTag:
				if tok.Pos() != e.pos || !ev.Implied || tok.Opens() {
					fail("void end %v has token %v", ev.Name, tok)
				}
			case tok.Kind() == htmlstream.EndTag && tok.Stray():
				// the end tag of an implied head
				if e.pos != -1 || e.name != nameHead || ev.Implied || !tok.Is("head") {
					fail("end %v has stray token %v", ev.Name, tok)
				}
			case tok.Kind() == htmlstream.EndTag:
				if e.pos != -2 && tok.StartPos() != e.pos || ev.Implied != tok.Implied() {
					fail("end %v (start %d) has token %v (start %d)", ev.Name, e.pos, tok, tok.StartPos())
				}
			default:
				fail("end %v has token %v", ev.Name, tok)
			}
		case Text:
			if len(ev.Text) == 0 {
				fail("empty text")
			}
			if len(ev.Text) > max(maxText, 4) {
				fail("text of %d bytes", len(ev.Text))
			}
			text = append(text, ev.Text...)
		case Comment:
			if len(ev.Text) > max(maxText, 4) {
				fail("comment of %d bytes", len(ev.Text))
			}
			comments = append(comments, ev.Text...)
		case Untracked:
			tok := ev.Token()
			if tok == nil {
				fail("untracked without a token")
			}
			switch tok.Kind() {
			case htmlstream.Text, htmlstream.CDATA:
				text = append(text, ev.Text...)
				rec.text = true
			case htmlstream.StartTag:
				rec.tag = true
				rec.template = tok.Opens() && tok.NS() == htmlstream.NamespaceXHTML && tok.Is("template")
			case htmlstream.EndTag:
				rec.tag = true
			}
		case Warning:
			if strict || ev.Token() == nil && string(ev.Text) != cdataEOFWarning || len(ev.Text) == 0 {
				fail("warning %q", ev.Text)
			}
			if c.valid {
				fail("warning %q at %d in valid input", ev.Text, ev.Pos)
			}
			if tok := ev.Token(); tok != nil {
				dropped[tok.Pos()] = true
			}
		case SyntaxError:
			if !strict || s.Err() == nil {
				fail("syntax error %q, error %v", ev.Text, s.Err())
			}
			if c.valid {
				fail("syntax error %q at %d in valid input", ev.Text, ev.Pos)
			}
		}
		records = append(records, rec)
	}
	err := s.Err()
	if errors.Is(err, htmlstream.ErrUnsupportedEncoding) && n == 0 {
		return
	}
	if err != nil {
		var se *htmlstream.SyntaxError
		if !errors.As(err, &se) || !strict || prev != SyntaxError {
			fail("error %v after %v", err, prev)
		}
	} else {
		if len(stack) != 0 || partial {
			fail("open at the end: %v, partial %v", stack, partial)
		}
		wantText, wantComments, tags := refTokens(c.newReader(), max(c.split-1, 0))
		if string(text) != string(wantText) {
			fail("text\n %q, want\n %q", text, wantText)
		}
		if string(comments) != string(wantComments) {
			fail("comments\n %q, want\n %q", comments, wantComments)
		}
		for _, tok := range tags {
			switch {
			case accounted[tok.Pos()]:
			case tok.Kind() == htmlstream.EndTag && dropped[tok.StartPos()]:
			case tok.Kind() == htmlstream.EndTag && keep && (tok.Is("body") || tok.Is("html")):
			default:
				fail("token %q at %d without an event", tok.Raw(), tok.Pos())
			}
		}
	}
	checkTokenReader(t, c, records, err)
	checkReadText(t, c, records)
	checkWalk(t, c, records, err)
}

// end returns the index of the EndElement record of the element at depth
// containing the record at i, or of a SyntaxError record before it, or
// len(records).
func end(records []record, i, depth int) int {
	for j := i + 1; j < len(records); j++ {
		switch records[j].kind {
		case EndElement:
			if records[j].depth == depth {
				return j
			}
		case SyntaxError:
			return j
		}
	}
	return len(records)
}

// selected returns a number from 0 to 6 for the record at i, selecting the
// operations of checkReadText and checkWalk.
func selected(c *fuzzCase, i int) int { return (i*31 + int(c.opts)) % 7 }

// checkTokenReader checks that a TokenReader returns the elements and text
// of the records, and the same error, or ErrUntracked at the first
// untracked tag.
func checkTokenReader(t *testing.T, c *fuzzCase, records []record, err error) {
	var wantText []byte
	wantErr := err
	if wantErr == nil {
		wantErr = io.EOF
	}
	var want []string
	for _, rec := range records {
		if rec.tag {
			wantErr = ErrUntracked
			break
		}
		switch rec.kind {
		case StartElement:
			want = append(want, "<"+rec.name.Local)
		case EndElement:
			want = append(want, "</"+rec.name.Local)
		case Text:
			wantText = append(wantText, rec.data...)
		case Untracked:
			if rec.text {
				wantText = append(wantText, rec.data...)
			}
		}
	}
	tr := NewTokenReader(c.sax(c.reader()))
	var got []string
	var text []byte
	for {
		tok, err := tr.Token()
		if err != nil {
			if err.Error() != wantErr.Error() {
				t.Fatalf("%v: TokenReader: error %v, want %v", c, err, wantErr)
			}
			break
		}
		switch tok := tok.(type) {
		case xml.StartElement:
			got = append(got, "<"+tok.Name.Local)
		case xml.EndElement:
			got = append(got, "</"+tok.Name.Local)
		case xml.CharData:
			text = append(text, tok...)
		}
	}
	if !slices.Equal(got, want) || string(text) != string(wantText) {
		t.Fatalf("%v: TokenReader:\n %v %q\nwant\n %v %q", c, got, text, want, wantText)
	}
	if wantErr == io.EOF && c.split == 0 && c.opts&optTokenReader != 0 {
		d := xml.NewTokenDecoder(NewTokenReader(c.sax(c.reader())))
		for {
			if _, err := d.Token(); err == io.EOF {
				break
			} else if err != nil {
				t.Fatalf("%v: encoding/xml: %v", c, err)
			}
		}
	}
}

// readText returns the text ReadText reads from the records from i to j,
// without template contents.
func readText(records []record, i, j int) string {
	var b strings.Builder
	for k := i; k < j; k++ {
		switch rec := records[k]; {
		case rec.kind == StartElement && rec.name == nameTemplate:
			k = end(records, k, rec.depth)
		case rec.kind == Untracked && rec.template:
			for k++; k < j && !(records[k].kind == Untracked && records[k].tag && records[k].untracked == rec.untracked-1); k++ {
			}
		case rec.kind == Text, rec.kind == Untracked && rec.text:
			b.WriteString(rec.data)
		}
	}
	return b.String()
}

// checkReadText checks that ReadText and Skip at selected events read the
// text and skip to the events of the records.
func checkReadText(t *testing.T, c *fuzzCase, records []record) {
	s := c.sax(c.reader())
	at := func(i int, op string) {
		t.Helper()
		ev := s.Event()
		if i >= len(records) {
			if ev.Kind != 0 {
				t.Fatalf("%v: after %s: %v, want the end", c, op, ev.Kind)
			}
			return
		}
		if rec := records[i]; ev.Kind != rec.kind || ev.Name != rec.name || ev.Pos != rec.pos {
			t.Fatalf("%v: after %s: %v %v @%d, want %v (record %d)", c, op, ev.Kind, ev.Name, ev.Pos, rec, i)
		}
	}
	for i := 0; i < len(records); i++ {
		if !s.Next() {
			t.Fatalf("%v: %d events, want %d", c, i, len(records))
		}
		at(i, "Next")
		rec := records[i]
		switch sel := selected(c, i); {
		case rec.kind == StartElement && sel == 0:
			j := end(records, i, rec.depth)
			want := ""
			if rec.name != nameTemplate {
				want = readText(records, i+1, j)
			}
			b, truncated := s.ReadText(nil, 0)
			at(j, "ReadText")
			if string(b) != want || truncated {
				t.Fatalf("%v: ReadText at record %d: %q, want %q", c, i, b, want)
			}
			i = j
		case rec.kind == StartElement && sel == 1:
			j := end(records, i, rec.depth)
			s.Skip()
			at(j, "Skip")
			i = j
		case rec.kind == Text && sel == 2:
			j := i
			for records[j].partial && j+1 < len(records) && records[j+1].kind == Text {
				j++
			}
			want := readText(records, i, j+1)
			if records[j].partial {
				j++ // a SyntaxError event
			}
			b, _ := s.ReadText(nil, 0)
			at(j, "ReadText at text")
			if string(b) != want {
				t.Fatalf("%v: ReadText at record %d: %q, want %q", c, i, b, want)
			}
			i = j
		}
	}
	if s.Next() {
		t.Fatalf("%v: more than %d events", c, len(records))
	}
}

// checkWalk checks that Walk calls the functions for the records, with
// SkipElement returned at selected events.
func checkWalk(t *testing.T, c *fuzzCase, records []record, err error) {
	var want []int
	var wantErr error = err
	for i := 0; i < len(records); i++ {
		want = append(want, i)
		if selected(c, i) != 3 {
			continue
		}
		rec := records[i]
		depth := rec.depth
		if rec.kind == EndElement {
			depth--
		}
		if depth == 0 {
			wantErr = nil
			break
		}
		i = end(records, i, depth) - 1
	}
	var got []int
	next := 0 // the first record not delivered yet
	fn := func(s *Reader, ev *Event) error {
		i := next
		for ; i < len(records); i++ {
			if rec := records[i]; ev.Kind == rec.kind && ev.Name == rec.name && ev.Pos == rec.pos && s.Depth() == rec.depth && string(ev.Text) == rec.data {
				break
			}
		}
		got, next = append(got, i), i+1
		if selected(c, i) == 3 {
			return SkipElement
		}
		return nil
	}
	h := Handler{fn, fn, fn, fn, fn, fn, fn, fn, fn}
	if err := c.sax(c.reader()).Walk(h); fmt.Sprint(err) != fmt.Sprint(wantErr) || !slices.Equal(got, want) {
		t.Fatalf("%v: Walk: %v, error %v\nwant %v, error %v", c, got, err, want, wantErr)
	}
}

// FuzzSAX checks the events of the input in a mode selected by mode, with
// the Reader options of the bits of opts, wrapping the Reader after the
// number of tokens in the low 3 bits of split, and reading a fragment if its
// high bit is set (see checkEvents). The seeds are the inputs in
// testdata/xml, testdata/polyglot and testdata/misc, and the html5lib-tests
// cases without parse errors in testdata/html5libgo, which are checked for
// Warning events.
func FuzzSAX(f *testing.F) {
	var seeds []string
	for _, c := range xml_testdata.Cases("*.txtar", "in", "tokens") {
		in, _ := c.Section("in")
		seeds = append(seeds, in)
	}
	for _, c := range polyglot_testdata.Cases("*.txtar", "in", "out", "warn") {
		in, _ := c.Section("in")
		seeds = append(seeds, in)
	}
	seeds = append(seeds, vectors()...)
	for i, in := range seeds {
		f.Add(in, byte(i%3), byte(i*37), byte(i%5)|byte(i%2)<<7)
	}
	// the Warning of a CDATA section reaching the end of the input after a
	// Text event in several parts
	f.Add("<![CDATA[00", byte(htmlstream.Universal), byte(0x74), byte(2))
	// the newline after a pre start tag and </>, wrapping the Reader before
	// and after the </>
	f.Add("0<!>0<pre></>\n</pre>", byte(htmlstream.HTML), byte(0xf7), byte(4))
	f.Add("0<!>0<pre></>\n</pre>", byte(htmlstream.HTML), byte(0xf7), byte(5))
	// the implied head and body elements in untracked content (an untracked
	// html element, head element and noscript element in an implied head),
	// where a whitespace Text token split by the chunk size is continued by
	// the token starting the body
	f.Add("<html Xmlns:0=000000>   0", byte(htmlstream.HTML), byte(0xb2), byte(0))
	f.Add("<html><head xmlns:0=000000>   0", byte(htmlstream.HTML), byte(0x32), byte(0))
	f.Add("<noscript xmlns:0=000000><div>   0", byte(htmlstream.HTML), byte(0x32), byte(0))
	f.Add("<html xmlns:0=000000></html>0", byte(htmlstream.HTML), byte(0x12), byte(0))
	// the inputs without parse errors, and the scripting setting (0 for
	// both)
	valid := map[string]int{}
	i := 0
	for c := range html5libgo_testdata.TreeTests() {
		in, v := c.Input, 0
		switch c.Scripting {
		case "on":
			v = 1
		case "off":
			v = -1
		}
		if old, ok := valid[in]; ok && old != v {
			v = 2 // different documents
		}
		valid[in] = v
		opts := byte(i*37) &^ (optDepth | optStackBytes | optXMLSyntax | optNoScripting)
		if v == -1 {
			opts |= optNoScripting
		}
		f.Add(in, byte(htmlstream.HTML), opts, byte(0))
		i++
	}
	f.Fuzz(func(t *testing.T, in string, mode, opts, split byte) {
		c := &fuzzCase{in: in, mode: htmlstream.Mode(mode % 3), opts: opts, split: int(split & 7), fragment: split&0x80 != 0}
		if v, ok := valid[in]; ok && c.mode == htmlstream.HTML && c.split == 0 && !c.fragment && opts&(optDepth|optStackBytes|optXMLSyntax) == 0 {
			noScripting := opts&optNoScripting != 0
			c.valid = v == 0 || v == 1 && !noScripting || v == -1 && noScripting
		}
		checkEvents(t, c)
	})
}
