package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"

	tea "charm.land/bubbletea/v2"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/bubbles/yamlviewport"
	"go.jacobcolvin.com/niceyaml/internal/cells"
	"go.jacobcolvin.com/niceyaml/internal/escape"
	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/style/kind"
	"go.jacobcolvin.com/niceyaml/style/theme"
)

// statusBarHeight is the number of terminal rows the status bar occupies
// below the viewport.
const statusBarHeight = 2

type modelOptions struct {
	search      string
	sources     []*niceyaml.Source
	lineNumbers bool
}

type model struct {
	searchInput string
	// The term of the --search flag. The model holds it until the first
	// [tea.WindowSizeMsg] gives the viewport its size.
	pendingSearch string
	currentTheme  string
	previousTheme string
	themeList     []string
	// Styles of the current theme. The model builds them once per theme
	// switch, and the printer, the status bar, and the theme picker share
	// them.
	styles       style.Styles
	viewport     yamlviewport.Model
	width        int
	height       int
	themeIndex   int
	lineNumbers  bool
	searching    bool
	themePicking bool
}

func newModel(opts *modelOptions) model {
	themeList := darkThemeNames()

	m := model{
		viewport:    yamlviewport.New(),
		themeList:   themeList,
		themeIndex:  max(0, slices.Index(themeList, theme.Charm.Name)),
		lineNumbers: opts.lineNumbers,
	}

	m.applyTheme(theme.Charm.Name)

	revisions := make([]yamlviewport.Revision, len(opts.sources))
	for i, source := range opts.sources {
		revisions[i] = source
	}

	m.viewport.AddRevisions(revisions...)

	// The viewport centers a match in the rows it has, and it has none
	// until the first tea.WindowSizeMsg sizes it, so the initial term waits
	// for that message.
	m.pendingSearch = opts.search

	return m
}

// revisionLabels names each file for the status bar. A file's base name
// labels it, so a full path stays out of the bar. Two files sharing a base
// name, such as a/config.yaml and b/config.yaml, each fall back to the path
// as the user typed it, so the bar tells the revisions apart.
func revisionLabels(paths []string) []string {
	labels := make([]string, len(paths))
	counts := make(map[string]int, len(paths))

	for i, path := range paths {
		labels[i] = filepath.Base(path)
		counts[labels[i]]++
	}

	for i, path := range paths {
		if counts[labels[i]] > 1 {
			labels[i] = path
		}
	}

	return labels
}

// Init implements [tea.Model].
//
//nolint:gocritic // hugeParam: required for tea.Model interface.
func (m model) Init() tea.Cmd {
	return nil
}

// Update implements [tea.Model].
//
//nolint:gocritic // hugeParam: required for tea.Model interface.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport.SetWidth(msg.Width)
		// Reserve rows for the status bar. A terminal shorter than the
		// status bar leaves the viewport no rows rather than a negative
		// height.
		m.viewport.SetHeight(max(0, msg.Height-statusBarHeight))

		if m.pendingSearch != "" {
			m.applySearch(m.pendingSearch)

			m.pendingSearch = ""
		}

	case tea.PasteMsg:
		// The terminal hands pasted text over as one message rather than
		// as key presses, so the search prompt reads it here. The theme
		// picker and the viewport have no use for it.
		if m.searching {
			m.searchInput += pastedSearchText(msg.Content)
		}

		return m, nil

	case tea.MouseWheelMsg:
		// The theme picker takes the wheel, so the document under it stays
		// where it is and the wheel moves the selection instead.
		if m.themePicking {
			m.updateThemeWheel(msg)

			return m, nil
		}

	case tea.KeyPressMsg:
		// Quit on ctrl+c from every state, including the theme picker and the
		// search prompt, which otherwise consume every key.
		if key.Matches(msg, key.NewBinding(key.WithKeys("ctrl+c"))) {
			return m, tea.Quit
		}

		// Handle theme picker input.
		if m.themePicking {
			m.updateThemeInput(msg)

			return m, nil
		}

		if m.searching {
			m.updateSearchInput(msg)

			return m, nil
		}

		switch {
		case key.Matches(msg, key.NewBinding(key.WithKeys("q"))):
			return m, tea.Quit

		case key.Matches(msg, key.NewBinding(key.WithKeys("t"))):
			m.themePicking = true
			m.previousTheme = m.currentTheme

		case key.Matches(msg, key.NewBinding(key.WithKeys("/"))):
			m.searching = true
			m.searchInput = ""

		case key.Matches(msg, key.NewBinding(key.WithKeys("n"))):
			m.viewport.SearchNext()

		case key.Matches(msg, key.NewBinding(key.WithKeys("N"))):
			m.viewport.SearchPrevious()

		case key.Matches(msg, key.NewBinding(key.WithKeys("esc"))):
			m.viewport.ClearSearch()

		case key.Matches(msg, key.NewBinding(key.WithKeys("g"))):
			m.viewport.GotoTop()

		case key.Matches(msg, key.NewBinding(key.WithKeys("G"))):
			m.viewport.GotoBottom()
		}
	}

	var cmd tea.Cmd

	m.viewport, cmd = m.viewport.Update(msg)

	return m, cmd
}

