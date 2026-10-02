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
	"github.com/goccy/go-yaml/token"
	"go.jacobcolvin.com/x/jsonschema"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/aliasing"
	"go.jacobcolvin.com/niceyaml/internal/aliaslimit"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/paths"
)

var (
	// ErrValidate indicates an unexpected, non-validation failure while
	// validating against a schema, such as a reference resolution problem.
	// Schema constraint violations come back as [*niceyaml.Error] values
	// with path information instead of wrapping this sentinel.
	ErrValidate = errors.New("validate schema")

	// ErrExcessiveAliasing indicates a value that shares maps, slices, or
	// byte slices so heavily that the validator would read far more data
	// than the value holds, as aliases in a YAML document make a decode
	// share them. It also indicates a document whose aliases would make
	// the decoder itself read that much. [Schema.Validate] and
	// [Schema.ValidateValue] return it wrapped together with [ErrValidate].
	// A [matcher.Content] guard refuses such a document with it too, and
	// [Registry.Lookup] then returns it wrapped together with [ErrResolve].
	// It is the same error value as [niceyaml.ErrExcessiveAliasing].
	ErrExcessiveAliasing = aliaslimit.ErrExcessiveAliasing

	// ErrCompile indicates a schema document that does not compile.
	// [Compile] and [Registry.Lookup] return it.
	ErrCompile = errors.New("compile schema")
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
// from [niceyaml.Node.At]. [Schema.ValidateValue] checks decoded data,
// such as one value taken from a document with a scoped
// [niceyaml.Node.Decode] into any.
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
// [Schema.ValidateValue] does. Validate applies the alias limit to the
// document of n before the decode. It applies the limit to the result as
// well only where the document holds an alias to a reference document.
// [niceyaml.WithValidator] runs the schema before a decode, a
// [niceyaml.Decoder] runs it on every node it decodes, and
// [niceyaml.Node.Validate] runs it on its own. A Node from
// [niceyaml.Node.At] decodes to the node it selects, so the schema checks
// that node and a violation's path resolves from it. Every error comes
// back bound through n with [niceyaml.Node.Bind], so a call to Validate
// returns the error [niceyaml.Node.Validate] returns for the schema. A
// validator that runs the schema on each node of a list thus reports
// each violation on its own lines. [Schema.ValidateValue] returns unbound
// errors for a caller that reports them somewhere else.
//
// A document that did not parse has no data to check, so Validate returns
// the syntax error [niceyaml.Node.Err] returns for it, whatever the
// schema accepts.
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
// The decoder writes out the whole content of an alias it spells as
// text, such as an alias used as a key. It also reads a mapping a merge
// key brings in again at every merge. A small document can therefore cost
// far more to decode than the decoded value shows. Before Validate
// decodes a node that holds an alias, it counts the nodes a decode of the
// whole document reads, with each alias reading its content in full. The
// count covers the whole document even for a node below the root, so
// every node in a document that holds an alias gets the same verdict.
// Validate applies the alias limit of [Schema.ValidateValue] to the
// count. An alias to a text tag, such as !!binary or !!str, counts one
// node per byte of the text under the tag. So does an alias that reaches
// a !!binary scalar through another tagged alias, as *s does for
// `&s !foo *b`. Each copy of a scalar the decoder writes out as text,
// such as in a key that holds a sequence, counts the same way. Any other
// alias to a scalar counts as one unaliased node. A document past the
// limit returns an error wrapping both [ErrValidate] and
// [ErrExcessiveAliasing] without decoding. Validate puts no limit of its
// own on the result of the decode. A node below the root then passes the
// limit wherever its document does, even when aliases make up a larger
// share of the node than of the document.
//
// A document that holds an alias with no anchor of its name before it
// is the exception. A decode resolves such an alias against a reference
// document, such as one of [niceyaml.WithReferences]. The count cannot
// see a reference document, so it takes the alias as one node. For such
// a document Validate also applies the limit to the result of the
// decode, as [Schema.ValidateValue] does. A node below the root can then
// exceed the limit where its document passes.
func (s *Schema) Validate(ctx context.Context, n *niceyaml.Node) error {
	err := n.Err()
	if err != nil {
		//nolint:wrapcheck // The source bound the syntax error already.
		return err
	}

	if s.acceptAll {
		return nil
	}

	err = aliasing.CheckDecode(n)
	if err != nil {
		//nolint:wrapcheck // Binding names the document; the error keeps its own context.
		return n.Bind(fmt.Errorf("%w: %w", ErrValidate, err))
	}

	// A decode into any yields only the YAML built-in types, none of which
	// validates itself, so the self-validation walk would find nothing.
	// The decode binds its own error.
	data, err := n.Decode[any](ctx, niceyaml.WithSelfValidation(false))
	if err != nil {
		return err
	}

	// The count above takes each alias as the validator reads the value
	// a decode shares at it, which is a mapping or a sequence in full and
	// a !!binary scalar by its text. An expansion check of data would
	// repeat that count for the node alone, and could give a node below
	// the root a verdict apart from its document's. The count cannot see
	// a reference document, though, so data gets the check where the
	// document holds an alias to one.
	if aliasing.HoldsReferenceAlias(n) {
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
// [*niceyaml.Error]. A single violation carries its YAML path on the error
// itself, and several violations become a count summary whose nested errors
// each carry the path to one failing location. The error of each violation
// wraps a [*Violation] that names the keyword the value fails and where
// that keyword stands in the schema. A value that matches no branch of an
// anyOf or oneOf counts as one violation, which nests the failures of
// each branch the value could have been meant for, as [Violation]
// describes. Any other failure wraps
// [ErrValidate], including a $ref the validator cannot resolve, since no
// location in the document is at fault for that.
//
// ValidateValue holds no source, so its errors are unbound and write
// their paths from the root of data. The [niceyaml.Node] data came from
// binds them with [niceyaml.Node.Bind]. A caller that reports them under
// another path, or in another document, puts them under that path with
// [niceyaml.Rebase] before it binds them, which the bound errors of
// [Schema.Validate] do not allow.
//
// ValidateValue rejects two shapes of data before checking anything. A
// value whose shared maps, slices, or byte slices would expand past the
// alias limit, as YAML aliases make them, returns an error wrapping both
// [ErrValidate] and [ErrExcessiveAliasing]. The limit follows the rule
// gopkg.in/yaml.v3 applies to the share of aliased nodes in a document,
// and a []byte counts as one node per character of its base64 text.
// Where yaml.v3 applies the rule node by node as it decodes,
// ValidateValue applies it once to the whole value. It counts each use of
// an aliased scalar other than a !!binary as an unaliased node, so it
// accepts some documents yaml.v3 rejects. A map or slice that contains
// itself returns an error wrapping [ErrValidate].
//
// The context reaches the underlying [jsonschema.Validator], where remote
// reference resolution honors its cancellation and deadlines.
func (s *Schema) ValidateValue(ctx context.Context, data any) error {
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

// validate checks data against the schema as [Schema.ValidateValue] does
// once data passes its expansion check. It takes the node a decode read
// data from, which [Schema.Validate] has and a caller of ValidateValue
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
	idx := newMemberIndex(resolver)

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

// sourceTarget is where a violation lies in a document: the path that
// names the location, and the node [sourcePath] reached there, which
// locates the violation where the path cannot.
type sourceTarget struct {
	// The node at the location, or nil when the walk could not follow the
	// path to it.
	node ast.Node
	// The entry that holds node, when a member name reached it.
	entry *ast.MappingValueNode
	path  paths.Path
	// Whether the walk wrote a decoded name it could not show to select
	// the entry of its member.
	unspelled bool
}

// token returns the token to bind a violation at when the walk reached
// its location and wrote a path it could not show to select it. With key
// set, the violation constrains the key of the member, as a path that
// ends in [paths.Path.Key] does. The token is the one a path to the
// location would resolve to: the token that starts the node, or the key
// of its entry.
//
// It returns nil when the path selects the location, and when the walk
// did not reach the location, as it cannot through a mapping whose keys
// it cannot all name. The path is then all that says where the violation
// lies.
func (t sourceTarget) token(key bool) *token.Token {
	if !t.unspelled {
		return nil
	}

	return t.start(key)
}

// start returns the token a path to the location of t would resolve to,
// whether or not the path of t selects the location. With key set, that
// is the key of the entry that holds the node, and the token that starts
// the node otherwise or where no entry holds it. It returns nil when the
// walk did not reach the location.
func (t sourceTarget) start(key bool) *token.Token {
	if astnode.IsNil(t.node) {
		return nil
	}

	if key && t.entry != nil {
		return keyToken(t.entry)
	}

	return astnode.FirstToken(t.node)
}

// keyToken returns the token that starts the key of entry, without the `?`
// of an explicit key, as a path that ends in [paths.Path.Key] resolves
// it. An entry with no key gives the token that starts its value.
func keyToken(entry *ast.MappingValueNode) *token.Token {
	if astnode.Content(entry.Key) == nil {
		return astnode.FirstToken(entry.Value)
	}

	key := ast.Node(entry.Key)
	if explicit, ok := key.(*ast.MappingKeyNode); ok {
		key = explicit.Value
	}

	return astnode.FirstToken(key)
}

// sourcePath converts instance-location segments to a [paths.Path] that
// resolves in root, and returns it in a [sourceTarget] with the node the
// walk reached. Each [jsonschema.Segment] already distinguishes an array
// index from a property name, so sourcePath does no numeric guessing.
//
// A property name is the name the decoder produced, which the source may
// spell another way, as it spells the member name 16 as 0x10. The walk
// down root matches each mapping key by its decoded name and writes the
// source spelling of that key into the path instead. The spelling is the
// text [paths.Resolver.KeyName] gives the key, which a path selector
// matches.
//
// The walk follows each alias through the resolver of idx, so it reaches
// the node a path through the same alias resolves to. A key under an
// aliased mapping keeps its source spelling too, and an alias used as a
// key takes the spelling of its anchor's content. A segment the walk
// cannot follow keeps its decoded name, such as a member behind an alias
// that does not resolve. A member a merge key brings in takes the
// spelling its source gives the key. Every segment keeps its decoded name
// when root is nil, as does a key the walk finds but cannot spell.
//
// The walk writes a spelling only where a path selector with that
// spelling selects the entry that sets the member, as the finder of idx
// reports. A later entry with the same spelling can win the selector,
// such as a later key of the mapping, a later merge key, or a later
// source of one merge key. The segment then keeps its decoded name, which
// may select no entry, or another entry. Where the name a segment writes
// does not select the entry of its member, every segment below keeps its
// decoded name too, since a key spelled below would resolve under the
// entry that name selects. The target then reports the path as unspelled.
// The walk still follows each member it finds, so the target holds the
// node of the location, and [sourceTarget.token] locates the violation
// there. A member the walk does not find leaves the target with no node.
//
// The finder reads the sources of a merge key that brings a key in or
// stands after it, and the walks that share idx share the limit of one
// [paths.EntryFinder] on those reads. Past that limit the finder reports
// no entry for such a key, so its segment and every segment below keep
// their decoded names.
func sourcePath(root ast.Node, idx *memberIndex, segments []jsonschema.Segment) sourceTarget {
	t := sourceTarget{path: paths.Root(), node: root}

	for _, seg := range segments {
		content := deref(idx.resolver, t.node)

		if seg.IsIndex {
			t.path = t.path.Index(seg.Index)
			t.node, t.entry = elementNode(content, seg.Index), nil

			continue
		}

		name := seg.Key
		member := idx.lookup(content, seg.Key)

		if !t.unspelled {
			var (
				spelled string
				ok      bool
			)

			if member.entry != nil {
				spelled, ok = idx.resolver.KeyName(member.entry.Key)
			}

			switch {
			case ok && idx.selects(content, spelled, member):
				name = spelled

			case idx.selects(content, name, member):
				// The decoded name selects the entry as it is.

			default:
				t.unspelled = true
			}
		}

		t.path = t.path.Child(name)
		t.node, t.entry = member.value, member.entry
	}

	return t
}

// deref returns the content under node. It looks through what
// [astnode.Content] looks through and follows each alias through r. It
// returns nil for an alias that does not resolve, and for an alias it
// reaches again, which leads back to itself through a tag.
func deref(r *paths.Resolver, node ast.Node) ast.Node {
	var followed []*ast.AliasNode

	for {
		node = astnode.Content(node)

		alias, ok := node.(*ast.AliasNode)
		if !ok {
			return node
		}

		if slices.Contains(followed, alias) {
			return nil
		}

		followed = append(followed, alias)

		target, err := r.Deref(alias)
		if err != nil {
			return nil
		}

		node = target
	}
}

// elementNode returns the element at index of the sequence node holds, or
// nil for any other node, including a typed nil, and for an index the
// sequence does not hold.
func elementNode(node ast.Node, index int) ast.Node {
	seq, ok := astnode.Content(node).(*ast.SequenceNode)
	if !ok || index < 0 || index >= len(seq.Values) {
		return nil
	}

	return seq.Values[index]
}

// memberIndex finds the members of the mappings that [sourcePath] steps
// through to spell a violation's path, and that [normalizeJSON] steps
// through to find the scalar a timestamp came from. It holds the member
// table of each mapping it has read, so each key decodes once however
// many lookups pass through its mapping or merge it in. The resolver
// binds the aliases of the document. The finder tells which entry a
// spelling selects, and weighs the merge reads of every walk together
// against the limit behind [paths.ErrExcessiveMerging].
//
// Create instances with [newMemberIndex].
type memberIndex struct {
	resolver *paths.Resolver
	finder   *paths.EntryFinder
	members  map[ast.Node]memberTable
}

// memberTable holds the members of one mapping, by the name a decode
// gives each key. It is complete when it names every member the decode
// keeps, so a member of an earlier mapping entry holds the value the
// decode keeps whenever the table leaves its name out.
type memberTable struct {
	members  map[string]memberNode
	complete bool
}

// newMemberIndex creates a new [*memberIndex] that follows aliases
// through r.
func newMemberIndex(r *paths.Resolver) *memberIndex {
	return &memberIndex{resolver: r, finder: r.EntryFinder(), members: map[ast.Node]memberTable{}}
}

// lookup returns the member [memberIndex.memberNodes] finds for name in
// the mapping node holds, or a member with nil nodes when it finds none.
func (idx *memberIndex) lookup(node ast.Node, name string) memberNode {
	return idx.memberNodes(node).members[name]
}

// selects reports whether a path selector with name selects the entry
// that sets member in the mapping node holds, as the finder of idx
// reports. It reports false for a member with no entry, and where the
// finder returns an error, such as [paths.ErrExcessiveMerging] once its
// lookups pass that limit.
func (idx *memberIndex) selects(node ast.Node, name string, member memberNode) bool {
	entry, err := idx.finder.Entry(node, name)

	return err == nil && member.entry != nil && entry == member.entry
}

// memberNode holds the entry that sets a mapping member, with the value
// node of that entry.
type memberNode struct {
	entry *ast.MappingValueNode
	value ast.Node
}

// memberNodes returns the members of the mapping node holds, by the name
// a decode gives each key, or an empty table for any other node. A decode
// sets the members in order, so where several members decode to one
// name, the table holds the last, which is the member whose value the
// decode keeps.
//
// A merge key sets each member its sources define, and the table holds
// the member a source gives that name. A merge key whose sources do not
// resolve, or that lead back to the mapping, may set a member of any
// name, so the table leaves out every member before it.
//
// An alias key decodes to the name the content of its anchor gives. A
// key with no name, such as an alias key [aliasKeyName] cannot name or a
// typed-nil key a tree built by hand may hold, may set a member of any
// name. The table leaves out every member before such a key rather than
// hold one the key may have replaced.
func (idx *memberIndex) memberNodes(node ast.Node) memberTable {
	node = astnode.Content(node)

	if table, ok := idx.members[node]; ok {
		return table
	}

	// A merge source that leads back to node reads this incomplete table.
	idx.members[node] = memberTable{}

	table := memberTable{members: map[string]memberNode{}, complete: true}
	members := mappingMembers(node)

	for _, member := range slices.Backward(members) {
		// A tree built by hand may hold a nil member, which sets nothing.
		if member == nil {
			continue
		}

		if isMergeKey(member.Key) {
			if !idx.addMerged(table.members, member) {
				table.complete = false

				break
			}

			continue
		}

		var (
			name string
			ok   bool
		)

		if _, isAlias := astnode.Content(member.Key).(*ast.AliasNode); isAlias {
			name, ok = aliasKeyName(idx.resolver, member.Key)
		} else {
			name, ok = decodedKey(member.Key)
		}

		if !ok {
			table.complete = false

			break
		}

		if _, seen := table.members[name]; !seen {
			table.members[name] = memberNode{entry: member, value: member.Value}
		}
	}

	idx.members[node] = table

	return table
}

// addMerged adds to found each member the sources of the merge key of
// member define, for a name found does not hold yet. A later source wins
// over an earlier one, as it does in a decode. It reports false when the
// sources do not resolve, or when the table of one of them is not
// complete.
func (idx *memberIndex) addMerged(found map[string]memberNode, member *ast.MappingValueNode) bool {
	sources, err := idx.resolver.MergeSources(&ast.MappingNode{
		Values: []*ast.MappingValueNode{member},
	})
	if err != nil {
		return false
	}

	for _, src := range slices.Backward(sources) {
		table := idx.memberNodes(src)
		if !table.complete {
			return false
		}

		for name, m := range table.members {
			if _, seen := found[name]; !seen {
				found[name] = m
			}
		}
	}

	return true
}

// aliasKeyName returns the member name a decode gives an alias key. That is
// the name [decodedKey] gives the content of the anchor the alias refers
// to, which r resolves. It reports false for an alias that does not resolve
// and for content with no name. It also reports false for an alias under a
// tag or an anchor of the key's own, which may change the name.
func aliasKeyName(r *paths.Resolver, key ast.MapKeyNode) (string, bool) {
	var node ast.Node = key

	if explicit, ok := node.(*ast.MappingKeyNode); ok && explicit != nil {
		node = explicit.Value
	}

	alias, ok := node.(*ast.AliasNode)
	if !ok || alias == nil {
		return "", false
	}

	target, err := r.Deref(alias)
	if err != nil {
		return "", false
	}

	content, ok := target.(ast.MapKeyNode)
	if !ok {
		return "", false
	}

	return decodedKey(content)
}

// mappingMembers returns the members of the mapping node holds, or nil
// for any other node, including a typed nil.
func mappingMembers(node ast.Node) []*ast.MappingValueNode {
	switch n := astnode.Content(node).(type) {
	case *ast.MappingNode:
		return n.Values
	case *ast.MappingValueNode:
		return []*ast.MappingValueNode{n}
	}

	return nil
}

// decodedKey returns the member name a decode gives the key node, with
// the key's tag applied, and reports whether the key has a name. A
// string key gives its unquoted text, and any other scalar gives its Go
// value as the decoder spells it. The hexadecimal key 0x10 reads as 16,
// !!bool yes reads as true, and a !!timestamp key reads as its time
// value. A null key reads as null in every spelling. A merge key has no
// name, because the decoder folds its value into the mapping. A key that
// is no scalar, such as a sequence, has no name either, and neither does
// a key the decoder cannot read on its own, such as an alias. A nil key,
// including a typed nil a tree built by hand may hold, has no name.
func decodedKey(key ast.MapKeyNode) (string, bool) {
	if _, ok := astnode.Content(key).(ast.ScalarNode); !ok || isMergeKey(key) {
		return "", false
	}

	var v any

	err := yaml.NodeToValue(key, &v)
	if err != nil {
		return "", false
	}

	return mapItemKey(v), true
}

// isMergeKey reports whether key is a `<<` merge key, looking through the
// `?` indicator, anchors, and tags. A nil key, including a typed nil, is
// not a merge key.
func isMergeKey(key ast.Node) bool {
	_, ok := astnode.Content(key).(*ast.MergeKeyNode)

	return ok
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
func normalizeJSON(data any, root ast.Node, idx *memberIndex) any {
	w := normalizer{
		idx:   idx,
		nodes: []ast.Node{deref(idx.resolver, root)},
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
	idx  *memberIndex
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
			key := mapItemKey(item.Key)
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
			next = elementNode(parent, seg.Index)
		} else {
			next = w.idx.lookup(parent, seg.Key).value
		}

		w.nodes = append(w.nodes, deref(w.idx.resolver, next))
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

// mapItemKey returns the member name a decode into a map gives a key it
// reads as the Go value key, such as the key of a [yaml.MapItem] or the
// value [decodedKey] reads from a key node. A string key gives its text,
// and any other key gives its printed Go value. A nil key gives null
// rather than the <nil> it would print as.
func mapItemKey(key any) string {
	switch k := key.(type) {
	case nil:
		return "null"
	case string:
		return k
	default:
		return fmt.Sprint(k)
	}
}

// checkExpansion returns an error wrapping [ErrValidate] when a map or
// slice in data contains itself. It returns one wrapping both
// [ErrValidate] and [ErrExcessiveAliasing] when aliases make up too much
// of the data the validator would read.
func checkExpansion(data any) error {
	w := expansionWalker{sizes: map[sharedKey]int{}, onPath: map[sharedKey]bool{}}

	_, err := w.walk(data)
	if err != nil {
		return err
	}

	if aliaslimit.Excessive(w.distinct, w.aliased) {
		return fmt.Errorf("%w: %w", ErrValidate, ErrExcessiveAliasing)
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
			// The key is a node of its own, and mapItemKey prints a key
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
