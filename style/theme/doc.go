// Package theme provides a catalog of pre-built color themes for YAML syntax
// highlighting, and builds custom themes from a few colors.
//
// Themes translate popular editor and terminal color schemes (Monokai, Dracula,
// Catppuccin, etc.) into [style.Styles] configurations.
//
// Applications offer familiar color palettes through them without defining
// token colors by hand.
//
// # Usage
//
// [Builtin] returns the [Catalog] of every theme this package ships.
// [Catalog.Get] looks a [Theme] up by its kebab-case name. A Theme resolves
// each [kind.Kind] through [Theme.Style], so it goes to
// [go.jacobcolvin.com/niceyaml/printer.WithStyles] as it is. A theme builds
// its styles on the first call and returns the same value afterwards:
//
//	if t, ok := theme.Builtin().Get("dracula"); ok {
//		p := printer.New(printer.WithStyles(t), printer.WithBox())
//	}
//
// Themes with a background paint it behind each token, so without
// [go.jacobcolvin.com/niceyaml/printer.WithBox] that color ends where the
// content of each row ends. The box fills the rest of the rectangle around
// the rows with it. The Surfaces section covers how to draw a theme on the
// terminal's own background instead.
//
// [Charm] paints the CharmTone palette of [style.Default] on a Pepper
// surface. It is a Theme so a program can name it without a lookup:
//
//	p := printer.New(printer.WithStyles(theme.Charm), printer.WithBox())
//
// [Theme.Styles] returns the [style.Styles] behind a theme, for a program
// that overrides some of its kinds with [style.Styles.With]:
//
//	styles := theme.Charm.Styles().With(style.Set(kind.Comment, dim))
//	p := printer.New(printer.WithStyles(styles), printer.WithBox())
//
// A program that names a kind of its own, such as one for an overlay, gives
// it a parent with [style.Inherit], and it then takes the parent's style
// under every theme in the catalog:
//
//	styles := t.Styles().With(style.Inherit(match, kind.GenericHighlight))
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
// [FromColors] creates a theme from a [Colors] value and a [Token] for
// each kind it colors. Each Token holds a spec in the form that
// [style.Parse] reads. [Derive] builds the styles of such a theme. It
// derives the headings, the highlights, the dimmed text, the diff lines,
// and the marks of errors and warnings from the colors. A theme from
// FromColors thus presents every kind that a built-in theme does. For
// example:
//
//	corp := theme.FromColors("corp", theme.Colors{
//		Fg:     lipgloss.Color("#e6e6e6"),
//		Bg:     lipgloss.Color("#101820"),
//		Accent: lipgloss.Color("#ff6f00"),
//		Mode:   theme.Dark,
//	}, map[kind.Kind]theme.Token{
//		kind.Comment:       "italic #7a8590",
//		kind.NameTag:       "bold #ff6f00",
//		kind.LiteralString: "#9ccc65",
//	})
//
// [Catalog.With] adds themes to a catalog. It returns a new Catalog and
// leaves the receiver as it was. A program adds its theme to the built-in
// catalog:
//
//	catalog := theme.Builtin().With(corp)
//
// A theme with the name of one the catalog holds replaces it, so a program
// can shadow a built-in theme. A Catalog never changes after construction,
// so a program builds one at startup and shares it with every picker that
// needs it.
//
// [New] creates a theme from a function that builds its [style.Styles] by
// hand:
//
//	custom := theme.New("my-theme", theme.Dark, func() style.Styles {
//		return style.New(lipgloss.NewStyle() /* , style.Set(...) */)
//	})
//
// Such a theme styles only the kinds it sets. Every other kind takes the
// style of its closest set ancestor, which is often the body text. A theme
// built with New should set each of the kinds that a renderer paints
// besides the token kinds, so that its marks stand apart from the text:
//
//   - [kind.GenericInserted] and [kind.GenericDeleted] for the lines of a
//     diff.
//   - [kind.GenericError] and [kind.GenericWarn] for the locations of
//     errors and warnings.
//   - [kind.TextError] and [kind.TextWarn] for the messages of errors and
//     warnings.
//   - [kind.GenericHighlight] and [kind.GenericHighlightDim] for search
//     matches and selections.
//   - [kind.UI] for gutters, annotations, and hunk headers, which
//     otherwise take the whole comment style.
//
// # Theme Structure
//
// Themes define colors for YAML token kinds: keys ([kind.NameTag]), strings
// ([kind.LiteralString]), numbers ([kind.LiteralNumber]), comments
// ([kind.Comment]), and so on.
//
// Through the inheritance of the [style] package, a theme specifies only
// the kinds it customizes, and every other kind falls back to the style of
// its parent.
//
// Every built-in theme other than [Charm] comes from [FromColors]. Each of
// these themes lists a base foreground and background, an accent color,
// OK, warning, and error colors, and the token kinds it colors. [Derive]
// derives the remaining kinds from those colors, so every built-in theme
// presents the same set of kinds.
//
// # Surfaces
//
// The Bg of a [Colors] value becomes the surface of the theme that
// [FromColors] builds from it, as [style.WithSurface] sets it. Every kind
// without a fill of its own draws on that color, and
// [go.jacobcolvin.com/niceyaml/printer.WithBox] fills the rest of the
// rectangle around the rows with it. A few themes, such
// as pygments, have no background and draw on the terminal's own.
//
// To draw any theme on the terminal's background, remove its surface:
//
//	styles := t.Styles().With(style.WithSurface(nil))
//
// Kinds with a fill of their own, such as the highlights, the headings,
// and the error marks, keep it. [Derive] picks the derived colors of
// each theme to keep their contrast on its surface. Without the surface,
// those colors meet the terminal's background instead, and some of them
// might be hard to read there.
//
// Most themes in this package derive from the Chroma syntax highlighter:
// https://github.com/alecthomas/chroma
package theme
