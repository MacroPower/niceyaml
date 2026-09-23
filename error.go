package niceyaml

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

var (
	// ErrNoLocation indicates the error carries neither a path, a position,
	// nor a range, or its path resolves to a token that carries no position.
	// [SourceError.Range] and [SourceError.Excerpt] return it.
	ErrNoLocation = errors.New("no location provided")

	// ErrNoDocuments indicates a [Source] that holds no YAML document where
	// one was expected, such as a file holding only a "..." marker.
	// [Source.Document] returns it.
	ErrNoDocuments = errors.New("no documents in source")

	// ErrMultipleDocuments indicates a [Source] that holds more than one YAML
	// document where one was expected. [Source.Document] returns it.
	ErrMultipleDocuments = errors.New("multiple documents in source")

	// ErrDecodeTarget indicates the value given to [Node.DecodeInto] is
	// not a non-nil pointer, so there is nothing to decode into.
	ErrDecodeTarget = errors.New("decode target is not a non-nil pointer")

	// ErrOutOfRange indicates the error's location lies outside the lines of
	// the source, past the last or before the first, which happens when a
	// position or range came from other text, or outside the view given to
	// [SourceError.Annotate], which holds none of the lines the locations
	// fall on. [SourceError.Range], [SourceError.Excerpt], and
	// [SourceError.Annotate] return it.
	ErrOutOfRange = errors.New("location outside source")

	// ErrPathNeedsDocument indicates an error that carries a path was bound
	// through [Source.Bind] in a source that holds no single document to
	// resolve the path in. It wraps the reason [Source.Document] gives:
	// [ErrMultipleDocuments], [ErrNoDocuments], or the error the file
	// fails to parse with. [SourceError.Range] and [SourceError.Excerpt]
	// return it. Bind such an error through [Node.Bind] with the
	// document it was checked against.
	ErrPathNeedsDocument = errors.New("path needs a document to resolve in")
)

