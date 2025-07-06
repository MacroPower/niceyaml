// Package schema provides JSON Schema generation and validation for Go types.
//
// This package wraps [github.com/invopop/jsonschema] for schema generation and
// [github.com/santhosh-tekuri/jsonschema/v6] for validation, with integration
// into niceyaml's error formatting system.
//
// # Schema Generation
//
// The [Generator] type creates JSON schemas from Go structs using reflection.
// When package paths are provided, it extracts source code comments to use as
// schema descriptions.
//
//	gen := schema.NewGenerator(MyConfig{},
//	    schema.WithPackagePaths("./..."),
//	)
//	schemaBytes, err := gen.Generate()
//
// The generated schema includes:
//   - Type information derived from Go struct fields
//   - Field descriptions from source code comments
//   - Links to pkg.go.dev documentation for each type
//
// Use [WithReflector] to customize the underlying jsonschema reflector,
// or [WithLookupCommentFunc] to provide custom comment resolution logic.
//
// If using the generated schema for validation, you should consider embedding
// it in your binary with [embed].
//
// # Schema Validation
//
// The [Validator] type validates data against a compiled JSON schema and
// returns errors with precise YAML path information.
//
//	validator, err := schema.NewValidator("config", schemaBytes)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	if err := validator.Validate(data); err != nil {
//	    fmt.Println(err) // Returns niceyaml.Error with path info.
//	}
//
// Use [MustNewValidator] when the schema is known to be valid at compile time:
//
//	var validator = schema.MustNewValidator("config", schemaBytes)
//
// Validation errors are returned as [niceyaml.Error] values, which include
// the YAML path to the invalid field. This integrates with niceyaml's
// [niceyaml.Printer] for rich error display with source context.
package schema
