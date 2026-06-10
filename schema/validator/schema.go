package validator

// Schema validates data against a compiled JSON schema.
//
// The default implementation is the compiled [jsonschema.Validator], whose
// [jsonschema.ValidationError] failures are converted into a [niceyaml.Error]
// with per-location YAML paths. Provide custom implementations via
// [SchemaCompiler].
type Schema interface {
	// Validate validates the given data against the schema.
	// Returns nil if validation succeeds.
	//
	// A returned error that unwraps to [*jsonschema.ValidationError] gains
	// rich, path-annotated reporting; any other error is treated as an
	// unexpected internal failure.
	Validate(data any) error
}

// SchemaCompiler compiles JSON schemas into [Schema] instances for validation.
//
// The default implementation is backed by [jsonschema.Compile].
// Provide custom implementations via [WithCompiler].
type SchemaCompiler interface {
	// AddResource adds a schema document as a resource at the given URL.
	AddResource(url string, doc any) error

	// Compile compiles the schema at the given URL and returns a [Schema].
	Compile(url string) (Schema, error)
}
