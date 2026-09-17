package colors_test

import (
	"image/color"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"

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
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := colors.Override(tc.base, tc.overlay)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestBlend(t *testing.T) {
	t.Parallel()

	red := lipgloss.Color("#FF0000")
	blue := lipgloss.Color("#0000FF")

	tcs := map[string]struct {
		c1      color.Color
		c2      color.Color
		want    color.Color
		isBlend bool
	}{
		"both valid returns blend": {
			c1:      red,
			c2:      blue,
			isBlend: true,
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
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := colors.Blend(tc.c1, tc.c2)
			if tc.isBlend {
				assert.NotNil(t, got)
				assert.NotEqual(t, tc.c1, got)
				assert.NotEqual(t, tc.c2, got)
			} else {
				assert.Equal(t, tc.want, got)
			}
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
		checkFgBlend  bool
		checkBgBlend  bool
		wantTransform string
	}{
		"blends foreground colors": {
			base:         lipgloss.NewStyle().Foreground(red),
			overlay:      lipgloss.NewStyle().Foreground(blue),
			checkFgBlend: true,
		},
		"blends background colors": {
			base:         lipgloss.NewStyle().Background(red),
			overlay:      lipgloss.NewStyle().Background(blue),
			checkBgBlend: true,
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
			checkFgBlend:  true,
			checkBgBlend:  true,
			wantTransform: "HELLO",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := colors.BlendStyles(tc.base, tc.overlay)
			assert.NotNil(t, got)

			if tc.checkFgBlend {
				fg := got.GetForeground()
				assert.NotNil(t, fg)
				assert.NotEqual(t, tc.base.GetForeground(), fg)
				assert.NotEqual(t, tc.overlay.GetForeground(), fg)
			} else if tc.wantFg != nil {
				assert.Equal(t, tc.wantFg, got.GetForeground())
			}

			if tc.checkBgBlend {
				bg := got.GetBackground()
				assert.NotNil(t, bg)
				assert.NotEqual(t, tc.base.GetBackground(), bg)
				assert.NotEqual(t, tc.overlay.GetBackground(), bg)
			} else if tc.wantBg != nil {
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
