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
		base     color.Color
		overlay  color.Color
		expected color.Color
	}{
		"overlay valid returns overlay": {
			base:     red,
			overlay:  blue,
			expected: blue,
		},
		"overlay nil returns base": {
			base:     red,
			overlay:  nil,
			expected: red,
		},
		"overlay NoColor returns base": {
			base:     red,
			overlay:  lipgloss.NoColor{},
			expected: red,
		},
		"overlay invisible returns base": {
			base:     red,
			overlay:  color.RGBA{R: 255, G: 0, B: 0, A: 0},
			expected: red,
		},
		"both nil returns nil": {
			base:     nil,
			overlay:  nil,
			expected: nil,
		},
		"base nil overlay valid returns overlay": {
			base:     nil,
			overlay:  blue,
			expected: blue,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := colors.Override(tc.base, tc.overlay)
			assert.Equal(t, tc.expected, got)
		})
	}
}

func TestBlend(t *testing.T) {
	t.Parallel()

	red := lipgloss.Color("#FF0000")
	blue := lipgloss.Color("#0000FF")

	tcs := map[string]struct {
		c1       color.Color
		c2       color.Color
		expected color.Color
		isBlend  bool
	}{
		"both valid returns blend": {
			c1:      red,
			c2:      blue,
			isBlend: true,
		},
		"both nil returns nil": {
			c1:       nil,
			c2:       nil,
			expected: nil,
		},
		"both NoColor returns nil": {
			c1:       lipgloss.NoColor{},
			c2:       lipgloss.NoColor{},
			expected: nil,
		},
		"c1 nil returns c2": {
			c1:       nil,
			c2:       blue,
			expected: blue,
		},
		"c2 nil returns c1": {
			c1:       red,
			c2:       nil,
			expected: red,
		},
		"c1 NoColor returns c2": {
			c1:       lipgloss.NoColor{},
			c2:       blue,
			expected: blue,
		},
		"c2 NoColor returns c1": {
			c1:       red,
			c2:       lipgloss.NoColor{},
			expected: red,
		},
		"c1 invisible returns c2": {
			c1:       color.RGBA{R: 255, G: 0, B: 0, A: 0},
			c2:       blue,
			expected: blue,
		},
		"c2 invisible returns c1": {
			c1:       red,
			c2:       color.RGBA{R: 0, G: 0, B: 255, A: 0},
			expected: red,
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
				assert.Equal(t, tc.expected, got)
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
		base            *lipgloss.Style
		overlay         *lipgloss.Style
		expectedFg      color.Color
		expectedBg      color.Color
		transformInput  string
		transformOutput string
		checkFgBlend    bool
		checkBgBlend    bool
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
			base:       ptr(lipgloss.NewStyle().Foreground(red)),
			overlay:    ptr(lipgloss.NewStyle()),
			expectedFg: red,
		},
		"only overlay has foreground": {
			base:       ptr(lipgloss.NewStyle()),
			overlay:    ptr(lipgloss.NewStyle().Foreground(blue)),
			expectedFg: blue,
		},
		"composes transforms overlay wraps base": {
			base:            ptr(lipgloss.NewStyle().Transform(lowerTransform)),
			overlay:         ptr(lipgloss.NewStyle().Transform(upperTransform)),
			transformInput:  "Hello",
			transformOutput: "HELLO",
		},
		"only base has transform": {
			base:            ptr(lipgloss.NewStyle().Transform(upperTransform)),
			overlay:         ptr(lipgloss.NewStyle()),
			transformInput:  "Hello",
			transformOutput: "HELLO",
		},
		"only overlay has transform": {
			base:            ptr(lipgloss.NewStyle()),
			overlay:         ptr(lipgloss.NewStyle().Transform(lowerTransform)),
			transformInput:  "Hello",
			transformOutput: "hello",
		},
		"neither has transform": {
			base:            ptr(lipgloss.NewStyle()),
			overlay:         ptr(lipgloss.NewStyle()),
			transformInput:  "Hello",
			transformOutput: "Hello",
		},
		"full integration": {
			base:            ptr(lipgloss.NewStyle().Foreground(red).Background(green).Transform(lowerTransform)),
			overlay:         ptr(lipgloss.NewStyle().Foreground(blue).Background(yellow).Transform(upperTransform)),
			checkFgBlend:    true,
			checkBgBlend:    true,
			transformInput:  "Hello",
			transformOutput: "HELLO",
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
			} else if tc.expectedFg != nil {
				assert.Equal(t, tc.expectedFg, got.GetForeground())
			}

			if tc.checkBgBlend {
				bg := got.GetBackground()
				assert.NotNil(t, bg)
				assert.NotEqual(t, tc.base.GetBackground(), bg)
				assert.NotEqual(t, tc.overlay.GetBackground(), bg)
			} else if tc.expectedBg != nil {
				assert.Equal(t, tc.expectedBg, got.GetBackground())
			}

			if tc.transformInput != "" {
				transform := got.GetTransform()
				if tc.transformOutput == tc.transformInput {
					assert.Nil(t, transform)
				} else {
					assert.NotNil(t, transform)
					assert.Equal(t, tc.transformOutput, transform(tc.transformInput))
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
		base            *lipgloss.Style
		overlay         *lipgloss.Style
		expectedFg      color.Color
		expectedBg      color.Color
		transformInput  string
		transformOutput string
	}{
		"overlay foreground replaces base": {
			base:       ptr(lipgloss.NewStyle().Foreground(red)),
			overlay:    ptr(lipgloss.NewStyle().Foreground(blue)),
			expectedFg: blue,
		},
		"overlay background replaces base": {
			base:       ptr(lipgloss.NewStyle().Background(red)),
			overlay:    ptr(lipgloss.NewStyle().Background(blue)),
			expectedBg: blue,
		},
		"overlay transform replaces base": {
			base:            ptr(lipgloss.NewStyle().Transform(upperTransform)),
			overlay:         ptr(lipgloss.NewStyle().Transform(lowerTransform)),
			transformInput:  "Hello",
			transformOutput: "hello",
		},
		"no overlay foreground keeps base": {
			base:       ptr(lipgloss.NewStyle().Foreground(red)),
			overlay:    ptr(lipgloss.NewStyle()),
			expectedFg: red,
		},
		"no overlay background keeps base": {
			base:       ptr(lipgloss.NewStyle().Background(green)),
			overlay:    ptr(lipgloss.NewStyle()),
			expectedBg: green,
		},
		"no overlay transform keeps base": {
			base:            ptr(lipgloss.NewStyle().Transform(upperTransform)),
			overlay:         ptr(lipgloss.NewStyle()),
			transformInput:  "Hello",
			transformOutput: "HELLO",
		},
		"full override": {
			base:            ptr(lipgloss.NewStyle().Foreground(red).Background(green).Transform(upperTransform)),
			overlay:         ptr(lipgloss.NewStyle().Foreground(blue).Background(yellow).Transform(lowerTransform)),
			expectedFg:      blue,
			expectedBg:      yellow,
			transformInput:  "Hello",
			transformOutput: "hello",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := colors.OverrideStyles(tc.base, tc.overlay)
			assert.NotNil(t, got)

			if tc.expectedFg != nil {
				assert.Equal(t, tc.expectedFg, got.GetForeground())
			}

			if tc.expectedBg != nil {
				assert.Equal(t, tc.expectedBg, got.GetBackground())
			}

			if tc.transformInput != "" {
				transform := got.GetTransform()
				assert.NotNil(t, transform)
				assert.Equal(t, tc.transformOutput, transform(tc.transformInput))
			}
		})
	}
}

func ptr[T any](v T) *T {
	return &v
}
