package style_test

import (
	"image/color"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/exp/charmtone"
	"github.com/lucasb-eyer/go-colorful"
	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml/internal/colors"
	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

func TestDefault_SharedValueStaysUnchanged(t *testing.T) {
	t.Parallel()

	first := style.Default()
	assert.Equal(t, first, style.Default())

	red := lipgloss.NewStyle().Foreground(lipgloss.Color("#ff0000"))
	extended := style.Default().With(style.Set(kind.Comment, red))
	assert.Equal(t, red, extended.Style(kind.Comment))

	// With copies the shared value, so a later call still sees the default.
	assert.Equal(t, charmtone.Oyster, style.Default().Style(kind.Comment).GetForeground())
}

func TestDefault_HighlightsReadable(t *testing.T) {
	t.Parallel()

	content := []kind.Kind{
		kind.Text,
		kind.Comment,
		kind.CommentPreproc,
		kind.LiteralString,
		kind.LiteralNumber,
		kind.LiteralBoolean,
		kind.LiteralNull,
		kind.NameTag,
		kind.NameAnchor,
		kind.Punctuation,
		kind.PunctuationHeading,
		kind.GenericInserted,
		kind.GenericDeleted,
	}

	tcs := map[string]struct {
		apply func(base, overlay lipgloss.Style) lipgloss.Style
	}{
		"override": {apply: colors.OverrideStyles},
		"blend":    {apply: colors.BlendStyles},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			styles := style.Default()

			for _, hl := range []kind.Kind{kind.GenericHighlight, kind.GenericHighlightDim} {
				for _, k := range content {
					got := tc.apply(styles.Style(k), styles.Style(hl))
					assert.NotEqual(t, hex(got.GetForeground()), hex(got.GetBackground()),
						"%s over %s hides its text", hl, k)
				}

				text := tc.apply(styles.Style(kind.Text), styles.Style(hl))
				assert.GreaterOrEqual(t, contrast(text.GetForeground(), text.GetBackground()), 4.5,
					"%s over %s", hl, kind.Text)
			}

			// The current match stands apart from the other matches.
			current := tc.apply(styles.Style(kind.Text), styles.Style(kind.GenericHighlight))
			other := tc.apply(styles.Style(kind.Text), styles.Style(kind.GenericHighlightDim))
			assert.NotEqual(t, hex(other.GetBackground()), hex(current.GetBackground()))
		})
	}
}

// hex returns c as a hex string, so colors of different types compare by
// value.
func hex(c color.Color) string {
	cf, _ := colorful.MakeColor(c)

	return cf.Hex()
}

// contrast returns the WCAG contrast ratio between a and b, from 1 for
// equal colors to 21 for black on white.
func contrast(a, b color.Color) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}

	return (la + 0.05) / (lb + 0.05)
}

// luminance returns the WCAG relative luminance of c.
func luminance(c color.Color) float64 {
	cf, _ := colorful.MakeColor(c)
	r, g, b := cf.LinearRgb()

	return 0.2126*r + 0.7152*g + 0.0722*b
}
