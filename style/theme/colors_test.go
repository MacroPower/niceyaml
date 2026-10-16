package theme_test

import (
	"image/color"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/style/kind"
	"go.jacobcolvin.com/niceyaml/style/theme"
)

// brand holds the colors of a dark custom theme, with every color set.
var brand = theme.Colors{
	Fg:     lipgloss.Color("#e6e6e6"),
	Bg:     lipgloss.Color("#101820"),
	Accent: lipgloss.Color("#ff6f00"),
	OK:     lipgloss.Color("#00aa00"),
	Warn:   lipgloss.Color("#ffcc00"),
	Error:  lipgloss.Color("#dd3333"),
	Mode:   theme.Dark,
}

func TestDerive(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		colors theme.Colors
		tokens map[kind.Kind]theme.Token
		kind   kind.Kind
		want   string
	}{
		"body text takes fg on the surface": {colors: brand, kind: kind.Text, want: "#e6e6e6 bg:#101820"},
		"unset fg and bg leave the terminal's colors": {
			colors: theme.Colors{Mode: theme.Light},
			kind:   kind.Text,
			want:   "",
		},

		// The colors decide the status text, the headings, and the marks a
		// renderer paints, with no token for any of them.
		"status text takes its color": {colors: brand, kind: kind.TextWarn, want: "#ffcc00 bg:#101820"},
		"heading paints the accent":   {colors: brand, kind: kind.GenericHeading, want: "bold #101820 bg:#ff6f00"},
		"error heading paints error": {
			colors: brand,
			kind:   kind.GenericHeadingError,
			want:   "bold #101820 bg:#dd3333",
		},
		"inserted lines take ok":   {colors: brand, kind: kind.GenericInserted, want: "#00aa00 bg:#101820"},
		"deleted lines take error": {colors: brand, kind: kind.GenericDeleted, want: "#dd3333 bg:#101820"},
		"error mark is a badge":    {colors: brand, kind: kind.GenericError, want: "#101820 bg:#dd3333"},
		"warning mark is a badge":  {colors: brand, kind: kind.GenericWarn, want: "#101820 bg:#ffcc00"},

		// A color left unset takes a default picked for the mode.
		"unset accent on light": {colors: theme.Colors{Mode: theme.Light}, kind: kind.TextAccent, want: "#0969da"},
		"unset ok on light":     {colors: theme.Colors{Mode: theme.Light}, kind: kind.GenericInserted, want: "#1a7f37"},
		"unset warn on dark":    {colors: theme.Colors{Mode: theme.Dark}, kind: kind.TextWarn, want: "#d29922"},
		"unset error on dark": {
			colors: theme.Colors{Mode: theme.Dark},
			kind:   kind.GenericError,
			want:   "#000000 bg:#f85149",
		},
		"no color counts as unset": {
			colors: theme.Colors{OK: lipgloss.NoColor{}, Mode: theme.Dark},
			kind:   kind.GenericInserted,
			want:   "#3fb950",
		},

		// A token layers over the style its kind inherits.
		"attributes alone keep the parent color": {
			colors: brand,
			tokens: map[kind.Kind]theme.Token{kind.Name: "#abcdef", kind.NameTag: "bold"},
			kind:   kind.NameTag,
			want:   "bold #abcdef bg:#101820",
		},
		"nobold turns off the parent's bold": {
			colors: brand,
			tokens: map[kind.Kind]theme.Token{kind.Name: "bold #abcdef", kind.NameTag: "nobold"},
			kind:   kind.NameTag,
			want:   "#abcdef bg:#101820",
		},
		"custom kind": {
			colors: brand,
			tokens: map[kind.Kind]theme.Token{kind.Kind("mine"): "#00ffff"},
			kind:   kind.Kind("mine"),
			want:   "#00ffff bg:#101820",
		},

		// A token for a derived kind layers over the derived style, and a
		// token above it does not reach it.
		"token layers over a derived kind": {
			colors: brand,
			tokens: map[kind.Kind]theme.Token{kind.TextAccent: "underline"},
			kind:   kind.TextAccent,
			want:   "underline #ff6f00 bg:#101820",
		},
		"generic token leaves the heading": {
			colors: brand,
			tokens: map[kind.Kind]theme.Token{kind.Generic: "#123456"},
			kind:   kind.GenericHeading,
			want:   "bold #101820 bg:#ff6f00",
		},
		"text token leaves the status text": {
			colors: brand,
			tokens: map[kind.Kind]theme.Token{kind.Text: "#123456"},
			kind:   kind.TextOK,
			want:   "#00aa00 bg:#101820",
		},

		// A token for a mark replaces the default badge.
		"token replaces the error badge": {
			colors: brand,
			tokens: map[kind.Kind]theme.Token{kind.GenericError: "#ff0000"},
			kind:   kind.GenericError,
			want:   "#ff0000 bg:#101820",
		},

		// Null takes the boolean token unless a token of its own names it.
		"null takes the boolean token": {
			colors: brand,
			tokens: map[kind.Kind]theme.Token{kind.LiteralBoolean: "#ff79c6"},
			kind:   kind.LiteralNull,
			want:   "#ff79c6 bg:#101820",
		},
		"null token wins": {
			colors: brand,
			tokens: map[kind.Kind]theme.Token{kind.LiteralBoolean: "#ff79c6", kind.LiteralNull: "#bd93f9"},
			kind:   kind.LiteralNull,
			want:   "#bd93f9 bg:#101820",
		},

		// The chrome takes the color of the comments and none of their
		// attributes, and a UI token layers over that color.
		"chrome takes the comment color upright": {
			colors: brand,
			tokens: map[kind.Kind]theme.Token{kind.Comment: "bold italic #7a8590"},
			kind:   kind.UI,
			want:   "#7a8590 bg:#101820",
		},
		"children inherit the chrome": {
			colors: brand,
			tokens: map[kind.Kind]theme.Token{kind.Comment: "italic #7a8590"},
			kind:   kind.UILineNumber,
			want:   "#7a8590 bg:#101820",
		},
		"ui token layers over the comment color": {
			colors: brand,
			tokens: map[kind.Kind]theme.Token{kind.Comment: "italic #7a8590", kind.UI: "bold"},
			kind:   kind.UILineNumber,
			want:   "bold #7a8590 bg:#101820",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := theme.Derive(tc.colors, tc.tokens)
			assert.Equal(t, tc.want, style.Encode(got.Style(tc.kind)))
		})
	}
}

