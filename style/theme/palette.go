package theme

import (
	"image/color"

	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// dimShift separates a dimmed variant from its source color.
const dimShift = 0.15

// surfaceShift lifts highlight and accent heading backgrounds off the base.
const surfaceShift = 0.30

// palette holds the colors a catalog theme is built from. Every built-in
// theme is one palette in [catalog], and [palette.styles] derives the full
// [style.Styles] from it, so a theme lists its colors rather than every
// category.
type palette struct {
	// Tokens sets token categories in the style-string form [style.Parse]
	// reads, layered over the base style. Categories left out inherit from
	// their parent.
	Tokens map[style.Style]string
	// Fg and Bg are the base text colors as hex strings. An empty value
	// leaves the terminal default in place; the derived categories then
	// assume black on white for a [Light] theme and white on black for a
	// [Dark] one.
	Fg, Bg string
	// Accent colors headings and accented text. OK, Warn, and Error color
	// the status categories.
	Accent, OK, Warn, Error string
	// Overrides is applied after every derived category, for the few
	// categories a theme sets outside the template.
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

	opts := make([]style.StylesOption, 0, len(p.Tokens)+len(p.Overrides)+15)

	for st, spec := range p.Tokens {
		opts = append(opts, style.Set(st, layer(base, style.MustParse(spec))))
	}

	opts = append(opts,
		style.Set(style.GenericHeading, heading(accent)),
		style.Set(style.GenericHeadingAccent,
			base.Background(towardFg(bg, surfaceShift)).Foreground(towardFg(fg, dimShift)),
		),
		style.Set(style.GenericHeadingSubtle, base.Background(towardFg(bg, dimShift))),
		style.Set(style.GenericHeadingOK, heading(ok)),
		style.Set(style.GenericHeadingWarn, heading(warn)),
		style.Set(style.GenericHeadingError, heading(errColor)),
		style.Set(style.GenericHighlight, lipgloss.NewStyle().Background(towardFg(bg, surfaceShift))),
		style.Set(style.GenericHighlightDim, lipgloss.NewStyle().Background(towardFg(bg, dimShift))),
		style.Set(style.TextAccent, base.Foreground(accent)),
		style.Set(style.TextAccentDim, base.Foreground(towardFg(accent, dimShift))),
		style.Set(style.TextSubtle, base.Foreground(fg)),
		style.Set(style.TextSubtleDim, base.Foreground(towardBg(fg, dimShift))),
		style.Set(style.TextOK, base.Foreground(ok)),
		style.Set(style.TextWarn, base.Foreground(warn)),
		style.Set(style.TextError, base.Foreground(errColor)),
	)

	opts = append(opts, p.Overrides...)

	return style.NewStyles(base, opts...)
}

// surface returns the foreground and background the derived categories are
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
