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
// The program picks the catalog when it adds the store. A registry
// therefore loads each schema that the store names as a schema of the
// program. The registry does not need
// [go.jacobcolvin.com/niceyaml/schema.WithAccess] for those schemas. That
// option gates only the schemas that a document names for itself. An
// Access that the registry does hold applies to them, as
// [go.jacobcolvin.com/niceyaml/schema.AllowURLPrefixes] describes.
//
// [New] does not perform I/O. The first lookup fetches the catalog, and
// later lookups reuse it until the cache time to live (TTL) expires. A
// refresh that fails keeps the previous catalog in use. When a fetch fails
// before any catalog has loaded, a lookup reports [ErrFetchCatalog]. The
// store then waits the retry interval before it loads the catalog again.
//
// A [Store] reads the catalog from SchemaStore.org by default.
// [WithCatalog] names another catalog with a
// [go.jacobcolvin.com/niceyaml/schema.Ref], such as a mirror URL, a file
// on disk, or a catalog that the program embeds. A [Catalog] marshaled to
// JSON gives the bytes of such a catalog:
//
//	store := schemastore.New(schemastore.WithCatalog(schema.Embedded(catalogJSON)))
//
// Until a catalog loads, [ErrFetchCatalog] ends the lookup of the
// registry. A program that should go on to the next resolver when the
// catalog does not load wraps the store in a
// [go.jacobcolvin.com/niceyaml/schema.ResolverFunc]:
//
//	store := schemastore.New()
//
//	failOpen := schema.ResolverFunc(func(ctx context.Context, doc *niceyaml.Node) (schema.Ref, error) {
//	    ref, err := store.Resolve(ctx, doc)
//	    if errors.Is(err, schemastore.ErrFetchCatalog) {
//	        return schema.Ref{}, fmt.Errorf("%w: %v", schema.ErrNoMatch, err)
//	    }
//
//	    return ref, err
//	})
//
// The wrapper formats the error of the store with %v, not %w. The
// registry ends the lookup on any error that matches
// [go.jacobcolvin.com/niceyaml/schema.ErrLoad] even when it wraps
// [go.jacobcolvin.com/niceyaml/schema.ErrNoMatch]. The error of a
// [go.jacobcolvin.com/niceyaml/schema.Loadable] that loads the catalog
// from another registry matches ErrLoad.
//
// A long-running program can take control of the catalog load. A call to
// [Store.Entries] at startup fetches the catalog before the first
// document and returns the entries that the store matches against. Calls
// to [Store.Refresh] on a ticker fetch the catalog at once and report
// each failed fetch. Lookups hide such a failure while an earlier catalog
// stays in use:
//
//	store := schemastore.New()
//
//	entries, err := store.Entries(ctx)
//	if err != nil {
//	    return err
//	}
//	for _, e := range entries {
//	    fmt.Println(e.Name, e.URL, e.FileMatch)
//	}
//
//	// Later, on a ticker:
//	if err := store.Refresh(ctx); err != nil {
//	    log.Printf("schema catalog is stale: %v", err)
//	}
package schemastore
