package niceyaml

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/tokens"
)

// SelfValidator is implemented by types that validate themselves.
//
// [Document.Decode] and [Document.DecodeInto] call Validate
// after decoding into a value that implements it, unless
// [WithSelfValidation] switches that off.
//
// An [*Error] the value returns writes its path from the value's own
// root, so a type that delegates to a field's Validate puts the result
// under the field with [Rebase]:
//
//	func (c Config) Validate() error {
//		return niceyaml.Rebase(c.Hours.Validate(), paths.Root().Child("hours"))
//	}
type SelfValidator interface {
	Validate() error
}

// Validator is implemented by types that validate a whole [*Document]
// before it decodes, such as a JSON schema or a schema registry that picks
// the schema from the document's content or file path.
//
// Pass one to [Document.Decode] with [WithValidator], or run one on its own
// with [Document.Validate]. The document is the scope that runs the
// validator: the whole document, or the node a Document from
// [Document.At] is scoped to, so a validator given to a scoped decode
// checks that node and its paths resolve from it. A validator that checks
// the decoded data decodes the document itself, and the context carries
// cancellation and deadlines to validators doing cancellable work, such as
// remote schema reference resolution:
//
//	func (s *Schema) Validate(ctx context.Context, doc *niceyaml.Document) error {
//		data, err := doc.Decode[any](ctx)
//		if err != nil {
//			return err
//		}
//
//		return s.check(ctx, data)
//	}
//
// A validator that knows a location returns an unbound [*Error], and the
// Document binds it to the source with itself as the document its path
// resolves in. A validator that binds an error itself does so through
// [Document.Bind], since the Document leaves a bound error as it is.
//
// See [ValidatorFunc], [go.jacobcolvin.com/niceyaml/schema.Schema],
// and [go.jacobcolvin.com/niceyaml/schema.Registry] for
// implementations.
type Validator interface {
	Validate(ctx context.Context, doc *Document) error
}

// ValidatorFunc adapts a function to the [Validator] interface.
//
//	kindPath := paths.Root().Child("kind")
//	known := niceyaml.ValidatorFunc(func(ctx context.Context, doc *niceyaml.Document) error {
//		node, err := doc.At(kindPath)
//		if err != nil {
//			return err
//		}
//
//		kind, err := node.Decode[string](ctx)
//		if err != nil {
//			return err
//		}
//
//		if kind != "Deployment" {
//			return niceyaml.NewError("unknown kind", niceyaml.AtPath(kindPath))
//		}
//
//		return nil
//	})
type ValidatorFunc func(ctx context.Context, doc *Document) error

// Validate implements [Validator].
func (f ValidatorFunc) Validate(ctx context.Context, doc *Document) error {
	return f(ctx, doc)
}

// newDocuments creates one [*Document] per YAML document of file, the AST
// src parsed, in file order. See alignDocumentTokens for how each document
// node finds its tokens and foldPreambles for which nodes become documents.
func newDocuments(src *Source, file *ast.File) []*Document {
	docs := foldPreambles(file.Docs, alignDocumentTokens(file, src.Tokens()))

	groups := make([]token.Tokens, len(docs))
	for i, doc := range docs {
		groups[i] = doc.tokens
	}

	spans := documentSpans(groups, src.lines.Len())

	for i, doc := range docs {
		doc.source = src
		doc.content = doc.tokens
		doc.span = spans[i]
		doc.index = i
		doc.preamble = preambleLen(doc.tokens)
	}

	return docs
}

// foldPreambles pairs each document node with its token group and folds
// the nodes that are not YAML documents into the ones that are. The parser
// makes a node with no header and no content from the comments and %YAML
// or %TAG directives above a "---" header, and from the comments after a
// "..." marker. Such a node joins the next document as its preamble,
// whichever of the two it holds, or the last document when no document
// follows it. A file that holds such nodes and nothing else, such as a
// file of comments, keeps the first as its one document, which decodes to
// nothing as an empty file does.
func foldPreambles(nodes []*ast.DocumentNode, groups []token.Tokens) []*Document {
	var (
		docs    []*Document
		pending token.Tokens
	)

	for i, node := range nodes {
		if isPreambleNode(node) {
			pending = append(pending, groups[i]...)

			continue
		}

		docs = append(docs, &Document{doc: node, tokens: slices.Concat(pending, groups[i])})
		pending = nil
	}

	switch {
	case len(docs) > 0 && len(pending) > 0:
		last := docs[len(docs)-1]
		last.tokens = append(last.tokens, pending...)

	case len(docs) == 0 && len(nodes) > 0:
		docs = append(docs, &Document{doc: nodes[0], tokens: pending})
	}

	return docs
}

