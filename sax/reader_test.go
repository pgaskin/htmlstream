package sax

import (
	"encoding/xml"
	"errors"
	"slices"
	"strings"
	"testing"
	"testing/iotest"
	"unicode/utf8"

	"github.com/pgaskin/htmlstream"
	"github.com/pgaskin/htmlstream/htmlutil"
)

func TestPartial(t *testing.T) {
	in := "<p>" + strings.Repeat("aé&amp;", 20000) + "<![CDATA[x]]>y</p><!--" + strings.Repeat("c", 100) + "-->"
	want := strings.Repeat("aé&", 20000) + "xy"
	for _, chunk := range []int{1, 7, 1000, 0} {
		for _, maxText := range []int{1, 2, 3, 100, 64 << 10, 0} {
			r, s := newReader(htmlstream.XML, in)
			if chunk != 0 {
				r.SetChunkSize(chunk)
			}
			if maxText != 0 {
				s.SetMaxText(maxText)
			} else {
				maxText = DefaultMaxText
			}
			var text, comment []byte
			var texts, comments int
			partial := false
			for ev := range s.All() {
				switch ev.Kind {
				case Text:
					text = append(text, ev.Text...)
					texts++
				case Comment:
					comment = append(comment, ev.Text...)
					comments++
				default:
					if partial {
						t.Fatalf("chunk %d, max %d: %v after a Partial event", chunk, maxText, ev.Kind)
					}
					continue
				}
				if len(ev.Text) > max(maxText, 2) || !utf8.Valid(ev.Text) {
					t.Fatalf("chunk %d, max %d: event of %d bytes: %q", chunk, maxText, len(ev.Text), ev.Text)
				}
				if ev.Partial && len(ev.Text) < maxText-3 {
					t.Fatalf("chunk %d, max %d: Partial event of %d bytes", chunk, maxText, len(ev.Text))
				}
				partial = ev.Partial
			}
			if s.Err() != nil || string(text) != want || string(comment) != strings.Repeat("c", 100) {
				t.Fatalf("chunk %d, max %d: text %d bytes, comment %q, error %v", chunk, maxText, len(text), comment, s.Err())
			}
			if maxText > 4 && texts > len(want)/(maxText-3)+1 {
				t.Errorf("chunk %d, max %d: %d text events", chunk, maxText, texts)
			}
			if wantComments := (100 + maxText - 1) / maxText; comments != wantComments {
				t.Errorf("chunk %d, max %d: %d comment events, want %d", chunk, maxText, comments, wantComments)
			}
		}
	}
	// the last chunk of a bogus comment can have the Raw of </>
	for chunk := 1; chunk <= 8; chunk++ {
		r := htmlstream.NewReader(iotest.OneByteReader(strings.NewReader("<p>a</00</>b</p>")))
		r.SetChunkSize(chunk)
		var text, comment []byte
		for ev := range NewReader(r).All() {
			switch ev.Kind {
			case Text:
				text = append(text, ev.Text...)
			case Comment:
				comment = append(comment, ev.Text...)
			}
		}
		if string(text) != "ab" || string(comment) != "00</" {
			t.Errorf("chunk %d: text %q, comment %q", chunk, text, comment)
		}
	}
}

