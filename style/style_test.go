package style_test

import (
	"image/color"
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

// italic turns on italics and keeps the rest of the style, for
// [style.Update].
//
//nolint:gocritic // hugeParam: value semantics match lipgloss.
func italic(s lipgloss.Style) lipgloss.Style {
	return s.Italic(true)
}

// bold turns on bold and keeps the rest of the style, for [style.Update].
//
//nolint:gocritic // hugeParam: value semantics match lipgloss.
func bold(s lipgloss.Style) lipgloss.Style {
	return s.Bold(true)
}

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
		match = kind.GenericHighlight + ".match"
		focus = match + ".focus"
		mine  = kind.Kind("customMine")
		sub   = mine + ".sub"
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
		"kind below a predefined kind takes the style of its parent": {
			opts: []style.Option{style.Set(kind.GenericHighlight, greenStyle)},
			want: map[kind.Kind]lipgloss.Style{match: greenStyle, focus: greenStyle},
		},
		"kind below a predefined kind takes a style set above its parent": {
			opts: []style.Option{style.Set(kind.Generic, redStyle)},
			want: map[kind.Kind]lipgloss.Style{match: redStyle, focus: redStyle},
		},
		"explicit Set on a kind below a predefined kind wins": {
			opts: []style.Option{
				style.Set(match, redStyle),
				style.Set(kind.GenericHighlight, greenStyle),
			},
			want: map[kind.Kind]lipgloss.Style{
				match:                 redStyle,
				focus:                 redStyle,
				kind.GenericHighlight: greenStyle,
			},
		},
		"kind below a set custom kind takes its style": {
			opts: []style.Option{style.Set(mine, redStyle)},
			want: map[kind.Kind]lipgloss.Style{mine: redStyle, sub: redStyle},
		},
		"kind below an unset custom kind takes the base style": {
			opts: []style.Option{style.Set(kind.GenericHighlight, greenStyle)},
			want: map[kind.Kind]lipgloss.Style{mine: baseStyle, sub: baseStyle},
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

func TestWithSurface(t *testing.T) {
	t.Parallel()

	const custom kind.Kind = "customSurface"

	surface := lipgloss.Color("#202020")
	other := lipgloss.Color("#303030")
	fill := lipgloss.NewStyle().Background(lipgloss.Color("#ff00ff"))

	// Each case builds its styles over baseStyle.
	tcs := map[string]struct {
		surface color.Color
		want    map[kind.Kind]lipgloss.Style
		opts    []style.Option
	}{
		"kinds without a background take the surface": {
			opts: []style.Option{
				style.WithSurface(surface),
				style.Set(kind.Comment, greenStyle),
			},
			surface: surface,
			want: map[kind.Kind]lipgloss.Style{
				kind.Text:               baseStyle.Background(surface),
				kind.Comment:            greenStyle.Background(surface),
				kind.UILineNumber:       greenStyle.Background(surface),
				kind.LiteralNumberFloat: baseStyle.Background(surface),
				custom:                  baseStyle.Background(surface),
			},
		},
		"kinds with a background keep it": {
			opts: []style.Option{
				style.Set(kind.GenericHighlight, fill),
				style.WithSurface(surface),
			},
			surface: surface,
			want: map[kind.Kind]lipgloss.Style{
				kind.GenericHighlight:                fill,
				kind.GenericHighlight.Child("match"): fill,
				kind.Text:                            baseStyle.Background(surface),
			},
		},
		"later surface replaces the earlier one": {
			opts: []style.Option{
				style.WithSurface(surface),
				style.WithSurface(other),
			},
			surface: other,
			want:    map[kind.Kind]lipgloss.Style{kind.Text: baseStyle.Background(other)},
		},
		"nil sets no surface": {
			opts: []style.Option{
				style.WithSurface(surface),
				style.WithSurface(nil),
			},
			want: map[kind.Kind]lipgloss.Style{kind.Text: baseStyle, kind.Comment: baseStyle},
		},
		"NoColor sets no surface": {
			opts: []style.Option{style.WithSurface(lipgloss.NoColor{})},
			want: map[kind.Kind]lipgloss.Style{kind.Text: baseStyle, kind.Comment: baseStyle},
		},
		"no option sets no surface": {
			want: map[kind.Kind]lipgloss.Style{kind.Text: baseStyle},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			styles := style.New(baseStyle, tc.opts...)

			assert.Equal(t, tc.surface, styles.Surface())
			assertStyles(t, tc.want, styles)
		})
	}
}

func TestStyles_Surface(t *testing.T) {
	t.Parallel()

	surface := lipgloss.Color("#202020")
	painted := style.New(baseStyle, style.WithSurface(surface), style.Set(kind.Comment, greenStyle))

	tcs := map[string]struct {
		styles  style.Styles
		surface color.Color
		want    map[kind.Kind]lipgloss.Style
	}{
		"With keeps the surface": {
			styles:  painted.With(style.Set(kind.LiteralNumber, redStyle)),
			surface: surface,
			want: map[kind.Kind]lipgloss.Style{
				kind.Text:          baseStyle.Background(surface),
				kind.LiteralNumber: redStyle.Background(surface),
			},
		},
		"With removes the surface": {
			styles: painted.With(style.WithSurface(nil)),
			want: map[kind.Kind]lipgloss.Style{
				kind.Text:    baseStyle,
				kind.Comment: greenStyle,
			},
		},
		"With adds a surface to the zero value": {
			styles:  style.Styles{}.With(style.WithSurface(surface)),
			surface: surface,
			want: map[kind.Kind]lipgloss.Style{
				kind.Text:    lipgloss.NewStyle().Background(surface),
				kind.Comment: lipgloss.NewStyle().Background(surface),
			},
		},
		"zero value": {
			styles: style.Styles{},
			want:   map[kind.Kind]lipgloss.Style{kind.Text: lipgloss.NewStyle()},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.surface, tc.styles.Surface())
			assertStyles(t, tc.want, tc.styles)
		})
	}

	// The receiver keeps its surface.
	assert.Equal(t, surface, painted.Surface())
	assert.Equal(t, greenStyle.Background(surface), painted.Style(kind.Comment))
}

