// Package htmlutil implements utility functions and wrappers for working with
// HTML content, including some DOM-like helpers for getting node text,
// sniffing character encoding, parsing attributes, and handling URLs.
//
// The helpers work on the tokens of an [htmlstream.Reader] or
// [htmlstream.Transformer] as they are read, without building a tree.
//
// Element categories classify HTML elements (in the XHTML namespace) by
// their start or end tags, open elements, or names (see [NamedElement]):
// [IsVoid], [IsHeadContent], [IsBlock], [IsTextBreak], [IsHidden],
// [IsHeading], [IsPreformatted], [IsFormatting] and [IsSpecial].
//
// [Stack] keeps a value for open elements, pushed at their start tags and
// popped at their end tags, explicit or implied. [Document] tracks where the
// html, head and body elements start and end, including the ones HTML
// parsers imply, and whether the document is in quirks mode.
//
// [InnerText] reads the text of an element approximating the DOM's
// innerText, and [TextContent] like its textContent, and [TextBuilder] and
// [CollapseSpace] collapse whitespace like rendered text. [ScriptType],
// [IsJavaScriptType] and [IsJSONType] classify script elements, and
// [CutCDATAWrapper] removes the CDATA section wrapper of script and style
// contents.
// [MetaCharset] returns the character encoding a meta element declares, and
// [PrescanCharset] finds the one declared at the start of a document, before
// reading it.
//
// [URLAttrs] returns the attributes of a start tag which contain URLs, and
// [ParseSrcset] the URLs of a srcset attribute. [BaseURL] tracks the base
// URL set by a base element, [CleanURL] removes the characters browsers
// ignore in URLs, and [ResolveURL] resolves URLs with net/url.
//
// The helpers based on a specification cite it, and say where they differ
// from it.
package htmlutil

import (
	"iter"

	"github.com/pgaskin/htmlstream"
)

// Stack keeps a value for some of the open elements, in sync with a Reader.
// Values are pushed on the start tag of an element, and popped on the
// element's end tag (explicit or implied), which is matched using
// [htmlstream.Token.StartPos], so stray end tags and the end tags of other
// elements are ignored. The zero value is an empty Stack ready to use.
//
// Pop only pops the innermost value, so every end tag must be passed to it:
// a value whose end tag isn't passed stays at the top, and the values below
// it aren't popped either. After [htmlstream.Reader.Skip], and after the
// iteration of [htmlstream.Reader.Children], the current token is the end
// tag, which must be passed to Pop too. [htmlstream.Reader.Find] skips end
// tags, so Stack can't be used with it.
type Stack[T any] struct {
	s []stackEntry[T]
}

type stackEntry[T any] struct {
	pos htmlstream.Pos
	v   T
}

// Push pushes v for the element opened by the start tag tok. It returns false
// (and does nothing) if tok doesn't open an element, or opens an untracked
// element (see [htmlstream.Token.Untracked]), whose end tag can't be matched.
func (s *Stack[T]) Push(tok *htmlstream.Token, v T) bool {
	if tok.Kind() != htmlstream.StartTag || !tok.Opens() || tok.Untracked() {
		return false
	}
	s.s = append(s.s, stackEntry[T]{tok.Pos(), v})
	return true
}

// Pop pops the value for the element closed by the end tag tok, if it is the
// innermost element with a value.
func (s *Stack[T]) Pop(tok *htmlstream.Token) (T, bool) {
	var zero T
	n := len(s.s)
	if n == 0 || tok.Kind() != htmlstream.EndTag || tok.StartPos() != s.s[n-1].pos {
		return zero, false
	}
	v := s.s[n-1].v
	s.s[n-1] = stackEntry[T]{}
	s.s = s.s[:n-1]
	return v, true
}

// Closes reports whether the end tag tok closes the innermost element with a
// value.
func (s *Stack[T]) Closes(tok *htmlstream.Token) bool {
	n := len(s.s)
	return n != 0 && tok.Kind() == htmlstream.EndTag && tok.StartPos() == s.s[n-1].pos
}

// Top returns the value of the innermost element with a value.
func (s *Stack[T]) Top() (*T, bool) {
	if len(s.s) == 0 {
		return nil, false
	}
	return &s.s[len(s.s)-1].v, true
}

// Len returns the number of elements with a value.
func (s *Stack[T]) Len() int { return len(s.s) }

// At returns the value at index i, where 0 is the outermost element. It
// panics if i is out of range [0, Len()).
func (s *Stack[T]) At(i int) *T { return &s.s[i].v }

// All returns an iterator over the indexes (see [Stack.At]) and values,
// innermost first, like [htmlstream.Reader.Stack].
func (s *Stack[T]) All() iter.Seq2[int, *T] {
	return func(yield func(int, *T) bool) {
		for i := len(s.s) - 1; i >= 0; i-- {
			if !yield(i, &s.s[i].v) {
				return
			}
		}
	}
}
