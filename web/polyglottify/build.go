package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/evanw/esbuild/pkg/api"
	"github.com/pgaskin/go-hbsubset"
	"github.com/pgaskin/go-woff2"
	"github.com/pgaskin/htmlstream"
)

const (
	outFile  = "index.html"
	wasmFile = "polyglottify.wasm"

	// The base font of the icons, with its ligatures, from the npm package.
	iconFontFile = "node_modules/@material-symbols/font-400/material-symbols-rounded.woff2"

	fontFamily = "Material Symbols Rounded"
)

var toolchain = flag.String("toolchain", "auto", "toolchain for the wasm module: auto (tinygo if found, otherwise go), tinygo or go")

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) == 0 && a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	})))
	flag.Parse()
	if _, err := os.Stat("polyglottify.html"); err != nil {
		fatal("run from the directory with polyglottify.html", err)
	}
	if _, err := os.Stat("node_modules"); err != nil {
		fatal("the npm packages are not installed, run npm ci", err)
	}
	if err := build(); err != nil {
		fatal("build", err)
	}
}

func fatal(msg string, err error) {
	slog.Error(msg, "err", err)
	os.Exit(1)
}

// run logs and runs cmd, with its output going to ours.
func run(cmd *exec.Cmd) error {
	slog.Info("run", "cmd", cmd.String())
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	start := time.Now()
	err := cmd.Run()
	if err != nil {
		slog.Error("run failed", "cmd", cmd.Path, "err", err)
		return err
	}
	slog.Info("run done", "cmd", cmd.Path, "took", time.Since(start).Round(time.Millisecond))
	return nil
}

func build() error {
	wasm, used, err := buildWasm()
	if err != nil {
		return err
	}

	b := &builder{wasm: wasm}
	for name, dst := range map[string]*[]byte{
		"polyglottify.html": &b.html,
		"polyglottify.css":  &b.css,
		"polyglottify.ts":   &b.ts,
	} {
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		*dst = data
	}

	icons, err := scanIcons(b.html, b.ts)
	if err != nil {
		return err
	}
	b.font, err = buildIconFont(icons)
	if err != nil {
		return fmt.Errorf("icon font: %w", err)
	}

	out, err := b.transform()
	if err != nil {
		return fmt.Errorf("transform polyglottify.html: %w", err)
	}
	if err := os.WriteFile(outFile, out, 0o644); err != nil {
		return err
	}

	fmt.Printf("%s  %8d bytes\n", outFile, len(out))
	fmt.Printf("  bundle         %8d bytes (%d modules)\n", b.bundleSize, b.modules)
	fmt.Printf("  icon font      %8d bytes woff2 (%d icons)\n", len(b.font.woff2), len(icons))
	fmt.Printf("  wasm           %8d bytes (%s)\n", len(wasm), used)
	return nil
}

// buildWasm builds polyglottify.go, returning the module and a description of
// how.
func buildWasm() ([]byte, string, error) {
	tool := *toolchain
	switch tool {
	case "auto":
		tool = "go"
		if _, err := exec.LookPath("tinygo"); err == nil {
			tool = "tinygo"
		}
	case "tinygo", "go":
	default:
		return nil, "", fmt.Errorf("unknown toolchain %q", tool)
	}

	out := wasmFile

	var cmd *exec.Cmd
	switch tool {
	case "tinygo":
		// The scheduler isn't needed.
		cmd = exec.Command("tinygo", "build", "-target", "wasip1", "-buildmode", "c-shared",
			"-scheduler", "none", "-gc", "precise", "-opt", "z", "-no-debug",
			"-o", out, "polyglottify.go")
	case "go":
		cmd = exec.Command("go", "build", "-buildmode=c-shared", "-trimpath", "-ldflags=-s", "-gcflags=all=-B",
			"-o", out, "polyglottify.go")
		cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	}
	if err := run(cmd); err != nil {
		return nil, "", fmt.Errorf("build wasm with %s: %w", tool, err)
	}
	used := tool

	// The features are the ones the toolchains emit, and all that Chromium
	// accepts of them (binaryen's own defaults, or all of its features, would
	// emit things which it doesn't).
	if path, err := exec.LookPath("wasm-opt"); err == nil {
		cmd := exec.Command(path, "-Oz", "--enable-bulk-memory", "--enable-nontrapping-float-to-int",
			"--enable-sign-ext", "--enable-mutable-globals", out, "-o", out)
		if err := run(cmd); err != nil {
			return nil, "", fmt.Errorf("wasm-opt: %w", err)
		}
		used += " + wasm-opt"
	}

	wasm, err := os.ReadFile(out)
	return wasm, used, err
}

