package htmlstream

import (
	"bytes"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"testing/iotest"

	html5lib_testdata "github.com/pgaskin/htmlstream/testdata/html5lib"
)

// html5lib-tests tokenizer suite, see html5lib_testdata.

// h5Key identifies a test in the suite. State is the initial state, or empty
// for all of the test's initial states.
type h5Key struct {
	File, Desc, State string
}

// Reasons for expected deviations from the html5lib tokenizer output.
const (
	devIncompleteTag = "EOF inside a tag gives an Incomplete StartTag/EndTag token instead of nothing"
	devEmptyEndTag   = "</> gives an empty bogus Comment token instead of nothing"
	devProcInst      = "<?target ...> is a bogus comment in HTML mode (deliberately kept, as before processing instructions were added to the HTML tokenizer)"
)

// h5Deviations lists tests skipped because of intended differences from the
// html5lib tokenizer.
var h5Deviations = map[h5Key]string{
	{"contentModelFlags.test", "End tag closing RCDATA or RAWTEXT (ending with space)", ""}: devIncompleteTag,
	{"contentModelFlags.test", "End tag closing RCDATA or RAWTEXT (ending with slash)", ""}: devIncompleteTag,
	{"test4.test", "CR EOF in tag name", ""}:                                                devIncompleteTag,
	{"test4.test", "Slash EOF in tag name", ""}:                                             devIncompleteTag,
	{"test4.test", "EOF in tag name state ", ""}:                                            devIncompleteTag,
	{"test4.test", "EOF in before attribute name state", ""}:                                devIncompleteTag,
	{"test4.test", "EOF in attribute name state", ""}:                                       devIncompleteTag,
	{"test4.test", "EOF in after attribute name state", ""}:                                 devIncompleteTag,
	{"test4.test", "EOF in before attribute value state", ""}:                               devIncompleteTag,
	{"test4.test", "EOF in attribute value (double quoted) state", ""}:                      devIncompleteTag,
	{"test4.test", "EOF in attribute value (single quoted) state", ""}:                      devIncompleteTag,
	{"test4.test", "EOF in attribute value (unquoted) state", ""}:                           devIncompleteTag,
	{"test4.test", "EOF in after attribute value state", ""}:                                devIncompleteTag,

	{"test1.test", "Empty end tag", ""}:                           devEmptyEndTag,
	{"test2.test", "Empty end tag with following characters", ""}: devEmptyEndTag,
	{"test2.test", "Empty end tag with following tag", ""}:        devEmptyEndTag,
	{"test2.test", "Empty end tag with following comment", ""}:    devEmptyEndTag,
	{"test2.test", "Empty end tag with following end tag", ""}:    devEmptyEndTag,
	{"test3.test", "</>", ""}:                                     devEmptyEndTag,

	{"test2.test", "Processing instruction", ""}:                                                           devProcInst,
	{"test2.test", "A processing instruction delimited by ?> doesn't contain the delimiting ?", ""}:        devProcInst,
	{"test2.test", "A processing instruction's data delimited by ?> contains the correct amount of ?", ""}: devProcInst,
	// EOF in a processing instruction gives nothing; these are bogus
	// comments to the Reader. Some descriptions are used twice.
	{"test3.test", "<?", ""}:     devProcInst,
	{"test3.test", "<?a", ""}:    devProcInst,
	{"test3.test", "<?b", ""}:    devProcInst,
	{"test3.test", "<?y", ""}:    devProcInst,
	{"test3.test", "<?z", ""}:    devProcInst,
	{"test3.test", "<?A", ""}:    devProcInst,
	{"test3.test", "<?B", ""}:    devProcInst,
	{"test3.test", "<?Y", ""}:    devProcInst,
	{"test3.test", "<?Z", ""}:    devProcInst,
	{"test3.test", "<?a a", ""}:  devProcInst,
	{"test3.test", "<?b b", ""}:  devProcInst,
	{"test3.test", "<?y y", ""}:  devProcInst,
	{"test3.test", "<?z z", ""}:  devProcInst,
	{"test3.test", "<?A A", ""}:  devProcInst,
	{"test3.test", "<?B B", ""}:  devProcInst,
	{"test3.test", "<?Y Y", ""}:  devProcInst,
	{"test3.test", "<?a a?", ""}: devProcInst,
	{"test3.test", "<?b b?", ""}: devProcInst,
	{"test3.test", "<?y y?", ""}: devProcInst,
	{"test3.test", "<?z z?", ""}: devProcInst,
	{"test3.test", "<?A A?", ""}: devProcInst,
	{"test3.test", "<?B B?", ""}: devProcInst,
	{"test3.test", "<?Y Y?", ""}: devProcInst,
	{"test3.test", "<?Z Z?", ""}: devProcInst,
}

