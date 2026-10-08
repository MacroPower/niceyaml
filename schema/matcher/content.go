package matcher

import (
	"context"
	"encoding"
	"errors"
	"math"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/aliasing"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/paths"
)

// Scalar is the set of types a want of [Content] may have: a string, a
// bool, an integer, or a float, or a type defined on one of them, such
// as [time.Duration].
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
// path holds want. The type of want decides how Match reads the scalar.
//
// A string matches the text of the scalar, quoted or plain, as [Text]
// reads it. A plain number or bool keeps its spelling, so "1.10" matches
// version: 1.10 and "1.1" does not.
//
// A number matches a scalar that YAML reads as a number, and a bool
// matches one that YAML reads as a bool, each by its value. So 16
// matches version: 0x10 and 1.0 matches version: 1, but 2 does not match
// version: "2", which is a string. An integer matches no value with a
// fraction, and a NaN matches .nan.
//
// A type defined on a string, a bool, or a number reads as that type
// does. A type that decodes itself, through an UnmarshalYAML or
// UnmarshalText method, matches the value its own decode gives, and so
// does a [time.Duration], which reads from text such as 5s and never
// from a number. A type with a Validate method validates the value Match
// reads, as a decode into it does, and Match returns the error of a
// value that fails.
//
// A null, a mapping, and a sequence match no want. Neither does a
// document without the path, or a scalar that does not read as the type
// of want. To match a null, use a [Func], as its example does. To test
// the text of a scalar rather than compare it, use [Text].
//
// The path resolves as [niceyaml.Node.At] resolves it: a `$` path from
// the root of the document, and an `@` path from the Node the matcher
// gets. A path that cannot resolve comes back as the error, so a
// registry stops at the document rather than routing it elsewhere. Such
// a path holds a wildcard selector, or an alias that names no anchor
// before it in the document. The error of a context that ended comes
// back too.
//
// Match also refuses a document whose aliases would make the read cost
// far more than the document holds, unless [niceyaml.WithAliasLimit]
// turned the limit off for its source. It refuses such a document before
// it decodes anything, as a decode does. The document is at fault for
// that error, as [niceyaml.IsInvalid] describes, and it matches
// [go.jacobcolvin.com/niceyaml/schema.ErrExcessiveAliasing]:
//
//	// Matches kind: Deployment.
//	matcher.Content(paths.Doc().Child("kind"), "Deployment")
//
//	// Matches version: 1.0 and version: 1.
//	matcher.Content(paths.Doc().Child("version"), 1.0)
//
// For several conditions, use [All] (AND) or [Any] (OR):
//
//	// Matches kind: Deployment AND apiVersion: apps/v1.
//	matcher.All(
//	    matcher.Content(paths.Doc().Child("kind"), "Deployment"),
//	    matcher.Content(paths.Doc().Child("apiVersion"), "apps/v1"),
//	)
func Content[T Scalar](path paths.Path, want T) Matcher {
	return &contentMatcher[T]{path: path, want: want}
}

