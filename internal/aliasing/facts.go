package aliasing

import (
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/paths"
)

// Facts holds what one walk of a document finds about its anchors and
// aliases.
//
// Create instances with [NewFacts].
type Facts struct {
	// The body of the document, which the counts of the alias limit read.
	body ast.Node
	// The resolver that binds each alias of body to its anchor.
	resolver *paths.Resolver
	// Enclosed holds each alias that lies inside the anchor that it refers
	// to, other than one that a `<<` merge key merges. The decoder reads
	// such an alias as null.
	Enclosed map[*ast.AliasNode]bool
	// The aliases that the decoder reads as null, as [NewFacts] finds them.
	nulls map[*ast.AliasNode]bool
	// Aliases holds each alias of the document, in document order.
	Aliases []AliasFact
	// Anchors holds each anchor of the document, in document order.
	Anchors []AnchorFact
	// ReferenceAlias reports whether the resolver does not bind some alias
	// of the document to an anchor, as for an alias to a reference
	// document.
	ReferenceAlias bool
}

// AliasFact is an alias of a document and the anchor that it refers to.
type AliasFact struct {
	// Node is the alias.
	Node *ast.AliasNode
	// Anchor is the anchor that the resolver binds the alias to, or nil
	// when the resolver does not bind the alias, as for an alias to a
	// reference document.
	Anchor *ast.AnchorNode
	// Name is the name of the alias, or empty when it does not have one.
	Name string
	// Inside reports whether the alias lies under the value of Anchor.
	Inside bool
	// Merge reports whether a `<<` merge key merges the alias.
	Merge bool
}

// AnchorFact is an anchor of a document.
type AnchorFact struct {
	// Node is the anchor.
	Node *ast.AnchorNode
	// Name is the name of the anchor, or empty when it does not have one.
	Name string
	// Extent holds the offsets of the first and the last token under the
	// anchor, comments included, as [astnode.Bounds] finds them.
	Extent astnode.Extent
}

// NewFacts creates new [*Facts] for body, a document body whose aliases r
// binds to their anchors. It walks body once and keeps a stack of the
// anchors that the walk is inside. The stack tells it which anchors each
// alias lies in.
//
// The decoder reads an alias inside the anchor that it refers to as null.
// An anchor on such an alias then holds the null, so an alias to that
// anchor reads null too. An alias refers to an anchor before it, so the walk has
// read that anchor by the time it reaches an alias outside it. The
// decoder rejects a `<<` merge key that names an alias inside its own
// anchor. It does not read a value for that alias, so the alias counts as
// null as well.
func NewFacts(body ast.Node, r *paths.Resolver) *Facts {
	f := &Facts{
		body:     body,
		resolver: r,
		Enclosed: map[*ast.AliasNode]bool{},
		nulls:    map[*ast.AliasNode]bool{},
	}

	var (
		// The indexes in Anchors of the anchors the walk is inside, the
		// innermost last.
		stack []int
		// The anchors the walk is inside.
		open = map[*ast.AnchorNode]bool{}
		// Each anchor on an alias the decoder reads as null.
		nullAnchors = map[*ast.AnchorNode]bool{}
		// The aliases `<<` merge keys merge.
		merged = map[*ast.AliasNode]bool{}
		visit  func(ast.Node) bool
	)

	// Widens the Extent of the innermost open anchor to tk.
	widen := func(tk *token.Token) {
		if len(stack) > 0 {
			top := &f.Anchors[stack[len(stack)-1]]
			top.Extent = top.Extent.With(tk)
		}
	}

	visit = func(node ast.Node) bool {
		anchor, ok := node.(*ast.AnchorNode)
		if ok {
			name, _ := astnode.Name(anchor.Name)

			i := len(f.Anchors)
			f.Anchors = append(f.Anchors, AnchorFact{Node: anchor, Name: name})
			stack = append(stack, i)

			widen(anchor.GetToken())

			if anchor.Comment != nil {
				widen(anchor.Comment.GetToken())
			}

			astnode.Inspect(anchor.Name, visit)

			open[anchor] = true

			astnode.Inspect(anchor.Value, visit)
			delete(open, anchor)

			stack = stack[:len(stack)-1]

			if len(stack) > 0 {
				outer := &f.Anchors[stack[len(stack)-1]]
				outer.Extent = outer.Extent.Union(f.Anchors[i].Extent)
			}

			if alias, ok := anchor.Value.(*ast.AliasNode); ok && f.nulls[alias] {
				nullAnchors[anchor] = true
			}

			return false
		}

		// The walk visits the nodes of a collection, not the token that
		// closes a flow collection or the "-" that opens each entry of a
		// block sequence, which the node holds beside them.
		widen(node.GetToken())

		switch n := node.(type) {
		case *ast.AliasNode:
			f.Aliases = append(f.Aliases, f.alias(n, merged[n], open, nullAnchors))

		case *ast.MappingValueNode:
			for _, source := range astnode.MergeValues(n) {
				if alias, ok := source.(*ast.AliasNode); ok {
					merged[alias] = true
				}
			}

		case *ast.MappingNode:
			widen(n.End)

		case *ast.SequenceNode:
			widen(n.End)

			for _, entry := range n.Entries {
				if entry != nil {
					widen(entry.Start)
				}
			}
		}

		return true
	}

	astnode.Inspect(body, visit)

	return f
}

// alias returns the [AliasFact] of alias, which a `<<` merge key merges
// when merge is set, and records whether the decoder reads it as null. The
// open set holds the anchors that the walk is inside, and nullAnchors
// holds each anchor on an alias that the decoder reads as null.
func (f *Facts) alias(alias *ast.AliasNode, merge bool, open, nullAnchors map[*ast.AnchorNode]bool) AliasFact {
	fact := AliasFact{Node: alias, Merge: merge}
	fact.Name, _ = astnode.Name(alias.Value)

	anchor, err := f.resolver.Anchor(alias)
	if node, ok := anchor.(*ast.AnchorNode); ok && err == nil {
		fact.Anchor = node
	}

	if fact.Anchor == nil {
		f.ReferenceAlias = true

		return fact
	}

	fact.Inside = open[fact.Anchor]

	if fact.Inside || nullAnchors[fact.Anchor] {
		f.nulls[alias] = true
	}

	if fact.Inside && !merge {
		f.Enclosed[alias] = true
	}

	return fact
}
