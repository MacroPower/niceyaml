// Package validator provides JSON Schema validation for data.
//
// The [Validator] type validates data against a compiled JSON schema and
// returns errors with precise YAML path information. It implements
// [niceyaml.SchemaValidator] for use with [niceyaml.DocumentDecoder].
//
//	v, err := validator.New("config.schema.json", schemaBytes)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	if err := v.ValidateSchema(data); err != nil {
//	    fmt.Println(err) // Returns niceyaml.Error with path info.
//	}
//
// Use [MustNew] when the schema is known to be valid at compile time:
//
//	var v = validator.MustNew("config.schema.json", schemaBytes)
//
// Validation errors are returned as [niceyaml.Error] values, which include
// the YAML path to the invalid field. This integrates with niceyaml's
// [niceyaml.Printer] for rich error display with source context.
//
// # Error Handling
//
// Construction errors can be checked using the sentinel error variables:
//
//   - [ErrUnmarshalSchema]: Schema JSON could not be parsed.
//   - [ErrAddResource]: Schema could not be added as a resource.
//   - [ErrCompileSchema]: Schema failed to compile.
//   - [ErrValidateSchema]: Unexpected error during validation.
//
// # Configuration
//
// Use [Option] functions to configure the [Validator]:
//
//   - [WithCompiler]: Inject a custom [SchemaCompiler] for schema compilation.
package validator
