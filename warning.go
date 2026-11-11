package niceyaml

import (
	"context"
	"errors"
	"iter"
	"slices"
	"strings"
	"sync"
	"weak"

	"go.jacobcolvin.com/niceyaml/internal/multierr"
	"go.jacobcolvin.com/niceyaml/internal/quantity"
	"go.jacobcolvin.com/niceyaml/position"
)

// severity is the mark that decides whether a problem is an error or a
// warning. A problem that does not carry a mark on its cause chain is an
// error.
type severity uint8

// The severity values are the marks that a problem carries. The value
// severityNone is not a mark, so the mark further down the chain decides.
// [NewWarning] and [Node.Warn] mark a warning, and [AsErrors] marks an
// error above each warning.
const (
	severityNone severity = iota
	severityWarning
	severityError
)

// NewWarning creates a new [*Error] with the given message that is a
// warning rather than an error. It takes the options of [NewError]. A
// warning thus carries a path, a position, a range, and details, and
// binds to a [Source] as an error does. The document is at fault for a
// warning as it is for an error from NewError, so [IsInvalid] reports a
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

// severityOf returns the nearest severity mark on the cause chain of err.
// Callers read it for one problem. A wrapper with several branches is a
// warning when any of its branches is. The branches of a wrapper that is
// one problem are that problem and sentinels, which do not carry a mark,
// so the problem decides. The walk stops at a sealed error and at a
// [*PanicError], and returns severityNone for either.
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

