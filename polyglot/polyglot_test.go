package polyglot

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

	"github.com/pgaskin/htmlstream"
	"github.com/pgaskin/htmlstream/htmlutil"
	"github.com/pgaskin/htmlstream/internal"
	polyglot_testdata "github.com/pgaskin/htmlstream/testdata/polyglot"
)

func TestLongTokens(t *testing.T) {
	text := strings.Repeat("a--b-\u00e9", 30000) + "-"
	for _, tc := range []struct {
		name string
		in   string
		want string // expected in the output
		not  string // not expected in the output
	}{
		{"Comment", "<!--" + text + "-->", string(htmlstream.AppendComment(nil, []byte(text))), ""},
		{"BogusComment", "<!" + text + ">", string(htmlstream.AppendComment(nil, []byte(text))), ""},
		{"XMLDeclaration", `<?xml version="1.0" ` + text + "?><p>x", "<p>x</p>", "<!--"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var b bytes.Buffer
			if err := Convert(&b, strings.NewReader(tc.in), Options{}); err != nil {
				t.Fatalf("error: %v", err)
			}
			out := b.String()
			if !strings.Contains(out, tc.want) {
				t.Errorf("output doesn't contain %.50q...", tc.want)
			}
			if tc.not != "" && strings.Contains(out, tc.not) {
				t.Errorf("output contains %q", tc.not)
			}
			if n := strings.Count(out, "<!--"); tc.not == "" && n != 1 {
				t.Errorf("output contains %d comments", n)
			}
		})
	}
}

func TestMaxRawText(t *testing.T) {
	// With XML syntax, title and textarea contents starting with a CDATA
	// section (after whitespace) are buffered like script contents, and
	// other ones are written as they are read.
	for _, tc := range []struct {
		name, pre, post string
		xmlSyntax       bool
		buffered        bool
	}{
		{"script", "", "", false, true},
		{"title", " <![CDATA[", "]]>", true, true},
		{"textarea", "\n", "", true, false},
		{"textarea", "  <![CDATA[x]]>", "", true, true},
		{"textarea", "x<![CDATA[y]]>", "", true, false},
	} {
		for _, n := range []int{3, 1 << 15} {
			contents := tc.pre + strings.Repeat("a<b;", n) + tc.post
			in := "<" + tc.name + ">" + contents + "</" + tc.name + ">"
			n := len(contents)
			convert := func(limit int) error {
				rd := htmlstream.NewReader(strings.NewReader(in))
				rd.SetXMLSyntax(tc.xmlSyntax)
				pw := NewWriter(io.Discard, Options{MaxRawText: limit})
				for rd.Next() {
					if err := pw.WriteToken(rd.Token()); err != nil {
						return err
					}
				}
				return pw.Close()
			}
			err := convert(n - 1)
			var perr *Error
			switch {
			case !tc.buffered && err != nil:
				t.Errorf("%s, %d bytes: got %v for contents over the limit, which aren't buffered", tc.name, n, err)
			case !tc.buffered:
			case !errors.As(err, &perr):
				t.Errorf("%s, %d bytes: got %v for contents over the limit, want an *Error", tc.name, n, err)
			default:
				if want := fmt.Sprintf("polyglot: error at offset %d: %s contents exceed MaxRawText (%d bytes)", len(tc.name)+2, tc.name, n-1); err.Error() != want {
					t.Errorf("%s, %d bytes: got %q, want %q", tc.name, n, err, want)
				}
			}
			if err := convert(n); err != nil {
				t.Errorf("%s, %d bytes: error for contents at the limit: %v", tc.name, n, err)
			}
		}
	}
}

// writeTokens converts the input read with rd's options with a Writer,
// passing the tokens to keep (nil keeps all of them).
func writeTokens(in string, setup func(rd *htmlstream.Reader), keep func(rd *htmlstream.Reader) bool) (string, string, error) {
	rd := htmlstream.NewReader(strings.NewReader(in))
	if setup != nil {
		setup(rd)
	}
	var warnings strings.Builder
	var b bytes.Buffer
	pw := NewWriter(&b, Options{
		DoctypeMode: NoDoctype,
		Warn:        func(w Warning) { fmt.Fprintf(&warnings, "%d: %s\n", w.Pos, w.Msg) },
	})
	for rd.Next() {
		if keep != nil && !keep(rd) {
			continue
		}
		if err := pw.WriteToken(rd.Token()); err != nil {
			return b.String(), warnings.String(), err
		}
	}
	if err := rd.Err(); err != nil {
		return "", "", err
	}
	err := pw.Close()
	if n := b.Len(); pw.Close() != err || b.Len() != n {
		return b.String(), warnings.String(), fmt.Errorf("second Close: other result or more output than the first (%v)", err)
	}
	return b.String(), warnings.String(), err
}

func TestXMLSyntaxCDATASpace(t *testing.T) {
	// The leading whitespace of a CDATA section read as text before the body
	// is written like that of the same text: dropped before the html
	// element, and in the head (or html element) before the body starts.
	// The third string is the warnings given for the CDATA section.
	for _, tc := range [][3]string{
		{"<![CDATA[ 00", " 00", "0: read the rest of the input as a CDATA section (HTML parsers would have ended it at the first >)\n"},
		{"<![CDATA[ 00]]>", " 00"},
		{"<html><![CDATA[ x]]>", "<html> x"},
		{"<head><![CDATA[ x]]>", "<head> x"},
		{"<head></head><![CDATA[ x]]>", "<head></head> x"},
		{"<head><title>t</title><![CDATA[\n x]]>", "<head><title>t</title>\n x"},
		{"<!--c--><![CDATA[ x]]>", "<!--c--> x"},
		{"<head><![CDATA[  ]]>", "<head>  "},
		{"<p><![CDATA[ x]]>", "<p> x"},
		{"<![CDATA[ ]]> <![CDATA[\n 00]]>", " \n 00"},
	} {
		want, wantWarnings, err := writeTokens(tc[1], nil, nil)
		if err != nil {
			t.Fatalf("%q: %v", tc[1], err)
		}
		wantWarnings += tc[2]
		for _, chunk := range []int{1, 2, 3, 0} {
			got, warnings, err := writeTokens(tc[0], func(rd *htmlstream.Reader) {
				rd.SetXMLSyntax(true)
				if chunk != 0 {
					rd.SetChunkSize(chunk)
				}
			}, nil)
			if err != nil || got != want || warnings != wantWarnings {
				t.Errorf("%q, chunk size %d: got %q, %q, %v\nwant %q, %q", tc[0], chunk, got, warnings, err, want, wantWarnings)
			}
		}
	}
}

func TestWriter(t *testing.T) {
	xmlSyntax := func(rd *htmlstream.Reader) { rd.SetXMLSyntax(true) }
	// removes the elements matching m, and the end tags of the elements it
	// unwraps
	removeIf := func(m htmlstream.Matcher, unwrap bool) func(rd *htmlstream.Reader) bool {
		var removed htmlutil.Stack[struct{}]
		return func(rd *htmlstream.Reader) bool {
			tok := rd.Token()
			switch tok.Kind() {
			case htmlstream.StartTag:
				if m(tok) {
					if unwrap {
						removed.Push(tok, struct{}{})
					} else {
						rd.Skip()
					}
					return false
				}
			case htmlstream.EndTag:
				if _, ok := removed.Pop(tok); ok {
					return false
				}
			}
			return true
		}
	}
	for _, tc := range []struct {
		name     string
		in       string
		setup    func(rd *htmlstream.Reader)
		keep     func(rd *htmlstream.Reader) bool
		out      string
		warnings string
	}{
		{"Plain", `<p>a<b>b<p>c`, nil, nil,
			`<html xmlns="http://www.w3.org/1999/xhtml"><head><meta charset="UTF-8" /></head><body><p>a<b>b</b></p><p><b>c</b></p></body></html>`, ""},
		{"XMLSyntax", `<p>a<i/>b<![CDATA[c<d]]><div/>`, xmlSyntax, nil,
			`<html xmlns="http://www.w3.org/1999/xhtml"><head><meta charset="UTF-8" /></head><body><p>a<i></i>bc&lt;d</p><div></div></body></html>`, ""},
		{"XMLSyntaxVoid", `<head><meta charset="utf-8"></meta><link rel="a"></head><p>a<br></br>b<BR>c<img src="x"></IMG></img><br>
</br>d</p>`, xmlSyntax, nil,
			"<html xmlns=\"http://www.w3.org/1999/xhtml\"><head><meta charset=\"UTF-8\" /><link rel=\"a\" /></head><body><p>a<br />b<br />c<img src=\"x\" /><br />\nd</p></body></html>", "94: dropped </img> (XML parsers would have read it as an end tag)\n"},
		{"XMLSyntaxVoidContents", `<p>a<br><!--c--></br>b<img src=x>y</img></p>`, xmlSyntax, nil,
			`<html xmlns="http://www.w3.org/1999/xhtml"><head><meta charset="UTF-8" /></head><body><p>a<br /><!--c--><br />b<img src="x" />y</p></body></html>`, "16: wrote br for </br> (XML parsers would have read it as an end tag)\n34: dropped </img> (XML parsers would have read it as an end tag)\n"},
		{"XMLSyntaxVoidUntracked", `<p><b><br></br>x<br>y</b></p>`, func(rd *htmlstream.Reader) { rd.SetXMLSyntax(true); rd.SetMaxDepth(1) }, nil,
			`<html xmlns="http://www.w3.org/1999/xhtml"><head><meta charset="UTF-8" /></head><body><p><br />x<br />y</p></body></html>`, "3: dropped b nested past the stack limits (MaxDepth or MaxStackBytes)\n"},
		{"XMLSyntaxCDATAEOF", `<p><![CDATA[a<b>c</p><p>d<svg><![CDATA[e`, func(rd *htmlstream.Reader) { rd.SetXMLSyntax(true); rd.SetChunkSize(4) }, nil,
			`<html xmlns="http://www.w3.org/1999/xhtml"><head><meta charset="UTF-8" /></head><body><p>a&lt;b&gt;c&lt;/p&gt;&lt;p&gt;d&lt;svg&gt;&lt;![CDATA[e</p></body></html>`, "3: read the rest of the input as a CDATA section (HTML parsers would have ended it at the first >)\n"},
		{"XMLSyntaxCDATAEOFForeign", `<p>a<svg><![CDATA[b<c>`, xmlSyntax, nil,
			`<html xmlns="http://www.w3.org/1999/xhtml"><head><meta charset="UTF-8" /></head><body><p>a<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink">b&lt;c&gt;</svg></p></body></html>`, ""},
		{"XMLSyntaxRCDATAChunks", "<title>  <![CDATA[a<b]]> </title><title> <![CD</title><title>abc<![CDATA[x</title></head><body><textarea>\n <![CDATA[y]]></textarea>", func(rd *htmlstream.Reader) { rd.SetXMLSyntax(true); rd.SetChunkSize(3) }, nil,
			"<html xmlns=\"http://www.w3.org/1999/xhtml\"><head><meta charset=\"UTF-8\" /><title>  a&lt;b </title><title> &lt;![CD</title><title>abc&lt;![CDATA[x</title></head><body><textarea> y</textarea></body></html>", "61: kept CDATA section markers in title as text (XML parsers would have read a CDATA section)\n105: leading newline in pre, textarea or listing can't be represented\n"},
		{"XMLSyntaxScriptMarkup", `<script>a && b &amp;&amp; c;</script><style><!-- p::before{content:"&#38;"} --></style><script>x(<![CDATA["&amp;<!--"]]>);</script><script type=application/json>{"a":"&#x26;"}</script><style>a&b;c</style>`, xmlSyntax, nil,
			"<html xmlns=\"http://www.w3.org/1999/xhtml\"><head><meta charset=\"UTF-8\" /><script>//<![CDATA[\na && b &amp;&amp; c;\n//]]></script><style>/*<![CDATA[*/\n<!-- p::before{content:\"&#38;\"} -->\n/*]]>*/</style><script>//<![CDATA[\nx(<![CDATA[\"&amp;<!--\"]]]]><![CDATA[>);\n//]]></script><script type=\"application/json\">{\"a\":\"\\u0026#x26;\"}</script><style>/*<![CDATA[*/\na&b;c\n/*]]>*/</style></head><body></body></html>", "8: kept character references in script as text (XML parsers would have decoded them) (will not warn again)\n44: kept comments in style as text (XML parsers would have read them as comments) (will not warn again)\n95: kept CDATA section markers in script as text (XML parsers would have read a CDATA section)\n"},
		{"XMLSyntaxPI", "<?xml version=\"1.0\"?><?pi a\r\nb?><p>a<?xml:namespace prefix = o ns = \"urn:o\" />b<?php echo $a->b; ?><?", xmlSyntax, nil,
			`<!--?pi a` + "\n" + `b?-->` + "\n" + `<html xmlns="http://www.w3.org/1999/xhtml"><head><meta charset="UTF-8" /></head><body><p>a<!--?xml:namespace prefix = o ns = "urn:o" /-->b<!--?php echo $a- -->b; ?&gt;<!--?--></p></body></html>`, ""},
		{"XMLSyntaxPIXMLDeclaration", "<?xml?a b?><?xml ?><?xml?><p>a<?xml version='1.0'?>b<?xml", xmlSyntax, nil,
			"<!--?xml?a b?-->\n<!--?xml?-->\n" + `<html xmlns="http://www.w3.org/1999/xhtml"><head><meta charset="UTF-8" /></head><body><p>a<!--?xml version='1.0'?-->b<!--?xml--></p></body></html>`, ""},
		{"XMLSyntaxCDATANUL", "<textarea><![CDATA[a\x00b]]></textarea>", xmlSyntax, nil,
			"<html xmlns=\"http://www.w3.org/1999/xhtml\"><head><meta charset=\"UTF-8\" /></head><body><textarea>a\uFFFDb</textarea></body></html>", "10: replaced characters XML doesn't allow\n"},
		{"XMLSyntaxCDATARawText", "<title> <![CDATA[a<b & c]]> </title><script>\n<![CDATA[\nif (a<b) x();\n]]>\n</script><style><![CDATA[p::before{content:\"&\"}]]></style><script type=\"application/ld+json\"><![CDATA[{\"a\":\"<b>\"}]]></script><script type=module> <![CDATA[a<b]]]]><![CDATA[>]]> </script></head><body><textarea><![CDATA[\nx&amp;<y\r\n]]></textarea><textarea>\n <![CDATA[a]]>\n</textarea>", xmlSyntax, nil,
			"<html xmlns=\"http://www.w3.org/1999/xhtml\"><head><meta charset=\"UTF-8\" /><title> a&lt;b &amp; c </title><script>\n//<![CDATA[\nif (a<b) x();\n//]]>\n</script><style>/*<![CDATA[*/\np::before{content:\"&\"}\n/*]]>*/</style><script type=\"application/ld+json\">{\"a\":\"\\u003cb\\u003e\"}</script><script type=\"module\"> //<![CDATA[\na<b]]]]><![CDATA[>\n//]]> </script></head><body><textarea>x&amp;amp;&lt;y\n</textarea><textarea> a\n</textarea></body></html>", "218: script containing ]]> can't be represented in XML\n282: leading newline in pre, textarea or listing can't be represented\n326: leading newline in pre, textarea or listing can't be represented\n"},
		{"XMLSyntaxCDATARawTextKept", "<title>t <![CDATA[a<b]]></title><script>a(); <![CDATA[ b<c ]]></script><style><![CDATA[a]]><![CDATA[b]]></style><script type=\"text/x-template\"><![CDATA[<b>x</b>]]></script></head><body><textarea><![CDATA[a]]><![CDATA[b]]></textarea>", xmlSyntax, nil,
			"<html xmlns=\"http://www.w3.org/1999/xhtml\"><head><meta charset=\"UTF-8\" /><title>t &lt;![CDATA[a&lt;b]]&gt;</title><script>//<![CDATA[\na(); <![CDATA[ b<c ]]]]><![CDATA[>\n//]]></script><style>/*<![CDATA[*/\n<![CDATA[a]]]]><![CDATA[><![CDATA[b]]]]><![CDATA[>\n/*]]>*/</style><script type=\"text/x-template\"><![CDATA[<b>x</b>]]></script></head><body><textarea>&lt;![CDATA[a]]&gt;&lt;![CDATA[b]]&gt;</textarea></body></html>", "7: kept CDATA section markers in title as text (XML parsers would have read a CDATA section)\n40: kept CDATA section markers in script as text (XML parsers would have read a CDATA section)\n78: kept CDATA section markers in style as text (XML parsers would have read a CDATA section)\n143: script of type \"text/x-template\" containing <, & or ]]> can't be represented in XML\n195: kept CDATA section markers in textarea as text (XML parsers would have read a CDATA section)\n"},
		{"CDATARawText", "<title><![CDATA[a<b]]></title><script><![CDATA[ b<c ]]></script>", nil, nil,
			"<html xmlns=\"http://www.w3.org/1999/xhtml\"><head><meta charset=\"UTF-8\" /><title>&lt;![CDATA[a&lt;b]]&gt;</title><script>//<![CDATA[\n<![CDATA[ b<c ]]]]><![CDATA[>\n//]]></script></head><body></body></html>", "38: script containing ]]> can't be represented in XML\n"},
		{"XMLSyntaxDoctype", `<!DOCTYPE html [<!ENTITY foo "bar">]><p>&foo;</p>`, xmlSyntax, nil,
			"<html xmlns=\"http://www.w3.org/1999/xhtml\"><head><meta charset=\"UTF-8\" /></head><body><p>&amp;foo;</p></body></html>", "0: read the doctype past the > in its internal subset (HTML parsers would have ended it there and read the rest as content)\n0: dropped the internal subset of the doctype without applying its declarations (XML parsers would have applied them, e.g., expanded the entities it declares)\n"},
		{"XMLSyntaxDoctypeLiteral", `<!DOCTYPE x SYSTEM "a>Hello`, xmlSyntax, nil,
			"<html xmlns=\"http://www.w3.org/1999/xhtml\"><head><meta charset=\"UTF-8\" /></head><body>Hello</body></html>", ""},
		{"XMLSyntaxDoctypeUnterminatedSubset", `<!DOCTYPE html [<!ENTITY a "b"><p>x</p>`, xmlSyntax, nil,
			"<html xmlns=\"http://www.w3.org/1999/xhtml\"><head><meta charset=\"UTF-8\" /></head><body><p>x</p></body></html>", ""},
		{"XMLSyntaxDoctypeEmptySubset", `<!DOCTYPE html [ ]><p>x</p>`, xmlSyntax, nil,
			"<html xmlns=\"http://www.w3.org/1999/xhtml\"><head><meta charset=\"UTF-8\" /></head><body><p>x</p></body></html>", ""},
		{"XMLSyntaxDoctypeOK", `<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.1//EN" "http://www.w3.org/TR/xhtml11/DTD/xhtml11.dtd"><p>x</p><!DOCTYPE html [ ]>`, xmlSyntax, nil,
			"<html xmlns=\"http://www.w3.org/1999/xhtml\"><head><meta charset=\"UTF-8\" /></head><body><p>x</p></body></html>", ""},
		{"XMLSyntaxPre", "<pre><![CDATA[\nx]]></pre><pre>\ny</pre><listing>&#10;z</listing><textarea>\r\nw</textarea><pre>\n\nv</pre>", xmlSyntax, nil,
			"<html xmlns=\"http://www.w3.org/1999/xhtml\"><head><meta charset=\"UTF-8\" /></head><body><pre>x</pre><pre>y</pre><listing>z</listing><textarea>w</textarea><pre>\nv</pre></body></html>", "5: leading newline in pre, textarea or listing can't be represented\n30: leading newline in pre, textarea or listing can't be represented\n47: leading newline in pre, textarea or listing can't be represented\n73: leading newline in pre, textarea or listing can't be represented\n92: leading newline in pre, textarea or listing can't be represented\n"},
		{"SelfClosingRawText", "<script src=\"a.js\"/></script><style/>p{}</style><textarea/>\n</textarea><xmp/>a</xmp>", nil, nil,
			`<html xmlns="http://www.w3.org/1999/xhtml"><head><meta charset="UTF-8" /><script src="a.js"></script><style>p{}</style></head><body><textarea></textarea><pre>a</pre></body></html>`,
			"29: read self-closing <style/> as a start tag, with the input up to </style> or the end of the input as its contents (to match HTML parsers, which ignore the slash)\n71: converted xmp to pre\n71: read self-closing <xmp/> as a start tag, with the input up to </xmp> or the end of the input as its contents (to match HTML parsers, which ignore the slash)\n"},
		{"SelfClosingRawTextXMLSyntax", `<title/><script src="a.js"/><style/><textarea/><xmp/><p>x</p>`, xmlSyntax, nil,
			`<html xmlns="http://www.w3.org/1999/xhtml"><head><meta charset="UTF-8" /><title></title><script src="a.js"></script><style></style></head><body><textarea></textarea><pre></pre><p>x</p></body></html>`,
			"47: converted xmp to pre\n"},
		{"SelfClosingNoscript", `<body><noscript/>a</noscript><noscript/><p>b</p></noscript>`, nil, nil,
			`<html xmlns="http://www.w3.org/1999/xhtml"><head><meta charset="UTF-8" /></head><body><noscript>a</noscript><noscript><p>b</p></noscript></body></html>`, ""},
		{"SelfClosingNoscriptScripting", `<body><noscript/>a</noscript>`, func(rd *htmlstream.Reader) { rd.SetScripting(true) }, nil,
			`<html xmlns="http://www.w3.org/1999/xhtml"><head><meta charset="UTF-8" /></head><body><noscript>a</noscript></body></html>`,
			"6: read self-closing <noscript/> as a start tag, with the input up to </noscript> or the end of the input as its contents (to match HTML parsers, which ignore the slash)\n"},
		{"Remove", `<p>a<span>b<i>c</i></span>d`, nil, removeIf(htmlstream.Tag("span"), false),
			`<html xmlns="http://www.w3.org/1999/xhtml"><head><meta charset="UTF-8" /></head><body><p>ad</p></body></html>`, ""},
		{"Unwrap", `<p>a<span>b<i>c</i></span>d`, nil, removeIf(htmlstream.Tag("span"), true),
			`<html xmlns="http://www.w3.org/1999/xhtml"><head><meta charset="UTF-8" /></head><body><p>ab<i>c</i>d</p></body></html>`, ""},
		{"UnwrapTable", `<table><tr><td>x</table>`, nil, removeIf(htmlstream.Tag("table"), true),
			`<html xmlns="http://www.w3.org/1999/xhtml"><head><meta charset="UTF-8" /></head><body><tr><td>x</td></tr></body></html>`,
			"7: kept tr outside a table (HTML parsers would have ignored it)\n11: kept td outside a table (HTML parsers would have ignored it)\n"},
		{"UnwrapFormatting", `<p><b>a<i>b</p>c`, nil, removeIf(htmlstream.Tag("b"), true),
			`<html xmlns="http://www.w3.org/1999/xhtml"><head><meta charset="UTF-8" /></head><body><p>a<i>b</i></p><i>c</i></body></html>`, ""},
		{"Positions", `<!--xxxxxxxxxx--><p xmlns:a="u" a:b="1">`, nil, removeIf(htmlstream.Tag("x"), false),
			"<!--xxxxxxxxxx-->\n" + `<html xmlns="http://www.w3.org/1999/xhtml"><head><meta charset="UTF-8" /></head><body><p xmlns:a="u" a:b="1"></p></body></html>`,
			`17: attribute "a:b" is in namespace "u" for XML parsers and in no namespace for HTML parsers (will not warn again for this prefix and namespace)` + "\n"},
		{"Scripting", "<noscript><p>a</p></noscript><noscript>b</noscript><body><noscript>c > d\u00a0</noscript><noscript>]]></noscript>", func(rd *htmlstream.Reader) { rd.SetScripting(true) }, nil,
			"<html xmlns=\"http://www.w3.org/1999/xhtml\"><head><meta charset=\"UTF-8\" /><noscript>&lt;p&gt;a&lt;/p&gt;</noscript><noscript>b</noscript></head><body><noscript>c > d\u00a0</noscript><noscript>]]&gt;</noscript></body></html>",
			"10: escaped raw text noscript contents containing <, & or ]]> (HTML parsers with scripting enabled will read them as raw text when parsing the output)\n" +
				"39: kept raw text noscript contents in the head (HTML parsers with scripting disabled will move them to the body when parsing the output)\n" +
				"95: escaped raw text noscript contents containing <, & or ]]> (HTML parsers with scripting enabled will read them as raw text when parsing the output)\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, warnings, err := writeTokens(tc.in, tc.setup, tc.keep)
			if err != nil {
				t.Fatal(err)
			}
			if out != tc.out {
				t.Errorf("got  %q\nwant %q", out, tc.out)
			}
			if warnings != tc.warnings {
				t.Errorf("warnings:\n%s\nwant:\n%s", warnings, tc.warnings)
			}
		})
	}
}

func TestWriterErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		mode htmlstream.Mode
		keep func(rd *htmlstream.Reader) bool
		err  string
	}{
		{"XMLMode", `<p>`, htmlstream.XML, nil, "token at 0 not read in HTML mode"},
		{"UniversalMode", `<p>x</p>`, htmlstream.Universal, nil, "token at 0 not read in HTML mode"},
		{"UniversalModeHTMLTags", `<div><p></p><br></div>`, htmlstream.Universal, nil, "token at 0 not read in HTML mode"},
		{"NoStart", `<div><span>x</span></div>`, htmlstream.HTML, func(rd *htmlstream.Reader) bool {
			return !rd.Token().Is("span") || rd.Token().Kind() != htmlstream.StartTag
		}, "end tag at 12 without its start tag (the caller left out the start tag at 5)"},
		{"NoEnd", `<div><span>x</span></div>`, htmlstream.HTML, func(rd *htmlstream.Reader) bool {
			return !rd.Token().Is("span") || rd.Token().Kind() != htmlstream.EndTag
		}, "with span open inside it"},
		{"NoEndAtClose", `<span>x</span>`, htmlstream.HTML, func(rd *htmlstream.Reader) bool {
			// the implied end tags of html and body are at the end
			return !rd.Token().Is("span") || rd.Token().Kind() != htmlstream.EndTag
		}, "Close called with span open (the caller wrote its start tag but left out its end tag)"},
		{"Partial", `<p>` + strings.Repeat("x", 10), htmlstream.HTML, func(rd *htmlstream.Reader) bool {
			rd.SetChunkSize(4)
			return !rd.Token().Continued() || rd.Token().Partial()
		}, "doesn't continue the previous one"},
		{"Continued", `<p>` + strings.Repeat("x", 10), htmlstream.HTML, func(rd *htmlstream.Reader) bool {
			rd.SetChunkSize(4)
			return rd.Token().Kind() != htmlstream.Text || rd.Token().Continued()
		}, "continues a token the caller left out"},
		{"UnwrapSVG", `<svg><g><rect/></g></svg>`, htmlstream.HTML, func(rd *htmlstream.Reader) bool {
			return !rd.Token().Is("svg")
		}, "SVG element g at 5 outside SVG content (the caller left out its root element)"},
		{"UnwrapMath", `<math><mi>x</mi></math>`, htmlstream.HTML, func(rd *htmlstream.Reader) bool {
			return !rd.Token().Is("math")
		}, "MathML element mi at 6 outside MathML content (the caller left out its root element)"},
		{"UnwrapIntegrationPoint", `<svg><foreignObject><p>x</p></foreignObject></svg>`, htmlstream.HTML, func(rd *htmlstream.Reader) bool {
			return !rd.Token().Is("foreignObject")
		}, "HTML element p at 20 in SVG content (the caller left out its integration point)"},
		{"UnwrapAnnotationXML", `<math><annotation-xml><svg></svg></annotation-xml></math>`, htmlstream.HTML, func(rd *htmlstream.Reader) bool {
			return !rd.Token().Is("annotation-xml")
		}, "svg at 22 in MathML content (the caller left out the element it was in, and HTML parsers will read it as a MathML element when parsing the output)"},
		{"UnwrapInTextIntegrationPoint", `<math><ms><c><malignmark></malignmark></c></ms></math>`, htmlstream.HTML, func(rd *htmlstream.Reader) bool {
			return !rd.Token().Is("c")
		}, "HTML element malignmark at 13 in a MathML text integration point (the caller left out the element it was in, and HTML parsers will read it as a MathML element when parsing the output)"},
		{"DroppedParent", `<math><ms><a!><malignmark></malignmark></a!></ms></math>`, htmlstream.HTML, nil, ""},
		{"SVGInMathIntegrationPoint", `<math><mi><svg></svg></mi><annotation-xml><svg></svg></annotation-xml></math><svg><desc><math></math></desc></svg>`, htmlstream.HTML, nil, ""},
		{"RemoveSVG", `<svg><g><rect/></g></svg><math><mi>x</mi></math>`, htmlstream.HTML, func(rd *htmlstream.Reader) bool {
			if tok := rd.Token(); tok.Kind() == htmlstream.StartTag && (tok.Is("svg") || tok.Is("math")) {
				rd.Skip()
				return false
			}
			return rd.Token().Kind() != htmlstream.EndTag || !rd.Token().Is("svg") && !rd.Token().Is("math")
		}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := writeTokens(tc.in, func(rd *htmlstream.Reader) { rd.SetMode(tc.mode) }, tc.keep)
			switch {
			case tc.err == "" && err != nil:
				t.Errorf("error: %v", err)
			case tc.err != "" && (!errors.Is(err, ErrInvalidOp) || !strings.Contains(err.Error(), tc.err)):
				t.Errorf("got error %v, want ErrInvalidOp with %q", err, tc.err)
			}
		})
	}

	// the same token twice
	rd := htmlstream.NewReader(strings.NewReader(`<p>a`))
	pw := NewWriter(io.Discard, Options{})
	rd.Next()
	if err := pw.WriteToken(rd.Token()); err != nil {
		t.Fatal(err)
	}
	if err := pw.WriteToken(rd.Token()); err == nil || !strings.Contains(err.Error(), "out of order") {
		t.Errorf("got error %v, want out of order", err)
	}
}

func TestXMLDecl(t *testing.T) {
	const (
		decl = `<?xml version="1.0" encoding="UTF-8"?>`
		rest = `<html xmlns="http://www.w3.org/1999/xhtml"><head><meta charset="UTF-8" /></head><body><p>x</p></body></html>`
	)
	// the charset meta element ends past byte 1024 of the output
	long := "<!--" + strings.Repeat("x", 990) + "-->"
	for _, tc := range []struct {
		name     string
		in       string
		opt      Options
		out      string
		warnings string
	}{
		{"None", `<?xml version="1.0"?><p>x`, Options{DoctypeMode: NoDoctype}, rest, ""},
		{"Add", `<p>x`, Options{XMLDecl: AddXMLDecl, DoctypeMode: NoDoctype}, decl + "\n" + rest, ""},
		{"AddReplacesInput", `<?xml version="1.0" encoding="latin1"?><p>x`, Options{XMLDecl: AddXMLDecl, DoctypeMode: NoDoctype}, decl + "\n" + rest, ""},
		{"KeepNone", `<!--c--><!DOCTYPE html><p>x`, Options{XMLDecl: KeepXMLDecl, DoctypeMode: KeepDoctype}, "<!--c-->\n<!DOCTYPE html>\n" + rest, ""},
		{"Keep", `<?xml version="1.0" encoding="latin1"?><!DOCTYPE html><p>x`, Options{XMLDecl: KeepXMLDecl, DoctypeMode: KeepDoctype}, decl + "\n<!DOCTYPE html>\n" + rest, ""},
		{"KeepAfterComment", "\n<!--c-->\n<?xml version='1.0'?><!DOCTYPE html><!--d--><p>x", Options{XMLDecl: KeepXMLDecl, DoctypeMode: KeepDoctype}, decl + "\n<!--c-->\n<!DOCTYPE html>\n<!--d-->\n" + rest, ""},
		{"KeepReplaceDoctype", `<!--c--><?xml version="1.0"?><p>x`, Options{XMLDecl: KeepXMLDecl}, decl + "\n<!DOCTYPE html>\n<!--c-->\n" + rest, ""},
		// other XML declarations are written as comments, as HTML parsers read them
		{"NoneTwice", `<?xml version="1.0"?><?xml version='1.0'?><p>x`, Options{DoctypeMode: NoDoctype}, "<!--?xml version='1.0'?-->\n" + rest, ""},
		{"NoneInBody", `<!DOCTYPE html><p>x<?xml version="1.0"?>`, Options{DoctypeMode: NoDoctype}, strings.Replace(rest, ">x<", `>x<!--?xml version="1.0"?--><`, 1), ""},
		{"KeepTwice", `<?xml version="1.0"?><?xml version='1.0'?><p>x`, Options{XMLDecl: KeepXMLDecl, DoctypeMode: NoDoctype}, decl + "\n<!--?xml version='1.0'?-->\n" + rest, ""},
		{"KeepInBody", `<!DOCTYPE html><p>x<?xml version="1.0"?>`, Options{XMLDecl: KeepXMLDecl, DoctypeMode: NoDoctype}, strings.Replace(rest, ">x<", `>x<!--?xml version="1.0"?--><`, 1), ""},
		{"KeepAfterHTML", `<html><?xml version="1.0"?><p>x`, Options{XMLDecl: KeepXMLDecl, DoctypeMode: NoDoctype}, strings.Replace(rest, "<head>", `<!--?xml version="1.0"?--><head>`, 1), ""},
		{"KeepLimit", `<!--cccccccc--><?xml version="1.0"?><p>x`, Options{XMLDecl: KeepXMLDecl, DoctypeMode: NoDoctype, MaxRawText: 8}, "<!--cccccccc-->\n" + rest, "15: dropped XML declaration (output before the root element exceeds MaxRawText)\n"},
		{"KeepAtLimit", `<!--cccc--><?xml version="1.0"?><p>x`, Options{XMLDecl: KeepXMLDecl, DoctypeMode: NoDoctype, MaxRawText: 12}, decl + "\n<!--cccc-->\n" + rest, ""},
		{"KeepLimitReplaceDoctype", `<!--cccc--><?xml version="1.0"?><p>x`, Options{XMLDecl: KeepXMLDecl, MaxRawText: 12}, decl + "\n<!DOCTYPE html>\n<!--cccc-->\n" + rest, ""},
		{"KeepLimitReplaceDoctypeExceeded", `<!--ccccc--><?xml version="1.0"?><p>x`, Options{XMLDecl: KeepXMLDecl, MaxRawText: 12}, "<!DOCTYPE html>\n<!--ccccc-->\n" + rest, "12: dropped XML declaration (output before the root element exceeds MaxRawText)\n"},
		{"KeepNegativeLimit", `<?xml version="1.0"?><p>x`, Options{XMLDecl: KeepXMLDecl, MaxRawText: -1}, decl + "\n<!DOCTYPE html>\n" + rest, ""},
		{"KeepNegativeLimitExceeded", `<!----><?xml version="1.0"?><p>x`, Options{XMLDecl: KeepXMLDecl, MaxRawText: -1}, "<!DOCTYPE html>\n<!---->\n" + rest, "7: dropped XML declaration (output before the root element exceeds MaxRawText)\n"},
		{"KeepEmpty", `<?xml version="1.0"?>`, Options{XMLDecl: KeepXMLDecl, DoctypeMode: NoDoctype}, decl + "\n" + `<html xmlns="http://www.w3.org/1999/xhtml"><head><meta charset="UTF-8" /></head><body></body></html>`, ""},
		// HTML parsers take UTF-8 from an XML declaration at the start, when
		// they don't find the meta element
		{"AddCharsetPast1024", long + `<p>x`, Options{XMLDecl: AddXMLDecl, DoctypeMode: NoDoctype}, decl + "\n" + long + "\n" + rest, ""},
		{"KeepCharsetPast1024", `<?xml version="1.0"?>` + long + `<p>x`, Options{XMLDecl: KeepXMLDecl, DoctypeMode: NoDoctype}, decl + "\n" + long + "\n" + rest, ""},
		{"KeepNoneCharsetPast1024", long + `<p>x`, Options{XMLDecl: KeepXMLDecl, DoctypeMode: NoDoctype}, long + "\n" + rest, "997: wrote the charset meta element past the first 1024 bytes (HTML parsers which aren't told the encoding will not find it before parsing the output)\n"},
		{"KeepLimitCharsetPast1024", `<?xml version="1.0"?>` + long + `<p>x`, Options{XMLDecl: KeepXMLDecl, DoctypeMode: NoDoctype, MaxRawText: 100}, long + "\n" + rest, "0: dropped XML declaration (output before the root element exceeds MaxRawText)\n1018: wrote the charset meta element past the first 1024 bytes (HTML parsers which aren't told the encoding will not find it before parsing the output)\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var warnings strings.Builder
			tc.opt.Warn = func(w Warning) { fmt.Fprintf(&warnings, "%d: %s\n", w.Pos, w.Msg) }
			var b bytes.Buffer
			if err := Convert(&b, strings.NewReader(tc.in), tc.opt); err != nil {
				t.Fatal(err)
			}
			if b.String() != tc.out {
				t.Errorf("got  %q\nwant %q", b.String(), tc.out)
			}
			if warnings.String() != tc.warnings {
				t.Errorf("warnings:\n%s\nwant:\n%s", warnings.String(), tc.warnings)
			}
			for chunk := 1; chunk <= 8; chunk++ {
				warnings.Reset()
				b.Reset()
				rd := htmlstream.NewReader(strings.NewReader(tc.in))
				rd.SetScripting(false)
				rd.SetChunkSize(chunk)
				pw := NewWriter(&b, tc.opt)
				for rd.Next() {
					if err := pw.WriteToken(rd.Token()); err != nil {
						t.Fatal(err)
					}
				}
				if err := pw.Close(); err != nil {
					t.Fatal(err)
				}
				if b.String() != tc.out || warnings.String() != tc.warnings {
					t.Errorf("chunk size %d: got %q\nwarnings:\n%s", chunk, b.String(), warnings.String())
				}
			}
		})
	}

	// a processing instruction, with XML syntax
	rd := htmlstream.NewReader(strings.NewReader(`<?xml version="1.0"?><!--c--><p>x`))
	rd.SetXMLSyntax(true)
	var b bytes.Buffer
	pw := NewWriter(&b, Options{XMLDecl: KeepXMLDecl, DoctypeMode: NoDoctype})
	for rd.Next() {
		if err := pw.WriteToken(rd.Token()); err != nil {
			t.Fatal(err)
		}
	}
	if err := pw.Close(); err != nil {
		t.Fatal(err)
	}
	if want := decl + "\n<!--c-->\n" + rest; b.String() != want {
		t.Errorf("XML syntax: got  %q\nwant %q", b.String(), want)
	}
}

