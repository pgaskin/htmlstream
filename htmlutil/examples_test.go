package htmlutil_test

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/pgaskin/htmlstream"
	"github.com/pgaskin/htmlstream/htmlutil"
)

// Make the URLs of a document absolute, as when saving or embedding a page.
// BaseURL tracks the base element, URLAttrs finds the attributes with URLs,
// and a srcset attribute has a list of them. URLs which can't be resolved are
// left as they are.
func Example() {
	const page = `<base href="/docs/"><a href=intro.html>Intro</a> <a href="http://[bad">Bad</a> <img src=a.png srcset="a-2x.png 2x, http://[bad 3x" alt="A">`

	doc, err := url.Parse("https://example.com/en/index.html")
	if err != nil {
		panic(err)
	}
	base := htmlutil.NewBaseURL(doc)
	x := htmlstream.NewTransformer(os.Stdout, strings.NewReader(page))
	for x.Next() {
		tok := x.Token()
		base.Update(tok)
		for name, a := range htmlutil.URLAttrs(tok) {
			var value string
			if htmlutil.IsSrcset(a) {
				var cs []string
				for c := range htmlutil.ParseSrcset(a.Value()) {
					if u, err := base.Resolve(c.URL); err == nil {
						c.URL = u.String()
					}
					cs = append(cs, strings.TrimSpace(c.URL+" "+c.Descriptor))
				}
				value = strings.Join(cs, ", ")
			} else if u, err := base.Resolve(a.Value()); err == nil {
				value = u.String()
			} else {
				continue
			}
			if err := x.SetAttr(name, value); err != nil {
				panic(err)
			}
		}
	}
	if err := x.Close(); err != nil {
		panic(err)
	}
	// Output:
	// <base href="https://example.com/docs/"><a href="https://example.com/docs/intro.html">Intro</a> <a href="http://[bad">Bad</a> <img src="https://example.com/docs/a.png" srcset="https://example.com/docs/a-2x.png 2x, http://[bad 3x" alt="A">
}

// The element categories classify start and end tags of HTML elements. The
// title in svg is an SVG element.
func ExampleIsBlock() {
	const page = `<p><b><br><td><h2><pre><img><svg><title></title></svg><title></title><script></script>`

	r := htmlstream.NewReader(strings.NewReader(page))
	for r.Next() {
		tok := r.Token()
		if tok.Kind() != htmlstream.StartTag {
			continue
		}
		var cats []string
		for _, c := range []struct {
			name string
			fn   func(*htmlstream.Token) bool
		}{
			{"block", htmlutil.IsBlock[*htmlstream.Token]},
			{"text-break", htmlutil.IsTextBreak[*htmlstream.Token]},
			{"void", htmlutil.IsVoid[*htmlstream.Token]},
			{"heading", htmlutil.IsHeading[*htmlstream.Token]},
			{"preformatted", htmlutil.IsPreformatted[*htmlstream.Token]},
			{"hidden", htmlutil.IsHidden[*htmlstream.Token]},
			{"head-content", htmlutil.IsHeadContent[*htmlstream.Token]},
			{"formatting", htmlutil.IsFormatting[*htmlstream.Token]},
			{"special", htmlutil.IsSpecial[*htmlstream.Token]},
		} {
			if c.fn(tok) {
				cats = append(cats, c.name)
			}
		}
		if len(cats) == 0 {
			cats = append(cats, "-")
		}
		fmt.Printf("%-8s %s\n", tok.Raw(), strings.Join(cats, " "))
	}
	if err := r.Err(); err != nil {
		panic(err)
	}
	// Output:
	// <p>      block text-break special
	// <b>      formatting
	// <br>     text-break void special
	// <td>     text-break special
	// <h2>     block text-break heading special
	// <pre>    block text-break preformatted special
	// <img>    void special
	// <svg>    -
	// <title>  -
	// <title>  hidden head-content special
	// <script> hidden head-content special
}

