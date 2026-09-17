package niceyaml

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"iter"
	"slices"
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
// [WithSelfValidation] switches that off.
type Validator interface {
	Validate() error
}

// SchemaValidator is implemented by types that validate arbitrary data against
// a schema.
//
// Pass one to [Document.Decode] with [WithSchemaValidator], and it decodes the
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

// DocumentValidator is implemented by types that validate a whole
// [*Document], such as a schema registry that picks the schema from the
// document's content or file path.
//
// Pass one to [Document.Decode] with [WithDocumentValidator], and it runs
// before decoding, after the schemas from [WithSchemaValidator]. The
// document is the whole document even for [Document.Get], since a validator
// that routes on the document has no meaning for one value inside it.
//
// A validator that knows a location returns an unbound [*Error], and the
// Document binds it to the source with the document's index, so its path
// resolves in the right document. A validator that binds an error itself
// through [Source.WrapError] sets [WithDocumentIndex] first, since the
// Document leaves a bound error as it is.
//
// See [go.jacobcolvin.com/niceyaml/schema/registry.Registry] for an
// implementation.
type DocumentValidator interface {
	Validate(ctx context.Context, doc *Document) error
}

// Documents is the sequence of YAML documents in a [*Source].
//
// A single YAML file can hold several documents separated by "---", often
// with different schemas and validation requirements. Documents builds a
// [*Document] for each parsed document once, so [Documents.At] and
// [Documents.All] return the same pointer for an index however often they
// run. Two calls to [Source.Documents] build two sets.
//
//	docs, err := source.Documents()
//	for _, doc := range docs.All() {
//		// Each doc is a Document.
//	}
//
// Create instances with [Source.Documents].
type Documents struct {
	source *Source
	docs   []*Document
}

// newDocuments creates a new [*Documents] holding one [*Document] per
// document of file, the AST src parsed, in file order. See
// alignDocumentTokens for how each Document finds its tokens.
func newDocuments(src *Source, file *ast.File) *Documents {
	docTokens := alignDocumentTokens(file, src.Tokens())

	docs := make([]*Document, len(file.Docs))
	for i, doc := range file.Docs {
		docs[i] = &Document{source: src, doc: doc, tokens: docTokens[i], index: i}
	}

	return &Documents{source: src, docs: docs}
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
	return len(d.docs)
}

// At returns the [*Document] at the given zero-based index, or nil when the
// index is outside the file. The same index returns the same pointer.
func (d *Documents) At(index int) *Document {
	if index < 0 || index >= len(d.docs) {
		return nil
	}

	return d.docs[index]
}

// All returns an iterator over the documents in the file, in order.
//
// Each iteration yields the document index and the [*Document] for that
// document, the one [Documents.At] returns for the index.
func (d *Documents) All() iter.Seq2[int, *Document] {
	return func(yield func(int, *Document) bool) {
		for i, doc := range d.docs {
			if !yield(i, doc) {
				return
			}
		}
	}
}

