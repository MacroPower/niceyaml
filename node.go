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

// documentState returns the [*aliasing.State] of the document of n, which
// must be a [*Node], or nil for a nil Node. It is [aliasing.StateOf],
// through which the packages of the module reach the state a document
// keeps.
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

// document is what describes one YAML document of a [Source] as a whole:
// its root, the tokens of the whole document, its index in the file, the
// length of its preamble, and the resolver its paths resolve through.
// Every [Node] of the document shares it.
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
	// Resolves every path in root, which pathResolver creates when the
	// first path needs it. No Node edits the tree, so the resolver binds
	// the aliases once however many paths the Nodes of the document
	// resolve.
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

// pathResolver returns the [paths.Resolver] for the document, and creates
// it on the first call. A document that did not parse gets a resolver
// with no document, so every path returns an error there. A resolver for
// its stand-in root would list no nodes for a path and return no error,
// as it does for an empty document.
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
// selects. [Node.At] returns the one node a path selects, and
// [Node.Nodes] returns one Node per match of a path that can select
// several, such as one with a `[*]` or `.*` selector. Every method reads
// from the node and resolves an `@` path from it, so [Node.Decode]
// decodes it alone, [Node.Validate] runs a [Validator] on it, and
// [Node.Bind] resolves the `@` paths of an error from it, so a check
// written for the type of a value reports the same lines whether the
// value is the whole document or one inside it. A `$` path resolves from
// the root of the document through any Node.
//
// Receive instances from [Source.Documents], [Source.AllDocuments],
// [Source.Document], [Node.At], [Node.Nodes], [Node.Document],
// [SourceError.Node], or [SourceError.Document].
//
// # Decoding
//
// [Node.Decode] returns a new value and [Node.DecodeInto] fills one the
// caller already holds, such as one pre-populated with defaults. Both run
// the same pipeline. Each [Validator] given with [WithValidator] checks
// the node before decoding, and a value that implements [SelfValidator]
// validates itself after, unless [WithSelfValidation] switches that off.
// [Node.Validate] runs the first step on its own, and [Node.SelfValidate]
// runs the last, on a value the caller may have changed since the decode.
//
//	for _, doc := range docs {
//		config, err := doc.Decode[Config](ctx, niceyaml.WithValidator(validator))
//		if err != nil {
//			return err
//		}
//	}
//
// [Source.Documents] returns the documents of such a loop. It leaves out
// the empty documents beside one with content, so the loop decodes the
// documents with content. A file with no content holds an empty document,
// which [Node.IsEmpty] reports. A validator that wants a mapping rejects
// it, and a decode with no such validator returns the zero value. A
// source that holds one document hands its root out from
// [Source.Document].
//
// A Node holds the Source it came from, and every decoding method binds
// the [Error] values it produces to that source, so the errors it returns
// carry a [SourceError] that renders the offending lines.
//
// # Scoped Nodes
//
// A Node from [Node.At] or [Node.Nodes] is scoped to the node a path
// selects. Decode decodes that node alone, which reads one value without
// decoding the whole document, such as a discriminator field that routes
// the document. [Node.DecodeAt] scopes and decodes in one call, for a
// caller that needs the value and not the Node, and
// [Node.DecodeIfPresent] does so for a value the document may leave out.
// Bind resolves the `@` paths in an error from the node, so a check
// written for the type of that value reports the right lines.
// The bound error carries each path from the root of the document, as a
// `$` path, so it names the value as a decode of the whole document does.
// Any Node reaches the root of its document through [Node.Document], and
// [Node.Path] is [paths.Doc] for the root and the `$` path to the node
// for a scoped Node.
//
// # Whole Document
//
// [Node.DocumentAST], [Node.DocumentIndex], [Node.Preamble], and
// [Node.FilePath] describe the document as a whole, whatever Node of it a
// caller holds. [Node.PathAt] reads the whole document too. It returns
// the `$` path of the node at a position, so a viewer names the value
// under its cursor.
//
// # Unparsed Documents
//
// [Source.AllDocuments] returns a Node for a document with a YAML syntax
// error too. Such a document has no tree, and [Node.Err] returns the
// syntax error. The methods that read the tree return that error:
// [Node.Decode], [Node.DecodeInto], [Node.DecodeAt],
// [Node.DecodeIfPresent], [Node.Validate], [Node.At], [Node.Nodes], and
// [Node.Ranges]. [Node.AST] and
// [Node.DocumentAST] return nil, and [Node.PathAt] finds no node. The
// methods that read the tokens and the lines work as they do for any
// document: [Node.Tokens], [Node.Preamble], [Node.Span], and [Node.View].
// A caller thus renders or diffs a document that does not parse yet, and
// the other documents of the file decode and validate as if it did.
//
// # Merged Documents
//
// A Node of the Source [NewSourceFromLayers] builds belongs to the
// document its layers merge into. Its Source holds the merged text,
// which is no file, so each error it returns binds in the file of a
// layer instead, as NewSourceFromLayers describes.
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
// or nil for a document that parsed. [Source.AllDocuments] returns a Node
// for every document of a file, and a syntax error fails the one document
// that holds it. The error is the [*SourceError] the parser reported for
// that document, bound to the Source. It matches [ErrSyntax], and it is
// among the errors [Source.File] returns:
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
// The error is bound to the document it belongs to, so
// [SourceError.Document] returns the root of that document for it,
// whether [Node.Err], [Source.File], or a method of the Node returned the
// error.
//
// A panic in the parser fails the documents it was reading as a syntax
// error does. Err then returns the error of that panic, which holds a
// [PanicError] and does not match ErrSyntax.
//
// A document that did not parse has no tree. [Node.Decode],
// [Node.DecodeInto], [Node.DecodeAt], [Node.DecodeIfPresent],
// [Node.Validate], [Node.At], [Node.Nodes], and [Node.Ranges] return the
// error Err returns, and no [Validator] runs.
// [Node.AST] and [Node.DocumentAST] return nil, and [Node.PathAt] reports
// false for every position. A path in an error that [Node.Bind] binds
// resolves nowhere, as Bind describes.
//
// A "---" header that directly follows an anchor with no value parses
// together with the document above it, as [Source.AllDocuments]
// describes. Both documents then return the same error, which is bound to
// the one of the two that holds its location. A nil Node has none.
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
// resolves from the receiver, and a `$` path from the root of the
// document, so a path from [Node.Path] or [Node.PathAt] of any Node of
// the document scopes the node it names. The Node shares the source and
// the document with the receiver, and reaches the document through
// [Node.Document]. A path that selects nothing returns an error bound to
// the source, which wraps [paths.ErrNotFound] when nothing exists at the
// path. A validator given to a scoped decode checks the node, and an `@`
// path in an error it or the decoded value reports resolves from the
// node. A check written for a type thus reports the same lines whether
// the type is the whole document or a value inside one:
//
//	hours, err := doc.At(paths.Doc().Child("spec", "hours"))
//	if err != nil {
//		return err
//	}
//
//	h, err := hours.Decode[Hours](ctx, niceyaml.WithValidator(hoursSchema))
//	if err != nil {
//		return err
//	}
//
//	return hours.Bind(check(h))
//
// A node whose tokens carry no position covers no lines and holds no
// tokens.
//
// # Errors
//
// At resolves the node to find the lines and tokens it covers, and a path
// that selects nothing returns the error [paths.Resolver.Node] describes,
// bound to the source: an error wrapping [paths.ErrNotFound] when nothing
// exists at the path, which also wraps [paths.ErrNoDocument] when the
// document has no content at all, such as an empty document or one
// holding only directives; [paths.ErrAlias] when an alias on the path
// does not resolve; [paths.ErrExcessiveMerging] when the key lookups of
// the path read far more nodes under `<<` merge keys than the document
// holds; and [paths.ErrWildcard] for a path that could match several
// nodes, which [Node.Nodes] scopes one by one.
//
// A document that did not parse has no node to select, so At returns the
// syntax error [Node.Err] returns.
//
// # Error Locations
//
// A path that names a key a mapping leaves out says where the value
// belongs, so its error binds as an [Error] with [AtPath] of the path
// binds. The error points at the key of the mapping that lacks the
// value, as [SourceError.Nearest] describes. Its message carries the
// path in front:
//
//	cfg.yaml:3:1: $.server.name: not found
//
// Any other path that selects nothing names no mapping, so its error
// carries no location. That holds for an index past the end of a
// sequence, for a key looked up in a scalar, and for any path in a
// document with no content:
//
//	cfg.yaml: resolve $.items[7]: not found
//
// The document lacks the value in each case, so [IsInvalid] reports
// every error that wraps [paths.ErrNotFound], with a location or without.
//
// # Absent Values
//
// A caller that falls back to a default when a value is absent reads it
// with [Node.DecodeIfPresent]:
//
//	version := 1
//
//	_, err := doc.DecodeIfPresent(ctx, versionPath, &version)
//	if err != nil {
//		return err
//	}
//
// A caller that needs the Node of such a value tests the error of At for
// [paths.ErrNotFound] before it decodes. A decode can return an error
// that matches paths.ErrNotFound for a node that is present, as
// [Node.DecodeAt] describes, so the same test after the decode takes
// that failure for an absent value.
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
// order, so a path with a `[*]`, `.*`, `..name`, or `..*` selector, which
// [Node.At] rejects, scopes every element of a sequence, every entry of a
// mapping, every entry with a name at any depth, or every node at any
// depth. An `@` path resolves from the receiver and a `$` path from the
// root of the document, as for Node.At. A path that selects nothing
// returns no Nodes and no error. Each Node is scoped as one from Node.At
// is, and [Node.Path] is the `$` path that selects its node alone, as
// [paths.Resolver.Matches] resolves it, so a validator run on each element,
// or an error bound to it, reports the element it came from:
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
// # Mapping Entries
//
// A mapping of named things, such as the jobs of a workflow, takes a `.*`
// selector from [paths.Path.ChildAll]. Each Node is then scoped at the
// path of one entry, and the last selector of that path, which
// [paths.Path.Last] gives, names the entry:
//
//	jobs, err := doc.Nodes(paths.Current().Child("jobs").ChildAll())
//	if err != nil {
//		return err
//	}
//
//	for _, job := range jobs {
//		sel, _ := job.Path().Last() // .build
//		fmt.Println(sel.Name)       // build
//		// ...
//	}
//
// The name is the text the selector matches the key by, as
// [paths.Resolver.KeyName] gives it, so a key the source spells `3.10`
// has the name 3.10 and the path `$.jobs.'3.10'`. A `~` selector from
// [paths.Path.Key] reaches the key node itself, which decodes as the
// decoder reads it, so that key decodes to the string 3.1.
//
// # Recursive Selectors
//
// A `..*` selector from [paths.Path.RecursiveAll] scopes every node below
// the receiver, each once, where the source writes it. A check that
// applies to every key of a document thus visits each key once:
//
//	nodes, err := doc.Nodes(paths.Current().RecursiveAll()) // @..*
//	if err != nil {
//		return err
//	}
//
//	for _, n := range nodes {
//		sel, ok := n.Path().Last()
//		if ok && sel.Kind == paths.SelectorChild && !snake.MatchString(sel.Name) {
//			errs = append(errs, niceyaml.NewError(
//				fmt.Sprintf("key %q is not snake_case", sel.Name),
//				niceyaml.AtPath(n.Path().Key())))
//		}
//	}
//
// # Empty Results
//
// A path that selects nothing returns no Nodes and no error, as
// [paths.Resolver.Nodes] does. A document with no content, which
// [Node.IsEmpty] reports, holds nothing for a selector to reach. A path
// with selectors returns no Nodes there too, where Node.At returns an
// error wrapping [paths.ErrNoDocument]. A loop over the items of each
// document of a file thus passes over an empty document. The root path
// selects the null at the "---" header of such a document, and nothing
// in one without a header, such as an empty file.
//
// # Errors
//
// The errors [paths.Resolver.Nodes] returns come back bound to the source:
// one wrapping [paths.ErrAlias] when an alias on the path does not
// resolve, [ErrExcessiveAliasing] when aliases lead a selector of the
// path to far more nodes than the document holds, and
// [paths.ErrExcessiveMerging] when the key lookups of a selector read far
// more nodes under `<<` merge keys than that. A document that did not
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

