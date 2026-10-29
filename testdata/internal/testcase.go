// Package internal reads the test cases in the txtar-like files of
// testdata/polyglot and testdata/xml.
package internal

import (
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strconv"
	"strings"
)

// File is a parsed test case file.
//
// The files are like txtar archives: lines of the form "-- header --" start
// sections, and the text before the first one is a comment. Which headers
// start a case and which start a section of a case is decided by
// [File.Cases].
type File struct {
	Name     string // the name of the file, as given to ParseFile
	sections []section
}

// section is a header of a file and the text following it.
type section struct {
	fields []string // the fields of the header, at least one
	text   string
}

// Case is a test case.
type Case struct {
	Name     string
	Flags    []string
	Sections map[string]string // by section name, for the sections present
}

// ParseFile parses the test case file name with the contents data.
func ParseFile(name string, data []byte) (File, error) {
	f := File{Name: name}
	var text strings.Builder
	end := func() {
		if len(f.sections) != 0 {
			f.sections[len(f.sections)-1].text = text.String()
		}
		text.Reset()
	}
	for line := range strings.Lines(string(data)) {
		h, ok1 := strings.CutPrefix(strings.TrimSuffix(line, "\n"), "-- ")
		h, ok2 := strings.CutSuffix(h, " --")
		if !ok1 || !ok2 {
			text.WriteString(line)
			continue
		}
		end()
		fields := strings.Fields(h)
		if len(fields) == 0 {
			return File{}, fmt.Errorf("%s: empty header", name)
		}
		f.sections = append(f.sections, section{fields: fields})
	}
	end()
	return f, nil
}

// ParseFiles parses the test case files in fsys matching pattern (see
// [fs.Glob]), in the order of fs.Glob. It returns an error if none match.
func ParseFiles(fsys fs.FS, pattern string) ([]File, error) {
	names, err := fs.Glob(fsys, pattern)
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("no files match %q", pattern)
	}
	files := make([]File, 0, len(names))
	for _, name := range names {
		b, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, err
		}
		f, err := ParseFile(name, b)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, nil
}

// Cases returns the test cases in f.
//
// A header naming one of sections starts that section of the current case,
// and any other header starts a case, with its name and flags; the text of
// that section is a comment. The value of a section is its text without the
// last newline, so a value ending with a newline is followed by an empty
// line. A section whose header has "quoted" after its name holds the value
// as a Go string literal instead, for values with characters other than tab,
// newline and those strconv.IsPrint accepts (e.g., control characters and
// non-ASCII spaces), invalid UTF-8, lines ending with whitespace, or lines
// which look like headers. Every case must have the first of sections (the
// input), so that a misspelled section header, which starts a case, is an
// error.
func (f File) Cases(sections ...string) ([]Case, error) {
	var cases []Case
	for _, s := range f.sections {
		name := s.fields[0]
		if !slices.Contains(sections, name) {
			if err := f.checkCase(cases, sections); err != nil {
				return nil, err
			}
			cases = append(cases, Case{
				Name:     name,
				Flags:    slices.Clone(s.fields[1:]),
				Sections: map[string]string{},
			})
			continue
		}
		if len(cases) == 0 {
			return nil, fmt.Errorf("%s: %s section before the first case", f.Name, name)
		}
		c := &cases[len(cases)-1]
		v := strings.TrimSuffix(s.text, "\n")
		if len(s.fields) == 2 && s.fields[1] == "quoted" {
			var err error
			if v, err = strconv.Unquote(v); err != nil {
				return nil, fmt.Errorf("%s: %s: %s: %w", f.Name, c.Name, name, err)
			}
		}
		if _, ok := c.Sections[name]; ok {
			return nil, fmt.Errorf("%s: %s: duplicate %s section", f.Name, c.Name, name)
		}
		c.Sections[name] = v
	}
	if err := f.checkCase(cases, sections); err != nil {
		return nil, err
	}
	return cases, nil
}

// checkCase checks that the last case, if any, has the first of sections.
func (f File) checkCase(cases []Case, sections []string) error {
	if n := len(cases); n != 0 && len(sections) != 0 {
		if _, ok := cases[n-1].Sections[sections[0]]; !ok {
			return fmt.Errorf("%s: %s: no %s section", f.Name, cases[n-1].Name, sections[0])
		}
	}
	return nil
}

// Cases returns the test cases (see [File.Cases]) in the files matching
// pattern (see [path.Match]), in file order. It returns an error if none
// match, or if two cases have the same name.
func Cases(files []File, pattern string, sections ...string) ([]Case, error) {
	var cases []Case
	var matched bool
	names := map[string]string{} // the file of each case name
	for _, f := range files {
		ok, err := path.Match(pattern, f.Name)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		matched = true
		cs, err := f.Cases(sections...)
		if err != nil {
			return nil, err
		}
		for _, c := range cs {
			if other, ok := names[c.Name]; ok {
				return nil, fmt.Errorf("%s: %s: duplicate case name (also in %s)", f.Name, c.Name, other)
			}
			names[c.Name] = f.Name
		}
		cases = append(cases, cs...)
	}
	if !matched {
		return nil, fmt.Errorf("no files match %q", pattern)
	}
	return cases, nil
}
