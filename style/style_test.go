package style_test

import (
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml/style"
)

func TestStyles_Style_EmptyStyles(t *testing.T) {
	t.Parallel()

	styles := style.Styles{}
	got := styles.Style(style.LiteralNumberInteger)

	// Should return an empty style when nothing is defined.
	assert.Equal(t, lipgloss.Style{}, got)
}

func TestNewStyles(t *testing.T) {
	t.Parallel()

	base := lipgloss.NewStyle().Foreground(lipgloss.Color("white"))
	red := base.Foreground(lipgloss.Color("red"))
	green := base.Foreground(lipgloss.Color("green"))

	styles := style.NewStyles(
		base,
		style.Set(style.LiteralNumber, red),
		style.Set(style.Comment, green),
	)

	t.Run("base style used for Text", func(t *testing.T) {
		t.Parallel()

		got := styles.Style(style.Text)
		assert.NotNil(t, got)
		assert.Equal(t, lipgloss.Color("white"), got.GetForeground())
	})

	t.Run("direct override is used", func(t *testing.T) {
		t.Parallel()

		got := styles.Style(style.LiteralNumber)
		assert.NotNil(t, got)
		assert.Equal(t, lipgloss.Color("red"), got.GetForeground())
	})

	t.Run("child inherits from parent override", func(t *testing.T) {
		t.Parallel()

		got := styles.Style(style.LiteralNumberFloat)
		assert.NotNil(t, got)
		assert.Equal(t, lipgloss.Color("red"), got.GetForeground())
	})

	t.Run("unrelated style inherits from base", func(t *testing.T) {
		t.Parallel()

		got := styles.Style(style.NameTag)
		assert.NotNil(t, got)
		assert.Equal(t, lipgloss.Color("white"), got.GetForeground())
	})

	t.Run("all styles are pre-computed", func(t *testing.T) {
		t.Parallel()

		// Check a sampling of styles exist directly in the map.
		stylesToCheck := []style.Kind{
			style.Text,
			style.Comment,
			style.LiteralNumber,
			style.LiteralNumberFloat,
			style.LiteralString,
			style.NameTag,
			style.Punctuation,
			style.PunctuationMappingValue,
			style.TextAccentDim,
			style.TextSubtleDim,
			style.GenericHeading,
		}

		// Every category resolves to the style of its closest set ancestor.
		set := []lipgloss.Style{
			styles.Style(style.Text),
			styles.Style(style.LiteralNumber),
			styles.Style(style.Comment),
		}

		for _, s := range stylesToCheck {
			assert.Contains(t, set, styles.Style(s), "style %q should resolve to a set ancestor", s)
		}
	})
}

func TestNewStyles_TextStyles(t *testing.T) {
	t.Parallel()

	base := lipgloss.NewStyle().Foreground(lipgloss.Color("white"))

	t.Run("inherit from Text when not explicitly set", func(t *testing.T) {
		t.Parallel()

		styles := style.NewStyles(base)

		for _, s := range []style.Kind{style.TextAccentDim, style.TextSubtleDim, style.GenericHeading} {
			got := styles.Style(s)
			assert.NotNil(t, got)
			assert.Equal(t, lipgloss.Color("white"), got.GetForeground(),
				"style %q should inherit foreground from Text", s)
		}
	})

	t.Run("explicit Set overrides inherited default", func(t *testing.T) {
		t.Parallel()

		accent := base.Foreground(lipgloss.Color("red"))
		subtle := base.Foreground(lipgloss.Color("gray"))
		title := lipgloss.NewStyle().
			Foreground(lipgloss.Color("black")).
			Background(lipgloss.Color("red"))

		styles := style.NewStyles(base,
			style.Set(style.TextAccentDim, accent),
			style.Set(style.TextSubtleDim, subtle),
			style.Set(style.GenericHeading, title),
		)

		assert.Equal(t, lipgloss.Color("red"), styles.Style(style.TextAccentDim).GetForeground())
		assert.Equal(t, lipgloss.Color("gray"), styles.Style(style.TextSubtleDim).GetForeground())
		assert.Equal(t, lipgloss.Color("black"), styles.Style(style.GenericHeading).GetForeground())
		assert.Equal(t, lipgloss.Color("red"), styles.Style(style.GenericHeading).GetBackground())
	})
}

