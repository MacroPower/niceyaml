package theme_test

import (
	"encoding/json"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/style/kind"
	"go.jacobcolvin.com/niceyaml/style/theme"
)

// corp is a Palette that names the same theme as brand.
var corp = theme.Palette{
	Name:   "test-corp",
	Mode:   theme.Dark,
	Fg:     "#e6e6e6",
	Bg:     "#101820",
	Accent: "#ff6f00",
	OK:     "#00aa00",
	Warn:   "#ffcc00",
	Error:  "#dd3333",
	Tokens: map[kind.Kind]theme.Token{
		kind.Comment: "italic #7a8590",
		kind.NameTag: "bold #ff6f00",
	},
}

func TestHex_UnmarshalText(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		err  error
		text string
		want theme.Hex
	}{
		"long form":     {text: "#2B1d14", want: "#2B1d14"},
		"short form":    {text: "#fff", want: "#fff"},
		"empty":         {text: "", want: ""},
		"five digits":   {text: "#2b1d1", err: style.ErrInvalidColor},
		"missing hash":  {text: "2b1d14", err: style.ErrInvalidColor},
		"color name":    {text: "red", err: style.ErrInvalidColor},
		"ANSI color":    {text: "4", err: style.ErrInvalidColor},
		"leading space": {text: " #fff", err: style.ErrInvalidColor},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// Each case starts from a color of its own, so a failing case
			// shows that it leaves the Hex in place.
			got := theme.Hex("#000000")

			err := got.UnmarshalText([]byte(tc.text))
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
				assert.Equal(t, theme.Hex("#000000"), got)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestHex_Color(t *testing.T) {
	t.Parallel()

	t.Run("reads a color", func(t *testing.T) {
		t.Parallel()

		got, err := theme.Hex("#ff6f00").Color()
		require.NoError(t, err)
		assert.Equal(t, lipgloss.Color("#ff6f00"), got)
	})

	t.Run("reads the empty Hex as unset", func(t *testing.T) {
		t.Parallel()

		got, err := theme.Hex("").Color()
		require.NoError(t, err)
		assert.Equal(t, lipgloss.NoColor{}, got)
	})

	// A Hex that a program converts from a string skips UnmarshalText, so
	// Color checks it.
	t.Run("rejects a mistyped color", func(t *testing.T) {
		t.Parallel()

		got, err := theme.Hex("#2b1d1").Color()
		require.ErrorIs(t, err, style.ErrInvalidColor)
		require.EqualError(t, err, `invalid color: "#2b1d1"`)
		assert.Nil(t, got)
	})
}

func TestPalette_Colors(t *testing.T) {
	t.Parallel()

	t.Run("converts each color", func(t *testing.T) {
		t.Parallel()

		got, err := corp.Colors()
		require.NoError(t, err)
		assert.Equal(t, brand, got)
	})

	t.Run("leaves an empty color unset", func(t *testing.T) {
		t.Parallel()

		got, err := theme.Palette{Mode: theme.Light, Accent: "#0000ff"}.Colors()
		require.NoError(t, err)
		assert.Equal(t, theme.Colors{
			Fg:     lipgloss.NoColor{},
			Bg:     lipgloss.NoColor{},
			Accent: lipgloss.Color("#0000ff"),
			OK:     lipgloss.NoColor{},
			Warn:   lipgloss.NoColor{},
			Error:  lipgloss.NoColor{},
			Mode:   theme.Light,
		}, got)
	})

	t.Run("reports every color it rejects", func(t *testing.T) {
		t.Parallel()

		p := corp
		p.Bg = "#2b1d1"
		p.Fg = "white"
		p.Error = "1"

		got, err := p.Colors()
		require.ErrorIs(t, err, style.ErrInvalidColor)
		require.EqualError(t, err, `color "fg": invalid color: "white"`+"\n"+
			`color "bg": invalid color: "#2b1d1"`+"\n"+
			`color "error": invalid color: "1"`)
		assert.Equal(t, theme.Colors{}, got)
	})

	// FromColors checks the mode of the Colors that it gets.
	t.Run("keeps an unset mode", func(t *testing.T) {
		t.Parallel()

		got, err := theme.Palette{Bg: "#101820"}.Colors()
		require.NoError(t, err)
		assert.Equal(t, theme.Mode(0), got.Mode)
	})
}

func TestPalette_Theme(t *testing.T) {
	t.Parallel()

	t.Run("creates the theme of the palette", func(t *testing.T) {
		t.Parallel()

		got, err := corp.Theme()
		require.NoError(t, err)

		assert.Equal(t, "test-corp", got.Name)
		assert.Equal(t, theme.Dark, got.Mode)
		assert.Equal(t, theme.MustDerive(brand, corp.Tokens), got.Styles())
		assert.Equal(t, "bold #ff6f00 bg:#101820", style.Encode(got.Style(kind.NameTag)))
	})

	t.Run("keeps a copy of the tokens", func(t *testing.T) {
		t.Parallel()

		p := corp
		p.Tokens = map[kind.Kind]theme.Token{kind.NameTag: "#ff6f00"}

		got, err := p.Theme()
		require.NoError(t, err)

		p.Tokens[kind.NameTag] = "#00ff00"

		assert.Equal(t, "#ff6f00 bg:#101820", style.Encode(got.Style(kind.NameTag)))
	})

	t.Run("accepts a palette of a name and a mode", func(t *testing.T) {
		t.Parallel()

		got, err := theme.Palette{Name: "test-plain", Mode: theme.Light}.Theme()
		require.NoError(t, err)
		assert.Equal(t, theme.MustDerive(theme.Colors{Mode: theme.Light}, nil), got.Styles())
	})

	tcs := map[string]struct {
		err    error
		change func(p *theme.Palette)
		want   string
	}{
		"empty name": {
			change: func(p *theme.Palette) { p.Name = "" },
			err:    theme.ErrEmptyName,
			want:   "empty name",
		},
		"unset mode": {
			change: func(p *theme.Palette) { p.Mode = 0 },
			err:    theme.ErrUnknownMode,
			want:   "unknown mode: mode is unset",
		},
		"mode out of range": {
			change: func(p *theme.Palette) { p.Mode = 3 },
			err:    theme.ErrUnknownMode,
			want:   "unknown mode: 3",
		},
		"mistyped color": {
			change: func(p *theme.Palette) { p.Bg = "#2b1d1" },
			err:    style.ErrInvalidColor,
			want:   `color "bg": invalid color: "#2b1d1"`,
		},
		"malformed token color": {
			change: func(p *theme.Palette) { p.Tokens = map[kind.Kind]theme.Token{kind.NameTag: "#zzz"} },
			err:    style.ErrInvalidColor,
			want:   `token for "nameTag": invalid color: "#zzz"`,
		},
		"unknown token keyword": {
			change: func(p *theme.Palette) { p.Tokens = map[kind.Kind]theme.Token{kind.NameTag: "bolder"} },
			err:    style.ErrUnknownKeyword,
			want:   `token for "nameTag": unknown keyword: "bolder"`,
		},
		"misspelled kind": {
			change: func(p *theme.Palette) { p.Tokens = map[kind.Kind]theme.Token{"literalStrin": "#a3c585"} },
			err:    theme.ErrUnknownKind,
			want:   `token for "literalStrin": unknown kind`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := corp
			tc.change(&p)

			got, err := p.Theme()
			require.ErrorIs(t, err, tc.err)
			require.EqualError(t, err, tc.want)
			assert.Equal(t, theme.Theme{}, got)
		})
	}

	t.Run("reports every problem", func(t *testing.T) {
		t.Parallel()

		_, err := theme.Palette{
			Fg:    "#f3e9dc",
			Bg:    "#2b1d1",
			Error: "red",
			Tokens: map[kind.Kind]theme.Token{
				kind.Comment:   "italic #9c8a7a",
				kind.NameTag:   "bolder #e0a458",
				"literalStrin": "#a3c585",
				"mine":         "#d0877",
				"zest":         "bold",
			},
		}.Theme()
		require.EqualError(t, err, "empty name\n"+
			"unknown mode: mode is unset\n"+
			`color "bg": invalid color: "#2b1d1"`+"\n"+
			`color "error": invalid color: "red"`+"\n"+
			`token for "literalStrin": unknown kind`+"\n"+
			`token for "mine": unknown kind`+"\n"+
			`token for "mine": invalid color: "#d0877"`+"\n"+
			`token for "nameTag": unknown keyword: "bolder"`+"\n"+
			`token for "zest": unknown kind`)
	})

	// A program whose theme file styles kinds of its own takes the colors
	// of the palette to FromColors, which accepts a kind of any name.
	t.Run("leaves custom kinds to FromColors", func(t *testing.T) {
		t.Parallel()

		p := corp
		p.Tokens = map[kind.Kind]theme.Token{"mine": "#00ffff"}

		_, err := p.Theme()
		require.ErrorIs(t, err, theme.ErrUnknownKind)

		colors, err := p.Colors()
		require.NoError(t, err)

		got, err := theme.FromColors(p.Name, colors, p.Tokens)
		require.NoError(t, err)
		assert.Equal(t, "#00ffff bg:#101820", style.Encode(got.Style("mine")))
	})
}