// Match implements [Matcher].
func (m *contentMatcher[T]) Match(ctx context.Context, doc *niceyaml.Node) (bool, error) {
	t := reflect.TypeFor[T]()

	node, raw, err := readScalar(ctx, doc, m.path, t)
	if err != nil || raw == nil {
		return false, err
	}

	// The decoder reads some plain floats, such as 1e3, as strings and
	// hands them to time.ParseDuration for a time.Duration, which returns
	// its own error. A number never decodes into a time.Duration, so such
	// a float does not match, as 1.5e3 does not.
	if t == durationType && isFloatScalar(node, raw) {
		return false, nil
	}

	// A decode that fails means the value does not read as T, which is a
	// no rather than a failure. That holds for an error the value's own
	// UnmarshalYAML returns too.
	got, err := decodeScalar[T](ctx, node)
	if errors.Is(err, niceyaml.ErrDecode) {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	return matchScalar(node, raw, reflect.ValueOf(got), reflect.ValueOf(m.want)), nil
}

// readScalar returns the node at path below doc and the value of that
// node as the YAML types name it. The value is nil, with no error, where
// no want matches: doc holds no node at path, the node holds a null, or
// the decoder rejects the node. The caller decodes the node as a t next,
// so readScalar applies the alias limit of such a decode first.
func readScalar(ctx context.Context, doc *niceyaml.Node, path paths.Path, t reflect.Type) (*niceyaml.Node, any, error) {
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

	// A few hundred bytes of nested aliases can make a decode take
	// minutes, so a node that holds an alias decodes only when its whole
	// document passes the alias limit. The aliases of the document are
	// the cause of a refusal, so the error declares the document at
	// fault.
	err = aliasing.CheckDecode(node)
	if err != nil {
		//nolint:wrapcheck // Binding names the document; the error keeps its own context.
		return nil, nil, doc.Invalid(err)
	}

	// A decode into a type that decodes itself from text writes the node
	// out with a copy of the text of each alias, which the count above
	// leaves out. A type whose UnmarshalYAML takes a decode function gets
	// the same treatment, since it may decode the node into such a type.
	if aliasing.DecodesText(t) {
		err = aliasing.CheckDecodeText(node)
		if err != nil {
			//nolint:wrapcheck // Binding names the document; the error keeps its own context.
			return nil, nil, doc.Invalid(err)
		}
	}

	// Read the value as the YAML types name it first, because a decode
	// into t loses what tells a null from an empty string or a false,
	// and a fraction from the integer it truncates to. A decode into any
	// yields only the YAML built-in types, none of which validates itself,
	// so the self-validation walk would find nothing.
	raw, err := node.Decode[any](ctx, niceyaml.WithSelfValidation(false))
	if errors.Is(err, niceyaml.ErrDecode) {
		return nil, nil, nil
	}

	if err != nil {
		return nil, nil, err
	}

	return node, raw, nil
}

// decodeScalar returns the value node decodes to as a T, validated as a
// decode of a T validates it. A T that [isPlain] reports decodes as the
// basic type of its kind, which the decoder reads the same way. The
// go-yaml decoder cannot set a float type with a name of its own from a
// float it reads as a string, such as 1e3, and the basic type has no
// such gap.
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
// matches want, where raw holds the value of node as the YAML types name
// it. Both got and want hold the type of the want that [Content] took.
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
	// The interfaces go-yaml decodes a value through when its pointer
	// implements one. The decoder reads an UnmarshalJSON method only under
	// [niceyaml.WithJSONUnmarshalers], which a match never sets.
	unmarshalerTypes = []reflect.Type{
		reflect.TypeFor[yaml.BytesUnmarshaler](),
		reflect.TypeFor[yaml.BytesUnmarshalerContext](),
		reflect.TypeFor[yaml.InterfaceUnmarshaler](),
		reflect.TypeFor[yaml.InterfaceUnmarshalerContext](),
		reflect.TypeFor[yaml.NodeUnmarshaler](),
		reflect.TypeFor[yaml.NodeUnmarshalerContext](),
		reflect.TypeFor[encoding.TextUnmarshaler](),
	}

	// The type go-yaml decodes by a rule of its own, as it decodes a type
	// with an unmarshaler. It parses a [time.Duration] from the text of a
	// scalar, and a type defined on one gets no such rule.
	durationType = reflect.TypeFor[time.Duration]()

	// The basic type of each kind a [Scalar] has.
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

	// The pattern of the decimal and exponent spellings of a float in the
	// YAML 1.2 core schema. It leaves out the infinity and NaN spellings.
	// The decoder reads most of them as floats, and it cannot convert the
	// rest, such as +.inf, into a number type.
	floatSyntax = regexp.MustCompile(`^[-+]?(\.\d+|\d+(\.\d*)?)([eE][-+]?\d+)?$`)
)

