package niceyaml

import (
	"errors"
	"fmt"
	"log/slog"

	"charm.land/lipgloss/v2"
	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
	"github.com/goccy/go-yaml/token"
)

// ErrorWrapper wraps errors with additional context for [Error] types.
// It holds default options that are applied to all wrapped errors.
type ErrorWrapper struct {
	opts []ErrorOption
}

// NewErrorWrapper creates a new [ErrorWrapper] with the given default options.
func NewErrorWrapper(opts ...ErrorOption) *ErrorWrapper {
	return &ErrorWrapper{
		opts: opts,
	}
}

// Wrap wraps an error with additional context for [Error]s.
// If the error isn't an [Error], it returns the original error unmodified.
func (ew *ErrorWrapper) Wrap(err error, opts ...ErrorOption) error {
	if err == nil {
		return nil
	}

	var yamlErr *Error
	if errors.As(err, &yamlErr) {
		for _, opt := range ew.opts {
			opt(yamlErr)
		}

		for _, opt := range opts {
			opt(yamlErr)
		}

		return yamlErr
	}

	return err
}

// Error represents a YAML error with optional source annotation.
// Use [WithErrorToken], [WithPath] and [WithTokens], or [WithPath] and [WithFile],
// to enable annotated error output that shows the relevant YAML location.
//
//nolint:recvcheck // Must satisfy error interface.
type Error struct {
	err         error
	path        *yaml.Path
	token       *token.Token
	printer     *Printer
	file        *ast.File
	tokens      token.Tokens
	sourceLines int
}

// NewError creates a new [Error] with the given underlying error and options.
// Default SourceLines is 4.
func NewError(err error, opts ...ErrorOption) *Error {
	e := &Error{
		err:         err,
		sourceLines: 4,
	}
	for _, opt := range opts {
		opt(e)
	}

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
func WithPrinter(p *Printer) ErrorOption {
	return func(e *Error) {
		e.printer = p
	}
}

// WithTokens sets the YAML tokens for annotating the error.
// The tokens are used to resolve the error path to a specific token location.
func WithTokens(tokens token.Tokens) ErrorOption {
	return func(e *Error) {
		e.tokens = tokens
	}
}

// WithFile sets the parsed AST file for resolving the error path.
// Use this instead of [WithTokens] when you already have a parsed file.
func WithFile(file *ast.File) ErrorOption {
	return func(e *Error) {
		e.file = file
	}
}

// Error returns the error message with source annotation if available.
//
//nolint:gocritic // hugeParam: Must satisfy error interface.
func (e Error) Error() string {
	if e.err == nil {
		return ""
	}
	if e.path == nil && e.token == nil {
		return e.err.Error()
	}

	errMsg, srcErr := e.annotateSource(e.path)
	if srcErr != nil {
		slog.Debug("failed to annotate yaml",
			slog.String("path", e.path.String()),
			slog.Any("error", srcErr),
		)
		// If we can't annotate the source, just return the error without it.
		return fmt.Sprintf("error at %s: %v", e.path.String(), e.err)
	}

	return errMsg
}

// GetPath returns the YAML path where the error occurred as a string.
func (e *Error) GetPath() string {
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
	errMsg := fmt.Sprintf("[%d:%d] %v:", line, col, e.err)
	errSource := e.printErrorToken(tk)

	return lipgloss.JoinVertical(lipgloss.Top, errMsg, "", errSource), nil
}

// resolveToken returns the error token, resolving it from the path if needed.
func (e *Error) resolveToken(path *yaml.Path) (*token.Token, error) {
	if e.token != nil {
		return e.token, nil
	}

	file := e.file
	if file == nil {
		var err error

		file, err = parser.Parse(e.tokens, 0)
		if err != nil {
			return nil, fmt.Errorf("parse tokens into ast.File: %w", err)
		}
	}

	return getTokenFromPath(file, path)
}

func (e *Error) printErrorToken(tk *token.Token) string {
	p := e.printer
	if p == nil {
		p = NewPrinter(WithLineNumbers())
	}

	p.AddStyleToToken(p.colorScheme.Error, Position{Line: tk.Position.Line, Col: tk.Position.Column})

	content, _ := p.PrintErrorToken(tk.Clone(), e.sourceLines)

	return content
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

// getTokenPosition returns the line and column position of the token.
func getTokenPosition(tk *token.Token) (int, int) {
	if tk == nil {
		return 0, 0
	}

	return tk.Position.Line, tk.Position.Column
}
