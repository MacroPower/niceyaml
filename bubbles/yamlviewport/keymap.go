package yamlviewport

import "charm.land/bubbles/v2/key"

// KeyMap defines the keybindings for the viewport.
//
// Keybindings are optional. Methods such as [Model.ScrollDown] and
// [Model.ScrollUp] control the viewport from code.
//
// Use [DefaultKeyMap] to get a set of default keybindings. A parent that
// wants one of the default keys for something else replaces that
// binding or disables it with [key.Binding.SetEnabled].
//
// SearchNext and SearchPrevious step through the matches of the
// current search term. The parent sets and clears the term with
// [Model.SetSearchTerm] and [Model.ClearSearch], because it owns the
// search prompt and its cancel key.
type KeyMap struct {
	// PageDown scrolls down by one page.
	PageDown key.Binding
	// PageUp scrolls up by one page.
	PageUp key.Binding
	// HalfPageUp scrolls up by half a page.
	HalfPageUp key.Binding
	// HalfPageDown scrolls down by half a page.
	HalfPageDown key.Binding
	// Down scrolls down by one row.
	Down key.Binding
	// Up scrolls up by one row.
	Up key.Binding
	// Left scrolls left by the horizontal step.
	Left key.Binding
	// Right scrolls right by the horizontal step.
	Right key.Binding
	// GotoTop scrolls to the top.
	GotoTop key.Binding
	// GotoBottom scrolls to the bottom.
	GotoBottom key.Binding
	// SearchNext selects the next search match.
	SearchNext key.Binding
	// SearchPrevious selects the previous search match.
	SearchPrevious key.Binding
	// NextRevision navigates to the next revision.
	NextRevision key.Binding
	// PreviousRevision navigates to the previous revision.
	PreviousRevision key.Binding
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
		GotoTop: key.NewBinding(
			key.WithKeys("home", "g"),
			key.WithHelp("g/home", "go to start"),
		),
		GotoBottom: key.NewBinding(
			key.WithKeys("end", "G"),
			key.WithHelp("G/end", "go to end"),
		),
		SearchNext: key.NewBinding(
			key.WithKeys("n"),
			key.WithHelp("n", "next match"),
		),
		SearchPrevious: key.NewBinding(
			key.WithKeys("N"),
			key.WithHelp("N", "prev match"),
		),
		NextRevision: key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("tab", "next revision"),
		),
		PreviousRevision: key.NewBinding(
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
