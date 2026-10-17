<p align="center">
  <h1 align="center">Nice YAML!</h1>
</p>

<p align="center">
  <a href="https://pkg.go.dev/go.jacobcolvin.com/niceyaml"><img alt="Go Reference" src="https://pkg.go.dev/badge/go.jacobcolvin.com/niceyaml.svg"></a>
  <a href="https://goreportcard.com/report/go.jacobcolvin.com/niceyaml"><img alt="Go Report Card" src="https://goreportcard.com/badge/go.jacobcolvin.com/niceyaml"></a>
  <a href="https://codecov.io/gh/macropower/niceyaml"><img src="https://codecov.io/gh/macropower/niceyaml/graph/badge.svg?token=4TNYTL2WXV"/></a>
  <a href="#installation"><img alt="Latest tag" src="https://img.shields.io/github/v/tag/macropower/niceyaml?label=version&sort=semver"></a>
  <a href="https://github.com/macropower/niceyaml/blob/main/LICENSE"><img alt="License" src="https://img.shields.io/github/license/macropower/niceyaml"></a>
</p>

Package `niceyaml` combines the powers of [go-yaml][goccy/go-yaml], [bubbletea][bubbletea], and more.

It enables **friendly and predictable handling of YAML-compatible documents** in your **CLI** or **TUI** applications, and includes:

- [`line.View`][niceyaml/line.View] **style overlay** and **annotation** system over a [`Source`][niceyaml.Source]
- Pretty [`printer`][niceyaml/printer] with [themes][niceyaml/style/theme]
- Rich [`Error`][niceyaml.Error] display using the above systems
- Source [**diffs**][niceyaml/diff] between revisions of a file
- String [`finder`][niceyaml/finder] for load-once, search-many scenarios
- [`Node`][niceyaml.Node] decoding with validation hooks, and a matching [`encoder`][niceyaml/encoder]
- JSON schema [validation][niceyaml/schema.Schema] with YAML path errors
- Document [`edit`][niceyaml/edit]s that keep comments and formatting, with errors bound at the lines the user wrote
- [`manifest`][niceyaml/manifest] loading and migration of configuration that declares its `apiVersion` and `kind`, as Kubernetes manifests do
- Bubble [`yamlviewport`][niceyaml/bubbles/yamlviewport] for Bubble Tea, in a module of its own
- [`fangs`][niceyaml/fangs] adapters for CLIs built with fang, in a module of their own
- Generic building blocks for your own bubbles

We use a **parse-once**, **style-once** approach. This means your users get a snappy UI, and you get a simple API. There's no need to employ multiple lexers, or perform any ANSI manipulation!

Every part of `niceyaml` shares one **positioning system**. Each position names a line of some content and a column on that line, counted in runes from zero. Slices of a view keep the positions of the whole, so a range from a search or an error applies to a slice as it is. Diffs render new content with rows of their own. The `diff.Result` of each diff maps each row to the line of the revision it shows, and each line of a revision to its row.

## Features

### Search

![view](./docs/assets/view.gif)

### Revision Diffs

![revisions](./docs/assets/revisions.gif)

### Themes

![themes](./docs/assets/themes.gif)

### Validation

![validate](./docs/assets/validate.gif)

## Installation

```sh
go get go.jacobcolvin.com/niceyaml@latest
```

`niceyaml` requires Go 1.27 or later, since `Decode[T]` on `Source` and
`Node` is a generic method, which Go 1.27 introduced.

## Usage

### Core Abstractions

Module `niceyaml` adds a few abstractions on top of [go-yaml][goccy/go-yaml]:

- [`line.Line`][niceyaml/line] - Tokens for a single line of YAML content
- [`line.Lines`][niceyaml/line] - A collection of `Line`s, which never changes after creation
- [`line.View`][niceyaml/line] - One rendering of a `Lines` value, which carries the overlays, annotations, and flags the printer draws
- [`niceyaml.Source`][niceyaml.Source] - A YAML file, which parses into one `Node` per document, decodes, wraps errors, exposes its `Lines`, and returns fresh `View`s over them

