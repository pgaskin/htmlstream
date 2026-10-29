package htmlutil

import (
	"strings"
	"testing"

	"github.com/pgaskin/htmlstream"
)

func TestMetaCharset(t *testing.T) {
	for _, tc := range []struct {
		page string
		name string // empty if none
	}{
		{`<meta charset=utf-8>`, "UTF-8"},
		{`<META CHARSET=UTF8>`, "UTF-8"},
		{`<meta charset=" Latin1 ">`, "windows-1252"},
		{`<meta charset=shift_jis>`, "Shift_JIS"},
		{`<meta charset=foo>`, ""},
		{`<meta charset>`, ""},
		{`<meta charset="">`, ""},
		{`<meta charset=utf-16>`, "UTF-8"},
		{`<meta charset=utf-16be>`, "UTF-8"},
		{`<meta charset=x-user-defined>`, "windows-1252"},
		{`<meta charset=iso-2022-kr>`, "replacement"},
		{`<meta http-equiv=Content-Type content="text/html; charset=UTF-8">`, "UTF-8"},
		{`<meta http-equiv=content-type content="text/html;charset = 'shift_jis'">`, "Shift_JIS"},
		{`<meta http-equiv=content-type content="text/html; charset='utf-8">`, ""},
		{`<meta http-equiv=content-type content="text/html; charset=">`, ""},
		{`<meta http-equiv=content-type content="text/html; charset=;utf-8">`, ""},
		{`<meta http-equiv=content-type content="text/html; charsetx; charset=koi8-r">`, "KOI8-R"},
		{`<meta http-equiv=content-type content="text/html; CHARSET=koi8-r;x">`, "KOI8-R"},
		{`<meta http-equiv=content-type content="charset=utf-16le">`, "UTF-8"},
		{`<meta http-equiv=content-type content="text/html">`, ""},
		{`<meta http-equiv=content-type>`, ""},
		{`<meta http-equiv=" content-type" content="charset=utf-8">`, ""},
		{`<meta name=x content="charset=utf-8">`, ""},
		// An invalid charset attribute doesn't prevent the content attribute
		// from being used, unlike in the prescan.
		{`<meta charset=foo http-equiv=content-type content="charset=koi8-r">`, "KOI8-R"},
		{`<meta charset=utf-8 http-equiv=content-type content="charset=koi8-r">`, "UTF-8"},
		// The first of duplicate attributes is used.
		{`<meta charset=koi8-r charset=utf-8>`, "KOI8-R"},
		{`<svg><meta charset=utf-8></svg>`, "UTF-8"}, // meta breaks out of svg
		{`<svg><title><meta charset=utf-8></title></svg>`, "UTF-8"},
		{`<math><mi><meta charset=utf-8>`, "UTF-8"},
		{`<link charset=utf-8>`, ""},
	} {
		r := htmlstream.NewReader(strings.NewReader(tc.page))
		var name string
		var ok, found bool
		for r.Next() {
			tok := r.Token()
			if tok.Kind() == htmlstream.StartTag {
				if n, o := MetaCharset(tok); o || tok.Is("meta") || tok.Is("link") {
					name, ok, found = n, o, true
				}
			}
		}
		if !found {
			t.Errorf("%s: no meta", tc.page)
		}
		if name != tc.name || ok != (tc.name != "") {
			t.Errorf("%s: got %q, %v, want %q", tc.page, name, ok, tc.name)
		}
	}

	r := htmlstream.NewReader(strings.NewReader(`<svg xmlns="http://www.w3.org/2000/svg"><meta charset="utf-8"/></svg>`))
	r.SetMode(htmlstream.XML)
	for r.Next() {
		if _, ok := MetaCharset(r.Token()); ok {
			t.Errorf("SVG meta in XML mode: got a declaration")
		}
	}
}

