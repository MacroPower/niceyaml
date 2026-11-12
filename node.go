package niceyaml

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/escape"
	"go.jacobcolvin.com/niceyaml/internal/aliasing"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/datapath"
	"go.jacobcolvin.com/niceyaml/internal/decodetree"
	"go.jacobcolvin.com/niceyaml/internal/pathindex"
	"go.jacobcolvin.com/niceyaml/internal/rejection"
	"go.jacobcolvin.com/niceyaml/internal/segment"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

func init() {
	aliasing.StateOf = documentState
}

// documentState returns the [*aliasing.State] of the document of n, a
// [*Node] or the [decodedValue] of one, or nil for a nil Node. Other
// packages of the module call it as [aliasing.StateOf].
func documentState(n any) *aliasing.State {
	if v, ok := n.(decodedValue); ok {
		n = v.node
	}

	node, ok := n.(*Node)
	if !ok {
		panic(fmt.Sprintf("aliasing.StateOf: %T is not a *niceyaml.Node", n))
	}

	if node == nil {
		return nil
	}

	return node.doc.state
}

// document holds the state of one YAML document of a [Source], which every
// [Node] of the document shares.
type document struct {
	// The root Node of the document.
	node *Node
	// The node the document parsed into. For a document that did not
	// parse, it is the stand-in [docparse.Parse] made, which does not have
	// a body.
	root *ast.DocumentNode
	// The error of a document that did not parse, or nil for one that
	// parsed. The bindSyntaxErrors function binds it to the Source
	// and to the document that it picks for the error.
	err error
	// Returns the resolver of every path in root, and creates it on the
	// first call. Nodes do not edit the tree, so the resolver binds the
	// aliases once.
	resolver func() *paths.Resolver
	// Returns the tree the go-yaml decoder reads for the document, and
	// builds it on the first call.
	tree func() *decodetree.Tree
	// Returns the index that finds the node of root at a position, and
	// builds it on the first call of PathAt.
	pathIndex func() *pathindex.Index
	// The state that the packages of the module reach through
	// aliasing.StateOf.
	state *aliasing.State
	// The tokens of the whole document.
	tokens token.Tokens
	// The tokens that carry a position, in the order of their offsets,
	// which the extent of a Node searches.
	positioned token.Tokens
	index      int
	// The index of root in the Docs of the file that the Source parsed. It
	// can differ from index, because preamble nodes fold into documents.
	fileIndex int
	// The number of tokens at the start of tokens before the content.
	preamble int
}

// pathResolver returns the [paths.Resolver] of the document.
func (d *document) pathResolver() *paths.Resolver {
	return d.resolver()
}

// newResolver creates the [paths.Resolver] of the document. A document
// that did not parse gets one without a document, so a path returns an
// error rather than an empty match. The resolver knows the anchors of the
// reference documents of the Source, so a path through an alias that reads
// one names that cause. The alias limit of the Source sets the limits of
// the resolver, as [WithAliasLimit] describes.
func (d *document) newResolver() *paths.Resolver {
	if d.err != nil {
		return paths.NewResolver(nil)
	}

	src := d.node.source
	limit := paths.WithAliasLimit(!src.skipAliasLimit)

	if len(src.references) == 0 {
		return paths.NewResolver(d.root, limit)
	}

	// The table of the reference anchors builds on the first alias that
	// lacks an anchor, so a path that does not meet one does not read the
	// reference documents.
	return paths.NewResolver(d.root, limit, paths.WithReferenceAnchors(src.referenceValue))
}

// pastAliasLimit reports whether the aliases of the document take it past
// the alias limit, as [aliasing.PastLimit] reports it. A lookup of the
// entries that a `<<` merge key brings in reads its merge sources through
// the aliases of the document. Those reads are part of what the limit
// weighs, so a document past the limit does not read them.
func (d *document) pastAliasLimit() bool {
	return aliasing.PastLimit(d.node)
}

// Node is a scope in a YAML document. It is either the root of the
// document, which [Source.Documents] and [Source.Document] return, or a
// node that a path selects. Every method reads from the node and resolves
// an `@` path from it. A `$` path resolves from the root of the document
// through any Node. [Node.Decode] decodes the node alone, [Node.Validate]
// runs a [Validator] on it, and [Node.Bind] resolves the `@` paths of an
// error from it. A check written for the type of a value thus reports the
// same lines whether the value is the whole document or one inside it. A
// value at an alias differs in one case. A decode of the Node at the alias
// binds a rejection of the whole value at the alias. A decode of an
// enclosing Node binds the same rejection at the anchor.
//
// Receive instances from [Source.Documents], [Source.AllDocuments],
// [Source.Document], [Node.At], [Node.Nodes], [Node.Document],
// [SourceError.Node], or [SourceError.Document]. The zero Node is not a
// valid Node.
//
// A nil Node does not belong to a document. Its readers return zero values
// or false, and [Node.View] returns a view without lines. The readers are
// [Node.Err], [Node.AST], [Node.DocumentAST], [Node.Document],
// [Node.Source], [Node.FilePath], [Node.FS], [Node.Path], [Node.Tokens],
// [Node.Preamble], [Node.Text], [Node.Scalar], [Node.Span], [Node.Range],
// [Node.KeyRange], [Node.PathAt], [Node.NodeAt], [Node.Kind],
// [Node.IsEmpty], [Node.Target], [Node.Origin], and [Node.MarshalYAML].
// [Node.Bind] returns nil for a nil error, and [Node.Warn] does nothing
// for one. The methods that need the document panic on a nil Node. They
// are [Node.At], [Node.Nodes], [Node.Ranges], [Node.Decode],
// [Node.DecodeInto], [Node.DecodeAt], [Node.DecodeIfPresent],
// [Node.YAMLComments], [Node.Validate], [Node.SelfValidate],
// [Node.DocumentResolver], [Node.DataLocator], [Node.DocumentIndex], and
// [Node.Bind] and [Node.Warn] of an error that is not nil.
//
// [SourceError.Node], [SourceError.Document], [Source.DocumentAt], and
// [Node.NodeAt] return a nil Node with false. A report thus reads the
// file path, the path, and the lines of the Node of each problem without
// checking for nil.
//
// The Decoding section of the package documentation describes the steps
// a decode runs. The Scoped Nodes section describes what a Node from
// [Node.At] or [Node.Nodes] reports. A path that selects the root gives
// the root itself, so a document has one Node at `$`, and only that Node
// reports [paths.Doc] from [Node.Path]. [Node.DocumentAST],
// [Node.DocumentIndex], [Node.DocumentResolver], [Node.Preamble], and
// [Node.FilePath] describe the document as a whole, whatever Node of it a
// caller holds. [Node.Document] returns the root.
//
// A document that did not parse does not have a tree. The methods that
// [Node.Err] lists return the error that Err returns. The methods
// that read the tokens and the lines work as they do for any document, so
// a caller renders or diffs a document that does not parse yet.
//
// A Node of the Source that [NewSourceFromLayers] builds belongs to the
// document that its layers merge into. Each error it returns binds in the
// file of a layer, as NewSourceFromLayers describes.
type Node struct {
	// The node the document writes at the scope, which At or Nodes resolves
	// once when it scopes the Node. At an alias, it is the alias, or the tag
	// on it. The root of a document leaves it unset, because its node is
	// the body of the document.
	node ast.Node
	// The node a decode reads, as paths.Resolver.Node gives it for the
	// scope. It differs from node at an alias that the resolver follows to
	// the content of its anchor. The root leaves it unset too.
	value  ast.Node
	source *Source
	// The enclosing document.
	doc *document
	// The tokens of the node. The root holds the tokens of the whole
	// document, and a Node that At scopes below the root holds a
	// sub-slice of them.
	content token.Tokens
	// The scope: the `$` path from the document root to the node, which is
	// paths.Doc for a whole document.
	base paths.Path
	// The lines of the source that the node covers.
	span position.Span
}

// DocumentAST returns the [*ast.DocumentNode] of the whole document the
// Node belongs to, the go-yaml node the document parsed into.
// [Node.Document] returns the root [*Node] of the same document.
// [Node.AST] returns the go-yaml node the Node selects, and [Node.Path] is
// the path from the document root to it. The node is part of the tree
// that [Source.File] returns. Every Node of the Source shares that tree
// and resolves against it, so a caller must not modify the node. A
// document that did not parse does not have a node, so DocumentAST
// returns nil for it, as [Node.Err] describes. It also returns nil for a
// nil Node.
func (n *Node) DocumentAST() *ast.DocumentNode {
	if n == nil || n.doc.err != nil {
		return nil
	}

	return n.doc.root
}

// Err returns the error of a document that did not parse, or nil for a
// document that parsed and for a nil Node. The error matches [ErrSyntax] for
// text that the parser rejects. It matches [ErrLimit] for a document past the
// depth limit or the document byte limit, and for a text past the byte limit
// of the Source. It matches
// [ErrEncoding] for the one document of a text that is not UTF-8. A syntax error
// fails the one document that holds it, and [Source.AllDocuments] returns a
// Node for that document too. The error is a [*SourceError] bound to the
// Source and to that document, so [SourceError.Document] returns its root. It
// is among the errors [Source.File] returns:
//
//	for _, doc := range source.AllDocuments() {
//		if err := doc.Err(); err != nil {
//			log.Print(niceyaml.FormatError(err))
//
//			continue
//		}
//
//		lipgloss.Println(p.Print(doc.View()))
//	}
//
// [Node.Decode], [Node.DecodeInto], [Node.DecodeAt],
// [Node.DecodeIfPresent], [Node.MarshalYAML], [Node.Validate], [Node.At],
// [Node.Nodes], and [Node.Ranges] return the error that Err returns, and
// they do not run a [Validator]. [Node.AST] and [Node.DocumentAST] return
// nil, and [Node.PathAt] reports false for every position.
//
// A panic in the parser fails the documents it was reading the same way.
// Err then returns an error that holds a [PanicError] and does not match
// ErrSyntax. A "---" header that directly follows an anchor without a
// value parses together with the document above it. Both documents then
// return the same error, bound to the one that holds its location.
func (n *Node) Err() error {
	if n == nil {
		return nil
	}

	return n.doc.err
}

