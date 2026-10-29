package htmlutil

import (
	"bytes"

	"github.com/pgaskin/htmlstream"
	"github.com/pgaskin/htmlstream/ascii"
	"github.com/pgaskin/htmlstream/internal"
)

// MetaCharset returns the name of the character encoding declared by a meta
// element, as HTML parsers read the start tag in the [in head] insertion
// mode: the encoding of the charset attribute if its value is an encoding
// label (see the [Encoding Standard]), or else, if the http-equiv attribute
// is an ASCII case-insensitive match for Content-Type, the encoding the
// [algorithm for extracting a character encoding from a meta element] gets
// from the content attribute. It returns false for other tokens, and for
// meta elements without a declaration it recognizes.
//
// The name is the one the Encoding Standard gives the encoding (e.g.,
// "UTF-8", or "windows-1252" for the label "latin1"), which
// golang.org/x/text/encoding/htmlindex.Get accepts. Like the steps to
// [change the encoding], which HTML parsers run with the declaration, it
// gives UTF-8 for UTF-16BE and UTF-16LE, and windows-1252 for
// x-user-defined. Some labels (e.g., "iso-2022-kr") give the replacement
// encoding, which decodes any non-empty input to a single U+FFFD.
//
// HTML parsers use the first declaration they read, unless the encoding was
// already certain (e.g., from a byte order mark or the Content-Type header).
// Before parsing, browsers look for it with the prescan (see
// [PrescanCharset]).
//
// [in head]: https://html.spec.whatwg.org/multipage/parsing.html#parsing-main-inhead
// [Encoding Standard]: https://encoding.spec.whatwg.org/#concept-encoding-get
// [algorithm for extracting a character encoding from a meta element]: https://html.spec.whatwg.org/multipage/urls-and-fetching.html#algorithm-for-extracting-a-character-encoding-from-a-meta-element
// [change the encoding]: https://html.spec.whatwg.org/multipage/parsing.html#change-the-encoding
func MetaCharset(tok *htmlstream.Token) (name string, ok bool) {
	if tok.Kind() != htmlstream.StartTag || tok.NS() != htmlstream.NamespaceXHTML || !isLocal(tok, "meta") {
		return "", false
	}
	if a, ok := tok.Attr("charset"); ok {
		if name, ok := internal.LookupEncoding(a.Value()); ok {
			return declaredEncoding(name), true
		}
	}
	if a, ok := tok.Attr("http-equiv"); !ok || !ascii.EqualFold(a.Value(), "content-type") {
		return "", false
	}
	c, ok := tok.Attr("content")
	if !ok {
		return "", false
	}
	label, ok := contentCharset(c.Value())
	if !ok {
		return "", false
	}
	name, ok = internal.LookupEncoding(label)
	if !ok {
		return "", false
	}
	return declaredEncoding(name), true
}

// declaredEncoding returns the encoding HTML parsers use for the encoding
// name declared by a meta element.
func declaredEncoding(name string) string {
	switch name {
	case "UTF-16BE", "UTF-16LE":
		return "UTF-8"
	case "x-user-defined":
		return "windows-1252"
	}
	return name
}

// contentCharset returns the encoding label in the content attribute of a
// meta element, following the algorithm for extracting a character encoding
// from a meta element (see MetaCharset) up to getting an encoding from it.
func contentCharset[S ~string | ~[]byte](s S) (S, bool) {
	pos := 0
	for {
		i := indexFold(s[pos:], "charset")
		if i < 0 {
			return s[:0], false
		}
		pos += i + len("charset")
		for pos < len(s) && ascii.IsSpace(s[pos]) {
			pos++
		}
		if pos < len(s) && s[pos] == '=' {
			break
		}
	}
	pos++ // =
	for pos < len(s) && ascii.IsSpace(s[pos]) {
		pos++
	}
	if pos == len(s) {
		return s[:0], false
	}
	if q := s[pos]; q == '"' || q == '\'' {
		for end := pos + 1; end < len(s); end++ {
			if s[end] == q {
				return s[pos+1 : end], true
			}
		}
		return s[:0], false
	}
	end := pos
	for end < len(s) && !ascii.IsSpace(s[end]) && s[end] != ';' {
		end++
	}
	return s[pos:end], true
}

// indexFold returns the index of the first ASCII case-insensitive match for
// the lowercase sub in s, or -1.
func indexFold[S ~string | ~[]byte](s S, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if ascii.EqualFold(s[i:i+len(sub)], sub) {
			return i
		}
	}
	return -1
}

