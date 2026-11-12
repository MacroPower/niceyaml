package paths

import (
	"errors"
	"fmt"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/position"
)

// AliasError is the error of an alias that does not resolve, as the
// lookups of a [Resolver] find one. It wraps [ErrAlias] and the reason.
// The reason is [ErrAliasCycle] for an alias that leads back to itself
// and [ErrAnchorMissing] for one without an anchor of its name before it.
// It is [ErrReferenceAlias] for an alias whose anchor a reference
// document defines. An alias without a name, which only a hand-built
// tree holds, wraps ErrAlias alone. Find the AliasError in an error with
// [errors.As], and read the alias from [AliasError.Alias] or its position
// from [AliasError.Position].
type AliasError struct {
	// Alias is the alias that does not resolve.
	Alias *ast.AliasNode
	// Cause says why the alias does not resolve. It wraps [ErrAlias]. A
	// nil Cause stands for ErrAlias itself.
	Cause error
	// Path is the path whose resolve read the alias, for the error of a
	// resolve such as [Resolver.Node]. A call without a path, such as
	// [Resolver.Deref], leaves it at the zero value.
	Path Path
	// Whether a resolve of Path made the error. The zero Path is also the
	// `@` path, so Path alone does not tell a resolve of `@` from a call
	// without a path.
	resolved bool
}

// Error names Path in front of Cause, as in "resolve $.a.b: alias does
// not resolve: *x forms a cycle". The error of a resolve of the `@` path
// reads as in "resolve @: alias does not resolve: *x forms a cycle". An
// AliasError of a call without a path, and one that a program builds
// with a zero Path, reads as Cause alone.
func (e *AliasError) Error() string {
	cause := e.Unwrap().Error()

	if !e.resolved && e.Path.Equal(Path{}) {
		return cause
	}

	return "resolve " + e.Path.String() + ": " + cause
}

// Unwrap returns Cause, or [ErrAlias] for a nil Cause.
func (e *AliasError) Unwrap() error {
	if e.Cause == nil {
		return ErrAlias
	}

	return e.Cause
}

// Position returns the position of Alias in the document. It reports
// false for a nil Alias and for an alias without a token or a position,
// such as one of a hand-built tree.
func (e *AliasError) Position() (position.Position, bool) {
	if e.Alias == nil {
		return position.Position{}, false
	}

	tk := e.Alias.GetToken()
	if tk == nil || tk.Position == nil {
		return position.Position{}, false
	}

	return position.NewFromToken(tk), true
}

// Resolver resolves paths in one document, from its root. The root is the
// node a `$` path reads from. An `@` path reads from the current node, and
// only [Resolver.NodeFrom] and [Resolver.MatchesFrom] take one. The other
// methods that resolve a path return an error wrapping [ErrNoCurrentNode]
// for an `@` path, and [Resolver.Nearest] reports false for one. To read
// an `@` path from the root, join [Doc] with it, as in
// paths.Doc().Join(p). The join leaves a `$` path as it is. The paths that
// [Resolver.Matches] and [Resolver.Nearest] return start at `$`.
//
// A Resolver binds each alias of the document to its anchor once, when a
// path first needs the binding, as at an alias. Resolving many paths in one
// document thus walks it once at most, rather than once per path. A
// Resolver also reads the keys of a mapping once, the first time a path
// looks up a key there. Resolving every key of a mapping thus takes time in
// proportion to its size. A Resolver is safe for concurrent use. After a
// change to the document, create a new Resolver.
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
// reports false when they do not define one. The value is a node of the
// reference document, which the Resolver reads as it reads the document.
// An alias of that document reads the anchor that the function returns for
// its name.
//
// An alias reads such an anchor only when the document does not define an
// anchor of its name before the alias. A path that ends at such an alias
// selects the alias, as [Resolver.Node] describes. A path through it
// returns an error that wraps [ErrReferenceAlias], as in "alias reads a
// reference document: *base". An alias key that reads such an anchor takes
// its name from the value of the anchor, as [Resolver.KeyName] describes.
// A `<<` merge key that reads such an anchor brings in the keys of its
// mapping, as the package documentation describes. A path through a key
// whose entry the merge key brings in returns that error too, and so does
// a `.*`, `..name`, or `..*` selector that reaches such a key. A nil
// anchor does not change anything.
func WithReferenceAnchors(anchor func(name string) (ast.Node, bool)) ResolverOption {
	return func(c *resolverConfig) {
		c.references = anchor
	}
}

