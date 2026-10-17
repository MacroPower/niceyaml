// Package schemastore uses the SchemaStore.org catalog to discover the
// schema of a YAML document from its file path.
//
// SchemaStore.org maintains a catalog of JSON schemas for common
// configuration files. This package fetches the catalog on the first
// lookup, matches YAML files against catalog patterns, and names the
// matching schema for the registry to load.
//
// # Usage
//
// Create a [*Store] and pass it to a
// [go.jacobcolvin.com/niceyaml/schema.Registry]:
//
//	reg := schema.NewRegistry(schema.WithResolvers(schemastore.New()))
//
// One [Store] covers the whole catalog. It matches file paths against the
// catalog patterns for tools such as GitHub Actions and Docker Compose.
// When several entries match a path, the store picks the one with the most
// specific pattern. So a pattern for the directory of one tool wins over a
// broad one such as "**/tasks/*.yml".
//
// The store drops some patterns from the catalog:
//
//   - The patterns for formats that are not YAML, such as *.toml and
//     *.jsonc.
//   - The patterns whose braces expand to too many patterns or take too
//     much work to expand.
//   - The patterns that hold an extglob group, such as "!(config).yml",
//     because the matcher of the store does not implement extglob.
//
// Entries left without patterns do not match any file. So the entry for
// GitHub issue forms, which lists only extglob patterns, never applies.
// The store keeps every other pattern, so YAML formats with an extension
// of their own, such as CITATION.cff, still match. So do names without an
// extension, such as .clang-format.
//
// The program does not maintain the schemas in the catalog, and some of
// them name a document in a $ref that does not load. By default, a
// registry fails every document that such a schema covers. With
// [go.jacobcolvin.com/niceyaml/schema.WithRequireRefs] set to false, the
// schema compiles, and only the documents that reach the $ref fail:
//
//	reg := schema.NewRegistry(
//	    schema.WithResolvers(schemastore.New()),
//	    schema.WithCompileOptions(schema.WithRequireRefs(false)),
//	)
//
// [New] does not perform I/O. The first lookup fetches the catalog, and
// later lookups reuse it until the cache time to live (TTL) expires. A
// refresh that fails keeps the previous catalog in use. When a fetch fails
// before any catalog has loaded, a lookup reports [ErrFetchCatalog]. The
// store then waits the retry interval before it contacts SchemaStore.org
// again.
package schemastore
