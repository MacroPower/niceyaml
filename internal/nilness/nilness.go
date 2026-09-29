// Package nilness reports whether an interface value holds nothing a
// constructor can call.
//
// An interface holding a nil pointer or nil func compares unequal to nil,
// yet calling a method through it panics or returns nonsense. Constructors
// that promise to panic on a nil argument call [IsNil] so that promise
// covers these values too.
package nilness

import "reflect"

// IsNil reports whether v is a nil interface or holds a nil pointer or nil
// func. A nil map or slice may have a method that reads it, so it counts as
// a value.
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