func TestPalette_Decode(t *testing.T) {
	t.Parallel()

	strict := niceyaml.WithUnknownFields(niceyaml.UnknownFieldsReject)

	t.Run("YAML document", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(`name: test-corp
mode: dark
fg: "#e6e6e6"
bg: "#101820"
accent: "#ff6f00"
ok: "#00aa00"
warn: "#ffcc00"
error: "#dd3333"
tokens:
  comment: "italic #7a8590"
  nameTag: "bold #ff6f00"
`, niceyaml.WithName("corp.yaml"))

		got, err := source.Decode[theme.Palette](t.Context(), strict)
		require.NoError(t, err)
		assert.Equal(t, corp, got)
	})

	// The decode reports each bad value where the file holds it, without a
	// validator.
	t.Run("reports each bad value at its place", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(`name: fika
mode: dusk
fg: "#f3e9dc"
bg: "#2b1d1"
tokens:
  comment: "italic #9c8a7a"
  nameTag: "bolder #e0a458"
  literalNumber: "#d0877"
`, niceyaml.WithName("fika.yaml"))

		_, err := source.Decode[theme.Palette](t.Context(), strict)
		require.ErrorIs(t, err, theme.ErrUnknownMode)
		require.ErrorIs(t, err, style.ErrInvalidColor)
		require.ErrorIs(t, err, style.ErrUnknownKeyword)
		require.EqualError(t, err, "fika.yaml: 4 problems\n"+
			`fika.yaml:2:7: $.mode: unknown mode "dusk", want light or dark`+"\n"+
			`fika.yaml:4:5: $.bg: invalid color: "#2b1d1"`+"\n"+
			`fika.yaml:7:12: $.tokens.nameTag: unknown keyword: "bolder"`+"\n"+
			`fika.yaml:8:18: $.tokens.literalNumber: invalid color: "#d0877"`)
	})

	// A decode cannot report a key that the file leaves out or a kind that
	// it misspells, so Theme reports them.
	t.Run("leaves a missing mode and a misspelled kind to Theme", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(`name: fika
bg: "#2b1d14"
tokens:
  literalStrin: "#a3c585"
`, niceyaml.WithName("fika.yaml"))

		got, err := source.Decode[theme.Palette](t.Context(), strict)
		require.NoError(t, err)

		_, err = got.Theme()
		require.EqualError(t, err, "unknown mode: mode is unset\n"+
			`token for "literalStrin": unknown kind`)
	})

	// YAML reads the unquoted color as a comment, so the key holds null
	// and the color stays unset.
	t.Run("unquoted color stays unset", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("name: fika\nmode: dark\nbg: #2b1d14\nok: ~\nwarn: \"\"\n")

		got, err := source.Decode[theme.Palette](t.Context(), strict)
		require.NoError(t, err)
		assert.Equal(t, theme.Palette{Name: "fika", Mode: theme.Dark}, got)
	})

	// A Palette nests in the config type of a program, and the decode
	// reports each error under the key that holds the palette.
	t.Run("nested in a config", func(t *testing.T) {
		t.Parallel()

		type config struct {
			Theme theme.Palette `yaml:"theme"`
		}

		source := niceyaml.NewSourceFromString(`theme:
  name: fika
  mode: dark
  bg: "#2b1d1"
`, niceyaml.WithName("config.yaml"))

		_, err := source.Decode[config](t.Context(), strict)
		require.EqualError(t, err, `config.yaml:4:7: $.theme.bg: invalid color: "#2b1d1"`)
	})

	t.Run("JSON round trip", func(t *testing.T) {
		t.Parallel()

		out, err := json.Marshal(corp)
		require.NoError(t, err)
		assert.JSONEq(t, `{
			"name": "test-corp",
			"mode": "dark",
			"fg": "#e6e6e6",
			"bg": "#101820",
			"accent": "#ff6f00",
			"ok": "#00aa00",
			"warn": "#ffcc00",
			"error": "#dd3333",
			"tokens": {"comment": "italic #7a8590", "nameTag": "bold #ff6f00"}
		}`, string(out))

		var got theme.Palette

		require.NoError(t, json.Unmarshal(out, &got))
		assert.Equal(t, corp, got)
	})

	t.Run("JSON rejects a mistyped color", func(t *testing.T) {
		t.Parallel()

		var got theme.Palette

		err := json.Unmarshal([]byte(`{"name":"fika","mode":"dark","bg":"#2b1d1"}`), &got)
		require.ErrorIs(t, err, style.ErrInvalidColor)
	})
}
