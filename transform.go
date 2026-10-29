package htmlstream

import (
	"bufio"
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io"
	"iter"
	"slices"
	"strconv"
	"sync/atomic"

	"github.com/pgaskin/htmlstream/ascii"
	"github.com/pgaskin/htmlstream/internal"
)

// Transformer reads tokens like a [Reader] and writes them to an io.Writer,
// applying edits made to the current token. Tokens without edits are written
// unchanged, so if no edits are made, the output is identical to the input.
// It has the methods of a Reader, and [Transformer.Reader] returns the Reader
// it reads with.
//
// Each token is written when the Transformer advances past it, including
// tokens skipped by [Transformer.Skip], [Transformer.Children],
// [Transformer.Find] and [Transformer.ReadText].
// Edits apply to the current token, and edits to a start tag can also affect
// the element's end tag.
//
// Attribute edits apply to the start tag as edited so far: attributes are
// matched by the names given by RenameAttr, and attributes added by SetAttr
// and SetAttrAt can be changed, renamed and removed like the original ones,
// keeping their position.
//
// The values set are written in double quotes, with &, <, >, ", ', tabs,
// newlines and carriage returns written as character references, so XML
// parsers don't change the whitespace to spaces
// ([attribute-value normalization]) and HTML parsers don't change carriage
// returns to newlines ([preprocessing the input stream]). On elements read
// under XML rules (those for which [Token.HTMLRules] reports false: in [XML]
// mode, and in documents read like in XML mode in [Universal] mode) in
// documents declared as XML 1.1, NEL and U+2028, which are line ends in XML
// 1.1, and the control characters other than tab, newline, carriage return
// and NUL, which it only allows as character references, are written as
// character references too (see Modes in the package documentation). HTML
// and XML parsers read the values as given, except for the characters XML
// doesn't allow (in XML 1.1, NUL), which aren't replaced (see
// [AppendXMLChars]). Names passed to the editing methods are written as
// given, without checks (see [ValidXMLName]). Markup passed as a []byte (e.g.,
// to Replace, InsertBefore, InsertAfter, Wrap and HoldDiscard) is copied, so
// the caller can reuse the slice.
//
// Output can be held back (see [Transformer.Hold]) to decide what to do with
// it after seeing the tokens which follow.
//
// The output is buffered: call [Transformer.Close] after the last token, or
// the output can be incomplete. Like a Reader, a Transformer isn't safe for
// concurrent use, and transforms a single input.
//
// The editing methods apply to the current token, and each one documents the
// kinds of token it accepts. Called without a current token or on another
// kind of token, they return an error wrapping [ErrInvalidOp] and do
// nothing else.
//
// Errors are sticky and can be retrieved with Err or Close, except
// [ErrInvalidOp], [ErrIncompleteTag], [ErrHeldOpen] and [ErrHoldReleased],
// which only mean the operation wasn't done. On error, the output is
// incomplete.
//
// [attribute-value normalization]: https://www.w3.org/TR/2006/REC-xml-20060816/#AVNormalize
// [preprocessing the input stream]: https://html.spec.whatwg.org/multipage/parsing.html#preprocessing-the-input-stream
type Transformer struct {
	r *Reader
	w *bufio.Writer

	// edits to the current token
	edited    bool
	remove    bool
	replace   []byte
	replaced  bool
	before    []byte
	wrapStart []byte // written after before
	after     []byte
	name      []byte
	renamed   bool
	selfClose int8 // 1 to add self-closing syntax, -1 to remove it
	closeTag  bool // write an end tag after the start tag
	dropAfter bool // drop the element's content and end tag after writing the start tag
	attrOps   []attrOp
	newAttrs  []newAttr

	drop int // drop tokens until the end of the element at this depth

	// actions of untracked elements (see Token.Untracked), innermost last
	uframes []uframe

	tail []byte // written after the last token

	// output held back (see Hold)
	holds   []hold
	hbuf    []byte
	maxHold int
	holdID  uint64 // identifies the Transformer in its Holds (0 before the first one)
	holdSeq uint64 // the seq of the last Hold

	recs []frameRec
	free []int32
	buf  []byte
	abuf []byte // attributes passed to RemoveAttrs
}

// ErrInvalidOp is returned by a Transformer method called without a current
// token, on a kind of token it doesn't accept, or in a state which doesn't
// allow it (e.g., HoldRelease of a hold with an open hold inside it). The
// returned error wraps it with details. Unlike other errors, it doesn't stop
// the Transformer, and the operation isn't done. The polyglot package's
// [github.com/pgaskin/htmlstream/polyglot.ErrInvalidOp] differs: it stops
// the conversion.
var ErrInvalidOp = errors.New("htmlstream: invalid operation")

// ErrIncompleteTag is returned when editing a start tag cut off by the end of
// the input. Unlike other errors, it doesn't stop the Transformer, and the
// tag is written unchanged.
var ErrIncompleteTag = errors.New("htmlstream: can't edit an incomplete start tag")

// ErrHeldOpen is returned by [Transformer.HoldDiscard] when the output held
// back has the start tag of an element which is still open and whose end tag
// has edits (from Wrap, Rename or Unwrap). The hold doesn't end, and the
// Transformer continues.
var ErrHeldOpen = errors.New("htmlstream: can't discard held output containing the start tag of an open element (its end tag has edits)")

// ErrHoldReleased is returned by [Transformer.HoldRelease] and
// [Transformer.HoldDiscard] for a hold which has already ended: released,
// discarded, or ended by Close. Like ErrHeldOpen, it doesn't stop the
// Transformer. The holds of [github.com/pgaskin/htmlstream/polyglot.Writer]
// return it too.
var ErrHoldReleased = errors.New("htmlstream: hold already released")

// ErrHoldLimit is returned when the output held back (see [Transformer.Hold])
// exceeds the limit set by [Transformer.SetMaxHold]. The returned error wraps
// it with the sizes.
var ErrHoldLimit = errors.New("htmlstream: held output exceeds MaxHold")

