package htmlstream

import (
	"bytes"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"testing/iotest"
)

func TestTransformPassthrough(t *testing.T) {
	for _, in := range allVectors() {
		for _, mode := range modes {
			var b bytes.Buffer
			x := NewTransformer(&b, iotest.OneByteReader(strings.NewReader(in)))
			x.SetMode(mode)
			x.SetChunkSize(3)
			if err := x.Close(); err != nil {
				t.Errorf("%s %q: %v", mode, in, err)
			}
			if b.String() != in {
				t.Errorf("%s %q: got %q", mode, in, b.String())
			}
		}
	}
}

// each calls fn for each token.
func each(fn func(x *Transformer, tok *Token) error) func(x *Transformer) error {
	return func(x *Transformer) error {
		for x.Next() {
			if err := fn(x, x.Token()); err != nil {
				return err
			}
		}
		return nil
	}
}

func TestTransform(t *testing.T) {
	for _, tc := range []struct {
		Name   string
		Mode   Mode
		Strict bool
		Depth  int // SetMaxDepth, if not zero
		In     string
		Do     func(x *Transformer) error
		Out    string // empty if an error is expected
	}{
		{
			Name: "RemoveDataAttrs",
			In:   `<div data-a="1" id=x data-b DATA-C='z'>t</div>`,
			Do: each(func(x *Transformer, tok *Token) error {
				if tok.Kind() == StartTag {
					return x.RemoveAttrs(func(a Attr) bool {
						return bytes.HasPrefix(bytes.ToLower(a.Name()), []byte("data-"))
					})
				}
				return nil
			}),
			Out: `<div id=x>t</div>`,
		},
		{
			Name: "RemoveElement",
			In:   `<p>a<script>x</script>b</p>`,
			Do: each(func(x *Transformer, tok *Token) error {
				if tok.Kind() == StartTag && tok.Is("script") {
					return x.Remove()
				}
				return nil
			}),
			Out: `<p>ab</p>`,
		},
		{
			Name:   "RemoveElementMisnestedInside",
			Strict: true,
			In:     `<div><b><i>x</b></i></div>y`,
			Do: each(func(x *Transformer, tok *Token) error {
				if tok.Kind() == StartTag && tok.Is("div") {
					return x.Remove()
				}
				return nil
			}),
			Out: `y`,
		},
		{
			Name:   "RemoveElementUnsoundStrict",
			Strict: true,
			In:     `<div><span>x</div>`,
			Do: each(func(x *Transformer, tok *Token) error {
				if tok.Kind() == StartTag && tok.Is("span") {
					return x.Remove()
				}
				return nil
			}),
		},
		{
			Name: "RemoveElementUnsound",
			In:   `<div><span>x</div>y`,
			Do: each(func(x *Transformer, tok *Token) error {
				if tok.Kind() == StartTag && tok.Is("span") {
					return x.Remove()
				}
				return nil
			}),
			Out: `<div></div>y`,
		},
		{
			Name:   "RemoveImpliedEndStrict",
			Strict: true,
			In:     `<ul><li>a<li>b</ul>`,
			Do: each(func(x *Transformer, tok *Token) error {
				if tok.Kind() == StartTag && tok.Is("li") {
					return x.Remove()
				}
				return nil
			}),
			Out: `<ul></ul>`,
		},
		{
			Name: "SetAttr",
			In:   `<a href='x' title=t>`,
			Do: each(func(x *Transformer, tok *Token) error {
				if tok.Kind() == StartTag {
					x.SetAttr("href", `y"&`)
					x.SetAttr("rel", "noopener")
				}
				return nil
			}),
			Out: `<a href="y&quot;&amp;" title=t rel="noopener">`,
		},
		{
			// Tabs, newlines and carriage returns are escaped under HTML
			// rules too.
			Name: "SetAttrWhitespace",
			In:   `<a title=t><svg x=1></svg>`,
			Do: each(func(x *Transformer, tok *Token) error {
				if tok.Kind() == StartTag {
					x.SetAttr("title", "a\tb\r\nc")
					x.SetAttrAt("x", "\n", 0)
				}
				return nil
			}),
			Out: `<a x="&#10;" title="a&#9;b&#13;&#10;c"><svg x="&#10;" title="a&#9;b&#13;&#10;c"></svg>`,
		},
		{
			Name: "SetAttrNoSpace",
			In:   `<a b="1"c="2"/>`,
			Mode: XML,
			Do: each(func(x *Transformer, tok *Token) error {
				if tok.Kind() == StartTag {
					x.SetAttr("c", "3")
				}
				return nil
			}),
			Out: `<a b="1" c="3"/>`,
		},
		{
			Name: "Unwrap",
			In:   `<p><b>x</b>y</p>`,
			Do: each(func(x *Transformer, tok *Token) error {
				if tok.Kind() == StartTag && tok.Is("b") {
					return x.Unwrap()
				}
				return nil
			}),
			Out: `<p>xy</p>`,
		},
		{
			Name: "UnwrapImplied",
			In:   `<ul><li>a<li>b</ul>`,
			Do: each(func(x *Transformer, tok *Token) error {
				if tok.Kind() == StartTag && tok.Is("li") {
					return x.Unwrap()
				}
				return nil
			}),
			Out: `<ul>ab</ul>`,
		},
		{
			Name: "WrapVoid",
			In:   `<img src=a>`,
			Do: each(func(x *Transformer, tok *Token) error {
				if tok.Kind() == StartTag && tok.Is("img") {
					return x.Wrap([]byte(`<a href=b>`), []byte(`</a>`))
				}
				return nil
			}),
			Out: `<a href=b><img src=a></a>`,
		},
		{
			Name: "WrapNested",
			In:   `<p>x</p>`,
			Do: each(func(x *Transformer, tok *Token) error {
				if tok.Kind() == StartTag && tok.Is("p") {
					x.Wrap([]byte(`<div>`), []byte(`</div>`))
					x.Wrap([]byte(`<span>`), []byte(`</span>`))
				}
				return nil
			}),
			Out: `<div><span><p>x</p></span></div>`,
		},
		{
			// Markup inserted before the start tag and after the element is
			// outside the wrap, whatever the order of the calls.
			Name: "WrapInsert",
			In:   `<br><br><p>x</p><p>y</p>`,
			Do: func(x *Transformer) error {
				first := true
				for x.Next() {
					tok := x.Token()
					switch {
					case tok.Kind() == StartTag:
						if first {
							x.Wrap([]byte(`<s>`), []byte(`</s>`))
						}
						x.InsertBefore([]byte(`B`))
						if !tok.Is("p") {
							x.InsertAfter([]byte(`A`))
						}
						if !first {
							x.Wrap([]byte(`<s>`), []byte(`</s>`))
						}
						first = !first
					case tok.Kind() == EndTag:
						x.InsertAfter([]byte(`A`))
					}
				}
				return nil
			},
			Out: `B<s><br></s>AB<s><br></s>AB<s><p>x</p></s>AB<s><p>y</p></s>A`,
		},
		{
			Name: "WrapImplied",
			In:   `<li>a<li>b`,
			Do: each(func(x *Transformer, tok *Token) error {
				if tok.Kind() == StartTag && tok.Is("li") && tok.Pos() == 0 {
					return x.Wrap([]byte(`<x>`), []byte(`</x>`))
				}
				return nil
			}),
			Out: `<x><li>a</x><li>b`,
		},
		{
			Name: "ExpandSelfClosingXML",
			Mode: XML,
			In:   `<a><b x="1"/><c /></a>`,
			Do: each(func(x *Transformer, tok *Token) error {
				if tok.Kind() == StartTag {
					return x.ExpandSelfClosing()
				}
				return nil
			}),
			Out: `<a><b x="1"></b><c ></c></a>`,
		},
		{
			Name: "ExpandSelfClosingHTML",
			In:   `<div/>x</div><br/><svg><path/></svg>`,
			Do: each(func(x *Transformer, tok *Token) error {
				if tok.Kind() == StartTag {
					return x.ExpandSelfClosing()
				}
				return nil
			}),
			Out: `<div>x</div><br><svg><path></path></svg>`,
		},
		{
			Name: "Empty",
			In:   `<div class=a><p>x</p>y</div>z`,
			Do: each(func(x *Transformer, tok *Token) error {
				if tok.Kind() == StartTag && tok.Is("div") {
					return x.Empty()
				}
				return nil
			}),
			Out: `<div class=a></div>z`,
		},
		{
			Name: "EmptyXML",
			Mode: XML,
			In:   `<a x="1"><b/>t</a><c/>`,
			Do: each(func(x *Transformer, tok *Token) error {
				if tok.Kind() == StartTag && tok.Is("a") {
					return x.Empty()
				}
				return nil
			}),
			Out: `<a x="1"/><c/>`,
		},
		{
			Name: "RenamePrefix",
			Mode: XML,
			In:   `<svg:svg xmlns:svg="http://www.w3.org/2000/svg"><svg:g><svg:path/></svg:g></svg:svg>`,
			Do: each(func(x *Transformer, tok *Token) error {
				if tok.Kind() == StartTag && tok.NS() == NamespaceSVG {
					if err := x.Rename(string(tok.Local())); err != nil {
						return err
					}
					if tok.Is("svg:svg") {
						x.RemoveAttr("xmlns:svg")
						x.SetAttr("xmlns", NamespaceSVG)
					}
				}
				return nil
			}),
			Out: `<svg xmlns="http://www.w3.org/2000/svg"><g><path/></g></svg>`,
		},
		{
			Name: "InsertBeforeImpliedEnd",
			In:   `<html><head><title>t</title><body>x`,
			Do: each(func(x *Transformer, tok *Token) error {
				if tok.Kind() == EndTag && tok.Is("head") {
					return x.InsertBefore([]byte(`<meta charset=utf-8>`))
				}
				return nil
			}),
			Out: `<html><head><title>t</title><meta charset=utf-8><body>x`,
		},
		{
			Name: "ReplaceImpliedEnd",
			In:   `<p>a<p>b`,
			Do: each(func(x *Transformer, tok *Token) error {
				if tok.Kind() == EndTag && tok.Implied() {
					return x.Replace([]byte(`</p>`))
				}
				return nil
			}),
			Out: `<p>a</p><p>b</p>`,
		},
		{
			Name: "FindRemove",
			In:   `<script id=a>1</script><script id=b>2</script><script id=c>3</script>`,
			Do: func(x *Transformer) error {
				for x.Find(Tag("script", AttrEquals("id", "b"))) {
					if err := x.Remove(); err != nil {
						return err
					}
				}
				return nil
			},
			Out: `<script id=a>1</script><script id=c>3</script>`,
		},
		{
			Name: "OperationOnText",
			In:   `x`,
			Do: each(func(x *Transformer, tok *Token) error {
				return x.Unwrap()
			}),
		},
		{
			// The end tag of an untracked element doesn't end the removal.
			Name:  "RemoveUntracked",
			Depth: 1,
			In:    `<a><b></b>x</a>y`,
			Do: each(func(x *Transformer, tok *Token) error {
				if tok.Kind() == StartTag && tok.Is("a") {
					return x.Remove()
				}
				return nil
			}),
			Out: `y`,
		},
		{
			// The actions of the outer element apply to its own end tag, not
			// to the end tag of an untracked element inside it.
			Name:  "UnwrapUntracked",
			Depth: 1,
			In:    `<a><b></b>x</a>`,
			Do: each(func(x *Transformer, tok *Token) error {
				if tok.Kind() == StartTag && tok.Is("a") {
					return x.Unwrap()
				}
				return nil
			}),
			Out: `<b></b>x`,
		},
		{
			Name:  "RenameUntracked",
			Depth: 1,
			In:    `<a><b></b>x</a>`,
			Do: each(func(x *Transformer, tok *Token) error {
				if tok.Kind() == StartTag && tok.Is("a") {
					return x.Rename("c")
				}
				return nil
			}),
			Out: `<c><b></b>x</c>`,
		},
		{
			// Operations on untracked elements end at the end tag at the
			// same depth.
			Name:  "RemoveUntrackedElement",
			Depth: 1,
			In:    `<a><b><c>x</c>y</b>z</a>`,
			Do: each(func(x *Transformer, tok *Token) error {
				if tok.Kind() == StartTag && tok.Is("b") {
					x.InsertBefore([]byte(`[`))
					return x.Remove()
				}
				return nil
			}),
			Out: `<a>[z</a>`,
		},
		{
			Name:  "EditUntrackedElements",
			Depth: 2, // at most 2 untracked elements with edits at a time
			In:    `<r><a><b><c>x</c>y</b><d>z</d><e>w</a></r>`,
			Do: each(func(x *Transformer, tok *Token) error {
				if tok.Kind() != StartTag {
					return nil
				}
				switch {
				case tok.Is("b"):
					if err := x.Wrap([]byte(`[`), []byte(`]`)); err != nil {
						return err
					}
					return x.Rename("B")
				case tok.Is("c"):
					return x.Unwrap()
				case tok.Is("d"):
					return x.Empty()
				case tok.Is("e"):
					return x.Wrap([]byte(`(`), []byte(`)`))
				}
				return nil
			}),
			Out: `<r><a>[<B>xy</B>]<d></d>(<e>w</a>)</r>`, // </a> closes the untracked e
		},
		{
			Name: "InsertBeforeRemove",
			In:   `<div>x</div>y`,
			Do: each(func(x *Transformer, tok *Token) error {
				if tok.Kind() == StartTag {
					x.InsertBefore([]byte(`<p>`))
					return x.Remove()
				}
				return nil
			}),
			Out: `<p>y`,
		},
		{
			Name: "RemoveInsertAfterEnd",
			In:   `<div>x</div>y`,
			Do: each(func(x *Transformer, tok *Token) error {
				if tok.Kind() == StartTag {
					if err := x.Remove(); err != nil {
						return err
					}
					return x.InsertAfter([]byte(`<hr>`))
				}
				return nil
			}),
			Out: `<hr>y`,
		},
		{
			Name: "WrapRemove",
			In:   `<div>x</div>y`,
			Do: each(func(x *Transformer, tok *Token) error {
				if tok.Kind() == StartTag {
					x.Wrap([]byte(`<w>`), []byte(`</w>`))
					return x.Remove()
				}
				return nil
			}),
			Out: `<w></w>y`,
		},
		{
			Name: "WrapEmpty",
			In:   `<div>x</div>y`,
			Do: each(func(x *Transformer, tok *Token) error {
				if tok.Kind() == StartTag {
					x.Wrap([]byte(`<w>`), []byte(`</w>`))
					return x.Empty()
				}
				return nil
			}),
			Out: `<w><div></div></w>y`,
		},
		{
			// markup inserted after the start tag stays in the emptied
			// element, and markup inserted after Empty goes after it
			Name: "InsertAfterEmpty",
			In:   `a<p>x</p>b<p>y</p>c`,
			Do: each(func(x *Transformer, tok *Token) error {
				if tok.Kind() != StartTag {
					return nil
				}
				first := tok.Pos() == 1
				x.Wrap([]byte(`<w>`), []byte(`</w>`))
				if first {
					x.InsertAfter([]byte(`[A]`))
					return x.Empty()
				}
				if err := x.Empty(); err != nil {
					return err
				}
				return x.InsertAfter([]byte(`[A]`))
			}),
			Out: `a<w><p>[A]</p></w>b<w><p></p></w>[A]c`,
		},
		{
			Name: "InsertAfterEmptyXML",
			Mode: XML,
			In:   `a<p>x</p>b<p>y</p>c`,
			Do: each(func(x *Transformer, tok *Token) error {
				if tok.Kind() != StartTag {
					return nil
				}
				first := tok.Pos() == 1
				x.Wrap([]byte(`<w>`), []byte(`</w>`))
				if first {
					x.InsertAfter([]byte(`[A]`))
					return x.Empty()
				}
				if err := x.Empty(); err != nil {
					return err
				}
				return x.InsertAfter([]byte(`[A]`))
			}),
			Out: `a<w><p>[A]</p></w>b<w><p/></w>[A]c`,
		},
		{
			// XHTML with an XML declaration follows XML rules in Universal
			// mode, so void elements get an end tag.
			Name: "ExpandSelfClosingUniversal",
			Mode: Universal,
			In:   `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml"><br/><p/></html><br/>`,
			Do: each(func(x *Transformer, tok *Token) error {
				if tok.Kind() == StartTag {
					return x.ExpandSelfClosing()
				}
				return nil
			}),
			Out: `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml"><br></br><p></p></html><br></br>`,
		},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			var b bytes.Buffer
			x := NewTransformer(&b, strings.NewReader(tc.In))
			x.SetMode(tc.Mode)
			x.SetStrict(tc.Strict)
			if tc.Depth != 0 {
				x.SetMaxDepth(tc.Depth)
			}
			err := tc.Do(x)
			if cerr := x.Close(); err == nil {
				err = cerr
			} else if cerr != nil && !errors.Is(cerr, err) && cerr != err {
				t.Errorf("Close returned %v, but operation returned %v", cerr, err)
			}
			if tc.Out == "" {
				if err == nil {
					t.Errorf("expected error, got output %q", b.String())
				} else {
					t.Logf("error (expected): %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if b.String() != tc.Out {
				t.Errorf("output:\n got %s\nwant %s", b.String(), tc.Out)
			}
		})
	}
}

