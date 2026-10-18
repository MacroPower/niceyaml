package paths

import (
	"errors"
	"fmt"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
)

// Resolver resolves paths in one document, from its root. The root is the
// node a `$` path reads from. A Resolver does not have another node for an
// `@` path to read from, so it reads both kinds of path from the root.
// [Resolver.NodeFrom] takes the node that an `@` path reads from. Every
// path a Resolver returns starts at `$`.
//
// [NewResolver] binds each alias of the document to its anchor once.
// Resolving many paths in one document thus walks it once rather than
// once per path. A Resolver also reads the keys of a mapping once, the
// first time a path looks up a key there. Resolving every key of a mapping
// thus takes time in proportion to its size. A Resolver is safe for
// concurrent use. After a change to the document, create a new Resolver.
//
// Create instances with [NewResolver].
type Resolver struct {
	doc      *ast.DocumentNode
	resolver *resolver
}

// ResolverOption configures a [Resolver] that [NewResolver] creates.
//
// Available options:
//   - [WithReferenceAnchors]
type ResolverOption func(*resolverConfig)

// resolverConfig holds the settings that a [ResolverOption] configures.
type resolverConfig struct {
	// Returns the value of the anchor of a name that a reference document
	// defines, or nil.
	references func(name string) (ast.Node, bool)
}

// WithReferenceAnchors is a [ResolverOption] that gives the anchors of the
// reference documents of the document, such as those of
// niceyaml.WithReferences. The function anchor returns the value of the
// anchor of a name that those documents define, under the anchor, and
// reports false when they do not define one. An alias reads such an
// anchor only when the document does not define an anchor of its name
// before the alias. A path that ends at such an alias selects the alias,
// as [Resolver.Node] describes. A path through it returns an error that
// wraps [ErrReferenceAlias], as in "alias reads a reference document:
// *base". An alias key that reads such an anchor takes its name from the
// value of the anchor, as [Resolver.KeyName] describes. A nil anchor does
// not change anything.
func WithReferenceAnchors(anchor func(name string) (ast.Node, bool)) ResolverOption {
	return func(c *resolverConfig) {
		c.references = anchor
	}
}

// NewResolver creates a new [*Resolver] for doc with opts. A nil doc gives
// a Resolver without a document to resolve in, so each of its resolve
// methods returns an error wrapping [ErrNoDocument].
func NewResolver(doc *ast.DocumentNode, opts ...ResolverOption) *Resolver {
	var cfg resolverConfig

	for _, opt := range opts {
		opt(&cfg)
	}

	if doc == nil {
		return &Resolver{resolver: &resolver{references: cfg.references}}
	}

	r := newResolver(doc)
	r.references = cfg.references

	return &Resolver{doc: doc, resolver: r}
}

// Node resolves the node at p in the document of the Resolver.
//
// The document root is the node a `$` path reads from. A Resolver does not
// have another node to start at, so an `@` path also reads from the
// document root. Such a path selects the node that the `$` path with the
// same selectors selects. [Resolver.NodeFrom] takes the node that an `@`
// path reads from.
//
// Node looks through anchors and aliases, so the result is the content the
// path names. It stops at a tag, because the tag decides how that content
// decodes, and it keeps any anchor or alias under the tag. The `.name` and
// `[n]` selectors follow aliases to their anchor and see the entries a
// `<<` merge key brings into a mapping.
//
// Under [WithReferenceAnchors], Node also stops at an alias that reads a
// reference document, because the content of its anchor lies in that
// document. A path that ends at such an alias selects the alias, or the
// tag on it, as a path that ends at a tag selects the tag. A selector that
// applies below the alias returns an error that wraps [ErrReferenceAlias].
// Node stops at an alias inside the content of the anchor it refers to in
// the same way, because the decoder reads such an alias as null. A
// selector below it returns an error that wraps [ErrAlias].
//
// Returns [ErrWildcard] for a path with a `.*`, `[*]`, or `..` selector.
// Resolve such a path with [Resolver.Nodes]. Wraps [ErrNotFound] when
// nothing exists at the path, together with [ErrNoDocument] when the
// document lacks content to resolve in. Wraps [ErrAlias] when an alias on
// the path does not resolve, including one under a tag. Wraps
// [ErrExcessiveMerging] when the key lookups of a selector read far more
// nodes under `<<` merge keys than the document holds.
func (r *Resolver) Node(p Path) (ast.Node, error) {
	m, err := p.single(r.resolver, r.doc)
	if err != nil {
		return nil, err
	}

	return r.nodeOf(p, m)
}

