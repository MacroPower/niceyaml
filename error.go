package niceyaml

import (
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"github.com/macropower/niceyaml/line"
	"github.com/macropower/niceyaml/paths"
	"github.com/macropower/niceyaml/position"
	"github.com/macropower/niceyaml/style"
)

// ErrNoSource indicates no source was provided to resolve an error path.
var ErrNoSource = errors.New("no source provided")

// FileGetter gets an [*ast.File].
// See [*Source] for an implementation.
type FileGetter interface {
	File() (*ast.File, error)
}

// PathPartGetter gets a yaml path and its part.
// See [*paths.Path] for an implementation.
type PathPartGetter interface {
	Path() *paths.YAMLPath
	Part() paths.Part
	Token(file *ast.File) (*token.Token, error)
}

// Error represents a YAML error with optional source annotation.
// To enable annotated error output that shows the relevant YAML location, provide:
//   - [WithErrorToken] directly specifies the error location, OR
//   - [WithPath] combined with [WithSource] to resolve the path
//
// Create instances with [NewError].
//
//nolint:recvcheck // Must satisfy error interface.
type Error struct {
	err         error
	printer     StyledSlicePrinter
	source      FileGetter
	pathGetter  PathPartGetter
	token       *token.Token
	widthFunc   func() int
	errors      []*Error
	sourceLines int
	width       int
}

// NewError creates a new [Error] with the given message.
// If wrapping an existing error, use [NewErrorFrom] instead.
// The [Error] can optionally be further configured with [ErrorOption]s.
func NewError(msg string, opts ...ErrorOption) *Error {
	return NewErrorFrom(errors.New(msg), opts...)
}

// NewErrorFrom creates a new [Error] from an existing error.
// If creating a new error with no wrapping, use [NewError] instead.
// The [Error] can optionally be further configured with [ErrorOption]s.
func NewErrorFrom(err error, opts ...ErrorOption) *Error {
	e := &Error{
		err:         err,
		sourceLines: 2,
	}
	e.SetOption(opts...)

	return e
}

// ErrorOption configures an [Error].
type ErrorOption func(e *Error)

// WithSourceLines sets the number of context lines to show around the error.
func WithSourceLines(lines int) ErrorOption {
	return func(e *Error) {
		e.sourceLines = lines
	}
}

// WithPath sets the YAML path where the error occurred.
// The [PathPartGetter] provides both the path and whether to highlight the key or value.
func WithPath(ptg PathPartGetter) ErrorOption {
	return func(e *Error) {
		e.pathGetter = ptg
	}
}

// WithErrorToken sets the token where the error occurred.
func WithErrorToken(tk *token.Token) ErrorOption {
	return func(e *Error) {
		e.token = tk
	}
}

// WithPrinter sets the printer used for formatting the error source.
func WithPrinter(p StyledSlicePrinter) ErrorOption {
	return func(e *Error) {
		e.printer = p
	}
}

// WithSource sets the [FileGetter] for resolving the error path.
// See [*Source] for an implementation.
func WithSource(src FileGetter) ErrorOption {
	return func(e *Error) {
		e.source = src
	}
}

// WithWidthFunc sets a function to determine the width for word wrapping.
// This takes precedence over [SetWidth] when both are configured.
func WithWidthFunc(fn func() int) ErrorOption {
	return func(e *Error) {
		e.widthFunc = fn
	}
}

// WithErrors adds nested errors to the [Error]. Each nested error has its own
// YAML path or token and will be rendered as an annotation below its resolved line.
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

	// If no main path/token but nested errors have paths, use nested-only rendering.
	if e.pathGetter == nil && e.token == nil {
		if e.hasNestedPaths() {
			errMsg, srcErr := e.annotateSourceFromNested()
			if srcErr != nil {
				slog.Debug("annotate yaml from nested",
					slog.Any("error", srcErr),
				)

				return e.formatPlainError()
			}

			return errMsg
		}

		return e.formatPlainError()
	}

	errMsg, srcErr := e.annotateSource()
	if srcErr != nil {
		pathStr := ""
		if e.pathGetter != nil {
			pathStr = e.pathGetter.Path().String()
		}

		slog.Debug("annotate yaml",
			slog.String("path", pathStr),
			slog.Any("error", srcErr),
		)
		// If we can't annotate the source, just return the error without it.
		if pathStr != "" {
			return fmt.Sprintf("at %s: %v", pathStr, e.err)
		}

		return e.err.Error()
	}

	return errMsg
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

