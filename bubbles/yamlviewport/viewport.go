package yamlviewport

import (
	"cmp"
	"reflect"
	"slices"
	"sort"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	tea "charm.land/bubbletea/v2"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/diff"
	"go.jacobcolvin.com/niceyaml/finder"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

const defaultHorizontalStep = 6

// Searcher builds an [Index] over the lines on display. The viewport loads
// the lines once per change of content and runs every search term through
// the Index it gets back.
//
// [WithFinder] adapts a [finder.Finder] to this interface.
type Searcher interface {
	Load(lines line.Lines) Index
}

// Index finds the [position.Range]s that match a search string in the lines
// it was built from.
//
// See [finder.Index] for an implementation.
type Index interface {
	Find(search string) position.Ranges
}

// finderSearcher adapts a [finder.Finder] to [Searcher].
type finderSearcher struct {
	finder *finder.Finder
}

// Load implements [Searcher].
func (s finderSearcher) Load(lines line.Lines) Index {
	return s.finder.Load(lines)
}

// Revision is one version of a document in the viewport's history: a name
// for the revision picker and a view of its content. A [*niceyaml.Source]
// is a Revision, so a source goes straight to [Model.AddRevision] or
// [Model.SetRevision]. A view that carries decoration, such as the marks
// [niceyaml.SourceError.Annotate] adds, goes in through [NewRevision]:
//
//	view := source.View()
//	for bound := range niceyaml.AllSourceErrors(err) {
//		bound.Annotate(view)
//	}
//
//	m.SetRevision(yamlviewport.NewRevision(source.Name(), view))
//
// The viewport reads the view when the revision, the diff mode, or the view
// mode changes, and never decorates it. Search highlights go on a clone, so
// the marks a caller adds stay, and the caller's view stays as the caller
// left it. Marks added after the viewport read the view show once the
// revision is set again. A diff between two revisions interleaves their
// lines in a view of its own, so decoration shows only while the viewport
// displays a revision without a diff.
//
// The viewport renders the lines the view holds in the order
// [line.View.All] yields them, which is content order, and windows them
// by index. A search covers the content of the view, and the viewport
// keeps the matches on lines the view holds.
//
// See [NewRevision] and [niceyaml.Source] for implementations.
type Revision interface {
	Name() string
	View() *line.View
}

var _ Revision = (*niceyaml.Source)(nil)

// NewRevision creates a new [Revision] from a name and a view of its
// content.
func NewRevision(name string, view *line.View) Revision {
	return revision{name: name, view: view}
}

// revision is the [Revision] that [NewRevision] returns.
type revision struct {
	view *line.View
	name string
}

// Name implements [Revision].
func (r revision) Name() string {
	return r.name
}

// View implements [Revision].
func (r revision) View() *line.View {
	return r.view
}

// DiffMode specifies how the viewport computes diffs between revisions.
//
// Use [Model.SetDiffMode] to change the mode, or [Model.ToggleDiffMode] to
// cycle through modes.
type DiffMode int

const (
	// DiffModeAdjacent compares the current revision with the immediately
	// preceding revision.
	// This is the default mode.
	DiffModeAdjacent DiffMode = iota
	// DiffModeOrigin compares the current revision with the first (origin)
	// revision, so the diff shows cumulative changes.
	DiffModeOrigin
	// DiffModeNone displays the current revision without any diff markers.
	DiffModeNone
)

// ViewMode specifies how the viewport renders diff content.
//
// Use [Model.SetViewMode] to change the mode, or [Model.ToggleViewMode] to
// cycle through modes.
type ViewMode int

const (
	// ViewModeFull displays all lines including unchanged content.
	// This is the default mode.
	ViewModeFull ViewMode = iota
	// ViewModeHunks displays only changed lines with surrounding context.
	ViewModeHunks
	// ViewModeSideBySide displays before and after content in separate panes.
	ViewModeSideBySide
)

// Option configures a [Model].
//
// Available options:
//   - [WithPrinter]
//   - [WithContainerStyle]
//   - [WithSearcher]
//   - [WithFinder]
type Option func(*Model)

// WithPrinter is an [Option] that sets the [*printer.Printer] used for
// rendering. Without it, the viewport creates a default [printer.Printer].
//
// The viewport never modifies the printer. Each render derives a copy with
// [printer.Printer.With] and the viewport's wrap width, so other renderers
// can share the same printer.
func WithPrinter(p *printer.Printer) Option {
	return func(m *Model) {
		m.printer = p
	}
}

// WithContainerStyle is an [Option] that sets the [lipgloss.Style] wrapped
// around the viewport, as [printer.WithContainerStyle] does for a printer.
// See [Model.SetContainerStyle].
//
//nolint:gocritic // hugeParam: Copying.
func WithContainerStyle(s lipgloss.Style) Option {
	return func(m *Model) {
		m.style = s
	}
}

// WithSearcher is an [Option] that sets the [Searcher] that search terms run
// through. Without it, and without [WithFinder], the viewport creates a
// [finder.Finder] with [finder.New], which folds case and ignores
// diacritics. A nil s selects that same default.
func WithSearcher(s Searcher) Option {
	return func(m *Model) {
		m.searcher = s
	}
}

// WithFinder is an [Option] that sets the [finder.Finder] that builds the
// search index. It is [WithSearcher] with f adapted to [Searcher]:
//
//	yamlviewport.WithFinder(finder.New(finder.WithNormalizer(nil)))
//
// A nil f selects the default searcher, as a nil [WithSearcher] does.
func WithFinder(f *finder.Finder) Option {
	if f == nil {
		return WithSearcher(nil)
	}

	return WithSearcher(finderSearcher{finder: f})
}

// New creates a new [Model] with the given options.
func New(opts ...Option) Model {
	var m Model

	for _, opt := range opts {
		opt(&m)
	}

	m.setInitialValues()

	return m
}

// Model is the Bubble Tea model for the YAML viewport.
// Create instances with [New].
//
// A zero Model has no printer, keymap, or searcher. It counts no rows, so
// [Model.View] returns "" and both scroll offsets stay at 0. Searching needs
// the searcher that [New] creates, so a zero Model finds no match for any
// term. Construct every Model with [New] and its [Option]s.
//
// # Rows
//
// The viewport scrolls by rendered row, not by source line. A line that
// wraps to the viewport width or carries an annotation takes several rows,
// and the vertical offset ([Model.YOffset], [Model.SetYOffset], and the
// scroll methods) counts those rows, so every row of the view is reachable.
// The frame of the printer's container style ([printer.WithContainerStyle])
// adds rows above the first line and below the last. Methods named after rows
// ([Model.TotalRowCount], [Model.VisibleRowCount]) count rows; methods named
// after lines ([Model.TotalLineCount], [Model.VisibleLineCount]) count the
// lines of the view.
//
// A layout change, such as a new width, style, printer, or wrap setting,
// keeps the same line at the top of the view. The top row stays at the same
// row of that line, or at its last row if the line no longer has that many.
// A change of content, such as a new revision, diff mode, or view mode,
// scrolls to the top, or to the first search match when the term has one.
type Model struct {
	// The container style applied to the viewport frame.
	style    lipgloss.Style
	printer  *printer.Printer
	searcher Searcher
	// Index over the lines on display, built when they change. In
	// side-by-side mode index covers the left pane and indexRight the
	// right one.
	index      Index
	indexRight Index
	// Revision history; revIndex below selects the revision on display.
	revisions []Revision
	// Cached diff between base and current revision.
	diffResult *diff.Result
	// The content on display before search highlights, for the left pane or
	// main content and for the right pane of a side-by-side diff.
	// In ViewModeFull: the unified diff, or the view of the revision.
	// In ViewModeHunks with diff: the hunks of the diff with their headers.
	// In ViewModeSideBySide with diff: the Before and After views.
	// In ViewModeSideBySide without diff: the view of the revision on the
	// left and nil on the right.
	//
	// A view a Revision hands out is the caller's, so the model never
	// decorates a base. It decorates a clone.
	baseLeft, baseRight *line.View
	// The base views with the search highlights added: fresh clones of
	// baseLeft and baseRight that decorate takes on every change of the term
	// or the selected match. Right is nil when baseRight is.
	left, right *line.View
	// Rendered row counts of the view. Copies of the Model share one cache
	// until a layout change gives a copy its own, so the counts that the
	// value-receiver View fills in stay filled for the Model it copied.
	rows *rowCache
	// Current search query.
	searchTerm string
	// KeyMap contains the keybindings for viewport navigation.
	KeyMap         KeyMap
	searchMatches  []searchMatch
	leftMatches    position.Ranges
	rightMatches   position.Ranges
	horizontalStep int
	revIndex       int
	diffMode       DiffMode
	// MouseWheelDelta is the number of rows to scroll per mouse wheel tick.
	// Default: 3.
	MouseWheelDelta int
	width           int
	searchIndex     int
	yOffset         int
	height          int
	xOffset         int
	viewMode        ViewMode
	hunkContext     int
	// The line at the top of the view and the row within it when a layout
	// change dropped the row counts. A negative row lies in the container
	// frame above the first line.
	anchorLine int
	anchorRow  int
	// Reports that the base views changed since the searcher last loaded
	// them.
	searcherStale bool
	// MouseWheelEnabled enables mouse wheel scrolling.
	// Default: true.
	MouseWheelEnabled bool
	// Wraps lines to the viewport width when true.
	wrapEnabled bool
	// Reports that anchorLine and anchorRow wait for ensureRows to restore
	// them.
	anchored bool
}

func (m *Model) setInitialValues() {
	m.KeyMap = DefaultKeyMap()
	m.MouseWheelEnabled = true
	m.MouseWheelDelta = 3
	m.horizontalStep = defaultHorizontalStep
	m.hunkContext = 3 // Default context lines around diff hunks.
	m.wrapEnabled = true
	m.searchIndex = -1

	if m.printer == nil {
		m.printer = printer.New()
	}

	if m.searcher == nil {
		m.searcher = finderSearcher{finder: finder.New()}
	}

	m.relayout()
}

// Init returns the command that starts the viewport, which is nil because
// the viewport requires no initialization commands. It follows the shape of
// [tea.Model.Init], so a parent model can call it from its own Init.
//
//nolint:gocritic // hugeParam: value receivers match the Bubble Tea update loop.
func (m Model) Init() tea.Cmd {
	return nil
}

// Height returns the height of the viewport.
func (m *Model) Height() int {
	return m.height
}

// SetHeight sets the height of the viewport and clamps the scroll offsets to
// the new bounds.
func (m *Model) SetHeight(h int) {
	// Row counts do not depend on the height, so the cache stays, and
	// ensureRows clamps the offsets on their next read.
	m.height = h
}

// Width returns the width of the viewport.
func (m *Model) Width() int {
	return m.width
}

// SetWidth sets the width of the viewport and clamps the scroll offsets to
// the new bounds.
func (m *Model) SetWidth(w int) {
	m.width = w
	m.relayout()
}

// relayout responds to a layout change, such as a new printer, style, wrap
// setting, or width, without rebuilding the view. It gives the Model an empty
// row count cache and leaves any copy that shares the old cache untouched.
// The next read of the scroll bounds fills the new cache.
//
// Row offsets from the old cache point at other lines once the rows reflow,
// so relayout records the line at the top of the view and the row within it,
// and ensureRows scrolls back to them. An anchor that ensureRows has not
// restored yet stays, so several changes in a row keep the original top line.
func (m *Model) relayout() {
	if !m.anchored && m.rows != nil && len(m.rows.left) > 0 {
		first, _ := m.rowWindow()

		m.anchorLine = min(first, len(m.rows.left)-1)
		m.anchorRow = m.yOffset - m.rows.sums[m.anchorLine]
		m.anchored = true
	}

	m.rows = &rowCache{}
}

// renderPrinter returns the printer to render with: the configured printer
// specialized to the viewport's word wrap setting and the given content
// width less the horizontal frame of the printer's container style, so a
// wrapped line and the frame around it together fit the content area.
//
// Pinning the printer's container to the given width makes the box it draws
// cover the same columns for every window. Without the pin it would shrink
// to the widest row of the window, and a window of short lines would carry
// its border in from the edge of the content area.
//
// A render prints a slice of the view, so renderPrinter sizes the gutter for
// the largest line number of the whole view rather than of the window, and
// every window lines up with the layout. In side-by-side mode both panes
// get the gutter of the longer revision, so the one horizontal offset lands
// on the same content column in both.
func (m *Model) renderPrinter(width int) *printer.Printer {
	wrapWidth := 0
	if m.wrapEnabled {
		wrapWidth = max(0, width-m.printer.ContainerStyle().GetHorizontalFrameSize())
	}

	maxNumber := m.printer.MaxNumber(m.left)

	if m.viewMode == ViewModeSideBySide && m.right != nil {
		maxNumber = max(maxNumber, m.printer.MaxNumber(m.right))
	}

	return m.printer.With(
		printer.WithWrap(wrapWidth),
		printer.WithMaxNumber(maxNumber),
		printer.WithContainerWidth(width),
	)
}

// SetPrinter sets the [*printer.Printer] used for rendering. See
// [WithPrinter].
//
// The view, its diff, and its search matches stay as they are, so switching
// themes costs one render and no diff or search index rebuild.
func (m *Model) SetPrinter(p *printer.Printer) {
	m.printer = p
	m.relayout()
}

// SetRevision replaces the revision history with a single revision.
//
// This is a convenience method equivalent to [Model.ClearRevisions] followed by
// [Model.AddRevision].
func (m *Model) SetRevision(r Revision) {
	m.ClearRevisions()
	m.AddRevision(r)
}

// AddRevision adds a new revision to the history.
// After adding, the viewport moves to the newly added revision.
//
// The viewport displays the view r hands out, decoration included, and
// adds its search highlights to a clone of it, so the view itself stays as
// the caller left it. See [Revision]. A nil r, or an r that holds a nil
// pointer such as a nil [*niceyaml.Source], adds nothing.
func (m *Model) AddRevision(r Revision) {
	if isNilRevision(r) {
		return
	}

	// Copies of a Model share the backing array of revisions, so the append
	// must not write into spare capacity that another copy can reach.
	m.revisions = append(slices.Clip(m.revisions), r)
	m.revIndex = len(m.revisions) - 1

	m.rebuildViews()
}

// isNilRevision reports whether r is nil or holds a nil pointer. A
// [Revision] implemented on a pointer type, such as [*niceyaml.Source],
// reads its fields in Name and View, so a nil one panics there.
func isNilRevision(r Revision) bool {
	if r == nil {
		return true
	}

	v := reflect.ValueOf(r)

	return v.Kind() == reflect.Pointer && v.IsNil()
}

// ClearRevisions removes all revisions from the history.
func (m *Model) ClearRevisions() {
	m.revisions = nil
	m.revIndex = 0
	m.rebuildViews()
}

// RevisionIndex returns the current revision index.
// Returns 0 without revisions.
func (m *Model) RevisionIndex() int {
	return m.revIndex
}

// RevisionName returns the name of the current revision.
// Returns an empty string without revisions.
func (m *Model) RevisionName() string {
	if !m.hasRevision() {
		return ""
	}

	return m.currentRevision().Name()
}

// RevisionNames returns all revision names in order, or nil without
// revisions.
func (m *Model) RevisionNames() []string {
	if len(m.revisions) == 0 {
		return nil
	}

	names := make([]string, len(m.revisions))
	for i, s := range m.revisions {
		names[i] = s.Name()
	}

	return names
}

// GotoRevision navigates to the revision at index, clamped to the valid range.
// Index 0 always shows the first revision without diff markers.
// Index 1 to N-1 shows a diff based on the current [DiffMode].
func (m *Model) GotoRevision(index int) {
	if !m.hasRevision() {
		return
	}

	m.revIndex = clamp(index, 0, len(m.revisions)-1)
	m.rebuildViews()
}

// RevisionCount returns the number of revisions in the history.
func (m *Model) RevisionCount() int {
	return len(m.revisions)
}

// AtFirstRevision reports whether the viewport is at revision index 0.
func (m *Model) AtFirstRevision() bool {
	return m.revIndex == 0
}

// AtLatestRevision reports whether the viewport is at the latest revision.
func (m *Model) AtLatestRevision() bool {
	return m.revIndex >= len(m.revisions)-1
}

// ShowingDiff reports whether the viewport is displaying a diff between
// revisions.
//
// This is true when not at the first revision and [DiffMode] is not
// [DiffModeNone].
func (m *Model) ShowingDiff() bool {
	return m.hasRevision() && m.revIndex > 0 && m.diffMode != DiffModeNone
}

// DiffStats returns the number of added and removed lines in the current
// diff, as [diff.Result.Stats] counts them.
//
// Returns the zero [diff.Stats] when the viewport shows no diff (at first
// revision, diff mode is none, or no revisions exist).
func (m *Model) DiffStats() diff.Stats {
	if !m.ShowingDiff() {
		return diff.Stats{}
	}

	return m.getDiffResult().Stats()
}

// DiffMode returns the current diff display mode.
func (m *Model) DiffMode() DiffMode {
	return m.diffMode
}

// SetDiffMode sets the diff display mode and rebuilds the view.
func (m *Model) SetDiffMode(mode DiffMode) {
	m.diffMode = mode
	m.rebuildViews()
}

// ToggleDiffMode cycles between diff modes.
func (m *Model) ToggleDiffMode() {
	switch m.diffMode {
	case DiffModeAdjacent:
		m.diffMode = DiffModeOrigin
	case DiffModeOrigin:
		m.diffMode = DiffModeNone
	case DiffModeNone:
		m.diffMode = DiffModeAdjacent
	}

	m.rebuildViews()
}

// ViewMode returns the current view mode.
func (m *Model) ViewMode() ViewMode {
	return m.viewMode
}

// SetViewMode sets the view mode and rebuilds the view.
func (m *Model) SetViewMode(mode ViewMode) {
	m.viewMode = mode
	m.rebuildViews()
}

// ToggleViewMode cycles through the view modes in the order [ViewModeFull],
// [ViewModeHunks], [ViewModeSideBySide].
func (m *Model) ToggleViewMode() {
	switch m.viewMode {
	case ViewModeFull:
		m.viewMode = ViewModeHunks
	case ViewModeHunks:
		m.viewMode = ViewModeSideBySide
	default:
		m.viewMode = ViewModeFull
	}

	m.rebuildViews()
}

// HunkContext returns the number of context lines shown around diff hunks.
func (m *Model) HunkContext() int {
	return m.hunkContext
}

// SetHunkContext sets the number of context lines shown around diff hunks
// in [ViewModeHunks], rebuilding the view when that mode is active. Default
// is 3.
func (m *Model) SetHunkContext(n int) {
	m.hunkContext = max(0, n)

	if m.viewMode == ViewModeHunks {
		m.rebuildViews()
	}
}

// WordWrap reports whether lines wrap to the viewport width.
func (m *Model) WordWrap() bool {
	return m.wrapEnabled
}

// SetWordWrap turns word wrapping on or off. Enabling it resets the
// horizontal scroll offset, since wrapped lines never overflow. The default
// is on.
func (m *Model) SetWordWrap(enabled bool) {
	m.wrapEnabled = enabled

	if enabled {
		m.xOffset = 0
	}

	m.relayout()
}

// ToggleWordWrap toggles word wrapping on or off. See [Model.SetWordWrap].
func (m *Model) ToggleWordWrap() {
	m.SetWordWrap(!m.wrapEnabled)
}

// ContainerStyle returns the container style applied to the viewport frame.
func (m *Model) ContainerStyle() lipgloss.Style {
	return m.style
}

// SetContainerStyle sets the container style applied to the viewport frame.
// The frame size changes the content area, so the scroll offsets clamp to
// it.
//
//nolint:gocritic // hugeParam: Copying.
func (m *Model) SetContainerStyle(s lipgloss.Style) {
	m.style = s
	m.relayout()
}

// NextRevision moves to the next revision in history.
// If already at the latest, does nothing.
func (m *Model) NextRevision() { m.seekRevision(1) }

// PreviousRevision moves to the previous revision in history.
// If already at the first (index 0), does nothing.
func (m *Model) PreviousRevision() { m.seekRevision(-1) }

// seekRevision moves the revision index by delta, with boundary checks.
func (m *Model) seekRevision(delta int) {
	if !m.hasRevision() {
		return
	}

	if delta > 0 && m.AtLatestRevision() {
		return
	}

	if delta < 0 && m.AtFirstRevision() {
		return
	}

	m.revIndex = clamp(m.revIndex+delta, 0, len(m.revisions)-1)
	m.rebuildViews()
}

// rebuildViews rebuilds the displayed views from the revision, diff mode, and
// view mode, then refreshes the search state and drops the cached row counts.
// Rendering itself waits for View, which renders only the visible window.
//
// Every change of content goes through rebuildViews. A match index or row
// offset carried over from the old content points at an arbitrary line, so
// the view scrolls to the top, or to the first match in the new content when
// the search term has one.
func (m *Model) rebuildViews() {
	m.diffResult = nil // Invalidate cached diff result.
	m.baseLeft = nil
	m.baseRight = nil
	m.searcherStale = true
	m.searchIndex = -1

	_, needsDiff := m.resolveRevisionSource()

	switch {
	case m.viewMode == ViewModeSideBySide && needsDiff:
		result := m.getDiffResult()
		m.baseLeft = result.Before()
		m.baseRight = result.After()

	case m.viewMode == ViewModeHunks && needsDiff:
		// Hunks holds no lines when the diff has no changes, which leaves
		// the view empty.
		m.baseLeft = m.getDiffResult().Hunks(m.hunkContext)

	default:
		m.baseLeft = m.getDisplayLines()
	}

	m.refreshSearch()

	// The old row counts describe other lines, so the cache starts empty
	// without anchoring to a line of the old content, as relayout would.
	// The horizontal offset describes them too, and a search match in the
	// new content sets it again below.
	m.anchored = false
	m.yOffset = 0
	m.xOffset = 0
	m.rows = &rowCache{}

	m.scrollToCurrentMatch()
}

// refreshSearch recomputes search matches and overlays for the current base
// views without rebuilding them. A zero Model, one not created with [New],
// has no searcher, so it finds no match for any term.
func (m *Model) refreshSearch() {
	if m.baseLeft == nil {
		m.left = nil
		m.right = nil
		m.searchMatches = nil
		m.leftMatches = nil
		m.rightMatches = nil
		m.searchIndex = -1

		return
	}

	switch {
	case m.searcher == nil:
		m.searchMatches = nil
		m.leftMatches = nil
		m.rightMatches = nil

	case m.viewMode == ViewModeSideBySide && m.baseRight != nil:
		m.updateSideBySideSearchState()

	default:
		m.updateSearchState(m.baseLeft)
	}

	m.decorate()
}

// decorate takes a fresh clone of each base view and adds the search
// highlights of the current matches and selection to it. A fresh clone
// carries no highlight of the last term or the last selection, and the
// base, which may be the caller's view, stays as it is.
func (m *Model) decorate() {
	m.left = m.baseLeft.Clone()
	m.right = m.baseRight.Clone()

	if m.left == nil {
		return
	}

	if m.viewMode == ViewModeSideBySide && m.right != nil {
		m.applySideBySideOverlays()
	} else {
		m.applySearchOverlays(m.left)
	}

	// A highlight style may change the width of the text it styles, so the
	// row counts of the old decoration no longer hold.
	m.relayout()
}

// applySearchOverlays adds overlay highlights for all search matches to
// lines, which holds none yet.
func (m *Model) applySearchOverlays(lines *line.View) {
	for i, match := range m.searchMatches {
		if i == m.searchIndex {
			lines.BlendOverlay(kind.GenericHighlight, match.rng)
		} else {
			lines.BlendOverlay(kind.GenericHighlightDim, match.rng)
		}
	}
}

// searchMatch pairs a match range with its source.
type searchMatch struct {
	rng    position.Range
	inLeft bool
}

// updateSideBySideSearchState updates search matches for side-by-side mode.
//
// It combines the matches from both panes and deduplicates them. Equal lines
// count as a single match, while deleted and inserted lines count as separate
// matches.
func (m *Model) updateSideBySideSearchState() {
	if m.searchTerm == "" {
		m.searchMatches = nil
		m.leftMatches = nil
		m.rightMatches = nil

		return
	}

	// Load both panes once per change of content, as the unified path
	// does, so typing a term does not rebuild the indexes on every
	// keystroke.
	if m.searcherStale || m.index == nil || m.indexRight == nil {
		m.index = m.searcher.Load(m.baseLeft.Lines())
		m.indexRight = m.searcher.Load(m.baseRight.Lines())

		m.searcherStale = false
	}

	// Search both panes and cache the results for overlay application.
	m.leftMatches = heldMatches(m.baseLeft, m.index.Find(m.searchTerm))
	m.rightMatches = heldMatches(m.baseRight, m.indexRight.Find(m.searchTerm))

	// Build combined match list. For equal lines, a match appears in both
	// panes at the same position, so we deduplicate by (row, startCol).
	// For deleted/inserted lines, the match only appears in one pane.
	//
	// Track equal-line match positions from the left pane for deduplication.
	equalLinePositions := make(map[position.Position]bool)

	combined := make([]searchMatch, 0, len(m.leftMatches)+len(m.rightMatches))

	for _, match := range m.leftMatches {
		combined = append(combined, searchMatch{rng: match, inLeft: true})

		// Track equal-line matches for deduplication.
		if m.baseLeft.Contains(match.Start.Line) && m.baseLeft.Flag(match.Start.Line) == line.FlagDefault {
			equalLinePositions[match.Start] = true
		}
	}

	// Add matches from the right pane, skipping duplicates on equal lines.
	for _, match := range m.rightMatches {
		if equalLinePositions[match.Start] {
			continue
		}

		combined = append(combined, searchMatch{rng: match, inLeft: false})
	}

	// Sort by position for consistent navigation order. A deleted line and
	// the inserted line that replaced it share a position, and the left
	// pane's match comes first there.
	slices.SortFunc(combined, func(a, b searchMatch) int {
		if a.rng.Start.Line != b.rng.Start.Line {
			return cmp.Compare(a.rng.Start.Line, b.rng.Start.Line)
		}

		if a.rng.Start.Col != b.rng.Start.Col {
			return cmp.Compare(a.rng.Start.Col, b.rng.Start.Col)
		}

		switch {
		case a.inLeft && !b.inLeft:
			return -1
		case !a.inLeft && b.inLeft:
			return 1
		default:
			return 0
		}
	})

	m.searchMatches = combined

	// Adjust search index if matches changed.
	switch {
	case len(m.searchMatches) == 0:
		m.searchIndex = -1
	case m.searchIndex >= len(m.searchMatches), m.searchIndex < 0:
		m.searchIndex = 0
	}
}

// applySideBySideOverlays adds search highlights to both panes, which hold
// none yet.
func (m *Model) applySideBySideOverlays() {
	// Determine the selected match position and whether it's on an equal line.
	var (
		selectedPos                     position.Position
		selectedInLeft, selectedIsEqual bool
	)

	if m.searchIndex >= 0 && m.searchIndex < len(m.searchMatches) {
		selected := m.searchMatches[m.searchIndex]
		selectedPos = selected.rng.Start
		selectedInLeft = selected.inLeft

		// A line is equal when both panes hold it unchanged. The padding
		// a diff puts opposite an inserted or deleted line is empty and
		// carries the default flag too, so one pane alone cannot tell the
		// two apart.
		if l := selectedPos.Line; m.left.Contains(l) && m.right.Contains(l) {
			selectedIsEqual = m.left.Flag(l) == line.FlagDefault &&
				m.right.Flag(l) == line.FlagDefault
		}
	}

	// Apply overlays to both panes using cached matches.
	m.applySideBySidePaneOverlays(m.left, m.leftMatches, selectedPos, selectedInLeft || selectedIsEqual)
	m.applySideBySidePaneOverlays(m.right, m.rightMatches, selectedPos, !selectedInLeft || selectedIsEqual)
}

// applySideBySidePaneOverlays adds search highlights to a single pane,
// which holds none yet. It uses cached matches and showSelected to
// determine the selected style.
func (m *Model) applySideBySidePaneOverlays(
	view *line.View,
	matches position.Ranges,
	selectedPos position.Position,
	showSelected bool,
) {
	if view == nil {
		return
	}

	for _, match := range matches {
		isSelected := match.Start == selectedPos && showSelected
		if isSelected {
			view.BlendOverlay(kind.GenericHighlight, match)
		} else {
			view.BlendOverlay(kind.GenericHighlightDim, match)
		}
	}
}

// updateSearchState updates the searcher and search matches for the given
// lines.
//
// It reloads the searcher only when the lines changed since the last load, so
// typing a search term does not rebuild the index on every keystroke.
func (m *Model) updateSearchState(lines *line.View) {
	if m.searchTerm == "" {
		m.searchMatches = nil
		m.leftMatches = nil
		m.rightMatches = nil

		return
	}

	if m.searcherStale || m.index == nil {
		m.index = m.searcher.Load(lines.Lines())

		m.searcherStale = false
	}

	// Convert ranges to searchMatch structs (the unified path does not
	// use inLeft).
	ranges := heldMatches(lines, m.index.Find(m.searchTerm))
	m.searchMatches = make([]searchMatch, 0, len(ranges))

	for _, rng := range ranges {
		m.searchMatches = append(m.searchMatches, searchMatch{rng: rng})
	}

	// Adjust search index if matches changed.
	switch {
	case len(m.searchMatches) == 0:
		m.searchIndex = -1
	case m.searchIndex >= len(m.searchMatches), m.searchIndex < 0:
		m.searchIndex = 0
	}
}

// heldMatches returns the matches that start on a line view holds. A
// search covers the whole content of the view, and a view over part of a
// document, such as one from [niceyaml.Node.View], holds some of it.
func heldMatches(view *line.View, matches position.Ranges) position.Ranges {
	held := make(position.Ranges, 0, len(matches))

	for _, match := range matches {
		if view.Contains(match.Start.Line) {
			held = append(held, match)
		}
	}

	return held
}

// getDiffBase returns the revision the current one is compared against
// based on the current [DiffMode].
// Returns nil if diff mode is [DiffModeNone] or there are no revisions.
func (m *Model) getDiffBase() Revision {
	if !m.hasRevision() {
		return nil
	}

	switch m.diffMode {
	case DiffModeOrigin:
		return m.revision(0)
	case DiffModeAdjacent:
		return m.revision(m.revIndex - 1)
	default:
		return nil
	}
}

// currentRevision returns the revision on display, or nil without revisions.
func (m *Model) currentRevision() Revision {
	return m.revision(m.revIndex)
}

// revision returns the revision at index, or nil when index is outside the
// history.
func (m *Model) revision(index int) Revision {
	if index < 0 || index >= len(m.revisions) {
		return nil
	}

	return m.revisions[index]
}

// getDisplayLines returns the base view to display for the current
// revision and [DiffMode]: a fresh unified diff, or the view the revision
// hands out, which is the caller's and which the model never decorates.
// Returns nil when there is no revision.
func (m *Model) getDisplayLines() *line.View {
	rev, needsDiff := m.resolveRevisionSource()
	if needsDiff {
		return m.getDiffResult().Unified()
	}

	if rev == nil {
		return nil
	}

	return rev.View()
}

// getDiffResult returns the cached [diff.Result], computing it if nil.
//
// Without a base for the current [DiffMode], the current revision stands in
// for it, which yields an empty diff rather than a nil [Revision].
func (m *Model) getDiffResult() *diff.Result {
	if m.diffResult == nil {
		current := m.currentRevision()

		base := m.getDiffBase()
		if base == nil {
			base = current
		}

		m.diffResult = diff.Diff(heldLines(base.View()), heldLines(current.View()))
	}

	return m.diffResult
}

// heldLines returns the lines v holds as [line.Lines], so a diff of two
// revisions compares the lines each view holds, as the other view modes
// render them, rather than every line of the content the view is over.
func heldLines(v *line.View) line.Lines {
	ls := make([]*line.Line, 0, v.Count())

	for _, l := range v.All() {
		ls = append(ls, l)
	}

	return line.Collect(ls...)
}

// resolveRevisionSource determines which revision to display for the
// current revision state.
//
// Returns (revision, false) when the viewport shows a revision without a
// diff (at the origin, or with diff mode none), (nil, false) without a
// revision, or (nil, true) when the caller must compute a diff.
func (m *Model) resolveRevisionSource() (Revision, bool) {
	if !m.hasRevision() {
		return nil, false
	}

	if !m.ShowingDiff() {
		return m.currentRevision(), false
	}

	return nil, true
}

// paneWidth returns the width lines wrap to: the content width, or in
// side-by-side mode the width of one pane.
func (m *Model) paneWidth() int {
	if m.viewMode == ViewModeSideBySide {
		return max(0, (m.maxWidth()-ansi.StringWidth(sideBySideSeparator))/2)
	}

	return m.maxWidth()
}

// rowCache holds the layout of a view: the row structure the printer
// reports for each pane and the prefix sums that place every line.
type rowCache struct {
	// Row counts of each line the view renders, in content order, for the
	// left and right panes. The right counts are nil outside side-by-side
	// diffs.
	left, right []int
	// The index in the content of the k-th rendered line, ascending.
	indices []int
	// Prefix sums of the taller pane after the top frame, so sums[k] is the
	// first row of the k-th rendered line and the last entry is the first row
	// of the bottom frame. Nil until filled.
	sums []int
	// Layouts of the left and right panes, which the row counts come from
	// and which place a position within its line. The right layout is
	// empty outside side-by-side diffs.
	leftLayout, rightLayout printer.Layout
	// Rows of the printer's container frame above the first line and below
	// the last. A view without lines has no frame rows.
	top, bottom int
}

// total returns the number of rows in a filled cache.
func (c *rowCache) total() int {
	return c.sums[len(c.sums)-1] + c.bottom
}

// ensureRows fills the row count cache when it is empty, scrolls back to the
// row that relayout recorded, and clamps both scroll offsets to the cache's
// bounds. It checks on every call, because a copy of the Model can fill a
// shared cache without restoring or clamping this Model's offsets.
func (m *Model) ensureRows() {
	if m.rows == nil {
		m.rows = &rowCache{}
	}

	if m.rows.sums == nil {
		m.fillRows()
	}

	if m.anchored {
		m.anchored = false

		// The anchored row keeps its place in the line, up to the line's new
		// last row.
		if n := len(m.rows.left); n > 0 {
			k := min(m.anchorLine, n-1)
			m.yOffset = m.rows.sums[k] + min(m.anchorRow, max(0, m.lineRows(k)-1))
		}
	}

	m.yOffset = clamp(m.yOffset, 0, m.rowOffsetLimit())
	m.xOffset = clamp(m.xOffset, 0, m.maxXOffset())
}

// fillRows computes the row counts of the view into the cache. A Model
// without a printer, such as a zero Model, has no rows.
func (m *Model) fillRows() {
	c := m.rows
	c.left, c.right = nil, nil
	c.top, c.bottom = 0, 0

	if m.left != nil && m.printer != nil {
		p := m.renderPrinter(m.paneWidth())

		c.leftLayout = p.Layout(m.left)
		c.indices, c.left = lineRows(c.leftLayout, m.left)

		if m.viewMode == ViewModeSideBySide && m.right != nil {
			c.rightLayout = p.Layout(m.right)
			_, c.right = lineRows(c.rightLayout, m.right)
		}

		// A layout counts the rows before the container style applies. Print
		// adds the container's frame above and below the lines it renders.
		if len(c.left) > 0 {
			frame := p.ContainerStyle()
			c.top = frame.GetMarginTop() + frame.GetBorderTopSize() + frame.GetPaddingTop()
			c.bottom = frame.GetPaddingBottom() + frame.GetBorderBottomSize() + frame.GetMarginBottom()
		}
	}

	sums := make([]int, len(c.left)+1)
	sums[0] = c.top

	for k, rows := range c.left {
		if c.right != nil {
			rows = max(rows, c.right[k])
		}

		sums[k+1] = sums[k] + rows
	}

	c.sums = sums
}

// lineRows returns the number of rows the k-th rendered line takes, which is
// the taller of its two panes in side-by-side mode.
func (m *Model) lineRows(k int) int {
	return m.rows.sums[k+1] - m.rows.sums[k]
}

// rowWindow returns the rendered lines [first, last) that own the rows of the
// visible window, which starts at the vertical offset and is one content
// height tall.
//
// The range holds at least one line whenever the view has lines. No line owns
// a frame row, so a window that lies entirely inside the top or the bottom
// frame still needs a line to render the frame around.
func (m *Model) rowWindow() (int, int) {
	m.ensureRows()

	n := len(m.rows.sums) - 1
	if n == 0 {
		return 0, 0
	}

	top := m.yOffset
	bottom := top + m.maxHeight()

	// The last line starting at or above the top row, and the first line
	// starting at or below the bottom row.
	first := clamp(sort.SearchInts(m.rows.sums, top+1)-1, 0, n-1)
	last := clamp(sort.SearchInts(m.rows.sums, bottom), first+1, n)

	return first, last
}

// window returns the span of content indices that selects the rendered
// lines [first, last) from the view, which holds its lines in ascending
// order, so a slice of the view by the span renders those lines alone.
func (m *Model) window(first, last int) position.Span {
	if first >= last {
		return position.Span{}
	}

	indices := m.rows.indices

	return position.NewSpan(indices[first], indices[last-1]+1)
}

// trimWindow drops the rows above and below the visible window from rows,
// which hold the rendered lines starting at the first line of the window. For
// the first line of the view, rows start with the top frame.
func (m *Model) trimWindow(rows []string, first int) []string {
	start := m.rows.sums[first]
	if first == 0 {
		start = 0
	}

	skip := min(m.yOffset-start, len(rows))
	rows = rows[skip:]

	return rows[:min(len(rows), m.maxHeight())]
}

// trimFrame drops the container frame rows that Print adds around the lines
// [first, last) in rows, except the top frame above the first line of the
// view and the bottom frame below its last line.
func (m *Model) trimFrame(rows []string, first, last int) []string {
	if first > 0 {
		rows = rows[min(m.rows.top, len(rows)):]
	}

	if last < len(m.rows.left) {
		rows = rows[:max(0, len(rows)-m.rows.bottom)]
	}

	return rows
}

// AtTop reports whether the viewport is scrolled to the top.
func (m *Model) AtTop() bool {
	return !m.hasContent() || m.YOffset() <= 0
}

// AtBottom reports whether the viewport is scrolled to or past the bottom.
func (m *Model) AtBottom() bool {
	return !m.hasContent() || m.YOffset() >= m.maxYOffset()
}

// ScrollPercent returns the vertical scroll position as a float between 0 and 1.
func (m *Model) ScrollPercent() float64 {
	if m.left == nil {
		return 1.0
	}

	return scrollPercent(m.YOffset(), m.maxHeight(), m.TotalRowCount())
}

// HorizontalScrollPercent returns the horizontal scroll position as a float
// between 0 and 1. It is 1 while word wrap is on, since wrapped lines never
// overflow the content width.
func (m *Model) HorizontalScrollPercent() float64 {
	if m.left == nil || m.printer == nil || m.wrapEnabled {
		return 1.0
	}

	return scrollPercent(m.XOffset(), m.scrollWidth(), m.rowWidth())
}

// scrollPercent calculates scroll position as a value between 0 and 1.
func scrollPercent(offset, visible, total int) float64 {
	if visible >= total {
		return 1.0
	}

	v := float64(offset) / float64(total-visible)

	return clamp(v, 0, 1)
}

// maxYOffset returns the maximum Y offset, in rows.
func (m *Model) maxYOffset() int {
	m.ensureRows()

	return m.rowOffsetLimit()
}

// rowOffsetLimit returns the last row the view can start at, from a filled
// cache: the row that brings the last row of the view to the bottom of the
// content area, or the last row of the view when the content area is shorter
// than the view is. A content area with no height, which a height of 0 or a
// container frame as tall as the height gives, would otherwise put the
// offset one row past the end of the view.
func (m *Model) rowOffsetLimit() int {
	total := m.rows.total()

	return max(0, min(total-m.maxHeight(), total-1))
}

// lineCount returns the number of lines the view renders.
func (m *Model) lineCount() int {
	return m.left.Count()
}

// maxXOffset returns the maximum X offset, which brings the last column of
// the widest rendered row into view. Wrapped lines never overflow the content
// width, so it is 0 while word wrap is on.
func (m *Model) maxXOffset() int {
	if m.left == nil || m.printer == nil || m.wrapEnabled {
		return 0
	}

	return max(0, m.rowWidth()-m.scrollWidth())
}

// rowWidth returns the width in cells of the widest row the printer renders
// for the view before the container frame applies, over both panes in
// side-by-side mode. It reads the layouts, so annotation rows and wide
// characters count toward the horizontal scroll bound.
func (m *Model) rowWidth() int {
	// Fill the cache without ensureRows, which clamps the horizontal offset
	// through this method.
	if m.rows == nil {
		m.rows = &rowCache{}
	}

	if m.rows.sums == nil {
		m.fillRows()
	}

	return max(m.rows.leftLayout.Width(), m.rows.rightLayout.Width())
}

// lineRows returns the index in the content of each line view holds and
// the number of rows each takes in layout, in content order.
func lineRows(layout printer.Layout, view *line.View) ([]int, []int) {
	indices := make([]int, 0, view.Count())
	rows := make([]int, 0, view.Count())

	for i := range view.All() {
		indices = append(indices, i)
		rows = append(rows, layout.LineRows(i))
	}

	return indices, rows
}

// scrollWidth returns the number of row columns a pane shows at once: the
// pane width less the horizontal frame of the printer's container, which
// cutRow keeps in place.
func (m *Model) scrollWidth() int {
	return max(0, m.paneWidth()-m.printer.ContainerStyle().GetHorizontalFrameSize())
}

// outerSize returns the width and height of the viewport frame: the set
// dimensions, capped by any fixed size on the container style.
func (m *Model) outerSize() (int, int) {
	w, h := m.Width(), m.Height()
	if sw := m.style.GetWidth(); sw != 0 {
		w = min(w, sw)
	}

	if sh := m.style.GetHeight(); sh != 0 {
		h = min(h, sh)
	}

	return w, h
}

// maxWidth returns the content width accounting for frame size.
func (m *Model) maxWidth() int {
	w, _ := m.outerSize()

	return max(0, w-m.style.GetHorizontalFrameSize())
}

// maxHeight returns the content height accounting for frame size.
func (m *Model) maxHeight() int {
	_, h := m.outerSize()

	return max(0, h-m.style.GetVerticalFrameSize())
}

// hasContent reports whether there is content to display.
func (m *Model) hasContent() bool {
	return m.lineCount() > 0
}

// hasRevision reports whether a revision exists.
func (m *Model) hasRevision() bool {
	return len(m.revisions) > 0
}

// canRender reports whether the content area has room for any rows.
func (m *Model) canRender() bool {
	return m.maxHeight() > 0 && m.maxWidth() > 0
}

// visibleRows renders the rows of the visible window, trimmed to the content
// height and without the padding renderContent adds.
func (m *Model) visibleRows() []string {
	if !m.canRender() || !m.hasContent() {
		return nil
	}

	first, last := m.rowWindow()
	if first >= last {
		return nil
	}

	p := m.renderPrinter(m.maxWidth())
	rows := splitLines(p.Print(m.left.Slice(m.window(first, last))))
	rows = m.trimWindow(m.trimFrame(rows, first, last), first)

	// Without wrapping, lines may exceed the viewport width. Cut them to the
	// horizontal window so lipgloss does not wrap them.
	if !m.wrapEnabled {
		maxWidth := m.maxWidth()

		for i := range rows {
			rows[i] = m.cutRow(rows[i], m.rowOffset(m.yOffset+i), maxWidth)
		}
	}

	return rows
}

// rowOffset returns the horizontal offset of the rendered row r: 0 for a row
// of the container frame above the first line or below the last, which holds
// no content, and the horizontal scroll offset for a row of a line.
func (m *Model) rowOffset(r int) int {
	if r < m.rows.top || r >= m.rows.total()-m.rows.bottom {
		return 0
	}

	return m.xOffset
}

// cutRow fits a rendered row to width by cutting its content columns to the
// horizontal window that starts at offset. The columns of the printer's
// container frame on either side stay in place, so a border keeps its edges
// while the content between them scrolls.
func (m *Model) cutRow(row string, offset, width int) string {
	frame := m.printer.ContainerStyle()
	left := frame.GetMarginLeft() + frame.GetBorderLeftSize() + frame.GetPaddingLeft()
	right := frame.GetHorizontalFrameSize() - left

	inner := ansi.StringWidth(row) - left - right
	if inner < 0 {
		return ansi.Cut(row, 0, width)
	}

	visible := max(0, width-left-right)

	// A pane of the side-by-side view is padded to its own widest row,
	// while the offset runs to the widest row of either pane, so the
	// window can reach past the content of this row. Filling the window
	// keeps the right frame in its column.
	content := ansi.Cut(ansi.Cut(row, left, left+inner), offset, offset+visible)
	if padding := visible - ansi.StringWidth(content); padding > 0 {
		content += m.printer.Style(kind.Text).Render(strings.Repeat(" ", padding))
	}

	return ansi.Cut(row, 0, left) + content + ansi.Cut(row, left+inner, left+inner+right)
}

// SetYOffset sets the vertical offset, in rows, clamped to the scrollable
// range.
func (m *Model) SetYOffset(n int) {
	m.yOffset = clamp(n, 0, m.maxYOffset())
}

// YOffset returns the vertical offset, the index of the rendered row at the
// top of the viewport.
func (m *Model) YOffset() int {
	m.ensureRows()

	return m.yOffset
}

// SetXOffset sets the X offset.
func (m *Model) SetXOffset(n int) {
	m.xOffset = clamp(n, 0, m.maxXOffset())
}

// XOffset returns the current X offset.
func (m *Model) XOffset() int {
	m.ensureRows()

	return m.xOffset
}

// ScrollDown moves the view down by n rows.
func (m *Model) ScrollDown(n int) {
	if m.AtBottom() || n == 0 {
		return
	}

	m.SetYOffset(m.YOffset() + n)
}

// ScrollUp moves the view up by n rows.
func (m *Model) ScrollUp(n int) {
	if m.AtTop() || n == 0 {
		return
	}

	m.SetYOffset(m.YOffset() - n)
}

// PageDown moves the view down by one page.
func (m *Model) PageDown() {
	m.ScrollDown(m.maxHeight())
}

// PageUp moves the view up by one page.
func (m *Model) PageUp() {
	m.ScrollUp(m.maxHeight())
}

// HalfPageDown moves the view down by half a page, and by one row when half
// a page rounds down to none.
func (m *Model) HalfPageDown() {
	if h := m.maxHeight(); h > 0 {
		m.ScrollDown(max(1, h/2))
	}
}

// HalfPageUp moves the view up by half a page, and by one row when half a
// page rounds down to none.
func (m *Model) HalfPageUp() {
	if h := m.maxHeight(); h > 0 {
		m.ScrollUp(max(1, h/2))
	}
}

// ScrollLeft moves the viewport left by n columns.
func (m *Model) ScrollLeft(n int) {
	m.SetXOffset(m.XOffset() - n)
}

// ScrollRight moves the viewport right by n columns.
func (m *Model) ScrollRight(n int) {
	m.SetXOffset(m.XOffset() + n)
}

// SetHorizontalStep sets the horizontal scroll step size.
func (m *Model) SetHorizontalStep(n int) {
	m.horizontalStep = max(0, n)
}

// GotoTop scrolls to the top.
func (m *Model) GotoTop() {
	m.SetYOffset(0)
}

// GotoBottom scrolls to the bottom.
func (m *Model) GotoBottom() {
	m.SetYOffset(m.maxYOffset())
}

// TotalLineCount returns the number of lines in the view. In [ViewModeHunks]
// that is the lines inside a hunk, not the whole diff.
func (m *Model) TotalLineCount() int {
	return m.lineCount()
}

// VisibleLineCount returns the number of lines with at least one row on
// screen. It reports at least 1 whenever the view has lines, since a window
// that lies wholly inside the container frame still renders around a line.
func (m *Model) VisibleLineCount() int {
	if !m.canRender() || !m.hasContent() {
		return 0
	}

	first, last := m.rowWindow()

	return last - first
}

// TotalRowCount returns the number of rendered rows in the view. It counts
// the rows of every line, wrapped to the viewport width, plus their
// annotations and the frame of the printer's container style.
func (m *Model) TotalRowCount() int {
	m.ensureRows()

	return m.rows.total()
}

// VisibleRowCount returns the number of rendered rows on screen, without the
// padding that fills the rest of the viewport height.
func (m *Model) VisibleRowCount() int {
	if !m.canRender() {
		return 0
	}

	return clamp(m.TotalRowCount()-m.YOffset(), 0, m.maxHeight())
}

// SetSearchTerm sets the search term and updates highlights.
// If the term is empty, clears all search highlights.
//
// The term stays set across content changes. A new revision, diff mode, or
// view mode starts the search over at the first match in the new content and
// scrolls to it. A new term likewise starts at its first match, while
// setting the same term again keeps the current match.
func (m *Model) SetSearchTerm(term string) {
	if term == "" {
		m.ClearSearch()

		return
	}

	// The match index of another term points at an arbitrary match of this
	// one. The same term keeps the current match and the scroll position,
	// so a parent that pushes the term on every update does not snap the
	// view back to the match.
	if term == m.searchTerm {
		m.refreshSearch()

		return
	}

	m.searchIndex = -1
	m.searchTerm = term
	m.refreshSearch()
	m.scrollToCurrentMatch()
}

// SearchTerm returns the current search term.
func (m *Model) SearchTerm() string {
	return m.searchTerm
}

// ClearSearch removes all search highlights and clears the search term.
func (m *Model) ClearSearch() {
	m.searchTerm = ""
	m.searchMatches = nil
	m.searchIndex = -1
	m.refreshSearch()
}

// SearchNext navigates to the next search match.
func (m *Model) SearchNext() {
	m.navigateSearch(1)
}

// SearchPrevious navigates to the previous search match.
func (m *Model) SearchPrevious() {
	m.navigateSearch(-1)
}

// navigateSearch moves the search index by delta, wrapping around.
func (m *Model) navigateSearch(delta int) {
	if len(m.searchMatches) == 0 {
		return
	}

	m.searchIndex = (m.searchIndex + delta + len(m.searchMatches)) % len(m.searchMatches)

	// Update overlays; rendering happens lazily in View.
	m.decorate()

	m.scrollToCurrentMatch()
}

// SearchIndex returns the current search match index (0-based), or -1 if no
// matches.
func (m *Model) SearchIndex() int {
	return m.searchIndex
}

// SearchCount returns the total number of search matches.
func (m *Model) SearchCount() int {
	return len(m.searchMatches)
}

// scrollToCurrentMatch scrolls to center the row that holds the start of the
// current search match in the viewport.
func (m *Model) scrollToCurrentMatch() {
	if m.searchIndex < 0 || m.searchIndex >= len(m.searchMatches) {
		return
	}

	match := m.searchMatches[m.searchIndex]
	m.ensureRows()

	// The row of the match within its line comes from the layout of the
	// pane it is in, and the line's first row from the sums that place the
	// taller of the two panes.
	layout := m.rows.leftLayout
	if m.right != nil && !match.inLeft {
		layout = m.rows.rightLayout
	}

	matchRow := layout.RowOf(match.rng.Start)
	if matchRow < 0 {
		return
	}

	i := layout.LineAt(matchRow)
	k, _ := slices.BinarySearch(m.rows.indices, i)
	row := m.rows.sums[k] + matchRow - layout.LineStart(i)

	// Use (maxHeight-1)/2 to ensure the match appears at the visual center.
	// For height 22: (22-1)/2 = 10, placing the match at position 10 (middle).
	// For height 21: (21-1)/2 = 10, placing the match at position 10 (middle).
	m.SetYOffset(row - (m.maxHeight()-1)/2)

	if m.wrapEnabled {
		return
	}

	// With wrap off every line is one row that starts at the gutter, so
	// the cell of the match is the gutter plus the width of the content
	// before its column, and the offset centers that cell the way the Y
	// offset centers its row. SetXOffset clamps, so a match inside the
	// first screen keeps the offset at 0.
	view := m.left
	if m.right != nil && !match.inLeft {
		view = m.right
	}

	if view == nil || match.rng.Start.Line < 0 || match.rng.Start.Line >= view.Lines().Len() {
		return
	}

	content := view.Lines().Line(match.rng.Start.Line).Content()
	x := layout.GutterWidth() + printer.ColWidth(content, match.rng.Start.Col)

	m.SetXOffset(x - (m.scrollWidth()-1)/2)
}

// Update processes Bubble Tea messages and returns the updated model.
//
//nolint:gocritic // hugeParam: value receivers match the Bubble Tea update loop.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, m.KeyMap.PageDown):
			m.PageDown()

		case key.Matches(msg, m.KeyMap.PageUp):
			m.PageUp()

		case key.Matches(msg, m.KeyMap.HalfPageDown):
			m.HalfPageDown()

		case key.Matches(msg, m.KeyMap.HalfPageUp):
			m.HalfPageUp()

		case key.Matches(msg, m.KeyMap.Down):
			m.ScrollDown(1)

		case key.Matches(msg, m.KeyMap.Up):
			m.ScrollUp(1)

		case key.Matches(msg, m.KeyMap.Left):
			m.ScrollLeft(m.horizontalStep)

		case key.Matches(msg, m.KeyMap.Right):
			m.ScrollRight(m.horizontalStep)

		case key.Matches(msg, m.KeyMap.NextRevision):
			m.NextRevision()

		case key.Matches(msg, m.KeyMap.PreviousRevision):
			m.PreviousRevision()

		case key.Matches(msg, m.KeyMap.ToggleDiffMode):
			m.ToggleDiffMode()

		case key.Matches(msg, m.KeyMap.ToggleViewMode):
			m.ToggleViewMode()

		case key.Matches(msg, m.KeyMap.ToggleWordWrap):
			m.ToggleWordWrap()
		}

	case tea.MouseWheelMsg:
		if !m.MouseWheelEnabled {
			break
		}

		// Handle shift+wheel for horizontal scrolling.
		if msg.Mod.Contains(tea.ModShift) {
			switch msg.Button {
			case tea.MouseWheelDown:
				m.ScrollRight(m.horizontalStep)
			case tea.MouseWheelUp:
				m.ScrollLeft(m.horizontalStep)
			}

			break
		}

		switch msg.Button {
		case tea.MouseWheelDown:
			m.ScrollDown(m.MouseWheelDelta)
		case tea.MouseWheelUp:
			m.ScrollUp(m.MouseWheelDelta)
		case tea.MouseWheelLeft:
			m.ScrollLeft(m.horizontalStep)
		case tea.MouseWheelRight:
			m.ScrollRight(m.horizontalStep)
		}
	}

	return m, nil
}

