package yamlviewport_test

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/exp/golden"
	"github.com/goccy/go-yaml/lexer"
	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	tea "charm.land/bubbletea/v2"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/bubbles/yamlviewport"
	"go.jacobcolvin.com/niceyaml/finder"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/style/theme"
)

// testPrinter returns a printer without styles or line numbers for predictable golden output.
func testPrinter() *printer.Printer {
	return printer.New(
		printer.WithStyles(style.Styles{}),
		printer.WithContainerStyle(lipgloss.NewStyle()),
		printer.WithGutter(printer.DiffGutter),
	)
}

// testPrinterWithLineNumbers returns a printer with line numbers (DefaultGutter).
func testPrinterWithLineNumbers() *printer.Printer {
	return printer.New(
		printer.WithStyles(style.Styles{}),
		printer.WithContainerStyle(lipgloss.NewStyle()),
	)
}

// testPrinterWithColors returns a printer with default syntax highlighting.
func testPrinterWithColors() *printer.Printer {
	return printer.New(
		printer.WithStyles(theme.Charm()),
		printer.WithContainerStyle(lipgloss.NewStyle()),
	)
}

// testPrinterWithSearch returns a printer with XML-style search highlights for testing.
func testPrinterWithSearch() *printer.Printer {
	return printer.New(
		printer.WithStyles(yamltest.NewXMLStyles(
			yamltest.XMLStyleInclude(style.GenericHighlightDim, style.GenericHighlight),
		)),
		printer.WithContainerStyle(lipgloss.NewStyle()),
		printer.WithGutter(printer.DiffGutter),
	)
}

func TestViewport_Golden(t *testing.T) {
	t.Parallel()

	type goldenTest struct {
		setupFunc func(m *yamlviewport.Model, tokens token.Tokens)
		yaml      string
		opts      []yamlviewport.Option
		width     int
		height    int
	}

	fullYAML, err := os.ReadFile("testdata/full.yaml")
	require.NoError(t, err)

	simpleYAML := stringtest.Input(`
		key: value
		number: 42
		bool: true
		list:
		  - item1
		  - item2
		nested:
		  child: data
	`)

	diffBeforeYAML := stringtest.Input(`
		name: original
		count: 10
		enabled: true
	`)

	diffAfterYAML := stringtest.Input(`
		name: modified
		count: 20
		enabled: true
		new_field: added
	`)

	wideYAML := stringtest.Input(`
		short: x
		very_long_key_name_that_requires_horizontal_scrolling: "This is a very long value that extends well beyond the viewport width and requires scrolling to see"
		another: y
	`)

	wrapYAML := stringtest.Input(`
		line1: a
		line2: b
		line3: c
		line4: d
		line5: "a value long enough to wrap onto several rows of a narrow viewport"
		line6: f
		line7: g
		line8: h
		line9: i
		line10: j
	`)

	tcs := map[string]goldenTest{
		"WrapScrolledToBottom": {
			// Line 5 wraps to several rows. Scrolling by row reaches the
			// last line, which line-based scrolling pushed off the bottom.
			opts:   []yamlviewport.Option{yamlviewport.WithPrinter(testPrinter())},
			yaml:   wrapYAML,
			width:  30,
			height: 5,
			setupFunc: func(m *yamlviewport.Model, _ token.Tokens) {
				m.GotoBottom()
			},
		},
		"WrapScrolledIntoLine": {
			// An offset inside the wrapped line starts the view on one of its
			// continuation rows.
			opts:   []yamlviewport.Option{yamlviewport.WithPrinter(testPrinter())},
			yaml:   wrapYAML,
			width:  30,
			height: 5,
			setupFunc: func(m *yamlviewport.Model, _ token.Tokens) {
				m.SetYOffset(6)
			},
		},
		"BasicView": {
			opts:   []yamlviewport.Option{yamlviewport.WithPrinter(testPrinter())},
			yaml:   simpleYAML,
			width:  80,
			height: 24,
		},
		"WithLineNumbers": {
			opts:   []yamlviewport.Option{yamlviewport.WithPrinter(testPrinterWithLineNumbers())},
			yaml:   simpleYAML,
			width:  80,
			height: 24,
		},
		"DefaultColors": {
			opts:   []yamlviewport.Option{yamlviewport.WithPrinter(testPrinterWithColors())},
			yaml:   simpleYAML,
			width:  80,
			height: 24,
		},
		"FullYAML": {
			opts:   []yamlviewport.Option{yamlviewport.WithPrinter(testPrinter())},
			yaml:   string(fullYAML),
			width:  80,
			height: 24,
		},
		"SearchHighlight": {
			opts: []yamlviewport.Option{
				yamlviewport.WithPrinter(testPrinterWithSearch()),
			},
			yaml:   simpleYAML,
			width:  80,
			height: 24,
			setupFunc: func(m *yamlviewport.Model, _ token.Tokens) {
				m.SetSearchTerm("item")
			},
		},
		"DiffMode": {
			opts: []yamlviewport.Option{yamlviewport.WithPrinter(testPrinter())},
			yaml: diffAfterYAML,
			setupFunc: func(m *yamlviewport.Model, _ token.Tokens) {
				m.ClearRevisions()
				m.AddRevision(niceyaml.NewSourceFromString(diffBeforeYAML, niceyaml.WithName("v1")))
				m.AddRevision(niceyaml.NewSourceFromString(diffAfterYAML, niceyaml.WithName("v2")))
				m.GoToRevision(1) // Show diff between revision 0 and 1.
			},
			width:  80,
			height: 24,
		},
		"ScrolledContent": {
			opts:   []yamlviewport.Option{yamlviewport.WithPrinter(testPrinter())},
			yaml:   string(fullYAML),
			width:  80,
			height: 10,
			setupFunc: func(m *yamlviewport.Model, _ token.Tokens) {
				m.SetYOffset(15) // Scroll down to middle of content.
			},
		},
		"HorizontalScroll": {
			opts:   []yamlviewport.Option{yamlviewport.WithPrinter(testPrinter())},
			yaml:   wideYAML,
			width:  40,
			height: 10,
			setupFunc: func(m *yamlviewport.Model, _ token.Tokens) {
				m.ToggleWordWrap() // Disable wrap (default is true).
				m.SetXOffset(20)   // Scroll right.
			},
		},
		"HorizontalScrollNoOffset": {
			opts:   []yamlviewport.Option{yamlviewport.WithPrinter(testPrinter())},
			yaml:   wideYAML,
			width:  40,
			height: 10,
			setupFunc: func(m *yamlviewport.Model, _ token.Tokens) {
				m.ToggleWordWrap() // Disable wrap (default is true).

				// XOffset stays at 0 - verifies lines are truncated, not wrapped.
			},
		},
		"FillHeight": {
			opts:   []yamlviewport.Option{yamlviewport.WithPrinter(testPrinter())},
			yaml:   "key: value",
			width:  80,
			height: 10,
			setupFunc: func(m *yamlviewport.Model, _ token.Tokens) {
				m.FillHeight = true
			},
		},
		"EmptyContent": {
			opts:   []yamlviewport.Option{yamlviewport.WithPrinter(testPrinter())},
			yaml:   "",
			width:  80,
			height: 24,
		},
		"StyledContainer": {
			opts: []yamlviewport.Option{
				yamlviewport.WithPrinter(testPrinter()),
				yamlviewport.WithStyle(lipgloss.NewStyle().
					Border(lipgloss.NormalBorder()).
					Padding(1)),
			},
			yaml:   simpleYAML,
			width:  80,
			height: 24,
		},
		"SmallViewport": {
			opts:   []yamlviewport.Option{yamlviewport.WithPrinter(testPrinter())},
			yaml:   simpleYAML,
			width:  20,
			height: 5,
		},
		"SearchNavigateNext": {
			opts: []yamlviewport.Option{
				yamlviewport.WithPrinter(testPrinterWithSearch()),
			},
			yaml:   simpleYAML,
			width:  80,
			height: 24,
			setupFunc: func(m *yamlviewport.Model, _ token.Tokens) {
				m.SetSearchTerm("item")
				m.SearchNext() // Move to second match.
			},
		},
		"SearchNoMatches": {
			opts: []yamlviewport.Option{
				yamlviewport.WithPrinter(testPrinterWithSearch()),
			},
			yaml:   simpleYAML,
			width:  80,
			height: 24,
			setupFunc: func(m *yamlviewport.Model, _ token.Tokens) {
				m.SetSearchTerm("nonexistent")
			},
		},
		"SearchNavigatePrevious": {
			opts: []yamlviewport.Option{
				yamlviewport.WithPrinter(testPrinterWithSearch()),
			},
			yaml:   simpleYAML,
			width:  80,
			height: 24,
			setupFunc: func(m *yamlviewport.Model, _ token.Tokens) {
				m.SetSearchTerm("item")
				m.SearchPrevious() // Wrap from first to last match.
			},
		},
		"SearchWrapAroundNext": {
			opts: []yamlviewport.Option{
				yamlviewport.WithPrinter(testPrinterWithSearch()),
			},
			yaml:   simpleYAML,
			width:  80,
			height: 24,
			setupFunc: func(m *yamlviewport.Model, _ token.Tokens) {
				m.SetSearchTerm("item")
				m.SearchNext() // Second match.
				m.SearchNext() // Wrap to first match.
			},
		},
		"SearchWrapAroundPrevious": {
			opts: []yamlviewport.Option{
				yamlviewport.WithPrinter(testPrinterWithSearch()),
			},
			yaml:   simpleYAML,
			width:  80,
			height: 24,
			setupFunc: func(m *yamlviewport.Model, _ token.Tokens) {
				m.SetSearchTerm("item")
				m.SearchPrevious() // Wrap to last match.
				m.SearchPrevious() // Move to first match.
			},
		},
		"SearchMultipleMatchesSameLine": {
			opts: []yamlviewport.Option{
				yamlviewport.WithPrinter(testPrinterWithSearch()),
			},
			yaml: stringtest.Input(`
				item: item_value
				another: data
			`),
			width:  80,
			height: 24,
			setupFunc: func(m *yamlviewport.Model, _ token.Tokens) {
				m.SetSearchTerm("item") // Matches twice on the same line.
			},
		},
		"SearchClear": {
			opts: []yamlviewport.Option{
				yamlviewport.WithPrinter(testPrinterWithSearch()),
			},
			yaml:   simpleYAML,
			width:  80,
			height: 24,
			setupFunc: func(m *yamlviewport.Model, _ token.Tokens) {
				m.SetSearchTerm("item")
				m.ClearSearch() // Clear search - no highlights.
			},
		},
		"SearchScrollsToFirstMatch": {
			// With a small viewport, setting a search term scrolls to the first match.
			// "timeout" first appears on line 36 in full.yaml.
			opts: []yamlviewport.Option{
				yamlviewport.WithPrinter(testPrinterWithSearch()),
			},
			yaml:   string(fullYAML),
			width:  80,
			height: 10,
			setupFunc: func(m *yamlviewport.Model, _ token.Tokens) {
				m.SetSearchTerm("timeout") // First match is on line 36.
			},
		},
		"SearchScrollsToNextMatch": {
			// Navigating to the next match scrolls the viewport.
			// "timeout" appears on lines 36 and 41 in full.yaml.
			opts: []yamlviewport.Option{
				yamlviewport.WithPrinter(testPrinterWithSearch()),
			},
			yaml:   string(fullYAML),
			width:  80,
			height: 10,
			setupFunc: func(m *yamlviewport.Model, _ token.Tokens) {
				m.SetSearchTerm("timeout")
				m.SearchNext() // Navigate to second match on line 41.
			},
		},
		"SearchScrollsToPreviousMatch": {
			// Navigating backwards scrolls to the previous match.
			// Start at the second "timeout" match, go back to the first.
			opts: []yamlviewport.Option{
				yamlviewport.WithPrinter(testPrinterWithSearch()),
			},
			yaml:   string(fullYAML),
			width:  80,
			height: 10,
			setupFunc: func(m *yamlviewport.Model, _ token.Tokens) {
				m.SetSearchTerm("timeout")
				m.SearchNext()     // Move to second match.
				m.SearchPrevious() // Move back to first match.
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tks := lexer.Tokenize(tc.yaml)

			m := yamlviewport.New(tc.opts...)
			m.SetWidth(tc.width)
			m.SetHeight(tc.height)
			m.SetSource(niceyaml.NewSourceFromTokens(tks))

			if tc.setupFunc != nil {
				tc.setupFunc(&m, tks)
			}

			output := m.View()
			golden.RequireEqual(t, output)
		})
	}
}

