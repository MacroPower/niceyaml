package main

import (
	"context"
	"errors"
	"fmt"

	"charm.land/lipgloss/v2"
	"github.com/goccy/go-yaml/token"

	_ "embed"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/style/kind"
	"go.jacobcolvin.com/niceyaml/style/theme"
)

var (
	//go:embed fika.yaml
	exampleFika string

	//go:embed broken.yaml
	exampleBroken string
)

// themeFile is the type a theme file decodes into. Each key under tokens
// names a kind, and each value holds the token for that kind, so the map
// goes to [theme.FromColors] as it is.
type themeFile struct {
	Name   string                    `yaml:"name"`
	Tokens map[kind.Kind]theme.Token `yaml:"tokens"`
	Colors struct {
		Fg     string `yaml:"fg"`
		Bg     string `yaml:"bg"`
		Accent string `yaml:"accent"`
		OK     string `yaml:"ok"`
		Warn   string `yaml:"warn"`
		Error  string `yaml:"error"`
	} `yaml:"colors"`
	Mode theme.Mode `yaml:"mode"`
}

func main() {
	ctx := context.Background()
	p := printer.New()

	// A theme.Token rejects a spec that style.Parse rejects when it decodes.
	// The validators run before the decoder, and a validator that fails ends
	// the decode before the decoder reads the tokens. The validToken rule
	// thus decodes each token. The decode then reports the problems of the
	// tokens together with those of the colors, the kinds, and the mode.
	// Key selects the keys of the tokens rather than their values. Each
	// passes when its path selects no Node, so it cannot catch a missing
	// mode. The knownMode rule checks the whole file instead.
	tokenPath := paths.Current().Child("tokens").ChildAll()
	rules := niceyaml.WithValidator(niceyaml.MultiValidator(
		niceyaml.ValidatorFunc(knownMode),
		niceyaml.Each(paths.Current().Child("colors").ChildAll(), niceyaml.ValidatorFunc(readableColor)),
		niceyaml.Each(tokenPath.Key(), niceyaml.ValidatorFunc(knownKind)),
		niceyaml.Each(tokenPath, niceyaml.ValidatorFunc(validToken)),
	))

	// The decoder rejects a key that no field reads, such as acent in place
	// of accent.
	strict := niceyaml.WithUnknownFields(niceyaml.UnknownFieldsReject)

	fmt.Println("\nBuild a theme from its file:")

	source := niceyaml.NewSourceFromString(exampleFika, niceyaml.WithName("fika.yaml"))

	fikaFile, err := source.Decode[themeFile](ctx, rules, strict)
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	// FromColors creates a theme whose styles Derive builds from the colors
	// and the tokens. Derive sets the headings, the highlights, the diff
	// lines, and the marks of errors from the colors. It then layers each
	// token over the style of its kind. The readableColor rule has checked
	// each color, so lipgloss.Color reads it as style.ParseColor does.
	fika, err := theme.FromColors(fikaFile.Name, theme.Colors{
		Fg:     lipgloss.Color(fikaFile.Colors.Fg),
		Bg:     lipgloss.Color(fikaFile.Colors.Bg),
		Accent: lipgloss.Color(fikaFile.Colors.Accent),
		OK:     lipgloss.Color(fikaFile.Colors.OK),
		Warn:   lipgloss.Color(fikaFile.Colors.Warn),
		Error:  lipgloss.Color(fikaFile.Colors.Error),
		Mode:   fikaFile.Mode,
	}, fikaFile.Tokens)
	if err != nil {
		fmt.Println(err)

		return
	}

	// Catalog.With adds the theme to a copy of the catalog, so a picker
	// lists it beside the built-in themes. A theme with the name of a
	// built-in one replaces it.
	catalog := theme.Builtin().With(fika)
	fmt.Printf("The catalog holds %d themes, %s among them.\n", catalog.Len(), fika.Name)

	fikaPrinter := printer.New(printer.WithStyles(fika), printer.WithBox())
	lipgloss.Println(fikaPrinter.Print(source.View()))

	fmt.Println("\nPrint the kinds the theme derives:")

	// A kind that neither the file nor Derive styles takes the style of its
	// parent. The double-quoted strings thus take the token of
	// literalString. The line numbers inherit from ui. Derive gives ui the
	// color of the comments but not their italics.
	//
	// Derive gives the highlights the hue of the accent where a tint of
	// that hue stands apart from the background and keeps the text on it
	// readable. A dark tint of amber does not stand apart from the brown of
	// fika, so the highlights turn magenta. A file that wants highlights of
	// its own sets backgrounds in tokens for genericHighlight and
	// genericHighlightDim. It also sets them for genericHeadingAccent and
	// genericHeadingSubtle, which draw on the same tints.
	fmt.Printf("%-20s %-14s %s\n", "KIND", "PARENT", "STYLE")

	for _, k := range []kind.Kind{
		kind.GenericHeading, kind.GenericHighlight, kind.GenericInserted, kind.GenericError,
		kind.TextSubtle, kind.LiteralStringDouble, kind.UILineNumber,
	} {
		fmt.Printf("%-20s %-14s %s\n", k, kind.Parent(k), style.Encode(fika.Style(k)))
	}

	fmt.Println("\nReject a broken theme file:")

	// MultiValidator runs every rule, and Each checks every Node that its
	// path selects, so the decode reports each mistake in the file.
	broken := niceyaml.NewSourceFromString(exampleBroken, niceyaml.WithName("broken.yaml"))
	_, err = broken.Decode[themeFile](ctx, rules, strict)
	lipgloss.Println(p.PrintError(err))
}

