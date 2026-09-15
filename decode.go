package niceyaml

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"iter"
	"sort"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/tokens"
)

// Validator is implemented by types that validate themselves.
//
// [Document.Decode] and [Document.DecodeInto] call Validate
// after decoding into a value that implements it, unless
// [WithoutValidator] switches that off.
type Validator interface {
	Validate() error
}

// SchemaValidator is implemented by types that validate arbitrary data against
// a schema.
//
// Pass one to [Document.Decode] with [WithSchema], and it decodes the
// document to [any] and calls ValidateSchema before decoding to the typed
// struct. [Document.ValidateSchema] runs one on its own. The
// context carries cancellation and deadlines to validators doing cancellable
// work, such as remote schema reference resolution.
//
// See [go.jacobcolvin.com/niceyaml/schema.NewValidator] for an
// implementation.
type SchemaValidator interface {
	ValidateSchema(ctx context.Context, data any) error
}

// Documents is the sequence of YAML documents in a [*Source].
//
// A single YAML file can hold several documents separated by "---", often
// with different schemas and validation requirements. Documents pairs each
// parsed document with its tokens once, and [Documents.All] yields a
// [*Document] for each:
//
//	docs, err := source.Documents()
//	for _, doc := range docs.All() {
//		// Each doc is a Document.
//	}
//
// Create instances with [Source.Documents].
type Documents struct {
	source *Source
	file   *ast.File
	// Tokens for each document, aligned with file.Docs by index at
	// construction. See alignDocumentTokens.
	docTokens []token.Tokens
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

// Source returns the underlying [*Source].
func (d *Documents) Source() *Source {
	return d.source
}

// Len returns the number of YAML documents in the file.
func (d *Documents) Len() int {
	return len(d.file.Docs)
}

// At returns the [*Document] at the given zero-based index, or nil when the
// index is outside the file.
func (d *Documents) At(index int) *Document {
	if index < 0 || index >= len(d.file.Docs) {
		return nil
	}

	return d.document(index)
}

// All returns an iterator over the documents in the file, in order.
//
// Each iteration yields the document index and a [*Document] for that
// document, built with the file path, tokens, and decode options of the
// [*Source]. The tokens were paired at construction, so each call yields
// the same slices.
func (d *Documents) All() iter.Seq2[int, *Document] {
	return func(yield func(int, *Document) bool) {
		for i := range d.file.Docs {
			if !yield(i, d.document(i)) {
				return
			}
		}
	}
}

// document builds the [*Document] at index with the context of the
// [*Source].
func (d *Documents) document(index int) *Document {
	return NewDocument(d.file.Docs[index], DocumentContext{
		Index:             index,
		FilePath:          d.source.FilePath(),
		Tokens:            d.docTokens[index],
		YAMLDecodeOptions: d.source.decodeOpts,
	})
}

// Document decodes and validates a single YAML document.
//
// [Document.Decode] returns a new value and
// [Document.DecodeInto] fills one the caller already holds, such as
// one pre-populated with defaults. Both run the same pipeline: each
// [SchemaValidator] given with [WithSchema] checks the document before
// decoding, and a value that implements [Validator] validates itself after,
// unless [WithoutValidator] is given.
//
//	for _, doc := range docs.All() {
//		config, err := doc.Decode[Config](ctx, niceyaml.WithSchema(validator))
//		if err != nil {
//			return err
//		}
//	}
//
// Use [Document.Get] or [Document.GetValue] to inspect values
// without decoding the whole document, which is helpful for routing
// documents based on a discriminator field.
//
// All decoding methods convert YAML errors to [Error] with source
// annotations.
//
// Create instances with [NewDocument] or iterate with [Documents.All].
type Document struct {
	doc        *ast.DocumentNode
	filePath   string
	tokens     token.Tokens
	decodeOpts []yaml.DecodeOption
	index      int
}

// DocumentContext is what a [Document] knows about its document
// beyond the AST: where it sits in the file, the tokens it came from, and
// how to decode it. [Documents.All] fills it in from the
// [Source]; callers that build a [Document] by hand pass what they
// have and leave the rest zero.
type DocumentContext struct {
	// FilePath is the path of the file the document came from. Schema
	// matchers route on it.
	FilePath string

	// Tokens are the tokens the document came from, with positions
	// relative to the document.
	Tokens token.Tokens

	// YAMLDecodeOptions reach the go-yaml decoder on every decode of the
	// document, ahead of the [DecodeOption] values given per call. A
	// [Source] sends the decoder half of [WithAllowDuplicateKeys] this way.
	YAMLDecodeOptions []yaml.DecodeOption

	// Index is the 0-indexed position of the document within the file.
	// Errors from the decoder carry it as their document index.
	Index int
}

// NewDocument creates a new [*Document] for doc with the given
// context. [Documents.All] is the usual way to get one, since it fills
// the context in from the [Source].
func NewDocument(doc *ast.DocumentNode, ctx DocumentContext) *Document {
	return &Document{
		doc:        doc,
		index:      ctx.Index,
		tokens:     ctx.Tokens,
		filePath:   ctx.FilePath,
		decodeOpts: ctx.YAMLDecodeOptions,
	}
}

// Node returns the underlying [*ast.DocumentNode].
func (dd *Document) Node() *ast.DocumentNode {
	return dd.doc
}

// Index returns the 0-indexed position of this document within the file,
// from [DocumentContext.Index].
func (dd *Document) Index() int {
	return dd.index
}

// Tokens returns the tokens for this document, from
// [DocumentContext.Tokens]. Returns nil when none were given.
func (dd *Document) Tokens() token.Tokens {
	return dd.tokens
}

// FilePath returns the path of the file the document came from, from
// [DocumentContext.FilePath]. Returns an empty string when none was given.
func (dd *Document) FilePath() string {
	return dd.filePath
}

// Get decodes the YAML value at path into a T without unmarshaling the whole
// document.
//
// This is useful when you need a typed field before deciding how to process
// the document, such as a version number or a list of tags:
//
//	versionPath := paths.Root().Child("version")
//	for _, doc := range docs.All() {
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
// that could match several nodes. YAML decoding errors, including a value
// that cannot be represented as T, are converted to [Error] with source
// annotations.
//
// The opts run the pipeline of [Document.DecodeInto] on the value at
// path rather than on the whole document: each [SchemaValidator] from
// [WithSchema] checks the value before decoding, a *T that implements
// [Validator] validates itself after, and [WithDisallowUnknownFields] and
// [WithYAMLDecodeOptions] configure the decoder.
//
// For a string view of any node, including mappings and sequences, use
// [Document.GetValue].
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

// GetValue extracts a YAML value as a string without unmarshaling.
//
// This is useful when you need to inspect document content before deciding how
// to process it. For example, multi-document files often use a discriminator
// field like "kind" or "version" to determine which schema applies:
//
//	kindPath := paths.Root().Child("kind")
//	for _, doc := range docs.All() {
//		kind, err := doc.GetValue(kindPath)
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
// For scalar values (strings, numbers, booleans), returns the semantic value
// rather than YAML syntax. For example, `kind: ""` returns an empty string,
// not the literal `""`. Null values return an empty string with a nil error.
//
// For non-scalar values (mappings, sequences), returns the YAML representation.
//
// Returns the same resolution errors as [Document.Get], so
// [paths.ErrNotFound] means nothing exists at the path and any other error
// means the path could not be resolved.
//
// For a typed value, use [Document.Get].
func (dd *Document) GetValue(path paths.Path) (string, error) {
	node, err := dd.node(path)
	if err != nil {
		return "", err
	}

	// Use GetValue() for scalar nodes to get the actual semantic value.
	if scalar, ok := node.(ast.ScalarNode); ok {
		v := scalar.GetValue()
		if v == nil {
			return "", nil // NullNode.
		}

		return fmt.Sprintf("%v", v), nil
	}

	// For non-scalar nodes (mappings, sequences), return YAML representation.
	return node.String(), nil
}

// node resolves path against the document body, ignoring the path's
// [paths.Part]. Errors come from [paths.Path.Node] as they are, since they
// already name the path.
func (dd *Document) node(path paths.Path) (ast.Node, error) {
	//nolint:wrapcheck // The paths error already names the path.
	return path.Node(dd.doc)
}

// ValidateSchema decodes the document to [any] and validates it using sv.
//
// Returns decoding errors or errors from the [SchemaValidator] ValidateSchema
// method.
func (dd *Document) ValidateSchema(ctx context.Context, sv SchemaValidator) error {
	return dd.validateSchema(ctx, dd.doc.Body, sv, nil)
}

// validateSchema decodes node to [any] with yamlOpts and validates it using
// sv, attaching the document index to a validation error.
func (dd *Document) validateSchema(
	ctx context.Context, node ast.Node, sv SchemaValidator, yamlOpts []yaml.DecodeOption,
) error {
	var untypedData any

	err := dd.decodeNode(ctx, node, &untypedData, yamlOpts)
	if err != nil {
		return err
	}

	err = sv.ValidateSchema(ctx, untypedData)
	if err != nil {
		return dd.locate(err)
	}

	return nil
}

// locate attaches this document's index to err when its chain holds an
// [*Error] without one, so its paths resolve in the right document of a
// multi-document source. A direct [*Error] is copied with the index; an
// Error behind other wrapping is wrapped in a new Error that carries it.
// Other errors pass through unchanged.
func (dd *Document) locate(err error) error {
	if err == nil {
		return nil
	}

	yamlErr, ok := errors.AsType[*Error](err)
	if !ok {
		return err
	}

	if _, set := yamlErr.DocumentIndex(); set {
		//nolint:wrapcheck // The producer already returns Error with path info.
		return err
	}

	if direct, ok := err.(*Error); ok { //nolint:errorlint // A direct Error is copied; a wrapped one is wrapped again.
		return direct.With(WithDocumentIndex(dd.index))
	}

	return NewErrorFrom(err, WithDocumentIndex(dd.index))
}

// DecodeOption configures [Document.Decode],
// [Document.DecodeInto], and [Document.Get].
//
// Available options:
//   - [WithSchema]
//   - [WithoutValidator]
//   - [WithDisallowUnknownFields]
//   - [WithYAMLDecodeOptions]
type DecodeOption func(*decodeConfig)

// decodeConfig holds the settings a [DecodeOption] configures.
type decodeConfig struct {
	schemas          []SchemaValidator
	yamlOpts         []yaml.DecodeOption
	withoutValidator bool
}

// WithSchema is a [DecodeOption] that validates the document against sv
// before decoding it. The document is decoded to [any] and handed to
// ValidateSchema, and a validation error ends the decode before any typed
// decoding. Several schemas run in the order given, stopping at the first
// that fails:
//
//	config, err := doc.Decode[Config](ctx, niceyaml.WithSchema(validator))
func WithSchema(sv SchemaValidator) DecodeOption {
	return func(c *decodeConfig) {
		c.schemas = append(c.schemas, sv)
	}
}

// WithoutValidator is a [DecodeOption] that skips the Validate method of a
// decoded value that implements [Validator]. Schemas given with [WithSchema]
// still run.
func WithoutValidator() DecodeOption {
	return func(c *decodeConfig) {
		c.withoutValidator = true
	}
}

// WithDisallowUnknownFields is a [DecodeOption] that rejects a mapping key
// that has no field in the target struct. Without it unknown keys are
// ignored.
func WithDisallowUnknownFields() DecodeOption {
	return WithYAMLDecodeOptions(yaml.DisallowUnknownField())
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

// Decode validates and decodes the document into a new T.
//
// Each [SchemaValidator] from [WithSchema] runs before decoding. If *T
// implements [Validator], Validate is called after successful decoding
// unless [WithoutValidator] is given. Methods declared on T itself are
// included in the method set of *T, so both value and pointer receivers
// participate. YAML decoding errors are converted to [Error] with source
// annotations. On error, the returned T is the zero value.
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

// DecodeInto validates and decodes the document into v, which must be a
// pointer.
//
// Each [SchemaValidator] from [WithSchema] runs before decoding. If v
// implements [Validator], Validate is called after successful decoding
// unless [WithoutValidator] is given. Fields absent from the document keep
// their existing values, so v may be pre-populated with defaults. YAML
// decoding errors are converted to [Error] with source annotations.
func (dd *Document) DecodeInto(ctx context.Context, v any, opts ...DecodeOption) error {
	return dd.decodeInto(ctx, dd.doc.Body, v, opts)
}

// decodeInto runs the decode pipeline on node: the schemas from opts check
// it, the decoder fills v with the options from the [Source] and from opts,
// and v validates itself unless opts switch that off.
func (dd *Document) decodeInto(ctx context.Context, node ast.Node, v any, opts []DecodeOption) error {
	var cfg decodeConfig

	for _, opt := range opts {
		opt(&cfg)
	}

	for _, sv := range cfg.schemas {
		err := dd.validateSchema(ctx, node, sv, cfg.yamlOpts)
		if err != nil {
			return err
		}
	}

	err := dd.decodeNode(ctx, node, v, cfg.yamlOpts)
	if err != nil {
		return err
	}

	if cfg.withoutValidator {
		return nil
	}

	if validator, ok := v.(Validator); ok {
		return dd.locate(validator.Validate())
	}

	return nil
}

// decodeNode decodes node to v with the document's decode options followed
// by yamlOpts, and converts YAML errors.
func (dd *Document) decodeNode(ctx context.Context, node ast.Node, v any, yamlOpts []yaml.DecodeOption) error {
	decodeOpts := make([]yaml.DecodeOption, 0, len(dd.decodeOpts)+len(yamlOpts))
	decodeOpts = append(decodeOpts, dd.decodeOpts...)
	decodeOpts = append(decodeOpts, yamlOpts...)

	dec := yaml.NewDecoder(bytes.NewReader(nil), decodeOpts...)
	err := dec.DecodeFromNodeContext(ctx, node, v)
	if err != nil {
		if yamlErr, ok := errors.AsType[yaml.Error](err); ok {
			return NewError(
				yamlErr.GetMessage(),
				WithErrorToken(yamlErr.GetToken()),
				WithDocumentIndex(dd.index),
			)
		}

		//nolint:wrapcheck // Return the original error if it's not a [yaml.Error].
		return err
	}

	return nil
}
