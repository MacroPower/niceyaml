package theme

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/exp/charmtone"

	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

// Mode is the background a theme targets. Its text form is "light" or
// "dark", so a Mode decodes from a YAML or JSON string, and
// [flag.TextVar] reads one from a flag.
//
// A decoder does not call [Mode.UnmarshalText] for a missing or null
// value. The Mode then keeps the value it held, which is [Light] for a
// zero Mode. A program that requires a mode decodes it into a [*Mode]
// and treats nil as missing.
//
// A type that holds a Mode reads text. A niceyaml decode into such a type
// thus holds the aliases of the whole document to the bound on the text
// they repeat, as the Limits section of [go.jacobcolvin.com/niceyaml]
// describes. When the document holds an alias, the decode counts that
// text once for the whole document. A config loader whose documents repeat
// large anchors decodes the mode as a string and calls
// [Mode.UnmarshalText] itself.
type Mode int

// Background modes.
const (
	// Light marks themes that target light backgrounds.
	Light Mode = iota
	// Dark marks themes that target dark backgrounds.
	Dark
)

// String returns "light" for [Light] and "dark" for [Dark]. For any other
// value, it returns the value in the form Mode(2).
func (m Mode) String() string {
	switch m {
	case Light:
		return "light"
	case Dark:
		return "dark"
	}

	return fmt.Sprintf("Mode(%d)", int(m))
}

// MarshalText returns the name that [Mode.String] returns. For a value
// other than [Light] and [Dark], it returns an error that wraps
// [ErrUnknownMode].
func (m Mode) MarshalText() ([]byte, error) {
	if m != Light && m != Dark {
		return nil, fmt.Errorf("%w: %d", ErrUnknownMode, int(m))
	}

	return []byte(m.String()), nil
}

// UnmarshalText sets m from the name "light" or "dark". For any other
// text, it returns an error that wraps [ErrUnknownMode] and leaves m as it
// was.
func (m *Mode) UnmarshalText(text []byte) error {
	switch string(text) {
	case "light":
		*m = Light
	case "dark":
		*m = Dark
	default:
		return fmt.Errorf("%w %q, want light or dark", ErrUnknownMode, text)
	}

	return nil
}

// Theme is a named color theme with its mode and a lazily built
// [style.Styles]. [Theme.Style] resolves one kind from those styles, so a
// Theme goes wherever a [style.Styler] does, such as
// [go.jacobcolvin.com/niceyaml/printer.WithStyles].
//
// Find one by name with [Catalog.Get], or list them all with
// [Catalog.All]. Create custom themes with [FromColors] or [New] and add
// them to a catalog with [Catalog.With].
type Theme struct {
	// The memoized builder for the [style.Styles]. The first call builds
	// them, and copies of the Theme share the result. It is nil for the
	// zero value.
	styles func() style.Styles
	// Name is the kebab-case identifier for the theme, such as "monokai" or
	// "dracula".
	Name string
	// Mode is the background the theme targets, light or dark.
	Mode Mode
}

// New creates a new [Theme] whose [Theme.Styles] calls build at most once
// and returns the same value afterwards. A nil build gives a Theme whose
// Styles returns a zero [style.Styles].
//
// The theme styles only the kinds that build sets, and every other kind
// takes the style of its closest set ancestor. [FromColors] derives the
// kinds that a renderer paints, such as the diff lines and the highlights,
// from a few colors instead.
func New(name string, mode Mode, build func() style.Styles) Theme {
	t := Theme{Name: name, Mode: mode}
	if build != nil {
		t.styles = sync.OnceValue(build)
	}

	return t
}

// Styles returns the [style.Styles] for the theme. The first call builds it
// and every later call, on this value or a copy of it, returns the same
// result. The zero Theme returns a zero [style.Styles].
func (t Theme) Styles() style.Styles {
	if t.styles == nil {
		return style.Styles{}
	}

	return t.styles()
}

