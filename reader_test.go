package htmlstream

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"
	"unicode/utf8"

	"github.com/pgaskin/htmlstream/internal"
	misc_testdata "github.com/pgaskin/htmlstream/testdata/misc"
	xml_testdata "github.com/pgaskin/htmlstream/testdata/xml"
)

var modes = []Mode{HTML, XML, Universal}

// extraVectors are additional inputs for the passthrough tests.
var extraVectors = []string{
	"",
	"<",
	"<a",
	"</",
	"<!",
	"<!-",
	"<!--",
	"<!---",
	"<?",
	"&",
	"&amp",
	"\r",
	"x\r\ny",
	"  \r\n x",
	"<p>a<p>b<div>c</div>",
	"<ul><li>a<li>b</ul>",
	"<table><tr><td>a<td>b<tr><td>c</table>",
	"<b><i>x</b>y</i>",
	"<div><td>x</td><tr><tbody></div>",
	"<table><tr><td><template><td>t</template>x</table>y",
	"<table><tr><td>a</td><table><tr><td>b</table>c",
	"<svg><circle><img src=\"x",
	"<table><tr><td><div><td>y</table>",
	"<html><head><title>x</title><body>y",
	"<svg><circle/><foreignObject><p>x</foreignObject><title>t<b>u</b></title></svg>",
	"<svg><![CDATA[a<b]]></svg><![CDATA[x]]>",
	"<math><mi>x</mi><annotation-xml encoding=text/html><p>y</p></annotation-xml></math>",
	"<script>if (a < b && c > d) {}</script>",
	"<textarea>&lt;b&gt;</textarea>",
	"<plaintext><a></plaintext>",
	"<a href=\"x>y</a> <b class=\"z\">",
	"<!DOCTYPE html PUBLIC \"-//W3C//DTD XHTML 1.0 Strict//EN\" \"http://www.w3.org/TR/xhtml1/DTD/xhtml1-strict.dtd\">",
	"<!DOCTYPE x [<!ENTITY a \"b>\"><!-- > -->]><x>&a;</x>",
	"<?xml version=\"1.0\"?>\n<!DOCTYPE html PUBLIC \"-//W3C//DTD XHTML 1.0 Strict//EN \"http://www.w3.org/TR/xhtml1/DTD/xhtml1-strict.dtd\">\n<html xmlns=\"http://www.w3.org/1999/xhtml\"><p class=\"a\">x</p><p>y</p></html>",
	"<?xml version=\"1.0\"?><?php echo 1; ><html xmlns=\"http://www.w3.org/1999/xhtml\"><br>x</html>",
	"<?xml version=\"1.0\"?><?php echo 1;",
	"<?xml?x?><html xmlns=\"http://www.w3.org/1999/xhtml\"><br>x</html>",
	"<?xml version=\"1.0\"?><x:a xmlns:x=\"urn:x\"><x:b/></x:a>",
	"<html xmlns=\"http://www.w3.org/1999/xhtml\"><script src=\"a\"/><p>x</p></html>",
	"<Svg><linearGradient></lineargradient><rect/><foreignObject><p>x</FOREIGNOBJECT></svg>",
	"<math><mi>x</MI><annotation-xml ENCODING=text/html><p>y</ANNOTATION-XML></math>",
	"<div xmlns=\"\"><p>a<p>b<br></div><svg><p xmlns=\"\">x</p></svg>",
	"<html xmlns=\"http://www.w3.org/1999/xhtml\"><div xmlns=\"\"><p>a<p>b<br><i xmlns=\"\">c</i></div></html>",
	"<html xmlns:o=\"urn:o\" xmlns=\"http://www.w3.org/TR/REC-html40\"><head><meta charset=utf-8><style>p{}</style></head><p>a<br>b<o:p></o:p><p>c",
}

// allVectors returns the edge-case inputs in testdata/misc, extraVectors and
// the inputs of the test cases in testdata/xml.
func allVectors() []string {
	var vs []string
	for c := range misc_testdata.Categories() {
		vs = slices.AppendSeq(vs, misc_testdata.Items(c))
	}
	vs = append(vs, extraVectors...)
	for _, c := range xml_testdata.Cases("*.txtar", "in", "tokens") {
		in, _ := c.Section("in")
		vs = append(vs, in)
	}
	return vs
}

// readAll reads all tokens, checking that they partition the input.
// readAll reads all tokens and checks their positions and raw bytes. If
// chunk is negative, the chunk size changes after every token, cycling
// through 1 to -chunk.
func readAll(t testing.TB, in string, mode Mode, scripting bool, chunk int, rd func(io.Reader) io.Reader) []string {
	t.Helper()
	var src io.Reader = strings.NewReader(in)
	if rd != nil {
		src = rd(src)
	}
	r := NewReader(src)
	r.SetMode(mode)
	r.SetScripting(scripting)
	if chunk > 0 {
		r.SetChunkSize(chunk)
	}
	var raw bytes.Buffer
	var toks []string
	var pos Pos
	depth := 0
	for r.Next() {
		tok := r.Token()
		if !tok.Implied() && tok.Pos() != pos {
			t.Errorf("%s %q: token %v at %d, expected %d", mode, in, tok, tok.Pos(), pos)
		}
		pos += Pos(len(tok.Raw()))
		raw.Write(tok.Raw())
		toks = append(toks, tok.String())
		checkNSCache(t, r, tok)
		checkOpen(t, r)
		switch tok.Kind() {
		case StartTag:
			if r.Depth() > depth {
				depth = r.Depth()
			} else {
				depth = r.Depth()
			}
		}
		if chunk < 0 {
			r.SetChunkSize(1 + len(toks)%-chunk)
		}
		if len(toks) > 10*len(in)+100 {
			t.Fatalf("%s %q: too many tokens", mode, in)
		}
	}
	if err := r.Err(); err == ErrUnsupportedEncoding && len(toks) == 0 {
		return nil
	} else if err != nil {
		t.Errorf("%s %q: error: %v", mode, in, err)
	}
	if raw.String() != in {
		t.Errorf("%s %q (chunk %d): raw mismatch:\n got %q\nwant %q\ntokens: %q", mode, in, chunk, raw.String(), in, toks)
	}
	return toks
}

// checkNSCache checks that the namespace strings of tok (see
// Reader.lookupNSString), made once and then cached, match the declarations.
func checkNSCache(t testing.TB, r *Reader, tok *Token) {
	t.Helper()
	var self *Token
	switch tok.Kind() {
	case StartTag:
		self = tok
	case EndTag:
	default:
		return
	}
	prefixes := [][]byte{tok.Prefix(), {}, []byte("xml")}
	for a := range tok.Attrs() {
		if p := a.Prefix(); p != nil {
			prefixes = append(prefixes, p)
		}
	}
	for _, p := range prefixes {
		want, wok := r.lookupNS(p, self)
		for range 2 {
			if got, ok := r.lookupNSString(p, len(r.decls), self); got != string(want) || ok != wok {
				t.Errorf("%v: prefix %q: got %q %v, want %q %v", tok, p, got, ok, want, wok)
			}
		}
	}
}

// checkOpen checks r.open against the stack: each open element is in the
// list of open elements with the hash of its name, innermost first.
func checkOpen(t testing.TB, r *Reader) {
	t.Helper()
	n := 0
	for h, i := range r.open {
		for prev := int32(len(r.stack)); i >= 0; i = r.stack[i].same {
			if i >= prev || r.openHash(r.frameName(&r.stack[i])) != h {
				t.Fatalf("open element %d in the wrong list", i)
			}
			prev = i
			n++
		}
	}
	if n != len(r.stack) {
		t.Fatalf("%d open elements listed, want %d", n, len(r.stack))
	}
}

// mergedTokens reads all tokens, merging text, comments and CDATA sections
// split into multiple tokens, so the result doesn't depend on the chunk size.
// chunk is as for readAll.
func mergedTokens(t testing.TB, in string, mode Mode, scripting bool, chunk int, rd func(io.Reader) io.Reader) []string {
	t.Helper()
	var src io.Reader = strings.NewReader(in)
	if rd != nil {
		src = rd(src)
	}
	r := NewReader(src)
	r.SetMode(mode)
	r.SetScripting(scripting)
	if chunk > 0 {
		r.SetChunkSize(chunk)
	}
	type merged struct {
		kind      Kind
		pos       Pos
		flags     tokFlags
		raw, text []byte
	}
	var toks []string
	var cur *merged
	flush := func() {
		if cur != nil {
			toks = append(toks, fmt.Sprintf("%s@%d %b %q %q", cur.kind, cur.pos, cur.flags, cur.raw, cur.text))
			cur = nil
		}
	}
	for n := 0; r.Next(); n++ {
		if chunk < 0 {
			r.SetChunkSize(1 + n%-chunk)
		}
		tok := r.Token()
		if tok.Continued() {
			if cur == nil || cur.kind != tok.Kind() {
				t.Fatalf("%s %q (chunk %d): continued token %v without a partial one", mode, in, chunk, tok)
			}
		} else {
			flush()
		}
		switch tok.Kind() {
		case Text, Comment, CDATA:
			if utf8.ValidString(in) && !utf8.Valid(tok.Raw()) {
				t.Errorf("%s %q (chunk %d): token %v splits a UTF-8 sequence", mode, in, chunk, tok)
			}
			if cur == nil {
				cur = &merged{kind: tok.Kind(), pos: tok.Pos()}
			}
			cur.flags |= tok.flags &^ (tokPartial | tokContinued)
			cur.raw = append(cur.raw, tok.Raw()...)
			cur.text = tok.AppendText(cur.text)
			if !tok.Partial() {
				flush()
			}
		default:
			toks = append(toks, fmt.Sprintf("%v@%d depth %d", tok, tok.Pos(), r.Depth()))
		}
	}
	flush()
	if err := r.Err(); err == ErrUnsupportedEncoding {
		toks = append(toks, err.Error())
	} else if err != nil {
		t.Errorf("%s %q (chunk %d): error: %v", mode, in, chunk, err)
	}
	return toks
}

func TestUnsupportedEncoding(t *testing.T) {
	for _, in := range []string{
		"\xfe\xff\x00<", "\xff\xfe<\x00", "\x00\x00\xfe\xff",
		"\x00<\x00a", "\x00<\x00a\x00>", "<\x00a\x00", "<\x00a\x00>\x00",
	} {
		r := NewReader(strings.NewReader(in))
		for r.Next() {
		}
		if r.Err() != ErrUnsupportedEncoding {
			t.Errorf("%q: got error %v", in, r.Err())
		}
	}
}

func TestChunkLongNumericRef(t *testing.T) {
	in := "&#" + strings.Repeat("0", 100) + "65;x"
	want := mergedTokens(t, in, HTML, false, 0, nil)
	for chunk := 1; chunk <= 5; chunk++ {
		got := mergedTokens(t, in, HTML, false, chunk, nil)
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("chunk %d: tokens differ:\n got %q\nwant %q", chunk, got, want)
		}
	}
	// Up to the maximum token size, the reference is one token whatever the
	// chunk size, and beyond it, the reference is split, and the parts are
	// decoded separately.
	for _, tc := range []struct {
		zeros       int
		first, rest string
	}{
		{59, "A", "x"}, // 64 bytes
		{100, "\ufffd", strings.Repeat("0", 38) + "65;x"},
	} {
		r := NewReader(strings.NewReader("&#" + strings.Repeat("0", tc.zeros) + "65;x"))
		r.SetMaxTokenSize(64)
		r.SetChunkSize(1)
		var first, rest []byte
		if r.Next() {
			first = r.Token().AppendText(nil)
		}
		for r.Next() {
			rest = r.Token().AppendText(rest)
		}
		if r.Err() != nil || string(first) != tc.first || string(rest) != tc.rest {
			t.Errorf("%d zeros: got %q then %q, %v, want %q then %q", tc.zeros, first, rest, r.Err(), tc.first, tc.rest)
		}
	}
}

func TestChunkUTF8(t *testing.T) {
	// Every split point falls inside a multi-byte sequence for some chunk
	// size.
	text := strings.Repeat("\u00e9\u20ac\U0001F600", 20)
	for _, in := range []string{text, "<!--" + text + "-->", "<!" + text + ">", "<svg><![CDATA[" + text + "]]></svg>", "<script>" + text + "</script>"} {
		want := mergedTokens(t, in, HTML, false, 0, nil)
		for chunk := 1; chunk <= 12; chunk++ {
			got := mergedTokens(t, in, HTML, false, chunk, nil)
			if strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Errorf("%q (chunk %d): tokens differ:\n got %q\nwant %q", in, chunk, got, want)
			}
		}
	}
}

func TestChunkIndependence(t *testing.T) {
	for _, in := range allVectors() {
		for _, mode := range modes {
			for _, scripting := range []bool{false, true} {
				want := mergedTokens(t, in, mode, scripting, 0, nil)
				for _, chunk := range []int{1, 2, 3, 7, 64, -5, -70} {
					got := mergedTokens(t, in, mode, scripting, chunk, iotest.OneByteReader)
					if strings.Join(got, "\n") != strings.Join(want, "\n") {
						t.Errorf("%s %q (scripting %v, chunk %d): tokens differ:\n got %q\nwant %q", mode, in, scripting, chunk, got, want)
					}
				}
			}
		}
	}
}

func TestPassthrough(t *testing.T) {
	readers := map[string]func(io.Reader) io.Reader{
		"full":    nil,
		"onebyte": iotest.OneByteReader,
		"half":    iotest.HalfReader,
	}
	for _, in := range allVectors() {
		for _, mode := range modes {
			for _, scripting := range []bool{false, true} {
				if mode == Universal {
					checkUniversal(t, in, scripting)
				}
				want := readAll(t, in, mode, scripting, 0, nil)
				for name, rd := range readers {
					for _, chunk := range []int{0, 1, 2, 3, 7, 64, -5, -70} {
						got := readAll(t, in, mode, scripting, chunk, rd)
						if chunk == 0 && strings.Join(got, "\n") != strings.Join(want, "\n") {
							t.Errorf("%s %q (scripting %v): %s reader: tokens differ:\n got %q\nwant %q", mode, in, scripting, name, got, want)
						}
					}
				}
			}
		}
	}
}

