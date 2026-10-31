package htmlstream_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing/iotest"

	"github.com/pgaskin/htmlstream"
)

// scaleImages doubles the width and height of the img elements of an HTML
// document. It returns the first error: a read or write error, or a size
// which isn't a number, after which it writes nothing more.
func scaleImages(w io.Writer, r io.Reader) (err error) {
	x := htmlstream.NewTransformer(w, r)
	defer func() {
		x.Fail(err) // leave this out to write the rest of the document after our own error
		if cerr := x.Close(); cerr != nil {
			err = cerr
		}
	}()
	for x.Find(htmlstream.Tag("img")) {
		for _, name := range []string{"width", "height"} {
			a, ok := x.Token().Attr(name)
			if !ok {
				continue
			}
			n, err := strconv.Atoi(a.Value())
			if err != nil {
				return fmt.Errorf("img %s at offset %d: %w", name, x.Token().Pos(), err)
			}
			if err := x.SetAttr(name, strconv.Itoa(n*2)); err != nil {
				return err
			}
		}
	}
	return nil // the deferred Close writes the rest of the document and reports its errors
}

// A function using a Transformer can call Fail and Close in a deferred
// function, so that it returns the first error, reading and writing nothing
// more after its own error, and reporting read and write errors even when it
// returns early. To write the rest of the document after some of its own
// errors, it can instead call Fail where the other errors are returned, and
// leave Fail out of the deferred function.
func ExampleTransformer_Fail() {
	const page = `<p><img src=a.png width=100 height=50> and <img src=b.png width=40></p>`

	for _, r := range []io.Reader{
		strings.NewReader(page),
		strings.NewReader(strings.Replace(page, "40", "40px", 1)),
		io.MultiReader(strings.NewReader(page[:50]), iotest.ErrReader(errors.New("connection reset"))),
	} {
		var b bytes.Buffer
		if err := scaleImages(&b, r); err != nil {
			fmt.Printf("error: %v (wrote %d bytes)\n", err, b.Len())
			continue
		}
		fmt.Println(b.String())
	}
	// Output:
	// <p><img src=a.png width="200" height="100"> and <img src=b.png width="80"></p>
	// error: img width at offset 43: strconv.Atoi: parsing "40px": invalid syntax (wrote 0 bytes)
	// error: connection reset (wrote 0 bytes)
}