// h5KnownFailures lists tests which fail because of suspected bugs in the
// Reader.
var h5KnownFailures = map[h5Key]string{}

// h5String returns the tokens one per line, each indented by a tab.
func h5String(toks []html5lib_testdata.Token) string {
	var b strings.Builder
	for _, t := range toks {
		b.WriteString("\n\t")
		b.WriteString(t.String())
	}
	return b.String()
}

// h5Normalize replaces NUL with U+FFFD and CR and CRLF with LF, as the
// tokenizer and the input stream preprocessing do for bogus comments and
// doctypes.
func h5Normalize(b []byte) string {
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.ReplaceAll(s, "\x00", "\uFFFD")
}

// h5Doctype parses the contents of a doctype (between "<!DOCTYPE" and ">")
// following the WHATWG doctype tokenizer states. If eof, the doctype was cut
// off by the end of the input instead of ending at '>'.
func h5Doctype(data []byte, eof bool) html5lib_testdata.Token {
	const (
		sDoctype = iota
		sBeforeName
		sName
		sAfterName
		sAfterPublicKeyword
		sBeforePublic
		sPublicDQ
		sPublicSQ
		sAfterPublic
		sBetween
		sAfterSystemKeyword
		sBeforeSystem
		sSystemDQ
		sSystemSQ
		sAfterSystem
		sBogus
	)
	d := h5Normalize(data)
	var name, pub, sys []byte
	var hasName, hasPub, hasSys bool
	correct := true
	isWS := func(c byte) bool { return c == '\t' || c == '\n' || c == '\f' || c == ' ' }
	appendChar := func(b []byte, c byte) []byte {
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		return append(b, c)
	}
	state := sDoctype
	for i := 0; i < len(d); i++ {
		c := d[i]
		switch state {
		case sDoctype:
			state = sBeforeName
			if !isWS(c) {
				i--
			}
		case sBeforeName:
			if !isWS(c) {
				hasName = true
				name = appendChar(name, c)
				state = sName
			}
		case sName:
			if isWS(c) {
				state = sAfterName
			} else {
				name = appendChar(name, c)
			}
		case sAfterName:
			switch {
			case isWS(c):
			case i+6 <= len(d) && strings.EqualFold(d[i:i+6], "public"):
				i += 5
				state = sAfterPublicKeyword
			case i+6 <= len(d) && strings.EqualFold(d[i:i+6], "system"):
				i += 5
				state = sAfterSystemKeyword
			default:
				correct = false
				state = sBogus
			}
		case sAfterPublicKeyword, sBeforePublic:
			switch {
			case isWS(c):
				if state == sAfterPublicKeyword {
					state = sBeforePublic
				}
			case c == '"':
				hasPub, state = true, sPublicDQ
			case c == '\'':
				hasPub, state = true, sPublicSQ
			default:
				correct, state = false, sBogus
			}
		case sPublicDQ, sPublicSQ:
			if c == '"' && state == sPublicDQ || c == '\'' && state == sPublicSQ {
				state = sAfterPublic
			} else {
				pub = append(pub, c)
			}
		case sAfterPublic, sBetween:
			switch {
			case isWS(c):
				state = sBetween
			case c == '"':
				hasSys, state = true, sSystemDQ
			case c == '\'':
				hasSys, state = true, sSystemSQ
			default:
				correct, state = false, sBogus
			}
		case sAfterSystemKeyword, sBeforeSystem:
			switch {
			case isWS(c):
				if state == sAfterSystemKeyword {
					state = sBeforeSystem
				}
			case c == '"':
				hasSys, state = true, sSystemDQ
			case c == '\'':
				hasSys, state = true, sSystemSQ
			default:
				correct, state = false, sBogus
			}
		case sSystemDQ, sSystemSQ:
			if c == '"' && state == sSystemDQ || c == '\'' && state == sSystemSQ {
				state = sAfterSystem
			} else {
				sys = append(sys, c)
			}
		case sAfterSystem:
			if !isWS(c) {
				state = sBogus // does not set force-quirks
			}
		case sBogus:
		}
	}
	if eof {
		if state != sBogus {
			correct = false
		}
	} else {
		switch state {
		case sDoctype, sBeforeName, sAfterPublicKeyword, sBeforePublic, sPublicDQ, sPublicSQ,
			sAfterSystemKeyword, sBeforeSystem, sSystemDQ, sSystemSQ:
			correct = false
		}
	}
	return html5lib_testdata.Token{
		Kind:            "DOCTYPE",
		Name:            string(name),
		PublicID:        string(pub),
		SystemID:        string(sys),
		NameMissing:     !hasName,
		PublicIDMissing: !hasPub,
		SystemIDMissing: !hasSys,
		ForceQuirks:     !correct,
	}
}

