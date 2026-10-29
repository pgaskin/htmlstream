package xmlutil_test

import (
	"fmt"
	"os"
	"strings"

	"github.com/pgaskin/htmlstream"
	"github.com/pgaskin/htmlstream/xmlutil"
)

// DetectEncoding gives the encoding of a document from its byte order mark,
// its first bytes and its XML declaration.
func ExampleDetectEncoding() {
	for _, head := range []string{
		`<?xml version="1.0"?><a/>`,
		`<?xml version="1.0" encoding="ISO-8859-1"?><a/>`,
		`<?xml version="1.0" encoding="x-custom"?><a/>`,
		"\xEF\xBB\xBF<a/>",
		"\xFF\xFE<\x00?\x00x\x00m\x00l\x00 \x00e\x00n\x00c\x00o\x00d\x00i\x00n\x00g\x00=\x00'\x00U\x00T\x00F\x00-\x001\x006\x00'\x00?\x00>\x00",
	} {
		fmt.Printf("%+v\n", xmlutil.DetectEncoding([]byte(head)))
	}
	// Output:
	// {Name:UTF-8 Declared: BOM:0}
	// {Name:windows-1252 Declared:ISO-8859-1 BOM:0}
	// {Name: Declared:x-custom BOM:0}
	// {Name:UTF-8 Declared: BOM:3}
	// {Name:UTF-16LE Declared:UTF-16 BOM:2}
}

// ParseDecl reads malformed declarations too, returning an error for the
// first difference from the syntax.
func ExampleParseDecl() {
	for _, decl := range []string{
		`<?xml version="1.0" encoding="UTF-8" standalone="no"?>`,
		`<?xml encoding='EUC-JP'?>`,
		`<?xml version=1.0 encoding=latin1?>`,
	} {
		d, n, err := xmlutil.ParseDecl([]byte(decl))
		fmt.Printf("%+v %d %v\n", d, n, err)
	}
	// Output:
	// {Version:1.0 Encoding:UTF-8 Standalone:no} 54 <nil>
	// {Version: Encoding:EUC-JP Standalone:} 25 <nil>
	// {Version:1.0 Encoding:latin1 Standalone:} 35 xmlutil: XML declaration: value of "version" isn't quoted at offset 14
}

// After decoding a document to UTF-8, a Transformer can replace the XML
// declaration, which names the original encoding.
func ExampleAppendDecl() {
	const doc = "<?xml version='1.0' encoding='ISO-8859-1'?>\n<p>café</p>" // decoded

	x := htmlstream.NewTransformer(os.Stdout, strings.NewReader(doc))
	x.SetMode(htmlstream.XML)
	for x.Next() {
		tok := x.Token()
		if tok.Pos() != 0 || tok.Kind() != htmlstream.ProcInst {
			continue
		}
		d, _, err := xmlutil.ParseDecl(tok.Raw())
		if err != nil {
			panic(err)
		}
		d.Encoding = "UTF-8"
		if err := x.Replace(xmlutil.AppendDecl(nil, d)); err != nil {
			panic(err)
		}
	}
	if err := x.Close(); err != nil {
		panic(err)
	}
	// Output:
	// <?xml version="1.0" encoding="UTF-8"?>
	// <p>café</p>
}

// AppendEscape escapes text and attribute values so XML parsers read them
// back unchanged, including whitespace in attribute values.
func ExampleAppendEscape() {
	const value = "a < b && c\td\r\ne"
	b := []byte(`<p title="`)
	b = xmlutil.AppendEscape(b, value)
	b = append(b, `">`...)
	b = xmlutil.AppendEscape(b, value)
	b = append(b, "</p>"...)
	fmt.Println(string(b))
	// Output:
	// <p title="a &lt; b &amp;&amp; c&#9;d&#13;&#10;e">a &lt; b &amp;&amp; c&#9;d&#13;&#10;e</p>
}

// ASCIICompatible reports whether a Reader can read a document without
// decoding it first.
func ExampleEncoding_ASCIICompatible() {
	for _, head := range []string{
		`<?xml version="1.0" encoding="Shift_JIS"?>`,
		`<?xml version="1.0" encoding="ISO-2022-JP"?>`,
		"\xFE\xFF\x00<\x00?\x00x\x00m\x00l",
	} {
		e := xmlutil.DetectEncoding([]byte(head))
		fmt.Println(e.Name, e.ASCIICompatible())
	}
	// Output:
	// Shift_JIS true
	// ISO-2022-JP false
	// UTF-16BE false
}
