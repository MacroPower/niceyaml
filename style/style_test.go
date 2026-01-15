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

	// Should return empty style when nothing is defined.
	assert.Equal(t, lipgloss.Style{}, *got)
}

func TestNewStyles(t *testing.T) {
	t.Parallel()

	base := lipgloss.NewStyle().Foreground(lipgloss.Color("white"))
	red := base.Foreground(lipgloss.Color("red"))
	green := base.Foreground(lipgloss.Color("green"))

	styles := style.NewStyles(base,
		style.Set(style.LiteralNumber, red),
		style.Set(style.Comment, green),
	)

	t.Run("base style used for Text", func(t *testing.T) {
		t.Parallel()

		got := styles.Style(style.Text)
		assert.Equal(t, lipgloss.Color("white"), got.GetForeground())
	})

	t.Run("direct override is used", func(t *testing.T) {
		t.Parallel()

		got := styles.Style(style.LiteralNumber)
		assert.Equal(t, lipgloss.Color("red"), got.GetForeground())
	})

	t.Run("child inherits from parent override", func(t *testing.T) {
		t.Parallel()

		got := styles.Style(style.LiteralNumberFloat)
		assert.Equal(t, lipgloss.Color("red"), got.GetForeground())
	})

	t.Run("unrelated style inherits from base", func(t *testing.T) {
		t.Parallel()

		got := styles.Style(style.NameTag)
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

	styles := style.NewStyles(base,
		style.Set(style.Text, red),
		style.Set(style.LiteralNumber, blue),
	)

	t.Run("Text override takes precedence over base", func(t *testing.T) {
		t.Parallel()

		got := styles.Style(style.Text)
		assert.Equal(t, lipgloss.Color("red"), got.GetForeground())
	})

	t.Run("other overrides still work", func(t *testing.T) {
		t.Parallel()

		got := styles.Style(style.LiteralNumber)
		assert.Equal(t, lipgloss.Color("blue"), got.GetForeground())
	})
}
