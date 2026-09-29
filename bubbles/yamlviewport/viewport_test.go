package yamlviewport_test

import (
	"fmt"
	"math"
	"os"
	"slices"
	"strings"
	"testing"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"
	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	tea "charm.land/bubbletea/v2"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/bubbles/yamlviewport"
	"go.jacobcolvin.com/niceyaml/diff"
	"go.jacobcolvin.com/niceyaml/finder"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/style/kind"
	"go.jacobcolvin.com/niceyaml/style/theme"
	"go.jacobcolvin.com/niceyaml/tokens"
)

func TestViewport_SearchDecorationRefreshesRowCounts(t *testing.T) {
	t.Parallel()

	// A highlight style may transform the text it styles and change its
	// width, so the row counts must follow the decoration of the current
	// term and selection rather than the one before it.
	widen := lipgloss.NewStyle().Transform(func(s string) string {
		return "<<" + s + ">>"
	})
	styles := style.New(lipgloss.NewStyle(),
		style.Set(kind.GenericHighlight, widen),
		style.Set(kind.GenericHighlightDim, widen),
	)
	p := printer.New(
		printer.WithStyles(styles),
		printer.WithContainerStyle(lipgloss.NewStyle()),
		printer.WithGutter(printer.NoGutter),
	)

	m := yamlviewport.New(yamlviewport.WithPrinter(p))
	m.SetWidth(24)
	m.SetHeight(2)
	m.SetWordWrap(true)
	m.SetRevision(niceyaml.NewSourceFromString("k: aaaa aaaa aaaa aaaa\nv: aaaa aaaa aaaa aaaa\n"))

	_ = m.View()

	before := m.TotalRowCount()

	m.SetSearchTerm("aaaa")

	cached := m.TotalRowCount()

	// A width round trip drops every cached count, so this is the true total.
	m.SetWidth(25)
	m.SetWidth(24)
	assert.Equal(t, m.TotalRowCount(), cached)
	assert.Greater(t, cached, before, "the widened highlights should wrap more rows")
}

func TestViewport_SearchNextRefreshesRowCounts(t *testing.T) {
	t.Parallel()

	// Only the selected match widens, so each move of the selection changes
	// the rows and the width of the line it leaves and of the line it
	// reaches. The row counts and the horizontal scroll bound follow both.
	widen := lipgloss.NewStyle().Transform(func(s string) string {
		return "<<" + s + ">>"
	})
	styles := style.New(lipgloss.NewStyle(),
		style.Set(kind.GenericHighlight, widen),
		style.Set(kind.GenericHighlightDim, lipgloss.NewStyle()),
	)
	p := printer.New(
		printer.WithStyles(styles),
		printer.WithContainerStyle(lipgloss.NewStyle()),
		printer.WithGutter(printer.NoGutter),
	)

	// Line i is 9+i columns wide, or 13+i with the selected match, so the
	// widest row and the lines that wrap change with the selection.
	lines := make([]string, 0, 12)
	for i := range 12 {
		lines = append(lines, fmt.Sprintf("k%d: %s term", i, strings.Repeat("a", i)))
	}

	before := strings.Join(lines, "\n") + "\n"
	lines[3] = "k3: changed term"
	after := strings.Join(lines, "\n") + "\n"

	const height = 5

	tcs := map[string]struct {
		viewMode yamlviewport.ViewMode
		width    int
		wrap     bool
	}{
		"unified wrapped": {
			viewMode: yamlviewport.ViewModeFull,
			width:    20,
			wrap:     true,
		},
		"unified unwrapped": {
			viewMode: yamlviewport.ViewModeFull,
			width:    12,
		},
		"side by side wrapped": {
			viewMode: yamlviewport.ViewModeSideBySide,
			width:    43, // Two panes of 20 columns.
			wrap:     true,
		},
		"side by side unwrapped": {
			viewMode: yamlviewport.ViewModeSideBySide,
			width:    27, // Two panes of 12 columns.
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := yamlviewport.New(yamlviewport.WithPrinter(p))
			m.SetWidth(tc.width)
			m.SetHeight(height)
			m.SetWordWrap(tc.wrap)
			m.AddRevision(niceyaml.NewSourceFromString(before, niceyaml.WithName("v1")))
			m.AddRevision(niceyaml.NewSourceFromString(after, niceyaml.WithName("v2")))
			m.SetViewMode(tc.viewMode)
			m.SetSearchTerm("term")
			require.Positive(t, m.SearchCount())

			maxXOffset := func(m yamlviewport.Model) int {
				m.SetXOffset(1 << 30)

				return m.XOffset()
			}

			check := func(step int) {
				t.Helper()

				// The selected match sits on the center row unless the
				// offset stops at either end.
				rows := strings.Split(m.View(), "\n")
				at := slices.IndexFunc(rows, func(row string) bool {
					return strings.Contains(row, "<<")
				})
				require.NotEqual(t, -1, at, "step %d: the selected match is on screen", step)

				if top := m.YOffset(); top > 0 && top < m.TotalRowCount()-height {
					assert.Equal(t, (height-1)/2, at, "step %d", step)
				}

				// A width round trip on a copy drops every cached count, so
				// the copy measures every line with the current decoration.
				fresh := m
				fresh.SetWidth(tc.width + 1)
				fresh.SetWidth(tc.width)

				assert.Equal(t, fresh.TotalRowCount(), m.TotalRowCount(), "step %d", step)
				assert.Equal(t, maxXOffset(fresh), maxXOffset(m), "step %d", step)
			}

			for step := range m.SearchCount() + 2 {
				check(step)
				m.SearchNext()
			}

			for step := range 3 {
				m.SearchPrevious()
				check(-step - 1)
			}
		})
	}
}

func TestViewport_SearchTermRefreshesRowCounts(t *testing.T) {
	t.Parallel()

	// Both highlight styles widen the text they style, so a new term changes
	// the rows and the width of the lines its matches leave and of the lines
	// they reach. The row counts, the horizontal scroll bound, and the
	// render follow every change of term.
	widen := lipgloss.NewStyle().Transform(func(s string) string {
		return "<<" + s + ">>"
	})
	styles := style.New(lipgloss.NewStyle(),
		style.Set(kind.GenericHighlight, widen),
		style.Set(kind.GenericHighlightDim, widen),
	)
	p := printer.New(
		printer.WithStyles(styles),
		printer.WithContainerStyle(lipgloss.NewStyle().Border(lipgloss.NormalBorder()).Padding(0, 1)),
		printer.WithGutter(printer.NoGutter),
	)

	lines := make([]string, 0, 12)
	for i := range 12 {
		lines = append(lines, fmt.Sprintf("k%d: %s term", i, strings.Repeat("a", i)))
	}

	before := strings.Join(lines, "\n") + "\n"
	lines[3] = "k3: changed term"
	after := strings.Join(lines, "\n") + "\n"

	// Terms that match on every line, on a few, across a line break, and
	// on none.
	terms := []string{"term", "aaaa", "k1", "term\nk", "zzz", "a", "changed"}

	tcs := map[string]struct {
		viewMode yamlviewport.ViewMode
		width    int
		wrap     bool
	}{
		"unified wrapped": {
			viewMode: yamlviewport.ViewModeFull,
			width:    24,
			wrap:     true,
		},
		"unified unwrapped": {
			viewMode: yamlviewport.ViewModeFull,
			width:    16,
		},
		"hunks wrapped": {
			viewMode: yamlviewport.ViewModeHunks,
			width:    24,
			wrap:     true,
		},
		"side by side wrapped": {
			viewMode: yamlviewport.ViewModeSideBySide,
			width:    51, // Two panes of 24 columns.
			wrap:     true,
		},
		"side by side unwrapped": {
			viewMode: yamlviewport.ViewModeSideBySide,
			width:    35, // Two panes of 16 columns.
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := yamlviewport.New(yamlviewport.WithPrinter(p))
			m.SetWidth(tc.width)
			m.SetHeight(5)
			m.SetWordWrap(tc.wrap)
			m.AddRevision(niceyaml.NewSourceFromString(before, niceyaml.WithName("v1")))
			m.AddRevision(niceyaml.NewSourceFromString(after, niceyaml.WithName("v2")))
			m.SetViewMode(tc.viewMode)

			_ = m.View()

			maxXOffset := func(m yamlviewport.Model) int {
				m.SetXOffset(1 << 30)

				return m.XOffset()
			}

			check := func(step string) {
				t.Helper()

				// A width round trip on a copy drops every cached count, so
				// the copy measures every line with the current decoration.
				fresh := m
				fresh.SetWidth(tc.width + 1)
				fresh.SetWidth(tc.width)

				assert.Equal(t, fresh.TotalRowCount(), m.TotalRowCount(), step)
				assert.Equal(t, maxXOffset(fresh), maxXOffset(m), step)
				assert.Equal(t, fresh.View(), m.View(), step)
			}

			for _, term := range terms {
				m.SetSearchTerm(term)
				check(fmt.Sprintf("term %q", term))
			}

			m.ClearSearch()
			check("clear")
		})
	}
}

func TestViewport_SearchHighlightsMatchAcrossLines(t *testing.T) {
	t.Parallel()

	// A match that runs across a line break highlights its part of each
	// line, including when the window starts on the second of them. The
	// last line differs between the revisions, so every other line sits at
	// the same index in each view mode.
	var before strings.Builder

	for i := range 30 {
		fmt.Fprintf(&before, "k%d: v%d\n", i, i)
	}

	after := strings.Replace(before.String(), "k29: v29", "k29: changed", 1)

	tcs := map[string]struct {
		want     string
		viewMode yamlviewport.ViewMode
		offset   int
		panes    int
	}{
		"window starts on the first line": {
			viewMode: yamlviewport.ViewModeFull,
			offset:   10,
			want:     "<genericHighlight>v10</genericHighlight>",
			panes:    1,
		},
		"window starts on the second line": {
			viewMode: yamlviewport.ViewModeFull,
			offset:   11,
			want:     "<genericHighlight>k11</genericHighlight>",
			panes:    1,
		},
		"side by side window starts on the second line": {
			viewMode: yamlviewport.ViewModeSideBySide,
			offset:   11,
			want:     "<genericHighlight>k11</genericHighlight>",
			panes:    2,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := yamlviewport.New(yamlviewport.WithPrinter(testPrinterWithSearch()))
			m.SetWidth(160)
			m.SetHeight(5)
			m.AddRevision(niceyaml.NewSourceFromString(before.String(), niceyaml.WithName("v1")))
			m.AddRevision(niceyaml.NewSourceFromString(after, niceyaml.WithName("v2")))
			m.SetViewMode(tc.viewMode)
			m.SetSearchTerm("v10\nk11")
			require.Equal(t, 1, m.SearchCount())

			m.SetYOffset(tc.offset)

			top, _, _ := strings.Cut(m.View(), "\n")
			assert.Equal(t, tc.panes, strings.Count(top, tc.want), top)

			// A width round trip on a copy drops every cached count, so the
			// copy measures every line with the current decoration.
			fresh := m
			fresh.SetWidth(161)
			fresh.SetWidth(160)
			assert.Equal(t, fresh.TotalRowCount(), m.TotalRowCount())
			assert.Equal(t, fresh.View(), m.View())
		})
	}
}

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
		printer.WithStyles(theme.Charm.Styles()),
		printer.WithContainerStyle(lipgloss.NewStyle()),
	)
}