// Script kinds, as returned by scriptKind.
const (
	scriptClassic = "classic"
	scriptModule  = "module"
)

// scriptKind classifies the script start tag t.
func scriptKind(t *htmlstream.Token) (string, error) {
	typ, _ := t.Attr("type")
	if src, ok := t.Attr("src"); ok {
		switch {
		case src.Value() == "polyglottify.ts" && typ.Value() == "module":
			return scriptModule, nil
		}
		return "", fmt.Errorf("script src %q is not supported", src.Value())
	}
	if v := strings.ToLower(typ.Value()); v != "" && v != "text/javascript" {
		return "", fmt.Errorf("inline script type %q is not supported", v)
	}
	return scriptClassic, nil
}

// scanIcons returns the sorted names of the icons used by the page: the
// data-icon values, and the icon("name") calls in the inline scripts and the
// module.
func scanIcons(html, ts []byte) ([]string, error) {
	icons := map[string]bool{}

	names, err := iconCalls(string(ts))
	if err != nil {
		return nil, fmt.Errorf("polyglottify.ts: %w", err)
	}
	for _, name := range names {
		icons[name] = true
	}

	r := htmlstream.NewReader(bytes.NewReader(html))
	for r.Next() {
		t := r.Token()
		if t.Kind() != htmlstream.StartTag {
			continue
		}
		if a, ok := t.Attr("data-icon"); ok {
			icons[a.Value()] = true
		}
		if !t.Is("script") {
			continue
		}
		kind, err := scriptKind(t)
		if err != nil {
			return nil, fmt.Errorf("polyglottify.html: %w", err)
		}
		if kind != scriptClassic {
			continue
		}
		var text []byte
		if r.Next() && r.Token().Kind() == htmlstream.Text {
			text, _ = r.ReadText(nil, 0)
		}
		names, err := iconCalls(string(text))
		if err != nil {
			return nil, fmt.Errorf("polyglottify.html script: %w", err)
		}
		for _, name := range names {
			icons[name] = true
		}
	}
	if err := r.Err(); err != nil {
		return nil, fmt.Errorf("polyglottify.html: %w", err)
	}

	var sorted []string
	for name := range icons {
		sorted = append(sorted, name)
	}
	slices.Sort(sorted)
	return sorted, nil
}

var (
	iconCall    = regexp.MustCompile(`\bicon\(`)
	iconLiteral = regexp.MustCompile(`\bicon\("([a-z0-9_]+)"\)`)
)

// iconCalls returns the names of the icon("name") calls in js. Any other call
// of icon is an error, since its argument can't be known at build time.
func iconCalls(js string) ([]string, error) {
	for _, loc := range iconCall.FindAllStringIndex(js, -1) {
		if strings.HasSuffix(js[:loc[0]], "function ") {
			continue
		}
		if m := iconLiteral.FindStringIndex(js[loc[0]:]); m == nil || m[0] != 0 {
			line := 1 + strings.Count(js[:loc[0]], "\n")
			return nil, fmt.Errorf("line %d: icon must be called with a string literal", line)
		}
	}
	var names []string
	for _, m := range iconLiteral.FindAllStringSubmatch(js, -1) {
		names = append(names, m[1])
	}
	return names, nil
}

