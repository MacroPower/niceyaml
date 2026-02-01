package yamlviewport_test

import (
	"os"
	"testing"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/exp/golden"
	"github.com/goccy/go-yaml/lexer"
	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	tea "charm.land/bubbletea/v2"

	"jacobcolvin.com/niceyaml"
	"jacobcolvin.com/niceyaml/bubbles/yamlviewport"
	"jacobcolvin.com/niceyaml/internal/yamltest"
	"jacobcolvin.com/niceyaml/style"
	"jacobcolvin.com/niceyaml/style/theme"
)

// testPrinter returns a printer without styles or line numbers for predictable golden output.
func testPrinter() *niceyaml.Printer {
	return niceyaml.NewPrinter(
		niceyaml.WithStyles(style.Styles{}),
		niceyaml.WithStyle(lipgloss.NewStyle()),
		niceyaml.WithGutter(niceyaml.DiffGutter()),
	)
}

// testPrinterWithLineNumbers returns a printer with line numbers (DefaultGutter).
func testPrinterWithLineNumbers() *niceyaml.Printer {
	return niceyaml.NewPrinter(
		niceyaml.WithStyles(style.Styles{}),
		niceyaml.WithStyle(lipgloss.NewStyle()),
	)
}

// testPrinterWithColors returns a printer with default syntax highlighting.
func testPrinterWithColors() *niceyaml.Printer {
	return niceyaml.NewPrinter(
		niceyaml.WithStyles(theme.Charm()),
		niceyaml.WithStyle(lipgloss.NewStyle()),
	)
}

// testPrinterWithSearch returns a printer with XML-style search highlights for testing.
func testPrinterWithSearch() *niceyaml.Printer {
	return niceyaml.NewPrinter(
		niceyaml.WithStyles(yamltest.NewXMLStyles(
			yamltest.XMLStyleInclude(style.Search, style.SearchSelected),
		)),
		niceyaml.WithStyle(lipgloss.NewStyle()),
		niceyaml.WithGutter(niceyaml.DiffGutter()),
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

	simpleYAML := yamltest.Input(`
		key: value
		number: 42
		bool: true
		list:
		  - item1
		  - item2
		nested:
		  child: data
	`)

	diffBeforeYAML := yamltest.Input(`
		name: original
		count: 10
		enabled: true
	`)

	diffAfterYAML := yamltest.Input(`
		name: modified
		count: 20
		enabled: true
		new_field: added
	`)

	wideYAML := yamltest.Input(`
		short: x
		very_long_key_name_that_requires_horizontal_scrolling: "This is a very long value that extends well beyond the viewport width and requires scrolling to see"
		another: y
	`)

	tcs := map[string]goldenTest{
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
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tks := lexer.Tokenize(tc.yaml)

			m := yamlviewport.New(tc.opts...)
			m.SetWidth(tc.width)
			m.SetHeight(tc.height)
			m.SetTokens(niceyaml.NewSourceFromTokens(tks))

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

	verticalYAML := yamltest.Input(`
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

	horizontalYAML := yamltest.Input(`
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
			m.SetTokens(niceyaml.NewSourceFromTokens(tks))

			if tc.setup != nil {
				tc.setup(&m)
			}

			tc.test(t, &m)
		})
	}
}

