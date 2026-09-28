package style_test

import (
	"image/color"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/style/kind"
	"go.jacobcolvin.com/niceyaml/style/theme"
)

func TestStyles_Style_EmptyStyles(t *testing.T) {
	t.Parallel()

	styles := style.Styles{}
	got := styles.Style(kind.LiteralNumberInteger)

	// The zero value returns an empty style for every kind.
	assert.Equal(t, lipgloss.Style{}, got)
}

func TestNew(t *testing.T) {
	t.Parallel()

	base := lipgloss.NewStyle().Foreground(lipgloss.Color("#ffffff"))
	red := base.Foreground(lipgloss.Color("#ff0000"))
	green := base.Foreground(lipgloss.Color("#00ff00"))

	styles := style.New(
		base,
		style.Set(kind.LiteralNumber, red),
		style.Set(kind.Comment, green),
	)

	t.Run("base style used for Text", func(t *testing.T) {
		t.Parallel()

		got := styles.Style(kind.Text)
		assert.Equal(t, lipgloss.Color("#ffffff"), got.GetForeground())
	})

	t.Run("direct override is used", func(t *testing.T) {
		t.Parallel()

		got := styles.Style(kind.LiteralNumber)
		assert.Equal(t, lipgloss.Color("#ff0000"), got.GetForeground())
	})

	t.Run("child inherits from parent override", func(t *testing.T) {
		t.Parallel()

		got := styles.Style(kind.LiteralNumberFloat)
		assert.Equal(t, lipgloss.Color("#ff0000"), got.GetForeground())
	})

	t.Run("chrome inherits from Comment", func(t *testing.T) {
		t.Parallel()

		got := styles.Style(kind.UILineNumber)
		assert.Equal(t, lipgloss.Color("#00ff00"), got.GetForeground())
	})

	t.Run("unrelated style inherits from base", func(t *testing.T) {
		t.Parallel()

		got := styles.Style(kind.NameTag)
		assert.Equal(t, lipgloss.Color("#ffffff"), got.GetForeground())
	})

	t.Run("each kind resolves to its closest set ancestor", func(t *testing.T) {
		t.Parallel()

		tests := map[string]struct {
			want color.Color
			kind kind.Kind
		}{
			"Text":                    {kind: kind.Text, want: lipgloss.Color("#ffffff")},
			"Comment":                 {kind: kind.Comment, want: lipgloss.Color("#00ff00")},
			"LiteralNumber":           {kind: kind.LiteralNumber, want: lipgloss.Color("#ff0000")},
			"LiteralNumberFloat":      {kind: kind.LiteralNumberFloat, want: lipgloss.Color("#ff0000")},
			"LiteralString":           {kind: kind.LiteralString, want: lipgloss.Color("#ffffff")},
			"NameTag":                 {kind: kind.NameTag, want: lipgloss.Color("#ffffff")},
			"Punctuation":             {kind: kind.Punctuation, want: lipgloss.Color("#ffffff")},
			"PunctuationMappingValue": {kind: kind.PunctuationMappingValue, want: lipgloss.Color("#ffffff")},
			"TextAccentDim":           {kind: kind.TextAccentDim, want: lipgloss.Color("#ffffff")},
			"TextSubtleDim":           {kind: kind.TextSubtleDim, want: lipgloss.Color("#ffffff")},
			"GenericHeading":          {kind: kind.GenericHeading, want: lipgloss.Color("#ffffff")},
			"UILineNumber":            {kind: kind.UILineNumber, want: lipgloss.Color("#00ff00")},
		}

		for name, tt := range tests {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				assert.Equal(t, tt.want, styles.Style(tt.kind).GetForeground())
			})
		}
	})
}

