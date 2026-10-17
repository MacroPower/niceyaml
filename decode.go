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
// [Node.SelfValidate], [Source.SelfValidate], and [SelfValidateValue]
// take them too. They read only the functions of [WithValueCheck] and
// the options that say how a value decodes, as Node.SelfValidate lists
// them.
//
// Available options:
//   - [WithValidator]
//   - [WithSelfValidation]
//   - [WithValueCheck]
//   - [WithDisallowUnknownFields]
//   - [WithAllowedFieldPrefixes]
//   - [WithUnmarshaler]
//   - [WithYAMLOrderedMaps]
//   - [WithDefaults]
//   - [DecodeOptions]
//
// An option reaches only the call that gets it, so a [Validator] that
// decodes the Node it gets passes options of its own. A setting that
// describes the documents, such as [WithReferences] or [WithAliasLimit],
// belongs to the [Source], as the Decoding section of the package
// documentation describes. A Source also gives every decode of its
// documents the unmarshalers of [WithUnmarshalers], and those of
// [WithUnmarshaler] in the call apply over them.
type DecodeOption func(*decodeConfig)

// decodeConfig holds the settings a [DecodeOption] configures. Its zero
// value holds the defaults.
type decodeConfig struct {
	// The function that [WithDefaults] gives each type.
	defaults             defaultFuncs
	validators           []Validator
	allowedFieldPrefixes []string
	// The functions from [WithValueCheck], in the order given.
	checks []valueCheck
	// The paths of the keys that [hook.KnownKeys] admits.
	knownKeys []paths.Path
	// The types that the options give an unmarshaler, which the go-yaml
	// decoder does not call on its own.
	unmarshalers          unmarshaler.Options
	skipSelfValidation    bool
	disallowUnknownFields bool
	orderedMaps           bool
}

// valueCheck is a function from [WithValueCheck] for the values of one
// type. Its run function takes a pointer to the value.
type valueCheck struct {
	typ reflect.Type
	run func(ctx context.Context, v reflect.Value) error
}

// newDecodeConfig returns the settings of a decode of a document of s.
// It starts from the defaults, applies the unmarshalers of
// [WithUnmarshalers] on s over them, and then applies opts, each in
// order. A nil s stands for a value without a document, and it does not
// add an unmarshaler. The result does not share anything with another
// decode.
func newDecodeConfig(s *Source, opts []DecodeOption) decodeConfig {
	var cfg decodeConfig

	if s != nil {
		for _, u := range s.unmarshalers {
			u.applyTo(&cfg.unmarshalers)
		}
	}

	for _, opt := range opts {
		opt(&cfg)
	}

	return cfg
}

// WithValidator is a [DecodeOption] that validates the document with dv
// before the decode. A validation error ends the decode before any typed
// decoding. Each WithValidator option adds a validator. The decode runs
// them as [ChainValidator] runs them, so it runs them in the order given
// and stops at the first that fails. A caller that wants every failure
// of several validators gives one option a [MultiValidator].
//
// Validation skips a nil dv, and a dv that holds a nil pointer or func,
// such as a schema a program loads only on some paths. A
// [go.jacobcolvin.com/niceyaml/schema.Schema] checks the document against
// one JSON schema, and a [go.jacobcolvin.com/niceyaml/schema.Registry]
// against the schema it picks for the document:
//
//	config, err := doc.Decode[Config](ctx, niceyaml.WithValidator(reg))
//
// An empty document validates as any other does. Wrap a validator in
// [SkipEmpty] to pass an empty document, so the decode returns the zero
// value.
//
// [Node.SelfValidate], [Source.SelfValidate], and [SelfValidateValue] do
// not run validators, so they ignore WithValidator.
func WithValidator(dv Validator) DecodeOption {
	return func(c *decodeConfig) {
		c.validators = append(c.validators, dv)
	}
}

// WithSelfValidation is a [DecodeOption] that sets whether the decode
// runs the self-validation walk on the decoded value. The walk calls the
// Validate method of each [SelfValidator] in the value and the functions
// from [WithValueCheck]. The default is true. Validators given with
// [WithValidator] run either way. [Node.SelfValidate] and
// [Source.SelfValidate] run the walk whatever the option says, so a
// caller that turns it off for the decode validates the value later. That
// later call runs only the functions of WithValueCheck it gets, so the
// caller passes them again.
func WithSelfValidation(enabled bool) DecodeOption {
	return func(c *decodeConfig) {
		c.skipSelfValidation = !enabled
	}
}

