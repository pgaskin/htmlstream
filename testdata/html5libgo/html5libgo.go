// Package html5libgo_testdata provides a selection of the tree construction
// tests of html5lib-tests (https://github.com/html5lib/html5lib-tests), from
// the copy in golang.org/x/net v0.60.0 (html/testdata/html5lib-tests/), in
// testdata/html5libgo/tree-construction.txtar, under the license in
// testdata/html5libgo/LICENSE: the tests without parse errors (see
// [TreeTests]).
package html5libgo_testdata

import (
	"embed"
	"fmt"
	"iter"
	"slices"

	"github.com/pgaskin/htmlstream/testdata/internal"
)

//go:embed tree-construction.txtar
var fsys embed.FS

var treeTests []TreeTest

func init() {
	files, err := internal.ParseFiles(fsys, "tree-construction.txtar")
	if err != nil {
		panic(err)
	}
	cs, err := internal.Cases(files, "*", "in", "document")
	if err != nil {
		panic(err)
	}
	for _, c := range cs {
		t := TreeTest{Name: c.Name, Input: c.Sections["in"], Document: c.Sections["document"]}
		for _, flag := range c.Flags {
			switch flag {
			case "script-on":
				t.Scripting = "on"
			case "script-off":
				t.Scripting = "off"
			default:
				panic(fmt.Errorf("tree-construction.txtar: %s: unknown flag %q", c.Name, flag))
			}
		}
		treeTests = append(treeTests, t)
	}
	if len(treeTests) == 0 {
		panic("no tree construction tests")
	}
}

// TreeTest is a tree construction test without parse errors: one of the
// tests of tree-construction/*.dat whose #errors and #new-errors sections are
// empty, other than #document-fragment tests and tests without a doctype (see
// the comment at the start of tree-construction.txtar).
type TreeTest struct {
	Name      string // the file and the number of the test in it, e.g. "blocks/2"
	Input     string // the #data section
	Document  string // the #document section, unchanged
	Scripting string // "on" or "off" for #script-on and #script-off tests, otherwise empty
}

// TreeTests returns the tree construction tests without parse errors, in
// file order.
func TreeTests() iter.Seq[TreeTest] {
	return slices.Values(treeTests)
}
