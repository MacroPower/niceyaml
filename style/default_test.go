package style_test

import (
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/exp/charmtone"
	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml/internal/colors"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

func TestDefault_SharedValueStaysUnchanged(t *testing.T) {
	t.Parallel()

	first := style.Default()
	assert.Equal(t, first, style.Default())

	extended := style.Default().With(style.Set(kind.Comment, redStyle))
	assert.Equal(t, redStyle, extended.Style(kind.Comment))

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

	// The default styles draw on the terminal's own background, and
	// theme.Charm draws the same palette on a Pepper surface, which a
	// blended highlight mixes into.
	pepper := style.Default().With(style.WithSurface(charmtone.Pepper))

	tcs := map[string]struct {
		apply  func(base, overlay lipgloss.Style) lipgloss.Style
		styles style.Styles
	}{
		"override":           {apply: colors.OverrideStyles, styles: style.Default()},
		"blend":              {apply: colors.BlendStyles, styles: style.Default()},
		"override on pepper": {apply: colors.OverrideStyles, styles: pepper},
		"blend on pepper":    {apply: colors.BlendStyles, styles: pepper},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			styles := tc.styles

			for _, hl := range []kind.Kind{kind.GenericHighlight, kind.GenericHighlightDim} {
				for _, k := range content {
					got := tc.apply(styles.Style(k), styles.Style(hl))
					assert.NotEqual(t, yamltest.ColorHex(got.GetForeground()), yamltest.ColorHex(got.GetBackground()),
						"%s over %s hides its text", hl, k)
				}

				text := tc.apply(styles.Style(kind.Text), styles.Style(hl))
				assert.GreaterOrEqual(t, yamltest.Contrast(text.GetForeground(), text.GetBackground()), 4.5,
					"%s over %s", hl, kind.Text)
			}

			// The current match stands apart from the other matches.
			current := tc.apply(styles.Style(kind.Text), styles.Style(kind.GenericHighlight))
			other := tc.apply(styles.Style(kind.Text), styles.Style(kind.GenericHighlightDim))
			assert.NotEqual(t, yamltest.ColorHex(other.GetBackground()), yamltest.ColorHex(current.GetBackground()))
		})
	}
}
