// Package pathindex finds the path of the node of a YAML document that
// holds a token, by the offset of the token.
//
// An [Index] holds one step for each entry of a mapping and each element
// of a sequence, the nodes a path selector names, with the offsets of
// their tokens. [Index.PathAt] finds the innermost step around an offset
// and spells its path, and [Index.OwnerPath] finds the node the go-yaml
// decoder names when it reports a token.
package pathindex

import (
	"slices"
	"sort"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/paths"
)

// Index finds the node of one document that holds a token, by the
// offset of the token.
//
// It holds one step for each entry of a mapping and each element of
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
// Create instances with [New].
type Index struct {
	steps []step
	// The offsets of the tokens of the body.
	body astnode.Extent
}

// step is one selector of a path, with the offsets of the tokens of
// the node it selects: an entry of a mapping, which a `.name` selector
// names, or an element of a sequence, which an `[n]` selector names.
type step struct {
	// The mapping that holds the entry, or nil for an element.
	mapping *ast.MappingNode
	// The offsets of the first and the last token of the node, comments
	// left out.
	lo, hi int
	// The index in the steps of the step that holds this one, or -1 for
	// a step the body holds.
	parent int
	// The offsets of the tokens of the key of the entry, comments left
	// out, which hold no token for an element.
	key astnode.Extent
	// The index of the entry in its mapping, or of the element in its
	// sequence.
	index int
}

// entry returns the entry of the step, or nil for an element.
func (s step) entry() *ast.MappingValueNode {
	if s.mapping == nil {
		return nil
	}

	return s.mapping.Values[s.index]
}

// New creates a new [*Index] for a document with the given
// body. A body with no content, as [astnode.HasContent] reports it, has
// no steps and no offsets, so the index finds nothing.
func New(body ast.Node) *Index {
	if !astnode.HasContent(body) {
		return &Index{}
	}

	// The count holds the entries and elements inside a key too, which
	// the walk does not step into, so it is room enough for the steps.
	var count stepCounter

	ast.Walk(&count, body)

	b := &builder{steps: make([]step, 0, int(count))}
	offsets := b.walk(body, -1)

	return &Index{steps: b.steps, body: offsets}
}

// stepCounter is an [ast.Visitor] that counts the entries of each mapping
// and the elements of each sequence it visits, so [New] sizes
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

// builder collects the steps of an [Index] in one walk of the tree.
type builder struct {
	steps []step
}

// walk adds the steps under node, each held by the step at index parent,
// and returns the offsets of the tokens under node. It looks through an
// anchor and a tag, whose own tokens belong to the node that holds them.
// A comment belongs to no node.
func (b *builder) walk(node ast.Node, parent int) astnode.Extent {
	if astnode.IsNil(node) {
		return astnode.Extent{}
	}

	switch n := node.(type) {
	case *ast.AnchorNode:
		return astnode.ExtentOf(n.Name, false).With(n.GetToken()).Union(b.walk(n.Value, parent))

	case *ast.TagNode:
		return astnode.Extent{}.With(n.GetToken()).Union(b.walk(n.Value, parent))

	case *ast.MappingNode:
		offsets := astnode.Extent{}.With(n.GetToken()).With(n.End)

		for i := range n.Values {
			offsets = offsets.Union(b.entry(n, i, parent))
		}

		return offsets

	case *ast.SequenceNode:
		offsets := astnode.Extent{}.With(n.GetToken()).With(n.End)

		for i, value := range n.Values {
			offsets = offsets.Union(b.element(n, i, value, parent))
		}

		return offsets

	default:
		return astnode.ExtentOf(node, false)
	}
}

// entry adds the step of the entry at index i of mapping, with the steps
// under its value, and returns the offsets of its tokens. The walk does
// not step into the key, so every token of the key belongs to the entry.
func (b *builder) entry(mapping *ast.MappingNode, i, parent int) astnode.Extent {
	entry := mapping.Values[i]
	if entry == nil {
		return astnode.Extent{}
	}

	id := len(b.steps)
	b.steps = append(b.steps, step{mapping: mapping, index: i, parent: parent})

	key := astnode.ExtentOf(entry.Key, false)
	b.steps[id].key = key

	return b.finish(id, key.With(entry.GetToken()).Union(b.walk(entry.Value, id)))
}

