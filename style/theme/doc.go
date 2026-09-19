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
// [Builtin] returns the [Catalog] of every theme this package ships.
// [Catalog.Get] looks a [Theme] up by its kebab-case name. A Theme resolves
// each [kind.Kind] through [Theme.Style], so it goes to
// [go.jacobcolvin.com/niceyaml/printer.WithStyles] as it is. A theme builds
// its styles on the first call and returns the same value afterwards:
//
//	if t, ok := theme.Builtin().Get("dracula"); ok {
//		p := printer.New(printer.WithStyles(t))
//	}
//
// [Charm] is the theme [style.Default] renders with, held as a Theme so a
// program can name it without a lookup:
//
//	p := printer.New(printer.WithStyles(theme.Charm))
//
// [Theme.Styles] returns the [style.Styles] behind a theme, for a program
// that overrides some of its kinds with [style.Styles.With]:
//
//	styles := theme.Charm.Styles().With(style.Set(kind.Comment, dim))
//	p := printer.New(printer.WithStyles(styles))
//
// [Catalog.All] returns every theme in the catalog, with the name and
// [Mode] alongside the styles for building a picker, and [Catalog.Mode]
// keeps the themes for one background:
//
//	for _, t := range theme.Builtin().Mode(theme.Dark).All() {
//		fmt.Println(t.Name)
//	}
//
// # Custom Themes
//
// Applications create custom themes with [New] and add them to a catalog
// with [Catalog.With], which returns a new Catalog and leaves the receiver
// as it was. A theme with the name of one the catalog holds replaces it,
// so a program can shadow a built-in theme:
//
//	custom := theme.New("my-theme", theme.Dark, func() style.Styles {
//		return style.NewStyles(lipgloss.NewStyle() /* , style.Set(...) */)
//	})
//	catalog := theme.Builtin().With(custom)
//
// A Catalog never changes after it is built, so a program builds one at
// startup and shares it with every picker that needs it.
//
// # Theme Structure
//
// Themes define colors for YAML token kinds: keys ([kind.NameTag]), strings
// ([kind.LiteralString]), numbers ([kind.LiteralNumber]), comments
// ([kind.Comment]), and so on.
//
// The [style] package's inheritance system means themes only need to specify
// the kinds they want to customize; undefined kinds fall back to
// their parent style.
//
// Each built-in theme is a small palette: a base foreground and background,
// an accent color, OK, warning, and error colors, and the token kinds it
// colors. The package derives the remaining categories, such as headings,
// highlights, and dimmed text, from those colors, so every theme presents the
// same set of categories.
//
// Most themes in this package are derived from the Chroma syntax highlighter:
// https://github.com/alecthomas/chroma
package theme
