package yamlviewport

import (
	"cmp"
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	tea "charm.land/bubbletea/v2"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/style"
)

const defaultHorizontalStep = 6

// Printer prints YAML.
//
// See [niceyaml.Printer] for an implementation.
type Printer interface {
	Print(lines niceyaml.LineIterator, spans ...position.Span) string
	SetWidth(width int)
	SetWordWrap(enabled bool)
	SetAnnotationsEnabled(enabled bool)
	Style(s style.Style) *lipgloss.Style
}

// Finder finds [position.Range]s for a search string.
//
// See [niceyaml.Finder] for an implementation.
type Finder interface {
	Load(lines niceyaml.LineIterator)
	Find(search string) []position.Range
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
//   - [WithFinder]
type Option func(*Model)

// WithPrinter is an [Option] that sets the [Printer] used for rendering.
// If not set, a default [niceyaml.Printer] is created.
func WithPrinter(p Printer) Option {
	return func(m *Model) {
		m.printer = p
	}
}

// WithStyle is an [Option] that sets the container style for the viewport.
//
//nolint:gocritic // hugeParam: Copying.
func WithStyle(s lipgloss.Style) Option {
	return func(m *Model) {
		m.Style = s
	}
}

// WithFinder is an [Option] that sets the [Finder].
func WithFinder(f Finder) Option {
	return func(m *Model) {
		m.finder = f
	}
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
type Model struct {
	// Style is the container style applied to the viewport frame.
	Style   lipgloss.Style
	printer Printer
	finder  Finder
	// Cached diff between base and current revision.
	revision   *niceyaml.Revision
	diffResult *niceyaml.DiffResult
	// Left holds the source for the left pane or main content.
	// In ViewModeFull/ViewModeHunks: Unified diff or plain content.
	// In ViewModeSideBySide with diff: Before source.
	// In ViewModeSideBySide without diff: plain content (same on both sides).
	left *niceyaml.Source
	// Right holds the right pane source for side-by-side diff rendering.
	// Only populated when viewMode == ViewModeSideBySide and showing a diff.
	right *niceyaml.Source
	// Current search query.
	searchTerm string
	// KeyMap contains the keybindings for viewport navigation.
	KeyMap         KeyMap
	searchMatches  []searchMatch
	leftMatches    []position.Range
	rightMatches   []position.Range
	horizontalStep int
	diffMode       DiffMode
	// MouseWheelDelta is the number of lines to scroll per mouse wheel tick.
	// Default: 3.
	MouseWheelDelta int
	width           int
	searchIndex     int
	yOffset         int
	height          int
	xOffset         int
	viewMode        ViewMode
	hunkContext     int
	// FillHeight pads output with empty lines to fill the viewport height when true.
	FillHeight bool
	// MouseWheelEnabled enables mouse wheel scrolling.
	// Default: true.
	MouseWheelEnabled bool
	// WrapEnabled enables line wrapping based on viewport width.
	WrapEnabled bool
	initialized bool
}

func (m *Model) setInitialValues() {
	m.KeyMap = DefaultKeyMap()
	m.MouseWheelEnabled = true
	m.MouseWheelDelta = 3
	m.horizontalStep = defaultHorizontalStep
	m.hunkContext = 3 // Default context lines around diff hunks.
	m.WrapEnabled = true
	m.searchIndex = -1

	if m.printer == nil {
		m.printer = niceyaml.NewPrinter()
	}

	if m.finder == nil {
		m.finder = niceyaml.NewFinder(
			niceyaml.WithNormalizer(niceyaml.NewStandardNormalizer()),
		)
	}

	m.initialized = true
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

// SetHeight sets the height of the viewport.
func (m *Model) SetHeight(h int) {
	m.height = h
}

// Width returns the width of the viewport.
func (m *Model) Width() int {
	return m.width
}

// SetWidth sets the width of the viewport.
func (m *Model) SetWidth(w int) {
	if m.width != w {
		m.width = w
		m.printer.SetWidth(w)
		if m.WrapEnabled {
			m.rerender()
		}
	}
}

// SetPrinter sets the [Printer] used for rendering and triggers a re-render.
func (m *Model) SetPrinter(p Printer) {
	m.printer = p
	m.printer.SetWidth(m.width)
	m.rerender()
}

// SetTokens replaces the revision history with a single revision.
//
// This is a convenience method equivalent to [Model.ClearRevisions] followed by
// [Model.AddRevision].
func (m *Model) SetTokens(s *niceyaml.Source) {
	m.ClearRevisions()
	m.AddRevision(s)
}

// AddRevision adds a new revision to the history.
// After adding, the revision pointer moves to the newly added revision.
func (m *Model) AddRevision(s *niceyaml.Source) {
	if m.revision == nil {
		m.revision = niceyaml.NewRevision(s)
	} else {
		m.revision = m.revision.Tip().Append(s)
	}

	m.rerender()

	if m.YOffset() > m.maxYOffset() {
		m.GotoBottom()
	}
}

// ClearRevisions removes all revisions from the history.
func (m *Model) ClearRevisions() {
	m.revision = nil
	m.rerender()
}

// RevisionIndex returns the current revision index.
// Returns 0 if revisions are empty.
func (m *Model) RevisionIndex() int {
	if !m.hasRevision() {
		return 0
	}

	return m.revision.Index()
}

// RevisionName returns the name of the current revision.
// Returns empty string if revisions are empty.
func (m *Model) RevisionName() string {
	if !m.hasRevision() {
		return ""
	}

	return m.revision.Name()
}

// RevisionNames returns all revision names in order.
func (m *Model) RevisionNames() []string {
	if !m.hasRevision() {
		return nil
	}

	return m.revision.Names()
}

// GoToRevision navigates to the revision at index, clamped to the valid range.
// Index 0 always shows the first revision without diff markers.
// Index 1 to N-1 shows a diff based on the current [DiffMode].
func (m *Model) GoToRevision(index int) {
	if !m.hasRevision() {
		return
	}

	maxIndex := m.revision.Len() - 1
	index = clamp(index, 0, maxIndex)
	m.revision = m.revision.At(index)
	m.rerender()
	m.GotoTop()
}

// RevisionCount returns the number of revisions in the history.
func (m *Model) RevisionCount() int {
	if !m.hasRevision() {
		return 0
	}

	return m.revision.Len()
}

// IsAtFirstRevision reports whether the viewport is at revision index 0.
func (m *Model) IsAtFirstRevision() bool {
	if !m.hasRevision() {
		return true
	}

	return m.revision.AtOrigin()
}

// IsAtLatestRevision reports whether the viewport is at the latest revision.
func (m *Model) IsAtLatestRevision() bool {
	if !m.hasRevision() {
		return true
	}

	return m.revision.AtTip()
}

// IsShowingDiff reports whether the viewport is displaying a diff between
// revisions.
//
// This is true when not at the first revision and [DiffMode] is not
// [DiffModeNone].
func (m *Model) IsShowingDiff() bool {
	return m.hasRevision() && !m.revision.AtOrigin() && m.diffMode != DiffModeNone
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

// SetDiffMode sets the diff display mode and rerenders.
func (m *Model) SetDiffMode(mode DiffMode) {
	m.diffMode = mode
	m.rerender()
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

	m.rerender()
}

// ViewMode returns the current view mode.
func (m *Model) ViewMode() ViewMode {
	return m.viewMode
}

// SetViewMode sets the view mode and rerenders.
func (m *Model) SetViewMode(mode ViewMode) {
	m.viewMode = mode
	m.rerender()
}

// ToggleViewMode cycles between view modes.
func (m *Model) ToggleViewMode() {
	switch m.viewMode {
	case ViewModeFull:
		m.viewMode = ViewModeSideBySide
	default:
		m.viewMode = ViewModeFull
	}

	m.rerender()
}

// HunkContext returns the number of context lines shown around diff hunks.
func (m *Model) HunkContext() int {
	return m.hunkContext
}

// SetHunkContext sets the number of context lines shown around diff hunks
// in [ViewModeHunks]. Default is 3.
func (m *Model) SetHunkContext(n int) {
	m.hunkContext = max(0, n)
}

// ToggleWordWrap toggles word wrapping on or off.
func (m *Model) ToggleWordWrap() {
	m.WrapEnabled = !m.WrapEnabled
	m.printer.SetWordWrap(m.WrapEnabled)

	if m.WrapEnabled {
		m.xOffset = 0
	}

	m.rerender()
}

// NextRevision moves to the next revision in history.
// If already at the latest, does nothing.
func (m *Model) NextRevision() { m.seekRevision(1) }

// PrevRevision moves to the previous revision in history.
// If already at the first (index 0), does nothing.
func (m *Model) PrevRevision() { m.seekRevision(-1) }

// seekRevision moves the revision pointer by delta, with boundary checks.
func (m *Model) seekRevision(delta int) {
	if !m.hasRevision() {
		return
	}
	if delta > 0 && m.revision.AtTip() {
		return
	}
	if delta < 0 && m.revision.AtOrigin() {
		return
	}

	m.revision = m.revision.Seek(delta)
	m.rerender()
	m.GotoTop()
}

// rerender updates the source lines and search state.
// Actual rendering is deferred to renderVisible for on-demand rendering.
func (m *Model) rerender() {
	m.diffResult = nil // Invalidate cached diff result.
	m.right = nil

	// Handle side-by-side mode with diff specially.
	if m.viewMode == ViewModeSideBySide {
		if _, needsDiff := m.resolveRevisionSource(); needsDiff {
			diff := m.getDiffResult()
			m.left = diff.Before()
			m.right = diff.After()
			m.updateSideBySideSearchState()
			m.applySideBySideOverlays()

			return
		}
	}

	// For other modes, use standard display lines.
	left := m.getDisplayLines()
	if left == nil {
		m.left = nil

		return
	}

	m.left = left
	m.updateSearchState(left)
	m.applySearchOverlays(left)
}

// applySearchOverlays sets overlay highlights for all search matches.
func (m *Model) applySearchOverlays(lines *niceyaml.Source) {
	lines.ClearOverlays()

	for i, match := range m.searchMatches {
		if i == m.searchIndex {
			lines.AddOverlay(style.SearchSelected, match.rng)
		} else {
			lines.AddOverlay(style.Search, match.rng)
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
	m.finder.Load(m.left)

	m.leftMatches = m.finder.Find(m.searchTerm)

	m.finder.Load(m.right)

	m.rightMatches = m.finder.Find(m.searchTerm)

	// Build combined match list. For equal lines, a match appears in both
	// sources at the same position, so we deduplicate by (row, startCol).
	// For deleted/inserted lines, the match only appears in one source.
	//
	// Track equal-line match positions from left source for deduplication.
	equalLinePositions := make(map[position.Position]bool)
	leftLines := m.left.Lines()

	combined := make([]searchMatch, 0, len(m.leftMatches)+len(m.rightMatches))

	for _, match := range m.leftMatches {
		combined = append(combined, searchMatch{rng: match, inLeft: true})

		// Track equal-line matches for deduplication.
		if match.Start.Line < len(leftLines) {
			if leftLines[match.Start.Line].Flag == line.FlagDefault {
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
		leftLines := m.left.Lines()
		if selectedPos.Line < len(leftLines) {
			selectedIsEqual = leftLines[selectedPos.Line].Flag == line.FlagDefault
		}
	}

	// Apply overlays to both sources using cached matches.
	m.applySideBySidePaneOverlays(m.left, m.leftMatches, selectedPos, selectedInLeft || selectedIsEqual)
	m.applySideBySidePaneOverlays(m.right, m.rightMatches, selectedPos, !selectedInLeft || selectedIsEqual)
}

// applySideBySidePaneOverlays applies search highlights to a single pane.
// It uses cached matches and showSelected to determine the selected style.
func (m *Model) applySideBySidePaneOverlays(
	src *niceyaml.Source,
	matches []position.Range,
	selectedPos position.Position,
	showSelected bool,
) {
	if src == nil {
		return
	}

	src.ClearOverlays()

	for _, match := range matches {
		isSelected := match.Start == selectedPos && showSelected
		if isSelected {
			src.AddOverlay(style.SearchSelected, match)
		} else {
			src.AddOverlay(style.Search, match)
		}
	}
}

// updateSearchState updates the finder and search matches for the given lines.
func (m *Model) updateSearchState(lines *niceyaml.Source) {
	if m.searchTerm == "" {
		m.searchMatches = nil
		m.leftMatches = nil
		m.rightMatches = nil

		return
	}

	m.finder.Load(lines)

	// Convert ranges to searchMatch structs (inLeft is not used in unified mode).
	ranges := m.finder.Find(m.searchTerm)
	m.searchMatches = make([]searchMatch, len(ranges))

	for i, rng := range ranges {
		m.searchMatches[i] = searchMatch{rng: rng}
	}

	// Adjust search index if matches changed.
	switch {
	case len(m.searchMatches) == 0:
		m.searchIndex = -1
	case m.searchIndex >= len(m.searchMatches), m.searchIndex < 0:
		m.searchIndex = 0
	}
}

// renderVisible renders only the visible slice of lines on demand.
func (m *Model) renderVisible() []string {
	if m.left == nil {
		return nil
	}

	start := m.YOffset()
	end := min(start+m.maxHeight(), m.left.Len())

	if start >= end {
		return nil
	}

	content := m.printer.Print(m.left, position.NewSpan(start, end))

	return strings.Split(content, "\n")
}

// getDiffBaseRevision returns the base revision for diff comparison based on
// the current [DiffMode].
// Returns nil if diff mode is [DiffModeNone] or revision is nil.
func (m *Model) getDiffBaseRevision() *niceyaml.Revision {
	if !m.hasRevision() {
		return nil
	}

	switch m.diffMode {
	case DiffModeOrigin:
		return m.revision.Origin()
	case DiffModeAdjacent:
		return m.revision.Seek(-1)
	default:
		return nil
	}
}

// getDisplayLines returns the lines to display based on current revision and
// [DiffMode].
func (m *Model) getDisplayLines() *niceyaml.Source {
	if src, needsDiff := m.resolveRevisionSource(); !needsDiff {
		return src
	}

	return m.getDiffResult().Unified()
}

// getDiffResult returns the cached [niceyaml.DiffResult], computing it if nil.
func (m *Model) getDiffResult() *niceyaml.DiffResult {
	if m.diffResult == nil {
		m.diffResult = niceyaml.Diff(m.getDiffBaseRevision(), m.revision)
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

	if m.revision.AtOrigin() {
		return m.revision.Origin().Source(), false
	}

	if !m.IsShowingDiff() {
		return m.revision.Source(), false
	}

	return nil, true
}

// AtTop reports whether the viewport is scrolled to the top.
func (m *Model) AtTop() bool {
	return !m.hasContent() || m.YOffset() <= 0
}

// AtBottom reports whether the viewport is scrolled to or past the bottom.
func (m *Model) AtBottom() bool {
	return !m.hasContent() || m.YOffset() >= m.maxYOffset()
}

// PastBottom reports whether the viewport is scrolled past the last line.
func (m *Model) PastBottom() bool {
	return m.hasContent() && m.YOffset() > m.maxYOffset()
}

// ScrollPercent returns the vertical scroll position as a float between 0 and 1.
func (m *Model) ScrollPercent() float64 {
	if m.left == nil {
		return 1.0
	}

	return scrollPercent(m.YOffset(), m.maxHeight(), m.left.Len())
}

// HorizontalScrollPercent returns the horizontal scroll position as a float
// between 0 and 1.
func (m *Model) HorizontalScrollPercent() float64 {
	if m.left == nil {
		return 1.0
	}

	return scrollPercent(m.xOffset, m.maxWidth(), m.left.Width())
}

// scrollPercent calculates scroll position as a value between 0 and 1.
func scrollPercent(offset, visible, total int) float64 {
	if visible >= total {
		return 1.0
	}

	v := float64(offset) / float64(total-visible)

	return clamp(v, 0, 1)
}

// maxYOffset returns the maximum Y offset.
func (m *Model) maxYOffset() int {
	lineCount := m.lineCount()
	if lineCount == 0 {
		return 0
	}

	return max(0, lineCount-m.maxHeight())
}

// lineCount returns the line count for the current view mode.
func (m *Model) lineCount() int {
	if m.left == nil {
		return 0
	}

	return m.left.Len()
}

// maxXOffset returns the maximum X offset.
func (m *Model) maxXOffset() int {
	if m.left == nil {
		return 0
	}

	return max(0, m.left.Width()-m.maxWidth())
}

// maxWidth returns the content width accounting for frame size.
func (m *Model) maxWidth() int {
	return max(0, m.Width()-m.Style.GetHorizontalFrameSize())
}

// maxHeight returns the content height accounting for frame size.
func (m *Model) maxHeight() int {
	return max(0, m.Height()-m.Style.GetVerticalFrameSize())
}

// hasContent reports whether there is content to display.
func (m *Model) hasContent() bool {
	return m.left != nil && !m.left.IsEmpty()
}

// hasRevision reports whether a revision exists.
func (m *Model) hasRevision() bool {
	return m.revision != nil
}

// visibleLines returns the lines currently visible in the viewport.
// If lines is nil, renders the visible portion of m.left on demand.
func (m *Model) visibleLines(lines []string) []string {
	maxHeight := m.maxHeight()
	maxWidth := m.maxWidth()

	if maxHeight == 0 || maxWidth == 0 {
		return nil
	}

	if lines == nil {
		lines = m.renderVisible()
	}

	if len(lines) == 0 {
		if m.FillHeight {
			return make([]string, maxHeight)
		}

		return nil
	}

	// Truncate to maxHeight if we have more lines than the viewport can show.
	// This happens when wrapping causes source lines to expand into more
	// rendered lines.
	if len(lines) > maxHeight {
		lines = lines[:maxHeight]
	}

	// Determine result length, padding for FillHeight if needed.
	resultLen := len(lines)
	if m.FillHeight && resultLen < maxHeight {
		resultLen = maxHeight
	}

	result := make([]string, resultLen)
	copy(result, lines)

	// Apply horizontal scrolling / line truncation.
	// When wrapping is disabled, lines may exceed viewport width.
	// Truncate to viewport width to prevent lipgloss from wrapping.
	if !m.WrapEnabled {
		for i := range result {
			result[i] = ansi.Cut(result[i], m.xOffset, m.xOffset+maxWidth)
		}
	}

	return result
}

// SetYOffset sets the Y offset.
func (m *Model) SetYOffset(n int) {
	m.yOffset = clamp(n, 0, m.maxYOffset())
}

// YOffset returns the current Y offset.
func (m *Model) YOffset() int {
	return m.yOffset
}

// SetXOffset sets the X offset.
func (m *Model) SetXOffset(n int) {
	m.xOffset = clamp(n, 0, m.maxXOffset())
}

// XOffset returns the current X offset.
func (m *Model) XOffset() int {
	return m.xOffset
}

// ScrollDown moves the view down by n lines.
func (m *Model) ScrollDown(n int) {
	if m.AtBottom() || n == 0 {
		return
	}

	m.SetYOffset(m.YOffset() + n)
}

// ScrollUp moves the view up by n lines.
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
	m.SetXOffset(m.xOffset - n)
}

// ScrollRight moves the viewport right by n columns.
func (m *Model) ScrollRight(n int) {
	m.SetXOffset(m.xOffset + n)
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

// TotalLineCount returns the total number of lines.
func (m *Model) TotalLineCount() int {
	return m.lineCount()
}

// VisibleLineCount returns the number of visible lines.
func (m *Model) VisibleLineCount() int {
	return len(m.visibleLines(nil))
}

// SetSearchTerm sets the search term and updates highlights.
// If the term is empty, clears all search highlights.
func (m *Model) SetSearchTerm(term string) {
	if term == "" {
		m.ClearSearch()
		return
	}

	m.searchTerm = term
	m.rerender()
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
	m.rerender()
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

// scrollToCurrentMatch scrolls to center the current search match in the viewport.
func (m *Model) scrollToCurrentMatch() {
	if m.searchIndex < 0 || m.searchIndex >= len(m.searchMatches) {
		return
	}

	match := m.searchMatches[m.searchIndex]
	startLine := match.rng.Start.Line

	// Center the match in the viewport.
	m.SetYOffset(startLine - m.maxHeight()/2)
}

// Update processes Bubble Tea messages and returns the updated model.
//
//nolint:gocritic // hugeParam: required for tea.Model interface compatibility.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	if !m.initialized {
		m.setInitialValues()
	}

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
	w, h := m.Width(), m.Height()
	if sw := m.Style.GetWidth(); sw != 0 {
		w = min(w, sw)
	}
	if sh := m.Style.GetHeight(); sh != 0 {
		h = min(h, sh)
	}

	if w == 0 || h == 0 {
		return 0, 0, false
	}

	contentW := w - m.Style.GetHorizontalFrameSize()
	contentH := h - m.Style.GetVerticalFrameSize()

	return contentW, contentH, true
}

// renderContent applies styling and renders lines into final output.
func (m *Model) renderContent(lines []string, contentW, contentH int) string {
	textStyle := lipgloss.NewStyle()
	if st := m.printer.Style(style.Text); st != nil {
		textStyle = *st
	}

	contents := textStyle.
		Width(contentW).
		Height(contentH).
		Render(strings.Join(lines, "\n"))

	return m.Style.
		UnsetWidth().UnsetHeight().
		Render(contents)
}

// View renders the viewport.
//
// The rendering behavior depends on the current [ViewMode]:
//   - [ViewModeFull]: Renders all lines (default behavior).
//   - [ViewModeHunks]: Renders only changed lines with 3 lines of context.
//   - [ViewModeSideBySide]: Renders before and after content in separate panes.
//
//nolint:gocritic // hugeParam: required for tea.Model interface compatibility.
func (m Model) View() string {
	w, h, ok := m.getViewDimensions()
	if !ok {
		return ""
	}

	var lines []string

	switch m.viewMode {
	case ViewModeHunks:
		hunksContent := m.getHunksDiffContent()
		lines = m.visibleLines(strings.Split(hunksContent, "\n"))

	case ViewModeSideBySide:
		return m.renderSideBySide(w, h)

	default:
		lines = m.visibleLines(nil)
	}

	return m.renderContent(lines, w, h)
}

// getHunksDiffContent returns diff content with context lines for hunks mode.
func (m *Model) getHunksDiffContent() string {
	if src, needsDiff := m.resolveRevisionSource(); !needsDiff {
		if src == nil {
			return ""
		}

		return m.printer.Print(src)
	}

	source, ranges := m.getDiffResult().Hunks(m.hunkContext)

	return m.printer.Print(source, ranges...)
}

// sideBySideSeparator is the column divider between panes.
const sideBySideSeparator = " │ "

// renderSideBySide renders the side-by-side view with two panes.
//
//nolint:gocritic // hugeParam: required for value receiver compatibility with View().
func (m Model) renderSideBySide(contentW, contentH int) string {
	// Get iterators for both panes.
	var leftIter, rightIter niceyaml.LineIterator
	if src, needsDiff := m.resolveRevisionSource(); !needsDiff {
		// Not showing a diff: show same content on both sides.
		if src == nil {
			return m.renderContent(nil, contentW, contentH)
		}

		leftIter = src
		rightIter = src
	} else {
		// Use cached sources which have overlays applied.
		leftIter = m.left
		rightIter = m.right
	}

	// Calculate pane width (both panes use the same width).
	separatorWidth := ansi.StringWidth(sideBySideSeparator)
	availableWidth := contentW - separatorWidth
	paneWidth := availableWidth / 2

	// Need room for separator plus at least 1 character per pane.
	if paneWidth < 1 {
		return m.renderContent(nil, contentW, contentH)
	}

	extraPadding := availableWidth % 2 // Add to separator if odd.

	// Determine visible span.
	start := m.YOffset()
	end := min(start+m.maxHeight(), leftIter.Len())

	if start >= end {
		return m.renderContent(nil, contentW, contentH)
	}

	span := position.NewSpan(start, end)

	// Render both panes.
	m.printer.SetWordWrap(m.WrapEnabled)
	m.printer.SetWidth(paneWidth)

	leftContent := m.printer.Print(leftIter, span)
	rightContent := m.printer.Print(rightIter, span)

	// Split into lines.
	leftLines := strings.Split(leftContent, "\n")
	rightLines := strings.Split(rightContent, "\n")

	// Get text style for padding empty areas.
	textStyle := lipgloss.NewStyle()
	if st := m.printer.Style(style.Text); st != nil {
		textStyle = *st
	}

	// Combine lines with separator, applying horizontal offset.
	maxLines := max(len(leftLines), len(rightLines))
	if m.FillHeight && maxLines < contentH {
		maxLines = contentH
	}

	combined := make([]string, maxLines)
	for i := range combined {
		var left, right string
		if i < len(leftLines) {
			left = leftLines[i]
		}
		if i < len(rightLines) {
			right = rightLines[i]
		}

		// Apply horizontal scrolling.
		if !m.WrapEnabled {
			left = ansi.Cut(left, m.xOffset, m.xOffset+paneWidth)
			right = ansi.Cut(right, m.xOffset, m.xOffset+paneWidth)
		}

		// Pad left pane to consistent width for alignment.
		leftPadded := ansi.Truncate(left, paneWidth, "")
		if padding := paneWidth - ansi.StringWidth(leftPadded); padding > 0 {
			leftPadded += textStyle.Render(strings.Repeat(" ", padding))
		}

		// Build separator with any extra padding from odd width.
		separator := sideBySideSeparator + strings.Repeat(" ", extraPadding)

		combined[i] = leftPadded + textStyle.Render(separator) + right
	}

	return m.renderContent(combined, contentW, contentH)
}

func clamp[T cmp.Ordered](v, low, high T) T {
	return min(high, max(low, v))
}