func TestNew_TextStyles(t *testing.T) {
	t.Parallel()

	base := lipgloss.NewStyle().Foreground(lipgloss.Color("#ffffff"))

	t.Run("inherit from Text when not explicitly set", func(t *testing.T) {
		t.Parallel()

		styles := style.New(base)

		for _, s := range []kind.Kind{kind.TextAccentDim, kind.TextSubtleDim, kind.GenericHeading} {
			got := styles.Style(s)
			assert.Equal(t, lipgloss.Color("#ffffff"), got.GetForeground(),
				"style %q should inherit foreground from Text", s)
		}
	})

	t.Run("explicit Set overrides inherited default", func(t *testing.T) {
		t.Parallel()

		accent := base.Foreground(lipgloss.Color("#ff0000"))
		subtle := base.Foreground(lipgloss.Color("#808080"))
		title := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#000000")).
			Background(lipgloss.Color("#ff0000"))

		styles := style.New(base,
			style.Set(kind.TextAccentDim, accent),
			style.Set(kind.TextSubtleDim, subtle),
			style.Set(kind.GenericHeading, title),
		)

		assert.Equal(t, lipgloss.Color("#ff0000"), styles.Style(kind.TextAccentDim).GetForeground())
		assert.Equal(t, lipgloss.Color("#808080"), styles.Style(kind.TextSubtleDim).GetForeground())
		assert.Equal(t, lipgloss.Color("#000000"), styles.Style(kind.GenericHeading).GetForeground())
		assert.Equal(t, lipgloss.Color("#ff0000"), styles.Style(kind.GenericHeading).GetBackground())
	})
}

func TestNew_Override(t *testing.T) {
	t.Parallel()

	base := lipgloss.NewStyle().Foreground(lipgloss.Color("#ffffff"))
	red := base.Foreground(lipgloss.Color("#ff0000"))
	blue := base.Foreground(lipgloss.Color("#0000ff"))

	styles := style.New(
		base,
		style.Set(kind.Text, red),
		style.Set(kind.LiteralNumber, blue),
	)

	t.Run("Text override takes precedence over base", func(t *testing.T) {
		t.Parallel()

		got := styles.Style(kind.Text)
		assert.Equal(t, lipgloss.Color("#ff0000"), got.GetForeground())
	})

	t.Run("other overrides still work", func(t *testing.T) {
		t.Parallel()

		got := styles.Style(kind.LiteralNumber)
		assert.Equal(t, lipgloss.Color("#0000ff"), got.GetForeground())
	})
}

