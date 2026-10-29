package difffuzz

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/pgaskin/htmlstream"
	"github.com/pgaskin/htmlstream/ascii"
	"github.com/pgaskin/htmlstream/htmlutil"
	"github.com/pgaskin/htmlstream/polyglot"
)

// polyglotSeeds returns the hand-written seeds in the htmlstream module's
// testdata/polyglot/seeds.txtar.
func polyglotSeeds() []string {
	var ins []string
	for _, tc := range polyglotCases("seeds.txtar") {
		ins = append(ins, tc.In)
	}
	return ins
}

// html5libTreeInputs returns the inputs of the html5lib tree construction
// tests in the golang.org/x/net module required by go.mod
// (html/testdata/html5lib-tests/tree-construction/*.dat), except fragment
// tests (which are parsed in a context element). The module directory is
// found with go mod download, which downloads the module if it isn't in the
// module cache. If that fails, the error is logged and there are no inputs.
func html5libTreeInputs(f *testing.F) []string {
	ins, err := html5libTreeInputsOnce()
	if err != nil {
		f.Logf("no html5lib tree construction seeds: %v", err)
	}
	return ins
}

var html5libTreeInputsOnce = sync.OnceValues(func() ([]string, error) {
	dir, err := moduleDir("golang.org/x/net")
	if err != nil {
		return nil, err
	}
	files, err := filepath.Glob(filepath.Join(dir, "html", "testdata", "html5lib-tests", "tree-construction", "*.dat"))
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no tree construction tests in %s", dir)
	}
	var ins []string
	for _, file := range files {
		b, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		var data []string
		section := ""
		fragment := false
		flush := func() {
			if data != nil && !fragment {
				ins = append(ins, strings.Join(data, "\n"))
			}
			data, fragment = nil, false
		}
		for line := range strings.Lines(string(b)) {
			line = strings.TrimSuffix(line, "\n")
			if strings.HasPrefix(line, "#") {
				switch line {
				case "#data":
					flush()
					data = []string{}
					section = line
					continue
				case "#errors", "#new-errors", "#document", "#document-fragment", "#script-on", "#script-off":
					section = line
					if line == "#document-fragment" {
						fragment = true
					}
					continue
				}
			}
			if section == "#data" {
				data = append(data, line)
			}
		}
		flush()
	}
	return ins, nil
})

