package htmlutil

import (
	"fmt"
	"math"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/pgaskin/htmlstream"
)

func TestURL(t *testing.T) {
	if got := CleanURL(" \x01 /a\tb\nc \x00"); got != "/abc" {
		t.Errorf("CleanURL: %q", got)
	}
	page, _ := url.Parse("http://example.com/dir/page.html#frag")
	if u, err := ResolveURL(page, ""); err != nil || u.String() != "http://example.com/dir/page.html" {
		t.Errorf("ResolveURL empty: %v %v", u, err)
	}
	if u, err := ResolveURL(page, "../x?y#z"); err != nil || u.String() != "http://example.com/x?y#z" {
		t.Errorf("ResolveURL: %v %v", u, err)
	}

	r := htmlstream.NewReader(strings.NewReader(`<base target=_blank><base href="javascript:x"><base href="/b/"><a href=x>`))
	base := NewBaseURL(page)
	var set []bool
	for r.Next() {
		if r.Token().Kind() == htmlstream.StartTag {
			set = append(set, base.Update(r.Token()))
		}
	}
	if !slices.Equal(set, []bool{false, false, false, false}) || base.URL() != page {
		t.Errorf("javascript base: set %v, base %v", set, base.URL())
	}
	r = htmlstream.NewReader(strings.NewReader(`<base href="/b/"><img src=x srcset="a.png 1x, b.png 2x"><a href=y>`))
	base = NewBaseURL(page)
	var links []string
	for r.Next() {
		tok := r.Token()
		base.Update(tok)
		for name, a := range URLAttrs(tok) {
			if IsSrcset(a) {
				for c := range ParseSrcset(a.Value()) {
					u, _ := base.Resolve(c.URL)
					links = append(links, u.String()+" "+c.Descriptor)
				}
				continue
			}
			u, _ := base.Resolve(a.Value())
			links = append(links, name+"="+u.String())
		}
	}
	want := []string{"href=http://example.com/b/", "src=http://example.com/b/x", "http://example.com/b/a.png 1x", "http://example.com/b/b.png 2x", "href=http://example.com/b/y"}
	if !slices.Equal(links, want) {
		t.Errorf("links:\n got %q\nwant %q", links, want)
	}

	// background on the table parts listed in the rendering section
	r = htmlstream.NewReader(strings.NewReader(`<table background=t><thead background=h><tr background=r><td background=d></thead><tbody background=b></tbody><tfoot background=f><col background=no>`))
	var bg []string
	for r.Next() {
		for _, a := range URLAttrs(r.Token()) {
			bg = append(bg, a.Value())
		}
	}
	if want := []string{"t", "h", "r", "d", "b", "f"}; !slices.Equal(bg, want) {
		t.Errorf("background: got %q, want %q", bg, want)
	}
}