// holdLimit returns an error wrapping ErrHoldLimit for held output of n
// bytes, exceeding limit.
func holdLimit(n, limit int) error {
	return &limitError{ErrHoldLimit, "htmlstream: held output of " + strconv.Itoa(n) + " bytes exceeds MaxHold (" + strconv.Itoa(limit) + " bytes)"}
}

// DefaultMaxHold is the default maximum size of the output held back by a
// Transformer (see [Transformer.SetMaxHold]) and by a polyglot Writer (see
// [github.com/pgaskin/htmlstream/polyglot.Options.MaxHold]), 64 MiB.
const DefaultMaxHold = 64 << 20

// A Hold identifies a hold started by [Transformer.Hold], for
// [Transformer.HoldRelease], [Transformer.HoldDiscard] and [Transformer.Held].
// It is only valid with the Transformer which returned it. The zero Hold is
// no hold.
type Hold struct {
	owner uint64 // Transformer.holdID
	seq   uint64 // hold.seq
}

// holdIDs numbers the Transformers which have started a hold, from 1, so a
// Hold can't be used with another Transformer.
var holdIDs atomic.Uint64

// hold is the start of the output held back by a call to Hold.
type hold struct {
	off int    // in hbuf
	pos Pos    // of the token at which Hold was called
	seq uint64 // numbers the holds of the Transformer, increasing up the stack
}

type frameAct uint8

const (
	actUnwrap frameAct = 1 << iota // drop the end tag
	actRec                         // frame has a frameRec
)

// uframe holds the actions of an untracked element at a depth.
type uframe struct {
	depth int
	f     frame
}

type frameRec struct {
	rename  []byte
	renamed bool
	wrapEnd []byte
}

type attrOp struct {
	remove bool
	set    bool
	value  []byte
	rename []byte
}

type newAttr struct {
	name, value []byte
	at          int32 // index of the original attribute to write it before
}

// NewTransformer returns a Transformer reading from r in [HTML] mode and
// writing to w. For XML documents, set [XML] mode with SetMode (see Modes in
// the package documentation).
func NewTransformer(w io.Writer, r io.Reader) *Transformer {
	t := &Transformer{
		r:       NewReader(r),
		w:       bufio.NewWriter(w),
		maxHold: DefaultMaxHold,
	}
	t.r.sink = t.emit
	return t
}

// Reader returns the Reader the Transformer reads with, for functions taking
// a Reader (e.g., htmlutil.InnerText). Tokens read with it are written like
// those read with the Transformer's methods, and edits apply to its current
// token.
func (t *Transformer) Reader() *Reader { return t.r }

// See [Reader.SetMode].
func (t *Transformer) SetMode(m Mode) { t.r.SetMode(m) }

// See [Reader.Mode].
func (t *Transformer) Mode() Mode { return t.r.Mode() }

// See [Reader.EffectiveMode].
func (t *Transformer) EffectiveMode() Mode { return t.r.EffectiveMode() }

// See [Reader.SetXMLSyntax].
func (t *Transformer) SetXMLSyntax(v bool) { t.r.SetXMLSyntax(v) }

// See [Reader.SetScripting].
func (t *Transformer) SetScripting(v bool) { t.r.SetScripting(v) }

// See [Reader.Scripting].
func (t *Transformer) Scripting() bool { return t.r.Scripting() }

// See [Reader.SetStrict].
func (t *Transformer) SetStrict(v bool) { t.r.SetStrict(v) }

// See [Reader.SetKeepAttrs].
func (t *Transformer) SetKeepAttrs(v bool) { t.r.SetKeepAttrs(v) }

// See [Reader.SetMaxTokenSize].
func (t *Transformer) SetMaxTokenSize(n int) { t.r.SetMaxTokenSize(n) }

// See [Reader.SetMaxDepth].
func (t *Transformer) SetMaxDepth(n int) { t.r.SetMaxDepth(n) }

// See [Reader.SetMaxStackBytes].
func (t *Transformer) SetMaxStackBytes(n int) { t.r.SetMaxStackBytes(n) }

// See [Reader.SetChunkSize].
func (t *Transformer) SetChunkSize(n int) { t.r.SetChunkSize(n) }

// See [Reader.Next].
func (t *Transformer) Next() bool { return t.r.Next() }

// See [Reader.Token].
func (t *Transformer) Token() *Token { return t.r.Token() }

// See [Reader.Err].
func (t *Transformer) Err() error { return t.r.Err() }

// See [Reader.All].
func (t *Transformer) All() iter.Seq[*Token] { return t.r.All() }

// See [Reader.Depth].
func (t *Transformer) Depth() int { return t.r.Depth() }

// See [Reader.Stack].
func (t *Transformer) Stack() iter.Seq[Element] { return t.r.Stack() }

// See [Reader.LookupPrefix].
func (t *Transformer) LookupPrefix(prefix string) (string, bool) { return t.r.LookupPrefix(prefix) }

// See [Reader.Skip].
func (t *Transformer) Skip() error { return t.r.Skip() }

// See [Reader.Children].
func (t *Transformer) Children() iter.Seq[*Token] { return t.r.Children() }

// See [Reader.Find].
func (t *Transformer) Find(m Matcher) bool { return t.r.Find(m) }

// See [Reader.ReadText].
func (t *Transformer) ReadText(dst []byte, limit int) ([]byte, bool) { return t.r.ReadText(dst, limit) }

// Close writes the remaining tokens, the output still held back (see
// [Transformer.Hold]) and anything inserted with InsertAtEnd, flushes the
// output and returns the first error encountered. It doesn't close the
// [io.Writer] passed to [NewTransformer]. Calling Close again returns the
// same error, and writes nothing more unless InsertAtEnd was called since.
func (t *Transformer) Close() error {
	for t.Next() {
	}
	if t.r.err == nil {
		t.holds = t.holds[:0]
		t.w.Write(t.hbuf)
		t.hbuf = t.hbuf[:0]
		t.w.Write(t.tail)
		t.tail = t.tail[:0]
		t.setErr(t.w.Flush())
	}
	return t.r.err
}

