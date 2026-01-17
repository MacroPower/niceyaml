package niceyaml

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"github.com/macropower/niceyaml/style"
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
	path        *yaml.Path
	token       *token.Token
	printer     StyledSlicePrinter
	source      FileGetter
	sourceLines int
}

// NewError creates a new [Error] with the given underlying error and options.
// Default SourceLines is 4.
func NewError(err error, opts ...ErrorOption) *Error {
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

// WithPath sets the YAML path where the error occurred.
func WithPath(path *yaml.Path) ErrorOption {
	return func(e *Error) {
		e.path = path
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

// Error returns the error message with source annotation if available.
func (e Error) Error() string {
	if e.err == nil {
		return ""
	}
	if e.path == nil && e.token == nil {
		return e.err.Error()
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

// SetOption applies the provided [ErrorOption]s to the [Error].
func (e *Error) SetOption(opts ...ErrorOption) {
	for _, opt := range opts {
		opt(e)
	}
}

// Unwrap returns the underlying error, enabling [errors.Is] and [errors.As].
func (e *Error) Unwrap() error {
	return e.err
}

// Path returns the [*yaml.Path] where the error occurred as a string.
func (e *Error) Path() string {
	if e.path == nil {
		return ""
	}

	return e.path.String()
}

func (e *Error) annotateSource(path *yaml.Path) (string, error) {
	tk, err := e.resolveToken(path)
	if err != nil {
		return "", fmt.Errorf("resolve token: %w", err)
	}

	line, col := getTokenPosition(tk)
	errMsg := fmt.Sprintf("[%d:%d] %v:\n", line, col, e.err)
	errSource := e.printErrorToken(tk)

	return errMsg + "\n" + errSource, nil
}

// resolveToken returns the error token, resolving it from the path if needed.
func (e *Error) resolveToken(path *yaml.Path) (*token.Token, error) {
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

	return getTokenFromPath(file, path)
}

func (e *Error) printErrorToken(tk *token.Token) string {
	p := e.printer
	if p == nil {
		p = NewPrinter()
	}

	t := NewSourceFromToken(tk)

	ranges := t.ContentPositionRangesFromToken(tk)
	for _, rng := range ranges {
		p.AddStyleToRange(p.Style(style.GenericError), rng)
	}

	curLine := max(0, tk.Position.Line-1)
	minLine, maxLine := curLine, curLine

	for _, rng := range ranges {
		lineNum := t.Line(rng.Start.Line).Number()
		if lineNum < minLine {
			minLine = lineNum
		}
		if lineNum > maxLine {
			maxLine = lineNum
		}
	}

	minLine = max(0, minLine-e.sourceLines)
	maxLine = max(minLine, maxLine+e.sourceLines)

	return p.PrintSlice(t, minLine, maxLine)
}

func getTokenFromPath(file *ast.File, path *yaml.Path) (*token.Token, error) {
	node, err := path.FilterFile(file)
	if err != nil {
		return nil, fmt.Errorf("filter from ast.File by YAMLPath: %w", err)
	}

	// Try to find the key token by looking up parent.
	// This is useful because path.FilterFile returns the VALUE node,
	// but for error reporting we want to point to the KEY.
	if keyToken := findKeyToken(file, node); keyToken != nil {
		return keyToken, nil
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
