package style

import (
	"errors"
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

var (
	// ErrInvalidColor is returned when a color value is not a valid hex color.
	ErrInvalidColor = errors.New("invalid color")
	// ErrUnknownKeyword is returned when a token is not a recognized keyword or color.
	ErrUnknownKeyword = errors.New("unknown keyword")
)

// Parse parses a Pygments-style string into a [lipgloss.Style].
//
// The input string contains space-separated tokens that specify colors and
// modifiers. See the package documentation for the full format specification.
//
// Returns an error if any token is invalid.
func Parse(s string) (lipgloss.Style, error) {
	style := lipgloss.NewStyle()

	s = strings.TrimSpace(s)
	if s == "" {
		return style, nil
	}

	for token := range strings.FieldsSeq(s) {
		var err error

		style, err = applyToken(style, token)
		if err != nil {
			return lipgloss.Style{}, err
		}
	}

	return style, nil
}

// MustParse parses a Pygments-style string, panicking on error.
//
// Use this for compile-time constants where the format is known to be valid.
func MustParse(s string) lipgloss.Style {
	style, err := Parse(s)
	if err != nil {
		panic(err)
	}

	return style
}

// Encode encodes a [lipgloss.Style] to a Pygments-style string.
//
// The output contains space-separated tokens representing the style's
// properties. Colors are encoded as lowercase hex values.
//
//nolint:gocritic // Value semantics preferred for API ergonomics.
func Encode(style lipgloss.Style) string {
	var parts []string

	// Modifiers first.
	if style.GetBold() {
		parts = append(parts, "bold")
	}

	if style.GetItalic() {
		parts = append(parts, "italic")
	}

	if style.GetUnderline() {
		parts = append(parts, "underline")
	}

	// Foreground color.
	if fg := style.GetForeground(); fg != nil {
		if hex := colorToHex(fg); hex != "" {
			parts = append(parts, hex)
		}
	}

	// Background color.
	if bg := style.GetBackground(); bg != nil {
		if hex := colorToHex(bg); hex != "" {
			parts = append(parts, "bg:"+hex)
		}
	}

	return strings.Join(parts, " ")
}

// applyToken applies a single token to the style.
//
//nolint:gocritic // Value semantics preferred for API ergonomics.
func applyToken(style lipgloss.Style, token string) (lipgloss.Style, error) {
	lower := strings.ToLower(token)

	// Check prefixes.
	switch {
	case strings.HasPrefix(lower, "bg:"):
		colorStr := token[3:]
		if !isValidColor(colorStr) {
			return style, fmt.Errorf("%w: %s", ErrInvalidColor, colorStr)
		}

		return style.Background(lipgloss.Color(colorStr)), nil

	case strings.HasPrefix(lower, "border:"):
		// Pygments compatibility, ignored.
		return style, nil
	}

	// Check keywords.
	switch lower {
	case "bold":
		return style.Bold(true), nil
	case "nobold":
		return style.Bold(false), nil
	case "italic":
		return style.Italic(true), nil
	case "noitalic":
		return style.Italic(false), nil
	case "underline":
		return style.Underline(true), nil
	case "nounderline":
		return style.Underline(false), nil
	case "noinherit":
		// Pygments compatibility, ignored.
		return style, nil
	}

	// Must be a foreground color.
	if !isValidColor(token) {
		return style, fmt.Errorf("%w: %s", ErrUnknownKeyword, token)
	}

	return style.Foreground(lipgloss.Color(token)), nil
}

// isValidColor checks if a string is a valid hex color.
func isValidColor(s string) bool {
	if !strings.HasPrefix(s, "#") {
		return false
	}

	hex := s[1:]
	if len(hex) != 3 && len(hex) != 6 {
		return false
	}

	for _, c := range hex {
		if !isHexDigit(c) {
			return false
		}
	}

	return true
}

// isHexDigit checks if a rune is a valid hex digit.
func isHexDigit(c rune) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// isColorSet checks if a color is set (not NoColor).
func isColorSet(c color.Color) bool {
	_, isNoColor := c.(lipgloss.NoColor)
	return !isNoColor
}

// colorToHex converts a [color.Color] to a hex string.
// Returns empty string if the color is not set.
func colorToHex(c color.Color) string {
	if !isColorSet(c) {
		return ""
	}

	r, g, b, a := c.RGBA()
	if a == 0 {
		return ""
	}

	// Convert from 16-bit to 8-bit.
	r8 := r >> 8
	g8 := g >> 8
	b8 := b >> 8

	return fmt.Sprintf("#%02x%02x%02x", r8, g8, b8)
}
