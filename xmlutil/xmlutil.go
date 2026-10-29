// Package xmlutil provides XML-specific helpers, including XML-style charset
// detection and declaration parsing.
//
// It is compatible with the XML spec for most documents, but does not strictly
// adhere to it and is generally a bit more lenient (since this library is
// primarily designed for the HTML spec).
//
// The helpers are for reading XML documents with a
// [github.com/pgaskin/htmlstream.Reader] in
// [github.com/pgaskin/htmlstream.XML] mode, and writing them. A Reader
// requires UTF-8 input, or input in another encoding in which ASCII characters
// have their ASCII bytes, so documents in other encodings (e.g., UTF-16) must
// be decoded first (see Input in the htmlstream package documentation).
package xmlutil

import (
	"bytes"
	"errors"
	"strconv"

	"github.com/pgaskin/htmlstream/internal"
)

// TODO: maybe add a checker for xml well-formedness

// AppendEscape appends s to dst, escaped for use as text or as a
// double-quoted attribute value in XML: &, <, >, " and tabs, newlines and
// carriage returns are written as character references. XML parsers then read
// it back unchanged: references aren't changed by [end-of-line handling],
// which converts carriage returns to newlines, or by
// [attribute-value normalization], which converts whitespace in attribute
// values to spaces, and escaping > keeps "]]>" out of [character data]. '
// isn't escaped, so attribute values must be in double quotes. Characters XML
// doesn't allow aren't replaced (see
// [github.com/pgaskin/htmlstream.AppendXMLChars] and
// [github.com/pgaskin/htmlstream.ValidXMLChars]). It follows XML 1.0: NEL and
// U+2028, which are line ends in XML 1.1 documents, aren't escaped, and
// neither are U+007F to U+009F, which XML 1.1 only allows as character
// references (see Modes in the htmlstream package documentation).
//
// [end-of-line handling]: https://www.w3.org/TR/xml/#sec-line-ends
// [attribute-value normalization]: https://www.w3.org/TR/xml/#AVNormalize
// [character data]: https://www.w3.org/TR/xml/#syntax
func AppendEscape[S ~string | ~[]byte](dst []byte, s S) []byte {
	return internal.AppendEscape(dst, s, internal.EscapeXML)
}

// Decl is an XML declaration, or the text declaration of an external parsed
// entity, which has no version or standalone declaration. Its values are as
// written in the declaration, or empty if they are missing.
type Decl struct {
	Version    string // e.g., "1.0"
	Encoding   string // the encoding name, e.g., "UTF-8"
	Standalone string // "yes" or "no"
}

// ErrNoDecl is returned by [ParseDecl] for input which doesn't start with an
// XML declaration.
var ErrNoDecl = errors.New("xmlutil: no XML declaration")

// DeclError is returned by [ParseDecl] for an XML declaration which doesn't
// follow the syntax of XML declarations and text declarations.
type DeclError struct {
	Offset int    // the offset of the first byte which doesn't follow the syntax
	Msg    string // what is wrong
}

// Error returns the message with the offset, as "xmlutil: XML declaration:
// Msg at offset Offset".
func (e *DeclError) Error() string {
	return "xmlutil: XML declaration: " + e.Msg + " at offset " + strconv.Itoa(e.Offset)
}