func TestUpdate(t *testing.T) {
	t.Parallel()

	custom := kind.GenericHighlight.Child("update")

	fill := lipgloss.NewStyle().Background(lipgloss.Color("#ff00ff"))

	// Each case builds its styles over baseStyle.
	tcs := map[string]struct {
		want map[kind.Kind]lipgloss.Style
		opts []style.Option
	}{
		"adjusts the style a Set gave the kind": {
			opts: []style.Option{
				style.Set(kind.Comment, greenStyle),
				style.Update(kind.Comment, italic),
			},
			want: map[kind.Kind]lipgloss.Style{
				kind.Comment:      greenStyle.Italic(true),
				kind.UILineNumber: greenStyle.Italic(true),
				kind.Text:         baseStyle,
			},
		},
		"adjusts the style of the closest set ancestor": {
			opts: []style.Option{
				style.Set(kind.LiteralNumber, redStyle),
				style.Update(kind.LiteralNumberFloat, bold),
			},
			want: map[kind.Kind]lipgloss.Style{
				kind.LiteralNumberFloat:   redStyle.Bold(true),
				kind.LiteralNumber:        redStyle,
				kind.LiteralNumberInteger: redStyle,
			},
		},
		"adjusts the base style when no ancestor is set": {
			opts: []style.Option{style.Update(kind.NameTag, bold)},
			want: map[kind.Kind]lipgloss.Style{
				kind.NameTag: baseStyle.Bold(true),
				kind.Name:    baseStyle,
				kind.Text:    baseStyle,
			},
		},
		"adjusts the base style through kind.Text": {
			opts: []style.Option{
				style.Set(kind.Comment, greenStyle),
				style.Update(kind.Text, bold),
			},
			want: map[kind.Kind]lipgloss.Style{
				kind.Text:    baseStyle.Bold(true),
				kind.NameTag: baseStyle.Bold(true),
				kind.Comment: greenStyle,
			},
		},
		"takes the style of the parent of a kind below a predefined kind": {
			opts: []style.Option{
				style.Set(kind.GenericHighlight, fill),
				style.Update(custom, bold),
			},
			want: map[kind.Kind]lipgloss.Style{
				custom:                fill.Bold(true),
				kind.GenericHighlight: fill,
			},
		},
		"takes a Set style of a kind below a predefined kind over its parent": {
			opts: []style.Option{
				style.Set(kind.GenericHighlight, fill),
				style.Set(custom, redStyle),
				style.Update(custom, bold),
			},
			want: map[kind.Kind]lipgloss.Style{
				custom:                redStyle.Bold(true),
				kind.GenericHighlight: fill,
			},
		},
		"second Update adjusts the result of the first": {
			opts: []style.Option{
				style.Update(kind.Comment, italic),
				style.Update(kind.Comment, bold),
			},
			want: map[kind.Kind]lipgloss.Style{kind.Comment: baseStyle.Italic(true).Bold(true)},
		},
		"later Set on the kind replaces the result": {
			opts: []style.Option{
				style.Update(kind.Comment, italic),
				style.Set(kind.Comment, greenStyle),
			},
			want: map[kind.Kind]lipgloss.Style{kind.Comment: greenStyle},
		},
		"later Set on an ancestor does not reach the kind": {
			opts: []style.Option{
				style.Update(kind.LiteralNumberFloat, bold),
				style.Set(kind.LiteralNumber, redStyle),
			},
			want: map[kind.Kind]lipgloss.Style{
				kind.LiteralNumberFloat:   baseStyle.Bold(true),
				kind.LiteralNumber:        redStyle,
				kind.LiteralNumberInteger: redStyle,
			},
		},
		"nil function leaves the kind inheriting": {
			opts: []style.Option{
				style.Update(kind.LiteralNumberFloat, nil),
				style.Set(kind.LiteralNumber, redStyle),
			},
			want: map[kind.Kind]lipgloss.Style{kind.LiteralNumberFloat: redStyle},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assertStyles(t, tc.want, style.New(baseStyle, tc.opts...))
		})
	}

	t.Run("With adjusts the style of the receiver", func(t *testing.T) {
		t.Parallel()

		original := style.New(baseStyle, style.Set(kind.Comment, greenStyle))
		result := original.With(style.Update(kind.Comment, italic))

		assert.Equal(t, greenStyle.Italic(true), result.Style(kind.Comment))
		assert.Equal(t, greenStyle, original.Style(kind.Comment))
	})

	t.Run("zero value can be extended", func(t *testing.T) {
		t.Parallel()

		result := style.Styles{}.With(style.Update(kind.Comment, bold))

		assert.Equal(t, lipgloss.NewStyle().Bold(true), result.Style(kind.Comment))
		assert.Equal(t, lipgloss.Style{}, result.Style(kind.Text))
	})

	t.Run("function runs one time for each build", func(t *testing.T) {
		t.Parallel()

		var calls int

		counted := style.Update(kind.Comment, func(s lipgloss.Style) lipgloss.Style {
			calls++

			return s.Italic(true)
		})

		styles := style.New(baseStyle, counted)

		assert.Equal(t, 1, calls)

		// A later With keeps the stored style and does not run the
		// function again.
		styles = styles.With(style.Set(kind.NameTag, redStyle))

		assert.Equal(t, 1, calls)
		assert.Equal(t, baseStyle.Italic(true), styles.Style(kind.Comment))
	})

	t.Run("adjusted kind stays on the surface", func(t *testing.T) {
		t.Parallel()

		surface := lipgloss.Color("#202020")
		other := lipgloss.Color("#303030")
		painted := style.New(baseStyle, style.WithSurface(surface), style.Set(kind.Comment, greenStyle))

		var got lipgloss.Style

		styles := painted.With(style.Update(kind.Comment, func(s lipgloss.Style) lipgloss.Style {
			got = s

			return s.Italic(true)
		}))

		// The function takes the style without the surface, and the kind
		// resolves with the surface behind it.
		assert.Equal(t, greenStyle, got)

		want := greenStyle.Italic(true)
		assert.Equal(t, want.Background(surface), styles.Style(kind.Comment))
		assert.Equal(t, want.Background(surface), styles.Style(kind.UILineNumber))

		// A later surface reaches the adjusted kind and its children.
		moved := styles.With(style.WithSurface(other))
		assert.Equal(t, want.Background(other), moved.Style(kind.Comment))
		assert.Equal(t, want.Background(other), moved.Style(kind.UILineNumber))

		plain := styles.With(style.WithSurface(nil))
		assert.Equal(t, want, plain.Style(kind.Comment))
		assert.Equal(t, want, plain.Style(kind.UILineNumber))
	})

	t.Run("one option adjusts each built-in theme", func(t *testing.T) {
		t.Parallel()

		for _, th := range theme.Builtin().All() {
			plain := th.Styles().With(style.WithSurface(nil))
			got := th.Styles().With(style.Update(kind.Comment, italic), style.WithSurface(nil))

			assert.Equal(t, plain.Style(kind.Comment).Italic(true), got.Style(kind.Comment), th.Name)
			assert.Equal(t, plain.Style(kind.NameTag), got.Style(kind.NameTag), th.Name)
		}
	})
}

