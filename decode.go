package niceyaml

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime/debug"
	"slices"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/aliasing"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/datapath"
	"go.jacobcolvin.com/niceyaml/internal/decodewalk"
	"go.jacobcolvin.com/niceyaml/internal/rejection"
	"go.jacobcolvin.com/niceyaml/internal/unmarshaler"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

// DecodeOption configures [Node.Decode], [Node.DecodeInto],
// [Node.DecodeAt], and [Node.DecodeIfPresent], and [Source.Decode] and
// [Source.DecodeInto], which decode the one document of a Source.
// [Node.SelfValidate] and [Source.SelfValidate] take them too, and read
// only the options that say how a value decodes, as Node.SelfValidate
// lists them.
//
// Available options:
//   - [WithValidator]
//   - [WithSelfValidation]
//   - [WithDisallowUnknownFields]
//   - [WithAllowedFieldPrefixes]
//   - [WithCustomUnmarshaler]
//   - [WithJSONUnmarshalers]
//   - [WithYAMLOrderedMaps]
//   - [DecodeOptions]
//
// An option reaches the call that gets it and no other, so a [Validator]
// that decodes the Node it gets passes options of its own. A setting
// that describes the documents, such as [WithReferences] or
// [WithAliasLimit], belongs to the [Source], as the Decoding section of
// the package documentation describes.
type DecodeOption func(*decodeConfig)

// decodeConfig holds the settings a [DecodeOption] configures. Its zero
// value holds the defaults.
type decodeConfig struct {
	validators           []Validator
	allowedFieldPrefixes []string
	// The types the options hand to an unmarshaler that no method of the
	// type declares.
	unmarshalers          unmarshaler.Options
	skipSelfValidation    bool
	disallowUnknownFields bool
	orderedMaps           bool
}

// newDecodeConfig returns the settings of a decode: the defaults, with
// opts applied over them in order. The result shares nothing with any
// other decode.
func newDecodeConfig(opts []DecodeOption) decodeConfig {
	var cfg decodeConfig

	for _, opt := range opts {
		opt(&cfg)
	}

	return cfg
}

// WithValidator is a [DecodeOption] that validates the document with dv
// before decoding it, and a validation error ends the decode before any
// typed decoding. Each WithValidator option adds a validator. The decode
// runs them as [ChainValidator] runs them, so it runs them in the order
// given and stops at the first that fails. A caller that wants every
// failure of several validators gives one option a [MultiValidator].
// Validation skips a nil dv, and one that holds a nil pointer or func,
// such as a schema a program loads only on some paths. A
// [go.jacobcolvin.com/niceyaml/schema.Schema] checks the document against
// one JSON schema, and a [go.jacobcolvin.com/niceyaml/schema.Registry]
// against the schema it picks for the document:
//
//	config, err := doc.Decode[Config](ctx, niceyaml.WithValidator(reg))
//
// An empty document validates as any other. [SkipEmpty] wraps a
// validator that should pass one, so the decode returns the zero value.
func WithValidator(dv Validator) DecodeOption {
	return func(c *decodeConfig) {
		c.validators = append(c.validators, dv)
	}
}

// WithSelfValidation is a [DecodeOption] that sets whether the values
// in a decoded value that implement [SelfValidator] validate themselves
// after decoding. The default is true. Validators given with
// [WithValidator] run either way. [Node.SelfValidate] and
// [Source.SelfValidate] run the walk whatever the option says, so a
// caller that turns it off for the decode validates the value later.
func WithSelfValidation(enabled bool) DecodeOption {
	return func(c *decodeConfig) {
		c.skipSelfValidation = !enabled
	}
}

// WithDisallowUnknownFields is a [DecodeOption] that sets whether a
// mapping key with no field in the target struct is an error. The default
// is false, and the decode then skips unknown keys.
//
// With the option on, a decode that fails reports every unknown field the
// document holds, in the order of the source, each at the path of its key
// and matching [ErrUnknownField] beside [ErrDecode]:
//
//	cfg.yaml: 2 unknown fields
//	cfg.yaml:3:1: $.replicsa~: unknown field "replicsa"
//	cfg.yaml:7:5: $.servers[0].prot~: unknown field "prot"
//
// The fields join the other problems of the decode, as the Decoding
// section of the package documentation describes, and [errors.As] finds
// the [yaml.UnknownFieldError] of each. The report holds only keys the
// go-yaml decoder rejects, so a key under a prefix that
// [WithAllowedFieldPrefixes] allows stays out of it. The decoder rejects
// no key below a field of an interface type, in a mapping that holds a
// key it does not read as a string, or in a mapping whose `<<` merge key
// it refuses. A struct that decodes itself decides for its own fields. An
// UnmarshalYAML that decodes into a second type with the same fields has
// them checked, and one that parses the text itself does not.
//
// The report misses an unknown field below a struct that decodes itself
// into fields that do not mirror the document, unless the decoder names
// it. It can also report a key that a function from
// [yaml.RegisterCustomUnmarshaler] accepts. A function given to
// [WithCustomUnmarshaler] has no such limit.
func WithDisallowUnknownFields(disallow bool) DecodeOption {
	return func(c *decodeConfig) {
		c.disallowUnknownFields = disallow
	}
}

// WithAllowedFieldPrefixes is a [DecodeOption] that lets a mapping hold
// a key that starts with one of prefixes where
// [WithDisallowUnknownFields] rejects a key with no field, such as the
// `x-` keys a format reserves for extensions:
//
//	spec, err := doc.Decode[Spec](ctx,
//		niceyaml.WithDisallowUnknownFields(true),
//		niceyaml.WithAllowedFieldPrefixes("x-"),
//	)
//
// Each WithAllowedFieldPrefixes adds to the prefixes given before it. An
// empty prefix allows every key. The option changes nothing in a decode
// that accepts unknown fields.
func WithAllowedFieldPrefixes(prefixes ...string) DecodeOption {
	return func(c *decodeConfig) {
		c.allowedFieldPrefixes = append(c.allowedFieldPrefixes, prefixes...)
	}
}

// WithCustomUnmarshaler is a [DecodeOption] that decodes every value of
// type T with fn, for a type the program cannot give an UnmarshalYAML
// method, such as one of another package. The decoder calls fn in place
// of decoding the fields, elements, or entries of T, and ahead of any
// unmarshaler method T has. A field of type *T decodes through fn too,
// and a null leaves it nil without a call. The option below decodes a
// [net.IPNet] from its CIDR text:
//
//	cidr := niceyaml.WithCustomUnmarshaler(func(ctx context.Context, n *net.IPNet, decode func(any) error) error {
//		var s string
//		if err := decode(&s); err != nil {
//			return err
//		}
//
//		_, parsed, err := net.ParseCIDR(s)
//		if err != nil {
//			return err
//		}
//
//		*n = *parsed
//
//		return nil
//	})
//
// The function gets the context of the decode and a decode function. The
// decode function decodes the value with the options of the decode that
// called fn, reads an alias as the content of its anchor, and runs no
// [Validator]. It returns an error for a pointer to T, which would call
// fn again, so a function that decodes T by its fields decodes into a
// second type with those fields, such as `type fields T`. A function that
// needs the YAML text of the value decodes into a [yaml.RawMessage].
//
// The decode binds an error fn returns at the value, and an `@` path in
// an [*Error] from fn reads from the value. A rejection from the decode
// function already names its place in the document, so fn returns it as
// it is. The decode reads T as a type that decodes itself, as the
// Decoding and Validation sections of the package documentation describe.
//
// A later option for the same T replaces an earlier one, and a nil fn or
// a pointer type T adds nothing. Give the function here rather than to
// [yaml.RegisterCustomUnmarshaler], whose types a decode cannot see.
func WithCustomUnmarshaler[T any](fn func(ctx context.Context, v *T, decode func(any) error) error) DecodeOption {
	return func(c *decodeConfig) {
		t := reflect.TypeFor[T]()
		if fn == nil || t.Kind() == reflect.Pointer {
			return
		}

		c.unmarshalers = c.unmarshalers.With(t, func(decode unmarshaler.ValueDecoder) yaml.DecodeOption {
			return yaml.CustomUnmarshalerContext(func(ctx context.Context, v *T, text []byte) error {
				err := fn(ctx, v, func(dst any) error {
					// A decode into T would call fn for the same value again,
					// and so on without end.
					if dt := reflect.TypeOf(dst); dt != nil && unmarshaler.Base(dt) == t {
						return fmt.Errorf("decode target is the type the function decodes: got %T", dst)
					}

					return decode(ctx, text, dst)
				})
				if err != nil {
					return shieldedError{err: err}
				}

				return nil
			})
		})
	}
}

// shieldedError holds the error a [WithCustomUnmarshaler] function
// returned while the go-yaml decoder carries it. The decoder searches
// the error of a struct field for a [yaml.TypeError] with [errors.As],
// and returns that TypeError alone in place of the error it found it in.
// An error that wraps one, as the rejection of a decode callback does,
// would thus lose its message and its path. A shieldedError unwraps to
// nothing, so that search ends at it, and [decodeWithRecover] takes the
// error out again.
//
// An unmarshaler above the value can still wrap a shieldedError in an
// error of its own. The shieldedError then stays in the chain, where it
// matches what its error matches, apart from a TypeError. A decode reads
// no location below it, so the wrapped error points at the value of the
// unmarshaler that wrapped it.
type shieldedError struct {
	err error
}

func (e shieldedError) Error() string {
	return e.err.Error()
}

// Is reports whether the error e holds matches target.
func (e shieldedError) Is(target error) bool {
	return errors.Is(e.err, target)
}

