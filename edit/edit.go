package edit

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/aliasing"
	"go.jacobcolvin.com/niceyaml/internal/bom"
	"go.jacobcolvin.com/niceyaml/internal/hook"
	"go.jacobcolvin.com/niceyaml/internal/mapkey"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

var (
	// ErrEngine indicates an edit that the engine cannot write so that the
	// text reads as the calls ask. It marks a bug in the engine, and it
	// also marks go-yaml behavior that the engine cannot write around.
	// Go-yaml's parser can refuse or misread the edited text, with or
	// without its comments, or panic on it. Go-yaml's decoder can refuse
	// it. Go-yaml's encoder can panic on the value of a Set, or write text
	// that go-yaml's decoder reads as another value. It can also write a
	// character that YAML does not allow in a text, or both "\n" and
	// "\r\n" line breaks for YAML of one kind. [New] fails with
	// [ErrMisread], which wraps ErrEngine, for some documents that go-yaml's
	// decoder reads otherwise than YAML does. The Supported YAML section of
	// the package documentation lists them. The message names
	// go-yaml in each of those cases. ErrEngine also indicates new text that
	// differs from the model of the calls of an
	// Apply, as the Transactions section of the package documentation
	// describes. It also indicates a call that the model cannot take. The
	// error binds where the user's text holds the value that differs, or at
	// the place of the call, as the Errors section of the package
	// documentation describes. An error that matches ErrEngine never
	// matches [ErrUnsupported].
	ErrEngine = errors.New("the engine cannot write this edit")

	// ErrMisread indicates a document of valid YAML that go-yaml's decoder
	// reads otherwise than YAML does, in one of the ways that the Supported
	// YAML section of the package documentation lists. Only [New] fails with
	// an error that matches ErrMisread. The error names go-yaml and what it
	// reads, and its detail says what the user can write so that go-yaml
	// reads the text as YAML does. ErrMisread wraps [ErrEngine], so such an
	// error matches both. A text that go-yaml's parser refuses fails New with
	// the error of the parse, as the Source gives it, which matches neither.
	//
	// The text is valid YAML, so [go.jacobcolvin.com/niceyaml.IsInvalid]
	// reports false for the error, as it does for a bug in the engine. The
	// user can still fix the text, so a server tests for ErrMisread before
	// it asks IsInvalid:
	//
	//	switch {
	//	case err == nil:
	//	case errors.Is(err, edit.ErrMisread):
	//		status = 422 // the user rewrites the text as the detail says
	//	case !niceyaml.IsInvalid(err):
	//		status = 500 // a bug in the engine, among other faults
	//	default:
	//		status = 422
	//	}
	//
	// A call that writes such a text fails with an error that matches
	// ErrEngine and not ErrMisread, because the engine wrote that text. So
	// does go-yaml's encoder, and go-yaml's parser or decoder when it refuses
	// the text that the engine wrote.
	ErrMisread = fmt.Errorf("go-yaml misreads the document: %w", ErrEngine)

	// The problem of a call of New with a nil Node, a Node below the root,
	// or a document that does not hold a value.
	errDocument = errors.New("New takes the root of a document that holds a value")
	// The problem of a document that holds a byte order mark inside it.
	errMarkInside = errors.New("a byte order mark")
	// The problem of a call of New with a protected path that is not a `$`
	// path of names and indexes.
	errProtectedPath = errors.New("WithProtected takes a $ path of names and indexes")
	// The problem of an Apply that a Func of its Doc calls.
	errNested = errors.New("Apply runs inside another Apply of the Doc")
	// The problem of an edit or a decode of a node after the Apply that gave
	// it returned.
	errEnded = errors.New("the Apply that gave the node has returned")
	// The problem of an Apply on a path that does not start at `$`, or that
	// holds a `~`.
	errPath = errors.New("Apply takes a $ path without ~")
	// The problems of a call of FileText without a Doc, with Docs of more
	// than one Source, and with two Docs of one document.
	errFileTextEmpty   = errors.New("FileText takes at least one Doc")
	errFileTextSources = errors.New("FileText takes Docs of one Source")
	errFileTextTwice   = errors.New("FileText takes each document once")
	// The problem of a parse of the text that finds no document in it.
	errNoDocument = errors.New("the text holds no document")
)

