// Package edit applies the edits of migration steps to the text of one
// YAML document. It keeps every byte that an edit leaves alone, and it
// records where each byte of the new text came from, so an error found in
// the new text binds at the line that the user wrote.
//
// A [Doc] holds the text of a document as the edits leave it. A [Handle]
// names one place of the document, and its methods read and edit the value
// there. A load and a plan of the manifest package share the engine.
package edit

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"unicode/utf8"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/bom"
	"go.jacobcolvin.com/niceyaml/internal/hook"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

// ErrEngine indicates new text that the engine cannot parse, or that does
// not decode to the values the edits wrote. It shows a bug in the engine,
// not in the document.
var ErrEngine = errors.New("migration changed a value it kept")

// Options configures a [Doc].
type Options struct {
	// App names the app in the messages of the engine, as "cafe" or
	// "this program".
	App string
	// TypeFields holds the paths of the type fields, which no step edits.
	TypeFields []paths.Path
}

// Doc is the text of one document as the edits of the steps leave it.
//
// Create instances with [New].
type Doc struct {
	// The first problem of the call that runs, which fails the document
	// unless the step returns its own.
	callErr error
	// The document as the user wrote it.
	user *niceyaml.Node
	// The parse of the current text, and its document.
	src  *niceyaml.Source
	node *niceyaml.Node
	// The places that the calls of the step running edited, by the origin
	// of the first byte of each.
	stepEdits map[int]stepEdit
	// What each mapping that merges or repeats a key read before the step
	// running, and the last edit of the step.
	merges   map[position.Position]*mergeView
	lastEdit *editRecord
	// The text of the lines of the user's document.
	orig string
	// The byte order mark the text starts with, which the parse leaves out.
	bom string
	// The current text, without the byte order mark.
	cur string
	// The label of the version whose step runs, as messages name it.
	label string
	opts  Options
	notes []note
	// The line starts of orig and of cur.
	origLines []int
	curLines  []int
	// The live handles, whose paths each edit keeps up to date.
	handles  []*Handle
	removals []Removal
	// The values that the calls of the step running wrote, which the check
	// after the step compares with what the document reads.
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
}

// note is where the text that an edit wrote binds its errors in the user's
// text, and what it says about the edit.
type note struct {
	// The label of the version whose step wrote the text, or "" for text
	// that holds no value.
	detail string
	path   paths.Path
	at     position.Range
	// The note names a value the file holds, so an error binds there.
	located bool
	hasPath bool
	// The text replaced a value, so the detail says the version set the
	// value from the one the file holds.
	replaced bool
}

