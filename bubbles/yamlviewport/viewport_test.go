package yamlviewport_test

import (
	"os"
	"testing"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/exp/golden"
	"github.com/goccy/go-yaml/lexer"
	"github.com/goccy/go-yaml/parser"
	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	tea "charm.land/bubbletea/v2"

	"github.com/macropower/niceyaml"
	"github.com/macropower/niceyaml/bubbles/yamlviewport"
)

// testPrinter returns a printer without styles for predictable golden output.
func testPrinter() *niceyaml.Printer {
	return niceyaml.NewPrinter(
		niceyaml.WithStyles(niceyaml.Styles{}),
		niceyaml.WithStyle(lipgloss.NewStyle()),
	)
}

// testPrinterWithLineNumbers returns a printer with line numbers.
func testPrinterWithLineNumbers() *niceyaml.Printer {
	return niceyaml.NewPrinter(
		niceyaml.WithStyles(niceyaml.Styles{}),
		niceyaml.WithStyle(lipgloss.NewStyle()),
		niceyaml.WithLineNumbers(),
		niceyaml.WithLineNumberStyle(lipgloss.NewStyle()),
	)
}

// testPrinterWithColors returns a printer with default syntax highlighting.
func testPrinterWithColors() *niceyaml.Printer {
	return niceyaml.NewPrinter(
		niceyaml.WithStyles(niceyaml.DefaultStyles()),
		niceyaml.WithStyle(lipgloss.NewStyle()),
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

	simpleYAML := `key: value
number: 42
bool: true
list:
  - item1
  - item2
nested:
  child: data`

	diffBeforeYAML := `name: original
count: 10
enabled: true
`

	diffAfterYAML := `name: modified
count: 20
enabled: true
new_field: added
`

	wideYAML := `short: x
very_long_key_name_that_requires_horizontal_scrolling: "This is a very long value that extends well beyond the viewport width and requires scrolling to see"
another: y`

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
				yamlviewport.WithPrinter(testPrinter()),
				yamlviewport.WithSearchStyle(lipgloss.NewStyle().Background(lipgloss.Color("11"))),
				yamlviewport.WithSelectedSearchStyle(lipgloss.NewStyle().Background(lipgloss.Color("9"))),
			},
			yaml:   simpleYAML,
			width:  80,
			height: 24,
			setupFunc: func(m *yamlviewport.Model, tokens token.Tokens) {
				finder := niceyaml.NewFinder()
				matches := finder.FindStringsInTokens("item", tokens)
				m.SetSearchMatches(matches)
			},
		},
		"DiffMode": {
			opts: []yamlviewport.Option{yamlviewport.WithPrinter(testPrinter())},
			yaml: diffAfterYAML,
			setupFunc: func(m *yamlviewport.Model, _ token.Tokens) {
				before := lexer.Tokenize(diffBeforeYAML)
				after := lexer.Tokenize(diffAfterYAML)
				m.SetDiff(before, after)
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
				m.SetXOffset(20) // Scroll right.
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
				yamlviewport.WithPrinter(testPrinter()),
				yamlviewport.WithSearchStyle(lipgloss.NewStyle().Background(lipgloss.Color("11"))),
				yamlviewport.WithSelectedSearchStyle(lipgloss.NewStyle().Background(lipgloss.Color("9"))),
			},
			yaml:   simpleYAML,
			width:  80,
			height: 24,
			setupFunc: func(m *yamlviewport.Model, tokens token.Tokens) {
				finder := niceyaml.NewFinder()
				matches := finder.FindStringsInTokens("item", tokens)
				m.SetSearchMatches(matches)
				m.SearchNext() // Move to second match.
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tokens := lexer.Tokenize(tc.yaml)

			m := yamlviewport.New(tc.opts...)
			m.SetWidth(tc.width)
			m.SetHeight(tc.height)
			m.SetTokens(tokens)

			if tc.setupFunc != nil {
				tc.setupFunc(&m, tokens)
			}

			output := m.View()
			golden.RequireEqual(t, output)
		})
	}
}

func TestViewport_Scrolling(t *testing.T) {
	t.Parallel()

	verticalYAML := `line1: a
line2: b
line3: c
line4: d
line5: e
line6: f
line7: g
line8: h
line9: i
line10: j`

	horizontalYAML := `short: x
very_long_line: "This is a very long value that extends beyond the viewport width"
another: y`

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
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.InDelta(t, 0.0, m.HorizontalScrollPercent(), 0.01)
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tokens := lexer.Tokenize(tc.yaml)

			m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
			m.SetWidth(tc.width)
			m.SetHeight(tc.height)
			m.SetTokens(tokens)

			if tc.setup != nil {
				tc.setup(&m)
			}

			tc.test(t, &m)
		})
	}
}

