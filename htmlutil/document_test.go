package htmlutil

import (
	"strings"
	"testing"

	"github.com/pgaskin/htmlstream"
	"github.com/pgaskin/htmlstream/ascii"
)

func TestDocument(t *testing.T) {
	for _, tc := range []struct {
		in        string
		out       string // part of each start tag and non-space text, and events
		scripting bool
		mode      htmlstream.Mode
	}{
		{`<title>t</title><p>x`, `[html head] title:InHead #:InHead [headend body] p:InBody #:InBody`, false, htmlstream.HTML},
		{`<html><head><title>t</title></head> <link><body>x`, `html:BeforeHead head:InHead title:InHead #:InHead [headend] link:AfterHead body:InBody #:InBody`, false, htmlstream.HTML},
		{`<!doctype html><html lang=en>  hello`, `html:BeforeHead [head headend body] #:InBody`, false, htmlstream.HTML},
		{`<head><script>x</script>text`, `[html] head:InHead script:InHead #:InHead [headend] [body] #:InBody`, false, htmlstream.HTML},
		{`</br>x<body><html>`, `[html head headend body] #:InBody [misplaced] body:InBody [misplaced] html:InBody`, false, htmlstream.HTML},
		{`<meta charset=utf-8>`, `[html head] meta:InHead [headend body]`, false, htmlstream.HTML},
		{`<title>t</title><b x=`, `[html head] title:InHead #:InHead b:InHead [headend body]`, false, htmlstream.HTML},
		{`<svg></svg>`, `[html head headend body] svg:InBody`, false, htmlstream.HTML},
		{"\uFEFF<title>t</title>", `#:BeforeHTML [html head] title:InHead #:InHead [headend body]`, false, htmlstream.HTML},
		{`&#32;<title>t</title>`, `#:BeforeHTML [html head] title:InHead #:InHead [headend body]`, false, htmlstream.HTML},
		{`<html><head></head></html><title>t</title>`, `html:BeforeHead head:InHead [headend] [body] title:InBody #:InBody`, false, htmlstream.HTML},
		{`<template><div>x</div><body><p>y</template>z`, `[html head] template:InHead div:InHead #:InHead [misplaced] body:InHead p:InHead #:InHead [headend body] #:InBody`, false, htmlstream.HTML},
		{`</head><noscript>x`, `[html head headend] [body] noscript:InBody #:InBody`, false, htmlstream.HTML},
		{`<title>t</title></head><link><noscript></head>x`, `[html head] title:InHead #:InHead [headend] link:AfterHead [body] noscript:InBody #:InBody`, false, htmlstream.HTML},
		{`<noscript></head></noscript></head><title>t</title>`, `[html head] noscript:InHead [headend] title:AfterHead #:AfterHead [body]`, false, htmlstream.HTML},
		{`<noscript></head></noscript></head><title>t</title>`, `[html head] noscript:InHead #:InHead [headend] title:AfterHead #:AfterHead [body]`, true, htmlstream.HTML},
		{`<head></head><template><html>x</template>`, `[html] head:InHead [headend] template:AfterHead [misplaced] html:AfterHead #:AfterHead [body]`, false, htmlstream.HTML},
		// in Universal mode, like in HTML mode for documents read as HTML,
		// and only the explicit html, head and body elements under XML rules
		{`<title>t</title></HEAD><link>`, `[html head] title:InHead #:InHead [headend] link:AfterHead [body]`, false, htmlstream.Universal},
		{`</BR>x`, `[html head headend body] #:InBody`, false, htmlstream.Universal},
		{`<head/><title>t</title><noscript>x`, `[html headend] head:AfterHead title:AfterHead #:AfterHead [body] noscript:InBody #:InBody`, false, htmlstream.Universal},
		{`<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml"><title>t</title><p>x</p></html>`, `html:BeforeHead title:BeforeHead #:BeforeHead p:BeforeHead #:BeforeHead`, false, htmlstream.Universal},
		{`<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml"><head><meta charset="utf-8"><title>t</title></head><body>x</body></html>`, `html:BeforeHead head:InHead meta:InHead title:InHead #:InHead [headend] body:InBody #:InBody`, false, htmlstream.Universal},
		{`<?xml version="1.0"?> <!-- c --><p xmlns="http://www.w3.org/1999/xhtml">x</p>`, `p:BeforeHTML #:BeforeHTML`, false, htmlstream.Universal},
		{`<?xml version="1.0"?> <p>x</p>`, `[html head headend body] p:InBody #:InBody`, false, htmlstream.Universal},
		{`x<html xmlns="http://www.w3.org/1999/xhtml"><title>t</title>`, `#:BeforeHTML html:BeforeHead title:BeforeHead #:BeforeHead`, false, htmlstream.XML},
		{`<html xmlns="http://www.w3.org/1999/xhtml"><head><title>t</title></head><body><p>x</p></body></html>`, `html:BeforeHead head:InHead title:InHead #:InHead [headend] body:InBody p:InBody #:InBody`, false, htmlstream.XML},
		{`<h:html xmlns:h="http://www.w3.org/1999/xhtml"><h:head><h:title>t</h:title></h:head></h:html>`, `h:html:BeforeHead h:head:InHead h:title:InHead #:InHead [headend]`, false, htmlstream.XML},
		{`<html xmlns="http://www.w3.org/1999/xhtml"><head/><body/></html>`, `html:BeforeHead [headend] head:AfterHead body:InBody`, false, htmlstream.XML},
		{`<html xmlns="http://www.w3.org/1999/xhtml"><head></head><p>x</p><body>y</body></html>`, `html:BeforeHead head:InHead [headend] p:AfterHead #:AfterHead body:InBody #:InBody`, false, htmlstream.XML},
		{`<html xmlns="http://www.w3.org/1999/xhtml"><head><body>x</body></head></html>`, `html:BeforeHead head:InHead body:InHead #:InHead [headend]`, false, htmlstream.XML},
		{`<html xmlns="http://www.w3.org/1999/xhtml"><body><head><title>t</title></head>x</body></html>`, `html:BeforeHead body:InBody head:InBody title:InBody #:InBody #:InBody`, false, htmlstream.XML},
		{`<html xmlns="http://www.w3.org/1999/xhtml"><div><head><title>t</title></head></div></html>`, `html:BeforeHead div:BeforeHead head:BeforeHead title:BeforeHead #:BeforeHead`, false, htmlstream.XML},
		{`<html xmlns="http://www.w3.org/1999/xhtml"><HEAD><title>t</title></HEAD><body>x</body></html>`, `html:BeforeHead HEAD:BeforeHead title:BeforeHead #:BeforeHead body:InBody #:InBody`, false, htmlstream.XML},
		{`<html xmlns="http://www.w3.org/1999/xhtml"><head><title>t`, `html:BeforeHead head:InHead title:InHead #:InHead [headend]`, false, htmlstream.XML},
		{`<html><head><title>t</title></head><body>x</body></html>`, `html:BeforeHTML head:BeforeHTML title:BeforeHTML #:BeforeHTML body:BeforeHTML #:BeforeHTML`, false, htmlstream.XML},
	} {
		r := htmlstream.NewReader(strings.NewReader(tc.in))
		r.SetMode(tc.mode)
		r.SetScripting(tc.scripting)
		var d Document
		var got []string
		events := func(ev DocumentEvent) {
			var e []string
			for _, x := range []struct {
				ev DocumentEvent
				s  string
			}{{ImpliedHTML, "html"}, {ImpliedHead, "head"}, {HeadEnd, "headend"}, {ImpliedBody, "body"}, {MisplacedTag, "misplaced"}} {
				if ev&x.ev != 0 {
					e = append(e, x.s)
				}
			}
			if len(e) != 0 {
				got = append(got, "["+strings.Join(e, " ")+"]")
			}
		}
		for r.Next() {
			tok := r.Token()
			events(d.Update(tok))
			switch tok.Kind() {
			case htmlstream.StartTag:
				got = append(got, string(tok.Name())+":"+d.Part().String())
			case htmlstream.Text:
				if !ascii.IsBlank(tok.Data()) {
					got = append(got, "#:"+d.Part().String())
				}
			}
		}
		events(d.End())
		if g := strings.Join(got, " "); g != tc.out {
			t.Errorf("%s %q, scripting %v:\n got %s\nwant %s", tc.mode, tc.in, tc.scripting, g, tc.out)
		}
	}
}

