package niceyaml

import (
	"cmp"
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

	"github.com/goccy/go-yaml/token"

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
	// one was expected, such as a file holding only a "..." marker.
	// [Source.Document] returns it.
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
	// The error comes back bound as a [SourceError] at the offending
	// token. For a value the document leaves out, such as the value of a
	// key with nothing after its colon, that is the null the parser puts
	// there, which an error at the path of the value binds to as well.
	// An error a value's own UnmarshalYAML returns, and the error
	// of a context that ended, come back as they are and do not match.
	// A panic in the go-yaml decoder or in a value's own UnmarshalYAML
	// does match, bound at the first token of the node that is not a
	// comment, with no go-yaml error in the chain. So does a value nested
	// deeper than the decoder allows, bound the same way, and a `<<`
	// merge key whose alias names no anchor before it, or an anchor that
	// holds the merge key, bound at the alias. The decoder reports those
	// two without a location, and the chain holds its error.
	ErrDecodeRejected = errors.New("decoder rejected the value")

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
	// against.
	ErrPathNeedsDocument = errors.New("path needs a document to resolve in")

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
	loc    any
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
// a field.
//
// An error joined from several, as [errors.Join] builds one, rebases
// branch by branch into a new join, so each line of its message carries
// the path of its own branch. A join whose every branch is a nil
// [*Error] or [*SourceError] pointer is still an error, and it points at
// base as an error with no location does.
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

	if branches, ok := joinBranches(err); ok {
		rebased := make([]error, 0, len(branches))
		for _, branch := range branches {
			r := Rebase(branch, base)
			if r != nil {
				rebased = append(rebased, r)
			}
		}

		if len(rebased) > 0 {
			return errors.Join(rebased...)
		}
	}

	return &Error{err: err, base: base, rebased: true}
}

// ErrorOption configures an [Error]. [AtPath] sets its path, [AtPosition]
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
// [Node.At] writes its paths from the value. A path from [Node.Path] is
// absolute, so it binds through the root of the document or through
// [Source.Bind], and a scoped Node joins it under its own path again.
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
		e.errors = append(e.errors, errs...)
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

	var msg string

	switch {
	case e.rebased:
		msg = e.message()

		if a := anchorOf(e); a.hasPath {
			msg = prefix(a.path.String()+":", msg)
		}

		return msg

	case e.hasLocation():
		msg = e.message()

	case e.err != nil:
		msg = e.err.Error()
	}

	if e.hasPath {
		msg = prefix(e.path.String()+":", msg)
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
	return slog.StringValue(renderErrorTree(NewErrorTree(e)))
}

// nested returns the nested errors of e that are not nothing, in the
// order [WithErrors] received them.
func (e *Error) nested() []error {
	out := make([]error, 0, len(e.errors))

	for _, n := range e.errors {
		if !isNothing(n) {
			out = append(out, n)
		}
	}

	return out
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

	return append(result, e.nested()...)
}