// DocumentResolver returns the [*paths.Resolver] of the whole document the
// Node belongs to, which every Node of the document shares. It resolves
// a `$` path from the document root, as [paths.Resolver] describes. It
// does not have the Node as a current node. It thus returns an error that
// wraps [paths.ErrNoCurrentNode] for an `@` path, where [Node.At] reads
// one from the Node. A path below the Node therefore starts with
// [Node.Path], which [paths.Path.Join] puts in front of an `@` path:
//
//	c, err := b.DocumentResolver().Node(b.Path().Join(paths.Current().Child("c")))
//
// The resolver follows the aliases and merge keys of the go-yaml nodes
// that [Node.AST] and [Node.DocumentAST] return.
//
// The document creates the resolver once, when the first of its Nodes
// needs it. The resolver binds the aliases of the document once, when a
// path first needs them. A [Validator] that runs for each item of a list
// therefore binds them once, but a resolver from [paths.NewResolver] binds
// them again for every item.
//
// The resolver applies the limits behind [ErrExcessiveAliasing] and
// [paths.ErrExcessiveMerging], as [paths.WithAliasLimit] describes.
// [WithAliasLimit] on the [Source] turns both limits off.
//
// The resolver knows the anchors of the reference documents of
// [WithReferences], as [paths.WithReferenceAnchors] describes. A path that
// ends at an alias that reads one of them selects the alias. A node that
// [paths.Resolver.Node] or [paths.Resolver.Matches] returns can thus be such
// an alias, or the tag on one, rather than the content of its anchor. A path
// through the alias returns an error that wraps [paths.ErrReferenceAlias],
// and so does a path through a key whose entry a `<<` merge key brings in
// from such an anchor. A resolver from paths.NewResolver does not know those
// anchors, so it fails at the alias with an error that names an alias
// without an anchor.
//
// A document that did not parse does not have a tree, as [Node.Err]
// describes. Its resolver is thus the one that [paths.NewResolver] creates
// for a nil document. Every `$` path it resolves returns an error wrapping
// [paths.ErrNoDocument]. An `@` path still returns an error wrapping
// [paths.ErrNoCurrentNode] instead. In an empty document that parsed,
// [paths.Resolver.Matches] instead returns an empty result and a nil
// error.
func (n *Node) DocumentResolver() *paths.Resolver {
	return n.doc.pathResolver()
}

// Document returns the root [*Node] of the document the Node belongs to.
// A Node from [Node.At] thus reaches the whole document, as a validator
// does when it picks a schema from the file path or the content of the
// document. Every Node of a document returns the root that
// [Source.AllDocuments] returns, and that root returns itself. A nil Node
// does not belong to a document, so Document returns nil for it.
func (n *Node) Document() *Node {
	if n == nil {
		return nil
	}

	return n.doc.node
}

// AST returns the [ast.Node] that the document writes at the scope of the
// Node. For the root Node, it is the body of the whole document. For a
// Node from [Node.At] or [Node.Nodes], it is the node at the path that
// method scoped. [Node.Decode] decodes that node, except at an alias,
// where a decode reads the content of the anchor that [Node.Target]
// returns. The String method of any node, a mapping or a sequence
// included, returns its text:
//
//	scoped, err := doc.At(path)
//	if err != nil {
//		return err
//	}
//
//	fmt.Println(scoped.AST().String())
//
// The body is what the parser built: nil for an empty document, and a
// comment group for one holding only comments. A file might hold only
// whitespace, or text that the lexer does not emit a token for, such as a
// lone "!". The body of its one document is then a scalar that holds the
// placeholder token [tokens.Tokenize] makes. [tokens.IsPlaceholder] tells
// that scalar apart from a scalar the file holds. A document of whitespace
// below a "---" header has a nil body.
//
// AST returns each of these bodies as it is, because such a document does
// not decode to a value. [Node.IsEmpty] reports each of these documents,
// so a caller does not need to tell the bodies apart. A document that did
// not parse does not have a body either, and [Node.Err] tells it apart
// from an empty document. The node is part of the tree that [Source.File]
// returns. Every Node of the Source shares that tree and resolves against
// it, so a caller must not modify the node.
//
// The body of the root Node keeps the anchor or the tag written on it.
// The node of a Node from At or Nodes keeps the tag. A type switch on
// the node can thus see an [*ast.AnchorNode] or an [*ast.TagNode] where
// the source holds a mapping. A Node at an alias holds the
// [*ast.AliasNode], or the tag on it, as the document writes it at the
// path. The content of the anchor lies where the anchor defines it, and
// [Node.Target] returns the Node there. AST thus differs from the node
// that [paths.Resolver.Node] gives for [Node.Path] at an alias that the
// resolver follows. [Node.Kind] looks through the tag and the alias,
// and reports whether the Node holds a mapping, a sequence, or a
// scalar.
//
// AST returns nil for a nil Node.
func (n *Node) AST() ast.Node {
	if n == nil {
		return nil
	}

	if n.base.IsRoot() {
		return n.doc.root.Body
	}

	return n.node
}

// decodedValue is the [aliasing.Node] of what a decode of node reads. Its
// AST is [Node.valueNode], so the alias checks of a Node at an alias weigh
// the content of the anchor that the decode reads rather than the alias.
type decodedValue struct {
	node *Node
}

// AST returns the node that a decode of the [Node] reads.
func (v decodedValue) AST() ast.Node {
	return v.node.valueNode()
}

// valueNode returns the node that a decode of n reads. It is the node
// that [paths.Resolver.Node] gives for the scope, which lies at the anchor
// for a Node at an alias that the resolver follows.
func (n *Node) valueNode() ast.Node {
	if n.base.IsRoot() {
		return n.doc.root.Body
	}

	return n.value
}

// At returns a [*Node] scoped to the node that path selects. An `@` path
// resolves from the receiver, and a `$` path resolves from the root of the
// document. A path from [Node.Path] or [Node.PathAt] of any Node of the
// document thus scopes the node it names. The Node shares the source and
// the document with the receiver, and decodes, validates, and binds
// errors as the Scoped Nodes section of the package documentation
// describes. A path that selects the root, such as [paths.Doc], gives the
// root that [Node.Document] returns rather than a Node scoped to its body.
// That Node spans the whole document, its preamble and its comments
// included. A function that takes only the root of a document takes it.
//
// A path that ends at an alias scopes the alias, or the tag on it, as the
// document writes it at the path. [Node.AST], [Node.Tokens], [Node.Span],
// [Node.View], and [Node.Range] describe the alias, and an error at the
// root of the scope binds there. A decode of that Node, [Node.Kind], and
// an `@` path below it read the content of the anchor. [Node.Target]
// returns the Node of that content, where the anchor defines it.
//
// A path that enters an alias that reads a reference document of
// [WithReferences] returns an error that wraps [paths.ErrReferenceAlias], as
// the References section of the package documentation describes. So does a
// path through a key whose entry a `<<` merge key brings in from such a
// document. A key that the merge key does not bring in resolves as it does
// in a mapping without the merge key. An alias inside the content of the
// anchor that it refers to reads null, as the
// go.jacobcolvin.com/niceyaml/paths package describes. A decode of a Node at
// such an alias thus reads null. A path that enters such an alias returns an
// error that wraps [paths.ErrAlias].
//
// A sequence element and the root do not have a key. A path with the `~`
// selector of [paths.Path.Key] after either thus does not select a node,
// and At returns an error wrapping [paths.ErrNotFound] for it. That
// error, [Node.Ranges], and an [Error] at a path that ends in such a `~`
// still point at the element or the root.
//
// A path that does not select a node returns the error that
// [paths.Resolver.Node] describes, bound to the source. It wraps
// [paths.ErrNotFound] when the document does not hold a value at the
// path, and [paths.ErrNoDocument] too when the document does not have
// content. It wraps [paths.ErrAlias] for an alias on the path that does
// not resolve. It wraps [paths.ErrExcessiveMerging] when `<<` merge keys
// make the lookups read far more nodes than the document holds, as
// [Node.Nodes] describes for that limit. It wraps
// [paths.ErrWildcard] for a path that can match several nodes, and
// [Node.Nodes] scopes each node that such a path selects.
//
// A document that did not parse returns the error that [Node.Err]
// returns. The Scoped Nodes section describes where a not-found error
// binds, and why only the error of At tells that a value is absent.
func (n *Node) At(path paths.Path) (*Node, error) {
	if n.doc.err != nil {
		return nil, n.doc.err
	}

	base := n.base.Join(path)

	node, err := n.doc.pathResolver().Node(base)
	if err != nil {
		// Bind to the receiver, a Node that exists, rather than to a Node
		// scoped at a path that resolves to no node.
		return nil, n.bindUnresolved(path, err)
	}

	return n.scopedAt(base, node), nil
}

// Nodes returns a [*Node] scoped to each node that path selects, in
// document order. [Node.At] rejects a path with a `[*]`, `.*`, `..name`,
// or `..*` selector. Through such a path, Nodes scopes every element of a
// sequence, every entry of a mapping, every entry with a name at any
// depth, or every node at any depth, each once. The path resolves as for
// Node.At, and Nodes scopes each Node as Node.At scopes its result.
// [Node.Path] of each is the `$` path that selects its node alone, so an
// error bound to it reports the element it came from:
//
//	items, err := doc.Nodes(paths.Current().Child("items").IndexAll())
//	if err != nil {
//		return err
//	}
//
//	for _, item := range items {
//		if err := item.Validate(ctx, itemSchema); err != nil {
//			errs = append(errs, err) // bound at $.items[i]
//		}
//	}
//
// [Each] turns that loop into a [Validator], so a decode or a
// [MultiValidator] can run it beside other validators.
//
// The last selector of that path names an entry by the text that
// [paths.Resolver.KeyName] gives its key. [paths.Path.Last] gives that
// selector.
//
// The `~` selector of [paths.Path.Key] scopes the key of each entry that
// the path before it selects. A sequence element and the root do not have
// a key, so it scopes nothing for them. The path `$..*~` thus scopes the
// key of each entry that `..*` selects and none of the sequence elements.
// [paths.Path.RecursiveAll] names the entries that `..*` leaves out, such
// as the entry of a `<<` merge key.
//
// A path that does not select a node returns an empty slice and a nil
// error. A path with a selector returns the same in a document without
// content, but Node.At returns an error wrapping [paths.ErrNoDocument]
// for it. In such a document, the root path selects the null under the
// "---" header. It does not select a node when the document lacks a
// header. Where the root path selects a node, Nodes gives the root that
// [Node.Document] returns for it, as Node.At does.
//
// Nodes scopes an alias as Node.At does. A `[*]` thus scopes an element
// that the document writes as an alias at the alias, beside the elements
// that the document holds. A `..*` selector lists each alias and does not
// descend below it, as [paths.Path.RecursiveAll] describes.
//
// The errors that [paths.Resolver.Nodes] returns come back bound to the
// source. An alias that does not resolve gives one wrapping
// [paths.ErrAlias]. Aliases or merge keys that lead a selector to far
// more nodes than the document holds give [ErrExcessiveAliasing] or
// [paths.ErrExcessiveMerging], bound to the receiver without a location.
// The document is at fault for either, as [IsInvalid] describes, and
// [WithAliasLimit] on the [Source] turns both limits off. A document that
// did not parse returns the error that [Node.Err] returns.
func (n *Node) Nodes(path paths.Path) ([]*Node, error) {
	if n.doc.err != nil {
		return nil, n.doc.err
	}

	found, err := n.doc.pathResolver().Matches(n.base.Join(path))
	if err != nil {
		if bound, ok := n.bindAlias(path, err); ok {
			return nil, bound
		}

		return nil, n.bindLookup(err)
	}

	nodes := make([]*Node, 0, len(found))

	for _, m := range found {
		nodes = append(nodes, n.scopedMatch(m))
	}

	return nodes, nil
}

// scopedAt returns the Node that [Node.At] gives for path, a `$` path that
// selects node. The root path gives the root of the document, so a
// document has one Node at `$`. Any other path gives the Node that
// [Node.scopedIn] gives for the holder of node.
func (n *Node) scopedAt(path paths.Path, node ast.Node) *Node {
	if path.IsRoot() {
		return n.doc.node
	}

	return n.scopedIn(path, node, datapath.ParentAt(n.doc.pathResolver(), path))
}

