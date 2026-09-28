package niceyaml

import (
	"context"
	"errors"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"
)

// decodeTree is the tree the go-yaml decoder reads for one document.
//
// The decoder keeps the anchors it has read in maps keyed by name, so an
// anchor it reads later hides an earlier one of the same name from every
// alias it reads after that. It reads a node in two passes, and a decode
// of a node below the body reads the anchors outside the node before it,
// so neither order matches the document. The tree therefore gives each
// anchor whose name another anchor of the document shares a name of its
// own, and gives each alias the name of the anchor the document's
// [paths.Resolver] binds it to, so every alias reads that anchor whatever
// order the decoder reads the anchors in.
//
// Renaming changes tokens, which the tree the [Source] shares must not
// see, so a document that renames an anchor reads a second parse of the
// Source from [Source.decodeParse]. A renamed anchor keeps the text of the
// Source, so the text the decoder hands an UnmarshalYAML method holds the
// names the document spells. A document whose anchor names differ reads
// the tree of the Source through [decodeView], which copies only the
// aliases, and the nodes above them, to drop the comments on their names.
//
// Create instances with [newDecodeTree].
type decodeTree struct {
	// The body of the document as the Source parsed it.
	source ast.Node
	// The body of the document as the decoder reads it.
	body ast.Node
	// The node of body that stands for each node of source outside its
	// comments, which scoped fills for the first decode of a node below
	// the body when the tree renames no anchor.
	nodes map[ast.Node]ast.Node
	// The tokens of the second parse the tree comes from, if any, which
	// the errors of the decoder may name.
	tokens map[*token.Token]struct{}
	// Spells each name the tree gives an anchor as the document does,
	// for the messages the decoder reports, or nil when no anchor has a
	// name of its own.
	names *strings.Replacer
	// The anchors of body, which scoped lists for the first decode of a
	// node below the body.
	anchors *anchorIndex
	// Fills nodes and anchors once.
	scopedOnce sync.Once
}

// decodeTree returns the [*decodeTree] of the document, and builds it on
// the first call.
func (d *document) decodeTree() *decodeTree {
	d.treeOnce.Do(func() {
		d.tree = d.newDecodeTree()
	})

	return d.tree
}

// newDecodeTree creates a new [*decodeTree] for the document.
func (d *document) newDecodeTree() *decodeTree {
	body := d.root.Body

	if shared := sharedAnchorNames(body); len(shared) > 0 {
		if tree, ok := d.renamedTree(shared); ok {
			return tree
		}
	}

	return &decodeTree{source: body, body: decodeView(body)}
}

// renamedTree returns the [*decodeTree] of the document from a second
// parse of the Source, with a name of its own for each anchor whose name
// is in shared. It reports false when the second parse does not give the
// document the same nodes, which a parse of the same tokens always does.
func (d *document) renamedTree(shared map[string]bool) (*decodeTree, bool) {
	src := d.node.source

	file, fileTokens := src.decodeParse()
	if file == nil {
		return nil, false
	}

	i := slices.Index(src.file.Docs, d.root)
	if i < 0 || i >= len(file.Docs) {
		return nil, false
	}

	body := file.Docs[i].Body

	nodes, ok := pairNodes(d.root.Body, body)
	if !ok {
		return nil, false
	}

	tokens := tokenCollector{}
	ast.Walk(tokens, body)

	for tk := range fileTokens {
		tokens[tk] = struct{}{}
	}

	// Each anchor with a shared name gets the name followed by the count
	// of anchors of that name so far. A name never holds a space, so the
	// new name is no other anchor's.
	renamed := map[ast.Node]string{}
	count := map[string]int{}

	var pairs []string

	for _, n := range sourceNodes(d.root.Body) {
		anchor, ok := n.(*ast.AnchorNode)
		if !ok {
			continue
		}

		name, ok := nodeName(anchor.Name)
		if !ok || !shared[name] {
			continue
		}

		count[name]++
		unique := name + " [" + strconv.Itoa(count[name]) + "]"
		renamed[anchor] = unique
		pairs = append(pairs, unique, name)

		// The decoder names an anchor by the value of the token of its
		// name, while the go-yaml formatter writes the token's text.
		if view, ok := nodes[anchor].(*ast.AnchorNode); ok {
			if tk := nodeToken(view.Name); tk != nil {
				tk.Value = unique
			}
		}
	}

	resolver := d.pathResolver()

	for n, v := range nodes {
		alias, ok := n.(*ast.AliasNode)
		if !ok {
			continue
		}

		view, ok := v.(*ast.AliasNode)
		if !ok {
			continue
		}

		// An alias that names no anchor before it keeps its name, so the
		// decoder reports it as the document spells it.
		name := ""

		anchor, err := resolver.Anchor(alias)
		if err == nil {
			name = renamed[anchor]
		}

		renameAlias(view, name)
	}

	return &decodeTree{
		source: d.root.Body,
		body:   body,
		nodes:  nodes,
		tokens: tokens,
		names:  strings.NewReplacer(pairs...),
	}, true
}