// Func edits a document through n, a node on one place that the path of
// [Doc.Apply] selects. An error fails the Apply, and an error without a
// location binds at the value of n.
type Func func(ctx context.Context, n *Node) error

// Option configures a [Doc].
//
// Options:
//   - [WithApp]
//   - [WithProtected]
//   - [WithUnmarshalers]
type Option func(*config)

// config holds the settings that each [Option] gives a [Doc].
type config struct {
	// The name of the app in the messages of the engine.
	app string
	// The paths of the values that [WithProtected] protects.
	protected []paths.Path
	// The unmarshalers that each decode applies ahead of those of the
	// Source of the document.
	unmarshalers []niceyaml.Unmarshaler
}

// WithApp is an [Option] that names the app in the messages of the
// engine. A refusal of YAML that the engine does not edit names it, as in
// "cafe does not edit a document that defines the anchor &x twice". So
// does the message of a bug in the engine, which asks the user to report
// the bug to the authors of the app. Without WithApp, a message names
// "this program".
func WithApp(name string) Option {
	return func(c *config) {
		c.app = name
	}
}

// WithProtected is an [Option] that protects the value at each of ps, a
// `$` path of names and indexes, and every value inside it. Each Apply
// protects the node of the text that each path reaches when it starts,
// through an alias or a `<<` merge key too, whichever path names the node.
// A path of [Doc.Apply] never selects a protected value or a value inside
// one. A [Func] cannot set, rename, move, or remove a protected value or a
// value inside one, and it cannot set, move, or remove a value that holds
// one. A path that holds no value when the Apply starts stays protected as
// the Apply found it, so a call that creates a value there fails. A path
// that a reference document decides protects the alias or the mapping
// whose lookup reads that document, as a value that holds the protected
// value. Each WithProtected adds to the paths before it. The Protected
// Values section of the package documentation gives the rules.
func WithProtected(ps ...paths.Path) Option {
	return func(c *config) {
		c.protected = append(c.protected, ps...)
	}
}

// WithUnmarshalers is an [Option] that gives each decode of the document
// the unmarshalers us, ahead of those of its [niceyaml.Source], as though
// [niceyaml.WithUnmarshalers] gave them first. [Node.Decode] applies
// them, and so does each decode of the Node that [Doc.Node] returns. Each
// WithUnmarshalers adds to the ones before it.
func WithUnmarshalers(us ...niceyaml.Unmarshaler) Option {
	return func(c *config) {
		c.unmarshalers = append(c.unmarshalers, us...)
	}
}

// ApplyOption configures one call of [Doc.Apply].
//
// Options:
//   - [WithLabel]
type ApplyOption func(*applyConfig)

// applyConfig holds the settings that each [ApplyOption] gives an Apply.
type applyConfig struct {
	label string
}

// WithLabel is an [ApplyOption] that names the edits of the Apply in its
// messages, as "v2" does in `v2 renames "cost" to "price", which already
// exists`. Without it, a message names "the edit". Each [Removal] of the
// Apply holds the label too.
func WithLabel(label string) ApplyOption {
	return func(c *applyConfig) {
		c.label = label
	}
}

