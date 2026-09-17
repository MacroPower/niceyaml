package theme

import (
	"errors"
	"fmt"
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

// Theme is a named color theme with its mode and a lazily built
// [style.Styles].
//
// Look one up by name with [Get] or enumerate them with [All]. Create custom
// themes with [New] and add them to the registry with [Register].
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

var (
	// ErrRegistered is returned by [Register] when a theme with the same name
	// already exists.
	ErrRegistered = errors.New("theme already registered")

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
			result = append(result, New(name, p.Mode, p.styles))
		}

		result = append(result, New("charm", Dark, Charm))

		slices.SortFunc(result, func(a, b Theme) int {
			return strings.Compare(a.Name, b.Name)
		})

		return result
	}()
)

// Register adds a custom theme to the registry, where [Get] and [All] find
// it. It returns [ErrRegistered] when a built-in or an earlier custom theme
// already has the same name and leaves the registry unchanged in that case.
//
// Register is safe for concurrent use.
func Register(t Theme) error {
	if _, builtin := builtinIndex[t.Name]; builtin {
		return fmt.Errorf("%w: %q", ErrRegistered, t.Name)
	}

	customMu.Lock()
	defer customMu.Unlock()

	if _, exists := customThemes[t.Name]; exists {
		return fmt.Errorf("%w: %q", ErrRegistered, t.Name)
	}

	customThemes[t.Name] = t
	customOrder = append(customOrder, t.Name)

	return nil
}

// Get returns the [Theme] registered under name. The boolean reports whether
// one was found.
func Get(name string) (Theme, bool) {
	if t, ok := builtinIndex[name]; ok {
		return t, true
	}

	customMu.RLock()
	defer customMu.RUnlock()

	t, ok := customThemes[name]

	return t, ok
}

// All returns every registered [Theme], built-in ones first in alphabetical
// order followed by custom ones in registration order. Filter by
// [Theme.Mode] to list the themes for one background.
func All() []Theme {
	customMu.RLock()
	defer customMu.RUnlock()

	result := make([]Theme, 0, len(themes)+len(customOrder))
	result = append(result, themes...)

	for _, name := range customOrder {
		result = append(result, customThemes[name])
	}

	return result
}
