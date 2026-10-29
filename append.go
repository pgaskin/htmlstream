package htmlstream

import "github.com/pgaskin/htmlstream/internal"

// AppendEscapeText appends s to dst, escaping it for use as text in HTML or
// XML: &, < and > are written as character references, and so are carriage
// returns, which HTML parsers ([preprocessing the input stream]) and XML
// parsers ([end-of-line handling]) convert to newlines. It isn't for the
// contents of raw text elements like script and style, where HTML parsers
// don't decode character references. Characters XML doesn't allow aren't
// replaced (see [AppendXMLChars]). NEL and U+2028, which are line ends in
// XML 1.1 documents, aren't escaped (see Modes in the package
// documentation).
//
// [preprocessing the input stream]: https://html.spec.whatwg.org/multipage/parsing.html#preprocessing-the-input-stream
// [end-of-line handling]: https://www.w3.org/TR/2006/REC-xml-20060816/#sec-line-ends
func AppendEscapeText[S ~string | ~[]byte](dst []byte, s S) []byte {
	return internal.AppendEscape(dst, s, internal.EscapeText)
}

// AppendEscapeAttr appends s to dst, escaping it for use as a quoted attribute
// value in HTML or XML: &, <, >, " and ' are written as character
// references, and so are tabs, newlines and carriage returns, which XML
// parsers would change to spaces ([attribute-value normalization]).
// Characters XML doesn't allow aren't replaced (see [AppendXMLChars]). NEL
// and U+2028, which are line ends in XML 1.1 documents, aren't escaped (see
// Modes in the package documentation).
//
// [attribute-value normalization]: https://www.w3.org/TR/2006/REC-xml-20060816/#AVNormalize
func AppendEscapeAttr[S ~string | ~[]byte](dst []byte, s S) []byte {
	return internal.AppendEscape(dst, s, internal.EscapeAttr)
}

// AppendStartTag appends a start tag named name to dst, with attributes given
// as name and value pairs. Values are escaped (see [AppendEscapeAttr]);
// names are written as-is and aren't checked (see [ValidXMLName]). If
// selfClosing, it is written as <name />, which HTML parsers ignore on HTML
// elements other than void elements (e.g., <div /> opens a div; see
// [Transformer.Empty]). It panics if attrs has an odd number of elements.
func AppendStartTag(dst []byte, name string, selfClosing bool, attrs ...string) []byte {
	if len(attrs)%2 != 0 {
		panic("htmlstream: odd number of attribute arguments")
	}
	dst = append(dst, '<')
	dst = append(dst, name...)
	for i := 0; i < len(attrs); i += 2 {
		dst = append(dst, ' ')
		dst = append(dst, attrs[i]...)
		dst = append(dst, `="`...)
		dst = AppendEscapeAttr(dst, attrs[i+1])
		dst = append(dst, '"')
	}
	if selfClosing {
		dst = append(dst, " />"...)
	} else {
		dst = append(dst, '>')
	}
	return dst
}

// AppendEndTag appends an end tag named name to dst.
func AppendEndTag(dst []byte, name string) []byte {
	dst = append(dst, "</"...)
	dst = append(dst, name...)
	return append(dst, '>')
}

// AppendElement appends an element named name to dst, with the escaped text
// and the attributes given as name and value pairs. It panics if attrs has an
// odd number of elements. For an element without content written as
// <name />, use [AppendStartTag] with selfClosing.
//
// The text is escaped with [AppendEscapeText], so name must not be a raw
// text element like script or style, where HTML parsers don't decode
// character references, or a void element like br, which has no end tag
// (HTML parsers read </br> as another br).
func AppendElement[S ~string | ~[]byte](dst []byte, name string, text S, attrs ...string) []byte {
	dst = AppendStartTag(dst, name, false, attrs...)
	dst = AppendEscapeText(dst, text)
	return AppendEndTag(dst, name)
}

