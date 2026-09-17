// Package schema provides JSON Schema directive parsing and validation for
// YAML documents.
//
// # Schema Directives
//
// Schema directives let YAML files declare their own schema, providing IDE
// integration and explicit validation control. The directive format follows the
// yaml-language-server convention, making schemas work seamlessly in editors
// like VS Code:
//
//	# yaml-language-server: $schema=./config.schema.json
//	name: example
//
// Use [ParseDirective] to extract the schema path from a single comment, and
// [ParseDocumentDirective] to find the directive in one document's tokens.
//
// # Generation and Validation
//
// Generate JSON schemas from Go types with
// [go.jacobcolvin.com/x/jsonschema] directly, or at build time with its
// `cmd/gen` go:generate tool; a type customizes its generated schema by
// implementing a JSONSchemaExtend method that the library calls:
//
//	func (t MyType) JSONSchemaExtend(_ context.Context, _ jsonschema.TypeContext, ts *jsonschema.TypeSchema) error {
//	    f := ts.Value.Properties["myField"]
//	    f.Description = "Custom description"
//	    f.MinLength = new(1)
//
//	    return nil
//	}
//
// Compile a schema with [go.jacobcolvin.com/x/jsonschema.CompileJSON] (or
// [go.jacobcolvin.com/x/jsonschema.MustCompileJSON] for embedded schemas) and
// wrap it with [NewValidator] to obtain a [*Validator], a
// [go.jacobcolvin.com/niceyaml.DocumentValidator] that reports failures as
// errors carrying YAML path information for integration with niceyaml's error
// display:
//
//	v := schema.NewValidator(jsonschema.MustCompileJSON(schemaBytes))
//	if err := doc.Validate(ctx, v); err != nil {
//	    // err is a *niceyaml.SourceError; %+v prints the failing lines.
//	}
//
// To validate and decode in one step, pass the validator to
// [go.jacobcolvin.com/niceyaml.Document.Decode] with
// [go.jacobcolvin.com/niceyaml.WithValidator].
//
// # Resolution
//
// When a document's schema is unknown ahead of time, a [Resolver] finds
// it. Resolve inspects the document and returns a [Ref], which names the
// schema by URL and loads its bytes on demand, or reports [ErrNoMatch] when
// the resolver does not apply. A [Registry] tries its resolvers in order and
// validates the document against the first schema named:
//
//	reg := schema.NewRegistry()
//
//	// Directive matching first (i.e. explicit user intent).
//	reg.Register(schema.Directive())
//
//	// Content-based matching.
//	kindPath := paths.Root().Child("kind")
//	reg.Register(schema.When(
//	    matcher.Content(kindPath, "Deployment"),
//	    schema.Embedded("example.com/k8s/deployment.json", deploymentSchema),
//	))
//
//	// Validate documents.
//	for _, doc := range docs {
//	    if err := reg.Validate(ctx, doc); err != nil {
//	        return err
//	    }
//	}
//
// A Registry implements [go.jacobcolvin.com/niceyaml.DocumentValidator],
// so [go.jacobcolvin.com/niceyaml.WithValidator] runs it before a decode.
// A document no resolver applies to fails with [ErrNoMatch], which is the
// answer a validation command wants. A decode that should check the
// documents it recognizes and accept the rest builds the registry with
// [WithRequireSchema] set false:
//
//	reg := schema.NewRegistry(schema.WithRequireSchema(false))
//	reg.Register(schema.Directive(), schemastore.New())
//
//	config, err := doc.Decode[Config](ctx, niceyaml.WithValidator(reg))
//
// # Loaders
//
// [Embedded], [File], [URL], and [FileOrURL] are resolvers that name the
// same schema for every document and never report [ErrNoMatch], so
// registering one on its own validates everything against it. Each returns
// a [Ref] whose URL identifies the schema and whose Load reads the bytes.
// The registry checks its cache by URL first, so a file is read or a URL
// fetched once per registry, however many documents name it.
//
// Embed a schema in the binary with go:embed:
//
//	//go:embed schema.json
//	var schemaBytes []byte
//
//	reg.Register(schema.Embedded("example.com/config/schema.json", schemaBytes))
//
// Read a schema from disk or over HTTP:
//
//	reg.Register(schema.File("./schemas/config.json"))
//	reg.Register(schema.URL("https://example.com/schema.json"))
//
// [FileOrURL] routes a reference as written in a directive or on a command
// line, which may be a file path or a URL.
//
// # Registration Order
//
// The registry tries registrations in order; the first resolver that does
// not report [ErrNoMatch] wins. [When] guards any resolver with a
// [go.jacobcolvin.com/niceyaml/schema/matcher.Matcher], so the schema
// applies only to documents the matcher accepts, and [Directive] reads the
// schema a document names for itself in a yaml-language-server comment. A
// common order puts explicit user intent first, then content-based
// matching, then file path conventions:
//
//	reg.Register(
//	    schema.Directive(),                                          // Explicit user intent.
//	    schema.When(matcher.Content(...), schema.Embedded(...)),     // By content.
//	    schema.When(matcher.MustFilePath(...), schema.File(...)),    // By path.
//	)
//
// Implement [Resolver], or wrap a function in [ResolverFunc], for
// resolution that decides and names the schema from the same inspection of
// the document.
//
// A resolver error that does not wrap [ErrNoMatch] ends the lookup, and the
// resolvers registered after it do not run. While no catalog has loaded, a
// [go.jacobcolvin.com/niceyaml/schema/schemastore.SchemaStore] that cannot
// reach SchemaStore.org ends the lookup this way, so register it after any
// resolver that should still apply without the catalog.
//
// # Schema Caching
//
// A resolver returns a [Ref] that names the schema by URL and loads its
// bytes on demand. The registry checks its cache of compiled validators by
// URL before calling Load, so each schema is loaded and compiled once per
// registry however many documents name it.
//
// # SchemaStore Integration
//
// For automatic schema discovery based on file paths, use the
// [go.jacobcolvin.com/niceyaml/schema/schemastore] package, whose
// SchemaStore type is a resolver:
//
//	reg.Register(schemastore.New())
package schema
