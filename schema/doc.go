// Package schema provides JSON Schema directive parsing and validation for
// YAML documents.
//
// # Schema Directives
//
// Schema directives let YAML files declare their own schema, providing IDE
// integration and explicit validation control. The directive format follows the
// yaml-language-server convention, making schemas work seamlessly in editors
// like VS Code:
//
//	# yaml-language-server: $schema=./config.schema.json
//	name: example
//
// Use [ParseDirective] to extract the schema path from a single comment, and
// [ParseDocumentDirectives] to handle multi-document streams.
//
// # Generation and Validation
//
// Generate JSON schemas from Go types with
// [go.jacobcolvin.com/x/jsonschema] directly; a type customizes its generated
// schema by implementing a JSONSchemaExtend method that the library calls:
//
//	func (t MyType) JSONSchemaExtend(_ context.Context, _ jsonschema.TypeContext, s *jsonschema.Schema) error {
//	    f := s.Properties["myField"]
//	    f.Description = "Custom description"
//	    f.MinLength = jsonschema.Ptr(1)
//
//	    return nil
//	}
//
// Compile a schema with [go.jacobcolvin.com/x/jsonschema.CompileJSON] (or
// [go.jacobcolvin.com/x/jsonschema.MustCompileJSON] for embedded schemas) and
// wrap it with [NewValidator] to obtain a
// [go.jacobcolvin.com/niceyaml.SchemaValidator] that reports failures as errors
// carrying YAML path information for integration with niceyaml's error display:
//
//	v := schema.NewValidator(jsonschema.MustCompileJSON(schemaBytes))
//	if err := doc.ValidateSchema(v); err != nil {
//	    // err is *niceyaml.Error with path info for highlighting.
//	}
package schema
