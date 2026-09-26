package paths

import (
	"fmt"

	"github.com/goccy/go-yaml/ast"
)

// Resolver resolves paths in one document. [NewResolver] binds each alias
// of the document to its anchor once, so resolving many paths in one
// document walks it once rather than once per path. After a change to the
// document, create a new Resolver.
//
// Create instances with [NewResolver].
type Resolver struct {
	doc      *ast.DocumentNode
	resolver *resolver
}

// NewResolver creates a new [*Resolver] for doc. A nil doc gives a Resolver
// that resolves nothing, as [Path.Node] resolves nothing in a nil document.
func NewResolver(doc *ast.DocumentNode) *Resolver {
	if doc == nil {
		return &Resolver{resolver: &resolver{}}
	}

	return &Resolver{doc: doc, resolver: newResolver(doc)}
}

// Node resolves the node at p in the document of the Resolver, as
// [Path.Node] does, with the same results and errors.
func (r *Resolver) Node(p Path) (ast.Node, error) {
	m, err := p.single(r.resolver, r.doc)
	if err != nil {
		return nil, err
	}

	node, err := r.resolver.deref(m.node)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", p, err)
	}

	// A tree built by hand may hold a nil where the parser always puts a
	// node, and a path that reaches one selects nothing.
	if isNilNode(node) {
		return nil, fmt.Errorf("resolve %s: %w", p, ErrNotFound)
	}

	return node, nil
}
