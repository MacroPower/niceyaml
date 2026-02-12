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
//   - [Text] -> [TextOK], [TextWarn], [TextError]: Base text styles
//   - [Comment], [CommentPreproc]: Comments and directives
//   - [Literal] -> [LiteralString], [LiteralNumber], [LiteralBoolean],
//     [LiteralNull]: Values
//   - [Name] -> [NameTag], [NameAnchor], [NameAlias]: Identifiers
//   - [Punctuation] -> [PunctuationMapping], [PunctuationSequence],
//     [PunctuationBlock]: Syntax
//   - [Generic] -> [GenericDeleted], [GenericInserted], [GenericError]: Diff
//     and error markers
//   - [GenericHighlight] -> [GenericHighlightDim]: Search and selection highlights
//   - [TextAccent] -> [TextAccentDim]: Emphasized text
//   - [TextSubtle] -> [TextSubtleDim]: De-emphasized text
//   - [GenericHeading] -> [GenericHeadingAccent], [GenericHeadingSubtle],
//     [GenericHeadingOK], [GenericHeadingWarn], [GenericHeadingError]: Headings
//
// # Pointer Identity
//
// [Styles] stores [*lipgloss.Style] pointers rather than values. This enables
// pointer equality comparisons during rendering: the internal color blender
// caches blend results keyed by style pointers, so identical style
// combinations always return the same pointer without redundant allocations.
//
// Consumers that construct [Styles] values should use [NewStyles] or [Styles.With]
// to preserve this property.
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
// The [go.jacobcolvin.com/niceyaml/style/theme] subpackage provides
// predefined themes (Monokai, Dracula, Catppuccin, etc.).
//
// Each theme is a function returning [Styles] with colors appropriate for that
// palette.
//
// [Mode] indicates whether a theme targets light or dark backgrounds.
//
// # Style Strings
//
// This package provides encoding and decoding of Pygments-style strings to and
// from [lipgloss.Style] objects via [Parse], [MustParse], and [Encode].
//
// Pygments-style strings are a compact, human-readable format for specifying
// text styling. They are commonly used in syntax highlighting configurations
// and theme files.
//
// Styles are specified as space-separated tokens. Order is not significant.
//
// Colors use hex format:
//
//	#rrggbb     - Foreground color (e.g., #ff0000 for red)
//	#rgb        - Short foreground color (e.g., #f00 for red)
//	bg:#rrggbb  - Background color
//
// Modifiers toggle text attributes:
//
//	bold / nobold           - Bold text
//	italic / noitalic       - Italic text
//	underline / nounderline - Underlined text
//
// Special tokens (ignored for Pygments compatibility):
//
//	noinherit
//	border:#rrggbb
//
// Example usage:
//
//	// A simple foreground color:
//	style, err := style.Parse("#ff0000")
//
//	// Bold text with a specific color:
//	style, err := style.Parse("bold #c678dd")
//
//	// Full specification with foreground and background:
//	style, err := style.Parse("#abb2bf bg:#282c34")
//
//	// For compile-time constants, use MustParse:
//	var keywordStyle = style.MustParse("bold #c678dd")
//
//	// To convert a style back to a string:
//	s := style.Encode(style) // "bold #c678dd"
package style