// TestTransformSelfClosingEdits checks edits combined with ExpandSelfClosing
// and Empty on the start tags named i, g or br.
func TestTransformSelfClosingEdits(t *testing.T) {
	expand := func(x *Transformer) error { return x.ExpandSelfClosing() }
	empty := func(x *Transformer) error { return x.Empty() }
	remove := func(x *Transformer) error { return x.Remove() }
	unwrap := func(x *Transformer) error { return x.Unwrap() }
	replace := func(x *Transformer) error { return x.Replace([]byte("R")) }
	rename := func(x *Transformer) error { return x.Rename("j") }
	wrap := func(x *Transformer) error { return x.Wrap([]byte("["), []byte("]")) }
	after := func(x *Transformer) error { return x.InsertAfter([]byte("+")) }
	const decl = `<?xml version="1.0"?>`
	const xhtmlBr = decl + `<p xmlns="http://www.w3.org/1999/xhtml">a<br></br>b<br/>c<br>d</p>`
	for _, tc := range []struct {
		mode      Mode
		xmlSyntax bool
		in        string
		ops       []func(*Transformer) error
		out       string
	}{
		{HTML, true, `<p>a<i/>b`, []func(*Transformer) error{expand}, `<p>a<i></i>b`},
		{HTML, true, `<p>a<i/>b`, []func(*Transformer) error{expand, remove}, `<p>ab`},
		{HTML, true, `<p>a<i/>b`, []func(*Transformer) error{remove, expand}, `<p>ab`},
		{HTML, true, `<p>a<i/>b`, []func(*Transformer) error{expand, unwrap}, `<p>ab`},
		{HTML, true, `<p>a<i/>b`, []func(*Transformer) error{expand, replace}, `<p>aRb`},
		{HTML, true, `<p>a<i/>b`, []func(*Transformer) error{expand, rename}, `<p>a<j></j>b`},
		{HTML, true, `<p>a<i/>b`, []func(*Transformer) error{expand, wrap, after}, `<p>a[<i>+</i>]b`},
		{HTML, true, `<p>a<i/>b`, []func(*Transformer) error{after, expand}, `<p>a<i>+</i>b`},
		{XML, false, `<p>a<i/>b</p>`, []func(*Transformer) error{expand, after}, `<p>a<i>+</i>b</p>`},
		{XML, false, `<p>a<i/>b</p>`, []func(*Transformer) error{expand, unwrap, after}, `<p>a+b</p>`},
		{XML, false, `<p>a<i/>b</p>`, []func(*Transformer) error{expand, replace, after}, `<p>aR+b</p>`},
		{HTML, false, `<svg><g/></svg>`, []func(*Transformer) error{expand, after}, `<svg><g>+</g></svg>`},
		{HTML, false, `<p>a<br/>b`, []func(*Transformer) error{expand, after}, `<p>a<br>+b`},
		{HTML, true, `<p>a<i/>b`, []func(*Transformer) error{expand, remove, after}, `<p>a+b`},
		{XML, false, `<p>a<i/>b</p>`, []func(*Transformer) error{expand, remove}, `<p>ab</p>`},
		{HTML, false, `<p>a<i/>b`, []func(*Transformer) error{expand, remove}, `<p>a`},

		// Empty writes an empty element for HTML parsers
		{HTML, false, `<i a=1>x</i>y`, []func(*Transformer) error{empty}, `<i a=1></i>y`},
		{HTML, false, `<i>x</i>y`, []func(*Transformer) error{rename, empty}, `<j></j>y`},
		{HTML, false, `<i>x</i>y`, []func(*Transformer) error{wrap, empty}, `[<i></i>]y`},
		{HTML, false, `<i/>x</i>y`, []func(*Transformer) error{empty}, `<i></i>y`},
		{HTML, false, `<i/>x</i>y`, []func(*Transformer) error{expand, empty}, `<i></i>y`},
		{HTML, true, `<i/>x`, []func(*Transformer) error{empty}, `<i></i>x`},
		{HTML, true, `<i/>x`, []func(*Transformer) error{empty, expand}, `<i></i>x`},
		{HTML, false, `<svg><g>x</g></svg>`, []func(*Transformer) error{empty}, `<svg><g/></svg>`},
		{HTML, false, `<svg><g/></svg>`, []func(*Transformer) error{expand, empty}, `<svg><g/></svg>`},
		{HTML, false, `<svg><g/></svg>`, []func(*Transformer) error{empty, expand}, `<svg><g></g></svg>`},
		{HTML, false, `<svg><g a="1"//>x</svg>`, []func(*Transformer) error{expand}, `<svg><g a="1"></g>x</svg>`},
		{HTML, true, `<p>a<i //>b`, []func(*Transformer) error{expand}, `<p>a<i ></i>b`},
		{HTML, false, `<i//>x</i>y`, []func(*Transformer) error{empty}, `<i></i>y`},
		{XML, false, `<i>x</i>y`, []func(*Transformer) error{empty}, `<i/>y`},
		{XML, false, `<p><i/>y</p>`, []func(*Transformer) error{empty, after}, `<p><i>+</i>y</p>`},
		{XML, false, `<p><i/>y</p>`, []func(*Transformer) error{after, empty}, `<p><i>+</i>y</p>`},
		{XML, false, `<p><i a="1"/>y</p>`, []func(*Transformer) error{wrap, empty, after}, `<p>[<i a="1">+</i>]y</p>`},
		{HTML, false, `<svg><g/></svg>`, []func(*Transformer) error{empty, after}, `<svg><g>+</g></svg>`},
		{HTML, false, `<p>a<br>b`, []func(*Transformer) error{empty, after}, `<p>a<br/>+b`},
		{Universal, false, `<i>x</i><b xmlns="http://www.w3.org/1999/xhtml"><i>x</i></b>`, []func(*Transformer) error{empty}, `<i></i><b xmlns="http://www.w3.org/1999/xhtml"><i></i></b>`},
		{Universal, false, decl + `<b xmlns="http://www.w3.org/1999/xhtml"><i>x</i></b>`, []func(*Transformer) error{empty}, decl + `<b xmlns="http://www.w3.org/1999/xhtml"><i/></b>`},

		// void elements under XML rules open an element unless self-closed,
		// closed by their end tag or before the next token
		{Universal, false, xhtmlBr, []func(*Transformer) error{remove}, decl + `<p xmlns="http://www.w3.org/1999/xhtml">abcd</p>`},
		{Universal, false, xhtmlBr, []func(*Transformer) error{unwrap}, decl + `<p xmlns="http://www.w3.org/1999/xhtml">abcd</p>`},
		{Universal, false, xhtmlBr, []func(*Transformer) error{rename}, decl + `<p xmlns="http://www.w3.org/1999/xhtml">a<j></j>b<j/>c<j>d</p>`},
		{Universal, false, xhtmlBr, []func(*Transformer) error{wrap}, decl + `<p xmlns="http://www.w3.org/1999/xhtml">a[<br></br>]b[<br/>]c[<br>]d</p>`},
		{Universal, false, xhtmlBr, []func(*Transformer) error{empty}, decl + `<p xmlns="http://www.w3.org/1999/xhtml">a<br/>b<br/>c<br/>d</p>`},
		{Universal, false, xhtmlBr, []func(*Transformer) error{expand}, decl + `<p xmlns="http://www.w3.org/1999/xhtml">a<br></br>b<br></br>c<br>d</p>`},
		{Universal, false, xhtmlBr, []func(*Transformer) error{after}, decl + `<p xmlns="http://www.w3.org/1999/xhtml">a<br></br>+b<br/>+c<br>+d</p>`},
		{Universal, false, xhtmlBr, []func(*Transformer) error{expand, after}, decl + `<p xmlns="http://www.w3.org/1999/xhtml">a<br></br>+b<br></br>+c<br>+d</p>`},
		{Universal, false, xhtmlBr, []func(*Transformer) error{after, wrap}, decl + `<p xmlns="http://www.w3.org/1999/xhtml">a[<br></br>]+b[<br/>]+c[<br>]+d</p>`},
		{Universal, false, xhtmlBr, []func(*Transformer) error{after, remove}, decl + `<p xmlns="http://www.w3.org/1999/xhtml">a+b+c+d</p>`},
		{HTML, true, `<p>a<br></br>b<br> </br>c<br>d<br/>e`, []func(*Transformer) error{after}, `<p>a<br></br>+b<br> </br>+c<br>+d<br/>+e`},
		{HTML, true, `<p>a<br></br>b<br> </br>c<br>d<br/>e`, []func(*Transformer) error{wrap, after, wrap}, `<p>a[[<br></br>]]+b[[<br> </br>]]+c[[<br>]]+d[[<br/>]]+e`},
		{Universal, false, xhtmlBr, []func(*Transformer) error{empty, after}, decl + `<p xmlns="http://www.w3.org/1999/xhtml">a<br/>+b<br/>+c<br/>+d</p>`},
		{Universal, false, xhtmlBr, []func(*Transformer) error{after, empty}, decl + `<p xmlns="http://www.w3.org/1999/xhtml">a<br/>+b<br/>+c<br/>+d</p>`},
	} {
		var b bytes.Buffer
		x := NewTransformer(&b, strings.NewReader(tc.in))
		x.SetMode(tc.mode)
		x.SetXMLSyntax(tc.xmlSyntax)
		for x.Next() {
			if tok := x.Token(); tok.Kind() == StartTag && (tok.Is("i") || tok.Is("g") || tok.Is("br")) {
				for _, op := range tc.ops {
					if err := op(x); err != nil {
						t.Fatalf("%q: %v", tc.in, err)
					}
				}
			}
		}
		if err := x.Close(); err != nil {
			t.Fatalf("%q: %v", tc.in, err)
		}
		if b.String() != tc.out {
			t.Errorf("%s xml syntax %v %q: got %s, want %s", tc.mode, tc.xmlSyntax, tc.in, b.String(), tc.out)
		}
	}
}