// The categories also classify open elements and element names.
func ExampleHTMLName() {
	fmt.Println(htmlutil.IsBlock(htmlutil.HTMLName("DIV")), htmlutil.IsBlock(htmlutil.HTMLName("span")))

	r := htmlstream.NewReader(strings.NewReader(`<pre><b>x`))
	for r.Next() {
		if r.Token().Kind() == htmlstream.Text {
			for e := range r.Stack() {
				fmt.Printf("%s preformatted: %v\n", e.Name(), htmlutil.IsPreformatted(e))
			}
		}
	}
	if err := r.Err(); err != nil {
		panic(err)
	}
	// Output:
	// true false
	// b preformatted: false
	// pre preformatted: true
}

// A Stack keeps a value for the open elements, here the language of the text
// from the lang attributes.
func ExampleStack() {
	const page = `<html lang=en><p>Hello, <span lang=fr>bonjour <b>le monde</b></span>!<p lang="">Unknown`

	r := htmlstream.NewReader(strings.NewReader(page))
	var lang htmlutil.Stack[string]
	for r.Next() {
		tok := r.Token()
		switch tok.Kind() {
		case htmlstream.StartTag:
			if a, ok := tok.Attr("lang"); ok {
				lang.Push(tok, a.Value())
			}
		case htmlstream.EndTag:
			lang.Pop(tok)
		case htmlstream.Text:
			l, ok := lang.Top()
			if !ok || *l == "" {
				fmt.Printf("%q (unknown)\n", tok.Raw())
			} else {
				fmt.Printf("%q (%s)\n", tok.Raw(), *l)
			}
		}
	}
	if err := r.Err(); err != nil {
		panic(err)
	}
	// Output:
	// "Hello, " (en)
	// "bonjour " (fr)
	// "le monde" (fr)
	// "!" (en)
	// "Unknown" (unknown)
}

// Document reports where the html, head and body elements start and end,
// including those HTML parsers imply, before each token.
func ExampleDocument() {
	for _, page := range []string{
		"<!DOCTYPE html>\n<title>Title</title><meta charset=utf-8>\n<p>Text",
		"<html><head><title>No doctype</title>",
	} {
		r := htmlstream.NewReader(strings.NewReader(page))
		var doc htmlutil.Document
		for r.Next() {
			tok := r.Token()
			if ev := doc.Update(tok); ev != 0 {
				raw := string(tok.Raw())
				if tok.Implied() {
					raw = "implied </" + string(tok.Name()) + ">"
				}
				fmt.Printf("%s: %v, now %v\n", raw, ev, doc.Part())
			}
		}
		if err := r.Err(); err != nil {
			panic(err)
		}
		if ev := doc.End(); ev != 0 {
			fmt.Printf("at the end: %v\n", ev)
		}
		fmt.Println("quirks:", doc.Quirks())
	}
	// Output:
	// <title>: ImpliedHTML|ImpliedHead, now InHead
	// <p>: HeadEnd|ImpliedBody, now InBody
	// quirks: false
	// implied </head>: HeadEnd, now AfterHead
	// implied </html>: ImpliedBody, now InBody
	// quirks: true
}

// InnerText approximates the text a browser renders. TextContent returns all
// the text in the element, like the DOM's textContent.
func ExampleInnerText() {
	const page = `<article>
  <h1>Fish   &amp; chips</h1>
  <p>Serves two.<br>Takes <b>20</b> minutes.</p>
  <table><tr><th>Item<th>Price<tr><td>Fish<td>4</table>
  <script>track()</script><p hidden>Hidden</p>
</article>`

	r := htmlstream.NewReader(strings.NewReader(page))
	r.Find(htmlstream.Tag("article"))
	text, _ := htmlutil.InnerText(r, htmlutil.TextOptions{})
	if err := r.Err(); err != nil {
		panic(err)
	}
	fmt.Printf("%s\n---\n", text)

	r = htmlstream.NewReader(strings.NewReader(page))
	r.Find(htmlstream.Tag("article"))
	text, _ = htmlutil.TextContent(r, 0)
	if err := r.Err(); err != nil {
		panic(err)
	}
	fmt.Printf("%q\n", htmlutil.CollapseSpace(text))
	// Output:
	// Fish & chips
	//
	// Serves two.
	// Takes 20 minutes.
	//
	// Item	Price
	// Fish	4
	// ---
	// "Fish & chips Serves two.Takes 20 minutes. ItemPriceFish4 track()Hidden"
}

