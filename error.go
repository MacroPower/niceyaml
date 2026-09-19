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
	// [Source.Document], [Source.Decode], and [Source.DecodeInto] return it.
	ErrNoDocuments = errors.New("no documents in source")

	// ErrMultipleDocuments indicates a [Source] that holds more than one YAML
	// document where one was expected. [Source.Document], [Source.Decode],
	// and [Source.DecodeInto] return it.
	ErrMultipleDocuments = errors.New("multiple documents in source")

	// ErrDecodeTarget indicates the value given to [Document.DecodeInto] or
	// [Source.DecodeInto] is not a non-nil pointer, so there is nothing to
	// decode into.
	ErrDecodeTarget = errors.New("decode target is not a non-nil pointer")

	// ErrOutOfRange indicates the error's location lies outside the lines of
	// the source, past the last or before the first, which happens when a
	// position or range came from other text. [SourceError.Range] and
	// [SourceError.Excerpt] return it.
	ErrOutOfRange = errors.New("location outside source")
)

// Location is where an [Error] points in a YAML document: a [paths.Path],
// a [position.Position], or a [position.Range]. [WithPath], [WithPosition],
// and [WithRange] each set one, and [Error.Location] returns the one set,
// as one of those three types, so a caller reads it with a type switch:
//
//	switch loc := err.Location().(type) {
//	case paths.Path:
//	case position.Position:
//	case position.Range:
//	case nil: // No location.
//	}
type Location interface {
	// String returns the location as people read it: the path expression
	// for a path, and 1-indexed coordinates for a position or a range.
	String() string
}

// Error is an error that points at a location in a YAML document.
//
// The location is a [Location]: a [paths.Path], a [position.Position], or
// a [position.Range], set with [WithPath], [WithPosition], or [WithRange].
// An Error holds one, and the last of those options given wins. A path
// resolves within one document of a source: the [Document] that binds the
// Error, whether its own methods and validators produced the Error or
// [Document.Bind] bound one built elsewhere.
//
// An Error carries what a producer knows and nothing about presentation. A
// validator that knows a path uses [WithPath] and need not hold the source.
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
// rather than text: [Error.Errors] returns them, [Error.Unwrap] exposes
// them to [errors.Is] and [errors.As], and the [SourceError] that binds
// the Error binds each one as a child with a resolved location of its own.
//
// Error implements the error interface. Use [Error.Unwrap] with [errors.Is]
// and [errors.As] to inspect wrapped errors.
//
// Create instances with [NewError] or [NewErrorFrom].
type Error struct {
	err    error
	loc    Location
	errors []error
}

// NewError creates a new [*Error] with the given message.
// Use [NewErrorFrom] instead if wrapping an existing error.
func NewError(msg string, opts ...ErrorOption) *Error {
	return NewErrorFrom(errors.New(msg), opts...)
}

