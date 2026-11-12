package style

import (
	"image/color"
	"maps"

	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style/kind"
)

// emptyStyle is a shared empty style, which the zero [Styles] value
// returns for every kind.
var emptyStyle = lipgloss.NewStyle()

// Styler retrieves the style for each [kind.Kind].
//
// A renderer asks for each kind while it renders. Some renderers, such as
// [go.jacobcolvin.com/niceyaml/printer.Printer], cache the styles they
// blend for overlays by the kinds involved. Style should therefore return
// the same style for a kind for the life of the value.
//
// See [Styles] for an implementation.
type Styler interface {
	Style(s kind.Kind) lipgloss.Style
}

// Styles resolves each [kind.Kind] to the [lipgloss.Style] it renders with.
//
// A Styles value holds a base style plus explicit overrides. [New] and
// [Styles.With] resolve every predefined kind through the inheritance
// hierarchy when they build the value, so [Styles.Style] is a map lookup.
// [Styles.Style] resolves any other kind, such as a custom one for an
// overlay, to the style of its closest resolved ancestor through
// [kind.Parent]. A custom kind below a predefined one, such as
// kind.GenericHighlight.Child("match"), thus takes the style of that
// predefined kind under any theme. A custom kind without a '.' in its name
// resolves to the base style, because [kind.Parent] places it under
// [kind.Text].
//
// A Styles value can also hold a surface, the background color that
// [WithSurface] sets. Every kind whose own style does not set a background
// resolves with the surface behind it. A theme thus paints one surface
// behind all of its text, and kinds with a fill of their own, such as
// [kind.GenericHighlight], keep that fill.
//
// The zero value resolves every kind to an empty style. Create instances
// with [New].
type Styles struct {
	config

	resolved map[kind.Kind]*lipgloss.Style
}

// Option configures a [Styles] value during construction.
//
// Available options:
//   - [Set]
//   - [Update]
//   - [WithSurface]
type Option func(*config)

// config holds the settings an [Option] configures. An option takes it in
// place of the [Styles] that embeds it, so only [New] and [Styles.With]
// can apply one. Each runs it on new maps of its own.
type config struct {
	overrides map[kind.Kind]*lipgloss.Style
	// The background behind every kind that does not set one. Nil leaves
	// the terminal's own background there.
	surface color.Color
}

// Set returns an [Option] that sets the [lipgloss.Style] for a [kind.Kind].
// Each kind below that kind in the hierarchy inherits the style, except
// where a [Set] or an [Update] gives the kind itself or a nearer ancestor
// a style of its own.
//
//nolint:gocritic // Value semantics preferred for API ergonomics.
func Set(s kind.Kind, ls lipgloss.Style) Option {
	return func(c *config) {
		c.overrides[s] = &ls
	}
}

// Update returns an [Option] that adjusts the style of a [kind.Kind]
// relative to the style the kind already has, where [Set] replaces the
// whole style. The option calls fn with the style that k resolves to from
// the options before it. That style is the one a [Set] or an Update gave
// k, or otherwise the style of the closest set ancestor of k through
// [kind.Parent]. The option then sets the style that fn returns for k, as
// [Set] does. With Update, a program
// changes one property of a kind under any theme and keeps the rest:
//
//	italic := style.Update(kind.Comment, func(s lipgloss.Style) lipgloss.Style {
//		return s.Italic(true)
//	})
//
//	styles := theme.Charm().Styles().With(italic)
//
// The style that fn takes does not carry the surface of [WithSurface],
// which [Styles.Style] adds to the styles it returns. The kind therefore
// stays on the surface, and a later [WithSurface] replaces or removes the
// surface behind it as it does for every other kind.
//
// Update depends on the options before it. [New] and [Styles.With] call
// fn one time for each Update they apply, in the order of their options. A
// later [Set] on k replaces the style that fn returned. A later [Set] on
// an ancestor of k does not reach k, because k holds a style of its own
// after the Update. A nil fn leaves k as it was.
func Update(k kind.Kind, fn func(lipgloss.Style) lipgloss.Style) Option {
	return func(c *config) {
		if fn == nil {
			return
		}

		ls := fn(*c.closest(k))
		c.overrides[k] = &ls
	}
}

// WithSurface returns an [Option] that sets the surface of a [Styles]
// value, the background behind every kind whose own style does not set
// one. [Styles.Style] returns such a kind with bg as its background. Kinds
// with a background of their own, such as highlights and headings, keep
// it. A nil bg, or a [lipgloss.NoColor], removes the surface, so every
// kind without a background of its own shows the terminal's own
// background. A later WithSurface replaces the surface.
//
// To remove the surface of a theme, pass nil:
//
//	plain := theme.Charm().Styles().With(style.WithSurface(nil))
//
// The colors of a built-in theme keep their contrast on the surface the
// theme sets. A theme without its surface draws them on the terminal's
// own background instead, where some text might be hard to read.
func WithSurface(bg color.Color) Option {
	if !isColorSet(bg) {
		bg = nil
	}

	return func(c *config) {
		c.surface = bg
	}
}

