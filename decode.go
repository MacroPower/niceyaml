package niceyaml

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/aliasing"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/datapath"
	"go.jacobcolvin.com/niceyaml/internal/decodetree"
	"go.jacobcolvin.com/niceyaml/internal/decodewalk"
	"go.jacobcolvin.com/niceyaml/internal/multierr"
	"go.jacobcolvin.com/niceyaml/internal/rejection"
	"go.jacobcolvin.com/niceyaml/internal/taggedscalar"
	"go.jacobcolvin.com/niceyaml/internal/unmarshaler"
	"go.jacobcolvin.com/niceyaml/internal/yamldecode"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

// DecodeOption configures [Node.Decode], [Node.DecodeInto],
// [Node.DecodeAt], and [Node.DecodeIfPresent], and [Source.Decode] and
// [Source.DecodeInto], which decode the one document of a Source.
// [Node.SelfValidate], [Source.SelfValidate], and [SelfValidateValue]
// take them too. They read only the functions of [WithValueCheck] and
// [WithNodeCheck], the types that [WithSelfValidationOf] turns off, and
// the options that say how a value decodes, as Node.SelfValidate lists
// them.
//
// Available options:
//   - [WithValidator]
//   - [WithSelfValidation]
//   - [WithSelfValidationOf]
//   - [WithValueCheck]
//   - [WithNodeCheck]
//   - [WithUnknownFields]
//   - [WithAllowedFieldPrefixes]
//   - [WithAllowedFields]
//   - [OverrideTypeRules]
//   - [WithYAMLOrderedMaps]
//   - [DecodeOptions]
//
// An option reaches only the call that gets it, so a [Validator] that
// decodes the Node it gets passes options of its own. A setting that
// describes the documents, such as [WithReferences] or [WithAliasLimit],
// belongs to the [Source], as the Decoding section of the package
// documentation describes. A Source also gives every decode of its
// documents the rules of [WithTypeRules], and those of
// [OverrideTypeRules] in the call apply over them.
type DecodeOption func(*decodeConfig)

// decodeConfig holds the settings a [DecodeOption] configures. Its zero
// value holds the defaults.
type decodeConfig struct {
	// The function that [TypeDefaults] gives each type.
	defaults             defaultFuncs
	validators           []Validator
	allowedFieldPrefixes []string
	// The functions from [WithValueCheck] and [WithNodeCheck], in the
	// order given.
	checks []valueCheck
	// The paths of the keys that [WithAllowedFields] admits.
	knownKeys []paths.Path
	// The types whose Validate and ValidateContext methods
	// [WithSelfValidationOf] turns off.
	methodsOff []reflect.Type
	// The types that the options give an unmarshaler, which the go-yaml
	// decoder does not call on its own.
	unmarshalers       unmarshaler.Options
	unknownFields      UnknownFieldsMode
	skipSelfValidation bool
	orderedMaps        bool
}

// valueCheck is a function from [WithValueCheck] or [WithNodeCheck] for
// the values of one type. Its run function takes the [*Node] of the value
// and a pointer to the value. A function from WithValueCheck does not read
// the Node, so node is false for it, and the walk passes it nil.
type valueCheck struct {
	typ  reflect.Type
	run  func(ctx context.Context, n *Node, v reflect.Value) error
	node bool
}

// newDecodeConfig returns the settings of a decode of a document of s.
// It starts from the defaults, applies the rules of [WithTypeRules] on s
// over them, and then applies opts, each in order. A nil s stands for a
// value without a document, and it does not add a rule. The result does
// not share anything with another decode.
func newDecodeConfig(s *Source, opts []DecodeOption) decodeConfig {
	var cfg decodeConfig

	if s != nil {
		for _, r := range s.typeRules {
			r.applyTo(&cfg)
		}
	}

	for _, opt := range opts {
		opt(&cfg)
	}

	return cfg
}

// WithValidator is a [DecodeOption] that validates the document with dv
// before the decode. A validation error ends the decode before the decoder
// runs. The decode then does not call any Validate or ValidateContext
// method, so the warnings of those methods do not reach the [Warnings]
// collector of the context. Each WithValidator option adds a validator.
// The decode runs them as
// [ChainValidator] runs them, so it runs them in the order given and stops
// at the first that fails. A caller that wants every failure of several
// validators gives one option a [MultiValidator]. The decode presumes the
// document at fault for each error of a validator that does not declare
// anything, as [Validator] describes. The decode runs the check of
// [Node.CheckAliasLimit] before the validators, and the validators do not
// run when that check fails. A decode into a type that reads text counts
// the text that the aliases write out after the validators, as the Decoding
// section of the package documentation describes.
//
// A [go.jacobcolvin.com/niceyaml/schema.Schema] checks the document
// against one JSON schema, and a
// [go.jacobcolvin.com/niceyaml/schema.Registry] against the schema it
// picks for the document:
//
//	config, err := doc.Decode[Config](ctx, niceyaml.WithValidator(reg))
//
// WithValidator panics if dv is nil, including a dv that holds a nil
// pointer or func, such as the nil schema that a dropped error leaves
// behind. A program that loads a schema only on some condition passes
// the Validator from [NoValidator] where it does not have one.
//
// An empty document validates as any other does. Wrap a validator in
// [SkipEmpty] to pass an empty document, so the decode returns the zero
// value.
//
// [Node.SelfValidate], [Source.SelfValidate], and [SelfValidateValue] do
// not run validators, so they ignore WithValidator.
func WithValidator(dv Validator) DecodeOption {
	mustValidator("WithValidator", dv)

	return func(c *decodeConfig) {
		c.validators = append(c.validators, dv)
	}
}

// WithSelfValidation is a [DecodeOption] that sets whether the decode runs the
// self-validation walk on the decoded value. The walk calls the Validate method
// of each [SelfValidator] in the value, the ValidateContext method of each
// [SelfValidatorContext], and the functions from [WithValueCheck] and
// [WithNodeCheck]. The default is true. Validators given with [WithValidator]
// run either way. [Node.SelfValidate] and [Source.SelfValidate] run the walk
// whatever the option says, so a caller that turns it off for the decode
// validates the value later. That later call runs only the functions of
// WithValueCheck and WithNodeCheck it gets, so the caller passes them again.
// [WithSelfValidationOf] turns off the methods of one type and keeps the
// rest of the walk.
func WithSelfValidation(enabled bool) DecodeOption {
	return func(c *decodeConfig) {
		c.skipSelfValidation = !enabled
	}
}

// WithSelfValidationOf is a [DecodeOption] that sets whether the
// self-validation walk calls the Validate method of [SelfValidator] and the
// ValidateContext method of [SelfValidatorContext] on each value of type
// T. The default is true. The walk calls those methods on every type that
// declares one, including a type of another module. The method of such a
// type can serve another purpose, such as a check that must run after the
// program provisions the value. A caller turns such a type off:
//
//	cfg, err := src.Decode[Config](ctx, niceyaml.WithSelfValidationOf[vendor.Client](false))
//
// With false, the walk still validates the values below each value of
// type T. It also still runs the functions of [WithValueCheck] and
// [WithNodeCheck] for T. A caller thus replaces the method with a function
// of its own. That function can call the method itself and wrap its error
// in [Unchecked]. The error then does not count as a fault of the
// document:
//
//	client := niceyaml.WithValueCheck(func(ctx context.Context, c *vendor.Client) error {
//		c.Provision()
//
//		err := c.Validate()
//		if err != nil {
//			return niceyaml.Unchecked(err)
//		}
//
//		return nil
//	})
//
// T matches by its exact type, as it does for WithValueCheck, so the
// option covers a value of type T and a value that a field of type *T
// points to. The last WithSelfValidationOf for T in the call sets the
// value. [WithSelfValidation] set to false turns the whole walk off,
// whatever this option says. [Node.SelfValidate], [Source.SelfValidate],
// and [SelfValidateValue] read this option too.
//
// WithSelfValidationOf panics for a pointer or interface T, because the
// walk never validates a value of such a type.
func WithSelfValidationOf[T any](enabled bool) DecodeOption {
	t := reflect.TypeFor[T]()
	if t.Kind() == reflect.Pointer || t.Kind() == reflect.Interface {
		panic(fmt.Sprintf(
			"niceyaml.WithSelfValidationOf: %v is a pointer or interface type, which the walk never validates", t,
		))
	}

	return func(c *decodeConfig) {
		off := slices.DeleteFunc(slices.Clone(c.methodsOff), func(o reflect.Type) bool {
			return o == t
		})

		if !enabled {
			off = append(off, t)
		}

		c.methodsOff = off
	}
}

// WithValueCheck is a [DecodeOption] that checks every value of type T in
// the decoded value with fn. It suits a rule that applies to one call,
// and a check of a type the program cannot give a Validate method, such
// as one of another package. A type that the program owns and whose
// check needs the context of the call implements [SelfValidatorContext]
// instead, so every decode runs that check:
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
// and after its own Validate or ValidateContext method, whatever they
// return. The Validation section of the package documentation describes
// that order. Each WithValueCheck adds a function. For one value, the walk
// runs every function for its type in the order given and reports the
// failure of each. A linter thus gives each independent rule its own
// WithValueCheck. An fn that reads state that another check guarantees
// guards it itself.
// For example, it returns nil when a field it depends on is empty. It can
// also call the Validate method of the value first and return nil when
// [HasErrors] reports an error in the result. The result of a Validate
// that only warns is not nil. A guard that tests it against nil thus
// skips fn on every value that only warns. For a type that implements
// [SelfValidatorContext], the guard calls ValidateContext with the ctx of
// fn in place of Validate. T matches by its exact type,
// so a check of string skips a value of a type such as `type Name
// string`. A field of type *T reaches fn through the value it points to,
// and a nil pointer skips fn.
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
// Only the context of the call stops the walk. An fn that holds a deadline
// of its own can return an error that matches [context.DeadlineExceeded]
// when the context of the call is still live. The walk reports that error
// at the value and checks the values that remain. The Cancellation section
// of the package documentation describes both cases.
//
// The walk does not presume the document at fault for a result of fn that
// matches [context.Canceled] or [context.DeadlineExceeded]. A join with
// one such branch matches as a whole, so the walk does not presume a fault
// for any problem of that join. An fn that returns other problems beside a
// timeout should declare each of them with [NewError] or [Invalid].
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
// The walk returns a panic in fn as an error that holds a [PanicError],
// bound at the value. WithValueCheck panics for a pointer or interface T,
// because the walk never checks a value of such a type. It also panics if
// fn is nil.
func WithValueCheck[T any](fn func(ctx context.Context, v *T) error) DecodeOption {
	if fn == nil {
		return addCheck[T]("WithValueCheck", false, nil)
	}

	return addCheck("WithValueCheck", false, func(ctx context.Context, _ *Node, v *T) error {
		return fn(ctx, v)
	})
}

// WithNodeCheck is a [DecodeOption] that checks every value of type T in
// the decoded value with fn, as [WithValueCheck] does. It hands fn the
// [*Node] of the value beside the value. A check that needs to know which
// file or which layer set a value reads it from n. For example, a path
// that resolves beside the file that sets it reads the directory from the
// origin of its Node. Each value of a merged [Source] then resolves in
// the directory of its own layer. The origin names a file in its
// [Node.FS] when it has a file system, and a file on disk when it does
// not. The check skips a value that a reference document sets, because
// that value does not have a layer:
//
//	exists := niceyaml.WithNodeCheck(func(ctx context.Context, n *niceyaml.Node, f *File) error {
//		if n == nil || *f == "" {
//			return nil
//		}
//
//		origin, err := n.Origin()
//		if errors.Is(err, paths.ErrAlias) {
//			return nil
//		}
//
//		if err != nil {
//			return err
//		}
//
//		if fsys := origin.FS(); fsys != nil {
//			_, err = fs.Stat(fsys, path.Join(path.Dir(origin.FilePath()), string(*f)))
//		} else {
//			_, err = os.Stat(filepath.Join(filepath.Dir(origin.FilePath()), string(*f)))
//		}
//
//		if err != nil {
//			return niceyaml.Unchecked(err)
//		}
//
//		return nil
//	})
//
// The functions of WithNodeCheck and WithValueCheck share one list. The
// walk runs every function for the type of a value in the order given,
// whichever option added it. Everything else that WithValueCheck
// describes holds for fn too. For example, T matches by its exact type,
// and the walk places the errors and the warnings of fn under the path of
// the value. The package documents rules for the order, the type match,
// the errors, and the warnings of the functions of WithValueCheck. Each
// of those rules covers the functions of WithNodeCheck too.
//
// The Node n is the one that [Node.At] gives for the path of the value
// below the Node that the walk validates. A map key gets the Node at the
// path of its entry with a `~` after it. On a merged Source, n reads the
// merged document, so fn calls [Node.Origin] to learn the layer that set
// the value.
//
// A layer can read a value through an alias to a reference document of
// [WithReferences], or bring one in with a `<<` merge key from such a
// document. The merged document holds that value, so fn gets a Node for
// it. The value does not come from the file of any layer, so Origin
// returns an error that wraps
// [go.jacobcolvin.com/niceyaml/paths.ErrAlias] for it. A function that
// returns that error fails the decode of a valid document. A function
// that does not need a layer for such a value returns nil for it, as the
// example does.
//
// The walk passes a nil n when the document does not hold the value at its
// path, so fn must handle a nil n. The walk passes a nil n in these cases:
//
//   - The walk runs under [SelfValidateValue], which does not have a
//     document.
//   - The value is a struct field that the document leaves out, such as
//     one that a function of [TypeDefaults] fills.
//   - The value lies below a value that decodes itself, through its own
//     unmarshaler method or a [CustomUnmarshaler] function. The value
//     that decodes itself gets its Node.
//   - The value lies below a map entry whose path names the entries of
//     several keys, as [ErrAmbiguousPath] describes.
//   - Node.At fails for the path of the value, such as a path below an
//     alias to a reference document in a Source that is not merged, or
//     the document did not parse.
//
// The walk returns a panic in fn as an error that holds a [PanicError],
// bound at the value. WithNodeCheck panics for a pointer or interface T,
// because the walk never checks a value of such a type. It also panics if
// fn is nil.
func WithNodeCheck[T any](fn func(ctx context.Context, n *Node, v *T) error) DecodeOption {
	return addCheck("WithNodeCheck", true, fn)
}

