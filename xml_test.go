package htmlstream

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"testing/iotest"

	xml_testdata "github.com/pgaskin/htmlstream/testdata/xml"
)

// xmlCaseMode returns the mode of an XML test case (see xml_testdata): XML,
// or Universal with the universal flag.
func xmlCaseMode(c xml_testdata.Case) Mode {
	if c.HasFlag("universal") {
		return Universal
	}
	return XML
}

// xmlTokens returns the tokens of in, one per line: the depth and the token
// (see Token.String), followed by the decoded text of text, comment and
// CDATA tokens, the data of processing instructions, and the namespace and
// decoded attribute values of start tags.
func xmlTokens(in string, mode Mode) string {
	r := NewReader(strings.NewReader(in))
	r.SetMode(mode)
	var b strings.Builder
	for r.Next() {
		tok := r.Token()
		fmt.Fprintf(&b, "%d %v", r.Depth(), tok)
		switch tok.Kind() {
		case Text, Comment, CDATA:
			fmt.Fprintf(&b, " text=%q", tok.AppendText(nil))
		case ProcInst:
			fmt.Fprintf(&b, " data=%q", tok.Data())
		case StartTag:
			if ns := tok.NS(); ns != "" {
				fmt.Fprintf(&b, " ns=%q", ns)
			}
			for a := range tok.Attrs() {
				fmt.Fprintf(&b, " %s=%q", a.Name(), a.Value())
			}
		}
		b.WriteByte('\n')
	}
	if err := r.Err(); err != nil {
		fmt.Fprintf(&b, "error: %v\n", err)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// TestXMLCases checks the tokens of the test cases in testdata/xml, and that
// a Transformer without edits writes them unchanged in all modes, with the
// input read at once, one byte at a time and in halves, and with chunk sizes
// from 1 to 64 (also changing after every token).
func TestXMLCases(t *testing.T) {
	readers := map[string]func(io.Reader) io.Reader{
		"full":    nil,
		"onebyte": iotest.OneByteReader,
		"half":    iotest.HalfReader,
	}
	for name, c := range xml_testdata.Cases("*.txtar", "in", "tokens") {
		t.Run(name, func(t *testing.T) {
			in, _ := c.Section("in")
			want, _ := c.Section("tokens")
			if got := xmlTokens(in, xmlCaseMode(c)); got != want {
				t.Errorf("tokens:\n%s\nwant:\n%s", got, want)
			}
			for _, mode := range modes {
				for name, rd := range readers {
					for _, chunk := range []int{0, 1, 2, 3, 7, 64, -5} {
						var src io.Reader = strings.NewReader(in)
						if rd != nil {
							src = rd(src)
						}
						var b bytes.Buffer
						x := NewTransformer(&b, src)
						x.SetMode(mode)
						if chunk > 0 {
							x.SetChunkSize(chunk)
						}
						for n := 0; x.Next(); n++ {
							if chunk < 0 {
								x.SetChunkSize(1 + n%-chunk)
							}
						}
						if err := x.Close(); err != nil {
							t.Errorf("%s, %s reader, chunk %d: %v", mode, name, chunk, err)
						}
						if b.String() != in {
							t.Errorf("%s, %s reader, chunk %d: got %q", mode, name, chunk, b.String())
						}
					}
				}
			}
		})
	}
}

// xmlCheck reads s with encoding/xml in strict mode, returning an error if
// it isn't well-formed. Any declared encoding is accepted (the input is read
// as UTF-8), and so are text and elements outside the root element (which
// encoding/xml doesn't check). encoding/xml reads directives with nested
// markup, and the Reader reads doctypes like XML parsers and other
// directives as bogus comments, so directives other than doctypes, and
// doctypes which the Reader reads differently (e.g., "<!DOCTYPE<0>>"), are
// an error, and so are tokens cut off by the end of the input, which
// encoding/xml can accept (e.g., "<!DOCTYPE[>").
func xmlCheck(s string) error {
	var doctypes []string
	r := NewReader(strings.NewReader(s))
	r.SetMode(XML)
	for r.Next() {
		tok := r.Token()
		if tok.Incomplete() {
			return fmt.Errorf("%v cut off by the end of the input", tok)
		}
		if tok.Kind() == Doctype {
			doctypes = append(doctypes, string(tok.Raw()))
		}
	}
	d := xml.NewDecoder(strings.NewReader(s))
	d.Strict = true
	d.CharsetReader = func(label string, r io.Reader) (io.Reader, error) { return r, nil }
	for {
		tok, err := d.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if dir, ok := tok.(xml.Directive); ok {
			if !bytes.HasPrefix(dir, []byte("DOCTYPE")) {
				return fmt.Errorf("directive %q", dir)
			}
			if len(doctypes) == 0 || doctypes[0] != "<!"+string(dir)+">" {
				return fmt.Errorf("doctype %q read differently", dir)
			}
			doctypes = doctypes[1:]
		}
	}
}

// xmlJoinedCDATAEnd reports whether the text of s contains "]]>", which
// edits form by joining text ending with "]]" and text starting with ">"
// (see the package documentation).
func xmlJoinedCDATAEnd(s string) bool {
	r := NewReader(strings.NewReader(s))
	r.SetMode(XML)
	var text []byte
	for r.Next() {
		if tok := r.Token(); tok.Kind() == Text {
			text = append(text, tok.Raw()...)
			continue
		}
		if bytes.Contains(text, []byte("]]>")) {
			return true
		}
		text = text[:0]
	}
	return bytes.Contains(text, []byte("]]>"))
}

func TestTransformXML(t *testing.T) {
	startTag := func(name string, fn func(x *Transformer) error) func(x *Transformer, tok *Token) error {
		return func(x *Transformer, tok *Token) error {
			if tok.Kind() == StartTag && tok.Is(name) {
				return fn(x)
			}
			return nil
		}
	}
	kind := func(k Kind, fn func(x *Transformer, tok *Token) error) func(x *Transformer, tok *Token) error {
		return func(x *Transformer, tok *Token) error {
			if tok.Kind() == k {
				return fn(x, tok)
			}
			return nil
		}
	}
	for _, tc := range []struct {
		Name  string
		Mode  Mode // XML if zero
		Chunk int
		In    string
		Do    func(x *Transformer, tok *Token) error
		Out   string
	}{
		{
			Name: "SetAttrEscaping",
			In:   `<a b="1"/>`,
			Do: startTag("a", func(x *Transformer) error {
				x.SetAttr("b", "x\ny\tz\r<&>\"'")
				return x.SetAttr("c", "]]>")
			}),
			Out: `<a b="x&#10;y&#9;z&#13;&lt;&amp;&gt;&quot;&#39;" c="]]&gt;"/>`,
		},
		{
			// Tabs, newlines and carriage returns are escaped under HTML
			// rules too.
			Name: "SetAttrEscapingUniversal",
			Mode: Universal,
			In:   `<p a="1"><svg xmlns="http://www.w3.org/2000/svg" a="1"/><x:y xmlns:x="urn:x" a="1"/><html xmlns="http://www.w3.org/1999/xhtml" a="1"/>`,
			Do: kind(StartTag, func(x *Transformer, tok *Token) error {
				return x.SetAttr("a", "x\ty\n\r'")
			}),
			Out: `<p a="x&#9;y&#10;&#13;&#39;"><svg xmlns="http://www.w3.org/2000/svg" a="x&#9;y&#10;&#13;&#39;"/>` +
				`<x:y xmlns:x="urn:x" a="x&#9;y&#10;&#13;&#39;"/>` +
				`<html xmlns="http://www.w3.org/1999/xhtml" a="x&#9;y&#10;&#13;&#39;"/>`,
		},
		{
			Name: "SetAttrEscapingHTML",
			Mode: HTML,
			In:   `<p a="1">x`,
			Do: kind(StartTag, func(x *Transformer, tok *Token) error {
				return x.SetAttr("a", "x\ty\r\nz\r'")
			}),
			Out: `<p a="x&#9;y&#13;&#10;z&#13;&#39;">x`,
		},
		{
			Name: "SetAttrEscapingUniversalXHTML",
			Mode: Universal,
			In:   `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml" a="1"><svg a="1"/><z:y a="1"/></html>`,
			Do: kind(StartTag, func(x *Transformer, tok *Token) error {
				return x.SetAttr("a", "x\ty\n\r'")
			}),
			Out: `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml" a="x&#9;y&#10;&#13;&#39;">` +
				`<svg a="x&#9;y&#10;&#13;&#39;"/><z:y a="x&#9;y&#10;&#13;&#39;"/></html>`,
		},
		{
			Name: "AttributesKept",
			In:   "<a  c = '1'\n\td=\"2\"  e=\"&foo;\" />",
			Do:   startTag("a", func(x *Transformer) error { return x.SetAttr("d", "x") }),
			Out:  "<a  c = '1'\n\td=\"x\"  e=\"&foo;\" />",
		},
		{
			Name: "NamesCaseSensitive",
			In:   `<A B="1" b="2"><a/></A>`,
			Do: startTag("A", func(x *Transformer) error {
				x.RemoveAttr("b")
				return x.SetAttr("B", "3")
			}),
			Out: `<A B="3"><a/></A>`,
		},
		{
			Name: "RenamePrefixed",
			In:   `<x:a xmlns:x="urn:x"><x:b>t</x:b ></x:a>`,
			Do: startTag("x:b", func(x *Transformer) error {
				x.Rename("y:b")
				return x.SetAttr("xmlns:y", "urn:y")
			}),
			Out: `<x:a xmlns:x="urn:x"><y:b xmlns:y="urn:y">t</y:b ></x:a>`,
		},
		{
			Name: "ExpandSelfClosing",
			In:   `<a><br/><p /><script/></a>`,
			Do: kind(StartTag, func(x *Transformer, tok *Token) error {
				return x.ExpandSelfClosing()
			}),
			Out: `<a><br></br><p ></p><script></script></a>`,
		},
		{
			Name: "Empty",
			In:   `<a><title>x</title><br>y</br><b/></a>`,
			Do: kind(StartTag, func(x *Transformer, tok *Token) error {
				if tok.Is("a") {
					return nil
				}
				return x.Empty()
			}),
			Out: `<a><title/><br/><b/></a>`,
		},
		{
			Name: "HTMLNames",
			In:   `<r><script>a<b/></script><p>x<p>y</p></p></r>`,
			Do: kind(StartTag, func(x *Transformer, tok *Token) error {
				switch {
				case tok.Is("b"):
					return x.Remove()
				case tok.Is("p") && x.Depth() == 3:
					return x.Unwrap()
				}
				return nil
			}),
			Out: `<r><script>a</script><p>xy</p></r>`,
		},
		{
			Name: "ReplaceCDATA",
			In:   `<a><![CDATA[old]]></a>`,
			Do: kind(CDATA, func(x *Transformer, tok *Token) error {
				return x.Replace(AppendCDATA(nil, "x]]>y"))
			}),
			Out: `<a><![CDATA[x]]]]><![CDATA[>y]]></a>`,
		},
		{
			Name:  "ReplaceSplitCDATA",
			Chunk: 4,
			In:    `<a><![CDATA[0123456789]]></a>`,
			Do: kind(CDATA, func(x *Transformer, tok *Token) error {
				for x.Token().Partial() {
					x.Remove()
					x.Next()
				}
				return x.Replace(AppendCDATA(nil, "new"))
			}),
			Out: `<a><![CDATA[new]]></a>`,
		},
		{
			Name: "ProcInsts",
			In:   `<?xml version="1.0"?><?xml-stylesheet href="a"?><?old a?><a/>`,
			Do: kind(ProcInst, func(x *Transformer, tok *Token) error {
				switch string(tok.Name()) {
				case "xml-stylesheet":
					return x.Remove()
				case "old":
					return x.Replace(AppendProcInst(nil, "new", "x?>y"))
				}
				return nil
			}),
			Out: `<?xml version="1.0"?><?new x? >y?><a/>`,
		},
		{
			Name: "Insert",
			In:   `<a>x</a>`,
			Do: startTag("a", func(x *Transformer) error {
				x.InsertBefore(AppendProcInst(nil, "pi", ""))
				x.InsertAfter(AppendComment(nil, "a--b-"))
				x.InsertAfter(AppendCDATA(nil, "]]>"))
				return x.InsertAfter(AppendEscapeText(nil, "]]>&\r"))
			}),
			Out: "<?pi?><a><!--a- -b- --><![CDATA[]]]]><![CDATA[>]]>]]&gt;&amp;&#13;x</a>",
		},
		{
			Name: "ReplaceText",
			In:   `<a>&foo;&amp;<b/>x</a>`,
			Do: kind(Text, func(x *Transformer, tok *Token) error {
				if string(tok.Raw()) == "x" {
					return x.Replace(AppendEscapeText(nil, "<y>"))
				}
				return nil
			}),
			Out: `<a>&foo;&amp;<b/>&lt;y&gt;</a>`,
		},
		{
			Name: "Doctype",
			In:   "<!DOCTYPE a [<!ENTITY e \"<b/>\"><?pi it's?>]><a>&e;</a>",
			Do:   startTag("a", func(x *Transformer) error { return x.SetAttr("b", "1") }),
			Out:  "<!DOCTYPE a [<!ENTITY e \"<b/>\"><?pi it's?>]><a b=\"1\">&e;</a>",
		},
		{
			Name: "CommentWithBangEnd",
			In:   `<a><!-- x --!> <b/> --><b/></a>`,
			Do:   startTag("b", func(x *Transformer) error { return x.Remove() }),
			Out:  `<a><!-- x --!> <b/> --></a>`,
		},
		{
			// The implied end tag of b writes nothing, and the end markup of
			// the wrap is written where b is closed.
			Name: "Misnested",
			In:   `<a><b><c>x</a></b>y`,
			Do: kind(StartTag, func(x *Transformer, tok *Token) error {
				switch {
				case tok.Is("a"):
					return x.Rename("A")
				case tok.Is("b"):
					return x.Wrap([]byte("<w>"), []byte("</w>"))
				case tok.Is("c"):
					return x.Unwrap()
				}
				return nil
			}),
			Out: `<A><w><b>x</w></A></b>y`,
		},
		{
			Name: "Unclosed",
			In:   `<a><b>x`,
			Do: startTag("b", func(x *Transformer) error {
				x.Rename("c")
				return x.Wrap([]byte("<w>"), []byte("</w>"))
			}),
			Out: `<a><w><c>x</w>`,
		},
		{
			Name: "RemoveUnclosed",
			In:   `<a><b>x</a>y`,
			Do:   startTag("b", func(x *Transformer) error { return x.Remove() }),
			Out:  `<a></a>y`,
		},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			var b bytes.Buffer
			x := NewTransformer(&b, strings.NewReader(tc.In))
			x.SetMode(XML)
			if tc.Mode != HTML {
				x.SetMode(tc.Mode)
			}
			if tc.Chunk != 0 {
				x.SetChunkSize(tc.Chunk)
			}
			for x.Next() {
				if err := tc.Do(x, x.Token()); err != nil {
					t.Fatal(err)
				}
			}
			if err := x.Close(); err != nil {
				t.Fatal(err)
			}
			if b.String() != tc.Out {
				t.Errorf("got %q\nwant %q", b.String(), tc.Out)
			}
			if xmlCheck(tc.In) == nil {
				if err := xmlCheck(b.String()); err != nil {
					t.Errorf("output isn't well-formed: %v", err)
				}
			}
		})
	}
}

