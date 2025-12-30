package niceyaml

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"iter"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
)

// Validator validates any data. Implementers should return an [Error]
// pointing to the relevant YAML token if validation fails.
type Validator interface {
	Validate(v any) error
}

// Decoder decodes YAML documents from an [*ast.File].
type Decoder struct {
	f *ast.File
}

// NewDecoder creates a new [Decoder] for the given [ast.File].
func NewDecoder(f *ast.File) *Decoder {
	return &Decoder{f}
}

// DocumentCount returns the number of YAML documents in the file.
func (d *Decoder) DocumentCount() int {
	return len(d.f.Docs)
}

// Documents returns an iterator over all documents in the YAML file.
func (d *Decoder) Documents() iter.Seq2[int, *DocumentDecoder] {
	return func(yield func(int, *DocumentDecoder) bool) {
		for i, doc := range d.f.Docs {
			if !yield(i, NewDocumentDecoder(d.f, doc)) {
				return
			}
		}
	}
}

// DocumentDecoder validates and decodes a single [*ast.DocumentNode].
//
// Note: Both [DocumentDecoder.Validate] and [DocumentDecoder.Decode] perform decode
// operations. [DocumentDecoder.Validate] decodes to [any] for schema validation, and
// [DocumentDecoder.Decode] decodes to the typed struct. However, both decodes use the
// same pre-parsed AST, so there is no overhead from YAML re-parsing. This is necessary
// because we must construct [any] for our [Validator], prior to decoding into typed structs.
type DocumentDecoder struct {
	f   *ast.File
	doc *ast.DocumentNode
}

// NewDocumentDecoder creates a new [DocumentDecoder].
func NewDocumentDecoder(f *ast.File, doc *ast.DocumentNode) *DocumentDecoder {
	return &DocumentDecoder{
		f:   f,
		doc: doc,
	}
}

// GetValue returns the value at the given path in the specified document as a string.
// The boolean return value indicates whether a value was found at the path.
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

// ValidateDecode is a convenience method that calls [DocumentDecoder.Validate] and then
// [DocumentDecoder.Decode] if validation succeeds.
func (dd *DocumentDecoder) ValidateDecode(v any, validator Validator) error {
	return dd.ValidateDecodeContext(context.Background(), v, validator)
}

// ValidateDecodeContext is a convenience method that calls [DocumentDecoder.ValidateContext] and then
// [DocumentDecoder.DecodeContext] if validation succeeds.
func (dd *DocumentDecoder) ValidateDecodeContext(ctx context.Context, v any, validator Validator) error {
	err := dd.ValidateContext(ctx, validator)
	if err != nil {
		return err
	}

	err = dd.DecodeContext(ctx, v)
	if err != nil {
		return err
	}

	return nil
}

// Validate decodes the document at the specified doc index into [any]
// and validates it using the given [Validator].
// Any YAML decoding errors are converted to [Error] with source annotations.
// The validator is responsible for returning an [Error] pointing to the relevant
// YAML token if validation fails.
func (dd *DocumentDecoder) Validate(validator Validator) error {
	return dd.ValidateContext(context.Background(), validator)
}

// ValidateContext decodes the document at the specified doc index into [any]
// with [context.Context] and validates it using the given [Validator].
// Any YAML decoding errors are converted to [Error] with source annotations.
// The validator is responsible for returning an [Error] pointing to the relevant
// YAML token if validation fails.
func (dd *DocumentDecoder) ValidateContext(ctx context.Context, validator Validator) error {
	var v any

	err := dd.DecodeContext(ctx, &v)
	if err != nil {
		return err
	}

	ew := NewErrorWrapper(WithFile(dd.f))
	err = validator.Validate(v)
	if err != nil {
		return ew.Wrap(fmt.Errorf("invalid document: %w", err))
	}

	return nil
}

// Decode decodes the specified document into v.
// Any YAML decoding errors are converted to [Error] with source annotations.
func (dd *DocumentDecoder) Decode(v any) error {
	return dd.DecodeContext(context.Background(), v)
}

// DecodeContext decodes the specified document into v with [context.Context].
// Any YAML decoding errors are converted to [Error] with source annotations.
func (dd *DocumentDecoder) DecodeContext(ctx context.Context, v any) error {
	dec := yaml.NewDecoder(bytes.NewReader(nil))
	err := dec.DecodeFromNodeContext(ctx, dd.doc.Body, v)
	if err == nil {
		return nil
	}

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
