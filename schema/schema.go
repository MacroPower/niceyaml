package schema

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"go.jacobcolvin.com/x/jsonschema"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/aliasing"
	"go.jacobcolvin.com/niceyaml/internal/aliaslimit"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/datapath"
	"go.jacobcolvin.com/niceyaml/internal/unplaced"
	"go.jacobcolvin.com/niceyaml/paths"
)

var (
	// ErrValidate indicates an unexpected, non-validation failure while
	// validating against a schema, such as a reference resolution problem.
	// Schema constraint violations come back as [*niceyaml.Error] values
	// with path information instead of wrapping this sentinel.
	// [niceyaml.IsInvalid] reports a violation. It does not report an
	// error that wraps ErrValidate, since the validation could not run,
	// even when a scoped [niceyaml.Node.Bind] places it at the value it
	// checked.
	ErrValidate = errors.New("validate schema")

	// ErrExcessiveAliasing indicates a value that shares maps, slices, or
	// byte slices so heavily that the validator would read far more data
	// than the value holds, as aliases in a YAML document make a decode
	// share them. It also indicates a document whose aliases would make
	// the decoder itself read that much. [Schema.Validate] and
	// [Schema.ValidateValue] return it. A
	// [matcher.Content] or [matcher.Text] guard refuses such a document
	// with it too, and [Registry.Lookup] then returns it wrapped together
	// with [ErrResolve]. The document or the value is at fault in every
	// case, so [niceyaml.IsInvalid] reports each of these errors, and none
	// of them wraps [ErrValidate].
	//
	// [niceyaml.WithAliasLimit] on the source of a document turns the
	// limit off for Validate and for the guard. ValidateValue takes no
	// source, so it applies the limit to every value.
	// It is the same error value as [niceyaml.ErrExcessiveAliasing].
	ErrExcessiveAliasing = aliaslimit.ErrExcessiveAliasing

	// ErrCompile indicates a schema document that does not compile.
	// [Compile] and [Registry.Lookup] return it.
	ErrCompile = errors.New("compile schema")

	// The source [Schema.ValidateValue] binds its errors to. It holds no
	// text and no name, since the value came from no file, so an error
	// bound to it reads as its path and its message. Every call shares
	// the one source, which never changes.
	noSource = niceyaml.NewSourceFromString("")
)

// CompileOption configures [Compile] and [MustCompile], and
// [WithCompileOptions] hands the same options to a [Registry].
//
// Available options:
//   - [WithJSONSchemaOptions]
type CompileOption func(*compileConfig)

// compileConfig holds the settings a [CompileOption] configures.
type compileConfig struct {
	jsonOpts []jsonschema.ValidateOption
}

// newCompileConfig applies opts over the defaults.
func newCompileConfig(opts []CompileOption) compileConfig {
	var cfg compileConfig

	for _, opt := range opts {
		opt(&cfg)
	}

	return cfg
}

// WithJSONSchemaOptions is a [CompileOption] that passes
// [jsonschema.ValidateOption] values to the underlying
// [jsonschema.CompileJSON]. It is the escape hatch for settings of the JSON
// Schema library that have no option of their own, such as format
// assertions or a custom format validator:
//
//	v, err := schema.Compile(ctx, data, schema.WithJSONSchemaOptions(jsonschema.WithFormats(true)))
//
// A [Registry] resolves the $refs of a schema from [File] or [URL] with
// a resolver of its own, and [WithCompileOptions] says when a
// [jsonschema.WithRefResolver] given here replaces it.
//
// The option keeps its own copy of opts, so writing to the caller's slice
// afterwards changes nothing, even for a [Registry] that compiles later.
func WithJSONSchemaOptions(opts ...jsonschema.ValidateOption) CompileOption {
	opts = slices.Clone(opts)

	return func(cfg *compileConfig) {
		cfg.jsonOpts = append(cfg.jsonOpts, opts...)
	}
}

