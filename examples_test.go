package htmlstream_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/pgaskin/htmlstream"
)

// A Transformer copies the input to the output, applying the edits made to
// each token. Everything else, including malformed markup, is copied
// unchanged.
func Example() {
	const page = `<P>See <a href="https://example.com/">example.com</a>,
<A HREF=/about class=nav>about</A> and <a href="https://example.org/" rel=nofollow>example.org</a>.
<!-- unclosed <b>bold`

	x := htmlstream.NewTransformer(os.Stdout, strings.NewReader(page))
	x.SetMaxTokenSize(64 << 10) // a lower limit for untrusted input
	for x.Find(htmlstream.Tag("a", htmlstream.AttrFunc("href", func(v []byte) bool {
		return bytes.HasPrefix(v, []byte("https://"))
	}))) {
		rel := "noopener"
		if a, ok := x.Token().Attr("rel"); ok {
			rel = a.Value() + " " + rel
		}
		if err := x.SetAttr("rel", rel); err != nil {
			panic(err)
		}
	}
	if err := x.Close(); err != nil {
		panic(err)
	}
	// Output:
	// <P>See <a href="https://example.com/" rel="noopener">example.com</a>,
	// <A HREF=/about class=nav>about</A> and <a href="https://example.org/" rel="nofollow noopener">example.org</a>.
	// <!-- unclosed <b>bold
}

// Every element opened by a start tag is closed by exactly one end tag. End
// tags omitted in the input are implied, and end tags which don't close an
// element are stray.
func ExampleReader() {
	const page = `<ul><li>One<li>Two</ul></span><p>Three`

	r := htmlstream.NewReader(strings.NewReader(page))
	for tok := range r.All() {
		var flags string
		if tok.Implied() {
			flags += " (implied)"
		}
		if tok.Stray() {
			flags += " (stray)"
		}
		fmt.Printf("%d %-8s %q%s\n", r.Depth(), tok.Kind(), tok.Raw(), flags)
	}
	if err := r.Err(); err != nil {
		panic(err)
	}
	// Output:
	// 1 StartTag "<ul>"
	// 2 StartTag "<li>"
	// 2 Text     "One"
	// 2 EndTag   "" (implied)
	// 2 StartTag "<li>"
	// 2 Text     "Two"
	// 2 EndTag   "" (implied)
	// 1 EndTag   "</ul>"
	// 0 EndTag   "</span>" (stray)
	// 1 StartTag "<p>"
	// 1 Text     "Three"
	// 1 EndTag   "" (implied)
}

// Find advances to the next start tag matching a Matcher. Tag names and
// attribute names are compared ASCII case-insensitively in HTML mode.
func ExampleReader_Find() {
	const page = `<div class="card featured"><IMG SRC=a.png alt=A></div>
<div class=card><img src=b.png><img alt="no source"></div>
<img class=featured src=c.png>`

	r := htmlstream.NewReader(strings.NewReader(page))
	for r.Find(htmlstream.Or(
		htmlstream.HasClass("featured"),
		htmlstream.Tag("img", htmlstream.HasAttr("src"), htmlstream.Not(htmlstream.HasAttr("alt"))),
	)) {
		tok := r.Token()
		fmt.Printf("%s %s\n", tok.Name(), tok.Raw())
	}
	if err := r.Err(); err != nil {
		panic(err)
	}
	// Output:
	// div <div class="card featured">
	// img <img src=b.png>
	// img <img class=featured src=c.png>
}

// Skip advances to the end tag of the current element, here to leave out the
// contents of script and style elements while collecting the text.
func ExampleReader_Skip() {
	const page = `<p>Hello<script>document.write("<p>scripted</p>")</script>, <style>p { color: red }</style>world</p>`

	r := htmlstream.NewReader(strings.NewReader(page))
	var text []byte
	for r.Next() {
		tok := r.Token()
		switch tok.Kind() {
		case htmlstream.StartTag:
			if tok.Is("script") || tok.Is("style") {
				if err := r.Skip(); err != nil {
					panic(err)
				}
			}
		case htmlstream.Text:
			text = tok.AppendText(text)
		}
	}
	if err := r.Err(); err != nil {
		panic(err)
	}
	fmt.Println(string(text))
	// Output:
	// Hello, world
}

