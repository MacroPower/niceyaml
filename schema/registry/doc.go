// Package registry routes YAML documents to schemas using pluggable
// resolvers.
//
// A [Resolver] finds the schema for a document in one call, reporting
// [ErrNoMatch] when it does not apply. The [Registry] type tries its
// resolvers in order and validates the document against the first schema
// found. Matchers from [matcher] and loaders from [loader] pair into a
// resolver through [Registry.RegisterFunc].
//
// # Usage
//
// Create a registry and register resolvers:
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
// Registrations are evaluated in order; the first resolver that does not
// report [ErrNoMatch] wins. A common pattern prioritizes explicit user intent
// first (directives), then content-based matching, then file path
// conventions:
//
//	reg.Register(registry.Directive())                           // Explicit user intent.
//	reg.RegisterFunc(matcher.Content(...), loader.Embedded(...)) // By content.
//	reg.RegisterFunc(matcher.FilePath(...), loader.File(...))    // By path.
//
// # Schema Caching
//
// Compiled validators are cached by schema URL to avoid recompilation.
//
// # SchemaStore Integration
//
// For automatic schema discovery based on file paths, use the
// [registry/schemastore] package which implements [Resolver]:
//
//	store, _ := schemastore.New(ctx)
//	reg.Register(store)
package registry