// ParseDecl parses the XML declaration at the start of b, and returns it and
// its length. b is, e.g., the start of a document, or the Raw of a
// [github.com/pgaskin/htmlstream.ProcInst] token with the target "xml" (at the
// start of the document, the only place where an XML declaration is allowed;
// elsewhere, XML reserves the target). It returns [ErrNoDecl] if b doesn't
// start with "<?xml" followed by XML whitespace or "?>" (the target is
// case-sensitive).
//
// The declaration follows the [XMLDecl] production of XML 1.0: the
// version, encoding and standalone pseudo-attributes, in that order, with
// whitespace before each, "=" between the name and the value (with optional
// whitespace around it), and the value in single or double quotes. The
// version must match [VersionNum] (e.g., "1.0"), the encoding [EncName]
// (e.g., "UTF-8"), and standalone must be "yes" or "no" ([SDDecl]). A
// declaration without a version is accepted if it has an encoding and no
// standalone, like the text declarations of external parsed entities
// ([TextDecl]).
//
// For a declaration which doesn't follow the syntax, it returns a
// [*DeclError] for the first difference, along with the pseudo-attributes it
// read and the length of the declaration: it reads them in any order and with
// unquoted values (up to the next whitespace or "?>"), and skips unknown ones.
// For a declaration cut off by the end of b, the length is 0.
//
// [XMLDecl]: https://www.w3.org/TR/xml/#NT-XMLDecl
// [VersionNum]: https://www.w3.org/TR/xml/#NT-VersionNum
// [EncName]: https://www.w3.org/TR/xml/#NT-EncName
// [SDDecl]: https://www.w3.org/TR/xml/#NT-SDDecl
// [TextDecl]: https://www.w3.org/TR/xml/#NT-TextDecl
func ParseDecl(b []byte) (d Decl, n int, err error) {
	id, n, derr, ok := internal.ParseDecl(b)
	switch {
	case !ok:
		return Decl{}, 0, ErrNoDecl
	case derr != nil:
		return Decl(id), n, &DeclError{derr.Offset, derr.Msg}
	}
	return Decl(id), n, nil
}

// AppendDecl appends an XML declaration with the non-empty values of d to
// dst, in double quotes, e.g., <?xml version="1.0" encoding="UTF-8"?>. The
// values are written as-is and aren't checked (see [ParseDecl] for their
// syntax). An XML declaration must have a version, and a text declaration an
// encoding.
func AppendDecl(dst []byte, d Decl) []byte {
	dst = append(dst, "<?xml"...)
	for _, a := range [...]struct{ name, value string }{
		{"version", d.Version},
		{"encoding", d.Encoding},
		{"standalone", d.Standalone},
	} {
		if a.value != "" {
			dst = append(dst, ' ')
			dst = append(dst, a.name...)
			dst = append(dst, `="`...)
			dst = append(dst, a.value...)
			dst = append(dst, '"')
		}
	}
	return append(dst, "?>"...)
}

// Encoding is the character encoding of an XML document detected by
// [DetectEncoding].
type Encoding struct {
	// Name is the name of the encoding: the name the [Encoding Standard]
	// gives it (e.g., "UTF-8", "UTF-16LE" or "windows-1252"), which
	// golang.org/x/text/encoding/htmlindex.Get accepts, or "UTF-32BE" or
	// "UTF-32LE", which aren't in the Encoding Standard (see
	// golang.org/x/text/encoding/unicode/utf32). It is empty if the encoding
	// isn't known: for EBCDIC (see Declared, e.g., with
	// golang.org/x/text/encoding/ianaindex.IANA), 32-bit encodings with
	// unusual byte orders, and ASCII-compatible encodings declared with a
	// name which isn't a label of the Encoding Standard.
	Name string

	// Declared is the encoding name in the XML declaration, as written, or
	// empty if there is none. It is read from declarations which don't
	// follow the syntax too (see [ParseDecl]).
	Declared string

	// BOM is the length of the byte order mark at the start of the document,
	// or 0 if there is none. Some decoders keep it as a U+FEFF character
	// (e.g., those of htmlindex), so it can be skipped before decoding.
	BOM int
}

