package htmlutil_test

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing/iotest"
	"time"

	"github.com/pgaskin/htmlstream"
	"github.com/pgaskin/htmlstream/htmlutil"
)

// eventDates returns the dates of the time elements of an HTML document.
func eventDates(r io.Reader) (dates []time.Time, err error) {
	rd := htmlstream.NewReader(r)
	rd.SetMaxTokenSize(64 << 10)
	defer func() {
		if rerr := rd.Err(); rerr != nil {
			err = rerr // it stopped the Reader, and may have cut off the text which failed to parse
		}
	}()
	for rd.Find(htmlstream.Tag("time")) {
		s, _ := htmlutil.TextContent(rd, 64)
		d, err := time.Parse(time.DateOnly, strings.TrimSpace(s))
		if err != nil {
			return nil, fmt.Errorf("event date: %w", err)
		}
		dates = append(dates, d)
	}
	return dates, nil // the deferred function reports a read error
}

// The text helpers have no error result: a read error stops the Reader,
// cutting off the text, and Err returns it. A function which returns its own
// error for text it can't parse can check Err in a deferred function, so that
// it returns the read error instead, which comes first.
func ExampleTextContent_readError() {
	const page = `<ul><li><time>2026-10-30</time> Launch<li><time>2026-11-05</time> Review</ul>`

	for _, r := range []io.Reader{
		strings.NewReader(page),
		strings.NewReader(strings.Replace(page, "11-05", "11-5", 1)),
		io.MultiReader(strings.NewReader(page[:54]), iotest.ErrReader(errors.New("connection reset"))),
	} {
		dates, err := eventDates(r)
		if err != nil {
			fmt.Println("error:", err)
			continue
		}
		var s []string
		for _, d := range dates {
			s = append(s, d.Format("Mon Jan 2"))
		}
		fmt.Println(strings.Join(s, ", "))
	}
	// Output:
	// Fri Oct 30, Thu Nov 5
	// error: event date: parsing time "2026-11-5" as "2006-01-02": cannot parse "5" as "02"
	// error: connection reset
}