// As sets target to the first error in the chain of the error e holds
// that matches it, as [errors.As] does, and finds no [yaml.TypeError].
func (e shieldedError) As(target any) bool {
	if _, ok := target.(**yaml.TypeError); ok {
		return false
	}

	return errors.As(e.err, target)
}

// WithJSONUnmarshalers is a [DecodeOption] that sets whether a type with
// an UnmarshalJSON method decodes through that method. The default is
// false, and the decode then reads such a type by its fields, elements,
// or entries, as it reads a type with no method.
//
// When enabled, the decoder converts the node to JSON, with each alias
// in the node written out in full, and hands the JSON to the method. It
// calls an UnmarshalYAML method of the type in place of UnmarshalJSON,
// and an UnmarshalText method for a node that reads as a string.
//
// The decode then reads such a type as one that decodes itself. The
// values below a value of the type thus validate as [SelfValidator]
// describes, and the error its method returns binds at the value, as
// [Node.DecodeInto] describes. A struct that gets the method from an
// embedded field decodes the document into that field, so the field
// validates at the path of the struct.
func WithJSONUnmarshalers(enabled bool) DecodeOption {
	return func(c *decodeConfig) {
		c.unmarshalers.JSON = enabled
	}
}

// WithYAMLOrderedMaps is a [DecodeOption] that sets whether a mapping
// that decodes into an any value becomes a [yaml.MapSlice], which holds
// the entries in the order of the document. The default is false, and
// such a mapping then becomes a map[string]any. The option carries the
// YAML prefix because the decoded value holds a go-yaml type.
//
//	data, err := doc.Decode[any](ctx, niceyaml.WithYAMLOrderedMaps(true))
//
// A target of a typed map or a struct decodes as it does without the
// option. A [go.jacobcolvin.com/niceyaml/schema.Schema] reads a MapSlice
// as the mapping it holds, so
// [go.jacobcolvin.com/niceyaml/schema.Schema.ValidateValue] checks the
// decoded value as it is.
func WithYAMLOrderedMaps(enabled bool) DecodeOption {
	return func(c *decodeConfig) {
		c.orderedMaps = enabled
	}
}

// DecodeOptions is a [DecodeOption] that applies opts in order, so a
// program that decodes many documents names its settings in one place.
// A call applies the result as it applies opts written in its place:
//
//	settings := niceyaml.DecodeOptions(niceyaml.WithDisallowUnknownFields(true))
//	strict := niceyaml.DecodeOptions(niceyaml.WithValidator(reg), settings)
//
//	manifest, err := doc.Decode[Manifest](ctx, strict)
//	kind, err := doc.DecodeAt[string](ctx, kindPath, settings)
//
// A validator among opts checks the node each call decodes. For
// [Node.DecodeAt], [Node.DecodeIfPresent], and a Node from [Node.At] or
// [Node.Nodes], that node is one value inside the document, so those
// calls take the settings without the schema of the whole document.
//
// [WithValidator] and [WithAllowedFieldPrefixes] add to what the options
// before them gave, and [WithCustomUnmarshaler] adds a type. Every other
// option replaces what an earlier one of its kind set. A call that gets
// the same DecodeOptions twice thus runs each of its validators twice.
//
// DecodeOptions copies opts, so the result keeps its options when the
// caller changes the slice it passed. A nil DecodeOption among opts
// panics in the call that applies the result.
func DecodeOptions(opts ...DecodeOption) DecodeOption {
	opts = slices.Clone(opts)

	return func(c *decodeConfig) {
		for _, opt := range opts {
			opt(c)
		}
	}
}

// DecodeInto validates the node, decodes it into v, and validates the
// result. The node is the whole document for the root Node of a
// document. A program that decodes many nodes with the same options
// states them once with [DecodeOptions].
//
// The steps run in the order the Decoding section of the package
// documentation lists, and the first that fails ends the decode. A v that
// is not a non-nil pointer returns an error wrapping [ErrDecodeTarget],
// bound to the source, before anything runs. A document that did not
// parse then returns the syntax error [Node.Err] returns. Each [Validator]
// from [WithValidator] runs next, in the order given. When the node holds
// an alias, the decode counts what a decode of the whole document reads,
// with each alias reading its content in full, and returns an error
// matching [ErrExcessiveAliasing] when aliases make up too much of it.
// The go-yaml decoder then fills v, and once it accepts the node, every
// value in v that implements [SelfValidator] validates itself, unless
// [WithSelfValidation] turns that off. A step that fails before the
// decoder runs leaves v as it is.
//
// Fields absent from the document keep their values, so a caller may
// fill v with defaults first. A mapping merges into a struct field by
// field at every depth, and the document replaces a slice, an array, a
// map, or an interface value whole. A node without content, or a null,
// leaves v as it is, unless v points to a pointer or an interface, which
// a null with no tag sets to nil. An alias reads the anchor of its name
// defined last before it in the document, inside the node or outside
// it, and an alias with no such anchor reads one of a reference document
// from [WithReferences].
//
// Every error comes back bound to the source as a [SourceError]. An
// error of the decode itself matches [ErrDecode] and carries the `$`
// path of the value the decoder rejected. The error of an unmarshaler
// binds at the value that returned it, and a decode that fails reports
// every problem it finds in one error. A panic in the decoder or in an
// unmarshaler comes back as an error that holds a [PanicError], bound at
// the first token of the node, and does not match ErrDecode. The
// Decoding section describes the messages, the sentinels of each
// problem, and where each error binds.
func (n *Node) DecodeInto(ctx context.Context, v any, opts ...DecodeOption) error {
	return n.decodeInto(ctx, v, newDecodeConfig(opts), nil)
}

// decodeInto is [Node.DecodeInto] with its settings resolved. The decode
// also collects the comments of the node in comments, as
// [Node.decodeUnbound] describes, unless comments is nil.
func (n *Node) decodeInto(ctx context.Context, v any, cfg decodeConfig, comments yaml.CommentMap) error {
	err := checkTarget(v, ErrDecodeTarget, true)
	if err != nil {
		return n.bindOwn(err)
	}

	err = n.validate(ctx, cfg.validators)
	if err != nil {
		return err
	}

	err = aliasing.CheckDecode(n)
	if err == nil && aliasing.DecodesTextWith(reflect.TypeOf(v).Elem(), cfg.unmarshalers) {
		err = aliasing.CheckDecodeText(n)
	}

	if err != nil {
		return n.Bind(Invalid(err, atToken(astnode.StartToken(n.AST()))))
	}

	err = n.bindOwn(n.decodeUnbound(ctx, n.AST(), v, cfg, comments))
	if err != nil {
		return err
	}

	if cfg.skipSelfValidation {
		return nil
	}

	return n.selfValidate(ctx, v, cfg)
}

// checkTarget returns sentinel when v holds no value: when v is nil or a
// nil pointer, or with needPointer set, when v is no pointer. A decode
// needs a pointer, since the go-yaml decoder panics on a nil interface
// and decodes nothing into a nil pointer, so the check runs before v
// reaches it. When v points to a pointer, that pointer may be nil, as a
// decode of a null leaves it, and [allocate] allocates it. A nil v
// reads "got nil" beside the sentinel of a decode, and the sentinel alone
// for any other check.
func checkTarget(v any, sentinel error, needPointer bool) error {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		if needPointer {
			return fmt.Errorf("%w: got nil", sentinel)
		}

		return sentinel
	}

	if needPointer && rv.Kind() != reflect.Pointer || rv.Kind() == reflect.Pointer && rv.IsNil() {
		return fmt.Errorf("%w: got %T", sentinel, v)
	}

	return nil
}

// maxPointerDepth caps the pointers [allocate] allocates through. A
// type that refers back to itself, such as "type P *P", never reaches a
// value that is not a pointer, and the go-yaml decoder loops forever on
// a non-nil one.
const maxPointerDepth = 8

// allocate returns the pointer the go-yaml decoder decodes a value that
// is no null into for v, a non-nil pointer. The decoder decodes nothing
// into a nil pointer and replaces the value behind a non-nil one. When v
// points to a pointer, allocate therefore allocates each nil pointer on
// the way down and returns the last one, and the decoder fills its value
// in place. It returns v when the pointers go deeper than
// [maxPointerDepth].
func allocate(v any) any {
	rv := reflect.ValueOf(v)

	depth := 0
	for t := rv.Type().Elem(); t.Kind() == reflect.Pointer; t = t.Elem() {
		depth++
		if depth > maxPointerDepth {
			return v
		}
	}

	for range depth {
		elem := rv.Elem()
		if elem.IsNil() {
			elem.Set(reflect.New(elem.Type().Elem()))
		}

		rv = elem
	}

	return rv.Interface()
}

// newDecoder returns a go-yaml decoder for a decode of n with cfg. It
// applies the decode options of the source, then the ones the settings
// of cfg stand for, then extra. The function of each
// [WithCustomUnmarshaler] in cfg reads its value as [Node.decodeValue]
// decodes it with cfg.
func (n *Node) newDecoder(cfg decodeConfig, extra ...yaml.DecodeOption) *yaml.Decoder {
	decode := func(ctx context.Context, text []byte, dst any) error {
		return n.decodeValue(ctx, text, dst, cfg)
	}

	opts := slices.Concat(n.source.decodeOptions(), cfg.unmarshalers.YAMLOptions(decode))

	if cfg.disallowUnknownFields {
		opts = append(opts, yaml.DisallowUnknownField())
	}

	if len(cfg.allowedFieldPrefixes) > 0 {
		opts = append(opts, yaml.AllowFieldPrefixes(cfg.allowedFieldPrefixes...))
	}

	if cfg.orderedMaps {
		opts = append(opts, yaml.UseOrderedMap())
	}

	return yaml.NewDecoder(bytes.NewReader(nil), append(opts, extra...)...)
}

