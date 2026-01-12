// Package validator provides JSON Schema validation for data.
//
// The [Validator] type validates data against a compiled JSON schema and
// returns errors with precise YAML path information.
//
//	v, err := validator.New("config", schemaBytes)
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
//	var v = validator.MustNew("config", schemaBytes)
//
// Validation errors are returned as [niceyaml.Error] values, which include
// the YAML path to the invalid field. This integrates with niceyaml's
// [niceyaml.Printer] for rich error display with source context.
package validator