Most use cases will only need to interact with `Source`. Its `Lines` method returns the `line.Lines` that the [`finder`][niceyaml/finder] and [`diff`][niceyaml/diff] packages read, and its `View` method returns a fresh `line.View` over them for the [`printer`][niceyaml/printer]. Both packages read a view too, so a search or a diff of one document takes the `View` of its `Node` and reports the lines of the file. Diffs return plain views, since interleaved lines from two revisions are not a YAML document. A program that replaces a scalar on one line can splice the bytes of `Text` at the offsets that `Offset` gives. The [`edit`][niceyaml/edit] package renames keys and sets, moves, and removes values, and it keeps every byte that an edit leaves alone.

These abstractions let you iterate over arbitrary lines of tokens from one or more YAML documents, and they keep the original token details from the lexer. Multi-line and overlapping tokens cause common problems in diffs, partial rendering, and search, and these abstractions solve them.

```mermaid
flowchart LR
    A["Input"]
    subgraph Source
        B["Lexer"] --> C["Parser"]
    end
    A --> Source
    C --> D["Decoder"]
    D --> E["Struct"]
```

### Printer

- [examples/printer](examples/printer)
- [examples/annotations](examples/annotations)
- [examples/renderer](examples/renderer)

A [`line.View`][niceyaml/line.View] carries the notes and marks of one rendering, and the printer decides how they look. The annotations example annotates lines, marks ranges, and prints the same view wrapped to a width and with a gutter of its own.

Views do not depend on the printer. The renderer example writes an HTML renderer from the segments, flags, and annotations of a view. It renders a diff with the same function.

### Themes

- [examples/themes/catalog](examples/themes/catalog)
- [examples/themes/custom](examples/themes/custom)

The catalog example looks up a [theme][niceyaml/style/theme] by name for a `-theme` flag and sets the color profile for a `-color` flag. The custom example reads a theme from a YAML file, checks each color and style string, and builds the theme with [`theme.FromColors`][niceyaml/style/theme.FromColors].

### Diffs

- [examples/diffs](examples/diffs)
- [examples/review](examples/review)

The review example diffs one document of a file, so the gutter keeps the line numbers of the file. It carries the validation errors of each revision onto the diff, prints the hunks around the changes and the errors, and prints the two revisions side by side.

### Finder

- [examples/finder](examples/finder)

### Paths

- [examples/paths](examples/paths)

A [`paths.Path`][niceyaml/paths] such as `$.menu.lunch[0].price` names one node of a document, and a path with a wildcard names several. A [`Node`][niceyaml.Node] decodes the value at a path, lists the nodes a path selects, and returns the path at a position.

### Errors

- [examples/errors](examples/errors)
- [examples/reports](examples/reports)

An [`Error`][niceyaml.Error] names a path and holds no source. The errors example binds such errors to a `Source`. It then renders them as a styled excerpt, as plain text for a log, as marks on the whole document, and as one row per problem.

The reports example checks several files and keeps each error after [`niceyaml.Detach`][niceyaml.Detach] frees its `Source`. At the end, it reports every problem as an excerpt, as GitHub workflow commands, and as Markdown.

### Warnings

- [examples/warnings](examples/warnings)

[`niceyaml.Warnings`][niceyaml.Warnings] collects the problems that do not fail a decode. The example reports warnings beside errors, turns them into errors for a strict mode, and reads the severity of each problem.

### Decoding

- [examples/decoding](examples/decoding)
- [examples/limits](examples/limits)

The decoding example starts each value that a document leaves out from the defaults of its type. It also reports every problem of a document at once. The limits example decodes uploads that a server does not trust. It caps the size, depth, and aliases of each upload, and it reads shared anchors from a file that the server ships. It then maps each error to an HTTP status with [`niceyaml.IsInvalid`][niceyaml.IsInvalid].

### Validators

- [examples/validators](examples/validators)

A validator checks a document before the decoder reads it. The example rejects an unknown field at decode time. It then runs rules of its own with `MultiValidator`, which reports every failure, and with `ChainValidator`, which stops at the first. One rule checks a single breakfast, and `Each` runs it on every breakfast, so its error names the breakfast that failed.

