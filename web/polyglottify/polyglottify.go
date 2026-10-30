//go:build ignore

// Command polyglottifyweb is the WebAssembly module behind polyglottify.html:
// polyglot conversion exposed as a few exported functions, with no JavaScript
// runtime (no syscall/js or wasm_exec.js). polyglottify_wasm.ts is the other half.
//
// It is a WASI reactor: the host calls _initialize once and then the exports.
// Strings are passed through the module's memory in buffers from alloc
// (released with release), and defaults and polyglottify return a pointer to
// a result buffer which holds its length as a little-endian uint32 followed by
// that many bytes of JSON. A result is valid until the next call.
//
//	alloc(n) ptr
//	release(ptr)
//	defaults() result              {"maxTokenSize":n,"maxDepth":n,"maxStackBytes":n,"maxRawText":n}
//	polyglottify(in, inLen, opts, optsLen) result
//	                               {"output":s,"warnings":[w...],"error":w}
//
// The options (see the format at convert) are a flat binary encoding, and the
// warnings and the error have the line and column (1-based, in characters),
// the UTF-16 index of their position in the input, and the message. The error
// has only the message if it has no position.
//
// It is tagged ignore so that the directory builds as the build program
// (build.go), which compiles this file explicitly. The same source
// builds with TinyGo (preferred, since the module is about a sixth of the
// size) and with the standard toolchain; see buildWasm for the flags. A panic
// isn't recovered: htmlstream isn't meant to panic on any input, and the
// resulting trap is reported by the page.
package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
	"unsafe"

	"github.com/pgaskin/htmlstream"
	"github.com/pgaskin/htmlstream/polyglot"
)

func main() {}

// live holds the buffers returned by alloc, since the host only has their
// addresses. last is the most recent result, for the same reason.
var (
	live = map[uint32][]byte{}
	last []byte
)

func addr(b []byte) uint32 { return uint32(uintptr(unsafe.Pointer(unsafe.SliceData(b)))) }

func view(p, n uint32) []byte {
	if n == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Add(nil, p)), n)
}

//go:wasmexport alloc
func alloc(n uint32) uint32 {
	b := make([]byte, max(n, 1))
	p := addr(b)
	live[p] = b
	return p
}

//go:wasmexport release
func release(p uint32) {
	delete(live, p)
}

// result finishes a buffer which starts with four bytes reserved for the
// length, and returns its address.
func result(b []byte) uint32 {
	n := uint32(len(b) - 4)
	b[0], b[1], b[2], b[3] = byte(n), byte(n>>8), byte(n>>16), byte(n>>24)
	last = b
	return addr(b)
}

//go:wasmexport defaults
func defaults() uint32 {
	b := []byte{0, 0, 0, 0}
	b = append(b, `{"maxTokenSize":`...)
	b = strconv.AppendInt(b, htmlstream.DefaultMaxTokenSize, 10)
	b = append(b, `,"maxDepth":`...)
	b = strconv.AppendInt(b, htmlstream.DefaultMaxDepth, 10)
	b = append(b, `,"maxStackBytes":`...)
	b = strconv.AppendInt(b, htmlstream.DefaultMaxStackBytes, 10)
	b = append(b, `,"maxRawText":`...)
	b = strconv.AppendInt(b, polyglot.DefaultMaxRawText, 10)
	return result(append(b, '}'))
}

//go:wasmexport polyglottify
func polyglottify(in, inLen, opts, optsLen uint32) uint32 {
	return result(convert(string(view(in, inLen)), view(opts, optsLen)))
}

// An optionReader reads the options, which are, in order: a byte of flags
// (strict 1, finalNewline 2, xmlSyntax 4, scripting 8); the limits maxDepth,
// maxRawText, maxTokenSize and maxStackBytes as uint32 (0 for the default);
// and the strings indent, indentPrefix, doctype and xmlDecl, each a uint32
// length and the bytes. Integers are little-endian.
type optionReader []byte

func (r *optionReader) byte() (v byte) {
	if len(*r) > 0 {
		v, *r = (*r)[0], (*r)[1:]
	}
	return
}

func (r *optionReader) int() int {
	if len(*r) < 4 {
		*r = nil
		return 0
	}
	b := *r
	*r = b[4:]
	return int(uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24)
}

func (r *optionReader) string() string {
	n := min(r.int(), len(*r))
	s := string((*r)[:n])
	*r = (*r)[n:]
	return s
}

// appendString appends s as a JSON string, with invalid UTF-8 replaced like
// the host's decoder would.
func appendString(b []byte, s string) []byte {
	const hex = "0123456789abcdef"
	b = append(b, '"')
	for i := 0; i < len(s); {
		switch c := s[i]; {
		case c == '"' || c == '\\':
			b = append(b, '\\', c)
		case c == '\n':
			b = append(b, '\\', 'n')
		case c == '\r':
			b = append(b, '\\', 'r')
		case c == '\t':
			b = append(b, '\\', 't')
		case c < 0x20:
			b = append(b, '\\', 'u', '0', '0', hex[c>>4], hex[c&15])
		case c < utf8.RuneSelf:
			b = append(b, c)
		default:
			r, n := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && n == 1 {
				b = append(b, `�`...)
			} else {
				b = append(b, s[i:i+n]...)
			}
			i += n
			continue
		}
		i++
	}
	return append(b, '"')
}

