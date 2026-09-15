// Package theme provides a catalog of pre-built color themes for YAML syntax
// highlighting.
//
// Themes translate popular editor and terminal color schemes (Monokai, Dracula,
// Catppuccin, etc.) into [style.Styles] configurations.
//
// This allows applications to offer familiar, well-designed color palettes
// without manually defining token colors.
//
// # Using Themes
//
// Themes are looked up by kebab-case name. [Styles] returns a ready-to-use
// [style.Styles]:
//
//	if styles, ok := theme.Styles("dracula"); ok {
//		printer := niceyaml.NewPrinter(niceyaml.WithStyles(styles))
//	}
//
// Filter available themes by [Mode] with [List]:
//
//	darkThemes := theme.List(theme.Dark)   // ["monokai", "dracula", ...]
//	lightThemes := theme.List(theme.Light) // ["solarized-light", "catppuccin-latte", ...]
//
// [Get] and [All] return the [Theme] entries themselves, with the name and
// mode alongside the styles, for building a picker:
//
//	for _, t := range theme.All() {
//		fmt.Println(t.Name, t.Mode)
//	}
//
// # Custom Themes
//
// Applications can register custom themes at runtime with [Register]:
//
//	theme.Register("my-theme", func() style.Styles {
//		return style.NewStyles(lipgloss.NewStyle() /* , style.Set(...) */)
//	}, theme.Dark)
//
// Registered themes become available through [Styles] and [List] alongside
// built-in themes.
//
// # Theme Structure
//
// Themes define colors for YAML token categories: keys ([style.NameTag]),
// strings ([style.LiteralString]), numbers ([style.LiteralNumber]), comments
// ([style.Comment]), and so on.
//
// The [style] package's inheritance system means themes only need to specify
// the categories they want to customize; undefined categories fall back to
// their parent style.
//
// Each built-in theme is a small palette: a base foreground and background,
// an accent color, OK, warning, and error colors, and the token categories it
// colors. The package derives the remaining categories, such as headings,
// highlights, and dimmed text, from those colors, so every theme presents the
// same set of categories.
//
// Most themes in this package are derived from the Chroma syntax highlighter:
// https://github.com/alecthomas/chroma
package theme
