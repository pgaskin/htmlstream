// Package testdata contains test data for htmlstream.
//
// It is exported, but with absolutely no compatibility gurarantees whatsoever.
package testdata

import (
	"fmt"
	"iter"
	"slices"
	"strconv"
	"strings"

	html5lib_testdata "github.com/pgaskin/htmlstream/testdata/html5lib"
	html5libgo_testdata "github.com/pgaskin/htmlstream/testdata/html5libgo"
	misc_testdata "github.com/pgaskin/htmlstream/testdata/misc"
	polyglot_testdata "github.com/pgaskin/htmlstream/testdata/polyglot"
	xml_testdata "github.com/pgaskin/htmlstream/testdata/xml"
	xmltest_testdata "github.com/pgaskin/htmlstream/testdata/xmltest"
)

// VectorOptions filters the vectors returned by [Vectors].
type VectorOptions struct {
	// Suites are the names of the suites to include (see [Vectors]), or
	// empty for all of them.
	Suites []string

	// NoOutputs leaves out the expected outputs which are also inputs (the
	// out sections of the polyglot cases).
	NoOutputs bool
}

// Vectors returns the test vectors of all the suites in testdata, in order,
// with a description of the suite and case they came from, e.g.
// "misc tag.txt line 12" or "polyglot document.txtar: CommentsAfterBodyInForeign".
// The same input may be returned more than once.
//
// It is mainly intended for one-off tests to evaluate the impact of changes
// (e.g., which inputs a change to the Reader affects). Regular tests should
// use the suites directly.
//
// The suites are:
//
//   - misc: the inputs in testdata/misc
//   - xml: the in sections of the cases in testdata/xml (the tokens sections
//     aren't inputs)
//   - xmltest: every file in xmltest.zip, including the external entities,
//     canonical forms and documentation, like TestXMLTestPassthrough
//   - html5lib: the inputs of the html5lib tokenizer tests, including the
//     xmlViolation tests, with the escapes of doubleEscaped tests decoded
//     (see [html5lib_testdata.Test]), and the initial states other than the
//     data state in the description
//   - html5libgo: the in sections of the html5lib tree construction tests in
//     testdata/html5libgo (the document sections are trees, not inputs)
//   - polyglot: the in and out sections of the cases in testdata/polyglot
//     (the warn sections aren't inputs)
//
// It doesn't include the vectors in the tests themselves (e.g., extraVectors
// in reader_test.go), or the full html5lib tree construction tests used by
// internal/difffuzz, which are read from the golang.org/x/net module.
//
// Vectors panics if opt has an unknown suite.
func Vectors(opt VectorOptions) iter.Seq2[string, string] {
	for _, name := range opt.Suites {
		if !slices.ContainsFunc(suites, func(s suite) bool { return s.name == name }) {
			panic("testdata: no suite " + strconv.Quote(name))
		}
	}
	return func(yield func(string, string) bool) {
		for _, s := range suites {
			if len(opt.Suites) != 0 && !slices.Contains(opt.Suites, s.name) {
				continue
			}
			for in, desc := range s.vectors(opt) {
				if !yield(in, desc) {
					return
				}
			}
		}
	}
}

type suite struct {
	name    string
	vectors func(VectorOptions) iter.Seq2[string, string]
}

var suites = []suite{
	{"misc", miscVectors},
	{"xml", xmlVectors},
	{"xmltest", xmltestVectors},
	{"html5lib", html5libVectors},
	{"html5libgo", html5libgoVectors},
	{"polyglot", polyglotVectors},
}

func miscVectors(VectorOptions) iter.Seq2[string, string] {
	return func(yield func(string, string) bool) {
		for c := range misc_testdata.Categories() {
			for line, in := range misc_testdata.Lines(c) {
				if !yield(in, fmt.Sprintf("misc %s.txt line %d", c, line)) {
					return
				}
			}
		}
	}
}

func xmlVectors(VectorOptions) iter.Seq2[string, string] {
	return func(yield func(string, string) bool) {
		for file := range xml_testdata.Files() {
			for name, c := range xml_testdata.Cases(file, "in", "tokens") {
				in, _ := c.Section("in")
				if !yield(in, "xml "+file+": "+name) {
					return
				}
			}
		}
	}
}

func xmltestVectors(VectorOptions) iter.Seq2[string, string] {
	return func(yield func(string, string) bool) {
		for name, in := range xmltest_testdata.Files() {
			if !yield(in, "xmltest "+name) {
				return
			}
		}
	}
}

func html5libVectors(VectorOptions) iter.Seq2[string, string] {
	return func(yield func(string, string) bool) {
		for file := range html5lib_testdata.Files() {
			for t := range html5lib_testdata.Tests(file) {
				desc := "html5lib tokenizer: " + file + ": " + t.Description
				if !slices.Equal(t.InitialStates, []string{"Data state"}) {
					desc += " (" + strings.Join(t.InitialStates, ", ") + ")"
				}
				if !yield(t.Input, desc) {
					return
				}
			}
		}
	}
}

func html5libgoVectors(VectorOptions) iter.Seq2[string, string] {
	return func(yield func(string, string) bool) {
		for t := range html5libgo_testdata.TreeTests() {
			if !yield(t.Input, "html5libgo tree-construction.txtar: "+t.Name) {
				return
			}
		}
	}
}

func polyglotVectors(opt VectorOptions) iter.Seq2[string, string] {
	return func(yield func(string, string) bool) {
		for file := range polyglot_testdata.Files() {
			for name, c := range polyglot_testdata.Cases(file, "in", "out", "warn") {
				desc := "polyglot " + file + ": " + name
				in, _ := c.Section("in")
				if !yield(in, desc) {
					return
				}
				if out, ok := c.Section("out"); ok && !opt.NoOutputs {
					if !yield(out, desc+" (out)") {
						return
					}
				}
			}
		}
	}
}