func TestParseSrcset(t *testing.T) {
	// The single-candidate cases (except the clamped width) were checked
	// against Chromium with img.currentSrc.
	for _, tc := range []struct {
		in   string
		want []string // URL|Descriptor|Width|Density|Height
	}{
		{"", nil},
		{" ,, \t\n", nil},
		{"a", []string{"a||0|1|0"}},
		{"a ,", []string{"a||0|1|0"}},
		{"a,,,", []string{"a||0|1|0"}},
		{"a 1x,", []string{"a|1x|0|1|0"}},
		{"a 1x,,", []string{"a|1x|0|1|0"}},

		// a URL ends at whitespace, not at a comma
		{" a.png 100w,b.png,data:image/png;base64,x,y 2x, c.png (min-width: 1px) 1x ,d.png 1x", []string{
			"a.png|100w|100|0|0", "b.png,data:image/png;base64,x,y|2x|0|2|0", "d.png|1x|0|1|0",
		}},
		{"a,b, c,d 2x", []string{"a,b||0|1|0", "c,d|2x|0|2|0"}},
		{"a 2x, b 1x", []string{"a|2x|0|2|0", "b|1x|0|1|0"}},

		// x
		{"a 0x", []string{"a|0x|0|0|0"}},
		{"a -0x", []string{"a|-0x|0|0|0"}},
		{"a 00x", []string{"a|00x|0|0|0"}},
		{"a 0.0x", []string{"a|0.0x|0|0|0"}},
		{"a .5x", []string{"a|.5x|0|0.5|0"}},
		{"a 1.25x", []string{"a|1.25x|0|1.25|0"}},
		{"a 1e1x", []string{"a|1e1x|0|10|0"}},
		{"a 1E-1x", []string{"a|1E-1x|0|0.1|0"}},
		{"a 1e+1x", []string{"a|1e+1x|0|10|0"}},
		{"a 1e-400x", []string{"a|1e-400x|0|0|0"}},
		{"a -1e-400x", []string{"a|-1e-400x|0|0|0"}},
		{"a -1x", nil},
		{"a -.5x", nil},
		{"a 1.x", nil},
		{"a +1x", nil},
		{"a 1e400x", nil},
		{"a 1e1.5x", nil},
		{"a 1e-x", nil},
		{"a 1X", nil},
		{"a x", nil},
		{"a 1xx", nil},
		{"a 0x1x", nil},
		{"a Infinityx", nil},
		{"a NaNx", nil},
		{"a 2x 2x", nil},

		// w and h
		{"a 100w", []string{"a|100w|100|0|0"}},
		{"a 010w", []string{"a|010w|10|0|0"}},
		{"a 99999999999999999999w", []string{fmt.Sprintf("a|99999999999999999999w|%d|0|0", math.MaxInt)}},
		{"a 100w 50h", []string{"a|100w 50h|100|0|50"}},
		{"a 50h 100w", []string{"a|50h 100w|100|0|50"}},
		{"a  100w\t50h  ,b", []string{"a|100w 50h|100|0|50", "b||0|1|0"}},
		{"a 0w", nil},
		{"a +1w", nil},
		{"a -1w", nil},
		{"a 1.0w", nil},
		{"a 100W", nil},
		{"a 1w2", nil},
		{"a 100w 100w", nil},
		{"a 50h", nil},
		{"a 100w 0h", nil},
		{"a 100w 50h 50h", nil},
		{"a 1x 100w", nil},
		{"a 100w 1x", nil},
		{"a 1x 50h", nil},
		{"a 1x 50h 100w", nil},

		// parentheses
		{"a (x) 1x, b", []string{"b||0|1|0"}},
		{"a 1x (x), b", []string{"b||0|1|0"}},
		{"a (x, y) 1x, b", []string{"b||0|1|0"}},
		{"a 1x(x, y), b", []string{"b||0|1|0"}},
		{"a 1x (x, b", nil},
		{"a 1x foo, b", []string{"b||0|1|0"}},
	} {
		var got, spec []string
		for c := range ParseSrcset(tc.in) {
			got = append(got, fmt.Sprintf("%s|%s|%d|%g|%d", c.URL, c.Descriptor, c.Width, c.Density, c.Height))
		}
		for _, c := range parseSrcsetSpec(tc.in) {
			spec = append(spec, fmt.Sprintf("%s|%s|%d|%g|%d", c.URL, c.Descriptor, c.Width, c.Density, c.Height))
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%q:\n got %q\nwant %q", tc.in, got, tc.want)
		}
		if !slices.Equal(spec, tc.want) {
			t.Errorf("%q: transcription:\n got %q\nwant %q", tc.in, spec, tc.want)
		}
	}
}

