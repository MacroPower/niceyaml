package niceyaml

import (
	"context"
	"errors"
	"slices"
	"sync"

	"go.jacobcolvin.com/niceyaml/internal/multierr"
	"go.jacobcolvin.com/niceyaml/internal/quantity"
	"go.jacobcolvin.com/niceyaml/position"
)

// severity is the mark that decides whether a problem is an error or a
// warning. A problem with no mark on its cause chain is an error.
type severity uint8

// The marks a problem carries. With no mark, the mark further down the
// chain decides. [NewWarning] and [Node.Warn] mark a warning, and
// [AsErrors] marks an error above each warning.
const (
	severityNone severity = iota
	severityWarning
	severityError
)

// NewWarning creates a new [*Error] with the given message that is a
// warning rather than an error. It takes the options of [NewError], so a
// warning carries a path, a position, a range, and details, and binds to
// a [Source] as an error does. The document is at fault for a warning,
// the same way it is for an error from NewError, so [IsInvalid] reports a
// warning that stands alone.
//
// A warning does not make a check fail. A [Validator] reports one with
// [Node.Warn], and a Validate method of a [SelfValidator] returns one
// alone or joined with its errors:
//
//	func (s MenuItemSpec) Validate() error {
//		if slices.Contains(s.Tags, "seasonal") && s.Until == "" {
//			return niceyaml.NewWarning("a seasonal item without until stays on the menu all year",
//				niceyaml.AtPath(paths.Current().Child("tags")))
//		}
//
//		return nil
//	}
//
// The Warnings section of the package documentation describes how the
// calls of this package collect warnings and how the renderers show them.
func NewWarning(msg string, opts ...ErrorOption) *Error {
	e := NewError(msg, opts...)
	e.severity = severityWarning

	return e
}

// severityOf returns the nearest severity mark on the cause chain of err,
// which callers read for one problem. A wrapper with several branches is
// a warning when one of its branches is. The branches of a wrapper that
// is one problem are that problem and sentinels, which carry no mark, so
// the problem decides. The walk stops at a sealed error and at a
// [*PanicError], which mark nothing.
func severityOf(err error) severity {
	for cur := err; !isNothing(cur); {
		switch x := cur.(type) { //nolint:errorlint // Walks the chain one node at a time.
		case *Error:
			if x.severity != severityNone {
				return x.severity
			}

			cur = x.err

		case *markedError:
			if x.sealed {
				return severityNone
			}

			cur = x.err

		case *PanicError:
			return severityNone

		case interface{ Unwrap() error }:
			cur = x.Unwrap()

		case interface{ Unwrap() []error }:
			if slices.ContainsFunc(x.Unwrap(), isWarning) {
				return severityWarning
			}

			return severityNone

		default:
			return severityNone
		}
	}

	return severityNone
}

// isWarning reports whether the nearest severity mark on the cause chain
// of err marks a warning.
func isWarning(err error) bool {
	return severityOf(err) == severityWarning
}

// marksWarning reports whether err or any error below it, details and
// bindings included, carries the mark of a warning. A false answer means
// that every problem of err is an error, so a caller skips the tree.
func marksWarning(err error) bool {
	switch x := err.(type) { //nolint:errorlint // Walks the tree one node at a time.
	case nil:
		return false

	case *SourceError:
		if x == nil {
			return false
		}

		if marksWarning(x.err) {
			return true
		}

		return slices.ContainsFunc(x.below, func(c boundChild) bool { return marksWarning(c.bound) })

	case *Error:
		if x == nil {
			return false
		}

		if x.severity == severityWarning || marksWarning(x.err) {
			return true
		}

		return slices.ContainsFunc(x.errors, marksWarning) || slices.ContainsFunc(x.details, marksWarning)

	case interface{ Unwrap() error }:
		return marksWarning(x.Unwrap())

	case interface{ Unwrap() []error }:
		return slices.ContainsFunc(x.Unwrap(), marksWarning)

	default:
		return false
	}
}

