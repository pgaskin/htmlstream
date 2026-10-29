package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/pgaskin/htmlstream"
	"github.com/pgaskin/htmlstream/polyglot"
)

const (
	htmlStart = `<html xmlns="http://www.w3.org/1999/xhtml"><head><meta charset="UTF-8" /></head><body>`
	htmlEnd   = `</body></html>`
	xhtml1    = `<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Strict//EN" "http://www.w3.org/TR/xhtml1/DTD/xhtml1-strict.dtd">`
)

func runCmd(t *testing.T, stdin string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var o, e bytes.Buffer
	code = run("polyglottify", args, strings.NewReader(stdin), &o, &e)
	return o.String(), e.String(), code
}

func TestUsageErrors(t *testing.T) {
	for _, tc := range []struct {
		args []string
		msg  string
	}{
		{[]string{"a", "b"}, "polyglottify: more than one file without -i\n"},
		{[]string{"-", "-"}, "polyglottify: more than one file without -i\n"},
		{[]string{"-i"}, "polyglottify: no files to convert in place\n"},
		{[]string{"-i", "-"}, "polyglottify: can't convert standard input in place\n"},
		{[]string{"-i", "a", "-"}, "polyglottify: can't convert standard input in place\n"},
		{[]string{"-doctype", "foo"}, `polyglottify: invalid -doctype "foo": must be default, keep, none, or a doctype declaration` + "\n"},
		{[]string{"-doctype", "<!DOCTYPE html"}, `polyglottify: invalid -doctype "<!DOCTYPE html": must be default, keep, none, or a doctype declaration` + "\n"},
		{[]string{"-doctype", "<p>"}, `polyglottify: invalid -doctype "<p>": not a doctype declaration` + "\n"},
		{[]string{"-doctype", "<!doctype html>"}, `polyglottify: invalid -doctype "<!doctype html>": not written by -doctype keep as given ("<!DOCTYPE html>")` + "\n"},
		{[]string{"-doctype", "<!DOCTYPE html><!-- -->"}, `polyglottify: invalid -doctype "<!DOCTYPE html><!-- -->": not a single doctype declaration` + "\n"},
		{[]string{"-doctype", "<!DOCTYPE html><p>"}, `polyglottify: invalid -doctype "<!DOCTYPE html><p>": not a single doctype declaration` + "\n"},
		{[]string{"-doctype", "<!DOCTYPE 1>"}, `polyglottify: invalid -doctype "<!DOCTYPE 1>": name "1" isn't an XML name` + "\n"},
		{[]string{"-doctype", `<!DOCTYPE html PUBLIC "-//W3C//DTD HTML 4.01 Transitional//EN">`}, "selects quirks mode"},
		{[]string{"-xmldecl", "yes"}, `polyglottify: invalid -xmldecl "yes": must be none, add or keep` + "\n"},
		{[]string{"-max-depth", "0"}, "polyglottify: invalid -max-depth 0: must be positive\n"},
		{[]string{"-max-token-size", "-1"}, "polyglottify: invalid -max-token-size -1: must be positive\n"},
		{[]string{"-max-stack-bytes", "0"}, "polyglottify: invalid -max-stack-bytes 0: must be positive\n"},
		{[]string{"-max-raw-text", "0"}, "polyglottify: invalid -max-raw-text 0: must be positive\n"},
		{[]string{"-max-depth", "x"}, `invalid value "x" for flag -max-depth`},
		{[]string{"-nosuch"}, "flag provided but not defined: -nosuch\n"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			stdout, stderr, code := runCmd(t, "", tc.args...)
			if code != 2 {
				t.Errorf("exit status %d, want 2", code)
			}
			if stdout != "" {
				t.Errorf("stdout %q, want none", stdout)
			}
			if !strings.Contains(stderr, tc.msg) {
				t.Errorf("stderr %q doesn't contain %q", stderr, tc.msg)
			}
			if !strings.Contains(stderr, "\nusage: polyglottify [flags] [file|-]...\n") {
				t.Errorf("stderr %q doesn't have the usage after the error", stderr)
			}
		})
	}
}