// Children iterates over the tokens inside an element, including those of
// nested elements. Depth gives the nesting level of the current token.
func ExampleReader_Children() {
	const page = `<nav><ul>
<li><a href=/>Home</a>
<li><a href=/docs>Docs</a><ul><li><a href=/docs/api>API</a></ul>
<li><a href=/blog>Blog</a>
</ul></nav>`

	r := htmlstream.NewReader(strings.NewReader(page))
	if !r.Find(htmlstream.Tag("ul")) {
		panic("no list")
	}
	depth := r.Depth()
	for tok := range r.Children() {
		if tok.Kind() == htmlstream.StartTag && tok.Is("a") {
			href, _ := tok.Attr("href")
			fmt.Printf("depth %d: %s\n", r.Depth()-depth, href.Value())
		}
	}
	if err := r.Err(); err != nil {
		panic(err)
	}
	fmt.Println(r.Token().Kind(), string(r.Token().Raw()))
	// Output:
	// depth 2: /
	// depth 2: /docs
	// depth 4: /docs/api
	// depth 2: /blog
	// EndTag </ul>
}

// Stack returns the open elements. With SetKeepAttrs, their attributes are
// kept too.
func ExampleReader_Stack() {
	const page = `<body><main id=content><section class=intro><p>Hello, <em>world</em>!</p></section></main></body>`

	r := htmlstream.NewReader(strings.NewReader(page))
	r.SetKeepAttrs(true)
	if !r.Find(htmlstream.Tag("em")) {
		panic("no em")
	}
	var path []string
	for e := range r.Stack() {
		s := string(e.Name())
		if id, ok := e.Attr("id"); ok {
			s += "#" + id.Value()
		}
		if class, ok := e.Attr("class"); ok {
			s += "." + class.Value()
		}
		path = append([]string{s}, path...)
	}
	fmt.Println(strings.Join(path, " > "))
	// Output:
	// body > main#content > section.intro > p > em
}

// Long text is split into several tokens at about the chunk size. ReadText
// reads the decoded text of all of them, up to a limit.
func ExampleReader_ReadText() {
	const page = `<p>Fish &amp; chips, fish &amp; chips, fish &amp; chips.</p><p>Next</p>`

	r := htmlstream.NewReader(strings.NewReader(page))
	r.SetChunkSize(16)
	for r.Next() {
		tok := r.Token()
		if tok.Kind() == htmlstream.Text {
			partial := tok.Partial()
			text, truncated := r.ReadText(nil, 24)
			fmt.Printf("%q partial=%t truncated=%t\n", text, partial, truncated)
		}
	}
	if err := r.Err(); err != nil {
		panic(err)
	}
	// Output:
	// "Fish & chips, fish & chi" partial=true truncated=true
	// "Next" partial=false truncated=false
}

// HTML mode reads XHTML like browsers read text/html: the self-closed script
// element here contains the rest of the document. With SetXMLSyntax,
// self-closing tags are read as XML parsers read them, as needed for EPUB
// content documents.
func ExampleReader_SetXMLSyntax() {
	const page = `<?xml version="1.0"?>
<html xmlns="http://www.w3.org/1999/xhtml"><head><script src="a.js"/></head>
<body><p>Hello<br/>world</p><![CDATA[x < y]]></body></html>`

	for _, xmlSyntax := range []bool{false, true} {
		r := htmlstream.NewReader(strings.NewReader(page))
		r.SetXMLSyntax(xmlSyntax)
		var names []string
		for r.Next() {
			if tok := r.Token(); tok.Kind() == htmlstream.StartTag {
				names = append(names, string(tok.Name()))
			}
		}
		if err := r.Err(); err != nil {
			panic(err)
		}
		fmt.Printf("xml syntax=%t: %s\n", xmlSyntax, strings.Join(names, " "))
	}
	// Output:
	// xml syntax=false: html head script
	// xml syntax=true: html head script body p br
}

