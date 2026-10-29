package sax

import (
	"slices"
	"strings"
	"testing"

	"github.com/pgaskin/htmlstream"
	html5libgo_testdata "github.com/pgaskin/htmlstream/testdata/html5libgo"
)

// html5libTree returns the elements, text, comments and doctype of the
// events of s in the format of the #document sections of the html5lib-tests
// tree construction tests, without attributes, and whether there was a
// Warning or SyntaxError event or an error.
func html5libTree(s *Reader) (string, bool) {
	var b strings.Builder
	depth := 0
	text := false // the last line is text, which more text continues
	line := func(s string) {
		b.WriteString("\n| ")
		b.WriteString(strings.Repeat("  ", depth))
		b.WriteString(s)
		text = false
	}
	bad := false
	for ev := range s.All() {
		switch ev.Kind {
		case StartElement:
			switch ev.Name.Space {
			case htmlstream.NamespaceSVG:
				line("<svg " + ev.Name.Local + ">")
			case htmlstream.NamespaceMathML:
				line("<math " + ev.Name.Local + ">")
			default:
				line("<" + ev.Name.Local + ">")
			}
			depth++
			if ev.Name == nameTemplate {
				line("content")
				depth++
			}
		case EndElement:
			depth--
			if ev.Name == nameTemplate {
				depth--
			}
			text = false
		case Text:
			if text {
				// before the closing quote
				s := b.String()
				b.Reset()
				b.WriteString(s[:len(s)-1])
				b.Write(ev.Text)
				b.WriteByte('"')
			} else {
				line(`"` + string(ev.Text) + `"`)
			}
			text = true
		case Comment:
			line("<!-- " + string(ev.Text) + " -->")
		case Doctype:
			line("<!DOCTYPE " + strings.ToLower(string(ev.Text)) + ">")
		default:
			bad = true
		}
	}
	return strings.TrimPrefix(b.String(), "\n"), bad || s.Err() != nil
}

// withoutAttrs returns the lines of an html5lib-tests #document section
// without the attributes.
func withoutAttrs(doc string) string {
	var lines []string
	attr := false
	for l := range strings.Lines(doc) {
		l = strings.TrimSuffix(l, "\n")
		if v, ok := strings.CutPrefix(l, "| "); ok {
			v = strings.TrimLeft(v, " ")
			attr = !strings.HasPrefix(v, "<") && !strings.HasPrefix(v, `"`) && v != "content"
		}
		if !attr {
			lines = append(lines, l)
		}
	}
	return strings.Join(lines, "\n")
}

// TestHTML5Lib checks that the html5lib-tests tree construction cases
// without parse errors give no Warning or SyntaxError events in strict
// mode, and the elements, text and comments of HTML parsers, except for the
// differences listed in the package documentation.
func TestHTML5Lib(t *testing.T) {
	differ := map[string]bool{
		"tests1/34":  true, // a comment after a body end tag
		"tests2/51":  true, // whitespace before the html element
		"tests7/10":  true, // whitespace before the head
		"tests7/11":  true, // whitespace before the head
		"tests12/1":  true, // no implied tbody
		"tests12/2":  true, // no implied tbody
		"tests17/13": true, // no implied tbody
		"tests18/32": true, // frameset
		"tests18/34": true, // frameset
		"tests19/39": true, // frameset
		"tests19/40": true, // frameset
	}
	for c := range html5libgo_testdata.TreeTests() {
		scripting := []bool{true, false}
		switch c.Scripting {
		case "on":
			scripting = []bool{true}
		case "off":
			scripting = []bool{false}
		}
		want := withoutAttrs(c.Document)
		same := true
		for _, sc := range scripting {
			r, s := newReader(htmlstream.HTML, c.Input)
			r.SetScripting(sc)
			s.SetStrict(true)
			got, bad := html5libTree(s)
			if bad {
				r, s := newReader(htmlstream.HTML, c.Input)
				r.SetScripting(sc)
				s.SetStrict(true)
				t.Errorf("%s %q, scripting %v: %s", c.Name, c.Input, sc, dump(s))
				continue
			}
			if got != want {
				same = false
				if !differ[c.Name] {
					t.Errorf("%s %q, scripting %v:\n%s\nwant\n%s", c.Name, c.Input, sc, got, want)
				}
			}
		}
		if same && differ[c.Name] {
			t.Errorf("%s: no difference", c.Name)
		}
	}
	if n := len(slices.Collect(html5libgo_testdata.TreeTests())); n < 150 {
		t.Errorf("%d cases", n)
	}
}
