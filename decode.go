package niceyaml

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"

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
// [Node.Decode] and [Node.DecodeInto] call Validate after decoding on
// every value in the decoded value that implements it: the value
// itself, and each field, element, or map entry below it, however deep,
// unless [WithSelfValidation] switches that off. A check that belongs
// to the caller rather than the type, such as one that needs a registry
// of known names, runs on the decoded value after Decode returns, and
// [Node.Bind] binds its result to the document the value came from. A
// check that must run inside the decode, so a [Decoder] carries it to
// every node and its failures report beside the others, is a
// [Validator] that decodes the node itself with [Node.Decode] and checks
// the value it gets, as the Validator example shows.
//
// An [*Error] the value returns writes its path from the value's own
// root, and the decode puts it under the path of the value in the
// document: the name go-yaml decoded the field under, from its yaml
// tag, its json tag, or its lowercased name, the index of an element,
// or the key of a map entry, so a type checks its own invariants once
// and reports the right lines wherever a document holds it:
//
//	type Config struct {
//		Hours Hours  `yaml:"hours"`
//		Items []Item `yaml:"items"`
//	}
//
//	func (h Hours) Validate() error {
//		if h.Close.Before(h.Open) {
//			return niceyaml.NewError("closes before it opens", niceyaml.AtPath(paths.Root().Child("close")))
//		}
//
//		return nil
//	}
//
// A decode of Config reports $.hours.close from Hours and $.items[2].price
// from an Item, with Config declaring no Validate of its own. The values
// below a value validate first, and the value validates only when every
// one of them passed, so a parent that checks a relation between its
// fields sees fields that hold together, and a decode reports every
// value that failed. A field an inline tag flattens keeps the path of
// the struct that holds it. A value whose type decodes itself, through
// an UnmarshalYAML or UnmarshalText method, validates itself and
// nothing below it, since its fields need not mirror the document. The
// decode cannot see a type that go-yaml decodes whole through a
// [yaml.CustomUnmarshaler] option or an UnmarshalJSON method under
// [yaml.UseJSONUnmarshaler], so the values below such a type walk as if
// its fields mirrored the document; a decode of one runs its checks
// with [WithSelfValidation] off. A parent need not call the Validate
// of its fields, and [Rebase] is for a check run on a value after
// Decode returns.
//
// Any value with a Validate method takes part, including one from a
// package that names its own check that way, such as a generated
// message type, so a decode runs those checks too and reports their
// errors at the value that owns the method. A check that reads state
// the caller fills in after the decode runs on a value with that state
// set already through [Node.DecodeInto], which keeps the fields the
// document does not name, and [WithSelfValidation] false switches the
// walk off for every value. A Validate that rewrites its value, or that
// the value's own UnmarshalYAML already ran, runs again inside the
// decode, so it should be idempotent.
type SelfValidator interface {
	Validate() error
}

// Validator is implemented by types that validate a [*Node] before it
// decodes, such as a JSON schema or a schema registry that picks the
// schema from the document's content or file path.
//
// Pass one to [Node.Decode] with [WithValidator], give one to
// [NewDecoder] for a [Decoder] that checks every node it decodes, or run
// one on its own with [Node.Validate]. The Node is the scope that runs
// the validator: the root of a whole document, or the node a Node from
// [Node.At] selects, so a validator given to a scoped decode checks that
// node and its paths resolve from it. A validator that needs the whole
// document reaches it through [Node.Document]. One that can only check
// a whole document, as a [go.jacobcolvin.com/niceyaml/schema.Registry]
// can, refuses a scoped Node with an error instead of checking the
// document around it, so a caller validates once at the root and decodes
// the nodes below it without that validator.
//
// A validator that checks the decoded data
// reads the node with [Node.Decode], which runs the validators the
// caller passes and no other, so a validator never runs itself again. The
// context carries cancellation and deadlines to validators doing
// cancellable work, such as remote schema reference resolution:
//
//	func (s *Schema) Validate(ctx context.Context, n *niceyaml.Node) error {
//		data, err := n.Decode[any](ctx)
//		if err != nil {
//			return err
//		}
//
//		return s.check(ctx, data)
//	}
//
// A validator that knows a location returns an unbound [*Error], and the
// Node binds it to the source with itself as the scope its path resolves
// from. A validator that binds an error itself does so through
// [Node.Bind], since the Node leaves a bound error as it is.
//
// See [ValidatorFunc], [MultiValidator],
// [go.jacobcolvin.com/niceyaml/schema.Schema], and
// [go.jacobcolvin.com/niceyaml/schema.Registry] for implementations.
type Validator interface {
	Validate(ctx context.Context, n *Node) error
}