// convertHooked converts in with a Writer with the options.
func convertHooked(t *testing.T, in string, opt Options) string {
	t.Helper()
	rd := htmlstream.NewReader(strings.NewReader(in))
	rd.SetXMLSyntax(true)
	rd.SetScripting(false)
	var b bytes.Buffer
	pw := NewWriter(&b, opt)
	for rd.Next() {
		if err := pw.WriteToken(rd.Token()); err != nil {
			t.Fatalf("%q: WriteToken: %v", in, err)
		}
	}
	if err := rd.Err(); err != nil {
		t.Fatalf("%q: %v", in, err)
	}
	if err := pw.Close(); err != nil {
		t.Fatalf("%q: Close: %v", in, err)
	}
	return b.String()
}

// TestHooks checks the Tag and Text hooks and the output holds: hooks which
// do nothing, or write what the conversion writes, don't change the output
// and see well-nested tags and text nodes with paired IDs (text nodes with the
// ID and Depth of the innermost open element), decorating hooks
// put their markup where documented, reopened formatting elements have the ID
// of the element, attribute edits, Unwrap and Remove change the output as
// documented and are refused where documented, held output is released or
// discarded, and the errors of the writer and the events are sticky.
func TestHooks(t *testing.T) {
	inputs := []string{
		`<!DOCTYPE html><html><head><title>t</title></head><body><p>a<b>b</b>c</p><!--x--><img src=i><br/></body></html>`,
		`<p>a<o:p> </o:p>b<meta charset=x>c<i/>d<![CDATA[e<f]]>g`,
		`<p><b>1</p><p>2</b>3<table><tr><td>4<td>5</table><ul>6`,
		`<p>x</p></p></br><svg><rect/><g>t</g></svg><math><mi>y</mi></math>`,
		`<script>a < b</script><style>p > q</style><pre>` + "\n" + `z</pre><textarea>w</textarea>`,
		`<title>no body</title>text. more`,
		`<select><option>a<option>b</select><a href=x><b>1</a>2</b>`,
		`<p xmlns:x="u"><b x:a="1">1</p><p>2</b>`,
		`<p xmlns:x="u"><x:a xmlns:x="v"><b xmlns:x="w"></b><x:c/></x:a>t</p>`,
	}
	for _, in := range inputs {
		plain := convertHooked(t, in, Options{})
		// hooks doing nothing, and hooks writing what the conversion writes
		for _, explicit := range []bool{false, true} {
			var open []Tag
			textOpen := false
			maxID := 0
			ids := map[int]bool{}
			got := convertHooked(t, in, Options{
				OnTag: func(e *Tag) {
					if textOpen {
						t.Errorf("%q: tag %+v during a text node", in, e)
					}
					if depth := len(open); e.Depth != depth && (e.Start || e.Depth != depth-1) {
						t.Errorf("%q: tag %+v at depth %d, want %d", in, e, e.Depth, depth)
					}
					if e.Start {
						switch {
						case e.Reopened && !ids[e.ID]:
							t.Errorf("%q: reopened %+v without an earlier start tag with its ID", in, e)
						case !e.Reopened && e.ID <= maxID:
							t.Errorf("%q: start tag %+v with ID %d after %d", in, e, e.ID, maxID)
						case !e.Reopened:
							maxID = e.ID
							ids[e.ID] = true
						}
						open = append(open, *e)
					} else if n := len(open); n == 0 {
						t.Errorf("%q: end tag %+v without a start tag", in, e)
					} else {
						if s := open[n-1]; s.Name != e.Name || s.Local != e.Local || s.NS != e.NS || s.Reopened != e.Reopened || s.Void != e.Void || s.ID != e.ID {
							t.Errorf("%q: end tag %+v doesn't match the start tag %+v", in, e, s)
						}
						open = open[:n-1]
					}
					if e.Name == "" || e.Local == "" || e.NS == "" || e.ID == 0 {
						t.Errorf("%q: tag without name, namespace or ID: %+v", in, e)
					}
					if strings.Contains(e.Name, ":") && (e.Name == "x:a" && e.NS != "v" || e.Name == "x:c" && e.NS != "v") {
						t.Errorf("%q: namespace of %+v", in, e)
					}
					if e.Token != nil && (!e.Start || e.Reopened || e.Token.Kind() != htmlstream.StartTag) {
						t.Errorf("%q: tag %+v with the token %s", in, e, e.Token)
					}
					if e.Writer() == nil {
						t.Errorf("%q: tag %+v without a writer", in, e)
					}
					if explicit {
						// assigning the fields changes nothing
						name, rename := e.Name, e.Start && !e.Reopened
						e.Start, e.Void, e.Reopened, e.Name, e.Local, e.NS, e.ID, e.Depth, e.Token = !e.Start, !e.Void, !e.Reopened, "x:y", "y", "", 0, 0, nil
						if rename {
							e.Rename(name)
						}
						e.InsertBefore(nil)
						e.InsertAfter(nil)
					}
				},
				OnText: func(x *Text) {
					if x.First == textOpen {
						t.Errorf("%q: text %+v: First %v while text open %v", in, x, x.First, textOpen)
					}
					textOpen = !x.Last
					if x.Last != (x.Token == nil) || x.Last && len(x.Data) != 0 {
						t.Errorf("%q: text %+v: Last without data and token, or data or token with Last", in, x)
					}
					if n := len(open); n == 0 || x.Depth != n || x.ID != open[n-1].ID {
						t.Errorf("%q: text %+v in %d open elements, the innermost %+v", in, x, n, open[max(n-1, 0):])
					}
					if explicit {
						data := x.Data
						x.Data, x.First, x.Last, x.Raw, x.Token, x.ID, x.Depth = []byte("<x>"), !x.First, !x.Last, !x.Raw, nil, 0, 0
						x.SetText(data)
					}
				},
			})
			if got != plain {
				t.Errorf("%q: output differs with pass-through hooks (explicit %v):\n%s\n%s", in, explicit, plain, got)
			}
			if len(open) != 0 || textOpen {
				t.Errorf("%q: %d tags open and text open %v at the end", in, len(open), textOpen)
			}
		}
	}

	// decorating hooks: every tag in braces, every text node in brackets,
	// script contents upper-cased (the charset meta element comes before the
	// markup inserted after the head start tag)
	in := `<!DOCTYPE html><title>t</title><p>a<o:p> </o:p>b<img src=i><b>c</p><p>d</b><script>x</script>`
	want := "<!DOCTYPE html>\n" + `{<html xmlns="http://www.w3.org/1999/xhtml">}{<head>{<meta charset="UTF-8" />}{}}{<title>}[t]{</title>}{</head>}{<body>}{<p>}[a b]{<img src="i" />}{}{<b>}[c]{</b>}{</p>}{<p>}{<b>}[d]{</b>}{<script>}X{</script>}{</p>}{</body>}{</html>}`
	raw := false
	var dst []byte
	got := convertHooked(t, in, Options{
		DoctypeMode: KeepDoctype,
		OnTag: func(e *Tag) {
			e.InsertBefore([]byte("{"))
			e.InsertAfter([]byte("}"))
		},
		OnText: func(x *Text) {
			if x.Raw {
				raw = true
				x.SetText(bytes.ToUpper(x.Data))
				return
			}
			if x.Last && raw {
				raw = false
				return
			}
			dst = dst[:0]
			if x.First {
				dst = append(dst, '[')
			}
			dst = AppendEscapeText(dst, x.Data)
			if x.Last {
				dst = append(dst, ']')
			}
			x.SetMarkup(dst)
		},
	})
	if got != want {
		t.Errorf("decorated output:\n got %s\nwant %s", got, want)
	}

	// reopened formatting elements: the events have the ID of the element,
	// and so do the text events in them
	var events []string
	convertHooked(t, `<p><b>1<i class="x" id=1>2</b>3</i>4</p><p><b>5</p><p>6</b></p>`, Options{
		OnTag: func(e *Tag) {
			if e.Name != "b" && e.Name != "i" {
				return
			}
			ev := fmt.Sprintf("</%s %d", e.Name, e.ID)
			if e.Start {
				ev = fmt.Sprintf("<%s %d", e.Name, e.ID)
				if e.Token != nil {
					ev += " " + string(e.Token.Raw())
				}
			}
			if e.Reopened {
				ev += " reopened"
			}
			events = append(events, ev)
		},
		OnText: func(x *Text) {
			// the text in a reopened element has its ID
			if !x.Last {
				events = append(events, fmt.Sprintf("%s %d %d", x.Data, x.ID, x.Depth))
			}
		},
	})
	wantEvents := []string{`<b 6 <b>`, `1 6 4`, `<i 7 <i class="x" id=1>`, `2 7 5`, `</i 7`, `</b 6`, `<i 7 reopened`, `3 7 4`, `</i 7 reopened`, `4 5 3`, `<b 9 <b>`, `5 9 4`, `</b 9`, `<b 9 reopened`, `6 9 4`, `</b 9 reopened`}
	if !slices.Equal(events, wantEvents) {
		t.Errorf("reopened element events:\n got %q\nwant %q", events, wantEvents)
	}

	// comments after the body and html end tags are written after them, and
	// the whitespace around them in the body is one text node
	events = events[:0]
	got = convertHooked(t, "<p>x</p> </body> <!--c--> </html> <!--d--> ", Options{
		DoctypeMode: NoDoctype,
		OnText: func(x *Text) {
			events = append(events, fmt.Sprintf("%q %t %t", x.Data, x.First, x.Last))
		},
	})
	wantEvents = []string{`"x" true false`, `"" false true`, `" " true false`, `" " false false`, `" " false false`, `" " false false`, `" " false false`, `"" false true`}
	if want := "<p>x</p>     </body><!--c--></html><!--d-->"; !strings.HasSuffix(got, want) || !slices.Equal(events, wantEvents) {
		t.Errorf("comments after the body: got %s, want it to end with %s, events:\n got %q\nwant %q", got, want, events, wantEvents)
	}

	// holds: the output after the body start tag is held back until the
	// first element, and the held output is discarded or kept
	for _, tc := range []struct{ in, want string }{
		{`<body> <!--c--> <div>x</div>`, `<body>[ <!--c--> ]<div>x</div></body>`},
		{`<body> <p>x</p>`, `<body>! <p>x</p></body>`},
		{`<body><p>x</p>`, `<body>!<p>x</p></body>`},
		{`<body> `, `<body>! </body>`},
	} {
		var h Hold
		got := convertHooked(t, tc.in, Options{
			DoctypeMode: NoDoctype,
			OnTag: func(e *Tag) {
				pw := e.Writer()
				switch {
				case e.Start && e.Name == "body":
					h = pw.Hold()
				case h != Hold{}:
					var err error
					if e.Name == "div" {
						err = pw.HoldDiscard(h, append(append([]byte("["), pw.Held(h)...), ']'))
					} else {
						err = pw.HoldDiscard(h, append([]byte("!"), pw.Held(h)...))
					}
					h = Hold{}
					if err != nil {
						t.Fatal(err)
					}
				}
			},
		})
		if !strings.Contains(got, tc.want) {
			t.Errorf("%q: got %s, want it to contain %s", tc.in, got, tc.want)
		}
	}

	// several holds in one Tag hook, holds ended in the hook which started
	// them, and the errors: at the start tag of each element in ops, H holds,
	// R releases the innermost hold, d discards it with "[D]", D discards it
	// with the held output in brackets, I inserts the element name in braces
	// after the tag, a digit checks Holding, and o, e, z and f release the
	// outermost hold, the last hold ended, the zero Hold and a hold of another
	// Writer. Holds started for the head start tag and the charset meta
	// element start after the meta and the markup inserted after both.
	foreign := NewWriter(io.Discard, Options{}).Hold()
	for _, tc := range []struct {
		ops       map[string]string
		want, err string
	}{
		{map[string]string{"body": "HH2", "div": "RR0"}, `<body><p>x</p><div>y</div></body>`, ""},
		{map[string]string{"body": "HH2", "div": "DD0"}, `<body>[[<p>x</p>]]<div>y</div></body>`, ""},
		{map[string]string{"body": "H", "p": "Hd1", "div": "D0"}, `<body>[<p>[D]x</p>]<div>y</div></body>`, ""},
		{map[string]string{"body": "H", "p": "HR1", "div": "D0"}, `<body>[<p>x</p>]<div>y</div></body>`, ""},
		{map[string]string{"p": "HHd1", "div": "D0"}, `<body><p>[[D]x</p>]<div>y</div></body>`, ""},
		{map[string]string{"p": "HdHd0"}, `<body><p>[D][D]x</p><div>y</div></body>`, ""},
		{map[string]string{"body": "HRe", "p": "HHde1", "div": "R0e"}, `<body><p>[D]x</p><div>y</div></body>`, ""},
		{map[string]string{"head": "IH", "meta": "IHD1", "body": "D0"}, `<head><meta charset="UTF-8" />{meta}{head}[[]</head>]<body>`, ""},
		{map[string]string{"head": "IHd0", "meta": "I"}, `<head><meta charset="UTF-8" />{meta}{head}[D]</head><body>`, ""},
		{map[string]string{"head": "H", "meta": "H2", "body": "DD0"}, `<head><meta charset="UTF-8" />[[</head>]]<body>`, ""},
		{map[string]string{"body": "HH", "div": "o"}, "", "polyglot: invalid operation: HoldRelease called with an outer Hold (a hold inside it is open)"},
		{map[string]string{"p": "z"}, "", "polyglot: invalid operation: HoldRelease called with the zero Hold"},
		{map[string]string{"p": "f"}, "", "polyglot: invalid operation: HoldRelease called with a hold created by a different Writer"},
	} {
		var hs, ended []Hold
		var opErr error
		got, err := convertHookedErr(`<body><p>x</p><div>y</div>`, Options{
			DoctypeMode: NoDoctype,
			OnTag: func(e *Tag) {
				if !e.Start {
					return
				}
				pw := e.Writer()
				for _, op := range tc.ops[e.Name] {
					var err error
					top := len(hs) - 1
					switch op {
					case 'H':
						hs = append(hs, pw.Hold())
					case 'R':
						err = pw.HoldRelease(hs[top])
					case 'd':
						err = pw.HoldDiscard(hs[top], []byte("[D]"))
					case 'D':
						err = pw.HoldDiscard(hs[top], append(append([]byte("["), pw.Held(hs[top])...), ']'))
					case 'I':
						e.InsertAfter([]byte("{" + e.Name + "}"))
					case 'e':
						if err := pw.HoldRelease(ended[len(ended)-1]); err != htmlstream.ErrHoldReleased {
							t.Errorf("%v: HoldRelease of an ended hold: got %v, want ErrHoldReleased", tc.ops, err)
						}
					case 'o':
						opErr = pw.HoldRelease(hs[0])
					case 'z':
						opErr = pw.HoldRelease(Hold{})
					case 'f':
						opErr = pw.HoldRelease(foreign)
					default:
						if n := int(op - '0'); pw.Holding() != n {
							t.Errorf("%v: Holding %d at %s, want %d", tc.ops, pw.Holding(), e.Name, n)
						}
					}
					if err != nil {
						t.Fatal(err)
					}
					if op == 'R' || op == 'd' || op == 'D' {
						ended = append(ended, hs[top])
						hs = hs[:top]
					}
				}
			},
		}, false)
		switch {
		case tc.err == "" && err != nil:
			t.Errorf("%v: %v", tc.ops, err)
		case tc.err == "" && !strings.Contains(got, tc.want):
			t.Errorf("%v: got %s, want it to contain %s", tc.ops, got, tc.want)
		case tc.err != "" && (!errors.Is(opErr, ErrInvalidOp) || opErr.Error() != tc.err || err != opErr):
			t.Errorf("%v: got %v, then %v, want %q (sticky)", tc.ops, opErr, err, tc.err)
		}
	}

	// hold errors other than ErrHoldReleased are sticky, and Hold then
	// returns the zero Hold
	pw := NewWriter(io.Discard, Options{})
	h := pw.Hold()
	if err := pw.HoldRelease(h); err != nil {
		t.Fatal(err)
	}
	if err := pw.HoldRelease(h); err != htmlstream.ErrHoldReleased {
		t.Errorf("HoldRelease of a released hold: got %v, want ErrHoldReleased", err)
	} else if err := pw.Close(); err != nil {
		t.Errorf("Close after ErrHoldReleased: %v", err)
	}
	pw = NewWriter(io.Discard, Options{})
	h = pw.Hold()
	if err := pw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := pw.HoldDiscard(h, nil); err != htmlstream.ErrHoldReleased {
		t.Errorf("HoldDiscard after Close: got %v, want ErrHoldReleased", err)
	}
	// Hold after Close returns the zero Hold
	var out bytes.Buffer
	pw = NewWriter(&out, Options{})
	if err := pw.Close(); err != nil {
		t.Fatal(err)
	}
	n := out.Len()
	if h := pw.Hold(); h != (Hold{}) || pw.Holding() != 0 {
		t.Errorf("Hold after Close: %v, %d holds", h, pw.Holding())
	} else if err := pw.HoldDiscard(h, []byte("<!--x-->")); !errors.Is(err, ErrInvalidOp) {
		t.Errorf("HoldDiscard after Close: got %v, want ErrInvalidOp", err)
	} else if out.Len() != n || pw.Written() != int64(n) || pw.Close() != nil {
		t.Error("HoldDiscard after Close: output or result changed")
	}
	pw = NewWriter(io.Discard, Options{})
	if err := pw.HoldDiscard(Hold{}, nil); err == nil {
		t.Error("HoldDiscard of the zero Hold: no error")
	} else if err2 := pw.WriteToken(nil); err2 != err {
		t.Errorf("WriteToken after a HoldDiscard error: %v, want %v", err2, err)
	} else if pw.Hold() != (Hold{}) || pw.HoldRelease(h) != err {
		t.Error("Hold or HoldRelease after an error: not the zero Hold and the error")
	}

	t.Run("Attrs", testAttrs)
	t.Run("Rename", testRename)
	t.Run("Unwrap", testUnwrap)
	t.Run("Remove", testRemove)
	t.Run("Text", testText)
	t.Run("Writer", testWriterMethods)
	t.Run("Errors", testHookErrors)
}

