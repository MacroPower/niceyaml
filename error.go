package niceyaml

import (
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/aliaslimit"
	"go.jacobcolvin.com/niceyaml/internal/escape"
	"go.jacobcolvin.com/niceyaml/internal/excerpt"
	"go.jacobcolvin.com/niceyaml/internal/fault"
	"go.jacobcolvin.com/niceyaml/internal/multierr"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

var (
	// ErrNoLocation indicates a path resolves to a token that carries no
	// position. [Node.Ranges] returns it, and [SourceError.Unresolved]
	// reports it for an error bound at such a path.
	ErrNoLocation = errors.New("no location provided")

	// ErrMultipleDocuments indicates a [Source] that holds more than one YAML
	// document where one was expected, among the documents
	// [Source.Documents] returns. [Source.Document] returns it, located at
	// the second of them. The document is at fault for it, as [IsInvalid]
	// describes.
	ErrMultipleDocuments = errors.New("multiple documents in source")

	// ErrDecodeTarget indicates the value given to [Node.DecodeInto] or
	// [Node.DecodeIfPresent] is not a non-nil pointer, so there is nothing
	// to decode into. The error comes back bound to the source as a
	// [SourceError] with no location. It is a mistake of the caller, so
	// [IsInvalid] does not report it.
	ErrDecodeTarget = errors.New("decode target is not a non-nil pointer")

	// ErrSelfValidateTarget indicates the value given to
	// [Node.SelfValidate], [Source.SelfValidate], or [SelfValidateValue]
	// is nil or a nil pointer, so there is nothing to validate. The error
	// comes back bound to the source as a [SourceError] with no location,
	// and SelfValidateValue binds it to no document. It is a mistake of the
	// caller, so [IsInvalid] does not report it.
	ErrSelfValidateTarget = errors.New("self-validation target is nil")

	// ErrDecode indicates that the go-yaml decoder did not decode a node
	// into its target. [Node.Decode] and [Node.DecodeInto] return it, and
	// so does a [Validator] that decodes the node it checks, as a
	// [go.jacobcolvin.com/niceyaml/schema.Schema] does. These errors match:
	//
	//   - A value the decoder rejects, such as one of the wrong kind, a
	//     number that overflows its type, an alias with no anchor, or a
	//     field the target lacks under [WithDisallowUnknownFields].
	//   - The error a value's own UnmarshalYAML or UnmarshalText returns,
	//     which stays in the chain.
	//   - The error for a target type whose definition the decoder
	//     refuses, such as a struct with two fields of one name.
	//   - The error of a reference document from [WithReferences] that
	//     does not parse.
	//
	// The error of a context that ended does not match, even when an
	// unmarshaler wraps it. Neither does a [PanicError], [ErrDecodeTarget],
	// [ErrExcessiveAliasing], or an error that a [Validator] or a
	// [SelfValidator] reports about the value it checks. Text the parser
	// rejects matches [ErrSyntax] instead. [ErrUnknownField],
	// [ErrWrongKind], and [ErrOverflow] label some of the rejections, and a
	// problem with a label matches ErrDecode too. The document is at fault
	// for each problem that matches, as [IsInvalid] describes, and
	// [Node.DecodeInto] describes where each error binds.
	//
	// [errors.Is] reports whether any problem of an error matches
	// ErrDecode, so an error that matches can hold a problem of another
	// kind beside it. A detail from [WithDetails] is no problem, so an
	// error that names a decode error only among its details does not
	// match.
	ErrDecode = errors.New("value does not decode")

	// ErrUnknownField indicates a mapping key that no field of the target
	// struct reads, which a decode rejects under
	// [WithDisallowUnknownFields]. The problem reads `unknown field
	// "replicsa"` and carries the path of the key.
	//
	// ErrUnknownField, [ErrWrongKind], and [ErrOverflow] each label some
	// of the problems that match [ErrDecode], and a problem with a label
	// matches ErrDecode too. A label follows the message the decode writes,
	// not the go-yaml error behind it. A problem that keeps the text of
	// go-yaml or of an unmarshaler matches no label, such as an alias with
	// no anchor or text that does not parse as a [time.Duration]. A
	// [WithCustomUnmarshaler] function that returns or wraps the rejection
	// of its decode function keeps the label of that rejection.
	//
	// [errors.Is] reports whether any problem of an error matches a
	// label, so a caller that sorts the problems of a decode tests the Err
	// of each node [Problems] yields, with ErrDecode last:
	//
	//	for problem := range niceyaml.Problems(err) {
	//		switch {
	//		case errors.Is(problem.Err, niceyaml.ErrUnknownField):
	//			code = "unknown_field"
	//		case errors.Is(problem.Err, niceyaml.ErrWrongKind):
	//			code = "wrong_kind"
	//		case errors.Is(problem.Err, niceyaml.ErrOverflow):
	//			code = "out_of_range"
	//		case errors.Is(problem.Err, niceyaml.ErrDecode):
	//			code = "invalid"
	//		}
	//	}
	ErrUnknownField = errors.New("unknown field")

	// ErrWrongKind indicates a value of a kind its target does not take,
	// such as a string for an integer or a sequence for a mapping. The
	// problem reads "expected integer, got string", with the kinds
	// [Node.DecodeInto] lists. A value the decoder rejects for its Go type
	// alone matches too, and reads "expected mapping, got mapping of
	// another type".
	//
	// ErrWrongKind is a label on a problem that matches [ErrDecode], as
	// [ErrUnknownField] describes. A number outside the range of its
	// integer type matches [ErrOverflow] instead.
	ErrWrongKind = errors.New("value of the wrong kind")

	// ErrOverflow indicates a number outside the range of the integer
	// type that reads it, such as 300 for an int8. The problem reads
	// "expected integer from -128 to 127, got 300". An infinity for an
	// integer matches too. A float field reports no overflow, so `1e400`
	// there reads "expected float, got string" and matches [ErrWrongKind].
	//
	// ErrOverflow is a label on a problem that matches [ErrDecode], as
	// [ErrUnknownField] describes. [ErrOutOfRange] is for a location
	// outside the source and says nothing of a number.
	ErrOverflow = errors.New("number outside the range of its type")

	// ErrExcessiveAliasing indicates a node whose document holds so many
	// nested aliases that a decode would read far more than the document
	// holds, which can take the go-yaml decoder minutes on a few hundred
	// bytes. [Node.Decode] and [Node.DecodeInto] refuse to decode a node
	// that holds an alias when the aliases of its document go past the
	// limit gopkg.in/yaml.v3 applies. A [Validator] that decodes the node
	// it checks returns the same error, as a
	// [go.jacobcolvin.com/niceyaml/schema.Schema] does. [WithAliasLimit]
	// on the [Source] turns the limit off for every one of them. The
	// error comes back bound as a [SourceError] at the first token of the
	// node that is not a comment. The document is at fault for it, as
	// [IsInvalid] describes, and it does not match [ErrDecode].
	//
	// [Node.Nodes] returns ErrExcessiveAliasing too, bound to its receiver
	// with no location, when aliases lead a selector of the path to far
	// more nodes than the document holds. IsInvalid does not report that
	// error, and WithAliasLimit leaves that limit on. The paths and schema
	// packages export the same error value.
	ErrExcessiveAliasing = aliaslimit.ErrExcessiveAliasing

	// ErrSyntax indicates text the go-yaml parser rejects, such as a flow
	// sequence with no closing bracket, a tab that indents a key, or a key
	// a mapping holds twice without [WithAllowDuplicateKeys]. Every error
	// the parser returns matches it. [Source.File] and [Source.Document]
	// return it, and [Node.Err] returns it for a document that did not
	// parse, among the documents [Source.Documents] returns. Each error
	// comes back bound as a [SourceError] at the offending token. The error
	// of a file with several such documents is a join, which matches
	// through each of them. [errors.As] still finds the go-yaml error,
	// which no error of the chain unwraps to. The document is at fault for
	// each problem the parse reports with ErrSyntax, as [IsInvalid]
	// describes.
	//
	// A panic in the parser does not match, such as one on a token with
	// no position that [NewSourceFromTokens] received. It comes back from
	// the same calls as a [PanicError], bound at the first token with a
	// position among the ones the parser was reading.
	//
	// [errors.Is] reports whether any problem of an error matches
	// ErrSyntax, so an error that matches can hold a problem of another
	// kind beside it. A detail from [WithDetails] is no problem, so an
	// error that names a syntax error only among its details does not
	// match.
	ErrSyntax = errors.New("invalid YAML syntax")

	// ErrOutOfRange indicates the error's location lies outside the source.
	// The location starts on a line past the last or before the first,
	// which happens when a position or range came from other text, or at a
	// column before the first. [SourceError.Unresolved] reports it. A
	// number outside the range of its integer type matches [ErrOverflow]
	// instead.
	ErrOutOfRange = errors.New("location outside source")

	// ErrPathNeedsDocument indicates that [Source.Bind] bound an error that
	// carries a path in a source that holds no single document to resolve
	// the path in. It wraps the reason [Source.Document] gives:
	// [ErrMultipleDocuments], or the error the file fails to parse with.
	// [SourceError.Unresolved] reports it. Bind such an error through
	// [Node.Bind] with the document the check ran against. Node.Bind
	// reports it too for a path bound through a document that did not
	// parse, where it wraps the syntax error [Node.Err] returns.
	ErrPathNeedsDocument = errors.New("path needs a document to resolve in")

	// ErrAmbiguousPath indicates a path that names the entries of more
	// than one key of a decoded map. Keys of different types that the
	// document spells with one text, such as 1 and "1" in a map[any]T,
	// share one path, and so do several NaN keys. Such a path resolves to
	// the document entry of one of them at most. A decode binds the
	// errors a [SelfValidator] reports under such a path with no
	// position, so none points at the line of another entry.
	// [SourceError.Unresolved] reports it. An error below a slice, an
	// array, or a map that decodes itself binds at that value instead, as
	// SelfValidator describes for every error there.
	ErrAmbiguousPath = errors.New("path names the entries of several keys")

	// The reason of a [SourceError] whose error carries no location at
	// all, which is not a failure to resolve one, so
	// [SourceError.Unresolved] reports nil for it.
	errUnlocated = errors.New("no location")

	// The mark of a problem the document is at fault for. An error
	// declares the fault by matching it from an Is method, as [Error.Is]
	// does, and [ErrorTree.IsInvalid] reads it. The schema package matches
	// the same value, so it comes from an internal package.
	errInvalid = fault.ErrInvalid

	// The source [BindValue] binds to. It holds no text and no name,
	// since the value came from no file, so an error bound to it reads
	// as its path and its message. Every call shares the one source,
	// which never changes.
	valueSource = sync.OnceValue(func() *Source {
		return NewSourceFromString("")
	})

	// The targets the error [panicked] returns matches for none of the
	// [*PanicError] it holds.
	panicRefused = []error{ErrDecode, ErrUnknownField, ErrWrongKind, ErrOverflow, ErrSyntax, errInvalid}
)

// Error is an error that points at a location in a YAML document: a path
// to the value the error is about, a position or a range of the
// characters at fault, or a path with either. An Error holds what a check
// knows and no source, so a validator builds one from a path alone.
// Binding the Error through a [Node] resolves the location and produces a
// [*SourceError], which puts the position and the path in front of the
// message. [Error.Error] returns the message alone.
//
// An Error is one problem. [WithDetails] adds the errors that explain it,
// and [NewSummary] heads several problems with one message. The
// constructor says whether the document is at fault for the Error, as
// [IsInvalid] describes. The Errors section of the package documentation
// describes these roles, the locations, and the faults.
//
// [errors.Is] and [errors.As] search the error an Error wraps and each
// problem a summary heads, as [Error.Unwrap] returns them, and pass over
// the details. An Error is immutable once created, and [Error.With]
// returns a copy with more options applied.
//
// Create instances with [NewError], [Invalid], [Place], or
// [NewSummary].
type Error struct {
	err    error
	move   *move
	errors []error
	errorConfig
	invalid bool
}

// move is how an [Error] from [Rebase] moves the paths below it.
type move struct {
	// The path the errors below write their `@` paths from, since a path
	// with no selectors is a base like any other.
	base paths.Path
	// Where a path below binds when the document does not hold it. Its
	// paths read from the node that base reads from. The rebase of the
	// self-validation walk sets it below a value that decodes itself.
	fallback fallback
	// The move shifts paths alone, as a detail takes the base of the
	// problem it explains, so it points nothing at its base.
	movesOnly bool
	// The move comes from a binding in a layer of a merged [Source], so
	// its base stands in place of the root of a `$` path below it, where a
	// Rebase leaves such a path as it is.
	reroots bool
	// The base names the entries of several keys of a decoded map, so it
	// resolves in no document, for the reason ErrAmbiguousPath. The
	// rebase of the self-validation walk sets it under such an entry, and
	// it holds for every error below.
	ambiguous bool
}

// NewError creates a new [*Error] with the given message. The Error is a
// problem the document is at fault for, so [IsInvalid] reports it.
// Use [Invalid] instead to declare the document at fault for an existing
// error.
//
// An Error may carry a location and no message, as a detail that marks a
// related location does. Such an Error comes from an empty message:
//
//	niceyaml.NewError("", niceyaml.AtPath(firstUse))
//
// Its binding names the position and the path alone, and an excerpt marks
// the location with a caret run and no text beside it.
func NewError(msg string, opts ...ErrorOption) *Error {
	e := wrapUndeclared(errors.New(msg), opts...)
	e.invalid = true

	return e
}

// Invalid creates a new [*Error] that wraps err and declares the document
// at fault for it, so [IsInvalid] reports it. The document is at fault for
// each problem the Error heads too, such as each branch of a join it
// wraps, bound or not. An err that matches [context.Canceled] or
// [context.DeadlineExceeded], or a join with such a branch, gets the
// options and no declaration, since that error is about the call. A
// detail of err decides nothing. Use [NewError] instead to create an
// error from a message, and [Place] to locate an error without declaring
// a fault.
//
// Invalid returns nil for a nil err, and for a nil [*Error] or
// [*SourceError] pointer, whatever the options. It returns an error
// rather than an [*Error], so the nil compares equal to nil wherever it
// goes, and a validator returns the result as it is:
//
//	return niceyaml.Invalid(spec.Check())
//
// An err that is a join stays a list of problems, and the options locate
// the heading above them and no branch. To point each branch at a value,
// move the join under the path of the value with [Rebase], or wrap each
// branch before joining them.
func Invalid(err error, opts ...ErrorOption) error {
	if isNothing(err) {
		return nil
	}

	e := wrapUndeclared(err, opts...)
	e.invalid = !contextEnded(err)

	return e
}

// Place creates a new [*Error] that wraps err and gives it the location
// and the details of the options. Place declares nothing about err, so
// [IsInvalid] reports the result only when it reports err. An err the
// document is at fault for through its own chain, such as an Error from
// [NewError] or a decode rejection, stays a fault inside Place. A check
// that could not run, such as one whose I/O fails, shows its error at the
// value it read this way:
//
//	return niceyaml.Place(fmt.Errorf("stat license: %w", err),
//		niceyaml.AtRange(licenseRange), niceyaml.WithDetails(why))
//
// The result binds and prints as one from [Invalid] with the same options
// does. A decode declares the document at fault for every error a
// [SelfValidator] returns, so a Validate method that returns an Error from
// Place still reports a fault, and a check whose I/O can fail belongs in a
// [Validator].
//
// Place returns nil for a nil err, and for a nil [*Error] or
// [*SourceError] pointer, whatever the options. It returns an error rather
// than an [*Error] for the reason Invalid gives. An err that is a join
// stays a list of problems, and the options locate the heading above them
// and no branch, as Invalid describes.
func Place(err error, opts ...ErrorOption) error {
	if isNothing(err) {
		return nil
	}

	return wrapUndeclared(err, opts...)
}

// wrapUndeclared returns an [*Error] around err with opts applied. The
// Error does not match [errInvalid] itself, so it declares nothing about
// err. [NewError], [Invalid], and [Place] build on it.
func wrapUndeclared(err error, opts ...ErrorOption) *Error {
	e := &Error{err: err}
	for _, opt := range opts {
		opt(&e.errorConfig)
	}

	return e
}

// NewSummary creates a new summary of errs: an [*Error] whose message msg
// heads several separate problems, such as "3 schema violations" above
// one error per violation. The summary is a heading and never a problem
// itself. [SourceError.Error] lists each of errs below the line of the
// summary, [ErrorTree.Problems] yields each of them and never the
// summary, and [FormatError] draws each as a branch.
//
// A summary carries no location. [Rebase] and a scoped [Node.Bind] give
// none to it, and they locate each of errs on its own, as they locate
// each branch of a join. A validator's summary above violations with no
// location thus binds each violation at the value it checked.
//
// NewSummary skips a nil error in errs. With no error left it returns
// nil, and with one it returns that error alone, since a heading such as
// a count says nothing the one error does not. It returns an error rather
// than an [*Error], as [errors.Join] does, so the nil compares equal to
// nil wherever it goes. A validator therefore returns the result for any
// number of violations:
//
//	return niceyaml.NewSummary(fmt.Sprintf("%d violations", len(errs)), errs...)
//
// [errors.Join] lists several problems with no heading. An error built
// with [WithDetails] is one problem, with the errors that explain it
// below it.
func NewSummary(msg string, errs ...error) error {
	var members []error

	for _, err := range errs {
		if !isNothing(err) {
			members = append(members, err)
		}
	}

	switch len(members) {
	case 0:
		return nil
	case 1:
		return members[0]
	}

	return &Error{err: errors.New(msg), errors: members}
}

// Is reports whether target is the mark of a problem the document is at
// fault for and the [Error] declares that fault. The module keeps the
// mark internal, so the method matches no target a caller can name, and
// a caller asks [IsInvalid] or [ErrorTree.IsInvalid] instead of
// [errors.Is]. An Error from [NewError] or [Invalid] declares the
// fault, with a location or without, and so does the Error a decode puts
// around what a [SelfValidator] returns. An Error from [Place] or
// [Rebase], a summary from [NewSummary], and the Error a scoped
// [Node.Bind] puts around an error declare nothing, and neither does an
// Error from Invalid around the error of a context that ended. Any
// other target matches through the errors the Error wraps, as
// [Error.Unwrap] returns them.
func (e *Error) Is(target error) bool {
	return target == errInvalid && e != nil && e.invalid
}

// IsInvalid reports whether the document is at fault for every problem
// of err, the question a caller asks to pick a status code, an exit code,
// or whether to retry. The problems are the nodes [ErrorTree.Problems]
// yields for err, so IsInvalid(err) and NewErrorTree(err).IsInvalid()
// agree. A nil err is not invalid, and neither is an error with no
// problem, such as one with an empty message.
//
// The code that builds an error declares the fault, and a location never
// decides it. The document is at fault for these problems:
//
//   - An [*Error] from [NewError] or [Invalid], and each problem it heads.
//   - Each problem the parse reports with [ErrSyntax] or a decode reports
//     with [ErrDecode].
//   - Every error a [SelfValidator] returns.
//   - [ErrMultipleDocuments], a value that [Node.At], [Node.Ranges], or
//     [Node.Nodes] does not find, a schema violation, the ErrNoMatch of a
//     schema registry, and [ErrExcessiveAliasing] from every reader but
//     Node.Nodes.
//
// Any other error declares nothing, so a wrapper is a fault only when the
// error it wraps is one. The error of a context that ended and a
// [PanicError] are never faults. One problem that is no fault makes the
// whole error not invalid, and a heading or a detail decides nothing. The
// Errors section of the package documentation describes the cases in full.
//
// A server asks IsInvalid before it tests for a sentinel, since
// [errors.Is] reports whether any problem matches:
//
//	switch {
//	case err == nil:
//	case !niceyaml.IsInvalid(err):
//		status = 500 // schema load, canceled context, I/O, panic
//	case errors.Is(err, niceyaml.ErrSyntax):
//		status = 400
//	default:
//		status = 422 // decode rejection, violation, Validate method
//	}
func IsInvalid(err error) bool {
	return NewErrorTree(err).IsInvalid()
}

// With returns a copy of the [Error] with the given options applied. The
// receiver is unchanged, so an Error shared between callers can be
// specialized per use:
//
//	located := err.With(niceyaml.AtPath(namePath))
func (e *Error) With(opts ...ErrorOption) *Error {
	if e == nil {
		return nil
	}

	c := *e
	c.errors = slices.Clone(e.errors)
	c.details = slices.Clone(e.details)

	for _, opt := range opts {
		opt(&c.errorConfig)
	}

	return &c
}

// Rebase returns err with base in front of every `@` path in its tree: on
// the [*Error] that anchors it, on each problem a summary or a join heads,
// and on each detail. A `$` path stays as it is, as [paths.Path.Join]
// leaves it, and so does a position or a range. Each problem that carries
// no location points at base itself, while a heading and a detail gain no
// location. Rebases compose, and a `$` base stops the bases above it from
// moving the paths below it. The Errors section of the package
// documentation describes rebasing.
//
// A check written for a type writes `@` paths from the value it checks.
// A caller that runs it on a value inside a document rebases the result
// under the path of that value before it binds the result:
//
//	return doc.Bind(niceyaml.Rebase(checkHours(&cfg.Hours), paths.Doc().Child("hours")))
//
// A path names a map key as the document spells it, so a base built from
// a decoded key can miss. The key 0x10 of a map[int]Server decodes to 16,
// and an error under `$.ports.16` then binds at the `ports:` key, as
// [SourceError.Nearest] describes. A [DataLocator] finds an entry by its
// decoded key.
//
// The result keeps the message of err, and [errors.Is] and [errors.As]
// see through it, including through a join that Rebase rebuilds branch by
// branch. Rebase declares nothing, so [IsInvalid] reports the result only
// when it reports err. A nil err, or a nil [*Error] or [*SourceError]
// pointer, returns nil. An error that is bound already comes back as it
// is, except the result of [BindValue], which rebases as the error it was
// made from.
func Rebase(err error, base paths.Path) error {
	return rebase(err, move{base: base}, false)
}

// rebase is [Rebase] with the paths of err moved as m moves them. With
// [move.movesOnly], the result points nothing at the base, as a detail
// takes the base of the problem it explains. With invalid, each Error the
// rebase builds matches [errInvalid], as the self-validation walk marks
// what a [SelfValidator] returns. A binding takes no base, and with
// invalid it comes back as [markInvalid] marks it. A binding that stands
// in no document rebases as the error [placeable] returns for it.
func rebase(err error, m move, invalid bool) error {
	if isNothing(err) {
		return nil
	}

	err = placeable(err)
	if isNothing(err) {
		return nil
	}

	if isBound(err) {
		if invalid {
			return markInvalid(err)
		}

		return err
	}

	// An Error that adds nothing to the join it wraps rebases as the join,
	// with its mark on each branch, and the copy keeps what the Error
	// declared.
	x, ok := err.(*Error) //nolint:errorlint // The node itself, not a chain search.
	if ok && x.addsNothing() {
		if _, _, joined := joinBranches(x.err); joined {
			return &Error{err: rebase(x.err, m, invalid || x.invalid), invalid: x.invalid || invalid}
		}
	}

	joined := mapJoin(err, func(branch error) error {
		return rebase(branch, m, invalid)
	})
	if joined != nil {
		return joined
	}

	return &Error{err: err, move: &m, invalid: invalid}
}

// markInvalid returns err as a problem the document is at fault for, as
// an [*Error] the caller declared invalid marks each problem it heads. An
// Error that matches [errInvalid] itself comes back as it is. Any other
// err comes back inside an Error that adds nothing to it but the mark, so
// the error keeps its text, its location, and its children. A binding
// gains the mark the same way, and [isBound] still reads the result as a
// binding.
func markInvalid(err error) error {
	if isNothing(err) {
		return err
	}

	x, ok := err.(*Error) //nolint:errorlint // The node itself, not a chain search.
	if ok && x.invalid {
		return err
	}

	return &Error{err: err, invalid: true}
}

// ErrorOption configures an [Error]. [NewError], [Invalid], [Place],
// and [Error.With] take the same options. [AtPath] sets the path of the
// Error. [AtPosition] or [AtRange] sets its position or range, and the
// last of those two given wins. [WithDetails] adds the errors that
// explain it. A [DataLocator] returns the options that locate an error by
// the names of decoded data.
//
// Available options:
//   - [AtPath]
//   - [AtPosition]
//   - [AtRange]
//   - [WithDetails]
//   - [DataLocator.At]
//   - [DataLocator.AtKey]
type ErrorOption func(*errorConfig)

// errorConfig holds the settings an [ErrorOption] configures. An option
// takes it in place of the [Error] that embeds it, so only a constructor
// and [Error.With] can apply one.
type errorConfig struct {
	// The position or the range: a position.Position or a position.Range,
	// or nil when neither is set.
	loc any
	// The errors from WithDetails, which leave out the nil ones.
	details []error
	// The path, when hasPath, since a path with no selectors is a path
	// like any other.
	path    paths.Path
	hasPath bool
}

// AtPath is an [ErrorOption] that sets the YAML path of the value the
// error is about. It replaces a path set before it. An `@` path resolves
// from the [Node] that binds the Error and a `$` path from the root of the
// document, as the Errors section of the package documentation describes.
//
// The error points at the node the path selects, which for a mapping
// entry is its value. A mapping or a sequence spans many lines, so an
// error there points at the token that introduces it, as
// [paths.Resolver.Token] describes. That token is the key of the entry
// that holds it, or the "-" of its element in a block sequence. With
// neither, it is the "{" or "[" of a flow collection, or the first key or
// element of a block collection at the root. A path from [paths.Path.Key]
// points at the key of the entry whatever its value holds, which suits an
// error about the key itself:
//
//	niceyaml.NewError("unknown field", niceyaml.AtPath(paths.Current().Child("spec", "foo").Key()))
//
// A path the document leaves out, as a check for a required field names,
// binds at the key of the mapping that lacks the value. A path that enters
// an alias the document cannot follow binds at the alias, as
// [SourceError.Nearest] describes. With [AtPosition] or [AtRange], the
// error binds there instead, and the path names the value in the message
// alone. [DataLocator.At] builds both from the names of decoded data.
func AtPath(p paths.Path) ErrorOption {
	return func(c *errorConfig) {
		c.path, c.hasPath = p, true
	}
}

// AtPosition is an [ErrorOption] that sets the 0-indexed position where
// the error occurred. It replaces a position or a range set before it. The
// position is in the coordinates of the lines [Source.Lines] returns,
// where line 0 is line 1 of the text. [SourceError.Excerpt] highlights
// the content of the token at that position. A path from [AtPath] on the
// same Error names the value in the message, and the position locates
// the error. A producer that holds a go-yaml token converts it with
// [position.NewFromToken], and one that holds none names the position on
// its own.
func AtPosition(p position.Position) ErrorOption {
	return func(c *errorConfig) {
		c.loc = p
	}
}

// atToken returns an [ErrorOption] that sets the position of tk, or one
// that sets nothing when tk or its position is nil, as the token of a
// go-yaml error can be.
func atToken(tk *token.Token) ErrorOption {
	if tk == nil || tk.Position == nil {
		return func(*errorConfig) {}
	}

	return AtPosition(position.NewFromToken(tk))
}

// AtRange is an [ErrorOption] that sets the 0-indexed range the error
// covers. It replaces a position or a range set before it. The range is in
// the coordinates of the view [Source.Lines] returns, where line 0 is
// line 1 of the text. [SourceError.Excerpt] highlights the whole range
// rather than one token, so it is the option for a check that knows the
// columns an error covers, such as one that runs on rendered lines. A
// path from [AtPath] on the same Error names the value in the message,
// and the range locates the error, so a check on the characters inside a
// value names the value and highlights the characters:
//
//	niceyaml.NewError("invalid character", niceyaml.AtPath(namePath), niceyaml.AtRange(charRange))
//
// A range that ends before its start covers nothing, so it binds as the
// empty range at its start.
func AtRange(r position.Range) ErrorOption {
	return func(c *errorConfig) {
		c.loc = r
	}
}

// WithDetails is an [ErrorOption] that adds errors that explain the
// [Error], such as the reasons for it, the forms a value failed to match,
// or a related location:
//
//	niceyaml.NewError("port 80 conflicts", niceyaml.AtPath(b),
//		niceyaml.WithDetails(niceyaml.NewError("first declared here", niceyaml.AtPath(a))))
//
// The Error stays one problem, and a detail is never a problem of its
// own. [ErrorTree.Problems] yields the Error once, with its details as the
// Children of its node, and [SourceError.Error] leaves them out of its
// message, while [FormatError] draws each as a branch. Binding binds each
// detail at the location its own error carries, if any, and a detail that
// is or wraps a [*SourceError] stays as it is, so it can point into
// another file. [Rebase] and a scoped [Node.Bind] move the paths of a
// detail and give it no location.
//
// [errors.Is] and [errors.As] on the Error pass over its details, so a
// sentinel that a detail wraps never matches for the problem it explains.
// WithDetails skips a nil error. To report several separate problems,
// join them with [errors.Join] or head them with [NewSummary].
func WithDetails(errs ...error) ErrorOption {
	return func(c *errorConfig) {
		for _, n := range errs {
			if !isNothing(n) {
				c.details = append(c.details, n)
			}
		}
	}
}

// Error returns the message of the error the [Error] wraps, with no
// location in it. A wrapper such as [fmt.Errorf] around the Error thus
// holds the message alone, and the [SourceError] that binds the Error puts
// the position and the path that [Error.Path] reports in front of the
// whole text. The errors a summary heads and the details stay out of the
// message too. An Error from [NewError] with an empty message, and a nil
// Error, have no text. [FormatError] and [BindValue] put the path in front
// of an Error that no binding holds.
func (e *Error) Error() string {
	if e == nil || e.err == nil {
		return ""
	}

	return e.err.Error()
}

// Format implements [fmt.Formatter].
//
// The %v and %s verbs print [Error.Error], which holds no location. The
// %+v verb prints what [FormatError] renders for the error, its message
// behind its path with the errors below it as a tree, with
// [ErrorListLimit] problems at most and a last row that counts the rest.
// Every other verb formats [Error.Error] as it formats a string, with the
// width, precision, and flags given, so %q quotes the message.
func (e *Error) Format(f fmt.State, verb rune) {
	formatVerb(f, verb, e)
}

// LogValue implements [slog.LogValuer].
//
// The value is the tree [FormatError] prints for the error, as a string
// with no source excerpt, with [ErrorListLimit] problems at most and a
// last row that counts the rest. A wrapper such as [fmt.Errorf] around the
// Error logs as its own message, which holds no path, so a program that
// wants the path of any error logs [FormatError] as a string.
func (e *Error) LogValue() slog.Value {
	return logValue(e)
}

// locus is the location an [Error] carries: a path when hasPath, a
// [position.Position] or a [position.Range] in loc, or both. The zero
// locus is no location. The path of a locus that is ambiguous names the
// entries of several keys of a decoded map, so [locate] resolves it in no
// document. The fallback says where the path binds when the document does
// not hold it.
type locus struct {
	loc       any
	path      paths.Path
	fallback  fallback
	hasPath   bool
	ambiguous bool
}

// rebase returns l with base in front of its path and of each path of its
// fallback. A locus with no path comes back as it is, since a base moves
// paths alone. An `@` path takes base in front, and a `$` path stays as
// it is, as [paths.Path.Join] joins them.
func (l locus) rebase(base paths.Path) locus {
	if l.hasPath {
		l.path = base.Join(l.path)
		l.fallback = l.fallback.rebase(base)
	}

	return l
}

// fallback is where an error binds when the document does not hold its
// path. The self-validation walk gives one to each error at or below a
// value that decodes itself, since the fields of such a value need not
// mirror the document. The zero fallback is none, so a path that does
// not resolve then leaves its error with no position.
type fallback struct {
	// The paths of the values above the error that decode themselves, the
	// nearest first. The error binds at the first one the document holds.
	at []paths.Path
	// The first of at is a slice, an array, or a map, which has no field
	// tags to say where its elements sit. The path of the error then
	// names a value of the program alone, and the error binds at the
	// first of at that the document holds whether or not the document
	// holds a node at that path.
	only bool
}

// rebase returns f with base in front of each of its paths, as
// [locus.rebase] puts it in front of the path they stand in for.
func (f fallback) rebase(base paths.Path) fallback {
	if len(f.at) == 0 {
		return f
	}

	at := make([]paths.Path, 0, len(f.at))
	for _, p := range f.at {
		at = append(at, base.Join(p))
	}

	f.at = at

	return f
}

// then returns the fallback of a path that has f and lies below an
// [Error] whose fallback is outer, once the paths of both read from one
// node. The values of f lie below those of outer, so they come first.
// When outer binds every error below it, the values of f say nothing of
// where the path sits, so outer stands alone.
func (f fallback) then(outer fallback) fallback {
	if outer.only {
		return outer
	}

	return fallback{at: slices.Concat(f.at, outer.at), only: f.only}
}

// locus returns the location e carries itself, without looking through
// its cause chain or applying its base.
func (e *Error) locus() locus {
	return locus{loc: e.loc, path: e.path, hasPath: e.hasPath}
}

// apply returns l, the location of an error below an [Error] that moves
// it as m does, with the base of m applied. The base goes in front of an `@`
// path, as [locus.rebase] puts it there. An Error that reroots puts it in
// front of a `$` path too, in place of the root, since such a path reads
// from the merged value and the base is where a layer holds it.
// A path below an Error that is ambiguous is ambiguous too.
//
// An `@` path takes the fallback of e behind its own, as [fallback.then]
// joins them, and a `$` path, which the base leaves as it is, takes none.
// A fallback that binds every error below e stands in for the path of
// each, so a key that shares its path there makes no path ambiguous, and
// only the mark of e itself counts.
func (m *move) apply(l locus) locus {
	l.ambiguous = l.ambiguous || m.ambiguous

	if m.reroots && l.hasPath {
		rel, _ := l.path.CutPrefix(paths.Doc())
		l.path = m.base.Join(rel)

		return l
	}

	if !l.hasPath || l.path.IsAbsolute() {
		return l.rebase(m.base)
	}

	l = l.rebase(m.base)
	l.fallback = l.fallback.then(m.fallback)

	if m.fallback.only {
		l.ambiguous = m.ambiguous
	}

	return l
}

// at returns the location an [Error] that moves paths as m does points a
// problem with no location of its own at: its base.
func (m *move) at() locus {
	return locus{path: m.base, hasPath: true, ambiguous: m.ambiguous, fallback: m.fallback}
}

// then returns the move of a child below an [Error] that moves paths as
// m does, and below that an Error that moves them as inner does: the base
// of m joined with the base of inner. A nil move moves nothing, so then
// returns m for a nil inner. The fallback and the mark of inner join
// those of m as [move.apply] joins them from below. A `$` base replaces
// the base above it, so the fallback of inner then stands alone. A
// fallback of m that binds every error below it makes no path below it
// ambiguous. The move moves paths alone once either does, and never
// reroots.
func (m *move) then(inner *move) *move {
	if inner == nil {
		return m
	}

	var outer move

	if m != nil {
		outer = *m
	}

	covered := outer.fallback.only && !inner.base.IsAbsolute()

	joined := move{
		base:      outer.base.Join(inner.base),
		movesOnly: outer.movesOnly || inner.movesOnly,
		ambiguous: outer.ambiguous || inner.ambiguous && !covered,
	}

	if inner.base.IsAbsolute() {
		joined.fallback = inner.fallback
	} else {
		joined.fallback = inner.fallback.rebase(outer.base).then(outer.fallback)
	}

	return &joined
}

// addsNothing reports whether e adds nothing to the error it wraps: e is
// not nil, is not from [Rebase], carries no location of its own, is no
// summary, and holds no details. Such an Error has the text and the
// children of the error it wraps.
func (e *Error) addsNothing() bool {
	return e != nil && e.move == nil && !e.hasLocation() && !e.nests()
}

// nests reports whether e holds errors below it of its own: the errors a
// summary heads or the details from [WithDetails].
func (e *Error) nests() bool {
	return len(e.errors) > 0 || len(e.details) > 0
}

// hasLocation reports whether e carries a location of its own: a path, a
// position, or a range.
func (e *Error) hasLocation() bool {
	return e.hasPath || e.loc != nil
}

// Unwrap returns the errors [errors.Is] and [errors.As] search: the error
// the [Error] wraps, then each error a summary from [NewSummary] heads.
// A match thus means that the problem matches, or a problem the summary
// heads.
//
// Unwrap leaves out the details from [WithDetails], which explain a
// problem and are none themselves. A sentinel or a typed error that a
// detail wraps thus never matches for the problem it explains. The
// children with Detail set of the node of the Error in the tree
// [NewErrorTree] builds are the details. A nil Error unwraps to nothing,
// so a chain that holds one is safe to walk.
func (e *Error) Unwrap() []error {
	if e == nil || (e.err == nil && len(e.errors) == 0) {
		return nil
	}

	result := make([]error, 0, 1+len(e.errors))
	if e.err != nil {
		result = append(result, e.err)
	}

	return append(result, e.errors...)
}

// location returns the locus of the [Error]: a path from [AtPath], a
// position from [AtPosition], a range from [AtRange], or a path beside a
// position or a range. It looks through wrapping to the nearest Error
// that carries one, with the base of every [Rebase] on the way joined in
// front of a path. A [*SourceError] on the way ends the walk, as binding
// does, and reports the location it resolved from, with no base from
// above the binding, so an Error and its binding agree on the location.
// An Error from Rebase with nothing located below it points at its base
// when the chain below it is one problem, as [anchorOf] describes. A nil
// Error and an Error with no location return the zero locus.
func (e *Error) location() locus {
	if e == nil {
		return locus{}
	}

	a := anchorOf(e)
	if x, ok := a.err.(*SourceError); ok { //nolint:errorlint // The anchor itself, found by the walk.
		return boundLocus(x)
	}

	return a.locus
}

// Path returns the [paths.Path] the [Error] is about and true, or the
// zero Path and false when the Error carries none. The location is the
// one [AtPath] set on the Error itself or on the nearest located Error
// along its cause chain. An Error built with [Invalid] around a
// located Error thus reports that location, with the base of every
// [Rebase] on the way joined in front. A [*SourceError] along the chain
// ends the walk, and the Error reports the path [SourceError.Path]
// reports for that binding, with no base from a Rebase above the binding
// in front.
// An Error that carries a position or a range beside the path reports
// both. A nil Error has none.
func (e *Error) Path() (paths.Path, bool) {
	l := e.location()

	return l.path, l.hasPath
}

// Position returns the [position.Position] the [Error] points at and
// true, or the zero Position and false when the Error carries a range or
// no position. It looks through wrapping as [Error.Path] does.
func (e *Error) Position() (position.Position, bool) {
	p, ok := e.location().loc.(position.Position)

	return p, ok
}

// Range returns the [position.Range] the [Error] covers and true, or the
// zero Range and false when the Error carries a position or no range. It
// looks through wrapping as [Error.Path] does. The range is the one
// [AtRange] set, in the coordinates of [Source.Lines].
// [SourceError.Range] returns the range a location of any kind resolved
// to once a binding holds the Error.
func (e *Error) Range() (position.Range, bool) {
	r, ok := e.location().loc.(position.Range)

	return r, ok
}

// textCause returns the innermost error along the causes of e that is
// not an Error, the one whose text [Error.Error] returns, or nil when
// the causes end at nil or at a nil Error, which has no text.
func (e *Error) textCause() error {
	for cur := e; ; {
		inner, ok := cur.err.(*Error) //nolint:errorlint // Identity of the direct child, not a chain search.
		if !ok {
			return cur.err
		}

		if inner == nil {
			return nil
		}

		cur = inner
	}
}

// location is a resolved error location: the position the message reports,
// the range to highlight when the error carried one, and the token a path
// resolved to when the error carried a path. For a path that names a key
// the document leaves out, the token is the key of the mapping that lacks
// it, and near is the path of that mapping. For a path that enters an
// alias the document cannot follow, the token is that alias, near is the
// path of the alias, and unfollowed is the error the path failed to
// resolve with. For a path that bound at its [fallback], the token is
// that of the value the fallback names, and near is the path of that
// value.
type location struct {
	unfollowed error
	rng        *position.Range
	tk         *token.Token
	near       *paths.Path
	// Set for a location that resolved in a layer of the document
	// [NewSourceFromLayers] builds, rather than in the document the error
	// bound through. It is the path of the root of that document in the
	// document of the layer.
	layerRoot *paths.Path
	pos       position.Position
}

// highlight returns the ranges to highlight for l in lines, as
// [excerpt.Ranges] finds them.
func (l location) highlight(lines line.Lines) position.Ranges {
	return excerpt.Ranges(lines, l.rng, l.tk, l.pos)
}

// locate resolves l, the location of an [Error], and returns the node it
// is bound to. A range or a position is the location as it is, and a
// path locates the error at the position of the token it resolves to in
// the document. [anchorOf] joins the base of every Error from [Rebase]
// above that Error in front of the path, and [binder.scoped] put the
// scope of the binding there, so the path starts at `$` and locate
// resolves it as it is. A path beside a range or a position names the
// value in the message and is not resolved, so a range locates the error
// whether or not the document holds the path. A range or a position in
// the document [NewSourceFromLayers] builds resolves as [locateMerged]
// describes instead. The node is the one b binds
// with, or, when b routes, the root of the document [binder.route] picks
// for the location. An empty l is errUnlocated, a path bound where no
// document resolves it is [ErrPathNeedsDocument], and a path that is
// ambiguous, as [locus] describes one, is [ErrAmbiguousPath].
func locate(b binder, l locus) (location, *Node, error) {
	switch loc := l.loc.(type) {
	case position.Range:
		node := b.nodeAt(loc.Start.Line)
		if node.merges() {
			return locateMerged(b, node, loc.Start, l)
		}

		return location{pos: loc.Start, rng: &loc}, node, nil

	case position.Position:
		node := b.nodeAt(loc.Line)
		if node.merges() {
			return locateMerged(b, node, loc, l)
		}

		return location{pos: loc}, node, nil
	}

	if l.hasPath {
		if l.ambiguous {
			return location{}, b.node, fmt.Errorf("%w: %s", ErrAmbiguousPath, l.path)
		}

		return locateBelow(b, l)
	}

	return location{}, b.node, errUnlocated
}

// locateBelow resolves the path of l as [locatePath] does. A path that
// does not resolve binds at the first path of its fallback that does.
// The near of that location is the path it resolved, unless that path
// resolved to a node near it already. A fallback that stands in for the
// path, as [fallback] describes one, resolves in its place. A path of the
// fallback that equals the path sets no near, since the error then binds
// at the node its own path selects. When nothing resolves, the result is
// that of the first path locateBelow tried.
func locateBelow(b binder, l locus) (location, *Node, error) {
	var (
		loc   location
		node  *Node
		err   error
		tried bool
	)

	if !l.fallback.only {
		loc, node, err = locatePath(b, l.path)
		if err == nil {
			return loc, node, nil
		}

		tried = true
	}

	for _, path := range l.fallback.at {
		floc, fnode, ferr := locatePath(b, path)
		if ferr == nil {
			if floc.near == nil && !path.Equal(l.path) {
				floc.near = &path
			}

			return floc, fnode, nil
		}

		if !tried {
			loc, node, err, tried = floc, fnode, ferr, true
		}
	}

	return loc, node, err
}

// locatePath resolves path from the root of the document of the node b
// binds with, or of the one document of the source when b routes.
// [binder.scoped] put the scope of that node in front of the path
// already, so the path starts at `$`. A path that names a key the
// document leaves out resolves to the key of the mapping that lacks it,
// as [Node.nearestLocation] finds it. A path that enters an alias the
// document cannot follow resolves to that alias, as [Node.aliasLocation]
// finds it. A source that holds several
// documents, or does not parse, has no document to resolve the path in,
// so the location is [ErrPathNeedsDocument] wrapping that reason. A
// document that did not parse has no tree to resolve the path in either,
// so the location is ErrPathNeedsDocument wrapping its syntax error.
//
// In the document [NewSourceFromLayers] builds, the path resolves in the document of
// the layer [layering.layer] picks, and that layer is the node locatePath
// returns, so it belongs to another source than the one b binds to. The
// location then says so, whether or not the path resolved there, and
// holds the path of the root of the merged document in that layer.
func locatePath(b binder, path paths.Path) (location, *Node, error) {
	node := b.node

	if node == nil && b.route {
		doc, err := b.src.single()
		if err != nil {
			return location{}, nil, fmt.Errorf("%w: %s: %w", ErrPathNeedsDocument, path, err)
		}

		node = doc
	}

	if node == nil {
		return location{}, nil, fmt.Errorf("%w: %s", ErrPathNeedsDocument, path)
	}

	layer, base, path := node.source.layers.layer(node, path)

	loc, err := layer.node.resolveLocation(path)
	if layer.node != node {
		loc.layerRoot = &base
	}

	return loc, layer.node, err
}

// resolveLocation resolves path, which starts at `$`, in the document of
// n, as [locatePath] describes for the document an error binds in.
func (n *Node) resolveLocation(path paths.Path) (location, error) {
	root := n.doc.node

	if root.doc.err != nil {
		return location{}, fmt.Errorf("%w: %s: %w", ErrPathNeedsDocument, path, root.doc.err)
	}

	loc, err := root.pathLocation(path)
	if err != nil {
		if near, ok := root.nearestLocation(path, err); ok {
			return near, nil
		}

		if alias, ok := root.aliasLocation(path, err); ok {
			return alias, nil
		}

		return location{}, err
	}

	return loc, nil
}

// merges reports whether n is a Node of the document [NewSourceFromLayers] builds,
// whose errors bind in the layers. A nil n is none.
func (n *Node) merges() bool {
	return n != nil && n.source.layers != nil
}

// locateMerged resolves the position at in the document of node, which
// [NewSourceFromLayers] built, for an error with the location l. The text of that
// document is no file of the caller. The error therefore binds at the
// value that lies at the position, in the layer that holds it, as a path
// to that value binds. Where no value lies at the position, such as in
// the preamble, the path of l locates the error. An l with no path is
// then errUnlocated, and its error binds in the lowest layer.
func locateMerged(b binder, node *Node, at position.Position, l locus) (location, *Node, error) {
	b.node = node

	if path, ok := node.PathAt(at); ok {
		return locatePath(b, path)
	}

	if l.hasPath && !l.ambiguous {
		return locatePath(b, l.path)
	}

	return location{}, node, errUnlocated
}

// SourceError is an error bound to the [*Source] it occurred in. Binding
// resolves the location of the error against the source once, so a
// SourceError never changes and every method reads that result.
// [SourceError.Error] puts the position and the `$` path in front of the
// message and lists the problems a heading heads, one per line, and
// [SourceError.Excerpt] returns the surrounding lines with the location
// highlighted. A SourceError unwraps to the error it binds, so
// [errors.Is] and [errors.As] see through it.
//
// Binding binds the whole tree of the error. Each problem a summary or a
// join heads and each detail binds as a SourceError of its own, at the
// location its own error carries, and [NewErrorTree] lays them out as a
// tree. A path the document leaves out binds at the nearest node above
// it, as [SourceError.Nearest] describes, and any other location that
// does not resolve costs the SourceError its position, for the reason
// [SourceError.Unresolved] returns. The Errors section of the package
// documentation describes these cases. [FormatError] prints the tree and
// the excerpts as plain text, however a caller wrapped or joined the
// error:
//
//	if _, err := source.File(); err != nil {
//		log.Print(niceyaml.FormatError(err))
//	}
//
// Create instances with [Node.Bind] or [Source.Bind], or receive them
// from the [Source] and [Node] methods. [BindValue] creates one for a
// value that came from no document.
type SourceError struct {
	err error
	// The error the binding was made from when it stands in no document,
	// as [BindValue] builds one, and nil for every other binding. Each
	// binding below such a binding holds the error it was made from too.
	// [placeable] hands it to a later binding in place of this one.
	free error
	// The reason the location did not resolve, which is nil when it did:
	// errUnlocated for an error that carries none, the error of a path
	// that does not resolve, or ErrOutOfRange for a location the source
	// does not hold.
	locErr error
	source *Source
	// The node paths resolved from, which is nil for an error the Source
	// bound to no document.
	node *Node
	// The binding the first line of the message comes from when Error
	// leaves the message as it is, or nil when Error puts a position or
	// name in front, or when no binding wrote the first line.
	lead *SourceError
	// The location, which binding resolved.
	loc location
	// The error that ends the cause chain with children below it.
	end chainEnd
	// The ranges the excerpt highlights.
	ranges position.Ranges
	// The bound children in the order the cause chain reaches them. They
	// are the problems the binding heads and the details of every Error
	// along the chain. The problems are the errors a summary along the
	// chain heads, or every branch followBranches keeps of the error that
	// ends it by unwrapping to several, located or not.
	below []boundChild
	// The texts of the binding, which texts builds the first time a
	// caller asks for one.
	built boundTexts
	// The range the location covers in the source.
	rng position.Range
	// Makes passedLead find lead once, the first time a message asks.
	leadOnce sync.Once
	// Makes texts build built once.
	builtOnce sync.Once
	// The error wraps a binding, whose location, source, and children this
	// one took over, and whose position its message carries already.
	adopted bool
}

// boundChild is a child of a [SourceError], whether it is a detail
// rather than a problem the binding heads, and whether the binding that
// holds it located it. A located child is one the binding bound anew from
// an [*Error] that carries its location, so its line names a position or
// a path the text of the child lacks. A child that was a binding already
// names its own position in its text, and one with no location names
// none. An unbound child is one the binding bound anew from an error that
// was no binding and wraps none. The text of such an error names none of
// the problems a summary in it heads, where the text of a binding lists
// them.
type boundChild struct {
	bound   *SourceError
	detail  bool
	located bool
	unbound bool
}

// DefaultContextLines is the number of context lines the %+v verb of an
// [*Error] or a [*SourceError] shows on either side of each marked line.
const DefaultContextLines = 2

// DefaultExcerptWidth is the number of columns an error excerpt shows
// around each location it marks on a line longer than that, unless
// [WithExcerptWidth] sets another for the excerpt.
const DefaultExcerptWidth = 160

// ExcerptOption configures the excerpts of an error, which [FormatError],
// [NewErrorReport], and [SourceError.Excerpt] build. An excerpt is a view
// of the lines of a source around the locations an error marks in it, and
// the options say how many lines it keeps around each one and how much of
// a long line it shows. The renderer of an error chooses both, so one
// program renders the same error with whole lines in a terminal and with
// bounded rows in a log.
//
// Available options:
//   - [WithContextLines]
//   - [WithExcerptWidth]
//
// [go.jacobcolvin.com/niceyaml/printer.Printer.PrintError] takes the
// same options for the excerpts it draws with styles, and
// [go.jacobcolvin.com/niceyaml/printer.Printer.PrintReport] draws the
// report [NewErrorReport] builds with them.
type ExcerptOption func(*excerptConfig)

// excerptConfig holds the settings an [ExcerptOption] configures.
type excerptConfig struct {
	// The lines of unchanged content an excerpt keeps on either side of
	// each marked line.
	context int
	// The columns an excerpt shows around each mark on a long line, where
	// 0 shows every line whole.
	width int
}

// defaultExcerptConfig returns the settings an excerpt has when no
// [ExcerptOption] changes them: [DefaultContextLines] lines of context
// and [DefaultExcerptWidth] columns of width.
func defaultExcerptConfig() excerptConfig {
	return excerptConfig{context: DefaultContextLines, width: DefaultExcerptWidth}
}

// newExcerptConfig returns the settings of opts applied in order over
// [defaultExcerptConfig].
func newExcerptConfig(opts []ExcerptOption) excerptConfig {
	cfg := defaultExcerptConfig()

	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	return cfg
}

// WithContextLines is an [ExcerptOption] that sets how many lines of
// unchanged content an excerpt keeps on either side of each marked line,
// as [line.View.Hunks] keeps them. The default is [DefaultContextLines].
// A count of 0 shows the marked lines alone, and a negative count counts
// as 0:
//
//	log.Print(niceyaml.FormatError(err, niceyaml.WithContextLines(0)))
func WithContextLines(n int) ExcerptOption {
	return func(c *excerptConfig) {
		c.context = max(0, n)
	}
}

// WithExcerptWidth is an [ExcerptOption] that sets how many columns of a
// long line an excerpt shows around each location it marks. The default
// is [DefaultExcerptWidth]. A line of cols columns or fewer prints whole,
// and a longer line prints a window of cols columns around each mark,
// with "..." in place of the rest, as [line.View.Clip] cuts a view. A
// context line with no mark keeps its first cols columns. A cols of 0
// shows every line whole, and a negative cols counts as 0:
//
//	fmt.Print(niceyaml.FormatError(err, niceyaml.WithExcerptWidth(0)))
//
// The width drops the columns outside each window, so it bounds what an
// excerpt adds to a log. To fit rows to a terminal, wrap them with
// [go.jacobcolvin.com/niceyaml/printer.WithWrapWidth] instead. The %+v
// verb of an [*Error] or a [*SourceError] takes no option, so it renders
// at the default width.
func WithExcerptWidth(cols int) ExcerptOption {
	return func(c *excerptConfig) {
		c.width = max(0, cols)
	}
}

// ErrorListLimit is the number of errors [SourceError.Error] lists below
// its first line. A message that holds more ends with a line that counts
// the rest.
//
// It is also the number of problems the %+v verb and the LogValue method
// of an [*Error] or a [*SourceError] show, each with its details, above
// a last row that counts the rest. A logger calls those two for a
// program, so an error with thousands of problems reaches a log as a
// short text. [FormatError] and
// [go.jacobcolvin.com/niceyaml/printer.Printer.PrintError] show every
// problem.
const ErrorListLimit = 10

// boundTexts holds the texts a [SourceError] gives out. The message of a
// binding holds the messages of the bindings below it, and the walk for
// the first line reads them again. Texts built anew for every call would
// rebuild the bindings below once per binding above them, so a binding
// builds its texts once and keeps them.
type boundTexts struct {
	// The text of the bound error without the part the children show, as
	// [chainEnd.cut] returns it, behind the path [SourceError.ownPath]
	// returns, when there is one.
	own string
	// The line of the binding itself: own behind the position or the
	// name. It is empty for a binding with no text of its own, such as
	// one of a join.
	head string
	// The message [SourceError.Error] returns.
	msg string
	// The first lines of the list, [ErrorListLimit] at most.
	entries []string
	// The number of lines the whole list holds, which is zero when msg
	// lists no error below the binding.
	count int
	// The number of children at the end of the list of children whose
	// text own holds already, so the list leaves out each of them that
	// the binding did not locate.
	shown int
	// The number of children at the end of the list of children whose
	// text the message of the bound error holds, as [chainEnd.cut]
	// counts them. It counts the branches of a join too, whose text own
	// lacks.
	held int
}

// binder is where an error binds: the source, and the node that binds
// it, which is nil for [Source.Bind] and for the errors a Source produces
// itself. A binder that routes picks the document for each location it
// resolves, as [Source.Bind] does; one that does not, as the parser's
// binder must not, since the documents are not built until the parse
// ends, binds to the source alone, and [bindSyntaxErrors] gives each error
// of the parse its document afterwards. A binder that locates binds an
// error a caller or a validator gave a Node, so [binder.located] points
// an error that holds no location at that Node. A binder that is
// unplaced binds an error about a value that came from no document, as
// [BindValue] does, so each binding it builds keeps the error it was
// made from for [placeable] and is bound to no node.
type binder struct {
	src      *Source
	node     *Node
	route    bool
	locate   bool
	unplaced bool
}

// located returns err as b binds it at the top of its tree. A binder that
// locates binds through a Node from [Node.At] or [Node.Nodes], and a
// problem that carries no location is then about the value of that Node.
// The error comes back from [Rebase] under [paths.Current], so each such
// problem binds at the Node as an [Error] with [AtPath] of paths.Current
// does, and [binder.scoped] puts the scope in front. Rebase decides
// problem by problem, so a summary, a join, and a detail gain no
// location, and a scoped bind agrees with a Rebase under the path of the
// Node. The error of a context that ended is about the call and comes
// back as it is. So does every error for a binder that does not locate,
// and for one whose node is the root of a document or nil.
func (b binder) located(err error) error {
	if !b.locate || b.node == nil || b.node.base.IsRoot() || contextEnded(err) {
		return err
	}

	return Rebase(err, paths.Current())
}

// scopedError is an error as a binding holds it, with the `@` paths it
// carries under the scope of the binding, and the anchor of that error, as
// [anchorOf] finds it.
type scopedError struct {
	err    error
	anchor anchor
}

// scoped returns err as a binding of b holds it. An error writes its `@`
// paths from the node that binds it, so it comes back under the scope of
// that node, as [underScope] returns it, and its paths start at `$`. The
// scope is the [Node.Path] of the node b binds with, which is
// [paths.Doc] for the root of a document, or paths.Doc when b binds with
// no node, since a path then resolves from the root of a document.
func (b binder) scoped(err error) scopedError {
	scope := paths.Doc()
	if b.node != nil {
		scope = b.node.base
	}

	scoped, _ := underScope(err, scope)

	return scoped
}

// underScope returns err with scope in front of each `@` path it carries,
// and reports whether it changed anything. It finds the anchor of the
// result while it walks err, so the binding reads the chain of err once.
//
// An error whose cause chain reaches an [*Error] that carries an `@` path
// comes back inside an Error from [Rebase] at scope, so [Error.Path]
// reports the joined path. One whose cause chain reaches a `$` path reads
// from the root of the document already, and Rebase would leave that path
// as it is, so the error comes back as it is. A chain with an anchor is no
// join, since a join holds separate problems, so underScope reads whether
// err is a join only for a chain with none. A join, as [joinBranches]
// finds one, comes back as a new join of its branches under scope, as
// Rebase builds one, so each branch carries the joined path of its own.
// Rebase points a problem with no location at its base, and underScope
// leaves such an error as it is. [binder.located] gives each such problem
// the scope before underScope runs, so an error that names no path here
// gains none. A binding resolved its location already and comes back as
// it is.
func underScope(err error, scope paths.Path) (scopedError, bool) {
	if isNothing(err) {
		return scopedError{err: err}, false
	}

	a := anchorOf(err)
	if a.err != nil {
		_, located := a.err.(*Error) //nolint:errorlint // The anchor itself, found by the walk.
		if !located || !a.hasPath || a.path.IsAbsolute() {
			return scopedError{err: err, anchor: a}, false
		}

		// The anchor of the Error from Rebase is the anchor below it, with
		// the base in front of its path, as anchorOf reads it.
		a.locus = a.rebase(scope)

		return scopedError{err: &Error{err: err, move: &move{base: scope}}, anchor: a}, true
	}

	unchanged := scopedError{err: err, anchor: a}

	// An Error that adds nothing to the join it wraps reads as the join.
	x, ok := err.(*Error) //nolint:errorlint // The node itself, not a chain search.
	if ok && x.addsNothing() {
		if _, _, joined := joinBranches(x.err); joined {
			inner, changed := underScope(x.err, scope)
			if !changed {
				return unchanged, false
			}

			// The copy keeps what the Error declared.
			return anchored(&Error{err: inner.err, invalid: x.invalid}), true
		}
	}

	changed := false

	joined := mapJoin(err, func(branch error) error {
		s, c := underScope(branch, scope)
		changed = changed || c

		return s.err
	})
	if joined == nil || !changed {
		return unchanged, false
	}

	return anchored(joined), true
}

// mapJoin applies f to each branch of err that is not nil, when err reads
// as a join, as [joinBranches] reads it, and joins the results again as
// [multierr.Rejoin] does. A branch f maps to nil drops out. It returns
// nil when err reads as no join or every branch drops out.
func mapJoin(err error, f func(error) error) error {
	branches, _, ok := joinBranches(err)
	if !ok {
		return nil
	}

	mapped := make([]error, 0, len(branches))

	for _, branch := range branches {
		if branch == nil {
			continue
		}

		m := f(branch)
		if m != nil {
			mapped = append(mapped, m)
		}
	}

	if len(mapped) == 0 {
		return nil
	}

	return multierr.Rejoin(err, mapped)
}

// anchored returns err with its anchor, as [anchorOf] finds it.
func anchored(err error) scopedError {
	return scopedError{err: err, anchor: anchorOf(err)}
}

// nodeAt returns the node an error on line idx binds to: the one b binds
// with, or, when b routes, the root of the document of the source whose
// span holds the line, whether or not that document parsed. That root is
// nil when the line lies outside the source. The spans of the documents
// run in order and each ends where the next starts, so a search for the
// first span that ends past the line finds the one that holds it.
//
// The Source [NewSourceFromLayers] builds holds one document, and every
// error of that Source binds in a layer through it. A line outside the
// merged text thus gives that document too, so [locateMerged] reads the
// line as one that holds no value.
func (b binder) nodeAt(idx int) *Node {
	if b.node != nil || !b.route {
		return b.node
	}

	docs := b.src.documents()

	if b.src.layers != nil {
		return docs[0]
	}

	i := sort.Search(len(docs), func(i int) bool {
		return docs[i].span.End > idx
	})
	if i < len(docs) && docs[i].span.Contains(idx) {
		return docs[i]
	}

	return nil
}

// BindValue binds err to no document, for a check on a value that came
// from none, such as the body of a request or a value the program built.
// [Error.Error] writes no path, and the error BindValue returns puts each
// path in its text, so it names every failing location through %v,
// inside a wrapper, in a join, and in a log. A summary of two violations
// reads:
//
//	2 violations
//	$.port: 0 is less than 1
//	$.name: name is required
//
// Each path starts at `$`, which here is the value the check ran on, and
// no file and no position stand in front of a line.
// [go.jacobcolvin.com/niceyaml/schema.Schema.ValidateValue] returns its
// violations this way, and [SelfValidateValue] binds what it finds the
// same way.
//
// The result stands in no document, so a document can still place it.
// [Rebase], every Bind, and a decode read the result as the error it was
// made from, as the Errors section of the package documentation
// describes. Placing builds a new error and leaves the result as it is.
//
// The result is a [*SourceError] bound to no [Node], and so is each
// binding below it. Its methods that report a document, a position, or a
// range report false, and [SourceError.Source] returns a source with no
// name and no lines. [SourceError.Unresolved] returns a reason that wraps
// [go.jacobcolvin.com/niceyaml/paths.ErrNoDocument] for a path, and
// [ErrOutOfRange] for a position or a range.
//
// BindValue returns nil for a nil err, and for a nil [*Error] or
// [*SourceError] pointer. An error that is bound already comes back as it
// is, whether a document bound it or BindValue did.
func BindValue(err error) error {
	return bindTree(err, binder{src: valueSource(), route: true, unplaced: true})
}

// bindTree binds err to b. A nil err, or a nil [*Error] or [*SourceError]
// pointer, carries nothing to bind and comes back as a nil error. A
// caller thus compares the result against nil whatever the shape of the
// nil it passed. An error that [isBound] reports is a binding already and
// comes back as it is. Any other error, including an Error above a
// binding that carries a location, heads errors, or holds details, binds
// as a new SourceError.
//
// A binding that stands in no document is the exception. A binder of a
// document binds the error [placeable] returns for it. A binder that is
// unplaced has no document to place it in, so that binding is bound
// already for it.
func bindTree(err error, b binder) error {
	if isNothing(err) {
		return nil
	}

	if b.unplaced && isBound(err) {
		return err
	}

	err = placeable(err)
	if isNothing(err) {
		return nil
	}

	if isBound(err) {
		return err
	}

	bound := newSourceError(b.located(err), b)
	if b.unplaced {
		bound.free = err
	}

	return bound
}

// placeable returns err with a binding that stands in no document, as
// [BindValue] builds one, replaced by the error the binding was made
// from. Bind and [Rebase] thus place such an error in a document as
// they place an error that no source bound yet. Every other binding is a
// binding of a document, and comes back as it is.
//
// The binding may sit below wrappers that each unwrap to one error, such
// as the ones [fmt.Errorf] builds with one %w verb. A wrapper that
// [fmt.Errorf] builds with several %w verbs counts as one of them when
// [followBranches] keeps a single branch of it, as it does for a
// sentinel beside the binding. The text of the binding names the paths
// of its errors, and the text of the error it was made from names none.
// Each wrapper therefore comes back as the stand-in [multierr.Replace]
// returns, whose message is the message of the wrapper with the text of
// the binding cut down to that of its error. A wrapper whose message
// does not hold the text of the binding once, such as one that quotes it
// or writes a message of its own, keeps that message with every path it
// names. Its error places all the same, as an error that no source bound
// yet places under such a wrapper.
//
// An [*Error] writes the message of the error it wraps, so an Error
// above the binding comes back as a copy around the error the binding
// was made from. The copy keeps the location, the details, and the fault
// the Error declared, and binds as it would around an error that no
// source bound yet.
func placeable(err error) error {
	placed, _ := replaceUnplaced(err)

	return placed
}

// replaceUnplaced is [placeable], and it reports whether it replaced a
// binding.
func replaceUnplaced(err error) (error, bool) {
	switch x := err.(type) { //nolint:errorlint // Walks the chain one node at a time.
	case *SourceError:
		if x == nil || x.free == nil {
			return err, false
		}

		return x.free, true

	case *Error:
		if x == nil {
			return err, false
		}

		placed, ok := replaceUnplaced(x.err)
		if !ok {
			return err, false
		}

		// The copy keeps what the Error declared and writes the message of
		// the placed error.
		c := *x
		c.err = placed

		return &c, true

	case interface{ Unwrap() error }:
		return replaceBelow(err, x.Unwrap(), nil)

	case interface{ Unwrap() []error }:
		// Both checks read no message, so a wrapper that holds no such
		// binding, as nearly every one does, costs a walk of its errors
		// and formats nothing.
		if !multierr.IsWrapErrors(err) || !holdsUnplaced(err) {
			return err, false
		}

		branches := x.Unwrap()

		_, below := followBranches(err, branches)

		return replaceBelow(err, below, branches)

	default:
		return err, false
	}
}

// holdsUnplaced reports whether [replaceUnplaced] could find a binding
// that stands in no document below err. It follows each wrapper that
// unwraps to one error, each [*Error] to its cause, and every branch of
// a wrapper that [fmt.Errorf] builds with several %w verbs, and it reads
// the message of none.
func holdsUnplaced(err error) bool {
	for {
		switch x := err.(type) { //nolint:errorlint // Walks the chain one node at a time.
		case *SourceError:
			return x != nil && x.free != nil

		case *Error:
			if x == nil {
				return false
			}

			err = x.err

		case interface{ Unwrap() error }:
			err = x.Unwrap()

		case interface{ Unwrap() []error }:
			return multierr.IsWrapErrors(err) && slices.ContainsFunc(x.Unwrap(), holdsUnplaced)

		default:
			return false
		}
	}
}

// replaceBelow is [replaceUnplaced] for wrapper, an error whose cause
// chain goes on through below. The branches are every error wrapper
// unwraps to when it unwraps to several, and nil otherwise. The ones
// beside below add nothing to the tree, as a sentinel does, so the
// stand-in [multierr.Replace] returns keeps them to match as wrapper
// does. The message of
// wrapper stays as it is unless it holds the message of below once.
func replaceBelow(wrapper, below error, branches []error) (error, bool) {
	if below == nil {
		return wrapper, false
	}

	placed, ok := replaceUnplaced(below)
	if !ok {
		return wrapper, false
	}

	// A message that holds the text below it once shows where that text
	// stands, so the text of the placed error, which names no path, takes
	// its place.
	msg, old := wrapper.Error(), below.Error()
	if old != "" && strings.Count(msg, old) == 1 {
		msg = strings.Replace(msg, old, placed.Error(), 1)
	}

	var beside []error

	for _, branch := range branches {
		if !isNothing(branch) && isLeaf(branch) {
			beside = append(beside, branch)
		}
	}

	// The stand-in unwraps to the placed error and matches the wrapper,
	// since the wrapper still unwraps to the binding.
	return multierr.Replace(wrapper, placed, msg, beside), true
}

// isBound reports whether err is a binding already: a [*SourceError], or
// an error that wraps one along its cause chain with no [*Error] above it
// that carries a location, heads errors as a summary, or holds details.
// The chain follows a wrapper with several %w verbs to the one branch
// [followBranches] keeps of it. Such an Error adds to the tree, so the
// error binds anew around the inner binding.
func isBound(err error) bool {
	for cur := err; ; {
		switch x := cur.(type) { //nolint:errorlint // Walks the chain one node at a time.
		case *SourceError:
			return x != nil

		case *Error:
			if x == nil || x.hasLocation() || x.nests() {
				return false
			}

			cur = x.err

		case interface{ Unwrap() error }:
			cur = x.Unwrap()

		case interface{ Unwrap() []error }:
			_, cur = followBranches(cur, x.Unwrap())

		default:
			return false
		}
	}
}

// leadingBinding returns the binding the first line of the message of err
// comes from, which puts the name or position of its own source there, or
// nil when the first line comes from no binding. The walk follows the
// text [Error.Error] writes rather than the structure [isBound] reads,
// since the errors below an [*Error] never reach its text. A binding
// that leaves its message untouched puts nothing in front of its first
// line, so when [SourceError.passedLead] finds the binding below it that
// wrote that line, the walk returns that one. An Error writes the text of
// its cause, so the walk continues there.
//
// At a join, as [joinBranches] finds one, the walk continues with its
// first branch that is not a nil interface, since the join writes the
// message of each such branch on a line of its own. A nil [*Error] or
// [*SourceError] there writes an empty first line, so no binding leads.
// At a wrapper that [fmt.Errorf] builds with several %w verbs, the walk
// continues with the first branch [followBranches] keeps of it, as it
// does through a wrapper with one %w verb. A sentinel in front of a
// binding thus leaves the binding to lead. Any other error that unwraps
// to several writes a message of its own. The walk continues with its
// first branch that is not a nil interface when that message starts with
// the message of the branch, as a list of the messages joined with "; "
// does. A message that starts some other way, such as with a count of
// the branches, comes from no binding.
func leadingBinding(err error) *SourceError {
	for cur := err; ; {
		switch x := cur.(type) { //nolint:errorlint // Walks the chain one node at a time.
		case *SourceError:
			lead := x.passedLead()
			if lead != nil {
				return lead
			}

			return x

		case *Error:
			if x == nil {
				return nil
			}

			cur = x.err

		case interface{ Unwrap() error }:
			cur = x.Unwrap()

		case interface{ Unwrap() []error }:
			branches := x.Unwrap()

			switch {
			case multierr.IsJoin(cur):
				cur = multierr.First(branches)

			case multierr.IsWrapErrors(cur):
				if multierr.JoinMessage(cur.Error(), branches) {
					cur = multierr.First(branches)

					continue
				}

				kept, next := followBranches(cur, branches)
				if next == nil && len(kept) > 0 {
					next = kept[0]
				}

				cur = next

			default:
				// A message that reads as a join starts with the message
				// of its first branch too, so one read of the message
				// covers both.
				lead := multierr.First(branches)
				if lead == nil || !strings.HasPrefix(cur.Error(), lead.Error()) {
					return nil
				}

				cur = lead
			}

		default:
			return nil
		}
	}
}

// passedLead returns the binding the first line of the message of e comes
// from when [SourceError.Error] leaves the message of e as it is. A
// binding of a source with no name has no name to put in front, so it
// passes on any binding that leads. A binding of a named source passes on
// one that names its source already. A binding of the same source that
// supplies the first line names it. One of another source names that
// source instead, which still stands for e when e has no child bound to
// its own source. A binding of a wrapper around a join whose every branch
// is a binding of another source has none. A binding that adopted another
// follows the same rules, since it writes no position of its own. It
// returns nil for any other e, which puts its own position or name in
// front, for an e that puts its path in front, as [SourceError.ownPath]
// reports, and when no binding wrote the first line.
//
// The message of each binding holds the messages of the bindings below
// it, and the walk for the first line passes through those that leave
// theirs as it is. Finding the lead anew for every message would walk
// the bindings below once per binding above them, so each binding finds
// its lead once and keeps it.
func (e *SourceError) passedLead() *SourceError {
	if e == nil {
		return nil
	}

	e.leadOnce.Do(func() {
		if e.locErr == nil && !e.adopted {
			return
		}

		if _, ok := e.ownPath(); ok {
			return
		}

		lead := leadingBinding(e.err)
		if lead != nil && (e.source.Name() == "" || lead.Source() == e.source || !holdsSource(e, e.source)) {
			e.lead = lead
		}
	})

	return e.lead
}

// ownPath returns the path e puts in front of the text of the error it
// binds and true, or false when it puts none there. It is the path of the
// [*Error] that anchors the bound error, as [anchorOf] finds it, which
// [SourceError.Path] reports too. A binding that adopted another puts no
// path there, since the text of the binding it took over names its own.
func (e *SourceError) ownPath() (paths.Path, bool) {
	a := anchorOf(e.err)

	return a.path, a.hasPath
}

// keepsMessage reports whether [SourceError.Error] returns the message of
// e with no position, name, or document in front. A binding that resolved
// a location of its own writes its position. Any other binding keeps the
// message when the first line names the source already, as
// [SourceError.passedLead] finds, or when [SourceError.place] has nothing
// to put in front.
func (e *SourceError) keepsMessage() bool {
	if e.locErr == nil && !e.adopted {
		return false
	}

	return e.passedLead() != nil || e.place() == ""
}

// place returns what stands in front of the message of a binding that
// writes no position: the name of its source, then the document
// [SourceError.documentLabel] returns, each behind a colon, as
// "cafe.yaml: document 3:". Either part stays out when e has none, and a
// binding with neither has an empty place.
func (e *SourceError) place() string {
	name, doc := e.source.label(), e.documentLabel()

	switch {
	case doc == "":
		return suffixed(name)
	case name == "":
		return suffixed(doc)
	default:
		return name + ": " + doc + ":"
	}
}

// suffixed returns s with a colon after it, or "" for an empty s.
func suffixed(s string) string {
	if s == "" {
		return ""
	}

	return s + ":"
}

// documentLabel returns the words that name the document of e in a
// message with no position, as "document 3" does for the third document
// of a file. No line says which document such an error is about, where a
// position says it for every other error. The label is empty for a
// binding that resolved a location, for one bound to no Node, and for
// one in a source that holds a single document, which needs no telling
// apart. The label counts every document of the file, as
// [Source.AllDocuments] returns them, so the number is the place of the
// document in the file.
func (e *SourceError) documentLabel() string {
	if e.locErr == nil || e.node == nil {
		return ""
	}

	if len(e.node.source.documents()) < 2 {
		return ""
	}

	return "document " + oneBased(e.node.doc.index)
}

// holdsSource reports whether a child of e is bound to src. A child that
// reads as the binding of a join, as [joinBinding] finds one, has no text
// of its own, so the walk looks through it to the branches of that
// binding, as [appendBoundChildren] does.
func holdsSource(e *SourceError, src *Source) bool {
	for _, c := range e.below {
		joined := joinBinding(c.bound)
		if joined != nil {
			if holdsSource(joined, src) {
				return true
			}

			continue
		}

		if c.bound.Source() == src {
			return true
		}
	}

	return false
}

// anchor is the error along a cause chain that carries the location, and
// the location it carries. Its path has the base of every Error from
// [Rebase] above it joined in front. A [*SourceError] resolved its
// location already, so its anchor holds the zero locus. The zero anchor
// is a chain that holds none.
type anchor struct {
	err error
	locus
}

// anchorOf returns the anchor of err: the first located [*Error] along
// its cause chain, or the first [*SourceError]. An Error from [Rebase]
// with no anchor below it is the anchor, located at its base, when the
// chain below it is one problem and the Rebase points at its base. A
// chain that holds a summary from [NewSummary], or ends at an error that
// unwraps to at least one branch [followBranches] keeps, is a heading
// rather than a problem, so it takes no location from the base. A join
// whose every branch is a nil pointer heads nothing, so it is one
// problem. The chain follows a wrapper to the one error it wraps and an
// Error to its cause. It follows a wrapper with several %w verbs to the
// one branch followBranches keeps of it. Any other error that unwraps to
// several, such as one from [errors.Join], ends the chain. Such an error
// carries no location of its own, and each of its branches binds as a
// child.
func anchorOf(err error) anchor {
	a, _ := findAnchor(err)

	return a
}

// findAnchor returns the anchor of err, as [anchorOf] describes it, and
// whether the chain of err is a heading over separate problems. The
// second result counts only when the anchor is the zero anchor. One walk
// of the chain answers both, so a chain of Errors from [Rebase] reads the
// chain below each once.
func findAnchor(err error) (anchor, bool) {
	switch x := err.(type) { //nolint:errorlint // Walks the chain one node at a time.
	case *SourceError:
		if x == nil {
			return anchor{}, false
		}

		return anchor{err: x}, false

	case *Error:
		if x == nil {
			return anchor{}, false
		}

		if x.hasLocation() {
			l := x.locus()
			if x.move != nil {
				l = x.move.apply(l)
			}

			return anchor{err: x, locus: l}, false
		}

		a, heading := findAnchor(x.err)
		if a.err != nil {
			if x.move != nil {
				a.locus = x.move.apply(a.locus)
			}

			return a, false
		}

		heading = heading || len(x.errors) > 0

		if x.move != nil && !x.move.movesOnly && !heading {
			return anchor{err: x, locus: x.move.at()}, false
		}

		return anchor{}, heading

	case interface{ Unwrap() error }:
		return findAnchor(x.Unwrap())

	case interface{ Unwrap() []error }:
		branches, next := followBranches(err, x.Unwrap())
		if next == nil {
			return anchor{}, len(branches) > 0
		}

		return findAnchor(next)

	default:
		return anchor{}, false
	}
}

// boundLocus returns the location e resolved its position from. Its path
// is the one [SourceError.Path] reports for e, with the base of every
// [Rebase] inside e joined in front and no base from an Error above e. A
// binding that wraps another reports the one it wraps.
func boundLocus(e *SourceError) locus {
	found := anchorOf(e.err)

	if inner, ok := found.err.(*SourceError); ok { //nolint:errorlint // The anchor itself, found by the walk.
		return boundLocus(inner)
	}

	return found.locus
}

// newSourceError binds err to b and resolves its location, with a path
// resolving in the document of b. The binding holds err as
// [binder.scoped] returns it, so each path of the bound error starts at
// `$` and reads from the root of the document. The children of err bind
// the same way, each under the scope on its own, so a child that carries
// no path stays as it is. A binding takes the source of the node its
// location resolved in. That is the source of b, unless b binds in the
// document [NewSourceFromLayers] builds, where a location resolves in a
// layer of another source. A binder that is unplaced binds to no node,
// since its error stands in no document.
func newSourceError(err error, b binder) *SourceError {
	scoped := b.scoped(err)
	found := scoped.anchor

	e := &SourceError{err: scoped.err, source: b.src, node: b.node, locErr: errUnlocated}

	switch a := found.err.(type) { //nolint:errorlint // The anchor itself, found by the walk.
	case *Error:
		e.loc, e.node, e.locErr = locate(b, found.locus)

		// The path looked in the one document of an empty source, and the
		// binding stands in none.
		if b.unplaced {
			e.node = nil
		}

		if e.node != nil {
			e.source = e.node.source
		}

		// The path of the error reads from the value the layers hold.
		// The binding reports it from the root of the document of the
		// layer it bound in, as a bind through that layer would.
		if root := e.loc.layerRoot; root != nil && found.hasPath && !root.IsRoot() {
			e.err = &Error{err: e.err, move: &move{base: *root, movesOnly: true, reroots: true}}
		}

	case *SourceError:
		// The error wraps a binding, so it is that binding with more
		// around it. It takes over the location and source the binding
		// resolved, and its message carries the position the binding put
		// there already.
		e.adopted = true
		e.source = a.source
		e.node = a.node
		e.loc, e.locErr = a.loc, a.locErr
		e.rng, e.ranges = a.rng, a.ranges
	}

	if !e.adopted {
		// No error binds in the text [mergeLayers] merged, which is no file of
		// the caller. One that found no layer binds in the lowest.
		if home := e.source.layers.home(); home.source != nil {
			e.source, e.node = home.source, home.node
		}

		if e.locErr == nil {
			e.locErr = checkInRange(e.loc, e.source.lines)
		}

		if e.locErr == nil {
			e.ranges = e.loc.highlight(e.source.lines)
			e.rng = excerpt.Cover(e.ranges, e.loc.pos)
		}
	}

	e.collect(err, b)

	return e
}

// collect binds the children of the error e binds, the ones [walkChildren]
// finds along its cause chain, and keeps the end of that chain. A
// [*SourceError] that ends the chain is the cause of the error above it
// rather than a violation of its own, so its problems and details join
// those of e, and every other child binds through [SourceError.addChild].
func (e *SourceError) collect(err error, b binder) {
	e.end = walkChildren(err,
		func(x *SourceError) { e.below = append(e.below, x.below...) },
		func(n error, base childBase, detail bool) { e.addChild(n, b, base, detail) },
	)
}

// addChild binds n as a child of e, among its details when detail is set
// and among the problems it heads otherwise. A binding is the child as it
// is, and any other error binds where e binds, or takes over the binding
// it wraps, as a binding that [markInvalid] marked does. The base is the
// base of every Error from [Rebase] above n, and a child under an Error
// from Rebase binds as a rebased Error at that base, the root included,
// so its own message and [SourceError.Path] carry the joined path as the
// message of the root does. A nil n, or a nil pointer, adds nothing.
func (e *SourceError) addChild(n error, b binder, base childBase, detail bool) {
	// The base returns a binding as it is unless it marks the binding
	// invalid, so the child is a binding when n is one that gains no mark.
	// A binding that stands in no document binds here as its error does.
	child := base.rebase(placeable(n), detail)
	if isNothing(child) {
		return
	}

	bound, ok := child.(*SourceError) //nolint:errorlint // The node itself, not a chain search.
	if !ok {
		bound = newSourceError(child, b)

		// A child that took over a binding of a document stands where
		// that binding does.
		if b.unplaced && !bound.adopted {
			bound.free = child
		}
	}

	// A binding that takes its location from an Error never reports
	// errUnlocated, which only an error with no location gets.
	unbound := !ok && !bound.adopted
	located := unbound && !errors.Is(bound.locErr, errUnlocated)

	e.below = append(e.below, boundChild{bound: bound, detail: detail, located: located, unbound: unbound})
}

// heads reports whether e heads separate problems: whether a child of e
// is no detail.
func (e *SourceError) heads() bool {
	return slices.ContainsFunc(e.below, func(c boundChild) bool { return !c.detail })
}

// Source returns the [*Source] the error is bound to. A nil SourceError is
// bound to none.
func (e *SourceError) Source() *Source {
	if e == nil {
		return nil
	}

	return e.source
}

// Node returns the [*Node] the error is bound to and true, or nil and
// false for an error bound to none. The Node is the one whose methods and
// validators produced the error or whose [Node.Bind] bound it, and an `@`
// path in the error resolves from it. [Source.Bind] binds to the root of
// the document the location falls in, and the error stays bound to that
// root when its location does not resolve there. A position or a range
// falls in the document whose [Node.Span] holds its line, whether or not
// that document parsed, and a path falls in the one document of the
// source. Source.Bind binds to none an error with no location, a position
// or a range on a line no document holds, and a path in a source that
// holds several documents or has a YAML syntax error.
//
// A YAML syntax error is bound to the root of the document whose span
// holds its location, or to the first document it fails when it has no
// location in the source. An error that wraps a binding keeps the Node of
// that binding. An error bound in a merged [Source] is bound to the Node
// of the layer its location resolved in, or of the lowest layer when it
// carries no location. The error [BindValue] returns and each binding
// below it are bound to none, and so is a nil SourceError.
func (e *SourceError) Node() (*Node, bool) {
	if e == nil || e.node == nil {
		return nil, false
	}

	return e.node, true
}

// Document returns the root [*Node] of the document the error is bound to
// and true, or nil and false where [SourceError.Node] reports false. The
// root is the [Node.Document] of the Node that SourceError.Node returns,
// the one [Source.AllDocuments] returns at the index of the document. The
// syntax error of the second document of three thus returns the root of
// that document, as a violation there does. [SourceError.DocumentIndex]
// reports the index of the document.
func (e *SourceError) Document() (*Node, bool) {
	node, ok := e.Node()
	if !ok {
		return nil, false
	}

	return node.Document(), true
}

// DocumentIndex returns the 0-indexed position within the file of the
// document the error is bound to and true, or 0 and false where
// [SourceError.Document] reports false. It is the [Node.DocumentIndex] of
// that root. The message of a bound error counts documents from 1, as it
// counts lines, so "document 3" in [SourceError.Error] is the document at
// index 2. A report that groups the problems of a file by document reads
// the index of each:
//
//	for problem := range niceyaml.Problems(err) {
//		if index, ok := problem.DocumentIndex(); ok {
//			byDocument[index] = append(byDocument[index], problem.Message())
//		}
//	}
func (e *SourceError) DocumentIndex() (int, bool) {
	doc, ok := e.Document()
	if !ok {
		return 0, false
	}

	return doc.DocumentIndex(), true
}

// Message returns the text of the bound error with no position, document,
// or path in front. It is the message [NewError] or [Invalid] gave an
// [*Error], or the text of any other error as it is, with any text a
// wrapper such as [fmt.Errorf] added around it. [SourceError.Excerpt]
// annotates a location with it, and a structured report carries it beside
// [SourceError.Position] and [SourceError.Path], as [Problems] describes.
// The errors [SourceError.Error] lists below the message are not part of
// it, so the message of a summary is the summary. A binding of a join has
// no line of its own, and its message is the text of the join. A nil
// SourceError has an empty message.
func (e *SourceError) Message() string {
	return e.text()
}

// Path returns the [paths.Path] the bound error is about and true, or the
// zero Path and false when it carries none. It is the path [Error.Path]
// reports for the [*Error] that gave the binding its location, with the
// [Node.Path] of the Node that bound it in front of an `@` path, so it
// starts at `$`. An error written as `@.price` and bound through the Node
// at `$.items[1]` reports `$.items[1].price`. A rejection of the go-yaml
// decoder reports the path where the document writes the value, as
// [Node.DecodeInto] describes, which can lie outside the scope of the
// Node. An error that binds at a range or a position beside its path
// reports that path, resolved or not. A binding that wraps another
// reports the path of the one it wraps, and a nil SourceError has none.
func (e *SourceError) Path() (paths.Path, bool) {
	if e == nil {
		return paths.Path{}, false
	}

	l := boundLocus(e)

	return l.path, l.hasPath
}

// Unwrap returns the error the [SourceError] binds. A nil SourceError
// unwraps to nothing, so a chain that holds one is safe to walk.
func (e *SourceError) Unwrap() error {
	if e == nil {
		return nil
	}

	return e.err
}

// Error returns the message of the bound error with its resolved position
// in front, as "name:line:col: $.path: msg" when the error carries a path
// and "name:line:col: msg" when it does not. The name is [Source.Name],
// and a source with no name leads with "line:col:". An error with no
// position reads "name: msg". In a source that holds several documents,
// such an error names its document behind the name, counted from 1 among
// [Source.AllDocuments], as "name: document 3: msg". The text a wrapper
// added to the bound error sits behind the path. A first line that a
// binding of the same source wrote already gains no second position.
//
// The message holds one line per problem, and details stay out. A
// heading, which is a summary, a join, or a wrapper around either, lists
// the problems it heads below its own line in the order of their
// positions, [ErrorListLimit] at most, with a last line that counts the
// rest:
//
//	cafe.yaml: 2 schema violations
//	cafe.yaml:6:8: $.spec.sla: string does not match pattern
//	cafe.yaml:22:11: $.spec.hours.days: expected "array", got "string"
//
// A join has no line of its own. Any other error that unwraps to several
// keeps its own message, and each branch follows as a line unless that
// message holds its text already, as "a; b" does. A branch located from
// an [*Error] follows either way, since its line names a position the
// message lacks.
//
// The name, each path, and the text of a decode rejection reach the
// message with their control characters drawn or escaped, while a message
// the caller wrote stays as it is. The message holds no source lines, so
// it stays short in a log. Error builds the message on its first call and
// returns it from then on. A nil SourceError has an empty message.
func (e *SourceError) Error() string {
	return e.texts().msg
}

// headline returns the line of e itself, without the errors below it:
// the text [NewErrorTree] gives the node of e. It is empty for a binding
// with no text of its own.
func (e *SourceError) headline() string {
	return e.texts().head
}

// texts returns the texts of e, which it builds the first time it is
// called. A nil e has none.
func (e *SourceError) texts() boundTexts {
	if e == nil {
		return boundTexts{}
	}

	e.builtOnce.Do(func() { e.built = e.buildTexts() })

	return e.built
}

// buildTexts builds the texts of e. A binding that heads problems lists
// them under its own line, and a binding with no text of its own, such as
// one of a join, is the list alone. Any other binding is one problem, and
// its message is the text of the bound error behind its position and its
// path, with none of its details. The text of the bound error shows the
// errors below it already when the cause chain ends at a join or a
// binding, so the line of e is that text without them, as [chainEnd.cut]
// returns it. When a wrapper rewrote that text, nothing can cut the
// errors out of it, so the message stays the text as the wrapper wrote
// it.
func (e *SourceError) buildTexts() boundTexts {
	var (
		t    boundTexts
		full string
		cut  = true
	)

	// A Node that binds a join through a scope binds a new join of the
	// scoped branches, so the bound error says whether it is a join, where
	// the end of the chain is the join the caller gave. The message of a
	// join is the messages of the bindings below it, and the list stands
	// in for it, so only a join that lists nothing reads it. Reading it
	// for a join bound at each step of a loop would build the list of
	// every join below.
	_, _, joined := joinBranches(e.err)
	if joined {
		t.held = e.end.branches
	} else {
		full = e.err.Error()
		t.own, t.shown, t.held, cut = e.end.cut(full)
	}

	if cut && e.heads() {
		t.entries, t.count = e.list(t.shown)
	}

	path, hasPath := e.ownPath()
	pathed := func(msg string) string {
		if !hasPath {
			return msg
		}

		return withPath(path, msg)
	}

	// No error below e reaches the list, so the text stands as it is.
	if t.count == 0 {
		if joined {
			full = e.err.Error()
		}

		if t.own == "" {
			t.own = full
		}

		t.own = pathed(t.own)
		t.head = e.prefixed(t.own)
		t.msg = e.prefixed(pathed(full))

		return t
	}

	t.own = pathed(t.own)

	lines := make([]string, 0, len(t.entries)+2)

	if t.own != "" {
		t.head = e.prefixed(t.own)
		lines = append(lines, t.head)
	}

	lines = append(lines, t.entries...)

	if more := t.count - len(t.entries); more > 0 {
		lines = append(lines, e.more(more))
	}

	t.msg = strings.Join(lines, "\n")

	return t
}

// bare reports whether e has neither a message nor a position, so it
// adds no line to the list of the binding above it.
func (e *SourceError) bare() bool {
	t := e.texts()

	return t.count == 0 && t.own == "" && (e.locErr != nil || e.adopted)
}

// more returns the line that ends a list which leaves n errors out. It
// names the source of e, as every other line of the list names its own.
func (e *SourceError) more(n int) string {
	rest := "and " + strconv.Itoa(n) + " more"

	name := e.source.label()
	if name == "" {
		return rest
	}

	return name + ": " + rest
}

// withPath returns msg behind p, as "$.path: msg", or p and a colon alone
// for an empty msg.
func withPath(p paths.Path, msg string) string {
	return prefix(p.String()+":", msg)
}

// prefixed returns msg behind the position e resolved to, behind the name of
// its source and the document it belongs to, or as it is, by the rules
// [SourceError.Error] documents. The path goes in front of msg before
// prefixed runs, so it stands between the position and the text.
func (e *SourceError) prefixed(msg string) string {
	switch {
	case e.locErr == nil && !e.adopted:
		return prefix(formatPosition(e.source.label(), e.loc.pos), msg)

	case e.keepsMessage():
		return msg

	default:
		return prefix(e.place(), msg)
	}
}

// list returns the lines [SourceError.Error] lists below the line of e,
// [ErrorListLimit] of them at most, and the number of lines the whole
// list holds. The problems e heads come in the order [NewErrorTree]
// shows them, as [SourceError.ordered] returns them, without those among
// the last shown of them that e did not locate, whose text the line of e
// holds already. Each gives its own line. One that heads problems of its
// own gives their lines after its own. The list thus reads as the rows
// [ErrorTree.Problems] yields, with the line of each heading above its
// own.
func (e *SourceError) list(shown int) ([]string, int) {
	var (
		entries []string
		count   int
	)

	add := func(lines ...string) {
		count += len(lines)
		entries = append(entries, lines[:min(len(lines), ErrorListLimit-len(entries))]...)
	}

	for _, child := range e.ordered(shown) {
		t := child.texts()

		switch {
		case child.bare():
			continue

		case t.count == 0:
			add(t.msg)

			continue
		}

		if t.head != "" {
			add(t.head)
		}

		add(t.entries...)

		count += t.count - len(t.entries)
	}

	return entries, count
}

// ordered returns the problems e heads in the order [NewErrorTree] shows
// them, without those among the last shown of them that e did not locate.
// The text of e holds the text of each of the last shown, and it names
// the location of none of them, so a problem e located keeps its line.
// The text e holds for an unbound child among them is the text that child
// had before e bound it, which lists no problem below the child. Those
// problems thus stand in place of the child, without the ones that text
// holds and the child did not locate, as [boundTexts.held] counts them.
// That order is by position within the source each is bound to, as
// [trees] sorts them, with the branches of a problem that binds a join in
// place of that problem.
func (e *SourceError) ordered(shown int) []*SourceError {
	held := len(e.below) - shown

	var listed []*SourceError

	for i, c := range e.below {
		switch {
		case c.detail:
			continue

		case i < held || c.located:
			listed = append(listed, c.bound)

		case c.unbound:
			// A child that lists nothing under its own line gives no
			// line here, as one a wrapper rewrote does not.
			if t := c.bound.texts(); t.count > 0 {
				listed = append(listed, c.bound.ordered(t.held)...)
			}
		}
	}

	children := make([]boundChild, 0, len(listed))
	for _, bound := range listed {
		children = append(children, boundChild{bound: bound})
	}

	// A detail is no problem, and the order numbers each source where it
	// first appears among the problems alone.
	kids := slices.DeleteFunc(flatten(nil, children, e, false), func(kid positioned) bool {
		return kid.detail || kid.underDetail
	})

	kids = arrange(kids)

	out := make([]*SourceError, 0, len(kids))
	for i := range kids {
		out = append(out, kids[i].bound)
	}

	return out
}

// formatPosition returns pos as "name:line:col:", the shape editors and
// build tools read, or "line:col:" when name is empty.
func formatPosition(name string, pos position.Position) string {
	if name == "" {
		return editorPosition(pos) + ":"
	}

	return name + ":" + editorPosition(pos) + ":"
}

// editorPosition returns pos as "line:col" counted from 1, as editors
// and build tools count, where [position.Position.String] counts from 0
// as the fields do.
func editorPosition(pos position.Position) string {
	return oneBased(pos.Line) + ":" + oneBased(pos.Col)
}

// oneBased returns the decimal text of n + 1, which turns an index
// counted from 0 into one counted from 1. The sum for the largest int
// does not fit in an int, so it prints through a uint64 rather than
// wrapping around to a negative number.
func oneBased(n int) string {
	if n == math.MaxInt {
		return strconv.FormatUint(uint64(n)+1, 10)
	}

	return strconv.Itoa(n + 1)
}

// prefix returns p and msg separated by a space, or the other one alone
// when either is empty.
func prefix(p, msg string) string {
	if p == "" || msg == "" {
		return p + msg
	}

	return p + " " + msg
}

// isNothing reports whether err is nil or a nil [*Error] or [*SourceError]
// pointer, which carries no message and no location.
func isNothing(err error) bool {
	switch x := err.(type) { //nolint:errorlint // The node itself, not a chain search.
	case nil:
		return true
	case *Error:
		return x == nil
	case *SourceError:
		return x == nil
	default:
		return false
	}
}

// text returns the text the excerpt annotates the location of e with: the
// message of the bound error with no position or path in front, since the
// caret marks the location. It comes without the part the errors below e
// show, as [chainEnd.cut] returns it, and a message that is all such a
// part, as the message of a join is, stays whole. An Error that wraps a
// binding e adopted annotates with the text of that binding, the one
// [textBinding] finds, since the caret marks the location of the binding.
func (e *SourceError) text() string {
	if e == nil {
		return ""
	}

	e = textBinding(e)

	return bareMessage(e.err, e.end)
}

// bareMessage returns the message of err without the part the errors
// below err show, which end, the [chainEnd] of the cause chain of err,
// cuts out. A message that is all such a part, as the message of a join
// is, stays whole.
func bareMessage(err error, end chainEnd) string {
	msg := err.Error()
	if own, _, _, _ := end.cut(msg); own != "" {
		return own
	}

	return msg
}

// all yields e and every binding below it in depth-first order, each
// once however many times the tree reaches it, and reports whether the
// caller wants more. A nil e yields nothing.
func (e *SourceError) all(seen map[*SourceError]bool, yield func(*SourceError) bool) bool {
	if e == nil || seen[e] {
		return true
	}

	seen[e] = true

	if !yield(e) {
		return false
	}

	for _, c := range e.below {
		if !c.bound.all(seen, yield) {
			return false
		}
	}

	return true
}

// Bindings returns an iterator over each [*SourceError] reached through
// the wrappers and joins around err, in depth-first order, so the
// outermost comes first and each branch of an [errors.Join] follows the
// one before it. It does not look below a binding, so each binding comes
// as one unit. A caller that renders each binding on its own, such as one
// section per document of a file, walks it:
//
//	for bound := range niceyaml.Bindings(err) {
//		fmt.Println(niceyaml.FormatError(bound))
//	}
//
// A nil err has no bindings.
func Bindings(err error) iter.Seq[*SourceError] {
	return func(yield func(*SourceError) bool) {
		for below := range errorsBelow(err) {
			x, ok := below.(*SourceError) //nolint:errorlint // The node itself, not a chain search.
			if ok && !yield(x) {
				return
			}
		}
	}
}

// errorsBelow returns an iterator over err and every error below it, in
// depth-first order. Below an [*Error] it reaches the error the Error
// wraps, then each error a summary heads, then each detail, which
// [Error.Unwrap] leaves out. Below any other error it reaches each error
// that error wraps. It does not look below a [*SourceError], whose
// children the binding holds itself. A nil err, and a nil pointer, yield
// nothing.
func errorsBelow(err error) iter.Seq[error] {
	return func(yield func(error) bool) {
		eachBelow(err, yield)
	}
}

// eachBelow yields the errors [errorsBelow] yields for err, and reports
// whether yield wanted more. It passes yield down rather than ranging
// over the iterator of each error below, which would take time quadratic
// in the depth of a long chain of joins.
func eachBelow(err error, yield func(error) bool) bool {
	if isNothing(err) {
		return true
	}

	if !yield(err) {
		return false
	}

	switch x := err.(type) { //nolint:errorlint // Walks the tree one node at a time.
	case *SourceError:
		return true

	case *Error:
		if !eachBelow(x.err, yield) {
			return false
		}

		for _, inner := range x.errors {
			if !eachBelow(inner, yield) {
				return false
			}
		}

		for _, inner := range x.details {
			if !eachBelow(inner, yield) {
				return false
			}
		}

	case interface{ Unwrap() error }:
		return eachBelow(x.Unwrap(), yield)

	case interface{ Unwrap() []error }:
		for _, inner := range x.Unwrap() {
			if !eachBelow(inner, yield) {
				return false
			}
		}
	}

	return true
}

// allBindings returns an iterator over every binding in the tree of
// err: each [*SourceError] [Bindings] yields, in the same order, and
// every binding below each one. A parent comes before the bindings under
// it, and the children of a child come right after it, whatever source
// each binds to. A binding the tree reaches twice, such as one bound
// before a summary headed it and joined beside that summary, comes once.
// It yields a summary beside the violations under it and each detail
// beside the error it explains, and it passes over every error bound to
// no source. A nil err has no bindings.
func allBindings(err error) iter.Seq[*SourceError] {
	return func(yield func(*SourceError) bool) {
		seen := make(map[*SourceError]bool)

		for x := range Bindings(err) {
			if !x.all(seen, yield) {
				return
			}
		}
	}
}

// Format implements [fmt.Formatter].
//
// The %v and %s verbs print [SourceError.Error]. The %+v verb prints what
// [FormatError] renders for the error at [DefaultContextLines] lines of
// context and [DefaultExcerptWidth] columns, with [ErrorListLimit]
// problems at most and a last row that counts the rest. Every other verb
// formats [SourceError.Error] as it formats a string, with the width,
// precision, and flags given, so %q quotes the message. A wrapper such as
// [fmt.Errorf] around a SourceError formats as its own message under
// every verb, so a program that holds any error calls FormatError for the
// excerpt. A source with excerpts off, as [WithExcerpts] sets it, shows
// no line under any verb.
func (e *SourceError) Format(f fmt.State, verb rune) {
	formatVerb(f, verb, e)
}

// LogValue implements [slog.LogValuer].
//
// The value is the tree [FormatError] prints for the error, as a string
// with each error behind its own position and no source excerpt, with
// [ErrorListLimit] problems at most and a last row that counts the rest.
// A wrapper such as [fmt.Errorf] around a SourceError logs as its own
// message, which holds the list [SourceError.Error] returns.
func (e *SourceError) LogValue() slog.Value {
	return logValue(e)
}

// FormatError renders err as plain text for a log or a terminal without
// color. Its message comes first as the tree [NewErrorTree] builds, with
// a connector in front of each error below another and each error behind
// the position its location resolved to:
//
//	cafe.yaml: 2 schema violations
//	|-- 6:8: $.spec.sla: string does not match pattern
//	`-- 22:11: $.spec.hours.days: expected "array", got "string"
//
// An excerpt follows for each source the bindings in err touch, led by
// the name of its source when there are several. Each excerpt shows the
// marked lines with context lines on either side, carets under each
// location, and the message of each error below a binding beside its
// caret. The context lines and the width of long lines come from opts, as
// [ExcerptOption] describes. A line starting "no excerpt:" then follows
// for each binding [Bindings] yields whose tree resolves no location,
// with the reason [SourceError.Unresolved] returns for it. A binding with
// no reason, and one whose reason wraps
// [go.jacobcolvin.com/niceyaml/paths.ErrNoDocument], gets no such line. A
// source with excerpts off, as [WithExcerpts] sets it, adds no excerpt and
// no line in its place.
//
// FormatError looks through the wrappers and joins around a [SourceError]
// and renders every problem, however many err holds. An error bound to no
// source renders as its tree alone, with the path of its [*Error] in
// front, as "@.open: closes before it opens". An error whose tree renders
// nothing renders its message instead. The output holds no escape
// sequences, since control characters render as their pictures and a tab
// as four spaces. A nil err renders as "".
func FormatError(err error, opts ...ExcerptOption) string {
	return formatError(err, newExcerptConfig(opts), 0)
}

// formatError renders err as [FormatError] documents it, from the report
// [newErrorReport] builds for err with the excerpt settings and limit. The tree comes
// first, as [renderErrorTree] lays it out. Each excerpt follows as
// [line.View.String] renders it, below the name of its source when the
// excerpt has Named set. A line starting "no excerpt:" then gives the
// reason of each unresolved binding.
func formatError(err error, cfg excerptConfig, limit int) string {
	rep := newErrorReport(err, cfg, limit)

	parts := make([]string, 0, 1+len(rep.Excerpts)+len(rep.Unresolved))
	parts = append(parts, renderErrorTree(rep.Tree))

	for _, shown := range rep.Excerpts {
		part := shown.View.String()

		// The name is the caller's text, so its control characters render
		// as pictures like those of the tree.
		if shown.Named {
			part = shown.Source.label() + "\n" + part
		}

		parts = append(parts, part)
	}

	for _, bound := range rep.Unresolved {
		// The output promises no escape sequences, so the reason draws
		// its control characters as pictures like those of the tree,
		// whatever text it holds. A tab becomes four spaces, as it does
		// in the tree.
		parts = append(parts, "no excerpt: "+escape.Line(bound.Unresolved().Error()))
	}

	return joinParts(parts...)
}

// renderErrorTree lays t out as plain text: the text of the root, then
// each child behind a connector, "|-- " for a child with a sibling after
// it and "`-- " for the last, with the children of a child indented
// under its connector. A row of a text after its first sits under the
// text rather than the connector. A root without text, which stands for
// several errors and adds no message of its own, has no row of its own,
// so its children lead.
//
// Each text lays out as [escape.Message] returns it. Control characters
// render as their pictures, so text the library did not write, such as
// the message of the program's own validator, cannot write to the
// terminal, and the output holds no escape sequences as [FormatError]
// promises. A tab becomes four spaces instead, since it lays out the text
// after it.
func renderErrorTree(t ErrorTree) string {
	var sb strings.Builder

	if t.Label != "" {
		sb.WriteString(escape.Message(t.Label))
	}

	writeErrorBranches(&sb, t.Children, "", t.Label != "")

	return sb.String()
}

// formatVerb formats err for verb, as [Error.Format] and
// [SourceError.Format] describe. The %+v verb writes what [FormatError]
// renders, with [ErrorListLimit] problems at most, and every other verb
// formats the message as a string.
func formatVerb(f fmt.State, verb rune, err error) {
	switch {
	case verb == 'v' && f.Flag('+'):
		writeString(f, formatError(err, defaultExcerptConfig(), ErrorListLimit))

	default:
		//nolint:errcheck // Formatter has no error channel.
		_, _ = fmt.Fprintf(f, fmt.FormatString(f, verb), err.Error())
	}
}

// logValue returns the tree of err as [renderErrorTree] lays it out, with
// [ErrorListLimit] problems at most and a last row that counts the rest,
// as [limitTree] cuts it. When that tree has no row, such as for a bound
// join of typed-nil errors, it renders the tree [messageTree] returns in
// its place, as an [ErrorReport] with nothing else to draw holds it.
func logValue(err error) slog.Value {
	tree, _ := limitTree(NewErrorTree(err), ErrorListLimit)

	return slog.StringValue(renderErrorTree(messageTree(tree, err)))
}

// writeErrorBranches writes children behind their connectors, indented
// by indent, with a line break before each unless the first is the first
// row of the output.
func writeErrorBranches(sb *strings.Builder, children []ErrorTree, indent string, broken bool) {
	for i, child := range children {
		connector, below := "|-- ", "|   "
		if i == len(children)-1 {
			connector, below = "`-- ", "    "
		}

		for j, row := range strings.Split(escape.Message(child.Label), "\n") {
			if broken {
				sb.WriteByte('\n')
			}

			broken = true

			if j == 0 {
				sb.WriteString(indent + connector + row)
			} else {
				sb.WriteString(indent + below + row)
			}
		}

		writeErrorBranches(sb, child.Children, indent+below, broken)
	}
}

// writeString writes s to f. It drops write errors, as [fmt] itself does
// for a [fmt.Formatter].
func writeString(f fmt.State, s string) {
	_, _ = io.WriteString(f, s) //nolint:errcheck // Formatter has no error channel.
}

// Range returns the range in the source that the error points at and
// true. It is the range the error carries, or the content of the token at
// its position or at the node its path resolves to, across several lines
// for a token that spans them. For a position from [AtPosition] inside a
// token, on the spaces before it, or on a later line of a multi-line
// token, the range starts where the content of the token starts, so it
// can differ from [SourceError.Position]. For a path the document leaves
// out, or one that enters an alias it cannot follow, the range covers the
// token [SourceError.Nearest] describes. The range is in the coordinates
// of [Source.Lines].
//
// Range reports false when the error carries no location or its location
// did not resolve, as [SourceError.Unresolved] explains, and for a nil
// SourceError.
func (e *SourceError) Range() (position.Range, bool) {
	if e == nil || e.locErr != nil {
		return position.Range{}, false
	}

	return e.rng, true
}

// Position returns the position [SourceError.Error] puts in front of the
// message and true. It counts from 0 as [position.Position] does, so a
// message that opens with "3:9:" has the position of line 2, column 8.
// For a range or a path, it is the start of [SourceError.Range]. A
// position from [AtPosition] comes back as the error gave it, where Range
// starts at the content of the token the position falls in. A report
// that names the place of an error as its message does reads Position,
// and one that marks the text reads Range.
//
// Position reports false when Range does, for an error that carries no
// location or whose location did not resolve. A nil SourceError has no
// position.
func (e *SourceError) Position() (position.Position, bool) {
	if e == nil || e.locErr != nil {
		return position.Position{}, false
	}

	return e.loc.pos, true
}

// Unresolved returns why the location of the bound error did not
// resolve. It returns nil when the location resolved, and when the error
// carries no location at all, as an error from [fmt.Errorf] bound through
// the root of a document does. The reason is one of these:
//
//   - [ErrOutOfRange] for a location on a line the source does not hold or
//     at a column before the first.
//   - [ErrPathNeedsDocument] for a path with no single document to resolve
//     in.
//   - The resolution error from [go.jacobcolvin.com/niceyaml/paths] for a
//     path that selects nothing in the document.
//   - [ErrNoLocation] for a path whose token carries no position.
//   - [ErrAmbiguousPath] for a path that names the entries of several keys
//     of a decoded map.
//
// A path to a key a mapping leaves out binds at that mapping, as
// [SourceError.Nearest] describes, and has no reason. A path that enters
// an alias the document cannot follow is the one binding with both a
// position and a reason. It binds at the alias, and the reason wraps
// [go.jacobcolvin.com/niceyaml/paths.ErrAlias], so a caller tells that
// position from one at the value itself. A nil SourceError has nothing to
// resolve.
func (e *SourceError) Unresolved() error {
	if e == nil || errors.Is(e.locErr, errUnlocated) {
		return nil
	}

	if e.locErr == nil {
		return e.loc.unfollowed
	}

	return e.locErr
}

// Nearest returns the path of the node the error is bound at, and true,
// when the document does not hold the value the path of the error names.
// Binding then locates the error at the nearest node above the path that
// the document holds, and [SourceError.Path] and the message keep the path
// the error carries. Nearest reports false for an error bound at the node
// its path selects, for one with no path or no position, and for a nil
// SourceError. The nearest node is one of these:
//
//   - The mapping that lacks a key the path names, as for a required field
//     or a misspelled name. The range covers the key of that mapping, or
//     the token [AtPath] describes for a mapping with no key, and
//     [SourceError.Unresolved] returns nil.
//   - An alias on the path that the document cannot follow, such as one to
//     an anchor of a reference document from [WithReferences]. The range
//     covers the `*` of the alias, and SourceError.Unresolved returns a
//     reason that wraps [go.jacobcolvin.com/niceyaml/paths.ErrAlias].
//   - A value that decodes itself, for an error a [SelfValidator] returns
//     at or below it, since its fields need not mirror the document. The
//     range covers the text of the value. Every error below a slice, an
//     array, or a map that decodes itself binds at it.
//
// An error about a required field thus binds at the key of the mapping
// that lacks the field:
//
//	niceyaml.NewError("name is required", niceyaml.AtPath(paths.Doc().Child("server", "name")))
//	// cfg.yaml:3:1: $.server.name: name is required
//
// A key that a `<<` merge key could set from an alias the document cannot
// follow has no position, since the document cannot tell which line holds
// its value.
func (e *SourceError) Nearest() (paths.Path, bool) {
	if e == nil || e.locErr != nil || e.loc.near == nil {
		return paths.Path{}, false
	}

	return *e.loc.near, true
}

// Annotate marks the error and every binding below it on view, as
// [Annotate] marks them, and reports whether it marked any line. A caller
// that holds one binding, such as one [Bindings] yields or [errors.As]
// finds, thus marks its whole tree with one call. A binding with no
// location of its own, such as a summary or a join, adds no mark for
// itself, while the bindings below it still mark their lines. A nil
// SourceError marks nothing.
func (e *SourceError) Annotate(view *line.View) bool {
	return Annotate(e, view)
}

// Annotate marks every binding in the tree of err on view, once each, on
// the lines of its source that view holds, and reports whether it marked
// any line. A viewer that shows a document with its errors in place marks
// its view and renders it as it is:
//
//	view := source.View()
//	niceyaml.Annotate(err, view)
//	lipgloss.Println(p.Print(view))
//
// Each binding highlights its location with [kind.GenericError] and adds
// its [SourceError.Message] as an annotation below its line in
// [kind.TextError], with each tab as four spaces. An error with no message
// adds an empty annotation, which a renderer draws as a caret run under
// the highlight. A location whose highlight leaves out its own line, such
// as a position past the end of a line, gets an overlay of no width, so
// [line.View.Hunks] keeps the line. A column past the end of its line
// moves to the column after its last rune. A binding whose location did
// not resolve adds no mark.
//
// Annotate finds each line by identity, so the view may be the whole
// source, a slice of it, or a diff against another revision, and a
// binding marks only the lines of its own source. A line keeps each mark
// once, so marking a view again, or with a binding inside an error that
// marked it already, doubles no message. Annotate marks a source with
// excerpts off too, since the caller decides who sees the view. It
// reports true when it marked a line, counting one that held the marks
// already, and false for a nil err.
func Annotate(err error, view *line.View) bool {
	sources, positions := treePositions(slices.Collect(Bindings(err)), true, nil)

	marked := false

	for _, src := range sources {
		if excerpt.Annotate(view, src.lines, positions[src]) {
			marked = true
		}
	}

	return marked
}

// Excerpt returns a [line.View] of the source the error is bound to
// around the locations of its tree, and true. It marks a fresh
// [Source.View] as [Annotate] does, with a caret run alone at the location
// of the root, since the tree [FormatError] prints above the excerpt names
// the root. [line.View.Hunks] then keeps the context lines opts set on
// either side of each marked line, with distant locations in separate
// hunks, and [line.View.Clip] cuts long lines to the width opts set, as
// [ExcerptOption] describes. The lines keep their numbers in the source,
// so any [go.jacobcolvin.com/niceyaml/printer.Printer] renders the
// excerpt with the line numbers of the file.
//
// Excerpt leaves out a node bound to another source and a node whose
// location does not resolve. It reports false, with no view, when no
// location in the tree resolves in the source, when the source has
// excerpts off, as [WithExcerpts] describes, and for a nil SourceError.
func (e *SourceError) Excerpt(opts ...ExcerptOption) (*line.View, bool) {
	if e == nil {
		return nil, false
	}

	// The location of a lone binding carries no message, so the excerpt
	// gives it a caret run alone, since the tree above the excerpt names
	// it.
	_, positions := treePositions([]*SourceError{e}, false, nil)

	return e.source.excerpt(positions[e.source], newExcerptConfig(opts))
}

// excerpt returns the excerpt of s for positions, the resolved locations
// of the nodes bound to s: a fresh view of the source with the positions
// marked, cut to the hunks around them with the context lines of cfg and
// clipped to its width. Every excerpt of an error comes from here, so
// the check of [WithExcerpts] and the clip of [WithExcerptWidth] each
// stand in one place. It reports false, with no view, when positions
// mark no line of s, and for a Source whose text WithExcerpts keeps out
// of excerpts.
func (s *Source) excerpt(positions []excerpt.Mark, cfg excerptConfig) (*line.View, bool) {
	if !s.ShowsExcerpts() {
		return nil, false
	}

	view := s.View()

	if !excerpt.Annotate(view, s.lines, positions) {
		return nil, false
	}

	return view.Hunks(cfg.context).Clip(cfg.width), true
}

// marks reports whether the location of e, or of a binding below it,
// resolved, so an excerpt marks a line for the tree of e.
func (e *SourceError) marks() bool {
	found := false

	e.all(map[*SourceError]bool{}, func(n *SourceError) bool {
		found = n.locErr == nil

		return !found
	})

	return found
}

// joinParts joins the parts that are not empty with a blank line between
// each pair.
func joinParts(parts ...string) string {
	kept := make([]string, 0, len(parts))

	for _, part := range parts {
		if part != "" {
			kept = append(kept, part)
		}
	}

	return strings.Join(kept, "\n\n")
}

// treePositions returns the sources the trees of bindings touch, with the
// resolved locations of the nodes bound to each, from one walk of each
// tree. The sources come in the order the walk reaches them: the source
// of each binding, then the source of each node below it, in depth-first
// order. An excerpt per source thus comes out in that order. The
// locations of each source come in the same order, and a node whose
// location did not resolve adds none. A node the trees reach twice adds
// its location once.
//
// Every node below a binding carries its message, and the own location of
// each binding carries it when labeled is set. A nil binding touches no
// source. A node in skipped touches no source and adds no location, and
// a nil skipped holds none.
func treePositions(
	bindings []*SourceError,
	labeled bool,
	skipped map[*SourceError]bool,
) ([]*Source, map[*Source][]excerpt.Mark) {
	var sources []*Source

	touched := make(map[*Source]bool)
	positions := make(map[*Source][]excerpt.Mark)
	seen := make(map[*SourceError]bool)

	for _, root := range bindings {
		root.all(seen, func(n *SourceError) bool {
			if skipped[n] {
				return true
			}

			if !touched[n.source] {
				touched[n.source] = true
				sources = append(sources, n.source)
			}

			if n.locErr != nil {
				return true
			}

			at := excerpt.Mark{Pos: n.loc.pos, Ranges: n.ranges}
			if labeled || n != root {
				at.Message = n.text()
			}

			positions[n.source] = append(positions[n.source], at)

			return true
		})
	}

	return sources, positions
}

// checkInRange reports [ErrOutOfRange] when loc starts on a line lines
// does not hold, past its last line or before its first, or at a column
// before the first. The message counts lines and columns from 1, as the
// text does. It names the line and the lines the source holds, or the
// column and its line.
func checkInRange(loc location, lines line.Lines) error {
	textLine := oneBased(loc.pos.Line)

	if loc.pos.Line < 0 || loc.pos.Line >= lines.Len() {
		if lines.Len() == 0 {
			return fmt.Errorf("%w: line %s of an empty source", ErrOutOfRange, textLine)
		}

		return fmt.Errorf("%w: line %s not in lines 1-%d", ErrOutOfRange, textLine, lines.Len())
	}

	if loc.pos.Col < 0 {
		return fmt.Errorf("%w: column %d of line %s", ErrOutOfRange, loc.pos.Col+1, textLine)
	}

	return nil
}

// PanicError is the error of a panic that the parse or a decode
// recovered. Value holds the value the code passed to panic, and Stack
// holds the stack of the goroutine at the panic, as [debug.Stack] formats
// it. The message leaves the stack out.
//
// The go-yaml parser and decoder run under a recover, and so do the
// UnmarshalYAML and UnmarshalText methods and the [WithCustomUnmarshaler]
// functions the decoder calls. A panic in any of them comes back as a
// [SourceError] that holds a PanicError, with a message such as "decoder
// panicked: registry not initialized":
//
//	if p, ok := errors.AsType[*niceyaml.PanicError](err); ok {
//		slog.Error("decode panicked", "value", p.Value, "stack", string(p.Stack))
//	}
//
// A panic is a bug in code and says nothing about the document. The error
// matches none of [ErrDecode], [ErrSyntax], and the decode labels such as
// [ErrWrongKind], and [IsInvalid] does not report it. Nothing recovers a
// panic in a [Validator] or a [SelfValidator], so it reaches the caller as
// a panic.
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

// markedError is an error with marks of its own on top of the error it
// holds. [errors.Is] matches each of its marks, beside what that error
// matches. A message that is not empty stands in for the message of the
// error it holds.
//
// A sealed markedError unwraps to nothing, so every walk that reads an
// error along its chain ends at it, and a reporter that prints each error
// of a chain never prints what the error it holds carries, such as the
// position and excerpt go-yaml writes into its own text. [errors.Is] and
// [errors.As] still reach that error through it, apart from the targets
// it refuses, which Is reports false for.
type markedError struct {
	err error
	// The text that stands in for the message of err, or empty.
	msg string
	// The errors the error matches on top of what err matches.
	marks []error
	// The errors a sealed error matches for none of err.
	refused []error
	// The error unwraps to nothing.
	sealed bool
}

func (e markedError) Error() string {
	if e.msg != "" {
		return e.msg
	}

	return e.err.Error()
}

// Unwrap returns the error e holds, or nil when e is sealed.
func (e markedError) Unwrap() error {
	if e.sealed {
		return nil
	}

	return e.err
}

// Is reports whether target is one of the marks of e. A sealed e also
// reports what the error it holds matches, apart from the targets it
// refuses. [errors.Is] reads the error an e that is not sealed holds
// through Unwrap.
func (e markedError) Is(target error) bool {
	if slices.Contains(e.marks, target) {
		return true
	}

	if !e.sealed || slices.Contains(e.refused, target) {
		return false
	}

	return errors.Is(e.err, target)
}

// As sets target to the first error in the chain of the error a sealed e
// holds that matches it, as [errors.As] does. It reports false for an e
// that is not sealed, whose error [errors.As] reads through Unwrap.
func (e markedError) As(target any) bool {
	return e.sealed && errors.As(e.err, target)
}

// without returns e without mark among its marks. It shares no marks
// with e.
func (e markedError) without(mark error) markedError {
	e.marks = slices.DeleteFunc(slices.Clone(e.marks), func(m error) bool { return errors.Is(m, mark) })

	return e
}

// contextEnded reports whether err is, or wraps, the error of a context
// that ended: [context.Canceled] or [context.DeadlineExceeded].
func contextEnded(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// holdsLocation reports whether err, or an error anywhere in the tree it
// unwraps to, is a [*SourceError] or an [*Error] that carries a location
// or comes from [Rebase]. The details of an Error count as part of that
// tree, though [Error.Unwrap] leaves them out.
func holdsLocation(err error) bool {
	for below := range errorsBelow(err) {
		switch x := below.(type) { //nolint:errorlint // The node itself, not a chain search.
		case *SourceError:
			return true

		case *Error:
			if x.hasLocation() || x.move != nil {
				return true
			}
		}
	}

	return false
}
