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

func TestStyles_Style_EmptyStyles(t *testing.T) {
	t.Parallel()

	styles := style.Styles{}
	got := styles.Style(kind.LiteralNumberInteger)

	// The zero value returns an empty style for every kind.
	assert.Equal(t, lipgloss.Style{}, got)
}

func TestNew(t *testing.T) {
	t.Parallel()

	base := lipgloss.NewStyle().Foreground(lipgloss.Color("#ffffff"))
	red := base.Foreground(lipgloss.Color("#ff0000"))
	green := base.Foreground(lipgloss.Color("#00ff00"))
	blue := base.Foreground(lipgloss.Color("#0000ff"))
	gray := base.Foreground(lipgloss.Color("#808080"))
	title := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#000000")).
		Background(lipgloss.Color("#ff0000"))

	// Each case builds its styles over base.
	tcs := map[string]struct {
		want map[kind.Kind]lipgloss.Style
		opts []style.Option
	}{
		"each kind resolves to its closest set ancestor": {
			opts: []style.Option{
				style.Set(kind.LiteralNumber, red),
				style.Set(kind.Comment, green),
			},
			want: map[kind.Kind]lipgloss.Style{
				kind.Text:                    base,
				kind.Comment:                 green,
				kind.LiteralNumber:           red,
				kind.LiteralNumberFloat:      red,
				kind.LiteralString:           base,
				kind.NameTag:                 base,
				kind.Punctuation:             base,
				kind.PunctuationMappingValue: base,
				kind.TextAccentDim:           base,
				kind.TextSubtleDim:           base,
				kind.GenericHeading:          base,
				kind.UILineNumber:            green,
			},
		},
		"explicit Set overrides inherited default": {
			opts: []style.Option{
				style.Set(kind.TextAccentDim, red),
				style.Set(kind.TextSubtleDim, gray),
				style.Set(kind.GenericHeading, title),
			},
			want: map[kind.Kind]lipgloss.Style{
				kind.TextAccentDim:  red,
				kind.TextSubtleDim:  gray,
				kind.GenericHeading: title,
			},
		},
		"Text override takes precedence over base": {
			opts: []style.Option{
				style.Set(kind.Text, red),
				style.Set(kind.LiteralNumber, blue),
			},
			want: map[kind.Kind]lipgloss.Style{
				kind.Text:          red,
				kind.LiteralNumber: blue,
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			styles := style.New(base, tc.opts...)

			for k := range tc.want {
				assert.Equal(t, tc.want[k], styles.Style(k), "kind %q", k)
			}
		})
	}
}

func TestStyles_With(t *testing.T) {
	t.Parallel()

	base := lipgloss.NewStyle().Foreground(lipgloss.Color("#ffffff"))
	red := lipgloss.NewStyle().Foreground(lipgloss.Color("#ff0000"))
	green := lipgloss.NewStyle().Foreground(lipgloss.Color("#00ff00"))
	yellow := lipgloss.NewStyle().Foreground(lipgloss.Color("#ffff00"))

	original := style.New(base, style.Set(kind.Comment, green))

	// Custom style key for testing.
	const customKey kind.Kind = "customKey"

	tcs := map[string]struct {
		want map[kind.Kind]lipgloss.Style
		opts []style.Option
	}{
		"adds new custom style and keeps untouched categories": {
			opts: []style.Option{style.Set(customKey, red)},
			want: map[kind.Kind]lipgloss.Style{
				customKey:    red,
				kind.Comment: green,
				kind.Text:    base,
			},
		},
		"overrides existing style": {
			opts: []style.Option{style.Set(kind.Comment, yellow)},
			want: map[kind.Kind]lipgloss.Style{kind.Comment: yellow},
		},
		"adds and overrides in one call": {
			opts: []style.Option{
				style.Set(customKey, red),
				style.Set(kind.Comment, yellow),
			},
			want: map[kind.Kind]lipgloss.Style{
				customKey:    red,
				kind.Comment: yellow,
			},
		},
		"re-resolves inheritance": {
			// Overriding a parent kind reaches the children that inherit it.
			opts: []style.Option{style.Set(kind.LiteralNumber, red)},
			want: map[kind.Kind]lipgloss.Style{kind.LiteralNumberFloat: red},
		},
		"empty options returns an equal copy": {
			want: map[kind.Kind]lipgloss.Style{
				kind.Comment: green,
				kind.Text:    base,
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			result := original.With(tc.opts...)

			for k := range tc.want {
				assert.Equal(t, tc.want[k], result.Style(k), "kind %q", k)
			}

			// The original is not modified. The custom key is unset in it,
			// so it inherits the base.
			assert.Equal(t, base, original.Style(customKey))
			assert.Equal(t, green, original.Style(kind.Comment))
			assert.Equal(t, base, original.Style(kind.LiteralNumberFloat))
		})
	}

	t.Run("zero value can be extended", func(t *testing.T) {
		t.Parallel()

		result := style.Styles{}.With(style.Set(kind.Comment, yellow))

		assert.Equal(t, yellow, result.Style(kind.Comment))
		assert.Equal(t, lipgloss.Style{}, result.Style(kind.Text))
	})
}

