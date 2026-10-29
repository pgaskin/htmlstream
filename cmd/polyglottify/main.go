// Polyglottify converts HTML documents to polyglot markup: markup which HTML
// and XML parsers read as the same document (see the polyglot package). The
// input must be UTF-8: convert documents in other encodings first (see
// Encodings in the polyglot package).
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/pgaskin/htmlstream"
	"github.com/pgaskin/htmlstream/polyglot"
)

// stdinName is the name of standard input in warnings and errors.
const stdinName = "stdin"

func main() {
	name := "polyglottify"
	if len(os.Args) > 0 {
		name = filepath.Base(os.Args[0])
	}
	os.Exit(run(name, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// config is the conversion configured by the flags. It reads the input with
// its own Reader rather than with polyglot.Convert for the Reader limits,
// which polyglot.Options doesn't have.
type config struct {
	opt   polyglot.Options
	quiet bool

	// Reader limits, applied if set
	maxTokenSize, maxDepth, maxStackBytes *int

	stderr io.Writer
}

func run(name string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fset := flag.NewFlagSet(name, flag.ContinueOnError)
	fset.SetOutput(stderr)
	fset.Usage = func() {
		fmt.Fprintf(stderr, "usage: %s [flags] [file|-]...\n", name)
		fmt.Fprintf(stderr, "flags:\n")
		fset.PrintDefaults()
	}
	doctype := fset.String("doctype", "", "doctype `mode` to use (default or \"\", keep, none, <replacement_doctype>) (default \"<!DOCTYPE html>\")")
	xmlDecl := fset.String("xmldecl", "none", "xml declaration `mode` to use (none, add, keep)")
	strict := fset.Bool("strict", false, "fail if input is structurally unsound/ambiguous (instead of attempting to continue)")
	finalNewline := fset.Bool("final-newline", false, "end the output with a line break (HTML parsers append it to the body, taking it from whitespace at the end of the body if it ends with one, and XML parsers ignore it)")
	indent := fset.String("indent", "", "indent the output with this `string` of spaces and tabs (\\t for a tab) for each level of block-level elements, changing only whitespace which isn't rendered with the default styles")
	indentPrefix := fset.String("indent-prefix", "", "start each line of the output after the first one with this `string` of spaces and tabs (\\t for a tab), except in comments and in contents whose whitespace is kept")
	quiet := fset.Bool("quiet", false, "don't print warnings to stderr")
	inPlace := fset.Bool("i", false, "convert the files in place, writing to a temporary file first (requires at least one filename)")
	xmlSyntax := fset.Bool("xml-syntax", false, "accept CDATA sections, processing instructions, self-closing tags on non-void elements, end tags of void elements (<br></br> is one br), and doctypes with an internal subset, for documents written as XHTML (e.g., EPUB content) (it may slightly change the meaning of looser HTML documents if used for them)")
	scripting := fset.Bool("scripting", false, "read noscript contents as raw text rather than descending into it (like browsers with js enabled) (you probably DO NOT want this as it will result in contents containing markup being written as escaped text)")
	maxTokenSize := fset.Int("max-token-size", htmlstream.DefaultMaxTokenSize, "maximum size of a tag, doctype or processing instruction, in `bytes`")
	maxDepth := fset.Int("max-depth", htmlstream.DefaultMaxDepth, "maximum `depth` of nested elements, in the input and in the output (where formatting elements reopened after misnested end tags can nest deeper); the tags of deeper ones are dropped, keeping their contents")
	maxStackBytes := fset.Int("max-stack-bytes", htmlstream.DefaultMaxStackBytes, "maximum size of the names and namespace declarations kept for the open elements, in `bytes`")
	maxRawText := fset.Int("max-raw-text", polyglot.DefaultMaxRawText, "maximum size of script and style contents, noscript contents with -scripting, title and textarea contents starting with a CDATA section with -xml-syntax, and the output before the root element with -xmldecl keep, in `bytes`")
	if err := fset.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	usageError := func(format string, args ...any) int {
		fmt.Fprintf(stderr, "%s: %s\n", name, fmt.Sprintf(format, args...))
		fset.Usage()
		return 2
	}

	c := config{
		quiet:  *quiet,
		stderr: stderr,
	}
	c.opt.Strict = *strict
	c.opt.XMLSyntax = *xmlSyntax
	c.opt.Scripting = *scripting
	c.opt.FinalNewline = *finalNewline
	c.opt.Indent = strings.ReplaceAll(*indent, `\t`, "\t")
	c.opt.IndentPrefix = strings.ReplaceAll(*indentPrefix, `\t`, "\t")
	if strings.Trim(c.opt.Indent, " \t") != "" || strings.Trim(c.opt.IndentPrefix, " \t") != "" {
		return usageError("invalid -indent %q or -indent-prefix %q: must have only spaces and tabs", *indent, *indentPrefix)
	}
	switch *doctype {
	case "", "default":
	case "keep":
		c.opt.DoctypeMode = polyglot.KeepDoctype
	case "none":
		c.opt.DoctypeMode = polyglot.NoDoctype
	default:
		if !strings.HasPrefix(*doctype, "<") || !strings.HasSuffix(*doctype, ">") {
			return usageError("invalid -doctype %q: must be default, keep, none, or a doctype declaration", *doctype)
		}
		if err := checkDoctype(*doctype); err != nil {
			return usageError("invalid -doctype %q: %v", *doctype, err)
		}
		c.opt.Doctype = *doctype
	}
	switch *xmlDecl {
	case "none":
	case "add":
		c.opt.XMLDecl = polyglot.AddXMLDecl
	case "keep":
		c.opt.XMLDecl = polyglot.KeepXMLDecl
	default:
		return usageError("invalid -xmldecl %q: must be none, add or keep", *xmlDecl)
	}
	var invalid error
	fset.Visit(func(f *flag.Flag) {
		var p **int
		var v int
		switch f.Name {
		case "max-token-size":
			p, v = &c.maxTokenSize, *maxTokenSize
		case "max-depth":
			p, v = &c.maxDepth, *maxDepth
			c.opt.MaxDepth = v
		case "max-stack-bytes":
			p, v = &c.maxStackBytes, *maxStackBytes
		case "max-raw-text":
			c.opt.MaxRawText, v = *maxRawText, *maxRawText
		default:
			return
		}
		if v <= 0 && invalid == nil {
			invalid = fmt.Errorf("invalid -%s %d: must be positive", f.Name, v)
		}
		if p != nil {
			*p = &v
		}
	})
	if invalid != nil {
		return usageError("%v", invalid)
	}

	files := fset.Args()
	if !*inPlace {
		if len(files) > 1 {
			return usageError("more than one file without -i")
		}
		if len(files) == 0 || files[0] == "-" {
			if err := c.convert(stdout, stdin, stdinName); err != nil {
				fmt.Fprintf(stderr, "%s: %v\n", name, err)
				return 1
			}
			return 0
		}
		f, err := os.Open(files[0])
		if err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", name, err)
			return 1
		}
		defer f.Close()
		if err := c.convert(stdout, f, files[0]); err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", name, err)
			return 1
		}
		return 0
	}
	if len(files) == 0 {
		return usageError("no files to convert in place")
	}
	if slices.Contains(files, "-") {
		return usageError("can't convert standard input in place")
	}
	status := 0
	for _, file := range files {
		if err := c.convertInPlace(file); err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", name, err)
			status = 1
		}
	}
	return status
}