// pastedSearchText returns the part of pasted text that the search prompt
// takes: the first line, without control characters. The first line ends
// at the first carriage return or line feed, since terminals deliver
// pasted line breaks as either. The prompt shows the term on one line,
// and a line break or tab in it would make the term the viewport searches
// for differ from the term the prompt shows.
func pastedSearchText(s string) string {
	first := s
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		first = s[:i]
	}

	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}

		return r
	}, first)
}

func (m *model) updateSearchInput(msg tea.KeyPressMsg) {
	switch {
	case key.Matches(msg, key.NewBinding(key.WithKeys("enter"))):
		m.searching = false
		m.applySearch(m.searchInput)

	case key.Matches(msg, key.NewBinding(key.WithKeys("esc"))):
		m.searching = false
		m.searchInput = ""

	case key.Matches(msg, key.NewBinding(key.WithKeys("backspace"))):
		// The key decoder delivers a character built from several runes,
		// such as an emoji with a skin tone modifier, as one key press, so
		// backspace removes the whole character rather than its last rune.
		m.searchInput = cells.TrimLastCluster(m.searchInput)

	default:
		if s := msg.Text; s != "" {
			m.searchInput += s
		}
	}
}

func (m *model) updateThemeInput(msg tea.KeyPressMsg) {
	switch {
	case key.Matches(msg, key.NewBinding(key.WithKeys("enter"))):
		// Confirm selection and close.
		m.themePicking = false

	case key.Matches(msg, key.NewBinding(key.WithKeys("esc"))):
		// Revert to previous theme and close.
		m.themePicking = false
		m.applyTheme(m.previousTheme)

		// A theme outside the picker's list has no index, so the selection
		// falls back to the first entry.
		m.themeIndex = max(0, slices.Index(m.themeList, m.previousTheme))

	case key.Matches(msg, key.NewBinding(key.WithKeys("j", "down"))):
		m.moveThemeSelection(1)

	case key.Matches(msg, key.NewBinding(key.WithKeys("k", "up"))):
		m.moveThemeSelection(-1)
	}
}

func (m *model) updateThemeWheel(msg tea.MouseWheelMsg) {
	switch msg.Button {
	case tea.MouseWheelDown:
		m.moveThemeSelection(1)

	case tea.MouseWheelUp:
		m.moveThemeSelection(-1)

	default:
		// The picker lists themes in one column, so it has no use for
		// horizontal scrolling.
	}
}

// moveThemeSelection moves the picker's selection by delta entries and
// previews the theme it lands on. The selection stops at either end of
// the list.
func (m *model) moveThemeSelection(delta int) {
	i := min(max(m.themeIndex+delta, 0), len(m.themeList)-1)
	if i < 0 || i == m.themeIndex {
		return
	}

	m.themeIndex = i
	m.applyTheme(m.themeList[i])
}

// applySearch searches for term and scrolls to its first match. The viewport
// keeps its current match and scroll position when it gets the term it
// already has, so applySearch clears the term first. Submitting the active
// term again then brings its first match back into view.
func (m *model) applySearch(term string) {
	m.viewport.ClearSearch()
	m.viewport.SetSearchTerm(term)
}