func TestStyles_UnsetCategories(t *testing.T) {
	t.Parallel()

	styles := style.New(lipgloss.NewStyle().Foreground(lipgloss.Color("#ffffff")))

	// An unset predefined kind inherits the base, and so does an unknown
	// kind, whose parent is Text.
	assert.Equal(t, styles.Style(kind.Text), styles.Style(kind.NameTag))
	assert.Equal(t, styles.Style(kind.Text), styles.Style("never-set"))
	assert.Equal(t, lipgloss.NewStyle(), style.Styles{}.Style("never-set"))
}

func TestInherit(t *testing.T) {
	t.Parallel()

	const (
		match kind.Kind = "customMatch"
		focus kind.Kind = "customFocus"
		other kind.Kind = "customOther"
	)

	base := lipgloss.NewStyle().Foreground(lipgloss.Color("#ffffff"))
	red := lipgloss.NewStyle().Foreground(lipgloss.Color("#ff0000"))
	green := lipgloss.NewStyle().Foreground(lipgloss.Color("#00ff00"))

	// Each case builds its styles over base.
	tcs := map[string]struct {
		want map[kind.Kind]lipgloss.Style
		opts []style.Option
	}{
		"explicit Set before Inherit wins": {
			opts: []style.Option{
				style.Set(match, red),
				style.Inherit(match, kind.GenericHighlight),
				style.Set(kind.GenericHighlight, green),
			},
			want: map[kind.Kind]lipgloss.Style{match: red},
		},
		"explicit Set after Inherit wins": {
			opts: []style.Option{
				style.Inherit(match, kind.GenericHighlight),
				style.Set(kind.GenericHighlight, green),
				style.Set(match, red),
			},
			want: map[kind.Kind]lipgloss.Style{match: red},
		},
		"later Inherit replaces the parent": {
			opts: []style.Option{
				style.Set(kind.GenericHighlight, green),
				style.Set(kind.TextAccent, red),
				style.Inherit(match, kind.GenericHighlight),
				style.Inherit(match, kind.TextAccent),
			},
			want: map[kind.Kind]lipgloss.Style{match: red},
		},
		"custom kinds chain to a predefined ancestor": {
			opts: []style.Option{
				style.Set(kind.GenericHighlight, green),
				style.Inherit(focus, match),
				style.Inherit(match, kind.GenericHighlight),
			},
			want: map[kind.Kind]lipgloss.Style{focus: green, match: green},
		},
		"predefined kind moves under a new parent": {
			opts: []style.Option{
				style.Set(kind.Comment, green),
				style.Inherit(kind.UI, kind.Text),
			},
			want: map[kind.Kind]lipgloss.Style{
				kind.UI:           base,
				kind.UILineNumber: base,
				kind.Comment:      green,
			},
		},
		"cycle resolves to the base style": {
			opts: []style.Option{
				style.Inherit(match, focus),
				style.Inherit(focus, match),
			},
			want: map[kind.Kind]lipgloss.Style{match: base, focus: base},
		},
		"cycle through a set kind resolves to that kind": {
			opts: []style.Option{
				style.Inherit(match, focus),
				style.Inherit(focus, match),
				style.Set(focus, red),
			},
			want: map[kind.Kind]lipgloss.Style{match: red, focus: red},
		},
		"cycle above the child resolves to the base style": {
			opts: []style.Option{
				style.Inherit(match, focus),
				style.Inherit(focus, other),
				style.Inherit(other, focus),
			},
			want: map[kind.Kind]lipgloss.Style{match: base},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			styles := style.New(base, tc.opts...)

			for k := range tc.want {
				assert.Equal(t, tc.want[k], styles.Style(k), "kind %q", k)
			}
		})
	}

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

		original := style.New(base, style.Inherit(match, kind.GenericHighlight))
		result := original.With(style.Set(kind.GenericHighlight, red))

		assert.Equal(t, red, result.Style(match))
		assert.Equal(t, base, original.Style(match))
	})
}