func TestNewStyles_Override(t *testing.T) {
	t.Parallel()

	base := lipgloss.NewStyle().Foreground(lipgloss.Color("white"))
	red := base.Foreground(lipgloss.Color("red"))
	blue := base.Foreground(lipgloss.Color("blue"))

	styles := style.NewStyles(
		base,
		style.Set(style.Text, red),
		style.Set(style.LiteralNumber, blue),
	)

	t.Run("Text override takes precedence over base", func(t *testing.T) {
		t.Parallel()

		got := styles.Style(style.Text)
		assert.NotNil(t, got)
		assert.Equal(t, lipgloss.Color("red"), got.GetForeground())
	})

	t.Run("other overrides still work", func(t *testing.T) {
		t.Parallel()

		got := styles.Style(style.LiteralNumber)
		assert.NotNil(t, got)
		assert.Equal(t, lipgloss.Color("blue"), got.GetForeground())
	})
}

func TestStyles_With(t *testing.T) {
	t.Parallel()

	base := lipgloss.NewStyle().Foreground(lipgloss.Color("white"))
	red := lipgloss.NewStyle().Foreground(lipgloss.Color("red"))
	green := lipgloss.NewStyle().Foreground(lipgloss.Color("green"))
	yellow := lipgloss.NewStyle().Foreground(lipgloss.Color("yellow"))

	original := style.NewStyles(base, style.Set(style.Comment, green))

	// Custom style key for testing.
	const customKey style.Kind = "customKey"

	t.Run("adds new custom style", func(t *testing.T) {
		t.Parallel()

		result := original.With(style.Set(customKey, red))

		got := result.Style(customKey)
		assert.NotNil(t, got)
		assert.Equal(t, lipgloss.Color("red"), got.GetForeground())
	})

	t.Run("overrides existing style", func(t *testing.T) {
		t.Parallel()

		result := original.With(style.Set(style.Comment, yellow))

		got := result.Style(style.Comment)
		assert.NotNil(t, got)
		assert.Equal(t, lipgloss.Color("yellow"), got.GetForeground())
	})

	t.Run("original is not modified", func(t *testing.T) {
		t.Parallel()

		_ = original.With(
			style.Set(customKey, red),
			style.Set(style.Comment, yellow),
		)

		// Custom key should return empty style (not found) in original.
		got := original.Style(customKey)
		assert.NotNil(t, got)
		assert.Equal(t, lipgloss.Style{}, got)

		// Comment should still be green in original.
		got = original.Style(style.Comment)
		assert.NotNil(t, got)
		assert.Equal(t, lipgloss.Color("green"), got.GetForeground())
	})

	t.Run("re-resolves inheritance", func(t *testing.T) {
		t.Parallel()

		// Overriding a parent category reaches the children that inherit it.
		result := original.With(style.Set(style.LiteralNumber, red))

		assert.Equal(t, lipgloss.Color("red"), result.Style(style.LiteralNumberFloat).GetForeground())
		assert.Equal(t, lipgloss.Color("white"), original.Style(style.LiteralNumberFloat).GetForeground())
	})

	t.Run("keeps untouched categories", func(t *testing.T) {
		t.Parallel()

		result := original.With(style.Set(customKey, red))

		assert.Equal(t, original.Style(style.Comment), result.Style(style.Comment))
		assert.Equal(t, original.Style(style.Text), result.Style(style.Text))
	})

	t.Run("empty options returns an equal copy", func(t *testing.T) {
		t.Parallel()

		result := original.With()

		assert.Equal(t, lipgloss.Color("green"), result.Style(style.Comment).GetForeground())
		assert.Equal(t, lipgloss.Color("white"), result.Style(style.Text).GetForeground())
	})

	t.Run("zero value can be extended", func(t *testing.T) {
		t.Parallel()

		result := style.Styles{}.With(style.Set(style.Comment, yellow))

		assert.Equal(t, lipgloss.Color("yellow"), result.Style(style.Comment).GetForeground())
		assert.Equal(t, lipgloss.Style{}, result.Style(style.Text))
	})
}

func TestStyles_UnsetCategories(t *testing.T) {
	t.Parallel()

	styles := style.NewStyles(lipgloss.NewStyle().Foreground(lipgloss.Color("white")))

	// An unset predefined category inherits the base, and an unknown key is
	// an empty style.
	assert.Equal(t, styles.Style(style.Text), styles.Style(style.NameTag))
	assert.Equal(t, lipgloss.NewStyle(), styles.Style("never-set"))
}
