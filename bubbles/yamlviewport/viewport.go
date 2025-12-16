package yamlviewport

import (
	"cmp"
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
	searchMatches       []niceyaml.PositionRange
	tokens              token.Tokens
	beforeTokens        token.Tokens
	lines               []string
	xOffset             int
	horizontalStep      int
	MouseWheelDelta     int
	width               int
	searchIndex         int
	yOffset             int
	longestLineWidth    int
	height              int
	FillHeight          bool
	MouseWheelEnabled   bool
	diffMode            bool
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

// SetTokens sets the YAML tokens to display and re-renders.
// Clears diff mode if active.
func (m *Model) SetTokens(tokens token.Tokens) {
	m.tokens = tokens
	m.beforeTokens = nil
	m.diffMode = false
	m.rerender()

	if m.YOffset() > m.maxYOffset() {
		m.GotoBottom()
	}
}

// SetFile sets the YAML file to display and re-renders.
// Clears diff mode if active.
func (m *Model) SetFile(file *ast.File) {
	tk := findAnyTokenInFile(file)
	m.tokens = getAllTokens(tk)
	m.beforeTokens = nil
	m.diffMode = false
	m.rerender()

	if m.YOffset() > m.maxYOffset() {
		m.GotoBottom()
	}
}

// SetDiff sets the YAML tokens for diff display and re-renders.
// The viewport will show a unified diff between before and after tokens.
func (m *Model) SetDiff(before, after token.Tokens) {
	m.beforeTokens = before
	m.tokens = after
	m.diffMode = true
	m.rerender()

	if m.YOffset() > m.maxYOffset() {
		m.GotoBottom()
	}
}

// SetFileDiff sets the YAML files for diff display and re-renders.
// The viewport will show a unified diff between before and after files.
func (m *Model) SetFileDiff(before, after *ast.File) {
	beforeTk := findAnyTokenInFile(before)
	afterTk := findAnyTokenInFile(after)
	m.beforeTokens = getAllTokens(beforeTk)
	m.tokens = getAllTokens(afterTk)
	m.diffMode = true
	m.rerender()

	if m.YOffset() > m.maxYOffset() {
		m.GotoBottom()
	}
}

// ClearDiff exits diff mode and clears the before tokens.
// The viewport will continue displaying the current "after" tokens in normal mode.
func (m *Model) ClearDiff() {
	m.beforeTokens = nil
	m.diffMode = false
	m.rerender()
}

// IsDiffMode returns whether the viewport is in diff mode.
func (m *Model) IsDiffMode() bool {
	return m.diffMode
}

// rerender renders the tokens using the Printer with current search highlights.
func (m *Model) rerender() {
	if len(m.tokens) == 0 && len(m.beforeTokens) == 0 {
		m.lines = nil
		m.longestLineWidth = 0

		return
	}

	if m.printer == nil {
		m.printer = niceyaml.NewPrinter()
	}

	m.printer.ClearStyles()

	// Apply search highlights.
	for i, match := range m.searchMatches {
		style := m.SearchStyle
		if i == m.searchIndex {
			style = m.SelectedSearchStyle
		}

		m.printer.AddStyleToRange(&style, match)
	}

	var content string
	if m.diffMode {
		content = m.printer.PrintTokenDiff(m.beforeTokens, m.tokens)
	} else {
		content = m.printer.PrintTokens(m.tokens)
	}

	m.lines = strings.Split(content, "\n")
	m.longestLineWidth = maxLineWidth(m.lines)
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

// SetSearchMatches sets the search highlight ranges.
// Use [niceyaml.Finder.FindStringsInTokens] to generate matches.
func (m *Model) SetSearchMatches(matches []niceyaml.PositionRange) {
	m.searchMatches = matches
	if len(matches) > 0 {
		m.searchIndex = 0
	} else {
		m.searchIndex = -1
	}

	m.rerender()
	m.scrollToCurrentMatch()
}

// ClearSearch removes all search highlights.
func (m *Model) ClearSearch() {
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

// View renders the viewport.
//
//nolint:gocritic // hugeParam: required for tea.Model interface compatibility.
func (m Model) View() string {
	w, h := m.Width(), m.Height()
	if sw := m.Style.GetWidth(); sw != 0 {
		w = min(w, sw)
	}
	if sh := m.Style.GetHeight(); sh != 0 {
		h = min(h, sh)
	}

	if w == 0 || h == 0 {
		return ""
	}

	contentWidth := w - m.Style.GetHorizontalFrameSize()
	contentHeight := h - m.Style.GetVerticalFrameSize()
	contents := m.printer.GetStyle(niceyaml.StyleDefault).
		Width(contentWidth).
		Height(contentHeight).
		Render(strings.Join(m.visibleLines(), "\n"))

	return m.Style.
		UnsetWidth().UnsetHeight().
		Render(contents)
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