func TestStyles_Style(t *testing.T) {
	t.Parallel()

	const match = kind.GenericHighlight + ".match"

	t.Run("kind below a predefined kind takes its style under built-in themes", func(t *testing.T) {
		t.Parallel()

		dracula, ok := theme.Builtin().Get("dracula")
		require.True(t, ok)

		for _, th := range []theme.Theme{theme.Charm, dracula} {
			styles := th.Styles()

			assert.Equal(t, styles.Style(kind.GenericHighlight), styles.Style(match), th.Name)
			assert.Equal(t, styles.Style(kind.GenericHighlight), styles.Style(match.Child("focus")), th.Name)
			assert.NotEqual(t, styles.Style(kind.Text), styles.Style(match), th.Name)
		}
	})

	t.Run("With re-resolves through the parent", func(t *testing.T) {
		t.Parallel()

		original := style.New(baseStyle)
		result := original.With(style.Set(kind.GenericHighlight, redStyle))

		assert.Equal(t, redStyle, result.Style(match))
		assert.Equal(t, baseStyle, original.Style(match))
	})

	t.Run("zero value returns an empty style for a kind below a predefined kind", func(t *testing.T) {
		t.Parallel()

		assert.Equal(t, lipgloss.NewStyle(), style.Styles{}.Style(match))
	})
}