// View implements [tea.Model].
//
//nolint:gocritic // hugeParam: required for tea.Model interface.
func (m model) View() tea.View {
	base := m.baseView()

	// Overlay theme picker if active.
	if m.themePicking {
		overlay := m.renderThemeOverlay()
		overlayWidth := lipgloss.Width(overlay)
		overlayHeight := lipgloss.Height(overlay)

		// Center the overlay.
		overlayX := overlayOffset(m.width, overlayWidth)
		overlayY := overlayOffset(m.height, overlayHeight)

		baseLayer := lipgloss.NewLayer(base)
		overlayLayer := lipgloss.NewLayer(overlay).X(overlayX).Y(overlayY).Z(1)

		compositor := lipgloss.NewCompositor(baseLayer, overlayLayer)
		base = compositor.Render()
	}

	v := tea.NewView(base)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion

	return v
}

// baseView renders the viewport above the status bar, in the rows the
// terminal has. The viewport and the status bar each render rows as wide
// as the terminal, so a newline between them stacks them without padding.
// A terminal of two rows leaves the viewport none. A viewport with no rows
// renders an empty string that the newline would still turn into a row,
// so baseView leaves the viewport out instead. A viewport with rows but no
// columns, as in a terminal too narrow for a side-by-side pane, also
// renders an empty string, so baseView fills its rows with blank ones and
// the status bar stays on the bottom rows. A terminal of one row shows
// the title line alone. A terminal with no rows, as before the first
// window size arrives, renders nothing.
func (m *model) baseView() string {
	switch {
	case m.height <= 0:
		return ""

	case m.height == 1:
		return m.titleLine()

	case m.height == statusBarHeight:
		return m.statusBar()

	default:
		content := m.viewport.View()
		if content == "" {
			blank := strings.Repeat(" ", max(0, m.width))
			content = strings.Join(slices.Repeat([]string{blank}, m.height-statusBarHeight), "\n")
		}

		return content + "\n" + m.statusBar()
	}
}

// overlayOffset centers an overlay of size inner in a terminal of size outer.
// An overlay larger than the terminal sits at the origin, so the terminal
// shows its top left corner rather than clipping it away.
func overlayOffset(outer, inner int) int {
	return max(0, (outer-inner)/2)
}

func (m *model) statusBar() string {
	return m.titleLine() + "\n" + m.textLine()
}

// powerlineSep renders a powerline separator with fg from the previous
// segment's background and bg from the next segment's background.
//
//nolint:gocritic // hugeParam: value semantics match lipgloss.
func powerlineSep(from, to lipgloss.Style) string {
	return lipgloss.NewStyle().
		Foreground(from.GetBackground()).
		Background(to.GetBackground()).
		Render("\ue0b0")
}

type titleSegment struct {
	text     string
	styleKey kind.Kind
}

func (m *model) titleLine() string {
	// Diff stats for OK/Error segments.
	stats := m.viewport.DiffStats()

	revisionInfo := m.revisionLabel()

	// Show the top line rather than the top row, so the position counts
	// lines as the line count beside it does.
	topLine := m.viewport.TopLine() + 1

	var titleText string

	if revisionInfo != "" {
		titleText = fmt.Sprintf(" %s [%d] ", revisionInfo, topLine)
	} else {
		titleText = fmt.Sprintf(" [%d] ", topLine)
	}

	linesText := fmt.Sprintf(" %d lines ", m.viewport.TotalLineCount())

	segments := make([]titleSegment, 0, 6)
	segments = append(segments,
		titleSegment{" nyaml ", kind.GenericHeading},
		titleSegment{fmt.Sprintf(" +%d ", stats.Added), kind.GenericHeadingOK},
		titleSegment{fmt.Sprintf(" -%d ", stats.Removed), kind.GenericHeadingError},
		titleSegment{linesText, kind.GenericHeadingWarn},
		titleSegment{titleText, kind.GenericHeadingAccent},
	)

	// Width the fixed segments take, each with a separator after it, plus
	// the trailing separator after the subtitle.
	usedWidth := 1
	for _, seg := range segments {
		usedWidth += lipgloss.Width(seg.text) + 1 // +1 for separator.
	}

	subtitleLeft := fmt.Sprintf(" %s ", m.currentTheme)

	var subtitleRight string

	switch {
	case m.viewport.SearchCount() > 0:
		subtitleRight = fmt.Sprintf("%d/%d matches ",
			m.viewport.SearchIndex()+1,
			m.viewport.SearchCount(),
		)

	default:
		subtitleRight = fmt.Sprintf("%d%% ", int(m.viewport.ScrollPercent()*100))
	}

	subtitleContent := subtitleLeft + lipgloss.PlaceHorizontal(
		max(0, m.width-usedWidth-lipgloss.Width(subtitleLeft)),
		lipgloss.Right,
		subtitleRight,
	)

	segments = append(segments, titleSegment{subtitleContent, kind.GenericHeadingSubtle})

	// Render all segments with powerline separators.
	var sb strings.Builder

	for i, seg := range segments {
		s := m.styles.Style(seg.styleKey)
		sb.WriteString(s.Inline(true).Render(seg.text))

		if i < len(segments)-1 {
			next := m.styles.Style(segments[i+1].styleKey)
			sb.WriteString(powerlineSep(s, next))
		}
	}

	// The trailing separator transitions from the last Title bg to the
	// Text bg.
	lastStyle := m.styles.Style(segments[len(segments)-1].styleKey)
	textStyle := m.styles.Style(kind.Text)
	sb.WriteString(powerlineSep(lastStyle, textStyle))

	return m.clampWidth(sb.String())
}