// The modes read the same markup differently. In HTML mode, title contents
// are text and br is a void element. In XML mode, names are case-sensitive
// and every start tag opens an element unless self-closing. Universal mode
// reads a document without an XML declaration like HTML mode with XML
// syntax, so the br element is closed by an implied end tag before the text
// after it.
func ExampleReader_SetMode() {
	const page = `<title>A <b>bold</b> title</title><P>a<br>b</p>`

	for _, mode := range []htmlstream.Mode{htmlstream.HTML, htmlstream.XML, htmlstream.Universal} {
		r := htmlstream.NewReader(strings.NewReader(page))
		r.SetMode(mode)
		var toks []string
		for tok := range r.All() {
			s := string(tok.Raw())
			switch {
			case tok.Implied():
				s = "(implied </" + string(tok.Name()) + ">)"
			case tok.Stray():
				s += "(stray)"
			}
			toks = append(toks, s)
		}
		if err := r.Err(); err != nil {
			panic(err)
		}
		fmt.Printf("%s: %s\n", mode, strings.Join(toks, "|"))
	}
	// Output:
	// HTML: <title>|A <b>bold</b> title|</title>|<P>|a|<br>|b|</p>
	// XML: <title>|A |<b>|bold|</b>| title|</title>|<P>|a|<br>|b|</p>(stray)|(implied </br>)|(implied </P>)
	// Universal: <title>|A <b>bold</b> title|</title>|<P>|a|<br>|(implied </br>)|b|</p>
}

// In strict mode, operations which depend on structurally unsound markup,
// like skipping an element closed without its required end tag, return a
// SyntaxError.
func ExampleReader_SetStrict() {
	const page = `<div><span>unclosed</div><p>after</p>`

	for _, strict := range []bool{false, true} {
		r := htmlstream.NewReader(strings.NewReader(page))
		r.SetStrict(strict)
		if !r.Find(htmlstream.Tag("span")) {
			panic("no span")
		}
		if err := r.Skip(); err != nil {
			var serr *htmlstream.SyntaxError
			if errors.As(err, &serr) {
				fmt.Printf("strict=%t: %s at offset %d\n", strict, serr.Msg, serr.Pos)
				continue
			}
			panic(err)
		}
		fmt.Printf("strict=%t: skipped to %q, implied=%t unsound=%t\n", strict, r.Token().Raw(), r.Token().Implied(), r.Token().Unsound())
	}
	// Output:
	// strict=false: skipped to "", implied=true unsound=true
	// strict=true: element "span" at offset 5 closed without its end tag at offset 19
}

// Attributes are decoded like HTML parsers decode them. Raw gives the exact
// bytes.
func ExampleToken_Attrs() {
	const page = `<input type=checkbox Checked value="fish &amp; chips" title='it&#39;s' type=radio>`

	r := htmlstream.NewReader(strings.NewReader(page))
	r.Next()
	tok := r.Token()
	for a := range tok.Attrs() {
		fmt.Printf("%-8s %-24s value=%q has=%t quote=%q\n", a.Name(), a.Raw(), a.Value(), a.HasValue(), a.Quote())
	}
	typ, _ := tok.Attr("TYPE")
	fmt.Println("type:", typ.Value())
	// Output:
	// type     type=checkbox            value="checkbox" has=true quote='\x00'
	// Checked  Checked                  value="" has=false quote='\x00'
	// value    value="fish &amp; chips" value="fish & chips" has=true quote='"'
	// title    title='it&#39;s'         value="it's" has=true quote='\''
	// type     type=radio               value="radio" has=true quote='\x00'
	// type: checkbox
}

// In HTML mode, NS gives the namespace HTML parsers put an element in.
// AppendLocalName gives a name to switch on without allocating, lowercased in
// HTML mode, where names are case-insensitive, and AdjustSVGTagName the case
// HTML parsers give SVG names.
func ExampleToken_NS() {
	const page = `<DIV><svg viewBox="0 0 10 10"><FOREIGNOBJECT><p>text</p></FOREIGNOBJECT></svg><math><mi>x</mi></math></DIV>`

	r := htmlstream.NewReader(strings.NewReader(page))
	var buf [16]byte
	for r.Next() {
		tok := r.Token()
		if tok.Kind() != htmlstream.StartTag {
			continue
		}
		local := tok.AppendLocalName(buf[:0])
		if tok.NS() == htmlstream.NamespaceSVG {
			local = []byte(htmlstream.AdjustSVGTagName(string(local)))
		}
		fmt.Printf("%-13s %-13s %s\n", tok.Name(), local, tok.NS())
	}
	// Output:
	// DIV           div           http://www.w3.org/1999/xhtml
	// svg           svg           http://www.w3.org/2000/svg
	// FOREIGNOBJECT foreignObject http://www.w3.org/2000/svg
	// p             p             http://www.w3.org/1999/xhtml
	// math          math          http://www.w3.org/1998/Math/MathML
	// mi            mi            http://www.w3.org/1998/Math/MathML
}