// addCheck returns the [DecodeOption] that the function name returns for
// fn, which reads its [*Node] when node is true. It panics for a pointer
// or interface T and for a nil fn.
func addCheck[T any](name string, node bool, fn func(ctx context.Context, n *Node, v *T) error) DecodeOption {
	t := reflect.TypeFor[T]()
	if t.Kind() == reflect.Pointer || t.Kind() == reflect.Interface {
		panic(fmt.Sprintf("niceyaml.%s: %v is a pointer or interface type, which the walk never checks", name, t))
	}

	if fn == nil {
		panic(fmt.Sprintf("niceyaml.%s: fn is nil", name))
	}

	return func(c *decodeConfig) {
		run := func(ctx context.Context, n *Node, p reflect.Value) error {
			v, _ := reflect.TypeAssert[*T](p)

			return fn(ctx, n, v)
		}

		c.checks = append(c.checks, valueCheck{typ: t, node: node, run: run})
	}
}

// UnknownFieldsMode sets what a decode does with each unknown field, which
// is a mapping key that does not have a field in the target struct.
// [WithUnknownFields] takes one. Its text is "ignore", "warn", or
// "reject", so a program reads a flag into one with [flag.TextVar]. A
// decode under "warn" fails without a [Warnings] collector on its
// context, so the program puts one there and prints what it collects:
//
//	mode := niceyaml.UnknownFieldsReject
//	flag.TextVar(&mode, "unknown-fields", mode, "ignore, warn, or reject unknown fields")
//	flag.Parse()
//
//	warnings := niceyaml.NewWarnings()
//
//	cfg, err := src.Decode[Config](warnings.Context(ctx), niceyaml.WithUnknownFields(mode))
//	if err != nil {
//		return warnings.Report(err)
//	}
//
//	if w := warnings.Err(); w != nil {
//		log.Print(niceyaml.FormatError(w))
//	}
type UnknownFieldsMode int

const (
	// UnknownFieldsIgnore skips each unknown field. It is the default.
	UnknownFieldsIgnore UnknownFieldsMode = iota
	// UnknownFieldsWarn decodes as UnknownFieldsIgnore does, and it hands
	// each unknown field to the [Warnings] collector of the context as a
	// warning. A decode whose context does not carry a collector fails
	// with an error that wraps [ErrNoWarnings].
	UnknownFieldsWarn
	// UnknownFieldsReject fails the decode with an error for each unknown
	// field.
	UnknownFieldsReject
)

// String returns the name of m: "ignore", "warn", or "reject". For a value
// that is not one of the [UnknownFieldsMode] constants, it returns the
// value in the form "UnknownFieldsMode(3)".
func (m UnknownFieldsMode) String() string {
	switch m {
	case UnknownFieldsIgnore:
		return "ignore"
	case UnknownFieldsWarn:
		return "warn"
	case UnknownFieldsReject:
		return "reject"
	default:
		return "UnknownFieldsMode(" + strconv.Itoa(int(m)) + ")"
	}
}

// MarshalText implements [encoding.TextMarshaler]. The text is the name
// that [UnknownFieldsMode.String] returns. For a value that is not one of
// the UnknownFieldsMode constants, MarshalText returns an error that wraps
// [ErrUnknownFieldsMode].
func (m UnknownFieldsMode) MarshalText() ([]byte, error) {
	if m < UnknownFieldsIgnore || m > UnknownFieldsReject {
		return nil, fmt.Errorf("%w: %v", ErrUnknownFieldsMode, m)
	}

	return []byte(m.String()), nil
}

// UnmarshalText implements [encoding.TextUnmarshaler]. It reads the name
// of a mode, "ignore", "warn", or "reject", as [UnknownFieldsMode.String]
// writes it. For any other text, it returns an error that wraps
// [ErrUnknownFieldsMode] and leaves m as it was.
func (m *UnknownFieldsMode) UnmarshalText(text []byte) error {
	for mode := UnknownFieldsIgnore; mode <= UnknownFieldsReject; mode++ {
		if string(text) == mode.String() {
			*m = mode

			return nil
		}
	}

	return fmt.Errorf("%w: %q, want ignore, warn, or reject", ErrUnknownFieldsMode, text)
}

// WithUnknownFields is a [DecodeOption] that sets what the decode does with
// each unknown field, as [UnknownFieldsMode] describes. The default is
// [UnknownFieldsIgnore]. WithUnknownFields panics for a mode that is not
// one of the UnknownFieldsMode constants.
//
// Under [UnknownFieldsReject], a decode that fails reports every unknown
// field that the document holds, in the order of the source. Each one sits
// at the path of its key and matches [ErrUnknownField] beside [ErrDecode]:
//
//	cfg.yaml: 2 unknown fields
//	cfg.yaml:3:1: $.replicsa~: unknown field "replicsa"
//	cfg.yaml:7:5: $.servers[0].prot~: unknown field "prot"
//
// The fields join the other problems of the decode, as the Decoding
// section of the package documentation describes, and [errors.As] finds
// the [yaml.UnknownFieldError] of each.
//
// A key inside an anchor that several aliases or `<<` merge keys read is an
// unknown field once for each use. Each error or warning for that key has
// the path of its use, at the position of the key in the anchor. Under both
// modes, a key that three aliases read thus gives three errors or warnings.
//
// Under [UnknownFieldsWarn], the decoder fills the value as it does under
// UnknownFieldsIgnore. The decode then hands the same fields to the
// [Warnings] collector of ctx, whether the decoder accepts the document or
// not. The self-validation walk still runs. Each warning matches
// ErrUnknownField and ErrDecode, as each error under UnknownFieldsReject
// does:
//
//	warnings := niceyaml.NewWarnings()
//
//	cfg, err := doc.Decode[Config](warnings.Context(ctx), niceyaml.WithUnknownFields(niceyaml.UnknownFieldsWarn))
//
// Under UnknownFieldsWarn, a decode whose context does not carry a
// [Warnings] collector returns an error that wraps [ErrNoWarnings] before
// anything runs. The error comes inside [Unchecked], because the mistake
// lies in the program and not in the document. A hook of a call, such as a
// [Validator], gets a context that carries a collector, so a decode inside
// it passes the check.
//
// Under UnknownFieldsReject, the decoder rejects each unknown field. Under
// UnknownFieldsWarn, the decoder finds the same fields as it decodes and
// hands them to the collector in place of failing, even when the rest of
// the document fails. The decode thus calls each unmarshaler the same
// number of times in both modes. A callback of an unmarshaler does not
// fail for an unknown field under UnknownFieldsWarn, and the decoder warns
// of each such field. A call of the callback that fails for another reason
// warns of its fields only when the unmarshaler returns the error of that
// call unchanged. An unmarshaler that drops the error might read the value
// another way.
//
// UnknownFieldsWarn and UnknownFieldsReject differ beyond the severity of
// the fields. Under UnknownFieldsReject, a decode with an unknown field
// fails, so the Validate and ValidateContext methods of the value do not
// run. Under UnknownFieldsWarn, they run when the decoder accepts the rest
// of the document.
//
// The decoder checks the fields of a value that decodes itself only where
// the unmarshaler of that value decodes them through the callback of the
// decoder, such as an UnmarshalYAML method that decodes into a second type
// with the same fields. A field below a value that a function of
// [CustomUnmarshaler] decodes comes back inside the error of that value.
// UnknownFieldsReject reports that error, and UnknownFieldsWarn does not
// warn of the field.
//
// A key of a reference document binds at the alias or the `<<` merge key
// that led the decoder to the key, as the References section of the
// package documentation describes. The decoder reads the last entry of a
// key, and a merge key brings in the entries of its sources where it
// stands. So neither mode reports an entry that a later entry of its key
// replaces, such as one that a later merge key brings in.
//
// A decode that a [Validator] fails does not reach the decoder, so it does
// not report any unknown field. Neither UnknownFieldsWarn nor
// UnknownFieldsReject reports these keys:
//
//   - A key under a prefix that [WithAllowedFieldPrefixes] allows.
//   - A key at a path that [WithAllowedFields] admits.
//   - A key below a field of an interface type.
//   - A key in a mapping that holds a key that the decoder does not read as
//     a string.
//   - A key in a mapping whose `<<` merge key the decoder refuses.
//
// A map that a struct holds inline takes every key of the mapping,
// including the keys that other fields of the struct read. The struct
// thus does not have an unknown field. A struct that decodes itself
// still decides for its own fields, because its UnmarshalYAML method
// might not read the map. The decoder checks the fields of each member of
// an inline map, as it checks those of any other value.
//
// A struct that decodes itself decides for its own fields. When an
// UnmarshalYAML method decodes into a second type with the same fields,
// the decoder checks those fields. When the method parses the text itself,
// the decoder does not check them.
//
// A decode does not call a function from [yaml.RegisterCustomUnmarshaler],
// so it checks the fields of a type that such a function decodes, as it
// checks those of any other type. Give the function to [CustomUnmarshaler]
// instead.
func WithUnknownFields(mode UnknownFieldsMode) DecodeOption {
	if mode < UnknownFieldsIgnore || mode > UnknownFieldsReject {
		panic(fmt.Sprintf("niceyaml.WithUnknownFields: %v is not one of the UnknownFieldsMode constants", mode))
	}

	return func(c *decodeConfig) {
		c.unknownFields = mode
	}
}

// WithAllowedFieldPrefixes is a [DecodeOption] that lets a mapping hold a
// key without a field when the key starts with one of prefixes, even
// under [UnknownFieldsReject] or [UnknownFieldsWarn]. Such keys include
// the `x-` keys that a format reserves for extensions:
//
//	spec, err := doc.Decode[Spec](ctx,
//		niceyaml.WithUnknownFields(niceyaml.UnknownFieldsReject),
//		niceyaml.WithAllowedFieldPrefixes("x-"),
//	)
//
// Each WithAllowedFieldPrefixes adds to the prefixes given before it. An
// empty prefix allows every key. The option does not change a decode
// under [UnknownFieldsIgnore].
func WithAllowedFieldPrefixes(prefixes ...string) DecodeOption {
	return func(c *decodeConfig) {
		c.allowedFieldPrefixes = append(c.allowedFieldPrefixes, prefixes...)
	}
}

