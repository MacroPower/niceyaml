package schema

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"math"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"go.jacobcolvin.com/x/jsonschema"

	"go.jacobcolvin.com/niceyaml"
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
	// share them. [Schema.Validate] and [Schema.ValidateValue] return it
	// wrapped together with [ErrValidate].
	ErrExcessiveAliasing = errors.New("excessive aliasing")

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
	cfg := newCompileConfig(opts)

	compiled, err := jsonschema.CompileJSON(ctx, data, cfg.jsonOpts...)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCompile, err)
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
// location for [go.jacobcolvin.com/niceyaml/printer.Printer] to display.
// [Schema.Validate] checks a node, which is the whole document for the
// root [niceyaml.Node] of a document and one value inside it
// for a Node from [niceyaml.Node.At], and [Schema.ValidateValue] checks
// decoded data, such as one value taken from a document with a scoped
// [niceyaml.Node.Decode] into any.
//
// A Schema is the validator for a program that holds one schema and
// compiles it itself. It is also a [Resolver] that names itself for every
// document, so one goes into a [Registry] as it is, on its own or behind
// a [When] guard, and the registry validates with it without loading or
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
// [niceyaml.Node.Decode] and checks the result as [Schema.ValidateValue]
// does, so [niceyaml.WithValidator] runs the schema before a decode, a
// [niceyaml.Decoder] runs it on every node it decodes, and
// [niceyaml.Node.Validate] runs it on its own. A Node from
// [niceyaml.Node.At] decodes to the node it selects, so the schema checks
// that node and a violation's path resolves from it. A decoding error
// comes back bound to the source, and a violation as an unbound
// [*niceyaml.Error], which the node binds.
//
// Holding the node lets Validate spell each key in a violation's path as
// the source does, so a key the decoder respells, such as the hexadecimal
// 0x10 for the member name 16, still names its member.
//
// The node also shows which !!timestamp values the source wrote as a bare
// date. Validate hands the schema each of those as an RFC 3339 full-date,
// so !!timestamp 2001-12-14 matches format "date" as the untagged
// 2001-12-14 does. Where Validate cannot tell which scalar a timestamp
// came from, the timestamp keeps the date-time spelling that
// [Schema.ValidateValue] gives it. That holds behind an alias. It also
// holds under a mapping, at any depth, with a merge key, an alias key, or
// a key that is no scalar at or after the member leading to the
// timestamp. Such a key may set a member of the same name.
func (s *Schema) Validate(ctx context.Context, n *niceyaml.Node) error {
	// A decode into any yields only the YAML built-in types, none of which
	// validates itself, so the self-validation walk would find nothing.
	data, err := n.Decode[any](ctx, niceyaml.WithSelfValidation(false))
	if err != nil {
		return err
	}

	return s.validate(ctx, data, n)
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
// each carry the path to one failing location. Any other failure wraps
// [ErrValidate], including a $ref the validator cannot resolve, since no
// location in the document is at fault for that.
//
// ValidateValue rejects two shapes of data before checking anything. A
// value whose shared maps, slices, or byte slices would expand past the
// alias limit, as YAML aliases make them, returns an error wrapping both
// [ErrValidate] and [ErrExcessiveAliasing]. The limit follows the rule
// gopkg.in/yaml.v3 applies to the share of aliased nodes in a document,
// and a []byte counts as one node per character of its base64 text.
// Where yaml.v3 applies the rule node by node as it decodes,
// ValidateValue applies it once to the whole value and counts each use
// of an aliased scalar other than a !!binary as an unaliased node, so it
// accepts some documents yaml.v3 rejects. A map or slice that contains
// itself returns an error wrapping [ErrValidate].
//
// The context reaches the underlying [jsonschema.Validator], where remote
// reference resolution honors its cancellation and deadlines.
func (s *Schema) ValidateValue(ctx context.Context, data any) error {
	return s.validate(ctx, data, nil)
}

// validate is [Schema.ValidateValue] with the node data was decoded from,
// which [Schema.Validate] has and a caller of ValidateValue does not. A
// violation at a key the decoder respells, such as the hexadecimal 0x10,
// needs the node to spell the key in its path as the source does, and a
// !!timestamp needs it to show whether the source wrote only a date.
func (s *Schema) validate(ctx context.Context, data any, n *niceyaml.Node) error {
	// Both normalizeJSON and the validator read a shared value again at
	// every use, so the check runs before either of them.
	err := checkExpansion(data)
	if err != nil {
		return err
	}

	err = s.compiled.Validate(ctx, normalizeJSON(data, rootOf(n)))
	if err == nil {
		return nil
	}

	// A structured validation failure carries per-location paths; convert it to
	// a niceyaml.Error. Anything else is an unexpected internal failure.
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

	return newValidationError(ve, n)
}

// unresolvedRefs returns the failures in the tree of ve that report a $ref
// or $dynamicRef the validator could not resolve, whether a resolver
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

// newValidationError converts a [*jsonschema.ValidationError] into a
// [*niceyaml.Error].
//
// The conversion flattens the error tree to its concrete failures with
// [jsonschema.ValidationError.Leaves]. A single failure becomes the main
// error, carrying its own path so the printer highlights that location and
// [niceyaml.Error.Path] reports it. Several failures become a count summary
// with no path of its own; each nested error carries the path to one
// failing location.
func newValidationError(ve *jsonschema.ValidationError, n *niceyaml.Node) *niceyaml.Error {
	leaves := ve.Leaves()

	// Bind aliases across the whole document, so an alias inside a
	// scoped node reaches an anchor outside it.
	var targets map[*ast.AliasNode]ast.Node

	if n != nil {
		targets = aliasTargets(n.DocumentAST())
	}

	switch len(leaves) {
	case 0:
		return niceyaml.NewError(ve.Message)
	case 1:
		return leafError(leaves[0], n, targets)
	}

	causes := make([]error, 0, len(leaves))
	for _, leaf := range leaves {
		causes = append(causes, leafError(leaf, n, targets))
	}

	return niceyaml.NewError(
		fmt.Sprintf("%d schema violations", len(leaves)),
		niceyaml.WithErrors(causes...),
	)
}

// leafError converts one concrete failure into a [*niceyaml.Error] carrying
// the YAML path to the failing location. A failure that constrains the key
// of a member, such as an additional property, points at the key through
// [paths.Path.Key], and any other at the value.
//
// The path spells each key as the source does, so a key the decoder
// respells, such as 0x10 for the member name 16, still names its member.
// Without n, which a [Schema.ValidateValue] caller does not hand over, the
// path spells each key as the decoder does. The targets map binds each
// alias in the document of n to the content it refers to.
func leafError(
	leaf *jsonschema.ValidationError,
	n *niceyaml.Node,
	targets map[*ast.AliasNode]ast.Node,
) *niceyaml.Error {
	path := sourcePath(rootOf(n), targets, leaf.InstanceSegments())

	if leaf.TargetsKey() {
		path = path.Key()
	}

	return niceyaml.NewError(leaf.Message, niceyaml.AtPath(path))
}

// rootOf returns the tree of n, or nil for no node and for a node whose
// tree does not resolve.
func rootOf(n *niceyaml.Node) ast.Node {
	if n == nil {
		return nil
	}

	root, err := n.AST()
	if err != nil {
		return nil
	}

	return root
}

// sourcePath converts instance-location segments to a [paths.Path] that
// resolves in root. Each [jsonschema.Segment] already distinguishes an
// array index from a property name, so sourcePath does no numeric
// guessing.
//
// A property name is the name the decoder produced, which the source may
// spell another way, as it spells the member name 16 as 0x10. The walk
// down root matches each mapping key by its decoded name and writes the
// source spelling of that key into the path instead, which a path
// selector matches.
//
// The walk follows an alias to the content targets binds it to, the last
// anchor of its name before it in the document, as the paths package
// does. A key under an aliased mapping keeps its source spelling too. A
// segment the walk cannot follow keeps its decoded name, such as a member
// a merge key brought in or one behind an alias targets does not bind.
// Every segment keeps its decoded name when root is nil, as does a key
// the walk finds but cannot spell.
func sourcePath(
	root ast.Node,
	targets map[*ast.AliasNode]ast.Node,
	segments []jsonschema.Segment,
) paths.Path {
	path := paths.Root()
	node := followAliases(root, targets)

	for _, seg := range segments {
		if seg.IsIndex {
			path = path.Index(seg.Index)
			node = followAliases(elementNode(node, seg.Index), targets)

			continue
		}

		name := seg.Key

		keyNode, valueNode := memberNodes(node, seg.Key)
		if spelled := sourceKey(keyNode); spelled != "" {
			name = spelled
		}

		path = path.Child(name)
		node = followAliases(valueNode, targets)
	}

	return path
}

// aliasTargets returns the content each alias in doc refers to, which is
// the content of the last anchor of its name before the alias, the anchor
// the decoder uses for it. It returns nil for no document and for one
// with no body.
func aliasTargets(doc *ast.DocumentNode) map[*ast.AliasNode]ast.Node {
	if doc == nil || doc.Body == nil {
		return nil
	}

	b := &aliasBinder{
		anchors: map[string]ast.Node{},
		targets: map[*ast.AliasNode]ast.Node{},
	}

	ast.Walk(b, doc.Body)

	return b.targets
}

// aliasBinder binds aliases to anchors while [ast.Walk] visits a document
// in order. The anchors map holds the content of the last anchor of each
// name visited so far, and the targets map holds the content each visited
// alias refers to.
type aliasBinder struct {
	anchors map[string]ast.Node
	targets map[*ast.AliasNode]ast.Node
}

// Visit records an anchor or binds an alias, then returns b so [ast.Walk]
// continues into the children of node. It returns nil for a nil node and
// for a typed nil anchor or alias, so Walk stops rather than reading the
// fields behind it.
func (b *aliasBinder) Visit(node ast.Node) ast.Visitor {
	switch n := node.(type) {
	case nil:
		return nil

	case *ast.AnchorNode:
		if n == nil {
			return nil
		}

		if name, ok := anchorName(n.Name); ok {
			b.anchors[name] = n.Value
		}

	case *ast.AliasNode:
		if n == nil {
			return nil
		}

		name, ok := anchorName(n.Value)
		if !ok {
			return b
		}

		if target, ok := b.anchors[name]; ok {
			b.targets[n] = target
		}
	}

	return b
}

// anchorName returns the name that the name node of an anchor or an alias
// spells, and reports whether the node has one. A nil node, and one with
// no token, has no name.
func anchorName(node ast.Node) (string, bool) {
	if node == nil {
		return "", false
	}

	tk := node.GetToken()
	if tk == nil {
		return "", false
	}

	return tk.Value, true
}

// followAliases looks through node, and through each alias it reaches, to
// the content targets binds the alias to. It stops at an alias with no
// target, and at one it has already followed, so an alias that leads back
// to itself ends the walk rather than looping.
func followAliases(node ast.Node, targets map[*ast.AliasNode]ast.Node) ast.Node {
	followed := map[*ast.AliasNode]bool{}

	for {
		node = contentNode(node)

		alias, ok := node.(*ast.AliasNode)
		if !ok || alias == nil || followed[alias] {
			return node
		}

		target, ok := targets[alias]
		if !ok {
			return node
		}

		followed[alias] = true
		node = target
	}
}

// elementNode returns the element at index of the sequence node holds, or
// nil for any other node and for an index the sequence does not hold. A
// tree built by hand may hold a typed nil where the parser always puts a
// node, which holds no element either.
func elementNode(node ast.Node, index int) ast.Node {
	seq, ok := contentNode(node).(*ast.SequenceNode)
	if !ok || seq == nil || index < 0 || index >= len(seq.Values) {
		return nil
	}

	return seq.Values[index]
}

// memberNodes returns the key and value nodes of the member whose key
// decodes to name, or nil nodes when the node is no mapping or holds no
// such member. When several members decode to name, memberNodes returns
// the last, which is the member whose value the decode keeps.
func memberNodes(node ast.Node, name string) (ast.Node, ast.Node) {
	for _, member := range slices.Backward(mappingMembers(node)) {
		if key, ok := decodedKey(member.Key); ok && key == name {
			return member.Key, member.Value
		}
	}

	return nil, nil
}

// keptMembers returns the value node of each member of the mapping node
// holds, by the name [decodedKey] gives its key, or an empty map for any
// other node. A decode sets the members in order, so where two members
// share a name, the map holds the value of the later one, which the decode
// keeps. A key with no name, such as a merge key or an alias, may set a
// member of any name, so the map leaves out every member before the last
// such key.
func keptMembers(node ast.Node) map[string]ast.Node {
	kept := map[string]ast.Node{}

	for _, member := range slices.Backward(mappingMembers(node)) {
		name, ok := decodedKey(member.Key)
		if !ok {
			break
		}

		if _, found := kept[name]; !found {
			kept[name] = member.Value
		}
	}

	return kept
}

// mappingMembers returns the members of the mapping node holds, or nil
// for any other node. A tree built by hand may hold a typed nil where the
// parser always puts a node, which holds no member either.
func mappingMembers(node ast.Node) []*ast.MappingValueNode {
	switch n := contentNode(node).(type) {
	case *ast.MappingNode:
		if n != nil {
			return n.Values
		}

	case *ast.MappingValueNode:
		if n != nil {
			return []*ast.MappingValueNode{n}
		}
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
// a key the decoder cannot read on its own, such as an alias.
func decodedKey(key ast.MapKeyNode) (string, bool) {
	if _, ok := contentNode(key).(ast.ScalarNode); !ok || key.IsMergeKey() {
		return "", false
	}

	var v any

	err := yaml.NodeToValue(key, &v)
	if err != nil {
		return "", false
	}

	switch k := v.(type) {
	case nil:
		// A nil value prints as "<nil>" rather than the "null" the decoder
		// names the member by.
		return "null", true
	case string:
		return k, true
	default:
		return fmt.Sprint(k), true
	}
}

// sourceKey returns the name a path selector matches the key node by,
// which is the source spelling of the key. A string key gives its
// unquoted text, a block scalar key gives its content, and any other key
// gives its token text, so the hexadecimal key 0x10 reads as 0x10. A key
// with no content, or with no token, has no name.
func sourceKey(key ast.Node) string {
	switch k := contentNode(key).(type) {
	case nil:
		return ""
	case *ast.StringNode:
		return k.Value
	case *ast.LiteralNode:
		if k.Value == nil {
			return ""
		}

		return k.Value.Value

	default:
		tk := k.GetToken()
		if tk == nil {
			return ""
		}

		return tk.Value
	}
}

// contentNode looks through the nodes that wrap a value, so the walk sees
// the mapping or sequence behind a document, an anchor, or a tag.
func contentNode(node ast.Node) ast.Node {
	// A tree built by hand may hold a typed nil where the parser always
	// puts a node; such a wrapper holds no content and comes back as it is.
	for {
		switch n := node.(type) {
		case *ast.DocumentNode:
			if n == nil {
				return nil
			}

			node = n.Body

		case *ast.AnchorNode:
			if n == nil {
				return nil
			}

			node = n.Value

		case *ast.TagNode:
			if n == nil {
				return nil
			}

			node = n.Value

		case *ast.MappingKeyNode:
			if n == nil {
				return nil
			}

			node = n.Value

		default:
			return node
		}
	}
}

// normalizeJSON converts the YAML-native values a decode produces that the
// JSON Schema validator does not accept into the JSON spellings of the
// same data. A !!binary becomes its base64 text. A !!timestamp becomes an
// RFC 3339 full-date, such as 2001-12-14, when the scalar in root it was
// decoded from holds only a date, and an RFC 3339 date-time otherwise.
// Root is the node data was decoded from, and without it every timestamp
// becomes a date-time. A [yaml.MapSlice], which a decode with
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
func normalizeJSON(data any, root ast.Node) any {
	w := normalizer{
		members: map[ast.Node]map[string]ast.Node{},
		nodes:   []ast.Node{root},
	}

	out, _ := w.normalize(data)

	return out
}

// normalizer walks a value for [normalizeJSON]. It records the path from
// the top of the value down to the value it visits, and looks up the node
// in root that the path leads to only when it reaches a timestamp. It
// keeps each node it finds while the walk stays under that node, and it
// keeps the named members of each mapping it reads. So the lookups step
// down root at most once for each value the walk visits, and read each
// mapping at most once.
type normalizer struct {
	// The result of [keptMembers] for each mapping node the lookups have
	// stepped through.
	members map[ast.Node]map[string]ast.Node
	path    []jsonschema.Segment
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

// node returns the node in root that the visited value was decoded from.
// It returns nil when root is nil and when no node in root holds the
// value, as under an alias, whose content sits under its anchor, and at a
// member name [keptMembers] leaves out.
func (w *normalizer) node() ast.Node {
	for len(w.nodes) <= len(w.path) {
		parent := w.nodes[len(w.nodes)-1]
		seg := w.path[len(w.nodes)-1]

		var next ast.Node

		if seg.IsIndex {
			next = elementNode(parent, seg.Index)
		} else {
			next = w.member(parent, seg.Key)
		}

		w.nodes = append(w.nodes, next)
	}

	return w.nodes[len(w.path)]
}

// member returns the value node that [keptMembers] finds for name in the
// mapping node holds, or nil when it finds none.
func (w *normalizer) member(node ast.Node, name string) ast.Node {
	node = contentNode(node)

	members, ok := w.members[node]
	if !ok {
		members = keptMembers(node)
		w.members[node] = members
	}

	return members[name]
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
// its content without the header, as a decode reads it. A tree built by
// hand may hold a typed nil where the parser always puts a node, which
// holds no text.
func stringText(node ast.Node) (string, bool) {
	switch n := contentNode(node).(type) {
	case *ast.StringNode:
		if n != nil {
			return n.Value, true
		}

	case *ast.LiteralNode:
		if n != nil && n.Value != nil {
			return n.Value.Value, true
		}
	}

	return "", false
}

// mapItemKey returns the member name a decode into a map gives the key of
// a [yaml.MapItem]: null for a nil key, the text of a string key, and the
// printed Go value of any other key.
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

// The limits on shared values follow the rule gopkg.in/yaml.v3 applies
// to the aliases in a document it decodes.
const (
	// A value's aliases count as excessive only once the value passes
	// both of these node counts, for aliased nodes and for all nodes.
	minAliased  = 100
	minExpanded = 1000

	// Aliases may make up the larger share of the nodes in a value of up
	// to the lower count, and the smaller share in a value of the higher
	// count or more. The share falls in a straight line between them.
	maxAliasRatio       = 0.99
	minAliasRatio       = 0.10
	aliasRatioRangeLow  = 400_000
	aliasRatioRangeHigh = 4_000_000

	// A node count stops growing at this cap, far past every limit
	// above, so a chain of nested aliases cannot overflow it.
	countCap = math.MaxInt32
)

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

	expanded := addCapped(w.distinct, w.aliased)
	if w.aliased > minAliased && expanded > minExpanded &&
		float64(w.aliased)/float64(expanded) > allowedAliasRatio(expanded) {
		return fmt.Errorf("%w: %w", ErrValidate, ErrExcessiveAliasing)
	}

	return nil
}

// allowedAliasRatio returns the share of the expanded nodes that aliases
// may make up in a value of expanded nodes.
func allowedAliasRatio(expanded int) float64 {
	switch {
	case expanded <= aliasRatioRangeLow:
		return maxAliasRatio
	case expanded >= aliasRatioRangeHigh:
		return minAliasRatio
	default:
		progress := float64(expanded-aliasRatioRangeLow) / float64(aliasRatioRangeHigh-aliasRatioRangeLow)

		return maxAliasRatio - (maxAliasRatio-minAliasRatio)*progress
	}
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
	switch v := data.(type) {
	case map[string]any:
		if len(v) == 0 {
			return sharedKey{}, false
		}

	case []any:
		if len(v) == 0 {
			return sharedKey{}, false
		}

	case yaml.MapSlice:
		if len(v) == 0 {
			return sharedKey{}, false
		}

	case []byte:
		if len(v) == 0 {
			return sharedKey{}, false
		}

	default:
		return sharedKey{}, false
	}

	rv := reflect.ValueOf(data)

	return sharedKey{typ: rv.Type(), ptr: rv.Pointer(), len: rv.Len()}, true
}

// walk returns the size of data: one for a scalar, one per character of
// the base64 text of a byte slice, and one for a map or slice plus the
// size of each key and value in it. A map or slice that the walk reaches
// from inside itself returns an error wrapping [ErrValidate].
func (w *expansionWalker) walk(data any) (int, error) {
	w.distinct = addCapped(w.distinct, 1)

	key, ok := sharedKeyOf(data)
	if !ok {
		return 1, nil
	}

	if w.onPath[key] {
		return 0, fmt.Errorf("%w: value contains itself", ErrValidate)
	}

	if size, seen := w.sizes[key]; seen {
		w.aliased = addCapped(w.aliased, size)

		return size, nil
	}

	w.onPath[key] = true
	defer delete(w.onPath, key)

	size := 1

	switch v := data.(type) {
	case []byte:
		// The walk counted the first character of the base64 text on
		// entry.
		size = min(base64.StdEncoding.EncodedLen(len(v)), countCap)
		w.distinct = addCapped(w.distinct, size-1)

	case map[string]any:
		for _, elem := range v {
			// The key is a node of its own.
			w.distinct = addCapped(w.distinct, 1)

			n, err := w.walk(elem)
			if err != nil {
				return 0, err
			}

			size = addCapped(size, addCapped(1, n))
		}

	case yaml.MapSlice:
		for _, item := range v {
			// The key is a node of its own.
			w.distinct = addCapped(w.distinct, 1)

			n, err := w.walk(item.Value)
			if err != nil {
				return 0, err
			}

			size = addCapped(size, addCapped(1, n))
		}

	case []any:
		for _, elem := range v {
			n, err := w.walk(elem)
			if err != nil {
				return 0, err
			}

			size = addCapped(size, n)
		}
	}

	w.sizes[key] = size

	return size, nil
}

// addCapped returns a+b, or countCap when the sum would pass it. Both a
// and b fall between zero and countCap.
func addCapped(a, b int) int {
	if a > countCap-b {
		return countCap
	}

	return a + b
}
