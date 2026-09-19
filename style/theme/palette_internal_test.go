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
