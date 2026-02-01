// Package style provides a hierarchical styling system for YAML syntax
// highlighting.
//
// When rendering YAML, each token (keys, strings, numbers, punctuation, etc.)
// needs distinct visual styling.
//
// Rather than requiring themes to define every possible token type, this
// package uses inheritance: unspecified styles automatically fall back to their
// parent category.
//
// For example, [LiteralNumberFloat] inherits from [LiteralNumber], which
// inherits from [Literal], which inherits from [Text].
//
// # Style Categories
//
// [Style] constants identify token categories following Pygments naming
// conventions.
//
// The hierarchy is organized into major groups:
//
//   - [Text]: Base style inherited by all others
//   - [Comment], [CommentPreproc]: Comments and directives
//   - [Literal] -> [LiteralString], [LiteralNumber], [LiteralBoolean],
//     [LiteralNull]: Values
//   - [Name] -> [NameTag], [NameAnchor], [NameAlias], [NameDecorator]: Identifiers
//   - [Punctuation] -> [PunctuationMapping], [PunctuationSequence],
//     [PunctuationBlock]: Syntax
//   - [Generic] -> [GenericDeleted], [GenericInserted], [GenericError]: Diff
//     and error markers
//   - [Search], [SearchSelected]: Search match highlights
//
// [Style] constants start at 1,000,000 to avoid collisions with user-defined
// overlay keys (used for highlighting specific positions like errors).
//
// # Creating Style Maps
//
// [NewStyles] creates a [Styles] map that pre-computes inherited styles.
//
// Provide a base [lipgloss.Style] and use [Set] to override specific
// categories:
//
//	styles := style.NewStyles(
//	    lipgloss.NewStyle().Foreground(lipgloss.Color("white")),
//	    style.Set(style.Comment, lipgloss.NewStyle().Foreground(lipgloss.Color("8"))),
//	    style.Set(style.LiteralNumber, lipgloss.NewStyle().Foreground(lipgloss.Color("cyan"))),
//	)
//
// With this configuration, [LiteralNumberFloat] and [LiteralNumberInteger]
// inherit the cyan foreground from [LiteralNumber], while [LiteralString] falls
// back to white.
//
// # Themes
//
// The [jacobcolvin.com/niceyaml/style/theme] subpackage provides
// predefined themes (Monokai, Dracula, Catppuccin, etc.).
//
// Each theme is a function returning [Styles] with colors appropriate for that
// palette.
//
// [Mode] indicates whether a theme targets light or dark backgrounds.
package style