// Doc is the text of one YAML document as the edits leave it. A Doc is not
// safe for concurrent use.
//
// Create instances with [New].
type Doc struct {
	// The first problem of the call that runs, which fails the Apply. The
	// error that the Func returns comes first, unless the problem matches
	// [ErrEngine].
	callErr error
	// The problem of the first call of the Apply running after which the
	// text differed from the model of the calls, which each later call of
	// the Apply returns, or nil.
	stuck error
	// The document as the user wrote it.
	user *niceyaml.Node
	// The parse of the current text, and its document.
	src  *niceyaml.Source
	node *niceyaml.Node
	// The index of the current parse, which [Doc.parseIndex] makes again
	// after each parse, and one other index that it keeps. The end of an
	// Apply keeps only the reader of the keys of the current parse. The
	// readers of the parses of an Apply share the decodes of the keys
	// without an alias in plain.
	index, other *parseIndex
	plain        *mapkey.Plain
	// What go-yaml's decoder reads from each scalar key that the
	// canonical-key test read, by its token, as [Doc.readKey] reads it.
	keyReads map[keyRead]keyReading
	// The last edit of the Apply running.
	lastEdit *editRecord
	// The string of each block scalar of the user's document, by the offset
	// in orig of its indicator, which [Doc.readUserScalars] reads.
	scalars map[int]string
	// The text of the lines of the user's document.
	orig string
	// The offset in orig of each byte that a byte order mark stood in front
	// of in the user's text. The tokenizer drops such a mark, so orig does
	// not hold it. [New] refuses such a mark in the first document of a file
	// and inside a document, and [Doc.Text] writes each other mark again.
	marks []int
	// The line break that a line an edit adds ends with when the current
	// text does not have one: the first line break of the user's document,
	// or else of its file, or else "\n".
	brk string
	// The current text.
	cur string
	// The text of the "---" that opens the next document, or "" when no such
	// header follows. Each parse reads the current text with it, as the
	// parser reads the document in its file. The comment lines before the
	// header belong to the text of the document, as they do at the end of a
	// file.
	tail string
	// The label of the Apply running, or "" for one without a label.
	label string
	// What the call running does, as a message names it, such as
	// "renames $.a to "b"", or "" when no call runs.
	callNamed string
	// What the call running does, as the detail that names it names it,
	// such as "reads $.a" for a read, or "" when no call runs. The calls of
	// [Node] and the write of a type field run, as [Doc.callDetails] names
	// them.
	running string
	cfg     config
	notes   []note
	// The line starts of orig and of cur.
	origLines []int
	curLines  []int
	// The offsets of the runes of the lines of cur that [Doc.offset] read.
	runes runeOffsets
	// The nodes of the Apply running, whose paths each edit keeps up to
	// date, and the guards of the paths of [WithProtected], as
	// [Doc.guardValues] gives them.
	nodes  []*Node
	guards []*guard
	// Each node that the edit of the call running changes, as the call found
	// it, which a call that fails restores.
	found    map[*Node]Node
	removals []Removal
	// The model of the calls of the Apply running, or nil before its first
	// call.
	model *model
	// The zones of the Apply running, in the order of their sites, as
	// [Doc.beginZones] finds them, and the key of each, as [Doc.heldZone]
	// reads it.
	zones    []*zone
	zoneKeys map[zoneKey]bool
	// The storage of the groups of keys that [Doc.constructZones] reads.
	groups *keyGroups
	// The reach of the Set running, which [Doc.renderPayload] checks, or
	// nil.
	pending *reachCall
	// The capture of the site of an error that the edit package binds again,
	// as [Doc.sitePlace] fills it, or nil.
	capture *capture
	// The merges of the text that the call running found, which
	// [Doc.mergeKept] checks after the call.
	mergesAt mergeState
	// The place in orig that each byte of cur came from, or for a byte an
	// edit wrote, -1 minus the index of its note.
	origin []int
	// The first offset in origin of each origin that [Doc.partSpan] looked
	// up, as [Doc.firstOffset] reads it.
	firsts originFirsts
	// The text that the call running found, which holds the place of the
	// call, while calling reports that a call runs.
	callStart state
	// The site where the value that the Set running replaces binds, while
	// replacing reports that the Set replaces a value, as [Doc.startCall]
	// finds it.
	replaced site
	// The line of the user's text where orig starts.
	base int
	// Whether a block sequence that an edit creates indents its "-", as
	// [Doc.sequenceIndent] tells: 1 when it does, -1 when it does not, or 0
	// before sequenceIndent reads it.
	seqIndent int
	// The bytes [start, end) of the text of the user's Source that orig
	// holds, with its byte order mark.
	start, end int
	// Whether an Apply is running, and whether a call of an edit runs.
	applying, calling bool
	// Whether the Set running replaces a value, as replaced holds its site.
	replacing bool
	// Whether the check after the last call runs, whose problems name no
	// call, as [Doc.callDetails] tells.
	checking bool
	// Whether the error that binds in the current text is a problem that the
	// edit package found, as [Doc.bindOwn] binds it, and not an error that a
	// caller binds by a path.
	own bool
}

