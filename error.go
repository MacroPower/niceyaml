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
	"unicode"
	"weak"

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
	// ErrNoLocation indicates that a path resolves to a token that does not
	// carry a position. [Node.Ranges] returns it, and [SourceError.Unresolved]
	// reports it for an error bound at such a path.
	ErrNoLocation = errors.New("no location provided")

	// ErrMultipleDocuments indicates a [Source] that holds more than one YAML
	// document where a caller expects one. The count covers the documents
	// [Source.Documents] returns. [Source.Document] returns it, located at the
	// second of them. The document is at fault for it, as [IsInvalid]
	// describes.
	ErrMultipleDocuments = errors.New("multiple documents in source")

	// ErrDecodeTarget indicates that the value given to [Node.DecodeInto] or
	// [Node.DecodeIfPresent] is nil, a nil pointer, or not a pointer, so the
	// decode cannot write into it. The call returns the error bound to the
	// source as a [SourceError] with no location. It is a mistake of the
	// caller, so [IsInvalid] does not report it.
	ErrDecodeTarget = errors.New("decode target is not a non-nil pointer")

	// ErrSelfValidateTarget indicates that the value given to
	// [Node.SelfValidate], [Source.SelfValidate], or [SelfValidateValue] is
	// nil or a nil pointer, so the call does not have a value to validate.
	// Node.SelfValidate and Source.SelfValidate return the error bound to the
	// source as a [SourceError] with no location. SelfValidateValue binds it
	// with [BindValue], outside any document. It is a mistake of the caller,
	// so [IsInvalid] does not report it.
	ErrSelfValidateTarget = errors.New("self-validation target is nil")

	// ErrDecode indicates that the go-yaml decoder did not decode a node into
	// its target. [Node.Decode] and [Node.DecodeInto] return it, and so does a
	// [Validator] that decodes the node it checks, as a
	// [go.jacobcolvin.com/niceyaml/schema.Schema] does. These errors match:
	//
	//   - A value the decoder rejects, such as one of the wrong kind, a number
	//     that overflows its type, an alias with no anchor, or a field the
	//     target lacks under [UnknownFieldsReject].
	//   - The error that a value's own UnmarshalYAML or UnmarshalText method
	//     returns, unless it holds an Error from [Unchecked]. That error stays
	//     in the chain.
	//   - The error for a target type whose definition the decoder refuses,
	//     such as a struct with two fields of one name.
	//
	// The error of a context that ended does not match, even when an
	// unmarshaler wraps it. Neither does a [PanicError], [ErrDecodeTarget], or
	// [ErrExcessiveAliasing]. An error from [Unchecked] that an unmarshaler or
	// a [CustomUnmarshaler] function returns does not match either. Neither
	// does an error that a [Validator], a [SelfValidator], or a function from
	// [WithValueCheck] reports about the value it checks.
	//
	// Text the parser rejects matches [ErrSyntax] instead. [ErrUnknownField],
	// [ErrWrongKind], and [ErrOverflow] label some of the rejections, and a
	// problem with a label matches ErrDecode too. The document is at fault for
	// each problem that matches, as [IsInvalid] describes. The Decoding
	// section of the package documentation describes where each error binds.
	//
	// [errors.Is] reports whether any problem of an error matches ErrDecode,
	// so an error that matches can hold a problem of another kind beside it. A
	// detail from [WithDetails] is not a problem, so an error that names a
	// decode error only among its details does not match.
	ErrDecode = errors.New("value does not decode")

	// ErrUnknownField indicates a mapping key that does not match any field of
	// the target struct. A decode reports such a key under
	// [WithUnknownFields], as an error with [UnknownFieldsReject] or as a
	// warning with [UnknownFieldsWarn]. The problem reads `unknown field
	// "replicsa"` and carries the path of the key.
	//
	// ErrUnknownField, [ErrWrongKind], and [ErrOverflow] each label some of
	// the problems that match [ErrDecode], and a problem with a label matches
	// ErrDecode too. A label follows the message the decode writes, not the
	// go-yaml error behind it. A problem that keeps the text of go-yaml or of
	// an unmarshaler does not match any label. An alias with no anchor and
	// text that does not parse as a [time.Duration] are such problems. A
	// [CustomUnmarshaler] function that returns or wraps the rejection of its
	// decode function keeps the label of that rejection.
	//
	// [errors.Is] reports whether any problem of an error matches a label. A
	// caller that sorts the problems of a decode thus tests the Err of each
	// node [Problems] yields, with ErrDecode last:
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

	// ErrUnknownFieldsMode indicates a value or a text that does not name an
	// [UnknownFieldsMode]. [UnknownFieldsMode.MarshalText] and
	// [UnknownFieldsMode.UnmarshalText] return it. The caller of such a call
	// passed the value or the text, so [IsInvalid] does not report its
	// error. A decode of a document whose text does not name a mode wraps
	// the error beside [ErrDecode], and IsInvalid reports that error.
	ErrUnknownFieldsMode = errors.New("not an unknown fields mode")

	// ErrWrongKind indicates a value of a kind its target does not take,
	// such as a string for an integer or a sequence for a mapping. The
	// problem reads "expected integer, got string", with the kinds the
	// Decoding section of the package documentation lists. A value the
	// decoder rejects for its Go type alone matches too, and reads
	// "expected mapping, got mapping of another type".
	//
	// ErrWrongKind is a label on a problem that matches [ErrDecode], as
	// [ErrUnknownField] describes. A number outside the range of its
	// integer type matches [ErrOverflow] instead.
	ErrWrongKind = errors.New("value of the wrong kind")

	// ErrOverflow indicates a number outside the range of the integer type
	// that reads it, such as 300 for an int8. The problem reads "expected
	// integer from -128 to 127, got 300". An infinity for an integer matches
	// too. A float field does not report an overflow, so `1e400` there reads
	// "expected float, got string" and matches [ErrWrongKind].
	//
	// ErrOverflow is a label on a problem that matches [ErrDecode], as
	// [ErrUnknownField] describes. [ErrOutOfRange] is about a location outside
	// the source, not about a number.
	ErrOverflow = errors.New("number outside the range of its type")

	// ErrExcessiveAliasing indicates a node whose document holds so many
	// nested aliases that decoding the node reads far more than the document
	// holds. The go-yaml decoder can take minutes on such a document of a few
	// hundred bytes.
	//
	// [Node.Decode] and [Node.DecodeInto] refuse to decode a node that holds
	// an alias when the aliases of its document exceed the limit that
	// gopkg.in/yaml.v3 applies. A [Validator] that decodes the node it checks
	// returns the same error, as a [go.jacobcolvin.com/niceyaml/schema.Schema]
	// does. [WithAliasLimit] on the [Source] disables the limit for each of
	// them. Each returns the error bound as a [SourceError] at the alias of the
	// document that repeats the most nodes, as in "app.yaml:14:30: aliases
	// expand the document past the alias limit". The count covers the whole
	// document, so that alias can lie outside the node that the call decodes.
	// The document is at fault for the error, as [IsInvalid] describes, and it
	// does not match [ErrDecode].
	//
	// A decode into a type that reads text anywhere and [NewSourceFromLayers]
	// also fail when the aliases of a document repeat more text than the byte
	// limit of the Source allows. They fail as well when the aliases nest the
	// document past its depth limit. Either error matches [ErrLimit] too, and
	// it binds at the alias that takes the text past the bound, as ErrLimit
	// describes. WithAliasLimit disables those bounds along with the limit.
	// The alias limit does not have a size, so the error of that limit alone
	// does not match ErrLimit, as ErrLimit describes.
	//
	// [Node.Nodes] returns ErrExcessiveAliasing too, bound to its receiver
	// with no location, when aliases lead a selector of the path to far more
	// nodes than the document holds. IsInvalid does not report that error, and
	// WithAliasLimit does not disable that limit. The paths and schema
	// packages export the same error value.
	ErrExcessiveAliasing = aliaslimit.ErrExcessiveAliasing

	// ErrSyntax indicates text the go-yaml parser rejects, such as a flow
	// sequence with no closing bracket, a tab that indents a key, or a key a
	// mapping holds twice without [WithAllowDuplicateKeys]. Every error the
	// parser returns matches it. [Source.File] and [Source.Document] return
	// it, and [Node.Err] returns it for each document that the parser
	// rejected among the documents [Source.Documents] returns. When a
	// reference document from [WithReferences] does not parse, every decode
	// of a Source that reads it returns the error of that document.
	//
	// A document past the depth limit of [WithDepthLimit] does not parse
	// either, and neither does the one document of a text past the byte limit
	// of [WithByteLimit]. The errors of both match [ErrLimit] instead.
	//
	// Each of these calls returns the error bound as a [SourceError] at the
	// offending token. The error of a file with several such documents is a
	// join, which matches through each of them. [errors.As] still finds the
	// go-yaml error, although the chain does not unwrap to it. The document is
	// at fault for each problem the parse reports with ErrSyntax, as
	// [IsInvalid] describes.
	//
	// A panic in the parser does not match. A token without a position that
	// [NewSourceFromTokens] received can cause such a panic. The same calls
	// return the panic as a [PanicError], bound at the first token with a
	// position among the tokens the parser was reading.
	//
	// [errors.Is] reports whether any problem of an error matches ErrSyntax,
	// so an error that matches can hold a problem of another kind beside it. A
	// detail from [WithDetails] is not a problem, so an error that names a
	// syntax error only among its details does not match.
	ErrSyntax = errors.New("invalid YAML syntax")

	// ErrOutOfRange indicates that the location of an error lies outside the
	// source. The location starts on a line past the last or before the first,
	// or at a column before the first. A position or a range that came from
	// other text can start on such a line. [SourceError.Unresolved] reports
	// it. A number outside the range of its integer type matches [ErrOverflow]
	// instead.
	ErrOutOfRange = errors.New("location outside source")

	// ErrPathNeedsDocument indicates that [Source.Bind] bound an error that
	// carries a path in a source that does not hold a single document to
	// resolve the path in. It wraps the reason [Source.Document] gives:
	// [ErrMultipleDocuments], or the error the file fails to parse with.
	// [SourceError.Unresolved] reports it. Bind such an error through
	// [Node.Bind] with the document the check ran against. Node.Bind reports
	// it too for a path bound through a document that did not parse, where it
	// wraps the error that [Node.Err] returns.
	ErrPathNeedsDocument = errors.New("path needs a document to resolve in")

	// ErrAmbiguousPath indicates a path that names the entries of more than
	// one key of a decoded map. Keys of different types that the document
	// spells with one text share one path, such as 1 and "1" in a map[any]T.
	// Several NaN keys share one path too. Such a path resolves to the
	// document entry of one of them at most. A decode binds the errors a
	// [SelfValidator] reports under such a path without a position. Those
	// errors thus do not point at the line of another entry.
	// [SourceError.Unresolved] reports it.
	//
	// An error below a slice, an array, or a map that decodes itself binds at
	// that value instead, as the Validation section of the package
	// documentation describes.
	ErrAmbiguousPath = errors.New("path names the entries of several keys")

	// ErrLimit indicates a text past a limit of its [Source]. A file that
	// holds more bytes than [WithByteLimit] allows fails as a file that does
	// not read. A document that nests more levels than [WithDepthLimit]
	// allows fails as a document that does not parse, as [Node.Err] reports.
	// Its error matches ErrLimit and not [ErrSyntax]. The document is at
	// fault for each, as [IsInvalid] describes.
	//
	// The same limits bound the text that the aliases of a document expand
	// to, for a decode into a type that reads text anywhere and for
	// [NewSourceFromLayers]. A node that holds an alias fails such a decode
	// or merge when the aliases of its document repeat more text than the
	// byte limit allows. It also fails when they nest the document more
	// levels than the depth limit allows. Either error also matches
	// [ErrExcessiveAliasing] and binds at the first alias that takes the text
	// past the bound, as in "aliases repeat more than 256 KiB of text". The
	// Limits section of the package documentation describes the bounds.
	//
	// Errors that match ErrLimit name a limit that the caller sets as a size,
	// and a larger limit lets the same text pass. The alias limit of
	// [WithAliasLimit] does not have a size, so an error of that limit alone
	// matches [ErrExcessiveAliasing] and not ErrLimit. Programs that refuse
	// both kinds, such as servers that answer 413, test for both sentinels.
	//
	// The error of a schema past its load limit, as
	// [go.jacobcolvin.com/niceyaml/schema.WithLoadLimit] describes, matches
	// ErrLimit too. So does the error of a schema catalog past its limit. The
	// schema or the catalog is at fault for that error, not the document, so
	// IsInvalid reports false for it.
	ErrLimit = fault.ErrLimit

	// The reason of a [SourceError] whose error does not carry a location. A
	// missing location is not a failure to resolve one, so
	// [SourceError.Unresolved] reports nil for it.
	errUnlocated = errors.New("no location")

	// The mark of a problem the document is at fault for, which an error
	// matches from its Is method. It lives in an internal package because
	// the schema package matches the same value.
	errInvalid = fault.ErrInvalid

	// The empty source [BindValue] binds to, so an error bound to it reads as
	// its path and its message. Every call shares it, because it never
	// changes.
	valueSource = sync.OnceValue(func() *Source {
		return NewSourceFromString("")
	})

	// The targets an error from [panicked] never matches, even when the
	// [*PanicError] it holds does.
	panicRefused = []error{ErrDecode, ErrUnknownField, ErrWrongKind, ErrOverflow, ErrSyntax, errInvalid}
)

// Error is an error that points at a location in a YAML document. The
// location is a path to the value the error is about, a position or a range
// of the characters at fault, or a path with either. An Error holds what a
// check knows but not a source, so a validator builds one from a path
// alone. Binding the Error through a [Node] resolves the location and
// produces a [*SourceError], which puts the position and the path in front
// of the message. [Error.Error] returns the message alone.
//
// An Error is one problem. [WithDetails] adds the errors that explain it,
// and [NewSummary] heads several problems with one message. The constructor
// says whether the document is at fault for the Error, as [IsInvalid]
// describes. The Errors section of the package documentation describes
// these roles, the locations, and the faults.
//
// [errors.Is] and [errors.As] search the error an Error wraps and each
// problem a summary heads, as [Error.Unwrap] returns them, and skip the
// details. An Error never changes after its creation, and [Error.With]
// returns a copy with more options applied.
//
// Create instances with [NewError], [Invalid], [Unchecked], [Locate], or
// [NewSummary].
type Error struct {
	err    error
	move   *move
	errors []error
	errorConfig
	fault declaration
	// The severity mark of the Error, which decides the severity of the
	// problem below it, as [severityOf] reads it.
	severity severity
	// The Error is a heading that counts the errors and the warnings it
	// heads, so [AsErrors] counts them again.
	tally bool
}

// declaration is what an [Error] declares about the fault of the document.
// The values rise in strength, so where two apply to one error, the
// larger one holds.
type declaration uint8

const (
	// Like an Error from [Rebase] or [Locate], the Error does not declare
	// anything.
	undeclared declaration = iota
	// The Error comes from [Unchecked], so its check could not run. A
	// fault that a hook presumes does not reach the error the Error wraps.
	unchecked
	// A hook presumes the document at fault for an error that the code it
	// calls returns, as a decode does for each error of a [SelfValidator]
	// or a function from [WithValueCheck].
	presumed
	// [NewError] or [Invalid] declared the fault.
	declared
)

// invalid reports whether the document is at fault under d.
func (d declaration) invalid() bool {
	return d >= presumed
}