// decodeValue decodes text into dst with cfg, for the decode callback of
// a [WithCustomUnmarshaler] function. The text is what the go-yaml
// decoder hands that function for one value of the document of n, and
// dst is the pointer the function gave the callback. Any other dst
// returns an error wrapping [ErrDecodeTarget].
//
// The text is a document of its own, so decodeValue reads it as a
// [Source] that takes the settings of the source of n, and decodes the
// document of that Source as [Node.decodeUnbound] does. The settings of cfg
// that say how a value decodes thus apply below the value too, and a
// failed decode reports every problem of the value. A [Validator] checks
// a document, so decodeValue runs none.
//
// No Node binds the error of that decode. It names places in the text,
// and decodeValue returns it as [valueError] writes it for the decode
// that called the function to place. An error of ctx comes back as it
// is.
func (n *Node) decodeValue(ctx context.Context, text []byte, dst any, cfg decodeConfig) error {
	err := checkTarget(dst, ErrDecodeTarget, true)
	if err != nil {
		return err
	}

	source := NewSourceFromBytes(text, func(c *sourceConfig) {
		c.allowDuplicateKeys = n.source.allowDuplicateKeys
	})

	doc, err := source.Document()
	if err != nil {
		// A panic of the parser stays a panic, so the decode that called
		// the function passes it on.
		if p, ok := errors.AsType[*PanicError](err); ok {
			return fmt.Errorf("parse the text of the value: %w", panicked("parser", p))
		}

		// The decoder wrote the text from a node that parsed, and the
		// positions of the error count the lines of that text.
		//nolint:errorlint // The binding of the text is no binding of the document.
		return fmt.Errorf("parse the text of the value: %v", err)
	}

	return valueError(doc.decodeUnbound(ctx, doc.AST(), dst, cfg, nil))
}

// decodeUnbound decodes node to v with cfg, and returns the error for
// the Node to bind: a rejection of the decoder as an [*Error] at the path
// of the offending value, as [Node.decodeRejection] describes, and any
// other, such as a canceled context, as it is. [Node.decodeValue] writes
// that error for another document to bind instead. A node without content,
// the body of an empty document, leaves v as it is, so defaults already
// in v survive, as [Node.DecodeInto] promises. [yaml.Unmarshal] instead
// zeroes its target for input that holds no value. A tagged null, such
// as a "!!seq" tag over no value, also leaves v as it is, since the
// go-yaml decoder reads it as no value. A null with no tag, anchored or
// not, leaves v as it is too, unless v points to a pointer or an
// interface. The decoder keeps the value of a field for such a null, so
// the decode keeps the value of v as well, of any kind. [Node.At] and
// [Node.Nodes] hand a decode the value an anchor names, so an anchored
// null at the root reads as a scoped decode reads it. When v points to a
// pointer, a null sets that pointer to nil, and so does an alias that
// reads as null. A panic in the decoder comes back as an error that holds a
// [*PanicError], bound at the first token of node that is not a
// comment. The decoder reads node in the [decodetree.Tree] of the document,
// and for a node below the body, a failure in an anchor outside node
// that node reads comes back as its error. The error of a value that
// decodes itself binds at the path [Node.locateDecodeError] finds for
// that value. A rejection comes back with the other problems of the
// document that [Node.decodeProblems] finds, bound as one error.
//
// The decoder that fills v also collects the comments of node in
// comments, as [yaml.CommentToMap] collects them, unless comments is
// nil. [Node.YAMLComments] passes an empty map of its own. A node that
// starts no decoder adds nothing to the map. The decode empties the map
// once the decoder has registered the anchors a node below the body
// reads, since their comments lie outside node.
//
// The go-yaml decoder never checks the context, so a context that has
// ended before the decode starts, or while it registers the anchors node
// needs, stops the decode, and its error comes back as it is, whatever
// node holds.
func (n *Node) decodeUnbound(
	ctx context.Context,
	node ast.Node,
	v any,
	cfg decodeConfig,
	comments yaml.CommentMap,
) error {
	err := ctx.Err()
	if err != nil {
		return err //nolint:wrapcheck // The caller binds the error.
	}

	value := node
	if anchor, ok := node.(*ast.AnchorNode); ok {
		value = anchor.Value
	}

	null := astnode.IsNil(value) || value.Type() == ast.NullType
	kind := reflect.TypeOf(v).Elem().Kind()

	if !astnode.HasContent(node) || astnode.IsTaggedNull(node) ||
		null && kind != reflect.Pointer && kind != reflect.Interface {
		return nil
	}

	var extra []yaml.DecodeOption

	if comments != nil {
		extra = append(extra, yaml.CommentToMap(comments))
	}

	dec := n.newDecoder(cfg, extra...)

	view, err := n.prime(ctx, dec, node)
	if err != nil {
		return n.decodeRejection(err)
	}

	// The pass decoded anchors outside node with dec, which wrote their
	// comments to the map.
	clear(comments)

	// The decoder tests an alias for null, rather than the anchor it
	// names, so it gives a pointer target a value for an alias to a null.
	if kind == reflect.Pointer && isNullAlias(ctx, dec, value, view) {
		reflect.ValueOf(v).Elem().SetZero()

		return nil
	}

	// For a null, the decoder sets *v to nil.
	target := v
	if !null {
		target = allocate(v)
	}

	// The recover turns a panic in the go-yaml decoder, or in a value's
	// own UnmarshalYAML, into an error that holds a PanicError.
	err = decodeWithRecover(ctx, dec, view, target)

	// The decoder returns the error of a value that decodes itself with
	// no token, so the Node finds the value that reported it.
	err = n.locateDecodeError(ctx, n.rejection(err, view), node, v, cfg)

	// The decoder stops at its first rejection, so the Node finds the
	// other problems of the document and reports them together.
	return n.decodeProblems(ctx, err, node, v, cfg)
}

// isNullAlias reports whether value is an alias that dec reads as null.
// [Node.At] and [Node.Nodes] resolve every alias on a path to its
// anchor, so only the body of a document reaches a decode as an alias.
// Such an alias names an anchor of a reference document, which only dec
// can read. The check reads view, the [decodetree.DecodeView] of the
// node, with dec itself, which holds the anchors of the reference
// documents for the decode that follows.
func isNullAlias(ctx context.Context, dec *yaml.Decoder, value, view ast.Node) bool {
	if _, ok := value.(*ast.AliasNode); !ok {
		return false
	}

	var sink any

	err := decodeWithRecover(ctx, dec, view, &sink)

	return err == nil && sink == nil
}

// rejection returns err, which the go-yaml decoder returned for scope, a
// node of the [decodetree.Tree], as an [*Error] that matches [ErrDecode] and
// [errPlaced] when the decoder reported the rejection without a token of
// the source. The depth limit of the decoder binds at the first
// token of scope that is not a comment. A `<<` merge key whose alias
// names no mapping the decoder can find binds at the alias, when err is
// the decoder's failure for that alias, as [decodetree.Tree.UnresolvedMerge]
// describes, with the message a decode of the whole document gives that
// alias. Any other [yaml.Error] without a token of the source comes back
// that way when scope holds an alias to a reference document,
// as [decodetree.Tree.ReferenceAliases] finds them, with go-yaml's position
// and excerpt of that document left out and the message and the label
// [document.rejectionText] gives it. It binds at the alias when
// scope holds one such alias, and carries no location when it holds
// several, since the token does not say which of them led to it. The
// count ignores the target type, so a type that reads only one of
// several such aliases gets no location either. The decoder gives no
// way to tell such an error from an unwrapped [yaml.Error] of the parse
// an UnmarshalYAML runs on its bytes, so that error comes back the same
// way. Any other error comes back as it is, such as one from a value's own
// UnmarshalYAML, an ended context, or a panic [decodeWithRecover]
// already placed. So does any error for a nil scope.
func (n *Node) rejection(err error, scope ast.Node) error {
	if err == nil || astnode.IsNil(scope) || errors.Is(err, errPlaced) || contextEnded(err) {
		return err
	}

	yamlErr, ok := err.(yaml.Error) //nolint:errorlint // A wrapped error is the unmarshaler's own.
	if ok && n.holdsToken(yamlErr.GetToken()) {
		return err
	}

	tree := n.doc.tree()

	var (
		at    *token.Token
		cause error
	)

	if errors.Is(err, yaml.ErrExceededMaxDepth) {
		at, cause = astnode.StartToken(scope), n.doc.restoreError(err)
	} else if tk, msg, ok := tree.UnresolvedMerge(scope, err); ok {
		// The decoder's failure for an alias inside its own anchor names
		// the null it merges in its place, which the source does not hold,
		// so the message is the one a decode of the whole document gives.
		at, cause = tk, markedError{err: err, msg: msg}
	}

	if at == nil && ok {
		refs := tree.ReferenceAliases(scope)
		if len(refs) == 0 {
			return err
		}

		msg, label := n.doc.rejectionText(yamlErr)
		rejected := markDecode(yamlMessage(yamlErr, msg), label, errPlaced)

		// The token does not say which anchor of a reference document
		// holds it, so only a decode that reads one alias from a
		// reference knows which alias led to it.
		if len(refs) > 1 {
			return Invalid(rejected)
		}

		return Invalid(rejected, atToken(refs[0]))
	}

	if at == nil {
		return err
	}

	rejected := markDecode(fmt.Errorf("decoder rejected the value: %w", cause), errPlaced)

	return Invalid(rejected, atToken(at))
}

// errPlaced marks an error of a decode that [decodeWithRecover] or
// [Node.rejection] placed in the source, so [Node.locateDecodeError]
// looks for no value that reported it.
var errPlaced = errors.New("placed")

