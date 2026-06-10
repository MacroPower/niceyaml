package validator

import (
	"encoding/json"
	"errors"
	"fmt"

	"go.jacobcolvin.com/x/jsonschema"
)

var (
	// ErrUnmarshalSchema indicates the schema JSON could not be parsed.
	ErrUnmarshalSchema = errors.New("unmarshal schema")

	// ErrAddResource indicates the schema could not be added as a resource.
	ErrAddResource = errors.New("add schema resource")

	// ErrCompileSchema indicates the schema failed to compile.
	ErrCompileSchema = errors.New("compile schema")

	// ErrValidateSchema indicates an unexpected error occurred during schema
	// validation.
	//
	// This wraps non-validation errors from the underlying library, such as
	// unexpected data types or internal errors.
	ErrValidateSchema = errors.New("validate schema")
)

type validatorConfig struct {
	compiler SchemaCompiler
}

// Option configures a [Validator].
//
// Available options:
//   - [WithCompiler]
type Option func(*validatorConfig)

// WithCompiler is an [Option] that sets a custom [SchemaCompiler] for schema
// compilation.
//
// If not provided, a default compiler backed by [jsonschema.Compile] is used.
func WithCompiler(c SchemaCompiler) Option {
	return func(cfg *validatorConfig) {
		cfg.compiler = c
	}
}

// Validator validates data against a compiled JSON schema and returns errors
// with YAML path information.
//
// Validator implements [niceyaml.SchemaValidator] for use with
// [niceyaml.DocumentDecoder].
//
// Create instances with [New] or [MustNew].
type Validator struct {
	schema Schema
	url    string
}

// New creates a new [*Validator] from JSON schema data.
//
// The url parameter identifies the schema for reference resolution between
// schemas. Returns an error if the schema JSON is invalid or fails to compile.
func New(url string, schemaData []byte, opts ...Option) (*Validator, error) {
	cfg := &validatorConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	var schema any

	err := json.Unmarshal(schemaData, &schema)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnmarshalSchema, err)
	}

	// A JSON Schema document must be an object or a boolean. Reject other
	// top-level JSON values (arrays, strings, numbers, null) before compilation.
	switch schema.(type) {
	case map[string]any, bool:
	default:
		return nil, fmt.Errorf("%w: schema must be a JSON object or boolean", ErrCompileSchema)
	}

	compiler := cfg.compiler
	if compiler == nil {
		compiler = newDefaultCompiler()
	}

	err = compiler.AddResource(url, schema)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrAddResource, err)
	}

	s, err := compiler.Compile(url)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCompileSchema, err)
	}

	return &Validator{schema: s, url: url}, nil
}

// MustNew is like [New] but panics on error.
// Use for schemas known to be valid at compile time, such as embedded schemas.
func MustNew(url string, schemaData []byte, opts ...Option) *Validator {
	v, err := New(url, schemaData, opts...)
	if err != nil {
		panic(err)
	}

	return v
}

// ValidateSchema validates data against the schema.
//
// Returns nil if validation succeeds. On failure, returns a [*niceyaml.Error]
// containing the YAML path to each invalid field for use with
// [niceyaml.Printer].
func (s *Validator) ValidateSchema(data any) error {
	err := s.schema.Validate(data)
	if err == nil {
		return nil
	}

	// A structured validation failure carries per-location paths; convert it to
	// a niceyaml.Error. Anything else is an unexpected internal error.
	var validationErr *jsonschema.ValidationError

	if errors.As(err, &validationErr) {
		return newValidationError(validationErr, s.url)
	}

	return fmt.Errorf("%w: %w", ErrValidateSchema, err)
}