// appendMessage appends a warning or error as a JSON object. at is nil for a
// message without a position.
func appendMessage(b []byte, at *position, msg string) []byte {
	b = append(b, '{')
	if at != nil {
		b = append(b, `"line":`...)
		b = strconv.AppendInt(b, int64(at.line), 10)
		b = append(b, `,"col":`...)
		b = strconv.AppendInt(b, int64(at.col), 10)
		b = append(b, `,"index":`...)
		b = strconv.AppendInt(b, int64(at.index), 10)
		b = append(b, ',')
	}
	b = append(b, `"msg":`...)
	b = appendString(b, msg)
	return append(b, '}')
}

// failure returns a result with no output or warnings, and the error msg.
func failure(msg string) []byte {
	b := append([]byte{0, 0, 0, 0}, `{"output":"","warnings":[],"error":`...)
	b = appendMessage(b, nil, msg)
	return append(b, '}')
}

// convert converts input with the options o, returning the result (with space
// for the length at the start).
func convert(input string, o []byte) []byte {
	r := optionReader(o)
	flags := r.byte()
	maxDepth, maxRawText, maxTokenSize, maxStackBytes := r.int(), r.int(), r.int(), r.int()
	indent, indentPrefix, doctype, xmlDecl := r.string(), r.string(), r.string(), r.string()

	var opt polyglot.Options
	opt.Strict = flags&1 != 0
	opt.FinalNewline = flags&2 != 0
	opt.Indent = indent
	opt.IndentPrefix = indentPrefix
	if strings.Trim(opt.Indent, " \t") != "" || strings.Trim(opt.IndentPrefix, " \t") != "" {
		return failure("invalid indent or indent prefix: must have only spaces and tabs")
	}
	switch v := doctype; v {
	case "", "default":
	case "keep":
		opt.DoctypeMode = polyglot.KeepDoctype
	case "none":
		opt.DoctypeMode = polyglot.NoDoctype
	default:
		if len(v) < 9 || !strings.EqualFold(v[:9], "<!DOCTYPE") || !strings.HasSuffix(v, ">") {
			return failure("invalid custom doctype: must be a doctype declaration")
		}
		opt.Doctype = v
	}
	switch v := xmlDecl; v {
	case "", "none":
	case "add":
		opt.XMLDecl = polyglot.AddXMLDecl
	case "keep":
		opt.XMLDecl = polyglot.KeepXMLDecl
	default:
		return failure(fmt.Sprintf("invalid xml declaration mode %q", v))
	}
	opt.MaxDepth = maxDepth
	opt.MaxRawText = maxRawText

	pos := newPositioner(input)
	var warnings []byte
	opt.Warn = func(w polyglot.Warning) {
		if len(warnings) > 0 {
			warnings = append(warnings, ',')
		}
		warnings = appendMessage(warnings, pos.at(w.Pos), w.Msg)
	}

	rd := htmlstream.NewReader(strings.NewReader(input))
	rd.SetStrict(opt.Strict)
	rd.SetXMLSyntax(flags&4 != 0)
	rd.SetScripting(flags&8 != 0)
	if maxTokenSize > 0 {
		rd.SetMaxTokenSize(maxTokenSize)
	}
	if maxDepth > 0 {
		rd.SetMaxDepth(maxDepth)
	}
	if maxStackBytes > 0 {
		rd.SetMaxStackBytes(maxStackBytes)
	}

	var sb strings.Builder
	pw := polyglot.NewWriter(&sb, opt)
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

	b := make([]byte, 4, 4+len(`{"output":,"warnings":[],"error":`)+sb.Len()+len(warnings)+64)
	b = append(b, `{"output":`...)
	b = appendString(b, sb.String())
	b = append(b, `,"warnings":[`...)
	b = append(b, warnings...)
	b = append(b, ']')
	if err != nil {
		b = append(b, `,"error":`...)
		if se, ok := errors.AsType[*htmlstream.SyntaxError](err); ok {
			b = appendMessage(b, pos.at(se.Pos), se.Msg)
		} else if pe, ok := errors.AsType[*polyglot.Error](err); ok {
			b = appendMessage(b, pos.at(pe.Pos), pe.Msg)
		} else {
			b = appendMessage(b, nil, err.Error())
		}
	}
	return append(b, '}')
}

type position struct{ line, col, index int }

// positioner converts byte offsets in a string to lines, columns and UTF-16
// indexes, scanning forward from the last offset.
type positioner struct {
	s                 string
	off, line, col, u int
}

func newPositioner(s string) *positioner {
	return &positioner{s: s, line: 1, col: 1}
}

func (p *positioner) at(pos htmlstream.Pos) *position {
	off := min(max(int(pos), 0), len(p.s))
	if off < p.off {
		*p = *newPositioner(p.s)
	}
	for p.off < off {
		r, n := utf8.DecodeRuneInString(p.s[p.off:])
		switch {
		case r == '\n', r == '\r' && !strings.HasPrefix(p.s[p.off+n:], "\n"):
			p.line++
			p.col = 1
		case r == '\r':
		default:
			p.col++
		}
		if r >= 0x10000 {
			p.u += 2
		} else {
			p.u++
		}
		p.off += n
	}
	return &position{p.line, p.col, p.u}
}