// markDecode returns err as an error of a decode, which matches
// [ErrDecode] and [errInvalid], and each of marks that is not nil, such
// as the label of a rejection or [errPlaced]. It reads as err and
// unwraps to it. An error of the parse matches [ErrSyntax] instead.
func markDecode(err error, marks ...error) markedError {
	marks = slices.DeleteFunc(append([]error{ErrDecode, errInvalid}, marks...), func(mark error) bool {
		return mark == nil
	})

	return markedError{err: err, marks: marks}
}

// yamlMessage returns err, a [yaml.Error], reduced to its message, or to
// msg when msg is not empty. The go-yaml text carries its own position
// and excerpt, which the [SourceError] binding the error renders itself,
// so the message alone goes in the chain. The result is sealed, as
// [markedError] describes, so a reporter never prints that excerpt,
// which holds lines of a [Source] whatever [WithExcerpts] says of it.
func yamlMessage(err yaml.Error, msg string) markedError {
	if msg == "" {
		msg = err.GetMessage()
	}

	return markedError{err: err, msg: msg, sealed: true}
}

// asDecodeError returns err, an error the go-yaml decoder returned, as
// one that matches [ErrDecode]. The error of a context that ended comes
// back as it is, even when an unmarshaler wraps it. So does an error that
// holds a [*PanicError], as [holdsPanic] reports, and one that matches
// already.
func asDecodeError(err error) error {
	if err == nil || errors.Is(err, ErrDecode) || contextEnded(err) || holdsPanic(err) {
		return err
	}

	return markDecode(err)
}

// decodeWithRecover decodes node into v with dec. It turns a panic in the
// decoder, or in the code of the caller that the decoder runs, into an
// [*Error] that holds a [*PanicError] and matches [errPlaced]. The Error
// points at the first token of node that is not a comment, so a comment
// above the value does not take the location. The panic is no fault of
// the document, so the Error declares nothing and matches neither
// [ErrDecode] nor [errInvalid]. The error of a [WithCustomUnmarshaler]
// function comes back as the function returned it, out of the
// [shieldedError] the decoder carried it in.
func decodeWithRecover(ctx context.Context, dec *yaml.Decoder, node ast.Node, v any) (err error) {
	defer func() {
		p := recover()
		if p == nil {
			return
		}

		err = Place(panicked("decoder", &PanicError{Value: p, Stack: debug.Stack()}, errPlaced),
			atToken(astnode.StartToken(node)))
	}()

	err = dec.DecodeFromNodeContext(ctx, node, v)

	// The decoder carried the error of a function from an option inside
	// a shield, so it could not rewrite it.
	if shielded, ok := err.(shieldedError); ok { //nolint:errorlint // The decoder returns it unwrapped.
		return shielded.err
	}

	return err //nolint:wrapcheck // The caller binds the error.
}

// Decode validates and decodes the node into a new T, as
// [Node.DecodeInto] does, and returns the zero value of T on error. The
// node is the whole document for the root Node of a document, and a Node
// from [Node.At] decodes one value without the rest of the document:
//
//	config, err := doc.Decode[Config](ctx, niceyaml.WithValidator(reg))
//
// A scalar decodes into a string whatever its type, so a Decode[string]
// reads a discriminator such as kind. A number decodes into a string in
// its canonical spelling, so 1.10 reads as "1.1" and 0x10 as "16".
// [go.jacobcolvin.com/niceyaml/schema/matcher.Text] hands its function
// the text as the document wrote it, and the String method of
// [Node.AST] gives the YAML text of any node.
//
// An [ast.Node] in the result is part of a tree the document shares, so
// a caller must not modify it.
func (n *Node) Decode[T any](ctx context.Context, opts ...DecodeOption) (T, error) {
	var v T

	err := n.DecodeInto(ctx, &v, opts...)
	if err != nil {
		var zero T

		return zero, err
	}

	return v, nil
}

// YAMLComments returns the comments of the node in a new map, each under
// the YAML path of the value it belongs to, as go-yaml's
// [yaml.CommentToMap] option collects them.
// [go.jacobcolvin.com/niceyaml/encoder.WithYAMLComments] writes such a
// map back, so a program that decodes a document, changes the value, and
// encodes it again keeps the comments:
//
//	comments, err := doc.YAMLComments(ctx)
//	if err != nil {
//		return err
//	}
//
//	out, err := encoder.Marshal(ctx, cfg, encoder.WithYAMLComments(comments))
//
// The call decodes the node into an any, as [Node.DecodeInto] does with
// no [DecodeOption], so a program that also decodes the node reads it
// twice. A decode that fails, such as one of a document that did not
// parse, returns its error and a nil map. A node that holds no value
// returns an empty map.
//
// Each key is a path from the root of the document, whatever node the
// call reads, so the map of a whole document pairs with an encode of its
// value, and the map of a Node from [Node.At] does not pair with an
// encode of that node alone.
func (n *Node) YAMLComments(ctx context.Context) (yaml.CommentMap, error) {
	var value any

	comments := yaml.CommentMap{}

	err := n.decodeInto(ctx, &value, decodeConfig{skipSelfValidation: true}, comments)
	if err != nil {
		return nil, err
	}

	return comments, nil
}

// prime returns node as the [decodetree.Tree] of the document reads it,
// and registers with dec the anchors that the aliases in it refer to, as
// [decodetree.Tree.Prime] lists them. The decoder registers the anchors
// of the node it decodes, so an alias in a node below the body finds an
// anchor defined elsewhere in the document only after the decoder has
// seen that anchor. The body holds every anchor it reads, so prime
// registers none for it. Each anchor decodes on its own.
//
// Every anchor of the tree has a name of its own, so the pass decodes
// only anchors that node, or an anchor node reads, refers to. A failure
// in one of them would leave an alias with nothing to read, so the pass
// stops and returns it, as a decode of the whole document fails there
// too, as [Node.rejection] reads it for that anchor. A context that ends
// stops the pass, which then returns the error of the context.
func (n *Node) prime(ctx context.Context, dec *yaml.Decoder, node ast.Node) (ast.Node, error) {
	view := n.doc.tree().View(node)
	if node == n.doc.root.Body {
		return view, nil
	}

	for _, anchor := range n.doc.tree().Prime(view) {
		// Without an anchor it needs, the decode of the node would fail
		// in its stead.
		err := ctx.Err()
		if err != nil {
			return view, err //nolint:wrapcheck // The context names the reason, and the Node binds it.
		}

		var sink any

		err = decodeWithRecover(ctx, dec, anchor, &sink)
		if err != nil {
			return view, n.rejection(err, anchor)
		}
	}

	return view, nil
}

// DecodeAt validates and decodes the node path selects into a new T. It
// is [Node.At] and then [Node.Decode] on the Node that At returns, for a
// caller that needs the value and not the Node:
//
//	kind, err := doc.DecodeAt[string](ctx, paths.Current().Child("kind"))
//
// The path resolves as At resolves it, and the decode runs on the scoped
// Node, as the Scoped Nodes section of the package documentation
// describes. Every error is the one At or Decode returns. A path that
// selects nothing returns the error of At, such as
// "cfg.yaml:1:1: $.kind: not found", and a document that did not parse
// returns the syntax error [Node.Err] returns. In a document with no
// content, which [Node.IsEmpty] reports, every path with a selector
// returns an error wrapping [paths.ErrNoDocument], and so does the root
// path when the document has no "---" header, where Decode on the root
// returns the zero value. On error, the returned T is the zero value.
//
// DecodeAt reads a value the document must hold. The decode can also
// return an error that matches [paths.ErrNotFound], such as one from a
// [Validator] that reads a required key below the node, so a caller that
// falls back to a default calls [Node.DecodeIfPresent] instead.
//
// DecodeAt drops the scoped Node, so an `@` path in a check the caller
// later runs on the value resolves from the receiver. A caller that binds
// such errors scopes the Node with At and binds through it, or moves them
// under path with [Rebase] before the receiver binds them:
//
//	return doc.Bind(niceyaml.Rebase(check(h), hoursPath))
func (n *Node) DecodeAt[T any](ctx context.Context, path paths.Path, opts ...DecodeOption) (T, error) {
	node, err := n.At(path)
	if err != nil {
		var zero T

		return zero, err
	}

	return node.Decode[T](ctx, opts...)
}

