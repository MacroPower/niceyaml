package manifest

import (
	"errors"
	"fmt"
	"slices"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/hook"
	"go.jacobcolvin.com/niceyaml/paths"
)

// Each problem that a load, a plan, or Apply reports matches one of these
// sentinels with [errors.Is], so a program branches on the cause without
// reading the message. [niceyaml.IsInvalid] reports each problem of a load,
// because the files that the user wrote cause them.
var (
	// ErrEncoding indicates a file that is not UTF-8, such as one with a
	// byte that UTF-8 does not allow or one in UTF-16.
	ErrEncoding = errors.New("text is not UTF-8")

	// ErrOutsideLink indicates a symbolic link that a directory walk found,
	// whose target lies outside the input directory or does not match the
	// include patterns.
	ErrOutsideLink = errors.New("link leads outside the input directory")

	// ErrMissingTypeField indicates a document without one of the type
	// fields, or without both.
	ErrMissingTypeField = errors.New("missing type field")

	// ErrFieldAlias indicates a type field or a name whose path passes
	// through an alias or a `<<` merge key.
	ErrFieldAlias = errors.New("field comes from an alias")

	// ErrMixedLayers indicates the layers of a merged document that write
	// different text in a type field.
	ErrMixedLayers = errors.New("layers declare different type fields")

	// ErrUnknownKind indicates a kind that the registry does not hold.
	ErrUnknownKind = errors.New("unknown kind")

	// ErrUnknownVersion indicates a version that the kind does not have.
	ErrUnknownVersion = errors.New("unknown version")

	// ErrKindMismatch indicates a document that pairs a version with a
	// kind name that the version does not give.
	ErrKindMismatch = errors.New("version has no such kind name")

	// ErrRemovedVersion indicates a version that [WithRemoved] names.
	ErrRemovedVersion = errors.New("removed version")

	// ErrMissingName indicates a manifest without a name that is a
	// non-empty string, in a registry that reads names.
	ErrMissingName = errors.New("missing name")

	// ErrDuplicateName indicates two manifests of one kind with one name.
	ErrDuplicateName = errors.New("duplicate name")

	// ErrMissingManifest indicates a kind that [Required] marks and that the
	// set lacks.
	ErrMissingManifest = errors.New("missing manifest")

	// ErrExtraManifest indicates a second manifest of a kind that
	// [Singleton] marks.
	ErrExtraManifest = errors.New("more than one manifest")

	// ErrReferenceEdit indicates a document whose steps edit a reference
	// document, which a rewrite cannot change.
	ErrReferenceEdit = errors.New("steps edit a reference document")

	// ErrReadOnly indicates a file that Apply cannot rewrite.
	ErrReadOnly = errors.New("read-only file")

	// ErrChanged indicates a file that changed or went away after the plan
	// read it.
	ErrChanged = errors.New("file changed")

	// ErrDeprecated indicates the warning about a deprecated version, kind,
	// field, or value.
	ErrDeprecated = errors.New("deprecated")

	// ErrRemoved indicates the warning about a value that a step removed.
	ErrRemoved = errors.New("removed by a migration")
)

// problemError is the error of a problem that a load writes itself: its
// message, and the errors it matches beside it.
type problemError struct {
	msg   string
	marks []error
}

// Error returns the message of e.
func (e *problemError) Error() string {
	return e.msg
}

// Is reports whether target matches one of the marks of e.
func (e *problemError) Is(target error) bool {
	return slices.ContainsFunc(e.marks, func(m error) bool { return errors.Is(m, target) })
}

// newProblem returns the problem msg, which matches mark, at the location
// of opts. The document is at fault for it.
func newProblem(mark error, msg string, opts ...niceyaml.ErrorOption) error {
	return niceyaml.Invalid(&problemError{msg: msg, marks: []error{mark}}, opts...)
}

