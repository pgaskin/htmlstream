package difffuzz

import (
	"bytes"
	"flag"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/pgaskin/htmlstream/polyglot"
)

var corpusFlag = flag.String("corpus", "", "directories of FuzzPolyglotDOM corpus files for TestXMLLint, separated like PATH")

// TestXMLLint checks the polyglot.Convert output for the FuzzPolyglotDOM seeds
// and the corpus files in testdata/fuzz/FuzzPolyglotDOM and the directories
// in -corpus with xmllint --noout, which also checks the namespace
// constraints. Outputs with a warning for a script type, which aren't
// well-formed, are left out. Messages of xmllint which are only warnings
// (e.g., an xml:space value other than default or preserve) are logged. It
// is skipped if xmllint isn't installed.
func TestXMLLint(t *testing.T) {
	xmllint, err := exec.LookPath("xmllint")
	if err != nil {
		t.Skip("xmllint not found")
	}
	var ins []string
	for _, tc := range slices.Concat(polyglotCases("*.txtar"), polyglotLongTests) {
		ins = append(ins, tc.In)
	}
	html5lib, err := html5libTreeInputsOnce()
	if err != nil {
		t.Logf("no html5lib tree construction inputs: %v", err)
	}
	ins = append(ins, html5lib...)
	for _, dir := range slices.Concat([]string{filepath.Join("testdata", "fuzz", "FuzzPolyglotDOM")}, filepath.SplitList(*corpusFlag)) {
		files, err := filepath.Glob(filepath.Join(dir, "*"))
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			b, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			in, ok, err := corpusString(b)
			if err != nil || !ok {
				t.Errorf("%s: not a fuzz corpus file: %v", file, err)
				continue
			}
			ins = append(ins, in)
		}
	}

	dir := t.TempDir()
	var files []string
	var fileIns, fileWarns []string
	for _, in := range ins {
		if !utf8.ValidString(in) {
			continue
		}
		var b bytes.Buffer
		var warns []string
		err := polyglot.Convert(&b, strings.NewReader(in), polyglot.Options{Warn: func(w polyglot.Warning) {
			warns = append(warns, fmt.Sprintf("%d: %s", w.Pos, w.Msg))
		}})
		if err != nil || slices.ContainsFunc(warns, isScriptTypeWarning) {
			continue
		}
		file := filepath.Join(dir, strconv.Itoa(len(files))+".xhtml")
		if err := os.WriteFile(file, b.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		files = append(files, file)
		fileIns = append(fileIns, in)
		fileWarns = append(fileWarns, strings.Join(warns, "\n"))
	}

	// xmllint prints the messages for a file on lines starting with its
	// name, each followed by lines of context
	cmd := exec.Command(xmllint, append([]string{"--noout"}, files...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err = cmd.Run()
	if _, ok := err.(*exec.ExitError); err != nil && !ok {
		t.Fatal(err)
	}
	msgs := map[int][]string{}
	errs := map[int]bool{} // outputs with an error, not only warnings
	cur := -1
	for line := range strings.Lines(stderr.String()) {
		if rest, ok := strings.CutPrefix(line, dir+string(filepath.Separator)); ok {
			if n, _, ok := strings.Cut(rest, ".xhtml:"); ok {
				if i, err := strconv.Atoi(n); err == nil && i < len(files) {
					cur = i
					errs[i] = errs[i] || !strings.Contains(line, " warning : ")
				}
			}
		}
		if cur < 0 {
			t.Errorf("xmllint: %s", line)
			continue
		}
		msgs[cur] = append(msgs[cur], strings.TrimPrefix(strings.TrimSuffix(line, "\n"), dir+string(filepath.Separator)))
	}
	for _, i := range slices.Sorted(maps.Keys(msgs)) {
		report := t.Logf
		if errs[i] {
			report = t.Errorf
		}
		report("%q: xmllint:\n%s\nwarnings:\n%s", fileIns[i], strings.Join(msgs[i], "\n"), fileWarns[i])
	}
	if err != nil && !t.Failed() {
		t.Errorf("xmllint: %v", err)
	}
	t.Logf("%d outputs checked", len(files))
}