// NodeFrom resolves the node at p with node as the current node, as
// [Resolver.Node] resolves p from the root of the document. An `@` path
// reads from the current node. The node belongs to the document of the
// Resolver, such as one that Node or NodeFrom returned. A caller that
// walks down the document can resolve each step from the node above it,
// rather than a longer path from the root each time:
//
//	spec, err := r.Node(paths.Doc().Child("spec"))
//	replicas, err := r.NodeFrom(spec, paths.Current().Child("replicas"))
//
// Where q is the path that resolves to node, NodeFrom gives the node
// that q joined with an `@` path p resolves to, unless p starts with the
// `~` selector from [Path.Key]. A path starts at node as it does at the
// root, so a `~` there selects node itself. A `$` path reads from the
// document root wherever it resolves. NodeFrom thus resolves it as
// Resolver.Node does and ignores node.
//
// NodeFrom selects an alias that reads a reference document as
// Resolver.Node does. A selector that applies below such an alias returns
// an error that wraps [ErrReferenceAlias], including a selector of an `@`
// path that starts at the alias.
//
// Returns the errors [Resolver.Node] returns. An `@` path resolves in node
// rather than in the document, so only the errors for a `$` path wrap
// [ErrNoDocument]. A nil node does not hold anything to resolve in, so
// NodeFrom wraps [ErrNotFound] for an `@` path and a nil node.
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
	node, err := r.resolver.selected(m.node)
	if err != nil {
		return nil, resolveError(p, err)
	}

	// A tree built by hand may hold a nil where the parser always puts a
	// node, and a path that reaches one selects nothing.
	if astnode.IsNil(node) {
		return nil, fmt.Errorf("resolve %s: %w", p, ErrNotFound)
	}

	return node, nil
}

// Token resolves the [*token.Token] that p points at in the document of
// the Resolver. An error at the path binds to that token. A scalar is its
// own token. A mapping or a sequence spans many lines, so it points at
// the token that introduces it:
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
// lies outside the node that [Resolver.Node] returns. A path to such a
// value points where the same path ending in the `~` selector from
// [Path.Key] does. A path ending in that selector points at the key of
// the entry. An alias resolves to its own token rather than to the
// content of its anchor, because the alias is where the path points in
// the source.
//
// The path resolves only against the document body, from its root,
// whether the path starts at `$` or `@`. The same path thus resolves to
// different tokens in different documents of one file. Token returns the
// same errors as [Resolver.Node], except that it does not look through
// the node the last selector reaches. An alias there that does not
// resolve yields the alias's own token rather than [ErrAlias]. Such an
// alias might not name an anchor, or it might sit inside the content of
// its own anchor. Token still returns [ErrAlias] for an alias that an
// earlier selector resolves through.
func (r *Resolver) Token(p Path) (*token.Token, error) {
	m, err := p.single(r.resolver, r.doc)
	if err != nil {
		return nil, err
	}

	return p.tokenOf(m)
}

// Nearest returns the path of the mapping that lacks a key p names, and
// reports whether the document holds such a mapping. It resolves p from
// the root of the document, whether p starts at `$` or `@`, and the path
// it returns starts at `$`.
//
// When the document leaves out a key that p names, p does not select
// anything. The longest prefix of p that resolves is then where that key
// belongs. Nearest returns that prefix when every selector of p after it
// is a `.name` selector and the prefix resolves to a mapping or to a null.
// Such a null stands in the place of a mapping. An error about a value
// the document leaves out, such as a required field, points there:
//
//	// In a document that holds server and no tls under it.
//	near, ok := r.Nearest(paths.Doc().Child("server", "tls", "cert"))
//	// $.server, true
//
// It reports false for a path that resolves, because such a path does
// not miss a key. It also reports false when p fails to select a node for
// another reason. An index past the end of a sequence is one such reason,
// and a name looked up in a scalar or a sequence is another. A `.*`,
// `[*]`, or `..` selector anywhere in p gives false, and so does a `~`
// selector at or after the missing key. An alias that does not resolve
// also gives false, and so does a document without content.
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

