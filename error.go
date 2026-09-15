package niceyaml

import (
	"errors"
	"fmt"
	"io"
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
	// ErrNoLocation indicates the error carries neither a path, a token, nor
	// a range. [SourceError.Location] and [SourceError.Detail] return it.
	ErrNoLocation = errors.New("no location provided")

	// ErrTokenNotFound indicates the error's token, or the token its path
	// resolves to, carries no position. [SourceError.Location] and
	// [SourceError.Detail] return it.
	ErrTokenNotFound = errors.New("token not found in source")

	// ErrDocumentNotFound indicates the error's document index is outside the
	// documents the source parsed into. [SourceError.Location] and
	// [SourceError.Detail] return it.
	ErrDocumentNotFound = errors.New("document not found in source")

	// ErrOutOfRange indicates the error's location lies past the last line
	// of the source, which happens when a token or range came from other
	// text. [SourceError.Detail] returns it.
	ErrOutOfRange = errors.New("location outside source")

	// The resolution error for a path location with no source to resolve it
	// in, which is the case in [Error.Error].
	errNoSource = errors.New("no source provided")

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
// [WithErrors] are not part of the message. They surface through
// [Error.Unwrap], and [SourceError.Detail] renders them as annotations.
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

// Error returns the error message prefixed with its location.
//
// The location is "[line:col]" when the error carries a token or a range,
// "at $.path" when it carries a path, and nothing when it has none. Nothing
// resolves paths here; wrap the error with [Source.WrapError] to see their
// positions. Nested errors from [WithErrors] are not part of the message.
func (e *Error) Error() string {
	return e.headline(nil, 0)
}

// headline returns e's message prefixed with its location. A path resolves
// against src when one is given, in document doc. An Error built from a nil
// error has an empty headline.
//
// An Error without a position of its own that directly wraps another Error
// adds no text, so the headline is the inner Error's, resolved the same
// way. Foreign wrapping in between renders the inner Error itself, with its
// position unresolved.
func (e *Error) headline(src *Source, doc int) string {
	if e.err == nil {
		return ""
	}

	inner, ok := e.err.(*Error) //nolint:errorlint // Identity of the direct child, not a chain search.
	if ok && !e.hasPosition() {
		return inner.headline(src, doc)
	}

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

// hasPosition reports whether e carries a token, a range, or a path of its
// own.
func (e *Error) hasPosition() bool {
	return e.token != nil || e.rng != nil || e.path != nil
}

// hasLocation reports whether e carries a position or nested errors.
func (e *Error) hasLocation() bool {
	return e.hasPosition() || len(e.errors) > 0
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

// documentIndex returns e's own document index, or fallback when
// [WithDocumentIndex] did not set one. A nested error resolves in its own
// index when it carries one and otherwise in the one the outer chain
// selects.
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

// location is a resolved error location: the position the headline reports,
// and the range to highlight when the error carried one.
type location struct {
	rng *position.Range
	pos position.Position
}

// locate resolves e's location. A range or token needs no source; a path
// resolves against src in document doc. An Error without a position of its
// own that directly wraps another Error takes that Error's location.
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
			return location{}, errNoSource
		}

		file, err := src.File()
		if err != nil {
			return location{}, err
		}

		tk, err := resolveToken(file, e.path, doc)
		if err != nil {
			return location{}, err
		}

		if tk == nil || tk.Position == nil {
			return location{}, ErrTokenNotFound
		}

		return location{pos: position.NewFromToken(tk)}, nil

	default:
		inner, ok := e.err.(*Error) //nolint:errorlint // Identity of the direct child, not a chain search.
		if ok {
			return inner.locate(src, doc)
		}

		return location{}, ErrNoLocation
	}
}

