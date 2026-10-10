package paths

import (
	"errors"
	"fmt"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
)

// Resolver resolves paths in one document, from its root. The root is the
// node a `$` path reads from, and a Resolver has no other node for an `@`
// path to read from, so it reads both from the root.
// [Resolver.NodeFrom] takes the node an `@` path reads from. Every path a
// Resolver returns starts at `$`.
//
// [NewResolver] binds each alias
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
// that resolves nothing, so each of its resolve methods returns an error
// wrapping [ErrNoDocument].
func NewResolver(doc *ast.DocumentNode) *Resolver {
	if doc == nil {
		return &Resolver{resolver: &resolver{}}
	}

	return &Resolver{doc: doc, resolver: newResolver(doc)}
}

// Node resolves the node at p in the document of the Resolver.
//
// The document root is the node a `$` path reads from. A Resolver has no
// other node to start at, so an `@` path reads from the document root
// too, and selects the node the `$` path with its selectors selects.
// [Resolver.NodeFrom] takes the node an `@` path reads from.
//
// It looks through anchors and aliases, so the result is the content the
// path names. It stops at a tag, which decides how that content decodes,
// and keeps any anchor or alias under the tag. The `.name` and `[n]`
// selectors follow aliases to their anchor and see the entries a `<<`
// merge key brings into a mapping.
//
// Returns [ErrWildcard] for a path with a `.*`, `[*]`, or `..` selector,
// which needs [Resolver.Nodes]. Wraps [ErrNotFound] when nothing exists at the
// path, together with [ErrNoDocument] when the document has no content to
// resolve in. Wraps [ErrAlias] when an alias on the path does not resolve,
// including one under a tag, and [ErrExcessiveMerging] when the key lookups
// of a selector read far more nodes under `<<` merge keys than the document
// holds.
func (r *Resolver) Node(p Path) (ast.Node, error) {
	m, err := p.single(r.resolver, r.doc)
	if err != nil {
		return nil, err
	}

	return r.nodeOf(p, m)
}

// NodeFrom resolves the node at p with node as the current node, which
// an `@` path reads from, as [Resolver.Node] resolves p from the root of
// the document. The node belongs to the document of the Resolver, such
// as one that Node or NodeFrom returned. A caller that walks down the
// document can resolve each step from the node above it, rather than a
// longer path from the root each time:
//
//	spec, err := r.Node(paths.Doc().Child("spec"))
//	replicas, err := r.NodeFrom(spec, paths.Current().Child("replicas"))
//
// Where q is the path that resolves to node, NodeFrom gives the node
// that q joined with an `@` path p resolves to, unless p starts with the
// `~` selector from [Path.Key]. A path starts at node as it would at the
// root, so a `~` there selects node itself. A `$` path reads from the
// document root wherever it resolves, so NodeFrom resolves it as
// Resolver.Node does and node plays no part.
//
// Returns the errors [Resolver.Node] returns. Only a `$` path wraps
// [ErrNoDocument], since an `@` path resolves in node rather than in the
// document. A nil node holds nothing to resolve in, so NodeFrom wraps
// [ErrNotFound] for an `@` path and a nil node.
func (r *Resolver) NodeFrom(node ast.Node, p Path) (ast.Node, error) {
	if p.absolute {
		return r.Node(p)
	}

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

// Token resolves the [*token.Token] p points at in the document of the
// Resolver, where an error at the path binds. A scalar is its own token. A mapping or a
// sequence spans many lines, so it points at the token that introduces
// it:
//
//   - The value of a mapping entry points at the key of the entry,
//     whether the value is in block or flow style.
//   - An element of a block sequence points at its "-".
//   - Any other flow mapping or flow sequence points at its own "{" or
//     "[", such as one at the root or an element of a flow sequence.
//   - A block mapping or a block sequence at the root points at its
//     first key or its first element.
//
// Token looks through the anchors and tags on a mapping or a sequence to
// find that token. For a mapping or a sequence under a key, the token
// lies outside the node [Resolver.Node] returns, and a path to the value
// points where the same path ending in the `~` selector from [Path.Key]
// does. A path ending in that selector points at the key of the entry.
// An alias resolves to its own token rather than the anchor's content,
// since that is where the path points in the source.
//
// The path resolves against the document body only, from its root whether
// the path starts at `$` or `@`, so the same path resolves to different
// tokens in different documents of one file. Token returns the same
// errors as [Resolver.Node], except that it does not look through the node
// the last selector reaches. An alias there that does not resolve, such
// as one that names no anchor or one inside the content of its own
// anchor, yields the alias's own token rather than [ErrAlias].
// Token still returns [ErrAlias] for an alias an earlier selector
// resolves through.
func (r *Resolver) Token(p Path) (*token.Token, error) {
	m, err := p.single(r.resolver, r.doc)
	if err != nil {
		return nil, err
	}

	return p.tokenOf(m)
}

// Nearest returns the path of the mapping that lacks a key p names, and
// reports whether the document holds one. It resolves p from the root of
// the document, whether p starts at `$` or `@`, and the path it returns
// starts at `$`. When p selects nothing because the document leaves a key
// out, the longest prefix of p that resolves is where that key belongs.
// Nearest returns that prefix when it resolves to a mapping, or to a
// null, which stands where a mapping would, and every selector of p after
// it is a `.name` selector. An error about a value the document leaves
// out, such as a required field, points there:
//
//	// In a document that holds server and no tls under it.
//	near, ok := r.Nearest(paths.Doc().Child("server", "tls", "cert"))
//	// $.server, true
//
// It reports false for a path that resolves, which misses no key. It
// reports false too for a path that selects nothing for another reason,
// such as an index past the end of a sequence or a name looked up in a
// scalar or a sequence. A `[*]`, `..`, or `~` selector at or after the
// missing key gives false as well, and a `.*` selector anywhere in p
// does. So does an alias that does not resolve, and so does a document
// with no content.
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

		return Doc().extend(p.segments[:k]...), true
	}

	return Path{}, false
}