// Matches resolves every node that p selects in the document of the
// Resolver, as [Resolver.Nodes] does. It returns each node with the path
// that selects it alone. A caller that checks each element of a sequence,
// each entry of a mapping, or each node a `..` selector finds can thus
// report the one it checked:
//
//	for _, m := range matches {
//		fmt.Println(m.Path) // $.items[0], $.items[1], ...
//	}
//
// A path without `.*`, `[*]`, or `..` selectors yields at most one match.
// The path of that match is the path as given, starting at `$`. The path
// of a node reached through an alias is the path as written, not the
// location of the anchor. The path of an entry that a `<<` merge key
// brings in is the path of the mapping that merges it. Returns the errors
// [Resolver.Nodes] returns.
//
// Each node is the one that [Resolver.Node] gives for the path of its
// match. A match at an alias that reads a reference document thus holds
// the alias, or the tag on it, rather than the content of the anchor.
func (r *Resolver) Matches(p Path) ([]Match, error) {
	found, err := p.matches(r.resolver, r.doc)
	if err != nil {
		return nil, err
	}

	return r.matchesOf(p, found, true)
}

// MatchesFrom resolves every node that p selects with node as the current
// node, as [Resolver.NodeFrom] resolves one node. An `@` path reads from
// node, and the path of each match is the `@` path that selects its node
// alone from node. A caller that holds a node, rather than a path to it,
// can thus list what a wildcard selects below it:
//
//	matches, err := r.MatchesFrom(spec, paths.Current().Child("containers").IndexAll())
//	// @.containers[0], @.containers[1], ...
//
// A `$` path reads from the document root wherever it resolves, so
// MatchesFrom resolves it as [Resolver.Matches] does and ignores node. An
// `@` path resolves in node rather than in the document, so a nil node
// yields no match. Returns the errors that [Resolver.Matches] returns,
// other than those that wrap [ErrNoDocument] for an `@` path.
func (r *Resolver) MatchesFrom(node ast.Node, p Path) ([]Match, error) {
	if p.absolute {
		return r.Matches(p)
	}

	found, err := r.resolver.resolve(node, p.segments)
	if err != nil {
		return nil, resolveError(p, err)
	}

	return r.matchesOf(p, found, false)
}

// matchesOf returns the [Match] of each of found, the matches of p, as
// [Resolver.Matches] gives them. The path of each starts at `$` when
// absolute is set, and at `@` otherwise.
func (r *Resolver) matchesOf(p Path, found []match, absolute bool) ([]Match, error) {
	matches := make([]Match, 0, len(found))

	for _, m := range found {
		node, err := r.resolver.selected(m.node)
		if err != nil {
			return nil, resolveError(p, err)
		}

		// A tree built by hand might hold a nil where the parser always
		// puts a node, and a nil is nothing to list.
		if astnode.IsNil(node) {
			continue
		}

		matches = append(matches, Match{Node: node, Path: Path{segments: m.segs, absolute: absolute}})
	}

	return matches, nil
}

