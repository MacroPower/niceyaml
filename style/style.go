package style

import (
	"maps"

	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style/kind"
)

// A shared empty style, returned for lookups of kinds that are
// neither predefined nor set.
var emptyStyle = lipgloss.NewStyle()

// Styles resolves each [kind.Kind] to the [lipgloss.Style] it renders with.
//
// A Styles value holds a base style plus explicit overrides, and resolves every
// predefined kind through the inheritance hierarchy when it is built, so
// [Styles.Style] is a map lookup. Custom kinds, such as one for an overlay,
// are stored as given.
//
// The zero value resolves every kind to an empty style. Create instances
// with [NewStyles].
type Styles struct {
	overrides map[kind.Kind]*lipgloss.Style
	resolved  map[kind.Kind]*lipgloss.Style
}

// StylesOption configures a [Styles] value during construction.
//
// Available options:
//   - [Set]
type StylesOption func(*Styles)

// Set returns a [StylesOption] that sets the [lipgloss.Style] for a [kind.Kind].
// Kinds below it in the hierarchy inherit it unless they are set themselves.
//
//nolint:gocritic // Value semantics preferred for API ergonomics.
func Set(s kind.Kind, ls lipgloss.Style) StylesOption {
	return func(st *Styles) {
		st.overrides[s] = &ls
	}
}

// NewStyles creates a new [Styles] value with inheritance resolved.
//
// The base style is used for [kind.Text] and inherited by every other kind.
// Use [Set] options to override specific kinds; child kinds inherit from
// their closest set ancestor.
//
//nolint:gocritic // Value semantics preferred for API ergonomics.
func NewStyles(base lipgloss.Style, opts ...StylesOption) Styles {
	st := Styles{overrides: map[kind.Kind]*lipgloss.Style{kind.Text: &base}}

	for _, opt := range opts {
		opt(&st)
	}

	st.resolved = resolveStyles(st.overrides)

	return st
}

// resolveStyles walks the hierarchy for every predefined kind and returns
// the map of kind to its closest set ancestor. Custom kinds outside the
// hierarchy resolve to themselves. The overrides must hold [kind.Text].
func resolveStyles(overrides map[kind.Kind]*lipgloss.Style) map[kind.Kind]*lipgloss.Style {
	lookup := func(st kind.Kind) *lipgloss.Style {
		for current := st; ; current = kind.Parent(current) {
			if ls, ok := overrides[current]; ok {
				return ls
			}

			if current == kind.Text {
				return overrides[kind.Text]
			}
		}
	}

	resolved := make(map[kind.Kind]*lipgloss.Style, len(overrides))
	resolved[kind.Text] = lookup(kind.Text)

	for st := range kind.All() {
		resolved[st] = lookup(st)
	}

	for st, ls := range overrides {
		if !kind.IsPredefined(st) {
			resolved[st] = ls
		}
	}

	return resolved
}

// Style returns the [lipgloss.Style] for the given [kind.Kind].
//
// A kind that is neither predefined nor set returns an empty style.
func (s Styles) Style(st kind.Kind) lipgloss.Style {
	if ls, ok := s.resolved[st]; ok && ls != nil {
		return *ls
	}

	return emptyStyle
}

// With returns a copy of the [Styles] with the given options applied and
// inheritance resolved again, so overriding a parent kind also changes
// the children that inherit from it. The receiver is unchanged.
func (s Styles) With(opts ...StylesOption) Styles {
	c := Styles{overrides: make(map[kind.Kind]*lipgloss.Style, len(s.overrides)+len(opts))}
	maps.Copy(c.overrides, s.overrides)

	if _, ok := c.overrides[kind.Text]; !ok {
		c.overrides[kind.Text] = &emptyStyle
	}

	for _, opt := range opts {
		opt(&c)
	}

	c.resolved = resolveStyles(c.overrides)

	return c
}
