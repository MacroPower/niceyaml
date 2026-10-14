package colors_test

import (
	"image/color"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/lucasb-eyer/go-colorful"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/internal/colors"
)

var (
	red    = lipgloss.Color("#FF0000")
	green  = lipgloss.Color("#00FF00")
	blue   = lipgloss.Color("#0000FF")
	yellow = lipgloss.Color("#FFFF00")

	// The two functions that lay one style over another.
	layers = map[string]func(base, overlay lipgloss.Style) lipgloss.Style{
		"blend":    colors.BlendStyles,
		"override": colors.OverrideStyles,
	}
)

func TestOverride(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		base    color.Color
		overlay color.Color
		want    color.Color
	}{
		"overlay valid returns overlay": {base: red, overlay: blue, want: blue},
		"overlay outside the gamut is clamped": {
			base:    red,
			overlay: colorful.Color{R: -0.2, G: 1.4, B: 0.5},
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

func TestAbsentColor(t *testing.T) {
	t.Parallel()

	// Override and Blend share one rule for an absent color, so both hand
	// back the other color, clamped into the gamut, and nil when both
	// colors are absent.
	tcs := map[string]struct {
		base    color.Color
		overlay color.Color
		want    color.Color
	}{
		"overlay nil":       {base: red, overlay: nil, want: red},
		"overlay NoColor":   {base: red, overlay: lipgloss.NoColor{}, want: red},
		"overlay invisible": {base: red, overlay: color.RGBA{A: 0}, want: red},
		"overlay nil with base outside the gamut": {
			base:    colorful.Color{R: -0.2, G: 1.4, B: 0.5},
			overlay: nil,
			want:    colorful.Color{R: 0, G: 1, B: 0.5},
		},
		"base nil":       {base: nil, overlay: blue, want: blue},
		"base NoColor":   {base: lipgloss.NoColor{}, overlay: blue, want: blue},
		"base invisible": {base: color.RGBA{R: 255, G: 0, B: 0, A: 0}, overlay: blue, want: blue},
		"both nil":       {base: nil, overlay: nil, want: nil},
		"both NoColor":   {base: lipgloss.NoColor{}, overlay: lipgloss.NoColor{}, want: nil},
		"both invisible": {
			base:    color.RGBA{R: 255, G: 0, B: 0, A: 0},
			overlay: color.RGBA{R: 0, G: 0, B: 255, A: 0},
			want:    nil,
		},
		"overlay nil with base NoColor":       {base: lipgloss.NoColor{}, overlay: nil, want: nil},
		"overlay NoColor with base invisible": {base: color.RGBA{A: 0}, overlay: lipgloss.NoColor{}, want: nil},
		"overlay invisible with base NoColor": {base: lipgloss.NoColor{}, overlay: color.RGBA{A: 0}, want: nil},
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
// sRGB gamut and truncated to 8 bits per channel. It mixes the colors with
// go-colorful directly, so a test that compares against it catches a blend
// that drops either color.
func labMidpoint(t *testing.T, c1, c2 color.Color) color.Color {
	t.Helper()

	cf1, ok := colorful.MakeColor(c1)
	require.True(t, ok, "c1 is visible")

	cf2, ok := colorful.MakeColor(c2)
	require.True(t, ok, "c2 is visible")

	return color.RGBAModel.Convert(cf1.BlendLab(cf2, 0.5).Clamped())
}

func TestBlend(t *testing.T) {
	t.Parallel()

	assert.Equal(t, labMidpoint(t, red, blue), colors.Blend(red, blue))
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
		"red and blue":     {c1: red, c2: blue},
		"green and yellow": {c1: green, c2: yellow},
		"gray and orange":  {c1: lipgloss.Color("#808080"), c2: lipgloss.Color("#FFA500")},
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
		"out of gamut against nil": {c1: nil, c2: colorful.Color{R: 1.8, G: -0.4, B: 0.5}},
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

func TestBlend_NearBlack(t *testing.T) {
	t.Parallel()

	// A blend of black with a dark color holds channels below 1/256. The
	// SGR writer prints a 16-bit channel that small as if it were 8-bit,
	// so a 16-bit blend of black and #010120 would print as orange
	// 230;120;20.
	black := lipgloss.Color("#000000")

	tcs := map[string]struct {
		dark color.Color
		want string
	}{
		"navy":   {dark: lipgloss.Color("#010120"), want: "48;2;0;0;20"},
		"plum":   {dark: lipgloss.Color("#12011c"), want: "48;2;10;0;17"},
		"indigo": {dark: lipgloss.Color("#13004d"), want: "48;2;21;0;41"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rendered := lipgloss.NewStyle().Background(colors.Blend(black, tc.dark)).Render("x")
			assert.Equal(t, "\x1b["+tc.want+"mx\x1b[m", rendered)
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

	tcs := map[string]colorful.Color{
		"negative channel":  {R: 0.5, G: -0.2, B: 0.5},
		"channel above one": {R: 1.8, G: 0.2, B: 0.5},
	}

	for name, bad := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, colors.Blend(bad.Clamped(), black), colors.Blend(bad, black))
			assert.Equal(t, colors.Blend(black, bad.Clamped()), colors.Blend(black, bad))
		})
	}
}

func TestStyles_Colors(t *testing.T) {
	t.Parallel()

	// Each transform adds a letter of its own, so the text a style renders
	// shows which transforms ran and in which order.
	plain := lipgloss.NewStyle()
	tagBase := func(s string) string { return s + "b" }
	tagOverlay := func(s string) string { return s + "o" }

	tcs := map[string]struct {
		base         lipgloss.Style
		overlay      lipgloss.Style
		wantBlend    lipgloss.Style
		wantOverride lipgloss.Style
	}{
		"both have a foreground": {
			base:         plain.Foreground(red),
			overlay:      plain.Foreground(blue),
			wantBlend:    plain.Foreground(labMidpoint(t, red, blue)),
			wantOverride: plain.Foreground(blue),
		},
		"both have a background": {
			base:         plain.Background(red),
			overlay:      plain.Background(blue),
			wantBlend:    plain.Background(labMidpoint(t, red, blue)),
			wantOverride: plain.Background(blue),
		},
		"only base has a foreground": {
			base:         plain.Foreground(red),
			overlay:      plain,
			wantBlend:    plain.Foreground(red),
			wantOverride: plain.Foreground(red),
		},
		"only overlay has a foreground": {
			base:         plain,
			overlay:      plain.Foreground(blue),
			wantBlend:    plain.Foreground(blue),
			wantOverride: plain.Foreground(blue),
		},
		"only base has a background": {
			base:         plain.Background(red),
			overlay:      plain,
			wantBlend:    plain.Background(red),
			wantOverride: plain.Background(red),
		},
		"only overlay has a background": {
			base:         plain,
			overlay:      plain.Background(blue),
			wantBlend:    plain.Background(blue),
			wantOverride: plain.Background(blue),
		},
		"both have a transform": {
			// A blend runs the overlay's transform on what the base's
			// returns.
			base:         plain.Transform(tagBase),
			overlay:      plain.Transform(tagOverlay),
			wantBlend:    plain.Transform(func(s string) string { return s + "bo" }),
			wantOverride: plain.Transform(tagOverlay),
		},
		"only base has a transform": {
			base:         plain.Transform(tagBase),
			overlay:      plain,
			wantBlend:    plain.Transform(tagBase),
			wantOverride: plain.Transform(tagBase),
		},
		"only overlay has a transform": {
			base:         plain,
			overlay:      plain.Transform(tagOverlay),
			wantBlend:    plain.Transform(tagOverlay),
			wantOverride: plain.Transform(tagOverlay),
		},
		"both have colors and a transform": {
			base:    plain.Foreground(red).Background(green).Transform(tagBase),
			overlay: plain.Foreground(blue).Background(yellow).Transform(tagOverlay),
			wantBlend: plain.Foreground(labMidpoint(t, red, blue)).Background(labMidpoint(t, green, yellow)).
				Transform(func(s string) string { return s + "bo" }),
			wantOverride: plain.Foreground(blue).Background(yellow).Transform(tagOverlay),
		},
	}

	for name, tc := range tcs {
		for layerName, want := range map[string]lipgloss.Style{"blend": tc.wantBlend, "override": tc.wantOverride} {
			t.Run(layerName+"/"+name, func(t *testing.T) {
				t.Parallel()

				got := layers[layerName](tc.base, tc.overlay)

				assert.Equal(t, want.GetForeground(), got.GetForeground(), "foreground")
				assert.Equal(t, want.GetBackground(), got.GetBackground(), "background")

				// The rendered text carries the letters of the transforms.
				assert.Equal(t, want.Render("x"), got.Render("x"))
			})
		}
	}
}

func TestStyles_Attributes(t *testing.T) {
	t.Parallel()

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
			overlay: lipgloss.NewStyle().Foreground(blue),
			want:    lipgloss.NewStyle().Italic(true).Foreground(blue),
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

	for layerName, fn := range layers {
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

func TestStyles_UnsetColors(t *testing.T) {
	t.Parallel()

	parent := lipgloss.NewStyle().Foreground(red).Background(green).UnderlineColor(blue)

	tcs := map[string]struct {
		base    lipgloss.Style
		overlay lipgloss.Style
		want    lipgloss.Style
	}{
		"both styles empty": {
			base:    lipgloss.NewStyle(),
			overlay: lipgloss.NewStyle(),
			want:    lipgloss.NewStyle(),
		},
		"attributes without colors": {
			base:    lipgloss.NewStyle().Bold(true),
			overlay: lipgloss.NewStyle().Italic(true),
			want:    lipgloss.NewStyle().Bold(true).Italic(true),
		},
	}

	for layerName, fn := range layers {
		for name, tc := range tcs {
			t.Run(layerName+"/"+name, func(t *testing.T) {
				t.Parallel()

				got := fn(tc.base, tc.overlay)
				assert.Equal(t, tc.want, got)

				inherited := got.Inherit(parent)
				assert.Equal(t, red, inherited.GetForeground(), "foreground")
				assert.Equal(t, green, inherited.GetBackground(), "background")
				assert.Equal(t, blue, inherited.GetUnderlineColor(), "underline color")
			})
		}
	}
}
