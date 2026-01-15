package main

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/charmtone"

	tea "charm.land/bubbletea/v2"

	"github.com/macropower/niceyaml"
	"github.com/macropower/niceyaml/bubbles/yamlviewport"
)

type modelOptions struct {
	search      string
	contents    [][]byte
	lineNumbers bool
}

//nolint:recvcheck // tea.Model requires value receivers for Init, Update, View.
type model struct {
	searchInput string
	viewport    yamlviewport.Model
	width       int
	searching   bool
}

func newModel(opts *modelOptions) model {
	// Create printer with options.
	var printerOpts []niceyaml.PrinterOption

	if !opts.lineNumbers {
		printerOpts = append(printerOpts, niceyaml.WithGutter(niceyaml.DiffGutter()))
	}

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

	m := model{
		viewport: vp,
	}

	for i, c := range opts.contents {
		m.viewport.AppendRevision(niceyaml.NewSourceFromString(string(c), niceyaml.WithName(fmt.Sprintf("v%d", i))))
	}

	// Apply initial search if provided.
	if opts.search != "" {
		m.applySearch(opts.search)
	}

	return m
}

//nolint:gocritic // hugeParam: required for tea.Model interface.
func (m model) Init() tea.Cmd {
	return nil
}

//nolint:gocritic // hugeParam: required for tea.Model interface.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.viewport.SetWidth(msg.Width)
		m.viewport.SetHeight(msg.Height - 1) // Reserve 1 line for status bar.

	case tea.KeyPressMsg:
		if m.searching {
			m.updateSearchInput(msg)
			return m, nil
		}

		switch {
		case key.Matches(msg, key.NewBinding(key.WithKeys("q", "ctrl+c"))):
			return m, tea.Quit

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

func (m *model) applySearch(term string) {
	m.viewport.SetSearchTerm(term)
}

//nolint:gocritic // hugeParam: required for tea.Model interface.
func (m model) View() tea.View {
	var b strings.Builder
	b.WriteString(m.viewport.View())
	b.WriteString("\n")
	b.WriteString(m.statusBar())

	v := tea.NewView(b.String())
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

	// Pad middle.
	padding := max(0, m.width-ansi.StringWidth(left)-ansi.StringWidth(right))

	style := lipgloss.NewStyle().
		Background(charmtone.Charcoal).
		Foreground(charmtone.Salt)

	return style.Render(left + strings.Repeat(" ", padding) + right)
}