// NewResolver creates a new [*Resolver] for doc with opts. A nil doc gives
// a Resolver without a document to resolve in. Each of its resolve methods
// thus returns an error wrapping [ErrNoDocument] for a `$` path.
func NewResolver(doc *ast.DocumentNode, opts ...ResolverOption) *Resolver {
	var cfg resolverConfig

	for _, opt := range opts {
		opt(&cfg)
	}

	if doc == nil {
		return &Resolver{resolver: &resolver{binding: noBinding, references: cfg.references}}
	}

	r := newResolver(doc)
	r.references = cfg.references

	return &Resolver{doc: doc, resolver: r}
}

// Node resolves the node at the `$` path p in the document of the
// Resolver.
//
// The document root is the node a `$` path reads from. Node does not have
// a current node for an `@` path to read from, so it returns an error
// wrapping [ErrNoCurrentNode] for one. [Resolver.NodeFrom] takes the node
// that an `@` path reads from.
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
// So does a `.name` selector whose entry a `<<` merge key brings in from
// such a document. A `.name` selector of a key that the merge key does not
// bring in selects the entry of the document, or nothing.
// Node stops at an alias inside the content of the anchor it refers to in
// the same way, because the decoder reads such an alias as null. A
// selector below it returns an error that wraps [ErrAlias].
//
// The `~` selector from [Path.Key] selects the key of an entry. A sequence
// element and the root do not have a key, so Node wraps [ErrNotFound] for
// a path with a `~` after either. [Resolver.Token] still points at the
// element or the root for a path that ends in such a `~`.
//
// Wraps [ErrNoCurrentNode] for an `@` path. Returns [ErrWildcard] for a
// path with a `.*`, `[*]`, or `..` selector. Resolve such a path with
// [Resolver.Nodes]. Wraps [ErrNotFound] when nothing exists at the path,
// together with [ErrNoDocument] when the document lacks content to resolve
// in. Wraps [ErrAlias] when an alias on the path does not resolve,
// including one under a tag. That error is an [*AliasError], which carries
// the alias and the path. Wraps [ErrExcessiveMerging] when the key lookups
// of a selector read far more nodes under `<<` merge keys than the document
// holds.
func (r *Resolver) Node(p Path) (ast.Node, error) {
	m, err := p.single(r.resolver, r.doc)
	if err != nil {
		return nil, err
	}

	// Only the keyless match at the header of a document without content
	// comes from such a document.
	if m.keyless && !astnode.HasContent(r.doc.Body) {
		return nil, fmt.Errorf("resolve %s: %w: %w", p, ErrNotFound, ErrNoDocument)
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
// `~` selector from [Path.Key]. NodeFrom gets node without the entry that
// holds it, so a `~` there does not find a key, as at the root, and
// NodeFrom wraps [ErrNotFound]. Resolve q joined with p from the root to
// select the key of node. A `$` path reads from the document root wherever
// it resolves. NodeFrom thus resolves it as Resolver.Node does and ignores
// node.
//
// NodeFrom selects an alias that reads a reference document as
// Resolver.Node does. A selector that applies below such an alias returns
// an error that wraps [ErrReferenceAlias], including a selector of an `@`
// path that starts at the alias.
//
// Returns the errors [Resolver.Node] returns, other than
// [ErrNoCurrentNode]. An `@` path resolves in node rather than in the
// document, so only the errors for a `$` path wrap [ErrNoDocument]. A nil
// node does not hold anything to resolve in, so NodeFrom wraps
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
// [Resolver.Node] gives it. A keyless match does not select a node, so
// nodeOf returns an error wrapping [ErrNotFound] for one.
func (r *Resolver) nodeOf(p Path, m match) (ast.Node, error) {
	if m.keyless {
		return nil, fmt.Errorf("resolve %s: %w", p, ErrNotFound)
	}

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
// own token, and a value that holds a tag alone, as in "a: !!null", points
// at its tag. A mapping or a sequence spans many lines, so it points at
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
// A sequence element and the root do not have a key. A path that ends in
// the `~` selector after either thus points where the path before the
// selector does. [Resolver.Node] wraps [ErrNotFound] for such a path, but
// an error at it still binds to a token.
//
// The path resolves only against the document body, from its root. The
// same path thus resolves to different tokens in different documents of
// one file. Token does not have a current node for an `@` path to read
// from, so it returns an error wrapping [ErrNoCurrentNode] for one. To
// find the token of an `@` path below a node, join the `$` path of that
// node with it, as [Path.Join] gives it.
//
// Token returns the same errors as [Resolver.Node], except for a path
// that ends in a `~` without a key to select. It also does not look
// through the node the last selector reaches. An alias there that does
// not resolve yields the alias's own token rather than [ErrAlias]. Such
// an alias might not name an anchor, or it might sit inside the content
// of its own anchor. Token still returns [ErrAlias] for an alias that an
// earlier selector resolves through.
func (r *Resolver) Token(p Path) (*token.Token, error) {
	m, err := p.single(r.resolver, r.doc)
	if err != nil {
		return nil, err
	}

	return p.tokenOf(m)
}

// Nearest returns the path of the nearest node the document holds on the
// way to p, and reports whether p misses a node below that node. It
// resolves the `$` path p from the root of the document, and the path it
// returns starts at `$`. Nearest does not have a current node for an `@`
// path to read from, so it reports false for one.
//
// When the document does not hold a node that p names, p does not select
// anything. Nearest then returns the longest prefix of p that resolves,
// whatever selector after it misses. That prefix can hold a mapping that
// lacks a key, a sequence too short for an index, or a scalar, which does
// not hold names. An error about a value the document does not hold,
// such as a required field, points there:
//
//	// In a document that holds server and no tls under it.
//	near, ok := r.Nearest(paths.Doc().Child("server", "tls", "cert"))
//	// $.server, true
//
// A caller that inserts the missing value at the prefix must check the
// kind of the node there. Only a mapping or a null takes a key, and only
// a sequence takes an index.
//
// A prefix that ends in a `~` selector after a sequence element or the
// root does not resolve, because neither has a key. Nearest goes on to
// the shorter prefix before that `~`. That `~` does not read the element
// or the root, so the prefix before it can be an alias that does not
// resolve. Nearest returns such a prefix, and [Resolver.Token] gives the
// token of its alias.
//
// It reports false for a path that [Resolver.Token] resolves. Such a path
// includes one that ends in a `~` after an element or the root, because
// an error at that path binds at the element or the root itself. A `.*`,
// `[*]`, or `..` selector anywhere in p gives false. A path that enters
// an alias that does not resolve also gives false, and so does a
// document without content.
func (r *Resolver) Nearest(p Path) (Path, bool) {
	if !p.absolute || p.HasWildcard() || r.doc == nil || !astnode.HasContent(r.doc.Body) {
		return Path{}, false
	}

	// A prefix resolves whenever a longer one does, so the first prefix
	// that resolves, from the longest down, is the longest.
	for k := len(p.segments); k >= 0; k-- {
		m, err := Path{segments: p.segments[:k], absolute: true}.single(r.resolver, r.doc)
		if errors.Is(err, ErrNotFound) {
			continue
		}

		if err != nil || k == len(p.segments) {
			return Path{}, false
		}

		// A `~` that does not find a key does not select a node.
		if m.keyless {
			continue
		}

		return Doc().extend(p.segments[:k]...), true
	}

	return Path{}, false
}

// Matches resolves every node that p selects in the document of the
// Resolver, as [Resolver.Nodes] does. It returns each node with the path
// that selects it alone and with the entry or the sequence that holds it,
// as [Match] describes. A caller that checks each element of a sequence,
// each entry of a mapping, or each node a `..` selector finds can thus
// report the one it checked:
//
//	for _, m := range matches {
//		fmt.Println(m.Path) // $.items[0], $.items[1], ...
//	}
//
// A path without `.*`, `[*]`, or `..` selectors yields at most one match.
// The path of that match is the path as given. The path of a node reached
// through an alias is the path as written, not the location of the anchor.
// The path of an entry that a `<<` merge key brings in is the path of the
// mapping that merges it. Returns the errors [Resolver.Nodes] returns.
//
// Each node is the one that [Resolver.Node] gives for the path of its
// match. A match at an alias that reads a reference document thus holds
// the alias, or the tag on it, rather than the content of the anchor.
//
// A `~` selects only the key of an entry. A path such as `$..*~` thus
// yields a match for the key of each entry that `..*` selects and none for
// a sequence element.
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
// yields no match. MatchesFrom gets node without the entry that holds it,
// so an `@` path that starts with a `~` yields no match either. Returns
// the errors that [Resolver.Matches] returns, other than those that wrap
// [ErrNoCurrentNode] or, for an `@` path, [ErrNoDocument].
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
// [Resolver.Matches] gives them. It leaves out a keyless match, which
// does not select a node. The path of each starts at `$` when absolute is
// set, and at `@` otherwise.
func (r *Resolver) matchesOf(p Path, found []match, absolute bool) ([]Match, error) {
	matches := make([]Match, 0, len(found))

	for _, m := range found {
		if m.keyless {
			continue
		}

		node, err := r.resolver.selected(m.node)
		if err != nil {
			return nil, resolveError(p, err)
		}

		// A tree built by hand might hold a nil where the parser always
		// puts a node, and a nil is nothing to list.
		if astnode.IsNil(node) {
			continue
		}

		matches = append(matches, Match{
			Node:     node,
			Path:     Path{segments: m.segs, absolute: absolute},
			Entry:    m.entry,
			Sequence: m.seq,
			Index:    m.index,
			written:  m.node,
		})
	}

	return matches, nil
}

// Nodes resolves every node that p selects in the document of the Resolver,
// in document order. As [Resolver.Node] does, it resolves a `$` path from
// the document root and returns an error wrapping [ErrNoCurrentNode] for an
// `@` path. A path without `.*`, `[*]`, or `..` selectors yields at most
// one node, and an empty result means nothing exists at the path.
// [Resolver.Matches] returns the same nodes with the path that selects each
// one alone.
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
// # Key Selectors
//
// The `~` selector from [Path.Key] lists the key of each entry that the
// path before it selects. A sequence element and the root do not have a
// key, so the `~` lists nothing for them. After `..*`, `..name`, or `[*]`,
// it thus lists the keys of the entries among the matches and leaves out
// the elements:
//
//	keys, err := r.Nodes(paths.Doc().RecursiveAll().Key()) // $..*~
//
// The `~` lists a key only for an entry that the path before it selects.
// The `.*` and `..*` selectors leave out the entry of a `<<` merge key, so
// `$..*~` does not list a `<<` key. A path that names the merge key, such
// as `$.base.<<~`, selects it.
//
// # Empty Documents
//
// A document without content, such as an empty one or one of comments
// alone, does not hold a node for a selector to reach. The root path
// selects the null at the "---" header of such a document. When the
// document lacks a header, the root path does not select anything. Nodes
// returns an empty result there for a path with a selector, as it does
// wherever a path does not select anything.
//
// # Errors
//
// Wraps [ErrNoCurrentNode] for an `@` path. Wraps [ErrNoDocument], together
// with [ErrNotFound], when the Resolver has a nil document. Wraps
// [ErrAlias] when an alias on the path does not resolve, including one
// under a tag. A `.*` selector reads every `<<` merge key of its mapping
// and of the mappings it merges, so an alias that one of them names counts
// as on the path. A `..*` selector lists every value below the node it
// starts from, so an alias it lists also counts as on the path, unless the
// alias reads a reference document. A `..name` or `..*` selector looks up
// the key of each entry that a `<<` merge key after it might override. An
// alias of a reference document whose mapping holds that key thus counts as
// on the path too. The error is an [*AliasError] that carries the alias, as
// [Resolver.Node] returns one.
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
// decoder reads node, and a caller can learn from the tag how the decoder
// types a scalar. A caller that walks the document itself should read the
// node that selectors apply to with [Resolver.Content], which also looks
// through the tags. Deref gives an untyped nil for a nil node and for an
// anchor or alias whose content is nil, including a typed nil that a
// hand-built tree might hold.
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

// Content returns the node that selectors apply to at node. That node is
// the mapping, sequence, or scalar under every anchor, alias, and tag on
// node. The one exception is a tag that converts a collection, such as
// `!!str` or `!!null` on a mapping or on an alias of one. The decoder
// reads a scalar there, so Content returns that [*ast.TagNode]. A
// selector selects nothing below such a tag, as the package documentation
// describes. A tag on a scalar, such as `!!str 5`, gives the scalar.
//
// A caller that walks the document itself can thus branch on the type of
// the result. The result is an [*ast.MappingNode] exactly where `.name`
// and `.*` apply to the entries below node. It is an [*ast.SequenceNode]
// exactly where `[n]` and `[*]` apply to the items below node. An empty
// mapping still gives a mapping, though `.*` matches nothing in it. Any
// other result means that no selector selects below node. Content gives
// an untyped nil where [Resolver.Deref] does, such as for a nil node.
//
// Returns the errors that [Resolver.Deref] returns for node: an
// [*AliasError] wrapping [ErrAlias] for an alias that does not resolve,
// and an error wrapping [ErrReferenceAlias] for an alias that reads a
// reference document.
func (r *Resolver) Content(node ast.Node) (ast.Node, error) {
	content, err := r.resolver.collectionContent(node)
	if err != nil {
		return nil, fmt.Errorf("content: %w", err)
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

// Entry returns the entry that [Path.Child] with name selects in the
// mapping at node. Where a `<<` merge key brings that entry in, it
// belongs to the merge source. Entry looks through the anchors, tags, and
// aliases on node. A caller that walks a mapping itself can compare the
// result with an entry it reached to learn whether a path through name
// selects that entry. A later entry with the same key text can win the
// selector. That entry can be a later key of the mapping or of a later
// merge source.
//
// Returns an error wrapping [ErrNotFound] when node is not a mapping or
// name does not select an entry in it. Returns an [*AliasError] wrapping
// [ErrAlias] when an alias on the way does not resolve. The error wraps
// [ErrReferenceAlias] too when a `<<` merge key brings in the entry from a
// reference document, as [WithReferenceAnchors] describes. Returns an error
// wrapping [ErrExcessiveMerging] when the lookup reads far more nodes
// under `<<` merge keys than the document holds. Each call weighs its own
// reads against that limit, but the calls of one [EntryFinder] weigh
// theirs together.
func (r *Resolver) Entry(node ast.Node, name string) (*ast.MappingValueNode, error) {
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
func (f *EntryFinder) Entry(node ast.Node, name string) (*ast.MappingValueNode, error) {
	content, err := f.resolver.selectable(node)
	if err != nil {
		return nil, fmt.Errorf("entry %q: %w", name, err)
	}

	mapping, ok := content.(*ast.MappingNode)
	if !ok || mapping == nil {
		return nil, fmt.Errorf("entry %q: %w: not a mapping", name, ErrNotFound)
	}

	entry, _, ok, err := f.resolver.lookup(mapping, name, nil, &f.reads, nil)
	if err != nil {
		return nil, fmt.Errorf("entry %q: %w", name, err)
	}

	if !ok {
		return nil, fmt.Errorf("entry %q: %w", name, ErrNotFound)
	}

	return entry, nil
}

// Anchor returns the anchor that alias refers to. The result is the
// anchor whose content [Resolver.Deref] reaches first when it follows the
// alias. Where the anchor holds another alias, Anchor stops at the anchor,
// but Deref continues to the content of the anchor that the second alias
// refers to. Anchor also returns the anchor of an alias that leads back to
// itself, such as one inside the content of that anchor. Deref rejects
// such an alias.
//
// The document holds a tagged alias, such as `!!str *x`, as an
// [*ast.TagNode] whose Value is the alias, so pass that Value to Anchor.
//
// Returns an [*AliasError] wrapping [ErrAlias] for an alias that does not
// have a name or does not name an anchor before it. The error of an alias
// whose anchor a reference document defines wraps [ErrReferenceAlias] too,
// as [WithReferenceAnchors] describes. Returns an error wrapping
// [ErrNotFound] for a nil alias.
func (r *Resolver) Anchor(alias *ast.AliasNode) (*ast.AnchorNode, error) {
	if alias == nil {
		return nil, fmt.Errorf("anchor: %w: nil alias", ErrNotFound)
	}

	tk := astnode.Token(alias.Value)
	if tk == nil {
		return nil, fmt.Errorf("anchor: %w", &AliasError{Alias: alias, Cause: errNoAliasName})
	}

	anchor, ok := r.resolver.binding().targets[alias]
	if !ok {
		return nil, fmt.Errorf("anchor: %w", &AliasError{Alias: alias, Cause: r.resolver.missingAnchor(tk.Value)})
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
// Returns nil when node is not a mapping, and an [*AliasError] wrapping
// [ErrAlias] when an alias on the way does not resolve. MergeSources
// returns mappings of the document alone, so the error of an alias that
// reads a reference document wraps [ErrReferenceAlias].
func (r *Resolver) MergeSources(node ast.Node) ([]*ast.MappingNode, error) {
	content, err := r.resolver.unwrap(node)
	if err != nil {
		return nil, fmt.Errorf("merge sources: %w", err)
	}

	mapping, ok := content.(*ast.MappingNode)
	if !ok || mapping == nil {
		return nil, nil
	}

	var (
		sources []*ast.MappingNode
		found   []lookupSource
	)

	for _, entry := range mapping.Values {
		if entry == nil || !astnode.IsMergeKey(entry.Key) {
			continue
		}

		found, _, err = r.resolver.mergeSources(found[:0], entry.Value, nil, false)
		if err != nil {
			return nil, fmt.Errorf("merge sources: %w", err)
		}

		for _, src := range found {
			sources = append(sources, src.mapping)
		}
	}

	return sources, nil
}
