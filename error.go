package niceyaml

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"github.com/macropower/niceyaml/line"
	"github.com/macropower/niceyaml/position"
	"github.com/macropower/niceyaml/style"
)

// PathTarget specifies which part of a mapping entry to highlight when resolving a path.
type PathTarget int

const (
	// PathKey highlights the key of a mapping entry.
	PathKey PathTarget = iota
	// PathValue highlights the value of a mapping entry.
	PathValue
)

// ErrNoSource indicates no source was provided to resolve an error path.
var ErrNoSource = errors.New("no source provided")

// FileGetter gets an [*ast.File].
// See [*Source] for an implementation.
type FileGetter interface {
	File() (*ast.File, error)
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
	path        *Path
	token       *token.Token
	errors      []*Error // Nested errors with their own paths/tokens.
	pathTarget  PathTarget
	sourceLines int
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
		sourceLines: 4,
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

// WithPath sets the YAML path where the error occurred and which part to highlight.
func WithPath(path *Path, target PathTarget) ErrorOption {
	return func(e *Error) {
		e.path = path
		e.pathTarget = target
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
	if e.path == nil && e.token == nil {
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

	errMsg, srcErr := e.annotateSource(e.path)
	if srcErr != nil {
		slog.Debug("annotate yaml",
			slog.String("path", e.path.String()),
			slog.Any("error", srcErr),
		)
		// If we can't annotate the source, just return the error without it.
		return fmt.Sprintf("at %s: %v", e.path.String(), e.err)
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
		if nested != nil && (nested.path != nil || nested.token != nil) {
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

// Path returns the [*Path] where the error occurred as a string.
// If the main error has no path but nested errors do, returns the most specific
// nested error's path, prioritizing key-targeting errors (like additionalProperties).
func (e *Error) Path() string {
	if e.path != nil {
		return e.path.String()
	}

	// Find the most specific nested error's path, prioritizing key-targeting errors.
	var bestPath string

	var bestIsKey bool

	for _, nested := range e.errors {
		if nested == nil || nested.path == nil {
			continue
		}

		pathStr := nested.path.String()
		isKey := nested.pathTarget == PathKey

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

func (e *Error) annotateSource(path *Path) (string, error) {
	tk, err := e.resolveToken(path)
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
		if nested.path != nil {
			tk, tkErr := getTokenFromPath(file, nested.path, nested.pathTarget)
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
	p := e.printer
	if p == nil {
		p = NewPrinter()
	}

	t := NewSourceFromToken(refToken)

	// Resolve and process all nested errors.
	resolved := e.resolveNestedErrors(t)

	// Calculate line range from resolved errors.
	minLine, maxLine := e.calculateNestedLineRange(resolved)

	// Highlight each nested error.
	for _, r := range resolved {
		nestedRanges := t.ContentPositionRanges(r.pos)
		for _, rng := range nestedRanges {
			p.AddStyleToRange(p.Style(style.GenericError), rng)
		}
	}

	// Add annotations for nested errors.
	e.addErrorAnnotations(t, resolved)

	minLine = max(0, minLine-e.sourceLines)
	maxLine = max(minLine, maxLine+e.sourceLines)

	errMsg := fmt.Sprintf("%v:\n", e.err)

	return errMsg + "\n" + p.PrintSlice(t, minLine, maxLine)
}

// calculateNestedLineRange returns the min and max line indices from resolved errors.
func (e *Error) calculateNestedLineRange(resolved []resolvedError) (int, int) {
	if len(resolved) == 0 {
		return 0, 0
	}

	minLine := resolved[0].lineIdx
	maxLine := resolved[0].lineIdx

	for _, r := range resolved[1:] {
		if r.lineIdx < minLine {
			minLine = r.lineIdx
		}

		if r.lineIdx > maxLine {
			maxLine = r.lineIdx
		}
	}

	return minLine, maxLine
}

// resolveToken returns the error token, resolving it from the path if needed.
func (e *Error) resolveToken(path *Path) (*token.Token, error) {
	if e.token != nil {
		return e.token, nil
	}

	if e.source == nil {
		return nil, ErrNoSource
	}

	file, err := e.source.File()
	if err != nil {
		return nil, fmt.Errorf("parse source: %w", err)
	}

	return getTokenFromPath(file, path, e.pathTarget)
}

// resolvedError holds information about a resolved nested error.
type resolvedError struct {
	message string            // Error message for annotation.
	pos     position.Position // Position for highlighting.
	lineIdx int               // 0-indexed line in Source.
	col     int               // 0-indexed column position for annotation.
}

func (e *Error) printErrorToken(tk *token.Token) string {
	p := e.printer
	if p == nil {
		p = NewPrinter()
	}

	t := NewSourceFromToken(tk)

	// Highlight the main error token.
	ranges := t.ContentPositionRangesFromToken(tk)
	for _, rng := range ranges {
		p.AddStyleToRange(p.Style(style.GenericError), rng)
	}

	curLine := max(0, tk.Position.Line-1)
	minLine, maxLine := curLine, curLine

	for _, rng := range ranges {
		lineIdx := rng.Start.Line
		if lineIdx < minLine {
			minLine = lineIdx
		}
		if lineIdx > maxLine {
			maxLine = lineIdx
		}
	}

	// Resolve nested errors and expand line range.
	resolved := e.resolveNestedErrors(t)
	for _, r := range resolved {
		// Highlight nested error content at the resolved position.
		nestedRanges := t.ContentPositionRanges(r.pos)
		for _, rng := range nestedRanges {
			p.AddStyleToRange(p.Style(style.GenericError), rng)
		}
		// Expand line range to include nested errors.
		if r.lineIdx < minLine {
			minLine = r.lineIdx
		}
		if r.lineIdx > maxLine {
			maxLine = r.lineIdx
		}
	}

	// Add annotations for nested errors.
	e.addErrorAnnotations(t, resolved)

	minLine = max(0, minLine-e.sourceLines)
	maxLine = max(minLine, maxLine+e.sourceLines)

	return p.PrintSlice(t, minLine, maxLine)
}

// resolveNestedErrors resolves all nested error paths/tokens and returns
// information needed to annotate them.
func (e *Error) resolveNestedErrors(t *Source) []resolvedError {
	if len(e.errors) == 0 {
		return nil
	}

	var resolved []resolvedError

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
	case nested.path != nil:
		// Use the parent's source to resolve the nested error's path.
		file, fileErr := t.File()
		if fileErr != nil {
			return resolvedError{}, fmt.Errorf("parse source: %w", fileErr)
		}

		tk, err := getTokenFromPath(file, nested.path, nested.pathTarget)
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
			Content:  "^ " + combined,
			Position: line.Below,
			Col:      minCol,
		})
	}
}

func getTokenFromPath(file *ast.File, path *Path, target PathTarget) (*token.Token, error) {
	node, err := path.FilterFile(file)
	if err != nil {
		return nil, fmt.Errorf("filter from ast.File by YAMLPath: %w", err)
	}

	if target == PathKey {
		if keyToken := findKeyToken(file, node); keyToken != nil {
			return keyToken, nil
		}
	}

	return node.GetToken(), nil
}

// findKeyToken finds the KEY token for the given node by looking at its parent.
// Returns nil if the node is not a value in a mapping (e.g., array element or root).
func findKeyToken(file *ast.File, node ast.Node) *token.Token {
	if file == nil || node == nil || len(file.Docs) == 0 {
		return nil
	}

	parent := ast.Parent(file.Docs[0].Body, node)
	if parent == nil {
		return nil
	}

	if mv, ok := parent.(*ast.MappingValueNode); ok {
		return mv.Key.GetToken()
	}

	return nil
}

// getTokenPosition returns the 1-indexed line and column position of the token.
// Note: go-yaml uses 1-indexed positions, unlike [position.Position] which is 0-indexed.
func getTokenPosition(tk *token.Token) (int, int) {
	if tk == nil {
		return 0, 0
	}

	return tk.Position.Line, tk.Position.Column
}
