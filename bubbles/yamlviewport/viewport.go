package yamlviewport

import (
	"cmp"
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
	"go.jacobcolvin.com/niceyaml/normalizer"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/style"
)

const defaultHorizontalStep = 6

// Searcher builds an [Index] over the lines on display. The viewport loads
// the lines once per change of content and runs every search term through
// the Index it gets back.
//
// [WithFinder] adapts a [finder.Finder] to this interface.
type Searcher interface {
	Load(lines line.View) Index
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
func (s finderSearcher) Load(lines line.View) Index {
	return s.finder.Load(lines)
}

// DiffMode specifies how diffs are computed between revisions.
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
	// revision, showing cumulative changes.
	DiffModeOrigin
	// DiffModeNone displays the current revision without any diff markers, showing
	// the plain document content.
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
//   - [WithStyle]
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

// WithStyle is an [Option] that sets the container style for the viewport.
// See [Model.SetStyle].
//
//nolint:gocritic // hugeParam: Copying.
func WithStyle(s lipgloss.Style) Option {
	return func(m *Model) {
		m.style = s
	}
}

// WithSearcher is an [Option] that sets the [Searcher] that search terms run
// through. Without it, and without [WithFinder], the viewport creates a
// [finder.Finder] with a default [normalizer.Normalizer].
func WithSearcher(s Searcher) Option {
	return func(m *Model) {
		m.searcher = s
	}
}

// WithFinder is an [Option] that sets the [finder.Finder] that builds the
// search index. It is [WithSearcher] with f adapted to [Searcher]:
//
//	yamlviewport.WithFinder(finder.New(finder.WithNormalizer(normalizer.New(
//		normalizer.WithDiacriticFold(false),
//	))))
func WithFinder(f *finder.Finder) Option {
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
// the searcher that [New] creates, so construct every Model with [New] and
// its [Option]s.
//
// # Rows and Lines
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
type Model struct {
	// The container style applied to the viewport frame.
	style    lipgloss.Style
	printer  *printer.Printer
	searcher Searcher
	// Index over the lines on display, built when they change.
	index Index
	// Revision history; revIndex below selects the revision on display.
	revisions []*niceyaml.Source
	// Cached diff between base and current revision.
	diffResult *diff.Result
	// Left holds the view for the left pane or main content.
	// In ViewModeFull: Unified diff or plain content.
	// In ViewModeHunks with diff: the hunks of the diff with their headers.
	// In ViewModeSideBySide with diff: Before view.
	// In ViewModeSideBySide without diff: plain content (same on both sides).
	//
	// The model always owns the view, either a clone of the revision's lines
	// or a fresh diff result, so search overlays never touch the caller's
	// Source.
	left line.Lines
	// Right holds the right pane view for side-by-side diff rendering.
	// Only populated when viewMode == ViewModeSideBySide and showing a diff.
	right line.Lines
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
	// FillHeight pads output with empty lines to fill the viewport height when true.
	FillHeight bool
	// Reports that left changed since the searcher last loaded it.
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
		m.searcher = finderSearcher{finder: finder.New(
			finder.WithNormalizer(normalizer.New()),
		)}
	}

	m.relayout()
}

// Init implements the [tea.Model] interface.
// It returns nil because the viewport requires no initialization commands.
//
//nolint:gocritic // hugeParam: required by tea.Model interface.
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

// relayout responds to a change in how the view is laid out, such as a new
// printer, style, wrap setting, or width, without rebuilding the view. It
// gives the Model an empty row count cache and leaves any copy that shares
// the old cache untouched. The next read of the scroll bounds fills the new
// cache.
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
// specialized to the given content width and the viewport's word wrap
// setting, so wrapped lines fit the content area.
func (m *Model) renderPrinter(width int) *printer.Printer {
	if !m.wrapEnabled {
		width = 0
	}

	return m.printer.With(printer.WithWidth(width))
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

// SetSource replaces the revision history with a single revision.
//
// This is a convenience method equivalent to [Model.ClearRevisions] followed by
// [Model.AddRevision].
//
// The viewport renders a private copy of the source's lines. It does not
// display overlays the caller adds to s afterward, and its search highlights
// never modify s.
func (m *Model) SetSource(s *niceyaml.Source) {
	m.ClearRevisions()
	m.AddRevision(s)
}

// AddRevision adds a new revision to the history.
// After adding, the viewport moves to the newly added revision.
func (m *Model) AddRevision(s *niceyaml.Source) {
	m.revisions = append(m.revisions, s)
	m.revIndex = len(m.revisions) - 1

	m.rebuildViews()
}

// ClearRevisions removes all revisions from the history.
func (m *Model) ClearRevisions() {
	m.revisions = nil
	m.revIndex = 0
	m.rebuildViews()
}

// RevisionIndex returns the current revision index.
// Returns 0 if revisions are empty.
func (m *Model) RevisionIndex() int {
	return m.revIndex
}

// RevisionName returns the name of the current revision.
// Returns empty string if revisions are empty.
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

// GoToRevision navigates to the revision at index, clamped to the valid range.
// Index 0 always shows the first revision without diff markers.
// Index 1 to N-1 shows a diff based on the current [DiffMode].
func (m *Model) GoToRevision(index int) {
	if !m.hasRevision() {
		return
	}

	m.revIndex = clamp(index, 0, len(m.revisions)-1)
	m.showRevision()
}

// RevisionCount returns the number of revisions in the history.
func (m *Model) RevisionCount() int {
	return len(m.revisions)
}

// IsAtFirstRevision reports whether the viewport is at revision index 0.
func (m *Model) IsAtFirstRevision() bool {
	return m.revIndex == 0
}

// IsAtLatestRevision reports whether the viewport is at the latest revision.
func (m *Model) IsAtLatestRevision() bool {
	return m.revIndex >= len(m.revisions)-1
}

// IsShowingDiff reports whether the viewport is displaying a diff between
// revisions.
//
// This is true when not at the first revision and [DiffMode] is not
// [DiffModeNone].
func (m *Model) IsShowingDiff() bool {
	return m.hasRevision() && m.revIndex > 0 && m.diffMode != DiffModeNone
}

// DiffStats returns the number of added and removed lines in the current diff.
//
// Returns (0, 0) if no diff is being shown (at first revision, diff mode is none,
// or no revisions exist).
func (m *Model) DiffStats() (int, int) {
	if !m.IsShowingDiff() {
		return 0, 0
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

// Style returns the container style applied to the viewport frame.
func (m *Model) Style() lipgloss.Style {
	return m.style
}

// SetStyle sets the container style applied to the viewport frame. The frame
// size changes the content area, so the scroll offsets clamp to it.
//
//nolint:gocritic // hugeParam: Copying.
func (m *Model) SetStyle(s lipgloss.Style) {
	m.style = s
	m.relayout()
}

// NextRevision moves to the next revision in history.
// If already at the latest, does nothing.
func (m *Model) NextRevision() { m.seekRevision(1) }

// PrevRevision moves to the previous revision in history.
// If already at the first (index 0), does nothing.
func (m *Model) PrevRevision() { m.seekRevision(-1) }

// seekRevision moves the revision index by delta, with boundary checks.
func (m *Model) seekRevision(delta int) {
	if !m.hasRevision() {
		return
	}

	if delta > 0 && m.IsAtLatestRevision() {
		return
	}

	if delta < 0 && m.IsAtFirstRevision() {
		return
	}

	m.revIndex = clamp(m.revIndex+delta, 0, len(m.revisions)-1)
	m.showRevision()
}

// showRevision rebuilds the view for the selected revision and scrolls to
// the top, or to the first search match when the search term has one.
func (m *Model) showRevision() {
	m.rebuildViews()

	if m.searchIndex < 0 {
		m.GotoTop()
	}
}

// rebuildViews rebuilds the displayed views from the revision, diff mode, and
// view mode, then refreshes the search state and drops the cached row counts.
// Rendering itself waits for View, which renders only the visible window.
//
// Every change of content goes through rebuildViews. A match index carried
// over from the old content points at an arbitrary line, so the first match
// in the new content becomes the current match and the view scrolls to it.
func (m *Model) rebuildViews() {
	m.diffResult = nil // Invalidate cached diff result.
	m.left = nil
	m.right = nil
	m.searcherStale = true
	m.searchIndex = -1

	_, needsDiff := m.resolveRevisionSource()

	switch {
	case m.viewMode == ViewModeSideBySide && needsDiff:
		result := m.getDiffResult()
		m.left = result.Before()
		m.right = result.After()

	case m.viewMode == ViewModeHunks && needsDiff:
		// Hunks returns nil when the diff has no changes, which leaves the
		// view empty.
		m.left = m.getDiffResult().Hunks(m.hunkContext)

	default:
		m.left = m.getDisplayLines()
	}

	m.refreshSearch()
	m.relayout()
	m.scrollToCurrentMatch()
}

// refreshSearch recomputes search matches and overlays for the current views
// without rebuilding them.
func (m *Model) refreshSearch() {
	if m.left == nil {
		m.searchMatches = nil
		m.leftMatches = nil
		m.rightMatches = nil
		m.searchIndex = -1

		return
	}

	if m.viewMode == ViewModeSideBySide && m.right != nil {
		m.updateSideBySideSearchState()
		m.applySideBySideOverlays()

		return
	}

	m.updateSearchState(m.left)
	m.applySearchOverlays(m.left)
}

// applySearchOverlays sets overlay highlights for all search matches.
func (m *Model) applySearchOverlays(lines line.Lines) {
	lines.ClearOverlays()

	for i, match := range m.searchMatches {
		if i == m.searchIndex {
			lines.BlendOverlay(style.GenericHighlight, match.rng)
		} else {
			lines.BlendOverlay(style.GenericHighlightDim, match.rng)
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
// Matches are combined from both sources with deduplication: equal lines count
// as a single match, while deleted/inserted lines are separate matches.
func (m *Model) updateSideBySideSearchState() {
	if m.searchTerm == "" {
		m.searchMatches = nil
		m.leftMatches = nil
		m.rightMatches = nil

		return
	}

	// Search on both sources and cache results for overlay application.
	m.leftMatches = m.searcher.Load(m.left).Find(m.searchTerm)
	m.rightMatches = m.searcher.Load(m.right).Find(m.searchTerm)

	// Build combined match list. For equal lines, a match appears in both
	// sources at the same position, so we deduplicate by (row, startCol).
	// For deleted/inserted lines, the match only appears in one source.
	//
	// Track equal-line match positions from left source for deduplication.
	equalLinePositions := make(map[position.Position]bool)
	leftLines := m.left

	combined := make([]searchMatch, 0, len(m.leftMatches)+len(m.rightMatches))

	for _, match := range m.leftMatches {
		combined = append(combined, searchMatch{rng: match, inLeft: true})

		// Track equal-line matches for deduplication.
		if match.Start.Line < len(leftLines) {
			if leftLines[match.Start.Line].Flag() == line.FlagDefault {
				equalLinePositions[match.Start] = true
			}
		}
	}

	// Add matches from right source, skipping duplicates on equal lines.
	for _, match := range m.rightMatches {
		if equalLinePositions[match.Start] {
			continue
		}

		combined = append(combined, searchMatch{rng: match, inLeft: false})
	}

	// Sort by position for consistent navigation order.
	slices.SortFunc(combined, func(a, b searchMatch) int {
		if a.rng.Start.Line != b.rng.Start.Line {
			return cmp.Compare(a.rng.Start.Line, b.rng.Start.Line)
		}

		return cmp.Compare(a.rng.Start.Col, b.rng.Start.Col)
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

// applySideBySideOverlays applies search highlights to both panes.
func (m *Model) applySideBySideOverlays() {
	if m.searchTerm == "" {
		return
	}

	// Determine the selected match position and whether it's on an equal line.
	var (
		selectedPos                     position.Position
		selectedInLeft, selectedIsEqual bool
	)

	if m.searchIndex >= 0 && m.searchIndex < len(m.searchMatches) {
		selected := m.searchMatches[m.searchIndex]
		selectedPos = selected.rng.Start
		selectedInLeft = selected.inLeft

		// Check if selected match is on an equal line.
		leftLines := m.left
		if selectedPos.Line < len(leftLines) {
			selectedIsEqual = leftLines[selectedPos.Line].Flag() == line.FlagDefault
		}
	}

	// Apply overlays to both sources using cached matches.
	m.applySideBySidePaneOverlays(m.left, m.leftMatches, selectedPos, selectedInLeft || selectedIsEqual)
	m.applySideBySidePaneOverlays(m.right, m.rightMatches, selectedPos, !selectedInLeft || selectedIsEqual)
}

// applySideBySidePaneOverlays applies search highlights to a single pane.
// It uses cached matches and showSelected to determine the selected style.
func (m *Model) applySideBySidePaneOverlays(
	view line.Lines,
	matches position.Ranges,
	selectedPos position.Position,
	showSelected bool,
) {
	if view == nil {
		return
	}

	view.ClearOverlays()

	for _, match := range matches {
		isSelected := match.Start == selectedPos && showSelected
		if isSelected {
			view.BlendOverlay(style.GenericHighlight, match)
		} else {
			view.BlendOverlay(style.GenericHighlightDim, match)
		}
	}
}

// updateSearchState updates the searcher and search matches for the given
// lines.
//
// It reloads the searcher only when the lines changed since the last load, so
// typing a search term does not rebuild the index on every keystroke.
func (m *Model) updateSearchState(lines line.Lines) {
	if m.searchTerm == "" {
		m.searchMatches = nil
		m.leftMatches = nil
		m.rightMatches = nil

		return
	}

	if m.searcherStale || m.index == nil {
		m.index = m.searcher.Load(lines)

		m.searcherStale = false
	}

	// Convert ranges to searchMatch structs (inLeft is not used in unified mode).
	ranges := m.index.Find(m.searchTerm)
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

// getDiffBase returns the revision the current one is compared against
// based on the current [DiffMode].
// Returns nil if diff mode is [DiffModeNone] or there are no revisions.
func (m *Model) getDiffBase() *niceyaml.Source {
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
func (m *Model) currentRevision() *niceyaml.Source {
	return m.revision(m.revIndex)
}

// revision returns the revision at index, or nil when index is outside the
// history.
func (m *Model) revision(index int) *niceyaml.Source {
	if index < 0 || index >= len(m.revisions) {
		return nil
	}

	return m.revisions[index]
}

// getDisplayLines returns the view to display based on current revision and
// [DiffMode].
//
// The model always owns the result, either a clone of the revision's lines
// or a fresh unified diff. Returns nil when there is no revision.
func (m *Model) getDisplayLines() line.Lines {
	src, needsDiff := m.resolveRevisionSource()
	if needsDiff {
		return m.getDiffResult().Unified()
	}

	if src == nil {
		return nil
	}

	return src.Lines()
}

// getDiffResult returns the cached [diff.Result], computing it if nil.
//
// Without a base for the current [DiffMode], the current revision stands in
// for it, which yields an empty diff rather than a nil [niceyaml.Source].
func (m *Model) getDiffResult() *diff.Result {
	if m.diffResult == nil {
		current := m.currentRevision()

		base := m.getDiffBase()
		if base == nil {
			base = current
		}

		m.diffResult = diff.Diff(base.Lines(), current.Lines())
	}

	return m.diffResult
}

// resolveRevisionSource determines which source to display for the current
// revision state.
//
// Returns (source, false) for non-diff cases (origin, no diff mode, or no
// revision), or (nil, true) when a diff should be computed.
func (m *Model) resolveRevisionSource() (*niceyaml.Source, bool) {
	if !m.hasRevision() {
		return nil, false
	}

	if !m.IsShowingDiff() {
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

// rowCache holds the rendered row counts of a view.
type rowCache struct {
	// Row counts of each line the view renders, in span order, for the left
	// and right panes. The right counts are nil outside side-by-side diffs.
	left, right []int
	// Prefix sums of the taller pane after the top frame, so sums[k] is the
	// first row of the k-th rendered line and the last entry is the first row
	// of the bottom frame. Nil until filled.
	sums []int
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

	m.yOffset = clamp(m.yOffset, 0, max(0, m.rows.total()-m.maxHeight()))
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

		c.left = p.Rows(m.left)

		if m.viewMode == ViewModeSideBySide && m.right != nil {
			c.right = p.Rows(m.right)
		}

		// Rows counts the rows before the container style applies. Print adds
		// the container's frame above and below the lines it renders.
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
func (m *Model) rowWindow() (int, int) {
	m.ensureRows()

	n := len(m.rows.sums) - 1
	top := m.yOffset
	bottom := top + m.maxHeight()

	// The last line starting at or above the top row, and the first line
	// starting at or below the bottom row.
	first := clamp(sort.SearchInts(m.rows.sums, top+1)-1, 0, n)
	last := clamp(sort.SearchInts(m.rows.sums, bottom), first, n)

	return first, last
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

// PastBottom reports whether the offset lies past the last row.
func (m *Model) PastBottom() bool {
	return m.hasContent() && m.YOffset() > m.maxYOffset()
}

// ScrollPercent returns the vertical scroll position as a float between 0 and 1.
func (m *Model) ScrollPercent() float64 {
	if m.left == nil {
		return 1.0
	}

	return scrollPercent(m.YOffset(), m.maxHeight(), m.TotalRowCount())
}

// HorizontalScrollPercent returns the horizontal scroll position as a float
// between 0 and 1.
func (m *Model) HorizontalScrollPercent() float64 {
	if m.left == nil {
		return 1.0
	}

	return scrollPercent(m.XOffset(), m.maxWidth(), m.left.Width())
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

	return max(0, m.rows.total()-m.maxHeight())
}

// lineCount returns the number of lines the view renders.
func (m *Model) lineCount() int {
	return m.left.Len()
}

// maxXOffset returns the maximum X offset.
func (m *Model) maxXOffset() int {
	if m.left == nil {
		return 0
	}

	return max(0, m.left.Width()-m.maxWidth())
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
// height and without FillHeight padding.
func (m *Model) visibleRows() []string {
	if !m.canRender() || !m.hasContent() {
		return nil
	}

	first, last := m.rowWindow()
	if first >= last {
		return nil
	}

	p := m.renderPrinter(m.maxWidth())
	rows := splitLines(p.Print(m.left, position.NewSpan(first, last)))
	rows = m.trimWindow(m.trimFrame(rows, first, last), first)

	// Without wrapping, lines may exceed the viewport width. Cut them to the
	// horizontal window so lipgloss does not wrap them.
	if !m.wrapEnabled {
		maxWidth := m.maxWidth()

		for i := range rows {
			rows[i] = ansi.Cut(rows[i], m.xOffset, m.xOffset+maxWidth)
		}
	}

	return rows
}

// padRows pads rows with empty rows up to the content height when FillHeight
// is set.
func (m *Model) padRows(rows []string) []string {
	if !m.canRender() {
		return nil
	}

	if maxHeight := m.maxHeight(); m.FillHeight && len(rows) < maxHeight {
		padded := make([]string, maxHeight)
		copy(padded, rows)

		return padded
	}

	return rows
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

// HalfPageDown moves the view down by half a page.
func (m *Model) HalfPageDown() {
	m.ScrollDown(m.maxHeight() / 2)
}

// HalfPageUp moves the view up by half a page.
func (m *Model) HalfPageUp() {
	m.ScrollUp(m.maxHeight() / 2)
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
// screen.
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

// VisibleRowCount returns the number of rendered rows on screen, without any
// FillHeight padding.
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
// scrolls to it.
func (m *Model) SetSearchTerm(term string) {
	if term == "" {
		m.ClearSearch()

		return
	}

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
	if m.viewMode == ViewModeSideBySide && m.right != nil {
		m.applySideBySideOverlays()
	} else if m.left != nil {
		m.applySearchOverlays(m.left)
	}

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

// scrollToCurrentMatch scrolls to center the current search match in the
// viewport.
func (m *Model) scrollToCurrentMatch() {
	if m.searchIndex < 0 || m.searchIndex >= len(m.searchMatches) {
		return
	}

	k := m.searchMatches[m.searchIndex].rng.Start.Line

	m.ensureRows()

	// Center the first row of the matched line in the viewport.
	// Use (maxHeight-1)/2 to ensure the match appears at the visual center.
	// For height 22: (22-1)/2 = 10, placing the match at position 10 (middle).
	// For height 21: (21-1)/2 = 10, placing the match at position 10 (middle).
	m.SetYOffset(m.rows.sums[k] - (m.maxHeight()-1)/2)
}

// Update processes Bubble Tea messages and returns the updated model.
//
//nolint:gocritic // hugeParam: required for tea.Model interface compatibility.
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

		case key.Matches(msg, m.KeyMap.PrevRevision):
			m.PrevRevision()

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

// getViewDimensions returns (width, height, ok).
// If ok is false, the viewport has zero dimensions and should not render.
func (m *Model) getViewDimensions() (int, int, bool) {
	if w, h := m.outerSize(); w == 0 || h == 0 {
		return 0, 0, false
	}

	return m.maxWidth(), m.maxHeight(), true
}

// renderContent applies styling and renders lines into final output.
func (m *Model) renderContent(lines []string, contentW, contentH int) string {
	textStyle := m.printer.Style(style.Text)

	contents := textStyle.
		Width(contentW).
		Height(contentH).
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
//nolint:gocritic // hugeParam: required for tea.Model interface compatibility.
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

	return m.renderContent(m.padRows(m.visibleRows()), w, h)
}

// sideBySideSeparator is the column divider between panes.
const sideBySideSeparator = " │ "

// renderSideBySide renders the side-by-side view with two panes.
func (m *Model) renderSideBySide(contentW, contentH int) string {
	// Get views for both panes. The model owns both, with overlays applied.
	if !m.hasContent() {
		return m.renderContent(m.padRows(nil), contentW, contentH)
	}

	right := m.right
	if right == nil {
		// Not showing a diff: show same content on both sides.
		right = m.left
	}

	// Need room for separator plus at least 1 character per pane.
	paneWidth := m.paneWidth()
	if paneWidth < 1 {
		return m.renderContent(nil, contentW, contentH)
	}

	first, last := m.rowWindow()
	if first >= last {
		return m.renderContent(m.padRows(nil), contentW, contentH)
	}

	// Render the lines of the window in both panes.
	window := position.NewSpan(first, last)
	p := m.renderPrinter(paneWidth)

	leftRows := m.trimFrame(splitLines(p.Print(m.left, window)), first, last)
	rightRows := m.trimFrame(splitLines(p.Print(right, window)), first, last)

	// Get text style for padding empty areas.
	textStyle := m.printer.Style(style.Text)

	// Build separator with any extra padding from odd width.
	separatorWidth := ansi.StringWidth(sideBySideSeparator)
	extraPadding := (contentW - separatorWidth) % 2
	separator := textStyle.Render(sideBySideSeparator + strings.Repeat(" ", extraPadding))

	combined := make([]string, 0, m.rows.sums[last]-m.rows.sums[first])

	var li, ri int

	// Join the next count rows of the panes, taking at most leftCount rows
	// from the left pane and rightCount from the right.
	appendRows := func(count, leftCount, rightCount int) {
		for i := range count {
			var left, right string

			if i < leftCount && li < len(leftRows) {
				left = leftRows[li]
				li++
			}

			if i < rightCount && ri < len(rightRows) {
				right = rightRows[ri]
				ri++
			}

			// Apply horizontal scrolling.
			if !m.wrapEnabled {
				left = ansi.Cut(left, m.xOffset, m.xOffset+paneWidth)
				right = ansi.Cut(right, m.xOffset, m.xOffset+paneWidth)
			}

			// Pad left pane to consistent width for alignment.
			leftPadded := ansi.Truncate(left, paneWidth, "")
			if padding := paneWidth - ansi.StringWidth(leftPadded); padding > 0 {
				leftPadded += textStyle.Render(strings.Repeat(" ", padding))
			}

			combined = append(combined, leftPadded+separator+right)
		}
	}

	if first == 0 {
		appendRows(m.rows.top, m.rows.top, m.rows.top)
	}

	// Zip the panes line by line. A line that wraps taller in one pane gets
	// blank rows in the other, so the panes stay aligned.
	for k := first; k < last; k++ {
		leftCount := m.rows.left[k]

		rightCount := leftCount
		if m.rows.right != nil {
			rightCount = m.rows.right[k]
		}

		appendRows(m.lineRows(k), leftCount, rightCount)
	}

	if last == len(m.rows.left) {
		appendRows(m.rows.bottom, m.rows.bottom, m.rows.bottom)
	}

	combined = m.trimWindow(combined, first)

	return m.renderContent(m.padRows(combined), contentW, contentH)
}

func clamp[T cmp.Ordered](v, low, high T) T {
	return min(high, max(low, v))
}

// splitLines splits content by newlines, correctly handling trailing newlines.
// Unlike [strings.Split], this does not produce an empty trailing element when
// the content ends with a newline.
func splitLines(content string) []string {
	if content == "" {
		return nil
	}

	return strings.Split(strings.TrimSuffix(content, "\n"), "\n")
}
