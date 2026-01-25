// Package schema provides common types for JSON Schema operations.
//
// This package serves as the shared foundation for the generator and validator
// subpackages, providing type aliases and utilities that allow working with
// JSON schemas without directly depending on the underlying libraries.
//
// # Type Alias
//
// The [JSON] type aliases [jsonschema.Schema], allowing code to import schema
// types from this package rather than the underlying library. This simplifies
// dependency management and provides a stable import path.
//
// # Schema Customization
//
// Types can customize their generated JSON schema representation by
// implementing the JSONSchemaExtend method, which is called by
// [jsonschema] during schema generation:
//
//	func (t MyType) JSONSchemaExtend(js *schema.JSON) {
//	    f := schema.MustGetProperty("myField", js)
//	    f.Description = "Custom description"
//	    f.MinLength = schema.PtrUint64(1)
//	}
//
// Use [GetProperty] to retrieve schema properties by name, returning
// [ErrPropertyNotFound] if the property does not exist. Use [MustGetProperty]
// when the property is known to exist (panics on error).
//
// # Subpackages
//
// The [jacobcolvin.com/niceyaml/schema/generator] package generates JSON
// schemas from Go types using reflection, extracting source code comments as
// descriptions.
//
// The [jacobcolvin.com/niceyaml/schema/validator] package validates data
// against compiled JSON schemas, returning errors with YAML path information
// for integration with niceyaml's error display.
package schema