// marksWarning reports whether err or any error below it carries the
// mark of a warning. The errors below err include its details and its
// bindings. A false answer means that every problem of err is an error,
// so a caller skips the tree.
func marksWarning(err error) bool {
	switch x := err.(type) { //nolint:errorlint // Walks the tree one node at a time.
	case nil:
		return false

	case *SourceError:
		return x.marksWarning()

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

// marksWarning reports whether e, the error it binds, or a child of e
// carries the mark of a warning, as [marksWarning] reports it. It returns
// false for a nil e. It finds the answer a single time, because the error
// that e binds can hold a binding that is a child of e as well. A search
// per call walks such a binding twice, so its time doubles with each
// level of a nest of them.
func (e *SourceError) marksWarning() bool {
	if e == nil {
		return false
	}

	e.markedOnce.Do(func() {
		e.marked = marksWarning(e.err) ||
			slices.ContainsFunc(e.below, func(c boundChild) bool { return c.bound.marksWarning() })
	})

	return e.marked
}

// HasErrors reports whether err holds an error, as opposed to warnings
// alone. It reports false for a nil err and for a nil [*Error] or
// [*SourceError] pointer. It also reports false for an error whose every
// problem is a warning, as [NewWarning] and [Node.Warn] mark one. It
// reports true for any other error, including one that does not hold a
// problem, such as an error with an empty message. The problems are the
// nodes that [Problems] yields.
//
// A Validate method returns its warnings beside its errors, so code that
// calls one itself asks HasErrors whether the method failed:
//
//	if err := cfg.Validate(); niceyaml.HasErrors(err) {
//		return err
//	}
//
// A ValidateContext method of a [SelfValidatorContext] returns nil for a
// value that passes when each of its warnings comes from a nested call
// that gets its context, such as [SelfValidateValue]. Such a call hands
// its warnings to that context. A ValidateContext returns each warning
// that it raises itself beside its errors, so its callers ask HasErrors
// too.
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
// warning of err becomes an error. The mark leaves the text of each
// problem as it was, without the "warning:" label. Each heading that
// niceyaml wrote to count the errors and the warnings below it counts
// them again, so "2 errors, 1 warning" reads "3 errors". A program in
// strict mode applies AsErrors to the report of a call. [Warnings.Report]
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
// a [*SourceError], and a join. It calls the AsErrors method of an error
// that has one, so the result of such an error keeps its type.
//
// A wrapper that heads several problems gets a mark above each problem,
// as a join does. Such wrappers include one that [fmt.Errorf] builds
// around a join and one that it builds with several %w verbs. AsErrors
// rebuilds each error that the wrapper wraps. When the message of such an
// error appears exactly once in the message of the wrapper, the text of
// the rebuilt error takes its place in the message of the result.
// Otherwise the message stays as the wrapper wrote it. The result matches
// the wrapper for [errors.Is] and [errors.As].
//
// AsErrors marks any other error above the warning it wraps, and the
// message of the result is the message of that error. The message of a
// wrapper around one bound warning thus keeps the "warning:" label of the
// binding. A program applies AsErrors to the error that [Warnings.Report]
// or [Warnings.Err] returns, before it wraps the error with [fmt.Errorf].
// AsErrors returns an err that does not hold a warning as it is, and
// returns nil for a nil err.
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

// WithoutWarnings returns the errors of err without its warnings, as a
// check boundary returns them. [Node.Validate], a decode, and the other
// calls that collect warnings apply it to the result of each check they
// run, and hand the warnings to the [Warnings] collector on the context.
// WithoutWarnings returns an err that does not hold a warning as it is,
// and nil for an err that holds only warnings or for a nil err.
//
// For an err that holds both, WithoutWarnings keeps each join that
// [errors.Join] builds and each wrapper of one %w verb that [fmt.Errorf]
// builds. It removes the warnings below them. It calls the
// WithoutWarnings method of an error that holds both and has one, so the
// result of such an error keeps its type. WithoutWarnings looks for the
// method on err itself, on each branch of those joins, and on the error
// that each of those wrappers wraps:
//
//	func (e *RuleSetError) WithoutWarnings() error {
//		var fails []error
//		for _, f := range e.Errs {
//			if f = niceyaml.WithoutWarnings(f); f != nil {
//				fails = append(fails, f)
//			}
//		}
//
//		return &RuleSetError{Set: e.Set, Errs: fails}
//	}
//
// The method applies WithoutWarnings to the errors it holds, not to its
// receiver, because WithoutWarnings calls the method again. WithoutWarnings
// uses the result of the method only when that result holds an error and
// does not hold a warning. For a result that does not hold an error or
// that still holds a warning, such as nil or the receiver, WithoutWarnings
// splits the error as it splits an error without the method.
//
// Any other error that holds both becomes the join of its errors. Such
// errors include a summary from [NewSummary] and an error that [Node.Bind]
// or a [Source] has bound, even when the bound error has the method. That
// join drops the heading and the type of the error, so [errors.As] does
// not find that type in the result. Each error of the join keeps the text
// of the wrappers above it as its [ErrorTree.Context].
func WithoutWarnings(err error) error {
	failure, _ := splitWarnings(err) //nolint:errcheck // The warnings are the second error, which the call drops.

	return failure
}

// remark returns err with sev marked above each of its problems that
// carries another severity. A problem that does not carry a mark counts
// as an error here. It rebuilds each heading over its problems, and
// counts the problems of a tally again.
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

	w, ok := err.(interface{ Unwrap() error }) //nolint:errorlint // The node itself, not a chain search.
	if ok && heads(err) {
		inner := w.Unwrap()
		remarked := remark(inner, sev)

		return multierr.Replace(err, remarked, replaceText(err.Error(), inner.Error(), remarked.Error()), nil)
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
// Errors and the wrappers on top of it. Such an err is a join, a summary,
// a binding of either, a wrapper with several %w verbs that heads several
// problems, or a wrapper around any of them. A wrapper with one problem
// among its branches reads as that problem.
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

		case interface{ Unwrap() error }:
			cur = x.Unwrap()

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
	c := e.copied()

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

// copied returns a copy of e that builds its own texts, so a caller can
// change the error that the copy binds.
func (e *SourceError) copied() *SourceError {
	return &SourceError{
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
}

// retally returns err with each tally along its chain of Errors rebuilt.
// The problems of each rebuilt tally carry sev, and the tally counts them
// again.
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
// It skips each nil member.
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
// members, as "2 errors, 1 warning". It names only the severities that
// the members carry.
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
// warning". It leaves out a count of zero, but two zeros read "0 errors".
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
// Decode methods of [Node] and [Source], [Node.SelfValidate],
// [Source.SelfValidate], [SelfValidateValue], and
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
// A call whose context does not carry a collector drops its warnings,
// whether it passes or fails. A decode under [UnknownFieldsWarn] is the
// exception. It returns an error that wraps [ErrNoWarnings] before
// anything runs.
//
// [go.jacobcolvin.com/niceyaml/manifest.Registry.Load] and
// [go.jacobcolvin.com/niceyaml/manifest.Registry.Check] hand each warning
// that a load keeps to the collector as well. One collector thus gathers
// the warnings of the decodes and the loads of a program.
//
// Of the warnings whose rows read alike, the collector keeps one. Two rows
// read alike when they share a source, a place, and the message that the
// row shows behind its location. The place of a warning is its position
// and its path. In a file of several documents, the row of a warning
// without a position names the document that binds it, as "document 2".
// That document is part of the place too.
//
// A deprecation that two checks report thus shows once. A warning at one
// position shows once as well, whichever document of the file reports it.
// A warning without a position shows once for each document that reports
// it.
//
// The row of a warning holds the text of a wrapper that holds that warning
// alone, as in "lint: old spec". Two such wrappers with different texts
// around one warning thus keep a row each. A wrapper that heads several
// warnings does not write its text in their rows, so each of those rows
// reads as "old spec". Two such wrappers with different texts over one
// warning keep one row for it. That row keeps the [ErrorTree.Context] of
// the warning that reached the collector first.
//
// A copy that [Detach] detached shares the source and the place of the
// warning it copies. Of the two, the collector keeps the warning,
// whichever reaches it first, so [Warnings.All] reports its Source and
// [Annotate] marks it.
//
// A Warnings is safe for concurrent use, so calls in several goroutines
// can share one. A nil *Warnings does not collect or hold any warning.
//
// Create instances with [NewWarnings].
type Warnings struct {
	rows []warningRow
	mu   sync.Mutex
}

// NewWarnings creates a new, empty [*Warnings].
func NewWarnings() *Warnings {
	return &Warnings{}
}

// Context returns a copy of ctx that carries w, so each call of niceyaml
// that runs with the context hands its warnings to w. A [Validator]
// reports to w through [Node.Warn] with the context it gets. For a nil w,
// Context returns ctx as it is.
//
// Outside a call of niceyaml, a later Context replaces w for the calls
// that run with the derived context. A hook gets its context from a call.
// Validators, functions of [WithValueCheck], and ValidateContext methods
// of a [SelfValidatorContext] are hooks. On the context of a hook, a later
// Context hands each warning to the collectors of the call as well. So
// does each Context derived from the context it returns. A validator that
// reads the warnings of a check it runs collects them with a Warnings of
// its own. The caller of the enclosing call still gets them.
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
// order their sources first reached w, and the warnings that do not carry
// a position come last. Each tree holds the warning bound as the call
// bound it, with its details as its Children. A wrapper around several
// warnings is a heading, which w does not hold. Each of those warnings
// keeps the text of the wrapper as its [ErrorTree.Context]. A program reads
// the fields of each tree as it reads those of a node that [Problems]
// yields:
//
//	for warning := range warnings.All() {
//		pos, _ := warning.Position()
//		emit(warning.Name(), pos, warning.Message())
//	}
//
// A report in JSON encodes each tree, as [ErrorTree.MarshalJSON]
// describes.
func (w *Warnings) All() iter.Seq[ErrorTree] {
	return func(yield func(ErrorTree) bool) {
		rows := w.sorted()

		for i := range rows {
			if !yield(NewErrorTree(rows[i].err)) {
				return
			}
		}
	}
}

// Err returns the warnings that w holds as one error, in the order
// [Warnings.All] yields them, or nil when w does not hold a warning. Err
// returns one warning alone, and several under a heading that counts
// them, as in "cafe.yaml: 2 warnings". The heading binds to the source of
// the warnings when they share one, and otherwise [BindValue] binds it
// outside any document. Warnings that [Detach] detached do not keep a
// Source, so BindValue binds their heading too. [Warnings.Report] puts
// the warnings beside the errors of a call that fails, and a program in
// strict mode turns either result into errors with [AsErrors].
func (w *Warnings) Err() error {
	return tallyRows(w.sorted())
}

// Report returns one error that lists the problems of err and the
// warnings of w together, sorted by position. It returns nil only when err
// is nil and w does not hold a warning. A heading that counts both
// severities stands over them and binds as the heading of [Warnings.Err]
// binds. A renderer calls Report to show a failure as a compiler does,
// with each warning between the errors around it:
//
//	cafe.yaml: 2 errors, 1 warning
//	|-- 2:1: $.curency~: unknown field "curency"
//	|-- 3:1: warning: $.legacyPricing~: deprecated field "legacyPricing"
//	`-- 4:1: $.wifi~: unknown field "wifi"
//
// For a nil err, Report returns what [Warnings.Err] returns, so the report
// of a call that passes with warnings is not nil. A program that keeps the
// report of each call, as a check of many files does, calls Report
// whether or not err is nil. It reads err, or asks [HasErrors] of the
// report, to tell a failure from a pass:
//
//	err := source.ValidateDocuments(warnings.Context(ctx), reg)
//	if report := warnings.Report(err); report != nil {
//		kept = append(kept, report)
//	}
//
// A warning of err that w holds as well shows once, with the text of each
// wrapper around it in err. So does a warning that w holds and that a
// [Locate], [Invalid], or [Unchecked] result in err gives its location
// to. It shows at that location, as err shows it. A copy in err that
// [Detach] detached counts as the warning it copies.
//
// Report builds a new error and leaves err as it is. The result keeps
// each problem of err for [errors.Is] and [errors.As], but not the
// headings and the wrappers above them. A program thus classifies the
// failure with err itself. Each problem keeps the text of those wrappers
// as its [ErrorTree.Context], and the rows of the result do not show that
// text. When w does not hold a warning, Report returns err as it is.
func (w *Warnings) Report(err error) error {
	held := w.sorted()
	if len(held) == 0 {
		return err
	}

	if isNothing(err) {
		return tallyRows(held)
	}

	rows := rowsOf(err)
	shown := append(slices.Clone(rows), locatedCopies(err)...)

	for i := range held {
		if indexRow(shown, held[i].copies) < 0 {
			rows = append(rows, held[i])
		}
	}

	return tallyRows(sortRows(rows))
}

// keep adds each of rows unless w holds a row that reads alike, as
// [warningRow.same] reports. Of two such rows, w keeps the row of a live
// [Source]. A copy that [Detach] detached thus gives way to the warning
// it copies. Otherwise w keeps the row that it holds, with the context
// that the error of that row carries.
func (w *Warnings) keep(rows []warningRow) {
	w.mu.Lock()
	defer w.mu.Unlock()

	for i := range rows {
		at := indexRow(w.rows, rows[i].same)

		switch {
		case at < 0:
			w.rows = append(w.rows, rows[i])
		case rows[i].live() && !w.rows[at].live():
			w.rows[at] = rows[i]
		}
	}
}

// sorted returns a copy of the rows of w, sorted as [sortRows] sorts them.
// A nil w does not hold a row.
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
	// The collector that takes every warning that a call delivers on the
	// context.
	w *Warnings
	// The scope of the enclosing call, which takes each warning as well.
	// [Warnings.Context] sets it on a scope that it derives inside a call.
	// [enterCall] keeps it on the scope that it marks from such a scope. It
	// is nil otherwise.
	outer *warningScope
	// Whether a call of this package runs with the context. When it is
	// set, a scope that Warnings.Context derives from this one hands its
	// warnings to this one too.
	call bool
}

// scopeOf returns the scope that ctx carries, or nil.
func scopeOf(ctx context.Context) *warningScope {
	s, _ := ctx.Value(warningKey{}).(*warningScope) //nolint:errcheck // A context with no scope reads as nil.

	return s
}

// enterCall returns ctx marked as the context of a call of this package
// that collects warnings. A call passes the result to code of the program
// that runs outside [runHook], as a decode passes it to each unmarshaler
// that takes a context. A collector that such code puts on the context then
// hands its warnings to the collectors of the call as well. For a ctx
// that does not carry a collector, enterCall returns ctx as it is.
func enterCall(ctx context.Context) context.Context {
	s := scopeOf(ctx)
	if s == nil || s.call {
		return ctx
	}

	return context.WithValue(ctx, warningKey{}, &warningScope{w: s.w, outer: s.outer, call: true})
}

// withoutWarnings returns a copy of ctx that does not carry a collector,
// so a call that runs with it drops its warnings. The copy keeps the
// deadline, the cancellation, and the other values of ctx. A step that
// decodes a value the decoder already decoded runs that decode with it.
// The search for the problems of a decode is such a step. So are the
// locator of an error without a token, the fill of the defaults, and the
// walk that spells the map keys. The collectors of ctx thus do not get the
// warnings that an unmarshaler hands to its context again.
func withoutWarnings(ctx context.Context) context.Context {
	if scopeOf(ctx) == nil {
		return ctx
	}

	return context.WithValue(ctx, warningKey{}, (*warningScope)(nil))
}

// takesWarnings reports whether a call that runs with ctx hands its
// warnings somewhere on purpose. That holds for a ctx that carries a
// collector. It also holds for a copy from [withoutWarnings]. Such a copy
// drops the warnings of a decode that niceyaml runs again. It does not
// hold for a ctx that never carried a collector.
func takesWarnings(ctx context.Context) bool {
	// The copy from withoutWarnings holds a nil *warningScope, which reads
	// as a value of the key, unlike a context without the key.
	return ctx.Value(warningKey{}) != nil
}

// isolate returns a copy of ctx whose collector is w alone, marked as the
// context of a call. The collectors of ctx do not get the warnings of a
// call that runs with the copy. A collector that code derives from the
// copy with [Warnings.Context] hands its warnings to w as well.
func isolate(ctx context.Context, w *Warnings) context.Context {
	return context.WithValue(ctx, warningKey{}, &warningScope{w: w, call: true})
}

// runHook runs a hook of a call and returns the errors and then the
// warnings of the result. The hooks of a call include a [Validator] and a
// function of [WithValueCheck]. The warnings include those that the hook
// hands to its context. That context is a copy of ctx whose collector
// holds those warnings, and the collectors of ctx do not get them. The
// call thus binds every warning of the hook as it binds the result.
func runHook(ctx context.Context, run func(ctx context.Context) error) (error, error) {
	held := NewWarnings()

	failure, warnings := splitWarnings(run(isolate(ctx, held)))

	handed := held.Err()
	if handed == nil {
		return failure, warnings
	}

	if warnings == nil {
		return failure, handed
	}

	return failure, errors.Join(warnings, handed)
}

// deliver hands each problem of err to every collector that ctx carries.
// The error err is a warning bound as the call binds it. For a nil err,
// deliver does nothing, and a ctx that does not carry a collector drops
// err.
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
// problems of err. When err does not hold a warning, splitWarnings
// returns it whole as the errors. When err does not hold an error, it
// returns err whole as the warnings. For an err that holds both and has a
// WithoutWarnings method, the errors are the result of that method when
// [splitSelf] accepts it. The warnings are those that [splitProblems]
// finds in err. For any other err that holds both, [splitBelow] keeps
// the join or the wrapper on top of the errors when it can split err.
// Otherwise splitWarnings returns each severity as the join of its
// problems, as splitProblems returns them.
//
// The warnings of a branch that does not hold an error thus stay below
// the headings of that branch, as they do in a result that holds warnings
// alone. A call binds and sorts the warnings of a hook as one error. The
// rows of those warnings thus keep one order, whatever the errors beside
// them.
func splitWarnings(err error) (error, error) {
	var failure, warnings error

	switch {
	case isNothing(err):
	case !holdsWarning(err):
		failure = err
	case !HasErrors(err):
		warnings = err
	default:
		failure = splitSelf(err)
		if failure != nil {
			_, warnings = splitProblems(err) //nolint:errcheck // The method gives the errors.

			return failure, warnings
		}

		failure, warnings = splitBelow(err)
		if failure == nil {
			failure, warnings = splitProblems(err)
		}
	}

	return failure, warnings
}

// splitSelf returns the result of the WithoutWarnings method of err, for
// an err that holds both errors and warnings. It returns nil when err does
// not have the method, or when the result of the method does not hold an
// error or still holds a warning. The split of err then goes on as for an
// error without the method.
func splitSelf(err error) error {
	x, ok := err.(interface{ WithoutWarnings() error }) //nolint:errorlint // The node itself, not a chain search.
	if !ok {
		return nil
	}

	failure := x.WithoutWarnings()
	if !HasErrors(failure) || holdsWarning(failure) {
		return nil
	}

	return failure //nolint:wrapcheck // The error rebuilds itself and keeps its type.
}

// splitProblems returns the join of the errors and then the join of the
// warnings among the problems of err, in the order [Problems] yields
// them. The join leaves out the headings above each problem. Each problem
// thus carries the text of the wrappers of those headings, as
// [underHeadings] puts that text on an error, and keeps its context.
func splitProblems(err error) (error, error) {
	var errs, warns []error

	for problem := range Problems(err) {
		kept := underHeadings(problem.Err, problem.above)

		if isWarning(problem.Err) {
			warns = append(warns, kept)
		} else {
			errs = append(errs, kept)
		}
	}

	return multierr.Join(errs), multierr.Join(warns)
}

// splitBelow splits err below its top, for an err that holds both errors
// and warnings. It splits a join that [errors.Join] builds and a join of
// a wrapper with several %w verbs branch by branch, as [splitJoin] does.
// It splits a wrapper that [fmt.Errorf] builds with one %w verb, and the
// copy of one that [Detach] builds, below the wrapper, as [splitWrapper]
// does. It does not split an error of any other type. Any stand-in for
// such an error matches the error as the check wrote it, with its warnings.
// For such an err, splitBelow thus builds no stand-in and returns a nil
// failure.
func splitBelow(err error) (error, error) {
	var failure, warnings error

	switch {
	case multierr.IsJoin(err), multierr.IsWrapErrors(err):
		failure, warnings = splitJoin(err)
	case multierr.IsWrap(err):
		failure, warnings = splitWrapper(err, errors.Unwrap(err))
	}

	return failure, warnings
}

// splitJoin splits err branch by branch when err is a join, as
// [splitWarnings] splits each branch. The errors keep the join over the
// branches that hold errors, and an [errors.Join] of one such branch
// reads as that branch. The warnings are the join of the warnings of each
// branch. For an err that is not a join, splitJoin returns a nil failure.
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

// splitWrapper splits err below it. The wrapper err wraps below alone.
// The errors keep a stand-in that matches err. The text of the stand-in
// holds the errors of below in place of the text of below. The warnings
// of below do not keep err above them. Each of them thus carries the text
// that err wrote around below, as [underHeadings] puts that text on an
// error, and keeps its context. For an err whose text does not show the
// text of below exactly one time, splitWrapper returns a nil failure.
func splitWrapper(err, below error) (error, error) {
	failure, warnings := splitWarnings(below)
	if failure == nil || warnings == nil {
		return nil, nil //nolint:nilnil // A nil failure means that the wrapper does not split.
	}

	msg, inner := err.Error(), below.Error()
	if inner == "" || strings.Count(msg, inner) != 1 {
		return nil, nil //nolint:nilnil // A nil failure means that the wrapper does not split.
	}

	text := replaceText(msg, inner, failure.Error())
	added := wrapperText{}.around([]wrapper{{err: err, next: below}})

	return multierr.Replace(err, failure, text, nil), underHeadings(warnings, added)
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
// runs. Otherwise the collectors do not get the warnings of that work.
// Warn does nothing for a nil err, and a context that does not carry a
// collector drops the warning.
func (n *Node) Warn(ctx context.Context, err error) {
	if isNothing(err) {
		return
	}

	deliver(ctx, n.Bind(remark(err, severityWarning)))
}

// warningRow is one problem that a [Warnings] holds or that
// [Warnings.Report] lists. It holds the source, the place, and the message
// that identify the problem.
type warningRow struct {
	// The error of the problem. It carries the text of the wrappers of the
	// headings that stood above the problem, as [underHeadings] puts that
	// text on an error. A tree of it thus reports that text as its
	// context, and the row of the problem does not show it.
	err error
	// The source of the problem, as [SourceError.origin] gives it, or nil
	// for a problem that is not bound to a source.
	src excerptSource
	// The weak pointer that src gives as its identity, or a nil pointer
	// for a row without a source. [warningRow.sameSource] compares rows
	// through it, so a comparison does not make a weak pointer.
	id weak.Pointer[Source]
	// The message that the row of the problem shows behind its location,
	// as [ErrorTree.rowMessage] returns it. It holds the text of each
	// wrapper that holds the problem alone, and not the text of a wrapper
	// that headed several problems.
	msg string
	// The message of the binding of the problem, without the text that a
	// wrapper around the binding adds, as [repeatKey] reads it. A binding
	// that adopted such a wrapper gives the message of the binding inside
	// the wrapper, as [SourceError.innermost] finds it. A problem without a
	// binding gives the message that its row shows.
	bound string
	path  string
	// The document that the message of the problem names, as "document
	// 2", or "" for a message that does not name one, as
	// [SourceError.documentLabel] gives it.
	doc     string
	pos     position.Position
	located bool
}

// rowsOf returns a row for each problem of err, or one row for err itself
// when it does not hold a problem.
func rowsOf(err error) []warningRow {
	var rows []warningRow

	for problem := range Problems(err) {
		row := sourceRow(problem.Bound.origin())
		row.err = underHeadings(problem.Err, problem.above)
		row.msg = problem.rowMessage()

		row.bound = row.msg
		if problem.Bound != nil {
			row.bound = problem.Bound.innermost().Message()
			row.doc = problem.Bound.documentLabel()
		}

		row.pos, row.located = problem.Position()

		if p, ok := problem.Path(); ok {
			row.path = p.String()
		}

		rows = append(rows, row)
	}

	if len(rows) == 0 {
		rows = append(rows, warningRow{err: err, msg: err.Error(), bound: err.Error()})
	}

	return rows
}

// locatedCopies returns a row for each binding that a [Locate],
// [Invalid], or [Unchecked] result among the problems of err gave its
// location to. The row of the problem shows that binding at the location
// of the result. A warning of a collector that such a row copies thus
// shows in err already.
func locatedCopies(err error) []warningRow {
	var rows []warningRow

	for problem := range Problems(err) {
		if problem.Bound != nil {
			rows = locatedRows(rows, problem.Bound)
		}
	}

	return rows
}

// locatedRows appends to rows a row for each binding that a located
// result gave its location to, along the chain of bindings that starts at
// b, outermost first. Such a binding stands at the end of the chain of the
// binding of the result, which did not adopt it. A row holds what
// [warningRow.copies] compares.
func locatedRows(rows []warningRow, b *SourceError) []warningRow {
	for {
		outer := b.innermost()
		if outer.adopted {
			return rows
		}

		inner, ok := outer.end.err.(*SourceError) //nolint:errorlint // The end of the chain, found by the walk.
		if !ok {
			return rows
		}

		row := sourceRow(inner.origin())
		row.bound = inner.innermost().Message()
		row.pos, row.located = inner.Position()
		row.doc = inner.documentLabel()

		if p, ok := inner.Path(); ok {
			row.path = p.String()
		}

		rows = append(rows, row)
		b = inner
	}
}

// indexRow returns the index of the first of rows that match reports true
// for, or -1 when none does. It hands match a pointer to each row, so a
// search through many rows does not copy them.
func indexRow(rows []warningRow, match func(*warningRow) bool) int {
	for i := range rows {
		if match(&rows[i]) {
			return i
		}
	}

	return -1
}

// sourceRow returns a row with the source src and its identity. A nil src
// gives a row without a source.
func sourceRow(src excerptSource) warningRow {
	if src == nil {
		return warningRow{}
	}

	return warningRow{src: src, id: src.identity()}
}

// same reports whether r and o read alike. They do when they share a
// place, as [warningRow.samePlace] reports it, and the message that each
// row shows. The comparison leaves out the text that a row carries from a
// heading, because a row does not show that text. Two rows that match thus
// print one line, and two rows that print different lines do not match.
func (r *warningRow) same(o *warningRow) bool {
	return r.samePlace(o) && r.msg == o.msg
}

// copies reports whether r and o share a place, as [warningRow.samePlace]
// reports it, and the message of their binding. A warning and a wrapped
// copy of it thus match, and so do a warning and a copy that [Detach]
// detached.
func (r *warningRow) copies(o *warningRow) bool {
	return r.samePlace(o) && r.bound == o.bound
}

// samePlace reports whether r and o share a source and a place. They
// share a source as [warningRow.sameSource] reports it. A place is a
// position, a path, and the document that the message names. Only a
// message without a position names a document. The document that binds a
// warning at a position thus does not set two rows apart. A copy that
// [Detach] detached keeps the label of its document, so it shares the
// place of the warning it copies.
func (r *warningRow) samePlace(o *warningRow) bool {
	return r.sameSource(o) && r.doc == o.doc && r.located == o.located && r.pos == o.pos && r.path == o.path
}

// live reports whether r comes from a live [Source], and not from what a
// copy that [Detach] detached kept of one.
func (r *warningRow) live() bool {
	return r.src != nil && r.src.live() != nil
}

// sameSource reports whether r and o come from one source. They do when
// both lack a source, and when their sources are texts of one [Source], as
// [sameSource] reports it.
func (r *warningRow) sameSource(o *warningRow) bool {
	return r.id == o.id
}

// sortRows sorts rows by position within the order their sources first
// appear, and returns them. The rows that do not have a position come
// last.
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

	for i := range rows {
		group(rows[i].src)
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
// an empty slice, and the error of the row for a lone row. Otherwise it
// returns a heading that counts the errors and the warnings among them.
// The heading binds to the source of the rows when every row has that
// source, and otherwise [BindValue] binds it outside any document. Rows
// that [Detach] detached do not keep a Source, so BindValue binds their
// heading too.
func tallyRows(rows []warningRow) error {
	switch len(rows) {
	case 0:
		return nil
	case 1:
		return rows[0].err
	}

	src := rows[0].src
	errs := make([]error, 0, len(rows))

	for i := range rows {
		errs = append(errs, rows[i].err)

		if rows[i].src != src {
			src = nil
		}
	}

	tally := newTally(errs)

	if bound, ok := src.(*Source); ok && bound != valueSource() {
		return bound.Bind(tally)
	}

	return BindValue(tally)
}