func TestURLAttrsSVG(t *testing.T) {
	for _, tc := range []struct {
		mode htmlstream.Mode
		in   string
		want []string
	}{
		{htmlstream.HTML, `<svg><image xlink:href=a /><FEIMAGE Xlink:Href=b /><use href=c xlink:href=no /><a href=d></a><g href=no /></svg><math><mi xlink:href=no /></math>`,
			[]string{"xlink:href=a", "xlink:href=b", "href=c", "href=d"}},
		// the prefix is resolved from the declarations in XML mode, and
		// element names are case-sensitive
		{htmlstream.XML, `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xl="http://www.w3.org/1999/xlink"><image xl:href="a"/><feImage xl:href="b"/><feimage href="no"/><use xlink:href="no"/><s:a xmlns:s="http://www.w3.org/2000/svg" href="c"/></svg>`,
			[]string{"xlink:href=a", "xlink:href=b", "href=c"}},
		{htmlstream.XML, `<html xmlns="http://www.w3.org/1999/xhtml"><body background="a"><a href="b"/><image href="no"/></body></html>`,
			[]string{"background=a", "href=b"}},
		// HTML parsers put xlink:href in the XLink namespace whatever the
		// declarations say
		{htmlstream.HTML, `<svg xmlns:xlink="u"><image xlink:href=a /><use XMLNS:XLINK="" xlink:href=b /></svg>`,
			[]string{"xlink:href=a", "xlink:href=b"}},
		// HTML parsers put only xlink:href in the XLink namespace, not other
		// prefixes bound to it
		{htmlstream.HTML, `<svg xmlns:x="http://www.w3.org/1999/xlink"><image x:href=no xlink:href=a /><use x:href=no /></svg>`,
			[]string{"xlink:href=a"}},
		{htmlstream.Universal, `<svg xmlns:xlink="u"><image xlink:href=a /></svg><svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="u"><image xlink:href="b"/></svg>`,
			[]string{"xlink:href=a", "xlink:href=b"}},
		{htmlstream.Universal, `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml"><svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="u"><image xlink:href="no"/></svg></html>`,
			nil},
		{htmlstream.XML, `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="u"><image xlink:href="no"/></svg>`,
			nil},
	} {
		r := htmlstream.NewReader(strings.NewReader(tc.in))
		r.SetMode(tc.mode)
		var got []string
		for r.Next() {
			for name, a := range URLAttrs(r.Token()) {
				got = append(got, name+"="+a.Value())
			}
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s:\n got %q\nwant %q", tc.in, got, tc.want)
		}
	}
}

func TestURLAttrsImage(t *testing.T) {
	// HTML parsers turn an HTML image start tag into img; XML parsers keep
	// image.
	const xhtml = `<html xmlns="http://www.w3.org/1999/xhtml">`
	for _, tc := range []struct {
		mode htmlstream.Mode
		in   string
		want []string
	}{
		{htmlstream.HTML, `<image src=a srcset="b 2x" longdesc=c alt=no><IMAGE SRC=d><svg><image src=no href=e /></svg>`,
			[]string{"src=a", "srcset=b 2x", "longdesc=c", "src=d", "href=e"}},
		{htmlstream.HTML, xhtml + `<image src="a"/></html>`, []string{"src=a"}},
		{htmlstream.Universal, `<image src=a><svg><image src=no /></svg>`, []string{"src=a"}},
		{htmlstream.Universal, xhtml + `<image src="a"/></html>`, []string{"src=a"}},
		{htmlstream.Universal, `<?xml version="1.0"?>` + xhtml + `<image src="no"/></html>`, nil},
		{htmlstream.XML, xhtml + `<image src="no"/></html>`, nil},
	} {
		r := htmlstream.NewReader(strings.NewReader(tc.in))
		r.SetMode(tc.mode)
		var got []string
		for r.Next() {
			for name, a := range URLAttrs(r.Token()) {
				got = append(got, name+"="+a.Value())
			}
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s %q:\n got %q\nwant %q", tc.mode, tc.in, got, tc.want)
		}
	}
}

func TestBaseURLSeen(t *testing.T) {
	r := htmlstream.NewReader(strings.NewReader(`<base href="javascript:x"><base href=/y>`))
	b := NewBaseURL(nil)
	for r.Next() {
		b.Update(r.Token())
	}
	if !b.Seen() || b.URL() != nil {
		t.Errorf("Seen %v, URL %v", b.Seen(), b.URL())
	}
}

func TestBaseURLTemplate(t *testing.T) {
	page, _ := url.Parse("http://example.com/a/b")
	r := htmlstream.NewReader(strings.NewReader(`<template><base href=/t></template><base href=/x/>`))
	b := NewBaseURL(page)
	for r.Next() {
		b.Update(r.Token())
	}
	if got := b.URL().String(); got != "http://example.com/x/" {
		t.Errorf("got %s", got)
	}
}