// newWarning returns the warning msg, which matches mark, at the location
// of opts.
func newWarning(mark error, msg string, opts ...niceyaml.ErrorOption) error {
	return hook.MarkWarning(newProblem(mark, msg, opts...)) //nolint:wrapcheck // The mark adds no text.
}

// Warning describes one warning of a load. Each warning that a load
// reports wraps one, so [errors.As] finds it in each row that
// [niceyaml.Problems] yields, in strict mode too. A filter from
// [WithWarningFilter] receives it to decide whether the load keeps the
// warning.
//
// The message of a Warning is the message of Err, and Warning adds no
// severity of its own, so a warning stays a warning inside it.
type Warning struct { //nolint:errname // A Warning is no error, though it wraps one.
	// Err holds the warning that the check produced, with its message
	// unchanged. [errors.Is] finds [ErrDeprecated] or [ErrRemoved] through
	// it.
	Err error
	// File is the name of the file that messages show, such as
	// "config/menu.yaml". It is empty for a warning that binds in no file.
	File string
	// Kind is the registered name of the kind, as [Object.Kind] holds it,
	// also for a document that writes an old name from [KindName].
	Kind string
	// Version is the version that the document declares, as
	// [Object.Version] holds it, and empty for an unversioned document. In
	// a plan, it is the version that the document declares after Apply.
	Version string
	// Path is the path that the message shows, such as
	// `$.spec.legacyPricing~`, or `$` for a warning with no path. A `~` at
	// its end marks a warning about a key. In a plan, it is the path that a
	// load after Apply shows.
	Path paths.Path
}

// Error returns the message of Err, or an empty string for a nil Err.
func (w *Warning) Error() string {
	if w.Err == nil {
		return ""
	}

	return w.Err.Error()
}

// Unwrap returns Err.
func (w *Warning) Unwrap() error {
	return w.Err
}

// LoadError is the error of a load, a plan, or Apply that reports one
// problem or more. It holds every error and every warning in load order,
// under one heading that counts them, as in "config: 2 errors, 1 warning".
// The heading starts with the name of the input when the load has one, as
// [Registry.Load] describes. A LoadError that holds one problem reads as
// that problem alone.
//
// [LoadError.Error] returns the heading, then one line per problem, with
// [niceyaml.ErrorListLimit] lines at most and a last line that counts the
// rest. Every renderer of niceyaml shows the heading over the problems, each
// with its details and its excerpt. [errors.Is], [errors.As], and
// [niceyaml.Problems] reach each problem.
type LoadError struct {
	// The one problem, or the heading bound over all of them.
	err error
}

// newLoadError returns a [LoadError] over rows, bound problems in load
// order, under a heading bound to named, or nil for no row.
func newLoadError(named *niceyaml.Source, rows []error) error {
	if len(rows) == 0 {
		return nil
	}

	return &LoadError{err: named.Bind(hook.Tally(rows))}
}

// Error returns the heading of e and one line per problem.
func (e *LoadError) Error() string {
	return e.err.Error()
}

// Unwrap returns the error that holds the problems of e: the heading that
// counts them, or the one problem.
func (e *LoadError) Unwrap() []error {
	return []error{e.err}
}

// AsErrors returns a LoadError with an error mark above each warning of e,
// so each reads as an error and the heading counts it as one.
// [niceyaml.AsErrors] calls it.
func (e *LoadError) AsErrors() error {
	return &LoadError{err: niceyaml.AsErrors(e.err)}
}

// panicError is the error of a panic in a function of the app, such as a
// step, with a message such as "check panicked: boom".
type panicError struct {
	p *niceyaml.PanicError
	// The name of the function that panicked.
	in string
}

// Error returns the name of the function and the value of the panic.
func (e *panicError) Error() string {
	return fmt.Sprintf("%s panicked: %v", e.in, e.p.Value)
}

// Unwrap returns the [niceyaml.PanicError] of the panic.
func (e *panicError) Unwrap() error {
	return e.p
}