// checkDoctype checks that s is a single doctype declaration, written as
// [polyglot.KeepDoctype] writes it. The error gives the reason only.
func checkDoctype(s string) error {
	if len(s) < 9 || !strings.EqualFold(s[:9], "<!DOCTYPE") {
		return errors.New("not a doctype declaration")
	}
	r := htmlstream.NewReader(strings.NewReader(s))
	if !r.Next() || r.Token().Kind() != htmlstream.Doctype || r.Token().Incomplete() || string(r.Token().Raw()) != s {
		return errors.New("not a single doctype declaration")
	}
	var b strings.Builder
	var reason string
	err := polyglot.Convert(&b, strings.NewReader(s), polyglot.Options{
		DoctypeMode: polyglot.KeepDoctype,
		Warn: func(w polyglot.Warning) {
			if why, ok := strings.CutPrefix(w.Msg, "dropped doctype ("); ok && reason == "" {
				reason = strings.TrimSuffix(why, ")")
			}
		},
	})
	if err != nil {
		return err
	}
	doctype, _, written := strings.Cut(b.String(), "\n<html")
	if !written {
		if reason == "" {
			reason = "not a doctype"
		}
		return errors.New(reason)
	}
	if doctype != s {
		return fmt.Errorf("not written by -doctype keep as given (%q)", doctype)
	}
	return nil
}

