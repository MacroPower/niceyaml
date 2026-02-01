// Package loader provides strategies for loading schema data for validation.
//
// Loaders retrieve schema data from various sources and return it for
// compilation by [registry.Registry]. A [Loader] receives the document being
// validated and returns a [Result] containing either raw schema bytes or a
// pre-compiled validator.
//
// Loaders fall into two categories: static and dynamic. Static loaders return
// the same schema regardless of the document content—useful when you know
// exactly which schema applies. Dynamic loaders inspect the document to
// determine which schema to load, enabling scenarios like selecting a schema
// based on a "kind" field or resolving an inline schema directive.
//
// Example static loader with embedded schema:
//
//	//go:embed schema.json
//	var schemaBytes []byte
//
//	l := loader.Embedded("schema.json", schemaBytes)
//
// Example dynamic loader that selects schema based on document content:
//
//	kindPath := paths.Root().Child("kind").Path()
//	l := loader.Custom(func(ctx context.Context, doc *niceyaml.DocumentDecoder) ([]byte, string, error) {
//	    kind, _ := doc.GetValue(kindPath)
//	    schemaPath := fmt.Sprintf("schemas/%s.json", strings.ToLower(kind))
//	    data, err := schemaFS.ReadFile(schemaPath)
//	    return data, schemaPath, err
//	})
package loader
