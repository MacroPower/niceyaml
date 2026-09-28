package paths

import (
	"fmt"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"
)

// Resolver resolves paths in one document. [NewResolver] binds each alias
// of the document to its anchor once, so resolving many paths in one
// document walks it once rather than once per path. A Resolver also reads
// the keys of a mapping once, the first time a path looks up a key there,
// so resolving every key of a mapping takes time in proportion to its
// size. A Resolver is safe for concurrent use. After a change to the
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

// Token resolves the token that starts the node at p in the document of
// the Resolver, as [Path.Token] does, with the same results and errors.
func (r *Resolver) Token(p Path) (*token.Token, error) {
	m, err := p.single(r.resolver, r.doc)
	if err != nil {
		return nil, err
	}

	return p.tokenOf(m.node)
}

// Matches resolves every node p selects in the document of the Resolver,
// as [Path.Matches] does, with the same results and errors.
func (r *Resolver) Matches(p Path) ([]Match, error) {
	found, err := p.matches(r.resolver, r.doc)
	if err != nil {
		return nil, err
	}

	matches := make([]Match, 0, len(found))

	for _, m := range found {
		node, err := r.resolver.deref(m.node)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", p, err)
		}

		// A tree built by hand may hold a nil where the parser always
		// puts a node, and a nil is nothing to list.
		if isNilNode(node) {
			continue
		}

		matches = append(matches, Match{Node: node, Path: Path{segments: m.segs}})
	}

	return matches, nil
}

// Deref returns the content under node: it looks through the anchors on
// node and follows an alias to the content of the anchor it refers to, as
// [Resolver.Node] does. A tag on that content stays, so the result reads
// as the decoder reads the alias. A caller that walks the document itself
// follows aliases with Deref to reach the node a path through the same
// alias resolves to, and looks through a tag it reaches on the way. A nil
// node gives nil.
//
// Returns an error wrapping [ErrAlias] for an alias that names no anchor
// or that leads back to itself.
func (r *Resolver) Deref(node ast.Node) (ast.Node, error) {
	content, err := r.resolver.deref(node)
	if err != nil {
		return nil, fmt.Errorf("deref: %w", err)
	}

	return content, nil
}

// Anchor returns the anchor, an [*ast.AnchorNode], that node refers to
// when node is an alias. That is the anchor whose content
// [Resolver.Deref] reaches first when it follows the alias. Where the
// anchor holds another alias, Anchor stops at the anchor, while Deref
// goes on to the content of the anchor the second alias refers to.
//
// Returns an error wrapping [ErrAlias] when node is not an alias, and for
// an alias that has no name or names no anchor before it.
func (r *Resolver) Anchor(node ast.Node) (ast.Node, error) {
	alias, ok := node.(*ast.AliasNode)
	if !ok || alias == nil {
		return nil, fmt.Errorf("anchor: %w: not an alias", ErrAlias)
	}

	tk := nodeToken(alias.Value)
	if tk == nil {
		return nil, fmt.Errorf("anchor: %w: alias has no name", ErrAlias)
	}

	target, ok := r.resolver.targets[alias]
	if !ok {
		return nil, fmt.Errorf("anchor: %w: *%s has no anchor before it", ErrAlias, tk.Value)
	}

	anchor, ok := r.resolver.owners[target]
	if !ok {
		return nil, fmt.Errorf("anchor: %w: *%s has no anchor before it", ErrAlias, tk.Value)
	}

	return anchor, nil
}

// MergeSources returns the mappings the `<<` merge keys of the mapping at
// node bring in, in the order the decoder applies them. The merge keys go
// in document order and the sources of one merge key in sequence order,
// so a later source wins over an earlier one for a key both define. It
// looks through the anchors, tags, and aliases on node, on each merge
// value, and on each element of a sequence of sources.
//
// Returns nil when node is not a mapping, and an error wrapping [ErrAlias]
// when an alias on the way does not resolve.
func (r *Resolver) MergeSources(node ast.Node) ([]ast.Node, error) {
	content, err := r.resolver.unwrap(node)
	if err != nil {
		return nil, fmt.Errorf("merge sources: %w", err)
	}

	mapping, ok := content.(*ast.MappingNode)
	if !ok || mapping == nil {
		return nil, nil
	}

	var sources []ast.Node

	for _, entry := range mapping.Values {
		if entry == nil || !isMergeKey(entry.Key) {
			continue
		}

		found, err := r.resolver.mergeSources(entry.Value)
		if err != nil {
			return nil, fmt.Errorf("merge sources: %w", err)
		}

		for _, src := range found {
			sources = append(sources, src)
		}
	}

	return sources, nil
}
