package niceyaml

import (
	"errors"
	"fmt"
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
// [ErrDecode] nor [ErrSyntax], whatever value the code panicked with,
// and it matches no label on a decode problem, such as [ErrWrongKind]. It
// declares no fault of the document either, so [IsInvalid] does not
// report the error that a decode or the parse returns for it. A document
// can still trip a panic in go-yaml, as a sequence of three elements does
// in a decode into a [2]string. IsInvalid shows the case a server adds to
// answer such a document with a status of its own.
//
// # Validators
//
// Nothing recovers a panic in the Check method of a [Validator] or in
// the Validate method of a [SelfValidator], so it reaches the caller as a
// panic. A caller that wants a panic of the decode to crash the same way
// raises it again:
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

// panicRefused holds the targets the error [panicked] returns matches for
// none of the [*PanicError] it holds.
var panicRefused = []error{ErrDecode, ErrUnknownField, ErrWrongKind, ErrOverflow, ErrSyntax, errInvalid}

// panicked returns p, a panic in the code that in names, as the recover
// of the parse or of a decode returns it, with marks on top, as
// [markedError] holds them. Its message names the code that panicked in
// front of the value, as "decoder panicked: boom" does.
//
// The error is sealed, so the value of the panic adds no location, no
// problem, and no fault of the document to the error, even when the code
// panicked with an [*Error]. [errors.As] still finds the PanicError and
// what it unwraps to. [errors.Is] matches what the PanicError matches,
// apart from [ErrDecode] and each label on a decode problem, [ErrSyntax],
// and [errInvalid].
func panicked(in string, p *PanicError, marks ...error) markedError {
	return markedError{
		err:     p,
		msg:     fmt.Sprintf("%s panicked: %v", in, p.Value),
		marks:   marks,
		refused: panicRefused,
		sealed:  true,
	}
}

// holdsPanic reports whether err is, or wraps, a [*PanicError]. The
// document is not at fault for such an error, so a decode and the parse
// pass it on as they pass on the error of a context that ended.
func holdsPanic(err error) bool {
	var p *PanicError

	return errors.As(err, &p)
}