// isPreambleNode reports whether node is one the parser cut off from the
// document it belongs to: a node with no "---" header whose body holds no
// value. A node with a header and a body of comments is an explicit empty
// document and stays one.
func isPreambleNode(node *ast.DocumentNode) bool {
	return node.Start == nil && !hasContent(node.Body)
}

// preambleLen returns the number of tokens at the start of tks before the
// document's content: comments, the tokens of each %YAML or %TAG directive
// line, and the "---" and "..." markers. A stream without content is all
// preamble.
func preambleLen(tks token.Tokens) int {
	inDirective, directiveLine := false, 0

	for i, tk := range tks {
		if tk.Type == token.DirectiveType {
			// The lexer splits a directive line into a directive token and
			// the tokens holding its value, which read as content unless
			// the rest of the line goes with the directive.
			inDirective = tk.Position != nil
			if inDirective {
				directiveLine = tk.Position.Line
			}

			continue
		}

		if inDirective && tk.Position != nil && tk.Position.Line == directiveLine {
			continue
		}

		switch tk.Type {
		case token.CommentType, token.DocumentHeaderType, token.DocumentEndType:
			continue

		default:
			return i
		}
	}

	return len(tks)
}

// documentSpans returns the lines of a view of total lines that each token
// group covers. The groups partition the file in order, so a group runs
// from the line its first token starts on to the line the next group starts
// on, the first group runs from the top of the view, and the last group runs
// to the end of it. A group after the first with no tokens covers no lines
// and sits where the next group starts.
func documentSpans(groups []token.Tokens, total int) []position.Span {
	spans := make([]position.Span, len(groups))

	end := total
	for i := len(groups) - 1; i >= 0; i-- {
		start := end
		if len(groups[i]) > 0 {
			start = min(end, max(0, groups[i][0].Position.Line-1))
		}

		spans[i] = position.NewSpan(start, end)
		end = start
	}

	// The lines above the first group's first token, such as blank lines or
	// a comment the lexer hangs off a later token, belong to no later group,
	// so the first group takes them the way the last group takes the lines
	// below its last token. A first group with no tokens takes them too,
	// which keeps the spans covering every line of the view.
	if len(groups) > 0 {
		spans[0] = position.NewSpan(0, spans[0].End)
	}

	return spans
}

// alignDocumentTokens pairs every document in file with the token group it
// starts in, returning one entry per document in file order.
//
// The groups come from [tokens.SplitDocuments]. Each document anchors at
// the offset of its header token, or of its body's first token when it has
// no header, and takes the groups from the first one no earlier document
// claimed up to the one the next document anchors in, so a group that starts
// ahead of the first anchor, such as a leading "..." marker, joins the
// document below it. Matching by offset rather than by index keeps a
// document paired with its own tokens when the parser and the splitter
// disagree on boundaries. The splitter cuts a group at every header, while
// the parser collapses consecutive headers into one document, so such a
// document spans several groups and takes them all. A document with no
// anchor gets nil tokens.
func alignDocumentTokens(file *ast.File, tks token.Tokens) []token.Tokens {
	var (
		groups []token.Tokens
		starts []int
	)

	for _, group := range tokens.SplitDocuments(tks) {
		if len(group) == 0 || group[0].Position == nil {
			continue
		}

		groups = append(groups, group)
		starts = append(starts, group[0].Position.Offset)
	}

	anchors := make([]int, len(file.Docs))
	anchored := make([]bool, len(file.Docs))

	for i, doc := range file.Docs {
		anchors[i], anchored[i] = documentOffset(doc)
	}

	result := make([]token.Tokens, len(file.Docs))

	// The first group no document has claimed, so a group that no anchor
	// reaches back to still joins a document instead of being dropped.
	next := 0

	for i := range file.Docs {
		if !anchored[i] {
			continue
		}

		// Index of the last group that starts at or before the anchor.
		idx := sort.Search(len(starts), func(j int) bool { return starts[j] > anchors[i] }) - 1
		if idx < 0 {
			continue
		}

		idx = min(idx, next)

		// The groups before the one the next anchored document starts in
		// belong to this one, and the last document takes the rest.
		end := len(starts)

		for j := i + 1; j < len(file.Docs); j++ {
			if anchored[j] {
				end = sort.Search(len(starts), func(k int) bool { return starts[k] > anchors[j] }) - 1

				break
			}
		}

		next = max(end, idx+1)
		result[i] = slices.Concat(groups[idx:next]...)
	}

	return result
}