// on returns the declaration d puts on err. A presumed fault does not
// reach an err that holds an Error from [Unchecked] on its cause chain,
// and any other d reaches it unchanged.
func (d declaration) on(err error) declaration {
	if d == presumed && holdsUnchecked(err) {
		return undeclared
	}

	return d
}

// under returns the declaration that the errors below x take when d comes
// from above x. A fault that x declares passes down when it is stronger
// than d, and an x from [Unchecked] stops a presumed fault.
func (d declaration) under(x *Error) declaration {
	switch {
	case x.fault == unchecked && d == presumed:
		return undeclared
	case x.fault.invalid() && x.fault > d:
		return x.fault
	default:
		return d
	}
}

// move is how an [Error] from [Rebase] moves the paths below it.
type move struct {
	// The path that the errors below write their `@` paths from. A path with
	// no selectors is a base like any other.
	base paths.Path
	// Where a path below binds when the document does not hold it. Its
	// paths read from the node that base reads from. The rebase of the
	// self-validation walk sets it below a value that decodes itself.
	fallback fallback
	// Like the move of a detail, the move shifts paths alone and does not
	// point an error at its base. A detail takes the base of the problem it
	// explains.
	movesOnly bool
	// The move comes from a binding in a layer of a merged [Source], so its
	// base replaces the root of a `$` path below it. A Rebase leaves such a
	// path as it is.
	reroots bool
	// The base names the entries of several keys of a decoded map, so it does
	// not resolve in any document. [SourceError.Unresolved] reports
	// ErrAmbiguousPath for every error below.
	ambiguous bool
}

// NewError creates a new [*Error] with the given message. The Error is a
// problem the document is at fault for, so [IsInvalid] reports it. Use
// [Invalid] instead to declare the document at fault for an existing error.
//
// An Error can carry a location without a message, as a detail that marks a
// related location does. NewError builds such an Error from an empty
// message:
//
//	niceyaml.NewError("", niceyaml.AtPath(firstUse))
//
// Its binding names the position and the path alone, and an excerpt marks
// the location with a caret run alone.
func NewError(msg string, opts ...ErrorOption) *Error {
	e := wrapUndeclared(errors.New(msg), opts...)
	e.fault = declared

	return e
}

// Invalid creates a new [*Error] that wraps err and declares the document
// at fault for it, so [IsInvalid] reports it. The document is at fault for
// each problem the Error heads too, such as each branch of a join it wraps,
// bound or not. An err that matches [context.Canceled] or
// [context.DeadlineExceeded], or a join with such a branch, gets the
// options but not the declaration. Such an error is about the call, not the
// document. A detail of err does not decide the fault. Use [NewError]
// instead to create an error from a message, [Unchecked] to declare that a
// check could not run, and [Locate] to add a location without a
// declaration.
//
// Invalid returns nil for a nil err, and for a nil [*Error] or
// [*SourceError] pointer, whatever the options. It returns an error rather
// than an [*Error], so the nil compares equal to nil wherever it goes. A
// validator thus returns the result as it is:
//
//	return niceyaml.Invalid(spec.Check())
//
// An err that is a join stays a list of problems. The options give their
// location to each problem of the join that no source bound and that does
// not carry a location of its own. A problem with a location of its own
// keeps it, so the first problem here binds at sizePath and the second at
// namePath:
//
//	return niceyaml.Invalid(errors.Join(
//		errors.New("too big"),
//		niceyaml.NewError("name is taken", niceyaml.AtPath(namePath)),
//	), niceyaml.AtPath(sizePath))
//
// The join does not have a line of its own, so the location shows only on
// the lines of the problems that take it. The Errors section of the package
// documentation describes a location on a heading. [Locate] describes the
// location that an err that is bound already keeps.
func Invalid(err error, opts ...ErrorOption) error {
	if isNothing(err) {
		return nil
	}

	e := wrapUndeclared(err, opts...)
	if !contextEnded(err) {
		e.fault = declared
	}

	return e
}

// Unchecked creates a new [*Error] that wraps err, gives it the location
// and the details of the options, and declares that the check could not
// run. A check whose I/O fails returns its error this way, at the value it
// read:
//
//	return niceyaml.Unchecked(fmt.Errorf("stat license: %w", err),
//		niceyaml.AtRange(licenseRange), niceyaml.WithDetails(why))
//
// The result binds and prints as one from [Invalid] with the same options
// does. Unchecked does not declare a fault, so [IsInvalid] reports the
// result only when it reports err. An err the document is at fault for
// through its own chain, such as an Error from [NewError] or a decode
// rejection, stays a fault inside Unchecked. An Unchecked inside Invalid is
// a fault too.
//
// A hook presumes the document at fault for an error that it returns and
// that does not declare anything, as the Errors section of the package
// documentation describes. An Error from Unchecked on the cause chain of
// the error cancels that presumption. An I/O error that a Validate method,
// a function from [WithValueCheck], or an unmarshaler wraps in Unchecked is
// thus not a fault. Such an error from an unmarshaler does not match
// [ErrDecode] either.
//
// An Unchecked around a join cancels the presumption for each branch it
// heads. A branch that declares a fault itself stays a fault. An Unchecked
// in one branch of a join cancels the presumption for that branch alone. So
// does an Unchecked in one branch of a wrapper that [fmt.Errorf] builds
// with several %w verbs and that heads several problems.
//
// Unchecked returns nil for a nil err, and for a nil [*Error] or
// [*SourceError] pointer, whatever the options. It returns an error rather
// than an [*Error] for the reason Invalid gives. An err that is a join
// stays a list of problems, as Invalid describes. The options give their
// location to each problem of the join that no source bound and that does
// not carry a location of its own.
func Unchecked(err error, opts ...ErrorOption) error {
	if isNothing(err) {
		return nil
	}

	e := wrapUndeclared(err, opts...)
	e.fault = unchecked

	return e
}

// Locate creates a new [*Error] that wraps err and gives it the location
// and the details of the options. It does not declare anything, so the
// fault stays with err. A check that calls a helper adds the location of
// the value to the error of the helper this way. The error keeps what the
// helper declared:
//
//	if err := checkName(cfg.Name); err != nil {
//		return niceyaml.Locate(err, niceyaml.AtPath(paths.Current().Child("name")))
//	}
//
// With a location or details, the result binds and prints as one from
// [Invalid] or [Unchecked] with the same options does. A result with
// neither reads as err does in a wrapper with several %w verbs. A result
// around a plain error thus reads as a sentinel there.
//
// [IsInvalid] reports the result only when it reports err. A hook presumes
// a fault for the result exactly when it presumes one for err, as the
// Errors section of the package documentation describes. A plain I/O error
// that a Validate method or a function from [WithValueCheck] returns inside
// Locate is thus a fault. Such a check should wrap the error in Unchecked
// instead. A [Validator] declares its own faults, so a plain error that it
// returns inside Locate is not a fault.
//
// Locate returns nil for a nil err, and for a nil [*Error] or
// [*SourceError] pointer, whatever the options. It returns an error rather
// than an [*Error] for the reason Invalid gives. An err that is a join
// stays a list of problems, as Invalid describes. The options give their
// location to each problem of the join that no source bound and that does
// not carry a location of its own. To move the `@` paths of an error under
// the path of a value, use [Rebase].
//
// An err that is bound already, such as an error of a [Node] method, keeps
// the location of its binding when that location resolved in the source
// that binds the result. The binding names the value at fault, and its
// excerpt marks that value. The location of the options thus does not
// show, and the details still do.
//
// The options give their location to any other binding outside a join,
// such as one whose location did not resolve or one of an included file.
// The line of that binding then follows the location in the message. It
// keeps the path of the binding, as in the message "$.license.owner: no
// owner" of a result located at $.license. [Invalid] and [Unchecked] treat
// a bound err the same way.
//
// The line of that binding leaves out the "warning:" label, because the
// row shows the severity of the result. A warning of an included file
// thus reads "main.yaml:1:10: warning: $.include: inc.yaml:2:9: $.y.deep:
// old deep". When the source of that binding binds the result, the line
// leaves out the name of that source too, because the location names that
// source already. The line leaves out the document of the binding when
// the result is bound to that same document. It leaves out the path of
// the binding when the row of the result shows that path and the line
// does not name a document.
//
// When a source binds a result with a location, each binding in err that
// [Detach] detached from that source counts as a binding of that source.
// Err holds such a binding when err leads to it through wrappers, joins,
// and errors that no source bound, as [Bindings] finds it. Such a binding
// can be err itself, a problem of a join or a summary in err, or a detail
// of an [*Error] in err. Each binding below it that the same call of
// Detach detached from that source counts as one too, such as a detail.
// Their lines read as the lines of live bindings do, as in "m.yaml:4:4:
// $.c: plain".
//
// These bindings share an excerpt with the other bindings of that source,
// and [Annotate] marks them on a view of the source. When err is such a
// binding and its location resolved, the bound result keeps that location
// and binds in that source again, as it does for a live binding.
// For each binding that binds in the source again, [SourceError.Node]
// reports the Node at the path of the Node that its detached copy was
// bound to, as [Node.At] returns it. A detached detail of the options and
// a detached binding below a live binding in err each render as a binding
// of a source of their own, as Detach describes.
//
// A problem of a join that a source bound keeps its own place, whether the
// source bound the whole join or that problem alone. The location of the
// options does not show on it. A join of "a" and "b" that main.yaml binds
// thus reads "main.yaml: a" and "main.yaml: b" inside Locate too. So does
// a join of main.Bind(a) and main.Bind(b).
//
// Before a source binds the result, the result reads as the binding
// inside it when the location of that binding resolved, whatever its
// source. It reads as that binding too when the options name the path of
// the binding and its location did not resolve. The line of the binding
// shows that path already. Other located results can sit between the two,
// as when a helper locates the error at a parent value and its caller at
// that path. A source that binds the result takes the location of the
// outermost result. The locations of the results between thus do not
// show, before the bind or after it. The message of the result,
// [Problems], [NewErrorTree], and [FormatError] show that binding at its
// own location.
//
// [Error.Path] of the result reports the path of a binding whose location
// resolved, before and after the bind. For a binding of another source,
// [SourceError.Path] of the bound result reports the location of the
// options. A program that reports one problem of an included file at the
// place of the include thus binds the result to the including source, as
// [Node.Validate] does.
func Locate(err error, opts ...ErrorOption) error {
	if isNothing(err) {
		return nil
	}

	return wrapUndeclared(err, opts...)
}

// holdsUnchecked reports whether an [Error] from [Unchecked] sits on the
// cause chain of err. The walk skips details and the problems a summary
// heads, and it stops at a join, whose branches each answer for themselves.
// A wrapper with several %w verbs that heads several problems stops the
// walk as a join does. A wrapper with a single problem among its branches
// continues through that problem. An Error from Unchecked is always a
// problem, so the walk never drops one as a sentinel. Any other error with
// several branches is one problem, so an Unchecked in any of its branches
// counts.
func holdsUnchecked(err error) bool {
	for cur := err; !isNothing(cur); {
		switch x := cur.(type) { //nolint:errorlint // Walks the chain one node at a time.
		case *Error:
			if x.fault == unchecked {
				return true
			}

			cur = x.err

		case interface{ Unwrap() error }:
			cur = x.Unwrap()

		case interface{ Unwrap() []error }:
			branches := x.Unwrap()

			if multierr.IsWrapErrors(cur) {
				_, cur = followBranches(cur, branches)

				continue
			}

			if multierr.IsJoin(cur) || multierr.JoinMessage(cur.Error(), branches) {
				return false
			}

			return slices.ContainsFunc(branches, holdsUnchecked)

		default:
			return false
		}
	}

	return false
}

// wrapUndeclared returns an [*Error] around err with opts applied. The
// Error does not match [errInvalid] itself, so it does not declare anything
// about err. [NewError], [Invalid], [Unchecked], and [Locate] build on it,
// and a decode uses it to put a location on an error it rewrites.
func wrapUndeclared(err error, opts ...ErrorOption) *Error {
	e := &Error{err: err}
	for _, opt := range opts {
		opt(&e.errorConfig)
	}

	return e
}

// NewSummary creates a new summary of errs: an [*Error] whose message msg
// heads several separate problems, such as "3 schema violations" above one
// error per violation. The summary is a heading and never a problem itself.
// [SourceError.Error] lists each of errs below the line of the summary.
// [ErrorTree.Problems] yields each of them and never the summary, and
// [FormatError] draws each as a branch.
//
// [Rebase] and a scoped [Node.Bind] do not give a location to a summary.
// They locate each of errs on its own, as they locate each branch of a
// join. A validator's summary above violations without a location thus
// binds each violation at the value it checked. A location that
// [Error.With] puts on a summary stands in front of the line of the
// summary. Each of errs that does not carry a location takes that location
// in place of the base of a Rebase or a scoped Node.Bind.
//
// NewSummary skips a nil error in errs. When errs is empty or holds only nil
// errors, it returns nil. When one error is left, it returns that error
// alone, because a heading such as a count does not add information to one
// error. It returns an error rather than an [*Error], as [errors.Join] does,
// so the nil compares equal to nil wherever it goes. A validator therefore
// returns the result for any number of violations:
//
//	return niceyaml.NewSummary(fmt.Sprintf("%d violations", len(errs)), errs...)
//
// [errors.Join] lists several problems without a heading. An error built
// with [WithDetails] is one problem, with the errors that explain it below
// it.
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
// fault for and the [Error] declares that fault. The module keeps the mark
// internal, so the method does not match any target that a caller can name.
// A caller asks [IsInvalid] or [ErrorTree.IsInvalid] instead of
// [errors.Is].
//
// An Error from [NewError] or [Invalid] declares the fault, with a location
// or without. So does the Error a decode puts around an error that a
// [SelfValidator] or a function from [WithValueCheck] returns, unless an
// Error from [Unchecked] sits on the cause chain of that error. An Error
// from Unchecked, [Locate], or [Rebase], a summary from [NewSummary], and
// the Error a scoped [Node.Bind] puts around an error do not declare a
// fault. Neither does an Error from Invalid around the error of a context
// that ended. Any other target matches through the errors the Error wraps,
// as [Error.Unwrap] returns them.
func (e *Error) Is(target error) bool {
	return target == errInvalid && e != nil && e.fault.invalid()
}

// IsInvalid reports whether the document is at fault for every problem of
// err. Callers use it to pick a status code or an exit code. The problems
// are the nodes that [ErrorTree.Problems] yields for err, so
// IsInvalid(err) and NewErrorTree(err).IsInvalid() agree. One problem that
// is not a fault makes IsInvalid report false for the whole error.
// Headings and details do not decide the result. IsInvalid also reports
// false for a nil err and for an error that does not hold a problem, such
// as one with an empty message.
//
// A retry does not clear a fault of the document. Errors that hold a fault
// beside a problem of another kind thus fail again, although IsInvalid
// reports false for them. Callers that decide whether to retry ask
// [ErrorTree.IsInvalid] of each problem instead.
//
// The code that builds an error declares the fault, as the Errors section
// of the package documentation describes. Beside the errors that section
// names, the document is at fault for [ErrMultipleDocuments] and for a
// value that [Node.At] or [Node.Ranges] does not find. It is also at fault
// for a schema violation, for the ErrNoMatch of a schema registry, and for
// [ErrExcessiveAliasing] from every reader but [Node.Nodes].
//
// Servers ask IsInvalid before they test for a sentinel, because
// [errors.Is] reports whether any problem matches:
//
//	switch {
//	case err == nil:
//	case !niceyaml.IsInvalid(err):
//		status = 500 // schema load, canceled context, I/O, panic
//	case errors.Is(err, niceyaml.ErrSyntax):
//		status = 400
//	default:
//		status = 422 // decode rejection, violation, Validate method, value check
//	}
func IsInvalid(err error) bool {
	return NewErrorTree(err).IsInvalid()
}