// TextContent returns the text of an element as it is, except for template
// contents and the newline HTML parsers drop after the start tag of pre.
// Here it is truncated to 20 bytes.
func ExampleTextContent() {
	const page = `<pre>
for i := range 3 {
	print(i)
}</pre>
<p>A <b>long</b> <template>x</template>paragraph  with   spaces.</p>`

	r := htmlstream.NewReader(strings.NewReader(page))
	for r.Find(htmlstream.Or(htmlstream.Tag("pre"), htmlstream.Tag("p"))) {
		text, truncated := htmlutil.TextContent(r, 20)
		fmt.Printf("%q %t\n", text, truncated)
	}
	// Output:
	// "for i := range 3 {\n\t" true
	// "A long paragraph  wi" true
}

// TextBuilder collects text from tokens, collapsing whitespace.
func ExampleTextBuilder() {
	const page = `<ul><li>  One </li><li>Two<br>lines</li></ul>`

	r := htmlstream.NewReader(strings.NewReader(page))
	var b htmlutil.TextBuilder
	for r.Next() {
		tok := r.Token()
		switch {
		case tok.Kind() == htmlstream.Text:
			b.WriteText(tok)
		case htmlutil.IsTextBreak(tok):
			b.Break()
		}
	}
	if err := r.Err(); err != nil {
		panic(err)
	}
	fmt.Printf("%q\n", b.String())
	// Output:
	// "One Two lines"
}

// BaseURL tracks the base element, which applies to the URLs after it.
func ExampleBaseURL() {
	const page = `<head><base href="/docs/"><link rel=stylesheet href=style.css></head>
<body background=bg.png><a href="../about#team">About</a>
<svg><a href="b.svg"><image xlink:href=c.png /></a></svg>`

	doc, err := url.Parse("https://example.com/en/index.html")
	if err != nil {
		panic(err)
	}
	base := htmlutil.NewBaseURL(doc)
	r := htmlstream.NewReader(strings.NewReader(page))
	for r.Next() {
		tok := r.Token()
		base.Update(tok)
		for name, a := range htmlutil.URLAttrs(tok) {
			u, err := base.Resolve(a.Value())
			if err != nil {
				continue
			}
			fmt.Printf("%s %s: %s\n", tok.Name(), name, u)
		}
	}
	if err := r.Err(); err != nil {
		panic(err)
	}
	// Output:
	// base href: https://example.com/docs/
	// link href: https://example.com/docs/style.css
	// body background: https://example.com/docs/bg.png
	// a href: https://example.com/about#team
	// a href: https://example.com/docs/b.svg
	// image xlink:href: https://example.com/docs/c.png
}

// ParseSrcset splits a srcset attribute into image candidates, leaving out
// those with invalid descriptors. A URL ends at whitespace, so it can
// contain commas.
func ExampleParseSrcset() {
	for c := range htmlutil.ParseSrcset("small.jpg 480w, large.jpg  1200w 900h,huge.jpg 2X, data:image/png;base64,AA== 1.5x, a.jpg") {
		fmt.Printf("%q %q w=%d x=%g h=%d\n", c.URL, c.Descriptor, c.Width, c.Density, c.Height)
	}
	// Output:
	// "small.jpg" "480w" w=480 x=0 h=0
	// "large.jpg" "1200w 900h" w=1200 x=0 h=900
	// "data:image/png;base64,AA==" "1.5x" w=0 x=1.5 h=0
	// "a.jpg" "" w=0 x=1 h=0
}