// isFloatScalar reports whether raw, the value of node as the YAML types
// name it, holds a float or a string in the YAML float syntax. The syntax
// alone decides, so 1e999, which overflows a float64, counts. A scalar
// the document writes as a string does not count, so "1e3" does not.
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
// of its kind, which holds when t is no [time.Duration] and its pointer
// implements no unmarshaler the decoder honors. Methods that play no
// part in decoding, such as a String method, leave a type plain.
func isPlain(t reflect.Type) bool {
	return t != durationType &&
		!slices.ContainsFunc(unmarshalerTypes, reflect.PointerTo(t).Implements)
}

// writtenText returns the text of the scalar node holds, where decoded
// is the string node decodes to. The decoder respells a number or a bool
// it reads into a string, so 1.10 becomes "1.1", 0x10 becomes "16", and
// True becomes "true". For such a scalar, which is an integer, a float,
// an infinity, a NaN, or a bool, writtenText returns the text as the
// document spells it. It returns decoded for any other. It reaches the
// scalar as [valueNode] does.
func writtenText(node *niceyaml.Node, decoded string) string {
	n, _ := valueNode(node)

	switch v := n.(type) {
	case *ast.IntegerNode, *ast.FloatNode, *ast.InfinityNode, *ast.NanNode, *ast.BoolNode:
		return v.GetToken().Value
	default:
		return decoded
	}
}

// isExplicitString reports whether node holds a scalar the document
// writes as a string: a quoted scalar, a block scalar, or a scalar with
// a !!str tag. It reaches the scalar as [valueNode] does. The value raw
// holds, as the YAML types name it, must be a string, so a tag such as
// !!int on a quoted scalar decides the type.
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

// valueNode returns the node that node selects with its anchors, tags,
// and aliases looked through, and reports whether a !!str tag sits on the
// way. It follows each alias to the content of its anchor, as the decoder
// does. [niceyaml.Node.At] follows an alias at the end of a path but stops
// at a tag on it, so the node of `version: !t *v` still holds the alias.
// The first result is nil for an alias that does not resolve or that
// leads back to itself.
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

			target, err := node.Resolver().Deref(v)
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
// string is a scalar the document writes as a string, or a plain scalar
// outside the YAML float syntax, such as inf or 0x1p-2.
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

// floatEqual reports whether a and b hold the same value, counting two
// NaNs as equal, so a NaN want matches a .nan in the document.
func floatEqual(a, b float64) bool {
	return a == b || (math.IsNaN(a) && math.IsNaN(b))
}

// floatEqualsInteger reports whether f holds exactly the value of the
// integer v. The comparison runs in the integer's own type, since a
// float64 cannot hold every integer above 2^53.
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
// it, can match v, the integer a decode of it gave, and reports true for
// a raw value that holds no float. An integer never matches a float with
// a fraction. The decoder converts a float to an integer type of its own
// without a range check, which turns -.inf or 1e19 into the lowest or
// highest int64 on some platforms, so the float must also hold the value
// of v. A type that decodes itself reads the float its own way, so only
// the fraction counts for it.
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
// it, can match v, the float32 a decode of it gave, and reports true for
// a raw value that holds no float. The decoder converts a float to a
// float32 without a range check, which turns a finite value too large
// for a float32, such as 1e39, into an infinity, so an infinite v
// matches only an infinite float. A type that decodes itself reads the
// float its own way, so floatHoldsFloat32 reports true for it.
func floatHoldsFloat32(raw any, v reflect.Value) bool {
	if !isPlain(v.Type()) || !math.IsInf(v.Float(), 0) {
		return true
	}

	f, ok := rawFloat(raw)

	return !ok || math.IsInf(f, 0)
}

// rawFloat returns the float that raw, the value as the YAML types name
// it, holds, and reports whether it holds one. The decoder reads some
// plain floats, such as 25e-1 and 1e19, as strings and converts them when
// it decodes them into an integer, so a string counts when it has the
// YAML float syntax. A string that Go alone parses as a float, such as
// inf or 0x1p-2, does not count.
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
