package difffuzz

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/pgaskin/htmlstream"
	"github.com/pgaskin/htmlstream/polyglot"
)

var probeFlag = flag.String("probe", "", "file with the inputs for TestProbe: a fuzz corpus file, or one input per line")

// TestProbe prints the polyglot.Convert output and warnings for the inputs in
// the file named by -probe, the HTML DOMs of the input and the output and the
// XML DOM of the output if they differ, and the Reader tokens. The file is a
// fuzz corpus file (its first string value is the input), or has one input
// per line, as a quoted Go string or raw.
func TestProbe(t *testing.T) {
	if *probeFlag == "" {
		t.Skip("no -probe file")
	}
	b, err := os.ReadFile(*probeFlag)
	if err != nil {
		t.Fatal(err)
	}
	var ins []string
	if in, ok, err := corpusString(b); err != nil {
		t.Fatalf("%s: %v", *probeFlag, err)
	} else if ok {
		ins = append(ins, in)
	} else {
		sc := bufio.NewScanner(bytes.NewReader(b))
		sc.Buffer(nil, len(b)+1)
		for sc.Scan() {
			line := sc.Text()
			if line == "" {
				continue
			}
			in, err := strconv.Unquote(line)
			if err != nil {
				in = line
			}
			ins = append(ins, in)
		}
	}
	for _, in := range ins {
		var b bytes.Buffer
		var warns []string
		err = polyglot.Convert(&b, strings.NewReader(in), polyglot.Options{Warn: func(w polyglot.Warning) {
			warns = append(warns, fmt.Sprintf("%d: %s", w.Pos, w.Msg))
		}})
		out := b.String()
		fmt.Printf("=== %q\nerr: %v\nout: %s\n", in, err, out)
		for _, w := range warns {
			fmt.Printf("warn: %s\n", w)
		}
		in1, out1 := htmlDOM(t, in), htmlDOM(t, out)
		x, xerr := xmlDOM(out)
		fmt.Printf("HTML in == out: %v, XML == HTML out: %v (xml err %v)\n", in1 == out1, x == out1, xerr)
		if in1 != out1 || x != out1 {
			fmt.Printf("in DOM:\n%s\nout DOM:\n%s\nXML DOM:\n%s\n", in1, out1, x)
		}
		r := htmlstream.NewReader(strings.NewReader(in))
		for r.Next() {
			tok := r.Token()
			fmt.Printf("  tok %v implied=%v stray=%v ns=%s\n", tok, tok.Implied(), tok.Stray(), tok.NS())
		}
	}
}

// corpusString returns the first string value of a fuzz corpus file, and
// false if b isn't a fuzz corpus file. A fuzz corpus file without a string
// value is an error.
func corpusString(b []byte) (string, bool, error) {
	rest, ok := bytes.CutPrefix(b, []byte("go test fuzz v1\n"))
	if !ok {
		return "", false, nil
	}
	for line := range strings.Lines(string(rest)) {
		v, ok := strings.CutPrefix(strings.TrimSpace(line), "string(")
		if !ok {
			continue
		}
		v, ok = strings.CutSuffix(v, ")")
		if !ok {
			return "", false, fmt.Errorf("invalid value %q", line)
		}
		s, err := strconv.Unquote(v)
		if err != nil {
			return "", false, fmt.Errorf("invalid value %q: %w", line, err)
		}
		return s, true, nil
	}
	return "", true, errors.New("no string value")
}
