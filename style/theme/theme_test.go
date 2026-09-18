package theme_test

import (
	"slices"
	"sync/atomic"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/style/theme"
)

func TestNew_NilBuild(t *testing.T) {
	t.Parallel()

	th := theme.New("test-nil-build", theme.Dark, nil)

	assert.Equal(t, "test-nil-build", th.Name)
	assert.Equal(t, theme.Dark, th.Mode)
	assert.Equal(t, style.Styles{}, th.Styles())

	got, ok := theme.Catalog{}.With(th).Get("test-nil-build")
	require.True(t, ok)
	assert.Equal(t, style.Styles{}, got.Styles())
}

func TestCharm(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "charm", theme.Charm.Name)
	assert.Equal(t, theme.Dark, theme.Charm.Mode)
	assert.Equal(t, style.Default(), theme.Charm.Styles())

	got, ok := theme.Builtin().Get("charm")
	require.True(t, ok)
	assert.Equal(t, theme.Charm.Name, got.Name)
}

func TestCatalog_With(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		check func(t *testing.T)
	}{
		"added theme is returned by Get": {
			check: func(t *testing.T) {
				t.Helper()

				c := theme.Builtin().With(theme.New("test-custom", theme.Dark, marked(style.Comment)))

				got, ok := c.Get("test-custom")
				require.True(t, ok)
				assert.Equal(t, "test-custom", got.Name)
				assert.Equal(t, theme.Dark, got.Mode)
				assert.True(t, isMarked(got.Styles(), style.Comment))
			},
		},
		"added theme goes on the end": {
			check: func(t *testing.T) {
				t.Helper()

				c := theme.Builtin().With(theme.New("test-listed", theme.Light, empty))

				all := c.All()
				assert.Len(t, all, theme.Builtin().Len()+1)
				assert.Equal(t, "test-listed", all[len(all)-1].Name)
				assert.Equal(t, theme.Light, all[len(all)-1].Mode)
			},
		},
		"later theme of one name wins": {
			check: func(t *testing.T) {
				t.Helper()

				c := theme.Catalog{}.With(
					theme.New("test-replace", theme.Dark, marked(style.Comment)),
					theme.New("test-replace", theme.Light, marked(style.NameTag)),
				)

				got, ok := c.Get("test-replace")
				require.True(t, ok)
				assert.Equal(t, theme.Light, got.Mode)
				assert.True(t, isMarked(got.Styles(), style.NameTag))
				assert.Equal(t, 1, c.Len())
			},
		},
		"built-in theme is replaced in place": {
			check: func(t *testing.T) {
				t.Helper()

				c := theme.Builtin().With(theme.New("vulcan", theme.Dark, marked(style.NameTag)))

				got, ok := c.Get("vulcan")
				require.True(t, ok)
				assert.True(t, isMarked(got.Styles(), style.NameTag))
				assert.Equal(t, theme.Builtin().Len(), c.Len())

				names := make([]string, 0, c.Len())
				for _, th := range c.All() {
					names = append(names, th.Name)
				}

				assert.Equal(t, 1, countOf(names, "vulcan"))
				assert.True(t, slices.IsSorted(names), "replacing keeps the position")
			},
		},
		"receiver is unchanged": {
			check: func(t *testing.T) {
				t.Helper()

				base := theme.Catalog{}.With(theme.New("test-base", theme.Dark, empty))
				_ = base.With(
					theme.New("test-extra", theme.Dark, empty),
					theme.New("test-base", theme.Light, empty),
				)

				assert.Equal(t, 1, base.Len())

				got, ok := base.Get("test-base")
				require.True(t, ok)
				assert.Equal(t, theme.Dark, got.Mode)

				_, ok = base.Get("test-extra")
				assert.False(t, ok)
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

func TestCatalog_Get(t *testing.T) {
	t.Parallel()

	t.Run("built-in theme", func(t *testing.T) {
		t.Parallel()

		got, ok := theme.Builtin().Get("dracula")
		require.True(t, ok)
		assert.Equal(t, "dracula", got.Name)
		assert.Equal(t, theme.Dark, got.Mode)
		assert.NotNil(t, got.Styles().Style(style.Text))
	})

	t.Run("unknown theme", func(t *testing.T) {
		t.Parallel()

		_, ok := theme.Builtin().Get("no-such-theme")
		assert.False(t, ok)
	})

	t.Run("zero catalog", func(t *testing.T) {
		t.Parallel()

		var c theme.Catalog

		_, ok := c.Get("dracula")
		assert.False(t, ok)
		assert.Empty(t, c.All())
		assert.Equal(t, 0, c.Len())
	})
}

func TestCatalog_Mode(t *testing.T) {
	t.Parallel()

	c := theme.Catalog{}.With(
		theme.New("a-dark", theme.Dark, empty),
		theme.New("b-light", theme.Light, empty),
		theme.New("c-dark", theme.Dark, empty),
	)

	dark := c.Mode(theme.Dark)
	assert.Equal(t, 2, dark.Len())
	assert.Equal(t, "a-dark", dark.All()[0].Name)
	assert.Equal(t, "c-dark", dark.All()[1].Name)

	_, ok := dark.Get("b-light")
	assert.False(t, ok)

	light := c.Mode(theme.Light)
	assert.Equal(t, 1, light.Len())
	assert.Equal(t, "b-light", light.All()[0].Name)

	for _, th := range theme.Builtin().Mode(theme.Dark).All() {
		assert.Equal(t, theme.Dark, th.Mode, th.Name)
	}
}

func TestBuiltin(t *testing.T) {
	t.Parallel()

	all := theme.Builtin().All()

	names := make([]string, 0, len(all))
	for _, th := range all {
		names = append(names, th.Name)
	}

	assert.Equal(t, "abap", names[0])
	assert.Contains(t, names, "charm")
	assert.True(t, slices.IsSorted(names))
	assert.Equal(t, len(names), theme.Builtin().Len())

	// Every entry is complete and builds.
	for _, th := range all {
		assert.NotEmpty(t, th.Name)
		assert.NotNil(t, th.Styles().Style(style.Text), th.Name)
	}

	// The slice is a copy.
	all[0] = theme.Theme{}

	assert.Equal(t, "abap", theme.Builtin().All()[0].Name)
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

	// Subtle text is de-emphasized text, so every built-in theme must give
	// it a foreground of its own, and the dim variant another.
	for _, th := range theme.Builtin().All() {
		t.Run(th.Name, func(t *testing.T) {
			t.Parallel()

			styles := th.Styles()
			text := styles.Style(style.Text).GetForeground()
			subtle := styles.Style(style.TextSubtle).GetForeground()
			dim := styles.Style(style.TextSubtleDim).GetForeground()

			assert.NotEqual(t, text, subtle, "TextSubtle matches Text")
			assert.NotEqual(t, subtle, dim, "TextSubtleDim matches TextSubtle")
		})
	}
}

// countOf returns how many times name appears in names.
func countOf(names []string, name string) int {
	n := 0

	for _, s := range names {
		if s == name {
			n++
		}
	}

	return n
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

func TestTheme_Style(t *testing.T) {
	t.Parallel()

	var _ printer.StyleGetter = theme.Theme{}

	assert.Equal(t, theme.Charm.Styles().Style(style.Comment), theme.Charm.Style(style.Comment))
	assert.Equal(t, lipgloss.NewStyle(), theme.Theme{}.Style(style.Comment))

	p := printer.New(printer.WithStyles(theme.Charm))
	assert.Equal(t, theme.Charm.Styles().Style(style.NameTag), p.Style(style.NameTag))
}