// A TokenBuffer keeps copies of tokens after the Reader advances, here to
// list the links after reading the whole document.
func ExampleTokenBuffer() {
	const page = `<p><a href=/a>A</a>, <a href=/b>B</a> and <a href=/c>C</a>.</p>`

	r := htmlstream.NewReader(strings.NewReader(page))
	var links htmlstream.TokenBuffer
	for r.Find(htmlstream.Tag("a", htmlstream.HasAttr("href"))) {
		links.Append(r.Token())
	}
	if err := r.Err(); err != nil {
		panic(err)
	}
	fmt.Println(links.Len(), "links,", links.Size(), "bytes")
	for tok := range links.All() {
		href, _ := tok.Attr("href")
		fmt.Printf("%d: %s\n", tok.Pos(), href.Value())
	}
	// Output:
	// 3 links, 33 bytes
	// 3: /a
	// 21: /b
	// 42: /c
}

// Attribute edits keep the other attributes as they are written. Positions
// passed to SetAttrAt count the attributes of the input.
func ExampleTransformer_SetAttr() {
	const page = `<img src=cat.jpg WIDTH=100 data-x=1 data-y=2 alt='A cat'>`

	x := htmlstream.NewTransformer(os.Stdout, strings.NewReader(page))
	for x.Find(htmlstream.Tag("img")) {
		edits := []error{
			x.SetAttr("alt", `A "cat"`),     // replaces the value, keeping the position
			x.SetAttr("loading", "lazy"),    // adds an attribute at the end
			x.SetAttrAt("id", "photo", 0),   // adds an attribute before the first one
			x.RenameAttr("width", "height"), // keeps the value
			x.RemoveAttrs(func(a htmlstream.Attr) bool {
				return bytes.HasPrefix(a.Name(), []byte("data-"))
			}),
		}
		if err := errors.Join(edits...); err != nil {
			panic(err)
		}
	}
	if err := x.Close(); err != nil {
		panic(err)
	}
	// Output:
	// <img id="photo" src=cat.jpg height=100 alt="A &quot;cat&quot;" loading="lazy">
}

// Element operations on a start tag also apply to the element's end tag and
// contents.
func ExampleTransformer_Remove() {
	const page = `<div class=ad>Buy <b>now</b>!</div><p>Text<span class=ad>sponsored</span>.</p><!-- comment -->`

	x := htmlstream.NewTransformer(os.Stdout, strings.NewReader(page))
	for x.Next() {
		tok := x.Token()
		var err error
		switch {
		case tok.Kind() == htmlstream.Comment:
			err = x.Remove()
		case htmlstream.HasClass("ad")(tok):
			err = x.Remove() // removes the contents and the end tag too
		}
		if err != nil {
			panic(err)
		}
	}
	if err := x.Close(); err != nil {
		panic(err)
	}
	// Output:
	// <p>Text.</p>
}

// Unwrap removes the tags of an element, keeping its contents.
func ExampleTransformer_Unwrap() {
	const page = `<p><font color=red>Warning:</font> do not <span>touch</span>.`

	x := htmlstream.NewTransformer(os.Stdout, strings.NewReader(page))
	for x.Next() {
		tok := x.Token()
		if tok.Kind() == htmlstream.StartTag && (tok.Is("font") || tok.Is("span")) {
			if err := x.Unwrap(); err != nil {
				panic(err)
			}
		}
	}
	if err := x.Close(); err != nil {
		panic(err)
	}
	// Output:
	// <p>Warning: do not touch.
}

