package edit

import (
	"slices"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/aliasing"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/mapkey"
	"go.jacobcolvin.com/niceyaml/paths"
)

// keptAnchor is an anchor that an edit takes from the text but that an
// alias outside the taken value reads.
type keptAnchor struct {
	// The anchor, and its name.
	anchor *ast.AnchorNode
	name   string
	// The aliases outside the value that holds the anchor that read the
	// anchor.
	readers []reader
}

// reader is an alias that reads an anchor that an edit takes.
type reader struct {
	// The alias.
	ref ref
}

// keeper holds the anchors that an edit takes from the text and that an
// alias outside the taken value reads. Create instances with
// [Doc.anchorsIn].
type keeper struct {
	// The anchors, in the order of the text.
	kept []keptAnchor
}

// anchorsIn returns the [keeper] of an edit that takes nodes from the
// current text. Its anchors are the anchors that each node defines and that
// an alias outside that node reads, in the order of the text, each with
// those aliases. An anchor inside the value of another one of them comes
// after it.
func (d *Doc) anchorsIn(nodes ...ast.Node) *keeper {
	k := &keeper{}

	for _, node := range nodes {
		if astnode.IsNil(node) {
			continue
		}

		outer, ok := d.nodeSpan(node)
		if !ok {
			continue
		}

		astnode.Inspect(node, func(n ast.Node) bool {
			a, ok := n.(*ast.AnchorNode)
			if !ok {
				return true
			}

			name, _ := astnode.Name(a.Name)

			var readers []reader

			for _, r := range d.readersOf(name, a) {
				if !inside(outer, r.start) {
					readers = append(readers, reader{ref: r})
				}
			}

			if _, ok := d.propsSpan(a); ok && len(readers) > 0 {
				k.kept = append(k.kept, keptAnchor{name: name, anchor: a, readers: readers})
			}

			return true
		})
	}

	return k
}

// inside reports whether the byte at off lies in s.
func inside(s span, off int) bool {
	return off >= s.start && off < s.end
}

// ref is an anchor or an alias of one name in the current text.
type ref struct {
	node ast.Node
	// The offset of its first byte.
	start int
	// Whether node is an anchor.
	anchor bool
}

// refs returns the anchors and the aliases of name, in the order of the
// text. The caller must not change the returned slice.
func (d *Doc) refs(name string) []ref {
	if name == "" {
		return nil
	}

	return d.readRefs().refs[name]
}

// aliases returns the aliases of the current text, in the order of the
// text. The caller must not change the returned slice.
func (d *Doc) aliases() []ref {
	return d.readRefs().aliases
}

// readRefs returns the index of the current parse with the anchors and the
// aliases of each name and every alias, which it reads from the
// [aliasing.Facts] of the parse on its first call. The first token of an
// anchor or an alias starts its text, as [Doc.propsSpan] and
// [Doc.nodeSpan] read it.
func (d *Doc) readRefs() *parseIndex {
	t := d.parseIndex()
	if t.refs != nil {
		return t
	}

	facts := aliasing.StateOf(d.node).Facts()
	t.refs, t.aliases, t.anchors = make(map[string][]ref), []ref{}, make(map[*ast.AnchorNode]int)

	for _, a := range facts.Anchors {
		if s, ok := d.tokenSpan(a.Node.Start); ok && a.Name != "" {
			t.refs[a.Name] = append(t.refs[a.Name], ref{node: a.Node, start: s.start, anchor: true})
			t.anchors[a.Node] = s.start
		}
	}

	for _, a := range facts.Aliases {
		s, ok := d.tokenSpan(a.Node.Start)
		if !ok {
			s, ok = d.nodeSpan(a.Node)
		}

		if !ok {
			continue
		}

		r := ref{node: a.Node, start: s.start}

		if a.Name != "" {
			t.refs[a.Name] = append(t.refs[a.Name], r)
		}

		if s.start < len(d.origin) {
			t.aliases = append(t.aliases, r)
		}
	}

	for _, list := range t.refs {
		slices.SortStableFunc(list, func(a, b ref) int { return a.start - b.start })
	}

	slices.SortStableFunc(t.aliases, func(a, b ref) int { return a.start - b.start })

	return t
}

