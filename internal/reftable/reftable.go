package reftable

import (
	"slices"
	"sync"

	"github.com/goccy/go-yaml/ast"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/decodetree"
)

// Table holds the anchors of reference documents by name.
//
// The anchors of the documents of a table hide those of the tables it
// reads, and a later table among those hides an earlier one. Within the
// documents, the last anchor of a name in the text wins, even one after
// the alias that reads it. An anchor inside another anchor comes after
// it in the text. In a document, the rule decides each alias that does
// not follow an anchor of its name. It also decides every alias inside a
// reference document.
//
// A decode departs from the rule in one case. When the decoder reads an
// anchor, it registers each anchor inside it again. An alias that it
// reads after the outer anchor then finds the inner one, even when the
// table holds a later anchor of that name.
//
// The go-yaml decoder reads the trees of a table and does not write to
// them, so the decodes of many goroutines share them. A nil Table does
// not hold any anchors.
//
// Create instances with [New].
type Table struct {
	// The anchors of the documents of the table, the last of each name.
	own map[string]*entry
	// The tables whose anchors those of own hide, in order.
	refs []*Table
}

// entry is an anchor of a [Table].
type entry struct {
	// The anchor, a node of a reference document.
	anchor *ast.AnchorNode
	// Returns the anchor as the decoder reads it, built on the first call.
	view func() ast.Node
	// Returns the names the aliases under the anchor read, each once, in
	// document order, found on the first call.
	reads func() []string
}

// New creates a new [*Table] of the anchors under bodies, the bodies of
// documents in order, over the anchors of refs. New skips each nil table
// in refs.
func New(bodies []ast.Node, refs ...*Table) *Table {
	t := &Table{own: map[string]*entry{}, refs: slices.DeleteFunc(slices.Clone(refs), isNil)}

	for _, body := range bodies {
		// The walk visits the anchors in the order their names appear in
		// the text, so an anchor inside another comes after it.
		astnode.Inspect(body, func(n ast.Node) bool {
			anchor, ok := n.(*ast.AnchorNode)
			if !ok {
				return true
			}

			if name, ok := astnode.Name(anchor.Name); ok {
				t.own[name] = newEntry(anchor)
			}

			return true
		})
	}

	return t
}

// isNil reports whether t is nil.
func isNil(t *Table) bool {
	return t == nil
}

// newEntry creates a new [*entry] for anchor.
func newEntry(anchor *ast.AnchorNode) *entry {
	return &entry{
		anchor: anchor,
		view:   sync.OnceValue(func() ast.Node { return decodetree.DecodeView(anchor) }),
		reads:  sync.OnceValue(func() []string { return aliasNames(anchor.Value) }),
	}
}

// Anchor returns the anchor the table holds for name, and reports false
// when it does not hold one.
func (t *Table) Anchor(name string) (*ast.AnchorNode, bool) {
	e := t.entry(name)
	if e == nil {
		return nil, false
	}

	return e.anchor, true
}

// entry returns the entry the table holds for name, or nil.
func (t *Table) entry(name string) *entry {
	if t == nil {
		return nil
	}

	if e, ok := t.own[name]; ok {
		return e
	}

	for _, ref := range slices.Backward(t.refs) {
		if e := ref.entry(name); e != nil {
			return e
		}
	}

	return nil
}

// Prime returns the anchors that a go-yaml decoder reads, in order,
// before it decodes a node whose aliases read names. Each of those
// aliases then finds the anchor the table holds for its name. The list
// also holds every anchor that an alias inside a listed anchor reaches.
// It holds each anchor once, after the anchors that anchor reaches, so
// the aliases inside each find their anchors the same way.
//
// In a loop of anchors that reach each other, the anchor of the loop
// that names reach first comes last. The decoder reads an alias inside
// the anchor it refers to as null. It rejects any other alias that
// closes a loop, because the anchor that alias names comes later in the
// list.
//
// Each anchor comes back as [decodetree.DecodeView] gives it. Prime skips
// each name that the table does not hold an anchor for.
func (t *Table) Prime(names []string) []ast.Node {
	if t == nil || len(names) == 0 {
		return nil
	}

	var (
		order []ast.Node
		seen  = map[*entry]bool{}
		// The entries the walk is inside, each with the index of its next
		// read, innermost last.
		stack []frame
	)

	push := func(name string) {
		e := t.entry(name)
		if e != nil && !seen[e] {
			seen[e] = true
			stack = append(stack, frame{entry: e})
		}
	}

	for _, name := range names {
		push(name)

		for len(stack) > 0 {
			top := len(stack) - 1
			reads := stack[top].entry.reads()

			if next := stack[top].next; next < len(reads) {
				stack[top].next++
				push(reads[next])

				continue
			}

			order = append(order, stack[top].entry.view())
			stack = stack[:top]
		}
	}

	return order
}

// frame is an entry on the stack of [Table.Prime], with the index of the
// next name it reads.
type frame struct {
	entry *entry
	// The index of the next name among the reads of entry.
	next int
}

// aliasNames returns the names the aliases under node read, each once, in
// document order.
func aliasNames(node ast.Node) []string {
	var names []string

	seen := map[string]bool{}

	astnode.Inspect(node, func(n ast.Node) bool {
		alias, ok := n.(*ast.AliasNode)
		if !ok {
			return true
		}

		if name, ok := astnode.Name(alias.Value); ok && !seen[name] {
			seen[name] = true
			names = append(names, name)
		}

		return true
	})

	return names
}
