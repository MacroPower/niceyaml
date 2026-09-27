package niceyaml

import (
	"context"
	"sort"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"
)

// anchorIndex lists the nodes of a document that register anchors when
// the go-yaml decoder reads them, so a decode of a node below the body
// primes the anchors its aliases need without reading the whole body.
//
// Create instances with [newAnchorIndex].
type anchorIndex struct {
	// The definitions of each anchor name, in document order.
	defs map[string][]*ast.AnchorNode
	// The nodes that register at least one anchor, in document order.
	entries []anchorEntry
}

// anchorEntry is a node that registers anchors when the decoder reads it:
// an anchor, or a mapping entry with a `<<` merge key, which records
// again the anchors of the mappings it merges.
type anchorEntry struct {
	node ast.Node
	// The names of the anchors a decode of node registers, and the names
	// its aliases read, which include the names read inside each mapping
	// a merge key brings in. Each holds at least the names the decoder
	// uses, and may hold more.
	registers, reads []string
	// The offsets of the first and the last token under node.
	start, end int
}

// newAnchorIndex creates a new [*anchorIndex] for the document body.
func newAnchorIndex(body ast.Node) *anchorIndex {
	var found registrars

	ast.Walk(&found, body)

	idx := &anchorIndex{defs: found.defs}

	for _, node := range found.nodes {
		first, last := tokenBounds(node)
		if len(first) == 0 {
			continue
		}

		names := idx.namesOf(node)
		if len(names.registers) == 0 {
			continue
		}

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

// indexAnchors returns the [*anchorIndex] of the document, and creates it
// on the first call.
func (d *document) indexAnchors() *anchorIndex {
	d.anchorsOnce.Do(func() {
		d.anchors = newAnchorIndex(d.root.Body)
	})

	return d.anchors
}

// primeAnchors registers with dec the anchors that the aliases in node,
// a node below the body of the document, refer to. Each alias refers to
// the last anchor of its name the decoder registers before it. The pass
// decodes the nodes that register anchors and end before node, in
// document order and each on its own. Those are the anchors, and the `<<`
// merge keys, which record again the anchors of the mappings they merge.
// It decodes only the ones that register a name node reads, or a name
// read by one it decodes after them, so its cost follows the anchors node
// needs rather than the size of the document. An anchor or merge key that
// holds node counts for the anchors inside it that end before node. An
// anchor that holds node registers its name as null, as it is while the
// decoder reads the value of the anchor.
//
// The pass only primes anchors, so a failure in it, which concerns a
// value the caller did not ask for, is not the caller's error, and it
// stops none of the decodes after it. An alias the pass could not resolve
// fails again in the decode of the node itself. A context that ends stops
// the pass.
func (d *document) primeAnchors(ctx context.Context, dec *yaml.Decoder, node ast.Node) {
	idx := d.indexAnchors()
	if len(idx.entries) == 0 {
		return
	}

	needed := idx.namesOf(node).reads
	if len(needed) == 0 {
		return
	}

	first, _ := tokenBounds(node)
	if len(first) == 0 {
		return
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

		if ctx.Err() != nil {
			return
		}

		var sink any

		//nolint:errcheck // The pass only primes anchors.
		_ = decodeWithRecover(ctx, dec, decodeView(e.node), &sink)
	}
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
func (idx *anchorIndex) namesOf(node ast.Node) *anchorNames {
	names := &anchorNames{
		defs:      idx.defs,
		registers: map[string]struct{}{},
		reads:     map[string]struct{}{},
		followed:  map[string]bool{},
	}

	ast.Walk(names, node)

	return names
}

// anchorNames is an [ast.Visitor] that collects the names of the anchors
// the nodes it visits define and the names of the aliases they hold. At a
// `<<` merge key, it also visits the content of each anchor that an alias
// in the merged value may name, since the decoder reads the mapping it
// merges again there. It follows each name once, through every anchor
// of that name, so the names hold at least the ones the decoder uses.
type anchorNames struct {
	defs      map[string][]*ast.AnchorNode
	registers map[string]struct{}
	reads     map[string]struct{}
	followed  map[string]bool
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

	case *ast.MappingValueNode:
		if isMergeEntry(n) {
			a.follow(n.Value)
		}
	}

	return a
}

// follow visits the content of each anchor an alias in value may name.
func (a *anchorNames) follow(value ast.Node) {
	aliases := aliasNames{}

	ast.Walk(aliases, value)

	for name := range aliases {
		if a.followed[name] {
			continue
		}

		a.followed[name] = true

		for _, def := range a.defs[name] {
			ast.Walk(a, def.Value)
		}
	}
}

// aliasNames is an [ast.Visitor] that collects the names of the aliases
// it visits.
type aliasNames map[string]struct{}

// Visit implements [ast.Visitor].
func (a aliasNames) Visit(node ast.Node) ast.Visitor {
	if isNilNode(node) {
		return nil
	}

	if alias, ok := node.(*ast.AliasNode); ok {
		if name, ok := nodeName(alias.Value); ok {
			a[name] = struct{}{}
		}
	}

	return a
}

// registrars is an [ast.Visitor] that collects, in document order, every
// node that registers anchors when the decoder reads it, at any depth,
// and the definitions of each anchor name.
type registrars struct {
	defs  map[string][]*ast.AnchorNode
	nodes []ast.Node
}

// Visit implements [ast.Visitor].
func (r *registrars) Visit(node ast.Node) ast.Visitor {
	if isNilNode(node) {
		return nil
	}

	switch n := node.(type) {
	case *ast.AnchorNode:
		r.nodes = append(r.nodes, n)

		if name, ok := nodeName(n.Name); ok {
			if r.defs == nil {
				r.defs = map[string][]*ast.AnchorNode{}
			}

			r.defs[name] = append(r.defs[name], n)
		}

	case *ast.MappingValueNode:
		if isMergeEntry(n) {
			r.nodes = append(r.nodes, n)
		}
	}

	return r
}

// isMergeEntry reports whether entry has a `<<` merge key.
func isMergeEntry(entry *ast.MappingValueNode) bool {
	return entry != nil && entry.Key != nil && entry.Key.IsMergeKey()
}

// nodeName returns the value of the token of node, the name the decoder
// gives an anchor or looks an alias up by.
func nodeName(node ast.Node) (string, bool) {
	if isNilNode(node) {
		return "", false
	}

	tk := node.GetToken()
	if tk == nil {
		return "", false
	}

	return tk.Value, true
}

// setKeys returns the keys of set.
func setKeys(set map[string]struct{}) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}

	return keys
}
