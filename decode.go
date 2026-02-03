package niceyaml

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"iter"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/tokens"
)

// SourceDecoder provides access to YAML documents from a [*Source].
// See [Source] for an implementation.
type SourceDecoder interface {
	// Decoder returns a [*Decoder] for iterating over documents.
	Decoder() (*Decoder, error)
}

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
// If a type implements this interface, [DocumentDecoder.Unmarshal]
// automatically decodes the document to [any] and calls ValidateSchema before
// decoding to the typed struct.
//
// See [go.jacobcolvin.com/niceyaml/schema/validator.Validator] for an
// implementation.
type SchemaValidator interface {
	ValidateSchema(data any) error
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
}

// NewDecoder creates a new [*Decoder] for the given [*Source].
//
// Returns an error if the source cannot be parsed.
func NewDecoder(s *Source) (*Decoder, error) {
	f, err := s.File()
	if err != nil {
		return nil, err
	}

	return &Decoder{source: s, file: f}, nil
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
// including file path, tokens, and document index.
func (d *Decoder) Documents() iter.Seq2[int, *DocumentDecoder] {
	filePath := d.source.FilePath()
	srcTokens := d.source.Tokens()

	return func(yield func(int, *DocumentDecoder) bool) {
		var (
			nextTokens func() (int, token.Tokens, bool)
			stop       func()
		)

		if srcTokens != nil {
			nextTokens, stop = iter.Pull2(tokens.SplitDocuments(srcTokens))
			defer stop()
		}

		for i, doc := range d.file.Docs {
			var tks token.Tokens
			if nextTokens != nil {
				_, tks, _ = nextTokens()
			}

			dd := &DocumentDecoder{
				doc:      doc,
				index:    i,
				tokens:   tks,
				filePath: filePath,
			}

			if !yield(i, dd) {
				return
			}
		}
	}
}

// DocumentDecoder decodes and validates a single YAML document.
//
// It separates decoding from document iteration, allowing validation hooks
// to run at the right time during unmarshaling. Types implementing
// [SchemaValidator] are validated before decoding, and types implementing
// [Validator] are validated after. Types may implement both interfaces.
//
// Use [DocumentDecoder.GetValue] to inspect values without unmarshaling,
// which is helpful for routing documents based on a discriminator field.
//
// For most use cases, call [DocumentDecoder.Unmarshal] to get the full
// validation pipeline:
//
//	for _, doc := range decoder.Documents() {
//		var config Config
//		if err := doc.Unmarshal(&config); err != nil {
//			return err
//		}
//	}
//
// Use [DocumentDecoder.Decode] directly when you need decoding without
// validation hooks. All decoding methods convert YAML errors to [Error]
// with source annotations.
//
// Create instances with [NewDocumentDecoder] or iterate with [Decoder.Documents].
type DocumentDecoder struct {
	doc      *ast.DocumentNode
	filePath string
	tokens   token.Tokens
	index    int
}

// NewDocumentDecoder creates a new [*DocumentDecoder] for the given
// [*ast.DocumentNode].
//
// For full context (index, tokens, file path), use [Decoder.Documents] instead,
// which propagates context from the [Decoder].
func NewDocumentDecoder(doc *ast.DocumentNode) *DocumentDecoder {
	return &DocumentDecoder{
		doc: doc,
	}
}

// Document returns the underlying [*ast.DocumentNode].
func (dd *DocumentDecoder) Document() *ast.DocumentNode {
	return dd.doc
}

// Index returns the 0-indexed position of this document within the file.
//
// Returns 0 if created with [NewDocumentDecoder] without context.
func (dd *DocumentDecoder) Index() int {
	return dd.index
}

// Tokens returns the tokens for this document.
//
// Returns nil if created with [NewDocumentDecoder] without context or if the
// [Decoder] was created without tokens.
func (dd *DocumentDecoder) Tokens() token.Tokens {
	return dd.tokens
}

// FilePath returns the file path of the source file.
//
// Returns an empty string if created with [NewDocumentDecoder] without context
// or if the [Decoder] was created without a file path.
func (dd *DocumentDecoder) FilePath() string {
	return dd.filePath
}

// GetValue extracts a YAML value without unmarshaling.
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
func (dd *DocumentDecoder) GetValue(path *paths.YAMLPath) (string, bool) {
	if path == nil {
		return "", false
	}

	if dd.doc.Body != nil && dd.doc.Body.Type() == ast.DirectiveType {
		return "", false
	}

	node, err := path.FilterNode(dd.doc.Body)
	if err != nil || node == nil {
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

// ValidateSchema decodes the document to [any] and validates it using sv.
//
// This is a convenience wrapper around [DocumentDecoder.ValidateSchemaContext]
// with [context.Background].
func (dd *DocumentDecoder) ValidateSchema(sv SchemaValidator) error {
	return dd.ValidateSchemaContext(context.Background(), sv)
}

// ValidateSchemaContext decodes the document to [any] and validates it using sv
// with [context.Context].
//
// Returns decoding errors or errors from the [SchemaValidator] ValidateSchema
// method.
func (dd *DocumentDecoder) ValidateSchemaContext(ctx context.Context, sv SchemaValidator) error {
	var untypedData any

	err := dd.decodeNode(ctx, &untypedData)
	if err != nil {
		return err
	}

	err = sv.ValidateSchema(untypedData)
	if err != nil {
		//nolint:wrapcheck // SchemaValidator.ValidateSchema should return Error with path info.
		return err
	}

	return nil
}

// Decode decodes the document into v.
//
// This is a convenience wrapper around [DocumentDecoder.DecodeContext] with
// [context.Background].
//
// YAML decoding errors are converted to [Error] with source annotations.
func (dd *DocumentDecoder) Decode(v any) error {
	return dd.DecodeContext(context.Background(), v)
}

// DecodeContext decodes the document into v with [context.Context].
// YAML decoding errors are converted to [Error] with source annotations.
func (dd *DocumentDecoder) DecodeContext(ctx context.Context, v any) error {
	return dd.decodeNode(ctx, v)
}

// Unmarshal validates and decodes the document into v.
//
// This is a convenience wrapper around [DocumentDecoder.UnmarshalContext] with
// [context.Background].
//
// If v implements [SchemaValidator], ValidateSchema is called before decoding.
// If v implements [Validator], Validate is called after successful decoding.
func (dd *DocumentDecoder) Unmarshal(v any) error {
	return dd.UnmarshalContext(context.Background(), v)
}

// UnmarshalContext validates and decodes the document into v
// with [context.Context].
//
// If v implements [SchemaValidator], ValidateSchema is called before decoding.
// If v implements [Validator], Validate is called after successful decoding.
func (dd *DocumentDecoder) UnmarshalContext(ctx context.Context, v any) error {
	// Validate if type provides schema validation.
	if sv, ok := v.(SchemaValidator); ok {
		err := dd.ValidateSchemaContext(ctx, sv)
		if err != nil {
			return err
		}
	}

	// Decode to typed struct.
	err := dd.DecodeContext(ctx, v)
	if err != nil {
		return err
	}

	// Self-validation.
	if validator, ok := v.(Validator); ok {
		//nolint:wrapcheck // Validator.Validate should return Error with path info.
		return validator.Validate()
	}

	return nil
}

// decodeNode decodes the document body to v and converts YAML errors.
func (dd *DocumentDecoder) decodeNode(ctx context.Context, v any) error {
	dec := yaml.NewDecoder(bytes.NewReader(nil))
	err := dec.DecodeFromNodeContext(ctx, dd.doc.Body, v)
	if err != nil {
		var yamlErr yaml.Error
		if errors.As(err, &yamlErr) {
			return NewError(
				yamlErr.GetMessage(),
				WithErrorToken(yamlErr.GetToken()),
			)
		}

		//nolint:wrapcheck // Return the original error if it's not a [yaml.Error].
		return err
	}

	return nil
}
