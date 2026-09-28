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
// Create a [*Store] and hand it to a
// [go.jacobcolvin.com/niceyaml/schema.Registry]:
//
//	reg := schema.NewRegistry(schema.WithResolvers(schemastore.New()))
//
// This one resolver handles all SchemaStore schemas. It matches file
// paths against the catalog patterns for tools like GitHub Actions and
// Docker Compose. The store keeps only the patterns that can match a
// YAML or JSON file. It drops a pattern whose file name carries another
// extension, such as *.toml, and keeps a name without an extension, such
// as .clang-format.
//
// New performs no I/O. The first lookup fetches the catalog, and later
// lookups reuse it until the cache TTL expires. A refresh that fails keeps
// the previous catalog in use. When a fetch fails and no earlier one
// succeeded, a lookup reports [ErrFetchCatalog], and the store waits the
// retry interval before contacting SchemaStore.org again.
package schemastore