func TestDerive_KeepsColors(t *testing.T) {
	t.Parallel()

	// The kinds that take a color whole hold it as given. A theme of ANSI
	// colors thus keeps the palette of the terminal, and each mark matches
	// the status text of its color.
	styles := theme.Derive(theme.Colors{
		OK:    lipgloss.Green,
		Warn:  lipgloss.Yellow,
		Error: lipgloss.Red,
		Mode:  theme.Dark,
	}, nil)

	tcs := map[string]struct {
		get  func(lipgloss.Style) color.Color
		want color.Color
		kind kind.Kind
	}{
		"ok text":         {kind: kind.TextOK, get: lipgloss.Style.GetForeground, want: lipgloss.Green},
		"inserted lines":  {kind: kind.GenericInserted, get: lipgloss.Style.GetForeground, want: lipgloss.Green},
		"error text":      {kind: kind.TextError, get: lipgloss.Style.GetForeground, want: lipgloss.Red},
		"deleted lines":   {kind: kind.GenericDeleted, get: lipgloss.Style.GetForeground, want: lipgloss.Red},
		"error heading":   {kind: kind.GenericHeadingError, get: lipgloss.Style.GetBackground, want: lipgloss.Red},
		"error mark":      {kind: kind.GenericError, get: lipgloss.Style.GetBackground, want: lipgloss.Red},
		"warning text":    {kind: kind.TextWarn, get: lipgloss.Style.GetForeground, want: lipgloss.Yellow},
		"warning heading": {kind: kind.GenericHeadingWarn, get: lipgloss.Style.GetBackground, want: lipgloss.Yellow},
		"warning mark":    {kind: kind.GenericWarn, get: lipgloss.Style.GetBackground, want: lipgloss.Yellow},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.get(styles.Style(tc.kind)))
		})
	}
}

func TestDerive_Surface(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		bg   color.Color
		want color.Color
	}{
		"bg becomes the surface": {bg: lipgloss.Color("#101820"), want: lipgloss.Color("#101820")},
		"nil bg sets none":       {bg: nil, want: nil},
		"no color sets none":     {bg: lipgloss.NoColor{}, want: nil},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := theme.Derive(theme.Colors{Bg: tc.bg, Mode: theme.Dark}, nil)
			assert.Equal(t, tc.want, got.Surface())

			// The highlights paint a fill of their own on either surface.
			assert.False(t, unset(got.Style(kind.GenericHighlight).GetBackground()))
		})
	}
}