// InsertAtEnd writes raw at the end of the output, after the last token, when
// the Transformer is closed. After an error, which Close returns, it does
// nothing.
func (t *Transformer) InsertAtEnd(raw []byte) {
	if t.r.err == nil {
		t.tail = append(t.tail, raw...)
	}
}

// Hold starts holding back the output from the current token on: the current
// token and the following ones are written with their edits, like
// otherwise, but to a buffer instead of the output, until the hold ends with
// [Transformer.HoldRelease] or [Transformer.HoldDiscard]. The output of
// element operations (Remove, Empty, Skip, Children) on held tokens is held
// too.
//
// A hold started while another is open is inside it, and must end first: its
// output then stays in the enclosing hold. HoldRelease and HoldDiscard of a
// hold with an open hold inside it return an error wrapping [ErrInvalidOp],
// and so do those of the zero Hold and of a hold of another Transformer.
// Those of a hold which has ended return [ErrHoldReleased]. Holds can end
// after the end of the input (when Next returns false), before Close. Close
// ends the holds still open, writing the output they hold back.
//
// The edits of a held token can't be changed after the Transformer advances
// past it, but the edits of an element's end tag (from Wrap, Rename and
// Unwrap of its start tag) apply when the end tag is written, which may be
// after the hold ends.
//
// The held output is limited to [DefaultMaxHold] by default (see
// [Transformer.SetMaxHold]): exceeding it is an error ([ErrHoldLimit]). To
// give up on a hold at a smaller size instead, check the size of
// [Transformer.Held] and end the hold with HoldRelease or HoldDiscard.
//
// The current token can be of any kind.
func (t *Transformer) Hold() (Hold, error) {
	c, err := t.current("Hold called")
	if err != nil {
		return Hold{}, err
	}
	if t.holdID == 0 {
		t.holdID = holdIDs.Add(1)
	}
	t.holdSeq++
	t.holds = append(t.holds, hold{off: len(t.hbuf), pos: c.pos, seq: t.holdSeq})
	return Hold{t.holdID, t.holdSeq}, nil
}

// Holding returns the number of holds which haven't ended.
func (t *Transformer) Holding() int { return len(t.holds) }

// Held returns the output held back by the hold h so far, including that of
// the holds inside it, or nil if h isn't open. It doesn't include the current
// token. It is valid until the Transformer advances or a hold ends.
func (t *Transformer) Held(h Hold) []byte {
	i := t.holdAt(h)
	if i < 0 {
		return nil
	}
	return t.hbuf[t.holds[i].off:]
}

// HoldRelease ends the hold h, writing the output held back (or keeping it in
// the enclosing hold). See [Transformer.Hold] for the errors.
func (t *Transformer) HoldRelease(h Hold) error {
	i, err := t.holdEnd("HoldRelease", h)
	if err != nil {
		return err
	}
	t.holds = t.holds[:i]
	t.flushHeld()
	return nil
}

// HoldDiscard ends the hold h, writing raw (which may be nil) instead of the
// output held back. The current token isn't part of the held output, so
// markup to put before the held output is passed as part of raw, e.g.,
// HoldDiscard(h, append(prefix, t.Held(h)...)); InsertBefore on the current
// token writes after the held output. If a hold encloses h, raw is held back
// in it, and counts toward the limit (see [Transformer.SetMaxHold]).
//
// If the output held back contains the start tag of an element which is still
// open and whose end tag has edits (from Wrap, Rename or Unwrap), the end
// tag would no longer match the start tag written, so HoldDiscard returns
// [ErrHeldOpen] and the hold doesn't end. The end tags of elements without
// such edits are written as usual, like after Replace(nil) on the start tag:
// use Remove on the end tag to drop it.
//
// See [Transformer.Hold] for the other errors.
func (t *Transformer) HoldDiscard(h Hold, raw []byte) error {
	i, err := t.holdEnd("HoldDiscard", h)
	if err != nil {
		return err
	}
	hd := t.holds[i]
	// elements opened by held tokens, which are before the current token (all
	// of them after the end of the input)
	c := t.r.cur
	held := func(f *frame) bool { return f.act != 0 && f.pos >= hd.pos && (c == nil || f.pos < c.pos) }
	for j := range t.r.stack {
		if held(&t.r.stack[j]) {
			return ErrHeldOpen
		}
	}
	for j := range t.uframes {
		if held(&t.uframes[j].f) {
			return ErrHeldOpen
		}
	}
	t.holds = t.holds[:i]
	t.hbuf = append(t.hbuf[:hd.off], raw...)
	if len(t.holds) != 0 && len(t.hbuf) > t.maxHold {
		return t.setErr(holdLimit(len(t.hbuf), t.maxHold))
	}
	t.flushHeld()
	return nil
}

// holdAt returns the index of the open hold h in t.holds, or -1.
func (t *Transformer) holdAt(h Hold) int {
	if h.owner != t.holdID {
		return -1
	}
	if n := len(t.holds); n != 0 && t.holds[n-1].seq == h.seq {
		return n - 1
	}
	// the seqs increase up the stack
	if i, ok := slices.BinarySearchFunc(t.holds, h.seq, func(x hold, seq uint64) int { return cmp.Compare(x.seq, seq) }); ok {
		return i
	}
	return -1
}

// holdEnd returns the index in t.holds of the hold h for the operation op,
// HoldRelease or HoldDiscard, checking that it is the innermost open hold.
func (t *Transformer) holdEnd(op string, h Hold) (int, error) {
	if t.r.err != nil {
		return 0, t.r.err
	}
	switch i := t.holdAt(h); {
	case i >= 0 && i == len(t.holds)-1:
		return i, nil
	case i >= 0:
		return 0, invalid(op + " called with an outer Hold (a hold inside it is open)")
	case h == Hold{}:
		return 0, invalid(op + " called with the zero Hold")
	case h.owner != t.holdID:
		return 0, invalid(op + " called with a hold created by a different Transformer")
	}
	return 0, ErrHoldReleased
}

