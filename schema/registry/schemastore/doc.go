// Package schemastore integrates with SchemaStore.org for automatic schema
// discovery based on file paths.
//
// SchemaStore.org maintains a catalog of JSON schemas for common configuration
// files. This package fetches the catalog on the first lookup, matches YAML
// files against catalog patterns, and names the matching schema for the
// registry to load.
//
// # Usage
//
// Create a [*SchemaStore] and register it with a
// [go.jacobcolvin.com/niceyaml/schema/registry.Registry]:
//
//	reg.Register(schemastore.New())
//
// This single registration handles all SchemaStore schemas, matching file
// paths against catalog patterns for common tools like GitHub Actions,
// Docker Compose, and many others. Only YAML file patterns are considered.
//
// New performs no I/O. The first lookup fetches the catalog; later lookups
// reuse it until the cache TTL expires, and a refresh that fails keeps the
// previous catalog in use. When the catalog cannot be fetched at all, a
// lookup reports [ErrFetchCatalog], and the store waits the retry interval
// before contacting SchemaStore.org again.
package schemastore