// readersOf returns the aliases of name that read a, an anchor of name, in
// the order of the text. Each alias reads the anchor that the resolver of
// the current text binds it to, as a decode reads it.
func (d *Doc) readersOf(name string, a *ast.AnchorNode) []ref {
	res := d.node.Resolver()

	var out []ref

	for _, r := range d.refs(name) {
		if r.anchor {
			continue
		}

		read, err := res.Anchor(r.node)
		if err == nil && read == a {
			out = append(out, r)
		}
	}

	return out
}

// anchorOf returns the anchor that alias, an alias of the current text,
// reads, as the resolver of the text binds it. It reports false for an
// alias that reads no anchor of the text, such as one of a reference
// document.
func (d *Doc) anchorOf(alias *ast.AliasNode) (ref, bool) {
	read, err := d.node.Resolver().Anchor(alias)
	if err != nil {
		return ref{}, false
	}

	anchor, ok := read.(*ast.AnchorNode)
	if !ok {
		return ref{}, false
	}

	s, ok := d.propsSpan(anchor)
	if !ok {
		return ref{}, false
	}

	return ref{node: anchor, start: s.start, anchor: true}, true
}

// readerPath returns the path in the current text of the value that reads
// node, an alias or an anchor. That is the value that holds node, or the
// mapping that holds the entry when node lies under a `<<` merge key or in a
// key. When no path selects that value, as below a key that is a
// collection, it is the nearest value that holds it and that a path
// selects.
func (d *Doc) readerPath(node ast.Node) (paths.Path, bool) {
	read, child := node, node
	up := d.ancestors(node)

	for i, n := range up {
		if mv, ok := n.(*ast.MappingValueNode); ok && (mv.Value != child || astnode.IsMergeKey(mv.Key)) {
			read = mv

			if i+1 < len(up) {
				if m, ok := up[i+1].(*ast.MappingNode); ok {
					read = m
				}
			}
		}

		child = n
	}

	for _, n := range append([]ast.Node{read}, d.ancestors(read)...) {
		if rel, ok := d.pathIn(d.node.AST(), n); ok {
			return paths.Doc().Join(rel), true
		}
	}

	return paths.Path{}, false
}

// pathIn returns the path from root to node, a node that root holds, as an
// `@` path of names and indexes. It reports false for a node that root does
// not hold as a value.
func (d *Doc) pathIn(root, node ast.Node) (paths.Path, bool) {
	var sels []paths.Selector

	child := node

	for _, up := range d.ancestors(node) {
		if child == root {
			break
		}

		switch x := up.(type) {
		case *ast.MappingValueNode:
			name, ok := d.node.Resolver().KeyName(x.Key)
			if !ok || x.Value != child {
				return paths.Path{}, false
			}

			sels = append(sels, paths.Selector{Kind: paths.SelectorChild, Name: name})

		case *ast.SequenceNode:
			i, ok := d.tree().item[child]
			if !ok || i >= len(x.Values) || x.Values[i] != child {
				return paths.Path{}, false
			}

			sels = append(sels, paths.Selector{Kind: paths.SelectorIndex, Index: i})

		case *ast.DocumentNode:
			return paths.Path{}, false
		}

		child = up
	}

	if child != root {
		return paths.Path{}, false
	}

	slices.Reverse(sels)

	return appendSelectors(paths.Current(), sels), true
}

// isProps reports whether node is an anchor or a tag, which wraps a value.
func isProps(node ast.Node) bool {
	switch node.(type) {
	case *ast.AnchorNode, *ast.TagNode:
		return true
	default:
		return false
	}
}