// DecodeIfPresent validates and decodes the node path selects into v,
// and reports whether the document holds a node at path. It is
// [Node.At] and then [Node.DecodeInto] on the Node that At returns, for
// a value the document may leave out. The caller fills v with its
// default first, and a path that selects nothing leaves v as it is:
//
//	version := 1
//
//	found, err := doc.DecodeIfPresent(ctx, versionPath, &version)
//
// The target v must be a non-nil pointer. Any other v returns an error
// wrapping [ErrDecodeTarget], bound to the source, before the path
// resolves, so the mistake shows whether or not the document holds the
// value.
//
// DecodeIfPresent returns false and a nil error only when At returns an
// error that matches [paths.ErrNotFound]. Every other failure returns
// false and the error. That covers a syntax error, an alias on the path
// that does not resolve, a wildcard path, and every error of the decode,
// even one that matches paths.ErrNotFound, so a node that fails a
// [Validator] never reads as absent.
//
// A null is present, so `version: ~` reports true and leaves version at
// its default, unless v points to a pointer or an interface, which such
// a null sets to nil. A key looked up in a value that is no mapping, an
// index past the end of a sequence, and every path with a selector in a
// document with no content are absent, as
// [go.jacobcolvin.com/niceyaml/schema/matcher.Exists] finds them.
//
// The decode runs on the scoped Node and drops it, as [Node.DecodeAt]
// describes.
func (n *Node) DecodeIfPresent(ctx context.Context, path paths.Path, v any, opts ...DecodeOption) (bool, error) {
	// The target check runs first, so a wrong target fails for an absent
	// path too, where no decode would run to reject it.
	err := checkTarget(v, ErrDecodeTarget, true)
	if err != nil {
		return false, n.bindOwn(err)
	}

	node, err := n.At(path)
	if errors.Is(err, paths.ErrNotFound) {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	// The decode can return an error that matches paths.ErrNotFound too,
	// so only the error of At above counts as an absent value.
	err = node.DecodeInto(ctx, v, opts...)
	if err != nil {
		return false, err
	}

	return true, nil
}

// decodeProblems returns err, the rejection a decode of node into v
// with cfg returned, with the other problems of that decode beside it,
// for the Node to bind.
//
// The go-yaml decoder decides whether a decode fails, and it returns one
// rejection for a document that holds several problems. That is the
// first value it rejects, in the order the target declares its fields,
// or an unknown field when it rejects no value. DecodeProblems runs
// only after the decoder rejected, and its result always holds that
// rejection as [Node.decodeRejection] returns it, so no decode passes or
// fails because of it. A [problemCollector] finds the other problems.
//
// With no other problem, the result is the rejection as
// [Node.decodeRejection] returns it. With more, it is a summary from
// [NewSummary] that heads the rejection and each other problem, in the
// order of the source. Its message counts them, as "3 problems" does,
// and reads "3 unknown fields" when each is the rejection of an unknown
// field. A path in a problem starts at `$` or reads from the scope of n,
// as a path in the rejection does, so the Node binds the summary as it
// binds the rejection alone.
//
// The collector marks each problem by a position, and the rejection
// stands for the problem at the position [Node.rejectedAt] gives it. A
// rejection with no such position comes back alone, since nothing tells
// it apart from a problem the collector finds. Every rejection comes
// back alone once ctx has ended, before the pass or during it, since the
// collector then stops before it has read the whole document. Returns
// nil for a nil err.
func (n *Node) decodeProblems(
	ctx context.Context,
	err error,
	node ast.Node,
	v any,
	cfg decodeConfig,
) error {
	if err == nil {
		return nil
	}

	rejectionErr := n.decodeRejection(err)

	at, ok := n.rejectedAt(err, rejectionErr)
	if !ok || ctx.Err() != nil {
		return rejectionErr
	}

	c := newProblemCollector(ctx, n, cfg)
	c.collect(reflect.TypeOf(v).Elem(), node)

	delete(c.found, at)

	if len(c.found) == 0 || ctx.Err() != nil {
		return rejectionErr
	}

	unknown, ok := err.(*yaml.UnknownFieldError) //nolint:errorlint // A wrapped error is the unmarshaler's own.

	problems := make([]decodeProblem, 0, len(c.found)+1)
	problems = append(problems, decodeProblem{
		err:          rejectionErr,
		at:           at,
		unknownField: ok && n.holdsToken(unknown.Token),
	})

	for _, problem := range c.found {
		problems = append(problems, problem)
	}

	slices.SortFunc(problems, func(a, b decodeProblem) int {
		return cmp.Or(cmp.Compare(a.at.Line, b.at.Line), cmp.Compare(a.at.Col, b.at.Col))
	})

	errs := make([]error, 0, len(problems))
	unknownFields := true

	for _, problem := range problems {
		errs = append(errs, problem.err)
		unknownFields = unknownFields && problem.unknownField
	}

	msg := fmt.Sprintf("%d problems", len(errs))
	if unknownFields {
		msg = fmt.Sprintf("%d unknown fields", len(errs))
	}

	return NewSummary(msg, errs...)
}

// rejectedAt returns the position that marks err, a rejection of the
// decoder, among the problems a [problemCollector] finds, where
// rejectionErr is err as [Node.decodeRejection] returns it. The decoder
// names a value it rejects by a token of the source, and the collector
// marks a problem it decodes by the same token. Any other rejection lies
// where the Node binds it, which is where the collector marks a problem
// that takes a path.
//
// The binding of a rejection in the document [NewSourceFromLayers] builds resolves in
// the file of a layer, and the collector reads the merged text. Such a
// rejection lies where [Node.mergedPosition] finds it in that text.
//
// The bool result is false for a rejection that binds at no position,
// such as one the decoder reports with no token for a value that
// [Node.locateDecodeError] did not find. It is false too for a binding
// that n did not make, such as one an unmarshaler returns for a source
// of its own.
func (n *Node) rejectedAt(err, rejectionErr error) (position.Position, bool) {
	yamlErr, ok := err.(yaml.Error) //nolint:errorlint // A wrapped error is the unmarshaler's own.
	if ok && n.holdsToken(yamlErr.GetToken()) {
		return position.NewFromToken(yamlErr.GetToken()), true
	}

	if n.merges() {
		return n.mergedPosition(rejectionErr)
	}

	srcErr, ok := n.bindOwn(n.decodeRejection(err)).(*SourceError) //nolint:errorlint // A binding below a wrapper is not one n made.
	if !ok || srcErr.node != n {
		return position.Position{}, false
	}

	return srcErr.Position()
}

// mergedPosition returns the position of rejectionErr in the text of the
// document of n, which [NewSourceFromLayers] built, where rejectionErr is
// a rejection as [Node.decodeRejection] returns it. That is the position
// the binding of the rejection reports in a document no merge built. A
// position stands as it is, and so does the start of a range. A path that
// starts at `@` reads from the scope of n. Every path resolves as
// [Node.resolveLocation] resolves it.
//
// The bool result is false for a rejection with no location. It is false
// for one that takes the location of a binding, as the error an
// unmarshaler returns for a source of its own does. It is false too for
// a location that resolves nowhere in the text.
func (n *Node) mergedPosition(rejectionErr error) (position.Position, bool) {
	found := anchorOf(placeable(rejectionErr))
	if _, own := found.err.(*Error); !own { //nolint:errorlint // The anchor itself, found by the walk.
		return position.Position{}, false
	}

	var loc location

	switch at := found.loc.(type) {
	case position.Range:
		loc.pos = at.Start

	case position.Position:
		loc.pos = at

	default:
		if found.ambiguous {
			return position.Position{}, false
		}

		var err error

		loc, err = n.resolveLocation(n.base.Join(found.path))
		if err != nil {
			return position.Position{}, false
		}
	}

	if checkInRange(loc, n.source.lines) != nil {
		return position.Position{}, false
	}

	return loc.pos, true
}

// decodeRejection returns err, an error from the decoder, for the Node to
// bind. A [yaml.Error] at a token of the source comes back as
// [Node.tokenRejection] returns it, at the path of the node the decoder
// names by the token. That path starts at `$`, so the Node binds it with
// no scope in front. Any other error comes back as [asDecodeError]
// returns it, with a message that names each anchor as the document
// does, and keeps the location it carries, such as one a value's own
// UnmarshalYAML returns, or the path [Node.locateDecodeError] put it
// under.
//
// Only a [yaml.Error] the decoder returns itself converts. One that an
// UnmarshalYAML wraps stays the error of that unmarshaler, with the text
// and sentinels of its wrapper. One from a parse an UnmarshalYAML runs on
// its bytes has a token of that parse, so it stays the error of the
// value too. The decoder builds the mapping an inline field decodes
// from, with no token for the mapping or its keys, so a rejection of a
// key of an inline map, or of an inline field whose type takes no
// mapping, has no token of the source either.
func (n *Node) decodeRejection(err error) error {
	yamlErr, ok := err.(yaml.Error) //nolint:errorlint // A wrapped error is the unmarshaler's own.
	if !ok || !n.holdsToken(yamlErr.GetToken()) {
		return asDecodeError(n.doc.restoreError(err))
	}

	return n.tokenRejection(yamlErr)
}

// tokenRejection returns err, a rejection the decoder reported at a token
// of the source, as an [*Error] that matches [ErrDecode]. The Error reads
// as [document.rejectionText] writes err, with each anchor named as the
// document names it, matches the label rejectionText gives err, and
// carries the location [Node.rejectionLocation] gives the token. The path
// in that location starts at `$`.
func (n *Node) tokenRejection(err yaml.Error) error {
	msg, label := n.doc.rejectionText(err)
	rejected := markDecode(yamlMessage(err, msg), label)

	return Invalid(rejected, n.rejectionLocation(err.GetToken())...)
}

// decodeProblem is one problem of a decode, before the Node binds it.
type decodeProblem struct {
	// The problem, located as [Node.decodeRejection] locates a rejection.
	err error
	// The position that marks the problem among the others, which is the
	// position of the value the problem is about.
	at position.Position
	// The problem is the rejection of an unknown field.
	unknownField bool
}

// problemCollector finds the problems of a decode beside the rejection
// the go-yaml decoder returned for it. The decoder reads the fields of a
// struct in the order the struct declares them and returns the first
// rejection among them, so a document that holds several problems shows
// one for each decode.
//
// The collector walks the type the decode filled beside the node the
// decode read, as [decodewalk.Walk] pairs them. Where the walk reaches a
// leaf, it decodes the node of the leaf alone into a new value of the
// type of the leaf. A leaf is a value the walk reads nothing below. That
// is a scalar, or a node of a kind its type does not decode from, such
// as a sequence where a struct takes a mapping. The walk never decodes a
// mapping of the document into the struct or the map that reads it, or a
// sequence into its slice. A value thus costs the same however deep it
// lies, and the pass decodes no value below such a mapping or sequence a
// second time.
//
// A leaf adds a problem only for these rejections. The decoder builds
// the first three itself, with no code of the caller:
//
//   - A [yaml.TypeError], a [yaml.OverflowError], or a
//     [yaml.UnexpectedNodeTypeError] at a token of the source. Those are
//     a value of the wrong kind and a number out of range.
//   - The error of [time.ParseDuration] for a scalar in a
//     [time.Duration], which the decoder returns with no token.
//   - The refusal of a mapping or a sequence in a type that takes a
//     scalar alone, as [unmarshaler.Options.RefusesForText] reports,
//     which the decoder returns with no token too. The problem reads as
//     [textRejection] writes it.
//   - The error of an UnmarshalText method for a scalar in a type that
//     [unmarshaler.Options.FromText] reports, which the decoder returns
//     with no token as well.
//
// The walk reads nothing at or below any other value that decodes
// itself, as [unmarshaler.Options.ReportsOwnError] lists those types, a
// type an option of the decode gives an unmarshaler among them. Only the
// unmarshaler of such a value says what it accepts, and a second call
// can answer otherwise than the first, as one that counts the names it
// has seen does. The decoder parses a [time.Duration] and a [time.Time]
// itself, so the walk reads both. It hands an UnmarshalText method the
// text of a scalar, without the context or the node of the decode. The
// walk therefore takes that method to answer the same for the same text,
// and reads the scalar. The decoder calls no method for a mapping or a
// sequence it refuses for such a type, so the walk reads that node as a
// leaf too. The walk reads nothing below an interface or a
// [yaml.MapSlice] either, which take any value.
//
// The walk cannot see every rule the decode applies, such as the types
// [yaml.RegisterCustomUnmarshaler] gave a function, or the value the
// decoder holds for an anchor. So the value that reads a leaf confirms
// each problem, as [problemCollector.confirmed] describes. The collector
// keeps a problem only when the decoder rejects the leaf there as it
// rejected the leaf alone. A struct that a registered function decodes
// thus judges its own fields. It judges nothing below them, so the
// collector can add a problem there that the function accepts.
//
// The pass decodes one leaf at a time, so the one unmarshaler it calls
// for a value the walk reads is that UnmarshalText method. It calls the
// method once for each field, element, or map value that reads the
// scalar, and once more to confirm a scalar the method rejects. A decode
// of the struct that reads a leaf can call code the walk does not pair
// with the leaf. That is a registered function and the unmarshaler of
// another value that reads the entry of the leaf, as a field of the same
// name in an inline struct does. What those return adds no problem.
//
// When the decoder rejects unknown fields, [decodewalk.UnknownFields]
// adds the rejection of each one the document holds.
//
// The collector marks each problem by a position and keeps the first it
// finds there, so a value that several aliases reach is one problem.
//
// One go-yaml decoder runs every decode of the pass, so the pass applies
// the options once. The decoder holds the anchors of the node as the
// decode read them, as [problemCollector.register] leaves them.
//
// Create instances with [newProblemCollector].
type problemCollector struct {
	ctx     context.Context
	node    *Node
	decoder *yaml.Decoder
	// The problems found, by the position that marks each.
	found map[position.Position]decodeProblem
	// What the walk knows of the decode, which decodes with decoder.
	rules decodewalk.Rules
	// The settings of the decode.
	cfg decodeConfig
}

// newProblemCollector creates a new [*problemCollector] for a decode of
// n with cfg.
func newProblemCollector(ctx context.Context, n *Node, cfg decodeConfig) *problemCollector {
	dec := n.newDecoder(cfg)

	return &problemCollector{
		ctx:     ctx,
		node:    n,
		decoder: dec,
		found:   map[position.Position]decodeProblem{},
		rules:   n.walkRules(ctx, dec, cfg.unmarshalers),
		cfg:     cfg,
	}
}

// walkRules returns the [decodewalk.Rules] of a decode of n whose options
// name the unmarshalers u, for a walk that asks dec about the values it
// reaches. The walk reads each node as the [decodetree.Tree] of the
// document reads it.
func (n *Node) walkRules(ctx context.Context, dec *yaml.Decoder, u unmarshaler.Options) decodewalk.Rules {
	return decodewalk.Rules{
		Resolver: n.doc.pathResolver(),
		View:     n.doc.tree().View,
		Decode: func(node ast.Node, v any) error {
			return decodeWithRecover(ctx, dec, node, v)
		},
		Unmarshalers: u,
	}
}

// collect finds the problems of a decode of node into a value of type
// t: the leaves the decoder rejects, and the unknown fields, when the
// decoder rejects those.
func (c *problemCollector) collect(t reflect.Type, node ast.Node) {
	c.register(node)

	from := datapath.ParentAt(c.rules.Resolver, c.node.base)
	decodewalk.Walk(c.ctx, c.rules, t, node, from, c.visit)

	if !c.cfg.disallowUnknownFields {
		return
	}

	for _, field := range decodewalk.UnknownFields(c.ctx, c.rules, t, node) {
		c.add(decodeProblem{
			err:          c.node.tokenRejection(field),
			at:           position.NewFromToken(field.Token),
			unknownField: true,
		})
	}
}

// register has the decoder of the pass read the anchors of node, the
// node the decode read, as the decoder of the decode read them.
//
// The go-yaml decoder reads a node as plain values before it decodes it
// into a target. It keeps the value of each anchor, and assigns that
// value to the target of an alias that takes it, with no decode. So the
// pass reads node into an interface once, after the anchors outside node
// that node refers to, as [Node.prime] reads them. Every later
// decode of the pass then reads an alias as the decode did.
//
// The decoder stops that read at a node it cannot read as a plain value,
// such as a tag that does not convert its value, and the decode returned
// that rejection. The anchors from that node on stay unknown to the
// decoder of the pass, as they did to the decoder of the decode. A value
// that reads one of them through an alias adds no problem, since its
// decode fails for the alias.
func (c *problemCollector) register(node ast.Node) {
	view, err := c.node.prime(c.ctx, c.decoder, node)
	if err != nil {
		return
	}

	var sink any

	_ = c.rules.Decode(view, &sink) //nolint:errcheck // The decode returned this rejection.
}

// add keeps problem, unless the collector holds one at the same position
// already.
func (c *problemCollector) add(problem decodeProblem) {
	if _, held := c.found[problem.at]; !held {
		c.found[problem.at] = problem
	}
}

// visit reads v, a value the walk reaches, and tells the walk whether to
// go below it. A leaf adds its problem, as [problemCollector.leaf]
// describes. The walk goes below no key, no member whose key has no
// name, since no path spells its place, and no value that decodes
// itself. Of those, a duration, a time, and a scalar in a type that
// [unmarshaler.Options.FromText] reports are leaves. So is a mapping or a
// sequence the decoder refuses for such a type, as
// [unmarshaler.Options.RefusesForText] reports. An inline field that
// decodes itself reads the mapping of its struct and is no leaf.
func (c *problemCollector) visit(v *decodewalk.Value) decodewalk.Action {
	u := c.cfg.unmarshalers

	switch {
	case v.Kind == decodewalk.Key, !v.Named, v.Kind == decodewalk.Inline && u.ReportsOwnError(v.Type):
		return decodewalk.Skip

	case v.Type == unmarshaler.DurationType || v.Type == unmarshaler.TimeType:
		c.leaf(v)

		return decodewalk.Skip

	case u.ReportsOwnError(v.Type):
		content := datapath.Deref(c.rules.Resolver, v.Node)

		// The decoder calls an UnmarshalText method for a scalar alone. It
		// refuses any other node itself, unless another unmarshaler of the
		// type takes that node.
		if u.RefusesForText(v.Type, content) || astnode.IsScalar(content) && u.FromText(v.Type) {
			c.leaf(v)
		}

		return decodewalk.Skip

	case v.Reads == decodewalk.Whole:
		c.leaf(v)

		return decodewalk.Skip

	case v.Reads == decodewalk.Nothing:
		return decodewalk.Skip

	default:
		return decodewalk.Descend
	}
}

// leaf decodes the node of v into a new value of its type and adds the
// rejection the decoder builds for it as a problem. The walk reads
// nothing below v.
//
// A rejection at a token of the source takes the location of that token,
// as [Node.tokenRejection] gives it. The error of a parse has no token.
// That is the error of [time.ParseDuration] for a scalar in a duration,
// or of the UnmarshalText method of a type that
// [unmarshaler.Options.FromText] reports. It takes the path of v, as
// [Node.locateDecodeError] puts one under the path of its value. It adds
// a problem only when that path selects the node the document holds for
// v, which the path of an entry that a later one hides does not. The
// path selects that node when it points at the token
// [astnode.PathToken] gives it. The refusal of a mapping or a sequence
// that [unmarshaler.Options.RefusesForText] reports has no token either.
// It takes that path the same way, and reads as [textRejection] writes
// it.
//
// Any other error adds nothing. That is another rejection of the
// decoder, such as one for a tag that does not convert its value, the
// error of a type that only a [yaml.RegisterCustomUnmarshaler] function
// decodes, or a panic that [decodeWithRecover] placed. It is also an
// error of an UnmarshalText method that names a place of its own, as
// [Node.lacksLocation] reports, since the position that marks a problem
// is the one its path resolves to.
func (c *problemCollector) leaf(v *decodewalk.Value) {
	err := c.rules.Decode(c.rules.View(v.Node), reflect.New(v.Type).Interface())
	if err == nil {
		return
	}

	if rejected, ok := builtRejection(err); ok {
		tk := rejected.GetToken()
		if c.node.holdsToken(tk) && c.confirmed(err, v) {
			c.add(decodeProblem{err: c.node.tokenRejection(rejected), at: position.NewFromToken(tk)})
		}

		return
	}

	_, isYAML := err.(yaml.Error) //nolint:errorlint // The decoder returns it unwrapped.
	if isYAML || !c.node.lacksLocation(err) {
		return
	}

	content := datapath.Deref(c.rules.Resolver, v.Node)

	var cause error

	switch {
	case c.cfg.unmarshalers.RefusesForText(v.Type, content):
		cause = textRejection(content)

	// The decoder parses the text of a scalar as a duration, or hands it
	// to an UnmarshalText method, and returns the error of that parse as
	// it is.
	case astnode.IsScalar(content) && (v.Type == unmarshaler.DurationType || c.cfg.unmarshalers.FromText(v.Type)):
		cause = asDecodeError(c.node.doc.restoreError(err))

	default:
		return
	}

	path := v.Path()

	loc, lerr := c.node.pathLocation(path)
	if lerr != nil || loc.tk != astnode.PathToken(v.Held, v.From) || !c.confirmed(err, v) {
		return
	}

	c.add(decodeProblem{err: Rebase(cause, path), at: loc.pos})
}

// builtRejection returns err as a rejection the decoder builds itself
// for a value it reads with no code of the caller: a value of the wrong
// kind, or a number out of range. The bool result is false for any other
// error.
func builtRejection(err error) (yaml.Error, bool) {
	switch rejected := err.(type) { //nolint:errorlint // The decoder returns these types unwrapped.
	case *yaml.TypeError:
		return rejected, true
	case *yaml.OverflowError:
		return rejected, true
	case *yaml.UnexpectedNodeTypeError:
		return rejected, true
	default:
		return nil, false
	}
}

// confirmed reports whether the value that reads v, a leaf, rejects it as
// err, the rejection a decode of v alone returned.
//
// A struct reads a field from a mapping that holds the entry of the
// field alone, into a new value of the type of that struct, as the
// [decodetree.Tree] of the document reads the entry. So does a struct
// that holds a map inline, for a member of that map. The decoder thus
// applies what the walk cannot see, such as a
// [yaml.RegisterCustomUnmarshaler] function for the struct. A struct that
// holds an inline field that decodes itself, as
// [unmarshaler.Options.InlinesUnmarshaler] reports, confirms nothing,
// since any decode of it calls that unmarshaler.
//
// A sequence or a map reads v as the one element of a sequence, which
// decodes into a slice of the type of v. The decoder reads a field, an
// element, and a map value alike, and it reads an alias otherwise than
// the content of its anchor. Where the value it holds for the anchor fits
// the target, it assigns that value and decodes nothing. A leaf that
// fails alone can thus pass through its alias.
//
// The value the walk starts at decodes alone in the decode itself, so
// nothing more confirms it.
func (c *problemCollector) confirmed(err error, v *decodewalk.Value) bool {
	var (
		node   ast.Node
		target reflect.Value
	)

	switch {
	case v.Struct != nil:
		if c.cfg.unmarshalers.InlinesUnmarshaler(v.Struct) {
			return false
		}

		view, ok := c.rules.View(v.Entry).(*ast.MappingValueNode)
		if !ok || view.Key == nil {
			return false
		}

		node, target = ast.Mapping(view.Start, false, view), reflect.New(v.Struct)

	case v.Kind == decodewalk.Element || v.Kind == decodewalk.Member:
		elements := ast.Sequence(v.Held.GetToken(), false)
		elements.Values = append(elements.Values, c.rules.View(v.Held))

		node, target = elements, reflect.New(reflect.SliceOf(v.Type))

	default:
		return true
	}

	return sameRejection(err, c.rules.Decode(node, target.Interface()))
}

// sameRejection reports whether again, the error of a second decode, is
// err, the rejection of a first one. Two rejections at a token are the
// same when they have one type and name one token. Two errors with no
// token are the same when they read the same.
func sameRejection(err, again error) bool {
	if again == nil {
		return false
	}

	want, tokened := err.(yaml.Error)      //nolint:errorlint // The decoder returns it unwrapped.
	got, alsoTokened := again.(yaml.Error) //nolint:errorlint // The decoder returns it unwrapped.

	if !tokened || !alsoTokened {
		return tokened == alsoTokened && err.Error() == again.Error()
	}

	return reflect.TypeOf(want) == reflect.TypeOf(got) && want.GetToken() == got.GetToken()
}

// rejectionLabel returns the sentinel a rejection of the given label
// matches, as [rejection.Classify] labels it: [ErrWrongKind],
// [ErrOverflow], or [ErrUnknownField], or nil for [rejection.None].
func rejectionLabel(label rejection.Label) error {
	switch label {
	case rejection.WrongKind:
		return ErrWrongKind
	case rejection.Overflow:
		return ErrOverflow
	case rejection.UnknownField:
		return ErrUnknownField
	default:
		return nil
	}
}

// rejectionLocation returns the options that locate a rejection the
// go-yaml decoder reported at tk, a token of the source.
//
// The first is the path of the node the decoder names by tk, as
// [pathindex.Index.OwnerPath] finds it, which starts at `$`. The
// error then binds where that path resolves, which is where an error at
// the same path from a [Validator] or a [SelfValidator] binds.
//
// A path can name a node it does not select, as the path of the earlier
// of two entries with one key does. A second option then gives the
// position of tk. The error binds there, and the path only names the
// node in the message.
//
// A node under a key with no name has no path, so the position of tk
// alone locates it.
func (n *Node) rejectionLocation(tk *token.Token) []ErrorOption {
	path, selects, ok := n.doc.pathIndex().OwnerPath(n.doc.pathResolver(), tk)

	switch {
	case !ok:
		return []ErrorOption{atToken(tk)}
	case selects:
		return []ErrorOption{AtPath(path)}
	default:
		return []ErrorOption{AtPath(path), atToken(tk)}
	}
}

// locateDecodeError returns err, the error a decode of node into v with
// cfg returned, at the path of the value that reported it. The go-yaml
// decoder returns the error of a value that decodes itself with no token
// of the source. Such a value is a [time.Duration] or has an unmarshaler,
// from a method or from an option of cfg, as
// [unmarshaler.Options.ReportsOwnError] lists them. To find that value,
// locateDecodeError walks the type of v beside node, as [errorLocator]
// describes, and decodes each such value again from its own node. The
// first one whose decode fails with err again, as [errorLocator.same]
// compares the two, reported it. The walk reads the entry the decoder
// reads for each field, so a field the decoder never read reports
// nothing.
//
// The result holds err under that path, as [Rebase] returns it, so the
// [SourceError] that binds the result reports the path and marks the
// value. The result matches [ErrDecode], and err stays in its chain, so
// the error of a value's own unmarshaler matches what it matched before
// too.
//
// An unmarshaler holds no [Node], so an [*Error] it returns writes an
// `@` path that reads from its own value, as the error of a
// [SelfValidator] does. The Rebase puts that path under the path of the
// value. It leaves a `$` path, a position, and a range as they are, so
// an err that carries one of those binds where it did before.
//
// The path of a value under the node the decode reads is never the root,
// with two exceptions. When node holds a scalar and the type of v
// decodes itself, the scalar is the value, so err points at node. When
// the decoder refuses node for the type of v, as
// [unmarshaler.Options.RefusesForText] reports, node is the value it
// refused.
//
// The decoder words that refusal for neither the value nor its kind, so
// the result then holds the rejection [textRejection] writes in place of
// err.
//
// An err that [Node.unplaced] reports false for comes back as it is,
// such as one the decode placed already. So does a binding, as [isBound]
// reports one, since it resolved its location in a source already. So
// does an err no value reproduces, and an `@` path in it then reads from
// node. A decode of node into any value reads no unmarshaler, so an err
// that such a decode returns too is the decoder's own, and it comes back
// as it is as well.
func (n *Node) locateDecodeError(
	ctx context.Context,
	err error,
	node ast.Node,
	v any,
	cfg decodeConfig,
) error {
	if err == nil || !n.unplaced(err) || isBound(err) {
		return err
	}

	dec := n.newDecoder(cfg)
	l := &errorLocator{
		ctx:     ctx,
		node:    n,
		decoder: dec,
		msg:     err.Error(),
		anchor:  anchorOf(err),
		places:  placesOf(err),

		unmarshalers: cfg.unmarshalers,
	}

	var sink any

	if l.reproduces(node, &sink) {
		return err
	}

	t := unmarshaler.Base(reflect.TypeOf(v).Elem())

	decodewalk.Walk(ctx, n.walkRules(ctx, dec, cfg.unmarshalers), t, node, astnode.Parent{}, l.visit)

	// With no value below node, the value is node itself.
	path := paths.Current()

	switch {
	case l.found != nil:
		path = l.found.Path()

	case !cfg.unmarshalers.ReportsOwnError(t) || !astnode.IsScalar(datapath.Deref(n.doc.pathResolver(), node)):
		return err
	}

	cause := l.refusal
	if cause == nil {
		cause = asDecodeError(n.doc.restoreError(err))
	}

	return Rebase(cause, path)
}

// unplaced reports whether the decode has yet to find the value err is
// about. It reports false for an err that matches [errPlaced], which
// [Node.rejection] or [decodeWithRecover] placed, for the error of a
// context that ended, and for a [yaml.Error] at a token of the source.
func (n *Node) unplaced(err error) bool {
	if errors.Is(err, errPlaced) || contextEnded(err) {
		return false
	}

	yamlErr, ok := err.(yaml.Error) //nolint:errorlint // A wrapped error is the unmarshaler's own.

	return !ok || !n.holdsToken(yamlErr.GetToken())
}

// lacksLocation reports whether err names no place at all, so it points
// at the value that reported it and nowhere else. It reports false for
// an err the decode placed, as [Node.unplaced] reports one. It also
// reports false when an error in the tree of err is a [*SourceError] or
// an [*Error] with a location, since those say where they point.
func (n *Node) lacksLocation(err error) bool {
	return n.unplaced(err) && !holdsLocation(err)
}

// textRejection returns the rejection of content, a mapping or a
// sequence the decoder refuses as [unmarshaler.Options.RefusesForText]
// reports. The error the decoder returns for it names neither the value
// nor its kind. The rejection matches [ErrDecode] and [ErrWrongKind] and
// reads "expected string, got sequence", as [rejection.ExpectedString]
// words it.
func textRejection(content ast.Node) error {
	return markDecode(errors.New(rejection.ExpectedString(content.Type())), ErrWrongKind)
}

// valueError returns err as an error about one value of a document, for
// the decode of that document to place. The err argument is what a
// decode of the text of the value returned before any Node bound it, as
// [Node.decodeValue] runs one. It is one problem, or the summary of
// several that [Node.decodeProblems] builds, and each problem comes back
// as [valueProblem] writes it.
func valueError(err error) error {
	summary, ok := err.(*Error) //nolint:errorlint // The decode returns its summary unwrapped.
	if !ok || summary == nil || len(summary.errors) == 0 {
		return valueProblem(err)
	}

	problems := make([]error, 0, len(summary.errors))
	for _, problem := range summary.errors {
		problems = append(problems, valueProblem(problem))
	}

	return NewSummary(summary.Error(), problems...)
}

// valueProblem returns problem as a problem of one value of a document,
// where problem is a problem of a decode of the text of that value.
//
// The decode locates a rejection by a token of the text. The path of
// that token reads from the root of the text, which is the value, so
// the result writes it as an `@` path. The decode of the document puts
// that path under the path of the value, as [Node.locateDecodeError]
// describes, and the path then resolves in the document, through each
// alias and `<<` merge key the text wrote out. A position counts the
// lines of the text and not of the document, so the result keeps none.
// A rejection of the value itself thus comes back with no location, and
// the decode of the document points it at the value it finds. So does an
// error the decode placed in the text, such as a panic, which the decode
// of the document has yet to place.
//
// Any other problem is the error of an unmarshaler below the value. Its
// `@` paths read from the value already, so it comes back as it is.
// When it names a position, as an unmarshaler that takes its node can
// build one, it comes back under the path it names, or under the path
// of the value, which stands over that position.
func valueProblem(problem error) error {
	x, ok := problem.(*Error) //nolint:errorlint // The decode returns the Error it located unwrapped.
	if !ok || x == nil || x.move != nil || !x.hasLocation() {
		at := anchorOf(problem)
		if at.loc == nil {
			return problem
		}

		return Place(problem, AtPath(at.path))
	}

	located := *x
	located.loc = nil

	if placed, ok := x.err.(markedError); ok { //nolint:errorlint // The decode placed the error it built.
		located.err = placed.without(errPlaced)
	}

	rel, _ := x.path.CutPrefix(paths.Doc())
	located.path, located.hasPath = rel, !rel.Equal(paths.Current())

	if located.addsNothing() {
		return located.err
	}

	return &located
}

// errorLocator finds the value that reported an error of a decode with
// no token. It walks the type the decode filled beside the node the
// decode read, as [decodewalk.Walk] pairs them, in the order the decoder
// reads them. The walk reads nothing below an interface, since the
// decoder calls no unmarshaler for the values it puts there.
//
// At each value whose type [unmarshaler.Options.ReportsOwnError] names,
// the locator decodes the node of the value into a new value of that
// type. A decode that fails with the error the locator looks for
// reproduces the error, as [errorLocator.same] compares the two. The
// walk then reads the fields, elements, or entries of that value the
// same way, as if they mirrored the document. A value that decodes
// itself through a second type with the same fields, such as an
// UnmarshalYAML that decodes into `type plain T`, returns the error of a
// field as it is, so the locator narrows to that field. The walk stops
// at the first value that reproduces the error with nothing below it
// that does. It stops too at a value whose node the decoder refuses, as
// [unmarshaler.Options.RefusesForText] reports, since the decoder reads
// nothing below that node.
//
// A path names an entry of a map by the text of its key, and the last
// entry with that text wins the path. The walk passes over an entry that
// loses its path to a later one, since no path points at it.
//
// The second decode runs the unmarshaler of each value the walk reaches
// again, on a value of its own, so the first decode keeps what it set.
// An unmarshaler that returns the same error for the same node gets the
// place of its node. One that reads state the first decode changed, such
// as a set of the names it has seen, may fail at another node or pass,
// so its error may come back at the wrong value or with no location. One
// that reads the value the caller put in the target before the decode
// decodes a zero value here, so its error may come back with no location
// too.
//
// One go-yaml decoder runs every decode of the walk, so the walk applies
// the options once.
type errorLocator struct {
	ctx     context.Context
	node    *Node
	decoder *yaml.Decoder
	// The value the walk found, or nil.
	found *decodewalk.Value
	// The message of the error to locate.
	msg string
	// The rejection of the value the walk found, as [textRejection]
	// writes it, or nil when that value reported the error itself.
	refusal error
	// The places the error to locate names, as [placesOf] lists them.
	places []locus
	// The types the options of the decode give an unmarshaler.
	unmarshalers unmarshaler.Options
	// The [*Error] in the error to locate that carries its location, and
	// that location, or the zero anchor when the error names no place.
	anchor anchor
}

// visit reads v, a value the walk reaches, and tells the walk where to
// go next. A value that decodes itself and reproduces the error is the
// one the locator found, unless a value below it reproduces the error
// too, so the walk goes below it. The walk ends at a value the decoder
// refuses, as [errorLocator.refuses] reports, and once it has left the
// values below the one it found. The value the walk starts at decodes
// in the decode itself, so only a refusal finds it there.
//
// A key of a map is the value when its type decodes itself and its
// decode reproduces the error. The walk reads nothing below a key, and
// passes over an entry no path points at.
func (l *errorLocator) visit(v *decodewalk.Value) decodewalk.Action {
	u := l.unmarshalers

	switch {
	case l.found != nil && v.Depth <= l.found.Depth:
		return decodewalk.Stop

	case v.Kind == decodewalk.Root:
		if l.refuses(v) {
			l.found = v

			return decodewalk.Stop
		}

		return decodewalk.Descend

	case v.Kind == decodewalk.Key:
		if v.Selected() && u.ReportsOwnError(v.Type) && l.reproduces(v.Node, reflect.New(v.Type).Interface()) {
			l.found = v

			return decodewalk.Stop
		}

		return decodewalk.Skip

	case !v.Named, v.Kind == decodewalk.Member && !v.Selected():
		return decodewalk.Skip

	case !u.ReportsOwnError(v.Type):
		return decodewalk.Descend

	case !l.reproduces(v.Node, reflect.New(v.Type).Interface()):
		return decodewalk.Skip
	}

	l.found = v

	if l.refuses(v) {
		return decodewalk.Stop
	}

	return decodewalk.Descend
}

// refuses reports whether the decoder refuses the node of v for its
// type, as [unmarshaler.Options.RefusesForText] reports. The value is
// then the one the walk looks for, since its decode calls no unmarshaler
// and reads nothing below the node, so refuses keeps the rejection
// [textRejection] writes for it.
func (l *errorLocator) refuses(v *decodewalk.Value) bool {
	content := datapath.Deref(l.node.doc.pathResolver(), v.Node)
	if !l.unmarshalers.RefusesForText(v.Type, content) {
		return false
	}

	l.refusal = textRejection(content)

	return true
}

// reproduces reports whether a decode of node into v, a pointer to a new
// value, fails with the error the locator looks for, as
// [errorLocator.same] compares the two. The decode reads node as a
// decode of that node alone does, with the anchors outside it that it
// refers to. A context that has ended reproduces nothing, so the walk
// ends with no location.
func (l *errorLocator) reproduces(node ast.Node, v any) bool {
	if l.ctx.Err() != nil {
		return false
	}

	view, err := l.node.prime(l.ctx, l.decoder, node)
	if err != nil {
		return false
	}

	err = decodeWithRecover(l.ctx, l.decoder, view, v)

	return err != nil && l.same(err)
}

// same reports whether again, the error of a second decode, is the error
// the locator looks for. An error that names no place is the same when
// it reads the same.
//
// An [*Error] an unmarshaler returns writes an `@` path that reads from
// the value of that unmarshaler, so the path goes under the value that
// wrote it and no other. Two errors that name a place are thus the same
// only when they name the same places, as [placesOf] lists them. A value
// that names one of its fields in an error of its own writes a path the
// field never wrote, so the walk does not narrow to the field, and the
// path goes under the value once.
//
// A value that puts text in front of the error of a value below it
// leaves that path as it is, and the path still reads from the value
// below. Two errors that name the same places are therefore the same too
// when the Error that carries the place reads the same, whatever text a
// wrapper puts around it. The walk then narrows through the wrapper to
// the value that wrote the path.
func (l *errorLocator) same(again error) bool {
	if !slices.EqualFunc(placesOf(again), l.places, sameLocus) {
		return false
	}

	if again.Error() == l.msg {
		return true
	}

	found := anchorOf(again)

	return found.err != nil && l.anchor.err != nil && found.err.Error() == l.anchor.err.Error()
}

// placesOf returns the place each error in the tree of err names: the
// one of err, of each problem it heads, and of each detail, in the order
// [ErrorTree.All] yields them. A place is the location [anchorOf] finds
// for the error, with the base of every [Rebase] above it in front of
// its path, and the zero locus for an error that names none. An error
// that heads several problems, such as a join, names no place itself, so
// the places of its problems tell it from another.
func placesOf(err error) []locus {
	var places []locus

	for node := range NewErrorTree(err).All() {
		places = append(places, anchorOf(node.Err).locus)
	}

	return places
}

// sameLocus reports whether a and b name the same place: the same path,
// or none, and the same position or range, or none.
func sameLocus(a, b locus) bool {
	return a.hasPath == b.hasPath && a.path.Equal(b.path) && a.loc == b.loc && a.ambiguous == b.ambiguous
}