// SetMaxHold sets the maximum size of the output held back (see
// [Transformer.Hold]). The default is [DefaultMaxHold]. Negative values are
// treated as 0, with which holding back any output is an error.
func (t *Transformer) SetMaxHold(n int) {
	t.maxHold = max(n, 0)
}

// flushHeld writes the held output if there is no hold left.
func (t *Transformer) flushHeld() {
	if len(t.holds) == 0 && len(t.hbuf) != 0 {
		t.w.Write(t.hbuf)
		t.hbuf = t.hbuf[:0]
	}
}

// write writes b to the output, or to the held output.
func (t *Transformer) write(b []byte) {
	if len(t.holds) == 0 {
		t.w.Write(b)
		return
	}
	t.hbuf = append(t.hbuf, b...)
	if len(t.hbuf) > t.maxHold {
		t.setErr(holdLimit(len(t.hbuf), t.maxHold))
	}
}

func (t *Transformer) setErr(err error) error {
	if t.r.err == nil && err != nil {
		t.r.err = err
	}
	return t.r.err
}

// invalid returns an error wrapping ErrInvalidOp with the message.
func invalid(msg string) error {
	return fmt.Errorf("%w: %s", ErrInvalidOp, msg)
}

// kindNames are the descriptions of token kinds in error messages.
var kindNames = [...]string{
	Text:     "a text token",
	StartTag: "a start tag",
	EndTag:   "an end tag",
	Comment:  "a comment",
	CDATA:    "a CDATA section",
	Doctype:  "a doctype",
	ProcInst: "a processing instruction",
}

// current returns the current token for the operation op (described as in
// error messages), checking it is one of kinds, if any.
func (t *Transformer) current(op string, kinds ...Kind) (*Token, error) {
	if t.r.err != nil {
		return nil, t.r.err
	}
	c := t.r.cur
	if c == nil {
		return nil, invalid(op + " with no current token")
	}
	if len(kinds) != 0 && !slices.Contains(kinds, c.kind) {
		return nil, invalid(op + " of " + kindNames[c.kind])
	}
	return c, nil
}

// startTag returns the current start tag for the operation op, checking it
// can be edited.
func (t *Transformer) startTag(op string) (*Token, error) {
	c, err := t.current(op, StartTag)
	if err != nil {
		return nil, err
	}
	if c.flags&tokIncomplete != 0 {
		return nil, ErrIncompleteTag
	}
	if t.r.strict && c.flags&tokUnsound != 0 {
		return nil, t.setErr(c.UnsoundError())
	}
	return c, nil
}

// element returns the frame holding the actions of the element opened by the
// current start tag, and its depth, or nil if it doesn't open an element.
func (t *Transformer) element() (*frame, int, error) {
	depth, err := t.r.elementStart()
	if err != nil {
		return nil, 0, t.setErr(err)
	}
	if depth == 0 {
		return nil, 0, nil
	}
	if !t.r.cur.Untracked() {
		return &t.r.stack[len(t.r.stack)-1], depth, nil
	}
	if n := len(t.uframes); n == 0 || t.uframes[n-1].depth != depth {
		if n >= t.r.maxDepth {
			return nil, 0, t.setErr(t.r.untrackedLimit())
		}
		t.uframes = append(t.uframes, uframe{depth: depth, f: frame{pos: t.r.cur.pos}})
	}
	return &t.uframes[len(t.uframes)-1].f, depth, nil
}

// endOf returns the frame holding the actions of the element closed by the
// end tag c, or nil if there is none.
func (t *Transformer) endOf(c *Token) *frame {
	if c.kind != EndTag || c.flags&tokStray != 0 {
		return nil
	}
	if c.spos >= 0 {
		return &t.r.stack[len(t.r.stack)-1]
	}
	if n := len(t.uframes); n != 0 && t.uframes[n-1].depth == t.Depth() {
		return &t.uframes[n-1].f
	}
	return nil
}

func (t *Transformer) rec(f *frame) *frameRec {
	if f.act&actRec == 0 {
		if n := len(t.free); n != 0 {
			f.aux = t.free[n-1]
			t.free = t.free[:n-1]
		} else {
			f.aux = int32(len(t.recs))
			t.recs = append(t.recs, frameRec{})
		}
		f.act |= actRec
	}
	return &t.recs[f.aux]
}

// Remove removes the current token. If it is a start tag which opens an
// element, the element's contents and end tag are also removed, and the
// current token becomes the element's end tag. Markup inserted before the
// start tag and the markup passed to [Transformer.Wrap] are still written;
// markup inserted after the start tag is inside the element and is removed
// with it.
//
// A void element under XML rules or with XML syntax (e.g., <br>, see
// Element model in the package documentation) opens an element only up to
// its end tag if that follows it directly or after whitespace, so only then
// is the end tag removed with it: in <br>x</br>, the end tag is stray, and
// stays in the output, as it does for [Transformer.Unwrap],
// [Transformer.Rename] and [Transformer.Empty].
//
// The current token can be of any kind.
func (t *Transformer) Remove() error {
	c, err := t.current("removal")
	if err != nil {
		return err
	}
	if c.kind == StartTag {
		f, depth, err := t.element()
		if err != nil {
			return err
		}
		if f != nil {
			t.write(t.before)
			t.write(t.wrapStart)
			t.before, t.wrapStart = t.before[:0], t.wrapStart[:0]
			t.remove = true
			t.drop = depth
			return t.skipToEnd(depth)
		}
	}
	t.remove = true
	return nil
}

// skipToEnd advances to the end tag of the element at depth.
func (t *Transformer) skipToEnd(depth int) error {
	for t.Next() {
		if t.r.atEnd(depth) {
			return nil
		}
	}
	return t.setErr(t.r.err)
}

// Replace replaces the current token with raw. If the current token is a
// start tag which opens an element, only the start tag is replaced.
//
// The current token can be of any kind.
func (t *Transformer) Replace(raw []byte) error {
	if _, err := t.current("replacement"); err != nil {
		return err
	}
	t.replace = append(t.replace[:0], raw...)
	t.replaced = true
	return nil
}

