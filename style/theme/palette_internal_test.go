package theme

import (
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml/style/kind"
)

func TestPalette_TokensOverrideDerivedKinds(t *testing.T) {
	t.Parallel()

	// A Tokens entry for a kind the palette also derives, such as TextOK,
	// is the author's word and wins over the derived value.
	p := palette{
		Mode:   Dark,
		Fg:     "#ffffff",
		Bg:     "#000000",
		Accent: "#ff00ff",
		OK:     "#00ff00",
		Warn:   "#ffff00",
		Error:  "#ff0000",
		Tokens: map[kind.Kind]string{
			kind.TextOK: "#123456",
		},
	}

	assert.Equal(t, lipgloss.Color("#123456"), p.styles().Style(kind.TextOK).GetForeground())
}

func TestPalette_UITakesCommentColorAlone(t *testing.T) {
	t.Parallel()

	// The chrome takes the foreground of the comments and none of their
	// attributes, so italic comments leave the line numbers upright.
	p := palette{
		Mode:   Dark,
		Fg:     "#ffffff",
		Bg:     "#000000",
		Accent: "#ff00ff",
		OK:     "#00ff00",
		Warn:   "#ffff00",
		Error:  "#ff0000",
		Tokens: map[kind.Kind]string{
			kind.Comment: "italic #888888",
		},
	}

	s := p.styles()

	assert.True(t, s.Style(kind.Comment).GetItalic())
	assert.Equal(t, lipgloss.Color("#888888"), s.Style(kind.UI).GetForeground())
	assert.False(t, s.Style(kind.UI).GetItalic())
	assert.Equal(t, s.Style(kind.UI), s.Style(kind.UILineNumber), "children inherit the chrome style")
	assert.Equal(t, lipgloss.Color("#000000"), s.Style(kind.UILineNumber).GetBackground())
}