// documentOffset returns the offset of the token that anchors doc: its header
// token, or its body's first token when it has no header. The boolean is
// false when doc has neither.
func documentOffset(doc *ast.DocumentNode) (int, bool) {
	if doc.Start != nil && doc.Start.Position != nil {
		return doc.Start.Position.Offset, true
	}

	if doc.Body != nil {
		if tk := doc.Body.GetToken(); tk != nil && tk.Position != nil {
			return tk.Position.Offset, true
		}
	}

	return 0, false
}

// Document decodes and validates a single YAML document.
//
// [Document.Decode] returns a new value and
// [Document.DecodeInto] fills one the caller already holds, such as
// one pre-populated with defaults. Both run the same pipeline. Each
// [Validator] given with [WithValidator] checks the document before
// decoding, and a value that implements [SelfValidator] validates itself after,
// unless [WithSelfValidation] switches that off. [Document.Validate] runs
// the first step on its own.
//
//	for _, doc := range docs {
//		config, err := doc.Decode[Config](ctx, niceyaml.WithValidator(validator))
//		if err != nil {
//			return err
//		}
//	}
//
// A source that holds one document hands it out from [Source.Document].
//
// A Document is a scope. [Document.At] returns one scoped to the node a
// path selects, and every method of that Document reads and resolves from
// the node. Decode decodes the node alone, which reads one value without
// decoding the whole document, such as a discriminator field that routes
// the document, and Bind resolves the paths in an error from the node, so
// a check written for the type of that value reports the right lines.
//
// A Document holds the [*Source] it came from, and every decoding method
// binds the [Error] values it produces to that source, so the errors it
// returns carry a [SourceError] that renders the offending lines.
//
// Receive instances from [Source.Documents] or [Document.At].
type Document struct {
	source *Source
	doc    *ast.DocumentNode
	// The tokens of the whole document, whatever the scope.
	tokens token.Tokens
	// The tokens of the node the scope selects: tokens for a whole
	// document, and a sub-slice of them for a Document from At.
	content token.Tokens
	// The scope: the path from the document root to the node the Document
	// resolves from, which is the root for a whole document.
	base paths.Path
	// The lines of the source that the node the scope selects covers.
	span  position.Span
	index int
	// The number of tokens at the start of tokens before the content.
	preamble int
}

// Root returns the [*ast.DocumentNode] of the whole document, whatever
// node the Document is scoped to. [Document.Node] returns the node the
// scope selects, and [Document.Path] is the path from this root to it.
func (dd *Document) Root() *ast.DocumentNode {
	return dd.doc
}

// Node returns the node the Document is scoped to, resolved in the
// document as [Document.Decode] and the other scoped methods resolve it:
// the body of the whole document for a Document from [Source.Documents],
// and the node its path selects for one from [Document.At]. The text of
// any node, including a mapping or a sequence, is its String method:
//
//	scoped, err := doc.At(path)
//	if err != nil {
//		return err
//	}
//
//	node, err := scoped.Node()
//	if err != nil {
//		return err
//	}
//
//	fmt.Println(node.String())
//
// The body is what the parser built: nil for an empty document, and a
// comment group for one holding only comments. Node returns either
// without an error, as such a document decodes to nothing.
func (dd *Document) Node() (ast.Node, error) {
	if dd.base.IsRoot() {
		return dd.doc.Body, nil
	}

	node, err := dd.base.Node(dd.doc)

	return node, dd.Bind(err)
}

// At returns a [*Document] scoped to the node path selects, with path
// resolving from the scope of the receiver. The scoped Document shares
// the source and the document with the receiver. [Document.Root],
// [Document.Index], [Document.Preamble], [Document.FilePath], and
// [Document.Source] describe the enclosing document as the receiver does,
// while [Document.Node], [Document.Span], [Document.Tokens],
// [Document.Decode], [Document.DecodeInto], [Document.Validate],
// [Document.Bind], and [Document.Ranges] read and resolve from the node.
// A validator given to a scoped decode checks the node, and a path in an
// error it or the decoded value reports resolves from the node, so a
// check written for a type reports the same lines whether the type is
// the whole document or a value inside one:
//
//	hours, err := doc.At(paths.Root().Child("spec", "hours"))
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
// At resolves the node to find the lines and tokens it covers, and a path
// that selects nothing returns the error [paths.Path.Node] describes,
// bound to the source: an error wrapping [paths.ErrNotFound] when nothing
// exists at the path, which also wraps [paths.ErrNoDocument] when the
// document has no content at all, such as an empty document or one
// holding only directives; [paths.ErrAlias] when an alias on the path
// does not resolve; and [paths.ErrWildcard] for a path that could match
// several nodes. A caller that falls back when a value is absent checks
// for [paths.ErrNotFound]:
//
//	version := 1
//
//	node, err := doc.At(versionPath)
//	if err == nil {
//		version, err = node.Decode[int](ctx)
//	}
//
//	if err != nil && !errors.Is(err, paths.ErrNotFound) {
//		return err
//	}
//
// A node whose tokens carry no position covers no lines and holds no
// tokens.
func (dd *Document) At(path paths.Path) (*Document, error) {
	c := *dd
	c.base = dd.base.Join(path)

	node, err := c.base.Node(dd.doc)
	if err != nil {
		// Bind to the receiver, a Document that exists, rather than to
		// the copy, whose scope moved to a path that resolves to no
		// node.
		return nil, dd.Bind(err)
	}

	c.span, c.content = dd.extent(node)

	return &c, nil
}