func TestViewport_Scrolling(t *testing.T) {
	t.Parallel()

	verticalYAML := stringtest.Input(`
		line1: a
		line2: b
		line3: c
		line4: d
		line5: e
		line6: f
		line7: g
		line8: h
		line9: i
		line10: j
	`)

	horizontalYAML := stringtest.Input(`
		short: x
		very_long_line: "This is a very long value that extends beyond the viewport width"
		another: y
	`)

	tcs := map[string]struct {
		setup  func(m *yamlviewport.Model)
		test   func(t *testing.T, m *yamlviewport.Model)
		yaml   string
		width  int
		height int
	}{
		"Vertical/ScrollDown": {
			yaml:   verticalYAML,
			width:  80,
			height: 5,
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, 0, m.YOffset())
				assert.True(t, m.AtTop())
				assert.False(t, m.AtBottom())

				m.ScrollDown(2)
				assert.Equal(t, 2, m.YOffset())
				assert.False(t, m.AtTop())
			},
		},
		"Vertical/ScrollUp": {
			yaml:   verticalYAML,
			width:  80,
			height: 5,
			setup: func(m *yamlviewport.Model) {
				m.SetYOffset(5)
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				m.ScrollUp(2)
				assert.Equal(t, 3, m.YOffset())
			},
		},
		"Vertical/PageDown": {
			yaml:   verticalYAML,
			width:  80,
			height: 5,
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				m.PageDown()
				assert.Equal(t, 5, m.YOffset())
			},
		},
		"Vertical/PageUp": {
			yaml:   verticalYAML,
			width:  80,
			height: 5,
			setup: func(m *yamlviewport.Model) {
				m.GotoBottom()
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				m.PageUp()
				assert.Equal(t, 0, m.YOffset())
			},
		},
		"Vertical/HalfPageDown": {
			yaml:   verticalYAML,
			width:  80,
			height: 6,
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				m.HalfPageDown()
				assert.Equal(t, 3, m.YOffset())
			},
		},
		"Vertical/HalfPageUp": {
			yaml:   verticalYAML,
			width:  80,
			height: 6,
			setup: func(m *yamlviewport.Model) {
				m.GotoBottom()
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				m.HalfPageUp()
				// From offset 4 (bottom with height 6, 10 lines), half page up is 3 lines.
				assert.Equal(t, 1, m.YOffset())
			},
		},
		"Vertical/GotoTopBottom": {
			yaml:   verticalYAML,
			width:  80,
			height: 5,
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				m.GotoBottom()
				assert.True(t, m.AtBottom())

				m.GotoTop()
				assert.True(t, m.AtTop())
				assert.Equal(t, 0, m.YOffset())
			},
		},
		"Vertical/ScrollPercent": {
			yaml:   verticalYAML,
			width:  80,
			height: 5,
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.InDelta(t, 0.0, m.ScrollPercent(), 0.01)

				m.GotoBottom()
				assert.InDelta(t, 1.0, m.ScrollPercent(), 0.01)
			},
		},
		"Horizontal/ScrollRight": {
			yaml:   horizontalYAML,
			width:  40,
			height: 10,
			setup: func(m *yamlviewport.Model) {
				m.ToggleWordWrap() // Disable wrap.
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, 0, m.XOffset())

				m.ScrollRight(10)
				assert.Equal(t, 10, m.XOffset())
			},
		},
		"Horizontal/ScrollLeft": {
			yaml:   horizontalYAML,
			width:  40,
			height: 10,
			setup: func(m *yamlviewport.Model) {
				m.ToggleWordWrap() // Disable wrap.
				m.SetXOffset(20)
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				m.ScrollLeft(5)
				assert.Equal(t, 15, m.XOffset())
			},
		},
		"Horizontal/SetHorizontalStep": {
			yaml:   horizontalYAML,
			width:  40,
			height: 10,
			setup: func(m *yamlviewport.Model) {
				m.SetHorizontalStep(10)
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				// Initial offset is 0.
				assert.Equal(t, 0, m.XOffset())
			},
		},
		"Horizontal/ScrollPercent": {
			yaml:   horizontalYAML,
			width:  40,
			height: 10,
			setup: func(m *yamlviewport.Model) {
				m.ToggleWordWrap() // Disable wrap.
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.InDelta(t, 0.0, m.HorizontalScrollPercent(), 0.01)
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tks := lexer.Tokenize(tc.yaml)

			m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
			m.SetWidth(tc.width)
			m.SetHeight(tc.height)
			m.SetSource(niceyaml.NewSourceFromTokens(tks))

			if tc.setup != nil {
				tc.setup(&m)
			}

			tc.test(t, &m)
		})
	}
}

func TestViewport_RowScrolling(t *testing.T) {
	t.Parallel()

	wrapYAML := stringtest.Input(`
		line1: a
		line2: b
		line3: c
		line4: d
		line5: "a value long enough to wrap onto several rows of a narrow viewport"
		line6: f
		line7: g
		line8: h
		line9: i
		line10: j
	`)

	newModel := func(t *testing.T) *yamlviewport.Model {
		t.Helper()

		m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
		m.SetWidth(30)
		m.SetHeight(5)
		m.SetSource(niceyaml.NewSourceFromString(wrapYAML))

		return &m
	}

	firstRow := func(view string) string {
		row, _, _ := strings.Cut(view, "\n")

		return row
	}

	lastRow := func(view string) string {
		rows := strings.Split(view, "\n")

		return rows[len(rows)-1]
	}

	t.Run("counts rows and lines separately", func(t *testing.T) {
		t.Parallel()

		m := newModel(t)

		assert.Equal(t, 10, m.TotalLineCount())
		assert.Greater(t, m.TotalRowCount(), 10)
		assert.Equal(t, 5, m.VisibleRowCount())
		assert.Equal(t, 5, m.VisibleLineCount())

		// Wrapping off, every line is one row again.
		m.SetWordWrap(false)
		assert.Equal(t, 10, m.TotalRowCount())
	})

	t.Run("bottom shows the last line", func(t *testing.T) {
		t.Parallel()

		m := newModel(t)
		m.GotoBottom()

		assert.Equal(t, m.TotalRowCount()-5, m.YOffset())
		assert.True(t, m.AtBottom())
		assert.False(t, m.PastBottom())
		assert.InDelta(t, 1.0, m.ScrollPercent(), 0.01)
		assert.Contains(t, lastRow(m.View()), "line10")

		// One row up still ends inside the document, not past it.
		m.ScrollUp(1)
		assert.False(t, m.AtBottom())
		assert.Contains(t, lastRow(m.View()), "line9")
	})

	t.Run("scrolls through a wrapped line one row at a time", func(t *testing.T) {
		t.Parallel()

		m := newModel(t)
		m.SetYOffset(4) // Line 5 starts at row 4.

		assert.Contains(t, firstRow(m.View()), "line5")

		m.ScrollDown(1)
		assert.Equal(t, 5, m.YOffset())

		top := firstRow(m.View())
		assert.NotContains(t, top, "line5")
		assert.NotContains(t, top, "line6")
		assert.Equal(t, 5, m.VisibleRowCount())
	})

	t.Run("mouse wheel scrolls by rows", func(t *testing.T) {
		t.Parallel()

		m := newModel(t)
		m.SetYOffset(4)

		updated, _ := m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
		assert.Equal(t, 4+updated.MouseWheelDelta, updated.YOffset())
	})

	t.Run("resize keeps the offset inside the rows", func(t *testing.T) {
		t.Parallel()

		m := newModel(t)
		m.GotoBottom()

		// A wider viewport wraps less, so the maximum offset drops and the
		// offset follows it.
		m.SetWidth(200)
		assert.Equal(t, 10, m.TotalRowCount())
		assert.Equal(t, 5, m.YOffset())
		assert.False(t, m.PastBottom())
	})
}

func TestViewport_LayoutChangesKeepTopLine(t *testing.T) {
	t.Parallel()

	// The first 50 lines wrap to two rows at width 30 and fit one row at
	// width 60, so line k100 starts at row 150 when they wrap and at row 100
	// when they do not.
	var src strings.Builder

	for i := range 200 {
		value := "v"
		if i < 50 {
			value = strings.TrimSpace(strings.Repeat("word ", 8))
		}

		fmt.Fprintf(&src, "k%d: %s\n", i, value)
	}

	tcs := map[string]struct {
		change     func(m *yamlviewport.Model)
		wantTop    string
		offset     int
		wantOffset int
	}{
		"wrap off": {
			offset:     150,
			change:     func(m *yamlviewport.Model) { m.SetWordWrap(false) },
			wantOffset: 100,
			wantTop:    "k100:",
		},
		"wider width": {
			offset:     150,
			change:     func(m *yamlviewport.Model) { m.SetWidth(60) },
			wantOffset: 100,
			wantTop:    "k100:",
		},
		"several changes before a read": {
			offset: 150,
			change: func(m *yamlviewport.Model) {
				m.SetWordWrap(false)
				m.SetWidth(60)
				m.SetWordWrap(true)
			},
			wantOffset: 100,
			wantTop:    "k100:",
		},
		"view renders first": {
			offset: 150,
			change: func(m *yamlviewport.Model) {
				m.SetWordWrap(false)

				// View has a value receiver, so the copy it runs on fills the
				// row counts that m shares.
				_ = m.View()
			},
			wantOffset: 100,
			wantTop:    "k100:",
		},
		"second row of a wrapped line with the same layout": {
			offset:     21,
			change:     func(m *yamlviewport.Model) { m.SetPrinter(testPrinter()) },
			wantOffset: 21,
		},
		"second row of a line that stops wrapping": {
			offset:     21,
			change:     func(m *yamlviewport.Model) { m.SetWordWrap(false) },
			wantOffset: 10,
			wantTop:    "k10:",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
			m.SetWidth(30)
			m.SetHeight(5)
			m.SetSource(niceyaml.NewSourceFromString(src.String()))
			require.Equal(t, 250, m.TotalRowCount())

			m.SetYOffset(tc.offset)
			tc.change(&m)

			assert.Equal(t, tc.wantOffset, m.YOffset())

			if tc.wantTop != "" {
				top, _, _ := strings.Cut(m.View(), "\n")
				assert.Contains(t, top, tc.wantTop)
			}
		})
	}
}

func TestViewport_SearchBeforeSize(t *testing.T) {
	t.Parallel()

	// A program sets the search term before its first window size message.
	// The match stays on screen once the lines wrap to that size.
	var src strings.Builder

	for i := range 60 {
		fmt.Fprintf(&src, "k%d: %s\n", i, strings.TrimSpace(strings.Repeat("word ", 8)))
	}

	src.WriteString("needle: here\n")

	m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
	m.SetSource(niceyaml.NewSourceFromString(src.String()))
	m.SetSearchTerm("needle")

	m.SetWidth(30)
	m.SetHeight(10)

	assert.Contains(t, m.View(), "needle: here")
}