// resolveToken resolves p to a token in document docIndex of file.
func resolveToken(file *ast.File, p *paths.Path, docIndex int) (*token.Token, error) {
	if file == nil {
		return nil, errNoSource
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
// [SourceError.Error] reports paths as "[line:col]" positions,
// [SourceError.Location] returns the resolved range, and
// [SourceError.Detail] renders the surrounding lines with the location
// highlighted. The %+v verb prints the message and the detail:
//
//	fmt.Printf("%+v\n", source.WrapError(err))
//
// Nested errors appear as annotations below their own lines, and distant
// locations render as separate hunks.
//
// [SourceError.Render] returns what %+v prints, and both it and
// [SourceError.Detail] accept [DetailOption] values for the [Printer] and the
// number of context lines, so the caller that renders the error decides how
// it looks. A SourceError implements the error interface and unwraps to the
// error it was created from, so [errors.Is] and [errors.As] see through it.
//
// Create instances with [Source.WrapError].
type SourceError struct {
	err    error
	source *Source
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
	printer      *Printer
	contextLines int
}

// newDetailConfig applies opts over the defaults: the shared default
// [Printer] and [defaultContextLines].
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
// shown around each error location. The default is 2.
func WithContextLines(lines int) DetailOption {
	return func(c *detailConfig) {
		c.contextLines = lines
	}
}

// WithPrinter is a [DetailOption] that sets the [*Printer] that renders the
// source excerpt. The printer's width, set with [WithWidth], controls word
// wrapping, and its styles color the highlighted locations. The default is a
// [Printer] from [NewPrinter].
func WithPrinter(p *Printer) DetailOption {
	return func(c *detailConfig) {
		c.printer = p
	}
}

// newSourceError binds err to src.
func newSourceError(err error, src *Source) *SourceError {
	return &SourceError{err: err, source: src}
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
// for a path that does not resolve. Context added around the [Error] with
// [fmt.Errorf] is kept, in which case the inner Error renders its own
// unresolved location. Nested errors are not part of the message; see
// [SourceError.Detail].
//
// The result is plain text and never includes source lines, so it is safe to
// log or compare. Use [SourceError.Detail] or the %+v verb for the annotated
// source excerpt.
func (e *SourceError) Error() string {
	root, ok := e.err.(*Error) //nolint:errorlint // Identity of the direct child, not a chain search.
	if !ok {
		return e.err.Error()
	}

	return root.headline(e.source, root.defaultDocumentIndex())
}

// located returns the outermost [*Error] in the chain and the anchor that
// carries the location. Both are nil when the chain holds no Error or its
// first Error is a nil pointer.
func (e *SourceError) located() (*Error, *Error) {
	root, ok := firstError(e.err)
	if !ok {
		return nil, nil
	}

	a := root.anchor()

	return root, a
}

// firstError returns the first [*Error] in err's chain. It reports false when
// the chain holds none or when that Error is a nil pointer, which has no
// message or location to render.
func firstError(err error) (*Error, bool) {
	e, ok := errors.AsType[*Error](err)

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
//
// It returns [ErrNoLocation] when the error carries no location,
// [ErrTokenNotFound] when the token has no position, [ErrDocumentNotFound]
// when the document index is outside the source, and the resolution error
// from [go.jacobcolvin.com/niceyaml/paths] when a path does not resolve.
func (e *SourceError) Location() (position.Range, error) {
	root, a := e.located()
	if a == nil {
		return position.Range{}, ErrNoLocation
	}

	loc, err := a.locate(e.source, root.defaultDocumentIndex())
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
// last line. The [Printer] and the number of context lines come from opts,
// and rendering works on a private view of the source.
func (e *SourceError) Detail(opts ...DetailOption) (string, error) {
	detail, _, err := e.detail(opts)

	return detail, err
}

// detail is [SourceError.Detail] that also returns the headline of every
// nested error the excerpt does not annotate, in the order the errors were
// given.
func (e *SourceError) detail(opts []DetailOption) (string, []string, error) {
	root, a := e.located()
	if a == nil {
		return "", nil, ErrNoLocation
	}

	view := e.source.Lines()
	doc := root.defaultDocumentIndex()

	positions, unresolved, err := e.collectPositions(a, doc, view)

	headlines := make([]string, 0, len(unresolved))
	for _, nested := range unresolved {
		headlines = append(headlines, nested.headline(e.source, nested.documentIndex(doc)))
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
func (e *SourceError) render(cfg detailConfig, view Lines, positions []errorPosition) string {
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
	hunkSpans := hunkSpans(allRanges.LineIndices(), cfg.contextLines, view.Len())

	// Add "..." annotations to first line of each non-first hunk.
	for i, span := range hunkSpans {
		if i > 0 {
			view[span.Start].AddAnnotation(line.Annotation{
				Content:   "...",
				Placement: line.Above,
			})
		}
	}

	return cfg.printer.Print(view, hunkSpans...)
}

// collectPositions resolves the main location of a and the locations of its
// nested errors within view, with the ranges each highlights. Nested errors
// whose location does not resolve come back separately, in order. The error
// joins the resolution failures, so it is nil when every location resolved
// and, when none did, says why.
func (e *SourceError) collectPositions(a *Error, doc int, view Lines) ([]errorPosition, []*Error, error) {
	positions := make([]errorPosition, 0, 1+len(a.errors))

	var (
		unresolved []*Error
		errs       []error
	)

	loc, err := a.locate(e.source, doc)
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
		if nested == nil || nested.err == nil {
			continue
		}

		loc, err := nested.locate(e.source, nested.documentIndex(doc))
		if err == nil {
			err = e.checkInRange(loc, view)
		}

		if err != nil {
			errs = append(errs, fmt.Errorf("%w: %w", nested.err, err))
			unresolved = append(unresolved, nested)

			continue
		}

		positions = append(positions, errorPosition{
			message: nested.err.Error(),
			pos:     loc.pos,
			ranges:  highlightRanges(view, loc),
		})
	}

	return positions, unresolved, errors.Join(errs...)
}

// checkInRange reports [ErrOutOfRange] when loc starts past the last line
// of view.
func (e *SourceError) checkInRange(loc location, view Lines) error {
	if loc.pos.Line >= view.Len() {
		return fmt.Errorf("%w: line %d of %d", ErrOutOfRange, loc.pos.Line+1, view.Len())
	}

	return nil
}

// highlightRanges returns the ranges to highlight for loc: the range itself
// when the error carried one, otherwise the content of the token at its
// position.
func highlightRanges(view Lines, loc location) position.Ranges {
	if loc.rng != nil {
		return position.Ranges{*loc.rng}
	}

	return view.ContentRanges(view.TokenAt(loc.pos))
}

// hunkSpans groups error line indices into spans based on proximity, with
// contextLines lines of context applied and clamped to totalLines. Errors
// merge when their context windows would be adjacent or overlapping, so
// there is always at least one line gap between hunks for the "..."
// separator.
func hunkSpans(errorLines []int, contextLines, totalLines int) position.Spans {
	if len(errorLines) == 0 {
		return nil
	}

	return position.GroupIndices(errorLines, contextLines).
		Expand(contextLines).
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
