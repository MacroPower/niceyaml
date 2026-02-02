package style_test

import (
	"image/color"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/style"
)

// isColorSet checks if a color is set (not NoColor).
func isColorSet(c color.Color) bool {
	_, isNoColor := c.(lipgloss.NoColor)
	return !isNoColor
}

func TestParse(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		input string
		check func(t *testing.T, s lipgloss.Style)
		err   error
	}{
		"empty string": {
			input: "",
			check: func(t *testing.T, s lipgloss.Style) {
				t.Helper()
				assert.False(t, s.GetBold())
				assert.False(t, s.GetItalic())
				assert.False(t, s.GetUnderline())
				assert.False(t, isColorSet(s.GetForeground()))
				assert.False(t, isColorSet(s.GetBackground()))
			},
		},
		"whitespace only": {
			input: "   \t\n  ",
			check: func(t *testing.T, s lipgloss.Style) {
				t.Helper()
				assert.False(t, isColorSet(s.GetForeground()))
			},
		},
		"foreground color": {
			input: "#ff0000",
			check: func(t *testing.T, s lipgloss.Style) {
				t.Helper()
				require.True(t, isColorSet(s.GetForeground()))

				r, g, b, _ := s.GetForeground().RGBA()
				assert.Equal(t, uint32(0xffff), r)
				assert.Equal(t, uint32(0), g)
				assert.Equal(t, uint32(0), b)
			},
		},
		"short foreground color": {
			input: "#f00",
			check: func(t *testing.T, s lipgloss.Style) {
				t.Helper()
				require.True(t, isColorSet(s.GetForeground()))

				r, g, b, _ := s.GetForeground().RGBA()
				assert.Equal(t, uint32(0xffff), r)
				assert.Equal(t, uint32(0), g)
				assert.Equal(t, uint32(0), b)
			},
		},
		"background color": {
			input: "bg:#00ff00",
			check: func(t *testing.T, s lipgloss.Style) {
				t.Helper()
				require.True(t, isColorSet(s.GetBackground()))

				r, g, b, _ := s.GetBackground().RGBA()
				assert.Equal(t, uint32(0), r)
				assert.Equal(t, uint32(0xffff), g)
				assert.Equal(t, uint32(0), b)
			},
		},
		"bold": {
			input: "bold",
			check: func(t *testing.T, s lipgloss.Style) {
				t.Helper()
				assert.True(t, s.GetBold())
			},
		},
		"nobold": {
			input: "nobold",
			check: func(t *testing.T, s lipgloss.Style) {
				t.Helper()
				assert.False(t, s.GetBold())
			},
		},
		"italic": {
			input: "italic",
			check: func(t *testing.T, s lipgloss.Style) {
				t.Helper()
				assert.True(t, s.GetItalic())
			},
		},
		"noitalic": {
			input: "noitalic",
			check: func(t *testing.T, s lipgloss.Style) {
				t.Helper()
				assert.False(t, s.GetItalic())
			},
		},
		"underline": {
			input: "underline",
			check: func(t *testing.T, s lipgloss.Style) {
				t.Helper()
				assert.True(t, s.GetUnderline())
			},
		},
		"nounderline": {
			input: "nounderline",
			check: func(t *testing.T, s lipgloss.Style) {
				t.Helper()
				assert.False(t, s.GetUnderline())
			},
		},
		"noinherit ignored": {
			input: "noinherit #ff0000",
			check: func(t *testing.T, s lipgloss.Style) {
				t.Helper()
				require.True(t, isColorSet(s.GetForeground()))
			},
		},
		"border ignored": {
			input: "border:#0000ff #ff0000",
			check: func(t *testing.T, s lipgloss.Style) {
				t.Helper()
				require.True(t, isColorSet(s.GetForeground()))
				assert.False(t, isColorSet(s.GetBorderTopForeground()))
			},
		},
		"combined style": {
			input: "bold italic #c678dd bg:#282c34",
			check: func(t *testing.T, s lipgloss.Style) {
				t.Helper()
				assert.True(t, s.GetBold())
				assert.True(t, s.GetItalic())
				require.True(t, isColorSet(s.GetForeground()))
				require.True(t, isColorSet(s.GetBackground()))
			},
		},
		"case insensitive keywords": {
			input: "BOLD Italic UnDeRlInE",
			check: func(t *testing.T, s lipgloss.Style) {
				t.Helper()
				assert.True(t, s.GetBold())
				assert.True(t, s.GetItalic())
				assert.True(t, s.GetUnderline())
			},
		},
		"case insensitive prefixes": {
			input: "BG:#ff0000",
			check: func(t *testing.T, s lipgloss.Style) {
				t.Helper()
				require.True(t, isColorSet(s.GetBackground()))
			},
		},
		"invalid color - no hash": {
			input: "ff0000",
			err:   style.ErrUnknownKeyword,
		},
		"invalid color - wrong length": {
			input: "#ff00",
			err:   style.ErrUnknownKeyword,
		},
		"invalid color - invalid hex": {
			input: "#gggggg",
			err:   style.ErrUnknownKeyword,
		},
		"invalid bg color": {
			input: "bg:#invalid",
			err:   style.ErrInvalidColor,
		},
		"unknown keyword": {
			input: "unknown",
			err:   style.ErrUnknownKeyword,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s, err := style.Parse(tt.input)
			if tt.err != nil {
				require.ErrorIs(t, err, tt.err)
				return
			}

			require.NoError(t, err)
			if tt.check != nil {
				tt.check(t, s)
			}
		})
	}
}