// replaceIconCalls replaces the icon("name") calls in js with string literals
// of the icon characters.
func replaceIconCalls(js string, runes map[string]rune) string {
	return iconLiteral.ReplaceAllStringFunc(js, func(call string) string {
		name := iconLiteral.FindStringSubmatch(call)[1]
		if r := runes[name]; r > 0xFFFF {
			return fmt.Sprintf(`"\u{%x}"`, r)
		} else {
			return fmt.Sprintf(`"\u%04x"`, r)
		}
	})
}

// The icon font is the subset of the variable font to the used icons, as
// woff2. Icons are addressed by codepoint, without ligatures.
type iconFont struct {
	woff2 []byte
	runes map[string]rune
}

// github.com/ottrec/website@6015c9b5ed46e81f744ef240d6515a07e63b5074/static/static.go
func buildIconFont(names []string) (*iconFont, error) {
	packed, err := os.ReadFile(iconFontFile)
	if err != nil {
		return nil, fmt.Errorf("icon font (run npm ci): %w", err)
	}
	ttf, err := woff2.DecodeBytes(packed)
	if err != nil {
		return nil, fmt.Errorf("decode icon font: %w", err)
	}
	table, err := iconRunes(ttf)
	if err != nil {
		return nil, fmt.Errorf("icon names: %w", err)
	}

	f := &iconFont{runes: map[string]rune{}}
	var unicodes []rune
	for _, name := range names {
		r, ok := table[name]
		if !ok {
			return nil, fmt.Errorf("icon %q does not exist", name)
		}
		f.runes[name] = r
		unicodes = append(unicodes, r)
	}

	// The axes are the ones of the font, the others are pinned to their defaults.
	pins := map[hbsubset.Tag]float32{}
	if face, err := hbsubset.NewFace(ttf, 0); err == nil {
		for tag, def := range map[string]float32{"FILL": 0, "wght": 400, "GRAD": 0, "opsz": 24} {
			if _, ok := face.VariationAxes()[hbsubset.MakeTag(tag)]; ok {
				pins[hbsubset.MakeTag(tag)] = def
			}
		}
	}
	slog.Info("hbsubset", "bytes", len(ttf), "icons", len(unicodes))
	sub, err := hbsubset.Subset(ttf, 0, &hbsubset.Options{
		Unicodes:            unicodes,
		PinAllAxesToDefault: true,
		PinAxes:             pins,
		DropTables:          []hbsubset.Tag{hbsubset.MakeTag("GSUB")},
		NameIDs: []hbsubset.NameID{
			hbsubset.NameUniqueID,
			hbsubset.NameCopyright,
			hbsubset.NameFontFamily,
			hbsubset.NameFontSubfamily,
			hbsubset.NameTypographicFamily,
			hbsubset.NameTypographicSubfamily,
			hbsubset.NameFullName,
			hbsubset.NameMacFullName,
			hbsubset.NamePostscriptName,
			hbsubset.NameManufacturer,
			hbsubset.NameDescription,
			hbsubset.NameVariationsPSPrefix,
		},
		NameLanguages: []uint32{1033},
	})
	if err != nil {
		return nil, fmt.Errorf("subset: %w", err)
	}

	face, err := hbsubset.NewFace(sub, 0)
	if err != nil {
		return nil, fmt.Errorf("read subset: %w", err)
	}
	have := face.GlyphMapping()
	for name, r := range f.runes {
		if _, ok := have[r]; !ok {
			return nil, fmt.Errorf("icon %q (U+%04X) is missing from the subset", name, r)
		}
	}

	f.woff2, err = woff2.Encode(sub, nil)
	if err != nil {
		return nil, fmt.Errorf("woff2: %w", err)
	}
	slog.Info("woff2", "bytes", len(sub), "out", len(f.woff2))
	return f, nil
}

