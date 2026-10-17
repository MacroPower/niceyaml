package schema

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"
	"go.jacobcolvin.com/x/jsonschema"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/aliasing"
	"go.jacobcolvin.com/niceyaml/internal/aliaslimit"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/datapath"
	"go.jacobcolvin.com/niceyaml/internal/hook"
	"go.jacobcolvin.com/niceyaml/internal/httpfetch"
	"go.jacobcolvin.com/niceyaml/internal/schemahook"
	"go.jacobcolvin.com/niceyaml/paths"
)

var (
	// ErrValidate indicates an unexpected, non-validation failure while
	// validating against a schema, such as a reference resolution problem.
	// A [Schema] returns each schema constraint violation as a
	// [*niceyaml.Error] with path information. Such an error does not wrap
	// this sentinel. [niceyaml.IsInvalid] reports a violation. An error that
	// wraps ErrValidate means that the validation could not run. IsInvalid
	// does not report such an error, even when a scoped
	// [niceyaml.Node.Bind] places it at the value it checked. When a
	// Validate method or a [niceyaml.WithValueCheck] function returns an
	// error that does not declare anything, a decode presumes the document
	// at fault. Either one thus returns an error that wraps ErrValidate
	// inside [niceyaml.Unchecked].
	ErrValidate = errors.New("validate schema")

	// ErrExcessiveAliasing indicates a value that shares maps, slices, or
	// byte slices so heavily that validating it reads far more data than
	// the value holds. Aliases in a YAML document make a decode share them
	// that way. ErrExcessiveAliasing also indicates a document whose
	// aliases make the decoder itself read that much. [Schema.Check] and
	// [Schema.ValidateValue] return it. A [matcher.Content] or
	// [matcher.Text] guard refuses such a document with it too, and
	// [Registry.Lookup] then returns it wrapped together with [ErrResolve].
	// The document or the value is at fault in every case, so
	// [niceyaml.IsInvalid] reports each of these errors, and they do not
	// wrap [ErrValidate].
	//
	// [niceyaml.WithAliasLimit] on the source of a document turns the
	// limit off for Check and for the guard. ValidateValue does not take a
	// source, so it applies the limit to every value. ErrExcessiveAliasing
	// is the same error value as [niceyaml.ErrExcessiveAliasing].
	ErrExcessiveAliasing = aliaslimit.ErrExcessiveAliasing

	// ErrCompile indicates a schema document that does not compile.
	// [Compile] returns it, and a [Registry] returns it as the Phase of a
	// [*LoadError]. Without a ref resolver, a schema with a $ref to another
	// document fails to compile, as [WithRequireRefs] describes.
	ErrCompile = errors.New("compile schema")

	// ErrDeprecated indicates the warning about a value that a schema with
	// "deprecated": true applies to. [Schema.Check] and
	// [Schema.ValidateValue] report each such value as a warning that
	// matches it.
	ErrDeprecated = errors.New("deprecated")
)

// CompileOption configures [Compile] and [MustCompile], and
// [WithCompileOptions] hands the same options to a [Registry].
//
// Available options:
//   - [WithJSONSchemaOptions]
//   - [WithRequireRefs]
type CompileOption func(*compileConfig)

// compileConfig holds the settings a [CompileOption] configures.
type compileConfig struct {
	jsonOpts []jsonschema.ValidateOption
	// Whether a compile fails when it leaves a $ref to another document
	// unresolved.
	requireRefs bool
}

// newCompileConfig applies opts over the defaults.
func newCompileConfig(opts []CompileOption) compileConfig {
	cfg := compileConfig{requireRefs: true}

	for _, opt := range opts {
		opt(&cfg)
	}

	return cfg
}

// WithJSONSchemaOptions is a [CompileOption] that passes
// [jsonschema.ValidateOption] values to the underlying
// [jsonschema.Compile]. Use it for settings of the JSON Schema library
// that this package does not offer as options, such as format assertions
// or a custom format validator:
//
//	v, err := schema.Compile(ctx, data, schema.WithJSONSchemaOptions(jsonschema.WithFormats(true)))
//
// A [Registry] resolves the $refs of a schema from [File] or [URL] with
// a resolver of its own. [WithCompileOptions] says when a
// [jsonschema.WithRefResolver] given here replaces that resolver. A
// compile does not check which documents such a resolver serves, as
// [WithRequireRefs] describes.
//
// The option keeps its own copy of opts. A later write to the caller's
// slice does not change the option, even for a [Registry] that compiles
// later.
func WithJSONSchemaOptions(opts ...jsonschema.ValidateOption) CompileOption {
	opts = slices.Clone(opts)

	return func(cfg *compileConfig) {
		cfg.jsonOpts = append(cfg.jsonOpts, opts...)
	}
}

// WithRequireRefs is a [CompileOption] that sets whether a schema
// compiles only when each $ref to another document resolves. The default
// is true. A compile resolves every $ref in the schema, whether or not a
// document can reach it. A schema with a $ref that does not resolve
// therefore fails once, when it compiles. [Compile] does not have a ref
// resolver of its own. It returns an error wrapping [ErrCompile] for such
// a $ref unless [WithJSONSchemaOptions] gives it a resolver. A
// [Registry] returns an error wrapping [ErrLoad] for a document it cannot
// load, as [Registry.Schema] describes.
//
// With false, such a schema compiles. Each document whose validation
// reaches the $ref then fails with an error wrapping [ErrValidate], and a
// document that never reaches it passes. A registry of schemas the
// program does not maintain takes false, because a public schema might
// name a missing document under a rare branch:
//
//	reg := schema.NewRegistry(
//	    schema.WithResolvers(schema.Directive(), schemastore.New()),
//	    schema.WithCompileOptions(schema.WithRequireRefs(false)),
//	)
//
// A ref resolver given with [WithJSONSchemaOptions] answers the JSON
// Schema library itself, so the compile cannot tell which documents that
// resolver serves. A schema with a $ref to a document that the resolver
// does not serve compiles under either setting. Each document whose
// validation reaches that $ref fails with an error wrapping
// [ErrValidate].
func WithRequireRefs(require bool) CompileOption {
	return func(cfg *compileConfig) {
		cfg.requireRefs = require
	}
}