func TestUntracked(t *testing.T) {
	const in = `<a><b><c x=1><d>x<e>y</e><!--z--></d>w</c>v</b><b><c>u</a>`
	r, s := newReader(htmlstream.XML, in)
	r.SetMaxDepth(2)
	var paths []int
	got := ""
	for ev := range s.All() {
		if ev.Kind == Untracked {
			paths = append(paths, s.Depth())
		}
		got += " " + dumpEvent(ev)
	}
	// the untracked c is closed by </a>, whatever its name
	const want = ` <{}a> <{}b> ~<c x=1> ~<d> ~x ~<e> ~y ~</e> ~<!--z--> ~</d> ~w ~</c> "v" </{}b> <{}b> ~<c> ~u ~</a> </{}b?> </{}a?>`
	if got != want || s.Err() != nil {
		t.Errorf("got  %s\nwant %s\nerror %v", got, want, s.Err())
	}
	if slices.ContainsFunc(paths, func(n int) bool { return n != 2 }) {
		t.Errorf("Untracked events at depths %v, want 2", paths)
	}

	r, s = newReader(htmlstream.XML, in)
	r.SetMaxDepth(1)
	s.Next()
	if text, _ := s.ReadText(nil, 0); string(text) != "xywvu" {
		t.Errorf("ReadText: %q", text)
	}

	r, s = newReader(htmlstream.XML, in)
	r.SetMaxDepth(2)
	s.Next()
	s.Next()
	s.Next()
	if s.Skip(); s.Err() != nil || dumpEvent(s.Event()) != "~</c>" || s.Next() && dumpEvent(s.Event()) != `"v"` {
		t.Errorf("Skip at an untracked start tag: %v, %s", s.Err(), dumpEvent(s.Event()))
	}

	var v struct {
		B []string `xml:"b"`
	}
	r, s = newReader(htmlstream.XML, in)
	r.SetMaxDepth(2)
	tr := NewTokenReader(s)
	if err := xml.NewTokenDecoder(tr).Decode(&v); err != ErrUntracked || dumpEvent(s.Event()) != "~<c x=1>" {
		t.Errorf("TokenReader: %q, %v at %s", v.B, err, dumpEvent(s.Event()))
	}
	if _, err := tr.Token(); err != ErrUntracked {
		t.Errorf("TokenReader after the error: %v", err)
	}
}

// dumpEvent returns the event as dump does, but with the text of Untracked
// Text tokens unquoted.
func dumpEvent(ev *Event) string {
	var b strings.Builder
	switch ev.Kind {
	case Untracked:
		if raw := ev.Token().Raw(); len(raw) != 0 {
			if k := ev.Token().Kind(); k == htmlstream.Text {
				return "~" + string(ev.Text)
			}
			return "~" + string(raw)
		}
		return "~implied"
	case Text:
		return `"` + string(ev.Text) + `"`
	case StartElement, EndElement:
		b.WriteByte('<')
		if ev.Kind == EndElement {
			b.WriteByte('/')
		}
		if ev.Name.Space == "" {
			b.WriteString("{}")
		}
		b.WriteString(ev.Name.Local)
		if ev.Implied {
			b.WriteByte('?')
		}
		b.WriteByte('>')
		return b.String()
	}
	return "!" + ev.Kind.String() + "(" + string(ev.Text) + ")"
}

func TestStrict(t *testing.T) {
	for _, c := range []struct {
		mode        htmlstream.Mode
		in          string
		lax, strict string
	}{
		{htmlstream.XML, `<a>ok</a>`, `<{}a> "ok" </{}a>`, `<{}a> "ok" </{}a>`},
		{htmlstream.XML, `<a><b>x</a>`,
			`<{}a> <{}b> "x" </{}b?> </{}a>`,
			`<{}a> <{}b> "x" !SyntaxError(element "b" at offset 3 closed without its end tag) ERR htmlstream: syntax error at offset 7: element "b" at offset 3 closed without its end tag`},
		{htmlstream.XML, `<a>x</b>y</a>`,
			`<{}a> "x" !Warning(dropped stray </b>) "y" </{}a>`,
			`<{}a> "x" !SyntaxError(dropped stray </b>) ERR htmlstream: syntax error at offset 4: dropped stray </b>`},
		{htmlstream.HTML, `<p>x<body>y`,
			`<html?> <head?> </head?> <body?> <p> "x" !Warning(dropped misplaced body start tag) "y" </p?> </body?> </html?>`,
			`<html?> <head?> </head?> <body?> <p> "x" !SyntaxError(dropped misplaced body start tag) ERR htmlstream: syntax error at offset 4: dropped misplaced body start tag`},
		{htmlstream.HTML, `<p>a</p>b<a href="x>c`,
			`<html?> <head?> </head?> <body?> <p> "a" </p> "b" !Warning(dropped incomplete <a>) </body?> </html?>`,
			`<html?> <head?> </head?> <body?> <p> "a" </p> "b" !SyntaxError(start tag "a" is cut off by the end of the input) ERR htmlstream: syntax error at offset 9: start tag "a" is cut off by the end of the input`},
	} {
		_, s := newReader(c.mode, c.in)
		if got := dump(s); got != c.lax {
			t.Errorf("%v %q:\n got  %s\n want %s", c.mode, c.in, got, c.lax)
		}
		_, s = newReader(c.mode, c.in)
		s.SetStrict(true)
		if got := dump(s); got != c.strict {
			t.Errorf("%v %q strict:\n got  %s\n want %s", c.mode, c.in, got, c.strict)
		}
		if err := s.Err(); err != nil {
			var se *htmlstream.SyntaxError
			if !errors.As(err, &se) || s.Next() || s.Event().Kind != 0 || s.Err() != err {
				t.Errorf("%v %q strict: error %v after the end", c.mode, c.in, err)
			}
		}
	}
}

