// Package niceyaml provides utilities for working with YAML documents,
// built on top of [github.com/goccy/go-yaml].
//
// It enables consistent, intuitive, and predictable experiences for developers
// and users when working with YAML and YAML-compatible documents.
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