// testPrinterWithSearch returns a printer with XML-style search highlights for testing.
func testPrinterWithSearch() *printer.Printer {
	return printer.New(
		printer.WithStyles(yamltest.NewXMLStyles(
			yamltest.XMLStyleInclude(kind.GenericHighlightDim, kind.GenericHighlight),
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
				m.GotoRevision(1) // Show diff between revision 0 and 1.
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

				// XOffset stays at 0 to verify the viewport truncates the
				// lines instead of wrapping them.
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
				yamlviewport.WithContainerStyle(lipgloss.NewStyle().
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
			// "i" matches four times, in list, item1, item2, and child, so
			// each navigation case selects a different match.
			opts: []yamlviewport.Option{
				yamlviewport.WithPrinter(testPrinterWithSearch()),
			},
			yaml:   simpleYAML,
			width:  80,
			height: 24,
			setupFunc: func(m *yamlviewport.Model, _ token.Tokens) {
				m.SetSearchTerm("i")
				m.SearchNext() // Move to the second match, in item1.
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
				m.SetSearchTerm("i")
				m.SearchPrevious() // Wrap from the first match to the last, in child.
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
				m.SetSearchTerm("i")
				m.SearchNext() // Second match, in item1.
				m.SearchNext() // Third match, in item2.
				m.SearchNext() // Fourth match, in child.
				m.SearchNext() // Wrap to the first match, in list.
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
				m.SetSearchTerm("i")
				m.SearchPrevious() // Wrap to the last match, in child.
				m.SearchPrevious() // Move back to the third match, in item2.
			},
		},
		"SearchMultipleMatchesSameLine": {
			// The XML tags of the test styles take up columns, so the
			// viewport is wide enough that the highlighted line fits on
			// one row.
			opts: []yamlviewport.Option{
				yamlviewport.WithPrinter(testPrinterWithSearch()),
			},
			yaml: stringtest.Input(`
				item: item_value
				another: data
			`),
			width:  120,
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
				m.ClearSearch() // Clearing the search removes the highlights.
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

			tks := tokens.Tokenize(tc.yaml)

			m := yamlviewport.New(tc.opts...)
			m.SetWidth(tc.width)
			m.SetHeight(tc.height)
			m.SetRevision(niceyaml.NewSourceFromTokens(tks))

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
		"Vertical/ScrollDownNegativeAtBottom": {
			yaml:   verticalYAML,
			width:  80,
			height: 5,
			setup: func(m *yamlviewport.Model) {
				m.GotoBottom()
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				m.ScrollDown(-3)
				assert.Equal(t, 2, m.YOffset())
			},
		},
		"Vertical/ScrollUpNegativeAtTop": {
			yaml:   verticalYAML,
			width:  80,
			height: 5,
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				m.ScrollUp(-3)
				assert.Equal(t, 3, m.YOffset())
			},
		},
		"Vertical/InvertedWheelAtTop": {
			yaml:   verticalYAML,
			width:  80,
			height: 5,
			setup: func(m *yamlviewport.Model) {
				m.MouseWheelDelta = -3
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()

				*m, _ = m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
				assert.Equal(t, 3, m.YOffset())
			},
		},
		"Vertical/InvertedWheelAtBottom": {
			yaml:   verticalYAML,
			width:  80,
			height: 5,
			setup: func(m *yamlviewport.Model) {
				m.MouseWheelDelta = -3
				m.GotoBottom()
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()

				*m, _ = m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
				assert.Equal(t, 2, m.YOffset())
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
				m.ToggleWordWrap() // Disable wrap.
				m.SetHorizontalStep(10)
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()

				*m, _ = m.Update(tea.KeyPressMsg{Code: 'l'})
				assert.Equal(t, 10, m.XOffset())

				*m, _ = m.Update(tea.KeyPressMsg{Code: 'h'})
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

			tks := tokens.Tokenize(tc.yaml)

			m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
			m.SetWidth(tc.width)
			m.SetHeight(tc.height)
			m.SetRevision(niceyaml.NewSourceFromTokens(tks))

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
		m.SetRevision(niceyaml.NewSourceFromString(wrapYAML))

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

	t.Run("top line counts lines", func(t *testing.T) {
		t.Parallel()

		m := newModel(t)
		assert.Equal(t, 0, m.TopLine())

		// Row 5 is the second row of line5, which wraps.
		m.SetYOffset(5)
		assert.Equal(t, 4, m.TopLine())

		// The last five rows hold line6 through line10.
		m.GotoBottom()
		assert.Greater(t, m.YOffset(), 5)
		assert.Equal(t, 5, m.TopLine())
	})

	t.Run("bottom shows the last line", func(t *testing.T) {
		t.Parallel()

		m := newModel(t)
		m.GotoBottom()

		assert.Equal(t, m.TotalRowCount()-5, m.YOffset())
		assert.True(t, m.AtBottom())
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
			m.SetRevision(niceyaml.NewSourceFromString(src.String()))
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

func TestViewport_LayoutChangesKeepFrameRow(t *testing.T) {
	t.Parallel()

	framed := func(s lipgloss.Style) *printer.Printer {
		return testPrinter().With(printer.WithContainerStyle(s))
	}

	border := lipgloss.NewStyle().Border(lipgloss.NormalBorder())
	padding := lipgloss.NewStyle().PaddingTop(2).PaddingBottom(2)
	tall := border.Padding(1, 1)

	// A view at the top stays at the top, so a frame that appears or grows
	// shows its outer row. A view whose top row lies in the frame stays on
	// the same row of that frame, or on its last row when the frame shrinks.
	tcs := map[string]struct {
		container lipgloss.Style
		change    func(m *yamlviewport.Model)
		wantTop   string
		lines     int
		height    int
		offset    int
		want      int
		bottom    bool
	}{
		"top gains a border": {
			container: lipgloss.NewStyle(),
			lines:     50,
			height:    5,
			change:    func(m *yamlviewport.Model) { m.SetPrinter(framed(border)) },
			want:      0,
			wantTop:   "─",
		},
		"top frame grows": {
			container: border,
			lines:     50,
			height:    5,
			change:    func(m *yamlviewport.Model) { m.SetPrinter(framed(tall)) },
			want:      0,
			wantTop:   "─",
		},
		"top frame shrinks under its last row": {
			container: padding,
			lines:     50,
			height:    5,
			offset:    1,
			change:    func(m *yamlviewport.Model) { m.SetPrinter(framed(border)) },
			want:      0,
			wantTop:   "─",
		},
		"bottom padding with the same layout": {
			container: padding,
			lines:     5,
			height:    1,
			bottom:    true,
			change:    func(m *yamlviewport.Model) { m.SetPrinter(framed(padding)) },
			want:      8,
		},
		"bottom frame taller than height with the same layout": {
			container: tall,
			lines:     10,
			height:    2,
			bottom:    true,
			change:    func(m *yamlviewport.Model) { m.SetPrinter(framed(tall)) },
			want:      12,
		},
		"bottom frame taller than height at a new width": {
			container: tall,
			lines:     10,
			height:    2,
			bottom:    true,
			change:    func(m *yamlviewport.Model) { m.SetWidth(41) },
			want:      12,
		},
		"bottom frame taller than height with wrap off": {
			container: tall,
			lines:     10,
			height:    2,
			bottom:    true,
			change:    func(m *yamlviewport.Model) { m.SetWordWrap(false) },
			want:      12,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var src strings.Builder

			for i := range tc.lines {
				fmt.Fprintf(&src, "k%d: v\n", i)
			}

			m := yamlviewport.New(yamlviewport.WithPrinter(framed(tc.container)))
			m.SetWidth(40)
			m.SetHeight(tc.height)
			m.SetRevision(niceyaml.NewSourceFromString(src.String()))

			if tc.bottom {
				m.GotoBottom()
			} else {
				m.SetYOffset(tc.offset)
			}

			// A program renders the view before the layout changes.
			_ = m.View()

			tc.change(&m)

			assert.Equal(t, tc.want, m.YOffset())

			if tc.bottom {
				assert.True(t, m.AtBottom())
			}

			if tc.wantTop != "" {
				top, _, _ := strings.Cut(m.View(), "\n")
				assert.Contains(t, top, tc.wantTop)
			}
		})
	}
}

func TestViewport_ContainerStyleKeepsTopLine(t *testing.T) {
	t.Parallel()

	// The new style makes the content area taller and narrower at once. The
	// taller area lowers the scroll limit of the old layout to k17, but the
	// lines wrap in the narrower area, so the new layout can still show k20
	// at the top.
	var src strings.Builder

	for i := range 30 {
		fmt.Fprintf(&src, "k%02d: word word word word\n", i)
	}

	m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
	m.SetWidth(40)
	m.SetHeight(13)
	m.SetContainerStyle(lipgloss.NewStyle().PaddingTop(3))
	m.SetRevision(niceyaml.NewSourceFromString(src.String()))
	m.GotoBottom()
	require.Equal(t, 20, m.YOffset())

	m.SetContainerStyle(lipgloss.NewStyle().PaddingLeft(25))

	assert.Equal(t, 40, m.YOffset())

	top, _, _ := strings.Cut(m.View(), "\n")
	assert.Contains(t, top, "k20:")
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
	m.SetRevision(niceyaml.NewSourceFromString(src.String()))
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
		height    int
		mode      yamlviewport.ViewMode
	}{
		"vertical padding": {
			container: lipgloss.NewStyle().Padding(1, 0),
			top:       1,
			bottom:    1,
			height:    5,
		},
		"top padding": {
			container: lipgloss.NewStyle().PaddingTop(2),
			top:       2,
			height:    5,
		},
		"border": {
			container: lipgloss.NewStyle().Border(lipgloss.NormalBorder()),
			edge:      "─",
			top:       1,
			bottom:    1,
			height:    5,
		},
		"border side by side": {
			container: lipgloss.NewStyle().Border(lipgloss.NormalBorder()),
			edge:      "─",
			top:       1,
			bottom:    1,
			height:    5,
			mode:      yamlviewport.ViewModeSideBySide,
		},
		"frame taller than height": {
			container: lipgloss.NewStyle().Border(lipgloss.NormalBorder()).Padding(1, 1),
			edge:      "─",
			top:       2,
			bottom:    2,
			height:    2,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			const lines = 10

			p := testPrinter().With(printer.WithContainerStyle(tc.container))
			m := yamlviewport.New(yamlviewport.WithPrinter(p))
			m.SetWidth(40)
			m.SetHeight(tc.height)
			m.SetViewMode(tc.mode)
			m.SetRevision(niceyaml.NewSourceFromString(src.String()))

			total := tc.top + lines + tc.bottom
			require.Equal(t, total, m.TotalRowCount())

			m.GotoBottom()
			require.Equal(t, total-tc.height, m.YOffset())

			for offset := range total - tc.height + 1 {
				m.SetYOffset(offset)

				// A top row in the frame reports the nearest line.
				assert.Equal(t, min(max(offset-tc.top, 0), lines-1), m.TopLine(), "offset %d", offset)

				rows := strings.Split(m.View(), "\n")
				require.Len(t, rows, tc.height)

				for i, row := range rows {
					r := offset + i
					if r < tc.top || r >= tc.top+lines {
						assert.NotContains(t, row, "line", "offset %d, row %d", offset, i)

						// The border is the outermost row of the frame. The
						// rest of the frame is padding, which has no edge
						// glyph to show.
						if r == 0 || r == total-1 {
							assert.Contains(t, row, tc.edge, "offset %d, row %d", offset, i)
						}

						continue
					}

					assert.Contains(t, row, fmt.Sprintf("line%d:", r-tc.top+1), "offset %d, row %d", offset, i)
				}
			}
		})
	}
}

func TestViewport_ContainerFrameWidth(t *testing.T) {
	t.Parallel()

	// Three lines that wrap at the viewport width, the last ending in END.
	src := "a: " + strings.Repeat("x", 30) + "\n" +
		"b: " + strings.Repeat("y", 30) + "\n" +
		"c: " + strings.Repeat("z", 30) + " END\n"

	// The horizontal frame of the printer's container style takes columns
	// from the content area, so lines wrap to what is left and every rendered
	// row fits the viewport width. A row wider than the viewport would wrap
	// again in the final render and push the last line out of reach.
	tcs := map[string]struct {
		container lipgloss.Style
		last      string
	}{
		"border": {
			container: lipgloss.NewStyle().Border(lipgloss.NormalBorder()),
			last:      "└",
		},
		"horizontal padding": {
			container: lipgloss.NewStyle().Padding(0, 2),
			last:      "END",
		},
		"margin": {
			container: lipgloss.NewStyle().MarginLeft(3),
			last:      "END",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			const (
				width  = 20
				height = 6
			)

			p := testPrinter().With(printer.WithContainerStyle(tc.container))
			m := yamlviewport.New(yamlviewport.WithPrinter(p))
			m.SetWidth(width)
			m.SetHeight(height)
			m.SetRevision(niceyaml.NewSourceFromString(src))

			for offset := range m.TotalRowCount() - height + 1 {
				m.SetYOffset(offset)

				rows := strings.Split(m.View(), "\n")
				require.Len(t, rows, height, "offset %d", offset)

				for i, row := range rows {
					assert.Equal(t, width, lipgloss.Width(row), "offset %d, row %d", offset, i)
				}
			}

			m.GotoBottom()

			rows := strings.Split(m.View(), "\n")
			assert.Contains(t, rows[len(rows)-1], tc.last)
			assert.Contains(t, m.View(), "END")
		})
	}
}

func TestViewport_ContainerBoxKeepsItsColumns(t *testing.T) {
	t.Parallel()

	// The printer sizes the container's box to the widest row it renders, so
	// a window holding only short lines once drew a box that stopped well
	// short of the viewport width. The viewport pins the box to the content
	// width, so its border sits in the first and last column of every row at
	// every offset.
	src := "a: 1\nb: 2\nc: 3\nlong: " + strings.Repeat("x", 40) + "\nd: 4\ne: 5\n"

	tcs := map[string]struct {
		mode yamlviewport.ViewMode
		wrap bool
	}{
		"wrap on":              {wrap: true},
		"wrap off":             {wrap: false},
		"side by side wrap on": {wrap: true, mode: yamlviewport.ViewModeSideBySide},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			const (
				width  = 24
				height = 3
			)

			p := testPrinter().With(printer.WithContainerStyle(lipgloss.NewStyle().Border(lipgloss.NormalBorder())))
			m := yamlviewport.New(yamlviewport.WithPrinter(p))
			m.SetWidth(width)
			m.SetHeight(height)
			m.SetViewMode(tc.mode)
			m.SetWordWrap(tc.wrap)
			m.SetRevision(niceyaml.NewSourceFromString(src))

			for offset := range m.TotalRowCount() - height + 1 {
				m.SetYOffset(offset)

				for i, row := range strings.Split(m.View(), "\n") {
					plain := []rune(ansi.Strip(row))
					require.Len(t, plain, width, "offset %d, row %d", offset, i)

					assert.Contains(t, "┌│└", string(plain[0]), "offset %d, row %d", offset, i)
					assert.Contains(t, "┐│┘", string(plain[width-1]), "offset %d, row %d", offset, i)
				}
			}
		})
	}
}

func TestViewport_SideBySideFillerRowsKeepTheFrame(t *testing.T) {
	t.Parallel()

	// A line that wraps taller in one pane leaves the other pane short of
	// rows, and the filler rows once rendered as spaces, so the short pane
	// lost its border partway down the window. Every row of both panes
	// carries the border of the printer's container.
	const (
		width  = 31 // Two panes of 14 columns either side of a 3-column separator.
		height = 4
		pane   = 14
	)

	before := niceyaml.NewSourceFromString("a: 1\nb: short\nc: 3\n")
	after := niceyaml.NewSourceFromString("a: 1\nb: " + strings.Repeat("y", 60) + "\nc: 3\n")

	p := testPrinter().With(printer.WithContainerStyle(lipgloss.NewStyle().Border(lipgloss.NormalBorder())))
	m := yamlviewport.New(yamlviewport.WithPrinter(p))
	m.SetWidth(width)
	m.SetHeight(height)
	m.SetViewMode(yamlviewport.ViewModeSideBySide)
	m.AddRevision(before)
	m.AddRevision(after)

	// The wrapped line takes more rows in the right pane than in the left,
	// so the left pane runs out of rows partway down the window.
	require.Greater(t, m.TotalRowCount(), before.View().Count()+2)

	for offset := range m.TotalRowCount() - height + 1 {
		m.SetYOffset(offset)

		for i, row := range strings.Split(m.View(), "\n") {
			plain := []rune(ansi.Strip(row))
			require.Len(t, plain, width, "offset %d, row %d", offset, i)

			for _, col := range []int{0, width - pane} {
				assert.Contains(t, "┌│└", string(plain[col]), "offset %d, row %d, col %d", offset, i, col)
			}

			for _, col := range []int{pane - 1, width - 1} {
				assert.Contains(t, "┐│┘", string(plain[col]), "offset %d, row %d, col %d", offset, i, col)
			}
		}
	}
}

func TestViewport_HorizontalScrollKeepsFrame(t *testing.T) {
	t.Parallel()

	// With wrap off, horizontal scrolling cuts the content columns of each
	// row and leaves the border of the printer's container in place, corners
	// included. The gutter is the first content column, so an offset of 5
	// starts the row at the fifth column of the line.
	tcs := map[string]struct {
		mode    yamlviewport.ViewMode
		width   int
		wantTop string
		wantRow string
	}{
		"full": {
			mode:    yamlviewport.ViewModeFull,
			width:   24,
			wantTop: "┌" + strings.Repeat("─", 22) + "┐",
			wantRow: "│123456789abcdefghijklm│",
		},
		"side by side": {
			mode:    yamlviewport.ViewModeSideBySide,
			width:   30,
			wantTop: "┌───────────┐ │  ┌───────────┐",
			wantRow: "│123456789ab│ │  │123456789ab│",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := testPrinter().With(printer.WithContainerStyle(lipgloss.NewStyle().Border(lipgloss.NormalBorder())))
			m := yamlviewport.New(yamlviewport.WithPrinter(p))
			m.SetWidth(tc.width)
			m.SetHeight(3)
			m.SetViewMode(tc.mode)
			m.SetWordWrap(false)
			m.SetRevision(niceyaml.NewSourceFromString("k: 0123456789abcdefghijklmnopqrstuvwxyz\n"))

			m.SetXOffset(5)
			require.Equal(t, 5, m.XOffset())

			rows := strings.Split(m.View(), "\n")
			require.Len(t, rows, 3)

			assert.Equal(t, tc.wantTop, rows[0])
			assert.Equal(t, tc.wantRow, rows[1])
			assert.Equal(t, strings.ReplaceAll(strings.ReplaceAll(tc.wantTop, "┌", "└"), "┐", "┘"), rows[2])
		})
	}
}

func TestViewport_HorizontalScrollKeepsFrameOfNarrowerPane(t *testing.T) {
	t.Parallel()

	// The offset runs to the widest row of either pane, so the window can
	// reach past the content of the narrower pane. Its right border stays
	// in its column, as the top and bottom borders do.
	p := testPrinter().With(printer.WithContainerStyle(lipgloss.NewStyle().Border(lipgloss.NormalBorder())))
	m := yamlviewport.New(yamlviewport.WithPrinter(p))
	m.SetWidth(31)
	m.SetHeight(3)
	m.SetViewMode(yamlviewport.ViewModeSideBySide)
	m.SetWordWrap(false)
	m.SetRevision(niceyaml.NewSourceFromString("a: 1\nb: s\nc: 3\n"))
	m.AddRevision(niceyaml.NewSourceFromString("a: 1\nb: " + strings.Repeat("y", 50) + "\nc: 3\n"))

	for _, offset := range []int{1, 10, 40} {
		m.SetXOffset(offset)
		require.Equal(t, offset, m.XOffset())

		rows := strings.Split(m.View(), "\n")
		require.Len(t, rows, 3)

		top := []rune(rows[0])
		row := []rune(rows[1])
		require.Len(t, row, len(top), "offset %d: %q", offset, rows[1])

		for i, r := range top {
			if r == '┌' || r == '┐' {
				assert.Equal(t, '│', row[i], "offset %d column %d: %q", offset, i, rows[1])
			}
		}
	}
}

func TestViewport_HorizontalScrollKeepsFrameAtWideRune(t *testing.T) {
	t.Parallel()

	// A wide character that begins before the horizontal offset has only its
	// right cell inside the window. The cut once kept the whole character,
	// which pushed the rest of the row one column right and the right border
	// out of the view. The window shows that cell as a blank, so the border
	// keeps its column. The gutter and "k: " take four columns, so an offset
	// of 5 falls inside the character.
	tcs := map[string]struct {
		mode  yamlviewport.ViewMode
		width int
	}{
		"full": {
			mode:  yamlviewport.ViewModeFull,
			width: 20,
		},
		"side by side": {
			mode:  yamlviewport.ViewModeSideBySide,
			width: 41,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := testPrinter().With(printer.WithContainerStyle(lipgloss.NewStyle().Border(lipgloss.NormalBorder())))
			m := yamlviewport.New(yamlviewport.WithPrinter(p))
			m.SetWidth(tc.width)
			m.SetHeight(4)
			m.SetViewMode(tc.mode)
			m.SetWordWrap(false)
			m.SetRevision(niceyaml.NewSourceFromString(
				"a: " + strings.Repeat("a", 34) + "\nk: 日" + strings.Repeat("b", 36) + "\n",
			))

			m.SetXOffset(5)
			require.Equal(t, 5, m.XOffset())

			rows := strings.Split(ansi.Strip(m.View()), "\n")
			require.Len(t, rows, 4)

			top := rows[0]
			for i, row := range rows[1 : len(rows)-1] {
				require.Equal(t, ansi.StringWidth(top), ansi.StringWidth(row), "row %d: %q", i, row)

				for col, r := range []rune(top) {
					if r == '┌' || r == '┐' {
						assert.Equal(t, "│", ansi.Cut(row, col, col+1), "row %d, col %d: %q", i, col, row)
					}
				}
			}

			assert.True(t, strings.HasPrefix(rows[2], "│ b"), "%q", rows[2])
		})
	}
}

func TestViewport_SideBySideWithoutPaneColumns(t *testing.T) {
	t.Parallel()

	// The separator takes three columns, so a width of 3 or 4 leaves the
	// panes no column. The view shows nothing, and the counts of what is
	// on screen agree with it.
	tcs := map[string]struct {
		width int
	}{
		"width 3": {width: 3},
		"width 4": {width: 4},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
			m.SetWidth(tc.width)
			m.SetHeight(5)
			m.AddRevision(niceyaml.NewSourceFromString("a: 1\nb: 2\nc: 3\n", niceyaml.WithName("v1")))
			m.AddRevision(niceyaml.NewSourceFromString("a: 1\nb: 9\nc: 3\n", niceyaml.WithName("v2")))
			m.SetViewMode(yamlviewport.ViewModeSideBySide)

			assert.Empty(t, m.View())
			assert.Equal(t, 0, m.VisibleLineCount())
			assert.Equal(t, 0, m.VisibleRowCount())
		})
	}
}

