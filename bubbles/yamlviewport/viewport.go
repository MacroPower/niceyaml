package yamlviewport

import (
	"cmp"
	"iter"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	tea "charm.land/bubbletea/v2"

	"jacobcolvin.com/niceyaml"
	"jacobcolvin.com/niceyaml/line"
	"jacobcolvin.com/niceyaml/position"
	"jacobcolvin.com/niceyaml/style"
)

const defaultHorizontalStep = 6

const (
	// SearchOverlayKind identifies search match highlights in the viewport.
	//
	// Configure the style via [WithSearchStyle] or by including it in a
	// [Printer]'s style set.
	SearchOverlayKind style.Style = iota
	// SelectedSearchOverlayKind identifies the currently selected search match.
	//
	// Configure the style via [WithSelectedSearchStyle] or by including it in a
	// [Printer]'s style set.
	SelectedSearchOverlayKind
)

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

// Source provides access to YAML content as lines and runes with overlay support.
//
// See [niceyaml.Source] for an implementation.
type Source interface {
	Name() string
	AllLines(spans ...position.Span) iter.Seq2[position.Position, line.Line]
	AllRunes(ranges ...position.Range) iter.Seq2[position.Position, rune]
	IsEmpty() bool
	Len() int
	AddOverlay(s style.Style, ranges ...position.Range)
	ClearOverlays()
}

// DiffMode specifies how diffs are computed between revisions.
//
// Use [Model.SetDiffMode] to change the mode, or [Model.ToggleDiffMode] to
// cycle through modes.
type DiffMode int

//nolint:grouper // Separate const block needed for iota.
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

// Option configures a [Model].
//
// Available options:
//   - [WithPrinter]
//   - [WithStyle]
//   - [WithSearchStyle]
//   - [WithSelectedSearchStyle]
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

// WithSearchStyle is an [Option] that sets the style for search highlights.
//
//nolint:gocritic // hugeParam: Copying.
func WithSearchStyle(s lipgloss.Style) Option {
	return func(m *Model) {
		m.SearchStyle = s
	}
}