func TestViewport_ContainerFrame(t *testing.T) {
	t.Parallel()

	var src strings.Builder

	for i := 1; i <= 10; i++ {
		fmt.Fprintf(&src, "line%d: v\n", i)
	}

	// The frame of the printer's container style adds rows above the first
	// line and below the last. Each offset shows the frame and line rows it
	// covers, so the frame stays visible and every line stays reachable.
	tcs := map[string]struct {
		container lipgloss.Style
		edge      string
		top       int
		bottom    int
		mode      yamlviewport.ViewMode
	}{
		"vertical padding": {
			container: lipgloss.NewStyle().Padding(1, 0),
			top:       1,
			bottom:    1,
		},
		"top padding": {
			container: lipgloss.NewStyle().PaddingTop(2),
			top:       2,
		},
		"border": {
			container: lipgloss.NewStyle().Border(lipgloss.NormalBorder()),
			edge:      "─",
			top:       1,
			bottom:    1,
		},
		"border side by side": {
			container: lipgloss.NewStyle().Border(lipgloss.NormalBorder()),
			edge:      "─",
			top:       1,
			bottom:    1,
			mode:      yamlviewport.ViewModeSideBySide,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			const (
				height = 5
				lines  = 10
			)

			p := testPrinter().With(printer.WithContainerStyle(tc.container))
			m := yamlviewport.New(yamlviewport.WithPrinter(p))
			m.SetWidth(40)
			m.SetHeight(height)
			m.SetViewMode(tc.mode)
			m.SetSource(niceyaml.NewSourceFromString(src.String()))

			total := tc.top + lines + tc.bottom
			require.Equal(t, total, m.TotalRowCount())

			m.GotoBottom()
			require.Equal(t, total-height, m.YOffset())

			for offset := range total - height + 1 {
				m.SetYOffset(offset)

				rows := strings.Split(m.View(), "\n")
				require.Len(t, rows, height)

				for i, row := range rows {
					r := offset + i
					if r < tc.top || r >= tc.top+lines {
						assert.NotContains(t, row, "line", "offset %d, row %d", offset, i)
						assert.Contains(t, row, tc.edge, "offset %d, row %d", offset, i)

						continue
					}

					assert.Contains(t, row, fmt.Sprintf("line%d:", r-tc.top+1), "offset %d, row %d", offset, i)
				}
			}
		})
	}
}

func TestViewport_Search(t *testing.T) {
	t.Parallel()

	yaml := stringtest.Input(`
		item1: first
		item2: second
		other: third
		item3: fourth
	`)

	tks := lexer.Tokenize(yaml)
	lines := niceyaml.NewSourceFromTokens(tks)

	tcs := map[string]struct {
		test       func(t *testing.T, m *yamlviewport.Model)
		searchTerm string
	}{
		"SetSearchTerm": {
			searchTerm: "item",
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, 3, m.SearchCount())
				assert.Equal(t, 0, m.SearchIndex())
				assert.Equal(t, "item", m.SearchTerm())
			},
		},
		"SearchNext": {
			searchTerm: "item",
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				m.SearchNext()
				assert.Equal(t, 1, m.SearchIndex())

				m.SearchNext()
				assert.Equal(t, 2, m.SearchIndex())

				// Wraps around.
				m.SearchNext()
				assert.Equal(t, 0, m.SearchIndex())
			},
		},
		"SearchPrevious": {
			searchTerm: "item",
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				// Wraps around from 0 to last.
				m.SearchPrevious()
				assert.Equal(t, 2, m.SearchIndex())

				m.SearchPrevious()
				assert.Equal(t, 1, m.SearchIndex())
			},
		},
		"ClearSearch": {
			searchTerm: "item",
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				m.ClearSearch()
				assert.Equal(t, 0, m.SearchCount())
				assert.Equal(t, -1, m.SearchIndex())
				assert.Empty(t, m.SearchTerm())
			},
		},
		"NoMatches": {
			searchTerm: "nonexistent",
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, 0, m.SearchCount())
				assert.Equal(t, -1, m.SearchIndex())

				// SearchNext/Previous should be no-ops.
				m.SearchNext()
				assert.Equal(t, -1, m.SearchIndex())
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
			m.SetWidth(80)
			m.SetHeight(24)
			m.SetSource(lines)
			m.SetSearchTerm(tc.searchTerm)

			tc.test(t, &m)
		})
	}
}

func TestViewport_Revisions(t *testing.T) {
	t.Parallel()

	rev1 := stringtest.Input(`
		name: original
		value: 10
	`)

	rev2 := stringtest.Input(`
		name: modified
		value: 20
	`)

	rev3 := stringtest.Input(`
		name: final
		value: 30
		new: added
	`)

	rev1Tokens := lexer.Tokenize(rev1)
	rev2Tokens := lexer.Tokenize(rev2)
	rev3Tokens := lexer.Tokenize(rev3)

	tcs := map[string]struct {
		setup func(m *yamlviewport.Model)
		test  func(t *testing.T, m *yamlviewport.Model)
	}{
		"ClearRevisions/Empty": {
			setup: func(m *yamlviewport.Model) {
				m.ClearRevisions()
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, 0, m.RevisionCount())
				assert.Equal(t, 0, m.RevisionIndex())
				assert.False(t, m.IsShowingDiff())
				assert.Empty(t, m.RevisionName())
			},
		},
		"AddRevision/Single": {
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, 1, m.RevisionCount())
				assert.Equal(t, 0, m.RevisionIndex()) // At the only revision.
				assert.True(t, m.IsAtLatestRevision())
				assert.False(t, m.IsShowingDiff()) // Only one revision, no diff possible.
				assert.Equal(t, "rev1", m.RevisionName())
			},
		},
		"AddRevision/Multiple": {
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev3Tokens, niceyaml.WithName("rev3")))
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, 3, m.RevisionCount())
				assert.Equal(t, 2, m.RevisionIndex()) // At latest (0-indexed).
				assert.True(t, m.IsAtLatestRevision())
				assert.True(t, m.IsShowingDiff()) // At index > 0 with default diffMode.
				assert.Equal(t, "rev3", m.RevisionName())
			},
		},
		"AddRevision": {
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")))
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, 2, m.RevisionCount())
				assert.Equal(t, 1, m.RevisionIndex()) // At latest (0-indexed).
				assert.Equal(t, "rev2", m.RevisionName())
			},
		},
		"ClearRevisions": {
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")))
				m.ClearRevisions()
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, 0, m.RevisionCount())
				assert.Equal(t, 0, m.RevisionIndex())
				assert.Empty(t, m.RevisionName())
			},
		},
		"GoToRevision/First": {
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev3Tokens, niceyaml.WithName("rev3")))
				m.GoToRevision(0)
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, 0, m.RevisionIndex())
				assert.True(t, m.IsAtFirstRevision())
				assert.False(t, m.IsShowingDiff()) // Position 0 shows plain view.
				assert.Equal(t, "rev1", m.RevisionName())
			},
		},
		"GoToRevision/Middle": {
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev3Tokens, niceyaml.WithName("rev3")))
				m.GoToRevision(1)
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, 1, m.RevisionIndex())
				assert.True(t, m.IsShowingDiff()) // Position 1 shows diff.
				assert.Equal(t, "rev2", m.RevisionName())
			},
		},
		"GoToRevision/Clamped": {
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")))
				m.GoToRevision(100) // Should clamp to max (N-1).
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, 1, m.RevisionIndex()) // Clamped to last index (0-indexed).
				assert.True(t, m.IsAtLatestRevision())
				assert.Equal(t, "rev2", m.RevisionName())
			},
		},
		"NextRevision": {
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev3Tokens, niceyaml.WithName("rev3")))
				m.GoToRevision(0)
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, "rev1", m.RevisionName())
				m.NextRevision()
				assert.Equal(t, 1, m.RevisionIndex())
				assert.True(t, m.IsShowingDiff())
				assert.Equal(t, "rev2", m.RevisionName())
			},
		},
		"NextRevision/AtLatest": {
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")))

				// Already at latest (index 1).
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				m.NextRevision()
				assert.Equal(t, 1, m.RevisionIndex()) // Should not change.
				assert.Equal(t, "rev2", m.RevisionName())
			},
		},
		"PrevRevision": {
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev3Tokens, niceyaml.WithName("rev3")))
				m.GoToRevision(2)
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, "rev3", m.RevisionName())
				m.PrevRevision()
				assert.Equal(t, 1, m.RevisionIndex())
				assert.Equal(t, "rev2", m.RevisionName())
			},
		},
		"PrevRevision/AtFirst": {
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")))
				m.GoToRevision(0)
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				m.PrevRevision()
				assert.Equal(t, 0, m.RevisionIndex()) // Should not change.
				assert.Equal(t, "rev1", m.RevisionName())
			},
		},
		"IsShowingDiff/BoundaryConditions": {
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev3Tokens, niceyaml.WithName("rev3")))
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				// At index 2 (latest), showing diff with default mode.
				assert.True(t, m.IsShowingDiff())
				assert.Equal(t, "rev3", m.RevisionName())

				m.GoToRevision(0)
				assert.False(t, m.IsShowingDiff()) // First revision, no diff.
				assert.Equal(t, "rev1", m.RevisionName())

				m.GoToRevision(1)
				assert.True(t, m.IsShowingDiff()) // Between 0 and 1.
				assert.Equal(t, "rev2", m.RevisionName())

				m.GoToRevision(2)
				assert.True(t, m.IsShowingDiff()) // Between 1 and 2.
				assert.Equal(t, "rev3", m.RevisionName())
			},
		},
		"SetTokensReplacesSingleRevision": {
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")))
				m.SetSource(niceyaml.NewSourceFromTokens(rev3Tokens)) // SetSource uses the Source's name.
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, 1, m.RevisionCount())
				assert.Equal(t, 0, m.RevisionIndex()) // Only one revision at index 0.
				assert.Empty(t, m.RevisionName())     // NewSourceFromTokens without name uses empty.
			},
		},
		"RevisionNames/Empty": {
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Empty(t, m.RevisionNames())
			},
		},
		"RevisionNames/Single": {
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, []string{"rev1"}, m.RevisionNames())
			},
		},
		"RevisionNames/Multiple": {
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev3Tokens, niceyaml.WithName("rev3")))
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, []string{"rev1", "rev2", "rev3"}, m.RevisionNames())
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
			m.SetWidth(80)
			m.SetHeight(24)

			if tc.setup != nil {
				tc.setup(&m)
			}

			tc.test(t, &m)
		})
	}
}