// With returns a copy of the [Error] with the given options applied. The
// receiver does not change, so each caller that shares an Error can
// specialize a copy for its own use:
//
//	located := err.With(niceyaml.AtPath(namePath))
//
// A location on a summary from [NewSummary] reaches each problem it heads
// that does not carry one, as NewSummary describes.
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

// Rebase returns err with base in front of every `@` path in its tree. The
// paths it moves are those on the [*Error] that anchors err, on each
// problem that a summary or a join heads, and on each detail. A `$` path
// stays as it is, as [paths.Path.Join] leaves it, and so does a position or
// a range.
//
// Each problem that does not carry a location points at base itself, and a
// heading or a detail does not gain a location. A heading inside err that
// carries a location gives that location, in place of base, to each problem
// below it that does not carry one. Rebases compose, and a `$` base stops
// the bases above it from moving the paths below it. The Errors section of
// the package documentation describes rebasing.
//
// A check written for a type writes `@` paths from the value it checks. A
// caller that runs it on a value inside a document rebases the result under
// the path of that value before it binds the result:
//
//	return doc.Bind(niceyaml.Rebase(checkHours(&cfg.Hours), paths.Doc().Child("hours")))
//
// A path names a map key as the document spells it, so a base built from a
// decoded key might miss. The key 0x10 of a map[int]Server decodes to 16,
// and an error under `$.ports.16` then binds at the `ports:` key, as
// [SourceError.Nearest] describes. A [DataLocator] finds an entry by its
// decoded key.
//
// The result keeps the message of err, and [errors.Is] and [errors.As] see
// through it, including through a join that Rebase rebuilds branch by
// branch. Rebase does not declare anything, so [IsInvalid] reports the
// result only when it reports err. Rebase returns nil for a nil err, and
// for a nil [*Error] or [*SourceError] pointer. It returns an error that is
// bound already as it is, except the result of [BindValue], which it
// rebases as the error that BindValue bound.
func Rebase(err error, base paths.Path) error {
	return rebase(err, move{base: base}, undeclared)
}

// rebase is [Rebase] with the paths of err moved as m moves them. Each
// Error it builds declares d, as [declaration.on] puts d on the error it
// wraps. A binding does not take a base, and one that does not stand in a
// document rebases as [placeable] returns it.
func rebase(err error, m move, d declaration) error {
	if isNothing(err) {
		return nil
	}

	err = placeable(err)
	if isNothing(err) {
		return nil
	}

	if isBound(err) {
		return markFault(err, d)
	}

	d = d.on(err)

	// An Error that adds nothing to the join it wraps rebases as the join,
	// with its mark on each branch, and the copy keeps what the Error
	// declared.
	x, ok := err.(*Error) //nolint:errorlint // The node itself, not a chain search.
	if ok && x.addsNothing() {
		if _, _, joined := joinBranches(x.err); joined {
			below := d.under(x)

			return &Error{err: rebase(x.err, m, below), fault: max(x.fault, below)}
		}
	}

	joined := mapJoin(err, func(branch error) error {
		return rebase(branch, m, d)
	})
	if joined != nil {
		return joined
	}

	return &Error{err: err, move: &m, fault: d}
}

// markFault returns err with d on it, as [declaration.on] puts d on err. An
// err whose own Error declares as much stays as it is, and so does an err
// that d does not declare a fault for. It wraps any other err in an Error
// that adds only the mark. The result keeps the text, location, and
// children of err, and [isBound] still reports it.
func markFault(err error, d declaration) error {
	if isNothing(err) {
		return err
	}

	d = d.on(err)
	if !d.invalid() {
		return err
	}

	x, ok := err.(*Error) //nolint:errorlint // The node itself, not a chain search.
	if ok && x.fault >= d {
		return err
	}

	return &Error{err: err, fault: d}
}

// ErrorOption configures an [Error]. [NewError], [Invalid], [Unchecked],
// [Locate], and [Error.With] take the same options. [AtPath] sets the path
// of the Error. [AtPosition] or [AtRange] sets its position or range, and
// the last one given wins. [WithDetails] adds the errors that explain it. A
// [DataLocator] returns the options that locate an error by the names of
// decoded data.
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
	// The position or the range: a position.Position or a position.Range, or
	// nil when no option sets either.
	loc any
	// The errors from WithDetails, without the nil ones.
	details []error
	// The path, when hasPath is true. A path with no selectors is a path like
	// any other, so hasPath marks whether an option set a path.
	path    paths.Path
	hasPath bool
}

// AtPath is an [ErrorOption] that sets the YAML path of the value the error
// is about. It replaces a path that an earlier option set. An `@` path
// resolves from the [Node] that binds the Error and a `$` path from the
// root of the document, as the Errors section of the package documentation
// describes.
//
// The error points at the node the path selects, which for a mapping entry
// is its value. A mapping or a sequence spans many lines, so an error there
// points at the token that introduces it, as [paths.Resolver.Token]
// describes. That token is the key of the entry that holds it, or the "-"
// of its element in a block sequence. Without either, it is the "{" or "["
// of a flow collection, or the first key or element of a block collection
// at the root. A path from [paths.Path.Key] points at the key of the entry
// whatever its value holds. Such a path suits an error about the key
// itself:
//
//	niceyaml.NewError("unknown field", niceyaml.AtPath(paths.Current().Child("spec", "foo").Key()))
//
// A path that the document leaves out, such as the path a check for a
// required field names, binds at the key of the mapping that lacks the
// value. A path that enters an alias the document cannot follow binds at
// the alias, as [SourceError.Nearest] describes. With [AtPosition] or
// [AtRange], the error binds there instead, and the path names the value in
// the message alone. [DataLocator.At] builds both from the names of decoded
// data.
func AtPath(p paths.Path) ErrorOption {
	return func(c *errorConfig) {
		c.path, c.hasPath = p, true
	}
}

// AtPosition is an [ErrorOption] that sets the 0-indexed position where the
// error occurred. It replaces a position or a range that an earlier option
// set. The position is in the coordinates of the lines [Source.Lines]
// returns, which count the first line of the text as 0.
// [SourceError.Excerpt] highlights the content of the token at that
// position. A path from [AtPath] on the same Error names the value in the
// message, and the position locates the error. A producer that holds a
// go-yaml token converts it with [position.NewFromToken], and one that does
// not hold a token builds the position itself.
func AtPosition(p position.Position) ErrorOption {
	return func(c *errorConfig) {
		c.loc = p
	}
}

// atToken returns an [ErrorOption] that sets the position of tk, or one
// that does not set anything when tk or its position is nil. The token of a
// go-yaml error, or its position, might be nil.
func atToken(tk *token.Token) ErrorOption {
	if tk == nil || tk.Position == nil {
		return func(*errorConfig) {}
	}

	return AtPosition(position.NewFromToken(tk))
}

// AtRange is an [ErrorOption] that sets the 0-indexed range the error
// covers. It replaces a position or a range that an earlier option set. The
// range is in the coordinates of the lines [Source.Lines] returns, which
// count the first line of the text as 0. [SourceError.Excerpt] highlights
// the whole range rather than one token. AtRange thus suits a check that
// knows the columns an error covers, such as one that runs on rendered
// lines.
//
// A path from [AtPath] on the same Error names the value in the message,
// and the range locates the error. A check on the characters inside a value
// thus names the value and highlights the characters:
//
//	niceyaml.NewError("invalid character", niceyaml.AtPath(namePath), niceyaml.AtRange(charRange))
//
// A range that ends before its start does not cover any text, so it binds
// as the empty range at its start.
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
// The Error stays one problem, and a detail is never a problem of its own.
// [ErrorTree.Problems] yields the Error once, with its details as the
// Children of its node. [SourceError.Error] leaves the details out of its
// message, but [FormatError] draws each as a branch.
//
// Binding binds each detail at the location its own error carries, if it
// carries one. A detail that is or wraps a [*SourceError] stays as it is,
// so it can point into another file. [Rebase] and a scoped [Node.Bind] move
// the paths of a detail but do not give it a location. A location on the
// Error or on a heading above it does not reach a detail either. A join
// does not have a line of its own, so the details that [Invalid],
// [Unchecked], or [Locate] put on a join sit beside its problems.
//
// [errors.Is] and [errors.As] on the Error skip its details, so a sentinel
// that a detail wraps never matches for the problem it explains.
// WithDetails skips a nil error. To report several separate problems, join
// them with [errors.Join] or head them with [NewSummary].
func WithDetails(errs ...error) ErrorOption {
	return func(c *errorConfig) {
		for _, n := range errs {
			if !isNothing(n) {
				c.details = append(c.details, n)
			}
		}
	}
}

// Error returns the message of the error the [Error] wraps, without a
// location in it. A wrapper such as [fmt.Errorf] around the Error thus
// holds the message alone. The [SourceError] that binds the Error puts the
// position and the path that [Error.Path] reports in front of the whole
// text. The errors a summary heads and the details stay out of the message
// too. An Error from [NewError] with an empty message and a nil Error both
// return an empty string. [FormatError] and [BindValue] put the path in
// front of an Error that a binding does not hold.
func (e *Error) Error() string {
	if e == nil || e.err == nil {
		return ""
	}

	return e.err.Error()
}

// Format implements [fmt.Formatter].
//
// The %v and %s verbs print [Error.Error], which does not hold a location.
// The %+v verb prints what [FormatError] renders for the error: its message
// behind its path, with the errors below it as a tree. The tree shows at
// most [ErrorListLimit] problems and a last row that counts the rest. The
// excerpt of a binding below the error takes the default options, or, for a
// detached binding, the options [Detach] received. Every other verb formats
// [Error.Error] as it formats a string, with the width, precision, and
// flags given, so %q quotes the message.
func (e *Error) Format(f fmt.State, verb rune) {
	formatVerb(f, verb, e)
}

// LogValue implements [slog.LogValuer].
//
// The value is the tree [FormatError] prints for the error, as a string
// without a source excerpt. It shows at most [ErrorListLimit] problems and
// a last row that counts the rest. A wrapper such as [fmt.Errorf] around
// the Error logs as its own message, which does not hold a path. A program
// that wants the path of any error thus logs the string FormatError
// returns.
func (e *Error) LogValue() slog.Value {
	return logValue(e)
}

// locus is the location an [Error] carries: a path when hasPath, a position
// or a range in loc, or both. An ambiguous path names entries of several
// keys of a decoded map, so [locate] does not resolve it in any document.
type locus struct {
	loc       any
	path      paths.Path
	fallback  fallback
	hasPath   bool
	ambiguous bool
}

// rebase returns l with base joined in front of its path and its fallback
// paths, as [paths.Path.Join] joins them. It returns a locus without a path
// unchanged, because a base moves paths alone.
func (l locus) rebase(base paths.Path) locus {
	if l.hasPath {
		l.path = base.Join(l.path)
		l.fallback = l.fallback.rebase(base)
	}

	return l
}

