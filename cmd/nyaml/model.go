package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/exp/charmtone"

	tea "charm.land/bubbletea/v2"

	"github.com/macropower/niceyaml"
	"github.com/macropower/niceyaml/bubbles/yamlviewport"
	"github.com/macropower/niceyaml/style"
	"github.com/macropower/niceyaml/style/theme"
)

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
		yamlviewport.WithSearchStyle(lipgloss.NewStyle().
			Background(lipgloss.Darken(charmtone.Mustard, 0.5)).
			Foreground(charmtone.Ox),
		),
		yamlviewport.WithSelectedSearchStyle(lipgloss.NewStyle().
			Background(charmtone.Mustard).
			Foreground(charmtone.Ox),
		),
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
		m.viewport.AppendRevision(niceyaml.NewSourceFromString(
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
		m.viewport.SetHeight(msg.Height - 1) // Reserve 1 line for status bar.

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
	// Add revision indicator if multiple revisions.
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

			revisionInfo = fmt.Sprintf("[diff %d/%d%s] ", idx, count, modeIndicator)

		case m.viewport.DiffMode() == yamlviewport.DiffModeNone && idx > 0 && idx < count:
			// None mode at a diff position shows rev with "none" indicator.
			revisionInfo = fmt.Sprintf("[rev %d/%d none] ", idx+1, count)

		case m.viewport.IsAtLatestRevision():
			revisionInfo = fmt.Sprintf("[rev %d/%d] ", count, count)

		default:
			revisionInfo = fmt.Sprintf("[rev %d/%d] ", idx+1, count)
		}
	}

	left := fmt.Sprintf(" %s[%d]",
		revisionInfo,
		m.viewport.YOffset()+1,
	)

	// Right: search status or scroll percent.
	var right string

	switch {
	case m.searching:
		right = "/" + m.searchInput
	case m.viewport.SearchCount() > 0:
		right = fmt.Sprintf("%d/%d matches ",
			m.viewport.SearchIndex()+1,
			m.viewport.SearchCount(),
		)

	default:
		right = fmt.Sprintf("%d%% ", int(m.viewport.ScrollPercent()*100))
	}

	barStyle := lipgloss.NewStyle().
		Background(charmtone.Charcoal).
		Foreground(charmtone.Salt).
		Inline(true)

	padding := max(0, lipgloss.Width(left))

	right = lipgloss.PlaceHorizontal(m.width-padding, lipgloss.Right, right)

	return barStyle.Render(left + right)
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
	accentStyle := styles.Style(style.NameTag)
	dimStyle := styles.Style(style.Comment)

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
		BorderForeground(accentStyle.GetForeground()).
		BorderBackground(baseStyle.GetBackground()).
		Padding(0, 1)

	// Content width inside the border and padding.
	contentWidth := overlayWidth - 4

	headerStyle := accentStyle.Bold(true).Background(baseStyle.GetBackground()).Width(contentWidth)
	footerStyle := dimStyle.Background(baseStyle.GetBackground()).Width(contentWidth)

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
