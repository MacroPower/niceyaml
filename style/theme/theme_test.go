package theme_test

import (
	"slices"
	"sync"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/style/theme"
)

func TestRegister(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		setup func()
		check func(t *testing.T)
	}{
		"retrieve registered theme via Styles": {
			setup: func() {
				theme.Register("test-custom", func() style.Styles {
					return marked(style.Comment)
				}, theme.Dark)
			},
			check: func(t *testing.T) {
				t.Helper()

				got, ok := theme.Styles("test-custom")
				require.True(t, ok)
				assert.True(t, isMarked(got, style.Comment))
			},
		},
		"registered theme appears in List": {
			setup: func() {
				theme.Register("test-listed", func() style.Styles {
					return style.NewStyles(lipgloss.NewStyle())
				}, theme.Dark)
			},
			check: func(t *testing.T) {
				t.Helper()

				names := theme.List(theme.Dark)
				assert.True(t, slices.Contains(names, "test-listed"))
			},
		},
		"replace existing custom theme": {
			setup: func() {
				theme.Register("test-replace", func() style.Styles {
					return marked(style.Comment)
				}, theme.Dark)
				theme.Register("test-replace", func() style.Styles {
					return marked(style.NameTag)
				}, theme.Dark)
			},
			check: func(t *testing.T) {
				t.Helper()

				got, ok := theme.Styles("test-replace")
				require.True(t, ok)
				assert.True(t, isMarked(got, style.NameTag))
				assert.False(t, isMarked(got, style.Comment))
			},
		},
		"dark theme not in light list": {
			setup: func() {
				theme.Register("test-dark-only", func() style.Styles {
					return style.NewStyles(lipgloss.NewStyle())
				}, theme.Dark)
			},
			check: func(t *testing.T) {
				t.Helper()

				dark := theme.List(theme.Dark)
				light := theme.List(theme.Light)

				assert.True(t, slices.Contains(dark, "test-dark-only"))
				assert.False(t, slices.Contains(light, "test-dark-only"))
			},
		},
	}

	for name, tt := range tests {
		tt.setup()
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tt.check(t)
		})
	}
}

func TestRegisterConcurrent(t *testing.T) {
	t.Parallel()

	var wg sync.WaitGroup

	for i := range 50 {
		wg.Go(func() {
			name := "concurrent-" + string(rune('a'+i%26))

			theme.Register(name, func() style.Styles {
				return style.NewStyles(lipgloss.NewStyle())
			}, theme.Dark)
			theme.Styles(name)
			theme.List(theme.Dark)
		})
	}

	wg.Wait()
}

func TestGet(t *testing.T) {
	t.Parallel()

	t.Run("built-in theme", func(t *testing.T) {
		t.Parallel()

		got, ok := theme.Get("dracula")
		require.True(t, ok)
		assert.Equal(t, "dracula", got.Name)
		assert.Equal(t, theme.Dark, got.Mode)
		assert.NotNil(t, got.Styles)
	})

	t.Run("unknown theme", func(t *testing.T) {
		t.Parallel()

		_, ok := theme.Get("no-such-theme")
		assert.False(t, ok)
	})

	t.Run("custom theme takes precedence", func(t *testing.T) {
		t.Parallel()

		theme.Register("test-get-override", func() style.Styles {
			return marked(style.Comment)
		}, theme.Light)

		got, ok := theme.Get("test-get-override")
		require.True(t, ok)
		assert.Equal(t, theme.Light, got.Mode)
	})
}

func TestAll(t *testing.T) {
	t.Parallel()

	theme.Register("test-all-custom", func() style.Styles {
		return style.NewStyles(lipgloss.NewStyle())
	}, theme.Dark)

	all := theme.All()

	names := make([]string, 0, len(all))
	for _, th := range all {
		names = append(names, th.Name)
	}

	// Built-in themes lead and stay sorted; custom themes follow.
	assert.Equal(t, "abap", names[0])
	assert.Contains(t, names, "test-all-custom")
	assert.Greater(t, slices.Index(names, "test-all-custom"), slices.Index(names, "xcode-dark"))

	// Every entry is complete.
	for _, th := range all {
		assert.NotEmpty(t, th.Name)
		assert.NotNil(t, th.Styles, th.Name)
	}

	// List filters All by mode.
	for _, name := range theme.List(theme.Dark) {
		th, ok := theme.Get(name)
		require.True(t, ok, name)
		assert.Equal(t, theme.Dark, th.Mode, name)
	}
}

func TestAll_ReplacesBuiltIn(t *testing.T) {
	t.Parallel()

	// Keep the built-in mode so the parallel mode assertions in TestAll hold.
	theme.Register("vulcan", func() style.Styles {
		return marked(style.NameTag)
	}, theme.Dark)

	all := theme.All()

	names := make([]string, 0, len(all))
	count := 0

	for _, th := range all {
		names = append(names, th.Name)

		if th.Name == "vulcan" {
			count++
		}
	}

	// The custom theme takes the built-in's place instead of adding a second
	// entry under the same name.
	assert.Equal(t, 1, count)
	assert.Less(t, slices.Index(names, "vulcan"), slices.Index(names, "xcode-dark"))

	styles, ok := theme.Styles("vulcan")
	require.True(t, ok)
	assert.True(t, isMarked(styles, style.NameTag))
	assert.False(t, isMarked(styles, style.Comment))
}

// marker is the foreground that marked gives one category, so a test can tell
// which dummy theme a lookup returned.
var marker = lipgloss.Color("#123456")

// marked returns a theme whose only set category is s.
func marked(s style.Style) style.Styles {
	return style.NewStyles(lipgloss.NewStyle(), style.Set(s, lipgloss.NewStyle().Foreground(marker)))
}

// isMarked reports whether s carries the marker in styles.
func isMarked(styles style.Styles, s style.Style) bool {
	return styles.Style(s).GetForeground() == marker
}
