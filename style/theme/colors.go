package theme

import (
	"cmp"
	"errors"
	"fmt"
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

// Colors holds the colors that [Derive] builds the [style.Styles] of a
// theme from. A theme built from a Colors value presents every kind that a
// built-in theme does.
//
// A color field that holds nil or a [lipgloss.NoColor] is unset.
type Colors struct {
	// Fg is the color of the body text. An unset Fg leaves the terminal's
	// own foreground in place. [Derive] then derives the other kinds from
	// black for a [Light] theme and from white for a [Dark] one.
	Fg color.Color
	// Bg is the background of the theme, and it becomes the surface of
	// the styles, as [style.WithSurface] sets it. An unset Bg does not set
	// a surface, so the text shows the terminal's own background. [Derive]
	// then derives the other kinds from white for a [Light] theme and from
	// black for a [Dark] one.
	Bg color.Color
	// Accent colors the plain headings and accented text. The highlights
	// take its hue, or the nearest hue that lets them stand apart. An unset
	// Accent takes a blue picked for the mode.
	Accent color.Color
	// OK colors success text, success headings, and inserted lines. An
	// unset OK takes a green picked for the mode.
	OK color.Color
	// Warn colors warning text, warning headings, and the locations of
	// warnings. An unset Warn takes an amber picked for the mode.
	Warn color.Color
	// Error colors error text, error headings, deleted lines, and the
	// locations of errors. An unset Error takes a red picked for the mode.
	Error color.Color
	// Mode is the background the theme targets. It also picks the
	// direction of the derived shifts. [kind.TextSubtle] and
	// [kind.TextSubtleDim] move toward the background, but
	// [kind.TextAccentDim] moves away from it. The dimmed accent thus reads
	// brighter than the accent on a [Dark] theme and darker on a [Light]
	// one. An accent that cannot move further from the background, such as
	// black on a [Light] theme, dims toward it instead.
	Mode Mode
}

// Token styles one kind of a theme that [Derive] builds. It holds a spec
// in the form that [style.Parse] reads, such as "bold #268bd2".
//
// Derive layers each Token over the style its kind takes without the
// Token. A Token that names only attributes thus keeps the colors of that
// style. The "nobold", "noitalic", and "nounderline" keywords turn off an
// attribute that the style beneath sets. In [style.Parse], those keywords
// leave the attribute unset instead.
//
// A Token decodes from text, such as a YAML or JSON string, through
// [Token.UnmarshalText], which rejects a spec that [style.Parse] rejects.
// A program that decodes a theme file into a map of Tokens thus gets the
// error from the decode.
//
// A type that holds a Token reads text. A niceyaml decode into such a type
// thus holds the aliases of the whole document to the bound on the text
// they repeat, as the Limits section of [go.jacobcolvin.com/niceyaml]
// describes. When the document holds an alias, the decode counts that
// text once for the whole document. A config loader whose documents repeat
// large anchors decodes the tokens as strings and converts them to Tokens,
// which [FromColors] checks.
type Token string

// UnmarshalText sets t to text when [style.Parse] accepts it. Otherwise it
// returns the error from style.Parse, which wraps [style.ErrInvalidColor]
// or [style.ErrUnknownKeyword], and leaves t as it was.
func (t *Token) UnmarshalText(text []byte) error {
	_, err := style.Parse(string(text))
	if err != nil {
		return err //nolint:wrapcheck // The doc names the error of style.Parse.
	}

	*t = Token(text)

	return nil
}

// FromColors creates a new [Theme] named name, whose [Theme.Styles]
// returns the styles that [Derive] builds from c and tokens. The theme
// takes its [Mode] from c. It keeps a copy of tokens, so a later change
// to the map does not change the theme.
//
// FromColors checks each token when it creates the theme. It returns an
// error for each token that [style.Parse] rejects, sorted by kind and
// joined with [errors.Join]. Each error names the kind of its token and
// wraps the error from style.Parse. A decoder that calls
// [Token.UnmarshalText], such as niceyaml, go-yaml, or encoding/json,
// rejects such a token earlier, when it decodes the theme file. A
// niceyaml decode also reports where the file holds the token.
//
// A Token that a program sets any other way reaches FromColors unchecked,
// and the error for it does not name a place in a file. Such Tokens
// include one converted from a string with theme.Token(spec), and one set
// by a decoder that does not call UnmarshalText, such as mapstructure
// without its TextUnmarshallerHookFunc.
func FromColors(name string, c Colors, tokens map[kind.Kind]Token) (Theme, error) {
	err := checkTokens(tokens)
	if err != nil {
		return Theme{}, err
	}

	return fromColors(name, c, maps.Clone(tokens)), nil
}

// MustFromColors is like [FromColors] but panics when FromColors returns
// an error.
//
// Use it for a theme whose tokens the program spells out in its source.
func MustFromColors(name string, c Colors, tokens map[kind.Kind]Token) Theme {
	t, err := FromColors(name, c, tokens)
	if err != nil {
		panic(err)
	}

	return t
}

// fromColors creates a new [Theme] that derives its styles from c and
// tokens as [FromColors] does, but without checking or copying tokens.
// The first call to [Theme.Styles] panics on a token that [style.Parse]
// rejects. The built-in catalog creates its themes with fromColors. A
// program that imports the package thus does not parse every built-in
// token at startup.
func fromColors(name string, c Colors, tokens map[kind.Kind]Token) Theme {
	return New(name, c.Mode, func() style.Styles {
		return derive(c, tokens)
	})
}

// Derive builds the [style.Styles] of a theme from c and tokens.
//
// Derive first sets the kinds that the colors of c decide:
//
//   - [kind.GenericHeading] draws bold text on Accent,
//     [kind.GenericHeadingOK] on OK, [kind.GenericHeadingWarn] on Warn,
//     and [kind.GenericHeadingError] on Error. Each draws its text in Fg
//     or Bg, whichever contrasts more with the color behind it.
//   - [kind.GenericHighlightDim] paints a tint of Bg, and
//     [kind.GenericHighlight] paints a stronger tint.
//     [kind.GenericHeadingSubtle] draws Fg on the dim tint, and
//     [kind.GenericHeadingAccent] draws Fg, shifted away from Bg, on the
//     stronger one.
//   - [kind.TextAccent] draws in Accent, and [kind.TextAccentDim] draws in
//     a shade of Accent that Mode picks.
//   - [kind.TextSubtle] draws Fg shifted toward Bg, and
//     [kind.TextSubtleDim] shifts it further.
//   - [kind.TextOK] draws in OK, [kind.TextWarn] in Warn, and
//     [kind.TextError] in Error.
//
// A token for one of these kinds layers over the style that Derive sets
// for it. A token for a kind above one of them, such as [kind.Generic] or
// [kind.Text], does not reach it.
//
// A renderer paints the lines of a diff and the locations of errors and
// warnings in four more kinds. Derive gives each one that tokens leaves
// out a default:
//
//   - [kind.GenericInserted] draws in OK.
//   - [kind.GenericDeleted] draws in Error.
//   - [kind.GenericError] draws as a badge on Error.
//   - [kind.GenericWarn] draws as a badge on Warn.
//
// Each badge draws its text in Fg or Bg, whichever contrasts more with the
// badge. The mark thus shows even over a token drawn in the color of the
// badge. A token for one of these four kinds replaces the default and
// layers over the style the kind inherits.
//
// Derive then layers each token over the style its kind inherits, parents
// ahead of children. A token for a child thus layers over the finished
// style of its parent. These kinds take a style from another kind:
//
//   - [kind.LiteralNull] takes the token of [kind.LiteralBoolean] when
//     tokens sets the booleans and leaves null out. Most editor color
//     schemes color null, true, and false as one keyword constant.
//   - [kind.UI] takes the foreground of the finished [kind.Comment] style
//     but not its attributes. The gutters and annotations thus do not
//     take the italics or bold of the comments. A UI token layers over
//     that color, and the kinds below UI inherit from it.
//
// Derive picks the two highlight tints against the finished token styles.
// Each tint stands apart from the color beneath it, whether a renderer
// paints the highlight over the text or blends it into the background of
// the text. On each tint, the body text and the comments stay readable.
// So does the text of each diff line or badge that paints a fill of its
// own.
//
// Bg becomes the surface of the styles, as [style.WithSurface] sets it, so
// a kind holds a background of its own only when it paints one.
//
// The rules and the constants behind the derived colors might change in a
// later release. A test that pins the exact output of Derive might then
// fail.
//
// Derive returns an error for each token that [style.Parse] rejects,
// sorted by kind and joined with [errors.Join]. Each error names the kind
// of its token and wraps the error from style.Parse. A decoder that calls
// [Token.UnmarshalText] rejects such a token earlier, as [FromColors]
// describes.
func Derive(c Colors, tokens map[kind.Kind]Token) (style.Styles, error) {
	err := checkTokens(tokens)
	if err != nil {
		return style.Styles{}, err
	}

	return derive(c, tokens), nil
}

// MustDerive is like [Derive] but panics when Derive returns an error.
//
// Use it for tokens that the program spells out in its source, such as in
// the build function of a theme from [New].
func MustDerive(c Colors, tokens map[kind.Kind]Token) style.Styles {
	s, err := Derive(c, tokens)
	if err != nil {
		panic(err)
	}

	return s
}

// derive builds the styles that [Derive] describes, without checking
// tokens first. It panics on a token that [style.Parse] rejects.
func derive(c Colors, tokens map[kind.Kind]Token) style.Styles {
	c = c.filled()
	fg, bg := c.surface()

	// The search for the highlight surfaces holds the finished token
	// styles to contrast floors, so a first build with bg in place of the
	// surfaces supplies those styles.
	dim, surface := highlightSurfaces(build(c, tokens, bg, bg), fg, bg, c.Accent, c.Mode)

	return build(c, tokens, dim, surface)
}

// checkTokens joins an error for each token that [style.Parse] rejects,
// in the order of the kinds. It returns nil when style.Parse accepts every
// token.
func checkTokens(tokens map[kind.Kind]Token) error {
	var errs []error

	for _, k := range slices.Sorted(maps.Keys(tokens)) {
		_, err := style.Parse(string(tokens[k]))
		if err != nil {
			errs = append(errs, fmt.Errorf("token for %s: %w", k, err))
		}
	}

	return errors.Join(errs...)
}

// filled returns c with each unset color other than Fg and Bg replaced by
// the default for its mode.
func (c Colors) filled() Colors {
	accent, ok, warn, errColor := "#0969da", "#1a7f37", "#9a6700", "#cf222e"
	if c.Mode == Dark {
		accent, ok, warn, errColor = "#58a6ff", "#3fb950", "#d29922", "#f85149"
	}

	if !isColorSet(c.Accent) {
		c.Accent = lipgloss.Color(accent)
	}

	if !isColorSet(c.OK) {
		c.OK = lipgloss.Color(ok)
	}

	if !isColorSet(c.Warn) {
		c.Warn = lipgloss.Color(warn)
	}

	if !isColorSet(c.Error) {
		c.Error = lipgloss.Color(errColor)
	}

	return c
}

// build builds the [style.Styles] for c and tokens, with dimSurface and
// surface as the backgrounds of the highlights and of the subtle and
// accent headings. The caller must set every color of c other than Fg and
// Bg, as filled does. Bg becomes the surface of the result, as
// [style.WithSurface] sets it, so a kind holds a background of its own only
// when it paints a fill.
func build(c Colors, tokens map[kind.Kind]Token, dimSurface, surface color.Color) style.Styles {
	base := lipgloss.NewStyle()
	if isColorSet(c.Fg) {
		base = base.Foreground(c.Fg)
	}

	fg, bg := c.surface()

	towardFg, towardBg := lipgloss.Lighten, lipgloss.Darken
	if c.Mode == Light {
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

	dimAccent := towardFg(c.Accent, dimShift)
	if sameColor(dimAccent, c.Accent) {
		dimAccent = towardBg(c.Accent, dimShift)
	}

	// The derived kinds resolve first, so a token naming one of them
	// layers over the derived value rather than the other way around.
	derived := []style.Option{
		style.Set(kind.GenericHeading, heading(c.Accent)),
		style.Set(kind.GenericHeadingAccent, base.Background(surface).Foreground(towardFg(fg, dimShift))),
		style.Set(kind.GenericHeadingSubtle, base.Background(dimSurface).Foreground(fg)),
		style.Set(kind.GenericHeadingOK, heading(c.OK)),
		style.Set(kind.GenericHeadingWarn, heading(c.Warn)),
		style.Set(kind.GenericHeadingError, heading(c.Error)),
		style.Set(kind.GenericHighlight, lipgloss.NewStyle().Background(surface)),
		style.Set(kind.GenericHighlightDim, lipgloss.NewStyle().Background(dimSurface)),
		style.Set(kind.TextAccent, base.Foreground(c.Accent)),
		style.Set(kind.TextAccentDim, base.Foreground(dimAccent)),
		style.Set(kind.TextSubtle, base.Foreground(towardBg(fg, dimShift))),
		style.Set(kind.TextSubtleDim, base.Foreground(towardBg(fg, subtleDimShift))),
		style.Set(kind.TextOK, base.Foreground(c.OK)),
		style.Set(kind.TextWarn, base.Foreground(c.Warn)),
		style.Set(kind.TextError, base.Foreground(c.Error)),
	}

	s := style.New(base, derived...)

	// The printer paints whole inserted and deleted lines and their + and -
	// markers in these kinds. It marks errors with a GenericError overlay
	// and warnings with a GenericWarn overlay, and each overlay replaces the
	// style underneath. The two overlays draw as badges, so each shows over
	// a token drawn in the color of its badge. The marks hold the colors of
	// c as given, so an ANSI color keeps the palette of the terminal. Each
	// mark stands in for a missing token and layers over the parent of its
	// kind, as a token does. A token for one of these kinds thus replaces
	// the mark outright.
	marks := map[kind.Kind]lipgloss.Style{
		kind.GenericInserted: lipgloss.NewStyle().Foreground(c.OK),
		kind.GenericDeleted:  lipgloss.NewStyle().Foreground(c.Error),
		kind.GenericError:    lipgloss.NewStyle().Foreground(textOn(c.Error)).Background(c.Error),
		kind.GenericWarn:     lipgloss.NewStyle().Foreground(textOn(c.Warn)).Background(c.Warn),
	}

	// Null and the booleans share one keyword-constant token in most editor
	// color schemes, so when tokens leaves null out, null takes the boolean
	// token. The two kinds are siblings under Literal, so the token
	// resolves to the same style for both.
	specs := tokens
	if spec, ok := tokens[kind.LiteralBoolean]; ok {
		if _, ok := tokens[kind.LiteralNull]; !ok {
			specs = maps.Clone(tokens)
			specs[kind.LiteralNull] = spec
		}
	}

	// A token layers over the style its kind already resolves to. A token
	// of "bold" alone thus keeps the ancestor's colors. Parents come
	// first, so a child layers over the parent's finished style. The pass
	// visits UI even when tokens leaves it out, because the chrome takes its
	// color from the finished comments. ByDepth puts the two entries of a
	// kind that specs and marks both hold side by side, and Compact drops
	// the second.
	kinds := slices.Compact(byDepth(slices.Concat(
		slices.Collect(maps.Keys(specs)),
		slices.Collect(maps.Keys(marks)),
		[]kind.Kind{kind.UI},
	)))

	for _, st := range kinds {
		cur := s.Style(st)
		if st == kind.UI {
			// The chrome takes the comment color alone. A token that sets
			// comments in italics or bold styles the comments, and a line
			// number drawn the same way reads as one. UI comes after
			// Comment and ahead of its own children in the depth order. A
			// UI token thus layers over this style, and a token for a child
			// layers over UI rather than over the comments.
			cur = base.Foreground(s.Style(kind.Comment).GetForeground())
		}

		if spec, ok := specs[st]; ok {
			cur = layer(cur, spec)
		} else if mark, ok := marks[st]; ok {
			cur = paint(cur, mark)
		}

		s = s.With(style.Set(st, cur))
	}

	// The token pass layers each token over a style it reads from s. A
	// style read with the surface in place carries the surface as a fill
	// of its own, so the surface comes last.
	if isColorSet(c.Bg) {
		s = s.With(style.WithSurface(c.Bg))
	}

	return s
}

// dimShift separates a dimmed variant from its source color.
const dimShift = 0.15

// subtleDimShift moves [kind.TextSubtleDim] further toward the background
// than dimShift moves [kind.TextSubtle].
const subtleDimShift = 0.30

// minTextContrast is the WCAG AA contrast ratio for body text. The body
// text keeps at least this ratio on a highlight, unless its own ratio on
// the base background is less than maxContrastLoss times this one. The
// text then keeps at least its own ratio divided by maxContrastLoss.
const minTextContrast = 4.5

// maxContrastLoss is the most that a highlight might divide the contrast
// of the comments by. It bounds the body text the same way only where the
// body's own contrast is less than minTextContrast times maxContrastLoss.
// Body text with more contrast than that keeps minTextContrast alone.
const maxContrastLoss = 1.25

// blendDistance and paintDistance are CIEDE2000 distances, on
// go-colorful's scale of 0 to about 1. [highlightSurfaces] puts them
// between the base background and the dim highlight, and between the dim
// highlight and the highlight. The first measures the highlights as the
// printer blends them into the base background, and the second measures
// them as it paints them over it. Both are well more than 0.05, about the
// least distance at which a reader tells two colors apart. The highlights
// thus show at a glance in either overlay mode.
const (
	blendDistance = 0.08
	paintDistance = 0.135
)

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
// the hierarchy. A custom kind counts as one step, because [kind.Parent]
// reports [kind.Text] for it.
func depth(st kind.Kind) int {
	n := 0
	for current := st; current != kind.Text; current = kind.Parent(current) {
		n++
	}

	return n
}

// surface returns the foreground and background that [Derive] derives the
// other kinds from. These are Fg and Bg, or black and white arranged for
// the mode where c leaves one unset.
func (c Colors) surface() (color.Color, color.Color) {
	fg, bg := lipgloss.Color("#000000"), lipgloss.Color("#ffffff")
	if c.Mode == Dark {
		fg, bg = bg, fg
	}

	if isColorSet(c.Fg) {
		fg = c.Fg
	}

	if isColorSet(c.Bg) {
		bg = c.Bg
	}

	return fg, bg
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
// that puts it far enough from the color beneath it. Far enough means
// blendDistance when the printer blends both into bg, and paintDistance
// when the printer paints both. The search stops before the contrast of fg
// on a surface becomes less than the lower of minTextContrast and the
// contrast of fg with bg divided by maxContrastLoss. It also stops before
// the contrast of the comments on a surface becomes less than their
// contrast with bg divided by maxContrastLoss.
//
// The search tries the hues that [hues] returns for accent, in order, and
// takes the first that reaches both distances within these floors. The
// gamut holds less chroma for some hues than for others at a given
// luminance, such as blue near white, so the hue of accent alone might not
// reach them. When none of the hues reaches both distances, the surfaces
// take the first hue. The highlight then takes the surface farthest from
// bg, and the dim highlight takes the surface that splits the room most
// evenly.
//
// Text that the theme draws on a fill of its own, such as an inserted line
// or an error mark, keeps less contrast. A highlight that the printer paints
// replaces the fill with a surface near the luminance of bg. Text drawn in
// the color of bg then keeps a contrast of at most about maxContrastLoss
// there. A highlight that the printer blends mixes the fill halfway toward
// a surface near bg. The text then keeps about the contrast it has on a
// blend of the fill with bg itself. The search holds the text to that
// contrast divided by maxContrastLoss.
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
	for _, k := range []kind.Kind{kind.GenericInserted, kind.GenericDeleted, kind.GenericError, kind.GenericWarn} {
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
// the text of each fill keeps its floor when the printer blends c into the
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
// luminance step where none of the tints keeps the floors.
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

// candidate is a surface that [tint] tries, with the color it shows when
// the printer blends it into the base background, and with its luminance
// and tint steps.
type candidate struct {
	color, blended color.Color
	lift, share    int
}

// apart returns the CIEDE2000 distance between c and below when the
// printer blends both into the base background.
func (c candidate) apart(below candidate) float64 {
	return distance(c.blended, below.blended)
}

// next returns the first of found, at the luminance and tint steps of c or
// past them, that sits far enough from c. It returns false when none does.
// Far enough means blendDistance when the printer blends both into the base
// background, and paintDistance when it paints both.
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
// to the opposite hue. A gray does not have a hue, so its hues start from
// blue.
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

// layer returns base with the colors and attributes of spec applied on
// top. The other properties of base stay in place. Attribute keywords
// apply left to right as they do in [style.Parse], so the last keyword for
// an attribute wins. A "nobold", "noitalic", or "nounderline" keyword
// turns the attribute off even when base sets it. In [style.Parse], that
// keyword leaves the attribute unset instead.
//
//nolint:gocritic // Value semantics match lipgloss.
func layer(base lipgloss.Style, spec Token) lipgloss.Style {
	base = paint(base, style.MustParse(string(spec)))

	for word := range strings.FieldsSeq(strings.ToLower(string(spec))) {
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

// paint returns base with the foreground and background that over sets.
// The other properties of base stay in place.
//
//nolint:gocritic // Value semantics match lipgloss.
func paint(base, over lipgloss.Style) lipgloss.Style {
	if c := over.GetForeground(); isColorSet(c) {
		base = base.Foreground(c)
	}

	if c := over.GetBackground(); isColorSet(c) {
		base = base.Background(c)
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
