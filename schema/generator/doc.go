// Package generator creates JSON schemas from Go types, bridging Go struct
// definitions to JSON Schema for YAML validation workflows.
//
// Many configuration systems define their structure using Go structs with JSON
// or YAML tags.
//
// This package extracts that structure into a JSON Schema that can validate
// configuration files, power IDE autocompletion, and generate documentation.
//
// It leverages [jsonschema] for reflection while adding Go source comment
// extraction for richer schema descriptions.
//
// # Usage
//
// Create a [*Generator] with your configuration struct and call
// [Generator.Generate]:
//
//	type Config struct {
//	    Port    int    `json:"port"    jsonschema:"title=Port"`
//	    Timeout string `json:"timeout" jsonschema:"title=Timeout"`
//	}
//
//	gen := generator.New(Config{})
//	schemaBytes, err := gen.Generate()
//
// The resulting JSON Schema can be used with
// [go.jacobcolvin.com/niceyaml/schema/validator] to validate YAML files
// against your Go type definitions.
//
// # Comment Extraction
//
// By default, schemas contain only type information. Use [WithPackagePaths] to
// parse Go source files and extract doc comments as schema descriptions:
//
//	gen := generator.New(Config{},
//	    generator.WithPackagePaths("./config/..."),
//	)
//
// This walks the Go AST to find comments on types and struct fields, then
// includes them in the schema's "description" fields.
//
// The default comment formatter, [DefaultLookupCommentFunc], also appends
// pkg.go.dev URLs for each type, providing a quick reference link in IDEs that
// display schema descriptions.
//
// For custom comment formatting, use [WithLookupCommentFunc] to provide a
// [LookupCommentFunc] that transforms the comment map into description strings.
//
// # Reflector Customization
//
// The underlying [github.com/invopop/jsonschema.Reflector] can be customized
// via [WithReflector] to control schema generation behavior like reference
// handling and additional properties.
package generator
