package colors_test

import (
	"image/color"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/lucasb-eyer/go-colorful"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/internal/colors"
)

func TestOverride(t *testing.T) {
	t.Parallel()

	red := lipgloss.Color("#FF0000")
	blue := lipgloss.Color("#0000FF")

	tcs := map[string]struct {
		base    color.Color
		overlay color.Color
		want    color.Color
	}{
		"overlay valid returns overlay": {
			base:    red,
			overlay: blue,
			want:    blue,
		},
		"overlay nil returns base": {
			base:    red,
			overlay: nil,
			want:    red,
		},
		"overlay NoColor returns base": {
			base:    red,
			overlay: lipgloss.NoColor{},
			want:    red,
		},
		"overlay invisible returns base": {
			base:    red,
			overlay: color.RGBA{R: 255, G: 0, B: 0, A: 0},
			want:    red,
		},
		"both nil returns nil": {
			base:    nil,
			overlay: nil,
			want:    nil,
		},
		"base nil overlay valid returns overlay": {
			base:    nil,
			overlay: blue,
			want:    blue,
		},
		"overlay outside the gamut is clamped": {
			base:    red,
			overlay: colorful.Color{R: -0.2, G: 1.4, B: 0.5},
			want:    colorful.Color{R: 0, G: 1, B: 0.5},
		},
		"base outside the gamut with overlay nil is clamped": {
			base:    colorful.Color{R: -0.2, G: 1.4, B: 0.5},
			overlay: nil,
			want:    colorful.Color{R: 0, G: 1, B: 0.5},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := colors.Override(tc.base, tc.overlay)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestAbsentOverlay(t *testing.T) {
	t.Parallel()

	// Override and Blend share one rule for an absent color, so both hand
	// back the base color, clamped into the gamut, for each of these
	// overlays.
	tcs := map[string]struct {
		base    color.Color
		overlay color.Color
		want    color.Color
	}{
		"nil": {
			base:    lipgloss.Color("#FF0000"),
			overlay: nil,
			want:    lipgloss.Color("#FF0000"),
		},
		"NoColor": {
			base:    lipgloss.Color("#FF0000"),
			overlay: lipgloss.NoColor{},
			want:    lipgloss.Color("#FF0000"),
		},
		"invisible": {
			base:    lipgloss.Color("#FF0000"),
			overlay: color.RGBA{A: 0},
			want:    lipgloss.Color("#FF0000"),
		},
		"nil with base outside the gamut": {
			base:    colorful.Color{R: -0.2, G: 1.4, B: 0.5},
			overlay: nil,
			want:    colorful.Color{R: 0, G: 1, B: 0.5},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, colors.Override(tc.base, tc.overlay))
			assert.Equal(t, tc.want, colors.Blend(tc.base, tc.overlay))
		})
	}
}

// labMidpoint returns the 50/50 LAB blend of c1 and c2, clamped to the
// sRGB gamut. It mixes the colors with go-colorful directly, so a test that
// compares against it catches a blend that drops either color.
func labMidpoint(t *testing.T, c1, c2 color.Color) color.Color {
	t.Helper()

	cf1, ok := colorful.MakeColor(c1)
	require.True(t, ok, "c1 is visible")

	cf2, ok := colorful.MakeColor(c2)
	require.True(t, ok, "c2 is visible")

	return cf1.BlendLab(cf2, 0.5).Clamped()
}

func TestBlend(t *testing.T) {
	t.Parallel()

	red := lipgloss.Color("#FF0000")
	blue := lipgloss.Color("#0000FF")

	tcs := map[string]struct {
		c1   color.Color
		c2   color.Color
		want color.Color
	}{
		"both valid returns blend": {
			c1:   red,
			c2:   blue,
			want: labMidpoint(t, red, blue),
		},
		"both nil returns nil": {
			c1:   nil,
			c2:   nil,
			want: nil,
		},
		"both NoColor returns nil": {
			c1:   lipgloss.NoColor{},
			c2:   lipgloss.NoColor{},
			want: nil,
		},
		"c1 nil returns c2": {
			c1:   nil,
			c2:   blue,
			want: blue,
		},
		"c2 nil returns c1": {
			c1:   red,
			c2:   nil,
			want: red,
		},
		"c1 NoColor returns c2": {
			c1:   lipgloss.NoColor{},
			c2:   blue,
			want: blue,
		},
		"c2 NoColor returns c1": {
			c1:   red,
			c2:   lipgloss.NoColor{},
			want: red,
		},
		"c1 invisible returns c2": {
			c1:   color.RGBA{R: 255, G: 0, B: 0, A: 0},
			c2:   blue,
			want: blue,
		},
		"c2 invisible returns c1": {
			c1:   red,
			c2:   color.RGBA{R: 0, G: 0, B: 255, A: 0},
			want: red,
		},
		"NoColor against invisible returns nil": {
			c1:   lipgloss.NoColor{},
			c2:   color.RGBA{R: 0, G: 0, B: 0, A: 0},
			want: nil,
		},
		"both invisible returns nil": {
			c1:   color.RGBA{R: 255, G: 0, B: 0, A: 0},
			c2:   color.RGBA{R: 0, G: 0, B: 255, A: 0},
			want: nil,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := colors.Blend(tc.c1, tc.c2)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestBlend_Symmetric(t *testing.T) {
	t.Parallel()

	// The order of the two colors does not change the blend. BlendLab
	// rounds differently in each direction, so the check allows a tiny
	// distance.
	tcs := map[string]struct {
		c1 color.Color
		c2 color.Color
	}{
		"red and blue": {
			c1: lipgloss.Color("#FF0000"),
			c2: lipgloss.Color("#0000FF"),
		},
		"green and yellow": {
			c1: lipgloss.Color("#00FF00"),
			c2: lipgloss.Color("#FFFF00"),
		},
		"gray and orange": {
			c1: lipgloss.Color("#808080"),
			c2: lipgloss.Color("#FFA500"),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			forward, ok := colorful.MakeColor(colors.Blend(tc.c1, tc.c2))
			require.True(t, ok)

			backward, ok := colorful.MakeColor(colors.Blend(tc.c2, tc.c1))
			require.True(t, ok)

			assert.InDelta(t, 0, forward.DistanceLab(backward), 1e-9)
		})
	}
}

func TestBlend_InGamut(t *testing.T) {
	t.Parallel()

	// The LAB midpoint of two in-gamut colors can leave the sRGB gamut, and
	// a negative channel wraps to a huge uint32 in RGBA, which lipgloss then
	// prints as an invalid SGR sequence. Blend clamps every channel.
	tcs := map[string]struct {
		c1 color.Color
		c2 color.Color
	}{
		"red and blue": {
			c1: color.RGBA{R: 255, G: 0, B: 0, A: 255},
			c2: color.RGBA{R: 0, G: 0, B: 255, A: 255},
		},
		"green and magenta": {
			c1: color.RGBA{R: 0, G: 255, B: 0, A: 255},
			c2: color.RGBA{R: 255, G: 0, B: 255, A: 255},
		},
		"yellow and blue": {
			c1: color.RGBA{R: 255, G: 255, B: 0, A: 255},
			c2: color.RGBA{R: 0, G: 0, B: 255, A: 255},
		},
		"out of gamut against NoColor": {
			// Blend still clamps a color it hands back as it is.
			c1: colorful.Color{R: 1.8, G: -0.4, B: 0.5},
			c2: lipgloss.NoColor{},
		},
		"out of gamut against nil": {
			c1: nil,
			c2: colorful.Color{R: 1.8, G: -0.4, B: 0.5},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := colors.Blend(tc.c1, tc.c2)

			r, g, b, a := got.RGBA()
			for _, ch := range []uint32{r, g, b, a} {
				assert.LessOrEqual(t, ch, uint32(0xffff))
			}

			rendered := lipgloss.NewStyle().Foreground(got).Render("x")
			assert.Regexp(t, `^\x1b\[38;2;\d{1,3};\d{1,3};\d{1,3}mx`, rendered)
		})
	}
}

func TestBlend_ClampsInputs(t *testing.T) {
	t.Parallel()

	// The blend reads each color through an RGBA roundtrip. The roundtrip
	// wraps a negative channel to a bright one and keeps a channel above 1
	// as it is. An out-of-gamut color would then pull the midpoint somewhere
	// the clamped color never reaches. Blend clamps each color before
	// mixing, the way the paths that hand one color back do.
	black := color.RGBA{A: 255}

	tcs := map[string]struct {
		bad colorful.Color
	}{
		"negative channel":  {bad: colorful.Color{R: 0.5, G: -0.2, B: 0.5}},
		"channel above one": {bad: colorful.Color{R: 1.8, G: 0.2, B: 0.5}},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, colors.Blend(tc.bad.Clamped(), black), colors.Blend(tc.bad, black))
			assert.Equal(t, colors.Blend(black, tc.bad.Clamped()), colors.Blend(black, tc.bad))
		})
	}
}