func TestViewport_DiffMode(t *testing.T) {
	t.Parallel()

	rev1 := stringtest.Input(`
		name: original
		value: 10
	`)

	rev2 := stringtest.Input(`
		name: modified
		value: 20
	`)

	rev3 := stringtest.Input(`
		name: final
		value: 30
		new: added
	`)

	rev1Tokens := lexer.Tokenize(rev1)
	rev2Tokens := lexer.Tokenize(rev2)
	rev3Tokens := lexer.Tokenize(rev3)

	tcs := map[string]struct {
		setup func(m *yamlviewport.Model)
		test  func(t *testing.T, m *yamlviewport.Model)
	}{
		"DefaultModeIsAdjacent": {
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev3Tokens, niceyaml.WithName("rev3")))
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, yamlviewport.DiffModeAdjacent, m.DiffMode())
			},
		},
		"SetDiffMode": {
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")))
				m.SetDiffMode(yamlviewport.DiffModeOrigin)
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, yamlviewport.DiffModeOrigin, m.DiffMode())
			},
		},
		"ToggleDiffMode/AdjacentToOrigin": {
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")))
				m.ToggleDiffMode()
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, yamlviewport.DiffModeOrigin, m.DiffMode())
			},
		},
		"ToggleDiffMode/OriginToNone": {
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")))
				m.SetDiffMode(yamlviewport.DiffModeOrigin)
				m.ToggleDiffMode()
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, yamlviewport.DiffModeNone, m.DiffMode())
			},
		},
		"ToggleDiffMode/NoneToAdjacent": {
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")))
				m.SetDiffMode(yamlviewport.DiffModeNone)
				m.ToggleDiffMode()
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, yamlviewport.DiffModeAdjacent, m.DiffMode())
			},
		},
		"ToggleDiffMode/FullCycle": {
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")))
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				// Start: Adjacent.
				assert.Equal(t, yamlviewport.DiffModeAdjacent, m.DiffMode())

				m.ToggleDiffMode()
				assert.Equal(t, yamlviewport.DiffModeOrigin, m.DiffMode())

				m.ToggleDiffMode()
				assert.Equal(t, yamlviewport.DiffModeNone, m.DiffMode())

				m.ToggleDiffMode()
				assert.Equal(t, yamlviewport.DiffModeAdjacent, m.DiffMode())
			},
		},
		"SetDiffMode/None": {
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")))
				m.SetDiffMode(yamlviewport.DiffModeNone)
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, yamlviewport.DiffModeNone, m.DiffMode())
			},
		},
		"ModeNone/NotShowingDiff": {
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev3Tokens, niceyaml.WithName("rev3")))
				m.GoToRevision(1)
				m.SetDiffMode(yamlviewport.DiffModeNone)
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				// At index 1 with None mode, IsShowingDiff should be false.
				assert.False(t, m.IsShowingDiff())
				assert.Equal(t, yamlviewport.DiffModeNone, m.DiffMode())
			},
		},
		"ModeAtIndex0/NoEffect": {
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev3Tokens, niceyaml.WithName("rev3")))
				m.GoToRevision(0)
				m.SetDiffMode(yamlviewport.DiffModeOrigin)
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				// At index 0, both modes show plain view (no diff).
				assert.False(t, m.IsShowingDiff())
				assert.Equal(t, yamlviewport.DiffModeOrigin, m.DiffMode())
			},
		},
		"ModeAtLatest/ShowsDiff": {
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")))
				// Already at latest (index 1).
				m.SetDiffMode(yamlviewport.DiffModeOrigin)
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				// At latest with index > 0, showing diff.
				assert.True(t, m.IsShowingDiff())
				assert.Equal(t, yamlviewport.DiffModeOrigin, m.DiffMode())
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
			m.SetWidth(80)
			m.SetHeight(24)

			if tc.setup != nil {
				tc.setup(&m)
			}

			tc.test(t, &m)
		})
	}
}

func TestViewport_State(t *testing.T) {
	t.Parallel()

	lineCountYAML := stringtest.Input(`
		line1: a
		line2: b
		line3: c
		line4: d
		line5: e
	`)

	pastBottomYAML := `line1: a
line2: b
line3: c`

	tcs := map[string]struct {
		setup  func(m *yamlviewport.Model)
		test   func(t *testing.T, m *yamlviewport.Model)
		yaml   string
		opts   []yamlviewport.Option
		width  int
		height int
	}{
		"Dimensions/SetWidth": {
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				m.SetWidth(100)
				assert.Equal(t, 100, m.Width())
			},
		},
		"Dimensions/SetHeight": {
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				m.SetHeight(50)
				assert.Equal(t, 50, m.Height())
			},
		},
		"Dimensions/ZeroDimensions": {
			opts:   []yamlviewport.Option{yamlviewport.WithPrinter(testPrinter())},
			yaml:   "key: value",
			width:  0,
			height: 0,
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				// View should return empty string for zero dimensions.
				assert.Empty(t, m.View())
			},
		},
		"LineCounts/TotalLineCount": {
			opts:   []yamlviewport.Option{yamlviewport.WithPrinter(testPrinter())},
			yaml:   lineCountYAML,
			width:  80,
			height: 24,
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, 5, m.TotalLineCount())
			},
		},
		"LineCounts/VisibleLineCount": {
			opts:   []yamlviewport.Option{yamlviewport.WithPrinter(testPrinter())},
			yaml:   lineCountYAML,
			width:  80,
			height: 3,
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, 3, m.VisibleLineCount())
			},
		},
		"LineCounts/EmptyContent": {
			opts:   []yamlviewport.Option{yamlviewport.WithPrinter(testPrinter())},
			yaml:   "",
			width:  80,
			height: 24,
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, 0, m.TotalLineCount())
			},
		},
		"Init/ReturnsNil": {
			opts: []yamlviewport.Option{yamlviewport.WithPrinter(testPrinter())},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()

				cmd := m.Init()
				assert.Nil(t, cmd)
			},
		},
		"PastBottom/HeightLargerThanContent": {
			opts:   []yamlviewport.Option{yamlviewport.WithPrinter(testPrinter())},
			yaml:   pastBottomYAML,
			width:  80,
			height: 10,
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.False(t, m.PastBottom())
			},
		},
		"PastBottom/AtTop": {
			opts:   []yamlviewport.Option{yamlviewport.WithPrinter(testPrinter())},
			yaml:   pastBottomYAML,
			width:  80,
			height: 2,
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.False(t, m.PastBottom())
			},
		},
		"PastBottom/AtBottom": {
			opts:   []yamlviewport.Option{yamlviewport.WithPrinter(testPrinter())},
			yaml:   pastBottomYAML,
			width:  80,
			height: 2,
			setup: func(m *yamlviewport.Model) {
				m.GotoBottom()
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.False(t, m.PastBottom())
				assert.True(t, m.AtBottom())
			},
		},
		"PastBottom/SetHeightReclamps": {
			opts:   []yamlviewport.Option{yamlviewport.WithPrinter(testPrinter())},
			yaml:   lineCountYAML,
			width:  80,
			height: 3,
			setup: func(m *yamlviewport.Model) {
				m.GotoBottom()
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, 2, m.YOffset())

				// Growing the viewport lowers the maximum offset, so the
				// offset follows it instead of overshooting the content.
				m.SetHeight(4)
				assert.Equal(t, 1, m.YOffset())
				assert.False(t, m.PastBottom())
				assert.True(t, m.AtBottom())

				m.SetHeight(10)
				assert.Equal(t, 0, m.YOffset())
			},
		},
		"Search/ClearRevisionsResetsMatches": {
			opts:   []yamlviewport.Option{yamlviewport.WithPrinter(testPrinter())},
			yaml:   lineCountYAML,
			width:  80,
			height: 24,
			setup: func(m *yamlviewport.Model) {
				m.SetSearchTerm("line")
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, 5, m.SearchCount())

				m.ClearRevisions()
				assert.Equal(t, 0, m.TotalLineCount())
				assert.Equal(t, 0, m.SearchCount())
				assert.Equal(t, -1, m.SearchIndex())
				assert.Equal(t, "line", m.SearchTerm())

				// The term survives, so the search runs again on new content.
				m.SetSource(niceyaml.NewSourceFromString("line: a\n"))
				assert.Equal(t, 1, m.SearchCount())
				assert.Equal(t, 0, m.SearchIndex())
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := yamlviewport.New(tc.opts...)
			if tc.width > 0 {
				m.SetWidth(tc.width)
			}

			if tc.height > 0 {
				m.SetHeight(tc.height)
			}

			if tc.yaml != "" {
				m.SetSource(niceyaml.NewSourceFromTokens(lexer.Tokenize(tc.yaml)))
			}

			if tc.setup != nil {
				tc.setup(&m)
			}

			tc.test(t, &m)
		})
	}
}

func TestViewport_SetFile(t *testing.T) {
	t.Parallel()

	simpleYAML := stringtest.Input(`
		key: value
		number: 42
	`)

	beforeYAML := stringtest.Input(`
		name: original
		count: 10
	`)

	afterYAML := stringtest.Input(`
		name: modified
		count: 20
		new: added
	`)

	tcs := map[string]struct {
		test       func(t *testing.T, m *yamlviewport.Model)
		yaml       string
		beforeYAML string
		afterYAML  string
	}{
		"SetFile": {
			yaml: simpleYAML,
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, 2, m.TotalLineCount())
				assert.Equal(t, 1, m.RevisionCount())
				assert.False(t, m.IsShowingDiff())
			},
		},
		"AddRevisionFromFile": {
			beforeYAML: beforeYAML,
			afterYAML:  afterYAML,
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, 2, m.RevisionCount())
				assert.Equal(t, 1, m.RevisionIndex()) // At latest (0-indexed).
				assert.True(t, m.IsShowingDiff())     // At index > 0, showing diff.
				assert.Equal(t, "after", m.RevisionName())
				assert.Positive(t, m.TotalLineCount())

				m.GoToRevision(0)
				assert.Equal(t, "before", m.RevisionName())
				assert.False(t, m.IsShowingDiff()) // First revision, no diff.
			},
		},
		"SetTokensClearsRevisions": {
			beforeYAML: beforeYAML,
			afterYAML:  afterYAML,
			yaml:       simpleYAML,
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				// After SetSource, revisions should be replaced with single file.
				assert.Equal(t, 1, m.RevisionCount())
				assert.False(t, m.IsShowingDiff())
				assert.Equal(t, 2, m.TotalLineCount())
				assert.Empty(t, m.RevisionName()) // SetSource uses Source's name.
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
			m.SetWidth(80)
			m.SetHeight(24)

			if tc.beforeYAML != "" && tc.afterYAML != "" {
				beforeLines := niceyaml.NewSourceFromString(tc.beforeYAML, niceyaml.WithName("before"))
				m.AddRevision(beforeLines)

				afterLines := niceyaml.NewSourceFromString(tc.afterYAML, niceyaml.WithName("after"))
				m.AddRevision(afterLines)
			}

			if tc.yaml != "" {
				m.SetSource(niceyaml.NewSourceFromString(tc.yaml))
			}

			tc.test(t, &m)
		})
	}
}