// TestMidStreamKept checks the html and body end tags after the Reader
// starts in the middle of the input: in HTML mode, html and body elements
// open at the start stay open after their end tags, and the end tags of
// implied ones are stray (also in Universal mode in documents read as HTML);
// under XML rules, the end tags close them.
func TestMidStreamKept(t *testing.T) {
	for _, c := range []struct {
		mode     htmlstream.Mode
		in, find string
		want     string
	}{
		{htmlstream.HTML, `<html><body><div><p>a</div></body>x</html>c<p>d`, "p",
			`<p> "a" </p?> </div> "x" "c" <p> "d" </p?> </body?> </html?>`},
		{htmlstream.HTML, `<title>t</title><div>a</div></body></html>c`, "div",
			`<div> "a" </div> !Warning(dropped stray </body>) !Warning(dropped stray </html>) "c"`},
		{htmlstream.XML, `<html><body><div>a</div></body>x</html>`, "div",
			`<div> "a" </div> </body> "x" </html>`},
		{htmlstream.Universal, `<html><body><div>a</div></body>x</html>c`, "div",
			`<div> "a" </div> "x" "c" </body?> </html?>`},
		{htmlstream.Universal, `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml"><body><div>a</div></body>x</html>c`, "div",
			`<div> "a" </div> </body> "x" </html> "c"`},
	} {
		r := htmlstream.NewReader(strings.NewReader(c.in))
		r.SetMode(c.mode)
		r.Find(htmlstream.Tag(c.find))
		s := NewReader(r)
		s.Next()
		got := strings.ReplaceAll(dumpEvent(s.Event())+" "+dump(s), "{}", "")
		if got != c.want {
			t.Errorf("%s %q:\n got %s\nwant %s", c.mode, c.in, got, c.want)
		}
	}
}