// InsertBefore writes raw before the current token, outside the wraps of a
// start tag (see [Transformer.Wrap]).
//
// The current token can be of any kind.
func (t *Transformer) InsertBefore(raw []byte) error {
	if _, err := t.current("insertion"); err != nil {
		return err
	}
	t.before = append(t.before, raw...)
	return nil
}

// InsertAfter writes raw after the current token. For a start tag which
// opens an element, this is inside the element, before its contents:
// [Transformer.Remove] removes it with the element, and [Transformer.Empty]
// keeps it in the element.
//
// An implied end tag (see [Token.Implied]) writes nothing, so HTML parsers
// read markup inserted after it as inside the element, which they close
// later (e.g., after the implied end tag of the p in <div><p>x</div>). To
// close the element first, replace the implied end tag with an end tag (see
// [AppendEndTag]).
//
// The current token can be of any kind.
func (t *Transformer) InsertAfter(raw []byte) error {
	if _, err := t.current("insertion"); err != nil {
		return err
	}
	t.after = append(t.after, raw...)
	return nil
}

// Unwrap removes the current start tag and, if it opens an element, the
// element's end tag, keeping the element's contents.
//
// The current token must be a start tag.
func (t *Transformer) Unwrap() error {
	if _, err := t.startTag("unwrap"); err != nil {
		return err
	}
	f, _, err := t.element()
	if err != nil {
		return err
	}
	if f != nil {
		f.act |= actUnwrap
	}
	t.remove = true
	return nil
}

// Wrap writes start before the current start tag, and end after the end tag
// of the element it opens (or after the start tag if it doesn't open an
// element). Later wraps are inside earlier ones. Markup inserted before the
// start tag (see [Transformer.InsertBefore]), and after the element (with
// [Transformer.InsertAfter] at its end tag, or at the start tag if it doesn't
// open an element), is outside the wrap, whatever the order of the calls.
//
// Under HTML rules, end is written after the element's end tag also when it
// is implied (see [Token.Implied]), where HTML parsers still have the element
// open, like markup inserted after it (see [Transformer.InsertAfter]): e.g.,
// wrapping the p of <p>a<div>b</div> in a span gives
// <span><p>a</span><div>b</div>, where HTML parsers ignore the </span>, since
// the p is open, and put the div in the span. To close the element first,
// replace the implied end tag with an end tag (see [AppendEndTag]).
//
// The current token must be a start tag.
func (t *Transformer) Wrap(start, end []byte) error {
	if _, err := t.startTag("wrap"); err != nil {
		return err
	}
	f, _, err := t.element()
	if err != nil {
		return err
	}
	t.wrapStart = append(t.wrapStart, start...)
	if f != nil {
		r := t.rec(f)
		r.wrapEnd = append(append([]byte(nil), end...), r.wrapEnd...)
	} else {
		t.after = append(append([]byte(nil), end...), t.after...)
	}
	return nil
}

// TODO: RenameNS for namespace-aware renaming (automatically adding namespace declaration)

// Rename renames the current start tag and the end tag of the element it
// opens, if any. Implied end tags are not written. A name with a namespace
// prefix needs a declaration of the prefix in scope, which Rename doesn't
// add (e.g., add an xmlns:prefix attribute with SetAttr). The stray end tag
// of a void element under XML rules or with XML syntax (e.g., the </br> in
// <br>x</br>, see [Transformer.Remove]) isn't renamed.
//
// The current token must be a start tag.
func (t *Transformer) Rename(name string) error {
	if _, err := t.startTag("rename"); err != nil {
		return err
	}
	f, _, err := t.element()
	if err != nil {
		return err
	}
	t.edited = true
	t.name = append(t.name[:0], name...)
	t.renamed = true
	if f != nil {
		r := t.rec(f)
		r.rename = append(r.rename[:0], name...)
		r.renamed = true
	}
	return nil
}

// RenameEnd renames the current end tag. Implied end tags are not written.
//
// The current token must be an end tag.
func (t *Transformer) RenameEnd(name string) error {
	if _, err := t.current("end tag rename", EndTag); err != nil {
		return err
	}
	t.name = append(t.name[:0], name...)
	t.renamed = true
	return nil
}

// ExpandSelfClosing rewrites a self-closing start tag like <x/> as <x></x>.
// It removes all the "/" characters directly before the ">", since HTML
// parsers read <x//> as self-closing too. If the self-closing syntax isn't
// honored (e.g., <div/> in HTML mode, which opens an element), or the element
// is an HTML void element under HTML rules (e.g., <br/>), only these "/"
// characters are removed. The end tag is part of the start
// tag's edits: Remove, Replace and Unwrap remove it with the start tag.
//
// The current token must be a start tag.
func (t *Transformer) ExpandSelfClosing() error {
	c, err := t.startTag("self-closing tag expansion")
	if err != nil {
		return err
	}
	if !c.SelfClosing() {
		return nil
	}
	t.edited = true
	t.selfClose = -1
	t.closeTag = !c.Opens() && !htmlVoid(c)
	return nil
}

// htmlVoid reports whether the start tag c is an HTML void element under HTML
// rules.
func htmlVoid(c *Token) bool {
	return c.ns == nsHTML && c.HTMLRules() && internal.HTMLElem(lowerASCII(make([]byte, 0, 16), c.Name()))&internal.Void != 0
}