// ancestors returns the nodes that hold node in the syntax tree of the
// current text, nearest first, or nil when the tree does not hold it.
func (d *Doc) ancestors(node ast.Node) []ast.Node {
	t := d.tree()

	var out []ast.Node

	for n, ok := t.parent[node]; ok; n, ok = t.parent[n] {
		out = append(out, n)
	}

	return out
}

// inText reports whether the syntax tree of the current text holds node.
// A node of a reference document lies outside it.
func (d *Doc) inText(node ast.Node) bool {
	t := d.tree()
	_, ok := t.parent[node]

	return ok || node == t.root
}

// parseIndex indexes one parse of the text, so a lookup does not walk the
// syntax tree. Each part fills on the first lookup that needs it, and the
// next parse takes a new index. Create instances with [Doc.parseIndex].
type parseIndex struct {
	// The parse, and the root of its tree.
	node *niceyaml.Node
	root ast.Node
	// The node that holds each node other than the root, and the index of
	// each item of a sequence, or nil before [Doc.tree] reads them.
	parent map[ast.Node]ast.Node
	item   map[ast.Node]int
	// The anchors and the aliases of each name in the order of the text,
	// and the offset of the first byte of each anchor of a name, or nil
	// before [Doc.readRefs] reads them.
	refs    map[string][]ref
	anchors map[*ast.AnchorNode]int
	// The reader of the keys, or nil before [Doc.keyReader] makes it.
	keys *mapkey.Keys
	// The aliases that lie inside the value of the anchor that each refers
	// to, or nil before [Doc.enclosedAliases] reads them.
	enclosed map[*ast.AliasNode]bool
	// The zone of each << merge key in a refused form as the parse holds
	// it, or nil for a zone that the parse no longer holds. [Doc.liveZone]
	// fills the map.
	live map[*zone]*zone
	// The merge entries of the parse by the starts of their keys, or nil
	// before [Doc.mergeKeyStarts] reads them.
	mergeStarts map[int]*ast.MappingValueNode
	// The span of each node that [Doc.nodeSpan] read in the text of the
	// parse.
	spans map[ast.Node]spanOf
	// Every alias in the order of the text, which [Doc.readRefs] reads
	// with refs.
	aliases []ref
	// The block scalars of the tree, or nil before [Doc.literals] reads
	// them, and the block scalars with a header and a string by the offsets
	// of their headers, or nil before [Doc.headers] reads them.
	literals []*ast.LiteralNode
	headers  []headerAt
	// The comments of the text that open after white space, or nil before
	// [Doc.openComments] reads them.
	comments []span
	// The mappings that hold a << merge key, or nil before
	// [Doc.mergeMappings] reads them.
	merges []mergeMapping
	// The walk of the tree, or nil before [Doc.walked] makes it.
	walk *nodeWalk
	// The tokens of the Source, or nil before [Doc.tokens] reads them, and
	// its comment tokens, or nil before [Doc.commentTokens] reads them.
	tokens, commentToks token.Tokens
	// The misnested entries and items, or nil before [Doc.misnests] reads
	// them.
	misnests []misnest
	// The kept merges, or nil before [Doc.keptMerges] reads them.
	kept []keptMerge
	// The form of each << merge key that [Doc.formOf] read.
	forms map[*ast.MappingValueNode]mergeForm
	// The keys of each mapping that [Doc.ownKeys] read.
	own map[ast.Node][]string
	// The mappings whose names a merge of a reference document decides, or
	// nil before [Doc.referenceNames] reads them.
	refNames map[ast.Node]*ast.MappingValueNode
	// The comment origins that [Doc.commentOrigins] read, with the slice of
	// origins that it read them for.
	commented originMemo[int]
}

// originMemo is what a check read from a parse for one slice of the
// origins of its text. An edit makes a new slice of origins and does not
// change the one before it, so the slice names the origins that the check
// read. A call that leaves the text as it found it takes back the slice
// before it, as [Doc.keepOrigins] does, so one parse can hold two slices.
type originMemo[T any] struct {
	origin []int
	out    []T
	set    bool
}

