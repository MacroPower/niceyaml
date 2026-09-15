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
// [ParseDocumentDirective] to find the directive in one document's tokens.
//
// # Generation and Validation
//
// Generate JSON schemas from Go types with
// [go.jacobcolvin.com/x/jsonschema] directly, or at build time with its
// `cmd/gen` go:generate tool; a type customizes its generated schema by
// implementing a JSONSchemaExtend method that the library calls:
//
//	func (t MyType) JSONSchemaExtend(_ context.Context, _ jsonschema.TypeContext, ts *jsonschema.TypeSchema) error {
//	    f := ts.Value.Properties["myField"]
//	    f.Description = "Custom description"
//	    f.MinLength = new(1)
//
//	    return nil
//	}
//
// Compile a schema with [go.jacobcolvin.com/x/jsonschema.CompileJSON] (or
// [go.jacobcolvin.com/x/jsonschema.MustCompileJSON] for embedded schemas) and
// wrap it with [NewValidator] to obtain a [*Validator], a
// [go.jacobcolvin.com/niceyaml.SchemaValidator] that reports failures as errors
// carrying YAML path information for integration with niceyaml's error display:
//
//	v := schema.NewValidator(jsonschema.MustCompileJSON(schemaBytes))
//	if err := doc.ValidateSchema(ctx, v); err != nil {
//	    // err is *niceyaml.Error with path info for highlighting.
//	}
//
// To validate and decode in one step, pass the validator to
// [go.jacobcolvin.com/niceyaml.Document.Decode] with
// [go.jacobcolvin.com/niceyaml.WithSchema].
//
// # Resolution
//
// When a document's schema is unknown ahead of time, a [Resolver]
// finds it. Resolve inspects the document and returns a [Ref], which names
// the schema by URL and loads its bytes on demand, or reports [ErrNoMatch]
// when the resolver does not apply. The
// [go.jacobcolvin.com/niceyaml/schema/registry] package tries resolvers in
// order, caches compiled validators by URL, and validates documents against
// the first schema found; the
// [go.jacobcolvin.com/niceyaml/schema/loader] package supplies resolvers
// that read a fixed schema from memory, disk, or HTTP.
package schema
