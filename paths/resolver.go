package paths

import (
	"fmt"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
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
	if astnode.IsNil(node) {
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
		if astnode.IsNil(node) {
			continue
		}

		matches = append(matches, Match{Node: node, Path: Path{segments: m.segs}})
	}

	return matches, nil
}

// Deref returns the content under node. It looks through the anchors on
// node and follows an alias to the content of the anchor it refers to, as
// [Resolver.Node] does. It stops at a tag on node or on that content, and
// keeps any anchor or alias under the tag, so the result reads as the
// decoder reads node. A caller that walks the document itself follows
// aliases with Deref to reach the node a path through the same alias
// resolves to, and looks through a tag it reaches on the way. Deref gives
// an untyped nil for a nil node and for an anchor or alias whose content
// is nil, including a typed nil a hand-built tree may hold.
//
// Returns an error wrapping [ErrAlias] for an alias that names no anchor
// or that leads back to itself, such as one inside the content of the
// anchor it refers to. Deref checks the aliases under the tag it stops at
// the same way.
func (r *Resolver) Deref(node ast.Node) (ast.Node, error) {
	content, err := r.resolver.deref(node)
	if err != nil {
		return nil, fmt.Errorf("deref: %w", err)
	}

	return content, nil
}

// KeyName returns the text a child selector matches key by, where key is
// the key node of a mapping entry. [Path.Child] with that text selects
// the value of that entry, unless a later entry in the mapping repeats
// the key. A string key gives its unquoted text, a block scalar key gives
// its content, and any other scalar key gives its source text, such as
// 0x10 for an int. An alias key, tagged or not, gives the text of the
// content of its anchor. The `?` of an explicit key, and the anchors and
// tags on key, add nothing to its text.
//
// The bool result is false for a key with no text a selector can match:
// a nil key, a sequence or mapping key, and an alias key with no anchor
// before it or one that leads back to itself. It is true for the quoted
// empty key `""`.
func (r *Resolver) KeyName(key ast.Node) (string, bool) {
	return r.resolver.keyName(key)
}

// Entry returns the entry, an [*ast.MappingValueNode], that [Path.Child]
// with name selects in the mapping at node. Where a `<<` merge key brings
// that entry in, it belongs to the merge source. Entry looks through the
// anchors, tags, and aliases on node. A caller that walks a mapping
// itself compares the result with an entry it reached to learn whether a
// path through name selects that entry. A later entry with the same key
// text can win the selector, such as a later key of the mapping or of a
// later merge source.
//
// Returns an error wrapping [ErrNotFound] when node is not a mapping or
// name selects no entry in it, one wrapping [ErrAlias] when an alias on
// the way does not resolve, and one wrapping [ErrExcessiveMerging] when
// the lookup reads far more nodes under `<<` merge keys than the document
// holds.
func (r *Resolver) Entry(node ast.Node, name string) (ast.Node, error) {
	content, err := r.resolver.unwrap(node)
	if err != nil {
		return nil, fmt.Errorf("entry %q: %w", name, err)
	}

	mapping, ok := content.(*ast.MappingNode)
	if !ok || mapping == nil {
		return nil, fmt.Errorf("entry %q: %w: not a mapping", name, ErrNotFound)
	}

	var reads lookupReads

	entry, _, ok, err := r.resolver.lookup(mapping, name, nil, &reads)
	if err != nil {
		return nil, fmt.Errorf("entry %q: %w", name, err)
	}

	if !ok {
		return nil, fmt.Errorf("entry %q: %w", name, ErrNotFound)
	}

	return entry, nil
}

// Anchor returns the anchor, an [*ast.AnchorNode], that node refers to
// when node is an alias. That is the anchor whose content
// [Resolver.Deref] reaches first when it follows the alias. Where the
// anchor holds another alias, Anchor stops at the anchor, while Deref
// goes on to the content of the anchor the second alias refers to.
// Anchor also returns the anchor of an alias that leads back to itself,
// such as one inside the content of that anchor, which Deref rejects.
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

	anchor, ok := r.resolver.targets[alias]
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

		found, _, err := r.resolver.mergeSources(nil, entry.Value)
		if err != nil {
			return nil, fmt.Errorf("merge sources: %w", err)
		}

		for _, src := range found {
			sources = append(sources, src)
		}
	}

	return sources, nil
}