// hasNestedPaths returns true if any nested errors have paths or tokens.
func (e *Error) hasNestedPaths() bool {
	for _, nested := range e.errors {
		if nested != nil && (nested.pathGetter != nil || nested.token != nil) {
			return true
		}
	}

	return false
}

// SetOption applies the provided [ErrorOption]s to the [Error].
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

// getPrinter returns the configured printer, or a default if none was set.
// If a width is configured, it applies the width to the printer.
func (e *Error) getPrinter() StyledSlicePrinter {
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

	return p
}

// Unwrap returns the underlying errors, enabling [errors.Is] and [errors.As].
// This implements the Go 1.20+ multi-error unwrap interface.
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

// Path returns the [*PathTarget] where the error occurred as a string.
// If the main error has no path but nested errors do, returns the most specific
// nested error's path, prioritizing key-targeting errors (like additionalProperties).
func (e *Error) Path() string {
	if e.pathGetter != nil {
		return e.pathGetter.Path().String()
	}

	// Find the most specific nested error's path, prioritizing key-targeting errors.
	var (
		bestPath  string
		bestIsKey bool
	)

	for _, nested := range e.errors {
		if nested == nil || nested.pathGetter == nil {
			continue
		}

		pathStr := nested.pathGetter.Path().String()
		isKey := nested.pathGetter.Part() == paths.PartKey

		// Prefer key-targeting errors, then longer (more specific) paths.
		if bestPath == "" ||
			(isKey && !bestIsKey) ||
			(isKey == bestIsKey && len(pathStr) > len(bestPath)) {
			bestPath = pathStr
			bestIsKey = isKey
		}
	}

	return bestPath
}

func (e *Error) annotateSource() (string, error) {
	tk, err := e.resolveToken()
	if err != nil {
		return "", fmt.Errorf("resolve token: %w", err)
	}

	lineNum, col := getTokenPosition(tk)
	errMsg := fmt.Sprintf("[%d:%d] %v:\n", lineNum, col, e.err)
	errSource := e.printErrorToken(tk)

	return errMsg + "\n" + errSource, nil
}

// annotateSourceFromNested creates an annotated source display using only nested errors.
// This is used when the main error has no path but nested errors do.
func (e *Error) annotateSourceFromNested() (string, error) {
	if e.source == nil {
		return "", ErrNoSource
	}

	file, err := e.source.File()
	if err != nil {
		return "", fmt.Errorf("parse source: %w", err)
	}

	// Find a reference token from nested errors to create the Source.
	var refToken *token.Token

	for _, nested := range e.errors {
		if nested == nil {
			continue
		}

		// First try to use the token directly if available.
		if nested.token != nil {
			refToken = nested.token

			break
		}

		// Otherwise try to resolve from path.
		if nested.pathGetter != nil {
			tk, tkErr := nested.pathGetter.Token(file)
			if tkErr == nil {
				refToken = tk

				break
			}
		}
	}

	if refToken == nil {
		return "", errors.New("no resolvable nested errors")
	}

	return e.printNestedErrors(refToken), nil
}

// printNestedErrors renders nested errors without a main token highlight.
// Used when the main error has no path but nested errors do.
func (e *Error) printNestedErrors(refToken *token.Token) string {
	p := e.getPrinter()
	t := NewSourceFromToken(refToken)

	// Resolve and process all nested errors.
	resolved := e.resolveNestedErrors(t)

	// Collect error line indices and ranges from resolved errors.
	errorLines := make([]int, 0, len(resolved))
	nestedRanges := make([]position.Range, 0, len(resolved))

	for _, r := range resolved {
		errorLines = append(errorLines, r.lineIdx)
		nestedRanges = append(nestedRanges, t.ContentPositionRanges(r.pos)...)
	}

	errMsg := fmt.Sprintf("%v:\n", e.err)

	// Build hunks from error line indices.
	hunks := e.buildErrorHunks(errorLines)

	// If single hunk, use simple path (no separator needed).
	if len(hunks) <= 1 {
		// Apply error styles with original positions.
		for _, rng := range nestedRanges {
			p.AddStyleToRange(p.Style(style.GenericError), rng)
		}

		e.addErrorAnnotations(t, resolved)

		minLine, maxLine := e.calculateHunkRange(hunks)
		minLine = max(0, minLine-e.sourceLines)
		maxLine = min(t.Len()-1, maxLine+e.sourceLines)

		return errMsg + "\n" + p.PrintSlice(t, minLine, maxLine)
	}

	// Multiple hunks: build filtered lines with separators.
	hunkLines := e.buildHunkLines(t, hunks)
	indexMap := e.buildLineIndexMap(hunks, t.Len())
	e.addErrorAnnotationsToHunkLines(hunkLines, hunks, resolved, t.Len())

	// Apply error styles with corrected positions for hunk lines.
	for _, rng := range nestedRanges {
		if hunkIdx, exists := indexMap[rng.Start.Line]; exists {
			correctedRng := position.NewRange(
				position.New(hunkIdx, rng.Start.Col),
				position.New(hunkIdx, rng.End.Col),
			)
			p.AddStyleToRange(p.Style(style.GenericError), correctedRng)
		}
	}

	// Create a new Source from hunk lines and print all of it.
	filteredSource := &Source{lines: hunkLines}

	return errMsg + "\n" + p.PrintSlice(filteredSource, 0, len(hunkLines)-1)
}

