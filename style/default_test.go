package style_test

import (
	"image/color"
	"math"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/exp/charmtone"
	"github.com/lucasb-eyer/go-colorful"
	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml/internal/colors"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

// tokenKinds lists the kinds that the default styles color in YAML content.
var tokenKinds = []kind.Kind{
	kind.Text,
	kind.Comment,
	kind.CommentPreproc,
	kind.LiteralString,
	kind.LiteralNumber,
	kind.LiteralBoolean,
	kind.LiteralNull,
	kind.NameTag,
	kind.NameAnchor,
	kind.NameAlias,
	kind.NameDecorator,
	kind.Punctuation,
	kind.PunctuationHeading,
}

func TestDefault_SharedValueStaysUnchanged(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		isDark bool
	}{
		"dark":  {isDark: true},
		"light": {isDark: false},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			first := style.Default(tc.isDark)
			assert.Equal(t, first, style.Default(tc.isDark))

			comment := first.Style(kind.Comment)

			extended := style.Default(tc.isDark).With(style.Set(kind.Comment, redStyle))
			assert.Equal(t, redStyle, extended.Style(kind.Comment))

			// With copies the shared value, so a later call still sees the
			// default.
			assert.Equal(t, comment, style.Default(tc.isDark).Style(kind.Comment))
		})
	}
}

func TestDefault_Background(t *testing.T) {
	t.Parallel()

	dark, light := style.Default(true), style.Default(false)

	assert.Equal(t, charmtone.Smoke, dark.Style(kind.Text).GetForeground())
	assert.Equal(t, charmtone.Char, light.Style(kind.Text).GetForeground())

	// Neither palette paints a surface, so both draw on the terminal's own
	// background.
	for name, styles := range map[string]style.Styles{"dark": dark, "light": light} {
		assert.Nil(t, styles.Surface(), name)
		assert.Equal(t, lipgloss.NoColor{}, styles.Style(kind.Text).GetBackground(), name)
	}
}

func TestDefault_Readable(t *testing.T) {
	t.Parallel()

	// The default styles draw on the terminal's own background. The test
	// takes that background to be white for a light terminal and black for
	// a dark one. The light palette keeps the WCAG AA 4.5:1 for every
	// token. The dark palette dims its comments on purpose, so its floor is
	// the 3:1 that WCAG asks of large text.
	tcs := map[string]struct {
		background color.Color
		isDark     bool
		floor      float64
	}{
		"light on white": {isDark: false, background: color.White, floor: 4.5},
		"dark on black":  {isDark: true, background: color.Black, floor: 3},
	}

	// These kinds draw text on a fill of their own, whatever the terminal
	// shows behind them.
	filled := []kind.Kind{
		kind.GenericInserted,
		kind.GenericDeleted,
		kind.GenericError,
		kind.GenericWarn,
		kind.GenericHeading,
		kind.GenericHeadingAccent,
		kind.GenericHeadingSubtle,
		kind.GenericHeadingOK,
		kind.GenericHeadingWarn,
		kind.GenericHeadingError,
	}

	// The messages of errors and warnings and the labels of a status bar
	// draw on the background as the tokens do.
	text := append([]kind.Kind{
		kind.TextAccent,
		kind.TextAccentDim,
		kind.TextSubtle,
		kind.TextOK,
		kind.TextWarn,
		kind.TextError,
		kind.UI,
	}, tokenKinds...)

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			styles := style.Default(tc.isDark)

			for _, k := range text {
				fg := styles.Style(k).GetForeground()
				assert.GreaterOrEqual(t, yamltest.Contrast(fg, tc.background), tc.floor,
					"%s draws %s", k, yamltest.ColorHex(fg))
			}

			for _, k := range filled {
				st := styles.Style(k)
				fg, bg := st.GetForeground(), st.GetBackground()
				assert.GreaterOrEqual(t, yamltest.Contrast(fg, bg), tc.floor,
					"%s draws %s on %s", k, yamltest.ColorHex(fg), yamltest.ColorHex(bg))
			}
		})
	}
}