// Compile creates a new [*Schema] from a JSON schema document. The
// context reaches the reference resolver for references it resolves
// while compiling. Each error Compile returns wraps [ErrCompile].
//
// Compile suits a program that holds one schema, such as one embedded in
// the binary:
//
//	//go:embed config.schema.json
//	var schemaJSON []byte
//
//	v, err := schema.Compile(ctx, schemaJSON)
//
// The document holds JSON or YAML. Compile reads data whose first byte
// after white space is '{' as JSON, and any other data as YAML:
//
//	//go:embed config.schema.yaml
//	var schemaYAML []byte
//
//	v, err := schema.Compile(ctx, schemaYAML)
//
// That byte alone picks the reader. JSON that does not parse fails as
// JSON, and Compile does not read it again as YAML. A YAML schema is one
// document, and Compile refuses a duplicate key, a second document, and
// aliases past the limit [niceyaml.WithAliasLimit] describes. Its error
// for YAML that does not parse names the position in data:
//
//	compile schema: YAML decode: 3:9: ...
//
// The data is at fault for that error, so it does not wrap an error of
// the root package, and [niceyaml.IsInvalid] does not report it. A YAML
// schema holds the values a decode into any yields. The package
// documentation says how that decode reads numbers such as 1e3 and 010.
//
// The go-yaml decoder takes several hundred times the size of the text
// in memory, so Compile refuses YAML of more than [DefaultLoadLimit]
// bytes. Its error matches [niceyaml.ErrLimit], and IsInvalid does not
// report it either. Compile reads JSON of any size. A [Registry] refuses
// YAML from [Embedded] of more than the limit [WithLoadLimit] sets, and
// it reads JSON from Embedded of any size.
//
// A schema known valid at build time compiles with [MustCompile] at package
// scope. A registry compiles the schemas its resolvers name as bytes the
// same way, with the options [WithCompileOptions] gives it. It takes a
// compiled Schema as it is.
//
// Compile does not read files or fetch URLs. A $ref to another document,
// such as "server.json" or an https URL, resolves only through a ref
// resolver given with [WithJSONSchemaOptions]. Without one, the schema
// does not compile, and the error names the document:
//
//	compile schema: no ref resolver for $ref to "server.json": schema URI not resolved
//
// The schema fails wherever the $ref stands, even under a branch that
// documents never take. [WithRequireRefs] lets such a schema compile and
// leaves the $ref to fail the documents that reach it. A $ref to a
// document the schema holds under an $id resolves there and does not
// need a resolver. A $ref to a location the document lacks, such as
// "#/$defs/missing", fails the compile under either setting.
//
// A schema whose $refs name files or URLs beside it loads through a
// [Registry] instead. [Registry.Schema] loads the schema a [File] or [URL]
// names and resolves each $ref against that location:
//
//	v, err := schema.NewRegistry().Schema(ctx, schema.File("schemas/root.json"))
func Compile(ctx context.Context, data []byte, opts ...CompileOption) (*Schema, error) {
	compiled, err := compileDocument(ctx, data, DefaultLoadLimit, opts)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCompile, err)
	}

	return compiled, nil
}

// compileDocument is [Compile] without the [ErrCompile] wrap. It refuses
// YAML text of more than limit bytes.
func compileDocument(ctx context.Context, data []byte, limit int, opts []CompileOption) (*Schema, error) {
	doc, err := parseDocument(ctx, data, limit)
	if err != nil {
		return nil, err
	}

	cfg := newCompileConfig(opts)
	jsonOpts := cfg.jsonOpts
	rec := &refRecorder{}

	// The recording resolver goes first, so a ref resolver among the
	// options replaces it. It then answers only for a compile that has no
	// other resolver, where no $ref to another document can resolve.
	if cfg.requireRefs {
		jsonOpts = slices.Concat(
			[]jsonschema.ValidateOption{
				jsonschema.WithRefResolver(rec.resolver(jsonschema.RefResolverFunc(noRefResolver))),
			},
			jsonOpts,
		)
	}

	compiled, err := jsonschema.Compile(ctx, doc, jsonOpts...)
	if err != nil {
		//nolint:wrapcheck // Callers wrap the error with ErrCompile.
		return nil, err
	}

	err = rec.stop()
	if err != nil {
		return nil, err
	}

	return FromJSONSchema(compiled), nil
}

// parseDocument reads data as one schema document without compiling it.
// It reads data that [isJSONObject] reports as JSON, and any other data
// as YAML through [decodeYAML] within limit. One byte picks the reader, so
// parseDocument never hands data that one reader refused to the other.
// Some malformed JSON is also valid YAML, which reads a member without a
// value as null. The schema library reads a null keyword as absent. The
// JSON reader refuses such data instead of compiling it as YAML.
func parseDocument(ctx context.Context, data []byte, limit int) (*jsonschema.Schema, error) {
	if isJSONObject(data) {
		//nolint:wrapcheck // Callers wrap the error with ErrCompile.
		return jsonschema.ParseSchema(data)
	}

	doc, err := decodeYAML(ctx, data, limit)
	if err != nil {
		return nil, err
	}

	// Normalize spells each integer as the schema library reads a JSON
	// number, which keeps one past 2^53 exact.
	//nolint:wrapcheck // Callers wrap the error with ErrCompile.
	return jsonschema.ParseSchemaValue(jsonschema.Normalize(doc))
}

// isJSONObject reports whether the first byte of data after JSON white
// space is '{'. A schema document in JSON is an object or a boolean. YAML
// reads a boolean as JSON does, so only an object needs the JSON reader.
func isJSONObject(data []byte) bool {
	return bytes.HasPrefix(bytes.TrimLeft(data, " \t\r\n"), []byte("{"))
}

// decodeYAML decodes data, the text of a schema document in YAML, into
// the value a decode into any yields. The text must be one document of at
// most limit bytes. The decode refuses a duplicate key and aliases past
// the limit [niceyaml.WithAliasLimit] describes.
//
// The error of a text past limit wraps the error of [httpfetch.TooLarge],
// which matches [niceyaml.ErrLimit] and does not declare a fault. The
// schema is at fault for any other decode error, not the document that
// the schema validates. The error that decodeYAML returns for it holds
// the text of the decode error but does not wrap it. [niceyaml.IsInvalid]
// therefore does not report that error.
//
// The decode takes the context without its cancellation. Each decode
// error becomes text here, and the error of a context that has ended must
// not become text. A JSON read does not check the context either, so a
// context that ends stops the compile at the same step for JSON and for
// YAML.
func decodeYAML(ctx context.Context, data []byte, limit int) (any, error) {
	// The go-yaml decoder takes several hundred times the size of the text
	// in memory, so the decode refuses text past limit.
	if len(data) > limit {
		return nil, fmt.Errorf("YAML decode: %w", httpfetch.TooLarge("text", limit))
	}

	source := niceyaml.NewSourceFromBytes(data, niceyaml.WithByteLimit(limit))

	doc, err := source.Decode[any](context.WithoutCancel(ctx))
	if err != nil {
		return nil, errors.New("YAML decode: " + err.Error())
	}

	return doc, nil
}

