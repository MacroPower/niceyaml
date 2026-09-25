package matcher

import (
	"context"
	"errors"
	"math"
	"reflect"

	"go.jacobcolvin.com/niceyaml"
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
// the text of a scalar, and a number matches its numeric value however
// the document spells it, though an integer want never matches a value
// with a fraction. A null matches only a nil want, such as
// Content[any](path, nil). When T is an interface, two numbers compare
// by value whatever their Go types, so Content[any](path, 1) matches an
// integer the decoder reads as a uint64. A document without the path, or
// whose value does not decode into T, does not match. Any other error
// from the read, such as an alias on the path that names no anchor, a
// path with a wildcard selector, or a context that ended, comes back as
// the error, so a registry stops at the document rather than routing it
// elsewhere:
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

	// Read the value as the YAML types name it first, because a decode
	// into T loses what tells a null from an empty string or a false,
	// and a fraction from the integer it truncates to.
	raw, err := node.Decode[any](ctx)
	if errors.Is(err, niceyaml.ErrDecodeRejected) {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	if raw == nil {
		return any(m.want) == nil, nil
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

	if f, ok := raw.(float64); ok && isInteger(gv.Kind()) && f != math.Trunc(f) {
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

// numericEqual reports whether a and b are numbers of predeclared Go
// types holding the same value. The second result is false when either
// is not such a number, so the caller falls back to ==.
func numericEqual(a, b any) (bool, bool) {
	av, bv := reflect.ValueOf(a), reflect.ValueOf(b)
	if !isPredeclaredNumber(av) || !isPredeclaredNumber(bv) {
		return false, false
	}

	ak, bk := av.Kind(), bv.Kind()

	switch {
	case isFloat(ak) || isFloat(bk):
		return toFloat(av) == toFloat(bv), true
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

func unsignedEqualsSigned(u uint64, i int64) bool {
	if i < 0 || u > math.MaxInt64 {
		return false
	}

	return int64(u) == i
}

func isPredeclaredNumber(v reflect.Value) bool {
	if !v.IsValid() || v.Type().PkgPath() != "" {
		return false
	}

	k := v.Kind()

	return isInteger(k) || isFloat(k)
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

func toFloat(v reflect.Value) float64 {
	return v.Convert(reflect.TypeFor[float64]()).Float()
}