func TestMidStream(t *testing.T) {
	const in = `<html><body><div id=x><p>a<p>b</div><p>c`
	for _, c := range []struct {
		find func(r *htmlstream.Reader)
		path string
		want string
	}{
		{func(r *htmlstream.Reader) { r.Find(htmlstream.Tag("div")) }, "html body div",
			`<div> <p> "a" </p?> <p> "b" </p?> </div> <p> "c" </p?> </body?> </html?>`},
		{func(r *htmlstream.Reader) { r.Find(htmlstream.Tag("p")); r.Next() }, "html body div p",
			`"a" </p?> <p> "b" </p?> </div> <p> "c" </p?> </body?> </html?>`},
		{func(r *htmlstream.Reader) { r.Find(htmlstream.Tag("p")); r.Next(); r.Next() }, "html body div p",
			`</p?> <p> "b" </p?> </div> <p> "c" </p?> </body?> </html?>`},
	} {
		r := htmlstream.NewReader(strings.NewReader(in))
		c.find(r)
		s := NewReader(r)
		s.Next()
		var path []string
		for _, n := range s.Path() {
			path = append(path, n.Local)
		}
		got := dumpEvent(s.Event()) + " " + dump(s)
		got = strings.ReplaceAll(got, "{}", "")
		if strings.Join(path, " ") != c.path || got != c.want {
			t.Errorf("path %v, events\n %s\nwant %s\n %s", path, got, c.path, c.want)
		}
	}

	// decode the elements found one at a time
	type item struct {
		ID   string `xml:"id,attr"`
		Text string `xml:",chardata"`
	}
	r := htmlstream.NewReader(strings.NewReader(`<feed><x><item id="1">a</item><item id="2">b<b/></item><item id="3"/></x></feed>`))
	r.SetMode(htmlstream.XML)
	var items []item
	for r.Find(htmlstream.Tag("item")) {
		var it item
		if err := xml.NewTokenDecoder(NewTokenReader(NewReader(r))).Decode(&it); err != nil {
			t.Fatal(err)
		}
		items = append(items, it)
	}
	if r.Err() != nil || !slices.Equal(items, []item{{"1", "a"}, {"2", "b"}, {"3", ""}}) {
		t.Errorf("items %v, error %v", items, r.Err())
	}

	// at a </>, which gives no event
	r = htmlstream.NewReader(strings.NewReader("<p>a</>b</p>"))
	r.Find(htmlstream.Tag("p"))
	r.Next()
	r.Next()
	if got := dump(NewReader(r)); got != `"b" </p>` {
		t.Errorf("at </>: %s", got)
	}

	// inside untracked content
	r = htmlstream.NewReader(strings.NewReader(`<a><b><c>x</c>y<d/></b>z</a>`))
	r.SetMode(htmlstream.XML)
	r.SetMaxDepth(1)
	r.Find(htmlstream.Tag("c"))
	s := NewReader(r)
	if got := dump(s); got != `~<c> ~x ~</c> ~y ~<d/> ~</b> "z" </{}a>` {
		t.Errorf("untracked: %s", got)
	}
}

