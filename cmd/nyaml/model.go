package main

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/exp/charmtone"
	"github.com/goccy/go-yaml/lexer"
	"github.com/goccy/go-yaml/token"

	tea "charm.land/bubbletea/v2"

	"github.com/macropower/niceyaml"
	"github.com/macropower/niceyaml/bubbles/yamlviewport"
)

type modelOptions struct {
	search      string
	contents    [][]byte
	lineNumbers bool
	wrap        bool
}

//nolint:recvcheck // tea.Model requires value receivers for Init, Update, View.
type model struct {
	printer     *niceyaml.Printer
	finder      *niceyaml.Finder
	searchInput string
	tokens      token.Tokens
	viewport    yamlviewport.Model
	width       int
	searching   bool
	wrap        bool
}

func newModel(opts *modelOptions) model {
	// Create printer with options.
	var printerOpts []niceyaml.PrinterOption

	if opts.lineNumbers {
		printerOpts = append(printerOpts, niceyaml.WithLineNumbers())
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
		printer:  printer,
		finder:   niceyaml.NewFinder(),
		wrap:     opts.wrap,
	}

	revisions := make([]token.Tokens, 0, len(opts.contents))
	for _, c := range opts.contents {
		revisions = append(revisions, lexer.Tokenize(string(c)))
	}

	m.tokens = revisions[len(revisions)-1]
	m.viewport.SetRevisions(revisions)

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

		if m.wrap {
			m.printer.SetWidth(msg.Width)
			m.viewport.SetTokens(m.tokens) // Re-render with new width.
		}

	case tea.KeyPressMsg:
		if m.searching {
			return m.handleSearchInput(msg)
		}

		switch {
		case key.Matches(msg, key.NewBinding(key.WithKeys("q", "ctrl+c"))):
			return m, tea.Quit

		case key.Matches(msg, key.NewBinding(key.WithKeys("/"))):
			m.searching = true
			m.searchInput = ""

			return m, nil

		case key.Matches(msg, key.NewBinding(key.WithKeys("n"))):
			m.viewport.SearchNext()

			return m, nil

		case key.Matches(msg, key.NewBinding(key.WithKeys("N"))):
			m.viewport.SearchPrevious()

			return m, nil

		case key.Matches(msg, key.NewBinding(key.WithKeys("esc"))):
			m.viewport.ClearSearch()

			return m, nil

		case key.Matches(msg, key.NewBinding(key.WithKeys("g"))):
			m.viewport.GotoTop()

			return m, nil

		case key.Matches(msg, key.NewBinding(key.WithKeys("G"))):
			m.viewport.GotoBottom()

			return m, nil
		}
	}

	var cmd tea.Cmd

	m.viewport, cmd = m.viewport.Update(msg)

	return m, cmd
}

//nolint:gocritic // hugeParam: required for tea.Model interface.
func (m model) handleSearchInput(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
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

	return m, nil
}

func (m *model) applySearch(term string) {
	if term == "" {
		m.viewport.ClearSearch()
		return
	}

	matches := m.finder.FindStringsInTokens(term, m.tokens)
	m.viewport.SetSearchMatches(matches)
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
	padding := max(0, m.width-len(left)-len(right))

	style := lipgloss.NewStyle().
		Background(charmtone.Charcoal).
		Foreground(charmtone.Salt)

	return style.Width(m.width).Render(left + strings.Repeat(" ", padding) + right)
}
