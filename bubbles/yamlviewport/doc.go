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
//	m.SetRevisions(niceyaml.NewSourceFromString(yamlContent))
//
// The viewport is a component in the Bubbles convention rather than a
// [tea.Model]. [Model.Update] returns the updated Model, so hold it as a
// field of your own model, forward messages to Update, and store the Model
// it returns.
//
// A [Revision] is a name and a view of the content. A [*niceyaml.Source] is
// one, and [NewRevision] makes one from a decorated view, so a viewer shows
// a document with its error marks in place:
//
//	view := source.View()
//	niceyaml.Annotate(err, view)
//	m.SetRevisions(yamlviewport.NewRevision(source.Name(), view))
//
// Search highlights go on a clone of the view, so the marks stay and the
// viewport never changes the view itself.
//
// # Revision History
//
// The viewport tracks the versions of a document as a history of
// revisions. [Model.SetRevisions] loads a history and shows its last
// revision.
//
// Users can navigate between revisions with Tab and Shift+Tab, and [KeyMap]
// sets those keys.
//
// At a revision index greater than 0, the viewport computes and displays a
// diff.
//
//	m.SetRevisions(
//		niceyaml.NewSourceFromString(v1, niceyaml.WithName("v1")),
//		niceyaml.NewSourceFromString(v2, niceyaml.WithName("v2")),
//	)
//	// Now showing diff between v1 and v2.
//
// A history that grows or changes while the user reads it has a method for
// each case, and neither moves the view. [Model.AppendRevision] adds a
// revision to the end, such as the next version from a watch, and stays on
// the revision on display. A parent that follows the latest revision calls
// [Model.GotoRevision] after it. [Model.ReplaceRevision] swaps one revision
// in place, such as for a view that carries the marks of a validation
// result that arrived after the first render:
//
//	view := sources[i].View()
//	niceyaml.Annotate(err, view)
//	m.ReplaceRevision(i, yamlviewport.NewRevision(sources[i].Name(), view))
//
// The same line stays at the top of the view, and the selected search match
// stays selected.
//
// A [DiffMode] picks the revision the viewport compares with. By default it
// compares each revision with the one before it, and [Model.SetDiffMode]
// can compare with the first revision instead or turn the diff off.
// [Model.DiffBaseIndex] returns the index of the revision the diff
// compares with, and [Model.DiffResult] returns the diff on display. A
// parent reads the changes from that diff, such as to jump to the next
// change or to list the changes, without computing the diff again.
//
// Set [ViewModeHunks] via [Model.SetViewMode] to render a condensed diff
// showing only changed lines with surrounding context, or
// [ViewModeSideBySide] to render the before and after content in two
// panes. [Model.ToggleViewMode] cycles through the view modes. A view
// mode is a presentation of a diff. Without a diff every mode renders the
// revision as [ViewModeFull] does, and the mode takes effect when a diff
// shows.
//
// A line of a diff keeps the overlays and annotations it has in its
// revision. A diff therefore shows the error marks of the lines it holds.
// [Revision] says which revision each line comes from.
//
// # Search
//
// Call [Model.SetSearchTerm] to highlight matches.
// Navigate between matches with [Model.SearchNext] and [Model.SearchPrevious],
// or select one by its index with [Model.SetSearchIndex].
// The viewport automatically scrolls to center the row of the current
// match. With wrapping off, it scrolls sideways only when the match ends
// past the right edge of the first screen. It then centers the start of the
// match. A match that fits the first screen leaves the view at the
// first column. The parent can set a term before the content area has room
// for a row, such as before the first [tea.WindowSizeMsg]. The viewport then
// scrolls to the current match when the area has room.
//
// Search highlighting uses [kind.GenericHighlightDim] for regular matches and
// [kind.GenericHighlight] for the current match.
//
// Configure these styles in your theme (see the
// [go.jacobcolvin.com/niceyaml/style/theme] package).
//
// # Mouse
//
// The viewport scrolls with the mouse wheel and ignores clicks.
// [Model.HitAt] maps a cell of the viewport to a position in a revision.
// The parent model then decides what a click or a hover does there:
//
//	case tea.MouseClickMsg:
//		if hit, ok := m.viewport.HitAt(msg.X, msg.Y); ok {
//			m.selected = hit
//		}
//
// A diff shows lines of two revisions, so the [Hit] names the revision
// that holds the line under the cell. Its position is a place in that
// revision.
//
// [Model.Reveal] scrolls the viewport to a Hit, whether HitAt returned it
// or the parent built it from a position in a revision, such as the place
// of an error. A Reveal before the content area has room for a row waits,
// as a search does, and scrolls when the area has room. A parent can
// therefore reveal the first error before the first [tea.WindowSizeMsg]
// arrives.
//
// # Customization
//
// Provide a custom [printer.Printer] via [WithPrinter] to control syntax
// highlighting, line numbers, and annotations. Without one, the viewport
// renders with [go.jacobcolvin.com/niceyaml/style/theme.Charm] inside the
// box of [printer.WithBox].
//
// Provide a [finder.Finder] via [WithFinder] to change how search terms
// match, such as the normalization it applies, or a custom [Searcher] via
// [WithSearcher] for another search implementation.
//
// Provide a [diff.Differ] via [WithDiffer] to change how the viewport
// compares two revisions, such as with a line key that ignores spaces at
// the end of each line or with another
// [go.jacobcolvin.com/niceyaml/diff/lcs.Algorithm]. Every view mode,
// [Model.DiffStats], and [Model.DiffResult] use that differ.
// [Model.SetDiffer] swaps it at runtime, such as from a key binding that
// toggles whitespace changes.
//
// Configure keybindings through the [KeyMap] field on [Model].
//
// The viewport supports both keyboard navigation (vim-style by default) and
// mouse wheel scrolling.
package yamlviewport