func TestViewport_Search(t *testing.T) {
	t.Parallel()

	yaml := `item1: first
item2: second
other: third
item3: fourth`

	tokens := lexer.Tokenize(yaml)
	finder := niceyaml.NewFinder()
	matches := finder.FindStringsInTokens("item", tokens)
	noMatches := finder.FindStringsInTokens("nonexistent", tokens)

	tcs := map[string]struct {
		test    func(t *testing.T, m *yamlviewport.Model)
		matches []niceyaml.PositionRange
	}{
		"SetSearchMatches": {
			matches: matches,
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, 3, m.SearchCount())
				assert.Equal(t, 0, m.SearchIndex())
			},
		},
		"SearchNext": {
			matches: matches,
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
			matches: matches,
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
			matches: matches,
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				m.ClearSearch()
				assert.Equal(t, 0, m.SearchCount())
				assert.Equal(t, -1, m.SearchIndex())
			},
		},
		"NoMatches": {
			matches: noMatches,
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
			m.SetTokens(tokens)
			m.SetSearchMatches(tc.matches)

			tc.test(t, &m)
		})
	}
}

func TestViewport_DiffMode(t *testing.T) {
	t.Parallel()

	before := `name: original
value: 10`

	after := `name: modified
value: 20
new: added`

	beforeTokens := lexer.Tokenize(before)
	afterTokens := lexer.Tokenize(after)

	tcs := map[string]struct {
		setup func(m *yamlviewport.Model)
		test  func(t *testing.T, m *yamlviewport.Model)
	}{
		"SetDiff": {
			setup: func(m *yamlviewport.Model) {
				m.SetDiff(beforeTokens, afterTokens)
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.True(t, m.IsDiffMode())
			},
		},
		"ClearDiff": {
			setup: func(m *yamlviewport.Model) {
				m.SetDiff(beforeTokens, afterTokens)
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				m.ClearDiff()
				assert.False(t, m.IsDiffMode())
			},
		},
		"SetTokensClearsDiff": {
			setup: func(m *yamlviewport.Model) {
				m.SetDiff(beforeTokens, afterTokens)
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.True(t, m.IsDiffMode())

				m.SetTokens(afterTokens)
				assert.False(t, m.IsDiffMode())
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

	lineCountYAML := `line1: a
line2: b
line3: c
line4: d
line5: e`

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
				m.SetTokens(lexer.Tokenize(tc.yaml))
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

	simpleYAML := `key: value
number: 42`

	beforeYAML := `name: original
count: 10`

	afterYAML := `name: modified
count: 20
new: added`

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
				assert.False(t, m.IsDiffMode())
			},
		},
		"SetFileDiff": {
			beforeYAML: beforeYAML,
			afterYAML:  afterYAML,
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.True(t, m.IsDiffMode())
				// Diff mode should show more lines due to changes.
				assert.Positive(t, m.TotalLineCount())
			},
		},
		"SetFileClearsDiff": {
			beforeYAML: beforeYAML,
			afterYAML:  afterYAML,
			yaml:       simpleYAML,
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				// After SetFile, diff mode should be cleared.
				assert.False(t, m.IsDiffMode())
				assert.Equal(t, 2, m.TotalLineCount())
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
				beforeFile, err := parser.ParseBytes([]byte(tc.beforeYAML), parser.ParseComments)
				require.NoError(t, err)

				afterFile, err := parser.ParseBytes([]byte(tc.afterYAML), parser.ParseComments)
				require.NoError(t, err)
				m.SetFileDiff(beforeFile, afterFile)
			}

			if tc.yaml != "" {
				file, err := parser.ParseBytes([]byte(tc.yaml), parser.ParseComments)
				require.NoError(t, err)
				m.SetFile(file)
			}

			tc.test(t, &m)
		})
	}
}

func TestViewport_Update(t *testing.T) {
	t.Parallel()

	verticalYAML := `line1: a
line2: b
line3: c
line4: d
line5: e
line6: f
line7: g
line8: h
line9: i
line10: j`

	wideYAML := `short: x
very_long_key_name_that_requires_horizontal_scrolling: "This is a very long value that extends well beyond the viewport width and requires scrolling to see"
another: y`

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
			golden: true,
		},
		"KeyLeft": {
			msg:    tea.KeyPressMsg{Code: 'h'},
			yaml:   wideYAML,
			width:  40,
			height: 5,
			setup: func(m *yamlviewport.Model) {
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
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tokens := lexer.Tokenize(tc.yaml)

			m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
			m.SetWidth(tc.width)
			m.SetHeight(tc.height)
			m.SetTokens(tokens)

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

	tokens := lexer.Tokenize("key: value\nkey2: value2\nkey3: value3")

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
			m.SetTokens(tokens)

			if tc.setup != nil {
				tc.setup(&m)
			}

			tc.test(t, &m)
		})
	}
}
