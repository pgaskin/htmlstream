package polyglot_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/pgaskin/htmlstream"
	"github.com/pgaskin/htmlstream/htmlutil"
	"github.com/pgaskin/htmlstream/polyglot"
)

// Convert writes the implied elements and end tags, quotes attributes, closes
// void elements, replaces character references, declares the namespaces and
// the encoding, and reopens misnested formatting elements.
func ExampleConvert() {
	const page = `<!doctype html>
<html lang=en><title>Fish &amp; chips</title>
<P CLASS=intro>Fish&nbsp;&amp;&nbsp;chips<br>
<IMG SRC=fish.png alt=Fish>
<table><tr><td>Cod<td>&pound;8</table>
<p><b>bold <i>both</b> italic</i>`

	if err := polyglot.Convert(os.Stdout, strings.NewReader(page), polyglot.Options{}); err != nil {
		panic(err)
	}
	// Output:
	// <!DOCTYPE html>
	// <html lang="en" xmlns="http://www.w3.org/1999/xhtml" xml:lang="en"><head><meta charset="UTF-8" /><title>Fish &amp; chips</title>
	// </head><body><p class="intro">Fish&#160;&amp;&#160;chips<br />
	// <img src="fish.png" alt="Fish" />
	// </p><table><tbody><tr><td>Cod</td><td>£8</td></tr></tbody></table>
	// <p><b>bold <i>both</i></b><i> italic</i></p></body></html>
}