func TestViewport_Search(t *testing.T) {
	t.Parallel()

	yaml := yamltest.Input(`
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
			m.SetTokens(lines)
			m.SetSearchTerm(tc.searchTerm)

			tc.test(t, &m)
		})
	}
}

func TestViewport_Revisions(t *testing.T) {
	t.Parallel()

	rev1 := yamltest.Input(`
		name: original
		value: 10
	`)

	rev2 := yamltest.Input(`
		name: modified
		value: 20
	`)

	rev3 := yamltest.Input(`
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
				m.SetTokens(niceyaml.NewSourceFromTokens(rev3Tokens)) // SetTokens uses Lines' name.
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

	rev1 := yamltest.Input(`
		name: original
		value: 10
	`)

	rev2 := yamltest.Input(`
		name: modified
		value: 20
	`)

	rev3 := yamltest.Input(`
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

	lineCountYAML := yamltest.Input(`
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
				m.SetTokens(niceyaml.NewSourceFromTokens(lexer.Tokenize(tc.yaml)))
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

	simpleYAML := yamltest.Input(`
		key: value
		number: 42
	`)

	beforeYAML := yamltest.Input(`
		name: original
		count: 10
	`)

	afterYAML := yamltest.Input(`
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
				// After SetTokens, revisions should be replaced with single file.
				assert.Equal(t, 1, m.RevisionCount())
				assert.False(t, m.IsShowingDiff())
				assert.Equal(t, 2, m.TotalLineCount())
				assert.Empty(t, m.RevisionName()) // SetTokens uses Source's name.
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
				m.SetTokens(niceyaml.NewSourceFromString(tc.yaml))
			}

			tc.test(t, &m)
		})
	}
}

func TestViewport_Update(t *testing.T) {
	t.Parallel()

	verticalYAML := yamltest.Input(`
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

	wideYAML := yamltest.Input(`
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
				// After PrevRevision, we're at index 0 (the original SetTokens revision).
				assert.Equal(t, 0, m.RevisionIndex())
				assert.False(t, m.IsShowingDiff()) // First revision, no diff.
				assert.Empty(t, m.RevisionName())  // SetTokens uses empty name.
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
			m.SetTokens(niceyaml.NewSourceFromTokens(tks))

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
			m.SetTokens(lines)

			if tc.setup != nil {
				tc.setup(&m)
			}

			tc.test(t, &m)
		})
	}
}

func TestViewport_RevisionDeduplication(t *testing.T) {
	t.Parallel()

	content := yamltest.Input(`
		name: same
		value: 10
	`)

	sameTokens1 := lexer.Tokenize(content)
	sameTokens2 := lexer.Tokenize(content)

	differentContent := yamltest.Input(`
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
	rev1YAML := yamltest.Input(`
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
	rev2YAML := yamltest.Input(`
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
	rev3YAML := yamltest.Input(`
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
	rev1YAML := yamltest.Input(`
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
	rev2YAML := yamltest.Input(`
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
	rev3YAML := yamltest.Input(`
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
				moreDeletionsBefore := yamltest.Input(`
					keep: start
					del1: a
					del2: b
					del3: c
					del4: d
					keep: end
				`)
				moreDeletionsAfter := yamltest.Input(`
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
				moreInsertionsBefore := yamltest.Input(`
					keep: start
					del1: a
					keep: end
				`)
				moreInsertionsAfter := yamltest.Input(`
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

	rev1YAML := yamltest.Input(`
		name: original
		count: 10
		enabled: true
	`)

	rev2YAML := yamltest.Input(`
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
				m.SetYOffset(1)
				m.SetViewMode(yamlviewport.ViewModeHunks)
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()

				output := m.View()
				// Should have content but be scrolled.
				assert.NotEmpty(t, output)
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

func TestViewport_WithFinder(t *testing.T) {
	t.Parallel()

	t.Run("custom finder is used for search", func(t *testing.T) {
		t.Parallel()

		finder := niceyaml.NewFinder()
		m := yamlviewport.New(
			yamlviewport.WithPrinter(testPrinter()),
			yamlviewport.WithFinder(finder),
		)

		m.SetWidth(80)
		m.SetHeight(10)

		tokens := lexer.Tokenize("key: value\n")
		m.SetTokens(niceyaml.NewSourceFromTokens(tokens))

		m.SetSearchTerm("value")

		assert.Equal(t, "value", m.SearchTerm())
		assert.Positive(t, m.SearchCount())
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
		m.SetTokens(niceyaml.NewSourceFromTokens(tokens))

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
		m.SetTokens(niceyaml.NewSourceFromTokens(tokens))

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
	m.SetTokens(niceyaml.NewSourceFromTokens(tokens))

	// Disable wrapping first.
	m.ToggleWordWrap()
	assert.False(t, m.WrapEnabled)

	// Scroll right.
	m.ScrollRight(5)
	assert.Equal(t, 5, m.XOffset())

	// Toggle back to enable wrapping - should reset xOffset.
	m.ToggleWordWrap()
	assert.True(t, m.WrapEnabled)
	assert.Equal(t, 0, m.XOffset())
}

func TestViewport_SetSearchTermEmpty(t *testing.T) {
	t.Parallel()

	m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
	m.SetWidth(80)
	m.SetHeight(10)

	tokens := lexer.Tokenize("key: value\n")
	m.SetTokens(niceyaml.NewSourceFromTokens(tokens))

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
	beforeYAML := yamltest.Input(`
		foo: a
		bar: b
	`)
	afterYAML := yamltest.Input(`
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
