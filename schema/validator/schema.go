package validator

import "go.jacobcolvin.com/niceyaml/paths"

// Schema validates data against a compiled JSON schema.
//
// The default implementation wraps [jsonschema.Schema].
// Provide custom implementations via [SchemaCompiler].
type Schema interface {
	// Validate validates the given data against the schema.
	// Returns nil if validation succeeds.
	//
	// If validation fails, the error may implement [SchemaError] to provide
	// detailed information about the failure.
	Validate(data any) error
}

// SchemaCompiler compiles JSON schemas into [Schema] instances for validation.
//
// The default implementation wraps [jsonschema.Compiler].
// Provide custom implementations via [WithCompiler].
type SchemaCompiler interface {
	// AddResource adds a schema document as a resource at the given URL.
	AddResource(url string, doc any) error

	// Compile compiles the schema at the given URL and returns a [Schema].
	Compile(url string) (Schema, error)
}

// SchemaError provides details about a schema validation failure.
//
// Implementations are optional; validation may return any error.
// When implemented, enables rich error details including path highlighting.
//
// The default implementation wraps [jsonschema.ValidationError].
type SchemaError interface {
	error

	// Path returns the path to the invalid data along with the target part.
	// For additionalProperties errors, this includes the invalid property name.
	Path() *paths.Path

	// Message returns a human-readable error message.
	Message() string

	// Causes returns nested validation errors.
	Causes() []SchemaError

	// IsWrapper returns true for structural wrapper errors (allOf, anyOf, etc.)
	// that should be traversed to find concrete errors.
	IsWrapper() bool

	// URL returns the schema URL if available, or empty string.
	URL() string
}
