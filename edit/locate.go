package edit

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"unicode/utf8"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/hook"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

// note is where the text that an edit wrote binds its errors in the user's
// text, and what it says about the edit.
type note struct {
	// The subject of the Apply that wrote a value, as messages name it. It
	// is "" for text that holds no value, and for text that stands for text
	// of the user, such as a renamed key.
	detail string
	path   paths.Path
	at     position.Range
	// The note names a value the file holds, so an error binds there.
	located bool
	hasPath bool
	// The text replaced a value, so the detail says the Apply set the value
	// from the one the file holds.
	replaced bool
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
	src, ok := hook.WorkingSource(d.cur, d.user, d.cfg.unmarshalers, d.locate).(*niceyaml.Source)
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

// locate gives the place in the user's text of a location that an error
// resolved in the current text, or in a copy of it that keeps every line
// and column.
func (d *Doc) locate(at hook.Located) hook.Place {
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
// is, in the terms of the current text.
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