// Empty removes the contents of the element opened by the current start tag
// and its end tag, and writes it as an empty element. The current token
// becomes the element's end tag. Markup inserted after the start tag (see
// [Transformer.InsertAfter]) stays inside the element, and markup inserted
// before or after the end tag is written after the element.
//
// HTML elements under HTML rules (see [Token.HTMLRules]) other than void
// elements are written as <x ...></x>, since HTML parsers ignore
// self-closing syntax on them. Other elements are written with self-closing
// syntax, like <x .../>, which XML parsers and, for SVG and MathML elements,
// HTML parsers read as empty, or as <x ...></x> to keep markup inserted after
// the start tag inside the element. Void elements are always written with
// self-closing syntax, with the markup inserted after the start tag following
// them. The stray end tag of a void element under XML rules or with XML
// syntax (e.g., the </br> in <br>x</br>, see [Transformer.Remove]) stays in
// the output, after the contents following the element.
//
// The current token must be a start tag.
func (t *Transformer) Empty() error {
	c, err := t.startTag("emptying")
	if err != nil {
		return err
	}
	f, depth, err := t.element()
	if err != nil {
		return err
	}
	// markup inserted after the start tag of an element other than a void
	// element is written inside it, so it needs an end tag
	inside := f != nil && len(t.after) != 0 && !(c.Untracked() && t.r.voidOpen || !c.Untracked() && f.flags&frameXMLVoid != 0)
	t.edited = true
	t.selfClose = 0
	if c.ns == nsHTML && c.HTMLRules() && !htmlVoid(c) || inside {
		if c.SelfClosing() {
			t.selfClose = -1
		}
		t.closeTag = true
	} else {
		if !c.SelfClosing() {
			t.selfClose = 1
		}
		t.closeTag = false
	}
	if f == nil {
		return nil
	}
	t.dropAfter = true
	return t.skipToEnd(depth)
}

// SetAttr sets the value of the first attribute named name (see
// [Attr.Is]) on the current start tag, keeping its position, or adds one at
// the end like [Transformer.SetAttrAt] with position -1. The value is
// escaped as described in [Transformer].
//
// The current token must be a start tag.
func (t *Transformer) SetAttr(name, value string) error {
	c, err := t.startTag("attribute edit")
	if err != nil {
		return err
	}
	t.edited = true
	t.initAttrOps(c)
	for i := range c.attrs {
		if !t.attrOps[i].remove && t.attrIs(c, i, name) {
			t.attrOps[i].set = true
			t.attrOps[i].value = append(t.attrOps[i].value[:0], value...)
			return nil
		}
	}
	for i := range t.newAttrs {
		if c.attrNameIs(t.newAttrs[i].name, name) {
			t.newAttrs[i].value = append(t.newAttrs[i].value[:0], value...)
			return nil
		}
	}
	t.addAttr(name, value, int32(len(c.attrs)))
	return nil
}

// SetAttrAt removes the attributes named name (see [Attr.Is]) from the current
// start tag and adds one with the value at position at in the original
// attributes: 0 is before the first, 1 before the second, -1 after the last,
// -2 before the last. Attributes added at the same position are written in the
// order they were added. The value is escaped as described in [Transformer].
//
// Positions count the attributes of the start tag as [Token.Attrs] returns
// them, including duplicates, so they don't change with other edits to the
// tag. With n attributes, at must be between -(n+1) and n; otherwise,
// SetAttrAt returns an error wrapping [ErrInvalidOp].
//
// The current token must be a start tag.
func (t *Transformer) SetAttrAt(name, value string, at int) error {
	c, err := t.startTag("attribute edit")
	if err != nil {
		return err
	}
	n := len(c.attrs)
	pos := at
	if pos < 0 {
		pos += n + 1
	}
	if pos < 0 || pos > n {
		return invalid("attribute position " + strconv.Itoa(at) + " out of range (the tag has " + strconv.Itoa(n) + " attributes)")
	}
	t.edited = true
	t.initAttrOps(c)
	for i := range c.attrs {
		if !t.attrOps[i].remove && t.attrIs(c, i, name) {
			t.attrOps[i].remove = true
		}
	}
	t.newAttrs = slices.DeleteFunc(t.newAttrs, func(a newAttr) bool {
		return c.attrNameIs(a.name, name)
	})
	t.addAttr(name, value, int32(pos))
	return nil
}

// addAttr adds an attribute before the original attribute at, reusing the
// buffers of earlier tags.
func (t *Transformer) addAttr(name, value string, at int32) {
	n := len(t.newAttrs)
	if n < cap(t.newAttrs) {
		t.newAttrs = t.newAttrs[:n+1]
	} else {
		t.newAttrs = append(t.newAttrs, newAttr{})
	}
	a := &t.newAttrs[n]
	a.name = append(a.name[:0], name...)
	a.value = append(a.value[:0], value...)
	a.at = at
}

// RenameAttr renames the attributes named name (see [Attr.Is]) on the current
// start tag to newName, keeping their position and value.
//
// The current token must be a start tag.
func (t *Transformer) RenameAttr(name, newName string) error {
	c, err := t.startTag("attribute edit")
	if err != nil {
		return err
	}
	t.initAttrOps(c)
	for i := range c.attrs {
		if !t.attrOps[i].remove && t.attrIs(c, i, name) {
			t.attrOps[i].rename = []byte(newName)
			t.edited = true
		}
	}
	for i := range t.newAttrs {
		if c.attrNameIs(t.newAttrs[i].name, name) {
			t.newAttrs[i].name = []byte(newName)
		}
	}
	return nil
}

// RemoveAttr removes all attributes named name (see [Attr.Is]) from the
// current start tag.
//
// The current token must be a start tag.
func (t *Transformer) RemoveAttr(name string) error {
	return t.removeAttrs(func(a Attr) bool { return a.Is(name) })
}

// RemoveAttrs removes the attributes matching fn from the current start tag.
// fn is called with each attribute as edited so far, including added ones. An
// edited or added attribute passed to fn is only valid during the call.
//
// The current token must be a start tag.
func (t *Transformer) RemoveAttrs(fn func(Attr) bool) error {
	return t.removeAttrs(fn)
}

func (t *Transformer) removeAttrs(fn func(Attr) bool) error {
	c, err := t.startTag("attribute edit")
	if err != nil {
		return err
	}
	t.initAttrOps(c)
	for i, s := range c.attrs {
		op := &t.attrOps[i]
		if op.remove {
			continue
		}
		a := c.attr(s)
		if op.set || op.rename != nil {
			a = t.editedAttr(c, i)
		}
		if fn(a) {
			op.remove = true
			t.edited = true
		}
	}
	t.newAttrs = slices.DeleteFunc(t.newAttrs, func(a newAttr) bool {
		return fn(t.makeAttr(c, a.name, a.value))
	})
	return nil
}