// scopedMatch returns the Node that [Node.Nodes] gives for m, a match of a
// `$` path. It scopes the node of m as [Node.scopedAt] does, with the
// holder that m carries.
func (n *Node) scopedMatch(m paths.Match) *Node {
	if m.Path.IsRoot() {
		return n.doc.node
	}

	return n.scopedIn(m.Path, m.Node, astnode.Parent{Entry: m.Entry, Sequence: m.Sequence, Index: m.Index})
}

// scopedIn returns a copy of n that [Node.scoped] scopes to node. The path
// is a `$` path other than the root that selects node, and in holds node.
// A path that ends at an alias gives a copy scoped to the alias, as the
// document writes it at the path, that reads node.
func (n *Node) scopedIn(path paths.Path, node ast.Node, in astnode.Parent) *Node {
	held := heldIn(in)
	intro := func() *token.Token { return astnode.PathToken(held, in) }

	written := n.writtenIn(path, in)
	if _, ok := astnode.Content(written).(*ast.AliasNode); !ok {
		return n.scopedBy(path, node, intro)
	}

	c := n.scopedBy(path, written, intro)
	c.value = node

	return c
}

// writtenAt returns the node that the document writes at path, a `$` path
// other than the root, as [Node.writtenIn] gives it.
func (n *Node) writtenAt(path paths.Path) ast.Node {
	// ParentAt gives no holder for a `~`, which picks the key of an entry.
	return n.writtenIn(path, datapath.ParentAt(n.doc.pathResolver(), path))
}

// writtenIn returns the node that the document writes at path, a `$` path
// other than the root, where in holds that node. It returns nil when no
// entry or element holds the node. A path that ends in `~` names the key
// of its entry, which in does not hold.
func (n *Node) writtenIn(path paths.Path, in astnode.Parent) ast.Node {
	if held := heldIn(in); held != nil {
		return held
	}

	if path.IsKey() {
		return n.entryKey(path)
	}

	return nil
}

// heldIn returns the node that in holds, as the document writes it. That
// node is the value of the entry of in, or the element of its sequence
// at its index. The zero [astnode.Parent] does not hold a node, so heldIn
// returns nil for it.
func heldIn(in astnode.Parent) ast.Node {
	switch {
	case in.Entry != nil:
		return in.Entry.Value
	case in.Sequence != nil && in.Index < len(in.Sequence.Values):
		return in.Sequence.Values[in.Index]
	default:
		return nil
	}
}

// scoped returns a copy of n scoped at path, a `$` path that selects node,
// which a decode of the copy reads. The span of a mapping or a sequence
// starts at the key or "-" that introduces it, so it holds the token
// [paths.Resolver.Token] gives path.
func (n *Node) scoped(path paths.Path, node ast.Node) *Node {
	return n.scopedBy(path, node, func() *token.Token {
		in := datapath.ParentAt(n.doc.pathResolver(), path)

		return astnode.PathToken(heldIn(in), in)
	})
}

// scopedBy returns a copy of n scoped at path, a `$` path that names node,
// as [Node.scoped] describes. The span of a mapping or a sequence starts
// at the token that intro gives, the key or "-" that introduces it.
func (n *Node) scopedBy(path paths.Path, node ast.Node, intro func() *token.Token) *Node {
	c := *n
	c.base = path
	c.node = node
	c.value = node
	c.span, c.content = n.extent(node)

	// A scalar or an alias is its own token, so no token above it
	// introduces it.
	if astnode.IsScalar(astnode.Content(node)) {
		return &c
	}

	tk := intro()
	if tk == nil || tk.Position == nil {
		return &c
	}

	if at := tk.Position.Line - 1; at >= 0 && at < c.span.Start {
		c.span = position.NewSpan(at, c.span.End)
	}

	return &c
}

// extent returns the lines and the tokens of node in the document. The
// lines end at the last one that holds content of those tokens. A node
// without a token in the document, such as an implicit null, covers its
// line.
func (n *Node) extent(node ast.Node) (position.Span, token.Tokens) {
	first, last := astnode.Bounds(node)
	if len(first) == 0 {
		return position.Span{}, nil
	}

	total := n.source.lines.Len()
	lo, hi := first[0].Position.Offset, last[0].Position.Offset

	all := n.doc.positioned
	from := sort.Search(len(all), func(i int) bool { return all[i].Position.Offset >= lo })
	to := sort.Search(len(all), func(i int) bool { return all[i].Position.Offset > hi })
	tks := all[from:to:to]

	// A token with no text, such as the empty content of a block scalar or
	// an implicit null, can share its offset with a token of another node.
	// A token at either end of the window counts only when node holds it.
	outside := func(tk *token.Token) bool {
		switch tk.Position.Offset {
		case lo:
			return !slices.ContainsFunc(first, func(b *token.Token) bool { return segment.SameToken(tk, b) })
		case hi:
			return !slices.ContainsFunc(last, func(b *token.Token) bool { return segment.SameToken(tk, b) })
		default:
			return false
		}
	}

	if slices.ContainsFunc(tks, outside) {
		tks = slices.DeleteFunc(slices.Clone(tks), outside)
	}

	if len(tks) == 0 {
		at := min(max(first[0].Position.Line-1, 0), total)

		return position.NewSpan(at, min(at+1, total)), nil
	}

	start := tks[0].Position.Line - 1
	end := start

	// The last token with content ends the span. A token after it, such as
	// the empty content of a block scalar, holds no text to put a line
	// into the span.
	for _, tk := range slices.Backward(tks) {
		if lastLine, ok := n.lastContentLine(tk); ok {
			end = max(end, lastLine)

			break
		}
	}

	return position.NewSpan(min(max(start, 0), total), min(end+1, total)), tks
}

// lastContentLine returns the last line that holds content of tk other
// than spaces, or false when tk does not hold such content on any line.
// Skipping the indentation that the lexer folds into a scalar keeps the
// line of a sibling out of the span.
func (n *Node) lastContentLine(tk *token.Token) (int, bool) {
	lines := n.source.lines

	last, found := 0, false

	for i := max(tk.Position.Line-1, 0); i < lines.Len(); i++ {
		sp, ok := lines.Line(i).ContentSpan(tk)
		if !ok {
			// The lines of a token run on from the line its position
			// names, so the first line without it ends the search.
			break
		}

		if sp.Len() > 0 {
			last, found = i, true
		}
	}

	return last, found
}

// Path returns the scope of the [Node], which is the `$` path from the
// document root to the node. It is [paths.Doc] for the root Node of a
// document and the joined paths for a Node from [Node.At]. A Node from
// [Node.Nodes] reports the path that selects its node alone, so the Node
// for the first match of `$.items[*]` reports `$.items[0]`. The path
// starts at `$`, so a caller can pass it to [Node.At], [Node.Ranges], or
// [AtPath] through any Node of the document. It names the same node there.
// A nil Node returns the zero [paths.Path].
func (n *Node) Path() paths.Path {
	if n == nil {
		return paths.Path{}
	}

	return n.base
}

// Source returns the [*Source] the node came from, or nil for a nil Node.
func (n *Node) Source() *Source {
	if n == nil {
		return nil
	}

	return n.source
}

// DocumentIndex returns the 0-indexed position within the file of the
// document the Node belongs to. It counts every document of the file, an
// empty one included, so it is the index of the root in the slice that
// [Source.AllDocuments] returns. [Source.Documents] leaves out the empty
// documents beside one with content, so a root below one of them has a
// smaller index in that slice. A Node from [Node.At] reports the index of
// the document that holds it. The index of the node itself within a
// sequence is the Index of the selector that [paths.Path.Last] gives for
// [Node.Path].
//
// The message of a bound error counts documents from 1, as it counts
// lines, so "document 3" in [SourceError.Error] is the document at
// index 2. [SourceError.DocumentIndex] reports the index of the document
// an error is bound to, for a caller that holds the error but not a Node.
//
// DocumentIndex panics on a nil Node, because the index 0 names the first
// document.
func (n *Node) DocumentIndex() int {
	return n.doc.index
}

// Tokens returns the tokens of the node, with the positions they have in
// the source. For the root of a document, they are its preamble and its
// content. For the root of the last document, they also hold the comments
// after a "..." marker that ends it. After any other document, those
// comments become the preamble of the next document. The tokens are nil
// when the document does not have a token to anchor it, such as a
// document with neither a header nor a body.
//
// A path that selects the root gives the root, so [Node.At] of such a
// path returns the tokens of the root. For a Node that Node.At scopes
// below the root, they run from the first token under the node through
// the last, comments between them included. They are nil when the
// scope does not select anything, and for a nil Node. The slice is a
// copy, so reordering it does not affect the Node. Every Node shares the
// tokens themselves, so a caller must not modify them.
//
// The go-yaml parser does not read these tokens as they are, as
// [Source.Tokens] describes. Hand the parser the stream that
// [tokens.ForParser] returns. That stream gives the tree that go-yaml
// builds, which can differ from the tree that [Source.File] builds. For a
// tree that File builds, take the File of a second Source, as Source.File
// describes.
func (n *Node) Tokens() token.Tokens {
	if n == nil {
		return nil
	}

	return slices.Clone(n.content)
}

// Text returns the text of the node as [Source.Text] writes it, with the
// line endings, the quotes, and the escapes of the source. Like
// [Node.Tokens], Text treats the root of a document differently from a
// Node below it.
//
// For the root of a document, Text holds each line of [Node.Span] with its
// line ending. It ends with the final line ending of the document when the
// text has one. A byte order mark at the start of the first line of a
// document belongs to that document. The texts of the documents that
// [Source.AllDocuments] returns thus join into Source.Text:
//
//	for _, doc := range source.AllDocuments() {
//		out.WriteString(doc.Text())
//	}
//
// The root of a document that did not parse has a Text too, so a stream
// splitter copies every document of a file. The text that
// [go.jacobcolvin.com/niceyaml/edit.Doc.Text] returns for a document
// leaves out the byte order mark at its start, so it can differ from Text.
//
// For a Node that [Node.At], [Node.Nodes], or [Node.NodeAt] scopes below
// the root, Text holds the bytes of [Node.Range]. For `image: "nginx:1.25"`,
// the Text of `$.image` is `"nginx:1.25"` with its quotes. The text of a
// block mapping or a block sequence keeps the indentation of the file on
// every line but the first. It is thus not a document of its own.
//
// Text returns "" for a nil Node and for a scope that does not select
// anything.
func (n *Node) Text() string {
	if n == nil {
		return ""
	}

	src := n.source
	text := src.Text()

	if n.base.IsRoot() {
		// The first document starts the text and the last one ends it, even
		// where they hold no line, as in a text of a byte order mark alone.
		start, end := 0, len(text)
		if n.doc.index > 0 {
			start = src.lineStart(n.span.Start)
		}

		if n.span.End < src.lines.Len() {
			end = src.lineStart(n.span.End)
		}

		return text[start:max(start, end)]
	}

	r, ok := n.Range()
	if !ok {
		return ""
	}

	start, ok := src.Offset(r.Start)
	if !ok {
		return ""
	}

	end, ok := src.Offset(r.End)
	if !ok {
		return ""
	}

	return text[start:end]
}