// state is the text of a [Doc] as an Apply found it, which a failed Apply
// restores.
type state struct {
	src      *niceyaml.Source
	node     *niceyaml.Node
	cur      string
	origin   []int
	curLines []int
	notes    int
	removals int
}

// save returns the text of d as it stands.
func (d *Doc) save() state {
	return state{
		src:      d.src,
		node:     d.node,
		cur:      d.cur,
		origin:   d.origin,
		curLines: d.curLines,
		notes:    len(d.notes),
		removals: len(d.removals),
	}
}

// restore replaces the text of d with the text that s saved, and drops
// the notes and the removals after s.
func (d *Doc) restore(s state) {
	d.show(s)

	d.notes = d.notes[:s.notes]
	d.removals = d.removals[:s.removals]
}

// show replaces the text of d with the text that s saved, and keeps the
// notes and the removals. The notes of that text stay valid, because the
// edits only add notes.
func (d *Doc) show(s state) {
	d.src, d.node, d.cur, d.origin, d.curLines = s.src, s.node, s.cur, s.origin, s.curLines
}

// keepNode keeps n as the call running found it, unless the call kept it
// already, so a call that fails restores it.
func (d *Doc) keepNode(n *Node) {
	if !d.calling {
		return
	}

	if d.found == nil {
		d.found = make(map[*Node]Node)
	}

	if _, ok := d.found[n]; !ok {
		d.found[n] = *n
	}
}

// undoCall leaves the Doc as the call running found it, after the call
// failed. The text, the removals, and the nodes that the call changed go
// back to what the call found. The notes of the call stay, because each is
// the origin of text that the call wrote, and nothing refers to them once
// that text goes. When the model took the change of the call, the model
// starts again from the text, as at the first call of an Apply. That text
// holds the values of the model before the call. The calls after it thus
// read the text, the nodes, and the model as they stood before the call.
func (d *Doc) undoCall(changed bool) {
	d.show(d.callStart)

	d.removals = d.removals[:d.callStart.removals]

	for n, was := range d.found {
		*n = was
	}

	d.found = nil

	if changed {
		d.model = nil
	}
}

// Removal is one value of the user's text that an edit removed. A rename
// or a move can take the value to another place first, and a Set can
// replace it. Edits inside the value can take some of its text or move
// other text into it. Key, Path, Range, and Item still describe the value
// as the user's text holds it.
type Removal struct {
	// Reason is the reason that [WithReason] gave, or "".
	Reason string
	// Label is the label that [WithLabel] gave the Apply that removed the
	// value, or "".
	Label string
	// Key is the key that the user wrote for the value, or "" when the
	// user's text holds the value as an item.
	Key string
	// Path is the path of the value as the user's text spells it. It ends
	// in `~` when the user wrote the value under a key.
	Path paths.Path
	// Range is the range in the user's text of the key that the user wrote
	// for the value. For an item of a block sequence, it runs from the "-"
	// through the value. For an item of a flow sequence, it is the range of
	// the value.
	Range position.Range
	// Item is true when the user's text holds the value as an item of a
	// sequence.
	Item bool
}