func TestPrescanCharset(t *testing.T) {
	for _, tc := range []struct {
		head string
		name string // empty if none
	}{
		{``, ""},
		{`<meta charset=utf-8>`, "UTF-8"},
		{`<meta charset="koi8-r">`, "KOI8-R"},
		{`<meta charset='koi8-r'>`, "KOI8-R"},
		{`<META CHARSET=KOI8-R>`, "KOI8-R"},
		{`<meta charset=latin1>`, "windows-1252"},
		{`<meta charset=utf-16>`, "UTF-8"},
		{`<meta charset=utf-16le>`, "UTF-8"},
		{`<meta charset=x-user-defined>`, "windows-1252"},
		{`<meta charset=iso-2022-kr>`, "replacement"},
		{`<meta charset=foo>`, ""},
		{`<meta charset=foo><meta charset=koi8-r>`, "KOI8-R"},
		{`<meta charset=koi8-r><meta charset=utf-8>`, "KOI8-R"},
		{`<meta/charset=koi8-r>`, "KOI8-R"},
		{"<meta\fcharset=koi8-r>", "KOI8-R"},
		{`<meta  charset = "koi8-r" >`, "KOI8-R"},
		{`<meta charset=" koi8-r ">`, "KOI8-R"},
		{`<meta charset=koi8-r/>`, ""}, // the value is "koi8-r/"
		{`<meta charset="koi8-r"/>`, "KOI8-R"},
		{`<meta charset="koi8-r"x=y>`, "KOI8-R"},
		{`<meta name="charset=koi8-r">`, ""},
		{`<meta x="y>"charset=koi8-r>`, "KOI8-R"},
		{`<meta x=y>charset=koi8-r>`, ""},
		{`<meta x='<meta charset=koi8-r>'>`, ""},
		{`<meta x=a charset=koi8-r>`, "KOI8-R"},
		{`<meta =charset=koi8-r>`, ""}, // the name is "=charset"
		{`<metacharset=koi8-r>`, ""},
		{`<meta>charset=koi8-r>`, ""},
		{`<meta>`, ""},
		{`<meta charset=koi8-r`, ""},    // cut off
		{`<meta charset="koi8-r"`, ""},  // cut off
		{`<meta charset="koi8-r`, ""},   // cut off
		{`<meta charset="koi8-r" `, ""}, // cut off
		{`<meta charset=koi8-r `, ""},   // cut off
		{`<meta charset=`, ""},          // cut off
		{`<meta charset=koi8-r >`, "KOI8-R"},

		// need pragma
		{`<meta http-equiv=content-type content="text/html; charset=koi8-r">`, "KOI8-R"},
		{`<meta content="text/html; charset=koi8-r" http-equiv="Content-Type">`, "KOI8-R"},
		{`<meta content="text/html; charset=koi8-r">`, ""},
		{`<meta http-equiv=refresh content="text/html; charset=koi8-r">`, ""},
		{`<meta http-equiv=" content-type" content="charset=koi8-r">`, ""},
		{`<meta http-equiv=content-type content="text/html">`, ""},
		{`<meta http-equiv=content-type content="charset='koi8-r">`, ""},
		{`<meta http-equiv=content-type content="charset=foo">`, ""},
		{`<meta http-equiv=content-type content="charset=utf-16be">`, "UTF-8"},
		{`<meta http-equiv=content-type content="charset=koi8-r" charset=utf-8>`, "UTF-8"},
		{`<meta charset=utf-8 http-equiv=content-type content="charset=koi8-r">`, "UTF-8"},
		{`<meta charset=foo http-equiv=content-type content="charset=koi8-r">`, ""}, // charset is failure
		{`<meta http-equiv=content-type content="charset=koi8-r" charset=foo>`, ""},
		{`<meta http-equiv=content-type content="charset=foo" charset=koi8-r>`, "KOI8-R"},
		{`<meta content="charset=koi8-r" content="charset=utf-8" http-equiv=content-type>`, "KOI8-R"},
		{`<meta content="charset=foo" content="charset=utf-8" http-equiv=content-type>`, ""}, // duplicate
		{`<meta charset=foo charset=koi8-r>`, ""},                                            // duplicate
		{`<meta http-equiv=x http-equiv=content-type content="charset=koi8-r">`, ""},         // duplicate
		{`<meta http-equiv=content-type content="charset=koi8-r">`, "KOI8-R"},

		// comments
		{`<!-- <meta charset=koi8-r> -->`, ""},
		{`<!-- <meta charset=koi8-r> --><meta charset=utf-8>`, "UTF-8"},
		{`<!--><meta charset=koi8-r>`, "KOI8-R"},
		{`<!---><meta charset=koi8-r>`, "KOI8-R"},
		{`<!-- --!><meta charset=koi8-r> -->`, ""},
		{`<!-- <meta charset=koi8-r>`, ""},
		{`<!--<meta charset=koi8-r>-->`, ""},

		// other markup
		{`<!DOCTYPE html><meta charset=koi8-r>`, "KOI8-R"},
		{`<!x <meta charset=koi8-r>`, ""},
		{`<!x> <meta charset=koi8-r>`, "KOI8-R"},
		{`<? <meta charset=koi8-r> ?>`, ""},
		{`</ <meta charset=koi8-r>`, ""},
		{`</x <meta charset=koi8-r>`, ""},
		{`</meta charset=koi8-r>`, ""},
		{`<title x="<meta charset=utf-8>"><meta charset=koi8-r>`, "KOI8-R"},
		{`<title><meta charset=koi8-r></title>`, "KOI8-R"},
		{`<script>"<meta charset=koi8-r>"</script>`, "KOI8-R"},
		{`<p title=<meta charset=koi8-r>`, ""},
		{`<p title=x <meta charset=koi8-r>`, ""},
		{`<p><meta charset=koi8-r>`, "KOI8-R"},
		{`< meta charset=koi8-r>`, ""},
		{`<<meta charset=koi8-r>`, "KOI8-R"},
		{`<1 <meta charset=koi8-r>`, "KOI8-R"},
		{"\xEF\xBB\xBF<meta charset=koi8-r>", "KOI8-R"}, // the BOM isn't checked
		{"<p>\x00<meta charset=koi8-r>", "KOI8-R"},

		// XML declarations
		{"<\x00?\x00x\x00m\x00l\x00", "UTF-16LE"},
		{"\x00<\x00?\x00x\x00m\x00l", "UTF-16BE"},
		{"<\x00?\x00x\x00", "UTF-16LE"},
		{"<\x00?\x00X\x00", ""},
		{"\xFF\xFE<\x00?\x00x\x00", ""},
		{`<?xml version="1.0" encoding="koi8-r"?>`, "KOI8-R"},
		{`<?xml version="1.0" encoding='koi8-r'?>`, "KOI8-R"},
		{`<?xml encoding = "koi8-r"?>`, "KOI8-R"},
		{"<?xml encoding\x01=\x02'koi8-r'?>", "KOI8-R"},
		{`<?xml version="1.0"?>`, ""},
		{`<?xml version="1.0" encoding="utf-16"?>`, "UTF-8"},
		{`<?xml version="1.0" encoding="x-user-defined"?>`, "x-user-defined"},
		{`<?xml version="1.0" encoding="foo"?>`, ""},
		{`<?xml version="1.0" encoding=koi8-r?>`, ""},
		{`<?xml version="1.0" encoding="koi8-r ?>`, ""},
		{`<?xml version="1.0" encoding=" koi8-r"?>`, ""},
		{`<?xml version="1.0" encoding="koi8>r"?>`, ""},
		{`<?xml version="1.0" ENCODING="koi8-r"?>`, ""},
		{`<?xml version="1.0"?> encoding="koi8-r"`, ""},
		{`<?xml version="1.0" encoding="koi8-r"`, ""},
		{`<?xml encoding="koi8-r">`, "KOI8-R"},
		{`<?xmlencoding="koi8-r"?>`, "KOI8-R"},
		{`<?XML version="1.0" encoding="koi8-r"?>`, ""},
		{` <?xml version="1.0" encoding="koi8-r"?>`, ""},
		{"\xEF\xBB\xBF<?xml version=\"1.0\" encoding=\"koi8-r\"?>", ""},
		{`<?xml version="1.0" encoding="koi8-r"?><meta charset=utf-8>`, "UTF-8"},
		{`<?xml version="1.0" encoding="koi8-r"?><meta charset=foo>`, "KOI8-R"},
	} {
		name, ok := PrescanCharset([]byte(tc.head))
		if name != tc.name || ok != (tc.name != "") {
			t.Errorf("%q: got %q, %v, want %q", tc.head, name, ok, tc.name)
		}
	}
}