// Preamble returns the tokens before the content of the document the Node
// belongs to. They are the comments and %YAML or %TAG directives above
// its "---" header, the header itself, and the comments between the
// header and the first token of the content.
//
// The parser splits the tokens above the header into a node of their own.
// [Source.Documents] folds them back into the document that the YAML spec
// attaches them to. A schema directive written above the header is thus
// in the preamble of the document it describes. A document without
// content, such as one holding comments alone, is all preamble. The slice
// is a copy of shared tokens that a caller must not modify, as for
// [Node.Tokens]. Preamble returns nil for a nil Node.
func (n *Node) Preamble() token.Tokens {
	if n == nil {
		return nil
	}

	return slices.Clone(n.doc.tokens[:n.doc.preamble])
}

// FilePath returns the path of the file the document came from, which is
// [Source.FilePath]. It is absolute for a document [NewSourceFromFile]
// read, so a schema routes on the file and not on how the caller
// spelled its path. It returns an empty string when the source does not
// have a path, and for a nil Node.
//
// The document that [NewSourceFromLayers] builds did not come from a file.
// Its Source has the path of the layer [MergeAs] names, or else of the
// lowest layer. Every Node of it returns that path, whichever layer holds
// its value. [Node.Origin] returns the Node of the value in the file of
// its layer, and FilePath of that Node names the file.
func (n *Node) FilePath() string {
	return n.Source().FilePath()
}

// FS returns the file system that [Node.FilePath] names a file in, which
// is [Source.FS]. It returns nil when the source does not have one, and a
// path then names a file on disk. It also returns nil for a nil Node. A
// Node of the document that [NewSourceFromLayers] builds returns the file
// system of the layer [MergeAs] names, or else of the lowest layer, as
// [Node.FilePath] describes.
func (n *Node) FS() fs.FS {
	return n.Source().FS()
}

// Span returns the lines of [Source.Lines] that the node covers. A whole
// document covers the lines from the one its first token starts on to the
// one before the next document starts. The last document covers the lines
// to the end of the source. The first document also covers the lines
// above its first token, so the spans of a source cover every one of its
// lines. A document without tokens, other than the first, does not cover
// any line, and neither does a nil Node.
//
// A path that selects the root gives the root, so [Node.At] and
// [Node.Nodes] of such a path return the span of the whole document. A
// Node that Node.At or Node.Nodes scopes below the root covers the lines
// from the first token under its node through the last. A scope that does not select
// anything does not cover any line. A scalar covers its own lines alone. A
// mapping or a sequence also covers the line of the key or the "-" that
// introduces it. The span thus holds the token that [Node.Ranges] and an
// error bound at the root of the scope resolve to:
//
//	hours:      # the span of $.hours starts here
//	  open: 9
//
// A path that ends at an alias, such as `$.c` in `c: *x`, covers the
// lines of the alias alone, as a scalar does, wherever its anchor lies.
// The span of `spec:` with `*base` on the next line thus leaves out the
// line of the key, even where [Node.Kind] reports a mapping. Node.Ranges
// and an error at the root of such a Node resolve to the alias.
// [SourceError.Annotate] thus marks the error on [Node.View]. A key or an
// index that the content of the anchor lacks directly below the alias,
// such as a missing required key, binds at the alias too. Annotate marks
// its error on Node.View as well. An error at a node that the content
// holds binds at the anchor, such as one that a decode reports for a
// value of the anchor. A path that the content lacks deeper down binds at
// the nearest node that the content holds, inside the anchor. Annotate
// marks both errors on the view of the Node that [Node.Target] returns.
//
// An error at a Node of the document that [NewSourceFromLayers] builds
// binds in the file of a layer. A caller thus marks the view of its
// [Node.Origin] instead.
//
// [Node.View] returns a view sliced to the span, and the span slices any
// other view over the source, such as one with decoration already:
//
//	lipgloss.Println(p.Print(view.Slice(doc.Span())))
//
// Span is line-granular. [Node.Range] gives the columns of the node, and
// the lines of Span widen to the key that introduces a collection, so
// Span is not Range rounded to lines.
func (n *Node) Span() position.Span {
	if n == nil {
		return position.Span{}
	}

	return n.span
}

// Range returns the range of the text of the node, in the coordinates of
// [Source.Lines], and true. The range runs from the first token of the
// node that [Node.AST] returns through the last of its tokens that holds
// text, without the spaces around either and without comments. For
// `flow: {a: 1, b: 2}`, `$.flow` covers the braces and the text between
// them, and `$.flow.a` covers the `1`. The range of a mapping or a
// sequence thus starts at its first token and not at the key or the "-"
// that introduces it. A comment after its last entry lies outside the
// range. A Node from a path that ends in `~` covers its key. A block
// scalar covers its indicator through the last line of its text.
//
// A value without text of its own, such as the null of `y:` or the text
// of a block scalar without lines, has the empty range at the position of
// its own token. For `y:` that position is the column after the ":", and
// for an element written as `- ` alone, the column after the "-".
//
// A Node from a path that ends at an alias covers the alias, as the
// document writes it at the path, with the tag written on it. For
// `c: *x`, `$.c` covers `*x`, which holds the token that [Node.Ranges]
// highlights. A path that ends in `~` covers a key written as an alias
// the same way. A path through the alias, such as `$.c.b`, covers the
// text at the anchor. So does a path to an entry that a `<<` merge key
// brings in. The key of such an entry lies at the anchor too.
// [Node.Target] gives the Node of the content of the anchor, whose Range
// covers that content.
//
// Range, [Node.Ranges], and [Node.Span] describe one node in three ways.
// Range is its extent. Ranges of a path is what an error at the path
// highlights, which for a mapping or a sequence is the token that
// introduces it. Span is the lines that [Node.View] renders. Span widens
// to the line of the introducing key, and Range starts at the first token
// of the value, so Span is not Range rounded to lines. [Node.KeyRange]
// gives the key of the entry that holds the node:
//
//	key, _ := n.KeyRange()
//	value, _ := n.Range()
//	symbol := Symbol{Range: position.NewRange(key.Start, value.End), SelectionRange: key}
//
// Range reports false for a nil Node, for a document that did not parse
// or that does not hold content, and for a node whose tokens do not carry
// a position.
func (n *Node) Range() (position.Range, bool) {
	if n == nil || n.doc.err != nil {
		return position.Range{}, false
	}

	node := n.AST()
	if !astnode.HasContent(node) {
		return position.Range{}, false
	}

	return n.rangeOf(node)
}

// KeyRange returns the range of the key of the mapping entry whose value
// the node is, as [Node.Range] gives a range, and true. The range leaves
// out the "?" of an explicit key and covers the anchor or the tag written
// on the key. A Node from a path that ends in `~` reports the range of
// the key it selects, which [Node.Range] reports too.
//
// KeyRange reports false for a nil Node and for a document that did not
// parse. The root of a document and an element of a sequence do not have
// a key, so it reports false for them too. It also reports false for a
// key whose tokens do not carry a position.
func (n *Node) KeyRange() (position.Range, bool) {
	if n == nil || n.doc.err != nil || n.base.IsRoot() {
		return position.Range{}, false
	}

	key := n.entryKey(n.base)
	if !astnode.HasContent(key) {
		return position.Range{}, false
	}

	return n.rangeOf(key)
}

// entryKey returns the key of the mapping entry that holds the node at
// path, a `$` path, without the "?" of an explicit key. A path that ends
// in `~` names the entry of its key. It returns nil when no entry holds
// the node.
func (n *Node) entryKey(path paths.Path) ast.Node {
	in := datapath.ParentAt(n.doc.pathResolver(), path.Value())
	if in.Entry == nil {
		return nil
	}

	key := ast.Node(in.Entry.Key)
	if explicit, ok := key.(*ast.MappingKeyNode); ok {
		key = explicit.Value
	}

	return key
}

// rangeOf returns the range of the text of node in the lines of the
// source, as [Node.Range] describes it. It reads the tokens of node that
// carry a position, comments left out, and bounds the range on the first
// and the last of them that hold text. A node without such a token has
// the empty range at the position of its own token.
func (n *Node) rangeOf(node ast.Node) (position.Range, bool) {
	lines := n.source.lines

	var (
		first, last position.Ranges
		lo, hi      int
	)

	for _, tk := range astnode.Tokens(node) {
		ranges := lines.ContentRanges(tk)
		if len(ranges) == 0 {
			continue
		}

		off := tk.Position.Offset

		if first == nil || off < lo {
			first, lo = ranges, off
		}

		if last == nil || off > hi {
			last, hi = ranges, off
		}
	}

	if first != nil {
		return position.NewRange(first[0].Start, last[len(last)-1].End), true
	}

	tk := astnode.Token(node)
	if tk == nil || tk.Position == nil {
		return position.Range{}, false
	}

	at := position.NewFromToken(tk)

	return position.NewRange(at, at), true
}

// View returns a new [*line.View] over the lines of [Source.Lines] that
// the node covers, [Node.Span], with the line numbers they have in the
// file. A document of a file that holds several, or the node a Node from
// [Node.At] selects, thus renders, diffs, or searches on its own:
//
//	result, err := diff.Diff(ctx, before[1].View(), after[1].View())
//
// Each call returns a view of its own without decoration, as [Source.View]
// does, and [line.View.Held] returns its lines on their own. The view
// shares its lines with every view over the source. [SourceError.Annotate]
// thus marks a bound error on it when the location of the error lies in
// the span, and reports false otherwise, as Node.Span describes. A nil
// Node returns a view without lines, as a nil [Source] does.
func (n *Node) View() *line.View {
	if n == nil {
		return n.Source().View()
	}

	return line.NewViewWithin(n.source.lines, n.span)
}

// Ranges returns the ranges of the token that path points at, the token
// that [paths.Resolver.Token] resolves. It returns one range per line the
// token spans, without the spaces around its content. They are the ranges
// that [SourceError.Excerpt] highlights for an [Error] built with [AtPath]
// at that path. The path resolves as it does in such an Error.
//
// A scalar covers every line of its text, and a block scalar covers its
// indicator. A value that holds a tag alone, as in "a: !!null", covers its
// tag. A mapping or a sequence covers the token that introduces it.
// That token is the key of its entry, the "-" of its element in a block
// sequence, or else its own "{" or "[". A block mapping or a block
// sequence at the root covers its first key or element. A path from
// [paths.Path.Key] covers the key of the entry.
//
// A sequence element and the root do not have a key. A path that ends in
// a `~` after either covers what the path before the `~` covers.
//
// An overlay of the ranges highlights the token in a view:
//
//	ranges, err := doc.Ranges(paths.Doc().Child("spec", "replicas"))
//	if err != nil {
//		return err
//	}
//
//	view.AddOverlay(kind.GenericHighlight, ranges...)
//
// To mark every line of a mapping or a sequence, use [Node.Span] or
// [Node.View] of the Node [Node.At] returns. For a Node at an alias, that
// Span covers the alias alone, and the Span of the Node that
// [Node.Target] returns covers the content of the anchor. [Node.Range]
// gives the columns of its text, from its first token through its last.
//
// A path that does not resolve returns the error [paths.Resolver.Token]
// describes, bound to the source as [Node.At] binds it. A token without a
// position returns an error wrapping [ErrNoLocation]. A document that did
// not parse returns the error that [Node.Err] returns. Ranges returns nil
// for a value without text of its own, such as the null of an entry
// without a value.
func (n *Node) Ranges(path paths.Path) (position.Ranges, error) {
	if n.doc.err != nil {
		return nil, n.doc.err
	}

	loc, err := n.pathLocation(path)
	if err != nil {
		return nil, n.bindUnresolved(path, err)
	}

	return loc.highlight(n.source.lines), nil
}