func TestDocumentQuirks(t *testing.T) {
	for _, tc := range []struct {
		in     string
		quirks bool
	}{
		{``, true},
		{`<p>x`, true},
		{`<!DOCTYPE html>`, false},
		{`<!doctype HTML>`, false},
		{"<!-- c -->\n <!DOCTYPE html><p>", false},
		{`<?xml version="1.0"?><!DOCTYPE html>`, false},
		{`x<!DOCTYPE html>`, true},
		{`</p><!DOCTYPE html>`, true},
		{`<!DOCTYPE html><!DOCTYPE foo>`, false},
		{`<!DOCTYPE>`, true},
		{`<!DOCTYPE foo>`, true},
		{`<!DOCTYPE html`, true},
		{`<!DOCTYPE html x>`, true},
		{`<!DOCTYPE html PUBLIC "-//W3C//DTD HTML 4.01//EN">`, false},
		{`<!DOCTYPE html PUBLIC "-//W3C//DTD HTML 4.01//EN" "http://www.w3.org/TR/html4/strict.dtd">`, false},
		{`<!DOCTYPE html PUBLIC "-//W3C//DTD HTML 4.01 Transitional//EN">`, true},
		{`<!DOCTYPE html PUBLIC "-//W3C//DTD HTML 4.01 Transitional//EN" "http://www.w3.org/TR/html4/loose.dtd">`, false},
		{`<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Transitional//EN" "http://www.w3.org/TR/xhtml1/DTD/xhtml1-transitional.dtd">`, false},
		{`<!DOCTYPE HTML PUBLIC "-//W3C//DTD HTML 3.2 Final//EN">`, true},
		{`<!DOCTYPE html PUBLIC 'html'>`, true},
		{`<!DOCTYPE html PUBLIC "-/W3C/DTD HTML 4.0 Transitional/EN" "x">`, true},
		{`<!DOCTYPE html SYSTEM "http://www.ibm.com/data/dtd/v11/ibmxhtml1-transitional.dtd">`, true},
		{`<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Transitional//EN" "http://www.ibm.com/data/dtd/v11/ibmxhtml1-transitional.dtd">`, true},
		{`<!DOCTYPE html PUBLIC "-//W3C//DTD HTML 4.01 Frameset//EN" "HTTP://WWW.IBM.COM/data/dtd/v11/ibmxhtml1-transitional.dtd">`, true},
		{`<!DOCTYPE html SYSTEM "about:legacy-compat">`, false},
		{`<!DOCTYPE html SYSTEM "about:legacy-compat" x>`, false},
		{`<!DOCTYPE html SYSTEM about:legacy-compat>`, true},
		{`<!DOCTYPE html PUBLIC "x>"`, true},
		{`<!DOCTYPE html PUBLIC "x"y>`, true},
		{`<!DOCTYPE html PUBLIC>`, true},
		{`<!DOCTYPEhtml>`, false},
	} {
		r := htmlstream.NewReader(strings.NewReader(tc.in))
		var d Document
		for r.Next() {
			d.Update(r.Token())
		}
		if d.Quirks() != tc.quirks {
			t.Errorf("%q: quirks %v, want %v", tc.in, d.Quirks(), tc.quirks)
		}
	}
	// XML parsers never use quirks mode.
	for _, tc := range []struct {
		mode htmlstream.Mode
		in   string
	}{
		{htmlstream.XML, `<p>x</p>`},
		{htmlstream.Universal, `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml"><p>x</p></html>`},
	} {
		r := htmlstream.NewReader(strings.NewReader(tc.in))
		r.SetMode(tc.mode)
		var d Document
		for r.Next() {
			d.Update(r.Token())
		}
		if d.Quirks() {
			t.Errorf("%s %q: quirks", tc.mode, tc.in)
		}
	}
}