func TestViewport_Update(t *testing.T) {
	t.Parallel()

	verticalYAML := stringtest.Input(`
		line1: a
		line2: b
		line3: c
		line4: d
		line5: e
		line6: f
		line7: g
		line8: h
		line9: i
		line10: j
	`)

	wideYAML := stringtest.Input(`
		short: x
		very_long_key_name_that_requires_horizontal_scrolling: "This is a very long value that extends well beyond the viewport width and requires scrolling to see"
		another: y
	`)

	tcs := map[string]struct {
		msg    tea.Msg
		setup  func(m *yamlviewport.Model)
		test   func(t *testing.T, m *yamlviewport.Model)
		yaml   string
		width  int
		height int
		golden bool
	}{
		// Golden file tests (verify rendered output).
		"KeyDown": {
			msg:    tea.KeyPressMsg{Code: 'j'},
			yaml:   verticalYAML,
			width:  40,
			height: 5,
			golden: true,
		},
		"KeyUp": {
			msg:    tea.KeyPressMsg{Code: 'k'},
			yaml:   verticalYAML,
			width:  40,
			height: 5,
			setup: func(m *yamlviewport.Model) {
				m.SetYOffset(3)
			},
			golden: true,
		},
		"KeyPageDown": {
			msg:    tea.KeyPressMsg{Code: 'f'},
			yaml:   verticalYAML,
			width:  40,
			height: 5,
			golden: true,
		},
		"KeyPageUp": {
			msg:    tea.KeyPressMsg{Code: 'b'},
			yaml:   verticalYAML,
			width:  40,
			height: 5,
			setup: func(m *yamlviewport.Model) {
				m.GotoBottom()
			},
			golden: true,
		},
		"KeyHalfPageDown": {
			msg:    tea.KeyPressMsg{Code: 'd'},
			yaml:   verticalYAML,
			width:  40,
			height: 6,
			golden: true,
		},
		"KeyHalfPageUp": {
			msg:    tea.KeyPressMsg{Code: 'u'},
			yaml:   verticalYAML,
			width:  40,
			height: 6,
			setup: func(m *yamlviewport.Model) {
				m.GotoBottom()
			},
			golden: true,
		},
		"KeyRight": {
			msg:    tea.KeyPressMsg{Code: 'l'},
			yaml:   wideYAML,
			width:  40,
			height: 5,
			setup: func(m *yamlviewport.Model) {
				m.ToggleWordWrap() // Disable wrap.
			},
			golden: true,
		},
		"KeyLeft": {
			msg:    tea.KeyPressMsg{Code: 'h'},
			yaml:   wideYAML,
			width:  40,
			height: 5,
			setup: func(m *yamlviewport.Model) {
				m.ToggleWordWrap() // Disable wrap.
				m.SetXOffset(20)
			},
			golden: true,
		},
		"MouseWheelDown": {
			msg:    tea.MouseWheelMsg{Button: tea.MouseWheelDown},
			yaml:   verticalYAML,
			width:  40,
			height: 5,
			golden: true,
		},
		"MouseWheelUp": {
			msg:    tea.MouseWheelMsg{Button: tea.MouseWheelUp},
			yaml:   verticalYAML,
			width:  40,
			height: 5,
			setup: func(m *yamlviewport.Model) {
				m.SetYOffset(5)
			},
			golden: true,
		},
		// Behavior tests (verify state changes).
		"Behavior/MouseWheelDownWithShift": {
			msg:    tea.MouseWheelMsg{Button: tea.MouseWheelDown, Mod: tea.ModShift},
			yaml:   wideYAML,
			width:  40,
			height: 10,
			setup: func(m *yamlviewport.Model) {
				m.ToggleWordWrap() // Disable wrap.
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Positive(t, m.XOffset())
				assert.Equal(t, 0, m.YOffset()) // Y should not change.
			},
		},
		"Behavior/MouseWheelUpWithShift": {
			msg:   tea.MouseWheelMsg{Button: tea.MouseWheelUp, Mod: tea.ModShift},
			yaml:  wideYAML,
			width: 40,
			setup: func(m *yamlviewport.Model) {
				m.ToggleWordWrap() // Disable wrap.
				m.SetXOffset(20)
			},
			height: 10,
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Less(t, m.XOffset(), 20)
			},
		},
		"Behavior/MouseWheelLeft": {
			msg:    tea.MouseWheelMsg{Button: tea.MouseWheelLeft},
			yaml:   wideYAML,
			width:  40,
			height: 10,
			setup: func(m *yamlviewport.Model) {
				m.ToggleWordWrap() // Disable wrap.
				m.SetXOffset(20)
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Less(t, m.XOffset(), 20)
			},
		},
		"Behavior/MouseWheelRight": {
			msg:    tea.MouseWheelMsg{Button: tea.MouseWheelRight},
			yaml:   wideYAML,
			width:  40,
			height: 10,
			setup: func(m *yamlviewport.Model) {
				m.ToggleWordWrap() // Disable wrap.
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Positive(t, m.XOffset())
			},
		},
		"Behavior/MouseWheelDisabled": {
			msg:    tea.MouseWheelMsg{Button: tea.MouseWheelDown},
			yaml:   verticalYAML,
			width:  80,
			height: 5,
			setup: func(m *yamlviewport.Model) {
				m.MouseWheelEnabled = false
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, 0, m.YOffset()) // Should not scroll.
			},
		},
		"Behavior/MouseWheelCustomDelta": {
			msg:    tea.MouseWheelMsg{Button: tea.MouseWheelDown},
			yaml:   verticalYAML,
			width:  80,
			height: 5,
			setup: func(m *yamlviewport.Model) {
				m.MouseWheelDelta = 5
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, 5, m.YOffset())
			},
		},
		"Behavior/TabNextRevision": {
			msg:    tea.KeyPressMsg{Code: tea.KeyTab},
			yaml:   verticalYAML,
			width:  80,
			height: 5,
			setup: func(m *yamlviewport.Model) {
				// Add a second revision and go to revision 0.
				second := lexer.Tokenize("line1: modified\nline2: changed")
				m.AddRevision(niceyaml.NewSourceFromTokens(second, niceyaml.WithName("change")))
				m.GoToRevision(0)
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, 1, m.RevisionIndex())
				assert.True(t, m.IsShowingDiff())
				assert.Equal(t, "change", m.RevisionName())
			},
		},
		"Behavior/ShiftTabPrevRevision": {
			msg:    tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift},
			yaml:   verticalYAML,
			width:  80,
			height: 5,
			setup: func(m *yamlviewport.Model) {
				// Add a second revision (starts at latest index 1).
				second := lexer.Tokenize("line1: modified\nline2: changed")
				m.AddRevision(niceyaml.NewSourceFromTokens(second, niceyaml.WithName("change")))

				// Now at index 1 (latest).
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				// After PrevRevision, we're at index 0 (the original SetSource revision).
				assert.Equal(t, 0, m.RevisionIndex())
				assert.False(t, m.IsShowingDiff()) // First revision, no diff.
				assert.Empty(t, m.RevisionName())  // SetSource uses empty name.
			},
		},
		"Behavior/MToggleDiffMode": {
			msg:    tea.KeyPressMsg{Code: 'm'},
			yaml:   verticalYAML,
			width:  80,
			height: 5,
			setup: func(m *yamlviewport.Model) {
				second := lexer.Tokenize("line1: modified\nline2: changed")
				m.AddRevision(niceyaml.NewSourceFromTokens(second, niceyaml.WithName("change")))
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, yamlviewport.DiffModeOrigin, m.DiffMode())
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tks := lexer.Tokenize(tc.yaml)

			m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
			m.SetWidth(tc.width)
			m.SetHeight(tc.height)
			m.SetSource(niceyaml.NewSourceFromTokens(tks))

			if tc.setup != nil {
				tc.setup(&m)
			}

			updated, _ := m.Update(tc.msg)

			if tc.golden {
				golden.RequireEqual(t, updated.View())
			}

			if tc.test != nil {
				tc.test(t, &updated)
			}
		})
	}
}

func TestViewport_KeyMap(t *testing.T) {
	t.Parallel()

	tks := lexer.Tokenize("key: value\nkey2: value2\nkey3: value3")
	lines := niceyaml.NewSourceFromTokens(tks)

	tcs := map[string]struct {
		setup func(m *yamlviewport.Model)
		test  func(t *testing.T, m *yamlviewport.Model)
	}{
		"DefaultKeyMapEnabled": {
			test: func(t *testing.T, _ *yamlviewport.Model) {
				t.Helper()

				km := yamlviewport.DefaultKeyMap()
				assert.True(t, km.PageDown.Enabled())
				assert.True(t, km.PageUp.Enabled())
				assert.True(t, km.HalfPageDown.Enabled())
				assert.True(t, km.HalfPageUp.Enabled())
				assert.True(t, km.Down.Enabled())
				assert.True(t, km.Up.Enabled())
				assert.True(t, km.Left.Enabled())
				assert.True(t, km.Right.Enabled())
				assert.True(t, km.NextRevision.Enabled())
				assert.True(t, km.PrevRevision.Enabled())
				assert.True(t, km.ToggleDiffMode.Enabled())
			},
		},
		"CustomKeyMap": {
			setup: func(m *yamlviewport.Model) {
				m.KeyMap.Down = key.NewBinding(key.WithKeys("x"))
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()

				// Default 'j' should not work anymore (since we changed the binding).
				updated, _ := m.Update(tea.KeyPressMsg{Code: 'j'})
				assert.Equal(t, 0, updated.YOffset())

				// Custom 'x' should work.
				updated, _ = m.Update(tea.KeyPressMsg{Code: 'x'})
				assert.Equal(t, 1, updated.YOffset())
			},
		},
		"DisabledKeyBinding": {
			setup: func(m *yamlviewport.Model) {
				m.KeyMap.Down.SetEnabled(false)
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()

				// 'j' should not work.
				updated, _ := m.Update(tea.KeyPressMsg{Code: 'j'})
				assert.Equal(t, 0, updated.YOffset())
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
			m.SetWidth(80)
			m.SetHeight(2)
			m.SetSource(lines)

			if tc.setup != nil {
				tc.setup(&m)
			}

			tc.test(t, &m)
		})
	}
}

func TestViewport_RevisionDeduplication(t *testing.T) {
	t.Parallel()

	content := stringtest.Input(`
		name: same
		value: 10
	`)

	sameTokens1 := lexer.Tokenize(content)
	sameTokens2 := lexer.Tokenize(content)

	differentContent := stringtest.Input(`
		name: different
		value: 20
	`)
	differentTokens := lexer.Tokenize(differentContent)

	tcs := map[string]struct {
		setup func(m *yamlviewport.Model)
		test  func(t *testing.T, m *yamlviewport.Model)
	}{
		"DuplicateRevisions/CountIsCorrect": {
			setup: func(m *yamlviewport.Model) {
				// Add two revisions with identical content.
				m.AddRevision(niceyaml.NewSourceFromTokens(sameTokens1, niceyaml.WithName("first")))
				m.AddRevision(niceyaml.NewSourceFromTokens(sameTokens2, niceyaml.WithName("second")))
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				// Should still report 2 revisions (the logical count).
				assert.Equal(t, 2, m.RevisionCount())
				// At latest (index 1), RevisionName returns the current revision name.
				assert.Equal(t, "second", m.RevisionName())
			},
		},
		"DuplicateRevisions/NavigationWorks": {
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(sameTokens1, niceyaml.WithName("first")))
				m.AddRevision(niceyaml.NewSourceFromTokens(sameTokens2, niceyaml.WithName("second")))
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				// Should be able to navigate through all revisions.
				m.GoToRevision(0)
				assert.Equal(t, 0, m.RevisionIndex())
				assert.True(t, m.IsAtFirstRevision())
				assert.Equal(t, "first", m.RevisionName())

				m.NextRevision()
				assert.Equal(t, 1, m.RevisionIndex())
				assert.Equal(t, "second", m.RevisionName())
				assert.True(t, m.IsAtLatestRevision())

				// Already at latest, NextRevision does nothing.
				m.NextRevision()
				assert.Equal(t, 1, m.RevisionIndex())
				assert.Equal(t, "second", m.RevisionName())
			},
		},
		"DuplicateRevisions/ContentRendersCorrectly": {
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(sameTokens1, niceyaml.WithName("first")))
				m.AddRevision(niceyaml.NewSourceFromTokens(sameTokens2, niceyaml.WithName("second")))
				m.GoToRevision(0)
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()

				// Content should render (not crash).
				view := m.View()
				assert.Contains(t, view, "name")
				assert.Contains(t, view, "same")
				assert.Equal(t, "first", m.RevisionName())
			},
		},
		"MixedRevisions/Works": {
			setup: func(m *yamlviewport.Model) {
				// Two identical + one different.
				m.AddRevision(niceyaml.NewSourceFromTokens(sameTokens1, niceyaml.WithName("first")))
				m.AddRevision(niceyaml.NewSourceFromTokens(sameTokens2, niceyaml.WithName("second")))
				m.AddRevision(niceyaml.NewSourceFromTokens(differentTokens, niceyaml.WithName("different")))
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, 3, m.RevisionCount())

				// At latest (index 2).
				assert.True(t, m.IsAtLatestRevision())
				assert.Equal(t, "different", m.RevisionName())
				assert.True(t, m.IsShowingDiff()) // At index > 0, showing diff.
			},
		},
		"AppendDuplicate/Works": {
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(sameTokens1, niceyaml.WithName("first")))
				m.AddRevision(niceyaml.NewSourceFromTokens(sameTokens2, niceyaml.WithName("second")))
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, 2, m.RevisionCount())
				assert.True(t, m.IsAtLatestRevision())
				assert.Equal(t, "second", m.RevisionName())
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
			m.SetWidth(80)
			m.SetHeight(24)

			if tc.setup != nil {
				tc.setup(&m)
			}

			tc.test(t, &m)
		})
	}
}

