package niceyaml

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/style"
)

var (
	// ErrNoLocation indicates the error carries neither a path, a token, nor
	// a range. [SourceError.Location] and [SourceError.Excerpt] return it.
	ErrNoLocation = errors.New("no location provided")

	// ErrTokenNotFound indicates the error's token, or the token its path
	// resolves to, carries no position. [SourceError.Location] and
	// [SourceError.Excerpt] return it.
	ErrTokenNotFound = errors.New("token not found in source")

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
	// token or range came from other text. [SourceError.Location] and
	// [SourceError.Excerpt] return it.
	ErrOutOfRange = errors.New("location outside source")
)

// Renderer renders a [line.View] as text. [SourceError.Detail] renders the
// excerpt around an error's location with one, and the %+v verb renders it
// as plain text.
//
// See [go.jacobcolvin.com/niceyaml/printer.Printer] for an implementation.
type Renderer interface {
	Print(view *line.View) string
}

// Error is an error that points at a location in a YAML document.
//
// The location is a [paths.Path], a [*token.Token], or a [position.Range],
// set with [WithPath], [WithToken], or [WithRange]. A path resolves within
// one document of a source, and the binder picks which: a [Document] binds
// the Errors its methods and validators produce, and [Document.WrapError]
// binds one built elsewhere, to itself; [Source.WrapError] binds to the
// single document [Source.Document] picks.
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
// "name:line:col: msg".
// Nested errors from [WithErrors] are not part of the message. They surface
// through [Error.Unwrap], and [SourceError.Excerpt] renders them as
// annotations.
//
// Error implements the error interface. Use [Error.Unwrap] with [errors.Is]
// and [errors.As] to inspect wrapped errors.
//
// Create instances with [NewError] or [NewErrorFrom].
type Error struct {
	err    error
	path   *paths.Path
	token  *token.Token
	rng    *position.Range
	errors []*Error
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
//	located := err.With(niceyaml.WithPath(namePath.Value()))
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
//   - [WithToken]
//   - [WithRange]
//   - [WithErrors]
type ErrorOption func(e *Error)

// WithPath is an [ErrorOption] that sets the YAML path where the error occurred.
//
// The [paths.Path] provides both the path and whether to highlight the key
// or value.
func WithPath(p paths.Path) ErrorOption {
	return func(e *Error) {
		e.path = &p
	}
}

// WithToken is an [ErrorOption] that sets the token where the error
// occurred. Only the token's position is used, so a token from a parsed AST
// works even though the parser clones tokens.
func WithToken(tk *token.Token) ErrorOption {
	return func(e *Error) {
		e.token = tk
	}
}

// WithRange is an [ErrorOption] that sets the 0-indexed range the error
// covers, in the coordinates of the view [Source.Lines] returns, where line
// 0 is line 1 of the text. It is the option for producers that know a
// location but hold no go-yaml token, such as a check that runs on rendered
// lines. [SourceError.Excerpt] highlights the whole range.
func WithRange(r position.Range) ErrorOption {
	return func(e *Error) {
		e.rng = &r
	}
}

// WithErrors is an [ErrorOption] that adds nested errors to the [Error].
//
// Each nested error has its own location and renders as an annotation
// below its resolved line.
func WithErrors(errs ...*Error) ErrorOption {
	return func(e *Error) {
		e.errors = append(e.errors, errs...)
	}
}

// Error returns the error message: "$.path: msg" when the Error carries a
// path, and the message alone otherwise. A token or a range puts nothing in
// the message, since the [SourceError] that binds the Error puts the
// resolved position in front. Nested errors from [WithErrors] are not part
// of the message. An Error created from a nil error has an empty message,
// so its text is the path alone, or "" when it has none.
func (e *Error) Error() string {
	var msg string

	if e.err != nil {
		msg = e.err.Error()
	}

	if e.path != nil {
		msg = prefixMessage(e.path.String()+":", msg)
	}

	return msg
}

// prefixMessage returns prefix and msg separated by a space, or prefix alone
// when msg is empty.
func prefixMessage(prefix, msg string) string {
	if msg == "" {
		return prefix
	}

	return prefix + " " + msg
}

// formatPosition returns pos as "name:line:col:", the shape editors and
// build tools read, or "line:col:" when name is empty. Editors count from 1,
// so the coordinates are 1-indexed.
func formatPosition(name string, pos position.Position) string {
	if name == "" {
		return fmt.Sprintf("%d:%d:", pos.Line+1, pos.Col+1)
	}

	return fmt.Sprintf("%s:%d:%d:", name, pos.Line+1, pos.Col+1)
}

// anchor returns the [Error] that carries the position: e itself when it has
// a token, path, or range, otherwise the nearest such Error wrapped inside
// e, looking through foreign wrapping. Falls back to e when none carries a
// position.
func (e *Error) anchor() *Error {
	for cur := e; ; {
		if cur.hasPosition() {
			return cur
		}

		inner, ok := errors.AsType[*Error](cur.err)
		if !ok || inner == nil {
			return e
		}

		cur = inner
	}
}

// hasPosition reports whether e carries a token, a range, or a path of its
// own.
func (e *Error) hasPosition() bool {
	return e.token != nil || e.rng != nil || e.path != nil
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

	for _, nested := range e.errors {
		if nested != nil {
			result = append(result, nested)
		}
	}

	return result
}

// Path returns the [paths.Path] set with [WithPath] and whether one was
// set. It looks through wrapping to the [Error] that carries the location,
// as [Error.Token] and [Error.Range] do.
func (e *Error) Path() (paths.Path, bool) {
	a := e.anchor()
	if a.path == nil {
		return paths.Path{}, false
	}

	return *a.path, true
}

// Token returns the token set with [WithToken] and whether one was set. It
// looks through wrapping to the [Error] that carries the location. The
// token is the one given, so treat it as read-only.
func (e *Error) Token() (*token.Token, bool) {
	a := e.anchor()
	if a.token == nil {
		return nil, false
	}

	return a.token, true
}

// Range returns the [position.Range] set with [WithRange] and whether one
// was set. It looks through wrapping to the [Error] that carries the
// location.
func (e *Error) Range() (position.Range, bool) {
	a := e.anchor()
	if a.rng == nil {
		return position.Range{}, false
	}

	return *a.rng, true
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

// locate resolves e's location: a range as it is, and a token, or the
// token a path resolves to in the document lookup returns, at the position
// of the token. An Error without a position of its own returns
// [ErrNoLocation].
func (e *Error) locate(lookup func() (*Document, error)) (location, error) {
	switch {
	case e.rng != nil:
		return location{pos: e.rng.Start, rng: e.rng}, nil

	case e.token != nil:
		if e.token.Position == nil {
			return location{}, ErrTokenNotFound
		}

		return location{pos: position.NewFromToken(e.token)}, nil

	case e.path != nil:
		doc, err := lookup()
		if err != nil {
			return location{}, err
		}

		tk, err := e.path.Token(doc.doc)
		if err != nil {
			//nolint:wrapcheck // The paths error already names the path.
			return location{}, err
		}

		if tk == nil || tk.Position == nil {
			return location{}, ErrTokenNotFound
		}

		return location{pos: position.NewFromToken(tk)}, nil

	default:
		return location{}, ErrNoLocation
	}
}

// SourceError is an error bound to the [*Source] it occurred in.
//
// [Source.File], [Source.Documents], and the [Document] methods bind every
// error they return whose chain holds an [*Error], and [Source.WrapError]
// and [Document.WrapError] bind an error built elsewhere. Binding resolves
// every location in the error against the source, once, so a SourceError
// never changes and every method of it reads that result: [SourceError.Error]
// puts the position in front of the message, [SourceError.Location]
// returns the resolved range, and [SourceError.Excerpt] returns the
// surrounding lines with the location highlighted. The %+v verb prints the
// message and the excerpt as plain text, with carets under the location,
// so it is safe for a log:
//
//	if _, err := source.File(); err != nil {
//		fmt.Printf("%+v\n", err)
//	}
//
// A path resolves in the document the binder picked: the [Document] that
// bound the error, or the single document [Source.Document] picks for an
// error bound through [Source.WrapError].
//
// The bound error is a tree, and the SourceError presents all of it. Its
// own position is that of the first [Error] along the cause chain, the
// chain that follows each wrapper to the error it wraps and, where an
// error unwraps to several as one from [errors.Join] does, to its first
// branch. Every other branch of the tree, whether a later branch of a join
// or a nested error given with [WithErrors], resolves the same way in the
// same document, and [SourceError.Excerpt] marks its location and
// annotates it with its message. Distant locations render as separate
// hunks. An error bound to another source inside the tree is a binding of
// its own, so the SourceError leaves it alone, and
// [go.jacobcolvin.com/niceyaml/printer.Printer.PrintError] renders every
// binding it finds in a tree.
//
// A location that does not resolve, such as a path the document does not
// hold, costs the SourceError its position: [SourceError.Error] returns the
// message alone, and [SourceError.Location] returns the reason.
//
// A SourceError never rewrites the message of the error it binds. The text
// a wrapper such as [fmt.Errorf] produced stays as it was, and the position
// goes in front of it. An error built by hand therefore goes through
// [Source.WrapError] first, and context around the SourceError comes after,
// so the position stays beside the message.
//
// The marks of an error are decoration on a [line.View], so the caller
// that renders the error decides how it looks. [SourceError.Excerpt]
// returns the hunks around the locations as a view for any [Renderer],
// [SourceError.Annotate] marks a whole view of the source, as a viewer
// that shows errors inline needs, and [SourceError.Detail] renders the
// excerpt with the Renderer and context lines it is given. A
// [go.jacobcolvin.com/niceyaml/printer.Printer] is a Renderer, and its
// PrintError method prints the message and the Detail with color:
//
//	fmt.Println(p.PrintError(err, 3))
//
// A SourceError implements the error interface and unwraps to the error it
// was created from, so [errors.Is] and [errors.As] see through it.
//
// Create instances with [Source.WrapError] or [Document.WrapError], or
// receive them from the [Source] and [Document] methods.
type SourceError struct {
	err    error
	source *Source
	// The reason the main location did not resolve, which is nil when it
	// did, and the reason there is no range for it: locErr, or
	// ErrOutOfRange for a location the source does not hold.
	locErr error
	rngErr error
	// The resolution failures, joined, or nil when every location resolved.
	resolveErr error
	// Every position the excerpt marks, the main one first.
	positions []errorPosition
	// The message of every nested error the excerpt does not show.
	unresolved []string
	// The main location, resolved when the error was bound, and the range
	// it covers in the source.
	loc location
	rng position.Range
}

// defaultContextLines is the number of context lines the %+v verb shows
// around an error.
const defaultContextLines = 2

// newSourceError binds err to src and resolves every location in it, with
// paths resolving in doc, or in the single document src picks when doc is
// nil. The main unit gives the SourceError its position, or the reason it
// has none, and every other unit its annotation. The message of each
// nested error whose location does not resolve is kept for
// [SourceError.Detail] to list.
func newSourceError(err error, src *Source, doc *Document) *SourceError {
	e := &SourceError{err: err, source: src}

	// The document paths resolve in. Source.Document parses the source on
	// the first path that asks and serves its cache after that.
	lookup := func() (*Document, error) {
		if doc != nil {
			return doc, nil
		}

		return src.Document()
	}

	units := collectUnits(err)
	e.positions = make([]errorPosition, 0, len(units))

	var errs []error

	for i, u := range units {
		loc, err := u.locate(lookup)
		if i == 0 {
			e.loc, e.locErr = loc, err
		}

		if err == nil {
			err = checkInRange(loc, src.lines)
		}

		if err != nil {
			if i == 0 {
				e.rngErr = err
			} else {
				err = u.wrapResolution(err)
			}

			if u.nested {
				e.unresolved = append(e.unresolved, u.root.Error())
			}

			errs = append(errs, err)

			continue
		}

		if i == 0 {
			e.rng = rangeOf(src.lines, loc)
		}

		pos := errorPosition{pos: loc.pos, ranges: highlightRanges(src.lines, loc)}
		if i > 0 {
			pos.message = u.text()
		}

		e.positions = append(e.positions, pos)
	}

	e.resolveErr = errors.Join(errs...)

	return e
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

// Error returns the message of the bound error with its resolved position
// in front: "name:line:col: $.path: msg" for a path error and
// "name:line:col: msg" for a token or range error, with any context a
// wrapper added between the position and the rest. The name is
// [Source.Name], and the position stands alone as "line:col:" when the
// source has none, so an error from a named file reads as a compiler
// diagnostic that editors and build tools link to the line. A chain that
// holds a SourceError from a binding to another source already carries the
// position that binding resolved, and a location that does not resolve has
// none to add, so both come back as they are. Nested errors are not part of
// the message; see [SourceError.Excerpt].
//
// The result is plain text and never includes source lines, so it is safe to
// log or compare. Use [SourceError.Excerpt] or the %+v verb for the
// annotated source excerpt.
func (e *SourceError) Error() string {
	msg := e.err.Error()
	if e.locErr != nil {
		return msg
	}

	return prefixMessage(formatPosition(e.source.Name(), e.loc.pos), msg)
}

// unit is one error of the tree a [SourceError] presents: the error at the
// root of a branch and the [*Error] along its cause chain that carries the
// position, or nil when none does. The main unit is the whole tree, and its
// position goes in front of the message. Every other unit marks its
// position in the excerpt and annotates it with its text.
type unit struct {
	root   error
	anchor *Error
	// Reached through WithErrors, so its text is not part of the message
	// and Detail lists it when the excerpt does not show it.
	nested bool
}

// collectUnits returns the units of err: the main unit first, then every
// branch in depth-first order.
func collectUnits(err error) []unit {
	var out []unit

	collectBranch(err, false, &out)

	return out
}

// collectBranch appends the unit rooted at root and, after it, the units of
// the branches along its cause chain. The chain follows a wrapper to the
// error it wraps, an [*Error] to the error it was created from, and an
// error that unwraps to several to its first branch. It ends at the first
// Error that carries a position, which anchors the unit, or at a
// [*SourceError], which is a binding of its own. Every nested error of an
// Error along the chain, and every later branch of an error that unwraps to
// several, starts a unit.
func collectBranch(root error, nested bool, out *[]unit) {
	u := unit{root: root, nested: nested}

	var branches []unit

	for cur := root; cur != nil; {
		//nolint:errorlint // Walks the tree one node at a time; errors.As would skip ahead.
		switch x := cur.(type) {
		case *SourceError:
			cur = nil

		case *Error:
			if x == nil {
				cur = nil

				break
			}

			for _, n := range x.errors {
				if n != nil {
					branches = append(branches, unit{root: n, nested: true})
				}
			}

			if x.hasPosition() {
				u.anchor = x
				cur = nil

				break
			}

			cur = x.err

		case interface{ Unwrap() error }:
			cur = x.Unwrap()

		case interface{ Unwrap() []error }:
			// A nil pointer binds nothing and locates nothing, so the
			// chain continues at the first branch that holds something.
			cur = nil

			for _, err := range x.Unwrap() {
				switch {
				case isNothing(err):
				case cur == nil:
					cur = err
				default:
					branches = append(branches, unit{root: err})
				}
			}

		default:
			cur = nil
		}
	}

	*out = append(*out, u)

	for _, b := range branches {
		collectBranch(b.root, b.nested, out)
	}
}

// locate resolves the location of u in the document lookup returns. A unit
// without an anchor returns [ErrNoLocation].
func (u unit) locate(lookup func() (*Document, error)) (location, error) {
	if u.anchor == nil {
		return location{}, ErrNoLocation
	}

	return u.anchor.locate(lookup)
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

// text returns the text a unit annotates its position with: the message of
// an [*Error] without the location it puts in front, since the caret marks
// it, or the message of any other error as it is.
func (u unit) text() string {
	if x, ok := u.root.(*Error); ok { //nolint:errorlint // The root itself, not a chain search.
		return x.message()
	}

	return u.root.Error()
}

// wrapResolution returns err, the failure to resolve the unit, behind the
// error the unit was created from. A unit built from a nil error has a
// location and no message of its own, so its failure stands alone.
func (u unit) wrapResolution(err error) error {
	if x, ok := u.root.(*Error); ok { //nolint:errorlint // The root itself, not a chain search.
		if x.err == nil {
			return err
		}

		return fmt.Errorf("%w: %w", x.err, err)
	}

	return fmt.Errorf("%w: %w", u.root, err)
}

// SourceErrors returns every [*SourceError] in the tree of err, in
// depth-first order, so the outermost comes first and each branch of an
// [errors.Join] follows the one before it. A caller that renders an error
// built from several bindings, such as one per document of a file, marks
// every one of them this way:
//
//	view := source.View()
//	for _, bound := range niceyaml.SourceErrors(err) {
//		_ = bound.Annotate(view)
//	}
//
// Returns nil when err is nil or its tree holds no SourceError.
func SourceErrors(err error) []*SourceError {
	var out []*SourceError

	walkErrors(err, func(err error) {
		bound, ok := err.(*SourceError) //nolint:errorlint // Visits every node itself.
		if ok && bound != nil {
			out = append(out, bound)
		}
	})

	return out
}

// walkErrors calls visit for err and every error below it, in depth-first
// order.
func walkErrors(err error, visit func(error)) {
	if err == nil {
		return
	}

	visit(err)

	switch x := err.(type) { //nolint:errorlint // Walks the tree one node at a time.
	case interface{ Unwrap() error }:
		walkErrors(x.Unwrap(), visit)

	case interface{ Unwrap() []error }:
		for _, inner := range x.Unwrap() {
			walkErrors(inner, visit)
		}
	}
}

// hasError reports whether err's tree holds an [*Error] that is not a nil
// pointer, which has no message or location to render.
func hasError(err error) bool {
	e, ok := errors.AsType[*Error](err)

	return ok && e != nil
}

// firstSourceError returns the first [*SourceError] in err's chain. It
// reports false when the chain holds none or when that SourceError is a nil
// pointer, which binds nothing.
func firstSourceError(err error) (*SourceError, bool) {
	e, ok := errors.AsType[*SourceError](err)

	return e, ok && e != nil
}

// Format implements [fmt.Formatter].
//
// The %v and %s verbs print [SourceError.Error]. The %+v verb prints
// [SourceError.Error], then [SourceError.Detail] rendered as plain text
// with two lines of context: each line of the excerpt behind its number,
// carets under the columns of every location on the row below, and the
// message of each branch beside its caret. The Detail of every other
// SourceError in the tree follows, as [SourceErrors] finds them. The output
// holds no escape sequences, so it reads in a log as it does in a terminal.
// The %q verb quotes [SourceError.Error].
func (e *SourceError) Format(f fmt.State, verb rune) {
	switch {
	case verb == 'v' && f.Flag('+'):
		parts := []string{e.Error()}
		for _, bound := range SourceErrors(e) {
			parts = append(parts, bound.Detail(plainRenderer{}, defaultContextLines))
		}

		writeString(f, joinParts(parts...))

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

// Location returns the range in the source that the error points at: the
// range it carries, or the content of the token it carries or its path
// resolves to. A token that spans several lines yields a range across them.
// The range is in the coordinates of the view [Source.Lines] returns, where
// line 0 is line 1 of the text.
//
// The location was resolved when the error was bound, so Location reads
// the result. It returns [ErrNoLocation] when the error carries no
// location, [ErrTokenNotFound] when the token has no position,
// [ErrOutOfRange] when the location starts on a line the source does not
// hold, the resolution error from [go.jacobcolvin.com/niceyaml/paths] when
// a path does not resolve, and the error [Source.Document] returns when a
// path error bound through [Source.WrapError] has no single document to
// resolve in.
func (e *SourceError) Location() (position.Range, error) {
	if e.rngErr != nil {
		return position.Range{}, e.rngErr
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
// error is bound to, such as one from [Source.View]: every location in
// the tree is highlighted with [style.GenericError], and the message of
// each branch other than the main one is an annotation below its own
// line. A viewer that shows a document with its errors in place marks its
// view this way and renders it as it is.
//
// Annotate marks every location that resolves and returns an error only
// when none does: the errors [SourceError.Location] returns, joined with
// those of the other branches, or [ErrOutOfRange] for a location past the
// last line. A branch whose location does not resolve is left out.
func (e *SourceError) Annotate(view *line.View) error {
	_, _, err := e.annotate(view)

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
// [SourceError.Annotate] does. A branch whose location does not resolve is
// left out of the excerpt, and [SourceError.Detail] lists the nested ones
// after it.
func (e *SourceError) Excerpt(context int) (*line.View, error) {
	excerpt, _, err := e.excerpt(context)

	return excerpt, err
}

// excerpt is [SourceError.Excerpt] that also returns the message of every
// nested error the excerpt does not annotate, in the order the errors were
// given.
func (e *SourceError) excerpt(context int) (*line.View, []string, error) {
	view := e.source.View()

	marked, unresolved, err := e.annotate(view)
	if err != nil {
		return nil, unresolved, err
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

	return excerpt, unresolved, nil
}

// Detail returns what [SourceError.Error] leaves out: [SourceError.Excerpt]
// with context lines, rendered by r, then one line per nested error from
// [WithErrors] the excerpt does not annotate, since its message is not
// part of [SourceError.Error]. A later branch of a join that does not
// resolve is not listed, as the message names it already. Blank
// lines separate the parts. When no location resolves, a line starting
// "no excerpt:" names the error [SourceError.Location] returns in place of
// the excerpt, unless that error is [ErrNoLocation], since an error that
// carries no location has nothing to explain. Returns "" when there is
// nothing to show.
//
// The %+v verb prints [SourceError.Error] and the Detail rendered as plain
// text with two lines of context. A
// [go.jacobcolvin.com/niceyaml/printer.Printer] is a Renderer, and its
// PrintError method prints the message and the Detail the same way with
// the printer's styles.
func (e *SourceError) Detail(r Renderer, context int) string {
	var parts []string

	excerpt, unresolved, err := e.excerpt(context)
	if err == nil {
		parts = append(parts, r.Print(excerpt))
	} else {
		_, locErr := e.Location()
		if locErr != nil && !errors.Is(locErr, ErrNoLocation) {
			parts = append(parts, "no excerpt: "+locErr.Error())
		}
	}

	if len(unresolved) > 0 {
		parts = append(parts, strings.Join(unresolved, "\n"))
	}

	return joinParts(parts...)
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
// lines it marked, with repeats, and the message of every nested error
// whose location did not resolve, in the order the errors were given.
func (e *SourceError) annotate(view *line.View) ([]int, []string, error) {
	positions := e.positions
	if len(positions) == 0 {
		return nil, e.unresolved, e.resolveErr
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

	view.AddOverlay(style.GenericError, allRanges...)

	for lineIdx, annotation := range prepareLineAnnotations(positions) {
		view.Annotate(lineIdx, annotation)
	}

	return marked, e.unresolved, nil
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
			Placement: line.Below,
			Col:       minCol,
		}
	}

	return result
}