// New creates a new [Doc] that holds the text of doc, the root of a
// document of a [niceyaml.Source] that read text, such as one that
// [niceyaml.Source.Documents] returns. It returns the error of a document
// that does not parse, or of a reference document of
// [niceyaml.WithReferences] that does not parse, because a decode of the
// document then fails. It also returns an error for a nil doc, a Node
// below the root, and a document that does not hold a value, such as an
// empty text. Go-yaml's lexer reads a tab after a tag as a part of the
// tag. Go-yaml's parser reads no value from a tag at the end of the text,
// as in "!t" or "!t\t" without a line break. So New refuses that document
// too. The parser reads an empty value under a tag that a line break or a
// space follows. So New takes "!t\n" with that value as its root.
// [niceyaml.IsInvalid] reports the error of a document that does not hold
// a value, but not the error of a nil doc or a Node below the root. A path
// of [WithProtected] that is not a `$` path of names and indexes fails New
// too. New reads the document as go-yaml reads it. It refuses some
// documents that go-yaml's decoder reads otherwise than YAML does, with an
// error that matches [ErrMisread]. The Supported YAML section of the
// package documentation lists them. It then refuses a document that holds
// YAML outside the subset that the engine edits, as that section lists it,
// with an error that matches [ErrUnsupported].
func New(doc *niceyaml.Node, opts ...Option) (*Doc, error) {
	switch {
	case doc == nil:
		return nil, fmt.Errorf("%w, not nil", errDocument)
	case doc != doc.Document():
		return nil, fmt.Errorf("%w, not the node at %s", errDocument, doc.Path())
	}

	var cfg config

	for _, opt := range opts {
		opt(&cfg)
	}

	err := doc.Err()
	if err != nil {
		return nil, err //nolint:wrapcheck // The error of the document binds in it.
	}

	// Every decode of the document fails on a reference document that does
	// not parse, so New fails with the error of that parse.
	for _, ref := range hook.ReferenceDocuments(doc) {
		if r, ok := ref.(*niceyaml.Node); ok && r.Err() != nil {
			return nil, r.Err() //nolint:wrapcheck // The error of the document binds in it.
		}
	}

	for _, p := range cfg.protected {
		if !p.IsAbsolute() || !plain(p) {
			return nil, fmt.Errorf("%w, not %s", errProtectedPath, p)
		}
	}

	d, err := readDoc(doc, cfg)
	if err != nil {
		return nil, err
	}

	// The text of the user is at fault for a document without a value, as
	// for an empty upload, unlike a program that passes a nil doc.
	if doc.IsEmpty() {
		//nolint:wrapcheck // The binding is the problem of the document.
		return nil, doc.Bind(niceyaml.Invalid(fmt.Errorf("%w, not an empty document", errDocument)))
	}

	for _, refuse := range []func() error{d.markRefusal, d.misreadRefusal, d.newRefusal} {
		err = refuse()
		if err != nil {
			return nil, err
		}
	}

	d.readUserScalars()

	return d, nil
}

// readDoc returns the [Doc] of doc, the root of a document that parses, as
// [New] reads it with cfg before it checks the text.
func readDoc(doc *niceyaml.Node, cfg config) (*Doc, error) {
	src := doc.Source()
	text := src.Text()
	span := doc.Span()

	// The comment lines before the "---" that opens the document belong to
	// the document before it, as its text holds them, so the document
	// starts at its header.
	if i := doc.DocumentIndex(); i > 0 {
		before := src.AllDocuments()[i-1].Span()
		if line, ok := headerAfter(src, before.End); ok && line > span.Start {
			span.Start = line
		}
	}

	// A byte order mark that the tokenizer drops lies before column 0 of
	// its line, so the document starts past the mark of its first line, and
	// that mark stays out of its text.
	start, ok := src.Offset(position.New(span.Start, 0))
	if !ok {
		start = 0
	}

	end := len(text)
	if span.End < src.Lines().Len() {
		if off, ok := src.Offset(position.New(span.End, 0)); ok {
			end = lineStart(text, off)
		}
	}

	d := &Doc{user: doc, base: span.Start, cfg: cfg, start: start}

	// The comment lines between the document and a "---" after it belong to
	// the document, as the comment lines at the end of a file do.
	if line, ok := headerAfter(src, span.End); ok {
		if off, ok := src.Offset(position.New(line, 0)); ok && lineStart(text, off) >= end {
			d.tail = text[off:headerEnd(src, line)]
			end = lineStart(text, off)
		}
	}

	d.end = end
	orig := text[start:end]

	// The parse counts positions without the marks that the tokenizer
	// drops, such as one after the "---" of a later document, so the text
	// leaves them out too.
	orig, d.marks = bom.Drop(orig)

	d.orig, d.cur = orig, orig
	d.origLines = lineStarts(orig)
	d.brk = cmp.Or(firstBreak(orig), firstBreak(text), "\n")

	d.origin = make([]int, len(orig))
	for i := range d.origin {
		d.origin[i] = i
	}

	err := d.parse()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrEngine, err)
	}

	return d, nil
}