// Document decodes and validates a single YAML document.
//
// [Document.Decode] returns a new value and
// [Document.DecodeInto] fills one the caller already holds, such as
// one pre-populated with defaults. Both run the same pipeline: each
// [SchemaValidator] given with [WithSchemaValidator] and each
// [DocumentValidator] given with [WithDocumentValidator] checks the document
// before decoding, and a value that implements [Validator] validates itself
// after, unless [WithSelfValidation] switches that off.
//
//	for _, doc := range docs.All() {
//		config, err := doc.Decode[Config](ctx, niceyaml.WithSchemaValidator(validator))
//		if err != nil {
//			return err
//		}
//	}
//
// Use [Document.Get] or [Document.GetValue] to inspect values
// without decoding the whole document, which is helpful for routing
// documents based on a discriminator field.
//
// A Document holds the [*Source] it came from, and every decoding method
// binds the [Error] values it produces to that source, so the errors it
// returns carry a [SourceError] that renders the offending lines.
//
// Create instances with [Documents.At] or iterate with [Documents.All].
type Document struct {
	source *Source
	doc    *ast.DocumentNode
	tokens token.Tokens
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
// with neither a header nor a body.
func (dd *Document) Tokens() token.Tokens {
	return dd.tokens
}

// FilePath returns the path of the file the document came from, which is
// [Source.FilePath]. Returns an empty string when the source has none.
func (dd *Document) FilePath() string {
	return dd.source.FilePath()
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
// that could match several nodes. Those errors come back as they are. A
// YAML decoding error, including a value that cannot be represented as T,
// comes back bound to the source as a [SourceError].
//
// The opts run the pipeline of [Document.DecodeInto] on the value at
// path rather than on the whole document: each [SchemaValidator] from
// [WithSchemaValidator] checks the value before decoding, a *T that implements
// [Validator] validates itself after, and [WithDisallowUnknownFields] and
// [WithYAMLDecodeOptions] configure the decoder. A [DocumentValidator] from
// [WithDocumentValidator] still receives the whole document.
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
// A decoding error, or an [*Error] from sv, comes back bound to the source
// as a [SourceError]. Any other error from sv comes back as it is.
func (dd *Document) ValidateSchema(ctx context.Context, sv SchemaValidator) error {
	var untypedData any

	err := dd.decodeNode(ctx, dd.doc.Body, &untypedData, nil)
	if err != nil {
		return err
	}

	err = sv.ValidateSchema(ctx, untypedData)
	if err != nil {
		return dd.bind(err)
	}

	return nil
}

// bind returns err bound to the document's source. An error already bound
// to that source comes back as it is. When the chain holds an [*Error]
// without a document index, bind sets this document's index so the paths
// resolve in the right document of a multi-document source. It copies a
// direct Error with the index and wraps one behind other wrapping in a new
// Error that carries it. An error whose chain holds no Error, such as one
// from [paths], passes through unchanged.
func (dd *Document) bind(err error) error {
	if err == nil {
		return nil
	}

	yamlErr, ok := firstError(err)
	if !ok {
		return err
	}

	bound, isBound := errors.AsType[*SourceError](err)
	if isBound && bound.source == dd.source {
		return err
	}

	if _, set := yamlErr.DocumentIndex(); !set {
		if direct, ok := err.(*Error); ok { //nolint:errorlint // A direct Error is copied; a wrapped one is wrapped again.
			err = direct.With(WithDocumentIndex(dd.index))
		} else {
			err = NewErrorFrom(err, WithDocumentIndex(dd.index))
		}
	}

	return dd.source.WrapError(err)
}

// DecodeOption configures [Document.Decode],
// [Document.DecodeInto], and [Document.Get].
//
// Available options:
//   - [WithSchemaValidator]
//   - [WithDocumentValidator]
//   - [WithSelfValidation]
//   - [WithDisallowUnknownFields]
//   - [WithYAMLDecodeOptions]
type DecodeOption func(*decodeConfig)

// decodeConfig holds the settings a [DecodeOption] configures.
type decodeConfig struct {
	schemas               []SchemaValidator
	docValidators         []DocumentValidator
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

// WithSchemaValidator is a [DecodeOption] that validates the document
// against sv before decoding it. The document is decoded to [any] once and
// handed to ValidateSchema, and a validation error ends the decode before
// any typed decoding. Several schemas receive the same value in the order
// given, stopping at the first that fails:
//
//	config, err := doc.Decode[Config](ctx, niceyaml.WithSchemaValidator(validator))
func WithSchemaValidator(sv SchemaValidator) DecodeOption {
	return func(c *decodeConfig) {
		c.schemas = append(c.schemas, sv)
	}
}

// WithDocumentValidator is a [DecodeOption] that validates the document
// with dv before decoding it. It runs after every schema from
// [WithSchemaValidator], and several document validators run in the order
// given, stopping at the first that fails. A
// [go.jacobcolvin.com/niceyaml/schema/registry.Registry] is one, so a
// document decodes against the schema the registry picks for it:
//
//	config, err := doc.Decode[Config](ctx, niceyaml.WithDocumentValidator(reg))
func WithDocumentValidator(dv DocumentValidator) DecodeOption {
	return func(c *decodeConfig) {
		c.docValidators = append(c.docValidators, dv)
	}
}

// WithSelfValidation is a [DecodeOption] that sets whether a decoded value
// that implements [Validator] validates itself after decoding. The default
// is true. Schemas given with [WithSchemaValidator] and document validators
// given with [WithDocumentValidator] run either way.
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

// Decode validates and decodes the document into a new T.
//
// Each [SchemaValidator] from [WithSchemaValidator] and each
// [DocumentValidator] from [WithDocumentValidator] runs before decoding. If
// *T implements [Validator], Validate is called after successful decoding
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

// DecodeInto validates and decodes the document into v, which must be a
// pointer.
//
// Each [SchemaValidator] from [WithSchemaValidator] and each
// [DocumentValidator] from [WithDocumentValidator] runs before decoding. If
// v implements [Validator], Validate is called after successful decoding
// unless [WithSelfValidation] switches that off. Fields absent from the
// document keep their existing values, so v may be pre-populated with
// defaults. YAML decoding errors, and [Error] values from the validators,
// come back bound to the source as [SourceError] values.
func (dd *Document) DecodeInto(ctx context.Context, v any, opts ...DecodeOption) error {
	return dd.decodeInto(ctx, dd.doc.Body, v, opts)
}

// decodeInto runs the decode pipeline on node: the schemas from opts check
// one untyped decode of it, the document validators from opts check the
// document, the decoder fills v with the options from the [Source] and from
// opts, and v validates itself unless opts switch that off.
func (dd *Document) decodeInto(ctx context.Context, node ast.Node, v any, opts []DecodeOption) error {
	cfg := newDecodeConfig(opts)
	yamlOpts := cfg.decodeOptions()

	if len(cfg.schemas) > 0 {
		var untypedData any

		err := dd.decodeNode(ctx, node, &untypedData, yamlOpts)
		if err != nil {
			return err
		}

		for _, sv := range cfg.schemas {
			err := sv.ValidateSchema(ctx, untypedData)
			if err != nil {
				return dd.bind(err)
			}
		}
	}

	for _, dv := range cfg.docValidators {
		err := dv.Validate(ctx, dd)
		if err != nil {
			return dd.bind(err)
		}
	}

	err := dd.decodeNode(ctx, node, v, yamlOpts)
	if err != nil {
		return err
	}

	if !cfg.selfValidation {
		return nil
	}

	if validator, ok := v.(Validator); ok {
		return dd.bind(validator.Validate())
	}

	return nil
}

// decodeNode decodes node to v with the source's decode options followed by
// yamlOpts, and binds a YAML error to the source. Any other error from the
// decoder, such as a canceled context, comes back as it is.
func (dd *Document) decodeNode(ctx context.Context, node ast.Node, v any, yamlOpts []yaml.DecodeOption) error {
	decodeOpts := make([]yaml.DecodeOption, 0, len(dd.source.decodeOpts)+len(yamlOpts))
	decodeOpts = append(decodeOpts, dd.source.decodeOpts...)
	decodeOpts = append(decodeOpts, yamlOpts...)

	dec := yaml.NewDecoder(bytes.NewReader(nil), decodeOpts...)
	err := dec.DecodeFromNodeContext(ctx, node, v)
	if err != nil {
		if yamlErr, ok := errors.AsType[yaml.Error](err); ok {
			return dd.bind(NewError(yamlErr.GetMessage(), WithToken(yamlErr.GetToken())))
		}

		//nolint:wrapcheck // Return the original error if it's not a [yaml.Error].
		return err
	}

	return nil
}
