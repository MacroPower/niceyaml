<p align="center">
  <h1 align="center">Nice YAML!</h1>
</p>

<p align="center">
  <a href="https://pkg.go.dev/github.com/macropower/niceyaml"><img alt="Go Reference" src="https://pkg.go.dev/badge/github.com/macropower/niceyaml.svg"></a>
  <a href="https://goreportcard.com/report/github.com/macropower/niceyaml"><img alt="Go Report Card" src="https://goreportcard.com/badge/github.com/macropower/niceyaml"></a>
  <a href="https://codecov.io/gh/macropower/niceyaml"><img src="https://codecov.io/gh/macropower/niceyaml/graph/badge.svg?token=4TNYTL2WXV"/></a>
  <a href="#-installation"><img alt="Latest tag" src="https://img.shields.io/github/v/tag/macropower/niceyaml?label=version&sort=semver"></a>
  <a href="https://github.com/macropower/niceyaml/blob/main/LICENSE"><img alt="License" src="https://img.shields.io/github/license/macropower/niceyaml"></a>
</p>

Package `niceyaml` combines the powers of [go-yaml][goccy/go-yaml], [bubbletea][bubbletea], and more.

It provides opinionated utilities for **friendly and predictable handling of YAML documents** in your **CLI** or **TUI** applications.

> Also supports all YAML-compatible document formats like KYAML or JSON.

![demo](./examples/demo.png)

## ✨ Features

### Pretty Printing

- Render YAML with syntax highlighting via [lipgloss][lipgloss], directly from go-yaml's AST
- Wrap YAML errors from go-yaml's parser with fully styled source annotations
- Supports custom color schemes, style overlays (e.g. highlights), diff rendering, and more

### JSON Schema Generation & Validation

1. Generate JSON schemas from structs via [invopop/jsonschema][invopop/jsonschema]
2. Provide generated schemas to users, include them via embedding
3. Use your JSON schemas to validate the Generators via [santhosh-tekuri/jsonschema][santhosh-tekuri/jsonschema]
4. Users receive the same feedback from your application and their YAML language server!

## 📦 Installation

```sh
go get github.com/macropower/niceyaml@latest
```

## 🚀 Usage

See [examples/](./examples/).

If you want to try the examples locally, just run:

```sh
go run github.com/macropower/niceyaml/examples@latest
```

[goccy/go-yaml]: https://github.com/goccy/go-yaml
[lipgloss]: https://github.com/charmbracelet/lipgloss
[bubbletea]: https://github.com/charmbracelet/bubbletea
[invopop/jsonschema]: https://github.com/invopop/jsonschema
[santhosh-tekuri/jsonschema]: https://github.com/santhosh-tekuri/jsonschema
