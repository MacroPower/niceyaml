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
// [Compile] turns a JSON schema document, in JSON or YAML, into a
// [*Schema], a [go.jacobcolvin.com/niceyaml.Validator] whose errors carry
// the YAML path to each failing location. [MustCompile] does the same at
// package scope for an embedded schema:
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
// A violation carries the path of the value that breaks the schema. A
// violation of additionalProperties or of propertyNames is about the key
// of a member, so its path ends in `~`, as
// [go.jacobcolvin.com/niceyaml/paths.Path.Key] writes one. A violation
// about a mapping or a sequence as a whole, such as one of maxItems,
// carries the path of that value and binds at the key that holds it, as
// [go.jacobcolvin.com/niceyaml.AtPath] describes:
//
//	config.yaml:4:1: $.tags: array has 3 items, maximum is 2
//
// A member the schema requires and the document leaves out reports the
// path it would have, such as $.server.name, and the error binds at the
// key of the mapping that lacks it.
//
// [Schema.Check] is the raw step. It returns its violations as it wrote
// them, with no source and no position, so a validator of the program's
// own runs the schema on a node it picks through
// [go.jacobcolvin.com/niceyaml.Node.Validate] and returns that error.
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
// # Deprecations
//
// A schema marks a field or a value deprecated with the deprecated
// keyword. [Schema.Check] and [Schema.ValidateValue] report each value
// that such a schema applies to as a warning that matches
// [ErrDeprecated], and the check still passes. The keyword on the
// subschema that properties, patternProperties, additionalProperties, or
// unevaluatedProperties gives a member marks the key of that member. So
// does the keyword under propertyNames. The warning binds at the key, and
// its path ends in `~`:
//
//	config.yaml:17:3: warning: $.legacyPricing~: deprecated field "legacyPricing"
//	`-- legacyPricing has no effect. Remove it.
//
// The keyword anywhere else marks the value, the schema that a $ref
// reaches included, and the warning binds at the value. The headline
// quotes the text of a scalar, as in `deprecated value "wep"`, and reads
// `deprecated value` alone for a mapping or a sequence. The detail comes
// from the deprecationMessage keyword of the schema, which the JSON
// language service of VS Code also reads. The generator of
// [go.jacobcolvin.com/x/jsonschema] writes that keyword from the
// Deprecated: paragraph of a doc comment. A schema without it warns with
// the headline alone.
//
// A choice in the schema decides whether a deprecation applies. A
// deprecation under a branch of anyOf or oneOf warns only when the value
// passes that branch. One under if warns only when the value passes if,
// one under contains only for the items that match, and one under not
// never. [go.jacobcolvin.com/x/jsonschema.Validator.ValidateDeprecations]
// lists the rules. Draft-07 ignores each sibling of a $ref, so under that
// draft a deprecated keyword beside a $ref warns nothing.
//
// One check gives one warning for each node of the text. The
// deprecations that reach a node through an alias, a merge key, or two
// schemas share its warning. The warning lists each distinct message
// once, in the order that the check evaluated them, and names the path
// that comes first in the document. A check hands its warnings to the
// [go.jacobcolvin.com/niceyaml.Warnings] collector on its context,
// whether the check passes or fails, as the Warnings section of
// [go.jacobcolvin.com/niceyaml] describes.
//
// # YAML Schemas
//
// A schema document holds JSON or YAML. [Compile] and a [Registry] read
// data whose first byte after white space is '{' as JSON and any other
// data as YAML, whatever the name of its file or URL. The rule covers the
// schema a [Ref] names and each document a $ref names, so a directive
// names a YAML schema as it does in yaml-language-server:
//
//	# yaml-language-server: $schema=./config.schema.yaml
//	name: example
//
// That byte alone picks the reader. JSON that does not parse fails as
// JSON, and nothing reads it again as YAML, where a trailing comma would
// pass and a member with no value would be null.
//
// A YAML schema is one document. A duplicate key, a second document, and
// aliases past the limit [go.jacobcolvin.com/niceyaml.WithAliasLimit]
// describes each fail the compile. A YAML schema that does not parse
// fails it too. The error names the position in the schema:
//
//	compile schema: "file:///etc/app/config.schema.yaml": YAML decode: 3:9: ...
//
// The schema is at fault for that error, so it wraps no error of the
// root package. [go.jacobcolvin.com/niceyaml.IsInvalid] does not report
// it for the document the schema would validate.
//
// The go-yaml decoder takes several hundred times the size of the text
// in memory. A registry therefore refuses a schema file, URL, or YAML
// text of more than the limit [WithLoadLimit] sets. [Compile] refuses
// YAML of more than [DefaultLoadLimit]. The error of a schema past the
// limit matches [go.jacobcolvin.com/niceyaml.ErrLimit], and IsInvalid
// does not report that error either. Compile takes JSON text of any
// size, and so does a registry for the bytes of [Embedded] or
// [Loadable].
//
// A YAML schema holds the values a decode into any yields, and
// [Schema.Check] reads the numbers of a document the same way. The
// go-yaml decoder reads three spellings of a number in its own way:
//
//   - A number with an exponent and no decimal point, such as 1e3, is
//     the text "1e3". The number is 1.0e3.
//   - An integer with a leading zero, such as 010, is octal, so it is 8.
//   - An integer that does not fit in 64 bits, such as
//     18446744073709551616, is text.
//
// Text under a keyword that takes a number, such as maximum, fails the
// compile. Under const or enum, the schema compiles and accepts only
// that text. The YAML 1.2 core schema reads 1e3 as 1000 and 010 as 10,
// so an editor that follows it reads such a schema another way. The
// numbers .inf and .nan fail the compile, since JSON has neither.
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
// [Directive] lets each document pick its own schema. A decode of
// documents from another trust domain confines the registry first, as
// Untrusted Documents describes.
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
// without loading it again. The registry remembers a failed load or
// compile for the interval [WithRetryAfter] sets, one minute by default.
// The documents that name a broken schema inside the interval cost one
// attempt between them, and the first document after the interval loads
// the schema again. [WithCacheLimit] bounds the number of schemas the
// cache keeps.
//
// The registry serves a schema from its cache after the file or URL it
// came from changes. A program that watches its schema files, such as an
// editor, passes each file that changes to [Registry.Invalidate]. The
// registry then drops the schema of that file and each schema whose
// $refs reached the file. The next document that names one loads it
// again. [Registry.Purge] drops every schema the registry keeps.
//
// A caller that holds a Ref of its own takes the compiled schema from the
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
// outside that directory fails to load. Untrusted Documents pairs it
// with the other options that confine a registry for documents from
// another trust domain.
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
//
// # Untrusted Documents
//
// [Directive] lets the author of a document pick the files the registry
// reads and the URLs it fetches, and the schema a directive names can
// name more through its $refs. A program that validates documents from
// another trust domain, such as uploads or pull requests, confines its
// registry with [WithFSAt], [WithURLPrefixes], [WithCacheLimit], and
// [WithLoadLimit]. Each acts on every load the registry performs: the
// schema a [Ref] names, each $ref, and each redirect. One registry with
// these options serves the program for its whole lifetime:
//
//	root, err := os.OpenRoot(tenantDir)
//	if err != nil {
//	    return err
//	}
//	defer root.Close()
//
//	reg := schema.NewRegistry(
//	    schema.WithFSAt(tenantDir, root.FS()),
//	    schema.WithURLPrefixes("https://schemas.example.com/"),
//	    schema.WithCacheLimit(1024),
//	    schema.WithLoadLimit(1<<20),
//	    schema.WithResolvers(schema.Directive()),
//	)
//
//	source, err := niceyaml.NewSourceFromFile(filepath.Join(tenantDir, "app.yaml"))
//
// [WithFSAt] confines the reads from disk to tenantDir. A directive that
// names ./schema.json reads schema.json in tenantDir. One that names
// ../other/schema.json, or an absolute path outside tenantDir, fails to
// load, and so does a $ref that leads there. The file system of an
// [os.Root] refuses a symbolic link that leads out of the tree, but the
// one [os.DirFS] returns follows it. The Root must stay open while the
// registry is in use.
//
// WithFSAt confines every document on disk.
// [go.jacobcolvin.com/niceyaml.NewSourceFromFile] gives each document
// an absolute path. A program that builds a document from bytes passes
// an absolute path to [go.jacobcolvin.com/niceyaml.WithFilePath]. A
// relative reference beside a document whose path is relative and that
// has no file system fails with [ErrNoBaseDir]. A document in the file
// system [go.jacobcolvin.com/niceyaml.WithFS] gives it reads only that
// file system.
//
// [WithURLPrefixes] confines the fetches to the URLs under its prefixes,
// and with no prefixes the registry fetches nothing. The prefixes cover
// the schemas of every resolver, so a registry that also holds a
// [go.jacobcolvin.com/niceyaml/schema/schemastore.Store] must list the
// hosts of the schemas its catalog names. A prefix names a host, not an
// address. A host whose DNS answer points at an internal address still
// receives the request, as in DNS rebinding. A program that must keep the
// fetches off its own network checks each address in the Dialer of the
// client it gives [WithHTTPClient].
//
// [WithCacheLimit] bounds the number of schemas the registry keeps.
// Without it, documents that name endless distinct schemas, such as one
// URL with a new query string each time, grow the cache until the
// program calls [Registry.Purge].
//
// The registry holds the file system of a document from
// [go.jacobcolvin.com/niceyaml.WithFS] for as long as it keeps an entry
// from it, as [FileFS] describes. The limit thus bounds those file
// systems too. When a program opens each upload in a file system of its
// own, such as an [*archive/zip.Reader], the registry therefore holds a
// bounded number of uploads. Without a limit, the registry holds every
// upload whose directive named a schema beside it, until the program
// passes the upload to [Registry.Forget]:
//
//	defer reg.Forget(upload)
//
// [WithLoadLimit] bounds the bytes of each schema document the registry
// reads, [DefaultLoadLimit] by default. A directive that names a YAML
// schema of 10 MiB can cost gigabytes to compile, as the YAML Schemas
// section describes. The example in this section lowers the limit to
// 1 MiB. The registry keeps each schema it compiles, so the load limit
// and the cache limit together bound the memory of the cache.
//
// The options do not keep the author from turning validation off. A
// directive of "$schema=none" passes its document, and so does a
// directive that names a schema that accepts the document. A program
// that must check every document against a schema of its own leaves
// Directive out of the resolvers.
//
// The text of a load error names the file or URL the registry tried,
// such as a path under tenantDir. A server that returns the formatted
// error to the author of the document reveals that path.
// [go.jacobcolvin.com/niceyaml.IsInvalid] reports false for a load
// error, so such a server can tell it apart from a fault of the document
// and return a message of its own.
//
// The error holds a [*LoadError], which [errors.As] finds, with the
// [Ref] of the schema. [Ref.Name] names that Ref without the password of
// a URL. Each file or URL that did not read is a [*FetchError] inside
// the LoadError, with the status of the response for a URL. The server
// builds its message from those fields, such as one that names the URL
// but not the path, or one that tells a missing schema from a host that
// failed.
package schema
