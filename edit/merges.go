package edit

import (
	"cmp"

	"github.com/goccy/go-yaml/ast"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

// editRecord is an edit of the Apply running, as a message names it.
type editRecord struct {
	// The note of the edit that wrote the value at the place of the edit, as
	// the writer of a [site] holds it, or nil.
	writer *note
	// What the edit does, as a message at the place of the edit names it,
	// such as "renames "closes" to "close"".
	desc string
	// What the edit does, as a message at another place names it, by the
	// path of the place, such as "renames $.hours.closes to "close"".
	named string
	// Where an error at the place of the edit binds, and the path there, as
	// [Doc.valueSite] gives the site. A place that an earlier edit created
	// binds at the site that [Doc.placeSite] gives, under the path of that
	// site.
	path paths.Path
	at   position.Range
}

// holder returns the path of the place of the edit of r without the `~` of
// a key. That path names its entry or its item, or the nearest value above
// it that the user wrote.
func (r editRecord) holder() paths.Path {
	if last, ok := r.path.Last(); ok && last.Kind == paths.SelectorKey {
		path, _ := r.path.Parent()

		return path
	}

	return r.path
}

// entryPlace returns the place in the user's text of the key of e, an
// entry of the current text, which a rename keeps. The place is that of
// the first byte of the key under its tags: the anchor of the key, or else
// the key itself. A key that an edit created takes a place of its own.
func (d *Doc) entryPlace(e *ast.MappingValueNode) position.Position {
	s, ok := d.placeSpan(keyContent(e))
	if !ok || s.start >= len(d.origin) {
		return position.New(-1, 0)
	}

	o := d.origin[s.start]
	if o >= 0 {
		return d.userPos(s.start)
	}

	// A renamed key keeps the place of the key it replaced. A created key,
	// and a key that a move wrote, take the place of the note alone.
	n := d.notes[-1-o]
	if n.located && n.detail == "" && !n.moved {
		return n.at.Start
	}

	return position.New(-2, o)
}

// placeSpan returns the span whose first byte names the place of key, the
// key of an entry, as [Doc.entryPlace] reads it: the anchor of the key under
// its tags, or else the token of the key.
func (d *Doc) placeSpan(key ast.Node) (span, bool) {
	for tag, ok := key.(*ast.TagNode); ok; tag, ok = key.(*ast.TagNode) {
		key = tag.Value
	}

	s, ok := d.propsSpan(key)
	if tk := astnode.PathToken(key, astnode.Parent{}); !ok && tk != nil {
		s, ok = d.tokenSpan(tk)
	}

	return s, ok
}

// recordEdit records an edit of the Apply running, which desc describes.
// A message about a later problem of the Apply names that edit. Named describes
// the edit by its path for a message about another place, or it is "" for
// an edit whose desc names its path already. The edit is at the place p, and
// a problem at that place binds where a problem of the edit binds, as
// [Doc.placeOfSite] gives the site, with its writer.
func (d *Doc) recordEdit(p *place, desc, named string) {
	s := d.placeOfSite(p)

	d.lastEdit = &editRecord{desc: desc, named: cmp.Or(named, desc), path: s.path, at: s.at, writer: s.writer}
}

// site returns the site where an error at the place of the edit of r
// binds, as the record holds it.
func (r editRecord) site() site {
	return site{path: r.holder(), at: r.at, writer: r.writer}
}

// keepOrigins gives each byte of the current text the origin that it had
// before the call running, when the call leaves the text as it found it.
// Such a call changes nothing that a decode could miss. Each record of the
// Apply that reads the origins, such as the merges of [Doc.mergeState],
// then keeps naming the bytes that it named before the call.
func (d *Doc) keepOrigins() {
	if d.cur != d.callStart.cur {
		return
	}

	d.origin = d.callStart.origin
}

// holderStart returns the offset of the byte that holds node, a value of
// the current text under its anchors and tags. That byte starts the key or
// the "?" of an explicit key of the entry, or the "-" of an item of a
// block sequence. For an item of a flow sequence, it is the first byte of
// the item. It reports false for the root and for a key.
func (d *Doc) holderStart(node ast.Node) (int, bool) {
	up := d.ancestors(node)
	child := node

	for len(up) > 0 && isProps(up[0]) {
		child, up = up[0], up[1:]
	}

	if len(up) == 0 {
		return 0, false
	}

	switch parent := up[0].(type) {
	case *ast.MappingValueNode:
		s, ok := d.nodeSpan(parent.Key)
		if !ok || parent.Value != child {
			return 0, false
		}

		if start := d.explicitKeyStart(s.start); start >= 0 {
			return start, true
		}

		return s.start, true

	case *ast.SequenceNode:
		i, ok := d.tree().item[child]
		if !ok || i >= len(parent.Values) || parent.Values[i] != child {
			return 0, false
		}

		if parent.IsFlowStyle || i >= len(parent.Entries) {
			s, ok := d.nodeSpan(child)

			return s.start, ok
		}

		s, ok := d.tokenSpan(parent.Entries[i].Start)

		return s.start, ok
	}

	return 0, false
}

// userWrote reports whether the byte at off, the first byte of a key, a
// "-", or an item of a flow sequence, stands for such text of the user's
// text. A byte that an edit wrote with a note that names the edit does
// not, as for a key that Set creates. A renamed key stands for the key of
// the user, and so do the key and the "-" that a move writes for its
// value. A byte that an edit wrote with the origin of another byte, such
// as a "-" that takes the origin of its value, does not stand for such
// text either.
func (d *Doc) userWrote(off int) bool {
	return d.standsFor(off) || d.ownByte(off)
}

// entryProblem returns the problem msg bound at the key of e, an entry of
// the current text, with the options opts. The problem shows path, the
// path that the user's text spells for the key, unless the user's text
// holds another path there.
func (d *Doc) entryProblem(e *ast.MappingValueNode, path paths.Path, msg string, opts ...niceyaml.ErrorOption) error {
	opts = append(opts, niceyaml.AtPath(path))

	if s, ok := d.nodeSpan(e.Key); ok {
		opts = append(opts, niceyaml.AtRange(position.NewRange(
			positionIn(d.cur, d.curLines, s.start),
			positionIn(d.cur, d.curLines, s.end),
		)))
	}

	return d.bindOwn(niceyaml.NewError(msg, opts...))
}

// comparePositions orders two positions by line, then by column.
func comparePositions(a, b position.Position) int {
	return cmp.Or(cmp.Compare(a.Line, b.Line), cmp.Compare(a.Col, b.Col))
}
