package theme

import (
	"errors"
	"fmt"
	"image/color"
	"maps"
	"slices"

	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

var (
	// ErrEmptyName reports a [Palette] without a name.
	ErrEmptyName = errors.New("empty name")

	// ErrUnknownKind reports a token of a [Palette] for a kind that
	// neither [kind.IsPredefined] nor [kind.IsBelowPredefined] accepts.
	ErrUnknownKind = errors.New("unknown kind")
)

// Hex is a color of a [Palette] as text, in the form that
// [style.ParseColor] reads, which is #rgb or #rrggbb. The empty Hex leaves
// its color unset.
//
// A Hex decodes from text, such as a YAML or JSON string, through
// [Hex.UnmarshalText], which rejects text that style.ParseColor rejects. A
// niceyaml decode thus reports a mistyped color where the file holds it.
// [Hex.Color] checks the text again, so it also rejects a mistyped Hex
// that a program sets any other way.
//
// A Hex names an RGB color and cannot name a color of the terminal's own
// palette. A program that wants an ANSI color in a theme sets it in a
// [Colors] value and calls [FromColors].
//
// A type that holds a Hex reads text, so the bound on alias text that
// [Token] describes applies to a niceyaml decode into it.
type Hex string

// UnmarshalText sets h to text when [style.ParseColor] accepts it, and an
// empty text sets the unset Hex. Otherwise it returns the error from
// style.ParseColor, which wraps [style.ErrInvalidColor], and leaves h as
// it was.
func (h *Hex) UnmarshalText(text []byte) error {
	_, err := style.ParseColor(string(text))
	if err != nil {
		return err //nolint:wrapcheck // The doc names the error of style.ParseColor.
	}

	*h = Hex(text)

	return nil
}

// Color returns the color that [style.ParseColor] reads from h. The empty
// Hex gives a [lipgloss.NoColor], which the fields of [Colors] read as
// unset. For any other text that style.ParseColor rejects, Color returns a
// nil color and an error that wraps [style.ErrInvalidColor].
func (h Hex) Color() (color.Color, error) {
	return style.ParseColor(string(h)) //nolint:wrapcheck // The doc names the error of style.ParseColor.
}

// Palette holds a whole theme as plain values. They are the name, the
// [Mode], the colors as [Hex] text, and a [Token] for each kind that the
// theme styles. [Palette.Theme] creates the [Theme] that a Palette
// describes.
//
// Every field decodes from text, so one decode reads a theme from a YAML
// or JSON file. The keys of the file are name, mode, fg, bg, accent, ok,
// warn, error, and tokens:
//
//	name: corp
//	mode: dark
//	fg: "#e6e6e6"
//	bg: "#101820"
//	accent: "#ff6f00"
//	tokens:
//	  comment: "italic #7a8590"
//	  nameTag: "bold #ff6f00"
//
// A program decodes such a file into a Palette, or into a type of its own
// that holds one, and calls Theme on the result:
//
//	pal, err := source.Decode[theme.Palette](ctx,
//		niceyaml.WithUnknownFields(niceyaml.UnknownFieldsReject))
//	if err != nil {
//		return err
//	}
//
//	corp, err := pal.Theme()
//
// The decode rejects a mode that [Mode.UnmarshalText] rejects, a color
// that [Hex.UnmarshalText] rejects, and a token that [Token.UnmarshalText]
// rejects. A niceyaml decode reports each of them where the file holds it.
// Theme then rejects what a decode cannot, such as a file that leaves out
// the name or the mode.
//
// YAML reads an unquoted # after a space as the start of a comment. The
// color in bg: #101820 is thus a comment, the key holds null, and Bg stays
// unset without an error. To report such a key, a program checks each
// color node that the file holds, for example with
// [go.jacobcolvin.com/niceyaml.Each]. Unquoted, the token bold #ff6f00
// reads as bold and loses its color the same way.
//
// Each Token layers over the style its kind takes without the Token, as
// [Token] describes. In a Palette, "nobold" thus turns off the bold of the
// style beneath. A Token that names only attributes keeps the colors of
// that style.
type Palette struct {
	// Tokens holds a [Token] for each kind that the theme styles, as
	// [Derive] reads them. [Palette.Theme] accepts only the kinds that
	// [kind.IsPredefined] or [kind.IsBelowPredefined] accepts.
	Tokens map[kind.Kind]Token `json:"tokens,omitempty" yaml:"tokens,omitempty"`
	// Name is the kebab-case identifier for the theme, such as "monokai".
	// [Palette.Theme] rejects an empty Name.
	Name string `json:"name" yaml:"name"`
	// Fg is the color of the body text, as the Fg of [Colors] describes.
	Fg Hex `json:"fg,omitempty" yaml:"fg,omitempty"`
	// Bg is the background of the theme, as the Bg of [Colors] describes.
	Bg Hex `json:"bg,omitempty" yaml:"bg,omitempty"`
	// Accent colors the plain headings and accented text, as the Accent of
	// [Colors] describes.
	Accent Hex `json:"accent,omitempty" yaml:"accent,omitempty"`
	// OK colors success text and inserted lines, as the OK of [Colors]
	// describes.
	OK Hex `json:"ok,omitempty" yaml:"ok,omitempty"`
	// Warn colors warning text and the locations of warnings, as the Warn
	// of [Colors] describes.
	Warn Hex `json:"warn,omitempty" yaml:"warn,omitempty"`
	// Error colors error text, deleted lines, and the locations of errors,
	// as the Error of [Colors] describes.
	Error Hex `json:"error,omitempty" yaml:"error,omitempty"`
	// Mode is the background the theme targets. [Palette.Theme] rejects an
	// unset Mode.
	Mode Mode `json:"mode" yaml:"mode"`
}

// Colors returns the [Colors] of p, with each [Hex] converted as
// [Hex.Color] converts it and with the [Mode] of p.
//
// Colors returns an error for each color that [style.ParseColor] rejects,
// in the order of the fields and joined with [errors.Join]. Each error
// names the key of its color in quotes, such as color "bg", and wraps
// [style.ErrInvalidColor]. A decoder that calls [Hex.UnmarshalText]
// rejects such a color earlier, when it decodes the file. Colors does not
// check the Mode, which [FromColors] and [Derive] check.
//
// [Palette.Theme] rejects a token for a custom kind that is not below a
// predefined one, such as kind.Kind("mine"). A program whose theme file
// styles such kinds passes the result to FromColors instead, with the name
// and the tokens of p:
//
//	colors, err := pal.Colors()
//	if err != nil {
//		return err
//	}
//
//	corp, err := theme.FromColors(pal.Name, colors, pal.Tokens)
func (p Palette) Colors() (Colors, error) {
	c, errs := p.colors()
	if len(errs) > 0 {
		return Colors{}, errors.Join(errs...)
	}

	return c, nil
}

// Theme creates the [Theme] that p describes. The theme takes the name and
// the [Mode] of p. Its [Theme.Styles] returns the styles that [Derive]
// builds from the colors and the tokens of p. The theme keeps a copy of
// the tokens, so a later change to the map does not change the theme.
//
// Theme checks p when it creates the theme and joins an error for each
// problem with [errors.Join]:
//
//   - An empty Name gives [ErrEmptyName].
//   - A Mode other than [Light] and [Dark], such as the unset Mode of a
//     file without a mode key, gives an error that wraps [ErrUnknownMode].
//   - A color that [style.ParseColor] rejects gives the error that
//     [Palette.Colors] returns for it.
//   - A token for a kind that neither [kind.IsPredefined] nor
//     [kind.IsBelowPredefined] accepts, such as a misspelled name, gives
//     an error that names the kind and wraps [ErrUnknownKind].
//   - A token that [style.Parse] rejects gives an error that names the
//     kind and wraps the error from style.Parse.
//
// The errors for the name, the mode, and the colors come first, in that
// order. The errors for the tokens follow, sorted by kind. A kind with
// both token errors gives the [ErrUnknownKind] error first.
//
// These errors name a field or a kind and not a place in a file. A decode
// reports a bad mode, color, or token earlier and at its place, as
// [Palette] describes. An empty name, an unset mode, and an unknown kind
// reach Theme from any decode. A program that wants one of them reported
// at its place in the file adds a validator for it to the decode.
func (p Palette) Theme() (Theme, error) {
	var errs []error

	if p.Name == "" {
		errs = append(errs, ErrEmptyName)
	}

	err := p.Mode.check()
	if err != nil {
		errs = append(errs, err)
	}

	c, colorErrs := p.colors()
	errs = append(errs, colorErrs...)

	for _, k := range slices.Sorted(maps.Keys(p.Tokens)) {
		if !kind.IsPredefined(k) && !kind.IsBelowPredefined(k) {
			errs = append(errs, fmt.Errorf("token for %q: %w", k, ErrUnknownKind))
		}

		err := checkToken(k, p.Tokens[k])
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return Theme{}, errors.Join(errs...)
	}

	return fromColors(p.Name, c, maps.Clone(p.Tokens)), nil
}

// colors returns the [Colors] of p and an error for each color that
// [style.ParseColor] rejects, in the order of the fields.
func (p Palette) colors() (Colors, []error) {
	var errs []error

	parse := func(key string, h Hex) color.Color {
		c, err := h.Color()
		if err != nil {
			errs = append(errs, fmt.Errorf("color %q: %w", key, err))
		}

		return c
	}

	return Colors{
		Fg:     parse("fg", p.Fg),
		Bg:     parse("bg", p.Bg),
		Accent: parse("accent", p.Accent),
		OK:     parse("ok", p.OK),
		Warn:   parse("warn", p.Warn),
		Error:  parse("error", p.Error),
		Mode:   p.Mode,
	}, errs
}

// unchecked returns the [Colors] of p without checking its colors. It
// converts each color with [lipgloss.Color], which reads most text that
// [style.ParseColor] rejects as unset and reads a number, such as 4, as a
// color. The built-in catalog converts its palettes with unchecked when
// the package initializes. A program that imports the package thus
// converts every built-in color at startup and does not check it.
func (p Palette) unchecked() Colors {
	return Colors{
		Fg:     lipgloss.Color(string(p.Fg)),
		Bg:     lipgloss.Color(string(p.Bg)),
		Accent: lipgloss.Color(string(p.Accent)),
		OK:     lipgloss.Color(string(p.OK)),
		Warn:   lipgloss.Color(string(p.Warn)),
		Error:  lipgloss.Color(string(p.Error)),
		Mode:   p.Mode,
	}
}