// FuzzParseSrcset compares ParseSrcset with a direct transcription of the
// HTML Standard algorithm.
func FuzzParseSrcset(f *testing.F) {
	for _, s := range []string{
		"a 1x, b 2x", "a 100w 50h,b", "a (x, y) 1x, b", "a,b,, c 1e1x", "a 1x (", "a\t1x\f,b -0x",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		var got []SrcsetCandidate
		for c := range ParseSrcset(s) {
			got = append(got, c)
		}
		if want := parseSrcsetSpec(s); !slices.Equal(got, want) {
			t.Errorf("%q:\n got %+v\nwant %+v", s, got, want)
		}
	})
}

var (
	srcsetIntRe   = regexp.MustCompile(`^[0-9]+$`)
	srcsetFloatRe = regexp.MustCompile(`^-?([0-9]+|[0-9]+\.[0-9]+|\.[0-9]+)([eE][-+]?[0-9]+)?$`)
)

func parseSrcsetSpec(input string) []SrcsetCandidate {
	isSpace := func(c byte) bool { return strings.IndexByte("\t\n\f\r ", c) >= 0 }
	parseInt := func(s string) int {
		n, err := strconv.Atoi(s)
		if err != nil {
			return math.MaxInt
		}
		return n
	}
	var candidates []SrcsetCandidate
	pos := 0
	for {
		for pos < len(input) && (isSpace(input[pos]) || input[pos] == ',') {
			pos++
		}
		if pos >= len(input) {
			return candidates
		}
		start := pos
		for pos < len(input) && !isSpace(input[pos]) {
			pos++
		}
		url := input[start:pos]
		var descriptors []string
		if strings.HasSuffix(url, ",") {
			url = strings.TrimRight(url, ",")
		} else {
			for pos < len(input) && isSpace(input[pos]) {
				pos++
			}
			var cur []byte
			state := "in descriptor"
		tokenizer:
			for ; ; pos++ {
				eof := pos >= len(input)
				var c byte
				if !eof {
					c = input[pos]
				}
				switch state {
				case "in descriptor":
					switch {
					case eof:
						if len(cur) != 0 {
							descriptors = append(descriptors, string(cur))
						}
						break tokenizer
					case isSpace(c):
						if len(cur) != 0 {
							descriptors = append(descriptors, string(cur))
							cur = nil
						}
						state = "after descriptor"
					case c == ',':
						pos++
						if len(cur) != 0 {
							descriptors = append(descriptors, string(cur))
						}
						break tokenizer
					case c == '(':
						cur = append(cur, c)
						state = "in parens"
					default:
						cur = append(cur, c)
					}
				case "in parens":
					switch {
					case eof:
						descriptors = append(descriptors, string(cur))
						break tokenizer
					case c == ')':
						cur = append(cur, c)
						state = "in descriptor"
					default:
						cur = append(cur, c)
					}
				case "after descriptor":
					switch {
					case eof:
						break tokenizer
					case isSpace(c):
					default:
						state = "in descriptor"
						pos--
					}
				}
			}
		}
		var (
			bad                    bool
			width, height          int
			density                float64
			hasW, hasX, hasFutureH bool
		)
		for _, d := range descriptors {
			num, k := d[:len(d)-1], d[len(d)-1]
			switch {
			case k == 'w' && srcsetIntRe.MatchString(num):
				if hasW || hasX {
					bad = true
				}
				if width = parseInt(num); width == 0 {
					bad = true
				}
				hasW = true
			case k == 'x' && srcsetFloatRe.MatchString(num):
				if hasW || hasX || hasFutureH {
					bad = true
				}
				v, err := strconv.ParseFloat(num, 64)
				if err != nil || v < 0 {
					bad = true
				}
				density, hasX = math.Abs(v), true
			case k == 'h' && srcsetIntRe.MatchString(num):
				if hasFutureH || hasX {
					bad = true
				}
				if height = parseInt(num); height == 0 {
					bad = true
				}
				hasFutureH = true
			default:
				bad = true
			}
		}
		if hasFutureH && !hasW {
			bad = true
		}
		if !bad {
			if !hasW && !hasX {
				density = 1
			}
			candidates = append(candidates, SrcsetCandidate{
				URL:        url,
				Descriptor: strings.Join(descriptors, " "),
				Width:      width,
				Density:    density,
				Height:     height,
			})
		}
	}
}
