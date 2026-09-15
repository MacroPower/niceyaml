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

	// ErrNoPathOrToken indicates neither a path nor token was provided.
	ErrNoPathOrToken = errors.New("no path or token provided")

	// ErrTokenNotFound indicates the token was not found in the source.
	ErrTokenNotFound = errors.New("token not found in source")

	// ErrDocumentNotFound indicates the error's document index is outside the
	// documents the source parsed into.
	ErrDocumentNotFound = errors.New("document not found in source")

	// Shared [Printer] used when no [WithPrinter] is configured.
	defaultPrinter = sync.OnceValue(func() *Printer { return NewPrinter() })
)

// Error represents a YAML error with optional source annotation.
//
// To enable annotated error output that shows the relevant YAML location, provide:
//   - [WithErrorToken] directly specifies the error location, OR
//   - [WithPath] combined with [WithSource] to resolve the path
//
// A path resolves within one document of the source. [WithDocumentIndex]
// selects which; without it the first document is used. [DocumentDecoder]
// sets the index on every error it returns, and nested errors without an
// index of their own inherit the index of the error that holds them.
//
// An Error is immutable once created. Since these conditions must only be
// satisfied before calling [Error.Detail], callers attach what they know
// later with [Error.With], which returns a copy, or with [Source.WrapError],
// which wraps the error in a new Error that carries the source. Rendering
// looks through that wrapping to find the Error holding the location, so
// context added with [fmt.Errorf] between the two is preserved.
//
// This means that callers may optionally attach any additional context that
// original [Error] producers might lack, thus avoiding the need for producers
// to take on any more responsibility than they need to. For example, a
// [SchemaValidator] that produces [Error] values will be path-aware, and thus
// should use [WithPath], but it will likely not have access to the [Source].
//
// [Error.Error] returns a plain, single-purpose message with the location,
// suitable for logs. [Error.Detail] renders the annotated source excerpt,
// and the %+v verb prints both:
//
//	fmt.Printf("%+v\n", source.WrapError(err))
//
// Error implements the error interface. Use [Error.Unwrap] with [errors.Is]
// and [errors.As] to inspect wrapped errors.
//
// Create instances with [NewError] or [NewErrorFrom].
type Error struct {
	err             error
	printer         *Printer
	source          *Source
	path            *paths.Path
	token           *token.Token
	errors          []*Error
	contextLines    int
	docIndex        int
	hasContextLines bool
	hasDocIndex     bool
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
//	fmt.Printf("%+v\n", err.With(niceyaml.WithSource(source)))
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
//   - [WithContextLines]
//   - [WithPath]
//   - [WithDocumentIndex]
//   - [WithErrorToken]
//   - [WithPrinter]
//   - [WithSource]
//   - [WithErrors]
type ErrorOption func(e *Error)

// defaultContextLines is the number of context lines shown around an error
// when [WithContextLines] is not set.
const defaultContextLines = 2

// WithContextLines is an [ErrorOption] that sets the number of context lines to
// show around the error. The default is 2.
func WithContextLines(lines int) ErrorOption {
	return func(e *Error) {
		e.contextLines = lines
		e.hasContextLines = true
	}
}

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

// WithErrorToken is an [ErrorOption] that sets the token where the error occurred.
func WithErrorToken(tk *token.Token) ErrorOption {
	return func(e *Error) {
		e.token = tk
	}
}

// WithPrinter is an [ErrorOption] that sets the [*Printer] used for
// formatting the error source. The printer's width, set with [WithWidth],
// controls word wrapping of the rendered detail.
func WithPrinter(p *Printer) ErrorOption {
	return func(e *Error) {
		e.printer = p
	}
}

// WithSource is an [ErrorOption] that sets the [*Source] the error renders
// from. Paths resolve against it, and tokens are located in it by position.
//
// Without a source, an error that carries a token renders from the token's
// own chain instead, which rebuilds the view on every [Error.Detail] call.
func WithSource(src *Source) ErrorOption {
	return func(e *Error) {
		e.source = src
	}
}

// WithErrors is an [ErrorOption] that adds nested errors to the [Error].
//
// Each nested error has its own YAML path or token and is rendered as an
// annotation below its resolved line.
func WithErrors(errs ...*Error) ErrorOption {
	return func(e *Error) {
		e.errors = append(e.errors, errs...)
	}
}

// bulletMarker opens a nested error line in [Error.Error], and bulletPrefix
// is the same marker with the newline that separates it from the line above.
const (
	bulletMarker = "  \u2022 "
	bulletPrefix = "\n" + bulletMarker
)

// Error returns the error message prefixed with its location.
//
// The location is the token position as "[line:col]" when the error carries
// a token or its path resolves against the attached [Source], the path as
// "at $.path" when it does not, and nothing when the error has neither.
// Nested errors follow on their own lines as indented bullets, each with its
// own location.
//
// The result is plain text and never includes source lines, so it is safe to
// log or compare. Use [Error.Detail] or the %+v verb for the annotated
// source excerpt.
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

	src := e.effectiveSource()

	return e.headline(a, src) + e.bullets(a, src)
}

