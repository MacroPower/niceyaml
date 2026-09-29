package style

import (
	"maps"

	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style/kind"
)

// emptyStyle is a shared empty style, which the zero [Styles] value
// returns for every kind.
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
// [Styles.Style] is a map lookup. A custom kind, such as one for an
// overlay, resolves through the parent [Inherit] gives it, so it takes the
// style of a predefined kind under any theme, and one with no parent
// resolves to the base style, as [kind.Parent] places it under [kind.Text].
//
// The zero value resolves every kind to an empty style. Create instances
// with [New].
type Styles struct {
	overrides map[kind.Kind]*lipgloss.Style
	parents   map[kind.Kind]kind.Kind
	resolved  map[kind.Kind]*lipgloss.Style
	// Building is true while [New] or [Styles.With] runs options on maps
	// it just made, so the options may write to those maps in place.
	building bool
}

// Option configures a [Styles] value during construction.
//
// An option applied by hand to an existing value changes that value
// alone and leaves every copy of it as it was, including the shared value
// [Default] returns. [Styles.Style] reports the change once [Styles.With]
// resolves inheritance again.
//
// Available options:
//   - [Set]
//   - [Inherit]
type Option func(*Styles)

// Set returns an [Option] that sets the [lipgloss.Style] for a [kind.Kind].
// Kinds below it in the hierarchy inherit it unless a [Set] on the kind
// itself or on a nearer ancestor overrides it.
//
//nolint:gocritic // Value semantics preferred for API ergonomics.
func Set(s kind.Kind, ls lipgloss.Style) Option {
	return func(st *Styles) {
		switch {
		case st.overrides == nil:
			st.overrides = make(map[kind.Kind]*lipgloss.Style, 1)
		case !st.building:
			st.overrides = maps.Clone(st.overrides)
		}

		st.overrides[s] = &ls
	}
}

// Inherit returns an [Option] that places child under parent in the
// hierarchy, in place of the parent [kind.Parent] gives it. A child that no
// [Set] names then resolves to the style of parent, or of the closest set
// ancestor above it. A program that names a kind of its own, such as one
// for an overlay, gives it the look of a predefined kind under any theme
// this way, with no color of its own:
//
//	styles := theme.Charm.Styles().With(
//		style.Inherit(kind.Kind("mine"), kind.GenericHighlight),
//	)
//
// A [Set] on child wins over the inherited style, whichever comes first.
// A later Inherit on the same child replaces the parent. A chain of
// parents that loops before it reaches a set kind resolves to the base
// style.
func Inherit(child, parent kind.Kind) Option {
	return func(st *Styles) {
		switch {
		case st.parents == nil:
			st.parents = make(map[kind.Kind]kind.Kind, 1)
		case !st.building:
			st.parents = maps.Clone(st.parents)
		}

		st.parents[child] = parent
	}
}

// New creates a new [Styles] value with inheritance resolved.
//
// [kind.Text] uses the base style, and every other kind inherits it.
// Use [Set] options to override specific kinds; child kinds inherit from
// their closest set ancestor. Use [Inherit] to place a custom kind under
// a predefined one.
//
//nolint:gocritic // Value semantics preferred for API ergonomics.
func New(base lipgloss.Style, opts ...Option) Styles {
	st := Styles{
		overrides: map[kind.Kind]*lipgloss.Style{kind.Text: &base},
		building:  true,
	}

	for _, opt := range opts {
		opt(&st)
	}

	st.building = false
	st.resolved = st.resolve()

	return st
}

// resolve walks the hierarchy for every predefined kind and every kind an
// option names and returns the map of kind to the style of its closest set
// ancestor. The parents set with [Inherit] take precedence over the
// predefined hierarchy, and a walk that revisits a kind ends at
// [kind.Text]. Only those parents can form a cycle, since the predefined
// hierarchy ends at [kind.Text], so a walk records the kinds it visits
// only when some are set. The overrides must hold [kind.Text].
func (s Styles) resolve() map[kind.Kind]*lipgloss.Style {
	lookup := func(st kind.Kind) *lipgloss.Style {
		var visited map[kind.Kind]bool

		for current := st; ; current = s.parent(current) {
			if ls, ok := s.overrides[current]; ok {
				return ls
			}

			if current == kind.Text || visited[current] {
				return s.overrides[kind.Text]
			}

			if len(s.parents) > 0 {
				if visited == nil {
					visited = make(map[kind.Kind]bool, len(s.parents)+1)
				}

				visited[current] = true
			}
		}
	}

	resolved := make(map[kind.Kind]*lipgloss.Style, len(s.overrides)+len(s.parents))
	resolved[kind.Text] = lookup(kind.Text)

	for st := range kind.All() {
		resolved[st] = lookup(st)
	}

	for st := range s.overrides {
		resolved[st] = lookup(st)
	}

	for st := range s.parents {
		resolved[st] = lookup(st)
	}

	return resolved
}

// parent returns the parent of st: the one [Inherit] set, or otherwise
// the one [kind.Parent] gives.
func (s Styles) parent(st kind.Kind) kind.Kind {
	if p, ok := s.parents[st]; ok {
		return p
	}

	return kind.Parent(st)
}

// Style returns the [lipgloss.Style] for the given [kind.Kind].
//
// A kind that is neither predefined nor set inherits from its parent as a
// predefined kind does. [Inherit] names that parent, and for a kind it
// leaves out, [kind.Parent] gives [kind.Text], so the kind returns the
// base style. An overlay of a custom kind that no option names therefore
// layers the base style over the text it covers. An overlay that replaces
// the style underneath gives that text the colors the base style sets,
// and one that blends tints the text toward those colors. Where the base
// style leaves a color unset, as the pygments theme does, the text keeps
// its own. The text also keeps attributes such as bold, since an overlay
// cannot turn one off. Use [Inherit] to give such a kind the style of a
// predefined kind, or [Set] to give it a style of its own. The zero
// Styles value returns an empty style for every kind.
func (s Styles) Style(st kind.Kind) lipgloss.Style {
	if ls, ok := s.resolved[st]; ok && ls != nil {
		return *ls
	}

	if ls, ok := s.resolved[kind.Parent(st)]; ok && ls != nil {
		return *ls
	}

	return emptyStyle
}

// With returns a copy of the [Styles] with the given options applied and
// inheritance resolved again, so overriding a parent kind also changes
// the children that inherit from it. The receiver is unchanged.
func (s Styles) With(opts ...Option) Styles {
	c := Styles{
		overrides: make(map[kind.Kind]*lipgloss.Style, len(s.overrides)+len(opts)),
		parents:   make(map[kind.Kind]kind.Kind, len(s.parents)+len(opts)),
		building:  true,
	}
	maps.Copy(c.overrides, s.overrides)
	maps.Copy(c.parents, s.parents)

	if _, ok := c.overrides[kind.Text]; !ok {
		c.overrides[kind.Text] = &emptyStyle
	}

	for _, opt := range opts {
		opt(&c)
	}

	c.building = false
	c.resolved = c.resolve()

	return c
}