// resolveToken returns the error token, resolving it from the pathGetter if needed.
func (e *Error) resolveToken() (*token.Token, error) {
	if e.token != nil {
		return e.token, nil
	}

	if e.pathGetter == nil {
		return nil, errors.New("no path or token")
	}

	if e.source == nil {
		return nil, ErrNoSource
	}

	file, err := e.source.File()
	if err != nil {
		return nil, fmt.Errorf("parse source: %w", err)
	}

	tk, err := e.pathGetter.Token(file)
	if err != nil {
		return nil, fmt.Errorf("resolve token: %w", err)
	}

	return tk, nil
}

// resolvedError holds information about a resolved nested error.
type resolvedError struct {
	message string            // Error message for annotation.
	pos     position.Position // Position for highlighting.
	lineIdx int               // 0-indexed line in Source.
	col     int               // 0-indexed column position for annotation.
}

// errorHunk represents a contiguous group of error lines to display.
type errorHunk struct {
	startLine int // 0-indexed start line (before context).
	endLine   int // 0-indexed end line (before context).
}

// buildErrorHunks groups error line indices into hunks based on proximity.
// Errors are merged when their context windows would be adjacent or overlapping.
// This ensures there's always at least one line gap between hunks (for the "..." separator).
func (e *Error) buildErrorHunks(errorLines []int) []errorHunk {
	if len(errorLines) == 0 {
		return nil
	}

	// Sort error lines.
	sorted := slices.Clone(errorLines)
	slices.Sort(sorted)

	// Merge errors when their context windows would be adjacent or overlapping.
	// Error at E1 has context [E1-S, E1+S], error at E2 has context [E2-S, E2+S].
	// Merge if E2-S <= E1+S+1, i.e., E2 <= E1 + 2S + 1.
	threshold := e.sourceLines*2 + 1
	hunks := []errorHunk{{startLine: sorted[0], endLine: sorted[0]}}

	for _, lineIdx := range sorted[1:] {
		lastHunk := &hunks[len(hunks)-1]
		if lineIdx <= lastHunk.endLine+threshold {
			// Merge into current hunk.
			lastHunk.endLine = lineIdx
		} else {
			// Start a new hunk.
			hunks = append(hunks, errorHunk{startLine: lineIdx, endLine: lineIdx})
		}
	}

	return hunks
}

// buildHunkLines creates a new line.Lines containing only the lines for the given hunks,
// with context added and "..." annotations between hunks.
func (e *Error) buildHunkLines(t *Source, hunks []errorHunk) line.Lines {
	if len(hunks) == 0 {
		return nil
	}

	totalLines := t.Len()

	var result line.Lines

	for i, hunk := range hunks {
		// Calculate line range with sourceLines context.
		startLine := max(0, hunk.startLine-e.sourceLines)
		endLine := min(totalLines-1, hunk.endLine+e.sourceLines)

		isFirstLineOfHunk := true

		for lineIdx := startLine; lineIdx <= endLine; lineIdx++ {
			ln := t.Line(lineIdx).Clone()

			// Add "..." annotation on first line of non-first hunks.
			if isFirstLineOfHunk && i > 0 {
				ln.Annotations.Add(line.Annotation{Content: "...", Position: line.Above})
			}

			isFirstLineOfHunk = false

			result = append(result, ln)
		}
	}

	return result
}

// buildLineIndexMap creates a mapping from original line indices to hunk line indices.
func (e *Error) buildLineIndexMap(hunks []errorHunk, totalLines int) map[int]int {
	indexMap := make(map[int]int)
	hunkLineIdx := 0

	for _, hunk := range hunks {
		startLine := max(0, hunk.startLine-e.sourceLines)
		endLine := min(totalLines-1, hunk.endLine+e.sourceLines)

		for lineIdx := startLine; lineIdx <= endLine; lineIdx++ {
			indexMap[lineIdx] = hunkLineIdx
			hunkLineIdx++
		}
	}

	return indexMap
}