// attrIs reports whether the original attribute i of c is named name after
// the edits so far.
func (t *Transformer) attrIs(c *Token, i int, name string) bool {
	if n := t.attrOps[i].rename; n != nil {
		return c.attrNameIs(n, name)
	}
	s := c.attrs[i]
	return c.attrNameIs(c.Raw()[s.name.start:s.name.end], name)
}

// editedAttr returns the original attribute i of c after the edits so far.
func (t *Transformer) editedAttr(c *Token, i int) Attr {
	s := c.attrs[i]
	op := &t.attrOps[i]
	raw := c.Raw()
	name := raw[s.name.start:s.name.end]
	if op.rename != nil {
		name = op.rename
	}
	if op.set {
		return t.makeAttr(c, name, op.value)
	}
	// renamed, with the original value
	b := append(t.abuf[:0], name...)
	off := int32(len(name)) - s.name.end
	a := s
	a.name = span{0, int32(len(name))}
	if s.hasVal {
		a.val = span{s.val.start + off, s.val.end + off}
		end := s.val.end
		if s.quote != 0 {
			end++
		}
		b = append(b, raw[s.name.end:end]...)
	}
	t.abuf = b
	return c.attrIn(b, a)
}

// makeAttr returns an attribute of c with the name and value.
func (t *Transformer) makeAttr(c *Token, name, value []byte) Attr {
	b := append(t.abuf[:0], name...)
	b = append(b, `="`...)
	vs := int32(len(b))
	b = appendAttrValue(b, value, c)
	a := attrSpan{span{0, int32(len(name))}, span{vs, int32(len(b))}, '"', true}
	b = append(b, '"')
	t.abuf = b
	return c.attrIn(b, a)
}

// appendAttrValue appends value to b, escaped as the value of an attribute
// set on the start tag c (see [Transformer]).
func appendAttrValue(b, value []byte, c *Token) []byte {
	if c.xmlRules() && c.flags&tokXML11 != 0 {
		// the line ends of XML 1.1, NEL and U+2028, and the control
		// characters it only allows as character references (other than
		// NUL, which it doesn't allow)
		start := 0
		for i := 0; i < len(value); {
			r, n := rune(value[i]), 1
			switch ch := value[i]; {
			case ch < 0x20 && ch != 0 && ch != '\t' && ch != '\n' && ch != '\r', ch == 0x7F:
			case ch == 0xC2 && i+1 < len(value) && value[i+1] >= 0x80 && value[i+1] <= 0x9F:
				r, n = rune(value[i+1]), 2
			case xml11LineEnd(value[i:]) == 3:
				r, n = 0x2028, 3
			default:
				i++
				continue
			}
			b = internal.AppendEscape(b, value[start:i], internal.EscapeAttr)
			b = strconv.AppendUint(append(b, "&#x"...), uint64(r), 16)
			b = append(b, ';')
			i += n
			start = i
		}
		value = value[start:]
	}
	return internal.AppendEscape(b, value, internal.EscapeAttr)
}

func (t *Transformer) initAttrOps(c *Token) {
	if len(t.attrOps) != len(c.attrs) {
		t.attrOps = slices.Grow(t.attrOps[:0], len(c.attrs))[:len(c.attrs)]
		for i := range t.attrOps {
			t.attrOps[i] = attrOp{value: t.attrOps[i].value[:0]}
			t.attrOps[i].rename = nil
		}
	}
}

func (c *Token) attr(s attrSpan) Attr {
	return c.attrIn(c.Raw(), s)
}

// attrNameIs reports whether the attribute name n of c is name, compared like
// [Attr.Is].
func (c *Token) attrNameIs(n []byte, name string) bool {
	if c.HTMLRules() {
		return htmlNameIs(n, name)
	}
	return string(n) == name
}

func (t *Transformer) resetEdits() {
	t.edited = false
	t.remove = false
	t.replace = t.replace[:0]
	t.replaced = false
	t.before = t.before[:0]
	t.wrapStart = t.wrapStart[:0]
	t.after = t.after[:0]
	t.name = t.name[:0]
	t.renamed = false
	t.selfClose = 0
	t.closeTag = false
	t.dropAfter = false
	t.attrOps = t.attrOps[:0]
	t.newAttrs = t.newAttrs[:0]
}

// emit writes a token before the Reader advances past it.
func (t *Transformer) emit(c *Token) {
	defer t.resetEdits()
	if t.drop > 0 {
		if t.r.atEnd(t.drop) {
			// the end of a removed or emptied element: write the markup
			// inserted around its end tag and the end of its wrap
			t.drop = 0
			t.write(t.before)
			if f := t.endOf(c); f != nil && f.act&actRec != 0 {
				t.write(t.recs[f.aux].wrapEnd)
			}
			t.write(t.after)
			t.endFrame(c)
		}
		return
	}
	t.write(t.before)
	t.write(t.wrapStart)
	if c.kind == EndTag && c.flags&tokStray == 0 {
		t.writeEnd(c, t.endOf(c))
		t.write(t.after)
		t.endFrame(c)
		return
	}
	switch {
	case t.remove:
	case t.replaced:
		t.write(t.replace)
	case c.kind == StartTag && t.edited:
		t.buf = t.appendStart(t.buf[:0], c)
		t.write(t.buf)
	case c.kind == EndTag && t.renamed:
		t.writeRenamedEnd(c, t.name)
	default:
		t.write(c.Raw())
	}
	if t.dropAfter {
		// inside the emptied element
		t.write(t.after)
		t.after = t.after[:0]
	}
	if t.closeTag && !t.remove && !t.replaced {
		t.buf = append(t.buf[:0], "</"...)
		if t.renamed {
			t.buf = append(t.buf, t.name...)
		} else {
			t.buf = append(t.buf, c.Name()...)
		}
		t.buf = append(t.buf, '>')
		t.write(t.buf)
	}
	t.write(t.after)
	if t.dropAfter {
		t.drop = t.Depth()
	}
}