func TestHelp(t *testing.T) {
	stdout, stderr, code := runCmd(t, "", "-h")
	if code != 0 || stdout != "" || !strings.HasPrefix(stderr, "usage: polyglottify [flags] [file|-]...\n") {
		t.Errorf("got %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	for _, f := range []string{"-doctype", "-xmldecl", "-strict", "-quiet", "-i", "-xml-syntax", "-scripting", "-max-token-size", "-max-depth", "-max-stack-bytes", "-max-raw-text"} {
		if !strings.Contains(stderr, "\n  "+f) {
			t.Errorf("usage doesn't have %s", f)
		}
	}
	for _, s := range []string{"to use (none, add, keep)", "keeping their contents", "-xmldecl keep"} {
		if !strings.Contains(stderr, s) {
			t.Errorf("usage doesn't have %q", s)
		}
	}
}

func TestDoctype(t *testing.T) {
	const in = xhtml1 + "<p>x"
	const body = htmlStart + "<p>x</p>" + htmlEnd
	for _, tc := range []struct {
		args []string
		out  string
	}{
		{nil, "<!DOCTYPE html>" + body},
		{[]string{"-doctype", ""}, "<!DOCTYPE html>" + body},
		{[]string{"-doctype", "default"}, "<!DOCTYPE html>" + body},
		{[]string{"-doctype", "keep"}, xhtml1 + body},
		{[]string{"-doctype", "none"}, body},
		{[]string{"-doctype", `<!DOCTYPE html SYSTEM "about:legacy-compat">`}, `<!DOCTYPE html SYSTEM "about:legacy-compat">` + body},
		{[]string{"-doctype", xhtml1}, xhtml1 + body},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			stdout, stderr, code := runCmd(t, in, tc.args...)
			if code != 0 || stderr != "" {
				t.Fatalf("exit status %d, stderr %q", code, stderr)
			}
			if stdout != tc.out {
				t.Errorf("got\n\t%s\nwant\n\t%s", stdout, tc.out)
			}
		})
	}
}

func TestXMLDecl(t *testing.T) {
	const decl = `<?xml version="1.0" encoding="UTF-8"?>`
	const out = "<!DOCTYPE html>" + htmlStart + "<p>x</p>" + htmlEnd
	for _, tc := range []struct {
		args []string
		in   string
		out  string
	}{
		{nil, `<?xml version="1.0"?><p>x`, out},
		{[]string{"-xmldecl", "none"}, `<?xml version="1.0"?><p>x`, out},
		{[]string{"-xmldecl", "add"}, `<p>x`, decl + out},
		{[]string{"-xmldecl", "keep"}, `<?xml version="1.0"?><p>x`, decl + out},
		{[]string{"-xmldecl", "keep"}, `<p>x`, out},
	} {
		t.Run(strings.Join(tc.args, " ")+" "+tc.in, func(t *testing.T) {
			stdout, stderr, code := runCmd(t, tc.in, tc.args...)
			if code != 0 || stderr != "" {
				t.Fatalf("exit status %d, stderr %q", code, stderr)
			}
			if stdout != tc.out {
				t.Errorf("got\n\t%s\nwant\n\t%s", stdout, tc.out)
			}
		})
	}
}

func TestStrict(t *testing.T) {
	const in = "<p>a\n<p>x<b></p>"
	if _, stderr, code := runCmd(t, in); code != 0 || stderr != "" {
		t.Errorf("without -strict: exit status %d, stderr %q", code, stderr)
	}
	_, stderr, code := runCmd(t, in, "-strict")
	if code != 1 {
		t.Errorf("exit status %d, want 1", code)
	}
	if want := "polyglottify: stdin:2:8: element \"b\" at offset 9 closed without its end tag\n"; stderr != want {
		t.Errorf("stderr %q, want %q", stderr, want)
	}
}

