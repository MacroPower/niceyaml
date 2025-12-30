package yamlviewport

import (
	"cmp"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	tea "charm.land/bubbletea/v2"

	"github.com/macropower/niceyaml"
)

const (
	defaultHorizontalStep = 6
)

// DiffMode specifies how diffs are computed between revisions.
type DiffMode int

//nolint:grouper // Separate const block needed for iota.
const (
	// DiffModeAdjacent shows diff between consecutive revisions.
	DiffModeAdjacent DiffMode = iota
	// DiffModeOrigin shows diff between first revision and current.
	DiffModeOrigin
	// DiffModeNone shows current revision without diff.
	DiffModeNone
)

// Finder finds matches in lines for highlighting.
// The viewport invokes this during rerender to get fresh matches.
type Finder interface {
	// Find returns [niceyaml.PositionRange] values to highlight in the given
	// [niceyaml.Source]. Positions are 0-indexed. Returns nil if no matches.
	Find(lines *niceyaml.Source) []niceyaml.PositionRange
}

// Option is a configuration option that works in conjunction with [New].
type Option func(*Model)

// WithPrinter sets the [niceyaml.Printer] used for rendering.
// If not set, a default printer is created.
func WithPrinter(p *niceyaml.Printer) Option {
	return func(m *Model) {
		m.printer = p
	}
}

// WithStyle sets the container style for the viewport.
//
//nolint:gocritic // hugeParam: Copying.
func WithStyle(s lipgloss.Style) Option {
	return func(m *Model) {
		m.Style = s
	}
}

// WithSearchStyle sets the style for search highlights.
//
//nolint:gocritic // hugeParam: Copying.
func WithSearchStyle(s lipgloss.Style) Option {
	return func(m *Model) {
		m.SearchStyle = s
	}
}

// WithSelectedSearchStyle sets the style for the currently selected search match.
//
//nolint:gocritic // hugeParam: Copying.
func WithSelectedSearchStyle(s lipgloss.Style) Option {
	return func(m *Model) {
		m.SelectedSearchStyle = s
	}
}

// New returns a new model with the given options.
func New(opts ...Option) Model {
	var m Model

	for _, opt := range opts {
		opt(&m)
	}

	m.setInitialValues()

	return m
}

// Model is the Bubble Tea model for the YAML viewport.
//
//nolint:recvcheck // tea.Model requires value receivers for Init, Update, View.
type Model struct {
	// Style is the container style applied to the viewport frame.
	Style lipgloss.Style
	// SelectedSearchStyle is the style for the currently selected search match.
	SelectedSearchStyle lipgloss.Style
	// SearchStyle is the style for search match highlights.
	SearchStyle lipgloss.Style
	printer     *niceyaml.Printer
	// KeyMap contains the keybindings for viewport navigation.
	KeyMap         KeyMap
	finder         Finder
	searchMatches  []niceyaml.PositionRange
	revision       *niceyaml.Revision
	lines          *niceyaml.Source
	renderedLines  []string
	xOffset        int
	horizontalStep int
	// MouseWheelDelta is the number of lines to scroll per mouse wheel tick. Default: 3.
	MouseWheelDelta  int
	width            int
	searchIndex      int
	yOffset          int
	longestLineWidth int
	height           int
	diffMode         DiffMode
	// FillHeight pads output with empty lines to fill the viewport height when true.
	FillHeight bool
	// MouseWheelEnabled enables mouse wheel scrolling. Default: true.
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
	m.searchIndex = -1

	if m.printer == nil {
		m.printer = niceyaml.NewPrinter()
	}

	m.initialized = true
}

// Init satisfies the [tea.Model] interface.
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
		if m.WrapEnabled {
			m.printer.SetWidth(w)
			m.rerender()
		}
	}
}

// SetTokens replaces the revision history with a single revision.
// This is a convenience method equivalent to ClearRevisions() followed by AppendRevision(lines).
func (m *Model) SetTokens(lines *niceyaml.Source) {
	m.ClearRevisions()
	m.AppendRevision(lines)
}

