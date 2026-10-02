package niceyaml

import (
	"cmp"
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

	// ErrNoDocuments indicates a [Source] that holds no YAML document where
	// one was expected, such as a file holding only a "..." marker and any
	// comment on its line. [Source.Document] returns it.
	ErrNoDocuments = errors.New("no documents in source")

	// ErrMultipleDocuments indicates a [Source] that holds more than one YAML
	// document where one was expected. [Source.Document] returns it.
	ErrMultipleDocuments = errors.New("multiple documents in source")

	// ErrDecodeTarget indicates the value given to [Node.DecodeInto] is
	// not a non-nil pointer, so there is nothing to decode into. The
	// error comes back bound to the source as a [SourceError] with no
	// location.
	ErrDecodeTarget = errors.New("decode target is not a non-nil pointer")

	// ErrDecodeRejected indicates the go-yaml decoder rejected the value
	// [Node.Decode], [Node.DecodeInto], or [Decoder.DecodeInto] gave it:
	// a value that does not read as the target type, a number that
	// overflows it, or a field the target lacks under
	// [WithDisallowUnknownFields]. The decoder reports every rejection as
	// one kind of error, so the sentinel tells them apart from nothing
	// finer.
	// The error comes back bound as a [SourceError] at the path of the
	// offending value, from the root of the document, or at the path of
	// its key for a rejection of the key, such as an unknown field. Its
	// message names kinds of YAML values and no Go type, as
	// [Node.DecodeInto] describes. For a value the document leaves out,
	// such as the value of a key with nothing after its colon, the path
	// resolves to the null the parser puts there.
	// Text that [time.ParseDuration] rejects for a [time.Duration]
	// matches too. The decoder reports it without a token, so the decode
	// binds it at the path of the value, as [Node.DecodeInto] describes.
	// An error a value's own UnmarshalYAML or UnmarshalText returns does
	// not match. It is the value's own, and the decode binds it at the
	// path of the value the same way. The error of a context that ended
	// comes back as it is and does not match either.
	// Neither does any other error the decoder reports without a token
	// of the source, which comes back with no location. One is
	// an error for a target type whose definition the decoder refuses,
	// such as a struct with two fields of one name or an inline embedded
	// struct that is not exported. Another is an error for a key of a map
	// tagged inline, such as the key `name` beside a map[int]int, or for
	// a field tagged inline whose type cannot hold a mapping, such as an
	// int. A value of an inline map keeps its token, so an error in that
	// value matches as it would in any other field.
	// A panic in the go-yaml decoder or in a value's own UnmarshalYAML
	// does match, bound at the first token of the node that is not a
	// comment, with no go-yaml error in the chain. So does a value nested
	// deeper than the decoder allows, bound the same way, and a `<<`
	// merge key whose alias names no anchor before it, or an anchor that
	// holds the merge key, bound at the alias. The decoder reports those
	// two without a location, and the chain holds its error. A rejection
	// of a value that an alias reads from a reference document, from
	// [WithReferences] or the yaml.Reference options, matches too, and
	// so does an unwrapped go-yaml error an UnmarshalYAML returns from a
	// parse of its own in such a node, since the decoder reports both
	// alike. So does a go-yaml error the decoder reports without a token
	// in such a node, such as the one for a key of an inline map[int]int.
	// Each binds at the alias when the node holds one alias to a
	// reference document, directly or inside an anchor its aliases reach.
	// When the node holds several, the error carries no location, even
	// if the target type reads only one of them. A definition error
	// never matches, even beside an alias to a reference document, since
	// the decoder returns it as a plain error, not a go-yaml error. The
	// error for a [time.Duration] matches only where the decode binds it
	// at the value. One the decode finds no path for, such as text an
	// alias reads from a reference document, comes back with no location
	// and does not match.
	ErrDecodeRejected = errors.New("decoder rejected the value")

	// ErrExcessiveAliasing indicates a node whose document holds so many
	// nested aliases that a decode would read far more than the document
	// holds. A merge key reads the mapping it brings in again at every
	// merge, so a few hundred bytes of nested aliases can take the go-yaml
	// decoder minutes to decode, and the decoder never checks the context.
	// [Node.Decode], [Node.DecodeInto], and [Decoder.DecodeInto] refuse
	// to decode a node that holds an alias when the aliases of its
	// document go past the limit gopkg.in/yaml.v3 applies, unless
	// [WithAliasLimit] turns the check off. The error comes back bound
	// as a [SourceError] at the first token of the node that is not a
	// comment, and it does not match [ErrDecodeRejected]. [Node.Nodes]
	// returns it too, bound to its receiver, when aliases lead a selector
	// of the path to far more nodes than the document holds. The paths
	// and schema packages export the same error value.
	ErrExcessiveAliasing = aliaslimit.ErrExcessiveAliasing

	// ErrParseRejected indicates the go-yaml parser panicked on the tokens
	// of a [Source], such as a token with no position that
	// [NewSourceFromTokens] received. [Source.File] returns it on every
	// call, bound as a [SourceError] at the first token with a position
	// among the ones the parser was reading.
	ErrParseRejected = errors.New("parser rejected the tokens")

	// ErrOutOfRange indicates the error's location lies outside the source.
	// The location starts on a line past the last or before the first,
	// which happens when a position or range came from other text, or at a
	// column before the first. [SourceError.Unresolved] reports it.
	ErrOutOfRange = errors.New("location outside source")

	// ErrPathNeedsDocument indicates that [Source.Bind] bound an error that
	// carries a path in a source that holds no single document to resolve
	// the path in. It wraps the reason [Source.Document] gives:
	// [ErrMultipleDocuments], [ErrNoDocuments], or the error the file
	// fails to parse with. [SourceError.Unresolved] reports it. Bind such
	// an error through [Node.Bind] with the document the check ran
	// against. Node.Bind reports it too for a path bound through a
	// document that did not parse, where it wraps the syntax error
	// [Node.Err] returns.
	ErrPathNeedsDocument = errors.New("path needs a document to resolve in")

	// ErrAmbiguousPath indicates a path that names the entries of more
	// than one key of a decoded map. Keys of different types that the
	// document spells with one text, such as 1 and "1" in a map[any]T,
	// share one path, and so do several NaN keys. Such a path resolves to
	// the document entry of one of them at most. A decode binds the
	// errors a [SelfValidator] reports under such a path with no
	// position, so none points at the line of another entry.
	// [SourceError.Unresolved] reports it.
	ErrAmbiguousPath = errors.New("path names the entries of several keys")

	// The reason of a [SourceError] whose error carries no location at
	// all, which is not a failure to resolve one, so
	// [SourceError.Unresolved] reports nil for it.
	errUnlocated = errors.New("no location")
)

// Error is an error that points at a location in a YAML document.
//
// The location is a [paths.Path], set with [AtPath], a
// [position.Position] or a [position.Range], set with [AtPosition] or
// [AtRange], or a path and one of the other two. A path names the value
// the error is about, and a position or a range names the characters at
// fault. An Error with both reports the path in its message and binds at
// the position or the range. A check that knows the value and the exact
// characters inside it, such as a rule on one character of a string,
// thus names the value and highlights the characters at once. The
// last of AtPosition and AtRange given wins. [Error.Path],
// [Error.Position], and [Error.Range] each return the part of the
// location of that type:
//
//	if path, ok := err.Path(); ok {
//		// The error is about the value at path.
//	}
//
// A path resolves within one document of a source, from the [Node] that
// binds the Error, whether its own methods and validators produced the
// Error or [Node.Bind] bound one built elsewhere. An Error that carries
// a position or a range as well binds there, and the path is not
// resolved, so it names the value in the message and in
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
// [Error.Error] returns the message, with the path in front when the Error
// carries one: "$.path: msg". A position is never part of the message
// until a [SourceError] binds the Error and puts the resolved one in front,
// so a bound error reads "name:line:col: $.path: msg" or
// "name:line:col: msg". Nested errors from [WithErrors] are structure
// rather than text. [Error.Errors] returns them, [Error.Unwrap] exposes
// them to [errors.Is] and [errors.As], and the [SourceError] that binds
// the Error binds each one as a child at the location its own error
// carries, if any. [Error.Format] prints them as a tree under the %+v
// verb.
//
// Error implements the error interface. Use [Error.Unwrap] with [errors.Is]
// and [errors.As] to inspect wrapped errors.
//
// Create instances with [NewError] or [WrapError].
type Error struct {
	err error
	// The position or the range: a position.Position or a position.Range,
	// or nil when neither is set.
	loc any
	// The nested errors from WithErrors, which leaves out the nil ones.
	errors []error
	// The path, when hasPath, since the root is a path like any other.
	path paths.Path
	// The path the errors under the Error write their paths from, which
	// Rebase sets, when rebased, since the root is a base like any other.
	base    paths.Path
	hasPath bool
	rebased bool
}

