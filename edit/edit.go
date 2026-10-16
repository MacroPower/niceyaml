package edit

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/bom"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

var (
	// ErrEngine indicates new text that the engine cannot parse, or that
	// does not decode to the values that the edits wrote. It shows a bug in
	// the engine, not in the document.
	ErrEngine = errors.New("the edited text does not read back as written")

	// The problem of a call of New with a nil Node, a Node below the root,
	// or a document that holds no value.
	errDocument = errors.New("New takes the root of a document that holds a value")
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
	// The problem of a call of FileText without a Doc, with Docs of more
	// than one Source, or with two Docs of one document.
	errFileText = errors.New("FileText takes Docs of one Source, each of a different document")
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
	// The name of the app in the message of a bug in the engine.
	app string
	// The paths of the values that no [Func] edits.
	protected []paths.Path
	// The unmarshalers that each decode applies ahead of those of the
	// Source of the document.
	unmarshalers []niceyaml.Unmarshaler
}

// WithApp is an [Option] that names the app in the message of a bug in
// the engine. The message asks the user to report the bug to the authors
// of the app. Without WithApp, it names "this program".
func WithApp(name string) Option {
	return func(c *config) {
		c.app = name
	}
}

// WithProtected is an [Option] that protects the value at each of ps, a
// `$` path of names and indexes, and every value inside it. A path of
// [Doc.Apply] selects no protected value. A [Func] cannot set, rename,
// move, or remove a protected value, or a value that holds one. Each
// WithProtected adds to the paths before it.
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
	// The first problem of the call that runs, which fails the Apply unless
	// the Func returns its own.
	callErr error
	// The document as the user wrote it.
	user *niceyaml.Node
	// The parse of the current text, and its document.
	src  *niceyaml.Source
	node *niceyaml.Node
	// The places that the calls of the Apply running edited, by the origin
	// of the first byte of each.
	editedPlaces map[int]editedPlace
	// What each mapping that merges or repeats a key read before the Apply
	// running, and the last edit of the Apply.
	merges   map[position.Position]*mergeView
	lastEdit *editRecord
	// The text of the lines of the user's document.
	orig string
	// The byte order mark the text starts with, which the parse leaves out.
	bom string
	// The current text, without the byte order mark.
	cur string
	// The label of the Apply running, or "" for one without a label.
	label string
	cfg   config
	notes []note
	// The line starts of orig and of cur.
	origLines []int
	curLines  []int
	// The nodes of the Apply running, whose paths each edit keeps up to
	// date.
	nodes    []*Node
	removals []Removal
	// The values that the calls of the Apply running wrote, which the check
	// after the calls compares with what the document reads.
	written []writtenValue
	// The place in orig that each byte of cur came from, or for a byte an
	// edit wrote, -1 minus the index of its note.
	origin []int
	// The line of the user's text where orig starts.
	base int
	// The bytes [start, end) of the text of the user's Source that orig
	// holds, with its byte order mark.
	start, end int
	// An edit changed the text.
	edited bool
	// An Apply runs.
	applying bool
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
	edited   bool
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
		edited:   d.edited,
	}
}

// restore puts back the text of d that s saved.
func (d *Doc) restore(s state) {
	d.src, d.node, d.cur, d.origin, d.curLines, d.edited = s.src, s.node, s.cur, s.origin, s.curLines, s.edited
	d.notes = d.notes[:s.notes]
	d.removals = d.removals[:s.removals]
}

// Removal is one value of the user's text that an edit removed.
type Removal struct {
	// Reason is the reason that [WithReason] gave, or "".
	Reason string
	// Label is the label that [WithLabel] gave the Apply that removed the
	// value, or "".
	Label string
	// Key is the key of the entry, for a removal of an entry.
	Key string
	// Path is the path of the value as the user's text spells it, which
	// ends in `~` for an entry.
	Path paths.Path
	// Range is the range of the key or the item in the user's text.
	Range position.Range
	// Item is set for the removal of a sequence item.
	Item bool
}

// writtenValue is a value a call wrote, which the check after the calls
// compares with what the text reads at path.
type writtenValue struct {
	value any
	path  paths.Path
}

