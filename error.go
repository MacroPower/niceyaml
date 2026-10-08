package niceyaml

import (
	"errors"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"math"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/aliaslimit"
	"go.jacobcolvin.com/niceyaml/internal/escape"
	"go.jacobcolvin.com/niceyaml/internal/fault"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/style/kind"
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
	// to decode into. The
	// error comes back bound to the source as a [SourceError] with no
	// location. It is a mistake of the caller, so [IsInvalid] does not
	// report it.
	ErrDecodeTarget = errors.New("decode target is not a non-nil pointer")

	// ErrSelfValidateTarget indicates the value given to
	// [Node.SelfValidate], [Source.SelfValidate], [Layers.SelfValidate],
	// or [SelfValidateValue] is nil or a nil pointer, so there is nothing
	// to validate. The error comes back bound to the source as a
	// [SourceError] with no location, and SelfValidateValue binds it to
	// no document. It is a mistake of the caller, so [IsInvalid] does not
	// report it.
	ErrSelfValidateTarget = errors.New("self-validation target is nil")

	// ErrDecode indicates that the go-yaml decoder did not decode a node
	// into its target. [Node.Decode] and [Node.DecodeInto] return it. So
	// does a [Validator] that decodes the node it checks, as a
	// [go.jacobcolvin.com/niceyaml/schema.Schema] does. These errors
	// match:
	//
	//   - A value the decoder rejects, such as one of the wrong kind, a
	//     number that overflows its type, an alias with no anchor, or a
	//     field the target lacks under [WithDisallowUnknownFields].
	//   - The error a value's own UnmarshalYAML or UnmarshalText returns.
	//     That error stays in the chain, so it still matches what the
	//     method returned.
	//   - A panic in the decoder or in such a method.
	//   - The error for a target type whose definition the decoder
	//     refuses, such as a struct with two fields of one name.
	//   - The error of a reference document from [WithReferences] that
	//     does not parse.
	//
	// The error of a context that ended does not match, even when an
	// unmarshaler wraps it. Neither does [ErrDecodeTarget] or
	// [ErrExcessiveAliasing], which come back before the decoder runs, or
	// an error that a [Validator] or a [SelfValidator] reports about the
	// value it checks.
	//
	// A document that matches parsed, since text the parser rejects
	// matches [ErrSyntax] instead. The document is at fault for each
	// problem a decode reports with ErrDecode, as [IsInvalid] describes,
	// and IsInvalid shows a server that answers each case with a status of
	// its own. [Node.DecodeInto] describes where each error binds.
	//
	// [errors.Is] reports whether any problem of an error matches
	// ErrDecode, and the error can hold a problem of another kind beside
	// it. The error of a [MultiValidator] whose first validator decodes the
	// node and whose second meets an I/O error matches ErrDecode, and
	// IsInvalid does not report it. A detail from [WithDetails] is no
	// problem, so an error that names a decode error only among its
	// details does not match.
	ErrDecode = errors.New("value does not decode")

	// ErrExcessiveAliasing indicates a node whose document holds so many
	// nested aliases that a decode would read far more than the document
	// holds. A merge key reads the mapping it brings in again at every
	// merge, so a few hundred bytes of nested aliases can take the go-yaml
	// decoder minutes to decode, and the decoder never checks the context.
	// [Node.Decode] and [Node.DecodeInto] refuse to decode a node that
	// holds an alias when the aliases of its document go past the limit
	// gopkg.in/yaml.v3 applies. A [Validator]
	// that decodes the node it checks returns the same error, as a
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
	// of the parse matches it. So does a panic in the parser, such as one
	// on a token with no position that [NewSourceFromTokens] received.
	// [Source.File], [Source.Documents], and [Source.Document] return it,
	// and [Node.Err] returns it for a document that did not parse. Each
	// error comes back bound as a [SourceError] at the offending token. A
	// panic binds at the first token with a position among the ones the
	// parser was reading. The error of a file with several such documents
	// is a join, which matches through each of them. The go-yaml error
	// stays in the chain, so [errors.As] still finds it. The document is
	// at fault for each problem the parse reports with ErrSyntax, as
	// [IsInvalid] describes.
	//
	// [errors.Is] reports whether any problem of an error matches
	// ErrSyntax, and the error can hold a problem of another kind beside
	// it. A join of a syntax error and a file that did not read matches
	// ErrSyntax, and IsInvalid does not report it. A detail from
	// [WithDetails] is no problem, so an error that names a syntax error
	// only among its details does not match.
	ErrSyntax = errors.New("invalid YAML syntax")

	// ErrOutOfRange indicates the error's location lies outside the source.
	// The location starts on a line past the last or before the first,
	// which happens when a position or range came from other text, or at a
	// column before the first. [SourceError.Unresolved] reports it.
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
)

