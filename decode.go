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

	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/tokens"
)

// SelfValidator is implemented by types that validate themselves.
//
// [Document.Decode] and [Document.DecodeInto] call Validate
// after decoding into a value that implements it, unless
// [WithSelfValidation] switches that off.
type SelfValidator interface {
	Validate() error
}

// Validator is implemented by types that validate a whole [*Document]
// before it decodes, such as a JSON schema or a schema registry that picks
// the schema from the document's content or file path.
//
// Pass one to [Document.Decode] with [WithValidator], or run one on its own
// with [Document.Validate]. The document is the whole document even for
// [Document.Get], since a validator that routes on the document has no
// meaning for one value inside it. A validator that checks the decoded data
// decodes the document itself, and the context carries cancellation and
// deadlines to validators doing cancellable work, such as remote schema
// reference resolution:
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
//		kind, err := doc.Get[string](ctx, kindPath)
//		if err != nil {
//			return err
//		}
//
//		if kind != "Deployment" {
//			return niceyaml.NewError("unknown kind", niceyaml.WithPath(kindPath))
//		}
//
//		return nil
//	})
type ValidatorFunc func(ctx context.Context, doc *Document) error

// Validate implements [Validator].
func (f ValidatorFunc) Validate(ctx context.Context, doc *Document) error {
	return f(ctx, doc)
}

// newDocuments creates one [*Document] per document of file, the AST src
// parsed, in file order. See alignDocumentTokens for how each Document finds
// its tokens.
func newDocuments(src *Source, file *ast.File) []*Document {
	docTokens := alignDocumentTokens(file, src.Tokens())
	spans := documentSpans(docTokens, src.lines.Len())

	docs := make([]*Document, len(file.Docs))
	for i, doc := range file.Docs {
		docs[i] = &Document{source: src, doc: doc, tokens: docTokens[i], span: spans[i], index: i}
	}

	return docs
}

// documentSpans returns the lines of a view of total lines that each token
// group covers. The groups partition the file in order, so a group runs
// from the line its first token starts on to the line the next group starts
// on, and the last group runs to the end of the view. A group with no
// tokens covers no lines and sits where the next group starts.
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

	return spans
}