// fallback is where an error binds when the document does not hold its
// path. The self-validation walk sets one below a value that decodes
// itself, because the fields of such a value do not have to mirror the
// document.
type fallback struct {
	// The paths of the values above the error that decode themselves, the
	// nearest first. The error binds at the first one the document holds.
	at []paths.Path
	// The first of at is a slice, an array, or a map, and field tags do not
	// place its elements. The error thus binds at the first of at that the
	// document holds, even when the document holds the path of the error.
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

// then returns f followed by outer, the fallback of an [Error] above, when
// the paths of both read from one node. The values of f come first because
// they lie lower. An outer that binds every error below it stands alone.
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

// apply returns l moved as m moves the errors below it. A rerooting base
// replaces the `$` root, because the base is where a layer holds the merged
// value. An only fallback stands in for the path, so m alone sets
// ambiguous.
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

// at returns the location that an [Error] that moves paths as m does gives
// each problem below it that does not carry a location of its own. That
// location is the base of m, with the fallback and the ambiguous mark of m.
func (m *move) at() locus {
	return locus{path: m.base, hasPath: true, ambiguous: m.ambiguous, fallback: m.fallback}
}

// then returns the move of a child below m and then inner, with the bases,
// fallbacks, and marks joined as [move.apply] joins them from below. A nil
// inner does not move anything, and the result never reroots.
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

// addsNothing reports whether e does not add anything to the error it
// wraps, so e has the text and the children of that error.
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
// the [Error] wraps, then each error a summary from [NewSummary] heads. A
// match thus means that the problem matches, or a problem the summary
// heads.
//
// Unwrap leaves out the details from [WithDetails], which explain a problem
// and are not problems themselves. A sentinel or a typed error that a
// detail wraps thus never matches for the problem it explains. In the tree
// that [NewErrorTree] builds, the details are the children of the node of
// the Error that have Detail set. A nil Error does not unwrap to any error,
// so a caller can safely walk a chain that holds one.
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

// location returns the locus of the nearest located Error along the chain
// of e, as [anchorOf] finds it. A [*SourceError] there reports the locus it
// bound, without a base from above, so e and its binding agree.
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

// Path returns the [paths.Path] the [Error] is about and true, or the zero
// Path and false when the Error does not carry one. The location is the one
// [AtPath] set on the Error itself or on the nearest located Error along
// its cause chain. An Error built with [Invalid] around a located Error
// thus reports that location, with the base of every [Rebase] on the way
// joined in front.
//
// A [*SourceError] along the chain ends the walk. The Error then reports
// the path [SourceError.Path] reports for that binding, without the base of
// a Rebase above the binding. A binding whose location resolved reports
// that location even below an Error with a location of its own. [Locate]
// describes the location that such an Error takes when a source binds it.
// An Error that carries a position or a range beside the path reports
// both. A nil Error does not have a path.
func (e *Error) Path() (paths.Path, bool) {
	l := e.location()

	return l.path, l.hasPath
}

// Position returns the [position.Position] the [Error] points at and true,
// or the zero Position and false when the Error does not carry a position,
// such as when it carries a range. It looks through wrapping as
// [Error.Path] does.
func (e *Error) Position() (position.Position, bool) {
	p, ok := e.location().loc.(position.Position)

	return p, ok
}

// Range returns the [position.Range] the [Error] covers and true, or the
// zero Range and false when the Error does not carry a range, such as when
// it carries a position. It looks through wrapping as [Error.Path] does.
// The range is the one [AtRange] set, in the coordinates of [Source.Lines].
// [SourceError.Range] returns the range a location of any kind resolved to
// after a binding holds the Error.
func (e *Error) Range() (position.Range, bool) {
	r, ok := e.location().loc.(position.Range)

	return r, ok
}

// location is a resolved error location. A path can bind at a node other
// than its own, for a missing key, an alias, or a fallback. Such a path
// sets tk to the token of that node and near to its path, and an alias also
// sets unfollowed.
type location struct {
	unfollowed error
	rng        *position.Range
	tk         *token.Token
	near       *paths.Path
	// The path of the merged root in the document of the layer, when the
	// location resolved in a layer of a [NewSourceFromLayers] document.
	layerRoot *paths.Path
	pos       position.Position
}

// highlight returns the ranges to highlight for l in lines, as
// [excerpt.Ranges] finds them.
func (l location) highlight(lines line.Lines) position.Ranges {
	return excerpt.Ranges(lines, l.rng, l.tk, l.pos)
}

// locate resolves l, the location of an [Error], and returns the node it
// binds to. The path must start at `$`, with every base and the scope
// joined in already. A position or a range wins over a path beside it.
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

// locateBelow resolves the path of l, or else the first fallback path
// that resolves, which becomes near unless it equals the path. When every
// path fails to resolve, it returns the result of the first path it tried.
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

// locatePath resolves path, which starts at `$`, in the document b binds
// with, or the one document of the source when b routes. A merged document
// resolves it in a layer, whose node belongs to another source than b.
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

// locateMerged resolves the position at in node, a merged document. The
// text of that document is not a file of the caller, so the error binds at
// the value at that position in the layer that holds it. Without a value
// there, the path of l locates the error.
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
// message and lists the problems a heading heads, one per line.
// [SourceError.Excerpt] returns the surrounding lines with the location
// highlighted. A SourceError unwraps to the error it binds, so [errors.Is]
// and [errors.As] see through it.
//
// Binding binds the whole tree of the error. Each problem that a summary or
// a join heads, and each detail, binds as a SourceError of its own at the
// location its own error carries. [NewErrorTree] arranges them as a tree.
//
// A problem that does not carry a location binds at the location of the
// nearest heading above it that carries one, unless a [Rebase] lies between
// them. A path that the document leaves out binds at the nearest node above
// it, as [SourceError.Nearest] describes. Any other location that does not
// resolve costs the SourceError its position, for the reason
// [SourceError.Unresolved] returns. The Errors section of the package
// documentation describes these cases.
//
// [FormatError] prints the tree and the excerpts as plain text, however a
// caller wrapped or joined the error:
//
//	if _, err := source.File(); err != nil {
//		log.Print(niceyaml.FormatError(err))
//	}
//
// A SourceError keeps its Source alive, with every line, token, and parse
// of it, until the program drops the error. A program that keeps the error
// longer than the Source calls [Detach]. Detach returns a copy that keeps
// what renders the error but not the Source.
//
// Create instances with [Node.Bind] or [Source.Bind], or receive them from
// the [Source] and [Node] methods. [BindValue] creates one for a value that
// did not come from a document.
type SourceError struct {
	err error
	// The error that a [BindValue] binding, or a binding below one, bound.
	// [placeable] hands it to a later binding in place of this one. It is nil
	// for every other binding.
	free error
	// The reason the location did not resolve, or nil when it did:
	// errUnlocated, the error of a path, or ErrOutOfRange.
	locErr error
	source *Source
	// The node that paths resolved from. It is nil for an error that the
	// Source did not bind to a document.
	node *Node
	// The binding the first line of the message comes from when Error leaves
	// the message as it is. It is nil when Error puts a position or a name in
	// front, or when the first line did not come from a binding.
	lead *SourceError
	// What a detached binding keeps of the source and the document it was
	// bound in, in place of source and node, which are nil. Both are nil for a
	// binding that holds its source. A detached binding that was not bound to
	// a document has a nil keptDoc.
	kept    *keptSource
	keptDoc *keptDocument
	// The location, which binding resolved.
	loc location
	// The error that ends the cause chain with children below it.
	end chainEnd
	// The ranges the excerpt highlights.
	ranges position.Ranges
	// The bound children in the order the cause chain reaches them: the
	// problems a summary or the join that ends the chain heads, and the
	// details of every Error along the chain.
	below []boundChild
	// The texts of the binding, which texts builds the first time a
	// caller asks for one.
	built boundTexts
	// The range the location covers in the source.
	rng position.Range
	// Makes passedLead find lead once, the first time a message asks.
	leadOnce sync.Once
	// Makes texts set built once.
	builtOnce sync.Once
	// The error wraps a binding, whose location, source, and children this
	// one adopted, and whose position its message carries already.
	adopted bool
}

// boundChild is a child of a [SourceError]. A located child is a new
// binding of an [*Error] whose location its text lacks. An unbound child is
// a new binding of an error that did not hold a binding, so its text omits
// the problems a summary heads.
type boundChild struct {
	bound   *SourceError
	detail  bool
	located bool
	unbound bool
}

// DefaultContextLines is the number of context lines the %+v verb of an
// [*Error] or a [*SourceError] shows on either side of each marked line.
// A binding that [Detach] detached shows the context lines of the options
// Detach received instead.
const DefaultContextLines = 2

// DefaultExcerptWidth is the number of columns an error excerpt shows
// around each location it marks on a line longer than that.
// [WithExcerptWidth] sets another width for the excerpt, or for the call of
// [Detach] that detached its binding.
const DefaultExcerptWidth = 160

// ExcerptOption configures the excerpts of an error, which [FormatError],
// [NewErrorReport], and [SourceError.Excerpt] build and [Detach] keeps. An
// excerpt is a view of the lines of a source around the locations an error
// marks in it. The options say how many lines it keeps around each one and
// how much of a long line it shows. The renderer of an error chooses both.
// One program can thus render the same error with whole lines in a terminal
// and with bounded rows in a log. Detach chooses them in place of the
// renderer, as [Detach] describes.
//
// Available options:
//   - [WithContextLines]
//   - [WithExcerptWidth]
//
// [go.jacobcolvin.com/niceyaml/printer.Printer.PrintError] takes the same
// options for the excerpts it draws with styles, and
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
// long line an excerpt shows around each location it marks. The default is
// [DefaultExcerptWidth]. A line of cols columns or fewer prints whole. A
// longer line prints a window of cols columns around each mark, with "..."
// in place of the rest, as [line.View.Clip] cuts a view. A context line
// without a mark keeps its first cols columns. A cols of 0 shows every line
// whole, and a negative cols counts as 0:
//
//	fmt.Print(niceyaml.FormatError(err, niceyaml.WithExcerptWidth(0)))
//
// The width drops the columns outside each window, so it bounds what an
// excerpt adds to a log. To fit rows to a terminal, wrap them with
// [go.jacobcolvin.com/niceyaml/printer.WithWrapWidth] instead. The %+v verb
// of an [*Error] or a [*SourceError] does not take an option, so it renders
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
// It also caps the problems that the %+v verb and the LogValue method of an
// [*Error] or a [*SourceError] show, each with its details. A last row
// below them counts the rest. A logger calls those two for a program, so an
// error with thousands of problems reaches a log as a short text.
// [FormatError] and
// [go.jacobcolvin.com/niceyaml/printer.Printer.PrintError] show every
// problem.
const ErrorListLimit = 10

// boundTexts holds the texts that the methods of a [SourceError] return,
// built once.
// The message of a binding holds the messages of the bindings below it, so
// building them on every call rebuilds each message once per binding above
// it.
type boundTexts struct {
	// The binding whose position, path, and warning label the line of the
	// binding writes, as [SourceError.line] writes them. For an adopted
	// binding whose wrapper keeps the line of the binding it adopted, it is
	// the rows of that binding. It is nil for an adopted binding whose
	// wrapper rewrote that line, so its line keeps the text of the wrapper.
	// It is the binding itself for every other binding.
	rows *SourceError
	// The message that rows writes when wraps is set: the text of the
	// wrapper with the message of the binding it wraps in place of the line
	// of that binding.
	wrapped string
	// The text of the bound error without the part the children show, as
	// [chainEnd.cut] returns it, behind the path [SourceError.ownPath]
	// returns, if it returns one.
	own string
	// The line of the binding itself: own behind the position or the name. It
	// is empty for a binding that does not have text of its own, such as the
	// binding of a join.
	head string
	// The message [SourceError.Error] returns.
	msg string
	// The first lines of the list, [ErrorListLimit] at most.
	entries []string
	// The number of lines the whole list holds. It is zero when msg does not
	// list an error below the binding.
	count int
	// The number of children at the end of the list of children whose
	// text own holds already, so the list leaves out each of them that
	// the binding did not locate.
	shown int
	// The number of children at the end of the list whose text the message
	// of the bound error holds, as [chainEnd.cut] counts them, join branches
	// included.
	held int
	// Whether the line of the binding holds the line of a binding below it,
	// which wrapped replaces.
	wraps bool
}

// binder is where an error binds: a source, and a node that is nil for
// [Source.Bind]. A binder that routes picks a document per location. The
// binder of the parser must not route, because the parse builds the
// documents only when it ends.
type binder struct {
	src      *Source
	node     *Node
	route    bool
	locate   bool
	unplaced bool
	// Whether the error stands in the error that an [*Error] with a
	// location wraps, as the err of a [Locate] result does. A binding in
	// it that [Detach] detached from src then binds in src again, as
	// [reattached] binds it.
	rebinds bool
}

// located returns err rebased under [paths.Current] for a binder that
// locates. Each problem without a location then binds at the Node, and a
// scoped bind agrees with a [Rebase] under the path of the Node.
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

// scoped returns err under the scope of the node b binds with, or under
// [paths.Doc] when b does not hold a node. The `@` paths of err then start
// at `$`. The anchor is the one that [binder.yields] lets b find.
func (b binder) scoped(err error) scopedError {
	scope := paths.Doc()
	if b.node != nil {
		scope = b.node.base
	}

	scoped, _ := underScope(err, scope, b.yields)

	return scoped
}

// yields reports whether a located [*Error] above bound gives way to bound
// when b binds the Error. It does when the location of bound resolved in a
// source that b binds in, as [Source.holds] reports it. The location of the
// Error then names a place in that source too, and bound names the value
// at fault better. In another source, the location of the Error names a
// place that bound does not, so the Error keeps it.
func (b binder) yields(bound *SourceError) bool {
	return resolved(bound) && b.src.holds(bound.origin())
}

// holds reports whether src is s, a layer that s merges, or the source of
// the document that s stands for as a working copy. An error that a
// binding of s resolves lands in one of them. The text that a detached
// binding kept of one of them counts as that source, as [sameSource]
// compares them.
func (s *Source) holds(src excerptSource) bool {
	switch {
	case s == nil || src == nil:
		return false

	case sameSource(s, src):
		return true

	case s.relocation != nil && s.relocation.home.source.holds(src):
		return true

	case s.layers == nil:
		return false
	}

	return slices.ContainsFunc(s.layers.layers, func(l resolvedLayer) bool { return l.source.holds(src) })
}

// underScope returns err with scope in front of each `@` path it carries,
// and reports whether that changed err. It finds the anchor as it walks,
// with a located [*Error] that gives way to a binding below it where
// yields reports so, as [findAnchor] describes. A binding thus reads the
// chain of err once.
func underScope(err error, scope paths.Path, yields func(*SourceError) bool) (scopedError, bool) {
	if isNothing(err) {
		return scopedError{err: err}, false
	}

	a, _ := findAnchor(err, yields)
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
			inner, changed := underScope(x.err, scope, yields)
			if !changed {
				return unchanged, false
			}

			// The copy keeps what the Error declared.
			return anchored(&Error{err: inner.err, fault: x.fault}, yields), true
		}
	}

	changed := false

	joined := mapJoin(err, func(branch error) error {
		s, c := underScope(branch, scope, yields)
		changed = changed || c

		return s.err
	})
	if joined == nil || !changed {
		return unchanged, false
	}

	return anchored(joined, yields), true
}

// mapJoin applies f to each non-nil branch of the join err and rejoins the
// results that are not nil. It returns nil when err does not read as a join
// or when every result is nil.
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

// anchored returns err with its anchor, as [findAnchor] finds it with
// yields.
func anchored(err error, yields func(*SourceError) bool) scopedError {
	a, _ := findAnchor(err, yields)

	return scopedError{err: err, anchor: a}
}

// nodeAt returns the node that an error on line idx binds to. That node is
// the node of b or, when b routes, the root of the document whose span
// holds the line. The spans are in line order and contiguous, so a binary
// search finds it.
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

// BindValue binds err outside any document, for a check on a value that did
// not come from a document, such as the body of a request or a value the
// program built. [Error.Error] does not write a path. The error BindValue
// returns puts each path in its text, so it names every failing location
// through %v, inside a wrapper, in a join, and in a log. A summary of two
// violations reads:
//
//	2 violations
//	$.port: 0 is less than 1
//	$.name: name is required
//
// Each path starts at `$`, which here is the value the check ran on. A line
// does not start with a file or a position.
// [go.jacobcolvin.com/niceyaml/schema.Schema.ValidateValue] returns its
// violations this way, and [SelfValidateValue] binds what it finds the same
// way.
//
// The result does not stand in a document, so a document can still place
// it. [Rebase], every Bind, and a decode read the result as the error that
// BindValue bound, as the Errors section of the package documentation
// describes. Placing builds a new error and leaves the result as it is.
//
// The result is a [*SourceError] that is not bound to a [Node], and so is
// each binding below it. Its methods that report a document, a position, or
// a range report false, and [SourceError.Source] returns a source without a
// name or lines. [SourceError.Unresolved] returns a reason that wraps
// [go.jacobcolvin.com/niceyaml/paths.ErrNoDocument] for a path, and
// [ErrOutOfRange] for a position or a range.
//
// BindValue returns nil for a nil err, and for a nil [*Error] or
// [*SourceError] pointer. It returns an error that is bound already as it
// is, whether a document bound it or BindValue did.
func BindValue(err error) error {
	return bindTree(err, binder{src: valueSource(), route: true, unplaced: true})
}

// bindTree binds err to b. It returns an untyped nil for an err that
// [isNothing] reports, and err as it is when [isBound] reports it. Only an
// unplaced binder counts a [BindValue] binding as bound, and any other
// binder places it.
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

// placeable returns err with a [BindValue] binding replaced by the error
// that BindValue bound, so a document can place it. Each wrapper above it
// becomes a stand-in, as [replaceBelow] and [mapWrapper] build them.
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
		if below == nil {
			// The wrapper heads several problems, so each of them gets its
			// binding replaced.
			return mapWrapper(err, branches, replaceUnplaced)
		}

		return replaceBelow(err, below, branches)

	default:
		return err, false
	}
}

// holdsUnplaced reports, without formatting any message, whether
// [replaceUnplaced] can find a binding below err that does not stand in a
// document.
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

