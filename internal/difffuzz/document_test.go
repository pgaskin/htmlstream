package difffuzz

import (
	"slices"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/pgaskin/htmlstream"
	"github.com/pgaskin/htmlstream/htmlutil"
)

// TestDocumentHead checks that htmlutil.Document puts the same
// elements in the head as an HTML parser.
func TestDocumentHead(t *testing.T) {
	for _, in := range []string{
		``,
		`<title>t</title><p>x`,
		`<html><head><title>t</title></head> <link rel=a><body>x`,
		`<!doctype html><html lang=en>  hello<title>x</title>`,
		`<head><script>x</script>text<meta charset=utf-8>`,
		`<meta charset=utf-8><style>p{}</style><base href=/><div>x</div><link rel=y>`,
		`<!-- c --><title>a</title><!-- d --><noscript>n</noscript><template><p>t</template><p>x`,
		`<head><svg></svg><title>t</title>`,
		`</br><title>t</title>`,
		`<head></head><script>s</script><body><style>s</style>`,
		`<html><head><title>a</title></head><body></body></html><meta name=x>`,
		`<head> &#32;x<title>t</title>`,
		`</head><noscript>n</noscript><title>t</title>`,
		`<title>t</title></head><meta name=x><noscript><title>t</title>`,
		`<noscript></head></noscript></head><title>t</title>`,
		`<templAte A=  `,
		`<title>t</title><templAte A=  `,
	} {
		want := xnetHead(t, in)
		got := documentHead(t, in)
		if !slices.Equal(got, want) {
			t.Errorf("%q: head elements:\n got %q\nwant %q", in, got, want)
		}
	}
}

// xnetHead returns the names of the children of the head element.
func xnetHead(t *testing.T, in string) []string {
	doc, err := html.Parse(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for n := range doc.Descendants() {
		if n.Type == html.ElementNode && n.Data == "head" {
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == html.ElementNode {
					names = append(names, c.Data)
				}
			}
			break
		}
	}
	return names
}

// documentHead returns the names of the start tags which Document places
// directly in the head, without those cut off by the end of the input, which
// it ignores.
func documentHead(t *testing.T, in string) []string {
	r := htmlstream.NewReader(strings.NewReader(in))
	var d htmlutil.Document
	var names []string
	var nested htmlutil.Stack[bool]
	for r.Next() {
		tok := r.Token()
		d.Update(tok)
		switch tok.Kind() {
		case htmlstream.StartTag:
			s := d.Part()
			if (s == htmlutil.InHead || s == htmlutil.AfterHead) && nested.Len() == 0 && !tok.Is("head") && !tok.Is("html") && !tok.Incomplete() {
				names = append(names, string(tok.AppendLocalName(nil)))
				nested.Push(tok, true)
			}
		case htmlstream.EndTag:
			nested.Pop(tok)
		}
	}
	if err := r.Err(); err != nil {
		t.Fatal(err)
	}
	return names
}
