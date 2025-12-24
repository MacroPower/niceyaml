package niceyaml

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
)

// ErrDocumentIndexOutOfRange is returned if the document index is greater than
// or equal to the number of documents in the YAML file.
var ErrDocumentIndexOutOfRange = errors.New("document index out of range")

// Validator validates arbitrary data, typically used to validate decoded YAML documents.
type Validator interface {
	Validate(v any) error
}

// Parser parses YAML from an [io.Reader] into an [ast.File].
type Parser struct {
	r    io.Reader
	opts []parser.Option
}

// NewParser returns a new [Parser] that reads YAML from r.
func NewParser(r io.Reader, opts ...parser.Option) *Parser {
	return &Parser{r, opts}
}

// Parse reads all YAML from the reader and parses it into an [ast.File].
// Any YAML parsing errors are converted to [Error] with source annotations.
func (p *Parser) Parse() (*ast.File, error) {
	b, err := io.ReadAll(p.r)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}

	file, err := parser.ParseBytes(b, parser.ParseComments, p.opts...)
	if err == nil {
		return file, nil
	}

	var yamlErr yaml.Error
	if errors.As(err, &yamlErr) {
		return nil, NewError(
			errors.New(yamlErr.GetMessage()),
			WithErrorToken(yamlErr.GetToken()),
		)
	}

	//nolint:wrapcheck // Return the original error if it's not a [yaml.Error].
	return nil, err
}

// Decoder decodes YAML documents from an AST [ast.File].
//
// Usage:
//
//	parser := niceyaml.NewParser(yamlReader)
//	astFile, err := parser.Parse()
//	if err != nil {
//		return fmt.Errorf("parse YAML: %w", err)
//	}
//	decoder := niceyaml.NewDecoder(astFile)
//	for i := range decoder.DocumentCount() {
//		kind := decoder.GetValue(i, niceyaml.RootPath().Key("kind"))
//		switch kind {
//		case "Deployment":
//			var deploy appsv1.Deployment
//			err := decoder.ValidateDecode(i, &deploy, deploymentValidator)
//			if err != nil {
//				return err
//			}
//			// Handle deployment...
//		}
//	}
//
// Note: Both [Decoder.Validate] and [Decoder.Decode] perform decode operations.
// [Decoder.Validate] to [any] for schema validation, and [Decoder.Decode] to the typed
// struct. However, both decodes use the same pre-parsed AST, so there is no overhead
// from YAML re-parsing. This is necessary because we must construct [any] for our
// [Validator], prior to decoding into typed structs.
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

// GetValue returns the string value at the given path in the specified document.
// It returns an empty string if the path does not exist or points to a directive.
func (d *Decoder) GetValue(doc int, path yaml.Path) (string, error) {
	if len(d.f.Docs) <= doc {
		return "", fmt.Errorf("get document %d of %d: %w", doc+1, len(d.f.Docs), ErrDocumentIndexOutOfRange)
	}

	docNode := d.f.Docs[doc]

	if docNode.Body != nil && docNode.Body.Type() == ast.DirectiveType {
		return "", nil
	}

	node, err := path.FilterNode(docNode.Body)
	if err != nil {
		return "", fmt.Errorf("get value at path %s: %w", path.String(), err)
	}
	if node == nil {
		return "", nil
	}

	return node.String(), nil
}

// ValidateDecode is a convenience method that calls [Decoder.Validate] and then
// [Decoder.Decode] if validation succeeds.
func (d *Decoder) ValidateDecode(doc int, v any, validator Validator) error {
	return d.ValidateDecodeContext(context.Background(), doc, v, validator)
}

// ValidateDecodeContext is a convenience method that calls [Decoder.ValidateContext] and then
// [Decoder.DecodeContext] if validation succeeds.
func (d *Decoder) ValidateDecodeContext(ctx context.Context, doc int, v any, validator Validator) error {
	err := d.ValidateContext(ctx, doc, validator)
	if err != nil {
		return err
	}

	err = d.DecodeContext(ctx, doc, v)
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
func (d *Decoder) Validate(doc int, validator Validator) error {
	return d.ValidateContext(context.Background(), doc, validator)
}

// ValidateContext decodes the document at the specified doc index into [any]
// with [context.Context] and validates it using the given [Validator].
// Any YAML decoding errors are converted to [Error] with source annotations.
// The validator is responsible for returning an [Error] pointing to the relevant
// YAML token if validation fails.
func (d *Decoder) ValidateContext(ctx context.Context, doc int, validator Validator) error {
	var v any

	err := d.DecodeContext(ctx, doc, &v)
	if err != nil {
		return err
	}

	ew := NewErrorWrapper(WithFile(d.f))
	err = validator.Validate(v)
	if err != nil {
		return ew.Wrap(fmt.Errorf("invalid document: %w", err))
	}

	return nil
}

// Decode decodes the specified document into v.
// Any YAML decoding errors are converted to [Error] with source annotations.
func (d *Decoder) Decode(doc int, v any) error {
	return d.DecodeContext(context.Background(), doc, v)
}

// DecodeContext decodes the specified document into v with [context.Context].
// Any YAML decoding errors are converted to [Error] with source annotations.
func (d *Decoder) DecodeContext(ctx context.Context, doc int, v any) error {
	if len(d.f.Docs) <= doc {
		return fmt.Errorf("get document %d of %d: %w", doc+1, len(d.f.Docs), ErrDocumentIndexOutOfRange)
	}

	dec := yaml.NewDecoder(bytes.NewReader(nil))
	err := dec.DecodeFromNodeContext(ctx, d.f.Docs[doc].Body, v)
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