// PrescanCharset returns the name of the character encoding declared at the
// start of an HTML document, following the HTML Standard's algorithm to
// [prescan a byte stream to determine its encoding], which browsers run on
// the first bytes of a document before parsing it. head is the start of the
// document: its first 1024 bytes, or the whole document if it is shorter.
// The HTML Standard encourages prescanning that many bytes, and documents
// must declare their encoding within them. A declaration cut off by the end of head
// isn't found.
//
// The prescan looks for the first meta element which declares an encoding,
// and gives the same encoding names and substitutions as [MetaCharset]. It
// reads bytes, not tokens: it skips comments, the attributes of other tags,
// and other markup up to the next ">", and finds meta elements anywhere
// else, including in the contents of raw text elements like script and
// title. Character references in attribute values aren't decoded, and a
// charset attribute with an unknown label prevents the content attribute
// from being used. It returns false if it doesn't find a declaration,
// except that:
//
//   - If head starts with "<?x" in UTF-16, which the prescan checks first, it
//     returns UTF-16LE or UTF-16BE.
//   - If head starts with an XML declaration with an encoding (e.g., <?xml
//     version="1.0" encoding="ISO-8859-2"?>), it returns that encoding, with
//     UTF-16BE and UTF-16LE replaced with UTF-8. As in the algorithm, the
//     declaration is only checked if no meta element declares an encoding,
//     and x-user-defined isn't replaced.
//
// It doesn't check for a byte order mark, which comes first in the HTML
// Standard's [encoding sniffing algorithm], followed by the charset
// parameter of the Content-Type header, which also takes precedence over the
// prescan. For example, with golang.org/x/text/encoding/htmlindex,
// golang.org/x/text/encoding/unicode and golang.org/x/text/transform:
//
//	br := bufio.NewReader(r)
//	head, _ := br.Peek(1024) // the whole input if it is shorter
//	name, ok := htmlutil.PrescanCharset(head)
//	if !ok {
//		name = "windows-1252" // or another default
//	}
//	enc, err := htmlindex.Get(name)
//	if err != nil {
//		return err
//	}
//	// BOMOverride decodes UTF-8 or UTF-16 instead if there is a byte order
//	// mark, and removes it.
//	rd := htmlstream.NewReader(transform.NewReader(br, unicode.BOMOverride(enc.NewDecoder())))
//
// [prescan a byte stream to determine its encoding]: https://html.spec.whatwg.org/multipage/parsing.html#prescan-a-byte-stream-to-determine-its-encoding
// [encoding sniffing algorithm]: https://html.spec.whatwg.org/multipage/parsing.html#encoding-sniffing-algorithm
func PrescanCharset(head []byte) (name string, ok bool) {
	switch {
	case bytes.HasPrefix(head, []byte("<\x00?\x00x\x00")):
		return "UTF-16LE", true
	case bytes.HasPrefix(head, []byte("\x00<\x00?\x00x")):
		return "UTF-16BE", true
	}
	if name, ok, done := prescanMeta(head); done {
		return name, ok
	}
	return xmlEncoding(head)
}

// prescanMeta runs the loop of the prescan from the start of b. It returns
// done == false if it runs out of bytes, which ends the prescan.
func prescanMeta(b []byte) (name string, ok, done bool) {
	for i := 0; i < len(b); i++ {
		if b[i] != '<' {
			continue
		}
		s := b[i:]
		switch {
		case bytes.HasPrefix(s, []byte("<!--")):
			// The first "-->" after the "<", whose dashes can be those
			// of "<!--".
			j := bytes.Index(b[i+2:], []byte("-->"))
			if j < 0 {
				return "", false, false
			}
			i += 2 + j + 2
		case len(s) >= 6 && ascii.EqualFold(s[1:5], "meta") && (ascii.IsSpace(s[5]) || s[5] == '/'):
			i += 5
			var (
				seen      uint8 // attrHTTPEquiv, attrContent and attrCharset
				gotPragma bool
				needSet   bool // need pragma isn't null
				need      bool // need pragma
				charset   bool // charset isn't null
				enc       string
				encOK     bool // charset isn't failure
			)
			for {
				name, value, found, j, more := prescanAttr(b, i)
				if !more {
					return "", false, false
				}
				i = j
				if !found {
					break
				}
				var bit uint8
				switch string(name) {
				case "http-equiv":
					bit = attrHTTPEquiv
				case "content":
					bit = attrContent
				case "charset":
					bit = attrCharset
				}
				if seen&bit != 0 {
					continue
				}
				seen |= bit
				switch bit {
				case attrHTTPEquiv:
					if string(value) == "content-type" {
						gotPragma = true
					}
				case attrContent:
					if label, ok := contentCharset(value); ok && !charset {
						if e, ok := internal.LookupEncoding(string(label)); ok {
							charset, enc, encOK = true, e, true
							needSet, need = true, true
						}
					}
				case attrCharset:
					enc, encOK = internal.LookupEncoding(string(value))
					charset = true
					needSet, need = true, false
				}
			}
			if !needSet || need && !gotPragma || !encOK {
				continue // next byte
			}
			return declaredEncoding(enc), true, true
		case len(s) >= 2 && isASCIIAlpha(s[1]), len(s) >= 3 && s[1] == '/' && isASCIIAlpha(s[2]):
			j := bytes.IndexAny(s, "\t\n\f\r >")
			if j < 0 {
				return "", false, false
			}
			i += j
			for {
				_, _, found, j, more := prescanAttr(b, i)
				if !more {
					return "", false, false
				}
				i = j
				if !found {
					break
				}
			}
		case len(s) >= 2 && (s[1] == '!' || s[1] == '/' || s[1] == '?'):
			j := bytes.IndexByte(s, '>')
			if j < 0 {
				return "", false, false
			}
			i += j
		}
	}
	return "", false, false
}