// xmlPattern is a pattern of literal parts and wildcards (see
// FuzzTransformXML).
type xmlPattern struct {
	parts []string
	wild  []bool // whether a wildcard precedes each part
	end   bool   // whether a wildcard follows the last part
}

// lit adds the literal s to the pattern.
func (p *xmlPattern) lit(s string) {
	if s == "" {
		return
	}
	if n := len(p.parts); n != 0 && !p.end {
		p.parts[n-1] += s
		return
	}
	p.parts = append(p.parts, s)
	p.wild = append(p.wild, p.end)
	p.end = false
}

// wildcard adds a wildcard to the pattern.
func (p *xmlPattern) wildcard() {
	p.end = true
}

// match reports whether s matches the pattern, matching each literal part
// at the leftmost position after the previous one, which is right after it
// unless a wildcard is between them, and the last one at the end of s unless
// a wildcard follows it.
func (p *xmlPattern) match(s string) bool {
	pos := 0
	for i, l := range p.parts {
		if !p.wild[i] {
			if !strings.HasPrefix(s[pos:], l) {
				return false
			}
			pos += len(l)
			continue
		}
		j := strings.Index(s[pos:], l)
		if i == len(p.parts)-1 && !p.end {
			// the last part ends the output
			j = strings.LastIndex(s[pos:], l)
		}
		if j < 0 {
			return false
		}
		pos += j + len(l)
	}
	return p.end || pos == len(s)
}