func TestBlendStyles(t *testing.T) {
	t.Parallel()

	red := lipgloss.Color("#FF0000")
	blue := lipgloss.Color("#0000FF")
	green := lipgloss.Color("#00FF00")
	yellow := lipgloss.Color("#FFFF00")

	upperTransform := strings.ToUpper
	lowerTransform := strings.ToLower

	tcs := map[string]struct {
		base          lipgloss.Style
		overlay       lipgloss.Style
		transformIn   string
		wantFg        color.Color
		wantBg        color.Color
		wantTransform string
	}{
		"blends foreground colors": {
			base:    lipgloss.NewStyle().Foreground(red),
			overlay: lipgloss.NewStyle().Foreground(blue),
			wantFg:  labMidpoint(t, red, blue),
		},
		"blends background colors": {
			base:    lipgloss.NewStyle().Background(red),
			overlay: lipgloss.NewStyle().Background(blue),
			wantBg:  labMidpoint(t, red, blue),
		},
		"only base has foreground": {
			base:    lipgloss.NewStyle().Foreground(red),
			overlay: lipgloss.NewStyle(),
			wantFg:  red,
		},
		"only overlay has foreground": {
			base:    lipgloss.NewStyle(),
			overlay: lipgloss.NewStyle().Foreground(blue),
			wantFg:  blue,
		},
		"only base has background": {
			base:    lipgloss.NewStyle().Background(red),
			overlay: lipgloss.NewStyle(),
			wantBg:  red,
		},
		"only overlay has background": {
			base:    lipgloss.NewStyle(),
			overlay: lipgloss.NewStyle().Background(blue),
			wantBg:  blue,
		},
		"composes transforms overlay wraps base": {
			base:          lipgloss.NewStyle().Transform(lowerTransform),
			overlay:       lipgloss.NewStyle().Transform(upperTransform),
			transformIn:   "Hello",
			wantTransform: "HELLO",
		},
		"only base has transform": {
			base:          lipgloss.NewStyle().Transform(upperTransform),
			overlay:       lipgloss.NewStyle(),
			transformIn:   "Hello",
			wantTransform: "HELLO",
		},
		"only overlay has transform": {
			base:          lipgloss.NewStyle(),
			overlay:       lipgloss.NewStyle().Transform(lowerTransform),
			transformIn:   "Hello",
			wantTransform: "hello",
		},
		"neither has transform": {
			base:          lipgloss.NewStyle(),
			overlay:       lipgloss.NewStyle(),
			transformIn:   "Hello",
			wantTransform: "Hello",
		},
		"full integration": {
			base:          lipgloss.NewStyle().Foreground(red).Background(green).Transform(lowerTransform),
			overlay:       lipgloss.NewStyle().Foreground(blue).Background(yellow).Transform(upperTransform),
			transformIn:   "Hello",
			wantFg:        labMidpoint(t, red, blue),
			wantBg:        labMidpoint(t, green, yellow),
			wantTransform: "HELLO",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := colors.BlendStyles(tc.base, tc.overlay)
			assert.NotNil(t, got)

			if tc.wantFg != nil {
				assert.Equal(t, tc.wantFg, got.GetForeground())
			}

			if tc.wantBg != nil {
				assert.Equal(t, tc.wantBg, got.GetBackground())
			}

			if tc.transformIn != "" {
				transform := got.GetTransform()
				if tc.wantTransform == tc.transformIn {
					assert.Nil(t, transform)
				} else {
					assert.NotNil(t, transform)
					assert.Equal(t, tc.wantTransform, transform(tc.transformIn))
				}
			}
		})
	}
}

