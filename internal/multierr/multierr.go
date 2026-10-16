// Package multierr reads and builds errors that hold several errors: the
// joins [errors.Join] builds, the wrappers [fmt.Errorf] builds with
// several %w verbs, and errors of other types whose message is the
// messages of their branches one per line.
//
// A caller that rebuilds such an error around new branches keeps what
// the old error matched. [Rejoin], [Rewrap], and [Replace] return
// stand-ins that match the old error for [errors.Is] and [errors.As]
// while they unwrap to the new errors. A caller that must drop the old
// error rebuilds a wrapper of [fmt.Errorf] with [Wrap] or [WrapErrors]
// instead. The fmt package does not export the types of those wrappers,
// so no caller targets them with [errors.As], and the rebuilt wrapper
// matches nothing itself. [Map] rebuilds an error of this package around
// new copies of the errors it holds.
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

	// The type of the errors [fmt.Errorf] builds with one %w verb.
	wrapType = reflect.TypeOf(fmt.Errorf("%w", errors.New("")))
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
// with several %w verbs, a stand-in for one that [Rewrap] returns, or a
// copy of one that [WrapErrors] returns.
func IsWrapErrors(err error) bool {
	if _, ok := err.(*wrapError); ok { //nolint:errorlint // The error itself, not a chain search.
		return true
	}

	return reflect.TypeOf(err) == wrapErrorsType
}

// IsWrap reports whether err is a wrapper that [fmt.Errorf] builds with
// one %w verb.
func IsWrap(err error) bool {
	return reflect.TypeOf(err) == wrapType
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

// Rewrap returns a stand-in for wrapper, a wrapper that [fmt.Errorf]
// builds with several %w verbs. The stand-in unwraps to branches in place
// of what wrapper unwraps to, and its message is msg. [IsWrapErrors]
// reports it as such a wrapper, so a caller reads it as it reads wrapper.
// It matches wrapper for [errors.Is] and [errors.As], as wrapper matches
// itself, and it matches what each of branches matches.
func Rewrap(wrapper error, branches []error, msg string) error {
	return &wrapError{like: wrapper, branches: branches, msg: msg}
}

// Replace returns a stand-in for wrapper, an error that wraps others,
// which unwraps to err in place of what wrapper wraps. Its message is
// msg. It matches wrapper for [errors.Is] and [errors.As], as wrapper
// matches itself, and it matches what each error of beside matches.
func Replace(wrapper, err error, msg string, beside []error) error {
	return &placedError{like: wrapper, err: err, msg: msg, beside: beside}
}

// Wrap returns an error whose message is msg and which unwraps to err, as
// a wrapper that [fmt.Errorf] builds with one %w verb does. The error
// keeps no reference to the wrapper it replaces, so it matches nothing
// itself, unlike the stand-in [Replace] returns.
func Wrap(err error, msg string) error {
	return &placedError{err: err, msg: msg}
}

// WrapErrors returns an error whose message is msg and which unwraps to
// branches, as a wrapper that [fmt.Errorf] builds with several %w verbs
// does. [IsWrapErrors] reports it as such a wrapper. The error keeps no
// reference to the wrapper it replaces, so it matches nothing itself,
// unlike the stand-in [Rewrap] returns.
func WrapErrors(branches []error, msg string) error {
	return &wrapError{branches: branches, msg: msg}
}

// IsBuilt reports whether this package built err. The stand-ins of
// [Rejoin], [Rewrap], and [Replace] count, and so do the wrappers of
// [Wrap] and [WrapErrors].
func IsBuilt(err error) bool {
	switch err.(type) { //nolint:errorlint // The error itself, not a chain search.
	case *joinError, *wrapError, *placedError:
		return true
	default:
		return false
	}
}

// Map returns a copy of err, an error that [IsBuilt] reports, with f
// applied to each error that err holds. Those are the error that err
// stands in for, the errors it unwraps to, and the errors beside it that
// [Replace] describes. The function f returns the error that the copy
// holds in place of the one f receives. It also reports whether the two
// differ. Map passes no nil error to f, and it reports whether f changed
// an error. When f changes none, or [IsBuilt] reports false for err, Map
// returns err itself and false.
func Map(err error, f func(error) (error, bool)) (error, bool) {
	switch x := err.(type) { //nolint:errorlint // The error itself, not a chain search.
	case *joinError:
		like, stands := mapOne(x.like, f)
		branches, holds := mapAll(x.branches, f)

		if !stands && !holds {
			return err, false
		}

		return &joinError{like: like, branches: branches}, true

	case *wrapError:
		like, stands := mapOne(x.like, f)
		branches, holds := mapAll(x.branches, f)

		if !stands && !holds {
			return err, false
		}

		return &wrapError{like: like, branches: branches, msg: x.msg}, true

	case *placedError:
		like, stands := mapOne(x.like, f)
		inner, holds := mapOne(x.err, f)
		beside, keeps := mapAll(x.beside, f)

		if !stands && !holds && !keeps {
			return err, false
		}

		return &placedError{like: like, err: inner, msg: x.msg, beside: beside}, true

	default:
		return err, false
	}
}

// mapOne returns err as f maps it, and whether f changed it. A nil err
// comes back as it is.
func mapOne(err error, f func(error) (error, bool)) (error, bool) {
	if err == nil {
		return nil, false
	}

	return f(err)
}

// mapAll returns errs with each mapped as [mapOne] maps it, and whether
// that changed any of them. It returns errs itself when it changed none.
func mapAll(errs []error, f func(error) (error, bool)) ([]error, bool) {
	var out []error

	for i, err := range errs {
		mapped, changed := mapOne(err, f)
		if changed && out == nil {
			out = slices.Clone(errs)
		}

		if out != nil {
			out[i] = mapped
		}
	}

	if out == nil {
		return errs, false
	}

	return out, true
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

// wrapError is the stand-in [Rewrap] returns, or the wrapper [WrapErrors]
// returns.
type wrapError struct {
	mimic

	msg string
	// The errors that take the place of the branches of the wrapper.
	branches []error
}

// Error returns the message [Rewrap] or [WrapErrors] received.
func (w *wrapError) Error() string {
	return w.msg
}

// Unwrap returns the errors that take the place of the branches of the
// wrapper.
func (w *wrapError) Unwrap() []error {
	return w.branches
}

// placedError is the stand-in [Replace] returns, or the wrapper [Wrap]
// returns.
type placedError struct {
	mimic

	// The error that takes the place of what the wrapper wraps.
	err error
	msg string
	// The errors whose matches the stand-in keeps beside the wrapper's.
	beside []error
}

// Error returns the message [Replace] or [Wrap] received.
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
// unwraps to. A mimic with no error matches nothing.
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
	if m.like == nil {
		return false
	}

	val := reflect.ValueOf(target)
	if val.Kind() == reflect.Pointer && !val.IsNil() && reflect.TypeOf(m.like).AssignableTo(val.Type().Elem()) {
		val.Elem().Set(reflect.ValueOf(m.like))

		return true
	}

	x, ok := m.like.(interface{ As(target any) bool }) //nolint:errorlint // The error itself, not a chain search.

	return ok && x.As(target)
}