// Error is an error that points at a location in a YAML document.
//
// The location is a [paths.Path], a [position.Position], or a
// [position.Range], set with [AtPath], [AtPosition], or [AtRange].
// An Error holds one, and the last of those options given wins.
// [Error.Path], [Error.Position], and [Error.Range] each return the
// location when it is of that type, so a caller that wants the path
// reads it without a type switch:
//
//	if path, ok := err.Path(); ok {
//		// The error points at path.
//	}
//
// A path resolves within one document of a source, from the [Node] that
// binds the Error, whether its own methods and validators produced the
// Error or [Node.Bind] bound one built elsewhere.
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
// the Error binds each one as a child with a resolved location of its own.
//
// Error implements the error interface. Use [Error.Unwrap] with [errors.Is]
// and [errors.As] to inspect wrapped errors.
//
// Create instances with [NewError] or [WrapError].
type Error struct {
	err error
	// The location: a paths.Path, a position.Position, or a position.Range,
	// or nil when none is set.
	loc    any
	errors []error
	// The path the paths under the Error are written from, which Rebase
	// sets, and whether it is set at all, since the root is a base like
	// any other.
	base    paths.Path
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

// Rebase returns an error whose paths are written from base. Every path
// in the tree of err, whether on the [*Error] that anchors it or on an
// error nested with [WithErrors], resolves as base joined with that path,
// and the message of the result carries the joined path. A check
// written for a type writes paths from the value's own root, so a
// caller that runs it on a value inside a decoded document rebases the
// result under the path of that value before binding it:
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
// under the base that carries no location points at base itself, and a
// position or a range stays as it is, since the base moves paths alone.
// A decode rebases the errors of every nested [SelfValidator] itself,
// so a Validate need not rebase the Validate of a field.
//
// The result wraps err, so [errors.Is] and [errors.As] see through it, and
// the text a wrapper such as [fmt.Errorf] added around a located error
// stays as it is, with the path the wrapper wrote in it, so rebase an
// error before adding context to it. A nil err, or a nil [*Error] or
// [*SourceError] pointer, returns nil, so a validator returns the result as
// it is. An error that is or wraps a [*SourceError] with no located
// [*Error] above it is bound already, with its location resolved, and
// comes back as it is. A located Error above a binding carries a location
// of its own, so Rebase puts the base in front of that one.
func Rebase(err error, base paths.Path) error {
	if isNothing(err) {
		return nil
	}

	if _, ok := anchorOf(err).err.(*SourceError); ok { //nolint:errorlint // The anchor itself, found by the walk.
		return err
	}

	return &Error{err: err, base: base, rebased: true}
}

// ErrorOption configures an [Error]. An At option sets the location of
// the Error, and the last one given wins. [WithErrors] adds nested errors.
//
// Available options:
//   - [AtPath]
//   - [AtPosition]
//   - [AtRange]
//   - [WithErrors]
type ErrorOption func(e *Error)

// AtPath is an [ErrorOption] that sets the YAML path where the error
// occurred as the location of the [Error], replacing any location set
// before it. The error points at the node the path selects, which for a
// mapping entry is its value, so [SourceError.Excerpt] highlights the
// value. A path from [paths.Path.Key] points at the key of the entry
// instead, which suits an error about the key itself, such as an unknown
// field:
//
//	niceyaml.NewError("unknown field", niceyaml.AtPath(paths.Root().Child("spec", "foo").Key()))
func AtPath(p paths.Path) ErrorOption {
	return func(e *Error) {
		e.loc = p
	}
}

// AtPosition is an [ErrorOption] that sets the 0-indexed position where
// the error occurred as the location of the [Error], replacing any
// location set before it. The position is in the coordinates of the lines
// [Source.Lines] returns, where line 0 is line 1 of the text.
// [SourceError.Excerpt] highlights the content of the token at that
// position. A producer that holds a go-yaml token converts it with
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
// covers as the location of the [Error], replacing any location set
// before it. The range is in the coordinates of the view [Source.Lines]
// returns, where line 0 is line 1 of the text. [SourceError.Excerpt]
// highlights the whole range rather than one token, so it is the option
// for a check that knows the columns an error covers, such as one that
// runs on rendered lines.
func AtRange(r position.Range) ErrorOption {
	return func(e *Error) {
		e.loc = r
	}
}

// WithErrors is an [ErrorOption] that adds nested errors to the [Error],
// such as one per violation a validator found.
//
// A nested error is any error, and one that is an [*Error] carries a
// location of its own. [Error.Errors] returns them, and the [SourceError]
// that binds the Error binds each one as a child with its own resolved
// location, listed as a branch of the message by [FormatError] and
// rendered as an annotation below its line in the excerpt. A nested
// error that is a [*SourceError]
// already, or wraps one, is bound as it is. A nil nested error is skipped.
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
// has an empty message, so its text is the path alone, or "" when it has
// none. An Error from [Rebase] carries the joined path in front of the
// message of the error it rebased, in place of the path that error wrote,
// and an Error with a location of its own likewise replaces the path an
// Error it wraps wrote, so the message names one location, the one
// [Error.Path] reports.
func (e *Error) Error() string {
	if e == nil {
		return ""
	}

	var msg string

	switch {
	case e.rebased:
		msg = e.message()

		if p, ok := e.location().(paths.Path); ok {
			msg = prefix(p.String()+":", msg)
		}

		return msg

	case e.hasPosition():
		msg = e.message()

	case e.err != nil:
		msg = e.err.Error()
	}

	if p, ok := e.loc.(paths.Path); ok {
		msg = prefix(p.String()+":", msg)
	}

	return msg
}

// nested returns the nested errors of e that are not nothing, in the
// order they were given.
func (e *Error) nested() []error {
	out := make([]error, 0, len(e.errors))

	for _, n := range e.errors {
		if !isNothing(n) {
			out = append(out, n)
		}
	}

	return out
}

// located returns the location of e: its own when it has one, otherwise
// that of the nearest located Error along its cause chain, looking
// through foreign wrapping, with the base of every Error from [Rebase] on
// the way joined in front of a path. An Error from Rebase with no located
// Error below it is located at its base. Reports false when the chain
// holds none.
func (e *Error) located() (any, bool) {
	if e.hasPosition() {
		if p, isPath := e.loc.(paths.Path); isPath && e.rebased {
			return e.base.Join(p), true
		}

		return e.loc, true
	}

	inner := nextError(e.err)
	if inner != nil {
		loc, ok := inner.located()
		if ok {
			if p, isPath := loc.(paths.Path); isPath && e.rebased {
				loc = e.base.Join(p)
			}

			return loc, true
		}
	}

	if e.rebased {
		return e.base, true
	}

	return nil, false
}

// nextError returns the nearest [*Error] along the cause chain of err: err
// itself, or the one a wrapper wraps, following each wrapper to the one
// error it wraps. An error that unwraps to several ends the chain, as it
// does for binding, so an Error and its binding agree on the location.
// Returns nil when the chain holds none.
func nextError(err error) *Error {
	for cur := err; cur != nil; {
		switch x := cur.(type) { //nolint:errorlint // Walks the chain one node at a time.
		case *Error:
			if x == nil {
				return nil
			}

			return x

		case interface{ Unwrap() error }:
			cur = x.Unwrap()

		default:
			return nil
		}
	}

	return nil
}

// hasPosition reports whether e carries a location of its own.
func (e *Error) hasPosition() bool {
	return e.loc != nil
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

// Cause returns the error the [Error] was created from: the error given
// to [WrapError], or one holding the message given to [NewError]. It is
// nil for an Error created from a nil error, and a nil Error has no cause.
func (e *Error) Cause() error {
	if e == nil {
		return nil
	}

	return e.err
}

// Errors returns the errors nested in the [Error] with [WithErrors], in the
// order they were given and without the nil ones. A nil Error nests
// nothing. The slice is a copy, so a caller may keep or sort it.
func (e *Error) Errors() []error {
	if e == nil {
		return nil
	}

	return e.nested()
}

// location returns the location of the [Error]: the [paths.Path],
// [position.Position], or [position.Range] that [AtPath], [AtPosition],
// or [AtRange] set, or nil when none did. It looks through wrapping to
// the nearest Error that carries one, so an Error built with [WrapError]
// around a located Error reports that location, and a path comes back
// with the base of every [Rebase] on the way joined in front. A nil Error
// has none.
func (e *Error) location() any {
	if e == nil {
		return nil
	}

	loc, ok := e.located()
	if !ok {
		return nil
	}

	return loc
}

// Path returns the [paths.Path] the [Error] points at and true, or the
// zero Path and false when the Error carries a position, a range, or no
// location. The location is the one [AtPath] set on the Error itself or
// on the nearest located Error along its cause chain, so an Error built
// with [WrapError] around a located Error reports that location, with
// the base of every [Rebase] on the way joined in front. A nil Error has
// none.
func (e *Error) Path() (paths.Path, bool) {
	p, ok := e.location().(paths.Path)

	return p, ok
}

// Position returns the [position.Position] the [Error] points at and
// true, or the zero Position and false when the Error carries a path, a
// range, or no location. It looks through wrapping as [Error.Path] does.
func (e *Error) Position() (position.Position, bool) {
	p, ok := e.location().(position.Position)

	return p, ok
}

// Range returns the [position.Range] the [Error] covers and true, or the
// zero Range and false when the Error carries a path, a position, or no
// location. It looks through wrapping as [Error.Path] does. The range is
// the one [AtRange] set, in the coordinates of [Source.Lines];
// [SourceError.Range] returns the range a location of any kind resolved
// to once the Error is bound.
func (e *Error) Range() (position.Range, bool) {
	r, ok := e.location().(position.Range)

	return r, ok
}

// message returns the text of e without the location e or the Errors it
// directly wraps put in front: the message of the innermost error that is
// not an Error, or "" when that error is nil. Text a foreign wrapper such as
// [fmt.Errorf] added stays as it is, as it does everywhere else.
func (e *Error) message() string {
	for cur := e; ; {
		inner, ok := cur.err.(*Error) //nolint:errorlint // Identity of the direct child, not a chain search.
		if !ok || inner == nil {
			if cur.err == nil {
				return ""
			}

			return cur.err.Error()
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

// locate resolves loc, the location of an [Error], and returns the node
// it is bound to: a range or a position as it is, and the token a path
// resolves to in the document, at the position of the token, with the
// base of b in front of the path. The node is the one b binds with, or,
// when b routes, the root of the document [binder.route] picks for the
// location. A nil loc returns [ErrNoLocation], and a path bound where no
// document resolves it returns [ErrPathNeedsDocument].
func locate(b binder, loc any) (location, *Node, error) {
	switch loc := loc.(type) {
	case position.Range:
		return location{pos: loc.Start, rng: &loc}, b.nodeAt(loc.Start.Line), nil

	case position.Position:
		return location{pos: loc}, b.nodeAt(loc.Line), nil

	case paths.Path:
		return locatePath(b, b.base.Join(loc))

	default:
		return location{}, b.node, ErrNoLocation
	}
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
// document it was checked against, and [Source.Bind] binds one to the
// document its location falls in, or to the source alone when it carries
// no location. Binding resolves the location of the error against the source, once, so
// a SourceError never changes and every method of it reads that result.
// [SourceError.Error] puts the position in front of the message,
// [SourceError.Message] returns the message alone, [SourceError.Range]
// returns the resolved range, [SourceError.Path] the path the error was
// written with, and [SourceError.Excerpt] returns the surrounding lines
// with the location highlighted. [FormatError] prints the message as a
// tree with a branch per nested error, then the excerpt as plain text
// with carets under the locations, so it is safe for a log, however the
// error is wrapped or joined:
//
//	if _, err := source.File(); err != nil {
//		log.Print(niceyaml.FormatError(err, 2))
//	}
//
// A path resolves from the [Node] that bound the error, which for
// [Source.Bind] is the root of the one document of the source. A path bound through
// Source.Bind in a source that holds none or several resolves nowhere,
// and the reason is [ErrPathNeedsDocument].
//
// The bound error is a tree, and binding binds every node of it. The
// location of the SourceError is that of the first located [Error] along
// the cause chain of the error it binds. The chain follows each wrapper to
// the one error it wraps and ends at an error that unwraps to several,
// such as one from [errors.Join], which carries no location of its own.
// Every error nested with [WithErrors] in an Error along that chain, and
// every branch of the error that ends it, is bound the same way to the
// same document and becomes a child. [SourceError.Errors] returns the
// children, each a SourceError with its own location and children, so a
// validator's report of several violations binds to one SourceError per
// violation whether it nests them with WithErrors or joins them. An error
// that is or wraps a SourceError, with no located Error above it, is a
// binding already. As a nested error it contributes that binding as the
// child, and as the error given to Bind it comes back as it is. A located
// Error above a binding binds anew at its own location, and its message
// carries the position the inner binding resolved as well as its own.
//
// [SourceError.Excerpt] marks the location of every node in the tree and
// annotates each child with its message, with distant locations in
// separate hunks.
//
// A location that does not resolve, such as a path the document does not
// hold or a position on a line the source does not have, costs the
// SourceError its position, and an error that carries no location never
// had one. [SourceError.Error] then puts the name of the source alone in
// front of the message, and [SourceError.Range] returns the reason.
//
// A SourceError never rewrites the message of the error it binds. The text
// a wrapper such as [fmt.Errorf] produced stays as it was, and the position
// goes in front of it. An error built by hand therefore goes through
// [Node.Bind] or [Source.Bind] first, and context around the
// SourceError comes after, so the position stays beside the message.
//
// The marks of an error are decoration on a [line.View], so the caller
// that renders the error decides how it looks. [SourceError.Excerpt]
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
// was created from, so [errors.Is] and [errors.As] see through it.
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
	// ErrNoLocation, the error of a path that does not resolve, or
	// ErrOutOfRange for a location the source does not hold.
	locErr error
	// The bound children: the errors nested along the cause chain and the
	// branches of it that lead to a location of their own.
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

// defaultContextLines is the number of context lines the %+v verb of a
// [*SourceError] shows around an error.
const defaultContextLines = 2

// binder is where an error binds: the source, and the node that binds
// it, which is nil for [Source.Bind] and for the errors a Source produces
// itself. A binder that routes picks the document for each location it
// resolves, as [Source.Bind] does; one that does not, as the parser's
// binder must not, since the documents are not built until the parse
// ends, binds to the source alone.
type binder struct {
	src  *Source
	node *Node
	// The path the paths of the errors bound here are written from: the
	// root, joined with the base of every Error from Rebase above them.
	base  paths.Path
	route bool
}

// nodeAt returns the node an error on line idx binds to: the one b binds
// with, or, when b routes, the root of the document of the source whose
// span holds the line, which is nil when the source does not parse or the
// line lies outside it.
func (b binder) nodeAt(idx int) *Node {
	if b.node != nil || !b.route {
		return b.node
	}

	docs, err := b.src.Documents()
	if err != nil {
		return nil
	}

	for _, doc := range docs {
		if doc.span.Contains(idx) {
			return doc
		}
	}

	return nil
}

// bindTree binds err to b. A nil err, or a nil [*Error] or [*SourceError]
// pointer, carries nothing to bind and comes back as a nil error, so a
// caller compares the result against nil whatever the shape of the nil it
// passed. An error that is or wraps a [*SourceError] along its cause chain,
// with no located [*Error] above it, is a binding already and comes back
// as it is. Any other error, including a located Error above a binding, is
// bound as a new SourceError.
func bindTree(err error, b binder) error {
	if isNothing(err) {
		return nil
	}

	if _, ok := anchorOf(err).err.(*SourceError); ok { //nolint:errorlint // The anchor itself, found by the walk.
		return err
	}

	return newSourceError(err, b)
}

// anchor is the error along a cause chain that carries the location, and
// the location it carries: a [paths.Path], with the base of every Error
// from [Rebase] above it joined in front, a [position.Position], or a
// [position.Range], or nil for a [*SourceError], which resolved its
// location already. The zero anchor is a chain that holds none.
type anchor struct {
	err error
	loc any
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

		if x.loc != nil {
			if p, ok := x.loc.(paths.Path); ok && x.rebased {
				return anchor{err: x, loc: x.base.Join(p)}
			}

			return anchor{err: x, loc: x.loc}
		}

		a := anchorOf(x.err)
		if a.err != nil {
			if p, ok := a.loc.(paths.Path); ok && x.rebased {
				a.loc = x.base.Join(p)
			}

			return a
		}

		if x.rebased {
			return anchor{err: x, loc: x.base}
		}

		return anchor{}

	case interface{ Unwrap() error }:
		return anchorOf(x.Unwrap())

	default:
		return anchor{}
	}
}

// newSourceError binds err to b and resolves its location, with a path
// resolving in the document of b. The children of err bind the same way.
func newSourceError(err error, b binder) *SourceError {
	e := &SourceError{err: err, source: b.src, node: b.node, locErr: ErrNoLocation}

	found := anchorOf(err)

	switch a := found.err.(type) { //nolint:errorlint // The anchor itself, found by the walk.
	case *Error:
		e.loc, e.node, e.locErr = locate(b, found.loc)

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
			e.rng = rangeOf(b.src.lines, e.loc)
		}
	}

	e.collect(err, b)

	return e
}

// collect binds the children of the error e binds: every error nested with
// [WithErrors] in an [*Error] along its cause chain, and every branch of
// the error that ends the chain by unwrapping to several, with the base of
// every Error from [Rebase] on the way in front of their paths. The chain
// also ends at a [*SourceError], which is the cause of the error above it
// rather than a violation of its own, so its children join the children
// of e.
func (e *SourceError) collect(err error, b binder) {
	for cur := err; !isNothing(cur); {
		switch x := cur.(type) { //nolint:errorlint // Walks the chain one node at a time.
		case *SourceError:
			e.errors = append(e.errors, x.errors...)

			return

		case *Error:
			if x.rebased {
				b.base = b.base.Join(x.base)
			}

			for _, n := range x.errors {
				e.addChild(n, b)
			}

			cur = x.err

		case interface{ Unwrap() error }:
			cur = x.Unwrap()

		case interface{ Unwrap() []error }:
			for _, branch := range x.Unwrap() {
				e.addChild(branch, b)
			}

			return

		default:
			return
		}
	}
}

// addChild binds n as a child of e. A binding is the child as it is, and
// any other error binds where e binds, or takes over the binding it
// wraps. A nil n, or a nil pointer, adds nothing.
func (e *SourceError) addChild(n error, b binder) {
	if isNothing(n) {
		return
	}

	if bound, ok := n.(*SourceError); ok { //nolint:errorlint // The node itself, not a chain search.
		e.errors = append(e.errors, bound)

		return
	}

	e.errors = append(e.errors, newSourceError(n, b))
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
// produced itself are bound to none, while Node.Bind keeps its node on
// every error it binds. A nil SourceError is bound to none.
func (e *SourceError) Node() *Node {
	if e == nil {
		return nil
	}

	return e.node
}

// Document returns the root [*Node] of the document the error is bound
// to, the one the node [SourceError.Node] returns belongs to, so a caller
// that sorts the errors of a file by document reads its [Node.Index]. An
// error bound to no node is bound to no document. A nil SourceError is
// bound to none.
func (e *SourceError) Document() *Node {
	if e == nil {
		return nil
	}

	return e.node.Document()
}

// Message returns the text of the bound error with no position or path
// in front: the message an [*Error] was created with, without the path
// [Error.Error] puts before it, or the text of any other error as it is.
// It is the text [SourceError.Excerpt] annotates a location with, and
// the field a structured report such as a JSON line or a CI annotation
// carries beside the position from [SourceError.Range] and the path from
// [SourceError.Path]:
//
//	for _, bound := range niceyaml.SourceErrors(err) {
//		rng, _ := bound.Range()
//		path, _ := bound.Path()
//		emit(bound.Source().FilePath(), rng.Start, bound.Message(), path)
//	}
//
// Text a wrapper such as [fmt.Errorf] added around the Error stays, with
// the path the wrapper wrote in it, as it does everywhere else. A nil
// SourceError has an empty message.
func (e *SourceError) Message() string {
	return e.text()
}

// Path returns the [paths.Path] the bound error points at and true, or
// the zero Path and false when it carries a position, a range, or no
// location. It is the path [Error.Path] reports for the [*Error] that
// gave the binding its location, with the base of every [Rebase] on the
// way joined in front, so an error bound through a scoped [Node]
// reports the path as the error wrote it, from the scope. A binding that
// wraps another reports the path of the one it wraps. A nil SourceError
// has none.
func (e *SourceError) Path() (paths.Path, bool) {
	if e == nil {
		return paths.Path{}, false
	}

	found := anchorOf(e.err)

	if inner, ok := found.err.(*SourceError); ok { //nolint:errorlint // The anchor itself, found by the walk.
		return inner.Path()
	}

	p, ok := found.loc.(paths.Path)

	return p, ok
}

// Unwrap returns the error the [SourceError] was created from. A nil
// SourceError unwraps to nothing, so a chain that holds one is safe to walk.
func (e *SourceError) Unwrap() error {
	if e == nil {
		return nil
	}

	return e.err
}

// Errors returns the bound children of the [SourceError]: one SourceError
// per error nested with [WithErrors] along the cause chain of the bound
// error, and per branch of it that leads to a location of its own, in the
// order they were given, each with its own location and children. A
// validator's report of several violations therefore unwraps to one
// child per violation:
//
//	for _, violation := range bound.Errors() {
//		rng, err := violation.Range()
//		...
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
// in front: "name:line:col: $.path: msg" for a path error and
// "name:line:col: msg" for a position or range error, with any context a
// wrapper added between the position and the rest. The name is
// [Source.Name], and the position stands alone as "line:col:" when the
// source has none, so an error from a named file reads as a compiler
// diagnostic that editors and build tools link to the line. An error
// without a location, or one whose location does not resolve, has no
// position to add, and the name then stands alone in front as "name: msg",
// so an error from one file of many still says which file; without a name
// the message comes back as it is.
//
// The message is the text of the bound error, which runs over several
// lines when that text does, as the text of an [errors.Join] and a
// message written with continuation lines both do. The nested errors
// are not part of it. [SourceError.Errors] returns them, and
// [FormatError] and
// [go.jacobcolvin.com/niceyaml/printer.Printer.PrintError] draw them as
// the branches of a tree. The result never includes source lines, so it
// is safe to log or compare; use [SourceError.Excerpt] or [FormatError]
// for the annotated source excerpt. A nil SourceError, which [Node.Bind] passes through
// as it does any nil pointer, has an empty message, as a nil [*Error] does.
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

	case name != "":
		return prefix(name+":", msg)

	default:
		return msg
	}
}

// formatPosition returns pos as "name:line:col:", the shape editors and
// build tools read, or "line:col:" when name is empty. Editors count from
// 1, so the coordinates are 1-indexed.
func formatPosition(name string, pos position.Position) string {
	if name == "" {
		return pos.String() + ":"
	}

	return name + ":" + pos.String() + ":"
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
// caret marks it, or the message of any other error as it is.
func (e *SourceError) text() string {
	if e == nil {
		return ""
	}

	if x, ok := e.err.(*Error); ok { //nolint:errorlint // The node itself, not a chain search.
		return x.message()
	}

	return e.err.Error()
}

// resolution returns why no location in the tree of e resolved: the reason
// of e itself, then that of every node below it behind the message of its
// error, joined. A node built from a nil error has a location and no
// message of its own, so its reason stands alone.
func (e *SourceError) resolution() error {
	errs := []error{e.locErr}

	e.walk(func(n *SourceError) {
		if n.locErr == nil || n.source != e.source {
			return
		}

		x, ok := n.err.(*Error) //nolint:errorlint // The node itself, not a chain search.
		if ok && x.err == nil {
			errs = append(errs, n.locErr)

			return
		}

		errs = append(errs, fmt.Errorf("%w: %w", n.err, n.locErr))
	})

	return errors.Join(errs...)
}

// walk calls visit for every node below e in depth-first order. A nil e
// has no nodes below it.
func (e *SourceError) walk(visit func(*SourceError)) {
	if e == nil {
		return
	}

	for _, c := range e.errors {
		visit(c)
		c.walk(visit)
	}
}

// SourceErrors returns every binding in the tree of err whose excerpt
// stands on its own: each [*SourceError] reached through the wrappers and
// joins around it, in depth-first order, so the outermost comes first and
// each branch of an [errors.Join] follows the one before it, and, below
// each one, every child bound to a source none of its ancestors in the
// result is bound to. The excerpt of a binding marks the children bound to
// the same source, so those are part of it. A caller that renders an
// error built from several bindings, such as one per document of a file,
// marks every one of them this way:
//
//	view := source.View()
//	for _, bound := range niceyaml.SourceErrors(err) {
//		_ = bound.Annotate(view)
//	}
//
// Returns nil when err is nil or its tree holds no SourceError.
func SourceErrors(err error) []*SourceError {
	var out []*SourceError

	var walk func(error, map[*Source]bool)

	walk = func(err error, seen map[*Source]bool) {
		switch x := err.(type) { //nolint:errorlint // Walks the tree one node at a time.
		case *SourceError:
			if x == nil {
				return
			}

			if !seen[x.source] {
				out = append(out, x)
			}

			// The children of a binding to a source seen already render
			// with the ancestor bound to it, and the rest on their own.
			below := maps.Clone(seen)
			below[x.source] = true

			for _, c := range x.errors {
				walk(c, below)
			}

		case interface{ Unwrap() error }:
			walk(x.Unwrap(), seen)

		case interface{ Unwrap() []error }:
			for _, inner := range x.Unwrap() {
				walk(inner, seen)
			}
		}
	}

	walk(err, map[*Source]bool{})

	return out
}

// Format implements [fmt.Formatter].
//
// The %v and %s verbs print [SourceError.Error]. The %+v verb prints
// what [FormatError] renders for the error with two lines of context,
// for a log that prints its errors that way. A wrapper such as
// [fmt.Errorf] around a SourceError formats as its own message, so a
// program that holds any error calls FormatError. The %q verb quotes
// [SourceError.Error].
func (e *SourceError) Format(f fmt.State, verb rune) {
	switch {
	case verb == 'v' && f.Flag('+'):
		writeString(f, FormatError(e, defaultContextLines))

	case verb == 'q':
		writeString(f, strconv.Quote(e.Error()))

	default:
		writeString(f, e.Error())
	}
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
// The excerpts follow, one per binding [SourceErrors] finds, each
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
// an error with nothing nested is its message.
// [go.jacobcolvin.com/niceyaml/printer.Printer.PrintError] renders the
// same tree and excerpts with styles. A nil err renders as "".
func FormatError(err error, context int) string {
	if err == nil {
		return ""
	}

	parts := []string{renderErrorTree(NewErrorTree(err))}

	for _, bound := range SourceErrors(err) {
		parts = append(parts, bound.detail(context))
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
func renderErrorTree(t ErrorTree) string {
	var sb strings.Builder

	if t.Text != "" {
		sb.WriteString(t.Text)
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

// Range returns the range in the source that the error points at: the
// range it carries, or the content of the token at the position it carries
// or its path resolves to. A token that spans several lines yields a range
// across them, and its start is the position [SourceError.Error] reports.
// The range is in the coordinates of the view [Source.Lines] returns, where
// line 0 is line 1 of the text.
//
// Binding resolved the location, so Range reads the result. It returns
// [ErrNoLocation] when the error carries no location or its path resolves
// to a token without one, [ErrOutOfRange] when the location starts on a
// line the source does not hold, and the resolution error from
// [go.jacobcolvin.com/niceyaml/paths] when a path does not resolve. An
// error whose Range fails has no position in [SourceError.Error] and no
// excerpt. A nil SourceError carries no location, so it returns
// [ErrNoLocation].
func (e *SourceError) Range() (position.Range, error) {
	if e == nil {
		return position.Range{}, ErrNoLocation
	}

	if e.locErr != nil {
		return position.Range{}, e.locErr
	}

	return e.rng, nil
}

// rangeOf returns the range loc covers in lines: the range it carries, or
// the content of the token at its position, which spans several lines for
// a multi-line token. A position with no token yields an empty range.
func rangeOf(lines line.Lines, loc location) position.Range {
	ranges := highlightRanges(lines, loc)
	if len(ranges) == 0 {
		return position.NewRange(loc.pos, loc.pos)
	}

	return position.NewRange(ranges[0].Start, ranges[len(ranges)-1].End)
}

// Annotate marks the error on view, which holds lines of the source the
// error is bound to. Annotate highlights the location of every node in
// the tree with [kind.GenericError] and adds the message of each node
// below the root as an annotation below its own line in [kind.TextError],
// so the message reads as error text without the highlight of the token
// it describes. A viewer that shows a document with its errors in place
// marks its view this way and renders it as it is.
//
// Annotate finds each line by identity rather than by index, since every
// view over the source shares its [*line.Line] values, so the view may be
// the whole source from [Source.View], a slice of it from [line.View.Slice]
// such as one document of a file, or a diff that interleaves the source
// with another revision, where the marks land on the lines of this source
// alone. A line the view does not hold is left out.
//
// Annotate marks every location that resolves and returns an error only
// when none does: the error [SourceError.Range] returns, joined with
// those of the nodes below it, or [ErrOutOfRange] for a location past the
// last line or on lines the view does not hold. A node whose location does
// not resolve is left out.
func (e *SourceError) Annotate(view *line.View) error {
	_, err := e.annotate(view)

	return err
}

// Excerpt returns a [line.View] of the source around the error's locations with each
// one highlighted, as [SourceError.Annotate] marks them, and context lines of unchanged
// content on either side of each marked line. Distant locations become separate hunks,
// and the first line of each hunk after the first carries a "..." annotation above it.
// The lines keep the numbers they have in the source, so any
// [go.jacobcolvin.com/niceyaml/printer.Printer] renders the excerpt with the file's
// line numbers, as it renders the hunks of a diff. A negative context shows the marked
// lines alone, as 0 does.
//
// Excerpt returns an error only when no location resolves, as
// [SourceError.Annotate] does. A node whose location does not resolve is
// left out of the excerpt; its message is still part of the tree
// [FormatError] prints. A nil SourceError carries no location, so it
// returns [ErrNoLocation].
func (e *SourceError) Excerpt(context int) (*line.View, error) {
	if e == nil {
		return nil, ErrNoLocation
	}

	view := e.source.View()

	marked, err := e.annotate(view)
	if err != nil {
		return nil, err
	}

	// Group the marked lines into hunks with context around each. Errors
	// whose context windows touch share a hunk, so a line gap always
	// separates two hunks for the "..." separator. ContextSpans clamps the
	// spans to the view, so each one starts on a line the view holds.
	spans := position.ContextSpans(marked, context, view.Lines().Len())
	excerpt := view.Slice(spans...)

	// Add "..." annotations to the first line of each hunk after the
	// first. ContextSpans clamps the spans to the view, so each one starts
	// on a line the excerpt holds.
	for _, span := range spans[1:] {
		excerpt.Annotate(span.Start, line.Annotation{
			Content:   "...",
			Kind:      kind.UISeparator,
			Placement: line.Above,
		})
	}

	return excerpt, nil
}

// detail returns what [SourceError.Error] leaves out: the excerpt from
// [SourceError.Excerpt] with context lines, which [line.View.String]
// renders as plain text. When no location resolves, a line starting
// "no excerpt:" names the error [SourceError.Range] returns in place of
// the excerpt, unless that error is [ErrNoLocation], since an error that
// carries no location has nothing to explain. Returns "" when there is
// nothing to show. The printer renders the same parts with its styles.
func (e *SourceError) detail(context int) string {
	excerpt, err := e.Excerpt(context)
	if err == nil {
		return excerpt.String()
	}

	_, locErr := e.Range()
	if locErr != nil && !errors.Is(locErr, ErrNoLocation) {
		return "no excerpt: " + locErr.Error()
	}

	return ""
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

// errorPosition holds a resolved error position: the main error position
// (message empty) or a nested error position with its annotation text.
type errorPosition struct {
	message string
	ranges  position.Ranges
	pos     position.Position
}

// annotate is [SourceError.Annotate] that also returns the indices of the
// lines of view it marked, with repeats.
func (e *SourceError) annotate(view *line.View) ([]int, error) {
	if e == nil {
		return nil, ErrNoLocation
	}

	var positions []errorPosition

	if e.locErr == nil {
		positions = append(positions, errorPosition{pos: e.loc.pos, ranges: e.ranges})
	}

	// A node bound to another source marks that source, and its excerpt
	// renders on its own; its children may still be bound to this one.
	e.walk(func(n *SourceError) {
		if n.locErr == nil && n.source == e.source {
			positions = append(positions, errorPosition{pos: n.loc.pos, ranges: n.ranges, message: n.text()})
		}
	})

	if len(positions) == 0 {
		return nil, e.resolution()
	}

	// The view may hold the lines of the source at other indices, as a
	// diff does, so every mark goes to the index that holds its line. The
	// line of each position joins the marked lines as well, since a
	// position with no token under it has no range to highlight and still
	// picks the lines an excerpt shows.
	index := e.lineIndex(view)

	var marked []int

	for _, pos := range positions {
		if i, ok := index(pos.pos.Line); ok {
			marked = append(marked, i)
		}

		for _, r := range pos.ranges {
			for _, lr := range e.source.lines.SliceLines(r) {
				if i, ok := index(lr.Start.Line); ok {
					view.AddOverlay(kind.GenericError, viewRange(lr, i))

					marked = append(marked, i)
				}
			}
		}
	}

	if len(marked) == 0 {
		return nil, fmt.Errorf("%w: the view holds none of the lines the error marks", ErrOutOfRange)
	}

	for lineIdx, annotation := range prepareLineAnnotations(positions) {
		if i, ok := index(lineIdx); ok {
			view.Annotate(i, annotation)
		}
	}

	return marked, nil
}

// lineIndex returns a lookup from a line index of the source to the index
// of view that holds that line, found by identity. A line index the
// source does not hold, or a line the view does not hold, reports false.
func (e *SourceError) lineIndex(view *line.View) func(int) (int, bool) {
	lines := e.source.lines

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
// does not hold: one past its last line, or one before its first. The
// message names the line and the lines the source holds as the text counts
// them, from 1.
func checkInRange(loc location, lines line.Lines) error {
	if loc.pos.Line >= 0 && loc.pos.Line < lines.Len() {
		return nil
	}

	textLine := loc.pos.Line + 1
	if lines.Len() == 0 {
		return fmt.Errorf("%w: line %d of an empty source", ErrOutOfRange, textLine)
	}

	return fmt.Errorf("%w: line %d not in lines 1-%d", ErrOutOfRange, textLine, lines.Len())
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
// within the lines comes back as it is.
func clampRange(lines line.Lines, r position.Range) position.Range {
	last := lines.Len() - 1
	if last < 0 || r.End.Line <= last {
		return r
	}

	return position.NewRange(r.Start, position.New(last, lines.Line(last).Width()))
}

// prepareLineAnnotations prepares annotations grouped by line index. It
// includes only positions with messages, which are the nested errors.
func prepareLineAnnotations(positions []errorPosition) map[int]line.Annotation {
	linePositions := make(map[int][]errorPosition)

	for _, pos := range positions {
		if pos.message != "" {
			linePositions[pos.pos.Line] = append(linePositions[pos.pos.Line], pos)
		}
	}

	result := make(map[int]line.Annotation)

	for lineIdx, lineErrs := range linePositions {
		messages := make([]string, 0, len(lineErrs))
		minCol := lineErrs[0].pos.Col

		for _, r := range lineErrs {
			messages = append(messages, r.message)
			minCol = min(minCol, r.pos.Col)
		}

		result[lineIdx] = line.Annotation{
			Content:   strings.Join(messages, "; "),
			Kind:      kind.TextError,
			Placement: line.Below,
			Col:       minCol,
		}
	}

	return result
}