// HasErrors reports whether err holds an error, as opposed to warnings
// alone. It reports false for a nil err, for a nil [*Error] or
// [*SourceError] pointer, and for an error whose every problem is a
// warning, as [NewWarning] and [Node.Warn] mark one. It reports true for
// any other error, one with no problem included, such as an error with an
// empty message. The problems are the nodes [Problems] yields.
//
// A Validate method returns its warnings beside its errors, so code that
// calls one itself asks HasErrors whether the method failed:
//
//	if err := cfg.Validate(); niceyaml.HasErrors(err) {
//		return err
//	}
func HasErrors(err error) bool {
	if isNothing(err) {
		return false
	}

	if !marksWarning(err) {
		return true
	}

	found := false

	for problem := range Problems(err) {
		found = true

		if !isWarning(problem.Err) {
			return true
		}
	}

	return !found
}

// holdsWarning reports whether a problem of err is a warning.
func holdsWarning(err error) bool {
	if !marksWarning(err) {
		return false
	}

	for problem := range Problems(err) {
		if isWarning(problem.Err) {
			return true
		}
	}

	return false
}

// AsErrors returns err with an error mark above each warning, so each
// warning of err becomes an error. The mark leaves the text of a
// problem as it was, without the "warning:" label, and each heading that
// niceyaml wrote to count the errors and the warnings below it counts
// them again, so "2 errors, 1 warning" reads "3 errors". A program in
// strict mode applies it to the warnings that its handler receives, or to
// the error of a call that fails:
//
//	var warned error
//	ctx = niceyaml.WithWarningHandler(ctx, func(w error) { warned = w })
//
//	config, err := doc.Decode[Config](ctx, niceyaml.WithValidator(reg))
//	if strict {
//		if err == nil {
//			err = warned
//		}
//
//		err = niceyaml.AsErrors(err)
//	}
//
// AsErrors rebuilds the errors that niceyaml builds, such as an [*Error],
// a [*SourceError], and a join, and calls the AsErrors method of an error
// that has one, so the result of such an error keeps its type. A wrapper
// that [fmt.Errorf] builds with several %w verbs and that heads several
// problems gets a mark above each problem, as a join does. The result
// keeps the message of the wrapper and matches it for [errors.Is] and
// [errors.As]. AsErrors marks any other error above the warning it wraps,
// so a program applies it to an error as a call returned it, before it
// wraps the error with [fmt.Errorf]. An err that holds no warning comes
// back as it is, and a nil err returns nil.
func AsErrors(err error) error {
	if isNothing(err) {
		return nil
	}

	if x, ok := err.(interface{ AsErrors() error }); ok { //nolint:errorlint // The node itself, not a chain search.
		return x.AsErrors() //nolint:wrapcheck // The error rebuilds itself and keeps its type.
	}

	if !holdsWarning(err) {
		return err
	}

	return remark(err, severityError)
}

// remark returns err with sev marked above each of its problems that
// carries another severity, where no mark counts as an error. It rebuilds
// each heading over its problems, and counts the problems of a tally again.
func remark(err error, sev severity) error {
	if isNothing(err) {
		return err
	}

	if sev == severityError {
		if x, ok := err.(interface{ AsErrors() error }); ok { //nolint:errorlint // The node itself, not a chain search.
			return x.AsErrors() //nolint:wrapcheck // The error rebuilds itself and keeps its type.
		}
	}

	switch x := err.(type) { //nolint:errorlint // The node itself, not a chain search.
	case *SourceError:
		return x.remarked(sev)

	case *Error:
		if len(x.errors) > 0 {
			c := *x
			c.errors = remarkAll(x.errors, sev)
			c.details = slices.Clone(x.details)

			if x.tally {
				c.err = errors.New(tallyText(c.errors))
			}

			return &c
		}

		if heads(x.err) {
			c := *x
			c.err = remark(x.err, sev)
			c.details = slices.Clone(x.details)

			return &c
		}
	}

	joined := mapJoin(err, func(branch error) error { return remark(branch, sev) })
	if joined != nil {
		return joined
	}

	x, ok := err.(interface{ Unwrap() []error }) //nolint:errorlint // The node itself, not a chain search.
	if ok && multierr.IsWrapErrors(err) && heads(err) {
		rewrapped, _ := mapWrapper(err, x.Unwrap(), func(branch error) (error, bool) {
			return remark(branch, sev), true
		})

		return rewrapped
	}

	return mark(err, sev)
}

