// Package ascii contains ASCII string/bytes helpers.
//
// They are the helpers HTML parsers use: ASCII whitespace, and case folding of
// ASCII letters only (unlike strings.EqualFold and strings.ToLower), as the
// [Infra Standard] defines them. They work on bytes, which gives the same
// results as on the code points of UTF-8 text, since the bytes of multi-byte
// UTF-8 sequences aren't ASCII.
//
// [Infra Standard]: https://infra.spec.whatwg.org/#strings
package ascii

// IsSpace reports whether c is [ASCII whitespace] (space, tab, newline, form
// feed or carriage return), which HTML uses for whitespace.
//
// [ASCII whitespace]: https://infra.spec.whatwg.org/#ascii-whitespace
func IsSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\f' || c == '\r'
}

// IsBlank reports whether s is empty or contains only ASCII whitespace.
func IsBlank[S ~string | ~[]byte](s S) bool {
	for i := range len(s) {
		if !IsSpace(s[i]) {
			return false
		}
	}
	return true
}

// TrimSpace returns s without leading and trailing ASCII whitespace, like
// the Infra Standard's [strip leading and trailing ASCII whitespace]. Unlike
// strings.TrimSpace, it doesn't remove other whitespace (such as vertical
// tabs or U+00A0).
//
// [strip leading and trailing ASCII whitespace]: https://infra.spec.whatwg.org/#strip-leading-and-trailing-ascii-whitespace
func TrimSpace[S ~string | ~[]byte](s S) S {
	for len(s) != 0 && IsSpace(s[0]) {
		s = s[1:]
	}
	for len(s) != 0 && IsSpace(s[len(s)-1]) {
		s = s[:len(s)-1]
	}
	return s
}

// ToLowerByte returns c lowercased if it is an ASCII letter, or c otherwise.
func ToLowerByte(c byte) byte {
	if 'A' <= c && c <= 'Z' {
		c += 'a' - 'A'
	}
	return c
}

// EqualFold reports whether a and b are equal with ASCII letters compared
// case-insensitively (an [ASCII case-insensitive] match), as HTML parsers
// compare names and keywords (strings.EqualFold also folds other letters,
// such as U+017F to s).
//
// [ASCII case-insensitive]: https://infra.spec.whatwg.org/#ascii-case-insensitive
func EqualFold[A, B ~string | ~[]byte](a A, b B) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range len(a) {
		if c, d := a[i], b[i]; c != d && ToLowerByte(c) != ToLowerByte(d) {
			return false
		}
	}
	return true
}

// AppendLower appends s to dst with ASCII letters lowercased.
func AppendLower[S ~string | ~[]byte](dst []byte, s S) []byte {
	for i := range len(s) {
		dst = append(dst, ToLowerByte(s[i]))
	}
	return dst
}

// ToLower returns s with ASCII letters lowercased (the Infra Standard's
// [ASCII lowercase]), as HTML parsers lowercase names (other letters are
// kept). It only allocates if s has uppercase ASCII letters: otherwise, it
// returns s, which for a []byte is the same slice. It doesn't modify s.
//
// [ASCII lowercase]: https://infra.spec.whatwg.org/#ascii-lowercase
func ToLower[S ~string | ~[]byte](s S) S {
	for i := range len(s) {
		if c := s[i]; 'A' <= c && c <= 'Z' {
			return S(AppendLower(append(make([]byte, 0, len(s)), s[:i]...), s[i:]))
		}
	}
	return s
}