// extent returns the lines and the tokens of node in the document: the
// tokens of the document from the first token under the node through the
// last, in source order, and the lines from the one the first starts on
// through the one the last ends on. A node whose tokens carry no position
// covers no lines and holds no tokens.
func (dd *Document) extent(node ast.Node) (position.Span, token.Tokens) {
	first, last := tokenBounds(node)
	if first == nil || last == nil {
		return position.Span{}, nil
	}

	var tks token.Tokens

	for _, tk := range dd.tokens {
		if tk == nil || tk.Position == nil {
			continue
		}

		if tk.Position.Offset >= first.Position.Offset && tk.Position.Offset <= last.Position.Offset {
			tks = append(tks, tk)
		}
	}

	if len(tks) == 0 {
		return position.Span{}, nil
	}

	start := tks[0].Position.Line - 1
	end := start

	// The content of the last token ends the span. Its text runs on
	// through the line breaks and the indentation of the next line, which
	// the lexer folds into a scalar, so the ranges of the text would put a
	// sibling's line into the span.
	for _, r := range dd.source.lines.ContentRanges(tks[len(tks)-1]) {
		end = max(end, r.LastLine())
	}

	total := dd.source.lines.Len()

	return position.NewSpan(min(max(start, 0), total), min(end+1, total)), tks
}

// isNilNode reports whether node is nil, including a typed nil a
// hand-built tree may hold behind a non-nil interface.
func isNilNode(node ast.Node) bool {
	return node == nil || reflect.ValueOf(node).IsNil()
}

// tokenBounds returns the tokens under node with the lowest and the
// highest offset, comments included, or nil when no token under node
// carries a position.
func tokenBounds(node ast.Node) (*token.Token, *token.Token) {
	if isNilNode(node) {
		return nil, nil
	}

	var b boundsFinder

	ast.Walk(&b, node)

	return b.first, b.last
}

// boundsFinder is an [ast.Visitor] that records the tokens with the lowest
// and the highest offset among the nodes it visits.
type boundsFinder struct {
	first, last *token.Token
}

// Visit implements [ast.Visitor].
func (b *boundsFinder) Visit(node ast.Node) ast.Visitor {
	if isNilNode(node) {
		return nil
	}

	b.consider(node.GetToken())

	// The walk visits the nodes of a collection, not the token that closes
	// a flow collection, which the node holds beside them.
	switch n := node.(type) {
	case *ast.SequenceNode:
		b.consider(n.End)
	case *ast.MappingNode:
		b.consider(n.End)
	}

	return b
}

// consider widens the bounds to tk. A nil token, or one without a
// position, changes nothing.
func (b *boundsFinder) consider(tk *token.Token) {
	if tk == nil || tk.Position == nil {
		return
	}

	if b.first == nil || tk.Position.Offset < b.first.Position.Offset {
		b.first = tk
	}

	if b.last == nil || tk.Position.Offset > b.last.Position.Offset {
		b.last = tk
	}
}

// Path returns the scope of the [Document]: the path from the document
// root to the node the Document resolves from, which is [paths.Root] for
// a Document from [Source.Documents] and the joined paths for one from
// [Document.At].
func (dd *Document) Path() paths.Path {
	return dd.base
}

// Source returns the [*Source] the document came from.
func (dd *Document) Source() *Source {
	return dd.source
}

// Index returns the 0-indexed position of the enclosing document within
// the file, whatever node the Document is scoped to.
func (dd *Document) Index() int {
	return dd.index
}