// h5Run tokenizes input starting in the given html5lib tokenizer state. It
// returns the tokens in html5lib format, the Reader's tokens as strings, and
// the concatenated Raw of all tokens.
func h5Run(input, state, lastStartTag string, rd func(io.Reader) io.Reader) (toks []html5lib_testdata.Token, strs []string, raw string, err error) {
	var src io.Reader = strings.NewReader(input)
	if rd != nil {
		src = rd(src)
	}
	r := NewReader(src)
	switch state {
	case "Data state":
	case "PLAINTEXT state":
		r.raw = rawPlaintext
	case "RCDATA state":
		r.raw = rawRCDATA
	case "RAWTEXT state":
		r.raw = rawText
	case "Script data state":
		r.raw = rawScript
	case "CDATA section state":
		r.cont = contCDATA
	default:
		return nil, nil, "", fmt.Errorf("unsupported initial state %q", state)
	}
	// Without a last start tag, no end tag is appropriate. Raw end tag names
	// are compared with the input lowercased, so an uppercase name never
	// matches.
	r.rawName = "X"
	if lastStartTag != "" {
		r.rawName = lastStartTag
	}

	var chars []byte
	var rawBuf bytes.Buffer
	flush := func() {
		if len(chars) > 0 {
			toks = append(toks, html5lib_testdata.Token{Kind: "Character", Data: string(chars)})
			chars = chars[:0]
		}
	}
	for r.Next() {
		t := r.Token()
		strs = append(strs, t.String())
		rawBuf.Write(t.Raw())
		if len(strs) > 10*len(input)+100 {
			return nil, nil, "", fmt.Errorf("too many tokens")
		}
		switch t.Kind() {
		case Text, CDATA:
			chars = t.AppendText(chars)
			continue
		case StartTag:
			flush()
			tok := html5lib_testdata.Token{Kind: "StartTag", Name: string(t.AppendLocalName(nil)), SelfClosing: t.SelfClosing()}
			for a := range t.Attrs() {
				name := string(a.AppendName(nil))
				if slices.ContainsFunc(tok.Attrs, func(x html5lib_testdata.Attr) bool { return x.Name == name }) {
					continue
				}
				tok.Attrs = append(tok.Attrs, html5lib_testdata.Attr{Name: name, Value: string(a.AppendValue(nil))})
			}
			toks = append(toks, tok)
		case EndTag:
			if t.Implied() {
				continue
			}
			flush()
			toks = append(toks, html5lib_testdata.Token{Kind: "EndTag", Name: string(t.AppendLocalName(nil))})
		case Comment:
			flush()
			tok := html5lib_testdata.Token{Kind: "Comment"}
			if t.Bogus() {
				tok.Data = h5Normalize(t.Data())
			} else {
				tok.Data = string(t.AppendText(nil))
			}
			toks = append(toks, tok)
		case Doctype:
			flush()
			toks = append(toks, h5Doctype(t.Data(), t.Incomplete()))
		case ProcInst:
			flush()
			toks = append(toks, html5lib_testdata.Token{Kind: "ProcessingInstruction", Name: string(t.Name()), Data: string(t.Data())})
		}
	}
	flush()
	return toks, strs, rawBuf.String(), r.Err()
}

