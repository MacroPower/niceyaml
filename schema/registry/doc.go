// Package registry routes YAML documents to schemas using pluggable
// resolvers.
//
// A [go.jacobcolvin.com/niceyaml/schema.Resolver] names the schema for a
// document in one call, reporting
// [go.jacobcolvin.com/niceyaml/schema.ErrNoMatch] when it does not apply.
// The [Registry] type tries its resolvers in order and validates the
// document against the first schema named.
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
//	reg.Register(registry.When(
//	    matcher.Content(kindPath, "Deployment"),
//	    loader.Embedded("example.com/k8s/deployment.json", deploymentSchema),
//	))
//
//	// Validate documents.
//	for _, doc := range decoder.Documents() {
//	    if err := reg.ValidateDocument(ctx, doc); err != nil {
//	        return err
//	    }
//	}
//
// # Resolvers
//
// The loaders in [go.jacobcolvin.com/niceyaml/schema/loader] are resolvers
// that name the same schema for every document, so registering one on its
// own validates everything against it. [When] guards any resolver with a
// [go.jacobcolvin.com/niceyaml/schema/matcher.Matcher], so the schema
// applies only to documents the matcher accepts. [Directive] reads the
// schema a document names for itself in a yaml-language-server comment.
// Implement [go.jacobcolvin.com/niceyaml/schema.Resolver], or wrap a
// function in [go.jacobcolvin.com/niceyaml/schema.ResolverFunc], for
// resolution that decides and names the schema from the same inspection of
// the document.
//
// # Registration Order
//
// Registrations are evaluated in order; the first resolver that does not
// report [go.jacobcolvin.com/niceyaml/schema.ErrNoMatch] wins. A common
// pattern prioritizes explicit user intent first (directives), then
// content-based matching, then file path conventions:
//
//	reg.Register(
//	    registry.Directive(),                                          // Explicit user intent.
//	    registry.When(matcher.Content(...), loader.Embedded(...)),     // By content.
//	    registry.When(matcher.MustFilePath(...), loader.File(...)),    // By path.
//	)
//
// # Schema Caching
//
// A resolver returns a [go.jacobcolvin.com/niceyaml/schema.Ref] that names
// the schema by URL and loads its bytes on demand. The registry checks its
// cache of compiled validators by URL before calling Load, so each schema is
// loaded and compiled once per registry however many documents name it.
//
// # SchemaStore Integration
//
// For automatic schema discovery based on file paths, use the
// [go.jacobcolvin.com/niceyaml/schema/registry/schemastore] package, whose
// SchemaStore type is a resolver:
//
//	reg.Register(schemastore.New())
package registry
