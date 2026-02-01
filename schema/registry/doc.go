// Package registry routes YAML documents to schemas using pluggable matchers
// and loaders.
//
// The [Registry] type orchestrates schema validation by combining matchers
// from [matcher] with loaders from [loader] to select and apply schemas to
// documents.
//
// # Usage
//
// Create a registry and register match-loader pairs:
//
//	reg := registry.New()
//
//	// Directive matching first (i.e. explicit user intent).
//	reg.Register(registry.Directive())
//
//	// Content-based matching.
//	kindPath := paths.Root().Child("kind").Path()
//	reg.RegisterFunc(
//	    matcher.Content(kindPath, "Deployment"),
//	    loader.Embedded("deployment.json", deploymentSchema),
//	)
//
//	// Validate documents.
//	for _, doc := range decoder.Documents() {
//	    if err := reg.ValidateDocument(ctx, doc); err != nil {
//	        return err
//	    }
//	}
//
// # Registration Order
//
// Registrations are evaluated in order; first match wins. A common pattern
// prioritizes explicit user intent first (directives), then content-based
// matching, then file path conventions:
//
//	reg.Register(registry.Directive())                           // Explicit user intent.
//	reg.RegisterFunc(matcher.Content(...), loader.Embedded(...)) // By content.
//	reg.RegisterFunc(matcher.FilePath(...), loader.File(...))    // By path.
//
// # Schema Caching
//
// Compiled validators are cached by schema URL to avoid recompilation. By
// default, an unbounded thread-safe map is used. For custom caching strategies
// (LRU, TTL, external cache), implement the [Cache] interface and provide it
// via [WithCache].
//
// # SchemaStore Integration
//
// For automatic schema discovery based on file paths, use the
// [registry/schemastore] package which implements [MatchLoader]:
//
//	store, _ := schemastore.New(ctx)
//	reg.Register(store)
package registry