// Nodes resolves every node that p selects in the document of the
// Resolver, in document order. As [Resolver.Node] does, it resolves from
// the document root whether the path starts at `$` or `@`. A path without
// `.*`, `[*]`, or `..` selectors yields at most one node, and an empty
// result means nothing exists at the path. [Resolver.Matches] returns the
// same nodes with the path that selects each one alone.
//
// Nodes lists one node for each path that selects it, as
// [Resolver.Matches] does. A node that several aliases or `<<` merge keys
// lead to appears once for each, in the place of that alias or merge key.
// A `..name` or `..*` selector lists each node once, even when chained
// `..` selectors reach it more than once.
//
// # Aliases and Tags
//
// As [Resolver.Node] does, Nodes looks through anchors and aliases, so
// each node is the content the path names. It stops at a tag. The
// `.name`, `.*`, `[n]`, and `[*]` selectors follow aliases to their anchor
// and see the entries a `<<` merge key brings into a mapping.
//
// Nodes also stops at an alias that reads a reference document, as
// Resolver.Node does. A `[*]` on a sequence thus lists such an element as
// the alias, or as the tag on it, beside the elements that the document
// holds. The decoder reads an alias inside the content of its own anchor
// as null, so a `.*`, `[*]`, or `..` selector selects nothing at such an
// alias, as on any other null.
//
// # Wildcard Selectors
//
// The `.*` selector lists the value of each entry that a `.name` selector
// resolves in a mapping, once for each name. It lists an entry that a
// `<<` merge key brings in at the place of that merge key. It lists the
// entries of one merge key in the order its sources first name them.
// Among entries that share a key, it lists the one that a path through
// that key selects, so a later entry or a later merge wins. It leaves out
// a merge key, whose value is a source of entries. It also leaves out an
// entry whose key does not have a name, as [Resolver.KeyName] reports it.
// Like `[*]` on a node that is not a sequence, it does not select anything
// on a node that is not a mapping.
//
// # Recursive Selectors
//
// The `..name` selector looks through an alias or tag on the node it
// starts from, as the other selectors do. Below that node, it visits each
// entry once, where the source defines it. It does not follow aliases
// there, including one that a `<<` merge key names. It does not list the
// entries that a merge key brings into a mapping under that mapping. A
// tag that converts a collection, such as `!!str`, holds a scalar, as the
// package documentation describes, so the selector visits nothing below
// such a tag, on the node it starts from or below it.
//
// The `..name` selector walks a mapping written inline under a `<<` key as
// it walks any other value. It lists the entries of that mapping under the
// `<<` selector, even when a later source or a key of the mapping itself
// overrides them. A path through `<<` can select a real key with the text
// `<<`. The `..name` selector then skips the merge key and its inline
// mapping, because no path through `<<` reaches them. That real key can
// belong to the mapping that holds the merge key, or a merge can bring it
// in.
//
// The `..name` selector skips an entry that a later entry with the same
// key shadows, whether the later entry belongs to its mapping or comes
// from a later `<<` merge key. It also skips an entry whose key does not
// have a name, as [Resolver.KeyName] reports it, and everything below that
// entry, because no path names them. An alias key that reads no anchor
// does not have a name, and neither does one whose anchor holds a
// collection.
//
// The `..*` selector visits what the `..name` selector visits. It lists
// the value of every entry it visits, whatever its key, and every element
// of a sequence it visits, each before the nodes below it. It leaves out
// the entry of a `<<` merge key, as `.*` does, and each element of a
// sequence that lists the sources of one. It still lists the entries of a
// mapping written inline under a merge key.
//
// # Empty Documents
//
// A document without content, such as an empty one or one of comments
// alone, does not hold a node for a selector to reach. The root path
// selects the null at the "---" header of such a document, and so does a
// path of only `~` selectors. When the document lacks a header, those
// paths do not select anything. Nodes returns an empty result there for a
// path with any other selector, as it does wherever a path does not
// select anything.
//
// # Errors
//
// Wraps [ErrNoDocument], together with [ErrNotFound], when the Resolver
// has a nil document. Wraps [ErrAlias] when an alias on the path does not
// resolve, including one under a tag. A `.*` selector reads every `<<`
// merge key of its mapping and of the mappings it merges, so an alias that
// one of them names counts as on the path. A `..*` selector lists every
// value below the node it starts from, so an alias it lists also counts as
// on the path, unless the alias reads a reference document. The error
// names the line and the column of the alias after the path, as
// [Resolver.Node] names them.
//
// Wraps [ErrExcessiveAliasing] when aliases lead a selector to far more
// nodes than the document holds. Wraps [ErrExcessiveMerging] when the key
// lookups of a selector read far more nodes under `<<` merge keys than
// the document holds.
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
// keeps any anchor or alias under the tag. The result thus reads as the
// decoder reads node. A caller that walks the document itself can follow
// aliases with Deref to reach the node that a path through the same alias
// resolves to. That caller should look through each tag it reaches on the
// way. Deref gives an untyped nil for a nil node and for an anchor or
// alias whose content is nil, including a typed nil that a hand-built
// tree might hold.
//
// Returns an error wrapping [ErrAlias] for an alias that does not name an
// anchor or that leads back to itself, such as one inside the content of
// the anchor it refers to. Deref checks the aliases under the tag it stops
// at the same way.
//
// Deref returns an error that wraps [ErrReferenceAlias] for an alias that
// reads a reference document, as [WithReferenceAnchors] names those,
// because the Resolver does not hold the content of its anchor. That alias
// is the node that [Resolver.Node] returns for a path that ends at it, so
// Deref of that node fails, and so does Deref of the tag on it.
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
// key, tagged or not, gives the text of the content of its anchor. That
// anchor can lie in a reference document of [WithReferenceAnchors], as it
// does for the decoder. The `?` of an explicit key and the anchors and
// tags on key do not change its text.
//
// The bool result is false for a key that does not have text a selector
// can match. Such a key is nil, a sequence, a mapping, or an alias key
// that leads back to itself or reads no anchor. The result is true for
// the quoted empty key `""`.
func (r *Resolver) KeyName(key ast.Node) (string, bool) {
	return r.resolver.keyName(key)
}