func TestSkipReadText(t *testing.T) {
	_, s := newReader(htmlstream.HTML, `<div><p>a<b>b</b>c</p><p>d&eacute;<!--x--></div>e<br>`)
	for s.Next() && s.Event().Name.Local != "div" {
	}
	if b, tr := s.ReadText([]byte("<"), 0); string(b) != "<abcdé" || tr {
		t.Errorf("ReadText: %q, %v", b, tr)
	}
	if ev := s.Event(); ev.Kind != EndElement || ev.Name.Local != "div" || ev.Implied || s.Depth() != 3 {
		t.Errorf("after ReadText: %s, depth %d", dumpEvent(ev), s.Depth())
	}
	s.Next()
	if b, tr := s.ReadText(nil, 0); string(b) != "e" || tr {
		t.Errorf("ReadText at text: %q", b)
	}
	s.Next()
	if b, tr := s.ReadText(nil, 0); len(b) != 0 || tr || s.Event().Kind != EndElement || s.Event().Name.Local != "br" {
		t.Errorf("ReadText at br: %q, %s", b, dumpEvent(s.Event()))
	}

	for _, c := range []struct {
		limit int
		want  string
	}{{1, "h"}, {2, "h"}, {3, "hé"}, {6, "hé€"}, {7, "hé€x"}, {8, "hé€xy"}, {0, "hé€xy"}} {
		_, s = newReader(htmlstream.XML, `<p>hé€<b>x</b>y</p>`)
		s.Next()
		b, tr := s.ReadText(nil, c.limit)
		if string(b) != c.want || tr != (c.limit > 0 && c.limit < 8) {
			t.Errorf("limit %d: %q, %v, want %q", c.limit, b, tr, c.want)
		}
	}

	for _, c := range []struct {
		mode htmlstream.Mode
		in   string
	}{
		{htmlstream.HTML, `<div>a<template>b<p>c</p></template>d<template></div>`},
		{htmlstream.XML, `<div xmlns="http://www.w3.org/1999/xhtml">a<template>b<p>c</p></template>d</div>`},
	} {
		r, s := newReader(c.mode, c.in)
		r.SetMaxDepth(5)
		for s.Next() && s.Event().Name.Local != "div" {
		}
		if b, _ := s.ReadText(nil, 0); string(b) != "ad" || s.Event().Kind != EndElement || s.Event().Name.Local != "div" {
			t.Errorf("%v %q: ReadText %q, then %s", c.mode, c.in, b, dumpEvent(s.Event()))
		}
	}
	r, s := newReader(htmlstream.XML, `<div xmlns="http://www.w3.org/1999/xhtml"><a>a<template>b<p>c</p></template>d</a></div>`)
	r.SetMaxDepth(1)
	s.Next()
	if b, _ := s.ReadText(nil, 0); string(b) != "ad" {
		t.Errorf("ReadText with an untracked template: %q", b)
	}
	_, s = newReader(htmlstream.HTML, `<template>a</template>b`)
	for s.Next() && s.Event().Name.Local != "template" {
	}
	if b, _ := s.ReadText(nil, 0); len(b) != 0 || s.Event().Kind != EndElement || s.Event().Name.Local != "template" {
		t.Errorf("ReadText at a template: %q, then %s", b, dumpEvent(s.Event()))
	}

	_, s = newReader(htmlstream.XML, `<p>abcdefgh</p>`)
	s.SetMaxText(3)
	s.Next()
	s.Next()
	if b, _ := s.ReadText(nil, 0); string(b) != "abcdefgh" || s.Event().Partial || string(s.Event().Text) != "gh" {
		t.Errorf("ReadText at partial text: %q, %s", b, dumpEvent(s.Event()))
	}

	_, s = newReader(htmlstream.XML, `<a><b>x<c/>y</b>z</a>`)
	s.Next()
	if s.Skip(); s.Err() != nil || s.Event().Kind != EndElement || s.Event().Name.Local != "a" || s.Next() {
		t.Errorf("Skip: %v, %s", s.Err(), dumpEvent(s.Event()))
	}
	_, s = newReader(htmlstream.XML, `<a><b>x</b>z</a>`)
	s.Next()
	s.Next()
	s.Skip()
	if s.Next(); string(s.Event().Text) != "z" || s.Depth() != 1 {
		t.Errorf("after Skip: %s, depth %d", dumpEvent(s.Event()), s.Depth())
	}
	if s.Skip(); s.Err() != nil || string(s.Event().Text) != "z" {
		t.Errorf("Skip at text: %v, %s", s.Err(), dumpEvent(s.Event()))
	}

	_, s = newReader(htmlstream.XML, `<a><b>x</c></b></a>`)
	s.SetStrict(true)
	s.Next()
	if s.Skip(); s.Err() == nil || s.Event().Kind != SyntaxError {
		t.Errorf("strict Skip: %v, %s", s.Err(), dumpEvent(s.Event()))
	}
}

// TestStrictLater checks that strict mode applies to the events after
// SetStrict, including the token read to end a Text event.
func TestStrictLater(t *testing.T) {
	_, s := newReader(htmlstream.XML, `<a><b>x</a>`)
	for s.Next() && s.Event().Kind != Text {
	}
	s.SetStrict(true)
	if s.Next(); s.Event().Kind != SyntaxError || s.Err() == nil {
		t.Errorf("after SetStrict: %s, %v", dumpEvent(s.Event()), s.Err())
	}
}