// checkUniversal checks that from the first start or end tag on, Universal
// mode reads in like HTML mode with XML syntax, or like XML mode with the
// void elements of XHTML, as it chose (see Reader.decide). The other Reader
// reads the input with whitespace before that tag, which leaves it in the
// same state as the tokens before it, except for the state set below.
func checkUniversal(t testing.TB, in string, scripting bool) {
	t.Helper()
	var doctype bool // a doctype precedes the first tag
	read := func(r *Reader, from Pos) ([]string, Pos) {
		r.SetScripting(scripting)
		var toks []string
		for r.Next() {
			tok := r.Token()
			if from < 0 && tok.Kind() == Doctype {
				doctype = true
			}
			if from < 0 && (tok.Kind() == StartTag || tok.Kind() == EndTag) {
				from = tok.Pos()
			}
			if from >= 0 && tok.Pos() >= from {
				toks = append(toks, fmt.Sprintf("%s depth=%d", describe(tok), r.Depth()))
			}
		}
		return append(toks, fmt.Sprint(r.Err())), from
	}
	u := NewReader(strings.NewReader(in))
	u.SetMode(Universal)
	got, from := read(u, -1)
	if from < 0 {
		return // no tag
	}
	r := NewReader(strings.NewReader(strings.Repeat(" ", int(from)) + in[from:]))
	mode := u.EffectiveMode()
	r.SetMode(mode)
	r.SetXMLSyntax(mode == HTML)
	r.xhtmlVoid, r.xml11, r.doctype = mode == XML, u.xml11, doctype
	if want, _ := read(r, from); !slices.Equal(got, want) {
		t.Errorf("Universal %q (scripting %v) differs from %s mode:\n got %q\nwant %q", in, scripting, mode, got, want)
	}
}

func TestDump(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	for _, in := range allVectors() {
		for _, mode := range modes {
			toks := readAll(t, in, mode, false, 0, nil)
			t.Logf("%s %q", mode, in)
			for _, tok := range toks {
				t.Logf("    %s", tok)
			}
		}
	}
}

func FuzzPassthrough(f *testing.F) {
	for _, in := range allVectors() {
		for i := range modes {
			f.Add(in, uint8(i), uint8(0))
		}
	}
	// inputs which found bugs
	f.Add("<heAd> 0", uint8(0x08), uint8(0x9f))
	f.Add("  \r\n", uint8(0x14), uint8(0x03))
	f.Add("<\x000\x00", uint8(0x00), uint8(0x00))
	f.Add("<\x000\x000", uint8(0x00), uint8('U'))
	f.Add("&#0000000000000000000000000000000000", uint8('h'), uint8(0x01))
	f.Add("<!DOCTYPE><A><!DOCTYPE[>]>", uint8(0xac), uint8(0x00))
	f.Fuzz(func(t *testing.T, in string, mode, chunk uint8) {
		m := modes[int(mode&0x7f)%len(modes)]
		scripting := mode&0x80 != 0
		c := int(chunk)
		if chunk&0x80 != 0 {
			c = -int(chunk&0x7f) - 1
		}
		readAll(t, in, m, scripting, c, iotest.OneByteReader)
		if m == Universal {
			checkUniversal(t, in, scripting)
		}
		want := mergedTokens(t, in, m, scripting, 0, nil)
		got := mergedTokens(t, in, m, scripting, c, iotest.OneByteReader)
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("%s %q (scripting %v, chunk %d): tokens differ:\n got %q\nwant %q", m, in, scripting, c, got, want)
		}
	})
}

func TestReadText(t *testing.T) {
	in := "<p>" + strings.Repeat("a&amp;b", 100) + "</p><!--" + strings.Repeat("c", 50) + "-->"
	r := NewReader(strings.NewReader(in))
	r.SetChunkSize(7)
	var got []string
	for r.Next() {
		switch tok := r.Token(); tok.Kind() {
		case Text:
			b, trunc := r.ReadText(nil, 0)
			if trunc {
				t.Errorf("text truncated without a limit")
			}
			got = append(got, string(b))
		case Comment:
			b, trunc := r.ReadText(nil, 10)
			if !trunc {
				t.Errorf("comment not truncated")
			}
			got = append(got, string(b))
		}
		if tok := r.Token(); tok != nil && tok.Partial() {
			t.Errorf("current token is partial after ReadText: %v", tok)
		}
	}
	if err := r.Err(); err != nil {
		t.Fatal(err)
	}
	want := []string{strings.Repeat("a&b", 100), strings.Repeat("c", 10)}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestAttrValue(t *testing.T) {
	for _, tc := range []struct {
		mode    Mode
		in, out string
	}{
		{HTML, "<a b=\"x\r\ny\rz\x00&amp;&notit;&amp=\">", "x\ny\nz\uFFFD&&notit;&amp="},
		{XML, "<a b=\"x\r\ny\tz\n&amp;&#10;\"/>", "x y z &\n"},
	} {
		r := NewReader(strings.NewReader(tc.in))
		r.SetMode(tc.mode)
		if !r.Next() {
			t.Fatal(r.Err())
		}
		a, _ := r.Token().Attr("b")
		if got := a.Value(); got != tc.out {
			t.Errorf("%s %q: got %q, want %q", tc.mode, tc.in, got, tc.out)
		}
	}
}

// repeatReader returns s repeated n times, without allocating all of it.
func repeatReader(prefix, s string, n int, suffix string) io.Reader {
	rs := []io.Reader{strings.NewReader(prefix)}
	rep := strings.Repeat(s, max(1, 4096/max(len(s), 1)))
	total := len(s) * n
	for total > 0 {
		k := min(total, len(rep))
		rs = append(rs, strings.NewReader(rep[:k]))
		total -= k
	}
	rs = append(rs, strings.NewReader(suffix))
	return io.MultiReader(rs...)
}

func TestLimits(t *testing.T) {
	for _, tc := range []struct {
		Name     string
		R        func() io.Reader
		MaxToken int
		Err      error
		MaxBuf   int // maximum buffer size (0 for the default bound)
	}{
		{Name: "DeepNesting", R: func() io.Reader { return repeatReader("", "<div>", 200000, "x") }},
		{Name: "UnclosedInline", R: func() io.Reader { return repeatReader("", "<b>", 200000, "x") }},
		{Name: "HugeText", R: func() io.Reader { return repeatReader("<p>", "abc&amp; ", 2000000, "</p>") }},
		{Name: "HugeScript", R: func() io.Reader { return repeatReader("<script>", "a<b-->", 1000000, "</script>") }},
		{Name: "HugeEscapedScript", R: func() io.Reader { return repeatReader("<script><!--", "<script>x</script>--", 300000, "</script>") }},
		{Name: "UnterminatedComment", R: func() io.Reader { return repeatReader("<!--", "-- -", 2000000, "") }},
		{Name: "UnterminatedBogus", R: func() io.Reader { return repeatReader("<!x", "a", 5000000, "") }},
		{Name: "UnterminatedCDATA", R: func() io.Reader { return repeatReader("<svg><![CDATA[", "]]]", 1000000, "") }},
		{Name: "HugeAttr", R: func() io.Reader { return repeatReader(`<a href="`, "x", 1<<20, `">`) }, MaxToken: 1 << 16, Err: ErrTokenTooLarge, MaxBuf: 1<<16 + 16},
		{Name: "ManyAttrs", R: func() io.Reader { return repeatReader(`<a `, "b ", 1<<20, `>`) }, MaxToken: 1 << 16, Err: ErrTokenTooLarge, MaxBuf: 1<<16 + 16},
		{Name: "UnterminatedTag", R: func() io.Reader { return repeatReader(`<a `, "b=c ", 1<<20, ``) }, MaxToken: 1 << 16, Err: ErrTokenTooLarge, MaxBuf: 1<<16 + 16},
		{Name: "UnterminatedDoctype", R: func() io.Reader { return repeatReader(`<!DOCTYPE `, "x", 1<<20, ``) }, MaxToken: 1 << 16, Err: ErrTokenTooLarge, MaxBuf: 1<<16 + 16},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			for _, mode := range modes {
				r := NewReader(tc.R())
				r.SetMode(mode)
				if tc.MaxToken != 0 {
					r.SetMaxTokenSize(tc.MaxToken)
				}
				var n, maxBuf, maxNames, maxAttrs int
				for r.Next() {
					n += len(r.Token().Raw())
					maxBuf = max(maxBuf, len(r.buf))
					maxNames = max(maxNames, cap(r.names))
					maxAttrs = max(maxAttrs, cap(r.tok.attrs))
				}
				maxBuf = max(maxBuf, len(r.buf))
				if err := r.Err(); !errors.Is(err, tc.Err) {
					t.Errorf("%s: error %v, want %v", mode, err, tc.Err)
				} else if want := "htmlstream: token too large (MaxTokenSize is 65536 bytes)"; tc.Err == ErrTokenTooLarge && err.Error() != want {
					t.Errorf("%s: error %q, want %q", mode, err, want)
				}
				lim := tc.MaxBuf
				if lim == 0 {
					lim = 2*DefaultChunkSize + defaultBufSize
				}
				if maxBuf > lim {
					t.Errorf("%s: buffer grew to %d (limit %d)", mode, maxBuf, lim)
				}
				if maxNames > 2*DefaultMaxStackBytes {
					t.Errorf("%s: stack names grew to %d", mode, maxNames)
				}
				t.Logf("%s: read %d bytes, max buffer %d, max names %d, max attrs %d, depth limit hit %v", mode, n, maxBuf, maxNames, maxAttrs, r.untracked > 0)
			}
		})
	}
}

func BenchmarkReader(b *testing.B) {
	doc := benchDoc()
	b.SetBytes(int64(len(doc)))
	for b.Loop() {
		r := NewReader(bytes.NewReader(doc))
		for r.Next() {
		}
		if r.Err() != nil {
			b.Fatal(r.Err())
		}
	}
}

func BenchmarkReaderAttrs(b *testing.B) {
	doc := benchDoc()
	b.SetBytes(int64(len(doc)))
	var buf []byte
	for b.Loop() {
		r := NewReader(bytes.NewReader(doc))
		for r.Next() {
			tok := r.Token()
			switch tok.Kind() {
			case StartTag:
				for a := range tok.Attrs() {
					buf = a.AppendValue(buf[:0])
				}
			case Text:
				buf = tok.AppendText(buf[:0])
			}
		}
	}
}

