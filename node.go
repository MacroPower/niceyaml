package niceyaml

import (
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"sort"
	"strings"
	"sync"

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
	// parse, it is the stand-in [docparse.Parse] made, which has no body.
	root *ast.DocumentNode
	// The syntax error of a document that did not parse, bound to the
	// Source and to the document bindSyntaxErrors picks for it, which is
	// nil for one that parsed.
	err error
	// Resolves every path in root. The first call of pathResolver creates
	// it, and since no Node edits the tree, it binds the aliases once.
	resolver *paths.Resolver
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
	// The index of root in the Docs of the file the Source parsed, which
	// can differ from index, since preamble nodes fold into documents.
	fileIndex int
	// The number of tokens at the start of tokens before the content.
	preamble int
	// Creates resolver once, for the first path any Node resolves.
	resolverOnce sync.Once
}

// pathResolver returns the [paths.Resolver] of the document, which it
// creates on the first call. A document that did not parse gets one with
// no document, so a path returns an error rather than an empty match.
func (d *document) pathResolver() *paths.Resolver {
	d.resolverOnce.Do(func() {
		if d.err != nil {
			d.resolver = paths.NewResolver(nil)

			return
		}

		d.resolver = paths.NewResolver(d.root)
	})

	return d.resolver
}

// Node is a scope in a YAML document: the root of the document, which
// [Source.Documents] and [Source.Document] return, or a node a path
// selects. Every method reads from the node and resolves an `@` path from
// it, and a `$` path resolves from the root of the document through any
// Node. [Node.Decode] decodes the node alone, [Node.Validate] runs a
// [Validator] on it, and [Node.Bind] resolves the `@` paths of an error
// from it, so a check written for the type of a value reports the same
// lines whether the value is the whole document or one inside it.
//
// Receive instances from [Source.Documents], [Source.AllDocuments],
// [Source.Document], [Node.At], [Node.Nodes], [Node.Document],
// [SourceError.Node], or [SourceError.Document].
//
// The Decoding section of the package documentation describes the steps
// a decode runs, and the Scoped Nodes section describes what a Node from
// [Node.At] or [Node.Nodes] reports. [Node.DocumentAST],
// [Node.DocumentIndex], [Node.Preamble], [Node.FilePath], and
// [Node.PathAt] describe the document as a whole, whatever Node of it a
// caller holds.
//
// A document with a YAML syntax error has no tree. The methods that read
// the tree return the error [Node.Err] returns, and the methods that read
// the tokens and the lines work as they do for any document, so a caller
// renders or diffs a document that does not parse yet.
//
// A Node of the Source [NewSourceFromLayers] builds belongs to the
// document its layers merge into, and each error it returns binds in the
// file of a layer, as NewSourceFromLayers describes.
type Node struct {
	// The node the scope selects, which At or Nodes resolves once when it
	// scopes the Node. The root of a document leaves it unset, since its
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
// [Node.Document] returns the root [*Node] of the same document,
// [Node.AST] returns the go-yaml node the Node selects, and [Node.Path]
// is the path from the document root to it. The node is part of the
// tree [Source.File] returns, which every Node of the Source shares and
// resolves against, so a caller must not modify it. A document that did
// not parse has no node, so DocumentAST returns nil for it, as [Node.Err]
// describes.
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
// [Node.Ranges] return the error Err returns, and no [Validator] runs.
// [Node.AST] and [Node.DocumentAST] return nil, and [Node.PathAt] reports
// false for every position.
//
// A panic in the parser fails the documents it was reading the same way,
// and Err then returns an error that holds a [PanicError] and does not
// match ErrSyntax. A "---" header that directly follows an anchor with no
// value parses together with the document above it, so both documents
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
// resolver follows the aliases and merge keys of the go-yaml
// nodes [Node.AST] and [Node.DocumentAST] return. The document creates
// the resolver once, when the first of its Nodes needs it, and the
// resolver binds the aliases of the document then. A [Validator] that
// runs for each item of a list therefore binds them once, where a
// resolver from [paths.NewResolver] binds them again for every item.
//
// A document that did not parse has no tree, as [Node.Err] describes, so
// its resolver holds no document, as [paths.NewResolver] of a nil
// document does. Every path it resolves returns an error wrapping
// [paths.ErrNoDocument], where [paths.Resolver.Matches] returns no
// matches and no error in an empty document that parsed.
func (n *Node) Resolver() *paths.Resolver {
	return n.doc.pathResolver()
}

// Document returns the root [*Node] of the document the Node belongs to,
// so a Node from [Node.At] reaches the whole document, as a validator
// that picks a schema from the file path or the content of the document
// does. Every Node of a document returns the root [Source.AllDocuments]
// returns, and that root returns itself. A nil Node belongs to none.
func (n *Node) Document() *Node {
	if n == nil {
		return nil
	}

	return n.doc.node
}

// AST returns the [ast.Node] the Node selects, the one [Node.Decode]
// decodes. For the root Node that is the body of the whole document, and
// for one from [Node.At] or [Node.Nodes] it is the node that method
// resolved when it scoped the Node. The text of any node, including a
// mapping or a sequence, is its String method:
//
//	scoped, err := doc.At(path)
//	if err != nil {
//		return err
//	}
//
//	fmt.Println(scoped.AST().String())
//
// The body is what the parser built: nil for an empty document, and a
// comment group for one holding only comments. When the whole file holds
// whitespace alone, or text the lexer emits nothing for such as a lone
// "!", the body of its one document is a scalar holding the placeholder
// token [tokens.Tokenize] makes, and [tokens.IsPlaceholder] tells it
// apart from a scalar the file holds. A document of whitespace below a
// "---" header has a nil body. AST returns each of these bodies as it
// is, as such a document decodes to nothing. [Node.IsEmpty] reports
// each of these documents, so a caller need not tell the bodies apart.
// A document that did not parse has no body either, and [Node.Err]
// tells it apart from an empty document. The node is part of the tree
// [Source.File] returns, which every Node of the Source shares and
// resolves against, so a caller must not modify it.
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

// At returns a [*Node] scoped to the node path selects. An `@` path
// resolves from the receiver and a `$` path from the root of the
// document, so a path from [Node.Path] or [Node.PathAt] of any Node of
// the document scopes the node it names. The Node shares the source and
// the document with the receiver, and reports what the Scoped Nodes
// section of the package documentation describes:
//
//	hours, err := doc.At(paths.Doc().Child("spec", "hours"))
//	if err != nil {
//		return err
//	}
//
//	h, err := hours.Decode[Hours](ctx, niceyaml.WithValidator(hoursSchema))
//
// A path that selects nothing returns the error [paths.Resolver.Node]
// describes, bound to the source. It wraps [paths.ErrNotFound] when
// nothing exists at the path, and [paths.ErrNoDocument] too when the
// document has no content. It wraps [paths.ErrAlias] for an alias on the
// path that does not resolve, [paths.ErrExcessiveMerging] when `<<` merge
// keys make the lookups read far more nodes than the document holds, and
// [paths.ErrWildcard] for a path that could match several nodes, which
// [Node.Nodes] scopes one by one. A document that did not parse returns
// the syntax error [Node.Err] returns.
//
// The error for a key a mapping leaves out binds at the key of that
// mapping, as in "cfg.yaml:3:1: $.server.name: not found", and any other
// path that selects nothing gives no location. [IsInvalid] reports every
// error that wraps paths.ErrNotFound. A caller that falls back to a
// default tests the error of At, not of a later decode, as
// [Node.DecodeIfPresent] does.
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

// Nodes returns a [*Node] scoped to each node path selects, in document
// order. A path with a `[*]`, `.*`, `..name`, or `..*` selector, which
// [Node.At] rejects, thus scopes every element of a sequence, every entry
// of a mapping, every entry with a name at any depth, or every node at
// any depth, each once. The path resolves as for Node.At, and each Node
// is scoped as one from Node.At is. [Node.Path] of each is the `$` path
// that selects its node alone, so an error bound to it reports the
// element it came from:
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
// The last selector of that path, which [paths.Path.Last] gives, names an
// entry by the text [paths.Resolver.KeyName] gives its key.
//
// A path that selects nothing returns no Nodes and no error, and so does
// a path with a selector in a document with no content, where Node.At
// returns an error wrapping [paths.ErrNoDocument]. The root path selects
// the null under the "---" header of such a document, and nothing in one
// without a header. The errors [paths.Resolver.Nodes] returns come back
// bound to the source: one wrapping [paths.ErrAlias] for an alias that
// does not resolve, and [ErrExcessiveAliasing] or
// [paths.ErrExcessiveMerging] when aliases or merge keys lead a selector
// to far more nodes than the document holds. A document that did not
// parse returns the syntax error [Node.Err] returns.
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
// with no token in the document, such as an implicit null, covers its line.
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
// than spaces, or false for none. Skipping the indentation the lexer folds
// into a scalar keeps the line of a sibling out of the span.
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

// Path returns the scope of the [Node]: the `$` path from the document
// root to the node, which is [paths.Doc] for the root Node of a document
// and the joined paths for one from [Node.At]. A Node from [Node.Nodes]
// reports the path that selects its node alone, so the Node for the first
// match of `$.items[*]` reports `$.items[0]`. The path starts at `$`, so
// it goes back to [Node.At], [Node.Ranges], or [AtPath] through any Node
// of the document and names the same node.
func (n *Node) Path() paths.Path {
	return n.base
}

// Source returns the [*Source] the node came from.
func (n *Node) Source() *Source {
	return n.source
}

// DocumentIndex returns the 0-indexed position within the file of the
// document the Node belongs to. It counts every document of the file, an
// empty one included, so it is the index of the root in the slice
// [Source.AllDocuments] returns. [Source.Documents] leaves out the empty
// documents beside one with content, so a root below one of them has a
// smaller index in that slice. A Node from [Node.At] reports the index
// of the document that holds it, and the index of the node itself within
// a sequence is the Index of the selector [paths.Path.Last] gives for
// [Node.Path]. The message of a bound error counts documents from 1, as
// it counts lines, so "document 3" in [SourceError.Error] is the document
// at index 2. [SourceError.DocumentIndex] reports the index of the
// document an error is bound to, for a caller that holds the error and
// no Node.
func (n *Node) DocumentIndex() int {
	return n.doc.index
}

// Tokens returns the tokens of the node, with the positions they have in
// the source. For the root of a document they are its preamble, its
// content, and, when no document follows, the comments after a "..."
// marker that ends it, which otherwise become the preamble of the next
// document. They are nil when no token anchors the document, such as one
// with neither a header nor a body. For a Node from [Node.At] they run
// from the first token under the node through the last, comments between
// them included, and are nil when the scope selects nothing. The slice
// is a copy, so reordering it reaches nothing. Every Node shares the
// tokens themselves, so a caller must not modify them.
func (n *Node) Tokens() token.Tokens {
	return slices.Clone(n.content)
}

// Preamble returns the tokens of the document the Node belongs to before
// its content: the comments and %YAML or %TAG directives above its "---"
// header, the header itself, and the comments between the header and the
// first token of the content. The parser cuts the tokens above the header
// off as a node of their own, and [Source.Documents] folds them back into
// the document the YAML spec attaches them to. A schema directive written
// above the header is thus in the preamble of the document it describes.
// A document without content, such as one holding comments alone, is all
// preamble. The slice is a copy of shared tokens that a caller must not
// modify, as for [Node.Tokens].
func (n *Node) Preamble() token.Tokens {
	return slices.Clone(n.doc.tokens[:n.doc.preamble])
}

// FilePath returns the path of the file the document came from, which is
// [Source.FilePath]. It is absolute for a document [NewSourceFromFile]
// read, so a schema routes on the file and not on how the caller
// spelled its path. Returns an empty string when the source has none.
//
// The document [NewSourceFromLayers] builds came from no file, and its
// Source has the path of the lowest layer. Every Node of it returns that path, whichever
// layer holds its value. [Node.Origin] returns the Node of the value in
// the file of its layer, and FilePath of that Node names the file.
func (n *Node) FilePath() string {
	return n.source.FilePath()
}

// FS returns the file system the path of the file the document came from
// names a file in, which is [Source.FS]. It returns nil when the source
// has none, where a path names a file on disk. A Node of the document
// [NewSourceFromLayers] builds returns the file system of the lowest
// layer, as
// [Node.FilePath] describes.
func (n *Node) FS() fs.FS {
	return n.source.FS()
}

// Span returns the lines of [Source.Lines] that the node covers. A whole
// document covers the lines from the one its first token starts on to the
// one before the next document starts, or to the end of the source for
// the last document. The first document also covers the lines above its
// first token, so the spans of a source cover every one of its lines. A
// document after the first with no tokens covers no lines.
//
// A Node from [Node.At] or [Node.Nodes] covers the lines from the first
// token under its node through the last, and a scope that selects nothing
// covers none. A scalar covers its own lines alone. A mapping or a
// sequence also covers the line of the key or the "-" that introduces it,
// so the span holds the token that [Node.Ranges] and an error bound at
// the root of the scope resolve to:
//
//	hours:      # the span of $.hours starts here
//	  open: 9
//
// A path that ends at an alias, such as `$.c` in `c: *x`, covers the
// content of the anchor, which [Node.AST] and [Node.Decode] read, while
// Node.Ranges and an error at its root resolve to the alias. Unless the
// two share a line, [SourceError.Annotate] then marks nothing on
// [Node.View] and reports false, and a caller adds the line of the error
// with [line.View.Slice]. An error at a Node of the document
// [NewSourceFromLayers] builds binds in the file of a layer, so a caller
// marks the view of its [Node.Origin] instead.
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
// Each call returns a view of its own with no decoration, as [Source.View]
// does, and [line.View.Held] returns its lines on their own. The view
// shares its lines with every view over the source, so
// [SourceError.Annotate] marks a bound error on it when the location of
// the error lies in the span, and reports false otherwise, as Node.Span
// describes.
func (n *Node) View() *line.View {
	return line.NewView(n.source.lines, n.span)
}

// Ranges returns the ranges of the token path points at, the token
// [paths.Resolver.Token] resolves, one per line the token spans, without
// the spaces around its content. They are the ranges
// [SourceError.Excerpt] highlights for an [Error] built with [AtPath] at
// that path, and the path resolves as it does in such an Error. A scalar
// covers every line of its text, and a block scalar its indicator. A
// mapping or a sequence covers the token that introduces it, which is the
// key of its entry, the "-" of its element in a block sequence, or else
// its own "{" or "[". A block mapping or a block sequence at the root
// covers its first key or element. A path from [paths.Path.Key] covers
// the key of the entry:
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
// describes, bound to the source as [Node.At] binds it. A token with no
// position returns an error wrapping [ErrNoLocation], and a document that
// did not parse returns the syntax error [Node.Err] returns. Ranges
// returns nil when the value holds no content on any line.
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
// false unless reason wraps [paths.ErrNotFound] and that key has a position.
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
// or false unless reason wraps [paths.ErrAlias]. It reports false for an
// alias under a `<<` merge key too, which may set any key or none.
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
// bare [paths.ErrNotFound] text, since the binding writes the path in front.
func (n *Node) bindUnresolved(path paths.Path, reason error) error {
	if !errors.Is(reason, paths.ErrNotFound) {
		return n.bindOwn(reason)
	}

	if _, ok := n.nearestLocation(path, reason); !ok {
		return n.bindOwn(Invalid(reason))
	}

	return n.bindOwn(Invalid(markedError{err: reason, msg: paths.ErrNotFound.Error()}, AtPath(path)))
}

// Bind binds err to the source of the document, with each path in err
// resolving in this document, an `@` path from the scope of the Node and
// a `$` path from the root. It resolves every location as it binds, so
// the position [SourceError.Error] reports and the excerpt
// [SourceError.Excerpt] returns are fixed from then on. The Node methods
// bind their errors already, so Bind is for an error built elsewhere,
// such as the result of a check the caller runs on a value it took from
// the document:
//
//	return item.Bind(check(value))
//
// The message carries each path from the root, as a `$` path, behind the
// position the path resolved to. The Node puts its [Node.Path] in front
// of each `@` path, as [Rebase] does, in a join, a summary from
// [NewSummary], and the details from [WithDetails] alike. A Node from
// [Node.At] or [Node.Nodes] binds a problem with no location at its own
// value. The root of a document gives such a problem no location, and
// neither does any Node for the error of a context that ended. The
// message of an error with no location names the source, and the
// document in a source of several, as in "name: document 3: msg". An
// Error with a position or a range binds there, and Bind leaves its path
// unresolved. A location declares nothing, so [IsInvalid] reports the
// bound error only when it reports err.
//
// Bind binds the whole tree of err, each problem and detail at its own
// location, as the Architecture section of the package documentation
// describes. To keep several errors as separate bindings, bind each one
// before joining them.
//
// Bind returns nil for a nil err, and for a nil [*Error] or
// [*SourceError] pointer. An error that wraps a [*SourceError], with no
// [*Error] above it that carries a location, heads errors, or holds
// details, is bound already and comes back as it is, so Bind is
// idempotent. Bind never modifies err, and binds the error [BindValue]
// returns as one no source has bound. In a document that did not parse,
// a path resolves nowhere, and [SourceError.Unresolved] returns
// [ErrPathNeedsDocument] wrapping the syntax error.
func (n *Node) Bind(err error) error {
	return bindTree(err, binder{src: n.source, node: n, locate: true})
}

// bindOwn binds an error of a call on n as [Node.Bind] does, except that an
// error with no location gains none, even for a scoped Node, because the
// error is about the call and not the value.
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

	if _, ok := n.source.parsed().result.Index[tk]; ok {
		return true
	}

	return n.doc.tree().Holds(tk)
}