// moduleDir returns the directory of a module required by go.mod, using go
// mod download.
func moduleDir(path string) (string, error) {
	cmd := exec.Command("go", "mod", "download", "-json", path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	var m struct{ Dir, Error string }
	if jerr := json.Unmarshal(out, &m); jerr == nil && m.Error != "" {
		return "", fmt.Errorf("go mod download %s: %s", path, m.Error)
	}
	if err != nil {
		return "", fmt.Errorf("go mod download %s: %w: %s", path, err, bytes.TrimSpace(stderr.Bytes()))
	}
	if m.Dir == "" {
		return "", fmt.Errorf("go mod download %s: no directory in %q", path, out)
	}
	return m.Dir, nil
}

// FuzzPolyglotDOM checks the guarantees of polyglot.Convert against
// golang.org/x/net/html and encoding/xml, with the DOM comparison of
// TestPolyglot:
//
//   - without warnings, HTML parsers parse the output to the same DOM as
//     the input, and XML parsers accept it and parse it to the same DOM as
//     HTML parsers;
//   - with warnings, XML parsers still accept the output, unless a script
//     of a type other than JavaScript or JSON contains < or & (which is
//     written unchanged, with a warning).
//
// Inputs for which it warns about a table closing a p in quirks mode are also
// converted without a doctype (NoDoctype), with the output in quirks mode,
// where the p is kept open around the table, and checked the same way.
// Inputs which aren't valid UTF-8 are skipped, since polyglot.Convert
// requires UTF-8 input. Read errors (e.g., a UTF-16 byte order mark) are
// skipped. The seeds are the inputs in the htmlstream module's
// testdata/polyglot (including fuzz.txtar, the inputs which found bugs) and
// the html5lib tree construction inputs.
func FuzzPolyglotDOM(f *testing.F) {
	for _, tc := range polyglotCases("*.txtar") {
		f.Add(tc.In)
	}
	for _, in := range html5libTreeInputs(f) {
		f.Add(in)
	}
	f.Fuzz(func(t *testing.T, in string) {
		if !utf8.ValidString(in) {
			return
		}
		var warnings []string
		var b bytes.Buffer
		err := polyglot.Convert(&b, strings.NewReader(in), polyglot.Options{
			Warn: func(w polyglot.Warning) {
				warnings = append(warnings, fmt.Sprintf("%d: %s", w.Pos, w.Msg))
			},
		})
		if err != nil {
			if errors.Is(err, polyglot.ErrInvalidOp) {
				t.Fatalf("%q: %v", in, err)
			}
			return // a Reader error, like an unsupported encoding
		}
		checkPolyglot(t, in, in, b.String(), warnings, false, false)
		checkIndent(t, in, b.String(), warnings)
		if !slices.ContainsFunc(warnings, isPTableWarning) {
			return
		}
		// in quirks mode, and the output without a doctype is too: the p is
		// kept open around the table
		warnings = warnings[:0]
		b.Reset()
		if err := polyglot.Convert(&b, strings.NewReader(in), polyglot.Options{
			DoctypeMode: polyglot.NoDoctype,
			Warn: func(w polyglot.Warning) {
				warnings = append(warnings, fmt.Sprintf("%d: %s", w.Pos, w.Msg))
			},
		}); err != nil {
			t.Fatalf("%q: NoDoctype: %v", in, err)
		}
		checkPolyglot(t, in, in, b.String(), warnings, false, false)
	})
}

// neutralWarnings match the polyglot warnings for differences which don't
// change the HTML DOM as compared (see htmlDOM): prefixed names, which XML
// parsers read in another namespace, invalid namespace declarations dropped,
// xml:lang written with the value of lang, and the charset meta element past
// the first 1024 bytes. The XML DOM is compared with the HTML DOM only when
// the warnings are those with xml set.
var neutralWarnings = []neutralWarning{
	{regexp.MustCompile(`^(element|attribute) ".*" is in .* for XML parsers and in .* for HTML parsers \(will not warn again for this prefix and namespace\)$`), false},
	{regexp.MustCompile(`^element ".*" is named ".*" for XML parsers and ".*" for HTML parsers \(will not warn again for this prefix and namespace\)$`), false},
	{regexp.MustCompile(`^dropped namespace declaration ".*" \(invalid in XML: .*\)$`), false},
	{regexp.MustCompile(`^wrote xml:lang differing from lang with the value of lang \(to match the lang HTML parsers use\) \(will not warn again\)$`), true},
	{regexp.MustCompile(`^wrote the charset meta element past the first 1024 bytes \(HTML parsers which aren't told the encoding will not find it before parsing the output\)$`), true},
}

type neutralWarning struct {
	re  *regexp.Regexp
	xml bool // the XML DOM is the same as the HTML DOM
}

func isPTableWarning(w string) bool {
	return strings.HasSuffix(w, ": closed p before table (HTML parsers would have put the table in the p in quirks mode)")
}

// checkPolyglot checks the output out of polyglot.Convert or a
// polyglot.Writer for the input in, with the warnings given (see
// FuzzPolyglotDOM). The HTML DOM of the output is compared with that of html,
// the input as an HTML parser would have to read it to get the same document
// (in, unless a Reader option changes how the input is read), or not compared
// if html is empty. With scripting, the DOMs are those of HTML parsers with
// scripting enabled. With xmlOnly, only the well-formedness of the output is
// checked. Warnings other than those of neutralWarnings stop the comparisons
// once the output is checked to be well-formed.
func checkPolyglot(t *testing.T, in, html, out string, warnings []string, scripting, xmlOnly bool) {
	xml, xmlErr := xmlDOM(out, true)
	compareXML := true
	for _, w := range warnings {
		_, msg, _ := strings.Cut(w, ": ")
		i := slices.IndexFunc(neutralWarnings, func(n neutralWarning) bool { return n.re.MatchString(msg) })
		if i < 0 {
			if xmlErr != nil && !slices.ContainsFunc(warnings, isScriptTypeWarning) {
				t.Fatalf("%q: output is not well-formed XML: %v\noutput: %s\nwarnings: %q", in, xmlErr, out, warnings)
			}
			return
		}
		compareXML = compareXML && neutralWarnings[i].xml
	}
	if xmlErr != nil {
		t.Fatalf("%q: output is not well-formed XML: %v\noutput: %s", in, xmlErr, out)
	}
	if xmlOnly {
		return
	}
	outDOM := htmlDOMScripting(t, out, scripting, false)
	if html != "" {
		if xnetForeignTemplate(html) {
			return
		}
		html = xnetDroppedSlash(html)
		inDOM := htmlDOMScripting(t, html, scripting, false)
		if inDOM != outDOM && htmlDOMScripting(t, dropSpaceAfterBody(html), scripting, false) == outDOM {
			return
		}
		if inDOM != outDOM && xnetScriptEscape(html) {
			return
		}
		if inDOM != outDOM {
			if d := htmlDOMScripting(t, xnetTemplateTableSpace(html), scripting, false); d == outDOM {
				inDOM = d
			}
		}
		if inDOM != outDOM {
			t.Fatalf("%q: HTML DOM differs without a warning:\noutput: %s\nDOM (input -, output +):\n%s", in, out, domDiff(inDOM, outDOM))
		}
	}
	outDOM = htmlDOMScripting(t, out, scripting, true)
	if !compareXML || xml != outDOM && (xnetTextareaCR.MatchString(out) || xnetScriptEscape(out)) {
		return
	}
	if xml != outDOM {
		t.Fatalf("%q: XML DOM differs from HTML DOM:\noutput: %s\nDOM (XML -, HTML +):\n%s", in, out, domDiff(xml, outDOM))
	}
}

// checkIndent checks the output of in with Indent and IndentPrefix against
// out, its output without: the warnings are the same, and without warnings,
// the HTML and XML DOMs are the same (see checkPolyglot), and the HTML DOM
// is the same as that of out except for whitespace-only text and the spaces
// and tabs at the start of lines of text. Inputs skipped by checkPolyglot
// for x/net's differences are skipped.
func checkIndent(t *testing.T, in, out string, warnings []string) {
	var indented []string
	var b bytes.Buffer
	err := polyglot.Convert(&b, strings.NewReader(in), polyglot.Options{
		Indent:       "\t",
		IndentPrefix: " ",
		Warn: func(w polyglot.Warning) {
			indented = append(indented, fmt.Sprintf("%d: %s", w.Pos, w.Msg))
		},
	})
	if err != nil {
		t.Fatalf("%q: with Indent: %v", in, err)
	}
	// the indentation can put the charset meta element past byte 1024
	past1024 := func(w string) bool { return strings.Contains(w, "past the first 1024 bytes") }
	indented = slices.DeleteFunc(indented, past1024)
	warnings = slices.DeleteFunc(slices.Clone(warnings), past1024)
	if !slices.Equal(indented, warnings) {
		t.Fatalf("%q: with Indent: warnings %q, without %q", in, indented, warnings)
	}
	if len(warnings) != 0 || xnetForeignTemplate(in) || xnetScriptEscape(in) {
		return
	}
	checkPolyglot(t, in, "", b.String(), nil, false, false)
	lines := regexp.MustCompile(`\\n( |\\t)*`)
	want := lines.ReplaceAllString(htmlDOMScripting(t, out, false, false), `\n`)
	if got := lines.ReplaceAllString(htmlDOMScripting(t, b.String(), false, false), `\n`); got != want {
		t.Fatalf("%q: HTML DOM differs with Indent:\noutput: %s\nwith Indent: %s\nDOM (without -, with +):\n%s", in, out, b.String(), domDiff(want, got))
	}
}

// writerSeeds are seeds for FuzzPolyglotWriter: XML syntax, noscript
// contents, and elements to leave out.
var writerSeeds = []string{
	`<p>a<i/>b<![CDATA[c<d]]><div/><br/><svg><rect/></svg>`,
	`<?xml version="1.0"?><?pi a?><p><?x`,
	`<pre><![CDATA[` + "\n" + `x]]></pre><table><![CDATA[y]]><tr><td>z`,
	`<noscript><p>a</p></noscript><body><noscript>b<i>c</i></noscript><noscript>d</noscript>`,
	`<head><noscript><link rel=x></noscript></head><noscript><style>s</style></noscript>`,
	`<div><span>a<b>b</b></span><table><tr><td>c</td></tr></table><template><tr><td>d</template></div>`,
	`<!DOCTYPE html [<!ENTITY a "b>">]><p>x`,
	`<!DOCTYPE html [ ]><head><meta charset=utf-8></meta><link rel=x>` + "\n" + `</head><p>a<br></br>b<BR> </br>c<img src=x></img></p>`,
	`<title> <![CDATA[a<b&amp;]]></title><script><![CDATA[if (a<b) x();]]></script><style>` + "\n" + `<![CDATA[p{}]]></style><script type=importmap><![CDATA[{"a<b":"c"}]]></script><textarea>` + "\n" + `<![CDATA[x<y]]></textarea>`,
	`<textarea><![CDATA[` + "\n" + `x<y]]></textarea><script><![CDATA[a]]><![CDATA[b]]></script>`,
	"<title> \r <![CDATA[\r\n0\r]]>\r</title><script><![CDATA[\r1\r\n]]></script>",
}

// quirksSeeds are seeds for FuzzPolyglotWriter with a table in a p, which
// the output without a doctype keeps open around the table.
var quirksSeeds = []string{
	`<p>a<table><tr><td>x</table>b<br><img src=i><title>t</title><table></table>c</p>d`,
	`<div><p>a<table></table>b</div>c<p>d<table></table><li>e`,
	`<p>a<table><tr><td><p>b<table></table>c</table>d<span>e</span>`,
	`<span><p>a<table></table>b</span>c<b><p>d<table></table>e</b>f`,
	`<p><b>a<table></table>b<p>c<table></table></>` + "\n" + `<pre></><svg>`,
}

// modeSeed is an input and a mode byte for FuzzPolyglotWriter or
// FuzzPolyglotHooks.
type modeSeed struct {
	in   string
	mode uint8
}

// writerFound are inputs and modes which found bugs with FuzzPolyglotWriter.
var writerFound = []modeSeed{
	{"0<nosCript>>", 0x23},
	{"<B><u A><u></B></u>00", 0x02},
	{"<mAth><ms><A!><mAlignmArk>", 0x00},
	{"<seleCt><A><p><C><option>", '1'},
	{"<templAte><td></td><tt><td></templAte>0", 0x1f},
	{"<A><option><B><option>", 0xaf},
	{"<mAth><A><AnnotAtion-Xml><svg >", ','},
	{"<mAth><ms><C><mAlignmArk>", '-'},
	{"<![CDATA[ 00", 0x01},
	{"<title><![CDATA[\r0]]>", 0x13},
	{"<mAth>000000000000000<g><g//>00", 0x01},
	{"<svg dX dX=/>00", '='}, // a known difference (rewritten by xnetDroppedSlash)
	{"<teXtAreA><![CDATA[\x00]]>", '?'},
	{"<svg><sCript> 0000000000", 0x01}, // an SVG script, normal text for HTML parsers
	{"<heAd/><nosCript>0", 0x01},
	{"<title><![CDATA[0\r]]>\n", '\r'},
	{"</BodY><!DOCTYPE><!0", '`'}, // a known difference (rewritten by specDoctypes)
	{"<<!DOCTYPE>A", '\\'},
}

// hooksFound are inputs and modes which found bugs with FuzzPolyglotHooks.
var hooksFound = []modeSeed{
	{"0000<templAte><td></td><A00000><td A00>", '7'},
	{"<A><p><B 00><B><B><B><A>0", 0x04}, // Noah's Ark clause with renamed formatting elements
	{"<sCript ><!--0<<sCript </sCript", 0x04},
	{"<ruBY><p><A000>0<rtC>", 0xd8}, // unwrapped child of a p closed at rtc (unwrapSound)
	{"<heAd/><nosCript>0", 0x19},    // noscript after a head self-closed with XML syntax
}

// FuzzPolyglotWriter checks polyglot.Writer like FuzzPolyglotDOM checks
// polyglot.Convert, with the Reader options polyglot.Convert doesn't use and
// with elements left out, selected by mode: bit 0 enables XML syntax, bit 1
// scripting, bits 2-4 select an element to leave out (the n-th distinct
// element name opened, or none for 0), and bit 5 selects leaving out only its
// tags (unwrap) instead of the whole element, and bit 6 writes no doctype
// (NoDoctype), so the output is in quirks mode if the input is, and a table
// doesn't close an open p.
//
// With XML syntax, the Reader reads self-closing tags of non-void HTML
// elements as empty elements, void elements as closed by their end tags,
// doctypes with an internal subset as a whole and CDATA sections in HTML
// content as text, which HTML parsers don't, so the HTML DOM of the output
// is compared with that of the input with those rewritten (xmlSyntaxInput);
// inputs with processing instructions aren't compared. With scripting, the
// DOMs are compared as parsed with scripting enabled. With an element left
// out, only the output is checked: it must be well-formed XML, and without
// warnings its XML and HTML DOMs must match, unless only the tags of an
// element were left out and its contents may be parsed differently without
// it (see unwrapSound). Leaving out an svg or math element or an integration
// point without its contents is an error, which is expected.
//
// The seeds are the hand-written seeds in the htmlstream module's
// testdata/polyglot/seeds.txtar and writerSeeds with several modes,
// writerFound, quirksSeeds without a doctype, and the html5lib tree
// construction inputs with XML syntax and with scripting.
func FuzzPolyglotWriter(f *testing.F) {
	for _, in := range slices.Concat(polyglotSeeds(), writerSeeds) {
		for _, mode := range []uint8{1, 2, 3, 1 | 1<<2, 1 | 1<<2 | 1<<5, 2 << 2} {
			f.Add(in, mode)
		}
	}
	for _, s := range writerFound {
		f.Add(s.in, s.mode)
	}
	for _, in := range quirksSeeds {
		f.Add(in, uint8(64))
		f.Add(in, uint8(64|1))
		f.Add(in, uint8(64|2<<2|32))
	}
	for _, in := range html5libTreeInputs(f) {
		f.Add(in, uint8(1))
		f.Add(in, uint8(2))
	}
	f.Fuzz(func(t *testing.T, in string, mode uint8) {
		if !utf8.ValidString(in) {
			return
		}
		xmlSyntax, scripting := mode&1 != 0, mode&2 != 0
		leaveOut, unwrap := int(mode>>2&7), mode&32 != 0
		var opt polyglot.Options
		if mode&64 != 0 {
			opt.DoctypeMode = polyglot.NoDoctype
		}
		rd := htmlstream.NewReader(strings.NewReader(in))
		rd.SetXMLSyntax(xmlSyntax)
		rd.SetScripting(scripting)
		var warnings []string
		var b bytes.Buffer
		opt.Warn = func(w polyglot.Warning) {
			warnings = append(warnings, fmt.Sprintf("%d: %s", w.Pos, w.Msg))
		}
		pw := polyglot.NewWriter(&b, opt)
		var names []string // distinct element names opened (see Token.AppendLocalName)
		var left htmlutil.Stack[struct{}]
		edited, sound := false, true
		for rd.Next() {
			tok := rd.Token()
			switch tok.Kind() {
			case htmlstream.StartTag:
				if leaveOut == 0 || !tok.Opens() {
					break
				}
				name := string(tok.AppendLocalName(nil))
				i := slices.Index(names, name)
				if i < 0 {
					i = len(names)
					names = append(names, name)
				}
				if i+1 != leaveOut {
					break
				}
				edited = true
				if unwrap {
					sound = sound && unwrapSound(rd, in)
					left.Push(tok, struct{}{})
				} else if err := rd.Skip(); err != nil {
					return
				}
				continue
			case htmlstream.EndTag:
				if _, ok := left.Pop(tok); ok {
					continue
				}
			}
			if err := pw.WriteToken(tok); err != nil {
				if edited && (strings.Contains(err.Error(), "the caller left out its root element") || strings.Contains(err.Error(), "the caller left out its integration point") || strings.Contains(err.Error(), "the caller left out the element it was in")) {
					return // the element left out sets the namespace of its contents
				}
				t.Fatalf("%q, mode %#x: WriteToken: %v", in, mode, err)
			}
		}
		if rd.Err() != nil {
			return
		}
		if err := pw.Close(); err != nil {
			t.Fatalf("%q, mode %#x: Close: %v", in, mode, err)
		}
		html := in
		switch {
		case edited, xmlSyntax && strings.Contains(in, "<?"):
			html = ""
		case xmlSyntax:
			html = xmlSyntaxInput(t, in, scripting)
		}
		checkPolyglot(t, in, html, b.String(), warnings, scripting, !sound)
	})
}

// unwrapSound reports whether the contents of the element opened by the
// current start tag of rd are parsed the same by HTML parsers without the
// element: an HTML element which isn't special or formatting, whose parent
// isn't an element whose end tag HTML parsers imply when it is the current
// node at some start tags (option, optgroup, h1-h6, rb, rt, rp, rtc; p at
// option and optgroup; p, li, dd and dt at rb, rt, rp and rtc, which
// generate implied end tags in a ruby), which the element prevents.
func unwrapSound(rd *htmlstream.Reader, in string) bool {
	tok := rd.Token()
	if tok.NS() != htmlstream.NamespaceXHTML || htmlutil.IsSpecial(tok) || htmlutil.IsFormatting(tok) {
		return false
	}
	lower := strings.ToLower(in)
	ruby := strings.Contains(lower, "<rb") || strings.Contains(lower, "<rt") || strings.Contains(lower, "<rp")
	i := 0
	for e := range rd.Stack() {
		if i++; i == 2 {
			// the parent
			switch strings.ToLower(string(e.Name())) {
			case "option", "optgroup", "h1", "h2", "h3", "h4", "h5", "h6", "rb", "rt", "rp", "rtc":
				return false
			case "p":
				return !strings.Contains(lower, "<opt") && !ruby
			case "li", "dd", "dt":
				return !ruby
			}
		}
	}
	return true
}

// xmlSyntaxInput rewrites in so that an HTML parser reads it like the Reader
// with XML syntax does: self-closing tags of non-void HTML elements are
// expanded, the end tags of void elements are removed, CDATA sections in HTML
// content are replaced with escaped text, a CDATA section around the
// contents of script (of JavaScript or JSON), style, title and textarea
// elements, which polyglot removes, is replaced with its contents, and
// doctypes which HTML parsers
// end at an earlier ">" (e.g., in an internal subset) are replaced with
// <!DOCTYPE html> or <!DOCTYPE quirks>, as htmlutil.Document decides (see
// specDoctypes). Other text is escaped too, so that text before a CDATA
// section doesn't form a tag with it (e.g., <<![CDATA[a). It returns an
// empty string for a self-closing html, head or body tag, whose end tag HTML
// parsers handle differently from the end of the element (e.g., text after
// </html> goes in the body).
func xmlSyntaxInput(t *testing.T, in string, scripting bool) string {
	var b bytes.Buffer
	x := htmlstream.NewTransformer(&b, strings.NewReader(in))
	x.SetXMLSyntax(true)
	x.SetScripting(scripting)
	var d htmlutil.Document
	var buf []byte
	var scriptType string
	for x.Next() {
		tok := x.Token()
		d.Update(tok)
		switch tok.Kind() {
		case htmlstream.Doctype:
			if raw := tok.Raw(); tok.Incomplete() || bytes.IndexByte(raw[:len(raw)-1], '>') >= 0 {
				doctype := "<!DOCTYPE html>"
				if d.Quirks() {
					doctype = "<!DOCTYPE quirks>"
				}
				if err := x.Replace([]byte(doctype)); err != nil {
					t.Fatal(err)
				}
			}
		case htmlstream.EndTag:
			if !tok.Stray() && !tok.Implied() && htmlutil.IsVoid(tok) {
				if err := x.Remove(); err != nil {
					t.Fatal(err)
				}
			}
		case htmlstream.StartTag:
			if tok.Is("script") {
				scriptType = htmlutil.ScriptType(tok)
			}
			if tok.SelfClosing() && !tok.Opens() && !htmlutil.IsVoid(tok) {
				if tok.NS() == htmlstream.NamespaceXHTML && (tok.Is("html") || tok.Is("head") || tok.Is("body")) {
					return ""
				}
				if err := x.ExpandSelfClosing(); err != nil && err != htmlstream.ErrIncompleteTag {
					t.Fatal(err)
				}
			}
		case htmlstream.Text, htmlstream.CDATA:
			var parent string
			inHTML := true
			for e := range x.Stack() {
				inHTML = e.NS() == htmlstream.NamespaceXHTML
				parent = strings.ToLower(string(e.Name()))
				break
			}
			// whole contents with a CDATA section, which polyglot removes
			whole := inHTML && !tok.Partial() && !tok.Continued()
			if tok.InRawText() {
				w := htmlutil.CDATABlockComment | htmlutil.CDATABare
				switch {
				case parent == "script" && htmlutil.IsJavaScriptType(scriptType):
					w |= htmlutil.CDATALineComment
					if scriptType != "module" {
						w |= htmlutil.CDATAHTMLComment
					}
				case parent == "style":
					w |= htmlutil.CDATAHTMLComment
				case parent == "script" && htmlutil.IsJSONType(scriptType):
					w = htmlutil.CDATABare
				default:
					whole = false
				}
				if s, ok := htmlutil.CutCDATAWrapper(tok.Data(), w); whole && ok {
					// with the whitespace around the wrapper, which polyglot
					// keeps
					data := tok.Data()
					lead := data[:len(data)-len(bytes.TrimLeft(data, "\t\n\f\r "))]
					trail := data[len(bytes.TrimRight(data, "\t\n\f\r ")):]
					if err := x.Replace(slices.Concat(lead, s, trail)); err != nil {
						t.Fatal(err)
					}
				}
				break
			}
			buf = tok.AppendText(buf[:0])
			if whole && tok.Kind() == htmlstream.Text && (parent == "title" || parent == "textarea") {
				// the undecoded data, with line ends normalized as XML
				// parsers do before reading CDATA sections (a CR before
				// "]]>" and an LF after it are two line ends)
				data := bytes.ReplaceAll(bytes.ReplaceAll(tok.Data(), []byte("\r\n"), []byte("\n")), []byte("\r"), []byte("\n"))
				if s, ok := bytes.CutPrefix(ascii.TrimSpace(data), []byte("<![CDATA[")); ok {
					if s, ok = bytes.CutSuffix(s, []byte("]]>")); ok && !bytes.Contains(s, []byte("]]>")) {
						i := bytes.Index(data, []byte("<![CDATA["))
						buf = slices.Concat(data[:i], s, data[i+len("<![CDATA[")+len(s)+len("]]>"):])
					}
				}
			}
			if inHTML || tok.Kind() == htmlstream.Text {
				if err := x.Replace(htmlstream.AppendEscapeText(nil, buf)); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if err := x.Close(); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// dropSpaceAfterBody removes the whitespace text after the first body or html
// end tag which closed other elements from in. HTML parsers keep elements
// left open before these end tags open, and append the whitespace after them
// to the elements (e.g., the text of an unclosed p), while the Reader closes
// them. The polyglot package writes the whitespace after them in them up to
// the first token other than whitespace, comments and body and html end tags
// (e.g., content, which it reports, or a stray end tag), and doesn't report
// the whitespace after that (see its token method), which is removed. NUL
// counts as whitespace, as in the polyglot package: in the elements, HTML
// parsers ignore it in HTML elements and replace it with U+FFFD in SVG and
// MathML elements, as the polyglot package does, and after them, HTML
// parsers ignore it in body text, and the polyglot package drops it.
// Leading whitespace which the Reader gives as a token of its own (with
// only html open) is kept with the text after it, unless that is whitespace
// too. Whitespace after a body or html end tag which closed nothing else is
// body text for both, so it is kept (see keepOpen in the polyglot package).
func dropSpaceAfterBody(in string) string {
	r := htmlstream.NewReader(strings.NewReader(in))
	var drop [][2]int
	after := false
	other := false          // a token other than whitespace, comments and body and html end tags came after it
	closed := ""            // the first element closed by the implied end tags before the token
	space := [2]int{-1, -1} // the last whitespace text token, until the next token
	for r.Next() {
		tok := r.Token()
		pos := int(tok.Pos())
		if space[0] >= 0 && !(tok.Kind() == htmlstream.Text && pos == space[1]) {
			drop = append(drop, space)
		}
		prev := space
		space = [2]int{-1, -1}
		switch {
		case tok.Kind() == htmlstream.EndTag && tok.Implied():
			if closed == "" {
				closed = strings.ToLower(string(tok.Name()))
			}
			continue
		case !after && tok.Kind() == htmlstream.EndTag && !tok.Stray() && (tok.Is("body") || tok.Is("html")):
			after = closed != "" && closed != "body"
		case after && !other:
			blank := tok.Kind() == htmlstream.Text && strings.TrimLeft(string(tok.AppendText(nil)), " \t\n\f\r\x00") == ""
			k := tok.Kind()
			other = !blank && k != htmlstream.Comment && !(k == htmlstream.EndTag && (tok.Is("body") || tok.Is("html")))
		case after && tok.Kind() == htmlstream.Text && strings.TrimLeft(string(tok.AppendText(nil)), " \t\n\f\r\x00") == "":
			space = [2]int{pos, pos + len(tok.Raw())}
			if prev[0] >= 0 && pos == prev[1] {
				space[0] = prev[0] // after leading whitespace
			}
		}
		closed = ""
	}
	if space[0] >= 0 {
		drop = append(drop, space)
	}
	var b strings.Builder
	last := 0
	for _, d := range drop {
		b.WriteString(in[last:d[0]])
		last = d[1]
	}
	b.WriteString(in[last:])
	return b.String()
}

// xnetScriptEscape reports whether a script in the input contains "<"
// followed by a character other than "/" or an ASCII letter after "<!--" (in
// the script data escaped state of the HTML5 tokenizer). x/net leaves the
// escaped state there, unlike the spec, which changes where the script ends
// after a later "<script" (e.g., <script><!--<<script></script>x).
func xnetScriptEscape(in string) bool {
	if !strings.Contains(strings.ToLower(in), "<script") {
		return false
	}
	r := htmlstream.NewReader(strings.NewReader(in))
	var script []byte
	inScript := false
	for r.Next() {
		tok := r.Token()
		switch {
		case tok.Kind() == htmlstream.StartTag && tok.NS() == htmlstream.NamespaceXHTML && tok.Is("script") && tok.Opens():
			inScript, script = true, script[:0]
		case inScript && tok.Kind() == htmlstream.Text:
			script = append(script, tok.Raw()...)
		case inScript:
			inScript = false
			if xnetEscapedLessThan(script) {
				return true
			}
		}
	}
	return inScript && xnetEscapedLessThan(script)
}

// xnetEscapedLessThan reports whether the script contents b have a "<"
// handled differently by x/net (see xnetScriptEscape). The escape states are
// tracked like the polyglot package does.
func xnetEscapedLessThan(b []byte) bool {
	escaped, double, dashes := false, false, 0
	for i := 0; i < len(b); i++ {
		switch b[i] {
		case '-':
			dashes = min(dashes+1, 2)
			continue
		case '>':
			if dashes == 2 {
				escaped, double = false, false
			}
			dashes = 0
			continue
		case '<':
		default:
			dashes = 0
			continue
		}
		dashes = 0
		if !escaped {
			if bytes.HasPrefix(b[i:], []byte("<!--")) {
				escaped, dashes = true, 2
				i += 3
			}
			continue
		}
		if !double && i+1 < len(b) && b[i+1] != '/' && !('a' <= b[i+1]|0x20 && b[i+1]|0x20 <= 'z') {
			return true
		}
		off := 1
		if double {
			off = 2
		}
		if n := off + len("script") + 1; i+n <= len(b) {
			t := b[i : i+n]
			if (!double || t[1] == '/') && strings.EqualFold(string(t[off:n-1]), "script") && strings.IndexByte(" \t\n\f\r/>", t[n-1]) >= 0 {
				double = !double
				i += n - 2
			}
		}
	}
	return false
}

// xnetForeignTemplate reports whether the input has a template element in
// an svg or math element (in an integration point). x/net drops it and
// everything after it, while the HTML5 spec inserts it like in the body.
func xnetForeignTemplate(in string) bool {
	if !strings.Contains(strings.ToLower(in), "<template") {
		return false
	}
	r := htmlstream.NewReader(strings.NewReader(in))
	var foreign htmlutil.Stack[bool]
	inForeign := func() bool {
		for _, f := range foreign.All() {
			if *f {
				return true
			}
		}
		return false
	}
	for r.Next() {
		tok := r.Token()
		switch tok.Kind() {
		case htmlstream.StartTag:
			if tok.NS() == htmlstream.NamespaceXHTML && tok.Is("template") && inForeign() {
				return true
			}
			if tok.Opens() {
				foreign.Push(tok, tok.NS() == htmlstream.NamespaceSVG || tok.NS() == htmlstream.NamespaceMathML)
			}
		case htmlstream.EndTag:
			foreign.Pop(tok)
		}
	}
	return false
}

// xnetDroppedSlash quotes the value of the last attribute of the start tags
// ending with "/>" which aren't self-closing, since the "/" ends an unquoted
// attribute value, and whose last attribute is a duplicate. x/net drops the
// duplicate attribute, and then reads the tag as self-closing, since it only
// checks whether the "/" ends the value of the last attribute it kept (e.g.,
// <svg a a=/>x).
func xnetDroppedSlash(in string) string {
	if !strings.Contains(in, "/>") {
		return in
	}
	r := htmlstream.NewReader(strings.NewReader(in))
	var b strings.Builder
	last := 0
	for r.Next() {
		tok := r.Token()
		if tok.Kind() != htmlstream.StartTag || tok.SelfClosing() || tok.Incomplete() || !bytes.HasSuffix(tok.Raw(), []byte("/>")) {
			continue
		}
		n := tok.NumAttrs()
		if n < 2 {
			continue
		}
		a := tok.AttrAt(n - 1)
		if a.Quote() != 0 || !a.HasValue() {
			continue
		}
		name := strings.ToLower(string(a.Name()))
		dup := false
		for i := range n - 1 {
			dup = dup || tok.AttrAt(i).Is(name)
		}
		if !dup {
			continue
		}
		raw := tok.Raw()
		pos := int(tok.Pos())
		b.WriteString(in[last : pos+bytes.LastIndex(raw, a.Raw())])
		fmt.Fprintf(&b, "%s=\"%s\">", a.Name(), a.RawValue())
		last = pos + len(raw)
	}
	if last == 0 {
		return in
	}
	b.WriteString(in[last:])
	return b.String()
}

// xnetTemplateTableSpace removes whitespace-only text directly in a template
// whose contents are parsed like the contents of a table, a table section or
// a row (the first child element is caption, colgroup, tbody, tfoot, thead,
// tr, td or th). The HTML5 spec inserts the whitespace in the template (the
// "in table" insertion mode handles text like in a table when the current
// node is a template), while x/net handles it like other text in a table,
// reopening formatting elements first (e.g., the a in
// <template><tr></tr><a><tr></tr> ). Whitespace-only text isn't part of the
// compared DOM, but text next to it across a comment is, so the fuzz target
// only uses this if the DOMs differ.
func xnetTemplateTableSpace(s string) string {
	if !strings.Contains(strings.ToLower(s), "<template") {
		return s
	}
	r := htmlstream.NewReader(strings.NewReader(s))
	type elem struct {
		template bool
		mode     byte // template: 't' for table contents, 'o' for other contents
	}
	var stack htmlutil.Stack[elem]
	var drop [][2]int
	run := [2]int{-1, -1} // the current text run
	space := false        // the text run is whitespace directly in such a template
	for r.Next() {
		tok := r.Token()
		pos := int(tok.Pos())
		if tok.Kind() == htmlstream.Text {
			if run[0] < 0 {
				t, ok := stack.Top()
				run[0], space = pos, ok && t.template && t.mode == 't'
			}
			run[1] = pos + len(tok.Raw())
			space = space && strings.Trim(string(tok.AppendText(nil)), " \t\n\f\r") == ""
			continue
		}
		if run[0] >= 0 && space {
			drop = append(drop, run)
		}
		run = [2]int{-1, -1}
		switch tok.Kind() {
		case htmlstream.StartTag:
			if t, ok := stack.Top(); ok && t.template && t.mode == 0 {
				switch strings.ToLower(string(tok.Name())) {
				case "base", "basefont", "bgsound", "link", "meta", "noframes", "script", "style", "template", "title":
					// head content, which doesn't select the insertion mode
				case "caption", "colgroup", "tbody", "tfoot", "thead", "tr", "td", "th":
					t.mode = 't'
				default:
					t.mode = 'o'
				}
			}
			stack.Push(tok, elem{template: tok.NS() == htmlstream.NamespaceXHTML && tok.Is("template")})
		case htmlstream.EndTag:
			stack.Pop(tok)
		}
	}
	if run[0] >= 0 && space {
		drop = append(drop, run)
	}
	if r.Err() != nil || len(drop) == 0 {
		return s
	}
	var b strings.Builder
	last := 0
	for _, d := range drop {
		b.WriteString(s[last:d[0]])
		last = d[1]
	}
	b.WriteString(s[last:])
	return b.String()
}

// xnetTextareaCR matches a textarea starting with a carriage return
// reference in the output. x/net drops the carriage return (and a line feed
// after it), unlike the HTML5 spec, which only drops a line feed directly
// after the start tag. For pre and listing, htmlDOM works around this (see
// specPreNewlines), but textarea contents can't contain a comment.
var xnetTextareaCR = regexp.MustCompile(`<textarea[^>]*>&#13;`)

func isScriptTypeWarning(w string) bool {
	return strings.Contains(w, ": script of type ")
}

// FuzzPolyglotHooks checks the invariants of the polyglot.Writer hooks: hooks
// which do nothing, write what the conversion writes (SetText of the text,
// nothing inserted around the tags), or hold back the output and release it or
// discard it with the held output (see holdOps), give byte-identical output,
// the tag events are well nested and never inside a text node, start and end
// tags have the same ID, Depth, Name, Local, NS, Reopened and Void, IDs
// increase and reopened elements have the ID of an earlier start tag, the text
// node events are paired (First, then Last) and have the ID and Depth of the
// innermost open element, and start tags from the input have their token. With
// edits (see editingHooks), the output must still be well-formed XML, and
// without warnings its XML and HTML DOMs must match. With renames only (see
// renameRandom), the output must also have the HTML DOM of the output without
// them, with the names changed, also with warnings. The mode byte selects XML
// syntax (bit 0), scripting (bit 1) and the edits and renames (bits 2-7, the
// random seed; none for 0). The seeds are the hand-written seeds, writerSeeds
// and the html5lib tree construction inputs with other modes than
// FuzzPolyglotWriter's, and hooksFound.
func FuzzPolyglotHooks(f *testing.F) {
	for _, in := range slices.Concat(polyglotSeeds(), writerSeeds) {
		for _, mode := range []uint8{0, 1, 2, 3, 1 << 2, 2 << 2, 1 | 3<<2, 5 << 2} {
			f.Add(in, mode)
		}
	}
	for _, s := range hooksFound {
		f.Add(s.in, s.mode)
	}
	for _, in := range html5libTreeInputs(f) {
		f.Add(in, uint8(0))
		f.Add(in, uint8(1<<2))
	}
	f.Fuzz(func(t *testing.T, in string, mode uint8) {
		if !utf8.ValidString(in) {
			return
		}
		xmlSyntax, scripting, edits := mode&1 != 0, mode&2 != 0, mode>>2
		convert := func(hooks func(rd *htmlstream.Reader) polyglot.Options) (string, []string, error) {
			rd := htmlstream.NewReader(strings.NewReader(in))
			rd.SetXMLSyntax(xmlSyntax)
			rd.SetScripting(scripting)
			var warnings []string
			opt := hooks(rd)
			opt.Warn = func(w polyglot.Warning) {
				warnings = append(warnings, fmt.Sprintf("%d: %s", w.Pos, w.Msg))
			}
			var b bytes.Buffer
			pw := polyglot.NewWriter(&b, opt)
			for rd.Next() {
				if err := pw.WriteToken(rd.Token()); err != nil {
					return "", warnings, err
				}
			}
			if err := rd.Err(); err != nil {
				return "", warnings, err
			}
			err := pw.Close()
			return b.String(), warnings, err
		}
		plain, _, err := convert(func(*htmlstream.Reader) polyglot.Options { return polyglot.Options{} })
		if err != nil {
			return
		}
		for pass := range 3 {
			explicit, holds := pass == 1, pass == 2
			hooked, _, err := convert(func(*htmlstream.Reader) polyglot.Options {
				c := &hookChecker{t: t, in: in, mode: mode, ids: map[int]bool{}}
				h := &holdOps{c: c, rnd: rand.New(rand.NewPCG(uint64(mode), uint64(len(in))))}
				return polyglot.Options{
					OnTag: func(e *polyglot.Tag) {
						c.tag(e)
						if explicit {
							e.InsertBefore(nil)
							e.InsertAfter(nil)
						}
						if holds {
							h.step(e.Writer())
						}
					},
					OnText: func(x *polyglot.Text) {
						c.text(x)
						if explicit {
							x.SetText(x.Data)
						}
						if holds {
							h.step(x.Writer())
						}
					},
				}
			})
			if err != nil {
				t.Fatalf("%q, mode %#x: with hooks: %v", in, mode, err)
			}
			if hooked != plain {
				t.Fatalf("%q, mode %#x: output differs with pass-through hooks (explicit %v, holds %v):\n%s\n%s", in, mode, explicit, holds, plain, hooked)
			}
		}
		if edits == 0 {
			return
		}
		var ed *editingHooks
		out, warnings, err := convert(func(rd *htmlstream.Reader) polyglot.Options {
			ed = &editingHooks{rd: rd, rnd: rand.New(rand.NewPCG(uint64(edits), uint64(len(in)))), c: &hookChecker{t: t, in: in, mode: mode, ids: map[int]bool{}}}
			return polyglot.Options{OnTag: ed.tag, OnText: ed.text}
		})
		if err != nil {
			if ed.refused(err) {
				return
			}
			t.Fatalf("%q, mode %#x: with edits: %v", in, mode, err)
		}
		checkPolyglot(t, in, "", out, warnings, scripting, false)

		// renames only: the HTML DOM is the one of the output without them,
		// with the names changed
		renamed, warnings, err := convert(func(*htmlstream.Reader) polyglot.Options {
			rnd := rand.New(rand.NewPCG(uint64(edits), uint64(len(in))))
			return polyglot.Options{OnTag: func(e *polyglot.Tag) { renameRandom(e, rnd) }}
		})
		if err != nil {
			t.Fatalf("%q, mode %#x: with renames: %v", in, mode, err)
		}
		checkPolyglot(t, in, "", renamed, warnings, scripting, false)
		if identicalFormatting(plain) {
			return // renames can change the Noah's Ark clause (see polyglot.Tag.Rename)
		}
		if want, got := renameCanonical(htmlDOMScripting(t, plain, scripting, false)), renameCanonical(htmlDOMScripting(t, renamed, scripting, false)); got != want {
			t.Fatalf("%q, mode %#x: HTML DOM differs with renames:\noutput: %s\nwith renames: %s\nDOM (without -, with +):\n%s", in, mode, plain, renamed, domDiff(want, got))
		}
	})
}

// renameGroups are the groups of HTML elements polyglot.Tag.Rename renames
// to each other (see its documentation).
var renameGroups = [][]string{
	{"h1", "h2", "h3", "h4", "h5", "h6"},
	{"pre", "listing"},
	{"article", "aside", "details", "dir", "fieldset", "figcaption", "figure", "footer", "header", "hgroup", "main", "nav", "section", "summary"},
	{"blockquote", "center", "dl", "menu"},
	{"ol", "ul"},
	{"applet", "marquee", "object"},
	{"b", "big", "code", "em", "i", "s", "small", "strike", "strong", "tt", "u"},
	{"span", "sub", "sup", "var"},
	{"abbr", "acronym", "bdi", "bdo", "cite", "data", "del", "dfn", "ins", "kbd", "mark", "q", "samp", "time"},
	{"area", "wbr"},
	{"embed", "img"},
	{"param", "source", "track"},
}

// renameGroupOf returns the group of an HTML element name, or nil.
func renameGroupOf(name string) []string {
	for _, g := range renameGroups {
		if slices.Contains(g, name) {
			return g
		}
	}
	return nil
}

// renameCanonical replaces the names of the HTML elements in a DOM dump (see
// domElem) with the first name of their rename group.
func renameCanonical(dom string) string {
	return renameElemRE.ReplaceAllStringFunc(dom, func(m string) string {
		sm := renameElemRE.FindStringSubmatch(m)
		if g := renameGroupOf(sm[2]); g != nil {
			return sm[1] + g[0] + " "
		}
		return m
	})
}

var renameElemRE = regexp.MustCompile(`(?m)^( *<\{` + regexp.QuoteMeta(htmlstream.NamespaceXHTML) + `\})([a-z0-9]+) `)

// renameRandom renames half of the start tags of HTML elements in the rename
// groups it is called for to a random element of their group.
func renameRandom(e *polyglot.Tag, rnd *rand.Rand) {
	if !e.Start || e.Reopened || e.NS != htmlstream.NamespaceXHTML {
		return
	}
	if g := renameGroupOf(e.Name); g != nil && rnd.IntN(2) == 0 {
		e.Rename(g[rnd.IntN(len(g))])
	}
}

// holdOps holds back the output at random hook events, up to twice in one
// event, and ends the innermost hold at random events, also in the event
// which started it, with HoldRelease or with HoldDiscard of the held output,
// which leave the output unchanged.
type holdOps struct {
	c   *hookChecker
	rnd *rand.Rand
	hs  []polyglot.Hold // open holds, innermost last
}

func (h *holdOps) step(pw *polyglot.Writer) {
	for range h.rnd.IntN(3) {
		n := len(h.hs)
		if n == 0 || h.rnd.IntN(2) == 0 {
			h.hs = append(h.hs, pw.Hold())
			continue
		}
		var err error
		if top := h.hs[n-1]; h.rnd.IntN(2) == 0 {
			err = pw.HoldRelease(top)
		} else {
			err = pw.HoldDiscard(top, pw.Held(top))
		}
		if err != nil {
			h.c.t.Fatalf("%q, mode %#x: ending a hold: %v", h.c.in, h.c.mode, err)
		}
		h.hs = h.hs[:n-1]
	}
	if pw.Holding() != len(h.hs) {
		h.c.t.Fatalf("%q, mode %#x: %d holds, want %d", h.c.in, h.c.mode, pw.Holding(), len(h.hs))
	}
}

// hookChecker checks the invariants of the hook events (see
// FuzzPolyglotHooks).
type hookChecker struct {
	t        *testing.T
	in       string
	mode     uint8
	open     []polyglot.Tag // start tags without their end tag
	textOpen bool
	maxID    int
	ids      map[int]bool // IDs of the start tags seen
}

func (c *hookChecker) tag(e *polyglot.Tag) {
	t, in, mode := c.t, c.in, c.mode
	if c.textOpen {
		t.Fatalf("%q, mode %#x: tag %+v during a text node", in, mode, e)
	}
	if depth := len(c.open); e.Depth != depth && (e.Start || e.Depth != depth-1) {
		t.Fatalf("%q, mode %#x: tag %+v at depth %d, want %d", in, mode, e, e.Depth, depth)
	}
	if e.Start {
		switch {
		case e.Reopened && !c.ids[e.ID]:
			t.Fatalf("%q, mode %#x: reopened %+v without an earlier start tag with its ID", in, mode, e)
		case !e.Reopened && e.ID <= c.maxID:
			t.Fatalf("%q, mode %#x: start tag %+v with ID %d after %d", in, mode, e, e.ID, c.maxID)
		case !e.Reopened:
			c.maxID = e.ID
			c.ids[e.ID] = true
		}
		c.open = append(c.open, *e)
	} else if n := len(c.open); n == 0 {
		t.Fatalf("%q, mode %#x: end tag %+v without a start tag", in, mode, e)
	} else {
		if s := c.open[n-1]; s.Name != e.Name || s.Local != e.Local || s.NS != e.NS || s.Reopened != e.Reopened || s.Void != e.Void || s.ID != e.ID {
			t.Fatalf("%q, mode %#x: end tag %+v doesn't match the start tag %+v", in, mode, e, s)
		}
		c.open = c.open[:n-1]
	}
	if e.Name == "" || e.Local == "" || e.NS == "" || e.ID == 0 {
		t.Fatalf("%q, mode %#x: tag without name, namespace or ID: %+v", in, mode, e)
	}
	if e.Token != nil && (!e.Start || e.Reopened || e.Token.Kind() != htmlstream.StartTag) {
		t.Fatalf("%q, mode %#x: tag %+v with the token %s", in, mode, e, e.Token)
	}
}

func (c *hookChecker) text(x *polyglot.Text) {
	t, in, mode := c.t, c.in, c.mode
	if x.First == c.textOpen {
		t.Fatalf("%q, mode %#x: text %+v: First %v while text open %v", in, mode, x, x.First, c.textOpen)
	}
	c.textOpen = !x.Last
	if x.Last != (x.Token == nil) || x.Last && len(x.Data) != 0 {
		t.Fatalf("%q, mode %#x: text %+v: Last without data and token, or data or token with Last", in, mode, x)
	}
	if n := len(c.open); n == 0 || x.Depth != n || x.ID != c.open[n-1].ID {
		t.Fatalf("%q, mode %#x: text %+v in %d open elements, the innermost %+v", in, mode, x, n, c.open[max(n-1, 0):])
	}
}

// editingHooks are hooks applying random edits which keep the output
// polyglot markup by the documented contract: elements renamed (see
// renameRandom), attributes set to DOM-neutral
// names (class, id, data-*, title, role, style, lang) with arbitrary values,
// attributes of the input removed (except those which change parsing: xmlns
// and prefixed names, type on input, script and style, encoding on
// annotation-xml, and charset, http-equiv and content on meta), elements of
// the input removed (except html, head and body, and the first child element
// of a template, which Remove refuses), elements unwrapped where
// their contents parse the same without them (see unwrapSound; reopened
// copies, foreign content and elements declaring prefixes are left alone),
// comments inserted around tags outside elements whose contents are text
// for HTML parsers, and text changed (letters upper-cased, which keeps
// whitespace-only text whitespace-only, or a comment inserted before it).
type editingHooks struct {
	rd  *htmlstream.Reader
	rnd *rand.Rand
	c   *hookChecker
	// Remove and Unwrap were called, which the conversion may refuse (see
	// refused)
	removed, unwrapped bool
}

// refused reports whether the conversion error err is the refusal of an edit
// the hooks don't rule out: an Unwrap in foreign content (the parent in the
// output may differ from the input's, e.g., for an element in foreign content
// whose integration point was dropped), or a Remove of a template's first
// child.
func (ed *editingHooks) refused(err error) bool {
	msg := err.Error()
	return ed.removed && strings.Contains(msg, "invalid operation: removal of") && strings.Contains(msg, "first child element of a template") ||
		ed.unwrapped && strings.Contains(msg, "invalid operation: unwrap of")
}

// textish are the elements whose contents are text for HTML parsers (with
// scripting enabled for noscript), where markup isn't inserted.
var textish = map[string]bool{"title": true, "textarea": true, "script": true, "style": true, "xmp": true, "iframe": true, "noembed": true, "noframes": true, "plaintext": true, "noscript": true, "pre": true, "listing": true}

// inTextish reports whether a tag event is inside an element whose contents
// are text for HTML parsers (or in pre and listing, where a leading newline
// matters).
func (ed *editingHooks) inTextish(e *polyglot.Tag) bool {
	for _, o := range ed.c.open {
		if o.NS == htmlstream.NamespaceXHTML && textish[o.Name] {
			return true
		}
	}
	return e != nil && e.NS == htmlstream.NamespaceXHTML && textish[e.Name]
}

var neutralAttrs = []string{"class", "id", "data-x", "title", "role", "style", "lang"}

func (ed *editingHooks) tag(e *polyglot.Tag) {
	// the checker's stack still has the enclosing elements for an end tag
	textish := ed.inTextish(e)
	ed.c.tag(e)
	n := ed.rnd.IntN(100)
	if !textish && n < 10 {
		e.InsertBefore([]byte("<!--b-->"))
	}
	if !textish && n >= 90 {
		e.InsertAfter([]byte("<!--a-->"))
	}
	if !e.Start || e.Reopened {
		return
	}
	if ed.rnd.IntN(4) == 0 {
		renameRandom(e, ed.rnd)
		// the end tag event has the name
		o := &ed.c.open[len(ed.c.open)-1]
		o.Name, o.Local = e.Name, e.Local
	}
	switch n := ed.rnd.IntN(100); {
	case n < 40:
	case n < 60:
		// the charset meta element can't be edited
		if e.Token == nil && e.Name == "meta" {
			return
		}
		name := neutralAttrs[ed.rnd.IntN(len(neutralAttrs))]
		value := []string{"", "x", `a "b" <c> & d`, " ", "\t\n", "é"}[ed.rnd.IntN(6)]
		e.SetAttr(name, value)
	case n < 70:
		if e.Token == nil {
			return
		}
		var names []string
		for a := range e.Token.Attrs() {
			if name := removableAttr(e, a); name != "" {
				names = append(names, name)
			}
		}
		if len(names) == 0 {
			return
		}
		name := names[ed.rnd.IntN(len(names))]
		e.RemoveAttr(name)
	case n < 85:
		if e.Token == nil || e.NS == htmlstream.NamespaceXHTML && (e.Name == "html" || e.Name == "head" || e.Name == "body") {
			return
		}
		e.Remove()
		ed.removed = true
	default:
		if e.Token == nil || e.NS != htmlstream.NamespaceXHTML || !unwrapSound(ed.rd, ed.c.in) {
			return
		}
		for a := range e.Token.Attrs() {
			if n := strings.ToLower(string(a.Name())); strings.HasPrefix(n, "xmlns:") || strings.HasPrefix(n, "xlink:") {
				return // the element declares prefixes
			}
		}
		e.Unwrap()
		ed.unwrapped = true
	}
}

// removableAttr returns the name of the attribute as written if it can be
// removed without changing how the document is parsed (see editingHooks), or
// an empty string.
func removableAttr(e *polyglot.Tag, a htmlstream.Attr) string {
	name := strings.ToLower(string(a.Name()))
	for i := 0; i < len(name); i++ {
		if c := name[i]; !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' && i > 0 || c == '-' && i > 0) {
			return ""
		}
	}
	switch e.NS {
	case htmlstream.NamespaceSVG:
		name = htmlstream.AdjustSVGAttrName(name)
	case htmlstream.NamespaceMathML:
		name = htmlstream.AdjustMathMLAttrName(name)
	}
	switch {
	case name == "xmlns":
		return ""
	case name == "type" && (e.Name == "input" || e.Name == "script" || e.Name == "style"):
		return ""
	case name == "encoding" && e.Name == "annotation-xml":
		return ""
	case e.Name == "meta" && (name == "charset" || name == "http-equiv" || name == "content"):
		return ""
	}
	return name
}

func (ed *editingHooks) text(x *polyglot.Text) {
	ed.c.text(x)
	if x.Raw || x.Last {
		return
	}
	switch n := ed.rnd.IntN(100); {
	case n < 50:
	case n < 80:
		if !ascii.IsBlank(x.Data) {
			x.SetText(bytes.ToUpper(x.Data))
		}
	case !ed.inTextish(nil):
		x.SetMarkup(polyglot.AppendEscapeText([]byte("<!--t-->"), x.Data))
	}
}

// identicalFormatting reports whether the output s has more than three
// identical start tags (the same name and attributes, as written) of
// formatting elements, for which HTML parsers apply the Noah's Ark clause.
// Renaming some of them can then change the elements HTML parsers reopen
// (see polyglot.Tag.Rename). It counts all of them, open or not, so it may
// report true in other cases too.
func identicalFormatting(s string) bool {
	r := htmlstream.NewReader(strings.NewReader(s))
	count := map[string]int{}
	for r.Next() {
		tok := r.Token()
		if tok.Kind() != htmlstream.StartTag || !htmlutil.IsFormatting(tok) {
			continue
		}
		if count[string(tok.Raw())]++; count[string(tok.Raw())] > 3 {
			return true
		}
	}
	return false
}
