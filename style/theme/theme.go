package theme

import (
	"maps"
	"slices"
	"strings"
	"sync"

	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

// Mode is the color scheme a theme targets.
type Mode int

// Color scheme modes.
const (
	// Light marks themes designed for light backgrounds.
	Light Mode = iota
	// Dark marks themes designed for dark backgrounds.
	Dark
)

// Theme is a named color theme with its mode and a lazily built
// [style.Styles]. [Theme.Style] resolves one kind from those styles, so a
// Theme goes wherever a
// [go.jacobcolvin.com/niceyaml/printer.StyleGetter] does, such as
// [go.jacobcolvin.com/niceyaml/printer.WithStyles].
//
// Look one up by name with [Catalog.Get] or enumerate them with
// [Catalog.All]. Create custom themes with [New] and add them to a catalog
// with [Catalog.With].
type Theme struct {
	// Memoized builder for the [style.Styles]: the first call builds them
	// and copies of the Theme share the result. Nil for the zero value.
	styles func() style.Styles
	// Name is the kebab-case identifier for the theme (e.g., "monokai",
	// "dracula").
	Name string
	// Mode indicates whether the theme is designed for light or dark
	// backgrounds.
	Mode Mode
}

// New creates a new [Theme] whose [Theme.Styles] calls build at most once
// and returns the same value afterwards. A nil build gives a Theme whose
// Styles returns a zero [style.Styles].
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
// is a [go.jacobcolvin.com/niceyaml/printer.StyleGetter] and goes to
// [go.jacobcolvin.com/niceyaml/printer.WithStyles] as it is. The zero
// Theme returns an empty style for every kind.
func (t Theme) Style(st kind.Kind) lipgloss.Style {
	return t.Styles().Style(st)
}

var (
	// Charm is the theme of CharmTone colors that [style.Default] returns,
	// listed here so a picker can offer it by name.
	Charm = New("charm", Dark, style.Default)

	// The catalog of every palette plus charm, built once.
	builtin = func() Catalog {
		themes := make([]Theme, 0, len(palettes)+1)
		for name, p := range palettes {
			themes = append(themes, New(name, p.Mode, p.styles))
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
// A Catalog never changes after it is built. [Catalog.With] returns a new
// Catalog holding more themes, and the receiver stays as it was, so a
// program builds one at startup and shares it with every picker and
// printer that needs it. The zero Catalog holds no themes.
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
//	catalog := theme.Builtin().With(theme.New("corp", theme.Dark, buildCorp))
func Builtin() Catalog {
	return builtin
}

// With returns a [Catalog] holding the themes of the receiver and then the
// given ones. A theme whose name the receiver already holds replaces that
// entry in place, and one with a new name goes on the end, so a program
// shadows a built-in theme by adding one of the same name. When several
// given themes share a name, the last one wins. The receiver is unchanged.
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

// Get returns the [Theme] held under name. The boolean reports whether one
// was found.
func (c Catalog) Get(name string) (Theme, bool) {
	i, ok := c.index[name]
	if !ok {
		return Theme{}, false
	}

	return c.themes[i], true
}

// All returns every [Theme] in the [Catalog], in order. The slice is a
// copy, so reordering it reaches nothing.
func (c Catalog) All() []Theme {
	return slices.Clone(c.themes)
}

// Mode returns the [Catalog] of the themes designed for mode, in the order
// they hold in the receiver, so a picker lists the themes for one
// background:
//
//	for _, t := range theme.Builtin().Mode(theme.Dark).All() {
//		fmt.Println(t.Name)
//	}
func (c Catalog) Mode(mode Mode) Catalog {
	var out Catalog

	for _, t := range c.themes {
		if t.Mode == mode {
			out = out.With(t)
		}
	}

	return out
}

// Len returns the number of themes in the [Catalog].
func (c Catalog) Len() int {
	return len(c.themes)
}
