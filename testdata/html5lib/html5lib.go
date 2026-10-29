// Package html5lib_testdata provides the tokenizer tests of html5lib-tests
// (https://github.com/html5lib/html5lib-tests at commit
// c777c408b61078ea2eb4acefc2535f54dbc8b28a), in testdata/html5lib/tokenizer,
// under the license in testdata/html5lib/LICENSE.
//
// See tokenizer/README.md in html5lib-tests for the format.
package html5lib_testdata

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"iter"
	"path"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

//go:embed tokenizer/*.test
var fsys embed.FS

var (
	files []string          // sorted
	tests map[string][]Test // by file
)

func init() {
	names, err := fs.Glob(fsys, "tokenizer/*.test")
	if err != nil {
		panic(err)
	}
	if len(names) == 0 {
		panic("no tests")
	}
	tests = make(map[string][]Test, len(names))
	for _, name := range names {
		b, err := fs.ReadFile(fsys, name)
		if err != nil {
			panic(err)
		}
		file := path.Base(name)
		ts, err := parseFile(file, b)
		if err != nil {
			panic(fmt.Errorf("%s: %w", name, err))
		}
		files = append(files, file)
		tests[file] = ts
	}
}

// Test is a tokenizer test.
type Test struct {
	File        string // the name of the test file, e.g. "test1.test"
	Description string
	Input       string // with the escapes of doubleEscaped tests decoded

	// InitialStates are the names of the tokenizer states to run the test
	// in, e.g. "Data state" or "RCDATA state" (only "Data state" if the
	// test doesn't list any).
	InitialStates []string

	// LastStartTag is the tag name of the last start tag, which decides
	// whether an end tag is appropriate in the RCDATA, RAWTEXT and script
	// data states, or empty if there is none.
	LastStartTag string

	// Output is the expected tokens, with adjacent Character tokens merged.
	Output []Token

	// Errors is the expected parse errors.
	Errors []Error

	// XMLViolation is set for the tests of xmlViolation.test, whose output
	// has the changes of "Coercing an HTML DOM into an infoset" in the HTML
	// standard (e.g., U+FFFF replaced and "--" in comments changed), so
	// tokenizers without them don't match it.
	XMLViolation bool
}

// Token is an expected token.
type Token struct {
	// Kind is "DOCTYPE", "StartTag", "EndTag", "Comment", "Character" or
	// "ProcessingInstruction".
	Kind string

	// Name is the name of a DOCTYPE, StartTag or EndTag, or the target of
	// a ProcessingInstruction.
	Name string

	// Data is the data of a Comment, Character or ProcessingInstruction.
	Data string

	// Attrs is the attributes of a StartTag, in the order of the test file
	// (the source order, without duplicates).
	Attrs []Attr

	// SelfClosing is the self-closing flag of a StartTag.
	SelfClosing bool

	// PublicID and SystemID are the identifiers of a DOCTYPE.
	PublicID, SystemID string

	// NameMissing, PublicIDMissing and SystemIDMissing report whether the
	// name or an identifier of a DOCTYPE is missing (null in the test
	// file), which is distinct from empty.
	NameMissing, PublicIDMissing, SystemIDMissing bool

	// ForceQuirks is the force-quirks flag of a DOCTYPE (the inverse of
	// its correctness in the test file).
	ForceQuirks bool
}

// Attr is an attribute of a StartTag.
type Attr struct {
	Name, Value string
}

// Error is an expected parse error.
type Error struct {
	Code      string // e.g. "eof-in-tag"
	Line, Col int
}

// String returns the token on one line, with the strings quoted, for
// comparisons and messages, e.g. `StartTag "a" {"href": "x"} self-closing`.
// Missing DOCTYPE strings are written as null.
func (t Token) String() string {
	q := func(s string, missing bool) string {
		if missing {
			return "null"
		}
		return strconv.Quote(s)
	}
	switch t.Kind {
	case "DOCTYPE":
		s := fmt.Sprintf("DOCTYPE %s %s %s", q(t.Name, t.NameMissing), q(t.PublicID, t.PublicIDMissing), q(t.SystemID, t.SystemIDMissing))
		if t.ForceQuirks {
			s += " force-quirks"
		}
		return s
	case "StartTag":
		var b strings.Builder
		fmt.Fprintf(&b, "StartTag %q {", t.Name)
		for i, a := range t.Attrs {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%q: %q", a.Name, a.Value)
		}
		b.WriteString("}")
		if t.SelfClosing {
			b.WriteString(" self-closing")
		}
		return b.String()
	case "EndTag":
		return "EndTag " + strconv.Quote(t.Name)
	case "ProcessingInstruction":
		return fmt.Sprintf("ProcessingInstruction %q %q", t.Name, t.Data)
	}
	return t.Kind + " " + strconv.Quote(t.Data)
}