// noRefResolver is the ref resolver of a compile whose options do not
// hold one. Like the schema library without a resolver, it does not serve
// any document, but its error says why the $ref does not resolve.
func noRefResolver(_ context.Context, uri string) (*jsonschema.Schema, error) {
	return nil, fmt.Errorf("no ref resolver for $ref to %q: %w", httpfetch.Redacted(uri), jsonschema.ErrNotResolved)
}

// refRecorder records the errors a ref resolver returns while a schema
// compiles. The schema library compiles a schema whose $ref names a
// document the resolver does not serve. It reports the $ref only to a
// validation that reaches it. The recorder lets the compile fail for
// such a document instead. The compiled schema keeps the resolver for its
// validations, which run concurrently. [refRecorder.stop] therefore ends
// the recording when the compile returns.
type refRecorder struct {
	errs    []error
	mu      sync.Mutex
	stopped bool
}

// resolver returns a resolver that answers as next does and records each
// error next returns until [refRecorder.stop] runs.
func (rec *refRecorder) resolver(next jsonschema.RefResolver) jsonschema.RefResolver {
	return jsonschema.RefResolverFunc(func(ctx context.Context, uri string) (*jsonschema.Schema, error) {
		s, err := next.ResolveRef(ctx, uri)
		if err != nil {
			rec.record(err)
		}

		//nolint:wrapcheck // The recorder answers as the resolver does.
		return s, err
	})
}

// record keeps err unless the recording has stopped.
func (rec *refRecorder) record(err error) {
	rec.mu.Lock()
	defer rec.mu.Unlock()

	if !rec.stopped {
		rec.errs = append(rec.errs, err)
	}
}

// stop ends the recording and returns the errors recorded as one error,
// or nil when the resolver did not return an error.
func (rec *refRecorder) stop() error {
	rec.mu.Lock()
	defer rec.mu.Unlock()

	rec.stopped = true

	errs := rec.errs
	rec.errs = nil

	if len(errs) == 1 {
		return errs[0]
	}

	return errors.Join(errs...)
}

// MustCompile is [Compile] with a background context, and it panics when
// the schema does not compile. Use it for a package-scope schema compiled
// from a document go:embed brings in:
//
//	var Config = schema.MustCompile(schemaJSON)
//
// A schema with a $ref to another document compiles only through a ref
// resolver, as [Compile] describes. Without one, MustCompile panics for
// such a schema when the package initializes.
func MustCompile(data []byte, opts ...CompileOption) *Schema {
	v, err := Compile(context.Background(), data, opts...)
	if err != nil {
		panic(err)
	}

	return v
}

// FromJSONSchema creates a new [*Schema] from a [*jsonschema.Validator]
// compiled elsewhere, such as one built from a Go type with
// [jsonschema.Compile]. A schema held as JSON or YAML text compiles with
// [Compile] or [MustCompile] instead.
//
// FromJSONSchema panics if v is nil.
func FromJSONSchema(v *jsonschema.Validator) *Schema {
	if v == nil {
		panic("schema.FromJSONSchema: validator is nil")
	}

	return &Schema{compiled: v}
}

// Schema is a compiled JSON schema. It is a [niceyaml.Validator] that
// checks a document against the schema. It reports constraint violations
// as [*niceyaml.Error] values that carry the YAML path to each failing
// location. The errors bind to the source, so
// [go.jacobcolvin.com/niceyaml/printer.Printer] can display them. Each
// one wraps a [*Violation] that names the keyword the value fails. For each
// value that a deprecated schema applies to, a Schema hands a warning
// that matches [ErrDeprecated] to the [niceyaml.Warnings] collector of
// the context.
//
// [Schema.Check] checks a node. The root [niceyaml.Node] of a document
// stands for the whole document, and a Node from [niceyaml.Node.At]
// stands for one value inside it. [Schema.ValidateValue] checks decoded
// data that did not come from a document, such as the body of a request.
// The text of its error names the path of each violation. A caller that
// knows where the data stands in a document places that error there with
// [niceyaml.Rebase] and [niceyaml.Node.Bind]. It places the warnings the
// same way, as the Placement section of ValidateValue describes.
//
// A Schema is the validator for a program that holds one schema and
// compiles it itself. It is also a [Resolver] that names itself for every
// document. A Schema therefore goes into a [Registry] as it is, on its own
// or behind a [When] guard. The registry then validates with it without
// loading or compiling anything:
//
//	var Config = schema.MustCompile(schemaJSON)
//
//	reg := schema.NewRegistry(schema.WithResolvers(
//	    schema.When(matcher.Content(kindPath, "Config"), Config),
//	    schema.When(matcher.Content(kindPath, "Pod"), schema.FromJSONSchema(podValidator)),
//	))
//
// A Schema is safe for concurrent use. Create instances with [Compile],
// [MustCompile], or [FromJSONSchema].
type Schema struct {
	compiled *jsonschema.Validator
	// Whether the schema passes every document and value without reading
	// it, as the schema that a "$schema=none" directive names does.
	acceptAll bool
}

// Ref returns the [Ref] that carries the compiled schema, which
// [Ref.Schema] returns and a [Registry] validates with as it is. A
// resolver that picks among compiled schemas returns one:
//
//	schema.ResolverFunc(func(ctx context.Context, doc *niceyaml.Node) (schema.Ref, error) {
//	    if strings.HasSuffix(doc.FilePath(), ".pod.yaml") {
//	        return Pod.Ref(), nil
//	    }
//
//	    return schema.Ref{}, schema.ErrNoMatch
//	})
//
// The [Ref.Key] of the Ref is empty, because the registry does not load
// or cache anything for it. [Registry.Schema] returns the schema as it
// is, and [Registry.Load] returns an error wrapping [ErrLoad].
func (s *Schema) Ref() Ref {
	return Ref{schema: s}
}