func TestChildren(t *testing.T) {
	r := NewReader(strings.NewReader(`<ul><li>a<li>b <b>c</b></ul><p>d`))
	var got []string
	for r.Find(Tag("li")) {
		var text []byte
		for tok := range r.Children() {
			if tok.Kind() == Text {
				text = tok.AppendText(text)
			}
		}
		if tok := r.Token(); tok.Kind() != EndTag || !tok.Is("li") {
			t.Errorf("after Children: %v", tok)
		}
		got = append(got, string(text))
	}
	if err := r.Err(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, "|") != "a|b c" {
		t.Errorf("got %q", got)
	}
}

// FuzzTransform applies edits chosen by ops to each token, checking that the
// Transformer doesn't panic and that the output can be read again.
func FuzzTransform(f *testing.F) {
	for _, in := range allVectors() {
		f.Add(in, uint8(0), []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12})
	}
	f.Fuzz(func(t *testing.T, in string, mode uint8, ops []byte) {
		if len(ops) == 0 {
			return
		}
		var b bytes.Buffer
		x := NewTransformer(&b, iotest.OneByteReader(strings.NewReader(in)))
		x.SetMode(modes[int(mode)%len(modes)])
		x.SetStrict(mode&0x80 != 0)
		x.SetChunkSize(int(mode>>2&15) + 1)
		if mode&0x40 != 0 {
			x.SetMaxDepth(2) // operations on untracked elements
		}
		// Up to 4 operations are applied to a token: the next one too if the
		// high bit is set.
		for j := 0; x.Next(); {
			for k := 0; ; k++ {
				tok := x.Token()
				if tok == nil {
					break
				}
				op := ops[j%len(ops)]
				j++
				switch op % 16 {
				case 0:
					if op != 0 {
						x.SetChunkSize(int(op >> 4))
					}
				case 1:
					x.Remove()
				case 2:
					x.Unwrap()
				case 3:
					x.Wrap([]byte("<w>"), []byte("</w>"))
				case 4:
					x.Rename("r")
				case 5:
					if op&0x10 == 0 {
						x.SetAttr("a", "<&\">")
					} else {
						// a valid position: -(n+1) to n
						n := len(tok.attrs)
						x.SetAttrAt("a", "<&\">", int(op>>5)%(2*n+2)-(n+1))
					}
				case 6:
					x.RemoveAttrs(func(a Attr) bool { return len(a.Name())%2 == 0 })
				case 7:
					x.ExpandSelfClosing()
				case 8:
					x.Empty()
				case 9:
					x.InsertBefore([]byte("<!--b-->"))
				case 10:
					x.InsertAfter([]byte("<!--a-->"))
				case 11:
					x.Replace([]byte("R"))
				case 12:
					x.Skip()
				case 13:
					for range x.Children() {
					}
				case 14:
					x.ReadText(nil, int(op>>4))
				case 15:
					if tok.Kind() == EndTag {
						x.RenameEnd("e")
					}
				}
				if op&0x80 == 0 || k == 3 {
					break
				}
			}
		}
		x.Close()
		r := NewReader(bytes.NewReader(b.Bytes()))
		for r.Next() {
		}
	})
}