// clampWidth truncates a status bar row to the terminal width. A row whose
// fixed segments outgrow a narrow terminal then ends at that width. Without
// the clamp, the row would wrap onto a second row and push the rows below
// it off the screen.
func (m *model) clampWidth(row string) string {
	return lipgloss.NewStyle().MaxWidth(m.width).Render(row)
}

// revisionLabel returns the revision position for the title line, numbered
// 1-based, or an empty string when the viewport holds at most one revision.
func (m *model) revisionLabel() string {
	count := m.viewport.RevisionCount()
	if count <= 1 {
		return ""
	}

	rev := m.viewport.RevisionIndex() + 1

	switch {
	case m.viewport.ShowingDiff():
		modeIndicator := ""
		if m.viewport.DiffMode() == yamlviewport.DiffModeOrigin {
			modeIndicator = " origin"
		}

		return fmt.Sprintf("diff %d/%d%s", rev, count, modeIndicator)

	case m.viewport.DiffMode() == yamlviewport.DiffModeNone && rev > 1:
		return fmt.Sprintf("rev %d/%d none", rev, count)

	default:
		return fmt.Sprintf("rev %d/%d", rev, count)
	}
}

func (m *model) diffModeLabel() string {
	switch m.viewport.DiffMode() {
	case yamlviewport.DiffModeAdjacent:
		return "adjacent"
	case yamlviewport.DiffModeOrigin:
		return "origin"
	default:
		return "no diff"
	}
}

func (m *model) viewModeLabel() string {
	switch m.viewport.ViewMode() {
	case yamlviewport.ViewModeHunks:
		return "hunks"
	case yamlviewport.ViewModeSideBySide:
		return "side-by-side"
	default:
		return "full"
	}
}

func (m *model) textLine() string {
	textStyle := m.styles.Style(kind.Text).Inline(true)

	if m.searching {
		searchContent := m.styles.Style(kind.TextAccentDim).Inline(true).
			Render("/" + m.searchInput)

		remaining := max(0, m.width-lipgloss.Width(searchContent))

		return m.clampWidth(searchContent + textStyle.Render(strings.Repeat(" ", remaining)))
	}

	// Build search info label.
	var searchLabel string

	switch {
	case m.viewport.SearchCount() > 0:
		searchLabel = fmt.Sprintf("%d/%d",
			m.viewport.SearchIndex()+1,
			m.viewport.SearchCount(),
		)

	case m.viewport.SearchTerm() != "":
		searchLabel = "0/0"
	default:
		searchLabel = "/"
	}

	// Wrap status.
	wrapLabel := "no wrap"
	if m.viewport.WordWrap() {
		wrapLabel = "wrap"
	}

	type swatch struct {
		label    string
		styleKey kind.Kind
	}

	// A file name comes from the file system, so escape it the way the
	// error handler and the validate command render one.
	swatches := []swatch{
		{escape.Control(m.viewport.RevisionName()), kind.TextAccentDim},
		{searchLabel, kind.TextAccent},
		{m.diffModeLabel(), kind.TextOK},
		{m.viewModeLabel(), kind.TextWarn},
		{wrapLabel, kind.TextError},
		{fmt.Sprintf("%d/%d", m.viewport.VisibleLineCount(), m.viewport.TotalLineCount()), kind.TextSubtleDim},
		{fmt.Sprintf("col %d", m.viewport.XOffset()), kind.TextSubtle},
	}

	sep := m.styles.Style(kind.TextSubtleDim).Inline(true).Render(" · ")

	var sb strings.Builder

	for i, sw := range swatches {
		if i > 0 {
			sb.WriteString(sep)
		}

		sb.WriteString(m.styles.Style(sw.styleKey).Inline(true).Render(" " + sw.label + " "))
	}

	result := sb.String()

	// Right-pad with Text style to fill width.
	contentWidth := lipgloss.Width(result)
	remaining := max(0, m.width-contentWidth)

	return m.clampWidth(result + textStyle.Render(strings.Repeat(" ", remaining)))
}