// Tokens returns the tokens of the node the Document is scoped to, with
// the positions they have in the source. For a whole document they are
// its preamble, its content, and, when no document follows, the comments
// after a "..." marker that ends it, which otherwise become the preamble
// of the next document. They are nil when no token anchors the document,
// such as one with neither a header nor a body. For a Document from
// [Document.At] they run from the first token under the node through the
// last, comments between them included, and are nil when the scope
// selects nothing. The slice is a copy, so reordering it reaches nothing,
// while the tokens themselves are shared and read-only.
func (dd *Document) Tokens() token.Tokens {
	return slices.Clone(dd.content)
}

// Preamble returns the tokens of the enclosing document before its
// content, whatever node the Document is scoped to: the
// comments and %YAML or %TAG directives above its "---" header, the header
// itself, and the comments between the header and the first token of the
// content. The parser cuts the tokens above the header off as a node of
// their own, and [Source.Documents] folds them back into the document the
// YAML spec attaches them to, so a schema directive written above the
// header is in the preamble of the document it describes. A document
// without content, such as one holding comments alone, is all preamble.
// The slice is a copy, and the tokens are shared and read-only, as for
// [Document.Tokens].
func (dd *Document) Preamble() token.Tokens {
	return slices.Clone(dd.tokens[:dd.preamble])
}

// FilePath returns the path of the file the document came from, which is
// [Source.FilePath]. Returns an empty string when the source has none.
func (dd *Document) FilePath() string {
	return dd.source.FilePath()
}

// Span returns the lines of [Source.Lines] that the node the Document is
// scoped to covers. A whole document covers the lines from the one its
// first token starts on to the one before the next document starts, or
// to the end of the source for the last document. The first document also
// covers the lines above its first token, so the spans of a source cover
// every one of its lines. A document after the first with no tokens covers
// no lines.
//
// A Document from [Document.At] covers the lines from the one the first
// token under its node starts on through the one the last token ends on.
// The node of a mapping entry is its value, so the line of the key is in
// the span only when the value starts on it, and a scope from
// [paths.Path.Key] covers the key. A scope that selects nothing covers no
// lines.
//
// [Document.View] returns a view of the source sliced to the span, so a
// caller that renders the document need not slice one itself. The span
// slices any other view over the source, such as one that carries
// decoration already:
//
//	fmt.Println(p.Print(view.Slice(doc.Span())))
func (dd *Document) Span() position.Span {
	return dd.span
}

// View returns a new [*line.View] over the lines of [Source.Lines] that
// the Document covers, [Document.Span], with the line numbers they have in
// the file. A document of a file that holds several, or the node a
// Document from [Document.At] is scoped to, renders on its own:
//
//	fmt.Println(p.Print(doc.View()))
//
// Each call returns a view of its own with no decoration, as [Source.View]
// does, so overlays and annotations added to one reach neither the Source
// nor another view. The view shares its lines with every view over the
// source, so a bound error from the document marks it through
// [SourceError.Annotate] as it marks a view of the whole source.
func (dd *Document) View() *line.View {
	return dd.source.View().Slice(dd.span)
}

// Ranges returns the ranges the node at path covers, one per line, without
// the spaces around its content: the ranges [SourceError.Excerpt] highlights
// for an [Error] built with [AtPath] at that path. The path resolves
// from the scope of the Document, as it does in such an Error. A block
// scalar covers its indicator, and a path from [paths.Path.Key] covers
// the key of the entry rather than its value. The ranges highlight the
// value on a view of the source:
//
//	ranges, err := doc.Ranges(paths.Root().Child("spec", "replicas"))
//	if err != nil {
//		return err
//	}
//
//	view := doc.View()
//	view.AddOverlay(kind.GenericHighlight, ranges...)
//
// A path that does not resolve returns the error [paths.Path.Token]
// describes, bound to the source, and a path whose token carries no
// position returns an error wrapping [ErrNoLocation]. Returns nil when the
// value holds no content on any line.
func (dd *Document) Ranges(path paths.Path) (position.Ranges, error) {
	pos, err := dd.position(path)
	if err != nil {
		return nil, dd.Bind(err)
	}

	lines := dd.source.lines

	return lines.ContentRanges(lines.TokenAt(pos)), nil
}

// position returns the position of the token that path resolves to in the
// document, through [paths.Path.Token], with path resolving from the
// scope. An error from it names the path already and comes back as it is,
// and a token without a position is [ErrNoLocation].
func (dd *Document) position(path paths.Path) (position.Position, error) {
	tk, err := dd.base.Join(path).Token(dd.doc)
	if err != nil {
		//nolint:wrapcheck // The paths error already names the path.
		return position.Position{}, err
	}

	if tk == nil || tk.Position == nil {
		return position.Position{}, fmt.Errorf("%w: token at path has no position", ErrNoLocation)
	}

	return position.NewFromToken(tk), nil
}

