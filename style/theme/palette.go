package theme

import (
	"cmp"
	"image/color"
	"maps"
	"slices"

	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

// dimShift separates a dimmed variant from its source color.
const dimShift = 0.15

// surfaceShift lifts highlight and accent heading backgrounds off the base.
const surfaceShift = 0.30

// palette holds the colors a catalog theme is built from. Every built-in
// theme is one palette in [palettes], and [palette.styles] derives the full
// [style.Styles] from it, so a theme lists its colors rather than every
// kind.
type palette struct {
	// Tokens sets token kinds in the style-string form [style.Parse]
	// reads. Each spec layers over the style its kind inherits, so a spec
	// naming only attributes keeps the colors of the closest kind above
	// it. Kinds left out inherit from their parent.
	Tokens map[kind.Kind]string
	// Fg and Bg are the base text colors as hex strings. An empty value
	// leaves the terminal default in place; the derived kinds then
	// assume black on white for a [Light] theme and white on black for a
	// [Dark] one.
	Fg, Bg string
	// Accent colors headings and accented text. OK, Warn, and Error color
	// the status kinds.
	Accent, OK, Warn, Error string
	// Overrides is applied last, after the derived kinds and Tokens, for
	// the few kinds a theme sets outside the template. An override
	// replaces a kind's style rather than layering over it.
	Overrides []style.StylesOption
	// Mode is the background the theme is designed for. It also picks the
	// direction of the derived shifts, so dimmed text moves toward the
	// background and highlights move away from it.
	Mode Mode
}

// styles builds the [style.Styles] for the palette.
func (p palette) styles() style.Styles {
	base := lipgloss.NewStyle()
	if p.Fg != "" {
		base = base.Foreground(lipgloss.Color(p.Fg))
	}

	if p.Bg != "" {
		base = base.Background(lipgloss.Color(p.Bg))
	}

	fg, bg := p.surface()
	accent := lipgloss.Color(p.Accent)
	ok := lipgloss.Color(p.OK)
	warn := lipgloss.Color(p.Warn)
	errColor := lipgloss.Color(p.Error)

	towardFg, towardBg := lipgloss.Lighten, lipgloss.Darken
	if p.Mode == Light {
		towardFg, towardBg = lipgloss.Darken, lipgloss.Lighten
	}

	heading := func(c color.Color) lipgloss.Style {
		return lipgloss.NewStyle().Foreground(bg).Background(c).Bold(true)
	}

	// The derived kinds resolve first, so a Tokens entry naming one of them
	// layers over the derived value rather than the other way around.
	derived := []style.StylesOption{
		style.Set(kind.GenericHeading, heading(accent)),
		style.Set(kind.GenericHeadingAccent,
			base.Background(towardFg(bg, surfaceShift)).Foreground(towardFg(fg, dimShift)),
		),
		style.Set(kind.GenericHeadingSubtle, base.Background(towardFg(bg, dimShift)).Foreground(fg)),
		style.Set(kind.GenericHeadingOK, heading(ok)),
		style.Set(kind.GenericHeadingWarn, heading(warn)),
		style.Set(kind.GenericHeadingError, heading(errColor)),
		style.Set(kind.GenericHighlight, lipgloss.NewStyle().Background(towardFg(bg, surfaceShift))),
		style.Set(kind.GenericHighlightDim, lipgloss.NewStyle().Background(towardFg(bg, dimShift))),
		style.Set(kind.TextAccent, base.Foreground(accent)),
		style.Set(kind.TextAccentDim, base.Foreground(towardFg(accent, dimShift))),
		style.Set(kind.TextSubtle, base.Foreground(towardBg(fg, dimShift))),
		style.Set(kind.TextSubtleDim, base.Foreground(towardBg(fg, surfaceShift))),
		style.Set(kind.TextOK, base.Foreground(ok)),
		style.Set(kind.TextWarn, base.Foreground(warn)),
		style.Set(kind.TextError, base.Foreground(errColor)),
	}

	s := style.NewStyles(base, derived...)

	// A Tokens spec layers over the style its kind already resolves to, so
	// a spec of "bold" alone keeps the ancestor's colors. Parents come
	// first, so a child layers over the parent's finished style.
	for _, st := range byDepth(p.Tokens) {
		s = s.With(style.Set(st, layer(s.Style(st), style.MustParse(p.Tokens[st]))))
	}

	return s.With(p.Overrides...)
}

// byDepth returns the kinds of tokens ordered by their distance from
// [kind.Text], parents ahead of their children. Kinds an equal distance
// out never inherit from one another, so name orders those.
func byDepth(tokens map[kind.Kind]string) []kind.Kind {
	return slices.SortedFunc(maps.Keys(tokens), func(a, b kind.Kind) int {
		if d := cmp.Compare(depth(a), depth(b)); d != 0 {
			return d
		}

		return cmp.Compare(a, b)
	})
}

// depth returns how many steps separate st from [kind.Text], the root of
// the hierarchy. A custom kind counts as one step, since [kind.Parent]
// reports [kind.Text] for it.
func depth(st kind.Kind) int {
	n := 0
	for current := st; current != kind.Text; current = kind.Parent(current) {
		n++
	}

	return n
}

// surface returns the foreground and background the derived kinds are
// computed from: the palette's own colors, or black and white arranged for
// the mode when the palette leaves one unset.
func (p palette) surface() (color.Color, color.Color) {
	fg, bg := "#000000", "#ffffff"
	if p.Mode == Dark {
		fg, bg = bg, fg
	}

	if p.Fg != "" {
		fg = p.Fg
	}

	if p.Bg != "" {
		bg = p.Bg
	}

	return lipgloss.Color(fg), lipgloss.Color(bg)
}

// layer returns base with the colors and attributes set in over applied on
// top, leaving base's other properties in place.
//
//nolint:gocritic // Value semantics match lipgloss.
func layer(base, over lipgloss.Style) lipgloss.Style {
	if c := over.GetForeground(); isColorSet(c) {
		base = base.Foreground(c)
	}

	if c := over.GetBackground(); isColorSet(c) {
		base = base.Background(c)
	}

	if over.GetBold() {
		base = base.Bold(true)
	}

	if over.GetItalic() {
		base = base.Italic(true)
	}

	if over.GetUnderline() {
		base = base.Underline(true)
	}

	return base
}

// isColorSet reports whether c names a color rather than the absence of one.
func isColorSet(c color.Color) bool {
	if c == nil {
		return false
	}

	_, none := c.(lipgloss.NoColor)

	return !none
}
