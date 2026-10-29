package ascii

import "testing"

func TestASCII(t *testing.T) {
	for c, want := range map[byte]bool{' ': true, '\t': true, '\n': true, '\f': true, '\r': true, '\v': false, 'a': false, 0xA0: false} {
		if IsSpace(c) != want {
			t.Errorf("IsSpace(%q) != %v", c, want)
		}
	}
	if !IsBlank("") || !IsBlank(" \t\r\n\f") || IsBlank(" x ") || IsBlank([]byte("\v")) {
		t.Errorf("IsBlank")
	}
	if got := TrimSpace(" \t\va\u00a0 \r\n"); got != "\va\u00a0" || string(TrimSpace([]byte(" "))) != "" {
		t.Errorf("TrimSpace: %q", got)
	}
	if !EqualFold("Content-Type", []byte("content-TYPE")) || EqualFold("\u017f", "s") || EqualFold("K", "\u212a") || EqualFold("a", "ab") {
		t.Errorf("EqualFold")
	}
	if got := ToLower("SVG ForeignObject \u00c9"); got != "svg foreignobject \u00c9" {
		t.Errorf("ToLower: %q", got)
	}
	if in := []byte("svg"); &ToLower(in)[0] != &in[0] {
		t.Errorf("ToLower: lowercase []byte copied")
	}
	if in := []byte("SVG\u00c9"); string(ToLower(in)) != "svg\u00c9" || string(in) != "SVG\u00c9" {
		t.Errorf("ToLower: %q", in)
	}
	if got := string(AppendLower([]byte("x:"), "AbC")); got != "x:abc" {
		t.Errorf("AppendLower: %q", got)
	}
	if ToLowerByte('Q') != 'q' || ToLowerByte('@') != '@' || ToLowerByte('[') != '[' {
		t.Errorf("ToLowerByte")
	}
}
