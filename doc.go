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
// palettes; use [theme.Charm] as a sensible default.
//
// [Error] wraps errors with YAML source context.
//
// [Error.Error] returns the message with its position, and [Error.Detail]
// renders the surrounding lines with the error position highlighted. The %+v
// verb prints both.
//
// Multiple nested errors appear as annotations below their respective lines,
// with distant errors displayed in separate hunks.
//
// # Error Configuration
//
// Sources can pre-configure error formatting via [WithErrorOptions], which
// stores [ErrorOption] values applied when [*Source.WrapError] converts errors:
//
//	source, _ := niceyaml.NewSourceFromFile("config.yaml",
//		niceyaml.WithErrorOptions(
//			niceyaml.WithContextLines(3),
//			niceyaml.WithPrinter(myPrinter),
//		),
//	)
//
//	// Later, WrapError applies the stored options automatically.
//	if err := validate(source); err != nil {
//		return source.WrapError(err)
//	}
//
// This separates error production (validators, decoders) from error
// presentation (source context, formatting), allowing each layer to provide
// what it knows.
//
// # Validation Pipeline
//
// For structured validation, [Decoder] iterates over documents in an
// [*ast.File] and [DocumentDecoder] provides the validation pipeline:
//
//	source := niceyaml.NewSourceFromString(yamlContent)
//	decoder, _ := source.Decoder()
//	for _, doc := range decoder.Documents() {
//		config, err := doc.Unmarshal[Config](ctx)
//		if err != nil {
//			return source.WrapError(err)
//		}
//	}
//
// [DocumentDecoder.Unmarshal] supports two validation hooks: types implementing
// [SchemaValidator] are validated against an external schema before decoding,
// and types implementing [Validator] are self-validated after decoding.
// [DocumentDecoder.UnmarshalInto] runs the same pipeline on a value you already
// hold, such as one pre-populated with defaults.
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
//	lines, spans := result.Hunks(3)
//	fmt.Println(printer.Print(lines, spans...))
//
// [Revision] chains document versions in a doubly-linked list, useful for
// tracking history across many versions. Any two revisions can be diffed:
//
//	revs := niceyaml.NewRevision(original).Append(modified)
//	result := niceyaml.Diff(revs.Origin(), revs.Tip())
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
//	for _, rng := range finder.Find("search term") {
//		source.AddOverlay(style.GenericInserted, rng)
//	}
package niceyaml