// remarkAll returns errs with each remarked as [remark] marks it.
func remarkAll(errs []error, sev severity) []error {
	out := make([]error, 0, len(errs))
	for _, err := range errs {
		out = append(out, remark(err, sev))
	}

	return out
}

// heads reports whether err stands over separate problems through the
// Errors on top of it. Such an err is a join, a summary, a binding of
// either, or a wrapper with several %w verbs that heads several problems.
// A wrapper with one problem among its branches reads as that problem.
func heads(err error) bool {
	for cur := err; !isNothing(cur); {
		if _, _, ok := joinBranches(cur); ok {
			return true
		}

		switch x := cur.(type) { //nolint:errorlint // Walks the chain one node at a time.
		case *Error:
			if len(x.errors) > 0 {
				return true
			}

			cur = x.err

		case *SourceError:
			return x.heads()

		case interface{ Unwrap() []error }:
			if !multierr.IsWrapErrors(cur) {
				return false
			}

			kept, next := followBranches(cur, x.Unwrap())
			if next == nil {
				return len(kept) > 0
			}

			cur = next

		default:
			return false
		}
	}

	return false
}

// mark returns err, one problem, with sev marked above it, or err as it is
// when it carries that severity already. A binding gets the mark below it,
// so its text gains or loses the "warning:" label.
func mark(err error, sev severity) error {
	current := severityOf(err)
	if current == sev || sev == severityError && current == severityNone {
		return err
	}

	if x, ok := err.(*SourceError); ok { //nolint:errorlint // The node itself, not a chain search.
		return x.remarked(sev)
	}

	return &Error{err: err, severity: sev}
}

// remarked returns a copy of e with sev marked above each of its problems,
// as [remark] marks them. A binding of one problem gets the mark above the
// error it binds, so it keeps its location and its children.
func (e *SourceError) remarked(sev severity) *SourceError {
	c := &SourceError{
		err:     e.err,
		free:    e.free,
		locErr:  e.locErr,
		source:  e.source,
		node:    e.node,
		loc:     e.loc,
		end:     e.end,
		ranges:  e.ranges,
		below:   e.below,
		rng:     e.rng,
		adopted: e.adopted,
	}

	if e.heads() {
		c.below = make([]boundChild, 0, len(e.below))

		for _, child := range e.below {
			if !child.detail {
				child.bound = child.bound.remarked(sev)
			}

			c.below = append(c.below, child)
		}

		c.err = retally(e.err, sev)

		return c
	}

	current := severityOf(e.err)
	if current == sev || sev == severityError && current == severityNone {
		return e
	}

	c.err = &Error{err: e.err, severity: sev}

	if e.free != nil {
		c.free = &Error{err: e.free, severity: sev}
	}

	return c
}

// retally returns err with each tally along its chain of Errors counted
// again once its problems carry sev, and with those problems remarked.
func retally(err error, sev severity) error {
	x, ok := err.(*Error) //nolint:errorlint // Walks the chain one node at a time.
	if !ok || x == nil {
		return err
	}

	c := *x

	if x.tally {
		c.errors = remarkAll(x.errors, sev)
		c.err = errors.New(tallyText(c.errors))

		return &c
	}

	c.err = retally(x.err, sev)

	return &c
}

