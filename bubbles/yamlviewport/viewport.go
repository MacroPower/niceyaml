package yamlviewport

import (
	"cmp"
	"fmt"
	"hash/fnv"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

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

// Finder finds matches in tokens for highlighting.
// The viewport invokes this during rerender to get fresh matches.
type Finder interface {
	// FindTokens returns position ranges to highlight in the given tokens.
	// Returns nil if no matches.
	FindTokens(tokens token.Tokens) []niceyaml.PositionRange
}

// Revision holds metadata for a single revision.
type Revision struct {
	Hash uint64 // FNV-1a hash of token content for deduplication.
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
	Style               lipgloss.Style
	SelectedSearchStyle lipgloss.Style
	SearchStyle         lipgloss.Style
	printer             *niceyaml.Printer
	KeyMap              KeyMap
	finder              Finder
	searchMatches       []niceyaml.PositionRange
	revisions           []Revision
	tokensByHash        map[uint64]token.Tokens
	lines               []string
	xOffset             int
	horizontalStep      int
	MouseWheelDelta     int
	width               int
	searchIndex         int
	yOffset             int
	longestLineWidth    int
	height              int
	revisionIndex       int
	diffMode            DiffMode
	FillHeight          bool
	MouseWheelEnabled   bool
	initialized         bool
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
	m.width = w
}

// SetTokens replaces the revision history with a single revision.
// This is a convenience method equivalent to SetRevisions([]token.Tokens{tokens}).
func (m *Model) SetTokens(tokens token.Tokens) {
	m.SetRevisions([]token.Tokens{tokens})
}

// SetFile replaces the revision history with a single revision from a file.
// This is a convenience method equivalent to ClearRevisions() followed by AppendFileRevision(file).
func (m *Model) SetFile(file *ast.File) {
	m.ClearRevisions()
	m.AppendFileRevision(file)
}

// SetRevisions replaces all revisions with the given slice.
// The revision index is set to len(revisions), showing the latest revision without diff.
// Pass nil or empty slice to clear all content.
func (m *Model) SetRevisions(revisions []token.Tokens) {
	if len(revisions) == 0 {
		m.revisions = nil
		m.tokensByHash = nil
		m.revisionIndex = 0
	} else {
		m.revisions = make([]Revision, len(revisions))
		m.tokensByHash = make(map[uint64]token.Tokens)

		for i, tokens := range revisions {
			hash := hashTokens(tokens)
			m.revisions[i] = Revision{Hash: hash}
			m.tokensByHash[hash] = tokens
		}

		m.revisionIndex = len(revisions)
	}

	m.rerender()

	if m.YOffset() > m.maxYOffset() {
		m.GotoBottom()
	}
}

// AppendRevision adds a new revision to the history.
// The revision index is set to len(revisions), showing the latest revision without diff.
func (m *Model) AppendRevision(revision token.Tokens) {
	hash := hashTokens(revision)
	m.revisions = append(m.revisions, Revision{Hash: hash})

	if m.tokensByHash == nil {
		m.tokensByHash = make(map[uint64]token.Tokens)
	}

	m.tokensByHash[hash] = revision
	m.revisionIndex = len(m.revisions)
	m.rerender()

	if m.YOffset() > m.maxYOffset() {
		m.GotoBottom()
	}
}

// AppendFileRevision adds a new revision from an [*ast.File] to the history.
// The revision index is set to len(revisions), showing the latest revision without diff.
func (m *Model) AppendFileRevision(file *ast.File) {
	tk := findAnyTokenInFile(file)
	tokens := getAllTokens(tk)
	m.AppendRevision(tokens)
}

// ClearRevisions removes all revisions from the history.
func (m *Model) ClearRevisions() {
	m.revisions = nil
	m.tokensByHash = nil
	m.revisionIndex = 0
	m.rerender()
}

// RevisionIndex returns the current revision index.
// Returns 0 if revisions are empty.
func (m *Model) RevisionIndex() int {
	return m.revisionIndex
}

// GoToRevision sets the current revision index (clamped to valid range).
// Index 0 shows the first revision without diff.
// Index 1 to N-1 shows a diff between revisions[index-1] and revisions[index].
// Index N (len) shows the latest revision without diff.
func (m *Model) GoToRevision(index int) {
	maxIndex := len(m.revisions)
	m.revisionIndex = clamp(index, 0, maxIndex)
	m.rerender()
	m.GotoTop()
}

// RevisionCount returns the number of revisions in the history.
func (m *Model) RevisionCount() int {
	return len(m.revisions)
}

// currentRevisionTokens returns the tokens for the currently displayed revision.
// For diffs, this returns the "after" revision tokens.
func (m *Model) currentRevisionTokens() token.Tokens {
	n := len(m.revisions)
	if n == 0 {
		return nil
	}

	switch {
	case m.revisionIndex == 0:
		return m.getRevisionTokens(0)
	case m.revisionIndex >= n:
		return m.getRevisionTokens(n - 1)
	default:
		return m.getRevisionTokens(m.revisionIndex)
	}
}

// getRevisionTokens returns the tokens for the revision at the given index.
func (m *Model) getRevisionTokens(index int) token.Tokens {
	if index < 0 || index >= len(m.revisions) {
		return nil
	}

	return m.tokensByHash[m.revisions[index].Hash]
}

// IsAtFirstRevision returns true if at revision index 0.
func (m *Model) IsAtFirstRevision() bool {
	return m.revisionIndex == 0
}

// IsAtLatestRevision returns true if at the latest revision index (len(revisions)).
func (m *Model) IsAtLatestRevision() bool {
	return m.revisionIndex >= len(m.revisions)
}

// IsShowingDiff returns true if currently displaying a diff between revisions.
// This is true when 0 < revisionIndex < len(revisions) and diffMode is not None.
func (m *Model) IsShowingDiff() bool {
	n := len(m.revisions)
	return n > 0 && m.revisionIndex > 0 && m.revisionIndex < n && m.diffMode != DiffModeNone
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
func (m *Model) NextRevision() {
	if m.revisionIndex < len(m.revisions) {
		m.revisionIndex++
		m.rerender()
		m.GotoTop()
	}
}

// PrevRevision moves to the previous revision in history.
// If already at the first (index 0), does nothing.
func (m *Model) PrevRevision() {
	if m.revisionIndex > 0 {
		m.revisionIndex--
		m.rerender()
		m.GotoTop()
	}
}

// rerender renders the tokens using the Printer with current search highlights.
func (m *Model) rerender() {
	n := len(m.revisions)
	if n == 0 {
		m.lines = nil
		m.longestLineWidth = 0

		return
	}

	if m.printer == nil {
		m.printer = niceyaml.NewPrinter()
	}

	m.printer.ClearStyles()

	// Compute fresh matches if finder is set.
	if m.finder != nil {
		tokens := m.currentRevisionTokens()
		m.searchMatches = m.finder.FindTokens(tokens)

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

	var content string

	switch {
	case m.revisionIndex == 0:
		// At first position: show first revision without diff.
		content = m.printer.PrintTokens(m.getRevisionTokens(0))
	case m.revisionIndex >= n:
		// At or past latest: show last revision without diff.
		content = m.printer.PrintTokens(m.getRevisionTokens(n - 1))
	default:
		// In between: show diff.
		content = m.getDiffContent()
	}

	m.lines = strings.Split(content, "\n")
	m.longestLineWidth = maxLineWidth(m.lines)
}

func (m *Model) getDiffContent() string {
	switch m.diffMode {
	case DiffModeOrigin:
		before := m.getRevisionTokens(0)
		after := m.getRevisionTokens(m.revisionIndex)

		return m.printer.PrintTokenDiff(before, after)

	case DiffModeAdjacent:
		before := m.getRevisionTokens(m.revisionIndex - 1)
		after := m.getRevisionTokens(m.revisionIndex)

		return m.printer.PrintTokenDiff(before, after)

	case DiffModeNone:
		return m.printer.PrintTokens(m.getRevisionTokens(m.revisionIndex))
	}

	panic("unexpected diff mode")
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
	total := len(m.lines)
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
	return max(0, len(m.lines)-m.maxHeight())
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
func (m *Model) visibleLines() []string {
	maxHeight := m.maxHeight()
	maxWidth := m.maxWidth()

	if maxHeight == 0 || maxWidth == 0 {
		return nil
	}

	total := len(m.lines)
	if total == 0 {
		if m.FillHeight {
			return make([]string, maxHeight)
		}

		return nil
	}

	start := m.YOffset()
	end := min(start+maxHeight, total)

	// Determine final capacity based on FillHeight.
	capacity := end - start
	if m.FillHeight && capacity < maxHeight {
		capacity = maxHeight
	}

	lines := make([]string, capacity)
	copy(lines, m.lines[start:end])

	// Apply horizontal scrolling if content is wider than viewport.
	if m.xOffset > 0 || m.longestLineWidth > maxWidth {
		for i := range lines {
			lines[i] = ansi.Cut(lines[i], m.xOffset, m.xOffset+maxWidth)
		}
	}

	return lines
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
	if m.AtBottom() || n == 0 || len(m.lines) == 0 {
		return
	}

	m.SetYOffset(m.YOffset() + n)
}

// ScrollUp moves the view up by n lines.
func (m *Model) ScrollUp(n int) {
	if m.AtTop() || n == 0 || len(m.lines) == 0 {
		return
	}

	m.SetYOffset(m.YOffset() - n)
}

// PageDown moves the view down by one page.
func (m *Model) PageDown() {
	if m.AtBottom() {
		return
	}

	m.ScrollDown(m.maxHeight())
}

// PageUp moves the view up by one page.
func (m *Model) PageUp() {
	if m.AtTop() {
		return
	}

	m.ScrollUp(m.maxHeight())
}

// HalfPageDown moves the view down by half a page.
func (m *Model) HalfPageDown() {
	if m.AtBottom() {
		return
	}

	m.ScrollDown(m.maxHeight() / 2) //nolint:mnd // Half page.
}

// HalfPageUp moves the view up by half a page.
func (m *Model) HalfPageUp() {
	if m.AtTop() {
		return
	}

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
	if m.AtTop() {
		return
	}

	m.SetYOffset(0)
}

// GotoBottom scrolls to the bottom.
func (m *Model) GotoBottom() {
	m.SetYOffset(m.maxYOffset())
}

// TotalLineCount returns the total number of lines.
func (m *Model) TotalLineCount() int {
	return len(m.lines)
}

// VisibleLineCount returns the number of visible lines.
func (m *Model) VisibleLineCount() int {
	return len(m.visibleLines())
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
	if len(m.searchMatches) == 0 {
		return
	}

	m.searchIndex = (m.searchIndex + 1) % len(m.searchMatches)
	m.rerender()
	m.scrollToCurrentMatch()
}

// SearchPrevious navigates to the previous search match.
func (m *Model) SearchPrevious() {
	if len(m.searchMatches) == 0 {
		return
	}

	m.searchIndex = (m.searchIndex - 1 + len(m.searchMatches)) % len(m.searchMatches)
	m.rerender()
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

// scrollToCurrentMatch scrolls to make the current search match visible.
func (m *Model) scrollToCurrentMatch() {
	if m.searchIndex < 0 || m.searchIndex >= len(m.searchMatches) {
		return
	}

	match := m.searchMatches[m.searchIndex]
	// Line is 1-indexed in PositionRange, convert to 0-indexed.
	line := match.Start.Line - 1

	// Ensure the match line is visible.
	if line < m.YOffset() {
		m.SetYOffset(line)
	} else if line >= m.YOffset()+m.maxHeight() {
		m.SetYOffset(line - m.maxHeight() + 1)
	}
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

		switch msg.Button {
		case tea.MouseWheelDown:
			if msg.Mod.Contains(tea.ModShift) {
				m.ScrollRight(m.horizontalStep)
				break
			}

			m.ScrollDown(m.MouseWheelDelta)

		case tea.MouseWheelUp:
			if msg.Mod.Contains(tea.ModShift) {
				m.ScrollLeft(m.horizontalStep)
				break
			}

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

// applyScrolling applies Y and X offset scrolling to a slice of lines.
func (m *Model) applyScrolling(lines []string) []string {
	maxHeight := m.maxHeight()
	maxWidth := m.maxWidth()
	total := len(lines)

	if total == 0 {
		return nil
	}

	start := m.YOffset()
	end := min(start+maxHeight, total)

	if start >= total {
		start = max(0, total-maxHeight)
		end = total
	}

	visible := make([]string, end-start)
	copy(visible, lines[start:end])

	if m.xOffset > 0 {
		for i := range visible {
			visible[i] = ansi.Cut(visible[i], m.xOffset, m.xOffset+maxWidth)
		}
	}

	return visible
}

// View renders the viewport.
//
//nolint:gocritic // hugeParam: required for tea.Model interface compatibility.
func (m Model) View() string {
	w, h, ok := m.getViewDimensions()
	if !ok {
		return ""
	}

	return m.renderContent(m.visibleLines(), w, h)
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

	return m.renderContent(m.applyScrolling(summaryLines), w, h)
}

// getSummaryDiffContent returns summary diff content for the current revision.
func (m *Model) getSummaryDiffContent(context int) string {
	n := len(m.revisions)
	if n == 0 {
		return ""
	}

	// At first or last position, show tokens without diff.
	// Note: revisionIndex ranges from 0 to n (inclusive), where n = len(revisions).
	// - Index 0 is "before first revision".
	// - Indices 1..n-1 show diffs.
	// - Index n is "after last revision".
	if m.revisionIndex == 0 || m.revisionIndex >= n {
		return m.printer.PrintTokens(m.currentRevisionTokens())
	}

	// In between: show summary diff.
	switch m.diffMode {
	case DiffModeOrigin:
		before := m.getRevisionTokens(0)
		after := m.getRevisionTokens(m.revisionIndex)

		return m.printer.PrintTokenDiffSummary(before, after, context)

	case DiffModeAdjacent:
		before := m.getRevisionTokens(m.revisionIndex - 1)
		after := m.getRevisionTokens(m.revisionIndex)

		return m.printer.PrintTokenDiffSummary(before, after, context)

	case DiffModeNone:
		return m.printer.PrintTokens(m.getRevisionTokens(m.revisionIndex))
	}

	// This should be impossible to reach with the above exhaustive switch.
	panic(fmt.Sprintf("unimplemented diff mode: %+v", m.diffMode))
}

func clamp[T cmp.Ordered](v, low, high T) T {
	if high < low {
		low, high = high, low
	}

	return min(high, max(low, v))
}

func hashTokens(tokens token.Tokens) uint64 {
	h := fnv.New64a()
	for _, tk := range tokens {
		_, _ = h.Write([]byte(tk.Origin)) // Hash.Hash.Write never returns an error.
	}

	return h.Sum64()
}

func maxLineWidth(lines []string) int {
	result := 0
	for _, line := range lines {
		result = max(result, ansi.StringWidth(line))
	}

	return result
}

func findAnyTokenInFile(f *ast.File) *token.Token {
	for _, doc := range f.Docs {
		if doc.Start != nil {
			return doc.Start
		}
		if doc.Body != nil {
			return doc.Body.GetToken()
		}
		if doc.End != nil {
			return doc.End
		}
	}

	return nil
}

func getAllTokens(tk *token.Token) token.Tokens {
	// Walk backward to find the first token.
	for tk.Prev != nil {
		tk = tk.Prev
	}

	// Clone the first token.
	firstTk := tk.Clone()

	// Preserve leading whitespace from previous token.
	if firstTk.Prev != nil {
		prev := firstTk.Prev
		whiteSpaceLen := len(prev.Origin) - len(strings.TrimRight(prev.Origin, " "))
		if whiteSpaceLen > 0 {
			firstTk.Origin = strings.Repeat(" ", whiteSpaceLen) + firstTk.Origin
		}
	}

	tokens := token.Tokens{firstTk}

	// Walk forward to collect tokens up to maxLine.
	for t := tk.Next; t != nil; t = t.Next {
		// Skip parser-added implicit null tokens to match lexer output.
		if t.Type == token.ImplicitNullType {
			continue
		}

		tokens.Add(t.Clone())
	}

	return tokens
}