// Rename renames the start tag of an element and its end tag. Implied end
// tags aren't written.
func ExampleTransformer_Rename() {
	const page = `<p><b>Bold</b> and <i>italic</I>.<p><b>Second`

	x := htmlstream.NewTransformer(os.Stdout, strings.NewReader(page))
	for x.Next() {
		tok := x.Token()
		if tok.Kind() != htmlstream.StartTag {
			continue
		}
		var err error
		switch {
		case tok.Is("b"):
			err = x.Rename("strong")
		case tok.Is("i"):
			err = x.Rename("em")
		}
		if err != nil {
			panic(err)
		}
	}
	if err := x.Close(); err != nil {
		panic(err)
	}
	// Output:
	// <p><strong>Bold</strong> and <em>italic</em>.<p><strong>Second
}

// Wrap writes markup around an element, and Replace writes markup instead of
// a token.
func ExampleTransformer_Wrap() {
	const page = `<table><tr><td>1</td></tr></table><p>An <img src=x.png alt="image"> here.</p>`

	x := htmlstream.NewTransformer(os.Stdout, strings.NewReader(page))
	for x.Next() {
		tok := x.Token()
		if tok.Kind() != htmlstream.StartTag {
			continue
		}
		var err error
		switch {
		case tok.Is("table"):
			err = x.Wrap([]byte(`<div class="scroll">`), []byte(`</div>`))
		case tok.Is("img"):
			alt, _ := tok.Attr("alt")
			err = x.Replace(htmlstream.AppendElement(nil, "em", alt.Value()))
		}
		if err != nil {
			panic(err)
		}
	}
	if err := x.Close(); err != nil {
		panic(err)
	}
	// Output:
	// <div class="scroll"><table><tr><td>1</td></tr></table></div><p>An <em>image</em> here.</p>
}

// InsertBefore and InsertAfter write markup around a token. Markup written
// after a start tag is inside the element. InsertAtEnd writes markup at the
// end of the output.
func ExampleTransformer_InsertBefore() {
	const page = `<h1>Title</h1><p>Text<br>more text.</p>`

	x := htmlstream.NewTransformer(os.Stdout, strings.NewReader(page))
	for x.Next() {
		tok := x.Token()
		if tok.Kind() != htmlstream.StartTag {
			continue
		}
		var err error
		switch {
		case tok.Is("h1"):
			err = errors.Join(
				x.InsertBefore([]byte(`<a id="top"></a>`)),
				x.InsertAfter([]byte("&sect; ")), // inside the element
			)
		case tok.Is("br"):
			err = x.InsertAfter([]byte("\n")) // a void element has no contents
		}
		if err != nil {
			panic(err)
		}
	}
	x.InsertAtEnd([]byte(`<footer><a href="#top">Top</a></footer>`))
	if err := x.Close(); err != nil {
		panic(err)
	}
	// Output:
	// <a id="top"></a><h1>&sect; Title</h1><p>Text<br>
	// more text.</p><footer><a href="#top">Top</a></footer>
}

// Empty removes the contents of an element. HTML elements keep their end tag,
// and SVG elements are written with self-closing syntax.
func ExampleTransformer_Empty() {
	const page = `<p>Text</p><script>tracker()</script><svg><circle r=1><title>x</title></circle></svg>`

	x := htmlstream.NewTransformer(os.Stdout, strings.NewReader(page))
	for x.Next() {
		tok := x.Token()
		if tok.Kind() == htmlstream.StartTag && (tok.Is("script") || tok.Is("circle")) {
			if err := x.Empty(); err != nil {
				panic(err)
			}
		}
	}
	if err := x.Close(); err != nil {
		panic(err)
	}
	// Output:
	// <p>Text</p><script></script><svg><circle r=1 /></svg>
}

