package niceyaml

import (
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

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
)

// Error represents a YAML error with optional source annotation.
//
// To enable annotated error output that shows the relevant YAML location, provide:
//   - [WithErrorToken] directly specifies the error location, OR
//   - [WithPath] combined with [WithSource] to resolve the path
//
// Since these conditions must only be satisfied before calling [Error.Error],
// you may use [Error.SetOption] to supply them at any time and in any context
// before then.
//
// This means that callers may optionally attach any additional context that
// original [Error] producers might lack, thus avoiding the need for producers
// to take on any more responsibility than they need to.
//
// For example, a [SchemaValidator] that produces [Error] values will be
// path-aware, and thus should use [WithPath], but it will likely not have
// access to the [Source].
//
// For convenience, [Source.WrapError] can be used if you only need to add the
// [Source] without any other [ErrorOption] values.
//
// Error implements the error interface. Use [Error.Unwrap] with [errors.Is]
// and [errors.As] to inspect wrapped errors.
//
// Create instances with [NewError] or [NewErrorFrom].
type Error struct {
	err         error
	printer     WrappingPrinter
	source      *Source
	path        *paths.Path
	token       *token.Token
	widthFunc   func() int
	errors      []*Error
	sourceLines int
	width       int
}

// NewError creates a new [*Error] with the given message.
// Use [NewErrorFrom] instead if wrapping an existing error.
func NewError(msg string, opts ...ErrorOption) *Error {
	return NewErrorFrom(errors.New(msg), opts...)
}

// NewErrorFrom creates a new [*Error] wrapping an existing error.
// Use [NewError] instead if creating an error from a message string.
func NewErrorFrom(err error, opts ...ErrorOption) *Error {
	e := &Error{
		err:         err,
		sourceLines: 2,
	}
	e.SetOption(opts...)

	return e
}

// ErrorOption configures an [Error].
//
// Available options:
//   - [WithSourceLines]
//   - [WithPath]
//   - [WithErrorToken]
//   - [WithPrinter]
//   - [WithSource]
//   - [WithWidthFunc]
//   - [WithErrors]
type ErrorOption func(e *Error)

