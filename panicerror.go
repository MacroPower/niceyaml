package niceyaml

import (
	"errors"
	"fmt"
	"runtime/debug"
)

// PanicError is the error of a panic that the parse or a decode
// recovered. Value holds the value the code passed to panic. Stack holds
// the stack of the goroutine at the panic, as [debug.Stack] formats it,
// and the message leaves it out.
//
// # Recovered Panics
//
// The go-yaml parser and the go-yaml decoder run under a recover. So
// does the code the decoder calls, which is an UnmarshalYAML or
// UnmarshalText method and a function from [WithCustomUnmarshaler]. A
// panic in any of them comes back as an error that holds a PanicError,
// bound as a [SourceError]. A decode binds it at the node it read, as
// [Node.DecodeInto] describes, and the parse binds it as [Source.File]
// describes. The message names the code that panicked and the value, as
// "decoder panicked: registry not initialized" does. [errors.AsType]
// finds the PanicError, which holds the stack to log:
//
//	if p, ok := errors.AsType[*niceyaml.PanicError](err); ok {
//		slog.Error("decode panicked", "value", p.Value, "stack", string(p.Stack))
//	}
//
// # Faults
//
// A panic is a bug in code, the caller's or go-yaml's, and it says
// nothing about the document. A recovered panic thus matches neither
// [ErrDecode] nor [ErrSyntax], whatever value the code panicked with. It
// declares no fault of the document either, so [IsInvalid] does not
// report the error that a decode or the parse returns for it. A document
// can still trip a panic in go-yaml, as a sequence of three elements does
// in a decode into a [2]string. IsInvalid shows the case a server adds to
// answer such a document with a status of its own.
//
// # Validators
//
// Nothing recovers a panic in a [Validator] or in the Validate method of
// a [SelfValidator], so it reaches the caller as a panic. A caller that
// wants a panic of the decode to crash the same way raises it again:
//
//	if p, ok := errors.AsType[*niceyaml.PanicError](err); ok {
//		panic(p.Value)
//	}
type PanicError struct {
	// Value is the value the code passed to panic.
	Value any
	// Stack is the stack of the goroutine at the panic.
	Stack []byte
}

// Error returns the value of the panic behind "panic: ", formatted as
// the %v verb of [fmt.Sprintf] formats it.
func (e *PanicError) Error() string {
	return fmt.Sprintf("panic: %v", e.Value)
}

// Unwrap returns Value when it is an error, and nil for any other value.
// [errors.Is] and [errors.As] thus reach an error the code panicked with.
func (e *PanicError) Unwrap() error {
	if err, ok := e.Value.(error); ok {
		return err
	}

	return nil
}

// recoveredError is a [*PanicError] as the recover of the parse or of a
// decode returns it. Its message names the code that panicked in front
// of the value, as "decoder panicked: boom" does.
//
// A recoveredError unwraps to nothing, so every walk that reads an error
// along its chain ends at it. The value of the panic thus adds no
// location, no problem, and no fault of the document to the error, even
// when the code panicked with an [*Error]. [errors.As] still finds the
// PanicError and what it unwraps to. [errors.Is] matches what the
// PanicError matches, apart from [ErrDecode], [ErrSyntax], and
// [errInvalid].
//
// Create instances with [recovered].
type recoveredError struct {
	err *PanicError
	// The code that panicked, which is "decoder" or "parser".
	in string
	// Whether the error matches [errPlaced].
	placed bool
}

// recovered creates a new [recoveredError] for p, the value a recover
// returned for a panic in the code that in names. The deferred function
// that recovered p calls it, so the stack still holds the frames of the
// panic.
func recovered(in string, p any) recoveredError {
	return recoveredError{err: &PanicError{Value: p, Stack: debug.Stack()}, in: in}
}

func (e recoveredError) Error() string {
	return fmt.Sprintf("%s panicked: %v", e.in, e.err.Value)
}

// Is reports whether the [*PanicError] e holds matches target, or whether
// target is [errPlaced] for a panic a decode placed. It reports false
// for [ErrDecode], [ErrSyntax], and [errInvalid], even when the code
// panicked with an error that matches one of them.
func (e recoveredError) Is(target error) bool {
	switch target {
	case errPlaced:
		return e.placed

	case ErrDecode, ErrSyntax, errInvalid:
		return false

	default:
		return errors.Is(e.err, target)
	}
}

// As sets target to the [*PanicError] e holds, or to the first error in
// the chain of the value of the panic that matches it, as [errors.As]
// does.
func (e recoveredError) As(target any) bool {
	return errors.As(e.err, target)
}

// holdsPanic reports whether err is, or wraps, a [*PanicError]. The
// document is not at fault for such an error, so a decode and the parse
// pass it on as they pass on the error of a context that ended.
func holdsPanic(err error) bool {
	var p *PanicError

	return errors.As(err, &p)
}