// knownMode rejects a theme file without a mode, with a null mode, or with
// a mode that is neither dark nor light. Without the rule, a file without
// a mode or with mode: ~ decodes as [theme.Light], the zero [theme.Mode].
func knownMode(ctx context.Context, n *niceyaml.Node) error {
	modePath := paths.Current().Child("mode")

	// A null never reaches UnmarshalText, but it sets a pointer to nil, so
	// the rule decodes into a *theme.Mode. The decode rejects a name other
	// than dark and light.
	var mode *theme.Mode

	found, err := n.DecodeIfPresent(ctx, modePath, &mode)
	if err != nil {
		return fmt.Errorf("decode mode: %w", err)
	}

	if !found {
		return niceyaml.NewError("mode is missing")
	}

	if mode == nil {
		return niceyaml.NewError("mode is empty", niceyaml.AtPath(modePath))
	}

	return nil
}

// readableColor rejects a color that is null or empty, or that
// style.ParseColor rejects. ParseColor reads an empty string as unset, but
// each color that the file names must hold a value. The rule decodes into
// a new string, and a null leaves it empty. YAML reads an unquoted color,
// such as bg: #2b1d14, as a comment that leaves the key null. The rule
// gives the hint about the "#" only when a comment stands in place of the
// value, and not for a null such as bg: ~.
func readableColor(ctx context.Context, n *niceyaml.Node) error {
	text, err := n.Decode[string](ctx)
	if err != nil {
		return fmt.Errorf("decode color: %w", err)
	}

	if text == "" {
		if tks := n.Tokens(); len(tks) > 0 && tks[0].Type == token.CommentType {
			return niceyaml.NewError("color is empty",
				niceyaml.WithDetails(errors.New(`YAML reads an unquoted "#" as the start of a comment`)))
		}

		return niceyaml.NewError("color is empty")
	}

	// Invalid returns nil for a color that ParseColor accepts.
	_, err = style.ParseColor(text)

	return niceyaml.Invalid(err)
}

// knownKind rejects a key that does not name a predefined kind. FromColors
// accepts a token for a kind of any name, so a misspelled key styles a kind
// that no renderer draws.
func knownKind(ctx context.Context, n *niceyaml.Node) error {
	name, err := n.Decode[kind.Kind](ctx)
	if err != nil {
		return fmt.Errorf("decode kind: %w", err)
	}

	if kind.IsPredefined(name) {
		return nil
	}

	return niceyaml.NewError("unknown kind")
}

// validToken rejects a token that style.Parse rejects, through the decode
// into a [theme.Token].
func validToken(ctx context.Context, n *niceyaml.Node) error {
	_, err := n.Decode[theme.Token](ctx)
	if err != nil {
		return fmt.Errorf("decode token: %w", err)
	}

	return nil
}
