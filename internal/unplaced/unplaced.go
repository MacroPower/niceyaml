// Package unplaced holds the mark of an error that stands in no document.
//
// An error type of the module declares that its errors are about a value
// that came from no document by matching [Err] from an Is method. The
// niceyaml package binds what such an error wraps, and a later binding
// then places that error in a document. The mark sits in an internal
// package so the schema package can match it while no caller outside the
// module can name it.
package unplaced

import "errors"

// Err is the target an error matches from an Is method to declare that
// the error it wraps stands in no document. No error wraps it, so its
// message reaches no caller.
var Err = errors.New("unplaced error")