// FuzzTransformXML applies edits in XML mode which keep well-formed XML
// well-formed: removing elements and whole tokens (except end tags, and
// comments and CDATA sections split into multiple tokens), unwrapping,
// wrapping, renaming and emptying elements, attribute edits, expanding
// self-closing tags, and inserting or replacing markup written with the Append
// functions (at the boundaries of comments and CDATA sections). It checks
// that:
//
//   - the tokens without edits are written unchanged, with only the output of
//     the edited tokens between them;
//   - if encoding/xml reads the input as well-formed (see xmlCheck), it reads
//     the output as well-formed too, with the attribute values set and the
//     text inserted in elements, CDATA sections and processing instructions
//     unchanged (except for the conversion of carriage returns to newlines);
//   - the Reader reads the attribute values set unchanged.
//
// chunk sets the chunk size (from 1 to 16, or the default if 0), and the
// first byte of ops selects the attribute value set. Each following byte is
// the edit for the next token (none for 0 to 3 modulo 16), with arguments
// from its high bits.
func FuzzTransformXML(f *testing.F) {
	for _, c := range xml_testdata.Cases("*.txtar", "in", "tokens") {
		in, _ := c.Section("in")
		f.Add(in, uint8(0), []byte{2, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 0x1e, 0x2e, 0x3e, 0x4e})
		f.Add(in, uint8(3), []byte{3, 0x1f, 0x2f, 0x48, 0x39, 0x5d, 0x67, 0x9e, 0xae, 0x5e, 0x7e, 0x9e})
	}
	// inputs which found bugs
	f.Add("<!DOCTYPE<0>>", uint8(0), []byte("00$"))
	f.Add("00>", uint8('!'), []byte("0\xff0"))
	f.Add("<!DOCTYPE[>", uint8(3), []byte("0."))
	// a value with U+2028 set in an XML 1.1 document
	f.Add(`<?xml version="1.1"?><a/>`, uint8(0), []byte{6, 8})
	// a misplaced XML 1.1 declaration becoming the output's
	f.Add("0<?xml version=\"1.1\"?><A>", uint8(0x1e), []byte("0$08"))
	values := []string{"x", "", "x\ny\tz\r\n", `<&>"'`, "]]>", "&amp;&#10;", "é\u2028"}
	texts := []string{"", "x", "]]>", "a]]>b]]", "--", "-", "?>", "a?>b?", "<&>\"'", "\r\n\r", " \tx", "&amp;"}
	f.Fuzz(func(t *testing.T, in string, chunk uint8, ops []byte) {
		if len(ops) == 0 {
			return
		}
		value := values[int(ops[0])%len(values)]
		ops = ops[1:]
		if len(ops) == 0 {
			ops = []byte{0}
		}
		var b bytes.Buffer
		x := NewTransformer(&b, iotest.OneByteReader(strings.NewReader(in)))
		x.SetMode(XML)
		if chunk != 0 {
			x.SetChunkSize(int(chunk%16) + 1)
		}
		var p xmlPattern
		type inserted struct {
			kind byte // 'c' for CDATA, 'e' for text in an element, 'p' for a processing instruction
			text string
		}
		var want []inserted
		ends := map[Pos]bool{} // start tags of the elements whose end tags are edited
		for j := 0; x.Next(); j++ {
			tok := x.Token()
			raw := string(tok.Raw())
			k := tok.Kind()
			op := ops[j%len(ops)]
			text := texts[int(op>>4)%len(texts)]
			// whether the token is a whole comment or CDATA section, or the
			// start or end of one
			first := k != Comment && k != CDATA || !tok.Continued()
			last := k != Comment && k != CDATA || !tok.Partial()
			var err error
			edited := true
			switch op % 16 {
			case 4:
				if k == EndTag || !first || !last {
					edited = false
					break
				}
				err = x.Remove()
			case 5:
				err = x.Unwrap()
			case 6:
				err = x.Wrap([]byte("<w>"), []byte("</w>"))
			case 7:
				if op&0x10 != 0 {
					err = x.Rename("p:r")
				} else {
					err = x.Rename("r")
				}
			case 8:
				err = x.SetAttr("zz", value)
			case 9:
				n := len(tok.attrs)
				err = x.SetAttrAt("zz", value, int(op>>4)%(2*n+2)-(n+1))
			case 10:
				err = x.RemoveAttrs(func(a Attr) bool { return len(a.Name())%2 == int(op>>4)%2 })
			case 11:
				err = x.RenameAttr(string([]byte{'a' + op>>4}), "y")
			case 12:
				err = x.ExpandSelfClosing()
			case 13:
				err = x.Empty()
			case 14:
				before := op&0x10 != 0
				if before && !first || !before && !last {
					edited = false
					break
				}
				var m []byte
				switch op >> 5 {
				case 0, 1:
					m = AppendElement(nil, "zz-e", text)
					want = append(want, inserted{'e', text})
				case 2, 3:
					m = append(AppendCDATA([]byte("<zz-c>"), text), "</zz-c>"...)
					want = append(want, inserted{'c', text})
				case 4, 5:
					m = AppendProcInst(nil, "zz-p", text)
					want = append(want, inserted{'p', text})
				default:
					m = AppendComment(nil, text)
				}
				if before {
					err = x.InsertBefore(m)
				} else {
					err = x.InsertAfter(m)
				}
			case 15:
				switch {
				case k == Text:
					err = x.Replace(AppendEscapeText(nil, text))
				case k == ProcInst:
					err = x.Replace(AppendProcInst(nil, "zz-p", text))
					want = append(want, inserted{'p', text})
				case !first || !last:
					edited = false
				case k == Comment:
					err = x.Replace(AppendComment(nil, text))
				case k == CDATA:
					err = x.Replace(AppendCDATA(nil, text))
				default:
					edited = false
				}
			default:
				edited = false
			}
			if errors.Is(err, ErrInvalidOp) || err == ErrIncompleteTag {
				edited = false
			} else if err != nil {
				t.Fatal(err)
			}
			if edited && k == StartTag && op%16 >= 5 && op%16 <= 7 {
				ends[tok.Pos()] = true
			}
			if k == EndTag && (ends[tok.StartPos()] || tok.StartPos() < 0 && !tok.Stray()) {
				edited = true
			}
			if edited {
				p.wildcard()
			} else {
				p.lit(raw)
			}
		}
		if err := x.Close(); err != nil {
			return // the input can't be read
		}
		out := b.String()
		if !p.match(out) {
			t.Fatalf("output %q doesn't match the input %q without the edited tokens %q", out, in, p.parts)
		}
		// edits before a misplaced XML declaration can make it the
		// output's, which changes the XML version the output is read with
		sameDecl := strings.HasPrefix(out, "<?xml") == strings.HasPrefix(in, "<?xml")
		r := NewReader(strings.NewReader(out))
		r.SetMode(XML)
		for r.Next() {
			if a, ok := r.Token().Attr("zz"); ok && a.Value() != value && sameDecl && !strings.Contains(in, "zz") {
				t.Fatalf("output %q: attribute value %q, want %q", out, a.Value(), value)
			}
		}
		if xmlCheck(in) != nil {
			return
		}
		if err := xmlCheck(out); err != nil {
			if xmlJoinedCDATAEnd(out) {
				return // documented
			}
			t.Fatalf("output %q isn't well-formed: %v", out, err)
		}
		if strings.Contains(in, "zz") {
			return
		}
		got, err := xmlInserted(out, value)
		if err != nil {
			t.Fatalf("output %q: %v", out, err)
		}
		nl := strings.NewReplacer("\r\n", "\n", "\r", "\n")
		var w []string
		for _, ins := range want {
			s := ins.text
			switch ins.kind {
			case 'c':
				s = nl.Replace(s)
			case 'p':
				s = nl.Replace(strings.TrimLeft(strings.ReplaceAll(s, "?>", "? >"), " \t\r\n"))
			}
			w = append(w, string(ins.kind)+s)
		}
		if !slices.Equal(got, w) {
			t.Fatalf("output %q: inserted text %q, want %q", out, got, w)
		}
	})
}

