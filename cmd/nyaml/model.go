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
	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/style/theme"
)

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
	viewport      yamlviewport.Model
	width         int
	height        int
	themeIndex    int
	lineNumbers   bool
	searching     bool
	themePicking  bool
}

func newModel(opts *modelOptions) model {
	// Get sorted theme list.
	themeList := theme.List(style.Dark)
	slices.Sort(themeList)

	// Default theme.
	defaultTheme := "charm"

	// Create printer with options.
	printerOpts := buildPrinterOpts(opts.lineNumbers, defaultTheme)
	printer := niceyaml.NewPrinter(printerOpts...)

	// Create viewport.
	vp := yamlviewport.New(
		yamlviewport.WithPrinter(printer),
	)

	// Find default theme index.
	themeIndex := max(0, slices.Index(themeList, defaultTheme))

	m := model{
		viewport:     vp,
		themeList:    themeList,
		themeIndex:   themeIndex,
		currentTheme: defaultTheme,
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
		m.viewport.SetHeight(msg.Height - 2) // Reserve 2 lines for status bar.

	case tea.KeyPressMsg:
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
		case key.Matches(msg, key.NewBinding(key.WithKeys("q", "ctrl+c"))):
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
		if m.searchInput != "" {
			m.searchInput = m.searchInput[:len(m.searchInput)-1]
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

		m.themeIndex = slices.Index(m.themeList, m.previousTheme)

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
		overlayX := (m.width - overlayWidth) / 2
		overlayY := (m.height - overlayHeight) / 2

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

func (m *model) statusBar() string {
	return m.titleLine() + "\n" + m.textLine()
}

// powerlineSep renders a powerline separator with fg from the previous
// segment's background and bg from the next segment's background.
func powerlineSep(from, to *lipgloss.Style) string {
	return lipgloss.NewStyle().
		Foreground(from.GetBackground()).
		Background(to.GetBackground()).
		Render("\ue0b0")
}

type titleSegment struct {
	text     string
	styleKey style.Style
}

func (m *model) titleLine() string {
	styles, _ := theme.Styles(m.currentTheme)

	// Diff stats for OK/Error segments.
	added, removed := m.viewport.DiffStats()

	// Build revision info text.
	revisionInfo := ""
	if m.viewport.RevisionCount() > 1 {
		idx := m.viewport.RevisionIndex()
		count := m.viewport.RevisionCount()

		switch {
		case m.viewport.IsShowingDiff():
			modeIndicator := ""
			if m.viewport.DiffMode() == yamlviewport.DiffModeOrigin {
				modeIndicator = " origin"
			}

			revisionInfo = fmt.Sprintf("diff %d/%d%s", idx, count, modeIndicator)

		case m.viewport.DiffMode() == yamlviewport.DiffModeNone && idx > 0 && idx < count:
			revisionInfo = fmt.Sprintf("rev %d/%d none", idx+1, count)

		case m.viewport.IsAtLatestRevision():
			revisionInfo = fmt.Sprintf("rev %d/%d", count, count)

		default:
			revisionInfo = fmt.Sprintf("rev %d/%d", idx+1, count)
		}
	}

	var titleText string
	if revisionInfo != "" {
		titleText = fmt.Sprintf(" %s [%d] ", revisionInfo, m.viewport.YOffset()+1)
	} else {
		titleText = fmt.Sprintf(" [%d] ", m.viewport.YOffset()+1)
	}

	linesText := fmt.Sprintf(" %d lines ", m.viewport.TotalLineCount())

	segments := make([]titleSegment, 0, 6)
	segments = append(segments,
		titleSegment{" nyaml ", style.Title},
		titleSegment{fmt.Sprintf(" +%d ", added), style.TitleOK},
		titleSegment{fmt.Sprintf(" -%d ", removed), style.TitleError},
		titleSegment{linesText, style.TitleWarn},
		titleSegment{titleText, style.TitleAccent},
	)

	// Calculate width used by fixed segments (text + 1 separator each).
	usedWidth := 0
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

	segments = append(segments, titleSegment{subtitleContent, style.TitleSubtle})

	// Render all segments with powerline separators.
	var sb strings.Builder

	for i, seg := range segments {
		s := styles.Style(seg.styleKey)
		sb.WriteString(s.Inline(true).Render(seg.text))

		if i < len(segments)-1 {
			next := styles.Style(segments[i+1].styleKey)
			sb.WriteString(powerlineSep(s, next))
		}
	}

	// Trailing separator: transition from last Title bg to Text bg.
	lastStyle := styles.Style(segments[len(segments)-1].styleKey)
	textStyle := styles.Style(style.Text)
	sb.WriteString(powerlineSep(lastStyle, textStyle))

	return sb.String()
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
	styles, _ := theme.Styles(m.currentTheme)
	textStyle := styles.Style(style.Text).Inline(true)

	if m.searching {
		searchContent := styles.Style(style.TextAccentDim).Inline(true).
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
	if m.viewport.WrapEnabled {
		wrapLabel = "wrap"
	}

	type swatch struct {
		label    string
		styleKey style.Style
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

	sep := styles.Style(style.TextSubtleDim).Inline(true).Render(" · ")

	var sb strings.Builder

	for i, sw := range swatches {
		if i > 0 {
			sb.WriteString(sep)
		}

		sb.WriteString(styles.Style(sw.styleKey).Inline(true).Render(" " + sw.label + " "))
	}

	result := sb.String()

	// Right-pad with Text style to fill width.
	contentWidth := lipgloss.Width(result)
	remaining := max(0, m.width-contentWidth)

	return result + textStyle.Render(strings.Repeat(" ", remaining))
}

func buildPrinterOpts(lineNumbers bool, themeName string) []niceyaml.PrinterOption {
	var opts []niceyaml.PrinterOption

	if !lineNumbers {
		opts = append(opts, niceyaml.WithGutter(niceyaml.DiffGutter()))
	}

	if styles, ok := theme.Styles(themeName); ok {
		opts = append(opts, niceyaml.WithStyles(styles))
	}

	return opts
}

func (m *model) applyTheme(name string) {
	m.currentTheme = name
	printer := niceyaml.NewPrinter(buildPrinterOpts(m.lineNumbers, name)...)
	m.viewport.SetPrinter(printer)
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

	// Get current theme styles for the overlay appearance.
	styles, _ := theme.Styles(m.currentTheme)
	baseStyle := styles.Style(style.Text)
	titleStyle := styles.Style(style.Title)
	dimStyle := styles.Style(style.TextSubtleDim)

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