// addErrorAnnotationsToHunkLines adds error annotations to the filtered hunk lines,
// mapping from original source line indices to the new indices in hunkLines.
func (e *Error) addErrorAnnotationsToHunkLines(
	hunkLines line.Lines,
	hunks []errorHunk,
	resolved []resolvedError,
	totalLines int,
) {
	if len(resolved) == 0 {
		return
	}

	// Build a mapping from original line index to hunkLines index.
	indexMap := e.buildLineIndexMap(hunks, totalLines)

	// Group errors by original line to combine messages.
	lineAnnotations := make(map[int][]resolvedError)
	for _, r := range resolved {
		lineAnnotations[r.lineIdx] = append(lineAnnotations[r.lineIdx], r)
	}

	// Set annotations for each line.
	// Note: Error annotations take precedence over "..." separators since
	// the Line struct only supports one annotation, and error information
	// is more valuable than visual separators. Line number gaps in the
	// gutter will still indicate missing lines.
	for lineIdx, lineErrs := range lineAnnotations {
		hunkIdx, exists := indexMap[lineIdx]
		if !exists || hunkIdx < 0 || hunkIdx >= len(hunkLines) {
			continue
		}

		// Combine messages and find the leftmost column.
		var messages []string

		minCol := lineErrs[0].col

		for _, r := range lineErrs {
			messages = append(messages, r.message)
			if r.col < minCol {
				minCol = r.col
			}
		}

		combined := strings.Join(messages, "; ")
		hunkLines[hunkIdx].Annotations.Add(line.Annotation{
			Content:  combined,
			Position: line.Below,
			Col:      minCol,
		})
	}
}

func (e *Error) printErrorToken(tk *token.Token) string {
	p := e.getPrinter()
	t := NewSourceFromToken(tk)

	// Collect main error token ranges.
	mainRanges := t.ContentPositionRangesFromToken(tk)

	// Collect all error line indices.
	errorLineSet := make(map[int]struct{})
	curLine := max(0, tk.Position.Line-1)
	errorLineSet[curLine] = struct{}{}

	for _, rng := range mainRanges {
		errorLineSet[rng.Start.Line] = struct{}{}
	}

	// Resolve nested errors and collect their line indices and ranges.
	resolved := e.resolveNestedErrors(t)
	nestedRanges := make([]position.Range, 0, len(resolved))

	for _, r := range resolved {
		errorLineSet[r.lineIdx] = struct{}{}
		nestedRanges = append(nestedRanges, t.ContentPositionRanges(r.pos)...)
	}

	// Convert set to slice.
	errorLines := make([]int, 0, len(errorLineSet))
	for lineIdx := range errorLineSet {
		errorLines = append(errorLines, lineIdx)
	}

	// Build hunks from error line indices.
	hunks := e.buildErrorHunks(errorLines)

	// If single hunk, use simple path (no separator needed).
	if len(hunks) <= 1 {
		// Apply error styles with original positions.
		for _, rng := range mainRanges {
			p.AddStyleToRange(p.Style(style.GenericError), rng)
		}

		for _, rng := range nestedRanges {
			p.AddStyleToRange(p.Style(style.GenericError), rng)
		}

		e.addErrorAnnotations(t, resolved)

		minLine, maxLine := e.calculateHunkRange(hunks)
		minLine = max(0, minLine-e.sourceLines)
		maxLine = min(t.Len()-1, maxLine+e.sourceLines)

		return p.PrintSlice(t, minLine, maxLine)
	}

	// Multiple hunks: build filtered lines with separators.
	hunkLines := e.buildHunkLines(t, hunks)
	indexMap := e.buildLineIndexMap(hunks, t.Len())
	e.addErrorAnnotationsToHunkLines(hunkLines, hunks, resolved, t.Len())

	// Apply error styles with corrected positions for hunk lines.
	for _, rng := range mainRanges {
		if hunkIdx, exists := indexMap[rng.Start.Line]; exists {
			correctedRng := position.NewRange(
				position.New(hunkIdx, rng.Start.Col),
				position.New(hunkIdx, rng.End.Col),
			)
			p.AddStyleToRange(p.Style(style.GenericError), correctedRng)
		}
	}

	for _, rng := range nestedRanges {
		if hunkIdx, exists := indexMap[rng.Start.Line]; exists {
			correctedRng := position.NewRange(
				position.New(hunkIdx, rng.Start.Col),
				position.New(hunkIdx, rng.End.Col),
			)
			p.AddStyleToRange(p.Style(style.GenericError), correctedRng)
		}
	}

	// Create a new Source from hunk lines and print all of it.
	filteredSource := &Source{lines: hunkLines}

	return p.PrintSlice(filteredSource, 0, len(hunkLines)-1)
}

