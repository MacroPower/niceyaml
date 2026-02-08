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
//		// Error displays the YAML with the problematic location highlighted.
//		fmt.Println(source.WrapError(err))
//	}
//
// # Architecture
//
// The package centers on [Source], which organizes YAML tokens from go-yaml
// into [line.Lines].
//
// Each line tracks its tokens plus optional metadata: annotations (error
// messages, diff headers), flags (inserted/deleted), and overlays (style spans
// for highlighting).
//
// This line-oriented structure enables efficient rendering and precise position
// tracking.
//
// [Printer] renders any [LineIterator] (including [*Source]) with syntax
// highlighting via lipgloss.
//
// It supports customizable gutters (line numbers, diff markers), word wrapping,
// and annotation rendering.
//
// Themes from [go.jacobcolvin.com/niceyaml/style/theme] provide color
// palettes; use [theme.Charm] as a sensible default.
//
// [Error] wraps errors with YAML source context.
//
// When you know the error location (via token or path), [Error.Error] renders
// surrounding lines with the error position highlighted.
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
//			niceyaml.WithSourceLines(3),
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
//		var config Config
//		if err := doc.Unmarshal(&config); err != nil {
//			return source.WrapError(err)
//		}
//	}
//
// [DocumentDecoder.Unmarshal] supports two validation hooks: types implementing
// [SchemaValidator] are validated against an external schema before decoding,
// and types implementing [Validator] are self-validated after decoding.
//
// Both produce [Error] values with path information that [Source.WrapError] can
// annotate with source context.
//
// # Diffs
//
// [Revision] chains document versions in a doubly-linked list.
//
// [Differ] computes line differences using the [diff] package.
// The default [diff.Hirschberg] algorithm is space-efficient for large files:
//
//	revs := niceyaml.NewRevision(original).Append(modified)
//	result := niceyaml.Diff(revs.Origin(), revs.Tip())
//	printer := niceyaml.NewPrinter()
//	fmt.Println(printer.Print(result.Unified()))
//	source, spans := result.Hunks(3)
//	fmt.Println(printer.Print(source, spans...))
//
// Custom algorithms implement [diff.Algorithm]. For reusable differ instances:
//
//	differ := niceyaml.NewDiffer(niceyaml.WithAlgorithm(myAlgo))
//	result := differ.Diff(revA, revB)
//
// The diff output uses [line.Flag] to mark inserted/deleted lines and
// [line.Annotation] for unified diff hunk headers.
//
// # Text Search
//
// [Finder] locates strings within tokens, returning [position.Range] values
// suitable for [Source.AddOverlay].
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
