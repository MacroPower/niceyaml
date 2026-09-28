package theme

import (
	"cmp"
	"image/color"
	"maps"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/lucasb-eyer/go-colorful"

	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

// dimShift separates a dimmed variant from its source color.
const dimShift = 0.15

// surfaceShift lifts highlight and accent heading backgrounds off the base.
const surfaceShift = 0.30

// palette holds the colors [palette.styles] builds the full [style.Styles]
// of a catalog theme from. Every built-in theme except charm is one palette
// in [palettes], so a theme lists its colors rather than every kind.
type palette struct {
	// Tokens sets token kinds in the style-string form [style.Parse]
	// reads. Each spec layers over the style its kind inherits, so a spec
	// naming only attributes keeps the colors of the closest kind above
	// it. Kinds left out inherit from their parent, with two exceptions.
	// The inserted, deleted, and error marks take their colors from OK and
	// Error. [kind.LiteralNull] takes the [kind.LiteralBoolean] spec, since
	// the source themes color null, true, and false as one keyword
	// constant.
	Tokens map[kind.Kind]string
	// Fg and Bg are the base text colors as hex strings. An empty value
	// leaves the terminal default in place; the derived kinds then
	// assume black on white for a [Light] theme and white on black for a
	// [Dark] one.
	Fg, Bg string
	// Accent colors headings and accented text. OK, Warn, and Error color
	// the status kinds. OK and Error also color the inserted, deleted, and
	// error marks when Tokens leaves them out. A heading, and an error mark
	// Tokens leaves out, draws its text in Fg or Bg, whichever contrasts
	// more with the color behind it.
	Accent, OK, Warn, Error string
	// Mode is the background the theme targets. It also picks the
	// direction of the derived shifts. [kind.TextSubtle] and
	// [kind.TextSubtleDim] move toward the background, while highlights,
	// accent headings, and [kind.TextAccentDim] move away from it, so the
	// dimmed accent reads brighter than the accent on a [Dark] theme and
	// darker on a [Light] one. An accent that cannot move further from the
	// background, such as black on a [Light] theme, dims toward it instead.
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

	// Text drawn on a painted color takes whichever surface color reads
	// best there, so a light accent on a light theme takes the dark
	// foreground.
	textOn := func(c color.Color) color.Color {
		if contrast(fg, c) > contrast(bg, c) {
			return fg
		}

		return bg
	}

	heading := func(c color.Color) lipgloss.Style {
		return lipgloss.NewStyle().Foreground(textOn(c)).Background(c).Bold(true)
	}

	dimAccent := towardFg(accent, dimShift)
	if sameColor(dimAccent, accent) {
		dimAccent = towardBg(accent, dimShift)
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
		style.Set(kind.TextAccentDim, base.Foreground(dimAccent)),
		style.Set(kind.TextSubtle, base.Foreground(towardBg(fg, dimShift))),
		style.Set(kind.TextSubtleDim, base.Foreground(towardBg(fg, surfaceShift))),
		style.Set(kind.TextOK, base.Foreground(ok)),
		style.Set(kind.TextWarn, base.Foreground(warn)),
		style.Set(kind.TextError, base.Foreground(errColor)),
	}

	s := style.New(base, derived...)

	// The printer paints whole inserted and deleted lines and their + and -
	// markers in these kinds, and it marks errors with a GenericError
	// overlay that replaces the style underneath. A palette that leaves
	// them out takes its OK and Error colors, and the error mark draws as a
	// badge so it shows over a token drawn in the Error color. Each default
	// fills in for a missing Tokens spec and layers over Generic the same
	// way, so a theme spec for one of these kinds replaces the default
	// outright rather than layering over it.
	tokens := maps.Clone(p.Tokens)
	if tokens == nil {
		tokens = map[kind.Kind]string{}
	}

	defaults := map[kind.Kind]string{
		kind.GenericInserted: style.Encode(lipgloss.NewStyle().Foreground(ok)),
		kind.GenericDeleted:  style.Encode(lipgloss.NewStyle().Foreground(errColor)),
		kind.GenericError:    style.Encode(lipgloss.NewStyle().Foreground(textOn(errColor)).Background(errColor)),
	}
	// Null and the booleans share one keyword-constant token in the source
	// themes, so a palette that leaves null out draws it with the boolean
	// spec. The two kinds are siblings under Literal, so the spec resolves
	// to the same style for both.
	if spec, ok := tokens[kind.LiteralBoolean]; ok {
		defaults[kind.LiteralNull] = spec
	}

	for st, spec := range defaults {
		if _, ok := tokens[st]; !ok {
			tokens[st] = spec
		}
	}

	// A Tokens spec layers over the style its kind already resolves to, so
	// a spec of "bold" alone keeps the ancestor's colors. Parents come
	// first, so a child layers over the parent's finished style. The pass
	// visits UI even when Tokens leaves it out, since the chrome takes its
	// color from the finished comments.
	kinds := slices.Collect(maps.Keys(tokens))
	if _, ok := tokens[kind.UI]; !ok {
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

		if spec, ok := tokens[st]; ok {
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

// surface returns the foreground and background that [palette.styles]
// derives the other kinds from. These are the palette's own colors, or
// black and white arranged for the mode when the palette leaves one unset.
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

// sameColor reports whether a and b hold the same channel values.
func sameColor(a, b color.Color) bool {
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()

	return ar == br && ag == bg && ab == bb && aa == ba
}

// contrast returns the WCAG contrast ratio between a and b, from 1 for
// equal colors to 21 for black on white.
func contrast(a, b color.Color) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}

	return (la + 0.05) / (lb + 0.05)
}

// luminance returns the WCAG relative luminance of c.
func luminance(c color.Color) float64 {
	cf, _ := colorful.MakeColor(c)
	r, g, b := cf.LinearRgb()

	return 0.2126*r + 0.7152*g + 0.0722*b
}

// layer returns base with the colors and attributes the spec sets applied
// on top, leaving base's other properties in place. The spec is in the
// form [style.Parse] reads, and attribute keywords apply left to right as
// they do there, so the last keyword for an attribute wins. A "nobold",
// "noitalic", or "nounderline" keyword turns the attribute off even when
// base sets it, where [style.Parse] only leaves it unset.
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

	for word := range strings.FieldsSeq(strings.ToLower(spec)) {
		switch word {
		case "bold":
			base = base.Bold(true)
		case "nobold":
			base = base.Bold(false)
		case "italic":
			base = base.Italic(true)
		case "noitalic":
			base = base.Italic(false)
		case "underline":
			base = base.Underline(true)
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