// Input which polyglot markup can't represent exactly is reported through
// Options.Warn.
func ExampleConvert_warnings() {
	const page = `<p xmlns:epub="http://www.idpf.org/2007/ops" epub:type="note">Note</p>
<table>stray text<tr><td>cell</td></tr></table>`

	var b bytes.Buffer
	err := polyglot.Convert(&b, strings.NewReader(page), polyglot.Options{
		DoctypeMode: polyglot.NoDoctype,
		Warn: func(w polyglot.Warning) {
			fmt.Printf("offset %d: %s\n", w.Pos, w.Msg)
		},
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(b.String())
	// Output:
	// offset 0: attribute "epub:type" is in namespace "http://www.idpf.org/2007/ops" for XML parsers and in no namespace for HTML parsers (will not warn again for this prefix and namespace)
	// offset 78: kept text in a table outside a cell (HTML parsers would have moved it before the table)
	// <html xmlns="http://www.w3.org/1999/xhtml"><head><meta charset="UTF-8" /></head><body><p xmlns:epub="http://www.idpf.org/2007/ops" epub:type="note">Note</p>
	// <table>stray text<tbody><tr><td>cell</td></tr></tbody></table></body></html>
}

// The doctype of the input can be kept or replaced, and an XML declaration
// added.
func ExampleConvert_doctype() {
	const page = `<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.1//EN" "http://www.w3.org/TR/xhtml11/DTD/xhtml11.dtd"><p>Text`

	for _, opt := range []polyglot.Options{
		{},
		{DoctypeMode: polyglot.KeepDoctype, XMLDecl: polyglot.AddXMLDecl},
		{Doctype: "<!DOCTYPE html SYSTEM \"about:legacy-compat\">"},
		{DoctypeMode: polyglot.NoDoctype},
	} {
		var b bytes.Buffer
		if err := polyglot.Convert(&b, strings.NewReader(page), opt); err != nil {
			panic(err)
		}
		head, _, _ := strings.Cut(b.String(), "<html")
		fmt.Printf("%q\n", head)
	}
	// Output:
	// "<!DOCTYPE html>\n"
	// "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<!DOCTYPE html PUBLIC \"-//W3C//DTD XHTML 1.1//EN\" \"http://www.w3.org/TR/xhtml11/DTD/xhtml11.dtd\">\n"
	// "<!DOCTYPE html SYSTEM \"about:legacy-compat\">\n"
	// ""
}

// Script and style contents with < or & are wrapped in a CDATA section inside
// a comment, and JSON is escaped instead.
func ExampleConvert_script() {
	const page = `<script>if (a < b && c) go()</script>
<script>ok()</script>
<style>a > b { color: red }</style>
<script type=application/ld+json>{"name": "<Fish & chips>"}</script>`

	if err := polyglot.Convert(os.Stdout, strings.NewReader(page), polyglot.Options{DoctypeMode: polyglot.NoDoctype}); err != nil {
		panic(err)
	}
	// Output:
	// <html xmlns="http://www.w3.org/1999/xhtml"><head><meta charset="UTF-8" /><script>//<![CDATA[
	// if (a < b && c) go()
	// //]]></script>
	// <script>ok()</script>
	// <style>a > b { color: red }</style>
	// <script type="application/ld+json">{"name": "\u003cFish \u0026 chips\u003e"}</script></head><body></body></html>
}

// A Writer converts the tokens of the caller's Reader, here set up like the
// Reader of Convert by Options.NewReader. Leaving out all the tokens of an
// element removes it, and leaving out its start and end tags keeps the
// contents.
func ExampleWriter() {
	const page = `<p>Text<script>track()</script> with <font color=red>red</font> and <span>spans<span>.`

	opt := polyglot.Options{DoctypeMode: polyglot.NoDoctype}
	r := opt.NewReader(strings.NewReader(page))
	pw := polyglot.NewWriter(os.Stdout, opt)
	var unwrapped htmlutil.Stack[struct{}]
	for r.Next() {
		tok := r.Token()
		switch tok.Kind() {
		case htmlstream.StartTag:
			if tok.Is("script") {
				r.Skip()
				continue
			}
			if tok.Is("font") || tok.Is("span") {
				unwrapped.Push(tok, struct{}{})
				continue
			}
		case htmlstream.EndTag:
			if _, ok := unwrapped.Pop(tok); ok {
				continue
			}
		}
		if err := pw.WriteToken(tok); err != nil {
			panic(err)
		}
	}
	if err := r.Err(); err != nil {
		panic(err)
	}
	if err := pw.Close(); err != nil {
		panic(err)
	}
	// Output:
	// <html xmlns="http://www.w3.org/1999/xhtml"><head><meta charset="UTF-8" /></head><body><p>Text with red and spans.</p></body></html>
}

// The Tag hook edits the tags as they are written: here, attributes of links
// and images, a wrapper around tables and the removal of iframes.
func ExampleTag() {
	const page = `<p><a href=https://example.com/>External</a> <a href=/about>About</a></p>
<table><tr><td>1</td></tr></table><iframe src=ad.html></iframe><img src=a.png>`

	err := polyglot.Convert(os.Stdout, strings.NewReader(page), polyglot.Options{
		DoctypeMode: polyglot.NoDoctype,
		OnTag: func(e *polyglot.Tag) {
			if e.NS != htmlstream.NamespaceXHTML {
				return
			}
			switch {
			case e.Start && e.Name == "a" && e.Token != nil:
				if href, ok := e.Token.Attr("href"); ok && strings.HasPrefix(href.Value(), "https://") {
					e.SetAttr("rel", "noopener")
				}
			case e.Start && e.Name == "img":
				e.SetAttr("loading", "lazy")
			case e.Name == "table":
				if e.Start {
					e.InsertBefore([]byte(`<div class="scroll">`))
				} else {
					e.InsertAfter([]byte(`</div>`))
				}
			case e.Start && e.Name == "iframe":
				e.Remove()
			}
		},
	})
	if err != nil {
		panic(err)
	}
	// Output:
	// <html xmlns="http://www.w3.org/1999/xhtml"><head><meta charset="UTF-8" /></head><body><p><a href="https://example.com/" rel="noopener">External</a> <a href="/about">About</a></p>
	// <div class="scroll"><table><tbody><tr><td>1</td></tr></tbody></table></div><img src="a.png" loading="lazy" /></body></html>
}

// The Text hook replaces text as it is written. SetText escapes the new text,
// like the text of the input.
func ExampleText() {
	const page = `<p>It's "fish" &amp; chips.</p><script>if (a < b) alert("x")</script>`

	err := polyglot.Convert(os.Stdout, strings.NewReader(page), polyglot.Options{
		DoctypeMode: polyglot.NoDoctype,
		OnText: func(t *polyglot.Text) {
			if t.Raw || t.Last {
				return // leave script contents alone
			}
			s := strings.NewReplacer("'", "\u2019", ` "`, " \u201c", `"`, "\u201d").Replace(string(t.Data))
			t.SetText([]byte(s))
		},
	})
	if err != nil {
		panic(err)
	}
	// Output:
	// <html xmlns="http://www.w3.org/1999/xhtml"><head><meta charset="UTF-8" /></head><body><p>It’s “fish” &amp; chips.</p><script>//<![CDATA[
	// if (a < b) alert("x")
	// //]]></script></body></html>
}

// SetMarkup replaces text with markup, here with the text around it escaped
// with AppendEscapeText. Text nodes are reported in parts, so a word split
// across two parts isn't matched.
func ExampleText_SetMarkup() {
	const page = `<p>Fish &amp; chips are <b>tasty</b>.</p><script>chips()</script>`

	err := polyglot.Convert(os.Stdout, strings.NewReader(page), polyglot.Options{
		DoctypeMode: polyglot.NoDoctype,
		OnText: func(t *polyglot.Text) {
			if t.Raw || t.Last {
				return
			}
			var b []byte
			for i, word := range bytes.Split(t.Data, []byte("chips")) {
				if i != 0 {
					b = append(b, "<mark>chips</mark>"...)
				}
				b = polyglot.AppendEscapeText(b, word)
			}
			t.SetMarkup(b)
		},
	})
	if err != nil {
		panic(err)
	}
	// Output:
	// <html xmlns="http://www.w3.org/1999/xhtml"><head><meta charset="UTF-8" /></head><body><p>Fish &amp; <mark>chips</mark> are <b>tasty</b>.</p><script>chips()</script></body></html>
}

// Hold holds back the output, here to insert a table of contents built from
// the headings at the start of the body.
func ExampleWriter_Hold() {
	const page = `<h1>Fish</h1><p>Intro.<h2>Cod</h2><p>White.<h2>Salmon &amp; trout</h2><p>Pink.`

	var toc, heading []byte
	var body polyglot.Hold
	inHeading := false
	n := 0
	err := polyglot.Convert(os.Stdout, strings.NewReader(page), polyglot.Options{
		DoctypeMode: polyglot.NoDoctype,
		OnTag: func(e *polyglot.Tag) {
			pw := e.Writer()
			switch {
			case e.Name == "body" && e.Start:
				body = pw.Hold()
				toc = append(toc, "<ul>"...)
			case e.Name == "body":
				toc = append(toc, "</ul>"...)
				pw.Fail(pw.HoldDiscard(body, append(toc, pw.Held(body)...)))
			case e.Name == "h2" && e.Start:
				n++
				e.SetAttr("id", "s"+strconv.Itoa(n))
				inHeading = true
				heading = heading[:0]
			case e.Name == "h2":
				inHeading = false
				toc = append(toc, `<li><a href="#s`+strconv.Itoa(n)+`">`...)
				toc = polyglot.AppendEscapeText(toc, heading)
				toc = append(toc, "</a></li>"...)
			}
		},
		OnText: func(t *polyglot.Text) {
			if inHeading {
				heading = append(heading, t.Data...)
			}
		},
	})
	if err != nil {
		panic(err)
	}
	// Output:
	// <html xmlns="http://www.w3.org/1999/xhtml"><head><meta charset="UTF-8" /></head><body><ul><li><a href="#s1">Cod</a></li><li><a href="#s2">Salmon &amp; trout</a></li></ul><h1>Fish</h1><p>Intro.</p><h2 id="s1">Cod</h2><p>White.</p><h2 id="s2">Salmon &amp; trout</h2><p>Pink.</p></body></html>
}

// With Options.Strict, structurally unsound input is an error instead of being
// converted on a best-effort basis.
func ExampleConvert_strict() {
	for _, page := range []string{
		`<p>Optional end tags<li>are fine`,
		`<div><span>Unclosed span</div>`,
	} {
		err := polyglot.Convert(io.Discard, strings.NewReader(page), polyglot.Options{Strict: true})
		var serr *htmlstream.SyntaxError
		switch {
		case errors.As(err, &serr):
			fmt.Printf("%s: offset %d: %s\n", page, serr.Pos, serr.Msg)
		case err != nil:
			panic(err)
		default:
			fmt.Printf("%s: ok\n", page)
		}
	}
	// Output:
	// <p>Optional end tags<li>are fine: ok
	// <div><span>Unclosed span</div>: offset 24: element "span" at offset 5 closed without its end tag
}
