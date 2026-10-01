package theme

import (
	"cmp"
	"image/color"
	"maps"
	"math"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/lucasb-eyer/go-colorful"

	"go.jacobcolvin.com/niceyaml/internal/colors"
	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

// dimShift separates a dimmed variant from its source color.
const dimShift = 0.15

// subtleDimShift moves [kind.TextSubtleDim] further toward the background
// than dimShift moves [kind.TextSubtle].
const subtleDimShift = 0.30

// minTextContrast is the WCAG AA contrast ratio for body text. The body
// text keeps at least this ratio on a highlight, unless its own ratio on
// the base background is below maxContrastLoss times this one. The text
// then keeps at least its own ratio divided by maxContrastLoss.
const minTextContrast = 4.5

// maxContrastLoss is the most that a highlight may divide the contrast of
// the comments by. It bounds the body text the same way only where the
// body's own contrast is below minTextContrast times maxContrastLoss,
// since body text above that need only keep minTextContrast.
const maxContrastLoss = 1.25

// blendDistance and paintDistance are the CIEDE2000 distances, on
// go-colorful's scale of 0 to about 1, that [highlightSurfaces] puts
// between the base background and the dim highlight, and between the dim
// highlight and the highlight. The first measures the highlights as the
// printer blends them into the base background, and the second measures
// them as it paints them over it. Both sit well above 0.05, about the
// least distance at which a reader tells two colors apart, so the
// highlights show at a glance in either overlay mode.
const (
	blendDistance = 0.08
	paintDistance = 0.135
)

// palette holds the colors [palette.styles] builds the full [style.Styles]
// of a catalog theme from. Every built-in theme except charm is one palette
// in [palettes], so a theme lists its colors rather than every kind.
type palette struct {
	// Tokens sets token kinds in the style-string form [style.Parse]
	// reads. Each spec layers over the style its kind inherits, so a spec
	// naming only attributes keeps the colors of the closest kind above
	// it. Kinds left out inherit from their parent, with four exceptions.
	// The headings, the highlights, and the accent, subtle, and status text
	// take the styles the palette derives for them, even when Tokens sets
	// a kind above them, and a spec for one of them layers over the
	// derived style. The inserted, deleted, and error marks take their
	// colors from OK and Error. [kind.LiteralNull] takes the
	// [kind.LiteralBoolean] spec, since the source themes color null, true,
	// and false as one keyword constant. [kind.UI] takes only the
	// foreground of the finished [kind.Comment] style, so the chrome does
	// not pick up the italics or bold of the comments. A UI spec layers
	// over that color, and the UI children inherit from UI as usual.
	Tokens map[kind.Kind]string
	// Fg and Bg are the base text colors as hex strings. An empty value
	// leaves the terminal default in place; the derived kinds then
	// assume black on white for a [Light] theme and white on black for a
	// [Dark] one.
	Fg, Bg string
	// Accent colors headings and accented text, and highlights take its
	// hue, or the nearest hue that lets them stand apart. OK, Warn, and
	// Error color the status kinds. OK and Error also color the inserted,
	// deleted, and error marks when Tokens leaves them out. A heading, and
	// an error mark Tokens leaves out, draws its text in Fg or Bg,
	// whichever contrasts more with the color behind it.
	Accent, OK, Warn, Error string
	// Mode is the background the theme targets. It also picks the
	// direction of the derived shifts. [kind.TextSubtle] and
	// [kind.TextSubtleDim] move toward the background, while
	// [kind.TextAccentDim] moves away from it, so the dimmed accent reads
	// brighter than the accent on a [Dark] theme and darker on a [Light]
	// one. An accent that cannot move further from the background, such as
	// black on a [Light] theme, dims toward it instead. Highlights and the
	// accent and subtle headings tint the background, and they step its
	// luminance toward that of the text only where the tint needs room.
	Mode Mode
}

// styles builds the [style.Styles] for the palette.
func (p palette) styles() style.Styles {
	fg, bg := p.surface()

	// The search for the highlight surfaces holds the finished token
	// styles to contrast floors, so a first build with bg in place of the
	// surfaces supplies those styles.
	dim, surface := highlightSurfaces(p.build(bg, bg), fg, bg, lipgloss.Color(p.Accent), p.Mode)

	return p.build(dim, surface)
}

// build builds the [style.Styles] for the palette, with dimSurface and
// surface as the backgrounds of the highlights and of the subtle and
// accent headings.
func (p palette) build(dimSurface, surface color.Color) style.Styles {
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
		style.Set(kind.GenericHeadingAccent, base.Background(surface).Foreground(towardFg(fg, dimShift))),
		style.Set(kind.GenericHeadingSubtle, base.Background(dimSurface).Foreground(fg)),
		style.Set(kind.GenericHeadingOK, heading(ok)),
		style.Set(kind.GenericHeadingWarn, heading(warn)),
		style.Set(kind.GenericHeadingError, heading(errColor)),
		style.Set(kind.GenericHighlight, lipgloss.NewStyle().Background(surface)),
		style.Set(kind.GenericHighlightDim, lipgloss.NewStyle().Background(dimSurface)),
		style.Set(kind.TextAccent, base.Foreground(accent)),
		style.Set(kind.TextAccentDim, base.Foreground(dimAccent)),
		style.Set(kind.TextSubtle, base.Foreground(towardBg(fg, dimShift))),
		style.Set(kind.TextSubtleDim, base.Foreground(towardBg(fg, subtleDimShift))),
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

// highlightSurfaces returns the backgrounds of [kind.GenericHighlightDim]
// and [kind.GenericHighlight] for a theme that draws fg on bg. The styles
// s are the theme's own, built with bg in place of the highlights.
//
// The dim surface tints bg toward a hue, and the other surface tints the
// dim one further. A surface at the luminance of bg keeps the contrast of
// a color drawn on bg, so the surfaces keep that luminance where the gamut
// leaves the tint room. Near black or white it leaves little, so the
// surfaces also step toward the luminance of fg.
//
// Each surface takes the smallest step, and the least tint at that step,
// that puts it blendDistance from the color beneath it once the printer
// blends both into bg, and paintDistance from it once the printer paints
// both. The search stops before fg drops below minTextContrast on a
// surface, or below its own contrast with bg divided by maxContrastLoss
// where that is lower, and before the comments drop below their own
// contrast with bg divided by maxContrastLoss.
//
// The search tries the hues that [hues] returns for accent, in order, and
// takes the first that reaches both distances within these floors. The
// gamut holds less chroma for some hues than for others at a given
// luminance, such as blue near white, so the hue of accent alone may fall
// short. Where no hue reaches both distances, the surfaces take the first
// hue. The highlight then takes the surface farthest from bg, and the dim
// highlight takes the surface that splits the room most evenly.
//
// Text that the theme draws on a fill of its own, such as an inserted line
// or an error mark, keeps less contrast. A highlight that the printer paints
// replaces the fill with a surface near the luminance of bg, so text drawn
// in the color of bg keeps a contrast of at most about maxContrastLoss
// there. A highlight that the printer blends mixes the fill halfway toward
// a surface near bg, so the text loses about as much as it would if the
// printer blended bg itself into the fill. The search holds the text to
// that contrast divided by maxContrastLoss.
func highlightSurfaces(s style.Styles, fg, bg, accent color.Color, mode Mode) (color.Color, color.Color) {
	comment := s.Style(kind.Comment).GetForeground()
	if !isColorSet(comment) {
		comment = fg
	}

	t := tint{
		bg:           bg,
		fg:           fg,
		comment:      comment,
		base:         toLinear(bg),
		textFloor:    min(minTextContrast, contrast(fg, bg)/maxContrastLoss),
		commentFloor: contrast(comment, bg) / maxContrastLoss,
		lighter:      mode == Dark,
	}

	// The printer paints whole diff lines and error marks in these kinds,
	// and a theme may draw each on a fill of its own.
	for _, k := range []kind.Kind{kind.GenericInserted, kind.GenericDeleted, kind.GenericError} {
		st := s.Style(k)

		paint := st.GetBackground()
		if !isColorSet(paint) || sameColor(paint, bg) {
			continue
		}

		text := st.GetForeground()
		if !isColorSet(text) {
			text = fg
		}

		t.fills = append(t.fills, fill{
			text:  text,
			paint: paint,
			floor: contrast(text, colors.Blend(paint, bg)) / maxContrastLoss,
		})
	}

	plain := candidate{color: bg, blended: colors.Blend(bg, bg)}
	ring := hues(accent)

	for _, hue := range ring {
		t.hue = hue
		found := t.candidates()

		if dim, ok := plain.next(found); ok {
			if surface, ok := dim.next(found); ok {
				return dim.color, surface.color
			}
		}
	}

	// No hue leaves room for both distances within the floors.
	t.hue = ring[0]
	found := t.candidates()

	far, reach := plain, 0.0
	for _, c := range found {
		if d := c.apart(plain); d > reach {
			far, reach = c, d
		}
	}

	dim, gap := plain, 0.0
	for _, c := range found {
		if d := min(c.apart(plain), far.apart(c)); d > gap {
			dim, gap = c, d
		}
	}

	return dim.color, far.color
}

// liftStep is the contrast ratio with the base background that each
// luminance step of [tint] adds.
const liftStep = 0.005

// tintSteps is the number of steps [tint] takes from an untinted surface
// to the full hue.
const tintSteps = 20

// tint searches the surfaces of [highlightSurfaces]. Each candidate steps
// the luminance of bg toward that of fg, by lightening bg on a [Dark]
// theme and darkening it on a [Light] one. It then mixes that untinted
// color part of the way toward the hue at the same luminance. Light mixes
// linearly, so every tint at a step keeps the step's luminance.
type tint struct {
	bg, fg, comment         color.Color
	fills                   []fill
	base, hue               linear
	textFloor, commentFloor float64
	lighter                 bool
}

// fill is text that a theme draws on a fill of its own, with the contrast
// that the text keeps when the printer blends a highlight into the fill.
type fill struct {
	text, paint color.Color
	floor       float64
}

// at returns the candidate at luminance step i and tint step j, or false
// when step i leaves the range of luminance.
func (t tint) at(i, j int) (color.Color, bool) {
	from := t.base.luminance() + 0.05
	lift := 1 + float64(i)*liftStep

	y, end := from/lift-0.05, linear{}
	if t.lighter {
		y, end = from*lift-0.05, linear{r: 1, g: 1, b: 1}
	}

	if y < 0 || y > 1 {
		return nil, false
	}

	untinted := t.base.toward(end, y)

	return untinted.mix(t.hue.shade(y), float64(j)/tintSteps).color(), true
}

// keeps reports whether fg and comment keep their floors on c, and whether
// the text of each fill keeps its floor once the printer blends c into the
// fill.
func (t tint) keeps(c color.Color) bool {
	if contrast(t.fg, c) < t.textFloor || contrast(t.comment, c) < t.commentFloor {
		return false
	}

	for _, f := range t.fills {
		if contrast(f.text, colors.Blend(f.paint, c)) < f.floor {
			return false
		}
	}

	return true
}

// candidates returns the candidates that keep the floors, in the order
// that [highlightSurfaces] prefers them: the smallest luminance step
// first, and the least tint first within a step. It stops at the first
// luminance step where no tint keeps the floors.
func (t tint) candidates() []candidate {
	var found []candidate

	for i := 0; ; i++ {
		kept := false

		for j := 0; j <= tintSteps; j++ {
			c, ok := t.at(i, j)
			if !ok {
				return found
			}

			if t.keeps(c) {
				kept = true

				found = append(found, candidate{color: c, blended: colors.Blend(t.bg, c), lift: i, share: j})
			}
		}

		if !kept {
			return found
		}
	}
}

// candidate is a surface that [tint] tries, with the color it shows once
// the printer blends it into the base background, and with its luminance
// and tint steps.
type candidate struct {
	color, blended color.Color
	lift, share    int
}

// apart returns the CIEDE2000 distance between c and below once the
// printer blends both into the base background.
func (c candidate) apart(below candidate) float64 {
	return distance(c.blended, below.blended)
}

// next returns the first of found, at the luminance and tint steps of c or
// past them, that sits far enough from c. It returns false when none does.
// Far enough means blendDistance once the printer blends both into the base
// background, and paintDistance once it paints both.
func (c candidate) next(found []candidate) (candidate, bool) {
	for _, n := range found {
		if n.lift >= c.lift && n.share >= c.share &&
			n.apart(c) >= blendDistance && distance(n.color, c.color) >= paintDistance {
			return n, true
		}
	}

	return candidate{}, false
}

// linear is a color as linear sRGB channels from 0 to 1. Light adds
// linearly, so a mix of two colors has the same mix of their luminance.
type linear struct{ r, g, b float64 }

// toLinear returns c as [linear] channels.
func toLinear(c color.Color) linear {
	cf, _ := colorful.MakeColor(c)
	r, g, b := cf.LinearRgb()

	return linear{r: r, g: g, b: b}
}

// hueTurn is the angle, in degrees, between neighboring hues that [hues]
// returns.
const hueTurn = 30

// blueHue is the angle, in degrees, of blue on the color wheel.
const blueHue = 240

// hues returns the hues that [highlightSurfaces] tries, in the order it
// tries them, as from [hueAt]. The first is the hue of accent, and the
// rest turn hueTurn further from it at each step, on alternate sides, out
// to the opposite hue. A gray has no hue, so its hues start from blue.
func hues(accent color.Color) []linear {
	l := toLinear(accent)

	start, saturation, _ := colorful.Color{R: l.r, G: l.g, B: l.b}.Hsv()
	if saturation == 0 {
		start = blueHue
	}

	ring := []linear{hueAt(start)}
	for turn := float64(hueTurn); turn < 180; turn += hueTurn {
		ring = append(ring, hueAt(start+turn), hueAt(start-turn))
	}

	return append(ring, hueAt(start+180))
}

// hueAt returns the most saturated color at the angle deg on the color
// wheel of linear channels, with its brightest channel at 1 and its
// dimmest at 0.
func hueAt(deg float64) linear {
	c := colorful.Hsv(math.Mod(deg+360, 360), 1, 1)

	return linear{r: c.R, g: c.G, b: c.B}
}

// luminance returns the WCAG relative luminance of l.
func (l linear) luminance() float64 {
	return 0.2126*l.r + 0.7152*l.g + 0.0722*l.b
}

// mix returns the color a share t of the way from l to o.
func (l linear) mix(o linear, t float64) linear {
	return linear{r: l.r + (o.r-l.r)*t, g: l.g + (o.g-l.g)*t, b: l.b + (o.b-l.b)*t}
}

// toward returns the color on the line from l to o whose luminance is y,
// or l when the two colors share a luminance.
func (l linear) toward(o linear, y float64) linear {
	from, to := l.luminance(), o.luminance()
	if from == to {
		return l
	}

	return l.mix(o, (y-from)/(to-from))
}

// shade returns the color on the line from black through l to white whose
// luminance is y. For a color from [hueAt], that is the most saturated
// color of its hue at that luminance.
func (l linear) shade(y float64) linear {
	if y <= l.luminance() {
		return l.toward(linear{}, y)
	}

	return l.toward(linear{r: 1, g: 1, b: 1}, y)
}

// color returns l rounded to an 8-bit color.
func (l linear) color() color.Color {
	return lipgloss.Color(colorful.LinearRgb(l.r, l.g, l.b).Clamped().Hex())
}

// distance returns the CIEDE2000 distance between a and b, on
// go-colorful's scale of 0 to about 1.
func distance(a, b color.Color) float64 {
	ca, _ := colorful.MakeColor(a)
	cb, _ := colorful.MakeColor(b)

	return ca.DistanceCIEDE2000(cb)
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
	return toLinear(c).luminance()
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