// NewError creates a new [*Error] with the given message.
// Use [WrapError] instead if wrapping an existing error.
func NewError(msg string, opts ...ErrorOption) *Error {
	return WrapError(errors.New(msg), opts...)
}

// WrapError creates a new [*Error] wrapping an existing error.
// Use [NewError] instead if creating an error from a message string.
func WrapError(err error, opts ...ErrorOption) *Error {
	e := &Error{err: err}
	for _, opt := range opts {
		opt(e)
	}

	return e
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

	for _, opt := range opts {
		opt(&c)
	}

	return &c
}

// Rebase returns an error that writes its paths from base. Every path
// in the tree of err, whether on the [*Error] that anchors it or on an
// error nested with [WithErrors], resolves as base joined with that path,
// and the message of the result carries the joined path. A check
// written for a type writes paths from the value's own root. A caller
// that runs it on a value inside a document rebases the result under
// the path of that value before it binds the result:
//
//	func checkHours(h *Hours) error {
//		if h.Close.Before(h.Open) {
//			return niceyaml.NewError("closes before it opens", niceyaml.AtPath(paths.Root().Child("close")))
//		}
//
//		return nil
//	}
//
//	return doc.Bind(niceyaml.Rebase(checkHours(&cfg.Hours), paths.Root().Child("hours")))
//
// The same call puts each element of a slice under its index. Rebases
// compose, so a chain of them composes the chain of paths. An error
// under the base that carries no location and wraps no binding points at
// base itself, and a position or a range stays as it is, since the base
// moves paths alone. A decode rebases the errors of every nested
// [SelfValidator] itself, so a Validate need not rebase the Validate of
// a field. A Node from [Node.At] or [Node.Nodes] puts its own path in
// front of each path in an error it binds, so a check bound through the
// Node of its value needs no Rebase. Such a Node leaves an error with no
// location as it is.
//
// An error joined from several, as [errors.Join] builds one, rebases
// branch by branch into a new join, so each line of its message carries
// the path of its own branch. Any other error that unwraps to several,
// and whose message is theirs one per line, rebases the same way. The
// new join matches that error for [errors.Is] and [errors.As], and hands
// both checks to any Is or As method the error has, so a caller that
// checks for a multi-error type of its own still finds it. An [*Error]
// that only wraps a join, with no location and no errors nested with
// [WithErrors], rebases the way the join does, and the result stays an
// [*Error]. A join whose every branch is a nil [*Error] or
// [*SourceError] pointer is still an error, and it points at base as an
// error with no location does.
//
// The result wraps err, or each branch of a join, so [errors.Is] and
// [errors.As] see through it, and the text a wrapper such as [fmt.Errorf]
// added around a located error stays as it is, with the path the wrapper
// wrote in it, so rebase an error before adding context to it. A nil
// err, or a nil [*Error] or [*SourceError] pointer, returns nil, so a
// validator returns the result as it is. An error that is or wraps a
// [*SourceError], with no [*Error] above it that carries a location or
// nests errors with [WithErrors], is bound already, with its location
// resolved, and comes back as it is. An Error above a binding that
// carries a location or nests errors adds paths of its own, so Rebase
// puts the base in front of those.
func Rebase(err error, base paths.Path) error {
	if isNothing(err) {
		return nil
	}

	if isBound(err) {
		return err
	}

	// An Error that adds nothing to the join it wraps rebases as the join.
	x, ok := err.(*Error) //nolint:errorlint // The node itself, not a chain search.
	if ok && x.addsNothing() {
		if _, joined := joinBranches(x.err); joined {
			return WrapError(Rebase(x.err, base))
		}
	}

	if branches, ok := joinBranches(err); ok {
		rebased := make([]error, 0, len(branches))
		for _, branch := range branches {
			r := Rebase(branch, base)
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

	return &Error{err: err, base: base, rebased: true}
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
	if target != nil && reflect.TypeOf(target).Comparable() && j.join == target {
		return true
	}

	x, ok := j.join.(interface{ Is(target error) bool }) //nolint:errorlint // The join itself, not a chain search.

	return ok && x.Is(target)
}

// As sets target to the join when target points at a type the join is
// assignable to, as [errors.As] does for an error in the chain. Otherwise
// it reports what the As method of the join, if it has one, reports.
func (j *rebasedJoinError) As(target any) bool {
	val := reflect.ValueOf(target)
	if val.Kind() == reflect.Pointer && !val.IsNil() && reflect.TypeOf(j.join).AssignableTo(val.Type().Elem()) {
		val.Elem().Set(reflect.ValueOf(j.join))

		return true
	}

	x, ok := j.join.(interface{ As(target any) bool }) //nolint:errorlint // The join itself, not a chain search.

	return ok && x.As(target)
}

// ErrorOption configures an [Error]. [AtPath] sets its path. [AtPosition]
// or [AtRange] sets its position or range, and the last of those two
// given wins. [WithErrors] adds nested errors.
//
// Available options:
//   - [AtPath]
//   - [AtPosition]
//   - [AtRange]
//   - [WithErrors]
type ErrorOption func(e *Error)

// AtPath is an [ErrorOption] that sets the YAML path of the value the
// error is about. It replaces a path set before it. The error points at
// the node the path selects, which for a mapping entry is its value, so
// [SourceError.Excerpt] highlights the value, and for a mapping or
// sequence it highlights the first key or element. [AtPosition] or
// [AtRange] narrows the location to the characters at fault instead, and
// the path then names the value in the message alone. A path from
// [paths.Path.Key] points at the key of the entry instead, which suits
// an error about the key itself, such as an unknown field:
//
//	niceyaml.NewError("unknown field", niceyaml.AtPath(paths.Root().Child("spec", "foo").Key()))
//
// The path resolves from the scope of the [Node] that binds the Error,
// so [paths.Root] names that node itself, and a check on a value from
// [Node.At] writes its paths from the value. The binding puts the path
// of that Node in front, so its message and [SourceError.Path] carry
// the path from the root of the document. A path from [Node.Path] or
// from SourceError.Path starts at that root already, so it binds through
// the root of the document or through [Source.Bind], and a scoped Node
// joins it under its own path again.
//
// The document may leave the value out, as it does when a check reports
// a required field. The path then selects nothing, and the error binds at
// the key of the mapping that lacks the value, as [SourceError.Nearest]
// describes. A check thus names the field whether the document holds it
// or not:
//
//	func (s Server) Validate() error {
//		if s.Name == "" {
//			return niceyaml.NewError("name is required", niceyaml.AtPath(paths.Root().Child("name")))
//		}
//
//		return nil
//	}
//
// A producer may know where the value lies and still have no path that
// selects it. A validator that reads decoded data cannot always spell
// the key of a value as the source does. Such a producer gives
// [AtPosition] or [AtRange] beside the path. The error then binds there,
// and the path names the value in the message.
func AtPath(p paths.Path) ErrorOption {
	return func(e *Error) {
		e.path, e.hasPath = p, true
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
	return func(e *Error) {
		e.loc = p
	}
}

// atToken returns an [ErrorOption] that sets the position of tk, or one
// that sets nothing when tk or its position is nil, as the token of a
// go-yaml error can be.
func atToken(tk *token.Token) ErrorOption {
	if tk == nil || tk.Position == nil {
		return func(*Error) {}
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
	return func(e *Error) {
		e.loc = r
	}
}

// WithErrors is an [ErrorOption] that adds nested errors to the [Error],
// such as one per violation a validator found.
//
// A nested error is any error, and one that is an [*Error] can carry a
// location of its own. [Error.Errors] returns them, and the [SourceError]
// that binds the Error binds each one as a child at the location its own
// error carries, if any. [FormatError] lists each child as a branch of
// the message, and [SourceError.Excerpt] annotates the line of each child
// whose location resolves. A nested error that is a [*SourceError]
// already, or wraps one, binds as it is. WithErrors skips a nil nested
// error.
func WithErrors(errs ...error) ErrorOption {
	return func(e *Error) {
		for _, n := range errs {
			if !isNothing(n) {
				e.errors = append(e.errors, n)
			}
		}
	}
}

// Error returns the error message: "$.path: msg" when the Error carries a
// path, from [AtPath], and the message alone otherwise. A
// position or a range puts nothing in the message, since the [SourceError]
// that binds the Error puts the resolved position in front, and the nested
// errors from [WithErrors] put nothing in it either, since that SourceError
// lists them behind their own positions. An Error created from a nil error
// has an empty message, so its text is the path and a colon, such as
// "$.a:", or "" when it has none. An Error from [Rebase] carries the
// joined path in front of the message of the error it rebased, in place
// of the path that error wrote. An Error with a location of its own
// likewise replaces the path an Error it wraps wrote. The message thus
// names one location, the one [Error.Path] reports. An Error from Rebase
// whose cause chain reaches a [*SourceError] before a located Error puts
// no path in front, whether or not the binding has a location. That
// binding owns the location, and its text names whatever position and
// path it has.
func (e *Error) Error() string {
	if e == nil {
		return ""
	}

	if !e.rebased && !e.hasLocation() {
		if e.err == nil {
			return ""
		}

		return e.err.Error()
	}

	msg := e.message()
	if a := anchorOf(e); a.hasPath {
		msg = prefix(a.path.String()+":", msg)
	}

	return msg
}

// Format implements [fmt.Formatter].
//
// The %v and %s verbs print [Error.Error]. The %+v verb prints what
// [FormatError] renders for the error: its message as a tree with the
// nested errors from [WithErrors] under it. A log or a failing test that
// prints an unbound Error that way shows every nested error, as it does
// for a [*SourceError]. Every other verb formats [Error.Error] as it
// formats a string, with the width, precision, and flags given, so %q
// quotes the message and %-20v pads it.
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
// its message, with the nested errors from [WithErrors] under it behind
// their paths, and no source excerpt. A handler that logs an error by
// [Error.Error], as [slog.JSONHandler] does, shows every nested error
// this way, and one that formats it with %+v, as [slog.TextHandler]
// does, keeps the excerpt out of the attribute. A program that wants
// the excerpt in a log logs [FormatError] as a string.
func (e *Error) LogValue() slog.Value {
	return slog.StringValue(logTree(e))
}

// locus is the location an [Error] carries: a path when hasPath, a
// [position.Position] or a [position.Range] in loc, or both. The zero
// locus is no location.
type locus struct {
	loc     any
	path    paths.Path
	hasPath bool
}

// rebase returns l with base in front of its path. A locus with no path
// comes back as it is, since a base moves paths alone.
func (l locus) rebase(base paths.Path) locus {
	if l.hasPath {
		l.path = base.Join(l.path)
	}

	return l
}

// locus returns the location e carries itself, without looking through
// its cause chain or applying its base.
func (e *Error) locus() locus {
	return locus{loc: e.loc, path: e.path, hasPath: e.hasPath}
}

// addsNothing reports whether e adds nothing to the error it wraps: e is
// not nil, is not from [Rebase], carries no location of its own, and
// nests no errors with [WithErrors]. Such an Error has the text and the
// children of the error it wraps.
func (e *Error) addsNothing() bool {
	return e != nil && !e.rebased && !e.hasLocation() && len(e.errors) == 0
}

// hasLocation reports whether e carries a location of its own: a path, a
// position, or a range.
func (e *Error) hasLocation() bool {
	return e.hasPath || e.loc != nil
}

// Unwrap returns the underlying errors for [errors.Is] and [errors.As]. A
// nil Error unwraps to nothing, so a chain that holds one is safe to walk.
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
// [WrapError], or one holding the message given to [NewError]. An Error
// that wraps another Error returns that Error, where [SourceError.Cause]
// looks through it. It is nil for an Error created from a nil error, and
// a nil Error has no cause.
func (e *Error) Cause() error {
	if e == nil {
		return nil
	}

	return e.err
}

// Errors returns the errors nested in the [Error] with [WithErrors], in the
// order WithErrors received them and without the nil ones. A nil Error nests
// nothing. The slice is a copy, so a caller may keep or sort it.
func (e *Error) Errors() []error {
	if e == nil {
		return nil
	}

	return slices.Clone(e.errors)
}

// location returns the locus of the [Error]: a path from [AtPath], a
// position from [AtPosition], a range from [AtRange], or a path beside a
// position or a range. It looks through wrapping to the nearest Error
// that carries one, with the base of every [Rebase] on the way joined in
// front of a path. A [*SourceError] on the way ends the walk, as binding
// does, and reports the location it resolved from, with no base from
// above the binding, so an Error and its binding agree on the location.
// An Error from Rebase with nothing located below it points at its base.
// A nil Error and an Error with no location return the zero locus.
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
// along its cause chain. An Error built with [WrapError] around a
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

// message returns the text of e without the location e or the Errors it
// directly wraps put in front: the message of [Error.textCause], or ""
// when that is nil. Text a foreign wrapper such as [fmt.Errorf] added
// stays as it is, as it does everywhere else.
func (e *Error) message() string {
	cause := e.textCause()
	if cause == nil {
		return ""
	}

	return cause.Error()
}

// textCause returns the innermost error along the causes of e that is
// not an Error, the one whose text [Error.message] returns, or nil when
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
// it, and near is the path of that mapping.
type location struct {
	rng  *position.Range
	tk   *token.Token
	near *paths.Path
	pos  position.Position
}

// locate resolves l, the location of an [Error], and returns the node it
// is bound to. A range or a position is the location as it is, and a
// path locates the error at the position of the token it resolves to in
// the document. [anchorOf] joins the base of every Error from [Rebase]
// above that Error in front of the path, so locate resolves the path as
// it is. A path beside a range or a position names the value in the
// message and is not resolved, so a range locates the error whether or
// not the document holds the path. The node is the one b binds with, or,
// when b routes, the root of the document [binder.route] picks for the
// location. An empty l is errUnlocated, a path bound where no document
// resolves it is [ErrPathNeedsDocument], and a path bound through a
// binder that marks its paths ambiguous is [ErrAmbiguousPath].
func locate(b binder, l locus) (location, *Node, error) {
	switch loc := l.loc.(type) {
	case position.Range:
		return location{pos: loc.Start, rng: &loc}, b.nodeAt(loc.Start.Line), nil

	case position.Position:
		return location{pos: loc}, b.nodeAt(loc.Line), nil
	}

	if l.hasPath {
		if b.ambiguous {
			return location{}, b.node, fmt.Errorf("%w: %s", ErrAmbiguousPath, l.path)
		}

		return locatePath(b, l.path)
	}

	return location{}, b.node, errUnlocated
}

// locatePath resolves path from the root of the document of the node b
// binds with, or of the one document of the source when b routes.
// [binder.scoped] put the scope of that node in front of the path
// already, so the path reads from the root. A path that names a key the
// document leaves out resolves to the key of the mapping that lacks it,
// as [Node.nearestLocation] finds it. A source that holds no document,
// holds several, or does not parse has no document to resolve the path
// in, so the location is [ErrPathNeedsDocument] wrapping that reason. A
// document that did not parse has no tree to resolve the path in either,
// so the location is ErrPathNeedsDocument wrapping its syntax error.
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

	root := node.doc.node

	if root.doc.err != nil {
		return location{}, node, fmt.Errorf("%w: %s: %w", ErrPathNeedsDocument, path, root.doc.err)
	}

	loc, err := root.pathLocation(path)
	if err != nil {
		if near, ok := root.nearestLocation(path, err); ok {
			return near, node, nil
		}

		return location{}, node, err
	}

	return loc, node, nil
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
// the message, [SourceError.Message] returns the message alone,
// [SourceError.Range] returns the resolved range, [SourceError.Path] the
// path the error carries, and [SourceError.Excerpt] returns the
// surrounding lines with the location highlighted. [FormatError] prints
// the message as a tree with a branch per nested error, then the excerpt
// as plain text with carets under the locations. Its output is safe for a
// log, however the caller wrapped or joined the error:
//
//	if _, err := source.File(); err != nil {
//		log.Print(niceyaml.FormatError(err, 2))
//	}
//
// A path resolves from the [Node] that bound the error, which for
// [Source.Bind] is the root of the one document of the source. A Node
// from [Node.At] or [Node.Nodes] puts its own path in front of each path
// the error carries. The message and [SourceError.Path] thus read from
// the root of the document whichever Node bound the error, as the
// position beside them does. A path bound through Source.Bind in a
// source that holds none or several resolves nowhere, and the reason is
// [ErrPathNeedsDocument].
//
// The bound error is a tree, and binding binds every node of it. The
// location of the SourceError is that of the first located [Error] along
// the cause chain of the error it binds. The chain follows each wrapper to
// the one error it wraps and ends at an error that unwraps to several,
// such as one from [errors.Join], which carries no location of its own.
// Binding binds every error nested with [WithErrors] in an Error along
// that chain, and every branch of the error that ends it, the same way
// to the same document, and each becomes a child. A wrapper that
// [fmt.Errorf] builds with several %w verbs, such as one from a sentinel
// and a cause, keeps only its branches that carry a location or errors
// nested below them, since its message shows the text of the rest
// already. When one branch remains, the chain goes on through it as it
// does through a wrapper with one %w verb. [SourceError.Errors]
// returns the children, each a SourceError with its own children, if any,
// and its own location when its error carries one. A validator's report
// of several violations therefore binds to one SourceError per violation
// whether it nests them with WithErrors or joins them. An error that is
// or wraps a SourceError, with no Error above it that carries a location
// or nests errors, is a binding already. As a nested error it contributes that
// binding as the child, and as the error given to Bind it comes back as
// it is. A located Error above a binding binds anew at its own location,
// and its message carries the position the inner binding resolved as well
// as its own. An Error above a binding that nests errors binds anew
// around it, with those errors as children.
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
// such as [fmt.Errorf] produced stays as it was, and the position goes
// in front of it. The one part that changes is the path an [Error]
// writes in front of its own message, which a scoped Node joins its
// path in front of. An error built by hand therefore goes through
// [Node.Bind] or [Source.Bind] first, and context around the
// SourceError comes after, so the position and the path stay beside the
// message.
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
// from the [Source] and [Node] methods.
type SourceError struct {
	err    error
	source *Source
	// The node paths resolved from, which is nil for an error the Source
	// bound to no document.
	node *Node
	// The reason the location did not resolve, which is nil when it did:
	// errUnlocated for an error that carries none, the error of a path
	// that does not resolve, or ErrOutOfRange for a location the source
	// does not hold.
	locErr error
	// The binding the first line of the message comes from when Error
	// leaves the message as it is, or nil when Error puts a position or
	// name in front, or when no binding wrote the first line.
	lead *SourceError
	// The bound children: the errors nested with WithErrors along the cause
	// chain and every branch followBranches keeps of the error that ends
	// it by unwrapping to several, located or not.
	errors []*SourceError
	// The message Error returns, built the first time Error asks.
	msg string
	// The ranges the excerpt highlights, the location, resolved when the
	// error was bound, and the range it covers in the source.
	ranges position.Ranges
	loc    location
	rng    position.Range
	// Makes passedLead find lead once, the first time a message asks.
	leadOnce sync.Once
	// Makes Error build msg once.
	msgOnce sync.Once
	// The error wraps a binding, whose location, source, and children this
	// one took over, and whose position its message carries already.
	adopted bool
}

// DefaultContextLines is the number of context lines the %+v verb of an
// [*Error] or a [*SourceError] shows on either side of each marked line.
const DefaultContextLines = 2

// binder is where an error binds: the source, and the node that binds
// it, which is nil for [Source.Bind] and for the errors a Source produces
// itself. A binder that routes picks the document for each location it
// resolves, as [Source.Bind] does; one that does not, as the parser's
// binder must not, since the documents are not built until the parse
// ends, binds to the source alone. A binder that marks its paths
// ambiguous binds errors whose paths may name another value, so it
// resolves no path, for the reason [ErrAmbiguousPath]. It still locates
// a position or a range. A binder that is rooted binds errors whose
// paths read from the root of the document already, as the path of a
// decode rejection does, so it puts no scope in front of them.
type binder struct {
	src       *Source
	node      *Node
	route     bool
	ambiguous bool
	rooted    bool
}

// scoped returns err as a binding of b holds it. An error bound through
// a Node from [Node.At] or [Node.Nodes] writes its paths from that node,
// so it comes back under the scope of the node, as [underScope] returns
// it, and its paths read from the root of the document. An error bound
// through the root of a document, through no node, or through a rooted
// binder comes back as it is.
func (b binder) scoped(err error) error {
	if b.node == nil || b.node.base.IsRoot() || b.rooted {
		return err
	}

	scoped, _ := underScope(err, b.node.base)

	return scoped
}

// underScope returns err with scope in front of each path its message
// carries, and reports whether it changed anything. An error whose cause
// chain reaches an [*Error] that carries a path comes back inside an
// Error from [Rebase] at scope, so its message and [Error.Path] carry
// the joined path. A join, as [joinBranches] finds one, comes back as a
// new join of its branches under scope, as Rebase builds one, so each
// line of its message carries the joined path of its own branch. Rebase
// points an error with no location at its base, and underScope leaves
// such an error as it is, so binding through a scoped Node gives no
// error a location it did not carry. A binding resolved its location
// already and comes back as it is.
func underScope(err error, scope paths.Path) (error, bool) {
	if isNothing(err) {
		return err, false
	}

	// An Error that adds nothing to the join it wraps reads as the join.
	x, ok := err.(*Error) //nolint:errorlint // The node itself, not a chain search.
	if ok && x.addsNothing() {
		if _, joined := joinBranches(x.err); joined {
			inner, changed := underScope(x.err, scope)
			if !changed {
				return err, false
			}

			return WrapError(inner), true
		}
	}

	if branches, ok := joinBranches(err); ok {
		scoped := make([]error, 0, len(branches))
		changed := false

		for _, branch := range branches {
			if branch == nil {
				continue
			}

			s, c := underScope(branch, scope)
			scoped = append(scoped, s)
			changed = changed || c
		}

		switch {
		case !changed:
			return err, false
		case isJoinError(err):
			return errors.Join(scoped...), true
		default:
			return &rebasedJoinError{join: err, branches: scoped}, true
		}
	}

	a := anchorOf(err)
	if _, located := a.err.(*Error); located && a.hasPath { //nolint:errorlint // The anchor itself, found by the walk.
		return &Error{err: err, base: scope, rebased: true}, true
	}

	return err, false
}

// nodeAt returns the node an error on line idx binds to: the one b binds
// with, or, when b routes, the root of the document of the source whose
// span holds the line. That root is nil when the source does not parse or
// the line lies outside it. The spans of the documents run in order and each
// ends where the next starts, so a search for the first span that ends
// past the line finds the one that holds it.
func (b binder) nodeAt(idx int) *Node {
	if b.node != nil || !b.route {
		return b.node
	}

	docs, err := b.src.documents()
	if err != nil {
		return nil
	}

	i := sort.Search(len(docs), func(i int) bool {
		return docs[i].span.End > idx
	})
	if i < len(docs) && docs[i].span.Contains(idx) {
		return docs[i]
	}

	return nil
}

// bindTree binds err to b. A nil err, or a nil [*Error] or [*SourceError]
// pointer, carries nothing to bind and comes back as a nil error. A
// caller thus compares the result against nil whatever the shape of the
// nil it passed. An error that [isBound] reports is a binding already and
// comes back as it is. Any other error, including an Error above a
// binding that carries a location or nests errors, binds as a new
// SourceError.
func bindTree(err error, b binder) error {
	if isNothing(err) {
		return nil
	}

	if isBound(err) {
		return err
	}

	return newSourceError(err, b)
}

// isBound reports whether err is a binding already: a [*SourceError], or
// an error that wraps one along its cause chain with no [*Error] above it
// that carries a location or nests errors with [WithErrors]. The chain
// follows a wrapper with several %w verbs to the one branch
// [followBranches] keeps of it. Such an Error adds to the tree, so the
// error binds anew around the inner binding.
func isBound(err error) bool {
	for cur := err; ; {
		switch x := cur.(type) { //nolint:errorlint // Walks the chain one node at a time.
		case *SourceError:
			return x != nil

		case *Error:
			if x == nil || x.hasLocation() || len(x.errors) > 0 {
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
// since the nested errors of an [*Error] never reach its text. A binding
// that leaves its message untouched puts nothing in front of its first
// line, so when [SourceError.passedLead] finds the binding below it that
// wrote that line, the walk returns that one. An Error that writes a path
// in front leads with the path. An Error from [Rebase], or one that
// carries a position or a range alone, writes the text of
// [Error.textCause] otherwise, so the walk continues there, and any other
// Error writes the text of its cause.
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
			switch {
			case x == nil:
				return nil

			case x.rebased || x.hasLocation():
				if anchorOf(x).hasPath {
					return nil
				}

				cur = x.textCause()

			default:
				cur = x.err
			}

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
// source instead, which still stands for e when no child of e is bound to
// the source of e, as in a join whose every branch is a binding of
// another source. A binding that adopted another follows the same rules,
// since it writes no position of its own. It returns nil for any other e,
// which puts its own position or name in front, and when no binding wrote
// the first line.
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

	lead := leadingBinding(e.err)
	if lead == nil {
		return nil
	}

	if e.source.Name() == "" || lead.Source() == e.source || !holdsSource(e, e.source) {
		return lead
	}

	return nil
}

// keepsMessage reports whether [SourceError.Error] returns the message of
// e as it is, with no position or name in front. A binding that resolved
// a location of its own writes its position. Any other binding keeps the
// message when its source has no name, or when the first line names the
// source already, as [SourceError.passedLead] finds.
func (e *SourceError) keepsMessage() bool {
	if e.locErr == nil && !e.adopted {
		return false
	}

	return e.source.Name() == "" || e.passedLead() != nil
}

// holdsSource reports whether a child of e is bound to src. A child that
// reads as the binding of a join, as [joinBinding] finds one, has no text
// of its own, so the walk looks through it to the branches of that
// binding, as [appendBoundChildren] does.
func holdsSource(e *SourceError, src *Source) bool {
	for _, c := range e.errors {
		joined := joinBinding(c)
		if joined != nil {
			if holdsSource(joined, src) {
				return true
			}

			continue
		}

		if c.Source() == src {
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
// with no anchor below it is the anchor, located at its base. The chain
// follows a wrapper to the one error it wraps and an Error to its cause.
// It follows a wrapper with several %w verbs to the one branch
// [followBranches] keeps of it. Any other error that unwraps to several,
// such as one from [errors.Join], ends the chain. Such an error carries
// no location of its own, and each of its branches binds as a child.
func anchorOf(err error) anchor {
	switch x := err.(type) { //nolint:errorlint // Walks the chain one node at a time.
	case *SourceError:
		if x == nil {
			return anchor{}
		}

		return anchor{err: x}

	case *Error:
		if x == nil {
			return anchor{}
		}

		if x.hasLocation() {
			l := x.locus()
			if x.rebased {
				l = l.rebase(x.base)
			}

			return anchor{err: x, locus: l}
		}

		a := anchorOf(x.err)
		if a.err != nil {
			if x.rebased {
				a.locus = a.rebase(x.base)
			}

			return a
		}

		if x.rebased {
			at := locus{path: x.base, hasPath: true}

			return anchor{err: x, locus: at}
		}

		return anchor{}

	case interface{ Unwrap() error }:
		return anchorOf(x.Unwrap())

	case interface{ Unwrap() []error }:
		_, next := followBranches(err, x.Unwrap())
		if next == nil {
			return anchor{}
		}

		return anchorOf(next)

	default:
		return anchor{}
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
// [binder.scoped] returns it, so the path of an error bound through a
// scoped Node reads from the root of the document. The children of err
// bind the same way, each under the scope on its own, so a child that
// carries no path stays as it is.
func newSourceError(err error, b binder) *SourceError {
	scoped := b.scoped(err)

	e := &SourceError{err: scoped, source: b.src, node: b.node, locErr: errUnlocated}

	found := anchorOf(scoped)

	switch a := found.err.(type) { //nolint:errorlint // The anchor itself, found by the walk.
	case *Error:
		e.loc, e.node, e.locErr = locate(b, found.locus)

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
		if e.locErr == nil {
			e.locErr = checkInRange(e.loc, b.src.lines)
		}

		if e.locErr == nil {
			e.ranges = highlightRanges(b.src.lines, e.loc)
			e.rng = rangeOf(e.ranges, e.loc.pos)
		}
	}

	e.collect(err, b)

	return e
}

// collect binds the children of the error e binds, the ones [walkChildren]
// finds along its cause chain. A [*SourceError] that ends the chain is
// the cause of the error above it rather than a violation of its own, so
// its children join the children of e, and every other child binds
// through [SourceError.addChild].
func (e *SourceError) collect(err error, b binder) {
	walkChildren(err,
		func(x *SourceError) { e.errors = append(e.errors, x.errors...) },
		func(n error, base childBase) { e.addChild(n, b, base) },
	)
}

// addChild binds n as a child of e. A binding is the child as it is, and
// any other error binds where e binds, or takes over the binding it
// wraps. The base is the base of every Error from [Rebase] above n, and
// a child under an Error from Rebase binds as a rebased Error at that
// base, the root included, so its own message and [SourceError.Path]
// carry the joined path as the message of the root does. A nil n, or a
// nil pointer, adds nothing.
func (e *SourceError) addChild(n error, b binder, base childBase) {
	// Rebase returns a binding as it is, so the child is a binding exactly
	// when n is.
	child := base.rebase(n)
	if isNothing(child) {
		return
	}

	if bound, ok := child.(*SourceError); ok { //nolint:errorlint // The node itself, not a chain search.
		e.errors = append(e.errors, bound)

		return
	}

	e.errors = append(e.errors, newSourceError(child, b))
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
// scope a path in the error resolves, or, for an error bound through
// [Source.Bind], the root of the document its location falls in. The
// error wrote its path from that scope, and [SourceError.Path] reports
// the path from the root of the document. A rejection of the go-yaml
// decoder is bound to the Node that decoded, and its path reads from the
// root of the document already. A
// position or a range falls in the document whose [Node.Span] holds its
// line, and a path falls in the one document of the source. The error
// stays bound to that root when its location does not resolve there, as
// with a path that selects nothing or a column before the first.
// Source.Bind binds an error to none when it carries no location or the
// source does not parse. It also binds to none a position or a range on
// a line no document holds, and a path in a source that holds several
// documents or none. An error a [Source] that does not parse produced
// itself is bound to none as well. Node.Bind keeps its node on every
// error it binds anew, except an error that wraps a binding. Such an
// error keeps the node of the binding it wraps, whether Node.Bind
// returns it as it is or binds it anew around the binding with the
// errors it nests, so its node can be nil or belong to another document
// or source. The copy of a Node that a decode hands a [Validator] binds
// errors to that Node, as Validator describes. A nil SourceError is bound
// to none.
func (e *SourceError) Node() *Node {
	if e == nil {
		return nil
	}

	return e.node
}

// Document returns the root [*Node] of the document the error is bound
// to, the one the node [SourceError.Node] returns belongs to, so a caller
// that sorts the errors of a file by document reads its
// [Node.DocumentIndex]. The root is the one [Source.Documents] returns,
// even for an error bound to a Node whose [Node.Document] returns a copy,
// such as one a [Validator] scoped with [Node.At] from the Node a decode
// handed it. An error bound to no node is bound to no document. A nil
// SourceError is bound to none.
func (e *SourceError) Document() *Node {
	if e == nil || e.node == nil {
		return nil
	}

	return e.node.doc.node
}

// Message returns the text of the bound error with no position or path
// in front: the message [NewError] or [WrapError] gave an [*Error],
// without the path [Error.Error] puts before it, or the text of any other
// error as it is. It is the text [SourceError.Excerpt] annotates a
// location with, and the field a structured report such as a JSON line
// or a CI annotation carries beside the position from
// [SourceError.Position] and the path from [SourceError.Path]. Such a
// report walks [ErrorTree.Problems], which yields one node per problem.
// That walk passes over the summary a validator puts above several
// violations, and it yields each error bound to no source, which has no
// binding to read:
//
//	for problem := range niceyaml.NewErrorTree(err).Problems() {
//		bound := problem.Bound
//		if bound == nil {
//			emit("", position.Position{}, problem.Text, paths.Path{})
//
//			continue
//		}
//
//		path, _ := bound.Path()
//		pos, _ := bound.Position()
//		emit(bound.Source().FilePath(), pos, bound.Message(), path)
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
// Text a wrapper such as [fmt.Errorf] added around the Error stays, with
// the path the wrapper wrote in it, as it does everywhere else. A nil
// SourceError has an empty message.
func (e *SourceError) Message() string {
	return e.text()
}

// Path returns the [paths.Path] the bound error is about and true, or the
// zero Path and false when it carries none. The path reads from the root
// of the document, whichever [Node] bound the error. It is the path
// [Error.Path] reports for the [*Error] that gave the binding its
// location, with the base of every [Rebase] on the way joined in front.
// A Node from [Node.At] or [Node.Nodes] joins its own [Node.Path] in
// front of that, so an error written as `$.price` and bound through the
// Node at `$.items[1]` reports `$.items[1].price`. The root of the
// document resolves the result, so it goes to the [Node.Ranges] or the
// [Node.At] of [SourceError.Document]. [paths.Path.CutPrefix] with the
// path of [SourceError.Node] gives back the path as the error wrote it.
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
// locate it. For a bound [*Error] it is the error given to [WrapError],
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
// [errors.As] on the binding searches the errors nested in it as well. On
// a binding that reports several violations it finds the cause of the
// first violation, where Cause returns the error of the binding alone. A
// wrapper such as [fmt.Errorf] ends the walk and is the cause itself, so
// the search above still finds a RuleError the wrapper holds. Cause is
// nil for an Error created from a nil error, and a nil SourceError has no
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

// Errors returns the bound children of the [SourceError]. Each error
// nested with [WithErrors] along the cause chain of the bound error
// becomes a child, and so does each branch of the error that ends the
// chain by unwrapping to several, such as one from [errors.Join]. A
// validator's report of several violations therefore unwraps to one
// child per violation, in the order the validator gave them. A wrapper
// that [fmt.Errorf] builds with several %w verbs keeps only its
// branches that carry a location or errors nested below them, and when
// one remains, the chain goes on through it. Each child carries its own
// children, if any, and a location when its error carries one, so a
// caller checks [SourceError.Range] before it uses the position:
//
//	for _, violation := range bound.Errors() {
//		if rng, ok := violation.Range(); ok {
//			...
//		}
//	}
//
// The slice is a copy, so a caller may keep or sort it. A nil SourceError
// has no children.
func (e *SourceError) Errors() []*SourceError {
	if e == nil {
		return nil
	}

	return slices.Clone(e.errors)
}

// Error returns the message of the bound error with its resolved position
// in front: "name:line:col: $.path: msg" when the error carries a path,
// whether it binds at the path or at a position or range beside it, and
// "name:line:col: msg" for a position or range alone. Any context a
// wrapper added sits between the position and the rest. The name is
// [Source.Name], and the position stands alone as "line:col:" when the
// source has none, so an error from a named file reads as a compiler
// diagnostic that editors and build tools link to the line. An error
// without a location, or one whose location does not resolve, has no
// position to add, and the name then stands alone in front as "name: msg",
// so an error from one file of many still says which file. The message
// comes back as it is when the source has no name, and when the first
// line of the message comes from a binding of the same source, which
// puts the name or position there already. A first line from a binding
// of another source names that source alone. The name then still goes in
// front when a child of the error is bound to this source, and it stays
// out when every child is bound to another, as in a join of bindings
// from other files. A binding inside the message that leaves its own
// message untouched puts nothing in front of the first line, so when a
// binding below it wrote that line, the line counts as coming from that
// one. A SourceError that binds anew around a binding writes no position
// of its own, so it puts the name in front by the same rule.
//
// The message is the text of the bound error, which runs over several
// lines when that text does, as the text of an [errors.Join] and a
// message written with continuation lines both do. The nested errors
// are not part of it. [SourceError.Errors] returns them, and
// [FormatError] and
// [go.jacobcolvin.com/niceyaml/printer.Printer.PrintError] draw them as
// the branches of a tree. The result never includes source lines, so it
// is safe to log or compare. [SourceError.Excerpt] and [FormatError]
// return the annotated source excerpt. Error reads the text of the bound
// error the first time it is called and returns the same message from
// then on. A nil SourceError, as [errors.As] can yield from a chain that
// holds one, has an empty message, as a nil [*Error] does.
func (e *SourceError) Error() string {
	if e == nil {
		return ""
	}

	e.msgOnce.Do(func() { e.msg = e.buildError() })

	return e.msg
}

// buildError builds the message [SourceError.Error] returns. The message
// of each binding holds the messages of the bindings below it, and the
// walk for the first line reads them again. A message built anew for
// every call would rebuild the bindings below once per binding above
// them, so Error builds it once and keeps it.
func (e *SourceError) buildError() string {
	msg := e.err.Error()
	name := e.source.Name()

	switch {
	case e.locErr == nil && !e.adopted:
		return prefix(formatPosition(name, e.loc.pos), msg)

	case e.keepsMessage():
		return msg

	default:
		return prefix(name+":", msg)
	}
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

// prefix returns p and msg separated by a space, or p alone when msg is
// empty.
func prefix(p, msg string) string {
	if msg == "" {
		return p
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
// message of an [*Error] without the location it puts in front, since the
// caret marks it, or the message of any other error as it is. An Error
// that wraps a binding e adopted annotates with the text of that binding,
// the one [textBinding] finds, since the caret marks the location of the
// binding.
func (e *SourceError) text() string {
	if e == nil {
		return ""
	}

	e = textBinding(e)

	if x, ok := e.err.(*Error); ok { //nolint:errorlint // The node itself, not a chain search.
		return x.message()
	}

	return e.err.Error()
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

	for _, c := range e.errors {
		if !c.all(seen, yield) {
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
// view with [SourceError.Annotate] walks [AllBindings], which yields the
// bindings below each one too. A caller that lists the problems of an
// error walks [ErrorTree.Problems]. A nil err has no bindings.
func Bindings(err error) iter.Seq[*SourceError] {
	return func(yield func(*SourceError) bool) {
		eachBinding(err, yield)
	}
}

// eachBinding calls visit for each [*SourceError] reached through the
// wrappers and joins around err, in depth-first order, and stops when
// visit reports false. It does not look below a binding, whose children
// visit reaches through the binding itself. Reports whether every visit
// wanted more.
func eachBinding(err error, visit func(*SourceError) bool) bool {
	switch x := err.(type) { //nolint:errorlint // Walks the tree one node at a time.
	case *SourceError:
		if x == nil {
			return true
		}

		return visit(x)

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
// each binds to. A binding the tree reaches twice, such as one bound before
// [WithErrors] nested it under another and joined beside that one, comes
// once. It is the walk that marks a view, since [SourceError.Annotate]
// marks one binding:
//
//	view := source.View()
//	for bound := range niceyaml.AllBindings(err) {
//		bound.Annotate(view)
//	}
//
// A report that lists the problems of an error walks
// [ErrorTree.Problems] instead, as the example on [SourceError.Message]
// shows. AllBindings yields a summary beside the violations under it and
// each reason beside the error it explains, and it passes over every
// error bound to no source. A nil err has no bindings.
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
// such as [fmt.Errorf] around a SourceError formats as its own message,
// so a program that holds any error calls FormatError. Every other verb
// formats [SourceError.Error] as it formats a string, with the width,
// precision, and flags given, so %q quotes the message and %-20v pads it.
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
// nested error under it behind its own position, and no source excerpt.
// A handler that logs an error by [SourceError.Error], as
// [slog.JSONHandler] does, shows every nested error this way, and one
// that formats it with %+v, as [slog.TextHandler] does, keeps the
// excerpt out of the attribute. A wrapper such as [fmt.Errorf] around a
// SourceError logs as its own message, so a program that holds any
// error, or wants the excerpt in a log, logs [FormatError] as a string.
func (e *SourceError) LogValue() slog.Value {
	return slog.StringValue(logTree(e))
}

// FormatError renders err as plain text for a log or a terminal without
// color: its message as a tree, then the excerpt of every error bound to
// a source in it. The tree is [NewErrorTree], with a connector in front
// of each nested error, so a validator's report reads as its summary
// with one branch per violation, each behind the position its location
// resolved to:
//
//	cafe.yaml: 2 schema violations
//	|-- 6:8: $.spec.sla: string does not match pattern
//	`-- 22:11: $.spec.hours.days: expected "array", got "string"
//
// The excerpts follow, one per source the bindings in err touch, as
// [Excerpts] yields them. An error joined from one binding per document
// of a file thus shows that file once, with the errors of every document
// on it. A binding whose nested errors point into another file shows an
// excerpt of that file too. Each excerpt keeps context lines of
// unchanged content on either side of each marked line and renders as
// [line.View.String] renders a view. Each line sits behind its number,
// with carets under the columns of every location on the row below and
// the message of each nested error beside its caret. Among several
// bindings, the message of each binding sits beside its own caret too.
// When the bindings touch more than one source, the name of its source
// leads each excerpt on a row of its own. A negative context shows the
// marked lines alone. Blank lines separate the parts.
//
// A line starting "no excerpt:" follows the excerpts for each binding
// [Bindings] finds whose tree resolves no location, with the reason the
// location of the binding itself did not resolve. A binding that carries
// no location of its own gets no such line. A binding whose own location
// does not resolve but whose nested errors do gets their excerpts and no
// reason, and its message stays in the tree without a position.
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
// An error that binds to no source renders as its tree alone, which for
// an error with nothing nested is its message. An error whose tree and
// excerpts both render nothing, such as a bound join of typed-nil errors,
// renders its message in their place, with control characters as their
// pictures like any other.
// [go.jacobcolvin.com/niceyaml/printer.Printer.PrintError] renders the
// same tree and excerpts with styles. A nil err renders as "".
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
// describes.
// The range is in the coordinates of the view [Source.Lines] returns, where
// line 0 is line 1 of the text.
//
// Binding resolved the location, so Range reads the result, and reports
// false when there is none: the error carries no location, as one from
// [fmt.Errorf] does, or its location did not resolve, for the reason
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
// all, which is the ordinary case for an error from [fmt.Errorf] and
// nothing to explain. The reason is [ErrOutOfRange] for a location on a
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
// A nil SourceError has nothing to resolve.
func (e *SourceError) Unresolved() error {
	if e == nil || errors.Is(e.locErr, errUnlocated) {
		return nil
	}

	return e.locErr
}

// Nearest returns the path of the mapping the error is bound at, and
// true, when the path the error carries names a key the document leaves
// out. An error about a missing value, such as a required field, carries
// the path the value would have, and that path selects nothing. Binding
// locates such an error at the key of the mapping that lacks the value,
// the nearest node above the path that the document holds:
//
//	niceyaml.NewError("name is required", niceyaml.AtPath(paths.Root().Child("server", "name")))
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
// approximate location from an exact one checks Nearest. It reports
// false for an error bound at the node its path selects, for one with no
// path, and for one whose location did not resolve. A nil SourceError
// reports false.
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

// Annotate marks the error on view, which holds lines of the source the
// error is bound to. Annotate highlights the location of the error with
// [kind.GenericError] and adds its message from [SourceError.Message] as
// an annotation below its line in [kind.TextError], so the message reads
// as error text without the highlight of the token it describes. The
// annotation holds each tab of the message as four spaces, as the tree
// [FormatError] prints spells it. Annotate leaves the nodes below the
// error to their own Annotate, so a viewer that shows a document with
// its errors in place marks its view with every binding [AllBindings]
// yields and renders it as it is:
//
//	view := source.View()
//	for bound := range niceyaml.AllBindings(err) {
//		bound.Annotate(view)
//	}
//
// A line several errors mark carries an annotation for each, which
// [line.View.String] and the printer draw on one row joined by "; " in
// column order, whatever order the bindings come in. An error with no
// message marks its line with an annotation below it with no content. A
// renderer that draws marks from annotations, as the printer does, draws
// that annotation as a caret run under the highlight, so the range shows
// its extent without color. The annotation starts at the first column the
// highlight covers on its line, so a position on the spaces around a token
// puts the message under the token. A location whose highlight leaves out
// its own line gets an overlay of no width at its column on that line. Such
// locations include a position past the end of a line and a path to an
// empty value. They also include a position on a line of only spaces inside
// a block scalar, and a range that starts at the end of its first line. The
// overlay renders nothing and still counts as decoration, so
// [line.View.Hunks] keeps the line. Annotate moves a column past the end of
// its line to the column after its last rune, for the overlay and the
// message alike, so a renderer spends at most one cell past the line on the
// mark. [SourceError.Error] still reports the column as given.
//
// Annotate finds each line by identity rather than by index, since every
// view over a source shares its [*line.Line] values, so the view may be
// the whole source from [Source.View], a slice of it from [line.View.Slice]
// such as one document of a file, or a diff that interleaves the source
// with another revision. An error marks only the lines of its own source
// that the view holds, and Annotate skips a line the view does not hold.
// A unified diff from [go.jacobcolvin.com/niceyaml/diff.Result.Unified]
// takes its unchanged lines from the after revision, so it holds every
// line of that revision but only the deleted lines of the before
// revision. An error bound to the before revision therefore marks
// nothing on a line the diff left unchanged.
// [go.jacobcolvin.com/niceyaml/diff.Result.Before] holds every line of
// the before revision.
//
// Annotate reports whether it marked any line. It reports false when the
// location did not resolve, for the reason [SourceError.Unresolved]
// gives, or when the view holds none of the lines the location falls on.
//
// [SourceError.Excerpt] marks the whole tree of the error on a fresh view
// of its source, with the message of each node below the root beside its
// line, for the excerpt under the tree [FormatError] prints.
func (e *SourceError) Annotate(view *line.View) bool {
	if e == nil || e.locErr != nil {
		return false
	}

	return annotate(view, e.source, []errorPosition{{
		pos:     e.loc.pos,
		ranges:  e.ranges,
		message: e.text(),
	}})
}

// Excerpt returns a [line.View] of the source the error is bound to
// around the locations of its tree, with each one highlighted. Excerpt
// marks a fresh [Source.View] with the location of every node in the
// tree and the message of each node below the root as an annotation
// below its own line, with each tab as four spaces, as
// [SourceError.Annotate] adds it. The root's own location gets a caret
// run alone, since the tree [FormatError] prints above the excerpt names
// the root.
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
// summary over nested errors, whose children from [SourceError.Errors]
// each name their own reason. Excerpt leaves out a node whose location
// does not resolve. Its message is still part of the tree [FormatError]
// prints. A nil SourceError carries no location.
func (e *SourceError) Excerpt(context int) (*line.View, bool) {
	if e == nil {
		return nil, false
	}

	_, positions := excerptPositions([]*SourceError{e})
	view := e.source.View()

	if !annotate(view, e.source, positions[e.source]) {
		return nil, false
	}

	return view.Hunks(context), true
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

// yieldExcerpts yields the excerpt of each of sources that positions marks
// a line of, in order: a fresh view of the source with its positions
// marked, cut to the hunks around them with context lines.
func yieldExcerpts(
	sources []*Source,
	positions map[*Source][]errorPosition,
	context int,
	yield func(*Source, *line.View) bool,
) {
	for _, src := range sources {
		view := src.View()

		if !annotate(view, src, positions[src]) {
			continue
		}

		if !yield(src, view.Hunks(context)) {
			return
		}
	}
}

// errorDetails returns what the tree of err leaves out: each excerpt
// [Excerpts] yields with context lines, which [line.View.String] renders
// as plain text, then a line starting "no excerpt:" for each binding
// [Bindings] finds whose tree marks nothing, with the reason
// [SourceError.Unresolved] returns. A binding that carries no location
// has nothing to explain. When the bindings touch more than one source,
// the name of its source leads each excerpt on a row of its own, so the
// reader tells the excerpts apart. Returns nothing when there is nothing
// to show. The printer renders the same parts with its styles.
func errorDetails(err error, context int) []string {
	bindings := slices.Collect(Bindings(err))
	sources, positions := excerptPositions(bindings)

	var parts []string

	yieldExcerpts(sources, positions, context, func(src *Source, excerpt *line.View) bool {
		part := excerpt.String()

		// The name is the caller's text, so its control characters render
		// as pictures like those of the tree.
		if len(sources) > 1 && src.Name() != "" {
			part = escape.Control(escape.Tabs(src.Name())) + "\n" + part
		}

		parts = append(parts, part)

		return true
	})

	for _, bound := range bindings {
		if bound.marks() {
			continue
		}

		// The reason names the path, which a key of the document spells,
		// so its control characters render as pictures like those of the
		// tree. A tab in the key becomes four spaces, as it does in the
		// tree.
		reason := bound.Unresolved()
		if reason != nil {
			parts = append(parts, "no excerpt: "+escape.Control(escape.Tabs(reason.Error())))
		}
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
// [line.View.Hunks] keeps. The annotation below a line starts at the first
// column the highlight of its position covers on that line, where
// [markUnannotated] starts a caret run, so a position on the spaces around
// a token puts its message under the token. A position with no highlight on
// its line keeps its column. A column past the end of its line moves to the
// column after its last rune, in the overlay and in the annotation below
// the line. A far column then costs a renderer no more cells than a column
// at the end.
func annotateSource(view *line.View, src *Source, positions []errorPosition) []int {
	if len(positions) == 0 {
		return nil
	}

	index := src.lineIndex(view)

	var marked []int

	notes := make([]errorPosition, 0, len(positions))

	for _, pos := range positions {
		var segments []position.Range

		for _, r := range pos.ranges {
			segments = append(segments, src.lines.SliceLines(r)...)
		}

		start, onLine := highlightStart(pos.pos, segments)

		note := pos
		note.pos.Col = start
		notes = append(notes, note)

		if i, ok := index(pos.pos.Line); ok {
			marked = append(marked, i)

			if !onLine {
				col := min(pos.pos.Col, src.lines.Line(pos.pos.Line).Width())
				view.AddLineOverlay(i, line.Overlay{
					Cols: position.NewSpan(col, col),
					Kind: kind.GenericError,
				})
			}
		}

		// [line.Lines.SliceLines] already clamped each segment to its
		// line, which the view holds at index i.
		for _, lr := range segments {
			if i, ok := index(lr.Start.Line); ok {
				view.AddLineOverlay(i, line.Overlay{
					Cols: position.NewSpan(lr.Start.Col, lr.End.Col),
					Kind: kind.GenericError,
				})

				marked = append(marked, i)
			}
		}
	}

	if len(marked) == 0 {
		return nil
	}

	for lineIdx, annotation := range prepareLineAnnotations(notes) {
		if i, ok := index(lineIdx); ok {
			annotation.Col = min(annotation.Col, src.lines.Line(lineIdx).Width())
			view.Annotate(i, annotation)
		}
	}

	return marked
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

// excerptPositions returns the sources the trees of bindings touch, with
// the resolved locations of the nodes bound to each, from one walk of
// each tree. The sources come in the order the walk reaches them: the
// source of each binding, then the source of each node below it, in
// depth-first order. An excerpt per source thus comes out in that order.
// The locations of each source come in the same order, and a node whose
// location did not resolve adds none. A node the trees reach twice adds
// its location once.
//
// Every node below a binding carries its message. The own location of a
// binding carries it only among several bindings, where the message tells
// the carets of one excerpt apart. The location of a lone binding carries
// none, so an excerpt gives it a caret run alone. A nil binding touches
// no source.
func excerptPositions(bindings []*SourceError) ([]*Source, map[*Source][]errorPosition) {
	var sources []*Source

	touched := make(map[*Source]bool)
	positions := make(map[*Source][]errorPosition)
	seen := make(map[*SourceError]bool)
	labeled := len(bindings) > 1

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

// prepareLineAnnotations prepares annotations grouped by line index. It
// includes only positions with messages. Each line joins its messages in
// column order, so they read in the order of the carets, and messages at
// the same column keep the order they arrived in. A message that repeats
// at one column reads once. Several errors say the same of one value when
// a schema states a constraint twice, or when a value fails two branches
// of an anyOf the same way. Each tab in a message becomes four spaces, as
// [escape.Tabs] returns it, so the annotation spells the message as the
// tree of [FormatError] does. A renderer draws the other control
// characters of an annotation as their pictures.
func prepareLineAnnotations(positions []errorPosition) map[int]line.Annotation {
	linePositions := make(map[int][]errorPosition)

	for _, pos := range positions {
		if pos.message != "" {
			linePositions[pos.pos.Line] = append(linePositions[pos.pos.Line], pos)
		}
	}

	result := make(map[int]line.Annotation)

	for lineIdx, lineErrs := range linePositions {
		slices.SortStableFunc(lineErrs, func(a, b errorPosition) int {
			return cmp.Compare(a.pos.Col, b.pos.Col)
		})

		messages := make([]string, 0, len(lineErrs))

		for i, r := range lineErrs {
			repeats := slices.ContainsFunc(lineErrs[:i], func(prev errorPosition) bool {
				return prev.pos.Col == r.pos.Col && prev.message == r.message
			})
			if !repeats {
				messages = append(messages, escape.Tabs(r.message))
			}
		}

		result[lineIdx] = line.Annotation{
			Content:   strings.Join(messages, "; "),
			Kind:      kind.TextError,
			Placement: line.Below,
			Col:       lineErrs[0].pos.Col,
		}
	}

	return result
}