// alignDocumentTokens pairs every document in file with the token group it
// starts in, returning one entry per document in file order.
//
// The groups come from [tokens.SplitDocuments]. Each document is anchored by
// the offset of its header token, or of its body's first token when it has
// no header, and takes the last group that starts at or before that offset.
// Matching by offset rather than by index keeps a document paired with its
// own tokens when the parser and the splitter disagree on boundaries, which
// happens for streams such as consecutive empty headers. A document with no
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

	result := make([]token.Tokens, len(file.Docs))

	for i, doc := range file.Docs {
		offset, ok := documentOffset(doc)
		if !ok {
			continue
		}

		// Index of the last group that starts at or before offset.
		idx := sort.Search(len(starts), func(j int) bool { return starts[j] > offset }) - 1
		if idx >= 0 {
			result[i] = groups[idx]
		}
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
// one pre-populated with defaults. Both run the same pipeline: each
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
// A source that holds one document decodes through [Source.Decode] and
// [Source.DecodeInto], which run the same pipeline on that document.
//
// Use [Document.Get] to read one value without decoding the whole
// document, which is helpful for routing documents based on a
// discriminator field.
//
// A Document holds the [*Source] it came from, and every decoding method
// binds the [Error] values it produces to that source, so the errors it
// returns carry a [SourceError] that renders the offending lines.
//
// Receive instances from [Source.Documents].
type Document struct {
	source *Source
	doc    *ast.DocumentNode
	tokens token.Tokens
	span   position.Span
	index  int
}

// Node returns the underlying [*ast.DocumentNode].
func (dd *Document) Node() *ast.DocumentNode {
	return dd.doc
}

// Source returns the [*Source] the document came from.
func (dd *Document) Source() *Source {
	return dd.source
}

// Index returns the 0-indexed position of this document within the file.
func (dd *Document) Index() int {
	return dd.index
}

// Tokens returns the tokens of this document, with the positions they have
// in the source. Returns nil when no token anchors the document, such as one
// with neither a header nor a body. The slice is a copy, so reordering it
// reaches nothing, while the tokens themselves are shared and read-only.
func (dd *Document) Tokens() token.Tokens {
	return slices.Clone(dd.tokens)
}

// FilePath returns the path of the file the document came from, which is
// [Source.FilePath]. Returns an empty string when the source has none.
func (dd *Document) FilePath() string {
	return dd.source.FilePath()
}

// Span returns the lines of [Source.Lines] that the document covers: from
// the line its first token starts on to the line before the next document
// starts, or to the end of the source for the last document. A document
// with no tokens covers no lines. Slice a view of the source with the span
// to render one document of a file with the file's line numbers:
//
//	fmt.Println(p.Print(source.View().Slice(doc.Span())))
func (dd *Document) Span() position.Span {
	return dd.span
}

// HasContent reports whether the document holds a YAML value. A document
// that holds only comments, or only %YAML and %TAG directives, has none.
// The parser splits such a preamble off from the content below the next
// "---" as a document of its own, so a file that opens with a license
// header or a %YAML directive parses into one document without content
// and one with it. An explicitly empty document, whose body is nil, counts
// as content, since it is the null document a schema may validate and a
// decode fills with nothing.
//
// [Source.Document] selects the document with content, and a
// [go.jacobcolvin.com/niceyaml/schema.Registry] validates only documents
// with content.
func (dd *Document) HasContent() bool {
	return dd.doc.Body == nil || hasContent(dd.doc.Body)
}

// node resolves path against the document body, ignoring the path's
// [paths.Part]. An error from [paths.Path.Node] names the path already, so
// it is bound to the source as it is.
func (dd *Document) node(path paths.Path) (ast.Node, error) {
	node, err := path.Node(dd.doc)

	return node, dd.Bind(err)
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
// resolving in this document. It resolves every location in err as it
// binds, so the position [SourceError.Error] reports and the range
// [SourceError.Location] returns are fixed from then on, and
// [SourceError.Detail] and [SourceError.Excerpt] render the excerpt with
// the [Renderer] of the caller's choice.
//
// The Document methods bind the errors they return already. Bind is for
// an error built elsewhere, such as a validator's [*Error] with a path, or
// one from a check the caller runs on a value it took from the document:
//
//	value, err := doc.Get[map[string]any](ctx, path)
//	if err != nil {
//		return err
//	}
//
//	return doc.Bind(check(value))
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
// If err is nil, Bind returns nil. If the first [*SourceError] in err's
// chain is bound to this source already, Bind returns err unchanged, so
// binding is idempotent. A nil [*Error] or [*SourceError] pointer as err
// carries nothing to bind and comes back as it is, and one inside the
// chain binds nothing, so Bind looks past it. Bind never modifies err.
func (dd *Document) Bind(err error) error {
	if isNothing(err) {
		return err
	}

	bound, isBound := firstSourceError(err)
	if isBound && bound.source == dd.source {
		return err
	}

	return newSourceError(err, dd.source, dd)
}

// DecodeOption configures [Document.Decode],
// [Document.DecodeInto], and [Document.Get].
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

// DecodeInto validates and decodes the document into v, which must be a
// non-nil pointer. Any other v returns [ErrDecodeTarget] before anything
// runs.
//
// Each [Validator] from [WithValidator] runs before decoding. If
// v implements [SelfValidator], Validate is called after successful decoding
// unless [WithSelfValidation] switches that off. Fields absent from the
// document keep their existing values, so v may be pre-populated with
// defaults. YAML decoding errors, and [Error] values from the validators,
// come back bound to the source as [SourceError] values.
func (dd *Document) DecodeInto(ctx context.Context, v any, opts ...DecodeOption) error {
	return dd.decodeInto(ctx, dd.doc.Body, v, opts)
}

// decodeInto runs the decode pipeline on node: the validators from opts
// check the document, the decoder fills v with the options from the
// [Source] and from opts, and v validates itself unless opts switch that
// off. A v that is not a non-nil pointer returns [ErrDecodeTarget] before
// the validators run.
func (dd *Document) decodeInto(ctx context.Context, node ast.Node, v any, opts []DecodeOption) error {
	err := checkDecodeTarget(v)
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
	// document only after the decoder has seen the whole body.
	if node != dd.doc.Body && hasAlias(node) {
		var sink any

		err := dec.DecodeFromNodeContext(ctx, dd.doc.Body, &sink)
		if err != nil {
			return dd.bindDecodeError(err)
		}
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
		return dd.Bind(NewError(yamlErr.GetMessage(), atToken(yamlErr.GetToken())))
	}

	return dd.Bind(err)
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

// Get decodes the YAML value at path into a T without unmarshaling the whole
// document.
//
// This is useful when you need a typed field before deciding how to process
// the document, such as a version number or a list of tags:
//
//	versionPath := paths.Root().Child("version")
//	for _, doc := range docs {
//		version, err := doc.Get[int](ctx, versionPath)
//		if errors.Is(err, paths.ErrNotFound) {
//			version = 1
//		} else if err != nil {
//			return err
//		}
//	}
//
// Path resolution errors come from [paths.Path.Node]: an error wrapping
// [paths.ErrNotFound] when nothing exists at the path, which also wraps
// [paths.ErrNoDocument] when the document has no content at all, such as an
// empty document or one holding only directives; [paths.ErrAlias] when an
// alias on the path does not resolve; and [paths.ErrWildcard] for a path
// that could match several nodes. Every error comes back bound to the
// source as a [SourceError], a path resolution error with the name of the
// source in front and a YAML decoding error, including a value that cannot
// be represented as T, with the position of the offending token. An alias
// inside the
// value resolves against the anchors of the whole document, so a value
// that refers to an anchor defined outside it decodes as it does in the
// whole document.
//
// The opts run the pipeline of [Document.DecodeInto] on the value at
// path rather than on the whole document: a *T that implements [SelfValidator]
// validates itself after decoding, and [WithDisallowUnknownFields] and
// [WithYAMLDecodeOptions] configure the decoder. A [Validator] from
// [WithValidator] still receives the whole document.
//
// A scalar decodes into a string as its text, so Get[string] reads a
// discriminator field such as kind whatever its type:
//
//	kindPath := paths.Root().Child("kind")
//	for _, doc := range docs {
//		kind, err := doc.Get[string](ctx, kindPath)
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
// For the YAML text of any node, including a mapping or a sequence, resolve
// it with [paths.Path.Node] against [Document.Node] and call its String
// method.
func (dd *Document) Get[T any](ctx context.Context, path paths.Path, opts ...DecodeOption) (T, error) {
	var zero T

	node, err := dd.node(path)
	if err != nil {
		return zero, err
	}

	var v T

	err = dd.decodeInto(ctx, node, &v, opts)
	if err != nil {
		return zero, err
	}

	return v, nil
}

// Decode validates and decodes the document into a new T.
//
// Each [Validator] from [WithValidator] runs before decoding. If
// *T implements [SelfValidator], Validate is called after successful decoding
// unless [WithSelfValidation] switches that off. Methods declared on T
// itself are included in the method set of *T, so both value and pointer
// receivers participate. YAML decoding errors, and [Error] values from the
// validators, come back bound to the source as [SourceError] values. On
// error, the returned T is the zero value.
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