// read returns what read reads for origin, which it reads again when the
// memo holds what it read for another slice.
func (m *originMemo[T]) read(origin []int, read func() []T) []T {
	if !m.set || len(m.origin) != len(origin) || len(origin) > 0 && &m.origin[0] != &origin[0] {
		m.origin, m.out, m.set = origin, read(), true
	}

	return m.out
}

// spanOf is a span that [Doc.nodeSpan] read, and whether the node has
// one.
type spanOf struct {
	s  span
	ok bool
}

// parseIndex returns the index of the current parse of the text. It makes
// a new index after each parse. The Doc keeps one other index: the index of
// the text that the call running found, which a check can show again, as
// [Doc.heldComment] does, or else the index of the parse before. A Move
// parses the text twice, so the text that the call found can lie two
// parses back.
func (d *Doc) parseIndex() *parseIndex {
	switch {
	case d.index != nil && d.index.node == d.node:
	case d.other != nil && d.other.node == d.node:
		d.index, d.other = d.other, d.index
	default:
		keep := d.index
		if start := d.callStart.node; d.other != nil && d.other.node == start && (keep == nil || keep.node != start) {
			keep = d.other
		}

		d.index, d.other = &parseIndex{node: d.node, root: d.node.AST()}, keep
	}

	return d.index
}

// tree returns the index of the current parse with the parent of each node
// and the index of each item. It reads them from the walk of the parse on
// its first call for the parse. The parent of a node is the node of its
// first visit. A later visit of a node, or of the root, adds no parents.
func (d *Doc) tree() *parseIndex {
	t := d.parseIndex()
	if t.parent != nil {
		return t
	}

	w := d.walked()
	t.parent, t.item = make(map[ast.Node]ast.Node, len(w.nodes)), make(map[ast.Node]int)

	for i := 0; i < len(w.nodes); {
		n := w.nodes[i]
		if _, seen := t.parent[n]; seen || i > 0 && n == t.root {
			i = int(w.end[i])

			continue
		}

		if i > 0 {
			t.parent[n] = w.nodes[w.up[i]]
		}

		if seq, ok := n.(*ast.SequenceNode); ok {
			for j, v := range seq.Values {
				if _, seen := t.item[v]; !seen {
					t.item[v] = j
				}
			}
		}

		i++
	}

	return t
}

// nodeWalk is the walk of the tree of a parse from its root, as
// [astnode.Inspect] walks it. It holds each node in the order of the walk
// and the index of the node whose children hold it, or -1 for the root. It
// also holds the index past the last node below each node. The checks
// after a call read each parse through its one walk, as [Doc.inspect]
// reads it.
type nodeWalk struct {
	nodes   []ast.Node
	up, end []int32
	// The entries, the tags, and the block scalars of nodes, in their order,
	// which [nodeWalk.kinds] reads on its first call.
	entries  []*ast.MappingValueNode
	tags     []*ast.TagNode
	literals []*ast.LiteralNode
	typed    bool
}

// kinds returns w with its entries, its tags, and its block scalars.
func (w *nodeWalk) kinds() *nodeWalk {
	if w.typed {
		return w
	}

	w.typed = true

	for _, n := range w.nodes {
		switch x := n.(type) {
		case *ast.MappingValueNode:
			w.entries = append(w.entries, x)
		case *ast.TagNode:
			w.tags = append(w.tags, x)
		case *ast.LiteralNode:
			w.literals = append(w.literals, x)
		}
	}

	return w
}

// walked returns the walk of the current parse, which it makes on its
// first call for the parse.
func (d *Doc) walked() *nodeWalk {
	t := d.parseIndex()
	if t.walk == nil {
		t.walk = walkTree(t.root, len(d.tokens())*5/4+1)
	}

	return t.walk
}

