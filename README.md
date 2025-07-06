# Nice YAML!

[![Go Reference](https://pkg.go.dev/badge/github.com/macropower/niceyaml.svg)](https://pkg.go.dev/github.com/macropower/niceyaml)

Package `niceyaml` is an opinionated set of utilities for use with [go-yaml][goccy/go-yaml].

The goal of this package is to enable consistent, intuitive, and predictable experiences for developers and users when working with YAML and YAML-compatible documents (like KYAML or JSON).

## Features

### Styled YAML Printing

Render YAML with syntax highlighting via [lipgloss][lipgloss], directly from go-yaml's AST.

```go
p := niceyaml.NewPrinter(
    niceyaml.WithColorScheme(niceyaml.DefaultColorScheme()),
    niceyaml.WithLineNumbers(),
)

// Highlight a specific position (e.g., for search results).
p.AddStyleToRange(lipgloss.NewStyle(), niceyaml.PositionRange{
    Start: niceyaml.Position{Line: 2, Col: 1},
    End:   niceyaml.Position{Line: 2, Col: 5},
})

// Render diffs between two YAML documents.
diff := p.PrintTokenDiff(beforeTokens, afterTokens)
```

### JSON Schema Generation & Validation

Generate JSON schemas from Go types. Use your generated schemas for validation.

Wraps [invopop/jsonschema][invopop/jsonschema] for JSON Schema generation, and [santhosh-tekuri/jsonschema][santhosh-tekuri/jsonschema] for validation.

```go
import "github.com/macropower/niceyaml/schema"

// Generate schema from Go struct (extracts comments as descriptions).
gen := schema.NewGenerator(MyConfig{}, schema.WithPackagePaths("./..."))
schemaBytes, _ := gen.Generate()

// Validate data against schema.
validator := schema.MustNewValidator("config", schemaBytes)
if err := validator.Validate(data); err != nil {
    fmt.Println(err) // "error at $.users[0].email: must be a valid email"
}
```

### Error Formatting

Create user-friendly errors with source context and path information.

```go
p := niceyaml.NewPrinter(
    niceyaml.WithColorScheme(niceyaml.DefaultColorScheme()),
    niceyaml.WithLineNumbers(),
)

err := niceyaml.NewError(
    errors.New("invalid value"),
    niceyaml.WithPrinter(p),
    niceyaml.WithToken(token),
)

fmt.Println(err) // Styled output with source annotation.
```

[goccy/go-yaml]: https://github.com/goccy/go-yaml
[lipgloss]: https://github.com/charmbracelet/lipgloss
[invopop/jsonschema]: https://github.com/invopop/jsonschema
[santhosh-tekuri/jsonschema]: https://github.com/santhosh-tekuri/jsonschema