func TestViewport_DiffOfRevisionsOverPartOfTheSource(t *testing.T) {
	t.Parallel()

	// A diff compares the lines each revision holds, as the other view
	// modes render them, so a change outside the held lines does not show.
	slice := func(input string) yamlviewport.Revision {
		source := niceyaml.NewSourceFromString(input)

		return yamlviewport.NewRevision("part", source.View().Slice(position.NewSpan(1, 4)))
	}

	m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
	m.SetWidth(80)
	m.SetHeight(10)
	m.SetRevision(slice("a: 1\nb: 2\nc: 3\nd: 4\ne: 5\n"))
	m.AddRevision(slice("a: 9\nb: 2\nc: 3\nd: 4\ne: 9\n"))

	require.True(t, m.ShowingDiff())

	assert.Equal(t, diff.Stats{}, m.DiffStats())
	assert.Equal(t, 3, m.TotalLineCount())
	assert.NotContains(t, m.View(), "a: 9")
	assert.NotContains(t, m.View(), "e: 5")

	// A change inside the held lines shows as it does for a whole source.
	m.AddRevision(slice("a: 9\nb: 2\nc: 8\nd: 4\ne: 9\n"))

	assert.Equal(t, diff.Stats{Added: 1, Removed: 1}, m.DiffStats())
	assert.Contains(t, m.View(), "c: 8")
	assert.Contains(t, m.View(), "c: 3")
}

func TestViewport_ViewFitsHeight(t *testing.T) {
	t.Parallel()

	border := lipgloss.NewStyle().Border(lipgloss.NormalBorder())

	// A view is as tall as the set height, and a height with no room for
	// content, whether negative or swallowed by the frame of the container
	// style, renders nothing at all.
	tcs := map[string]struct {
		style    lipgloss.Style
		height   int
		wantRows int
	}{
		"negative height":                   {height: -1},
		"zero height":                       {height: 0},
		"one row":                           {height: 1, wantRows: 1},
		"border with no room":               {style: border, height: 2},
		"border smaller than its own frame": {style: border, height: 1},
		"border with one row":               {style: border, height: 3, wantRows: 3},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := yamlviewport.New(
				yamlviewport.WithPrinter(testPrinter()),
				yamlviewport.WithContainerStyle(tc.style),
			)
			m.SetWidth(20)
			m.SetHeight(tc.height)
			m.SetRevision(niceyaml.NewSourceFromString("a: 1\nb: 2\nc: 3\n"))

			view := m.View()
			if tc.wantRows == 0 {
				assert.Empty(t, view)

				return
			}

			assert.Len(t, strings.Split(view, "\n"), tc.wantRows)
		})
	}
}

func TestViewport_FixedSizeContainerStyle(t *testing.T) {
	t.Parallel()

	box := lipgloss.NewStyle().Border(lipgloss.NormalBorder())

	// A fixed Width or Height on the container style caps the box as
	// lipgloss draws it, and margins add to that size.
	tcs := map[string]struct {
		style      lipgloss.Style
		wantWidth  int
		wantHeight int
	}{
		"fixed size": {
			style:      box.Width(30).Height(8),
			wantWidth:  30,
			wantHeight: 8,
		},
		"fixed size with margins": {
			style:      box.Margin(1, 3).Width(30).Height(8),
			wantWidth:  36,
			wantHeight: 10,
		},
		"fixed width with margins": {
			style:      box.Margin(1, 3).Width(30),
			wantWidth:  36,
			wantHeight: 40,
		},
		"fixed height with margins": {
			style:      box.Margin(1, 3).Height(8),
			wantWidth:  60,
			wantHeight: 10,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := yamlviewport.New(
				yamlviewport.WithPrinter(testPrinter()),
				yamlviewport.WithContainerStyle(tc.style),
			)
			m.SetWidth(60)
			m.SetHeight(40)
			m.SetRevision(niceyaml.NewSourceFromString("a: 1\nb: 2\n"))

			view := m.View()
			assert.Equal(t, tc.wantWidth, lipgloss.Width(view))
			assert.Equal(t, tc.wantHeight, lipgloss.Height(view))
		})
	}
}

func TestViewport_MaxSizeContainerStyle(t *testing.T) {
	t.Parallel()

	box := lipgloss.NewStyle().Border(lipgloss.NormalBorder())

	// A MaxWidth or MaxHeight on the container style caps the box, margins
	// included, so the whole border shows and a page moves by the rows on
	// screen.
	tcs := map[string]struct {
		style      lipgloss.Style
		wantWidth  int
		wantHeight int
		wantRows   int
	}{
		"max size": {
			style:      box.MaxWidth(12).MaxHeight(6),
			wantWidth:  12,
			wantHeight: 6,
			wantRows:   4,
		},
		"max size with margins": {
			style:      box.Margin(1, 2).MaxWidth(16).MaxHeight(8),
			wantWidth:  16,
			wantHeight: 8,
			wantRows:   4,
		},
		"max size above fixed size": {
			style:      box.Width(12).Height(6).MaxWidth(14).MaxHeight(8),
			wantWidth:  12,
			wantHeight: 6,
			wantRows:   4,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var src strings.Builder

			for i := range 50 {
				fmt.Fprintf(&src, "k%d: v\n", i)
			}

			m := yamlviewport.New(
				yamlviewport.WithPrinter(testPrinter()),
				yamlviewport.WithContainerStyle(tc.style),
			)
			m.SetWidth(20)
			m.SetHeight(12)
			m.SetRevision(niceyaml.NewSourceFromString(src.String()))

			view := m.View()
			assert.Equal(t, tc.wantWidth, lipgloss.Width(view))
			assert.Equal(t, tc.wantHeight, lipgloss.Height(view))
			assert.Contains(t, view, "┘")
			assert.Equal(t, tc.wantRows, m.VisibleRowCount())

			m.PageDown()
			assert.Equal(t, tc.wantRows, m.YOffset())
		})
	}
}

func TestViewport_PrinterContainerSizeIgnored(t *testing.T) {
	t.Parallel()

	// The viewport sizes the printer's container to the content area. A
	// fixed size on that container would wrap, pad, or cut the rows after
	// the viewport counted them, so the viewport drops it, and the last
	// row stays reachable.
	tcs := map[string]struct {
		container lipgloss.Style
		line      string
		lines     int
		width     int
		height    int
		wantTotal int
		wantLast  string
	}{
		"width": {
			container: lipgloss.NewStyle().Width(30),
			line:      "k%d: aaaa bbbb cccc dddd eeee ffff gggg hhhh",
			lines:     20,
			width:     50,
			height:    6,
			wantTotal: 20,
			wantLast:  "k20:",
		},
		"max width": {
			container: lipgloss.NewStyle().MaxWidth(30),
			line:      "k%d: aaaa bbbb cccc dddd eeee ffff gggg hhhh",
			lines:     20,
			width:     50,
			height:    6,
			wantTotal: 20,
			wantLast:  "hhhh",
		},
		"height": {
			container: lipgloss.NewStyle().Border(lipgloss.NormalBorder()).Height(8),
			line:      "k%d: v",
			lines:     10,
			width:     20,
			height:    4,
			wantTotal: 12,
			wantLast:  "└",
		},
		"max height": {
			container: lipgloss.NewStyle().Border(lipgloss.NormalBorder()).MaxHeight(4),
			line:      "k%d: v",
			lines:     10,
			width:     20,
			height:    4,
			wantTotal: 12,
			wantLast:  "└",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var src strings.Builder

			for i := 1; i <= tc.lines; i++ {
				fmt.Fprintf(&src, tc.line+"\n", i)
			}

			p := testPrinter().With(printer.WithContainerStyle(tc.container))
			m := yamlviewport.New(yamlviewport.WithPrinter(p))
			m.SetWidth(tc.width)
			m.SetHeight(tc.height)
			m.SetWordWrap(true)
			m.SetRevision(niceyaml.NewSourceFromString(src.String()))

			assert.Equal(t, tc.wantTotal, m.TotalRowCount())

			m.GotoBottom()

			rows := strings.Split(m.View(), "\n")
			require.Len(t, rows, tc.height)
			assert.Contains(t, rows[len(rows)-1], tc.wantLast)
		})
	}
}

func TestViewport_ViewFillsContentArea(t *testing.T) {
	t.Parallel()

	// Every view fills the content area, whether it shows no line, fewer
	// lines than the height, or more.
	var long strings.Builder

	for i := range 20 {
		fmt.Fprintf(&long, "k%d: %s\n", i, strings.Repeat("word ", i))
	}

	tcs := map[string]struct {
		yaml string
		mode yamlviewport.ViewMode
	}{
		"empty":                    {},
		"fewer lines than height":  {yaml: "a: 1\n"},
		"more lines than height":   {yaml: long.String()},
		"side by side empty":       {mode: yamlviewport.ViewModeSideBySide},
		"side by side fewer lines": {yaml: "a: 1\n", mode: yamlviewport.ViewModeSideBySide},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			const width, height = 40, 6

			m := yamlviewport.New(yamlviewport.WithPrinter(testPrinterWithColors()))
			m.SetWidth(width)
			m.SetHeight(height)
			m.SetViewMode(tc.mode)

			if tc.yaml != "" {
				m.SetRevision(niceyaml.NewSourceFromString(tc.yaml))
			}

			rows := strings.Split(m.View(), "\n")
			require.Len(t, rows, height)

			for i, row := range rows {
				assert.Equal(t, width, lipgloss.Width(row), "row %d", i)
			}
		})
	}
}

func TestViewport_ViewFitsWidth(t *testing.T) {
	t.Parallel()

	// A gutter keeps a rendered row at its own width plus a content column,
	// however narrow the wrap width is, so a viewport of a few columns gets
	// rows wider than it asked for. The view clips them to its width.
	tcs := map[string]struct {
		printer *printer.Printer
		mode    yamlviewport.ViewMode
	}{
		"diff gutter":  {printer: testPrinter()},
		"line numbers": {printer: testPrinterWithLineNumbers()},
		"side by side": {printer: testPrinter(), mode: yamlviewport.ViewModeSideBySide},
		"hunks":        {printer: testPrinter(), mode: yamlviewport.ViewModeHunks},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			const height = 3

			for width := 1; width <= 12; width++ {
				m := yamlviewport.New(yamlviewport.WithPrinter(tc.printer))
				m.SetWidth(width)
				m.SetHeight(height)
				m.SetViewMode(tc.mode)
				m.AddRevision(niceyaml.NewSourceFromString("name: original\ncount: 10\n"))
				m.AddRevision(niceyaml.NewSourceFromString("name: modified\ncount: 20\n"))

				view := m.View()
				if view == "" {
					continue
				}

				for i, row := range strings.Split(view, "\n") {
					assert.LessOrEqual(t, lipgloss.Width(row), width, "width %d, row %d", width, i)
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

	tks := tokens.Tokenize(yaml)
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
			m.SetRevision(lines)
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

	rev1Tokens := tokens.Tokenize(rev1)
	rev2Tokens := tokens.Tokenize(rev2)
	rev3Tokens := tokens.Tokenize(rev3)

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
				assert.False(t, m.ShowingDiff())
				assert.Empty(t, m.RevisionName())
			},
		},
		"AddRevision/Nil": {
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(nil)
				m.SetRevision(nil)
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, 0, m.RevisionCount())
				assert.Empty(t, m.RevisionName())
				assert.Empty(t, m.RevisionNames())
			},
		},
		"AddRevision/NilSource": {
			setup: func(m *yamlviewport.Model) {
				m.AddRevision((*niceyaml.Source)(nil))
				m.SetRevision((*niceyaml.Source)(nil))
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, 0, m.RevisionCount())
				assert.Empty(t, m.RevisionName())
				assert.Empty(t, m.RevisionNames())
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
				assert.True(t, m.AtLatestRevision())
				assert.False(t, m.ShowingDiff()) // Only one revision, no diff possible.
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
				assert.True(t, m.AtLatestRevision())
				assert.True(t, m.ShowingDiff()) // At index > 0 with default diffMode.
				assert.Equal(t, "rev3", m.RevisionName())
			},
		},
		"AddRevisions/Multiple": {
			setup: func(m *yamlviewport.Model) {
				m.AddRevisions(
					niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")),
					niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")),
					niceyaml.NewSourceFromTokens(rev3Tokens, niceyaml.WithName("rev3")),
				)
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, 3, m.RevisionCount())
				assert.Equal(t, 2, m.RevisionIndex())
				assert.True(t, m.AtLatestRevision())
				assert.True(t, m.ShowingDiff())
				assert.Equal(t, []string{"rev1", "rev2", "rev3"}, m.RevisionNames())
			},
		},
		"AddRevisions/SkipsNil": {
			setup: func(m *yamlviewport.Model) {
				m.AddRevisions(
					nil,
					niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")),
					(*niceyaml.Source)(nil),
					niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")),
					nil,
				)
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, 2, m.RevisionCount())
				assert.Equal(t, 1, m.RevisionIndex())
				assert.Equal(t, []string{"rev1", "rev2"}, m.RevisionNames())
			},
		},
		"AddRevisions/Empty": {
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")))
				m.GotoRevision(0)
				m.AddRevisions()
				m.AddRevisions(nil, (*niceyaml.Source)(nil))
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, 2, m.RevisionCount())
				assert.Equal(t, 0, m.RevisionIndex()) // Stays on the revision it was at.
				assert.Equal(t, "rev1", m.RevisionName())
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
		"GotoRevision/First": {
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev3Tokens, niceyaml.WithName("rev3")))
				m.GotoRevision(0)
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, 0, m.RevisionIndex())
				assert.True(t, m.AtFirstRevision())
				assert.False(t, m.ShowingDiff()) // Position 0 shows plain view.
				assert.Equal(t, "rev1", m.RevisionName())
			},
		},
		"GotoRevision/Middle": {
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev3Tokens, niceyaml.WithName("rev3")))
				m.GotoRevision(1)
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, 1, m.RevisionIndex())
				assert.True(t, m.ShowingDiff()) // Position 1 shows diff.
				assert.Equal(t, "rev2", m.RevisionName())
			},
		},
		"GotoRevision/Clamped": {
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")))
				m.GotoRevision(100) // Should clamp to max (N-1).
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, 1, m.RevisionIndex()) // Clamped to last index (0-indexed).
				assert.True(t, m.AtLatestRevision())
				assert.Equal(t, "rev2", m.RevisionName())
			},
		},
		"NextRevision": {
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev3Tokens, niceyaml.WithName("rev3")))
				m.GotoRevision(0)
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, "rev1", m.RevisionName())
				m.NextRevision()
				assert.Equal(t, 1, m.RevisionIndex())
				assert.True(t, m.ShowingDiff())
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
		"PreviousRevision": {
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev3Tokens, niceyaml.WithName("rev3")))
				m.GotoRevision(2)
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, "rev3", m.RevisionName())
				m.PreviousRevision()
				assert.Equal(t, 1, m.RevisionIndex())
				assert.Equal(t, "rev2", m.RevisionName())
			},
		},
		"PreviousRevision/AtFirst": {
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")))
				m.GotoRevision(0)
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				m.PreviousRevision()
				assert.Equal(t, 0, m.RevisionIndex()) // Should not change.
				assert.Equal(t, "rev1", m.RevisionName())
			},
		},
		"ShowingDiff/BoundaryConditions": {
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev3Tokens, niceyaml.WithName("rev3")))
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				// Index 2 is the latest, so the view shows a diff in the
				// default mode.
				assert.True(t, m.ShowingDiff())
				assert.Equal(t, "rev3", m.RevisionName())

				m.GotoRevision(0)
				assert.False(t, m.ShowingDiff()) // First revision, no diff.
				assert.Equal(t, "rev1", m.RevisionName())

				m.GotoRevision(1)
				assert.True(t, m.ShowingDiff()) // Between 0 and 1.
				assert.Equal(t, "rev2", m.RevisionName())

				m.GotoRevision(2)
				assert.True(t, m.ShowingDiff()) // Between 1 and 2.
				assert.Equal(t, "rev3", m.RevisionName())
			},
		},
		"SetTokensReplacesSingleRevision": {
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")))
				m.SetRevision(niceyaml.NewSourceFromTokens(rev3Tokens)) // SetRevision uses the Source's name.
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

	rev1Tokens := tokens.Tokenize(rev1)
	rev2Tokens := tokens.Tokenize(rev2)
	rev3Tokens := tokens.Tokenize(rev3)

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
		"SetDiffMode/OutOfRange": {
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")))
				m.SetDiffMode(yamlviewport.DiffMode(3))
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				// An undefined mode falls back to the default.
				assert.Equal(t, yamlviewport.DiffModeAdjacent, m.DiffMode())
				assert.True(t, m.ShowingDiff())
				assert.Equal(t, diff.Stats{Added: 2, Removed: 2}, m.DiffStats())
			},
		},
		"ToggleDiffMode/FromOutOfRange": {
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")))
				m.SetDiffMode(yamlviewport.DiffMode(-1))
				m.ToggleDiffMode()
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, yamlviewport.DiffModeOrigin, m.DiffMode())
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
				m.GotoRevision(1)
				m.SetDiffMode(yamlviewport.DiffModeNone)
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				// At index 1 with None mode, ShowingDiff should be false.
				assert.False(t, m.ShowingDiff())
				assert.Equal(t, yamlviewport.DiffModeNone, m.DiffMode())
			},
		},
		"ModeAtIndex0/NoEffect": {
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev3Tokens, niceyaml.WithName("rev3")))
				m.GotoRevision(0)
				m.SetDiffMode(yamlviewport.DiffModeOrigin)
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				// At index 0, both modes show plain view (no diff).
				assert.False(t, m.ShowingDiff())
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
				// The latest revision has index > 0, so the view shows a diff.
				assert.True(t, m.ShowingDiff())
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

