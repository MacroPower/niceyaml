package yamlviewport

import "charm.land/bubbles/v2/key"

// KeyMap defines the keybindings for the viewport.
//
// Note that you don't necessarily need to use keybindings at all; the viewport
// can be controlled programmatically with methods like [Model.ScrollDown] and
// [Model.ScrollUp].
//
// Use [DefaultKeyMap] to get a set of default keybindings.
type KeyMap struct {
	// PageDown scrolls down by one page.
	PageDown key.Binding
	// PageUp scrolls up by one page.
	PageUp key.Binding
	// HalfPageUp scrolls up by half a page.
	HalfPageUp key.Binding
	// HalfPageDown scrolls down by half a page.
	HalfPageDown key.Binding
	// Down scrolls down by one line.
	Down key.Binding
	// Up scrolls up by one line.
	Up key.Binding
	// Left scrolls left by the horizontal step.
	Left key.Binding
	// Right scrolls right by the horizontal step.
	Right key.Binding
	// NextRevision navigates to the next revision.
	NextRevision key.Binding
	// PrevRevision navigates to the previous revision.
	PrevRevision key.Binding
	// ToggleDiffMode cycles through diff computation modes.
	ToggleDiffMode key.Binding
	// ToggleViewMode cycles through view rendering modes.
	ToggleViewMode key.Binding
	// ToggleWordWrap toggles word wrapping.
	ToggleWordWrap key.Binding
}

// DefaultKeyMap returns a new [KeyMap] with pager-like default keybindings.
func DefaultKeyMap() KeyMap {
	return KeyMap{
		PageDown: key.NewBinding(
			key.WithKeys("pgdown", "space", "f"),
			key.WithHelp("f/pgdn", "page down"),
		),
		PageUp: key.NewBinding(
			key.WithKeys("pgup", "b"),
			key.WithHelp("b/pgup", "page up"),
		),
		HalfPageUp: key.NewBinding(
			key.WithKeys("u", "ctrl+u"),
			key.WithHelp("u", "½ page up"),
		),
		HalfPageDown: key.NewBinding(
			key.WithKeys("d", "ctrl+d"),
			key.WithHelp("d", "½ page down"),
		),
		Up: key.NewBinding(
			key.WithKeys("up", "k"),
			key.WithHelp("↑/k", "up"),
		),
		Down: key.NewBinding(
			key.WithKeys("down", "j"),
			key.WithHelp("↓/j", "down"),
		),
		Left: key.NewBinding(
			key.WithKeys("left", "h"),
			key.WithHelp("←/h", "move left"),
		),
		Right: key.NewBinding(
			key.WithKeys("right", "l"),
			key.WithHelp("→/l", "move right"),
		),
		NextRevision: key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("tab", "next revision"),
		),
		PrevRevision: key.NewBinding(
			key.WithKeys("shift+tab"),
			key.WithHelp("shift+tab", "prev revision"),
		),
		ToggleDiffMode: key.NewBinding(
			key.WithKeys("m"),
			key.WithHelp("m", "toggle diff mode"),
		),
		ToggleViewMode: key.NewBinding(
			key.WithKeys("v"),
			key.WithHelp("v", "toggle view mode"),
		),
		ToggleWordWrap: key.NewBinding(
			key.WithKeys("w"),
			key.WithHelp("w", "toggle word wrap"),
		),
	}
}