// newDecodeTree creates the [*decodetree.Tree] of the document.
func (d *document) newDecodeTree() *decodetree.Tree {
	src := d.node.source

	return decodetree.New(d.root.Body, d.state.Facts(), decodetree.Options{
		Reparse: func() (ast.Node, map[*token.Token]int) {
			r := src.decodeParse()
			if d.fileIndex < 0 || d.fileIndex >= len(r.File.Docs) {
				return nil, nil
			}

			return r.File.Docs[d.fileIndex].Body, r.Index
		},
		Texts: func() []string {
			return append(spellingTexts(d.tokens), src.referenceTexts()...)
		},
		References: len(src.references) > 0,
	})
}

// rejectionText returns the message and the sentinel [rejection.Classify]
// gives err, with the anchor names of the document and each control
// character escaped, since the decoder copies document text into messages.
func (d *document) rejectionText(err yaml.Error) (string, error) {
	msg, label := rejection.Classify(err)

	return escape.Control(d.tree().RestoreNames(msg)), rejectionLabel(label)
}

// restoreError returns err with each anchor name the [decodetree.Tree]
// gave spelled as the document spells it. It leaves an [*Error] or a
// [*SourceError] alone, since the binding writes their messages.
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

	return markedError{err: err, msg: restored}
}

// spellingTexts returns the texts a new anchor name must not spell, which
// are the joined text of tks and the value of each token that holds " [".
// A value differs from its text where a scalar holds an escape or a fold.
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
// match, or [SourceError.Position] gives one, so a viewer names the value
// under its cursor:
//
//	if path, ok := doc.PathAt(cursor); ok {
//		status = path.String() // $.spec.containers[0].image
//	}
//
// The path goes back to [Node.At] or [Node.Ranges] of any Node of the
// document. The innermost node is an entry of a mapping or an element of
// a sequence. A position on the key of an entry gives a path that ends in
// `~`, as [paths.Path.Key] writes one, and a position on the ":" or the
// value gives the path of the entry. The "-" of a block sequence belongs
// to its element, and the "[", "{", ",", anchor, or tag of a collection
// to the collection. A position in the indentation of a line resolves to
// the token after it. The path names where the document writes the node,
// so a position on an alias gives the entry that holds the alias, and a
// position inside an anchor gives the path where the anchor defines it.
//
// PathAt reports false on a comment, a blank line, a "---" header, a
// "..." marker, or a directive, outside the lines of the source, and in
// another document of the source, so a caller with several documents asks
// each. It also reports false for a document that did not parse or holds
// no content, and inside an entry that no path selects, such as the
// earlier of two entries with one key. A path it returns selects the node
// at pos, and none of its selectors is a wildcard.
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
	// NodeNone is the kind of a Node that holds no value: the root of an
	// empty document, of a document of comments or directives alone, or
	// of a document that did not parse, and a nil Node. The root of a
	// document that is an alias with no anchor before it has this kind
	// too. [Node.IsEmpty] reports the documents with no content among
	// these, and [Node.Err] tells a document that did not parse apart
	// from an empty one.
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
// scalar, or [NodeNone] when it holds no value. A caller that walks a
// document picks the selector that lists the children of the Node from
// it:
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
// `&r {a: 1}` and of `!!map {a: 1}` is thus a mapping, where [Node.AST]
// gives the anchor or the tag that wraps it. A Node that [Node.At] or
// [Node.Nodes] scopes at an alias holds the content of its anchor, so no
// kind stands for an alias. The root of a document that is an alias with
// no anchor before it holds no value.
//
// Every scalar has the one kind. The type a scalar decodes to depends on
// its tag, on its text, and on the Go type a decode fills, so a caller
// that needs it decodes the Node.
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
// and holds no content. Such a document is an empty file, a file of
// whitespace or comments alone, or a "---" header with nothing but
// comments below it. A %YAML or %TAG directive above the header adds no
// content.
//
// IsEmpty describes the document and not a value in it. A document that
// holds any value is not empty, so IsEmpty is false for an empty mapping
// `{}`, an empty sequence `[]`, an empty string `""`, an explicit null
// such as `~`, and an alias, even one with no anchor before it. A caller
// that asks whether a mapping or a sequence holds anything lists its
// entries with [Node.Nodes].
//
// IsEmpty is false for a document that did not parse, for a Node that
// [Node.At] or [Node.Nodes] scopes below the root, and for a nil Node.
// [Node.Kind] is [NodeNone] for every Node IsEmpty reports, and for the
// root of a document that did not parse or that is an alias with no
// anchor before it, so a caller that passes over empty documents asks
// IsEmpty. [SkipEmpty] wraps a [Validator] so that it passes an empty
// document.
func (n *Node) IsEmpty() bool {
	if n == nil || n.doc.err != nil || !n.base.IsRoot() {
		return false
	}

	return !astnode.HasContent(n.doc.root.Body)
}