func TestViewModeHunks_Golden(t *testing.T) {
	t.Parallel()

	// Base revision with multiple lines for context testing.
	rev1YAML := stringtest.Input(`
		name: original
		count: 10
		enabled: true
		description: "This is the original description"
		tags:
		  - alpha
		  - beta
		settings:
		  timeout: 30
		  retries: 3
	`)

	// Modified revision with changes in middle.
	rev2YAML := stringtest.Input(`
		name: modified
		count: 20
		enabled: true
		description: "This is the updated description"
		tags:
		  - alpha
		  - gamma
		settings:
		  timeout: 60
		  retries: 5
	`)

	// Third revision with additional changes.
	rev3YAML := stringtest.Input(`
		name: final
		count: 30
		enabled: false
		description: "This is the final description"
		tags:
		  - alpha
		  - gamma
		  - delta
		settings:
		  timeout: 90
		  retries: 10
	`)

	rev1Tokens := lexer.Tokenize(rev1YAML)
	rev2Tokens := lexer.Tokenize(rev2YAML)
	rev3Tokens := lexer.Tokenize(rev3YAML)

	type goldenTest struct {
		setupFunc func(m *yamlviewport.Model)
		width     int
		height    int
	}

	tcs := map[string]goldenTest{
		"BasicHunks": {
			setupFunc: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")))
				m.GoToRevision(1)
				m.SetViewMode(yamlviewport.ViewModeHunks)
			},
			width:  80,
			height: 30,
		},
		"AtFirstRevision": {
			setupFunc: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")))
				m.GoToRevision(0)
				m.SetViewMode(yamlviewport.ViewModeHunks)
			},
			width:  80,
			height: 30,
		},
		"AtLatestRevision": {
			setupFunc: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")))
				m.SetViewMode(yamlviewport.ViewModeHunks)

				// Default is at latest (index 2).
			},
			width:  80,
			height: 30,
		},
		"DiffModeOrigin": {
			setupFunc: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev3Tokens, niceyaml.WithName("rev3")))
				m.GoToRevision(2)
				m.SetDiffMode(yamlviewport.DiffModeOrigin)
				m.SetViewMode(yamlviewport.ViewModeHunks)
			},
			width:  80,
			height: 30,
		},
		"DiffModeNone": {
			setupFunc: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")))
				m.GoToRevision(1)
				m.SetDiffMode(yamlviewport.DiffModeNone)
				m.SetViewMode(yamlviewport.ViewModeHunks)
			},
			width:  80,
			height: 30,
		},
		"MultipleRevisions": {
			setupFunc: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev3Tokens, niceyaml.WithName("rev3")))
				m.GoToRevision(2)
				m.SetViewMode(yamlviewport.ViewModeHunks)
			},
			width:  80,
			height: 30,
		},
		"HunksScrolled": {
			// The offset counts rows, and the hunk header is a row.
			setupFunc: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")))
				m.GoToRevision(1)
				m.SetViewMode(yamlviewport.ViewModeHunks)
				m.SetYOffset(2)
			},
			width:  80,
			height: 5,
		},
		"HunksScrolledToBottom": {
			setupFunc: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")))
				m.GoToRevision(1)
				m.SetViewMode(yamlviewport.ViewModeHunks)
				m.GotoBottom()
			},
			width:  80,
			height: 5,
		},
		"HunksSearch": {
			setupFunc: func(m *yamlviewport.Model) {
				m.SetPrinter(testPrinterWithSearch())
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")))
				m.GoToRevision(1)
				m.SetViewMode(yamlviewport.ViewModeHunks)
				m.SetSearchTerm("name")
			},
			width:  80,
			height: 30,
		},
		"HunksSearchNavigate": {
			setupFunc: func(m *yamlviewport.Model) {
				m.SetPrinter(testPrinterWithSearch())
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")))
				m.GoToRevision(1)
				m.SetViewMode(yamlviewport.ViewModeHunks)
				m.SetSearchTerm("name")
				m.SearchNext() // Navigate to second match.
			},
			width:  80,
			height: 30,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
			m.SetWidth(tc.width)
			m.SetHeight(tc.height)

			if tc.setupFunc != nil {
				tc.setupFunc(&m)
			}

			output := m.View()
			golden.RequireEqual(t, output)
		})
	}
}

