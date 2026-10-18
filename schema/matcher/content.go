package matcher

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"slices"
	"strconv"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/hook"
	"go.jacobcolvin.com/niceyaml/internal/unmarshaler"
	"go.jacobcolvin.com/niceyaml/paths"
)

// Scalar is the set of types that the want of [Content] can have. It holds
// the string, bool, integer, and float types, and every type defined on
// one of them, such as [time.Duration].
type Scalar interface {
	~string | ~bool |
		~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~uintptr |
		~float32 | ~float64
}

// contentMatcher matches documents by a single YAML scalar.
type contentMatcher[T Scalar] struct {
	want T
	path paths.Path
}

// Content creates a new [Matcher] that matches documents whose scalar at
// path holds want. The type of want decides how Match reads the scalar:
//
//	// Matches kind: Deployment.
//	matcher.Content(paths.Doc().Child("kind"), "Deployment")
//
//	// Matches version: 1.0 and version: 1.
//	matcher.Content(paths.Doc().Child("version"), 1.0)
//
// A string matches the text of the scalar, quoted or plain, as [Text]
// reads it. A plain number or bool keeps its spelling, so "1.10" matches
// version: 1.10 and "1.1" does not.
//
// Numbers and bools match by value. A number matches a scalar that YAML
// reads as a number, and a bool matches one that YAML reads as a bool. So
// 16 matches version: 0x10 and 1.0 matches version: 1, but 2 does not
// match version: "2", which is a string. An integer does not match a
// value with a fraction, and a NaN matches .nan.
//
// A type defined on a string, a bool, or a number reads as that
// underlying type does. A type that decodes itself through an
// UnmarshalYAML or UnmarshalText method matches the value that its own
// decode gives. So does a [time.Duration], which reads from text such as
// 5s but not from a number. When the type of want has a Validate method,
// Match validates the value it reads, as a decode into that type does. It
// returns the error of a value that fails.
//
// Nulls, mappings, and sequences do not match any want. Neither does a
// document without the path, or a scalar that does not read as the type
// of want. To match a null, use a [Func], as its example does. To test
// the text of a scalar rather than compare it, use [Text].
//
// The path resolves as [niceyaml.Node.At] resolves it. A `$` path starts
// from the root of the document, and an `@` path starts from the Node
// that the matcher gets. Match returns the error of a path that cannot
// resolve, so a registry stops at the document rather than routing it
// elsewhere. Such a path holds a wildcard selector, or an alias that does
// not name an earlier anchor in the document. Match also returns the error
// of a context that ended. It returns the error of a panic in an
// UnmarshalYAML or UnmarshalText method of the type of want too, and that
// error holds a [*niceyaml.PanicError].
//
// An alias at the end of the path that reads a reference document of
// [niceyaml.WithReferences] resolves. The path selects that alias, and
// Match reads the scalar of its anchor through the decode, with the
// spelling and the quotes that the reference document gives it. A path
// through such an alias returns an error that wraps
// [paths.ErrReferenceAlias].
//
// Match also refuses a document whose aliases make the read cost far more
// than the document holds, unless [niceyaml.WithAliasLimit] disabled the
// limit for its source. It refuses such a document before it decodes
// anything, as a decode does. The error marks the document at fault, as
// [niceyaml.IsInvalid] describes, and it matches
// [go.jacobcolvin.com/niceyaml/schema.ErrExcessiveAliasing].
//
// For several conditions, use [All] (AND) or [Any] (OR):
//
//	// Matches kind: Deployment AND apiVersion: apps/v1.
//	matcher.All(
//	    matcher.Content(paths.Doc().Child("kind"), "Deployment"),
//	    matcher.Content(paths.Doc().Child("apiVersion"), "apps/v1"),
//	)
//
// The Matcher describes itself as `$.kind is "Deployment"`, through
// [Describer]. That description is the reason that
// [go.jacobcolvin.com/niceyaml/schema.When] gives when it declines. The
// description gives want as a value of its basic type, so a String
// method on the type of want does not change it. A string reads in
// quotes, and a number or a bool reads as Go formats it, such as 3 or
// true. When the type of want decodes itself, as a [time.Duration] does,
// want reads as Go formats it instead, such as 5s. Such a want of a
// string kind reads in quotes.
func Content[T Scalar](path paths.Path, want T) Matcher {
	return &contentMatcher[T]{path: path, want: want}
}

