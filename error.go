package niceyaml

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/style"
)

var (
	// ErrNoSource indicates no source was provided to resolve an error path.
	ErrNoSource = errors.New("no source provided")

	// ErrNoLocation indicates the error carries neither a path, a token, nor
	// a range.
	ErrNoLocation = errors.New("no location provided")

	// ErrTokenNotFound indicates the token was not found in the source.
	ErrTokenNotFound = errors.New("token not found in source")

	// ErrDocumentNotFound indicates the error's document index is outside the
	// documents the source parsed into.
	ErrDocumentNotFound = errors.New("document not found in source")

	// Shared [Printer] used when no [WithPrinter] is configured.
	defaultPrinter = sync.OnceValue(func() *Printer { return NewPrinter() })
)

// Error is an error that points at a location in a YAML document.
//
// The location is a [*paths.Path], a [*token.Token], or a [position.Range],
// set with [WithPath], [WithErrorToken], or [WithErrorRange]. A path resolves
// within one document of a source. [WithDocumentIndex] selects which; without
// it the first document is used. [DocumentDecoder] sets the index on every
// error it returns, and nested errors without an index of their own inherit
// the index of the error that holds them.
//
// An Error carries what a producer knows and nothing about presentation. A
// validator that knows a path uses [WithPath] and need not hold the source.
// To render the error against its document, wrap it with [Source.WrapError],
// which returns a [*SourceError] that resolves the location and renders the
// annotated excerpt. Context added with [fmt.Errorf] between the two is
// preserved.
//
// An Error is immutable once created. [Error.With] returns a copy with more
// options applied.
//
// [Error.Error] returns the message with its location: the token or range
// position as "[line:col]", or the path as "at $.path". Nested errors from
// [WithErrors] follow on their own lines as indented bullets.
//
// Error implements the error interface. Use [Error.Unwrap] with [errors.Is]
// and [errors.As] to inspect wrapped errors.
//
// Create instances with [NewError] or [NewErrorFrom].
type Error struct {
	err         error
	path        *paths.Path
	token       *token.Token
	rng         *position.Range
	errors      []*Error
	docIndex    int
	hasDocIndex bool
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
//	indexed := err.With(niceyaml.WithDocumentIndex(2))
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
//   - [WithErrorToken]
//   - [WithErrorRange]
//   - [WithDocumentIndex]
//   - [WithErrors]
type ErrorOption func(e *Error)

// WithPath is an [ErrorOption] that sets the YAML path where the error occurred.
//
// The [*paths.Path] provides both the path and whether to highlight the key
// or value.
func WithPath(p *paths.Path) ErrorOption {
	return func(e *Error) {
		e.path = p
	}
}

// WithDocumentIndex is an [ErrorOption] that sets the 0-indexed document
// the error's path resolves in. It matters only for multi-document sources.
//
// [DocumentDecoder] applies it to the errors it returns, so callers only need
// it when they build path errors for a specific document by hand.
func WithDocumentIndex(index int) ErrorOption {
	return func(e *Error) {
		e.docIndex = index
		e.hasDocIndex = true
	}
}

// WithErrorToken is an [ErrorOption] that sets the token where the error
// occurred. Only the token's position is used, so a token from a parsed AST
// works even though the parser clones tokens.
func WithErrorToken(tk *token.Token) ErrorOption {
	return func(e *Error) {
		e.token = tk
	}
}

// WithErrorRange is an [ErrorOption] that sets the 0-indexed range the error
// covers. It is the option for producers that know a location but hold no
// go-yaml token, such as a check that runs on rendered lines.
// [SourceError.Detail] highlights the whole range.
func WithErrorRange(r position.Range) ErrorOption {
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

// bulletPrefix separates a nested error line from the line above it in
// [Error.Error].
const bulletPrefix = "\n  • "

// Error returns the error message prefixed with its location.
//
// The location is "[line:col]" when the error carries a token or a range,
// "at $.path" when it carries a path, and nothing when it has none. Nested
// errors follow on their own lines as indented bullets, each with its own
// location. Nothing resolves paths here; wrap the error with
// [Source.WrapError] to see their positions.
func (e *Error) Error() string {
	if e.err == nil {
		return ""
	}

	// When the located Error sits behind foreign wrapping, that wrapping
	// already renders it.
	a, direct := e.located()
	if !direct {
		return e.err.Error()
	}

	doc := e.defaultDocumentIndex()

	return a.headline(nil, doc) + a.bullets(nil, doc)
}

// message returns [Error.Error] without the nested bullet lines. When the
// located Error sits behind foreign wrapping, it keeps the wrapper's text
// and cuts the bullets the inner Error appended from its end.
func (e *Error) message() string {
	if e.err == nil {
		return ""
	}

	a, direct := e.located()
	if direct {
		return a.headline(nil, e.defaultDocumentIndex())
	}

	return cutBullets(e.err)
}

// cutBullets returns err's message with the bullet lines cut that the
// outermost [*Error] in its chain appended. It removes only the exact
// suffix that Error appended, so a message that carries the bullet marker
// on its own keeps it.
func cutBullets(err error) string {
	msg := err.Error()

	rendered, ok := errors.AsType[*Error](err)
	if !ok {
		return msg
	}

	suffix, found := strings.CutPrefix(rendered.Error(), rendered.message())
	if !found || suffix == "" {
		return msg
	}

	trimmed, cut := strings.CutSuffix(msg, suffix)
	if !cut {
		return msg
	}

	return trimmed
}

// headline returns e's message prefixed with its location. A path resolves
// against src when one is given, in the document selected by e's own index
// or doc when e has none.
func (e *Error) headline(src *Source, doc int) string {
	loc, err := e.locate(src, doc)
	if err == nil {
		// Editors count from 1, so the headline uses 1-indexed coordinates.
		return fmt.Sprintf("[%d:%d] %v", loc.pos.Line+1, loc.pos.Col+1, e.err)
	}

	if e.path != nil {
		return fmt.Sprintf("at %s: %v", e.path, e.err)
	}

	return e.err.Error()
}

// bullets renders e's nested errors as the indented lines that follow the
// headline. Returns an empty string when there are none.
func (e *Error) bullets(src *Source, doc int) string {
	var sb strings.Builder

	for _, nested := range e.errors {
		if nested == nil || nested.err == nil {
			continue
		}

		sb.WriteString(bulletPrefix)
		sb.WriteString(nested.headline(src, doc))
	}

	return sb.String()
}

// located returns the anchor and whether e renders it itself, which is the
// case when the anchor is e or e wraps it with nothing in between.
func (e *Error) located() (*Error, bool) {
	a := e.anchor()

	return a, a == e || e.wrapsDirectly(a)
}

// find returns the first [Error] in e's chain that satisfies pred, walking
// from e inward and looking through foreign wrapping. The walk ends at the
// [Error] that carries the location, since the ones below it describe no
// position to configure. Returns nil when none matches.
func (e *Error) find(pred func(*Error) bool) *Error {
	for cur := e; ; {
		if pred(cur) {
			return cur
		}

		if cur.hasLocation() {
			return nil
		}

		inner, ok := errors.AsType[*Error](cur.err)
		if !ok {
			return nil
		}

		cur = inner
	}
}

// anchor returns the [Error] that carries the location: e itself when it has
// a token, path, range, or nested errors, otherwise the nearest such Error
// wrapped inside e. Falls back to e when none carries a location.
func (e *Error) anchor() *Error {
	a := e.find((*Error).hasLocation)
	if a == nil {
		return e
	}

	return a
}

// hasLocation reports whether e carries a token, a range, a path, or nested
// errors.
func (e *Error) hasLocation() bool {
	return e.token != nil || e.rng != nil || e.path != nil || len(e.errors) > 0
}

// wrapsDirectly reports whether target is reachable from e through [Error]
// wrappers alone, with no foreign wrapping in between. Such wrappers
// contribute no message text of their own, so e renders target itself.
func (e *Error) wrapsDirectly(target *Error) bool {
	for cur := e; ; {
		inner, ok := cur.err.(*Error) //nolint:errorlint // Identity of the direct child, not a chain search.
		if !ok {
			return false
		}

		if inner == target {
			return true
		}

		cur = inner
	}
}

// Unwrap returns the underlying errors for [errors.Is] and [errors.As].
func (e *Error) Unwrap() []error {
	if e.err == nil && len(e.errors) == 0 {
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

// Path returns the YAML path where the error occurred as a string, or an
// empty string when it carries none. It looks through wrapping to the
// [Error] that carries the location.
func (e *Error) Path() string {
	return e.anchor().pathString()
}

// pathString returns e's own path as a string, or an empty string when it
// carries none.
func (e *Error) pathString() string {
	if e.path == nil {
		return ""
	}

	return e.path.String()
}

// DocumentIndex returns the 0-indexed document the error's path resolves in
// and whether one was set with [WithDocumentIndex], on this Error or on any
// Error it wraps down to the one that carries the location. The outermost
// index wins, so context added between the two with [fmt.Errorf] never hides
// it.
func (e *Error) DocumentIndex() (int, bool) {
	a := e.find(func(c *Error) bool { return c.hasDocIndex })
	if a == nil {
		return 0, false
	}

	return a.docIndex, true
}

// documentIndex returns the document index to resolve paths in, or fallback
// when none was set with [WithDocumentIndex].
func (e *Error) documentIndex(fallback int) int {
	if e.hasDocIndex {
		return e.docIndex
	}

	return fallback
}

// defaultDocumentIndex returns the document index nested errors inherit: the
// outermost one set in e's chain, or 0.
func (e *Error) defaultDocumentIndex() int {
	index, _ := e.DocumentIndex()

	return index
}

// hasResolvableNestedErrors reports whether any nested error carries a
// location of its own.
func (e *Error) hasResolvableNestedErrors() bool {
	for _, nested := range e.errors {
		if nested != nil && (nested.token != nil || nested.rng != nil || nested.path != nil) {
			return true
		}
	}

	return false
}

// location is a resolved error location: the position the headline reports,
// and the range to highlight when the error carried one.
type location struct {
	rng *position.Range
	pos position.Position
}

// locate resolves e's own location. A range or token needs no source; a
// path resolves against src in the document e selects, or doc when e has no
// index of its own.
func (e *Error) locate(src *Source, doc int) (location, error) {
	switch {
	case e.rng != nil:
		return location{pos: e.rng.Start, rng: e.rng}, nil

	case e.token != nil:
		if e.token.Position == nil {
			return location{}, ErrTokenNotFound
		}

		return location{pos: position.NewFromToken(e.token)}, nil

	case e.path != nil:
		if src == nil {
			return location{}, ErrNoSource
		}

		file, err := src.File()
		if err != nil {
			return location{}, err
		}

		tk, err := resolveToken(file, e.path, e.documentIndex(doc))
		if err != nil {
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

// resolveToken resolves p to a token in document docIndex of file.
func resolveToken(file *ast.File, p *paths.Path, docIndex int) (*token.Token, error) {
	if file == nil {
		return nil, ErrNoSource
	}

	if docIndex < 0 || docIndex >= len(file.Docs) {
		return nil, fmt.Errorf("%w: index %d of %d", ErrDocumentNotFound, docIndex, len(file.Docs))
	}

	resolved, err := p.Token(file.Docs[docIndex])
	if err != nil {
		return nil, fmt.Errorf("path token: %w", err)
	}

	return resolved, nil
}

// SourceError is an error bound to the [*Source] it occurred in.
//
// [Source.WrapError] creates one around any error whose chain holds an
// [*Error]. It resolves the Error's location against the source, so
// [SourceError.Error] reports paths as "[line:col]" positions, and
// [SourceError.Detail] renders the surrounding lines with the location
// highlighted. The %+v verb prints both:
//
//	fmt.Printf("%+v\n", source.WrapError(err))
//
// Nested errors appear as annotations below their own lines, and distant
// locations render as separate hunks.
//
// The [Printer] and the number of context lines come from the source's
// [WithErrorOptions]. A SourceError implements the error interface and
// unwraps to the error it was created from, so [errors.Is] and [errors.As]
// see through it.
//
// Create instances with [Source.WrapError].
type SourceError struct {
	err          error
	source       *Source
	printer      *Printer
	contextLines int
}

// SourceErrorOption configures how a [SourceError] renders. Set them on a
// [Source] with [WithErrorOptions].
//
// Available options:
//   - [WithPrinter]
//   - [WithContextLines]
type SourceErrorOption func(*SourceError)

// defaultContextLines is the number of context lines shown around an error
// when [WithContextLines] is not set.
const defaultContextLines = 2

// WithContextLines is a [SourceErrorOption] that sets the number of context
// lines shown around each error location. The default is 2.
func WithContextLines(lines int) SourceErrorOption {
	return func(e *SourceError) {
		e.contextLines = lines
	}
}

// WithPrinter is a [SourceErrorOption] that sets the [*Printer] that renders
// the error detail. The printer's width, set with [WithWidth], controls word
// wrapping of the rendered detail.
func WithPrinter(p *Printer) SourceErrorOption {
	return func(e *SourceError) {
		e.printer = p
	}
}

// newSourceError binds err to src and applies opts.
func newSourceError(err error, src *Source, opts []SourceErrorOption) *SourceError {
	e := &SourceError{err: err, source: src, contextLines: defaultContextLines}
	for _, opt := range opts {
		opt(e)
	}

	return e
}

// Source returns the [*Source] the error is bound to.
func (e *SourceError) Source() *Source {
	return e.source
}

// Unwrap returns the error the [SourceError] was created from.
func (e *SourceError) Unwrap() error {
	return e.err
}

// Error returns the error message with its location resolved against the
// source: "[line:col]" for a token, range, or resolvable path, "at $.path"
// for a path that does not resolve. Nested errors follow as indented
// bullets. Context added around the [Error] with [fmt.Errorf] is kept, in
// which case the inner Error renders its own unresolved location.
//
// The result is plain text and never includes source lines, so it is safe to
// log or compare. Use [SourceError.Detail] or the %+v verb for the annotated
// source excerpt.
func (e *SourceError) Error() string {
	root, a, direct := e.located()
	if !direct {
		return e.err.Error()
	}

	doc := root.defaultDocumentIndex()

	return a.headline(e.source, doc) + a.bullets(e.source, doc)
}

// located returns the outermost [*Error] in the chain, the anchor that
// carries the location, and whether the chain reaches the anchor through
// Error values alone so that the SourceError renders it itself.
func (e *SourceError) located() (*Error, *Error, bool) {
	root, ok := e.err.(*Error) //nolint:errorlint // Identity of the direct child, not a chain search.
	if !ok {
		inner, found := errors.AsType[*Error](e.err)
		if !found {
			return nil, nil, false
		}

		anchor := inner.anchor()

		return inner, anchor, false
	}

	anchor := root.anchor()

	return root, anchor, root == anchor || root.wrapsDirectly(anchor)
}

// message returns [SourceError.Error] without the nested bullet lines, since
// the %+v form renders those as annotations in [SourceError.Detail].
func (e *SourceError) message() string {
	root, a, direct := e.located()
	if direct {
		return a.headline(e.source, root.defaultDocumentIndex())
	}

	return cutBullets(e.err)
}

// Format implements [fmt.Formatter].
//
// The %v and %s verbs print [SourceError.Error]. The %+v verb prints the
// message followed by a blank line and [SourceError.Detail], falling back to
// [SourceError.Error] when there is no detail to show. The %q verb quotes
// [SourceError.Error].
func (e *SourceError) Format(f fmt.State, verb rune) {
	switch {
	case verb == 'v' && f.Flag('+'):
		detail := e.Detail()
		if detail == "" {
			writeString(f, e.Error())

			return
		}

		writeString(f, e.message())
		writeString(f, "\n\n")
		writeString(f, detail)

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

// Detail renders the source around the error's location with the location
// highlighted. Nested errors appear as annotations below their own lines, and
// distant locations render as separate hunks.
//
// Detail returns an empty string when no location resolves. Rendering uses
// the [Printer] from [WithPrinter], or a default one, and works on a private
// view of the source.
func (e *SourceError) Detail() string {
	root, a, _ := e.located()
	if a == nil {
		return ""
	}

	doc := root.defaultDocumentIndex()

	main, err := a.locate(e.source, doc)
	if err != nil {
		slog.Debug("resolve main location for error",
			slog.String("path", a.pathString()),
			slog.Any("error", err),
		)

		// Nested errors can still render on their own.
		if !a.hasResolvableNestedErrors() {
			return ""
		}

		return e.render(a, doc, nil)
	}

	return e.render(a, doc, &main)
}

// errorPosition holds a resolved error position: the main error position
// (message empty) or a nested error position with its annotation text.
type errorPosition struct {
	message string
	ranges  position.Ranges
	pos     position.Position
}

// render renders the source with every resolved location of a highlighted:
// main, when given, as the main error without an annotation, and the nested
// errors of a with their messages.
//
// Rendering happens on a private view of the source, so calling
// [SourceError.Detail] repeatedly renders the same output.
func (e *SourceError) render(a *Error, doc int, main *location) string {
	view := e.source.Lines()
	positions := e.collectPositions(a, doc, view, main)

	// Collect all ranges from positions and apply overlays to the view.
	var allRanges position.Ranges

	for _, pos := range positions {
		allRanges = append(allRanges, pos.ranges...)
	}

	view.AddOverlay(style.GenericError, allRanges...)

	for lineIdx, annotation := range prepareLineAnnotations(positions) {
		view[lineIdx].AddAnnotation(annotation)
	}

	// Build hunk spans from all line indices covered by error ranges.
	hunkSpans := e.hunkSpans(allRanges.LineIndices(), view.Len())

	// Add "..." annotations to first line of each non-first hunk.
	for i, span := range hunkSpans {
		if i > 0 {
			view[span.Start].AddAnnotation(line.Annotation{
				Content:   "...",
				Placement: line.Above,
			})
		}
	}

	p := e.printer
	if p == nil {
		p = defaultPrinter()
	}

	return p.Print(view, hunkSpans...)
}

// collectPositions collects the main and nested positions of a that fall
// within view, with the ranges each highlights.
func (e *SourceError) collectPositions(
	a *Error, doc int, view line.Lines, main *location,
) []errorPosition {
	positions := make([]errorPosition, 0, 1+len(a.errors))

	if main != nil && main.pos.Line < view.Len() {
		positions = append(positions, errorPosition{
			pos:    main.pos,
			ranges: highlightRanges(view, *main),
		})
	}

	for _, nested := range a.errors {
		if nested == nil || nested.err == nil {
			continue
		}

		loc, err := nested.locate(e.source, doc)
		if err != nil {
			slog.Debug("resolve nested error", slog.Any("error", err))

			continue
		}

		if loc.pos.Line >= view.Len() {
			continue
		}

		positions = append(positions, errorPosition{
			message: nested.err.Error(),
			pos:     loc.pos,
			ranges:  highlightRanges(view, loc),
		})
	}

	return positions
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

// hunkSpans groups error line indices into spans based on proximity, with
// the context lines applied and clamped to totalLines. Errors merge when
// their context windows would be adjacent or overlapping, so there is
// always at least one line gap between hunks for the "..." separator.
func (e *SourceError) hunkSpans(errorLines []int, totalLines int) position.Spans {
	if len(errorLines) == 0 {
		return nil
	}

	return position.GroupIndices(errorLines, e.contextLines).
		Expand(e.contextLines).
		Clamp(0, totalLines)
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