// Compile creates a new [*Schema] from a JSON schema document. The
// context reaches the reference resolver for references it resolves
// while compiling. An error wraps [ErrCompile].
//
// It is the path for a program that holds one schema, such as one embedded
// in the binary:
//
//	//go:embed config.schema.json
//	var schemaJSON []byte
//
//	v, err := schema.Compile(ctx, schemaJSON)
//
// A schema known valid at build time compiles with [MustCompile] at package
// scope. A registry compiles the schemas its resolvers name as bytes the
// same way, with the options [WithCompileOptions] gives it, and takes a
// compiled Schema as it is.
//
// Compile reads no file and fetches no URL. A $ref to another document,
// such as "server.json" or an https URL, resolves only through a ref
// resolver given with [WithJSONSchemaOptions]. When such a $ref does not
// resolve, the schema still compiles. Each document whose validation
// reaches the $ref then fails with an error wrapping [ErrValidate], and a
// document that never reaches it passes. A $ref to a location the
// document lacks, such as "#/$defs/missing", fails the compile.
//
// A schema whose $refs name files or URLs beside it loads through a
// [Registry] instead. [Registry.Schema] loads the schema a [File] or [URL]
// names and resolves each $ref against that location:
//
//	v, err := schema.NewRegistry().Schema(ctx, schema.File("schemas/root.json"))
func Compile(ctx context.Context, data []byte, opts ...CompileOption) (*Schema, error) {
	compiled, err := compileJSON(ctx, data, opts)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCompile, err)
	}

	return compiled, nil
}

// compileJSON is [Compile] before wrapping the error with [ErrCompile].
func compileJSON(ctx context.Context, data []byte, opts []CompileOption) (*Schema, error) {
	cfg := newCompileConfig(opts)

	compiled, err := jsonschema.CompileJSON(ctx, data, cfg.jsonOpts...)
	if err != nil {
		//nolint:wrapcheck // Callers wrap the error with ErrCompile.
		return nil, err
	}

	return FromJSONSchema(compiled), nil
}

// MustCompile is [Compile] with a background context that panics when the
// schema does not compile. Use it for a package-scope schema compiled
// from a document go:embed brings in:
//
//	var Config = schema.MustCompile(schemaJSON)
//
// A schema whose $ref to another document does not resolve still
// compiles, as [Compile] describes, so MustCompile returns it. Each
// document that reaches that $ref then fails validation.
func MustCompile(data []byte, opts ...CompileOption) *Schema {
	v, err := Compile(context.Background(), data, opts...)
	if err != nil {
		panic(err)
	}

	return v
}

// FromJSONSchema creates a new [*Schema] from a [*jsonschema.Validator]
// compiled elsewhere, such as one built from a Go type with
// [jsonschema.Compile]. A schema held as JSON compiles with [Compile] or
// [MustCompile] instead.
//
// Panics if v is nil.
func FromJSONSchema(v *jsonschema.Validator) *Schema {
	if v == nil {
		panic("schema.FromJSONSchema: validator is nil")
	}

	return &Schema{compiled: v}
}

// Schema is a compiled JSON schema. It is a [niceyaml.Validator] that
// checks a document against the schema and reports constraint violations
// as [*niceyaml.Error] values that carry the YAML path to each failing
// location, bound to the source for
// [go.jacobcolvin.com/niceyaml/printer.Printer] to display.
// Each one wraps a [*Violation] that names the keyword the value fails.
// [Schema.Validate] checks a node, which is the whole document for the
// root [niceyaml.Node] of a document and one value inside it for a Node
// from [niceyaml.Node.At]. [Schema.ValidateValue] checks decoded data
// that came from no document, such as the body of a request, and the
// text of its error names the path of each violation. A caller that
// knows where the data stands in a document places that error there with
// [niceyaml.Rebase] and [niceyaml.Node.Bind].
//
// A Schema is the validator for a program that holds one schema and
// compiles it itself. It is also a [Resolver] that names itself for every
// document, so one goes into a [Registry] as it is, on its own or behind
// a [When] guard. The registry then validates with it without loading or
// compiling anything:
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
	// Passes every document and value without reading it, as the schema a
	// "$schema=none" directive names does.
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
// The Ref has no [Ref.Key], since the registry has nothing to load or
// cache for it. [Registry.Schema] returns the schema as it is, and
// [Registry.Load] returns an error wrapping [ErrLoad].
func (s *Schema) Ref() Ref {
	return Ref{schema: s}
}

// Resolve implements [Resolver]. It names the schema for every document
// and never reports [ErrNoMatch].
func (s *Schema) Resolve(_ context.Context, _ *niceyaml.Node) (Ref, error) {
	return s.Ref(), nil
}

