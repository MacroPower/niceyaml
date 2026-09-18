package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"

	tea "charm.land/bubbletea/v2"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/bubbles/yamlviewport"
	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/style/theme"
)

// statusBarHeight is the number of terminal rows the status bar occupies
// below the viewport.
const statusBarHeight = 2

// fileEntry holds a file path and its contents.
type fileEntry struct {
	path    string
	content []byte
}

type modelOptions struct {
	search      string
	files       []fileEntry
	lineNumbers bool
}

type model struct {
	searchInput   string
	currentTheme  string
	previousTheme string
	themeList     []string
	// Styles of the current theme, built once per theme switch and shared by
	// the printer, the status bar, and the theme picker.
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
	// Get sorted theme list.
	themeList := darkThemeNames()
	slices.Sort(themeList)

	// Default theme.
	defaultTheme := "charm"
	styles := themeStyles(defaultTheme)

	// Create printer with options.
	printerOpts := buildPrinterOpts(opts.lineNumbers, styles)
	p := printer.New(printerOpts...)

	// Create viewport.
	vp := yamlviewport.New(
		yamlviewport.WithPrinter(p),
	)

	// Find default theme index.
	themeIndex := max(0, slices.Index(themeList, defaultTheme))

	m := model{
		viewport:     vp,
		themeList:    themeList,
		themeIndex:   themeIndex,
		currentTheme: defaultTheme,
		styles:       styles,
		lineNumbers:  opts.lineNumbers,
	}

	for _, f := range opts.files {
		m.viewport.AddRevision(niceyaml.NewSourceFromString(
			string(f.content),
			niceyaml.WithName(filepath.Base(f.path)),
		))
	}

	// Apply initial search if provided.
	if opts.search != "" {
		m.applySearch(opts.search)
	}

	return m
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
		// Reserve 2 lines for the status bar; a terminal shorter than that
		// leaves the viewport no rows rather than a negative height.
		m.viewport.SetHeight(max(0, msg.Height-statusBarHeight))

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

func (m *model) updateSearchInput(msg tea.KeyPressMsg) {
	switch {
	case key.Matches(msg, key.NewBinding(key.WithKeys("enter"))):
		m.searching = false
		m.applySearch(m.searchInput)

	case key.Matches(msg, key.NewBinding(key.WithKeys("esc"))):
		m.searching = false
		m.searchInput = ""

	case key.Matches(msg, key.NewBinding(key.WithKeys("backspace"))):
		if runes := []rune(m.searchInput); len(runes) > 0 {
			m.searchInput = string(runes[:len(runes)-1])
		}

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
		// Move selection down with live preview.
		if m.themeIndex < len(m.themeList)-1 {
			m.themeIndex++
			m.applyTheme(m.themeList[m.themeIndex])
		}

	case key.Matches(msg, key.NewBinding(key.WithKeys("k", "up"))):
		// Move selection up with live preview.
		if m.themeIndex > 0 {
			m.themeIndex--
			m.applyTheme(m.themeList[m.themeIndex])
		}
	}
}

func (m *model) applySearch(term string) {
	m.viewport.SetSearchTerm(term)
}

// View implements [tea.Model].
//
//nolint:gocritic // hugeParam: required for tea.Model interface.
func (m model) View() tea.View {
	base := lipgloss.JoinVertical(
		lipgloss.Top,
		m.viewport.View(),
		m.statusBar(),
	)

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
	styleKey style.Kind
}

func (m *model) titleLine() string {
	// Diff stats for OK/Error segments.
	added, removed := m.viewport.DiffStats()

	revisionInfo := m.revisionLabel()

	var titleText string

	if revisionInfo != "" {
		titleText = fmt.Sprintf(" %s [%d] ", revisionInfo, m.viewport.YOffset()+1)
	} else {
		titleText = fmt.Sprintf(" [%d] ", m.viewport.YOffset()+1)
	}

	linesText := fmt.Sprintf(" %d lines ", m.viewport.TotalLineCount())

	segments := make([]titleSegment, 0, 6)
	segments = append(segments,
		titleSegment{" nyaml ", style.GenericHeading},
		titleSegment{fmt.Sprintf(" +%d ", added), style.GenericHeadingOK},
		titleSegment{fmt.Sprintf(" -%d ", removed), style.GenericHeadingError},
		titleSegment{linesText, style.GenericHeadingWarn},
		titleSegment{titleText, style.GenericHeadingAccent},
	)

	// Width used by the fixed segments, each followed by a separator, plus
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

	segments = append(segments, titleSegment{subtitleContent, style.GenericHeadingSubtle})

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

	// Trailing separator: transition from last Title bg to Text bg.
	lastStyle := m.styles.Style(segments[len(segments)-1].styleKey)
	textStyle := m.styles.Style(style.Text)
	sb.WriteString(powerlineSep(lastStyle, textStyle))

	return sb.String()
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
	case m.viewport.IsShowingDiff():
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
	textStyle := m.styles.Style(style.Text).Inline(true)

	if m.searching {
		searchContent := m.styles.Style(style.TextAccentDim).Inline(true).
			Render("/" + m.searchInput)

		remaining := max(0, m.width-lipgloss.Width(searchContent))

		return searchContent + textStyle.Render(strings.Repeat(" ", remaining))
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
		styleKey style.Kind
	}

	swatches := []swatch{
		{m.viewport.RevisionName(), style.TextAccentDim},
		{searchLabel, style.TextAccent},
		{m.diffModeLabel(), style.TextOK},
		{m.viewModeLabel(), style.TextWarn},
		{wrapLabel, style.TextError},
		{fmt.Sprintf("%d/%d", m.viewport.VisibleLineCount(), m.viewport.TotalLineCount()), style.TextSubtleDim},
		{fmt.Sprintf("col %d", m.viewport.XOffset()), style.TextSubtle},
	}

	sep := m.styles.Style(style.TextSubtleDim).Inline(true).Render(" · ")

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

	return result + textStyle.Render(strings.Repeat(" ", remaining))
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
	baseStyle := m.styles.Style(style.Text)
	titleStyle := m.styles.Style(style.GenericHeading)
	dimStyle := m.styles.Style(style.TextSubtleDim)

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