func TestDefault_HighlightsReadable(t *testing.T) {
	t.Parallel()

	content := append([]kind.Kind{kind.GenericInserted, kind.GenericDeleted}, tokenKinds...)

	// The default styles draw on the terminal's own background. The test
	// takes that background to be black for the dark palette and white for
	// the light one. The themes theme.Charm() and theme.CharmLight() draw the
	// same palettes on a Pepper surface and a Salt surface, and a blended
	// highlight mixes into each.
	dark, light := style.Default(true), style.Default(false)
	pepper := dark.With(style.WithSurface(charmtone.Pepper))
	salt := light.With(style.WithSurface(charmtone.Salt))

	// A reader tells two fills apart from a distance of about 0.05. A
	// painted highlight keeps the 0.135 that the catalog themes keep. A
	// blend halves the distance between a fill and the surface, and the
	// grays of CharmTone then leave 0.06.
	const (
		overrideApart = 0.135
		blendApart    = 0.06
	)

	tcs := map[string]struct {
		background color.Color
		apply      func(base, overlay lipgloss.Style) lipgloss.Style
		styles     style.Styles
		apart      float64
	}{
		"dark override": {
			apply: colors.OverrideStyles, styles: dark, background: color.Black, apart: overrideApart,
		},
		"dark blend": {
			apply: colors.BlendStyles, styles: dark, background: color.Black, apart: overrideApart,
		},
		"dark override on pepper": {
			apply: colors.OverrideStyles, styles: pepper, background: charmtone.Pepper, apart: overrideApart,
		},
		"dark blend on pepper": {
			apply: colors.BlendStyles, styles: pepper, background: charmtone.Pepper, apart: blendApart,
		},
		"light override": {
			apply: colors.OverrideStyles, styles: light, background: color.White, apart: overrideApart,
		},
		"light blend": {
			apply: colors.BlendStyles, styles: light, background: color.White, apart: overrideApart,
		},
		"light override on salt": {
			apply: colors.OverrideStyles, styles: salt, background: charmtone.Salt, apart: overrideApart,
		},
		"light blend on salt": {
			apply: colors.BlendStyles, styles: salt, background: charmtone.Salt, apart: blendApart,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			styles := tc.styles

			for _, hl := range []kind.Kind{kind.GenericHighlight, kind.GenericHighlightDim} {
				for _, k := range content {
					got := tc.apply(styles.Style(k), styles.Style(hl))
					assert.NotEqual(t, yamltest.ColorHex(got.GetForeground()), yamltest.ColorHex(got.GetBackground()),
						"%s over %s hides its text", hl, k)
				}

				text := tc.apply(styles.Style(kind.Text), styles.Style(hl))
				assert.GreaterOrEqual(t, yamltest.Contrast(text.GetForeground(), text.GetBackground()), 4.5,
					"%s over %s", hl, kind.Text)
			}

			// The viewport fills every match but the selected one with the
			// dim highlight, so that fill stands apart from the background.
			// The selected match stands apart from the other matches. A
			// palette without a surface has nothing to blend a fill into,
			// so its blend keeps the distance of a painted highlight.
			current := tc.apply(styles.Style(kind.Text), styles.Style(kind.GenericHighlight)).GetBackground()
			other := tc.apply(styles.Style(kind.Text), styles.Style(kind.GenericHighlightDim)).GetBackground()

			assert.GreaterOrEqual(t, distance(tc.background, other), tc.apart,
				"dim highlight %s sits close to the background %s",
				yamltest.ColorHex(other), yamltest.ColorHex(tc.background))
			assert.GreaterOrEqual(t, distance(other, current), tc.apart,
				"highlight %s sits close to the dim highlight %s",
				yamltest.ColorHex(current), yamltest.ColorHex(other))
		})
	}
}

func TestDefault_HeadingFillsShow(t *testing.T) {
	t.Parallel()

	// A title bar draws its quiet segments in these kinds. Each fill shows
	// against the terminal's own background and against the surface that
	// theme.Charm() or theme.CharmLight() paints.
	tcs := map[string]struct {
		background color.Color
		isDark     bool
	}{
		"dark on black":  {isDark: true, background: color.Black},
		"dark on pepper": {isDark: true, background: charmtone.Pepper},
		"light on white": {isDark: false, background: color.White},
		"light on salt":  {isDark: false, background: charmtone.Salt},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			styles := style.Default(tc.isDark)

			for _, k := range []kind.Kind{kind.GenericHeadingAccent, kind.GenericHeadingSubtle} {
				fill := styles.Style(k).GetBackground()
				assert.GreaterOrEqual(t, distance(tc.background, fill), 0.07,
					"%s fill %s sits close to the background %s",
					k, yamltest.ColorHex(fill), yamltest.ColorHex(tc.background))
			}
		})
	}
}

