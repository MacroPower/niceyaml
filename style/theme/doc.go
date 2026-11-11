// Package theme provides a catalog of pre-built color themes for YAML syntax
// highlighting, and builds custom themes from a few colors.
//
// Themes translate editor and terminal color schemes, such as Monokai,
// Dracula, and Catppuccin, into [style.Styles] configurations.
//
// Applications offer familiar color palettes through them without defining
// token colors by hand.
//
// # Usage
//
// [Builtin] returns the [Catalog] of every theme this package ships.
// [Catalog.Get] finds a [Theme] by its kebab-case name. A Theme resolves
// each [kind.Kind] through [Theme.Style], so it goes to
// [go.jacobcolvin.com/niceyaml/printer.WithStyles] as it is. A theme builds
// its styles on the first call to [Theme.Styles] or [Theme.Style] and
// returns the same value afterwards:
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
// [Charm] paints the dark CharmTone palette of [style.Default] on a Pepper
// surface, and [CharmLight] paints the light palette on a Salt surface.
// Each is a Theme so a program can name it without a lookup:
//
//	p := printer.New(printer.WithStyles(theme.Charm), printer.WithBox())
//
// To print on the terminal's own background, a program passes
// [style.Default] the background it detects in place of either theme.
//
// [Theme.Styles] returns the [style.Styles] behind a theme, for a program
// that overrides some of its kinds with [style.Styles.With]:
//
//	styles := theme.Charm.Styles().With(style.Set(kind.Comment, dim))
//	p := printer.New(printer.WithStyles(styles), printer.WithBox())
//
// [Theme.With] applies the same options and returns a Theme with the same
// name and mode, so the result goes into a [Catalog]. [style.Update]
// adjusts a kind relative to the style the theme gives it, where
// [style.Set] replaces that style. One Update thus suits every theme in a
// catalog:
//
//	italic := style.Update(kind.Comment, func(s lipgloss.Style) lipgloss.Style {
//		return s.Italic(true)
//	})
//
//	if t, ok := theme.Builtin().Get("dracula"); ok {
//		catalog := theme.Builtin().With(t.With(italic))
//	}
//
// A program that names a kind of its own, such as one for an overlay, gives
// it a parent with [style.Inherit]. The kind then takes the parent's style
// under every theme in the catalog:
//
//	styles := t.Styles().With(style.Inherit(match, kind.GenericHighlight))
//
// [Catalog.All] returns every theme in the catalog, with the name and
// [Mode] alongside the styles for building a picker. [Catalog.Mode] keeps
// the themes for one background:
//
//	for _, t := range theme.Builtin().Mode(theme.Dark).All() {
//		fmt.Println(t.Name)
//	}
//
// A Mode prints as "light" or "dark" and reads the same names back, so a
// program takes one from a flag or a theme file:
//
//	var mode theme.Mode
//	flag.TextVar(&mode, "mode", theme.Dark, "light or dark")
//
// # Custom Themes
//
// [FromColors] creates a theme from a [Colors] value and a [Token] for
// each kind it colors. Each Token holds a spec in the form that
// [style.Parse] reads. [Derive] builds the styles of such a theme. It
// derives the headings, the highlights, the dimmed text, the diff lines,
// and the marks of errors and warnings from the colors. A theme from
// FromColors thus presents every kind that a built-in theme does.
//
// FromColors returns an error for an unset mode and for a token that
// style.Parse rejects. [MustFromColors] panics instead, for a theme that a
// program spells out in its source:
//
//	corp := theme.MustFromColors("corp", theme.Colors{
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
// style of its closest set ancestor, which is often the body text. To keep
// the marks of a theme built with New apart from the text, a program
// should set each kind that a renderer paints besides the token kinds:
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
// # Theme Files
//
// A [Palette] holds a whole theme as plain values that decode from text. A
// program that reads a theme from a YAML or JSON file decodes the file
// into a Palette and calls [Palette.Theme]:
//
//	pal, err := source.Decode[theme.Palette](ctx,
//		niceyaml.WithUnknownFields(niceyaml.UnknownFieldsReject))
//	if err != nil {
//		return err
//	}
//
//	corp, err := pal.Theme()
//
// A [Mode], a [Hex], and a [Token] each reject bad text when they decode.
// A niceyaml decode thus reports a mistyped mode, color, or token where
// the file holds it, without a validator. A Palette also decodes as a
// field of a larger config type, and the path of each error then starts
// at that field.
//
// Theme reports what a decode cannot. It rejects an empty name, an unset
// mode, and a token for a kind that [kind.IsPredefined] rejects, such as a
// misspelled name. These errors name the field or the kind and not a
// place in the file. A program that wants a place for one of them adds a
// validator for it to the decode.
//
// A decode does not report an unquoted color. YAML reads the color in
// bg: #2b1d14 as a comment, so the key holds null, and a null leaves the
// field unset. To report such a key, a program checks each color node
// that the file holds, for example with
// [go.jacobcolvin.com/niceyaml.Each].
//
// A Hex names an RGB color, so a Palette cannot hold a color of the
// terminal's own palette. A program that wants one sets it in a [Colors]
// value and calls FromColors. A program whose theme file styles kinds of
// its own also calls FromColors, with the colors that [Palette.Colors]
// returns.
//
// # Theme Structure
//
// Themes define colors for YAML token kinds, such as keys
// ([kind.NameTag]), strings ([kind.LiteralString]), numbers
// ([kind.LiteralNumber]), and comments ([kind.Comment]).
//
// Through the inheritance of the [style] package, a theme specifies only
// the kinds it customizes, and every other kind takes the style of its
// parent.
//
// Every built-in theme other than [Charm] and [CharmLight] comes from a
// [Palette]. Each of these themes lists a base foreground and background,
// an accent color, OK, warning, and error colors, and the token kinds it
// colors. [Derive] derives the remaining kinds from those colors, so
// every built-in theme presents the same set of kinds.
//
// # Surfaces
//
// The Bg of a [Colors] value becomes the surface of the theme that
// [FromColors] builds from it, as [style.WithSurface] sets it. Every kind
// without a fill of its own draws on that color, and
// [go.jacobcolvin.com/niceyaml/printer.WithBox] fills the rest of the
// rectangle around the rows with it. A few themes, such as pygments, do
// not have a background and draw on the terminal's own.
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
// A highlight that the printer blends, such as a search match from
// [go.jacobcolvin.com/niceyaml/line.View.BlendOverlay], mixes its fill
// with the surface beneath it. On the surface, it thus shows a softer
// color than its fill. Without the surface, nothing lies beneath the fill,
// and the match shows the fill unmixed, as a highlight that replaces the
// style does.
//
// Under the 16-color profile, [github.com/charmbracelet/colorprofile.ANSI],
// the writer maps each color to one of the 16 basic ANSI colors through a
// fixed table. The terminal then draws each of those from its own palette.
// Many dark surfaces become ANSI black or blue, and a token color near the
// surface can become the same color and vanish. Nord's surface and its
// keys both become cyan, for example. A program that can print under that
// profile removes the surface there:
//
//	styles := t.Styles()
//	if profile == colorprofile.ANSI {
//		styles = styles.With(style.WithSurface(nil))
//	}
//
// The profile comes from the writer that prints, such as the Profile field
// of [lipgloss.Writer], or from a [charm.land/bubbletea/v2.ColorProfileMsg]
// in a Bubble Tea program.
//
// Under the 16-color profile, the two highlights of some themes, such as
// nord and solarized-dark, become one color. The current search match then
// looks like the others. Under the 256-color profile,
// [github.com/charmbracelet/colorprofile.ANSI256], the text of each
// built-in theme keeps a color apart from its surface. The dim highlight
// of some dark themes, such as monokai and gruvbox, becomes the color of
// the surface there. So does the background of [kind.GenericHeadingSubtle].
//
// Most themes in this package derive from the Chroma syntax highlighter:
// https://github.com/alecthomas/chroma
package theme