// xmlInserted reads the output of FuzzTransformXML with encoding/xml, and
// returns the text of the zz-e elements (prefixed with "e"), of the zz-c
// elements (with "c") and of the zz-p processing instructions (with "p"), in
// order. It returns an error if a zz attribute doesn't have the value.
func xmlInserted(s, value string) ([]string, error) {
	d := xml.NewDecoder(strings.NewReader(s))
	d.CharsetReader = func(label string, r io.Reader) (io.Reader, error) { return r, nil }
	var got []string
	var in string // the kind of the zz element read, if any
	var text []byte
	for {
		tok, err := d.Token()
		if err == io.EOF {
			return got, nil
		}
		if err != nil {
			return nil, err
		}
		switch tok := tok.(type) {
		case xml.StartElement:
			for _, a := range tok.Attr {
				if a.Name.Space == "" && a.Name.Local == "zz" && a.Value != value {
					return nil, fmt.Errorf("attribute value %q, want %q", a.Value, value)
				}
			}
			switch tok.Name.Local {
			case "zz-e", "zz-c":
				in, text = tok.Name.Local, text[:0]
			}
		case xml.CharData:
			text = append(text, tok...)
		case xml.EndElement:
			if in != "" && tok.Name.Local == in {
				got = append(got, in[3:]+string(text))
				in = ""
			}
		case xml.ProcInst:
			if tok.Target == "zz-p" {
				got = append(got, "p"+strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(string(tok.Inst)))
			}
		}
	}
}