// ASCIICompatible reports whether a [github.com/pgaskin/htmlstream.Reader]
// can read a document in the encoding without decoding it first. It is
// best-effort, from [Encoding.Name]: it reports true for the encodings of the
// [Encoding Standard] in which bytes 0x00 to 0x7F outside multi-byte
// characters are ASCII characters (which it calls ASCII-compatible in its
// [security background]): UTF-8, the single-byte encodings (e.g.,
// windows-1252), EUC-JP, Shift_JIS, EUC-KR, Big5, GBK and gb18030. It reports
// false for UTF-16BE, UTF-16LE, UTF-32BE, UTF-32LE, ISO-2022-JP (whose escape
// sequences switch to modes in which ASCII bytes are parts of JIS
// characters), replacement, and empty or unknown names, including those of
// ASCII-compatible encodings outside the Encoding Standard.
//
// A Reader reads a document in an ASCII-compatible encoding with the
// differences described in Input in the htmlstream package documentation.
// In Shift_JIS, Big5, GBK and gb18030, the second byte of a character can
// be that of an ASCII character from "@" to "~" (0x40 to 0x7E: letters,
// "[", "]", "\", "^", "_", "`", "{", "|", "}", "~" and "@"), in EUC-KR that
// of a letter, and in gb18030 the second and fourth bytes of a four-byte
// character are those of ASCII digits; none of them is a markup character
// or whitespace. Decoded text has character references and U+FFFD
// replacements as UTF-8, mixed with the undecoded bytes.
//
// [Encoding Standard]: https://encoding.spec.whatwg.org/
// [security background]: https://encoding.spec.whatwg.org/#security-background
func (e Encoding) ASCIICompatible() bool {
	name, ok := internal.LookupEncoding(e.Name)
	if !ok || name != e.Name {
		return false
	}
	switch name {
	case "UTF-16BE", "UTF-16LE", "ISO-2022-JP", "replacement":
		return false
	}
	return true
}