// convertInPlace converts file, replacing it with the output if the conversion
// succeeds.
func (c *config) convertInPlace(file string) (err error) {
	path, err := filepath.EvalSymlinks(file)
	if err != nil {
		return err
	}
	in, err := os.Open(path)
	if err != nil {
		return err
	}
	defer in.Close()
	fi, err := in.Stat()
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s: not a regular file", file)
	}
	out, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			out.Close()
			os.Remove(out.Name())
		}
	}()
	if err := c.convert(out, in, file); err != nil {
		return err
	}
	if err := out.Chmod(fi.Mode() & (fs.ModePerm | fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky)); err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(out.Name(), path)
}

// convert converts the document read from r to w, reporting warnings for the
// input named name. The error is prefixed with the name and the position,
// unless it is an error about a path.
func (c *config) convert(w io.Writer, r io.Reader, name string) error {
	lr := &lineReader{r: r}
	opt := c.opt
	if !c.quiet {
		opt.Warn = func(w polyglot.Warning) {
			fmt.Fprintf(c.stderr, "%s: %s\n", lr.position(name, w.Pos), w.Msg)
		}
	}
	rd := htmlstream.NewReader(lr)
	rd.SetScripting(opt.Scripting)
	rd.SetStrict(opt.Strict)
	rd.SetXMLSyntax(opt.XMLSyntax)
	if c.maxTokenSize != nil {
		rd.SetMaxTokenSize(*c.maxTokenSize)
	}
	if c.maxDepth != nil {
		rd.SetMaxDepth(*c.maxDepth)
	}
	if c.maxStackBytes != nil {
		rd.SetMaxStackBytes(*c.maxStackBytes)
	}
	pw := polyglot.NewWriter(w, opt)
	err := func() error {
		for rd.Next() {
			if err := pw.WriteToken(rd.Token()); err != nil {
				return err
			}
		}
		if err := rd.Err(); err != nil {
			return err
		}
		return pw.Close()
	}()
	if err == nil {
		return nil
	}
	if se, ok := errors.AsType[*htmlstream.SyntaxError](err); ok {
		return fmt.Errorf("%s: %s", lr.position(name, se.Pos), se.Msg)
	}
	if pe, ok := errors.AsType[*polyglot.Error](err); ok {
		return fmt.Errorf("%s: %s", lr.position(name, pe.Pos), pe.Msg)
	}
	if _, ok := errors.AsType[*fs.PathError](err); ok {
		return err
	}
	return fmt.Errorf("%s: %w", name, err)
}

// maxLines is the number of line starts a lineReader keeps at least, so that
// its memory doesn't grow with the input.
const maxLines = 1 << 16

// lineReader reads from r, recording the offsets of the starts of the last
// lines read, to report positions as lines and columns.
type lineReader struct {
	r       io.Reader
	n       int64   // bytes read
	lines   []int64 // offsets of the starts of the last lines read, after the first line
	dropped int     // the number of line starts dropped before lines
	cr      bool    // the last byte read is CR
}

func (l *lineReader) Read(b []byte) (int, error) {
	n, err := l.r.Read(b)
	for i, c := range b[:n] {
		off := l.n + int64(i)
		switch {
		case c == '\n' && l.cr:
			l.lines[len(l.lines)-1] = off + 1
		case c == '\n' || c == '\r':
			if len(l.lines) == 2*maxLines {
				l.dropped += copy(l.lines, l.lines[maxLines:])
				l.lines = l.lines[:maxLines]
			}
			l.lines = append(l.lines, off+1)
		}
		l.cr = c == '\r'
	}
	l.n += int64(n)
	return n, err
}

// position formats pos in the input named name as name:line:column, or as
// "name: byte offset" if the start of its line was dropped.
func (l *lineReader) position(name string, pos htmlstream.Pos) string {
	off := int64(pos)
	i, found := slices.BinarySearch(l.lines, off)
	if found {
		i++
	}
	start := int64(0)
	switch {
	case i > 0:
		start = l.lines[i-1]
	case l.dropped != 0:
		return fmt.Sprintf("%s: byte %d", name, off)
	}
	return fmt.Sprintf("%s:%d:%d", name, l.dropped+i+1, off-start+1)
}
