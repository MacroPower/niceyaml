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
// Each theme is exported as a function (e.g., [Monokai], [Dracula]) that
// returns a ready-to-use [style.Styles]:
//
//	styles := theme.Monokai()
//	printer := niceyaml.NewPrinter(niceyaml.WithStyles(styles))
//
// For user-selectable themes, look up by name with [Styles]:
//
//	if styles, ok := theme.Styles("dracula"); ok {
//		// Use styles
//	}
//
// Filter available themes by [style.Mode] with [List]:
//
//	darkThemes := theme.List(style.Dark)   // ["monokai", "dracula", ...]
//	lightThemes := theme.List(style.Light) // ["solarized-light", "catppuccin-latte", ...]
//
// # Custom Themes
//
// Applications can register custom themes at runtime with [Register]:
//
//	theme.Register("my-theme", func() style.Styles {
//		return style.Styles{ /* ... */ }
//	}, style.Dark)
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
// Most themes in this package are derived from the Chroma syntax highlighter:
// https://github.com/alecthomas/chroma
package theme
