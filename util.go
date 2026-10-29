package htmlstream

import (
	"encoding/xml"
	"strings"
	"unicode/utf8"

	"github.com/pgaskin/htmlstream/internal"
)

func init() {
	internal.TokenMode = func(tok any) (uint8, bool) {
		t := tok.(*Token)
		mode := t.mode
		if t.flags&tokUniversal != 0 {
			mode = Universal
		}
		return uint8(mode), t.flags&tokXMLSyntax != 0
	}
}

// ValidXMLName reports whether name is a valid XML name with namespaces (a
// [QName] in Namespaces in XML 1.0): at most one colon, which isn't at the
// start or end, and the prefix and the local name each start with a letter
// or underscore. Non-ASCII characters must be name characters under the
// [XML 1.0 Fourth Edition] rules, which encoding/xml implements and all XML
// 1.0 parsers accept (the Fifth Edition allows more). Only the syntax is
// checked: names with the reserved xml and xmlns prefixes are valid.
//
// [QName]: https://www.w3.org/TR/2006/REC-xml-names-20060816/#NT-QName
// [XML 1.0 Fourth Edition]: https://www.w3.org/TR/2006/REC-xml-20060816/#NT-Name
func ValidXMLName(name string) bool {
	if name == "" || strings.Count(name, ":") > 1 || name[0] == ':' || name[len(name)-1] == ':' {
		return false
	}
	ascii := true
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', c == '_', c == ':':
		case i > 0 && name[i-1] != ':' && ('0' <= c && c <= '9' || c == '-' || c == '.'):
		case c >= 0x80:
			ascii = false
		default:
			return false
		}
	}
	if ascii {
		return true
	}
	if !utf8.ValidString(name) {
		return false
	}
	// the prefix and the local name must both be names
	for part := range strings.SplitSeq(name, ":") {
		if _, err := xml.NewDecoder(strings.NewReader("<" + part + "/>")).Token(); err != nil {
			return false
		}
	}
	return true
}

// ValidXMLChars reports whether s is valid UTF-8 and only contains the
// characters allowed in [XML 1.0] documents (the Char production; see
// [AppendXMLChars]). XML 1.1 only allows U+007F to U+009F other than NEL as
// character references (see Modes in the package documentation).
//
// [XML 1.0]: https://www.w3.org/TR/2006/REC-xml-20060816/#NT-Char
func ValidXMLChars[S ~string | ~[]byte](s S) bool {
	for i := 0; i < len(s); {
		c, n := internal.DecodeRune(s[i:])
		if !xmlChar(c, n) {
			return false
		}
		i += n
	}
	return true
}

func xmlChar(c rune, n int) bool {
	switch {
	case c == utf8.RuneError && n == 1:
		return false
	case c == '\t', c == '\n', c == '\r':
		return true
	case c < 0x20, c == 0xFFFE, c == 0xFFFF:
		return false
	}
	return true
}

// AdjustSVGTagName returns the case HTML parsers give the SVG element with
// the lowercase name lname (e.g., "foreignobject" becomes "foreignObject"),
// or lname if it isn't adjusted. The names are those of the table in the
// rules for parsing tokens in [foreign content] of the HTML Standard.
//
// [foreign content]: https://html.spec.whatwg.org/multipage/parsing.html#parsing-main-inforeign
func AdjustSVGTagName(lname string) string {
	if n, ok := internal.SVGTagNames[lname]; ok {
		return n
	}
	return lname
}

// AdjustSVGAttrName returns the case HTML parsers give the SVG attribute with
// the lowercase name lname (e.g., "viewbox" becomes "viewBox"), or lname if
// it isn't adjusted, as in the steps to [adjust SVG attributes] of the HTML
// Standard.
//
// [adjust SVG attributes]: https://html.spec.whatwg.org/multipage/parsing.html#adjust-svg-attributes
func AdjustSVGAttrName(lname string) string {
	if n, ok := internal.SVGAttrNames[lname]; ok {
		return n
	}
	return lname
}

// AdjustMathMLAttrName returns the case HTML parsers give the MathML
// attribute with the lowercase name lname, or lname if it isn't adjusted:
// definitionurl becomes definitionURL, as in the steps to
// [adjust MathML attributes] of the HTML Standard.
//
// [adjust MathML attributes]: https://html.spec.whatwg.org/multipage/parsing.html#adjust-mathml-attributes
func AdjustMathMLAttrName(lname string) string {
	if n, ok := internal.MathMLAttrNames[lname]; ok {
		return n
	}
	return lname
}