// Removal is one value that a step removed.
type Removal struct {
	// Reason is the reason the step gave.
	Reason string
	// Label is the version whose step removed the value.
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

// writtenValue is a value a call wrote, which the check after the step
// compares with what the text reads at path.
type writtenValue struct {
	value any
	path  paths.Path
}

// New creates a new [Doc] that holds the text of user, a document that
// parsed and holds content.
func New(user *niceyaml.Node, opts Options) (*Doc, error) {
	src := user.Source()
	text := src.Text()
	span := user.Span()

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

	d := &Doc{user: user, base: span.Start, opts: opts, start: start, end: end}

	// The parse counts positions without the marks that the tokenizer
	// drops, such as one after the "---" of a later document. The text
	// leaves them out too. A rewrite keeps the mark that opens the text
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

	err := d.parse()
	if err != nil {
		return nil, err
	}

	return d, nil
}

// Text returns the current text of the document, with the byte order mark
// it started with.
func (d *Doc) Text() string {
	return d.bom + d.cur
}

// Node returns the current text of the document as a document, whose
// errors bind in the user's text.
func (d *Doc) Node() *niceyaml.Node {
	return d.node
}

// Bounds returns the bytes [start, end) of the text of the user's Source
// that the document holds, which [Doc.Text] takes the place of in a
// rewrite of the file.
func (d *Doc) Bounds() (int, int) {
	return d.start, d.end
}

// Edited reports whether an edit changed the text.
func (d *Doc) Edited() bool {
	return d.edited
}

// Removals returns the values that the steps removed, in the order of the
// calls.
func (d *Doc) Removals() []Removal {
	return d.removals
}

// lineStarts returns the offset of each line of text.
func lineStarts(text string) []int {
	starts := []int{0}

	for i := range len(text) {
		if text[i] == '\n' {
			starts = append(starts, i+1)
		}
	}

	return starts
}

// parse parses the current text and records its lines.
func (d *Doc) parse() error {
	src, ok := hook.WorkingSource(d.cur, d.user, d.Locate).(*niceyaml.Source)
	if !ok {
		return errors.New("edit: no working source")
	}

	d.src = src
	d.curLines = lineStarts(d.cur)
	d.node = nil

	for _, n := range src.AllDocuments() {
		err := n.Err()
		if err != nil {
			return fmt.Errorf("%w: %w", ErrEngine, err)
		}

		if !n.IsEmpty() || d.node == nil {
			d.node = n
		}

		if !n.IsEmpty() {
			break
		}
	}

	if d.node == nil {
		return fmt.Errorf("%w: the text holds no document", ErrEngine)
	}

	return nil
}

// offset returns the offset in the current text of pos.
func (d *Doc) offset(pos position.Position) int {
	return offsetIn(d.cur, d.curLines, pos)
}

// offsetIn returns the offset in text, whose lines start at starts, of pos.
func offsetIn(text string, starts []int, pos position.Position) int {
	if pos.Line >= len(starts) {
		return len(text)
	}

	off := starts[pos.Line]

	for range pos.Col {
		if off >= len(text) || text[off] == '\n' {
			break
		}

		_, size := utf8.DecodeRuneInString(text[off:])
		off += size
	}

	return off
}

// positionIn returns the position in text, whose lines start at starts,
// of the byte at off.
func positionIn(text string, starts []int, off int) position.Position {
	line, _ := slices.BinarySearch(starts, off+1)
	line--

	return position.New(line, utf8.RuneCountInString(text[starts[line]:off]))
}

// Locate gives the place in the user's text of a location that an error
// resolved in the current text, or in a copy of it that keeps every line
// and column.
func (d *Doc) Locate(at hook.Located) hook.Place {
	place := hook.Place{Path: at.Path, HasPath: at.HasPath}

	if !at.HasRange {
		return place
	}

	start := d.offset(at.Range.Start)
	end := max(start, d.offset(at.Range.End))

	if !d.edited {
		place.Range = d.userRange(start, end)
		place.HasRange = true

		return place
	}

	if start < len(d.origin) && d.origin[start] < 0 {
		return d.notes[-1-d.origin[start]].place(place, at)
	}

	rng := d.userRange(start, end)
	place.Range, place.HasRange = rng, true

	if at.HasPath {
		place.Path = d.userPath(at.Path, rng.Start)
	}

	return place
}

// place returns place, the place of at, a location in text that an edit
// wrote with n, moved to where n binds.
func (n note) place(place hook.Place, at hook.Located) hook.Place {
	if n.located {
		place.Range, place.HasRange = n.at, true
	}

	if n.hasPath {
		place.Path, place.HasPath = n.path, true
	}

	if n.detail != "" && at.HasPath {
		what := n.detail + " sets " + at.Path.String()
		if n.replaced {
			what += " from this value"
		}

		place.Details = append(place.Details, errors.New(what))
	}

	return place
}

// userRange returns the range in the user's text of the bytes [start, end)
// of the current text, which an edit kept.
func (d *Doc) userRange(start, end int) position.Range {
	from := d.userPos(start)
	to := from

	if end > start {
		last := d.userPos(end - 1)
		to = position.New(last.Line, last.Col+1)
	}

	if to.Line < from.Line || to.Line == from.Line && to.Col < from.Col {
		to = from
	}

	return position.NewRange(from, to)
}

// userPos returns the position in the user's text of the byte at off of
// the current text, or of the nearest byte before it that an edit kept.
func (d *Doc) userPos(off int) position.Position {
	for i := min(off, len(d.origin)-1); i >= 0; i-- {
		if o := d.origin[i]; o >= 0 {
			pos := positionIn(d.orig, d.origLines, o)
			pos.Line += d.base

			return pos
		}
	}

	return position.New(d.base, 0)
}

// userPath returns the path of the value that the user's text holds at
// pos, for an error at path in the current text. A path that the current
// text does not hold, such as a field the document leaves out, stays as it
// is, in the terms of the current version.
func (d *Doc) userPath(path paths.Path, pos position.Position) paths.Path {
	// The resolver binds no error. A bound error locates itself through d,
	// which then calls userPath again.
	n, err := d.node.Resolver().Node(path)
	if err != nil || n == nil {
		return path
	}

	got, ok := d.user.PathAt(pos)
	if !ok {
		return path
	}

	last, _ := path.Last()
	gotLast, _ := got.Last()

	switch {
	case last.Kind == paths.SelectorKey && gotLast.Kind != paths.SelectorKey:
		return got.Key()

	case last.Kind != paths.SelectorKey && gotLast.Kind == paths.SelectorKey:
		parent, _ := got.Parent()

		return parent

	default:
		return got
	}
}

// addNote returns the origin of text that an edit writes with n.
func (d *Doc) addNote(n note) int {
	d.notes = append(d.notes, n)

	return -len(d.notes)
}

// splice replaces the bytes [start, end) of the current text with text,
// whose bytes came from origins, and parses the result.
func (d *Doc) splice(start, end int, text string, origins []int) error {
	return d.apply(edit{start: start, end: end, text: text, origins: origins})
}

// edit is one replacement of the bytes [start, end) of the current text.
type edit struct {
	text    string
	origins []int
	start   int
	end     int
}

// apply applies edits, which must not overlap, and parses the result once.
// An insertion goes in front of an edit that replaces the bytes after it,
// and insertions at one offset keep their order. New text that does not
// parse leaves the text as it was.
func (d *Doc) apply(edits ...edit) error {
	cur, origin, src, node, lines, edited := d.cur, d.origin, d.src, d.node, d.curLines, d.edited

	// The edits apply from the end of the text, so no edit moves the
	// offsets of the edits before it. Among the edits that start at one
	// offset, the one that ends later applies first. The reverse keeps the
	// order of insertions at one offset.
	edits = slices.Clone(edits)
	slices.Reverse(edits)
	slices.SortStableFunc(edits, func(a, b edit) int {
		return cmp.Or(cmp.Compare(b.start, a.start), cmp.Compare(b.end, a.end))
	})

	for _, e := range edits {
		d.cur = d.cur[:e.start] + e.text + d.cur[e.end:]
		d.origin = slices.Concat(d.origin[:e.start], e.origins, d.origin[e.end:])
	}

	d.edited = true

	err := d.parse()
	if err != nil {
		bad := d.cur
		d.cur, d.origin, d.src, d.node, d.curLines, d.edited = cur, origin, src, node, lines, edited

		return fmt.Errorf("%w\nnew text:\n%s", err, bad)
	}

	return nil
}

// written returns origins for text that an edit writes with the note
// origin.
func written(text string, origin int) []int {
	origins := make([]int, len(text))
	for i := range origins {
		origins[i] = origin
	}

	return origins
}
