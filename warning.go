package niceyaml

import (
	"context"
	"errors"
	"iter"
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
// strict mode applies it to the report of a call. [Warnings.Report]
// builds that report from the error of the call and its warnings:
//
//	warnings := niceyaml.NewWarnings()
//
//	config, err := doc.Decode[Config](warnings.Context(ctx), niceyaml.WithValidator(reg))
//	if strict {
//		err = niceyaml.AsErrors(warnings.Report(err))
//	}
//
// AsErrors rebuilds the errors that niceyaml builds, such as an [*Error],
// a [*SourceError], and a join, and calls the AsErrors method of an error
// that has one, so the result of such an error keeps its type. A wrapper
// that [fmt.Errorf] builds with several %w verbs and that heads several
// problems gets a mark above each problem, as a join does. The result
// keeps the message of the wrapper and matches it for [errors.Is] and
// [errors.As]. AsErrors marks any other error above the warning it wraps.
// A program thus applies it to the error that [Warnings.Report] or
// [Warnings.Err] returns, before it wraps the error with [fmt.Errorf]. An
// err that holds no warning comes back as it is, and a nil err returns
// nil.
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
		kept:    e.kept,
		keptDoc: e.keptDoc,
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

// Warnings collects the warnings of the calls of niceyaml and its schema
// package that run with a context from [Warnings.Context]. The calls that
// collect warnings are [Node.Validate], [Source.ValidateDocuments], the
// Decode methods of [Node] and [Source], both SelfValidate methods,
// [SelfValidateValue], and
// [go.jacobcolvin.com/niceyaml/schema.Schema.ValidateValue]. Each call
// hands every warning of the checks it runs to the collector, whether the
// call passes or fails, and returns its errors alone:
//
//	warnings := niceyaml.NewWarnings()
//
//	config, err := doc.Decode[Config](warnings.Context(ctx), niceyaml.WithValidator(reg))
//	if err != nil {
//		return warnings.Report(err)
//	}
//
//	if w := warnings.Err(); w != nil {
//		log.Print(niceyaml.FormatError(w))
//	}
//
// A call that runs with no collector drops its warnings, whether it
// passes or fails. The collector keeps one of the warnings that share a
// source, a position, a path, and a message, so a deprecation that two
// checks report shows once. It is safe for concurrent use, so calls in
// several goroutines can share one. A nil *Warnings collects nothing and
// holds no warning.
//
// Create instances with [NewWarnings].
type Warnings struct {
	rows []warningRow
	mu   sync.Mutex
}

// NewWarnings creates a new [*Warnings] that holds no warning.
func NewWarnings() *Warnings {
	return &Warnings{}
}

// Context returns a copy of ctx that carries w, so each call of niceyaml
// that runs with the context hands its warnings to w. A [Validator]
// reports to w through [Node.Warn] with the context it gets.
//
// Outside a call of niceyaml, a later Context replaces w for the calls
// that run with the derived context. A hook, such as a Validator or a
// function of [WithValueCheck], gets its context from a call. On that
// context, a later Context hands each warning to the collectors of the
// call as well. So does each Context derived from the context it
// returns. A validator that reads the warnings of a check it runs
// collects them with a Warnings of its own, and the caller of the
// enclosing call still gets them. For a nil w, Context returns ctx as it
// is.
func (w *Warnings) Context(ctx context.Context) context.Context {
	if w == nil {
		return ctx
	}

	scope := &warningScope{w: w}

	if outer := scopeOf(ctx); outer != nil {
		switch {
		case outer.call:
			scope.outer = outer
		case outer.outer != nil:
			// The scope of ctx was derived inside a call, so the call takes
			// the warnings of w as well.
			scope.outer = outer.outer
		}
	}

	return context.WithValue(ctx, warningKey{}, scope)
}

// All returns an iterator over the warnings that w holds, one
// [ErrorTree] per warning. The warnings sort by position, within the
// order their sources first reached w, and the warnings with no position
// come last. Each tree holds the warning bound as the call bound it, with
// its details as its Children. A program reads the fields of each tree as
// it reads those of a node that [Problems] yields:
//
//	for warning := range warnings.All() {
//		pos, _ := warning.Position()
//		emit(warning.Name(), pos, warning.Message())
//	}
func (w *Warnings) All() iter.Seq[ErrorTree] {
	return func(yield func(ErrorTree) bool) {
		for _, r := range w.sorted() {
			if !yield(NewErrorTree(r.err)) {
				return
			}
		}
	}
}

// Err returns the warnings that w holds as one error, in the order
// [Warnings.All] yields them, or nil when w holds none. One warning comes
// back alone. Several come back under a heading that counts them, as in
// "cafe.yaml: 2 warnings". The heading binds to the source of the
// warnings when they share one, and to no document otherwise, as
// [BindValue] binds an error. Warnings that [Detach] detached bind their
// heading to no document. [Warnings.Report] puts the warnings beside the
// errors of a call that fails, and a program in strict mode turns either
// result into errors with [AsErrors].
func (w *Warnings) Err() error {
	return tallyRows(w.sorted())
}

// Report returns one error that lists the problems of err and the
// warnings of w together, sorted by position. A heading that counts both
// severities stands over them and binds as the heading of [Warnings.Err]
// binds. A renderer calls Report to show a failure as a compiler does,
// with each warning between the errors around it:
//
//	cafe.yaml: 2 errors, 1 warning
//	|-- 2:1: $.curency~: unknown field "curency"
//	|-- 3:1: warning: $.legacyPricing~: deprecated field "legacyPricing"
//	`-- 4:1: $.wifi~: unknown field "wifi"
//
// A warning of err that w holds as well shows once. Report builds a new
// error and leaves err as it is. The result keeps each problem of err for
// [errors.Is] and [errors.As], but not the headings and the wrappers above
// them. A program thus classifies the failure with err itself. With no
// warning in w, err comes back as it is. For a nil err, Report returns
// what [Warnings.Err] returns.
func (w *Warnings) Report(err error) error {
	held := w.sorted()
	if len(held) == 0 {
		return err
	}

	if isNothing(err) {
		return tallyRows(held)
	}

	rows := rowsOf(err)

	for _, r := range held {
		if !slices.ContainsFunc(rows, r.same) {
			rows = append(rows, r)
		}
	}

	return tallyRows(sortRows(rows))
}

// keep adds each of rows unless w holds a row that shares its source, its
// position, its path, and its message.
func (w *Warnings) keep(rows []warningRow) {
	w.mu.Lock()
	defer w.mu.Unlock()

	for _, row := range rows {
		if !slices.ContainsFunc(w.rows, row.same) {
			w.rows = append(w.rows, row)
		}
	}
}

// sorted returns a copy of the rows of w, sorted as [sortRows] sorts them.
// A nil w holds no row.
func (w *Warnings) sorted() []warningRow {
	if w == nil {
		return nil
	}

	w.mu.Lock()

	rows := slices.Clone(w.rows)

	w.mu.Unlock()

	return sortRows(rows)
}

// warningKey is the key of the [warningScope] on a context.
type warningKey struct{}

// warningScope is what a context carries for the calls that collect
// warnings: the collector that each of them hands its warnings to.
type warningScope struct {
	// The collector that takes every warning delivered on the context.
	w *Warnings
	// The scope of the enclosing call, which takes each warning as well.
	// It is set for a scope that [Warnings.Context] derived inside a call
	// and for the scopes that enterCall marks from one, and nil otherwise.
	outer *warningScope
	// A call of this package runs with the context, so a scope that
	// Warnings.Context derives from this one hands its warnings here too.
	call bool
}

// scopeOf returns the scope that ctx carries, or nil.
func scopeOf(ctx context.Context) *warningScope {
	s, _ := ctx.Value(warningKey{}).(*warningScope) //nolint:errcheck // A context with no scope reads as nil.

	return s
}

// enterCall returns ctx marked as the context of a call of this package
// that collects warnings, for a call that passes ctx to code of the
// program outside [runHook], as a decode passes it to each unmarshaler
// that takes a context. A collector that such code puts on the context
// then hands its warnings to the collectors of the call as well. A ctx
// with no collector comes back as it is.
func enterCall(ctx context.Context) context.Context {
	s := scopeOf(ctx)
	if s == nil || s.call {
		return ctx
	}

	return context.WithValue(ctx, warningKey{}, &warningScope{w: s.w, outer: s.outer, call: true})
}

// runHook runs a hook of a call, such as a [Validator] or a function of
// [WithValueCheck], and returns the errors and then the warnings of the
// result. The warnings include those that the hook hands to its context,
// a copy of ctx whose collector holds them and none of the collectors of
// ctx. The call thus binds every warning of the hook as it binds the
// result.
func runHook(ctx context.Context, run func(ctx context.Context) error) (error, error) {
	held := NewWarnings()

	failure, warnings := splitWarnings(run(context.WithValue(ctx, warningKey{}, &warningScope{w: held, call: true})))

	handed := held.Err()
	if handed == nil {
		return failure, warnings
	}

	if warnings == nil {
		return failure, handed
	}

	return failure, errors.Join(warnings, handed)
}

// deliver hands each problem of err, a warning bound as the call binds
// it, to every collector that ctx carries. A nil err hands nothing, and a
// ctx with no collector drops err.
func deliver(ctx context.Context, err error) {
	s := scopeOf(ctx)
	if s == nil || isNothing(err) {
		return
	}

	rows := rowsOf(err)

	for ; s != nil; s = s.outer {
		s.w.keep(rows)
	}
}

// splitWarnings returns the errors and then the warnings among the
// problems of err. An err with no warning among its problems comes back
// whole as the errors, and one with no error whole as the warnings. For
// an err that holds both, [splitBelow] keeps the join or the wrapper on
// top of the errors where it can. Otherwise each severity comes back as
// the join of its problems, in the order [Problems] yields them.
func splitWarnings(err error) (error, error) {
	var failure, warnings error

	switch {
	case isNothing(err):
	case !holdsWarning(err):
		failure = err
	case !HasErrors(err):
		warnings = err
	default:
		failure, warnings = splitBelow(err)
		if failure == nil {
			failure, warnings = splitProblems(err)
		}
	}

	return failure, warnings
}

// splitProblems returns the join of the errors and then the join of the
// warnings among the problems of err, in the order [Problems] yields
// them.
func splitProblems(err error) (error, error) {
	var errs, warns []error

	for problem := range Problems(err) {
		if isWarning(problem.Err) {
			warns = append(warns, problem.Err)
		} else {
			errs = append(errs, problem.Err)
		}
	}

	return multierr.Join(errs), multierr.Join(warns)
}

// splitBelow splits err, which holds both errors and warnings, below its
// top. It splits a join branch by branch, as [splitJoin] does, and a
// wrapper of one error below the wrapper, as [splitWrapper] does. An
// [*Error] and a [*SourceError] do not split. For an err that does not
// split, splitBelow returns a nil failure.
func splitBelow(err error) (error, error) {
	var failure, warnings error

	switch x := err.(type) { //nolint:errorlint // The node itself, not a chain search.
	case *Error, *SourceError, *markedError, *PanicError:
	case interface{ Unwrap() []error }:
		failure, warnings = splitJoin(err)
	case interface{ Unwrap() error }:
		failure, warnings = splitWrapper(err, x.Unwrap())
	}

	return failure, warnings
}

// splitJoin splits err branch by branch, as [splitWarnings] splits each
// branch, when err is a join. The errors keep the join over the branches
// that hold errors, and an [errors.Join] of one such branch reads as
// that branch. For an err that is no join, splitJoin returns a nil
// failure.
func splitJoin(err error) (error, error) {
	branches, _, ok := joinBranches(err)
	if !ok {
		return nil, nil //nolint:nilnil // A nil failure means that err does not split.
	}

	var errs, warns []error

	for _, branch := range branches {
		f, w := splitWarnings(branch)
		if f != nil {
			errs = append(errs, f)
		}

		if w != nil {
			warns = append(warns, w)
		}
	}

	if len(errs) == 0 || multierr.IsJoin(err) {
		return multierr.Join(errs), multierr.Join(warns)
	}

	return multierr.Rejoin(err, errs), multierr.Join(warns)
}

// splitWrapper splits wrapper, which wraps below alone, below it. The
// errors keep the wrapper as a stand-in that matches it, and whose text
// holds the errors of below in place of the text of below. For a wrapper
// whose text does not show below once, splitWrapper returns a nil
// failure.
func splitWrapper(wrapper, below error) (error, error) {
	failure, warnings := splitWarnings(below)
	if failure == nil || warnings == nil {
		return nil, nil //nolint:nilnil // A nil failure means that the wrapper does not split.
	}

	msg := wrapper.Error()

	text := replaceText(msg, below.Error(), failure.Error())
	if text == msg {
		return nil, nil //nolint:nilnil // A nil failure means that the wrapper does not split.
	}

	return multierr.Replace(wrapper, failure, text, nil), warnings
}

// Warn reports err as a warning to the collector of ctx, as [Warnings]
// describes. It marks err a warning, as [NewWarning] marks one, and binds
// it at n as [Node.Bind] binds an error, so an `@` path reads from n. A
// [Validator] reports each warning this way and returns its failures
// alone:
//
//	if port < 1024 {
//		n.Warn(ctx, niceyaml.NewWarning("a port below 1024 needs root",
//			niceyaml.AtPath(portPath)))
//	}
//
// The validator passes the context it got to each check and decode that it
// runs, or the warnings of that work go nowhere. A nil err warns nothing,
// and a context with no collector drops the warning.
func (n *Node) Warn(ctx context.Context, err error) {
	if isNothing(err) {
		return
	}

	deliver(ctx, n.Bind(remark(err, severityWarning)))
}

// warningRow is one problem that a [Warnings] holds or that
// [Warnings.Report] lists, with the source, the position, the path, and
// the message that tell it apart.
type warningRow struct {
	err error
	// The source of the problem, as [SourceError.origin] gives it, or nil
	// for a problem bound to no source.
	src     excerptSource
	msg     string
	path    string
	pos     position.Position
	located bool
}

// rowsOf returns a row for each problem of err, or one row for err itself
// when it holds no problem.
func rowsOf(err error) []warningRow {
	var rows []warningRow

	for problem := range Problems(err) {
		row := warningRow{err: problem.Err, src: problem.Bound.origin(), msg: problem.Message()}
		row.pos, row.located = problem.Position()

		if p, ok := problem.Path(); ok {
			row.path = p.String()
		}

		rows = append(rows, row)
	}

	if len(rows) == 0 {
		rows = append(rows, warningRow{err: err, msg: err.Error()})
	}

	return rows
}

// same reports whether r and o share a source, a position, a path, and a
// message.
func (r warningRow) same(o warningRow) bool {
	return r.src == o.src && r.located == o.located && r.pos == o.pos && r.path == o.path && r.msg == o.msg
}

// sortRows sorts rows by position within the order their sources first
// appear, with the rows that have no position last, and returns them.
func sortRows(rows []warningRow) []warningRow {
	var sources []excerptSource

	group := func(src excerptSource) int {
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

	return rows
}

// tallyRows returns the errors of rows as one error. It returns nil for
// no row, the error of a lone row, and otherwise a heading that counts
// the errors and the warnings among them. The heading binds to the source
// of the rows when every row has that source, and to no document
// otherwise, as [BindValue] binds an error. Rows that [Detach] detached
// have no Source to bind to, so their heading binds to no document too.
func tallyRows(rows []warningRow) error {
	switch len(rows) {
	case 0:
		return nil
	case 1:
		return rows[0].err
	}

	src := rows[0].src
	errs := make([]error, 0, len(rows))

	for _, r := range rows {
		errs = append(errs, r.err)

		if r.src != src {
			src = nil
		}
	}

	tally := newTally(errs)

	if bound, ok := src.(*Source); ok && bound != valueSource() {
		return bound.Bind(tally)
	}

	return BindValue(tally)
}