// Hold holds back the output until the tokens which follow decide what to do
// with it, here to remove the paragraphs without text.
func ExampleTransformer_Hold() {
	const page = `<p>One</p><p> <!-- empty --> </p><p><br></p><p>Two <b>three</b></p>`

	x := htmlstream.NewTransformer(os.Stdout, strings.NewReader(page))
	var h htmlstream.Hold // the hold of the current paragraph, if not zero
	for x.Next() {
		tok := x.Token()
		var err error
		switch {
		case tok.Kind() == htmlstream.StartTag && tok.Is("p"):
			h, err = x.Hold()
		case h == htmlstream.Hold{}:
		case tok.Kind() == htmlstream.Text && len(bytes.TrimSpace(tok.Raw())) != 0:
			err = x.HoldRelease(h)
			h = htmlstream.Hold{}
		case tok.Kind() == htmlstream.EndTag && tok.Is("p"):
			// The end tag isn't held back: remove it, then the held output.
			if err = x.Remove(); err == nil {
				err = x.HoldDiscard(h, nil)
			}
			h = htmlstream.Hold{}
		}
		if err != nil {
			panic(err)
		}
	}
	if err := x.Close(); err != nil {
		panic(err)
	}
	// Output:
	// <p>One</p><p>Two <b>three</b></p>
}

// HoldDiscard ends a hold, replacing the output held back, here to put runs
// of images in a gallery element once the token after them is known, or
// after the end of the input.
func ExampleTransformer_HoldDiscard() {
	const page = `<p>One</p><img src=a.png><img src=b.png>
<img src=c.png><p>Two</p><img src=d.png><p>Three</p><img src=e.png><img src=f.png>`

	x := htmlstream.NewTransformer(os.Stdout, strings.NewReader(page))
	var h htmlstream.Hold
	images := 0
	// endRun ends the hold of a run of images, putting them in a gallery if
	// there are several.
	endRun := func() error {
		n := images
		images = 0
		if n == 1 {
			return x.HoldRelease(h)
		}
		gallery := append([]byte(`<div class="gallery">`), x.Held(h)...)
		return x.HoldDiscard(h, append(gallery, "</div>"...))
	}
	for x.Next() {
		tok := x.Token()
		var err error
		switch {
		case tok.Kind() == htmlstream.StartTag && tok.Is("img"):
			if images == 0 {
				h, err = x.Hold()
			}
			images++
		case images == 0:
		case tok.Kind() == htmlstream.Text && len(bytes.TrimSpace(tok.Raw())) == 0:
		default:
			// The current token isn't held back: it is written after.
			err = endRun()
		}
		if err != nil {
			panic(err)
		}
	}
	if images != 0 {
		if err := endRun(); err != nil {
			panic(err)
		}
	}
	if err := x.Close(); err != nil {
		panic(err)
	}
	// Output:
	// <p>One</p><div class="gallery"><img src=a.png><img src=b.png>
	// <img src=c.png></div><p>Two</p><img src=d.png><p>Three</p><div class="gallery"><img src=e.png><img src=f.png></div>
}

// Text longer than the chunk size is split into several tokens (see
// Token.Partial), here with a small chunk size. To replace all of it, hold
// back the output from the first token, read the text, replace the last
// token, and discard the held output.
func ExampleTransformer_ReadText() {
	const page = `<p>This text &amp; its character reference are longer than the chunk size.</p><p>Short.</p>`

	x := htmlstream.NewTransformer(os.Stdout, strings.NewReader(page))
	x.SetChunkSize(8)
	for x.Next() {
		if x.Token().Kind() != htmlstream.Text {
			continue
		}
		h, err := x.Hold()
		if err != nil {
			panic(err)
		}
		text, _ := x.ReadText(nil, 0) // the current token is now the last one
		if err := x.Replace(htmlstream.AppendEscapeText(nil, bytes.ToUpper(text))); err != nil {
			panic(err)
		}
		if err := x.HoldDiscard(h, nil); err != nil {
			panic(err)
		}
	}
	if err := x.Close(); err != nil {
		panic(err)
	}
	// Output:
	// <p>THIS TEXT &amp; ITS CHARACTER REFERENCE ARE LONGER THAN THE CHUNK SIZE.</p><p>SHORT.</p>
}

