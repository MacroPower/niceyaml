package aliasing

import (
	"github.com/goccy/go-yaml/ast"

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
	// Binds each alias of body to its anchor.
	resolver *paths.Resolver
	// Enclosed holds each alias that lies inside the anchor it refers to,
	// other than one a `<<` merge key merges. The decoder reads such an
	// alias as null.
	Enclosed map[*ast.AliasNode]bool
	// Holds each alias the decoder reads as null, as [NewFacts] finds it.
	nulls map[*ast.AliasNode]bool
	// Aliases holds each alias of the document, in document order.
	Aliases []AliasFact
	// Anchors holds each anchor of the document, in document order.
	Anchors []AnchorFact
}

// AliasFact is an alias of a document and the anchor it refers to.
type AliasFact struct {
	// Node is the alias.
	Node *ast.AliasNode
	// Anchor is the anchor the resolver binds the alias to, or nil when it
	// binds the alias to none, as for an alias to a reference document.
	Anchor *ast.AnchorNode
	// Name is the name of the alias, or empty when it has none.
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
	// Name is the name of the anchor, or empty when it has none.
	Name string
}

// NewFacts creates new [*Facts] for body, a document body whose aliases r
// binds to their anchors. It walks body once, and keeps a set of the
// anchors the walk is inside, so it knows which anchors each alias lies
// in.
//
// The decoder reads an alias inside the anchor it refers to as null. An
// anchor on such an alias then holds the null, so an alias to that anchor
// reads null too. An alias refers to an anchor before it, so the walk has
// read that anchor by the time it reaches an alias outside it. The
// decoder also reads nothing for an alias that a `<<` merge key names
// inside its own anchor, since it rejects such a merge, so that alias
// counts as null as well.
func NewFacts(body ast.Node, r *paths.Resolver) *Facts {
	f := &Facts{
		body:     body,
		resolver: r,
		Enclosed: map[*ast.AliasNode]bool{},
		nulls:    map[*ast.AliasNode]bool{},
	}

	var (
		// The anchors the walk is inside.
		open = map[*ast.AnchorNode]bool{}
		// Each anchor on an alias the decoder reads as null.
		nullAnchors = map[*ast.AnchorNode]bool{}
		// The aliases `<<` merge keys merge.
		merged = map[*ast.AliasNode]bool{}
		visit  func(ast.Node) bool
	)

	visit = func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.AnchorNode:
			name, _ := astnode.Name(n.Name)
			f.Anchors = append(f.Anchors, AnchorFact{Node: n, Name: name})

			open[n] = true

			astnode.Inspect(n.Value, visit)
			delete(open, n)

			if alias, ok := n.Value.(*ast.AliasNode); ok && f.nulls[alias] {
				nullAnchors[n] = true
			}

			return false

		case *ast.AliasNode:
			f.Aliases = append(f.Aliases, f.alias(n, merged[n], open, nullAnchors))

			return false

		case *ast.MappingValueNode:
			for _, source := range astnode.MergeValues(n) {
				if alias, ok := source.(*ast.AliasNode); ok {
					merged[alias] = true
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
// open set holds the anchors the walk is inside, and nullAnchors holds
// each anchor on an alias the decoder reads as null.
func (f *Facts) alias(alias *ast.AliasNode, merge bool, open, nullAnchors map[*ast.AnchorNode]bool) AliasFact {
	fact := AliasFact{Node: alias, Merge: merge}
	fact.Name, _ = astnode.Name(alias.Value)

	anchor, err := f.resolver.Anchor(alias)
	if node, ok := anchor.(*ast.AnchorNode); ok && err == nil {
		fact.Anchor = node
	}

	if fact.Anchor == nil {
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
