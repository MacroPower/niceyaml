package niceyaml

import (
	"bytes"
	"context"
	"errors"
	"iter"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
)

// Validator is implemented by types that validate themselves.
// If a type implements this interface, [DocumentDecoder.Unmarshal]
// will automatically call Validate after successful decoding.
type Validator interface {
	Validate() error
}

// SchemaValidator is implemented by types that validate arbitrary data
// against a schema. If a type implements this interface,
// [DocumentDecoder.Unmarshal] will automatically decode the document
// to [any] and call ValidateSchema before decoding to the typed struct.
// See [schema/validator.Validator] for an implementation.
type SchemaValidator interface {
	ValidateSchema(data any) error
}

// Decoder provides iteration over YAML documents in an [*ast.File].
// It wraps a parsed YAML file and yields [DocumentDecoder] instances
// for each document, enabling document-by-document processing of
// both single and multi-document YAML streams.
//
// Create instances with [NewDecoder].
type Decoder struct {
	f *ast.File
}

// NewDecoder creates a new [Decoder] for the given [*ast.File].
func NewDecoder(f *ast.File) *Decoder {
	return &Decoder{f: f}
}

// Len returns the number of YAML documents in the file.
func (d *Decoder) Len() int {
	return len(d.f.Docs)
}

// Documents returns an iterator over all documents in the YAML file.
// Each iteration yields the document index and a [DocumentDecoder]
// for that document.
func (d *Decoder) Documents() iter.Seq2[int, *DocumentDecoder] {
	return func(yield func(int, *DocumentDecoder) bool) {
		for i, doc := range d.f.Docs {
			if !yield(i, NewDocumentDecoder(doc)) {
				return
			}
		}
	}
}

// DocumentDecoder decodes and validates a single [*ast.DocumentNode].
// It provides several methods for working with YAML documents:
//
//   - [DocumentDecoder.GetValue]: Extract a value at a path as a string.
//   - [DocumentDecoder.Decode]: Decode to a typed struct.
//   - [DocumentDecoder.ValidateSchema]: Validate against a [SchemaValidator].
//   - [DocumentDecoder.Unmarshal]: Full validation and decoding pipeline.
//
// Use [DocumentDecoder.Unmarshal] for types implementing [SchemaValidator]
// or [Validator] to get automatic validation. Use [DocumentDecoder.Decode]
// when you only need decoding without validation hooks.
//
// All decoding methods convert YAML errors to [Error] with source annotations.
//
// Create instances with [NewDocumentDecoder].
type DocumentDecoder struct {
	doc *ast.DocumentNode
}

// NewDocumentDecoder creates a new [DocumentDecoder] for the given [*ast.DocumentNode].
func NewDocumentDecoder(doc *ast.DocumentNode) *DocumentDecoder {
	return &DocumentDecoder{
		doc: doc,
	}
}

// GetValue returns the string representation of the value at the given
// path, or an empty string and false if the path is nil, the document
// body is a directive, or no value exists at the path.
func (dd *DocumentDecoder) GetValue(path *yaml.Path) (string, bool) {
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

	return node.String(), true
}

// ValidateSchema decodes the document to [any] and validates it using sv.
// This is a convenience wrapper around [DocumentDecoder.ValidateSchemaContext]
// with [context.Background].
func (dd *DocumentDecoder) ValidateSchema(sv SchemaValidator) error {
	return dd.ValidateSchemaContext(context.Background(), sv)
}

// ValidateSchemaContext decodes the document to [any] and validates it using sv with [context.Context].
// Returns decoding errors or errors from [SchemaValidator.ValidateSchema].
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
// This is a convenience wrapper around [DocumentDecoder.DecodeContext]
// with [context.Background].
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
// This is a convenience wrapper around [DocumentDecoder.UnmarshalContext]
// with [context.Background].
//
// If v implements [SchemaValidator], ValidateSchema is called before decoding.
// If v implements [Validator], Validate is called after successful decoding.
func (dd *DocumentDecoder) Unmarshal(v any) error {
	return dd.UnmarshalContext(context.Background(), v)
}

// UnmarshalContext validates and decodes the document into v with [context.Context].
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
				errors.New(yamlErr.GetMessage()),
				WithErrorToken(yamlErr.GetToken()),
			)
		}

		//nolint:wrapcheck // Return the original error if it's not a [yaml.Error].
		return err
	}

	return nil
}
