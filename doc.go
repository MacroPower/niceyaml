// Package niceyaml provides utilities for working with YAML documents,
// built on top of [github.com/goccy/go-yaml].
//
// It enables consistent, intuitive, and predictable experiences for developers
// and users when working with YAML and YAML-compatible documents.
//
// # Line Management
//
// The core abstraction is [Lines], which organizes YAML tokens by line number.
// A [Lines] value can be created from various sources:
//
//   - [NewLinesFromString]: Parse YAML from a string
//   - [NewLinesFromFile]: Extract tokens from an [ast.File]
//   - [NewLinesFromToken]: Build from a single token
//   - [NewLinesFromTokens]: Build from a token slice
//
// Each [Line] contains the tokens for that line, along with optional metadata:
//
//   - [Annotation]: Extra content such as error messages or diff headers
//   - [Flag]: Category markers for diff display ([FlagInserted], [FlagDeleted])
//
// # Revision Tracking
//
// [Revision] represents a version of [Lines] in a doubly-linked chain,
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
// # Styled YAML Printing
//
// The [Printer] type renders YAML tokens with syntax highlighting via lipgloss.
// You can customize colors, enable line numbers, highlight specific ranges,
// and render diffs between documents.
//
// # Error Formatting
//
// The [Error] type wraps errors with YAML source context and precise location
// information. When printed, errors display the relevant portion of the source
// with the error location highlighted.
//
// Use [ErrorWrapper] to create errors with consistent default options.
//
// # String Finding
//
// The [Finder] type searches for strings within YAML tokens, returning
// [PositionRange] values that can be used with [Printer.AddStyleToRange]
// for highlighting matches.
//
// # Schema Generation and Validation
//
// See the [github.com/macropower/niceyaml/schema] subpackage.
package niceyaml