// Resolve implements [Resolver]. It names the schema for every document
// and never reports [ErrNoMatch].
func (s *Schema) Resolve(_ context.Context, _ *niceyaml.Node) (Ref, error) {
	return s.Ref(), nil
}

// Check implements [niceyaml.Validator]. It reads n as any through
// [niceyaml.Node.Decode] and checks the result against the schema as
// [Schema.ValidateValue] does. [niceyaml.WithValidator] runs the schema
// before a decode, and [niceyaml.Node.Validate] runs it on its own. A
// Node from [niceyaml.Node.At] decodes to the node it selects, so the
// schema checks that node and a violation's `@` path reads from it.
// Check returns each violation as it wrote it, and Node.Validate binds
// it through n, as [niceyaml.Validator] describes. For a document past
// the alias limit, Check returns an error matching
// [ErrExcessiveAliasing]. [Schema.ValidateValue] checks data for a caller
// that reports the errors somewhere else.
//
// # Deprecations
//
// Check reports each value that a schema with the deprecated keyword
// applies to as a warning, as the Deprecations section of the package
// documentation describes. It reports its warnings with
// [niceyaml.Node.Warn] whether n passes or fails, and it returns only its
// errors.
//
// # Unparsed Documents
//
// A document that did not parse does not have data to check. Check
// returns the syntax error that [niceyaml.Node.Err] returns for it,
// whatever the schema accepts.
//
// # Empty Documents
//
// An empty document, such as an empty file, decodes to null, so a schema
// that wants a mapping rejects it. [niceyaml.Source.Documents] omits the
// empty document that a trailing "---" leaves beside one with content.
// [niceyaml.SkipEmpty] wraps the schema so that it passes an empty
// document wherever it runs.
//
// # Key Spelling
//
// Check holds the node, so it spells each key in a violation's path as
// the source does. A key the decoder respells, such as the hexadecimal
// 0x10 for the member name 16, still names its member. Where a later key
// in the mapping or its merge sources has the same spelling, a path
// through that spelling selects the later key. The path then names the
// member and each key below it as the decoder does. Such a path does not
// select the member, so the violation carries the position of the value
// beside the path and binds there.
//
// A violation about a member that the mapping omits, such as one of
// required, carries the path of the missing member, as [Violation]
// describes. The path names the member as the schema does, because the
// source does not spell a key for it. The mapping can hold a key with
// that spelling for a member of another name, as the key 0x10 sets the
// member 16 under a schema that requires 0x10. The path then selects the
// entry of that key, so the violation carries the position of the
// mapping beside the path and binds there.
//
// To tell which key a spelling selects, Check reads the sources of each
// `<<` merge key that supplies the key or stands after it. For the
// violations of one call, Check can read as many nodes that way as
// [paths.ErrExcessiveMerging] allows one path selector. Past that limit,
// Check still reports every violation. A path through such a key names
// the member and each key below it as the decoder does.
//
// # Timestamps
//
// The node also shows which !!timestamp values the source wrote as a bare
// date. Check hands the schema each of those as an RFC 3339 full-date,
// so !!timestamp 2001-12-14 matches format "date" as the untagged
// 2001-12-14 does. Check finds that scalar behind an alias or a merge
// key as it finds the key it spells in a violation's path. Where Check
// cannot tell which scalar a timestamp came from, the timestamp keeps the
// date-time spelling that [Schema.ValidateValue] gives it. A timestamp
// behind an alias that does not resolve keeps that spelling. So does a
// timestamp under a mapping, at any depth, where a key at or after the
// member that leads to the timestamp has a member name that Check cannot
// tell. Such a key might set a member of the same name. A merge key whose
// sources do not resolve is one such key.
//
// # Alias Limit
//
// The decode applies the alias limit to the document of n before it reads
// anything. Check also applies the limit to the result, but only where
// the document holds an alias to a reference document.
// [niceyaml.WithAliasLimit] on the source of n turns both off.
//
// The decoder writes the whole content of an alias it spells as text,
// such as an alias used as a key. It also reads the sources of a merge
// key again at every merge. A small document can cost far more to decode
// than the decoded value shows. The decode that Check runs therefore
// counts before it reads. For a node that holds an alias, it counts the
// nodes that a decode of the whole document reads. In that count, each
// alias reads its content in full, as [niceyaml.Node.DecodeInto]
// describes. The count covers the whole document even for a node below
// the root, so every node in a document that holds an alias gets the same
// verdict. The decode applies the alias limit of [Schema.ValidateValue]
// to the count.
//
// An alias to a text tag, such as !!binary or !!str, counts one node per
// byte of the text under the tag. So does an alias that reaches a
// !!binary scalar through another tagged alias, as *s does for
// `&s !foo *b`. Each copy of a scalar that the decoder writes as text,
// such as in a key that holds a sequence, counts the same way. Any other
// alias to a scalar counts as one unaliased node. For a document past the
// limit, Check returns the error of that decode. That error matches
// [ErrExcessiveAliasing] and binds at the first token of the node that is
// not a comment:
//
//	app.yaml:1:1: excessive aliasing
//
// The aliases of the document are the cause, so [niceyaml.IsInvalid]
// reports the error, and it does not wrap [ErrValidate]. Check does not
// put a limit of its own on the result of the decode. A node below the
// root then passes the limit wherever its document does, even when
// aliases make up a larger share of the node than of the document.
//
// A document that holds an alias without an anchor of its name before it
// is the exception. A decode resolves such an alias against a reference
// document, such as one of [niceyaml.WithReferences]. The count cannot
// see a reference document, so it takes the alias as one node. For such
// a document, Check also applies the limit to the result of the decode,
// as [Schema.ValidateValue] does. A node below the root can then exceed
// the limit where its document passes.
//
// The error of the check on the result matches ErrExcessiveAliasing as
// well, and IsInvalid reports it. It does not carry a location, so the
// Node that runs the schema binds it as it binds any such error. A node
// below the root binds it at the node, under its path, and a root binds
// it without a position.
//
// [niceyaml.WithAliasLimit] on the source of n turns the limit off for
// the decode and for the result. Check then reads every use of every
// alias, at the cost WithAliasLimit describes.
func (s *Schema) Check(ctx context.Context, n *niceyaml.Node) error {
	err := n.Err()
	if err != nil {
		//nolint:wrapcheck // The source bound the syntax error already.
		return err
	}

	if s.acceptAll {
		return nil
	}

	// A decode into any yields only the YAML built-in types, none of which
	// validates itself, so the self-validation walk would find nothing.
	// The decode applies the alias limit to the document before it reads
	// anything, and it binds its own error.
	data, err := n.Decode[any](ctx, niceyaml.WithSelfValidation(false))
	if err != nil {
		return err
	}

	// The count of that decode takes each alias as the validator reads
	// the value a decode shares at it, which is a mapping or a sequence
	// in full and a !!binary scalar by its text. An expansion check of
	// data would repeat that count for the node alone, and could give a
	// node below the root a verdict apart from its document's. The count
	// cannot see a reference document, though, so data gets the check
	// where the document holds an alias to one and the limit applies.
	if aliasing.Limited(n) && aliasing.HoldsReferenceAlias(n) {
		err = checkExpansion(data)
		if err != nil {
			return err
		}
	}

	warnings, err := s.validate(ctx, data, n)

	for _, w := range warnings {
		n.Warn(ctx, w)
	}

	return err
}