// scoped returns a copy of n scoped at path, a `$` path that selects
// node, for [Node.At] and [Node.Nodes]. The tokens of the copy are those
// of node, as [Node.extent] finds them, and so are its lines, with one
// addition. The key of an entry introduces a mapping or a
// sequence that is its value, and the "-" of an element of a block
// sequence introduces one that is the element. [astnode.PathToken] gives
// that token. Where it lies on a line above the lines of node, the lines
// start there, so they hold the token [paths.Resolver.Token] resolves for
// path.
//
// A scalar is its own token, and nothing holds the root of a document or
// the key of an entry, so each keeps the lines of its node. So does the
// Node of a path that ends at an alias. Its node is the content of the
// anchor, and the alias the path points at introduces none of it.
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
// tokens run from the first token under the node through the last, in
// source order. The lines run from the one the first token starts on
// through the last one that holds content of a token among them. A node
// whose tokens carry no position covers no lines and holds no tokens. A
// node with no token in the document, such as an implicit null the
// parser adds, holds no tokens and covers the line its token names.
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

	// A token that holds no text can share its offset with a token of
	// another node. The empty content of a block scalar sits where the
	// next key starts, and an implicit null takes the offset of the ":"
	// or "-" before it. A token at either end of the window therefore
	// belongs to the node only when the node holds it.
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