func TestDefault_AccentDimMovesAwayFromBackground(t *testing.T) {
	t.Parallel()

	// A program sets the dimmed accent beside the accent. The dimmed accent
	// is a shade of the accent that moves away from the background. It
	// reads brighter on a dark terminal and darker on a light one. Each
	// palette keeps that relation against the terminal's own background and
	// against the surface that theme.Charm() or theme.CharmLight() paints.
	tcs := map[string]struct {
		background color.Color
		isDark     bool
	}{
		"dark on black":  {isDark: true, background: color.Black},
		"dark on pepper": {isDark: true, background: charmtone.Pepper},
		"light on white": {isDark: false, background: color.White},
		"light on salt":  {isDark: false, background: charmtone.Salt},
	}

	// A lighter or a darker shade turns its hue by a few degrees. The
	// dimmed accent keeps the hue of the accent within this many degrees.
	const hueDrift = 20

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			styles := style.Default(tc.isDark)
			accent := styles.Style(kind.TextAccent).GetForeground()
			dim := styles.Style(kind.TextAccentDim).GetForeground()

			assert.Greater(t, yamltest.Contrast(dim, tc.background), yamltest.Contrast(accent, tc.background),
				"dimmed accent %s sits closer to the background than the accent %s",
				yamltest.ColorHex(dim), yamltest.ColorHex(accent))
			assert.LessOrEqual(t, hueApart(accent, dim), float64(hueDrift),
				"dimmed accent %s leaves the hue of the accent %s",
				yamltest.ColorHex(dim), yamltest.ColorHex(accent))

			// A terminal with 256 colors keeps the two apart, in the same
			// order.
			accent256 := colorprofile.ANSI256.Convert(accent)
			dim256 := colorprofile.ANSI256.Convert(dim)

			assert.Greater(t, yamltest.Contrast(dim256, tc.background), yamltest.Contrast(accent256, tc.background),
				"dimmed accent %s sits closer to the background than the accent %s",
				yamltest.ColorHex(dim256), yamltest.ColorHex(accent256))
		})
	}
}

func TestDefault_ANSI256(t *testing.T) {
	t.Parallel()

	// A terminal with 256 colors draws each color as the nearest entry of
	// its table. These kinds carry a hue. A muted shade of one lands on a
	// gray entry, and a reader cannot tell that token from a comment. A
	// muted brown lands on a red entry, and a string then reads as an
	// anchor.
	hued := []kind.Kind{
		kind.LiteralString,
		kind.LiteralNumber,
		kind.LiteralBoolean,
		kind.LiteralNull,
		kind.NameTag,
		kind.NameAnchor,
		kind.NameAlias,
		kind.NameDecorator,
		kind.Punctuation,
	}

	// The table holds few entries of each hue, so the nearest one sits a few
	// degrees from the color it stands for. A token keeps its hue within
	// this many degrees.
	const hueDrift = 25

	// Each palette keeps the floor that TestDefault_Readable gives it in
	// true color.
	tcs := map[string]struct {
		background color.Color
		isDark     bool
		floor      float64
	}{
		"light on white": {isDark: false, background: color.White, floor: 4.5},
		"dark on black":  {isDark: true, background: color.Black, floor: 3},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			styles := style.Default(tc.isDark)

			converted := func(k kind.Kind) color.Color {
				return colorprofile.ANSI256.Convert(styles.Style(k).GetForeground())
			}

			for _, k := range tokenKinds {
				assert.GreaterOrEqual(t, yamltest.Contrast(converted(k), tc.background), tc.floor,
					"%s draws %s", k, yamltest.ColorHex(converted(k)))
			}

			for _, k := range hued {
				r, g, b, _ := converted(k).RGBA()
				assert.False(t, r == g && g == b,
					"%s draws the gray %s", k, yamltest.ColorHex(converted(k)))

				fg := styles.Style(k).GetForeground()
				assert.LessOrEqual(t, hueApart(fg, converted(k)), float64(hueDrift),
					"%s draws %s as %s", k, yamltest.ColorHex(fg), yamltest.ColorHex(converted(k)))
			}

			// Two tokens that differ in true color differ here too.
			for i, a := range tokenKinds {
				for _, b := range tokenKinds[i+1:] {
					if sameColor(styles.Style(a).GetForeground(), styles.Style(b).GetForeground()) {
						continue
					}

					assert.False(t, sameColor(converted(a), converted(b)),
						"%s and %s both draw %s", a, b, yamltest.ColorHex(converted(a)))
				}
			}
		})
	}
}

// sameColor reports whether a and b are one color, whatever their types.
func sameColor(a, b color.Color) bool {
	return yamltest.ColorHex(a) == yamltest.ColorHex(b)
}

// hueApart returns the angle between the hues of a and b, from 0 to 180
// degrees.
func hueApart(a, b color.Color) float64 {
	ca, _ := colorful.MakeColor(a)
	cb, _ := colorful.MakeColor(b)

	ha, _, _ := ca.Hsv()
	hb, _, _ := cb.Hsv()

	apart := math.Abs(ha - hb)
	if apart > 180 {
		apart = 360 - apart
	}

	return apart
}

// distance returns the CIEDE2000 distance between a and b, on
// go-colorful's scale of 0 to about 1.
func distance(a, b color.Color) float64 {
	ca, _ := colorful.MakeColor(a)
	cb, _ := colorful.MakeColor(b)

	return ca.DistanceCIEDE2000(cb)
}