// lineStart returns off, the offset in text of column 0 of a line as
// [niceyaml.Source.Offset] gives it, or the offset of the byte order mark
// before it. The tokenizer drops such a mark, which lies before column 0 of
// its line.
func lineStart(text string, off int) int {
	if strings.HasSuffix(text[:off], bom.Mark) {
		return off - len(bom.Mark)
	}

	return off
}

// headerAfter returns the line of the "---" that opens the next document
// after line, the line where a document of src ends, past the comments
// between them. It reports false when another token, or no token, follows
// those comments. The parser reads a document before such a header with the
// header after it, which gives an anchor without a value at the end of the
// document a null.
func headerAfter(src *niceyaml.Source, line int) (int, bool) {
	lines := src.Lines()

	// The lines from line on hold the tokens after the document in the
	// order of the text, and a token that starts before line is part of
	// the document.
	for l := line; l < lines.Len(); l++ {
		for _, tk := range lines.Line(l).SourceTokens() {
			switch {
			case tk.Position.Line <= line, tk.Type == token.CommentType:
			case tk.Type == token.DocumentHeaderType:
				return tk.Position.Line - 1, true
			default:
				return 0, false
			}
		}
	}

	return 0, false
}

// headerEnd returns the offset in the text of src past the "---" that
// starts the next document on line, as [headerAfter] gives it.
func headerEnd(src *niceyaml.Source, line int) int {
	text := src.Text()

	off, ok := src.Offset(position.New(line, 0))
	if !ok {
		return len(text)
	}

	at := strings.Index(text[off:], "---")
	if at < 0 {
		return len(text)
	}

	return off + at + len("---")
}

