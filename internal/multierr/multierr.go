// Package multierr reads and builds errors that hold several errors: the
// joins [errors.Join] builds, the wrappers [fmt.Errorf] builds with
// several %w verbs, and errors of other types whose message is the
// messages of their branches one per line.
//
// A caller that rebuilds such an error around new branches keeps what
// the old error matched. [Rejoin] and [Replace] return stand-ins that
// match the old error for [errors.Is] and [errors.As] while they unwrap
// to the new errors.
package multierr

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
)

var (
	// The type of the errors [errors.Join] builds.
	joinType = reflect.TypeOf(errors.Join(errors.New("")))

	// The type of the errors [fmt.Errorf] builds with several %w verbs.
	wrapErrorsType = reflect.TypeOf(fmt.Errorf("%w%w", errors.New(""), errors.New("")))
)

// Join returns nil for no errors, the error itself for one, and the
// [errors.Join] of errs for more.
func Join(errs []error) error {
	switch len(errs) {
	case 0:
		return nil
	case 1:
		return errs[0]
	default:
		return errors.Join(errs...)
	}
}

// IsJoin reports whether err is an error that [errors.Join] builds.
func IsJoin(err error) bool {
	return reflect.TypeOf(err) == joinType
}

// IsWrapErrors reports whether err is a wrapper that [fmt.Errorf] builds
// with several %w verbs.
func IsWrapErrors(err error) bool {
	return reflect.TypeOf(err) == wrapErrorsType
}

// JoinMessage reports whether msg is the messages of branches one per
// line, which is how [errors.Join] writes the message of the error it
// builds. A nil branch carries no message and no line of its own, so it
// takes no separator either.
//
// It matches msg against the branches in place and stops at the first
// mismatch, so a caller that checks every level of a chain of wrappers
// copies no message.
func JoinMessage(msg string, branches []error) bool {
	first := true

	for _, branch := range branches {
		if branch == nil {
			continue
		}

		var ok bool

		if !first {
			if msg, ok = strings.CutPrefix(msg, "\n"); !ok {
				return false
			}
		}

		first = false

		if msg, ok = strings.CutPrefix(msg, branch.Error()); !ok {
			return false
		}
	}

	return msg == ""
}

// HoldsFirst reports whether msg holds the message of the first of
// branches that is not a nil interface.
func HoldsFirst(msg string, branches []error) bool {
	first := First(branches)

	return first != nil && strings.Contains(msg, first.Error())
}

// First returns the first of branches that is not a nil interface, or
// nil when every branch is one.
func First(branches []error) error {
	for _, branch := range branches {
		if branch != nil {
			return branch
		}
	}

	return nil
}

// Rejoin returns a join of branches in place of joined, an error that
// holds several. A join [errors.Join] built comes back as another such
// join. A join of any other type comes back as a stand-in whose message
// is the messages of branches one per line, and which unwraps to
// branches. The stand-in matches joined for [errors.Is] and
// [errors.As], as joined matches itself, rather than unwrapping to it.
func Rejoin(joined error, branches []error) error {
	if IsJoin(joined) {
		return errors.Join(branches...)
	}

	return &joinError{like: joined, branches: branches}
}

// Replace returns a stand-in for wrapper, an error that wraps others,
// which unwraps to err in place of what wrapper wraps. Its message is
// msg. It matches wrapper for [errors.Is] and [errors.As], as wrapper
// matches itself, and it matches what each error of beside matches.
func Replace(wrapper, err error, msg string, beside []error) error {
	return &placedError{like: wrapper, err: err, msg: msg, beside: beside}
}

// joinError is the stand-in [Rejoin] returns.
type joinError struct {
	mimic

	// The branches of the join.
	branches []error
}

// Error returns the messages of the branches one per line.
func (j *joinError) Error() string {
	msgs := make([]string, 0, len(j.branches))
	for _, branch := range j.branches {
		msgs = append(msgs, branch.Error())
	}

	return strings.Join(msgs, "\n")
}

// Unwrap returns the branches.
func (j *joinError) Unwrap() []error {
	return j.branches
}

// placedError is the stand-in [Replace] returns.
type placedError struct {
	mimic

	// The error that takes the place of what the wrapper wraps.
	err error
	msg string
	// The errors whose matches the stand-in keeps beside the wrapper's.
	beside []error
}

// Error returns the message [Replace] received.
func (p *placedError) Error() string {
	return p.msg
}

// Unwrap returns the error that takes the place of what the wrapper
// wraps.
func (p *placedError) Unwrap() error {
	return p.err
}

// Is reports whether the wrapper matches target itself, or whether an
// error beside it matches target, as [errors.Is] reports it.
func (p *placedError) Is(target error) bool {
	if p.mimic.Is(target) {
		return true
	}

	return slices.ContainsFunc(p.beside, func(err error) bool { return errors.Is(err, target) })
}

// As reports whether the wrapper sets target itself, or whether an error
// beside it does, as [errors.As] reports it.
func (p *placedError) As(target any) bool {
	if p.mimic.As(target) {
		return true
	}

	return slices.ContainsFunc(p.beside, func(err error) bool { return errors.As(err, target) })
}

// mimic matches what its error matches itself, as [errors.Is] and
// [errors.As] ask of one error in a chain. It reads no error its error
// unwraps to.
type mimic struct {
	like error
}

// Is compares the error with target when the type of target is
// comparable, and then calls the Is method of the error, if it has one.
func (m mimic) Is(target error) bool {
	//nolint:errorlint // The error itself, not a chain search.
	if target != nil && reflect.TypeOf(target).Comparable() && m.like == target {
		return true
	}

	x, ok := m.like.(interface{ Is(target error) bool }) //nolint:errorlint // The error itself, not a chain search.

	return ok && x.Is(target)
}

// As sets target to the error when target points at a type the error is
// assignable to. Otherwise it reports what the As method of the error,
// if it has one, reports.
func (m mimic) As(target any) bool {
	val := reflect.ValueOf(target)
	if val.Kind() == reflect.Pointer && !val.IsNil() && reflect.TypeOf(m.like).AssignableTo(val.Type().Elem()) {
		val.Elem().Set(reflect.ValueOf(m.like))

		return true
	}

	x, ok := m.like.(interface{ As(target any) bool }) //nolint:errorlint // The error itself, not a chain search.

	return ok && x.As(target)
}
