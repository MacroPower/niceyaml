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

Key types by feature area:

- **Source:** `Source`, `LineIterator` - YAML token organization by line.
- **Diff System:** `FullDiff`, `SummaryDiff`, `Revision` - LCS-based diffing and version tracking.
- **Search:** `Finder`, `Normalizer`, `StandardNormalizer` - text search with normalization support.
- **Error Handling:** `Error`, `ErrorWrapper` - errors with source context and annotations.
- **YAML Utilities:** `Decoder`, `DocumentDecoder`, `Validator`, `Encoder`, `NewPath`, `NewPathBuilder` - parsing, decoding, and validation.
- **Printing:** `Printer`, `Style`, `Styles`, `StyleGetter`, `TokenStyler`, `GutterFunc`, `GutterContext` - syntax highlighting and styled rendering.

### Bubbles Subpackage (niceyaml/bubbles)

- **YAMLViewport** (`bubbles/yamlviewport/`): Bubble Tea component for interactive YAML viewing.
- Key types: `Model` (implements `tea.Model`), `DiffMode`.

### Fangs Subpackage (niceyaml/fangs)

- **ErrorHandler** (`fangs/`): Custom error handler for Charmbracelet Fang/Cobra integration.

### Schema Subpackage (niceyaml/schema)

- **Generator** (`schema/generate/`): Creates JSON schemas from Go types using `github.com/invopop/jsonschema`.
- **Validator** (`schema/validate/`): Validates data against JSON schemas using `github.com/santhosh-tekuri/jsonschema/v6`.

Validator integrates with our core package for improved YAML handling (e.g. supporting error annotations).

### Line Subpackage (niceyaml/line)

- **Line Processing** (`line/`): Line-by-line YAML token organization.
- Key types: `Line`, `Lines`, `Annotation`, `Flag`.

### Position Subpackage (niceyaml/position)

- **Positioning** (`position/`): 0-indexed position and range tracking.
- Key types: `Position`, `Range`, `Ranges`.
- Convention: 0-indexed (line and column start at 0), half-open ranges `[Start, End)`.

### Tokens Subpackage (niceyaml/tokens)

- **Token Segmentation** (`tokens/`): YAML token segmentation for rendering.
- Key types: `Segment`, `Segments`, `Segments2`.

### YAMLTest Subpackage (niceyaml/yamltest)

- **Testing Utilities** (`yamltest/`): Helpers for testing YAML token operations.
- Key types: `TokenBuilder`, `MockValidator`, `MockNormalizer`.
- Key functions: `RequireTokensValid`, `AssertTokensEqual`, `FormatToken`, `FormatTokens`.

## Code Style

### Go Conventions

- Document all exported items with doc comments.
- Use `[Name]` syntax for Go doc links.
- Package documentation in `doc.go` files.
- Wrap errors with `fmt.Errorf("context: %w", err)`; no "failed" or "error" in messages.
- Use global error variables for common errors.

### Code Patterns

- Functional options pattern used throughout (e.g., `PrinterOption`, `SourceOption`, `ErrorOption`).
- 0-indexed positioning convention for `position.Position` (line and column start at 0).
- Half-open ranges `[Start, End)` for `position.Range`.
- Prefer consistency over performance, avoid "fast paths" that could lead to unpredictable behavior.

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
- `charm.land/bubbletea/v2`: Terminal UI framework.
- `charm.land/bubbles/v2`: UI components for Bubble Tea.
- `github.com/invopop/jsonschema`: JSON schema generation from Go types.
- `github.com/santhosh-tekuri/jsonschema/v6`: JSON schema validation.