// Apply runs fn once on each place that path selects, in document order,
// and then checks the result. The path starts at `$` and does not hold a
// `~`. When path does not select any place, Apply calls fn zero times.
// Each entry and each item of the text is a place of its own, even when
// its value is an alias, so each takes a call. A path that reaches one
// entry or one item through an alias or a `<<` merge key reaches the same
// text, so all such paths take one call. To create a value, apply at a
// place that exists, such as `$`, and call [Node.Set] on the node that
// [Node.At] returns.
//
// A Source that [niceyaml.WithAllowDuplicateKeys] gives can hold two
// entries of one key in a mapping. A path of the key selects the last of
// them, as a decode reads it. A selector that selects more than one value,
// such as `.*`, `..*`, or `..name`, selects each entry of the key that it
// selects, so each takes a call. The selectors after it read below each
// such entry. The node of an entry that a later entry hides has the path
// of its key too, and an `@` path of [Node.At] reads below that entry. So
// the node reads the values of its own entry. A call that edits either
// entry fails with [ErrUnsupported], as [ConstructRepeatedKey] names it.
//
// Apply calls fn only on places that hold a value. A recursive selector
// thus selects nothing below a tag that converts a collection, such as
// !!str, because the decode reads that collection as a scalar.
//
// Each call sees the edits of the calls before it. A read after the write of
// an earlier call thus reads the written value, through an alias too. Apply
// skips each place that an earlier call removed, either directly or by
// replacing a value above it. When a method
// of [Node] cannot make its edit, the Apply fails, even when fn continues
// and returns nil. The calls act in order, so a later call can edit a value
// that an earlier call edited, through any path.
//
// After each call, the text must hold the values of a model of the calls,
// and each alias must read the value that the model holds at its place.
// Otherwise the call fails with [ErrEngine] at the first difference, and
// each later call of the Apply returns that error. A call also fails with
// ErrEngine when it leaves a text that [New] refuses with [ErrMisread] for
// its `<<` merge keys or for a byte order mark, as the package
// documentation describes.
//
// A call fails with [ErrUnsupported] when it reaches YAML that the engine
// does not edit, as the Supported YAML section of the package
// documentation lists it. A rename that makes a mapping hide an entry that
// its `<<` merge key brings in is one such call. A read never fails so.
//
// After the last call, Apply checks the text. The text must decode, and it
// must hold the values of the model, as the package documentation
// describes. A difference from the model shows a bug in the engine or a
// misread of go-yaml. Its error matches [ErrEngine] and binds where the
// user's text holds the value that differs.
//
// Each call parses and checks the whole text that it leaves. So the cost
// of a path that selects several places grows with the number of places
// times the size of the text. A rename of each key of a text of 400 keys,
// 3.7 KiB, took 0.5 s and allocated over 900 MiB. A program that edits
// untrusted input should keep the byte limit and the alias limit of its
// Source on, which [niceyaml.WithByteLimit] and [niceyaml.WithAliasLimit]
// set.
//
// Apply returns the first problem, bound in the user's text, and stops
// there. A failed Apply leaves the Doc as it found it, so the Doc drops
// every edit of the calls before the problem. Apply returns an error, and
// calls fn zero times, for a path that does not start at `$` or that holds
// a `~`. It does the same for a call from a Func of the same Doc, and that
// call fails the outer Apply too. A text whose aliases expand past the
// alias limit also fails before fn runs, with the error that a decode of
// the text gives. A path whose matches read through an alias that the text
// does not resolve, such as an alias of a reference document at a `<<`
// merge key, fails the Apply too. That problem reads as the problem of such
// a path of [Node.At] does, and it binds at the alias.
func (d *Doc) Apply(ctx context.Context, path paths.Path, fn Func, opts ...ApplyOption) error {
	if d.applying {
		d.record(errNested)

		return errNested
	}

	if !path.IsAbsolute() || holdsKey(path) {
		return fmt.Errorf("%w, not %s", errPath, path)
	}

	var cfg applyConfig

	for _, opt := range opts {
		opt(&cfg)
	}

	d.applying = true
	saved := d.save()
	passed := false

	// A panic in fn leaves the Doc as it found it too.
	defer func() {
		if !passed {
			d.restore(saved)
		}

		d.endApply()
	}()

	err := d.run(ctx, path, fn, cfg.label)
	passed = err == nil

	return err
}

// run runs fn on each place that path selects, as the Apply that messages
// name label, and checks the result.
func (d *Doc) run(ctx context.Context, path paths.Path, fn Func, label string) error {
	start := d.save()

	// A text whose aliases take a decode past the alias limit fails with the
	// error of that decode, before the calls read its aliases.
	if aliasing.PastLimit(d.node) {
		err := d.userDecode(ctx)
		if err != nil {
			return err
		}
	}

	d.begin(label)
	d.guardValues()

	nodes, err := d.matches(path)
	if err != nil {
		return err
	}

	for _, n := range nodes {
		if n.gone || d.displaced(n) {
			continue
		}

		err := d.endCall(n, fn(ctx, n))
		if err != nil {
			return err
		}
	}

	return d.check(ctx, start)
}

