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
// Themes from [jacobcolvin.com/niceyaml/style/theme] provide color
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
// # Validation Pipeline
//
// For structured validation, [Decoder] iterates over documents in an
// [*ast.File] and [DocumentDecoder] provides the validation pipeline:
//
//	source := niceyaml.NewSourceFromString(yamlContent)
//	file, _ := source.File()
//	decoder := niceyaml.NewDecoder(file)
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
// [FullDiff] and [SummaryDiff] compute line differences using Hirschberg's LCS
// algorithm:
//
//	revs := niceyaml.NewRevision(original).Append(modified)
//	source, spans := niceyaml.NewSummaryDiff(revs.Origin(), revs.Tip(), 3).Build()
//	fmt.Println(printer.Print(source, spans...))
//
// The diff output uses [line.Flag] to mark inserted/deleted lines and
// [line.Annotation] for unified diff hunk headers.
//
// # Text Search
//
// [Finder] locates strings within tokens, returning [position.Range] values
// suitable for [Source.AddOverlay].
//
// Use [StandardNormalizer] with [WithNormalizer] for case-insensitive,
// diacritic-insensitive matching:
//
//	finder := niceyaml.NewFinder(niceyaml.WithNormalizer(niceyaml.NewStandardNormalizer()))
//	finder.Load(source)
//	for _, rng := range finder.Find("search term") {
//		source.AddOverlay(style.GenericInserted, rng)
//	}
package niceyaml
