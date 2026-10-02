package paths

import (
	"errors"
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

	return r.nodeOf(p, m)
}

// NodeFrom resolves the node at p from node, as [Resolver.Node] resolves
// p from the root of the document. The node belongs to the document of
// the Resolver, such as one that Node or NodeFrom returned. A caller that
// walks down the document can resolve each step from the node above it,
// rather than a longer path from the root each time:
//
//	spec, err := r.Node(paths.Root().Child("spec"))
//	replicas, err := r.NodeFrom(spec, paths.Root().Child("replicas"))
//
// Where q is the path that resolves to node, NodeFrom gives the node
// that q joined with p resolves to, unless p starts with the `~` selector
// from [Path.Key]. A path starts at node as it would at the root, so a
// `~` there selects node itself.
//
// Returns the errors [Resolver.Node] returns, other than
// [ErrNoDocument]. A nil node holds nothing to resolve in, so NodeFrom
// wraps [ErrNotFound] for it.
func (r *Resolver) NodeFrom(node ast.Node, p Path) (ast.Node, error) {
	m, err := p.singleFrom(r.resolver, node)
	if err != nil {
		return nil, err
	}

	return r.nodeOf(p, m)
}

// nodeOf returns the content of the node of m, a match of p, as
// [Resolver.Node] gives it.
func (r *Resolver) nodeOf(p Path, m match) (ast.Node, error) {
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

// Nearest returns the path of the mapping that lacks a key p names, and
// reports whether the document holds one. When p selects nothing because
// the document leaves a key out, the longest prefix of p that resolves is
// where that key belongs. Nearest returns that prefix when it resolves to
// a mapping, or to a null, which stands where a mapping would, and every
// selector of p after it is a `.name` selector. An error about a value
// the document leaves out, such as a required field, points there:
//
//	// In a document that holds server and no tls under it.
//	near, ok := r.Nearest(paths.Root().Child("server", "tls", "cert"))
//	// $.server, true
//
// It reports false for a path that resolves, which misses no key. It
// reports false too for a path that selects nothing for another reason,
// such as an index past the end of a sequence or a name looked up in a
// scalar or a sequence. A `[*]`, `..`, or `~` selector at or after the
// missing key gives false as well. So does an alias that does not
// resolve, and so does a document with no content.
func (r *Resolver) Nearest(p Path) (Path, bool) {
	if p.wildcard() || r.doc == nil || !astnode.HasContent(r.doc.Body) {
		return Path{}, false
	}

	// A prefix resolves whenever a longer one does, so the first prefix
	// that resolves, from the longest down, is the longest.
	for k := len(p.segments); k >= 0; k-- {
		m, err := Path{segments: p.segments[:k]}.single(r.resolver, r.doc)
		if errors.Is(err, ErrNotFound) {
			continue
		}

		if err != nil || k == len(p.segments) {
			return Path{}, false
		}

		for _, seg := range p.segments[k:] {
			if seg.kind != segmentChild {
				return Path{}, false
			}
		}

		content, err := r.resolver.unwrap(m.node)
		if err != nil || astnode.IsNil(content) {
			return Path{}, false
		}

		if _, ok := content.(*ast.MappingNode); !ok && content.Type() != ast.NullType {
			return Path{}, false
		}

		return Root().extend(p.segments[:k]...), true
	}

	return Path{}, false
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
// the key or a later `<<` merge key brings it in. A string key gives its
// unquoted text, a block scalar key gives its content, and any other
// scalar key gives its source text, such as 0x10 for an int. An alias
// key, tagged or not, gives the text of the content of its anchor. The
// `?` of an explicit key, and the anchors and tags on key, add nothing
// to its text.
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
// name selects no entry in it, and one wrapping [ErrAlias] when an alias on
// the way does not resolve. Returns one wrapping [ErrExcessiveMerging] when
// the lookup reads far more nodes under `<<` merge keys than the document
// holds. Each call weighs its own reads against that limit, where the
// calls of one [EntryFinder] weigh theirs together.
func (r *Resolver) Entry(node ast.Node, name string) (ast.Node, error) {
	return r.EntryFinder().Entry(node, name)
}

// EntryFinder finds entries in the mappings of one document, as
// [Resolver.Entry] does, and weighs the reads of all its lookups together
// against the limit behind [ErrExcessiveMerging]. A lookup of a key with
// a `<<` merge key after it reads the sources of that merge key, so
// looking up each key of such a mapping reads those sources once for
// each key. A caller that looks up many entries uses one EntryFinder for
// them, so those reads stop at the limit rather than grow with the
// square of the document. An EntryFinder is not safe for concurrent use.
//
// Create instances with [Resolver.EntryFinder].
type EntryFinder struct {
	resolver *resolver
	reads    lookupReads
}

// EntryFinder creates a new [*EntryFinder] for the document of the
// Resolver.
func (r *Resolver) EntryFinder() *EntryFinder {
	return &EntryFinder{resolver: r.resolver}
}

// Entry returns the entry that [Path.Child] with name selects in the
// mapping at node, as [Resolver.Entry] does, with the same results and
// errors.
//
// It returns the error wrapping [ErrExcessiveMerging] for the lookup that
// takes the reads of the EntryFinder past the limit, and for every later
// lookup that reads a merge key. A later lookup that reads no merge key
// still returns its entry, such as one of a key with no merge key after
// it.
func (f *EntryFinder) Entry(node ast.Node, name string) (ast.Node, error) {
	content, err := f.resolver.unwrap(node)
	if err != nil {
		return nil, fmt.Errorf("entry %q: %w", name, err)
	}

	mapping, ok := content.(*ast.MappingNode)
	if !ok || mapping == nil {
		return nil, fmt.Errorf("entry %q: %w: not a mapping", name, ErrNotFound)
	}

	entry, _, ok, err := f.resolver.lookup(mapping, name, nil, &f.reads)
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