func TestTransformAttrs(t *testing.T) {
	for _, tc := range []struct {
		in, out string
		do      func(x *Transformer)
	}{
		{`<div a=1 data-b="2"id=x>`, `<div a=1 id=x>`, func(x *Transformer) { x.RemoveAttr("data-b") }},
		{`<div data-a="1"id=x>`, `<div id=x>`, func(x *Transformer) { x.RemoveAttr("data-a") }},
		{`<div a="1"b="2">`, `<div a="1"b="2" c="3">`, func(x *Transformer) { x.SetAttr("c", "3") }},
		{`<div a="1"b="2" c>`, `<div a="1" x="2" c>`, func(x *Transformer) { x.RenameAttr("b", "x") }},
		{`<svg:svg xmlns:svg="u" width=1>`, `<svg:svg xmlns="u" width=1>`, func(x *Transformer) { x.RenameAttr("xmlns:svg", "xmlns") }},
	} {
		var b bytes.Buffer
		x := NewTransformer(&b, strings.NewReader(tc.in))
		for x.Next() {
			if x.Token().Kind() == StartTag {
				tc.do(x)
			}
		}
		if err := x.Close(); err != nil {
			t.Fatal(err)
		}
		if b.String() != tc.out {
			t.Errorf("%q: got %q, want %q", tc.in, b.String(), tc.out)
		}
	}
}

func TestTransformIncomplete(t *testing.T) {
	var b bytes.Buffer
	x := NewTransformer(&b, strings.NewReader(`<p data-a=1><div data-a="1`))
	for x.Next() {
		if x.Token().Kind() == StartTag {
			if err := x.RemoveAttr("data-a"); err != nil && err != ErrIncompleteTag {
				t.Fatal(err)
			}
		}
	}
	x.InsertAtEnd([]byte("<!--end-->"))
	for range 2 {
		if err := x.Close(); err != nil {
			t.Fatal(err)
		}
		if want := `<p><div data-a="1<!--end-->`; b.String() != want {
			t.Errorf("got %q, want %q", b.String(), want)
		}
	}
}

// TestTransformInvalidOp checks that operations on the wrong kind of token or
// in the wrong state return ErrInvalidOp, don't change the output, and don't
// stop the Transformer.
func TestTransformInvalidOp(t *testing.T) {
	startOps := map[string]func(x *Transformer) error{
		"Unwrap":            (*Transformer).Unwrap,
		"Wrap":              func(x *Transformer) error { return x.Wrap([]byte("<w>"), []byte("</w>")) },
		"Rename":            func(x *Transformer) error { return x.Rename("r") },
		"ExpandSelfClosing": (*Transformer).ExpandSelfClosing,
		"Empty":             (*Transformer).Empty,
		"SetAttr":           func(x *Transformer) error { return x.SetAttr("a", "1") },
		"SetAttrAt":         func(x *Transformer) error { return x.SetAttrAt("a", "1", 0) },
		"RenameAttr":        func(x *Transformer) error { return x.RenameAttr("a", "b") },
		"RemoveAttr":        func(x *Transformer) error { return x.RemoveAttr("a") },
		"RemoveAttrs":       func(x *Transformer) error { return x.RemoveAttrs(func(Attr) bool { return true }) },
	}
	anyOps := map[string]func(x *Transformer) error{
		"Remove":       (*Transformer).Remove,
		"Replace":      func(x *Transformer) error { return x.Replace([]byte("R")) },
		"InsertBefore": func(x *Transformer) error { return x.InsertBefore([]byte("B")) },
		"InsertAfter":  func(x *Transformer) error { return x.InsertAfter([]byte("A")) },
	}
	const in = `<p a=1>x<!--c--></p><br/>`
	check := func(name string, match func(tok *Token) bool, op func(x *Transformer) error) {
		t.Helper()
		var b bytes.Buffer
		x := NewTransformer(&b, strings.NewReader(in))
		x.SetMode(XML)
		for x.Next() {
			tok := x.Token()
			if match(tok) {
				if err := op(x); !errors.Is(err, ErrInvalidOp) {
					t.Errorf("%s on %s: got %v, want ErrInvalidOp", name, tok, err)
				}
			}
			// later edits still apply
			if tok.Kind() == StartTag && tok.Is("br") {
				if err := x.SetAttr("b", "2"); err != nil {
					t.Errorf("%s: SetAttr: %v", name, err)
				}
			}
		}
		if err := x.Close(); err != nil {
			t.Errorf("%s: Close: %v", name, err)
		}
		if want := `<p a=1>x<!--c--></p><br b="2"/>`; b.String() != want {
			t.Errorf("%s: got %q, want %q", name, b.String(), want)
		}
	}
	kind := func(k Kind) func(tok *Token) bool {
		return func(tok *Token) bool { return tok.Kind() == k }
	}
	for name, op := range startOps {
		for _, k := range []Kind{EndTag, Text, Comment} {
			check(name, kind(k), op)
		}
	}
	for _, k := range []Kind{StartTag, Text, Comment} {
		check("RenameEnd", kind(k), func(x *Transformer) error { return x.RenameEnd("e") })
	}
	check("SetAttrAt out of range", func(tok *Token) bool { return tok.Kind() == StartTag && tok.Is("p") }, func(x *Transformer) error {
		return x.SetAttrAt("a", "1", 3)
	})
	check("HoldRelease", kind(Text), func(x *Transformer) error { return x.HoldRelease(Hold{}) })
	check("HoldDiscard", kind(Comment), func(x *Transformer) error { return x.HoldDiscard(Hold{}, nil) })

	// without a current token
	for name, op := range startOps {
		x := NewTransformer(io.Discard, strings.NewReader(in))
		if err := op(x); !errors.Is(err, ErrInvalidOp) {
			t.Errorf("%s before Next: got %v, want ErrInvalidOp", name, err)
		}
	}
	for name, op := range anyOps {
		x := NewTransformer(io.Discard, strings.NewReader(in))
		if err := op(x); !errors.Is(err, ErrInvalidOp) {
			t.Errorf("%s before Next: got %v, want ErrInvalidOp", name, err)
		}
		if err := x.Close(); err != nil {
			t.Errorf("%s before Next: Close: %v", name, err)
		}
		if err := op(x); !errors.Is(err, ErrInvalidOp) {
			t.Errorf("%s after Close: got %v, want ErrInvalidOp", name, err)
		}
	}
}

// TestTransformInvalidOpMessages checks the messages of errors wrapping
// ErrInvalidOp.
func TestTransformInvalidOpMessages(t *testing.T) {
	x := NewTransformer(io.Discard, strings.NewReader(`<p a=1>x</p>`))
	want := func(err error, msg string) {
		t.Helper()
		if !errors.Is(err, ErrInvalidOp) || err.Error() != "htmlstream: invalid operation: "+msg {
			t.Errorf("got %v, want %q", err, msg)
		}
	}
	want(x.Remove(), "removal with no current token")
	want(x.HoldRelease(Hold{}), "HoldRelease called with the zero Hold")
	x.Next()
	want(x.HoldDiscard(Hold{}, nil), "HoldDiscard called with the zero Hold")
	want(x.SetAttrAt("b", "2", 5), "attribute position 5 out of range (the tag has 1 attributes)")
	want(x.RenameEnd("q"), "end tag rename of a start tag")
	x.Next()
	want(x.SetAttr("b", "2"), "attribute edit of a text token")
	x.Next()
	want(x.Rename("q"), "rename of an end tag")
}