// renameAlias gives alias, a node of a second parse, a name free of
// comments, as [decodeView] does, and changes that name to name when name
// is not empty. The decoder looks an alias up by the value of the token
// of its name, and by the text of the node of its name.
func renameAlias(alias *ast.AliasNode, name string) {
	tk := nodeToken(alias.Value)
	if tk == nil {
		return
	}

	if name != "" {
		tk.Value = name
	}

	alias.Value = ast.String(tk)
}

// view returns the node of the tree that stands for node, a node of the
// document outside its comments. A node the tree does not hold, which no
// node of the document is, comes back as [decodeView] reads it.
func (t *decodeTree) view(node ast.Node) ast.Node {
	if node == t.source {
		return t.body
	}

	t.scoped()

	if v, ok := t.nodes[node]; ok {
		return v
	}

	return decodeView(node)
}

// index returns the [*anchorIndex] of the tree.
func (t *decodeTree) index() *anchorIndex {
	t.scoped()

	return t.anchors
}

// scoped fills the parts of the tree that only a decode of a node below
// the body reads, on the first call.
func (t *decodeTree) scoped() {
	t.scopedOnce.Do(func() {
		if t.nodes == nil {
			t.nodes, _ = pairNodes(t.source, t.body)
		}

		t.anchors = newAnchorIndex(t.body)
	})
}

// restoreNames returns msg with each name the tree gave an anchor spelled
// as the document does.
func (t *decodeTree) restoreNames(msg string) string {
	if t.names == nil {
		return msg
	}

	return t.names.Replace(msg)
}

// restoreError returns err with a message that spells each name the tree
// gave an anchor as the document does. It returns err itself when the
// message names no such anchor, and when err holds an [*Error] or a
// [*SourceError], whose message the binding writes from its parts.
func (t *decodeTree) restoreError(err error) error {
	if t.names == nil {
		return err
	}

	var (
		located *Error
		bound   *SourceError
	)

	if errors.As(err, &located) || errors.As(err, &bound) {
		return err
	}

	msg := err.Error()

	restored := t.names.Replace(msg)
	if restored == msg {
		return err
	}

	return restoredError{err: err, msg: restored}
}

// restoredError is an error from the decoder with a message that spells
// the names of anchors as the document does. It unwraps to the error the
// decoder returned.
type restoredError struct {
	err error
	msg string
}

func (e restoredError) Error() string {
	return e.msg
}

func (e restoredError) Unwrap() error {
	return e.err
}

// sharedAnchorNames returns the names that more than one anchor under
// body has.
func sharedAnchorNames(body ast.Node) map[string]bool {
	seen := map[string]bool{}
	shared := map[string]bool{}

	for _, n := range sourceNodes(body) {
		anchor, ok := n.(*ast.AnchorNode)
		if !ok {
			continue
		}

		name, ok := nodeName(anchor.Name)
		if !ok {
			continue
		}

		if seen[name] {
			shared[name] = true
		}

		seen[name] = true
	}

	return shared
}

