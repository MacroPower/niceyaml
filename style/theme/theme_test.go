package theme_test

import (
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/style/theme"
)

func TestNew_NilBuild(t *testing.T) {
	t.Parallel()

	th := theme.New("test-nil-build", theme.Dark, nil)

	assert.Equal(t, "test-nil-build", th.Name)
	assert.Equal(t, theme.Dark, th.Mode)
	assert.Equal(t, style.Styles{}, th.Styles())

	require.NoError(t, theme.Register(th))

	got, ok := theme.Get("test-nil-build")
	require.True(t, ok)
	assert.Equal(t, style.Styles{}, got.Styles())
}

func TestRegister(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		check func(t *testing.T)
	}{
		"registered theme is returned by Get": {
			check: func(t *testing.T) {
				t.Helper()

				err := theme.Register(theme.New("test-custom", theme.Dark, marked(style.Comment)))
				require.NoError(t, err)

				got, ok := theme.Get("test-custom")
				require.True(t, ok)
				assert.Equal(t, "test-custom", got.Name)
				assert.Equal(t, theme.Dark, got.Mode)
				assert.True(t, isMarked(got.Styles(), style.Comment))
			},
		},
		"registered theme appears in All with its mode": {
			check: func(t *testing.T) {
				t.Helper()

				err := theme.Register(theme.New("test-listed", theme.Light, empty))
				require.NoError(t, err)

				var got theme.Theme

				for _, th := range theme.All() {
					if th.Name == "test-listed" {
						got = th
					}
				}

				assert.Equal(t, "test-listed", got.Name)
				assert.Equal(t, theme.Light, got.Mode)
			},
		},
		"second registration of a custom name is rejected": {
			check: func(t *testing.T) {
				t.Helper()

				err := theme.Register(theme.New("test-replace", theme.Dark, marked(style.Comment)))
				require.NoError(t, err)

				err = theme.Register(theme.New("test-replace", theme.Light, marked(style.NameTag)))
				require.ErrorIs(t, err, theme.ErrRegistered)

				// The first registration stands.
				got, ok := theme.Get("test-replace")
				require.True(t, ok)
				assert.Equal(t, theme.Dark, got.Mode)
				assert.True(t, isMarked(got.Styles(), style.Comment))
				assert.False(t, isMarked(got.Styles(), style.NameTag))
			},
		},
		"built-in name is rejected": {
			check: func(t *testing.T) {
				t.Helper()

				err := theme.Register(theme.New("vulcan", theme.Dark, marked(style.NameTag)))
				require.ErrorIs(t, err, theme.ErrRegistered)

				got, ok := theme.Get("vulcan")
				require.True(t, ok)
				assert.False(t, isMarked(got.Styles(), style.NameTag))

				// No second entry is added.
				count := 0

				for _, th := range theme.All() {
					if th.Name == "vulcan" {
						count++
					}
				}

				assert.Equal(t, 1, count)
			},
		},
	}

	for name, tt := range tests {
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

			// Names repeat across goroutines, so a rejection is the only
			// acceptable error.
			err := theme.Register(theme.New(name, theme.Dark, empty))
			if err != nil && !errors.Is(err, theme.ErrRegistered) {
				t.Errorf("Register(%q) = %v", name, err)
			}

			_, ok := theme.Get(name)
			assert.True(t, ok)

			theme.All()
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
		assert.NotNil(t, got.Styles().Style(style.Text))
	})

	t.Run("unknown theme", func(t *testing.T) {
		t.Parallel()

		_, ok := theme.Get("no-such-theme")
		assert.False(t, ok)
	})
}

func TestAll(t *testing.T) {
	t.Parallel()

	err := theme.Register(theme.New("test-all-custom", theme.Dark, empty))
	require.NoError(t, err)

	all := theme.All()

	names := make([]string, 0, len(all))
	for _, th := range all {
		names = append(names, th.Name)
	}

	// Built-in themes lead and stay sorted; custom themes follow.
	assert.Equal(t, "abap", names[0])
	assert.Contains(t, names, "charm")
	assert.Contains(t, names, "test-all-custom")
	assert.Greater(t, slices.Index(names, "test-all-custom"), slices.Index(names, "xcode-dark"))

	builtin := names[:slices.Index(names, "xcode-dark")+1]
	assert.True(t, slices.IsSorted(builtin))

	// Every entry is complete and builds.
	for _, th := range all {
		assert.NotEmpty(t, th.Name)
		assert.NotNil(t, th.Styles().Style(style.Text), th.Name)
	}
}

func TestThemeStyles(t *testing.T) {
	t.Parallel()

	t.Run("builds once and shares across copies", func(t *testing.T) {
		t.Parallel()

		var calls atomic.Int32

		th := theme.New("test-memo", theme.Dark, func() style.Styles {
			calls.Add(1)

			return marked(style.Comment)()
		})

		first := th.Styles()

		duplicate := th
		second := duplicate.Styles()

		assert.Equal(t, int32(1), calls.Load())
		assert.True(t, isMarked(first, style.Comment))
		assert.True(t, isMarked(second, style.Comment))
	})

	t.Run("zero value", func(t *testing.T) {
		t.Parallel()

		var th theme.Theme

		assert.NotNil(t, th.Styles().Style(style.Text))
	})
}

func TestPalette_SubtleTextDiffersFromText(t *testing.T) {
	t.Parallel()

	// Subtle text is de-emphasized text, so every registered theme must
	// give it a foreground of its own, and the dim variant another.
	for _, th := range theme.All() {
		t.Run(th.Name, func(t *testing.T) {
			t.Parallel()

			styles := th.Styles()
			text := styles.Style(style.Text).GetForeground()
			subtle := styles.Style(style.TextSubtle).GetForeground()
			dim := styles.Style(style.TextSubtleDim).GetForeground()

			// Other tests register dummy themes that set no colors.
			if _, ok := subtle.(lipgloss.NoColor); ok {
				t.Skip("theme sets no subtle text color")
			}

			assert.NotEqual(t, text, subtle, "TextSubtle matches Text")
			assert.NotEqual(t, subtle, dim, "TextSubtleDim matches TextSubtle")
		})
	}
}

// marker is the foreground that marked gives one category, so a test can tell
// which dummy theme a lookup returned.
var marker = lipgloss.Color("#123456")

// empty builds a theme with no categories set.
func empty() style.Styles {
	return style.NewStyles(lipgloss.NewStyle())
}

// marked returns a builder for a theme whose only set category is s.
func marked(s style.Kind) func() style.Styles {
	return func() style.Styles {
		return style.NewStyles(lipgloss.NewStyle(), style.Set(s, lipgloss.NewStyle().Foreground(marker)))
	}
}

// isMarked reports whether s carries the marker in styles.
func isMarked(styles style.Styles, s style.Kind) bool {
	return styles.Style(s).GetForeground() == marker
}
