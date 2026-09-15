// Package niceyaml provides utilities for working with YAML documents.
// It is built using [yaml] and [lipgloss].
//
// By directly styling YAML tokens a single time, niceyaml is much more
// consistent, flexible, and performant, when compared to using multiple
// distinct styling systems.
//
// It also provides an alternative implementation of go-yaml's source
// annotations for errors, as well as adapters for use with JSON schema
// validation.
//
// # Usage
//
// Parse YAML into a [Source], then use a [Printer] to render it with syntax
// highlighting:
//
//	source := niceyaml.NewSourceFromString(yamlContent)
//	printer := niceyaml.NewPrinter()
//	fmt.Println(printer.Print(source))
//
// When errors occur, wrap them with source context to show users exactly where
// the problem is:
//
//	file, err := source.File()
//	if err != nil {
//		// The %+v verb displays the YAML with the problematic location
//		// highlighted; plain %v prints the message and position only.
//		fmt.Printf("%+v\n", source.WrapError(err))
//	}
//
// # Architecture
//
// The package separates a YAML document from its rendering.
//
// [Source] is the document. It owns the tokens from go-yaml, lazily parses
// them into an AST with [Source.File], iterates documents with
// [Source.Decoder], and attaches source context to errors with
// [Source.WrapError].
//
// [line.Lines] is the view. It organizes tokens into lines, and each line
// carries optional metadata for rendering. Annotations hold error messages
// and diff headers, flags mark inserted and deleted lines, and overlays apply
// style spans for highlighting. A Source exposes its view through
// [Source.Lines] and delegates the view methods, so highlighting through
// either path renders identically.
//
// A view need not be a YAML document. Diffs, for example, interleave lines
// from two revisions and are plain [line.Lines] values.
//
// [Printer] renders any [LineIterator], which both [*Source] and [line.Lines]
// satisfy, with syntax highlighting via lipgloss.
//
// It supports customizable gutters (line numbers, diff markers), word wrapping,
// and annotation rendering.
//
// Themes from [go.jacobcolvin.com/niceyaml/style/theme] provide color
// palettes. Without one, [Printer] renders with [style.Default].
//
// [Error] points at a location in a YAML document: a path, a token, or a
// range. [Error.Error] returns the message with that location, and nothing
// more, so a validator can build one without holding the source.
//
// [SourceError] binds an Error to its [Source]. [Source.WrapError] creates
// one, [SourceError.Error] reports the location as a resolved position, and
// [SourceError.Detail] renders the surrounding lines with the location
// highlighted. The %+v verb prints both. Nested errors appear as annotations
// below their respective lines, with distant errors displayed in separate
// hunks.
//
// # Error Presentation
//
// The %+v verb renders a [SourceError] with a default [Printer] and two
// lines of context. The code that prints the error chooses anything else,
// through [DetailOption] values passed to [SourceError.Render] or
// [SourceError.Detail]:
//
//	var bound *niceyaml.SourceError
//	if errors.As(err, &bound) {
//		fmt.Println(bound.Render(
//			niceyaml.WithPrinter(printer),
//			niceyaml.WithContextLines(3),
//		))
//	}
//
// This separates error production (validators, decoders) from error
// presentation (source context, formatting), allowing each layer to provide
// what it knows: a validator the path, a source the document, and the
// caller that prints the terminal width and theme.
//
// # Validation Pipeline
//
// For structured validation, [Decoder] iterates over documents in an
// [*ast.File] and [DocumentDecoder] provides the validation pipeline:
//
//	source := niceyaml.NewSourceFromString(yamlContent)
//	decoder, _ := source.Decoder()
//	for _, doc := range decoder.Documents() {
//		config, err := doc.Unmarshal[Config](ctx, niceyaml.WithSchema(validator))
//		if err != nil {
//			return source.WrapError(err)
//		}
//	}
//
// [DocumentDecoder.Unmarshal] supports two validation hooks: a
// [SchemaValidator] passed with [WithSchema] checks the document against an
// external schema before decoding, and a type implementing [Validator]
// validates itself after decoding. [DocumentDecoder.UnmarshalInto] runs the
// same pipeline on a value you already hold, such as one pre-populated with
// defaults.
//
// Both produce [Error] values with path information that [Source.WrapError] can
// annotate with source context.
//
// # Diffs
//
// [Differ] computes line differences using the [diff] package.
// The default [diff.Hirschberg] algorithm is space-efficient for large files:
//
//	result := niceyaml.Diff(original, modified)
//	printer := niceyaml.NewPrinter()
//	fmt.Println(printer.Print(result.Unified()))
//	fmt.Println(printer.Print(result.Hunks(3)))
//
// [Revisions] keeps the versions of a document in order, from the original
// to the latest. Any two revisions can be diffed:
//
//	revs := niceyaml.Revisions{original, modified}
//	result := niceyaml.Diff(revs[0], revs[1])
//
// Custom algorithms implement [diff.Algorithm]. For reusable differ instances:
//
//	differ := niceyaml.NewDiffer(niceyaml.WithAlgorithm(myAlgo))
//	result := differ.Diff(before, after)
//
// Diff output is a [line.Lines] view rather than a [Source], since the
// interleaved lines do not form a YAML document. It uses [line.Flag] to mark
// inserted/deleted lines and [line.Annotation] for unified diff hunk headers.
//
// # Text Search
//
// [Finder] locates strings within tokens, returning [position.Range] values
// suitable for [Source.AddOverlay] or [line.Lines.AddOverlay].
//
// Use [normalizer.New] with [WithNormalizer] for case-insensitive,
// diacritic-insensitive matching:
//
//	finder := niceyaml.NewFinder(niceyaml.WithNormalizer(normalizer.New()))
//	finder.Load(source)
//	view := source.Lines()
//	view.AddOverlay(style.GenericHighlight, finder.Find("search term")...)
//	fmt.Println(printer.Print(view))
//
// # Dependencies
//
// niceyaml is a facade over go-yaml. The exported API names go-yaml types
// only where niceyaml wraps the document model rather than hiding it: the
// [*ast.File] and [*ast.DocumentNode] a [Source] parses into, the
// [token.Tokens] it lexes, and [ast.Node] and [*token.Token] as results of
// resolving a [paths.Path]. Positions, ranges, lines, errors, and styles are
// niceyaml's own types, and [paths.Path.YAMLPath] converts to go-yaml's
// path type when a caller needs it.
//
// Every go-yaml setting has a named option, such as [WithAllowDuplicateKeys]
// or [WithIndent]. The options that pass go-yaml values through carry a YAML
// prefix, as in [WithYAMLDecodeOptions] and [WithYAMLEncodeOptions], so a
// caller can tell at the call site when the go-yaml dependency shows. A test
// in this package enforces both rules on every exported declaration.
//
// Rendering builds on lipgloss, and the [style] package exposes its Style
// type directly since a theme is a set of lipgloss styles. The
// [go.jacobcolvin.com/niceyaml/cmd/nyaml/fangs] and
// [go.jacobcolvin.com/niceyaml/bubbles/yamlviewport] packages are adapters
// for the charm libraries they build on and expose those libraries' types by
// design.
package niceyaml