func buildPrinterOpts(lineNumbers bool, styles style.Styles) []printer.Option {
	opts := []printer.Option{printer.WithStyles(styles)}

	if !lineNumbers {
		opts = append(opts, printer.WithGutter(printer.DiffGutter))
	}

	return opts
}

// darkThemeNames returns the names of every built-in dark theme.
func darkThemeNames() []string {
	dark := theme.Builtin().Mode(theme.Dark).All()

	names := make([]string, 0, len(dark))
	for _, t := range dark {
		names = append(names, t.Name)
	}

	return names
}

// themeStyles returns the styles of the named built-in theme, or the
// default styles when no theme has that name.
func themeStyles(name string) style.Styles {
	if t, ok := theme.Builtin().Get(name); ok {
		return t.Styles()
	}

	return style.Default()
}

// applyTheme switches the model to the named theme. It is the one place
// that builds a theme's styles and printer, for the first frame and for
// the theme picker alike.
func (m *model) applyTheme(name string) {
	m.currentTheme = name
	m.styles = themeStyles(name)
	p := printer.New(buildPrinterOpts(m.lineNumbers, m.styles)...)
	m.viewport.SetPrinter(p)
}

func (m *model) renderThemeOverlay() string {
	// Calculate overlay dimensions (roughly 25% of screen).
	overlayWidth := max(30, m.width/4)
	overlayHeight := max(10, m.height/4)

	// Calculate visible items (accounting for header, footer, borders).
	// Ensure odd number for perfect centering.
	visibleItems := overlayHeight - 4
	if visibleItems%2 == 0 {
		visibleItems++
		overlayHeight++
	}

	// Calculate scroll offset to keep selection centered when possible.
	maxScroll := max(0, len(m.themeList)-visibleItems)
	scrollOffset := min(maxScroll, max(0, m.themeIndex-visibleItems/2))

	// Use the current theme styles for the overlay appearance.
	baseStyle := m.styles.Style(kind.Text)
	titleStyle := m.styles.Style(kind.GenericHeading)
	dimStyle := m.styles.Style(kind.TextSubtleDim)

	// Build theme list content.
	var items []string

	for i := scrollOffset; i < len(m.themeList) && len(items) < visibleItems; i++ {
		name := m.themeList[i]
		prefix := "  "
		if i == m.themeIndex {
			prefix = "> "
		}

		// Truncate name if too long.
		maxNameLen := overlayWidth - 6
		if len(name) > maxNameLen {
			name = name[:maxNameLen-1] + "~"
		}

		items = append(items, prefix+name)
	}

	content := strings.Join(items, "\n")

	// Style the overlay using theme colors.
	overlayStyle := baseStyle.
		Width(overlayWidth).
		Height(overlayHeight).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(titleStyle.GetForeground()).
		BorderBackground(baseStyle.GetBackground()).
		Padding(0, 1)

	// Content width inside the border and padding.
	contentWidth := overlayWidth - 4

	headerStyle := titleStyle.Width(contentWidth)
	footerStyle := dimStyle.Width(contentWidth)

	header := headerStyle.Render("Select Theme")
	footer := footerStyle.Render("enter select · esc cancel")

	return overlayStyle.Render(
		lipgloss.JoinVertical(
			lipgloss.Left,
			header,
			content,
			footer,
		),
	)
}