func TestTransformSetAttrAt(t *testing.T) {
	at := func(name, value string, at int) func(x *Transformer) error {
		return func(x *Transformer) error { return x.SetAttrAt(name, value, at) }
	}
	seq := func(fns ...func(x *Transformer) error) func(x *Transformer) error {
		return func(x *Transformer) error {
			for _, fn := range fns {
				if err := fn(x); err != nil {
					return err
				}
			}
			return nil
		}
	}
	set := func(name, value string) func(x *Transformer) error {
		return func(x *Transformer) error { return x.SetAttr(name, value) }
	}
	remove := func(name string) func(x *Transformer) error {
		return func(x *Transformer) error { return x.RemoveAttr(name) }
	}
	rename := func(name, newName string) func(x *Transformer) error {
		return func(x *Transformer) error { return x.RenameAttr(name, newName) }
	}
	const abc = `<a b="1" c="2" d="3">`
	for _, tc := range []struct {
		name string
		mode Mode
		in   string
		do   func(x *Transformer) error // applied to the first start tag
		out  string                     // empty if an error is expected
	}{
		{"First", HTML, abc, at("x", "v", 0), `<a x="v" b="1" c="2" d="3">`},
		{"Second", HTML, abc, at("x", "v", 1), `<a b="1" x="v" c="2" d="3">`},
		{"Last", HTML, abc, at("x", "v", 2), `<a b="1" c="2" x="v" d="3">`},
		{"End", HTML, abc, at("x", "v", 3), `<a b="1" c="2" d="3" x="v">`},
		{"EndNegative", HTML, abc, at("x", "v", -1), `<a b="1" c="2" d="3" x="v">`},
		{"LastNegative", HTML, abc, at("x", "v", -2), `<a b="1" c="2" x="v" d="3">`},
		{"FirstNegative", HTML, abc, at("x", "v", -4), `<a x="v" b="1" c="2" d="3">`},
		{"OutOfRange", HTML, abc, at("x", "v", 4), ""},
		{"OutOfRangeNegative", HTML, abc, at("x", "v", -5), ""},
		{"NoAttrs", HTML, `<a>`, at("x", "v", 0), `<a x="v">`},
		{"NoAttrsNegative", HTML, `<a>`, at("x", "v", -1), `<a x="v">`},
		{"NoAttrsOutOfRange", HTML, `<a>`, at("x", "v", 1), ""},
		{"EndTag", HTML, `</a>`, at("x", "v", 0), ""},

		// existing attributes with the name are removed
		{"Move", HTML, `<svg width=1 xmlns="u">`, at("xmlns", "u", 0), `<svg xmlns="u" width=1>`},
		{"MoveUnquoted", HTML, `<a b="1" c=2>`, at("c", "2", 0), `<a c="2" b="1">`},
		{"InPlace", HTML, `<a b="1"c="2">`, at("b", "3", 0), `<a b="3" c="2">`},
		{"InPlaceLast", HTML, `<a b="1"c="2">`, at("c", "3", 1), `<a b="1" c="3">`},
		{"Duplicates", HTML, `<a b="1" b="2" c="3">`, at("b", "x", 2), `<a b="x" c="3">`},
		{"DuplicatesEnd", HTML, `<a b="1" c="3" b="2">`, at("b", "x", -1), `<a c="3" b="x">`},
		{"DuplicatesCountInPositions", HTML, `<a b="1" b="2" c="3">`, at("x", "v", -2), `<a b="1" b="2" x="v" c="3">`},
		{"FoldHTML", HTML, `<a B="1" c="2">`, at("b", "2", -1), `<a c="2" b="2">`},
		{"ExactXML", XML, `<a B="1"/>`, at("b", "2", 0), `<a b="2" B="1"/>`},

		// separators
		{"NoSpace", HTML, `<a b="1"c="2">`, at("x", "v", 1), `<a b="1" x="v" c="2">`},
		{"NoSpaceKept", HTML, `<a b="1"c="2"d="3">`, at("x", "v", 3), `<a b="1"c="2"d="3" x="v">`},
		{"Newlines", HTML, "<a\n b=\"1\"\n c=\"2\">", at("x", "v", 1), "<a\n b=\"1\" x=\"v\"\n c=\"2\">"},
		{"Unquoted", HTML, `<a b=1>`, at("x", "v", -1), `<a b=1 x="v">`},
		{"UnquotedFirst", HTML, `<a b=1>`, at("x", "v", 0), `<a x="v" b=1>`},
		{"EmptyUnquoted", HTML, `<a b=>`, at("x", "v", -1), `<a b="" x="v">`},
		{"EmptyUnquotedSpace", HTML, `<a b= >`, at("x", "v", -1), `<a b= "" x="v">`},
		{"EmptyUnquotedSetAttr", HTML, `<a b=>`, set("x", "v"), `<a b="" x="v">`},
		{"EmptyUnquotedBefore", HTML, `<a b=>`, at("x", "v", 0), `<a x="v" b=>`},
		{"Escaped", HTML, `<a>`, at("x", `<&"'>`, 0), `<a x="&lt;&amp;&quot;&#39;&gt;">`},
		{"EqualsName", HTML, `<a 0 b=""=x>`, remove("b"), `<a 0="" =x>`},
		{"EqualsNameRenamed", HTML, `<a 0 b=""=x>`, seq(remove("b"), rename("0", "y")), `<a y="" =x>`},
		{"EqualsNameAdded", HTML, `<a 0 b=""=x>`, at("b", "1", 0), `<a b="1" 0="" =x>`},
		{"EqualsNameAfterAdded", HTML, `<a 0 b=""=x>`, at("b", "1", 1), `<a 0 b="1" =x>`},
		{"UnquotedBeforeSlash", HTML, `<a 0=0 b/>`, remove("b"), `<a 0=0 />`},
		{"UnquotedBeforeSlashMoved", HTML, `<a 0=0 b/>`, at("b", "1", 0), `<a b="1" 0=0 />`},
		{"UnquotedBeforeSlashSeparator", HTML, `<a 0=0 b/c>`, remove("b"), `<a 0=0 /c>`},
		{"UnquotedBeforeSlashExpand", XML, `<a 0=0 b/>`, seq(remove("b"), func(x *Transformer) error { return x.ExpandSelfClosing() }), `<a 0=0></a>`},
		{"SlashSeparator", HTML, `<a b="1"/c="2">`, at("x", "v", 1), `<a b="1" x="v"/c="2">`},

		// self-closing syntax
		{"SelfClosingXML", XML, `<a b="1"/>`, at("x", "v", -1), `<a b="1" x="v"/>`},
		{"SelfClosingXMLFirst", XML, `<a b="1"/>`, at("x", "v", 0), `<a x="v" b="1"/>`},
		{"SelfClosingNoAttrs", XML, `<a/>`, at("x", "v", 0), `<a x="v"/>`},
		{"SelfClosingHTML", HTML, `<br b=1 />`, at("x", "v", -1), `<br b=1 x="v" />`},
		{"SelfClosingUnquotedValue", HTML, `<br b=1/>`, at("x", "v", -1), `<br b=1/ x="v">`},
		{"Expand", XML, `<a b="1"/>`, seq(at("x", "v", 0), func(x *Transformer) error { return x.ExpandSelfClosing() }), `<a x="v" b="1"></a>`},
		{"ExpandEnd", XML, `<a b="1"/>`, seq(at("x", "v", -1), func(x *Transformer) error { return x.ExpandSelfClosing() }), `<a b="1" x="v"></a>`},
		{"Empty", XML, `<div b=1>x</div>`, seq(at("x", "v", 0), func(x *Transformer) error { return x.Empty() }), `<div x="v" b=1 />`},
		{"EmptyEnd", XML, `<div b=1>x</div>`, seq(at("x", "v", -1), func(x *Transformer) error { return x.Empty() }), `<div b=1 x="v"/>`},
		{"EmptyUnquotedEmptyValue", XML, `<div b=>x</div>`, func(x *Transformer) error { return x.Empty() }, `<div b=""/>`},
		{"EmptyHTML", HTML, `<div b=1>x</div>`, seq(at("x", "v", -1), func(x *Transformer) error { return x.Empty() }), `<div b=1 x="v"></div>`},
		{"RenameTag", XML, `<svg:svg width="1"/>`, seq(func(x *Transformer) error { return x.Rename("svg") }, at("xmlns", "u", 0)), `<svg xmlns="u" width="1"/>`},

		// order of added attributes
		{"SamePosition", HTML, abc, seq(at("x", "1", 1), at("y", "2", 1)), `<a b="1" x="1" y="2" c="2" d="3">`},
		{"SetAttrThenEnd", HTML, abc, seq(set("x", "1"), at("y", "2", -1)), `<a b="1" c="2" d="3" x="1" y="2">`},
		{"EndThenSetAttr", HTML, abc, seq(at("y", "2", -1), set("x", "1")), `<a b="1" c="2" d="3" y="2" x="1">`},
		{"ReAdd", HTML, abc, seq(at("x", "1", 0), at("y", "2", 0), at("x", "3", 0)), `<a y="2" x="3" b="1" c="2" d="3">`},
		{"MoveAdded", HTML, abc, seq(set("x", "1"), at("x", "2", 0)), `<a x="2" b="1" c="2" d="3">`},

		// positions refer to the original attributes
		{"AfterRemove", HTML, abc, seq(remove("b"), at("x", "v", 1)), `<a x="v" c="2" d="3">`},
		{"BeforeRemove", HTML, abc, seq(at("x", "v", 1), remove("b")), `<a x="v" c="2" d="3">`},
		{"RemoveAtPosition", HTML, abc, seq(remove("c"), at("x", "v", 1)), `<a b="1" x="v" d="3">`},
		{"RemoveAllAndAdd", HTML, abc, seq(func(x *Transformer) error {
			return x.RemoveAttrs(func(Attr) bool { return true })
		}, at("x", "v", 2)), `<a x="v">`},
		{"AfterAdd", HTML, abc, seq(at("x", "v", 0), at("y", "w", 2)), `<a x="v" b="1" c="2" y="w" d="3">`},
		{"AfterSetAttr", HTML, abc, seq(set("y", "w"), at("x", "v", 1)), `<a b="1" x="v" c="2" d="3" y="w">`},
		{"AfterRename", HTML, abc, seq(rename("c", "z"), at("x", "v", 2)), `<a b="1" z="2" x="v" d="3">`},

		// later edits to an added attribute
		{"SetAttrAfter", HTML, abc, seq(at("x", "1", 0), set("x", "2")), `<a x="2" b="1" c="2" d="3">`},
		{"SetAttrAfterFold", HTML, abc, seq(at("x", "1", 0), set("X", "2")), `<a x="2" b="1" c="2" d="3">`},
		{"RemoveAttrAfter", HTML, abc, seq(at("x", "1", 0), remove("x")), abc},
		{"RenameAttrAfter", HTML, abc, seq(at("x", "1", 0), rename("x", "y")), `<a y="1" b="1" c="2" d="3">`},
		{"RemoveAttrsAfter", HTML, abc, seq(at("x", "v", 0), func(x *Transformer) error {
			return x.RemoveAttrs(func(a Attr) bool { return a.Value() == "v" })
		}), abc},

		// edits match the names given by RenameAttr
		{"RenamedSet", HTML, `<a b="1" c="2">`, seq(rename("b", "x"), set("x", "v")), `<a x="v" c="2">`},
		{"RenamedSetAt", HTML, `<a b="1" c="2">`, seq(rename("b", "x"), at("x", "v", -1)), `<a c="2" x="v">`},
		{"RenamedRemove", HTML, `<a b="1" c="2">`, seq(rename("b", "x"), remove("x")), `<a c="2">`},
		{"RenamedOldName", HTML, `<a b="1" c="2">`, seq(rename("b", "x"), remove("b")), `<a x="1" c="2">`},
		{"RenamedTwice", HTML, `<a b="1" c="2">`, seq(rename("b", "x"), rename("x", "y")), `<a y="1" c="2">`},
		{"RenamedValue", HTML, `<a b='1&amp;' c="2">`, seq(rename("b", "x"), func(x *Transformer) error {
			return x.RemoveAttrs(func(a Attr) bool { return a.Is("x") && a.Value() == "1&" && string(a.Raw()) == `x='1&amp;'` })
		}), `<a c="2">`},
		{"SetValue", HTML, `<a b='1' c="2">`, seq(set("b", "<"), func(x *Transformer) error {
			return x.RemoveAttrs(func(a Attr) bool { return a.Value() == "<" })
		}), `<a c="2">`},

		// edits apply to the first start tag only
		{"NextTagUnchanged", HTML, `<a b="1"><a b="1"c=2/>`, at("x", "v", 0), `<a x="v" b="1"><a b="1"c=2/>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var b bytes.Buffer
			x := NewTransformer(&b, strings.NewReader(tc.in))
			x.SetMode(tc.mode)
			var err error
			for first := true; x.Next(); first = false {
				if first {
					err = tc.do(x)
				}
			}
			if cerr := x.Close(); err == nil {
				err = cerr
			}
			if tc.out == "" {
				if err == nil {
					t.Errorf("expected error, got output %q", b.String())
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if b.String() != tc.out {
				t.Errorf("output:\n got %s\nwant %s", b.String(), tc.out)
			}
		})
	}
}

// FuzzTransformAttrs applies SetAttr, SetAttrAt, RemoveAttr and RenameAttr to
// start tags and checks the attributes read from the output against a model.
func FuzzTransformAttrs(f *testing.F) {
	for _, in := range allVectors() {
		f.Add(in, uint8(0), []byte{0x01, 0x12, 0x23, 0x34, 0x45, 0x56, 0x67, 0x78, 0x89})
	}
	f.Add(`<a b="1"c=2 d= B=3><x y=/><p q= >`, uint8(0), []byte{0x01, 0x05, 0x20, 0x81, 0x09, 0x42, 0x13})
	f.Add(`<a b="1"c=2 d= B=3/><x y=/><p q= >`, uint8(1), []byte{0x01, 0x05, 0x20, 0x81, 0x09, 0x42, 0x13})
	// inputs which found bugs
	f.Add("<\x000\x00", uint8(0x00), []byte("0"))
	f.Add("<A 0=0 A/>", uint8(0x1b), []byte("9"))
	f.Add(`<A 0 B=""= 0>`, uint8('6'), []byte("1"))
	f.Add("<plAinteXt/><A", uint8('_'), []byte("0"))
	f.Add("<plAinteXt/><A", uint8('_'), []byte("1"))
	// values with tabs, newlines and carriage returns, under HTML and XML rules
	for mode := range uint8(3) {
		f.Add(`<p a="1"><svg xmlns="http://www.w3.org/2000/svg"><g x="1"/></svg><q>`, mode, []byte{0x01, 0xA0, 0x02, 0x01, 0xB9, 0x05, 0x02})
	}
	names := []string{"a", "b", "A", "c", "x"}
	values := []string{"", "1", `x"y`, "<&>", "'", "a\tb\r\nc\n"}
	type attr struct {
		name, value string
		at          int // index of the original attribute it is written before
	}
	f.Fuzz(func(t *testing.T, in string, mode uint8, ops []byte) {
		if len(ops) == 0 {
			return
		}
		var b bytes.Buffer
		x := NewTransformer(&b, strings.NewReader(in))
		x.SetMode(modes[int(mode)%len(modes)])
		var want [][]attr
		k := 0
		next := func() byte {
			op := ops[k%len(ops)]
			k++
			return op
		}
		for x.Next() {
			tok := x.Token()
			if tok.Kind() != StartTag {
				continue
			}
			n := len(tok.attrs)
			var orig []attr
			removed := make([]bool, n)
			for a := range tok.Attrs() {
				orig = append(orig, attr{string(a.Name()), a.Value(), len(orig)})
			}
			var added []attr
			is := func(a attr, name string) bool {
				if tok.HTMLRules() {
					return strings.EqualFold(a.name, name)
				}
				return a.name == name
			}
			deleteAdded := func(name string) {
				added = slices.DeleteFunc(added, func(a attr) bool { return is(a, name) })
			}
			for range int(next() % 4) {
				op := next()
				name := names[int(op>>3)%len(names)]
				value := values[int(op>>5)%len(values)]
				// the value read back: the Transformer writes tabs,
				// newlines and carriage returns as character references
				read := value
				var err error
				switch op % 4 {
				case 0:
					err = x.SetAttr(name, value)
					done := false
					for i := range orig {
						if !removed[i] && is(orig[i], name) {
							orig[i].value = read
							done = true
							break
						}
					}
					for i := range added {
						if !done && is(added[i], name) {
							added[i].value = read
							done = true
						}
					}
					if !done {
						added = append(added, attr{name, read, n})
					}
				case 1:
					at := int(next())%(2*n+2) - (n + 1)
					err = x.SetAttrAt(name, value, at)
					if at < 0 {
						at += n + 1
					}
					for i := range orig {
						if is(orig[i], name) {
							removed[i] = true
						}
					}
					deleteAdded(name)
					added = append(added, attr{name, read, at})
				case 2:
					err = x.RemoveAttr(name)
					for i := range orig {
						if is(orig[i], name) {
							removed[i] = true
						}
					}
					deleteAdded(name)
				case 3:
					newName := names[int(op>>5)%len(names)]
					err = x.RenameAttr(name, newName)
					for i := range orig {
						if !removed[i] && is(orig[i], name) {
							orig[i].name = newName
						}
					}
					for i := range added {
						if is(added[i], name) {
							added[i].name = newName
						}
					}
				}
				if err == ErrIncompleteTag {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			var w []attr
			if tok.Incomplete() {
				// not edited
				for a := range tok.Attrs() {
					w = append(w, attr{string(a.Name()), a.Value(), 0})
				}
			} else {
				for i := 0; i <= n; i++ {
					for _, a := range added {
						if a.at == i {
							w = append(w, a)
						}
					}
					if i < n && !removed[i] {
						w = append(w, orig[i])
					}
				}
			}
			want = append(want, w)
			var err error
			switch next() % 8 {
			case 0:
				err = x.ExpandSelfClosing()
			case 1:
				err = x.Empty()
			}
			if err != nil && err != ErrIncompleteTag {
				t.Fatal(err)
			}
		}
		if err := x.Close(); err != nil {
			return // the input can't be read
		}
		r := NewReader(bytes.NewReader(b.Bytes()))
		r.SetMode(modes[int(mode)%len(modes)])
		j := 0
		for r.Next() {
			tok := r.Token()
			if tok.Kind() != StartTag {
				continue
			}
			if j >= len(want) {
				t.Fatalf("output %q has more start tags than the input", b.String())
			}
			var got []attr
			for a := range tok.Attrs() {
				got = append(got, attr{string(a.Name()), a.Value(), 0})
			}
			w := want[j]
			ok := len(got) == len(w)
			for i := 0; ok && i < len(got); i++ {
				ok = got[i].name == w[i].name && got[i].value == w[i].value
			}
			if !ok {
				t.Fatalf("start tag %d %q in output %q:\n got %v\nwant %v", j, tok.Raw(), b.String(), got, w)
			}
			j++
		}
		if j != len(want) {
			t.Fatalf("output %q has %d start tags, want %d", b.String(), j, len(want))
		}
	})
}

func TestTransformHold(t *testing.T) {
	var h Hold
	for _, tc := range []struct {
		name, in, out string
		do            func(x *Transformer) error
	}{
		{"Release", `<p>a<b>b</b>c`, `<p>a<b>b</b>c`, each(func(x *Transformer, tok *Token) (err error) {
			switch {
			case tok.Kind() == StartTag && tok.Is("b"):
				h = x.Hold()
				return nil
			case tok.Kind() == Text && string(tok.Raw()) == "c":
				if string(x.Held(h)) != "<b>b</b>" {
					return errors.New("held " + string(x.Held(h)))
				}
				return x.HoldRelease(h)
			}
			return nil
		})},
		{"Discard", `<p>a<b>b</b>c`, `<p>a[b]c`, each(func(x *Transformer, tok *Token) (err error) {
			switch {
			case tok.Kind() == StartTag && tok.Is("b"):
				h = x.Hold()
				return nil
			case tok.Kind() == Text && string(tok.Raw()) == "c":
				return x.HoldDiscard(h, []byte("[b]"))
			}
			return nil
		})},
		{"DiscardEmptyElement", `<x> <!--c--> </x><y> z </y>`, `<y> z </y>`, each(func(x *Transformer, tok *Token) (err error) {
			switch tok.Kind() {
			case StartTag:
				if x.Holding() != 0 {
					return x.HoldRelease(h)
				}
				h = x.Hold()
				return nil
			case EndTag:
				if x.Holding() != 0 {
					if err := x.Remove(); err != nil {
						return err
					}
					return x.HoldDiscard(h, nil)
				}
			case Text:
				if x.Holding() != 0 && strings.TrimSpace(string(tok.Raw())) != "" {
					return x.HoldRelease(h)
				}
			}
			return nil
		})},
		{"Edits", `<p a=1>x<i>y</i>`, `<p a="2">!x<b>y</b>`, each(func(x *Transformer, tok *Token) (err error) {
			switch {
			case tok.Kind() == StartTag && tok.Is("p"):
				h = x.Hold()
				if err := x.InsertAfter([]byte("!")); err != nil {
					return err
				}
				return x.SetAttr("a", "2")
			case tok.Kind() == StartTag && tok.Is("i"):
				// the end tag edits apply after the hold ends
				if err := x.HoldRelease(h); err != nil {
					return err
				}
				return x.Rename("b")
			}
			return nil
		})},
		{"Nested", `abcd`, `a[b]d`, func(x *Transformer) error {
			x.SetChunkSize(1)
			var outer, inner Hold
			for x.Next() {
				var err error
				switch string(x.Token().Raw()) {
				case "a":
					outer = x.Hold()
				case "b":
					inner = x.Hold()
				case "c":
					// "b" is held back by both holds
					if string(x.Held(outer)) != "ab" || string(x.Held(inner)) != "b" {
						return errors.New("held " + string(x.Held(outer)))
					}
					if err = x.HoldDiscard(inner, append(append([]byte("["), x.Held(inner)...), ']')); err == nil {
						inner = x.Hold()
					}
				case "d":
					if err = x.HoldDiscard(inner, nil); err == nil {
						if x.Holding() != 1 || string(x.Held(outer)) != "a[b]" || x.Held(inner) != nil {
							return errors.New("held " + string(x.Held(outer)))
						}
						err = x.HoldRelease(outer)
					}
				}
				if err != nil {
					return err
				}
			}
			return nil
		}},
		{"Remove", `<p>a<b>b</b>c</p>d`, `[]d`, each(func(x *Transformer, tok *Token) (err error) {
			switch {
			case tok.Kind() == StartTag && tok.Is("p"):
				h = x.Hold()
				return x.Remove()
			case tok.Kind() == Text && string(tok.Raw()) == "d":
				if string(x.Held(h)) != "" {
					return errors.New("held " + string(x.Held(h)))
				}
				return x.HoldDiscard(h, []byte("[]"))
			}
			return nil
		})},
		{"Skip", `<p>a<b>b<i>c</i></b>d</p>`, `<p>a<!--s--><b>b<i>c</i></b>d</p>`, each(func(x *Transformer, tok *Token) (err error) {
			switch {
			case tok.Kind() == StartTag && tok.Is("b"):
				h = x.Hold()
				x.Skip()
				return x.Err()
			case tok.Kind() == Text && string(tok.Raw()) == "d":
				return x.HoldDiscard(h, append([]byte("<!--s-->"), x.Held(h)...))
			}
			return nil
		})},
		{"Close", `<p>a`, `<p>a<!--end-->`, func(x *Transformer) (err error) {
			x.Next()
			x.InsertAtEnd([]byte("<!--end-->"))
			x.Hold() // released by Close
			return nil
		}},
		{"BeforeFirst", `<p>a</p>`, `[]a</p>`, func(x *Transformer) (err error) {
			h = x.Hold() // from the first token on
			x.Next()
			x.Next()
			if string(x.Held(h)) != "<p>" {
				return errors.New("held " + string(x.Held(h)))
			}
			return x.HoldDiscard(h, []byte("[]"))
		}},
		{"AfterEnd", `<p>a</p><p> `, `<p>a</p><!--e-->`, func(x *Transformer) (err error) {
			if err := each(func(x *Transformer, tok *Token) (err error) {
				if tok.Kind() == StartTag && x.Holding() == 0 {
					h = x.Hold()
				} else if tok.Kind() == Text && strings.TrimSpace(string(tok.Raw())) != "" {
					err = x.HoldRelease(h)
				}
				return err
			})(x); err != nil {
				return err
			}
			// the empty p at the end of the input
			if string(x.Held(h)) != "<p> " {
				return errors.New("held " + string(x.Held(h)))
			}
			// a hold started after the end of the input holds nothing
			if e := x.Hold(); len(x.Held(e)) != 0 || x.HoldRelease(e) != nil {
				return errors.New("hold after the end of the input")
			}
			return x.HoldDiscard(h, []byte("<!--e-->"))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var b bytes.Buffer
			x := NewTransformer(&b, strings.NewReader(tc.in))
			if err := tc.do(x); err != nil {
				t.Fatal(err)
			}
			if err := x.Close(); err != nil {
				t.Fatal(err)
			}
			if b.String() != tc.out {
				t.Errorf("got %q, want %q", b.String(), tc.out)
			}
		})
	}
}

func TestTransformHoldErrors(t *testing.T) {
	// Discarding the start tag of an element with end tag edits, with the
	// hold of the start tag and with an outer hold.
	for _, op := range []func(x *Transformer) error{
		func(x *Transformer) error { return x.Wrap([]byte("<w>"), []byte("</w>")) },
		func(x *Transformer) error { return x.Rename("r") },
		func(x *Transformer) error { return x.Unwrap() },
	} {
		for _, depth := range []int{1024, 1} {
			var b bytes.Buffer
			x := NewTransformer(&b, strings.NewReader(`<div><p>a<b>b</b>c</p></div>`))
			x.SetMaxDepth(depth) // 1: the p is untracked
			x.Next()
			outer := x.Hold()
			x.Next()
			h := x.Hold()
			if err := op(x); err != nil {
				t.Fatal(err)
			}
			// the start tag isn't held back yet
			if err := x.HoldDiscard(h, nil); err != nil {
				t.Fatal(err)
			}
			h = x.Hold()
			x.Next()
			if err := x.HoldDiscard(h, nil); err != ErrHeldOpen {
				t.Errorf("HoldDiscard: got %v, want ErrHeldOpen", err)
			}
			if err := x.HoldRelease(h); err != nil {
				t.Fatal(err)
			}
			if err := x.HoldDiscard(outer, nil); err != ErrHeldOpen {
				t.Errorf("HoldDiscard of the outer hold: got %v, want ErrHeldOpen", err)
			}
			if x.Holding() != 1 {
				t.Error("HoldDiscard ended the hold")
			}
			if err := x.Close(); err != nil {
				t.Fatal(err)
			}
			if err := x.HoldRelease(outer); err != ErrHoldReleased {
				t.Errorf("HoldRelease after Close: got %v, want ErrHoldReleased", err)
			}
		}
	}

	var b bytes.Buffer
	x := NewTransformer(&b, strings.NewReader(`<p>aaaa</p>`))
	x.SetMaxHold(4)
	x.Next()
	x.Hold()
	if err := x.Close(); !errors.Is(err, ErrHoldLimit) {
		t.Errorf("got %v, want ErrHoldLimit", err)
	} else if want := "htmlstream: held output of 7 bytes exceeds MaxHold (4 bytes)"; err.Error() != want {
		t.Errorf("got %q, want %q", err, want)
	} else if n := b.Len(); x.Close() != err || b.Len() != n {
		t.Error("second Close: other result or more output than the first")
	} else if h := x.Hold(); h != (Hold{}) {
		t.Errorf("Hold after an error: got %v, want the zero Hold", h)
	}

	// raw of HoldDiscard counts toward the limit only inside another hold
	raw := []byte("<!--" + strings.Repeat("r", 40) + "-->")
	for _, nested := range []bool{false, true} {
		b.Reset()
		x := NewTransformer(&b, strings.NewReader(`<p>abc</p><div>d</div>`))
		x.SetMaxHold(30)
		x.Next()
		outer := x.Hold()
		if !nested {
			x.HoldRelease(outer)
		}
		h := x.Hold()
		for x.Next() && !x.Token().Is("div") {
		}
		err := x.HoldDiscard(h, raw)
		switch {
		case !nested && err != nil:
			t.Errorf("HoldDiscard over MaxHold without an enclosing hold: %v", err)
		case !nested && (x.Close() != nil || b.String() != string(raw)+`<div>d</div>`):
			t.Errorf("HoldDiscard over MaxHold without an enclosing hold: got %q", b.String())
		case nested && (!errors.Is(err, ErrHoldLimit) || x.Close() != err):
			t.Errorf("HoldDiscard over MaxHold in an enclosing hold: got %v", err)
		}
	}

	// HoldRelease and HoldDiscard of a hold other than the innermost open one
	// of the Transformer return an error and change nothing
	other := NewTransformer(io.Discard, strings.NewReader(`x`))
	other.Next()
	foreign := other.Hold()
	for _, tc := range []struct {
		name string
		hold func(outer, released, discarded Hold) Hold
		msg  string // after the method name, or "" for ErrHoldReleased
	}{
		{"Zero", func(_, _, _ Hold) Hold { return Hold{} }, "called with the zero Hold"},
		{"Outer", func(o, _, _ Hold) Hold { return o }, "called with an outer Hold (a hold inside it is open)"},
		{"Released", func(_, r, _ Hold) Hold { return r }, ""},
		{"Discarded", func(_, _, d Hold) Hold { return d }, ""},
		{"OtherTransformer", func(_, _, _ Hold) Hold { return foreign }, "called with a hold created by a different Transformer"},
	} {
		for _, op := range []string{"HoldRelease", "HoldDiscard"} {
			const in = `<p>a<b>b</b>c</p>`
			var b bytes.Buffer
			x := NewTransformer(&b, strings.NewReader(in))
			var outer, released, discarded, inner Hold
			err := each(func(x *Transformer, tok *Token) (err error) {
				switch string(tok.Raw()) {
				case "<p>":
					outer = x.Hold()
				case "a":
					released = x.Hold()
					discarded = x.Hold()
					if err = x.HoldDiscard(discarded, x.Held(discarded)); err == nil {
						err = x.HoldRelease(released)
					}
				case "<b>":
					inner = x.Hold()
				case "b":
					h := tc.hold(outer, released, discarded)
					if op == "HoldRelease" {
						err = x.HoldRelease(h)
					} else {
						err = x.HoldDiscard(h, []byte("[D]"))
					}
					switch {
					case tc.msg == "" && err != ErrHoldReleased:
						t.Errorf("%s %s: got %v, want ErrHoldReleased", op, tc.name, err)
					case tc.msg != "" && (!errors.Is(err, ErrInvalidOp) || err.Error() != "htmlstream: invalid operation: "+op+" "+tc.msg):
						t.Errorf("%s %s: got %v, want ErrInvalidOp with %q", op, tc.name, err, tc.msg)
					}
					if x.Holding() != 2 || string(x.Held(inner)) != "<b>" {
						t.Errorf("%s %s: %d holds, the innermost holding %q", op, tc.name, x.Holding(), x.Held(inner))
					}
					err = nil
				case "c":
					if err = x.HoldRelease(inner); err == nil {
						err = x.HoldRelease(outer)
					}
				}
				return err
			})(x)
			if err != nil {
				t.Fatalf("%s %s: %v", op, tc.name, err)
			}
			if err := x.Close(); err != nil || b.String() != in {
				t.Errorf("%s %s: got %q, %v", op, tc.name, b.String(), err)
			}
		}
	}
	if ErrHoldReleased.Error() != "htmlstream: hold already released" {
		t.Errorf("ErrHoldReleased: %q", ErrHoldReleased)
	}
}

// FuzzTransformHold checks that holds which are released, or discarded and
// replaced with the held output, don't change the output, also when ended
// after the end of the input, and that HoldRelease and HoldDiscard of a hold
// other than the innermost open one return an error and change nothing.
func FuzzTransformHold(f *testing.F) {
	for _, in := range allVectors() {
		f.Add(in, uint8(0), []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}, []byte{1, 0, 2, 1, 1, 3, 4, 11, 5, 1, 6, 7, 20, 3, 1})
	}
	f.Fuzz(func(t *testing.T, in string, mode uint8, ops, hops []byte) {
		if len(ops) == 0 || len(hops) == 0 {
			return
		}
		other := NewTransformer(io.Discard, strings.NewReader("x"))
		other.Next()
		foreign := other.Hold()
		run := func(holds bool) (string, error) {
			var b bytes.Buffer
			x := NewTransformer(&b, strings.NewReader(in))
			x.SetMode(modes[int(mode)%len(modes)])
			x.SetChunkSize(int(mode>>2&15) + 1)
			if mode&0x40 != 0 {
				x.SetMaxDepth(2)
			}
			var hs, ended []Hold // open (innermost last) and ended holds
			// end calls HoldRelease, or HoldDiscard with the held output
			end := func(h Hold, discard, clone bool) error {
				if !discard {
					return x.HoldRelease(h)
				}
				raw := x.Held(h)
				if clone {
					raw = slices.Clone(raw)
				}
				return x.HoldDiscard(h, raw)
			}
			// misuse ends h, which isn't the innermost open hold
			misuse := func(h Hold, discard bool, want error) {
				if x.Err() != nil {
					return
				}
				n := x.Holding()
				var err error
				if discard {
					err = x.HoldDiscard(h, []byte("[D]"))
				} else {
					err = x.HoldRelease(h)
				}
				if !errors.Is(err, want) || x.Holding() != n {
					t.Errorf("ending %v (discard %v): got %v, want %v, %d holds, want %d", h, discard, err, want, x.Holding(), n)
				}
			}
			for j := 0; x.Next(); j++ {
				if holds {
					h := hops[j%len(hops)]
					discard := h&8 != 0
					switch h % 8 {
					case 1:
						if h := x.Hold(); h != (Hold{}) {
							hs = append(hs, h)
						}
					case 2:
						if n := len(hs); n != 0 {
							err := end(hs[n-1], discard, h&16 != 0)
							if err == ErrHeldOpen {
								err = end(hs[n-1], false, false)
							}
							if err == nil {
								ended = append(ended, hs[n-1])
								hs = hs[:n-1]
							}
						}
					case 3:
						if len(hs) > 1 {
							misuse(hs[int(h>>4)%(len(hs)-1)], discard, ErrInvalidOp)
						}
					case 4:
						if len(ended) != 0 {
							misuse(ended[int(h>>4)%len(ended)], discard, ErrHoldReleased)
						}
					case 5:
						misuse(Hold{}, discard, ErrInvalidOp)
					case 6:
						misuse(foreign, discard, ErrInvalidOp)
					}
					if x.Holding() != len(hs) {
						t.Fatalf("%d holds, want %d", x.Holding(), len(hs))
					}
				}
				switch op := ops[j%len(ops)]; op % 13 {
				case 1:
					x.Remove()
				case 2:
					x.Unwrap()
				case 3:
					x.Wrap([]byte("<w>"), []byte("</w>"))
				case 4:
					x.Rename("r")
				case 5:
					x.SetAttr("a", "<&\">")
				case 6:
					x.ExpandSelfClosing()
				case 7:
					x.Empty()
				case 8:
					x.InsertBefore([]byte("<!--b-->"))
				case 9:
					x.InsertAfter([]byte("<!--a-->"))
				case 10:
					x.Replace([]byte("R"))
				case 11:
					x.Skip()
				case 12:
					for range x.Children() {
					}
				}
			}
			// end the holds after the end of the input
			for i := len(hs) - 1; i >= 0 && x.Err() == nil; i-- {
				h := hops[i%len(hops)]
				if err := end(hs[i], h&8 != 0, h&16 != 0); err != nil {
					t.Errorf("ending a hold after the end of the input: %v", err)
				}
			}
			err := x.Close()
			if n := b.Len(); x.Close() != err || b.Len() != n {
				t.Errorf("second Close: other result or more output than the first (%v)", err)
			}
			return b.String(), err
		}
		want, err1 := run(false)
		got, err2 := run(true)
		if (err1 == nil) != (err2 == nil) {
			t.Fatalf("errors: %v, %v", err1, err2)
		}
		if err1 == nil && got != want {
			t.Errorf("with holds:\n%q\nwithout:\n%q", got, want)
		}
	})
}

// TestTransformFail checks that Fail stops the Transformer: Close returns the
// first error and writes nothing more.
func TestTransformFail(t *testing.T) {
	errCaller := errors.New("caller")
	errRead := errors.New("read")

	var b bytes.Buffer
	x := NewTransformer(&b, strings.NewReader(`<p>a<b>b</b>c</p>`))
	x.Next()
	x.InsertAtEnd([]byte("<!--end-->"))
	x.Next()
	x.Hold()
	x.Next()
	x.Fail(errCaller)
	x.Fail(errRead) // the first error is kept
	if x.Next() || x.Err() != errCaller {
		t.Errorf("after Fail: Next returned true or Err is %v", x.Err())
	}
	if err := x.SetAttr("x", "y"); err != errCaller {
		t.Errorf("SetAttr after Fail: got %v", err)
	}
	if err := x.Close(); err != errCaller || b.Len() != 0 {
		t.Errorf("Close after Fail: got %v, wrote %q", err, b.String())
	}

	// a read error before the caller's error is kept
	b.Reset()
	x = NewTransformer(&b, io.MultiReader(strings.NewReader(`<p>a`), iotest.ErrReader(errRead)))
	for x.Next() {
	}
	x.Fail(errCaller)
	if err := x.Close(); err != errRead {
		t.Errorf("Close after a read error and Fail: got %v", err)
	}

	// Fail(nil) does nothing
	b.Reset()
	x = NewTransformer(&b, strings.NewReader(`<p>a</p>`))
	x.Next()
	x.Fail(nil)
	if err := x.Close(); err != nil || b.String() != `<p>a</p>` {
		t.Errorf("Close after Fail(nil): got %v, wrote %q", err, b.String())
	}

	// the output already flushed stays, the rest isn't written
	b.Reset()
	in := strings.Repeat("<p>a</p>", 1024) + "<p>tail</p>"
	x = NewTransformer(&b, strings.NewReader(in))
	for x.Next() && !bytes.Contains(x.Token().Raw(), []byte("tail")) {
	}
	x.Fail(errCaller)
	if err := x.Close(); err != errCaller || b.Len() == 0 || !strings.HasPrefix(in, b.String()) || strings.Contains(b.String(), "tail") {
		t.Errorf("Close after Fail with flushed output: got %v, wrote %d bytes", err, b.Len())
	}
}

// failWriter is an io.Writer whose writes fail with err.
type failWriter struct{ err error }

func (w failWriter) Write([]byte) (int, error) { return 0, w.err }

// TestTransformWriteError checks that a write error stops the Transformer
// once the buffered output is written, instead of at Close.
func TestTransformWriteError(t *testing.T) {
	errWrite := errors.New("write")
	const n = 4096
	in := strings.Repeat("<p>a</p>", n)
	x := NewTransformer(failWriter{errWrite}, strings.NewReader(in))
	tokens := 0
	for x.Next() {
		tokens++
	}
	if x.Err() != errWrite || tokens >= 3*n {
		t.Errorf("got %v after %d of %d tokens, want the write error before the end", x.Err(), tokens, 3*n)
	}
	if err := x.Close(); err != errWrite {
		t.Errorf("Close: got %v", err)
	}

	// releasing a hold writes the held output
	x = NewTransformer(failWriter{errWrite}, strings.NewReader(in))
	x.Next()
	h := x.Hold()
	for i := 0; i < 2*n && x.Next(); i++ {
	}
	if err := x.HoldRelease(h); err != errWrite {
		t.Errorf("HoldRelease: got %v", err)
	}
}

// TestTransformUntrackedLimit checks that element operations on untracked
// elements are limited to the maximum depth, with a sticky error past it.
func TestTransformUntrackedLimit(t *testing.T) {
	for _, tc := range []struct {
		nested int
		err    bool
	}{
		{4, false}, // 2 tracked, 2 untracked
		{5, true},
	} {
		in := strings.Repeat("<span>", tc.nested) + "x" + strings.Repeat("</span>", tc.nested)
		var b bytes.Buffer
		x := NewTransformer(&b, strings.NewReader(in))
		x.SetMaxDepth(2)
		var renameErr error
		for x.Next() {
			if x.Token().Kind() == StartTag {
				if err := x.Rename("b"); err != nil && renameErr == nil {
					renameErr = err
				}
			}
		}
		err := x.Close()
		if !tc.err {
			if err != nil || renameErr != nil {
				t.Errorf("%d nested: %v, %v", tc.nested, renameErr, err)
			} else if want := strings.Repeat("<b>", tc.nested) + "x" + strings.Repeat("</b>", tc.nested); b.String() != want {
				t.Errorf("%d nested: got %q, want %q", tc.nested, b.String(), want)
			}
			continue
		}
		if !errors.Is(renameErr, ErrUntrackedLimit) || !errors.Is(err, ErrUntrackedLimit) {
			t.Errorf("%d nested: got %v, %v, want ErrUntrackedLimit", tc.nested, renameErr, err)
		} else if want := "htmlstream: too many untracked elements (MaxDepth is 2) with pending operations"; err.Error() != want {
			t.Errorf("%d nested: got %q, want %q", tc.nested, err, want)
		}
	}
}
