package polyglot_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing/iotest"

	"github.com/pgaskin/htmlstream"
	"github.com/pgaskin/htmlstream/polyglot"
)

// convertArticle converts an HTML article to polyglot markup, rejecting
// articles with forms.
func convertArticle(w io.Writer, r io.Reader) (err error) {
	opt := polyglot.Options{DoctypeMode: polyglot.NoDoctype}
	rd := opt.NewReader(r)
	pw := polyglot.NewWriter(w, opt)
	defer func() {
		pw.Fail(rd.Err()) // a read error comes first
		pw.Fail(err)      // leave this out to write the rest of the document after our own error
		if cerr := pw.Close(); cerr != nil {
			err = cerr
		}
	}()
	for rd.Next() {
		tok := rd.Token()
		if tok.Kind() == htmlstream.StartTag && tok.Is("form") {
			return fmt.Errorf("form at offset %d", tok.Pos())
		}
		if err := pw.WriteToken(tok); err != nil {
			return err
		}
	}
	return nil // the deferred Close ends the document and reports the errors
}

// A function converting the tokens of its own Reader can give the Reader's
// error and its own to Fail in a deferred function before Close, so that it
// returns the first error, and the Writer writes nothing more after an error.
func ExampleWriter_Fail() {
	const page = `<p>Some <b>bold</b> text<p>More text`

	for _, r := range []io.Reader{
		strings.NewReader(page),
		strings.NewReader(page + `<form action=/x>`),
		io.MultiReader(strings.NewReader(page[:20]), iotest.ErrReader(errors.New("connection reset"))),
	} {
		var b bytes.Buffer
		if err := convertArticle(&b, r); err != nil {
			fmt.Printf("error: %v (wrote %d bytes)\n", err, b.Len())
			continue
		}
		fmt.Println(b.String())
	}
	// Output:
	// <html xmlns="http://www.w3.org/1999/xhtml"><head><meta charset="UTF-8" /></head><body><p>Some <b>bold</b> text</p><p>More text</p></body></html>
	// error: form at offset 36 (wrote 0 bytes)
	// error: connection reset (wrote 0 bytes)
}