// NewErrorFrom creates a new [*Error] wrapping an existing error.
// Use [NewError] instead if creating an error from a message string.
func NewErrorFrom(err error, opts ...ErrorOption) *Error {
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
//	located := err.With(niceyaml.WithPath(namePath))
func (e *Error) With(opts ...ErrorOption) *Error {
	c := *e
	c.errors = slices.Clone(e.errors)

	for _, opt := range opts {
		opt(&c)
	}

	return &c
}

// ErrorOption configures an [Error].
//
// Available options:
//   - [WithPath]
//   - [WithPosition]
//   - [WithRange]
//   - [WithErrors]
type ErrorOption func(e *Error)

// WithPath is an [ErrorOption] that sets the YAML path where the error
// occurred as the [Location] of the [Error], replacing any location set
// before it.
//
// The [paths.Path] provides both the path and whether to highlight the key
// or value.
func WithPath(p paths.Path) ErrorOption {
	return func(e *Error) {
		e.loc = p
	}
}

// WithPosition is an [ErrorOption] that sets the 0-indexed position where
// the error occurred as the [Location] of the [Error], replacing any
// location set before it. The position is in the coordinates of the lines
// [Source.Lines] returns, where line 0 is line 1 of the text.
// [SourceError.Excerpt] highlights the content of the token at that
// position. A producer that holds a go-yaml token converts it with
// [position.NewFromToken], and one that holds none names the position on
// its own.
func WithPosition(p position.Position) ErrorOption {
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

	return WithPosition(position.NewFromToken(tk))
}

// WithRange is an [ErrorOption] that sets the 0-indexed range the error
// covers as the [Location] of the [Error], replacing any location set
// before it. The range is in the coordinates of the view [Source.Lines]
// returns, where line 0 is line 1 of the text. [SourceError.Excerpt]
// highlights the whole range rather than one token, so it is the option
// for a check that knows the columns an error covers, such as one that
// runs on rendered lines.
func WithRange(r position.Range) ErrorOption {
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
// location, listed on a line of its own by the %+v verb and rendered as
// an annotation below that line. A nested error that is a [*SourceError]
// already, or wraps one, is bound as it is. A nil nested error is skipped.
func WithErrors(errs ...error) ErrorOption {
	return func(e *Error) {
		e.errors = append(e.errors, errs...)
	}
}

// Error returns the error message: "$.path: msg" when the Error carries a
// path, and the message alone otherwise. A position or a range puts nothing
// in the message, since the [SourceError] that binds the Error puts the
// resolved position in front, and the nested errors from [WithErrors] put
// nothing in it either, since that SourceError lists them behind their own
// positions. An Error created from a nil error has an empty message, so
// its text is the path alone, or "" when it has none.
func (e *Error) Error() string {
	var msg string

	if e.err != nil {
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

// anchor returns the [Error] that carries the location: e itself when it
// has one, otherwise the nearest such Error along its cause chain, looking
// through foreign wrapping. Falls back to e when none carries a location.
func (e *Error) anchor() *Error {
	for cur := e; ; {
		if cur.hasPosition() {
			return cur
		}

		inner := nextError(cur.err)
		if inner == nil {
			return e
		}

		cur = inner
	}
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
// to [NewErrorFrom], or one holding the message given to [NewError]. It is
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

// Location returns the [Location] of the [Error]: the [paths.Path],
// [position.Position], or [position.Range] that [WithPath], [WithPosition],
// or [WithRange] set, or nil when none did. It looks through wrapping to
// the nearest Error that carries one, so an Error built with [NewErrorFrom]
// around a located Error reports that location. A nil Error has none.
func (e *Error) Location() Location {
	if e == nil {
		return nil
	}

	return e.anchor().loc
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

// locate resolves e's location: a range or a position as it is, and the
// token a path resolves to in the document lookup returns, at the position
// of the token. An Error without a location of its own returns
// [ErrNoLocation].
func (e *Error) locate(lookup func() (*Document, error)) (location, error) {
	switch loc := e.loc.(type) {
	case position.Range:
		return location{pos: loc.Start, rng: &loc}, nil

	case position.Position:
		return location{pos: loc}, nil

	case paths.Path:
		doc, err := lookup()
		if err != nil {
			return location{}, err
		}

		pos, err := doc.position(loc)
		if err != nil {
			return location{}, err
		}

		return location{pos: pos}, nil

	default:
		return location{}, ErrNoLocation
	}
}

// SourceError is an error bound to the [*Source] it occurred in.
//
// [Source.File], [Source.Documents], and the [Document] methods bind every
// error they return, and [Document.Bind] binds an error built elsewhere.
// Binding resolves the location of the error against the source, once, so
// a SourceError never changes and every method of it reads that result:
// [SourceError.Error] puts the position in front of the message,
// [SourceError.Range] returns the resolved range, and
// [SourceError.Excerpt] returns the surrounding lines with the location
// highlighted. The %+v verb prints the message, one line per nested
// error, and the excerpt as plain text, with carets under the locations,
// so it is safe for a log:
//
//	if _, err := source.File(); err != nil {
//		fmt.Printf("%+v\n", err)
//	}
//
// A path resolves in the [Document] that bound the error.
//
// The bound error is a tree, and binding binds every node of it. The
// location of the SourceError is that of the first located [Error] along
// the cause chain of the error it binds: the chain follows each wrapper to
// the one error it wraps and ends at an error that unwraps to several,
// such as one from [errors.Join], which carries no location of its own.
// Every error nested with [WithErrors] in an Error along that chain, and
// every branch of the error that ends it, is bound the same way to the
// same document and becomes a child. [SourceError.Errors] returns the
// children, each a SourceError with its own location and children, so a
// validator's report of several violations binds to one SourceError per
// violation whether it nests them with WithErrors or joins them. An error
// that is or wraps a SourceError is a binding already: as a nested error
// it contributes that binding as the child, and as the error given to Bind
// it comes back as it is.
//
// [SourceError.Excerpt] marks the location of every node in the tree and
// annotates each child with its message, with distant locations in
// separate hunks.
//
// A location that does not resolve, such as a path the document does not
// hold or a position on a line the source does not have, costs the
// SourceError its position, and an error that carries no location never
// had one: [SourceError.Error] then puts the name of the source alone in
// front of the message, and [SourceError.Range] returns the reason.
//
// A SourceError never rewrites the message of the error it binds. The text
// a wrapper such as [fmt.Errorf] produced stays as it was, and the position
// goes in front of it. An error built by hand therefore goes through
// [Document.Bind] first, and context around the SourceError comes after,
// so the position stays beside the message.
//
// The marks of an error are decoration on a [line.View], so the caller
// that renders the error decides how it looks. [SourceError.Excerpt]
// returns the hunks around the locations as a view for any renderer, and
// [SourceError.Annotate] marks a whole view of the source, as a viewer
// that shows errors inline needs. The %+v verb renders the excerpt as
// plain text, and [go.jacobcolvin.com/niceyaml/printer.Printer.PrintError]
// prints the message as a tree and the excerpt with color and the context
// lines the printer is configured with:
//
//	fmt.Println(p.PrintError(err))
//
// A SourceError implements the error interface and unwraps to the error it
// was created from, so [errors.Is] and [errors.As] see through it.
//
// Create instances with [Document.Bind], or receive them from the [Source]
// and [Document] methods.
type SourceError struct {
	err    error
	source *Source
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

// defaultContextLines is the number of context lines the %+v verb shows
// around an error.
const defaultContextLines = 2

// bindTree binds err to src, with paths resolving in doc, which is nil for
// the errors a Source produces itself. A nil err or a nil [*Error] or
// [*SourceError] pointer comes back as it is, as does an error that is or
// wraps a [*SourceError] along its cause chain, since that is a binding
// already. Any other error is bound as a new SourceError.
func bindTree(err error, src *Source, doc *Document) error {
	if isNothing(err) {
		return err
	}

	if _, ok := anchorOf(err).(*SourceError); ok { //nolint:errorlint // The anchor itself, found by the walk.
		return err
	}

	return newSourceError(err, src, doc)
}

// anchorOf returns the error along the cause chain of err that carries the
// location: the first located [*Error], or the first [*SourceError], which
// resolved one already. The chain follows a wrapper to the one error it
// wraps and an Error to its cause. An error that unwraps to several, such
// as one from [errors.Join], ends the chain: it carries no location of its
// own, and each of its branches binds as a child. Returns nil when the
// chain holds no anchor.
func anchorOf(err error) error {
	switch x := err.(type) { //nolint:errorlint // Walks the chain one node at a time.
	case *SourceError:
		if x == nil {
			return nil
		}

		return x

	case *Error:
		if x == nil {
			return nil
		}

		if x.loc != nil {
			return x
		}

		return anchorOf(x.err)

	case interface{ Unwrap() error }:
		return anchorOf(x.Unwrap())

	default:
		return nil
	}
}

// newSourceError binds err to src and resolves its location, with a path
// resolving in doc. A nil doc, which src passes for the errors it produces
// itself, resolves no path. The children of err bind the same way.
func newSourceError(err error, src *Source, doc *Document) *SourceError {
	e := &SourceError{err: err, source: src, locErr: ErrNoLocation}

	// The document paths resolve in. The Source binds only the errors it
	// produces itself, which carry no path, so a path with no document
	// reports that rather than pick one.
	lookup := func() (*Document, error) {
		if doc == nil {
			return nil, fmt.Errorf("%w: no document to resolve the path in", ErrNoLocation)
		}

		return doc, nil
	}

	switch a := anchorOf(err).(type) { //nolint:errorlint // The anchor itself, found by the walk.
	case *Error:
		e.loc, e.locErr = a.locate(lookup)

	case *SourceError:
		// The error wraps a binding, so it is that binding with more
		// around it: it takes over the location and source the binding
		// resolved, and its message carries the position the binding put
		// there already.
		e.adopted = true
		e.source = a.source
		e.loc, e.locErr = a.loc, a.locErr
		e.rng, e.ranges = a.rng, a.ranges
	}

	if !e.adopted {
		if e.locErr == nil {
			e.locErr = checkInRange(e.loc, src.lines)
		}

		if e.locErr == nil {
			e.ranges = highlightRanges(src.lines, e.loc)
			e.rng = rangeOf(src.lines, e.loc)
		}
	}

	e.collect(err, src, doc)

	return e
}

// collect binds the children of the error e binds: every error nested with
// [WithErrors] in an [*Error] along its cause chain, and every branch of
// the error that ends the chain by unwrapping to several. The chain also
// ends at a [*SourceError], which is the cause of the error above it
// rather than a violation of its own, so its children join the children
// of e.
func (e *SourceError) collect(err error, src *Source, doc *Document) {
	for cur := err; !isNothing(cur); {
		switch x := cur.(type) { //nolint:errorlint // Walks the chain one node at a time.
		case *SourceError:
			e.errors = append(e.errors, x.errors...)

			return

		case *Error:
			for _, n := range x.errors {
				e.addChild(n, src, doc)
			}

			cur = x.err

		case interface{ Unwrap() error }:
			cur = x.Unwrap()

		case interface{ Unwrap() []error }:
			for _, branch := range x.Unwrap() {
				e.addChild(branch, src, doc)
			}

			return

		default:
			return
		}
	}
}

// addChild binds n as a child of e. A binding is the child as it is, and
// any other error binds to the same source and document as e, or takes
// over the binding it wraps. A nil n, or a nil pointer, adds nothing.
func (e *SourceError) addChild(n error, src *Source, doc *Document) {
	if isNothing(n) {
		return
	}

	if bound, ok := n.(*SourceError); ok { //nolint:errorlint // The node itself, not a chain search.
		e.errors = append(e.errors, bound)

		return
	}

	e.errors = append(e.errors, newSourceError(n, src, doc))
}

// Source returns the [*Source] the error is bound to.
func (e *SourceError) Source() *Source {
	return e.source
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
// The slice is a copy, so a caller may keep or sort it.
func (e *SourceError) Errors() []*SourceError {
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
// The message is one line, as the message of any error is, so it wraps
// and logs as one. The nested errors are not part of it: [SourceError.Errors]
// returns them, the %+v verb lists each one behind its own position, and
// [go.jacobcolvin.com/niceyaml/printer.Printer.PrintError] draws them as a
// tree. The result never includes source lines, so it is safe to log or
// compare; use [SourceError.Excerpt] or the %+v verb for the annotated
// source excerpt.
func (e *SourceError) Error() string {
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

// walk calls visit for every node below e in depth-first order.
func (e *SourceError) walk(visit func(*SourceError)) {
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
// [SourceError.Error], then the Error of every node below it in the tree
// on a line of its own, so a log names every violation and where it is,
// then [SourceError.Excerpt] rendered as plain text with two lines of
// context: each line of the excerpt behind its number, carets under the
// columns of every location on the row below, and the message of each
// child beside its caret. The output holds no escape sequences, so it
// reads in a log as it does in a terminal. The %q verb quotes
// [SourceError.Error].
func (e *SourceError) Format(f fmt.State, verb rune) {
	switch {
	case verb == 'v' && f.Flag('+'):
		lines := []string{e.Error()}

		e.walk(func(n *SourceError) {
			lines = append(lines, n.Error())
		})

		writeString(f, joinParts(strings.Join(lines, "\n"), e.detail(defaultContextLines)))

	case verb == 'q':
		writeString(f, strconv.Quote(e.Error()))

	default:
		writeString(f, e.Error())
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
// The location was resolved when the error was bound, so Range reads the
// result. It returns [ErrNoLocation] when the error carries no location or
// its path resolves to a token without one, [ErrOutOfRange] when the
// location starts on a line the source does not hold, and the resolution
// error from [go.jacobcolvin.com/niceyaml/paths] when a path does not
// resolve. An error whose Range fails has no position in
// [SourceError.Error] and no excerpt.
func (e *SourceError) Range() (position.Range, error) {
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

// Annotate marks the error on view, which is a view of the source the
// error is bound to, such as one from [Source.View]: the location of every
// node in the tree is highlighted with [kind.GenericError], and the
// message of each node below the root is an annotation below its own
// line in [kind.TextError], so the message reads as error text without
// the highlight of the token it describes. A viewer that shows a document
// with its errors in place marks its view this way and renders it as it
// is.
//
// Annotate marks every location that resolves and returns an error only
// when none does: the error [SourceError.Range] returns, joined with
// those of the nodes below it, or [ErrOutOfRange] for a location past the
// last line. A node whose location does not resolve is left out.
func (e *SourceError) Annotate(view *line.View) error {
	_, err := e.annotate(view)

	return err
}

// Excerpt returns a [line.View] of the source around the error's locations
// with each one highlighted, as [SourceError.Annotate] marks them, and
// context lines of unchanged content on either side of each marked line.
// Distant locations become separate hunks, and the first line of each hunk
// after the first carries a "..." annotation above it. The lines keep the
// numbers they have in the source, so any [printer.Printer] renders the
// excerpt with the file's line numbers, as it renders the hunks of a diff.
// A negative context shows the marked lines alone, as 0 does.
//
// Excerpt returns an error only when no location resolves, as
// [SourceError.Annotate] does. A node whose location does not resolve is
// left out of the excerpt; its message is still part of the %+v output.
func (e *SourceError) Excerpt(context int) (*line.View, error) {
	view := e.source.View()

	marked, err := e.annotate(view)
	if err != nil {
		return nil, err
	}

	// Group the marked lines into hunks with context around each. Errors
	// whose context windows touch share a hunk, so a line gap always
	// separates two hunks for the "..." separator. ContextSpans clamps the
	// spans to the view, so each one starts on a line the view holds.
	spans := position.ContextSpans(marked, context, view.Len())
	excerpt := view.Slice(spans...)

	// Add "..." annotations to first line of each non-first hunk.
	start := 0

	for i, span := range spans {
		if i > 0 {
			excerpt.Annotate(start, line.Annotation{
				Content:   "...",
				Placement: line.Above,
			})
		}

		start += span.Len()
	}

	return excerpt, nil
}

// detail returns what [SourceError.Error] leaves out: [SourceError.Excerpt]
// with context lines, rendered as plain text. When no location resolves, a
// line starting "no excerpt:" names the error [SourceError.Range] returns
// in place of the excerpt, unless that error is [ErrNoLocation], since an
// error that carries no location has nothing to explain. Returns "" when
// there is nothing to show. The printer renders the same parts with its
// styles.
func (e *SourceError) detail(context int) string {
	excerpt, err := e.Excerpt(context)
	if err == nil {
		return plainRenderer{}.Print(excerpt)
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
// lines it marked, with repeats.
func (e *SourceError) annotate(view *line.View) ([]int, error) {
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

	// Collect all ranges from positions and apply overlays to the view. The
	// line of each position joins the marked lines as well, since a
	// position with no token under it has no range to highlight and still
	// picks the lines an excerpt shows.
	var allRanges position.Ranges

	marked := make([]int, 0, len(positions))

	for _, pos := range positions {
		allRanges = append(allRanges, pos.ranges...)
		marked = append(marked, pos.pos.Line)
	}

	marked = append(marked, allRanges.LineIndices()...)

	view.AddOverlay(kind.GenericError, allRanges...)

	for lineIdx, annotation := range prepareLineAnnotations(positions) {
		view.Annotate(lineIdx, annotation)
	}

	return marked, nil
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
		return position.Ranges{*loc.rng}
	}

	return view.ContentRanges(view.TokenAt(loc.pos))
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
