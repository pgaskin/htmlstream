// Package internal contains the data tables and helpers shared by
// htmlstream and htmlutil.
package internal

import "unicode/utf8"

//go:generate go run entity_generate.go

// LookupEntity returns the value of the named character reference name
// (without the leading '&', including the ';' if any).
func LookupEntity(name []byte) (string, bool) {
	lo, hi := 0, len(entities)
	for lo < hi {
		// comparisons with string(name) don't allocate
		m := int(uint(lo+hi) >> 1)
		switch e := entities[m].name; {
		case e < string(name):
			lo = m + 1
		case e > string(name):
			hi = m
		default:
			return entities[m].value, true
		}
	}
	return "", false
}

// c1Replacements maps numeric character references in 0x80-0x9F to the
// windows-1252 characters, as required by HTML5. Zero entries are kept as-is.
var c1Replacements = [32]rune{
	0x20AC, 0, 0x201A, 0x0192, 0x201E, 0x2026, 0x2020, 0x2021,
	0x02C6, 0x2030, 0x0160, 0x2039, 0x0152, 0, 0x017D, 0,
	0, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014,
	0x02DC, 0x2122, 0x0161, 0x203A, 0x0153, 0, 0x017E, 0x0178,
}

// DecodeRef decodes the character reference at the start of s, which must
// begin with '&'. It returns the decoded value appended to dst and the number
// of bytes of s consumed, or n == 0 if s does not start with a reference that
// should be decoded.
//
// In attribute values (attr), a named reference without a terminating ';'
// followed by '=' or an alphanumeric character is not decoded. In XML mode
// (xml), named and numeric references require a terminating ';', and the
// windows-1252 mapping of C1 references is not applied.
func DecodeRef(dst, s []byte, attr, xml bool) (_ []byte, n int) {
	if len(s) < 2 || s[0] != '&' {
		return dst, 0
	}
	if s[1] == '#' {
		return decodeNumericRef(dst, s, xml)
	}
	i := 1
	for i < len(s) && i <= EntityMaxName && isAlnum(s[i]) {
		i++
	}
	if i == 1 {
		return dst, 0
	}
	if i < len(s) && s[i] == ';' {
		if v, ok := LookupEntity(s[1 : i+1]); ok {
			return append(dst, v...), i + 1
		}
	}
	if xml {
		return dst, 0
	}
	// Longest legacy (no ';') name which is a prefix of the alphanumeric run.
	for j := min(i, 1+EntityMaxLegacy); j > 2; j-- {
		v, ok := LookupEntity(s[1:j])
		if !ok {
			continue
		}
		if attr && j < len(s) && (s[j] == '=' || isAlnum(s[j])) {
			return dst, 0
		}
		return append(dst, v...), j
	}
	return dst, 0
}

func decodeNumericRef(dst, s []byte, xml bool) (_ []byte, n int) {
	i, base := 2, 10
	if i < len(s) && (s[i] == 'x' || s[i] == 'X') {
		i, base = 3, 16
	}
	var c rune
	digits := i
	for ; i < len(s); i++ {
		var d rune
		switch b := s[i]; {
		case '0' <= b && b <= '9':
			d = rune(b - '0')
		case base == 16 && 'a' <= b && b <= 'f':
			d = rune(b-'a') + 10
		case base == 16 && 'A' <= b && b <= 'F':
			d = rune(b-'A') + 10
		default:
			goto done
		}
		if c <= utf8.MaxRune {
			c = c*rune(base) + d
		}
	}
done:
	if i == digits {
		return dst, 0
	}
	if i < len(s) && s[i] == ';' {
		i++
	} else if xml {
		return dst, 0
	}
	switch {
	case c == 0, c > utf8.MaxRune, 0xD800 <= c && c <= 0xDFFF:
		c = utf8.RuneError
	case !xml && 0x80 <= c && c <= 0x9F:
		if r := c1Replacements[c-0x80]; r != 0 {
			c = r
		}
	}
	return utf8.AppendRune(dst, c), i
}

func isAlnum(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9'
}