func TestStyles_Attributes(t *testing.T) {
	t.Parallel()

	layer := map[string]func(base, overlay lipgloss.Style) lipgloss.Style{
		"blend":    colors.BlendStyles,
		"override": colors.OverrideStyles,
	}

	red := lipgloss.Color("#FF0000")

	tcs := map[string]struct {
		base    lipgloss.Style
		overlay lipgloss.Style
		want    lipgloss.Style
	}{
		"overlay attributes apply to a plain base": {
			base:    lipgloss.NewStyle(),
			overlay: lipgloss.NewStyle().Bold(true).Underline(true),
			want:    lipgloss.NewStyle().Bold(true).Underline(true),
		},
		"base attributes survive an overlay without any": {
			base:    lipgloss.NewStyle().Italic(true),
			overlay: lipgloss.NewStyle().Foreground(lipgloss.Color("#0000FF")),
			want:    lipgloss.NewStyle().Italic(true).Foreground(lipgloss.Color("#0000FF")),
		},
		"attributes from both sides combine": {
			base:    lipgloss.NewStyle().Bold(true),
			overlay: lipgloss.NewStyle().Strikethrough(true).Faint(true).Blink(true).Reverse(true),
			want: lipgloss.NewStyle().Bold(true).
				Strikethrough(true).Faint(true).Blink(true).Reverse(true),
		},
		"overlay underline style and color apply": {
			base:    lipgloss.NewStyle(),
			overlay: lipgloss.NewStyle().UnderlineStyle(lipgloss.UnderlineCurly).UnderlineColor(red),
			want:    lipgloss.NewStyle().UnderlineStyle(lipgloss.UnderlineCurly).UnderlineColor(red),
		},
		"overlay underline style replaces base's": {
			base:    lipgloss.NewStyle().UnderlineStyle(lipgloss.UnderlineDouble),
			overlay: lipgloss.NewStyle().UnderlineStyle(lipgloss.UnderlineCurly),
			want:    lipgloss.NewStyle().UnderlineStyle(lipgloss.UnderlineCurly),
		},
		"base underline color survives an overlay without one": {
			base:    lipgloss.NewStyle().Underline(true).UnderlineColor(red),
			overlay: lipgloss.NewStyle().Bold(true),
			want:    lipgloss.NewStyle().Bold(true).Underline(true).UnderlineColor(red),
		},
		"overlay hyperlink applies": {
			base:    lipgloss.NewStyle(),
			overlay: lipgloss.NewStyle().Hyperlink("https://example.com"),
			want:    lipgloss.NewStyle().Hyperlink("https://example.com"),
		},
		"overlay hyperlink replaces base's": {
			base:    lipgloss.NewStyle().Hyperlink("https://a.example", "id=a"),
			overlay: lipgloss.NewStyle().Hyperlink("https://b.example"),
			want:    lipgloss.NewStyle().Hyperlink("https://b.example"),
		},
	}

	for layerName, fn := range layer {
		for name, tc := range tcs {
			t.Run(layerName+"/"+name, func(t *testing.T) {
				t.Parallel()

				got := fn(tc.base, tc.overlay)

				assert.Equal(t, tc.want.GetBold(), got.GetBold(), "bold")
				assert.Equal(t, tc.want.GetItalic(), got.GetItalic(), "italic")
				assert.Equal(t, tc.want.GetUnderline(), got.GetUnderline(), "underline")
				assert.Equal(t, tc.want.GetUnderlineStyle(), got.GetUnderlineStyle(), "underline style")
				assert.Equal(t, tc.want.GetUnderlineColor(), got.GetUnderlineColor(), "underline color")

				wantLink, wantParams := tc.want.GetHyperlink()
				gotLink, gotParams := got.GetHyperlink()
				assert.Equal(t, wantLink, gotLink, "hyperlink")
				assert.Equal(t, wantParams, gotParams, "hyperlink params")
				assert.Equal(t, tc.want.GetStrikethrough(), got.GetStrikethrough(), "strikethrough")
				assert.Equal(t, tc.want.GetFaint(), got.GetFaint(), "faint")
				assert.Equal(t, tc.want.GetBlink(), got.GetBlink(), "blink")
				assert.Equal(t, tc.want.GetReverse(), got.GetReverse(), "reverse")
				assert.Equal(t, tc.want.Render("x"), got.Render("x"))
			})
		}
	}
}