// ResolveURL resolves a URL like browsers do for the cases net/url handles:
// tabs and newlines are removed, and the fragment of the base isn't kept.
func ExampleResolveURL() {
	base, _ := url.Parse("https://example.com/a/b.html#section")
	for _, ref := range []string{"c.html", " ../d\n.html ", "?q=1", "", "mailto:a@example.com"} {
		u, err := htmlutil.ResolveURL(base, ref)
		if err != nil {
			panic(err)
		}
		fmt.Printf("%q: %s\n", ref, u)
	}
	// Output:
	// "c.html": https://example.com/a/c.html
	// " ../d\n.html ": https://example.com/d.html
	// "?q=1": https://example.com/a/b.html?q=1
	// "": https://example.com/a/b.html
	// "mailto:a@example.com": mailto:a@example.com
}

// ScriptType determines the type of a script like browsers do, to tell
// classic scripts and modules apart from data blocks.
func ExampleScriptType() {
	const page = `<script>a()</script>
<script type=module>b()</script>
<script type="text/JavaScript ">c()</script>
<script language=VBScript>d</script>
<script type=application/ld+json>{}</script>
<script type=importmap>{}</script>
<script type="text/javascript; charset=utf-8">e()</script>
<script type=text/template><p>{{x}}</p></script>`

	r := htmlstream.NewReader(strings.NewReader(page))
	for r.Find(htmlstream.Tag("script")) {
		t := htmlutil.ScriptType(r.Token())
		fmt.Printf("%-33q js=%-5t json=%t\n", t, htmlutil.IsJavaScriptType(t), htmlutil.IsJSONType(t))
	}
	if err := r.Err(); err != nil {
		panic(err)
	}
	// Output:
	// "text/javascript"                 js=true  json=false
	// "module"                          js=true  json=false
	// "text/javascript"                 js=true  json=false
	// "text/vbscript"                   js=false json=false
	// "application/ld+json"             js=false json=true
	// "importmap"                       js=false json=true
	// "text/javascript; charset=utf-8"  js=false json=false
	// "text/template"                   js=false json=false
}

// MetaCharset returns the encoding a meta element declares, with the
// encoding names of the Encoding Standard.
func ExampleMetaCharset() {
	const page = `<meta charset=UTF-8>
<meta charset=latin1>
<meta http-equiv=Content-Type content="text/html; charset=utf-16">
<meta http-equiv=content-type content="text/html">
<meta charset=unknown>
<meta name=viewport content="width=device-width">`

	r := htmlstream.NewReader(strings.NewReader(page))
	for r.Find(htmlstream.Tag("meta")) {
		name, ok := htmlutil.MetaCharset(r.Token())
		fmt.Printf("%q %t\n", name, ok)
	}
	if err := r.Err(); err != nil {
		panic(err)
	}
	// Output:
	// "UTF-8" true
	// "windows-1252" true
	// "UTF-8" true
	// "" false
	// "" false
	// "" false
}

// PrescanCharset finds the encoding declared at the start of a document,
// before reading it. The prescan skips comments, but not the contents of
// script elements.
func ExamplePrescanCharset() {
	for _, head := range []string{
		`<!DOCTYPE html><html><head><meta charset="Shift_JIS"><title>x</title>`,
		`<!-- <meta charset=utf-8> --><meta http-equiv=content-type content="text/html; charset=iso-8859-2">`,
		`<meta content="text/html; charset=iso-8859-2">`,
		`<script>document.write("<meta charset=koi8-r>")</script>`,
		`<?xml version="1.0" encoding="windows-1251"?><html>`,
		`<p>No declaration`,
	} {
		name, ok := htmlutil.PrescanCharset([]byte(head))
		fmt.Printf("%q %t\n", name, ok)
	}
	// Output:
	// "Shift_JIS" true
	// "ISO-8859-2" true
	// "" false
	// "KOI8-R" true
	// "windows-1251" true
	// "" false
}