// pathLocation returns the location of the token path resolves to, with
// an `@` path resolving from the scope. A token without a position gives
// [ErrNoLocation].
func (n *Node) pathLocation(path paths.Path) (location, error) {
	tk, err := n.doc.pathResolver().Token(n.base.Join(path))
	if err != nil {
		//nolint:wrapcheck // The paths error already names the path.
		return location{}, err
	}

	if tk == nil || tk.Position == nil {
		return location{}, fmt.Errorf("%w: token at path has no position", ErrNoLocation)
	}

	return location{pos: position.NewFromToken(tk), tk: tk}, nil
}

// nearestLocation returns the location an error binds at when path names
// a node the document does not hold. That location is the nearest node
// the document holds, as [paths.Resolver.Nearest] finds it. A mapping, a
// sequence, or a null binds at the key of its entry, the "-" of its
// element, or the first token of the root. A scalar binds at its own
// token, where an error at its path binds. An alias that the document
// writes at the path of the node binds at the alias, whatever the content
// of its anchor. A Node at the alias covers the alias alone. An element
// or a root that a `~` passes can be an alias that does not resolve, and
// it binds at that alias. It reports true only when reason wraps
// [paths.ErrNotFound] and that token has a position.
func (n *Node) nearestLocation(path paths.Path, reason error) (location, bool) {
	if !errors.Is(reason, paths.ErrNotFound) {
		return location{}, false
	}

	resolver := n.doc.pathResolver()

	near, ok := resolver.Nearest(n.base.Join(path))
	if !ok {
		return location{}, false
	}

	at := near.Key()

	node, err := resolver.Node(near)
	if err == nil && heldScalar(n.doc.content(node)) || n.writesAlias(near) {
		at = near
	}

	tk, err := resolver.Token(at)
	if err != nil || tk == nil || tk.Position == nil {
		return location{}, false
	}

	return location{pos: position.NewFromToken(tk), tk: tk, near: &near}, true
}

// writesAlias reports whether the document of n writes an alias, or a tag
// on one, at path, a `$` path.
func (n *Node) writesAlias(path paths.Path) bool {
	if path.IsRoot() {
		return false
	}

	_, ok := astnode.Content(n.writtenAt(path)).(*ast.AliasNode)

	return ok
}

// heldScalar reports whether content, a mapping, a sequence, or a scalar
// as [document.content] returns it, is a scalar other than a null. An
// error at a path below such a scalar binds at the scalar itself, because
// the scalar is where the path stops.
func heldScalar(content ast.Node) bool {
	switch content.(type) {
	case *ast.MappingNode, *ast.SequenceNode:
		return false
	}

	return !astnode.IsNil(content) && content.Type() != ast.NullType
}

// aliasLocation returns the location of the alias that path cannot follow,
// as reason names it. Its place is the longest path above path, or path
// itself, that points at a token, which is the path that reads the alias.
// A path that can select several nodes, such as one with a `..*`
// selector, reads the alias at the place of the alias in the document, so
// the walk starts from the path that spells that place there. It reports
// false when reason does not name an alias of the document that does not
// resolve.
func (n *Node) aliasLocation(path paths.Path, reason error) (location, bool) {
	unresolved, ok := errors.AsType[*paths.AliasError](reason)
	if !ok || unresolved.Alias == nil {
		return location{}, false
	}

	tk := unresolved.Alias.GetToken()
	if tk == nil || tk.Position == nil {
		return location{}, false
	}

	resolver := n.doc.pathResolver()
	start := n.base.Join(path)

	if start.HasWildcard() {
		spelled, _, ok := n.doc.pathIndex().OwnerPath(resolver, tk)
		if !ok {
			return location{}, false
		}

		start = spelled
	}

	for at, ok := start, true; ok; at, ok = at.Parent() {
		_, err := resolver.Token(at)
		if err == nil {
			return location{pos: position.NewFromToken(tk), tk: tk, near: &at, unfollowed: reason}, true
		}
	}

	return location{}, false
}

// bindUnresolved binds reason, the error path failed to resolve with. A
// missing node binds as [Invalid] at path, so it binds at the nearest node
// the document holds, as [Node.nearestLocation] finds it. Its message is
// the bare [paths.ErrNotFound] text, because the binding writes the path
// in front of it. A document without content adds the
// [paths.ErrNoDocument] text. An alias that the path cannot follow, such
// as one without an anchor or one inside the value of its own anchor,
// binds at the alias, as [Node.bindAlias] binds it. Any other reason binds
// as [Node.bindLookup] binds it.
func (n *Node) bindUnresolved(path paths.Path, reason error) error {
	if bound, ok := n.bindAlias(path, reason); ok {
		return bound
	}

	if !errors.Is(reason, paths.ErrNotFound) {
		return n.bindLookup(reason)
	}

	msg := paths.ErrNotFound.Error()
	if errors.Is(reason, paths.ErrNoDocument) {
		msg += ": " + paths.ErrNoDocument.Error()
	}

	return n.bindOwn(Invalid(&markedError{err: reason, msg: msg}, AtPath(path)))
}

// bindLookup binds reason, the error of a lookup that neither misses a
// value nor stops at an alias that [Node.bindAlias] binds. An alias of the
// document that does not resolve binds as it is. A path that the aliases
// or `<<` merge keys of the document take past a limit binds as [Invalid]
// without a location. The document is at fault for such a path. Any other
// reason, such as a wildcard path that [Node.At] rejects, binds inside
// [Unchecked], because the document is not at fault for it. A validator
// that returns such an error thus does not report a fault.
func (n *Node) bindLookup(reason error) error {
	if errors.Is(reason, paths.ErrAlias) && !errors.Is(reason, paths.ErrReferenceAlias) {
		return n.bindOwn(reason)
	}

	if errors.Is(reason, paths.ErrExcessiveAliasing) || errors.Is(reason, paths.ErrExcessiveMerging) {
		return n.bindOwn(Invalid(reason))
	}

	return n.bindOwn(Unchecked(reason))
}

// bindAlias binds reason, the error that path failed to resolve with, as
// [Invalid] at the alias that reason names, under the path that reads that
// alias, as [Node.aliasLocation] finds it, when the alias lies in the
// document and does not resolve. An alias that reads a reference document
// binds there too, as [Unchecked] binds an error, because the document is
// not at fault for it. The message names path when the alias lies
// elsewhere. It reports false for any other reason.
func (n *Node) bindAlias(path paths.Path, reason error) (error, bool) {
	loc, ok := n.aliasLocation(path, reason)
	if !ok {
		return nil, false
	}

	// The binding writes the path of the alias in front of the message, and
	// the position of the alias comes from the binding.
	msg := reason.Error()

	inner := errors.Unwrap(reason)
	if inner != nil {
		msg = inner.Error()
	}

	if full := n.base.Join(path); !full.Equal(*loc.near) {
		msg = fmt.Sprintf("resolve %s: %s", full, msg)
	}

	// An alias of a reference document is no fault of the document, so its
	// error binds there as one whose check could not run. A validator that
	// returns it thus does not report a fault either.
	locate := Invalid
	if errors.Is(reason, paths.ErrReferenceAlias) {
		locate = Unchecked
	}

	err := locate(&markedError{err: reason, msg: msg}, AtPath(*loc.near), AtPosition(loc.pos))

	return n.bindOwn(err), true
}

// Bind binds err to the source of the document. Each path in err resolves
// in the document of the Node, an `@` path from the scope of the Node and
// a `$` path from the root. Bind resolves every location as it binds, so
// the position that [SourceError.Error] reports and the excerpt that
// [SourceError.Excerpt] returns stay fixed from then on. The Node methods
// bind their errors already. Bind is for errors that other code builds,
// such as the results of checks that callers run on values that they took
// from the document:
//
//	return item.Bind(check(value))
//
// Bind binds the whole tree of err, each problem and detail at its own
// location, as the Errors section of the package documentation describes.
// A Node from [Node.At] or [Node.Nodes] binds a problem without a location
// at its own value, as the Scoped Nodes section describes. The message of
// an error without a location names the source. In a source of several
// documents, it also names the first line of the document and the
// document, as in "name:11: document 3: msg". [SourceError.DocumentSpan]
// reports that line. To keep several errors
// as separate bindings, bind each one before joining them.
//
// Bind returns nil for a nil err, and for a nil [*Error] or
// [*SourceError] pointer, even on a nil Node. Bind returns an already
// bound error unchanged, so Bind is idempotent. The Errors section
// defines a bound error. Bind never modifies err. In a document that did
// not parse, a path does not resolve, and [SourceError.Unresolved]
// returns [ErrPathNeedsDocument] wrapping the error of the parse.
func (n *Node) Bind(err error) error {
	if isNothing(err) {
		return nil
	}

	return bindTree(err, binder{src: n.source, node: n, locate: true})
}

// bindOwn binds an error of a call on n as [Node.Bind] does, except that
// an error without a location does not gain one, even for a scoped Node.
// The exception holds because the error concerns the whole call or path,
// not a value that n scopes.
func (n *Node) bindOwn(err error) error {
	return bindTree(err, binder{src: n.source, node: n})
}

// holdsToken reports whether tk is a token of the parse of the source or
// of the parse the [decodetree.Tree] reads. It compares pointers, so a
// token from another parse, such as one in UnmarshalYAML, never matches.
func (n *Node) holdsToken(tk *token.Token) bool {
	if tk == nil {
		return false
	}

	return n.source.holdsToken(tk) || n.doc.tree().Holds(tk)
}

// holdsToken reports whether tk is a token of the parse of s. It compares
// pointers, as [Node.holdsToken] does, so a nil tk never matches.
func (s *Source) holdsToken(tk *token.Token) bool {
	_, ok := s.parsed.get(s.parse).result.Index[tk]

	return ok
}

// newDecodeTree creates the [*decodetree.Tree] of the document.
func (d *document) newDecodeTree() *decodetree.Tree {
	src := d.node.source

	return decodetree.New(d.root.Body, d.state.Facts(), decodetree.Options{
		Reparse: func() (ast.Node, map[*token.Token]int) {
			r := src.decodeParse.get(src.reparse)
			if d.fileIndex < 0 || d.fileIndex >= len(r.File.Docs) {
				return nil, nil
			}

			return r.File.Docs[d.fileIndex].Body, r.Index
		},
		Texts: func() []string {
			return spellingTexts(d.tokens)
		},
		ReferenceSpellings: src.referenceSpellings,
		References:         len(src.references) > 0,
	})
}

