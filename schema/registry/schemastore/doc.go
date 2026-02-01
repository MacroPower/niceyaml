// Package schemastore integrates with SchemaStore.org for automatic schema
// discovery based on file paths.
//
// SchemaStore.org maintains a catalog of JSON schemas for common configuration
// files. This package fetches the catalog during construction, matches YAML
// files against catalog patterns, and loads the appropriate schemas.
//
// # Usage
//
// Create a [*SchemaStore] and register it with a [registry.Registry]:
//
//	store, err := schemastore.New(ctx)
//	if err != nil {
//	    log.Printf("schemastore unavailable: %v", err)
//	    return
//	}
//	reg.Register(store)
//
// This single registration handles all SchemaStore schemas, matching file
// paths against catalog patterns for common tools like GitHub Actions,
// Docker Compose, and many others. Only YAML file patterns are considered.
package schemastore