func TestWarnings(t *testing.T) {
	for _, tc := range []struct {
		name, in, pos string
	}{
		{"first line", "<table>y</table>", "stdin:1:8"},
		{"LF", "x\n\n<table>y</table>", "stdin:3:8"},
		{"CRLF", "x\r\n\r\n<table>y</table>", "stdin:3:8"},
		{"CR", "x\r\r<table>y</table>", "stdin:3:8"},
		{"LF CR", "x\n\r<table>y</table>", "stdin:3:8"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, code := runCmd(t, tc.in)
			if code != 0 {
				t.Fatalf("exit status %d, stderr %q", code, stderr)
			}
			if want := tc.pos + ": kept text in a table outside a cell (HTML parsers would have moved it before the table)\n"; stderr != want {
				t.Errorf("stderr %q, want %q", stderr, want)
			}
			quietOut, quietErr, code := runCmd(t, tc.in, "-quiet")
			if code != 0 || quietErr != "" {
				t.Errorf("-quiet: exit status %d, stderr %q", code, quietErr)
			}
			if quietOut != stdout {
				t.Errorf("-quiet changed the output: %q, want %q", quietOut, stdout)
			}
		})
	}
}

func TestPosition(t *testing.T) {
	const in = "ab\ncd\r\nef\rg\r\r\n\nh"
	for _, tc := range []struct {
		pos  htmlstream.Pos
		want string
	}{
		{0, "f:1:1"},
		{2, "f:1:3"},
		{3, "f:2:1"},
		{4, "f:2:2"},
		{5, "f:2:3"},
		{6, "f:2:4"},
		{7, "f:3:1"},
		{10, "f:4:1"},
		{11, "f:4:2"},
		{12, "f:5:1"},
		{13, "f:5:2"},
		{14, "f:6:1"},
		{15, "f:7:1"},
		{16, "f:7:2"},
	} {
		// one byte at a time, so a CRLF is split across reads
		lr := &lineReader{r: iotest.OneByteReader(strings.NewReader(in))}
		if _, err := io.Copy(io.Discard, lr); err != nil {
			t.Fatal(err)
		}
		if got := lr.position("f", tc.pos); got != tc.want {
			t.Errorf("position %d: got %s, want %s", tc.pos, got, tc.want)
		}
	}
}

func TestPositionManyLines(t *testing.T) {
	// The line starts kept are bounded, and positions before them are
	// reported as offsets.
	n := 3*maxLines + 10
	in := strings.Repeat("a\r\n", n) + "b"
	lr := &lineReader{r: strings.NewReader(in)}
	if _, err := io.Copy(io.Discard, lr); err != nil {
		t.Fatal(err)
	}
	if len(lr.lines) > 2*maxLines {
		t.Errorf("%d line starts kept", len(lr.lines))
	}
	for _, tc := range []struct {
		pos  int
		want string
	}{
		{3 * n, fmt.Sprintf("f:%d:1", n+1)},
		{3*(n-maxLines) + 1, fmt.Sprintf("f:%d:2", n-maxLines+1)},
		{1, "f: byte 1"},
	} {
		if got := lr.position("f", htmlstream.Pos(tc.pos)); got != tc.want {
			t.Errorf("position %d: got %s, want %s", tc.pos, got, tc.want)
		}
	}
}

func TestStdin(t *testing.T) {
	const want = "<!DOCTYPE html>" + htmlStart + "<p>x</p>" + htmlEnd
	for _, args := range [][]string{nil, {"-"}, {"-strict", "-"}} {
		stdout, stderr, code := runCmd(t, "<p>x", args...)
		if code != 0 || stderr != "" || stdout != want {
			t.Errorf("%q: got %d, stdout %q, stderr %q", args, code, stdout, stderr)
		}
	}
}

func TestFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.html")
	writeFile(t, file, "<p>a\n<p>x<b></p>", 0o644)

	stdout, stderr, code := runCmd(t, "<p>stdin", file)
	if code != 0 {
		t.Fatalf("exit status %d, stderr %q", code, stderr)
	}
	if want := "<!DOCTYPE html>" + htmlStart + "<p>a\n</p><p>x<b></b></p>" + htmlEnd; stdout != want {
		t.Errorf("got\n\t%s\nwant\n\t%s", stdout, want)
	}
	if got := readFile(t, file); got != "<p>a\n<p>x<b></p>" {
		t.Errorf("file changed to %q", got)
	}

	_, stderr, code = runCmd(t, "", "-strict", file)
	if want := "polyglottify: " + file + ":2:8: element \"b\" at offset 9 closed without its end tag\n"; code != 1 || stderr != want {
		t.Errorf("-strict: exit status %d, stderr %q, want 1 and %q", code, stderr, want)
	}

	missing := filepath.Join(dir, "missing.html")
	_, stderr, code = runCmd(t, "", missing)
	if want := "polyglottify: open " + missing + ": no such file or directory\n"; code != 1 || stderr != want {
		t.Errorf("missing file: exit status %d, stderr %q, want 1 and %q", code, stderr, want)
	}
}

func TestInPlace(t *testing.T) {
	dir := t.TempDir()
	files := []struct {
		name string
		in   string
		perm os.FileMode
		out  string
	}{
		{"a.html", "<p>a", 0o644, htmlStart + "<p>a</p>" + htmlEnd},
		{"b.html", "<br>", 0o600, htmlStart + "<br />" + htmlEnd},
		{"c.xhtml", "<P CLASS=x>c", 0o751, htmlStart + `<p class="x">c</p>` + htmlEnd},
	}
	var args []string
	for _, f := range files {
		file := filepath.Join(dir, f.name)
		writeFile(t, file, f.in, f.perm)
		args = append(args, file)
	}
	stdout, stderr, code := runCmd(t, "<p>stdin", append([]string{"-i", "-doctype", "none"}, args...)...)
	if code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("exit status %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	for _, f := range files {
		file := filepath.Join(dir, f.name)
		if got := readFile(t, file); got != f.out {
			t.Errorf("%s: got %q, want %q", f.name, got, f.out)
		}
		if perm := stat(t, file).Mode().Perm(); perm != f.perm {
			t.Errorf("%s: permissions %v, want %v", f.name, perm, f.perm)
		}
	}
	checkDir(t, dir, "a.html", "b.html", "c.xhtml")
}

func TestInPlaceSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.html")
	link := filepath.Join(dir, "link.html")
	writeFile(t, target, "<p>a", 0o640)
	if err := os.Symlink("target.html", link); err != nil {
		t.Skip(err)
	}
	if _, stderr, code := runCmd(t, "", "-i", "-doctype", "none", link); code != 0 {
		t.Fatalf("exit status %d, stderr %q", code, stderr)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("link replaced: %v, %v", fi.Mode(), err)
	}
	if got, want := readFile(t, target), htmlStart+"<p>a</p>"+htmlEnd; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if perm := stat(t, target).Mode().Perm(); perm != 0o640 {
		t.Errorf("permissions %v, want 0640", perm)
	}
	checkDir(t, dir, "link.html", "target.html")
}

func TestInPlaceFailure(t *testing.T) {
	dir := t.TempDir()
	good1 := filepath.Join(dir, "good1.html")
	bad := filepath.Join(dir, "bad.html")
	good2 := filepath.Join(dir, "good2.html")
	missing := filepath.Join(dir, "missing.html")
	sub := filepath.Join(dir, "sub")
	writeFile(t, good1, "<p>1", 0o644)
	writeFile(t, bad, "<p>x\n<b></p>", 0o600)
	writeFile(t, good2, "<p>2", 0o644)
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runCmd(t, "", "-i", "-strict", "-doctype", "none", good1, bad, missing, sub, good2)
	if code != 1 || stdout != "" {
		t.Errorf("exit status %d, stdout %q, want 1 and none", code, stdout)
	}
	lines := strings.Split(strings.TrimSuffix(stderr, "\n"), "\n")
	if len(lines) != 3 ||
		lines[0] != "polyglottify: "+bad+":2:4: element \"b\" at offset 5 closed without its end tag" ||
		lines[1] != "polyglottify: lstat "+missing+": no such file or directory" ||
		lines[2] != "polyglottify: "+sub+": not a regular file" {
		t.Errorf("stderr:\n%s", stderr)
	}
	if got := readFile(t, bad); got != "<p>x\n<b></p>" {
		t.Errorf("bad.html changed to %q", got)
	}
	if perm := stat(t, bad).Mode().Perm(); perm != 0o600 {
		t.Errorf("bad.html permissions %v, want 0600", perm)
	}
	for i, file := range []string{good1, good2} {
		if got, want := readFile(t, file), fmt.Sprintf("%s<p>%d</p>%s", htmlStart, i+1, htmlEnd); got != want {
			t.Errorf("%s: got %q, want %q", file, got, want)
		}
	}
	checkDir(t, dir, "bad.html", "good1.html", "good2.html", "sub")
}