// Entry returns the entry, an [*ast.MappingValueNode], that [Path.Child]
// with name selects in the mapping at node. Where a `<<` merge key brings
// that entry in, it belongs to the merge source. Entry looks through the
// anchors, tags, and aliases on node. A caller that walks a mapping
// itself can compare the result with an entry it reached to learn whether
// a path through name selects that entry. A later entry with the same key
// text can win the selector. That entry can be a later key of the mapping
// or of a later merge source.
//
// Returns an error wrapping [ErrNotFound] when node is not a mapping or
// name does not select an entry in it. Returns one wrapping [ErrAlias]
// when an alias on the way does not resolve. Returns one wrapping
// [ErrExcessiveMerging] when the lookup reads far more nodes under `<<`
// merge keys than the document holds. Each call weighs its own reads
// against that limit, but the calls of one [EntryFinder] weigh theirs
// together.
func (r *Resolver) Entry(node ast.Node, name string) (ast.Node, error) {
	return r.EntryFinder().Entry(node, name)
}

// EntryFinder finds entries in the mappings of one document, as
// [Resolver.Entry] does, and weighs the reads of all its lookups together
// against the limit behind [ErrExcessiveMerging]. A lookup of a key with
// a `<<` merge key after it reads the sources of that merge key. Looking
// up each key of such a mapping thus reads those sources once for each
// key. A caller that looks up many entries should use one EntryFinder for
// them. Those reads then stop at the limit rather than grow with the
// square of the size of the document. An EntryFinder is not safe for
// concurrent use.
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
// takes the reads of the EntryFinder past the limit. It also returns that
// error for every later lookup that reads a merge key. A later lookup
// that does not read a merge key still returns its entry, such as a
// lookup of a key without a merge key after it.
func (f *EntryFinder) Entry(node ast.Node, name string) (ast.Node, error) {
	content, err := f.resolver.selectable(node)
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
// when node is an alias. The result is the anchor whose content
// [Resolver.Deref] reaches first when it follows the alias. Where the
// anchor holds another alias, Anchor stops at the anchor, but Deref
// continues to the content of the anchor that the second alias refers
// to. Anchor also returns the anchor of an alias that leads back to
// itself, such as one inside the content of that anchor. Deref rejects
// such an alias.
//
// Returns an error wrapping [ErrAlias] when node is not an alias, and for
// an alias that does not have a name or does not name an anchor before
// it. The error of an alias whose anchor a reference document defines
// wraps [ErrReferenceAlias] too, as [WithReferenceAnchors] describes.
func (r *Resolver) Anchor(node ast.Node) (ast.Node, error) {
	alias, ok := node.(*ast.AliasNode)
	if !ok || alias == nil {
		return nil, fmt.Errorf("anchor: %w: not an alias", ErrAlias)
	}

	tk := astnode.Token(alias.Value)
	if tk == nil {
		return nil, fmt.Errorf("anchor: %w: alias has no name", ErrAlias)
	}

	anchor, ok := r.resolver.targets[alias]
	if !ok {
		return nil, fmt.Errorf("anchor: %w", r.resolver.missingAnchor(tk.Value))
	}

	return anchor, nil
}

// MergeSources returns the mappings the `<<` merge keys of the mapping at
// node bring in, in the order the decoder applies them. The merge keys go
// in document order, and the sources of one merge key go in sequence
// order. A later source thus wins over an earlier one for a key both
// define. MergeSources looks through the anchors, tags, and aliases on
// node, on each merge value, and on each element of a sequence of
// sources.
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
		if entry == nil || !astnode.IsMergeKey(entry.Key) {
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