// rejectionText returns the message and the sentinel that
// [rejection.Classify] gives err. It spells each anchor name in the
// message as the document does. It also escapes each control character,
// because the decoder copies document text into messages.
func (d *document) rejectionText(err yaml.Error) (string, error) {
	msg, label := rejection.Classify(err)

	return escape.Control(d.tree().RestoreNames(msg)), rejectionLabel(label)
}

// restoreError returns err with each anchor name the [decodetree.Tree]
// gave spelled as the document spells it. It leaves an [*Error] or a
// [*SourceError] alone, because the binding writes their messages.
func (d *document) restoreError(err error) error {
	var (
		located *Error
		bound   *SourceError
	)

	if errors.As(err, &located) || errors.As(err, &bound) {
		return err
	}

	tree := d.tree()

	msg := err.Error()
	if tree.RestoreNames(msg) == msg {
		return err
	}

	return &namedError{err: err, tree: tree}
}

// namedError is an error whose message spells each anchor name that a
// [decodetree.Tree] gave as the document spells it. It unwraps to the
// error it holds. The binding reads a go-yaml error below it and spells
// the names in the message of the result again.
type namedError struct {
	err  error
	tree *decodetree.Tree
}

func (e *namedError) Error() string {
	return e.tree.RestoreNames(e.err.Error())
}

// Unwrap returns the error e holds.
func (e *namedError) Unwrap() error {
	return e.err
}

// spellingTexts returns the texts that a new anchor name must not spell.
// They are the joined text of tks and the value of each token that holds
// " [". A value differs from its text where a scalar holds an escape or a
// fold.
func spellingTexts(tks token.Tokens) []string {
	var (
		text   strings.Builder
		values []string
	)

	for _, tk := range tks {
		if tk == nil {
			continue
		}

		text.WriteString(tk.Origin)

		if strings.Contains(tk.Value, " [") {
			values = append(values, tk.Value)
		}
	}

	return append(values, text.String())
}

// PathAt returns the `$` path of the innermost node of the document at
// pos, and true. The position is in the coordinates of [Source.Lines], as
// a cursor in a viewer, a [go.jacobcolvin.com/niceyaml/finder.Finder]
// match, or [SourceError.Position] gives one. A viewer thus names the
// value under its cursor:
//
//	if path, ok := doc.PathAt(cursor); ok {
//		status = path.String() // $.spec.containers[0].image
//	}
//
// A caller can pass the path back to [Node.At] or [Node.Ranges] of any
// Node of the document. PathAt answers within the scope of the Node, as
// [Node.NodeAt] does. A Node from [Node.At] gives the paths of the nodes
// it holds, its own key included, and reports false elsewhere in the
// document. A caller that holds such a Node and wants the path anywhere in
// the document asks the root:
//
//	path, ok := node.Document().PathAt(cursor)
//
// The innermost node is an entry of a mapping or an element of a
// sequence. A position on the key of an entry gives a path that ends in
// `~`, as [paths.Path.Key] writes one. A position on the ":" or the value
// gives the path of the entry. The "-" of a block sequence belongs to its
// element, and the "[", "{", ",", anchor, or tag of a collection belongs
// to the collection.
//
// A position in the indentation of a line resolves to the token after it.
// The lexer gives the spaces between two tokens on a line to one of them.
// PathAt resolves a position in the spaces that lead the text of a token
// to that token, and reports false in the spaces that trail the text of a
// token. The path names where the document writes the node. A position
// on an alias thus gives the entry that holds the alias, and a position
// inside an anchor gives the path where the anchor defines it.
//
// PathAt reports false on a comment, a blank line, a line of spaces
// alone, a "---" header, a "..." marker, or a directive. It also reports
// false past the end of a line, as at the end of `y:`. [Node.NodeAt]
// answers there with the node that encloses the position. PathAt reports
// false outside the lines of the source and in another document of the
// source. [Source.DocumentAt] picks the document of a position. PathAt
// reports false for a document that did not parse or that does not hold
// content. It reports false inside an entry that paths cannot select,
// such as the earlier of two entries with one key. Every path it returns
// selects the node at pos and does not hold a wildcard selector.
//
// The position of a node from [Node.AST] or [paths.Resolver] does not
// name that node in every case. The parser gives a block mapping the
// token of its first entry, such as its ":", and a block sequence the "-"
// of its first element. PathAt thus gives the path of that entry or element at
// such a position. [Node.PathOf] returns the path of the node itself. A
// caller that visits every value of a document asks [Node.Nodes] for
// them, as with `doc.Nodes(paths.Doc().RecursiveAll())`, and each Node
// it returns carries its path.
func (n *Node) PathAt(pos position.Position) (paths.Path, bool) {
	if n == nil || n.doc.err != nil {
		return paths.Path{}, false
	}

	tk := contentTokenAt(n.source.lines, pos)
	if tk == nil {
		return paths.Path{}, false
	}

	path, ok := n.doc.pathIndex().PathAt(n.doc.pathResolver(), tk.Position.Offset)
	if !ok {
		return paths.Path{}, false
	}

	if _, under := path.CutPrefix(n.base); !under {
		return paths.Path{}, false
	}

	return path, true
}

// PathOf returns the `$` path of node, a go-yaml node of the document,
// and true. The node comes from the tree of the document, such as from
// [Node.AST], [Source.File], or a [paths.Resolver] lookup, or from a field
// of type [ast.Node] that a decode of the document fills. A caller can
// pass the path to [Node.At] for the Node of the value, or to [AtPath]
// for an error there:
//
//	sources, _ := doc.DocumentResolver().MergeSources(merged.AST())
//	for _, src := range sources {
//		path, ok := doc.PathOf(src) // $.base, the mapping that "<<" reads
//		// ...
//	}
//
// The path names where the document writes the node, as [Node.PathAt]
// does, and holds no wildcard selector. A mapping or a sequence, in block
// or flow style, gives the path of the entry or element that holds it.
// The body of the document gives the root path. An anchor, a tag, or
// an alias gives the path of the entry or element that writes it. The
// content that an alias reads thus gives the path where its anchor
// defines it. An alias that does not resolve, such as one before its
// anchor, also gives its path, as PathAt gives it. [Node.At] reports a
// [*paths.AliasError] at that path, and [AtPath] binds an error there.
// A key gives the path of its entry with a trailing `~`, as
// [paths.Path.Key] writes one. An [*ast.MappingValueNode] gives the path
// of its entry, and so does the null that the parser makes for a value
// that the entry leaves out. An element works the same way.
//
// PathOf answers within the scope of the Node, as PathAt does. It reports
// false for a nil Node or node, for a comment, and for an
// [*ast.DocumentNode], whose body gives the root path. It also reports
// false for a node whose path does not select it, such as one inside the
// earlier of two entries with one key. A document that did not parse and
// a node outside the document report false too.
//
// PathOf finds the node by the offset of its token and by its type. It
// thus finds a node of a second parse of the same text, such as the one
// that a decode might read. It cannot tell a node of another text apart.
// Such a node gets the path of whatever node lies at its offset in the
// document.
//
// In one case two nodes of a parse share a type and a position. An entry
// with a "?" key that holds a null, as `? !!null` writes one, leaves out
// its value, and another entry follows it. PathOf tells the two nulls
// apart for a node of the document. For a node of a second parse, the
// null of the key gets the path of its entry, and not the path that ends
// in `~`.
func (n *Node) PathOf(node ast.Node) (paths.Path, bool) {
	if n == nil || n.doc.err != nil {
		return paths.Path{}, false
	}

	path, ok := n.doc.pathIndex().NodePath(n.doc.pathResolver(), node)
	if !ok {
		return paths.Path{}, false
	}

	if _, under := path.CutPrefix(n.base); !under {
		return paths.Path{}, false
	}

	return path, true
}

// NodeAt returns the innermost [*Node] of the document that holds pos,
// and true. The position is in the coordinates of [Source.Lines], as for
// [Node.PathAt]. Where PathAt names the token under a cursor, NodeAt
// names the node to complete in, so it answers at the positions a user
// types at:
//
//	if n, ok := doc.NodeAt(cursor); ok {
//		keys := completions(n.Path()) // $.server on its blank line, $.y after "y:"
//	}
//
// The node is an entry of a mapping or an element of a sequence, as
// [Node.At] scopes it, or the root of the document. A position on the
// key, the ":", or the value of an entry gives the entry. A position
// between the entries of a collection gives the collection, as the "{",
// the "," and the "}" of `{a: 1, b: 2}` do. A position in a comment, in
// the spaces after the last text of a line, or past the end of the line
// belongs to the node of the last text before it. The end of `y:` thus
// gives `$.y`. A blank line or a line of spaces alone belongs
// to the innermost node whose [Node.Span] holds the line, whatever its
// column. When no node spans the line, it belongs to the collection that
// holds the last entry before it. A blank line after `host: a` under
// `server:` thus gives `$.server`, and a caller that wants a wider node
// walks up with [paths.Path.Parent]. A line before the first entry of the
// document, or one that no entry holds, gives the root.
//
// A position that lies inside an entry that paths cannot select, such as
// the earlier of two entries with one key, gives the nearest node around
// it that a path selects. A Node from [Node.At] answers for the positions
// of the nodes it holds, and reports false elsewhere in the document, as
// Node.PathAt does. A position that the root path holds gives the root
// that [Node.Document] returns, as Node.At gives it.
//
// NodeAt reports false for a nil Node, for a document that did not parse
// or that does not hold content, and for a position outside the lines of
// the document. [Source.DocumentAt] picks the document of a position in a
// Source that holds several.
func (n *Node) NodeAt(pos position.Position) (*Node, bool) {
	if n == nil || n.doc.err != nil || n.doc.node.IsEmpty() || pos.Col < 0 {
		return nil, false
	}

	root := n.doc.node
	if !root.span.Contains(pos.Line) {
		return nil, false
	}

	path, ok := root.pathHolding(pos)
	if !ok {
		return nil, false
	}

	if _, under := path.CutPrefix(n.base); !under {
		return nil, false
	}

	node, err := n.At(path)
	if err != nil {
		return nil, false
	}

	return node, true
}

// pathHolding returns the `$` path of the node that [Node.NodeAt] finds
// for pos in the document of n, the root of its document.
func (n *Node) pathHolding(pos position.Position) (paths.Path, bool) {
	lines := n.source.lines
	index, r := n.doc.pathIndex(), n.doc.pathResolver()

	if tk := contentTokenAt(lines, pos); tk != nil {
		return index.HolderPath(r, tk.Position.Offset), true
	}

	if tk := lastContentTokenOn(lines.Line(pos.Line), pos.Col); tk != nil {
		return index.HolderPath(r, tk.Position.Offset), true
	}

	// The line holds no text before pos. Find the node of the last text
	// on an earlier line of the document. That node holds the line when
	// its own lines run past it, as the lines of a collection or a block
	// scalar do. Otherwise the collection that holds that node holds the
	// line.
	for i := pos.Line - 1; i >= n.span.Start; i-- {
		tk := lastContentTokenOn(lines.Line(i), lines.Line(i).Width())
		if tk == nil {
			continue
		}

		path := index.HolderPath(r, tk.Position.Offset)

		before, err := n.At(path)
		if err != nil {
			return paths.Path{}, false
		}

		if before.span.Contains(pos.Line) {
			return path, true
		}

		parent, _ := path.Parent()

		return parent, true
	}

	return paths.Doc(), true
}

