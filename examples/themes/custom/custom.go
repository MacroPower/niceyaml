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

	//go:embed incomplete.yaml
	exampleIncomplete string

	// The decoder rejects a key that no field of a theme.Palette reads,
	// such as acent in place of accent.
	strict = niceyaml.WithUnknownFields(niceyaml.UnknownFieldsReject)
)

func main() {
	ctx := context.Background()
	p := printer.New()

	fmt.Println("\nBuild a theme from its file:")

	// A theme.Palette holds a whole theme file. Its mode, its colors, and
	// its tokens each reject bad text when they decode. The decode thus
	// reports each of them at its place without a validator.
	source := niceyaml.NewSourceFromString(exampleFika, niceyaml.WithName("fika.yaml"))

	pal, err := source.Decode[theme.Palette](ctx, strict)
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	// Theme creates a theme whose styles Derive builds from the colors and
	// the tokens. Derive sets the headings, the highlights, the diff
	// lines, and the marks of errors from the colors. It then layers each
	// token over the style of its kind.
	fika, err := pal.Theme()
	if err != nil {
		fmt.Println(err)

		return
	}

	// Catalog.With adds the theme to a copy of the catalog, so a picker
	// lists it beside the built-in themes. A theme with the name of a
	// built-in one replaces it.
	catalog := theme.Builtin().With(fika)
	fmt.Printf("The catalog holds %d themes, %s among them.\n", catalog.Len(), fika.Name)

	fikaPrinter := printer.New(printer.WithStyles(fika), printer.WithBox(true))
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

	// Two optional rules report what a Palette cannot report at its place
	// in the file. The hasValue rule rejects a key without a value, such as
	// a color that YAML reads as a comment. The knownKind rule rejects a
	// misspelled kind, which Theme otherwise reports by name alone. Key
	// selects the keys of the tokens rather than their values.
	//
	// The validators run before the decoder, and a validator that fails
	// ends the decode before the decoder reads the file. The decodes rule
	// thus decodes the palette as well. The decode then reports the
	// problems of the mode, the colors, and the tokens together with those
	// of the two rules.
	rules := niceyaml.WithValidator(niceyaml.MultiValidator(
		niceyaml.ValidatorFunc(decodes),
		niceyaml.Each(paths.Current().RecursiveAll(), niceyaml.ValidatorFunc(hasValue)),
		niceyaml.Each(paths.Current().Child("tokens").ChildAll().Key(), niceyaml.ValidatorFunc(knownKind)),
	))

	broken := niceyaml.NewSourceFromString(exampleBroken, niceyaml.WithName("broken.yaml"))
	_, err = broken.Decode[theme.Palette](ctx, rules, strict)
	lipgloss.Println(p.PrintError(err))

	fmt.Println("\nReject a theme file without a name and a mode:")

	// A decode cannot report a key that the file leaves out, so the name
	// and the mode stay unset. Theme rejects both, without a place in the
	// file.
	incomplete := niceyaml.NewSourceFromString(exampleIncomplete, niceyaml.WithName("incomplete.yaml"))

	pal, err = incomplete.Decode[theme.Palette](ctx, rules, strict)
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	_, err = pal.Theme()
	fmt.Println(err)
}

// decodes rejects a theme file that does not decode into a [theme.Palette].
func decodes(ctx context.Context, n *niceyaml.Node) error {
	_, err := n.Decode[theme.Palette](ctx, strict)

	return err
}

// hasValue rejects a key that holds a null. A null leaves the field of a
// [theme.Palette] unset without an error, and a theme file has no use for
// one. YAML reads an unquoted color, such as bg: #2b1d14, as a comment
// that leaves the key null. The rule gives the hint about the "#" only
// when a comment stands in place of the value, and not for a null such as
// bg: ~.
func hasValue(ctx context.Context, n *niceyaml.Node) error {
	value, err := n.Decode[any](ctx)
	if err != nil {
		return fmt.Errorf("decode value: %w", err)
	}

	if value != nil {
		return nil
	}

	if tks := n.Tokens(); len(tks) > 0 && tks[0].Type == token.CommentType {
		return niceyaml.NewError("value is empty",
			niceyaml.WithDetails(errors.New(`YAML reads an unquoted "#" as the start of a comment`)))
	}

	return niceyaml.NewError("value is empty")
}

// knownKind rejects a key that names neither a predefined kind nor a kind
// below one. [theme.Palette.Theme] rejects such a key too, but it names
// the kind without a place in the file.
func knownKind(ctx context.Context, n *niceyaml.Node) error {
	name, err := n.Decode[kind.Kind](ctx)
	if err != nil {
		return fmt.Errorf("decode kind: %w", err)
	}

	if kind.IsPredefined(name) || kind.IsBelowPredefined(name) {
		return nil
	}

	return niceyaml.NewError("unknown kind")
}
