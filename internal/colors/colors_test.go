package colors_test

import (
	"image/color"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"

	"github.com/macropower/niceyaml/internal/colors"
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

func TestBlendStyles(t *testing.T) {
	t.Parallel()

	red := lipgloss.Color("#FF0000")
	blue := lipgloss.Color("#0000FF")
	green := lipgloss.Color("#00FF00")
	yellow := lipgloss.Color("#FFFF00")

	upperTransform := strings.ToUpper
	lowerTransform := strings.ToLower

	tcs := map[string]struct {
		base          *lipgloss.Style
		overlay       *lipgloss.Style
		transformIn   string
		wantFg        color.Color
		wantBg        color.Color
		checkFgBlend  bool
		checkBgBlend  bool
		wantTransform string
	}{
		"blends foreground colors": {
			base:         ptr(lipgloss.NewStyle().Foreground(red)),
			overlay:      ptr(lipgloss.NewStyle().Foreground(blue)),
			checkFgBlend: true,
		},
		"blends background colors": {
			base:         ptr(lipgloss.NewStyle().Background(red)),
			overlay:      ptr(lipgloss.NewStyle().Background(blue)),
			checkBgBlend: true,
		},
		"only base has foreground": {
			base:    ptr(lipgloss.NewStyle().Foreground(red)),
			overlay: ptr(lipgloss.NewStyle()),
			wantFg:  red,
		},
		"only overlay has foreground": {
			base:    ptr(lipgloss.NewStyle()),
			overlay: ptr(lipgloss.NewStyle().Foreground(blue)),
			wantFg:  blue,
		},
		"composes transforms overlay wraps base": {
			base:          ptr(lipgloss.NewStyle().Transform(lowerTransform)),
			overlay:       ptr(lipgloss.NewStyle().Transform(upperTransform)),
			transformIn:   "Hello",
			wantTransform: "HELLO",
		},
		"only base has transform": {
			base:          ptr(lipgloss.NewStyle().Transform(upperTransform)),
			overlay:       ptr(lipgloss.NewStyle()),
			transformIn:   "Hello",
			wantTransform: "HELLO",
		},
		"only overlay has transform": {
			base:          ptr(lipgloss.NewStyle()),
			overlay:       ptr(lipgloss.NewStyle().Transform(lowerTransform)),
			transformIn:   "Hello",
			wantTransform: "hello",
		},
		"neither has transform": {
			base:          ptr(lipgloss.NewStyle()),
			overlay:       ptr(lipgloss.NewStyle()),
			transformIn:   "Hello",
			wantTransform: "Hello",
		},
		"full integration": {
			base:          ptr(lipgloss.NewStyle().Foreground(red).Background(green).Transform(lowerTransform)),
			overlay:       ptr(lipgloss.NewStyle().Foreground(blue).Background(yellow).Transform(upperTransform)),
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
		base          *lipgloss.Style
		overlay       *lipgloss.Style
		transformIn   string
		wantFg        color.Color
		wantBg        color.Color
		wantTransform string
	}{
		"overlay foreground replaces base": {
			base:    ptr(lipgloss.NewStyle().Foreground(red)),
			overlay: ptr(lipgloss.NewStyle().Foreground(blue)),
			wantFg:  blue,
		},
		"overlay background replaces base": {
			base:    ptr(lipgloss.NewStyle().Background(red)),
			overlay: ptr(lipgloss.NewStyle().Background(blue)),
			wantBg:  blue,
		},
		"overlay transform replaces base": {
			base:          ptr(lipgloss.NewStyle().Transform(upperTransform)),
			overlay:       ptr(lipgloss.NewStyle().Transform(lowerTransform)),
			transformIn:   "Hello",
			wantTransform: "hello",
		},
		"no overlay foreground keeps base": {
			base:    ptr(lipgloss.NewStyle().Foreground(red)),
			overlay: ptr(lipgloss.NewStyle()),
			wantFg:  red,
		},
		"no overlay background keeps base": {
			base:    ptr(lipgloss.NewStyle().Background(green)),
			overlay: ptr(lipgloss.NewStyle()),
			wantBg:  green,
		},
		"no overlay transform keeps base": {
			base:          ptr(lipgloss.NewStyle().Transform(upperTransform)),
			overlay:       ptr(lipgloss.NewStyle()),
			transformIn:   "Hello",
			wantTransform: "HELLO",
		},
		"full override": {
			base:          ptr(lipgloss.NewStyle().Foreground(red).Background(green).Transform(upperTransform)),
			overlay:       ptr(lipgloss.NewStyle().Foreground(blue).Background(yellow).Transform(lowerTransform)),
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

func ptr[T any](v T) *T {
	return &v
}

func TestBlender_Blend(t *testing.T) {
	t.Parallel()

	red := lipgloss.NewStyle().Foreground(lipgloss.Color("#FF0000"))
	blue := lipgloss.NewStyle().Foreground(lipgloss.Color("#0000FF"))

	t.Run("returns stable pointer for same inputs", func(t *testing.T) {
		t.Parallel()

		b := colors.NewBlender()

		result1 := b.Blend(&red, &blue, false)
		result2 := b.Blend(&red, &blue, false)

		// Same inputs should return same pointer.
		assert.Same(t, result1, result2)
	})

	t.Run("different override flag returns different results", func(t *testing.T) {
		t.Parallel()

		b := colors.NewBlender()

		blended := b.Blend(&red, &blue, false)
		overridden := b.Blend(&red, &blue, true)

		// Different override flag should return different pointers.
		assert.NotSame(t, blended, overridden)
	})

	t.Run("blended result can be used in further blends", func(t *testing.T) {
		t.Parallel()

		green := lipgloss.NewStyle().Foreground(lipgloss.Color("#00FF00"))
		b := colors.NewBlender()

		// Blend red + blue.
		result1 := b.Blend(&red, &blue, false)

		// Use blended result in another blend.
		result2 := b.Blend(result1, &green, false)

		// Should work and return stable pointer.
		result3 := b.Blend(result1, &green, false)
		assert.Same(t, result2, result3)
	})

	t.Run("blend produces blended color", func(t *testing.T) {
		t.Parallel()

		b := colors.NewBlender()

		result := b.Blend(&red, &blue, false)

		// Blended color should be different from both inputs.
		fg := result.GetForeground()
		assert.NotEqual(t, red.GetForeground(), fg)
		assert.NotEqual(t, blue.GetForeground(), fg)
	})

	t.Run("override produces overlay color", func(t *testing.T) {
		t.Parallel()

		b := colors.NewBlender()

		result := b.Blend(&red, &blue, true)

		// Override should use overlay's color.
		assert.Equal(t, blue.GetForeground(), result.GetForeground())
	})
}
