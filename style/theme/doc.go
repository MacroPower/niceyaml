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
// [Get] looks a [Theme] up by its kebab-case name, and [Theme.Styles] returns
// a ready-to-use [style.Styles]. A theme builds its styles on the first call
// and returns the same value afterwards:
//
//	if t, ok := theme.Get("dracula"); ok {
//		p := printer.New(printer.WithStyles(t.Styles()))
//	}
//
// [All] returns every theme, built-in ones first in alphabetical order, with
// the name and [Mode] alongside the styles for building a picker. Filter by
// [Theme.Mode] to list the themes for one background:
//
//	for _, t := range theme.All() {
//		if t.Mode == theme.Dark {
//			fmt.Println(t.Name)
//		}
//	}
//
// # Custom Themes
//
// Applications create custom themes with [New] and add them to the registry
// with [Register]. Register returns [ErrRegistered] when a theme with the
// same name already exists, so a custom theme cannot replace a built-in one:
//
//	custom := theme.New("my-theme", theme.Dark, func() style.Styles {
//		return style.NewStyles(lipgloss.NewStyle() /* , style.Set(...) */)
//	})
//	if err := theme.Register(custom); err != nil {
//		// The name is taken.
//	}
//
// Registered themes become available through [Get] and [All] alongside
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
