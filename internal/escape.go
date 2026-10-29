package internal

import (
	"strings"
	"unicode/utf8"
)

// EscapeTable maps bytes to the index of their character reference in
// escapeRefs, or to 0 for bytes written as is.
type EscapeTable [256]uint8

// escapeRefs are the character references of escapeChars.
var escapeRefs = [...]string{"", "&amp;", "&lt;", "&gt;", "&quot;", "&#39;", "&#9;", "&#10;", "&#13;"}

const escapeChars = "\x00&<>\"'\t\n\r"

// escapeTable returns an EscapeTable escaping the bytes in chars, which must
// be in escapeChars.
func escapeTable(chars string) *EscapeTable {
	var t EscapeTable
	for i := 1; i < len(escapeChars); i++ {
		if strings.IndexByte(chars, escapeChars[i]) >= 0 {
			t[escapeChars[i]] = uint8(i)
		}
	}
	return &t
}

var (
	// EscapeText escapes &, <, > and carriage returns.
	EscapeText = escapeTable("&<>\r")
	// EscapeAttr escapes &, <, >, ", ', tabs, newlines and carriage returns.
	EscapeAttr = escapeTable("&<>\"'\t\n\r")
	// EscapeXML escapes &, <, >, ", tabs, newlines and carriage returns.
	EscapeXML = escapeTable("&<>\"\t\n\r")
)

// AppendEscape appends s to dst, writing the bytes escaped by t as
// character references.
func AppendEscape[S ~string | ~[]byte](dst []byte, s S, t *EscapeTable) []byte {
	start := 0
	for i := range len(s) {
		if r := t[s[i]]; r != 0 {
			dst = append(dst, s[start:i]...)
			dst = append(dst, escapeRefs[r]...)
			start = i + 1
		}
	}
	return append(dst, s[start:]...)
}

// DecodeRune is [utf8.DecodeRune] for strings and byte slices.
func DecodeRune[S ~string | ~[]byte](s S) (rune, int) {
	if len(s) != 0 && s[0] < utf8.RuneSelf {
		return rune(s[0]), 1
	}
	var b [utf8.UTFMax]byte
	n := copy(b[:], s)
	return utf8.DecodeRune(b[:n])
}