// ValidateValue checks data, the decoded form of a YAML value, against the
// schema. The value holds the types a decode into any yields: maps with
// string keys, slices, strings, bools, nil, and numbers, nested as the
// document nests them. The validator reads the data as JSON does, so it
// does not accept a Go struct.
//
// ValidateValue returns nil when data conforms. For a value that breaks a
// constraint, it returns a [*niceyaml.SourceError] that is not bound to a
// document. The text of that error names the path of each violation, as
// in "$.port: 0 is less than 1". When the check cannot run, such as for a
// $ref the validator cannot resolve, ValidateValue returns an error
// wrapping [ErrValidate]. When a Validate method or a
// [niceyaml.WithValueCheck] function returns an error that does not
// declare anything, a decode presumes the document at fault. Either one
// that calls ValidateValue thus returns an error that wraps ErrValidate
// inside [niceyaml.Unchecked].
//
// # Conversions
//
// ValidateValue first converts the YAML-native values the JSON Schema
// validator does not accept into the JSON spelling of the same data,
// anywhere in the value. A !!binary becomes its base64 text. A
// !!timestamp becomes its RFC 3339 date-time text, even when the source
// wrote it as a bare date, because a [time.Time] does not record the
// spelling. [Schema.Check] reads the source and spells such a timestamp
// as a full-date where it can. The [yaml.MapSlice] that a decode with
// [niceyaml.WithYAMLOrderedMaps] yields for each mapping becomes a map
// with the same members. Where two items share a key, the later one wins,
// as it does in a decode into a map.
//
// # Violations
//
// A single violation carries its YAML path on the error itself. Several
// violations become a count summary from [niceyaml.NewSummary] that heads
// one error per violation, each with the path to one failing location.
// The error of each violation wraps a [*Violation] that names the keyword
// the value fails and where that keyword stands in the schema. A value
// that does not match any branch of an anyOf or oneOf counts as one
// violation. Its details are the failures of each branch that the author
// might have meant the value for, as [Violation] describes. Any other
// failure wraps [ErrValidate], because the value is not at fault for it.
// A $ref the validator cannot resolve is one such failure.
//
// # Deprecations
//
// ValidateValue reports each value that a schema with the deprecated
// keyword applies to as a warning, as the Deprecations section of the
// package documentation describes. It hands each warning to the
// collector that [niceyaml.Warnings.Context] put on ctx, whether data
// passes or fails. Each warning binds as [niceyaml.BindValue] binds an
// error. ValidateValue returns only its errors, and
// [niceyaml.Warnings.Report] puts the warnings beside them:
//
//	1 error, 1 warning
//	$.port: 0 is less than 1
//	warning: $.legacyPricing~: deprecated field "legacyPricing"
//
// # Error Text
//
// ValidateValue does not take a document, so it binds its errors without
// one, through [niceyaml.BindValue]. The binding puts each path in the
// text of the error. The error therefore names every failing location
// through %v, inside a wrapper from [fmt.Errorf], in a join from
// [errors.Join], and in a log. One violation reads
// "$.port: 0 is less than 1", and several read as the summary with one
// violation per line:
//
//	2 schema violations
//	$.port: 0 is less than 1
//	$.name: missing required property "name"
//
// Each path starts at `$`, which here names the value data stands for.
// No position stands in front of a path. [niceyaml.IsInvalid] reports
// the error of a violation, and [errors.As] finds the [*Violation] in it,
// or the first of several. [niceyaml.FormatError] prints the same errors
// as a tree.
//
// # Placement
//
// The result does not stand in a document, so a document can still place
// it, as BindValue describes. [niceyaml.Rebase] and every Bind return an
// error bound to a document as it is. They read this result as the errors
// that ValidateValue built it from. The paths of those errors start at
// `@`, the value data stands for. A caller that knows where the value
// stands in a document puts the errors under that path. A
// [niceyaml.Node] of the document then binds them:
//
//	err := s.ValidateValue(ctx, data)
//
//	return doc.Bind(niceyaml.Rebase(err, paths.Doc().Child("request")))
//
// The bound error reads "app.yaml:2:9: $.request.port: 0 is less than 1".
// The same holds for a result that a Validate method of a type or a
// [niceyaml.Validator] returns. A decode places such a result at the
// value it checked.
//
// A wrapper such as [fmt.Errorf] around the result keeps its text through
// both calls, behind the position and the path, as in
// "app.yaml:2:9: $.request.port: check: 0 is less than 1". BindValue
// describes a wrapper that rewrites the text of the result, and a result
// inside [niceyaml.Invalid], [niceyaml.Unchecked], or [niceyaml.Locate].
// A Bind places the violations inside each of them too. The result itself
// never changes.
//
// Each violation the result heads places the same way on its own, so a
// caller that drops some of them places the rest. The violations are
// the problems [niceyaml.Problems] yields for the result:
//
//	for violation := range niceyaml.Problems(bound) {
//		errs = append(errs, doc.Bind(niceyaml.Rebase(violation.Err, base)))
//	}
//
// The warnings place the same way. A caller outside a call of niceyaml
// collects them with a [niceyaml.Warnings] of its own and places what
// [niceyaml.Warnings.Err] returns. A function of
// [niceyaml.WithValueCheck] passes its context to ValidateValue, and the
// decode places each warning that ValidateValue reports at the value
// that the function checked. A [niceyaml.Validator] passes its context
// the same way, and the Node that runs the validator places each warning
// at that Node.
//
// # Key Names
//
// ValidateValue does not hold a source, so each path names a key as the
// decoder does, such as 16 for a key the document spells 0x10. A caller
// that reports the errors in the document the data came from takes each
// location from [niceyaml.Node.DataLocator].
//
// # Rejected Data
//
// ValidateValue rejects some data before it checks anything. For a map or
// slice that contains itself, it returns an error wrapping [ErrValidate].
// YAML aliases make a decode share maps, slices, and byte slices. For a
// value whose shared parts expand past the alias limit, ValidateValue
// returns an error matching [ErrExcessiveAliasing]. The value is at fault
// for that error, so [niceyaml.IsInvalid] reports it, and it does not
// wrap [ErrValidate].
//
// The limit follows the rule that gopkg.in/yaml.v3 applies to the share
// of aliased nodes in a document. A []byte counts as one node per
// character of its base64 text. The yaml.v3 decoder applies the rule to
// each node during its decode, but ValidateValue applies it once to the
// whole value. It counts each use of an aliased scalar other than a
// !!binary as an unaliased node, so it accepts some documents that
// yaml.v3 rejects. ValidateValue does not take a source, so it applies
// the limit to every value, even one decoded from a source that
// [niceyaml.WithAliasLimit] turned the limit off for.
//
// # Context
//
// The context reaches the underlying [jsonschema.Validator], and the
// resolution of remote references honors its cancellation and deadline.
func (s *Schema) ValidateValue(ctx context.Context, data any) error {
	warnings, err := s.checkValue(ctx, data)

	for _, w := range warnings {
		hook.Warn(ctx, niceyaml.BindValue(w))
	}

	//nolint:wrapcheck // Binding puts each path in the text, and the error keeps its own context.
	return niceyaml.BindValue(err)
}

