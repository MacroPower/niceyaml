package style

import (
	"errors"
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/internal/colors"
)

var (
	// ErrInvalidColor reports a color value that is not a valid hex color.
	// A token carrying a bg: or border: prefix or a leading # names a
	// color, so [Parse] reports a malformed one with this rather than
	// [ErrUnknownKeyword].
	ErrInvalidColor = errors.New("invalid color")
	// ErrUnknownKeyword reports a token that is neither a known keyword nor
	// a color.
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

	// Lipgloss cannot clear underline once it is set, because
	// UnsetUnderline sets it to none. Parse tracks the keywords here and
	// sets underline once at the end, so nounderline leaves the attribute
	// unset, the same as nobold and noitalic.
	underline := false

	for token := range strings.FieldsSeq(s) {
		switch strings.ToLower(token) {
		case "underline":
			underline = true

			continue

		case "nounderline":
			underline = false

			continue
		}

		var err error

		style, err = applyToken(style, token)
		if err != nil {
			return lipgloss.Style{}, err
		}
	}

	if underline {
		style = style.Underline(true)
	}

	return style, nil
}

// MustParse parses a Pygments-style string and panics when [Parse] returns
// an error.
//
// Use it for package-level variables set from a constant string.
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
// properties. Encode writes colors as lowercase hex values.
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
	if hex := colorToHex(style.GetForeground()); hex != "" {
		parts = append(parts, hex)
	}

	// Background color.
	if hex := colorToHex(style.GetBackground()); hex != "" {
		parts = append(parts, "bg:"+hex)
	}

	return strings.Join(parts, " ")
}

// applyToken applies a single token to the style. [Parse] handles the
// underline keywords itself.
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
		// Pygments compatibility. Check the color, then ignore the border.
		colorStr := token[7:]
		if !isValidColor(colorStr) {
			return style, fmt.Errorf("%w: %s", ErrInvalidColor, colorStr)
		}

		return style, nil
	}

	// Check keywords.
	switch lower {
	case "bold":
		return style.Bold(true), nil
	case "nobold":
		// Encode writes no token for an attribute that is off, so the
		// keyword clears the attribute rather than setting it to false,
		// and Parse and Encode round-trip.
		return style.UnsetBold(), nil
	case "italic":
		return style.Italic(true), nil
	case "noitalic":
		return style.UnsetItalic(), nil
	case "noinherit":
		// Pygments compatibility, ignored.
		return style, nil
	}

	// Must be a foreground color.
	if !isValidColor(token) {
		// A leading # says the token was meant as a color, so report an
		// invalid color rather than an unknown keyword.
		if strings.HasPrefix(token, "#") {
			return style, fmt.Errorf("%w: %s", ErrInvalidColor, token)
		}

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

// isColorSet checks if a color is set (not nil and not NoColor).
func isColorSet(c color.Color) bool {
	if c == nil {
		return false
	}

	_, isNoColor := c.(lipgloss.NoColor)

	return !isNoColor
}

// colorToHex converts a [color.Color] to a hex string. It returns an empty
// string for a color that is not set or fully transparent.
func colorToHex(c color.Color) string {
	if !isColorSet(c) {
		return ""
	}

	// RGBA wraps a colorful.Color channel that lies outside the sRGB gamut,
	// so a negative channel would encode bright. Clamp the color first.
	c = colors.Clamped(c)

	// RGBA premultiplies the channels by alpha, so a translucent color
	// would encode darker than it is. NRGBA holds the channels as given.
	nrgba, ok := color.NRGBAModel.Convert(c).(color.NRGBA)
	if !ok || nrgba.A == 0 {
		return ""
	}

	return fmt.Sprintf("#%02x%02x%02x", nrgba.R, nrgba.G, nrgba.B)
}