// newTally returns a heading over members that counts the errors and the
// warnings among them, as "2 errors, 1 warning", or the one member alone.
// A nil member adds nothing.
func newTally(members []error) error {
	members = slices.DeleteFunc(slices.Clone(members), isNothing)

	switch len(members) {
	case 0:
		return nil
	case 1:
		return members[0]
	}

	return &Error{err: errors.New(tallyText(members)), errors: members, tally: true}
}

// tallyText returns the count of the errors and the warnings among
// members, as "2 errors, 1 warning". A severity with none stays out.
func tallyText(members []error) string {
	warnings := 0

	for _, m := range members {
		if isWarning(m) {
			warnings++
		}
	}

	return countText(len(members)-warnings, warnings)
}

// countText returns errs errors and warns warnings as "2 errors, 1
// warning". A count of zero stays out, and two zeros read "0 errors".
func countText(errs, warns int) string {
	var parts []string

	if errs > 0 || warns == 0 {
		parts = append(parts, quantity.Plural(errs, "error"))
	}

	if warns > 0 {
		parts = append(parts, quantity.Plural(warns, "warning"))
	}

	if len(parts) == 1 {
		return parts[0]
	}

	return parts[0] + ", " + parts[1]
}

// warningKey is the key of the warning handler on a context.
type warningKey struct{}

// warningHandler receives the warnings of a call.
type warningHandler func(error)

// WithWarningHandler returns a copy of ctx that carries fn, so each call
// of this package that runs with the context hands its warnings to fn. A
// call that passes calls fn once, with one error that holds every
// warning of the call, sorted by position: the warning alone, or a
// heading such as "cafe.yaml: 2 warnings" over several. A call that fails
// returns its warnings in its error and calls fn with none. Without a
// handler, a call that passes drops its warnings.
//
// The calls that collect warnings are [Node.Validate],
// [Source.ValidateDocuments], the Decode methods of [Node] and [Source],
// both SelfValidate methods, and [SelfValidateValue]. Each runs
// its checks with a context of its own, so the warnings of a [Validator],
// of a nested call, and of [Node.Warn] reach the call that runs them
// first. Calls in several goroutines that share a context each call fn,
// so fn guards its own state:
//
//	var (
//		mu     sync.Mutex
//		warned []error
//	)
//
//	ctx = niceyaml.WithWarningHandler(ctx, func(w error) {
//		mu.Lock()
//		defer mu.Unlock()
//
//		warned = append(warned, w)
//	})
//
// A later WithWarningHandler on a context derived from ctx replaces fn
// for the calls that run with the derived context.
func WithWarningHandler(ctx context.Context, fn func(error)) context.Context {
	return context.WithValue(ctx, warningKey{}, warningHandler(fn))
}

// reportWarning hands err to the warning handler of ctx, if it has one. A
// nil err hands nothing.
func reportWarning(ctx context.Context, err error) {
	if isNothing(err) {
		return
	}

	h, ok := ctx.Value(warningKey{}).(warningHandler)
	if ok && h != nil {
		h(err)
	}
}

// Warn reports err as a warning to the call that runs ctx, as
// [WithWarningHandler] describes. It marks err a warning, as [NewWarning]
// marks one, and binds it at n as [Node.Bind] binds an error, so an `@`
// path reads from n. A [Validator] reports each warning this way and
// returns its failures alone:
//
//	if port < 1024 {
//		n.Warn(ctx, niceyaml.NewWarning("a port below 1024 needs root",
//			niceyaml.AtPath(portPath)))
//	}
//
// The validator passes the context it got to each check and decode that it
// runs, or the warnings of that work go nowhere. A nil err warns nothing,
// and a context without a call or a handler drops the warning.
func (n *Node) Warn(ctx context.Context, err error) {
	if isNothing(err) {
		return
	}

	reportWarning(ctx, n.Bind(remark(err, severityWarning)))
}