// WithValueCheck is a [DecodeOption] that checks every value of type T in
// the decoded value with fn. It suits a check that needs the context of
// the call, and a check of a type the program cannot give a Validate
// method, such as one of another package:
//
//	allowed := niceyaml.WithValueCheck(func(ctx context.Context, p *netip.Prefix) error {
//		ok, err := tenant.Allows(ctx, *p)
//		if err != nil {
//			return niceyaml.Unchecked(err)
//		}
//
//		if !ok {
//			return niceyaml.NewError("prefix not allowed")
//		}
//
//		return nil
//	})
//
// The self-validation walk calls fn on a value after the values below it
// and its own Validate method pass, as the Validation section of the
// package documentation describes. Each WithValueCheck adds a function.
// For one value, the walk runs the functions for its type in the order
// given and stops at the first that fails, as a decode runs repeated
// [WithValidator] options. T matches by its exact type, so a check of
// string skips a value of a type such as `type Name string`. A field of
// type *T reaches fn through the value it points to, and a nil pointer
// skips fn.
//
// The `@` paths of an error from fn read from the value. The walk puts
// them under the path of the value, as it does for a Validate method. It
// places each warning that fn hands to its context the same way, such as
// one from [SelfValidateValue] or
// [go.jacobcolvin.com/niceyaml/schema.Schema.ValidateValue]. The walk
// also presumes the document at fault for an error from fn that does not
// declare a fault, a plain I/O error included, as it does for a Validate
// method. A check thus keeps its faults when it moves from a Validate
// method to fn. A check that can fail for reasons outside the document
// returns the error of such a failure inside [Unchecked], as the lookup in
// the example does.
//
// A struct field the document leaves out reaches fn as its zero value. A
// map key, a map value, and a value inside an interface reach fn as a
// pointer to a copy, so fn must not change v.
//
// The walk runs the functions one at a time, and only after the
// validators and the decoder accept the document. A program that wants
// to run the I/O of its checks in parallel thus runs those checks itself
// after Decode returns. A decode with [WithSelfValidation] off does not
// run any function, and a later [Node.SelfValidate] runs only the
// functions it gets. A call that gets one [DecodeOptions] value twice
// runs each of its functions twice.
//
// WithValueCheck panics for a pointer or interface T, because the walk
// never checks a value of such a type. A nil fn does not add a check.
func WithValueCheck[T any](fn func(ctx context.Context, v *T) error) DecodeOption {
	t := reflect.TypeFor[T]()
	if t.Kind() == reflect.Pointer || t.Kind() == reflect.Interface {
		panic(fmt.Sprintf("niceyaml.WithValueCheck: %v is a pointer or interface type, which the walk never checks", t))
	}

	return func(c *decodeConfig) {
		if fn == nil {
			return
		}

		c.checks = append(c.checks, valueCheck{typ: t, run: func(ctx context.Context, p reflect.Value) error {
			v, _ := reflect.TypeAssert[*T](p)

			return fn(ctx, v)
		}})
	}
}

// WithDisallowUnknownFields is a [DecodeOption] that sets whether a
// mapping key without a field in the target struct is an error. The
// default is false, and the decode then skips unknown keys.
//
// With the option on, a decode that fails reports every unknown field
// that the document holds, in the order of the source. Each one sits at
// the path of its key and matches [ErrUnknownField] beside [ErrDecode]:
//
//	cfg.yaml: 2 unknown fields
//	cfg.yaml:3:1: $.replicsa~: unknown field "replicsa"
//	cfg.yaml:7:5: $.servers[0].prot~: unknown field "prot"
//
// The fields join the other problems of the decode, as the Decoding
// section of the package documentation describes, and [errors.As] finds
// the [yaml.UnknownFieldError] of each. The report holds only keys the
// go-yaml decoder rejects, so it leaves out a key under a prefix that
// [WithAllowedFieldPrefixes] allows.
//
// The decoder does not reject a key below a field of an interface type.
// It also does not reject a key in a mapping that holds a key it does not
// read as a string, or in a mapping whose `<<` merge key it refuses. A
// struct that decodes itself decides for its own fields. When an
// UnmarshalYAML method decodes into a second type with the same fields,
// the decoder checks those fields. When the method parses the text
// itself, the decoder does not check them.
//
// Below a struct that decodes itself into fields that do not mirror the
// document, the report holds an unknown field only when the decoder
// names it. The report can also hold a key that a function from
// [yaml.RegisterCustomUnmarshaler] accepts. A function from
// [CustomUnmarshaler] does not have that limit.
func WithDisallowUnknownFields(disallow bool) DecodeOption {
	return func(c *decodeConfig) {
		c.disallowUnknownFields = disallow
	}
}

// WithAllowedFieldPrefixes is a [DecodeOption] that lets a mapping hold a
// key without a field when the key starts with one of prefixes, even
// under [WithDisallowUnknownFields]. Such keys include the `x-` keys that
// a format reserves for extensions:
//
//	spec, err := doc.Decode[Spec](ctx,
//		niceyaml.WithDisallowUnknownFields(true),
//		niceyaml.WithAllowedFieldPrefixes("x-"),
//	)
//
// Each WithAllowedFieldPrefixes adds to the prefixes given before it. An
// empty prefix allows every key. The option does not change a decode
// that accepts unknown fields.
func WithAllowedFieldPrefixes(prefixes ...string) DecodeOption {
	return func(c *decodeConfig) {
		c.allowedFieldPrefixes = append(c.allowedFieldPrefixes, prefixes...)
	}
}

