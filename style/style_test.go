package style_test

import (
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"

	"github.com/macropower/niceyaml/style"
)

func TestStyles_Style_EmptyStyles(t *testing.T) {
	t.Parallel()

	styles := style.Styles{}
	got := styles.Style(style.LiteralNumberInteger)

	// Should return an empty style when nothing is defined.
	assert.NotNil(t, got)
	assert.Equal(t, lipgloss.Style{}, *got)
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
		stylesToCheck := []style.Style{
			style.Text,
			style.Comment,
			style.LiteralNumber,
			style.LiteralNumberFloat,
			style.LiteralString,
			style.NameTag,
			style.Punctuation,
			style.PunctuationMappingValue,
		}

		for _, s := range stylesToCheck {
			_, ok := styles[s]
			assert.True(t, ok, "style %d should be pre-computed in map", s)
		}
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
	const customKey style.Style = 1

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
		assert.Equal(t, lipgloss.Style{}, *got)

		// Comment should still be green in original.
		got = original.Style(style.Comment)
		assert.NotNil(t, got)
		assert.Equal(t, lipgloss.Color("green"), got.GetForeground())
	})

	t.Run("empty options returns copy", func(t *testing.T) {
		t.Parallel()

		// Capture original Text style before calling With.
		originalTextStyle := original[style.Text]

		result := original.With()

		// Should be equal in content.
		assert.Len(t, result, len(original))

		// Modify the copy.
		result[style.Text] = red

		// Original map should be unaffected - still has the original Text style.
		assert.Equal(t, originalTextStyle, original[style.Text])

		// Result should have the new value.
		assert.Equal(t, lipgloss.Color("red"), result[style.Text].GetForeground())
	})
}
