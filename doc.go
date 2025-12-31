// Package niceyaml provides utilities for working with YAML documents,
// built on top of [github.com/goccy/go-yaml].
//
// It enables consistent, intuitive, and predictable experiences for developers
// and users when working with YAML and YAML-compatible documents.
//
// # Line Management
//
// The core abstraction is [Source], which organizes YAML tokens by line number.
// A [Source] value can be created from various sources:
//
//   - [NewSourceFromString]: Parse YAML from a string
//   - [NewSourceFromToken]: Build from a single token
//   - [NewSourceFromTokens]: Build from a token slice
//
// Use [WithName] to assign a name to [Source] for identification.
// Use [Source.Parse] to parse tokens into an [ast.File] for further processing.
//
// [Source.Lines] and [Source.Runes] provide iterators for traversing
// lines and individual runes with their positions.
//
// Each [line.Line] contains the tokens for that line, along with optional metadata:
//
//   - [line.Annotation]: Extra content such as error messages or diff headers
//   - [line.Flag]: Category markers ([line.FlagInserted], [line.FlagDeleted], [line.FlagAnnotation])
//
// # Position Tracking
//
// [position.Position] represents a 0-indexed line and column location within a document.
// [position.Range] defines a half-open range [Start, End) for selecting spans
// of text. Create positions and ranges using [position.New] and [position.NewRange].
//
// These types integrate with the [Printer] for highlighting and with the [Finder]
// for search results.
//
// # Revision Tracking
//
// [Revision] represents a version of [Source] in a doubly-linked chain,
// enabling navigation through document history:
//
//	origin := niceyaml.NewRevision(original)
//	tip := origin.Append(modified)
//	tip.Origin() // returns origin
//
// # Diff Generation
//
// [FullDiff] and [SummaryDiff] compute differences between two revisions
// using a longest common subsequence (LCS) algorithm:
//
//	full := niceyaml.NewFullDiff(origin, tip)
//	full.Lines()  // all lines with inserted/deleted flags
//
//	summary := niceyaml.NewSummaryDiff(origin, tip, 3)
//	summary.Lines()  // changed lines with 3 lines of context
//
// The summary output follows unified diff format with hunk headers.
//
// # Style System
//
// The [Style] type identifies token categories for syntax highlighting.
// [Styles] maps style identifiers to lipgloss styles. Use [DefaultStyles]
// for a sensible default palette, or provide a custom [StyleGetter] to
// the [Printer].
//
// # Styled YAML Printing
//
// The [Printer] type renders YAML tokens with syntax highlighting via lipgloss.
// Use [Printer.Print] with any [LineIterator] (such as [*Source]) to render output.
// Alternatively, use [Printer.PrintSlice] to render a subset of lines.
//
// Configure printers using [PrinterOption] functions:
//
//   - [WithStyle]: Set the container style
//   - [WithStyles]: Provide custom token styles via [StyleGetter]
//   - [WithGutter]: Set a gutter function for line prefixes
//
// Built-in gutter functions include [DefaultGutter], [DiffGutter],
// [LineNumberGutter], and [NoGutter].
//
// Use [Printer.SetWidth] to enable word wrapping, and
// [Printer.AddStyleToRange] to highlight specific [position.Range] spans.
//
// # Error Formatting
//
// The [Error] type wraps errors with YAML source context and precise location
// information. When printed, errors display the relevant portion of the source
// with the error location highlighted.
//
// Create errors with [NewError] and configure with [ErrorOption] functions:
//
//   - [WithSourceLines]: Number of context lines to display
//   - [WithPath]: YAML path where the error occurred
//   - [WithErrorToken]: Token associated with the error
//   - [WithPrinter]: Customize error source formatting
//
// Use [ErrorWrapper] to create errors with consistent default options.
//
// # String Finding
//
// The [Finder] type searches for strings within YAML tokens, returning
// [position.Range] values that can be used with [Printer.AddStyleToRange]
// for highlighting matches. Configure search behavior with [WithNormalizer]
// to apply text normalization such as [StandardNormalizer] for case-insensitive
// matching.
//
// # YAML Utilities
//
// [Encoder] and [Decoder] wrap go-yaml functionality for encoding and
// decoding YAML documents with consistent error handling and
// validation via the [Validator] interface.
//
// [Decoder] provides an iterator-based API via [Decoder.Documents], which
// yields [DocumentDecoder] instances for each document in a multi-document
// YAML file:
//
//	decoder := niceyaml.NewDecoder(file)
//	for _, doc := range decoder.Documents() {
//	    var config Config
//	    if err := doc.Decode(&config); err != nil {
//	        return err
//	    }
//	}
//
// [DocumentDecoder] provides methods for working with individual documents:
// [DocumentDecoder.GetValue], [DocumentDecoder.Validate], [DocumentDecoder.Decode],
// and their context-aware variants.
//
// [NewPathBuilder] and [NewPath] create [*yaml.Path]s for pointing to specific
// YAML paths programmatically.
//
// # Schema Generation and Validation
//
// The [github.com/macropower/niceyaml/schema/generate] package provides JSON
// schema generation from Go types. Use [generate.NewGenerator] with options
// like [generate.WithPackagePaths] to include source comments as descriptions.
//
// The [github.com/macropower/niceyaml/schema/validate] package validates data
// against JSON schemas. Use [validate.NewValidator] to create validators that
// return [Error] values with precise YAML path information.
package niceyaml
