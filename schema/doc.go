// Package schema provides JSON Schema directive parsing and validation for
// YAML documents.
//
// # Schema Directives
//
// Schema directives let YAML files declare their own schema. The directive
// format follows the yaml-language-server convention, so editors like VS Code
// read it:
//
//	# yaml-language-server: $schema=./config.schema.json
//	name: example
//
// The IntelliJ short form "# $schema: ./config.schema.json" names a schema
// too. The directive "# yaml-language-server: $schema=none" turns validation
// off for its document, as it does in yaml-language-server.
//
// Use [ParseDirective] to extract the schema path from a single comment, and
// [ParseDocumentDirective] to find the directive in one document's tokens.
//
// # Generation
//
// Generate JSON schemas from Go types with
// [go.jacobcolvin.com/x/jsonschema] directly, or at build time with its
// `cmd/gen` go:generate tool. A type customizes its generated schema by
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
// # Validation
//
// [Compile] turns a JSON schema document into a [*Schema], a
// [go.jacobcolvin.com/niceyaml.Validator] whose errors carry the YAML path
// to each failing location. [MustCompile] does the same at package scope
// for an embedded schema:
//
//	//go:embed config.schema.json
//	var schemaBytes []byte
//
//	var Config = schema.MustCompile(schemaBytes)
//
//	if err := doc.Validate(ctx, Config); err != nil {
//	    // err is a *niceyaml.SourceError; niceyaml.FormatError prints the failing lines.
//	}
//
// One violation is the error itself. Several come back as one error that
// counts them, and its message lists each behind its own position, so a
// program that prints the error alone still names every violation:
//
//	config.yaml: 2 schema violations
//	config.yaml:2:7: $.port: 0 is less than 1
//	config.yaml:3:1: $.extra~: value is not allowed
//
// A member the schema requires and the document leaves out reports the
// path it would have, such as $.server.name, and the error binds at the
// key of the mapping that lacks it.
//
// [Schema.Validate] returns the same bound error, so a validator of the
// program's own runs the schema on a node it picks and returns the
// result.
//
// [Schema.ValidateValue] checks decoded data that came from no document,
// such as the body of a request. The text of its error names the path of
// each violation from the value, so a program prints or returns the
// error as it is:
//
//	2 schema violations
//	$.port: 0 is less than 1
//	$.name: missing required property "name"
//
// That error stands in no document, so a caller that knows where the
// data stands in one places it there.
// [go.jacobcolvin.com/niceyaml.Rebase] or the
// [go.jacobcolvin.com/niceyaml.Node] of that value puts the errors under
// its path, and the bound error then names the file and the position of
// each violation.
//
// To validate and decode in one step, pass the schema to
// [go.jacobcolvin.com/niceyaml.Node.Decode] with
// [go.jacobcolvin.com/niceyaml.WithValidator]. A schema compiled by
// [go.jacobcolvin.com/x/jsonschema] itself, such as one built from a Go
// type, goes through [FromJSONSchema].
//
// The error of each violation wraps a [*Violation], which names the JSON
// Schema keyword the value fails and where that keyword stands in the
// schema. A program that suppresses a rule, rewords a message, or writes
// a report of its own reads the Violation and leaves the text of the
// message alone.
//
// Settings of the JSON Schema library pass through [WithJSONSchemaOptions].
// Its JSONSchema prefix shows the dependency at the call site, as the YAML
// prefix does on the options of the root package that pass go-yaml values
// through.
//
// [Compile] reads no file and fetches no URL, so a $ref to another
// document resolves only through a ref resolver given with
// [WithJSONSchemaOptions]. Without one, the schema does not compile. A
// schema whose $refs name files or URLs beside it loads through a
// [Registry], which resolves each $ref against the location of the
// schema. [FileFS] names a schema in a file system, and the registry
// reads it and the files its $refs name from there, such as from an
// [embed.FS]:
//
//	//go:embed schemas
//	var schemasFS embed.FS
//
//	reg := schema.NewRegistry()
//	root, err := reg.Schema(ctx, schema.FileFS(schemasFS, "schemas/root.json"))
//
// A schema loads only when each document its $refs name loads too, so a
// $ref to a file that is missing fails the schema once, with an error
// wrapping [ErrLoad], rather than each document that reaches the $ref.
// [WithRequireRefs] turns the check off for schemas the program does not
// maintain, such as the ones a catalog names. Such a schema then
// compiles, and validation fails with an error wrapping [ErrValidate]
// for each document that reaches the $ref.
//
// # Resolution
//
// When a document's schema is unknown ahead of time, a [Resolver] finds
// it. Resolve inspects the document and returns a [Ref] naming the
// schema, or reports [ErrNoMatch] when the resolver does not apply. A
// [Registry] tries its resolvers in order and validates the document
// against the first schema named. A Ref names either bytes under a key or
// a [*Schema] compiled already. The registry loads the bytes from a file,
// a URL, or the function given to [Loadable], and caches the compiled
// schema by the key. A Ref is a Resolver itself, so the loaders below go
// in directly or behind a [When] guard:
//
//	kindPath := paths.Doc().Child("kind")
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
//	    if err := doc.Validate(ctx, reg); err != nil {
//	        return err
//	    }
//	}
//
// A Registry implements [go.jacobcolvin.com/niceyaml.Validator],
// so [go.jacobcolvin.com/niceyaml.WithValidator] runs it before a decode.
// A document no resolver applies to fails with [ErrNoMatch], which is the
// answer a validation command wants.
// [go.jacobcolvin.com/niceyaml.IsInvalid] reports that error, so the
// command counts it as a fault of the document. A decode that should
// check the documents it recognizes and accept the rest builds the
// registry with [WithRequireSchema] set false:
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
// [Embedded], [File], and [URL] return a [Ref] that names one schema, and
// [FileOrURL] returns one for a reference that may be either, as written
// in a directive or on a command line. A Ref is a resolver that names
// its schema for every document and never reports [ErrNoMatch], so a
// registry holding one alone validates everything against it:
//
//	reg := schema.NewRegistry(schema.WithResolvers(schema.Embedded(schemaBytes)))
//
// A [*Schema] is a resolver of the same kind. One compiled at package
// scope with [MustCompile], or built from a Go type with
// [FromJSONSchema], goes into a registry as it is. The registry then
// validates with it without loading or compiling anything:
//
//	var Config = schema.MustCompile(configJSON)
//
//	reg := schema.NewRegistry(schema.WithResolvers(
//	    schema.Directive(),
//	    schema.When(matcher.Content(kindPath, "Config"), Config),
//	))
//
// [WithCompileOptions] reaches the schemas the registry compiles from
// bytes. A Schema compiled elsewhere keeps the options of its own compile.
//
// The loaders return a [Ref] whose key identifies the schema and whose
// bytes the registry loads through [Registry.Load]. [Embedded] holds a
// copy of the bytes. The registry reads a file from the file system its
// Ref names, or from disk, where [WithFSAt] confines the read, and it
// fetches a URL with the client [WithHTTPClient] gave it.
// [Registry.Schema] checks its cache by
// key before it loads and compiles those bytes, so once a schema
// compiles, the registry serves it to every later document that names it
// without loading it again. A failed load or compile stays out of the
// cache, so the next document that names the key loads it again. The
// registry does keep the failure of a schema whose $ref names a document
// that does not load, as [Registry.Schema] describes. A
// caller that holds a Ref of its own takes the compiled schema from the
// same cache. A resolver that picks the schema from the document returns
// the same Refs:
//
//	schema.ResolverFunc(func(ctx context.Context, doc *niceyaml.Node) (schema.Ref, error) {
//	    var kind string
//
//	    found, err := doc.DecodeIfPresent(ctx, kindPath, &kind)
//	    if err != nil {
//	        return schema.Ref{}, err
//	    }
//
//	    if !found {
//	        return schema.Ref{}, schema.ErrNoMatch
//	    }
//
//	    // The document picks kind, so keep it to a file name in schemas/.
//	    if strings.ContainsAny(kind, `/\`) {
//	        return schema.Ref{}, fmt.Errorf("kind %q: not a schema name", kind)
//	    }
//
//	    return schema.File("schemas/" + kind + ".json"), nil
//	})
//
// Embed a schema in the binary with go:embed:
//
//	//go:embed schema.json
//	var schemaBytes []byte
//
//	reg := schema.NewRegistry(schema.WithResolvers(schema.Embedded(schemaBytes)))
//
// Read a schema from disk or over HTTP. [File] and [URL] take a reference
// written in the program and panic on an empty one, as [Loadable] panics
// on an empty key:
//
//	schema.File("./schemas/config.json")
//	schema.URL("https://example.com/schema.json")
//
// A schema file lives on disk or in a file system, and each [Ref] says
// which. [File] names a file on disk, and [FileFS] names one in a file
// system, such as an [embed.FS]. A reference written beside a document
// lives where the document does, so [Directive] and [RefBeside] read it
// from the file system of a document that
// [go.jacobcolvin.com/niceyaml.NewSourceFromFS] opened and from disk for
// one that [go.jacobcolvin.com/niceyaml.NewSourceFromFile] opened. One
// registry thus serves bundled documents and documents on disk.
//
// [WithFSAt] confines the files a registry reads from disk. It reads
// them through a file system that stands for one directory, and a path
// outside that directory fails to load, so the option suits a program
// that validates documents from another trust domain:
//
//	root, err := os.OpenRoot(dir)
//	if err != nil {
//	    return err
//	}
//	defer root.Close()
//
//	reg := schema.NewRegistry(
//	    schema.WithFSAt(dir, root.FS()),
//	    schema.WithResolvers(schema.Directive()),
//	)
//
// [FileOrURL] routes a reference as written in a directive or on a command
// line, which may be a file path or a URL, and returns an error for one
// that names nothing. The registry fetches every URL with one client, which
// [WithHTTPClient] sets, so that client's timeout or proxy applies to every
// resolver that names a URL:
//
//	reg := schema.NewRegistry(
//	    schema.WithHTTPClient(&http.Client{Timeout: 10 * time.Second}),
//	    schema.WithResolvers(schema.Directive(), schemastore.New()),
//	)
//
// # Resolver Order
//
// The registry tries the resolvers of [WithResolvers] in the order given,
// and the first that does not report [ErrNoMatch] wins. [When] guards any
// resolver with a [go.jacobcolvin.com/niceyaml/schema/matcher.Matcher], so
// the schema applies only to documents the matcher accepts. [Directive]
// reads the schema a document names for itself in a yaml-language-server
// comment. A common order puts explicit user intent first, then
// content-based matching, then file path conventions:
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
// [go.jacobcolvin.com/niceyaml/schema/schemastore.Store] that cannot
// reach SchemaStore.org ends the lookup this way, so place it after any
// resolver that should still apply without the catalog.
//
// # SchemaStore Integration
//
// For automatic schema discovery based on file paths, use the
// [go.jacobcolvin.com/niceyaml/schema/schemastore] package, whose
// Store type is a resolver:
//
//	reg := schema.NewRegistry(schema.WithResolvers(schemastore.New()))
package schema