// Cause returns the error the [Error] wraps: the error given to
// [WrapError], or one holding the message given to [NewError]. It is
// nil for an Error created from a nil error, and a nil Error has no cause.
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

	return e.nested()
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
// and the range to highlight when the error carried one.
type location struct {
	rng *position.Range
	pos position.Position
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
// location. An empty l is errUnlocated, and a path bound where no
// document resolves it is [ErrPathNeedsDocument].
func locate(b binder, l locus) (location, *Node, error) {
	switch loc := l.loc.(type) {
	case position.Range:
		return location{pos: loc.Start, rng: &loc}, b.nodeAt(loc.Start.Line), nil

	case position.Position:
		return location{pos: loc}, b.nodeAt(loc.Line), nil
	}

	if l.hasPath {
		return locatePath(b, l.path)
	}

	return location{}, b.node, errUnlocated
}

// locatePath resolves path from the node b binds with, or from the root
// of the one document of the source when b routes. A source that holds
// none, holds several, or does not parse has no document to resolve the
// path in, so the location is [ErrPathNeedsDocument] wrapping that
// reason.
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

	pos, err := node.position(path)
	if err != nil {
		return location{}, node, err
	}

	return location{pos: pos}, node, nil
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
// [Source.Bind] is the root of the one document of the source. A path
// bound through Source.Bind in a source that holds none or several
// resolves nowhere, and the reason is [ErrPathNeedsDocument].
//
// The bound error is a tree, and binding binds every node of it. The
// location of the SourceError is that of the first located [Error] along
// the cause chain of the error it binds. The chain follows each wrapper to
// the one error it wraps and ends at an error that unwraps to several,
// such as one from [errors.Join], which carries no location of its own.
// Binding binds every error nested with [WithErrors] in an Error along
// that chain, and every branch of the error that ends it, the same way
// to the same document, and each becomes a child. [SourceError.Errors]
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
// A location that does not resolve, such as a path the document does not
// hold or a position on a line the source does not have, costs the
// SourceError its position. An error that carries no location never had
// one. [SourceError.Error] then puts the name of the source alone in
// front of the message, [SourceError.Range] reports false, and
// [SourceError.Unresolved] returns the reason for the first case and nil
// for the second.
//
// A SourceError never rewrites the message of the error it binds. The text
// a wrapper such as [fmt.Errorf] produced stays as it was, and the position
// goes in front of it. An error built by hand therefore goes through
// [Node.Bind] or [Source.Bind] first, and context around the
// SourceError comes after, so the position stays beside the message.
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
//	fmt.Println(p.PrintError(err))
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
	// The bound children: the errors nested with WithErrors along the cause
	// chain and every branch of the error that ends it by unwrapping to
	// several, located or not.
	errors []*SourceError
	// The ranges the excerpt highlights, the location, resolved when the
	// error was bound, and the range it covers in the source.
	ranges position.Ranges
	loc    location
	rng    position.Range
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
// ends, binds to the source alone.
type binder struct {
	src   *Source
	node  *Node
	route bool
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
// that carries a location or nests errors with [WithErrors]. Such an Error
// adds to the tree, so the error binds anew around the inner binding.
func isBound(err error) bool {
	for cur := err; ; {
		switch x := cur.(type) { //nolint:errorlint // Walks the chain one node at a time.
		case *SourceError:
			return x != nil

		case *Error:
			if x == nil || x.hasLocation() || len(x.nested()) > 0 {
				return false
			}

			cur = x.err

		case interface{ Unwrap() error }:
			cur = x.Unwrap()

		default:
			return false
		}
	}
}

// leadingBinding returns the binding the first line of the message of err
// comes from, which puts the name or position of its own source there, or
// nil when the first line comes from no binding. The walk follows the
// text [Error.Error] writes rather than the structure [isBound] reads,
// since the nested errors of an [*Error] never reach its text. An Error
// that writes a path in front leads with the path. An Error from
// [Rebase], or one that carries a position or a range alone, writes the
// text of [Error.textCause] otherwise, so the walk continues there, and
// any other Error writes the text of its cause. At an error that unwraps
// to several, the walk continues with its first branch that is not nil,
// since that branch supplies the first line of the text of an
// [errors.Join].
func leadingBinding(err error) *SourceError {
	for cur := err; ; {
		switch x := cur.(type) { //nolint:errorlint // Walks the chain one node at a time.
		case *SourceError:
			return x

		case *Error:
			switch {
			case x == nil:
				return nil

			case x.rebased:
				if anchorOf(x).hasPath {
					return nil
				}

				cur = x.textCause()

			case x.hasPath:
				return nil

			case x.hasLocation():
				cur = x.textCause()

			default:
				cur = x.err
			}

		case interface{ Unwrap() error }:
			cur = x.Unwrap()

		case interface{ Unwrap() []error }:
			cur = nil

			for _, branch := range x.Unwrap() {
				if !isNothing(branch) {
					cur = branch

					break
				}
			}

		default:
			return nil
		}
	}
}

// leadNamesSource reports whether the first line of the message of e
// names the source of e already, so [SourceError.Error] puts no name in
// front. A binding of the same source that supplies the first line names
// it. One of another source names that source instead, which still
// stands for e when no child of e is bound to the source of e, as in a
// join whose every branch is a binding of another source.
func (e *SourceError) leadNamesSource() bool {
	lead := leadingBinding(e.err)
	if lead == nil {
		return false
	}

	return lead.Source() == e.source || !holdsSource(e, e.source)
}

// holdsSource reports whether a child of e is bound to src. A child that
// binds a join has no text of its own, so the walk looks through it to
// its branches, as [boundChildren] does.
func holdsSource(e *SourceError, src *Source) bool {
	for _, c := range e.errors {
		if _, joined := joinBranches(c.Unwrap()); joined {
			if holdsSource(c, src) {
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
// An error that unwraps to several, such as one from [errors.Join], ends
// the chain. Such an error carries no location of its own, and each of
// its branches binds as a child.
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
// resolving in the document of b. The children of err bind the same way.
func newSourceError(err error, b binder) *SourceError {
	e := &SourceError{err: err, source: b.src, node: b.node, locErr: errUnlocated}

	found := anchorOf(err)

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
// [Source.Bind], the root of the document its location falls in. An
// error bound through Source.Bind without a location, or with one that
// resolves in no document, and one a [Source] that does not parse
// produced itself are bound to none. Node.Bind keeps its node on every
// error it binds anew, except an error that wraps a binding. Such an
// error keeps the node of the binding it wraps, whether Node.Bind
// returns it as it is or binds it anew around the binding with the
// errors it nests, so its node can be nil or belong to another document
// or source. A nil SourceError is bound to none.
func (e *SourceError) Node() *Node {
	if e == nil {
		return nil
	}

	return e.node
}

// Document returns the root [*Node] of the document the error is bound
// to, the one the node [SourceError.Node] returns belongs to, so a caller
// that sorts the errors of a file by document reads its
// [Node.DocumentIndex]. An error bound to no node is bound to no
// document. A nil SourceError is bound to none.
func (e *SourceError) Document() *Node {
	if e == nil {
		return nil
	}

	return e.node.Document()
}

// Message returns the text of the bound error with no position or path
// in front: the message [NewError] or [WrapError] gave an [*Error],
// without the path [Error.Error] puts before it, or the text of any other
// error as it is. It is the text [SourceError.Excerpt] annotates a
// location with, and the field a structured report such as a JSON line
// or a CI annotation carries beside the position from
// [SourceError.Range] and the path from [SourceError.Path]. Such a
// report walks every binding in the tree with [AllBindings]. A validator
// that found several violations reports them as the children of one
// binding with no location of its own, and a binding whose location did
// not resolve has no range but still names a violation:
//
//	for bound := range niceyaml.AllBindings(err) {
//		path, _ := bound.Path()
//		if rng, ok := bound.Range(); ok {
//			emit(bound.Source().FilePath(), rng.Start, bound.Message(), path)
//		} else {
//			emit(bound.Source().FilePath(), position.Position{}, bound.Message(), path)
//		}
//	}
//
// The report carries the start of the resolved range. For a position an
// error gave with [AtPosition] inside a token, that start marks where the
// content of the token starts, and it can differ from the position
// [SourceError.Error] reports, as [SourceError.Range] describes.
//
// Text a wrapper such as [fmt.Errorf] added around the Error stays, with
// the path the wrapper wrote in it, as it does everywhere else. A nil
// SourceError has an empty message.
func (e *SourceError) Message() string {
	return e.text()
}

// Path returns the [paths.Path] the bound error is about and true, or the
// zero Path and false when it carries none. It is the path [Error.Path]
// reports for the [*Error] that gave the binding its location, with the
// base of every [Rebase] on the way joined in front. An error bound
// through a scoped [Node] thus reports the path as the error wrote it,
// from the scope. An error that carries a range or a position beside its path
// binds at that location and reports the path as written, resolved or
// not. A binding that wraps another reports the path of the one it
// wraps. A nil SourceError has none.
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

// Errors returns the bound children of the [SourceError]. Each error
// nested with [WithErrors] along the cause chain of the bound error
// becomes a child, and so does each branch of the error that ends the
// chain by unwrapping to several, such as one from [errors.Join]. A
// validator's report of several violations therefore unwraps to one
// child per violation, in the order the validator gave them. Each child
// carries its own children, if any, and a location when its error carries
// one, so a caller checks [SourceError.Range] before it uses the
// position:
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
// from other files.
//
// The message is the text of the bound error, which runs over several
// lines when that text does, as the text of an [errors.Join] and a
// message written with continuation lines both do. The nested errors
// are not part of it. [SourceError.Errors] returns them, and
// [FormatError] and
// [go.jacobcolvin.com/niceyaml/printer.Printer.PrintError] draw them as
// the branches of a tree. The result never includes source lines, so it
// is safe to log or compare. [SourceError.Excerpt] and [FormatError]
// return the annotated source excerpt. A nil SourceError, as [errors.As] can
// yield from a chain that holds one, has an empty message, as a nil
// [*Error] does.
func (e *SourceError) Error() string {
	if e == nil {
		return ""
	}

	msg := e.err.Error()
	name := e.source.Name()

	switch {
	case e.adopted:
		return msg

	case e.locErr == nil:
		return prefix(formatPosition(name, e.loc.pos), msg)

	case name != "" && !e.leadNamesSource():
		return prefix(name+":", msg)

	default:
		return msg
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

// boundCause returns the [*SourceError] x reaches through the causes of
// Errors alone, or nil when a wrapper of another kind or the end of the
// chain comes first.
func boundCause(x *Error) *SourceError {
	for cur := x; cur != nil; {
		switch next := cur.err.(type) { //nolint:errorlint // Identity of the direct child, not a chain search.
		case *Error:
			cur = next
		case *SourceError:
			return next
		default:
			return nil
		}
	}

	return nil
}

// walk calls visit for every node below e in depth-first order, each
// once however many times the tree reaches it, as [AllBindings] yields
// them. A nil e has no nodes below it.
func (e *SourceError) walk(visit func(*SourceError)) {
	e.all(map[*SourceError]bool{}, func(n *SourceError) bool {
		if n != e {
			visit(n)
		}

		return true
	})
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
// binding as one unit. [FormatError] walks it to print one tree and one
// set of excerpts per binding, as for an error joined from one binding
// per document of a file:
//
//	for bound := range niceyaml.Bindings(err) {
//		for src, excerpt := range bound.Excerpts(2) {
//			fmt.Println(src.Name())
//			fmt.Println(excerpt)
//		}
//	}
//
// A caller that marks a view with [SourceError.Annotate], or that wants
// one entry per error, the nodes below each binding included, walks
// [AllBindings] instead. A nil err has no bindings.
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
// It is also the walk a structured report makes, one that emits a row for
// each error with a location of its own, as the example on
// [SourceError.Message] shows. A nil err has no bindings.
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
	return slog.StringValue(renderErrorTree(NewErrorTree(e)))
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
// The excerpts follow, one per binding [Bindings] finds, each
// rendered as [SourceError.Excerpt] with context lines of unchanged
// content on either side of each marked line, as [line.View.String]
// renders a view: each line behind its number, carets under the columns
// of every location on the row below, and the message of each nested
// error beside its caret. A negative context shows the marked lines
// alone. Blank lines separate the parts. A binding whose location does
// not resolve prints a line starting "no excerpt:" that names the reason
// in place of its excerpt, unless it carries no location at all. The
// output holds no escape sequences, so it reads in a log as it does in a
// terminal.
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

	for bound := range Bindings(err) {
		parts = append(parts, bound.details(context)...)
	}

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
// Control characters in a text render as their pictures, so a key of the
// document that holds an escape sequence cannot reach the terminal, and
// the output holds no escape sequences as [FormatError] promises.
func renderErrorTree(t ErrorTree) string {
	var sb strings.Builder

	if t.Text != "" {
		sb.WriteString(escape.Rows(t.Text))
	}

	writeErrorBranches(&sb, t.Children, "", t.Text != "")

	return sb.String()
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

		for j, row := range strings.Split(child.Text, "\n") {
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
// The range is in the coordinates of the view [Source.Lines] returns, where
// line 0 is line 1 of the text.
//
// Binding resolved the location, so Range reads the result, and reports
// false when there is none: the error carries no location, as one from
// [fmt.Errorf] does, or its location did not resolve, for the reason
// [SourceError.Unresolved] returns. An error whose Range reports false
// has no position in [SourceError.Error] and no excerpt. A nil
// SourceError carries no location.
func (e *SourceError) Range() (position.Range, bool) {
	if e == nil || e.locErr != nil {
		return position.Range{}, false
	}

	return e.rng, true
}

// Unresolved returns why the location of the bound error did not
// resolve, and nil when it did or when the error carries no location at
// all, which is the ordinary case for an error from [fmt.Errorf] and
// nothing to explain. The reason is [ErrOutOfRange] for a location on a
// line the source does not hold or at a column before the first,
// [ErrPathNeedsDocument] for a path bound through [Source.Bind] in a
// source with no single document, the resolution error from
// [go.jacobcolvin.com/niceyaml/paths] for a path the document does not
// hold, or [ErrNoLocation] for a path whose token carries no position. A
// renderer names it in place of the excerpt:
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
// as error text without the highlight of the token it describes. Annotate
// leaves the nodes below the error to their own Annotate, so a viewer
// that shows a document with its errors in place marks its view with
// every binding [AllBindings] yields and renders it as it is:
//
//	view := source.View()
//	for bound := range niceyaml.AllBindings(err) {
//		bound.Annotate(view)
//	}
//
// A line several errors mark carries an annotation for each, which
// [line.View.String] and the printer draw on one row joined by "; ". An
// error with no message marks its line with an annotation below it with
// no content. A renderer that draws marks from annotations, as the
// printer does, draws that annotation as a caret run under the
// highlight, so the range shows its extent without color. A location
// with no token under it, such as a position past the end of a line or a
// path to an empty value, gets an overlay of no width at its column. The
// overlay renders nothing and still counts as decoration, so
// [line.View.Hunks] keeps the line. Annotate moves a column past the end
// of its line to the column after its last rune, for the overlay and the
// message alike, so a renderer spends at most one cell past the line on
// the mark.
// [SourceError.Error] still reports the column as given.
//
// Annotate finds each line by identity rather than by index, since every
// view over a source shares its [*line.Line] values, so the view may be
// the whole source from [Source.View], a slice of it from [line.View.Slice]
// such as one document of a file, or a diff that interleaves the source
// with another revision. An error bound to another source marks the
// lines of that source the view holds, so a diff of two revisions shows
// the errors of both, and a view of one source shows the errors bound to
// it. Annotate skips a line the view does not hold.
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

	marked := annotateSource(view, e.source, []errorPosition{{
		pos:     e.loc.pos,
		ranges:  e.ranges,
		message: e.text(),
	}})

	markUnannotated(view, marked)

	return len(marked) > 0
}

// Excerpt returns a [line.View] of the source the error is bound to
// around the locations of its tree, with each one highlighted. Excerpt
// marks a fresh [Source.View] with the location of every node in the
// tree and the message of each node below the root as an annotation
// below its own line. The root's own location gets a caret run alone,
// since the tree [FormatError] prints above the excerpt names the root.
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
// resolves. [SourceError.Unresolved] then names why the location of the
// error itself did not resolve, and returns nil for an error that
// carries no location of its own, such as a join or a summary over
// nested errors, whose children from [SourceError.Errors] each name
// their own reason. Excerpt leaves out a node whose location does not
// resolve. Its message is still part of the tree [FormatError] prints. A
// nil SourceError carries no location.
func (e *SourceError) Excerpt(context int) (*line.View, bool) {
	if e == nil {
		return nil, false
	}

	view := e.source.View()

	if len(e.annotate(view)) == 0 {
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
// [FormatError] and
// [go.jacobcolvin.com/niceyaml/printer.Printer.PrintError] render the
// excerpts of each binding [Bindings] yields this way. A nil SourceError
// yields nothing.
func (e *SourceError) Excerpts(context int) iter.Seq2[*Source, *line.View] {
	return func(yield func(*Source, *line.View) bool) {
		for _, src := range e.sources() {
			view := src.View()

			if len(e.annotate(view)) == 0 {
				continue
			}

			if !yield(src, view.Hunks(context)) {
				return
			}
		}
	}
}

// details returns what [SourceError.Error] leaves out: each excerpt from
// [SourceError.Excerpts] with context lines, which [line.View.String]
// renders as plain text. When no location resolves, a line starting
// "no excerpt:" names the reason [SourceError.Unresolved] returns in
// place of the excerpts, and an error that carries no location has
// nothing to explain. Returns nothing when there is nothing to show. The
// printer renders the same parts with its styles.
func (e *SourceError) details(context int) []string {
	var parts []string

	for _, excerpt := range e.Excerpts(context) {
		parts = append(parts, excerpt.String())
	}

	if len(parts) > 0 {
		return parts
	}

	// The reason names the path, which a key of the document spells, so
	// its control characters render as pictures like those of the tree.
	reason := e.Unresolved()
	if reason != nil {
		return []string{"no excerpt: " + escape.Control(reason.Error())}
	}

	return nil
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

// annotate marks the whole tree of e on view, as [SourceError.Excerpt]
// and [SourceError.Excerpts] show it: the location of every node
// highlighted, the message of each node below the root as an annotation
// below its line, and the root's own location with a caret run alone.
// It returns the indices of the lines of view it marked, with repeats,
// which are none when no location resolved or the view holds none of
// their lines.
//
// The locations of the tree group by the source each node is bound to,
// in the order [SourceError.sources] gives. The method thus finds a line
// by the identity it has in its own source, and a line index of one
// source never stands for a line of another.
func (e *SourceError) annotate(view *line.View) []int {
	if e == nil {
		return nil
	}

	positions := make(map[*Source][]errorPosition)

	if e.locErr == nil {
		positions[e.source] = append(positions[e.source], errorPosition{pos: e.loc.pos, ranges: e.ranges})
	}

	e.walk(func(n *SourceError) {
		if n.locErr == nil {
			positions[n.source] = append(positions[n.source], errorPosition{
				pos:     n.loc.pos,
				ranges:  n.ranges,
				message: n.text(),
			})
		}
	})

	var marked []int

	for _, src := range e.sources() {
		marked = append(marked, annotateSource(view, src, positions[src])...)
	}

	if len(marked) == 0 {
		return nil
	}

	markUnannotated(view, marked)

	return marked
}

// annotateSource marks positions, the resolved locations of the nodes
// bound to src, on view and returns the indices of the lines it marked,
// with repeats. The view may hold the lines of src at any indices, as a
// diff does, so every mark goes to the index that holds its line. A
// position with no token under it, or a range that covers no column of
// its lines, has nothing to highlight, so its line gets an overlay of no
// width at its column. The overlay renders nothing and still marks the
// line as decorated, so the line joins the hunks [line.View.Hunks] keeps.
// A column past the end of its line moves to the column after its last
// rune, in the overlay and in the annotation below the line. A far column then
// costs a renderer no more cells than a column at the end.
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

			if len(segments) == 0 {
				col := min(pos.pos.Col, src.lines.Line(pos.pos.Line).Width())
				view.AddLineOverlay(i, line.Overlay{
					Cols: position.NewSpan(col, col),
					Kind: kind.GenericError,
				})
			}
		}

		for _, lr := range segments {
			if i, ok := index(lr.Start.Line); ok {
				view.AddOverlay(kind.GenericError, viewRange(lr, i))

				marked = append(marked, i)
			}
		}
	}

	if len(marked) == 0 {
		return nil
	}

	for lineIdx, annotation := range prepareLineAnnotations(positions) {
		if i, ok := index(lineIdx); ok {
			annotation.Col = min(annotation.Col, src.lines.Line(lineIdx).Width())
			view.Annotate(i, annotation)
		}
	}

	return marked
}

// sources returns the source of the binding, then the source of each
// node below it that no node before it in depth-first order is bound
// to. An excerpt per source thus comes out in the order the tree reaches
// them.
func (e *SourceError) sources() []*Source {
	if e == nil {
		return nil
	}

	out := []*Source{e.source}
	seen := map[*Source]bool{e.source: true}

	e.walk(func(n *SourceError) {
		if !seen[n.source] {
			seen[n.source] = true
			out = append(out, n.source)
		}
	})

	return out
}

// markUnannotated adds an annotation without content, in [kind.TextError]
// and at the first column its overlays cover, below every line of view at
// an index in marked that carries no annotation below it yet. A line
// several locations mark, or that an earlier error marked, thus gets one.
// [line.View.String] draws the marks of a line from its overlays. A
// renderer that draws them from its annotations, as the printer does,
// draws such an annotation as a caret run under the overlays. The range
// of a location with no message beside it, such as the root of a bound
// error, then shows its extent without color.
func markUnannotated(view *line.View, marked []int) {
	for _, i := range marked {
		overlays := view.Overlays(i)
		if len(overlays) == 0 || len(view.Annotations(i).Filter(line.Below)) > 0 {
			continue
		}

		col := overlays[0].Cols.Start
		for _, o := range overlays[1:] {
			col = min(col, o.Cols.Start)
		}

		view.Annotate(i, line.Annotation{Kind: kind.TextError, Placement: line.Below, Col: col})
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

// viewRange moves the single-line range r to line i of a view.
func viewRange(r position.Range, i int) position.Range {
	return position.NewRange(position.New(i, r.Start.Col), position.New(i, r.End.Col))
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
// when the error carried one, otherwise the content of the token at its
// position.
func highlightRanges(view line.Lines, loc location) position.Ranges {
	if loc.rng != nil {
		return position.Ranges{clampRange(view, *loc.rng)}
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
// the same column keep the order they arrived in.
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
		for _, r := range lineErrs {
			messages = append(messages, r.message)
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