// pairNodes maps each node under a, outside its comments, to the node in
// the same place under b. It reports false when the two trees differ in
// shape.
func pairNodes(a, b ast.Node) (map[ast.Node]ast.Node, bool) {
	from, to := sourceNodes(a), sourceNodes(b)
	if len(from) != len(to) {
		return nil, false
	}

	pairs := make(map[ast.Node]ast.Node, len(from))

	for i, n := range from {
		if n.Type() != to[i].Type() {
			return nil, false
		}

		pairs[n] = to[i]
	}

	return pairs, true
}

// sourceNodes returns the nodes under node outside its comments, in the
// order [ast.Walk] visits them.
func sourceNodes(node ast.Node) []ast.Node {
	var nodes nodeCollector

	ast.Walk(&nodes, node)

	return nodes
}

// nodeCollector is an [ast.Visitor] that collects the nodes it visits,
// and leaves out comments.
type nodeCollector []ast.Node

// Visit implements [ast.Visitor].
func (c *nodeCollector) Visit(node ast.Node) ast.Visitor {
	if isNilNode(node) || node.Type() == ast.CommentType {
		return nil
	}

	*c = append(*c, node)

	return c
}

// anchorIndex lists the anchors of a [decodeTree], so a decode of a node
// below the body primes the anchors its aliases need without reading the
// whole body.
//
// Create instances with [newAnchorIndex].
type anchorIndex struct {
	// The anchors, in document order.
	entries []anchorEntry
}

// anchorEntry is an anchor, which registers its name and the names of the
// anchors inside it when the decoder reads it.
type anchorEntry struct {
	node ast.Node
	// The names of the anchors a decode of node registers, and the names
	// its aliases read.
	registers, reads []string
	// The offsets of the first and the last token under node.
	start, end int
}

// newAnchorIndex creates a new [*anchorIndex] for body.
func newAnchorIndex(body ast.Node) *anchorIndex {
	idx := &anchorIndex{}

	for _, node := range sourceNodes(body) {
		if _, ok := node.(*ast.AnchorNode); !ok {
			continue
		}

		first, last := tokenBounds(node)
		if len(first) == 0 {
			continue
		}

		names := namesOf(node)

		idx.entries = append(idx.entries, anchorEntry{
			node:      node,
			registers: setKeys(names.registers),
			reads:     setKeys(names.reads),
			start:     first[0].Position.Offset,
			end:       last[0].Position.Offset,
		})
	}

	// The walk visits a node before the nodes inside it, and a stable sort
	// keeps that order for nodes that start at one offset.
	sort.SliceStable(idx.entries, func(i, j int) bool {
		return idx.entries[i].start < idx.entries[j].start
	})

	return idx
}