func TestDerive_MarksStandOut(t *testing.T) {
	t.Parallel()

	// A theme from a few colors, or from none, gets marks that a reader
	// tells apart from the body text and from one another.
	tcs := map[string]struct {
		colors theme.Colors
	}{
		"every color set":  {colors: brand},
		"mode alone light": {colors: theme.Colors{Mode: theme.Light}},
		"mode alone dark":  {colors: theme.Colors{Mode: theme.Dark}},
		"brand on light": {colors: theme.Colors{
			Fg:     lipgloss.Color("#202020"),
			Bg:     lipgloss.Color("#fdf6e3"),
			Accent: lipgloss.Color("#d33682"),
			Mode:   theme.Light,
		}},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			styles := theme.Derive(tc.colors, map[kind.Kind]theme.Token{kind.Comment: "italic #808080"})
			text := style.Encode(styles.Style(kind.Text))

			for _, k := range []kind.Kind{
				kind.GenericInserted,
				kind.GenericDeleted,
				kind.GenericError,
				kind.GenericWarn,
				kind.TextAccent,
				kind.TextSubtle,
			} {
				assert.NotEqual(t, text, style.Encode(styles.Style(k)), "%s matches Text", k)
			}

			assert.NotEqual(t,
				style.Encode(styles.Style(kind.GenericInserted)),
				style.Encode(styles.Style(kind.GenericDeleted)),
				"GenericDeleted matches GenericInserted",
			)

			// Without Fg and Bg, the derived kinds assume black on white
			// for a light theme and white on black for a dark one.
			fg, bg := styles.Style(kind.Text).GetForeground(), styles.Style(kind.Text).GetBackground()
			if unset(fg) {
				fg, bg = lipgloss.Color("#000000"), lipgloss.Color("#ffffff")
				if tc.colors.Mode == theme.Dark {
					fg, bg = bg, fg
				}
			}

			// Each badge draws its text in the body color that reads best
			// on its fill.
			for _, k := range []kind.Kind{kind.GenericError, kind.GenericWarn} {
				st := styles.Style(k)
				got := yamltest.Contrast(st.GetForeground(), st.GetBackground())

				for _, body := range []color.Color{fg, bg} {
					assert.GreaterOrEqual(t, got, yamltest.Contrast(body, st.GetBackground()),
						"%s draws %s", k, style.Encode(st))
				}
			}

			// The highlights stand apart from the background behind the
			// text and from each other.
			dim := styles.Style(kind.GenericHighlightDim).GetBackground()
			hl := styles.Style(kind.GenericHighlight).GetBackground()

			assert.GreaterOrEqual(t, distance(bg, dim), minMarkDistance,
				"dim highlight %s sits close to the background %s", yamltest.ColorHex(dim), yamltest.ColorHex(bg))
			assert.GreaterOrEqual(t, distance(dim, hl), minMarkDistance,
				"highlight %s sits close to the dim highlight %s", yamltest.ColorHex(hl), yamltest.ColorHex(dim))

			// The chrome draws upright in the comment color.
			assert.Equal(t,
				styles.Style(kind.Comment).GetForeground(),
				styles.Style(kind.UILineNumber).GetForeground(),
			)
			assert.False(t, styles.Style(kind.UILineNumber).GetItalic())
		})
	}
}

func TestDerive_InvalidToken(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		token theme.Token
		err   error
	}{
		"malformed color": {token: "#zzz", err: style.ErrInvalidColor},
		"unknown keyword": {token: "sparkly", err: style.ErrUnknownKeyword},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tokens := map[kind.Kind]theme.Token{kind.Comment: "italic", kind.NameTag: tc.token}

			err := panicError(t, func() {
				theme.Derive(brand, tokens)
			})
			require.ErrorIs(t, err, tc.err)
			assert.ErrorContains(t, err, string(kind.NameTag))
		})
	}
}

func TestFromColors(t *testing.T) {
	t.Parallel()

	t.Run("derives the styles", func(t *testing.T) {
		t.Parallel()

		tokens := map[kind.Kind]theme.Token{kind.NameTag: "bold #ff6f00"}

		th := theme.FromColors("test-corp", brand, tokens)

		assert.Equal(t, "test-corp", th.Name)
		assert.Equal(t, theme.Dark, th.Mode)
		assert.Equal(t, theme.Derive(brand, tokens), th.Styles())
	})

	t.Run("keeps a copy of the tokens", func(t *testing.T) {
		t.Parallel()

		tokens := map[kind.Kind]theme.Token{kind.NameTag: "#ff6f00"}

		th := theme.FromColors("test-copy", brand, tokens)
		tokens[kind.NameTag] = "#00ff00"

		assert.Equal(t, "#ff6f00 bg:#101820", style.Encode(th.Style(kind.NameTag)))
	})

	t.Run("checks the tokens when it creates the theme", func(t *testing.T) {
		t.Parallel()

		err := panicError(t, func() {
			theme.FromColors("test-invalid", brand, map[kind.Kind]theme.Token{kind.NameTag: "#zzz"})
		})
		require.ErrorIs(t, err, style.ErrInvalidColor)
	})

	t.Run("joins a catalog", func(t *testing.T) {
		t.Parallel()

		c := theme.Builtin().With(theme.FromColors("test-joined", brand, nil))

		got, ok := c.Get("test-joined")
		require.True(t, ok)
		assert.Equal(t, "#e6e6e6 bg:#101820", style.Encode(got.Style(kind.Text)))
	})
}

// panicError runs f and returns the error that f panics with, or nil when
// f returns. The test fails when f panics with a value other than an error.
func panicError(t *testing.T, f func()) error {
	t.Helper()

	var err error

	func() {
		defer func() {
			r := recover()
			if r == nil {
				return
			}

			e, ok := r.(error)
			assert.True(t, ok, "panic value %v is not an error", r)

			err = e
		}()

		f()
	}()

	return err
}
