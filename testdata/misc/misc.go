// Package misc_testdata provides the test inputs in testdata/misc: inputs
// from fuzzing, plus some extra ones.
//
// Each category is a file of the directory, with one input per line, written
// with Go escapes (double quotes aren't escaped). Empty lines are ignored.
package misc_testdata

import (
	"embed"
	"fmt"
	"io/fs"
	"iter"
	"maps"
	"slices"
	"strconv"
	"strings"
)

//go:embed *.txt
var fsys embed.FS

var items = map[string][]string{} // by category

func init() {
	names, err := fs.Glob(fsys, "*.txt")
	if err != nil {
		panic(err)
	}
	if len(names) == 0 {
		panic("no inputs")
	}
	for _, name := range names {
		b, err := fs.ReadFile(fsys, name)
		if err != nil {
			panic(err)
		}
		var vs []string
		i := 0
		for line := range strings.Lines(string(b)) {
			i++
			line = strings.TrimSuffix(line, "\n")
			if line == "" {
				continue
			}
			v, err := strconv.Unquote(`"` + strings.ReplaceAll(line, `"`, `\"`) + `"`)
			if err != nil {
				panic(fmt.Errorf("%s:%d: %q: %w", name, i, line, err))
			}
			vs = append(vs, v)
		}
		items[strings.TrimSuffix(name, ".txt")] = vs
	}
}

// Categories returns the categories (e.g., "comment" and "ent"), sorted.
func Categories() iter.Seq[string] {
	return slices.Values(slices.Sorted(maps.Keys(items)))
}

// Items returns the inputs in the category, in file order. It panics if
// there is no such category.
func Items(category string) iter.Seq[string] {
	vs, ok := items[category]
	if !ok {
		panic("misc_testdata: no category " + strconv.Quote(category))
	}
	return slices.Values(vs)
}
