package niceyaml

import (
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/aliasing"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/datapath"
	"go.jacobcolvin.com/niceyaml/internal/decodetree"
	"go.jacobcolvin.com/niceyaml/internal/escape"
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
	// The syntax error of a document that did not parse, or nil for one
	// that parsed. The bindSyntaxErrors function binds it to the Source
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
// error rather than an empty match.
func (d *document) newResolver() *paths.Resolver {
	if d.err != nil {
		return paths.NewResolver(nil)
	}

	return paths.NewResolver(d.root)
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
// A document with a YAML syntax error does not have a tree. The methods
// that [Node.Err] lists return the error that Err returns. The methods
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

// Err returns the YAML syntax error of the document the Node belongs to,
// or nil for a document that parsed and for a nil Node. A syntax error
// fails the one document that holds it, and [Source.AllDocuments] returns
// a Node for that document too. The error is a [*SourceError] bound to
// the Source and to that document, so [SourceError.Document] returns its
// root. It matches [ErrSyntax], and it is among the errors [Source.File]
// returns:
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
// [Node.DecodeIfPresent], [Node.Validate], [Node.At], [Node.Nodes], and
// [Node.Ranges] return the error that Err returns, and they do not run a
// [Validator]. [Node.AST] and [Node.DocumentAST] return nil, and
// [Node.PathAt] reports false for every position.
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
// needs it, and the resolver binds the aliases of the document then. A
// [Validator] that runs for each item of a list therefore binds them
// once, but a resolver from [paths.NewResolver] binds them again for every
// item.
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
// holds a mapping. [Node.Kind] looks through those and through an alias,
// and reports whether the Node holds a mapping, a sequence, or a scalar.
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
// A document that did not parse returns the syntax error that [Node.Err]
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
// A path that does not select a node returns an empty slice and a nil
// error. A path with a selector returns the same in a document without
// content, but Node.At returns an error wrapping [paths.ErrNoDocument]
// for it. In such a document, the root path selects the null under the
// "---" header. It does not select a node when the document lacks a
// header.
//
// The errors that [paths.Resolver.Nodes] returns come back bound to the
// source. An alias that does not resolve gives one wrapping
// [paths.ErrAlias]. Aliases or merge keys that lead a selector to far
// more nodes than the document holds give [ErrExcessiveAliasing] or
// [paths.ErrExcessiveMerging]. A document that did not parse returns the
// syntax error that [Node.Err] returns.
func (n *Node) Nodes(path paths.Path) ([]*Node, error) {
	if n.doc.err != nil {
		return nil, n.doc.err
	}

	found, err := n.doc.pathResolver().Matches(n.base.Join(path))
	if err != nil {
		return nil, n.bindOwn(err)
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
// A path that ends at an alias, such as `$.c` in `c: *x`, covers the
// content of the anchor, which [Node.AST] and [Node.Decode] read.
// Node.Ranges and an error at its root resolve to the alias instead. When
// the anchor and the alias lie on different lines, [SourceError.Annotate]
// does not mark anything on [Node.View] and reports false. A caller then
// adds the line of the error with [line.View.Slice]. An error at a Node of
// the document that [NewSourceFromLayers] builds binds in the file of a
// layer, so a caller marks the view of its [Node.Origin] instead.
//
// [Node.View] returns a view sliced to the span, and the span slices any
// other view over the source, such as one with decoration already:
//
//	lipgloss.Println(p.Print(view.Slice(doc.Span())))
func (n *Node) Span() position.Span {
	return n.span
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
// indicator. A mapping or a sequence covers the token that introduces it.
// That token is the key of its entry, the "-" of its element in a block
// sequence, or else its own "{" or "[". A block mapping or a block
// sequence at the root covers its first key or element. A path from
// [paths.Path.Key] covers the key of the entry:
//
//	ranges, err := doc.Ranges(paths.Doc().Child("spec", "replicas"))
//	if err != nil {
//		return err
//	}
//
//	view.AddOverlay(kind.GenericHighlight, ranges...)
//
// To mark every line of a mapping or a sequence, use [Node.Span] or
// [Node.View] of the Node [Node.At] returns.
//
// A path that does not resolve returns the error [paths.Resolver.Token]
// describes, bound to the source as [Node.At] binds it. A token without a
// position returns an error wrapping [ErrNoLocation]. A document that did
// not parse returns the syntax error that [Node.Err] returns. Ranges
// returns nil when the value does not hold content on any line.
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

// aliasLocation returns the location of the alias that path cannot follow.
// It reports false when reason does not wrap [paths.ErrAlias]. It also
// reports false for an alias under a `<<` merge key, because such an alias
// might set any key or might not set one.
func (n *Node) aliasLocation(path paths.Path, reason error) (location, bool) {
	if !errors.Is(reason, paths.ErrAlias) {
		return location{}, false
	}

	resolver := n.doc.pathResolver()

	// The path fails at the first selector that reads through the alias,
	// so the longest path above it that resolves ends at the node that
	// selector reads.
	for at, ok := n.base.Join(path).Parent(); ok; at, ok = at.Parent() {
		tk, err := resolver.Token(at)
		if err != nil {
			continue
		}

		// The resolver reads through every node but an alias it cannot
		// follow. Where it reads through this one, the selector failed at
		// an alias under a `<<` merge key of the mapping here.
		_, err = resolver.Node(at)
		if !errors.Is(err, paths.ErrAlias) || tk == nil || tk.Position == nil {
			return location{}, false
		}

		return location{pos: position.NewFromToken(tk), tk: tk, near: &at, unfollowed: reason}, true
	}

	return location{}, false
}

// bindUnresolved binds reason, the error path failed to resolve with. A
// missing key binds as [Invalid] at the mapping that lacks it, with the
// bare [paths.ErrNotFound] text, because the binding writes the path in
// front of it.
func (n *Node) bindUnresolved(path paths.Path, reason error) error {
	if !errors.Is(reason, paths.ErrNotFound) {
		return n.bindOwn(reason)
	}

	if _, ok := n.nearestLocation(path, reason); !ok {
		return n.bindOwn(Invalid(reason))
	}

	return n.bindOwn(Invalid(&markedError{err: reason, msg: paths.ErrNotFound.Error()}, AtPath(path)))
}

// Bind binds err to the source of the document. Each path in err resolves
// in the document of the Node, an `@` path from the scope of the Node and
// a `$` path from the root. Bind resolves every location as it binds, so
// the position that [SourceError.Error] reports and the excerpt that
// [SourceError.Excerpt] returns stay fixed from then on. The Node methods
// bind their errors already. Bind is for an error built elsewhere, such as
// the result of a check that the caller runs on a value it took from the
// document:
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
// wrapping the syntax error.
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
// The path names where the document writes the node. A position on an
// alias thus gives the entry that holds the alias, and a position inside
// an anchor gives the path where the anchor defines it.
//
// PathAt reports false on a comment, a blank line, a "---" header, a
// "..." marker, or a directive. It also reports false outside the lines
// of the source and in another document of the source, so a caller with
// several documents asks each. It reports false for a document that did
// not parse or that does not hold content. It reports false inside an
// entry that paths cannot select, such as the earlier of two entries with
// one key. Every path it returns selects the node at pos and does not
// hold a wildcard selector.
func (n *Node) PathAt(pos position.Position) (paths.Path, bool) {
	if n == nil || n.doc.err != nil {
		return paths.Path{}, false
	}

	tk := n.source.lines.TokenAt(pos)
	if tk == nil || tk.Position == nil || tk.Type == token.CommentType {
		return paths.Path{}, false
	}

	return n.doc.pathIndex().PathAt(n.doc.pathResolver(), tk.Position.Offset)
}

// NodeKind is the structural kind of the value a [Node] holds, as
// [Node.Kind] reports it.
type NodeKind int

// [NodeKind] constants.
const (
	// NodeNone is the kind of a Node that does not hold a value. Such a
	// Node is the root of an empty document, of a document of comments or
	// directives alone, or of a document that did not parse, or a nil
	// Node. The root of a document that is an alias without an anchor
	// before it has this kind too. [Node.IsEmpty] reports the documents
	// among these that do not have content, and [Node.Err] tells a
	// document that did not parse apart from an empty one.
	NodeNone NodeKind = iota
	// NodeMapping is the kind of a mapping, block or flow.
	NodeMapping
	// NodeSequence is the kind of a sequence, block or flow.
	NodeSequence
	// NodeScalar is the kind of a scalar, a null included, whatever its
	// tag.
	NodeScalar
)

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
// Kind looks through the anchor and the tag on the node, an alias and
// the anchor it refers to, and the `?` of an explicit key. The root of
// `&r {a: 1}` and of `!!map {a: 1}` is thus a mapping, but [Node.AST]
// gives the anchor or the tag that wraps it. A Node that [Node.At] or
// [Node.Nodes] scopes at an alias holds the content of its anchor, so
// [NodeKind] does not have a kind for an alias. The root of a document
// that is an alias without an anchor before it does not hold a value.
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

	switch datapath.Deref(n.doc.pathResolver(), node).(type) {
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
// [Node.Kind] is [NodeNone] for every Node that IsEmpty reports. It is
// also [NodeNone] for the root of a document that did not parse or that
// is an alias without an anchor before it. A caller that skips empty
// documents thus asks IsEmpty. [SkipEmpty] wraps a [Validator] so that it
// passes an empty document.
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

		if alias, ok := root.aliasLocation(path, err); ok {
			return alias, nil
		}

		return location{}, err
	}

	return loc, nil
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