// WithAllowedFields is a [DecodeOption] that lets the document hold the
// key at each of keys, even under [UnknownFieldsReject] or
// [UnknownFieldsWarn]. A loader that picks the Go type from a field such
// as "type" or "kind" admits that field. The decode still rejects a key
// of the same name that a nested mapping writes:
//
//	s3, err := doc.Decode[S3](ctx,
//		niceyaml.WithUnknownFields(niceyaml.UnknownFieldsReject),
//		niceyaml.WithAllowedFields(paths.Current().Child("type")),
//	)
//
// Each key names an entry by the path of its value, such as `$.type`, or
// by the path of its key, such as `$.type~`. A `$` path reads from the
// root of the document. An `@` path reads from the node that the decode
// reads, so it suits a decode with [Node.DecodeAt] or on a Node from
// [Node.At]. A path with a `[*]`, `.*`, `..name`, or `..*` selector admits
// the key of every entry that it selects. A path that does not select an
// entry below the node that the decode reads admits nothing.
// [WithAllowedFieldPrefixes] instead admits a name at any depth.
//
// The decode admits each key where the document writes it. A path that
// reaches its entry through an alias or a `<<` merge key finds the key
// in the anchored mapping. The decode reads that key at the anchor and at
// every alias and merge key that reads the anchor. With `$.nested.type`
// admitted, the decode thus also accepts the key at `$.base.type` here.
// It does so when the type of base does not have a field for the key:
//
//	base: &b
//	  type: x
//	nested: *b
//
// A path whose mapping writes the key itself finds that key, so a `<<`
// merge key in that mapping does not admit the key of its anchor.
//
// A read keeps an admitted key when a field of a struct, a map, or an
// interface takes the key. The decoder fills each field that takes it. A
// value of a node type of the go-yaml ast package keeps every key below
// it, so it gets the node of the document. When one read of a key keeps
// it, each read of the key that does not keep it reports an unknown
// field. In the document shown, a type of
// base with a type field thus has the decode report `$.nested.type~`
// when the type of nested does not have one.
//
// A struct can hold inline a field of a type other than a struct, such
// as a map, directly or through inline structs. That field takes every
// key of the mapping, so the struct keeps each admitted key, and the
// field holds the key under every [UnknownFieldsMode]. In the document
// shown, a type of base that holds a map inline thus has the decode
// report `$.nested.type~` when the type of nested does not have a field
// for the key. An inline field that reads an anchor, as a
// `yaml:",inline,alias"` tag sets, does not take the keys of the mapping.
//
// The decode leaves out each admitted key that no read keeps. A value
// that decodes itself, such as one with an UnmarshalYAML method, does not
// keep an admitted key below it. Its unmarshaler thus sees the key only
// when another read keeps it, and it gets a copy of its node without the
// key. An admitted key that the decode leaves out
// also hides each entry of its name that a `<<` merge key before it
// brings in. The decode leaves those entries out too. A [Validator] of
// the decode still sees each key.
//
// Each WithAllowedFields adds to the keys given before it. The option
// does not change a decode under [UnknownFieldsIgnore].
func WithAllowedFields(keys ...paths.Path) DecodeOption {
	keys = slices.Clone(keys)

	return func(c *decodeConfig) {
		c.knownKeys = append(c.knownKeys, keys...)
	}
}

// TypeRule sets how a decode reads the values of a type of the program.
// [CustomUnmarshaler] gives one type a function that reads its YAML,
// [TypeDefaults] gives one type a function that sets the value a decode
// starts from, and [JSONUnmarshalers] turns the UnmarshalJSON methods of
// the types on or off.
//
// A program gives its rules to the [Source] with [WithTypeRules], so
// every decode of the documents applies them. A call applies its own
// over them with [OverrideTypeRules]. A Source from
// [NewSourceFromLayers] takes them with [MergeTypeRules]. Each place
// takes the same value. In the example, a Source takes cidr with
// WithTypeRules, and one call on a document of another Source takes it
// with OverrideTypeRules:
//
//	cidr := niceyaml.CustomUnmarshaler(decodeCIDR)
//
//	source, err := niceyaml.NewSourceFromFile(path, niceyaml.WithTypeRules(cidr))
//	nets, err := doc.Decode[[]net.IPNet](ctx, niceyaml.OverrideTypeRules(cidr))
//
// A decode applies its rules in order, those of the Source first. A
// function for a type replaces the function of the same kind that an
// earlier TypeRule gave that type, and a nil function removes it. The
// last JSONUnmarshalers applies. The zero TypeRule does not change
// anything.
//
// Create instances with [CustomUnmarshaler], [TypeDefaults], or
// [JSONUnmarshalers].
type TypeRule struct {
	// Sets the rule in the settings of a decode, or nil for the zero
	// TypeRule.
	apply func(*decodeConfig)
}

// applyTo sets the rule of r in c.
func (r TypeRule) applyTo(c *decodeConfig) {
	if r.apply != nil {
		r.apply(c)
	}
}

// typeRulesConfig returns the settings that rules give a decode when it
// applies them, in order, to the zero settings.
func typeRulesConfig(rules []TypeRule) decodeConfig {
	var cfg decodeConfig

	for _, r := range rules {
		r.applyTo(&cfg)
	}

	return cfg
}

// CustomUnmarshaler creates a new [TypeRule] that decodes every value
// of type T with fn, for a type the program cannot give an UnmarshalYAML
// method, such as one of another package. The decoder calls fn in place
// of decoding the fields, elements, or entries of T, and ahead of any
// unmarshaler method T has. A field of type *T decodes through fn too,
// and a null leaves it nil without calling fn. The cidr TypeRule
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
// decode function decodes the value with the type rules of the [Source],
// defaults included, and the options of the decode. It reads an alias as the
// content of its anchor, and it does not run a [Validator]. A decode into a
// pointer to T calls fn again, so the decode function returns an error
// for such a target. A function that decodes T by its fields decodes
// into a second type with those fields, such as `type fields T`. A
// function that needs the YAML text of the value decodes into a
// [yaml.RawMessage].
//
// The decoder writes the value out as text for the decode function. A
// decode of a value that holds an alias then fails when the aliases of
// the document repeat more text than the document byte limit of the Source
// allows.
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
// A program gives the TypeRule to the Source with [WithTypeRules], so
// every decode of the documents applies it, the decode inside a
// [Validator] included. A nil fn removes the function that an earlier
// TypeRule or the Source gave the type. A library that gets a Source
// from its caller can thus read T by the methods of the type alone, when
// it passes CustomUnmarshaler[T](nil) to [OverrideTypeRules].
//
// A decode does not call a function from [yaml.RegisterCustomUnmarshaler],
// so it reads the type of such a function as it reads a type without one.
// Give the function to CustomUnmarshaler instead.
//
// CustomUnmarshaler panics for a pointer type T. The decoder reads a *T
// through T, so the function for T already decodes a pointer to T.
func CustomUnmarshaler[T any](fn func(ctx context.Context, v *T, decode func(any) error) error) TypeRule {
	t := reflect.TypeFor[T]()
	if t.Kind() == reflect.Pointer {
		panic(fmt.Sprintf("niceyaml.CustomUnmarshaler: %v is a pointer type, "+
			"which the decoder reads through the type it points to", t))
	}

	if fn == nil {
		return TypeRule{apply: func(c *decodeConfig) {
			c.unmarshalers = c.unmarshalers.Without(t)
		}}
	}

	opt := func(decode unmarshaler.ValueDecoder) yamldecode.DecodeOption {
		return yamldecode.CustomUnmarshalerContext(func(ctx context.Context, v *T, text []byte) error {
			return fn(ctx, v, func(dst any) error {
				// A decode into T calls fn again for the same value, so the
				// recursion never ends.
				if dt := reflect.TypeOf(dst); dt != nil && unmarshaler.Base(dt) == t {
					return fmt.Errorf("decode target is the type the function decodes: got %T", dst)
				}

				return decode(ctx, text, dst)
			})
		})
	}

	return TypeRule{apply: func(c *decodeConfig) {
		c.unmarshalers = c.unmarshalers.With(t, opt)
	}}
}

// JSONUnmarshalers creates a new [TypeRule] that sets whether a type
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
// JSONUnmarshalers(true) to the [Source] with [WithTypeRules], so every
// decode of the documents applies it, the decode inside a [Validator]
// included. A call that gets JSONUnmarshalers(false) through
// [OverrideTypeRules] turns the methods off for that call alone.
func JSONUnmarshalers(enabled bool) TypeRule {
	return TypeRule{apply: func(c *decodeConfig) {
		c.unmarshalers.JSON = enabled
	}}
}