// github.com/ottrec/website@6015c9b5ed46e81f744ef240d6515a07e63b5074/static/icons.go
func iconRunes(ttf []byte) (map[string]rune, error) {
	face, err := hbsubset.NewFace(ttf, 0)
	if err != nil {
		return nil, err
	}
	letter := make(map[uint32]byte) // glyphs of the name letters a-z0-9_
	code := make(map[uint32]rune)   // smallest codepoint per glyph
	for r, g := range face.GlyphMapping() {
		if r == '_' || 'a' <= r && r <= 'z' || '0' <= r && r <= '9' {
			letter[g] = byte(r)
		}
		if c, ok := code[g]; !ok || r < c {
			code[g] = r
		}
	}
	gsub, err := face.Table(hbsubset.MakeTag("GSUB"))
	if err != nil {
		return nil, fmt.Errorf("get GSUB table: %w", err)
	}
	return gsubLigatureNames(gsub, letter, code)
}

// github.com/ottrec/website@6015c9b5ed46e81f744ef240d6515a07e63b5074/static/icons.go
func gsubLigatureNames(gsub []byte, letter map[uint32]byte, code map[uint32]rune) (_ map[string]rune, err error) {
	// The font comes from a package with a locked version, so rather than
	// checking the bounds of every read, a malformed table panics and the
	// panic becomes an error.
	defer func() {
		if recover() != nil {
			err = errors.New("malformed GSUB table")
		}
	}()
	u16 := func(off int) int { return int(binary.BigEndian.Uint16(gsub[off:])) }
	u32 := func(off int) int { return int(binary.BigEndian.Uint32(gsub[off:])) }

	names := make(map[string]rune)

	// Ligature: ligGlyph, compCount, comps[compCount-1] (the first comes from
	// the coverage table)
	ligature := func(off int, first uint32) {
		name := make([]byte, 0, 32)
		c, ok := letter[first]
		if !ok {
			return
		}
		name = append(name, c)
		for i := range u16(off+2) - 1 {
			if c, ok = letter[uint32(u16(off+4+i*2))]; !ok {
				return
			}
			name = append(name, c)
		}
		if r, ok := code[uint32(u16(off))]; ok {
			names[string(name)] = r
		}
	}

	// Coverage: the first glyph of the ligature set at each index
	coverage := func(off int) []uint32 {
		var glyphs []uint32
		switch u16(off) {
		case 1: // glyph list
			for i := range u16(off + 2) {
				glyphs = append(glyphs, uint32(u16(off+4+i*2)))
			}
		case 2: // glyph ranges
			for i := range u16(off + 2) {
				for g := u16(off + 4 + i*6); g <= u16(off+6+i*6); g++ {
					glyphs = append(glyphs, uint32(g))
				}
			}
		}
		return glyphs
	}

	// LigatureSubst format 1: coverage, then a LigatureSet per covered glyph
	ligatureSubst := func(off int) {
		if u16(off) != 1 {
			return
		}
		firsts := coverage(off + u16(off+2))
		for i := range min(u16(off+4), len(firsts)) {
			set := off + u16(off+6+i*2)
			for j := range u16(set) {
				ligature(set+u16(set+2+j*2), firsts[i])
			}
		}
	}

	lookups := u16(8) // lookup list offset
	for i := range u16(lookups) {
		lookup := lookups + u16(lookups+2+i*2)
		for j := range u16(lookup + 4) {
			sub := lookup + u16(lookup+6+j*2)
			switch u16(lookup) { // lookup type
			case 4:
				ligatureSubst(sub)
			case 7: // extension: format, wrapped type, u32 offset
				if u16(sub+2) == 4 {
					ligatureSubst(sub + u32(sub+4))
				}
			}
		}
	}
	return names, nil
}

// A builder holds the sources and what the single pass over the page needs.
type builder struct {
	html, css, ts []byte
	wasm          []byte
	font          *iconFont

	bundleSize int
	modules    int
}