// tokenPathAt returns the path of the node that holds the token at pos,
// and true, as [Node.PathAt] resolves the start of that token. The lexer
// bundles the spaces after the text of a token into the token. PathAt
// reports false in those spaces, but tokenPathAt resolves them to the
// token. A position in the space after a tag thus names the value of the
// tag. It reports false where no token holds pos, on a comment, and where
// PathAt reports false at the start of the token.
func (n *Node) tokenPathAt(pos position.Position) (paths.Path, bool) {
	if n == nil || n.doc.err != nil {
		return paths.Path{}, false
	}

	tk := n.source.lines.TokenAt(pos)
	if tk == nil || tk.Position == nil || tk.Type == token.CommentType {
		return paths.Path{}, false
	}

	return n.doc.pathIndex().PathAt(n.doc.pathResolver(), tk.Position.Offset)
}

// contentTokenAt returns the token of lines whose text holds pos, or
// whose text follows pos on its line, as [Node.PathAt] resolves a
// position. It returns nil for a position on a comment or outside the
// lines. It also returns nil in the spaces after the last text of a line,
// past the end of the line, and on a line without text.
func contentTokenAt(lines line.Lines, pos position.Position) *token.Token {
	tk := lines.TokenAt(pos)
	if tk == nil || tk.Position == nil || tk.Type == token.CommentType {
		return nil
	}

	sp, ok := lines.Line(pos.Line).ContentSpan(tk)
	if !ok || sp.Len() == 0 || pos.Col >= sp.End {
		return nil
	}

	return tk
}

// lastContentTokenOn returns the last token of l that holds text before
// column col, comments left out, or nil when no token does.
//
// It starts at the column before col and steps back one token at a time
// until a token holds such text. It thus looks up only the tokens between
// that text and col, however many tokens the line holds.
func lastContentTokenOn(l *line.Line, col int) *token.Token {
	for c := min(col, l.Width()) - 1; c >= 0; {
		tk := l.TokenAt(c)

		if tk != nil && tk.Position != nil && tk.Type != token.CommentType {
			if sp, ok := l.ContentSpan(tk); ok && sp.Len() > 0 && sp.Start < col {
				return tk
			}
		}

		c = firstColumnOf(l, c, tk) - 1
	}

	return nil
}

// firstColumnOf returns the first column of the run of columns of l that
// ends at col and that [line.Line.TokenAt] resolves to tk, the token at
// col. The run holds the columns of tk up to col and the indentation
// before tk. In indentation that text does not follow, tk is nil and the
// run holds that indentation.
//
// It compares the tokens themselves, so it tells tk from another token of
// the line with the same type, value, origin, and position.
// [line.Line.TokenSpan] reports the columns of the first such token, which
// can sit far before col.
func firstColumnOf(l *line.Line, col int, tk *token.Token) int {
	// The columns before the run resolve to the tokens before tk, so a
	// search finds the first column of the run. The search does not step
	// through each column of a long token or of a wide indentation.
	return sort.Search(col, func(c int) bool { return l.TokenAt(c) == tk })
}

// NodeKind is the structural kind of the value a [Node] holds, as
// [Node.Kind] reports it.
type NodeKind int

// [NodeKind] constants.
const (
	// NodeNone is the kind of a Node that does not hold a value. Such a
	// Node is the root of an empty document, of a document of comments or
	// directives alone, or of a document that did not parse, or a nil Node.
	// The root of a document that is an alias without an anchor before it
	// or in its reference documents has this kind too. [Node.IsEmpty]
	// reports the documents among these that do not have content, and
	// [Node.Err] tells a document that did not parse apart from an empty
	// one.
	NodeNone NodeKind = iota
	// NodeMapping is the kind of a mapping, block or flow.
	NodeMapping
	// NodeSequence is the kind of a sequence, block or flow.
	NodeSequence
	// NodeScalar is the kind of a scalar, a null included, whatever its
	// tag.
	NodeScalar
)

// String returns "none" for [NodeNone], "mapping" for [NodeMapping],
// "sequence" for [NodeSequence], "scalar" for [NodeScalar], and
// "NodeKind(n)" for any other value n.
func (k NodeKind) String() string {
	switch k {
	case NodeNone:
		return "none"
	case NodeMapping:
		return "mapping"
	case NodeSequence:
		return "sequence"
	case NodeScalar:
		return "scalar"
	default:
		return "NodeKind(" + strconv.Itoa(int(k)) + ")"
	}
}

// Kind reports whether the Node holds a mapping, a sequence, or a
// scalar, or [NodeNone] when it does not hold a value. A caller that
// walks a document uses the kind to pick the selector that lists the
// children of the Node:
//
//	var children []*niceyaml.Node
//
//	switch n.Kind() {
//	case niceyaml.NodeMapping:
//		children, err = n.Nodes(paths.Current().ChildAll())
//	case niceyaml.NodeSequence:
//		children, err = n.Nodes(paths.Current().IndexAll())
//	}
//
// Such a walk follows every alias, so it calls [Node.CheckAliasLimit] on
// the Node it starts from before it reads any children.
//
// Kind looks through the anchor and the tag on the node, an alias and the
// anchor it refers to, and the `?` of an explicit key. The root of
// `&r {a: 1}` and of `!!map {a: 1}` is thus a mapping, but [Node.AST] gives
// the anchor or the tag that wraps it. A decode reads the value under a tag
// such as !!str or !!null as a scalar, so "!!str *k" and "!!null *k" are
// scalars when k is a sequence. A Node at an alias reports the kind of the
// content of its anchor, as a decode reads it, and that anchor can lie in
// a reference document of [WithReferences]. [NodeKind] thus does not have
// a kind for an alias, and [Node.Target] tells a Node at an alias apart.
// A Node at an alias inside the content of its own anchor holds a value
// that a decode reads as null, so Kind reports [NodeScalar]. The root of
// a document that is an alias without an anchor before it or in its
// reference documents does not hold a value.
//
// Every scalar has the same kind, [NodeScalar]. The type that a scalar
// decodes to depends on its tag, its text, and the Go type that a decode
// fills. A caller that needs that type decodes the Node.
func (n *Node) Kind() NodeKind {
	if n == nil || n.doc.err != nil {
		return NodeNone
	}

	node := n.valueNode()
	if !astnode.HasContent(node) {
		return NodeNone
	}

	switch n.doc.content(node).(type) {
	case nil:
		return NodeNone
	case *ast.MappingNode:
		return NodeMapping
	case *ast.SequenceNode:
		return NodeSequence
	default:
		return NodeScalar
	}
}

// CheckAliasLimit returns the error that a decode of the Node into a type
// that does not read text returns for the alias limit. It returns nil when
// the limit lets such a decode read the Node. A caller that walks the Node
// with [Node.Kind] and [Node.Nodes] follows every alias. Each call of
// Nodes stays within the limits of its own path,
// so such a walk of a document whose aliases nest can run for minutes
// without an error. A walk that follows aliases calls CheckAliasLimit
// first:
//
//	if err := n.CheckAliasLimit(); err != nil {
//		return err
//	}
//
// The error matches [ErrExcessiveAliasing], and [IsInvalid] reports it. It
// binds at the alias of the document that repeats the most nodes, as the
// Alias Limit section of the package documentation describes. The count
// covers the whole document, so a Node that holds one small alias in a
// document past the limit fails. The document keeps the count, so a check
// of each item of a list counts the document once.
//
// CheckAliasLimit returns nil for a Node whose value does not hold an
// alias and for a nil Node. It also returns nil for each Node of a
// [Source] with [WithAliasLimit] set to false. For a document that did not
// parse, it returns the error that [Node.Err] returns.
//
// A decode into a type that reads text also counts the text that the
// aliases write out, as the Decoding section of the package documentation
// describes. That decode fails when aliases make up too much of the text.
// It also fails when the text passes the document byte limit or the depth
// limit of the Source. The check leaves out the whole count of the text,
// so it can return nil for a Node that such a decode refuses.
//
// [Node.Validate] does not run the check. A validator that reads tokens or
// a `$..*` path does not follow an alias, so an editor or a linter can
// still report on a document past the limit.
func (n *Node) CheckAliasLimit() error {
	if n == nil {
		return nil
	}

	if n.doc.err != nil {
		return n.doc.err
	}

	err := aliasing.CheckDecode(decodedValue{n})
	if err != nil {
		return n.aliasingError(err)
	}

	return nil
}

// Scalar returns the text of the scalar of the Node as the document
// writes it, and reports whether the Node holds a scalar with a text. Use
// Scalar for the text that the author typed, such as a version or a file
// mode, and [Node.Decode] for the value that YAML reads from that text:
//
//	n, err := doc.At(paths.Doc().Child("python"))
//	if err != nil {
//		return err
//	}
//
//	text, ok := n.Scalar()
//
// The text is the string that a decode reads from the scalar before a tag
// converts it. So python: 3.10 gives "3.10", mode: 0755 gives "0755", and
// hex: 0x10 gives "0x10", where a Decode[string] gives "3.1", "493", and
// "16". A tag does not change the text, so !!int "0x10" gives "0x10" too,
// where a Decode[string] gives "0". Scalar resolves the quotes, the
// escapes, and the folding of the scalar, so `image: "nginx:1.25"` gives
// nginx:1.25 without its quotes.
// [Node.Text] returns the scalar with its quotes, as the source writes it.
// A block scalar gives the lines after its header, such as "|" or ">-",
// with the indentation, the folding, and the chomping of the block
// applied, so a `|` scalar that holds 1.10 gives "1.10\n".
//
// Scalar reads through anchors, tags, and aliases as [Node.Kind] and a
// decode do. An alias thus gives the text of the scalar that its anchor
// holds, in the document or in a reference document of [WithReferences].
// A written null gives its text, so `~` gives "~" and null gives "null".
// A tag can convert a mapping or a sequence to a scalar, as "!!str *k"
// does for a sequence k. Such a tag has no text of its own, so Scalar
// prints the value that a decode reads from it, as [fmt.Sprint] prints
// it. So "!!str *k" gives "[x y]" for the sequence [x, y], and "!!int *k"
// gives "0". Scalar reports false when that decode fails, as it does for
// "!!bool *k", or reads a null, as it does for "!!null *k".
//
// Scalar reports false for a nil Node and a Node whose document did not
// parse. It also reports false for a mapping, a sequence, and an entry or
// an item without a value. A tag without a scalar under it, as in
// `key: !!str`, reports false, though a decode reads "" from it. An alias
// that does not resolve and an alias inside the content of its own anchor
// report false too. The
// [go.jacobcolvin.com/niceyaml/matcher.Text] matcher reads a scalar by a
// rule of its own, as its documentation describes.
//
//nolint:contextcheck // The decode of a converted collection reads no state that a context carries.
func (n *Node) Scalar() (string, bool) {
	if n == nil || n.doc.err != nil {
		return "", false
	}

	node := n.valueNode()
	if !astnode.HasContent(node) {
		return "", false
	}

	content := n.doc.content(node)
	if _, ok := content.(*ast.TagNode); !ok {
		return astnode.ScalarText(content)
	}

	v, err := n.Decode[any](context.Background())
	if err != nil || v == nil {
		return "", false
	}

	if s, ok := v.(string); ok {
		return s, true
	}

	return fmt.Sprint(v), true
}

