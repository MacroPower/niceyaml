package main

import (
	"flag"
	"fmt"
	"image/color"
	"os"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/lucasb-eyer/go-colorful"

	_ "embed"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/finder"
	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/style/kind"
	"go.jacobcolvin.com/niceyaml/style/theme"
)

//go:embed demo.yaml
var example string

func main() {
	// A program lets its user pick the theme and the color profile with
	// flags. Try -theme dracula, -color 16, or -theme list. No theme has the
	// name list, so the program prints the names of every theme it knows.
	themeName := flag.String("theme", "nord", "name of a built-in theme")
	profileName := flag.String("color", "auto", "auto, always, 256, 16, or never")

	flag.Parse()

	fmt.Println("\nPick a color profile:")

	// The Profile field of lipgloss.Writer overrides the detected profile
	// for lipgloss.Println, as the Color Profiles section of the printer docs
	// describes. A forced profile writes its colors even into a pipe, so
	// -color always behaves as --color=always does in other tools.
	profiles := map[string]colorprofile.Profile{
		"auto":   lipgloss.Writer.Profile,
		"always": colorprofile.TrueColor,
		"256":    colorprofile.ANSI256,
		"16":     colorprofile.ANSI,
		"never":  colorprofile.NoTTY,
	}

	profile, ok := profiles[*profileName]
	if !ok {
		fmt.Printf("No color profile is named %q.\n", *profileName)

		return
	}

	lipgloss.Writer.Profile = profile
	fmt.Println("Profile:", profile)

	fmt.Println("\nPick a theme by name:")

	// Get matches the name exactly, so Nord finds nothing. Mode returns a
	// new Catalog of the themes for one background.
	catalog := theme.Builtin()

	t, ok := catalog.Get(*themeName)
	if !ok {
		fmt.Printf("No theme is named %q.\n", *themeName)

		for _, m := range []theme.Mode{theme.Dark, theme.Light} {
			fmt.Printf("Themes for a %s terminal:\n", m)

			for _, entry := range catalog.Mode(m).All() {
				fmt.Println(" ", entry.Name)
			}
		}

		return
	}

	fmt.Printf("%s is one of %d themes for a %s terminal.\n", t.Name, catalog.Mode(t.Mode).Len(), t.Mode)

	// A theme paints its surface behind every kind without a fill of its
	// own. The style of each such kind thus holds the surface as its
	// background.
	styles := t.Styles()
	fmt.Println("Keys:", style.Encode(styles.Style(kind.NameTag)))

	source := niceyaml.NewSourceFromString(example)

	// Mark a search match. A highlight paints a fill of its own, so it
	// shows on any background. A blended highlight mixes its fill with the
	// surface beneath it. Without a surface, the fill shows unmixed and
	// looks stronger.
	view := source.View()
	view.BlendOverlay(kind.GenericHighlight, finder.New().Load(source.Lines()).Find("semla")...)

	fmt.Println("\nPrint in a box:")

	// Without the box, the surface ends where the content of each row ends.
	// The box fills the rest of the rectangle around the rows with it.
	boxed := printer.New(printer.WithStyles(styles), printer.WithBox())
	lipgloss.Println(boxed.Print(view))

	fmt.Println("\nPrint on the terminal's own background:")

	// Without its surface, the text of a theme meets the terminal's own
	// background, so the theme reads well only on a terminal of its mode.
	// The program asks the terminal for its background with
	// lipgloss.BackgroundColor, which returns an error when it cannot ask.
	// Outside Windows, lipgloss.BackgroundColor cannot ask when stdin or
	// stdout is not a terminal, such as a pipe. When the terminal answers
	// but does not report its background, lipgloss.BackgroundColor returns
	// a nil color without an error.
	// A Bubble Tea program requests the background with the
	// tea.RequestBackgroundColor command and receives a
	// tea.BackgroundColorMsg.
	bg, err := lipgloss.BackgroundColor(os.Stdin, os.Stdout)

	switch {
	case err != nil || bg == nil:
		fmt.Println("The program cannot learn the background of the terminal.")
	case isDark(bg) != (t.Mode == theme.Dark):
		fmt.Printf("The background of this terminal does not suit %s.\n", t.Name)
	}

	// Under the 16-color profile, the keys of nord take the color of its
	// surface and vanish from the box, but they show here. The Surfaces
	// section of the theme docs covers the collision.
	plain := printer.New(printer.WithStyles(styles.With(style.WithSurface(nil))))
	lipgloss.Println(plain.Print(view))
}

// isDark reports whether the HSL lightness of c is less than one half, the
// test that lipgloss.HasDarkBackground applies to a background.
func isDark(c color.Color) bool {
	col, _ := colorful.MakeColor(c)
	_, _, lightness := col.Hsl()

	return lightness < 0.5
}