// ValidatorFunc adapts a function to the [Validator] interface.
//
//	kindPath := paths.Root().Child("kind")
//	known := niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
//		node, err := n.At(kindPath)
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
type ValidatorFunc func(ctx context.Context, n *Node) error

// Validate implements [Validator].
func (f ValidatorFunc) Validate(ctx context.Context, n *Node) error {
	return f(ctx, n)
}

// MultiValidator returns a [Validator] that runs every validator in
// order and reports every failure, where the validators [WithValidator]
// gives a decode run in order and stop at the first that fails. It suits
// validators that check a document independently, such as a schema and
// a check on the names it uses, so one run reports the violations of
// both:
//
//	config, err := doc.Decode[Config](ctx, niceyaml.WithValidator(niceyaml.MultiValidator(schema, names)))
//
// Each validator sees the node whatever the ones before it reported, so
// a validator that decodes the node reports its own failure beside a
// schema violation of the same value. A validator that needs an earlier
// one to have passed runs on its own instead. The run skips a nil
// validator. Two or more failures come back joined in the order given,
// which the Node binds as one, so [errors.Is] matches any one of them
// and the decode renders them as one tree. A lone failure comes back as
// the validator returned it, so it binds with its own position, as it
// would if that validator ran alone. No failure is no error. A context
// that ends stops the run, and the error is then the one the context
// reports, or the one the validator that saw it end returned.
func MultiValidator(validators ...Validator) Validator {
	return ValidatorFunc(func(ctx context.Context, n *Node) error {
		var errs []error

		for _, dv := range validators {
			if dv == nil {
				continue
			}

			err := ctx.Err()
			if err != nil {
				return err //nolint:wrapcheck // The context names the reason, and the Node binds it.
			}

			// A typed nil pointer is no failure, as [Node.Bind] reads it.
			err = dv.Validate(ctx, n)
			if isNothing(err) {
				continue
			}

			if ctx.Err() != nil {
				return err //nolint:wrapcheck // The validator's own error, which the Node binds.
			}

			errs = append(errs, err)
		}

		switch len(errs) {
		case 0:
			return nil
		case 1:
			return errs[0]
		default:
			return errors.Join(errs...)
		}
	})
}