// DetectEncoding detects the character encoding of an XML document (or
// external parsed entity) starting with head, as described by XML 1.0 for
// documents without external encoding information ([Appendix F.1]). head
// should have at least the XML declaration (e.g., the first 1024 bytes of
// the document, or the whole document if it is shorter). A declaration cut
// off by the end of head is read up to the end.
//
// The first four bytes give the byte order mark or the encoding family:
//
//   - With a byte order mark (EF BB BF, FE FF or FF FE, and for UCS-4, 00 00
//     FE FF, FF FE 00 00, 00 00 FF FE or FE FF 00 00), the encoding is
//     UTF-8, UTF-16BE, UTF-16LE, UTF-32BE, UTF-32LE or unknown (UCS-4 with
//     the byte orders 2143 or 3412).
//   - Without one, "<?" in a 16-bit or 32-bit encoding gives UTF-16BE,
//     UTF-16LE, UTF-32BE, UTF-32LE or unknown (the unusual byte orders), and
//     "<?xm" in EBCDIC (4C 6F A7 94) gives an unknown encoding, which the
//     declaration names (e.g., IBM037).
//   - Otherwise, the encoding is ASCII-compatible: the one named by the XML
//     declaration, or UTF-8 without a declaration or encoding name.
//
// Encoding names are looked up like [get an encoding] in the Encoding
// Standard, which browsers use for XML documents too: they are matched ASCII
// case-insensitively after removing leading and trailing whitespace, and
// some are aliases of other encodings (e.g., ISO-8859-1 and US-ASCII are
// windows-1252). XML 1.0 refers to the encodings registered with IANA
// instead ([4.3.3]), which golang.org/x/text/encoding/ianaindex.IANA looks up
// (e.g., from [Encoding.Declared]). As in the steps to [change the encoding] of
// the HTML Standard, UTF-16 (or UTF-16BE or UTF-16LE) declared in an
// ASCII-compatible document gives UTF-8. Some names (e.g., ISO-2022-KR) give
// the replacement encoding, which decodes any non-empty input to a single
// U+FFFD.
//
// DetectEncoding differs from XML processors in these ways:
//
//   - The declared encoding isn't checked against the encoding detected from
//     the byte order mark or the first bytes, which XML 1.0 requires. With
//     a byte order mark or a 16-bit or 32-bit encoding, the declaration only
//     sets [Encoding.Declared].
//   - XML declarations which don't follow the syntax, a fatal error for XML
//     processors, are read like [ParseDecl] does.
//   - It doesn't take external encoding information, like the charset
//     parameter of the Content-Type header, which takes precedence over the
//     declaration, but not over a byte order mark ([RFC 7303]).
//
// Browsers differ further: they support only the encodings of the Encoding
// Standard (not UTF-32, EBCDIC or UCS-4), and read FF FE 00 00 as a UTF-16LE
// byte order mark. For an XML declaration in an HTML document,
// [github.com/pgaskin/htmlstream/htmlutil.PrescanCharset] follows the HTML
// Standard instead, which looks for "encoding" anywhere before the first ">",
// and only in ASCII-compatible encodings.
//
// For example, with golang.org/x/text/encoding/htmlindex,
// golang.org/x/text/encoding/unicode/utf32 (for the UTF-32 encodings, which
// htmlindex doesn't have) and golang.org/x/text/transform:
//
//	br := bufio.NewReader(r)
//	head, _ := br.Peek(1024) // the whole input if it is shorter
//	e := xmlutil.DetectEncoding(head)
//	var enc encoding.Encoding
//	switch e.Name {
//	case "":
//		return fmt.Errorf("unsupported encoding %q", e.Declared)
//	case "UTF-32BE":
//		enc = utf32.UTF32(utf32.BigEndian, utf32.IgnoreBOM)
//	case "UTF-32LE":
//		enc = utf32.UTF32(utf32.LittleEndian, utf32.IgnoreBOM)
//	default:
//		var err error
//		if enc, err = htmlindex.Get(e.Name); err != nil {
//			return err
//		}
//	}
//	br.Discard(e.BOM)
//	rd := htmlstream.NewReader(transform.NewReader(br, enc.NewDecoder()))
//	rd.SetMode(htmlstream.XML)
//
// The XML declaration of a document decoded to UTF-8 names the original
// encoding, so a Transformer writing the document can replace it (see
// [AppendDecl]).
//
// [Appendix F.1]: https://www.w3.org/TR/xml/#sec-guessing-no-ext-info
// [get an encoding]: https://encoding.spec.whatwg.org/#concept-encoding-get
// [change the encoding]: https://html.spec.whatwg.org/multipage/parsing.html#change-the-encoding
// [4.3.3]: https://www.w3.org/TR/xml/#charencoding
// [RFC 7303]: https://www.rfc-editor.org/rfc/rfc7303#section-3.2
func DetectEncoding(head []byte) Encoding {
	var e Encoding
	unit, sig := 1, 0 // the code unit size, and the byte holding ASCII characters in a code unit
	switch {
	case bytes.HasPrefix(head, []byte{0x00, 0x00, 0xFE, 0xFF}):
		e, unit, sig = Encoding{Name: "UTF-32BE", BOM: 4}, 4, 3
	case bytes.HasPrefix(head, []byte{0xFF, 0xFE, 0x00, 0x00}):
		e, unit, sig = Encoding{Name: "UTF-32LE", BOM: 4}, 4, 0
	case bytes.HasPrefix(head, []byte{0x00, 0x00, 0xFF, 0xFE}):
		e, unit, sig = Encoding{BOM: 4}, 4, 2
	case bytes.HasPrefix(head, []byte{0xFE, 0xFF, 0x00, 0x00}):
		e, unit, sig = Encoding{BOM: 4}, 4, 1
	case bytes.HasPrefix(head, []byte{0xFE, 0xFF}):
		e, unit, sig = Encoding{Name: "UTF-16BE", BOM: 2}, 2, 1
	case bytes.HasPrefix(head, []byte{0xFF, 0xFE}):
		e, unit, sig = Encoding{Name: "UTF-16LE", BOM: 2}, 2, 0
	case bytes.HasPrefix(head, []byte{0xEF, 0xBB, 0xBF}):
		e = Encoding{Name: "UTF-8", BOM: 3}
	case bytes.HasPrefix(head, []byte{0x00, 0x00, 0x00, '<'}):
		e, unit, sig = Encoding{Name: "UTF-32BE"}, 4, 3
	case bytes.HasPrefix(head, []byte{'<', 0x00, 0x00, 0x00}):
		e, unit, sig = Encoding{Name: "UTF-32LE"}, 4, 0
	case bytes.HasPrefix(head, []byte{0x00, 0x00, '<', 0x00}):
		unit, sig = 4, 2
	case bytes.HasPrefix(head, []byte{0x00, '<', 0x00, 0x00}):
		unit, sig = 4, 1
	case bytes.HasPrefix(head, []byte{0x00, '<', 0x00, '?'}):
		e, unit, sig = Encoding{Name: "UTF-16BE"}, 2, 1
	case bytes.HasPrefix(head, []byte{'<', 0x00, '?', 0x00}):
		e, unit, sig = Encoding{Name: "UTF-16LE"}, 2, 0
	case bytes.HasPrefix(head, []byte{0x4C, 0x6F, 0xA7, 0x94}):
		e.Declared = declaredEncoding(fromEBCDIC(head))
		return e
	default:
		e.Declared = declaredEncoding(head)
		switch name, ok := internal.LookupEncoding(e.Declared); {
		case e.Declared == "":
			e.Name = "UTF-8"
		case !ok:
		case name == "UTF-16BE" || name == "UTF-16LE":
			e.Name = "UTF-8"
		default:
			e.Name = name
		}
		return e
	}
	if unit == 1 {
		e.Declared = declaredEncoding(head[e.BOM:])
	} else {
		e.Declared = declaredEncoding(narrow(head[e.BOM:], unit, sig))
	}
	return e
}