// OverrideTypeRules is a [DecodeOption] that applies rules to the
// decode, in order, over the rules that [WithTypeRules] gives the
// [Source]. A call can thus replace or remove a function that the Source
// gives a type, or turn the JSON methods off for itself alone:
//
//	cfg, err := doc.Decode[Config](ctx, niceyaml.OverrideTypeRules(niceyaml.JSONUnmarshalers(false)))
//
// A step that must see the document without the defaults that the Source
// gives its types removes the function of each type that its value holds,
// for its own call:
//
//	raw, err := n.Decode[Item](ctx, niceyaml.OverrideTypeRules(
//		niceyaml.TypeDefaults[Item](nil),
//		niceyaml.TypeDefaults[Printer](nil),
//	))
//
// A program that reads every document of a Source with the same rules
// gives them to the Source instead. A decode that this option does not
// reach, such as one inside a [Validator], then applies them too.
// OverrideTypeRules copies the slice rules, so the option keeps its rules
// when the caller changes the slice it passed.
func OverrideTypeRules(rules ...TypeRule) DecodeOption {
	rules = slices.Clone(rules)

	return func(c *decodeConfig) {
		for _, r := range rules {
			r.applyTo(c)
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
//	settings := niceyaml.DecodeOptions(niceyaml.WithUnknownFields(niceyaml.UnknownFieldsReject))
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
// A [TypeRule] that every decode of the documents must apply goes to the
// [Source] with [WithTypeRules] instead of into such a bundle. A decode
// that a bundle does not reach, such as one inside a [Validator], then
// applies it too. A rule that only the decodes of the bundle apply, such
// as defaults that a [Validator] must not see, goes into the bundle
// through [OverrideTypeRules].
//
// [WithValidator], [WithValueCheck], [WithNodeCheck],
// [WithAllowedFieldPrefixes], and [WithAllowedFields] add to what the
// options before them gave, and [OverrideTypeRules] applies its rules over
// the earlier ones, as [TypeRule] describes. Every other option replaces
// what an earlier one of its kind set. A call that gets the same
// DecodeOptions twice thus runs each of its validators and each of its
// value checks twice.
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
// [ErrDecodeTarget] before anything runs. So does a decode under
// [UnknownFieldsWarn] whose ctx does not carry a [Warnings] collector,
// with an error that wraps [ErrNoWarnings]. When the value that the decode
// reads holds an alias, the decode counts what a decode of the whole
// document reads, with each alias reading its content in full. For a Node
// at a local alias without a tag that lies outside the content of its
// anchor, that value is the content of the anchor.
// When aliases make up too much of that count, DecodeInto returns an
// error that matches [ErrExcessiveAliasing], bound at the alias of the
// document that repeats the most. For a v whose type reads
// text anywhere, the error for aliases past the bounds that the document
// byte limit and the depth limit of the [Source] set on them matches
// [ErrLimit] too.
//
// A step that fails before the go-yaml decoder runs leaves v as it is. A
// field the document leaves out keeps its value. The Decoding section
// describes how a mapping, a null, and an alias fill v. The Defaults
// section describes the value that each value the decode creates starts
// from. A decode into a v that [Source.Defaults] or [Source.DefaultsInto]
// filled under the same options returns what [Node.Decode] returns. A
// caller whose type is a runtime value, such as a [reflect.Type], fills v
// with Source.DefaultsInto. A v that points to a nil
// pointer starts from the defaults too, except for a null or empty
// document, which leaves that pointer nil.
//
// Every error comes back bound to the source as a [SourceError]. An
// error of the decoder matches [ErrDecode] and carries the `$` path of
// the value it rejected. A decode that fails reports every problem it
// finds in one error. A panic in the decoder or in an unmarshaler comes
// back as an error that holds a [PanicError] and does not match
// ErrDecode. A decode of any Node of a document in which go-yaml's parser
// nests a line deeper than YAML reads it, or leaves a line out, fails with
// an error that matches ErrDecode. The Decoding section describes the
// messages, the sentinels of each problem, and where each error binds.
func (n *Node) DecodeInto(ctx context.Context, v any, opts ...DecodeOption) error {
	return n.decodeInto(enterCall(ctx), v, newDecodeConfig(n.source, opts))
}

// decodeInto is [Node.DecodeInto] with its settings resolved.
func (n *Node) decodeInto(ctx context.Context, v any, cfg decodeConfig) error {
	err := checkDecode(ctx, v, cfg)
	if err != nil {
		return n.bindOwn(err)
	}

	err = n.treeError()
	if err != nil {
		return err
	}

	// The alias check runs before the validators, so a validator that
	// walks the aliases of the document does not run past the limit.
	err = n.CheckAliasLimit()
	if err != nil {
		return err
	}

	err = n.validate(ctx, cfg.validators)
	if err != nil {
		return err
	}

	// The text check depends on the type of v, so it runs after the
	// validators.
	if aliasing.DecodesTextWith(reflect.TypeOf(v).Elem(), cfg.unmarshalers) {
		err = aliasing.CheckDecodeText(decodedValue{n})
		if err != nil {
			return n.aliasingError(err)
		}
	}

	// The decoder step reports each problem as an error, whatever it wraps,
	// because the value of each problem does not decode.
	err = n.bindOwn(AsErrors(n.decodeUnbound(ctx, n.valueNode(), v, cfg)))
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
		limit := &LimitError{Kind: LimitDocumentBytes, Max: expansion.Limit, Aliases: true}
		if expansion.Depth {
			limit.Kind = LimitDepth
		}

		return n.Bind(Invalid(limit, AtToken(expansion.Alias.GetToken())))
	}

	ratio, ok := errors.AsType[*aliasing.RatioError](err)
	if ok && ratio.Alias != nil {
		excessive := &markedError{err: errors.New(ratio.Error()), marks: []error{ErrExcessiveAliasing}}

		return n.Bind(Invalid(excessive, AtToken(ratio.Alias.GetToken())))
	}

	return n.Bind(Invalid(err, AtToken(astnode.StartToken(n.AST()))))
}

// treeError returns the error that fails every decode of n before it
// reads a value, or nil. A reference document that does not parse, or
// one past a limit, fails every decode with the error of its parse. One
// that go-yaml misnests or leaves a line out of fails every decode with
// the error of that line, and so does the document of n, as
// [Node.misnestError] describes.
func (n *Node) treeError() error {
	if n.source.referenceErr != nil {
		return n.Bind(n.source.referenceErr)
	}

	return n.misnestError()
}

// misnestError returns the error of a decode of n when go-yaml's parse
// of the document of n nests a line too deep, as [document.findMisnest]
// finds it, or nil. Without such a line, it returns the error of
// [Node.droppedError]. The error binds in the document of n.
func (n *Node) misnestError() error {
	err := n.doc.misnest()
	if err == nil {
		return n.droppedError()
	}

	return n.Bind(err)
}

// droppedError returns the error of a decode of n when go-yaml's parse
// of the document of n leaves a line out, as [document.findDropped] finds
// it, or nil. A document in which the parse also nests a line too deep
// gets nil, because a decode fails with the error of that line instead.
// The error binds in the document of n.
func (n *Node) droppedError() error {
	if n.doc.misnest() != nil {
		return nil //nolint:nilerr // A decode fails with the error of the nested line.
	}

	return n.Bind(n.doc.dropped())
}

// misnestError returns the error of [Node.misnestError] for the first
// document of s that holds a line that go-yaml nests too deep or leaves
// out, or nil. The error binds in that document.
func (s *Source) misnestError() error {
	for _, doc := range s.documents() {
		err := doc.misnestError()
		if err != nil {
			return err
		}
	}

	return nil
}

// findMisnest returns the error of the first block entry or item that
// go-yaml's parse of the document nests deeper than YAML reads it, as
// [astnode.Misnested] finds it, or nil. Go-yaml nests the lines after a
// tag on an empty value in that value. So it reads "a: !t\nb: 1" as
// {a: {b: 1}}, where YAML reads b beside a. Every path after such a tag
// then names a value of another tree. The error binds at the anchor or
// the tag that holds the line, or at the line when neither does, and it
// names the path of that anchor or tag. It matches [ErrDecode], and the
// document is at fault for it. A document that did not parse gets nil,
// because its stand-in does not have a body.
func (d *document) findMisnest() error {
	var (
		at    *token.Token
		props ast.Node
	)

	astnode.Misnested(d.root.Body, func(tk *token.Token, p ast.Node) bool {
		at, props = tk, p

		return false
	})

	if at == nil {
		return nil
	}

	msg := "go-yaml nests this line in the value of a line above it, but YAML reads it outside that value"

	switch p := props.(type) {
	case *ast.TagNode:
		at = p.Start
		msg = fmt.Sprintf("go-yaml nests the lines after the tag %s in its value, but YAML reads them outside it",
			p.Start.Value)

	case *ast.AnchorNode:
		name, _ := astnode.Name(p.Name)
		at = p.Start
		msg = fmt.Sprintf("go-yaml nests the lines after the anchor &%s in its value, but YAML reads them outside it",
			name)
	}

	opts := []ErrorOption{AtToken(at)}

	if path, ok := d.node.PathOf(props); props != nil && ok {
		opts = append(opts, AtPath(path))
	}

	return Invalid(markDecode(errors.New(msg)), opts...)
}

// findDropped returns the error of the first line that go-yaml's parse of
// the document leaves out, as [astnode.Dropped] finds it, or nil. Go-yaml
// leaves out the line after the value of an anchor under a scalar tag,
// when that value is a collection or starts on a line below the anchor.
// So it reads "c: !!null &k {x: 1}\nd: 2\na: 1" as {c: null, a: 1},
// where YAML reads d too. The error binds at the tag and names its path
// and the line that the parse leaves out.
func (d *document) findDropped() error {
	var (
		tag  *ast.TagNode
		left *token.Token
	)

	astnode.Dropped(d.root.Body, d.positioned, func(t *ast.TagNode, tk *token.Token) bool {
		tag, left = t, tk

		return false
	})

	if tag == nil {
		return nil
	}

	msg := fmt.Sprintf("go-yaml leaves out line %d after the value of the tag %s, but YAML reads it",
		left.Position.Line, tag.Start.Value)

	opts := []ErrorOption{AtToken(tag.Start)}

	if path, ok := d.node.PathOf(tag); ok {
		opts = append(opts, AtPath(path))
	}

	return Invalid(markDecode(errors.New(msg)), opts...)
}

// checkDecode returns the error of a decode into v with cfg that cannot
// start, or nil. For a v that [checkTarget] rejects, the error wraps
// [ErrDecodeTarget]. For a decode under [UnknownFieldsWarn] whose ctx does
// not carry a collector, it wraps [ErrNoWarnings]. A decode inside a decode
// that niceyaml runs again with a context from [withoutWarnings] drops its
// warnings on purpose, so it passes the check. Each error is a mistake of
// the caller, so it comes inside [Unchecked].
func checkDecode(ctx context.Context, v any, cfg decodeConfig) error {
	err := checkTarget(v, ErrDecodeTarget, true)
	if err != nil {
		return err
	}

	if cfg.unknownFields == UnknownFieldsWarn && !takesWarnings(ctx) {
		return Unchecked(fmt.Errorf("unknown fields mode %q: %w", UnknownFieldsWarn, ErrNoWarnings))
	}

	return nil
}

// checkTarget returns an error wrapping sentinel when v is nil or a nil
// pointer, or, with needPointer set, when v is not a pointer. The go-yaml
// decoder panics on such a target or ignores it. The target is a mistake
// of the caller and no fault of the document, so the error comes inside
// [Unchecked]. A check that returns it as it is thus does not report a
// fault.
func checkTarget(v any, sentinel error, needPointer bool) error {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		if needPointer {
			return Unchecked(fmt.Errorf("%w: got nil", sentinel))
		}

		return Unchecked(sentinel)
	}

	if needPointer && rv.Kind() != reflect.Pointer || rv.Kind() == reflect.Pointer && rv.IsNil() {
		return Unchecked(fmt.Errorf("%w: got %T", sentinel, v))
	}

	return nil
}

// newDecoder returns a go-yaml decoder for a decode of n with cfg. It
// applies the options of the source, then those of cfg. The decoder binds
// each alias as the document does, through [document.resolveAlias]. Where
// cut is not nil, an alias reads the content of its anchor as cut leaves
// it, as [keyCut.readNode] returns it, when the decoder reads the alias.
func (n *Node) newDecoder(cfg decodeConfig, cut *keyCut, extra ...yamldecode.DecodeOption) *yamldecode.Decoder {
	decode := func(ctx context.Context, text []byte, dst any) error {
		return n.decodeValue(ctx, text, dst, cfg)
	}

	resolve := func(alias *ast.AliasNode) (ast.Node, bool) {
		node, ok := n.doc.resolveAlias(alias)
		if ok && cut != nil {
			node = cut.readNode(node)
		}

		return node, ok
	}

	opts := slices.Concat(
		n.source.decodeOptions(),
		[]yamldecode.DecodeOption{yamldecode.ResolveAlias(resolve)},
		cfg.unmarshalers.YAMLOptions(decode),
	)

	if cfg.unknownFields == UnknownFieldsReject {
		opts = append(opts, yamldecode.DisallowUnknownField())
	}

	if len(cfg.allowedFieldPrefixes) > 0 {
		opts = append(opts, yamldecode.AllowFieldPrefixes(cfg.allowedFieldPrefixes...))
	}

	if cfg.orderedMaps {
		opts = append(opts, yamldecode.UseOrderedMap())
	}

	return yamldecode.NewDecoder(bytes.NewReader(nil), append(opts, extra...)...)
}

// ownDecode decodes the first document of text into an any with go-yaml's
// decoder alone, as yaml.Unmarshal decodes it. The decoder parses the text
// itself, because its decode of a parse that it did not make keeps the
// value that its first pass gave each anchor, so an alias inside the value
// of its own anchor reads that value there and not the anchor again. The
// decoder reads the text of each reference document of s, and it takes a
// key that a mapping repeats only when s allows duplicate keys. A panic of
// the decoder comes back as a [*PanicError].
func (s *Source) ownDecode(ctx context.Context, text string) (any, error) {
	var (
		v    any
		err  error
		opts []yaml.DecodeOption
	)

	for _, ref := range s.references {
		opts = append(opts, yaml.ReferenceReaders(strings.NewReader(ref.Text())))
	}

	if s.allowDuplicateKeys {
		opts = append(opts, yaml.AllowDuplicateMapKey())
	}

	func() {
		defer func() {
			if p := recover(); p != nil {
				v, err = nil, recovered(p)
			}
		}()

		err = yaml.NewDecoder(strings.NewReader(text), opts...).DecodeContext(ctx, &v)
	}()

	// A text without a document decodes to nil, as yaml.Unmarshal reads it.
	if errors.Is(err, io.EOF) {
		v, err = nil, nil
	}

	return v, err //nolint:wrapcheck // The caller names go-yaml's decoder.
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
		c.noLimits()
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
	// outer decode, not in the text of the value. The outer decode does
	// not warn of a field of the text, as [UnknownFieldsWarn] describes.
	cfg.knownKeys = nil
	if cfg.unknownFields == UnknownFieldsWarn {
		cfg.unknownFields = UnknownFieldsIgnore
	}

	return valueError(doc.decodeUnbound(ctx, doc.AST(), dst, cfg))
}

// readContent returns the node that a decode of node reads, and the alias
// that node holds, if any. For an anchor, the decode reads the anchor's
// value. For an alias, it reads the content of the anchor that the alias
// names.
func (n *Node) readContent(node ast.Node) (ast.Node, *ast.AliasNode) {
	value := node
	if anchor, ok := node.(*ast.AnchorNode); ok {
		value = anchor.Value
	}

	alias, ok := value.(*ast.AliasNode)
	if !ok {
		return value, nil
	}

	held, err := n.doc.aliasContent(alias)
	if err == nil {
		value = held
	}

	return value, alias
}

// commentMap returns the comments of node and of the nodes below it, in
// the map that [Node.YAMLComments] returns. Each key is a path from base,
// so `$` names base. Without base, the keys start from node. Without node,
// the map is empty.
//
// The walk files each comment under the key and in the kind that the
// go-yaml decoder gives it. Like the decoder, it takes a comment group
// above the token of its node as a head comment and one on the line of
// that token as a line comment. It keeps the first comment of each kind
// under a key, in the order of the nodes in the document. It visits every
// node below node, except in the value of a `<<` merge key. It thus also
// files the comments of the nodes that the decoder does not read, such as
// the name of an anchor or the value under a `!!null` tag. In the value
// of a merge key, it files only the comments that the decoder files,
// those of the entries of each mapping that the key merges. It does not
// follow an alias, so it leaves out the comments of the anchors that
// aliases name.
func commentMap(node, base ast.Node) yaml.CommentMap {
	comments := yaml.CommentMap{}

	if astnode.IsNil(node) {
		return comments
	}

	if astnode.IsNil(base) {
		base = node
	}

	prefix := base.GetPath()

	add := func(path string, c *yaml.Comment) {
		// Every path below node extends the path of base at `.`, `[`, or
		// the end of the path. The test guards against a path that no
		// input produces.
		rest, ok := strings.CutPrefix(path, prefix)
		if !ok || rest != "" && rest[0] != '.' && rest[0] != '[' {
			return
		}

		key := "$" + rest
		for _, held := range comments[key] {
			if held.Position == c.Position {
				return
			}
		}

		comments[key] = append(comments[key], c)
		slices.SortStableFunc(comments[key], func(a, b *yaml.Comment) int {
			return cmp.Compare(a.Position, b.Position)
		})
	}

	var visit func(ast.Node) bool

	visit = func(n ast.Node) bool {
		if n.Type() == ast.CommentType {
			return false
		}

		// For the entry of a `<<` merge key, the decoder files the
		// comments of the entry and of the entries of each mapping that
		// the key merges. It files nothing else of the key or the value.
		if entry, ok := n.(*ast.MappingValueNode); ok && astnode.IsMergeKey(entry.Key) {
			if path, c, ok := headOrLineComment(entry); ok {
				add(path, c)
			}

			if texts := commentTexts(entry.FootComment); len(texts) != 0 {
				add(entry.FootComment.GetPath(), yaml.FootComment(texts...))
			}

			for _, merged := range astnode.MergeValues(entry) {
				entries, _ := astnode.Entries(merged)
				for _, e := range entries {
					astnode.Inspect(e, visit)
				}
			}

			return false
		}

		var foot *ast.CommentGroupNode

		switch n := n.(type) {
		case *ast.SequenceNode:
			for i, head := range n.ValueHeadComments {
				if texts := commentTexts(head); len(texts) != 0 && i < len(n.Values) {
					add(n.Values[i].GetPath(), yaml.HeadComment(texts...))
				}
			}

			if texts := commentTexts(n.GetComment()); len(texts) != 0 && len(n.Values) != 0 {
				add(n.Values[0].GetPath(), yaml.HeadComment(texts...))
			}

			if texts := commentTexts(n.FootComment); len(texts) != 0 {
				add(n.FootComment.GetPath(), yaml.FootComment(texts...))
			}

			return true

		case *ast.MappingNode:
			foot = n.FootComment

		case *ast.MappingValueNode:
			foot = n.FootComment
		}

		if path, c, ok := headOrLineComment(n); ok {
			add(path, c)
		}

		if texts := commentTexts(foot); len(texts) != 0 {
			add(foot.GetPath(), yaml.FootComment(texts...))
		}

		return true
	}

	astnode.Inspect(node, visit)

	return comments
}

// headOrLineComment returns the comment of node, which is not a
// sequence, and the path under which the go-yaml decoder files it. It
// reports false when node holds no comment.
func headOrLineComment(n ast.Node) (string, *yaml.Comment, bool) {
	group := n.GetComment()
	tok := n.GetToken()
	texts := commentTexts(group)

	if len(texts) == 0 || tok == nil {
		return "", nil, false
	}

	first := math.MaxInt
	for _, c := range group.Comments {
		first = min(first, c.Token.Position.Line)
	}

	if first >= tok.Position.Line {
		return n.GetPath(), yaml.LineComment(texts[0]), true
	}

	path := n.GetPath()

	switch n := n.(type) {
	case *ast.MappingNode:
		if len(n.Values) != 0 {
			path = n.Values[0].Key.GetPath()
		}

	case *ast.MappingValueNode:
		path = n.Key.GetPath()
	}

	return path, yaml.HeadComment(texts...), true
}

// commentTexts returns the text of each comment of group, or nil for a
// group without comments.
func commentTexts(group *ast.CommentGroupNode) []string {
	if group == nil {
		return nil
	}

	texts := make([]string, 0, len(group.Comments))
	for _, c := range group.Comments {
		texts = append(texts, c.Token.Value)
	}

	return texts
}

// decodeUnbound decodes node into v with cfg and returns the error for
// the Node to bind. A node without content or a null leaves v as it is,
// so defaults survive, but an untagged null clears a pointer or interface.
func (n *Node) decodeUnbound(
	ctx context.Context,
	node ast.Node,
	v any,
	cfg decodeConfig,
) error {
	// A context that has ended stops the decode before the decoder reads
	// any node.
	err := ctx.Err()
	if err != nil {
		return err //nolint:wrapcheck // The caller binds the error.
	}

	value, _ := n.readContent(node)

	// A document body or a Node at an alias that reads a reference document
	// holds the alias. The decode reads the content of its anchor there, so
	// the tests for a null read that content, with any tag on it.
	null := astnode.IsNil(value) || value.Type() == ast.NullType
	kind := reflect.TypeOf(v).Elem().Kind()

	if !astnode.HasContent(node) || astnode.IsTaggedNull(value) ||
		null && kind != reflect.Pointer && kind != reflect.Interface {
		return nil
	}

	// The decoders read each alias as the cut leaves its anchor, so they
	// read the cut that admitKnownKeys returns. The decoder of the decode
	// reports every problem of the document. The walks ask a decoder that
	// stops at the first problem it finds.
	var (
		cut     keyCut
		unknown fieldReports
	)

	opts := []yamldecode.DecodeOption{yamldecode.CollectErrors()}
	if cfg.unknownFields == UnknownFieldsWarn {
		opts = append(opts, yamldecode.ReportUnknownField(unknown.add))
	}

	// The decoder starts each value that it creates from the defaults of
	// its type. The decode passes the option without defaults too. The
	// option keeps the start of an inline field that the outer struct
	// names. It has an alias decode its anchor again when its place starts
	// from another value. A target that [Node.DecodeInto] fills
	// needs both rules.
	opts = append(opts, cfg.defaults.startOption())

	dec := n.newDecoder(cfg, &cut, opts...)

	cut = n.admitKnownKeys(ctx, n.walkRules(ctx, n.newDecoder(cfg, &cut), cfg.unmarshalers), cfg,
		reflect.TypeOf(v).Elem(), node)

	// For a null, the decoder sets *v to nil.
	target := v

	if !null {
		target, err = startTarget(v, cfg, node)
		if err != nil {
			return err
		}
	}

	// The recover turns a panic in the go-yaml decoder, in a value's own
	// UnmarshalYAML, or in a function of TypeDefaults into an error that
	// holds a PanicError.
	err = decodeWithRecover(ctx, dec, n.readRoot(node, cut), target)

	n.warnUnknownFields(ctx, unknown.found, cut.view, err)

	return n.decodeErrors(err, cut.view)
}

// fieldReports gathers the unknown fields that the decoder of a decode
// under [UnknownFieldsWarn] reports, as [yamldecode.ReportUnknownField]
// hands them.
type fieldReports struct {
	// The error of each unknown field, in the order the decoder reported
	// them.
	found []error
}

// add adds err, the error of one unknown field, to u.
func (u *fieldReports) add(err error) {
	u.found = append(u.found, err)
}

// warnUnknownFields hands each unknown field of found, which the decoder
// of a decode of scope reported, to the [Warnings] collector of ctx in the
// order of the source, as [UnknownFieldsWarn] describes. It does not warn
// when ctx has ended, or when err, the error of the decode, holds a panic.
func (n *Node) warnUnknownFields(ctx context.Context, found []error, scope ast.Node, err error) {
	if len(found) == 0 || ctx.Err() != nil || holdsPanic(err) {
		return
	}

	problems := make([]decodeProblem, 0, len(found))

	for i, e := range found {
		problem := n.decodeProblem(e, scope)
		problem.order = i
		problems = append(problems, problem)
	}

	sortProblems(problems)

	for _, problem := range problems {
		n.Warn(ctx, problem.err)
	}
}

// firstError returns one error of err, an error of a decoder under
// [yamldecode.CollectErrors], without its path. The decoder lists its
// errors in the order of the document. The function returns the first
// error of that list that is not an unknown field, or the first unknown
// field when the list holds only unknown fields.
func firstError(err error) error {
	list, ok := withoutPaths(err).(yamldecode.Errors) //nolint:errorlint // The decoder returns its list unwrapped.
	if !ok {
		return withoutPaths(err)
	}

	for _, e := range list {
		if _, unknown := e.(*yaml.UnknownFieldError); !unknown { //nolint:errorlint // The decoder returns it unwrapped.
			return e
		}
	}

	return list[0]
}

// withoutPaths returns err, an error of a decoder under
// [yamldecode.CollectErrors], without the path of each error in it.
func withoutPaths(err error) error {
	switch found := err.(type) { //nolint:errorlint // The decoder returns these unwrapped.
	case *yamldecode.PathError:
		return found.Err
	case yamldecode.Errors:
		bare := make(yamldecode.Errors, 0, len(found))
		for _, e := range found {
			bare = append(bare, withoutPaths(e))
		}

		return bare

	default:
		return err
	}
}

// readRoot returns the node that the decoder reads for node, the node of
// a decode, as cut leaves it. At the root of a decode, the go-yaml decoder
// hands an UnmarshalText method empty text for an alias whose anchor
// another parse holds, as a reference document does. The decoder reads the
// anchor in place of such an alias. An alias inside the anchor that names
// it then reads null, as it does where the alias lies below the root.
func (n *Node) readRoot(node ast.Node, cut keyCut) ast.Node {
	if _, alias := n.readContent(node); alias != nil {
		if anchor, ok := n.rootReference(alias); ok {
			return anchor
		}
	}

	return cut.read
}

// rootReference returns the anchor of a reference document that alias
// reads, as [reftable.Table.Anchor] gives it. Such an alias is one that the
// resolver of the document does not bind to an anchor of the document. It
// reports false for any other alias.
func (n *Node) rootReference(alias *ast.AliasNode) (ast.Node, bool) {
	_, err := n.doc.pathResolver().Deref(alias)
	if !errors.Is(err, paths.ErrReferenceAlias) {
		return nil, false
	}

	name, ok := astnode.Name(alias.Value)
	if !ok {
		return nil, false
	}

	anchor, ok := n.source.referenceAnchors().Anchor(name)
	if !ok {
		return nil, false
	}

	return anchor, true
}

// rejection places err, an error that the go-yaml decoder returned for
// scope without a token of the document, at the node in scope that caused
// it. The decoder read the value of err at path, a `$` path, which pathed
// reports that the decoder gave. For a token of a reference document, that
// node is the alias that led the decoder to the token, as
// [Node.referenceRead] finds it on path. The result is an [*Error] that
// matches [ErrDecode] and [errPlaced]. Any other err comes back as it is.
func (n *Node) rejection(err error, scope ast.Node, path paths.Path, pathed bool) error {
	if err == nil || astnode.IsNil(scope) || errors.Is(err, errPlaced) || contextEnded(err) {
		return err
	}

	yamlErr, ok := err.(yaml.Error) //nolint:errorlint // A wrapped error is the unmarshaler's own.

	tree := n.doc.tree()

	var at *token.Token

	if errors.Is(err, yaml.ErrExceededMaxDepth) {
		at = astnode.StartToken(scope)
	} else if rejected := rejectedAlias(err); rejected != nil {
		// The rejection binds at the alias that the decoder could not read.
		// An alias of a reference document binds at the alias of the
		// document that led the decoder to it.
		if n.holdsToken(rejected.Start) {
			at = rejected.Start
		} else if alias, found := n.referenceRead(tree.ReferenceAliases(scope), rejected.Start, path); found {
			at = alias.Token
		}
	}

	if at == nil && ok {
		refs := tree.ReferenceAliases(scope)
		if len(refs) == 0 {
			return err
		}

		alias, found := n.referenceRead(refs, yamlErr.GetToken(), path)
		if !found {
			return n.referenceRejection(yamlErr, nil, path, pathed)
		}

		return n.referenceRejection(yamlErr, &alias, path, pathed)
	}

	if at == nil {
		return err
	}

	rejected := markDecode(fmt.Errorf("decoder rejected the value: %w", err), errPlaced)

	return Invalid(rejected, AtToken(at))
}

// rejectedAlias returns the alias that err names: an alias that the decoder
// holds no anchor for, or an alias under a merge key that the decoder reads
// inside the anchor that it names. It returns nil for any other err.
func rejectedAlias(err error) *ast.AliasNode {
	if missing, ok := errors.AsType[*yamldecode.MissingAnchorError](err); ok {
		return missing.Alias
	}

	if loop, ok := errors.AsType[*yamldecode.MergeLoopError](err); ok {
		return loop.Alias
	}

	return nil
}

// referenceRejection returns err, a rejection at a token of a reference
// document, as an [*Error] that matches [ErrDecode], its label, and
// [errPlaced]. The Error binds at alias, the alias that led the decoder to
// the token, as [Node.referenceLocation] locates it on path, the `$` path
// of the read when pathed holds. A nil alias leaves the Error without a
// location.
func (n *Node) referenceRejection(
	err yaml.Error, alias *decodetree.ReferenceAlias, path paths.Path, pathed bool,
) error {
	msg, label := n.doc.rejectionText(err)
	rejected := markDecode(yamlMessage(err, msg), label, errPlaced)

	if alias == nil {
		return Invalid(rejected)
	}

	if pathed && n.bindsAt(path, alias.Token) {
		return Invalid(rejected, AtPath(path))
	}

	return Invalid(rejected, n.referenceLocation(*alias, err.GetToken())...)
}

// referenceRead returns the alias among refs, the aliases of a reference
// document that a decode reads, that led the decoder to tk, a token of a
// reference document. The anchor of that alias reaches tk, as
// [reftable.Places.Paths] finds the places of a token. When refs holds one
// alias, or one alias of refs reaches tk, referenceRead returns that
// alias. When several do, it returns the one that path, the `$` path on
// which the decoder read tk, passes, as [Node.referenceAliasOn] finds it.
// A later entry of the same key can take path, so that it passes no
// alias. It then returns the first alias whose own path and the path of
// tk below its anchor spell path, as [Node.readsThrough] reports. It
// reports false when none does.
func (n *Node) referenceRead(
	refs []decodetree.ReferenceAlias, tk *token.Token, path paths.Path,
) (decodetree.ReferenceAlias, bool) {
	if len(refs) == 1 {
		return refs[0], true
	}

	table := n.source.referenceAnchors()
	places := table.Places(tk)
	reaches := map[string]bool{}

	var reached []decodetree.ReferenceAlias

	for _, ref := range refs {
		found, ok := reaches[ref.Name]
		if !ok {
			anchor, held := table.Anchor(ref.Name)
			found = held && len(places.Paths(anchor)) > 0
			reaches[ref.Name] = found
		}

		if found {
			reached = append(reached, ref)
		}
	}

	switch len(reached) {
	case 0:
		return decodetree.ReferenceAlias{}, false
	case 1:
		return reached[0], true
	}

	on := n.referenceAliasOn(path)

	for _, ref := range reached {
		if ref.Token == on {
			return ref, true
		}
	}

	for _, ref := range reached {
		if n.readsThrough(ref, tk, path) {
			return ref, true
		}
	}

	return decodetree.ReferenceAlias{}, false
}

// readsThrough reports whether path, a `$` path, reads tk, a token of a
// reference document, through alias. The path then starts with the path
// of the alias, read through the `<<` merge keys of the document, and goes
// on with a path from the anchor of the alias to tk, as
// [Node.referenceLocation] spells them.
func (n *Node) readsThrough(alias decodetree.ReferenceAlias, tk *token.Token, path paths.Path) bool {
	table := n.source.referenceAnchors()

	anchor, ok := table.Anchor(alias.Name)
	if !ok {
		return false
	}

	places := table.Places(tk)

	below := places.Paths(anchor)
	if alias.Merge {
		below = places.MergePaths(anchor)
	}

	owner, ok := n.doc.pathIndex().MergedOwnerPath(n.doc.pathResolver(), alias.Token)
	if !ok {
		return false
	}

	for _, rest := range below {
		if owner.Join(rest).Equal(path) {
			return true
		}
	}

	return false
}

// referenceAliasOn returns the token of the alias of a reference document
// that path, a `$` path, reads through, or nil. The path ends at that
// alias, or it goes on below it into the anchor that the alias reads.
func (n *Node) referenceAliasOn(path paths.Path) *token.Token {
	tk, err := n.doc.pathResolver().Token(path)
	if err == nil {
		if tk != nil && tk.Type == token.AliasType {
			return tk
		}

		return nil
	}

	unresolved, ok := errors.AsType[*paths.AliasError](err)
	if !ok || unresolved.Alias == nil {
		return nil
	}

	return unresolved.Alias.GetToken()
}

// referenceLocation returns the options that locate a rejection at tk, a
// token of a reference document, which the decoder read through alias,
// where the path of the read does not bind at the alias, or the decoder
// gave no such path. The rejection binds at the alias. Its path is the
// path of the value of tk.
// That path starts with the path of the alias, read through the `<<` merge
// keys of the document, as [pathindex.Index.MergedOwnerPath] spells it.
// For an alias that a merge key merges, that path names the mapping that
// holds the merge key. The path goes on from the anchor of the alias to tk,
// as [reftable.Places.Paths] finds it, or [reftable.Places.MergePaths] for
// such an alias. The path binds as a [Validator] error at that path binds,
// so [SourceError.Nearest] names the alias, or the mapping that holds the
// merge key.
//
// A later entry of the same key can take that path, so that it binds
// elsewhere. The path then goes through the merge keys, as
// [pathindex.Index.OwnerPath] spells the path of the alias, and on from
// the anchor read as a value. Where neither path binds at the alias, the
// rejection carries the first path and binds at the position of the alias.
// The rejection binds at the alias without a path when the anchor reaches
// tk on more than one path, or a path cannot spell the way there.
func (n *Node) referenceLocation(alias decodetree.ReferenceAlias, tk *token.Token) []ErrorOption {
	at := AtToken(alias.Token)

	table := n.source.referenceAnchors()

	anchor, ok := table.Anchor(alias.Name)
	if !ok {
		return []ErrorOption{at}
	}

	places := table.Places(tk)
	values := places.Paths(anchor)

	below := values
	if alias.Merge {
		below = places.MergePaths(anchor)
	}

	if len(below) != 1 {
		return []ErrorOption{at}
	}

	index, resolver := n.doc.pathIndex(), n.doc.pathResolver()

	path, ok := index.MergedOwnerPath(resolver, alias.Token)
	if !ok {
		return []ErrorOption{at}
	}

	path = path.Join(below[0])

	if n.bindsAt(path, alias.Token) {
		return []ErrorOption{AtPath(path)}
	}

	if through, _, ok := index.OwnerPath(resolver, alias.Token); ok && len(values) == 1 {
		through = through.Join(values[0])

		if n.bindsAt(through, alias.Token) {
			return []ErrorOption{AtPath(through)}
		}
	}

	return []ErrorOption{AtPath(path), at}
}

// bindsAt reports whether a [Validator] error at path, which starts at
// `$`, binds at the position of tk. A path that selects another node, such
// as a later entry of the same key, binds there.
func (n *Node) bindsAt(path paths.Path, tk *token.Token) bool {
	loc, err := n.doc.node.resolveLocation(path)

	return err == nil && loc.tk != nil && tk != nil &&
		position.NewFromToken(loc.tk) == position.NewFromToken(tk)
}

// The mark of an error of a decode that [decodeWithRecover] or
// [Node.rejection] placed in the source, so [Node.decodeProblem] does not
// place it at the path of its value again.
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
// [*Error] at node that holds a [*PanicError]. For a [*defaultsPanic], the
// error names the type whose defaults panicked, and it sits at the node of
// the value that the decoder created. The document is not at fault for a
// panic, so the error matches [errPlaced] but not [ErrDecode].
func decodeWithRecover(ctx context.Context, dec *yamldecode.Decoder, node ast.Node, v any) (err error) {
	defer func() {
		p := recover()
		if p == nil {
			return
		}

		in, panicErr, at := "decoder", recovered(p), astnode.StartToken(node)
		if d, ok := p.(*defaultsPanic); ok {
			in, panicErr = fmt.Sprintf("the defaults of %s", d.typ), d.err
			if tk := astnode.StartToken(d.node); tk != nil {
				at = tk
			}
		}

		err = Unchecked(panicked(in, panicErr, errPlaced), AtToken(at))
	}()

	return dec.DecodeFromNodeContext(ctx, node, v) //nolint:wrapcheck // The caller binds the error.
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
// [Node.Scalar] returns the text of a scalar as the document writes it,
// such as a version or a file mode.
//
// An [ast.Node] in the result is the node that [Node.AST] returns for its
// place, and at an alias it is the content of the anchor. The node is
// part of the tree that [Source.File] returns, so a caller must not
// modify it. The Decoding section of the package documentation names the
// nodes that are not the document's, such as the nodes inside reference
// documents.
func (n *Node) Decode[T any](ctx context.Context, opts ...DecodeOption) (T, error) {
	var zero T

	ctx = enterCall(ctx)
	cfg := newDecodeConfig(n.source, opts)

	// The check of the context runs before the functions of TypeDefaults,
	// so a decode that cannot start runs no code of the caller.
	err := checkDecode(ctx, &zero, cfg)
	if err != nil {
		return zero, n.bindOwn(err)
	}

	start, err := cfg.defaults.start(reflect.TypeFor[T](), n.valueNode())
	if err != nil {
		return zero, n.bindOwn(err)
	}

	// A nil interface asserts to the zero value, which it is.
	v, _ := reflect.TypeAssert[T](start)

	err = n.decodeInto(ctx, &v, cfg)
	if err != nil {
		return zero, err
	}

	return v, nil
}

// MarshalYAML returns the value that go-yaml's encoder writes for the
// Node. That value holds what [Node.Decode] reads into an any, with the
// keys of each mapping in the order of the document.
// [go.jacobcolvin.com/niceyaml/encoder.Marshal] thus writes a Node, or a
// value that holds Nodes, as the values of the nodes:
//
//	items, err := doc.Nodes(paths.MustParse("$.items[*]"))
//	if err != nil {
//		return err
//	}
//
//	out, err := encoder.Marshal(ctx, items)
//
// The encode writes the content of each alias and the entries of each
// `<<` merge key in their place. It writes each key of a mapping once,
// with the value that a decode into a map reads for the key, at the place
// of the first entry that holds the key. A mapping can set a key that its
// `<<` merge key also holds, as in "{<<: *base, x: 9}". A mapping of a
// [Source] under [WithAllowDuplicateKeys] can repeat a key. Each such key
// encodes once.
//
// The decoded value does not hold the comments, the quotes, or the block
// and flow styles of the node. So the encode does not write them. Of the
// tags of the node, it writes only !!binary and !!timestamp. A !!binary
// scalar encodes as that tag and its bytes in base64. A !!timestamp
// scalar encodes as that tag and its time in the layout of
// [time.RFC3339Nano]. The encode writes each of the two scalars in double
// quotes, as in `!!timestamp "2001-12-14T21:59:43Z"`. Each thus decodes
// to the same bytes or the same time in block style and in flow style. A
// decode reads each key as a string, so the encode writes a key such as 1
// or true in quotes.
//
// MarshalYAML returns each mapping as a [yaml.MapSlice] and each sequence
// as a []any. It returns the value of a !!binary or a !!timestamp scalar
// as a value of an internal type that marshals to the tagged scalar. The
// result shares the MapSlice or []any of each anchor among its aliases,
// as a decode does. A change to that value thus shows at every alias.
//
// MarshalYAML returns the error of the decode, such as the error that
// [Node.Err] returns for a document that did not parse. A nil Node
// encodes as null.
func (n *Node) MarshalYAML(ctx context.Context) (any, error) {
	if n == nil {
		return nil, nil //nolint:nilnil // A nil pointer encodes as null.
	}

	v, err := n.Decode[any](ctx, WithYAMLOrderedMaps(true))
	if err != nil {
		return nil, err
	}

	return encodable(v), nil
}

// encodable returns v, a value that a decode read into an any under
// [WithYAMLOrderedMaps], as the value that [Node.MarshalYAML] returns for
// it. The decoder appends the entries of a `<<` merge key and each entry
// of a repeated key to the [yaml.MapSlice] of their mapping, so a
// MapSlice can hold a key more than once. In the result, each MapSlice
// holds each key once, at the place of its first item, with the value of
// its last item. That value is the one that a decode into a map reads.
//
// A []byte becomes a [taggedscalar.Binary], because go-yaml's encoder
// writes a []byte as a sequence of integers. A [time.Time] becomes a
// [taggedscalar.Timestamp], because the encoder writes a time as a
// string. The result holds a new MapSlice for each MapSlice of v and a
// new []any for each []any.
//
// A decode shares the MapSlice or []any of an anchor among its aliases.
// The result shares the new MapSlice or []any the same way, so the
// conversion reads each shared value once. The alias limit of
// [go.jacobcolvin.com/niceyaml/schema.Schema.ValidateValue] counts the
// uses of each shared value of the result. It thus sees the aliases of
// the document.
func encodable(v any) any {
	c := encodableConverter{done: map[encodableKey]any{}}

	return c.convert(v)
}

// encodableKey names a MapSlice or []any that is not empty, by the
// address of its first item and its length. A slice and a shorter slice
// of it share an address, so the length is part of the key. The type of
// the address tells the two kinds of slice apart.
//
//nolint:unused // The fields tell the keys of the map apart.
type encodableKey struct {
	first any
	len   int
}

// encodableConverter converts a value for [encodable]. It holds the
// result for each MapSlice and []any that it converted.
type encodableConverter struct {
	done map[encodableKey]any
}

// convert returns the result of [encodable] for v.
func (c *encodableConverter) convert(v any) any {
	var key encodableKey

	switch x := v.(type) {
	case yaml.MapSlice:
		if len(x) == 0 {
			return c.convertMapSlice(x)
		}

		key = encodableKey{first: &x[0], len: len(x)}

	case []any:
		if len(x) == 0 {
			return c.convertSlice(x)
		}

		key = encodableKey{first: &x[0], len: len(x)}

	case []byte:
		return taggedscalar.Binary(x)

	case time.Time:
		return taggedscalar.Timestamp(x)

	default:
		return v
	}

	if out, ok := c.done[key]; ok {
		return out
	}

	var out any

	switch x := v.(type) {
	case yaml.MapSlice:
		out = c.convertMapSlice(x)
	case []any:
		out = c.convertSlice(x)
	}

	c.done[key] = out

	return out
}

// convertMapSlice returns a new MapSlice that holds each key of x once,
// as [encodable] describes.
func (c *encodableConverter) convertMapSlice(x yaml.MapSlice) yaml.MapSlice {
	out := make(yaml.MapSlice, 0, len(x))
	at := make(map[string]int, len(x))

	for _, item := range x {
		name := datapath.MemberName(item.Key)
		value := c.convert(item.Value)

		if i, ok := at[name]; ok {
			out[i].Value = value

			continue
		}

		at[name] = len(out)
		out = append(out, yaml.MapItem{Key: item.Key, Value: value})
	}

	return out
}

// convertSlice returns a new []any that holds the result of [encodable]
// for each element of x.
func (c *encodableConverter) convertSlice(x []any) []any {
	out := make([]any, len(x))
	for i, e := range x {
		out[i] = c.convert(e)
	}

	return out
}

// YAMLComments returns the comments of the node in a new map of the type
// that go-yaml's [yaml.CommentToMap] option fills. Each comment sits
// under the path of its value from the node.
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
// Each document of a stream has its own map, keyed from its own root. An
// [go.jacobcolvin.com/niceyaml/encoder.Encoder] that writes the stream
// back takes each map in the Encode call of its document:
//
//	enc := encoder.New(w)
//	for _, doc := range src.Documents() {
//		var v map[string]any
//		if err := doc.DecodeInto(ctx, &v); err != nil {
//			return err
//		}
//
//		comments, err := doc.YAMLComments(ctx)
//		if err != nil {
//			return err
//		}
//
//		if err := enc.Encode(ctx, v, encoder.WithYAMLComments(comments)); err != nil {
//			return err
//		}
//	}
//
// The encoder finds the value of each comment by its path in the changed
// value. It drops the comments of a removed value, because their path
// does not select a node. A go-yaml path does not step into a value that
// has an anchor or a tag. The encoder thus also drops the comments of the
// keys and items in a value that it writes with an anchor or a tag. It
// drops the line comment of such a value too when it writes the value as
// a block mapping or a block sequence. The value of a struct field with
// the anchor option in its yaml tag is such a value.
//
// A path with an index names a position in a list, not the item that
// held it. After the program inserts or removes a list item, the comment
// of each position from that item to the end goes to the item that holds
// the position in the changed list. The
// [go.jacobcolvin.com/niceyaml/edit] package changes the text of a
// document in place, so each comment stays with its item.
//
// The call reads the comments from the syntax tree of the node and does
// not decode it, so it returns the comments of a node that a decode
// rejects. Each comment sits under the path and in the kind, head, line,
// or foot, that go-yaml's decoder gives it under [yaml.CommentToMap]. The
// map also holds the comments that the decoder drops. They are those on
// the line of an anchor or an alias after its name, and those on a value
// under a `!!null` tag. For a document that did not parse, or a [Source]
// with a reference document that did not parse, YAMLComments returns the
// error of that parse and a nil map. It does the same for a context that
// has ended. Go-yaml's parser can nest a line deeper than YAML reads it,
// or leave a line out, as the Decoding section of the package
// documentation describes. For such a document, YAMLComments returns the
// error of a decode of the node and a nil map. For a node that does not
// hold a value, YAMLComments returns an empty map.
//
// Each key is a path from the node, so `$` names the value that a decode
// of the same Node returns. The map thus pairs with an encode of that
// value. For a Node from [Node.At] at `$.spec`, the comment of
// `$.spec.replicas` in the document has the key `$.replicas`. A Node at
// an alias keys its comments from the content of the anchor that the
// alias reads. For keys from the root of the document, call YAMLComments
// on the Node that [Node.Document] returns.
//
// Like the decoder, the map files the comments of a `<<` merge key under
// paths through `<<`, such as `$.base.<<` for a head comment above the
// key. It does the same for the comments of the entries of a mapping that
// the key holds in place, such as `$.base.<<.port`. These paths do not
// name a value that a decode returns, so the encoder drops their
// comments. The map leaves out the other comments in the value of a merge
// key. One example is a comment on the line of a mapping in flow style,
// and another is one above an item of a sequence.
//
// The map leaves out the comments of each anchor outside the node that an
// alias or a `<<` merge key in the node reads. It also leaves out the
// comments that belong to the mapping or sequence that holds the node.
// The map of the document files some of them at the path of the node.
// They are the head and line comments of the key that holds the node and
// the head comment of the item that holds it. They also include the foot
// comment that follows the node as the last value of that mapping or
// sequence.
//
// The map keeps one comment of each kind under a path. When the node
// holds two comments of one kind at a path, the map keeps the one that
// comes first in the document, as the decoder does.
//
// Each key is in the path syntax of go-yaml, which prints a name with
// whitespace or `:` without quotes, as in `$.my key`. [paths.Parse]
// rejects such an unquoted name, so it does not read every key. To look
// up the key of a [paths.Path], convert the path with
// [paths.Path.YAMLPath] and use the String of the result, not the String
// of the [paths.Path].
func (n *Node) YAMLComments(ctx context.Context) (yaml.CommentMap, error) {
	err := ctx.Err()
	if err != nil {
		return nil, err //nolint:wrapcheck // The error names the context that ended.
	}

	if n.doc.err != nil {
		return nil, n.doc.err
	}

	// The map keys each comment by its path. A path after a line that
	// go-yaml nests too deep names a value of another tree, and a line
	// that it leaves out has no comments in the map.
	err = n.treeError()
	if err != nil {
		return nil, err
	}

	node := n.valueNode()
	if !astnode.HasContent(node) {
		return yaml.CommentMap{}, nil
	}

	// A Node at an anchor holds the comments of the anchor and of its
	// value. A Node at an alias holds only those of the content that the
	// alias reads.
	read, alias := n.readContent(node)
	if alias != nil {
		node = read
	}

	return commentMap(node, read), nil
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
// returns the error that [Node.Err] returns. A document in which go-yaml's
// parser nests a line deeper than YAML reads it, or leaves a line out,
// returns the error of Decode. So does a document that reads a reference
// document from [WithReferences] that does not parse or that go-yaml
// misnests or leaves a line out of. Both errors come before the path
// resolves, as the Decoding section of the package documentation
// describes.
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
	var zero T

	// A path after a line that go-yaml nests too deep can name a value of
	// another tree, and a line that it leaves out reads as absent. Either
	// line can sit in the document or in a reference document that a
	// merge key reads, so the check runs before the path resolves.
	err := n.treeError()
	if err != nil {
		return zero, err
	}

	node, err := n.At(path)
	if err != nil {
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
// the source, before the path resolves. So does a decode under
// [UnknownFieldsWarn] whose context does not carry a [Warnings] collector,
// with an error that wraps [ErrNoWarnings]. Each mistake thus shows
// whether or not the document holds the value.
//
// DecodeIfPresent returns false and a nil error only when At returns an
// error that matches [paths.ErrNotFound]. Every other failure returns
// false and the error. Those failures include the error of a document
// that did not parse, an alias on the path that does not resolve, and a
// wildcard path. They include the error of a document in which go-yaml's
// parser nests a line deeper than YAML reads it, or leaves a line out, as
// the Decoding section of the package documentation describes. They
// include the error of a reference document from [WithReferences] that
// does not parse or that go-yaml misnests or leaves a line out of. Both
// errors come before the path resolves, so DecodeIfPresent returns them
// for an absent path too. They also include every error of the decode,
// even one that matches paths.ErrNotFound. A node that fails a
// [Validator] thus never reads as absent.
//
// A null is present, so `version: ~` reports true and leaves version at
// its default. When v points to a pointer or an interface, such a null
// sets that pointer or interface to nil. A path is absent when it looks
// up a key in a value that is not a mapping, or an index past the end of
// a sequence. Every path with a selector in a document without content
// is absent too. [go.jacobcolvin.com/niceyaml/matcher.Exists]
// finds such paths absent as well.
//
// The decode runs on the scoped Node and drops it, as [Node.DecodeAt]
// describes.
func (n *Node) DecodeIfPresent(ctx context.Context, path paths.Path, v any, opts ...DecodeOption) (bool, error) {
	// The checks of the target and of the context run first. A mistake thus
	// fails for an absent path too, where no decode runs to reject it.
	cfg := newDecodeConfig(n.source, opts)

	err := checkDecode(ctx, v, cfg)
	if err != nil {
		return false, n.bindOwn(err)
	}

	// A path after a line that go-yaml nests too deep can name a value of
	// another tree, and a line that it leaves out reads as absent. Either
	// line can sit in the document or in a reference document that a
	// merge key reads, so the check runs before the path resolves.
	err = n.treeError()
	if err != nil {
		return false, err
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
	err = node.decodeInto(enterCall(ctx), v, cfg)
	if err != nil {
		return false, err
	}

	return true, nil
}

// decodeErrors returns err, the error of a decode of scope, for the Node
// to bind. The decoder of the decode reports every problem it finds, as
// [yamldecode.CollectErrors] describes, and each with the path on which it
// read the value of the problem. A value that several aliases or `<<`
// merge keys read thus gives one problem for each use. [Node.decodeProblem]
// places each problem, and [problemsError] reports them together. The
// argument scope is the node as the decoder read it, without the entries
// that the known keys of the decode cut.
//
// When an unmarshaler returns the error of a context that ended,
// decodeErrors returns that error alone, without the other problems.
func (n *Node) decodeErrors(err error, scope ast.Node) error {
	if err == nil {
		return nil
	}

	list, ok := err.(yamldecode.Errors) //nolint:errorlint // The decoder returns its list unwrapped.
	if !ok {
		list = yamldecode.Errors{err}
	}

	// An unmarshaler that saw the context end ends the decode. Its error
	// comes back alone, as a panic does.
	for _, found := range list {
		e := withoutPaths(found)
		if own, ok := e.(*yamldecode.UnmarshalerError); ok { //nolint:errorlint // The decoder returns it unwrapped.
			e = own.Err
		}

		if contextEnded(e) {
			return e
		}
	}

	problems := make([]decodeProblem, 0, len(list))

	for i, found := range list {
		problem := n.decodeProblem(found, scope)
		problem.order = i
		problems = append(problems, problem)
	}

	return problemsError(problems)
}

// decodeProblem returns err, one problem that the decoder of a decode of
// scope reported, as a problem that the Node binds.
//
//   - A rejection at a token of the document binds as [Node.useRejection]
//     binds it, at the path on which the decoder read the token.
//   - The error of a value that decodes itself binds at the path of that
//     value, as [Node.unmarshalerProblem] describes.
//   - A rejection at a token of a reference document, an alias without an
//     anchor, and a decode too deep bind as [Node.rejection] binds them.
//   - Any other error binds at the path of its value, and an error without
//     a path comes back as it is.
func (n *Node) decodeProblem(err error, scope ast.Node) decodeProblem {
	var (
		rel    = paths.Current()
		pathed bool
	)

	if located, ok := err.(*yamldecode.PathError); ok { //nolint:errorlint // The decoder returns it unwrapped.
		rel, err, pathed = stepsPath(located.Path), located.Err, true
	}

	path := n.base.Join(rel)

	if own, ok := err.(*yamldecode.UnmarshalerError); ok { //nolint:errorlint // The decoder returns it unwrapped.
		return n.unmarshalerProblem(own, rel, pathed)
	}

	yamlErr, ok := err.(yaml.Error) //nolint:errorlint // A wrapped error is the unmarshaler's own.
	if ok && n.holdsToken(yamlErr.GetToken()) {
		problem := n.useRejection(yamlErr, path)
		_, problem.unknownField = err.(*yaml.UnknownFieldError) //nolint:errorlint // The decoder returns it unwrapped.

		return problem
	}

	placed := n.rejection(err, scope, path, pathed)
	if !errors.Is(placed, errPlaced) && pathed && !rel.Equal(paths.Current()) &&
		!contextEnded(placed) && !holdsPanic(placed) {
		placed = Rebase(asDecodeError(placed), rel)
	}

	problem := decodeProblem{
		err:          asDecodeError(placed),
		unknownField: errors.Is(placed, errPlaced) && errors.Is(placed, ErrUnknownField),
	}
	problem.at = n.boundAt(problem.err)

	return problem
}

// unmarshalerProblem returns own, the error of a value that decodes
// itself, as a problem at rel, the `@` path on which the decoder read the
// value. Without such a path, the problem binds at the path of the node
// of own in the document. The cause
// keeps the `@` paths that the unmarshaler wrote, from the value. The
// decoder refuses a mapping or a sequence for a type that decodes itself
// from text alone, and the problem names the kind of that node, as
// [textRejection] writes it.
//
// An unmarshaler can wrap the error that its callback returned for a value
// inside it, as [innerValue] finds it. When that error carries a location,
// such as an `@` path that the unmarshaler of the inner value wrote, the
// location reads from the inner value. The problem then binds at the path
// of the inner value, with the text of each wrapper. A wrapped rejection
// at a token binds at the path on which the decoder read the token, as
// [Node.placeRejections] places it. When the callback returned several
// errors, the text of the unmarshaler heads them at the path of its value.
func (n *Node) unmarshalerProblem(own *yamldecode.UnmarshalerError, rel paths.Path, pathed bool) decodeProblem {
	// An unmarshaler that saw the context end returns its error as it
	// is, as the decode returns the error of the context.
	if contextEnded(own.Err) {
		return decodeProblem{err: own.Err}
	}

	cause := asDecodeError(own.Err)
	if errors.Is(own.Err, yamldecode.ErrTextOnly) {
		cause = textRejection(own.Node)
	}

	base, node := rel, own.Node
	if !pathed {
		if at, ok := n.doc.node.PathOf(own.Node); ok {
			base = at
		}
	}

	if at, held, ok := innerValue(own.Err); ok && pathed {
		base, node = at, held
	}

	var placed placedRejections

	read := own.Err
	if pathed {
		read = n.placeRejections(own.Err, &placed)
		if placed.count != 0 {
			cause = asDecodeError(read)
		}
	}

	problem := decodeProblem{err: Rebase(cause, base)}

	list, ok := errors.AsType[yamldecode.Errors](read)
	if ok && len(list) != 0 {
		// The callback returned several errors, and the text of the
		// unmarshaler heads them at its value.
		problem.err = Invalid(problem.err, AtPath(n.base.Join(base)))
	}

	switch tk := astnode.StartToken(node); {
	case placed.count == 1:
		problem.at = placed.at
	case n.holdsToken(tk) && tk.Position != nil:
		problem.at = position.NewFromToken(tk)
	default:
		problem.at = n.boundAt(problem.err)
	}

	return problem
}

// placedRejections counts the rejections that [Node.placeRejections]
// placed, and holds the position of the first one.
type placedRejections struct {
	at    position.Position
	count int
}

// placeRejections returns err, the error of an unmarshaler, with each
// rejection at a token of the document below it read as
// [Node.useRejection] reads it. Each such rejection came back from the
// callback of the unmarshaler with the path on which the decoder read its
// token, and the unmarshaler wrapped it. So it binds at that path. The
// binding of a wrapped rejection otherwise places it at its token alone,
// and each alias that reads a value then gets the path of the anchor. Each
// wrapper above a placed rejection becomes a stand-in whose message holds
// the text of the placed rejection. The walk stops at an [*Error] and at a
// [*SourceError], which carry locations of their own.
func (n *Node) placeRejections(err error, placed *placedRejections) error {
	switch x := err.(type) { //nolint:errorlint // Walks the chain one node at a time.
	case nil, *Error, *SourceError:
		return err

	case *yamldecode.PathError:
		yamlErr, ok := x.Err.(yaml.Error) //nolint:errorlint // The decoder returns it unwrapped.
		if ok && n.holdsToken(yamlErr.GetToken()) {
			problem := n.useRejection(yamlErr, n.base.Join(stepsPath(x.Path)))
			if placed.count == 0 {
				placed.at = problem.at
			}

			placed.count++

			return problem.err
		}
	}

	joined := mapJoin(err, func(branch error) error { return n.placeRejections(branch, placed) })
	if joined != nil {
		return joined
	}

	if x, ok := err.(interface{ Unwrap() []error }); ok &&
		multierr.IsWrapErrors(err) { //nolint:errorlint // The node itself, not a chain search.
		return n.placeBranches(err, x.Unwrap(), placed)
	}

	w, ok := err.(interface{ Unwrap() error }) //nolint:errorlint // The node itself, not a chain search.
	if !ok {
		return err
	}

	inner, held := w.Unwrap(), placed.count

	read := n.placeRejections(inner, placed)
	if placed.count == held {
		return err
	}

	msg := err.Error()
	if link := inner.Error(); link != "" {
		msg = strings.ReplaceAll(msg, link, read.Error())
	}

	return multierr.Replace(err, read, msg, nil)
}

// placeBranches returns wrapper, an error that [fmt.Errorf] builds with
// several %w verbs, with each rejection in branches, the errors that it
// unwraps to, placed by [Node.placeRejections]. A placed rejection reads
// as a sentinel to [isLeaf], so placeBranches walks every branch, unlike
// [mapWrapper]. When it places a rejection, it returns a stand-in for
// wrapper over the placed branches, whose message has the text of each
// changed branch replaced. Otherwise it returns wrapper.
func (n *Node) placeBranches(wrapper error, branches []error, placed *placedRejections) error {
	msg := wrapper.Error()
	mapped := slices.Clone(branches)
	held := placed.count

	for i, branch := range branches {
		if isNothing(branch) {
			continue
		}

		before := placed.count

		m := n.placeRejections(branch, placed)
		if placed.count != before {
			if link := branch.Error(); link != "" {
				msg = strings.ReplaceAll(msg, link, m.Error())
			}

			mapped[i] = m
		}
	}

	if placed.count == held {
		return wrapper
	}

	return multierr.Rewrap(wrapper, mapped, msg)
}

// innerValue returns the path of the deepest value inside err, the error
// of an unmarshaler, at which the decoder found an error that carries a
// location of its own, and the node of that value. Such an error came
// back from the callback of the unmarshaler with the path of its value,
// and the unmarshaler wrapped it. The location reads from that value, as
// an `@` path that the unmarshaler of the value wrote does. It reports
// false when err does not hold such an error, or when a wrapper moved
// that location, as [Rebase] does, so that it reads from the value of err.
func innerValue(err error) (paths.Path, ast.Node, bool) {
	var found *yamldecode.PathError

	for at := err; ; {
		located, ok := errors.AsType[*yamldecode.PathError](at)
		if !ok {
			break
		}

		found, at = located, located.Err
	}

	if found == nil || !holdsLocation(found.Err) || !slices.EqualFunc(placesOf(err), placesOf(found.Err), sameLocus) {
		return paths.Path{}, nil, false
	}

	var node ast.Node

	if inner, ok := errors.AsType[*yamldecode.UnmarshalerError](found.Err); ok {
		node = inner.Node
	}

	return stepsPath(found.Path), node, true
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

// stepsPath returns the `@` path that steps spell, from the value that a
// decode reads.
func stepsPath(steps []yamldecode.Step) paths.Path {
	path := paths.Current()

	for _, step := range steps {
		switch {
		case step.IsIndex:
			path = path.Index(step.Index)
		case step.IsKey:
			path = path.Child(step.Key).Key()
		default:
			path = path.Child(step.Key)
		}
	}

	return path
}

// boundAt returns the position at which n binds err, or the zero position
// when n binds it without a position of its own.
func (n *Node) boundAt(err error) position.Position {
	bound, ok := n.bindOwn(err).(*SourceError) //nolint:errorlint // A binding below a wrapper is not one n made.
	if !ok || bound.node != n {
		return position.Position{}
	}

	pos, _ := bound.Position()

	return pos
}

// problemsError returns the error that reports problems, or nil when
// there are none. One problem comes back alone. Several come back in the
// order of the source, as the problems of a [Summary] that counts them.
// The Summary counts unknown fields when each problem is one.
func problemsError(problems []decodeProblem) error {
	switch len(problems) {
	case 0:
		return nil
	case 1:
		return problems[0].err
	}

	sortProblems(problems)

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

// rejectionCause returns err, a rejection at a source token, as the cause
// of the [*Error] that [Node.useRejection] returns.
func (n *Node) rejectionCause(err yaml.Error) error {
	msg, label := n.doc.rejectionText(err)

	return markDecode(yamlMessage(err, msg), label)
}

// useRejection returns err, a rejection at a source token, as a problem
// of a value that the decode read at path, a `$` path through the aliases
// and `<<` merge keys that read it. The problem carries path when path
// binds at the node of the token, as a [Validator] error at path binds
// there. A value inside an anchor that an alias brings in thus reports the
// path through the alias, at the position of the value. A path that
// selects an alias, which the document holds for the value itself, also
// names the use, and the problem carries it at the position of the node of
// the token. Where path binds elsewhere, as when a later entry of the same
// key takes it, the problem binds where [Node.rejectionLocation] puts the
// token. So does the rejection of the value of the decode itself.
func (n *Node) useRejection(err yaml.Error, path paths.Path) decodeProblem {
	tk := err.GetToken()
	owner := n.doc.pathIndex().OwnerToken(tk)
	problem := decodeProblem{at: position.NewFromToken(tk)}

	var opts []ErrorOption

	switch {
	case path.Equal(n.base) || owner == nil:
		opts = n.rejectionLocation(tk)

	case n.bindsAt(path, owner):
		opts = []ErrorOption{AtPath(path)}

	case n.selectsAlias(path):
		opts = []ErrorOption{AtPath(path), AtToken(owner)}

	default:
		opts = n.rejectionLocation(tk)
	}

	problem.err = Invalid(n.rejectionCause(err), opts...)

	return problem
}

// selectsAlias reports whether path, a `$` path, selects an alias that the
// document writes and that the resolver of the document follows.
func (n *Node) selectsAlias(path paths.Path) bool {
	tk, err := n.doc.pathResolver().Token(path)

	return err == nil && tk != nil && tk.Type == token.AliasType
}

// decodeProblem is one problem of a decode, before the Node binds it.
type decodeProblem struct {
	// The problem, located at the path of the use that read the value.
	err error
	// The position that marks the problem among the others, which is the
	// position of the value the problem is about.
	at position.Position
	// The order in which the decoder reported the problem, which orders
	// the problems at one position.
	order int
	// Whether the problem is the rejection of an unknown field.
	unknownField bool
}

// sortProblems sorts problems in the order of the source. The problems at
// one position come in the order the decoder reported them.
func sortProblems(problems []decodeProblem) {
	slices.SortFunc(problems, func(a, b decodeProblem) int {
		return cmp.Or(a.at.Compare(b.at), cmp.Compare(a.order, b.order))
	})
}

// walkRules returns the [decodewalk.Rules] of a decode of n with the
// unmarshalers u, for a walk that asks dec about the values it reaches.
// The walk reads one error of each decode, the error that [firstError]
// picks.
func (n *Node) walkRules(ctx context.Context, dec *yamldecode.Decoder, u unmarshaler.Options) decodewalk.Rules {
	return decodewalk.Rules{
		Resolver: n.doc.pathResolver(),
		Decode: func(node ast.Node, v any) error {
			return firstError(decodeWithRecover(ctx, dec, node, v))
		},
		Unmarshalers:   u,
		Anchor:         n.source.referenceAlias,
		AnchorKeys:     n.referenceAnchorKeys,
		PastAliasLimit: n.doc.pastAliasLimit,
		Keys:           &n.source.mergeKeys,
	}
}

// referenceAnchorKeys returns the keys of the merge sources under the
// anchor of a reference document that alias reads, as
// [reftable.Table.Keys] keeps them. The walks of every Source that reads
// the anchor share them. It returns nil when the reference documents do
// not define an anchor of the name of alias.
func (n *Node) referenceAnchorKeys(alias *ast.AliasNode) *decodewalk.Keys {
	name, ok := astnode.Name(alias.Value)
	if !ok {
		return nil
	}

	return n.source.referenceAnchors().Keys(name)
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
// error at that path binds. The path reads through the `<<` merge keys of
// the document, as [pathindex.Index.MergedOwnerPath] spells it, when it
// binds at the node. A later entry of the same key can take that path. The
// path then goes through the merge keys, as [pathindex.Index.OwnerPath]
// spells it. It adds the token of the node when a later entry with the
// same key takes that path too, or when the path selects nothing, as below
// a tag that converts a collection. So an alias binds at its "*" as it
// does where the path selects it.
//
// For a Node at a local alias, a rejection at the root of the content
// that the alias reads binds at the path of the Node, and thus at the
// alias. A rejection at the root of a Node at a tagged alias or at an
// alias to a reference document binds there too.
func (n *Node) rejectionLocation(tk *token.Token) []ErrorOption {
	index, resolver := n.doc.pathIndex(), n.doc.pathResolver()

	path, selects, ok := index.OwnerPath(resolver, tk)
	if !ok {
		return []ErrorOption{AtToken(tk)}
	}

	if n.readsRoot(tk) {
		return []ErrorOption{AtPath(n.base)}
	}

	if merged, ok := index.MergedOwnerPath(resolver, tk); ok && n.bindsAt(merged, index.OwnerToken(tk)) {
		return []ErrorOption{AtPath(merged)}
	}

	if selects {
		return []ErrorOption{AtPath(path)}
	}

	return []ErrorOption{AtPath(path), AtToken(index.OwnerToken(tk))}
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

		return relocated(problem, AtPath(at.path))
	}

	located := *x
	located.loc = nil
	located.relocates = true

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