// Validate implements [niceyaml.Validator]. It reads n as any through
// [niceyaml.Node.Decode] and checks the result against the schema as
// [Schema.ValidateValue] does. The decode applies the alias limit to the
// document of n before it reads anything. Validate applies the limit to
// the result as well only where the document holds an alias to a
// reference document. [niceyaml.WithAliasLimit] on the source of n turns
// both off. [niceyaml.WithValidator] runs the schema before a decode, a
// [niceyaml.Decoder] runs it on every node it decodes, and
// [niceyaml.Node.Validate] runs it on its own. A Node from
// [niceyaml.Node.At] decodes to the node it selects, so the schema checks
// that node and a violation's `@` path resolves from it. Every error
// comes back bound through n with [niceyaml.Node.Bind], so a call to
// Validate returns the error [niceyaml.Node.Validate] returns for the
// schema. A validator that runs the schema on each node of a list thus
// reports each violation on its own lines. [Schema.ValidateValue] checks
// data for a caller that reports the errors somewhere else.
//
// A document that did not parse has no data to check, so Validate returns
// the syntax error [niceyaml.Node.Err] returns for it, whatever the
// schema accepts. An empty document, such as the one a trailing "---"
// leaves, decodes to null, so a schema that wants a mapping rejects it.
// [niceyaml.SkipEmpty] wraps the schema to pass such a document.
//
// Holding the node lets Validate spell each key in a violation's path as
// the source does, so a key the decoder respells, such as the hexadecimal
// 0x10 for the member name 16, still names its member. Where a later key
// in the mapping or its merge sources has the same spelling, a path
// through that spelling selects the later key, so the path names the
// member and each key below it as the decoder does. Such a path does not
// select the member, so the violation carries the position of the value
// beside the path and binds there.
//
// A violation about a member the mapping leaves out, such as one of
// required, carries the path the member would have, as [Violation]
// describes. The path names the member as the schema does, since the
// source spells no key for it. The mapping can hold a key with that
// spelling for a member of another name, as the key 0x10 sets the member
// 16 under a schema that requires 0x10. The path then selects the entry
// of that key, so the violation carries the position of the mapping
// beside the path and binds there.
//
// To tell which key a spelling selects, Validate reads the sources of
// each `<<` merge key that brings the key in or stands after it. The
// violations of one call may read as many nodes that way as
// [paths.ErrExcessiveMerging] allows one path selector. Past that limit
// Validate still reports every violation, and a path through such a key
// names the member and each key below it as the decoder does.
//
// The node also shows which !!timestamp values the source wrote as a bare
// date. Validate hands the schema each of those as an RFC 3339 full-date,
// so !!timestamp 2001-12-14 matches format "date" as the untagged
// 2001-12-14 does. Validate finds that scalar behind an alias or a merge
// key as it finds the key it spells in a violation's path. Where Validate
// cannot tell which scalar a timestamp came from, the timestamp keeps the
// date-time spelling that [Schema.ValidateValue] gives it. That holds
// behind an alias that does not resolve. It also holds under a mapping,
// at any depth, with a key whose member name Validate cannot tell at or
// after the member leading to the timestamp, since such a key may set a
// member of the same name. A merge key whose sources do not resolve is
// one such key.
//
// The decoder writes out the whole content of an alias it spells as text,
// such as an alias used as a key. It also reads a mapping a merge key
// brings in again at every merge. A small document can therefore cost far
// more to decode than the decoded value shows. The decode Validate runs
// therefore counts before it reads. For a node that holds an alias, it
// counts the nodes a decode of the whole document reads, with each alias
// reading its content in full, as [niceyaml.Node.DecodeInto] describes.
// The count covers the whole document even for a node below the root, so
// every node in a document that holds an alias gets the same verdict. The
// decode applies the alias limit of [Schema.ValidateValue] to the count.
// An alias to a text tag, such as !!binary or !!str, counts one node per
// byte of the text under the tag. So does an alias that reaches a
// !!binary scalar through another tagged alias, as *s does for
// `&s !foo *b`. Each copy of a scalar the decoder writes out as text,
// such as in a key that holds a sequence, counts the same way. Any other
// alias to a scalar counts as one unaliased node. For a document past the
// limit, Validate returns the error of that decode, which matches
// [ErrExcessiveAliasing] and binds at the first token of the node that is
// not a comment:
//
//	app.yaml:1:1: excessive aliasing
//
// The aliases of the document are the cause, so [niceyaml.IsInvalid]
// reports the error, and it does not wrap [ErrValidate]. Validate puts no
// limit of its own on the result of the decode. A node below the root
// then passes the limit wherever its document does, even when aliases
// make up a larger share of the node than of the document.
//
// A document that holds an alias with no anchor of its name before it
// is the exception. A decode resolves such an alias against a reference
// document, such as one of [niceyaml.WithReferences]. The count cannot
// see a reference document, so it takes the alias as one node. For such
// a document Validate also applies the limit to the result of the
// decode, as [Schema.ValidateValue] does. A node below the root can then
// exceed the limit where its document passes. That error matches
// ErrExcessiveAliasing as well, and IsInvalid reports it. It carries no
// location, so it binds through n as any such error does. A node below
// the root binds it at the node, under its path, and a root binds it
// with no position.
//
// [niceyaml.WithAliasLimit] on the source of n turns the limit off for
// the decode and for the result. Validate then reads every use of every
// alias, at the cost WithAliasLimit describes.
func (s *Schema) Validate(ctx context.Context, n *niceyaml.Node) error {
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
			//nolint:wrapcheck // Binding names the document; the error keeps its own context.
			return n.Bind(err)
		}
	}

	//nolint:wrapcheck // Binding resolves the violations; each keeps its own context.
	return n.Bind(s.validate(ctx, data, n))
}