### Schemas

- [examples/schemas/cafe](examples/schemas/cafe)
- [examples/schemas/registry](examples/schemas/registry)
- [examples/schemas/resolvers](examples/schemas/resolvers)
- [examples/schemas/untrusted](examples/schemas/untrusted)
- [examples/schemas/deprecations](examples/schemas/deprecations)

Types declare their constraints with `jsonschema` struct tags. The `gen` tool from [go.jacobcolvin.com/x/jsonschema](https://pkg.go.dev/go.jacobcolvin.com/x/jsonschema) turns them into [cafe.v1.json](examples/schemas/cafe/cafe.v1.json). Run `go run ./examples/schemas/cafe/demo` to validate a config against that schema and render any failures as source-annotated errors.

`gen` names each property as `encoding/json` names the field. The decoder reads the `yaml` tag, or the `json` tag when a field has no `yaml` tag, and lowercases the Go name when that tag names none. Each field of the example has a `json` tag that names it, so each property matches the key that the decoder reads. The tests of the example call [`schema.CheckFields`][niceyaml/schema.CheckFields], which reports each field where the two disagree.

A [`schema.Registry`][niceyaml/schema.Registry] picks the schema of each document, so one file can hold documents of several kinds. Run `go run ./examples/schemas/registry` to route the documents of a stream by a `$schema` comment or by their `kind` and report the ones that fail.

The resolvers of a registry run in order, and the first one that does not decline picks the schema. The resolvers example puts a directive in the file ahead of a match on its path. When no resolver picks a schema, the error lists the reasons that the resolvers gave. The untrusted example validates uploads whose directives name files and URLs that the server must not read. The deprecations example reports the `deprecated` keyword of a schema as a warning beside the violations.

### Encoder

- [examples/encoder](examples/encoder)

[`encoder.Marshal`][niceyaml/encoder] writes a Go value as YAML. `encoder.Pretty` gives it the layout prettier writes, and `encoder.WithYAMLComments` puts a comment at each YAML path you name. A `Source` reads the result as it reads any document, so the printer highlights it.

### Layers

- [examples/layers](examples/layers)

[`niceyaml.NewSourceFromLayers`][niceyaml.NewSourceFromLayers] merges several files into one document, such as a base file with the file of one environment over it. The example merges two files and traces each value to the file that set it. It then validates the merged document, overrides a value from the environment, skips a missing optional file, and decodes the result over defaults. The docs of `NewSourceFromLayers` list the merge rules.

### Edits

- [examples/edits/rewrite](examples/edits/rewrite)
- [examples/edits/guards](examples/edits/guards)

[`edit.Doc`][niceyaml/edit.Doc] renames keys and sets, moves, and removes values. It keeps every byte that an edit leaves alone. The rewrite example edits a file and validates the result, so each error points at the line that the owner wrote. The guards example shows the checks that keep an edit from damaging a file. When an `Apply` fails, it drops every edit it made. Wildcards skip the protected keys.

The `edit` package edits a subset of YAML and refuses the rest with `edit.ErrUnsupported`, whose detail says what to write instead. The guards example shows such a refusal for the removal of an anchored value that aliases still read. The engine reads and edits a document as go-yaml reads it, also where go-yaml reads valid YAML otherwise than YAML does. `edit.New` refuses a few such documents with `edit.ErrMisread`, such as one with an own key before a `<<` merge key that brings in the same key. Its detail says what to write so that go-yaml reads the text as YAML does. The Supported YAML section of the [package documentation][niceyaml/edit] lists the subset and those documents.

### Manifests

- [examples/manifests/load](examples/manifests/load)
- [examples/manifests/migrate](examples/manifests/migrate)

[`manifest.Registry`][niceyaml/manifest.Registry] declares the kinds of an app once and loads a directory of manifests into typed Go values. The load example reports every problem of a broken directory in one error. The migrate example reads files of version `v1beta1` as the current Go type, then plans, diffs, and applies the rewrite to `v1`.

### Viewport

- [examples/viewport](examples/viewport)

See [cmd/nyaml](cmd/nyaml) for a complete Bubble Tea application that loads, pages, searches, diffs, and validates YAML documents.

[`yamlviewport`][niceyaml/bubbles/yamlviewport] builds on the public API of `niceyaml` alone, so your own bubble has every building block it uses. [`printer.Layout`][niceyaml/printer.Layout] maps each position to its row and cell and each cell back to its position, and [`printer.Cut`][niceyaml/printer.Cut] cuts a printed row to a window of those cells.

[goccy/go-yaml]: https://github.com/goccy/go-yaml
[lipgloss]: https://github.com/charmbracelet/lipgloss
[bubbletea]: https://github.com/charmbracelet/bubbletea
[go.jacobcolvin.com/x/jsonschema]: https://github.com/MacroPower/x/tree/main/jsonschema
[niceyaml.Detach]: https://pkg.go.dev/go.jacobcolvin.com/niceyaml#Detach
[niceyaml.Error]: https://pkg.go.dev/go.jacobcolvin.com/niceyaml#Error
[niceyaml.IsInvalid]: https://pkg.go.dev/go.jacobcolvin.com/niceyaml#IsInvalid
[niceyaml.NewSourceFromLayers]: https://pkg.go.dev/go.jacobcolvin.com/niceyaml#NewSourceFromLayers
[niceyaml.Node]: https://pkg.go.dev/go.jacobcolvin.com/niceyaml#Node
[niceyaml.Source]: https://pkg.go.dev/go.jacobcolvin.com/niceyaml#Source
[niceyaml.Warnings]: https://pkg.go.dev/go.jacobcolvin.com/niceyaml#Warnings
[niceyaml/diff]: https://pkg.go.dev/go.jacobcolvin.com/niceyaml/diff
[niceyaml/edit]: https://pkg.go.dev/go.jacobcolvin.com/niceyaml/edit
[niceyaml/edit.Doc]: https://pkg.go.dev/go.jacobcolvin.com/niceyaml/edit#Doc
[niceyaml/encoder]: https://pkg.go.dev/go.jacobcolvin.com/niceyaml/encoder
[niceyaml/finder]: https://pkg.go.dev/go.jacobcolvin.com/niceyaml/finder
[niceyaml/line]: https://pkg.go.dev/go.jacobcolvin.com/niceyaml/line
[niceyaml/line.View]: https://pkg.go.dev/go.jacobcolvin.com/niceyaml/line#View
[niceyaml/manifest]: https://pkg.go.dev/go.jacobcolvin.com/niceyaml/manifest
[niceyaml/manifest.Registry]: https://pkg.go.dev/go.jacobcolvin.com/niceyaml/manifest#Registry
[niceyaml/paths]: https://pkg.go.dev/go.jacobcolvin.com/niceyaml/paths
[niceyaml/printer]: https://pkg.go.dev/go.jacobcolvin.com/niceyaml/printer
[niceyaml/printer.Cut]: https://pkg.go.dev/go.jacobcolvin.com/niceyaml/printer#Cut
[niceyaml/printer.Layout]: https://pkg.go.dev/go.jacobcolvin.com/niceyaml/printer#Layout
[niceyaml/style/theme]: https://pkg.go.dev/go.jacobcolvin.com/niceyaml/style/theme
[niceyaml/style/theme.FromColors]: https://pkg.go.dev/go.jacobcolvin.com/niceyaml/style/theme#FromColors
[niceyaml/style/kind.Kind]: https://pkg.go.dev/go.jacobcolvin.com/niceyaml/style/kind#Kind
[niceyaml/bubbles/yamlviewport]: https://pkg.go.dev/go.jacobcolvin.com/niceyaml/bubbles/yamlviewport
[niceyaml/fangs]: https://pkg.go.dev/go.jacobcolvin.com/niceyaml/fangs
[niceyaml/schema.CheckFields]: https://pkg.go.dev/go.jacobcolvin.com/niceyaml/schema#CheckFields
[niceyaml/schema.Registry]: https://pkg.go.dev/go.jacobcolvin.com/niceyaml/schema#Registry
[niceyaml/schema.Schema]: https://pkg.go.dev/go.jacobcolvin.com/niceyaml/schema#Schema