// The Append functions write markup which HTML and XML parsers read the same
// way, for inserting with a Transformer.
func ExampleAppendElement() {
	var b []byte
	b = htmlstream.AppendElement(b, "a", "Fish & chips <3", "href", "/search?q=fish&chips", "title", `"fish"`)
	b = append(b, '\n')
	b = htmlstream.AppendStartTag(b, "img", true, "src", "a.png", "alt", "")
	b = append(b, '\n')
	b = htmlstream.AppendStartTag(b, "p", false)
	b = htmlstream.AppendEscapeText(b, "1 < 2\r\n")
	b = htmlstream.AppendEndTag(b, "p")
	b = append(b, '\n')
	b = htmlstream.AppendComment(b, "built with --release")
	fmt.Println(string(b))
	// Output:
	// <a href="/search?q=fish&amp;chips" title="&quot;fish&quot;">Fish &amp; chips &lt;3</a>
	// <img src="a.png" alt="" />
	// <p>1 &lt; 2&#13;
	// </p>
	// <!--built with - -release-->
}

// AppendComment changes the text where it would end the comment early or
// isn't allowed in XML comments.
func ExampleAppendComment() {
	for _, text := range []string{"note", "a -- b", "->x", "x-", "<!-- nested -->"} {
		fmt.Println(string(htmlstream.AppendComment(nil, text)))
	}
	// Output:
	// <!--note-->
	// <!--a - - b-->
	// <!-- ->x-->
	// <!--x- -->
	// <!--<!- - nested - ->-->
}

// The XML helpers check and fix names and text for XML output.
func ExampleValidXMLName() {
	for _, name := range []string{"p", "xlink:href", "data-1", "1st", "a:b:c", "@click"} {
		fmt.Println(name, htmlstream.ValidXMLName(name))
	}
	text, replaced := htmlstream.AppendXMLChars(nil, "form\ffeed and \x01 control")
	fmt.Printf("%q %t\n", text, replaced)
	fmt.Println(htmlstream.AdjustSVGTagName("foreignobject"), htmlstream.AdjustSVGAttrName("viewbox"))
	// Output:
	// p true
	// xlink:href true
	// data-1 true
	// 1st false
	// a:b:c false
	// @click false
	// "form feed and � control" true
	// foreignObject viewBox
}

// In XML mode, a Transformer edits XML documents, keeping everything it
// doesn't edit as written: here, the XML declaration, a doctype with an
// internal subset, processing instructions, CDATA sections, entity
// references and namespace prefixes. Elements named like HTML elements, like
// script and br, are ordinary elements.
func ExampleTransformer_xml() {
	const doc = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE feed [<!ENTITY ver "2.1">]>
<?xml-stylesheet href="feed.xsl" type="text/xsl"?>
<f:feed xmlns:f="urn:feed" f:version='&ver;'>
  <f:entry id="1"><script><![CDATA[a < b]]></script></f:entry><f:entry id="2" draft="yes"><br>x</br></f:entry>
  <f:entry id="3"><br/></f:entry>
</f:feed>`

	x := htmlstream.NewTransformer(os.Stdout, strings.NewReader(doc))
	x.SetMode(htmlstream.XML)
	for x.Next() {
		tok := x.Token()
		var err error
		switch {
		case tok.Kind() == htmlstream.ProcInst && string(tok.Name()) == "xml-stylesheet":
			err = x.Replace(htmlstream.AppendProcInst(nil, "xml-stylesheet", `href="new.xsl" type="text/xsl"`))
		case tok.Kind() == htmlstream.StartTag && tok.Is("f:entry") && tok.HasAttr("draft"):
			err = x.Remove()
		case tok.Kind() == htmlstream.CDATA:
			err = x.Replace(htmlstream.AppendCDATA(nil, "if (a[b[0]]>c) {}"))
		}
		if err != nil {
			panic(err)
		}
	}
	if err := x.Close(); err != nil {
		panic(err)
	}
	// Output:
	// <?xml version="1.0" encoding="UTF-8"?>
	// <!DOCTYPE feed [<!ENTITY ver "2.1">]>
	// <?xml-stylesheet href="new.xsl" type="text/xsl"?>
	// <f:feed xmlns:f="urn:feed" f:version='&ver;'>
	//   <f:entry id="1"><script><![CDATA[if (a[b[0]]]]><![CDATA[>c) {}]]></script></f:entry>
	//   <f:entry id="3"><br/></f:entry>
	// </f:feed>
}