// WithSourceLines is an [ErrorOption] that sets the number of context lines to
// show around the error.
func WithSourceLines(lines int) ErrorOption {
	return func(e *Error) {
		e.sourceLines = lines
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

// WithErrorToken is an [ErrorOption] that sets the token where the error occurred.
func WithErrorToken(tk *token.Token) ErrorOption {
	return func(e *Error) {
		e.token = tk
	}
}

// WithPrinter is an [ErrorOption] that sets the [WrappingPrinter] used for
// formatting the error source.
func WithPrinter(p WrappingPrinter) ErrorOption {
	return func(e *Error) {
		e.printer = p
	}
}

// WithSource is an [ErrorOption] that sets the [*Source] for resolving the
// error path.
func WithSource(src *Source) ErrorOption {
	return func(e *Error) {
		e.source = src
	}
}

// WithWidthFunc is an [ErrorOption] that sets a function to determine the width
// for word wrapping.
// This takes precedence over [Error.SetWidth] when both are configured.
func WithWidthFunc(fn func() int) ErrorOption {
	return func(e *Error) {
		e.widthFunc = fn
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

// Error returns the error message with source annotation if available.
func (e Error) Error() string {
	if e.err == nil {
		return ""
	}

	// Try to resolve main token for position display.
	mainToken, err := e.resolveMainToken()
	if err != nil {
		pathStr := ""
		if e.path != nil {
			pathStr = e.path.Path().String()
		}

		slog.Debug("resolve main token for error",
			slog.String("path", pathStr),
			slog.Any("error", err),
		)

		// Check if we can still render via nested errors (nested-only case).
		if e.source == nil || !e.hasResolvableNestedErrors() {
			if pathStr != "" {
				return fmt.Sprintf("at %s: %v", pathStr, e.err)
			}

			return e.formatPlainError()
		}

		// Proceed with nested-only rendering (mainToken stays nil).
	}

	// Build the error header.
	var header string
	if mainToken != nil {
		pos := position.NewFromToken(mainToken)
		header = fmt.Sprintf("[%s] %v:\n", pos.String(), e.err)
	} else {
		header = fmt.Sprintf("%v:\n", e.err)
	}

	// Render the source with error positions highlighted.
	return header + "\n" + e.renderErrorSource(mainToken)
}

// formatPlainError formats the error without source annotation.
// Nested errors are rendered as bullet points if present.
func (e Error) formatPlainError() string {
	if len(e.errors) == 0 {
		return e.err.Error()
	}

	var sb strings.Builder

	sb.WriteString(e.err.Error())

	for _, nested := range e.errors {
		if nested != nil && nested.err != nil {
			sb.WriteString("\n  • ")
			sb.WriteString(nested.err.Error())
		}
	}

	return sb.String()
}

// resolveMainToken resolves the main error's token for position display.
// Returns the token or an error if the main error has no resolvable path/token.
func (e *Error) resolveMainToken() (*token.Token, error) {
	// Direct token doesn't need file (Source comes from token's Origin).
	if e.token != nil {
		return e.token, nil
	}

	// Path resolution requires a valid file.
	if e.path == nil {
		return nil, ErrNoPathOrToken
	}

	file, err := e.getFile()
	if err != nil {
		return nil, err
	}

	return resolveToken(file, nil, e.path)
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

// SetOption applies the provided [ErrorOption] values to the [Error].
func (e *Error) SetOption(opts ...ErrorOption) {
	for _, opt := range opts {
		opt(e)
	}
}

// SetWidth sets the width for word wrapping of the error output.
// A width of 0 disables wrapping.
func (e *Error) SetWidth(width int) {
	e.width = width
}

// getFile returns the parsed AST file from the source.
func (e *Error) getFile() (*ast.File, error) {
	if e.source == nil {
		return nil, ErrNoSource
	}

	return e.source.File()
}

// getPrinter returns the configured printer, or a default if none was set.
// If a width is configured, it applies the width to the printer.
func (e *Error) getPrinter() WrappingPrinter {
	width := e.width
	if e.widthFunc != nil {
		width = e.widthFunc()
	}

	if e.printer != nil {
		e.printer.SetWidth(width)

		return e.printer
	}

	p := NewPrinter()
	p.SetWidth(width)

	e.printer = p

	return p
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
func (e *Error) Path() string {
	if e.path != nil {
		return e.path.Path().String()
	}

	return ""
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
// Returns spans with sourceLines context applied and clamped to totalLines.
func (e *Error) buildHunkSpans(errorLines []int, totalLines int) position.Spans {
	if len(errorLines) == 0 {
		return nil
	}

	// Sort error lines.
	sorted := slices.Clone(errorLines)
	slices.Sort(sorted)

	// Group indices, expand by context, clamp to valid range.
	return position.GroupIndices(sorted, e.sourceLines).
		Expand(e.sourceLines).
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
			Content:  strings.Join(messages, "; "),
			Position: line.Below,
			Col:      minCol,
		}
	}

	return result
}

// resolveNestedError resolves a single nested error's path or token.
func (e *Error) resolveNestedError(t *Source, nested *Error) (errorPosition, error) {
	file, err := t.File()
	if err != nil {
		return errorPosition{}, fmt.Errorf("parse source: %w", err)
	}

	tk, err := resolveToken(file, nested.token, nested.path)
	if err != nil {
		return errorPosition{}, err
	}

	if tk == nil || tk.Position == nil {
		return errorPosition{}, ErrTokenNotFound
	}

	pos := position.NewFromToken(tk)
	if pos.Line >= t.Len() {
		return errorPosition{}, ErrTokenNotFound
	}

	return errorPosition{
		message: nested.err.Error(),
		pos:     pos,
	}, nil
}

// collectErrorPositions collects all error positions (main and nested) into a
// unified slice.
// If mainToken is provided, it becomes the first position without a message.
// Nested errors are appended with their messages.
func (e *Error) collectErrorPositions(t *Source, mainToken *token.Token) []errorPosition {
	positions := make([]errorPosition, 0, 1+len(e.errors))

	// Add main error position if token is provided.
	if mainToken != nil && mainToken.Position != nil {
		pos := position.NewFromToken(mainToken)
		if pos.Line < t.Len() {
			positions = append(positions, errorPosition{
				pos:    pos,
				ranges: t.ContentPositionRangesFromToken(mainToken),
			})
		}
	}

	// Add nested error positions.
	for _, nested := range e.errors {
		if nested == nil || nested.err == nil {
			continue
		}

		r, resolveErr := e.resolveNestedError(t, nested)
		if resolveErr != nil {
			slog.Debug("resolve nested error",
				slog.Any("error", resolveErr),
			)

			continue
		}

		r.ranges = t.ContentPositionRanges(r.pos)
		positions = append(positions, r)
	}

	return positions
}

// renderErrorSource renders the error source with all error positions highlighted.
// MainToken (if provided) is highlighted as the main error without annotation.
// Source is created from mainToken when available, otherwise from e.source.
func (e *Error) renderErrorSource(mainToken *token.Token) string {
	p := e.getPrinter()

	var t *Source
	if mainToken != nil {
		// Create Source from token to ensure position alignment.
		t = NewSourceFromToken(mainToken)
	} else {
		// Nested-only case: use existing source directly.
		t = e.source
	}

	positions := e.collectErrorPositions(t, mainToken)

	// Collect all ranges from positions and apply overlays directly.
	var allRanges position.Ranges
	for _, pos := range positions {
		allRanges = append(allRanges, pos.ranges...)
	}

	t.AddOverlay(style.GenericError, allRanges...)

	// Apply annotations directly to source lines.
	lineAnnotations := prepareLineAnnotations(positions)
	for lineIdx, annotation := range lineAnnotations {
		t.Line(lineIdx).AddAnnotation(annotation)
	}

	// Build hunk spans from all line indices covered by error ranges.
	hunkSpans := e.buildHunkSpans(allRanges.LineIndices(), t.Len())

	// Add "..." annotations to first line of each non-first hunk.
	for i, span := range hunkSpans {
		if i > 0 {
			t.Line(span.Start).AddAnnotation(line.Annotation{
				Content:  "...",
				Position: line.Above,
			})
		}
	}

	// Print source with all hunk spans.
	return p.Print(t, hunkSpans...)
}

// resolveToken resolves a token from either a direct token or path.
// The file parameter is required when pg is non-nil.
func resolveToken(file *ast.File, tk *token.Token, p *paths.Path) (*token.Token, error) {
	if tk != nil {
		return tk, nil
	}

	if p != nil {
		if file == nil {
			return nil, ErrNoSource
		}

		resolved, err := p.Token(file)
		if err != nil {
			return nil, fmt.Errorf("path token: %w", err)
		}

		return resolved, nil
	}

	return nil, ErrNoPathOrToken
}