// declaredEncoding returns the encoding name of the XML declaration at the
// start of b, if any.
func declaredEncoding(b []byte) string {
	d, _, err := ParseDecl(b)
	if err == ErrNoDecl {
		return ""
	}
	return d.Encoding
}

// narrow returns the ASCII characters at the start of b in an encoding with
// a code unit size of unit bytes, holding the character in the byte at sig
// and zero bytes elsewhere, up to the first other character or ">".
func narrow(b []byte, unit, sig int) []byte {
	var s []byte
	for i := 0; i+unit <= len(b); i += unit {
		for j := range unit {
			if j != sig && b[i+j] != 0 {
				return s
			}
		}
		c := b[i+sig]
		if c >= 0x80 {
			return s
		}
		s = append(s, c)
		if c == '>' {
			break
		}
	}
	return s
}

// fromEBCDIC returns the characters at the start of b, in EBCDIC, which XML
// declarations use (letters, digits, whitespace and <?=._-"'>), up to the
// first other character or ">". Their bytes are the same in the EBCDIC code
// pages.
func fromEBCDIC(b []byte) []byte {
	var s []byte
	for _, c := range b {
		var a byte
		switch {
		case 0x81 <= c && c <= 0x89:
			a = 'a' + c - 0x81
		case 0x91 <= c && c <= 0x99:
			a = 'j' + c - 0x91
		case 0xA2 <= c && c <= 0xA9:
			a = 's' + c - 0xA2
		case 0xC1 <= c && c <= 0xC9:
			a = 'A' + c - 0xC1
		case 0xD1 <= c && c <= 0xD9:
			a = 'J' + c - 0xD1
		case 0xE2 <= c && c <= 0xE9:
			a = 'S' + c - 0xE2
		case 0xF0 <= c && c <= 0xF9:
			a = '0' + c - 0xF0
		default:
			a = ebcdicPunct[c]
		}
		if a == 0 {
			return s
		}
		s = append(s, a)
		if a == '>' {
			break
		}
	}
	return s
}

// ebcdicPunct maps the EBCDIC bytes of the whitespace and punctuation in XML
// declarations to ASCII. NL (0x15) is mapped to a newline.
var ebcdicPunct = [256]byte{
	0x05: '\t', 0x0D: '\r', 0x15: '\n', 0x25: '\n', 0x40: ' ',
	0x4B: '.', 0x4C: '<', 0x60: '-', 0x6D: '_', 0x6E: '>', 0x6F: '?',
	0x7D: '\'', 0x7E: '=', 0x7F: '"',
}
