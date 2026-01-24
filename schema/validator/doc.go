// Package validator validates YAML data against JSON Schema and returns errors
// with precise source locations for rich error display.
//
// The package bridges JSON Schema validation with niceyaml's error reporting
// system.
//
// When validation fails, errors include the exact YAML path to each invalid
// field, enabling [niceyaml.Printer] to highlight problematic values in their
// source context.
//
// # Usage
//
// Create a [Validator] from JSON schema bytes using [New] or [MustNew]:
//
//	v, err := validator.New("config.schema.json", schemaBytes)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// For embedded schemas known valid at compile time:
//	var v = validator.MustNew("config.schema.json", schemaBytes)
//
// The URL parameter identifies the schema for reference resolution between
// schemas.
//
// Use a relative path or URL that matches how the schema references itself or
// other schemas.
//
// # Validation Flow
//
// [Validator] implements [niceyaml.SchemaValidator], integrating with
// [niceyaml.DocumentDecoder] for seamless validation during decoding:
//
//	for _, doc := range decoder.Documents() {
//	    if err := doc.ValidateSchema(v); err != nil {
//	        // err is *niceyaml.Error with path info for highlighting
//	    }
//	}
//
// When validation fails, the returned [*niceyaml.Error] contains nested errors
// for each validation failure.
// Each nested error carries a path identifying the invalid field.
//
// Paths target either the key or value depending on the error type: structural
// errors (additional properties, missing required fields, array length)
// highlight keys, while value errors (type mismatch, pattern, enum) highlight
// values.
//
// # Schema Directives
//
// The package parses yaml-language-server schema directives embedded in YAML
// comments.
// These directives let each YAML file specify its own schema:
//
//	# yaml-language-server: $schema=./config.schema.json
//	name: example
//
// Use [ParseDirective] for single comments or [ParseDocumentDirectives] for
// multi-document YAML streams.
// Both return [Directive] values containing the schema path.
// Directives must appear before any content in their document to be recognized.
//
// # Extensibility
//
// The [Schema] and [SchemaCompiler] interfaces abstract the underlying JSON
// schema implementation.
//
// Use [WithCompiler] to provide a custom compiler, enabling features like
// custom format validators or remote schema loading.
// The default implementation uses [jsonschema].
package validator