// AppendComment appends a comment containing text to dst, changing the text so
// the comment is valid in both HTML and XML: a space is inserted where the
// text starts with ">" or "->", between two hyphens, and after a hyphen at
// the end. The text then follows the comment syntax of the [HTML Standard]
// (it doesn't start with ">" or "->", contain "<!--", "-->" or "--!>", or
// end with "<!-") and the Comment production of [XML 1.0] (no "--", and no
// "-" at the end). Characters XML doesn't allow aren't replaced (see
// [AppendXMLChars]).
//
// [HTML Standard]: https://html.spec.whatwg.org/multipage/syntax.html#comments
// [XML 1.0]: https://www.w3.org/TR/2006/REC-xml-20060816/#NT-Comment
func AppendComment[S ~string | ~[]byte](dst []byte, text S) []byte {
	dst = append(dst, "<!--"...)
	n := len(text)
	if n != 0 && text[0] == '>' || n > 1 && text[0] == '-' && text[1] == '>' {
		dst = append(dst, ' ')
	}
	for i := range n {
		c := text[i]
		dst = append(dst, c)
		if c == '-' && i+1 < n && text[i+1] == '-' {
			dst = append(dst, ' ')
		}
	}
	if n != 0 && text[n-1] == '-' {
		dst = append(dst, ' ')
	}
	return append(dst, "-->"...)
}

// AppendCDATA appends a [CDATA section] containing text to dst. Where the
// text contains "]]>", which would end the section, the section is ended
// after "]]" and another one started before ">", so XML parsers read the text
// unchanged, except for carriage returns, which they convert to newlines
// everywhere ([end-of-line handling]) and which can't be escaped in a CDATA
// section. Characters XML doesn't allow aren't replaced (see
// [AppendXMLChars]).
//
// The output may not be valid in HTML documents: HTML parsers only read CDATA
// sections in SVG and MathML content (elsewhere, they read a bogus comment
// ending at the first ">").
//
// [CDATA section]: https://www.w3.org/TR/xml/#sec-cdata-sect
// [end-of-line handling]: https://www.w3.org/TR/xml/#sec-line-ends
func AppendCDATA[S ~string | ~[]byte](dst []byte, text S) []byte {
	dst = append(dst, "<![CDATA["...)
	for i := 0; i+3 <= len(text); i++ {
		if text[i] == ']' && text[i+1] == ']' && text[i+2] == '>' {
			dst = append(dst, text[:i+2]...)
			dst = append(dst, "]]><![CDATA["...)
			text = text[i+2:]
			i = -1
		}
	}
	dst = append(dst, text...)
	return append(dst, "]]>"...)
}

// AppendProcInst appends a [processing instruction] with the target and data
// to dst, as <?target data?>, or <?target?> if data is empty. A space is
// inserted between "?" and ">" where the data contains "?>", which would end
// the processing instruction. XML parsers skip the whitespace after the
// target, so data starting with whitespace isn't read unchanged. The target
// is written as-is and isn't checked: it must be a name (see [ValidXMLName])
// other than "xml" in any case, which is reserved for the XML declaration.
// Characters XML doesn't allow aren't replaced (see [AppendXMLChars]).
//
// The output may not be valid in HTML documents: HTML parsers read a
// processing instruction as a bogus comment ending at the first ">" (see
// [Token.Bogus]).
//
// [processing instruction]: https://www.w3.org/TR/xml/#sec-pi
func AppendProcInst[S ~string | ~[]byte](dst []byte, target string, data S) []byte {
	dst = append(dst, "<?"...)
	dst = append(dst, target...)
	if len(data) != 0 {
		dst = append(dst, ' ')
		for i := range len(data) {
			dst = append(dst, data[i])
			if data[i] == '?' && i+1 < len(data) && data[i+1] == '>' {
				dst = append(dst, ' ')
			}
		}
	}
	return append(dst, "?>"...)
}

// AppendXMLChars appends s to dst, replacing the characters [XML 1.0] doesn't
// allow (control characters other than tab, newline and carriage return,
// U+FFFE, U+FFFF, and invalid UTF-8, including encoded surrogates) with
// U+FFFD, and form feeds, which are whitespace in HTML, with spaces. It
// reports whether anything was replaced. XML 1.1 only allows U+007F to
// U+009F other than NEL as character references, which AppendXMLChars
// doesn't write, unlike a [Transformer] in the attribute values it sets (see
// Modes in the package documentation).
//
// [XML 1.0]: https://www.w3.org/TR/2006/REC-xml-20060816/#NT-Char
func AppendXMLChars[S ~string | ~[]byte](dst []byte, s S) ([]byte, bool) {
	changed := false
	for i := 0; i < len(s); {
		c, n := internal.DecodeRune(s[i:])
		switch {
		case c == '\f':
			dst = append(dst, ' ')
			changed = true
		case !xmlChar(c, n):
			dst = append(dst, "\uFFFD"...)
			changed = true
		default:
			dst = append(dst, s[i:i+n]...)
		}
		i += n
	}
	return dst, changed
}