// Unmarshaler sets how the types of the program read YAML.
// [CustomUnmarshaler] gives one type a function, and [JSONUnmarshalers]
// turns the UnmarshalJSON methods of the types on or off.
//
// A program gives its unmarshalers to the [Source] with
// [WithUnmarshalers], so every decode of the documents applies them. A
// call applies its own over them with [WithUnmarshaler]. A Source from
// [NewSourceFromLayers] takes them with [MergeUnmarshalers]. Each place
// takes the same value. In the example, a Source takes cidr with
// WithUnmarshalers, and one call on a document of another Source takes
// it with WithUnmarshaler:
//
//	cidr := niceyaml.CustomUnmarshaler(decodeCIDR)
//
//	source, err := niceyaml.NewSourceFromFile(path, niceyaml.WithUnmarshalers(cidr))
//	nets, err := doc.Decode[[]net.IPNet](ctx, niceyaml.WithUnmarshaler(cidr))
//
// A decode applies its unmarshalers in order, those of the Source first.
// A function for a type replaces the function that an earlier
// Unmarshaler gave that type, and a nil function removes it. The last
// JSONUnmarshalers applies. The zero Unmarshaler does not change
// anything.
//
// Create instances with [CustomUnmarshaler] or [JSONUnmarshalers].
type Unmarshaler struct {
	// Sets the unmarshaler in the settings of a decode, or nil for the
	// zero Unmarshaler.
	apply func(*unmarshaler.Options)
}

// applyTo sets the unmarshaler of u in o.
func (u Unmarshaler) applyTo(o *unmarshaler.Options) {
	if u.apply != nil {
		u.apply(o)
	}
}

