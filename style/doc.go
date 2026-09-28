// Package style maps the kinds of text in a rendering to lipgloss styles.
//
// When rendering YAML, each token (keys, strings, numbers, punctuation, etc.)
// needs distinct visual styling. A [Styles] value maps each
// [kind.Kind] to the [lipgloss.Style] it
// renders with. The kind package declares the kinds and their hierarchy and
// depends on no terminal library, so the packages that mark content, such
// as line and diff, name kinds without depending on lipgloss. This package
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
//	    lipgloss.NewStyle().Foreground(lipgloss.Color("15")),
//	    style.Set(kind.Comment, lipgloss.NewStyle().Foreground(lipgloss.Color("8"))),
//	    style.Set(kind.LiteralNumber, lipgloss.NewStyle().Foreground(lipgloss.Color("6"))),
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
// [Parse] and [MustParse] decode Pygments-style strings into
// [lipgloss.Style] values, and [Encode] turns a style back into one.
//
// Pygments-style strings specify text styling. Syntax highlighting
// configurations and theme files commonly use them.
//
// A Pygments-style string holds space-separated tokens, and [Parse] applies
// them left to right. A later color replaces an earlier one of the same kind,
// and a no* keyword clears the attribute only when it follows the keyword that
// set it, so "bold nobold" leaves bold off and "nobold bold" turns it on.
//
// Colors use hex format:
//
//   - #rrggbb sets the foreground color, such as #ff0000 for red.
//   - #rgb sets the foreground color in short form, such as #f00 for red.
//   - bg:#rrggbb sets the background color.
//
// Modifiers toggle text attributes:
//
//   - bold and nobold turn bold text on and off.
//   - italic and noitalic turn italic text on and off.
//   - underline and nounderline turn underlined text on and off.
//
// For Pygments compatibility, [Parse] accepts and ignores the noinherit and
// border:#rrggbb tokens.
//
// Example usage:
//
//	// A simple foreground color:
//	red, err := style.Parse("#ff0000")
//
//	// Bold text with a specific color:
//	keyword, err := style.Parse("bold #c678dd")
//
//	// Full specification with foreground and background:
//	full, err := style.Parse("#abb2bf bg:#282c34")
//
//	// For package-level variables, use MustParse:
//	var keywordStyle = style.MustParse("bold #c678dd")
//
//	// To convert a style back to a string:
//	s := style.Encode(keyword) // "bold #c678dd"
package style
