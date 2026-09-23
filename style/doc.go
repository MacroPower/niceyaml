// Package style maps the kinds of text in a rendering to lipgloss styles.
//
// When rendering YAML, each token (keys, strings, numbers, punctuation, etc.)
// needs distinct visual styling. A [Styles] value maps each
// [kind.Kind] to the [lipgloss.Style] it
// renders with. The kind package declares the kinds and their hierarchy and
// depends on no terminal library, so the packages that mark content, such
// as line and diff, name kinds without depending on lipgloss; this package
// puts the styles behind them.
//
// Rather than requiring themes to define every possible kind, a Styles value
// resolves inheritance. A kind that is not set falls back to its parent in
// the hierarchy. For example, [kind.LiteralNumberFloat] inherits from
// [kind.LiteralNumber], which inherits from [kind.Literal], which inherits
// from [kind.Text].
//
// # Construction
//
// [New] creates a [Styles] value that resolves inherited styles.
//
// Provide a base [lipgloss.Style] and use [Set] to override specific
// kinds:
//
//	styles := style.New(
//	    lipgloss.NewStyle().Foreground(lipgloss.Color("white")),
//	    style.Set(kind.Comment, lipgloss.NewStyle().Foreground(lipgloss.Color("8"))),
//	    style.Set(kind.LiteralNumber, lipgloss.NewStyle().Foreground(lipgloss.Color("cyan"))),
//	)
//
// With this configuration, [kind.LiteralNumberFloat] and
// [kind.LiteralNumberInteger] inherit the cyan foreground from
// [kind.LiteralNumber], while [kind.LiteralString] falls back to white.
// [Styles.With] derives a new value with more overrides and resolves
// inheritance again, so overriding a parent later reaches its children too.
//
// A custom kind, such as one a program names for its own overlay, has no
// parent of its own, so it inherits from [kind.Text]. [Inherit] places it
// under a predefined kind, and it then takes that kind's style under any
// theme, with no color of its own.
//
//	const match kind.Kind = "match"
//
//	styles := theme.Charm.Styles().With(style.Inherit(match, kind.GenericHighlight))
//
// A [Set] on the custom kind still wins over the inherited style.
//
// # Themes
//
// The [go.jacobcolvin.com/niceyaml/style/theme] subpackage provides
// predefined themes (Monokai, Dracula, Catppuccin, etc.). A program looks a
// theme up by name and gets [Styles] with the colors of that palette.
//
// # Style Strings
//
// This package provides encoding and decoding of Pygments-style strings to and
// from [lipgloss.Style] objects via [Parse], [MustParse], and [Encode].
//
// Pygments-style strings are a format for specifying text styling. They are
// commonly used in syntax highlighting configurations and theme files.
//
// A Pygments-style string holds space-separated tokens, and [Parse] applies
// them left to right. A later color replaces an earlier one of the same kind,
// and a no* keyword clears the attribute only when it follows the keyword that
// set it, so "bold nobold" leaves bold off and "nobold bold" turns it on.
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