// CustomUnmarshaler creates a new [Unmarshaler] that decodes every value
// of type T with fn, for a type the program cannot give an UnmarshalYAML
// method, such as one of another package. The decoder calls fn in place
// of decoding the fields, elements, or entries of T, and ahead of any
// unmarshaler method T has. A field of type *T decodes through fn too,
// and a null leaves it nil without calling fn. The cidr Unmarshaler
// decodes a [net.IPNet] from its CIDR text:
//
//	cidr := niceyaml.CustomUnmarshaler(func(ctx context.Context, n *net.IPNet, decode func(any) error) error {
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
// decode function decodes the value with the unmarshalers of the
// [Source] and the options of the decode. It reads an alias as the
// content of its anchor, and it does not run a [Validator]. A decode into a
// pointer to T calls fn again, so the decode function returns an error
// for such a target. A function that decodes T by its fields decodes
// into a second type with those fields, such as `type fields T`. A
// function that needs the YAML text of the value decodes into a
// [yaml.RawMessage].
//
// The decoder writes the value out as text for the decode function. A
// decode of a node that holds an alias then fails when the aliases of
// the document repeat more text than the byte limit of the Source allows.
// It also fails when they nest the document past its depth limit. Each
// error matches [ErrLimit], as the Limits section of the package
// documentation describes.
//
// The decode reads T as a type that decodes itself. An error that fn
// returns thus binds at the value, as the Decoding and Validation
// sections of the package documentation describe. A rejection from the
// decode function already names its place in the document, so fn returns
// it as it is. A fn that reads the value from elsewhere, such as from a
// secret store, wraps the error of a failed lookup in [Unchecked], so
// [IsInvalid] does not report it.
//
// A program gives the Unmarshaler to the Source with [WithUnmarshalers],
// so every decode of the documents applies it, the decode inside a
// [Validator] included. A nil fn removes the function that an earlier
// Unmarshaler or the Source gave the type. A library that gets a Source
// from its caller can thus read T by the methods of the type alone, when
// it passes CustomUnmarshaler[T](nil) to [WithUnmarshaler]. Give the
// function to CustomUnmarshaler rather than to
// [yaml.RegisterCustomUnmarshaler], whose types a decode cannot see.
//
// CustomUnmarshaler panics for a pointer type T. The decoder reads a *T
// through T, so the function for T already decodes a pointer to T.
func CustomUnmarshaler[T any](fn func(ctx context.Context, v *T, decode func(any) error) error) Unmarshaler {
	t := reflect.TypeFor[T]()
	if t.Kind() == reflect.Pointer {
		panic(fmt.Sprintf("niceyaml.CustomUnmarshaler: %v is a pointer type, "+
			"which the decoder reads through the type it points to", t))
	}

	if fn == nil {
		return Unmarshaler{apply: func(o *unmarshaler.Options) {
			*o = o.Without(t)
		}}
	}

	opt := func(decode unmarshaler.ValueDecoder) yaml.DecodeOption {
		return yaml.CustomUnmarshalerContext(func(ctx context.Context, v *T, text []byte) error {
			err := fn(ctx, v, func(dst any) error {
				// A decode into T calls fn again for the same value, so the
				// recursion never ends.
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
	}

	return Unmarshaler{apply: func(o *unmarshaler.Options) {
		*o = o.With(t, opt)
	}}
}

// shieldedError carries the error of a [CustomUnmarshaler] function past
// the go-yaml decoder, which swaps an error that wraps a [yaml.TypeError]
// for that TypeError. [decodeWithRecover] unwraps it.
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
// that matches it, as [errors.As] does, but it never finds a
// [yaml.TypeError].
func (e shieldedError) As(target any) bool {
	if _, ok := target.(**yaml.TypeError); ok {
		return false
	}

	return errors.As(e.err, target)
}

// JSONUnmarshalers creates a new [Unmarshaler] that sets whether a type
// with an UnmarshalJSON method decodes through that method. Without
// JSONUnmarshalers(true), a decode reads such a type by its fields,
// elements, or entries, as it reads a type without the method.
//
// With the methods on, the decoder converts the node to JSON, with each
// alias in the node written out in full, and hands the JSON to the
// method. It calls an UnmarshalYAML method of the type in place of
// UnmarshalJSON, and an UnmarshalText method for a node that reads as a
// string.
//
// The decode then reads such a type as one that decodes itself. The error
// that its method returns thus binds at the value. The values below it
// validate, as the Decoding and Validation sections of the package
// documentation describe. A struct that gets the method from an embedded
// field decodes the document into that field, so the field validates at
// the path of the struct.
//
// A program whose types read their documents through UnmarshalJSON gives
// JSONUnmarshalers(true) to the [Source] with [WithUnmarshalers], so
// every decode of the documents applies it, the decode inside a
// [Validator] included. A call that gets JSONUnmarshalers(false) through
// [WithUnmarshaler] turns the methods off for that call alone.
func JSONUnmarshalers(enabled bool) Unmarshaler {
	return Unmarshaler{apply: func(o *unmarshaler.Options) {
		o.JSON = enabled
	}}
}

// WithUnmarshaler is a [DecodeOption] that applies the unmarshalers us
// to the decode, in order, over the unmarshalers that [WithUnmarshalers]
// gives the [Source]. A call can thus replace or remove the function the
// Source gives a type, or turn the JSON methods off for itself alone:
//
//	cfg, err := doc.Decode[Config](ctx, niceyaml.WithUnmarshaler(niceyaml.JSONUnmarshalers(false)))
//
// A program that reads every document of a Source with the same
// unmarshalers gives them to the Source instead. A decode that this
// option does not reach, such as one inside a [Validator], then applies
// them too. WithUnmarshaler copies the slice us, so the option keeps its
// unmarshalers when the caller changes the slice it passed.
func WithUnmarshaler(us ...Unmarshaler) DecodeOption {
	us = slices.Clone(us)

	return func(c *decodeConfig) {
		for _, u := range us {
			u.applyTo(&c.unmarshalers)
		}
	}
}

// WithYAMLOrderedMaps is a [DecodeOption] that sets whether a mapping
// that decodes into an any value becomes a [yaml.MapSlice]. A MapSlice
// holds the entries in the order of the document. The default is false,
// and such a mapping then becomes a map[string]any. The option carries the
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
// [Node.Nodes], that node is one value inside the document. Those calls
// thus take the settings without the schema of the whole document.
//
// The unmarshalers that say how the types of the program read YAML go to
// the [Source] with [WithUnmarshalers] instead of into such a bundle. A
// decode that a bundle does not reach, such as one inside a [Validator],
// then applies them too.
//
// [WithValidator], [WithValueCheck], and [WithAllowedFieldPrefixes] add to
// what the options before them gave, and [WithUnmarshaler] applies its
// unmarshalers over the earlier ones, as [Unmarshaler] describes. Every
// other option replaces what an earlier one of its kind set. A call that
// gets the same DecodeOptions twice thus runs each of its validators and
// each of its value checks twice.
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
// The decode runs the steps the Decoding section of the package documentation
// lists, and the first that fails ends it. For a v that is nil, a nil
// pointer, or not a pointer, DecodeInto returns an error that wraps
// [ErrDecodeTarget] before anything runs. When the node holds an alias, the
// decode counts what a decode of the whole document reads, with each alias
// reading its content in full. When aliases make up too much of that count,
// DecodeInto returns an error that matches [ErrExcessiveAliasing], bound at
// the alias of the document that repeats the most. For a v whose type reads
// text anywhere, the error for aliases past the bounds that the byte limit
// and the depth limit of the [Source] set on them matches [ErrLimit] too.
//
// A step that fails before the go-yaml decoder runs leaves v as it is. A
// field the document leaves out keeps its value. The Decoding section
// describes how a mapping, a null, and an alias fill v. The Defaults
// section describes the value that each value the decode creates starts
// from.
//
// Every error comes back bound to the source as a [SourceError]. An
// error of the decoder matches [ErrDecode] and carries the `$` path of
// the value it rejected. A decode that fails reports every problem it
// finds in one error. A panic in the decoder or in an unmarshaler comes
// back as an error that holds a [PanicError] and does not match
// ErrDecode. The Decoding section describes the messages, the sentinels
// of each problem, and where each error binds.
func (n *Node) DecodeInto(ctx context.Context, v any, opts ...DecodeOption) error {
	return n.decodeInto(enterCall(ctx), v, newDecodeConfig(n.source, opts), nil)
}

// decodeInto is [Node.DecodeInto] with its settings resolved. It also
// collects the comments of the node in comments, unless comments is nil.
func (n *Node) decodeInto(ctx context.Context, v any, cfg decodeConfig, comments yaml.CommentMap) error {
	err := checkTarget(v, ErrDecodeTarget, true)
	if err != nil {
		return n.bindOwn(err)
	}

	// A reference document that does not parse, or one past a limit,
	// fails every decode with the error of its parse.
	if n.source.referenceErr != nil {
		return n.Bind(n.source.referenceErr)
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
		return n.aliasingError(err)
	}

	// The decoder step reports each problem as an error, whatever it wraps,
	// since the decoder stops at the first and reads no value past it.
	err = n.bindOwn(AsErrors(n.decodeUnbound(ctx, n.AST(), v, cfg, comments)))
	if err != nil {
		return err
	}

	if cfg.skipSelfValidation {
		return nil
	}

	return n.selfValidate(ctx, v, cfg)
}

// aliasingError returns err, the error of an alias check of n, bound to n
// with the document at fault. An [*aliasing.ExpansionError] binds at the
// alias that takes the text past a bound of the Source, and an
// [*aliasing.RatioError] binds at the alias that repeats the most. The
// bound error does not keep a node of the document, so [Detach] can
// release the parse. Any other error binds at the first token of n that is
// not a comment.
func (n *Node) aliasingError(err error) error {
	expansion, ok := errors.AsType[*aliasing.ExpansionError](err)
	if ok {
		limit := &markedError{err: errors.New(expansion.Error()), marks: []error{ErrLimit, ErrExcessiveAliasing}}

		return n.Bind(Invalid(limit, atToken(expansion.Alias.GetToken())))
	}

	ratio, ok := errors.AsType[*aliasing.RatioError](err)
	if ok && ratio.Alias != nil {
		excessive := &markedError{err: errors.New(ratio.Error()), marks: []error{ErrExcessiveAliasing}}

		return n.Bind(Invalid(excessive, atToken(ratio.Alias.GetToken())))
	}

	return n.Bind(Invalid(err, atToken(astnode.StartToken(n.AST()))))
}

// checkTarget returns sentinel when v is nil or a nil pointer, or, with
// needPointer set, when v is not a pointer. The go-yaml decoder panics on
// such a target or ignores it. [allocate] fills a nil pointer that v
// points to.
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

// maxPointerDepth caps the pointers that [allocate] allocates through,
// because a type such as "type P *P" never ends in a non-pointer. The
// go-yaml decoder loops forever on a non-nil value of such a type.
const maxPointerDepth = 8

// allocate allocates each nil pointer below v, a non-nil pointer, and
// returns the last. The go-yaml decoder then fills that value in place
// for a node that is not null. It returns v past [maxPointerDepth]
// pointers.
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
// applies the options of the source, then those of cfg, then extra.
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

// decodeValue decodes text, the value a [CustomUnmarshaler] function
// got, into dst with cfg, as a [Source] of its own without a [Validator].
// [valueError] rewrites the error for the outer decode to place.
func (n *Node) decodeValue(ctx context.Context, text []byte, dst any, cfg decodeConfig) error {
	err := checkTarget(dst, ErrDecodeTarget, true)
	if err != nil {
		return err
	}

	source := NewSourceFromBytes(text, func(c *sourceConfig) {
		c.allowDuplicateKeys = n.source.allowDuplicateKeys

		// The decoder wrote the text from a node that passed the limits of
		// its own Source. The decode checked the aliases of that node
		// against the bounds of those limits before the decoder wrote it.
		c.noLimits = true
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

	// The paths of the known keys name places in the document of the
	// outer decode, not in the text of the value.
	cfg.knownKeys = nil

	return valueError(doc.decodeUnbound(ctx, doc.AST(), dst, cfg, nil))
}

// decodeUnbound decodes node into v with cfg and returns the error for
// the Node to bind. A node without content or a null leaves v as it is,
// so defaults survive, but an untagged null clears a pointer or interface.
func (n *Node) decodeUnbound(
	ctx context.Context,
	node ast.Node,
	v any,
	cfg decodeConfig,
	comments yaml.CommentMap,
) error {
	// The go-yaml decoder never checks the context.
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

	view = n.admitKnownKeys(cfg, reflect.TypeOf(v).Elem(), view)

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

	var start reflect.Value

	if !null {
		target, start, err = startTarget(v, cfg, node)
		if err != nil {
			return err
		}
	}

	// The recover turns a panic in the go-yaml decoder, or in a value's
	// own UnmarshalYAML, into an error that holds a PanicError.
	err = decodeWithRecover(ctx, dec, view, target)

	// The decoder starts each value it creates from zero, so the Node
	// builds those values again from their defaults.
	if err == nil && start.IsValid() {
		err = n.fillDefaults(ctx, dec, cfg, node, target, start)
	}

	// The decoder returns the error of a value that decodes itself with
	// no token, so the Node finds the value that reported it.
	err = n.locateDecodeError(ctx, n.rejection(err, view), node, v, cfg)

	// The decoder stops at its first rejection, so the Node finds the
	// other problems of the document and reports them together.
	return n.decodeProblems(ctx, err, node, v, cfg)
}

// isNullAlias reports whether value is an alias that dec reads as null.
// Only a document body reaches a decode as an alias, and such an alias
// names an anchor of a reference document, which only dec holds.
func isNullAlias(ctx context.Context, dec *yaml.Decoder, value, view ast.Node) bool {
	if _, ok := value.(*ast.AliasNode); !ok {
		return false
	}

	var sink any

	err := decodeWithRecover(ctx, dec, view, &sink)

	return err == nil && sink == nil
}

// rejection places err, an error that the go-yaml decoder returned for
// scope without a source token, at the node in scope that caused it. The
// result is an [*Error] that matches [ErrDecode] and [errPlaced]. Any
// other err comes back as it is.
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
		at, cause = tk, &markedError{err: err, msg: msg}
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
// does not look for the value that reported it.
var errPlaced = errors.New("placed")

// markDecode returns err as an error of a decode. The result matches
// [ErrDecode], [errInvalid], and each mark that is not nil, such as the
// label of a rejection or [errPlaced]. It reads as err and unwraps to it.
func markDecode(err error, marks ...error) *markedError {
	marks = slices.DeleteFunc(append([]error{ErrDecode, errInvalid}, marks...), func(mark error) bool {
		return mark == nil
	})

	return &markedError{err: err, marks: marks}
}

// yamlMessage returns err reduced to msg, or to its own message when msg
// is empty. It seals the result, so a reporter does not print the go-yaml
// excerpt. That excerpt shows lines of the [Source] whatever
// [WithExcerpts] says.
func yamlMessage(err yaml.Error, msg string) *markedError {
	if msg == "" {
		msg = err.GetMessage()
	}

	return &markedError{err: err, msg: msg, sealed: true}
}

// asDecodeError returns err, from the go-yaml decoder, as an error that
// matches [ErrDecode]. An ended context, a panic, an err that holds an
// Error from [Unchecked] on its cause chain, and an err that already
// matches come back as they are.
func asDecodeError(err error) error {
	if err == nil || errors.Is(err, ErrDecode) || contextEnded(err) || holdsPanic(err) || holdsUnchecked(err) {
		return err
	}

	return markDecode(err)
}

// decodeWithRecover decodes node into v with dec. It turns a panic into an
// [*Error] at node that holds a [*PanicError]. The document is not at
// fault for a panic, so the error matches [errPlaced] but not
// [ErrDecode].
func decodeWithRecover(ctx context.Context, dec *yaml.Decoder, node ast.Node, v any) error {
	return decodeAtWithRecover(ctx, dec, node, v, node)
}

// decodeAtWithRecover is [decodeWithRecover] with the error of a panic at
// the first token of at, a node of the document, rather than of node.
func decodeAtWithRecover(ctx context.Context, dec *yaml.Decoder, node ast.Node, v any, at ast.Node) (err error) {
	defer func() {
		p := recover()
		if p == nil {
			return
		}

		err = Unchecked(panicked("decoder", &PanicError{Value: p, Stack: debug.Stack()}, errPlaced),
			atToken(astnode.StartToken(at)))
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
	var zero T

	start, err := newDecodeConfig(n.source, opts).defaults.start(reflect.TypeFor[T](), n.AST())
	if err != nil {
		return zero, n.bindOwn(err)
	}

	// A nil interface asserts to the zero value, which it is.
	v, _ := reflect.TypeAssert[T](start)

	err = n.DecodeInto(ctx, &v, opts...)
	if err != nil {
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
// The call decodes the node into an any, as [Node.DecodeInto] does
// without a [DecodeOption], so a program that also decodes the node reads
// it twice. A decode that fails, such as one of a document that did not
// parse, returns its error and a nil map. For a node that does not hold
// a value, YAMLComments returns an empty map.
//
// Each key is a path from the root of the document, whatever node the
// call reads. The map of a whole document thus pairs with an encode of
// its value. The map of a Node from [Node.At] does not pair with an
// encode of that node alone.
func (n *Node) YAMLComments(ctx context.Context) (yaml.CommentMap, error) {
	var value any

	comments := yaml.CommentMap{}

	cfg := newDecodeConfig(n.source, nil)
	cfg.skipSelfValidation = true

	err := n.decodeInto(ctx, &value, cfg, comments)
	if err != nil {
		return nil, err
	}

	return comments, nil
}

// prime returns the view of node in the [decodetree.Tree] and has dec read
// the anchors outside node that its aliases need. The decoder knows only
// the anchors it has decoded. Those it needs are the anchors of the
// reference documents that the aliases reach and, for a node below the
// body, the anchors of the document before node. It stops at the first
// anchor that fails.
func (n *Node) prime(ctx context.Context, dec *yaml.Decoder, node ast.Node) (ast.Node, error) {
	tree := n.doc.tree()
	view := tree.View(node)

	var anchors []*ast.AnchorNode

	if node != n.doc.root.Body {
		anchors = tree.Prime(view)
	}

	// An anchor of the document can read a reference document, but no
	// anchor of a reference document reads the document. The anchors of
	// the reference documents thus go first.
	if refs := n.source.referenceAnchors(); refs != nil {
		for _, anchor := range refs.Prime(tree.ReferenceNames(view)) {
			err := ctx.Err()
			if err != nil {
				return view, err //nolint:wrapcheck // The context names the reason, and the Node binds it.
			}

			// No token of a reference document lies in the document, so the
			// error binds at view, where an error of the decode of view
			// binds.
			var sink any

			err = decodeAtWithRecover(ctx, dec, anchor, &sink, view)
			if err != nil {
				return view, n.rejection(err, view)
			}
		}
	}

	for _, anchor := range anchors {
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
// does not select a node returns the error of At, such as
// "cfg.yaml:1:1: $.kind: not found". A document that did not parse
// returns the error that [Node.Err] returns.
//
// In a document without content, which [Node.IsEmpty] reports, every
// path with a selector returns an error that wraps [paths.ErrNoDocument].
// So does the root path when the document does not have a "---" header,
// but Decode on that root returns the zero value. On error, the returned
// T is the zero value.
//
// DecodeAt reads a value the document must hold. The decode can also
// return an error that matches [paths.ErrNotFound], such as one from a
// [Validator] that reads a required key below the node. A caller that
// falls back to a default thus calls [Node.DecodeIfPresent] instead.
//
// DecodeAt drops the scoped Node, so an `@` path in a check that the
// caller later runs on the value resolves from the receiver. A caller
// that binds such errors scopes the Node with At and binds through it, or
// moves them under path with [Rebase] before the receiver binds them:
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
// a value the document might leave out. The caller fills v with its
// default first, and a path that does not select a node leaves v as it
// is:
//
//	version := 1
//
//	found, err := doc.DecodeIfPresent(ctx, versionPath, &version)
//
// The target v must be a non-nil pointer. For any other v,
// DecodeIfPresent returns an error that wraps [ErrDecodeTarget], bound to
// the source, before the path resolves. The mistake thus shows whether or
// not the document holds the value.
//
// DecodeIfPresent returns false and a nil error only when At returns an
// error that matches [paths.ErrNotFound]. Every other failure returns
// false and the error. Those failures include the error of a document
// that did not parse, an alias on the path that does not resolve, and a
// wildcard path. They also include every error of the decode, even one
// that matches paths.ErrNotFound. A node that fails a [Validator] thus
// never reads as absent.
//
// A null is present, so `version: ~` reports true and leaves version at
// its default. When v points to a pointer or an interface, such a null
// sets that pointer or interface to nil. A path is absent when it looks
// up a key in a value that is not a mapping, or an index past the end of
// a sequence. Every path with a selector in a document without content
// is absent too. [go.jacobcolvin.com/niceyaml/schema/matcher.Exists]
// finds such paths absent as well.
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

// decodeProblems returns err, the one rejection of a failed decode, in a
// summary with the other problems a [problemCollector] finds. It returns
// the rejection alone when the rejection lacks a position or ctx ends.
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

// rejectedAt returns where err, a decoder rejection, lies in the text a
// [problemCollector] reads, which is the merged text for a layered source.
// It reports false when n does not bind rejectionErr at a position of its
// own.
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

// mergedPosition returns where rejectionErr lies in the merged text of a
// layered document. It reports false when the rejection lacks a location
// of its own or its location falls outside the text.
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

// decodeRejection returns err, from the decoder, for the Node to bind. An
// unwrapped [yaml.Error] at a source token becomes a rejection at its `$`
// path, and any other err keeps the location it carries.
func (n *Node) decodeRejection(err error) error {
	yamlErr, ok := err.(yaml.Error) //nolint:errorlint // A wrapped error is the unmarshaler's own.
	if !ok || !n.holdsToken(yamlErr.GetToken()) {
		return asDecodeError(n.doc.restoreError(err))
	}

	return n.tokenRejection(yamlErr)
}

// tokenRejection returns err, a rejection at a source token, as an
// [*Error]. The Error matches [ErrDecode] and its label, names each
// anchor as the document does, and binds where [Node.rejectionLocation]
// puts the token.
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
	// Whether the problem is the rejection of an unknown field.
	unknownField bool
}

// problemCollector finds the problems of a decode beside the one rejection
// the go-yaml decoder returned. It decodes each leaf of the walk alone,
// never inside the mapping or sequence that holds it. A leaf thus costs
// the pass the same however deep it lies. The pass confirms the rejection
// of a field or an element by decoding it again, as the one entry of a
// mapping or the one element of a sequence.
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

// walkRules returns the [decodewalk.Rules] of a decode of n with the
// unmarshalers u, for a walk that asks dec about the values it reaches.
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

// collect finds the problems of a decode of node into a value of type t.
// Those are the leaves that the decoder rejects and, when the decoder
// rejects unknown fields, the unknown fields other than the known keys of
// the decode.
func (c *problemCollector) collect(t reflect.Type, node ast.Node) {
	c.register(node)

	from := datapath.ParentAt(c.rules.Resolver, c.node.base)
	decodewalk.Walk(c.ctx, c.rules, t, node, from, c.visit)

	if !c.cfg.disallowUnknownFields {
		return
	}

	known := c.node.knownKeys(c.cfg)

	for _, field := range decodewalk.UnknownFields(c.ctx, c.rules, t, node) {
		// The search reads node, which holds the known keys that the view
		// of the decode leaves out.
		if _, ok := known[position.NewFromToken(field.Token)]; ok {
			continue
		}

		c.add(decodeProblem{
			err:          c.node.tokenRejection(field),
			at:           position.NewFromToken(field.Token),
			unknownField: true,
		})
	}
}

// register has the decoder of the pass read node into an interface once,
// after [Node.prime], so it holds each anchor as the decode held it. The
// go-yaml decoder assigns an alias the value it holds for the anchor.
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

// visit tells the walk whether to go below v and reads each leaf. Among
// values that decode themselves, it reads only durations, times, and text
// types, because any other unmarshaler might answer a second call
// differently.
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

// leaf decodes the node of v alone and adds the rejection as a problem.
// A rejection without a token goes at the path of v, and only when that
// path selects v rather than a later entry with the same key.
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

// builtRejection returns err as a rejection the decoder builds without
// code of the caller, for a value of the wrong kind or a number out of
// range.
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

// confirmed reports whether the value that holds v, a leaf, also rejects
// it as err when it holds v alone. That decode applies what the walk cannot
// see, such as a registered unmarshaler or the value held for an anchor.
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

// sameRejection reports whether again, the error of a second decode,
// matches err by type and token, or by text when neither has a token.
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

// rejectionLocation returns the options that locate a rejection at tk by
// the `$` path of its node. The rejection then binds where a [Validator]
// error at that path binds. It adds tk when a later entry with the same
// key takes the path.
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

// locateDecodeError rebases err under the path of the value that reported
// it, a value that decodes itself. The go-yaml decoder returned err
// without a token. An [errorLocator] finds the value by decoding each
// candidate again.
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

	// A decode into any runs no unmarshaler, so the decoder wrote err itself.
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

// unplaced reports whether the decode has yet to find the value err is about.
func (n *Node) unplaced(err error) bool {
	if errors.Is(err, errPlaced) || contextEnded(err) {
		return false
	}

	yamlErr, ok := err.(yaml.Error) //nolint:errorlint // A wrapped error is the unmarshaler's own.

	return !ok || !n.holdsToken(yamlErr.GetToken())
}

// lacksLocation reports whether err does not name a place, so it points
// at the value that reported it. A [*SourceError] or an [*Error] with a
// location anywhere in its tree names one.
func (n *Node) lacksLocation(err error) bool {
	return n.unplaced(err) && !holdsLocation(err)
}

// textRejection returns the rejection of content, a mapping or sequence
// that the decoder refuses for a text type. It replaces the decoder's
// error, which names neither the value nor its kind.
func textRejection(content ast.Node) error {
	return markDecode(errors.New(rejection.ExpectedString(content.Type())), ErrWrongKind)
}

// valueError returns err, from [Node.decodeValue], for the outer decode
// to place. It rewrites each problem of a summary as [valueProblem] does.
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

// valueProblem rewrites problem, from a decode of the text of one value,
// for the outer decode to place. Positions count lines of the text, so it
// keeps only paths, and a `$` path becomes an `@` path from the value.
func valueProblem(problem error) error {
	x, ok := problem.(*Error) //nolint:errorlint // The decode returns the Error it located unwrapped.
	if !ok || x == nil || x.move != nil || !x.hasLocation() {
		at := anchorOf(problem)
		if at.loc == nil {
			return problem
		}

		return wrapUndeclared(problem, AtPath(at.path))
	}

	located := *x
	located.loc = nil

	if placed, ok := x.err.(*markedError); ok { //nolint:errorlint // The decode placed the error it built.
		located.err = placed.without(errPlaced)
	}

	rel, _ := x.path.CutPrefix(paths.Doc())
	located.path, located.hasPath = rel, !rel.Equal(paths.Current())

	if located.addsNothing() {
		return located.err
	}

	return &located
}

// errorLocator finds the value that reported a decode error without a
// token. It decodes each value that decodes itself again, alone, and
// keeps the deepest that fails the same way. An unmarshaler with state
// can mislead it.
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
	// that location, or the zero anchor when the error does not name a
	// place.
	anchor anchor
}

// visit tells the walk where to go after v. A value that reproduces the
// error becomes the find, and the walk goes below it for a deeper one. The
// root decoded in the decode itself, so only a refusal finds it.
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

// refuses reports whether the decoder refuses the node of v for its type
// without calling an unmarshaler, and records the [textRejection] for it.
func (l *errorLocator) refuses(v *decodewalk.Value) bool {
	content := datapath.Deref(l.node.doc.pathResolver(), v.Node)
	if !l.unmarshalers.RefusesForText(v.Type, content) {
		return false
	}

	l.refusal = textRejection(content)

	return true
}

// reproduces reports whether a decode of node into v, a pointer to a new
// value, fails with the error the locator looks for. With an ended
// context, reproduces reports false, so the walk ends without a location.
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

// same reports whether again is the error the locator looks for. The two
// must name the same places, because an `@` path belongs to the value that
// wrote it. They must also read the same, in full or at the [*Error] that
// anchors them.
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

// placesOf returns the place, as [anchorOf] finds it, that each error in
// the tree of err names, in the order [ErrorTree.All] yields them. An
// error that does not name a place adds the zero locus.
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
