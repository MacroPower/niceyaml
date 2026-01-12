// Package generator provides JSON Schema generation from Go types.
//
// The [Generator] type creates JSON schemas from Go structs using reflection.
// When package paths are provided, it extracts source code comments to use as
// schema descriptions.
//
//	gen := generator.New(MyConfig{},
//	    generator.WithPackagePaths("./..."),
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
package generator
