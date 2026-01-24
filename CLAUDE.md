# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Test Commands

```bash
task format # Format and lint
task lint   # Lint only
task test   # Run all tests
```

## Architecture

```bash
task docs # Print all package docs
```

## Code Style

### Go Conventions

- Document all exported items with doc comments.
- Package documentation in `doc.go` files, if multiple files exist.
- Wrap errors with `fmt.Errorf("context: %w", err)`, or `fmt.Errorf("%w: %w", ErrSentinel, err)`.
- Avoid using "failed" or "error" in library error messages.
- Use global error variables for common errors.
- Use constructors with functional options.
- Accept interfaces, return concrete types.

### Code Patterns

- Functional options pattern used throughout (e.g., `PrinterOption`, `SourceOption`, `ErrorOption`).
- 0-indexed positioning convention for `position.Position` (line and column start at 0).
- Half-open ranges `[Start, End)` for `position.Range`.
- Prefer consistency over performance, avoid "fast paths" that could lead to unpredictable behavior.

### Documentation

- Use `[Name]` syntax for Go doc links.
- Constructors should always begin: `// NewThing creates a new [Thing].`
- Types with constructors should always note: `// Create instances with [NewThing].`
- Interfaces should note: `// See [Thing] for an implementation.`
- Interfaces should have sensible names: `type Builder interface { Build() Thing } // Builder builds [Thing]s.`
- Functional option types should have a list linking to all functions of that type.
- Functional options should always have a link to their type.
- Package docs should explain concepts and usage patterns; do not just enumerate exports.

### Testing

- Use `github.com/stretchr/testify/assert` and `require`.
- Table-driven tests with `map[string]struct{}` format.
- Field names: prefer `want` for expected output, `err` for expected errors.
- For inputs, use clear contextual names (e.g., `before`/`after` for diffs, `line`/`col` for positions).
- Always use `t.Parallel()` in all tests.
- Create test packages (`package foo_test`) testing public API.
- Use `require.ErrorIs` for sentinel error checking.
- Use `require.ErrorAs` for error type extraction.
- Use the `yamltest` helpers whenever possible.

### Key Dependencies

- `github.com/goccy/go-yaml`: YAML parsing, AST, tokens, and paths.
- `charm.land/lipgloss/v2`: Terminal styling.
- `charm.land/bubbletea/v2`: Terminal UI framework.
- `charm.land/bubbles/v2`: UI components for Bubble Tea.
- `github.com/invopop/jsonschema`: JSON schema generation from Go types.
- `github.com/santhosh-tekuri/jsonschema/v6`: JSON schema validation.
