package niceyaml

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/style"
)

var (
	// ErrNoLocation indicates the error carries neither a path, a token, nor
	// a range. [SourceError.Location] and [SourceError.Detail] return it.
	ErrNoLocation = errors.New("no location provided")

	// ErrTokenNotFound indicates the error's token, or the token its path
	// resolves to, carries no position. [SourceError.Location] and
	// [SourceError.Detail] return it.
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
	// [SourceError.Detail] return it.
	ErrOutOfRange = errors.New("location outside source")

	// Shared [printer.Printer] used when no [WithPrinter] is configured.
	defaultPrinter = sync.OnceValue(func() *printer.Printer { return printer.New() })
)

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
// so a bound error reads "[line:col] $.path: msg" or "[line:col] msg".
// Nested errors from [WithErrors] are not part of the message. They surface
// through [Error.Unwrap], and [SourceError.Detail] renders them as
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
// lines. [SourceError.Detail] highlights the whole range.
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

// formatPosition returns pos as "[line:col]". Editors count from 1, so the
// coordinates are 1-indexed.
func formatPosition(pos position.Position) string {
	return fmt.Sprintf("[%d:%d]", pos.Line+1, pos.Col+1)
}

// anchor returns the [Error] that carries the location: e itself when it has
// a token, path, range, or nested errors, otherwise the nearest such Error
// wrapped inside e, looking through foreign wrapping. Falls back to e when
// none carries a location.
func (e *Error) anchor() *Error {
	for cur := e; ; {
		if cur.hasLocation() {
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

// hasLocation reports whether e carries a position or nested errors.
func (e *Error) hasLocation() bool {
	return e.hasPosition() || len(e.errors) > 0
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

// locate resolves e's location in the source b is bound to: a range as it
// is, and a token, or the token a path resolves to in the document b
// selects, at the position of the token. An Error without a position of
// its own takes the location of the nearest Error it wraps, looking through
// foreign wrapping as [Error.anchor] does.
func (e *Error) locate(b *SourceError) (location, error) {
	switch {
	case e.rng != nil:
		return location{pos: e.rng.Start, rng: e.rng}, nil

	case e.token != nil:
		if e.token.Position == nil {
			return location{}, ErrTokenNotFound
		}

		return location{pos: position.NewFromToken(e.token)}, nil

	case e.path != nil:
		doc, err := b.document()
		if err != nil {
			return location{}, err
		}

		tk, err := e.path.Token(doc.doc)
		if err != nil {
			return location{}, fmt.Errorf("path token: %w", err)
		}

		if tk == nil || tk.Position == nil {
			return location{}, ErrTokenNotFound
		}

		return location{pos: position.NewFromToken(tk)}, nil

	default:
		inner, ok := errors.AsType[*Error](e.err)
		if ok && inner != nil {
			return inner.locate(b)
		}

		return location{}, ErrNoLocation
	}
}

// SourceError is an error bound to the [*Source] it occurred in.
//
// [Source.File], [Source.Documents], and the [Document] methods bind every
// error they return whose chain holds an [*Error], and [Source.WrapError]
// and [Document.WrapError] bind an error built elsewhere. The SourceError
// resolves the Error's location against the source, so [SourceError.Error]
// puts the position in front of the message, [SourceError.Location]
// returns the resolved range, and [SourceError.Detail] renders the
// surrounding lines with the location highlighted. The %+v verb prints the
// message and the detail:
//
//	if _, err := source.File(); err != nil {
//		fmt.Printf("%+v\n", err)
//	}
//
// A path resolves in the document the binder picked: the [Document] that
// bound the error, or the single document [Source.Document] picks for an
// error bound through [Source.WrapError]. Nested errors resolve in the same
// document, appear as annotations below their own lines, and distant
// locations render as separate hunks.
//
// A SourceError never rewrites the message of the error it binds. The text
// a wrapper such as [fmt.Errorf] produced stays as it was, and the position
// goes in front of it. An error built by hand therefore goes through
// [Source.WrapError] first, and context around the SourceError comes after,
// so the position stays beside the message.
//
// [SourceError.Render] returns what %+v prints, and both it and
// [SourceError.Detail] accept [DetailOption] values for the [printer.Printer] and the
// number of context lines, so the caller that renders the error decides how
// it looks. A SourceError implements the error interface and unwraps to the
// error it was created from, so [errors.Is] and [errors.As] see through it.
//
// Create instances with [Source.WrapError] or [Document.WrapError], or
// receive them from the [Source] and [Document] methods.
type SourceError struct {
	err    error
	source *Source
	// The document paths resolve in, or nil for the single document the
	// source picks.
	doc *Document
}

// DetailOption configures how [SourceError.Detail] and [SourceError.Render]
// render the source excerpt.
//
// Available options:
//   - [WithPrinter]
//   - [WithContextLines]
type DetailOption func(*detailConfig)

// detailConfig holds the settings a [DetailOption] configures.
type detailConfig struct {
	printer      *printer.Printer
	contextLines int
}

// newDetailConfig applies opts over the defaults: the shared default
// [printer.Printer] and [defaultContextLines].
func newDetailConfig(opts []DetailOption) detailConfig {
	c := detailConfig{contextLines: defaultContextLines}
	for _, opt := range opts {
		opt(&c)
	}

	if c.printer == nil {
		c.printer = defaultPrinter()
	}

	return c
}

// defaultContextLines is the number of context lines shown around an error
// when [WithContextLines] is not set.
const defaultContextLines = 2

// WithContextLines is a [DetailOption] that sets the number of context lines
// shown around each error location. The default is 2, and a negative count
// shows the error lines alone, as 0 does.
func WithContextLines(lines int) DetailOption {
	return func(c *detailConfig) {
		c.contextLines = lines
	}
}

// WithPrinter is a [DetailOption] that sets the [*printer.Printer] that
// renders the source excerpt. The printer's width, set with
// [printer.WithWidth], controls word wrapping, and its styles color the
// highlighted locations. The default is a [printer.Printer] from
// [printer.New].
func WithPrinter(p *printer.Printer) DetailOption {
	return func(c *detailConfig) {
		c.printer = p
	}
}

// newSourceError binds err to src, with paths resolving in doc, or in the
// single document src picks when doc is nil.
func newSourceError(err error, src *Source, doc *Document) *SourceError {
	return &SourceError{err: err, source: src, doc: doc}
}

// Source returns the [*Source] the error is bound to.
func (e *SourceError) Source() *Source {
	return e.source
}

// document returns the document paths resolve in: the one the error was
// bound through, or the single document [Source.Document] picks, with its
// error when the source holds no such document.
func (e *SourceError) document() (*Document, error) {
	if e.doc != nil {
		return e.doc, nil
	}

	return e.source.Document()
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
// in front: "[line:col] $.path: msg" for a path error and "[line:col] msg"
// for a token or range error, with any context a wrapper added between the
// position and the rest. A chain that holds a SourceError from a binding to
// another source already carries the position that binding resolved, and a
// location that does not resolve has none to add, so both come back as
// they are. Nested errors are not part of the message; see
// [SourceError.Detail].
//
// The result is plain text and never includes source lines, so it is safe to
// log or compare. Use [SourceError.Detail] or the %+v verb for the annotated
// source excerpt.
func (e *SourceError) Error() string {
	msg := e.err.Error()

	// A binding to another source put the position it resolved into the
	// text that the wrappers above it froze.
	if _, ok := firstSourceError(e.err); ok { //nolint:errcheck // Presence check, not a value extraction.
		return msg
	}

	a := e.anchor()
	if a == nil {
		return msg
	}

	loc, err := a.locate(e)
	if err != nil {
		return msg
	}

	return prefixMessage(formatPosition(loc.pos), msg)
}

// anchor returns the [*Error] in the chain that carries the location, or
// nil when the chain holds no Error or its first Error is a nil pointer.
func (e *SourceError) anchor() *Error {
	root, ok := firstError(e.err)
	if !ok {
		return nil
	}

	return root.anchor()
}

// firstError returns the first [*Error] in err's chain. It reports false when
// the chain holds none or when that Error is a nil pointer, which has no
// message or location to render.
func firstError(err error) (*Error, bool) {
	e, ok := errors.AsType[*Error](err)

	return e, ok && e != nil
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
// [SourceError.Render] with the default options. The %q verb quotes
// [SourceError.Error].
func (e *SourceError) Format(f fmt.State, verb rune) {
	switch {
	case verb == 'v' && f.Flag('+'):
		writeString(f, e.Render())

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
// It returns [ErrNoLocation] when the error carries no location,
// [ErrTokenNotFound] when the token has no position, [ErrOutOfRange] when
// the location starts on a line the source does not hold, the resolution
// error from [go.jacobcolvin.com/niceyaml/paths] when a path does not
// resolve, and the error [Source.Document] returns when a path error bound
// through [Source.WrapError] has no single document to resolve in.
func (e *SourceError) Location() (position.Range, error) {
	a := e.anchor()
	if a == nil {
		return position.Range{}, ErrNoLocation
	}

	loc, err := a.locate(e)
	if err != nil {
		return position.Range{}, err
	}

	err = e.checkInRange(loc, e.source.lines)
	if err != nil {
		return position.Range{}, err
	}

	return e.rangeOf(loc), nil
}

// rangeOf returns the range loc covers in the source: the range it carries,
// or the content of the token at its position, which spans several lines
// for a multi-line token. A position with no token yields an empty range.
func (e *SourceError) rangeOf(loc location) position.Range {
	ranges := highlightRanges(e.source.lines, loc)
	if len(ranges) == 0 {
		return position.NewRange(loc.pos, loc.pos)
	}

	return position.NewRange(ranges[0].Start, ranges[len(ranges)-1].End)
}

// Detail renders the source around the error's location with the location
// highlighted. Nested errors appear as annotations below their own lines, and
// distant locations render as separate hunks. A nested error whose location
// does not resolve is left out of the excerpt; [SourceError.Render] lists
// those after it.
//
// Detail renders every location that resolves and returns an error only
// when none does: the errors [SourceError.Location] returns, joined with
// those of the nested errors, or [ErrOutOfRange] for a location past the
// last line. The [printer.Printer] and the number of context lines come from opts,
// and rendering works on a private view of the source.
func (e *SourceError) Detail(opts ...DetailOption) (string, error) {
	detail, _, err := e.detail(opts)

	return detail, err
}

// detail is [SourceError.Detail] that also returns the message of every
// nested error the excerpt does not annotate, in the order the errors were
// given.
func (e *SourceError) detail(opts []DetailOption) (string, []string, error) {
	a := e.anchor()
	if a == nil {
		return "", nil, ErrNoLocation
	}

	view := e.source.Lines()

	positions, unresolved, err := e.collectPositions(a, view)

	headlines := make([]string, 0, len(unresolved))
	for _, nested := range unresolved {
		headlines = append(headlines, nested.Error())
	}

	if len(positions) == 0 {
		return "", headlines, err
	}

	return e.render(newDetailConfig(opts), view, positions), headlines, nil
}

// Render returns [SourceError.Error], then [SourceError.Detail] rendered
// with opts when a location resolves, then one line per nested error the
// detail does not annotate, each with its own unresolved location. Blank
// lines separate the parts. This is what the %+v verb prints.
func (e *SourceError) Render(opts ...DetailOption) string {
	parts := []string{e.Error()}

	detail, unresolved, err := e.detail(opts)
	if err == nil {
		parts = append(parts, detail)
	}

	if len(unresolved) > 0 {
		parts = append(parts, strings.Join(unresolved, "\n"))
	}

	return strings.Join(parts, "\n\n")
}

// errorPosition holds a resolved error position: the main error position
// (message empty) or a nested error position with its annotation text.
type errorPosition struct {
	message string
	ranges  position.Ranges
	pos     position.Position
}

// render renders view with every position highlighted, the nested ones
// annotated with their messages, and the lines around them grouped into
// hunks.
//
// Rendering happens on a private view of the source, so calling
// [SourceError.Detail] repeatedly renders the same output.
func (e *SourceError) render(cfg detailConfig, view line.Lines, positions []errorPosition) string {
	// Collect all ranges from positions and apply overlays to the view. The
	// line of each position joins the error lines as well, since a position
	// with no token under it has no range to highlight and still picks the
	// lines the excerpt shows.
	var allRanges position.Ranges

	errorLines := make([]int, 0, len(positions))

	for _, pos := range positions {
		allRanges = append(allRanges, pos.ranges...)
		errorLines = append(errorLines, pos.pos.Line)
	}

	errorLines = append(errorLines, allRanges.LineIndices()...)

	view.AddOverlay(style.GenericError, allRanges...)

	for lineIdx, annotation := range prepareLineAnnotations(positions) {
		view[lineIdx].AddAnnotation(annotation)
	}

	// Group the error lines into hunks with context around each. Errors
	// whose context windows touch share a hunk, so a line gap always
	// separates two hunks for the "..." separator. ContextSpans clamps the
	// spans to the view, so each one starts on a line the view holds.
	hunkSpans := position.ContextSpans(errorLines, cfg.contextLines, view.Len())

	// Add "..." annotations to first line of each non-first hunk.
	for i, span := range hunkSpans {
		if i == 0 || span.Start < 0 || span.Start >= view.Len() {
			continue
		}

		view[span.Start].AddAnnotation(line.Annotation{
			Content:   "...",
			Placement: line.Above,
		})
	}

	return cfg.printer.Print(view, hunkSpans...)
}

// collectPositions resolves the main location of a and the locations of its
// nested errors within view, with the ranges each highlights. A nested
// error is a chain like the main one: its location comes from the nearest
// Error in it that carries one, it resolves in the same document, and its
// annotation is its message without the path that Error puts in front,
// since the caret marks it. Nested errors whose location does not resolve
// come back separately, in order. The error joins the resolution failures,
// so it is nil when every location resolved and, when none did, says why.
func (e *SourceError) collectPositions(a *Error, view line.Lines) ([]errorPosition, []*Error, error) {
	positions := make([]errorPosition, 0, 1+len(a.errors))

	var (
		unresolved []*Error
		errs       []error
	)

	loc, err := a.locate(e)
	if err == nil {
		err = e.checkInRange(loc, view)
	}

	if err != nil {
		errs = append(errs, err)
	} else {
		positions = append(positions, errorPosition{
			pos:    loc.pos,
			ranges: highlightRanges(view, loc),
		})
	}

	for _, nested := range a.errors {
		if nested == nil {
			continue
		}

		loc, err := nested.locate(e)
		if err == nil {
			err = e.checkInRange(loc, view)
		}

		if err != nil {
			// A nested Error built from a nil error has a location and no
			// message of its own, so its resolution error stands alone.
			if nested.err != nil {
				err = fmt.Errorf("%w: %w", nested.err, err)
			}

			errs = append(errs, err)
			unresolved = append(unresolved, nested)

			continue
		}

		positions = append(positions, errorPosition{
			message: nested.message(),
			pos:     loc.pos,
			ranges:  highlightRanges(view, loc),
		})
	}

	return positions, unresolved, errors.Join(errs...)
}

// checkInRange reports [ErrOutOfRange] when loc starts on a line view does
// not hold: one past its last line, or one before its first. The message
// names the line and the lines the view holds as the text counts them, from
// 1.
func (e *SourceError) checkInRange(loc location, view line.Lines) error {
	if loc.pos.Line >= 0 && loc.pos.Line < view.Len() {
		return nil
	}

	textLine := loc.pos.Line + 1
	if view.Len() == 0 {
		return fmt.Errorf("%w: line %d of an empty source", ErrOutOfRange, textLine)
	}

	return fmt.Errorf("%w: line %d not in lines 1-%d", ErrOutOfRange, textLine, view.Len())
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