func TestViewport_SameDiffBaseKeepsPosition(t *testing.T) {
	t.Parallel()

	// At revision index 1, adjacent and origin both compare against the
	// first revision, so a change between them shows the same diff and
	// leaves the scroll offset and the selected match where they are.
	doc := func(changed int) string {
		var sb strings.Builder

		for i := range 50 {
			value := "v"
			if i == changed {
				value = "w"
			}

			fmt.Fprintf(&sb, "k%d: %s\n", i, value)
		}

		return sb.String()
	}

	m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
	m.SetWidth(40)
	m.SetHeight(10)
	m.AddRevision(niceyaml.NewSourceFromString(doc(-1), niceyaml.WithName("v1")))
	m.AddRevision(niceyaml.NewSourceFromString(doc(20), niceyaml.WithName("v2")))
	m.SetSearchTerm("v")
	m.SearchNext()
	m.SearchNext()
	m.SetYOffset(30)

	require.Equal(t, 1, m.RevisionIndex())
	require.Equal(t, 2, m.SearchIndex())
	require.Equal(t, 30, m.YOffset())

	m.ToggleDiffMode()
	require.Equal(t, yamlviewport.DiffModeOrigin, m.DiffMode())
	assert.Equal(t, 2, m.SearchIndex(), "adjacent to origin")
	assert.Equal(t, 30, m.YOffset(), "adjacent to origin")

	m.SetDiffMode(yamlviewport.DiffModeAdjacent)
	assert.Equal(t, 2, m.SearchIndex(), "origin to adjacent")
	assert.Equal(t, 30, m.YOffset(), "origin to adjacent")

	// At revision index 2, origin compares against another revision than
	// adjacent does, so the view shows other content and starts over.
	m.AddRevision(niceyaml.NewSourceFromString(doc(40), niceyaml.WithName("v3")))
	m.SearchNext()
	m.SetYOffset(30)

	require.Equal(t, 1, m.SearchIndex())
	require.Equal(t, 30, m.YOffset())

	m.SetDiffMode(yamlviewport.DiffModeOrigin)
	assert.Equal(t, 0, m.SearchIndex(), "adjacent to origin at index 2")
	assert.Equal(t, 0, m.YOffset(), "adjacent to origin at index 2")
}

