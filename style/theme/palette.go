package theme

import (
	"cmp"
	"image/color"
	"maps"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

// dimShift separates a dimmed variant from its source color.
const dimShift = 0.15

// surfaceShift lifts highlight and accent heading backgrounds off the base.
const surfaceShift = 0.30

// palette holds the colors a catalog theme is built from. Every built-in
// theme except charm is one palette in [palettes], and [palette.styles]
// derives the full [style.Styles] from it, so a theme lists its colors
// rather than every kind.
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
	// Mode is the background the theme is designed for. It also picks the
	// direction of the derived shifts. [kind.TextSubtle] and
	// [kind.TextSubtleDim] move toward the background, while highlights,
	// accent headings, and [kind.TextAccentDim] move away from it, so the
	// dimmed accent reads brighter than the accent on a [Dark] theme and
	// darker on a [Light] one.
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
	derived := []style.Option{
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

	s := style.New(base, derived...)

	// A Tokens spec layers over the style its kind already resolves to, so
	// a spec of "bold" alone keeps the ancestor's colors. Parents come
	// first, so a child layers over the parent's finished style. The pass
	// visits UI even when Tokens leaves it out, since the chrome takes its
	// color from the finished comments.
	kinds := slices.Collect(maps.Keys(p.Tokens))
	if _, ok := p.Tokens[kind.UI]; !ok {
		kinds = append(kinds, kind.UI)
	}

	for _, st := range byDepth(kinds) {
		cur := s.Style(st)
		if st == kind.UI {
			// The chrome takes the comment color alone. A Tokens spec that
			// sets comments in italics or bold styles the comments, and a
			// line number drawn the same way would read as one. UI comes
			// after Comment and ahead of its own children in the depth
			// order, so a UI spec layers over this style, and a spec for a
			// child layers over UI rather than over the comments.
			cur = base.Foreground(s.Style(kind.Comment).GetForeground())
		}

		if spec, ok := p.Tokens[st]; ok {
			cur = layer(cur, spec)
		}

		s = s.With(style.Set(st, cur))
	}

	return s
}

// byDepth returns kinds ordered by their distance from [kind.Text],
// parents ahead of their children. Kinds an equal distance out never
// inherit from one another, so name orders those.
func byDepth(kinds []kind.Kind) []kind.Kind {
	return slices.SortedFunc(slices.Values(kinds), func(a, b kind.Kind) int {
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

// layer returns base with the colors and attributes the spec sets applied
// on top, leaving base's other properties in place. The spec is in the
// form [style.Parse] reads. A "nobold", "noitalic", or "nounderline"
// keyword turns the attribute off, since [style.Parse] leaves it unset,
// which reads the same as a spec that never named it.
//
//nolint:gocritic // Value semantics match lipgloss.
func layer(base lipgloss.Style, spec string) lipgloss.Style {
	over := style.MustParse(spec)

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

	for word := range strings.FieldsSeq(strings.ToLower(spec)) {
		switch word {
		case "nobold":
			base = base.Bold(false)
		case "noitalic":
			base = base.Italic(false)
		case "nounderline":
			base = base.Underline(false)
		}
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