// WithSelectedSearchStyle is an [Option] that sets the style for the currently
// selected search match.
//
//nolint:gocritic // hugeParam: Copying.
func WithSelectedSearchStyle(s lipgloss.Style) Option {
	return func(m *Model) {
		m.SelectedSearchStyle = s
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
	Style lipgloss.Style
	// SelectedSearchStyle is the style for the currently selected search match.
	SelectedSearchStyle lipgloss.Style
	// SearchStyle is the style for search match highlights.
	SearchStyle lipgloss.Style
	printer     Printer
	// KeyMap contains the keybindings for viewport navigation.
	KeyMap         KeyMap
	searchTerm     string // Current search query.
	finder         Finder
	searchMatches  []position.Range
	revision       *niceyaml.Revision
	diffResult     *niceyaml.DiffResult // Cached diff between base and current revision.
	lines          Source
	renderedLines  []string
	xOffset        int
	horizontalStep int
	// MouseWheelDelta is the number of lines to scroll per mouse wheel tick.
	// Default: 3.
	MouseWheelDelta  int
	width            int
	searchIndex      int
	yOffset          int
	longestLineWidth int
	height           int
	diffMode         DiffMode
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
	m.WrapEnabled = true
	m.searchIndex = -1

	if m.printer == nil {
		m.printer = m.createDefaultPrinter()
	}

	if m.finder == nil {
		m.finder = niceyaml.NewFinder(
			niceyaml.WithNormalizer(niceyaml.NewStandardNormalizer()),
		)
	}

	m.initialized = true
}

// createDefaultPrinter creates a printer with search highlight styles included.
func (m *Model) createDefaultPrinter() *niceyaml.Printer {
	return niceyaml.NewPrinter(
		niceyaml.WithStyles(m.searchStyles()),
	)
}

// searchStyles returns styles that include search highlight styling.
func (m *Model) searchStyles() style.Styles {
	return style.NewStyles(lipgloss.Style{},
		style.Set(SearchOverlayKind, m.SearchStyle),
		style.Set(SelectedSearchOverlayKind, m.SelectedSearchStyle),
	)
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
func (m *Model) SetTokens(lines niceyaml.NamedLineSource) {
	m.ClearRevisions()
	m.AddRevision(lines)
}

// AddRevision adds a new revision to the history.
// After adding, the revision pointer moves to the newly added revision.
func (m *Model) AddRevision(lines niceyaml.NamedLineSource) {
	if m.revision == nil {
		m.revision = niceyaml.NewRevision(lines)
	} else {
		m.revision = m.revision.Tip().Append(lines)
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
	if m.revision == nil {
		return 0
	}

	return m.revision.Index()
}

// RevisionName returns the name of the current revision.
// Returns empty string if revisions are empty.
func (m *Model) RevisionName() string {
	if m.revision == nil {
		return ""
	}

	return m.revision.Name()
}

// RevisionNames returns all revision names in order.
func (m *Model) RevisionNames() []string {
	if m.revision == nil {
		return nil
	}

	return m.revision.Names()
}

// GoToRevision navigates to the revision at index, clamped to the valid range.
// Index 0 always shows the first revision without diff markers.
// Index 1 to N-1 shows a diff based on the current [DiffMode].
func (m *Model) GoToRevision(index int) {
	if m.revision == nil {
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
	if m.revision == nil {
		return 0
	}

	return m.revision.Len()
}

// IsAtFirstRevision reports whether the viewport is at revision index 0.
func (m *Model) IsAtFirstRevision() bool {
	if m.revision == nil {
		return true
	}

	return m.revision.AtOrigin()
}

// IsAtLatestRevision reports whether the viewport is at the latest revision.
func (m *Model) IsAtLatestRevision() bool {
	if m.revision == nil {
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
	return m.revision != nil && !m.revision.AtOrigin() && m.diffMode != DiffModeNone
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
	if m.revision == nil {
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

// rerender renders the tokens using the [Printer] with current search
// highlights.
func (m *Model) rerender() {
	m.diffResult = nil // Invalidate cached diff result.

	lines := m.getDisplayLines(nil)
	if lines == nil {
		m.renderedLines = nil
		m.longestLineWidth = 0
		m.lines = nil

		return
	}

	m.lines = lines

	if m.printer == nil {
		m.printer = m.createDefaultPrinter()
	}

	m.updateSearchState(lines)

	// Apply search highlights via overlays.
	m.applySearchOverlays(lines)

	content := m.printer.Print(lines)

	m.renderedLines = strings.Split(content, "\n")
	m.longestLineWidth = maxLineWidth(m.renderedLines)
}

// applySearchOverlays sets overlay highlights for all search matches.
func (m *Model) applySearchOverlays(lines Source) {
	lines.ClearOverlays()

	for i, match := range m.searchMatches {
		if i == m.searchIndex {
			lines.AddOverlay(SelectedSearchOverlayKind, match)
		} else {
			lines.AddOverlay(SearchOverlayKind, match)
		}
	}
}

// updateSearchState updates the finder and search matches for the given lines.
func (m *Model) updateSearchState(lines Source) {
	if m.searchTerm == "" {
		m.searchMatches = nil

		return
	}

	m.finder.Load(lines)

	m.searchMatches = m.finder.Find(m.searchTerm)

	// Adjust search index if matches changed.
	switch {
	case len(m.searchMatches) == 0:
		m.searchIndex = -1
	case m.searchIndex >= len(m.searchMatches), m.searchIndex < 0:
		m.searchIndex = 0
	}
}

// rerenderLine re-renders a single line and updates m.renderedLines.
func (m *Model) rerenderLine(idx int) {
	if m.lines == nil || idx < 0 || idx >= len(m.renderedLines) {
		return
	}

	content := m.printer.Print(m.lines, position.NewSpan(idx, idx+1))

	m.renderedLines[idx] = content

	lineWidth := ansi.StringWidth(content)
	if lineWidth > m.longestLineWidth {
		m.longestLineWidth = lineWidth
	}
}

// getDiffBaseRevision returns the base revision for diff comparison based on
// the current [DiffMode].
// Returns nil if diff mode is [DiffModeNone] or revision is nil.
func (m *Model) getDiffBaseRevision() *niceyaml.Revision {
	if m.revision == nil {
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
//
// The makeDiff func is called when a diff should be shown; if nil, uses
// [niceyaml.Diff].
func (m *Model) getDisplayLines(makeDiff func(base, current *niceyaml.Revision) Source) Source {
	if m.revision == nil {
		return nil
	}

	switch {
	case m.revision.AtOrigin():
		return m.revision.Origin().Source()
	case m.diffMode == DiffModeNone:
		return m.revision.Source()
	default:
		base := m.getDiffBaseRevision()
		if base == nil {
			return m.revision.Source()
		}
		if makeDiff != nil {
			return makeDiff(base, m.revision)
		}

		return m.getDiffResult().Full()
	}
}

// getDiffResult returns the cached [niceyaml.DiffResult], computing it if nil.
func (m *Model) getDiffResult() *niceyaml.DiffResult {
	if m.diffResult == nil {
		m.diffResult = niceyaml.Diff(m.getDiffBaseRevision(), m.revision)
	}

	return m.diffResult
}

// AtTop reports whether the viewport is scrolled to the top.
func (m *Model) AtTop() bool {
	return m.YOffset() <= 0
}

// AtBottom reports whether the viewport is scrolled to or past the bottom.
func (m *Model) AtBottom() bool {
	return m.YOffset() >= m.maxYOffset()
}

// PastBottom reports whether the viewport is scrolled past the last line.
func (m *Model) PastBottom() bool {
	return m.YOffset() > m.maxYOffset()
}

// ScrollPercent returns the vertical scroll position as a float between 0 and 1.
func (m *Model) ScrollPercent() float64 {
	total := len(m.renderedLines)
	if m.maxHeight() >= total {
		return 1.0
	}

	y := float64(m.YOffset())
	h := float64(m.maxHeight())
	t := float64(total)
	v := y / (t - h)

	return clamp(v, 0, 1)
}

// HorizontalScrollPercent returns the horizontal scroll position as a float
// between 0 and 1.
func (m *Model) HorizontalScrollPercent() float64 {
	if m.xOffset >= m.longestLineWidth-m.maxWidth() {
		return 1.0
	}

	x := float64(m.xOffset)
	w := float64(m.maxWidth())
	t := float64(m.longestLineWidth)
	v := x / (t - w)

	return clamp(v, 0, 1)
}

// maxYOffset returns the maximum Y offset.
func (m *Model) maxYOffset() int {
	return max(0, len(m.renderedLines)-m.maxHeight())
}

// maxXOffset returns the maximum X offset.
func (m *Model) maxXOffset() int {
	return max(0, m.longestLineWidth-m.maxWidth())
}

// maxWidth returns the content width accounting for frame size.
func (m *Model) maxWidth() int {
	return max(0, m.Width()-m.Style.GetHorizontalFrameSize())
}

// maxHeight returns the content height accounting for frame size.
func (m *Model) maxHeight() int {
	return max(0, m.Height()-m.Style.GetVerticalFrameSize())
}

// visibleLines returns the lines currently visible in the viewport.
// If lines is nil, uses m.lines.
func (m *Model) visibleLines(lines []string) []string {
	maxHeight := m.maxHeight()
	maxWidth := m.maxWidth()

	if maxHeight == 0 || maxWidth == 0 {
		return nil
	}

	if lines == nil {
		lines = m.renderedLines
	}

	total := len(lines)

	if total == 0 {
		if m.FillHeight {
			return make([]string, maxHeight)
		}

		return nil
	}

	start := m.YOffset()
	if start >= total {
		start = max(0, total-maxHeight)
	}

	end := min(start+maxHeight, total)

	// Determine final capacity based on FillHeight.
	capacity := end - start
	if m.FillHeight && capacity < maxHeight {
		capacity = maxHeight
	}

	result := make([]string, capacity)
	copy(result, lines[start:end])

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
	if m.AtBottom() || n == 0 || len(m.renderedLines) == 0 {
		return
	}

	m.SetYOffset(m.YOffset() + n)
}

// ScrollUp moves the view up by n lines.
func (m *Model) ScrollUp(n int) {
	if m.AtTop() || n == 0 || len(m.renderedLines) == 0 {
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
	m.ScrollDown(m.maxHeight() / 2) //nolint:mnd // Half page.
}

// HalfPageUp moves the view up by half a page.
func (m *Model) HalfPageUp() {
	m.ScrollUp(m.maxHeight() / 2) //nolint:mnd // Half page.
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
	return len(m.renderedLines)
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

	oldIndex := m.searchIndex
	m.searchIndex = (m.searchIndex + delta + len(m.searchMatches)) % len(m.searchMatches)

	// Only re-render the affected lines if cache is available.
	if m.lines == nil || oldIndex < 0 {
		m.rerender()
		m.scrollToCurrentMatch()

		return
	}

	m.applySearchOverlays(m.lines)

	oldLine := m.searchMatches[oldIndex].Start.Line
	newLine := m.searchMatches[m.searchIndex].Start.Line

	m.rerenderLine(oldLine)
	if newLine != oldLine {
		m.rerenderLine(newLine)
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
	startLine := match.Start.Line

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
	textStyle := lipgloss.Style{}
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
//nolint:gocritic // hugeParam: required for tea.Model interface compatibility.
func (m Model) View() string {
	w, h, ok := m.getViewDimensions()
	if !ok {
		return ""
	}

	return m.renderContent(m.visibleLines(nil), w, h)
}

// ViewSummary renders the viewport with summary diff (changes + context only).
//
// The context parameter specifies how many unchanged lines to show around each
// change. This is useful for showing a condensed diff view instead of the full
// file.
//
//nolint:gocritic // hugeParam: required for tea.Model interface compatibility.
func (m Model) ViewSummary(context int) string {
	w, h, ok := m.getViewDimensions()
	if !ok {
		return ""
	}

	summaryContent := m.getSummaryDiffContent(context)
	summaryLines := strings.Split(summaryContent, "\n")

	if len(summaryLines) == 0 {
		return ""
	}

	return m.renderContent(m.visibleLines(summaryLines), w, h)
}

// getSummaryDiffContent returns summary diff content for the current revision.
func (m *Model) getSummaryDiffContent(context int) string {
	if m.revision == nil {
		return ""
	}

	switch {
	case m.revision.AtOrigin():
		return m.printer.Print(m.revision.Origin().Source())
	case m.diffMode == DiffModeNone:
		return m.printer.Print(m.revision.Source())
	default:
		base := m.getDiffBaseRevision()
		if base == nil {
			return m.printer.Print(m.revision.Source())
		}

		source, ranges := m.getDiffResult().Hunks(context)

		return m.printer.Print(source, ranges...)
	}
}

func clamp[T cmp.Ordered](v, low, high T) T {
	if high < low {
		low, high = high, low
	}

	return min(high, max(low, v))
}

func maxLineWidth(lines []string) int {
	result := 0
	for _, li := range lines {
		result = max(result, ansi.StringWidth(li))
	}

	return result
}