func BenchmarkTransformer(b *testing.B) {
	doc := benchDoc()
	b.SetBytes(int64(len(doc)))
	for b.Loop() {
		x := NewTransformer(io.Discard, bytes.NewReader(doc))
		for x.Next() {
			if tok := x.Token(); tok.Kind() == StartTag && tok.Is("a") {
				x.SetAttr("rel", "noopener")
			}
		}
		if err := x.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

func TestAllocs(t *testing.T) {
	doc := benchDoc()
	r := NewReader(bytes.NewReader(doc))
	allocs := testing.AllocsPerRun(5, func() {
		r = NewReader(bytes.NewReader(doc))
		for r.Next() {
		}
	})
	t.Logf("%.0f allocations per %d byte document", allocs, len(doc))
	if allocs > 100 {
		t.Errorf("too many allocations: %.0f", allocs)
	}
}

// benchDoc returns a ~1 MB HTML document.
func benchDoc() []byte {
	var b bytes.Buffer
	b.WriteString("<!DOCTYPE html><html><head><title>Bench</title><style>p > a { color: red }</style></head><body>\n")
	for i := 0; b.Len() < 1<<20; i++ {
		b.WriteString(`<div class="item" id="item-`)
		b.WriteString(strconv.Itoa(i))
		b.WriteString(`"><h2>Item &amp; title</h2><p>Some <b>bold</b> text with a <a href="/x?a=1&amp;b=2">link</a> and <img src=a.png alt="">.<br>`)
		b.WriteString("<ul><li>one<li>two<li>three</ul><!-- comment --><script>if (a < b) { x++ }</script></div>\n")
	}
	b.WriteString("</body></html>")
	return b.Bytes()
}

func TestReadError(t *testing.T) {
	e := errors.New("read error")
	r := NewReader(io.MultiReader(strings.NewReader("<a href=x>text <i>more"), iotest.ErrReader(e)))
	var text []byte
	for r.Next() {
		if tok := r.Token(); tok.Kind() == Text {
			text = tok.AppendText(text)
		}
	}
	if r.Err() != e {
		t.Errorf("got error %v, want %v", r.Err(), e)
	}
	if string(text) != "text more" {
		t.Errorf("got text %q", text)
	}
}

func TestReadTextUTF8(t *testing.T) {
	r := NewReader(strings.NewReader("aé€"))
	r.Next()
	// 0 is no limit
	for limit, want := range []string{"aé€", "a", "a", "aé", "aé", "aé", "aé€", "aé€"} {
		got, trunc := r.ReadText(nil, limit)
		if wantTrunc := limit > 0 && limit < 6; string(got) != want || trunc != wantTrunc {
			t.Errorf("limit %d: got %q, %v, want %q, %v", limit, got, trunc, want, wantTrunc)
		}
	}
}

func TestReadTextTruncated(t *testing.T) {
	// The limit falls exactly at a chunk boundary, and the last chunk of
	// the comment decodes to nothing ("-->").
	for _, tc := range []struct {
		in    string
		chunk int
		limit int
		want  string
		trunc bool
	}{
		{"<!--abcdef-->", 5, 6, "abcdef", false},
		{"<!--abcdef-->", 5, 5, "abcde", true},
		{"<!--abcdef-->", 10, 6, "abcdef", false},
		{"abcdef<p>", 3, 6, "abcdef", false},
		{"abcdefg<p>", 3, 6, "abcdef", true},
		{"abcdefg<p>", 3, 1, "a", true},
		{"abcdef<p>", 3, 0, "abcdef", false},
		{"<!---->", 3, 0, "", false},
	} {
		r := NewReader(strings.NewReader(tc.in))
		r.SetChunkSize(tc.chunk)
		r.Next()
		got, trunc := r.ReadText(nil, tc.limit)
		if string(got) != tc.want || trunc != tc.trunc {
			t.Errorf("%q chunk %d limit %d: got %q, %v, want %q, %v", tc.in, tc.chunk, tc.limit, got, trunc, tc.want, tc.trunc)
		}
		if r.Token().Partial() {
			t.Errorf("%q chunk %d limit %d: current token is partial", tc.in, tc.chunk, tc.limit)
		}
	}
}

// structure returns a compact description of the tokens: "<a" for start
// tags, "</a>" for end tags, "</a?>" for implied end tags, "</a!>" for stray
// end tags, and "#" for text.
func structure(t *testing.T, in string, mode Mode) string {
	t.Helper()
	r := NewReader(strings.NewReader(in))
	r.SetMode(mode)
	return readStructure(t, r)
}

func readStructure(t *testing.T, r *Reader) string {
	t.Helper()
	var b []string
	for r.Next() {
		tok := r.Token()
		switch tok.Kind() {
		case StartTag:
			b = append(b, "<"+string(tok.Name()))
		case EndTag:
			switch {
			case tok.Implied():
				b = append(b, "</"+string(tok.Name())+"?>")
			case tok.Stray():
				b = append(b, "</"+string(tok.Name())+"!>")
			default:
				b = append(b, "</"+string(tok.Name())+">")
			}
		case Text:
			b = append(b, "#")
		}
	}
	if err := r.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(b, " ")
}

func TestStructure(t *testing.T) {
	for _, tc := range []struct {
		mode    Mode
		in, out string
	}{
		{HTML, `<a href=x>A<ul><li><a href=y>B</a></li></ul>`, `<a # <ul <li <a # </a> </li> </ul> </a?>`},
		{HTML, `<a>x<b><a>y`, `<a # <b </b?> </a?> <a # </a?>`},
		{HTML, `<head>Hello<title>x</title>`, `<head </head?> # <title # </title>`},
		{HTML, `<head><svg></svg>`, `<head </head?> <svg </svg>`},
		{HTML, `<head> <title>x</title>`, `<head # <title # </title> </head?>`},
		{Universal, `<svg xmlns="http://www.w3.org/2000/svg"><p>x</p></svg>`, `<svg </svg?> <p # </p> </svg!>`},
		{Universal, `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml"><svg xmlns="http://www.w3.org/2000/svg"><p>x</p></svg>`, `<html <svg <p # </p> </svg> </html?>`},
		{HTML, `<p>a<head><head>b`, `<p # <head <head # </p?>`},
		{HTML, `<table><tr><td>a<body>b</table>`, `<table <tr <td # <body # </td?> </tr?> </table>`},
		{HTML, `<html><head></head><body><html><body>x</body></html>`, `<html <head </head> <body <html <body # </body> </html>`},
		{HTML, `<head><html><title>x</title></head>`, `<head <html <title # </title> </head>`},
		{HTML, `<p><rb>a<rt>b</p>`, `<p <rb # <rt # </rt?> </rb?> </p>`},
		{HTML, `<ruby><rb>a<rt>b</ruby>`, `<ruby <rb # </rb?> <rt # </rt?> </ruby>`},
		{HTML, `<ruby><div><p>a<rp>b</div></ruby>`, `<ruby <div <p # </p?> <rp # </rp?> </div> </ruby>`},
		{HTML, `<ruby><rtc><li>a<rt>b<rb>c</ruby>`, `<ruby <rtc <li # </li?> <rt # </rt?> </rtc?> <rb # </rb?> </ruby>`},
		{HTML, `<select><option>a<optgroup><option>b<hr><option>c<input>`, `<select <option # </option?> <optgroup <option # </option?> </optgroup?> <hr <option # </option?> </select?> <input`},
		{HTML, `<option>a<optgroup>b<optgroup>c<div><select></div>`, `<option # </option?> <optgroup # <optgroup # <div <select </div!> </select?> </div?> </optgroup?> </optgroup?>`},
		{HTML, `<table><colgroup><template><col></template><col><caption>`, `<table <colgroup <template <col </template> <col </colgroup?> <caption </caption?> </table?>`},
		{HTML, `<table><tr><td><b>x<col><caption>y<col><colgroup><col>`, `<table <tr <td <b # </b?> </td?> </tr?> <col <caption # </caption?> <col <colgroup <col </colgroup?> </table?>`},
		{HTML, `<table><colgroup><col></a><col></colgroup><colgroup> <template></template></col>x<col></table>`, `<table <colgroup <col </colgroup?> </a!> <col </colgroup!> <colgroup # <template </template> </col!> </colgroup?> # <col </table>`},
		{HTML, `<table><colgroup> x<col>`, `<table <colgroup # </colgroup?> # <col </table?>`},
		{HTML, `<table><colgroup><html><col><body><col>`, `<table <colgroup <html <col </colgroup?> <body <col </table?>`},
		{HTML, `<template><table><tr><td><template><select></template></template><tr>`, `<template <table <tr <td <template <select </select?> </template> </td?> </tr?> </table?> </template> <tr </tr?>`},
		{HTML, `<h1>x<h2>y</h1>z`, `<h1 # </h1?> <h2 # </h1> #`},
		{HTML, `<h1>x</h2>y`, `<h1 # </h2> #`},
		// End tags with a foreign current node close through integration
		// points (foreign content rules), matching names case-insensitively.
		{HTML, `<svg><foreignObject>x</svg>y`, `<svg <foreignObject # </foreignObject?> </svg> #`},
		{HTML, `<svg><desc>x</svg>y`, `<svg <desc # </desc?> </svg> #`},
		{HTML, `<svg><title>x</svg>y`, `<svg <title # </title?> </svg> #`},
		{HTML, `<math><mi>x</math>y`, `<math <mi # </mi?> </math> #`},
		{HTML, `<math><annotation-xml encoding=text/html>x</math>y`, `<math <annotation-xml # </annotation-xml?> </math> #`},
		{HTML, `<svg><foreignObject><math><mi>x</foreignObject>y`, `<svg <foreignObject <math <mi # </mi?> </math?> </foreignObject> # </svg?>`},
		{HTML, `<math><mi><svg><g>x</math>y`, `<math <mi <svg <g # </g?> </svg?> </mi?> </math> #`},
		{HTML, `<svg><G>x</g>y`, `<svg <G # </g> # </svg?>`},
		{HTML, `<svg><foreignObject>x</foreignObject>y`, `<svg <foreignObject # </foreignObject> # </svg?>`},
		{HTML, `<svg><foreignObject><svg><g>x</foreignObject>y`, `<svg <foreignObject <svg <g # </g?> </svg?> </foreignObject> # </svg?>`},
		// With an HTML current node, the HTML rules stop at the integration
		// point (a special element) and the end tag is ignored.
		{HTML, `<svg><foreignObject><p>x</svg>y`, `<svg <foreignObject <p # </svg!> # </p?> </foreignObject?> </svg?>`},
		{HTML, `<svg><foreignObject><span>x</svg>y`, `<svg <foreignObject <span # </svg!> # </span?> </foreignObject?> </svg?>`},
		{HTML, `<math><mi><b>x</math>y`, `<math <mi <b # </math!> # </b?> </mi?> </math?>`},
		// br and p end tags break out of foreign content, up to an
		// integration point.
		{HTML, `<svg><g>x</p>y`, `<svg <g # </g?> </svg?> </p!> #`},
		{HTML, `<svg><g>x</br>y`, `<svg <g # </g?> </svg?> </br!> #`},
		{HTML, `<p><svg><g>x</p>y`, `<p <svg <g # </g?> </svg?> </p> #`},
		{HTML, `<math><mi>x</p>y`, `<math <mi # </p!> # </mi?> </math?>`},
		{HTML, `<svg><g>x</div>y`, `<svg <g # </div!> # </g?> </svg?>`},
		{Universal, `<svg xmlns="http://www.w3.org/2000/svg"><g>x</p>y`, `<svg <g # </g?> </svg?> </p!> #`},
		{Universal, `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml"><svg><g>x</p>y`, `<html <svg <g # </p!> # </g?> </svg?> </html?>`},
		{XML, `<svg><foreignObject>x</svg>y`, `<svg <foreignObject # </foreignObject?> </svg> #`},
		{XML, `<svg><g>x</p>y`, `<svg <g # </p!> # </g?> </svg?>`},
		// End tags are matched by name only to open elements, also with
		// names nested more than once or matching under HTML rules. "fu"
		// and "x" had the same hash in an earlier version.
		{XML, `<x><y></fu></zz></x>`, `<x <y </fu!> </zz!> </y?> </x>`},
		{HTML, `<x><table><y></fu></zz></x>`, `<x <table <y </fu!> </zz!> </x!> </y?> </table?> </x?>`},
		{XML, `<x><x><y></x></x></x><X>`, `<x <x <y </y?> </x> </x> </x!> <X </X?>`},
		{HTML, `<x><X><y></x></x></X>`, `<x <X <y </y?> </x> </x> </X!>`},
		{HTML, "<a\x00><A\uFFFD></a\uFFFD></A\x00></a\x00>", "<a\x00 <A\uFFFD </a\uFFFD> </A\x00> </a\x00!>"},
		{HTML, `<h1><b></h2></h3>`, `<h1 <b </b?> </h2> </h3!>`},
	} {
		if got := structure(t, tc.in, tc.mode); got != tc.out {
			t.Errorf("%s %q:\n got %s\nwant %s", tc.mode, tc.in, got, tc.out)
		}
	}
}

func TestHeadNoscript(t *testing.T) {
	for _, tc := range []struct{ in, out string }{
		{`<head><noscript><link><style>x</style>a`, `<head <noscript <link <style # </style> </noscript?> </head?> #`},
		{`<head><noscript> <title>x</title>`, `<head <noscript # </noscript?> <title # </title> </head?>`},
		{`<head><noscript><div>x`, `<head <noscript </noscript?> </head?> <div # </div?>`},
		{`<head><noscript><svg>`, `<head <noscript </noscript?> </head?> <svg </svg?>`},
		{`<head><noscript></head></p></noscript>`, `<head <noscript </head!> </p!> </noscript> </head?>`},
		{`<head><noscript><noscript>`, `<head <noscript <noscript </noscript?> </noscript?> </head?>`},
		{`<body><noscript><div>x`, `<body <noscript <div # </div?> </noscript?> </body?>`},
	} {
		r := NewReader(strings.NewReader(tc.in))
		r.SetScripting(false)
		if got := readStructure(t, r); got != tc.out {
			t.Errorf("%q:\n got %s\nwant %s", tc.in, got, tc.out)
		}
	}
}

func TestAccessors(t *testing.T) {
	r := NewReader(strings.NewReader(`<html xmlns:epub="http://www.idpf.org/2007/ops"><P class="a  b" epub:type="toc x"><svg><use xlink:href=#x /></svg></p></i>`))
	r.SetKeepAttrs(true)
	var got []string
	for r.Next() {
		tok := r.Token()
		var buf [16]byte
		switch tok.Kind() {
		case StartTag:
			got = append(got, string(tok.AppendLocalName(buf[:0])))
			for a := range tok.Attrs() {
				got = append(got, "@"+string(a.Raw())+"="+tok.AttrNamespace(a))
			}
			if HasClass("b")(tok) {
				got = append(got, "class b")
			}
			if AttrHasWord("epub:type", "toc")(tok) {
				got = append(got, "toc")
			}
			if HasAttrNS("http://www.idpf.org/2007/ops", "type")(tok) {
				got = append(got, "epub:type")
			}
			for e := range r.Stack() {
				got = append(got, "ns "+e.NS())
				break
			}
		case EndTag:
			got = append(got, "/"+string(tok.Name())+" "+strconv.FormatInt(int64(tok.StartPos()), 10))
		}
	}
	want := []string{
		"html", `@xmlns:epub="http://www.idpf.org/2007/ops"=` + NamespaceXMLNS, "ns " + NamespaceXHTML,
		"p", `@class="a  b"=`, `@epub:type="toc x"=http://www.idpf.org/2007/ops`, "class b", "toc", "epub:type", "ns " + NamespaceXHTML,
		"svg", "ns " + NamespaceSVG,
		"use", "@xlink:href=#x=" + NamespaceXLink, "ns " + NamespaceSVG,
		"/svg 82", "/p 48", "/i -1", "/html 0",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestElementAttrs checks the kept attributes of each open element,
// including elements without attributes between elements with them.
func TestElementAttrs(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`<a><b x="1" y="2">`, `b[x y] a[]`},
		{`<a x=1><b><c y=2>`, `c[y] b[] a[x]`},
		{`<a><b><c>`, `c[] b[] a[]`},
		{`<a x=1 z><b><c>`, `c[] b[] a[x z]`},
		{`<a x=1><b y=2></b><c><d>`, `d[] c[] a[x]`},
	} {
		r := NewReader(strings.NewReader(tc.in))
		r.SetMode(XML)
		r.SetKeepAttrs(true)
		var got string
		for r.Next() {
			var s []string
			for e := range r.Stack() {
				var names []string
				for a := range e.Attrs() {
					names = append(names, string(a.Name()))
				}
				s = append(s, string(e.Name())+"["+strings.Join(names, " ")+"]")
			}
			if r.Token().Kind() == StartTag {
				got = strings.Join(s, " ")
			}
		}
		if got != tc.want {
			t.Errorf("%q: got %s, want %s", tc.in, got, tc.want)
		}
	}
}

func TestXMLSyntax(t *testing.T) {
	// HTML mode with XML syntax: self-closing tags close non-void elements
	// (including raw text and RCDATA elements), void elements are closed by
	// their end tag, CDATA sections and processing instructions are
	// recognized everywhere (processing instructions end at the first ">",
	// like the bogus comments HTML parsers read), and doctypes may have an
	// internal subset. Otherwise the HTML rules apply.
	const in = `<?xml version="1.0"?><!DOCTYPE x [<!ENTITY a "b>">]><div/>x</div><title/><p><![CDATA[<a>]]><script/>&amp;<br/><P>y<br></BR><img>z</img>`
	const pi = `<p>a<?php echo $a->b; ?><?pi x?><?xml:namespace prefix="o" /><?><??>b<?x y`
	for _, tc := range []struct {
		mode      Mode
		in        string
		xmlSyntax bool
		out       string
	}{
		{HTML, in, true, `?xml !D <div/ #x </div!> <title/ <p [<a>] <script/ #& <br/ </p?> <P #y <br </BR> <img </img?> #z </img!> </P?>`},
		{HTML, in, false, `!?xml version="1.0"? !D #">]> <div/ #x </div> <title/ #<p><![CDATA[<a>]]><script/>&<br/><P>y<br></BR><img>z</img> </title?>`},
		{HTML, pi, true, `<p #a !?php echo $a- #b; ?> ?pi !?xml:namespace prefix="o" / !? ? #b !?x y </p?>`},
		{HTML, pi, false, `<p #a !?php echo $a- #b; ?> !?pi x? !?xml:namespace prefix="o" / !? !?? #b !?x y </p?>`},
		{HTML, `<!DOCTYPE x SYSTEM "a>b"><p>`, true, `!D #b"> <p </p?>`},
		{HTML, `<!DOCTYPE x [<!ENTITY a "b"><p>`, true, `!D <p </p?>`},
		// Universal mode: after an XML declaration, as in XML mode, unless
		// the end of the input is reached
		{Universal, `<?xml version="1.0"?><?pi a>b?><!DOCTYPE x SYSTEM "a>b"><p>`, false, `?xml ?pi !D <p </p?>`},
		{Universal, `<?xml version="1.0"?><?php echo 1; ><!DOCTYPE x SYSTEM "a b><p>x`, false, `?xml !?php echo 1;  !D <p #x </p?>`},
		{Universal, `<?xml version="1.0"?><?php echo 1;`, false, `?xml !?php echo 1;`},
	} {
		r := NewReader(strings.NewReader(tc.in))
		r.SetMode(tc.mode)
		r.SetXMLSyntax(tc.xmlSyntax)
		var b []string
		for r.Next() {
			switch tok := r.Token(); tok.Kind() {
			case StartTag:
				s := "<" + string(tok.Name())
				if tok.SelfClosing() {
					s += "/"
				}
				b = append(b, s)
			case EndTag:
				s := "</" + string(tok.Name())
				switch {
				case tok.Implied():
					s += "?"
				case tok.Stray():
					s += "!"
				}
				b = append(b, s+">")
			case Text:
				b = append(b, "#"+string(tok.AppendText(nil)))
			case CDATA:
				b = append(b, "["+string(tok.AppendText(nil))+"]")
			case ProcInst:
				b = append(b, "?"+string(tok.Name()))
			case Comment:
				b = append(b, "!"+string(tok.Data()))
			case Doctype:
				b = append(b, "!D")
			}
		}
		if got := strings.Join(b, " "); got != tc.out {
			t.Errorf("%s, xml syntax %v: %s\n got %s\nwant %s", tc.mode, tc.xmlSyntax, tc.in, got, tc.out)
		}
	}
}

func TestHTMLRules(t *testing.T) {
	const xhtml = ` xmlns="http://www.w3.org/1999/xhtml"`
	for _, tc := range []struct {
		mode Mode
		in   string
		want string // HTMLRules of the start tags and stray end tags
	}{
		{HTML, `<p><svg><g/></svg><o:p>`, "1111"},
		{XML, `<p` + xhtml + `><svg/>`, "00"},
		{Universal, `<p><svg><g/></svg><x:p/></BR><b` + xhtml + `><i/></b>`, "1111111"},
		{Universal, `<?xml version="1.0"?><b` + xhtml + `></BR><x:i/></x:i></b><p/>`, "00000"},
	} {
		r := NewReader(strings.NewReader(tc.in))
		r.SetMode(tc.mode)
		var got []byte
		for r.Next() {
			if tok := r.Token(); tok.Kind() == StartTag || tok.Stray() {
				got = append(got, map[bool]byte{false: '0', true: '1'}[tok.HTMLRules()])
			}
		}
		if string(got) != tc.want {
			t.Errorf("%s %q: got %s, want %s", tc.mode, tc.in, got, tc.want)
		}
	}
}

// TestIncompleteEOF checks constructs cut off by the end of the input.
func TestIncompleteEOF(t *testing.T) {
	for _, tc := range []struct {
		mode Mode
		in   string
	}{
		{HTML, `<!x abc`},
		{HTML, `</ x`},
		{HTML, `<?pi abc`},
		{HTML, `<!-- abc`},
		{HTML, `<!DOCTYPE abc`},
		{HTML, `<svg><![CDATA[abc`},
		{XML, `<?pi abc`},
		{HTML, `<a b=`},
	} {
		r := NewReader(strings.NewReader(tc.in))
		r.SetMode(tc.mode)
		var tok *Token
		for r.Next() {
			tok = r.Token()
		}
		if tok.Kind() == EndTag {
			continue // implied end tag
		}
		if !tok.Incomplete() || !tok.Unsound() {
			t.Errorf("%s %q: %v", tc.mode, tc.in, tok)
		}
	}

	// An incomplete start tag is classified like a complete one.
	r := NewReader(strings.NewReader(`<DIV class="x`))
	r.SetMode(Universal)
	r.Next()
	if tok := r.Token(); !tok.Incomplete() || !tok.Is("div") {
		t.Errorf("incomplete start tag in Universal mode: %v", tok)
	}
}

// TestElementNS checks that the namespace of an open element uses only the
// declarations in scope at it.
func TestElementNS(t *testing.T) {
	var got []string
	r := NewReader(strings.NewReader(`<p:a xmlns:p="u" xmlns="d"><p:b xmlns:p="v"><c xmlns=""><xml:d><e>`))
	r.SetMode(XML)
	for r.Next() {
		if r.Token().Kind() == StartTag && r.Token().Is("e") {
			for e := range r.Stack() {
				got = append(got, string(e.Name())+"="+e.NS())
			}
		}
	}
	want := "e= xml:d=" + NamespaceXML + " c= p:b=v p:a=u"
	if s := strings.Join(got, " "); s != want {
		t.Errorf("got %s, want %s", s, want)
	}
}

// TestElementMatchesToken checks that the innermost open element gives the
// same names, namespaces and attribute namespaces as the start tag which
// opened it.
func TestElementMatchesToken(t *testing.T) {
	ins := append(allVectors(),
		`<svg xmlns:xlink="x" xlink:href="a" xlink:foo="b" xml:lang="c"><a:b xmlns:a="u" a:c="d" xmlns:A="v"></a:b></svg>`,
		`<html xmlns="http://www.w3.org/1999/xhtml"><svg xmlns="http://www.w3.org/2000/svg" xlink:href="a"><p:x xmlns:p="u"/><math><mi xmlns:xlink="y" xlink:href="b">`,
		"<A\x00b C\x00d=1><Svg xmlns:X='u'><X:Y X:z=1>",
	)
	for _, m := range modes {
		for _, in := range ins {
			r := NewReader(strings.NewReader(in))
			r.SetMode(m)
			r.SetKeepAttrs(true)
			for r.Next() {
				tok := r.Token()
				if tok.Kind() != StartTag || !tok.Opens() || tok.Untracked() {
					continue
				}
				var e Element
				for e = range r.Stack() {
					break
				}
				diff := func(what string, got, want any) {
					t.Helper()
					if fmt.Sprint(got) != fmt.Sprint(want) {
						t.Errorf("%s %q %v: %s: element %q, token %q", m, in, tok, what, got, want)
					}
				}
				diff("Name", string(e.Name()), string(tok.Name()))
				diff("Prefix", string(e.Prefix()), string(tok.Prefix()))
				diff("Local", string(e.Local()), string(tok.Local()))
				diff("HTMLRules", e.HTMLRules(), tok.HTMLRules())
				diff("AppendLocalName", string(e.AppendLocalName(nil)), string(tok.AppendLocalName(nil)))
				diff("NS", e.NS(), tok.NS())
				for _, name := range []string{string(tok.Name()), string(tok.Local()), string(tok.AppendLocalName(nil)), strings.ToUpper(string(tok.Local()))} {
					diff("Is "+name, e.Is(name), tok.Is(name))
					diff("LocalIs "+name, e.LocalIs(name), tok.LocalIs(name))
				}
				var ta, ea []Attr
				for a := range tok.Attrs() {
					ta = append(ta, a)
				}
				for a := range e.Attrs() {
					ea = append(ea, a)
				}
				if len(ta) != len(ea) {
					t.Errorf("%s %q %v: %d element attributes, %d token attributes", m, in, tok, len(ea), len(ta))
					continue
				}
				for i, a := range ta {
					ns := tok.AttrNamespace(a)
					diff("AttrNamespace "+string(a.Name()), e.AttrNamespace(ea[i]), ns)
					name := string(a.Name())
					diff("HasAttr "+name, e.HasAttr(name), tok.HasAttr(name))
					_, eok := e.AttrNS(ns, string(a.Local()))
					_, tok2 := tok.AttrNS(ns, string(a.Local()))
					diff("AttrNS "+ns+" "+string(a.Local()), eok, tok2)
				}
			}
		}
	}
}

// TestUniversal checks where Universal mode chooses the rules of a
// document: the effective mode after each token, U before the choice, then H
// or X.
func TestUniversal(t *testing.T) {
	const decl, xhtml = `<?xml version="1.0"?>`, ` xmlns="http://www.w3.org/1999/xhtml"`
	for _, tc := range []struct{ in, want string }{
		{``, ``},
		{`<html` + xhtml + `>`, `HH`},
		{decl + `<html` + xhtml + `>`, `UXX`},
		{"\xef\xbb\xbf" + decl + "\n<!--c--><?pi?><!DOCTYPE html> <html" + xhtml + "><br>", `UUUUUUUXXXX`},
		{decl + `<h:html xmlns:h="http://www.w3.org/1999/xhtml">`, `UXX`},
		{decl + `<html` + xhtml + `/>`, `UX`},
		{decl + `<html` + xhtml, `UX`},
		{"\xef\xbb\xbf<html" + xhtml + ">", `HHH`},
		{"\xef\xbb\xbf<?xml-stylesheet?><html" + xhtml + ">", `UHHH`},
		{` ` + decl + `<html` + xhtml + `>`, `HHHH`},
		{`<!---->` + decl + `<html` + xhtml + `>`, `HHHH`},
		{`<?xml version="1.0" a>b?><html` + xhtml + `>`, `HHHH`},
		{decl + `<html xmlns:h="http://www.w3.org/1999/xhtml">`, `UHH`},
		{decl + `<h:html xmlns="http://www.w3.org/1999/xhtml">`, `UHH`},
		{decl + `<html XMLNS="http://www.w3.org/1999/xhtml">`, `UHH`},
		{decl + `<html xmlns="http://www.w3.org/1999/xhtml/">`, `UHH`},
		{decl + `<rss><html` + xhtml + `>`, `UHHH`},
		{decl + `</x><html` + xhtml + `>`, `UHHH`},
		{decl + `<![CDATA[x]]><html` + xhtml + `>`, `UHHH`},
		{decl + ` x<html` + xhtml + `>`, `UUHHH`},
		{decl + `<_x` + xhtml + `>`, `UH`},
		// a doctype or processing instruction reaching the end of the input
		// as XML ends at the first ">" in it
		{decl + `<!DOCTYPE html PUBLIC "a b><html` + xhtml + `><br>`, `UUXXXX`},
		{decl + `<!DOCTYPE html [<!ENTITY a "b>"><html` + xhtml + `><br>`, `UUHHHHH`},
		{decl + `<?php echo 1; ><html` + xhtml + `><br>`, `UUXXXX`},
		{decl + `<?php><p>`, `UUHH`},
		{decl + `<!DOCTYPE html PUBLIC "a b`, `UU`},
		{decl + `<?php`, `UU`},
		{decl + `<!DOCTYPE x SYSTEM "a>b"><html` + xhtml + `>`, `UUXX`},
		{decl + `<?pi a>b?><html` + xhtml + `>`, `UUXX`},
		// declarations recognized without validating them
		{`<?xml?><html` + xhtml + `>`, `UXX`},
		{`<?xml encoding="utf-8"?><html` + xhtml + `>`, `UXX`},
		{"<?xml\tv?><html" + xhtml + ">", `UXX`},
		{`<?xml?x?><html` + xhtml + `>`, `HHH`},
		{`<?xmlx?><html` + xhtml + `>`, `HHH`},
	} {
		r := NewReader(strings.NewReader(tc.in))
		r.SetMode(Universal)
		got := []byte{"HXU"[r.EffectiveMode()]}
		for r.Next() {
			got = append(got, "HXU"[r.EffectiveMode()])
			if m, _ := internal.TokenMode(r.Token()); Mode(m) != Universal || r.Mode() != Universal {
				t.Errorf("%q: token mode %s, mode %s", tc.in, Mode(m), r.Mode())
			}
		}
		if string(got) != "U"+tc.want {
			t.Errorf("%q: got %s, want U%s", tc.in, got, tc.want)
		}
	}
}

func TestUniversalDeclaredXHTML(t *testing.T) {
	const in = `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml" xmlns:h="http://www.w3.org/1999/xhtml"><script>a &lt; b<![CDATA[<x>]]></script><p>a<div>b</div></p><br><img/>&notit;<i title="&amp=">x</I></i><h:script>c&lt;</h:script><h:br>d</h:BR><h:p>e<h:p>f</h:p></h:p></html>`
	for _, tc := range []struct {
		out  string
		text string
	}{
		{`<html <script # # </script> <p # <div # </div> </p> <br </br?> <img # <i # </I!> </i> <h:script # </h:script> <h:br </h:br?> # </h:BR!> <h:p # <h:p # </h:p> </h:p> </html>`, "a < b<x>ab&notit;xc<def|&amp="},
	} {
		r := NewReader(strings.NewReader(in))
		r.SetMode(Universal)
		var b []string
		var text []byte
		var attr string
		for r.Next() {
			tok := r.Token()
			switch tok.Kind() {
			case StartTag:
				b = append(b, "<"+string(tok.Name()))
				if a, ok := tok.Attr("title"); ok {
					attr = a.Value()
				}
			case EndTag:
				switch {
				case tok.Implied():
					b = append(b, "</"+string(tok.Name())+"?>")
				case tok.Stray():
					b = append(b, "</"+string(tok.Name())+"!>")
				default:
					b = append(b, "</"+string(tok.Name())+">")
				}
			case Text, CDATA:
				b = append(b, "#")
				text = tok.AppendText(text)
			}
		}
		if got := strings.Join(b, " "); got != tc.out {
			t.Errorf("got %s\nwant %s", got, tc.out)
		}
		if got := string(text) + "|" + attr; got != tc.text {
			t.Errorf("text %q, want %q", got, tc.text)
		}
	}
}

func TestPrefixedNames(t *testing.T) {
	for _, tc := range []struct {
		mode          Mode
		prefix, local string
		ns            string
	}{
		{HTML, "", "o:p", NamespaceXHTML},
		{Universal, "", "o:p", NamespaceXHTML},
		{XML, "o", "p", "urn:o"},
	} {
		r := NewReader(strings.NewReader(`<html xmlns:o="urn:o"><o:p x:y="1" o:z="2" w="3">`))
		r.SetMode(tc.mode)
		r.Next()
		r.Next()
		tok := r.Token()
		if string(tok.Prefix()) != tc.prefix || string(tok.Local()) != tc.local {
			t.Errorf("%s: prefix %q local %q", tc.mode, tok.Prefix(), tok.Local())
		}
		if tok.NS() != tc.ns {
			t.Errorf("%s: NS %q", tc.mode, tok.NS())
		}
		if a, ok := tok.AttrNS("urn:o", "z"); !ok || a.Value() != "2" {
			t.Errorf("%s: AttrNS %v %v", tc.mode, a, ok)
		}
		if a, ok := tok.AttrNS("", "w"); !ok || a.Value() != "3" {
			t.Errorf("%s: AttrNS unprefixed %v %v", tc.mode, a, ok)
		}
		if !tok.HasAttr("x:y") || tok.HasAttr("y") {
			t.Errorf("%s: HasAttr", tc.mode)
		}
	}
}

func TestNamespaceDecls(t *testing.T) {
	// A name starting with a colon has no prefix, as in XML parsers: the
	// element is in the default namespace, and the attribute in none.
	r := NewReader(strings.NewReader(`<a xmlns:="urn:x" xmlns="u" xmlns="v" xmlns:p="1" xmlns:p="2"><a><p:b/><:c :d="1"/></a></a>`))
	r.SetMode(XML)
	var got []string
	for r.Next() {
		if tok := r.Token(); tok.Kind() == StartTag {
			got = append(got, string(tok.Name())+"="+tok.NS())
			for a := range tok.Attrs() {
				if a.Prefix() == nil && !a.Is("xmlns") {
					got = append(got, string(a.Local())+"="+tok.AttrNamespace(a))
				}
			}
		}
	}
	if want := "a=u a=u p:b=1 :c=u :d="; strings.Join(got, " ") != want {
		t.Errorf("got %q, want %q", strings.Join(got, " "), want)
	}
}

// TestEmptyDefaultNamespace checks that xmlns="" undeclares the default
// namespace under XML rules, and that documents read as HTML in Universal
// mode keep the declarations for LookupPrefix only, including the default
// namespace declaration of HTML 4 in the HTML written by Microsoft Word.
func TestEmptyDefaultNamespace(t *testing.T) {
	const x = NamespaceXHTML
	const h4 = "http://www.w3.org/TR/REC-html40"
	const decl = `<?xml version="1.0"?>`
	var many, decls strings.Builder
	for i := range internal.IndexMin + 1 {
		fmt.Fprintf(&many, ` a%d=""`, i)
		fmt.Fprintf(&decls, ` xmlns:p%d="u"`, i)
	}
	for _, tc := range []struct {
		mode Mode
		in   string
		want string // NS, HTMLRules and the default namespace in scope of each start tag
	}{
		{XML, `<div xmlns=""><p/></div>`, "div=,false, p=,false,"},
		{Universal, decl + `<html xmlns="` + x + `"><div xmlns=""><p/><i xmlns=""/></div></html>`, "html=" + x + ",false," + x + " div=,false, p=,false, i=,false,"},
		{Universal, decl + `<html xmlns="` + x + `"><p xmlns=""` + many.String() + `/></html>`, "html=" + x + ",false," + x + " p=,false,"},
		{XML, `<a` + decls.String() + `><b xmlns="" xmlns="urn:b"></b></a>`, "a=,false,- b=,false,"},
		{Universal, `<div xmlns=""><p/><b xmlns="urn:b"/></div>`, "div=" + x + ",true, p=" + x + ",true, b=" + x + ",true,urn:b"},
		{Universal, `<html xmlns:o="urn:o" xmlns="` + h4 + `"><p>a<br><o:p></o:p></html>`, "html=" + x + ",true," + h4 + " p=" + x + ",true," + h4 + " br=" + x + ",true," + h4 + " o:p=" + x + ",true," + h4},
		{XML, `<p xmlns="` + h4 + `"/>`, "p=" + h4 + ",false," + h4},
	} {
		r := NewReader(strings.NewReader(tc.in))
		r.SetMode(tc.mode)
		var got []string
		for r.Next() {
			tok := r.Token()
			if tok.Kind() != StartTag {
				continue
			}
			uri, ok := r.LookupPrefix("")
			if !ok {
				uri = "-"
			}
			got = append(got, fmt.Sprintf("%s=%s,%v,%s", tok.Name(), tok.NS(), tok.HTMLRules(), uri))
			if tok.Opens() {
				for e := range r.Stack() {
					if e.NS() != tok.NS() || e.HTMLRules() != tok.HTMLRules() {
						t.Errorf("%s %q: element %s: NS %q, HTMLRules %v", tc.mode, tc.in, e.Name(), e.NS(), e.HTMLRules())
					}
					break
				}
			}
		}
		if g := strings.Join(got, " "); g != tc.want {
			t.Errorf("%s %q:\n got %s\nwant %s", tc.mode, tc.in, g, tc.want)
		}
	}
}

func TestHeadWhitespaceRef(t *testing.T) {
	if got := structure(t, `<head>&#32;<title>x</title></head>`, HTML); got != `<head # <title # </title> </head>` {
		t.Errorf("got %s", got)
	}
	if got := structure(t, `<head>&#33;<title>x</title>`, HTML); got != `<head </head?> # <title # </title>` {
		t.Errorf("got %s", got)
	}
	// numeric references longer than the longest named one
	zeros := strings.Repeat("0", 40)
	if got := structure(t, `<head>&#`+zeros+`32;&#x`+zeros+`A<title>x</title></head>`, HTML); got != `<head # <title # </title> </head>` {
		t.Errorf("got %s", got)
	}
	if got := structure(t, `<head>&#`+zeros+`33<title>x</title>`, HTML); got != `<head </head?> # <title # </title>` {
		t.Errorf("got %s", got)
	}
	// A reference whose end is past the maximum token size isn't counted as
	// whitespace, so the text closes the head.
	r := NewReader(strings.NewReader(`<head>&#` + strings.Repeat("0", 100) + `32;<title>x</title></head>`))
	r.SetMode(HTML)
	r.SetMaxTokenSize(80)
	if got := readStructure(t, r); !strings.HasPrefix(got, `<head </head?> #`) {
		t.Errorf("got %s", got)
	}
}

func TestStackBudget(t *testing.T) {
	// The kept start tag and namespace declarations count against the
	// limit, not only the name.
	long := strings.Repeat("x", 200)
	for _, keep := range []bool{false, true} {
		in := `<a k="` + long + `"><b xmlns:p="` + long + `"><c>t</c></b></a>`
		r := NewReader(strings.NewReader(in))
		r.SetMode(XML)
		r.SetKeepAttrs(keep)
		r.SetMaxStackBytes(100)
		var untracked []string
		for r.Next() {
			if tok := r.Token(); tok.Kind() == StartTag && tok.Untracked() {
				untracked = append(untracked, string(tok.Name()))
			}
			if len(r.names)+r.uriBytes > r.maxStack {
				t.Fatalf("keep %v: %d bytes kept (%d of namespace URIs), limit %d", keep, len(r.names), r.uriBytes, r.maxStack)
			}
		}
		want := "b"
		if keep {
			want = "a b"
		}
		if got := strings.Join(untracked, " "); got != want {
			t.Errorf("keep %v: untracked %q, want %q", keep, got, want)
		}
	}

	// Namespace URIs count twice, for their strings: the name (1 byte), the
	// prefix (1) and the URI (9) need 20 bytes.
	for limit, untracked := range map[int]bool{20: false, 19: true} {
		r := NewReader(strings.NewReader(`<a xmlns:p="urn:12345"><p:b/></a>`))
		r.SetMode(XML)
		r.SetMaxStackBytes(limit)
		r.Next()
		if got := r.Token().Untracked(); got != untracked {
			t.Errorf("limit %d: untracked %v, want %v", limit, got, untracked)
		}
		r.Next()
		if got := r.Token().NS(); !untracked && got != "urn:12345" {
			t.Errorf("limit %d: namespace %q", limit, got)
		}
		r.Next()
		r.Next()
		if r.uriBytes != 0 || len(r.decls) != 0 {
			t.Errorf("limit %d: %d declarations (%d bytes) after the end", limit, len(r.decls), r.uriBytes)
		}
	}
}

// readNS returns the namespaces of tok, its attributes and the elements
// (with their attributes), and the one bound to prefix. It isn't inlined, so
// the test doesn't depend on its caller.
//
//go:noinline
func readNS(r *Reader, tok *Token, elems []Element, prefix string) (n int) {
	n += len(tok.NS())
	for i := range tok.NumAttrs() {
		n += len(tok.AttrNamespace(tok.AttrAt(i)))
	}
	for _, e := range elems {
		n += len(e.NS())
		for i := range e.NumAttrs() {
			n += len(e.AttrNamespace(e.AttrAt(i)))
		}
	}
	uri, _ := r.LookupPrefix(prefix)
	return n + len(uri)
}

func TestNSAllocs(t *testing.T) {
	// The namespace strings are made once for each declaration, on the
	// stack or on the current start tag.
	const in = `<f:feed xmlns:f="urn:feed" xmlns="urn:d&#x2d;1" f:a="1">` +
		`<entry xmlns:x="urn:x" x:b="2"><f:item f:c="3"/><y:z xmlns:y="urn:y" y:d="4" x:e="5"/><g/></entry></f:feed>`
	for _, mode := range []Mode{XML} {
		r := NewReader(strings.NewReader(in))
		r.SetMode(mode)
		r.SetKeepAttrs(true)
		var got []string
		for r.Next() {
			tok := r.Token()
			if tok.Kind() != StartTag && tok.Kind() != EndTag {
				continue
			}
			var elems []Element
			for e := range r.Stack() {
				elems = append(elems, e)
			}
			for _, prefix := range []string{"f", "x", "y", ""} {
				if allocs := testing.AllocsPerRun(100, func() { readNS(r, tok, elems, prefix) }); allocs != 0 {
					t.Errorf("%v: %s, prefix %q: %v allocations", mode, tok, prefix, allocs)
				}
			}
			s := string(tok.Name()) + "=" + tok.NS()
			for a := range tok.Attrs() {
				s += " " + string(a.Name()) + "=" + tok.AttrNamespace(a)
			}
			got = append(got, s)
		}
		want := []string{
			"f:feed=urn:feed xmlns:f=http://www.w3.org/2000/xmlns/ xmlns=http://www.w3.org/2000/xmlns/ f:a=urn:feed",
			"entry=urn:d-1 xmlns:x=http://www.w3.org/2000/xmlns/ x:b=urn:x",
			"f:item=urn:feed f:c=urn:feed",
			"y:z=urn:y xmlns:y=http://www.w3.org/2000/xmlns/ y:d=urn:y x:e=urn:x",
			"g=urn:d-1",
			"entry=urn:d-1",
			"f:feed=urn:feed",
		}
		if !slices.Equal(got, want) {
			t.Errorf("%v: got\n%s\nwant\n%s", mode, strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	}
}

func TestNSCopies(t *testing.T) {
	// Copies keep the namespaces in scope when they were made after the
	// Reader pops the declarations and reuses their memory for others.
	const in = `<a xmlns="urn:a1" xmlns:p="urn:p1"><b p:x="1"/><c xmlns:q="urn:q1" q:y="2"/></a>` +
		`<a xmlns="urn:a2" xmlns:p="urn:p2"><b p:x="1"/><c xmlns:q="urn:q2" q:y="2"/></a>`
	for _, mode := range []Mode{XML} {
		r := NewReader(strings.NewReader(in))
		r.SetMode(mode)
		var buf TokenBuffer
		var clones []*Token
		var want, strs []string
		for r.Next() {
			tok := r.Token()
			buf.Append(tok)
			clones = append(clones, tok.Clone())
			d := describe(tok)
			want = append(want, d)
			strs = append(strs, tok.NS())
			for a := range tok.Attrs() {
				strs = append(strs, tok.AttrNamespace(a))
			}
			// read them again from the cache
			if d2 := describe(tok); d2 != d {
				t.Errorf("%v: described again:\n got %s\nwant %s", mode, d2, d)
			}
		}
		for i := range buf.Len() {
			if got := describe(buf.At(i)); got != want[i] {
				t.Errorf("%v: buffer token %d:\n got %s\nwant %s", mode, i, got, want[i])
			}
			if got := describe(clones[i]); got != want[i] {
				t.Errorf("%v: clone %d:\n got %s\nwant %s", mode, i, got, want[i])
			}
		}
		// strings returned before the declarations were popped
		if got, want := strings.Join(strs, " "), "urn:a1 http://www.w3.org/2000/xmlns/ http://www.w3.org/2000/xmlns/ "+
			"urn:a1 urn:p1 "+
			"urn:a1 http://www.w3.org/2000/xmlns/ urn:q1 "+
			"urn:a1 "+
			"urn:a2 http://www.w3.org/2000/xmlns/ http://www.w3.org/2000/xmlns/ "+
			"urn:a2 urn:p2 "+
			"urn:a2 http://www.w3.org/2000/xmlns/ urn:q2 "+
			"urn:a2"; got != want {
			t.Errorf("%v: namespaces\n got %q\nwant %q", mode, got, want)
		}
		if len(r.decls) != 0 || r.uriBytes != 0 || len(r.selfNS) != 0 {
			t.Errorf("%v: declarations left: %d, %d bytes, %d on the start tag", mode, len(r.decls), r.uriBytes, len(r.selfNS))
		}
		for _, d := range r.decls[:cap(r.decls)] {
			if d.str != "" {
				t.Errorf("%v: popped declaration keeps %q", mode, d.str)
			}
		}
	}
}

// nsDump describes the namespaces of each tag in in, its attributes and the
// open elements, and those of some prefixes, reading tokens and copies.
// Attributes named z* are left out.
func nsDump(in string, mode Mode) []string {
	r := NewReader(strings.NewReader(in))
	r.SetMode(mode)
	var got []string
	for r.Next() {
		tok := r.Token()
		if tok.Kind() != StartTag && tok.Kind() != EndTag {
			continue
		}
		c := tok.Clone()
		s := fmt.Sprintf("%s=%s/%s", tok.Name(), tok.NS(), c.NS())
		for a := range tok.Attrs() {
			if n := a.Name(); len(n) == 0 || n[0] != 'z' && !bytes.HasPrefix(n, []byte("xmlns:z")) {
				s += fmt.Sprintf(" %s=%s/%s", n, tok.AttrNamespace(a), c.AttrNamespace(a))
			}
		}
		s += " |"
		for e := range r.Stack() {
			s += " " + string(e.Name()) + "=" + e.NS()
		}
		s += " |"
		for _, p := range []string{"", "p", "P", "q", "p\x00", "p\uFFFD"} {
			uri, ok := r.LookupPrefix(p)
			s += fmt.Sprintf(" %q=%s/%v", p, uri, ok)
		}
		got = append(got, s)
	}
	return got
}

func TestNSIndex(t *testing.T) {
	// With many declarations or attributes, the declarations are looked up
	// in a map, with the same results.
	for _, in := range []string{
		`<f:feed xmlns:f="urn:feed" xmlns="urn:d&#x2d;1" f:a="1">` +
			`<entry xmlns:x="urn:x" x:b="2"><f:item f:c="3"/><y:z xmlns:y="urn:y" y:d="4" x:e="5"/><g/></entry></f:feed>`,
		`<a xmlns:p="urn:1" xmlns:P="urn:2" xmlns:p="urn:3" xmlns="urn:d">` +
			`<b xmlns:p="urn:4" p:x="" P:y="" q:w=""><c p:z="" P:w="" xmlns:q="urn:q" q:v=""/><p:e/></b>` +
			`<d p:v="" P:u="" xmlns="urn:e" xmlns:p="urn:8" xmlns:p="urn:9"/><p:f xmlns:p="urn:5"><p:g xmlns:p="urn:6" xmlns="urn:7"><h/></p:g><p:i/></p:f><P:h/></a>`,
		"<a xmlns:p\x00=\"urn:nul\"><b p\uFFFD:x=\"\" p\x00:y=\"\"/><p\x00:c/></a>",
	} {
		// filler declarations on each start tag, for more than
		// internal.IndexMin attributes and declarations
		var fill strings.Builder
		for i := range 20 {
			fmt.Fprintf(&fill, ` xmlns:z%d="urn:z%d"`, i, i)
		}
		padded := strings.NewReplacer("<a", "<a"+fill.String(), "<b", "<b"+fill.String(), "<c", "<c"+fill.String(), "<d", "<d"+fill.String(), "<y:z", "<y:z"+fill.String(), "<f:item", "<f:item"+fill.String()).Replace(in)
		for _, mode := range modes {
			want := nsDump(in, mode)
			got := nsDump(padded, mode)
			if !slices.Equal(got, want) {
				t.Errorf("%v: %q:\n got %s\nwant %s", mode, in, strings.Join(got, "\n    "), strings.Join(want, "\n    "))
			}
		}
	}
}

func TestManyDecls(t *testing.T) {
	// Reading a tag takes time linear in its attributes and namespace
	// declarations, and so does resolving their namespaces.
	const n = 100000
	var b strings.Builder
	b.WriteString("<r")
	for i := range n {
		fmt.Fprintf(&b, " xmlns:p%d='u%d'", i, i)
	}
	for i := range n {
		fmt.Fprintf(&b, " p%d:a=''", i)
	}
	b.WriteString(" xmlns:p0='dup'")
	tag := b.String()
	for _, mode := range modes {
		for _, in := range []string{tag + "></r>", tag + "/>", "<r xmlns:p1='x'><s>" + tag[2:] + "></s></r>"} {
			r := NewReader(strings.NewReader(in))
			r.SetMode(mode)
			r.SetXMLSyntax(true)
			for r.Next() {
				tok := r.Token()
				if tok.Kind() != StartTag || tok.NumAttrs() < n {
					continue
				}
				c := tok.Clone()
				for i := range n {
					a := tok.AttrAt(n + i)
					want := "u" + strconv.Itoa(i)
					if got := tok.AttrNamespace(a); got != want {
						t.Fatalf("%v: %s: namespace %q, want %q", mode, a.Name(), got, want)
					}
					if got := c.AttrNamespace(a); got != want {
						t.Fatalf("%v: copy: %s: namespace %q, want %q", mode, a.Name(), got, want)
					}
				}
				if uri, _ := r.LookupPrefix("p1"); uri != "u1" {
					t.Errorf("%v: p1 bound to %q", mode, uri)
				}
			}
			if r.Err() != nil {
				t.Errorf("%v: %v", mode, r.Err())
			}
		}
	}
}

func TestHeadLeadingWhitespace(t *testing.T) {
	// Leading whitespace stays in the head, as in HTML parsers.
	for _, tc := range []struct{ in, out string }{
		{"<head> 0", `<head # </head?> #`},
		{"<head> \r\n&#32;&Tab;&#x20 x", `<head # </head?> #`},
		{"<head>x ", `<head </head?> #`},
		{"<head>&#000000000000000000000000000000000032; x", `<head # </head?> #`},
		{"<head>  <title>", `<head # <title </title?> </head?>`},
		{"<head> < x", `<head # </head?> #`},
	} {
		r := NewReader(strings.NewReader(tc.in))
		var b []string
		for r.Next() {
			switch tok := r.Token(); tok.Kind() {
			case StartTag:
				b = append(b, "<"+string(tok.Name()))
			case EndTag:
				b = append(b, "</"+string(tok.Name())+"?>")
			case Text:
				b = append(b, "#")
			}
		}
		if got := strings.Join(b, " "); got != tc.out {
			t.Errorf("%q: got %s, want %s", tc.in, got, tc.out)
		}
	}
}

func TestLocalIs(t *testing.T) {
	r := NewReader(strings.NewReader(`<svg:RECT/>`))
	r.SetMode(XML)
	r.Next()
	if !r.Token().LocalIs("RECT") || r.Token().LocalIs("rect") {
		t.Errorf("LocalIs on prefixed name in XML mode")
	}
	r = NewReader(strings.NewReader(`<svg:RECT/><DIV>`))
	r.Next()
	if !r.Token().LocalIs("svg:rect") || r.Token().LocalIs("rect") {
		t.Errorf("LocalIs on prefixed name in HTML mode")
	}
	r.Next()
	if !r.Token().LocalIs("div") {
		t.Errorf("LocalIs on HTML name")
	}

	// In HTML mode, names of foreign elements are case-insensitive too.
	r = NewReader(strings.NewReader(`<svg><foreignobject><DIV>`))
	r.Next()
	r.Next()
	if tok := r.Token(); !tok.Is("foreignObject") || !tok.LocalIs("foreignObject") || !TagNS(NamespaceSVG, "foreignObject")(tok) {
		t.Errorf("mixed case name in HTML mode")
	}
	r.Next()
	found := false
	for e := range r.Stack() {
		found = found || e.Is("FOREIGNOBJECT")
	}
	if !r.Token().Is("Div") || !found {
		t.Errorf("mixed case name in HTML mode")
	}
}

// TestNULNames checks names with NUL: under HTML rules, they are read like
// the HTML tokenizer reads them, with NUL replaced with U+FFFD, and under XML
// rules as written.
func TestNULNames(t *testing.T) {
	for _, mode := range []Mode{HTML, XML} {
		html := mode == HTML
		r := NewReader(strings.NewReader("<A\x00b C\x00d=1>"))
		r.SetMode(mode)
		r.SetKeepAttrs(true)
		r.Next()
		tok := r.Token()
		check := func(what string, got, want bool) {
			t.Helper()
			if got != want {
				t.Errorf("%s: %s is %v, want %v", mode, what, got, want)
			}
		}
		check("Name", string(tok.Name()) == "A\x00b", true)
		lname := "A\x00b"
		if html {
			lname = "a\uFFFDb"
		}
		if got := string(tok.AppendLocalName(nil)); got != lname {
			t.Errorf("%s: AppendLocalName is %q, want %q", mode, got, lname)
		}
		check(`Is("a\uFFFDb")`, tok.Is("a\uFFFDb"), html)
		check(`Is("A\uFFFDB")`, tok.Is("A\uFFFDB"), html)
		check(`Is("A\x00b")`, tok.Is("A\x00b"), !html)
		check(`Is("a\x00b")`, tok.Is("a\x00b"), false)
		check(`Is("a\uFFFD")`, tok.Is("a\uFFFD"), false)
		check(`Is("ab")`, tok.Is("ab"), false)
		check(`LocalIs("a\uFFFDb")`, tok.LocalIs("a\uFFFDb"), html)
		check(`Tag("a\uFFFDb")`, Tag("a\uFFFDb")(tok), html)
		for e := range r.Stack() {
			check(`Element.Is("a\uFFFDb")`, e.Is("a\uFFFDb"), html)
			_, ok := e.Attr("c\uFFFDd")
			check(`Element.Attr("c\uFFFDd")`, ok, html)
			break
		}
		a, _ := tok.Attr("C\x00d")
		if html {
			a, _ = tok.Attr("c\uFFFDd")
		}
		check("Attr.Name", string(a.Name()) == "C\x00d", true)
		aname := "C\x00d"
		if html {
			aname = "c\uFFFDd"
		}
		if got := string(a.AppendName(nil)); got != aname {
			t.Errorf("%s: AppendName is %q, want %q", mode, got, aname)
		}
		check(`Attr.Is("C\uFFFDD")`, a.Is("C\uFFFDD"), html)
		check(`Attr.Is("C\x00d")`, a.Is("C\x00d"), !html)
		check(`HasAttr("c\uFFFDd")`, tok.HasAttr("c\uFFFDd"), html)
		_, ok := tok.AttrNS("", "c\uFFFDd")
		check(`AttrNS("", "c\uFFFDd")`, ok, html)
	}

	// End tags close elements with the same name as HTML parsers read it.
	for _, tc := range []struct {
		mode    Mode
		in, out string
	}{
		{HTML, "<a\x00>x</A\uFFFD>y", "<a\x00 # </A\uFFFD> #"},
		{HTML, "<a\uFFFD>x</a\x00>y", "<a\uFFFD # </a\x00> #"},
		{HTML, "<a\x00\uFFFD>x</a\uFFFD\x00>y", "<a\x00\uFFFD # </a\uFFFD\x00> #"},
		{HTML, "<a\x00>x</a>y", "<a\x00 # </a!> # </a\x00?>"},
		{XML, "<a\x00>x</a\uFFFD>y", "<a\x00 # </a\uFFFD!> # </a\x00?>"},
	} {
		if got := structure(t, tc.in, tc.mode); got != tc.out {
			t.Errorf("%s %q:\n got %q\nwant %q", tc.mode, tc.in, got, tc.out)
		}
	}
}

// TestUntracked checks elements past the stack limits: their end tags, raw
// text, and the end tags of the tracked elements around them.
func TestUntracked(t *testing.T) {
	// tokens describes the tokens like structure, with the depth after each
	// start tag and the start position of the element each end tag closes.
	tokens := func(r *Reader) string {
		var b []string
		for r.Next() {
			tok := r.Token()
			switch tok.Kind() {
			case StartTag:
				s := "<" + string(tok.Name())
				if tok.Untracked() {
					s += "*"
				}
				b = append(b, s+strconv.Itoa(r.Depth()))
			case EndTag:
				s := "</" + string(tok.Name())
				switch {
				case tok.Implied():
					s += "?"
				case tok.Stray():
					s += "!"
				}
				b = append(b, s+strconv.FormatInt(int64(tok.StartPos()), 10))
			case Text:
				b = append(b, "#"+string(tok.Data()))
			}
		}
		if err := r.Err(); err != nil {
			t.Fatal(err)
		}
		return strings.Join(b, " ")
	}
	long := strings.Repeat("x", 100)
	for _, tc := range []struct {
		in       string
		depth    int // SetMaxDepth
		maxStack int // stack byte budget, if not zero
		out      string
	}{
		// end tags of untracked elements (closed by the next end tag whatever
		// its name), balanced by nameless implied end tags at EOF
		{`<a><b></b>x</a>y`, 1, 0, `<a1 <b*2 </b-1 #x </a0 #y`},
		{`<a><b>x`, 1, 0, `<a1 <b*2 #x </?-1 </a?0`},
		{`<a><b><c>x</b>`, 1, 0, `<a1 <b*2 <c*3 #x </b-1 </?-1 </a?0`},
		// untracked raw text elements
		{`<div><script>a<b</script></div>`, 1, 0, `<div1 <script*2 #a<b </script-1 </div0`},
		{`<div><title>a<b</title></div>`, 1, 0, `<div1 <title*2 #a<b </title-1 </div0`},
		// an untracked element below tracked ones (stack byte budget) is closed
		// by its own end tag, and the end tags of the elements above it are
		// matched normally
		{`<div xmlns:a="` + long + `"><script>a<b</script></div>`, 10, 50, `<div*1 <script2 #a<b </script116 </div-1`},
		{`<div xmlns:a="` + long + `"><b>x</b></div>y`, 10, 50, `<div*1 <b2 #x </b116 </div-1 #y`},
		// an end tag closing a tracked element through untracked ones
		{`<a><b xmlns:p="` + long + `"><c>x</a>y`, 10, 50, `<a1 <b*2 <c3 #x </c?117 </?-1 </a0 #y`},
		// implied end tags for a start tag close the untracked elements above
		{`<ul><li><b xmlns:p="` + long + `"><i>a<li>b`, 10, 50, `<ul1 <li2 <b*3 <i4 #a </i?122 </?-1 </li?4 <li2 #b </li?126 </ul?0`},
		// html, head and body are misplaced (not opened) inside untracked
		// elements too
		{`<a><b><html><body>x`, 1, 0, `<a1 <b*2 <html3 <body3 #x </?-1 </a?0`},
	} {
		r := NewReader(strings.NewReader(tc.in))
		r.SetMaxDepth(tc.depth)
		if tc.maxStack != 0 {
			r.SetMaxStackBytes(tc.maxStack)
		}
		if got := tokens(r); got != tc.out {
			t.Errorf("%q (depth %d, stack %d):\n got %s\nwant %s", tc.in, tc.depth, tc.maxStack, got, tc.out)
		}
	}

	// Skip stops at the end tag of the element, not of an untracked descendant.
	r := NewReader(strings.NewReader(`<a><b></b>x</a>y`))
	r.SetMaxDepth(1)
	r.Next()
	if r.Skip(); r.Err() != nil || !r.Token().Is("a") || r.Token().StartPos() != 0 {
		t.Errorf("Skip: %v, stopped at %v", r.Err(), r.Token())
	}
	r = NewReader(strings.NewReader(`<div xmlns:a="` + long + `"><b>x</b></div>`))
	r.SetMaxStackBytes(50)
	r.Next()
	r.Next()
	if r.Skip(); r.Err() != nil || !r.Token().Is("b") || r.Token().Implied() {
		t.Errorf("Skip above an untracked element: %v, stopped at %v", r.Err(), r.Token())
	}
	// Skip and Children on untracked elements stop at the end tag at the
	// same depth.
	r = NewReader(strings.NewReader(`<a><b><c>x</c>y</b>z</a>w`))
	r.SetMaxDepth(1)
	r.Next()
	r.Next()
	if r.Skip(); r.Err() != nil || r.Token().Kind() != EndTag || string(r.Token().Raw()) != "</b>" {
		t.Errorf("Skip on an untracked element: %v, stopped at %v", r.Err(), r.Token())
	}
	r.Next()
	if tok := r.Token(); tok.Kind() != Text || string(tok.Raw()) != "z" {
		t.Errorf("after Skip: %v", tok)
	}
	r = NewReader(strings.NewReader(`<a><b><c>x</c>y</b>z</a>w`))
	r.SetMaxDepth(1)
	r.Next()
	r.Next()
	var raw []string
	for tok := range r.Children() {
		raw = append(raw, string(tok.Raw()))
	}
	if got := strings.Join(raw, " "); got != "<c> x </c> y" || string(r.Token().Raw()) != "</b>" || r.Err() != nil {
		t.Errorf("Children of an untracked element: %s, stopped at %v, %v", got, r.Token(), r.Err())
	}
	// In strict mode, an untracked element closed without an end tag is an
	// error for an operation depending on its end, since whether its end tag
	// may be omitted isn't known.
	for _, tc := range []struct {
		in  string
		err bool
	}{
		{`<a><b>x</b>`, false},
		{`<a><b>x</a>`, false}, // closes b, whatever its name
		{`<a><b>x`, true},
		{`<a><b><c>x</a>`, true},
	} {
		r = NewReader(strings.NewReader(tc.in))
		r.SetMaxDepth(1)
		r.SetStrict(true)
		r.Next()
		r.Next()
		r.Skip()
		err := r.Err()
		var se *SyntaxError
		if tc.err != errors.As(err, &se) {
			t.Errorf("%q: strict Skip on an untracked element: %v", tc.in, err)
		}
	}
	// A void element under XML rules which can't be tracked is closed like a
	// tracked one: by its end tag if it is the next token, and otherwise by
	// an implied end tag which isn't unsound, also in an untracked element.
	for _, tc := range []struct {
		in              string
		depth, maxStack int
		out             string
	}{
		{`<p><br>x<br/><br></br><BR></br></p>`, 2, 0, `<html1 <p2 <br*3 </?-1 #x <br3 <br*3 </br-1 <BR*3 </br-1 </p64 </html21`},
		{`<p><b><br></br>x</b></p>`, 2, 0, `<html1 <p2 <b*3 <br*4 </br-1 #x </b-1 </p64 </html21`},
		{`<p><br a="` + long + `"></br><br a="` + long + `">x</p>`, 10, 150, `<html1 <p2 <br*3 </br-1 <br*3 </?-1 #x </p64 </html21`},
		{`<p><br>`, 2, 0, `<html1 <p2 <br*3 </?-1 </p?64 </html21`},
	} {
		r = NewReader(strings.NewReader(`<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml">` + tc.in + `</html>`))
		r.SetMode(Universal)
		r.SetKeepAttrs(true)
		r.SetMaxDepth(tc.depth)
		if tc.maxStack != 0 {
			r.SetMaxStackBytes(tc.maxStack)
		}
		r.SetStrict(true)
		if got := tokens(r); got != tc.out {
			t.Errorf("untracked void element under XML rules %q:\n got %s\nwant %s", tc.in, got, tc.out)
		}
	}
	r = NewReader(strings.NewReader(`<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml"><p><br>x</p></html>`))
	r.SetMode(Universal)
	r.SetMaxDepth(2)
	r.SetStrict(true)
	for r.Next() && !r.Token().Is("br") {
	}
	if r.Skip(); r.Err() != nil || !r.Token().Implied() {
		t.Errorf("strict Skip on an untracked void element: %v, stopped at %v", r.Err(), r.Token())
	}
	// Depth counts the untracked elements, and drops after their end tags.
	// A void sibling of an untracked element has the same depth.
	r = NewReader(strings.NewReader(`<a><b></b><br></a>`))
	r.SetMaxDepth(1)
	var depths []int
	for r.Next() {
		depths = append(depths, r.Depth())
	}
	if want := []int{1, 2, 2, 2, 1}; fmt.Sprint(depths) != fmt.Sprint(want) {
		t.Errorf("depths %v, want %v", depths, want)
	}
}

func TestImpliedHeadWhitespace(t *testing.T) {
	// Leading whitespace of text where HTML parsers imply the head is a
	// token of its own whatever the chunk size, like in an explicit head, so
	// consumers can keep it in the head. Text split at the chunk size is
	// merged here, and spaces are shown as underscores.
	for _, tc := range []struct{ in, out string }{
		{`<title>x</title>  abc`, `<title #x </title> #__ #abc`},
		{`<html>  abc`, `<html #__ #abc </html?>`},
		{`<html><head></head>  abc`, `<html <head </head> #__ #abc </html?>`},
		{`<p>x</p>  abc`, `<p #x </p> #__ #abc`},
		{`<p>  abc`, `<p #__abc </p?>`},
		{`<title>x</title>abc`, `<title #x </title> #abc`},
	} {
		for _, chunk := range []int{0, 1, 3} {
			r := NewReader(strings.NewReader(tc.in))
			if chunk != 0 {
				r.SetChunkSize(chunk)
			}
			var b []string
			for r.Next() {
				switch tok := r.Token(); tok.Kind() {
				case StartTag:
					b = append(b, "<"+string(tok.Name()))
				case EndTag:
					if tok.Implied() {
						b = append(b, "</"+string(tok.Name())+"?>")
					} else {
						b = append(b, "</"+string(tok.Name())+">")
					}
				case Text:
					d := strings.ReplaceAll(string(tok.Data()), " ", "_")
					if tok.Continued() {
						b[len(b)-1] += d
					} else {
						b = append(b, "#"+d)
					}
				}
			}
			if got := strings.Join(b, " "); got != tc.out {
				t.Errorf("%q (chunk %d): got %s, want %s", tc.in, chunk, got, tc.out)
			}
		}
	}
}

// TestStrictSoundImplied checks Skip in strict mode at elements closed by a
// sound implied end tag: plaintext at the end of the input, and void
// elements under XML rules.
func TestStrictSoundImplied(t *testing.T) {
	for _, tc := range []struct {
		mode Mode
		in   string
	}{
		{HTML, `<plaintext>abc`},
		{HTML, `<br>x`},
		{Universal, `<?xml version="1.0"?><br xmlns="http://www.w3.org/1999/xhtml">x`},
		{Universal, `<?xml version="1.0"?><br xmlns="http://www.w3.org/1999/xhtml">`},
	} {
		r := NewReader(strings.NewReader(tc.in))
		r.SetMode(tc.mode)
		r.SetXMLSyntax(true)
		r.SetStrict(true)
		for r.Next() && r.Token().Kind() != StartTag {
		}
		if r.Skip(); r.Err() != nil || !r.Token().Implied() || r.Token().Unsound() {
			t.Errorf("%s %q: Skip: %v, stopped at %v", tc.mode, tc.in, r.Err(), r.Token())
		}
	}
}

func TestAttrNSCase(t *testing.T) {
	r := NewReader(strings.NewReader(`<svg><use XLINK:HREF="#a" XMLNS:X="u"/></svg>`))
	r.Next()
	r.Next()
	tok := r.Token()
	var got []string
	for a := range tok.Attrs() {
		got = append(got, string(a.Name())+"="+tok.AttrNamespace(a))
	}
	want := []string{"XLINK:HREF=" + NamespaceXLink, "XMLNS:X=" + NamespaceXMLNS}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("AttrNamespace: got %q, want %q", got, want)
	}
	if a, ok := tok.AttrNS(NamespaceXLink, "href"); !ok || a.Value() != "#a" {
		t.Errorf("AttrNS: %v %v", a, ok)
	}
	if !HasAttrNS(NamespaceXLink, "href")(tok) || !AttrNSFunc(NamespaceXLink, "HREF", func(v []byte) bool { return string(v) == "#a" })(tok) || !AttrNSEquals(NamespaceXLink, "HREF", "#a")(tok) || AttrNSEquals(NamespaceXLink, "href", "#b")(tok) {
		t.Errorf("HasAttrNS/AttrNSFunc/AttrNSEquals")
	}
	// exact in XML mode
	r = NewReader(strings.NewReader(`<svg xmlns:xlink="` + NamespaceXLink + `"><use xlink:HREF="#a"/></svg>`))
	r.SetMode(XML)
	r.Next()
	r.Next()
	if _, ok := r.Token().AttrNS(NamespaceXLink, "href"); ok {
		t.Errorf("AttrNS folded in XML mode")
	}
	if AttrNSEquals(NamespaceXLink, "href", "#a")(r.Token()) || !AttrNSEquals(NamespaceXLink, "HREF", "#a")(r.Token()) {
		t.Errorf("AttrNSEquals folded in XML mode")
	}
	// by namespace, not prefix
	r = NewReader(strings.NewReader(`<p xmlns:o="urn:o" xmlns:x="urn:x" o:role="a" x:role="b"/>`))
	r.SetMode(XML)
	r.Next()
	if tok := r.Token(); !AttrNSEquals("urn:o", "role", "a")(tok) || AttrNSEquals("urn:o", "role", "b")(tok) || !AttrNSEquals("urn:x", "role", "b")(tok) {
		t.Errorf("AttrNSEquals by namespace")
	}
}

func TestAttrNSAdjusted(t *testing.T) {
	// HTML parsers put xlink:href and the other xlink attributes of the
	// "adjust foreign attributes" table in the XLink namespace on SVG and
	// MathML elements whatever the declarations say. Other prefixes are
	// resolved from the declarations.
	const svgDecl = `xmlns="` + NamespaceSVG + `" `
	for _, tc := range []struct {
		mode  Mode
		in    string
		want  string // AttrNamespace of each attribute of the second start tag
		xlink bool   // AttrNS finds an XLink href
	}{
		{HTML, `<svg xmlns:xlink="u"><image xlink:href="a" xlink:foo="b" xlink:TITLE="t" XLINK:Role="r"/></svg>`, "xlink:href=X xlink:foo=u xlink:TITLE=X XLINK:Role=X", true},
		{HTML, `<svg><image XMLNS:XLINK="u" xlink:href="a" xlink:arcrole="b"/></svg>`, "XMLNS:XLINK=" + NamespaceXMLNS + " xlink:href=X xlink:arcrole=X", true},
		{HTML, `<svg xmlns:xlink=""><use xlink:href="#a" xlink:actuate="b" xlink:show="c" xlink:type="d"/></svg>`, "xlink:href=X xlink:actuate=X xlink:show=X xlink:type=X", true},
		{HTML, `<math xmlns:xlink="u"><mi xlink:href="a"/></math>`, "xlink:href=X", true},
		{HTML, `<svg><g xlink:foo="1" xml:lang="en" xml:space="preserve"/></svg>`, "xlink:foo= xml:lang=" + NamespaceXML + " xml:space=" + NamespaceXML, false},
		{HTML, `<svg xmlns:x="` + NamespaceXLink + `"><image x:href="a"/></svg>`, "x:href=X", true},
		// HTML elements: declarations only
		{HTML, `<div xmlns:xlink="u"><p xlink:href="a"></div>`, "xlink:href=u", false},
		{HTML, `<div><p xlink:href="a"></div>`, "xlink:href=", false},
		{XML, `<svg xmlns:xlink="u"><image xlink:href="a"/></svg>`, "xlink:href=u", false},
		{XML, `<svg><image xlink:href="a"/></svg>`, "xlink:href=", false},
		{XML, `<svg xmlns:xlink="` + NamespaceXLink + `"><image xlink:href="a"/></svg>`, "xlink:href=X", true},
		// Universal mode: like HTML mode in documents read as HTML, also
		// with a namespace declaration on svg
		{Universal, `<svg xmlns:xlink="u"><image xlink:href="a" xlink:foo="b"/></svg>`, "xlink:href=X xlink:foo=u", true},
		{Universal, `<svg><image xlink:href="a" XLINK:HREF="b" XML:LANG="en"/></svg>`, "xlink:href=X XLINK:HREF=X XML:LANG=" + NamespaceXML, true},
		{Universal, `<svg ` + svgDecl + `xmlns:xlink="u"><image xlink:href="a"/></svg>`, "xlink:href=X", true},
	} {
		r := NewReader(strings.NewReader(tc.in))
		r.SetMode(tc.mode)
		r.Next()
		r.Next()
		tok := r.Token()
		var got []string
		for a := range tok.Attrs() {
			ns := tok.AttrNamespace(a)
			if ns == NamespaceXLink {
				ns = "X"
			}
			got = append(got, string(a.Name())+"="+ns)
		}
		if g := strings.Join(got, " "); g != tc.want {
			t.Errorf("%s %q: got %q, want %q", tc.mode, tc.in, g, tc.want)
		}
		_, ok := tok.AttrNS(NamespaceXLink, "href")
		if ok != tc.xlink || HasAttrNS(NamespaceXLink, "href")(tok) != tc.xlink {
			t.Errorf("%s %q: AttrNS/HasAttrNS XLink href %v, want %v", tc.mode, tc.in, ok, tc.xlink)
		}
	}
}

func TestDeclarationCase(t *testing.T) {
	// In HTML mode, declarations and prefixes are case-insensitive like
	// attribute names; in XML mode they are exact.
	for _, tc := range []struct {
		mode Mode
		in   string
		want string // AttrNamespace of each attribute of the inner tag, then the declared prefixes "x" and ""
	}{
		{HTML, `<svg XMLNS:X="u" XMLNS="d"><g X:a="1" x:b="2"/></svg>`, "X:a=u x:b=u x=u =d"},
		{HTML, `<svg xmlns:x="u"><g X:a="1"/></svg>`, "X:a=u x=u"},
		{HTML, `<svg><g XMLNS:X="u" x:a="1"/></svg>`, "XMLNS:X=" + NamespaceXMLNS + " x:a=u x=u"},
		{XML, `<svg XMLNS:X="u"><g X:a="1" x:b="2"/></svg>`, "X:a= x:b="},
		{XML, `<svg xmlns:X="u" XMLNS="d"><g X:a="1" x:b="2"/></svg>`, "X:a=u x:b="},
	} {
		r := NewReader(strings.NewReader(tc.in))
		r.SetMode(tc.mode)
		r.Next()
		r.Next()
		tok := r.Token()
		var got []string
		for a := range tok.Attrs() {
			got = append(got, string(a.Name())+"="+tok.AttrNamespace(a))
		}
		for _, p := range []string{"x", ""} {
			if uri, ok := r.LookupPrefix(p); ok {
				got = append(got, p+"="+uri)
			}
		}
		if g := strings.Join(got, " "); g != tc.want {
			t.Errorf("%s %q: got %q, want %q", tc.mode, tc.in, g, tc.want)
		}
	}
}

// TestTokenBuffer checks that copies of tokens (Token.Clone and TokenBuffer)
// give the same results as the originals, after the Reader advanced.
func TestTokenBuffer(t *testing.T) {
	const in = `<!DOCTYPE html><html xmlns:o="urn:o" xmlns:e="urn:e"><head><title>t&amp;</title></head><body><p class="a b" id=x e:type="c">text &lt; more<o:p>&nbsp;</o:p><br/><!--c--><![CDATA[d]]><svg xlink:href="h" o:x="1"><rect/></svg><p>unclosed`
	for _, mode := range []Mode{HTML, Universal, XML} {
		r := NewReader(strings.NewReader(in))
		r.SetMode(mode)
		r.SetXMLSyntax(true)
		var want []string
		var buf TokenBuffer
		var clones []*Token
		for r.Next() {
			tok := r.Token()
			want = append(want, describe(tok))
			buf.Append(tok)
			clones = append(clones, tok.Clone())
			if len(clones)%5 == 0 {
				// a copy of a copy
				clones[len(clones)-1] = clones[len(clones)-1].Clone()
			}
		}
		if r.Err() != nil {
			t.Fatal(r.Err())
		}
		if buf.Len() != len(want) {
			t.Fatalf("%v: %d tokens in buffer, want %d", mode, buf.Len(), len(want))
		}
		i := 0
		for tok := range buf.All() {
			if got := describe(tok); got != want[i] {
				t.Errorf("%v: buffer token %d:\n got %s\nwant %s", mode, i, got, want[i])
			}
			if got := describe(clones[i]); got != want[i] {
				t.Errorf("%v: clone %d:\n got %s\nwant %s", mode, i, got, want[i])
			}
			i++
		}
		if !strings.Contains(strings.Join(want, "\n"), `ns="urn:e"`) {
			t.Errorf("%v: no attribute in the e namespace among %q", mode, want)
		}
		buf.Reset()
		if buf.Len() != 0 || buf.Size() != 0 {
			t.Errorf("%v: buffer not empty after Reset", mode)
		}
	}

	// Appending to a buffer allocates only when it grows: after a reset,
	// the same tokens are appended without allocating.
	const doc = `<p class="x" id="y">a<o:p> </o:p></p>`
	var buf TokenBuffer
	read := func(appendTokens bool) {
		buf.Reset()
		r := NewReader(strings.NewReader(strings.Repeat(doc, 50)))
		for r.Next() {
			if appendTokens {
				buf.Append(r.Token())
			}
		}
	}
	read(true)
	n := buf.Len()
	base := testing.AllocsPerRun(5, func() { read(false) })
	if allocs := testing.AllocsPerRun(5, func() { read(true) }); allocs > base {
		t.Errorf("appending %d tokens to a reset buffer allocates %.0f times (reading alone: %.0f)", n, allocs, base)
	}
	for i, tok := range []*Token{buf.At(0), buf.At(2)} {
		if raw := string(tok.Raw()); raw != [2]string{`<p class="x" id="y">`, `<o:p>`}[i] {
			t.Errorf("token %d: raw %q", i, raw)
		}
	}
	if !HasClass("x")(buf.At(0)) || !AttrEquals("id", "y")(buf.At(0)) || HasClass("y")(buf.At(0)) {
		t.Error("matchers on a copy")
	}
}

// describe returns everything observable about a token.
func describe(tok *Token) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s pos=%d start=%d ns=%q local=%q prefix=%q data=%q text=%q html=%v opens=%v", tok, tok.Pos(), tok.StartPos(), tok.NS(), tok.Local(), tok.Prefix(), tok.Data(), tok.AppendText(nil), tok.HTMLRules(), tok.Opens())
	for a := range tok.Attrs() {
		fmt.Fprintf(&b, " [%q=%q ns=%q]", a.Name(), a.Value(), tok.AttrNamespace(a))
	}
	return b.String()
}

func TestUnsoundError(t *testing.T) {
	for _, tc := range []struct {
		in       string
		maxDepth int
		want     []string
	}{
		{`<div><span>x</div>`, 0, []string{`element "span" at offset 5 closed without its end tag`}},
		{`<p>a<b>b`, 0, []string{`element "b" at offset 4 closed without its end tag`}},
		{`<a href="x>`, 0, []string{`start tag "a" is cut off by the end of the input`}},
		{`</a`, 0, []string{`end tag "a" is cut off by the end of the input`}},
		{`<!-- x`, 0, []string{`comment is cut off by the end of the input`}},
		{`<!DOCTYPE html`, 0, []string{`doctype is cut off by the end of the input`}},
		{`<a b=c"d>x</a>`, 0, []string{`start tag "a" has < in its name, or a quote or < in an attribute name or unquoted attribute value`}},
		{`<script>x`, 0, []string{`raw text or RCDATA element cut off by the end of the input`, `element "script" at offset 0 closed without its end tag`}},
		{`<div><span><i>x`, 1, []string{`can't tell where an element nested past the stack limits ends`, `can't tell where an element nested past the stack limits ends`, `element "div" at offset 0 closed without its end tag`}},
		{`<p>a<li>b</li>`, 0, nil},
	} {
		r := NewReader(strings.NewReader(tc.in))
		if tc.maxDepth != 0 {
			r.SetMaxDepth(tc.maxDepth)
		}
		var got []string
		for r.Next() {
			tok := r.Token()
			if err := tok.UnsoundError(); err != nil {
				var serr *SyntaxError
				if !errors.As(err, &serr) || serr.Pos != tok.Pos() {
					t.Errorf("%q: %v isn't a SyntaxError at %d", tc.in, err, tok.Pos())
				}
				got = append(got, serr.Msg)
			} else if tok.Unsound() {
				t.Errorf("%q: unsound %v without an error", tc.in, tok)
			}
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%q: got %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestStrictUntrackedLimit checks that the untracked elements whose ends
// operations depend on in strict mode are limited to the maximum depth.
func TestStrictUntrackedLimit(t *testing.T) {
	for _, tc := range []struct {
		nested int
		err    bool
	}{
		{4, false}, // 2 tracked, 2 untracked
		{5, true},
	} {
		in := strings.Repeat("<div>", tc.nested) + "x" + strings.Repeat("</div>", tc.nested)
		r := NewReader(strings.NewReader(in))
		r.SetMaxDepth(2)
		r.SetStrict(true)
		var walk func()
		walk = func() {
			for tok := range r.Children() {
				if tok.Kind() == StartTag {
					walk()
				}
			}
		}
		for r.Next() {
			if r.Token().Kind() == StartTag {
				walk()
			}
		}
		if err := r.Err(); tc.err != errors.Is(err, ErrUntrackedLimit) || !tc.err && err != nil {
			t.Errorf("%d nested: got %v", tc.nested, err)
		}
	}
}

// TestAttrAt checks that indexed attribute access gives the attributes in the
// same order as Attrs, for tokens and open elements.
func TestAttrAt(t *testing.T) {
	r := NewReader(strings.NewReader(`<a x=1 y="2" x='3'><b>t`))
	r.SetKeepAttrs(true)
	for r.Next() {
		tok := r.Token()
		var want []string
		for a := range tok.Attrs() {
			want = append(want, string(a.Name())+"="+a.Value())
		}
		var got []string
		for i := range tok.NumAttrs() {
			a := tok.AttrAt(i)
			got = append(got, string(a.Name())+"="+a.Value())
		}
		if !slices.Equal(got, want) {
			t.Errorf("%v: AttrAt gives %q, Attrs %q", tok, got, want)
		}
		for e := range r.Stack() {
			var want, got []string
			for a := range e.Attrs() {
				want = append(want, string(a.Name())+"="+a.Value())
			}
			for i := range e.NumAttrs() {
				a := e.AttrAt(i)
				got = append(got, string(a.Name())+"="+a.Value())
			}
			if !slices.Equal(got, want) {
				t.Errorf("%v: element %s: AttrAt gives %q, Attrs %q", tok, e.Name(), got, want)
			}
		}
	}
	r = NewReader(strings.NewReader(`<a x=1 y="2" x='3'>`))
	r.Next()
	if n, v := r.Token().NumAttrs(), r.Token().AttrAt(2).Value(); n != 3 || v != "3" {
		t.Errorf("got %d attributes, the third %q", n, v)
	}
	defer func() {
		if recover() == nil {
			t.Error("AttrAt out of range didn't panic")
		}
	}()
	r.Token().AttrAt(3)
}

// readAttrs reads the attributes of tok into buf without allocating. It isn't
// inlined, so the test doesn't depend on its caller.
//
//go:noinline
func readAttrs(tok *Token, buf []byte) []byte {
	buf = buf[:0]
	for i := range tok.NumAttrs() {
		a := tok.AttrAt(i)
		buf = append(buf, a.Name()...)
		buf = a.AppendValue(buf)
	}
	return buf
}

func TestAttrAtAllocs(t *testing.T) {
	r := NewReader(strings.NewReader(`<a href="x&amp;y" class=b data-c='d'>`))
	r.Next()
	tok := r.Token()
	buf := make([]byte, 0, 64)
	if string(readAttrs(tok, buf)) != "hrefx&yclassbdata-cd" {
		t.Fatalf("got %q", readAttrs(tok, buf))
	}
	if n := testing.AllocsPerRun(100, func() { buf = readAttrs(tok, buf) }); n != 0 {
		t.Errorf("%v allocations", n)
	}
}