func TestWalk(t *testing.T) {
	const in = `<a><b>x<c>y</c></b><d>z</d><e>w</e></a>`
	var log []string
	stop := errors.New("stop")
	h := Handler{
		StartElement: func(s *Reader, ev *Event) error {
			log = append(log, "<"+ev.Name.Local+">")
			switch ev.Name.Local {
			case "b":
				return SkipElement
			case "d":
				b, _ := s.ReadText(nil, 0)
				log = append(log, "read "+string(b))
			case "e":
				s.Skip()
				return SkipElement
			}
			return nil
		},
		EndElement: func(s *Reader, ev *Event) error {
			log = append(log, "</"+ev.Name.Local+">")
			return nil
		},
		Text: func(s *Reader, ev *Event) error {
			log = append(log, string(ev.Text))
			if string(ev.Text) == "w" {
				return stop
			}
			return nil
		},
	}
	_, s := newReader(htmlstream.XML, in)
	if err := s.Walk(h); err != nil || strings.Join(log, " ") != "<a> <b> </b> <d> read z </d> <e> </e> </a>" {
		t.Errorf("Walk: %v, %v", log, err)
	}

	log = nil
	h.StartElement = nil
	_, s = newReader(htmlstream.XML, in)
	if err := s.Walk(h); err != stop || strings.Join(log, " ") != "x y </c> </b> z </d> w" {
		t.Errorf("Walk with error: %v, %v", log, err)
	}

	// SkipElement from other functions skips the rest of the parent element
	log = nil
	h.Text = func(s *Reader, ev *Event) error {
		log = append(log, string(ev.Text))
		return SkipElement
	}
	_, s = newReader(htmlstream.XML, `<a><b>x<c>y</c></b><d>z<e/></d></a>w<f/>`)
	if err := s.Walk(h); err != nil || strings.Join(log, " ") != "x </b> z </d> </a> w" {
		t.Errorf("Walk with SkipElement from Text: %v, %v", log, err)
	}
	log = nil
	h.Text = nil
	h.EndElement = func(s *Reader, ev *Event) error {
		log = append(log, "</"+ev.Name.Local+">")
		if ev.Name.Local == "c" || ev.Name.Local == "a" {
			return SkipElement
		}
		return nil
	}
	_, s = newReader(htmlstream.XML, `<a><b><c/><d/></b><e/></a><f/>`)
	if err := s.Walk(h); err != nil || strings.Join(log, " ") != "</c> </b> </e> </a>" {
		t.Errorf("Walk with SkipElement from EndElement: %v, %v", log, err)
	}

	// a Text function reading the rest of the text with ReadText
	log = nil
	h.EndElement = nil
	h.Text = func(s *Reader, ev *Event) error {
		b, _ := s.ReadText(nil, 0)
		log = append(log, string(b))
		return nil
	}
	_, s = newReader(htmlstream.XML, `<a>abcdefgh<b/>ij</a>`)
	s.SetMaxText(3)
	if err := s.Walk(h); err != nil || strings.Join(log, " ") != "abcdefgh ij" {
		t.Errorf("Walk with ReadText from Text: %q, %v", log, err)
	}

	var syntax string
	_, s = newReader(htmlstream.XML, `<a>x</b></a>`)
	s.SetStrict(true)
	err := s.Walk(Handler{SyntaxError: func(s *Reader, ev *Event) error {
		syntax = string(ev.Text)
		return nil
	}})
	if syntax != "dropped stray </b>" || err == nil || err != s.Err() {
		t.Errorf("Walk in strict mode: %q, %v", syntax, err)
	}
}

