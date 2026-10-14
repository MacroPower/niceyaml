package style_test

import (
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/style/kind"
	"go.jacobcolvin.com/niceyaml/style/theme"
)

// The styles the tests build [style.Styles] from. Each sets only a
// foreground color.
var (
	baseStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffffff"))
	redStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff0000"))
	greenStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#00ff00"))
	yellowStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffff00"))
	blueStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#0000ff"))
	grayStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#808080"))
)

// assertStyles asserts that styles holds the style want names for each of
// its kinds.
func assertStyles(t *testing.T, want map[kind.Kind]lipgloss.Style, styles style.Styles) {
	t.Helper()

	for k := range want {
		assert.Equal(t, want[k], styles.Style(k), "kind %q", k)
	}
}

func TestNew(t *testing.T) {
	t.Parallel()

	const (
		match kind.Kind = "customMatch"
		focus kind.Kind = "customFocus"
		other kind.Kind = "customOther"
	)

	title := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#000000")).
		Background(lipgloss.Color("#ff0000"))

	// Each case builds its styles over baseStyle.
	tcs := map[string]struct {
		want map[kind.Kind]lipgloss.Style
		opts []style.Option
	}{
		"each kind resolves to its closest set ancestor": {
			opts: []style.Option{
				style.Set(kind.LiteralNumber, redStyle),
				style.Set(kind.Comment, greenStyle),
			},
			want: map[kind.Kind]lipgloss.Style{
				kind.Text:                    baseStyle,
				kind.Comment:                 greenStyle,
				kind.LiteralNumber:           redStyle,
				kind.LiteralNumberFloat:      redStyle,
				kind.LiteralString:           baseStyle,
				kind.NameTag:                 baseStyle,
				kind.Punctuation:             baseStyle,
				kind.PunctuationMappingValue: baseStyle,
				kind.TextAccentDim:           baseStyle,
				kind.TextSubtleDim:           baseStyle,
				kind.GenericHeading:          baseStyle,
				kind.UILineNumber:            greenStyle,
			},
		},
		"explicit Set overrides inherited default": {
			opts: []style.Option{
				style.Set(kind.TextAccentDim, redStyle),
				style.Set(kind.TextSubtleDim, grayStyle),
				style.Set(kind.GenericHeading, title),
			},
			want: map[kind.Kind]lipgloss.Style{
				kind.TextAccentDim:  redStyle,
				kind.TextSubtleDim:  grayStyle,
				kind.GenericHeading: title,
			},
		},
		"Text override takes precedence over base": {
			opts: []style.Option{
				style.Set(kind.Text, redStyle),
				style.Set(kind.LiteralNumber, blueStyle),
			},
			want: map[kind.Kind]lipgloss.Style{
				kind.Text:          redStyle,
				kind.LiteralNumber: blueStyle,
			},
		},
		// An unknown kind has Text as its parent.
		"unset kinds inherit the base style": {
			want: map[kind.Kind]lipgloss.Style{
				kind.Text:    baseStyle,
				kind.NameTag: baseStyle,
				"never-set":  baseStyle,
			},
		},
		"explicit Set before Inherit wins": {
			opts: []style.Option{
				style.Set(match, redStyle),
				style.Inherit(match, kind.GenericHighlight),
				style.Set(kind.GenericHighlight, greenStyle),
			},
			want: map[kind.Kind]lipgloss.Style{match: redStyle},
		},
		"explicit Set after Inherit wins": {
			opts: []style.Option{
				style.Inherit(match, kind.GenericHighlight),
				style.Set(kind.GenericHighlight, greenStyle),
				style.Set(match, redStyle),
			},
			want: map[kind.Kind]lipgloss.Style{match: redStyle},
		},
		"later Inherit replaces the parent": {
			opts: []style.Option{
				style.Set(kind.GenericHighlight, greenStyle),
				style.Set(kind.TextAccent, redStyle),
				style.Inherit(match, kind.GenericHighlight),
				style.Inherit(match, kind.TextAccent),
			},
			want: map[kind.Kind]lipgloss.Style{match: redStyle},
		},
		"custom kinds chain to a predefined ancestor": {
			opts: []style.Option{
				style.Set(kind.GenericHighlight, greenStyle),
				style.Inherit(focus, match),
				style.Inherit(match, kind.GenericHighlight),
			},
			want: map[kind.Kind]lipgloss.Style{focus: greenStyle, match: greenStyle},
		},
		"predefined kind moves under a new parent": {
			opts: []style.Option{
				style.Set(kind.Comment, greenStyle),
				style.Inherit(kind.UI, kind.Text),
			},
			want: map[kind.Kind]lipgloss.Style{
				kind.UI:           baseStyle,
				kind.UILineNumber: baseStyle,
				kind.Comment:      greenStyle,
			},
		},
		"cycle resolves to the base style": {
			opts: []style.Option{
				style.Inherit(match, focus),
				style.Inherit(focus, match),
			},
			want: map[kind.Kind]lipgloss.Style{match: baseStyle, focus: baseStyle},
		},
		"cycle through a set kind resolves to that kind": {
			opts: []style.Option{
				style.Inherit(match, focus),
				style.Inherit(focus, match),
				style.Set(focus, redStyle),
			},
			want: map[kind.Kind]lipgloss.Style{match: redStyle, focus: redStyle},
		},
		"cycle above the child resolves to the base style": {
			opts: []style.Option{
				style.Inherit(match, focus),
				style.Inherit(focus, other),
				style.Inherit(other, focus),
			},
			want: map[kind.Kind]lipgloss.Style{match: baseStyle},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			styles := style.New(baseStyle, tc.opts...)

			assertStyles(t, tc.want, styles)
		})
	}
}