// Validate runs each validator on the document in the order given and
// stops at the first that fails. It is the validation step of
// [Document.Decode] on its own, for a caller that checks a document without
// decoding it:
//
//	for _, doc := range docs {
//		if err := doc.Validate(ctx, reg); err != nil {
//			return err
//		}
//	}
//
// An error from a validator comes back bound to the source as a
// [SourceError] through [Document.Bind], so an [*Error] renders its
// location and any other error names the source.
func (dd *Document) Validate(ctx context.Context, validators ...Validator) error {
	for _, dv := range validators {
		err := dv.Validate(ctx, dd)
		if err != nil {
			return dd.Bind(err)
		}
	}

	return nil
}

// Bind binds err to the document's source, with the paths in err
// resolving in this document from its scope. It resolves every location
// in err as it binds, so the position [SourceError.Error] reports and the
// range [SourceError.Range] returns are fixed from then on, and
// [SourceError.Excerpt] returns the excerpt as a view for the caller to
// render.
//
// The Document methods bind the errors they return already. Bind is for
// an error built elsewhere, such as a validator's [*Error] with a path, or
// one from a check the caller runs on a value it took from the document.
// Such an error writes its path from the value, so the Document scoped
// to that value with [Document.At] binds it:
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
// The message of a bound [*Error] keeps the path as the error wrote it,
// and the position in front of it is the one the path resolved to from
// the scope.
//
// An error without a location, such as one from
// [go.jacobcolvin.com/niceyaml/paths], binds all the same, and the bound
// error names the source in front of the message, as "name: msg", so an
// error from one file of many still says which file. The message of err
// stays as it is, and the position goes in front of it, so bind such an
// error before adding context with [fmt.Errorf] to keep the position
// beside the message:
//
//	fmt.Errorf("document %d: %w", i, doc.Bind(err))
//
// [Source.Bind] binds an error to the document its location falls in,
// so a caller that holds the source rather than a document binds there.
// A position or a range finds the document whose span holds it, and a
// path resolves in the one document of a source that holds one. A path
// in a source that holds several resolves nowhere there, with
// [ErrPathNeedsDocument] as the reason, and binds here instead.
//
// Binding binds the whole tree of err. The [Error] that anchors it gives
// the [SourceError] its location, and every error nested with
// [WithErrors] along the way becomes a child with a location of its own,
// which [SourceError.Errors] returns. An error that unwraps to several,
// such as one from [errors.Join], binds the same way whatever wraps it.
// The SourceError carries no location of its own, and each branch is a
// child with its own, so a validator that joins its violations reports
// each one with its position. To keep several errors as separate
// bindings, bind each one before joining them.
//
// If err is nil, Bind returns nil. A nil [*Error] or [*SourceError]
// pointer as err carries nothing to bind and also returns a nil error, so
// a validator that accumulates into a typed pointer and returns it on
// success reports no error, and one inside the chain binds nothing, so
// Bind looks past it. An error that is or wraps a [*SourceError] along its
// cause chain, with no located [*Error] above it, is bound already, to
// this source or another, and comes back as it is, so binding is
// idempotent. A located Error above a binding binds anew at its own
// location, with the position the inner binding resolved kept in its
// message. Bind never modifies err.
func (dd *Document) Bind(err error) error {
	return bindTree(err, binder{src: dd.source, doc: dd})
}

// DecodeOption configures [Document.Decode] and [Document.DecodeInto].
//
// Available options:
//   - [WithValidator]
//   - [WithSelfValidation]
//   - [WithDisallowUnknownFields]
//   - [WithYAMLDecodeOptions]
type DecodeOption func(*decodeConfig)

// decodeConfig holds the settings a [DecodeOption] configures.
type decodeConfig struct {
	validators            []Validator
	yamlOpts              []yaml.DecodeOption
	selfValidation        bool
	disallowUnknownFields bool
}

// newDecodeConfig applies opts over the defaults.
func newDecodeConfig(opts []DecodeOption) decodeConfig {
	cfg := decodeConfig{selfValidation: true}
	for _, opt := range opts {
		opt(&cfg)
	}

	return cfg
}