// getViewDimensions returns (width, height, ok). If ok is false, the content
// area has no room for rows, because a dimension is zero or negative or the
// frame of the container style takes all of it, and View renders "".
func (m *Model) getViewDimensions() (int, int, bool) {
	if !m.canRender() {
		return 0, 0, false
	}

	return m.maxWidth(), m.maxHeight(), true
}

// renderContent applies styling and renders lines into final output. It clips
// a row wider than contentW, a width the printer's gutter can force on a
// viewport of only a few columns.
func (m *Model) renderContent(lines []string, contentW, contentH int) string {
	textStyle := m.printer.Style(kind.Text)

	contents := textStyle.
		Width(contentW).
		Height(contentH).
		MaxWidth(contentW).
		MaxHeight(contentH).
		Render(strings.Join(lines, "\n"))

	return m.style.
		UnsetWidth().UnsetHeight().
		Render(contents)
}

// View renders the viewport. A zero Model, one not created with [New],
// renders "".
//
// The rendering behavior depends on the current [ViewMode]:
//   - [ViewModeFull]: Renders all lines (default behavior).
//   - [ViewModeHunks]: Renders only changed lines, with [Model.HunkContext]
//     lines of context around each change.
//   - [ViewModeSideBySide]: Renders before and after content in separate panes.
//
//nolint:gocritic // hugeParam: value receivers match the Bubble Tea update loop.
func (m Model) View() string {
	if m.printer == nil {
		return ""
	}

	w, h, ok := m.getViewDimensions()
	if !ok {
		return ""
	}

	if m.viewMode == ViewModeSideBySide {
		return m.renderSideBySide(w, h)
	}

	return m.renderContent(m.visibleRows(), w, h)
}