// warningRow is one warning a [warningCollector] holds, with the source,
// the position, and the message that tell it apart.
type warningRow struct {
	err     error
	src     *Source
	msg     string
	pos     position.Position
	located bool
}

// same reports whether r and o share a source, a position, and a message.
func (r warningRow) same(o warningRow) bool {
	return r.src == o.src && r.located == o.located && r.pos == o.pos && r.msg == o.msg
}

// warningCollector holds the warnings of one call until the call knows its
// outcome. It is safe for concurrent use, so a validator may report from
// several goroutines.
type warningCollector struct {
	rows []warningRow
	mu   sync.Mutex
}

// context returns ctx with the collector as its warning handler.
func (c *warningCollector) context(ctx context.Context) context.Context {
	return context.WithValue(ctx, warningKey{}, warningHandler(c.add))
}

// add takes in each problem of err as a row, and keeps one of the rows
// that share a source, a position, and a message.
func (c *warningCollector) add(err error) {
	if isNothing(err) {
		return
	}

	for problem := range Problems(err) {
		row := warningRow{err: problem.Err, src: problem.Source(), msg: problem.Message()}
		row.pos, row.located = problem.Position()

		c.keep(row)
	}
}

// keep adds row unless the collector holds a row that shares its source,
// its position, and its message.
func (c *warningCollector) keep(row warningRow) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !slices.ContainsFunc(c.rows, row.same) {
		c.rows = append(c.rows, row)
	}
}

// warnings returns the warnings the collector holds, sorted by position
// within the order their sources first appear, with those that have no
// position last.
func (c *warningCollector) warnings() []error {
	c.mu.Lock()

	rows := slices.Clone(c.rows)

	c.mu.Unlock()

	var sources []*Source

	group := func(src *Source) int {
		i := slices.Index(sources, src)
		if i < 0 {
			i = len(sources)
			sources = append(sources, src)
		}

		return i
	}

	for _, r := range rows {
		group(r.src)
	}

	slices.SortStableFunc(rows, func(a, b warningRow) int {
		switch {
		case !a.located && !b.located:
			return 0
		case !a.located:
			return 1
		case !b.located:
			return -1
		}

		if ga, gb := group(a.src), group(b.src); ga != gb {
			return ga - gb
		}

		if a.pos.Line != b.pos.Line {
			return a.pos.Line - b.pos.Line
		}

		return a.pos.Col - b.pos.Col
	})

	out := make([]error, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.err)
	}

	return out
}

// finish ends a call that ran its checks under c, with err the result of
// the call, bound as the call binds it. A result that holds no error
// passes. Its warnings join those of c, which go to the handler of ctx as
// one error that bind binds, and finish returns nil. A failure returns
// with the warnings of c in it, as [warningCollector.merge] puts them.
func (c *warningCollector) finish(ctx context.Context, err error, bind func(error) error) error {
	if HasErrors(err) {
		return c.merge(err, bind)
	}

	c.add(err)

	ws := c.warnings()

	switch len(ws) {
	case 0:
	case 1:
		reportWarning(ctx, ws[0])
	default:
		reportWarning(ctx, bind(newTally(ws)))
	}

	return nil
}

// merge returns failure with the warnings of c in it. With no warning,
// failure comes back as it is. Otherwise one heading that counts the
// errors and the warnings stands over the problems of failure and the
// warnings, which bind puts in source order, in place of any heading that
// failure had.
func (c *warningCollector) merge(failure error, bind func(error) error) error {
	c.mu.Lock()

	held := len(c.rows) > 0

	c.mu.Unlock()

	if !held {
		return failure
	}

	var rows []error

	for problem := range Problems(failure) {
		if isWarning(problem.Err) {
			c.add(problem.Err)

			continue
		}

		rows = append(rows, problem.Err)
	}

	return bind(newTally(append(rows, c.warnings()...)))
}

// unbound is the bind of a call that returns its result unbound, as
// [ValidatorFunc.Check] does.
func unbound(err error) error {
	return err
}
