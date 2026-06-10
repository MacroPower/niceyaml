// Package schema provides JSON Schema directive parsing for YAML documents.
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
//	func (t MyType) JSONSchemaExtend(js *jsonschema.Schema) {
//	    f := js.Properties["myField"]
//	    f.Description = "Custom description"
//	    f.MinLength = jsonschema.Ptr(1)
//	}
//
// The [go.jacobcolvin.com/niceyaml/schema/validator] package validates data
// against compiled JSON schemas, returning errors with YAML path information
// for integration with niceyaml's error display.
package schema
