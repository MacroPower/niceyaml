package theme

import (
	"slices"
	"strings"
	"sync"

	"go.jacobcolvin.com/niceyaml/style"
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

// Theme is a registry entry: a named color theme with its mode and style
// generator.
//
// Look one up by name with [Get], enumerate them with [All], or use the
// [Styles] and [List] shortcuts when only the styles or the names are needed.
type Theme struct {
	// Styles returns the [style.Styles] for this theme.
	Styles func() style.Styles
	// Name is the kebab-case identifier for the theme (e.g., "monokai", "dracula").
	Name string
	// Mode indicates whether the theme is designed for light or dark backgrounds.
	Mode Mode
}

var (
	customMu     sync.RWMutex
	customThemes = map[string]Theme{}
	// Names in registration order, so All is deterministic.
	customOrder []string

	// Indexes the built-in themes by name.
	builtinIndex = func() map[string]Theme {
		m := make(map[string]Theme, len(themes))
		for _, t := range themes {
			m[t.Name] = t
		}

		return m
	}()

	// The built-in themes in name order: every catalog palette plus charm.
	themes = func() []Theme {
		result := make([]Theme, 0, len(catalog)+1)
		for name, p := range catalog {
			result = append(result, Theme{Styles: p.styles, Name: name, Mode: p.Mode})
		}

		result = append(result, Theme{Styles: Charm, Name: "charm", Mode: Dark})

		slices.SortFunc(result, func(a, b Theme) int {
			return strings.Compare(a.Name, b.Name)
		})

		return result
	}()
)

// Register registers a custom theme by name.
//
// Registered themes become available through [Styles] and [List].
// If a theme with the same name already exists (built-in or custom),
// it is replaced.
//
// Register is safe for concurrent use.
func Register(name string, fn func() style.Styles, mode Mode) {
	customMu.Lock()
	defer customMu.Unlock()

	if _, exists := customThemes[name]; !exists {
		customOrder = append(customOrder, name)
	}

	customThemes[name] = Theme{Styles: fn, Name: name, Mode: mode}
}

// Get returns the [Theme] registered under name. The boolean reports whether
// one was found.
//
// Custom themes registered with [Register] take precedence over built-in
// themes with the same name.
func Get(name string) (Theme, bool) {
	if t, ok := lookupCustom(name); ok {
		return t, true
	}

	t, ok := builtinIndex[name]

	return t, ok
}

// lookupCustom returns the custom theme registered under name.
func lookupCustom(name string) (Theme, bool) {
	customMu.RLock()
	defer customMu.RUnlock()

	t, ok := customThemes[name]

	return t, ok
}

// All returns every registered [Theme], built-in ones first in alphabetical
// order followed by custom ones in registration order. A custom theme that
// shares a built-in name replaces it in the result.
func All() []Theme {
	customMu.RLock()
	defer customMu.RUnlock()

	result := make([]Theme, 0, len(themes)+len(customThemes))

	for _, t := range themes {
		if custom, ok := customThemes[t.Name]; ok {
			result = append(result, custom)

			continue
		}

		result = append(result, t)
	}

	for _, name := range customOrder {
		if _, builtin := builtinIndex[name]; builtin {
			continue
		}

		result = append(result, customThemes[name])
	}

	return result
}

// List returns the names of all themes matching the given [Mode], in
// the order of [All].
//
// Names are kebab-case identifiers (e.g., "monokai", "catppuccin-mocha")
// suitable for passing to [Styles] or [Get].
func List(m Mode) []string {
	var names []string

	for _, t := range All() {
		if t.Mode == m {
			names = append(names, t.Name)
		}
	}

	return names
}

// Styles returns the [style.Styles] for the named theme.
// The boolean reports whether the theme was found.
//
// Styles is shorthand for [Get] followed by calling [Theme.Styles].
func Styles(name string) (style.Styles, bool) {
	t, ok := Get(name)
	if !ok {
		return style.Styles{}, false
	}

	return t.Styles(), true
}