// ValidateValue checks data, the decoded form of a YAML value, against the
// schema. The value is the shape a decode into any yields: maps with
// string keys, slices, strings, bools, nil, and numbers, nested as the
// document nests them. The validator does not accept a Go struct, since it
// reads the data as JSON does.
//
// ValidateValue first converts the YAML-native values the JSON Schema
// validator does not accept into the JSON spelling of the same data,
// anywhere in the value. A !!binary becomes its base64 text and a
// !!timestamp its RFC 3339 date-time text, even for a timestamp the
// source wrote as a bare date, since a [time.Time] does not record that.
// [Schema.Validate] reads the source and spells such a timestamp as a
// full-date where it can. The [yaml.MapSlice] a decode with
// [yaml.UseOrderedMap] yields for each mapping becomes a map with the
// same members, and where two items share a key, the later one wins, as
// it does in a decode into a map.
//
// Returns nil when data conforms. On a constraint violation, returns a
// [*niceyaml.SourceError]. A single violation carries its YAML path on
// the error itself, and several violations become a count summary from
// [niceyaml.NewSummary] that heads one error per violation, each with the
// path to one failing location. The error of each violation wraps a
// [*Violation] that names the keyword the value fails and where that
// keyword stands in the schema. A value that matches no branch of an
// anyOf or oneOf counts as one violation, whose details are the failures
// of each branch the value could have been meant for, as [Violation]
// describes. Any other failure wraps [ErrValidate], including a $ref the
// validator cannot resolve, since the value is not at fault for that.
//
// ValidateValue takes no document, so it binds every error to an empty
// source with no name. The binding puts each path in the text of the
// error, so the error names every failing location through %v, inside a
// wrapper from [fmt.Errorf], in a join from [errors.Join], and in a log.
// One violation reads "$.port: 0 is less than 1", and several read as
// the summary with one violation per line:
//
//	2 schema violations
//	$.port: 0 is less than 1
//	$.name: missing required property "name"
//
// Each path starts at `$`, which here is the value data stands for, and
// no position stands in front of it. [niceyaml.IsInvalid] reports the
// error of a violation, and [errors.As] finds the [*Violation] in it,
// which is the first among several. [niceyaml.FormatError] prints the
// same errors as a tree.
//
// The result stands in no document, so a document can still place it.
// [niceyaml.Rebase] and every Bind return an error bound to a document
// as it is, and they read this result as the errors it was made from,
// whose paths start at `@`, the value data stands for. A caller that
// knows where the value stands in a document puts the errors under that
// path, and a [niceyaml.Node] of the document then binds them:
//
//	err := s.ValidateValue(ctx, data)
//
//	return doc.Bind(niceyaml.Rebase(err, paths.Doc().Child("request")))
//
// The bound error reads "app.yaml:2:9: $.request.port: 0 is less than 1".
// The same holds for a result that a Validate method of a type or a
// [niceyaml.Validator] returns, which a decode places at the value it
// checked. A wrapper such as [fmt.Errorf] around the result keeps its
// text through both calls, behind the position and the path, as in
// "app.yaml:2:9: $.request.port: check: 0 is less than 1". A wrapper
// whose text does not hold the text of the result, such as one that
// quotes it, stays as it is, and so does a result under an
// [*niceyaml.Error]. The result itself never changes.
//
// ValidateValue holds no source, so each path names a key as the decoder
// does, such as 16 for a key the document spells 0x10. A caller that
// reports the errors in the document the data came from takes each
// location from [niceyaml.Node.DataLocator].
//
// ValidateValue rejects two shapes of data before checking anything. A
// value whose shared maps, slices, or byte slices would expand past the
// alias limit, as YAML aliases make them, returns an error matching
// [ErrExcessiveAliasing]. The value is at fault for it, so
// [niceyaml.IsInvalid] reports the error, and it does not wrap
// [ErrValidate]. The limit follows the rule gopkg.in/yaml.v3 applies to
// the share of aliased nodes in a document, and a []byte counts as one
// node per character of its base64 text. Where yaml.v3 applies the rule
// node by node as it decodes, ValidateValue applies it once to the whole
// value. It counts each use of an aliased scalar other than a !!binary as
// an unaliased node, so it accepts some documents yaml.v3 rejects.
// ValidateValue takes no source, so it applies the limit to every value,
// including one decoded from a source that [niceyaml.WithAliasLimit]
// turned the limit off for. A map or slice that contains itself returns
// an error wrapping [ErrValidate].
//
// The context reaches the underlying [jsonschema.Validator], where remote
// reference resolution honors its cancellation and deadlines.
func (s *Schema) ValidateValue(ctx context.Context, data any) error {
	err := s.checkValue(ctx, data)
	if err == nil {
		return nil
	}

	//nolint:wrapcheck // Binding puts each path in the text; the error keeps its own context.
	return noSource.Bind(valueError{err: err})
}

