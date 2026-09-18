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
// [Compile] turns a JSON schema document into a [*Schema], a
// [go.jacobcolvin.com/niceyaml.Validator] that reports failures as errors
// carrying the YAML path to each failing location, and [MustCompile] does
// the same at package scope for an embedded schema:
//
//	//go:embed config.schema.json
//	var schemaBytes []byte
//
//	var Config = schema.MustCompile(schemaBytes)
//
//	if err := doc.Validate(ctx, Config); err != nil {
//	    // err is a *niceyaml.SourceError; %+v prints the failing lines.
//	}
//
// To validate and decode in one step, pass the schema to
// [go.jacobcolvin.com/niceyaml.Document.Decode] with
// [go.jacobcolvin.com/niceyaml.WithValidator]. A schema compiled by
// [go.jacobcolvin.com/x/jsonschema] itself, such as one built from a Go
// type, goes through [FromJSONSchema].
//
// Settings of the JSON Schema library pass through [WithJSONSchemaOptions],
// which carries the JSONSchema prefix so the dependency shows at the call
// site, as the options of the root package that pass go-yaml values through
// carry a YAML prefix.
//
// # Resolution
//
// When a document's schema is unknown ahead of time, a [Resolver] finds
// it. Resolve inspects the document and returns a [Ref], which carries a
// compiled schema through [Compiled] or names one by key and loads its
// bytes on demand through [Loadable], or reports [ErrNoMatch] when the
// resolver does not apply. A
// [Registry] tries its resolvers in order and validates the document
// against the first schema named:
//
//	kindPath := paths.Root().Child("kind")
//	reg := schema.NewRegistry(schema.WithResolvers(
//	    // Directive matching first (i.e. explicit user intent).
//	    schema.Directive(),
//	    // Content-based matching.
//	    schema.When(
//	        matcher.Content(kindPath, "Deployment"),
//	        schema.Embedded(deploymentSchema),
//	    ),
//	))
//
//	// Validate documents.
//	for _, doc := range docs {
//	    if err := reg.Validate(ctx, doc); err != nil {
//	        return err
//	    }
//	}
//
// A Registry implements [go.jacobcolvin.com/niceyaml.Validator],
// so [go.jacobcolvin.com/niceyaml.WithValidator] runs it before a decode.
// A document no resolver applies to fails with [ErrNoMatch], which is the
// answer a validation command wants. A decode that should check the
// documents it recognizes and accept the rest builds the registry with
// [WithRequireSchema] set false:
//
//	reg := schema.NewRegistry(
//	    schema.WithResolvers(schema.Directive(), schemastore.New()),
//	    schema.WithRequireSchema(false),
//	)
//
//	config, err := doc.Decode[Config](ctx, niceyaml.WithValidator(reg))
//
// # Loaders
//
// A [*Schema], [Embedded], [File], [URL], and [FileOrURL] are resolvers that
// name the same schema for every document and never report [ErrNoMatch],
// so a registry holding one alone validates everything against it. A
// Schema compiled already, such as the one [MustCompile] built at package
// scope, returns a [Compiled] ref naming itself:
//
//	reg := schema.NewRegistry(schema.WithResolvers(Config))
//
// The loaders return a [Loadable] ref whose key identifies the schema and
// whose load reads the bytes. The registry checks its cache by key first,
// so a file is read or a URL fetched once per registry, however many
// documents name it.
//
// Embed a schema in the binary with go:embed:
//
//	//go:embed schema.json
//	var schemaBytes []byte
//
//	reg := schema.NewRegistry(schema.WithResolvers(schema.Embedded(schemaBytes)))
//
// Read a schema from disk or over HTTP:
//
//	schema.File("./schemas/config.json")
//	schema.URL("https://example.com/schema.json")
//
// [FileOrURL] routes a reference as written in a directive or on a command
// line, which may be a file path or a URL.
//
// # Resolver Order
//
// The registry tries the resolvers of [WithResolvers] in the order given;
// the first that does not report [ErrNoMatch] wins. [When] guards any
// resolver with a [go.jacobcolvin.com/niceyaml/schema/matcher.Matcher], so
// the schema applies only to documents the matcher accepts, and
// [Directive] reads the schema a document names for itself in a
// yaml-language-server comment. A common order puts explicit user intent
// first, then content-based matching, then file path conventions:
//
//	reg := schema.NewRegistry(schema.WithResolvers(
//	    schema.Directive(),                                          // Explicit user intent.
//	    schema.When(matcher.Content(...), schema.Embedded(...)),     // By content.
//	    schema.When(matcher.MustFilePath(...), schema.File(...)),    // By path.
//	))
//
// Implement [Resolver], or wrap a function in [ResolverFunc], for
// resolution that decides and names the schema from the same inspection of
// the document.
//
// A resolver error that does not wrap [ErrNoMatch] ends the lookup, and the
// resolvers after it do not run. While no catalog has loaded, a
// [go.jacobcolvin.com/niceyaml/schema/schemastore.SchemaStore] that cannot
// reach SchemaStore.org ends the lookup this way, so place it after any
// resolver that should still apply without the catalog.
//
// # Schema Caching
//
// A resolver returns a [Ref] that names the schema by key and loads its
// bytes on demand. The registry checks its cache of compiled schemas by
// key before loading, so each schema is loaded and compiled once per
// registry however many documents name it. A [Compiled] ref skips the
// cache, since there is nothing to load.
//
// # SchemaStore Integration
//
// For automatic schema discovery based on file paths, use the
// [go.jacobcolvin.com/niceyaml/schema/schemastore] package, whose
// SchemaStore type is a resolver:
//
//	reg := schema.NewRegistry(schema.WithResolvers(schemastore.New()))
package schema