// decodeOptions returns the go-yaml options for one decode: the escape
// hatch options as given, then the ones the named settings stand for.
func (c decodeConfig) decodeOptions() []yaml.DecodeOption {
	if !c.disallowUnknownFields {
		return c.yamlOpts
	}

	return append(slices.Clone(c.yamlOpts), yaml.DisallowUnknownField())
}

// WithValidator is a [DecodeOption] that validates the document with dv
// before decoding it, and a validation error ends the decode before any
// typed decoding. Several validators run in the order given, stopping at
// the first that fails. A [go.jacobcolvin.com/niceyaml/schema.Schema]
// checks the document against one JSON schema, and a
// [go.jacobcolvin.com/niceyaml/schema.Registry] against the schema
// it picks for the document:
//
//	config, err := doc.Decode[Config](ctx, niceyaml.WithValidator(reg))
func WithValidator(dv Validator) DecodeOption {
	return func(c *decodeConfig) {
		c.validators = append(c.validators, dv)
	}
}

// WithSelfValidation is a [DecodeOption] that sets whether a decoded value
// that implements [SelfValidator] validates itself after decoding. The default
// is true. Validators given with [WithValidator] run either way.
func WithSelfValidation(enabled bool) DecodeOption {
	return func(c *decodeConfig) {
		c.selfValidation = enabled
	}
}

// WithDisallowUnknownFields is a [DecodeOption] that sets whether a mapping
// key with no field in the target struct is an error. The default is false,
// and unknown keys are then ignored.
func WithDisallowUnknownFields(disallow bool) DecodeOption {
	return func(c *decodeConfig) {
		c.disallowUnknownFields = disallow
	}
}

// WithYAMLDecodeOptions is a [DecodeOption] that passes [yaml.DecodeOption]
// values to the go-yaml decoder for this call, after the ones the [Source]
// sends for every decode. It is the escape hatch for decoder settings that
// have no option of their own.
func WithYAMLDecodeOptions(opts ...yaml.DecodeOption) DecodeOption {
	return func(c *decodeConfig) {
		c.yamlOpts = append(c.yamlOpts, opts...)
	}
}

// DecodeInto validates and decodes the document, or the node a Document
// from [Document.At] is scoped to, into v, which must be a non-nil
// pointer. Any other v returns [ErrDecodeTarget] before anything runs.
//
// Each [Validator] from [WithValidator] runs before decoding. If v
// implements [SelfValidator], DecodeInto calls Validate after decoding
// succeeds, unless [WithSelfValidation] switches that off. Fields absent
// from the document keep their existing values, so v may be pre-populated
// with defaults. YAML decoding errors, and [Error] values from the
// validators, come back bound to the source as [SourceError] values, with
// a path in them resolving from the scope.
//
// An alias inside the node resolves against the anchors of the whole
// document, so a value that refers to an anchor defined outside it decodes
// as it does in the whole document.
func (dd *Document) DecodeInto(ctx context.Context, v any, opts ...DecodeOption) error {
	err := checkDecodeTarget(v)
	if err != nil {
		return err
	}

	node, err := dd.Node()
	if err != nil {
		return err
	}

	cfg := newDecodeConfig(opts)

	err = dd.Validate(ctx, cfg.validators...)
	if err != nil {
		return err
	}

	err = dd.decodeNode(ctx, node, v, cfg.decodeOptions())
	if err != nil {
		return err
	}

	if !cfg.selfValidation {
		return nil
	}

	if validator, ok := v.(SelfValidator); ok {
		return dd.Bind(validator.Validate())
	}

	return nil
}

// checkDecodeTarget returns [ErrDecodeTarget] unless v is a non-nil
// pointer. The go-yaml decoder panics on a nil interface and decodes
// nothing into a nil pointer, so the check runs before v reaches it.
func checkDecodeTarget(v any) error {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return fmt.Errorf("%w: got nil", ErrDecodeTarget)
	}

	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return fmt.Errorf("%w: got %T", ErrDecodeTarget, v)
	}

	return nil
}

