package niceyaml

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"reflect"
	"runtime/debug"
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
	"go.jacobcolvin.com/niceyaml/internal/rejection"
	"go.jacobcolvin.com/niceyaml/internal/taggedscalar"
	"go.jacobcolvin.com/niceyaml/internal/unmarshaler"
	"go.jacobcolvin.com/niceyaml/internal/yamlfield"
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
	unmarshalers  unmarshaler.Options
	unknownFields UnknownFieldsMode
	// Whether the go-yaml decoder of a decode under UnknownFieldsReject
	// accepts unknown fields. [Node.decodeUnbound] sets it after the
	// decoder panics on an inline map. The search for unknown fields then
	// rejects them in its place.
	searchesUnknown    bool
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
// WithValueCheck panics for a pointer or interface T, because the walk
// never checks a value of such a type. It also panics if fn is nil.
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
// WithNodeCheck panics for a pointer or interface T, because the walk
// never checks a value of such a type. It also panics if fn is nil.
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
// Under UnknownFieldsWarn, a decode searches the document for unknown
// fields, even when the decode passes. Under UnknownFieldsReject, the
// search runs when the decoder rejects the document. It also runs after a
// decode whose decoder panics on an inline map, as the paragraph on
// inline maps describes.
//
// The search decodes once more each struct that decodes itself from a
// mapping and that it reads, so the unmarshaler of that struct runs again.
// It skips each such struct that another struct holds inline. The aliases
// of one anchor share that decode. When the decode rejects a field, the
// search runs the unmarshaler of the outermost such struct again for each
// key at or below it. Those calls get a context without the collector, so
// their warnings do not reach it. A decode under UnknownFieldsWarn that
// such a call runs with that context does not fail for the missing
// collector, and it drops its warnings.
//
// UnknownFieldsWarn and UnknownFieldsReject differ beyond the severity of
// the fields. Under UnknownFieldsReject, the decoder stops at the first
// unknown field, so the Validate and ValidateContext methods of the value
// do not run. Under UnknownFieldsWarn, they run when the decoder accepts
// the rest of the document.
//
// UnknownFieldsWarn reports the fields that the search finds. The search
// does not read below a value that decodes itself, except a struct whose
// UnmarshalYAML method decodes it through the decoder. UnknownFieldsWarn
// thus does not report a field below a value that a function of
// [CustomUnmarshaler] decodes, or below a slice with an UnmarshalYAML
// method. UnknownFieldsReject reports such a field when the decoder
// rejects it before any other value.
//
// The search does not read a reference document either. UnknownFieldsWarn
// thus does not report a key behind an alias or a `<<` merge key that reads
// one, even in a decode of the Node of such an alias. UnknownFieldsReject
// reports such a key when the decoder rejects it before any other value. Its
// error binds at the alias that led the decoder to the key, as the References
// section of the package documentation describes. The decoder picks the
// unknown field that it rejects in a mapping in no fixed order. Two decodes
// of one document can thus report different keys of a reference document,
// or one decode none.
//
// The search looks up the keys of such a merge key through the anchors of
// the reference document, as the decoder reads them. The decoder reads the
// last entry of a key, so the search does not report an entry that a later
// merge key replaces. The search reads below the value of an entry that
// comes before the merge key in the same mapping when the merge key does not
// bring in the key of that entry. When the merge key brings in that key, the
// decoder reads the value of the reference document, and the search does not
// read below it.
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
// A map that a struct holds inline takes every key that no other field of
// the struct reads. The struct thus does not have an unknown field. A
// struct that decodes itself still decides for its own fields, because
// its UnmarshalYAML method might not read the map. The search does not
// read below the members of an inline map, so neither mode reports an
// unknown field there.
//
// Under UnknownFieldsReject, the go-yaml decoder panics when the document
// leads it to a struct that holds a map inline. The panic comes also from
// a struct that an UnmarshalYAML method decodes into, and from a struct
// that holds inline a struct with an inline map that reads an anchor. A
// decode that does not lead the decoder to such a struct keeps the check
// of the decoder, even for a type that holds one.
//
// After the panic, the decode runs again from the value that v held, with
// a decoder that accepts unknown fields. That decoder fills the value as
// it does under UnknownFieldsWarn. It leaves out each admitted key that no
// read keeps, as [WithAllowedFields] describes. When the search then finds
// a field, the decode fails with an error for each one, and the
// self-validation walk does not run. When the search finds none, the walk
// runs.
//
// The decode that runs again calls each unmarshaler that the first decode
// reached a second time. The collector of ctx gets only the warnings of
// the second decode.
//
// A decode that runs again thus reports the fields that UnknownFieldsWarn
// reports. It also reports a field below a value that a function of
// [CustomUnmarshaler] decodes. That function starts a decode of its own,
// which rejects unknown fields again. It does not report a field
// below a slice with an UnmarshalYAML method, nor a key behind an alias
// that reads a reference document, even outside the struct with the
// inline map.
//
// The search confirms a field below a struct that decodes itself with the
// UnmarshalYAML method of that struct. When the method decodes into a
// type that makes the decoder panic so, the panic does not name a field.
// Neither mode then reports an unknown field at or below that struct. The
// method might decode into a type that holds a map inline, or into a
// mirror of a struct that holds inline a struct with an inline map that
// reads an anchor.
//
// A struct that decodes itself decides for its own fields. When an
// UnmarshalYAML method decodes into a second type with the same fields,
// the decoder checks those fields. When the method parses the text itself,
// the decoder does not check them.
//
// Below a struct that decodes itself into fields that do not mirror the
// document, the report holds an unknown field only when the decoder names
// it. The report can also hold a key that a function from
// [yaml.RegisterCustomUnmarshaler] accepts. A function from
// [CustomUnmarshaler] does not have that limit.
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
// interface takes the key. The decoder fills each field that takes it.
// When one read of a key keeps it, each read of the key that does not
// keep it reports an unknown field. In the document shown, a type of
// base with a type field thus has the decode report `$.nested.type~`
// when the type of nested does not have one.
//
// The decode leaves out each admitted key that no read keeps. A value
// that decodes itself, such as one with an UnmarshalYAML method, does not
// keep an admitted key below it. Its unmarshaler thus sees the key only
// when another read keeps it. An admitted key that the decode leaves out
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
// it passes CustomUnmarshaler[T](nil) to [OverrideTypeRules]. Give the
// function to CustomUnmarshaler rather than to
// [yaml.RegisterCustomUnmarshaler], whose types a decode cannot see.
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

	return TypeRule{apply: func(c *decodeConfig) {
		c.unmarshalers = c.unmarshalers.With(t, opt)
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
// ErrDecode. The Decoding section describes the messages, the sentinels
// of each problem, and where each error binds.
func (n *Node) DecodeInto(ctx context.Context, v any, opts ...DecodeOption) error {
	return n.decodeInto(enterCall(ctx), v, newDecodeConfig(n.source, opts), nil)
}

// decodeInto is [Node.DecodeInto] with its settings resolved. It also
// collects the comments of the node in comments, unless comments is nil,
// each keyed by a path from the node, as [Node.YAMLComments] describes.
func (n *Node) decodeInto(ctx context.Context, v any, cfg decodeConfig, comments yaml.CommentMap) error {
	err := checkDecode(ctx, v, cfg)
	if err != nil {
		return n.bindOwn(err)
	}

	// A reference document that does not parse, or one past a limit,
	// fails every decode with the error of its parse.
	if n.source.referenceErr != nil {
		return n.Bind(n.source.referenceErr)
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
	// because the decoder stops at the first and reads no value past it.
	node := n.valueNode()

	err = n.bindOwn(AsErrors(n.decodeUnbound(ctx, node, v, cfg, comments)))

	// A decode of an anchor files the comments of the anchor as well as
	// those of its value. A decode of an alias files only those of the
	// content it reads.
	read, alias := n.readContent(node)

	filer := node
	if alias != nil {
		filer = read
	}

	rebaseComments(comments, read, filer)

	if cfg.unknownFields == UnknownFieldsWarn {
		n.warnUnknownFields(ctx, reflect.TypeOf(v).Elem(), cfg, err)
	}

	if err != nil {
		return err
	}

	n.ownNodes(v)

	if cfg.skipSelfValidation {
		return nil
	}

	return n.selfValidate(ctx, v, cfg)
}

// isNodeType reports whether the go-yaml decoder sets a value of type t to
// the node it decodes.
func isNodeType(t reflect.Type) bool {
	return t == astNodeType || unmarshaler.IsSyntaxNode(t)
}

// reachesNodes reports whether a decode into a value of type t can set a
// node of the syntax tree in the value, outside the types that decode
// themselves.
func reachesNodes(t reflect.Type) bool {
	return holdsNodes.Get(t, func(t reflect.Type) bool {
		return yamlfield.Reaches(t, func(t reflect.Type) (bool, bool) {
			return isNodeType(t), unmarshaler.DecodesItself(t)
		})
	})
}

// ownNodes puts the node of the document in place of each node of the
// decoder's tree in v, the target of a decode of n. The decoder reads a
// tree that can rename anchors and copy nodes, as [decodetree.Tree]
// describes. It sets each [ast.Node] in v to a node of that tree. The
// swap reads the fields, elements, and entries that the decoder fills. It
// does not read below a type that decodes itself, so a node that such a
// type keeps stays the decoder's. A node of a reference document stays
// too, because the document does not hold it. A v whose type reaches no
// node costs nothing.
func (n *Node) ownNodes(v any) {
	rv := reflect.ValueOf(v)
	if !reachesNodes(rv.Type().Elem()) {
		return
	}

	s := nodeSwapper{tree: n.doc.tree(), seen: map[uintptr]map[reflect.Type]bool{}}
	s.walk(rv.Elem())
}

// nodeSwapper puts the nodes of the document in place of the nodes of a
// [decodetree.Tree] in a decoded value, as [Node.ownNodes] describes.
type nodeSwapper struct {
	// The tree the decoder read.
	tree *decodetree.Tree
	// The types read at each address the walk followed a pointer to, so a
	// value that points to itself ends the walk.
	seen map[uintptr]map[reflect.Type]bool
}

// walk swaps the nodes in v, a value that the walk can set. It reports
// whether it set a node.
func (s nodeSwapper) walk(v reflect.Value) bool {
	t := v.Type()
	if !reachesNodes(t) {
		return false
	}

	if t == astNodeType || t.Kind() == reflect.Pointer && unmarshaler.IsSyntaxNode(t.Elem()) {
		return !v.IsNil() && s.swap(v)
	}

	// The decoder sets a node held by value to a copy of its own node. The
	// copy shares the slices of that node, so the walk sets the whole value
	// and never writes below it.
	if unmarshaler.IsSyntaxNode(t) {
		return s.swapValue(v)
	}

	changed := false

	switch v.Kind() {
	case reflect.Pointer:
		if !v.IsNil() && s.enter(v) {
			changed = s.walk(v.Elem())
		}

	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice && (v.IsNil() || !s.enter(v)) {
			return false
		}

		for i := range v.Len() {
			changed = s.walk(v.Index(i)) || changed
		}

	case reflect.Map:
		changed = s.walkMap(v)

	case reflect.Struct:
		for i, field := range slices.Collect(t.Fields()) {
			if _, _, skip := yamlfield.Name(field); skip {
				continue
			}

			if f := v.Field(i); f.CanSet() {
				changed = s.walk(f) || changed
			}
		}

	default:
	}

	return changed
}

// enter reports whether the walk reads v, a pointer or a slice, for the
// first time as a value of its type.
func (s nodeSwapper) enter(v reflect.Value) bool {
	addr := v.Pointer()

	types := s.seen[addr]
	if types == nil {
		types = map[reflect.Type]bool{}
		s.seen[addr] = types
	}

	if types[v.Type()] {
		return false
	}

	types[v.Type()] = true

	return true
}

// walkMap swaps the nodes in the keys and the values of v, a map, and
// reports whether it set a node. A map does not hand out its entries to
// set, so the walk swaps the nodes in a copy of each entry. When it
// changes an entry, it clears v and adds every entry back. A key that does
// not equal itself, such as a NaN, thus keeps one entry for each entry
// the map held.
func (s nodeSwapper) walkMap(v reflect.Value) bool {
	if v.IsNil() || !s.enter(v) {
		return false
	}

	t := v.Type()
	keys, values := reachesNodes(t.Key()), reachesNodes(t.Elem())

	type entry struct{ key, value reflect.Value }

	entries := make([]entry, 0, v.Len())

	for iter := v.MapRange(); iter.Next(); {
		entries = append(entries, entry{key: iter.Key(), value: iter.Value()})
	}

	changed := false

	for i, e := range entries {
		key := reflect.New(t.Key()).Elem()
		key.Set(e.key)

		value := reflect.New(t.Elem()).Elem()
		value.Set(e.value)

		keyChanged := keys && s.walk(key)
		valueChanged := values && s.walk(value)

		if keyChanged || valueChanged {
			entries[i] = entry{key: key, value: value}
			changed = true
		}
	}

	if !changed {
		return false
	}

	v.Clear()

	for _, e := range entries {
		v.SetMapIndex(e.key, e.value)
	}

	return true
}

// swap sets v, an [ast.Node] or a pointer to a node type, to the node of
// the document that the node in v stands for, and reports whether that
// node differs from the node in v. It leaves v as it is when the document
// does not hold such a node, or holds one of another type.
func (s nodeSwapper) swap(v reflect.Value) bool {
	node, ok := reflect.TypeAssert[ast.Node](v)
	if !ok {
		return false
	}

	original, ok := s.tree.Original(node)
	if !ok || original == node {
		return false
	}

	o := reflect.ValueOf(original)
	if !o.Type().AssignableTo(v.Type()) {
		return false
	}

	v.Set(o)

	return true
}

// swapValue sets v, a node type of the go-yaml ast package held by value,
// to a copy of the node of the document that v stands for. It reports
// whether it set v, and it leaves v as it is when the document does not hold
// such a node, or holds one of another type.
func (s nodeSwapper) swapValue(v reflect.Value) bool {
	node, ok := reflect.TypeAssert[ast.Node](v.Addr())
	if !ok {
		return false
	}

	original, ok := s.tree.Original(node)
	if !ok || original == node {
		return false
	}

	o := reflect.ValueOf(original)
	if o.Type() != v.Addr().Type() {
		return false
	}

	v.Set(o.Elem())

	return true
}

// warnUnknownFields hands each unknown field of a decode of n into a value
// of type t to the [Warnings] collector of ctx, in the order of the
// source, as [UnknownFieldsWarn] describes. A decoder that rejects unknown
// fields confirms each one, with the other settings of cfg. The search
// does not run when ctx does not carry a collector, when ctx has ended, or
// when err, the error of the decoder step, holds a panic.
func (n *Node) warnUnknownFields(ctx context.Context, t reflect.Type, cfg decodeConfig, err error) {
	if scopeOf(ctx) == nil || ctx.Err() != nil || holdsPanic(err) || !astnode.HasContent(n.valueNode()) {
		return
	}

	strict := cfg
	strict.unknownFields = UnknownFieldsReject

	c := newProblemCollector(ctx, n, strict)
	c.register(n.valueNode())
	c.collectUnknown(t, n.valueNode())

	if ctx.Err() != nil {
		return
	}

	problems := slices.Collect(maps.Values(c.found))
	sortProblems(problems)

	for _, problem := range problems {
		n.Warn(ctx, problem.err)
	}
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

// checkDecode returns the error of a decode into v with cfg that cannot
// start, or nil. For a v that [checkTarget] rejects, the error wraps
// [ErrDecodeTarget]. For a decode under [UnknownFieldsWarn] whose ctx does
// not carry a collector, it wraps [ErrNoWarnings]. A decode that niceyaml
// runs again with a context from [withoutWarnings] drops its warnings on
// purpose, so it passes the check. Each error is a mistake of the caller,
// so it comes inside [Unchecked].
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
// decoder panics on such a target or ignores it. [allocate] fills a nil
// pointer that v points to. The target is a mistake of the caller and no
// fault of the document, so the error comes inside [Unchecked]. A check
// that returns it as it is thus does not report a fault.
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

	if cfg.unknownFields == UnknownFieldsReject && !cfg.searchesUnknown {
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

// ownDecode decodes the first document of text into an any with go-yaml's
// decoder alone, as yaml.Unmarshal decodes it. With comments, the decoder
// parses the text with its comments, as yaml.CommentToMap asks it to. The
// decoder parses the text itself, because its decode of a parse that it did
// not make keeps the value that its first pass gave each anchor, so an alias
// inside the value of its own anchor reads that value there and not the
// anchor again. The decoder reads the text of each reference document of s,
// and it takes a key that a mapping repeats only when s allows duplicate
// keys. A panic of the decoder comes back as a [*PanicError].
func (s *Source) ownDecode(ctx context.Context, text string, comments bool) (any, error) {
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

	if comments {
		opts = append(opts, yaml.CommentToMap(yaml.CommentMap{}))
	}

	func() {
		defer func() {
			if p := recover(); p != nil {
				v, err = nil, &PanicError{Value: p, Stack: debug.Stack()}
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
	// outer decode, not in the text of the value.
	cfg.knownKeys = nil

	return valueError(doc.decodeUnbound(ctx, doc.AST(), dst, cfg, nil))
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

// rebaseComments keys each comment of comments from read, the node that
// a decode read, so `$` names the decoded value. The go-yaml decoder keys
// each comment by the path from the root of the document of its node.
//
// The node filer is the one whose own comments the decode files. For a
// decode of an anchor, filer is the anchor. Otherwise filer and read are
// the same node. A comment stays in
// the map only when filer or a node below it files a comment of its kind
// under its key. The comments that leave belong to an anchor outside
// filer that an alias or a merge key in filer names.
//
// Every key that filer files extends the path of read at `.`, `[`, or
// the end of the key. The kinds filter thus keeps only keys under the
// prefix, and the boundary test guards against a key that no input
// produces.
func rebaseComments(comments yaml.CommentMap, read, filer ast.Node) {
	if len(comments) == 0 {
		return
	}

	prefix := ""
	if !astnode.IsNil(read) {
		prefix = read.GetPath()
	}

	own := commentKinds(filer)
	rebased := make(yaml.CommentMap, len(comments))

	for key, cs := range comments {
		rest, ok := strings.CutPrefix(key, prefix)
		if !ok || prefix == "" || (rest != "" && rest[0] != '.' && rest[0] != '[') {
			continue
		}

		var kept []*yaml.Comment

		for _, c := range cs {
			if own[key][c.Position] {
				kept = append(kept, c)
			}
		}

		if len(kept) != 0 {
			rebased["$"+rest] = kept
		}
	}

	clear(comments)
	maps.Copy(comments, rebased)
}

// commentKinds returns the keys and kinds under which the go-yaml decoder
// files the comments of node and of the nodes below it. Like the decoder,
// it takes a comment group above the token of its node as a head comment,
// and one on the line of that token as a line comment. It does not
// follow an alias, so it leaves out the comments of the anchors that
// aliases name.
func commentKinds(node ast.Node) map[string]map[yaml.CommentPosition]bool {
	kinds := map[string]map[yaml.CommentPosition]bool{}
	add := func(path string, kind yaml.CommentPosition) {
		if kinds[path] == nil {
			kinds[path] = map[yaml.CommentPosition]bool{}
		}

		kinds[path][kind] = true
	}

	astnode.Inspect(node, func(n ast.Node) bool {
		if n.Type() == ast.CommentType {
			return false
		}

		var foot *ast.CommentGroupNode

		switch n := n.(type) {
		case *ast.SequenceNode:
			for i, head := range n.ValueHeadComments {
				if hasComments(head) && i < len(n.Values) {
					add(n.Values[i].GetPath(), yaml.CommentHeadPosition)
				}
			}

			if hasComments(n.GetComment()) && len(n.Values) != 0 {
				add(n.Values[0].GetPath(), yaml.CommentHeadPosition)
			}

			if hasComments(n.FootComment) {
				add(n.FootComment.GetPath(), yaml.CommentFootPosition)
			}

			return true

		case *ast.MappingNode:
			foot = n.FootComment

		case *ast.MappingValueNode:
			foot = n.FootComment
		}

		if path, kind, ok := headOrLineKind(n); ok {
			add(path, kind)
		}

		if hasComments(foot) {
			add(foot.GetPath(), yaml.CommentFootPosition)
		}

		return true
	})

	return kinds
}

// headOrLineKind returns the key and kind under which the go-yaml decoder
// files the comment of node, which is not a sequence. It reports false
// when node holds no comment.
func headOrLineKind(n ast.Node) (string, yaml.CommentPosition, bool) {
	group := n.GetComment()
	tok := n.GetToken()

	if !hasComments(group) || tok == nil {
		return "", 0, false
	}

	first := math.MaxInt
	for _, c := range group.Comments {
		first = min(first, c.Token.Position.Line)
	}

	if first >= tok.Position.Line {
		return n.GetPath(), yaml.CommentLinePosition, true
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

	return path, yaml.CommentHeadPosition, true
}

// hasComments reports whether group holds a comment.
func hasComments(group *ast.CommentGroupNode) bool {
	return group != nil && len(group.Comments) != 0
}

// decodeUnbound decodes node into v with cfg and returns the error for
// the Node to bind. A node without content or a null leaves v as it is,
// so defaults survive, but an untagged null clears a pointer or interface.
//
// Under [UnknownFieldsReject], the go-yaml decoder panics on a map that a
// struct holds inline, as [panicsOnInlineMap] reports. After such a panic,
// the decode sets v back to what it held and runs again with a decoder
// that accepts unknown fields, as [decodeConfig] describes for
// searchesUnknown. A decode that a function of a [CustomUnmarshaler] runs
// starts with a decoder that rejects them again. The collectors of ctx
// get the warnings of the first pass only when that pass does not panic
// so.
func (n *Node) decodeUnbound(
	ctx context.Context,
	node ast.Node,
	v any,
	cfg decodeConfig,
	comments yaml.CommentMap,
) error {
	cfg.searchesUnknown = false

	if cfg.unknownFields != UnknownFieldsReject {
		return n.decodeOnce(ctx, node, v, cfg, comments)
	}

	restore := saveTarget(v)

	// The first pass holds its warnings until it ends, because the decode
	// that runs again after a panic hands the same warnings to ctx.
	first := ctx
	held := NewWarnings()

	if scopeOf(ctx) != nil {
		first = isolate(ctx, held)
	}

	err := n.decodeOnce(first, node, v, cfg, comments)
	if !panicsOnInlineMap(err) {
		handOn(ctx, held)

		return err
	}

	restore()

	cfg.searchesUnknown = true

	return n.decodeOnce(ctx, node, v, cfg, comments)
}

// saveTarget returns a function that sets each value along the chain of
// pointers that v, a non-nil pointer, reaches back to what it holds now.
// The chain ends at a value that is not a pointer, at a nil pointer, or
// past [maxPointerDepth] pointers. The copy of each value is shallow.
func saveTarget(v any) func() {
	var held, saved []reflect.Value

	rv := reflect.ValueOf(v).Elem()

	for range maxPointerDepth + 1 {
		c := reflect.New(rv.Type()).Elem()
		c.Set(rv)

		held = append(held, rv)
		saved = append(saved, c)

		if rv.Kind() != reflect.Pointer || rv.IsNil() {
			break
		}

		rv = rv.Elem()
	}

	return func() {
		for i, rv := range held {
			rv.Set(saved[i])
		}
	}
}

// panicsOnInlineMap reports whether err holds the panic of the go-yaml
// decoder on a struct that holds a map inline. The decoder raises it only
// when it rejects unknown fields. Its check for them reads the fields of
// each inline type as those of a struct. The check reads every inline
// field of an inline struct. So the decoder also panics on a map that
// reads an anchor when a struct holds the struct of that map inline.
func panicsOnInlineMap(err error) bool {
	p, ok := errors.AsType[*PanicError](err)

	return ok && bytes.Contains(p.Stack, []byte("go-yaml.(*Decoder).deleteStructKeys("))
}

// decodeOnce is [Node.decodeUnbound] with one decoder, which rejects
// unknown fields under [UnknownFieldsReject] unless cfg sets
// searchesUnknown.
func (n *Node) decodeOnce(
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

	value, alias := n.readContent(node)
	isAlias := alias != nil

	// A document body or a Node at an alias that reads a reference document
	// holds the alias. The decode reads the content of its anchor there, so
	// the tests for a null read that content, with any tag on it.
	null := astnode.IsNil(value) || value.Type() == ast.NullType
	kind := reflect.TypeOf(v).Elem().Kind()

	if !astnode.HasContent(node) || astnode.IsTaggedNull(value) ||
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

	cut := n.admitKnownKeys(ctx, n.walkRules(ctx, dec, cfg.unmarshalers), cfg, reflect.TypeOf(v).Elem(), node, view)

	// The view that the decoder reads leaves out entries and the anchors in
	// them. Its copies of the mappings that merge keys bring in do not hold
	// the anchors of those mappings either. The decoder reads the view
	// without the cut first, so it registers every anchor of the node in
	// document order. Each alias then reads its anchor as the document
	// writes it. The decode that follows reports any error of the view.
	if cut.read != view {
		var sink any

		_ = decodeWithRecover(ctx, dec, view, &sink) //nolint:errcheck // The decode of the cut reports it.
	}

	// The decoder registers each anchor outside node again with the cut
	// content, so its aliases read the anchor without the cut entries. The
	// read view does the same for the anchors inside node.
	for _, anchor := range cut.anchors {
		var sink any

		err = decodeWithRecover(ctx, dec, anchor, &sink)
		if err != nil {
			return n.decodeRejection(n.rejection(err, anchor, firstRead))
		}
	}

	view = cut.view

	// The pass decoded anchors outside node with dec, which wrote their
	// comments to the map. So did the read of the view without the cut.
	clear(comments)

	// The decoder tests an alias for null, rather than the anchor it
	// names, so it gives a pointer target a value for an alias to a null.
	if isAlias && null && kind == reflect.Pointer {
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

	// At the root of a decode, the go-yaml decoder hands an UnmarshalText
	// method empty text for an alias whose anchor another parse holds, as a
	// reference document does. The decoder reads the anchor in place of
	// such an alias. An alias inside the anchor that names it then reads
	// null, as it does where the alias lies below the root.
	read := cut.read
	if isAlias {
		if anchor, ok := n.rootReference(alias); ok {
			read = anchor
		}
	}

	// The recover turns a panic in the go-yaml decoder, or in a value's
	// own UnmarshalYAML, into an error that holds a PanicError.
	err = decodeWithRecover(ctx, dec, read, target)

	// The caller runs the decode again after a panic on an inline map, so
	// the decode does not look for the problems of this pass.
	if cfg.unknownFields == UnknownFieldsReject && !cfg.searchesUnknown && panicsOnInlineMap(err) {
		return err
	}

	// The decoder starts each value it creates from zero, so the Node
	// builds those values again from their defaults.
	if err == nil && start.IsValid() {
		err = n.fillDefaults(ctx, dec, cfg, node, target, start, cut)
	}

	// The decoder returns the error of a value that decodes itself with
	// no token, so the Node finds the value that reported it.
	order := n.readOrder(ctx, cfg, reflect.TypeOf(v).Elem(), node, err)
	err = n.locateDecodeError(ctx, n.rejection(err, view, order), node, v, cfg)

	if err == nil && cfg.searchesUnknown {
		return n.unknownFieldProblems(ctx, node, v, cfg)
	}

	// The decoder stops at its first rejection, so the Node finds the
	// other problems of the document and reports them together.
	return n.decodeProblems(ctx, err, node, v, cfg)
}

// unknownFieldProblems returns the error of a decode of node into v with
// cfg under [UnknownFieldsReject] whose decoder accepted the document
// without rejecting unknown fields, as [decodeConfig] describes for
// searchesUnknown. The error reports each unknown field that the search
// finds, as [Node.decodeProblems] reports them. It is nil when the search
// finds none, and the error of ctx when ctx ends. The search confirms
// each field with a decoder that rejects unknown fields.
func (n *Node) unknownFieldProblems(ctx context.Context, node ast.Node, v any, cfg decodeConfig) error {
	cfg.searchesUnknown = false

	c := newProblemCollector(ctx, n, cfg)
	c.register(node)
	c.collectUnknown(reflect.TypeOf(v).Elem(), node)

	err := ctx.Err()
	if err != nil {
		return err //nolint:wrapcheck // The caller binds the error.
	}

	return problemsError(slices.Collect(maps.Values(c.found)))
}

// rootReference returns the anchor of a reference document that alias
// reads, as [reftable.Table.View] gives it. Such an alias is one that the
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

	anchor, ok := n.source.referenceAnchors().View(name)
	if !ok {
		return nil, false
	}

	return anchor, true
}

// rejection places err, an error that the go-yaml decoder returned for
// scope without a source token, at the node in scope that caused it. For
// a token of a reference document, that node is the alias that led the
// decoder to the token, as [Node.referenceRead] finds it with order. The
// result is an [*Error] that matches [ErrDecode] and [errPlaced]. Any
// other err comes back as it is.
func (n *Node) rejection(err error, scope ast.Node, order readOrder) error {
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

		alias, found := n.referenceRead(refs, yamlErr.GetToken(), order)
		if !found {
			return n.referenceRejection(yamlErr, nil)
		}

		return n.referenceRejection(yamlErr, &alias)
	}

	if at == nil {
		return err
	}

	rejected := markDecode(fmt.Errorf("decoder rejected the value: %w", cause), errPlaced)

	return Invalid(rejected, AtToken(at))
}

// referenceRejection returns err, a rejection at a token of a reference
// document, as an [*Error] that matches [ErrDecode], its label, and
// [errPlaced]. The Error binds at alias, the alias that led the decoder to
// the token, as [Node.referenceLocation] locates it. A nil alias leaves
// the Error without a location.
func (n *Node) referenceRejection(err yaml.Error, alias *decodetree.ReferenceAlias) error {
	msg, label := n.doc.rejectionText(err)
	rejected := markDecode(yamlMessage(err, msg), label, errPlaced)

	if alias == nil {
		return Invalid(rejected)
	}

	return Invalid(rejected, n.referenceLocation(*alias, err.GetToken())...)
}

// readOrder picks the alias that led the decoder to a rejection among
// aliases, the aliases of a reference document that reach its token, in
// document order. It reports false when it cannot tell.
type readOrder func(aliases []decodetree.ReferenceAlias) (decodetree.ReferenceAlias, bool)

// firstRead is the [readOrder] of a decode into an interface, which reads
// each value in document order.
func firstRead(aliases []decodetree.ReferenceAlias) (decodetree.ReferenceAlias, bool) {
	return aliases[0], true
}

// referenceRead returns the alias among refs, the aliases of a reference
// document that a decode reads, that led the decoder to tk, a token of a
// reference document. The anchor of that alias reaches tk, as
// [reftable.Places.Paths] finds the places of a token. When refs holds one
// alias, or one alias of refs reaches tk, referenceRead returns that
// alias. When several do, order picks among them. It reports false when
// none does, or order cannot tell.
func (n *Node) referenceRead(
	refs []decodetree.ReferenceAlias, tk *token.Token, order readOrder,
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
			anchor, held := table.View(ref.Name)
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
	default:
		return order(reached)
	}
}

// referenceLocation returns the options that locate a rejection at tk, a
// token of a reference document, which the decoder read through alias. The
// rejection binds at the alias. Its path is the path of the value of tk.
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

	anchor, ok := table.View(alias.Name)
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

// readOrder returns the [readOrder] of a decode of node into a value of
// type t with cfg, which the decoder rejected with err, as
// [problemCollector.firstRead] picks the alias.
func (n *Node) readOrder(ctx context.Context, cfg decodeConfig, t reflect.Type, node ast.Node, err error) readOrder {
	return func(aliases []decodetree.ReferenceAlias) (decodetree.ReferenceAlias, bool) {
		c := newProblemCollector(ctx, n, cfg)
		c.register(node)

		return c.firstRead(t, node, err, aliases)
	}
}

var (
	// The mark of an error of a decode that [decodeWithRecover] or
	// [Node.rejection] placed in the source, so [Node.locateDecodeError]
	// does not look for the value that reported it.
	errPlaced = errors.New("placed")

	// The [ast.Node] interface, which the go-yaml decoder sets to the
	// node it decodes.
	astNodeType = reflect.TypeFor[ast.Node]()

	// Whether a decode into each type reaches a node of the syntax tree,
	// as [reachesNodes] reports.
	holdsNodes unmarshaler.TypeCache[bool]
)

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
			AtToken(astnode.StartToken(at)))
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
// [Node.Scalar] returns the text of a scalar as the document writes it,
// such as a version or a file mode.
//
// An [ast.Node] in the result is the node that [Node.AST] returns for its
// place, and at an alias it is the content of the anchor. The node is
// part of the tree that [Source.File] returns, so a caller must not
// modify it. The Decoding section of the package documentation names the
// nodes that a decode hands out from a tree of its own.
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

	err = n.decodeInto(ctx, &v, cfg, nil)
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
// Go-yaml's encoder writes some floats in exponent form without a
// fraction, such as 1e-05 for 0.00001 and 1e+06 for 1000000.0. Go-yaml
// reads that text as a string, so the encode of a Node that holds such a
// float decodes to a string in its place.
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
// keys and items in a value that it writes with an anchor or a tag. The
// value of a struct field with the anchor option in its yaml tag is such
// a value.
//
// A path with an index names a position in a list, not the item that
// held it. After the program inserts or removes a list item, the comment
// of each position from that item to the end goes to the item that holds
// the position in the changed list. The
// [go.jacobcolvin.com/niceyaml/edit] package changes the text of a
// document in place, so each comment stays with its item.
//
// The call decodes the node into an any, as [Node.DecodeInto] does
// without a [DecodeOption], so a program that also decodes the node reads
// it twice. A decode that fails, such as one of a document that did not
// parse, returns its error and a nil map. For a node that does not hold
// a value, YAMLComments returns an empty map.
//
// Each key is a path from the node, so `$` names the value that a decode
// of the same Node returns. The map thus pairs with an encode of that
// value. For a Node from [Node.At] at `$.spec`, the comment of
// `$.spec.replicas` in the document has the key `$.replicas`. A Node at
// an alias keys its comments from the content of the anchor that the
// alias reads. For keys from the root of the document, call YAMLComments
// on the Node that [Node.Document] returns.
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
// The decoder keeps one comment of each kind, head, line, or foot, under
// a path. A merge key can read a mapping of a reference document whose
// paths continue the path of the node. That mapping can hold a comment at
// a path where the node holds a comment of the same kind. When the
// decoder reads the merge key first, the map holds the comment of the
// reference document in place of the comment of the node.
//
// Each key is in the path syntax of go-yaml, which prints a name with
// whitespace or `:` without quotes, as in `$.my key`. [paths.Parse]
// rejects such an unquoted name, so it does not read every key. To look
// up the key of a [paths.Path], convert the path with
// [paths.Path.YAMLPath] and use the String of the result, not the String
// of the [paths.Path].
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
	// the reference documents thus go first. They include those that the
	// anchors of the document before node reach, because the decoder reads
	// those anchors too.
	if refs := n.source.referenceAnchors(); refs != nil {
		names := tree.ReferenceNames(view)
		for _, anchor := range anchors {
			names = append(names, tree.ReferenceNames(anchor)...)
		}

		for _, anchor := range refs.Prime(names) {
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
				return view, n.rejection(err, view, firstRead)
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
			return view, n.rejection(err, anchor, firstRead)
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
// the source, before the path resolves. So does a decode under
// [UnknownFieldsWarn] whose context does not carry a [Warnings] collector,
// with an error that wraps [ErrNoWarnings]. Each mistake thus shows
// whether or not the document holds the value.
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

	node, err := n.At(path)
	if errors.Is(err, paths.ErrNotFound) {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	// The decode can return an error that matches paths.ErrNotFound too,
	// so only the error of At above counts as an absent value.
	err = node.decodeInto(enterCall(ctx), v, cfg, nil)
	if err != nil {
		return false, err
	}

	return true, nil
}

// decodeProblems returns err, the one rejection of a failed decode, in a
// summary with the other problems a [problemCollector] finds. The problems
// that the collector finds again for the rejection, one for each use of
// the value, stand in its place with the paths of those uses. It returns
// the rejection alone when the rejection lacks a position or ctx ends, and
// the one problem alone when the collector finds no other.
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

	if ctx.Err() != nil {
		return rejectionErr
	}

	// The collector finds the rejection again at its position, once for
	// each use of the value that fails the same way, with the path of that
	// use. Those problems stand for the rejection. A problem at that
	// position with another path is a use that the decoder did not reach,
	// or a use that reads the value as another Go type. It keeps the error
	// of its own leaf decode.
	// A rejection without a token of the document, such as a duration that
	// does not parse, names the path of its use instead, so the problem
	// with that path is the rejection itself. When the rejection names no
	// path, the collector cannot tell its use from the others, and the
	// rejection stands for every problem at its position.
	yamlErr, tokened := err.(yaml.Error) //nolint:errorlint // A wrapped error is the unmarshaler's own.
	tokened = tokened && n.holdsToken(yamlErr.GetToken())

	usePath, pathed := "", false
	if !tokened {
		usePath, pathed = n.rejectionPath(rejectionErr)
	}

	var again, problems []decodeProblem

	for key, problem := range c.found {
		switch {
		case problem.at != at:
			continue
		case tokened && problem.rejected != nil && sameReading(yamlErr, problem.rejected):
			again = append(again, problem)

		case (tokened || pathed) && problem.path != "" && problem.path != usePath:
			continue
		}

		delete(c.found, key)
	}

	// Each problem that stands for the rejection keeps the location of its
	// use. Its cause is the error that the decode returned, so [errors.As]
	// finds that error and not the error of the leaf decode. That error
	// names the struct field of the one use the decoder read. When several
	// uses stand for it, each takes a copy that names no struct field, so
	// no row names the field of another use.
	for i, problem := range again {
		rejected := yamlErr
		if len(again) > 1 {
			rejected = withoutStructField(yamlErr)
		}

		problem.err = Invalid(n.rejectionCause(rejected), problem.loc...)
		problem.rejected = rejected
		again[i] = problem
	}

	if len(again) == 0 {
		again = append(again, decodeProblem{
			err:          rejectionErr,
			at:           at,
			order:        -1,
			unknownField: n.rejectsUnknownField(err),
		})
	}

	problems = append(problems, again...)

	for _, problem := range c.found {
		problems = append(problems, problem)
	}

	return problemsError(problems)
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

// rejectionPath returns the `$` path that rejectionErr, a decoder
// rejection without a token of the document, reports, spelled as the paths
// of a [problemCollector] are. It reports false when the rejection names no
// one path.
func (n *Node) rejectionPath(rejectionErr error) (string, bool) {
	found := anchorOf(placeable(rejectionErr))
	_, own := found.err.(*Error) //nolint:errorlint // The anchor itself, found by the walk.
	if !own || !found.hasPath || found.ambiguous {
		return "", false
	}

	return n.base.Join(found.path).String(), true
}

// rejectsUnknownField reports whether err, the rejection of a decode,
// is the decoder's own rejection of an unknown field. The key of that field
// lies in the document, or in a reference document when [Node.rejection]
// placed it at an alias.
func (n *Node) rejectsUnknownField(err error) bool {
	if unknown, ok := err.(*yaml.UnknownFieldError); ok { //nolint:errorlint // A wrapped error is the unmarshaler's own.
		return n.holdsToken(unknown.Token)
	}

	return errors.Is(err, errPlaced) && errors.Is(err, ErrUnknownField)
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
	return Invalid(n.rejectionCause(err), n.rejectionLocation(err.GetToken())...)
}

// rejectionCause returns err, a rejection at a source token, as the cause
// of the [*Error] that [Node.tokenRejection] returns.
func (n *Node) rejectionCause(err yaml.Error) error {
	msg, label := n.doc.rejectionText(err)

	return markDecode(yamlMessage(err, msg), label)
}

// useRejection returns err, a rejection at a source token, as a problem
// of a value that the decode read at path, a `$` path through the aliases
// and `<<` merge keys that read it. The problem carries path when path
// binds at the node of the token, as a [Validator] error at path binds
// there. A value inside an anchor that an alias brings in thus reports the
// path through the alias, at the position of the value. The argument alias
// is the token that a path to the value points at when the document holds
// an alias for the value itself, or nil. A path that binds at that alias
// also names the use, and the problem carries it at the position of the
// node of the token. Where path binds elsewhere, as when a later entry of
// the same key takes it, the problem binds as [Node.tokenRejection] binds
// it.
func (n *Node) useRejection(err yaml.Error, path paths.Path, alias *token.Token) decodeProblem {
	tk := err.GetToken()
	owner := n.doc.pathIndex().OwnerToken(tk)
	problem := decodeProblem{rejected: err, at: position.NewFromToken(tk)}

	switch {
	case n.bindsAt(path, owner):
		problem.loc = []ErrorOption{AtPath(path)}
		problem.path = path.String()

	case alias != nil && owner != nil && n.bindsAt(path, alias):
		problem.loc = []ErrorOption{AtPath(path), AtToken(owner)}
		problem.path = path.String()

	default:
		problem.loc = n.rejectionLocation(tk)
	}

	problem.err = Invalid(n.rejectionCause(err), problem.loc...)

	return problem
}

// decodeProblem is one problem of a decode, before the Node binds it.
type decodeProblem struct {
	// The problem, located as [Node.decodeRejection] locates a rejection,
	// or at the path of the use that read the value.
	err error
	// The rejection of the decoder at a source token that err reports, or
	// nil for any other problem.
	rejected yaml.Error
	// The path of the use that err reports, or empty when err reports the
	// path that the token alone gives.
	path string
	// The options that locate err when rejected is set. A rejection that
	// the collector finds again takes them, so the problem reports the
	// error that the decode returned at the location of this use.
	loc []ErrorOption
	// The position that marks the problem among the others, which is the
	// position of the value the problem is about.
	at position.Position
	// The order in which the collector found the problem, which orders the
	// problems at one position.
	order int
	// Whether the problem is the rejection of an unknown field.
	unknownField bool
}

// sortProblems sorts problems in the order of the source. The problems at
// one position come in the order the collector found them.
func sortProblems(problems []decodeProblem) {
	slices.SortFunc(problems, func(a, b decodeProblem) int {
		return cmp.Or(a.at.Compare(b.at), cmp.Compare(a.order, b.order))
	})
}

// problemKey names a problem of a decode by its position and the path of
// the use it reports. A value that several uses read and reject thus
// gives one problem for each use.
//
//nolint:unused // The fields tell the keys of the map apart.
type problemKey struct {
	path string
	at   position.Position
}

// problemCollector finds the problems of a decode beside the one rejection
// the go-yaml decoder returned. It walks each use of a value that aliases
// or `<<` merge keys read, as [decodewalk.WalkUses] does, so it finds a
// problem once for each use. It decodes each leaf of the walk alone,
// never inside the mapping or sequence that holds it. A leaf thus costs
// the pass the same however deep it lies, and the uses of one node share
// its decodes. The pass confirms the rejection of a field or an element
// by decoding it again, as the one entry of a mapping or the one element
// of a sequence.
//
// Under [UnknownFieldsReject], the decoder of the collector panics on a
// struct that holds a map inline, as [panicsOnInlineMap] reports. Each
// decode of the pass that panics so runs again with a decoder that
// accepts unknown fields, so the pass still finds the problems of the
// values. The search for unknown fields keeps the panic, as
// [decodewalk.UnknownFields] describes.
//
// Create instances with [newProblemCollector].
type problemCollector struct {
	ctx     context.Context
	node    *Node
	decoder *yaml.Decoder
	// The decoder for the settings of the decode with searchesUnknown
	// flipped, as [problemCollector.flipped] creates it, or nil before
	// the first call.
	flip *yaml.Decoder
	// The node that [problemCollector.register] read, which the flipped
	// decoder reads too.
	registered ast.Node
	// The problems found, by the position that marks each and the path of
	// the use it reports.
	found map[problemKey]decodeProblem
	// The error of the decode of each leaf, by its type and node.
	leaves map[leafKey]error
	// The answer of [problemCollector.confirmed] for each leaf it read.
	confirms map[confirmKey]bool
	// What the walk knows of the decode, which decodes as
	// [problemCollector.decode] does.
	rules decodewalk.Rules
	// The settings of the decode.
	cfg decodeConfig
}

// newProblemCollector creates a new [*problemCollector] for a decode of
// n with cfg. The collector decodes with a copy of ctx that drops each
// warning, as [withoutWarnings] describes.
func newProblemCollector(ctx context.Context, n *Node, cfg decodeConfig) *problemCollector {
	ctx = withoutWarnings(ctx)
	dec := n.newDecoder(cfg)

	c := &problemCollector{
		ctx:      ctx,
		node:     n,
		decoder:  dec,
		found:    map[problemKey]decodeProblem{},
		leaves:   map[leafKey]error{},
		confirms: map[confirmKey]bool{},
		rules:    n.walkRules(ctx, dec, cfg.unmarshalers),
		cfg:      cfg,
	}

	c.rules.Decode = c.decode

	return c
}

// decode decodes node into v with the decoder of the collector. When that
// decoder rejects unknown fields and panics on a struct that holds a map
// inline, decode runs again with the decoder that
// [problemCollector.flipped] returns, which accepts them.
func (c *problemCollector) decode(node ast.Node, v any) error {
	err := decodeWithRecover(c.ctx, c.decoder, node, v)
	if c.cfg.unknownFields != UnknownFieldsReject || c.cfg.searchesUnknown || !panicsOnInlineMap(err) {
		return err
	}

	return decodeWithRecover(c.ctx, c.flipped(), node, v)
}

// flipped returns a decoder for the settings of the collector with
// searchesUnknown flipped. Under [UnknownFieldsReject], it accepts unknown
// fields when the decoder of the collector rejects them, and the other way
// around. It reads the node that [problemCollector.register] read, so it
// holds the same anchors.
func (c *problemCollector) flipped() *yaml.Decoder {
	if c.flip != nil {
		return c.flip
	}

	cfg := c.cfg
	cfg.searchesUnknown = !cfg.searchesUnknown
	c.flip = c.node.newDecoder(cfg)

	if c.registered != nil {
		registerWith(c.ctx, c.node, c.flip, c.registered)
	}

	return c.flip
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
		Unmarshalers:      u,
		Anchor:            n.referenceAnchor,
		AnchorKeys:        n.referenceAnchorKeys,
		PastAliasLimit:    n.doc.pastAliasLimit,
		PanicsOnInlineMap: panicsOnInlineMap,
		Keys:              &n.source.mergeKeys,
	}
}

// referenceAnchor returns the value of the anchor of a reference document
// that alias reads, as the decoder reads it. It reports false when the
// reference documents do not define an anchor of the name of alias.
func (n *Node) referenceAnchor(alias *ast.AliasNode) (ast.Node, bool) {
	name, ok := astnode.Name(alias.Value)
	if !ok {
		return nil, false
	}

	anchor, ok := n.source.referenceAnchors().View(name)
	if !ok {
		return nil, false
	}

	return anchor.Value, true
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

// collect finds the problems of a decode of node into a value of type t
// beside the rejection of the decoder. Those are the leaves that the
// decoder rejects and, under [UnknownFieldsReject], the unknown fields
// that [problemCollector.collectUnknown] finds.
func (c *problemCollector) collect(t reflect.Type, node ast.Node) {
	c.register(node)

	from := datapath.ParentAt(c.rules.Resolver, c.node.base)
	decodewalk.WalkUses(c.ctx, c.rules, t, node, from, c.visit)

	if c.cfg.unknownFields == UnknownFieldsReject {
		c.collectUnknown(t, node)
	}
}

// collectUnknown finds the unknown fields of a decode of node into a value
// of type t, other than the known keys that the decode cuts, as
// [Node.cutKeys] finds them. [problemCollector.register] must have read
// node. The search confirms each field with a decoder that rejects
// unknown fields, which is the decoder of the collector unless the
// settings of the collector set searchesUnknown.
func (c *problemCollector) collectUnknown(t reflect.Type, node ast.Node) {
	cut := c.node.cutKeys(c.ctx, c.rules, c.cfg, t, node)

	// The search confirms each field with a decoder that rejects unknown
	// fields and panics on an inline map, which the decode of the
	// collector does not.
	dec := c.decoder
	if c.cfg.searchesUnknown {
		dec = c.flipped()
	}

	search := c.node.walkRules(c.ctx, dec, c.cfg.unmarshalers)

	for _, field := range decodewalk.UnknownFields(c.ctx, search, t, node) {
		// The search reads node, which holds the known keys that the view
		// of the decode leaves out.
		if cut[position.NewFromToken(field.Err.Token)] {
			continue
		}

		problem := c.node.useRejection(field.Err, c.node.base.Join(field.Path), nil)
		problem.unknownField = true

		c.add(problem)
	}
}

// register has the decoder of the pass read node into an interface once,
// after [Node.prime], so it holds each anchor as the decode held it. The
// go-yaml decoder assigns an alias the value it holds for the anchor.
func (c *problemCollector) register(node ast.Node) {
	c.registered = node

	registerWith(c.ctx, c.node, c.decoder, node)
}

// registerWith has dec read node into an interface once, after
// [Node.prime], as [problemCollector.register] describes for n.
func registerWith(ctx context.Context, n *Node, dec *yaml.Decoder, node ast.Node) {
	view, err := n.prime(ctx, dec, node)
	if err != nil {
		return
	}

	var sink any

	_ = decodeWithRecover(ctx, dec, view, &sink) //nolint:errcheck // The decode returned this rejection.
}

// add keeps problem, unless the collector holds one at the same position
// for the same path already.
func (c *problemCollector) add(problem decodeProblem) {
	key := problemKey{at: problem.at, path: problem.path}
	if _, held := c.found[key]; !held {
		problem.order = len(c.found)
		c.found[key] = problem
	}
}

// firstRead returns the alias among aliases that led the decoder to err,
// the rejection of a decode of node into a value of type t. Each alias
// reaches the token of err. The search walks the values of the decode in
// the order the decoder reads them, as [decodewalk.Walk] does. It decodes
// again each value that reaches one of the aliases, alone, and keeps the
// deepest that fails as err does. The decoder picks among the unknown
// fields of one mapping in no fixed order, so a value fails as an unknown
// field does when it fails with any unknown field. The decoder stopped
// inside that value, so it read the token through an alias that the value
// reaches:
//
//   - The alias of a value at an alias of a reference document, or of a
//     field whose entry a `<<` merge key brings in from such a document.
//   - For a struct whose fields do not lead to the alias, the alias of the
//     merge key that brings in the key of err, as [decodewalk.ReferenceKey]
//     finds it. Without such a key, the search picks the first alias in
//     document order that a `<<` merge key of the mapping reaches.
//   - For a map whose `<<` merge key fails as err does, the alias that the
//     search finds when it reads the source of that merge key as a map of
//     the same type. [problemCollector.failedMerge] finds that source. The
//     decoder of a map returns the error of a merge key before the error
//     of a member. So the search tries the merge keys of a map first. A
//     source in a reference document is a value at an alias of such a
//     document.
//   - The first alias in document order that any other value reaches.
//
// It reports false when the decode of no value fails as err does, or the
// context ends.
func (c *problemCollector) firstRead(
	t reflect.Type, node ast.Node, err error, aliases []decodetree.ReferenceAlias,
) (decodetree.ReferenceAlias, bool) {
	candidates := map[*token.Token]bool{}

	for _, alias := range aliases {
		candidates[alias.Token] = true
	}

	from := datapath.ParentAt(c.rules.Resolver, c.node.base)

	reached := c.search(t, node, from, err, candidates, map[ast.Node]bool{})
	if len(reached) == 0 {
		return decodetree.ReferenceAlias{}, false
	}

	return reached[0], true
}

// search returns the aliases among candidates that one value of a decode
// of node into a value of type t reaches, in document order. That value is
// the one that [problemCollector.firstRead] picks for err. The argument
// from names what holds node in the document. The set sources holds each
// merge source that the search has read, so a source that merges itself
// ends the search. It returns nil when the decode of no value fails as err
// does, or the context ends.
func (c *problemCollector) search(
	t reflect.Type,
	node ast.Node,
	from astnode.Parent,
	err error,
	candidates map[*token.Token]bool,
	sources map[ast.Node]bool,
) []decodetree.ReferenceAlias {
	tree := c.node.doc.tree()

	// The aliases among candidates that scopes reach, in document order.
	reach := func(scopes ...ast.Node) []decodetree.ReferenceAlias {
		var found []decodetree.ReferenceAlias

		for _, scope := range scopes {
			for _, ref := range tree.ReferenceAliases(c.rules.View(scope)) {
				if candidates[ref.Token] {
					found = append(found, ref)
				}
			}
		}

		return found
	}

	var (
		deepest *decodewalk.Value
		reached []decodetree.ReferenceAlias
	)

	decodewalk.Walk(c.ctx, c.rules, t, node, from, func(v *decodewalk.Value) decodewalk.Action {
		if deepest != nil && v.Depth <= deepest.Depth {
			return decodewalk.Stop
		}

		found := reach(v.Node)
		if v.Kind == decodewalk.Key || len(found) == 0 {
			return decodewalk.Skip
		}

		if !sameRead(err, c.rules.Decode(c.read(v), reflect.New(v.Type).Interface())) {
			return decodewalk.Skip
		}

		deepest, reached = v, found

		switch v.Reads {
		case decodewalk.Members:
			// The decoder of a map returns the error of a `<<` merge key at
			// once, and the error of any other entry after it reads the rest.
			mapping, _ := datapath.Deref(c.rules.Resolver, v.Node).(*ast.MappingNode) //nolint:errcheck // A map reads a mapping.

			source, failed := c.failedMerge(mapping, v.Type, err)
			if !failed {
				return decodewalk.Descend
			}

			merged := reach(source)
			if len(merged) == 0 {
				return decodewalk.Descend
			}

			// The search reads the source as a map of the same type. It stops
			// at once at a source in a reference document, a value at an
			// alias of such a document.
			reached = merged

			if !sources[source] {
				sources[source] = true

				if inner := c.search(v.Type, source, astnode.Parent{}, err, candidates, sources); len(inner) > 0 {
					reached = inner
				}
			}

			return decodewalk.Stop

		case decodewalk.Fields, decodewalk.Elements:
			return decodewalk.Descend

		default:
			return decodewalk.Stop
		}
	})

	if deepest == nil || c.ctx.Err() != nil {
		return nil
	}

	if deepest.Reads == decodewalk.Fields {
		mapping, _ := datapath.Deref(c.rules.Resolver, deepest.Node).(*ast.MappingNode) //nolint:errcheck // A struct reads a mapping.

		if merged := reach(c.mergeAliases(mapping, err)...); len(merged) > 0 {
			return merged
		}
	}

	return reached
}

// failedMerge returns the source of the `<<` merge key of mapping whose
// merge fails as err does, for a map of type t that reads mapping. The
// decoder reads the merge keys of mapping in document order and returns
// the error of the first that fails. So failedMerge returns the source of
// that merge key, as [problemCollector.failedSource] finds it. It reports
// false when no merge key fails as err does.
func (c *problemCollector) failedMerge(mapping *ast.MappingNode, t reflect.Type, err error) (ast.Node, bool) {
	if mapping == nil {
		return nil, false
	}

	for _, entry := range mapping.Values {
		if entry == nil || !astnode.IsMergeKey(entry.Key) {
			continue
		}

		if c.mergeFails(c.rules.View(entry), t, err) {
			return c.failedSource(entry.Value, t, err, map[ast.Node]bool{}), true
		}
	}

	return nil, false
}

// mergeFails reports whether the merge of entry, an entry with a `<<`
// merge key as the decoder reads it, fails as err does for a map of type
// t. A mapping that holds the merge key alone merges what the merge key
// merges into the map.
func (c *problemCollector) mergeFails(entry ast.Node, t reflect.Type, err error) bool {
	view, ok := entry.(*ast.MappingValueNode)
	if !ok {
		return false
	}

	merge := ast.Mapping(view.Start, false, view)

	return sameRead(err, c.rules.Decode(merge, reflect.New(t).Interface()))
}

// failedSource returns the source that leads the decoder to err within
// value, the value of a `<<` merge key whose merge fails as err does for a
// map of type t. That source is value, unless value is a sequence. The
// decoder reads the entries of the mappings of a sequence there as the
// entries of one mapping. It returns the error of a merge key among them
// at once, and otherwise the error of the first other entry that fails.
// For a merge key of a mapping that the document writes, failedSource
// goes on into the value of that merge key. For a merge key of a mapping
// in a reference document, and for any other entry, it returns the
// element that holds the entry. Where no element fails so, it returns
// value. The set seen holds the sequences that failedSource has read.
func (c *problemCollector) failedSource(value ast.Node, t reflect.Type, err error, seen map[ast.Node]bool) ast.Node {
	seq, ok := datapath.Deref(c.rules.Resolver, value).(*ast.SequenceNode)
	if !ok || seen[seq] {
		return value
	}

	seen[seq] = true

	for _, element := range seq.Values {
		entries, reference := c.sourceEntries(element)

		for _, entry := range entries {
			if entry == nil || !astnode.IsMergeKey(entry.Key) {
				continue
			}

			view := ast.Node(entry)
			if !reference {
				view = c.rules.View(entry)
			}

			if !c.mergeFails(view, t, err) {
				continue
			}

			if reference {
				return element
			}

			return c.failedSource(entry.Value, t, err, seen)
		}
	}

	for _, element := range seq.Values {
		if sameRead(err, c.rules.Decode(c.rules.View(element), reflect.New(t).Interface())) {
			return element
		}
	}

	return value
}

// sourceEntries returns the entries of the mapping that the decoder reads
// for element, an element of a sequence that a `<<` merge key merges. It
// reports whether that mapping lies in a reference document, where the
// entries are nodes as the decoder reads them. It returns nil when
// element does not read a mapping.
func (c *problemCollector) sourceEntries(element ast.Node) ([]*ast.MappingValueNode, bool) {
	if alias, ok := astnode.Content(element).(*ast.AliasNode); ok {
		_, err := c.rules.Resolver.Deref(alias)
		if errors.Is(err, paths.ErrReferenceAlias) {
			anchor, _ := c.rules.Anchor(alias)

			mapping, _ := astnode.Content(anchor).(*ast.MappingNode) //nolint:errcheck // Any other node has no entries.
			if mapping == nil {
				return nil, true
			}

			return mapping.Values, true
		}
	}

	mapping, _ := datapath.Deref(c.rules.Resolver, element).(*ast.MappingNode) //nolint:errcheck // Any other node has no entries.
	if mapping == nil {
		return nil, false
	}

	return mapping.Values, false
}

// mergeAliases returns the nodes through which the `<<` merge keys of
// mapping might have led the decoder to err, as [problemCollector.firstRead]
// picks among them. A key of err that a merge key brings in from a reference
// document names the alias that reads it, as [decodewalk.ReferenceKey] finds
// it. Without such a key, the nodes are the values of the merge keys.
func (c *problemCollector) mergeAliases(mapping *ast.MappingNode, err error) []ast.Node {
	yamlErr, ok := err.(yaml.Error) //nolint:errorlint // The decoder returns it unwrapped.
	if ok {
		alias, found := decodewalk.ReferenceKey(c.rules, mapping, yamlErr.GetToken())
		if found {
			return []ast.Node{alias}
		}
	}

	var merges []ast.Node

	for _, entry := range mapping.Values {
		if entry != nil && astnode.IsMergeKey(entry.Key) {
			merges = append(merges, entry.Value)
		}
	}

	return merges
}

// read returns the node that the decoder reads v from, as it reads it.
// For a field whose entry a `<<` merge key brings in from a reference
// document, that node is the value of that entry.
func (c *problemCollector) read(v *decodewalk.Value) ast.Node {
	if v.Merged != nil {
		return v.Merged
	}

	return c.rules.View(v.Node)
}

// visit tells the walk whether to go below v and reads each leaf. Among
// values that decode themselves, it reads only durations, times, and text
// types, because any other unmarshaler might answer a second call
// differently.
func (c *problemCollector) visit(v *decodewalk.Value) decodewalk.Action {
	u := c.cfg.unmarshalers

	switch {
	case v.Kind == decodewalk.Key, !v.Named, v.Reads == decodewalk.Reference,
		v.Kind == decodewalk.Inline && u.ReportsOwnError(v.Type):
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

// leafKey names the decode of a leaf, by the type of the leaf and the
// node the decoder reads it from.
//
//nolint:unused // The fields tell the keys of the map apart.
type leafKey struct {
	typ  reflect.Type
	node ast.Node
}

// confirmKey names what [problemCollector.confirmed] reads of a leaf.
//
//nolint:unused // The fields tell the keys of the map apart.
type confirmKey struct {
	typ   reflect.Type
	owner reflect.Type
	held  ast.Node
	entry *ast.MappingValueNode
	kind  decodewalk.Kind
}

// leaf decodes the node of v alone and adds the rejection as a problem.
// A rejection at a token goes at the path of v when that path binds at the
// token, as [Node.useRejection] describes. A rejection without a token
// goes at the path of v, and only when that path selects v rather than a
// later entry with the same key.
func (c *problemCollector) leaf(v *decodewalk.Value) {
	err := c.decodeLeaf(v)
	if err == nil {
		return
	}

	if rejected, ok := builtRejection(err); ok {
		tk := rejected.GetToken()
		if c.node.holdsToken(tk) && c.confirmed(err, v) {
			c.add(c.node.useRejection(rejected, c.node.base.Join(v.Path()), heldAlias(v)))
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

	c.add(decodeProblem{err: Rebase(cause, path), path: c.node.base.Join(path).String(), at: loc.pos})
}

// heldAlias returns the token that a path to v points at when the
// document holds an alias for v, or nil when it holds the value itself.
func heldAlias(v *decodewalk.Value) *token.Token {
	if _, ok := astnode.Content(v.Held).(*ast.AliasNode); !ok {
		return nil
	}

	return astnode.PathToken(v.Held, v.From)
}

// decodeLeaf decodes the node of v, a leaf, alone into a new value of its
// type, and returns the error. The uses of one node share the decode.
func (c *problemCollector) decodeLeaf(v *decodewalk.Value) error {
	view := c.rules.View(v.Node)

	key := leafKey{typ: v.Type, node: view}
	if err, held := c.leaves[key]; held {
		return err
	}

	err := c.rules.Decode(view, reflect.New(v.Type).Interface())
	c.leaves[key] = err

	return err //nolint:wrapcheck // The caller reads the error of the decoder as it is.
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
// The uses of one node that the same value holds share the answer, since
// err is the error of [problemCollector.decodeLeaf] for v.
func (c *problemCollector) confirmed(err error, v *decodewalk.Value) bool {
	key := confirmKey{typ: v.Type, owner: v.Struct, held: v.Held, entry: v.Entry, kind: v.Kind}
	if ok, held := c.confirms[key]; held {
		return ok
	}

	ok := c.confirm(err, v)
	c.confirms[key] = ok

	return ok
}

// confirm reports what [problemCollector.confirmed] reports for v.
func (c *problemCollector) confirm(err error, v *decodewalk.Value) bool {
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

// sameReading reports whether again, the rejection of a leaf decode,
// rejects the token of err, the rejection of the decode, the way err
// does. The two match when [sameRejection] matches them and their
// messages agree, so a use that reads the token as another Go type does
// not match. The messages leave out the struct field of a
// [*yaml.TypeError], which names the field the decode read and differs
// between the uses of one value.
func sameReading(err, again yaml.Error) bool {
	return sameRejection(err, again) && readingMessage(err) == readingMessage(again)
}

// readingMessage returns the message of err without the struct field
// that a [*yaml.TypeError] names.
func readingMessage(err yaml.Error) string {
	return withoutStructField(err).GetMessage()
}

// withoutStructField returns a copy of err without the struct field
// when err is a [*yaml.TypeError]. It returns any other error as it is,
// because no other rejection names a struct field.
func withoutStructField(err yaml.Error) yaml.Error {
	typed, ok := err.(*yaml.TypeError) //nolint:errorlint // The decoder returns it unwrapped.
	if !ok {
		return err
	}

	bare := *typed
	bare.StructFieldName = nil

	return &bare
}

// sameRead reports whether again, the error of a second decode of a
// value, shows that the decoder stopped inside that value with err. The
// two match as [sameRejection] matches them. When err is an unknown field,
// again matches it when it is any unknown field, because the decoder
// picks among the unknown fields of one mapping in no fixed order.
func sameRead(err, again error) bool {
	if _, unknown := err.(*yaml.UnknownFieldError); unknown { //nolint:errorlint // The decoder returns it unwrapped.
		_, alsoUnknown := again.(*yaml.UnknownFieldError) //nolint:errorlint // The decoder returns it unwrapped.

		return alsoUnknown
	}

	return sameRejection(err, again)
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

	// The locator decodes values again, as [withoutWarnings] describes.
	ctx = withoutWarnings(ctx)
	dec := n.newDecoder(cfg)
	l := &errorLocator{
		ctx:      ctx,
		node:     n,
		decoder:  dec,
		msg:      err.Error(),
		anchor:   anchorOf(err),
		places:   placesOf(err),
		resolver: n.doc.pathResolver(),
		open:     map[string]bool{},

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

	cause := l.refusal

	switch {
	case l.found != nil:
		path, cause = l.result()

	case !cfg.unmarshalers.ReportsOwnError(t) || !astnode.IsScalar(n.doc.content(node)):
		return err
	}

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
	// The locator that found the value inside the anchor of a reference
	// document that found stands for, or nil.
	inner *errorLocator
	// The resolver of the nodes the walk reads.
	resolver *paths.Resolver
	// The names of the anchors of reference documents that the walks of
	// the locator and of the locators above it read, which every locator
	// of one search shares.
	open map[string]bool
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
	// Whether the walk reads the value of an anchor of a reference
	// document, whose anchors the decoder has read already.
	reference bool
}

// result returns the path of the value the walk found, under the value it
// starts at, and the rejection of that value, as [errorLocator.refusal]
// holds it. For a value at an alias of a reference document, the path
// goes on to the value inside the anchor that [errorLocator.inside]
// finds.
func (l *errorLocator) result() (paths.Path, error) {
	path := l.found.Path()
	if l.inner == nil {
		return path, l.refusal
	}

	below, refusal := l.inner.result()

	return path.Join(below), refusal
}

// visit tells the walk where to go after v. A value that reproduces the
// error becomes the find, and the walk goes below it for a deeper one. The
// root decoded in the decode itself, so only a refusal finds it, or an
// alias of a reference document at the root. A value at such an alias that
// reproduces the error becomes the find whatever its type. The locator then
// walks the anchor of the alias for the value inside it that reported the
// error, as [errorLocator.inside] describes.
func (l *errorLocator) visit(v *decodewalk.Value) decodewalk.Action {
	u := l.unmarshalers

	switch {
	case l.found != nil && v.Depth <= l.found.Depth:
		return decodewalk.Stop

	// The walk does not read below an alias of a reference document, so
	// the locator walks the anchor of a value there that reproduces the
	// error. The root decoded in the decode itself.
	case v.Reads == decodewalk.Reference:
		if v.Kind == decodewalk.Root || v.Named && (v.Kind != decodewalk.Member || v.Selected()) && l.reproducesAt(v) {
			l.found = v
			l.inner = l.inside(v)
		}

		return decodewalk.Skip

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

// inside returns a locator that found the value that reported the error
// inside the anchor of a reference document that v stands for. The value v
// reads that anchor through its alias and reproduces the error, as
// [errorLocator.visit] finds it. The locator walks the value of the anchor
// as the type of v, as the walk of the document walks a value there. It
// reads the aliases inside as the decoder reads them. For a field whose
// entry a `<<` merge key brings in, it walks the value of that entry. It
// returns nil when the anchor is one that a walk above reads already, or
// when the walk does not find a value below v.
func (l *errorLocator) inside(v *decodewalk.Value) *errorLocator {
	alias, ok := astnode.Content(v.Node).(*ast.AliasNode)
	if !ok {
		return nil
	}

	name, ok := astnode.Name(alias.Value)
	if !ok || l.open[name] {
		return nil
	}

	value := v.Merged
	if value == nil {
		value, ok = l.node.referenceAnchor(alias)
		if !ok {
			return nil
		}
	}

	l.open[name] = true
	defer delete(l.open, name)

	src := l.node.source
	inner := &errorLocator{
		ctx:     l.ctx,
		node:    l.node,
		decoder: l.decoder,
		resolver: paths.NewResolver(&ast.DocumentNode{BaseNode: &ast.BaseNode{}, Body: value},
			paths.WithReferenceAnchors(src.referenceValue)),
		open:         l.open,
		msg:          l.msg,
		places:       l.places,
		unmarshalers: l.unmarshalers,
		anchor:       l.anchor,
		reference:    true,
	}

	rules := decodewalk.Rules{
		Resolver: inner.resolver,
		View:     func(node ast.Node) ast.Node { return node },
		Decode: func(node ast.Node, v any) error {
			return decodeWithRecover(l.ctx, l.decoder, node, v)
		},
		Unmarshalers: l.unmarshalers,
		Anchor:       l.node.referenceAnchor,
		AnchorKeys:   l.node.referenceAnchorKeys,
	}

	decodewalk.Walk(l.ctx, rules, v.Type, value, astnode.Parent{}, inner.visit)

	if inner.found == nil {
		return nil
	}

	return inner
}

// refuses reports whether the decoder refuses the node of v for its type
// without calling an unmarshaler, and records the [textRejection] for it.
func (l *errorLocator) refuses(v *decodewalk.Value) bool {
	content := datapath.Deref(l.resolver, v.Node)
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

	view, ok := l.view(node)
	if !ok {
		return false
	}

	err := decodeWithRecover(l.ctx, l.decoder, view, v)

	return err != nil && l.same(err)
}

// view returns node as the decoder reads it, after the decoder has read
// the anchors that node needs, as [Node.prime] describes. A node of a
// reference document is a node as the decoder reads it already, and the
// decoder has read its anchors. It reports false when an anchor fails.
func (l *errorLocator) view(node ast.Node) (ast.Node, bool) {
	if l.reference {
		return node, true
	}

	view, err := l.node.prime(l.ctx, l.decoder, node)

	return view, err == nil
}

// reproducesAt reports whether a decode of v, a value below the root at
// an alias of a reference document, fails with the error the locator
// looks for. The decode reads the alias in place, as the one element of a
// sequence of values of the type of v. At the root of a decode, the
// go-yaml decoder hands an UnmarshalText method empty text for an alias
// whose anchor another parse holds, so the alias alone would not fail.
func (l *errorLocator) reproducesAt(v *decodewalk.Value) bool {
	if l.ctx.Err() != nil {
		return false
	}

	view, ok := l.view(v.Node)
	if !ok {
		return false
	}

	// A field whose entry a merge key brings in reads the value of that
	// entry, which the alias stands for.
	if v.Merged != nil {
		view = v.Merged
	}

	elements := ast.Sequence(view.GetToken(), false)
	elements.Values = append(elements.Values, view)

	err := decodeWithRecover(l.ctx, l.decoder, elements, reflect.New(reflect.SliceOf(v.Type)).Interface())

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