func TestProcInstText(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"<?pi?>", ""},
		{"<?pi x?>", "x"},
		{"<?pi x\r\ny?>", "x\ny"},
		{"<?pi x\ry\r?>", "x\ny\n"},
		{"<?pi \t\r\n x ?>", "x "},
		{"<?pi\r\nx?>", "x"},
		{"<?pi a&amp;b\x00?>", "a&amp;b\x00"},
	} {
		for _, mode := range []Mode{XML, Universal} {
			r := NewReader(strings.NewReader(tc.in))
			r.SetMode(mode)
			if !r.Next() || r.Token().Kind() != ProcInst {
				t.Fatalf("%s %q: no processing instruction", mode, tc.in)
			}
			if got := string(r.Token().AppendText(nil)); got != tc.want {
				t.Errorf("%s %q: got %q, want %q", mode, tc.in, got, tc.want)
			}
		}
	}
}

// xml11Decoded returns the decoded text of the tokens in the root element r
// of in (except x:s tags), and the decoded attribute values of the start tags in it, each
// token ending with "|" (the tokens of split text are joined). The tokens
// are read one byte at a time, and decoded as they are read, or with clone,
// from copies made by Token.Clone and a TokenBuffer after reading all of
// them.
func xml11Decoded(t *testing.T, in string, mode Mode, chunk int, clone bool) string {
	t.Helper()
	decode := func(b []byte, tok *Token) []byte {
		switch tok.Kind() {
		case Text, Comment, CDATA, ProcInst:
			b = tok.AppendText(b)
		case StartTag:
			for a := range tok.Attrs() {
				b = a.AppendValue(b)
				if a.Value() != string(a.AppendValue(nil)) {
					t.Errorf("%s %q: Value and AppendValue differ", mode, in)
				}
			}
		default:
			return b
		}
		if !tok.Partial() {
			b = append(b, '|')
		}
		return b
	}
	r := NewReader(iotest.OneByteReader(strings.NewReader(in)))
	r.SetMode(mode)
	if chunk > 0 {
		r.SetChunkSize(chunk)
	}
	var b []byte
	var clones []*Token
	var buf TokenBuffer
	for r.Next() {
		tok := r.Token()
		if r.Depth() == 0 || tok.Is("r") || tok.Is("x:s") {
			continue
		}
		if clone {
			clones = append(clones, tok.Clone())
			buf.Append(tok)
		} else {
			b = decode(b, tok)
		}
	}
	if err := r.Err(); err != nil {
		t.Fatalf("%s %q: %v", mode, in, err)
	}
	if clone {
		var b2 []byte
		for i, tok := range clones {
			b = decode(b, tok)
			b2 = decode(b2, buf.At(i))
		}
		if string(b2) != string(b) {
			t.Errorf("%s %q (chunk %d): TokenBuffer copies decode to %q, clones to %q", mode, in, chunk, b2, b)
		}
	}
	return string(b)
}