// New creates a new [Styles] value with inheritance resolved.
//
// [kind.Text] uses the base style, and every other kind inherits it.
// Use [Set] options to override specific kinds. Child kinds inherit from
// their closest set ancestor. Use [Update] to adjust the style a kind
// already has. Use [WithSurface] to paint one background behind every
// kind that does not set one.
//
//nolint:gocritic // Value semantics preferred for API ergonomics.
func New(base lipgloss.Style, opts ...Option) Styles {
	st := Styles{
		overrides: map[kind.Kind]*lipgloss.Style{kind.Text: &base},
	}

	for _, opt := range opts {
		opt(&st.config)
	}

	st.resolved = st.resolve()

	return st
}

// resolve walks the hierarchy for every predefined kind and every kind an
// option names and returns the map of kind to the style of its closest set
// ancestor.
//
// A style that does not set a background resolves with the surface behind
// it when the value holds a surface. The overrides keep the styles as the
// options set them, so a later [WithSurface] replaces the surface of every
// kind.
func (s Styles) resolve() map[kind.Kind]*lipgloss.Style {
	// Kinds that inherit one style share one copy of it with the surface.
	surfaced := map[*lipgloss.Style]*lipgloss.Style{}

	lookup := func(st kind.Kind) *lipgloss.Style {
		ls := s.closest(st)
		if s.surface == nil || isColorSet(ls.GetBackground()) {
			return ls
		}

		if filled, ok := surfaced[ls]; ok {
			return filled
		}

		filled := ls.Background(s.surface)
		surfaced[ls] = &filled

		return &filled
	}

	resolved := make(map[kind.Kind]*lipgloss.Style, len(s.overrides))

	for st := range kind.All() {
		resolved[st] = lookup(st)
	}

	for st := range s.overrides {
		resolved[st] = lookup(st)
	}

	return resolved
}

// closest returns the style of st as the options set it, without the
// surface. That style is the override of st, or otherwise the override of
// its closest set ancestor through [kind.Parent]. The overrides must hold
// [kind.Text].
func (c config) closest(st kind.Kind) *lipgloss.Style {
	for current := st; ; current = kind.Parent(current) {
		if ls, ok := c.overrides[current]; ok {
			return ls
		}

		if current == kind.Text {
			return c.overrides[kind.Text]
		}
	}
}

// Style returns the [lipgloss.Style] for the given [kind.Kind].
//
// A kind that is neither predefined nor set inherits from its ancestors
// as a predefined kind does. Style walks up through [kind.Parent] to the
// closest kind that is predefined or set and returns its style. A custom
// kind below a predefined one, such as one that [kind.Kind.Child] names,
// thus takes the style of that predefined kind. A custom kind without a
// '.' in its name has [kind.Text] as its parent, so Style returns the base
// style for it. An overlay of such a kind layers the base style over the
// text it covers. An overlay that replaces the style underneath gives
// that text the colors the base style sets, and one that blends tints the
// text toward those colors. Where the base style leaves a color unset, as
// the pygments theme does, the text keeps its own. The text also keeps
// attributes such as bold, because an overlay cannot turn one off. Name
// such a kind below a predefined kind to give it that kind's style, or
// use [Set] to give it a style of its own. The zero Styles value returns
// an empty style for every kind.
//
// For a kind whose style does not set a background, Style returns that
// style with the surface of [WithSurface] as its background. A [Set] that
// takes its style from Style gives the kind that surface as a background
// of its own. A later [WithSurface] then leaves that background in place.
// To adjust the style of a kind and keep the kind on the surface, use
// [Update]. Its function takes the style without the surface.
func (s Styles) Style(st kind.Kind) lipgloss.Style {
	for current := st; ; current = kind.Parent(current) {
		if ls, ok := s.resolved[current]; ok && ls != nil {
			return *ls
		}

		if current == kind.Text {
			return emptyStyle
		}
	}
}

// Surface returns the background that [WithSurface] set, or nil when the
// value does not have one. Without a surface, every kind that does not set
// a background shows the terminal's own background.
func (s Styles) Surface() color.Color {
	return s.surface
}

// With returns a copy of the [Styles] with the given options applied and
// inheritance resolved again, so overriding a parent kind also changes
// the children that inherit from it. The copy keeps the surface of the
// receiver unless an option sets another. With leaves the receiver
// unchanged.
func (s Styles) With(opts ...Option) Styles {
	c := Styles{
		overrides: make(map[kind.Kind]*lipgloss.Style, len(s.overrides)+len(opts)),
		surface:   s.surface,
	}
	maps.Copy(c.overrides, s.overrides)

	if _, ok := c.overrides[kind.Text]; !ok {
		c.overrides[kind.Text] = &emptyStyle
	}

	for _, opt := range opts {
		opt(&c.config)
	}

	c.resolved = c.resolve()

	return c
}