func TestNamedElement(t *testing.T) {
	_, s := newReader(htmlstream.HTML, `<div><span>x</span><svg><title>y</title></svg><!--div-->`)
	var got []string
	for ev := range s.All() {
		got = append(got, strings.Join([]string{ev.Kind.String(), strings.TrimPrefix(ev.NS(), "http://www.w3.org/"), string(ev.AppendLocalName(nil)), map[bool]string{true: "block"}[htmlutil.IsBlock(ev)], map[bool]string{true: "hidden"}[htmlutil.IsHidden(ev)]}, " "))
	}
	want := []string{
		"StartElement 1999/xhtml html block ",
		"StartElement 1999/xhtml head  hidden",
		"EndElement 1999/xhtml head  hidden",
		"StartElement 1999/xhtml body block ",
		"StartElement 1999/xhtml div block ",
		"StartElement 1999/xhtml span  ",
		"Text    ",
		"EndElement 1999/xhtml span  ",
		"StartElement 2000/svg svg  ",
		"StartElement 2000/svg title  ",
		"Text    ",
		"EndElement 2000/svg title  ",
		"EndElement 2000/svg svg  ",
		"Comment    ",
		"EndElement 1999/xhtml div block ",
		"EndElement 1999/xhtml body block ",
		"EndElement 1999/xhtml html block ",
	}
	if !slices.Equal(got, want) {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

var (
	allocsHTML = `<div class="row"><a href="/x?a=1&amp;b=2" title="t">link text</a><span>more &amp; text</span><br><img src=a.png alt="x"><svg viewBox="0 0 1 1"><foreignObject></foreignObject></svg></div>
`
	allocsAtom = `<entry x:k="v"><title>A &amp; B</title><link href="http://e/1"/><x:ext>t<![CDATA[u]]></x:ext></entry>
`
)

// TestAllocs checks that reading events doesn't allocate for each event, and
// that a TokenReader allocates at most a few times for each element.
func TestAllocs(t *testing.T) {
	for _, c := range []struct {
		mode       htmlstream.Mode
		start, rep string
		end        string
	}{
		{htmlstream.HTML, "<!DOCTYPE html><title>t</title>", allocsHTML, ""},
		// a namespace declaration allocates its URI string once (see
		// htmlstream.Token.NS), so the declarations are on the root
		{htmlstream.XML, `<?xml version="1.0"?><feed xmlns="http://www.w3.org/2005/Atom" xmlns:x="urn:x">`, allocsAtom, "</feed>"},
	} {
		doc := func(n int) string { return c.start + strings.Repeat(c.rep, n) + c.end }
		count := func(in string, fn func(s *Reader)) float64 {
			return testing.AllocsPerRun(10, func() {
				_, s := newReader(c.mode, in)
				fn(s)
			})
		}
		events := func(s *Reader) {
			for s.Next() {
			}
		}
		attrs := func(s *Reader) {
			for ev := range s.All() {
				if tok := ev.Token(); ev.Kind == StartElement && tok != nil {
					if a, ok := tok.Attr("href"); ok {
						a.AppendValue(s.buf[:0])
					}
					tok.AttrNS("urn:x", "k")
				}
			}
		}
		tokens := func(s *Reader) {
			tr := NewTokenReader(s)
			for {
				if _, err := tr.Token(); err != nil {
					break
				}
			}
		}
		_, s := newReader(c.mode, doc(100))
		var nevents, nelems int
		for ev := range s.All() {
			nevents++
			if ev.Kind == StartElement {
				nelems++
			}
		}
		reader := testing.AllocsPerRun(10, func() {
			r, _ := newReader(c.mode, doc(100))
			for r.Next() {
			}
		})
		for _, f := range []struct {
			name string
			fn   func(s *Reader)
		}{{"events", events}, {"attrs", attrs}} {
			small, large := count(doc(10), f.fn), count(doc(100), f.fn)
			t.Logf("%v %s: %.0f allocations for %d events (htmlstream.Reader alone %.0f), %.0f for a tenth", c.mode, f.name, large, nevents, reader, small)
			if large-small > 8 {
				t.Errorf("%v %s: %.0f allocations for %d events, %.0f for a tenth", c.mode, f.name, large, nevents, small)
			}
		}
		n := count(doc(100), tokens)
		t.Logf("%v TokenReader: %.0f allocations for %d events, %d elements (%.2f per element)", c.mode, n, nevents, nelems, n/float64(nelems))
		if n > float64(4*nelems) {
			t.Errorf("%v TokenReader: %.0f allocations for %d elements", c.mode, n, nelems)
		}
	}
}
