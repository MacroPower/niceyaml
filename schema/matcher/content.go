package matcher

import (
	"context"
	"encoding"
	"errors"
	"math"
	"reflect"
	"slices"
	"strconv"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/aliasing"
	"go.jacobcolvin.com/niceyaml/paths"
)

// contentMatcher matches documents by a single YAML content value.
type contentMatcher[T comparable] struct {
	want T
	path paths.Path
}

// Content creates a new [Matcher] that matches documents whose value at
// path decodes to want.
//
// Match decodes the value at path, from the scope of the document, with
// [niceyaml.Node.Decode] as a T and compares the result to want, so
// the type of want decides how Match reads the YAML. A string matches
// the text of a scalar as the document spells it, so "1.10" matches
// version: 1.10 and "1.1" does not. A string type that decodes itself,
// through an UnmarshalYAML or UnmarshalText method, matches the value its
// own decode gives. A number matches its numeric value however the
// document spells it, though an integer want never matches a value with
// a fraction. A null matches only a nil want, such as
// Content[any](path, nil). When T is an interface, two numbers compare
// by value whatever their Go types, so Content[any](path, 1) matches an
// integer the decoder reads as a uint64. A document without the path, or
// whose value does not decode into T, does not match. Any other error
// from the read, such as an alias on the path that names no anchor, a
// path with a wildcard selector, or a context that ended, comes back as
// the error, so a registry stops at the document rather than routing it
// elsewhere. So does a document whose aliases would make the read cost
// far more than the document holds, which Match refuses before it
// decodes anything, with an error matching
// [go.jacobcolvin.com/niceyaml/schema.ErrExcessiveAliasing], as the
// schema validator refuses it:
//
//	// Matches kind: Deployment.
//	matcher.Content(paths.Root().Child("kind"), "Deployment")
//
//	// Matches version: 1.0 and version: 1.
//	matcher.Content(paths.Root().Child("version"), 1.0)
//
// For several conditions, use [All] (AND) or [Any] (OR):
//
//	// Matches kind: Deployment AND apiVersion: apps/v1.
//	matcher.All(
//	    matcher.Content(paths.Root().Child("kind"), "Deployment"),
//	    matcher.Content(paths.Root().Child("apiVersion"), "apps/v1"),
//	)
func Content[T comparable](path paths.Path, want T) Matcher {
	return &contentMatcher[T]{path: path, want: want}
}