// convertHookedErr converts in with a Writer with the options, with XML
// syntax and scripting off, and returns the output and the first error.
func convertHookedErr(in string, opt Options, scripting bool) (string, error) {
	rd := htmlstream.NewReader(strings.NewReader(in))
	rd.SetXMLSyntax(true)
	rd.SetScripting(scripting)
	var b bytes.Buffer
	pw := NewWriter(&b, opt)
	for rd.Next() {
		if err := pw.WriteToken(rd.Token()); err != nil {
			return b.String(), err
		}
	}
	if err := rd.Err(); err != nil {
		return b.String(), err
	}
	err := pw.Close()
	return b.String(), err
}

// testAttrs checks SetAttr and RemoveAttr: values replaced in place,
// attributes added before the end of the tag, lang and xml:lang mirrored, the
// edits of a formatting element repeated by its reopened copies, and edits on
// the tags the conversion adds.
func testAttrs(t *testing.T) {
	in := `<body class="k"><p lang="en" class="a" id="x" title="t">t</p><br class="c"><svg viewBox="0 0 1 1"><rect x="1"/></svg><div xml:lang="de">d</div><p><b class="a">1</p><p>2</b></p>`
	want := "<!DOCTYPE html>\n" + `<html xmlns="http://www.w3.org/1999/xhtml" class="h"><head><meta charset="UTF-8" /></head><body class="k2"><p lang="fr" class="b c" xml:lang="fr" data-x="&quot;&lt;&amp;&gt;">t</p><br class="c" id="y" /><svg viewBox="0 0 2 2" xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink"><rect x="1" y="2"></rect></svg><div>d</div><p><b class="z">1</b></p><p><b class="z">2</b></p></body></html>`
	got, err := convertHookedErr(in, Options{
		OnTag: func(e *Tag) {
			if !e.Start || e.Reopened {
				return
			}
			switch e.Name {
			case "html":
				e.SetAttr("class", "h")
			case "body":
				e.SetAttr("class", "k2")
			case "p":
				if _, ok := e.Token.Attr("lang"); !ok {
					return
				}
				e.SetAttr("class", "b c")
				e.RemoveAttr("id")
				e.SetAttr("data-x", `"<&>`)
				e.SetAttr("lang", "fr")
				e.RemoveAttr("title")
				e.RemoveAttr("missing")
			case "br":
				e.SetAttr("id", "y")
			case "svg":
				e.SetAttr("viewBox", "0 0 2 2")
			case "rect":
				e.SetAttr("y", "2")
			case "div":
				e.RemoveAttr("xml:lang")
			case "b":
				e.SetAttr("class", "z")
			}
		},
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("edited attributes:\n got %s\nwant %s", got, want)
	}
}

// testRename checks Rename: the start and end tags and the reopened copies
// are written with the name, the later events have it, attribute edits before
// and after it apply, and renames to other groups are refused.
func testRename(t *testing.T) {
	in := `<h1 class=a>x</h1><p><span id=s>a</span><b>1<i class=c>2</b>3</i><img src=x><q/></p><ul><li>x</ul><pre>` + "\n\ny</pre><div>z</div>"
	want := `<h2 class="a" id="h">x</h2><p><sub id="s">a</sub><b>1<em class="d">2</em></b><em class="d">3</em><embed src="x" /><cite></cite></p><ol><li>x</li></ol><listing>` + "\ny</listing><div>z</div>"
	renames := map[string]string{"h1": "h2", "span": "sub", "i": "em", "img": "embed", "q": "cite", "ul": "ol", "pre": "listing", "div": "div"}
	var events []string
	got, err := convertHookedErr(in, Options{
		DoctypeMode: NoDoctype,
		OnTag: func(e *Tag) {
			switch e.Name {
			case "html", "head", "meta", "body":
				return
			}
			if e.Start && !e.Reopened {
				switch e.Name {
				case "h1":
					e.SetAttr("id", "h")
				case "i":
					e.Rename("em")
					e.SetAttr("class", "d")
				case "div":
					e.Rename("div")
				}
				if name, ok := renames[e.Name]; ok {
					e.Rename(name)
				}
				if e.Name != e.Local {
					t.Errorf("%+v: Local %q, want the name", e, e.Local)
				}
			}
			ev := "</" + e.Name
			if e.Start {
				ev = "<" + e.Name
			}
			if e.Reopened {
				ev += " reopened"
			}
			events = append(events, ev)
		},
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	if want := `<html xmlns="http://www.w3.org/1999/xhtml"><head><meta charset="UTF-8" /></head><body>` + want + `</body></html>`; got != want {
		t.Errorf("renamed elements:\n got %s\nwant %s", got, want)
	}
	wantEvents := []string{"<h2", "</h2", "<p", "<sub", "</sub", "<b", "<em", "</em", "</b", "<em reopened", "</em reopened", "<embed", "</embed", "<cite", "</cite", "</p", "<ol", "<li", "</li", "</ol", "<listing", "</listing", "<div", "</div"}
	if !slices.Equal(events, wantEvents) {
		t.Errorf("events:\n got %q\nwant %q", events, wantEvents)
	}

	for _, tc := range []struct {
		in, from, to, want string
	}{
		{`<p>x</p>`, "/p", "div", "end tag"},
		{`<p><b>1</p><p>2</b>`, "b reopened", "i", "rename of a reopened b (rename its first start tag)"},
		{`<svg><g></g></svg>`, "g", "a", "foreign element"},
		{`<span>x</span>`, "span", "div", `span to "div"`},
		{`<section>x</section>`, "section", "div", `section to "div"`},
		{`<address>x</address>`, "address", "section", `address to "section"`},
		{`<section>x</section>`, "section", "search", `section to "search"`},
		{`<a>x</a>`, "a", "b", `a to "b"`},
		{`<b>x</b>`, "b", "font", `b to "font"`},
		{`<b>x</b>`, "b", "span", `b to "span"`},
		{`<h1>x</h1>`, "h1", "h7", `h1 to "h7"`},
		{`<h1>x</h1>`, "h1", "H2", `h1 to "H2"`},
		{`<table><tr><td>x</td></tr></table>`, "td", "th", `td to "th"`},
	} {
		_, err := convertHookedErr(tc.in, Options{
			OnTag: func(e *Tag) {
				name := e.Name
				if !e.Start {
					name = "/" + name
				}
				if e.Reopened {
					name += " reopened"
				}
				if name == tc.from {
					e.Rename(tc.to)
				}
			},
		}, false)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: Rename(%q) of %s: error %v, want one containing %q", tc.in, tc.to, tc.from, err, tc.want)
		}
	}
}

// TestRenameGroups checks that the elements of each group of renameGroup
// have the same flags for the Reader's rules and the conversion's, and that
// the groups have more than one element.
func TestRenameGroups(t *testing.T) {
	const rendering = internal.Block | internal.Hidden | internal.Preformatted
	flags := map[int]internal.Elem{}
	size := map[int]int{}
	for name, g := range renameGroup {
		f := internal.HTMLElem([]byte(name)) &^ rendering
		if prev, ok := flags[g]; ok && prev != f {
			t.Errorf("group %d: %s has the flags %#x, other elements %#x", g, name, f, prev)
		}
		flags[g] = f
		size[g]++
	}
	for g, n := range size {
		if n < 2 {
			t.Errorf("group %d has %d elements", g, n)
		}
	}
}

// testUnwrap checks Unwrap: the tags are left out and the events still
// reported, and an unwrapped formatting element isn't reopened.
func testUnwrap(t *testing.T) {
	for _, tc := range []struct{ in, unwrap, want string }{
		{`<p><span class=u>a<b>b</b></span>c</p>`, "span", `<p>a<b>b</b>c</p>`},
		{`<p><b>1</p><p>2</b>`, "b", `<p>1</p><p>2</p>`},
		{`<pre>` + "\n" + `x</pre>`, "pre", "\nx"},
		{`<p>a<i/>b</p>`, "i", `<p>ab</p>`},
	} {
		var events []string
		got, err := convertHookedErr(tc.in, Options{
			DoctypeMode: NoDoctype,
			OnTag: func(e *Tag) {
				if e.Name != tc.unwrap {
					return
				}
				if e.Start {
					e.Unwrap()
					events = append(events, "<"+e.Name)
				} else {
					events = append(events, "</"+e.Name)
				}
			},
		}, false)
		if err != nil {
			t.Fatalf("%q: %v", tc.in, err)
		}
		if want := `<html xmlns="http://www.w3.org/1999/xhtml"><head><meta charset="UTF-8" /></head><body>` + tc.want + `</body></html>`; got != want {
			t.Errorf("%q: unwrapped %s:\n got %s\nwant %s", tc.in, tc.unwrap, got, want)
		}
		if want := []string{"<" + tc.unwrap, "</" + tc.unwrap}; !slices.Equal(events, want) {
			t.Errorf("%q: events %q, want %q", tc.in, events, want)
		}
	}
}

// testRemove checks Remove: the element, its contents and its end tag are
// left out with no events for the contents, markup inserted before and after
// either tag is written where the element was, text nodes around it are
// separate, formatting elements opened inside it aren't reopened after it
// and those opened before it are, and the removed output isn't held.
func testRemove(t *testing.T) {
	for _, tc := range []struct{ in, remove, want, events string }{
		{`<p>a<span class=r>x<b>y</b></span>b</p>`, "span", `<p>a^+-!b</p>`, `<p[a]<span</span[b]</p`},
		{`<p>a<img src=x>b</p>`, "img", `<p>a^+-!b</p>`, `<p[a]<img</img[b]</p`},
		{`<div class=r><b>1</div>2`, "div", `^+-!2`, `<div</div[2]`},
		{`<p><b>1</p><table class=r><tr><td>2</table>3`, "table", `<p><b>1</b></p>^+-!<b>3</b>`, `<p<b[1]</b</p<table</table<b[3]</b`},
		{`<svg><rect/><g>t</g></svg>`, "rect", `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink">^+-!<g>t</g></svg>`, `<svg<rect</rect<g[t]</g</svg`},
		{`<p>a<i/>b</p>`, "i", `<p>a^+-!b</p>`, `<p[a]<i</i[b]</p`},
		{`<p>a<script class=r>x<y</script>b</p>`, "script", `<p>a^+-!b</p>`, `<p[a]<script</script[b]</p`},
	} {
		var events strings.Builder
		got, err := convertHookedErr(tc.in, Options{
			DoctypeMode: NoDoctype,
			OnTag: func(e *Tag) {
				switch e.Name {
				case "html", "head", "body", "meta":
					return
				}
				if e.Start {
					events.WriteString("<" + e.Name)
				} else {
					events.WriteString("</" + e.Name)
				}
				if e.Name != tc.remove {
					return
				}
				if e.Start {
					e.Remove()
					e.InsertBefore([]byte("^"))
					e.InsertAfter([]byte("+"))
				} else {
					e.InsertBefore([]byte("-"))
					e.InsertAfter([]byte("!"))
				}
			},
			OnText: func(x *Text) {
				if x.First {
					events.WriteString("[")
				}
				events.Write(x.Data)
				if x.Last {
					events.WriteString("]")
				}
			},
		}, false)
		if err != nil {
			t.Fatalf("%q: %v", tc.in, err)
		}
		if want := `<html xmlns="http://www.w3.org/1999/xhtml"><head><meta charset="UTF-8" /></head><body>` + tc.want + `</body></html>`; got != want {
			t.Errorf("%q: removed %s:\n got %s\nwant %s", tc.in, tc.remove, got, want)
		}
		if events.String() != tc.events {
			t.Errorf("%q: events\n got %s\nwant %s", tc.in, events.String(), tc.events)
		}
	}

	// the removed output isn't held
	var held string
	var h Hold
	got, err := convertHookedErr(`<body><span class=r>x</span><p>y</p>`, Options{
		DoctypeMode: NoDoctype,
		OnTag: func(e *Tag) {
			pw := e.Writer()
			switch {
			case e.Start && e.Name == "body":
				h = pw.Hold()
			case e.Start && e.Name == "span":
				e.Remove()
			case e.Start && e.Name == "p":
				held = string(pw.Held(h))
				pw.HoldRelease(h)
			}
		},
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if held != "" || !strings.Contains(got, "<body><p>y</p>") {
		t.Errorf("held %q, output %s", held, got)
	}
}

// testText checks SetText and SetMarkup: escaped text, raw contents wrapped
// in a CDATA section when needed, text added at the end of a node, text
// removed, and noscript raw contents escaped when they get markup characters.
// The contents set aren't warned about, and aren't errors with Strict.
func testText(t *testing.T) {
	for _, tc := range []struct {
		in, want  string
		scripting bool
		hook      func(x *Text)
	}{
		{`<script>a</script>`, `<script>//<![CDATA[` + "\n" + `a < b` + "\n" + `//]]></script>`, false, func(x *Text) {
			if x.Raw {
				x.SetText([]byte("a < b"))
			}
		}},
		{`<p>a</p>`, `<p>a&lt;</p>`, false, func(x *Text) {
			if x.Last {
				x.SetText([]byte("<"))
			}
		}},
		{`<p>a</p>`, `<p><br /></p>`, false, func(x *Text) {
			if !x.Last {
				x.SetMarkup([]byte("<br />"))
			}
		}},
		{`<p>a<!--c-->b</p>`, `<p><!--c--></p>`, false, func(x *Text) {
			x.SetMarkup(nil)
		}},
		{`<body><noscript>a</noscript>`, `<noscript>&lt;b&gt;</noscript>`, true, func(x *Text) {
			if x.Raw {
				x.SetText([]byte("<b>"))
			}
		}},
		{`<body><noscript>a</noscript>`, `<noscript>b</noscript>`, true, func(x *Text) {
			if x.Raw {
				x.SetText([]byte("b"))
			}
		}},
		{`<script>a</script>`, `<script>//<![CDATA[` + "\n" + `a]]]]><![CDATA[>b` + "\n" + `//]]></script>`, false, func(x *Text) {
			if x.Raw {
				x.SetText([]byte("a]]>b"))
			}
		}},
		{`<style>a</style>`, `<style>/*<![CDATA[*/` + "\n" + `a]]]]><![CDATA[>b` + "\n" + `/*]]>*/</style>`, false, func(x *Text) {
			if x.Raw {
				x.SetText([]byte("a]]>b"))
			}
		}},
		{`<script type="text/x-tmpl">a</script>`, `<script type="text/x-tmpl"><div>&amp;</script>`, false, func(x *Text) {
			if x.Raw {
				x.SetText([]byte("<div>&amp;"))
			}
		}},
	} {
		var warnings []string
		got, err := convertHookedErr(tc.in, Options{DoctypeMode: NoDoctype, OnText: tc.hook, Strict: true, Warn: func(w Warning) {
			warnings = append(warnings, w.Msg)
		}}, tc.scripting)
		if err != nil {
			t.Fatalf("%q: %v", tc.in, err)
		}
		if !strings.Contains(got, tc.want) {
			t.Errorf("%q: got %s, want it to contain %s", tc.in, got, tc.want)
		}
		if len(warnings) != 0 {
			t.Errorf("%q: warnings %q", tc.in, warnings)
		}
	}
	// the input contents are still warned about, and errors with Strict
	for _, strict := range []bool{false, true} {
		var warnings []string
		_, err := convertHookedErr(`<script type="text/x-tmpl"><div></script><style>a]]>b</style>`, Options{Strict: strict, Warn: func(w Warning) {
			warnings = append(warnings, w.Msg)
		}}, false)
		if strict && (!strings.Contains(fmt.Sprint(err), "can't be represented in XML") || len(warnings) != 0) {
			t.Errorf("strict: got %v, warnings %q", err, warnings)
		}
		if !strict && (err != nil || len(warnings) != 2) {
			t.Errorf("got %v, warnings %q", err, warnings)
		}
	}
}

// testWriterMethods checks Fail, MaxHold and Written.
func testWriterMethods(t *testing.T) {
	errBoom := errors.New("boom")
	_, err := convertHookedErr(`<p>x</p>`, Options{
		OnTag: func(e *Tag) {
			if e.Name == "p" {
				e.Writer().Fail(errBoom)
			}
		},
	}, false)
	if err != errBoom {
		t.Errorf("Fail: got %v, want %v", err, errBoom)
	}

	var pw *Writer
	_, err = convertHookedErr(`<p>`+strings.Repeat("x", 100)+`</p>`, Options{
		MaxHold: 50,
		OnTag: func(e *Tag) {
			if e.Start && e.Name == "body" {
				pw = e.Writer()
				pw.Hold()
			}
		},
	}, false)
	if !errors.Is(err, htmlstream.ErrHoldLimit) {
		t.Errorf("hold over the limit: got %v, want %v", err, htmlstream.ErrHoldLimit)
	} else if want := "polyglot: held output of 103 bytes exceeds MaxHold (50 bytes)"; err.Error() != want {
		t.Errorf("hold over the limit: got %q, want %q", err, want)
	}
	if err := pw.Close(); !errors.Is(err, htmlstream.ErrHoldLimit) {
		t.Errorf("Close after the hold limit: got %v", err)
	}
	// raw of HoldDiscard counts toward MaxHold only inside another hold, and
	// the error is returned by HoldDiscard
	raw := []byte("<!--" + strings.Repeat("r", 40) + "-->")
	for _, nested := range []bool{false, true} {
		var h, outer Hold
		var discardErr error
		got, err := convertHookedErr(`<p>abc</p><div>d</div>`, Options{
			MaxHold: 30,
			OnTag: func(e *Tag) {
				switch {
				case e.Start && e.Name == "body" && nested:
					outer = e.Writer().Hold()
				case e.Start && e.Name == "p":
					h = e.Writer().Hold()
				case e.Start && e.Name == "div":
					discardErr = e.Writer().HoldDiscard(h, raw)
					if discardErr == nil && nested {
						e.Writer().HoldRelease(outer)
					}
				}
			},
		}, false)
		switch {
		case !nested && (err != nil || discardErr != nil || !strings.Contains(got, `<p>`+string(raw)+`<div>`)):
			t.Errorf("HoldDiscard over MaxHold without an enclosing hold: %v, %v, %s", discardErr, err, got)
		case nested && (!errors.Is(discardErr, htmlstream.ErrHoldLimit) || err != discardErr):
			t.Errorf("HoldDiscard over MaxHold in an enclosing hold: got %v, then %v", discardErr, err)
		}
	}

	var atBody, atEnd int64
	var held int
	var h Hold
	got, err := convertHookedErr(`<body><p>x</p>`, Options{
		DoctypeMode: NoDoctype,
		OnTag: func(e *Tag) {
			pw := e.Writer()
			switch {
			case e.Start && e.Name == "body":
				atBody = pw.Written()
				h = pw.Hold()
			case !e.Start && e.Name == "body":
				held = len(pw.Held(h))
				atEnd = pw.Written()
				pw.HoldRelease(h)
			}
		},
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if i := int64(strings.Index(got, "<body>")); atBody != i {
		t.Errorf("Written at the body start tag: %d, want %d", atBody, i)
	}
	if i := int64(strings.Index(got, "</body>")); atEnd != i || held != len("<p>x</p>") {
		t.Errorf("Written at the body end tag: %d (held %d), want %d", atEnd, held, i)
	}
}

// testHookErrors checks the checks of the event methods: the errors are
// returned and sticky.
func testHookErrors(t *testing.T) {
	for _, tc := range []struct {
		name, in, want string
		fn             func(e *Tag) error
	}{
		{"SetAttr on an end tag", `<p>x</p>`, "end tag", func(e *Tag) error {
			if !e.Start && e.Name == "p" {
				e.SetAttr("class", "x")
			}
			return nil
		}},
		{"RemoveAttr on an end tag", `<p>x</p>`, "end tag", func(e *Tag) error {
			if !e.Start && e.Name == "p" {
				e.RemoveAttr("class")
			}
			return nil
		}},
		{"SetAttr on a reopened element", `<p><b>1</p><p>2</b>`, "reopened", func(e *Tag) error {
			if e.Reopened && e.Start {
				e.SetAttr("class", "x")
			}
			return nil
		}},
		{"SetAttr on the charset meta", `<p>x</p>`, "charset meta", func(e *Tag) error {
			if e.Start && e.Name == "meta" {
				e.SetAttr("class", "x")
			}
			return nil
		}},
		{"SetAttr xmlns", `<p>x</p>`, "namespace declaration", func(e *Tag) error {
			if e.Start && e.Name == "p" {
				e.SetAttr("xmlns:x", "u")
			}
			return nil
		}},
		{"SetAttr invalid name", `<p>x</p>`, "isn't an XML name", func(e *Tag) error {
			if e.Start && e.Name == "p" {
				e.SetAttr("1a", "u")
			}
			return nil
		}},
		{"SetAttr unbound prefix", `<p>x</p>`, "unbound prefix", func(e *Tag) error {
			if e.Start && e.Name == "p" {
				e.SetAttr("x:a", "u")
			}
			return nil
		}},
		{"SetAttr uppercase name", `<p>x</p>`, "HTML parsers give it", func(e *Tag) error {
			if e.Start && e.Name == "p" {
				e.SetAttr("Class", "u")
			}
			return nil
		}},
		{"SetAttr unadjusted SVG name", `<svg></svg>`, "HTML parsers give it", func(e *Tag) error {
			if e.Start && e.Name == "svg" {
				e.SetAttr("viewbox", "0 0 1 1")
			}
			return nil
		}},
		{"SetAttr invalid value", `<p>x</p>`, "attribute \"a\" set to a value with characters XML doesn't allow", func(e *Tag) error {
			if e.Start && e.Name == "p" {
				e.SetAttr("a", "\x01")
			}
			return nil
		}},
		{"Unwrap an end tag", `<p>x</p>`, "end tag", func(e *Tag) error {
			if !e.Start && e.Name == "p" {
				e.Unwrap()
			}
			return nil
		}},
		{"Unwrap a reopened element", `<p><b>1</p><p>2</b>`, "reopened", func(e *Tag) error {
			if e.Reopened && e.Start {
				e.Unwrap()
			}
			return nil
		}},
		{"Unwrap an implied tbody", `<table><tr><td>x`, "added by the conversion", func(e *Tag) error {
			if e.Start && e.Name == "tbody" {
				e.Unwrap()
			}
			return nil
		}},
		{"Unwrap the body", `<body><p>x</p>`, "unwrap of body", func(e *Tag) error {
			if e.Start && e.Name == "body" {
				e.Unwrap()
			}
			return nil
		}},
		{"Unwrap svg", `<svg><g/></svg>`, "foreign element", func(e *Tag) error {
			if e.Start && e.Name == "svg" {
				e.Unwrap()
			}
			return nil
		}},
		{"Unwrap in foreign content", `<svg><foreignObject><div><p>x</p></div></foreignObject></svg>`, "in foreign content", func(e *Tag) error {
			if e.Start && e.Name == "div" {
				e.Unwrap()
			}
			return nil
		}},
		{"Unwrap a declaring element", `<div xmlns:x="u"><x:a/></div>`, "unwrap of div declaring namespace prefixes", func(e *Tag) error {
			if e.Start && e.Name == "div" {
				e.Unwrap()
			}
			return nil
		}},
		{"Remove an end tag", `<p>x</p>`, "end tag", func(e *Tag) error {
			if !e.Start && e.Name == "p" {
				e.Remove()
				if e.Writer().Err() == nil {
					return errors.New("Writer.Err returned nil after the edit failed")
				}
			}
			return nil
		}},
		{"Remove a reopened element", `<p><b>1</p><p>2</b>`, "reopened", func(e *Tag) error {
			if e.Reopened && e.Start {
				e.Remove()
			}
			return nil
		}},
		{"Remove the br of a stray end tag", `<p>x</br></p>`, "added by the conversion", func(e *Tag) error {
			if e.Start && e.Name == "br" {
				e.Remove()
			}
			return nil
		}},
		{"Remove the first child of a template", `<template><td></td><a><td></td></a></template>`, "first child element of a template", func(e *Tag) error {
			if e.Start && e.Name == "td" {
				e.Remove()
			}
			return nil
		}},
		{"Unwrap the first child of a template", `<template><span></span><td></td></template>`, "first child element of a template", func(e *Tag) error {
			if e.Start && e.Name == "span" {
				e.Unwrap()
			}
			return nil
		}},
		{"Remove html", `<html><p>x</p>`, "removal of html", func(e *Tag) error {
			if e.Start && e.Name == "html" {
				e.Remove()
			}
			return nil
		}},
		{"token written from a hook", `<p>x</p>`, "token written from a hook", func(e *Tag) error {
			return e.Writer().WriteToken(nil)
		}},
	} {
		var hookErr error // of WriteToken and Close
		_, err := convertHookedErr(tc.in, Options{
			OnTag: func(e *Tag) {
				if err := tc.fn(e); err != nil && hookErr == nil {
					hookErr = err
				}
			},
		}, false)
		if !errors.Is(err, ErrInvalidOp) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: conversion error %v, want ErrInvalidOp with %q", tc.name, err, tc.want)
		}
		if hookErr != nil && err != hookErr {
			t.Errorf("%s: conversion error %v, want the hook's %v", tc.name, err, hookErr)
		}
	}

	// methods of an event called outside its hook call fail, with a sticky
	// error
	var saved *Tag
	called := false
	_, err := convertHookedErr(`<p>x</p>`, Options{
		OnTag: func(e *Tag) {
			saved = e
		},
		OnText: func(x *Text) {
			saved.InsertBefore(nil)
			saved.InsertAfter(nil)
			called = true
		},
	}, false)
	if !called {
		t.Error("stale InsertBefore and InsertAfter not called")
	}
	if err != errHook {
		t.Errorf("stale event method: got %v, want %v", err, errHook)
	}
	// after the conversion, the next call returns the error
	saved.SetAttr("a", "b")
	if err := saved.Writer().Close(); err != errHook {
		t.Errorf("stale SetAttr after the conversion: got %v, want %v", err, errHook)
	}
	// after a successful conversion, Close and Err keep returning nil
	saved = nil
	if _, err := convertHookedErr(`<p>x</p>`, Options{
		OnTag: func(e *Tag) {
			saved = e
		},
	}, false); err != nil {
		t.Fatal(err)
	}
	pw := saved.Writer()
	if err := pw.WriteToken(nil); !errors.Is(err, ErrInvalidOp) || !strings.Contains(err.Error(), "after Close") {
		t.Errorf("WriteToken after Close: %v", err)
	}
	saved.SetAttr("a", "b")
	pw.Fail(errors.New("after Close"))
	if err := pw.Close(); err != nil {
		t.Errorf("Close after a stale SetAttr, Fail and WriteToken: %v", err)
	}
	if err := pw.Err(); err != nil {
		t.Errorf("Err after Close: %v", err)
	}
	var savedText *Text
	called = false
	_, err = convertHookedErr(`<p>x</p>`, Options{
		OnTag: func(e *Tag) {
			if savedText != nil {
				savedText.SetText(nil)
				savedText.SetMarkup(nil)
				called = true
			}
		},
		OnText: func(x *Text) {
			savedText = x
		},
	}, false)
	if !called {
		t.Error("stale SetText and SetMarkup not called")
	}
	if err != errHook {
		t.Errorf("stale text event method: got %v, want %v", err, errHook)
	}
	_, err = convertHookedErr(`<script>x</script>`, Options{
		OnText: func(x *Text) {
			x.SetMarkup([]byte("y"))
		},
	}, false)
	if !errors.Is(err, ErrInvalidOp) || !strings.Contains(err.Error(), "markup in raw text") {
		t.Errorf("SetMarkup on raw text: %v", err)
	}
}

func TestFailOnWarning(t *testing.T) {
	// Options.Strict doesn't make warnings errors, but Warn can.
	for _, in := range []string{"<p>caf\xe9</p><p>x", "<span><div>x</span>y", "<p>x\x01"} {
		var pw *Writer
		pw = NewWriter(io.Discard, Options{Strict: true, Warn: func(w Warning) { pw.Fail(errors.New(w.Msg)) }})
		rd := htmlstream.NewReader(strings.NewReader(in))
		var err error
		for rd.Next() && err == nil {
			err = pw.WriteToken(rd.Token())
		}
		if err == nil {
			err = pw.Close()
		}
		if err == nil {
			t.Errorf("%q: no error", in)
		}
	}
}

func TestManyAttrs(t *testing.T) {
	// Converting a tag takes time linear in its attributes and namespace
	// declarations.
	const n = 100000
	list := func(format string) string {
		var b strings.Builder
		for i := range n {
			fmt.Fprintf(&b, format, i)
		}
		return b.String()
	}
	attrs, decls, prefixed := list(" a%d"), list(" xmlns:p%[1]d='u%[1]d'"), list(" p%d:a=''")
	for _, tc := range []struct {
		name, in string
		count    string // expected times n in the output
		times    int
		warnings int // times n (one for each prefix, see warnNS)
	}{
		{"Attrs", "<p" + attrs + " a0>x", `=""`, 1, 0},
		// b is reopened after the end tag of p
		{"Formatting", "<p><b" + attrs + " a0>x</p>y", `=""`, 2, 0},
		{"Decls", "<p" + decls + prefixed + " xmlns:p0='dup'>x", `="u`, 1, 1},
		{"OuterDecls", "<div" + decls + "><p" + prefixed + ">x", `:a=""`, 1, 1},
		// b is closed by the end tag of div, and reopened in p, outside the
		// declarations, so it declares the prefixes of its attributes
		{"Reopened", "<div" + decls + "><b" + prefixed + ">x</div><p>y", ` xmlns:p`, 2, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var b strings.Builder
			warnings := 0
			opt := Options{Warn: func(w Warning) { warnings++ }}
			if err := Convert(&b, strings.NewReader(tc.in), opt); err != nil {
				t.Fatal(err)
			}
			out := b.String()
			if got := strings.Count(out, tc.count); got != tc.times*n {
				t.Errorf("%q in the output %d times, want %d", tc.count, got, tc.times*n)
			}
			if warnings != tc.warnings*n {
				t.Errorf("%d warnings, want %d", warnings, tc.warnings*n)
			}
		})
	}
}

// TestAllocs checks that text and attribute values are escaped into a buffer
// reused across tokens: the allocations for a document don't depend on
// whether an earlier token (here, a long comment) grew it.
func TestAllocs(t *testing.T) {
	const n = 200
	for _, tc := range []struct {
		name, body string
	}{
		{"Text", strings.Repeat("a<!---->", n)},
		{"Attrs", strings.Repeat(`<br class="v">`, n)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			allocs := func(in string) float64 {
				return testing.AllocsPerRun(5, func() {
					if err := Convert(io.Discard, strings.NewReader(in), Options{}); err != nil {
						t.Fatal(err)
					}
				})
			}
			plain := allocs("<p>" + tc.body)
			grown := allocs("<!--" + strings.Repeat("x", 1<<10) + "--><p>" + tc.body)
			if plain > grown+20 {
				t.Errorf("%.0f allocations, %.0f with the buffer grown by a comment: a token allocates a new buffer", plain, grown)
			}
		})
	}
}

// allocDoc is a chapter-like EPUB content document with attributes, prefixed
// attributes and formatting elements, repeated n times in the body.
func allocDoc(n int) string {
	const para = `<p class="indent" id="p1">It is a <i>truth</i> universally <em>acknowledged</em>, that a <b class="name">single man</b> in possession of a <a href="#n1" id="r1" epub:type="noteref">good fortune</a>, must be in <span class="smallcaps" lang="fr">want</span> of a <strong>wife</strong>.<img src="../images/a.png" alt="An image" width="10" height="10"/><br/></p>
`
	return `<?xml version="1.0" encoding="utf-8"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops" lang="en" xml:lang="en">
<head>
<title>Chapter 1</title>
<link href="../css/style.css" rel="stylesheet" type="text/css"/>
<meta name="viewport" content="width=device-width"/>
</head>
<body>
<section id="chapter-1" epub:type="chapter" class="chapter">
<h2 class="title">Chapter 1</h2>
` + strings.Repeat(para, n) + `<aside id="n1" epub:type="footnote"><p>A note.</p></aside>
</section>
</body>
</html>
`
}

// TestAllocsDocuments reports the allocations of Convert for documents of
// different sizes, and checks that they don't grow with the size of the
// document: tags with attributes and formatting elements don't allocate.
func TestAllocsDocuments(t *testing.T) {
	var chapter float64
	for _, tc := range []struct {
		name string
		doc  string
	}{
		{"Small", `<!DOCTYPE html><html lang="en"><head><title>T</title></head><body><p>x</p></body></html>`},
		{"Chapter", allocDoc(20)},
		{"Big", allocDoc(500)},
	} {
		allocs := testing.AllocsPerRun(10, func() {
			if err := Convert(io.Discard, strings.NewReader(tc.doc), Options{}); err != nil {
				t.Fatal(err)
			}
		})
		t.Logf("%-8s %6d B input: %5.0f allocs", tc.name, len(tc.doc), allocs)
		switch tc.name {
		case "Chapter":
			chapter = allocs
		case "Big":
			if allocs > chapter {
				t.Errorf("the %d B document allocates %.0f times, the chapter %.0f: allocations grow with the document", len(tc.doc), allocs, chapter)
			}
		}
	}
}

// TestXML checks that the output is well-formed XML.
func TestXML(t *testing.T) {
	for _, in := range []string{
		``,
		`<title>T</title><p>Hello <b>world</b>`,
		`<p>a<p>b<ul><li>x<li>y</ul><b><i>bold-italic</b>italic</i> text`,
		`<table><tr><td>a<td>b<tr><td>c</table><table><col width=1><tr><th>h</table>`,
		`<input type=checkbox checked value='a"b' data-x=1 DATA-X=2><a href=?a=1&b=2>l</a>`,
		`<svg viewbox="0 0 10 10"><foreignobject><p>x</p></foreignobject><use xlink:href="#a"/></svg>`,
		`<script>if (a < b && c) {}</script><style>a > b { }</style><script type="application/ld+json">{"u":"a<b&c"}</script>`,
		`<!-- a -- b --><!--->x&nbsp;&copy;`,
		"<pre>\nx</pre>",
	} {
		var b bytes.Buffer
		if err := Convert(&b, iotest.OneByteReader(strings.NewReader(in)), Options{}); err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if err := checkXML(b.String()); err != nil {
			t.Errorf("%q: not well-formed: %v\n%s", in, err, b.String())
		}
	}
	var b bytes.Buffer
	Convert(&b, strings.NewReader(`<p>x`), Options{Doctype: `<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Strict//EN" "http://www.w3.org/TR/xhtml1/DTD/xhtml1-strict.dtd">`})
	if !strings.HasPrefix(b.String(), `<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Strict//EN"`) {
		t.Errorf("doctype option: %s", b.String())
	}
}

// checkXML checks that s is a well-formed XML document for a strict
// encoding/xml Decoder, with matching start and end tags, one root element,
// no text other than whitespace and no doctype after it, and that it meets
// the namespace constraints of Namespaces in XML 1.0, which encoding/xml
// doesn't check: names have at most one colon, prefixes other than xml and
// xmlns are declared and not undeclared, the xml prefix is only bound to the
// XML namespace, the xmlns prefix isn't declared, no other prefix (or the
// default namespace) is bound to the XML or XMLNS namespace, element names
// don't have the xmlns prefix, and no two attributes of an element have the
// same namespace and local name.
func checkXML(s string) error {
	d := xml.NewDecoder(strings.NewReader(s))
	d.Strict = true
	type element struct {
		name  xml.Name
		decls []xml.Attr // namespace declarations
	}
	var stack []element
	lookup := func(prefix string) (string, bool) {
		switch prefix {
		case "xml":
			return htmlstream.NamespaceXML, true
		case "xmlns":
			return htmlstream.NamespaceXMLNS, true
		}
		for i := len(stack) - 1; i >= 0; i-- {
			for _, a := range stack[i].decls {
				if a.Name.Local == prefix {
					return a.Value, true
				}
			}
		}
		return "", false
	}
	roots := 0
	for {
		tok, err := d.RawToken()
		if err == io.EOF {
			if len(stack) != 0 {
				return fmt.Errorf("unclosed element %s:%s", stack[len(stack)-1].name.Space, stack[len(stack)-1].name.Local)
			}
			if roots != 1 {
				return fmt.Errorf("%d root elements", roots)
			}
			return nil
		}
		if err != nil {
			return err
		}
		switch tok := tok.(type) {
		case xml.StartElement:
			if len(stack) == 0 {
				roots++
			}
			var decls []xml.Attr
			for _, a := range tok.Attr {
				switch {
				case a.Name.Space == "xmlns":
					switch p, v := a.Name.Local, a.Value; {
					case p == "xmlns":
						return fmt.Errorf("namespace declaration of the xmlns prefix in <%s:%s>", tok.Name.Space, tok.Name.Local)
					case p == "xml" && v != htmlstream.NamespaceXML:
						return fmt.Errorf("xml prefix bound to %q", v)
					case p != "xml" && (v == "" || v == htmlstream.NamespaceXML || v == htmlstream.NamespaceXMLNS):
						return fmt.Errorf("prefix %s bound to %q", p, v)
					}
					decls = append(decls, a)
				case a.Name.Space == "" && a.Name.Local == "xmlns":
					if a.Value == htmlstream.NamespaceXML || a.Value == htmlstream.NamespaceXMLNS {
						return fmt.Errorf("default namespace bound to %q", a.Value)
					}
				}
			}
			stack = append(stack, element{tok.Name, decls})
			if strings.Contains(tok.Name.Local, ":") {
				return fmt.Errorf("element name %s:%s with more than one colon", tok.Name.Space, tok.Name.Local)
			}
			if tok.Name.Space == "xmlns" {
				return fmt.Errorf("element name with the xmlns prefix: %s:%s", tok.Name.Space, tok.Name.Local)
			}
			if _, ok := lookup(tok.Name.Space); tok.Name.Space != "" && !ok {
				return fmt.Errorf("undeclared prefix in element name %s:%s", tok.Name.Space, tok.Name.Local)
			}
			var expanded []string
			for _, a := range tok.Attr {
				if strings.Contains(a.Name.Local, ":") {
					return fmt.Errorf("attribute name %s:%s with more than one colon", a.Name.Space, a.Name.Local)
				}
				if a.Name.Space == "" {
					continue
				}
				uri, ok := lookup(a.Name.Space)
				if !ok {
					return fmt.Errorf("undeclared prefix in attribute name %s:%s", a.Name.Space, a.Name.Local)
				}
				k := uri + " " + a.Name.Local
				if slices.Contains(expanded, k) {
					return fmt.Errorf("attribute {%s}%s repeated in <%s>", uri, a.Name.Local, tok.Name.Local)
				}
				expanded = append(expanded, k)
			}
		case xml.EndElement:
			if len(stack) == 0 || stack[len(stack)-1].name != tok.Name {
				return fmt.Errorf("unexpected end tag %s:%s", tok.Name.Space, tok.Name.Local)
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) == 0 && strings.Trim(string(tok), " \t\r\n") != "" {
				return fmt.Errorf("text outside the root element: %q", tok)
			}
		case xml.Directive:
			if roots != 0 {
				return fmt.Errorf("directive after the root element: <!%s>", tok)
			}
		}
	}
}

func TestDoctype(t *testing.T) {
	const xhtml11 = `<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.1//EN" "http://www.w3.org/TR/xhtml11/DTD/xhtml11.dtd">`
	for _, tc := range []struct {
		opt  Options
		in   string
		want string // the output up to the html start tag
		warn bool
	}{
		{Options{}, `<!-- c --><!DOCTYPE x>`, "<!DOCTYPE html>\n<!-- c -->\n", false},
		{Options{Doctype: xhtml11}, `<!DOCTYPE x>`, xhtml11 + "\n", false},
		{Options{XMLDecl: AddXMLDecl}, `<?xml version="1.0" encoding="iso-8859-1"?><p>`, "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<!DOCTYPE html>\n", false},
		{Options{DoctypeMode: NoDoctype, XMLDecl: AddXMLDecl}, `<!-- c --><?xml version="1.0"?><!DOCTYPE html>`, "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<!-- c -->\n", false},

		// KeepDoctype writes the doctype as HTML parsers read it, where it is
		{Options{DoctypeMode: KeepDoctype}, `<p>`, ``, false},
		{Options{DoctypeMode: KeepDoctype}, `<!-- c --> <!doctype HTML>`, "<!-- c -->\n<!DOCTYPE html>\n", false},
		{Options{DoctypeMode: KeepDoctype}, "<!DOCTYPE html\npublic '-//W3C//DTD XHTML 1.1//EN'\n\t'http://www.w3.org/TR/xhtml11/DTD/xhtml11.dtd'>", xhtml11 + "\n", false},
		{Options{DoctypeMode: KeepDoctype}, `<!DOCTYPE html SYSTEM 'a"b'>`, "<!DOCTYPE html SYSTEM 'a\"b'>\n", false},
		{Options{DoctypeMode: KeepDoctype}, `<!DOCTYPE html PUBLIC "-//W3C//DTD HTML 4.01//EN">`, "<!DOCTYPE html PUBLIC \"-//W3C//DTD HTML 4.01//EN\" \"\">\n", false},
		{Options{DoctypeMode: KeepDoctype}, `<!DOCTYPE html><!DOCTYPE x>`, "<!DOCTYPE html>\n", false},
		{Options{DoctypeMode: KeepDoctype}, `<p><!DOCTYPE html>`, ``, false},
		{Options{DoctypeMode: KeepDoctype}, `<!DOCTYPE "x>`, ``, true},
		{Options{DoctypeMode: KeepDoctype}, `<!DOCTYPE html PUBLIC "<x>" "">`, ``, true},
		{Options{DoctypeMode: KeepDoctype}, `<!DOCTYPE html SYSTEM "a'b>`, ``, true},
		// the quirks mode would change
		{Options{DoctypeMode: KeepDoctype}, `<!DOCTYPE html PUBLIC "-//W3C//DTD HTML 4.01 Transitional//EN">`, ``, true},
		{Options{DoctypeMode: KeepDoctype}, `<!DOCTYPE html x>`, ``, true},
		{Options{DoctypeMode: KeepDoctype}, `<!DOCTYPE html`, ``, true},
	} {
		var b bytes.Buffer
		warned := false
		tc.opt.Warn = func(Warning) { warned = true }
		if err := Convert(&b, strings.NewReader(tc.in), tc.opt); err != nil {
			t.Fatal(err)
		}
		got, _, _ := strings.Cut(b.String(), "<html")
		if got != tc.want || warned != tc.warn {
			t.Errorf("%+v %q: got %s, warning %v", tc.opt.DoctypeMode, tc.in, got, warned)
		}
	}
}

// TestQuirksTableInP checks that a table start tag doesn't close an open p
// when the input and the output are in quirks mode, and that the p stays open
// after the table like in HTML parsers.
func TestQuirksTableInP(t *testing.T) {
	const transitional = `<!DOCTYPE html PUBLIC "-//W3C//DTD HTML 4.01 Transitional//EN">`
	const limited = `<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Transitional//EN" "http://www.w3.org/TR/xhtml1/DTD/xhtml1-transitional.dtd">`
	const table = `<p>a<table><tr><td>x</table>b<div>c</div>`
	const closed = `<p>a</p><table><tbody><tr><td>x</td></tr></tbody></table>b<div>c</div>`
	const kept = `<p>a<table><tbody><tr><td>x</td></tr></tbody></table>b</p><div>c</div>`
	const warnClosed = "4: closed p before table (HTML parsers would have put the table in the p in quirks mode)"
	for _, tc := range []struct {
		opt  Options
		in   string
		want string // the body contents
		warn string
	}{
		{Options{}, table, closed, warnClosed},
		{Options{DoctypeMode: NoDoctype}, table, kept, ""},
		{Options{Doctype: transitional}, table, kept, ""},
		{Options{Doctype: limited}, table, closed, warnClosed},
		{Options{DoctypeMode: NoDoctype}, `<!DOCTYPE html>` + table, closed, ""},
		{Options{DoctypeMode: KeepDoctype}, table, kept, ""},
		// a kept doctype selecting quirks mode, and one dropped (the output
		// has no doctype)
		{Options{DoctypeMode: KeepDoctype}, `<!DOCTYPE html PUBLIC "-//W3C//DTD HTML 3.2 Final//EN">` + table, kept, ""},
		{Options{DoctypeMode: KeepDoctype}, transitional + table, kept, "0: dropped doctype (selects quirks mode in HTML parsers, and limited-quirks mode as written)"},
		// the p is closed by a start tag closing a p, a p end tag and the end
		// tag of an element around it, and kept open around void elements,
		// elements without child elements and other tables
		{Options{DoctypeMode: NoDoctype}, `<p>a<table></table>b<br><img src=i><title>t</title><table></table>c</p>d`, `<p>a<table></table>b<br /><img src="i" /><title>t</title><table></table>c</p>d`, ""},
		{Options{DoctypeMode: NoDoctype}, `<div><p>a<table></table>b</div>c<p>d<table></table><li>e`, `<div><p>a<table></table>b</p></div>c<p>d<table></table></p><li>e</li>`, ""},
		{Options{DoctypeMode: NoDoctype}, `<p>a<table><tr><td><p>b<table></table>c</table>d`, `<p>a<table><tbody><tr><td><p>b<table></table>c</p></td></tr></tbody></table>d</p>`, ""},
		{Options{DoctypeMode: NoDoctype}, `<p>a<table><tr><td><p>b<table></table>c</p></table>d</p>e`, `<p>a<table><tbody><tr><td><p>b<table></table>c</p></td></tr></tbody></table>d</p>e`, ""},
		{Options{DoctypeMode: NoDoctype}, `<p>a<table><tr><td><p>b<table></table></table>d<span>e`, `<p>a<table><tbody><tr><td><p>b<table></table></p></td></tr></tbody></table>d</p><span>e</span>`, "47: closed p at <span> after a table in it (HTML parsers would have kept the p open in quirks mode)"},
		// other elements would be in the p for HTML parsers
		{Options{DoctypeMode: NoDoctype}, `<p>a<table></table>b<span>c</span>`, `<p>a<table></table>b</p><span>c</span>`, "20: closed p at <span> after a table in it (HTML parsers would have kept the p open in quirks mode)"},
		// misplaced html, head and body start tags, which HTML parsers ignore,
		// don't change where the content after them goes
		{Options{DoctypeMode: NoDoctype}, `<p>a<table></table>b<html><span>c</span>`, `<p>a<table></table>b</p><span>c</span>`, "26: closed p at <span> after a table in it (HTML parsers would have kept the p open in quirks mode)"},
		{Options{DoctypeMode: NoDoctype}, `<p>a<table></table><html>b<br>c</p>d<p>e<table></table><body><div>f`, `<p>a<table></table>b<br />c</p>d<p>e<table></table></p><div>f</div>`, ""},
		// a noscript after a misplaced head start tag is in the p for HTML
		// parsers, which close it with the p (at the div and the p end tag),
		// and keep it open at other content
		{Options{DoctypeMode: NoDoctype}, `<p>a<table></table><head><noscript><div>b`, `<p>a<table></table></p><noscript></noscript><div>b</div>`, "25: closed p at <noscript> after a table in it (HTML parsers would have kept the p open in quirks mode)"},
		{Options{Doctype: transitional}, `<p>a<table></table><head><noscript> <meta><h1>b`, `<p>a<table></table></p><noscript> <meta /></noscript><h1>b</h1>`, "25: closed p at <noscript> after a table in it (HTML parsers would have kept the p open in quirks mode)"},
		{Options{DoctypeMode: NoDoctype}, `<p>a<table></table><head><noscript></p>b`, `<p>a<table></table></p><noscript><p></p></noscript>b`, "25: closed p at <noscript> after a table in it (HTML parsers would have kept the p open in quirks mode)"},
		{Options{DoctypeMode: NoDoctype}, `<p>a<table></table><head><noscript>b<div>c`, `<p>a<table></table></p><noscript></noscript>b<div>c</div>`, "25: closed p at <noscript> after a table in it (HTML parsers would have kept the p open in quirks mode)\n35: closed noscript after a misplaced <head> at the content after it (HTML parsers would have kept it open as body content)"},
		{Options{DoctypeMode: NoDoctype}, `<p>a<table></table><head><noscript></noscript><div>b`, `<p>a<table></table></p><noscript></noscript><div>b</div>`, "25: closed p at <noscript> after a table in it (HTML parsers would have kept the p open in quirks mode)"},
		{Options{DoctypeMode: NoDoctype}, `<span><p>a<table></table>b</span>c`, `<span><p>a<table></table>b</p></span>c`, "26: closed p at </span> (HTML parsers would have ignored the end tag)"},
		{Options{DoctypeMode: NoDoctype}, `<b><p>a<table></table>b</b>c`, `<b><p>a<table></table>b</p></b>c`, "23: closed p at </b> (HTML parsers would have kept p open and cloned b into it)"},
		// HTML parsers reopen the b in the p, like an element whose start tag
		// is after the table
		{Options{DoctypeMode: NoDoctype}, `<p><b>x<p><table></table>y<br>z`, `<p><b>x</b></p><p><table></table></p><b>y<br />z</b>`, "25: closed p before reopening b after a table in it (HTML parsers would have reopened it in the p in quirks mode)"},
		// HTML parsers put the table in the b, which the Reader closes too
		{Options{DoctypeMode: NoDoctype}, `<p><b>a<table></table>b`, `<p><b>a</b></p><table></table><b>b</b>`, "7: closed p before table (HTML parsers would have put the table in the p in quirks mode)"},
	} {
		var b bytes.Buffer
		var warnings []string
		tc.opt.Warn = func(w Warning) { warnings = append(warnings, fmt.Sprintf("%d: %s", w.Pos, w.Msg)) }
		if err := Convert(&b, strings.NewReader(tc.in), tc.opt); err != nil {
			t.Fatal(err)
		}
		_, got, _ := strings.Cut(b.String(), "<body>")
		got, _, _ = strings.Cut(got, "</body>")
		if warn := strings.Join(warnings, "\n"); got != tc.want || warn != tc.warn {
			t.Errorf("%+v %q:\n got %s\nwant %s\nwarnings:\n%s\nwant:\n%s", tc.opt, tc.in, got, tc.want, warn, tc.warn)
		}
	}
}

func TestAppend(t *testing.T) {
	const s = "a\u00a0<\"\n\u00a0"
	if got := string(AppendEscapeText(nil, s)); got != "a&#160;&lt;\"\n&#160;" {
		t.Errorf("AppendEscapeText: %q", got)
	}
	if got := string(AppendEscapeAttr(nil, []byte(s))); got != "a&#160;&lt;&quot;&#10;&#160;" {
		t.Errorf("AppendEscapeAttr: %q", got)
	}
}

func TestBogusComment(t *testing.T) {
	// Bogus comments are normalized like comments: newlines, and NUL
	// replaced with U+FFFD, as in HTML parsers (without a warning).
	var b bytes.Buffer
	warned := false
	opt := Options{Warn: func(Warning) { warned = true }}
	if err := Convert(&b, strings.NewReader("<p><!a\r\nb\x00><!--c\r\nd\x00-->"), opt); err != nil {
		t.Fatal(err)
	}
	if want := "<p><!--a\nb\ufffd--><!--c\nd\ufffd--></p>"; !strings.Contains(b.String(), want) || warned {
		t.Errorf("got %q, warning %v", b.String(), warned)
	}
}

func TestNULNames(t *testing.T) {
	// HTML parsers replace NUL in tag and attribute names with U+FFFD, which
	// isn't a name character in XML 1.0 (Fourth Edition), so the elements
	// and attributes are dropped with a warning. The end tags close the
	// elements with the names HTML parsers give them, and the second of
	// the attributes with the same name is dropped like HTML parsers do.
	for _, tc := range []struct {
		in, out  string
		warnings []string
	}{
		{"<div><p\x00 a=1>x</P\ufffd>y</div>", "<div>xy</div>", []string{"dropped element \"p\ufffd\" (can't be represented in XML)"}},
		{"<b\x00>x<b\ufffd>y</b\ufffd>z", "xyz", []string{"dropped element \"b\ufffd\"", "dropped element \"b\ufffd\""}},
		{"<div a\x00=1 A\ufffd=2 b=3>x</div>", `<div b="3">x</div>`, []string{"dropped attribute \"a\ufffd\" (can't be represented in XML)"}},
		{"<svg><g\x00 viewbox=1></G\ufffd>x</svg>", ">x</svg>", []string{"dropped element \"g\ufffd\""}},
	} {
		var b bytes.Buffer
		var warnings []string
		if err := Convert(&b, strings.NewReader(tc.in), Options{
			Warn: func(w Warning) { warnings = append(warnings, w.Msg) },
		}); err != nil {
			t.Fatalf("%q: %v", tc.in, err)
		}
		ok := len(warnings) == len(tc.warnings)
		for i := 0; ok && i < len(warnings); i++ {
			ok = strings.Contains(warnings[i], tc.warnings[i])
		}
		if !ok {
			t.Errorf("%q: warnings:\n%s\nwant:\n%s", tc.in, strings.Join(warnings, "\n"), strings.Join(tc.warnings, "\n"))
		}
		if !strings.Contains(b.String(), tc.out+"</body>") {
			t.Errorf("%q: got %s, want %s in the body", tc.in, b.String(), tc.out)
		}
	}
}

func TestLang(t *testing.T) {
	// lang and xml:lang are mirrored, including empty values (unknown
	// language), which override the inherited language.
	var b bytes.Buffer
	if err := Convert(&b, strings.NewReader(`<html lang="en"><p lang="">x</p><p xml:lang="">y</p>`), Options{}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`<html lang="en" xmlns="http://www.w3.org/1999/xhtml" xml:lang="en">`, `<p lang="" xml:lang="">x</p>`, `<p xml:lang="" lang="">y</p>`} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("no %s in %s", want, b.String())
		}
	}
}

func TestUppercaseDeclaration(t *testing.T) {
	// Declarations and prefixes are case-insensitive in HTML, and written
	// lowercased. The x attributes are in no namespace for HTML parsers
	// (one warning for the prefix).
	var b bytes.Buffer
	var warnings []string
	err := Convert(&b, strings.NewReader(`<svg XMLNS:X="u" X:a="1"><g x:b="2"/></svg>`), Options{
		Warn: func(w Warning) { warnings = append(warnings, w.Msg) },
	})
	want := `<svg xmlns:x="u" x:a="1" xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink"><g x:b="2"></g></svg>`
	if err != nil || !strings.Contains(b.String(), want) || len(warnings) != 1 {
		t.Errorf("got %v %q, warnings %q", err, b.String(), warnings)
	}
}

// TestPrefixWarnings checks the warnings for prefixed names which XML and
// HTML parsers put in different namespaces: one per kind (element or
// attribute), prefix and namespace URI, and none for the xlink and xml
// attributes HTML parsers put in a namespace on SVG and MathML elements, or
// for xml:lang on HTML elements (mirrored to lang), unless it differs from
// lang.
func TestPrefixWarnings(t *testing.T) {
	const epub = `xmlns:epub="http://www.idpf.org/2007/ops"`
	for _, tc := range []struct {
		in       string
		warnings []string // substrings of the expected warnings, in order
	}{
		{`<p xmlns:x="u" x:a="1">`, []string{`attribute "x:a" is in namespace "u" for XML parsers and in no namespace for HTML parsers`}},
		{`<html ` + epub + `><body><nav epub:type="toc"><ol><li><a epub:type="x">a</a></li></ol></nav><section epub:type="y"></section>`,
			[]string{`attribute "epub:type" is in namespace "http://www.idpf.org/2007/ops"`}},
		// declared again with another URI
		{`<html xmlns:epub="a"><body><p epub:type="t"><span epub:type="t"></span></p><div xmlns:epub="b" epub:type="t"></div><p epub:type="t">`,
			[]string{`"epub:type" is in namespace "a"`, `"epub:type" is in namespace "b"`}},
		// declared again with the same URI
		{`<div xmlns:x="u" x:a="1"></div><div xmlns:x="u" x:a="1"></div>`, []string{`"x:a"`}},
		{`<div xmlns:x="u" xmlns:y="u" x:a="1" y:a="2">`, []string{`"x:a"`, `dropped attribute "y:a" (same namespace and local name as another attribute`}},
		{`<div xmlns:x="u" xmlns:y="v" x:a="1" y:a="2">`, []string{`"x:a"`, `"y:a"`}},
		{`<p xmlns:x="u" X:A="1">`, []string{`"x:a"`}},
		// mapped by HTML parsers on foreign elements
		{`<svg xml:lang="en" xml:space="preserve"><use xlink:href="#a" xlink:title="t"/><a xlink:href="b" xlink:show="new" xlink:actuate="onRequest" xlink:type="simple" xlink:role="r" xlink:arcrole="r"></a></svg>`, nil},
		{`<math xml:lang="en"><mi xlink:href="#a">x</mi></math>`, nil},
		{`<svg xmlns:xlink="http://www.w3.org/1999/xlink"><use xlink:href="#a"/></svg>`, nil},
		{`<p lang="en" xml:lang="EN"><span lang="fr">x</span></p>`, nil},
		// not mapped by HTML parsers
		{`<p xlink:href="#a">`, []string{`attribute "xlink:href" is in namespace "http://www.w3.org/1999/xlink" for XML parsers and in no namespace for HTML parsers`}},
		{`<svg><g xlink:foo="x"/><g xlink:bar="x"/></svg>`, []string{`"xlink:foo"`}},
		{`<svg xml:base="x"></svg>`, []string{`"xml:base" is in namespace "http://www.w3.org/XML/1998/namespace"`}},
		{`<p xml:space="preserve">`, []string{`"xml:space"`}},
		{`<svg xmlns:xlink="u"><use xlink:href="#a"/></svg>`, []string{`attribute "xlink:href" is in namespace "u" for XML parsers and in namespace "http://www.w3.org/1999/xlink" for HTML parsers`}},
		// xml:lang without lang is mirrored to lang, and xml:lang with
		// another value is replaced
		{`<p xml:lang="fr">x</p>`, nil},
		{`<p xml:lang="fr">x</p><p lang="en" xml:lang="fr">y</p><p lang="de" xml:lang="fr">z</p>`, []string{`wrote xml:lang differing from lang`}},
		// elements
		{`<div xmlns:o="urn:o"><p>a<o:p></o:p></p><p>b<o:p></o:p></p></div>`, []string{`element "o:p" is in namespace "urn:o" for XML parsers and in namespace "http://www.w3.org/1999/xhtml" for HTML parsers`}},
		{`<svg:svg xmlns:svg="http://www.w3.org/2000/svg"><svg:rect/></svg:svg>`, []string{`element "svg:svg" is in namespace "http://www.w3.org/2000/svg" for XML parsers and in namespace "http://www.w3.org/1999/xhtml"`}},
		{`<svg xmlns:x="u"><x:g x:a="1"/></svg>`, []string{`element "x:g" is in namespace "u" for XML parsers and in namespace "http://www.w3.org/2000/svg"`, `attribute "x:a"`}},
		// undeclared prefixes are dropped
		{`<p y:a="1"><y:b>x</y:b></p>`, []string{`dropped attribute "y:a"`, `dropped element "y:b"`}},
		// and so are the declarations of dropped elements
		{`<y:b xmlns:x="u"><p x:a="1">x</p></y:b>`, []string{`dropped element "y:b"`, `dropped attribute "x:a"`}},
		// namespace declarations invalid in XML are dropped, and leave the
		// prefix unbound
		{`<a xmlns:epub epub:type="x">`, []string{`dropped namespace declaration "xmlns:epub" (invalid in XML: a prefix can't be undeclared`, `dropped attribute "epub:type"`}},
		{`<p xmlns:x="u"><span xmlns:x="" x:a="1"><x:b></x:b></span><i x:c="1">`, []string{`dropped namespace declaration "xmlns:x"`, `dropped attribute "x:a"`, `dropped element "x:b"`, `attribute "x:c" is in namespace "u"`}},
		{`<p xmlns:xml="z" xml:space="preserve">`, []string{`dropped namespace declaration "xmlns:xml" (invalid in XML: the xml prefix can't be bound to another namespace`, `"xml:space" is in namespace "http://www.w3.org/XML/1998/namespace"`}},
		{`<p xmlns:xml="http://www.w3.org/XML/1998/namespace">`, nil},
		{`<p xmlns:xmlns="http://www.w3.org/2000/xmlns/">`, []string{`dropped namespace declaration "xmlns:xmlns" (invalid in XML: the xmlns prefix can't be declared`}},
		{`<p xmlns:x="http://www.w3.org/XML/1998/namespace" x:a="1">`, []string{`dropped namespace declaration "xmlns:x" (invalid in XML: only the xml prefix`, `dropped attribute "x:a"`}},
		{`<p xmlns:x="http://www.w3.org/2000/xmlns/">`, []string{`dropped namespace declaration "xmlns:x" (invalid in XML: no prefix can be bound to the xmlns namespace`}},
		{`<xmlns:p>x</xmlns:p>`, []string{`dropped element "xmlns:p"`}},
		// xlink is declared again for its attributes
		{`<svg xmlns:xlink=""><use xlink:href="#a"/></svg>`, []string{`dropped namespace declaration "xmlns:xlink"`}},
		// the declarations of reopened formatting elements are written again
		{`<p><b xmlns:x="u">a</p><x:i>b</x:i>`, []string{`element "x:i" is in namespace "u"`}},
	} {
		var b bytes.Buffer
		var warnings []string
		if err := Convert(&b, iotest.OneByteReader(strings.NewReader(tc.in)), Options{
			Warn: func(w Warning) { warnings = append(warnings, w.Msg) },
		}); err != nil {
			t.Errorf("%s: %v", tc.in, err)
			continue
		}
		ok := len(warnings) == len(tc.warnings)
		for i := 0; ok && i < len(warnings); i++ {
			ok = strings.Contains(warnings[i], tc.warnings[i])
		}
		if !ok {
			t.Errorf("%s: warnings:\n%s\nwant:\n%s", tc.in, strings.Join(warnings, "\n"), strings.Join(tc.warnings, "\n"))
		}
	}
}

// TestDeepNesting checks that elements past the Reader's stack limits and
// the output depth limit are dropped with a warning, keeping the output
// well-formed, and that formatting elements aren't reopened past the output
// depth limit.
func TestDeepNesting(t *testing.T) {
	for _, tc := range []struct {
		name     string
		in       string
		maxDepth int
		depth    int // the output depth
		divs     int // the number of div elements in the output, if not -1

		// the numbers of warnings about the Reader's stack limits, the output
		// depth limit, and others
		stack, output, other int
	}{
		// html and body are open in the output
		{"Divs", strings.Repeat("<div>", htmlstream.DefaultMaxDepth+6) + "x", 0, htmlstream.DefaultMaxDepth, htmlstream.DefaultMaxDepth - 2, 6, 2, 0},
		// a b reopened in each b, until the b in the div at the limit is dropped
		{"Reopened", strings.Repeat("<div><b></div>x", 20000), 0, htmlstream.DefaultMaxDepth, -1, 0, 20000 - (htmlstream.DefaultMaxDepth - 3), 0},
		{"ReopenedMaxDepth", strings.Repeat("<div><b></div>x", 20000), 256, 256, -1, 0, 20000 - (256 - 3), 0},
		// tbody and tr would fit, but not the td in them
		{"Table", "<table><td>x</td></table><b><br></b>", 4, 4, 0, 0, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var b bytes.Buffer
			var stack, output, other int
			err := Convert(&b, strings.NewReader(tc.in), Options{MaxDepth: tc.maxDepth, Warn: func(w Warning) {
				switch {
				case strings.Contains(w.Msg, "past the stack limits"):
					stack++
				case strings.Contains(w.Msg, "the output depth limit"):
					output++
				default:
					t.Logf("warning: %d: %s", w.Pos, w.Msg)
					other++
				}
			}})
			if err != nil {
				t.Fatal(err)
			}
			if stack != tc.stack || output != tc.output || other != tc.other {
				t.Errorf("%d stack limit, %d output depth limit and %d other warnings, want %d, %d and %d", stack, output, other, tc.stack, tc.output, tc.other)
			}
			out := b.String()
			if n, m := strings.Count(out, "<div>"), strings.Count(out, "</div>"); tc.divs >= 0 && (n != tc.divs || m != tc.divs) {
				t.Errorf("%d start and %d end tags of div, want %d", n, m, tc.divs)
			}
			if err := checkXML(out); err != nil {
				t.Errorf("not well-formed: %v", err)
			}
			if maxDepth := outputDepth(out); maxDepth != tc.depth {
				t.Errorf("output depth %d, want %d", maxDepth, tc.depth)
			}
		})
	}
}

