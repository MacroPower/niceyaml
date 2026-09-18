// Package yamlviewport provides a Bubble Tea component for viewing YAML with
// syntax highlighting, revision history, and diff visualization.
//
// # Usage
//
// Create a viewport, set dimensions, and load content:
//
//	m := yamlviewport.New()
//	m.SetWidth(80)
//	m.SetHeight(24)
//	m.SetRevision(niceyaml.NewSourceFromString(yamlContent))
//
// The viewport implements [tea.Model], so embed it in your Bubble Tea
// application and forward messages to [Model.Update].
//
// A [Revision] is a name and a view of the content. A [niceyaml.Source] is
// one, and [NewRevision] makes one from a decorated view, so a viewer shows
// a document with its error marks in place:
//
//	view := source.View()
//	for _, bound := range validationErrors {
//		_ = bound.Annotate(view)
//	}
//
//	m.SetRevision(yamlviewport.NewRevision(source.Name(), view))
//
// Search highlights go on a clone of the view, so the marks stay and the
// view itself is never changed.
//
// # Revision History
//
// The viewport tracks multiple versions of a document using
// [Model.AddRevision].
//
// Users can navigate between revisions with Tab/Shift+Tab
// (configurable via [KeyMap]).
//
// When viewing revision N>0, the viewport automatically computes and displays
// a diff.
//
//	m.AddRevision(niceyaml.NewSourceFromString(v1, niceyaml.WithName("v1")))
//	m.AddRevision(niceyaml.NewSourceFromString(v2, niceyaml.WithName("v2")))
//	// Now showing diff between v1 and v2.
//
// Three diff modes control how comparisons are made:
//
//   - [DiffModeAdjacent]: Compare with the previous revision (default).
//   - [DiffModeOrigin]: Compare with the first revision.
//   - [DiffModeNone]: Show current revision without diff markers.
//
// Set [ViewModeHunks] via [Model.SetViewMode] to render a condensed diff
// showing only changed lines with surrounding context, or
// [ViewModeSideBySide] to render the before and after content in two
// panes. [Model.ToggleViewMode] cycles through all three modes.
//
// # Search
//
// Call [Model.SetSearchTerm] to highlight matches.
// Navigate between matches with [Model.SearchNext] and [Model.SearchPrevious].
// The viewport automatically scrolls to center the current match.
//
// Search highlighting uses [style.GenericHighlightDim] for regular matches and
// [style.GenericHighlight] for the current match.
//
// Configure these styles in your theme (see the
// [go.jacobcolvin.com/niceyaml/style/theme] package).
//
// # Customization
//
// Provide a custom [printer.Printer] via [WithPrinter] to control syntax
// highlighting, line numbers, and annotations.
//
// Provide a [finder.Finder] via [WithFinder] to change how search terms
// match, such as the normalization applied, or a custom [Searcher] via
// [WithSearcher] for another search implementation.
//
// Keybindings are fully configurable through the [KeyMap] field on [Model].
//
// The viewport supports both keyboard navigation (vim-style by default) and
// mouse wheel scrolling.
package yamlviewport