func TestViewport_ViewModeWithoutDiffKeepsPosition(t *testing.T) {
	t.Parallel()

	// Without a diff every view mode shows the same lines, so a change of
	// view mode keeps the top line and the selected match. With a diff the
	// modes show other content, so the view starts over at the first match.
	tcs := map[string]struct {
		from   yamlviewport.ViewMode
		to     yamlviewport.ViewMode
		diff   bool
		toggle bool
		keep   bool
	}{
		"set full to hunks": {
			from: yamlviewport.ViewModeFull,
			to:   yamlviewport.ViewModeHunks,
			keep: true,
		},
		"set full to side-by-side": {
			from: yamlviewport.ViewModeFull,
			to:   yamlviewport.ViewModeSideBySide,
			keep: true,
		},
		"set side-by-side to full": {
			from: yamlviewport.ViewModeSideBySide,
			to:   yamlviewport.ViewModeFull,
			keep: true,
		},
		"toggle full to hunks": {
			from:   yamlviewport.ViewModeFull,
			to:     yamlviewport.ViewModeHunks,
			toggle: true,
			keep:   true,
		},
		"toggle hunks to side-by-side": {
			from:   yamlviewport.ViewModeHunks,
			to:     yamlviewport.ViewModeSideBySide,
			toggle: true,
			keep:   true,
		},
		"toggle side-by-side to full": {
			from:   yamlviewport.ViewModeSideBySide,
			to:     yamlviewport.ViewModeFull,
			toggle: true,
			keep:   true,
		},
		"set full to hunks with diff": {
			from: yamlviewport.ViewModeFull,
			to:   yamlviewport.ViewModeHunks,
			diff: true,
		},
		"toggle full to hunks with diff": {
			from:   yamlviewport.ViewModeFull,
			to:     yamlviewport.ViewModeHunks,
			diff:   true,
			toggle: true,
		},
	}

	doc := func(changed int) string {
		var sb strings.Builder

		for i := range 50 {
			value := "v"
			if i == changed {
				value = "w"
			}

			fmt.Fprintf(&sb, "k%d: %s\n", i, value)
		}

		return sb.String()
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
			m.SetWidth(40)
			m.SetHeight(10)
			m.SetViewMode(tc.from)
			m.AddRevision(niceyaml.NewSourceFromString(doc(-1), niceyaml.WithName("v1")))

			if tc.diff {
				m.AddRevision(niceyaml.NewSourceFromString(doc(45), niceyaml.WithName("v2")))
			}

			m.SetSearchTerm("v")

			for range 20 {
				m.SearchNext()
			}

			require.Equal(t, tc.diff, m.ShowingDiff())
			require.Equal(t, 20, m.SearchIndex())

			topLine := m.TopLine()
			require.Positive(t, topLine)

			if tc.toggle {
				m.ToggleViewMode()
			} else {
				m.SetViewMode(tc.to)
			}

			require.Equal(t, tc.to, m.ViewMode())

			if tc.keep {
				assert.Equal(t, 20, m.SearchIndex())
				assert.Equal(t, topLine, m.TopLine())
			} else {
				assert.Equal(t, 0, m.SearchIndex())
				assert.Equal(t, 0, m.TopLine())
			}
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

	threeLineYAML := `line1: a
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
				assert.Equal(t, 0, m.TopLine())
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
		"AtBottom/GotoBottom": {
			opts:   []yamlviewport.Option{yamlviewport.WithPrinter(testPrinter())},
			yaml:   threeLineYAML,
			width:  80,
			height: 2,
			setup: func(m *yamlviewport.Model) {
				m.GotoBottom()
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.True(t, m.AtBottom())
			},
		},
		"AtBottom/SetHeightReclamps": {
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
				m.SetRevision(niceyaml.NewSourceFromString("line: a\n"))
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
				m.SetRevision(niceyaml.NewSourceFromTokens(tokens.Tokenize(tc.yaml)))
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
				assert.False(t, m.ShowingDiff())
			},
		},
		"AddRevisionFromFile": {
			beforeYAML: beforeYAML,
			afterYAML:  afterYAML,
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, 2, m.RevisionCount())
				assert.Equal(t, 1, m.RevisionIndex()) // At latest (0-indexed).
				assert.True(t, m.ShowingDiff())       // Index > 0 shows a diff.
				assert.Equal(t, "after", m.RevisionName())
				assert.Positive(t, m.TotalLineCount())

				m.GotoRevision(0)
				assert.Equal(t, "before", m.RevisionName())
				assert.False(t, m.ShowingDiff()) // First revision, no diff.
			},
		},
		"SetTokensClearsRevisions": {
			beforeYAML: beforeYAML,
			afterYAML:  afterYAML,
			yaml:       simpleYAML,
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				// SetRevision replaces the history with a single revision.
				assert.Equal(t, 1, m.RevisionCount())
				assert.False(t, m.ShowingDiff())
				assert.Equal(t, 2, m.TotalLineCount())
				assert.Empty(t, m.RevisionName()) // SetRevision uses the Source's name.
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
				m.SetRevision(niceyaml.NewSourceFromString(tc.yaml))
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
		// Shift remaps the vertical wheel alone, so a horizontal wheel
		// with Shift held scrolls as it does without it.
		"Behavior/MouseWheelLeftWithShift": {
			msg:    tea.MouseWheelMsg{Button: tea.MouseWheelLeft, Mod: tea.ModShift},
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
		"Behavior/MouseWheelRightWithShift": {
			msg:    tea.MouseWheelMsg{Button: tea.MouseWheelRight, Mod: tea.ModShift},
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
				second := tokens.Tokenize("line1: modified\nline2: changed")
				m.AddRevision(niceyaml.NewSourceFromTokens(second, niceyaml.WithName("change")))
				m.GotoRevision(0)
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, 1, m.RevisionIndex())
				assert.True(t, m.ShowingDiff())
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
				second := tokens.Tokenize("line1: modified\nline2: changed")
				m.AddRevision(niceyaml.NewSourceFromTokens(second, niceyaml.WithName("change")))

				// Now at index 1 (latest).
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				// After PreviousRevision, we're at index 0 (the revision SetRevision set).
				assert.Equal(t, 0, m.RevisionIndex())
				assert.False(t, m.ShowingDiff())  // First revision, no diff.
				assert.Empty(t, m.RevisionName()) // SetRevision uses the Source's name, which is empty.
			},
		},
		"Behavior/MToggleDiffMode": {
			msg:    tea.KeyPressMsg{Code: 'm'},
			yaml:   verticalYAML,
			width:  80,
			height: 5,
			setup: func(m *yamlviewport.Model) {
				second := tokens.Tokenize("line1: modified\nline2: changed")
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

			tks := tokens.Tokenize(tc.yaml)

			m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
			m.SetWidth(tc.width)
			m.SetHeight(tc.height)
			m.SetRevision(niceyaml.NewSourceFromTokens(tks))

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

	tks := tokens.Tokenize("key: value\nkey2: value2\nkey3: value3")
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
				assert.True(t, km.PreviousRevision.Enabled())
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
			m.SetRevision(lines)

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

	sameTokens1 := tokens.Tokenize(content)
	sameTokens2 := tokens.Tokenize(content)

	differentContent := stringtest.Input(`
		name: different
		value: 20
	`)
	differentTokens := tokens.Tokenize(differentContent)

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
				m.GotoRevision(0)
				assert.Equal(t, 0, m.RevisionIndex())
				assert.True(t, m.AtFirstRevision())
				assert.Equal(t, "first", m.RevisionName())

				m.NextRevision()
				assert.Equal(t, 1, m.RevisionIndex())
				assert.Equal(t, "second", m.RevisionName())
				assert.True(t, m.AtLatestRevision())

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
				m.GotoRevision(0)
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
				assert.True(t, m.AtLatestRevision())
				assert.Equal(t, "different", m.RevisionName())
				assert.True(t, m.ShowingDiff()) // Index > 0 shows a diff.
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
				assert.True(t, m.AtLatestRevision())
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

	rev1Tokens := tokens.Tokenize(rev1YAML)
	rev2Tokens := tokens.Tokenize(rev2YAML)
	rev3Tokens := tokens.Tokenize(rev3YAML)

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
				m.GotoRevision(1)
				m.SetViewMode(yamlviewport.ViewModeHunks)
			},
			width:  80,
			height: 30,
		},
		"AtFirstRevision": {
			setupFunc: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("rev1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("rev2")))
				m.GotoRevision(0)
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
				m.GotoRevision(2)
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
				m.GotoRevision(1)
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
				m.GotoRevision(2)
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
				m.GotoRevision(1)
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
				m.GotoRevision(1)
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
				m.GotoRevision(1)
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
				m.GotoRevision(1)
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

	rev1Tokens := tokens.Tokenize(rev1YAML)
	rev2Tokens := tokens.Tokenize(rev2YAML)
	rev3Tokens := tokens.Tokenize(rev3YAML)

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
				m.GotoRevision(1)
				m.SetViewMode(yamlviewport.ViewModeSideBySide)
			},
			width:  80,
			height: 24,
		},
		"SideBySideNoChanges": {
			// Same content on both sides renders identical panes.
			setupFunc: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("v1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("v2")))
				m.GotoRevision(1)
				m.SetViewMode(yamlviewport.ViewModeSideBySide)
			},
			width:  80,
			height: 24,
		},
		"SideBySideAtOrigin": {
			// At revision 0, both panes show the same content.
			setupFunc: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("v1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("v2")))
				m.GotoRevision(0)
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
				m.GotoRevision(1)
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
				m.GotoRevision(2)
				m.SetDiffMode(yamlviewport.DiffModeOrigin)
				m.SetViewMode(yamlviewport.ViewModeSideBySide)
			},
			width:  80,
			height: 24,
		},
		"SideBySideDiffModeNone": {
			// Side-by-side with no diff mode shows the same content on both panes.
			setupFunc: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("v1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("v2")))
				m.GotoRevision(1)
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
				m.GotoRevision(1)
				m.SetViewMode(yamlviewport.ViewModeSideBySide)
			},
			width:  60,
			height: 16,
		},
		"SideBySideTooNarrow": {
			// Width <=4: paneWidth = (4-3)/2 = 0, so it renders empty
			// without panic.
			setupFunc: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("v1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("v2")))
				m.GotoRevision(1)
				m.SetViewMode(yamlviewport.ViewModeSideBySide)
			},
			width:  4,
			height: 10,
		},
		"SideBySideMinimal": {
			// Width 5: paneWidth = (5-3)/2 = 1, minimum to render content.
			setupFunc: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("v1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("v2")))
				m.GotoRevision(1)
				m.SetViewMode(yamlviewport.ViewModeSideBySide)
			},
			width:  5,
			height: 10,
		},
		"SideBySideMoreDeletions": {
			// With more deletions than insertions, the right pane gets placeholders.
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
				m.GotoRevision(1)
				m.SetViewMode(yamlviewport.ViewModeSideBySide)
			},
			width:  80,
			height: 16,
		},
		"SideBySideMoreInsertions": {
			// With more insertions than deletions, the left pane gets placeholders.
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
				m.GotoRevision(1)
				m.SetViewMode(yamlviewport.ViewModeSideBySide)
			},
			width:  80,
			height: 16,
		},
		"SideBySideSearch": {
			// Verifies search highlights work in side-by-side mode.
			// The XML tags of the test styles take up columns, so this
			// case and the search cases below use a viewport wide enough
			// that highlighted lines fit on one row.
			setupFunc: func(m *yamlviewport.Model) {
				m.SetPrinter(testPrinterWithSearch())
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("v1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("v2")))
				m.GotoRevision(1)
				m.SetViewMode(yamlviewport.ViewModeSideBySide)
				m.SetSearchTerm("modified")
			},
			width:  190,
			height: 24,
		},
		"SideBySideSearchNavigate": {
			// Verifies search navigation moves the selection in side-by-side
			// mode. "key" matches on the changed key1 and key2 lines of both
			// panes. The matches run in row order, and the before pane comes
			// first within a row.
			setupFunc: func(m *yamlviewport.Model) {
				m.SetPrinter(testPrinterWithSearch())
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("v1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("v2")))
				m.GotoRevision(1)
				m.SetViewMode(yamlviewport.ViewModeSideBySide)
				m.SetSearchTerm("key")
				m.SearchNext() // Second match, key1 in the after pane.
				m.SearchNext() // Third match, key2 in the before pane.
			},
			width:  190,
			height: 24,
		},
		"SideBySideSearchDeletedLine": {
			// Verifies SearchSelected appears on deleted line (before pane only).
			setupFunc: func(m *yamlviewport.Model) {
				m.SetPrinter(testPrinterWithSearch())
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("v1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("v2")))
				m.GotoRevision(1)
				m.SetViewMode(yamlviewport.ViewModeSideBySide)
				m.SetSearchTerm("original") // Only in deleted line.
			},
			width:  190,
			height: 24,
		},
		"SideBySideSearchInsertedLine": {
			// Verifies SearchSelected appears on inserted line (after pane only).
			setupFunc: func(m *yamlviewport.Model) {
				m.SetPrinter(testPrinterWithSearch())
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("v1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("v2")))
				m.GotoRevision(1)
				m.SetViewMode(yamlviewport.ViewModeSideBySide)
				m.SetSearchTerm("modified") // Only in inserted line.
			},
			width:  190,
			height: 24,
		},
		"SideBySideSearchBothSidesFirstSelected": {
			// The search term appears on both deleted and inserted lines.
			// The selection sits on the first match, in the before pane.
			setupFunc: func(m *yamlviewport.Model) {
				m.SetPrinter(testPrinterWithSearch())
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("v1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("v2")))
				m.GotoRevision(1)
				m.SetViewMode(yamlviewport.ViewModeSideBySide)
				m.SetSearchTerm("name") // Appears on both deleted and inserted lines.

				// The search selects the first match by default, on the
				// deleted line in the before pane.
			},
			width:  190,
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
				m.GotoRevision(1)
				m.SetViewMode(yamlviewport.ViewModeSideBySide)
			},
			width:  50,
			height: 8,
		},
		"SideBySideSearchBothSidesSecondSelected": {
			// The search term appears on both deleted and inserted lines.
			// The selection sits on the second match, in the after pane.
			setupFunc: func(m *yamlviewport.Model) {
				m.SetPrinter(testPrinterWithSearch())
				m.AddRevision(niceyaml.NewSourceFromTokens(rev1Tokens, niceyaml.WithName("v1")))
				m.AddRevision(niceyaml.NewSourceFromTokens(rev2Tokens, niceyaml.WithName("v2")))
				m.GotoRevision(1)
				m.SetViewMode(yamlviewport.ViewModeSideBySide)
				m.SetSearchTerm("name") // Appears on both deleted and inserted lines.
				m.SearchNext()          // Move to second match (after/inserted line).
			},
			width:  190,
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

	rev1Tokens := tokens.Tokenize(rev1YAML)
	rev2Tokens := tokens.Tokenize(rev2YAML)

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
				m.GotoRevision(1)
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
				m.GotoRevision(1)
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
				m.GotoRevision(1)
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
		"SetViewModeOutOfRange": {
			width:  80,
			height: 5,
			setup: func(m *yamlviewport.Model) {
				var src strings.Builder

				for i := range 50 {
					fmt.Fprintf(&src, "k%d: v\n", i)
				}

				m.SetRevision(niceyaml.NewSourceFromString(src.String()))
				m.SetViewMode(yamlviewport.ViewMode(7))
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()

				// An undefined mode falls back to the default.
				assert.Equal(t, yamlviewport.ViewModeFull, m.ViewMode())

				// The mode already set, or an undefined one that falls back
				// to it, leaves the view where it is.
				m.SetYOffset(10)
				m.SetViewMode(yamlviewport.ViewModeFull)
				assert.Equal(t, 10, m.YOffset())

				m.SetViewMode(yamlviewport.ViewMode(-1))
				assert.Equal(t, yamlviewport.ViewModeFull, m.ViewMode())
				assert.Equal(t, 10, m.YOffset())
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
				m.GotoRevision(1)
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
				m.GotoRevision(1)
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

func TestViewport_CopiesKeepTheirRevisions(t *testing.T) {
	t.Parallel()

	m := yamlviewport.New()
	for _, name := range []string{"a", "b", "c"} {
		m.AddRevision(niceyaml.NewSourceFromString("key: "+name+"\n", niceyaml.WithName(name)))
	}

	// A Model is a value, so a copy must keep its own history when either
	// side adds a revision afterward.
	snap := m
	m.AddRevision(niceyaml.NewSourceFromString("key: d\n", niceyaml.WithName("d")))
	snap.AddRevision(niceyaml.NewSourceFromString("key: e\n", niceyaml.WithName("e")))

	assert.Equal(t, []string{"a", "b", "c", "d"}, m.RevisionNames())
	assert.Equal(t, []string{"a", "b", "c", "e"}, snap.RevisionNames())
}

func TestViewport_ZeroValue(t *testing.T) {
	t.Parallel()

	// A Model not created with New has no printer. It renders nothing and
	// counts no rows instead of panicking, and Update passes messages
	// through.
	var m yamlviewport.Model

	m.SetHeight(3)
	m.SetWidth(20)
	m.SetRevision(niceyaml.NewSourceFromString("key: value\n"))

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
	m.PreviousRevision()
	m.NextRevision()
	m.ToggleDiffMode()
	m.ToggleViewMode()
	m.ToggleWordWrap()
	assert.Equal(t, 0, m.YOffset())
	assert.Equal(t, 0, m.TotalRowCount())
	assert.Empty(t, m.View())

	// Searching needs the searcher New creates, so a zero Model finds no
	// match instead of panicking on the nil one.
	for _, mode := range []yamlviewport.ViewMode{yamlviewport.ViewModeFull, yamlviewport.ViewModeSideBySide} {
		m.SetViewMode(mode)
		m.SetSearchTerm("key")
		m.SearchNext()
		m.SearchPrevious()

		assert.Equal(t, "key", m.SearchTerm())
		assert.Equal(t, 0, m.SearchCount())
		assert.Equal(t, -1, m.SearchIndex())
		assert.Equal(t, 0, m.YOffset())
		assert.Empty(t, m.View())
	}

	m, cmd := m.Update(tea.KeyPressMsg{Code: 'j'})
	assert.Nil(t, cmd)
	assert.Empty(t, m.View())
}

func TestViewport_NilPrinterSelectsDefault(t *testing.T) {
	t.Parallel()

	// SetPrinter stored a nil printer as given, so the view went blank and
	// counted no rows while the search still counted its matches.
	setup := func(m *yamlviewport.Model) {
		m.SetWidth(40)
		m.SetHeight(5)
		m.SetRevision(niceyaml.NewSourceFromString("a: 1\nb: foo\nc: foo\n"))
		m.SetSearchTerm("foo")
	}

	ref := yamlviewport.New(yamlviewport.WithPrinter(printer.New()))
	setup(&ref)

	wantView := ref.View()
	wantRows := ref.TotalRowCount()

	tcs := map[string]struct {
		newModel func() yamlviewport.Model
	}{
		"WithPrinter(nil)": {
			newModel: func() yamlviewport.Model {
				return yamlviewport.New(yamlviewport.WithPrinter(nil))
			},
		},
		"SetPrinter(nil)": {
			newModel: func() yamlviewport.Model {
				m := yamlviewport.New(yamlviewport.WithPrinter(testPrinterWithColors()))
				m.SetPrinter(nil)

				return m
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := tc.newModel()
			setup(&m)

			assert.NotEmpty(t, m.View())
			assert.Equal(t, wantView, m.View())
			assert.Equal(t, wantRows, m.TotalRowCount())
			assert.Equal(t, 2, m.SearchCount())
		})
	}
}

// countingSearcher wraps a [finder.Finder] and counts its Load calls.
type countingSearcher struct {
	finder *finder.Finder
	loads  int
}

func (c *countingSearcher) Load(lines line.Lines) yamlviewport.Index {
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
	m.SetContainerStyle(lipgloss.NewStyle().Padding(1))
	m.SetWordWrap(false)
	m.SetWidth(60)
	m.SetHeight(5)

	assert.Equal(t, 1, searcher.loads)
	assert.Equal(t, 2, m.SearchCount())
	assert.NotEmpty(t, m.View())

	// A revision change rebuilds the view and reloads the searcher.
	m.PreviousRevision()
	assert.Equal(t, 2, searcher.loads)
	assert.Equal(t, 1, m.SearchCount())
}

func TestViewport_UnchangedLayoutKeepsRowCache(t *testing.T) {
	t.Parallel()

	// A program may pass the current width on every window size message,
	// or restyle the frame on every focus change. A change that leaves the
	// printer and the pane width as they are keeps the row counts, so the
	// next read lays out nothing.
	frame := lipgloss.NewStyle().Border(lipgloss.NormalBorder()).Width(30)

	tcs := map[string]struct {
		change func(*yamlviewport.Model, *printer.Printer)
	}{
		"same width": {
			change: func(m *yamlviewport.Model, _ *printer.Printer) { m.SetWidth(m.Width()) },
		},
		"width capped by style": {
			change: func(m *yamlviewport.Model, _ *printer.Printer) { m.SetWidth(50) },
		},
		"same word wrap": {
			change: func(m *yamlviewport.Model, _ *printer.Printer) { m.SetWordWrap(m.WordWrap()) },
		},
		"same container style": {
			change: func(m *yamlviewport.Model, _ *printer.Printer) { m.SetContainerStyle(m.ContainerStyle()) },
		},
		"border color only": {
			change: func(m *yamlviewport.Model, _ *printer.Printer) {
				m.SetContainerStyle(frame.BorderForeground(lipgloss.Color("#ff0000")))
			},
		},
		"same printer": {
			change: func(m *yamlviewport.Model, p *printer.Printer) { m.SetPrinter(p) },
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			renders := 0
			base := lipgloss.NewStyle().Transform(func(s string) string {
				renders++

				return s
			})
			p := printer.New(
				printer.WithStyles(style.New(base)),
				printer.WithContainerStyle(lipgloss.NewStyle()),
				printer.WithGutter(printer.NoGutter),
			)

			var src strings.Builder

			for i := range 50 {
				fmt.Fprintf(&src, "line%d: v\n", i)
			}

			m := yamlviewport.New(
				yamlviewport.WithPrinter(p),
				yamlviewport.WithContainerStyle(frame),
			)
			m.SetWidth(40)
			m.SetHeight(10)
			m.SetRevision(niceyaml.NewSourceFromString(src.String()))
			m.SetYOffset(20)
			m.TotalRowCount()

			renders = 0

			tc.change(&m, p)
			m.TotalRowCount()

			assert.Zero(t, renders, "the row counts should come from the cache")
			assert.Equal(t, 20, m.YOffset())
		})
	}
}

func TestViewport_SideBySideLoadsSearcherOncePerContent(t *testing.T) {
	t.Parallel()

	// Side-by-side search indexes both panes, and like the unified view it
	// loads them once per change of content rather than on every keystroke.
	searcher := &countingSearcher{finder: finder.New()}
	m := yamlviewport.New(
		yamlviewport.WithPrinter(testPrinter()),
		yamlviewport.WithSearcher(searcher),
	)
	m.SetWidth(80)
	m.SetHeight(10)
	m.AddRevision(niceyaml.NewSourceFromString("key: alpha\n", niceyaml.WithName("v1")))
	m.AddRevision(niceyaml.NewSourceFromString("key: alpha\nother: alpha\n", niceyaml.WithName("v2")))
	m.SetViewMode(yamlviewport.ViewModeSideBySide)

	for _, term := range []string{"a", "al", "alp", "alpha"} {
		m.SetSearchTerm(term)
	}

	assert.Equal(t, 2, searcher.loads, "one load per pane")
	assert.Equal(t, 2, m.SearchCount())

	// A revision change rebuilds the view and reloads the searcher: once
	// for the single first revision, then once per pane on the way back.
	m.PreviousRevision()
	m.NextRevision()
	assert.Equal(t, 5, searcher.loads)
}

// countingRevision wraps a [yamlviewport.Revision] and counts its View
// calls.
type countingRevision struct {
	yamlviewport.Revision

	views int
}

func (r *countingRevision) View() *line.View {
	r.views++

	return r.Revision.View()
}

func TestViewport_ViewModeReusesDiff(t *testing.T) {
	t.Parallel()

	docs := []string{
		"a: 1\nb: 2\nc: 3\n",
		"a: 1\nb: 20\nc: 3\n",
		"a: 10\nb: 20\nc: 3\nd: 4\n",
	}

	tests := map[string]struct {
		// Steps that run on a viewport that holds a revision of each doc
		// and shows the diff at revision index 1.
		change func(m *yamlviewport.Model, revs []*countingRevision)
		// Indexes into docs of the revisions the viewport holds after
		// change.
		history []int
		// Whether change reads the view of the revision on display again.
		reread bool
	}{
		"toggle view modes": {
			change: func(m *yamlviewport.Model, _ []*countingRevision) {
				m.ToggleViewMode()
				m.ToggleViewMode()
				m.ToggleViewMode()
			},
			history: []int{0, 1, 2},
		},
		"hunk context": {
			change: func(m *yamlviewport.Model, _ []*countingRevision) {
				m.SetViewMode(yamlviewport.ViewModeHunks)
				m.SetHunkContext(5)
			},
			history: []int{0, 1, 2},
		},
		"revision change": {
			change: func(m *yamlviewport.Model, _ []*countingRevision) {
				m.NextRevision()
				m.PreviousRevision()
			},
			history: []int{0, 1, 2},
			reread:  true,
		},
		"history replaced": {
			// The new history holds another revision at index 1, so the
			// diff at that index compares other content.
			change: func(m *yamlviewport.Model, revs []*countingRevision) {
				m.ClearRevisions()
				m.AddRevision(revs[0])
				m.AddRevision(revs[2])
			},
			history: []int{0, 2},
			reread:  true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
			m.SetWidth(80)
			m.SetHeight(10)

			revs := make([]*countingRevision, len(docs))
			for i, doc := range docs {
				revs[i] = &countingRevision{
					Revision: niceyaml.NewSourceFromString(doc, niceyaml.WithName(fmt.Sprintf("v%d", i+1))),
				}
				m.AddRevision(revs[i])
			}

			m.GotoRevision(1)

			for _, r := range revs {
				r.views = 0
			}

			tc.change(&m, revs)

			shown := revs[tc.history[m.RevisionIndex()]]
			if tc.reread {
				assert.Positive(t, shown.views)
			} else {
				for i, r := range revs {
					assert.Zero(t, r.views, "revision %d", i)
				}
			}

			want := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
			want.SetWidth(80)
			want.SetHeight(10)

			for _, i := range tc.history {
				want.AddRevision(niceyaml.NewSourceFromString(docs[i], niceyaml.WithName(fmt.Sprintf("v%d", i+1))))
			}

			want.GotoRevision(m.RevisionIndex())
			want.SetDiffMode(m.DiffMode())
			want.SetViewMode(m.ViewMode())
			want.SetHunkContext(m.HunkContext())

			assert.Equal(t, want.DiffStats(), m.DiffStats())
			assert.Equal(t, want.View(), m.View())
		})
	}
}

func TestViewport_AddRevisionsDiffsOnce(t *testing.T) {
	t.Parallel()

	m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
	m.SetWidth(80)
	m.SetHeight(10)

	revs := make([]*countingRevision, 5)
	batch := make([]yamlviewport.Revision, len(revs))

	for i := range revs {
		doc := fmt.Sprintf("a: %d\nb: 2\n", i)
		revs[i] = &countingRevision{
			Revision: niceyaml.NewSourceFromString(doc, niceyaml.WithName(fmt.Sprintf("v%d", i+1))),
		}
		batch[i] = revs[i]
	}

	m.AddRevisions(batch...)

	// Only the diff of the last revision against the one before it reads
	// views.
	for i, r := range revs[:3] {
		assert.Zero(t, r.views, "revision %d", i)
	}

	assert.Positive(t, revs[3].views)
	assert.Positive(t, revs[4].views)

	want := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
	want.SetWidth(80)
	want.SetHeight(10)

	for i := range revs {
		doc := fmt.Sprintf("a: %d\nb: 2\n", i)
		want.AddRevision(niceyaml.NewSourceFromString(doc, niceyaml.WithName(fmt.Sprintf("v%d", i+1))))
	}

	assert.Equal(t, want.RevisionNames(), m.RevisionNames())
	assert.Equal(t, want.DiffStats(), m.DiffStats())
	assert.Equal(t, want.View(), m.View())
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

		tks := tokens.Tokenize("key: value\n")
		m.SetRevision(niceyaml.NewSourceFromTokens(tks))

		m.SetSearchTerm("value")

		assert.Equal(t, "value", m.SearchTerm())
		assert.Positive(t, m.SearchCount())
		assert.Equal(t, 1, searcher.loads)
	})

	t.Run("WithFinder searches through the finder", func(t *testing.T) {
		t.Parallel()

		m := yamlviewport.New(
			yamlviewport.WithPrinter(testPrinter()),
			yamlviewport.WithFinder(finder.New(finder.WithNormalizer(nil))),
		)

		m.SetWidth(80)
		m.SetHeight(10)
		m.SetRevision(niceyaml.NewSourceFromString("key: Value\n"))

		// The finder has no normalizer, so the search is case-sensitive,
		// unlike the default finder.
		m.SetSearchTerm("value")
		assert.Equal(t, 0, m.SearchCount())

		m.SetSearchTerm("Value")
		assert.Equal(t, 1, m.SearchCount())
	})

	t.Run("a nil searcher or finder selects the default", func(t *testing.T) {
		t.Parallel()

		// WithFinder wrapped a nil finder in an adapter, which is a non-nil
		// Searcher, so the constructor kept it and the first search
		// dereferenced the finder.
		tcs := map[string]yamlviewport.Option{
			"nil searcher": yamlviewport.WithSearcher(nil),
			"nil finder":   yamlviewport.WithFinder(nil),
		}

		for name, opt := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()), opt)

				m.SetWidth(80)
				m.SetHeight(10)
				m.SetRevision(niceyaml.NewSourceFromString("key: Value\n"))

				// The default finder folds case.
				m.SetSearchTerm("value")
				assert.Equal(t, 1, m.SearchCount())
			})
		}
	})
}

// reverseSearcher wraps a [finder.Finder] in an Index that returns its
// matches in reverse document order.
type reverseSearcher struct {
	finder *finder.Finder
}

func (r reverseSearcher) Load(lines line.Lines) yamlviewport.Index {
	return reverseIndex{index: r.finder.Load(lines)}
}

type reverseIndex struct {
	index *finder.Index
}

func (r reverseIndex) Find(search string) position.Ranges {
	matches := slices.Clone(r.index.Find(search))
	slices.Reverse(matches)

	return matches
}

func TestViewport_SearchOrdersIndexMatches(t *testing.T) {
	t.Parallel()

	var before, after strings.Builder

	for i := range 40 {
		v := "x"
		if i == 0 || i == 39 {
			v = "needle"
		}

		fmt.Fprintf(&before, "k%d: %s\n", i, v)

		if i == 20 {
			v = "y"
		}

		fmt.Fprintf(&after, "k%d: %s\n", i, v)
	}

	// An Index may return matches in any order, and every view mode steps
	// through them from the top of the document.
	tcs := map[string]yamlviewport.ViewMode{
		"full":         yamlviewport.ViewModeFull,
		"side by side": yamlviewport.ViewModeSideBySide,
	}

	for name, mode := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := yamlviewport.New(
				yamlviewport.WithPrinter(testPrinter()),
				yamlviewport.WithSearcher(reverseSearcher{finder: finder.New()}),
			)
			m.SetWidth(80)
			m.SetHeight(5)
			m.AddRevision(niceyaml.NewSourceFromString(before.String(), niceyaml.WithName("v1")))
			m.AddRevision(niceyaml.NewSourceFromString(after.String(), niceyaml.WithName("v2")))
			m.SetViewMode(mode)

			m.SetSearchTerm("needle")
			require.Equal(t, 2, m.SearchCount())
			assert.Equal(t, 0, m.SearchIndex())
			assert.Equal(t, 0, m.YOffset())
		})
	}
}

// nilSearcher is a [yamlviewport.Searcher] whose Load returns a nil Index.
// It counts its Load calls.
type nilSearcher struct {
	loads int
}

func (s *nilSearcher) Load(line.Lines) yamlviewport.Index {
	s.loads++

	return nil
}

func TestViewport_NilIndexFindsNothing(t *testing.T) {
	t.Parallel()

	// A nil Index still counts as loaded, so typing a term loads each pane
	// once rather than on every keystroke.
	tcs := map[string]struct {
		viewMode  yamlviewport.ViewMode
		wantLoads int
	}{
		"unified": {
			viewMode:  yamlviewport.ViewModeFull,
			wantLoads: 1,
		},
		"side by side": {
			viewMode:  yamlviewport.ViewModeSideBySide,
			wantLoads: 2,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			searcher := &nilSearcher{}
			m := yamlviewport.New(
				yamlviewport.WithPrinter(testPrinter()),
				yamlviewport.WithSearcher(searcher),
			)
			m.SetWidth(80)
			m.SetHeight(10)
			m.AddRevision(niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithName("v1")))
			m.AddRevision(niceyaml.NewSourceFromString("a: 2\n", niceyaml.WithName("v2")))
			m.SetViewMode(tc.viewMode)

			assert.NotPanics(t, func() { m.SetSearchTerm("a") })
			assert.Equal(t, 0, m.SearchCount())
			assert.Equal(t, -1, m.SearchIndex())

			for _, term := range []string{"ab", "abc", "abcd"} {
				m.SetSearchTerm(term)
			}

			assert.Equal(t, tc.wantLoads, searcher.loads)
		})
	}
}

func TestViewport_SideBySideSelectedMatchOnInsertedLine(t *testing.T) {
	t.Parallel()

	// The diff pads the left pane opposite an inserted line with an empty
	// line that carries the default flag, the same flag an equal line
	// carries. A match selected on the inserted line belongs to the right
	// pane alone, so only the right pane draws the selected highlight.
	before := niceyaml.NewSourceFromString("a: 1\nb: 2\n", niceyaml.WithName("v1"))
	after := niceyaml.NewSourceFromString("a: 1\nfind: me\nb: 2\n", niceyaml.WithName("v2"))

	const highlight = "<genericHighlight>"

	m := yamlviewport.New(yamlviewport.WithPrinter(testPrinterWithSearch()))
	m.SetWidth(100)
	m.SetHeight(8)
	m.AddRevision(before)
	m.AddRevision(after)
	m.SetViewMode(yamlviewport.ViewModeSideBySide)

	m.SetSearchTerm("find")
	require.Equal(t, 1, m.SearchCount())

	view := m.View()
	require.Equal(t, 1, strings.Count(view, highlight))
	assert.NotContains(t, view, "<genericHighlightDim>")

	// The panes sit either side of a vertical bar, and the highlight is in
	// the right one.
	rows := strings.Split(view, "\n")

	i := slices.IndexFunc(rows, func(row string) bool { return strings.Contains(row, highlight) })
	require.NotEqual(t, -1, i)

	left, right, ok := strings.Cut(rows[i], "│")
	require.True(t, ok)
	assert.NotContains(t, left, highlight)
	assert.Contains(t, right, highlight)
}

func TestViewport_OffsetWithNoContentHeight(t *testing.T) {
	t.Parallel()

	// The offset is the index of the row at the top of the view, and a
	// content area with no height once let it reach the row count itself,
	// one row past the last row of the view.
	var src strings.Builder

	for i := 1; i <= 20; i++ {
		fmt.Fprintf(&src, "line%d: v\n", i)
	}

	tcs := map[string]struct {
		container lipgloss.Style
		height    int
	}{
		"no height":               {container: lipgloss.NewStyle(), height: 0},
		"frame as tall as height": {container: lipgloss.NewStyle().Border(lipgloss.NormalBorder()), height: 2},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := yamlviewport.New(
				yamlviewport.WithPrinter(testPrinter()),
				yamlviewport.WithContainerStyle(tc.container),
			)
			m.SetWidth(40)
			m.SetHeight(tc.height)
			m.SetRevision(niceyaml.NewSourceFromString(src.String()))

			require.Equal(t, 20, m.TotalRowCount())

			m.GotoBottom()
			assert.Equal(t, m.TotalRowCount()-1, m.YOffset())
			assert.True(t, m.AtBottom())

			m.SetYOffset(1000)
			assert.Equal(t, m.TotalRowCount()-1, m.YOffset())
		})
	}
}

func TestViewport_ScrollEdgeCases(t *testing.T) {
	t.Parallel()

	t.Run("scroll down with n=0 does nothing", func(t *testing.T) {
		t.Parallel()

		m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
		m.SetWidth(80)
		m.SetHeight(2)

		// Need more lines than viewport height to enable scrolling.
		tks := tokens.Tokenize("line1: value1\nline2: value2\nline3: value3\nline4: value4\nline5: value5\n")
		m.SetRevision(niceyaml.NewSourceFromTokens(tks))

		m.SetYOffset(1)
		require.False(t, m.AtBottom())
		m.ScrollDown(0)
		assert.Equal(t, 1, m.YOffset())
	})

	t.Run("scroll down with empty lines does nothing", func(t *testing.T) {
		t.Parallel()

		m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
		m.SetWidth(80)
		m.SetHeight(10)
		// No tokens set, so the lines are empty.

		m.ScrollDown(1)
		assert.Equal(t, 0, m.YOffset())
	})

	t.Run("scroll up with n=0 does nothing", func(t *testing.T) {
		t.Parallel()

		m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
		m.SetWidth(80)
		m.SetHeight(2)

		// Need more lines than viewport height to enable scrolling.
		tks := tokens.Tokenize("line1: value1\nline2: value2\nline3: value3\nline4: value4\nline5: value5\n")
		m.SetRevision(niceyaml.NewSourceFromTokens(tks))

		m.SetYOffset(2)
		m.ScrollUp(0)
		assert.Equal(t, 2, m.YOffset())
	})

	t.Run("scroll up with empty lines does nothing", func(t *testing.T) {
		t.Parallel()

		m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
		m.SetWidth(80)
		m.SetHeight(10)
		// No tokens set, so the lines are empty.

		m.ScrollUp(1)
		assert.Equal(t, 0, m.YOffset())
	})

	t.Run("half page at one row of content scrolls one row", func(t *testing.T) {
		t.Parallel()

		m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
		m.SetWidth(80)
		m.SetHeight(1)
		m.SetRevision(niceyaml.NewSourceFromString(
			"line1: v\nline2: v\nline3: v\nline4: v\nline5: v\n",
		))

		m.HalfPageDown()
		assert.Equal(t, 1, m.YOffset())

		m.HalfPageUp()
		assert.Equal(t, 0, m.YOffset())
	})
}

func TestViewport_ScrollByExtremeSteps(t *testing.T) {
	t.Parallel()

	// A step past either end stops at that end, however large the step is.
	tcs := map[string]struct {
		scroll   func(m *yamlviewport.Model)
		vertical bool
		wantEnd  bool
	}{
		"down by MaxInt": {
			scroll:   func(m *yamlviewport.Model) { m.ScrollDown(math.MaxInt) },
			vertical: true,
			wantEnd:  true,
		},
		"up by MinInt": {
			scroll:   func(m *yamlviewport.Model) { m.ScrollUp(math.MinInt) },
			vertical: true,
			wantEnd:  true,
		},
		"up by MaxInt": {
			scroll:   func(m *yamlviewport.Model) { m.ScrollUp(math.MaxInt) },
			vertical: true,
		},
		"down by MinInt": {
			scroll:   func(m *yamlviewport.Model) { m.ScrollDown(math.MinInt) },
			vertical: true,
		},
		"wheel down by MaxInt": {
			scroll: func(m *yamlviewport.Model) {
				m.MouseWheelDelta = math.MaxInt
				*m, _ = m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
			},
			vertical: true,
			wantEnd:  true,
		},
		"right by MaxInt": {
			scroll:  func(m *yamlviewport.Model) { m.ScrollRight(math.MaxInt) },
			wantEnd: true,
		},
		"left by MinInt": {
			scroll:  func(m *yamlviewport.Model) { m.ScrollLeft(math.MinInt) },
			wantEnd: true,
		},
		"left by MaxInt": {
			scroll: func(m *yamlviewport.Model) { m.ScrollLeft(math.MaxInt) },
		},
		"right by MinInt": {
			scroll: func(m *yamlviewport.Model) { m.ScrollRight(math.MinInt) },
		},
		"wheel right by MaxInt": {
			scroll: func(m *yamlviewport.Model) {
				m.SetHorizontalStep(math.MaxInt)

				*m, _ = m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelRight})
			},
			wantEnd: true,
		},
	}

	var src strings.Builder

	src.WriteString("long: " + strings.Repeat("x", 200) + "\n")

	for i := range 50 {
		fmt.Fprintf(&src, "k%d: v\n", i)
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
			m.SetWidth(40)
			m.SetHeight(5)
			m.SetWordWrap(false)
			m.SetRevision(niceyaml.NewSourceFromString(src.String()))
			m.SetYOffset(10)
			m.SetXOffset(10)

			offset := func(m *yamlviewport.Model) int {
				if tc.vertical {
					return m.YOffset()
				}

				return m.XOffset()
			}

			// A copy finds the far end without scrolling m.
			far := m
			far.SetYOffset(math.MaxInt)
			far.SetXOffset(math.MaxInt)
			require.Greater(t, offset(&far), offset(&m))

			want := 0
			if tc.wantEnd {
				want = offset(&far)
			}

			tc.scroll(&m)

			assert.Equal(t, want, offset(&m))
		})
	}
}

func TestViewport_RevisionStateEdgeCases(t *testing.T) {
	t.Parallel()

	t.Run("AtFirstRevision with multiple revisions", func(t *testing.T) {
		t.Parallel()

		m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
		m.SetWidth(80)
		m.SetHeight(10)

		tokens1 := tokens.Tokenize("v1: value1\n")
		tokens2 := tokens.Tokenize("v2: value2\n")

		m.AddRevision(niceyaml.NewSourceFromTokens(tokens1, niceyaml.WithName("rev1")))
		m.AddRevision(niceyaml.NewSourceFromTokens(tokens2, niceyaml.WithName("rev2")))

		// At latest revision (rev2), not at first.
		assert.False(t, m.AtFirstRevision())
		assert.True(t, m.AtLatestRevision())

		// Go to first revision.
		m.GotoRevision(0)
		assert.True(t, m.AtFirstRevision())
		assert.False(t, m.AtLatestRevision())
	})

	t.Run("AtFirstRevision with no revisions", func(t *testing.T) {
		t.Parallel()

		m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
		m.SetWidth(80)
		m.SetHeight(10)

		// With no revisions, both checks return true.
		assert.True(t, m.AtFirstRevision())
		assert.True(t, m.AtLatestRevision())
	})
}

func TestViewport_ToggleWordWrapResetsXOffset(t *testing.T) {
	t.Parallel()

	m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
	m.SetWidth(20)
	m.SetHeight(10)

	tks := tokens.Tokenize("key: very long value that exceeds width\n")
	m.SetRevision(niceyaml.NewSourceFromTokens(tks))

	// Disable wrapping first.
	m.ToggleWordWrap()
	assert.False(t, m.WordWrap())

	// Scroll right.
	m.ScrollRight(5)
	assert.Equal(t, 5, m.XOffset())

	// Toggle back to enable wrapping, which resets xOffset.
	m.ToggleWordWrap()
	assert.True(t, m.WordWrap())
	assert.Equal(t, 0, m.XOffset())
}

func TestViewport_WordWrapDisablesHorizontalScroll(t *testing.T) {
	t.Parallel()

	m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
	m.SetWidth(20)
	m.SetHeight(10)
	m.SetRevision(niceyaml.NewSourceFromString("key: very long value that exceeds the viewport width\n"))
	require.True(t, m.WordWrap())

	before := m.View()

	// Rows that wrap within the width leave nothing to scroll, so the
	// offset stays at 0.
	m.ScrollRight(6)
	m.ScrollRight(6)
	assert.Equal(t, 0, m.XOffset())
	assert.Equal(t, before, m.View())
	assert.InDelta(t, 1.0, m.HorizontalScrollPercent(), 0.01)

	m.SetXOffset(12)
	assert.Equal(t, 0, m.XOffset())

	// Turning wrap off shows the lines from their first column.
	m.SetWordWrap(false)
	assert.Equal(t, 0, m.XOffset())
	assert.Contains(t, m.View(), "key: very long")
}

func TestViewport_WrapKeepsFrameOfOverflowingRow(t *testing.T) {
	t.Parallel()

	// An annotation column past the end of a short line keeps its cell
	// even past the wrap width. The printer moves the message within the
	// width, but the "^" marker stays in that cell, so its row runs past
	// the pane with wrap on. The viewport once passed such a row through
	// uncut, so the content area truncated it, every row of the window
	// lost its right border, and horizontal scrolling stayed pinned at 0
	// with the marker out of reach.
	const width = 40

	tcs := map[string]struct {
		// The columns of the container's right border.
		cols []int
		mode yamlviewport.ViewMode
	}{
		"full": {
			cols: []int{width - 1},
		},
		"side by side": {
			// Two panes of 18 columns either side of a 4-column separator.
			cols: []int{17, width - 1},
			mode: yamlviewport.ViewModeSideBySide,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// The lines fill the pane, so every row of the view is a row
			// of the frame.
			source := niceyaml.NewSourceFromString(
				"a: 1\nk: v\nc: 3\nd: 4\ne: 5\nf: 6\ng: 7\nh: 8\ni: 9\nj: 10\n",
			)
			view := source.View()
			view.Annotate(1, line.Annotation{Content: "BAD", Placement: line.Below, Col: 50})

			p := testPrinterWithLineNumbers().With(
				printer.WithContainerStyle(lipgloss.NewStyle().Border(lipgloss.NormalBorder())),
			)
			m := yamlviewport.New(yamlviewport.WithPrinter(p))
			m.SetWidth(width)
			m.SetHeight(10)
			m.SetViewMode(tc.mode)
			m.SetRevision(yamlviewport.NewRevision(source.Name(), view))
			require.True(t, m.WordWrap())

			assertFrame := func(out string) {
				t.Helper()

				for i, row := range strings.Split(out, "\n") {
					plain := []rune(ansi.Strip(row))
					require.Len(t, plain, width, "row %d: %q", i, row)

					for _, col := range tc.cols {
						assert.Contains(t, "┐│┘", string(plain[col]), "row %d, col %d: %q", i, col, row)
					}
				}
			}

			out := m.View()
			assertFrame(out)
			assert.NotContains(t, out, "^")
			assert.Less(t, m.HorizontalScrollPercent(), 1.0)

			m.SetXOffset(1000)
			assert.Positive(t, m.XOffset())
			assert.InDelta(t, 1.0, m.HorizontalScrollPercent(), 0.01)

			out = m.View()
			assertFrame(out)
			assert.Contains(t, out, "^")

			// Wrapped content fits the width, so moving to a search match
			// returns the view to its first column.
			m.SetSearchTerm("a")
			assert.Equal(t, 0, m.XOffset())
		})
	}
}

func TestViewport_HorizontalScrollReachesEnd(t *testing.T) {
	t.Parallel()

	// A 42-column line and a 63-column one, both ending in END.
	short := "k: " + strings.Repeat("x", 36) + "END\n"
	long := "k: " + strings.Repeat("y", 57) + "END\n"

	// The maximum offset brings the last column of the widest rendered row
	// into view. The row is gutter plus content, the visible width is the
	// pane less the container frame, and in side-by-side mode both panes
	// count.
	tcs := map[string]struct {
		printer *printer.Printer
		setup   func(m *yamlviewport.Model)
		// The text at the end of the widest row, "END" when unset.
		tail    string
		width   int
		wantMax int
	}{
		"line number gutter": {
			printer: testPrinterWithLineNumbers(),
			width:   20,
			setup: func(m *yamlviewport.Model) {
				m.SetRevision(niceyaml.NewSourceFromString(short))
			},
			// Gutter 6 + content 42 - width 20.
			wantMax: 28,
		},
		"line number gutter and border": {
			printer: testPrinterWithLineNumbers().With(
				printer.WithContainerStyle(lipgloss.NewStyle().Border(lipgloss.NormalBorder())),
			),
			width: 20,
			setup: func(m *yamlviewport.Model) {
				m.SetRevision(niceyaml.NewSourceFromString(short))
			},
			// Gutter 6 + content 42 - (width 20 - border 2).
			wantMax: 30,
		},
		"side by side": {
			printer: testPrinter(),
			width:   60,
			setup: func(m *yamlviewport.Model) {
				before := strings.ReplaceAll(short, "x", "z")
				m.AddRevision(niceyaml.NewSourceFromString(before, niceyaml.WithName("v1")))
				m.AddRevision(niceyaml.NewSourceFromString(short, niceyaml.WithName("v2")))
				m.SetViewMode(yamlviewport.ViewModeSideBySide)
			},
			// Gutter 1 + content 42 - pane (60 - 3) / 2.
			wantMax: 15,
		},
		"side by side with a longer right pane": {
			printer: testPrinter(),
			width:   40,
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromString("k: short\n", niceyaml.WithName("v1")))
				m.AddRevision(niceyaml.NewSourceFromString(long, niceyaml.WithName("v2")))
				m.SetViewMode(yamlviewport.ViewModeSideBySide)
			},
			// Gutter 1 + content 63 - pane (40 - 3) / 2.
			wantMax: 46,
		},
		"wide characters": {
			printer: testPrinterWithLineNumbers(),
			width:   21,
			setup: func(m *yamlviewport.Model) {
				m.SetRevision(niceyaml.NewSourceFromString(
					"k: " + strings.Repeat("\u65e5", 20) + "END\n",
				))
			},
			// Gutter 6 + content 46 cells - width 21. Each wide glyph takes
			// two cells, so a rune count leaves END out of reach.
			wantMax: 31,
		},
		"hunk header wider than content": {
			printer: testPrinter(),
			width:   14,
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromString("a: 1\nb: 2\nc: 3\n"))
				m.AddRevision(niceyaml.NewSourceFromString("a: 1\nb: 9\nc: 3\n"))
				m.SetViewMode(yamlviewport.ViewModeHunks)
			},
			// Gutter 1 + hunk header 15 - width 14. The header is an
			// annotation row wider than any line of the hunk.
			tail:    "+1,3 @@",
			wantMax: 2,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := yamlviewport.New(yamlviewport.WithPrinter(tc.printer))
			m.SetWidth(tc.width)
			m.SetHeight(5)
			m.SetWordWrap(false)
			tc.setup(&m)

			assert.InDelta(t, 0.0, m.HorizontalScrollPercent(), 0.01)

			tail := tc.tail
			if tail == "" {
				tail = "END"
			}

			m.SetXOffset(1000)
			assert.Equal(t, tc.wantMax, m.XOffset())
			assert.InDelta(t, 1.0, m.HorizontalScrollPercent(), 0.01)
			assert.Contains(t, m.View(), tail)

			// One column short of the end cuts the last letter.
			m.SetXOffset(tc.wantMax - 1)
			assert.Less(t, m.HorizontalScrollPercent(), 1.0)
			assert.NotContains(t, m.View(), tail)
		})
	}
}

func TestViewport_SetSearchTermEmpty(t *testing.T) {
	t.Parallel()

	m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
	m.SetWidth(80)
	m.SetHeight(10)

	tks := tokens.Tokenize("key: value\n")
	m.SetRevision(niceyaml.NewSourceFromTokens(tks))

	// Set a search term first.
	m.SetSearchTerm("value")
	assert.Equal(t, "value", m.SearchTerm())
	assert.Positive(t, m.SearchCount())

	// Clear with empty string.
	m.SetSearchTerm("")
	assert.Empty(t, m.SearchTerm())
	assert.Equal(t, 0, m.SearchCount())
}

func TestViewport_SideBySidePanesShareGutterWidth(t *testing.T) {
	t.Parallel()

	// The before revision numbers its gutter for five digits and the after
	// revision for two, but the panes cut at one shared horizontal offset, so
	// they need one gutter width. Sized per pane, the narrower gutter runs
	// out first and the panes show different columns of the same line.
	var before strings.Builder

	// A first line long enough to scroll past the widest gutter.
	first := "k0: " + strings.Repeat("0123456789", 4) + "\n"

	before.WriteString(first)

	for i := 1; i < 10005; i++ {
		fmt.Fprintf(&before, "k%d: v\n", i)
	}

	m := yamlviewport.New(yamlviewport.WithPrinter(testPrinterWithLineNumbers()))
	m.SetWidth(60)
	m.SetHeight(5)
	m.SetWordWrap(false)
	m.AddRevision(niceyaml.NewSourceFromString(before.String(), niceyaml.WithName("v1")))
	m.AddRevision(niceyaml.NewSourceFromString(first+"last: v\n", niceyaml.WithName("v2")))
	m.SetViewMode(yamlviewport.ViewModeSideBySide)

	m.SetXOffset(7)
	require.Equal(t, 7, m.XOffset())

	// The first line is equal in both revisions, so both panes show it from
	// the same column.
	row, _, _ := strings.Cut(m.View(), "\n")
	left, right, ok := strings.Cut(ansi.Strip(row), "\u2502")
	require.True(t, ok, "no pane separator in %q", row)

	assert.Equal(t, strings.TrimSpace(left), strings.TrimSpace(right))
	assert.Contains(t, left, "k0: ")
}

// countingGutter renders line numbers and counts the gutters it renders for
// each line number.
type countingGutter struct {
	renders map[int]int
}

func (g *countingGutter) Width(ctx printer.GutterContext) int {
	return printer.LineNumberGutter.Width(ctx)
}

func (g *countingGutter) Render(ctx printer.GutterContext) string {
	g.renders[ctx.Number]++

	return printer.LineNumberGutter.Render(ctx)
}

func TestViewport_SideBySideWithoutDiffPrintsOnce(t *testing.T) {
	t.Parallel()

	// Without a diff both panes show the same content, so the view prints
	// the window once and shows its rows in both panes.
	gutter := &countingGutter{renders: map[int]int{}}
	p := testPrinter().With(printer.WithGutter(gutter))
	m := yamlviewport.New(yamlviewport.WithPrinter(p))
	m.SetWidth(40)
	m.SetHeight(10)
	m.SetViewMode(yamlviewport.ViewModeSideBySide)
	m.SetRevision(niceyaml.NewSourceFromString("a: 1\nb: 2\nc: 3\n"))
	m.TotalRowCount()

	clear(gutter.renders)

	rows := strings.Split(m.View(), "\n")

	for i, want := range []string{"a: 1", "b: 2", "c: 3"} {
		left, right, ok := strings.Cut(ansi.Strip(rows[i]), "│")
		require.True(t, ok, "no pane separator in %q", rows[i])
		assert.Contains(t, left, want)
		assert.Contains(t, right, want)
	}

	for number := 1; number <= 3; number++ {
		assert.Equal(t, 1, gutter.renders[number], "line %d", number)
	}
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
			m.GotoRevision(1)
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

	m.SetRevision(source)
	m.SetSearchTerm("value")
	require.Positive(t, m.SearchCount())

	_ = m.View()

	m.SetSearchTerm("")

	_ = m.View()

	// Search highlighting never reaches a fresh view of the caller's Source.
	assert.Empty(t, source.View().Overlays(0))
}

func TestViewport_RevisionOverPartOfTheSource(t *testing.T) {
	t.Parallel()

	// A view over part of a source keeps the indices of the source, so the
	// viewport windows it by those indices, and a search covers the whole
	// source but keeps the matches on the lines the view holds.
	p := printer.New(
		printer.WithStyles(yamltest.NewXMLStyles(
			yamltest.XMLStyleInclude(kind.GenericHighlight, kind.GenericHighlightDim),
		)),
		printer.WithContainerStyle(lipgloss.NewStyle()),
		printer.WithGutter(printer.LineNumberGutter),
	)

	m := yamlviewport.New(yamlviewport.WithPrinter(p))
	m.SetWidth(80)
	m.SetHeight(2)

	source := niceyaml.NewSourceFromString(stringtest.Input(`
		a: item
		b: two
		c: item
		d: four
		e: item
		f: six
	`), niceyaml.WithName("part"))

	m.SetRevision(yamlviewport.NewRevision(source.Name(), source.View().Slice(position.NewSpan(2, 5))))

	out := m.View()

	assert.Contains(t, out, "3 c: item")
	assert.Contains(t, out, "4 d: four")
	assert.NotContains(t, out, "a: item")
	assert.NotContains(t, out, "e: item")

	m.ScrollDown(1)

	out = m.View()

	assert.Contains(t, out, "4 d: four")
	assert.Contains(t, out, "5 e: item")
	assert.NotContains(t, out, "f: six")

	// The source holds three matches, and the view two of them.
	m.SetSearchTerm("item")

	assert.Equal(t, 2, m.SearchCount())
	assert.Equal(t, 0, m.SearchIndex())

	m.GotoTop()

	out = m.View()

	assert.Contains(t, out, "3 c: <genericHighlight>item</genericHighlight>")

	m.SearchNext()

	out = m.View()

	assert.Contains(t, out, "5 e: <genericHighlight>item</genericHighlight>")
	assert.Equal(t, 1, m.SearchIndex())
}

func TestViewport_RevisionKeepsDecoration(t *testing.T) {
	t.Parallel()

	p := printer.New(
		printer.WithStyles(yamltest.NewXMLStyles(
			yamltest.XMLStyleInclude(kind.GenericHighlight, kind.GenericError),
		)),
		printer.WithContainerStyle(lipgloss.NewStyle()),
		printer.WithGutter(printer.NoGutter),
	)

	m := yamlviewport.New(yamlviewport.WithPrinter(p))
	m.SetWidth(80)
	m.SetHeight(10)

	source := niceyaml.NewSourceFromString("key: value\nother: thing\n", niceyaml.WithName("marked"))
	view := source.View()
	view.Annotate(0, line.Annotation{Content: "note", Placement: line.Below})
	view.AddOverlay(kind.GenericError, position.NewRange(position.New(1, 0), position.New(1, 5)))

	m.SetRevision(yamlviewport.NewRevision(source.Name(), view))
	assert.Equal(t, "marked", m.RevisionName())

	// The marks the caller added show as they are.
	out := m.View()

	assert.Contains(t, out, "^ note")
	assert.Contains(t, out, "<genericError>other</genericError>")

	// Search highlights sit beside the marks rather than replacing them.
	m.SetSearchTerm("value")

	out = m.View()

	assert.Contains(t, out, "<genericHighlight>value</genericHighlight>")
	assert.Contains(t, out, "^ note")
	assert.Contains(t, out, "<genericError>other</genericError>")

	// Clearing the search leaves the marks alone.
	m.ClearSearch()

	out = m.View()

	assert.NotContains(t, out, "genericHighlight")
	assert.Contains(t, out, "^ note")
	assert.Contains(t, out, "<genericError>other</genericError>")

	// The caller's view never changes.
	assert.Empty(t, view.Overlays(0))
	assert.Len(t, view.Overlays(1), 1)
}

func TestViewport_RevisionIgnoresLaterMarks(t *testing.T) {
	t.Parallel()

	// A mark the caller adds after setting the revision shows once the
	// revision is set again, and no search action brings it in sooner.
	tcs := map[string]struct {
		act func(*yamlviewport.Model)
	}{
		"new term": {
			act: func(m *yamlviewport.Model) { m.SetSearchTerm("other") },
		},
		"term without match": {
			act: func(m *yamlviewport.Model) { m.SetSearchTerm("zzz") },
		},
		"same term": {
			act: func(m *yamlviewport.Model) { m.SetSearchTerm("value") },
		},
		"search next": {
			act: func(m *yamlviewport.Model) { m.SearchNext() },
		},
		"search previous": {
			act: func(m *yamlviewport.Model) { m.SearchPrevious() },
		},
		"clear search": {
			act: func(m *yamlviewport.Model) { m.ClearSearch() },
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := printer.New(
				printer.WithStyles(yamltest.NewXMLStyles(
					yamltest.XMLStyleInclude(kind.GenericHighlight),
				)),
				printer.WithContainerStyle(lipgloss.NewStyle()),
				printer.WithGutter(printer.NoGutter),
			)

			m := yamlviewport.New(yamlviewport.WithPrinter(p))
			m.SetWidth(80)
			m.SetHeight(10)

			source := niceyaml.NewSourceFromString("key: value\nother: value\n", niceyaml.WithName(name))
			view := source.View()

			m.SetRevision(yamlviewport.NewRevision(name, view))
			m.SetSearchTerm("value")
			require.Equal(t, 2, m.SearchCount())

			_ = m.View()
			rows := m.TotalRowCount()

			view.Annotate(0, line.Annotation{Content: "late", Placement: line.Below})

			tc.act(&m)

			assert.NotContains(t, m.View(), "^ late")
			assert.Equal(t, rows, m.TotalRowCount())

			m.SetRevision(yamlviewport.NewRevision(name, view))

			assert.Contains(t, m.View(), "^ late")
		})
	}
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
	m.PreviousRevision()
	assert.Equal(t, 2, m.SearchCount())
	assert.Equal(t, 0, m.SearchIndex())
	assert.Equal(t, 20-2, m.YOffset())
	assert.Contains(t, m.View(), "key20: needle")

	m.SearchNext()
	m.GotoRevision(1)
	assert.Equal(t, 0, m.SearchIndex())
	assert.Contains(t, m.View(), "key20: needle")

	// Without a search term, navigation returns to the top.
	m.ClearSearch()
	m.PreviousRevision()
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
				m.SetRevision(niceyaml.NewSourceFromString(doc(true)))
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

func TestViewport_SearchScrollsToWrappedRow(t *testing.T) {
	t.Parallel()

	// A long line wraps to many rows at width 20, and the match sits on the
	// last of them.
	long := func(value string) string {
		return value + ": " + strings.Repeat("word ", 40) + "needle\n"
	}

	// One long line whose match sits in the middle, after the separator.
	separated := func(separator string) string {
		return "key: " + strings.Repeat("a", 40) + separator +
			strings.Repeat("b", 10) + "needle" + strings.Repeat("c", 200) + "\n"
	}

	tcs := map[string]struct {
		setup   func(m *yamlviewport.Model)
		printer *printer.Printer
		// The view row holding the match, or -1 when the offset clamps.
		wantRow int
	}{
		"wrapped line": {
			setup: func(m *yamlviewport.Model) {
				m.SetRevision(niceyaml.NewSourceFromString("a: 1\n" + long("k") + "y: 2\nz: 3\n"))
			},
			wantRow: 1,
		},
		"middle row with line numbers": {
			printer: testPrinterWithLineNumbers(),
			setup: func(m *yamlviewport.Model) {
				m.SetWidth(30)
				m.SetRevision(niceyaml.NewSourceFromString(
					"a: 1\nk: " + strings.Repeat("word ", 20) + "needle " + strings.Repeat("word ", 20) + "\nz: 2\n",
				))
			},
			wantRow: 1,
		},
		"hunk header above the line": {
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromString("k: old\n", niceyaml.WithName("v1")))
				m.AddRevision(niceyaml.NewSourceFromString(long("k"), niceyaml.WithName("v2")))
				m.SetViewMode(yamlviewport.ViewModeHunks)
			},
			wantRow: -1,
		},
		"right pane": {
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromString("k: old\n", niceyaml.WithName("v1")))
				m.AddRevision(niceyaml.NewSourceFromString(long("k"), niceyaml.WithName("v2")))
				m.SetViewMode(yamlviewport.ViewModeSideBySide)
				m.SetWidth(43)
			},
			wantRow: -1,
		},
		// The renderer escapes a control character to a picture, so the row
		// the match lands on is the same as with a printable character in its
		// place. Walking the raw content instead stops at the control
		// character and centers the last row of the line.
		"printable character before the match": {
			setup: func(m *yamlviewport.Model) {
				m.SetRevision(niceyaml.NewSourceFromString(separated("x")))
			},
			wantRow: 1,
		},
		"control character before the match": {
			setup: func(m *yamlviewport.Model) {
				m.SetRevision(niceyaml.NewSourceFromString(separated("\a")))
			},
			wantRow: 1,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := tc.printer
			if p == nil {
				p = testPrinter()
			}

			m := yamlviewport.New(yamlviewport.WithPrinter(p))
			m.SetWidth(20)
			m.SetHeight(4)
			tc.setup(&m)

			m.SetSearchTerm("needle")
			require.Equal(t, 1, m.SearchCount())

			// The view centers the row that holds the match, not the first
			// row of its line.
			assert.Positive(t, m.YOffset())

			rows := strings.Split(m.View(), "\n")
			got := slices.IndexFunc(rows, func(row string) bool { return strings.Contains(row, "needle") })
			require.NotEqual(t, -1, got, "match not on screen:\n%s", m.View())

			if tc.wantRow >= 0 {
				assert.Equal(t, tc.wantRow, got)
			}
		})
	}
}

func TestViewport_ContentChangesScrollToTop(t *testing.T) {
	t.Parallel()

	doc := func(changed bool) string {
		var sb strings.Builder

		for i := range 100 {
			value := "value"
			if i == 60 && changed {
				value = "changed"
			}

			fmt.Fprintf(&sb, "key%d: %s\n", i, value)
		}

		return sb.String()
	}

	tcs := map[string]struct {
		change  func(m *yamlviewport.Model)
		wantTop string
	}{
		"add revision": {
			change: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromString(doc(true), niceyaml.WithName("v2")))
			},
			wantTop: "key0: value",
		},
		"set source": {
			change: func(m *yamlviewport.Model) {
				m.SetRevision(niceyaml.NewSourceFromString(doc(true)))
			},
			wantTop: "key0: value",
		},
		"diff mode": {
			change: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromString(doc(true), niceyaml.WithName("v2")))
				m.SetYOffset(50)
				m.SetDiffMode(yamlviewport.DiffModeNone)
			},
			wantTop: "key0: value",
		},
		"hunks": {
			change: func(m *yamlviewport.Model) {
				m.AddRevision(niceyaml.NewSourceFromString(doc(true), niceyaml.WithName("v2")))
				m.SetYOffset(50)
				m.SetViewMode(yamlviewport.ViewModeHunks)
			},
			wantTop: "@@",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
			m.SetWidth(80)
			m.SetHeight(4)
			m.AddRevision(niceyaml.NewSourceFromString(doc(false), niceyaml.WithName("v1")))
			m.SetYOffset(50)
			require.Equal(t, 50, m.YOffset())

			// The row offset of the old content points at an arbitrary line
			// of the new, so the new content starts at its first row.
			tc.change(&m)

			assert.Equal(t, 0, m.YOffset())

			top, _, _ := strings.Cut(m.View(), "\n")
			assert.Contains(t, top, tc.wantTop)
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

	// A new revision changes the displayed content, so the viewport
	// recomputes the matches against the diff rather than the stale index.
	m.AddRevision(niceyaml.NewSourceFromString("key: alpha\nother: alpha\n", niceyaml.WithName("v2")))
	assert.Equal(t, 2, m.SearchCount())

	m.SetSearchTerm("other")
	assert.Equal(t, 1, m.SearchCount())
}

func TestViewport_NewSearchTermStartsAtFirstMatch(t *testing.T) {
	t.Parallel()

	var sb strings.Builder

	for i := range 40 {
		fmt.Fprintf(&sb, "k%d: needle\n", i)
	}

	m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
	m.SetWidth(80)
	m.SetHeight(5)
	m.SetRevision(niceyaml.NewSourceFromString(sb.String()))

	m.SetSearchTerm("needle")

	for range 7 {
		m.SearchNext()
	}

	require.Equal(t, 7, m.SearchIndex())

	// Setting the same term again keeps the current match and the scroll
	// position, so a parent that pushes the term on every update does not
	// snap the view back to the match.
	m.SetSearchTerm("needle")
	assert.Equal(t, 7, m.SearchIndex())

	m.GotoBottom()

	bottom := m.YOffset()
	require.NotEqual(t, 0, bottom)

	m.SetSearchTerm("needle")
	assert.Equal(t, 7, m.SearchIndex())
	assert.Equal(t, bottom, m.YOffset())

	// Another term starts over at its first match, k1 on line 1, rather than
	// carrying the ordinal of the old term into the new match list.
	m.SetSearchTerm("k1")
	assert.Equal(t, 11, m.SearchCount())
	assert.Equal(t, 0, m.SearchIndex())
	assert.Contains(t, m.View(), "k1: needle")
	assert.NotContains(t, m.View(), "k16: needle")
}

func TestViewport_UnchangedSearchKeepsLayout(t *testing.T) {
	t.Parallel()

	var sb strings.Builder

	for i := range 50 {
		fmt.Fprintf(&sb, "k%d: v%d\n", i, i)
	}

	tcs := map[string]struct {
		setup func(m *yamlviewport.Model)
		act   func(m *yamlviewport.Model)
	}{
		"same term": {
			setup: func(m *yamlviewport.Model) {
				m.SetSearchTerm("v")
				m.SearchNext()
			},
			act: func(m *yamlviewport.Model) {
				m.SetSearchTerm("v")
			},
		},
		"clear without term": {
			setup: func(*yamlviewport.Model) {},
			act: func(m *yamlviewport.Model) {
				m.ClearSearch()
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// The base style counts every piece of text the printer styles,
			// which a layout of the view does for each line.
			renders := 0
			count := lipgloss.NewStyle().Transform(func(s string) string {
				renders++

				return s
			})
			p := printer.New(
				printer.WithStyles(style.New(count)),
				printer.WithContainerStyle(lipgloss.NewStyle()),
				printer.WithGutter(printer.NoGutter),
			)

			m := yamlviewport.New(yamlviewport.WithPrinter(p))
			m.SetWidth(40)
			m.SetHeight(10)
			m.SetRevision(niceyaml.NewSourceFromString(sb.String()))

			tc.setup(&m)
			m.SetYOffset(20)
			m.TotalRowCount()

			index := m.SearchIndex()
			offset := m.YOffset()

			require.Positive(t, renders)

			renders = 0

			// The call changes no match, highlight, or content, so the
			// cached layout still holds and nothing renders again.
			tc.act(&m)
			m.TotalRowCount()

			assert.Zero(t, renders)
			assert.Equal(t, index, m.SearchIndex())
			assert.Equal(t, offset, m.YOffset())
		})
	}
}

func TestViewport_SideBySideSearchOrder(t *testing.T) {
	t.Parallel()

	// Every line changes, so each line holds a deleted match in the left pane
	// and an inserted match at the same position in the right pane.
	doc := func(value string) string {
		var sb strings.Builder

		for i := range 20 {
			fmt.Fprintf(&sb, "k%02d: needle %s\n", i, value)
		}

		return sb.String()
	}

	m := yamlviewport.New(yamlviewport.WithPrinter(testPrinterWithSearch()))
	m.SetWidth(160)
	m.SetHeight(3)
	m.AddRevision(niceyaml.NewSourceFromString(doc("a"), niceyaml.WithName("v1")))
	m.AddRevision(niceyaml.NewSourceFromString(doc("b"), niceyaml.WithName("v2")))
	m.SetViewMode(yamlviewport.ViewModeSideBySide)

	m.SetSearchTerm("needle")
	require.Equal(t, 40, m.SearchCount())

	// Matches step through the panes left, right on each line in turn.
	for i := range m.SearchCount() {
		require.Equal(t, i, m.SearchIndex())

		var selected string

		for row := range strings.SplitSeq(m.View(), "\n") {
			if strings.Contains(row, "<genericHighlight>") {
				selected = row
			}
		}

		require.NotEmpty(t, selected, "match %d", i)

		left, right, ok := strings.Cut(selected, "│")
		require.True(t, ok, "match %d", i)

		assert.Contains(t, selected, fmt.Sprintf("k%02d:", i/2), "match %d", i)

		if i%2 == 0 {
			assert.Contains(t, left, "<genericHighlight>", "match %d", i)
		} else {
			assert.Contains(t, right, "<genericHighlight>", "match %d", i)
		}

		m.SearchNext()
	}
}

func TestViewport_ClearSearchSideBySide(t *testing.T) {
	t.Parallel()

	// The XML tags of the test styles take up columns, so the viewport is
	// wide enough that highlighted lines still fit on one row.
	newModel := func() yamlviewport.Model {
		m := yamlviewport.New(yamlviewport.WithPrinter(testPrinterWithSearch()))
		m.SetWidth(120)
		m.SetHeight(5)
		m.AddRevision(niceyaml.NewSourceFromString("foo: a\nbar: b\n", niceyaml.WithName("v1")))
		m.AddRevision(niceyaml.NewSourceFromString("foo: c\nbar: b\n", niceyaml.WithName("v2")))
		m.SetViewMode(yamlviewport.ViewModeSideBySide)

		return m
	}

	plain := newModel()
	m := newModel()

	m.SetSearchTerm("foo")
	require.Equal(t, 2, m.SearchCount())
	require.NotEqual(t, plain.View(), m.View())

	// Clearing the search removes the highlights from both panes.
	m.ClearSearch()
	assert.Equal(t, 0, m.SearchCount())
	assert.Equal(t, plain.View(), m.View())
}

func TestViewport_SearchScrollsToHorizontalMatch(t *testing.T) {
	t.Parallel()

	// With wrap off, a match past the right edge of the frame scrolls the
	// view horizontally so the match is on screen, centered as the Y
	// offset centers its row. A match whose cells all fit the first
	// screen leaves the offset at 0, even right of center, and so does
	// wrap, which has no horizontal scroll.
	tcs := map[string]struct {
		revisions  []string
		mode       yamlviewport.ViewMode
		width      int
		wrap       bool
		wantScroll bool
	}{
		"match past the right edge": {
			revisions:  []string{"k: " + strings.Repeat("-", 60) + "NEEDLE\nz: 1\n"},
			width:      30,
			wantScroll: true,
		},
		"wide runes before the match": {
			// Each rune before the needle takes two cells, so the cell of
			// the match is twice its column, and an offset counted in
			// runes would leave the needle off screen.
			revisions:  []string{"k: " + strings.Repeat("日本", 15) + "NEEDLE\nz: 1\n"},
			width:      30,
			wantScroll: true,
		},
		"match inside the first screen": {
			revisions:  []string{"k: NEEDLE " + strings.Repeat("-", 60) + "\nz: 1\n"},
			width:      30,
			wantScroll: false,
		},
		"match right of center inside the first screen": {
			revisions:  []string{"key: " + strings.Repeat("-", 20) + "NEEDLE" + strings.Repeat("-", 40) + "\nz: 1\n"},
			width:      40,
			wantScroll: false,
		},
		"match that ends at the edge of the first screen": {
			// The one-cell gutter, "key: ", and the dashes take 34 cells,
			// so the needle ends on the last of the 40.
			revisions:  []string{"key: " + strings.Repeat("-", 28) + "NEEDLE" + strings.Repeat("-", 40) + "\nz: 1\n"},
			width:      40,
			wantScroll: false,
		},
		"match that ends one cell past the first screen": {
			revisions:  []string{"key: " + strings.Repeat("-", 29) + "NEEDLE" + strings.Repeat("-", 40) + "\nz: 1\n"},
			width:      40,
			wantScroll: true,
		},
		"match in the right pane": {
			revisions: []string{
				"k: short\n",
				"k: " + strings.Repeat("-", 60) + "NEEDLE\n",
			},
			mode:       yamlviewport.ViewModeSideBySide,
			width:      60,
			wantScroll: true,
		},
		"wrap on": {
			revisions:  []string{"k: " + strings.Repeat("-", 60) + "NEEDLE\nz: 1\n"},
			width:      30,
			wrap:       true,
			wantScroll: false,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
			m.SetWidth(tc.width)
			m.SetHeight(5)
			m.SetViewMode(tc.mode)
			m.SetWordWrap(tc.wrap)

			for _, rev := range tc.revisions {
				m.AddRevision(niceyaml.NewSourceFromString(rev))
			}

			m.SetSearchTerm("NEEDLE")
			require.Equal(t, 1, m.SearchCount())

			if tc.wantScroll {
				assert.Positive(t, m.XOffset())
			} else {
				assert.Equal(t, 0, m.XOffset())
			}

			assert.Contains(t, m.View(), "NEEDLE")
		})
	}
}

func TestViewport_SearchScrollsToTransformedMatch(t *testing.T) {
	t.Parallel()

	// The highlight styles add brackets around every match, so each match
	// before the selected one pushes it two cells further right than its
	// column in the content. The X offset counts those cells, so the
	// selected match stays on screen with wrap off.
	dim := lipgloss.NewStyle().Transform(func(s string) string { return "[" + s + "]" })
	sel := lipgloss.NewStyle().Transform(func(s string) string { return "{" + s + "}" })
	p := printer.New(
		printer.WithStyles(style.New(lipgloss.NewStyle(),
			style.Set(kind.GenericHighlight, sel),
			style.Set(kind.GenericHighlightDim, dim),
		)),
		printer.WithContainerStyle(lipgloss.NewStyle()),
		printer.WithGutter(printer.NoGutter),
	)

	tcs := map[string]struct {
		next int
	}{
		"first match":  {next: 0},
		"middle match": {next: 12},
		"far match":    {next: 24},
		"last match":   {next: 29},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := yamlviewport.New(yamlviewport.WithPrinter(p))
			m.SetWidth(40)
			m.SetHeight(3)
			m.SetWordWrap(false)
			m.SetRevision(niceyaml.NewSourceFromString("k: " + strings.Repeat("ab ", 30) + "\n"))

			m.SetSearchTerm("ab")
			require.Equal(t, 30, m.SearchCount())

			for range tc.next {
				m.SearchNext()
			}

			assert.Contains(t, ansi.Strip(m.View()), "{ab}")
		})
	}
}

func TestViewport_ContentChangeResetsHorizontalScroll(t *testing.T) {
	t.Parallel()

	// A search match scrolls the view to the right, and content that holds
	// no match starts from the first column again rather than keeping an
	// offset that described other lines.
	m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
	m.SetWidth(30)
	m.SetHeight(5)
	m.SetWordWrap(false)
	m.SetRevision(niceyaml.NewSourceFromString("k: " + strings.Repeat("-", 60) + "NEEDLE\n"))

	m.SetSearchTerm("NEEDLE")
	require.Positive(t, m.XOffset())

	m.SetRevision(niceyaml.NewSourceFromString("k: " + strings.Repeat("-", 60) + "\n"))
	assert.Equal(t, 0, m.XOffset())
	assert.Contains(t, m.View(), "k: ---")
}

func TestViewport_ClipsRowsWiderThanContent(t *testing.T) {
	t.Parallel()

	// The gutter alone fills a viewport of a few columns, so every printed
	// row is wider than the content. The view cuts such a row to the width
	// rather than wrapping it onto a second screen row, which the scroll
	// math does not count. The view keeps one screen row per printed row,
	// so the last rows stay reachable.
	m := yamlviewport.New(yamlviewport.WithPrinter(testPrinterWithLineNumbers()))
	m.SetWidth(6)
	m.SetHeight(4)
	m.AddRevision(niceyaml.NewSourceFromString("a: 1\nb: 2\nc: 3\nd: 4\ne: 5\n"))

	// Every screen row opens with the gutter, which a wrapped row would
	// not, and the last row of the view at the largest offset is a row of
	// the last line.
	gutterRows := func() []string {
		t.Helper()

		rows := strings.Split(m.View(), "\n")
		require.Len(t, rows, 4)

		for _, row := range rows {
			assert.True(t, strings.HasPrefix(row, "   "), "row %q", row)
		}

		return rows
	}

	rows := gutterRows()
	assert.True(t, strings.HasPrefix(rows[0], "   1"), "row %q", rows[0])

	m.SetYOffset(100)

	rows = gutterRows()
	assert.True(t, strings.HasPrefix(rows[len(rows)-1], "   -"), "row %q", rows[len(rows)-1])
}

func TestViewport_UnchangedModeKeepsPosition(t *testing.T) {
	t.Parallel()

	// A mode change that shows the same content leaves the scroll offset
	// and the selected match where they are, as the Model doc promises
	// for anything but a change of content.
	var sb strings.Builder

	for i := range 50 {
		fmt.Fprintf(&sb, "k%d: v\n", i)
	}

	m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
	m.SetWidth(40)
	m.SetHeight(10)
	m.AddRevision(niceyaml.NewSourceFromString(sb.String()))
	m.SetSearchTerm("v")
	m.SearchNext()
	m.SearchNext()
	m.SearchNext()
	m.SetYOffset(30)

	require.Equal(t, 3, m.SearchIndex())
	require.Equal(t, 30, m.YOffset())

	// One revision shows no diff in any diff mode.
	m.ToggleDiffMode()
	assert.Equal(t, 3, m.SearchIndex(), "toggle diff mode")
	assert.Equal(t, 30, m.YOffset(), "toggle diff mode")

	m.SetDiffMode(yamlviewport.DiffModeNone)
	assert.Equal(t, 3, m.SearchIndex(), "set diff mode")
	assert.Equal(t, 30, m.YOffset(), "set diff mode")

	m.SetViewMode(m.ViewMode())
	assert.Equal(t, 3, m.SearchIndex(), "same view mode")
	assert.Equal(t, 30, m.YOffset(), "same view mode")

	m.GotoRevision(m.RevisionIndex())
	assert.Equal(t, 3, m.SearchIndex(), "same revision")
	assert.Equal(t, 30, m.YOffset(), "same revision")

	m.SetHunkContext(m.HunkContext())
	assert.Equal(t, 3, m.SearchIndex(), "same hunk context")
	assert.Equal(t, 30, m.YOffset(), "same hunk context")

	// Without a diff, hunks mode shows the same lines and the hunk context
	// shapes nothing on screen.
	m.SetViewMode(yamlviewport.ViewModeHunks)
	assert.Equal(t, 3, m.SearchIndex(), "hunks mode without diff")
	assert.Equal(t, 30, m.YOffset(), "hunks mode without diff")

	m.SetHunkContext(m.HunkContext() + 2)
	assert.Equal(t, 3, m.SearchIndex(), "hunk context without diff")
	assert.Equal(t, 30, m.YOffset(), "hunk context without diff")
}