// replaceBelow is [replaceUnplaced] for wrapper, whose chain continues
// through below. Branches holds every error wrapper unwraps to, when it
// unwraps to several. The stand-in keeps the leaves among them so that it
// matches as wrapper does.
func replaceBelow(wrapper, below error, branches []error) (error, bool) {
	if below == nil {
		return wrapper, false
	}

	placed, ok := replaceUnplaced(below)
	if !ok {
		return wrapper, false
	}

	// The placed error names no path, so its text takes the place of the
	// text of the binding.
	msg := replaceText(wrapper.Error(), below.Error(), placed.Error())

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

// mapWrapper applies f to each problem among branches, the errors that
// wrapper unwraps to. Wrapper is a wrapper that [fmt.Errorf] builds with
// several %w verbs, and [isLeaf] picks its sentinels, which f never sees.
// When f reports a change, mapWrapper returns true and a stand-in for
// wrapper over the results and the sentinels. The message of the stand-in
// has the text of each changed branch replaced. Otherwise mapWrapper
// returns wrapper and false.
func mapWrapper(wrapper error, branches []error, f func(error) (error, bool)) (error, bool) {
	msg := wrapper.Error()
	mapped := slices.Clone(branches)
	changed := false

	for i, branch := range branches {
		if isNothing(branch) || isLeaf(branch) {
			continue
		}

		m, ok := f(branch)
		if !ok {
			continue
		}

		msg = replaceText(msg, branch.Error(), m.Error())
		mapped[i], changed = m, true
	}

	if !changed {
		return wrapper, false
	}

	return multierr.Rewrap(wrapper, mapped, msg), true
}

// replaceText returns msg with old, the text of an error that msg holds,
// replaced by with. A message that holds old exactly once shows where that
// text stands, so replaceText returns any other message as it is.
func replaceText(msg, old, with string) string {
	if old == "" || strings.Count(msg, old) != 1 {
		return msg
	}

	return strings.Replace(msg, old, with, 1)
}

// isBound reports whether err is a [*SourceError] or wraps one without an
// [*Error] above it that adds a location, problems, or details. Such an
// Error binds anew around the binding.
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

// leadingBinding returns the binding that wrote the first line of the
// message of err, or nil. It follows the text each error writes, not the
// structure [isBound] reads, because an [*Error] leaves the errors below it
// out of its text.
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

// passedLead returns the binding that wrote the first line of the message
// of e, for an e that does not write a position or a path. It finds the
// lead a single time, because a search per message walks each binding once
// per binding above it.
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
		if lead != nil && (e.Name() == "" || sameSource(lead.origin(), e.origin()) || !holdsSource(e, e.origin())) {
			e.lead = lead
		}
	})

	return e.lead
}

// ownPath returns the path that e puts in front of the text it binds, from
// the anchor of the bound error. It returns false when e does not put a
// path there, as for an adopted binding whose text names its own path
// already.
func (e *SourceError) ownPath() (paths.Path, bool) {
	a := e.anchor()

	return a.path, a.hasPath
}

// keepsMessage reports whether [SourceError.Error] returns the message of e
// without a position, a name, or a document in front. Error does so when e
// did not resolve a location and either a lead names the source already or
// e does not have a place.
func (e *SourceError) keepsMessage() bool {
	if e.locErr == nil && !e.adopted {
		return false
	}

	return e.passedLead() != nil || e.place() == ""
}

// place returns what stands in front of the message of a binding that does
// not write a position, as "cafe.yaml: document 3:". It leaves out either
// part when e does not have it.
func (e *SourceError) place() string {
	name, doc := e.label(), e.documentLabel()

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

// documentLabel returns "document 3" for an e without a position that is
// bound in the third document of a file of several. The message of such an
// e does not have a line number that names the document. The number counts
// every document of the file, as [Source.AllDocuments] does. A detached
// binding reads the label it kept.
func (e *SourceError) documentLabel() string {
	switch {
	case e.locErr == nil:
		return ""
	case e.keptDoc != nil:
		return e.keptDoc.label
	case e.node == nil:
		return ""
	default:
		return documentName(e.node)
	}
}

// documentName returns "document 3" for node in the third document of a
// file of several, or "" for node in a file of one document.
func documentName(node *Node) string {
	if len(node.source.documents()) < 2 {
		return ""
	}

	return "document " + oneBased(node.doc.index)
}

// holdsSource reports whether a child of e is bound to src. It looks
// through a child that binds a join to its branches, because such a child
// does not have text of its own.
func holdsSource(e *SourceError, src excerptSource) bool {
	for _, c := range e.below {
		joined := joinBinding(c.bound)
		if joined != nil {
			if holdsSource(joined, src) {
				return true
			}

			continue
		}

		if c.bound.origin() == src {
			return true
		}
	}

	return false
}

// anchor is the error along a cause chain that carries the location, and
// that location with every base above it joined in. A [*SourceError]
// resolved its location already, so its anchor holds the zero locus.
type anchor struct {
	err error
	locus
	// Whether err is a binding that a located [*Error] above it gave way
	// to, as [findAnchor] lets an Error give way.
	gaveWay bool
}

// anchorOf returns the first located [*Error] or [*SourceError] along the
// cause chain of err. A located Error above a binding whose location
// resolved gives way to that binding, because no source binds err yet. A
// [Rebase] without a located error below it anchors at its base, unless
// the chain below heads several problems.
func anchorOf(err error) anchor {
	a, _ := findAnchor(err, resolved)

	return a
}

// resolved reports whether the location of e resolved.
func resolved(e *SourceError) bool {
	return e.locErr == nil
}

// anchor returns the anchor of the error that e binds. A located [*Error]
// along it gives way to the binding below it only when e adopted that
// binding, as [newSourceError] decided when it bound e.
func (e *SourceError) anchor() anchor {
	var yields func(*SourceError) bool

	if e.adopted {
		yields = resolved
	}

	a, _ := findAnchor(e.err, yields)

	return a
}

// findAnchor returns the anchor of err and, when it does not find one,
// whether the chain heads separate problems. One walk answers both, so a
// chain of [Rebase] Errors reads the chain below each once. A located
// [*Error] above a binding gives way to that binding when yields reports
// true for it, and a nil yields never gives way.
func findAnchor(err error, yields func(*SourceError) bool) (anchor, bool) {
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

		if x.hasLocation() && yields != nil {
			// A binding below that resolved its location in the source that
			// x binds in names the value at fault already, so the location
			// of x does not move it.
			below, _ := findAnchor(x.err, yields)

			bound, ok := below.err.(*SourceError) //nolint:errorlint // The anchor itself, found by the walk.
			if ok && yields(bound) {
				below.gaveWay = true

				return below, false
			}
		}

		if x.hasLocation() {
			l := x.locus()
			if x.move != nil {
				l = x.move.apply(l)
			}

			return anchor{err: x, locus: l}, false
		}

		a, heading := findAnchor(x.err, yields)
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
		return findAnchor(x.Unwrap(), yields)

	case interface{ Unwrap() []error }:
		branches, next := followBranches(err, x.Unwrap())
		if next == nil {
			return anchor{}, len(branches) > 0
		}

		return findAnchor(next, yields)

	default:
		return anchor{}, false
	}
}

// boundLocus returns the location e resolved its position from, with every
// base inside e joined in and without a base from above. A binding that
// wraps another reports the one it wraps.
func boundLocus(e *SourceError) locus {
	found := e.anchor()

	if inner, ok := found.err.(*SourceError); ok { //nolint:errorlint // The anchor itself, found by the walk.
		return boundLocus(inner)
	}

	return found.locus
}

// newSourceError binds err to b and resolves its location. The binding
// holds err scoped, so its paths start at `$`. It takes the source of the
// node it resolved in, which is a layer in a merged document.
func newSourceError(err error, b binder) *SourceError {
	scoped := b.scoped(err)
	found := scoped.anchor

	e := &SourceError{err: scoped.err, source: b.src, node: b.node, locErr: errUnlocated}

	var (
		reloc     *relocation
		relocated []error
	)

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

		// No error binds in a working copy, which is no file of the caller.
		if reloc = e.source.relocation; reloc != nil {
			relocated = e.relocate(reloc, found)
		}

	case *SourceError:
		// The error wraps a binding, so it takes over what the binding
		// resolved, and its message holds the binding's position.
		e.adopted = true
		e.source = a.source
		e.node = a.node
		e.kept, e.keptDoc = a.kept, a.keptDoc
		e.loc, e.locErr = a.loc, a.locErr
		e.rng, e.ranges = a.rng, a.ranges

		// The binding of a located Error is a binding of the source that
		// binds it, as when the Error does not give way. So is a binding
		// in the error that a located Error wraps.
		if found.gaveWay || b.rebinds {
			e.reattach(b)
		}
	}

	if !e.adopted {
		// An error with no location binds in the document that a working
		// copy stands for.
		if r := e.source.relocation; r != nil {
			e.source, e.node = r.home.source, r.home
		}

		// No error binds in the text [mergeLayers] merged, which is no file of
		// the caller. One that found no layer binds in the layer the merged
		// Source stands for.
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

	if len(relocated) > 0 {
		e.addRelocatedDetails(reloc, relocated)
	}

	return e
}

// reattach binds e in the [Source] that the binding e adopted came from,
// for a binding that [Detach] detached. It does so when b binds in that
// Source, as [reattachable] reports it. The Source has the lines that the
// detached binding resolved its location in, so e keeps that location. It
// binds to the Node that [keptNode] finds in place of the Node of that
// binding, which is gone. [SourceError.collect] binds the bindings below
// it in the Source again too.
func (e *SourceError) reattach(b binder) {
	src := reattachable(e.kept, b)
	if src == nil {
		return
	}

	e.source, e.node = src, keptNode(src, e.keptDoc)
	e.kept, e.keptDoc = nil, nil
}

// reattachable returns the [Source] that k, what a detached binding kept,
// came from, when b binds in that Source as [Source.holds] reports it. It
// returns nil for a nil k, for a Source that the program dropped, and for
// any other Source.
func reattachable(k *keptSource, b binder) *Source {
	if k == nil {
		return nil
	}

	src := k.from.Value()
	if src == nil || !b.src.holds(src) {
		return nil
	}

	return src
}

// keptNode returns the [*Node] of src that doc names, for a binding that
// [Detach] detached from src. The Node sits at the path of the Node that
// the binding was bound to, in the document at the index that doc kept.
// When the path does not resolve there, the result is the root of that
// document. A binding outside every document keeps a nil doc, and the
// result for it is nil.
func keptNode(src *Source, doc *keptDocument) *Node {
	if doc == nil {
		return nil
	}

	docs := src.documents()
	if doc.index >= len(docs) {
		return nil
	}

	root := docs[doc.index]
	if doc.path.IsRoot() {
		return root
	}

	node, err := root.At(doc.path)
	if err != nil {
		return root
	}

	return node
}

// collect binds the children [walkChildren] finds below err and keeps the
// end of its chain. A [*SourceError] that ends the chain is a cause, not a
// problem, so its children join those of e, as [SourceError.adoptChildren]
// returns them.
func (e *SourceError) collect(err error, b binder) {
	e.end = walkChildren(err,
		func(x *SourceError) { e.below = append(e.below, e.adoptChildren(x, b)...) },
		func(n error, base childBase, detail bool) { e.addChild(n, b, base, detail) },
	)
}

// adoptChildren returns the children of x, the binding that ends the cause
// chain of e. When e binds in a Source and x is a binding that [Detach]
// detached from the Source that b binds in, x reads as a binding of that
// Source. The bindings below x that the same call of Detach detached from
// that Source then come back bound in it again, as a [reattacher] binds
// them. Every other child comes back as it is.
func (e *SourceError) adoptChildren(x *SourceError, b binder) []boundChild {
	if e.kept != nil {
		return x.below
	}

	src := reattachable(x.kept, b)
	if src == nil {
		return x.below
	}

	r := newReattacher(x.kept, src)
	below, _ := r.children(x.below)

	return below
}

// reattached returns x bound in the [Source] of b again when x is a
// binding that [Detach] detached from that Source, as [reattachable]
// reports it. The bindings below x that the same call of Detach detached
// from that Source come back bound in it too, as a [reattacher] binds
// them. Any other x comes back as it is.
func reattached(x *SourceError, b binder) *SourceError {
	src := reattachable(x.kept, b)
	if src == nil {
		return x
	}

	r := newReattacher(x.kept, src)
	c, _ := r.binding(x)

	return c
}

// reattacher binds bindings that one call of [Detach] detached from src
// in src again. The keptSource from stands for what that call kept of src.
// The reattacher copies each binding that it binds again and each binding
// with one of those below it. The detached error that holds them thus
// stays as it is. Every other binding stays as it is.
type reattacher struct {
	from *keptSource
	src  *Source
	// The copy of each binding that the walk met, or the binding itself when
	// it does not need a copy.
	done map[*SourceError]*SourceError
}

// newReattacher creates a new [reattacher] that binds in src the bindings
// that the call of [Detach] that kept from detached.
func newReattacher(from *keptSource, src *Source) *reattacher {
	return &reattacher{from: from, src: src, done: make(map[*SourceError]*SourceError)}
}

// binding returns the copy of x with x and each binding below it bound in
// src again, as [reattacher] describes, and whether x needed one. The copy
// keeps the location x resolved and the errors x wraps.
func (r *reattacher) binding(x *SourceError) (*SourceError, bool) {
	if c, ok := r.done[x]; ok {
		return c, c != x
	}

	below, heads := r.children(x.below)

	if x.kept != r.from && !heads {
		r.done[x] = x

		return x, false
	}

	c := &SourceError{
		err:     x.err,
		free:    x.free,
		locErr:  x.locErr,
		source:  x.source,
		node:    x.node,
		lead:    x.passedLead(),
		kept:    x.kept,
		keptDoc: x.keptDoc,
		loc:     x.loc,
		end:     x.end,
		ranges:  x.ranges,
		below:   below,
		rng:     x.rng,
		adopted: x.adopted,
	}

	// The lead of x stands for the lead of the copy, so the copy finds none
	// of its own.
	c.leadOnce.Do(func() {})

	if x.kept == r.from {
		c.source, c.node = r.src, keptNode(r.src, x.keptDoc)
		c.kept, c.keptDoc = nil, nil
	}

	r.done[x] = c

	return c, true
}

// children returns below, the children of a binding, with each bound
// child walked as [reattacher.binding] walks it, and whether the walk
// changed any of them.
func (r *reattacher) children(below []boundChild) ([]boundChild, bool) {
	var out []boundChild

	for i, child := range below {
		bound, changed := r.binding(child.bound)
		if changed && out == nil {
			out = slices.Clone(below)
		}

		if out != nil {
			out[i].bound = bound
		}
	}

	if out == nil {
		return below, false
	}

	return out, true
}