// h5Lookup finds a test in a deviation or known failure list, either for the
// given initial state or for all states.
func h5Lookup(m map[h5Key]string, file, desc, state string) (h5Key, string, bool) {
	k := h5Key{file, desc, state}
	if reason, ok := m[k]; ok {
		return k, reason, true
	}
	k.State = ""
	reason, ok := m[k]
	return k, reason, ok
}

func TestHTML5LibTokenizer(t *testing.T) {
	var pass, deviate, known int
	seen := map[h5Key]bool{}
	ran := map[string]bool{} // files not filtered out by -run
	for base := range html5lib_testdata.Files() {
		t.Run(strings.TrimSuffix(base, ".test"), func(t *testing.T) {
			ran[base] = true
			for tc := range html5lib_testdata.Tests(base) {
				if tc.XMLViolation {
					// expects the changes of "Coercing an HTML DOM into an
					// infoset", which the Reader doesn't make
					continue
				}
				input, want := tc.Input, tc.Output
				for _, state := range tc.InitialStates {
					name := fmt.Sprintf("%q (%s, input %q)", tc.Description, state, input)

					// Passthrough and chunking independence are checked for
					// every test, including deviations.
					got, strs, raw, err := h5Run(input, state, tc.LastStartTag, nil)
					got1, strs1, raw1, err1 := h5Run(input, state, tc.LastStartTag, iotest.OneByteReader)
					if raw != input && err == nil {
						t.Errorf("%s: raw mismatch: got %q", name, raw)
					}
					if raw1 != input && err1 == nil {
						t.Errorf("%s: one byte reader: raw mismatch: got %q", name, raw1)
					}
					if !slices.Equal(strs, strs1) || fmt.Sprint(err) != fmt.Sprint(err1) {
						t.Errorf("%s: one byte reader: tokens differ:\n got %q (%v)\nwant %q (%v)", name, strs1, err1, strs, err)
					}
					if h5String(got) != h5String(got1) {
						t.Errorf("%s: one byte reader: html5lib tokens differ:%s\nwant:%s", name, h5String(got1), h5String(got))
					}

					ok := err == nil && h5String(got) == h5String(want)
					devKey, reason, isDev := h5Lookup(h5Deviations, base, tc.Description, state)
					knownKey, knownReason, isKnown := h5Lookup(h5KnownFailures, base, tc.Description, state)
					if isDev {
						seen[devKey] = seen[devKey] || !ok
					}
					if isKnown {
						seen[knownKey] = seen[knownKey] || !ok
						if !isDev {
							reason = knownReason
						}
					}
					switch {
					case ok:
						pass++
					case isDev:
						deviate++
					case isKnown:
						known++
					default:
						t.Errorf("%s\nerror: %v\n  got:%s\n want:%s\nreader tokens: %q", name, err, h5String(got), h5String(want), strs)
					}
					if (isDev || isKnown) && testing.Verbose() && !ok {
						t.Logf("%s: skipped: %s", name, reason)
					}
				}
			}
		})
	}
	// Every listed test must still fail in at least one of its states.
	for _, m := range []map[h5Key]string{h5Deviations, h5KnownFailures} {
		for k := range m {
			if ran[k.File] && !seen[k] {
				t.Errorf("listed test %+v passes or does not exist", k)
			}
		}
	}
	t.Logf("%d passed, %d expected deviations, %d known failures", pass, deviate, known)
}