// primeAnchors registers with dec the anchors that the aliases in node,
// a node of the [decodeTree] below its body, refer to. The pass decodes
// the anchors that end before node, in document order and each on its
// own. It decodes only the ones that register a name node reads, or a
// name read by one it decodes after them, so its cost follows the anchors
// node needs rather than the size of the document. An anchor that holds
// node counts for the anchors inside it that end before node, and
// registers its own name as null, as it is while the decoder reads the
// value of the anchor.
//
// Every anchor of the tree has a name of its own, so the pass decodes
// only anchors that node, or an anchor node reads, refers to. A failure
// in one of them would leave an alias with nothing to read, so the pass
// stops and returns it, as a decode of the whole document fails there
// too. A context that ends stops the pass, and the pass then returns nil.
func (d *document) primeAnchors(ctx context.Context, dec *yaml.Decoder, node ast.Node) error {
	idx := d.decodeTree().index()
	if len(idx.entries) == 0 {
		return nil
	}

	needed := namesOf(node).reads
	if len(needed) == 0 {
		return nil
	}

	first, _ := tokenBounds(node)
	if len(first) == 0 {
		return nil
	}

	start := first[0].Position.Offset

	// The outermost entries that end before node. One that holds node is
	// no candidate, and the entries inside it are.
	var candidates []anchorEntry

	covered := -1

	for _, e := range idx.entries {
		if e.start >= start {
			break
		}

		switch {
		case e.start <= covered:
			continue

		case e.end >= start:
			if anchor, ok := e.node.(*ast.AnchorNode); ok {
				candidates = append(candidates, pendingEntry(anchor))
			}

		default:
			candidates = append(candidates, e)
			covered = e.end
		}
	}

	// From the last candidate back, keep each one that registers a name
	// something after it reads, and add the names it reads itself.
	keep := make([]bool, len(candidates))

	for i := len(candidates) - 1; i >= 0; i-- {
		e := candidates[i]
		if !readsAny(needed, e.registers) {
			continue
		}

		keep[i] = true

		for _, name := range e.reads {
			needed[name] = struct{}{}
		}
	}

	for i, e := range candidates {
		if !keep[i] {
			continue
		}

		// A context that ends stops the pass.
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		var sink any

		err := decodeWithRecover(ctx, dec, e.node, &sink)
		if err != nil {
			return err
		}
	}

	return nil
}

// pendingEntry returns the entry for an anchor with the name of anchor
// over a null. The decoder registers a name as null while it reads the
// value of its anchor, so an alias inside that value reads as null, and
// the entry registers the name that way for an alias in a node inside
// anchor.
func pendingEntry(anchor *ast.AnchorNode) anchorEntry {
	var pos *token.Position

	if anchor.Start != nil {
		pos = anchor.Start.Position
	}

	pending := &ast.AnchorNode{
		BaseNode: &ast.BaseNode{},
		Start:    anchor.Start,
		Name:     anchor.Name,
		Value:    ast.Null(token.New("null", "null", pos)),
	}

	var registers []string

	if name, ok := nodeName(anchor.Name); ok {
		registers = []string{name}
	}

	return anchorEntry{node: pending, registers: registers}
}

// readsAny reports whether needed holds any of names.
func readsAny(needed map[string]struct{}, names []string) bool {
	for _, name := range names {
		if _, ok := needed[name]; ok {
			return true
		}
	}

	return false
}

// namesOf returns the names of the anchors a decode of node registers,
// and the names its aliases read.
func namesOf(node ast.Node) *anchorNames {
	names := &anchorNames{
		registers: map[string]struct{}{},
		reads:     map[string]struct{}{},
	}

	ast.Walk(names, node)

	return names
}

// anchorNames is an [ast.Visitor] that collects the names of the anchors
// the nodes it visits define and the names of the aliases they hold.
type anchorNames struct {
	registers map[string]struct{}
	reads     map[string]struct{}
}

// Visit implements [ast.Visitor].
func (a *anchorNames) Visit(node ast.Node) ast.Visitor {
	if isNilNode(node) {
		return nil
	}

	switch n := node.(type) {
	case *ast.AnchorNode:
		if name, ok := nodeName(n.Name); ok {
			a.registers[name] = struct{}{}
		}

	case *ast.AliasNode:
		if name, ok := nodeName(n.Value); ok {
			a.reads[name] = struct{}{}
		}
	}

	return a
}

// nodeName returns the value of the token of node, the name the decoder
// gives an anchor or looks an alias up by.
func nodeName(node ast.Node) (string, bool) {
	tk := nodeToken(node)
	if tk == nil {
		return "", false
	}

	return tk.Value, true
}

// nodeToken returns the token of node, or nil for a nil node, including
// a typed nil a hand-built tree may hold.
func nodeToken(node ast.Node) *token.Token {
	if isNilNode(node) {
		return nil
	}

	return node.GetToken()
}

// setKeys returns the keys of set.
func setKeys(set map[string]struct{}) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}

	return keys
}