// bullets renders the nested errors of a, the located Error, as the indented
// lines [Error.Error] appends after the headline. Returns an empty string when
// a holds none.
func (e *Error) bullets(a *Error, src *Source) string {
	var sb strings.Builder

	for _, nested := range a.errors {
		if nested == nil || nested.err == nil {
			continue
		}

		sb.WriteString(bulletPrefix)
		sb.WriteString(e.headline(nested, src))
	}

	return sb.String()
}

// message returns [Error.Error] without the nested bullet lines: the headline
// plus whatever context wraps it. The %+v form renders those nested errors as
// annotations in [Error.Detail] instead, so repeating them would be noise.
func (e *Error) message() string {
	if e.err == nil {
		return ""
	}

	a, direct := e.located()
	if direct {
		return e.headline(a, e.effectiveSource())
	}

	// Foreign wrapping rendered an Error of its own, bullets and all. Strip
	// exactly the bullets that Error appended rather than cutting at the first
	// bullet marker, which a message can carry on its own. A wrapper that added
	// text after the error leaves no such suffix, so the message keeps its
	// bullets.
	msg := e.err.Error()

	rendered, ok := errors.AsType[*Error](e.err)
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
// a token, path, or nested errors, otherwise the nearest such Error wrapped
// inside e. Falls back to e when none carries a location.
func (e *Error) anchor() *Error {
	a := e.find((*Error).hasLocation)
	if a == nil {
		return e
	}

	return a
}

// hasLocation reports whether e carries a token, a path, or nested errors.
func (e *Error) hasLocation() bool {
	return e.token != nil || e.path != nil || len(e.errors) > 0
}

// wrapsDirectly reports whether target is reachable from e through [Error]
// wrappers alone, with no foreign wrapping in between. Such wrappers
// contribute no message text of their own, so e renders target itself and
// resolves it against its own source rather than delegating.
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

// headline returns target's message prefixed with its location. Paths resolve
// against src, which is e's effective source, and against e's document index
// when target has none of its own.
func (e *Error) headline(target *Error, src *Source) string {
	tk, err := e.resolveIn(target, src)
	if err == nil && tk != nil && tk.Position != nil {
		return fmt.Sprintf("[%s] %v", position.NewFromToken(tk), target.err)
	}

	if target.path != nil {
		return fmt.Sprintf("at %s: %v", target.path.Path(), target.err)
	}

	return target.err.Error()
}

// Detail renders the source around the error's location with the location
// highlighted. Nested errors appear as annotations below their own lines, and
// distant locations render as separate hunks.
//
// Detail returns an empty string when no location resolves. Rendering uses the
// [Printer] from [WithPrinter], or a default one, and never modifies the
// [Source].
func (e *Error) Detail() string {
	if e.err == nil {
		return ""
	}

	a := e.anchor()
	src := e.effectiveSource()

	mainToken, err := e.resolveIn(a, src)
	if err != nil {
		slog.Debug("resolve main token for error",
			slog.String("path", a.pathString()),
			slog.Any("error", err),
		)

		// Nested errors can still render on their own when a source is present.
		if src == nil || !a.hasResolvableNestedErrors() {
			return ""
		}
	}

	return e.renderErrorSource(a, src, mainToken)
}

// effectiveSource returns the outermost source set in e's chain, or nil when
// none is set.
func (e *Error) effectiveSource() *Source {
	a := e.find(func(c *Error) bool { return c.source != nil })
	if a == nil {
		return nil
	}

	return a.source
}

// effectivePrinter returns the outermost printer set in e's chain, or the
// default.
func (e *Error) effectivePrinter() *Printer {
	a := e.find(func(c *Error) bool { return c.printer != nil })
	if a == nil {
		return defaultPrinter()
	}

	return a.printer
}

// effectiveContextLines returns the outermost context lines set in e's chain,
// or the default.
func (e *Error) effectiveContextLines() int {
	a := e.find(func(c *Error) bool { return c.hasContextLines })
	if a == nil {
		return defaultContextLines
	}

	return a.contextLines
}

// Format implements [fmt.Formatter].
//
// The %v and %s verbs print [Error.Error]. The %+v verb prints the headline
// followed by a blank line and [Error.Detail], falling back to [Error.Error]
// when there is no detail to show. The %q verb quotes [Error.Error].
func (e *Error) Format(f fmt.State, verb rune) {
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

// writeString writes s to f. Write errors are dropped, as [fmt] itself does
// for a [fmt.Formatter].
func writeString(f fmt.State, s string) {
	_, _ = io.WriteString(f, s) //nolint:errcheck // Formatter has no error channel.
}

// resolveIn resolves target's location against src, which is always the
// source the rendered view is built from, so a resolved position indexes the
// lines it will highlight. A token is used directly. A path resolves in the
// document selected by target's index, or e's index when target has none.
func (e *Error) resolveIn(target *Error, src *Source) (*token.Token, error) {
	if target.token != nil {
		return target.token, nil
	}

	if target.path == nil {
		return nil, ErrNoPathOrToken
	}

	if src == nil {
		return nil, ErrNoSource
	}

	file, err := src.File()
	if err != nil {
		return nil, err
	}

	return resolveToken(file, nil, target.path, target.documentIndex(e.defaultDocumentIndex()))
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

// hasResolvableNestedErrors checks if any nested error has a path or token.
func (e *Error) hasResolvableNestedErrors() bool {
	for _, nested := range e.errors {
		if nested != nil && (nested.token != nil || nested.path != nil) {
			return true
		}
	}

	return false
}

// Unwrap returns the underlying errors, enabling [errors.Is] and [errors.As].
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

// Path returns the [*paths.Path] path where the error occurred as a string.
// It looks through wrapping to the [Error] that carries the location.
func (e *Error) Path() string {
	return e.anchor().pathString()
}

// pathString returns e's own path as a string, or an empty string when it
// carries none.
func (e *Error) pathString() string {
	if e.path == nil {
		return ""
	}

	return e.path.Path().String()
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

// errorPosition holds information about a resolved error position.
//
// It represents either the main error position (message empty) or a nested
// error position with annotation text.
type errorPosition struct {
	message string            // Error message for annotation (empty for main error).
	ranges  []position.Range  // Ranges for this error's highlighting.
	pos     position.Position // Position for highlighting and annotation placement.
}

// buildHunkSpans groups error line indices into spans based on proximity.
//
// Errors are merged when their context windows would be adjacent or
// overlapping. This ensures there's always at least one line gap between
// hunks for the "..." separator.
//
// Returns spans with contextLines context applied and clamped to totalLines.
func (e *Error) buildHunkSpans(errorLines []int, totalLines int) position.Spans {
	if len(errorLines) == 0 {
		return nil
	}

	// Sort error lines.
	sorted := slices.Clone(errorLines)
	slices.Sort(sorted)

	// Group indices, expand by context, clamp to valid range.
	context := e.effectiveContextLines()

	return position.GroupIndices(sorted, context).
		Expand(context).
		Clamp(0, totalLines)
}

// prepareLineAnnotations prepares annotations grouped by original line index.
// Only positions with messages (non-main errors) are included.
func prepareLineAnnotations(positions []errorPosition) map[int]line.Annotation {
	// Group errors by line to combine messages.
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
			if r.pos.Col < minCol {
				minCol = r.pos.Col
			}
		}

		result[lineIdx] = line.Annotation{
			Content:   strings.Join(messages, "; "),
			Placement: line.Below,
			Col:       minCol,
		}
	}

	return result
}

// resolveNestedError resolves a single nested error's path or token against
// src, and checks that the resulting position falls within view.
func (e *Error) resolveNestedError(src *Source, view line.Lines, nested *Error) (errorPosition, error) {
	tk, err := e.resolveIn(nested, src)
	if err != nil {
		return errorPosition{}, err
	}

	if tk == nil || tk.Position == nil {
		return errorPosition{}, ErrTokenNotFound
	}

	pos := position.NewFromToken(tk)
	if pos.Line >= view.Len() {
		return errorPosition{}, ErrTokenNotFound
	}

	return errorPosition{
		message: nested.err.Error(),
		pos:     pos,
	}, nil
}

// collectErrorPositions collects all error positions (main and nested) into a
// unified slice.
//
// Paths resolve against src, and view supplies the ranges. If mainToken is
// provided, it becomes the first position without a message. The nested
// errors of a, the located Error, follow with their messages.
func (e *Error) collectErrorPositions(a *Error, src *Source, view line.Lines, mainToken *token.Token) []errorPosition {
	positions := make([]errorPosition, 0, 1+len(a.errors))

	// Add main error position if token is provided. The token is located by
	// position rather than identity, since a token from the parsed AST is a
	// clone of the one the view was built from.
	if mainToken != nil && mainToken.Position != nil {
		pos := position.NewFromToken(mainToken)
		if pos.Line < view.Len() {
			positions = append(positions, errorPosition{
				pos:    pos,
				ranges: view.ContentRanges(view.TokenAt(pos)),
			})
		}
	}

	// Add nested error positions.
	for _, nested := range a.errors {
		if nested == nil || nested.err == nil {
			continue
		}

		r, resolveErr := e.resolveNestedError(src, view, nested)
		if resolveErr != nil {
			slog.Debug("resolve nested error",
				slog.Any("error", resolveErr),
			)

			continue
		}

		r.ranges = view.ContentRanges(view.TokenAt(r.pos))
		positions = append(positions, r)
	}

	return positions
}

// renderErrorSource renders src with all error positions highlighted. It
// highlights mainToken, when provided, as the main error without an
// annotation, and annotates the nested errors of a, the located Error.
//
// Rendering happens on a clone of the source's [line.Lines] view, so
// renderErrorSource never mutates the source and calling [Error.Detail]
// repeatedly renders the same output. Without a source, the view is rebuilt
// from mainToken's chain on every call.
func (e *Error) renderErrorSource(a *Error, src *Source, mainToken *token.Token) string {
	p := e.effectivePrinter()

	if src == nil {
		src = NewSourceFromToken(mainToken)
	}

	view := src.Lines().Clone()

	positions := e.collectErrorPositions(a, src, view, mainToken)

	// Collect all ranges from positions and apply overlays to the view.
	var allRanges position.Ranges

	for _, pos := range positions {
		allRanges = append(allRanges, pos.ranges...)
	}

	view.AddOverlay(style.GenericError, allRanges...)

	// Apply annotations to the view's lines.
	lineAnnotations := prepareLineAnnotations(positions)
	for lineIdx, annotation := range lineAnnotations {
		view[lineIdx].AddAnnotation(annotation)
	}

	// Build hunk spans from all line indices covered by error ranges.
	hunkSpans := e.buildHunkSpans(allRanges.LineIndices(), view.Len())

	// Add "..." annotations to first line of each non-first hunk.
	for i, span := range hunkSpans {
		if i > 0 {
			view[span.Start].AddAnnotation(line.Annotation{
				Content:   "...",
				Placement: line.Above,
			})
		}
	}

	// Print the view with all hunk spans.
	return p.Print(view, hunkSpans...)
}

// resolveToken resolves a token from either a direct token or path.
//
// A path resolves in document docIndex of file, which is required when p is
// non-nil.
func resolveToken(file *ast.File, tk *token.Token, p *paths.Path, docIndex int) (*token.Token, error) {
	if tk != nil {
		return tk, nil
	}

	if p != nil {
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

	return nil, ErrNoPathOrToken
}