// holdsKey reports whether path holds a `~` selector.
func holdsKey(path paths.Path) bool {
	for sel := range path.Selectors() {
		if sel.Kind == paths.SelectorKey {
			return true
		}
	}

	return false
}

// endApply ends the Apply running. Its nodes stop taking edits, and the
// Doc drops what its calls kept, as [Doc.endCalls] drops it.
func (d *Doc) endApply() {
	for _, n := range d.nodes {
		n.ended = true
	}

	d.nodes, d.guards = nil, nil
	d.applying = false
	d.endCalls()
}

// Text returns the current text of the document. During an Apply, Text
// returns the text after the last call that passed. A byte order mark that
// the tokenizer drops after the first line of the document, as after a
// comment line in the prefix of a later document, stays in front of the
// line where the user wrote it. When an edit took that line away, the mark
// stands in front of the next line of the user's text that the edits kept,
// or goes with the text after it. A mark at the start of the first line of
// the document stands before the text of the document, so Text leaves it
// out, and [FileText] keeps it.
func (d *Doc) Text() string {
	if len(d.marks) == 0 {
		return d.cur
	}

	var (
		b    strings.Builder
		next = 0
		last = 0
	)

	for i := range len(d.cur) {
		if next == len(d.marks) {
			break
		}

		o := d.origin[i]
		if o < d.marks[next] {
			continue
		}

		// The mark goes at the start of the line of the first byte that the
		// edits kept from its place on, before the bytes that an edit wrote
		// there.
		at := strings.LastIndexByte(d.cur[:i], '\n') + 1
		at = max(at, last)

		b.WriteString(d.cur[last:at])
		b.WriteString(bom.Mark)

		last = at

		for next < len(d.marks) && d.marks[next] <= o {
			next++
		}
	}

	b.WriteString(d.cur[last:])

	// A mark at the end of the text stands after the last byte of the user.
	if next < len(d.marks) && d.marks[next] == len(d.orig) && d.cur != "" &&
		d.origin[len(d.cur)-1] == len(d.orig)-1 {
		b.WriteString(bom.Mark)
	}

	return b.String()
}

// FileText returns the text of the [niceyaml.Source] of docs, with the
// [Doc.Text] of each Doc in place of the text of its document. The other
// documents of the Source keep their text, and so does a byte order mark
// before a document. A program that edits several documents of one
// file passes the Doc of each. FileText returns an error when docs is
// empty, when the Docs come from more than one Source, and when two Docs
// come from one document. During an Apply, FileText reads the text of
// each Doc as Doc.Text does.
func FileText(docs ...*Doc) (string, error) {
	if len(docs) == 0 {
		return "", errFileTextEmpty
	}

	src := docs[0].user.Source()

	sorted := slices.Clone(docs)
	slices.SortStableFunc(sorted, func(a, b *Doc) int { return a.start - b.start })

	var (
		b    strings.Builder
		text = src.Text()
		last = 0
	)

	for i, d := range sorted {
		switch {
		case d.user.Source() != src:
			return "", fmt.Errorf("%w, not Docs of two Sources", errFileTextSources)
		case i > 0 && d.start < last:
			return "", fmt.Errorf("%w, not the document at line %d twice", errFileTextTwice, d.base+1)
		}

		b.WriteString(text[last:d.start])
		b.WriteString(d.Text())

		last = d.end
	}

	b.WriteString(text[last:])

	return b.String(), nil
}

// Node returns the current text of the document as a document, whose
// errors bind in the user's text. An error keeps the path that it names,
// as the Errors section of the package documentation describes. During an
// Apply, Node reads the document as [Doc.Text] does.
func (d *Doc) Node() *niceyaml.Node {
	return d.node
}

// Edited reports whether the text of the document differs from the text
// that [New] took.
func (d *Doc) Edited() bool {
	return d.cur != d.orig
}

// Removals returns the values of the user's text that the edits removed,
// in the order of the calls. It omits each value that an earlier edit
// created.
func (d *Doc) Removals() []Removal {
	return slices.Clone(d.removals)
}