// checkValue checks data against the schema. It returns the warnings of
// the value, and the error of a value that fails. Each is unbound, with a
// path that starts at `@`, the value data stands for. It does not hold a
// source, so each path names a key as the decoder does, such as 16 for a
// key the document spells 0x10.
func (s *Schema) checkValue(ctx context.Context, data any) ([]error, error) {
	if s.acceptAll {
		return nil, nil
	}

	// Both normalizeJSON and the validator read a shared value again at
	// every use, so the check runs before either of them.
	err := checkExpansion(data)
	if err != nil {
		return nil, err
	}

	return s.validate(ctx, data, nil)
}

// validate checks data against the schema as [Schema.checkValue] does
// after data passes its expansion check. It takes the node a decode read
// data from, which [Schema.Check] has and [Schema.ValidateValue] does
// not. A violation at a key the decoder respells, such as the hexadecimal
// 0x10, needs the node to spell the key in its path as the source does. A
// !!timestamp needs the node to show whether the source wrote only a
// date.
//
// It returns the warnings of data beside the error of a value that fails,
// each unbound.
func (s *Schema) validate(ctx context.Context, data any, n *niceyaml.Node) ([]error, error) {
	// The resolver binds aliases across the whole document, so an alias
	// inside a scoped node reaches an anchor outside it. The document
	// keeps one for every Node of it, so a check of each item of a list
	// binds the document once.
	resolver := paths.NewResolver(nil)
	if n != nil {
		resolver = n.Resolver()
	}

	// The timestamp lookups and every violation share one index, so each
	// key decodes once however many of them lie under its mapping.
	idx := datapath.NewIndex(resolver)
	c := converter{root: rootOf(n), idx: idx}
	instance := normalizeJSON(data, c.root, idx)

	var (
		records []jsonschema.Deprecation
		err     error
	)

	if schemahook.ReportsDeprecations(ctx) {
		records, err = s.compiled.ValidateDeprecations(ctx, instance)
	} else {
		err = s.compiled.Validate(ctx, instance)
	}

	if err == nil {
		return c.deprecations(records, instance), nil
	}

	// A structured validation failure carries per-location paths, so it
	// converts to a niceyaml.Error. Anything else is an unexpected internal
	// failure.
	ve, ok := errors.AsType[*jsonschema.ValidationError](err)
	if !ok {
		return nil, fmt.Errorf("%w: %w", ErrValidate, err)
	}

	// A $ref the validator cannot resolve arrives as a validation failure
	// at the referencing location, but the data there broke no constraint,
	// so report the schema problem rather than point at the document.
	if refErrs := unresolvedRefs(ve); len(refErrs) > 0 {
		return nil, fmt.Errorf("%w: %w", ErrValidate, errors.Join(refErrs...))
	}

	return c.deprecations(records, instance), c.validationError(ve)
}

// unresolvedRefs returns the failures in the tree of ve that report a $ref
// or $dynamicRef the validator could not resolve, each as an
// [unresolvedRefError]. Such a failure wraps [jsonschema.ErrRefResolve]
// when a resolver returned an error. When no resolver served the
// reference, its message says so. A reference keyword that is itself a
// leaf for any other reason names a target that does not allow any value,
// such as false or {"not": {}}. Its failure is the document's, so it
// stays a violation.
func unresolvedRefs(ve *jsonschema.ValidationError) []error {
	var errs []error

	for _, leaf := range ve.Leaves() {
		switch leaf.Keyword {
		case jsonschema.KeywordRef, jsonschema.KeywordDynamicRef:
			if errors.Is(leaf, jsonschema.ErrRefResolve) || strings.HasPrefix(leaf.Message, "cannot resolve ") {
				errs = append(errs, unresolvedRefError{leaf: leaf})
			}
		}
	}

	return errs
}

// unresolvedRefError is the failure of a $ref or $dynamicRef the validator
// could not resolve. Its message is the message of the failure, which
// states the error of the resolver when one returned an error. The
// failure unwraps to a list that holds that error. An error tree gives
// each error of such a list a row of its own. A tree of the bare failure
// therefore shows the error of the resolver twice. An unresolvedRefError
// unwraps to that error alone, as a wrapper from [fmt.Errorf] does. The
// tree then shows the failure in one row. [errors.As] still finds the
// [*jsonschema.ValidationError].
type unresolvedRefError struct {
	leaf *jsonschema.ValidationError
}

func (e unresolvedRefError) Error() string {
	return e.leaf.Error()
}

// Unwrap returns the error of the resolver, or nil when no resolver
// served the reference. The failure of a reference keyword that is a leaf
// does not have causes, so it holds at most one error.
func (e unresolvedRefError) Unwrap() error {
	if attached := e.leaf.Unwrap(); len(attached) > 0 {
		return attached[0]
	}

	return nil
}