// Describe implements [Describer].
func (m *contentMatcher[T]) Describe() string {
	return m.path.String() + " is " + describeScalar(m.want)
}

// describeScalar returns want as a description reads it. A string reads
// in quotes. A type that [isPlain] reports as plain reads as the basic
// type of its kind, so a defined type with a String method reads as the
// value that Match compares. Any other type, such as [time.Duration],
// reads as Go formats it.
func describeScalar[T Scalar](want T) string {
	v := reflect.ValueOf(want)

	if isPlain(v.Type()) {
		v = v.Convert(basicTypes[v.Kind()])
	}

	if v.Kind() == reflect.String {
		return fmt.Sprintf("%q", v.Interface())
	}

	return fmt.Sprint(v.Interface())
}

// Match implements [Matcher].
func (m *contentMatcher[T]) Match(ctx context.Context, doc *niceyaml.Node) (bool, error) {
	t := reflect.TypeFor[T]()

	node, raw, err := readScalar(ctx, doc, m.path)
	if err != nil || raw == nil {
		return false, err
	}

	// The decoder reads some plain floats, such as 1e3, as strings and
	// hands them to time.ParseDuration for a time.Duration, which returns
	// its own error. A number never decodes into a time.Duration, so such
	// a float does not match, as 1.5e3 does not.
	if t == unmarshaler.DurationType && isFloatScalar(node, raw) {
		return false, nil
	}

	// A decode that fails means the value does not read as T, which is a
	// no rather than a failure. That holds for an error the value's own
	// UnmarshalYAML returns too.
	got, err := decodeScalar[T](ctx, node)
	if declines(err) {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	return matchScalar(node, raw, reflect.ValueOf(got), reflect.ValueOf(m.want)), nil
}

// readScalar returns the node at path below doc and the value of that
// node as the YAML types name it. It returns a nil value and a nil error
// when doc does not hold a node at path, when the node holds a null, or
// when the decoder rejects the node. The matcher reports false in those
// cases. Each decode applies the alias limit of the document before the
// decoder reads the node. An alias error does not match
// [niceyaml.ErrDecode], so readScalar returns it.
func readScalar(ctx context.Context, doc *niceyaml.Node, path paths.Path) (*niceyaml.Node, any, error) {
	err := ctx.Err()
	if err != nil {
		//nolint:wrapcheck // The error of the context is the reason the matcher cannot decide.
		return nil, nil, err
	}

	node, err := doc.At(path)
	if errors.Is(err, paths.ErrNotFound) {
		return nil, nil, nil
	}

	if err != nil {
		//nolint:wrapcheck // The Node binds the error already.
		return nil, nil, err
	}

	// Read the value as the YAML types name it first, because a decode into
	// the type of the matcher loses what tells a null from an empty string or
	// a false, and a fraction from the integer it truncates to. A decode into
	// any yields only the YAML built-in types, none of which validates
	// itself, so the self-validation walk would find nothing.
	raw, err := node.Decode[any](ctx, niceyaml.WithSelfValidation(false))
	if declines(err) {
		return nil, nil, nil
	}

	if err != nil {
		return nil, nil, err
	}

	return node, raw, nil
}

// declines reports whether err, the error of a decode that a matcher ran,
// says that the value does not read as the type that the matcher asked
// for. A matcher reads such an error as a no rather than a failure. It
// reports true for errors that match [niceyaml.ErrDecode] and do not hold
// a [*niceyaml.PanicError]. A panic marks a bug in code rather than a
// fault in the document, so a matcher returns its error.
func declines(err error) bool {
	var p *niceyaml.PanicError

	return !errors.As(err, &p) && errors.Is(err, niceyaml.ErrDecode)
}

// decodeScalar returns the value that node decodes to as a T, validated
// as a decode of a T validates it. When [isPlain] reports true for T,
// decodeScalar decodes into the basic type of the kind of T, which the
// decoder reads the same way. The go-yaml decoder cannot set a type
// defined on a float from a float that it reads as a string, such as
// 1e3. It can set the basic type from such a float.
func decodeScalar[T Scalar](ctx context.Context, node *niceyaml.Node) (T, error) {
	var got T

	t := reflect.TypeFor[T]()

	target := t
	if isPlain(t) {
		target = basicTypes[t.Kind()]
	}

	decoded := reflect.New(target)

	// The basic type has no Validate method, so T validates below.
	err := node.DecodeInto(ctx, decoded.Interface(), niceyaml.WithSelfValidation(false))
	if err != nil {
		//nolint:wrapcheck // The Node binds the error already.
		return got, err
	}

	reflect.ValueOf(&got).Elem().Set(decoded.Elem().Convert(t))

	err = node.SelfValidate(ctx, &got)

	//nolint:wrapcheck // The Node binds the error already.
	return got, err
}

// matchScalar reports whether got, the value that node decoded to,
// matches want. The raw argument holds the value of node as the YAML
// types name it. Both got and want hold the type of the want that
// [Content] took.
func matchScalar(node *niceyaml.Node, raw any, got, want reflect.Value) bool {
	plain := isPlain(got.Type())

	switch kind := got.Kind(); {
	case kind == reflect.String:
		text := got.String()

		// A string type that decodes itself keeps the value its own
		// decode gave.
		if plain {
			text = writtenText(node, text)
		}

		return text == want.String()

	case kind == reflect.Bool:
		return got.Bool() == want.Bool()

	// The decoder converts a string that Go parses as a number into a
	// number type, so "2", inf, and 0x1p-2 would match numbers. A scalar
	// that YAML reads as a string does not match a number want.
	case plain && isNonNumberString(node, raw):
		return false

	case isFloat(kind):
		if kind == reflect.Float32 && !floatHoldsFloat32(raw, got) {
			return false
		}

		return floatEqual(got.Float(), want.Float())

	default:
		return floatHoldsInteger(raw, got) && got.Equal(want)
	}
}

var (
	// The basicTypes map holds the basic type of each kind of [Scalar].
	basicTypes = map[reflect.Kind]reflect.Type{
		reflect.String:  reflect.TypeFor[string](),
		reflect.Bool:    reflect.TypeFor[bool](),
		reflect.Int:     reflect.TypeFor[int](),
		reflect.Int8:    reflect.TypeFor[int8](),
		reflect.Int16:   reflect.TypeFor[int16](),
		reflect.Int32:   reflect.TypeFor[int32](),
		reflect.Int64:   reflect.TypeFor[int64](),
		reflect.Uint:    reflect.TypeFor[uint](),
		reflect.Uint8:   reflect.TypeFor[uint8](),
		reflect.Uint16:  reflect.TypeFor[uint16](),
		reflect.Uint32:  reflect.TypeFor[uint32](),
		reflect.Uint64:  reflect.TypeFor[uint64](),
		reflect.Uintptr: reflect.TypeFor[uintptr](),
		reflect.Float32: reflect.TypeFor[float32](),
		reflect.Float64: reflect.TypeFor[float64](),
	}

	// The floatSyntax pattern matches the decimal and exponent spellings of
	// a float in the YAML 1.2 core schema. It leaves out the infinity and
	// NaN spellings. The decoder reads most of those spellings as floats,
	// and it cannot convert the rest, such as +.inf, into a number type.
	floatSyntax = regexp.MustCompile(`^[-+]?(\.\d+|\d+(\.\d*)?)([eE][-+]?\d+)?$`)
)

// isFloatScalar reports whether raw, the value of node as the YAML types
// name it, holds a float or a string in the YAML float syntax. Only the
// syntax decides, so 1e999, which overflows a float64, counts. A scalar
// that the document writes as a string, such as "1e3", does not count.
func isFloatScalar(node *niceyaml.Node, raw any) bool {
	if isExplicitString(node, raw) {
		return false
	}

	switch v := raw.(type) {
	case float64:
		return true
	case string:
		return floatSyntax.MatchString(v)
	default:
		return false
	}
}

// isPlain reports whether the decoder reads t as it reads the basic type
// of its kind. It reports true when t is not [time.Duration] and its
// pointer does not implement any of the unmarshalers that the decoder
// honors. The decoder reads an UnmarshalJSON method only when a
// [niceyaml.JSONUnmarshalers](true) reaches the decode, and a match never
// passes one in its own options. Methods that do not take part in
// decoding, such as a String method, leave a type plain. A
// [time.Duration] decodes by a rule of its own, as a type with an
// unmarshaler does. A type defined on [time.Duration] does not get that
// rule.
func isPlain(t reflect.Type) bool {
	return t != unmarshaler.DurationType &&
		!slices.ContainsFunc(unmarshaler.Interfaces, reflect.PointerTo(t).Implements)
}

// writtenText returns the text of the scalar that node holds. The decoded
// argument is the string that node decodes to. The decoder respells a
// number or a bool that it reads into a string, so 1.10 becomes "1.1",
// 0x10 becomes "16", and True becomes "true". For an integer, a float, an
// infinity, a NaN, or a bool, writtenText returns the text as the
// document spells it. For any other scalar, it returns decoded. It finds
// the scalar with [valueNode].
func writtenText(node *niceyaml.Node, decoded string) string {
	n, _ := valueNode(node)

	switch v := n.(type) {
	case *ast.IntegerNode, *ast.FloatNode, *ast.InfinityNode, *ast.NanNode, *ast.BoolNode:
		return v.GetToken().Value
	default:
		return decoded
	}
}

// isExplicitString reports whether node holds a scalar that the document
// writes as a string: a quoted scalar, a block scalar, or a scalar with a
// !!str tag. It finds the scalar with [valueNode]. It reports false when
// raw, the value as the YAML types name it, is not a string. So a tag
// such as !!int on a quoted scalar decides the type.
func isExplicitString(node *niceyaml.Node, raw any) bool {
	if _, ok := raw.(string); !ok {
		return false
	}

	n, strTagged := valueNode(node)
	if strTagged {
		return true
	}

	switch v := n.(type) {
	case *ast.LiteralNode:
		return true
	case *ast.StringNode:
		t := v.GetToken().Type

		return t == token.SingleQuoteType || t == token.DoubleQuoteType

	default:
		return false
	}
}

// valueNode looks through the anchors, tags, and aliases of node and
// returns the node that they wrap. It also reports whether one of the
// tags it passed is !!str. It follows each alias to the content of its
// anchor, as the decoder does, and an alias that reads a reference
// document to the anchor there. [niceyaml.Node.At] follows an alias at
// the end of a path but stops at a tag on it, so the node of
// `version: !t *v` still holds the alias. At also stops at an alias that
// reads a reference document. The first result is nil for an alias that
// does not resolve or that leads back to itself.
func valueNode(node *niceyaml.Node) (ast.Node, bool) {
	var (
		n         = node.AST()
		strTagged bool
		followed  []*ast.AliasNode
	)

	for !astnode.IsNil(n) {
		switch v := n.(type) {
		case *ast.AnchorNode:
			n = v.Value
		case *ast.TagNode:
			switch v.Start.Value {
			case string(token.StringTag), "!<" + strTagURI + ">":
				strTagged = true
			}

			n = v.Value

		case *ast.AliasNode:
			if slices.Contains(followed, v) {
				return nil, strTagged
			}

			followed = append(followed, v)

			target, err := hook.AliasContent(node, v)
			if err != nil {
				return nil, strTagged
			}

			n = target

		default:
			return n, strTagged
		}
	}

	return nil, strTagged
}

// isNonNumberString reports whether raw, the value of node as the YAML
// types name it, is a string that YAML does not read as a number. Such a
// string is a scalar that the document writes as a string, or a plain
// scalar outside the YAML float syntax, such as inf or 0x1p-2.
func isNonNumberString(node *niceyaml.Node, raw any) bool {
	if _, ok := raw.(string); !ok {
		return false
	}

	if isExplicitString(node, raw) {
		return true
	}

	_, ok := rawFloat(raw)

	return !ok
}

// strTagURI is the full name of the !!str tag, which a verbatim tag
// spells out.
const strTagURI = "tag:yaml.org,2002:str"

// floatEqual reports whether a and b hold the same value. It counts two
// NaNs as equal, so a NaN want matches a .nan in the document.
func floatEqual(a, b float64) bool {
	return a == b || (math.IsNaN(a) && math.IsNaN(b))
}

// floatEqualsInteger reports whether f holds exactly the value of the
// integer v. It compares the two as 64-bit integers, because a float64
// cannot hold every integer greater than 2^53.
func floatEqualsInteger(f float64, v reflect.Value) bool {
	if f != math.Trunc(f) {
		return false
	}

	if isUnsigned(v.Kind()) {
		if f < 0 || f >= math.MaxUint64 {
			return false
		}

		return uint64(f) == v.Uint()
	}

	if f < math.MinInt64 || f >= math.MaxInt64 {
		return false
	}

	return int64(f) == v.Int()
}

// floatHoldsInteger reports whether raw, the value as the YAML types name
// it, can match v, the integer that a decode of raw gave. It reports true
// for a raw value that does not hold a float. An integer never matches a
// float with a fraction. The decoder converts a float to an integer type
// without a range check. On some platforms, the conversion turns -.inf or
// 1e19 into the lowest or highest int64, so the float must also hold the
// value of v. A type that decodes itself reads the float its own way, so
// only the fraction counts for it.
func floatHoldsInteger(raw any, v reflect.Value) bool {
	f, ok := rawFloat(raw)
	if !ok {
		return true
	}

	if f != math.Trunc(f) {
		return false
	}

	return !isPlain(v.Type()) || floatEqualsInteger(f, v)
}

// floatHoldsFloat32 reports whether raw, the value as the YAML types name
// it, can match v, the float32 that a decode of raw gave. It reports true
// for a raw value that does not hold a float. The decoder converts a
// float to a float32 without a range check. The conversion turns a finite
// value too large for a float32, such as 1e39, into an infinity, so an
// infinite v matches only an infinite float. A type that decodes itself
// reads the float its own way, so floatHoldsFloat32 reports true for it.
func floatHoldsFloat32(raw any, v reflect.Value) bool {
	if !isPlain(v.Type()) || !math.IsInf(v.Float(), 0) {
		return true
	}

	f, ok := rawFloat(raw)

	return !ok || math.IsInf(f, 0)
}

// rawFloat returns the float that raw holds, and reports whether raw
// holds one. The raw argument is the value as the YAML types name it. The
// decoder reads some plain floats, such as 25e-1 and 1e19, as strings,
// and it converts them when it decodes them into an integer. So a string
// counts when it has the YAML float syntax. A string that only Go parses
// as a float, such as inf or 0x1p-2, does not count.
func rawFloat(raw any) (float64, bool) {
	switch v := raw.(type) {
	case float64:
		return v, true
	case string:
		if !floatSyntax.MatchString(v) {
			return 0, false
		}

		f, err := strconv.ParseFloat(v, 64)

		return f, err == nil

	default:
		return 0, false
	}
}

func isUnsigned(k reflect.Kind) bool {
	return k >= reflect.Uint && k <= reflect.Uintptr
}

func isFloat(k reflect.Kind) bool {
	return k == reflect.Float32 || k == reflect.Float64
}
