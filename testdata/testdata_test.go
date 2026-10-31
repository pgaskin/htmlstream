package testdata

import (
	"strings"
	"testing"
)

func TestVectors(t *testing.T) {
	var all int
	for range Vectors(VectorOptions{}) {
		all++
	}
	var total int
	for _, s := range suites {
		var n int
		for _, desc := range Vectors(VectorOptions{Suites: []string{s.name}}) {
			if !strings.HasPrefix(desc, s.name+" ") {
				t.Errorf("%s: unexpected description %q", s.name, desc)
			}
			n++
		}
		if n == 0 {
			t.Errorf("%s: no vectors", s.name)
		}
		total += n
	}
	if total != all {
		t.Errorf("got %d vectors, expected %d from the suites", all, total)
	}

	var in, out int
	for _, desc := range Vectors(VectorOptions{Suites: []string{"polyglot"}, NoOutputs: true}) {
		if strings.HasSuffix(desc, " (out)") {
			out++
		}
		in++
	}
	if out != 0 {
		t.Errorf("NoOutputs: got %d outputs", out)
	}
	if in == 0 {
		t.Errorf("NoOutputs: no vectors")
	}

	var n int
	for range Vectors(VectorOptions{Suites: []string{"misc", "xml"}}) {
		if n++; n == 3 {
			break
		}
	}

	defer func() {
		if recover() == nil {
			t.Errorf("no panic for an unknown suite")
		}
	}()
	Vectors(VectorOptions{Suites: []string{"nonexistent"}})
}