func TestOverrideStyles(t *testing.T) {
	t.Parallel()

	red := lipgloss.Color("#FF0000")
	blue := lipgloss.Color("#0000FF")
	green := lipgloss.Color("#00FF00")
	yellow := lipgloss.Color("#FFFF00")

	upperTransform := strings.ToUpper
	lowerTransform := strings.ToLower

	tcs := map[string]struct {
		base          lipgloss.Style
		overlay       lipgloss.Style
		transformIn   string
		wantFg        color.Color
		wantBg        color.Color
		wantTransform string
	}{
		"overlay foreground replaces base": {
			base:    lipgloss.NewStyle().Foreground(red),
			overlay: lipgloss.NewStyle().Foreground(blue),
			wantFg:  blue,
		},
		"overlay background replaces base": {
			base:    lipgloss.NewStyle().Background(red),
			overlay: lipgloss.NewStyle().Background(blue),
			wantBg:  blue,
		},
		"overlay transform replaces base": {
			base:          lipgloss.NewStyle().Transform(upperTransform),
			overlay:       lipgloss.NewStyle().Transform(lowerTransform),
			transformIn:   "Hello",
			wantTransform: "hello",
		},
		"no overlay foreground keeps base": {
			base:    lipgloss.NewStyle().Foreground(red),
			overlay: lipgloss.NewStyle(),
			wantFg:  red,
		},
		"no overlay background keeps base": {
			base:    lipgloss.NewStyle().Background(green),
			overlay: lipgloss.NewStyle(),
			wantBg:  green,
		},
		"no overlay transform keeps base": {
			base:          lipgloss.NewStyle().Transform(upperTransform),
			overlay:       lipgloss.NewStyle(),
			transformIn:   "Hello",
			wantTransform: "HELLO",
		},
		"full override": {
			base:          lipgloss.NewStyle().Foreground(red).Background(green).Transform(upperTransform),
			overlay:       lipgloss.NewStyle().Foreground(blue).Background(yellow).Transform(lowerTransform),
			transformIn:   "Hello",
			wantFg:        blue,
			wantBg:        yellow,
			wantTransform: "hello",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := colors.OverrideStyles(tc.base, tc.overlay)
			assert.NotNil(t, got)

			if tc.wantFg != nil {
				assert.Equal(t, tc.wantFg, got.GetForeground())
			}

			if tc.wantBg != nil {
				assert.Equal(t, tc.wantBg, got.GetBackground())
			}

			if tc.transformIn != "" {
				transform := got.GetTransform()
				assert.NotNil(t, transform)
				assert.Equal(t, tc.wantTransform, transform(tc.transformIn))
			}
		})
	}
}
