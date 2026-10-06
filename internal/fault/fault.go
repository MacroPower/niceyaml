// Package fault holds the mark of an error the document is at fault for.
//
// An error type of the module declares that fault by matching
// [ErrInvalid] from an Is method. The niceyaml package reads the mark to
// tell a problem of the document from a check that could not run. The
// mark sits in an internal package so the schema package can match it
// while no caller outside the module can name it.
package fault

import "errors"

// ErrInvalid is the target an error matches from an Is method to declare
// the document at fault. No error wraps it, so its message reaches no
// caller.
var ErrInvalid = errors.New("invalid document")
