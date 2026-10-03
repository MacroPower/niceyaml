package niceyaml

import (
	"slices"
	"sort"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

// PathAt returns the path of the innermost node of the document at pos,
// and true. The position is in the coordinates of [Source.Lines], where
// line 0 is line 1 of the text, as a cursor in a viewer, the start of a
// [go.jacobcolvin.com/niceyaml/finder.Finder] match, or
// [SourceError.Position] gives one. A viewer thus names the value under
// its cursor:
//
//	if path, ok := doc.PathAt(cursor); ok {
//		status = path.String() // $.spec.containers[0].image
//	}
//
// The path starts at `$`, the root of the document, whichever [Node] of
// the document the caller holds. A `$` path resolves from that root
// through any Node, so the path goes back to the [Node.At] or the
// [Node.Ranges] of the Node that returned it, or of any other Node of
// the document. A position in another document of the source reports
// false, so a caller with a file of several documents asks each one:
//
//	for _, doc := range docs {
//		if path, ok := doc.PathAt(pos); ok {
//			return doc, path
//		}
//	}
//
// The innermost node is an entry of a mapping or an element of a
// sequence. A position on the key of an entry gives a path that ends in
// `~`, as [paths.Path.Key] writes one, and a position on the ":" or the
// value gives the path of the entry. The "-" of a block sequence belongs
// to its element. A collection holds text of its own between its entries
// or elements, such as "[", "{", ",", and the anchor or tag in front of
// it. A position there gives the path of the collection, which is the
// root for a collection or a scalar at the root of the document. A
// position in the indentation of a line resolves to the token that
// follows it, as [line.Lines.TokenAt] describes.
//
// The path is the place where the document writes the node. A position
// on an alias gives the path of the entry or element that holds the
// alias. A position inside the content of an anchor gives the path where
// the anchor defines that content, though a path through an alias or a
// `<<` merge key reaches the same node.
//
// PathAt reports false where no node of the document lies: on a comment,
// on a blank line, on a "---" header, a "..." marker, or a directive, and
// outside the lines of the source. It reports false for a document that
// did not parse, as [Node.Err] describes, and for one with no content.
// It also reports false for a position inside an entry that no path
// selects. Such an entry is the earlier of two entries with one key, one
// that a `<<` merge key after it overrides, or one whose key has no
// name, as [paths.Resolver.KeyName] reports. A path it returns therefore
// selects the node at pos, and none of its selectors is a wildcard.
//
// The first call reads the tree of the document once, and every Node of
// the document shares what it found.
func (n *Node) PathAt(pos position.Position) (paths.Path, bool) {
	if n == nil || n.doc.err != nil {
		return paths.Path{}, false
	}

	tk := n.source.lines.TokenAt(pos)
	if tk == nil || tk.Position == nil || tk.Type == token.CommentType {
		return paths.Path{}, false
	}

	return n.doc.pathIndex().pathAt(n.doc.pathResolver(), tk.Position.Offset)
}

// pathIndex returns the [*pathIndex] of the document, and builds it on
// the first call.
func (d *document) pathIndex() *pathIndex {
	d.pathsOnce.Do(func() {
		d.paths = newPathIndex(d.root.Body)
	})

	return d.paths
}

// pathIndex finds the node of one document that holds a token, by the
// offset of the token, for [Node.PathAt].
//
// It holds one [pathStep] for each entry of a mapping and each element of
// a sequence in the document, which are the nodes a path selector names.
// Each step names the step that holds it, so the steps from one up to the
// root spell the path of its node. The index holds no path, so its size
// grows with the number of steps and not with their depth.
//
// The steps lie in the order a walk of the tree reaches them, each before
// the steps it holds, which is the order of their first offsets. The
// last step that starts at or before an offset is therefore the innermost
// step around that offset, or one that ends before it, whose holders the
// search then tries in turn.
//
// Create instances with [newPathIndex].
type pathIndex struct {
	steps []pathStep
	// The offsets of the tokens of the body.
	body offsetRange
}

// pathStep is one selector of a path, with the offsets of the tokens of
// the node it selects: an entry of a mapping, which a `.name` selector
// names, or an element of a sequence, which an `[n]` selector names.
type pathStep struct {
	// The mapping that holds the entry, or nil for an element.
	mapping *ast.MappingNode
	// The offsets of the first and the last token of the node, comments
	// left out.
	lo, hi int
	// The index in the steps of the step that holds this one, or -1 for
	// a step the body holds.
	parent int
	// The index of the entry in its mapping, or of the element in its
	// sequence.
	index int
}

// entry returns the entry of the step, or nil for an element.
func (s pathStep) entry() *ast.MappingValueNode {
	if s.mapping == nil {
		return nil
	}

	return s.mapping.Values[s.index]
}

// offsetRange holds the offsets of the first and the last of some tokens,
// or nothing while ok is false.
type offsetRange struct {
	lo, hi int
	ok     bool
}

// with returns r widened to hold the offset of tk. A nil token, or one
// without a position, changes nothing.
func (r offsetRange) with(tk *token.Token) offsetRange {
	if tk == nil || tk.Position == nil {
		return r
	}

	off := tk.Position.Offset

	return r.union(offsetRange{lo: off, hi: off, ok: true})
}

// union returns the range that holds both r and other.
func (r offsetRange) union(other offsetRange) offsetRange {
	switch {
	case !other.ok:
		return r
	case !r.ok:
		return other
	}

	return offsetRange{lo: min(r.lo, other.lo), hi: max(r.hi, other.hi), ok: true}
}

// holds reports whether off lies in r.
func (r offsetRange) holds(off int) bool {
	return r.ok && r.lo <= off && off <= r.hi
}

// tokenOffsets returns the offsets of the first and the last token under
// node, comments left out, as f finds them. The finder must skip comments
// and hold no tokens.
func tokenOffsets(f *boundsFinder, node ast.Node) offsetRange {
	if astnode.IsNil(node) {
		return offsetRange{}
	}

	ast.Walk(f, node)

	if len(f.first) == 0 {
		return offsetRange{}
	}

	return offsetRange{lo: f.first[0].Position.Offset, hi: f.last[0].Position.Offset, ok: true}
}

// newPathIndex creates a new [*pathIndex] for a document with the given
// body. A body with no content, as [astnode.HasContent] reports it, has
// no steps and no offsets, so the index finds nothing.
func newPathIndex(body ast.Node) *pathIndex {
	if !astnode.HasContent(body) {
		return &pathIndex{}
	}

	// The count holds the entries and elements inside a key too, which
	// the walk does not step into, so it is room enough for the steps.
	var count stepCounter

	ast.Walk(&count, body)

	b := &pathIndexBuilder{
		steps:  make([]pathStep, 0, int(count)),
		finder: boundsFinder{skipComments: true},
	}
	offsets := b.walk(body, -1)

	return &pathIndex{steps: b.steps, body: offsets}
}

// stepCounter is an [ast.Visitor] that counts the entries of each mapping
// and the elements of each sequence it visits, so [newPathIndex] sizes
// its steps once and a large document does not grow them many times.
type stepCounter int

// Visit implements [ast.Visitor].
func (c *stepCounter) Visit(node ast.Node) ast.Visitor {
	if astnode.IsNil(node) {
		return nil
	}

	switch n := node.(type) {
	case *ast.MappingNode:
		*c += stepCounter(len(n.Values))
	case *ast.SequenceNode:
		*c += stepCounter(len(n.Values))
	}

	return c
}

// pathIndexBuilder collects the steps of a [pathIndex] in one walk of the
// tree.
type pathIndexBuilder struct {
	steps []pathStep
	// Finds the tokens of a node the walk does not step into, and keeps
	// its buffers from one node to the next.
	finder boundsFinder
}

// walk adds the steps under node, each held by the step at index parent,
// and returns the offsets of the tokens under node. It looks through an
// anchor and a tag, whose own tokens belong to the node that holds them.
// A comment belongs to no node.
func (b *pathIndexBuilder) walk(node ast.Node, parent int) offsetRange {
	if astnode.IsNil(node) {
		return offsetRange{}
	}

	switch n := node.(type) {
	case *ast.AnchorNode:
		return b.bounds(n.Name).with(n.GetToken()).union(b.walk(n.Value, parent))

	case *ast.TagNode:
		return offsetRange{}.with(n.GetToken()).union(b.walk(n.Value, parent))

	case *ast.MappingNode:
		offsets := offsetRange{}.with(n.GetToken()).with(n.End)

		for i := range n.Values {
			offsets = offsets.union(b.entry(n, i, parent))
		}

		return offsets

	case *ast.SequenceNode:
		offsets := offsetRange{}.with(n.GetToken()).with(n.End)

		for i, value := range n.Values {
			offsets = offsets.union(b.element(n, i, value, parent))
		}

		return offsets

	default:
		return b.bounds(node)
	}
}

// entry adds the step of the entry at index i of mapping, with the steps
// under its value, and returns the offsets of its tokens. The walk does
// not step into the key, so every token of the key belongs to the entry.
func (b *pathIndexBuilder) entry(mapping *ast.MappingNode, i, parent int) offsetRange {
	entry := mapping.Values[i]
	if entry == nil {
		return offsetRange{}
	}

	id := len(b.steps)
	b.steps = append(b.steps, pathStep{mapping: mapping, index: i, parent: parent})

	offsets := b.bounds(entry.Key).with(entry.GetToken())

	return b.finish(id, offsets.union(b.walk(entry.Value, id)))
}

// element adds the step of the element at index i of seq, with the steps
// under it, and returns the offsets of its tokens. The "-" of a block
// sequence belongs to its element. A flow sequence holds the "," before
// an element in the same place, which belongs to the sequence.
func (b *pathIndexBuilder) element(seq *ast.SequenceNode, i int, value ast.Node, parent int) offsetRange {
	id := len(b.steps)
	b.steps = append(b.steps, pathStep{index: i, parent: parent})

	var offsets offsetRange

	if i < len(seq.Entries) && seq.Entries[i] != nil {
		if start := seq.Entries[i].Start; start != nil && start.Type == token.SequenceEntryType {
			offsets = offsets.with(start)
		}
	}

	return b.finish(id, offsets.union(b.walk(value, id)))
}

// finish sets the offsets of the step at index id and returns them. A
// step whose node holds no token with a position has no offset to find it
// by, so finish drops it. Such a step holds no step, since the offsets of
// a step hold those of every step under it.
func (b *pathIndexBuilder) finish(id int, offsets offsetRange) offsetRange {
	if !offsets.ok {
		b.steps = b.steps[:id]

		return offsets
	}

	b.steps[id].lo, b.steps[id].hi = offsets.lo, offsets.hi

	return offsets
}

// bounds returns the offsets of the tokens under node, comments left out.
func (b *pathIndexBuilder) bounds(node ast.Node) offsetRange {
	b.finder.first, b.finder.last = b.finder.first[:0], b.finder.last[:0]

	return tokenOffsets(&b.finder, node)
}

// stepAt returns the index of the innermost step whose tokens hold the
// offset off, or -1 when no step holds it.
//
// A token that holds no text can share its offset with the first token of
// the next node, as the empty content of a block scalar does with the key
// below it. The step of that next node then starts where the one before
// it ends, and the search picks the later one, whose token holds the
// text at that offset.
func (x *pathIndex) stepAt(off int) int {
	i := sort.Search(len(x.steps), func(i int) bool { return x.steps[i].lo > off }) - 1

	for i >= 0 && off > x.steps[i].hi {
		i = x.steps[i].parent
	}

	return i
}

// pathAt returns the path of the innermost node that holds the token at
// offset off, as [Node.PathAt] describes, with r resolving the keys of
// the document. It reports false for a node that no path selects, as
// [pathIndex.spell] finds.
func (x *pathIndex) pathAt(r *paths.Resolver, off int) (paths.Path, bool) {
	if !x.body.holds(off) {
		return paths.Path{}, false
	}

	at := x.stepAt(off)

	path, selects, ok := x.spell(r, at, x.inKey(at, off))
	if !ok || !selects {
		return paths.Path{}, false
	}

	return path, true
}

// ownerPath returns the path of the outermost node that has tk as its own
// token, which is the node the go-yaml decoder names when it reports tk.
// The results are those of [pathIndex.spell], with ok false for a token
// that no node of the body holds.
//
// A node is the innermost one around its token, with one exception. A
// block mapping has the ":" of its first entry as its token, and a block
// sequence the "-" of its first element. The decoder reports a mapping
// or a sequence by that token, and never the entry or the element, so
// such a token gives the path of the collection. The null the parser
// makes for a value the document leaves out sits at the offset of the
// same ":" or "-", and its type tells it apart, so it gives the path of
// the entry or the element.
func (x *pathIndex) ownerPath(r *paths.Resolver, tk *token.Token) (paths.Path, bool, bool) {
	if tk == nil || tk.Position == nil || !x.body.holds(tk.Position.Offset) {
		return paths.Path{}, false, false
	}

	off := tk.Position.Offset
	at := x.stepAt(off)

	if at >= 0 && x.opens(at, tk) {
		return x.spell(r, x.steps[at].parent, false)
	}

	return x.spell(r, at, x.inKey(at, off))
}

// opens reports whether tk is the token of the block collection that the
// step at index at starts, as its first entry or its first element.
func (x *pathIndex) opens(at int, tk *token.Token) bool {
	step := x.steps[at]
	off := tk.Position.Offset

	if step.mapping == nil {
		return tk.Type == token.SequenceEntryType && step.index == 0 && step.lo == off
	}

	start := step.mapping.GetToken()

	return tk.Type == token.MappingValueType && start != nil && start.Position != nil &&
		start.Position.Offset == off
}

// inKey reports whether the offset off lies in the key of the entry of
// the step at index at. It reports false for an element, and for -1,
// which stands for the body.
func (x *pathIndex) inKey(at, off int) bool {
	if at < 0 {
		return false
	}

	entry := x.steps[at].entry()
	if entry == nil {
		return false
	}

	return tokenOffsets(&boundsFinder{skipComments: true}, entry.Key).holds(off)
}

// spell returns the path of the node of the step at index at, or the
// root for -1, which stands for the body. With key set, the path ends in
// the `~` selector, so it names the key of the entry. The resolver r
// names the keys of the document.
//
// A path selects the entry of a step only when a lookup of its key name
// in its mapping finds that entry, so spell runs that lookup for each
// entry from the node up to the root. The selects result is false when
// one of them finds another entry, or none. The path then names each
// entry on the way as the source spells its key, and resolves to another
// node or to none. The ok result is false when a key on the way has no
// name, as [paths.Resolver.KeyName] reports, so no path spells the node.
func (x *pathIndex) spell(r *paths.Resolver, at int, key bool) (paths.Path, bool, bool) {
	var selectors []paths.Path

	selects := true

	for i := at; i >= 0; i = x.steps[i].parent {
		step := x.steps[i]

		entry := step.entry()
		if entry == nil {
			selectors = append(selectors, paths.Current().Index(step.index))

			continue
		}

		name, ok := r.KeyName(entry.Key)
		if !ok {
			return paths.Path{}, false, false
		}

		found, err := r.Entry(step.mapping, name)
		if err != nil || found != ast.Node(entry) {
			selects = false
		}

		selectors = append(selectors, paths.Current().Child(name))
	}

	slices.Reverse(selectors)

	if key {
		selectors = append(selectors, paths.Current().Key())
	}

	return paths.Doc().Join(selectors...), selects, true
}