// The meta attributes the prescan handles.
const (
	attrHTTPEquiv uint8 = 1 << iota
	attrContent
	attrCharset
)

// prescanAttr runs the "get an attribute" steps of the prescan at b[i:]. It
// returns the attribute's name and value, lowercased, whether there is one,
// and the position after it. more is false if it runs out of bytes.
func prescanAttr(b []byte, i int) (name, value []byte, found bool, next int, more bool) {
	for i < len(b) && (ascii.IsSpace(b[i]) || b[i] == '/') {
		i++
	}
	if i == len(b) {
		return nil, nil, false, i, false
	}
	if b[i] == '>' {
		return nil, nil, false, i, true
	}
	for {
		if i == len(b) {
			return nil, nil, false, i, false
		}
		c := b[i]
		if c == '=' && len(name) != 0 {
			i++
			break
		}
		if ascii.IsSpace(c) {
			// spaces
			for i < len(b) && ascii.IsSpace(b[i]) {
				i++
			}
			if i == len(b) {
				return nil, nil, false, i, false
			}
			if b[i] != '=' {
				return name, nil, true, i, true
			}
			i++
			break
		}
		if c == '/' || c == '>' {
			return name, nil, true, i, true
		}
		name = append(name, ascii.ToLowerByte(c))
		i++
	}
	// value
	for i < len(b) && ascii.IsSpace(b[i]) {
		i++
	}
	if i == len(b) {
		return nil, nil, false, i, false
	}
	switch c := b[i]; c {
	case '"', '\'':
		for i++; i < len(b); i++ {
			if b[i] == c {
				return name, value, true, i + 1, true
			}
			value = append(value, ascii.ToLowerByte(b[i]))
		}
		return nil, nil, false, i, false
	case '>':
		return name, nil, true, i, true
	}
	for ; i < len(b); i++ {
		c := b[i]
		if ascii.IsSpace(c) || c == '>' {
			return name, value, true, i, true
		}
		value = append(value, ascii.ToLowerByte(c))
	}
	return nil, nil, false, i, false
}

// xmlEncoding returns the encoding of an XML declaration at the start of b,
// following the steps to get an XML encoding of the prescan.
func xmlEncoding(b []byte) (string, bool) {
	if !bytes.HasPrefix(b, []byte("<?xml")) {
		return "", false
	}
	end := bytes.IndexByte(b, '>')
	if end < 0 {
		return "", false
	}
	i := bytes.Index(b[:end], []byte("encoding"))
	if i < 0 {
		return "", false
	}
	i += len("encoding")
	for i < len(b) && b[i] <= ' ' {
		i++
	}
	if i == len(b) || b[i] != '=' {
		return "", false
	}
	i++
	for i < len(b) && b[i] <= ' ' {
		i++
	}
	if i == len(b) || b[i] != '"' && b[i] != '\'' {
		return "", false
	}
	q := b[i]
	i++
	n := bytes.IndexByte(b[i:], q)
	if n < 0 {
		return "", false
	}
	label := b[i : i+n]
	for _, c := range label {
		if c <= ' ' {
			return "", false
		}
	}
	name, ok := internal.LookupEncoding(string(label))
	if !ok {
		return "", false
	}
	if name == "UTF-16BE" || name == "UTF-16LE" {
		name = "UTF-8"
	}
	return name, true
}

func isASCIIAlpha(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
}