func TestStyles_With(t *testing.T) {
	t.Parallel()

	base := lipgloss.NewStyle().Foreground(lipgloss.Color("#ffffff"))
	red := lipgloss.NewStyle().Foreground(lipgloss.Color("#ff0000"))
	green := lipgloss.NewStyle().Foreground(lipgloss.Color("#00ff00"))
	yellow := lipgloss.NewStyle().Foreground(lipgloss.Color("#ffff00"))

	original := style.New(base, style.Set(kind.Comment, green))

	// Custom style key for testing.
	const customKey kind.Kind = "customKey"

	t.Run("adds new custom style", func(t *testing.T) {
		t.Parallel()

		result := original.With(style.Set(customKey, red))

		got := result.Style(customKey)
		assert.Equal(t, lipgloss.Color("#ff0000"), got.GetForeground())
	})

	t.Run("overrides existing style", func(t *testing.T) {
		t.Parallel()

		result := original.With(style.Set(kind.Comment, yellow))

		got := result.Style(kind.Comment)
		assert.Equal(t, lipgloss.Color("#ffff00"), got.GetForeground())
	})

	t.Run("original is not modified", func(t *testing.T) {
		t.Parallel()

		_ = original.With(
			style.Set(customKey, red),
			style.Set(kind.Comment, yellow),
		)

		// The custom key is unset in the original, so it inherits the base.
		got := original.Style(customKey)
		assert.Equal(t, original.Style(kind.Text), got)

		// Comment should still be green in original.
		got = original.Style(kind.Comment)
		assert.Equal(t, lipgloss.Color("#00ff00"), got.GetForeground())
	})

	t.Run("re-resolves inheritance", func(t *testing.T) {
		t.Parallel()

		// Overriding a parent kind reaches the children that inherit it.
		result := original.With(style.Set(kind.LiteralNumber, red))

		assert.Equal(t, lipgloss.Color("#ff0000"), result.Style(kind.LiteralNumberFloat).GetForeground())
		assert.Equal(t, lipgloss.Color("#ffffff"), original.Style(kind.LiteralNumberFloat).GetForeground())
	})

	t.Run("keeps untouched categories", func(t *testing.T) {
		t.Parallel()

		result := original.With(style.Set(customKey, red))

		assert.Equal(t, original.Style(kind.Comment), result.Style(kind.Comment))
		assert.Equal(t, original.Style(kind.Text), result.Style(kind.Text))
	})

	t.Run("empty options returns an equal copy", func(t *testing.T) {
		t.Parallel()

		result := original.With()

		assert.Equal(t, lipgloss.Color("#00ff00"), result.Style(kind.Comment).GetForeground())
		assert.Equal(t, lipgloss.Color("#ffffff"), result.Style(kind.Text).GetForeground())
	})

	t.Run("zero value can be extended", func(t *testing.T) {
		t.Parallel()

		result := style.Styles{}.With(style.Set(kind.Comment, yellow))

		assert.Equal(t, lipgloss.Color("#ffff00"), result.Style(kind.Comment).GetForeground())
		assert.Equal(t, lipgloss.Style{}, result.Style(kind.Text))
	})

	t.Run("option applies to a zero value", func(t *testing.T) {
		t.Parallel()

		var zero style.Styles

		assert.NotPanics(t, func() { style.Set(kind.Comment, yellow)(&zero) })

		// The option records the style, and resolving it makes the style
		// reachable.
		assert.Equal(t, lipgloss.NewStyle(), zero.Style(kind.Comment))
		assert.Equal(t, lipgloss.Color("#ffff00"), zero.With().Style(kind.Comment).GetForeground())
	})

	t.Run("option on a copy leaves the original unchanged", func(t *testing.T) {
		t.Parallel()

		shared := style.New(base, style.Set(kind.Comment, green))
		cp := shared

		style.Set(kind.Comment, red)(&cp)

		assert.Equal(t, lipgloss.Color("#00ff00"), shared.With().Style(kind.Comment).GetForeground())
		assert.Equal(t, lipgloss.Color("#ff0000"), cp.With().Style(kind.Comment).GetForeground())
	})
}

func TestStyles_UnsetCategories(t *testing.T) {
	t.Parallel()

	styles := style.New(lipgloss.NewStyle().Foreground(lipgloss.Color("#ffffff")))

	// An unset predefined kind inherits the base, and so does an unknown
	// kind, whose parent is Text.
	assert.Equal(t, styles.Style(kind.Text), styles.Style(kind.NameTag))
	assert.Equal(t, styles.Style(kind.Text), styles.Style("never-set"))
	assert.Equal(t, lipgloss.NewStyle(), style.Styles{}.Style("never-set"))
}

