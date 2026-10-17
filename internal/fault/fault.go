// Package fault holds the errors that mark a problem of a document, so
// that several packages of the module can set and match them.
//
// An error type of the module declares the document at fault by matching
// [ErrInvalid] from an Is method. The niceyaml package reads the mark to
// tell a problem of the document from a check that did not run. The
// mark sits in an internal package so the schema package can match it,
// and callers outside the module cannot name it.
//
// [ErrLimit] marks a text past a limit of its source, and the niceyaml
// package exports it. The expansion error of the aliasing package matches
// it too, for aliases that expand to more text, or nest it deeper, than
// the bounds of those limits allow.
package fault

import "errors"

var (
	// ErrInvalid is the target an error matches from an Is method to
	// declare the document at fault. Errors do not wrap it, so its
	// message does not reach a caller.
	ErrInvalid = errors.New("invalid document")

	// ErrLimit indicates a text past a limit of its source. The niceyaml
	// package exports the same error value.
	ErrLimit = errors.New("text past a limit")
)
