package difffuzz

import (
	"bytes"
	"strings"
	"testing"
	"unicode"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"

	"github.com/pgaskin/htmlstream/htmlutil"
)

// xnetPrescan is the prescan of golang.org/x/net/html/charset v0.60.0
// (charset.go, prescan), which isn't exported: DetermineEncoding falls back to
// UTF-8 detection and windows-1252 when it fails. It returns the encoding
// name, and whether it was taken from the content attribute after a charset
// attribute with an unknown label (contentAfterFailure), which the HTML
// Standard's prescan doesn't do.
func xnetPrescan(content []byte) (name string, contentAfterFailure bool) {
	z := html.NewTokenizer(bytes.NewReader(content))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return "", false

		case html.StartTagToken, html.SelfClosingTagToken:
			tagName, hasAttr := z.TagName()
			if !bytes.Equal(tagName, []byte("meta")) {
				continue
			}
			attrList := make(map[string]bool)
			gotPragma := false

			const (
				dontKnow = iota
				doNeedPragma
				doNotNeedPragma
			)
			needPragma := dontKnow

			name = ""
			found := false
			afterFailure := false
			for hasAttr {
				var key, val []byte
				key, val, hasAttr = z.TagAttr()
				ks := string(key)
				if attrList[ks] {
					continue
				}
				attrList[ks] = true
				for i, c := range val {
					if 'A' <= c && c <= 'Z' {
						val[i] = c + 0x20
					}
				}

				switch ks {
				case "http-equiv":
					if bytes.Equal(val, []byte("content-type")) {
						gotPragma = true
					}

				case "content":
					if !found {
						name = fromMetaElement(string(val))
						if name != "" {
							var e any
							e, name = charset.Lookup(name)
							found = e != nil && name != ""
							if found {
								afterFailure = needPragma == doNotNeedPragma
								needPragma = doNeedPragma
							}
						}
					}

				case "charset":
					e, n := charset.Lookup(string(val))
					name, found = n, e != nil
					afterFailure = false
					needPragma = doNotNeedPragma
				}
			}

			if needPragma == dontKnow || needPragma == doNeedPragma && !gotPragma {
				continue
			}

			if strings.HasPrefix(name, "utf-16") {
				name = "utf-8"
			}

			if found {
				return name, afterFailure
			}
		}
	}
}

// fromMetaElement is from golang.org/x/net/html/charset v0.60.0.
func fromMetaElement(s string) string {
	for s != "" {
		csLoc := strings.Index(s, "charset")
		if csLoc == -1 {
			return ""
		}
		s = s[csLoc+len("charset"):]
		s = strings.TrimLeft(s, " \t\n\f\r")
		if !strings.HasPrefix(s, "=") {
			continue
		}
		s = s[1:]
		s = strings.TrimLeft(s, " \t\n\f\r")
		if s == "" {
			return ""
		}
		if q := s[0]; q == '"' || q == '\'' {
			s = s[1:]
			closeQuote := strings.IndexRune(s, rune(q))
			if closeQuote == -1 {
				return ""
			}
			return s[:closeQuote]
		}

		end := strings.IndexAny(s, "; \t\n\f\r")
		if end == -1 {
			end = len(s)
		}
		return s[:end]
	}
	return ""
}

// prescanSkip reports why the x/net prescan, which uses the tokenizer,
// differs from the HTML Standard's prescan for head, or "" if they should
// agree.
func prescanSkip(head []byte) string {
	lower := bytes.ToLower(head)
	for _, name := range []string{"script", "style", "title", "textarea", "xmp", "iframe", "noembed", "noframes", "noscript", "plaintext"} {
		// The tokenizer doesn't read markup in the contents of raw text and
		// RCDATA elements.
		if bytes.Contains(lower, []byte("<"+name)) {
			return "raw text element"
		}
	}
	if bytes.Contains(head, []byte("--!>")) {
		return "comment ended by --!>" // only by the tokenizer
	}
	for i := 0; i < len(head); i++ {
		// The prescan reads a tag name up to whitespace or ">", the
		// tokenizer only up to "/" (except after "<meta", where the prescan
		// stops at "/" too).
		s := head[i:]
		if len(s) < 2 || s[0] != '<' {
			continue
		}
		s = bytes.TrimPrefix(s[1:], []byte("/"))
		if len(s) == 0 || !('a' <= s[0]|0x20 && s[0]|0x20 <= 'z') {
			continue
		}
		if j := bytes.IndexAny(s, "\t\n\f\r >"); j >= 0 {
			s = s[:j]
		}
		if j := bytes.IndexByte(s, '/'); j >= 0 && !bytes.EqualFold(s[:j], []byte("meta")) {
			return "/ in a tag name"
		}
	}
	for _, r := range string(head) {
		// x/text's htmlindex.Get trims labels with strings.TrimSpace, which
		// also removes whitespace other than ASCII whitespace (e.g., "\v"),
		// which the Encoding Standard keeps.
		if unicode.IsSpace(r) && !strings.ContainsRune("\t\n\f\r ", r) {
			return "label with whitespace other than ASCII whitespace"
		}
	}
	for _, p := range []string{"<?xml", "<\x00?\x00x\x00", "\x00<\x00?\x00x"} {
		if bytes.HasPrefix(head, []byte(p)) {
			return "XML declaration" // not checked by x/net
		}
	}
	return ""
}