// element adds the step of the element at index i of seq, with the steps
// under it, and returns the offsets of its tokens. The "-" of a block
// sequence belongs to its element. A flow sequence holds the "," before
// an element in the same place, which belongs to the sequence.
func (b *builder) element(seq *ast.SequenceNode, i int, value ast.Node, parent int) astnode.Extent {
	id := len(b.steps)
	b.steps = append(b.steps, step{index: i, parent: parent})

	var offsets astnode.Extent

	if i < len(seq.Entries) && seq.Entries[i] != nil {
		if start := seq.Entries[i].Start; start != nil && start.Type == token.SequenceEntryType {
			offsets = offsets.With(start)
		}
	}

	return b.finish(id, offsets.Union(b.walk(value, id)))
}

// finish sets the offsets of the step at index id and returns them. A
// step whose node holds no token with a position has no offset to find it
// by, so finish drops it. Such a step holds no step, since the offsets of
// a step hold those of every step under it.
func (b *builder) finish(id int, offsets astnode.Extent) astnode.Extent {
	if !offsets.OK {
		b.steps = b.steps[:id]

		return offsets
	}

	b.steps[id].lo, b.steps[id].hi = offsets.Lo, offsets.Hi

	return offsets
}

// stepAt returns the index of the innermost step whose tokens hold the
// offset off, or -1 when no step holds it.
//
// A token that holds no text can share its offset with the first token of
// the next node, as the empty content of a block scalar does with the key
// below it. The step of that next node then starts where the one before
// it ends, and the search picks the later one, whose token holds the
// text at that offset.
func (x *Index) stepAt(off int) int {
	i := sort.Search(len(x.steps), func(i int) bool { return x.steps[i].lo > off }) - 1

	for i >= 0 && off > x.steps[i].hi {
		i = x.steps[i].parent
	}

	return i
}

// PathAt returns the path of the innermost node that holds the token at
// offset off, with r resolving the keys of the document. The innermost
// node is an entry of a mapping or an element of a sequence. An offset in
// the key of an entry gives a path that ends in `~`, as [paths.Path.Key]
// writes one, and any other offset in the entry gives the path of the
// entry. The "-" of a block sequence belongs to its element. An offset in
// the text a collection holds between its entries or elements gives the
// path of the collection. It reports false for a node that no path selects, as
// [Index.spell] finds.
func (x *Index) PathAt(r *paths.Resolver, off int) (paths.Path, bool) {
	if !x.body.Holds(off) {
		return paths.Path{}, false
	}

	at := x.stepAt(off)

	path, selects, ok := x.spell(r, at, x.inKey(at, off))
	if !ok || !selects {
		return paths.Path{}, false
	}

	return path, true
}

// OwnerPath returns the path of the outermost node that has tk as its own
// token, which is the node the go-yaml decoder names when it reports tk.
// The second result reports whether the path selects that node, and the
// third whether any path spells it, as [Index.spell] finds them. Both are
// false for a token that no node of the body holds.
//
// A node is the innermost one around its token, with one exception. A
// block mapping has the ":" of its first entry as its token, and a block
// sequence the "-" of its first element. The decoder reports a mapping
// or a sequence by that token, and never the entry or the element, so
// such a token gives the path of the collection. The null the parser
// makes for a value the document leaves out sits at the offset of the
// same ":" or "-", and its type tells it apart, so it gives the path of
// the entry or the element.
func (x *Index) OwnerPath(r *paths.Resolver, tk *token.Token) (paths.Path, bool, bool) {
	if tk == nil || tk.Position == nil || !x.body.Holds(tk.Position.Offset) {
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
func (x *Index) opens(at int, tk *token.Token) bool {
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
func (x *Index) inKey(at, off int) bool {
	return at >= 0 && x.steps[at].key.Holds(off)
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
func (x *Index) spell(r *paths.Resolver, at int, key bool) (paths.Path, bool, bool) {
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