// TestXML11LineEnds checks the decoding of the line ends of XML 1.1 in
// documents declared as XML 1.1 and other documents, in all modes, with
// chunk sizes from 1 to 7, and from copies of the tokens.
func TestXML11LineEnds(t *testing.T) {
	const decl11, decl10 = `<?xml version="1.1"?>`, `<?xml version="1.0"?>`
	for _, tc := range []struct {
		name, in, want11, want10 string
	}{
		{
			"Text",
			"a\r\u0085b\u0085c\u2028d\r\ne\rf&#x85;&#x2028;&#13;g\r\u2028h\u0085\ri\r",
			"a\nb\nc\nd\ne\nf\u0085\u2028\rg\n\nh\n\ni\n|",
			"a\n\u0085b\u0085c\u2028d\ne\nf\u0085\u2028\rg\n\u2028h\u0085\ni\n|",
		},
		{
			"Comment",
			"<!--a\r\u0085b\u0085c\u2028d\r\ne\rf&#x85;\r-->",
			"a\nb\nc\nd\ne\nf&#x85;\n|",
			"a\n\u0085b\u0085c\u2028d\ne\nf&#x85;\n|",
		},
		{
			"CDATA",
			"<![CDATA[a\r\u0085b\u0085c\u2028d\r\ne\rf&#x85;\r]]>",
			"a\nb\nc\nd\ne\nf&#x85;\n|",
			"a\n\u0085b\u0085c\u2028d\ne\nf&#x85;\n|",
		},
		{
			"ProcInst",
			"<?pi \u0085\u2028\r\u0085 a\r\u0085b\u0085c\u2028d\r\ne\rf\r?>",
			"a\nb\nc\nd\ne\nf\n|",
			"\u0085\u2028\n\u0085 a\n\u0085b\u0085c\u2028d\ne\nf\n|",
		},
		{
			"Attr",
			"<e v=\"a\r\u0085b\u0085c\u2028d\r\ne\rf&#x85;&#x2028;&#13;g&#10;\th\"/>",
			"a b c d e f\u0085\u2028\rg\n h|",
			"a \u0085b\u0085c\u2028d e f\u0085\u2028\rg\n h|",
		},
		{
			"Mixed",
			"<e v=\"\u0085\">\u0085<!--\u0085--><![CDATA[\u0085]]><?pi \u0085x\u0085?>\u0085</e>",
			" |\n|\n|\n|x\n|\n|",
			"\u0085|\u0085|\u0085|\u0085|\u0085x\u0085|\u0085|",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, c := range []struct {
				in   string
				mode Mode
				want string
			}{
				{decl11 + "<r>" + tc.in + "</r>", XML, tc.want11},
				{"\ufeff" + decl11 + "<r>" + tc.in + "</r>", XML, tc.want11},
				{"<?xml version='1.1' encoding='UTF-8'?><r>" + tc.in + "</r>", XML, tc.want11},
				{decl11 + `<r xmlns="` + NamespaceXHTML + `">` + tc.in + "</r>", Universal, tc.want11},
				{decl11 + `<r xmlns="` + NamespaceXHTML + `"><x:s xmlns:x="` + NamespaceSVG + `">` + tc.in + "</x:s></r>", Universal, tc.want11},
				{"<r>" + tc.in + "</r>", XML, tc.want10},
				{decl10 + "<r>" + tc.in + "</r>", XML, tc.want10},
				{`<?xml version="1.10"?><r>` + tc.in + "</r>", XML, tc.want10},
				{" " + decl11 + "<r>" + tc.in + "</r>", XML, tc.want10},
				{"<!---->" + decl11 + "<r>" + tc.in + "</r>", XML, tc.want10},
				{"\ufeff\ufeff" + decl11 + "<r>" + tc.in + "</r>", XML, tc.want10},
				{`<?xml-stylesheet version="1.1"?><r>` + tc.in + "</r>", XML, tc.want10},
				{decl10 + `<r xmlns="` + NamespaceXHTML + `">` + tc.in + "</r>", Universal, tc.want10},
			} {
				for chunk := range 8 {
					for _, clone := range []bool{false, true} {
						if got := xml11Decoded(t, c.in, c.mode, chunk, clone); got != c.want {
							t.Errorf("%s %q (chunk %d, clone %v): got %q, want %q", c.mode, c.in, chunk, clone, got, c.want)
						}
					}
				}
			}
			// Under HTML rules, both versions are read the same.
			for _, c := range []struct {
				root string
				mode Mode
			}{
				{"<r>", Universal},
				{`<r xmlns="urn:x">`, Universal},
				{"<r>", HTML},
				{`<r xmlns="urn:x">`, HTML},
				{"<r><svg>", Universal},
			} {
				in10 := decl10 + c.root + tc.in + "</r>"
				in11 := decl11 + c.root + tc.in + "</r>"
				want := xml11Decoded(t, in10, c.mode, 0, false)
				for chunk := range 8 {
					for _, clone := range []bool{false, true} {
						if got := xml11Decoded(t, in11, c.mode, chunk, clone); got != want {
							t.Errorf("%s %q (chunk %d, clone %v): got %q, want %q", c.mode, in11, chunk, clone, got, want)
						}
					}
				}
			}
		})
	}
}