// Matches resolves every node p selects in the document of the Resolver,
// as [Resolver.Nodes] does, and returns each with the path that selects
// it alone. A caller
// that checks each element of a sequence, each entry of a mapping, or
// each node a `..` selector finds thus reports the one it checked:
//
//	for _, m := range matches {
//		fmt.Println(m.Path) // $.items[0], $.items[1], ...
//	}
//
// A path without `.*`, `[*]`, or `..` selectors yields at most one match,
// whose path is the path as given, at `$`. The path of a node reached
// through an alias is the path as written, not the location of the
// anchor, and the path of an entry a `<<` merge key brings in is the path
// of the mapping that merges it. Returns the errors [Resolver.Nodes] returns.
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

		matches = append(matches, Match{Node: node, Path: Path{segments: m.segs, absolute: true}})
	}

	return matches, nil
}

// Nodes resolves every node p selects in the document of the Resolver,
// in document order, from the document root, whether the path starts at
// `$` or `@`, as [Resolver.Node] does. A path without `.*`, `[*]`, or
// `..` selectors yields at most one node, and an empty result means
// nothing exists at the path.
// Nodes lists one node for each path that selects it, as [Resolver.Matches]
// does. A node that several aliases or `<<` merge keys lead to appears
// once for each, in the place of that alias or merge key. A `..name` or
// `..*` selector lists each node once, even when chained `..` selectors
// reach it more than once. An alias on the path that does not resolve
// returns an error wrapping [ErrAlias].
//
// # Aliases and Tags
//
// Nodes looks through anchors and aliases, so each node is the content
// the path names, and stops at a tag, as [Resolver.Node] does. The `.name`,
// `.*`, `[n]`, and `[*]` selectors follow aliases to their anchor and see
// the entries a `<<` merge key brings into a mapping.
//
// # Wildcard Selectors
//
// The `.*` selector lists the value of each entry that a `.name` selector
// resolves in a mapping, once for each name. It lists an entry a `<<`
// merge key brings in at the place of that merge key, and the entries of
// one merge key in the order its sources first name them. Among entries
// that share a key it lists the one a path through that key selects, so a
// later entry or a later merge wins. It leaves out a merge key, whose
// value is a source of entries, and an entry whose key has no name, as
// [Resolver.KeyName] reports it. On a node that is not a mapping it
// selects nothing, as `[*]` does on a node that is not a sequence.
//
// # Recursive Selectors
//
// The `..name` selector looks through
// an alias or tag on the node it starts from, as the other selectors do.
// Below that node it visits each entry once, where the source defines it.
// It does not follow aliases there, including one a `<<` merge key names,
// and it does not list the entries a merge key brings into a mapping under
// that mapping. It walks a mapping written inline under a `<<` key as it
// walks any other value, and lists its entries under the `<<` selector even
// when a later source or a key of the mapping itself overrides them. When a
// path through `<<` selects a real key with the text `<<`, whether the
// mapping holds it or a merge brings it in, the `..name` selector skips the
// merge key and its inline mapping, since no path through `<<` reaches
// them. It skips an entry that a later entry with the same key shadows,
// whether that entry belongs to its mapping or comes from a later `<<`
// merge key. It also skips an entry whose key has no name, as
// [Resolver.KeyName] reports it, and everything below that entry, since no
// path names them. An alias key with no anchor before it has no name, and
// so does one whose anchor holds a collection.
//
// The `..*` selector visits what the `..name` selector visits. It lists
// the value of every entry it visits, whatever its key, and every element
// of a sequence it visits, each before the nodes below it. It leaves out
// the entry of a `<<` merge key, as `.*` does, and each element of a
// sequence that lists the sources of one, but lists the entries of a
// mapping written inline there.
//
// # Empty Documents
//
// A document with no content, such as an empty one or one of comments
// alone, holds no node for a selector to reach. Nodes returns an empty
// result for a path with selectors there, as it does wherever a path
// selects nothing. The root path selects the null at the "---" header of
// such a document, and nothing when the document has no header.
//
// # Errors
//
// Wraps [ErrNoDocument], together with [ErrNotFound], when the Resolver
// has a nil document, and [ErrAlias] when an alias on the path does not
// resolve, including one under a tag. A `.*` selector reads every `<<`
// merge key of its mapping and of the mappings it merges, so an alias one
// of them names counts as on the path. A `..*` selector lists every value
// below the node it starts from, so an alias it lists counts as on the
// path too. Wraps [ErrExcessiveAliasing] when
// aliases lead a selector to far more nodes than the document holds, and
// [ErrExcessiveMerging] when the key lookups of a selector read far more
// nodes under `<<` merge keys than that. [Resolver.Matches] returns the same
// nodes with the path that selects each one alone.
func (r *Resolver) Nodes(p Path) ([]ast.Node, error) {
	found, err := r.Matches(p)
	if err != nil {
		return nil, err
	}

	nodes := make([]ast.Node, 0, len(found))
	for _, m := range found {
		nodes = append(nodes, m.Node)
	}

	return nodes, nil
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
