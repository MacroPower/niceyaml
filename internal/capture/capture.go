// Package capture carries a panic or a call to [runtime.Goexit] out of
// work that several callers wait on, so each caller can raise it again
// in its own goroutine.
//
// Shared work, such as a schema load or a catalog fetch, runs on a
// goroutine that no single caller owns. A panic there ends the process,
// because no caller can recover it, and a call to [runtime.Goexit] ends the
// goroutine without a result, so the callers wait forever. [Run] turns
// both into errors that the work hands to its callers, and each caller
// passes the error it receives to [Reraise], which panics with the same
// value or calls [runtime.Goexit].
//
// Reraise matches only the error Run returned, never an error that wraps
// it. Code that keeps a captured error, such as a cached fetch error, may
// later report it wrapped to callers that never waited on the work. Those
// callers get an ordinary error, even when a Reraise further up the stack
// sees it.
package capture

import (
	"errors"
	"fmt"
	"runtime"
)

// ErrGoexit is the error [Run] returns when its function calls
// [runtime.Goexit].
var ErrGoexit = errors.New("runtime.Goexit called")

// PanicError is the error [Run] returns when its function panics. Value
// holds the value the function passed to panic.
type PanicError struct {
	Value any
}

// Error implements error.
func (p *PanicError) Error() string {
	return fmt.Sprintf("panic: %v", p.Value)
}

// Run calls fn on a goroutine of its own, waits for fn to finish, and
// returns the error fn returned. When fn panics, Run returns a
// [*PanicError] with the panic value, and when fn calls [runtime.Goexit],
// Run returns [ErrGoexit]. In both cases the goroutine that called Run
// keeps running.
func Run(fn func() error) error {
	// A call to runtime.Goexit skips the assignment of fn's result, and
	// recover returns nil while it unwinds the goroutine, so err keeps
	// this value only when fn called runtime.Goexit.
	err := ErrGoexit
	done := make(chan struct{})

	go func() {
		defer close(done)

		defer func() {
			if p := recover(); p != nil {
				err = &PanicError{Value: p}
			}
		}()

		err = fn()
	}()

	<-done

	return err
}

// Reraise panics with the value of a [*PanicError] and calls
// [runtime.Goexit] for [ErrGoexit]. It returns for any other error, and
// for nil.
//
// Reraise matches err itself and ignores the errors err wraps, so a
// wrapped report of a past failure stays an ordinary error.
func Reraise(err error) {
	//nolint:errorlint // Only the error Run returned counts, not one wrapping it.
	if pe, ok := err.(*PanicError); ok {
		panic(pe.Value)
	}

	//nolint:errorlint // Only the error Run returned counts, not one wrapping it.
	if err == ErrGoexit {
		runtime.Goexit()
	}
}