func TestViewModeSideBySide_Golden(t *testing.T) {
	t.Parallel()

	// Base revision with multiple lines for testing.
	rev1YAML := stringtest.Input(`
		name: original
		count: 10
		enabled: true
		description: "This is the original description"
		tags:
		  - alpha
		  - beta
		settings:
		  timeout: 30
		  retries: 3
		extra:
		  key1: value1
		  key2: value2
	`)

	// Modified revision with changes.
	rev2YAML := stringtest.Input(`
		name: modified
		count: 20
		enabled: true
		description: "This is the updated description"
		tags:
		  - alpha
		  - gamma
		settings:
		  timeout: 60
		  retries: 5
		extra:
		  key1: changed1
		  key2: changed2
	`)

	// Third revision with additional changes.
	rev3YAML := stringtest.Input(`
		name: final
		count: 30
		enabled: false
		description: "This is the final description"
		tags:
		  - alpha
		  - gamma
		  - delta
		settings:
		  timeout: 90
		  retries: 10
	`)

	rev1Tokens := lexer.Tokenize(rev1YAML)
	rev2Tokens := lexer.Tokenize(rev2YAML)
	rev3Tokens := lexer.Tokenize(rev3YAML)

	type goldenTest struct {
		setupFunc func(m *yamlviewport.Model)
		width     int
		height    int
	}

	tcs := map[string]goldenTest{
		"SideBySide": {
			setupFunc: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("v1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("v2")))
				m.GoToRevision(1)
				m.SetViewMode(yamlviewport.ViewModeSideBySide)
			},
			width:  80,
			height: 24,
		},
		"SideBySideNoChanges": {
			// Same content on both sides - verifies identical panes.
			setupFunc: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("v1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("v2")))
				m.GoToRevision(1)
				m.SetViewMode(yamlviewport.ViewModeSideBySide)
			},
			width:  80,
			height: 24,
		},
		"SideBySideAtOrigin": {
			// At revision 0 - shows same content on both panes.
			setupFunc: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("v1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("v2")))
				m.GoToRevision(0)
				m.SetViewMode(yamlviewport.ViewModeSideBySide)
			},
			width:  80,
			height: 24,
		},
		"SideBySideScrolled": {
			// Side-by-side with vertical scrolling.
			setupFunc: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("v1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("v2")))
				m.GoToRevision(1)
				m.SetViewMode(yamlviewport.ViewModeSideBySide)
				m.SetYOffset(3)
			},
			width:  80,
			height: 10,
		},
		"SideBySideDiffModeOrigin": {
			// Side-by-side with origin diff mode (compares to first revision).
			setupFunc: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("v1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("v2")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev3Tokens, niceyaml.WithName("v3")))
				m.GoToRevision(2)
				m.SetDiffMode(yamlviewport.DiffModeOrigin)
				m.SetViewMode(yamlviewport.ViewModeSideBySide)
			},
			width:  80,
			height: 24,
		},
		"SideBySideDiffModeNone": {
			// Side-by-side with no diff mode - shows same content on both panes.
			setupFunc: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("v1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("v2")))
				m.GoToRevision(1)
				m.SetDiffMode(yamlviewport.DiffModeNone)
				m.SetViewMode(yamlviewport.ViewModeSideBySide)
			},
			width:  80,
			height: 24,
		},
		"SideBySideNarrow": {
			// Narrower viewport to test pane width calculation.
			setupFunc: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("v1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("v2")))
				m.GoToRevision(1)
				m.SetViewMode(yamlviewport.ViewModeSideBySide)
			},
			width:  60,
			height: 16,
		},
		"SideBySideTooNarrow": {
			// Width <=4: renders empty without panic.
			setupFunc: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("v1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("v2")))
				m.GoToRevision(1)
				m.SetViewMode(yamlviewport.ViewModeSideBySide)
			},
			width:  0,
			height: 10,
		},
		"SideBySideMinimal": {
			// Width 5: paneWidth = (5-3)/2 = 1, minimum to render content.
			setupFunc: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("v1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("v2")))
				m.GoToRevision(1)
				m.SetViewMode(yamlviewport.ViewModeSideBySide)
			},
			width:  5,
			height: 10,
		},
		"SideBySideMoreDeletions": {
			// More deletions than insertions - placeholders on the right pane.
			setupFunc: func(m *yamlviewport.Model) {
				moreDeletionsBefore := stringtest.Input(`
					keep: start
					del1: a
					del2: b
					del3: c
					del4: d
					keep: end
				`)
				moreDeletionsAfter := stringtest.Input(`
					keep: start
					ins1: x
					keep: end
				`)

				m.ClearRevisions()
				m.AddRevision(niceyaml.NewSourceFromString(moreDeletionsBefore, niceyaml.WithName("v1")))
				m.AddRevision(niceyaml.NewSourceFromString(moreDeletionsAfter, niceyaml.WithName("v2")))
				m.GoToRevision(1)
				m.SetViewMode(yamlviewport.ViewModeSideBySide)
			},
			width:  80,
			height: 16,
		},
		"SideBySideMoreInsertions": {
			// More insertions than deletions - placeholders on the left pane.
			setupFunc: func(m *yamlviewport.Model) {
				moreInsertionsBefore := stringtest.Input(`
					keep: start
					del1: a
					keep: end
				`)
				moreInsertionsAfter := stringtest.Input(`
					keep: start
					ins1: w
					ins2: x
					ins3: y
					ins4: z
					keep: end
				`)

				m.ClearRevisions()
				m.AddRevision(niceyaml.NewSourceFromString(moreInsertionsBefore, niceyaml.WithName("v1")))
				m.AddRevision(niceyaml.NewSourceFromString(moreInsertionsAfter, niceyaml.WithName("v2")))
				m.GoToRevision(1)
				m.SetViewMode(yamlviewport.ViewModeSideBySide)
			},
			width:  80,
			height: 16,
		},
		"SideBySideSearch": {
			// Verifies search highlights work in side-by-side mode.
			setupFunc: func(m *yamlviewport.Model) {
				m.SetPrinter(testPrinterWithSearch())
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("v1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("v2")))
				m.GoToRevision(1)
				m.SetViewMode(yamlviewport.ViewModeSideBySide)
				m.SetSearchTerm("modified")
			},
			width:  80,
			height: 24,
		},
		"SideBySideSearchNavigate": {
			// Verifies search navigation updates highlights in side-by-side mode.
			setupFunc: func(m *yamlviewport.Model) {
				m.SetPrinter(testPrinterWithSearch())
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("v1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("v2")))
				m.GoToRevision(1)
				m.SetViewMode(yamlviewport.ViewModeSideBySide)
				m.SetSearchTerm("enabled") // Appears in both revisions.
				m.SearchNext()             // Navigate to second match.
			},
			width:  80,
			height: 24,
		},
		"SideBySideSearchDeletedLine": {
			// Verifies SearchSelected appears on deleted line (before pane only).
			setupFunc: func(m *yamlviewport.Model) {
				m.SetPrinter(testPrinterWithSearch())
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("v1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("v2")))
				m.GoToRevision(1)
				m.SetViewMode(yamlviewport.ViewModeSideBySide)
				m.SetSearchTerm("original") // Only in deleted line.
			},
			width:  80,
			height: 24,
		},
		"SideBySideSearchInsertedLine": {
			// Verifies SearchSelected appears on inserted line (after pane only).
			setupFunc: func(m *yamlviewport.Model) {
				m.SetPrinter(testPrinterWithSearch())
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("v1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("v2")))
				m.GoToRevision(1)
				m.SetViewMode(yamlviewport.ViewModeSideBySide)
				m.SetSearchTerm("modified") // Only in inserted line.
			},
			width:  80,
			height: 24,
		},
		"SideBySideSearchBothSidesFirstSelected": {
			// Search term appears on both deleted and inserted lines.
			// First match (deleted/before) is selected.
			setupFunc: func(m *yamlviewport.Model) {
				m.SetPrinter(testPrinterWithSearch())
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("v1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("v2")))
				m.GoToRevision(1)
				m.SetViewMode(yamlviewport.ViewModeSideBySide)
				m.SetSearchTerm("name") // Appears on both deleted and inserted lines.

				// First match is selected by default (before/deleted line).
			},
			width:  80,
			height: 24,
		},
		"SideBySideWrapAligned": {
			// The before pane wraps a long line that the after pane replaced
			// with a short one. The after pane gets blank rows so the lines
			// below stay level.
			setupFunc: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromString(stringtest.Input(`
					name: original
					description: "a long description that wraps inside a narrow pane"
					enabled: true
				`), niceyaml.WithName("v1")))
				m.AddRevision(niceyaml.NewSourceFromString(stringtest.Input(`
					name: original
					description: short
					enabled: true
				`), niceyaml.WithName("v2")))
				m.GoToRevision(1)
				m.SetViewMode(yamlviewport.ViewModeSideBySide)
			},
			width:  50,
			height: 8,
		},
		"SideBySideSearchBothSidesSecondSelected": {
			// Search term appears on both deleted and inserted lines.
			// Second match (inserted/after) is selected.
			setupFunc: func(m *yamlviewport.Model) {
				m.SetPrinter(testPrinterWithSearch())
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("v1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("v2")))
				m.GoToRevision(1)
				m.SetViewMode(yamlviewport.ViewModeSideBySide)
				m.SetSearchTerm("name") // Appears on both deleted and inserted lines.
				m.SearchNext()          // Move to second match (after/inserted line).
			},
			width:  80,
			height: 24,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
			m.SetWidth(tc.width)
			m.SetHeight(tc.height)

			if tc.setupFunc != nil {
				tc.setupFunc(&m)
			}

			output := m.View()
			golden.RequireEqual(t, output)
		})
	}
}

func TestViewMode_Behavior(t *testing.T) {
	t.Parallel()

	rev1YAML := stringtest.Input(`
		name: original
		count: 10
		enabled: true
	`)

	rev2YAML := stringtest.Input(`
		name: modified
		count: 20
		enabled: true
	`)

	rev1Tokens := lexer.Tokenize(rev1YAML)
	rev2Tokens := lexer.Tokenize(rev2YAML)

	tcs := map[string]struct {
		setup  func(m *yamlviewport.Model)
		test   func(t *testing.T, m *yamlviewport.Model)
		width  int
		height int
	}{
		"DefaultViewModeIsFull": {
			width:  80,
			height: 24,
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()

				assert.Equal(t, yamlviewport.ViewModeFull, m.ViewMode())
			},
		},
		"SetViewModeHunks": {
			width:  80,
			height: 24,
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")))
				m.GoToRevision(1)
				m.SetViewMode(yamlviewport.ViewModeHunks)
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()

				assert.Equal(t, yamlviewport.ViewModeHunks, m.ViewMode())

				output := m.View()
				assert.NotEmpty(t, output)
			},
		},
		"HunksEmptyRevisions": {
			width:  80,
			height: 24,
			setup: func(m *yamlviewport.Model) {
				m.SetViewMode(yamlviewport.ViewModeHunks)
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()

				output := m.View()
				// Viewport returns blank lines for empty content, not empty string.
				// Check that no YAML keys are present.
				assert.NotContains(t, output, ":")
			},
		},
		"HunksZeroDimensions": {
			width:  0,
			height: 0,
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")))
				m.GoToRevision(1)
				m.SetViewMode(yamlviewport.ViewModeHunks)
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()

				output := m.View()
				assert.Empty(t, output)
			},
		},
		"HunksScrollingApplied": {
			width:  80,
			height: 3,
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")))
				m.GoToRevision(1)
				m.SetViewMode(yamlviewport.ViewModeHunks)
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()

				top := m.View()
				require.NotEmpty(t, top)

				// The hunk header, two deleted lines, and two inserted lines
				// make 6 rows, so the view scrolls.
				assert.Equal(t, 6, m.TotalRowCount())
				assert.Equal(t, 5, m.TotalLineCount())

				m.ScrollDown(1)
				assert.Equal(t, 1, m.YOffset())
				assert.NotEqual(t, top, m.View())

				m.GotoBottom()
				assert.Equal(t, 3, m.YOffset())
				assert.True(t, m.AtBottom())
				assert.Contains(t, m.View(), " enabled: true")
			},
		},
		"ToggleCyclesThroughHunks": {
			width:  80,
			height: 24,
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()

				m.ToggleViewMode()
				assert.Equal(t, yamlviewport.ViewModeHunks, m.ViewMode())

				m.ToggleViewMode()
				assert.Equal(t, yamlviewport.ViewModeSideBySide, m.ViewMode())

				m.ToggleViewMode()
				assert.Equal(t, yamlviewport.ViewModeFull, m.ViewMode())
			},
		},
		"SetHunkContextRebuildsHunks": {
			width:  80,
			height: 24,
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromString(stringtest.Input(`
					first: one
					second: two
					third: three
					fourth: four
					fifth: five
				`), niceyaml.WithName("v1")))
				m.AddRevision(niceyaml.NewSourceFromString(stringtest.Input(`
					first: one
					second: two
					third: three
					fourth: four
					fifth: changed
				`), niceyaml.WithName("v2")))
				m.GoToRevision(1)
				m.SetViewMode(yamlviewport.ViewModeHunks)
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()

				// Three context lines, the deleted line, and the inserted line.
				assert.Equal(t, 5, m.TotalLineCount())

				m.SetHunkContext(0)
				assert.Equal(t, 0, m.HunkContext())
				assert.Equal(t, 2, m.TotalLineCount())
				assert.NotContains(t, m.View(), "fourth")

				m.SetHunkContext(-1)
				assert.Equal(t, 0, m.HunkContext())
			},
		},
		"HunksSearchStaysInsideHunks": {
			width:  80,
			height: 5,
			setup: func(m *yamlviewport.Model) {
				m.SetHunkContext(1)
				m.AddRevision(niceyaml.NewSourceFromString(stringtest.Input(`
					first: one
					second: two
					third: three
					fourth: four
					fifth: five
				`), niceyaml.WithName("v1")))
				m.AddRevision(niceyaml.NewSourceFromString(stringtest.Input(`
					first: one
					second: two
					third: three
					fourth: four
					fifth: changed
				`), niceyaml.WithName("v2")))
				m.GoToRevision(1)
				m.SetViewMode(yamlviewport.ViewModeHunks)
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()

				// One context line, the deleted line, and the inserted line.
				assert.Equal(t, 3, m.TotalLineCount())

				// "first" is in the diff but outside every hunk.
				m.SetSearchTerm("first")
				assert.Equal(t, 0, m.SearchCount())

				m.SetSearchTerm("fifth")
				assert.Equal(t, 2, m.SearchCount())
				assert.Equal(t, 0, m.YOffset())
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
			if tc.width > 0 {
				m.SetWidth(tc.width)
			}

			if tc.height > 0 {
				m.SetHeight(tc.height)
			}

			if tc.setup != nil {
				tc.setup(&m)
			}

			tc.test(t, &m)
		})
	}
}

func TestViewport_ZeroValue(t *testing.T) {
	t.Parallel()

	// A Model not created with New has no printer. It renders nothing and
	// counts no rows instead of panicking, and Update passes messages
	// through.
	var m yamlviewport.Model

	m.SetHeight(3)
	m.SetWidth(20)
	m.SetSource(niceyaml.NewSourceFromString("key: value\n"))

	assert.Empty(t, m.View())

	assert.Equal(t, 0, m.YOffset())
	assert.Equal(t, 0, m.XOffset())
	assert.Equal(t, 1, m.TotalLineCount())
	assert.Equal(t, 0, m.TotalRowCount())
	assert.Equal(t, 0, m.VisibleLineCount())
	assert.Equal(t, 0, m.VisibleRowCount())
	assert.InDelta(t, 1.0, m.ScrollPercent(), 0.01)
	assert.InDelta(t, 1.0, m.HorizontalScrollPercent(), 0.01)
	assert.True(t, m.AtTop())
	assert.True(t, m.AtBottom())
	assert.False(t, m.PastBottom())

	m.ScrollDown(1)
	m.PageDown()
	m.HalfPageDown()
	m.GotoBottom()
	m.ScrollRight(1)
	assert.Equal(t, 0, m.YOffset())
	assert.Equal(t, 0, m.XOffset())

	m.ScrollUp(1)
	m.PageUp()
	m.HalfPageUp()
	m.GotoTop()
	m.ScrollLeft(1)
	m.SetYOffset(1)
	assert.Equal(t, 0, m.YOffset())

	m.AddRevision(niceyaml.NewSourceFromString("key: other\n"))
	m.PrevRevision()
	m.NextRevision()
	m.ToggleDiffMode()
	m.ToggleViewMode()
	m.ToggleWordWrap()
	assert.Equal(t, 0, m.YOffset())
	assert.Equal(t, 0, m.TotalRowCount())
	assert.Empty(t, m.View())

	m, cmd := m.Update(tea.KeyPressMsg{Code: 'j'})
	assert.Nil(t, cmd)
	assert.Empty(t, m.View())
}

// countingSearcher wraps a [finder.Finder] and counts its Load calls.
type countingSearcher struct {
	finder *finder.Finder
	loads  int
}

func (c *countingSearcher) Load(lines line.View) yamlviewport.Index {
	c.loads++

	return c.finder.Load(lines)
}

func TestViewport_LayoutChangesKeepSearchIndex(t *testing.T) {
	t.Parallel()

	searcher := &countingSearcher{finder: finder.New()}
	m := yamlviewport.New(
		yamlviewport.WithPrinter(testPrinter()),
		yamlviewport.WithSearcher(searcher),
	)
	m.SetWidth(80)
	m.SetHeight(10)
	m.AddRevision(niceyaml.NewSourceFromString("key: alpha\n", niceyaml.WithName("v1")))
	m.AddRevision(niceyaml.NewSourceFromString("key: alpha\nother: alpha\n", niceyaml.WithName("v2")))

	m.SetSearchTerm("alpha")
	require.Equal(t, 2, m.SearchCount())
	require.Equal(t, 1, searcher.loads)

	// Printer, style, wrap, and dimension changes leave the view alone, so
	// the searcher keeps its index and the matches survive.
	m.SetPrinter(testPrinterWithColors())
	m.SetStyle(lipgloss.NewStyle().Padding(1))
	m.SetWordWrap(false)
	m.SetWidth(60)
	m.SetHeight(5)

	assert.Equal(t, 1, searcher.loads)
	assert.Equal(t, 2, m.SearchCount())
	assert.NotEmpty(t, m.View())

	// A revision change rebuilds the view and reloads the searcher.
	m.PrevRevision()
	assert.Equal(t, 2, searcher.loads)
	assert.Equal(t, 1, m.SearchCount())
}

