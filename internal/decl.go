package internal

import (
	"bytes"
	"strconv"
	"strings"
)

// Decl is an XML declaration or text declaration (see xmlutil.Decl).
type Decl struct {
	Version    string
	Encoding   string
	Standalone string
}

// DeclError is a syntax error in a declaration (see xmlutil.DeclError).
type DeclError struct {
	Offset int
	Msg    string
}

// ParseDecl parses the declaration at the start of b like xmlutil.ParseDecl.
// ok is false if b doesn't start with a declaration.
func ParseDecl(b []byte) (d Decl, n int, err *DeclError, ok bool) {
	if !bytes.HasPrefix(b, []byte("<?xml")) || len(b) > 5 && !isSpace(b[5]) && (b[5] != '?' || len(b) > 6 && b[6] != '>') {
		return Decl{}, 0, nil, false
	}
	fail := func(off int, msg string) {
		if err == nil {
			err = &DeclError{off, msg}
		}
	}
	i := len("<?xml")
	last := 0 // the position of the last pseudo-attribute read in the syntax: 1 for version, 2 for encoding, 3 for standalone
	for {
		ws := i
		for i < len(b) && isSpace(b[i]) {
			i++
		}
		if i == len(b) || i == len(b)-1 && b[i] == '?' {
			fail(i, "declaration is cut off")
			return d, 0, err, true
		}
		if bytes.HasPrefix(b[i:], []byte("?>")) {
			n = i + 2
			break
		}
		// name
		start := i
		for i < len(b) && !isSpace(b[i]) && strings.IndexByte("=?>\"'", b[i]) < 0 {
			i++
		}
		name := string(b[start:i])
		if name == "" {
			fail(i, "unexpected "+strconv.QuoteRune(rune(b[i])))
			i++
			continue
		}
		if ws == start {
			fail(start, "missing whitespace before "+strconv.Quote(name))
		}
		// =
		for i < len(b) && isSpace(b[i]) {
			i++
		}
		if i == len(b) || b[i] != '=' {
			fail(i, "missing \"=\" after "+strconv.Quote(name))
			continue
		}
		i++
		for i < len(b) && isSpace(b[i]) {
			i++
		}
		// value
		var value string
		switch {
		case i == len(b):
			fail(i, "declaration is cut off")
			return d, 0, err, true
		case b[i] == '"' || b[i] == '\'':
			end := bytes.IndexByte(b[i+1:], b[i])
			if end < 0 {
				fail(len(b), "declaration is cut off")
				return d, 0, err, true
			}
			value = string(b[i+1 : i+1+end])
			i += end + 2
		default:
			fail(i, "value of "+strconv.Quote(name)+" isn't quoted")
			start := i
			for i < len(b) && !isSpace(b[i]) && !bytes.HasPrefix(b[i:], []byte("?>")) {
				i++
			}
			value = string(b[start:i])
		}
		var pos int
		var dst *string
		var valid bool
		switch name {
		case "version":
			pos, dst, valid = 1, &d.Version, validVersion(value)
		case "encoding":
			pos, dst, valid = 2, &d.Encoding, validEncName(value)
		case "standalone":
			pos, dst, valid = 3, &d.Standalone, value == "yes" || value == "no"
		default:
			fail(start, "unknown pseudo-attribute "+strconv.Quote(name))
			continue
		}
		switch {
		case pos <= last:
			fail(start, "pseudo-attribute "+strconv.Quote(name)+" is out of order or repeated")
		case !valid:
			fail(start, "invalid "+name+" "+strconv.Quote(value))
		}
		if *dst == "" {
			*dst = value
		}
		last = max(last, pos)
	}
	switch {
	case d.Version == "" && d.Encoding == "":
		fail(n-2, "missing version")
	case d.Version == "" && d.Standalone != "":
		fail(n-2, "standalone without a version")
	}
	return d, n, err, true
}

// isSpace reports whether c is XML whitespace (the [S] production).
//
// [S]: https://www.w3.org/TR/xml/#NT-S
func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// validVersion reports whether s matches the VersionNum production.
func validVersion(s string) bool {
	if len(s) < 3 || s[:2] != "1." {
		return false
	}
	for _, c := range []byte(s[2:]) {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// validEncName reports whether s matches the EncName production.
func validEncName(s string) bool {
	if s == "" || !isASCIIAlpha(s[0]) {
		return false
	}
	for _, c := range []byte(s[1:]) {
		if !isASCIIAlpha(c) && (c < '0' || c > '9') && c != '.' && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

func isASCIIAlpha(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
}