// writeEnd writes the end tag c, applying the actions of the element's frame
// f, if not nil.
func (t *Transformer) writeEnd(c *Token, f *frame) {
	var r *frameRec
	var act frameAct
	if f != nil {
		act = f.act
	}
	if act&actRec != 0 {
		r = &t.recs[f.aux]
	}
	switch {
	case t.remove || act&actUnwrap != 0:
	case t.replaced:
		t.write(t.replace)
	case c.Implied():
	case t.renamed:
		t.writeRenamedEnd(c, t.name)
	case r != nil && r.renamed:
		t.writeRenamedEnd(c, r.rename)
	default:
		t.write(c.Raw())
	}
	if r != nil {
		t.write(r.wrapEnd)
	}
}

func (t *Transformer) writeRenamedEnd(c *Token, name []byte) {
	raw := c.Raw()
	t.buf = append(append(append(t.buf[:0], "</"...), name...), raw[c.nspan.end:]...)
	t.write(t.buf)
}

// endFrame releases the actions of the element closed by the end tag c.
func (t *Transformer) endFrame(c *Token) {
	f := t.endOf(c)
	if f == nil {
		return
	}
	if f.act&actRec != 0 {
		r := &t.recs[f.aux]
		*r = frameRec{rename: r.rename[:0], wrapEnd: r.wrapEnd[:0]}
		t.free = append(t.free, f.aux)
	}
	f.act = 0
	if c.spos < 0 {
		t.uframes = t.uframes[:len(t.uframes)-1]
	}
}

// appendStart appends the edited start tag c to b, keeping the original
// bytes of unchanged attributes.
func (t *Transformer) appendStart(b []byte, c *Token) []byte {
	raw := c.Raw()
	b = append(b, '<')
	if t.renamed {
		b = append(b, t.name...)
	} else {
		b = append(b, c.Name()...)
	}
	prev := c.nspan.end
	unquoted := false    // the last attribute written has an unquoted value
	bare := false        // the value is also empty
	valueless := false   // the last attribute written has no value
	prevChanged := false // the previous attribute was removed or changed, or attributes were added before this one
	for i, s := range c.attrs {
		end := s.name.end
		if s.hasVal {
			end = s.val.end
			if s.quote != 0 {
				end++
			}
		}
		var op attrOp
		if i < len(t.attrOps) {
			op = t.attrOps[i]
		}
		changed := op.remove || op.set || op.rename != nil
		if len(t.newAttrs) != 0 {
			var added bool
			if b, added = t.appendNewAttrs(b, c, int32(i), unquoted, bare); added {
				unquoted, bare, valueless = false, false, false
				prevChanged = true
			}
		}
		if !op.remove {
			sep := raw[prev:s.name.start]
			if len(sep) == 0 && (changed || prevChanged) {
				// The attribute followed a quoted value without whitespace.
				// Keep it that way only if both are unchanged.
				sep = []byte{' '}
			}
			name := raw[s.name.start:s.name.end]
			if op.rename != nil {
				name = op.rename
			}
			if unquoted && len(sep) != 0 {
				b = endUnquoted(b, bare, sep[0])
			}
			if valueless && len(name) != 0 && name[0] == '=' {
				// A name starting with "=" can only follow a value. After
				// whitespace, it would become the value of the attribute
				// written before it, so give that one an empty value.
				b = append(b, `=""`...)
			}
			b = append(b, sep...)
			b = append(b, name...)
			if op.set {
				b = append(b, `="`...)
				b = appendAttrValue(b, op.value, c)
				b = append(b, '"')
				unquoted, bare, valueless = false, false, false
			} else {
				b = append(b, raw[s.name.end:end]...)
				unquoted = s.hasVal && s.quote == 0
				bare = unquoted && s.val.start == s.val.end
				valueless = !s.hasVal
			}
		}
		prev = end
		prevChanged = changed
	}
	if len(t.newAttrs) != 0 {
		var added bool
		if b, added = t.appendNewAttrs(b, c, int32(len(c.attrs)), unquoted, bare); added {
			unquoted, bare = false, false
		}
	}
	tail := raw[prev:]
	var suffix string
	switch t.selfClose {
	case 1:
		tail, suffix = bytes.TrimSuffix(tail, []byte(">")), "/>"
	case -1:
		// all of them, since HTML parsers read <x//> as self-closing
		tail, suffix = bytes.TrimRight(bytes.TrimSuffix(tail, []byte(">")), "/"), ">"
	}
	if unquoted {
		next := byte('>')
		if len(tail) != 0 {
			next = tail[0]
		} else if suffix != "" {
			next = suffix[0]
		}
		b = endUnquoted(b, bare, next)
	}
	b = append(b, tail...)
	b = append(b, suffix...)
	return b
}

// endUnquoted ends the unquoted value of the last attribute written before
// the byte next, which would otherwise continue it: an unquoted value ends at
// whitespace or ">", and an empty one only at ">".
func endUnquoted(b []byte, bare bool, next byte) []byte {
	switch {
	case next == '>':
	case bare:
		b = append(b, `""`...)
	case !ascii.IsSpace(next):
		b = append(b, ' ')
	}
	return b
}

// appendNewAttrs appends the attributes added before the original attribute
// at, reporting whether there were any. unquoted and bare describe the value
// of the last attribute written, as in appendStart.
func (t *Transformer) appendNewAttrs(b []byte, c *Token, at int32, unquoted, bare bool) ([]byte, bool) {
	added := false
	for _, a := range t.newAttrs {
		if a.at != at {
			continue
		}
		if unquoted && !added {
			b = endUnquoted(b, bare, ' ')
		}
		b = append(b, ' ')
		b = append(b, a.name...)
		b = append(b, `="`...)
		b = appendAttrValue(b, a.value, c)
		b = append(b, '"')
		added = true
	}
	return b, added
}
