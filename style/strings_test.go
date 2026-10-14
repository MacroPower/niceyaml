package style_test

import (
	"image/color"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/lucasb-eyer/go-colorful"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/style"
)

// styleAttrs holds the attributes of a [lipgloss.Style] that [style.Parse]
// reads from its input. A color is a hex string, or empty when unset.
type styleAttrs struct {
	Fg, Bg, Border          string
	Bold, Italic, Underline bool
}

// attrsOf returns the [styleAttrs] of s.
//
//nolint:gocritic // hugeParam: value semantics match lipgloss.
func attrsOf(s lipgloss.Style) styleAttrs {
	hex := func(c color.Color) string {
		if _, unset := c.(lipgloss.NoColor); unset {
			return ""
		}

		return yamltest.ColorHex(c)
	}

	return styleAttrs{
		Fg:        hex(s.GetForeground()),
		Bg:        hex(s.GetBackground()),
		Border:    hex(s.GetBorderTopForeground()),
		Bold:      s.GetBold(),
		Italic:    s.GetItalic(),
		Underline: s.GetUnderline(),
	}
}

func TestParse(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		err   error
		input string
		want  styleAttrs
	}{
		"empty string":           {input: "", want: styleAttrs{}},
		"whitespace only":        {input: "   \t\n  ", want: styleAttrs{}},
		"foreground color":       {input: "#ff0000", want: styleAttrs{Fg: "#ff0000"}},
		"short foreground color": {input: "#f00", want: styleAttrs{Fg: "#ff0000"}},
		"background color":       {input: "bg:#00ff00", want: styleAttrs{Bg: "#00ff00"}},
		"bold":                   {input: "bold", want: styleAttrs{Bold: true}},
		"italic":                 {input: "italic", want: styleAttrs{Italic: true}},
		"underline":              {input: "underline", want: styleAttrs{Underline: true}},
		"noinherit ignored":      {input: "noinherit #ff0000", want: styleAttrs{Fg: "#ff0000"}},
		"border ignored":         {input: "border:#0000ff #ff0000", want: styleAttrs{Fg: "#ff0000"}},
		"combined style": {
			input: "bold italic #c678dd bg:#282c34",
			want:  styleAttrs{Bold: true, Italic: true, Fg: "#c678dd", Bg: "#282c34"},
		},
		"case insensitive keywords": {
			input: "BOLD Italic UnDeRlInE",
			want:  styleAttrs{Bold: true, Italic: true, Underline: true},
		},
		"case insensitive prefixes":    {input: "BG:#ff0000", want: styleAttrs{Bg: "#ff0000"}},
		"invalid color - no hash":      {input: "ff0000", err: style.ErrUnknownKeyword},
		"invalid color - wrong length": {input: "#ff00", err: style.ErrInvalidColor},
		"invalid color - invalid hex":  {input: "#gggggg", err: style.ErrInvalidColor},
		"invalid bg color":             {input: "bg:#invalid", err: style.ErrInvalidColor},
		"invalid border color":         {input: "border:garbage", err: style.ErrInvalidColor},
		"empty border color":           {input: "border:", err: style.ErrInvalidColor},
		"unknown keyword":              {input: "unknown", err: style.ErrUnknownKeyword},
		"uppercase hex color":          {input: "Bold #FF0000", want: styleAttrs{Bold: true, Fg: "#ff0000"}},
		"modifiers only": {
			input: "bold italic underline",
			want:  styleAttrs{Bold: true, Italic: true, Underline: true},
		},
		"colors only": {input: "#aabbcc bg:#112233", want: styleAttrs{Fg: "#aabbcc", Bg: "#112233"}},
		"all features": {
			input: "bold italic underline #ffffff bg:#000000",
			want:  styleAttrs{Bold: true, Italic: true, Underline: true, Fg: "#ffffff", Bg: "#000000"},
		},
		"cleared modifier": {input: "bold nobold #ff0000", want: styleAttrs{Fg: "#ff0000"}},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := style.Parse(tc.input)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
				assert.Panics(t, func() { style.MustParse(tc.input) })

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, attrsOf(got))
			assert.Equal(t, tc.want, attrsOf(style.MustParse(tc.input)))

			// The encoded style parses back to the same attributes and
			// encodes to the same string again.
			encoded := style.Encode(got)

			reparsed, err := style.Parse(encoded)
			require.NoError(t, err)

			assert.Equal(t, tc.want, attrsOf(reparsed))
			assert.Equal(t, encoded, style.Encode(reparsed), "round-trip should be stable")
		})
	}
}