func TestStyles_With(t *testing.T) {
	t.Parallel()

	original := style.New(baseStyle, style.Set(kind.Comment, greenStyle))

	// Custom style key for testing.
	const customKey kind.Kind = "customKey"

	tcs := map[string]struct {
		want map[kind.Kind]lipgloss.Style
		opts []style.Option
	}{
		"adds new custom style and keeps untouched categories": {
			opts: []style.Option{style.Set(customKey, redStyle)},
			want: map[kind.Kind]lipgloss.Style{
				customKey:    redStyle,
				kind.Comment: greenStyle,
				kind.Text:    baseStyle,
			},
		},
		"overrides existing style": {
			opts: []style.Option{style.Set(kind.Comment, yellowStyle)},
			want: map[kind.Kind]lipgloss.Style{kind.Comment: yellowStyle},
		},
		"adds and overrides in one call": {
			opts: []style.Option{
				style.Set(customKey, redStyle),
				style.Set(kind.Comment, yellowStyle),
			},
			want: map[kind.Kind]lipgloss.Style{
				customKey:    redStyle,
				kind.Comment: yellowStyle,
			},
		},
		"re-resolves inheritance": {
			// Overriding a parent kind reaches the children that inherit it.
			opts: []style.Option{style.Set(kind.LiteralNumber, redStyle)},
			want: map[kind.Kind]lipgloss.Style{kind.LiteralNumberFloat: redStyle},
		},
		"empty options returns an equal copy": {
			want: map[kind.Kind]lipgloss.Style{
				kind.Comment: greenStyle,
				kind.Text:    baseStyle,
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			result := original.With(tc.opts...)
			assertStyles(t, tc.want, result)

			// The original is not modified. The custom key is unset in it,
			// so it inherits the base style.
			assert.Equal(t, baseStyle, original.Style(customKey))
			assert.Equal(t, greenStyle, original.Style(kind.Comment))
			assert.Equal(t, baseStyle, original.Style(kind.LiteralNumberFloat))
		})
	}

	t.Run("zero value can be extended", func(t *testing.T) {
		t.Parallel()

		result := style.Styles{}.With(style.Set(kind.Comment, yellowStyle))

		assert.Equal(t, yellowStyle, result.Style(kind.Comment))
		assert.Equal(t, lipgloss.Style{}, result.Style(kind.Text))

		// The zero value holds an empty style for every kind.
		assert.Equal(t, lipgloss.NewStyle(), style.Styles{}.Style("never-set"))
	})
}

func TestInherit(t *testing.T) {
	t.Parallel()

	const match kind.Kind = "customMatch"

	t.Run("custom kind takes the style of its parent under built-in themes", func(t *testing.T) {
		t.Parallel()

		dracula, ok := theme.Builtin().Get("dracula")
		require.True(t, ok)

		for _, th := range []theme.Theme{theme.Charm, dracula} {
			styles := th.Styles().With(style.Inherit(match, kind.GenericHighlight))

			assert.Equal(t, styles.Style(kind.GenericHighlight), styles.Style(match), th.Name)
			assert.NotEqual(t, styles.Style(kind.Text), styles.Style(match), th.Name)
		}
	})

	t.Run("With re-resolves through the inherited parent", func(t *testing.T) {
		t.Parallel()

		original := style.New(baseStyle, style.Inherit(match, kind.GenericHighlight))
		result := original.With(style.Set(kind.GenericHighlight, redStyle))

		assert.Equal(t, redStyle, result.Style(match))
		assert.Equal(t, baseStyle, original.Style(match))
	})
}
