// Package xml_testdata provides the XML test cases in testdata/xml:
// documents, well-formed and malformed, with their tokens.
//
// Each case has the sections in (the input) and tokens (the tokens of an
// htmlstream.Reader reading the input in XML mode, or in Universal mode with
// the universal flag). The tokens are one per line: the depth and the token
// (see htmlstream.Token.String), followed by text="..." with the decoded
// text of text, comment and CDATA tokens, data="..." with the data of
// processing instructions, and ns="..." with the namespace and name="..."
// with the decoded value of each attribute of start tags (Go-quoted). A
// last line "error: ..." holds the error of the Reader, if any.
package xml_testdata

import (
	"embed"
	"iter"
	"maps"
	"slices"

	"github.com/pgaskin/htmlstream/testdata/internal"
)

//go:embed *.txtar
var fsys embed.FS

var files []internal.File

func init() {
	var err error
	if files, err = internal.ParseFiles(fsys, "*.txtar"); err != nil {
		panic(err)
	}
	if _, err := internal.Cases(files, "*", "in", "tokens"); err != nil {
		panic(err)
	}
}

// Case is a test case.
type Case struct {
	flags    map[string]struct{}
	sections map[string]string
}

// Cases returns the test cases in the files matching pattern (see
// [path.Match]), such as "*.txtar" or "names.txtar", with their names, in
// file order, then in the order of the cases in each file. Names are
// unique.
//
// The files are like txtar archives: lines of the form "-- header --" start
// sections, and the text before the first one is a comment. A header naming
// one of sections (normally "in" and "tokens") starts that section of
// the current case, and any other header starts a case, with its name and
// flags; the text of that section is a comment. The value of a section is
// its text without the last newline. A section whose header has "quoted"
// after its name holds the value as a Go string literal instead.
//
// Cases panics if pattern is malformed, no file matches it, two cases have
// the same name, a case lacks the first of sections, or a file has a section
// before the first case, a duplicate section or an invalid quoted value.
func Cases(pattern string, sections ...string) iter.Seq2[string, Case] {
	cases, err := internal.Cases(files, pattern, sections...)
	if err != nil {
		panic(err)
	}
	return func(yield func(string, Case) bool) {
		for _, c := range cases {
			flags := make(map[string]struct{}, len(c.Flags))
			for _, f := range c.Flags {
				flags[f] = struct{}{}
			}
			if !yield(c.Name, Case{flags, c.Sections}) {
				return
			}
		}
	}
}

// Files returns the names of the test case files, e.g. "names.txtar", sorted.
func Files() iter.Seq[string] {
	return func(yield func(string) bool) {
		for _, f := range files {
			if !yield(f.Name) {
				return
			}
		}
	}
}

// Flags returns the flags of the case, sorted.
func (c Case) Flags() iter.Seq[string] {
	return slices.Values(slices.Sorted(maps.Keys(c.flags)))
}

// HasFlag reports whether the case has the flag.
func (c Case) HasFlag(flag string) bool {
	_, ok := c.flags[flag]
	return ok
}

// Section returns the value of the named section of the case, if present.
func (c Case) Section(name string) (string, bool) {
	v, ok := c.sections[name]
	return v, ok
}
