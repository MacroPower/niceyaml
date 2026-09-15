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

// ErrValueNotFound indicates that a YAML path did not resolve to a node in
// the document. [DocumentDecoder.Get] returns it wrapped with the path.
var ErrValueNotFound = errors.New("value not found")

// Validator is implemented by types that validate themselves.
//
// If a type implements this interface, [DocumentDecoder.Unmarshal]
// automatically calls Validate after successful decoding.
type Validator interface {
	Validate() error
}

// SchemaValidator is implemented by types that validate arbitrary data against
// a schema.
//
// Pass one to [DocumentDecoder.Unmarshal] with [WithSchema], and it decodes
// the document to [any] and calls ValidateSchema before decoding to the
// typed struct. [DocumentDecoder.ValidateSchema] runs one on its own. The
// context carries cancellation and deadlines to validators doing cancellable
// work, such as remote schema reference resolution.
//
// See [go.jacobcolvin.com/niceyaml/schema.NewValidator] for an
// implementation.
type SchemaValidator interface {
	ValidateSchema(ctx context.Context, data any) error
}

// Decoder iterates over YAML documents in a [*Source].
//
// A single YAML file can contain multiple documents separated by "---".
// These documents often have different schemas and/or validation requirements.
// Decoder provides lazy iteration over these documents, providing a
// [DocumentDecoder] for each.
//
//	dec, err := source.Decoder()
//	for _, dd := range dec.Documents() {
//		// Each dd is a DocumentDecoder instance.
//	}
//
// Create instances with [Source.Decoder].
type Decoder struct {
	source *Source
	file   *ast.File
	// Tokens for each document, aligned with file.Docs by index at
	// construction. See alignDocumentTokens.
	docTokens []token.Tokens
}

