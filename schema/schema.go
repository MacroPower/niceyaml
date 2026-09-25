package schema

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

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
func WithJSONSchemaOptions(opts ...jsonschema.ValidateOption) CompileOption {
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
func FromJSONSchema(v *jsonschema.Validator) *Schema {
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
func (s *Schema) Validate(ctx context.Context, n *niceyaml.Node) error {
	data, err := n.Decode[any](ctx)
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
// validator does not accept into the JSON spelling of the same data. A
// !!binary becomes its base64 text and a !!timestamp its RFC 3339 text,
// anywhere in the value.
//
// Returns nil when data conforms. On a constraint violation, returns a
// [*niceyaml.Error]. A single violation carries its YAML path on the error
// itself, and several violations become a count summary whose nested errors
// each carry the path to one failing location. Any other failure wraps
// [ErrValidate], including a $ref the validator cannot resolve, since no
// location in the document is at fault for that.
//
// The context reaches the underlying [jsonschema.Validator], where remote
// reference resolution honors its cancellation and deadlines.
func (s *Schema) ValidateValue(ctx context.Context, data any) error {
	return s.validate(ctx, data, nil)
}

// validate is [Schema.ValidateValue] with the node data was decoded from,
// which [Schema.Validate] has and a caller of ValidateValue does not. A
// violation at a key the decoder respells, such as the hexadecimal 0x10,
// needs the node to spell the key in its path as the source does.
func (s *Schema) validate(ctx context.Context, data any, n *niceyaml.Node) error {
	err := s.compiled.Validate(ctx, normalizeJSON(data))
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
// returned an error or no resolver served the reference. A reference that
// resolves reports its target's failures as causes, so a reference keyword
// that is itself a leaf names a target the validator never found.
func unresolvedRefs(ve *jsonschema.ValidationError) []error {
	var errs []error

	for _, leaf := range ve.Leaves() {
		switch leaf.Keyword {
		case jsonschema.KeywordRef, jsonschema.KeywordDynamicRef:
			errs = append(errs, leaf)
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

	switch len(leaves) {
	case 0:
		return niceyaml.NewError(ve.Message)
	case 1:
		return leafError(leaves[0], n)
	}

	causes := make([]error, 0, len(leaves))
	for _, leaf := range leaves {
		causes = append(causes, leafError(leaf, n))
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
// path spells each key as the decoder does.
func leafError(leaf *jsonschema.ValidationError, n *niceyaml.Node) *niceyaml.Error {
	path := sourcePath(rootOf(n), leaf.InstanceSegments())

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
// selector matches. A segment the walk cannot follow, such as a member a
// merge key brought in, keeps its decoded name, as does every segment
// when root is nil and a key the walk finds but cannot spell.
func sourcePath(root ast.Node, segments []jsonschema.Segment) paths.Path {
	path := paths.Root()
	node := root

	for _, seg := range segments {
		if seg.IsIndex {
			path = path.Index(seg.Index)
			node = elementNode(node, seg.Index)

			continue
		}

		name := seg.Key

		keyNode, valueNode := memberNodes(node, seg.Key)
		if spelled := sourceKey(keyNode); spelled != "" {
			name = spelled
		}

		path = path.Child(name)
		node = valueNode
	}

	return path
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
// such member. A tree built by hand may hold a typed nil where the parser
// always puts a node, which holds no member either.
func memberNodes(node ast.Node, name string) (ast.Node, ast.Node) {
	var members []*ast.MappingValueNode

	switch n := contentNode(node).(type) {
	case *ast.MappingNode:
		if n != nil {
			members = n.Values
		}

	case *ast.MappingValueNode:
		if n != nil {
			members = []*ast.MappingValueNode{n}
		}

	default:
		return nil, nil
	}

	for _, member := range members {
		if decodedKey(member.Key) == name {
			return member.Key, member.Value
		}
	}

	return nil, nil
}

// decodedKey returns the member name a decode gives the key node: the
// unquoted text of a string key, and the Go value of any other scalar as
// the decoder spells it, so the hexadecimal key 0x10 reads as 16, and a
// null key, however it is written, reads as null. A key that is no
// scalar, such as a sequence, has no name.
func decodedKey(key ast.MapKeyNode) string {
	switch k := contentNode(key).(type) {
	case *ast.StringNode:
		return k.Value
	case *ast.NullNode:
		// A null node carries a nil value, which prints as "<nil>" rather
		// than the "null" the decoder names the member by.
		return "null"
	case ast.ScalarNode:
		return fmt.Sprint(k.GetValue())
	default:
		return ""
	}
}

// sourceKey returns the name a path selector matches the key node by,
// which is the source spelling of the key: the unquoted text of a string
// key, and the token text of any other key, so the hexadecimal key 0x10
// reads as 0x10. A key with no content, or with no token, has no name.
func sourceKey(key ast.Node) string {
	switch k := contentNode(key).(type) {
	case nil:
		return ""
	case *ast.StringNode:
		return k.Value
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
// same data. A !!binary becomes its base64 text and a !!timestamp its RFC
// 3339 text. It walks maps and slices so a tagged scalar anywhere in a
// document stays validatable. Every other value comes back unchanged,
// non-finite floats included, since the validator treats those as numbers.
func normalizeJSON(data any) any {
	switch v := data.(type) {
	case []byte:
		return base64.StdEncoding.EncodeToString(v)
	case time.Time:
		return v.Format(time.RFC3339Nano)
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, elem := range v {
			out[key] = normalizeJSON(elem)
		}

		return out

	case []any:
		out := make([]any, len(v))
		for i, elem := range v {
			out[i] = normalizeJSON(elem)
		}

		return out

	default:
		return data
	}
}