// valueError holds the errors of a value that came from no document, as
// [Schema.ValidateValue] checks one. It matches [unplaced.Err], so the
// binding of the errors it wraps stands in no document, and a later Bind
// or [niceyaml.Rebase] places those errors.
type valueError struct {
	err error
}

// Error returns the message of the errors.
func (e valueError) Error() string {
	return e.err.Error()
}

// Unwrap returns the errors.
func (e valueError) Unwrap() error {
	return e.err
}

// Is reports whether target is [unplaced.Err].
func (e valueError) Is(target error) bool {
	return target == unplaced.Err
}

// checkValue checks data against the schema and returns the errors
// unbound, each with a path that starts at `@`, the value data stands
// for. It holds no source, so each path names a key as the decoder does,
// such as 16 for a key the document spells 0x10.
func (s *Schema) checkValue(ctx context.Context, data any) error {
	if s.acceptAll {
		return nil
	}

	// Both normalizeJSON and the validator read a shared value again at
	// every use, so the check runs before either of them.
	err := checkExpansion(data)
	if err != nil {
		return err
	}

	return s.validate(ctx, data, nil)
}

// validate checks data against the schema as [Schema.checkValue] does
// once data passes its expansion check. It takes the node a decode read
// data from, which [Schema.Validate] has and [Schema.ValidateValue]
// does not. A violation at a key the decoder respells, such as the
// hexadecimal 0x10, needs the node to spell the key in its path as the
// source does, and a !!timestamp needs it to show whether the source
// wrote only a date.
func (s *Schema) validate(ctx context.Context, data any, n *niceyaml.Node) error {
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

	err := s.compiled.Validate(ctx, normalizeJSON(data, rootOf(n), idx))
	if err == nil {
		return nil
	}

	// A structured validation failure carries per-location paths, so it
	// converts to a niceyaml.Error. Anything else is an unexpected internal
	// failure.
	ve, ok := errors.AsType[*jsonschema.ValidationError](err)
	if !ok {
		return fmt.Errorf("%w: %w", ErrValidate, err)
	}

	// A $ref the validator cannot resolve arrives as a validation failure
	// at the referencing location, but the data there broke no constraint,
	// so report the schema problem rather than point at the document.
	if refErrs := unresolvedRefs(ve); len(refErrs) > 0 {
		return fmt.Errorf("%w: %w", ErrValidate, errors.Join(refErrs...))
	}

	return newValidationError(ve, n, idx)
}

// unresolvedRefs returns the failures in the tree of ve that report a $ref
// or $dynamicRef the validator could not resolve. Either a resolver
// returned an error, which wraps [jsonschema.ErrRefResolve], or no
// resolver served the reference, which the message names. A reference
// keyword that is itself a leaf for any other reason names a target that
// allows nothing, such as false or {"not": {}}, so its failure is the
// document's and stays a violation.
func unresolvedRefs(ve *jsonschema.ValidationError) []error {
	var errs []error

	for _, leaf := range ve.Leaves() {
		switch leaf.Keyword {
		case jsonschema.KeywordRef, jsonschema.KeywordDynamicRef:
			if errors.Is(leaf, jsonschema.ErrRefResolve) || strings.HasPrefix(leaf.Message, "cannot resolve ") {
				errs = append(errs, leaf)
			}
		}
	}

	return errs
}

