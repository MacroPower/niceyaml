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
// needs the node to spell the key in its path as the source does.
func (s *Schema) validate(ctx context.Context, data any, n *niceyaml.Node) error {
	// Both normalizeJSON and the validator read a shared value again at
	// every use, so the check runs before either of them.
	err := checkExpansion(data)
	if err != nil {
		return err
	}

	err = s.compiled.Validate(ctx, normalizeJSON(data))
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
//
// A map or slice that holds no !!binary or !!timestamp comes back as the
// same container, and one that does comes back as a copy, so
// normalizeJSON never writes into the caller's data. Handing the
// validator the caller's own containers is safe because
// [jsonschema.Validator.Validate] only reads its instance and keeps no
// reference to it.
func normalizeJSON(data any) any {
	out, _ := normalize(data)

	return out
}

// normalize is [normalizeJSON] that also reports whether the value it
// returns differs from data.
func normalize(data any) (any, bool) {
	switch v := data.(type) {
	case []byte:
		return base64.StdEncoding.EncodeToString(v), true
	case time.Time:
		return v.Format(time.RFC3339Nano), true
	case map[string]any:
		var out map[string]any

		for key, elem := range v {
			norm, changed := normalize(elem)
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
			norm, changed := normalize(elem)
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

	default:
		return data, false
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