// transform rewrites polyglottify.html in a single pass. Everything not mentioned
// here is passed through unchanged.
func (b *builder) transform() (res []byte, err error) {
	var out bytes.Buffer
	x := htmlstream.NewTransformer(&out, bytes.NewReader(b.html))
	// The output is only complete after Close, which also reports the
	// Transformer's own errors, so they take precedence over the others.
	defer func() {
		if cerr := x.Close(); cerr != nil {
			res, err = nil, cerr
		} else if err == nil {
			res = out.Bytes()
		}
	}()
	var gotStyle bool

	for x.Next() {
		t := x.Token()
		if t.Kind() != htmlstream.StartTag {
			continue
		}

		if a, ok := t.Attr("data-icon"); ok {
			r, ok := b.font.runes[a.Value()]
			if !ok {
				return nil, fmt.Errorf("icon %q was not found by the first pass", a.Value())
			}
			if err := x.SetAttr("data-icon", string(r)); err != nil {
				return nil, err
			}
		}

		switch {
		case t.Is("link") && isStylesheet(t, "polyglottify.css"):
			style, err := b.style()
			if err != nil {
				return nil, fmt.Errorf("polyglottify.css: %w", err)
			}
			gotStyle = true
			if err := x.Replace([]byte(style)); err != nil {
				return nil, err
			}
		case t.Is("script"):
			if err := b.transformScript(x); err != nil {
				return nil, err
			}
		}
	}
	if !gotStyle {
		return nil, errors.New("no polyglottify.css stylesheet link to replace")
	}
	return nil, nil
}

func isStylesheet(t *htmlstream.Token, href string) bool {
	rel, _ := t.Attr("rel")
	h, _ := t.Attr("href")
	return strings.EqualFold(rel.Value(), "stylesheet") && h.Value() == href
}

// style returns the style element with the icon font and the minified css.
func (b *builder) style() (string, error) {
	css, err := minifyCSS(string(b.css))
	if err != nil {
		return "", err
	}
	return `<style>@font-face{font-family:"` + fontFamily + `";font-style:normal;font-weight:400;` +
		`src:url(data:font/woff2;base64,` + base64.StdEncoding.EncodeToString(b.font.woff2) + `) format("woff2")}` +
		css + `</style>`, nil
}

// transformScript rewrites the script element at the current start tag.
func (b *builder) transformScript(x *htmlstream.Transformer) error {
	kind, err := scriptKind(x.Token())
	if err != nil {
		return err
	}
	switch kind {
	case scriptModule:
		js, modules, err := bundle(replaceIconCalls(string(b.ts), b.font.runes), "polyglottify.ts")
		if err != nil {
			return fmt.Errorf("polyglottify.ts: %w", err)
		}
		b.bundleSize, b.modules = len(js), modules
		if err := inlineScript(x, js); err != nil {
			return err
		}
		// The module is fetched from a data URL because that's faster than
		// decoding it in script, and doesn't need a string of the decoded bytes.
		// Base64 can't end the script element.
		return x.Wrap(nil, []byte(`<script>try{fetch("data:application/octet-stream;base64,`+
			base64.StdEncoding.EncodeToString(b.wasm)+
			`").then(r=>r.arrayBuffer()).then(WebAssembly.compile).then(wasmResolve,wasmReject)}catch(e){wasmReject(e)}</script>`))
	default:
		return replaceText(x, func(js string) (string, error) {
			return minifyJS(replaceIconCalls(js, b.font.runes))
		})
	}
}

// inlineScript turns the external script at the current start tag into an
// inline one with the given (already escaped) code.
func inlineScript(x *htmlstream.Transformer, js string) error {
	if err := x.RemoveAttr("src"); err != nil {
		return err
	}
	return x.InsertAfter([]byte(js))
}

// replaceText replaces the text content of the element opened by the current
// start tag with the result of fn, which is given the text. The text can be
// split into several tokens, so the output of all of them is held back.
func replaceText(x *htmlstream.Transformer, fn func(string) (string, error)) error {
	if !x.Next() {
		return x.Err()
	}
	if x.Token().Kind() != htmlstream.Text {
		return nil
	}
	h := x.Hold()
	text, _ := x.ReadText(nil, 0)
	res, err := fn(string(text))
	if err != nil {
		return err
	}
	if err := x.Replace([]byte(res)); err != nil {
		return err
	}
	return x.HoldDiscard(h, nil)
}