// comparePrescan compares htmlutil.PrescanCharset with the x/net prescan,
// returning a description of the difference, or "" if they agree or differ
// in a known way.
func comparePrescan(head []byte) string {
	if prescanSkip(head) != "" {
		return ""
	}
	got, ok := htmlutil.PrescanCharset(head)
	want, afterFailure := xnetPrescan(bytes.Clone(head))
	if afterFailure {
		return "" // x/net uses the content attribute after a failed charset attribute
	}
	if want == "x-user-defined" {
		want = "windows-1252" // x/net doesn't replace it
	}
	if !ok {
		got = ""
	}
	if !strings.EqualFold(got, want) {
		return "got " + got + ", x/net " + want
	}
	return ""
}

// TestPrescanCharsetXNet compares htmlutil.PrescanCharset with the x/net
// prescan where they should agree, and checks the known differences.
func TestPrescanCharsetXNet(t *testing.T) {
	for _, head := range []string{
		``,
		`<meta charset=utf-8>`,
		`<META CHARSET="KOI8-R">`,
		`<meta charset=latin1>`,
		`<meta charset=utf-16>`,
		`<meta charset=iso-2022-kr>`,
		`<meta charset=foo><meta charset=koi8-r>`,
		`<meta/charset=koi8-r>`,
		`<meta charset=koi8-r/>`,
		`<meta charset="koi8-r"x=y>`,
		`<meta x="y>"charset=koi8-r>`,
		`<meta x=y>charset=koi8-r>`,
		`<meta =charset=koi8-r>`,
		`<metacharset=koi8-r>`,
		`<meta charset=koi8-r`,
		`<meta http-equiv=content-type content="text/html; charset=koi8-r">`,
		`<meta content="text/html; charset=koi8-r" http-equiv="Content-Type">`,
		`<meta content="text/html; charset=koi8-r">`,
		`<meta http-equiv=content-type content="charset=koi8-r" charset=utf-8>`,
		`<meta http-equiv=content-type content="charset=koi8-r" charset=foo>`,
		`<meta content="charset=foo" content="charset=utf-8" http-equiv=content-type>`,
		`<!-- <meta charset=koi8-r> --><meta charset=utf-8>`,
		`<!--><meta charset=koi8-r>`,
		`<!---><meta charset=koi8-r>`,
		`<!x <meta charset=koi8-r>`,
		`<? <meta charset=koi8-r> ?><meta charset=utf-8>`,
		`</ <meta charset=koi8-r>`,
		`</x <meta charset=koi8-r>`,
		`<p title=<meta charset=koi8-r>`,
		`<p title=x <meta charset=koi8-r>`,
		`<<meta charset=koi8-r>`,
		"\xEF\xBB\xBF<meta charset=koi8-r>",
	} {
		if d := comparePrescan([]byte(head)); d != "" {
			t.Errorf("%q: %s", head, d)
		}
	}

	// Known differences.
	for _, tc := range []struct {
		head, spec, xnet string
	}{
		{`<title><meta charset=koi8-r></title>`, "KOI8-R", ""},
		{`<script>"<meta charset=koi8-r>"</script><meta charset=utf-8>`, "KOI8-R", "utf-8"},
		{`<!-- --!><meta charset=koi8-r> -->`, "", "koi8-r"},
		{`<?xml version="1.0" encoding="koi8-r"?>`, "KOI8-R", ""},
		{"<\x00?\x00x\x00m\x00l\x00", "UTF-16LE", ""},
		{`<meta charset=x-user-defined>`, "windows-1252", "x-user-defined"},
		{`<meta charset=foo http-equiv=content-type content="charset=koi8-r">`, "", "koi8-r"},
		{`<A/0="><metA ChArset='utf8'>`, "UTF-8", ""},
		{"<meta charset=koi\v>", "", "koi8-r"},
	} {
		got, _ := htmlutil.PrescanCharset([]byte(tc.head))
		xnet, _ := xnetPrescan([]byte(tc.head))
		if got != tc.spec || xnet != tc.xnet {
			t.Errorf("%q: got %q, x/net %q, want %q, %q", tc.head, got, xnet, tc.spec, tc.xnet)
		}
	}
}

// FuzzPrescanCharsetXNet compares htmlutil.PrescanCharset with the x/net
// prescan, except where they differ in a known way (see prescanSkip and
// comparePrescan).
func FuzzPrescanCharsetXNet(f *testing.F) {
	for _, s := range []string{
		`<meta charset=utf-8>`,
		`<meta http-equiv=content-type content="text/html; charset=koi8-r">`,
		`<meta content="charset=koi8-r" http-equiv='Content-Type' charset="x">`,
		`<!-- <meta charset=koi8-r> --><p class=a title="<meta charset=x>"><meta charset=latin1>`,
		`<!DOCTYPE html><html><head><meta charset="shift_jis"></head>`,
		`</x <meta charset=koi8-r><meta/charset='utf-8'/>`,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, head []byte) {
		if len(head) > 1024 {
			head = head[:1024]
		}
		if d := comparePrescan(head); d != "" {
			t.Errorf("%q: %s", head, d)
		}
	})
}