func TestMustParse(t *testing.T) {
	t.Parallel()

	t.Run("valid input", func(t *testing.T) {
		t.Parallel()

		s := style.MustParse("bold #ff0000")
		assert.True(t, s.GetBold())
		assert.True(t, isColorSet(s.GetForeground()))
	})

	t.Run("invalid input panics", func(t *testing.T) {
		t.Parallel()

		assert.Panics(t, func() {
			style.MustParse("invalid")
		})
	})
}

func TestEncode(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		style lipgloss.Style
		want  string
	}{
		"empty style": {
			style: lipgloss.NewStyle(),
			want:  "",
		},
		"bold only": {
			style: lipgloss.NewStyle().Bold(true),
			want:  "bold",
		},
		"italic only": {
			style: lipgloss.NewStyle().Italic(true),
			want:  "italic",
		},
		"underline only": {
			style: lipgloss.NewStyle().Underline(true),
			want:  "underline",
		},
		"foreground color": {
			style: lipgloss.NewStyle().Foreground(lipgloss.Color("#ff0000")),
			want:  "#ff0000",
		},
		"background color": {
			style: lipgloss.NewStyle().Background(lipgloss.Color("#00ff00")),
			want:  "bg:#00ff00",
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

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := style.Encode(tt.style)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRoundTrip(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		input string
	}{
		"simple color":     {input: "#ff0000"},
		"bold":             {input: "bold"},
		"italic":           {input: "italic"},
		"underline":        {input: "underline"},
		"bg color":         {input: "bg:#00ff00"},
		"combined":         {input: "bold italic #c678dd bg:#282c34"},
		"all features":     {input: "bold italic underline #ffffff bg:#000000"},
		"modifiers only":   {input: "bold italic underline"},
		"colors only":      {input: "#aabbcc bg:#112233"},
		"case insensitive": {input: "Bold #FF0000"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// Parse the input.
			style1, err := style.Parse(tt.input)
			require.NoError(t, err)

			// Encode back to string.
			encoded := style.Encode(style1)

			// Parse the encoded string.
			style2, err := style.Parse(encoded)
			require.NoError(t, err)

			// Encode again and compare.
			encoded2 := style.Encode(style2)
			assert.Equal(t, encoded, encoded2, "round-trip should be stable")

			// Verify properties match.
			assert.Equal(t, style1.GetBold(), style2.GetBold())
			assert.Equal(t, style1.GetItalic(), style2.GetItalic())
			assert.Equal(t, style1.GetUnderline(), style2.GetUnderline())

			if isColorSet(style1.GetForeground()) {
				require.True(t, isColorSet(style2.GetForeground()))

				r1, g1, b1, _ := style1.GetForeground().RGBA()
				r2, g2, b2, _ := style2.GetForeground().RGBA()
				assert.Equal(t, r1, r2)
				assert.Equal(t, g1, g2)
				assert.Equal(t, b1, b2)
			}

			if isColorSet(style1.GetBackground()) {
				require.True(t, isColorSet(style2.GetBackground()))

				r1, g1, b1, _ := style1.GetBackground().RGBA()
				r2, g2, b2, _ := style2.GetBackground().RGBA()
				assert.Equal(t, r1, r2)
				assert.Equal(t, g1, g2)
				assert.Equal(t, b1, b2)
			}
		})
	}
}