// bundle bundles a TypeScript module and the npm packages it imports into one
// minified ES module, returning it with the number of files bundled. The
// packages are resolved by esbuild from node_modules (run npm ci), and the
// module is the entry, named sourcefile, with its relative imports read from
// the current directory.
func bundle(source, sourcefile string) (string, int, error) {
	slog.Info("esbuild", "op", "bundle", "entry", sourcefile, "bytes", len(source))
	res := api.Build(api.BuildOptions{
		Stdin: &api.StdinOptions{
			Contents:   source,
			Sourcefile: sourcefile,
			ResolveDir: ".",
			Loader:     api.LoaderTS,
		},
		Bundle:            true,
		Format:            api.FormatESModule,
		Target:            api.ES2022,
		Charset:           api.CharsetUTF8,
		Platform:          api.PlatformBrowser,
		TreeShaking:       api.TreeShakingTrue,
		MinifyWhitespace:  true,
		MinifyIdentifiers: true,
		MinifySyntax:      true,
		LegalComments:     api.LegalCommentsNone,
		LogLevel:          api.LogLevelSilent,
		Metafile:          true,
		Write:             false,
	})
	if err := esbuildError(res.Errors); err != nil {
		return "", 0, err
	}
	if len(res.OutputFiles) != 1 {
		return "", 0, fmt.Errorf("expected one output file, got %d", len(res.OutputFiles))
	}
	var meta struct {
		Inputs map[string]json.RawMessage `json:"inputs"`
	}
	if err := json.Unmarshal([]byte(res.Metafile), &meta); err != nil {
		return "", 0, err
	}
	return escapeScript(strings.TrimSpace(string(res.OutputFiles[0].Contents))), len(meta.Inputs), nil
}

func minifyCSS(css string) (string, error) {
	slog.Info("esbuild", "op", "minify css", "bytes", len(css))
	res := api.Transform(css, api.TransformOptions{
		Loader: api.LoaderCSS,
		Engines: []api.Engine{
			{Name: api.EngineSafari, Version: "15"},
			{Name: api.EngineChrome, Version: "110"},
			{Name: api.EngineFirefox, Version: "110"},
		},
		MinifyWhitespace: true,
		MinifySyntax:     true,
		LegalComments:    api.LegalCommentsNone,
		LogLevel:         api.LogLevelSilent,
	})
	if err := esbuildError(res.Errors); err != nil {
		return "", err
	}
	out := strings.TrimSpace(string(res.Code))
	if strings.Contains(strings.ToLower(out), "</style") {
		return "", errors.New("css contains </style")
	}
	return out, nil
}

// minifyJS minifies a classic script. Top-level names are kept since they are
// globals.
func minifyJS(js string) (string, error) {
	slog.Info("esbuild", "op", "minify js", "bytes", len(js))
	res := api.Transform(js, api.TransformOptions{
		Loader:            api.LoaderJS,
		Charset:           api.CharsetUTF8,
		MinifyWhitespace:  true,
		MinifyIdentifiers: true,
		MinifySyntax:      true,
		LegalComments:     api.LegalCommentsNone,
		LogLevel:          api.LogLevelSilent,
	})
	if err := esbuildError(res.Errors); err != nil {
		return "", err
	}
	return escapeScript(strings.TrimSpace(string(res.Code))), nil
}

func esbuildError(msgs []api.Message) error {
	if len(msgs) == 0 {
		return nil
	}
	return fmt.Errorf("esbuild: %s", strings.TrimSpace(strings.Join(api.FormatMessages(msgs, api.FormatMessagesOptions{}), "")))
}

var (
	closeScript = regexp.MustCompile(`(?i)</(script)`)
	openComment = regexp.MustCompile(`<!--`)
)

// escapeScript makes js safe to put inside a script element, assuming the
// sequences are in strings or regular expressions, as they are in practice.
func escapeScript(js string) string {
	js = closeScript.ReplaceAllString(js, `<\/$1`)
	return openComment.ReplaceAllString(js, `<\!--`)
}