// Style returns the [lipgloss.Style] for st from [Theme.Styles], so a Theme
// is a [style.Styler] and goes to
// [go.jacobcolvin.com/niceyaml/printer.WithStyles] as it is. The zero
// Theme returns an empty style for every kind.
func (t Theme) Style(st kind.Kind) lipgloss.Style {
	return t.Styles().Style(st)
}

var (
	// ErrUnknownMode reports text or a [Mode] value that names neither
	// [Light] nor [Dark].
	ErrUnknownMode = errors.New("unknown mode")

	// Charm is the theme of CharmTone colors. It paints the palette of
	// [style.Default] on a Pepper surface, which [style.Default] leaves
	// out. The catalog lists it so a picker can offer it by name.
	Charm = New("charm", Dark, func() style.Styles {
		return style.Default().With(style.WithSurface(charmtone.Pepper))
	})

	// The catalog of every palette plus charm, built once.
	builtin = func() Catalog {
		themes := make([]Theme, 0, len(palettes)+1)
		for name, p := range palettes {
			themes = append(themes, fromColors(name, p.colors(), p.Tokens))
		}

		themes = append(themes, Charm)

		slices.SortFunc(themes, func(a, b Theme) int {
			return strings.Compare(a.Name, b.Name)
		})

		return Catalog{}.With(themes...)
	}()
)

// Catalog is an ordered set of [Theme] values, one per name.
//
// A Catalog never changes after construction. [Catalog.With] returns a new
// Catalog that holds more themes and leaves the receiver as it was. A
// program therefore builds one at startup and shares it with every picker
// and printer that needs it. The zero Catalog is empty.
//
// Create instances with [Builtin], or from the zero value with
// [Catalog.With].
type Catalog struct {
	index  map[string]int
	themes []Theme
}

// Builtin returns the [Catalog] of every theme this package ships, in name
// order. Add to it with [Catalog.With]:
//
//	catalog := theme.Builtin().With(theme.FromColors("corp", corpColors, corpTokens))
func Builtin() Catalog {
	return builtin
}

// With returns a [Catalog] that holds the themes of the receiver and then
// the given ones. A theme whose name the receiver already holds replaces
// that entry in place, and one with a new name goes on the end. A program
// thus shadows a built-in theme by adding one of the same name. When
// several given themes share a name, the last one wins. With leaves the
// receiver unchanged.
func (c Catalog) With(themes ...Theme) Catalog {
	out := Catalog{
		index:  make(map[string]int, len(c.themes)+len(themes)),
		themes: make([]Theme, 0, len(c.themes)+len(themes)),
	}

	maps.Copy(out.index, c.index)

	out.themes = append(out.themes, c.themes...)

	for _, t := range themes {
		if i, ok := out.index[t.Name]; ok {
			out.themes[i] = t

			continue
		}

		out.index[t.Name] = len(out.themes)
		out.themes = append(out.themes, t)
	}

	return out
}

// Get returns the [Theme] held under name. The boolean reports whether the
// catalog holds one.
func (c Catalog) Get(name string) (Theme, bool) {
	i, ok := c.index[name]
	if !ok {
		return Theme{}, false
	}

	return c.themes[i], true
}

// All returns every [Theme] in the [Catalog], in order. The slice is a
// copy, so reordering it does not change the catalog.
func (c Catalog) All() []Theme {
	return slices.Clone(c.themes)
}

// Mode returns the [Catalog] of the themes that target mode, in the order
// the receiver holds them. A picker uses it to list the themes for one
// background:
//
//	for _, t := range theme.Builtin().Mode(theme.Dark).All() {
//		fmt.Println(t.Name)
//	}
func (c Catalog) Mode(mode Mode) Catalog {
	matches := make([]Theme, 0, len(c.themes))

	for _, t := range c.themes {
		if t.Mode == mode {
			matches = append(matches, t)
		}
	}

	if len(matches) == 0 {
		return Catalog{}
	}

	return Catalog{}.With(matches...)
}

// Len returns the number of themes in the [Catalog].
func (c Catalog) Len() int {
	return len(c.themes)
}