// NewDecoder creates a new [*Decoder] for the given [*Source].
//
// NewDecoder parses the source and pairs each parsed document with its
// tokens once, so [Decoder.Documents] can be iterated any number of times
// without repeating either step.
//
// Returns an error if the source cannot be parsed.
func NewDecoder(s *Source) (*Decoder, error) {
	f, err := s.File()
	if err != nil {
		return nil, err
	}

	return &Decoder{source: s, file: f, docTokens: alignDocumentTokens(f, s.Tokens())}, nil
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
func (d *Decoder) Source() *Source {
	return d.source
}

// Len returns the number of YAML documents in the file.
func (d *Decoder) Len() int {
	return len(d.file.Docs)
}

// Documents returns an iterator over all documents in the YAML file.
//
// Each iteration yields the document index and a [*DocumentDecoder] for that
// document. The [*DocumentDecoder] receives context from the [*Source]
// including file path, tokens, and document index. The tokens were paired at
// construction, so each call yields the same slices.
func (d *Decoder) Documents() iter.Seq2[int, *DocumentDecoder] {
	filePath := d.source.FilePath()

	return func(yield func(int, *DocumentDecoder) bool) {
		for i, doc := range d.file.Docs {
			dd := NewDocumentDecoder(doc, DocumentContext{
				Index:             i,
				FilePath:          filePath,
				Tokens:            d.docTokens[i],
				YAMLDecodeOptions: d.source.decodeOpts,
			})

			if !yield(i, dd) {
				return
			}
		}
	}
}

// DocumentDecoder decodes and validates a single YAML document.
//
// It separates decoding from document iteration, allowing validation hooks
// to run at the right time during unmarshaling. A [SchemaValidator] given
// with [WithSchema] runs before decoding, and a type implementing
// [Validator] validates itself after.
//
// Use [DocumentDecoder.Get] or [DocumentDecoder.GetValue] to inspect values
// without unmarshaling, which is helpful for routing documents based on a
// discriminator field.
//
// For most use cases, call [DocumentDecoder.Unmarshal] to get the full
// validation pipeline:
//
//	for _, doc := range decoder.Documents() {
//		config, err := doc.Unmarshal[Config](ctx, niceyaml.WithSchema(validator))
//		if err != nil {
//			return err
//		}
//	}
//
// Use [DocumentDecoder.Decode] directly when you need decoding without
// validation hooks. [DocumentDecoder.UnmarshalInto] and
// [DocumentDecoder.DecodeInto] fill a value you already hold, such as one
// pre-populated with defaults. All decoding methods convert YAML errors to
// [Error] with source annotations.
//
// Create instances with [NewDocumentDecoder] or iterate with [Decoder.Documents].
type DocumentDecoder struct {
	doc        *ast.DocumentNode
	filePath   string
	tokens     token.Tokens
	decodeOpts []yaml.DecodeOption
	index      int
}

// DocumentContext is what a [DocumentDecoder] knows about its document
// beyond the AST: where it sits in the file, the tokens it came from, and
// how to decode it. [Decoder.Documents] fills it in from the
// [Source]; callers that build a [DocumentDecoder] by hand pass what they
// have and leave the rest zero.
type DocumentContext struct {
	// FilePath is the path of the file the document came from. Schema
	// matchers route on it.
	FilePath string

	// Tokens are the tokens the document came from, with positions
	// relative to the document.
	Tokens token.Tokens

	// YAMLDecodeOptions reach the go-yaml decoder the way
	// [WithYAMLDecodeOptions] sends them for a Source.
	YAMLDecodeOptions []yaml.DecodeOption

	// Index is the 0-indexed position of the document within the file.
	// Errors from the decoder carry it as their document index.
	Index int
}

// NewDocumentDecoder creates a new [*DocumentDecoder] for doc with the given
// context. [Decoder.Documents] is the usual way to get one, since it fills
// the context in from the [Source].
func NewDocumentDecoder(doc *ast.DocumentNode, ctx DocumentContext) *DocumentDecoder {
	return &DocumentDecoder{
		doc:        doc,
		index:      ctx.Index,
		tokens:     ctx.Tokens,
		filePath:   ctx.FilePath,
		decodeOpts: ctx.YAMLDecodeOptions,
	}
}

// Document returns the underlying [*ast.DocumentNode].
func (dd *DocumentDecoder) Document() *ast.DocumentNode {
	return dd.doc
}

// Index returns the 0-indexed position of this document within the file,
// from [DocumentContext.Index].
func (dd *DocumentDecoder) Index() int {
	return dd.index
}

// Tokens returns the tokens for this document, from
// [DocumentContext.Tokens]. Returns nil when none were given.
func (dd *DocumentDecoder) Tokens() token.Tokens {
	return dd.tokens
}

// FilePath returns the path of the file the document came from, from
// [DocumentContext.FilePath]. Returns an empty string when none was given.
func (dd *DocumentDecoder) FilePath() string {
	return dd.filePath
}

// Get decodes the YAML value at path into a T without unmarshaling the whole
// document.
//
// This is useful when you need a typed field before deciding how to process
// the document, such as a version number or a list of tags:
//
//	versionPath := paths.Root().Child("version").Path()
//	for _, doc := range decoder.Documents() {
//		version, err := doc.Get[int](ctx, versionPath)
//		if errors.Is(err, niceyaml.ErrValueNotFound) {
//			version = 1
//		} else if err != nil {
//			return err
//		}
//	}
//
// Returns [ErrValueNotFound] if path is nil, the document is a directive, or
// no value exists at the path. YAML decoding errors, including a value that
// cannot be represented as T, are converted to [Error] with source
// annotations.
//
// For a string view of any node, including mappings and sequences, use
// [DocumentDecoder.GetValue].
func (dd *DocumentDecoder) Get[T any](ctx context.Context, path *paths.Path) (T, error) {
	var zero T

	node := dd.node(path)
	if node == nil {
		return zero, fmt.Errorf("%w: %s", ErrValueNotFound, path)
	}

	var v T

	err := dd.decodeNode(ctx, node, &v)
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
//	kindPath := paths.Root().Child("kind").Path()
//	for _, doc := range decoder.Documents() {
//		kind, _ := doc.GetValue(kindPath)
//		switch kind {
//		case "Pod":
//			// Unmarshal to Pod struct.
//		case "Service":
//			// Unmarshal to Service struct.
//		}
//	}
//
// For scalar values (strings, numbers, booleans), returns the semantic value
// rather than YAML syntax. For example, `kind: ""` returns an empty string,
// not the literal `""`. Null values return an empty string with found=true.
//
// For non-scalar values (mappings, sequences), returns the YAML representation.
//
// Returns an empty string and false if path is nil, the document is a
// directive, or no value exists at the path.
//
// For a typed value, use [DocumentDecoder.Get].
func (dd *DocumentDecoder) GetValue(path *paths.Path) (string, bool) {
	node := dd.node(path)
	if node == nil {
		return "", false
	}

	// Use GetValue() for scalar nodes to get the actual semantic value.
	if scalar, ok := node.(ast.ScalarNode); ok {
		v := scalar.GetValue()
		if v == nil {
			return "", true // NullNode.
		}

		return fmt.Sprintf("%v", v), true
	}

	// For non-scalar nodes (mappings, sequences), return YAML representation.
	return node.String(), true
}

// node resolves path against the document body, ignoring the path's
// [paths.Part].
//
// Returns nil if path is nil, the document is a directive, or no node exists
// at the path.
func (dd *DocumentDecoder) node(path *paths.Path) ast.Node {
	if path == nil {
		return nil
	}

	if dd.doc.Body != nil && dd.doc.Body.Type() == ast.DirectiveType {
		return nil
	}

	node, err := path.Node(dd.doc)
	if err != nil {
		return nil
	}

	return node
}

// ValidateSchema decodes the document to [any] and validates it using sv.
//
// Returns decoding errors or errors from the [SchemaValidator] ValidateSchema
// method.
func (dd *DocumentDecoder) ValidateSchema(ctx context.Context, sv SchemaValidator) error {
	var untypedData any

	err := dd.decodeNode(ctx, dd.doc.Body, &untypedData)
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
func (dd *DocumentDecoder) locate(err error) error {
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

// Decode decodes the document into a new T.
//
// YAML decoding errors are converted to [Error] with source annotations. On
// error, the returned T is the zero value.
//
// To decode into a value you already hold, use [DocumentDecoder.DecodeInto].
func (dd *DocumentDecoder) Decode[T any](ctx context.Context) (T, error) {
	var v T

	err := dd.DecodeInto(ctx, &v)
	if err != nil {
		var zero T

		return zero, err
	}

	return v, nil
}

// DecodeInto decodes the document into v, which must be a pointer.
//
// Fields absent from the document keep their existing values, so v may be
// pre-populated with defaults. YAML decoding errors are converted to [Error]
// with source annotations.
func (dd *DocumentDecoder) DecodeInto(ctx context.Context, v any) error {
	return dd.decodeNode(ctx, dd.doc.Body, v)
}

// UnmarshalOption configures [DocumentDecoder.Unmarshal] and
// [DocumentDecoder.UnmarshalInto].
//
// Available options:
//   - [WithSchema]
type UnmarshalOption func(*unmarshalConfig)

// unmarshalConfig holds the settings an [UnmarshalOption] configures.
type unmarshalConfig struct {
	schemas []SchemaValidator
}

// WithSchema is an [UnmarshalOption] that validates the document against sv
// before decoding it. The document is decoded to [any] and handed to
// ValidateSchema, and a validation error ends the unmarshal before any typed
// decoding. Several schemas run in the order given, stopping at the first
// that fails:
//
//	config, err := doc.Unmarshal[Config](ctx, niceyaml.WithSchema(validator))
func WithSchema(sv SchemaValidator) UnmarshalOption {
	return func(c *unmarshalConfig) {
		c.schemas = append(c.schemas, sv)
	}
}

// Unmarshal validates and decodes the document into a new T.
//
// Each [SchemaValidator] from [WithSchema] runs before decoding. If *T
// implements [Validator], Validate is called after successful decoding.
// Methods declared on T itself are included in the method set of *T, so both
// value and pointer receivers participate. On error, the returned T is the
// zero value.
//
// To unmarshal into a value you already hold, use
// [DocumentDecoder.UnmarshalInto].
func (dd *DocumentDecoder) Unmarshal[T any](ctx context.Context, opts ...UnmarshalOption) (T, error) {
	var v T

	err := dd.UnmarshalInto(ctx, &v, opts...)
	if err != nil {
		var zero T

		return zero, err
	}

	return v, nil
}

// UnmarshalInto validates and decodes the document into v, which must be a
// pointer.
//
// Each [SchemaValidator] from [WithSchema] runs before decoding. If v
// implements [Validator], Validate is called after successful decoding.
// Fields absent from the document keep their existing values, so v may be
// pre-populated with defaults.
func (dd *DocumentDecoder) UnmarshalInto(ctx context.Context, v any, opts ...UnmarshalOption) error {
	var cfg unmarshalConfig

	for _, opt := range opts {
		opt(&cfg)
	}

	for _, sv := range cfg.schemas {
		err := dd.ValidateSchema(ctx, sv)
		if err != nil {
			return err
		}
	}

	// Decode to typed struct.
	err := dd.DecodeInto(ctx, v)
	if err != nil {
		return err
	}

	// Self-validation.
	if validator, ok := v.(Validator); ok {
		return dd.locate(validator.Validate())
	}

	return nil
}

// decodeNode decodes node to v and converts YAML errors.
func (dd *DocumentDecoder) decodeNode(ctx context.Context, node ast.Node, v any) error {
	dec := yaml.NewDecoder(bytes.NewReader(nil), dd.decodeOpts...)
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