func TestParse_NoKeywordsLeaveAttributeUnset(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input  string
		parent lipgloss.Style
		get    func(lipgloss.Style) bool
	}{
		"nobold": {
			input:  "nobold",
			parent: lipgloss.NewStyle().Bold(true),
			get:    lipgloss.Style.GetBold,
		},
		"bold nobold": {
			input:  "bold nobold",
			parent: lipgloss.NewStyle().Bold(true),
			get:    lipgloss.Style.GetBold,
		},
		"noitalic": {
			input:  "noitalic",
			parent: lipgloss.NewStyle().Italic(true),
			get:    lipgloss.Style.GetItalic,
		},
		"nounderline": {
			input:  "nounderline",
			parent: lipgloss.NewStyle().Underline(true),
			get:    lipgloss.Style.GetUnderline,
		},
		"underline nounderline": {
			input:  "underline nounderline",
			parent: lipgloss.NewStyle().Underline(true),
			get:    lipgloss.Style.GetUnderline,
		},
		"mixed case nounderline": {
			input:  "UNDERLINE NoUnderline",
			parent: lipgloss.NewStyle().Underline(true),
			get:    lipgloss.Style.GetUnderline,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s, err := style.Parse(tc.input)
			require.NoError(t, err)
			assert.Equal(t, styleAttrs{}, attrsOf(s))

			// An unset attribute lets Inherit take the parent's value.
			assert.True(t, tc.get(s.Inherit(tc.parent)))
		})
	}
}

func TestEncode(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		style lipgloss.Style
		want  string
	}{
		"empty style":    {style: lipgloss.NewStyle(), want: ""},
		"bold only":      {style: lipgloss.NewStyle().Bold(true), want: "bold"},
		"italic only":    {style: lipgloss.NewStyle().Italic(true), want: "italic"},
		"underline only": {style: lipgloss.NewStyle().Underline(true), want: "underline"},
		"foreground color": {
			style: lipgloss.NewStyle().Foreground(lipgloss.Color("#ff0000")),
			want:  "#ff0000",
		},
		"background color": {
			style: lipgloss.NewStyle().Background(lipgloss.Color("#00ff00")),
			want:  "bg:#00ff00",
		},
		"translucent color keeps its channels": {
			style: lipgloss.NewStyle().Foreground(color.NRGBA{R: 255, G: 0, B: 0, A: 128}),
			want:  "#ff0000",
		},
		"transparent color is unset": {
			style: lipgloss.NewStyle().Foreground(color.NRGBA{R: 255, A: 0}),
			want:  "",
		},
		"out-of-gamut color is clamped": {
			style: lipgloss.NewStyle().Foreground(colorful.Color{R: -0.2, G: 0.5, B: 1.3}),
			want:  "#0080ff",
		},
		"out-of-gamut background is clamped": {
			style: lipgloss.NewStyle().Background(colorful.Color{R: -0.2, G: 0.5, B: 1.3}),
			want:  "bg:#0080ff",
		},
		"combined style": {
			style: lipgloss.NewStyle().
				Bold(true).
				Italic(true).
				Foreground(lipgloss.Color("#c678dd")).
				Background(lipgloss.Color("#282c34")),
			want: "bold italic #c678dd bg:#282c34",
		},
		"all modifiers": {
			style: lipgloss.NewStyle().
				Bold(true).
				Italic(true).
				Underline(true),
			want: "bold italic underline",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, style.Encode(tc.style))
		})
	}
}