// content returns the mapping, sequence, or scalar under node, as
// [datapath.Deref] finds it, except that it follows an alias that reads a
// reference document to the content of the anchor there. A tag that
// converts a collection under it, as "!!str *k" does for a sequence k,
// holds a scalar, as [astnode.Converted] reads such a tag, so content
// returns the tag there. The decoder reads an alias inside the content of
// the anchor it refers to as null, so content returns such an alias as a
// scalar. It returns nil for an alias that does not resolve, and for an
// alias it reaches a second time.
func (d *document) content(node ast.Node) ast.Node {
	return d.contentAfter(node, nil)
}

// contentAfter returns the content of node as [document.content] does,
// after the aliases of followed on the way to node.
func (d *document) contentAfter(node ast.Node, followed []*ast.AliasNode) ast.Node {
	for {
		if tag, ok := astnode.Converted(node).(*ast.TagNode); ok {
			inner := d.contentAfter(tag.Value, followed)
			if _, ok := inner.(*ast.MappingNode); ok {
				return tag
			}

			if _, ok := inner.(*ast.SequenceNode); ok {
				return tag
			}

			return inner
		}

		node = astnode.Content(node)

		alias, ok := node.(*ast.AliasNode)
		if !ok {
			return node
		}

		switch {
		case d.state.Facts().Enclosed[alias]:
			return alias
		case slices.Contains(followed, alias):
			return nil
		}

		followed = append(followed, alias)

		target, err := d.aliasContent(alias)
		if err != nil {
			return nil
		}

		node = target
	}
}

// aliasContent returns the node that the anchor of alias holds, where
// alias belongs to the document or to one of its reference documents. An
// alias that reads a reference document reads the anchor that the
// reference documents hold for its name, as a decode does. The node can
// hold another alias, such as one inside the value of its own anchor, as
// in "&k !!str *k", which a decode reads as null. Returns the error of
// [paths.Resolver.Anchor] for an alias that does not find an anchor.
func (d *document) aliasContent(alias *ast.AliasNode) (ast.Node, error) {
	anchor, err := d.aliasAnchor(alias)
	if err != nil {
		return nil, err
	}

	return anchor.Value, nil
}

// aliasAnchor returns the anchor that alias reads, as
// [document.aliasContent] finds it. The anchor lies in the document or in
// one of its reference documents.
func (d *document) aliasAnchor(alias *ast.AliasNode) (*ast.AnchorNode, error) {
	anchor, err := d.pathResolver().Anchor(alias)
	if errors.Is(err, paths.ErrReferenceAlias) {
		name, _ := astnode.Name(alias.Value)

		if ref, ok := d.node.source.referenceAnchors().Anchor(name); ok {
			return ref, nil
		}
	}

	if err != nil {
		//nolint:wrapcheck // The paths error names the alias.
		return nil, err
	}

	return anchor, nil
}

// Target returns the [*Node] of the content of the anchor that the Node
// reads, for a Node at an alias. A Node that [Node.At] or [Node.Nodes]
// scopes at an alias describes the alias as the document writes it, as
// [Node.AST] describes. Target describes the content where the anchor
// defines it. [Node.Span], [Node.View], [Node.Tokens], and [Node.AST] of
// the result describe that place, and [Node.Path] of the result is the
// path that spells the anchor:
//
//	base, err := c.Target()
//	if err != nil {
//		return err
//	}
//
//	if base != c {
//		fmt.Println(base.Path()) // $.base, where &x defines the content of *x
//	}
//
// Target returns the receiver for a Node that is not at an alias, so a
// result other than the receiver tells that the Node is at one. A tag on
// the alias does not change the result. Target follows one alias, so the
// result is the Node where the document writes the anchor. An alias
// inside the content of its own anchor thus gives the Node of that
// anchor, though a decode reads such an alias as null.
//
// An anchor on an entry that a later entry with the same key hides, as
// [WithAllowDuplicateKeys] allows, gives a Node of the content of that
// anchor. Its Path is the path that spells the anchor, which selects the
// later entry or a node below it, or no node at all. An error bound at
// the root of the Node and an `@` path from the Node resolve by that
// path, not at the hidden content. So such an error lies outside the
// View of the Node. A decode of the Node reads the hidden content, and an
// error that rejects that content binds there.
//
// An alias that reads a reference document of [WithReferences] gives a
// Node of the reference Source, as [Node.Origin] gives a Node of the
// Source of a layer. An error at that Node binds in the reference Source.
//
// Target returns an error that wraps [paths.ErrAlias] for an alias that
// does not resolve, such as the root of a document that is an alias
// without an anchor. A document that did not parse returns the error
// that [Node.Err] returns, and a nil Node returns nil and a nil error.
func (n *Node) Target() (*Node, error) {
	if n == nil {
		return nil, nil //nolint:nilnil // A nil Node is not at an alias.
	}

	if n.doc.err != nil {
		return nil, n.doc.err
	}

	alias, ok := astnode.Content(n.AST()).(*ast.AliasNode)
	if !ok {
		return n, nil
	}

	return n.target(alias)
}

// target returns the Node of the content of the anchor that alias reads,
// as [Node.Target] describes it. The error comes back bound to the source
// of n.
func (n *Node) target(alias *ast.AliasNode) (*Node, error) {
	anchor, err := n.doc.aliasAnchor(alias)
	if err != nil {
		return nil, n.bindUnresolved(paths.Current(), err)
	}

	tk := anchor.GetToken()

	doc := n.doc.node
	if !n.doc.holds(tk) {
		doc = n.source.referenceDocumentHolding(tk)
	}

	// Every anchor of a document that parsed lies in a document that the
	// Source holds, at a path that spells it.
	if doc == nil {
		return nil, n.bindOwn(fmt.Errorf("%w: no document holds the anchor of %s", paths.ErrAlias, alias))
	}

	index := doc.doc.pathIndex()

	path, selects, spelled := index.OwnerPath(doc.doc.pathResolver(), tk)

	switch {
	case selects:
		return doc.At(path)
	case spelled:
		// The path selects another node, such as a later entry with the
		// same key, so the Node scopes the content of the anchor itself.
		return doc.scopedBy(path, anchor.Value, func() *token.Token { return index.OwnerToken(tk) }), nil
	default:
		return nil, n.bindOwn(fmt.Errorf("%w: no path spells the anchor of %s", paths.ErrAlias, alias))
	}
}

// readsRoot reports whether tk, a token that a decode of n rejects, is
// the token of the content that n reads through a local alias. Such a
// Node is at an alias whose anchor the document of n defines, and tk
// belongs to the node that the anchor holds, not to a node below it.
func (n *Node) readsRoot(tk *token.Token) bool {
	if n.base.IsRoot() || n.value == n.node || !n.holdsToken(tk) {
		return false
	}

	alias, ok := astnode.Content(n.node).(*ast.AliasNode)
	if !ok {
		return false
	}

	anchor, err := n.doc.aliasAnchor(alias)
	if err != nil || !n.doc.holds(anchor.GetToken()) {
		return false
	}

	index := n.doc.pathIndex()
	owner := index.OwnerToken(tk)

	return owner != nil && owner == index.OwnerToken(anchor.GetToken())
}

// holds reports whether tk is a token of the tree of the document, as the
// parse of its Source copies one.
func (d *document) holds(tk *token.Token) bool {
	if tk == nil || tk.Position == nil {
		return false
	}

	src := d.node.source
	if _, ok := src.parsed.get(src.parse).result.Index[tk]; !ok {
		return false
	}

	all := d.positioned
	off := tk.Position.Offset

	for i := sort.Search(len(all), func(i int) bool { return all[i].Position.Offset >= off }); i < len(all); i++ {
		if all[i].Position.Offset != off {
			break
		}

		if segment.SameToken(all[i], tk) {
			return true
		}
	}

	return false
}

// IsEmpty reports whether the Node is the root of a document that parsed
// and does not hold content. Such a document is an empty file, a file of
// whitespace or comments alone, or a "---" header with only comments
// below it. A %YAML or %TAG directive above the header does not add
// content.
//
// IsEmpty describes the document and not a value in it. A document that
// holds any value is not empty. IsEmpty is thus false for an empty
// mapping `{}`, an empty sequence `[]`, an empty string `""`, an explicit
// null such as `~`, and an alias, even one without an anchor before it. A
// caller that asks whether a mapping or a sequence holds anything lists
// its entries with [Node.Nodes].
//
// IsEmpty is false for a document that did not parse, for a Node that
// [Node.At] or [Node.Nodes] scopes below the root, and for a nil Node.
// [Node.Kind] is [NodeNone] for every Node that IsEmpty reports. It is also
// [NodeNone] for the root of a document that did not parse or that is an
// alias without an anchor before it or in its reference documents. A caller
// that skips empty documents thus asks IsEmpty. [SkipEmpty] wraps a
// [Validator] so that it passes an empty document.
func (n *Node) IsEmpty() bool {
	if n == nil || n.doc.err != nil || !n.base.IsRoot() {
		return false
	}

	return !astnode.HasContent(n.doc.root.Body)
}

// resolveLocation resolves path, which starts at `$`, in the document of
// n, as [locatePath] describes for the document an error binds in.
func (n *Node) resolveLocation(path paths.Path) (location, error) {
	root := n.doc.node

	if root.doc.err != nil {
		return location{}, fmt.Errorf("%w: %s: %w", ErrPathNeedsDocument, path, root.doc.err)
	}

	loc, err := root.pathLocation(path)
	if err != nil {
		if near, ok := root.nearestLocation(path, err); ok {
			return near, nil
		}

		// A key that a `<<` merge key brings in from a reference document
		// binds at the alias of the merge key, as any alias that a path
		// cannot follow binds. An alias of a merge key that does not resolve
		// might set any key or might not set one, so a path below the
		// mapping that holds it does not bind.
		alias, ok := root.aliasLocation(path, err)
		if ok && (errors.Is(err, paths.ErrReferenceAlias) || !root.mergedBy(alias.tk)) {
			return alias, nil
		}

		return location{}, err
	}

	return loc, nil
}

// mergedBy reports whether tk is the token of an alias under a `<<` merge
// key of the document.
func (n *Node) mergedBy(tk *token.Token) bool {
	for _, a := range n.doc.state.Facts().Aliases {
		if a.Node.GetToken() == tk {
			return a.Merge
		}
	}

	return false
}

// merges reports whether n is a Node of a [NewSourceFromLayers] document,
// whose errors bind in the layers. A nil n is not such a Node.
func (n *Node) merges() bool {
	return n != nil && n.source.layers != nil
}

// anchorToken returns the token that locates the document. That token is
// its header, or else the first token of its content past the preamble,
// or else its first token. It is nil when the document does not have a
// token.
func (d *document) anchorToken() *token.Token {
	if d.root.Start != nil {
		return d.root.Start
	}

	if d.preamble < len(d.tokens) {
		return d.tokens[d.preamble]
	}

	if len(d.tokens) > 0 {
		return d.tokens[0]
	}

	return nil
}