// rootOf returns the tree of n, or nil for no node.
func rootOf(n *niceyaml.Node) ast.Node {
	if n == nil {
		return nil
	}

	return n.AST()
}

// sourcePath converts instance-location segments to a [paths.Path] that
// resolves in root, and returns it in a [datapath.Target] with the node
// the walk reached. Each [jsonschema.Segment] already distinguishes an
// array index from a property name, so sourcePath does no numeric
// guessing.
//
// A property name is the name the decoder produced, which the source may
// spell another way, as it spells the member name 16 as 0x10. The walk
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
	t := datapath.Target{Path: paths.Current(), Node: root}

	for _, seg := range segments {
		if seg.IsIndex {
			t = idx.Element(t, seg.Index)
		} else {
			t = idx.Member(t, seg.Key)
		}
	}

	return t
}

// normalizeJSON converts the YAML-native values a decode produces that the
// JSON Schema validator does not accept into the JSON spellings of the
// same data. A !!binary becomes its base64 text. A !!timestamp becomes an
// RFC 3339 full-date, such as 2001-12-14, when the scalar in root that a
// decode read it from holds only a date, and an RFC 3339 date-time
// otherwise. Root is the node a decode read data from, and without it
// every timestamp becomes a date-time. The index idx finds the members of
// the mappings in the document of root, and its resolver follows the
// aliases there. A [yaml.MapSlice], which a decode with
// [yaml.UseOrderedMap] yields for each mapping, becomes a map with the
// same members, and a later item replaces an earlier one with the same
// key, as a decode into a map does. It walks maps, slices, and ordered
// mappings so such a value anywhere in a document stays validatable.
// Every other value comes back unchanged, non-finite floats included,
// since the validator treats those as numbers.
//
// A map or slice that holds none of these values comes back as the same
// container, and one that does comes back as a copy. An ordered mapping
// always comes back as a new map. Either way, normalizeJSON never writes
// into the caller's data. Handing the validator the caller's own
// containers is safe because [jsonschema.Validator.Validate] only reads
// its instance and keeps no reference to it.
func normalizeJSON(data any, root ast.Node, idx *datapath.Index) any {
	w := normalizer{
		idx:   idx,
		nodes: []ast.Node{idx.Deref(root)},
	}

	out, _ := w.normalize(data)

	return out
}

// normalizer walks a value for [normalizeJSON]. It records the path from
// the top of the value down to the value it visits, and looks up the node
// in root that the path leads to only when it reaches a timestamp. It
// keeps each node it finds while the walk stays under that node, so the
// lookups step down root at most once for each value the walk visits. The
// index reads each mapping at most once.
type normalizer struct {
	// Finds the members of each mapping the lookups step through.
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
// value from, following each alias on the way through the resolver of
// the index. It returns nil when root is nil, behind an alias that does
// not resolve, and at a member name the index finds no value node for.
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
// Any other node gives a date-time, and so does a nil node, since t alone
// does not show whether the source wrote only a date.
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
// its content without the header, as a decode reads it. A typed nil holds
// no text.
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
// validator would read. The data is at fault for that error, so it comes
// from [niceyaml.Invalid] and does not wrap ErrValidate.
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
// decode shares the map, slice, or byte slice an anchor holds between
// its aliases, and a byte slice counts as one node per character of the
// base64 text normalizeJSON builds from it. The walker holds the size of
// each shared value it has walked, so it walks one once and adds that
// size at every later use. It also holds the shared values on the path
// it is walking down, so a value that contains itself stops the walk.
// It counts the nodes it visits as distinct, with a later use of a
// shared value as one node, and the nodes such a use repeats as aliased.
type expansionWalker struct {
	sizes    map[sharedKey]int
	onPath   map[sharedKey]bool
	distinct int
	aliased  int
}

// sharedKey names a map, slice, or byte slice by type, address, and
// length, since a slice and a shorter slice of it share an address. The
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
// one, which holds nothing to share.
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

// walk returns the size of data: one for a scalar, one per character of
// the base64 text of a byte slice, and one for a map or slice plus the
// size of each key and value in it. A map or slice that the walk reaches
// from inside itself returns an error wrapping [ErrValidate].
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