// As sets target to the failure when target is a pointer to a
// [*jsonschema.ValidationError], and reports whether it did.
func (e unresolvedRefError) As(target any) bool {
	ve, ok := target.(**jsonschema.ValidationError)
	if ok {
		*ve = e.leaf
	}

	return ok
}

// rootOf returns the tree of n, or nil when n is nil.
func rootOf(n *niceyaml.Node) ast.Node {
	if n == nil {
		return nil
	}

	return n.AST()
}

// sourcePath converts instance-location segments to a [paths.Path] that
// resolves in root, and returns it in a [datapath.Target] with the node
// the walk reached. Each [jsonschema.Segment] already distinguishes an
// array index from a property name, so sourcePath never guesses whether
// a segment is an index.
//
// A property name is the name the decoder produced, which the source
// might spell another way, such as 0x10 for the member name 16. The walk
// writes the source spelling of each key into the path wherever a path
// selector with that spelling selects the entry of the member, as
// [datapath.Index.Member] describes. Where it cannot, the segment and
// every segment below keep their decoded names, and
// [datapath.Target.Token] locates the violation at the node the walk
// reached. Every segment keeps its decoded name when root is nil.
//
// The walks that share idx share its limit on the merge sources they
// read, which is the limit of one [paths.EntryFinder].
func sourcePath(root ast.Node, idx *datapath.Index, segments []jsonschema.Segment) datapath.Target {
	t, _ := sourceRoute(root, idx, segments, false)

	return t
}

// sourceRoute returns the [datapath.Target] that [sourcePath] returns for
// segments, and the token at each step of its path. The tokens order the
// paths that reach one node. The token of a step is the one
// [datapath.Target.Start] gives. The last step takes the key of its entry
// when key is set.
func sourceRoute(
	root ast.Node,
	idx *datapath.Index,
	segments []jsonschema.Segment,
	key bool,
) (datapath.Target, []*token.Token) {
	t := datapath.Target{Path: paths.Current(), Node: root}
	route := make([]*token.Token, 0, len(segments))

	for i, seg := range segments {
		if seg.IsIndex {
			t = idx.Element(t, seg.Index)
		} else {
			t = idx.Member(t, seg.Key)
		}

		route = append(route, t.Start(key && i == len(segments)-1))
	}

	return t, route
}

// normalizeJSON converts the YAML-native values a decode produces that the
// JSON Schema validator does not accept into the JSON spellings of the
// same data. A !!binary becomes its base64 text. A !!timestamp becomes an
// RFC 3339 full-date, such as 2001-12-14, when the scalar in root that a
// decode read it from holds only a date. Any other !!timestamp becomes an
// RFC 3339 date-time. The node root is the one a decode read data from,
// and without it every timestamp becomes a date-time. The index idx finds
// the members of the mappings in the document of root, and its resolver
// follows the aliases there.
//
// Each [yaml.MapSlice] that a decode with [niceyaml.WithYAMLOrderedMaps]
// yields becomes a map with the same members. A later item replaces an
// earlier one with the same key, as a decode into a map does. The walk
// covers maps, slices, and ordered mappings, so it converts each of these
// values anywhere in a document. Every other value comes back unchanged,
// non-finite floats included, because the validator treats those as
// numbers.
//
// A map or slice without any of these values comes back as the same
// container. One that holds such a value comes back as a copy. An ordered
// mapping always comes back as a new map. Either way, normalizeJSON never
// writes into the caller's data. Handing the validator the caller's own
// containers is safe because [jsonschema.Validator.Validate] does not
// write to its instance or keep a reference to it.
func normalizeJSON(data any, root ast.Node, idx *datapath.Index) any {
	w := normalizer{
		idx:   idx,
		nodes: []ast.Node{idx.Deref(root)},
	}

	out, _ := w.normalize(data)

	return out
}

// normalizer walks a value for [normalizeJSON]. It records the path from
// the top of the value down to the value it visits. It looks up the node
// in root that the path leads to only when it reaches a timestamp. It
// keeps each node it finds while the walk stays under that node. The
// lookups therefore step down root at most once for each value the walk
// visits. The index reads each mapping at most once.
type normalizer struct {
	// The index that finds the members of each mapping the lookups step
	// through.
	idx  *datapath.Index
	path []jsonschema.Segment
	// The node each prefix of path leads to in root, as far down path as
	// the lookups have gone. The first is root, and nodes[i] is the node
	// path[:i] leads to.
	nodes []ast.Node
}

// normalize is [normalizeJSON] that also reports whether the value it
// returns differs from data.
func (w *normalizer) normalize(data any) (any, bool) {
	switch v := data.(type) {
	case []byte:
		return base64.StdEncoding.EncodeToString(v), true
	case time.Time:
		return timestampText(v, w.node()), true
	case map[string]any:
		var out map[string]any

		for key, elem := range v {
			norm, changed := w.child(jsonschema.Segment{Key: key}, elem)
			if !changed {
				continue
			}

			if out == nil {
				out = maps.Clone(v)
			}

			out[key] = norm
		}

		if out == nil {
			return data, false
		}

		return out, true

	case []any:
		var out []any

		for i, elem := range v {
			norm, changed := w.child(jsonschema.Segment{Index: i, IsIndex: true}, elem)
			if !changed {
				continue
			}

			if out == nil {
				out = slices.Clone(v)
			}

			out[i] = norm
		}

		if out == nil {
			return data, false
		}

		return out, true

	case yaml.MapSlice:
		out := make(map[string]any, len(v))

		for _, item := range v {
			key := datapath.MemberName(item.Key)
			norm, _ := w.child(jsonschema.Segment{Key: key}, item.Value)
			out[key] = norm
		}

		return out, true

	default:
		return data, false
	}
}

// child normalizes elem, the member or element of the visited value that
// seg names.
func (w *normalizer) child(seg jsonschema.Segment, elem any) (any, bool) {
	w.path = append(w.path, seg)
	norm, changed := w.normalize(elem)
	w.path = w.path[:len(w.path)-1]

	// The walk leaves elem, so it drops the nodes it found for elem and
	// below it.
	w.nodes = w.nodes[:min(len(w.nodes), len(w.path)+1)]

	return norm, changed
}