// newDocuments creates the root [*Node] of each YAML document of file, the
// AST src parsed, in file order. See alignDocumentTokens for how each
// document node finds its tokens and foldPreambles for which nodes become
// documents.
func newDocuments(src *Source, file *ast.File) []*Node {
	docs := foldPreambles(file.Docs, alignDocumentTokens(file, src.Tokens()))
	liftHeaderComments(docs)

	groups := make([]token.Tokens, len(docs))
	for i, doc := range docs {
		groups[i] = doc.tokens
	}

	spans := documentSpans(groups, src.lines.Len())

	nodes := make([]*Node, len(docs))

	for i, doc := range docs {
		doc.index = i
		doc.preamble = preambleLen(doc.tokens)
		doc.node = &Node{source: src, doc: doc, content: doc.tokens, span: spans[i]}
		nodes[i] = doc.node
	}

	return nodes
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
func foldPreambles(nodes []*ast.DocumentNode, groups []token.Tokens) []*document {
	var (
		docs    []*document
		pending token.Tokens
	)

	for i, node := range nodes {
		if isPreambleNode(node) {
			pending = append(pending, groups[i]...)

			continue
		}

		docs = append(docs, &document{root: node, tokens: slices.Concat(pending, groups[i])})
		pending = nil
	}

	switch {
	case len(docs) > 0 && len(pending) > 0:
		last := docs[len(docs)-1]
		last.tokens = append(last.tokens, pending...)

	case len(docs) == 0 && len(nodes) > 0:
		docs = append(docs, &document{root: nodes[0], tokens: pending})
	}

	return docs
}

// liftHeaderComments moves the whole-line comments that close the tokens
// of each document to the front of the document below when that one has
// a "---" header. The parser leaves such comments with the document above
// as trailing comments of its content, while the same comments above the
// first header of a file, or after a "..." marker, are the preamble of the
// document below. Lifting them puts a schema directive written above any
// header in the preamble of the document it describes. A comment on the
// last line of content stays with the content, and a comment below the
// header of a document without content stays with that document.
func liftHeaderComments(docs []*document) {
	for i := 1; i < len(docs); i++ {
		if docs[i].root.Start == nil {
			continue
		}

		prev := docs[i-1]

		cut := trailingCommentsStart(prev.tokens)
		if cut == len(prev.tokens) {
			continue
		}

		docs[i].tokens = slices.Concat(prev.tokens[cut:], docs[i].tokens)
		prev.tokens = prev.tokens[:cut]
	}
}

// trailingCommentsStart returns the index of the first token in the run of
// comments that closes tks, where each comment sits on a line below the
// last token of any other type. Returns len(tks) when no comment closes
// tks, or when the token before the run is a "---" header, whose
// document the comments below it belong to.
func trailingCommentsStart(tks token.Tokens) int {
	last := -1

	for i, tk := range tks {
		if tk.Type != token.CommentType {
			last = i
		}
	}

	if last < 0 || tks[last].Type == token.DocumentHeaderType || tks[last].Position == nil {
		return len(tks)
	}

	end := tks[last].Position.Line + countLineBreaks(tokens.TrimLineEnding(tks[last].Origin))
	start := len(tks)

	for i := len(tks) - 1; i > last; i-- {
		if tks[i].Position == nil || tks[i].Position.Line <= end {
			break
		}

		start = i
	}

	return start
}

// countLineBreaks returns the number of line breaks in s, counting a CRLF
// as one.
func countLineBreaks(s string) int {
	return strings.Count(s, "\n") + strings.Count(s, "\r") - strings.Count(s, "\r\n")
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

// document is what describes one YAML document of a [Source] as a whole:
// its root, the tokens of the whole document, its index in the file, and
// the length of its preamble. Every [Node] of the document shares it.
type document struct {
	// The root Node of the document.
	node *Node
	root *ast.DocumentNode
	// The tokens of the whole document.
	tokens token.Tokens
	index  int
	// The number of tokens at the start of tokens before the content.
	preamble int
}

// Node is a scope in a YAML document: the root of the document, which
// [Source.Documents] and [Source.Document] return, or the node a path
// selects, which [Node.At] returns. Every method reads and resolves from
// the node, so [Node.Decode] decodes it alone, [Node.Validate] runs a
// [Validator] on it, and [Node.Bind] resolves the paths of an error from
// it, so a check written for the type of a value reports the same lines
// whether the value is the whole document or one inside it.
//
// [Node.Decode] returns a new value and [Node.DecodeInto] fills one the
// caller already holds, such as one pre-populated with defaults. Both run
// the same pipeline. Each [Validator] given with [WithValidator] checks
// the node before decoding, and a value that implements [SelfValidator]
// validates itself after, unless [WithSelfValidation] switches that off.
// [Node.Validate] runs the first step on its own.
//
//	for _, doc := range docs {
//		config, err := doc.Decode[Config](ctx, niceyaml.WithValidator(validator))
//		if err != nil {
//			return err
//		}
//	}
//
// A source that holds one document hands its root out from
// [Source.Document].
//
// A Node from [Node.At] is scoped to the node a path selects. Decode
// decodes that node alone, which reads one value without decoding the
// whole document, such as a discriminator field that routes the
// document, and Bind resolves the paths in an error from the node, so a
// check written for the type of that value reports the right lines. Any
// Node reaches the root of its document through [Node.Document], and
// [Node.Path] is [paths.Root] for the root and the path from it for a
// scoped Node.
//
// [Node.DocumentAST], [Node.DocumentIndex], [Node.Preamble], and
// [Node.FilePath] describe the document as a whole, whatever Node of it a
// caller holds.
//
// A Node holds the Source it came from, and every decoding method binds
// the [Error] values it produces to that source, so the errors it returns
// carry a [SourceError] that renders the offending lines.
//
// Receive instances from [Source.Documents], [Source.Document],
// [Node.At], or [Node.Document].
type Node struct {
	source *Source
	// The enclosing document.
	doc *document
	// The tokens of the node: the tokens of the whole document for its
	// root, and a sub-slice of them for a Node from At.
	content token.Tokens
	// The scope: the path from the document root to the node, which is
	// the root for a whole document.
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
// resolves against, so it is read-only.
func (n *Node) DocumentAST() *ast.DocumentNode {
	return n.doc.root
}

// Document returns the root [*Node] of the document the Node belongs to,
// so a Node from [Node.At] reaches the whole document, as a validator
// that picks a schema from the file path or the content of the document
// does. The root Node of a document returns itself. A nil Node belongs
// to none.
func (n *Node) Document() *Node {
	if n == nil {
		return nil
	}

	return n.doc.node
}

// AST returns the [ast.Node] the Node selects, resolved in the document
// as [Node.Decode] and the other methods resolve it: the body of the
// whole document for the root Node, and the node its path selects for
// one from [Node.At]. The text of any node, including a mapping or a
// sequence, is its String method:
//
//	scoped, err := doc.At(path)
//	if err != nil {
//		return err
//	}
//
//	node, err := scoped.AST()
//	if err != nil {
//		return err
//	}
//
//	fmt.Println(node.String())
//
// The body is what the parser built: nil for an empty document, and a
// comment group for one holding only comments. AST returns either
// without an error, as such a document decodes to nothing. The node is
// part of the tree [Source.File] returns, which every Node of the Source
// shares and resolves against, so it is read-only.
func (n *Node) AST() (ast.Node, error) {
	if n.base.IsRoot() {
		return n.doc.root.Body, nil
	}

	node, err := n.base.Node(n.doc.root)

	return node, n.Bind(err)
}

// At returns a [*Node] scoped to the node path selects, with path
// resolving from the receiver. The Node shares the source and the
// document with the receiver, and reaches the document through
// [Node.Document]. A validator given to a scoped decode checks the node,
// and a path in an error it or the decoded value reports resolves from
// the node, so a check written for a type reports the same lines whether
// the type is the whole document or a value inside one:
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
// several nodes, which [Node.Nodes] scopes one by one. A caller that
// falls back when a value is absent checks for [paths.ErrNotFound]:
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
func (n *Node) At(path paths.Path) (*Node, error) {
	c := *n
	c.base = n.base.Join(path)

	node, err := c.base.Node(n.doc.root)
	if err != nil {
		// Bind to the receiver, a Node that exists, rather than to the
		// copy, whose scope moved to a path that resolves to no node.
		return nil, n.Bind(err)
	}

	c.span, c.content = n.extent(node)

	return &c, nil
}

// Nodes returns a [*Node] scoped to each node path selects, in document
// order, with path resolving from the receiver, so a path with a `[*]`
// or `..name` selector, which [Node.At] rejects, scopes every element of
// a sequence or every entry with a name at any depth. Each Node is
// scoped as one from Node.At is, and [Node.Path] is the path that
// selects its node alone, as [paths.Path.Matches] resolves it, so a
// validator run on each element, or an error bound to it, reports the
// element it came from:
//
//	items, err := doc.Nodes(paths.Root().Child("items").IndexAll())
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
// A path that selects nothing returns no Nodes and no error, as
// [paths.Path.Nodes] does, and the errors it returns come back bound to
// the source: an error wrapping [paths.ErrNoDocument] when the document
// has no content, and [paths.ErrAlias] when an alias on the path does not
// resolve.
func (n *Node) Nodes(path paths.Path) ([]*Node, error) {
	found, err := n.base.Join(path).Matches(n.doc.root)
	if err != nil {
		return nil, n.Bind(err)
	}

	nodes := make([]*Node, 0, len(found))

	for _, m := range found {
		c := *n
		c.base = m.Path
		c.span, c.content = n.extent(m.Node)
		nodes = append(nodes, &c)
	}

	return nodes, nil
}

// extent returns the lines and the tokens of node in the document: the
// tokens of the document from the first token under the node through the
// last, in source order, and the lines from the one the first starts on
// through the one the last ends on. A node whose tokens carry no position
// covers no lines and holds no tokens.
func (n *Node) extent(node ast.Node) (position.Span, token.Tokens) {
	first, last := tokenBounds(node)
	if first == nil || last == nil {
		return position.Span{}, nil
	}

	var tks token.Tokens

	for _, tk := range n.doc.tokens {
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
	for _, r := range n.source.lines.ContentRanges(tks[len(tks)-1]) {
		end = max(end, r.LastLine())
	}

	total := n.source.lines.Len()

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

// Path returns the scope of the [Node]: the path from the document root
// to the node, which is [paths.Root] for the root Node of a document and
// the joined paths for one from [Node.At].
func (n *Node) Path() paths.Path {
	return n.base
}

// Source returns the [*Source] the node came from.
func (n *Node) Source() *Source {
	return n.source
}

// DocumentIndex returns the 0-indexed position within the file of the
// document the Node belongs to. A Node from [Node.At] reports the index
// of the document that holds it, and the index of the node itself within
// a sequence is the last segment of [Node.Path].
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
// is a copy, so reordering it reaches nothing, while the tokens
// themselves are shared and read-only.
func (n *Node) Tokens() token.Tokens {
	return slices.Clone(n.content)
}

// Preamble returns the tokens of the document the Node belongs to before
// its content: the comments and %YAML or %TAG directives above its "---"
// header, the header itself, and the comments between the header and the
// first token of the content. The parser cuts the tokens above the header off as a node of
// their own, and [Source.Documents] folds them back into the document the
// YAML spec attaches them to, so a schema directive written above the
// header is in the preamble of the document it describes. A document
// without content, such as one holding comments alone, is all preamble.
// The slice is a copy, and the tokens are shared and read-only, as for
// [Node.Tokens].
func (n *Node) Preamble() token.Tokens {
	return slices.Clone(n.doc.tokens[:n.doc.preamble])
}

// FilePath returns the path of the file the document came from, which is
// [Source.FilePath]. Returns an empty string when the source has none.
func (n *Node) FilePath() string {
	return n.source.FilePath()
}

// Span returns the lines of [Source.Lines] that the node covers. A whole
// document covers the lines from the one its first token starts on to the
// one before the next document starts, or to the end of the source for
// the last document. The first document also covers the lines above its
// first token, so the spans of a source cover every one of its lines. A
// document after the first with no tokens covers no lines.
//
// A Node from [Node.At] covers the lines from the one the first token
// under its node starts on through the one the last token ends on. The
// node of a mapping entry is its value, so the line of the key is in the
// span only when the value starts on it, and a scope from
// [paths.Path.Key] covers the key. A scope that selects nothing covers no
// lines.
//
// [Node.View] returns a view of the source sliced to the span, so a
// caller that renders the node need not slice one itself. The span
// slices any other view over the source, such as one that carries
// decoration already:
//
//	fmt.Println(p.Print(view.Slice(doc.Span())))
func (n *Node) Span() position.Span {
	return n.span
}

// View returns a new [*line.View] over the lines of [Source.Lines] that
// the node covers, [Node.Span], with the line numbers they have in the
// file. A document of a file that holds several, or the node a Node from
// [Node.At] selects, renders on its own:
//
//	fmt.Println(p.Print(doc.View()))
//
// Each call returns a view of its own with no decoration, as [Source.View]
// does, so overlays and annotations added to one reach neither the Source
// nor another view. The view shares its lines with every view over the
// source, so a bound error from the document marks it through
// [SourceError.Annotate] as it marks a view of the whole source.
func (n *Node) View() *line.View {
	return n.source.View().Slice(n.span)
}

// Lines returns the lines of [Source.Lines] that the node covers,
// [Node.Span], as new [line.Lines] that share the lines of the source,
// what [line.View.Held] returns for [Node.View]. It is the input for a
// diff of one document of a file that holds several, where [Source.Lines]
// would diff the whole file:
//
//	result := diff.Diff(before[1].Lines(), after[1].Lines())
//
// Each line keeps the number it has in the file. A Node that covers no
// lines returns empty Lines.
func (n *Node) Lines() line.Lines {
	return n.View().Held()
}

// Ranges returns the ranges the node at path covers, one per line, without
// the spaces around its content: the ranges [SourceError.Excerpt] highlights
// for an [Error] built with [AtPath] at that path. The path resolves
// from the scope of the Node, as it does in such an Error. A block
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
func (n *Node) Ranges(path paths.Path) (position.Ranges, error) {
	pos, err := n.position(path)
	if err != nil {
		return nil, n.Bind(err)
	}

	lines := n.source.lines

	return lines.ContentRanges(lines.TokenAt(pos)), nil
}

// position returns the position of the token that path resolves to in the
// document, through [paths.Path.Token], with path resolving from the
// scope. An error from it names the path already and comes back as it is,
// and a token without a position is [ErrNoLocation].
func (n *Node) position(path paths.Path) (position.Position, error) {
	tk, err := n.base.Join(path).Token(n.doc.root)
	if err != nil {
		//nolint:wrapcheck // The paths error already names the path.
		return position.Position{}, err
	}

	if tk == nil || tk.Position == nil {
		return position.Position{}, fmt.Errorf("%w: token at path has no position", ErrNoLocation)
	}

	return position.NewFromToken(tk), nil
}

// Validate runs each validator on the node in the order given and stops
// at the first that fails. It is the validation step of [Node.Decode] on
// its own, for a caller that checks a document without decoding it:
//
//	for _, doc := range docs {
//		if err := doc.Validate(ctx, reg); err != nil {
//			return err
//		}
//	}
//
// Given no validators, Validate runs none and returns nil, and it skips
// a nil validator. A validator that returns a nil [*Error] or
// [*SourceError] pointer passes, and the next one runs.
// [Decoder.Validate] runs the validators a [Decoder] holds the same
// way.
//
// An error from a validator comes back bound to the source as a
// [SourceError] through [Node.Bind], so an [*Error] renders its
// location and any other error names the source.
func (n *Node) Validate(ctx context.Context, validators ...Validator) error {
	return n.validate(ctx, validators)
}

// validate runs validators on the node in order and binds the first
// error.
func (n *Node) validate(ctx context.Context, validators []Validator) error {
	for _, dv := range validators {
		if dv == nil {
			continue
		}

		// A typed nil pointer is no failure, as [Node.Bind] reads it.
		err := dv.Validate(ctx, n)
		if isNothing(err) {
			continue
		}

		return n.Bind(err)
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
// The Node methods bind the errors they return already. Bind is for an
// error built elsewhere, such as a validator's [*Error] with a path, or
// one from a check the caller runs on a value it took from the document.
// Such an error writes its path from the value, so the Node scoped to
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
// cause chain, with no [*Error] above it that carries a location or nests
// errors, is bound already, to this source or another, and comes back as
// it is, so binding is idempotent. A located Error above a binding binds
// anew at its own location, with the position the inner binding resolved
// kept in its message. An Error above a binding that nests errors binds
// anew around it, with those errors as children. Bind never modifies err.
func (n *Node) Bind(err error) error {
	return bindTree(err, binder{src: n.source, node: n})
}

// DecodeOption configures [Node.Decode] and [Node.DecodeInto], and
// [NewDecoder] takes the same options for a [Decoder] that applies them
// to every node it decodes.
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

// newDecodeConfig returns the settings of a decode: the defaults, with
// opts applied over them in order. The result shares nothing with any
// other decode.
func newDecodeConfig(opts []DecodeOption) decodeConfig {
	cfg := decodeConfig{selfValidation: true}

	for _, opt := range opts {
		opt(&cfg)
	}

	return cfg
}

// clone returns a copy of the settings that shares no slice with the
// receiver, so an option applied to the copy reaches no other decode.
func (c decodeConfig) clone() decodeConfig {
	c.validators = slices.Clone(c.validators)
	c.yamlOpts = slices.Clone(c.yamlOpts)

	return c
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
// typed decoding. Several validators run in the order given, stopping
// at the first that fails, and [MultiValidator] runs several and
// reports every failure. Validation skips a nil dv. A
// [go.jacobcolvin.com/niceyaml/schema.Schema] checks the document
// against one JSON schema, and a
// [go.jacobcolvin.com/niceyaml/schema.Registry] against the schema it
// picks for the document:
//
//	config, err := doc.Decode[Config](ctx, niceyaml.WithValidator(reg))
func WithValidator(dv Validator) DecodeOption {
	return func(c *decodeConfig) {
		c.validators = append(c.validators, dv)
	}
}

// WithSelfValidation is a [DecodeOption] that sets whether the values
// in a decoded value that implement [SelfValidator] validate themselves
// after decoding. The default is true. Validators given with
// [WithValidator] run either way.
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
// values to the go-yaml decoder, after the ones the [Source] sends for
// every decode. Each WithYAMLDecodeOptions appends to the values given
// before it, so the go-yaml decoder receives them in the order given. It
// is the escape hatch for decoder settings that have no option of their
// own.
func WithYAMLDecodeOptions(opts ...yaml.DecodeOption) DecodeOption {
	return func(c *decodeConfig) {
		c.yamlOpts = append(c.yamlOpts, opts...)
	}
}

// DecodeInto validates and decodes the node, which is the whole document
// for the root Node of a document, into v, which must be a non-nil
// pointer. Any other v returns [ErrDecodeTarget] before anything runs.
//
// Each [Validator] from [WithValidator] runs on the node before
// decoding, in the order given, and no validator runs when opts name
// none. After decoding succeeds, every value in v that implements
// [SelfValidator] validates itself, with the paths it reports put under
// the path of the value, unless [WithSelfValidation] switches that off.
// Fields absent from the document keep their existing values, so v may
// be pre-populated with defaults.
// YAML decoding errors, and [Error] values from the validators, come back
// bound to the source as [SourceError] values, with a path in them
// resolving from the scope. A decoding error the go-yaml decoder
// reports, such as a value that does not read as the target type,
// matches [ErrDecodeRejected].
//
// An alias inside the node resolves against the anchors of the whole
// document, so a value that refers to an anchor defined outside it decodes
// as it does in the whole document.
//
// [Decoder.DecodeInto] decodes with options stated once, for every node
// a [Decoder] decodes.
func (n *Node) DecodeInto(ctx context.Context, v any, opts ...DecodeOption) error {
	return n.decodeInto(ctx, v, newDecodeConfig(opts))
}

// decodeInto is [Node.DecodeInto] with its settings resolved.
func (n *Node) decodeInto(ctx context.Context, v any, cfg decodeConfig) error {
	err := checkDecodeTarget(v)
	if err != nil {
		return err
	}

	node, err := n.AST()
	if err != nil {
		return err
	}

	err = n.validate(ctx, cfg.validators)
	if err != nil {
		return err
	}

	yamlOpts := n.yamlOptions(cfg.decodeOptions())

	err = n.decodeNode(ctx, node, v, yamlOpts)
	if err != nil {
		return err
	}

	if cfg.selfValidation {
		return n.Bind(selfValidate(v, n, yamlOpts))
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

// yamlOptions returns the go-yaml options for a decode: the source's
// decode options followed by yamlOpts.
func (n *Node) yamlOptions(yamlOpts []yaml.DecodeOption) []yaml.DecodeOption {
	opts := make([]yaml.DecodeOption, 0, len(n.source.decodeOpts)+len(yamlOpts))
	opts = append(opts, n.source.decodeOpts...)

	return append(opts, yamlOpts...)
}

// decodeNode decodes node to v with yamlOpts, and binds the error to the
// source: a YAML error as an [*Error] at the offending token, and any
// other, such as a canceled context, as it is. A node without content,
// the body of an empty document, leaves v as it is, which is what
// [yaml.Unmarshal] does with input that holds no value.
func (n *Node) decodeNode(ctx context.Context, node ast.Node, v any, yamlOpts []yaml.DecodeOption) error {
	if !hasContent(node) {
		return nil
	}

	dec := yaml.NewDecoder(bytes.NewReader(nil), yamlOpts...)

	// The decoder registers the anchors of the node it decodes, so an alias
	// in a node below the body finds an anchor defined elsewhere in the
	// document only after the decoder has seen the whole body. That pass
	// only primes the anchors, so a failure in it, which concerns a value
	// the caller did not ask for, is not the caller's error; an alias the
	// pass could not resolve fails again in the decode of node itself.
	if node != n.doc.root.Body && hasAlias(node) {
		var sink any

		_ = dec.DecodeFromNodeContext(ctx, n.doc.root.Body, &sink) //nolint:errcheck // The pass only primes anchors.
	}

	return n.bindDecodeError(dec.DecodeFromNodeContext(ctx, node, v))
}

// bindDecodeError binds an error from the decoder to the source: a
// [yaml.Error] at a token of the source as an [*Error] at that token, so
// the excerpt marks it and the error matches [ErrDecodeRejected], and any
// other error, such as a canceled context or one a value's own
// UnmarshalYAML returns, as it is. An UnmarshalYAML that parses the bytes
// it gets returns a [yaml.Error] of its own, whose token comes from that
// parse rather than the source, so it stays the value's own error.
// Returns nil for a nil err.
func (n *Node) bindDecodeError(err error) error {
	if err == nil {
		return nil
	}

	yamlErr, ok := errors.AsType[yaml.Error](err)
	if !ok || !n.holdsToken(yamlErr.GetToken()) {
		return n.Bind(err)
	}

	return n.Bind(WrapError(decodeRejectedError{yamlMessageError{yamlErr}}, atToken(yamlErr.GetToken())))
}

// holdsToken reports whether tk is a token of the source, by its type,
// value, origin, and position, as [line.Lines.TokenRanges] matches a
// token the parser cloned from the stream.
func (n *Node) holdsToken(tk *token.Token) bool {
	return tk != nil && len(n.source.lines.TokenRanges(tk)) > 0
}

// decodeRejectedError is a [yamlMessageError] the decoder returned, which
// matches [ErrDecodeRejected]. The same error from the parser does not,
// so [Source.Documents] wraps it in the plain [yamlMessageError].
type decodeRejectedError struct {
	yamlMessageError
}

// Is reports whether target is [ErrDecodeRejected].
func (e decodeRejectedError) Is(target error) bool {
	return target == ErrDecodeRejected
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
// directive. So is a scalar holding the placeholder token [tokens.Tokenize]
// makes for text the lexer emits nothing for, such as a file of
// whitespace alone. The parser gives such documents no value to decode,
// and [yaml.Unmarshal] leaves its target as it is for their text.
func hasContent(node ast.Node) bool {
	if node == nil {
		return false
	}

	if scalar, ok := node.(*ast.StringNode); ok && tokens.IsPlaceholder(scalar.Token) {
		return false
	}

	switch node.Type() {
	case ast.CommentType, ast.DirectiveType:
		return false

	default:
		return true
	}
}

// Decode validates and decodes the node, which is the whole document for
// the root Node of a document, into a new T.
//
// Each [Validator] from [WithValidator] runs on the node before
// decoding, as [Node.DecodeInto] describes, and no validator runs when
// opts name none. After decoding succeeds, every value in the result
// that implements [SelfValidator] validates itself, T first among them,
// unless [WithSelfValidation] switches that off. The method set of a
// pointer includes the methods declared on the value, so both value and
// pointer receivers participate. YAML decoding errors, and [Error]
// values from the validators, come back bound to the source as
// [SourceError] values, and a value the go-yaml decoder rejects matches
// [ErrDecodeRejected]. On error, the returned T is the zero value.
//
// A scoped Decode reads one typed value without decoding the whole
// document, such as a version number or a list of tags, and a scalar
// decodes into a string whatever its type, so a Decode[string] reads a
// discriminator field such as kind. A number decodes into a string in its
// canonical spelling, so 1.10 reads as "1.1" and 0x10 as "16":
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
// the node from [Node.AST] and call its String method.
//
// To decode into a value you already hold, use [Node.DecodeInto]. To
// decode many nodes with options stated once, use a [Decoder].
func (n *Node) Decode[T any](ctx context.Context, opts ...DecodeOption) (T, error) {
	var v T

	err := n.DecodeInto(ctx, &v, opts...)
	if err != nil {
		var zero T

		return zero, err
	}

	return v, nil
}