// calculateHunkRange returns the min and max line indices from hunks.
func (e *Error) calculateHunkRange(hunks []errorHunk) (int, int) {
	if len(hunks) == 0 {
		return 0, 0
	}

	minLine := hunks[0].startLine
	maxLine := hunks[0].endLine

	for _, hunk := range hunks[1:] {
		if hunk.startLine < minLine {
			minLine = hunk.startLine
		}
		if hunk.endLine > maxLine {
			maxLine = hunk.endLine
		}
	}

	return minLine, maxLine
}

// resolveNestedErrors resolves all nested error paths/tokens and returns
// information needed to annotate them.
func (e *Error) resolveNestedErrors(t *Source) []resolvedError {
	if len(e.errors) == 0 {
		return nil
	}

	resolved := make([]resolvedError, 0, len(e.errors))

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

		resolved = append(resolved, r)
	}

	return resolved
}

// resolveNestedError resolves a single nested error's path or token.
func (e *Error) resolveNestedError(t *Source, nested *Error) (resolvedError, error) {
	var resolvedTk *token.Token

	// Try to resolve the nested error's token.
	switch {
	case nested.token != nil:
		resolvedTk = nested.token
	case nested.pathGetter != nil:
		// Use the parent's source to resolve the nested error's path.
		file, fileErr := t.File()
		if fileErr != nil {
			return resolvedError{}, fmt.Errorf("parse source: %w", fileErr)
		}

		tk, err := nested.pathGetter.Token(file)
		if err != nil {
			return resolvedError{}, fmt.Errorf("resolve path: %w", err)
		}

		resolvedTk = tk

	default:
		return resolvedError{}, errors.New("nested error has no path or token")
	}

	lineIdx := e.findLineIndex(t, resolvedTk)
	if lineIdx < 0 {
		return resolvedError{}, errors.New("token not found in source")
	}

	// Calculate column position for annotation (0-indexed).
	col := 0
	if resolvedTk.Position != nil {
		col = resolvedTk.Position.Column - 1 // Convert 1-indexed to 0-indexed.
	}

	pos := position.New(lineIdx, max(0, col))

	return resolvedError{
		lineIdx: lineIdx,
		col:     max(0, col),
		message: nested.err.Error(),
		pos:     pos,
	}, nil
}

// findLineIndex returns the 0-indexed line index for a token in the Source.
// Returns -1 if the token is not found.
func (e *Error) findLineIndex(t *Source, tk *token.Token) int {
	if tk == nil || tk.Position == nil {
		return -1
	}

	// Position.Line is 1-indexed, Source uses 0-indexed.
	lineIdx := tk.Position.Line - 1
	if lineIdx < 0 || lineIdx >= t.Len() {
		return -1
	}

	return lineIdx
}

// addErrorAnnotations sets annotations on the Source for resolved nested errors.
// Multiple errors on the same line are combined with "; " separator.
func (e *Error) addErrorAnnotations(t *Source, resolved []resolvedError) {
	if len(resolved) == 0 {
		return
	}

	// Group errors by line to combine messages.
	lineAnnotations := make(map[int][]resolvedError)

	for _, r := range resolved {
		lineAnnotations[r.lineIdx] = append(lineAnnotations[r.lineIdx], r)
	}

	// Set annotations for each line.
	for lineIdx, lineErrs := range lineAnnotations {
		if lineIdx < 0 || lineIdx >= t.Len() {
			continue
		}

		// Combine messages and find the leftmost column.
		var messages []string

		minCol := lineErrs[0].col

		for _, r := range lineErrs {
			messages = append(messages, r.message)
			if r.col < minCol {
				minCol = r.col
			}
		}

		combined := strings.Join(messages, "; ")
		t.Annotate(lineIdx, line.Annotation{
			Content:  combined,
			Position: line.Below,
			Col:      minCol,
		})
	}
}

// getTokenPosition returns the 1-indexed line and column position of the token.
// Note: go-yaml uses 1-indexed positions, unlike [position.Position] which is 0-indexed.
func getTokenPosition(tk *token.Token) (int, int) {
	if tk == nil {
		return 0, 0
	}

	return tk.Position.Line, tk.Position.Column
}