func TestViewport_WithSearcher(t *testing.T) {
	t.Parallel()

	t.Run("custom searcher is used for search", func(t *testing.T) {
		t.Parallel()

		searcher := &countingSearcher{finder: finder.New()}
		m := yamlviewport.New(
			yamlviewport.WithPrinter(testPrinter()),
			yamlviewport.WithSearcher(searcher),
		)

		m.SetWidth(80)
		m.SetHeight(10)

		tokens := lexer.Tokenize("key: value\n")
		m.SetSource(niceyaml.NewSourceFromTokens(tokens))

		m.SetSearchTerm("value")

		assert.Equal(t, "value", m.SearchTerm())
		assert.Positive(t, m.SearchCount())
		assert.Equal(t, 1, searcher.loads)
	})

	t.Run("WithFinder searches through the finder", func(t *testing.T) {
		t.Parallel()

		m := yamlviewport.New(
			yamlviewport.WithPrinter(testPrinter()),
			yamlviewport.WithFinder(finder.New()),
		)

		m.SetWidth(80)
		m.SetHeight(10)
		m.SetSource(niceyaml.NewSourceFromString("key: Value\n"))

		// The finder has no normalizer, so the search is case-sensitive.
		m.SetSearchTerm("value")
		assert.Equal(t, 0, m.SearchCount())

		m.SetSearchTerm("Value")
		assert.Equal(t, 1, m.SearchCount())
	})
}

func TestViewport_ScrollEdgeCases(t *testing.T) {
	t.Parallel()

	t.Run("scroll down with n=0 does nothing", func(t *testing.T) {
		t.Parallel()

		m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
		m.SetWidth(80)
		m.SetHeight(10)

		tokens := lexer.Tokenize("line1: value1\nline2: value2\nline3: value3\n")
		m.SetSource(niceyaml.NewSourceFromTokens(tokens))

		initialOffset := m.YOffset()
		m.ScrollDown(0)
		assert.Equal(t, initialOffset, m.YOffset())
	})

	t.Run("scroll down with empty lines does nothing", func(t *testing.T) {
		t.Parallel()

		m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
		m.SetWidth(80)
		m.SetHeight(10)
		// No tokens set - empty lines.

		m.ScrollDown(1)
		assert.Equal(t, 0, m.YOffset())
	})

	t.Run("scroll up with n=0 does nothing", func(t *testing.T) {
		t.Parallel()

		m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
		m.SetWidth(80)
		m.SetHeight(2)

		// Need more lines than viewport height to enable scrolling.
		tokens := lexer.Tokenize("line1: value1\nline2: value2\nline3: value3\nline4: value4\nline5: value5\n")
		m.SetSource(niceyaml.NewSourceFromTokens(tokens))

		m.SetYOffset(2)
		m.ScrollUp(0)
		assert.Equal(t, 2, m.YOffset())
	})

	t.Run("scroll up with empty lines does nothing", func(t *testing.T) {
		t.Parallel()

		m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
		m.SetWidth(80)
		m.SetHeight(10)
		// No tokens set - empty lines.

		m.ScrollUp(1)
		assert.Equal(t, 0, m.YOffset())
	})
}

func TestViewport_RevisionStateEdgeCases(t *testing.T) {
	t.Parallel()

	t.Run("IsAtFirstRevision with multiple revisions", func(t *testing.T) {
		t.Parallel()

		m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
		m.SetWidth(80)
		m.SetHeight(10)

		tokens1 := lexer.Tokenize("v1: value1\n")
		tokens2 := lexer.Tokenize("v2: value2\n")

		m.AddRevision(niceyaml.NewSourceFromTokens(tokens1, niceyaml.WithName("rev1")))
		m.AddRevision(niceyaml.NewSourceFromTokens(tokens2, niceyaml.WithName("rev2")))

		// At latest revision (rev2), not at first.
		assert.False(t, m.IsAtFirstRevision())
		assert.True(t, m.IsAtLatestRevision())

		// Go to first revision.
		m.GoToRevision(0)
		assert.True(t, m.IsAtFirstRevision())
		assert.False(t, m.IsAtLatestRevision())
	})

	t.Run("IsAtFirstRevision with no revisions", func(t *testing.T) {
		t.Parallel()

		m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
		m.SetWidth(80)
		m.SetHeight(10)

		// No revisions - should return true.
		assert.True(t, m.IsAtFirstRevision())
		assert.True(t, m.IsAtLatestRevision())
	})
}

func TestViewport_ToggleWordWrapResetsXOffset(t *testing.T) {
	t.Parallel()

	m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
	m.SetWidth(20)
	m.SetHeight(10)

	tokens := lexer.Tokenize("key: very long value that exceeds width\n")
	m.SetSource(niceyaml.NewSourceFromTokens(tokens))

	// Disable wrapping first.
	m.ToggleWordWrap()
	assert.False(t, m.WordWrap())

	// Scroll right.
	m.ScrollRight(5)
	assert.Equal(t, 5, m.XOffset())

	// Toggle back to enable wrapping - should reset xOffset.
	m.ToggleWordWrap()
	assert.True(t, m.WordWrap())
	assert.Equal(t, 0, m.XOffset())
}

func TestViewport_SetSearchTermEmpty(t *testing.T) {
	t.Parallel()

	m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
	m.SetWidth(80)
	m.SetHeight(10)

	tokens := lexer.Tokenize("key: value\n")
	m.SetSource(niceyaml.NewSourceFromTokens(tokens))

	// Set a search term first.
	m.SetSearchTerm("value")
	assert.Equal(t, "value", m.SearchTerm())
	assert.Positive(t, m.SearchCount())

	// Clear with empty string.
	m.SetSearchTerm("")
	assert.Empty(t, m.SearchTerm())
	assert.Equal(t, 0, m.SearchCount())
}

func TestSideBySideSearch_MatchCounting(t *testing.T) {
	t.Parallel()

	// Before: "foo: a", "bar: b"
	// After:  "foo: c", "bar: b"
	// "foo" appears on deleted + inserted lines = 2 matches.
	// "bar" appears on equal line = 1 match.
	beforeYAML := stringtest.Input(`
		foo: a
		bar: b
	`)
	afterYAML := stringtest.Input(`
		foo: c
		bar: b
	`)

	tcs := map[string]struct {
		searchTerm string
		wantCount  int
	}{
		"EqualLine": {
			searchTerm: "bar",
			wantCount:  1, // Equal lines count as single match.
		},
		"DeletedAndInserted": {
			searchTerm: "foo",
			wantCount:  2, // Deleted and inserted lines are separate matches.
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
			m.SetWidth(80)
			m.SetHeight(24)

			m.AddRevision(niceyaml.NewSourceFromString(beforeYAML, niceyaml.WithName("v1")))
			m.AddRevision(niceyaml.NewSourceFromString(afterYAML, niceyaml.WithName("v2")))
			m.GoToRevision(1)
			m.SetViewMode(yamlviewport.ViewModeSideBySide)
			m.SetSearchTerm(tc.searchTerm)

			assert.Equal(t, tc.wantCount, m.SearchCount())
		})
	}
}

func TestViewport_DoesNotMutateSource(t *testing.T) {
	t.Parallel()

	m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
	m.SetWidth(80)
	m.SetHeight(10)

	source := niceyaml.NewSourceFromString("key: value\n")

	m.SetSource(source)
	m.SetSearchTerm("value")
	require.Positive(t, m.SearchCount())

	_ = m.View()

	m.SetSearchTerm("")

	_ = m.View()

	// Search highlighting never reaches the caller's Source.
	assert.Empty(t, source.Lines()[0].Overlays())
}

func TestViewport_RevisionNavigationResetsSearch(t *testing.T) {
	t.Parallel()

	// Build a document with matches on lines 20 and 30, and a second
	// revision that changes line 5 and keeps both matches.
	var v1, v2 strings.Builder

	for i := range 40 {
		value := "value"
		if i == 20 || i == 30 {
			value = "needle"
		}

		fmt.Fprintf(&v1, "key%d: %s\n", i, value)

		if i == 5 {
			value = "changed"
		}

		fmt.Fprintf(&v2, "key%d: %s\n", i, value)
	}

	m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
	m.SetWidth(80)
	m.SetHeight(5)
	m.AddRevision(niceyaml.NewSourceFromString(v1.String(), niceyaml.WithName("v1")))
	m.AddRevision(niceyaml.NewSourceFromString(v2.String(), niceyaml.WithName("v2")))

	m.SetSearchTerm("needle")
	m.SearchNext()
	require.Equal(t, 1, m.SearchIndex())

	// Moving to another revision starts over at the first match and
	// scrolls to it rather than keeping an index into the old content.
	m.PrevRevision()
	assert.Equal(t, 2, m.SearchCount())
	assert.Equal(t, 0, m.SearchIndex())
	assert.Equal(t, 20-2, m.YOffset())
	assert.Contains(t, m.View(), "key20: needle")

	m.SearchNext()
	m.GoToRevision(1)
	assert.Equal(t, 0, m.SearchIndex())
	assert.Contains(t, m.View(), "key20: needle")

	// Without a search term, navigation returns to the top.
	m.ClearSearch()
	m.PrevRevision()
	assert.Equal(t, 0, m.YOffset())
}

func TestViewport_ContentChangesResetSearch(t *testing.T) {
	t.Parallel()

	// Build a document with matches on lines 20 and 30. The changed version
	// also changes line 5 and keeps both matches.
	doc := func(changed bool) string {
		var sb strings.Builder

		for i := range 40 {
			value := "value"

			switch {
			case i == 20, i == 30:
				value = "needle"
			case i == 5 && changed:
				value = "changed"
			}

			fmt.Fprintf(&sb, "key%d: %s\n", i, value)
		}

		return sb.String()
	}

	tcs := map[string]struct {
		change func(m *yamlviewport.Model)
	}{
		"add revision": {
			change: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromString(doc(false), niceyaml.WithName("v3")))
			},
		},
		"set source": {
			change: func(m *yamlviewport.Model) {
				m.SetSource(niceyaml.NewSourceFromString(doc(true)))
			},
		},
		"diff mode": {
			change: func(m *yamlviewport.Model) { m.SetDiffMode(yamlviewport.DiffModeNone) },
		},
		"view mode": {
			change: func(m *yamlviewport.Model) { m.SetViewMode(yamlviewport.ViewModeSideBySide) },
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
			m.SetWidth(80)
			m.SetHeight(5)
			m.AddRevision(niceyaml.NewSourceFromString(doc(false), niceyaml.WithName("v1")))
			m.AddRevision(niceyaml.NewSourceFromString(doc(true), niceyaml.WithName("v2")))

			m.SetSearchTerm("needle")
			m.SearchNext()
			require.Equal(t, 1, m.SearchIndex())

			// New content starts over at its first match and scrolls to it. An
			// index into the old content points at an arbitrary line.
			tc.change(&m)

			assert.Equal(t, 2, m.SearchCount())
			assert.Equal(t, 0, m.SearchIndex())
			assert.Contains(t, m.View(), "key20: needle")
		})
	}
}

func TestViewport_SearchAcrossRevisions(t *testing.T) {
	t.Parallel()

	m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
	m.SetWidth(80)
	m.SetHeight(10)

	m.AddRevision(niceyaml.NewSourceFromString("key: alpha\n", niceyaml.WithName("v1")))
	m.SetSearchTerm("alpha")
	assert.Equal(t, 1, m.SearchCount())

	// A new revision changes the displayed content, so matches are recomputed
	// against the diff rather than the stale index.
	m.AddRevision(niceyaml.NewSourceFromString("key: alpha\nother: alpha\n", niceyaml.WithName("v2")))
	assert.Equal(t, 2, m.SearchCount())

	m.SetSearchTerm("other")
	assert.Equal(t, 1, m.SearchCount())
}