func TestReaderFlags(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		in   string
		out  string // body contents, or the error
		code int
		warn string
	}{
		{"xml-syntax off", nil, `<span/>a<![CDATA[<b>]]>`, `<span>a<!--[CDATA[<b-->]]&gt;</span>`, 0, ""},
		{"xml-syntax", []string{"-xml-syntax"}, `<span/>a<![CDATA[<b>]]><?pi x?>`, `<span></span>a&lt;b&gt;<!--?pi x?-->`, 0, ""},
		{"scripting off", nil, `<p><noscript><i>a</i></noscript>`, `<p><noscript><i>a</i></noscript></p>`, 0, ""},
		{"scripting", []string{"-scripting"}, `<p><noscript><i>a</i></noscript>`, `<p><noscript>&lt;i&gt;a&lt;/i&gt;</noscript></p>`, 0, "stdin:1:14: escaped raw text noscript contents"},
		{"max-depth", []string{"-max-depth", "4"}, `<html><body><div><div><p>x</div></div>`, `<div><div>x</div></div>`, 0, "stdin:1:23: dropped p nested past the stack limits (MaxDepth or MaxStackBytes)\n"},
		{"max-depth output", []string{"-max-depth", "4"}, `<div><b></div>x<div><b></div>y`, `<div><b></b></div><b>x<div></div>y</b>`, 0, "stdin:1:21: dropped b nested past the output depth limit (Options.MaxDepth)\n"},
		{"max-stack-bytes", []string{"-max-stack-bytes", "3"}, `<div><p>x</div>`, `<div>x</div>`, 0, "stdin:1:6: dropped p nested past the stack limits (MaxDepth or MaxStackBytes)\n"},
		{"max-token-size", []string{"-max-token-size", "100"}, `<p class="` + strings.Repeat("a", 200) + `">x`, "polyglottify: stdin: htmlstream: token too large (MaxTokenSize is 100 bytes)\n", 1, ""},
		{"max-token-size ok", []string{"-max-token-size", "300"}, `<p class="` + strings.Repeat("a", 200) + `">x`, `<p class="` + strings.Repeat("a", 200) + `">x</p>`, 0, ""},
		{"max-raw-text", []string{"-max-raw-text", "5"}, `<script>if(a<b)x()</script>`, "polyglottify: stdin:1:9: script contents exceed MaxRawText (5 bytes)\n", 1, ""},
		{"strict script type", []string{"-strict"}, "<p>\n<script type=x>a<b</script>", "polyglottify: stdin:2:16: script of type \"x\" containing <, & or ]]> can't be represented in XML\n", 1, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, code := runCmd(t, tc.in, append([]string{"-doctype", "none"}, tc.args...)...)
			if code != tc.code {
				t.Errorf("exit status %d, want %d (stderr %q)", code, tc.code, stderr)
			}
			if tc.code != 0 {
				if stderr != tc.out {
					t.Errorf("stderr %q, want %q", stderr, tc.out)
				}
				return
			}
			if !strings.HasPrefix(stdout, htmlStart) || !strings.HasSuffix(stdout, htmlEnd) {
				t.Fatalf("stdout %q", stdout)
			}
			if got := stdout[len(htmlStart) : len(stdout)-len(htmlEnd)]; got != tc.out {
				t.Errorf("got\n\t%s\nwant\n\t%s", got, tc.out)
			}
			if (tc.warn == "") != (stderr == "") || !strings.HasPrefix(stderr, tc.warn) {
				t.Errorf("stderr %q, want prefix %q", stderr, tc.warn)
			}
		})
	}
}