// TestXML11SetAttr checks that attribute values set by a Transformer in
// documents declared as XML 1.1 are read back unchanged.
func TestXML11SetAttr(t *testing.T) {
	const value = "1\u00852\u20283\n4\t5\x016\x7f7\u00808\u009f9\x1f"
	const xml11 = "1&#x85;2&#x2028;3&#10;4&#9;5&#x1;6&#x7f;7&#x80;8&#x9f;9&#x1f;"
	const xml10 = "1\u00852\u20283&#10;4&#9;5\x016\x7f7\u00808\u009f9\x1f"
	for _, tc := range []struct {
		in   string
		mode Mode
		want string
	}{
		{`<?xml version="1.1"?><a b="x"/>`, XML, `<?xml version="1.1"?><a b="` + xml11 + `" c="` + xml11 + `"/>`},
		{`<?xml version="1.0"?><a b="x"/>`, XML, `<?xml version="1.0"?><a b="` + xml10 + `" c="` + xml10 + `"/>`},
		{`<?xml version="1.1"?><a xmlns="http://www.w3.org/1999/xhtml" b="x"/>`, Universal, `<?xml version="1.1"?><a xmlns="http://www.w3.org/1999/xhtml" b="` + xml11 + `" c="` + xml11 + `"/>`},
		{`<?xml version="1.1"?><a xmlns="urn:x" b="x"/>`, Universal, `<?xml version="1.1"?><a xmlns="urn:x" b="` + xml10 + `" c="` + xml10 + `"/>`},
		{`<?xml version="1.1"?><a b="x"/>`, Universal, `<?xml version="1.1"?><a b="` + xml10 + `" c="` + xml10 + `"/>`},
	} {
		var b bytes.Buffer
		x := NewTransformer(&b, strings.NewReader(tc.in))
		x.SetMode(tc.mode)
		for x.Next() {
			if tok := x.Token(); tok.Kind() == StartTag {
				if err := x.SetAttr("b", value); err != nil {
					t.Fatal(err)
				}
				if err := x.SetAttr("c", value); err != nil {
					t.Fatal(err)
				}
				// the values as edited
				x.RemoveAttrs(func(a Attr) bool {
					if a.Value() != value && !a.Is("xmlns") {
						t.Errorf("%s %q: %s=%q, want %q", tc.mode, tc.in, a.Name(), a.Value(), value)
					}
					return false
				})
			}
		}
		if err := x.Close(); err != nil {
			t.Fatal(err)
		}
		if b.String() != tc.want {
			t.Errorf("%s %q: got %q, want %q", tc.mode, tc.in, b.String(), tc.want)
		}
		r := NewReader(strings.NewReader(b.String()))
		r.SetMode(tc.mode)
		for r.Next() {
			if tok := r.Token(); tok.Kind() == StartTag {
				for a := range tok.Attrs() {
					if a.Value() != value && !a.Is("xmlns") {
						t.Errorf("%s %q: %s=%q, want %q", tc.mode, b.String(), a.Name(), a.Value(), value)
					}
				}
			}
		}
	}
}