// AppendRevision adds a new revision to the history.
// After appending, the revision pointer moves to the newly added revision.
func (m *Model) AppendRevision(lines *niceyaml.Source) {
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

// GoToRevision sets the current revision index (clamped to valid range).
// Index 0 shows the first revision without diff.
// Index 1 to N-1 shows a diff between revisions[index-1] and revisions[index].
func (m *Model) GoToRevision(index int) {
	if m.revision == nil {
		return
	}

	maxIndex := m.revision.Count() - 1
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

	return m.revision.Count()
}

// IsAtFirstRevision returns true if at revision index 0.
func (m *Model) IsAtFirstRevision() bool {
	if m.revision == nil {
		return true
	}

	return m.revision.AtOrigin()
}

// IsAtLatestRevision returns true if at the latest revision.
func (m *Model) IsAtLatestRevision() bool {
	if m.revision == nil {
		return true
	}

	return m.revision.AtTip()
}

// IsShowingDiff returns true if currently displaying a diff between revisions.
// This is true when not at the first revision and [DiffMode] is not [DiffModeNone].
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

// rerender renders the tokens using the Printer with current search highlights.
func (m *Model) rerender() {
	lines := m.getDisplayLines(nil)
	if lines == nil {
		m.renderedLines = nil
		m.longestLineWidth = 0
		m.lines = nil

		return
	}

	m.lines = lines

	if m.printer == nil {
		m.printer = niceyaml.NewPrinter()
	}

	m.printer.ClearStyles()

	// Compute fresh matches if finder is set.
	if m.finder != nil {
		m.searchMatches = m.finder.Find(lines)

		// Adjust search index if matches changed.
		if len(m.searchMatches) == 0 {
			m.searchIndex = -1
		} else if m.searchIndex >= len(m.searchMatches) || m.searchIndex < 0 {
			m.searchIndex = 0
		}
	}

	// Apply search highlights.
	for i, match := range m.searchMatches {
		style := m.SearchStyle
		if i == m.searchIndex {
			style = m.SelectedSearchStyle
		}

		m.printer.AddStyleToRange(&style, match)
	}

	content := m.printer.Print(lines)

	m.renderedLines = strings.Split(content, "\n")
	m.longestLineWidth = maxLineWidth(m.renderedLines)
}

// rerenderLine re-renders a single line and updates m.renderedLines.
func (m *Model) rerenderLine(idx int) {
	if m.lines == nil || idx < 0 || idx >= len(m.renderedLines) {
		return
	}

	content := m.printer.PrintSlice(m.lines, idx, idx)

	m.renderedLines[idx] = content

	lineWidth := ansi.StringWidth(content)
	if lineWidth > m.longestLineWidth {
		m.longestLineWidth = lineWidth
	}
}

// getDiffBaseRevision returns the base revision for diff comparison based on the current diff mode.
// Returns nil if diff mode is None or revision is nil.
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

// getDisplayLines returns the lines to display based on current revision and [DiffMode].
// The makeDiff func is called when a diff should be shown; if nil, uses [niceyaml.NewFullDiff].
func (m *Model) getDisplayLines(makeDiff func(base, current *niceyaml.Revision) *niceyaml.Source) *niceyaml.Source {
	if m.revision == nil {
		return nil
	}

	switch {
	case m.revision.AtOrigin():
		return m.revision.Origin().Lines()
	case m.diffMode == DiffModeNone:
		return m.revision.Lines()
	default:
		base := m.getDiffBaseRevision()
		if base == nil {
			return m.revision.Lines()
		}
		if makeDiff != nil {
			return makeDiff(base, m.revision)
		}

		return niceyaml.NewFullDiff(base, m.revision).Lines()
	}
}

// AtTop returns whether the viewport is at the top.
func (m *Model) AtTop() bool {
	return m.YOffset() <= 0
}

// AtBottom returns whether the viewport is at or past the bottom.
func (m *Model) AtBottom() bool {
	return m.YOffset() >= m.maxYOffset()
}

// PastBottom returns whether the viewport is scrolled past the last line.
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

// HorizontalScrollPercent returns the horizontal scroll position as a float between 0 and 1.
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

	// Apply horizontal scrolling.
	if m.xOffset > 0 {
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

// SetFinder sets a finder to be invoked during rerender.
// The finder receives the current revision's tokens and returns ranges to highlight.
// Pass nil to clear the finder and remove all highlights.
func (m *Model) SetFinder(finder Finder) {
	m.finder = finder

	if finder == nil {
		m.searchMatches = nil
		m.searchIndex = -1
	}

	m.rerender()
	m.scrollToCurrentMatch()
}

// ClearSearch removes all search highlights and clears the finder.
func (m *Model) ClearSearch() {
	m.finder = nil
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
	if m.lines != nil && oldIndex >= 0 {
		m.printer.ClearStyles()

		for i, match := range m.searchMatches {
			style := m.SearchStyle
			if i == m.searchIndex {
				style = m.SelectedSearchStyle
			}

			m.printer.AddStyleToRange(&style, match)
		}

		oldLine := m.searchMatches[oldIndex].Start.Line
		newLine := m.searchMatches[m.searchIndex].Start.Line

		m.rerenderLine(oldLine)
		if newLine != oldLine {
			m.rerenderLine(newLine)
		}
	} else {
		m.rerender()
	}

	m.scrollToCurrentMatch()
}

// SearchIndex returns the current search match index (0-based), or -1 if no matches.
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
	// Line is already 0-indexed in PositionRange.
	line := match.Start.Line

	// Center the match in the viewport.
	m.SetYOffset(line - m.maxHeight()/2)
}

// Update handles messages.
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
	contents := m.printer.GetStyle(niceyaml.StyleDefault).
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
// The context parameter specifies how many unchanged lines to show around each change.
// This is useful for showing a condensed diff view instead of the full file.
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
	lines := m.getDisplayLines(func(base, curr *niceyaml.Revision) *niceyaml.Source {
		return niceyaml.NewSummaryDiff(base, curr, context).Lines()
	})
	if lines == nil {
		return ""
	}

	return m.printer.Print(lines)
}

func clamp[T cmp.Ordered](v, low, high T) T {
	if high < low {
		low, high = high, low
	}

	return min(high, max(low, v))
}

func maxLineWidth(lines []string) int {
	result := 0
	for _, line := range lines {
		result = max(result, ansi.StringWidth(line))
	}

	return result
}