// lastContentLine returns the last line of the source that holds content
// of tk other than spaces. The lexer folds the line breaks and the
// indentation of the next line into a scalar, and a line where tk holds
// only those does not count, so a sibling's line stays out of the span.
// The boolean is false when tk holds no content on any line.
func (n *Node) lastContentLine(tk *token.Token) (int, bool) {
	lines := n.source.lines

	last, found := 0, false

	for i := max(tk.Position.Line-1, 0); i < lines.Len(); i++ {
		sp, ok := lines.Line(i).ContentSpan(tk)
		if !ok {
			// The lines of a token follow one another from the line its
			// position names, so the first line without it ends the
			// search. A token that holds no text, such as the empty
			// content of a block scalar, is on no line.
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
// A Node from [Node.At] or [Node.Nodes] covers the lines of its node,
// from the one the first token under the node starts on through the one
// the last token ends on. A scope that selects nothing covers no lines.
//
// A mapping or a sequence also covers the line of the token that
// introduces it, where that token lies above its content. The token is
// the key of the entry the mapping or the sequence is the value of, or
// the "-" of the element it is in a block sequence:
//
//	hours:      # the span of $.hours starts here
//	  open: 9
//	  close: 5
//
// The span then runs from the line of that token through the last line
// of the node. A flow collection on the line below its key thus starts at
// the key, and so does a mapping or a sequence under an explicit key. A
// line between the key and the content lies in the span too, such as one
// that holds an anchor, a tag, or a comment. [Node.Tokens] returns the
// tokens of the value alone, so the token that introduces it is not
// among them.
//
// A scalar is its own token, so it covers its own lines alone. The line
// of its key is in the span only when the scalar starts on it:
//
//	plain:
//	  one       # the span of $.plain is this line
//
// A scope from [paths.Path.Key] covers the key.
//
// [Node.Ranges] of the scope and an error bound at its root resolve to
// the token the path of the scope points at, as [paths.Resolver.Token]
// describes. That token is the key or the "-" that introduces the node,
// or else a token of the node itself, so the span holds its line.
// [SourceError.Annotate] thus marks such an error on [Node.View].
//
// A path that ends at an alias is the exception. The node of such a
// path, such as `$.c` in `c: *x`, is the content of the anchor. The span
// covers the lines of that content alone, which [Node.AST],
// [Node.Decode], and [Node.Tokens] read. Node.Ranges of the scope and an
// error bound at its root resolve to the alias, where the path points.
// The alias lies outside the span unless it shares a line with that
// content, as it can in a flow collection. Annotate then marks nothing on
// Node.View and reports false. A caller that shows the scope with that
// error adds the line of the error to the view:
//
//	view := hours.View()
//	if !bound.Annotate(view) { // only a path that ends at an alias
//		if pos, ok := bound.Position(); ok {
//			view = hours.Source().View().Slice(hours.Span(), position.NewSpan(pos.Line, pos.Line+1))
//			bound.Annotate(view)
//		}
//	}
//
// A tag on the alias keeps the node at the tag, as [paths.Resolver.Node]
// describes, so the node of `$.c` in `c: !t *x` is the tag with the alias
// under it, and the span covers the line of the alias instead.
//
// An error at a Node of the document [NewSourceFromLayers] builds binds
// in the file of a layer, so it marks nothing on a view of the merged
// text. A caller takes [Node.Origin] of that Node first and marks the
// view of the origin, whose span follows the same rules in the file of
// its layer.
//
// [Node.View] returns a view of the source sliced to the span, so a
// caller that renders the node need not slice one itself. The span
// slices any other view over the source, such as one that carries
// decoration already:
//
//	lipgloss.Println(p.Print(view.Slice(doc.Span())))
func (n *Node) Span() position.Span {
	return n.span
}

// View returns a new [*line.View] over the lines of [Source.Lines] that
// the node covers, [Node.Span], with the line numbers they have in the
// file. A document of a file that holds several, or the node a Node from
// [Node.At] selects, renders on its own:
//
//	lipgloss.Println(p.Print(doc.View()))
//
// The view keeps the index each line has in the source, so a diff or a
// search of the node takes the view and reports lines of the file. The
// views of one document in two revisions of a file that holds several
// diff that document alone:
//
//	result := diff.Diff(before[1].View(), after[1].View())
//
// [line.View.Held] returns the lines of the view on their own, such as
// for the text of the node:
//
//	text := doc.View().Held().Content()
//
// Each call returns a view of its own with no decoration, as [Source.View]
// does, so overlays and annotations added to one reach neither the Source
// nor another view. The view shares its lines with every view over the
// source, so [SourceError.Annotate] marks a bound error on it as on a
// view of the whole source when the location of the error lies in
// [Node.Span]. An error outside the span marks nothing on the view, and
// Annotate reports false for it. An error bound at the root of a scope
// lies in the span, unless the path of the scope ends at an alias on
// another line than the content of the anchor. Node.Span shows how to
// slice a view that holds the line of such an error.
func (n *Node) View() *line.View {
	return line.NewView(n.source.lines, n.span)
}

// Ranges returns the ranges of the token path points at, the token
// [paths.Resolver.Token] resolves, one per line the token spans, without the
// spaces around its content. They are the ranges [SourceError.Excerpt]
// highlights for an [Error] built with [AtPath] at that path, and the path
// resolves as it does in such an Error: an `@` path from the scope of the
// Node, and a `$` path, such as one from [SourceError.Path], from the root
// of the document. A scalar covers every line of its text, and a block
// scalar its indicator. A mapping or a sequence covers the token that
// introduces it: the key of the entry that holds it, the "-" of the
// element it is in a block sequence, or else its own "{" or "[". A block
// mapping or a block sequence at the root covers its first key or its
// first element. A path from [paths.Path.Key] covers the key of the
// entry whatever its value holds. The ranges mark the value on a view of
// the source:
//
//	ranges, err := doc.Ranges(paths.Doc().Child("spec", "replicas"))
//	if err != nil {
//		return err
//	}
//
//	view := doc.View()
//	view.AddOverlay(kind.GenericHighlight, ranges...)
//
// To mark every line of a mapping or sequence, pass the path to [Node.At]
// and use [Node.Span] or [Node.View] of the Node it returns.
//
// A path that does not resolve returns the error [paths.Resolver.Token]
// describes, bound to the source. The error of a path that names a key a
// mapping leaves out binds at that mapping, as it does for [Node.At], and
// [IsInvalid] reports every error that wraps [paths.ErrNotFound]. A
// path whose token carries no
// position returns an error wrapping [ErrNoLocation]. A document that
// did not parse returns the syntax error [Node.Err] returns. Returns nil
// when the value holds no content on any line.
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

// pathLocation returns the location of the token that path resolves to in
// the document, through [paths.Resolver.Token], with an `@` path resolving
// from the scope. The location holds the token and its position. An error
// from [paths.Resolver.Token] names the path already and comes back as it is,
// and a token without a position is [ErrNoLocation].
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

// nearestLocation returns the location an error binds at when its path
// names a key the document leaves out: the key of the mapping that lacks
// it, as [paths.Resolver.Nearest] finds that mapping, with an `@` path
// resolving from the scope. The reason is the error the path failed to
// resolve with. It reports false when that reason is not
// [paths.ErrNotFound], when no mapping lacks the key, and when the key of
// that mapping carries no position.
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

// aliasLocation returns the location an error binds at when its path
// enters an alias the document cannot follow: the token of that alias,
// with an `@` path resolving from the scope. Such an alias names no
// anchor before it, as one to an anchor of a reference document does, or
// lies inside the anchor it names. The reason is the error the path
// failed to resolve with, which the location keeps. It reports false when
// that reason is not [paths.ErrAlias] and when the token of the alias
// carries no position. It also reports false when the alias sits under a
// `<<` merge key. A path enters no alias to read a key of such a mapping,
// and the alias may set any key or none, so it says nothing of where the
// value is.
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

// bindUnresolved binds reason, the error path failed to resolve with from
// the scope of n, as [Node.At] and [Node.Ranges] return it. A reason that
// wraps [paths.ErrNotFound] says the document lacks the value, so it
// binds inside an [Error] from [Invalid], which matches [errInvalid]. A
// path that names a key a mapping leaves out binds as an Error with
// [AtPath] of that path binds, at the key of the mapping that lacks it,
// as [Node.nearestLocation] finds it. The binding writes the path in
// front, so the message is the one of [paths.ErrNotFound] alone, which
// leaves the path out. Any other path that selects nothing names no mapping, so its
// error binds with no location, as [Node.bindOwn] binds it. Any other
// reason is about the call, and it binds as it is.
func (n *Node) bindUnresolved(path paths.Path, reason error) error {
	if !errors.Is(reason, paths.ErrNotFound) {
		return n.bindOwn(reason)
	}

	if _, ok := n.nearestLocation(path, reason); !ok {
		return n.bindOwn(Invalid(reason))
	}

	return n.bindOwn(Invalid(markedError{err: reason, msg: paths.ErrNotFound.Error()}, AtPath(path)))
}

// Bind binds err to the document's source, with the paths in err
// resolving in this document: an `@` path from the scope of the Node, and
// a `$` path from the root of the document. It resolves every location
// in err as it binds, so the position [SourceError.Error] reports and the
// range [SourceError.Range] returns are fixed from then on, and
// [SourceError.Excerpt] returns the excerpt as a view for the caller to
// render. A Node from [Node.At] or [Node.Nodes] binds a problem with no
// location at its own value, and the root of a document gives such a
// problem no location. Bind returns nil for a nil err, and an error that
// is bound already comes back as it is.
//
// # Caller Checks
//
// The Node methods bind the errors they return already. Bind is for an
// error built elsewhere, such as a validator's [*Error] with a path, or
// one from a check the caller runs on a value it took from the document.
// Such an error writes an `@` path from the value, so the Node scoped to
// that value with [Node.At] binds it, and a check written for a type
// takes a pointer to it and goes with any decode of that type:
//
//	item, err := doc.At(path)
//	if err != nil {
//		return err
//	}
//
//	value, err := item.Decode[map[string]any](ctx)
//	if err != nil {
//		return err
//	}
//
//	return item.Bind(check(value))
//
// # Paths
//
// The message of a bound [*Error] carries its path from the root of the
// document, as a `$` path, behind the position the path resolved to. The
// Node puts its own [Node.Path] in front of each `@` path in err, as
// [Rebase] does, and leaves a `$` path as it is. A check that wrote
// `@.price` thus reports `$.items[1].price` when the Node at `$.items[1]`
// binds it, in the message and in [SourceError.Path], as a decode of the
// whole document reports it. A path that names the value by its place in
// the document, such as the [Node.Path] of another Node, starts at `$`
// and binds through any Node of the document. The paths of a join, of the
// errors a summary from [NewSummary] heads, and of the details from
// [WithDetails] change the same way. An Error writes no path into its own
// message, so text that a wrapper such as [fmt.Errorf] added around a
// located Error holds none, and the bound message names the joined path
// once, in front of that text.
//
// # Problems Without Locations
//
// A Node from Node.At or Node.Nodes stands for one value, so a problem
// with no location that it binds is about that value. The Node binds
// such a problem at itself, as it binds an Error with [AtPath] of
// [paths.Current]. A check that returns a plain error thus reports
// `$.items[1]` and the position of that item. The Node reads the roles
// the errors declare, problem by problem, as [Rebase] does:
//
//   - A problem with no location binds at the Node whatever its details
//     carry. An error "ports conflict" above the two ports it names thus
//     keeps its message on the line of the value.
//   - A summary, a join, and a wrapper around either are headings. A
//     heading gains no location, and each problem it heads binds on its
//     own, at the Node when it carries no location, beside located ones.
//   - A detail explains the error above it, so it gains no location.
//
// The Node thus binds an error as the root of the document binds a
// Rebase of it under [Node.Path], so a check bound through a scoped Node
// reports the lines and the paths that the same check run by a
// [SelfValidator] reports. The location places the error and declares
// nothing about it, so [IsInvalid] reports the bound error only when it
// reports err, where it reports every error a SelfValidator returns. An
// error that matches [context.Canceled] or [context.DeadlineExceeded] is
// about the call, so it gains no location either. The root of a document
// gives no error a location, since an error bound there can be about the
// document as a whole, such as a schema that does not load. A caller
// that holds a scoped Node binds such an error through the root
// [Node.Document] returns.
//
// An error that gains no location, such as one from
// [go.jacobcolvin.com/niceyaml/paths] bound through the root of a
// document, binds all the same, and the bound error names the source in
// front of the message, as "name: msg". An error from one file of many
// thus still says which file. In a source that holds more than one
// document, the document stands behind the name, counted from 1, as in
// "name: document 3: msg", so an error from one document of many says
// which document. The message of err stays as it is, and the position
// goes in front of it.
//
// # Positions and Ranges
//
// An Error that carries a position or a range beside its path,
// from [AtPosition] or [AtRange], binds at that position or range instead,
// with the position or the start of the range in front of the message.
// Bind does not resolve the path of such an Error, so a path the document
// does not hold gives no [SourceError.Unresolved] reason.
//
// # Source Binding
//
// [Source.Bind] binds an error to the document its location falls in,
// so a caller that holds the source rather than a document binds there.
// A position or a range finds the document whose span holds it, and a
// path resolves in the one document of a source that holds one. A path
// in a source that holds several resolves nowhere there, with
// [ErrPathNeedsDocument] as the reason, and binds here instead.
//
// # Error Trees
//
// Binding binds the whole tree of err. The [Error] that anchors it gives
// the [SourceError] its location. Every error a summary along the way
// heads becomes a child of the binding in the tree [NewErrorTree] builds,
// and every detail of an Error along the way becomes a child with Detail
// set. An error that unwraps to several, such as one from
// [errors.Join], binds the same way whatever wraps it. The SourceError
// carries no location of its own, and each branch is a child. A wrapper
// that [fmt.Errorf] builds with several %w verbs keeps only its branches
// that carry a location or errors below them, and when one remains, it
// binds where that branch does. Each child binds at the location its own
// error carries, if any, so a validator that joins its violations
// reports each one with its position. To keep several errors as separate
// bindings, bind each one before joining them.
//
// # Nil and Bound Errors
//
// If err is nil, Bind returns nil. A nil [*Error] or [*SourceError]
// pointer as err carries nothing to bind, so Bind returns a nil error for
// it too. A validator that accumulates into a typed pointer and returns
// it on success thus reports no error. Such a pointer inside the chain
// binds nothing, so Bind looks past it. An error that is or wraps a
// [*SourceError] along its cause chain, with no [*Error] above it that
// carries a location, heads errors, or holds details, is bound already,
// to this source or another, and comes back as it is. Binding is thus
// idempotent. A located Error above a binding binds anew at its own
// location, and its message keeps the position the inner binding
// resolved. An Error with details above a binding binds anew around it,
// with those details as children. Bind never modifies err.
//
// One binding stands in no document, which is the error [BindValue]
// returns for a value that came from none. Bind binds the error it was
// made from, as it binds an error that no source bound yet, so a
// validator that returns such a result reports it in the document. Each
// binding below that one binds the same way on its own.
//
// # Unparsed Documents
//
// A document that did not parse has no tree to resolve a path in, so a
// path bound through its Node resolves nowhere. The bound error keeps
// its message and the name of the source, and [SourceError.Unresolved]
// returns [ErrPathNeedsDocument] wrapping the syntax error [Node.Err]
// returns. A position or a range binds there as it does in any document.
func (n *Node) Bind(err error) error {
	return bindTree(err, binder{src: n.source, node: n, locate: true})
}

// bindOwn binds an error of an operation of n, as [Node.Bind] binds it,
// with one difference. The error is about the call, such as a path that
// resolves to no node or a decode target that is no pointer, so an error
// that holds no location gains none, even when n is a Node from [Node.At]
// or [Node.Nodes].
func (n *Node) bindOwn(err error) error {
	return bindTree(err, binder{src: n.source, node: n})
}

// holdsToken reports whether tk is a token of the source's parse, one of
// the tokens the parser built its tree from, or a token of the parse the
// [decodetree.Tree] of the document reads, as [decodetree.Tree.Holds] describes.
// It compares pointers, so a token from another parse, such as the one an
// UnmarshalYAML runs on its bytes, never matches, however closely it
// resembles a token of the source.
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

// rejectionText returns the message [rejection.Classify] writes for err,
// with each anchor named as the document names it, and the sentinel of
// the label Classify gives err. The decoder writes text of the document
// into some messages as it is, such as the name of an unknown field, so
// each control character comes back as its picture and the message stays
// on one line.
func (d *document) rejectionText(err yaml.Error) (string, error) {
	msg, label := rejection.Classify(err)

	return escape.Control(d.tree().RestoreNames(msg)), rejectionLabel(label)
}

// restoreError returns err with a message that spells each name the
// [decodetree.Tree] of the document gave an anchor as the document does.
// It returns err itself when the message names no such anchor, and when
// err holds an [*Error] or a [*SourceError], whose message the binding
// writes from its parts.
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

// spellingTexts returns the texts of tks that a new name must not spell:
// the text of tks, which holds a spelling that runs across tokens, such
// as a tag before a flow sequence, and the value of each token that holds
// " [". A value differs from the text where a quoted scalar holds an
// escape or a scalar folds a line break.
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

// PathAt returns the path of the innermost node of the document at pos,
// and true. The position is in the coordinates of [Source.Lines], where
// line 0 is line 1 of the text, as a cursor in a viewer, the start of a
// [go.jacobcolvin.com/niceyaml/finder.Finder] match, or
// [SourceError.Position] gives one. A viewer thus names the value under
// its cursor:
//
//	if path, ok := doc.PathAt(cursor); ok {
//		status = path.String() // $.spec.containers[0].image
//	}
//
// The path starts at `$`, the root of the document, whichever [Node] of
// the document the caller holds. A `$` path resolves from that root
// through any Node, so the path goes back to the [Node.At] or the
// [Node.Ranges] of the Node that returned it, or of any other Node of
// the document. A position in another document of the source reports
// false, so a caller with a file of several documents asks each one:
//
//	for _, doc := range docs {
//		if path, ok := doc.PathAt(pos); ok {
//			return doc, path
//		}
//	}
//
// The innermost node is an entry of a mapping or an element of a
// sequence. A position on the key of an entry gives a path that ends in
// `~`, as [paths.Path.Key] writes one, and a position on the ":" or the
// value gives the path of the entry. The "-" of a block sequence belongs
// to its element. A collection holds text of its own between its entries
// or elements, such as "[", "{", ",", and the anchor or tag in front of
// it. A position there gives the path of the collection, which is the
// root for a collection or a scalar at the root of the document. A
// position in the indentation of a line resolves to the token that
// follows it, as [line.Lines.TokenAt] describes.
//
// The path is the place where the document writes the node. A position
// on an alias gives the path of the entry or element that holds the
// alias. A position inside the content of an anchor gives the path where
// the anchor defines that content, though a path through an alias or a
// `<<` merge key reaches the same node.
//
// PathAt reports false where no node of the document lies: on a comment,
// on a blank line, on a "---" header, a "..." marker, or a directive, and
// outside the lines of the source. It reports false for a document that
// did not parse, as [Node.Err] describes, and for one with no content.
// It also reports false for a position inside an entry that no path
// selects. Such an entry is the earlier of two entries with one key, one
// that a `<<` merge key after it overrides, or one whose key has no
// name, as [paths.Resolver.KeyName] reports. A path it returns therefore
// selects the node at pos, and none of its selectors is a wildcard.
//
// The first call reads the tree of the document once, and every Node of
// the document shares what it found.
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
// `{}`, an empty sequence `[]`, and an empty string `""`. It is false
// for an explicit null such as `null` or `~`, and for an alias, even one
// with no anchor before it. A caller that asks whether a mapping or a
// sequence holds anything lists its entries with [Node.Nodes].
//
// It is false for a document that did not parse, which [Node.Err]
// reports, for a Node that [Node.At] or [Node.Nodes] scopes below the
// root, and for a nil Node. [Node.Kind] is [NodeNone] for every Node
// IsEmpty reports. Kind is NodeNone too for the root of a document that
// did not parse and for a root that is an alias with no anchor before
// it, so a caller that passes over empty documents asks IsEmpty.
//
// Every document validates and decodes, an empty one included. A file
// of several documents often holds empty ones. A trailing "---" leaves
// one, and so does a Helm template that renders to a comment alone.
// [Source.Documents] leaves those out of a file that holds a document
// with content, and [Source.AllDocuments] returns them. A file with no
// content holds one empty document, which Documents returns. A loop
// that reads the documents with content passes over the ones IsEmpty
// reports:
//
//	for _, doc := range docs {
//		if doc.IsEmpty() {
//			continue
//		}
//
//		manifest, err := doc.Decode[Manifest](ctx)
//		// ...
//	}
//
// [SkipEmpty] wraps a [Validator] so that it passes an empty document
// wherever it runs, such as in a decode.
func (n *Node) IsEmpty() bool {
	if n == nil || n.doc.err != nil || !n.base.IsRoot() {
		return false
	}

	return !astnode.HasContent(n.doc.root.Body)
}