// node returns the content of the node that a decode read the visited
// value from. It follows each alias on the way through the resolver of
// the index. It returns nil when root is nil or when an alias on the way
// does not resolve. It also returns nil when the index does not find a
// value node for a member name.
func (w *normalizer) node() ast.Node {
	for len(w.nodes) <= len(w.path) {
		parent := w.nodes[len(w.nodes)-1]
		seg := w.path[len(w.nodes)-1]

		var next ast.Node

		if seg.IsIndex {
			next = datapath.ElementNode(parent, seg.Index)
		} else {
			next = w.idx.MemberNode(parent, seg.Key)
		}

		w.nodes = append(w.nodes, w.idx.Deref(next))
	}

	return w.nodes[len(w.path)]
}

// dateOnlyLayout is the layout go-yaml uses to parse a !!timestamp that
// holds only a date. It accepts a month or a day of one digit, as in
// 2001-1-2.
const dateOnlyLayout = "2006-1-2"

// timestampText returns the RFC 3339 text of t, the value of a !!timestamp
// decoded from node. When the string scalar under the tag holds only a
// date, and that date is t, the text is a full-date such as 2001-12-14.
// Any other node gives a date-time, and so does a nil node, because t
// alone does not show whether the source wrote only a date.
func timestampText(t time.Time, node ast.Node) string {
	text, ok := stringText(node)
	if ok {
		// A second tag under the !!timestamp tag, such as !!int, changes
		// the value the decode parses, so the text must also give t.
		date, err := time.Parse(dateOnlyLayout, text)
		if err == nil && date.Equal(t) {
			return t.Format(time.DateOnly)
		}
	}

	return t.Format(time.RFC3339Nano)
}

// stringText returns the text of the string scalar under the anchors and
// tags of node, and reports whether node holds one. A block scalar gives
// its content without the header, as a decode reads it. A typed nil does
// not hold text.
func stringText(node ast.Node) (string, bool) {
	switch n := astnode.Content(node).(type) {
	case *ast.StringNode:
		return n.Value, true
	case *ast.LiteralNode:
		if n.Value != nil {
			return n.Value.Value, true
		}
	}

	return "", false
}

// checkExpansion returns an error wrapping [ErrValidate] when a map or
// slice in data contains itself. It returns one matching
// [ErrExcessiveAliasing] when aliases make up too much of the data the
// validator reads. The data is at fault for that error, so it comes from
// [niceyaml.Invalid] and does not wrap ErrValidate.
func checkExpansion(data any) error {
	w := expansionWalker{sizes: map[sharedKey]int{}, onPath: map[sharedKey]bool{}}

	_, err := w.walk(data)
	if err != nil {
		return err
	}

	if aliaslimit.Excessive(w.distinct, w.aliased) {
		return niceyaml.Invalid(ErrExcessiveAliasing)
	}

	return nil
}

// expansionWalker counts the nodes of a decoded value the way the
// validator reads them, which is every use of a shared value in full. A
// decode shares the map, slice, or byte slice that an anchor holds
// between its aliases. A byte slice counts as one node per character of
// the base64 text that normalizeJSON builds from it. The walker holds the
// size of each shared value it has walked, so it walks one once and adds
// that size at every later use. It also holds the shared values on the
// path it is walking down, so a value that contains itself stops the
// walk.
//
// The walker counts the nodes it visits as distinct, and a later use of
// a shared value counts as one distinct node. The walker counts the nodes
// that such a use repeats as aliased.
type expansionWalker struct {
	sizes    map[sharedKey]int
	onPath   map[sharedKey]bool
	distinct int
	aliased  int
}

// sharedKey names a map, slice, or byte slice by type, address, and
// length, because a slice and a shorter slice of it share an address. The
// fields serve as the map key.
//
//nolint:unused // The fields tell the keys of the sizes and onPath maps apart.
type sharedKey struct {
	typ reflect.Type
	ptr uintptr
	len int
}

// sharedKeyOf returns the key that names data when it is a map, slice,
// or byte slice. It returns false for any other value and for an empty
// one, which does not hold anything to share.
func sharedKeyOf(data any) (sharedKey, bool) {
	switch data.(type) {
	case map[string]any, []any, yaml.MapSlice, []byte:
	default:
		return sharedKey{}, false
	}

	rv := reflect.ValueOf(data)
	if rv.Len() == 0 {
		return sharedKey{}, false
	}

	return sharedKey{typ: rv.Type(), ptr: rv.Pointer(), len: rv.Len()}, true
}

// walk returns the size of data. A scalar has a size of one, and a byte
// slice has one per character of its base64 text. A map or slice has one
// plus the size of each key and value in it. For a map or slice that the
// walk reaches from inside itself, walk returns an error wrapping
// [ErrValidate].
func (w *expansionWalker) walk(data any) (int, error) {
	w.distinct = aliaslimit.AddCapped(w.distinct, 1)

	key, ok := sharedKeyOf(data)
	if !ok {
		return 1, nil
	}

	if w.onPath[key] {
		return 0, fmt.Errorf("%w: value contains itself", ErrValidate)
	}

	if size, seen := w.sizes[key]; seen {
		w.aliased = aliaslimit.AddCapped(w.aliased, size)

		return size, nil
	}

	w.onPath[key] = true
	defer delete(w.onPath, key)

	size := 1

	switch v := data.(type) {
	case []byte:
		// The walk counted the first character of the base64 text on
		// entry.
		size = min(base64.StdEncoding.EncodedLen(len(v)), aliaslimit.CountCap)
		w.distinct = aliaslimit.AddCapped(w.distinct, size-1)

	case map[string]any:
		for _, elem := range v {
			// The key is a node of its own.
			w.distinct = aliaslimit.AddCapped(w.distinct, 1)

			n, err := w.walk(elem)
			if err != nil {
				return 0, err
			}

			size = aliaslimit.AddCapped(size, aliaslimit.AddCapped(1, n))
		}

	case yaml.MapSlice:
		for _, item := range v {
			// The key is a node of its own, and datapath.MemberName prints a key
			// that is no string in full, so the walk reads the key as it
			// reads a value.
			kn, err := w.walk(item.Key)
			if err != nil {
				return 0, err
			}

			n, err := w.walk(item.Value)
			if err != nil {
				return 0, err
			}

			size = aliaslimit.AddCapped(size, aliaslimit.AddCapped(kn, n))
		}

	case []any:
		for _, elem := range v {
			n, err := w.walk(elem)
			if err != nil {
				return 0, err
			}

			size = aliaslimit.AddCapped(size, n)
		}
	}

	w.sizes[key] = size

	return size, nil
}