// walkTree returns the walk of the tree under root, with room for size
// nodes at first. A tree holds about one node for each token of its text,
// and a mapping or an entry adds one without a token of its own.
func walkTree(root ast.Node, size int) *nodeWalk {
	w := &nodeWalk{nodes: make([]ast.Node, 0, size), up: make([]int32, 0, size)}
	b := &walkBuilder{w: w}
	ast.Walk(b.level(0, -1), root)

	w.end = make([]int32, len(w.nodes))
	for i := range w.end {
		w.end[i] = int32(i + 1) //nolint:gosec // A tree holds fewer nodes than an int32 counts.
	}

	// Each node comes after the node that holds it.
	for i := len(w.nodes) - 1; i > 0; i-- {
		if up := w.up[i]; w.end[up] < w.end[i] {
			w.end[up] = w.end[i]
		}
	}

	return w
}

// walkBuilder holds the walk that the visitors of a [walkRecorder] record,
// and the visitor of each depth of the tree.
type walkBuilder struct {
	w      *nodeWalk
	levels []*walkRecorder
}

// level returns the visitor that visits the children of the node at the
// index at, which lies one level above depth. [ast.Walk] walks the tree
// depth first, and it visits the children of a node with the visitor that
// the node returned. It is through with that visitor before it visits the
// next node of the same depth, so each depth needs one visitor.
func (b *walkBuilder) level(depth int, at int32) *walkRecorder {
	if depth == len(b.levels) {
		b.levels = append(b.levels, &walkRecorder{b: b, depth: depth})
	}

	r := b.levels[depth]
	r.at = at

	return r
}

// walkRecorder is the [ast.Visitor] that records a [nodeWalk]. Each
// visitor holds the index of the node whose children it visits, and its
// depth in the tree.
type walkRecorder struct {
	b     *walkBuilder
	depth int
	at    int32
}

// Visit implements [ast.Visitor].
func (r *walkRecorder) Visit(node ast.Node) ast.Visitor {
	if astnode.IsNil(node) {
		return nil
	}

	w := r.b.w
	i := int32(len(w.nodes)) //nolint:gosec // A tree holds fewer nodes than an int32 counts.
	w.nodes = append(w.nodes, node)
	w.up = append(w.up, r.at)

	return r.b.level(r.depth+1, i)
}

// inspect calls f for each node of the tree of the current parse, as
// [astnode.Inspect] calls it from the root, from the walk of the parse.
// When f returns false, inspect skips the nodes below the node.
func (d *Doc) inspect(f func(ast.Node) bool) {
	w := d.walked()

	for i := 0; i < len(w.nodes); {
		if f(w.nodes[i]) {
			i++
		} else {
			i = int(w.end[i])
		}
	}
}

// tokens returns the tokens of the Source of the current parse, which it
// reads on its first call for the parse. The caller must not change the
// returned slice.
func (d *Doc) tokens() token.Tokens {
	t := d.parseIndex()
	if t.tokens == nil {
		t.tokens = d.src.Tokens()
	}

	return t.tokens
}

// commentTokens returns the comment tokens of the Source of the current
// parse, in the order of the text, which it reads on its first call for the
// parse. The caller must not change the returned slice.
func (d *Doc) commentTokens() token.Tokens {
	t := d.parseIndex()
	if t.commentToks == nil {
		t.commentToks = d.readCommentTokens()
	}

	return t.commentToks
}

// readCommentTokens reads the comment tokens of the current text, as
// [Doc.commentTokens] returns them.
func (d *Doc) readCommentTokens() token.Tokens {
	out := token.Tokens{}

	for _, tk := range d.tokens() {
		if tk.Type == token.CommentType {
			out = append(out, tk)
		}
	}

	return out
}

// parentAt returns the node whose children hold the node at i of w, or
// nil for the root.
func (w *nodeWalk) parentAt(i int) ast.Node {
	if w.up[i] < 0 {
		return nil
	}

	return w.nodes[w.up[i]]
}
