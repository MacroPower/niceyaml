package niceyaml

import (
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
	"go.jacobcolvin.com/niceyaml/internal/decodewalk"
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
// [*Node], or nil for a nil Node. Other packages of the module call it as
// [aliasing.StateOf].
func documentState(n any) *aliasing.State {
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
// one names that cause.
func (d *document) newResolver() *paths.Resolver {
	if d.err != nil {
		return paths.NewResolver(nil)
	}

	src := d.node.source
	if len(src.references) == 0 {
		return paths.NewResolver(d.root)
	}

	// The table of the reference anchors builds on the first alias that
	// lacks an anchor, so a path that does not meet one does not read the
	// reference documents.
	return paths.NewResolver(d.root, paths.WithReferenceAnchors(src.referenceValue))
}

// newPathEntries creates new [decodewalk.PathEntries] for the document.
// They read the anchors of the reference documents of the Source. They do
// not read a merge source through an alias of a document past the alias
// limit, as [document.pastAliasLimit] reports it. They keep the keys of
// each merge source that they read on the Source, as [Source.mergeKeys]
// describes, or on the reference table for an anchor of a reference
// document, as [Node.referenceAnchorKeys] describes.
func (d *document) newPathEntries() *decodewalk.PathEntries {
	return decodewalk.NewPathEntries(decodewalk.Rules{
		Resolver:       d.pathResolver(),
		Anchor:         d.node.referenceAnchor,
		AnchorKeys:     d.node.referenceAnchorKeys,
		PastAliasLimit: d.pastAliasLimit,
		Keys:           &d.node.source.mergeKeys,
	})
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
// same lines whether the value is the whole document or one inside it.
//
// Receive instances from [Source.Documents], [Source.AllDocuments],
// [Source.Document], [Node.At], [Node.Nodes], [Node.Document],
// [SourceError.Node], or [SourceError.Document].
//
// The Decoding section of the package documentation describes the steps
// a decode runs. The Scoped Nodes section describes what a Node from
// [Node.At] or [Node.Nodes] reports. [Node.DocumentAST],
// [Node.DocumentIndex], [Node.Preamble], [Node.FilePath], and
// [Node.PathAt] describe the document as a whole, whatever Node of it a
// caller holds.
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
	// The node the scope selects, which At or Nodes resolves once when it
	// scopes the Node. The root of a document leaves it unset, because its
	// node is the body of the document.
	node   ast.Node
	source *Source
	// The enclosing document.
	doc *document
	// The tokens of the node: the tokens of the whole document for its
	// root, and a sub-slice of them for a Node from At.
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
// returns nil for it, as [Node.Err] describes.
func (n *Node) DocumentAST() *ast.DocumentNode {
	if n.doc.err != nil {
		return nil
	}

	return n.doc.root
}

// Err returns the error of a document that did not parse, or nil for a
// document that parsed and for a nil Node. The error matches [ErrSyntax] for
// text that the parser rejects. It matches [ErrLimit] for a document past the
// depth limit or a text past the byte limit of the Source. A syntax error
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

// Resolver returns the [*paths.Resolver] of the whole document the Node
// belongs to, which every Node of the document shares. It resolves every
// path from the document root, whether it starts at `$` or `@`, as
// [paths.Resolver] describes. A path below the Node therefore starts with
// [Node.Path], which [paths.Path.Join] puts in front of an `@` path. The
// resolver follows the aliases and merge keys of the go-yaml nodes that
// [Node.AST] and [Node.DocumentAST] return.
//
// The document creates the resolver once, when the first of its Nodes
// needs it. The resolver binds the aliases of the document once, when a
// path first needs them. A [Validator] that runs for each item of a list
// therefore binds them once, but a resolver from [paths.NewResolver] binds
// them again for every item.
//
// The resolver knows the anchors of the reference documents of
// [WithReferences], as [paths.WithReferenceAnchors] describes. A path that
// ends at an alias that reads one of them selects the alias. A node that
// [paths.Resolver.Node] or [paths.Resolver.Matches] returns can thus be
// such an alias, or the tag on one, rather than the content of its anchor.
// A path through the alias returns an error that wraps
// [paths.ErrReferenceAlias]. A resolver from paths.NewResolver does not
// know those anchors, so it fails at the alias with an error that names an
// alias without an anchor.
//
// A document that did not parse does not have a tree, as [Node.Err]
// describes. Its resolver is thus the one that [paths.NewResolver] creates
// for a nil document. Every path it resolves returns an error wrapping
// [paths.ErrNoDocument]. In an empty document that parsed,
// [paths.Resolver.Matches] instead returns an empty result and a nil
// error.
func (n *Node) Resolver() *paths.Resolver {
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

// AST returns the [ast.Node] the Node selects, the one [Node.Decode]
// decodes. For the root Node, it is the body of the whole document. For a
// Node from [Node.At] or [Node.Nodes], it is the node that method resolved
// when it scoped the Node. The String method of any node, a mapping or a
// sequence included, returns its text:
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
// The node keeps the anchor or the tag written on it, so a type switch on
// it can see an [*ast.AnchorNode] or an [*ast.TagNode] where the source
// holds a mapping. A Node at an alias that reads a reference document of
// [WithReferences] holds the [*ast.AliasNode], or the tag on it, because
// the anchor lies in that document. [Node.Kind] looks through those and
// through an alias, and reports whether the Node holds a mapping, a
// sequence, or a scalar.
func (n *Node) AST() ast.Node {
	if n.base.IsRoot() {
		return n.doc.root.Body
	}

	return n.node
}

// At returns a [*Node] scoped to the node that path selects. An `@` path
// resolves from the receiver, and a `$` path resolves from the root of the
// document. A path from [Node.Path] or [Node.PathAt] of any Node of the
// document thus scopes the node it names. The Node shares the source and
// the document with the receiver, and decodes, validates, and binds
// errors as the Scoped Nodes section of the package documentation
// describes.
//
// A path that ends at an alias that reads a reference document of
// [WithReferences] scopes the alias, and a decode of that Node reads the
// anchor there. A path that enters such an alias returns an error that
// wraps [paths.ErrReferenceAlias], as the References section of the
// package documentation describes.
//
// An alias inside the content of the anchor that it refers to reads null,
// as the go.jacobcolvin.com/niceyaml/paths package describes. A path that
// ends at such an alias scopes the alias, or the tag on it, and a decode
// of that Node reads null. So in "a: &x {b: *x}" and "c: *x", the path
// $.a.b scopes the alias *x, and the path $.c scopes {b: *x}. A path that
// enters such an alias returns an error that wraps [paths.ErrAlias].
//
// A sequence element and the root do not have a key. A path with the `~`
// selector of [paths.Path.Key] after either thus does not select a node,
// and At returns an error wrapping [paths.ErrNotFound] for it.
// [Node.Ranges] and an [Error] at a path that ends in such a `~` still
// point at the element or the root.
//
// A path that does not select a node returns the error that
// [paths.Resolver.Node] describes, bound to the source. It wraps
// [paths.ErrNotFound] when the document does not hold a value at the
// path, and [paths.ErrNoDocument] too when the document does not have
// content. It wraps [paths.ErrAlias] for an alias on the path that does
// not resolve. It wraps [paths.ErrExcessiveMerging] when `<<` merge keys
// make the lookups read far more nodes than the document holds. It wraps
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

	return n.scoped(base, node), nil
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
// header.
//
// Nodes scopes an alias that reads a reference document of
// [WithReferences] as Node.At does. A `[*]` thus scopes an element that
// reads its value from a reference document at its alias, beside the
// elements that the document holds. Nodes also scopes an alias inside the
// content of its own anchor as Node.At does, and a `..*` selector does not
// descend below such an alias.
//
// The errors that [paths.Resolver.Nodes] returns come back bound to the
// source. An alias that does not resolve gives one wrapping
// [paths.ErrAlias]. Aliases or merge keys that lead a selector to far
// more nodes than the document holds give [ErrExcessiveAliasing] or
// [paths.ErrExcessiveMerging]. The document is not at fault for either,
// so each comes inside [Unchecked], and a validator that returns it does
// not report a fault. A document that did not parse returns the error
// that [Node.Err] returns.
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
		nodes = append(nodes, n.scoped(m.Path, m.Node))
	}

	return nodes, nil
}

// scoped returns a copy of n scoped at path, a `$` path that selects node.
// The span of a mapping or a sequence starts at the key or "-" that
// introduces it, so it holds the token [paths.Resolver.Token] gives path.
func (n *Node) scoped(path paths.Path, node ast.Node) *Node {
	c := *n
	c.base = path
	c.node = node
	c.span, c.content = n.extent(node)

	// A scalar is its own token, so no token above it introduces it.
	if astnode.IsScalar(astnode.Content(node)) {
		return &c
	}

	in := datapath.ParentAt(n.doc.pathResolver(), path)

	var value ast.Node

	switch {
	case in.Entry != nil:
		value = in.Entry.Value
	case in.Sequence != nil && in.Index < len(in.Sequence.Values):
		value = in.Sequence.Values[in.Index]
	}

	// The content behind an alias lies at its anchor, so the key or the
	// "-" in front of the alias introduces none of the lines of node.
	if _, ok := astnode.Content(value).(*ast.AliasNode); ok {
		return &c
	}

	tk := astnode.PathToken(value, in)
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
func (n *Node) Path() paths.Path {
	return n.base
}

// Source returns the [*Source] the node came from.
func (n *Node) Source() *Source {
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
// For a Node from [Node.At], they run from the first token under the node
// through the last, comments between them included. They are nil when the
// scope does not select anything. The slice is a copy, so reordering it
// does not affect the Node. Every Node shares the tokens themselves, so a
// caller must not modify them.
func (n *Node) Tokens() token.Tokens {
	return slices.Clone(n.content)
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
// [Node.Tokens].
func (n *Node) Preamble() token.Tokens {
	return slices.Clone(n.doc.tokens[:n.doc.preamble])
}

// FilePath returns the path of the file the document came from, which is
// [Source.FilePath]. It is absolute for a document [NewSourceFromFile]
// read, so a schema routes on the file and not on how the caller
// spelled its path. It returns an empty string when the source does not
// have a path.
//
// The document that [NewSourceFromLayers] builds did not come from a file.
// Its Source has the path of the layer [MergeAs] names, or else of the
// lowest layer. Every Node of it returns that path, whichever layer holds
// its value. [Node.Origin] returns the Node of the value in the file of
// its layer, and FilePath of that Node names the file.
func (n *Node) FilePath() string {
	return n.source.FilePath()
}

// FS returns the file system that [Node.FilePath] names a file in, which
// is [Source.FS]. It returns nil when the source does not have one. A path
// then names a file on disk. A Node of the document that
// [NewSourceFromLayers] builds returns the file system of the layer
// [MergeAs] names, or else of the lowest layer, as [Node.FilePath]
// describes.
func (n *Node) FS() fs.FS {
	return n.source.FS()
}

// Span returns the lines of [Source.Lines] that the node covers. A whole
// document covers the lines from the one its first token starts on to the
// one before the next document starts. The last document covers the lines
// to the end of the source. The first document also covers the lines
// above its first token, so the spans of a source cover every one of its
// lines. A document without tokens, other than the first, does not cover
// any line.
//
// A Node from [Node.At] or [Node.Nodes] covers the lines from the first
// token under its node through the last. A scope that does not select
// anything does not cover any line. A scalar covers its own lines alone. A
// mapping or a sequence also covers the line of the key or the "-" that
// introduces it. The span thus holds the token that [Node.Ranges] and an
// error bound at the root of the scope resolve to:
//
//	hours:      # the span of $.hours starts here
//	  open: 9
//
// A path that ends at an alias whose anchor the document defines, such as
// `$.c` in `c: *x`, covers the content of the anchor. That content is what
// [Node.AST] and [Node.Decode] read. Node.Ranges and an error at its root
// resolve to the alias instead. When the anchor and the alias lie on
// different lines, [SourceError.Annotate] does not mark anything on
// [Node.View] and reports false. A caller then adds the line of the error
// with [line.View.Slice]. An error at a Node of the document that
// [NewSourceFromLayers] builds binds in the file of a layer, so a caller
// marks the view of its [Node.Origin] instead.
//
// A path that ends at an alias that reads a reference document of
// [WithReferences] covers the lines of the alias alone. The anchor of such
// an alias lies in the reference document. The span of `spec:` with
// `*base` on the next line thus leaves out the line of the key, even where
// [Node.Kind] reports a mapping. Node.Ranges and an error at or below such
// a Node resolve to the alias, so SourceError.Annotate marks the error on
// Node.View.
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
// A Node from a path that ends at an alias covers the alias instead, as
// the document writes it at the path, with the tag written on it. For
// `c: *x`, `$.c` covers `*x`, which holds the token that [Node.Ranges]
// highlights. [Node.AST] returns the content of the anchor, and
// [Node.Span] covers its lines. A path that ends in `~` covers a key
// written as an alias the same way. A path through the alias, such as
// `$.c.b`, covers the text at the anchor. So does a path to an entry that
// a `<<` merge key brings in. The key of such an entry lies at the anchor
// too.
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
	if alias, ok := n.writtenAlias(); ok {
		node = alias
	}

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
	if last, ok := path.Last(); ok && last.Kind == paths.SelectorKey {
		path, _ = path.Parent()
	}

	in := datapath.ParentAt(n.doc.pathResolver(), path)
	if in.Entry == nil {
		return nil
	}

	key := ast.Node(in.Entry.Key)
	if explicit, ok := key.(*ast.MappingKeyNode); ok {
		key = explicit.Value
	}

	return key
}

// writtenAlias returns the node that the document writes at the path of
// n, and true when that node is an alias. The node keeps the tag written
// on the alias. [Node.AST] holds the content of the anchor that the alias
// refers to. A path that ends in `~` names the key of its entry.
func (n *Node) writtenAlias() (ast.Node, bool) {
	if n.base.IsRoot() {
		return nil, false
	}

	var written ast.Node

	// ParentAt gives no holder for a `~`, which picks the key of an entry.
	in := datapath.ParentAt(n.doc.pathResolver(), n.base)

	switch last, _ := n.base.Last(); {
	case in.Entry != nil:
		written = in.Entry.Value
	case in.Sequence != nil && in.Index < len(in.Sequence.Values):
		written = in.Sequence.Values[in.Index]
	case last.Kind == paths.SelectorKey:
		written = n.entryKey(n.base)
	}

	if _, ok := astnode.Content(written).(*ast.AliasNode); !ok {
		return nil, false
	}

	return written, true
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
//	result := diff.Diff(before[1].View(), after[1].View())
//
// Each call returns a view of its own without decoration, as [Source.View]
// does, and [line.View.Held] returns its lines on their own. The view
// shares its lines with every view over the source. [SourceError.Annotate]
// thus marks a bound error on it when the location of the error lies in
// the span, and reports false otherwise, as Node.Span describes.
func (n *Node) View() *line.View {
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
// [Node.View] of the Node [Node.At] returns. [Node.Range] gives the
// columns of its text, from its first token through its last.
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
// a missing key, which is the key of the mapping that lacks it. It reports
// true only when reason wraps [paths.ErrNotFound] and that key has a
// position.
func (n *Node) nearestLocation(path paths.Path, reason error) (location, bool) {
	if !errors.Is(reason, paths.ErrNotFound) {
		return location{}, false
	}

	resolver := n.doc.pathResolver()

	near, ok := resolver.Nearest(n.base.Join(path))
	if !ok {
		return location{}, false
	}

	tk, err := resolver.Token(near.Key())
	if err != nil || tk == nil || tk.Position == nil {
		return location{}, false
	}

	return location{pos: position.NewFromToken(tk), tk: tk, near: &near}, true
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

	if slices.ContainsFunc(slices.Collect(start.Selectors()), func(sel paths.Selector) bool {
		return sel.Kind != paths.SelectorChild && sel.Kind != paths.SelectorIndex && sel.Kind != paths.SelectorKey
	}) {
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
// missing key binds as [Invalid] at the mapping that lacks it, with the
// bare [paths.ErrNotFound] text, because the binding writes the path in
// front of it. An alias that the path cannot follow, such as one without
// an anchor or one inside the value of its own anchor, binds at the alias,
// as [Node.bindAlias] binds it. Any other reason binds as
// [Node.bindLookup] binds it.
func (n *Node) bindUnresolved(path paths.Path, reason error) error {
	if bound, ok := n.bindAlias(path, reason); ok {
		return bound
	}

	if !errors.Is(reason, paths.ErrNotFound) {
		return n.bindLookup(reason)
	}

	if _, ok := n.nearestLocation(path, reason); !ok {
		return n.bindOwn(Invalid(reason))
	}

	return n.bindOwn(Invalid(&markedError{err: reason, msg: paths.ErrNotFound.Error()}, AtPath(path)))
}

// bindLookup binds reason, the error of a lookup that neither misses a
// value nor stops at an alias that [Node.bindAlias] binds. An alias of the
// document that does not resolve binds as it is. Any other reason, such as
// a path past a limit or a wildcard path that [Node.At] rejects, binds
// inside [Unchecked], because the document is not at fault for it. A
// validator that returns such an error thus does not report a fault.
func (n *Node) bindLookup(reason error) error {
	if errors.Is(reason, paths.ErrAlias) && !errors.Is(reason, paths.ErrReferenceAlias) {
		return n.bindOwn(reason)
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
// an error without a location names the source, and the document in a
// source of several, as in "name: document 3: msg". To keep several errors
// as separate bindings, bind each one before joining them.
//
// Bind returns nil for a nil err, and for a nil [*Error] or
// [*SourceError] pointer. Bind returns an already bound error unchanged,
// so Bind is idempotent. The Errors section defines a bound error. Bind
// never modifies err. In a document that did not parse, a path does not
// resolve, and [SourceError.Unresolved] returns [ErrPathNeedsDocument]
// wrapping the error of the parse.
func (n *Node) Bind(err error) error {
	return bindTree(err, binder{src: n.source, node: n, locate: true})
}

// bindOwn binds an error of a call on n as [Node.Bind] does, except that
// an error without a location does not gain one, even for a scoped Node.
// The exception holds because the error is about the call and not the
// value.
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

	if _, ok := n.source.parsed.get(n.source.parse).result.Index[tk]; ok {
		return true
	}

	return n.doc.tree().Holds(tk)
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

	msg := err.Error()

	restored := d.tree().RestoreNames(msg)
	if restored == msg {
		return err
	}

	return &markedError{err: err, msg: restored}
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
// Node of the document. The innermost node is an entry of a mapping or an
// element of a sequence. A position on the key of an entry gives a path
// that ends in `~`, as [paths.Path.Key] writes one. A position on the ":"
// or the value gives the path of the entry. The "-" of a block sequence
// belongs to its element, and the "[", "{", ",", anchor, or tag of a
// collection belongs to the collection.
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
// false outside the lines
// of the source and in another document of the source. [Source.DocumentAt]
// picks the document of a position. PathAt reports false for a document
// that did not parse or that does not hold content. It reports false
// inside an entry that paths cannot select, such as the earlier of two
// entries with one key. Every path it returns selects the node at pos and
// does not hold a wildcard selector.
func (n *Node) PathAt(pos position.Position) (paths.Path, bool) {
	if n == nil || n.doc.err != nil {
		return paths.Path{}, false
	}

	tk := contentTokenAt(n.source.lines, pos)
	if tk == nil {
		return paths.Path{}, false
	}

	return n.doc.pathIndex().PathAt(n.doc.pathResolver(), tk.Position.Offset)
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
// of the nodes it holds, and reports false elsewhere in the document.
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
// Kind looks through the anchor and the tag on the node, an alias and the
// anchor it refers to, and the `?` of an explicit key. The root of
// `&r {a: 1}` and of `!!map {a: 1}` is thus a mapping, but [Node.AST] gives
// the anchor or the tag that wraps it. A decode reads the value under a tag
// such as !!str or !!null as a scalar, so "!!str *k" and "!!null *k" are
// scalars when k is a sequence. A Node that [Node.At] or [Node.Nodes] scopes
// at an alias holds the content of its anchor, so [NodeKind] does not have a
// kind for an alias. A Node at an alias that reads a reference document of
// [WithReferences] holds the alias, and Kind reports the kind of the content
// of its anchor there, as a decode reads it. A Node at an alias inside the
// content of its own anchor holds the alias too. A decode reads that alias
// as null, so Kind reports [NodeScalar]. The root of a document that is
// an alias without an anchor before it or in its reference documents does
// not hold a value.
//
// Every scalar has the same kind, [NodeScalar]. The type that a scalar
// decodes to depends on its tag, its text, and the Go type that a decode
// fills. A caller that needs that type decodes the Node.
func (n *Node) Kind() NodeKind {
	if n == nil || n.doc.err != nil {
		return NodeNone
	}

	node := n.AST()
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
	res := d.pathResolver()

	anchor, err := res.Anchor(alias)
	if errors.Is(err, paths.ErrReferenceAlias) {
		name, _ := astnode.Name(alias.Value)

		if ref, ok := d.node.source.referenceAnchors().Anchor(name); ok {
			return ref.Value, nil
		}
	}

	if err != nil {
		//nolint:wrapcheck // The paths error names the alias.
		return nil, err
	}

	return anchor.Value, nil
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

		// An alias under a `<<` merge key might set any key or might not set
		// one, so a path below the mapping that holds the merge key goes on
		// as [Node.mergeLocation] tells.
		if alias, ok := root.aliasLocation(path, err); ok && !root.mergedBy(alias.tk) {
			return alias, nil
		}

		return root.mergeLocation(path, err)
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

// mergeLocation returns the location of path, which did not resolve with
// reason. The resolver cannot read the entries that a `<<` merge key
// brings in from a reference document of [WithReferences], so a key of a
// mapping that holds such a merge key does not resolve, and reason wraps
// [paths.ErrReferenceAlias]. The lookup of a decode reads those entries,
// as [decodewalk.PathEntries.Entry] does. The walk thus goes on from the
// mapping through the entry that the decoder reads for the key:
//
//   - An entry that the merge key brings in binds at the alias of the
//     merge key, near the mapping that holds it.
//   - An entry of the document goes on along the rest of path.
//   - A key that no entry has binds at the mapping, as
//     [Node.nearestLocation] binds a key that the document leaves out.
//
// The rest of path binds as [Node.resolveLocation] binds a path. A path
// that enters an alias the document cannot follow binds at the alias, near
// the path of the alias, with a reason that names that alias. The location
// of a merge key binds so too. A rest of path that does not resolve fails as
// it fails in a mapping without the merge key, so a rest that selects
// nothing returns an error that wraps [paths.ErrNotFound]. The result is
// reason when the lookup cannot read the merge source of the key, or reason
// does not wrap [paths.ErrReferenceAlias].
//
// The walk reads the merge sources of a mapping through the aliases of the
// document, and such reads are part of what the alias limit weighs. The
// result is thus reason for a document past that limit, as
// [document.pastAliasLimit] reports it. The Source and the reference table
// keep the keys of each merge source that a walk reads, as
// [Source.mergeKeys] describes. So the walks of many paths read each
// source once.
func (n *Node) mergeLocation(path paths.Path, reason error) (location, error) {
	if !errors.Is(reason, paths.ErrReferenceAlias) || n.doc.pastAliasLimit() {
		return location{}, reason
	}

	resolver := n.doc.pathResolver()
	w := mergeWalk{
		resolver: resolver,
		entries:  n.doc.newPathEntries(),
		reason:   reason,
		full:     n.base.Join(path),
	}

	// The resolver stops at the first selector it cannot apply, so the
	// walk starts at the longest path above path that resolves.
	for at, ok := w.full.Parent(); ok; at, ok = at.Parent() {
		node, err := resolver.Node(at)
		if err != nil {
			continue
		}

		rest, _ := w.full.CutPrefix(at)

		return w.walk(datapath.Target{Node: node, Path: at}, rest)
	}

	return location{}, reason
}

// mergeWalk walks a path that the resolver cannot resolve, as
// [Node.mergeLocation] describes.
type mergeWalk struct {
	resolver *paths.Resolver
	// The lookups of the entries of the mappings of the document.
	entries *decodewalk.PathEntries
	// The reason that the resolver gives for full.
	reason error
	// The path that the walk resolves, which starts at `$`.
	full paths.Path
	// Whether the walk has passed a mapping whose `<<` merge key reads a
	// reference document by a key that the merge key does not bring in.
	// The document holds the entry of that key, or no entry has it.
	left bool
}

// walk applies the selectors of rest, an `@` path, from t, the node that
// the resolver resolves at the path of t. It returns the location of the
// path, as [Node.mergeLocation] describes.
func (w *mergeWalk) walk(t datapath.Target, rest paths.Path) (location, error) {
	sels := slices.Collect(rest.Selectors())

	for i, sel := range sels {
		if sel.Kind == paths.SelectorKey {
			if i != len(sels)-1 {
				return w.miss(nil)
			}

			return w.at(t, true, nil)
		}

		content, err := w.resolver.Deref(t.Node)
		if err != nil {
			if !errors.Is(err, paths.ErrAlias) {
				return w.miss(err)
			}

			return w.at(t, false, w.unresolved(err))
		}

		switch sel.Kind {
		case paths.SelectorChild:
			entry, err := w.resolver.Entry(content, sel.Name)

			switch {
			case errors.Is(err, paths.ErrReferenceAlias):
				mapping, ok := datapath.Deref(w.resolver, content).(*ast.MappingNode)
				if !ok {
					return w.miss(err)
				}

				var (
					alias *ast.AliasNode
					known bool
				)

				entry, alias, known = w.entries.Entry(mapping, sel.Name)
				if !known {
					return w.miss(err)
				}

				if alias != nil {
					return w.merge(t, alias)
				}

				w.left = true

			case err != nil && !errors.Is(err, paths.ErrNotFound):
				return w.miss(err)
			}

			if entry == nil {
				return w.missing(t, content, sels[i:])
			}

			t = datapath.Target{Node: entry.Value, Parent: astnode.Parent{Entry: entry}, Path: t.Path.Child(sel.Name)}

		case paths.SelectorIndex:
			seq, ok := datapath.Deref(w.resolver, content).(*ast.SequenceNode)
			if !ok || sel.Index >= len(seq.Values) {
				return w.miss(nil)
			}

			t = datapath.Target{
				Node:   seq.Values[sel.Index],
				Parent: astnode.Parent{Sequence: seq, Index: sel.Index},
				Path:   t.Path.Index(sel.Index),
			}

		default:
			return w.miss(nil)
		}
	}

	return w.at(t, false, nil)
}

// merge returns the location of an entry that alias, the alias of a `<<`
// merge key of the mapping at t, brings in from a reference document.
func (w *mergeWalk) merge(t datapath.Target, alias *ast.AliasNode) (location, error) {
	tk := alias.GetToken()

	_, err := w.resolver.Deref(alias)
	if err == nil || tk == nil || tk.Position == nil {
		return location{}, w.reason
	}

	return location{pos: position.NewFromToken(tk), tk: tk, near: &t.Path, unfollowed: w.unresolved(err)}, nil
}

// missing returns the location of a key that the mapping at t, whose
// content is content, leaves out. The selectors of rest start at that key.
// It binds as [Node.nearestLocation] binds such a key. Where that binding
// does not bind, the path does not resolve. That holds for a selector of
// rest that is not a child selector, and for content that is neither a
// mapping nor a null.
func (w *mergeWalk) missing(t datapath.Target, content ast.Node, rest []paths.Selector) (location, error) {
	for _, sel := range rest {
		if sel.Kind != paths.SelectorChild {
			return w.miss(nil)
		}
	}

	held := datapath.Deref(w.resolver, content)
	if _, ok := held.(*ast.MappingNode); !ok && (astnode.IsNil(held) || held.Type() != ast.NullType) {
		return w.miss(nil)
	}

	loc, err := w.at(t, true, nil)
	loc.near = &t.Path

	return loc, err
}

// miss returns the reason that the path does not resolve where the walk
// stops, for err, the error of the lookup there, or for nil when that
// lookup selects nothing. Until the walk passes a mapping by a key that
// its `<<` merge key of a reference document does not bring in, the
// resolver stops where the walk stops. The reason is then the reason of
// the resolver. After that mapping, the path fails as it fails in a
// mapping without the merge key. The reason reads as the resolver words
// err with the whole path, or wraps [paths.ErrNotFound].
func (w *mergeWalk) miss(err error) (location, error) {
	switch {
	case !w.left:
		return location{}, w.reason
	case err != nil:
		return location{}, w.unresolved(err)
	default:
		return location{}, fmt.Errorf("resolve %s: %w", w.full, paths.ErrNotFound)
	}
}

// at returns the location of the token of t, or of the key of its entry
// with key set. A reason that is not nil is the reason that the path does
// not resolve at t, an alias, so the location is near the path of t.
func (w *mergeWalk) at(t datapath.Target, key bool, reason error) (location, error) {
	tk := w.token(t, key)
	if tk == nil || tk.Position == nil {
		return location{}, w.reason
	}

	loc := location{pos: position.NewFromToken(tk), tk: tk}
	if reason != nil {
		loc.near, loc.unfollowed = &t.Path, reason
	}

	return loc, nil
}

// token returns the token that a path to t points at, or to the key of its
// entry with key set, as [paths.Resolver.Token] gives it. The walk starts
// at a node without what holds it, which the resolver resolves, so the
// resolver gives the token there.
func (w *mergeWalk) token(t datapath.Target, key bool) *token.Token {
	if t.Parent != (astnode.Parent{}) {
		return t.Start(key)
	}

	p := t.Path
	if key {
		p = p.Key()
	}

	tk, err := w.resolver.Token(p)
	if err != nil {
		return nil
	}

	return tk
}

// unresolved returns the reason that the path does not resolve where the
// walk stops, from err, the error of a lookup of the resolver there, such
// as [paths.Resolver.Deref] for an alias. It reads as the resolver words
// such a reason, with the whole path.
func (w *mergeWalk) unresolved(err error) error {
	return fmt.Errorf("resolve %s: %w", w.full, errors.Unwrap(err))
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