func TestInherit(t *testing.T) {
	t.Parallel()

	const (
		match kind.Kind = "customMatch"
		focus kind.Kind = "customFocus"
	)

	base := lipgloss.NewStyle().Foreground(lipgloss.Color("#ffffff"))
	red := lipgloss.NewStyle().Foreground(lipgloss.Color("#ff0000"))
	green := lipgloss.NewStyle().Foreground(lipgloss.Color("#00ff00"))

	t.Run("custom kind takes the style of its parent under built-in themes", func(t *testing.T) {
		t.Parallel()

		dracula, ok := theme.Builtin().Get("dracula")
		require.True(t, ok)

		for _, th := range []theme.Theme{theme.Charm, dracula} {
			styles := th.Styles().With(style.Inherit(match, kind.GenericHighlight))

			assert.Equal(t, styles.Style(kind.GenericHighlight), styles.Style(match), th.Name)
			assert.NotEqual(t, styles.Style(kind.Text), styles.Style(match), th.Name)
		}
	})

	t.Run("explicit Set wins in either order", func(t *testing.T) {
		t.Parallel()

		first := style.New(base,
			style.Set(match, red),
			style.Inherit(match, kind.GenericHighlight),
			style.Set(kind.GenericHighlight, green),
		)
		second := style.New(base,
			style.Inherit(match, kind.GenericHighlight),
			style.Set(kind.GenericHighlight, green),
			style.Set(match, red),
		)

		assert.Equal(t, lipgloss.Color("#ff0000"), first.Style(match).GetForeground())
		assert.Equal(t, lipgloss.Color("#ff0000"), second.Style(match).GetForeground())
	})

	t.Run("later Inherit replaces the parent", func(t *testing.T) {
		t.Parallel()

		styles := style.New(base,
			style.Set(kind.GenericHighlight, green),
			style.Set(kind.TextAccent, red),
			style.Inherit(match, kind.GenericHighlight),
			style.Inherit(match, kind.TextAccent),
		)

		assert.Equal(t, lipgloss.Color("#ff0000"), styles.Style(match).GetForeground())
	})

	t.Run("custom kinds chain to a predefined ancestor", func(t *testing.T) {
		t.Parallel()

		styles := style.New(base,
			style.Set(kind.GenericHighlight, green),
			style.Inherit(focus, match),
			style.Inherit(match, kind.GenericHighlight),
		)

		assert.Equal(t, lipgloss.Color("#00ff00"), styles.Style(focus).GetForeground())
		assert.Equal(t, lipgloss.Color("#00ff00"), styles.Style(match).GetForeground())
	})

	t.Run("predefined kind moves under a new parent", func(t *testing.T) {
		t.Parallel()

		styles := style.New(base,
			style.Set(kind.Comment, green),
			style.Inherit(kind.UI, kind.Text),
		)

		assert.Equal(t, lipgloss.Color("#ffffff"), styles.Style(kind.UI).GetForeground())
		assert.Equal(t, lipgloss.Color("#ffffff"), styles.Style(kind.UILineNumber).GetForeground())
		assert.Equal(t, lipgloss.Color("#00ff00"), styles.Style(kind.Comment).GetForeground())
	})

	t.Run("cycle resolves to the base style", func(t *testing.T) {
		t.Parallel()

		styles := style.New(base,
			style.Inherit(match, focus),
			style.Inherit(focus, match),
		)

		assert.Equal(t, styles.Style(kind.Text), styles.Style(match))
		assert.Equal(t, styles.Style(kind.Text), styles.Style(focus))
	})

	t.Run("With re-resolves through the inherited parent", func(t *testing.T) {
		t.Parallel()

		original := style.New(base, style.Inherit(match, kind.GenericHighlight))
		result := original.With(style.Set(kind.GenericHighlight, red))

		assert.Equal(t, lipgloss.Color("#ff0000"), result.Style(match).GetForeground())
		assert.Equal(t, lipgloss.Color("#ffffff"), original.Style(match).GetForeground())
	})

	t.Run("option applies to a zero value", func(t *testing.T) {
		t.Parallel()

		var zero style.Styles

		assert.NotPanics(t, func() { style.Inherit(match, kind.Comment)(&zero) })

		result := zero.With(style.Set(kind.Comment, green))
		assert.Equal(t, lipgloss.Color("#00ff00"), result.Style(match).GetForeground())
	})

	t.Run("option on a copy leaves the original unchanged", func(t *testing.T) {
		t.Parallel()

		shared := style.New(base,
			style.Set(kind.Comment, green),
			style.Inherit(focus, kind.Comment),
		)
		cp := shared

		style.Inherit(match, kind.Comment)(&cp)

		assert.Equal(t, lipgloss.Color("#ffffff"), shared.With().Style(match).GetForeground())
		assert.Equal(t, lipgloss.Color("#00ff00"), cp.With().Style(match).GetForeground())
	})
}
