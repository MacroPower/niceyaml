package theme

import (
	"image/color"
	"maps"
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

func TestPalette_DiffAndErrorKindsDefaultFromStatusColors(t *testing.T) {
	t.Parallel()

	// A palette that leaves the diff and error kinds out draws inserted
	// lines in OK, deleted lines in Error, and error marks as a badge on
	// Error in whichever body color reads better there.
	tcs := map[string]struct {
		tokens     map[kind.Kind]string
		kind       kind.Kind
		wantFg     color.Color
		wantBg     color.Color
		wantItalic bool
	}{
		"inserted takes ok": {
			kind:   kind.GenericInserted,
			wantFg: lipgloss.Color("#00ff00"),
			wantBg: lipgloss.Color("#000000"),
		},
		"deleted takes error": {
			kind:   kind.GenericDeleted,
			wantFg: lipgloss.Color("#ff0000"),
			wantBg: lipgloss.Color("#000000"),
		},
		"error draws a badge": {
			kind:   kind.GenericError,
			wantFg: lipgloss.Color("#000000"),
			wantBg: lipgloss.Color("#ff0000"),
		},
		"error token wins": {
			tokens: map[kind.Kind]string{kind.GenericError: "#123456"},
			kind:   kind.GenericError,
			wantFg: lipgloss.Color("#123456"),
			wantBg: lipgloss.Color("#000000"),
		},
		"defaults layer over generic": {
			tokens:     map[kind.Kind]string{kind.Generic: "italic"},
			kind:       kind.GenericDeleted,
			wantFg:     lipgloss.Color("#ff0000"),
			wantBg:     lipgloss.Color("#000000"),
			wantItalic: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := palette{
				Mode:   Dark,
				Fg:     "#ffffff",
				Bg:     "#000000",
				Accent: "#ff00ff",
				OK:     "#00ff00",
				Warn:   "#ffff00",
				Error:  "#ff0000",
				Tokens: tc.tokens,
			}

			got := p.styles().Style(tc.kind)

			assert.Equal(t, tc.wantFg, got.GetForeground())
			assert.Equal(t, tc.wantBg, got.GetBackground())
			assert.Equal(t, tc.wantItalic, got.GetItalic())
		})
	}
}

func TestPalette_TokensClearInheritedAttributes(t *testing.T) {
	t.Parallel()

	// A spec that names "noitalic" turns off the italic its kind inherits,
	// as a Pygments theme does for a child of an italic parent.
	p := palette{
		Mode:   Dark,
		Fg:     "#ffffff",
		Bg:     "#000000",
		Accent: "#ff00ff",
		OK:     "#00ff00",
		Warn:   "#ffff00",
		Error:  "#ff0000",
		Tokens: map[kind.Kind]string{
			kind.Generic:        "italic bold underline #888888",
			kind.GenericDeleted: "noitalic nobold nounderline #ff0000",
		},
	}

	s := p.styles()

	assert.True(t, s.Style(kind.Generic).GetItalic())
	assert.False(t, s.Style(kind.GenericDeleted).GetItalic())
	assert.False(t, s.Style(kind.GenericDeleted).GetBold())
	assert.False(t, s.Style(kind.GenericDeleted).GetUnderline())
	assert.Equal(t, lipgloss.Color("#ff0000"), s.Style(kind.GenericDeleted).GetForeground())
}

func TestPalette_TokensApplyAttributeKeywordsInOrder(t *testing.T) {
	t.Parallel()

	// Attribute keywords apply left to right, as [style.Parse] reads them,
	// so the last keyword for an attribute wins. Every case layers over an
	// italic Generic.
	tcs := map[string]struct {
		spec          string
		wantBold      bool
		wantItalic    bool
		wantUnderline bool
	}{
		"bold after nobold": {
			spec:       "nobold bold",
			wantBold:   true,
			wantItalic: true,
		},
		"nobold after bold": {
			spec:       "bold nobold",
			wantItalic: true,
		},
		"italic after noitalic": {
			spec:       "noitalic italic",
			wantItalic: true,
		},
		"noitalic after italic": {
			spec: "italic noitalic",
		},
		"underline after nounderline": {
			spec:          "nounderline underline",
			wantItalic:    true,
			wantUnderline: true,
		},
		"nounderline after underline": {
			spec:       "underline nounderline",
			wantItalic: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := palette{
				Mode:   Dark,
				Fg:     "#ffffff",
				Bg:     "#000000",
				Accent: "#ff00ff",
				OK:     "#00ff00",
				Warn:   "#ffff00",
				Error:  "#ff0000",
				Tokens: map[kind.Kind]string{
					kind.Generic:        "italic #888888",
					kind.GenericDeleted: tc.spec + " #ff0000",
				},
			}

			got := p.styles().Style(kind.GenericDeleted)

			assert.Equal(t, tc.wantBold, got.GetBold())
			assert.Equal(t, tc.wantItalic, got.GetItalic())
			assert.Equal(t, tc.wantUnderline, got.GetUnderline())
			assert.Equal(t, lipgloss.Color("#ff0000"), got.GetForeground())
		})
	}
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

func TestPalette_UITokensLayerOverChrome(t *testing.T) {
	t.Parallel()

	// Every case sets the comments in italics. A Tokens spec for UI or one
	// of its children layers over the upright chrome style, so none of the
	// chrome kinds turn italic.
	tcs := map[string]struct {
		tokens   map[kind.Kind]string
		kind     kind.Kind
		wantFg   color.Color
		wantBold bool
	}{
		"ui token": {
			tokens: map[kind.Kind]string{kind.UI: "#123456"},
			kind:   kind.UI,
			wantFg: lipgloss.Color("#123456"),
		},
		"ui token reaches children": {
			tokens: map[kind.Kind]string{kind.UI: "#123456"},
			kind:   kind.UILineNumber,
			wantFg: lipgloss.Color("#123456"),
		},
		"ui child color": {
			tokens: map[kind.Kind]string{kind.UILineNumber: "#abcdef"},
			kind:   kind.UILineNumber,
			wantFg: lipgloss.Color("#abcdef"),
		},
		"ui child attribute only": {
			tokens:   map[kind.Kind]string{kind.UIHunkHeader: "bold"},
			kind:     kind.UIHunkHeader,
			wantFg:   lipgloss.Color("#654321"),
			wantBold: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tokens := map[kind.Kind]string{kind.Comment: "italic #654321"}
			maps.Copy(tokens, tc.tokens)

			p := palette{
				Mode:   Dark,
				Fg:     "#ffffff",
				Bg:     "#000000",
				Accent: "#ff00ff",
				OK:     "#00ff00",
				Warn:   "#ffff00",
				Error:  "#ff0000",
				Tokens: tokens,
			}

			got := p.styles().Style(tc.kind)

			assert.Equal(t, tc.wantFg, got.GetForeground())
			assert.Equal(t, tc.wantBold, got.GetBold())
			assert.False(t, got.GetItalic())
		})
	}
}
