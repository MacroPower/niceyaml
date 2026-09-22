package style

import (
	"maps"

	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style/kind"
)

// emptyStyle is a shared empty style, returned for lookups of kinds that
// are neither predefined nor set.
var emptyStyle = lipgloss.NewStyle()

// Styler retrieves the style for each [kind.Kind].
//
// A renderer asks for each kind as it renders, and one such as
// [go.jacobcolvin.com/niceyaml/printer.Printer] caches the styles it
// blends for overlays by the kinds involved, so Style should return the
// same style for a kind for the life of the value.
//
// See [Styles] for an implementation.
type Styler interface {
	Style(s kind.Kind) lipgloss.Style
}

// Styles resolves each [kind.Kind] to the [lipgloss.Style] it renders with.
//
// A Styles value holds a base style plus explicit overrides, and resolves every
// predefined kind through the inheritance hierarchy when it is built, so
// [Styles.Style] is a map lookup. A Styles value stores custom kinds, such
// as one for an overlay, as given.
//
// The zero value resolves every kind to an empty style. Create instances
// with [New].
type Styles struct {
	overrides map[kind.Kind]*lipgloss.Style
	resolved  map[kind.Kind]*lipgloss.Style
}

// Option configures a [Styles] value during construction.
//
// Available options:
//   - [Set]
type Option func(*Styles)

// Set returns an [Option] that sets the [lipgloss.Style] for a [kind.Kind].
// Kinds below it in the hierarchy inherit it unless a [Set] on the kind
// itself or on a nearer ancestor overrides it.
//
//nolint:gocritic // Value semantics preferred for API ergonomics.
func Set(s kind.Kind, ls lipgloss.Style) Option {
	return func(st *Styles) {
		if st.overrides == nil {
			st.overrides = make(map[kind.Kind]*lipgloss.Style, 1)
		}

		st.overrides[s] = &ls
	}
}

// New creates a new [Styles] value with inheritance resolved.
//
// [kind.Text] uses the base style, and every other kind inherits it.
// Use [Set] options to override specific kinds; child kinds inherit from
// their closest set ancestor.
//
//nolint:gocritic // Value semantics preferred for API ergonomics.
func New(base lipgloss.Style, opts ...Option) Styles {
	st := Styles{overrides: map[kind.Kind]*lipgloss.Style{kind.Text: &base}}

	for _, opt := range opts {
		opt(&st)
	}

	st.resolved = resolveStyles(st.overrides)

	return st
}

// resolveStyles walks the hierarchy for every predefined kind and returns
// the map of kind to the style of its closest set ancestor. Custom kinds
// outside the hierarchy resolve to their own style. The overrides must
// hold [kind.Text].
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
func (s Styles) With(opts ...Option) Styles {
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