// Files returns the names of the test files, e.g. "test1.test", sorted.
func Files() iter.Seq[string] {
	return slices.Values(files)
}

// Tests returns the tests in the named test file (see [Files]), in file
// order. Each Test is a copy. It panics if there is no such file.
func Tests(file string) iter.Seq[Test] {
	ts, ok := tests[file]
	if !ok {
		panic("html5lib_testdata: no test file " + strconv.Quote(file))
	}
	return func(yield func(Test) bool) {
		for _, t := range ts {
			t.InitialStates = slices.Clone(t.InitialStates)
			t.Output = slices.Clone(t.Output)
			for i := range t.Output {
				t.Output[i].Attrs = slices.Clone(t.Output[i].Attrs)
			}
			t.Errors = slices.Clone(t.Errors)
			if !yield(t) {
				return
			}
		}
	}
}

// parseFile parses a test file.
func parseFile(file string, b []byte) ([]Test, error) {
	type jsonTest struct {
		Description   string            `json:"description"`
		Input         string            `json:"input"`
		Output        []json.RawMessage `json:"output"`
		InitialStates []string          `json:"initialStates"`
		LastStartTag  string            `json:"lastStartTag"`
		DoubleEscaped bool              `json:"doubleEscaped"`
		Errors        []Error           `json:"errors"`
	}
	var suite struct {
		Tests             []jsonTest `json:"tests"`
		XMLViolationTests []jsonTest `json:"xmlViolationTests"`
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(&suite); err != nil {
		return nil, err
	}
	if len(suite.Tests) == 0 && len(suite.XMLViolationTests) == 0 {
		return nil, fmt.Errorf("no tests")
	}
	var ts []Test
	for i, list := range [][]jsonTest{suite.Tests, suite.XMLViolationTests} {
		for _, jt := range list {
			t := Test{
				File:          file,
				Description:   jt.Description,
				Input:         jt.Input,
				InitialStates: jt.InitialStates,
				LastStartTag:  jt.LastStartTag,
				Errors:        jt.Errors,
				XMLViolation:  i == 1,
			}
			if jt.DoubleEscaped {
				t.Input = unescape(t.Input)
			}
			if len(t.InitialStates) == 0 {
				t.InitialStates = []string{"Data state"}
			}
			for _, m := range jt.Output {
				tok, err := parseToken(m, jt.DoubleEscaped)
				if err != nil {
					return nil, fmt.Errorf("%q: %w", jt.Description, err)
				}
				if n := len(t.Output); tok.Kind == "Character" && n > 0 && t.Output[n-1].Kind == "Character" {
					t.Output[n-1].Data += tok.Data
					continue
				}
				t.Output = append(t.Output, tok)
			}
			ts = append(ts, t)
		}
	}
	return ts, nil
}

// parseToken parses an expected token, an array with the kind followed by
// its fields. If doubleEscaped, the \uHHHH escapes in its strings are
// decoded.
func parseToken(m json.RawMessage, doubleEscaped bool) (Token, error) {
	var parts []json.RawMessage
	if err := json.Unmarshal(m, &parts); err != nil {
		return Token{}, err
	}
	str := func(i int) (s string, missing bool, err error) {
		var p *string
		if err := json.Unmarshal(parts[i], &p); err != nil {
			return "", false, fmt.Errorf("bad token %s: %w", m, err)
		}
		if p == nil {
			return "", true, nil
		}
		if doubleEscaped {
			return unescape(*p), false, nil
		}
		return *p, false, nil
	}
	nonNull := func(i int) (string, error) {
		s, missing, err := str(i)
		if err == nil && missing {
			err = fmt.Errorf("bad token %s: null", m)
		}
		return s, err
	}
	if len(parts) < 2 {
		return Token{}, fmt.Errorf("short token %s", m)
	}
	var tok Token
	if err := json.Unmarshal(parts[0], &tok.Kind); err != nil {
		return Token{}, err
	}
	var err error
	switch tok.Kind {
	case "DOCTYPE":
		if len(parts) != 5 {
			return Token{}, fmt.Errorf("bad doctype %s", m)
		}
		if tok.Name, tok.NameMissing, err = str(1); err != nil {
			return Token{}, err
		}
		if tok.PublicID, tok.PublicIDMissing, err = str(2); err != nil {
			return Token{}, err
		}
		if tok.SystemID, tok.SystemIDMissing, err = str(3); err != nil {
			return Token{}, err
		}
		var correct bool
		if err := json.Unmarshal(parts[4], &correct); err != nil {
			return Token{}, err
		}
		tok.ForceQuirks = !correct
	case "StartTag":
		if len(parts) > 4 {
			return Token{}, fmt.Errorf("bad start tag %s", m)
		}
		if tok.Name, err = nonNull(1); err != nil {
			return Token{}, err
		}
		if len(parts) > 2 {
			if tok.Attrs, err = parseAttrs(parts[2], doubleEscaped); err != nil {
				return Token{}, fmt.Errorf("bad attributes %s: %w", m, err)
			}
		}
		if len(parts) > 3 {
			if err := json.Unmarshal(parts[3], &tok.SelfClosing); err != nil {
				return Token{}, err
			}
		}
	case "EndTag":
		if len(parts) != 2 {
			return Token{}, fmt.Errorf("bad end tag %s", m)
		}
		if tok.Name, err = nonNull(1); err != nil {
			return Token{}, err
		}
	case "ProcessingInstruction":
		if len(parts) != 3 {
			return Token{}, fmt.Errorf("bad processing instruction %s", m)
		}
		if tok.Name, err = nonNull(1); err != nil {
			return Token{}, err
		}
		if tok.Data, err = nonNull(2); err != nil {
			return Token{}, err
		}
	case "Comment", "Character":
		if len(parts) != 2 {
			return Token{}, fmt.Errorf("bad token %s", m)
		}
		if tok.Data, err = nonNull(1); err != nil {
			return Token{}, err
		}
	default:
		return Token{}, fmt.Errorf("unknown token kind %q", tok.Kind)
	}
	return tok, nil
}

// parseAttrs parses the attributes of a start tag, a JSON object, keeping
// their order. If doubleEscaped, the \uHHHH escapes in the names and values
// are decoded.
func parseAttrs(m json.RawMessage, doubleEscaped bool) ([]Attr, error) {
	d := json.NewDecoder(bytes.NewReader(m))
	if t, err := d.Token(); err != nil {
		return nil, err
	} else if t != json.Delim('{') {
		return nil, fmt.Errorf("not an object")
	}
	var attrs []Attr
	for d.More() {
		var a Attr
		t, err := d.Token()
		if err != nil {
			return nil, err
		}
		a.Name = t.(string) // object keys are strings
		if err := d.Decode(&a.Value); err != nil {
			return nil, err
		}
		if doubleEscaped {
			a.Name, a.Value = unescape(a.Name), unescape(a.Value)
		}
		if slices.ContainsFunc(attrs, func(x Attr) bool { return x.Name == a.Name }) {
			return nil, fmt.Errorf("duplicate attribute %q", a.Name)
		}
		attrs = append(attrs, a)
	}
	return attrs, nil
}

// unescape replaces the \uHHHH escapes in s, for tests with doubleEscaped
// set. Surrogates which are not part of a pair are encoded like other code
// points (as in WTF-8), so they pass through a tokenizer as opaque bytes.
func unescape(s string) string {
	hex := func(i int) (rune, bool) {
		if i+6 > len(s) || s[i] != '\\' || s[i+1] != 'u' {
			return 0, false
		}
		v, err := strconv.ParseUint(s[i+2:i+6], 16, 16)
		return rune(v), err == nil
	}
	var b []byte
	for i := 0; i < len(s); {
		c, ok := hex(i)
		if !ok {
			b = append(b, s[i])
			i++
			continue
		}
		i += 6
		if 0xD800 <= c && c <= 0xDBFF {
			if d, ok := hex(i); ok && 0xDC00 <= d && d <= 0xDFFF {
				c = 0x10000 + (c-0xD800)<<10 + (d - 0xDC00)
				i += 6
			}
		}
		if 0xD800 <= c && c <= 0xDFFF {
			b = append(b, 0xE0|byte(c>>12), 0x80|byte(c>>6)&0x3F, 0x80|byte(c)&0x3F)
		} else {
			b = utf8.AppendRune(b, c)
		}
	}
	return string(b)
}