func TestDocumentStarted(t *testing.T) {
	for _, tc := range []struct {
		in      string
		started bool
	}{
		{``, false},
		{"\uFEFF \n<!-- c --><?x?>", false},
		{`<!DOCTYPE html>`, true},
		{`x`, true},
		{`<p>`, true},
		{`</p>`, true},
	} {
		r := htmlstream.NewReader(strings.NewReader(tc.in))
		var d Document
		for r.Next() {
			d.Update(r.Token())
		}
		if d.Started() != tc.started {
			t.Errorf("%q: started %v, want %v", tc.in, d.Started(), tc.started)
		}
	}
}

func TestDocumentString(t *testing.T) {
	for ev, want := range map[DocumentEvent]string{
		0:                         "0",
		HeadEnd:                   "HeadEnd",
		ImpliedHTML | ImpliedBody: "ImpliedHTML|ImpliedBody",
		MisplacedTag | 0x80:       "MisplacedTag|0x80",
		0x60:                      "0x60",
	} {
		if got := ev.String(); got != want {
			t.Errorf("DocumentEvent(%#x): got %q, want %q", uint8(ev), got, want)
		}
	}
	for p, want := range map[DocumentPart]string{BeforeHTML: "BeforeHTML", InBody: "InBody", 5: "DocumentPart(5)"} {
		if got := p.String(); got != want {
			t.Errorf("DocumentPart(%d): got %q, want %q", uint8(p), got, want)
		}
	}
}