// Match implements [Matcher].
func (m *contentMatcher[T]) Match(ctx context.Context, doc *niceyaml.Node) (bool, error) {
	err := ctx.Err()
	if err != nil {
		//nolint:wrapcheck // The error of the context is the reason the matcher cannot decide.
		return false, err
	}

	node, err := doc.At(m.path)
	if errors.Is(err, paths.ErrNotFound) {
		return false, nil
	}

	if err != nil {
		//nolint:wrapcheck // The Document binds the error already.
		return false, err
	}

	// A decode of a node that holds an alias reads the whole document to
	// find its anchors. A few hundred bytes of nested aliases can make
	// that read take minutes, so the document must pass the alias limit
	// of the schema validator before anything decodes it.
	err = aliasing.CheckDecode(node)
	if err != nil {
		//nolint:wrapcheck // Binding names the document; the error keeps its own context.
		return false, doc.Bind(err)
	}

	// Read the value as the YAML types name it first, because a decode
	// into T loses what tells a null from an empty string or a false,
	// and a fraction from the integer it truncates to. A decode into any
	// yields only the YAML built-in types, none of which validates itself,
	// so the self-validation walk would find nothing.
	raw, err := node.Decode[any](ctx, niceyaml.WithSelfValidation(false))
	if errors.Is(err, niceyaml.ErrDecodeRejected) {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	if raw == nil {
		return wantsNil(m.want), nil
	}

	// The decoder rejecting the value is the value not reading as T, which
	// is a no rather than a failure. An error the value's own UnmarshalYAML
	// returns is not a rejection, so it comes back as the error.
	got, err := node.Decode[T](ctx)
	if errors.Is(err, niceyaml.ErrDecodeRejected) {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	gv := reflect.ValueOf(&got).Elem()

	// The decoder respells a number or a bool it reads into a string, so
	// 1.10 becomes "1.1", 0x10 becomes "16", and True becomes "true". A
	// string want matches the scalar's text as written instead.
	if isPlainString(gv.Type()) {
		if text, ok := scalarText(node); ok {
			gv.SetString(text)
		}
	}

	if isInteger(gv.Kind()) && !floatHoldsInteger(raw, gv) {
		return false, nil
	}

	// T may be an interface such as any, whose dynamic type decides whether
	// == is defined. A value that == cannot compare, such as a map,
	// matches nothing rather than panicking. Two numbers behind an
	// interface compare by value, since the decoder picks the Go type of a
	// number and the caller picks the type of want.
	if !gv.Comparable() {
		return false, nil
	}

	if gv.Kind() == reflect.Interface {
		if eq, ok := numericEqual(got, m.want); ok {
			return eq, nil
		}
	}

	return got == m.want, nil
}

// unmarshalerTypes are the interfaces go-yaml decodes a value through
// when its pointer implements one. The decoder reads an UnmarshalJSON
// method only under [yaml.UseJSONUnmarshaler], which a match never sets.
var unmarshalerTypes = []reflect.Type{
	reflect.TypeFor[yaml.BytesUnmarshaler](),
	reflect.TypeFor[yaml.BytesUnmarshalerContext](),
	reflect.TypeFor[yaml.InterfaceUnmarshaler](),
	reflect.TypeFor[yaml.InterfaceUnmarshalerContext](),
	reflect.TypeFor[yaml.NodeUnmarshaler](),
	reflect.TypeFor[yaml.NodeUnmarshalerContext](),
	reflect.TypeFor[encoding.TextUnmarshaler](),
}

// isPlainString reports whether t is a string type that [isPlain]
// reports, so the decoder reads it as it reads a string.
func isPlainString(t reflect.Type) bool {
	return t.Kind() == reflect.String && isPlain(t)
}

// isPlain reports whether the pointer of t implements no unmarshaler the
// decoder honors, so the decoder reads t by its kind. Methods that play
// no part in decoding, such as a String method, leave a type plain.
func isPlain(t reflect.Type) bool {
	return !slices.ContainsFunc(unmarshalerTypes, reflect.PointerTo(t).Implements)
}

// scalarText returns the text of the scalar node holds as the document
// spells it, for a scalar the decoder respells: an integer, a float, an
// infinity, a NaN, or a bool, looking through an anchor or a tag on it.
// The second result is false when node holds anything else, which
// includes an alias, whose own text names the anchor.
func scalarText(node *niceyaml.Node) (string, bool) {
	n := node.AST()

	for {
		switch v := n.(type) {
		case *ast.AnchorNode:
			n = v.Value
		case *ast.TagNode:
			n = v.Value
		case *ast.IntegerNode, *ast.FloatNode, *ast.InfinityNode, *ast.NanNode, *ast.BoolNode:
			return v.GetToken().Value, true
		default:
			return "", false
		}
	}
}

// numericEqual reports whether a and b are numbers, of any integer or
// float kind, holding the same value. A named type such as
// [time.Duration] counts by its kind. The second result is false when either is not a
// number, so the caller falls back to ==.
func numericEqual(a, b any) (bool, bool) {
	av, bv := reflect.ValueOf(a), reflect.ValueOf(b)
	if !isNumber(av) || !isNumber(bv) {
		return false, false
	}

	ak, bk := av.Kind(), bv.Kind()

	switch {
	case isFloat(ak) && isFloat(bk):
		return av.Float() == bv.Float(), true
	case isFloat(ak):
		return floatEqualsInteger(av.Float(), bv), true
	case isFloat(bk):
		return floatEqualsInteger(bv.Float(), av), true
	case isUnsigned(ak) && isUnsigned(bk):
		return av.Uint() == bv.Uint(), true
	case isUnsigned(ak):
		return unsignedEqualsSigned(av.Uint(), bv.Int()), true
	case isUnsigned(bk):
		return unsignedEqualsSigned(bv.Uint(), av.Int()), true
	default:
		return av.Int() == bv.Int(), true
	}
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

func unsignedEqualsSigned(u uint64, i int64) bool {
	if i < 0 || u > math.MaxInt64 {
		return false
	}

	return int64(u) == i
}

// wantsNil reports whether want is nil: a nil interface, or a nil pointer,
// map, slice, channel, or function held by one.
func wantsNil[T comparable](want T) bool {
	v := reflect.ValueOf(&want).Elem()
	if v.Kind() == reflect.Interface {
		if v.IsNil() {
			return true
		}

		v = v.Elem()
	}

	switch v.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Chan, reflect.Func:
		return v.IsNil()
	default:
		return false
	}
}

// isNumber reports whether v holds an integer or a float of any Go type.
func isNumber(v reflect.Value) bool {
	if !v.IsValid() {
		return false
	}

	k := v.Kind()

	return isInteger(k) || isFloat(k)
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

// rawFloat returns the float that raw, the value as the YAML types name
// it, holds, and reports whether it holds one. The decoder reads some
// plain floats, such as 25e-1 and 1e19, as strings and converts them when
// it decodes them into an integer, so a string counts when it parses as
// a float.
func rawFloat(raw any) (float64, bool) {
	switch v := raw.(type) {
	case float64:
		return v, true
	case string:
		f, err := strconv.ParseFloat(v, 64)

		return f, err == nil

	default:
		return 0, false
	}
}

func isInteger(k reflect.Kind) bool {
	return isUnsigned(k) || (k >= reflect.Int && k <= reflect.Int64)
}

func isUnsigned(k reflect.Kind) bool {
	return k >= reflect.Uint && k <= reflect.Uintptr
}

func isFloat(k reflect.Kind) bool {
	return k == reflect.Float32 || k == reflect.Float64
}