// TestMatchesConvert checks that the output and warnings without the Reader
// flags are those of polyglot.Convert.
func TestMatchesConvert(t *testing.T) {
	inputs := []string{
		"",
		"<p>x",
		xhtml1 + "\n<html><head><title>t</title><meta charset=latin1></head><body><p>a<b>b<i>c</b>d</i></body></html>\n",
		"<?xml version=\"1.0\"?>\n<!-- c --><!DOCTYPE html><svg viewbox='0 0 1 1'><foreignobject><p>x</svg>",
		"<table><tr><td>a</td></tr>b<td>c</table><select><option>a<option>b</select>",
		"<noscript><p>a</p></noscript><script>if (a < b && c) x()</script><style>a>b{}</style>",
		"<p xmlns:epub=\"http://www.idpf.org/2007/ops\" epub:type=x>&nbsp;&amp;&copy;<pre>\nx</pre><xmp><b></xmp>",
		"<template><tr><td>a</template><p>a</p></body>x<plaintext><b>",
	}
	opts := []struct {
		args []string
		opt  polyglot.Options
	}{
		{nil, polyglot.Options{}},
		{[]string{"-doctype", "keep", "-xmldecl", "keep"}, polyglot.Options{DoctypeMode: polyglot.KeepDoctype, XMLDecl: polyglot.KeepXMLDecl}},
		{[]string{"-doctype", "none", "-xmldecl", "add"}, polyglot.Options{DoctypeMode: polyglot.NoDoctype, XMLDecl: polyglot.AddXMLDecl}},
		{[]string{"-doctype", xhtml1}, polyglot.Options{Doctype: xhtml1}},
		{[]string{"-strict"}, polyglot.Options{Strict: true}},
	}
	for i, in := range inputs {
		for _, o := range opts {
			t.Run(fmt.Sprintf("%d %s", i, strings.Join(o.args, " ")), func(t *testing.T) {
				var want bytes.Buffer
				var warnings []string
				opt := o.opt
				opt.Warn = func(w polyglot.Warning) {
					warnings = append(warnings, w.Msg)
				}
				wantErr := polyglot.Convert(&want, strings.NewReader(in), opt)

				stdout, stderr, code := runCmd(t, in, o.args...)
				if wantErr != nil {
					if code != 1 {
						t.Errorf("exit status %d, want 1 for %v", code, wantErr)
					}
					return
				}
				if code != 0 {
					t.Fatalf("exit status %d, stderr %q", code, stderr)
				}
				if stdout != want.String() {
					t.Errorf("got\n\t%s\nwant\n\t%s", stdout, want.String())
				}
				var got []string
				for line := range strings.Lines(stderr) {
					// stdin:line:column: msg
					parts := strings.SplitN(strings.TrimSuffix(line, "\n"), ": ", 2)
					if len(parts) != 2 {
						t.Fatalf("malformed warning %q", line)
					}
					got = append(got, parts[1])
				}
				if !slices.Equal(got, warnings) {
					t.Errorf("warnings\n\t%q\nwant\n\t%q", got, warnings)
				}
			})
		}
	}
}

func writeFile(t *testing.T, name, data string, perm os.FileMode) {
	t.Helper()
	if err := os.WriteFile(name, []byte(data), perm); err != nil {
		t.Fatal(err)
	}
	// not affected by the umask
	if err := os.Chmod(name, perm); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func stat(t *testing.T, name string) os.FileInfo {
	t.Helper()
	fi, err := os.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	return fi
}

// checkDir checks that dir has only the named files, so no temporary files
// are left.
func checkDir(t *testing.T, dir string, names ...string) {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range ents {
		got = append(got, e.Name())
	}
	if !slices.Equal(got, names) {
		t.Errorf("files in %s: %q, want %q", dir, got, names)
	}
}