// addChild binds n as a detail or a problem of e. A child under a [Rebase]
// binds rebased at base, so its message and path carry the joined path as
// the message of the root does.
func (e *SourceError) addChild(n error, b binder, base childBase, detail bool) {
	// The base returns a binding as it is unless it marks the binding
	// invalid, so the child is a binding when n is one that gains no mark.
	// A binding that stands in no document binds here as its error does.
	child := base.rebase(placeable(n), detail)
	if isNothing(child) {
		return
	}

	// A child in the error that a located Error wraps stands in that
	// error, and so does each error below it. A binding there that Detach
	// detached from the source of b binds in that source again.
	b.rebinds = b.rebinds || base.inLocated

	bound, ok := child.(*SourceError) //nolint:errorlint // The node itself, not a chain search.

	switch {
	case ok && b.rebinds:
		bound = reattached(bound, b)

	case !ok:
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

// heads reports whether e heads separate problems: whether a child of e is
// not a detail.
func (e *SourceError) heads() bool {
	return slices.ContainsFunc(e.below, func(c boundChild) bool { return !c.detail })
}

// headsAlone reports whether e heads separate problems and does not have a
// line of its own, such as the binding of a join. Each problem below such a
// binding that does not carry a location takes the location of the binding.
// The binding itself thus does not mark anything in an excerpt, even with a
// location.
func (e *SourceError) headsAlone() bool {
	return e.heads() && e.headline() == ""
}

// Source returns the [*Source] the error is bound to. It returns nil for a
// binding that [Detach] detached, which does not keep a Source, and for a
// nil SourceError. [SourceError.Name] and [SourceError.FilePath] report the
// name and the file path of the Source for a live binding and a detached
// one alike.
func (e *SourceError) Source() *Source {
	if e == nil {
		return nil
	}

	return e.source
}

// Name returns the name of the [Source] the error was bound to, as
// [Source.Name] returns it. A binding that [Detach] detached returns the
// name its Source had. A nil SourceError returns an empty name.
func (e *SourceError) Name() string {
	if e == nil {
		return ""
	}

	return e.origin().Name()
}

// FilePath returns the file path of the [Source] the error was bound to, as
// [Source.FilePath] returns it. A binding that [Detach] detached returns
// the file path its Source had. A nil SourceError returns an empty file
// path.
func (e *SourceError) FilePath() string {
	if e == nil {
		return ""
	}

	return e.origin().FilePath()
}

// label returns the name of the source of e as messages show it. The
// label draws control characters as [escape.Control] draws them and tabs
// as spaces, so a file name that holds a line feed stays on one line.
func (e *SourceError) label() string {
	return escape.Line(e.Name())
}

// origin returns the text the excerpts of e show: its Source, or what a
// detached binding kept of one. Bindings with the same origin share an
// excerpt, and a child with the origin of its parent drops the name the row
// of the parent shows. It returns nil for a nil e.
func (e *SourceError) origin() excerptSource {
	switch {
	case e == nil:
		return nil
	case e.kept != nil:
		return e.kept
	default:
		return e.source
	}
}

// sameDocument reports whether a and b, two bindings of one source, are
// bound to one document, or whether neither is bound to a document. Each
// compares by the index of its document, as [SourceError.DocumentIndex]
// reports it. A binding that [Detach] detached thus shares a document with
// a live binding of the source it came from.
func sameDocument(a, b *SourceError) bool {
	ia, inA := a.DocumentIndex()
	ib, inB := b.DocumentIndex()

	return inA == inB && ia == ib
}

// Node returns the [*Node] the error is bound to and true, or nil and false
// for an error that is not bound to a Node. The Node is the one whose
// methods and validators produced the error or whose [Node.Bind] bound it,
// and an `@` path in the error resolves from it. [Source.Bind] binds to the
// root of the document the location falls in, and the error stays bound to
// that root when its location does not resolve there. A position or a range
// falls in the document whose [Node.Span] holds its line, whether or not
// that document parsed. A path falls in the one document of the source.
//
// Source.Bind does not bind an error to a Node when the error lacks a
// location, or when its position or range lies on a line outside every
// document. It does not bind an error with a path to a Node either when the
// source holds several documents or has a YAML syntax error.
//
// The parse binds a YAML syntax error to the root of the document whose
// span holds its location. A syntax error that does not have a location in
// the source binds to the first document it fails. An error that wraps a
// binding keeps the Node of that binding. An error bound in a merged
// [Source] is bound to the Node of the layer its location resolved in. When
// it does not carry a location, it is bound to the Node of the layer
// [MergeAs] names, or else to the Node of the lowest layer.
//
// The error [BindValue] returns and each binding below it are not bound to
// a Node. A binding that [Detach] detached and a nil SourceError are not
// bound to a Node either.
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
// document the error is bound to and true, or 0 and false for a binding
// that is not in a document. It is the [Node.DocumentIndex] of the root
// that [SourceError.Document] returns. A binding that [Detach] detached
// reports the index of the document it was bound to, although
// SourceError.Document reports false for it. The message of a bound error
// counts documents from 1, as it counts lines, so "document 3" in
// [SourceError.Error] is the document at index 2. A report that groups the
// problems of a file by document reads the index of each:
//
//	for problem := range niceyaml.Problems(err) {
//		if index, ok := problem.DocumentIndex(); ok {
//			byDocument[index] = append(byDocument[index], problem.Message())
//		}
//	}
func (e *SourceError) DocumentIndex() (int, bool) {
	if e != nil && e.keptDoc != nil {
		return e.keptDoc.index, true
	}

	doc, ok := e.Document()
	if !ok {
		return 0, false
	}

	return doc.DocumentIndex(), true
}

// Message returns the text of the bound error without its own position,
// document, or path in front. It is the message [NewError] or [Invalid]
// gave an [*Error], or the text of any other error as it is, with any text
// a wrapper such as [fmt.Errorf] added around it. [SourceError.Excerpt]
// annotates a location with it, and a structured report carries it beside
// [SourceError.Position] and [SourceError.Path], as [Problems] describes.
// The errors [SourceError.Error] lists below the message are not part of
// it, so the message of a summary is its heading alone. A binding of a
// join does not have a line of its own. Its message holds the message of
// each problem of the join, one per line, in the order of the join. A
// heading inside the join, such as a summary, adds its own message above
// the messages of its problems. A nil SourceError has an empty message.
//
// A binding that wraps another binding leaves out the location of the
// inner one, unless the inner one takes the location of a [Locate],
// [Invalid], or [Unchecked] result. So the binding of a wrapper from
// [fmt.Errorf] that adds "decode price:" to an error of [Node.DecodeAt]
// has the message "decode price: not found". A wrapper whose text does not
// hold the line of the inner binding, such as one that rewrites it, has
// the message of the inner binding. A wrapper around the binding of a join
// keeps its own text, because the join does not have a line.
//
// [Locate] describes when its result gives its location to the binding
// inside it. The binding of a Locate, [Invalid], or [Unchecked] result
// that does so keeps the path of the inner binding in its message, as in
// "$.license.owner: no owner". The message leaves that path out when the
// row of the result shows it and the message does not name a document. It
// names the document of the inner binding when the result is not bound to
// that same document, as in "document 2: no owner". For an inner binding
// of another source, the message holds the line of that binding without
// the "warning:" label, as in "inc.yaml:1:6: $.key: bad key".
func (e *SourceError) Message() string {
	return e.text()
}

// Path returns the [paths.Path] the bound error is about and true, or the
// zero Path and false when it does not carry one. It is the path that
// [Error.Path] reports for the [*Error] that gave the binding its location.
// An `@` path gets the [Node.Path] of the Node that bound it in front, so
// the result starts at `$`. An error written as `@.price` and bound through
// the Node at `$.items[1]` reports `$.items[1].price`.
//
// A rejection of the go-yaml decoder reports the path where the document
// writes the value. That path might lie outside the scope of the Node, as
// the Decoding and Scoped Nodes sections of the package documentation
// describe. An error that binds at a range or a position beside its path
// reports that path, resolved or not. A binding that wraps another reports
// the path of the one it wraps, and a nil SourceError does not have a path.
func (e *SourceError) Path() (paths.Path, bool) {
	if e == nil {
		return paths.Path{}, false
	}

	l := boundLocus(e)

	return l.path, l.hasPath
}

// Unwrap returns the error the [SourceError] binds. A nil SourceError
// unwraps to nil, so a caller can safely walk a chain that holds one.
func (e *SourceError) Unwrap() error {
	if e == nil {
		return nil
	}

	return e.err
}

// Error returns the message of the bound error with its resolved position
// in front. The message reads "name:line:col: $.path: msg" when the error
// carries a path, and "name:line:col: msg" when it does not. The name is
// [Source.Name], and the message of a source without a name starts with
// "line:col:". An error without a position reads "name: msg". In a source
// that holds several documents, such an error names its document behind the
// name, counted from 1 among [Source.AllDocuments], as "name: document 3:
// msg".
//
// The text a wrapper added to the bound error sits behind the path. A
// binding that wraps another binding reads the same way. Its line takes the
// position and the path of the inner binding, and the text of the wrapper
// follows them. The line thus reads "cfg.yaml:4:3: $.price: decode price:
// not found" for a wrapper that adds "decode price:". A first line that a
// wrapper rewrote around a binding of the same source does not gain a
// second position.
//
// The message holds one line per problem and leaves out the details. A
// heading is a summary, a join, or a wrapper around either. It lists the
// problems it heads below its own line in the order of their positions, at
// most [ErrorListLimit] of them, with a last line that counts the rest:
//
//	cafe.yaml: 2 schema violations
//	cafe.yaml:6:8: $.spec.sla: string does not match pattern
//	cafe.yaml:22:11: $.spec.hours.days: expected "array", got "string"
//
// Each problem of a join that does not carry a location takes the location
// that [Invalid], [Unchecked], or [Locate] gives the join. The join thus
// does not have a line of its own, even with a location. Any other error
// that unwraps to several errors keeps its own message. Each branch follows
// as a line unless that message holds its text already, as a message that
// lists the texts of its branches does. A branch located from an [*Error]
// follows either way, because its line names a position the message lacks.
//
// The name, each path, and the text of a decode rejection reach the message
// with their control characters drawn or escaped. A message the caller
// wrote stays as it is. The message does not hold source lines, so it stays
// short in a log. Error builds the message on its first call and returns
// the same message on every later call. A nil SourceError has an empty
// message.
func (e *SourceError) Error() string {
	return e.texts().msg
}

// headline returns the line of e itself, without the errors below it: the
// text [NewErrorTree] gives the node of e. It is empty for a binding that
// does not have text of its own.
func (e *SourceError) headline() string {
	return e.texts().head
}

// texts returns the texts of e, which it builds the first time a caller
// asks for them. It returns empty texts for a nil e.
func (e *SourceError) texts() boundTexts {
	if e == nil {
		return boundTexts{}
	}

	e.builtOnce.Do(func() { e.built = e.buildTexts() })

	return e.built
}

// buildTexts builds the texts of e, with the problems it heads listed under
// its own line. Its line drops the text of the errors that the chain end
// shows, unless a wrapper rewrote that text. A rewritten text stays as the
// wrapper wrote it.
func (e *SourceError) buildTexts() boundTexts {
	var (
		t    boundTexts
		full string
		cut  = true
	)

	// The scoped error, not the chain end, says whether e binds a join. A
	// join reads its message only when it lists nothing, since its list
	// stands in for it and reading it would build every join below again.
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

	// An adopted binding writes its line as the binding it adopted writes
	// one, with the text of the wrapper in its message, when the wrapper
	// keeps that line. Any other adopted binding keeps the text of its
	// wrapper as it is.
	t.rows = e

	switch {
	case e.adopted:
		t.rows = nil

		if cut && !joined {
			inner := e.inner()
			t.rows, t.wrapped = wrapLine(t.own, inner, inner.text())
			t.wraps = t.rows != nil
		}

	case cut && !joined:
		// A located Error above a binding that it does not give way to
		// gives that binding its location. When both name one source, the
		// line of e then holds the line of the binding without the name of
		// that source. It also leaves out what the row of e shows already.
		// The line of a binding of another source keeps its name and its
		// position, and leaves out the "warning:" label, which the row of
		// e shows.
		inner, ok := e.end.err.(*SourceError) //nolint:errorlint // The end of the chain, found by the walk.

		var (
			shown    string
			rewrites bool
		)

		switch {
		case !ok:
		case sameSource(inner.origin(), e.origin()):
			shown, rewrites = inner.unnamedLine(e), true
		case inner.labels():
			shown, rewrites = inner.unlabeledLine(), true
		}

		if rewrites {
			rows, msg := wrapLine(t.own, inner, shown)
			t.wrapped, t.wraps = msg, rows != nil
		}
	}

	// No error below e reaches the list, so the text stands as it is.
	if t.count == 0 {
		if joined {
			full = e.err.Error()
		}

		if t.own == "" {
			t.own = full
		}

		if t.wraps {
			t.own, t.head = t.rows.lineAs(t.wrapped, e.isWarning())
			t.msg = t.head

			return t
		}

		pathed := e.pather(e.warns())
		t.own = pathed(t.own)
		t.head = e.prefixed(t.own)
		t.msg = e.prefixed(pathed(full))

		return t
	}

	lines := make([]string, 0, len(t.entries)+2)

	// A heading with no text of its own adds no line, even with a
	// location. Each problem below it that carries no location takes that
	// location and names it.
	switch {
	case t.wraps:
		t.own, t.head = t.rows.lineAs(t.wrapped, e.isWarning())
		lines = append(lines, t.head)

	case t.own != "":
		t.own, t.head = e.line(t.own)
		lines = append(lines, t.head)
	}

	lines = append(lines, t.entries...)

	if more := t.count - len(t.entries); more > 0 {
		lines = append(lines, e.more(more))
	}

	t.msg = strings.Join(lines, "\n")

	return t
}

// line returns msg as the line of e above the problems it lists, with the
// "warning:" label when [SourceError.warns] reports it. Each problem below
// shows its own line, so msg holds the line of the lead of e only when a
// wrapper rewrote the text of the problems. When e has a lead and msg does
// not hold its line, the line names the place of e, as the line of each
// problem names its own. Otherwise the line reads as [SourceError.lineAs]
// writes it.
func (e *SourceError) line(msg string) (string, string) {
	own := e.pather(e.warns())(msg)

	lead := e.passedLead()
	if lead != nil && !holdsLine(msg, lead) {
		return own, prefix(e.place(), own)
	}

	return own, e.prefixed(own)
}

// holdsLine reports whether text holds the line of x. A binding that does
// not have a line of its own, such as that of a join, is in no text.
func holdsLine(text string, x *SourceError) bool {
	head := x.headline()

	return head != "" && strings.Contains(text, head)
}

// lineAs returns msg as the line of e, with the "warning:" label when warns
// is true. The first text is msg behind the path and the label that
// [SourceError.pather] puts in front, and the second is that text behind
// the position or the place of e as well. A binding that writes its line
// as the binding it adopted writes one passes its own severity, because
// the line holds its own message.
func (e *SourceError) lineAs(msg string, warns bool) (string, string) {
	own := e.pather(warns)(msg)

	return own, e.prefixed(own)
}

// pather returns a function that puts the path that [SourceError.ownPath]
// returns in front of a message, if it returns one, and the "warning:"
// label in front of that when warns is true. The label sits between the
// position and the path, where compilers put it, so editors and problem
// matchers read it. The path takes a walk of the chain, so pather finds it
// one time for every message.
func (e *SourceError) pather(warns bool) func(string) string {
	path, hasPath := e.ownPath()

	return func(msg string) string {
		if hasPath {
			msg = withPath(path, msg)
		}

		if warns {
			msg = prefix("warning:", msg)
		}

		return msg
	}
}

// inner returns the binding that e adopted, or nil for a binding that did
// not adopt one.
func (e *SourceError) inner() *SourceError {
	if !e.adopted {
		return nil
	}

	inner, ok := e.anchor().err.(*SourceError) //nolint:errorlint // The anchor itself, found by the walk.
	if !ok {
		return nil
	}

	return inner
}

// innermost follows the bindings that e adopted, each inside the last,
// and returns the first one that did not adopt a binding. It returns e
// when e did not adopt one. The message of the result leaves out the text
// of every wrapper on the way. A wrapped copy of a binding thus has the
// message of that binding, however many times a source bound the wrapper.
func (e *SourceError) innermost() *SourceError {
	for inner := e.inner(); inner != nil; inner = e.inner() {
		e = inner
	}

	return e
}

// wrapLine returns the rows and the message of a wrapper around inner, as
// [boundTexts] holds them, when own holds the line of inner once. Own is
// the text of the wrapper without the part that the children show. The
// message is own with msg in place of that line. So the text "decode price:
// cfg.yaml:4:3: $.price: not found" with the message of inner as msg has
// the message "decode price: not found". The function returns nil for a
// wrapper that rewrote the line of inner. It returns nil for an inner that
// does not have a line of its own too, such as the binding of a join.
func wrapLine(own string, inner *SourceError, msg string) (*SourceError, string) {
	if inner == nil {
		return nil, ""
	}

	t := inner.texts()
	if t.rows == nil || t.head == "" || strings.Count(own, t.head) != 1 {
		return nil, ""
	}

	before, after, _ := strings.Cut(own, t.head)

	return t.rows, strings.TrimRightFunc(before+msg+after, unicode.IsSpace)
}

// unnamedLine returns the line of e without the name of its source and
// without the "warning:" label. Outer is a binding of the same source that
// gives e its location, and it writes this line behind that location, as
// [Locate] describes. The line leaves out the document of e when outer is
// bound to that same document, because the location of outer names it
// already. The line leaves out the path of e when the row of outer shows
// that path and the line does not name a document. A document label sets
// the two paths apart, so the line keeps the path behind it.
func (e *SourceError) unnamedLine(outer *SourceError) string {
	rows, msg := e, e.text()
	if t := e.texts(); t.wraps {
		rows = t.rows
	}

	var document string

	if !sameDocument(e, outer) {
		document = e.documentLabel()
	}

	path, hasPath := rows.ownPath()
	if hasPath {
		shown, shows := outer.ownPath()
		if !shows || !shown.Equal(path) || document != "" {
			msg = withPath(path, msg)
		}
	}

	return prefix(suffixed(document), msg)
}

// unlabeledLine returns the line of e without the "warning:" label. The
// binding of a [Locate] result in another source writes this line behind
// the location that it gives e, because the row of that binding shows the
// severity.
func (e *SourceError) unlabeledLine() string {
	rows, msg := e, e.text()
	if t := e.texts(); t.wraps {
		rows = t.rows
	}

	_, row := rows.lineAs(msg, false)

	return row
}

// labels reports whether the line of e carries the "warning:" label. A
// line that holds the message of a binding below e in place of its line
// takes the label as [SourceError.isWarning] reports it. Any other line
// takes it as [SourceError.warns] reports it. A binding that adopted a
// warning thus labels its line too, although warns reports false for it.
func (e *SourceError) labels() bool {
	if e.texts().wraps {
		return e.isWarning()
	}

	return e.warns()
}

// warns reports whether e binds one problem that is a warning, so a line
// that keeps the text of its wrapper carries the "warning:" label. A
// heading does not take the label, and neither does a binding that adopted
// a warning and keeps the text of its wrapper, which holds the label
// already. A line that holds the message of a binding below e in place of
// its line takes the label of e instead, as [SourceError.isWarning]
// reports it.
func (e *SourceError) warns() bool {
	if e.heads() || !isWarning(e.err) {
		return false
	}

	inner := e.inner()

	return inner == nil || !inner.warns()
}

// bare reports whether e has neither a message nor a position, so it does
// not add a line to the list of the binding above it. The line of an
// adopted binding that keeps the text of its wrapper holds the position
// the wrapper wrote, if any.
func (e *SourceError) bare() bool {
	t := e.texts()

	return t.count == 0 && t.own == "" && (e.locErr != nil || (e.adopted && t.rows == nil))
}

// more returns the line that ends a list that leaves n errors out. It names
// the source of e, as every other line of the list names its own.
func (e *SourceError) more(n int) string {
	rest := "and " + strconv.Itoa(n) + " more"

	name := e.label()
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

// prefixed returns msg behind the position, or the place, of e, by the
// rules [SourceError.Error] documents. The caller puts the path in front of
// msg first, so the path stands between the position and the text.
func (e *SourceError) prefixed(msg string) string {
	switch {
	case e.locErr == nil && !e.adopted:
		return prefix(formatPosition(e.label(), e.loc.pos), msg)

	case e.keepsMessage():
		return msg

	default:
		return prefix(e.place(), msg)
	}
}

// list returns the lines [SourceError.Error] lists below the line of e,
// [ErrorListLimit] at most, and the count of the whole list. It reads as
// the rows [ErrorTree.Problems] yields, with each heading above its own.
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

// ordered returns the problems e heads in [NewErrorTree] order, minus the
// last shown ones e did not locate, because the text of e holds them. An
// unbound child among those gives the problems below it in its place.
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

// formatPosition returns pos as "name:line:col:", the format editors and
// build tools read, or "line:col:" when name is empty.
func formatPosition(name string, pos position.Position) string {
	if name == "" {
		return editorPosition(pos) + ":"
	}

	return name + ":" + editorPosition(pos) + ":"
}

// editorPosition returns pos as "line:col" counted from 1, as editors and
// build tools count. [position.Position.String] counts from 0, as the
// fields do.
func editorPosition(pos position.Position) string {
	return oneBased(pos.Line) + ":" + oneBased(pos.Col)
}

// oneBased returns the decimal text of n + 1. It prints [math.MaxInt] + 1
// through a uint64, because the sum wraps to a negative int.
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
// pointer, which does not carry a message or a location.
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

// text returns the text that the excerpt annotates e with, which
// [SourceError.Message] returns. The caret marks the location, so the text
// leaves out the position and the path. An adopted binding that keeps the
// text of its wrapper as it is has the message of the binding it adopted,
// unless that binding is the binding of a join. A join does not have a
// line of its own, so the text of the wrapper is the message then. The
// binding of a join itself has the lines that [SourceError.joinText]
// builds.
func (e *SourceError) text() string {
	for e != nil {
		t := e.texts()
		if t.wraps {
			return t.wrapped
		}

		inner := e.inner()
		if t.rows != nil || inner == nil || joinBinding(inner) != nil {
			break
		}

		e = inner
	}

	if e == nil {
		return ""
	}

	if msg := e.joinText(); msg != "" {
		return msg
	}

	return bareMessage(e.err, e.end)
}

// joinText returns the text of each binding that [SourceError.Error] lists
// for e, one per line, in the order of the join and without the limit of
// [ErrorListLimit]. It does so for an e that reads as the binding of a
// join, as [joinBinding] finds it. Each line leaves out the location of
// its binding, as [SourceError.text] does. The binding of a join thus reads
// the same whether a [Node] bound its problems before the join or not. It
// returns "" for any other e, and for a join whose problems do not have
// text.
func (e *SourceError) joinText() string {
	if joinBinding(e) != e {
		return ""
	}

	return strings.Join(e.listedTexts(nil, 0), "\n")
}

// listedTexts returns lines with the text of each binding that
// [SourceError.Error] lists below the line of e appended, in the order of
// the children of e. It leaves out the last shown children that e did not
// locate, as [SourceError.ordered] does. An unbound child among those adds
// the texts of the bindings it lists in its place. A child that heads
// problems adds its own text, when it has any, and then the texts of the
// bindings it lists. A child that binds a join adds the texts of the
// bindings the join lists.
func (e *SourceError) listedTexts(lines []string, shown int) []string {
	held := len(e.below) - shown

	for i, c := range e.below {
		switch {
		case c.detail:
			continue

		case i < held || c.located:
			joined := joinBinding(c.bound)
			if joined != nil {
				lines = joined.listedTexts(lines, 0)

				continue
			}

			if msg := c.bound.text(); msg != "" {
				lines = append(lines, msg)
			}

			if t := c.bound.texts(); t.count > 0 {
				lines = c.bound.listedTexts(lines, t.shown)
			}

		case c.unbound:
			if t := c.bound.texts(); t.count > 0 {
				lines = c.bound.listedTexts(lines, t.held)
			}
		}
	}

	return lines
}

// bareMessage returns the message of err without the part that end removes
// for the errors below it. A message that is all such a part, as that of a
// join is, stays whole.
func bareMessage(err error, end chainEnd) string {
	msg := err.Error()
	if own, _, _, _ := end.cut(msg); own != "" {
		return own
	}

	return msg
}

// all yields e and every binding below it in depth-first order, and reports
// whether the caller wants more. It yields each binding once, however many
// times the tree reaches it. A nil e does not yield anything.
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

// Bindings returns an iterator over each [*SourceError] that the wrappers
// and joins around err lead to, in depth-first order. The outermost comes
// first, and each branch of an [errors.Join] follows the one before it. It
// does not look below a binding, so each binding comes as one unit. A
// caller that renders each binding on its own, such as one section per
// document of a file, walks it:
//
//	for bound := range niceyaml.Bindings(err) {
//		fmt.Println(niceyaml.FormatError(bound))
//	}
//
// The iterator does not yield a binding for a nil err.
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
// depth-first order, including the details [Error.Unwrap] leaves out. It
// stops at a [*SourceError], which holds its own children.
func errorsBelow(err error) iter.Seq[error] {
	return func(yield func(error) bool) {
		eachBelow(err, yield)
	}
}

// eachBelow yields what [errorsBelow] yields and reports whether yield
// wanted more. It passes yield down, because nested iterators take time
// quadratic in the depth of a chain of joins.
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

// allBindings returns an iterator over each binding [Bindings] yields and
// every binding below it, depth first, with each binding once however
// many times the tree reaches it.
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
// context and [DefaultExcerptWidth] columns. The tree shows at most
// [ErrorListLimit] problems and a last row that counts the rest. A binding
// that [Detach] detached renders at the options Detach received instead.
// Every other verb formats [SourceError.Error] as it formats a string, with
// the width, precision, and flags given, so %q quotes the message.
//
// A wrapper such as [fmt.Errorf] around a SourceError formats as its own
// message under every verb. A program that holds any error thus calls
// FormatError for the excerpt. A source with excerpts off, as
// [WithExcerpts] sets it, does not show a source line under any verb.
func (e *SourceError) Format(f fmt.State, verb rune) {
	formatVerb(f, verb, e)
}

// LogValue implements [slog.LogValuer].
//
// The value is the tree [FormatError] prints for the error, as a string
// with each error behind its own position and without a source excerpt. It
// shows at most [ErrorListLimit] problems and a last row that counts the
// rest. A wrapper such as [fmt.Errorf] around a SourceError logs as its own
// message, which holds the list [SourceError.Error] returns.
func (e *SourceError) LogValue() slog.Value {
	return logValue(e)
}

// FormatError renders err as plain text for a log or a terminal without
// color. Its message comes first as the tree [ErrorReport.Tree] holds, with
// a connector in front of each error below another and each error behind
// the position its location resolved to:
//
//	cafe.yaml: 2 schema violations
//	|-- 6:8: $.spec.sla: string does not match pattern
//	`-- 22:11: $.spec.hours.days: expected "array", got "string"
//
// An excerpt follows for each source the bindings in err touch, led by the
// name of its source when err touches several. Each excerpt shows the
// marked lines with context lines on either side, carets under each
// location, and the message of each error below a binding beside its caret.
// A source whose every mark repeats a warning that another excerpt marks
// does not get an excerpt, as [ErrorReport.Tree] describes. The context
// lines and the width of long lines come from opts, as [ExcerptOption]
// describes. An excerpt of a binding that [Detach] detached keeps those of
// the options Detach received instead.
//
// A line starting "no excerpt:" follows the excerpts for each binding
// [Bindings] yields whose tree does not resolve any location, with the
// reason [SourceError.Unresolved] returns for it. A binding without a
// reason, and one whose reason wraps
// [go.jacobcolvin.com/niceyaml/paths.ErrNoDocument], does not get such a
// line. A source with excerpts off, as [WithExcerpts] sets it, does not add
// an excerpt or a line in its place.
//
// FormatError looks through the wrappers and joins around a [SourceError]
// and renders every problem, however many err holds. An error that is not
// bound to a source renders as its tree alone, with the path of its
// [*Error] in front, as "@.open: closes before it opens". An error whose
// tree renders as empty text renders its message instead.
//
// A tab renders as four spaces in the tree, in each "no excerpt:" line,
// and in the message beside a caret. A line feed in a message of the tree
// starts a new row. Every other control character, and every control
// character of the source lines of an excerpt, renders as
// [go.jacobcolvin.com/niceyaml/escape.Control] draws it. A tab in a line
// of the source thus shows as a picture, and the output does not hold
// escape sequences. The text ends with its last row and no line feed, so a
// program that writes it into a file or a fenced block adds that line feed
// itself. A nil err renders as "".
func FormatError(err error, opts ...ExcerptOption) string {
	return formatError(err, newExcerptConfig(opts), 0)
}

// formatError renders err as [FormatError] documents it, from the report
// [newErrorReport] builds with cfg and limit.
func formatError(err error, cfg excerptConfig, limit int) string {
	rep := newErrorReport(err, cfg, limit)

	parts := make([]string, 0, 1+len(rep.Excerpts)+len(rep.Unresolved))
	parts = append(parts, renderErrorTree(rep.Tree))

	for _, shown := range rep.Excerpts {
		part := shown.View.String()

		// The name is the caller's text, so its control characters render
		// as those of the tree do.
		if shown.Named {
			part = escape.Line(shown.Name) + "\n" + part
		}

		parts = append(parts, part)
	}

	for _, bound := range rep.Unresolved {
		// The output promises no escape sequences, so the reason draws its
		// control characters and its tabs as the tree draws them.
		parts = append(parts, "no excerpt: "+escape.Line(bound.Unresolved().Error()))
	}

	return joinParts(parts...)
}

// renderErrorTree renders t as plain text, each child behind a connector
// as [FormatError] shows. Each text passes through [escape.Message], so
// text the library did not write cannot send escape sequences.
func renderErrorTree(t ErrorTree) string {
	var sb strings.Builder

	if t.Label != "" {
		sb.WriteString(escape.Message(t.Label))
	}

	writeErrorBranches(&sb, t.Children, "", t.Label != "")

	return sb.String()
}

// formatVerb formats err for verb, as [Error.Format] and
// [SourceError.Format] describe.
func formatVerb(f fmt.State, verb rune, err error) {
	switch {
	case verb == 'v' && f.Flag('+'):
		writeString(f, formatError(err, defaultExcerptConfig(), ErrorListLimit))

	default:
		//nolint:errcheck // Formatter has no error channel.
		_, _ = fmt.Fprintf(f, fmt.FormatString(f, verb), err.Error())
	}
}

// logValue returns the tree of err cut to [ErrorListLimit] problems. It
// returns the [messageTree] of err instead when the tree does not have a
// row, such as for a bound join of typed-nil errors.
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

// Range returns the range in the source that the error points at and true.
// It is the range the error carries, or the content of the token at its
// position or at the node its path resolves to. For a token that spans
// several lines, the range spans them too. The range is in the coordinates
// of [Source.Lines].
//
// A position from [AtPosition] can fall inside a token, on the spaces
// before it, or on a later line of a multi-line token. The range then
// starts where the content of the token starts, so it might differ from
// [SourceError.Position]. For a path the document leaves out, or one that
// enters an alias it cannot follow, the range covers the token
// [SourceError.Nearest] describes.
//
// Range reports false when the error does not carry a location or its
// location did not resolve, as [SourceError.Unresolved] explains, and for a
// nil SourceError.
func (e *SourceError) Range() (position.Range, bool) {
	if e == nil || e.locErr != nil {
		return position.Range{}, false
	}

	return e.rng, true
}

// Position returns the position [SourceError.Error] puts in front of the
// message and true. It counts from 0 as [position.Position] does, so a
// message that opens with "3:9:" has the position `{Line: 2, Col: 8}`. For
// a range or a path, it is the start of [SourceError.Range]. Position
// returns a position from [AtPosition] as the error gave it, but Range
// starts at the content of the token the position falls in. A report that
// names the place of an error as its message does reads Position, and one
// that marks the text reads Range.
//
// Position reports false when Range does, for an error that does not carry
// a location or whose location did not resolve. It also reports false for a
// nil SourceError.
func (e *SourceError) Position() (position.Position, bool) {
	if e == nil || e.locErr != nil {
		return position.Position{}, false
	}

	return e.loc.pos, true
}

// Unresolved returns why the location of the bound error did not resolve.
// It returns nil when the location resolved. It also returns nil when the
// error does not carry a location, such as an error from [fmt.Errorf] bound
// through the root of a document. The reason is one of these:
//
//   - [ErrOutOfRange] for a location on a line the source does not hold or
//     at a column before the first.
//   - [ErrPathNeedsDocument] for a path without a single document to
//     resolve in.
//   - The resolution error from [go.jacobcolvin.com/niceyaml/paths] for a
//     path that does not select anything in the document.
//   - [ErrNoLocation] for a path whose token does not carry a position.
//   - [ErrAmbiguousPath] for a path that names the entries of several keys
//     of a decoded map.
//
// A path to a key a mapping leaves out binds at that mapping, as
// [SourceError.Nearest] describes, and does not have a reason. A path that
// enters an alias the document cannot follow is the one binding with both a
// position and a reason. It binds at the alias, and the reason wraps
// [go.jacobcolvin.com/niceyaml/paths.ErrAlias], so a caller can distinguish
// that position from one at the value itself. For an alias that reads a
// reference document of [WithReferences], the reason wraps
// [go.jacobcolvin.com/niceyaml/paths.ErrReferenceAlias] too. Unresolved
// returns nil for a nil SourceError.
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
// the document holds. [SourceError.Path] and the message keep the path the
// error carries. Nearest reports false for an error bound at the node its
// path selects, for one that lacks either a path or a position, and for a
// nil SourceError. The nearest node is one of these:
//
//   - The mapping that lacks a key the path names, as for a required field
//     or a misspelled name. The range covers the key of that mapping, or
//     the token [AtPath] describes for a mapping without a key, and
//     [SourceError.Unresolved] returns nil.
//   - An alias on the path that the document cannot follow, such as one to
//     an anchor of a reference document from [WithReferences]. The range
//     covers the `*` of the alias, and SourceError.Unresolved returns a
//     reason that wraps [go.jacobcolvin.com/niceyaml/paths.ErrAlias].
//   - A value that decodes itself, for an error a [SelfValidator] returns
//     at or below it, because its fields do not have to mirror the
//     document. The range covers the text of the value. Every error below a
//     slice, an array, or a map that decodes itself binds at it.
//
// An error about a required field thus binds at the key of the mapping that
// lacks the field:
//
//	niceyaml.NewError("name is required", niceyaml.AtPath(paths.Doc().Child("server", "name")))
//	// cfg.yaml:3:1: $.server.name: name is required
//
// A `<<` merge key might set a key from an alias the document cannot
// follow. The document does not show which line holds the value of such a
// key, so the key does not have a position.
func (e *SourceError) Nearest() (paths.Path, bool) {
	if e == nil || e.locErr != nil || e.loc.near == nil {
		return paths.Path{}, false
	}

	return *e.loc.near, true
}

// Annotate marks the error and every binding below it on view, as
// [Annotate] marks them, and reports whether it marked any line. A caller
// that holds one binding, such as one [Bindings] yields or [errors.As]
// finds, thus marks its whole tree with one call. A binding without a
// location, such as the binding of a summary that does not carry one, does
// not add a mark for itself. Neither does a binding without a line of its
// own, such as the binding of a join, even with a location. The bindings
// below either one still mark their lines. A binding that [Detach] detached
// does not mark anything, and neither does a nil SourceError.
func (e *SourceError) Annotate(view *line.View) bool {
	return Annotate(e, view)
}

// Annotate marks every binding in the tree of err on view, once each, on
// the lines of its source that view holds. It reports whether it marked any
// line. A viewer that shows a document with its errors in place marks its
// view and renders it as it is:
//
//	view := source.View()
//	niceyaml.Annotate(err, view)
//	lipgloss.Println(p.Print(view))
//
// Each binding highlights its location with [kind.GenericError] and labels
// the same columns with an annotation in [kind.TextError] below each line
// the highlight covers. A renderer draws a caret run under the columns of
// each annotation. The first annotation on the line of the location carries
// the [ErrorTree.Message] of the node of the binding, with each tab as four
// spaces, and the others do not carry a message. That message is the
// [SourceError.Message] of the binding, with the text that a wrapper around
// the binding added. An error without a message thus gets caret runs alone.
//
// Problems whose node is a warning highlight in [kind.GenericWarn] and
// annotate in [kind.TextWarn] instead, and so do their details.
// [ErrorTree.Warning] reports whether a node is a warning. A warning that
// [AsErrors] marks above a wrapper around its binding thus draws as an
// error, as its row does.
//
// A location whose highlight leaves out its own line, such as a position
// past the end of a line, gets a zero-width annotation at its column. The
// annotation makes [line.View.Hunks] keep the line. A column past the end
// of its line moves to the column after its last rune. A binding whose
// location did not resolve does not add a mark. Each problem of a join that
// does not carry a location marks the location of the join. The binding of
// the join does not have a line of its own, so it does not add a mark even
// with a location.
//
// Annotate finds each line by identity, so the view can be the whole
// source, a slice of it, or a diff against another revision. A binding
// marks only the lines of its own source. A line keeps each mark once, so
// marking a view again, or with a binding inside an error that marked it
// already, does not double a message. Annotate marks a source with excerpts
// off too, because the caller decides who sees the view. A binding that
// [Detach] detached does not keep a line of any view, so it does not mark
// anything. Annotate reports true when it marked a line, counting one that
// held the marks already, and false for a nil err.
//
// A unified diff shows each unchanged line as the line of the after
// revision, so a binding of the before revision marks only the lines the
// diff deletes from it.
// [go.jacobcolvin.com/niceyaml/diff.Result.UnifiedRow] maps each line of
// the before revision to its row.
func Annotate(err error, view *line.View) bool {
	wrappers := wrapperMarks(NewErrorTree(err))
	sources, positions := treePositions(slices.Collect(Bindings(err)), true, nil, nil, wrappers)

	marked := false

	for _, origin := range sources {
		src := origin.live()
		if src != nil && excerpt.Annotate(view, src.lines, positions[origin]) {
			marked = true
		}
	}

	return marked
}

// Excerpt returns a [line.View] of the source the error is bound to around
// the locations of its tree, and true. It marks a fresh [Source.View] as
// [Annotate] does. The location of the root gets a caret run alone, because
// the tree that [FormatError] prints above the excerpt names the root.
// [line.View.Hunks] then keeps the context lines that opts sets on either
// side of each marked line, with distant locations in separate hunks.
// [line.View.Clip] cuts long lines to the width that opts sets, as
// [ExcerptOption] describes. The lines keep their numbers in the source, so
// any [go.jacobcolvin.com/niceyaml/printer.Printer] renders the excerpt
// with the line numbers of the file.
//
// Excerpt leaves out a node bound to another source and a node whose
// location does not resolve. It returns a nil view and false for a nil
// SourceError and for a source with excerpts off, as [WithExcerpts]
// describes. It does the same for a tree in which every location fails to
// resolve in the source.
//
// A binding that [Detach] detached builds its excerpt from the lines it
// kept, with the context lines and the width of the options Detach
// received, whatever opts holds. The view holds those lines alone, so its
// indices differ from those of [Source.Lines]. Each line keeps its number
// in the file.
func (e *SourceError) Excerpt(opts ...ExcerptOption) (*line.View, bool) {
	if e == nil {
		return nil, false
	}

	// The location of a lone binding carries no message, so the excerpt
	// gives it a caret run alone, since the tree above the excerpt names
	// it.
	_, positions := treePositions([]*SourceError{e}, false, nil, nil, nil)

	src := e.origin()

	return src.excerpt(positions[src], newExcerptConfig(opts))
}

// excerptSource is the text that the excerpts of a binding show. It is the
// [*Source] the binding is bound to, or the [*keptSource] that a detached
// binding holds in its place.
type excerptSource interface {
	// Name returns the name of the text, as [Source.Name] returns it.
	Name() string
	// FilePath returns the file path of the text, as [Source.FilePath]
	// returns it.
	FilePath() string
	// The excerpt method returns a view of the text with marks on it, cut to
	// hunks and clipped as cfg sets, or as the options the text kept. It
	// reports false when it did not mark a line.
	excerpt(marks []excerpt.Mark, cfg excerptConfig) (*line.View, bool)
	// The live method returns the Source of the text, or nil for the text
	// a detached binding kept.
	live() *Source
	// The identity method returns a weak pointer to the Source the text
	// came from: the Source itself, or the Source that a detached binding
	// kept the text of. The texts of one Source return equal pointers.
	identity() weak.Pointer[Source]
}

// live returns s, the Source of its own text.
func (s *Source) live() *Source {
	return s
}

// identity returns a weak pointer to s, the Source of its own text.
func (s *Source) identity() weak.Pointer[Source] {
	return weak.Make(s)
}

// sameSource reports whether a and b are texts of one [Source]: that Source
// itself, or what a binding that [Detach] detached kept of it. It reports
// false when either is nil.
func sameSource(a, b excerptSource) bool {
	return a != nil && b != nil && a.identity() == b.identity()
}

// excerpt returns a fresh view of s with positions marked, cut to hunks
// and clipped as cfg sets. Every error excerpt of a Source comes from
// here, so each passes the [WithExcerpts] check.
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
// resolved, so an excerpt marks a line for the tree of e. A binding that
// [SourceError.headsAlone] reports does not mark a line.
func (e *SourceError) marks() bool {
	found := false

	e.all(map[*SourceError]bool{}, func(n *SourceError) bool {
		found = n.locErr == nil && !n.headsAlone()

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

// treePositions returns the sources the trees of bindings touch, as
// [SourceError.origin] gives them, in walk order, with the resolved
// locations bound to each. A root carries its message only when labeled is
// true, and a node in skipped does not add anything. A node in quiet adds
// its source but not a mark. For a binding in wrappers, the mark carries
// the message and the severity of the node of the wrapper around it, as
// [wrapperMarks] finds them. The mark of a warning carries the "warning:"
// label beside its message. A detail takes the severity of the problem it
// explains but never the label.
func treePositions(
	bindings []*SourceError,
	labeled bool,
	skipped, quiet map[*SourceError]bool,
	wrappers map[*SourceError]wrapperMark,
) ([]excerptSource, map[excerptSource][]excerpt.Mark) {
	var (
		sources []excerptSource
		walk    func(n, root *SourceError, warning, detail bool)
	)

	touched := make(map[excerptSource]bool)
	positions := make(map[excerptSource][]excerpt.Mark)
	seen := make(map[*SourceError]bool)

	walk = func(n, root *SourceError, warning, detail bool) {
		if n == nil || seen[n] {
			return
		}

		seen[n] = true

		if !detail {
			warning = n.isWarning()
			if w, ok := wrappers[n]; ok {
				warning = w.warning
			}
		}

		src := n.origin()

		if !skipped[n] && !touched[src] {
			touched[src] = true
			sources = append(sources, src)
		}

		if !skipped[n] && !quiet[n] && n.locErr == nil && !n.headsAlone() {
			positions[src] = append(positions[src], n.mark(labeled || n != root, warning, detail, wrappers))
		}

		for _, c := range n.below {
			walk(c.bound, root, warning, detail || c.detail)
		}
	}

	for _, root := range bindings {
		walk(root, root, false, false)
	}

	return sources, positions
}

// mark returns the excerpt mark of e, a binding whose location resolved,
// with its message when labeled. The message is the one that wrappers
// holds for e, or the [SourceError.Message] of e. The mark of a warning
// carries the "warning:" label beside its message unless it is a detail.
func (e *SourceError) mark(labeled, warning, detail bool, wrappers map[*SourceError]wrapperMark) excerpt.Mark {
	at := excerpt.Mark{Pos: e.loc.pos, Ranges: e.ranges, Warning: warning}
	if !labeled {
		return at
	}

	at.Message = e.text()
	if w, ok := wrappers[e]; ok {
		at.Message = w.message
	}

	if warning && !detail {
		at.Message = prefix("warning:", at.Message)
	}

	return at
}

// isWarning reports whether e binds one problem that is a warning, as
// opposed to an error or a heading.
func (e *SourceError) isWarning() bool {
	return !e.heads() && isWarning(e.err)
}

// checkInRange reports [ErrOutOfRange] when loc starts on a line that lines
// does not hold or at a column before the first. The message counts lines
// and columns from 1, as the text does.
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

// PanicError is the error of a panic that the parse or a decode recovered.
// Value holds the value the code passed to panic, and Stack holds the stack
// of the goroutine at the panic, as [debug.Stack] formats it. The message
// leaves the stack out.
//
// The go-yaml parser and decoder run under a recover, and so do the
// UnmarshalYAML and UnmarshalText methods and the [CustomUnmarshaler]
// functions the decoder calls. A panic in any of them reaches the caller as
// a [SourceError] that holds a PanicError, with a message such as "decoder
// panicked: registry not initialized":
//
//	if p, ok := errors.AsType[*niceyaml.PanicError](err); ok {
//		slog.Error("decode panicked", "value", p.Value, "stack", string(p.Stack))
//	}
//
// A panic is a bug in code, not a fault of the document. The error does not
// match [ErrDecode], [ErrSyntax], or a decode label such as [ErrWrongKind],
// and [IsInvalid] does not report it. The library does not recover a panic
// in a [Validator] or a [SelfValidator], so such a panic reaches the caller
// as a panic.
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

// panicked returns p sealed with marks on top. The message starts with in,
// the name of the code that panicked, as in "decoder panicked: boom". The
// seal keeps a panic value, even an [*Error], from adding a location, a
// problem, or a fault.
func panicked(in string, p *PanicError, marks ...error) *markedError {
	return &markedError{
		err:     p,
		msg:     fmt.Sprintf("%s panicked: %v", in, p.Value),
		marks:   marks,
		refused: panicRefused,
		sealed:  true,
	}
}

// holdsPanic reports whether err is, or wraps, a [*PanicError]. The
// document is not at fault for such an error, so a decode and the parse
// return it as they return the error of a context that ended.
func holdsPanic(err error) bool {
	var p *PanicError

	return errors.As(err, &p)
}

// markedError is an error with marks of its own on top of the error it
// holds. A sealed one does not unwrap to an error. A chain walk thus does
// not print the position and the excerpt that go-yaml writes into the text
// of that error.
type markedError struct {
	err error
	// The text that stands in for the message of err. A sealed error always
	// returns it.
	msg string
	// The errors the error matches on top of what err matches.
	marks []error
	// The targets the Is method never matches through err.
	refused []error
	// The error does not unwrap to err.
	sealed bool
}

func (e *markedError) Error() string {
	if e.sealed || e.msg != "" {
		return e.msg
	}

	return e.err.Error()
}

// Unwrap returns the error e holds, or nil when e is sealed.
func (e *markedError) Unwrap() error {
	if e.sealed {
		return nil
	}

	return e.err
}

// Is reports whether target is one of the marks of e, or, for a sealed e,
// what the error it holds matches apart from the refused targets.
func (e *markedError) Is(target error) bool {
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
func (e *markedError) As(target any) bool {
	return e.sealed && errors.As(e.err, target)
}

// without returns e without mark among its marks. The result does not share
// a slice of marks with e.
func (e *markedError) without(mark error) *markedError {
	out := *e
	out.marks = slices.DeleteFunc(slices.Clone(e.marks), func(m error) bool { return errors.Is(m, mark) })

	return &out
}

// contextEnded reports whether err is, or wraps, the error of a context
// that ended: [context.Canceled] or [context.DeadlineExceeded].
func contextEnded(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// holdsLocation reports whether err or any error below it, details
// included, is a [*SourceError] or an [*Error] with a location or a move.
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
