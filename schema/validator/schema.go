package validator

import "github.com/macropower/niceyaml/paths"

// Schema validates data against a compiled JSON schema.
// See [jsonschemaAdapter] for an implementation wrapping
// [github.com/santhosh-tekuri/jsonschema/v6.Schema].
type Schema interface {
	// Validate validates the given data against the schema.
	// Returns nil if validation succeeds.
	// If validation fails, the error may implement [ValidationError]
	// to provide detailed information about the failure.
	Validate(data any) error
}

// SchemaCompiler compiles [Schema]s.
// See [defaultCompiler] for an implementation wrapping
// [github.com/santhosh-tekuri/jsonschema/v6.SchemaCompiler].
type SchemaCompiler interface {
	AddResource(url string, doc any) error
	Compile(url string) (Schema, error)
}

// SchemaError provides details about a schema validation failure.
// Implementations are optional; [Schema.Validate] may return any error.
// When implemented, enables rich error details including path highlighting.
// See [jsonschemaValidationError] for an implementation wrapping
// [github.com/santhosh-tekuri/jsonschema/v6.SchemaError].
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