// Error is an error that points at a location in a YAML document.
//
// The location is a [paths.Path], set with [AtPath], a
// [position.Position] or a [position.Range], set with [AtPosition] or
// [AtRange], or a path and one of the other two. A path names the value
// the error is about, and a position or a range names the characters at
// fault. An Error with both binds at the position or the range, and its
// binding names the path in its message. A check that knows the value
// and the exact characters inside it, such as a rule on one character of
// a string, thus names the value and highlights the characters at once.
// The last of AtPosition and AtRange given wins. [Error.Path],
// [Error.Position], and [Error.Range] each return the part of the
// location of that type:
//
//	if path, ok := err.Path(); ok {
//		// The error is about the value at path.
//	}
//
// A path resolves within one document of a source. An `@` path resolves
// from the [Node] that binds the Error, and a `$` path from the root of
// its document, whether the Node's own methods and validators produced the
// Error or [Node.Bind] bound one built elsewhere. An Error that carries
// a position or a range as well binds there, and the path is not
// resolved, so it names the value in the message of the binding and in
// [SourceError.Path] whether or not the document holds it.
//
// An Error carries what a producer knows and nothing about presentation. A
// validator that knows a path uses [AtPath] and need not hold the source.
// Binding produces a [*SourceError] that resolves the location and renders
// the annotated excerpt.
//
// An Error is immutable once created. [Error.With] returns a copy with more
// options applied.
//
// An Error from [NewError], [Invalid], or [Place] is one problem.
// [WithDetails] adds the errors that explain it, such as its reasons or a
// related location, and a detail is never a problem of its own. Several
// problems are a join from [errors.Join], or a summary from [NewSummary],
// an Error whose message heads them. Every reader keeps to these roles:
// [SourceError.Error] prints one line per problem, [ErrorTree.Problems]
// yields one node per problem, and [Rebase] and a scoped [Node.Bind] give
// their location to each problem that carries none, and to no heading
// and no detail.
//
// [Error.Error] returns the message alone, with no location in it, so a
// wrapper such as [fmt.Errorf] may add context around an Error anywhere
// between the check and the binding. The [SourceError] that binds the
// Error puts the resolved position and the path in front of the whole
// text, the context included, so a bound error reads
// "name:line:col: $.path: msg" or "name:line:col: msg". A program that
// prints an Error no binding holds yet reads it with [FormatError], as
// FormatError(err, 0), which puts the path in front of the text the same
// way through any wrapper, as the Error wrote it, such as
// "@.path: msg". A program that returns or logs such an Error binds it
// with [BindValue], and the text of that error names each path. The
// errors a summary heads and the details of
// an Error are structure rather than text. [Error.Errors] and [Error.Details]
// return them, and the [SourceError] that binds the Error binds each one
// as a child at the location its own error carries, if any.
// [Error.Format] prints them as a tree under the %+v verb.
//
// Error implements the error interface. [errors.Is] and [errors.As]
// search the error an Error wraps and each problem a summary heads, as
// [Error.Unwrap] returns them, so a match names a problem. They pass
// over the details, which explain a problem and are none themselves.
//
// The constructor of an Error says whether the document is at fault for
// it, as [IsInvalid] describes. An Error from [NewError] or [Invalid]
// declares the document at fault. One from [Place] takes the same
// options and declares nothing, so it gives a location to an error of a
// check that could not run.
//
// Create instances with [NewError], [Invalid], [Place], or
// [NewSummary].
type Error struct {
	err error
	// The errors a summary from NewSummary heads, which leave out the nil
	// ones. Only a summary has any.
	errors []error
	// The path the errors under the Error write their `@` paths from,
	// which Rebase sets, when rebased, since a path with no selectors is
	// a base like any other.
	base paths.Path
	// Where a path below the Error binds when the document does not hold
	// it. Its paths read from the node that base reads from. The rebase of
	// the self-validation walk sets it below a value that decodes itself.
	fallback fallback
	errorConfig
	rebased bool
	// The Error comes from a Rebase that moves paths alone, as a detail
	// takes the base of the problem it explains, so it points nothing at
	// its base.
	movesOnly bool
	// The Error comes from a binding in a layer of a [Layers], so its
	// base stands in place of the root of a `$` path below it, where a
	// Rebase leaves such a path as it is.
	reroots bool
	// The document is at fault for the Error, so it matches errInvalid.
	// NewError sets it, Invalid sets it around any error but that of a
	// context that ended, and so does the rebase of the self-validation
	// walk. A binding or a tree marks each problem below the Error the
	// same way, as childBase.rebase does.
	invalid bool
	// The path of the Error names the entries of several keys of a
	// decoded map, so it resolves in no document, for the reason
	// ErrAmbiguousPath. The rebase of the self-validation walk sets it
	// under such an entry, and it holds for every error below the Error.
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

// Invalid creates a new [*Error] that wraps err and declares the
// document at fault for it, so [IsInvalid] reports the Error for any err
// but that of a context that ended. The document is at fault for each
// problem the Error heads too, such as each branch of a join it wraps,
// including a branch that is a [*SourceError] already.
// Use [NewError] instead if creating an error from a message string, and
// [Place] to give a location to an error the document is not at fault
// for.
//
// Invalid returns nil for a nil err, and for a nil [*Error] or
// [*SourceError] pointer, whatever the options. It returns an error
// rather than an [*Error], as [NewSummary] does, so the nil compares
// equal to nil wherever it goes. A check that returns nil for a valid
// value thus goes inside Invalid as it is, and a validator returns the
// result bound through the Node it read, which [Node.Invalid] does in
// one call:
//
//	return hours.Invalid(spec.Check())
//
// A caller that reads the Error itself finds it with [errors.AsType]. An
// Error that carries a location and no message has no error to wrap, so
// it comes from NewError with an empty message.
//
// The error of a context that ended is about the call, so the document
// is not at fault for it. Invalid gives an err that matches
// [context.Canceled] or [context.DeadlineExceeded] the options and
// declares nothing about it, as a decode returns such an error from a
// [SelfValidator] with no mark. A canceled check inside Invalid thus
// reads as a check that could not run. A join with such a branch matches
// too, so Invalid declares nothing about any branch of it. A detail of
// err decides nothing, so Invalid declares the fault for an err whose
// detail alone wraps the error of a context.
//
// An err that is a join stays a list of problems, and the Error is a
// heading above them, so the options locate the heading and no branch. A
// path from [AtPath] binds as a line of its own with no message, as in
// "cfg.yaml:4:10: $.license:", above branches with no location. A
// position or a range puts no line in the message, and an excerpt marks
// it with no text beside it. To point each branch at a value, move the
// join under the path of the value with [Rebase], or wrap each branch
// before joining them.
func Invalid(err error, opts ...ErrorOption) error {
	if isNothing(err) {
		return nil
	}

	e := wrapUndeclared(err, opts...)
	e.invalid = !contextEnded(err)

	return e
}

// Place creates a new [*Error] wrapping an existing error. The Error
// gives err the location and the details of the options and declares
// nothing about it, so [IsInvalid] reports the result only when it
// reports err. A check that could not run, such as one whose I/O fails,
// shows its error at the value it read this way:
//
//	return niceyaml.Place(fmt.Errorf("stat license: %w", err),
//		niceyaml.AtRange(licenseRange), niceyaml.WithDetails(why))
//
// [NewError] and [Invalid] declare the document at fault, and Place
// places an error without declaring a fault. All three take every
// [ErrorOption], and one error binds and prints the same from either
// wrapper:
//
//	niceyaml.Invalid(err, niceyaml.AtPath(p)) // invalid
//	niceyaml.Place(err, niceyaml.AtPath(p))   // not invalid
//
// Either one binds as this line:
//
//	cfg.yaml:4:10: $.license: stat license: permission denied
//
// Place declares nothing, and it clears nothing either. An err the
// document is at fault for through its own chain, such as an Error from
// NewError or a decode rejection, stays a fault inside Place. A detail
// decides nothing, so an I/O error that Place gives a located detail
// from NewError stays a check that could not run. A decode marks every
// error a [SelfValidator] returns, so a Validate method that returns an
// Error from Place still reports a fault of the document. A [Validator]
// declares each of its errors itself, so a check whose I/O can fail
// belongs there.
//
// Place returns nil for a nil err, and for a nil [*Error] or
// [*SourceError] pointer, whatever the options. It returns an error
// rather than an [*Error] for the reason Invalid gives.
//
// An err that is a join stays a list of problems, and the options locate
// the heading above them and no branch, as Invalid describes. A path
// binds as a line of its own with no message, and a position or a range
// puts no line in the message. [Rebase] moves each branch of a join
// under a path, and it declares nothing either.
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
// summary, and [FormatError] draws each as a branch. [Error.Errors]
// returns them.
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
// yields for err, and IsInvalid answers as [ErrorTree.IsInvalid] does for
// the root of that tree, so IsInvalid(err) and
// NewErrorTree(err).IsInvalid() agree. A nil err is not invalid, and
// neither is an error with no problem to yield, such as one with an empty
// message.
//
// A problem is the document's fault or a check that could not run, such
// as a schema that does not load or a context that ended. The code that
// builds an error declares which, and a location never decides it. The
// document is at fault for these problems:
//
//   - An [*Error] from [NewError] or [Invalid], with a location or
//     without, and each problem such an Error heads, such as each branch
//     of a join that Invalid wraps. Invalid declares nothing about
//     the error of a context that ended.
//   - Each problem the parse reports with [ErrSyntax] and each one a
//     decode reports with [ErrDecode], which say at which stage the
//     document failed.
//   - Every error a [SelfValidator] returns, with a location or without,
//     except the error of a context that ended.
//   - [ErrMultipleDocuments], and an error that [Node.At],
//     [Node.Ranges], or [Node.Nodes] returns for a value the document
//     lacks, which wraps [paths.ErrNotFound].
//   - [ErrExcessiveAliasing] from every reader that applies the alias
//     limit to a document or a decoded value: a decode, a
//     [go.jacobcolvin.com/niceyaml/schema.Schema], and a
//     [go.jacobcolvin.com/niceyaml/schema/matcher.Content] or
//     [go.jacobcolvin.com/niceyaml/schema/matcher.Text] matcher. The
//     ErrExcessiveAliasing of [Node.Nodes] is about the path, so the
//     document is not at fault for it.
//   - A [go.jacobcolvin.com/niceyaml/schema.Violation], and the
//     [go.jacobcolvin.com/niceyaml/schema.ErrNoMatch] that a registry
//     reports for a document that names no schema it knows.
//
// Any other error declares nothing, and the document is at fault for it
// only when it is for the error it wraps. That covers a plain error, a
// wrapper such as one from [fmt.Errorf], the error of a context, an Error
// from [Place] or [Rebase], and the Error a scoped [Node.Bind] puts
// around an error to point it at a value. A [Validator] thus returns an
// Error from NewError or Invalid to report what is wrong with the
// document, and any other error when the check itself could not run. An
// error type of the validator's own declares the fault inside Invalid,
// where [errors.As] still finds it. Place gives an error of the second
// kind a location, so it shows at a value and stays no fault of the
// document, as [Validator] describes.
//
// Each problem answers for itself, and one that is no fault of the
// document makes the whole error not invalid. The error of a
// [MultiValidator] that met a violation and an I/O error is not invalid,
// and neither is a violation beside a schema that did not load. A
// heading decides nothing. A join from [errors.Join], a summary from
// [NewSummary], and a wrapper around either answer through the problems
// they head. A detail from [WithDetails] decides nothing either, since it
// explains a problem and is none itself, so an I/O error that names a
// located detail from NewError stays a check that could not run.
//
// A wrapper that [fmt.Errorf] builds with several %w verbs is one error
// and not a list of problems. An error it wraps that carries no location
// and heads no problems is part of the text of the wrapper, as
// [NewErrorTree] describes, and is no problem of its own. A read error
// wrapped beside violations thus goes uncounted, where a join counts it:
//
//	fmt.Errorf("%w; close: %w", violations, readErr) // invalid: the read error is no problem
//	errors.Join(violations, readErr)                 // not invalid: two problems
//
// A caller that must not lose the read error joins it.
//
// A server answers a document that is not YAML, one that does not fit
// the target, and a check that could not run with a status for each. It
// asks IsInvalid first, since [errors.Is] with ErrSyntax or ErrDecode
// reports whether any problem matches:
//
//	config, err := source.Decode[Config](ctx, niceyaml.WithValidator(v))
//	switch {
//	case err == nil:
//	case !niceyaml.IsInvalid(err):
//		status = 500 // some problem is not the document's: schema load, canceled context, I/O
//	case errors.Is(err, niceyaml.ErrSyntax):
//		status = 400
//	default:
//		status = 422 // decode rejection, violation, Validate method
//	}
//
// A caller that reports problem by problem, as an editor or a CI
// annotation does, asks [ErrorTree.IsInvalid] of each node
// [ErrorTree.Problems] yields.
//
// The document is at fault for every problem of the parse and of a
// decode. A few of them thus read invalid for a cause outside the
// document:
//
//   - The error an UnmarshalYAML or UnmarshalText method returns, such
//     as an I/O error, since the decode cannot tell the I/O of the
//     method from its check of the value.
//   - A panic in the parser, in the decoder, or in such a method.
//   - A target type the decoder refuses, and the error of a go-yaml
//     option, such as one for a reference file the decoder cannot open.
//
// A caller that must tell these apart checks for their own errors before
// it calls IsInvalid.
//
// [go.jacobcolvin.com/niceyaml/schema.ErrValidate] indicates a validation
// that could not run, and [io/fs.ErrInvalid], which a schema file that
// does not load can wrap, an error of the file system. IsInvalid reports
// neither.
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

// Rebase returns an error that writes its `@` paths from base. Every `@`
// path in the tree of err resolves as base joined with that path, whether
// it sits on the [*Error] that anchors the tree, on an error a summary
// from [NewSummary] heads, or on a detail from [WithDetails]. A `$` path
// reads from the root of the document already, so Rebase leaves it as it
// is, as [paths.Path.Join] does. [Error.Path] reports the joined path,
// and the binding of the result names it. A base that starts at `@` reads
// from the Node that binds the result, and one that starts at `$` from
// the root of the document. A check written for a type writes `@` paths,
// which read from the value. A caller that runs it on a value inside a
// document rebases the result under the path of that value before it
// binds the result:
//
//	func checkHours(h *Hours) error {
//		if h.Close.Before(h.Open) {
//			return niceyaml.NewError("closes before it opens", niceyaml.AtPath(paths.Current().Child("close")))
//		}
//
//		return nil
//	}
//
//	return doc.Bind(niceyaml.Rebase(checkHours(&cfg.Hours), paths.Doc().Child("hours")))
//
// The same call puts each element of a slice under its index, but a key
// of a map takes more care. A path names the key as the source spells
// it, and the key a decode hands back can spell it another way. The key
// `0x10` of a map[int]Server decodes to 16, so this loop rebases the
// check under `$.ports.16`, which the document leaves out:
//
//	for k, server := range cfg.Ports {
//		err := niceyaml.Rebase(checkServer(&server), paths.Doc().Child("ports", strconv.Itoa(k)))
//		errs = append(errs, doc.Bind(err)) // $.ports.16.name, at the ports: key
//	}
//
// The error then binds at the `ports:` key, as [SourceError.Nearest]
// describes, rather than at the entry. A key `3.10` of a
// map[string]Server decodes to "3.1" and misses the same way. A
// [DataLocator] from [Node.DataLocator] finds an entry by the name the
// decoder gives its key. A check of a map entry therefore takes each
// location from one, in place of a base built from the key:
//
//	loc := doc.DataLocator()
//
//	for k, server := range cfg.Ports {
//		if server.Name == "" {
//			at := loc.At("ports", strconv.Itoa(k), "name")
//			errs = append(errs, niceyaml.NewError("name is required", at))
//		}
//	}
//
//	return doc.Bind(errors.Join(errs...)) // $.ports.0x10.name
//
// The names a DataLocator reads are those of a decode into any, and a
// decode into a map can name some keys another way, as [DataLocator]
// describes. A check that runs during the decode belongs in a Validate
// of the entry's type instead, as [SelfValidator] describes.
//
// Rebases compose, so a chain of them composes the chain of paths, and a
// `$` base anywhere in the chain stops the bases above it from moving the
// paths below it. A position or a range stays as it is, since the base
// moves paths alone.
// Rebase reads the roles the errors declare, problem by problem:
//
//   - A summary, a join, and any other error that unwraps to several are
//     headings, and so is a wrapper around one, such as one from
//     [fmt.Errorf] or [Invalid]. A heading points at nothing, and each
//     problem it heads takes base on its own.
//   - Every other error is a problem. A problem that carries no location
//     and wraps no binding points at base itself, whatever its details
//     carry.
//   - A detail explains the error above it, so it points at nothing
//     either. Rebase moves its paths alone.
//
// A validator that returns a summary above violations with no location
// thus points each violation at base, and one that returns an error with
// reasons points the error at base and leaves the reasons as they are. A
// join whose every branch is a nil [*Error] or [*SourceError] pointer
// heads no problem, so it is one problem of its own, and it points at
// base.
//
// Rebase moves paths and declares nothing about err, so [IsInvalid]
// reports the result only when it reports err. A check that could not
// run, such as one whose I/O fails, gives its error a location with
// [Place], which takes a position, a range, or details as well as a
// path.
//
// A decode rebases the errors of every nested [SelfValidator] itself, so
// a Validate need not rebase the Validate of a field. A Node from
// [Node.At] or [Node.Nodes] puts its own path in front of each path in an
// error it binds, so a check bound through the Node of its value needs no
// Rebase. Such a Node points each problem with no location at its own
// value by the same rules, so it binds an error as a Rebase under its
// path does, as [Node.Bind] describes. A Rebase under [Node.Path] before
// that Node binds the error puts the path in front once, since the base
// starts at `$`:
//
//	hours.Bind(niceyaml.Rebase(err, hours.Path())) // $.spec.hours.open
//
// An error joined from several, as [errors.Join] builds one, rebases branch
// by branch into a new join, so each branch carries the joined path of its
// own. Any other error that unwraps to several, and whose message is theirs
// one per line, rebases the same way. The new join matches that error for
// [errors.Is] and [errors.As], and hands both checks to any Is or As method
// the error has, so a caller that checks for a multi-error type of its own
// still finds it. An [*Error] that only wraps a join, with no location and
// no details, rebases the way the join does, and the result stays an
// [*Error].
//
// The result wraps err, or each branch of a join, so [errors.Is] and
// [errors.As] see through it, and its message is the message of err. An
// [*Error] puts no path in its message, so the text a wrapper such as
// [fmt.Errorf] added around a located error holds no path either, and
// Rebase and wrapping compose in any order. A nil err, or a nil [*Error]
// or [*SourceError] pointer, returns nil, so a validator returns the
// result as it is. An error that is or wraps a [*SourceError], with no
// [*Error] above it that carries a location, heads errors as a summary,
// or holds details, is bound already, with its location resolved, and
// comes back as it is. An Error above a binding that carries a location,
// heads errors, or holds details adds paths of its own, so Rebase puts
// the base in front of those.
//
// One binding stands in no document, which is the error [BindValue]
// returns for a value that came from none. Rebase reads it as the error
// it was made from, so each problem takes the base, and a Node then
// binds them in its document. Each binding below that one, as
// [SourceError.Errors] and [SourceError.Details] return them, rebases
// the same way on its own.
func Rebase(err error, base paths.Path) error {
	return rebase(err, base, false, false, false, fallback{})
}

// rebase is [Rebase], and with movesOnly the result moves the paths of
// err alone and points nothing at base, as a detail takes the base of the
// problem it explains. With invalid, each Error the rebase builds matches
// [errInvalid], as the self-validation walk marks what a [SelfValidator]
// returns. With ambiguous, the path of each Error the rebase builds
// resolves in no document, for the reason [ErrAmbiguousPath], as the walk
// marks an error under a map entry that shares its path. Each Error the
// rebase builds takes fb, which the walk gives an error below a value
// that decodes itself. A binding takes no base, and with invalid it comes
// back as [markInvalid] marks it. A binding that stands in no document
// rebases as the error [placeable] returns for it.
func rebase(err error, base paths.Path, movesOnly, invalid, ambiguous bool, fb fallback) error {
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
	// and the copy keeps what the Error declared.
	x, ok := err.(*Error) //nolint:errorlint // The node itself, not a chain search.
	if ok && x.addsNothing() {
		if _, joined := joinBranches(x.err); joined {
			return &Error{err: rebase(x.err, base, movesOnly, invalid, ambiguous, fb), invalid: x.invalid || invalid}
		}
	}

	if branches, ok := joinBranches(err); ok {
		rebased := make([]error, 0, len(branches))
		for _, branch := range branches {
			r := rebase(branch, base, movesOnly, invalid, ambiguous, fb)
			if r != nil {
				rebased = append(rebased, r)
			}
		}

		if len(rebased) > 0 {
			if isJoinError(err) {
				return errors.Join(rebased...)
			}

			return &rebasedJoinError{join: err, branches: rebased}
		}
	}

	return &Error{
		err: err, base: base, rebased: true, movesOnly: movesOnly, invalid: invalid, ambiguous: ambiguous, fallback: fb,
	}
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

// headsInvalid reports whether err, an error that [joinBranches] reads as
// a join, passes through an [*Error] that matches [errInvalid] on the way
// to the join, as Invalid of a join does. Each problem of the join is
// then the document's fault.
func headsInvalid(err error) bool {
	for {
		x, ok := err.(*Error) //nolint:errorlint // Walks the chain one node at a time.
		if !ok || !x.addsNothing() {
			return false
		}

		if x.invalid {
			return true
		}

		err = x.err
	}
}

// rebasedJoinError is a join of a type other than the one [errors.Join]
// builds, rebased branch by branch. Its message is the messages of the
// rebased branches one per line, the shape the message of the join had,
// and it unwraps to those branches. It matches the join for [errors.Is]
// and [errors.As] rather than unwrapping to it, since the branches of
// the join still write their paths from the old base.
type rebasedJoinError struct {
	// The join Rebase received.
	join error
	// The rebased branches, which leave out the nil ones.
	branches []error
}

// Error returns the messages of the branches one per line.
func (j *rebasedJoinError) Error() string {
	msgs := make([]string, 0, len(j.branches))
	for _, branch := range j.branches {
		msgs = append(msgs, branch.Error())
	}

	return strings.Join(msgs, "\n")
}

// Unwrap returns the rebased branches.
func (j *rebasedJoinError) Unwrap() []error {
	return j.branches
}

// Is reports whether target is the join, or whether the Is method of the
// join, if it has one, matches target. Like [errors.Is], it compares the
// join with target only when the type of target is comparable.
func (j *rebasedJoinError) Is(target error) bool {
	return matchesItself(j.join, target)
}

// As sets target to the join when target points at a type the join is
// assignable to, as [errors.As] does for an error in the chain. Otherwise
// it reports what the As method of the join, if it has one, reports.
func (j *rebasedJoinError) As(target any) bool {
	return asItself(j.join, target)
}

// matchesItself reports whether err itself matches target, as
// [errors.Is] asks of one error in a chain. It compares err with target
// when the type of target is comparable, and then calls the Is method of
// err, if it has one. It reads no error err unwraps to, so a type that
// stands for err above other errors calls it from its own Is method.
func matchesItself(err, target error) bool {
	//nolint:errorlint // The error itself, not a chain search.
	if target != nil && reflect.TypeOf(target).Comparable() && err == target {
		return true
	}

	x, ok := err.(interface{ Is(target error) bool }) //nolint:errorlint // The error itself, not a chain search.

	return ok && x.Is(target)
}

// asItself sets target to err when target points at a type err is
// assignable to, as [errors.As] does for one error in a chain. Otherwise
// it reports what the As method of err, if it has one, reports. Like
// [matchesItself], it reads no error err unwraps to.
func asItself(err error, target any) bool {
	val := reflect.ValueOf(target)
	if val.Kind() == reflect.Pointer && !val.IsNil() && reflect.TypeOf(err).AssignableTo(val.Type().Elem()) {
		val.Elem().Set(reflect.ValueOf(err))

		return true
	}

	x, ok := err.(interface{ As(target any) bool }) //nolint:errorlint // The error itself, not a chain search.

	return ok && x.As(target)
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
// error is about. It replaces a path set before it. The error points at
// the node the path selects, which for a mapping entry is its value, so
// [SourceError.Excerpt] highlights the value, and for a mapping or
// sequence it highlights the first key or element. [AtPosition] or
// [AtRange] narrows the location to the characters at fault instead, and
// the path then names the value in the message of the binding alone. A
// path from [paths.Path.Key] points at the key of the entry instead,
// which suits an error about the key itself, such as an unknown field:
//
//	niceyaml.NewError("unknown field", niceyaml.AtPath(paths.Current().Child("spec", "foo").Key()))
//
// An `@` path resolves from the scope of the [Node] that binds the Error,
// so [paths.Current] names that node itself, and a check on a value from
// [Node.At] writes its paths from the value. The binding puts the path of
// that Node in front, so its message and [SourceError.Path] carry the `$`
// path from the root of the document. A `$` path resolves from that root
// through any Node, and the binding leaves it as it is. A path from
// [Node.Path], [Node.PathAt], or SourceError.Path starts at `$`, so it
// binds through the Node a validator got whatever the scope of that Node:
//
//	for _, item := range items {
//		errs = append(errs, n.NewError("bad item", niceyaml.AtPath(item.Path())))
//	}
//
// The document may leave the value out, as it does when a check reports
// a required field. The path then selects nothing, and the error binds at
// the key of the mapping that lacks the value, as [SourceError.Nearest]
// describes. A check thus names the field whether the document holds it
// or not:
//
//	func (s Server) Validate() error {
//		if s.Name == "" {
//			return niceyaml.NewError("name is required", niceyaml.AtPath(paths.Current().Child("name")))
//		}
//
//		return nil
//	}
//
// The value may also lie behind an alias the document cannot follow, as
// one in a reference document from [WithReferences] does. The error then
// binds at that alias, as SourceError.Nearest describes.
//
// A producer may know where the value lies and still have no path that
// selects it. A validator that reads decoded data cannot always spell
// the key of a value as the source does. Such a producer gives
// [AtPosition] or [AtRange] beside the path. The error then binds there,
// and the path names the value in the message. [DataLocator.At] builds
// both from the names of decoded data.
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
// [Error]: the reasons for it, the forms a value failed to match, or a
// related location, as a conflict names the place a value was first
// declared:
//
//	niceyaml.NewError("port 80 conflicts", niceyaml.AtPath(b),
//		niceyaml.WithDetails(niceyaml.NewError("first declared here", niceyaml.AtPath(a))))
//
// The Error stays one problem, and a detail is never a problem of its
// own. [ErrorTree.Problems] yields the Error once, with its details as
// the Children of its node. [SourceError.Error] prints the line of the
// Error alone, and [FormatError], the %+v verb, and [Error.LogValue] draw
// each detail as a branch below it.
//
// A detail is any error, and one that is an [*Error] can carry a location
// of its own. [Error.Details] returns them, and the [SourceError] that
// binds the Error binds each one as a detail at the location its own
// error carries, if any. [SourceError.Excerpt] annotates the line of each
// detail whose location resolves, and a detail from [NewError] with an
// empty message marks its location with no text beside it. A detail that
// is a [*SourceError] already, or wraps one, binds as it is, so a detail
// can point into another file. [Rebase] and a scoped [Node.Bind] move the
// paths of the details and give no location to a detail that carries
// none. WithDetails skips a nil error.
//
// [errors.Is] and [errors.As] on the Error search the problem and pass
// over its details, which [Error.Unwrap] leaves out. A detail that wraps
// a sentinel or a typed error thus never matches for the problem it
// explains. A caller reads the error of a detail from [Error.Details],
// or from the Err of each child of a node [ErrorTree.Problems] yields. An
// error type that matches its reasons too says so with Is and As methods
// of its own.
//
// To report several separate problems, join them with [errors.Join] or
// head them with [NewSummary].
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
// location in it. A path, a position, or a range stays out, since the
// [SourceError] that binds the Error puts the resolved position and the
// path in front of the message. A wrapper such as [fmt.Errorf] around the
// Error thus holds the message alone, and binding still puts the path
// that [Error.Path] reports in front of the whole text, however the
// wrappers and the Errors from [Rebase] above the Error nest. The errors
// a summary from [NewSummary] heads put nothing in the message either,
// since that SourceError lists them behind their own positions, and
// neither do the details from [WithDetails], which the tree [FormatError]
// prints shows below the message. An Error from [NewError] with an empty
// message has no text, whatever location it carries, so binding is what
// names its location.
//
// [FormatError] reads an Error that no binding holds yet. It puts the
// path in front of the message, and the %+v verb and [Error.LogValue]
// print the same tree. [BindValue] binds such an Error to no document,
// for a check on a value that came from none, and the message of that
// binding holds each path.
func (e *Error) Error() string {
	if e == nil || e.err == nil {
		return ""
	}

	return e.err.Error()
}

// Format implements [fmt.Formatter].
//
// The %v and %s verbs print [Error.Error], which holds no location. The
// %+v verb prints what [FormatError] renders for the error: its message
// behind its path, as a tree with the errors a summary heads, or the
// details from [WithDetails], under it. A log or a failing test that
// prints an unbound Error that way shows its path and every error below
// it, as it does for a [*SourceError]. Every other verb formats
// [Error.Error] as it formats a string, with the width, precision, and
// flags given, so %q quotes the message and %-20v pads it.
func (e *Error) Format(f fmt.State, verb rune) {
	switch {
	case verb == 'v' && f.Flag('+'):
		writeString(f, FormatError(e, DefaultContextLines))

	default:
		_, _ = fmt.Fprintf(f, fmt.FormatString(f, verb), e.Error()) //nolint:errcheck // Formatter has no error channel.
	}
}

// LogValue implements [slog.LogValuer].
//
// The value is the tree [FormatError] prints for the error, as a string:
// its message behind its path, with the errors a summary heads, or the
// details from [WithDetails], under it behind their paths, and no source
// excerpt. A handler that logs an error by [Error.Error], as
// [slog.JSONHandler] does, shows the path and every error below it this
// way, and one that formats it with %+v, as [slog.TextHandler] does,
// keeps the excerpt out of the attribute. A wrapper such as [fmt.Errorf]
// around the Error logs as its own message, which holds no path. A
// program that wants the path of any error, or the excerpt, logs
// [FormatError] as a string.
func (e *Error) LogValue() slog.Value {
	return slog.StringValue(logTree(e))
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
	return locus{loc: e.loc, path: e.path, hasPath: e.hasPath, ambiguous: e.ambiguous}
}

// move returns l, the location of an error below e, with the base of e
// applied, where e comes from [Rebase]. The base goes in front of an `@`
// path, as [locus.rebase] puts it there. An Error that reroots puts it in
// front of a `$` path too, in place of the root, since such a path reads
// from the value [Layers] hold and the base is where a layer holds it.
// A path below an Error that is ambiguous is ambiguous too.
//
// An `@` path takes the fallback of e behind its own, as [fallback.then]
// joins them, and a `$` path, which the base leaves as it is, takes none.
// A fallback that binds every error below e stands in for the path of
// each, so a key that shares its path there makes no path ambiguous, and
// only the mark of e itself counts.
func (e *Error) move(l locus) locus {
	l.ambiguous = l.ambiguous || e.ambiguous

	if e.reroots && l.hasPath {
		rel, _ := l.path.CutPrefix(paths.Doc())
		l.path = e.base.Join(rel)

		return l
	}

	if !l.hasPath || l.path.IsAbsolute() {
		return l.rebase(e.base)
	}

	l = l.rebase(e.base)
	l.fallback = l.fallback.then(e.fallback)

	if e.fallback.only {
		l.ambiguous = e.ambiguous
	}

	return l
}

// addsNothing reports whether e adds nothing to the error it wraps: e is
// not nil, is not from [Rebase], carries no location of its own, is no
// summary, and holds no details. Such an Error has the text and the
// children of the error it wraps.
func (e *Error) addsNothing() bool {
	return e != nil && !e.rebased && !e.hasLocation() && !e.nests()
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
// detail wraps thus never matches for the problem it explains.
// [Error.Details] returns the details. A nil Error unwraps to nothing, so
// a chain that holds one is safe to walk.
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

// Cause returns the error the [Error] wraps: the error given to
// [Invalid], or one holding the message given to [NewError]. An Error
// that wraps another Error returns that Error, where [SourceError.Cause]
// looks through it. It is nil for the zero Error, and a nil Error has no
// cause.
func (e *Error) Cause() error {
	if e == nil {
		return nil
	}

	return e.err
}

// Errors returns the errors the [Error] heads as a summary from
// [NewSummary], in the order NewSummary received them and without the nil
// ones. Any other Error, and a nil one, heads none. The slice is a copy,
// so a caller may keep or sort it.
func (e *Error) Errors() []error {
	if e == nil {
		return nil
	}

	return slices.Clone(e.errors)
}

// Details returns the errors that explain the [Error], the ones
// [WithDetails] added, in the order it received them and without the nil
// ones. A nil Error has none. The slice is a copy, so a caller may keep
// or sort it.
func (e *Error) Details() []error {
	if e == nil {
		return nil
	}

	return slices.Clone(e.details)
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
	// [Layers] build, rather than in the document the error bound
	// through. It is the path of the root of that document in the
	// document of the layer.
	layerRoot *paths.Path
	pos       position.Position
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
// the document [Layers] build resolves as [locateMerged] describes
// instead. The node is the one b binds
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
// In the document [Layers] build, the path resolves in the document of
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

	loc, err := layer.resolveLocation(path)
	if layer != node {
		loc.layerRoot = &base
	}

	return loc, layer, err
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

// merges reports whether n is a Node of the document [Layers] build,
// whose errors bind in the layers. A nil n is none.
func (n *Node) merges() bool {
	return n != nil && n.source.layers != nil
}

// locateMerged resolves the position at in the document of node, which
// [Layers] built, for an error with the location l. The text of that
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

// SourceError is an error bound to the [*Source] it occurred in.
//
// [Source.File], [Source.Documents], and the [Node] methods bind every
// error they return. [Node.Bind] binds an error built elsewhere to the
// document the check ran against, and [Source.Bind] binds one to the
// document its location falls in, or to the source alone when it carries
// no location. Binding resolves the location of the error against the
// source, once, so a SourceError never changes and every method of it
// reads that result. [SourceError.Error] puts the position in front of
// the message and lists the problems a heading heads, one per line.
// [SourceError.Message] returns the message alone,
// [SourceError.Range] returns the resolved range, [SourceError.Path] the
// path the error carries, and [SourceError.Excerpt] returns the
// surrounding lines with the location highlighted. [FormatError] prints
// the message as a tree with a branch per error below it, problems and
// details alike, then the excerpt as plain text with carets under the
// locations. Its output is safe for a log, however the caller wrapped or
// joined the error:
//
//	if _, err := source.File(); err != nil {
//		log.Print(niceyaml.FormatError(err, 2))
//	}
//
// An `@` path resolves from the [Node] that bound the error, which for
// [Source.Bind] is the root of the one document of the source, and a `$`
// path from the root of the document. The Node puts its own path in front
// of each `@` path the error carries. The message and [SourceError.Path]
// thus carry `$` paths, which read from the root of the document
// whichever Node bound the error, as the position beside them does. A
// path bound through Source.Bind in a source that holds none or several
// resolves nowhere, and the reason is [ErrPathNeedsDocument].
//
// The bound error is a tree, and binding binds every node of it. The
// location of the SourceError is that of the first located [Error] along
// the cause chain of the error it binds. The chain follows each wrapper to
// the one error it wraps and ends at an error that unwraps to several,
// such as one from [errors.Join], which carries no location of its own.
// Binding binds the errors below the chain the same way, to the same
// document unless [Layers] picks another for one of them, and each
// becomes a child. They are every error a summary from
// [NewSummary] along the chain heads, every detail from [WithDetails] of
// an Error along it, and every branch of the error that ends it. A
// wrapper that [fmt.Errorf] builds with
// several %w verbs, such as one from a sentinel and a cause, keeps only
// its branches that carry a location or errors below them, since its
// message shows the text of the rest already. When one branch remains,
// the chain goes on through it as it does through a wrapper with one %w
// verb. [SourceError.Errors] returns the problems the binding heads and
// [SourceError.Details] its details, each a SourceError with its own
// children, if any, and its own location when its error carries one. A
// validator's report of several violations therefore binds to one
// SourceError per violation whether it heads them with a summary or
// joins them. An error that is or wraps a SourceError, with no Error
// above it that carries a location, heads errors, or holds details, is a
// binding already. As a child it contributes that binding, and as the
// error given to Bind it comes back as it is. A located Error above a
// binding binds anew at its own location, and its message carries the
// position the inner binding resolved as well as its own. An Error with
// details above a binding binds anew around it, with those details as
// children.
//
// [SourceError.Excerpt] marks the location of every node in the tree and
// annotates each child with its message, with distant locations in
// separate hunks.
//
// A path from [AtPath] that names a key the document leaves out, as an
// error about a required field does, binds at the key of the mapping
// that lacks the value. [SourceError.Nearest] reports that mapping, and
// the message keeps the path the error carries.
//
// A path that enters an alias the document cannot follow binds at that
// alias. Such an alias names no anchor before it, as one to an anchor of
// a reference document from [WithReferences] does. The value lies
// outside the document, so the alias is the nearest line the document
// holds for it. SourceError.Nearest reports the path of the alias, and
// [SourceError.Unresolved] still returns the reason, which tells such a
// binding from one at the value itself.
//
// An error a [SelfValidator] returns at or below a value that decodes
// itself may carry a path that resolves to nothing, since the fields of
// such a value need not mirror the document. It binds at that value, as
// SelfValidator describes, and SourceError.Nearest reports the path of
// the value.
//
// Any other location that does not resolve costs the SourceError its
// position. Such locations include an index past the end of a sequence,
// a name looked up in a scalar, and a position on a line the source does
// not have. An error that carries no location never
// had one. [SourceError.Error] then puts the name of the source alone in
// front of the message, [SourceError.Range] reports false, and
// [SourceError.Unresolved] returns the reason for the first case and nil
// for the second.
//
// A SourceError keeps the text of the error it binds. The text a wrapper
// such as [fmt.Errorf] produced stays as it was, and the position and the
// path go in front of it. An [Error] writes no path into its message, so
// that text holds none, whether the wrapper sits above or below an Error
// from [Rebase], and the line names the one path [SourceError.Path]
// reports. The message of a join gives way to its branches, each on a
// line of its own behind its position.
//
// An error marks a [line.View] with decoration, so the caller that
// renders the error decides how it looks. [SourceError.Excerpt]
// returns the hunks around the locations as a view for any renderer, and
// [SourceError.Annotate] marks any view that holds lines of the source, as
// a viewer that shows errors inline needs: the whole source, a slice of
// it, or a diff against another revision. [FormatError] renders the
// message as a tree and the excerpt as plain text, and
// [go.jacobcolvin.com/niceyaml/printer.Printer.PrintError] prints the
// same tree and excerpt with color and the context lines the printer is
// configured with:
//
//	lipgloss.Fprintln(os.Stderr, p.PrintError(err))
//
// A SourceError implements the error interface and unwraps to the error it
// binds, so [errors.Is] and [errors.As] see through it.
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
// none.
type boundChild struct {
	bound   *SourceError
	detail  bool
	located bool
}

// DefaultContextLines is the number of context lines the %+v verb of an
// [*Error] or a [*SourceError] shows on either side of each marked line.
const DefaultContextLines = 2

// ErrorListLimit is the number of errors [SourceError.Error] lists below
// its first line. A message that holds more ends with a line that counts
// the rest.
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
	// The number of lines the whole list holds.
	count int
	// The number of children at the end of the list of children whose
	// text own holds already, so the list leaves out each of them that
	// the binding did not locate.
	shown int
	// Whether msg lists the errors below the binding.
	lists bool
	// Whether the binding has neither a message nor a position, so it
	// adds no line to the list of the binding above it.
	bare bool
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

		return scopedError{err: &Error{err: err, base: scope, rebased: true}, anchor: a}, true
	}

	unchanged := scopedError{err: err, anchor: a}

	// An Error that adds nothing to the join it wraps reads as the join.
	x, ok := err.(*Error) //nolint:errorlint // The node itself, not a chain search.
	if ok && x.addsNothing() {
		if _, joined := joinBranches(x.err); joined {
			inner, changed := underScope(x.err, scope)
			if !changed {
				return unchanged, false
			}

			// The copy keeps what the Error declared.
			return anchored(&Error{err: inner.err, invalid: x.invalid}), true
		}
	}

	branches, ok := joinBranches(err)
	if !ok {
		return unchanged, false
	}

	scoped := make([]error, 0, len(branches))
	changed := false

	for _, branch := range branches {
		if branch == nil {
			continue
		}

		s, c := underScope(branch, scope)
		scoped = append(scoped, s.err)
		changed = changed || c
	}

	switch {
	case !changed:
		return unchanged, false

	case isJoinError(err):
		return anchored(errors.Join(scoped...)), true

	default:
		return anchored(&rebasedJoinError{join: err, branches: scoped}), true
	}
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
func (b binder) nodeAt(idx int) *Node {
	if b.node != nil || !b.route {
		return b.node
	}

	docs := b.src.documents()

	i := sort.Search(len(docs), func(i int) bool {
		return docs[i].span.End > idx
	})
	if i < len(docs) && docs[i].span.Contains(idx) {
		return docs[i]
	}

	return nil
}

// BindValue binds err to no document. It serves a check on a value that
// came from none, such as the body of a request or a value the program
// built. [Error.Error] writes no path, so an error that nothing bound
// reads as its message alone, and a summary from [NewSummary] reads as
// its heading. A binding puts each path in the text, so the error
// BindValue returns names every failing location through %v, inside a
// wrapper from [fmt.Errorf], in a join from [errors.Join], and in a log:
//
//	2 violations
//	$.port: 0 is less than 1
//	$.name: name is required
//
// Each path starts at `$`, which here is the value the check ran on, and
// an `@` path reads from that value too. No file and no position stand
// in front of a line. [FormatError] prints the same errors as a tree.
//
// A validator of decoded data returns its errors through BindValue. An
// adapter for a policy engine or for another schema language is one:
//
//	func (e *Engine) ValidateValue(data any) error {
//		var errs []error
//
//		for _, finding := range e.check(data) {
//			at := niceyaml.AtPath(paths.Current().Child(finding.Names...))
//			errs = append(errs, niceyaml.Invalid(finding, at))
//		}
//
//		return niceyaml.BindValue(niceyaml.NewSummary(fmt.Sprintf("%d violations", len(errs)), errs...))
//	}
//
// [go.jacobcolvin.com/niceyaml/schema.Schema.ValidateValue] returns its
// violations this way. A caller that runs the Validate method of a type
// outside a decode binds the result the same way before it prints it:
//
//	if err := niceyaml.BindValue(request.Validate()); err != nil {
//		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
//	}
//
// That call runs the one method. [SelfValidateValue] runs the Validate
// of every value below request too, as a decode does, and binds the
// result as BindValue binds it.
//
// The result stands in no document, so a document can still place it.
// [Rebase] and every Bind return an error bound to a document as it is,
// and they read this result as err, the error it was made from. A caller
// that knows where the value stands in a document puts the errors under
// that path, and a [Node] of the document then binds them:
//
//	err := engine.ValidateValue(cfg.Request)
//
//	return doc.Bind(niceyaml.Rebase(err, paths.Doc().Child("request")))
//
// The bound error reads "app.yaml:2:9: $.request.port: 0 is less than 1".
// A decode places the result the same way, at the value it checked, when
// a Validate method of a type or a [Validator] returns it. Each binding
// below the result places on its own too, so a caller that drops some of
// the problems places the rest. Those bindings are the ones
// [SourceError.Errors] and [SourceError.Details] return for the result.
//
// A wrapper such as [fmt.Errorf] around the result keeps its text
// through both calls, behind the position and the path, as in
// "app.yaml:2:9: $.request.port: check: 0 is less than 1". A wrapper
// whose text does not hold the text of the result once, such as one that
// quotes it or writes a message of its own, places the errors too. It
// keeps the text it wrote, with any path that text names from the value.
//
// An [*Error] around the result binds as it does around err. With no
// option, the result places as it does alone. Details stay with the
// Error. A location on the Error is the location of the bound error, as
// it is above any error that carries a path. One problem thus reports at
// that location, and several keep their own lines below it.
//
// Placing builds a new error and leaves the result as it is. A result
// that no document places stays an error that names each path from the
// value. A result placed twice, in one document or in two, gives two
// errors, and each stands where its own call bound it. Rebase returns an
// error that nothing bound, so a validator that checks one field with
// another validator binds that result again under the path of the field:
//
//	return niceyaml.BindValue(niceyaml.Rebase(err, paths.Current().Child("request")))
//
// The result is a [*SourceError] bound to no [Node], and so is each
// binding below it. [SourceError.Node] and [SourceError.Document] return
// nil, and [SourceError.DocumentIndex], [SourceError.Position], and
// [SourceError.Range] report false. [SourceError.Source] returns a
// source with no name and no lines. For a binding with a path,
// [SourceError.Unresolved] returns a reason that wraps
// [go.jacobcolvin.com/niceyaml/paths.ErrNoDocument], which FormatError
// leaves out. A position or a range has no line to resolve on, so its
// reason is [ErrOutOfRange].
//
// BindValue returns nil for a nil err, and for a nil [*Error] or
// [*SourceError] pointer. An error that is bound already, as [Node.Bind]
// describes one, comes back as it is, whether a document bound it or
// BindValue did.
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
// Each wrapper therefore comes back as a [placedError], whose message is
// the message of the wrapper with the text of the binding cut down to
// that of its error. A wrapper whose message does not hold the text of
// the binding once, such as one that quotes it or writes a message of
// its own, keeps that message with every path it names. Its error
// places all the same, as an error that no source bound yet places
// under such a wrapper.
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
		if !isWrapErrors(err) || !holdsUnplaced(err) {
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
			return isWrapErrors(err) && slices.ContainsFunc(x.Unwrap(), holdsUnplaced)

		default:
			return false
		}
	}
}

// replaceBelow is [replaceUnplaced] for wrapper, an error whose cause
// chain goes on through below. The branches are every error wrapper
// unwraps to when it unwraps to several, and nil otherwise. The ones
// beside below add nothing to the tree, as a sentinel does, so the
// [placedError] keeps them to match as wrapper does. The message of
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

	return &placedError{
		wrapper: wrapper,
		err:     placed,
		beside:  beside,
		msg:     msg,
	}, true
}

// placedError stands for a wrapper around a binding that stood in no
// document, once [placeable] replaced that binding with its error. Its
// message is the message of the wrapper with the text of the binding cut
// down to the message of the error, or the message as the wrapper wrote
// it when that text does not stand there once. It unwraps to the error.
// It matches the wrapper for [errors.Is] and [errors.As] rather than
// unwrapping to it, since the wrapper still unwraps to the binding. A
// wrapper with several %w verbs unwraps to other errors beside the
// binding, such as a sentinel, and the placedError matches those too.
type placedError struct {
	// The wrapper Bind or Rebase received.
	wrapper error
	// The error below the wrapper, with the binding replaced.
	err error
	msg string
	// The errors the wrapper unwraps to beside the one that held the
	// binding, when it unwraps to several.
	beside []error
}

// Error returns the message of the wrapper, with the text of the binding
// cut down to the message of its error where the wrapper holds that
// text once.
func (p *placedError) Error() string {
	return p.msg
}

// Unwrap returns the error below the wrapper, with the binding replaced.
func (p *placedError) Unwrap() error {
	return p.err
}

// Is reports whether target is the wrapper, or whether the Is method of
// the wrapper, if it has one, matches target. Like [errors.Is], it
// compares the wrapper with target only when the type of target is
// comparable. It also reports whether an error the wrapper unwraps to
// beside the binding matches target, as [errors.Is] reports it.
func (p *placedError) Is(target error) bool {
	if matchesItself(p.wrapper, target) {
		return true
	}

	return slices.ContainsFunc(p.beside, func(err error) bool { return errors.Is(err, target) })
}

// As sets target to the wrapper when target points at a type the wrapper
// is assignable to, as [errors.As] does for an error in the chain.
// Otherwise it reports what the As method of the wrapper, if it has one,
// reports, and then what [errors.As] reports for each error the wrapper
// unwraps to beside the binding.
func (p *placedError) As(target any) bool {
	if asItself(p.wrapper, target) {
		return true
	}

	return slices.ContainsFunc(p.beside, func(err error) bool { return errors.As(err, target) })
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
			case isJoinError(cur):
				cur = firstBranch(branches)

			case isWrapErrors(cur):
				if isJoinMessage(cur.Error(), branches) {
					cur = firstBranch(branches)

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
				lead := firstBranch(branches)
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

// firstBranch returns the first of branches that is not a nil interface,
// or nil when every branch is one.
func firstBranch(branches []error) error {
	for _, branch := range branches {
		if branch != nil {
			return branch
		}
	}

	return nil
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

	e.leadOnce.Do(func() { e.lead = e.findPassedLead() })

	return e.lead
}

// findPassedLead finds the binding [SourceError.passedLead] returns.
func (e *SourceError) findPassedLead() *SourceError {
	if e.locErr == nil && !e.adopted {
		return nil
	}

	if _, ok := e.ownPath(); ok {
		return nil
	}

	lead := leadingBinding(e.err)
	if lead == nil {
		return nil
	}

	if e.source.Name() == "" || lead.Source() == e.source || !holdsSource(e, e.source) {
		return lead
	}

	return nil
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
			if x.rebased {
				l = x.move(l)
			}

			return anchor{err: x, locus: l}, false
		}

		a, heading := findAnchor(x.err)
		if a.err != nil {
			if x.rebased {
				a.locus = x.move(a.locus)
			}

			return a, false
		}

		heading = heading || len(x.errors) > 0

		if x.rebased && !x.movesOnly && !heading {
			at := locus{path: x.base, hasPath: true, ambiguous: x.ambiguous, fallback: x.fallback}

			return anchor{err: x, locus: at}, false
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
// document [Layers] build, where a location resolves in a layer of
// another source. A binder that is unplaced binds to no node, since its
// error stands in no document.
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
			e.err = &Error{err: e.err, base: *root, rebased: true, movesOnly: true, reroots: true}
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
		// No error binds in the text [Layers] merged, which is no file of
		// the caller. One that found no layer binds in the lowest.
		if home := e.source.layers.home(); home != nil {
			e.source, e.node = home.source, home
		}

		if e.locErr == nil {
			e.locErr = checkInRange(e.loc, e.source.lines)
		}

		if e.locErr == nil {
			e.ranges = highlightRanges(e.source.lines, e.loc)
			e.rng = rangeOf(e.ranges, e.loc.pos)
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
	located := !ok && !bound.adopted && !errors.Is(bound.locErr, errUnlocated)

	e.below = append(e.below, boundChild{bound: bound, detail: detail, located: located})
}

// heads reports whether e heads separate problems: whether a child of e
// is no detail.
func (e *SourceError) heads() bool {
	return slices.ContainsFunc(e.below, func(c boundChild) bool { return !c.detail })
}

// filtered returns the bound children of e that are details when detail
// is set, and the problems e heads otherwise, in their order.
func filtered(below []boundChild, detail bool) []*SourceError {
	var out []*SourceError

	for _, c := range below {
		if c.detail == detail {
			out = append(out, c.bound)
		}
	}

	return out
}

// Source returns the [*Source] the error is bound to. A nil SourceError is
// bound to none.
func (e *SourceError) Source() *Source {
	if e == nil {
		return nil
	}

	return e.source
}

// Node returns the [*Node] the error is bound to: the one whose methods
// and validators produced it or whose [Node.Bind] bound it, from whose
// scope an `@` path in the error resolves, or, for an error bound through
// [Source.Bind], the root of the document its location falls in. The
// error wrote such a path from that scope, and [SourceError.Path] reports
// the `$` path from the root of the document. A rejection of the go-yaml
// decoder is bound to the Node that decoded, and its path starts at `$`
// already. A position or a range falls in the document whose [Node.Span]
// holds its line, whether or not that document parsed, and a path falls
// in the one document of the source. The error stays bound to that root
// when its location does not resolve there, as with a path that selects
// nothing or a column before the first. Source.Bind binds an error to
// none when it carries no location. It also binds to none a position or a
// range on a line no document holds, and a path in a source that holds
// several documents or has a YAML syntax error.
//
// A YAML syntax error is bound to the root of the document it belongs
// to, the one whose span holds its location, so the error [Node.Err]
// returns names that document. Two documents that parse together share
// one error, as [Source.AllDocuments] describes, and it is bound to the
// one of the two that holds its location. A syntax error with no location
// in the source is bound to the first document it fails.
//
// Node.Bind keeps its node on every error it binds anew, except an error
// that wraps a binding. Such an error keeps the node of the binding it
// wraps, whether Node.Bind returns it as it is or binds it anew around
// the binding with the details it holds, so its node can be nil or belong
// to another document or source. An error that [Layers] bind is bound
// to the Node of the layer its location resolved in, or to the Node of
// the lowest layer when it carries no location.
//
// The error [BindValue] returns is about a value that came from no
// document. It is bound to none, and so is each binding below it, with a
// path or without.
// A nil SourceError is bound to none.
func (e *SourceError) Node() *Node {
	if e == nil {
		return nil
	}

	return e.node
}

// Document returns the root [*Node] of the document the error is bound
// to, the one the node [SourceError.Node] returns belongs to. The root is
// the one [Source.AllDocuments] returns at the index of the document. A
// YAML syntax error is bound to the document it belongs to, so the syntax
// error of the second document of three returns the root of that
// document, as a violation there does.
//
// Document returns nil for an error bound to no node, which
// SourceError.Node lists: an error [Source.Bind] bound with no location,
// with a position or a range on a line no document holds, or with a path
// in a source that holds several documents or has a syntax error. A nil
// SourceError is bound to none, and [ErrorTree.Bound] is nil for an error
// bound to no source. [Node.DocumentIndex] panics on a nil Node, so a
// caller that sorts the errors of a file by document reads
// [SourceError.DocumentIndex], which reports false wherever Document
// returns nil.
func (e *SourceError) Document() *Node {
	if e == nil {
		return nil
	}

	return e.node.Document()
}

// DocumentIndex returns the 0-indexed position within the file of the
// document the error is bound to and true, or 0 and false for an error
// bound to no document. It is the [Node.DocumentIndex] of the root
// [SourceError.Document] returns, and it reports false wherever Document
// returns nil. A report that groups the problems of a file by document
// thus reads the index from the binding and never holds a nil Node:
//
//	byDocument := make(map[int][]string)
//	for problem := range niceyaml.NewErrorTree(err).Problems() {
//		if index, ok := problem.Bound.DocumentIndex(); ok {
//			byDocument[index] = append(byDocument[index], problem.Message())
//		}
//	}
//
// A YAML syntax error reports the index of the document it belongs to, so
// the syntax error of the second document of three reports 1. An error
// [Source.Bind] bound with no location reports false, and so does a nil
// SourceError, which a node for an error bound to no source holds. The
// message of a bound error counts documents from 1, as it counts lines,
// so "document 3" in [SourceError.Error] is the document at index 2.
func (e *SourceError) DocumentIndex() (int, bool) {
	doc := e.Document()
	if doc == nil {
		return 0, false
	}

	return doc.DocumentIndex(), true
}

// Message returns the text of the bound error with no position, document,
// or path in front: the message [NewError] or [Invalid] gave an [*Error],
// or the text of any other error as it is. It is the text
// [SourceError.Excerpt] annotates a location with, and the field a
// structured report such as a JSON line or a CI annotation carries beside
// the position from [SourceError.Position] and the path from
// [SourceError.Path]. Such a report walks [ErrorTree.Problems], which
// yields one node per problem. That walk passes over the summary a
// validator puts above several violations and the details below each
// problem, and it yields each error bound to no source, which has no
// binding to read. [ErrorTree.Message] and [ErrorTree.Path] answer for
// every node, with the message and the path of the binding when the node
// has one. The methods of a nil SourceError return zero values, so the
// report reads the rest from the binding with no branch for a node that has
// none:
//
//	for problem := range niceyaml.NewErrorTree(err).Problems() {
//		path, _ := problem.Path()
//		pos, _ := problem.Bound.Position()
//		emit(problem.Bound.Source().FilePath(), pos, problem.Message(), path)
//	}
//
// A binding whose location did not resolve has no position but still
// names a problem, so the report carries the zero position for it. The
// position is the one [SourceError.Error] prints, counted from 0. A
// report that marks the text reads [SourceError.Range] instead. For a
// position an error gave with [AtPosition] inside a token, that range
// starts where the content of the token starts, which can differ from
// the position.
//
// Text a wrapper such as [fmt.Errorf] added around the Error stays, as it
// does everywhere else, and holds no path, since [Error.Error] writes none.
// The errors [SourceError.Error] lists below the message are not part of
// it, so the message of a summary is the summary, and a wrapper around a
// binding that lists errors keeps the line of that binding without them. A
// binding of a join has no line of its own, and its message is the text of
// the join. A nil SourceError has an empty message.
func (e *SourceError) Message() string {
	return e.text()
}

// Path returns the [paths.Path] the bound error is about and true, or the
// zero Path and false when it carries none. The path starts at `$`, so it
// reads from the root of the document, whichever [Node] bound the error.
// It is the path [Error.Path] reports for the [*Error] that gave the
// binding its location, with the base of every [Rebase] on the way joined
// in front. The Node that bound the error joins its own [Node.Path] in
// front of an `@` path, so an error written as `@.price` and bound
// through the Node at `$.items[1]` reports `$.items[1].price`. The result
// goes to the [Node.Ranges] or the [Node.At] of any Node of the document.
// [paths.Path.CutPrefix] with the path of [SourceError.Node] gives back
// the `@` path the error wrote.
// A rejection of the go-yaml decoder is the exception. It reports the
// path where the document writes the value, as [Node.DecodeInto]
// describes, which lies outside the scope of the Node for a value an
// alias or a `<<` merge key brings in from there.
//
// An error that carries a range or a position beside its path binds at
// that location and reports the path the same way, resolved or not. A
// binding that wraps another reports the path of the one it wraps. A nil
// SourceError has none.
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

// Cause returns the error the binding reports, without the Errors that
// locate it. For a bound [*Error] it is the error given to [Invalid],
// or one holding the message given to [NewError]. Cause looks through
// each Error that wraps another, such as the one [Rebase] puts around a
// located Error, and through a binding the bound error wraps. It returns
// the same error however many of them stand above it. A validator that
// wraps an error of its own type hands that value to the caller of each
// binding this way:
//
//	for bound := range niceyaml.AllBindings(err) {
//		if rule, ok := errors.AsType[*RuleError](bound.Cause()); ok {
//			report(bound, rule.ID)
//		}
//	}
//
// [errors.As] on the binding searches the problems it heads as well. On
// a binding that reports several violations it finds the cause of the
// first violation, where Cause returns the error of the binding alone. A
// wrapper such as [fmt.Errorf] ends the walk and is the cause itself, so
// the search above still finds a RuleError the wrapper holds. Cause is
// nil for a binding of the zero [Error], and a nil SourceError has no
// cause.
func (e *SourceError) Cause() error {
	for e != nil {
		cause := e.err
		if x, ok := cause.(*Error); ok { //nolint:errorlint // The node itself, not a chain search.
			cause = x.textCause()
		}

		inner, ok := cause.(*SourceError) //nolint:errorlint // The node itself, not a chain search.
		if !ok {
			return cause
		}

		e = inner
	}

	return nil
}

// Errors returns the bound problems the [SourceError] heads. Each error a
// summary from [NewSummary] heads along the cause chain of the bound
// error becomes one, and so does each branch of the error that ends the
// chain by unwrapping to several, such as one from [errors.Join]. A
// validator's report of several violations therefore unwraps to one
// binding per violation, in the order the validator gave them. A wrapper
// that [fmt.Errorf] builds with several %w verbs keeps only its branches
// that carry a location or errors below them, and when one remains, the
// chain goes on through it. Each binding carries its own problems or
// details, if any, and a location when its error carries one, so a
// caller checks [SourceError.Range] before it uses the position:
//
//	for _, violation := range bound.Errors() {
//		if rng, ok := violation.Range(); ok {
//			...
//		}
//	}
//
// A binding of one problem heads none, and [SourceError.Details] returns
// the errors that explain it. The slice is a copy, so a caller may keep
// or sort it. A nil SourceError heads nothing.
func (e *SourceError) Errors() []*SourceError {
	if e == nil {
		return nil
	}

	return filtered(e.below, false)
}

// Details returns the bound details of the [SourceError]: the errors
// [WithDetails] added to an [*Error] along the cause chain of the bound
// error, each bound at the location its own error carries, if any. They
// explain the error and are no problems of their own. The slice is a
// copy, so a caller may keep or sort it. A nil SourceError has none.
func (e *SourceError) Details() []*SourceError {
	if e == nil {
		return nil
	}

	return filtered(e.below, true)
}

// Error returns the message of the bound error with its resolved position
// in front: "name:line:col: $.path: msg" when the error carries a path,
// whether it binds at the path or at a position or range beside it, and
// "name:line:col: msg" for a position or range alone. The path is the one
// [SourceError.Path] reports, and msg is the whole text of the bound
// error, so any context a wrapper added sits behind the path, wherever
// the wrapper sat in the chain. The name is
// [Source.Name], and the position stands alone as "line:col:" when the
// source has none, so an error from a named file reads as a compiler
// diagnostic that editors and build tools link to the line. An error
// without a location, or one whose location does not resolve, has no
// position to add, and the name then stands alone in front as "name: msg",
// so an error from one file of many still says which file. No line says
// which document of the file such an error is about. In a source that
// holds more than one document, the document of the [Node] it is bound to
// therefore follows the name, counted from 1, as "name: document 3: msg".
// The count takes in every document [Source.AllDocuments] returns, an
// empty one included, so the number is the place of the document in the
// file.
// A source with no name leads with the document, as "document 3: msg". An
// error bound to no Node, such as one [Source.Bind] binds with no
// location, names no document. The message comes back as it is when the
// source has no name and no document to add. It also comes back as it is
// when the first line of the message comes from a binding of the same
// source, which puts the name or position there already. A first line
// from a binding of another source names that source alone. The name
// then still goes in
// front when a child of the error is bound to this source. It stays out
// when every child is bound to another, as under a wrapper around a join
// of bindings from other files. A binding inside the message that leaves
// its own message untouched puts nothing in front of the first line, so
// when a binding below it wrote that line, the line counts as coming from
// that one. A SourceError that binds anew around a binding writes no
// position of its own, so it puts the name in front by the same rule.
//
// The message holds one line per problem. A problem is one line, located
// or not, and its details from [WithDetails] stay out of the message. The
// line is the text of the bound error, which runs on when that text does,
// as a message written with continuation lines does. A heading, which is
// a summary from [NewSummary], a join, or a wrapper around either, lists
// the problems it heads under its own line, so a validator's report names
// every violation wherever the error goes:
//
//	cafe.yaml: 2 schema violations
//	cafe.yaml:6:8: $.spec.sla: string does not match pattern
//	cafe.yaml:22:11: $.spec.hours.days: expected "array", got "string"
//
// Each line is the message of a problem from [SourceError.Errors], behind
// the name of its own source. The lines come in the order [NewErrorTree]
// shows the children, which is the order of their positions, and they
// match the rows [ErrorTree.Problems] yields, but for the branches a
// multi-error holds in its message, as the next paragraph describes. A
// heading among them lists
// the problems it heads the same way, right after its own line. A
// problem with neither a message nor a position adds no line. An error
// joined from several, as [errors.Join] builds one, has no line of its
// own, so its message is the list alone, and the text a wrapper put in
// front of the join stays on a line above the list. The message lists
// [ErrorListLimit] errors at most, and a last line then counts the rest,
// as "cafe.yaml: and 490 more" does. [FormatError], the %+v verb,
// [SourceError.LogValue], and
// [go.jacobcolvin.com/niceyaml/printer.Printer.PrintError] draw every
// error as the branches of a tree, details included.
//
// Any other error that unwraps to several keeps the message it wrote, and
// its branches follow as lines. That message holds the text of a branch
// at most and never the location a binding gives it, so each branch the
// binding locates from an [*Error] it carries follows whatever the
// message holds. A branch with no location, or one that was a binding
// already and names its own position, follows unless the message holds
// it already. Error reads that from the first branch. A message that
// holds the message of its first branch holds them all, as "a; b" does
// and a wrapper [fmt.Errorf] builds with several %w verbs does. A count
// such as "2 violations" holds none. A wrapper that rewrites the message
// of an error with a list keeps the text it wrote, and no list follows
// it.
//
// A wrapper such as [fmt.Errorf] formats the error it wraps with Error,
// so the list reaches a log through any wrapper, and [SourceError.Message]
// returns the line of the error alone. The result never includes source
// lines, so it stays short in a log. The name of the source, each path,
// and the text of a decode rejection reach it with their control
// characters drawn or escaped, so a key or a file name cannot start a
// line of its own or write to a terminal. A message the caller wrote
// reaches it as it is, with any line feed it holds. [SourceError.Excerpt]
// and [FormatError] return the annotated source excerpt. Error reads the
// text
// of the bound error the first time it is called and returns the same
// message from then on. A nil SourceError, as [errors.As] can yield from a
// chain that holds one, has an empty message, as a nil [*Error] does.
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
	// The message of a join is the messages of the bindings below it,
	// and the list stands in for it, so nothing reads the text until a
	// step needs it. Reading it for a join bound at each step of a loop
	// would build the list of every join below.
	full := sync.OnceValue(e.err.Error)

	var (
		t   boundTexts
		cut = true
	)

	// A Node that binds a join through a scope binds a new join of the
	// scoped branches, so the bound error says whether it is a join, where
	// the end of the chain is the join the caller gave.
	if _, joined := joinBranchesOf(e.err, full); !joined {
		t.own, t.shown, cut = e.end.cut(full())
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
		if t.own == "" {
			t.own = full()
		}

		t.own = pathed(t.own)
		t.head = e.prefixed(t.own)
		t.msg = e.prefixed(pathed(full()))
		t.bare = t.own == "" && (e.locErr != nil || e.adopted)

		return t
	}

	t.lists = true
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
		case t.bare:
			continue

		case !t.lists:
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
// That order is by position within the source each is bound to, as
// [trees] sorts them, with the branches of a problem that binds a join in
// place of that problem.
func (e *SourceError) ordered(shown int) []*SourceError {
	held := len(e.below) - shown

	var listed []*SourceError

	for i, c := range e.below {
		if !c.detail && (i < held || c.located) {
			listed = append(listed, c.bound)
		}
	}

	kids := appendPlaced(nil, listed)

	groupSources(kids)
	slices.SortStableFunc(kids, comparePositioned)

	out := make([]*SourceError, 0, len(kids))
	for _, kid := range kids {
		out = append(out, kid.bound)
	}

	return out
}

// appendPlaced appends each of children to kids as [placed] returns it,
// and returns the extended slice. A child that binds a join has no line
// of its own, so its branches go in beside the other children, as
// [appendBoundChildren] puts them.
func appendPlaced(kids []positioned, children []*SourceError) []positioned {
	for _, child := range children {
		joined := joinBinding(child)
		if joined != nil {
			kids = appendPlaced(kids, filtered(joined.below, false))

			continue
		}

		kids = append(kids, placed(child))
	}

	return kids
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
	if own, _, _ := end.cut(msg); own != "" {
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
// one before it. It does not look below a binding, so it shows each
// binding as one unit. A caller that renders each binding on its own,
// such as one section per document of a file, walks it:
//
//	for bound := range niceyaml.Bindings(err) {
//		fmt.Println(niceyaml.FormatError(bound, 2))
//	}
//
// [FormatError] renders the whole error instead, as one tree and one
// excerpt per source, as [Excerpts] yields them. A caller that marks a
// view calls [Annotate], which marks every binding in err and needs no
// walk. A caller that lists the problems of an error walks
// [ErrorTree.Problems]. A nil err has no bindings.
func Bindings(err error) iter.Seq[*SourceError] {
	return func(yield func(*SourceError) bool) {
		eachBinding(err, yield)
	}
}

// eachBinding calls visit for each [*SourceError] reached through the
// wrappers and joins around err, in depth-first order, and stops when
// visit reports false. Below an [*Error] it reaches the error the Error
// wraps, then each error a summary heads, then each detail, which
// [Error.Unwrap] leaves out. It does not look below a binding, whose
// children visit reaches through the binding itself. Reports whether
// every visit wanted more.
func eachBinding(err error, visit func(*SourceError) bool) bool {
	switch x := err.(type) { //nolint:errorlint // Walks the tree one node at a time.
	case *SourceError:
		if x == nil {
			return true
		}

		return visit(x)

	case *Error:
		if x == nil {
			return true
		}

		if !eachBinding(x.err, visit) {
			return false
		}

		for _, inner := range slices.Concat(x.errors, x.details) {
			if !eachBinding(inner, visit) {
				return false
			}
		}

	case interface{ Unwrap() error }:
		return eachBinding(x.Unwrap(), visit)

	case interface{ Unwrap() []error }:
		for _, inner := range x.Unwrap() {
			if !eachBinding(inner, visit) {
				return false
			}
		}
	}

	return true
}

// AllBindings returns an iterator over every binding in the tree of
// err: each [*SourceError] [Bindings] yields, in the same order, and
// every binding below each one. A parent comes before the bindings under
// it, and the children of a child come right after it, whatever source
// each binds to. A binding the tree reaches twice, such as one bound
// before a summary headed it and joined beside that summary, comes once.
// A caller that reads something from every binding walks it, such as one
// that collects the sources an error touches:
//
//	sources := make(map[*niceyaml.Source]bool)
//	for bound := range niceyaml.AllBindings(err) {
//		sources[bound.Source()] = true
//	}
//
// [Annotate] marks a view with the bindings AllBindings yields, so a
// caller that marks a view calls it and walks nothing. A report that
// lists the problems of an error walks [ErrorTree.Problems] instead, as
// the example on [SourceError.Message] shows. AllBindings yields a
// summary beside the violations under it and each detail beside the
// error it explains, and it passes over every error bound to no source.
// A nil err has no bindings.
func AllBindings(err error) iter.Seq[*SourceError] {
	return func(yield func(*SourceError) bool) {
		seen := make(map[*SourceError]bool)

		eachBinding(err, func(x *SourceError) bool {
			return x.all(seen, yield)
		})
	}
}

// Format implements [fmt.Formatter].
//
// The %v and %s verbs print [SourceError.Error]. The %+v verb prints
// what [FormatError] renders for the error with [DefaultContextLines]
// lines of context, for a log that prints its errors that way. A wrapper
// such as [fmt.Errorf] around a SourceError formats as its own message
// under every verb. That message holds the list [SourceError.Error]
// returns and no excerpt, so a program that holds any error calls
// FormatError for the excerpt. Every other verb formats
// [SourceError.Error] as it formats a string, with the width, precision,
// and flags given, so %q quotes the message and %-20v pads it.
//
// The excerpt shows lines of the source around the location, so a
// logger that formats an error with %+v writes those lines into the log.
// Zap does so for every error that implements [fmt.Formatter]. Its
// zap.Error field holds [SourceError.Error] under the key "error" and
// the %+v output under "errorVerbose". The handlers of [log/slog] read
// [SourceError.LogValue] instead, which holds no excerpt. A program
// whose source holds secrets creates it with [WithExcerpts] set to
// false, and no verb then prints a line of that source.
func (e *SourceError) Format(f fmt.State, verb rune) {
	switch {
	case verb == 'v' && f.Flag('+'):
		writeString(f, FormatError(e, DefaultContextLines))

	default:
		_, _ = fmt.Fprintf(f, fmt.FormatString(f, verb), e.Error()) //nolint:errcheck // Formatter has no error channel.
	}
}

// LogValue implements [slog.LogValuer].
//
// The value is the tree [FormatError] prints for the error, as a string:
// its message behind the position its location resolved to, with each
// error below it, problem or detail, behind its own position, and no
// source excerpt. A handler that logs an error by [SourceError.Error], as
// [slog.JSONHandler] does, shows the whole tree this way, where Error
// lists [ErrorListLimit] problems at most and no details. A handler that
// formats the error with %+v, as
// [slog.TextHandler] does, keeps the excerpt out of the attribute. A
// wrapper such as [fmt.Errorf] around a SourceError logs as its own
// message, which holds the list Error returns. A program that wants the
// whole tree of any error, or the excerpt, logs [FormatError] as a
// string.
func (e *SourceError) LogValue() slog.Value {
	return slog.StringValue(logTree(e))
}

// FormatError renders err as plain text for a log or a terminal without
// color: its message as a tree, then the excerpt of every error bound to
// a source in it. The tree is [NewErrorTree], with a connector in front
// of each error below another. A validator's report thus reads as its
// summary with one branch per violation, each behind the position its
// location resolved to, and the details of an error branch off below it:
//
//	cafe.yaml: 2 schema violations
//	|-- 6:8: $.spec.sla: string does not match pattern
//	`-- 22:11: $.spec.hours.days: expected "array", got "string"
//
// The excerpts follow, one per source the bindings in err touch, as
// [Excerpts] yields them. An error joined from one binding per document
// of a file thus shows that file once, with the errors of every document
// on it. A binding whose children point into another file, such as a
// detail that names where a value was first declared, shows an excerpt of
// that file too. Each excerpt keeps context lines of
// unchanged content on either side of each marked line and renders as
// [line.View.String] renders a view. Each line sits behind its number,
// with carets under the columns of every location on the row below and
// the message of each error below the binding beside its caret. Among
// several bindings, the message of each binding sits beside its own caret
// too. When the bindings touch more than one source, the name of its
// source leads each excerpt on a row of its own. A negative context shows
// the marked lines alone. Blank lines separate the parts.
//
// A line starting "no excerpt:" follows the excerpts for each binding
// [Bindings] finds whose tree resolves no location, with the reason the
// location of the binding itself did not resolve. A binding that carries
// no location of its own gets no such line. Neither does a binding that
// carries a path into a document with no content, such as an empty file,
// since no path resolves there and the tree names the path already. A
// binding whose own location does not resolve but whose children do gets
// their excerpts and no reason, and its message stays in the tree without
// a position.
//
// A source with excerpts off, as [WithExcerpts] sets it for a text that
// holds secrets, adds no excerpt and no line in its place. The tree
// names the position, the path, and the message of each error bound in
// that source, and the excerpt of every other source follows the tree.
//
// The output holds no
// escape sequences, so it reads in a log as it does in a terminal.
// Each message of the tree, each message beside a caret, and each
// "no excerpt:" line draws control characters as their pictures. The
// exceptions are a tab, which becomes four spaces wherever it falls, and
// a line feed in a message of the tree, which starts a new row.
//
// FormatError looks through the wrappers and joins around a
// [SourceError], so it renders the excerpt however the error was
// wrapped, and it is what a program logs when it holds any error:
//
//	err := fmt.Errorf("load %s: %w", name, doc.Bind(check(cfg)))
//	log.Print(niceyaml.FormatError(err, 2))
//
// An error that binds to no source renders as its tree alone. For an error
// with nothing nested, that tree is its message behind the path the
// [*Error] along its cause chain carries, where a binding would put it,
// as in "@.open: closes before it opens" for a check of a value. An
// error whose tree and excerpts both render nothing, such as a bound join
// of typed-nil errors, renders its message in their place, with control
// characters as their pictures like any other.
// [go.jacobcolvin.com/niceyaml/printer.Printer.PrintError] renders the same
// tree and excerpts with styles. A nil err renders as "".
func FormatError(err error, context int) string {
	if err == nil {
		return ""
	}

	parts := []string{renderErrorTree(NewErrorTree(err))}
	parts = append(parts, errorDetails(err, context)...)

	out := joinParts(parts...)

	// A bound join whose branches all carry nothing renders as an empty
	// tree, so the message stands in for it rather than nothing, laid out
	// as the tree of one node that a plain error with that message gets.
	if out == "" {
		return renderErrorTree(ErrorTree{Text: err.Error()})
	}

	return out
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
// render as their pictures, so a key of the document that holds an escape
// sequence cannot reach the terminal, and the output holds no escape
// sequences as [FormatError] promises. A tab becomes four spaces instead,
// since it lays out the text after it.
func renderErrorTree(t ErrorTree) string {
	var sb strings.Builder

	if t.Text != "" {
		sb.WriteString(escape.Message(t.Text))
	}

	writeErrorBranches(&sb, t.Children, "", t.Text != "")

	return sb.String()
}

// logTree renders the tree of err as [renderErrorTree] lays it out. When
// that tree renders nothing, such as for a bound join of typed-nil
// errors, it renders the tree of one node that holds the message of err,
// as [FormatError] does when it has no excerpt either.
func logTree(err error) string {
	if out := renderErrorTree(NewErrorTree(err)); out != "" {
		return out
	}

	return renderErrorTree(ErrorTree{Text: err.Error()})
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

		for j, row := range strings.Split(escape.Tabs(child.Text), "\n") {
			if broken {
				sb.WriteByte('\n')
			}

			broken = true

			if j == 0 {
				sb.WriteString(indent + connector + escape.Control(row))
			} else {
				sb.WriteString(indent + below + escape.Control(row))
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

// Range returns the range in the source that the error points at: the
// range it carries, or the content of the token at the position it carries
// or its path resolves to. A token that spans several lines yields a range
// across them. For a range or a path, the range starts at the position
// [SourceError.Error] reports. Error reports a position from [AtPosition]
// as the error gave it. When that position falls inside a token, on the
// spaces before it, or on a later line of a multi-line token, the range
// starts where the content of the token starts instead. That start lies
// at an earlier column or line, or at a later column past the spaces.
// For a path that names a key the document leaves out, the range covers
// the key of the mapping that lacks it, as [SourceError.Nearest]
// describes. For a path that enters an alias the document cannot follow,
// it covers the `*` of that alias.
// The range is in the coordinates of the view [Source.Lines] returns, where
// line 0 is line 1 of the text.
//
// Binding resolved the location, so Range reads the result, and reports
// false when there is none: the error carries no location and gained
// none, as one from [fmt.Errorf] bound through the root of a document
// does, or its location did not resolve, for the reason
// [SourceError.Unresolved] returns. An error whose Range reports false
// has no position in [SourceError.Error] and marks no location of its
// own, so an excerpt [SourceError.Excerpt] builds for it shows the
// locations of the nodes below it. A nil SourceError carries no
// location.
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
// resolve, and nil when it did or when the error carries no location at
// all, which is the ordinary case for an error from [fmt.Errorf] bound
// through the root of a document and nothing to explain. The reason is
// [ErrOutOfRange] for a location on a
// line the source does not hold or at a column before the first,
// [ErrPathNeedsDocument] for a path bound through [Source.Bind] in a
// source with no single document, the resolution error from
// [go.jacobcolvin.com/niceyaml/paths] for a path that selects nothing
// in the document, or [ErrNoLocation] for a path whose token carries no
// position. A path that names a key a mapping leaves out resolves to the
// key of that mapping, as [SourceError.Nearest] describes, so it has no
// reason.
// For a path that names the entries of several keys of a decoded map, it
// is [ErrAmbiguousPath]. A renderer names the reason in place of the
// excerpt:
//
//	if excerpt, ok := bound.Excerpt(2); ok {
//		fmt.Println(excerpt)
//	} else if reason := bound.Unresolved(); reason != nil {
//		fmt.Println("no excerpt:", reason)
//	}
//
// [FormatError] prints that line for every reason but one that wraps
// [go.jacobcolvin.com/niceyaml/paths.ErrNoDocument], since no path
// resolves in a document with no content.
//
// One binding has a reason and a position both. A path that enters an
// alias the document cannot follow binds at that alias, as
// [SourceError.Nearest] describes, and Unresolved returns the resolution
// error of the path, which wraps
// [go.jacobcolvin.com/niceyaml/paths.ErrAlias]. The excerpt marks the
// alias, so the renderer above prints it. A caller that must know
// whether the position is the value or an alias in front of it checks
// the reason:
//
//	if errors.Is(bound.Unresolved(), paths.ErrAlias) {
//		// The value lies behind the alias, such as in a reference document.
//	}
//
// A nil SourceError has nothing to resolve.
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
// when the document does not hold the value its path names. Binding then
// locates the error at the nearest node above the path that the document
// holds. That node is the mapping that lacks a key the path names, or an
// alias on the path that the document cannot follow. For an error of a
// [SelfValidator], it may also be a value that decodes itself.
//
// An error about a missing value, such as a required field, carries the
// path the value would have, and that path selects nothing. Binding
// locates such an error at the key of the mapping that lacks the value:
//
//	niceyaml.NewError("name is required", niceyaml.AtPath(paths.Doc().Child("server", "name")))
//	// cfg.yaml:3:1: $.server.name: name is required
//
// [SourceError.Range] then covers the key `server`, [SourceError.Path]
// and the message keep the path as the error carries it, and
// [SourceError.Unresolved] returns nil. A mapping that is an element of a
// sequence, or the root of the document, has no key, so the range covers
// its first key instead, as it does for an error at the mapping itself.
//
// [go.jacobcolvin.com/niceyaml/paths.Resolver.Nearest] finds the mapping
// and says which paths have one. A path that misspells a name binds at
// the nearest mapping the same way, so a caller that must tell an
// approximate location from an exact one checks Nearest. The error
// [Node.At] and [Node.Ranges] return for such a path binds there too,
// with "not found" as its message.
//
// An alias the document cannot follow names no anchor before it, as one
// to an anchor of a reference document from [WithReferences] does, or
// lies inside the anchor it names. A decode reads the value behind an
// alias to a reference document, and the document holds no line for
// that value. An error whose path goes through the alias therefore binds
// at the alias:
//
//	// Line 2 of app.yaml holds `server: *server`, and a reference
//	// document holds the anchor.
//	niceyaml.NewError("port must be at least 1", niceyaml.AtPath(paths.Doc().Child("server", "port")))
//	// app.yaml:2:9: $.server.port: port must be at least 1
//
// SourceError.Range then covers the `*` of the alias, Nearest returns
// `$.server`, and SourceError.Unresolved returns the error the path
// failed to resolve with, which wraps
// [go.jacobcolvin.com/niceyaml/paths.ErrAlias]. That reason tells an
// alias from a mapping that lacks a key. The document cannot tell
// whether the value behind the alias holds the key, so an error about a
// key that value leaves out binds the same way.
//
// A path enters no alias to read a key of a mapping, even when a `<<`
// merge key of that mapping names an alias the document cannot follow.
// An error at such a key therefore has no position. The merge may set
// the key or leave it as the mapping spells it, so the document cannot
// tell which line holds the value.
//
// A value that decodes itself, as [SelfValidator] describes one, fills
// its fields as its method chooses, so the document may hold nothing at
// the path of an error at or below it. A decode binds such an error at
// that value:
//
//	// Line 2 of app.yaml holds `addr: db:0`, which an UnmarshalText
//	// method of the Addr type parses into a host and a port.
//	// app.yaml:2:7: $.addr.port: port must be at least 1
//
// SourceError.Range then covers the text of the value, Nearest returns
// `$.addr`, and SourceError.Unresolved returns nil. Every error below a
// slice, an array, or a map that decodes itself binds at it the same
// way, whether or not the document holds a node at its path.
//
// Nearest reports false for an error bound at the node its path selects,
// for one with no path, and for one that has no position. A nil
// SourceError reports false.
func (e *SourceError) Nearest() (paths.Path, bool) {
	if e == nil || e.locErr != nil || e.loc.near == nil {
		return paths.Path{}, false
	}

	return *e.loc.near, true
}

// rangeOf returns the one range that spans the highlight ranges from
// [highlightRanges], from the start of the first to the end of the last.
// With no ranges, it returns the empty range at the position at.
func rangeOf(ranges position.Ranges, at position.Position) position.Range {
	if len(ranges) == 0 {
		return position.NewRange(at, at)
	}

	return position.NewRange(ranges[0].Start, ranges[len(ranges)-1].End)
}

// Annotate marks the error and every binding below it on view, and
// reports whether it marked any line. It makes the marks [Annotate] makes
// for the error, so a caller that holds one binding, such as one
// [Bindings] yields or [errors.As] finds, marks the whole tree of that
// binding with one call:
//
//	var bound *niceyaml.SourceError
//	if errors.As(err, &bound) {
//		bound.Annotate(view)
//	}
//
// A binding with no location of its own, such as the summary a validator
// puts above its violations or the join of several errors, adds no mark
// for itself. The bindings below it still mark their lines, and Annotate
// reports true when any of them does. A loop over [AllBindings] that
// skips some bindings therefore still marks each one below a binding it
// keeps. A caller that marks some problems and leaves others out walks
// [ErrorTree.Problems] and marks the binding of each node it keeps, which
// marks that problem and its details. A nil SourceError marks nothing.
func (e *SourceError) Annotate(view *line.View) bool {
	return Annotate(e, view)
}

// Annotate marks err on view. It marks every binding in the tree of err,
// each [*SourceError] [AllBindings] yields, once, on the lines of its
// source that view holds, and reports whether it marked any line. A
// viewer that shows a document with its errors in place marks its view
// with one call and renders it as it is:
//
//	view := source.View()
//	niceyaml.Annotate(err, view)
//	lipgloss.Println(p.Print(view))
//
// Annotate highlights the location of each binding with
// [kind.GenericError] and adds its message from [SourceError.Message] as
// an annotation below its line in [kind.TextError], so the message reads
// as error text without the highlight of the token it describes. The
// annotation holds each tab of the message as four spaces, as the tree
// [FormatError] prints spells it. A binding whose location did not
// resolve adds no mark, for the reason [SourceError.Unresolved] gives.
//
// A line several errors mark carries an annotation for each, which
// [line.View.String] and the printer draw on one row joined by "; " in
// column order, whatever order the bindings come in. A line keeps each
// mark once, so a second call with the same error leaves the view as the
// first did, and a message that repeats at one column of a line reads
// once. A caller thus marks one view with several errors, or with an
// error and then a binding inside it, and no message doubles.
//
// An error with no message marks its line with an annotation below it
// with no content. A renderer that draws marks from annotations, as the
// printer does, draws that annotation as a caret run under the highlight,
// so the range shows its extent without color. The annotation starts at
// the first column the highlight covers on its line, so a position on the
// spaces around a token puts the message under the token. A location whose
// highlight leaves out its own line gets an overlay of no width at its
// column on that line. Such locations include a position past the end of a
// line and a path to an empty value. They also include a position on a
// line of only spaces inside a block scalar, and a range that starts at
// the end of its first line. The overlay renders nothing and still counts
// as decoration, so [line.View.Hunks] keeps the line. Annotate moves a
// column past the end of its line to the column after its last rune, for
// the overlay and the message alike, so a renderer spends at most one cell
// past the line on the mark. [SourceError.Error] still reports the column
// as given.
//
// Annotate finds each line by identity rather than by index, since every
// view over a source shares its [*line.Line] values, so the view may be
// the whole source from [Source.View], a slice of it from [line.View.Slice]
// such as one document of a file, or a diff that interleaves the source
// with another revision. A binding marks only the lines of its own source
// that the view holds, and Annotate skips a line the view does not hold.
// An error that holds bindings of several sources, such as the join of
// the errors of two files, thus marks the view of one file with the
// errors of that file alone.
// A unified diff from [go.jacobcolvin.com/niceyaml/diff.Result.Unified]
// takes its unchanged lines from the after revision, so it holds every
// line of that revision but only the deleted lines of the before
// revision. An error bound to the before revision therefore marks
// nothing on a line the diff left unchanged.
// [go.jacobcolvin.com/niceyaml/diff.Result.Before] holds every line of
// the before revision.
//
// Annotate reports whether it marked any line, including one that carried
// the marks already. It reports false when no location in err resolved,
// when the view holds none of the lines the locations fall on, and when
// err holds no binding, as a nil err holds none.
//
// [SourceError.Annotate] makes the same marks for one binding.
// [Excerpts] marks a fresh view of each source instead and cuts it to the
// hunks around the marks, for the excerpts under the tree [FormatError]
// prints.
//
// Annotate marks the lines of a source with excerpts off as it marks
// those of any other. The caller supplied the view and decides who sees
// it, and [WithExcerpts] covers the excerpts this package builds.
func Annotate(err error, view *line.View) bool {
	sources, positions := treePositions(slices.Collect(Bindings(err)), true)

	marked := false

	for _, src := range sources {
		if annotate(view, src, positions[src]) {
			marked = true
		}
	}

	return marked
}

// Excerpt returns a [line.View] of the source the error is bound to
// around the locations of its tree, with each one highlighted. Excerpt
// marks a fresh [Source.View] with the location of every node in the
// tree and the message of each node below the root as an annotation
// below its own line, with each tab as four spaces, as [Annotate] adds
// it. A line several nodes mark carries one annotation per message, and a
// message that repeats at one column of it reads once. The root's own
// location gets a caret run alone, since the tree [FormatError] prints
// above the excerpt names the root.
// [line.View.Hunks] then keeps context lines of unchanged content on
// either side of each marked line. Excerpt leaves out a node bound to
// another source, and [SourceError.Excerpts] shows it in its own source.
// Distant locations become separate hunks, and the first line of each hunk
// after the first carries a "..." annotation above it. The lines keep
// the numbers they have in the source, so any
// [go.jacobcolvin.com/niceyaml/printer.Printer] renders the excerpt
// with the file's line numbers, as it renders the hunks of a diff. A
// negative context shows the marked lines alone, as 0 does. A caller
// that marks several errors on one view, or adds search matches to it,
// takes the hunks of that view the same way.
//
// Excerpt reports false, with no view, when no location in the tree
// resolves in the source the error is bound to, even when a node bound
// to another source resolves in its own. [SourceError.Unresolved] names
// why the location of the error itself did not resolve, and returns nil
// for an error that carries no location of its own, such as a join or a
// summary, whose problems from [SourceError.Errors] each name their own
// reason. Excerpt leaves out a node whose location does not resolve. Its
// message is still part of the tree [FormatError] prints. A nil
// SourceError carries no location.
//
// Excerpt also reports false for an error bound to a source with
// excerpts off, as [WithExcerpts] describes, whatever resolves there.
func (e *SourceError) Excerpt(context int) (*line.View, bool) {
	if e == nil {
		return nil, false
	}

	_, positions := excerptPositions([]*SourceError{e})

	return e.source.excerpt(positions[e.source], context)
}

// Excerpts returns an iterator over one excerpt per source the tree of
// the error touches, each the [line.View] [SourceError.Excerpt] builds
// for that source: the source the error is bound to first, then the
// source of each node below it in the order the tree reaches them, with
// every node bound to that source marked on it. A source none of whose
// locations resolve, or that the tree names through unresolved nodes
// alone, yields nothing, so an error whose tree stays in one source
// yields one excerpt or none. An error bound to a file of values that
// names the lines of a template beside it renders as one excerpt of
// each file:
//
//	for src, excerpt := range bound.Excerpts(2) {
//		fmt.Println(src.Name())
//		fmt.Println(excerpt)
//	}
//
// [Excerpts] yields the same excerpts for the binding alone, and one
// excerpt per source for an error that holds several bindings. A nil
// SourceError yields nothing.
//
// A source with excerpts off, as [WithExcerpts] describes, yields
// nothing either, and the other sources of the tree yield theirs.
func (e *SourceError) Excerpts(context int) iter.Seq2[*Source, *line.View] {
	return func(yield func(*Source, *line.View) bool) {
		if e == nil {
			return
		}

		sources, positions := excerptPositions([]*SourceError{e})

		yieldExcerpts(sources, positions, context, yield)
	}
}

// Excerpts returns an iterator over one excerpt per source the bindings
// in err touch: every [*SourceError] [Bindings] finds, and every binding
// below each one. The sources come in the order the bindings reach them.
// Each excerpt is a fresh [Source.View] with the location of every one of
// those bindings marked on it, cut to the hunks around them, as
// [SourceError.Excerpt] cuts one. An error joined from one binding per
// document of a file thus yields one excerpt of that file with every
// document's errors on it:
//
//	for _, doc := range docs {
//		errs = append(errs, doc.Validate(ctx, schema))
//	}
//
//	for src, excerpt := range niceyaml.Excerpts(errors.Join(errs...), 2) {
//		fmt.Println(src.Name())
//		fmt.Println(excerpt)
//	}
//
// The message of each binding sits beside its caret, so a reader tells
// the carets of one excerpt apart. An error that holds one binding leaves
// the location of that binding with a caret run alone, since the tree
// [FormatError] prints above the excerpt names it, and yields what
// [SourceError.Excerpts] yields for that binding. Excerpts marks a
// binding the error reaches twice once.
//
// A source with excerpts off, as [WithExcerpts] describes, yields
// nothing, whatever resolves in it.
// A source none of whose locations resolve yields nothing. [FormatError]
// and [go.jacobcolvin.com/niceyaml/printer.Printer.PrintError] render
// these excerpts under the tree of the error. A caller that wants one
// excerpt per binding, such as one section per document, takes
// [SourceError.Excerpts] of each binding [Bindings] yields. An err with
// no bindings, and a nil err, yield nothing.
func Excerpts(err error, context int) iter.Seq2[*Source, *line.View] {
	return func(yield func(*Source, *line.View) bool) {
		sources, positions := excerptPositions(slices.Collect(Bindings(err)))

		yieldExcerpts(sources, positions, context, yield)
	}
}

// yieldExcerpts yields the excerpt of each of sources that has one, in
// order, as [Source.excerpt] builds it from the positions of that source.
func yieldExcerpts(
	sources []*Source,
	positions map[*Source][]errorPosition,
	context int,
	yield func(*Source, *line.View) bool,
) {
	for _, src := range sources {
		excerpt, ok := src.excerpt(positions[src], context)
		if !ok {
			continue
		}

		if !yield(src, excerpt) {
			return
		}
	}
}

// excerpt returns the excerpt of s for positions, the resolved locations
// of the nodes bound to s: a fresh view of the source with the positions
// marked, cut to the hunks around them with context lines. Every excerpt
// of an error comes from here, so the check of [WithExcerpts] stands in
// one place. It reports false, with no view, when positions mark no line
// of s, and for a Source whose text WithExcerpts keeps out of excerpts.
func (s *Source) excerpt(positions []errorPosition, context int) (*line.View, bool) {
	if !s.Excerpts() {
		return nil, false
	}

	view := s.View()

	if !annotate(view, s, positions) {
		return nil, false
	}

	return view.Hunks(context), true
}

// errorDetails returns what the tree of err leaves out: each excerpt
// [Excerpts] yields with context lines, which [line.View.String] renders
// as plain text, then a line starting "no excerpt:" for each binding
// [Bindings] finds whose tree marks nothing, with the reason
// [SourceError.Unresolved] returns. A binding that carries no location
// has nothing to explain, and neither has one whose reason wraps
// [paths.ErrNoDocument], since no path resolves in a document with no
// content. When the bindings touch more than one source, the name of its
// source leads each excerpt on a row of its own, so the reader tells the
// excerpts apart. Returns nothing when there is nothing to show. The
// printer renders the same parts with its styles.
func errorDetails(err error, context int) []string {
	bindings := slices.Collect(Bindings(err))
	sources, positions := excerptPositions(bindings)

	var parts []string

	yieldExcerpts(sources, positions, context, func(src *Source, excerpt *line.View) bool {
		part := excerpt.String()

		// The name is the caller's text, so its control characters render
		// as pictures like those of the tree.
		if len(sources) > 1 && src.Name() != "" {
			part = src.label() + "\n" + part
		}

		parts = append(parts, part)

		return true
	})

	for _, bound := range bindings {
		if bound.marks() {
			continue
		}

		// A document with no content has no line to show for any path, so
		// that reason explains nothing the tree leaves out.
		reason := bound.Unresolved()
		if reason == nil || errors.Is(reason, paths.ErrNoDocument) {
			continue
		}

		// The reason names the path, which a key of the document spells,
		// so its control characters render as pictures like those of the
		// tree. A tab in the key becomes four spaces, as it does in the
		// tree.
		parts = append(parts, "no excerpt: "+escape.Control(escape.Tabs(reason.Error())))
	}

	return parts
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

// errorPosition holds a resolved error position with the text to
// annotate its line with, which is empty for the root of an excerpt.
type errorPosition struct {
	message string
	ranges  position.Ranges
	pos     position.Position
}

// annotate marks positions, the resolved locations of the nodes bound to
// src, on view with [annotateSource]. [markUnannotated] then adds an
// annotation without content below each line it marked that has none.
// It reports whether it marked any line, which it does not when
// positions is empty or the view holds none of their lines.
func annotate(view *line.View, src *Source, positions []errorPosition) bool {
	marked := annotateSource(view, src, positions)

	markUnannotated(view, marked)

	return len(marked) > 0
}

// annotateSource marks positions, the resolved locations of the nodes bound
// to src, on view and returns the indices of the lines it marked, with
// repeats. The view may hold the lines of src at any indices, as a diff
// does, so every mark goes to the index that holds its line. A position
// whose highlight leaves out its own line gets an overlay of no width at
// its column on that line. Such positions include one with no token under
// it and a range that covers no column of its lines. They also include a
// position on a line of only spaces inside a block scalar, and a range that
// starts at the end of its first line. The overlay renders nothing and
// still marks the line as decorated, so the line joins the hunks
// [line.View.Hunks] keeps.
//
// Each position with a message adds one annotation below its line, so a
// line several positions mark carries one annotation per message, and a
// renderer joins them in column order. Each tab in a message becomes four
// spaces, as [escape.Tabs] returns it, so the annotation spells the message
// as the tree of [FormatError] does. A renderer draws the other control
// characters of an annotation as their pictures. The annotation starts at
// the first column the highlight of its position covers on that line, where
// [markUnannotated] starts a caret run, so a position on the spaces around
// a token puts its message under the token. A position with no highlight on
// its line keeps its column. A column past the end of its line moves to the
// column after its last rune, in the overlay and in the annotation below
// the line. A far column then costs a renderer no more cells than a column
// at the end.
//
// A line keeps each mark once. [addOverlay] and [addAnnotation] skip a mark
// the line holds already, so positions that repeat, in one call or across
// several, leave the view as the first of them does. A message that repeats
// at one column thus reads once. Several errors say the same of one value
// when a schema states a constraint twice, or when a value fails two
// branches of an anyOf the same way.
func annotateSource(view *line.View, src *Source, positions []errorPosition) []int {
	if len(positions) == 0 {
		return nil
	}

	index := src.lineIndex(view)

	var marked []int

	for _, pos := range positions {
		var segments []position.Range

		for _, r := range pos.ranges {
			segments = append(segments, src.lines.SliceLines(r)...)
		}

		if i, ok := index(pos.pos.Line); ok {
			marked = append(marked, i)

			width := src.lines.Line(pos.pos.Line).Width()
			start, onLine := highlightStart(pos.pos, segments)

			if !onLine {
				col := min(pos.pos.Col, width)
				addOverlay(view, i, line.Overlay{
					Cols: position.NewSpan(col, col),
					Kind: kind.GenericError,
				})
			}

			if pos.message != "" {
				addAnnotation(view, i, line.Annotation{
					Content:   escape.Tabs(pos.message),
					Kind:      kind.TextError,
					Placement: line.Below,
					Col:       min(start, width),
				})
			}
		}

		// [line.Lines.SliceLines] already clamped each segment to its
		// line, which the view holds at index i.
		for _, lr := range segments {
			if i, ok := index(lr.Start.Line); ok {
				addOverlay(view, i, line.Overlay{
					Cols: position.NewSpan(lr.Start.Col, lr.End.Col),
					Kind: kind.GenericError,
				})

				marked = append(marked, i)
			}
		}
	}

	return marked
}

// addOverlay adds o to line i of view, unless the line holds an equal
// overlay already.
func addOverlay(view *line.View, i int, o line.Overlay) {
	if !slices.Contains(view.Overlays(i), o) {
		view.AddLineOverlay(i, o)
	}
}

// addAnnotation adds a to line i of view, unless the line holds an equal
// annotation already.
func addAnnotation(view *line.View, i int, a line.Annotation) {
	if !slices.Contains(view.Annotations(i), a) {
		view.Annotate(i, a)
	}
}

// highlightStart returns the first column that segments cover on the
// line of at, or the column of at when no segment lies on that line. It
// also reports whether any segment lies on that line.
func highlightStart(at position.Position, segments []position.Range) (int, bool) {
	col, found := at.Col, false

	for _, lr := range segments {
		if lr.Start.Line == at.Line && (!found || lr.Start.Col < col) {
			col, found = lr.Start.Col, true
		}
	}

	return col, found
}

// excerptPositions returns what [treePositions] returns for the excerpts of
// bindings. The own location of a binding carries its message only among
// several bindings, where the message tells the carets of one excerpt
// apart. The location of a lone binding carries none, so an excerpt gives
// it a caret run alone, since the tree above the excerpt names it.
func excerptPositions(bindings []*SourceError) ([]*Source, map[*Source][]errorPosition) {
	return treePositions(bindings, len(bindings) > 1)
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
// source.
func treePositions(bindings []*SourceError, labeled bool) ([]*Source, map[*Source][]errorPosition) {
	var sources []*Source

	touched := make(map[*Source]bool)
	positions := make(map[*Source][]errorPosition)
	seen := make(map[*SourceError]bool)

	for _, root := range bindings {
		root.all(seen, func(n *SourceError) bool {
			if !touched[n.source] {
				touched[n.source] = true
				sources = append(sources, n.source)
			}

			if n.locErr != nil {
				return true
			}

			at := errorPosition{pos: n.loc.pos, ranges: n.ranges}
			if labeled || n != root {
				at.message = n.text()
			}

			positions[n.source] = append(positions[n.source], at)

			return true
		})
	}

	return sources, positions
}

// markUnannotated adds an annotation without content, in [kind.TextError]
// and at the first column its overlays without Blend set cover, so a
// search highlight on the line moves no caret, below every line of view at
// an index in marked that carries no annotation below it yet. A line
// several locations mark, or that an earlier error marked, thus gets one.
// [line.View.String] draws the marks of a line from its overlays. A
// renderer that draws them from its annotations, as the printer does,
// draws such an annotation as a caret run under the overlays. The range
// of a location with no message beside it, such as the root of a bound
// error, then shows its extent without color.
func markUnannotated(view *line.View, marked []int) {
	for _, i := range marked {
		// A line repeats in marked once per location on it, so the check
		// for an annotation below it comes before the scan of its overlays.
		// Each repeat after the first then skips the scan.
		if slices.ContainsFunc(view.Annotations(i), func(a line.Annotation) bool {
			return a.Placement == line.Below
		}) {
			continue
		}

		col, found := 0, false

		for _, o := range view.Overlays(i) {
			if !o.Blend && (!found || o.Cols.Start < col) {
				col, found = o.Cols.Start, true
			}
		}

		if found {
			view.Annotate(i, line.Annotation{Kind: kind.TextError, Placement: line.Below, Col: col})
		}
	}
}

// lineIndex returns a lookup from a line index of the source to the index
// of view that holds that line, found by identity. A line index the
// source does not hold, or a line the view does not hold, reports false.
func (s *Source) lineIndex(view *line.View) func(int) (int, bool) {
	lines := s.lines

	return func(srcIdx int) (int, bool) {
		if srcIdx < 0 || srcIdx >= lines.Len() {
			return 0, false
		}

		return view.Index(lines.Line(srcIdx))
	}
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

// highlightRanges returns the ranges to highlight for loc: the range itself
// when the error carried one, the content of the token a path resolved
// to, and otherwise the content of the token at its position. The parser
// makes a token for an empty value that no line holds, so a path to one
// has no ranges, where the token at its position would be a comment or
// the spaces after the colon.
func highlightRanges(view line.Lines, loc location) position.Ranges {
	if loc.rng != nil {
		return position.Ranges{clampRange(view, *loc.rng)}
	}

	if loc.tk != nil {
		return view.ContentRanges(loc.tk)
	}

	return view.ContentRanges(view.TokenAt(loc.pos))
}

// clampRange returns r cut to lines. A range that runs past the last line
// ends at the end of that line, so a range an error carried marks lines
// the source has and [SourceError.Range] reports one of them. A range
// that ends before its start, as given or after that cut, covers nothing,
// as [position.Range.LastLine] counts it, so it becomes the empty range at
// its start, and SourceError.Range never reports an end before the start.
// An end on a later line at a column before the first moves to the start
// of that line, so SourceError.Range never reports a negative column. A
// range within the lines comes back as it is.
func clampRange(lines line.Lines, r position.Range) position.Range {
	if last := lines.Len() - 1; last >= 0 && r.End.Line > last {
		r.End = position.New(last, lines.Line(last).Width())
	}

	if r.End.Line < r.Start.Line || (r.End.Line == r.Start.Line && r.End.Col < r.Start.Col) {
		return position.NewRange(r.Start, r.Start)
	}

	r.End.Col = max(r.End.Col, 0)

	return r
}
