# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Test Commands

```bash
task format
task lint
task test
```

## Architecture

### Core Package (niceyaml)

Go library (`github.com/macropower/niceyaml`) provides utilities for working with YAML, built on top of `github.com/goccy/go-yaml` and `charm.land/lipgloss/v2`.

### Schema Subpackage (niceyaml/schema)

- **Generator** (`schema/generate.go`): Creates JSON schemas from Go types using `github.com/invopop/jsonschema`.
- **Validator** (`schema/validate.go`): Validates data against JSON schemas using `github.com/santhosh-tekuri/jsonschema/v6`.

Validator integrates with our core package for improved YAML handling (e.g. supporting error annotations).

## Code Style

### Go Conventions

- Document all exported items with doc comments.
- Use `[Name]` syntax for Go doc links.
- Package documentation in `doc.go` files.
- Wrap errors with `fmt.Errorf("context: %w", err)`; no "failed" or "error" in messages.
- Use global error variables for common errors.

### Testing

- Use `github.com/stretchr/testify/assert` and `require`.
- Table-driven tests with `map[string]struct{}` format.
- Field names: `input`, `want`, `got`, `err`.
- Always use `t.Parallel()` in all tests.
- Create test packages (`package foo_test`) testing public API.
- Use `require.ErrorIs` for error type checking.
- Use multi-line strings or testdata files, avoid using `\n` in strings when testing >2 lines.

### Key Dependencies

- `github.com/goccy/go-yaml`: YAML parsing, AST, tokens, and paths.
- `charm.land/lipgloss/v2`: Terminal styling.
- `github.com/invopop/jsonschema`: JSON schema generation from Go types.
- `github.com/santhosh-tekuri/jsonschema/v6`: JSON schema validation.
