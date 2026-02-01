// Package validator validates YAML data against JSON Schema and returns errors
// with precise source locations for rich error display.
//
// The package bridges JSON Schema validation with niceyaml's error reporting
// system. When validation fails, errors include the exact YAML path to each
// invalid field, enabling [niceyaml.Printer] to highlight problematic values
// in their source context.
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
// schemas. Use a relative path or URL that matches how the schema references
// itself or other schemas.
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
// # Schema Routing
//
// For applications with multiple schemas, use the [registry] package to route
// documents to schemas based on content, file paths, or embedded directives:
//
//	import (
//	    "jacobcolvin.com/niceyaml/schema/loader"
//	    "jacobcolvin.com/niceyaml/schema/matcher"
//	    "jacobcolvin.com/niceyaml/schema/registry"
//	)
//
//	reg := registry.New()
//	reg.Register(registry.Directive())
//	reg.RegisterFunc(
//	    matcher.Content(paths.Root().Child("kind").Path(), "Deployment"),
//	    loader.Embedded("deployment.json", deploymentSchema),
//	)
//
// # Validation Errors
//
// When validation fails, the returned [*niceyaml.Error] contains nested errors
// for each validation failure. Each nested error carries a path identifying the
// invalid field.
//
// Paths target either the key or value depending on the error type: structural
// errors (additional properties, missing required fields, array length)
// highlight keys, while value errors (type mismatch, pattern, enum) highlight
// values.
//
// # Custom Compilers
//
// The default [jsonschema] compiler handles most use cases, but applications
// may need custom format validators, remote schema resolution, or integration
// with other JSON Schema libraries. The [SchemaCompiler] interface abstracts
// schema compilation, and [WithCompiler] injects a custom implementation.
package validator