// sideBySideSeparator is the column divider between panes.
const sideBySideSeparator = " │ "

// renderSideBySide renders the side-by-side view with two panes.
func (m *Model) renderSideBySide(contentW, contentH int) string {
	// Get views for both panes. The model owns both, with overlays applied.
	if !m.hasContent() {
		return m.renderContent(nil, contentW, contentH)
	}

	right := m.right
	if right == nil {
		// Without a diff, show the same content on both sides.
		right = m.left
	}

	// Need room for separator plus at least 1 character per pane.
	paneWidth := m.paneWidth()
	if paneWidth < 1 {
		return m.renderContent(nil, contentW, contentH)
	}

	first, last := m.rowWindow()
	if first >= last {
		return m.renderContent(nil, contentW, contentH)
	}

	// Render the lines of the window in both panes.
	window := m.window(first, last)
	p := m.renderPrinter(paneWidth)

	leftRows := m.trimFrame(splitLines(p.Print(m.left.Slice(window))), first, last)
	rightRows := m.trimFrame(splitLines(p.Print(right.Slice(window))), first, last)
	blank := m.blankPaneRow(p)

	// Get text style for padding empty areas.
	textStyle := m.printer.Style(kind.Text)

	// Build separator with any extra padding from odd width.
	separatorWidth := ansi.StringWidth(sideBySideSeparator)
	extraPadding := (contentW - separatorWidth) % 2
	separator := textStyle.Render(sideBySideSeparator + strings.Repeat(" ", extraPadding))

	combined := make([]string, 0, m.rows.sums[last]-m.rows.sums[first])

	var li, ri int

	// Join the next count rows of the panes, taking at most leftCount rows
	// from the left pane and rightCount from the right, with their content
	// columns scrolled to offset.
	appendRows := func(count, leftCount, rightCount, offset int) {
		for i := range count {
			// A pane out of rows shows the blank row, which carries the
			// container's frame. Horizontal scrolling cuts the content
			// columns of a rendered row; the blank row has none, and its
			// frame already sits where it belongs.
			left, right := blank, blank

			if i < leftCount && li < len(leftRows) {
				left = leftRows[li]
				li++

				if !m.wrapEnabled {
					left = m.cutRow(left, offset, paneWidth)
				}
			}

			if i < rightCount && ri < len(rightRows) {
				right = rightRows[ri]
				ri++

				if !m.wrapEnabled {
					right = m.cutRow(right, offset, paneWidth)
				}
			}

			// Pad left pane to consistent width for alignment. Both panes
			// are cut to the pane width, which the gutter of a pane row
			// can exceed on its own, so the joined row fits the content
			// width.
			leftPadded := ansi.Truncate(left, paneWidth, "")
			if padding := paneWidth - ansi.StringWidth(leftPadded); padding > 0 {
				leftPadded += textStyle.Render(strings.Repeat(" ", padding))
			}

			combined = append(combined, leftPadded+separator+ansi.Truncate(right, paneWidth, ""))
		}
	}

	if first == 0 {
		appendRows(m.rows.top, m.rows.top, m.rows.top, 0)
	}

	// Zip the panes line by line. A line that wraps taller in one pane gets
	// blank rows in the other, so the panes stay aligned.
	for k := first; k < last; k++ {
		leftCount := m.rows.left[k]

		rightCount := leftCount
		if m.rows.right != nil {
			rightCount = m.rows.right[k]
		}

		appendRows(m.lineRows(k), leftCount, rightCount, m.xOffset)
	}

	if last == len(m.rows.left) {
		appendRows(m.rows.bottom, m.rows.bottom, m.rows.bottom, 0)
	}

	combined = m.trimWindow(combined, first)

	return m.renderContent(combined, contentW, contentH)
}

// blankPaneRow renders the row a side-by-side pane shows where it has run
// out of rows, because the line wraps taller in the other pane. It is one
// empty content row with the frame of p's container style around it, so the
// pane keeps its border down the whole window. A container without a
// horizontal frame gives "", as a pane of spaces would render anyway.
func (m *Model) blankPaneRow(p *printer.Printer) string {
	rows := splitLines(p.Print(m.left.Slice(position.NewSpan(0, 0))))
	if m.rows.top >= len(rows) {
		return ""
	}

	return rows[m.rows.top]
}

func clamp[T cmp.Ordered](v, low, high T) T {
	return min(high, max(low, v))
}

// splitLines splits content by newlines. Unlike [strings.Split], it does not
// produce an empty trailing element when the content ends with a newline.
func splitLines(content string) []string {
	if content == "" {
		return nil
	}

	return strings.Split(strings.TrimSuffix(content, "\n"), "\n")
}
