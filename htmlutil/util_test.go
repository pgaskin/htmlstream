package htmlutil

import (
	"strings"
	"testing"

	"github.com/pgaskin/htmlstream"
)

func TestStack(t *testing.T) {
	r := htmlstream.NewReader(strings.NewReader(`<ul><li>a<li>b <i>c</ul></i><p>d`))
	var s Stack[string]
	var got []string
	for r.Next() {
		tok := r.Token()
		switch tok.Kind() {
		case htmlstream.StartTag:
			if tok.Is("li") || tok.Is("ul") {
				s.Push(tok, string(tok.Name())+"@"+strings.Repeat("x", int(tok.Pos())%3))
			}
		case htmlstream.EndTag:
			if v, ok := s.Pop(tok); ok {
				got = append(got, "/"+v)
			}
		case htmlstream.Text:
			if v, ok := s.Top(); ok {
				got = append(got, string(tok.Data())+" in "+*v)
			}
		}
	}
	if s.Len() != 0 {
		t.Errorf("stack not empty: %d", s.Len())
	}
	if len(got) != 6 || got[1] != "/li@x" || !strings.HasPrefix(got[5], "/ul") {
		t.Errorf("got %q", got)
	}
}

func TestStackUntracked(t *testing.T) {
	r := htmlstream.NewReader(strings.NewReader(`<div><div><div><div>x</div></div></div></div>`))
	r.SetMaxDepth(2)
	var s Stack[int]
	var maxLen int
	for r.Next() {
		tok := r.Token()
		switch tok.Kind() {
		case htmlstream.StartTag:
			s.Push(tok, int(tok.Pos()))
		case htmlstream.EndTag:
			s.Pop(tok)
		}
		maxLen = max(maxLen, s.Len())
	}
	if s.Len() != 0 || maxLen != 2 {
		t.Errorf("len %d, max %d", s.Len(), maxLen)
	}
}

func TestStackOrder(t *testing.T) {
	r := htmlstream.NewReader(strings.NewReader(`<a><b><c>`))
	var s Stack[string]
	for r.Next() {
		if tok := r.Token(); tok.Kind() == htmlstream.StartTag {
			s.Push(tok, string(tok.Name()))
		}
		if s.Len() == 3 {
			break
		}
	}
	var all []string
	for i, v := range s.All() {
		all = append(all, *v+string(rune('0'+i)))
	}
	if strings.Join(all, " ") != "c2 b1 a0" || *s.At(1) != "b" {
		t.Errorf("All %q", all)
	}
}
