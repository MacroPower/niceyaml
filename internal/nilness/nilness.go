// Package nilness reports whether an interface value does not hold
// anything that a caller can call.
//
// An interface that holds a nil pointer or nil func compares unequal to
// nil, but calling a method through it panics or returns nonsense.
// Constructors that promise to panic on a nil argument call [IsNil], so
// that promise covers these values too. Code that skips nil entries in a
// list calls it to skip these values as well.
package nilness

import "reflect"

// IsNil reports whether v is a nil interface or holds a nil pointer or nil
// func. A nil map or slice can have a method that reads it, so it counts
// as a value.
func IsNil(v any) bool {
	if v == nil {
		return true
	}

	rv := reflect.ValueOf(v)

	switch rv.Kind() {
	case reflect.Pointer, reflect.Func:
		return rv.IsNil()
	default:
		return false
	}
}