// decodeNode decodes node to v with the source's decode options followed by
// yamlOpts, and binds the error to the source: a YAML error as an [*Error]
// at the offending token, and any other, such as a canceled context, as it
// is. A node without
// content, the body of an empty document, leaves v as it is, which is what
// [yaml.Unmarshal] does with input that holds no value.
func (dd *Document) decodeNode(ctx context.Context, node ast.Node, v any, yamlOpts []yaml.DecodeOption) error {
	if !hasContent(node) {
		return nil
	}

	decodeOpts := make([]yaml.DecodeOption, 0, len(dd.source.decodeOpts)+len(yamlOpts))
	decodeOpts = append(decodeOpts, dd.source.decodeOpts...)
	decodeOpts = append(decodeOpts, yamlOpts...)

	dec := yaml.NewDecoder(bytes.NewReader(nil), decodeOpts...)

	// The decoder registers the anchors of the node it decodes, so an alias
	// in a node below the body finds an anchor defined elsewhere in the
	// document only after the decoder has seen the whole body. That pass
	// only primes the anchors, so a failure in it, which concerns a value
	// the caller did not ask for, is not the caller's error; an alias the
	// pass could not resolve fails again in the decode of node itself.
	if node != dd.doc.Body && hasAlias(node) {
		var sink any

		_ = dec.DecodeFromNodeContext(ctx, dd.doc.Body, &sink) //nolint:errcheck // The pass only primes anchors.
	}

	return dd.bindDecodeError(dec.DecodeFromNodeContext(ctx, node, v))
}

// bindDecodeError binds an error from the decoder to the source: a
// [yaml.Error] as an [*Error] at its token, so the excerpt marks it, and any
// other error, such as a canceled context, as it is. Returns nil for a nil
// err.
func (dd *Document) bindDecodeError(err error) error {
	if err == nil {
		return nil
	}

	if yamlErr, ok := errors.AsType[yaml.Error](err); ok {
		return dd.Bind(WrapError(yamlMessageError{yamlErr}, atToken(yamlErr.GetToken())))
	}

	return dd.Bind(err)
}

// yamlMessageError is a [yaml.Error] reduced to its message. The go-yaml text
// carries its own position and excerpt, which the [SourceError] binding
// the error renders itself, so the message alone goes in the chain, and
// the original error stays reachable through [errors.As].
type yamlMessageError struct {
	err yaml.Error
}

func (e yamlMessageError) Error() string {
	return e.err.GetMessage()
}

func (e yamlMessageError) Unwrap() error {
	return e.err
}

// hasAlias reports whether node or any node below it is an alias.
func hasAlias(node ast.Node) bool {
	if node == nil {
		return false
	}

	var found aliasFinder

	ast.Walk(&found, node)

	return bool(found)
}

// aliasFinder is an [ast.Visitor] that records whether it visited an alias
// node and stops the walk once it has.
type aliasFinder bool

// Visit implements [ast.Visitor].
func (f *aliasFinder) Visit(node ast.Node) ast.Visitor {
	if *f {
		return nil
	}

	if _, ok := node.(*ast.AliasNode); ok {
		*f = true

		return nil
	}

	return f
}

// hasContent reports whether node holds a YAML value. A nil node, a comment
// group, and a directive are the bodies of documents that hold none: an
// empty document, one holding only comments, and one holding only a %YAML
// directive. The parser gives such documents no value to decode, and
// [yaml.Unmarshal] leaves its target as it is for their text.
func hasContent(node ast.Node) bool {
	if node == nil {
		return false
	}

	switch node.Type() {
	case ast.CommentType, ast.DirectiveType:
		return false

	default:
		return true
	}
}

// Decode validates and decodes the document, or the node a Document from
// [Document.At] is scoped to, into a new T.
//
// Each [Validator] from [WithValidator] runs before decoding. If *T
// implements [SelfValidator], Decode calls Validate after decoding
// succeeds, unless [WithSelfValidation] switches that off. The method set
// of *T includes methods declared on T itself, so both value and pointer
// receivers participate. YAML decoding errors, and [Error] values from the
// validators, come back bound to the source as [SourceError] values. On
// error, the returned T is the zero value.
//
// A scoped Decode reads one typed value without decoding the whole
// document, such as a version number or a list of tags, and a scalar
// decodes into a string as its text, so a Decode[string] reads a
// discriminator field such as kind whatever its type:
//
//	kindPath := paths.Root().Child("kind")
//	for _, doc := range docs {
//		node, err := doc.At(kindPath)
//		if err != nil {
//			return err
//		}
//
//		kind, err := node.Decode[string](ctx)
//		if err != nil {
//			return err
//		}
//
//		switch kind {
//		case "Pod":
//			// Decode to Pod struct.
//		case "Service":
//			// Decode to Service struct.
//		}
//	}
//
// For the YAML text of any node, including a mapping or a sequence, take
// the node from [Document.Node] and call its String method.
//
// To decode into a value you already hold, use [Document.DecodeInto].
func (dd *Document) Decode[T any](ctx context.Context, opts ...DecodeOption) (T, error) {
	var v T

	err := dd.DecodeInto(ctx, &v, opts...)
	if err != nil {
		var zero T

		return zero, err
	}

	return v, nil
}