// New creates a new [Doc] that holds the text of doc, the root of a
// document of a [niceyaml.Source] that read text, such as one that
// [niceyaml.Source.Documents] returns. It returns the error of a document
// that does not parse. It also returns an error for a nil doc, a Node
// below the root, and a document that holds no value. A path of
// [WithProtected] that is not a `$` path of names and indexes fails New
// too.
func New(doc *niceyaml.Node, opts ...Option) (*Doc, error) {
	switch {
	case doc == nil:
		return nil, fmt.Errorf("%w, not nil", errDocument)
	case doc != doc.Document():
		return nil, fmt.Errorf("%w, not the node at %s", errDocument, doc.Path())
	}

	err := doc.Err()
	if err != nil {
		return nil, err //nolint:wrapcheck // The error of the document binds in it.
	}

	if doc.IsEmpty() {
		return nil, fmt.Errorf("%w, not an empty document", errDocument)
	}

	var cfg config

	for _, opt := range opts {
		opt(&cfg)
	}

	for _, p := range cfg.protected {
		if !p.IsAbsolute() || !plain(p) {
			return nil, fmt.Errorf("%w, not %s", errProtectedPath, p)
		}
	}

	src := doc.Source()
	text := src.Text()
	span := doc.Span()

	start, ok := src.Offset(position.New(span.Start, 0))
	if !ok {
		start = 0
	}

	end := len(text)
	if span.End < src.Lines().Len() {
		if off, ok := src.Offset(position.New(span.End, 0)); ok {
			end = off
		}
	}

	orig := text[start:end]

	d := &Doc{user: doc, base: span.Start, cfg: cfg, start: start, end: end}

	// The parse counts positions without the marks that the tokenizer
	// drops, such as one after the "---" of a later document. The text
	// leaves them out too. [Doc.Text] keeps the mark that opens the text
	// and drops any other, which the parse reads as nothing.
	orig, marks := bom.Drop(orig)
	if len(marks) > 0 && marks[0] == 0 {
		d.bom = bom.Mark
	}

	d.orig, d.cur = orig, orig
	d.origLines = lineStarts(orig)

	d.origin = make([]int, len(orig))
	for i := range d.origin {
		d.origin[i] = i
	}

	err = d.parse()
	if err != nil {
		return nil, err
	}

	return d, nil
}

// Apply runs fn once on each place that path selects, in document order,
// and then checks the result. The path starts at `$` and holds no `~`. A
// path that selects nothing calls fn zero times. Places that an alias or
// a `<<` merge key shares take one call. To create a value, apply at a
// place that exists, such as `$`, and call [Node.Set] on the node that
// [Node.At] returns.
//
// Each call sees the edits of the calls before it. A place that an earlier
// call took away, by a removal of the place or a replacement of a value
// above it, takes no call. A method of [Node] that cannot make its edit
// fails the Apply, even when fn goes on and returns nil. The calls of one
// Apply edit a shared value once. A second edit of a value fails when one
// of the two calls reached it through an alias or a `<<` merge key.
//
// After the last call, Apply checks the text. Each mapping that merges or
// repeats a key must read the same entry for each key as it did before.
// An edit that a merge key undoes thus fails. The text must also decode,
// and each value that [Node.Set] wrote must read back as written. A
// difference shows a bug in the engine, and its error matches
// [ErrEngine].
//
// Apply returns the first problem, bound in the user's text, and stops
// there. A failed Apply leaves the Doc as it found it, so the Doc drops
// every edit of the calls before the problem. Apply returns an error, and
// calls fn zero times, for a path that does not start at `$` or that
// holds a `~`. It does the same for a call from a Func of the same Doc,
// and that call fails the outer Apply too.
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
	d.begin(label)

	nodes, err := d.matches(path)
	if err != nil {
		return err
	}

	for _, n := range nodes {
		if n.gone {
			continue
		}

		err := d.endCall(n, fn(ctx, n))
		if err != nil {
			return err
		}
	}

	return d.check(ctx)
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

// endApply ends the Apply running. Its nodes stop taking edits.
func (d *Doc) endApply() {
	for _, n := range d.nodes {
		n.ended = true
	}

	d.nodes = nil
	d.callErr = nil
	d.applying = false
}

// Text returns the current text of the document, with the byte order mark
// it started with.
func (d *Doc) Text() string {
	return d.bom + d.cur
}

// FileText returns the text of the [niceyaml.Source] of docs, with the
// [Doc.Text] of each Doc in place of the text of its document. The other
// documents of the Source keep their text. A program that edits several
// documents of one file passes the Doc of each. FileText returns an error
// for no Doc, for Docs of more than one Source, and for two Docs of one
// document.
func FileText(docs ...*Doc) (string, error) {
	if len(docs) == 0 {
		return "", errFileText
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
		if d.user.Source() != src || i > 0 && d.start < last {
			return "", errFileText
		}

		b.WriteString(text[last:d.start])
		b.WriteString(d.Text())

		last = d.end
	}

	b.WriteString(text[last:])

	return b.String(), nil
}

// Node returns the current text of the document as a document, whose
// errors bind in the user's text, under the paths that the user's text
// spells.
func (d *Doc) Node() *niceyaml.Node {
	return d.node
}

// Edited reports whether an edit changed the text.
func (d *Doc) Edited() bool {
	return d.edited
}

// Removals returns the values of the user's text that the edits removed,
// in the order of the calls. It leaves out a value that an earlier edit
// created.
func (d *Doc) Removals() []Removal {
	return slices.Clone(d.removals)
}