// outputDepth returns the maximum depth of the elements in the output, read
// up to the first XML syntax error.
func outputDepth(out string) int {
	depth, maxDepth := 0, 0
	d := xml.NewDecoder(strings.NewReader(out))
	for {
		tok, err := d.RawToken()
		if err != nil {
			return maxDepth
		}
		switch tok.(type) {
		case xml.StartElement:
			depth++
			maxDepth = max(maxDepth, depth)
		case xml.EndElement:
			depth--
		}
	}
}

// TestWarnOrder checks that the output before a warning is written before
// the Warn call.
func TestWarnOrder(t *testing.T) {
	var b bytes.Buffer
	err := Convert(&b, strings.NewReader(`<p>a</p><p epub:type=x>b</p>`), Options{
		Warn: func(Warning) { b.WriteString("[warning]") },
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := b.String(), "<p>a</p>[warning]<p"; !strings.Contains(got, want) {
		t.Errorf("got %q, want it to contain %q", got, want)
	}
}

// convertCase is a test case in testdata/polyglot (see polyglot_testdata).
type convertCase struct {
	Name string
	In   string
	Out  string // the expected output, or empty for a fuzz seed
	Warn string // the expected warnings, one "pos: message" per line

	// NonIdempotent is set by the nonidempotent flag, for outputs which
	// Convert doesn't convert to the same output (or converts with warnings,
	// for cases without warnings).
	NonIdempotent bool

	// Options has XMLSyntax and Scripting set by the xs and scripting flags.
	Options Options
}

// readConvertCases returns the test cases in testdata/polyglot.
func readConvertCases() []convertCase {
	var cases []convertCase
	for name, c := range polyglot_testdata.Cases("*.txtar", "in", "out", "warn") {
		in, _ := c.Section("in")
		out, _ := c.Section("out")
		warn, _ := c.Section("warn")
		opt := Options{XMLSyntax: c.HasFlag("xs"), Scripting: c.HasFlag("scripting")}
		cases = append(cases, convertCase{name, in, out, warn, c.HasFlag("nonidempotent"), opt})
	}
	return cases
}

// convertWarnings converts the input from r with Convert, returning the
// output and the warnings, one "pos: message" per line.
func convertWarnings(r io.Reader, opt Options) (string, string, error) {
	var b bytes.Buffer
	var warnings []string
	opt.Warn = func(w Warning) {
		warnings = append(warnings, fmt.Sprintf("%d: %s", w.Pos, w.Msg))
	}
	err := Convert(&b, r, opt)
	return b.String(), strings.Join(warnings, "\n"), err
}

// TestStrictScriptType checks that a script which can't be written as
// well-formed XML is written with a warning, or is an error in strict mode.
func TestStrictScriptType(t *testing.T) {
	for _, tc := range []struct {
		in  string
		bad bool
	}{
		{`<script type=text/x-template><p>a & b</p></script>`, true},
		{`<script type=text/x-template>]]></script>`, true},
		{`<script type=text/x-template>x</script>`, false},
		{`<script>if (a < b && c) {}</script>`, false},
		{`<script type=application/json>{"a":"<&>"}</script>`, false},
		{`<style>a > b {}</style>`, false},
	} {
		out, warnings, err := convertWarnings(strings.NewReader(tc.in), Options{})
		if err != nil {
			t.Fatalf("%q: %v", tc.in, err)
		}
		if bad := scriptTypeWarning(warnings); bad != tc.bad {
			t.Errorf("%q: warnings %q", tc.in, warnings)
		}
		if err := checkXML(out); (err != nil) != tc.bad {
			t.Errorf("%q: well-formed %v, want %v: %v", tc.in, err == nil, !tc.bad, err)
		}
		var b bytes.Buffer
		err = Convert(&b, strings.NewReader(tc.in), Options{Strict: true})
		var perr *Error
		if (err != nil) != tc.bad || err != nil && (!errors.As(err, &perr) || perr.Pos != 29 || !strings.HasPrefix(perr.Msg, `script of type "text/x-template" containing`)) {
			t.Errorf("%q: strict error %v", tc.in, err)
		}
		if err == nil {
			if err := checkXML(b.String()); err != nil {
				t.Errorf("%q: strict output not well-formed: %v", tc.in, err)
			}
		}
	}
}

// scriptTypeWarning reports whether the warnings have the one for scripts
// written unchanged although they contain <, & or "]]>", which makes the
// output not well-formed.
func scriptTypeWarning(warnings string) bool {
	return strings.Contains(warnings, ": script of type ")
}

// TestConvert checks the output and warnings of Convert for the test cases in
// testdata/polyglot, with the input read at once and one byte at a time, and
// that the output is well-formed XML.
func TestConvert(t *testing.T) {
	for _, tc := range readConvertCases() {
		if tc.Out == "" {
			continue
		}
		t.Run(tc.Name, func(t *testing.T) {
			for _, oneByte := range []bool{false, true} {
				var r io.Reader = strings.NewReader(tc.In)
				if oneByte {
					r = iotest.OneByteReader(r)
				}
				out, warnings, err := convertWarnings(r, tc.Options)
				if err != nil {
					t.Fatalf("one byte at a time %v: error: %v", oneByte, err)
				}
				if out != tc.Out {
					t.Errorf("one byte at a time %v: output:\n got %s\nwant %s", oneByte, out, tc.Out)
				}
				if warnings != tc.Warn {
					t.Errorf("one byte at a time %v: warnings:\n%s\nwant:\n%s", oneByte, warnings, tc.Warn)
				}
			}
			if err := checkXML(tc.Out); err != nil && !scriptTypeWarning(tc.Warn) {
				t.Errorf("output is not well-formed XML: %v", err)
			}
			out, warnings, err := convertWarnings(strings.NewReader(tc.Out), tc.Options)
			if idempotent := err == nil && out == tc.Out && (warnings == "" || tc.Warn != ""); idempotent == tc.NonIdempotent {
				t.Errorf("converting the output again (nonidempotent flag %v): error %v, output:\n%s\nwarnings:\n%s", tc.NonIdempotent, err, out, warnings)
			}
		})
	}
}

// FuzzConvert checks that Convert returns no error other than
// [htmlstream.ErrUnsupportedEncoding], that the output is well-formed XML
// (see checkXML) unless a script of a type other than JavaScript or JSON
// contains <, & or "]]>" (written unchanged, with a warning), that in strict
// mode such output is an error (strict output without an error is
// well-formed, and output which isn't well-formed fails in strict mode), that
// output without warnings is converted to the same output without warnings,
// and that a Writer given the tokens of a Reader reading the input one byte
// at a time, with the default chunk size or one from 1 to 64 selected by
// chunk, writes the same output and warnings. If opts has bit 0 or 1 set,
// Convert with [Options.Scripting] or [Options.XMLSyntax] is compared to the
// Writer, whose Reader has the same options, instead. If opts has bit 2 set,
// the conversions write no doctype ([NoDoctype]), so the output is in quirks
// mode if the input is. If opts has bit 3 set, [Options.MaxDepth] is 3 plus
// the upper four bits of opts (also for the Readers of the Writers), and the
// output must not be deeper, unless a script of another type has markup
// (written unchanged). The seeds are the inputs of the test cases in
// testdata/polyglot, and inputs with tables in a p for quirks mode output.
func FuzzConvert(f *testing.F) {
	for _, tc := range readConvertCases() {
		f.Add(tc.In, byte(0), byte(0))
		f.Add(tc.In, byte(3), byte(0))
		f.Add(tc.In, byte(5), byte(3))
	}
	// the output in quirks mode (see TestQuirksTableInP)
	f.Add(`<p>a<table><tr><td>x</table>b<br><span>c</span>`, byte(0), byte(4))
	f.Add(`<span><p>a<table></table>b</span>c<p>d<table></table></p>`, byte(2), byte(6))
	f.Add(`<b><p>a<table></table>b</b>c<div><p>d<table></table>e</div>`, byte(5), byte(5))
	// a p kept open around a table in a table in a p kept open
	f.Add("<p><tABle><td><p><tABle>", byte(3), byte(0x26))
	// a misplaced html start tag after a table in a p kept open
	f.Add("<p><tABle></tABle><html><A>", byte('Q'), byte(0x05))
	// a formatting element reopened after a table in a p kept open
	f.Add("<p><A><p><tABle></tABle>0", byte('K'), byte('|'))
	// the last chunk of a bogus comment has the Raw of </>
	f.Add("</00</>", byte(5), byte(0x26))
	// invalid UTF-8 around a dropped NUL
	f.Add("000\xdd\x00\x98", byte(5), byte(0x59))
	// text split into parts in an SVG or MathML element left open by a body
	// end tag, before a comment
	f.Add("<BodY><mAth></BodY>0000<!0", byte(3), byte(0))
	// the output depth limit
	f.Add("<div><b></div>x<div><b><i></div>y</br></p><table><td>z", byte(0), byte(0x18))
	f.Add("<table><td><div><b></div><svg><g/></svg>x", byte(3), byte(0x0b))
	// scripts of another type with markup, written unchanged
	f.Add("<sCript tYpe=0><A>", byte(0), byte(0x09))
	f.Add("<sCript tYpe=0><b></b >", byte(0), byte(0x09))
	// a self-closing textarea start tag before a leading newline and
	// whitespace, with the newline and the whitespace in tokens of their own
	f.Add("<teXtAreA />\n\n0", byte('A'), byte(0))
	f.Fuzz(func(t *testing.T, in string, chunk, opts byte) {
		var opt Options
		if opts&4 != 0 {
			opt.DoctypeMode = NoDoctype
		}
		if opts&8 != 0 {
			opt.MaxDepth = 3 + int(opts>>4)
		}
		out, warnings, err := convertWarnings(strings.NewReader(in), opt)
		if errors.Is(err, htmlstream.ErrUnsupportedEncoding) {
			return
		}
		if err != nil {
			t.Fatalf("%q: error: %v", in, err)
		}
		xmlErr := checkXML(out)
		if xmlErr != nil && !scriptTypeWarning(warnings) {
			t.Fatalf("%q: output is not well-formed XML: %v\noutput: %s\nwarnings:\n%s", in, xmlErr, out, warnings)
		}
		if d := outputDepth(out); opt.MaxDepth != 0 && !scriptTypeWarning(warnings) && d > opt.MaxDepth {
			t.Fatalf("%q: output depth %d, MaxDepth %d\noutput: %s", in, d, opt.MaxDepth, out)
		}
		// without warnings, converting the output again gives the same output
		if warnings == "" {
			out2, warnings2, err := convertWarnings(strings.NewReader(out), opt)
			if err != nil || out2 != out || warnings2 != "" {
				t.Fatalf("%q: converting the output again: error %v\noutput: %s\nagain: %s\nwarnings:\n%s", in, err, out, out2, warnings2)
			}
		}
		// in strict mode, output which isn't well-formed is an error
		var sb bytes.Buffer
		if err := Convert(&sb, strings.NewReader(in), Options{Strict: true, DoctypeMode: opt.DoctypeMode, MaxDepth: opt.MaxDepth}); err == nil {
			if err := checkXML(sb.String()); err != nil {
				t.Fatalf("%q: strict output is not well-formed XML: %v\noutput: %s", in, err, sb.String())
			}
			if xmlErr != nil {
				t.Fatalf("%q: output is not well-formed XML (%v), but strict mode doesn't fail\noutput: %s", in, xmlErr, out)
			}
		}

		scripting, xmlSyntax := opts&1 != 0, opts&2 != 0
		if scripting || xmlSyntax {
			opt.Scripting, opt.XMLSyntax = scripting, xmlSyntax
			out, warnings, err = convertWarnings(strings.NewReader(in), opt)
			if err != nil {
				t.Fatalf("%q: scripting %v, XML syntax %v: %v", in, scripting, xmlSyntax, err)
			}
		}
		size := 0
		if chunk != 0 {
			size = 1 + int(chunk-1)%64
		}
		wout, wwarnings, err := writerWarnings(iotest.OneByteReader(strings.NewReader(in)), size, opt)
		if err != nil {
			t.Fatalf("%q: chunk size %d, scripting %v, XML syntax %v: Writer: %v", in, size, scripting, xmlSyntax, err)
		}
		if wout != out || wwarnings != warnings {
			t.Fatalf("%q: chunk size %d, scripting %v, XML syntax %v: output differs:\n%s\nwarnings:\n%s\nwant:\n%s\nwarnings:\n%s", in, size, scripting, xmlSyntax, wout, wwarnings, out, warnings)
		}
	})
}

// TestOptionsNewReader checks that a Writer given the tokens of the Reader of
// Options.NewReader writes what Convert writes, with the same warnings and
// errors.
func TestOptionsNewReader(t *testing.T) {
	for _, in := range []string{
		`<p>a<div/>b<noscript><b>c</b></noscript>`,
		`<b><b><b><b><b>x`,
		`<p a="b>c`,
	} {
		for _, opt := range []Options{{}, {XMLSyntax: true}, {Scripting: true}, {Strict: true}, {MaxDepth: 1}, {MaxDepth: 4}} {
			out, warnings, err := convertWarnings(strings.NewReader(in), opt)
			wout, wwarnings, werr := writerWarnings(strings.NewReader(in), 0, opt)
			if wout != out || wwarnings != warnings || fmt.Sprint(werr) != fmt.Sprint(err) {
				t.Errorf("%q %+v: got %q (%v)\nwarnings:\n%s\nwant %q (%v)\nwarnings:\n%s", in, opt, wout, werr, wwarnings, out, err, warnings)
			}
		}
	}
}

// writerWarnings converts the input from r with a Writer given the tokens of
// the Reader of opt.NewReader with the chunk size (0 for the default),
// returning the output and the warnings like convertWarnings.
func writerWarnings(r io.Reader, chunk int, opt Options) (string, string, error) {
	rd := opt.NewReader(r)
	if chunk > 0 {
		rd.SetChunkSize(chunk)
	}
	var b bytes.Buffer
	var warnings []string
	opt.Warn = func(w Warning) {
		warnings = append(warnings, fmt.Sprintf("%d: %s", w.Pos, w.Msg))
	}
	pw := NewWriter(&b, opt)
	var err error
	for rd.Next() && err == nil {
		err = pw.WriteToken(rd.Token())
	}
	if err == nil {
		err = rd.Err()
	}
	if err == nil {
		err = pw.Close()
	}
	return b.String(), strings.Join(warnings, "\n"), err
}

// TestConvertChunks checks that a Writer given the tokens of a Reader with
// small chunk sizes writes the output and warnings of the test cases in
// testdata/polyglot.
func TestConvertChunks(t *testing.T) {
	for _, tc := range readConvertCases() {
		if tc.Out == "" {
			continue
		}
		t.Run(tc.Name, func(t *testing.T) {
			for chunk := 1; chunk <= 16; chunk++ {
				for _, oneByte := range []bool{false, true} {
					var r io.Reader = strings.NewReader(tc.In)
					if oneByte {
						r = iotest.OneByteReader(r)
					}
					out, warnings, err := writerWarnings(r, chunk, tc.Options)
					if err != nil {
						t.Fatalf("chunk size %d, one byte at a time %v: error: %v", chunk, oneByte, err)
					}
					if out != tc.Out {
						t.Errorf("chunk size %d, one byte at a time %v: output:\n got %s\nwant %s", chunk, oneByte, out, tc.Out)
					}
					if warnings != tc.Warn {
						t.Errorf("chunk size %d, one byte at a time %v: warnings:\n%s\nwant:\n%s", chunk, oneByte, warnings, tc.Warn)
					}
				}
			}
		})
	}
}

// TestSpaceHold checks that the formatting elements reopened for text in a
// table are written before its leading whitespace up to maxSpaceHold bytes,
// and after longer whitespace, whatever the chunk size.
func TestSpaceHold(t *testing.T) {
	for _, n := range []int{maxSpaceHold, maxSpaceHold + 1} {
		space := strings.Repeat(" ", n)
		in := "<!DOCTYPE html><p><b>x<table>" + space + "y</table><table>" + space + "</table>"
		want := "<p><b>x</b></p><table><b>" + space + "y</b></table><table>" + space + "</table>"
		if n > maxSpaceHold {
			want = "<p><b>x</b></p><table>" + space + "<b>y</b></table><table>" + space + "</table>"
		}
		out, warnings, err := convertWarnings(strings.NewReader(in), Options{})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, want) {
			t.Errorf("%d bytes of whitespace: output doesn't contain the expected table: %s", n, out)
		}
		for _, chunk := range []int{1, 7, 4096} {
			wout, wwarnings, err := writerWarnings(strings.NewReader(in), chunk, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if wout != out || wwarnings != warnings {
				t.Errorf("%d bytes of whitespace, chunk size %d: output or warnings differ", n, chunk)
			}
		}
	}
}

// TestHoldLimits checks that the whitespace at the start of the head waits
// for the charset meta element up to maxHeadSpace bytes, that the whitespace
// and comments after the head are written after the head end tag up to
// maxAfterHead bytes, and in the head past it, and that the comments after
// the body end tag are written after it up to maxAfterBody bytes, and in the
// body past it, whatever the chunk size.
func TestHoldLimits(t *testing.T) {
	for _, tc := range []struct {
		n    int
		in   func(space string) string
		want func(space string) string
	}{
		{maxHeadSpace, func(sp string) string { return "<head>" + sp + "<meta charset=utf-8><title>t</title>" },
			func(sp string) string { return "<head>" + sp + `<meta charset="UTF-8" /><title>` }},
		{maxHeadSpace + 1, func(sp string) string { return "<head>" + sp + "<meta charset=utf-8><title>t</title>" },
			func(sp string) string { return `<head><meta charset="UTF-8" />` + sp + "<title>" }},
		{maxAfterHead - 8, func(sp string) string { return "<head></head><!--c-->" + sp + "<link rel=x><p>x" },
			func(sp string) string { return `<link rel="x" /></head><!--c-->` + sp + "<body>" }},
		{maxAfterHead - 7, func(sp string) string { return "<head></head><!--c-->" + sp + "<link rel=x><p>x" },
			func(sp string) string { return `<!--c-->` + sp + `<link rel="x" /></head><body>` }},
		{maxAfterBody - 23, func(sp string) string { return "<p>x</p></body><!--c--><p>y</p></body><!--" + sp + "--><!--d-->" },
			func(sp string) string { return "<p>x</p><p>y</p></body><!--c--><!--" + sp + "--><!--d--></html>" }},
		{maxAfterBody - 22, func(sp string) string { return "<p>x</p></body><!--c--><p>y</p></body><!--" + sp + "--><!--d-->" },
			func(sp string) string { return "<p>x</p><p>y</p><!--c--><!--" + sp + "--><!--d--></body></html>" }},
	} {
		space := strings.Repeat(" ", tc.n)
		in := tc.in(space)
		out, warnings, err := convertWarnings(strings.NewReader(in), Options{})
		if err != nil {
			t.Fatal(err)
		}
		if want := tc.want(space); !strings.Contains(out, want) {
			t.Errorf("%d bytes of whitespace in %.30q: output doesn't contain %.40q...: %.200s", tc.n, in, want, out)
		}
		for _, chunk := range []int{1, 7, 4096} {
			wout, wwarnings, err := writerWarnings(strings.NewReader(in), chunk, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if wout != out || wwarnings != warnings {
				t.Errorf("%d bytes of whitespace in %.30q, chunk size %d: output or warnings differ", tc.n, in, chunk)
			}
		}
	}
}

// TestFinalNewline checks that FinalNewline ends the output with a line
// break, taken from whitespace at the end of the body, and with Indent from
// the line break before the body end tag, whatever the chunk size, and that
// converting the output with Indent again gives the same output.
func TestFinalNewline(t *testing.T) {
	const start = `<html xmlns="http://www.w3.org/1999/xhtml"><head><meta charset="UTF-8" /></head><body>`
	for _, tc := range []struct{ in, out string }{
		{"<p>a</p>\n</body>\n</html>\n", "<p>a</p>\n\n</body></html>\n"},
		{"<p>a</p>", "<p>a</p></body></html>\n"},
		{"<p>a\n", "<p>a\n</p></body></html>\n"},
		{"a\n", "a\n</body></html>\n"},
		{"a<!---->\n \n", "a<!---->\n </body></html>\n"},
		{"<b>x</b> \n", "<b>x</b> </body></html>\n"},
		{"<p><b>x</p> \n", "<p><b>x</b></p><b> \n</b></body></html>\n"},
		{"<body><div><p>x</body>\n</html>\n", "<div><p>x\n\n</p></div></body></html>\n"},
		{"<p>a</p>\n</body>\n<!--c-->\n</html>\n<!--d-->\n", "<p>a</p>\n\n\n\n</body><!--c--></html><!--d-->\n"},
		{"", "</body></html>\n"},
	} {
		opt := Options{DoctypeMode: NoDoctype, FinalNewline: true}
		out, warnings, err := convertWarnings(strings.NewReader(tc.in), opt)
		if err != nil {
			t.Fatal(err)
		}
		if want := start + tc.out; out != want || warnings != "" {
			t.Errorf("%q:\n got %q\nwant %q\nwarnings: %s", tc.in, out, want, warnings)
		}
		for _, chunk := range []int{1, 3} {
			if wout, _, err := writerWarnings(strings.NewReader(tc.in), chunk, opt); err != nil || wout != out {
				t.Errorf("%q, chunk size %d: got %q, error %v", tc.in, chunk, wout, err)
			}
		}
	}
	// with Indent, the line break before the body end tag is taken instead
	for _, tc := range []struct{ in, prefix, out string }{
		{"<p>a</p>\n", "", "\n  <p>a</p></body></html>\n"},
		{"<p>a</p>\n</body>\n</html>\n", "", "\n  <p>a</p></body></html>\n"},
		{"<div><p>x", "", "\n  <div>\n    <p>x</p>\n  </div></body></html>\n"},
		{"<p>x</p>\n<!-- a -->\n\n", "", "\n  <p>x</p>\n  <!-- a --></body></html>\n"},
		{"<p>x</p><script>s</script>\n", "", "\n  <p>x</p>\n  <script>s</script></body></html>\n"},
		{"<pre>x\n</pre>\n", "", "\n  <pre>x\n</pre></body></html>\n"},
		{"<p>a</p>b\n", "", "\n  <p>a</p>b\n  </body></html>\n"},
		{"<b>x</b> \n", "", "<b>x</b> </body></html>\n"},
		{"\n", "", "</body></html>\n"},
		{"<div><p>x</p></div>\n", "\t", "\n\t  <div>\n\t    <p>x</p>\n\t  </div></body></html>\n"},
	} {
		opt := Options{DoctypeMode: NoDoctype, FinalNewline: true, Indent: "  ", IndentPrefix: tc.prefix}
		out, warnings, err := convertWarnings(strings.NewReader(tc.in), opt)
		if err != nil {
			t.Fatal(err)
		}
		if _, body, _ := strings.Cut(out, "<body>"); body != tc.out || warnings != "" {
			t.Errorf("%q with Indent, prefix %q:\n got %q\nwant %q\nwarnings: %s", tc.in, tc.prefix, body, tc.out, warnings)
		}
		for _, chunk := range []int{1, 3} {
			if wout, _, err := writerWarnings(strings.NewReader(tc.in), chunk, opt); err != nil || wout != out {
				t.Errorf("%q with Indent, prefix %q, chunk size %d: got %q, error %v", tc.in, tc.prefix, chunk, wout, err)
			}
		}
		if again, _, err := convertWarnings(strings.NewReader(out), opt); err != nil || again != out {
			t.Errorf("%q with Indent, prefix %q: converting the output again: error %v, got %q", tc.in, tc.prefix, err, again)
		}
	}
}

// TestIndent checks the output with Indent and IndentPrefix, whatever the
// chunk size and with hooks which do nothing, that converting it again gives
// the same output, and the error for an indentation which isn't whitespace.
func TestIndent(t *testing.T) {
	for _, tc := range []struct {
		name, in, indent, prefix, out string
	}{
		{"Blocks", "<!DOCTYPE html><title>t</title><div><p>a <b>b</b>\nc</p><ul><li>x<li>y</ul></div><p>z", "  ", "", `<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml"><head>
  <meta charset="UTF-8" />
  <title>t</title>
</head>
<body>
  <div>
    <p>a <b>b</b>
      c</p>
    <ul>
      <li>x</li>
      <li>y</li>
    </ul>
  </div>
  <p>z</p>
</body></html>`},
		{"Kept", "<pre>\n a\n  b</pre><p>x <span>y</span> <!-- c --> z</p><svg><g> <rect/> </g></svg><textarea> t\n u</textarea>", "\t", "", "<!DOCTYPE html>\n" + `<html xmlns="http://www.w3.org/1999/xhtml"><head>
	<meta charset="UTF-8" />
</head>
<body>
	<pre> a
  b</pre>
	<p>x <span>y</span> <!-- c --> z</p><svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink"><g> <rect></rect> </g></svg><textarea> t
 u</textarea></body></html>`},
		{"CommentsAndEmptyLines", "<body>\n<!-- a -->\n\n<div>\n\n<p>x</p>\n\n\n<!-- b -->\n<p>y</p>\n\n</div><div>\n</div>", "  ", "", `<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml"><head>
  <meta charset="UTF-8" />
</head>
<body>
  <!-- a -->

  <div>
    <p>x</p>

    <!-- b -->
    <p>y</p>
  </div>
  <div></div>
</body></html>`},
		{"Prefix", "<!-- c --><title>t</title><p>a\n  b</p><pre>c\nd</pre>", "", "\t", "<!DOCTYPE html>\n\t<!-- c -->\n" + "\t" + `<html xmlns="http://www.w3.org/1999/xhtml"><head><meta charset="UTF-8" /><title>t</title></head><body><p>a
	b</p><pre>c
d</pre></body></html>`},
		{"CommentsAfterBody", "<p>x</p>\n<!-- a -->\n</body>\n<!-- b -->\n</html>\n<!-- c -->\n", "  ", "", `<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml"><head>
  <meta charset="UTF-8" />
</head>
<body>
  <p>x</p>
  <!-- a -->
</body><!-- b --></html><!-- c -->`},
		{"IndentPrefix", "<div><p>a\n   b</p></div>", " ", "\t", "<!DOCTYPE html>\n" + "\t" + `<html xmlns="http://www.w3.org/1999/xhtml"><head>
	 <meta charset="UTF-8" />
	</head>
	<body>
	 <div>
	  <p>a
	   b</p>
	 </div>
	</body></html>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opt := Options{Indent: tc.indent, IndentPrefix: tc.prefix}
			out, warnings, err := convertWarnings(strings.NewReader(tc.in), opt)
			if err != nil || warnings != "" {
				t.Fatalf("error %v, warnings %s", err, warnings)
			}
			if out != tc.out {
				t.Errorf("got\n%s\nwant\n%s", out, tc.out)
			}
			for _, chunk := range []int{1, 3} {
				if wout, _, err := writerWarnings(strings.NewReader(tc.in), chunk, opt); err != nil || wout != out {
					t.Errorf("chunk size %d: error %v, got\n%s", chunk, err, wout)
				}
			}
			hooked := opt
			hooked.OnTag, hooked.OnText = func(*Tag) {}, func(*Text) {}
			if hout, _, err := convertWarnings(strings.NewReader(tc.in), hooked); err != nil || hout != out {
				t.Errorf("with hooks: error %v, got\n%s", err, hout)
			}
			if again, _, err := convertWarnings(strings.NewReader(out), opt); err != nil || again != out {
				t.Errorf("converting the output again: error %v, got\n%s", err, again)
			}
		})
	}
	for _, opt := range []Options{{Indent: "x"}, {IndentPrefix: "\n"}} {
		if err := Convert(io.Discard, strings.NewReader("<p>x"), opt); !errors.Is(err, ErrInvalidOp) {
			t.Errorf("%+v: error %v", opt, err)
		}
	}
}
