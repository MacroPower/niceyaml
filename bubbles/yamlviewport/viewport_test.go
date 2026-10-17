package yamlviewport_test

import (
	"cmp"
	"fmt"
	"math"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"
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
)

func TestDiffMode_String(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		want string
		mode yamlviewport.DiffMode
	}{
		"adjacent":     {mode: yamlviewport.DiffModeAdjacent, want: "adjacent"},
		"origin":       {mode: yamlviewport.DiffModeOrigin, want: "origin"},
		"none":         {mode: yamlviewport.DiffModeNone, want: "none"},
		"out of range": {mode: yamlviewport.DiffMode(3), want: "DiffMode(3)"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.mode.String())
		})
	}
}

func TestViewMode_String(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		want string
		mode yamlviewport.ViewMode
	}{
		"full":         {mode: yamlviewport.ViewModeFull, want: "full"},
		"hunks":        {mode: yamlviewport.ViewModeHunks, want: "hunks"},
		"side by side": {mode: yamlviewport.ViewModeSideBySide, want: "side-by-side"},
		"out of range": {mode: yamlviewport.ViewMode(-1), want: "ViewMode(-1)"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.mode.String())
		})
	}
}

func TestViewport_SearchDecorationRefreshesRowCounts(t *testing.T) {
	t.Parallel()

	// A highlight style may transform the text it styles and change its
	// width, so the row counts must follow the decoration of the current
	// term and selection rather than the one before it.
	widen := bracketStyle("<<", ">>")
	p := yamltest.NewPlainPrinter(printer.WithStyles(highlightStyles(widen, widen)))

	m := testModel(p, 24, 2)
	m.SetWordWrap(true)
	setRevision(&m, stringtest.LinesLF("k: aaaa aaaa aaaa aaaa", "v: aaaa aaaa aaaa aaaa"))

	_ = m.View()

	before := m.TotalRowCount()

	m.SetSearchTerm("aaaa")

	cached := m.TotalRowCount()
	fresh := remeasured(m)

	assert.Equal(t, fresh.TotalRowCount(), cached)
	assert.Greater(t, cached, before, "the widened highlights should wrap more rows")
}

func TestViewport_SearchNextRefreshesRowCounts(t *testing.T) {
	t.Parallel()

	// Only the selected match widens, so each move of the selection changes
	// the rows and the width of the line it leaves and of the line it
	// reaches. The row counts and the horizontal scroll bound follow both.
	p := yamltest.NewPlainPrinter(printer.WithStyles(highlightStyles(bracketStyle("<<", ">>"), lipgloss.NewStyle())))

	// Line i is 9+i columns wide, or 13+i with the selected match, so the
	// widest row and the lines that wrap change with the selection.
	before, after := termRevisions()

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

			m := testModel(p, tc.width, height)
			m.SetWordWrap(tc.wrap)
			addRevisions(&m, before, after)
			m.SetViewMode(tc.viewMode)
			m.SetSearchTerm("term")
			require.Positive(t, m.SearchCount())

			check := func(step int) {
				t.Helper()

				// The selected match sits on the center row unless the
				// offset stops at either end.
				at := rowIndex(m.View(), "<<")
				require.NotEqual(t, -1, at, "step %d: the selected match is on screen", step)

				if top := m.YOffset(); top > 0 && top < m.TotalRowCount()-height {
					assert.Equal(t, (height-1)/2, at, "step %d", step)
				}

				fresh := remeasured(m)

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

func TestViewport_SearchNextCostIndependentOfContent(t *testing.T) {
	t.Parallel()

	const (
		smallLen = 1000
		largeLen = 20000
		batches  = 20
		runs     = 50
	)

	tcs := map[string]struct {
		viewMode yamlviewport.ViewMode
	}{
		"unified": {
			viewMode: yamlviewport.ViewModeFull,
		},
		"side by side": {
			viewMode: yamlviewport.ViewModeSideBySide,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// The newModel closure returns a Model that shows the diff of two
			// revisions of n lines that each hold a match of the term. The
			// default highlight styles change only color, so no move of the
			// selection changes a row count or width.
			newModel := func(n int) *yamlviewport.Model {
				src := yamltest.GenerateYAML(n)

				m := testModel(printer.New(), 120, 40)
				addRevisions(&m, src, src+"extra: value\n")
				m.SetViewMode(tc.viewMode)
				m.SetSearchTerm("value")
				require.Greater(t, m.SearchCount(), n)

				m.TotalRowCount()

				return &m
			}

			// The moveBytes closure returns the bytes one move of the
			// selection allocates, averaged over a batch of moves.
			moveBytes := func(m *yamlviewport.Model) uint64 {
				var before, after runtime.MemStats

				runtime.ReadMemStats(&before)

				for range runs {
					m.SearchNext()
				}

				runtime.ReadMemStats(&after)

				return (after.TotalAlloc - before.TotalAlloc) / runs
			}

			// Tests running alongside add their own allocations to a batch,
			// so each size keeps its lowest batch. The sizes take turns, so
			// both run beside the same tests.
			smallModel, largeModel := newModel(smallLen), newModel(largeLen)
			small, large := uint64(math.MaxUint64), uint64(math.MaxUint64)

			for range batches {
				small = min(small, moveBytes(smallModel))
				large = min(large, moveBytes(largeModel))
			}

			// A move that copied even one int per line would grow by 8 bytes
			// for each line the larger content adds.
			assert.Less(t, large, small+8*(largeLen-smallLen),
				"move in %d lines allocates %d B, move in %d lines allocates %d B",
				smallLen, small, largeLen, large)
		})
	}
}

func TestViewport_SearchTermRefreshesRowCounts(t *testing.T) {
	t.Parallel()

	// Both highlight styles widen the text they style, so a new term changes
	// the rows and the width of the lines its matches leave and of the lines
	// they reach. The row counts, the horizontal scroll bound, and the
	// render follow every change of term.
	widen := bracketStyle("<<", ">>")
	p := yamltest.NewPlainPrinter(
		printer.WithStyles(highlightStyles(widen, widen)),
		printer.WithContainerStyle(normalBorder().Padding(0, 1)),
	)

	before, after := termRevisions()

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

			m := testModel(p, tc.width, 5)
			m.SetWordWrap(tc.wrap)
			addRevisions(&m, before, after)
			m.SetViewMode(tc.viewMode)

			_ = m.View()

			check := func(step string) {
				t.Helper()

				fresh := remeasured(m)

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
	before := yamltest.GenerateLines(30, "k%[1]d: v%[1]d")
	after := replaceLine(before, 29, "k29: changed")

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

			m := testModel(testPrinterWithSearch(), 160, 5)
			addRevisions(&m, before, after)
			m.SetViewMode(tc.viewMode)
			m.SetSearchTerm("v10\nk11")
			require.Equal(t, 1, m.SearchCount())

			m.SetYOffset(tc.offset)

			top := firstRow(m.View())
			assert.Equal(t, tc.panes, strings.Count(top, tc.want), top)

			fresh := remeasured(m)
			assert.Equal(t, fresh.TotalRowCount(), m.TotalRowCount())
			assert.Equal(t, fresh.View(), m.View())
		})
	}
}

// testPrinter returns a printer without styles, line numbers, or a
// container frame, for predictable golden output. Its gutter marks the
// lines of a diff.
func testPrinter() *printer.Printer {
	return yamltest.NewPlainPrinter(printer.WithGutter(printer.DiffGutter))
}

// testPrinterWithLineNumbers returns [testPrinter] with the default gutter,
// which numbers the lines.
func testPrinterWithLineNumbers() *printer.Printer {
	return testPrinter().With(printer.WithGutter(printer.DefaultGutter))
}

// testPrinterWithColors returns [testPrinterWithLineNumbers] with the
// default syntax highlighting.
func testPrinterWithColors() *printer.Printer {
	return testPrinterWithLineNumbers().With(printer.WithStyles(theme.Charm.Styles()))
}

// testPrinterWithSearch returns [testPrinter] with styles that wrap the
// search highlights in XML tags.
func testPrinterWithSearch() *printer.Printer {
	return testPrinter().With(printer.WithStyles(yamltest.NewXMLStyles(
		yamltest.XMLStyleInclude(kind.GenericHighlightDim, kind.GenericHighlight),
	)))
}

// framedPrinter returns [testPrinter] with the given container style.
//
//nolint:gocritic // hugeParam: value semantics match lipgloss.
func framedPrinter(container lipgloss.Style) *printer.Printer {
	return testPrinter().With(printer.WithContainerStyle(container))
}

// normalBorder returns a style that draws a border one cell wide on every
// side.
func normalBorder() lipgloss.Style {
	return lipgloss.NewStyle().Border(lipgloss.NormalBorder())
}

// bracketStyle returns a style that renders text between left and right, so
// the text takes more cells than its content.
func bracketStyle(left, right string) lipgloss.Style {
	return lipgloss.NewStyle().Transform(func(s string) string {
		return left + s + right
	})
}

// highlightStyles returns styles that render the selected search match with
// selected, every other match with dim, and the rest of the text as it is.
//
//nolint:gocritic // hugeParam: value semantics match lipgloss.
func highlightStyles(selected, dim lipgloss.Style) style.Styles {
	return style.New(lipgloss.NewStyle(),
		style.Set(kind.GenericHighlight, selected),
		style.Set(kind.GenericHighlightDim, dim),
	)
}

// countingStyle returns a style that renders text as it is and adds one to
// n for each piece of text it styles.
func countingStyle(n *int) lipgloss.Style {
	return lipgloss.NewStyle().Transform(func(s string) string {
		*n++

		return s
	})
}

// firstRow returns the first row of a rendered view.
func firstRow(view string) string {
	row, _, _ := strings.Cut(view, "\n")

	return row
}

// lastRow returns the last row of a rendered view.
func lastRow(view string) string {
	return view[strings.LastIndex(view, "\n")+1:]
}

// rowIndex returns the index of the first row of a rendered view that
// contains substr, or -1 when no row does.
func rowIndex(view, substr string) int {
	return slices.IndexFunc(strings.Split(view, "\n"), func(row string) bool {
		return strings.Contains(row, substr)
	})
}

// requireRows returns the rows of a rendered view. The test fails unless the
// view holds n rows.
func requireRows(t *testing.T, view string, n int, msgAndArgs ...any) []string {
	t.Helper()

	rows := strings.Split(view, "\n")
	require.Len(t, rows, n, msgAndArgs...)

	return rows
}

// panes returns the text of a side-by-side row on each side of the pane
// separator, without its styling. The test fails when the row has no
// separator.
func panes(t *testing.T, row string) (string, string) {
	t.Helper()

	left, right, ok := strings.Cut(ansi.Strip(row), "│")
	require.True(t, ok, "no pane separator in %q", row)

	return left, right
}

// selectedPanes returns the panes of the row of a side-by-side view that
// shows the selected search match, as [testPrinterWithSearch] tags it. The
// test fails when no row shows it.
func selectedPanes(t *testing.T, view string) (string, string) {
	t.Helper()

	i := rowIndex(view, "<genericHighlight>")
	require.NotEqual(t, -1, i, "no selected match in:\n%s", view)

	return panes(t, strings.Split(view, "\n")[i])
}

// requireFrame fails the test unless every row of the view of m is as wide
// as m, shows the left side of a border in each of the columns left, and
// shows the right side of one in each of the columns right.
func requireFrame(t *testing.T, m *yamlviewport.Model, left, right []int) {
	t.Helper()

	for i, row := range strings.Split(m.View(), "\n") {
		plain := []rune(ansi.Strip(row))
		require.Len(t, plain, m.Width(), "offset %d, row %d: %q", m.YOffset(), i, row)

		for _, col := range left {
			assert.Contains(t, "┌│└", string(plain[col]), "offset %d, row %d, col %d: %q", m.YOffset(), i, col, row)
		}

		for _, col := range right {
			assert.Contains(t, "┐│┘", string(plain[col]), "offset %d, row %d, col %d: %q", m.YOffset(), i, col, row)
		}
	}
}

// framedRows returns the n rows of the view of m without their styling.
// The test fails unless every row between the top and bottom borders is as
// wide as the top border and shows a side border in each column where the
// top border shows a corner.
func framedRows(t *testing.T, m *yamlviewport.Model, n int) []string {
	t.Helper()

	rows := requireRows(t, ansi.Strip(m.View()), n)
	top := rows[0]

	for i, row := range rows[1 : len(rows)-1] {
		require.Equal(t, ansi.StringWidth(top), ansi.StringWidth(row), "x offset %d, row %d: %q", m.XOffset(), i, row)

		for col, r := range []rune(top) {
			if r == '┌' || r == '┐' {
				assert.Equal(t, "│", printer.Cut(row, col, col+1),
					"x offset %d, row %d, col %d: %q", m.XOffset(), i, col, row)
			}
		}
	}

	return rows
}

// replaceLine returns doc with the line at index i replaced by text.
func replaceLine(doc string, i int, text string) string {
	lines := strings.SplitAfter(doc, "\n")
	lines[i] = text + "\n"

	return strings.Join(lines, "")
}

// termRevisions returns two revisions of 12 lines that each end in "term".
// Line i is 9+i columns wide, and the second revision changes line 3.
func termRevisions() (string, string) {
	var before strings.Builder

	for i := range 12 {
		fmt.Fprintf(&before, "k%d: %s term\n", i, strings.Repeat("a", i))
	}

	return before.String(), replaceLine(before.String(), 3, "k3: changed term")
}

// needleRevisions returns a document of 40 lines that holds "needle" on
// lines 20 and 30, and a second revision that changes line 5 and keeps both
// matches.
func needleRevisions() (string, string) {
	before := yamltest.GenerateLines(40, "key%d: value")
	before = replaceLine(before, 20, "key20: needle")
	before = replaceLine(before, 30, "key30: needle")

	return before, replaceLine(before, 5, "key5: changed")
}

// remeasured returns a copy of m that has dropped every cached row count
// through a width round trip, so it measures every line with the current
// decoration.
//
//nolint:gocritic // hugeParam: the helper changes a copy.
func remeasured(m yamlviewport.Model) yamlviewport.Model {
	width := m.Width()
	m.SetWidth(width + 1)
	m.SetWidth(width)

	return m
}

// maxXOffset returns the horizontal offset at which m stops scrolling
// right.
//
//nolint:gocritic // hugeParam: the helper scrolls a copy.
func maxXOffset(m yamlviewport.Model) int {
	m.SetXOffset(1 << 30)

	return m.XOffset()
}

// testModel returns a model of the given size that prints with p, or with
// [testPrinter] when p is nil.
func testModel(p *printer.Printer, width, height int, opts ...yamlviewport.Option) yamlviewport.Model {
	if p == nil {
		p = testPrinter()
	}

	m := yamlviewport.New(append([]yamlviewport.Option{yamlviewport.WithPrinter(p)}, opts...)...)
	m.SetWidth(width)
	m.SetHeight(height)

	return m
}

// setRevision replaces the history of m with one revision of doc, which
// has no name.
func setRevision(m *yamlviewport.Model, doc string) {
	m.SetRevision(niceyaml.NewSourceFromString(doc))
}

// addRevisions adds a revision of each document to m. It names each one
// after its place in the history, so the first is v1, the second v2, and so
// on.
func addRevisions(m *yamlviewport.Model, docs ...string) {
	for _, doc := range docs {
		name := fmt.Sprintf("v%d", m.RevisionCount()+1)
		m.AddRevision(niceyaml.NewSourceFromString(doc, niceyaml.WithName(name), niceyaml.WithByteLimit(0)))
	}
}

// searchNext moves the selected match of m ahead by n matches.
func searchNext(m *yamlviewport.Model, n int) {
	for range n {
		m.SearchNext()
	}
}

// Documents that several tests share.
var (
	// Ten lines that each fit one row.
	verticalYAML = stringtest.Input(`
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

	// Three lines, the second of which runs past the width of the viewport.
	wideYAML = stringtest.Input(`
		short: x
		very_long_key_name_that_requires_horizontal_scrolling: "This is a very long value that extends well beyond the viewport width and requires scrolling to see"
		another: y
	`)

	// Ten lines, the fifth of which wraps onto several rows of a narrow
	// viewport.
	wrapYAML = stringtest.Input(`
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

	// One line that holds "alpha", and a second revision that adds another.
	alphaYAML      = "key: alpha\n"
	alphaTwiceYAML = stringtest.LinesLF("key: alpha", "other: alpha")

	// Three short lines, and a second revision that changes the middle one.
	threeYAML        = stringtest.LinesLF("a: 1", "b: 2", "c: 3")
	threeChangedYAML = stringtest.LinesLF("a: 1", "b: 9", "c: 3")

	// Revisions the golden tests of the view modes diff. The second changes
	// lines throughout the first.
	modeRev1YAML = stringtest.Input(`
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

	modeRev2YAML = stringtest.Input(`
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

	// The third revision changes more lines and adds a list item.
	modeRev3YAML = stringtest.Input(`
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

	// Revisions the [yamlviewport.Model.HitAt] tests point into. The second
	// line of the first two holds wide characters, and each later revision
	// changes the lines between the first and the last.
	hitV1 = stringtest.LinesLF("a: 1", "b: 日本語 x", "c: 3")
	hitV2 = stringtest.LinesLF("a: 1", "b: 日本 y", "new: line here", "c: 3")
	hitV3 = stringtest.LinesLF("a: 1", "new: line here", "c: 3", "d: 4")
)

func TestViewport_Golden(t *testing.T) {
	t.Parallel()

	type goldenTest struct {
		setupFunc func(m *yamlviewport.Model)
		// The printer of the model, [testPrinter] when nil.
		printer *printer.Printer
		style   lipgloss.Style
		yaml    string
		// The viewport is 80 columns wide and 24 rows tall unless the case
		// sets width or height.
		width  int
		height int
	}

	fullYAML := yamltest.ReadFile(t, "testdata/full.yaml")

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

	tcs := map[string]goldenTest{
		"WrapScrolledToBottom": {
			// Line 5 wraps to several rows. Scrolling by row reaches the
			// last line, which line-based scrolling pushed off the bottom.
			yaml:   wrapYAML,
			width:  30,
			height: 5,
			setupFunc: func(m *yamlviewport.Model) {
				m.GotoBottom()
			},
		},
		"WrapScrolledIntoLine": {
			// An offset inside the wrapped line starts the view on one of its
			// continuation rows.
			yaml:   wrapYAML,
			width:  30,
			height: 5,
			setupFunc: func(m *yamlviewport.Model) {
				m.SetYOffset(6)
			},
		},
		"BasicView": {
			yaml: simpleYAML,
		},
		"WithLineNumbers": {
			printer: testPrinterWithLineNumbers(),
			yaml:    simpleYAML,
		},
		"DefaultColors": {
			printer: testPrinterWithColors(),
			yaml:    simpleYAML,
		},
		"FullYAML": {
			yaml: fullYAML,
		},
		"SearchHighlight": {
			printer: testPrinterWithSearch(),
			yaml:    simpleYAML,
			setupFunc: func(m *yamlviewport.Model) {
				m.SetSearchTerm("item")
			},
		},
		"DiffMode": {
			yaml: diffAfterYAML,
			setupFunc: func(m *yamlviewport.Model) {
				m.ClearRevisions()
				addRevisions(m, diffBeforeYAML, diffAfterYAML)
				m.GotoRevision(1) // Show diff between revision 0 and 1.
			},
		},
		"ScrolledContent": {
			yaml:   fullYAML,
			height: 10,
			setupFunc: func(m *yamlviewport.Model) {
				m.SetYOffset(15) // Scroll down to middle of content.
			},
		},
		"HorizontalScroll": {
			yaml:   wideYAML,
			width:  40,
			height: 10,
			setupFunc: func(m *yamlviewport.Model) {
				m.ToggleWordWrap() // Disable wrap (default is true).
				m.SetXOffset(20)   // Scroll right.
			},
		},
		"HorizontalScrollNoOffset": {
			yaml:   wideYAML,
			width:  40,
			height: 10,
			setupFunc: func(m *yamlviewport.Model) {
				m.ToggleWordWrap() // Disable wrap (default is true).

				// XOffset stays at 0 to verify the viewport truncates the
				// lines instead of wrapping them.
			},
		},
		"EmptyContent": {
			yaml: "",
		},
		"StyledContainer": {
			style: normalBorder().Padding(1),
			yaml:  simpleYAML,
		},
		"SmallViewport": {
			yaml:   simpleYAML,
			width:  20,
			height: 5,
		},
		"SearchNavigateNext": {
			// "i" matches four times, in list, item1, item2, and child, so
			// each navigation case selects a different match.
			printer: testPrinterWithSearch(),
			yaml:    simpleYAML,
			setupFunc: func(m *yamlviewport.Model) {
				m.SetSearchTerm("i")
				m.SearchNext() // Move to the second match, in item1.
			},
		},
		"SearchNoMatches": {
			printer: testPrinterWithSearch(),
			yaml:    simpleYAML,
			setupFunc: func(m *yamlviewport.Model) {
				m.SetSearchTerm("nonexistent")
			},
		},
		"SearchNavigatePrevious": {
			printer: testPrinterWithSearch(),
			yaml:    simpleYAML,
			setupFunc: func(m *yamlviewport.Model) {
				m.SetSearchTerm("i")
				m.SearchPrevious() // Wrap from the first match to the last, in child.
			},
		},
		"SearchWrapAroundNext": {
			printer: testPrinterWithSearch(),
			yaml:    simpleYAML,
			setupFunc: func(m *yamlviewport.Model) {
				m.SetSearchTerm("i")
				m.SearchNext() // Second match, in item1.
				m.SearchNext() // Third match, in item2.
				m.SearchNext() // Fourth match, in child.
				m.SearchNext() // Wrap to the first match, in list.
			},
		},
		"SearchWrapAroundPrevious": {
			printer: testPrinterWithSearch(),
			yaml:    simpleYAML,
			setupFunc: func(m *yamlviewport.Model) {
				m.SetSearchTerm("i")
				m.SearchPrevious() // Wrap to the last match, in child.
				m.SearchPrevious() // Move back to the third match, in item2.
			},
		},
		"SearchMultipleMatchesSameLine": {
			// The XML tags of the test styles take up columns, so the
			// viewport is wide enough that the highlighted line fits on
			// one row.
			printer: testPrinterWithSearch(),
			yaml: stringtest.Input(`
				item: item_value
				another: data
			`),
			width: 120,
			setupFunc: func(m *yamlviewport.Model) {
				m.SetSearchTerm("item") // Matches twice on the same line.
			},
		},
		"SearchClear": {
			printer: testPrinterWithSearch(),
			yaml:    simpleYAML,
			setupFunc: func(m *yamlviewport.Model) {
				m.SetSearchTerm("item")
				m.ClearSearch() // Clearing the search removes the highlights.
			},
		},
		"SearchScrollsToFirstMatch": {
			// With a small viewport, setting a search term scrolls to the first match.
			// "timeout" first appears on line 36 in full.yaml.
			printer: testPrinterWithSearch(),
			yaml:    fullYAML,
			height:  10,
			setupFunc: func(m *yamlviewport.Model) {
				m.SetSearchTerm("timeout") // First match is on line 36.
			},
		},
		"SearchScrollsToNextMatch": {
			// Navigating to the next match scrolls the viewport.
			// "timeout" appears on lines 36 and 41 in full.yaml.
			printer: testPrinterWithSearch(),
			yaml:    fullYAML,
			height:  10,
			setupFunc: func(m *yamlviewport.Model) {
				m.SetSearchTerm("timeout")
				m.SearchNext() // Navigate to second match on line 41.
			},
		},
		"SearchScrollsToPreviousMatch": {
			// Navigating backwards scrolls to the previous match.
			// Start at the second "timeout" match, go back to the first.
			printer: testPrinterWithSearch(),
			yaml:    fullYAML,
			height:  10,
			setupFunc: func(m *yamlviewport.Model) {
				m.SetSearchTerm("timeout")
				m.SearchNext()     // Move to second match.
				m.SearchPrevious() // Move back to first match.
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := testModel(
				tc.printer,
				cmp.Or(tc.width, 80),
				cmp.Or(tc.height, 24),
				yamlviewport.WithContainerStyle(tc.style),
			)
			setRevision(&m, tc.yaml)

			if tc.setupFunc != nil {
				tc.setupFunc(&m)
			}

			golden.RequireEqual(t, m.View())
		})
	}
}

func TestViewport_Scrolling(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		setup func(m *yamlviewport.Model)
		test  func(t *testing.T, m *yamlviewport.Model)
		yaml  string
		// The viewport is 80 columns wide and 5 rows tall unless the case
		// sets width or height.
		width  int
		height int
	}{
		"Vertical/ScrollDown": {
			yaml: verticalYAML,
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
			yaml: verticalYAML,
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
			yaml: verticalYAML,
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				m.PageDown()
				assert.Equal(t, 5, m.YOffset())
			},
		},
		"Vertical/PageUp": {
			yaml: verticalYAML,
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
			yaml: verticalYAML,
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
			yaml: verticalYAML,
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				m.ScrollUp(-3)
				assert.Equal(t, 3, m.YOffset())
			},
		},
		"Vertical/HalfPageDown": {
			yaml:   verticalYAML,
			height: 6,
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				m.HalfPageDown()
				assert.Equal(t, 3, m.YOffset())
			},
		},
		"Vertical/HalfPageUp": {
			yaml:   verticalYAML,
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
			yaml: verticalYAML,
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
			yaml: verticalYAML,
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.InDelta(t, 0.0, m.ScrollPercent(), 0.01)

				m.GotoBottom()
				assert.InDelta(t, 1.0, m.ScrollPercent(), 0.01)
			},
		},
		"Vertical/ScrollDownByZero": {
			yaml:   verticalYAML,
			height: 2,
			setup: func(m *yamlviewport.Model) {
				m.SetYOffset(1)
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				require.False(t, m.AtBottom())
				m.ScrollDown(0)
				assert.Equal(t, 1, m.YOffset())
			},
		},
		"Vertical/ScrollUpByZero": {
			yaml:   verticalYAML,
			height: 2,
			setup: func(m *yamlviewport.Model) {
				m.SetYOffset(2)
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				m.ScrollUp(0)
				assert.Equal(t, 2, m.YOffset())
			},
		},
		"Vertical/ScrollDownWithoutLines": {
			height: 10,
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				m.ScrollDown(1)
				assert.Equal(t, 0, m.YOffset())
			},
		},
		"Vertical/ScrollUpWithoutLines": {
			height: 10,
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				m.ScrollUp(1)
				assert.Equal(t, 0, m.YOffset())
			},
		},
		"Vertical/HalfPageOfOneRow": {
			// Half a page of one row of content still scrolls one row.
			yaml:   verticalYAML,
			height: 1,
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				m.HalfPageDown()
				assert.Equal(t, 1, m.YOffset())

				m.HalfPageUp()
				assert.Equal(t, 0, m.YOffset())
			},
		},
		"Horizontal/ToggleWordWrapResetsXOffset": {
			yaml:   "key: very long value that exceeds width\n",
			width:  20,
			height: 10,
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()

				m.ToggleWordWrap()
				assert.False(t, m.WordWrap())

				m.ScrollRight(5)
				assert.Equal(t, 5, m.XOffset())

				// Turning wrap back on returns the view to its first column.
				m.ToggleWordWrap()
				assert.True(t, m.WordWrap())
				assert.Equal(t, 0, m.XOffset())
			},
		},
		"Horizontal/WordWrapDisablesScroll": {
			yaml:   "key: very long value that exceeds the viewport width\n",
			width:  20,
			height: 10,
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				require.True(t, m.WordWrap())

				before := m.View()

				// Rows that wrap within the width leave nothing to scroll,
				// so the offset stays at 0.
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
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := testModel(testPrinter(), cmp.Or(tc.width, 80), cmp.Or(tc.height, 5))

			// A case without YAML scrolls a model that holds no revision.
			if tc.yaml != "" {
				setRevision(&m, tc.yaml)
			}

			if tc.setup != nil {
				tc.setup(&m)
			}

			tc.test(t, &m)
		})
	}
}

func TestViewport_ScrollingWithoutWrap(t *testing.T) {
	t.Parallel()

	// Each case scrolls wideYAML in a 40 by 10 viewport with word wrap
	// off, so its long line runs past the right edge.
	tcs := map[string]struct {
		test func(t *testing.T, m *yamlviewport.Model)
	}{
		"ScrollRight": {
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, 0, m.XOffset())

				m.ScrollRight(10)
				assert.Equal(t, 10, m.XOffset())
			},
		},
		"ScrollLeft": {
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				m.SetXOffset(20)

				m.ScrollLeft(5)
				assert.Equal(t, 15, m.XOffset())
			},
		},
		"SetHorizontalStep": {
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				m.SetHorizontalStep(10)

				*m, _ = m.Update(tea.KeyPressMsg{Code: 'l'})
				assert.Equal(t, 10, m.XOffset())

				*m, _ = m.Update(tea.KeyPressMsg{Code: 'h'})
				assert.Equal(t, 0, m.XOffset())
			},
		},
		"HorizontalScrollPercent": {
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.InDelta(t, 0.0, m.HorizontalScrollPercent(), 0.01)
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := testModel(testPrinter(), 40, 10)
			setRevision(&m, wideYAML)
			m.SetWordWrap(false)

			tc.test(t, &m)
		})
	}
}

func TestViewport_RowScrolling(t *testing.T) {
	t.Parallel()

	newModel := func(t *testing.T) *yamlviewport.Model {
		t.Helper()

		m := testModel(testPrinter(), 30, 5)
		setRevision(&m, wrapYAML)

		return &m
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

			m := testModel(testPrinter(), 30, 5)
			setRevision(&m, src.String())
			require.Equal(t, 250, m.TotalRowCount())

			m.SetYOffset(tc.offset)
			tc.change(&m)

			assert.Equal(t, tc.wantOffset, m.YOffset())

			if tc.wantTop != "" {
				assert.Contains(t, firstRow(m.View()), tc.wantTop)
			}
		})
	}
}

func TestViewport_LayoutChangesKeepFrameRow(t *testing.T) {
	t.Parallel()

	border := normalBorder()
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
			change:    func(m *yamlviewport.Model) { m.SetPrinter(framedPrinter(border)) },
			want:      0,
			wantTop:   "─",
		},
		"top frame grows": {
			container: border,
			lines:     50,
			height:    5,
			change:    func(m *yamlviewport.Model) { m.SetPrinter(framedPrinter(tall)) },
			want:      0,
			wantTop:   "─",
		},
		"top frame shrinks under its last row": {
			container: padding,
			lines:     50,
			height:    5,
			offset:    1,
			change:    func(m *yamlviewport.Model) { m.SetPrinter(framedPrinter(border)) },
			want:      0,
			wantTop:   "─",
		},
		"bottom padding with the same layout": {
			container: padding,
			lines:     5,
			height:    1,
			bottom:    true,
			change:    func(m *yamlviewport.Model) { m.SetPrinter(framedPrinter(padding)) },
			want:      8,
		},
		"bottom frame taller than height with the same layout": {
			container: tall,
			lines:     10,
			height:    2,
			bottom:    true,
			change:    func(m *yamlviewport.Model) { m.SetPrinter(framedPrinter(tall)) },
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

			m := testModel(framedPrinter(tc.container), 40, tc.height)
			setRevision(&m, yamltest.GenerateYAML(tc.lines))

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
				assert.Contains(t, firstRow(m.View()), tc.wantTop)
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
	m := testModel(testPrinter(), 40, 13)
	m.SetContainerStyle(lipgloss.NewStyle().PaddingTop(3))
	setRevision(&m, yamltest.GenerateLines(30, "k%02d: word word word word"))
	m.GotoBottom()
	require.Equal(t, 20, m.YOffset())

	m.SetContainerStyle(lipgloss.NewStyle().PaddingLeft(25))

	assert.Equal(t, 40, m.YOffset())

	assert.Contains(t, firstRow(m.View()), "k20:")
}

func TestViewport_ResizeClampsOffsetAtOnce(t *testing.T) {
	t.Parallel()

	// Each resize clamps the offset to the content area it leaves, so a
	// larger area between two resizes lowers the offset whether or not
	// anything reads it in between.
	src := yamltest.GenerateYAML(30)

	// A top padding of 90 rows leaves a content area 10 rows tall in a
	// viewport 100 rows tall, and keeps the content width.
	padded := lipgloss.NewStyle().PaddingTop(90)

	// A border leaves a content area 10 rows tall in a viewport 12 rows
	// tall, and narrows the content width.
	bordered := normalBorder()

	tcs := map[string]struct {
		style  lipgloss.Style
		resize func(m *yamlviewport.Model)
		height int
		want   int
	}{
		"taller": {
			height: 10,
			resize: func(m *yamlviewport.Model) {
				m.SetHeight(100)
				m.SetHeight(10)
			},
			want: 0,
		},
		"taller with a read between": {
			height: 10,
			resize: func(m *yamlviewport.Model) {
				m.SetHeight(100)
				m.AtTop()
				m.SetHeight(10)
			},
			want: 0,
		},
		"taller with a view between": {
			height: 10,
			resize: func(m *yamlviewport.Model) {
				m.SetHeight(100)

				_ = m.View()

				m.SetHeight(10)
			},
			want: 0,
		},
		"partly taller": {
			height: 10,
			resize: func(m *yamlviewport.Model) {
				m.SetHeight(15)
				m.SetHeight(10)
			},
			want: 15,
		},
		"taller at a new width": {
			height: 10,
			resize: func(m *yamlviewport.Model) {
				m.SetWidth(70)
				m.SetHeight(100)
				m.SetHeight(10)
			},
			want: 0,
		},
		"taller at a new width with a read between": {
			height: 10,
			resize: func(m *yamlviewport.Model) {
				m.SetWidth(70)
				m.SetHeight(100)
				m.AtTop()
				m.SetHeight(10)
			},
			want: 0,
		},
		"container frame shrinks": {
			style:  padded,
			height: 100,
			resize: func(m *yamlviewport.Model) {
				m.SetContainerStyle(lipgloss.NewStyle())
				m.SetContainerStyle(padded)
			},
			want: 0,
		},
		"container frame shrinks with a read between": {
			style:  padded,
			height: 100,
			resize: func(m *yamlviewport.Model) {
				m.SetContainerStyle(lipgloss.NewStyle())
				m.AtTop()
				m.SetContainerStyle(padded)
			},
			want: 0,
		},
		"container border removed and restored": {
			style:  bordered,
			height: 12,
			resize: func(m *yamlviewport.Model) {
				m.SetContainerStyle(lipgloss.NewStyle())
				m.SetContainerStyle(bordered)
			},
			want: 18,
		},
		"container border removed and restored with a read between": {
			style:  bordered,
			height: 12,
			resize: func(m *yamlviewport.Model) {
				m.SetContainerStyle(lipgloss.NewStyle())
				m.AtTop()
				m.SetContainerStyle(bordered)
			},
			want: 18,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := testModel(testPrinter(), 80, tc.height, yamlviewport.WithContainerStyle(tc.style))
			setRevision(&m, src)
			m.SetYOffset(20)
			require.Equal(t, 20, m.YOffset())

			tc.resize(&m)

			assert.Equal(t, tc.want, m.YOffset())
		})
	}
}

func TestViewport_ScrollBeforeSize(t *testing.T) {
	t.Parallel()

	// A program sets the search term or reveals a position before its
	// first window size message. The scroll waits for the size, so the
	// view matches one that got its size first. The target sits on the
	// center row, and with wrap off the view scrolls sideways to it.
	const width, height = 30, 10

	plain := yamltest.GenerateLines(60, "key%d: value")
	wordy := yamltest.GenerateLines(60, "k%d: "+strings.TrimSpace(strings.Repeat("word ", 8)))
	deepLine := "k30: " + strings.Repeat("word ", 30) + "needle " + strings.Repeat("word ", 30)

	needle := replaceLine(plain, 40, "key40: needle")
	wrapped := replaceLine(wordy, 40, "needle: here")
	deep := replaceLine(wordy, 30, deepLine)

	search := func(_ *testing.T, m *yamlviewport.Model) { m.SetSearchTerm("needle") }
	clearSearch := func(m *yamlviewport.Model) { m.ClearSearch() }
	reveal := func(line, col int, want bool) func(t *testing.T, m *yamlviewport.Model) {
		return func(t *testing.T, m *yamlviewport.Model) {
			t.Helper()

			hit := yamlviewport.Hit{Revision: m.RevisionIndex(), Position: position.New(line, col)}
			require.Equal(t, want, m.Reveal(hit))
		}
	}

	tcs := map[string]struct {
		// Sets the content and the modes.
		setup  func(m *yamlviewport.Model)
		scroll func(t *testing.T, m *yamlviewport.Model)
		// Changes the layout. The Model that gets its size last changes it
		// after scroll, and the one that gets its size first before it.
		layout func(m *yamlviewport.Model)
		// Gives the content area room. The default sets the width and then
		// the height.
		size func(m *yamlviewport.Model)
		// Runs on both Models after the size. A search scroll that still
		// waits finds no match after ClearSearch and leaves the view at the
		// top, so ClearSearch here shows whether the size ran the scroll.
		after func(m *yamlviewport.Model)
		// Reports that the center row shows "needle".
		centered bool
	}{
		"search": {
			setup:    func(m *yamlviewport.Model) { setRevision(m, needle) },
			scroll:   search,
			centered: true,
		},
		"search in wrapped lines": {
			setup:    func(m *yamlviewport.Model) { setRevision(m, wrapped) },
			scroll:   search,
			centered: true,
		},
		"search deep in a wrapped line": {
			setup:    func(m *yamlviewport.Model) { setRevision(m, deep) },
			scroll:   search,
			centered: true,
		},
		"search past the right edge": {
			setup: func(m *yamlviewport.Model) {
				m.SetWordWrap(false)
				setRevision(m, replaceLine(plain, 40, "key40: "+strings.Repeat("-", 60)+"needle"))
			},
			scroll:   search,
			centered: true,
		},
		"search in the right pane": {
			setup: func(m *yamlviewport.Model) {
				addRevisions(m, plain, needle)
				m.SetViewMode(yamlviewport.ViewModeSideBySide)
			},
			scroll: search,
			size: func(m *yamlviewport.Model) {
				m.SetWidth(2*width + 1)
				m.SetHeight(height)
			},
			centered: true,
		},
		"next match": {
			setup:    func(m *yamlviewport.Model) { setRevision(m, replaceLine(needle, 20, "key20: needle")) },
			scroll:   func(_ *testing.T, m *yamlviewport.Model) { m.SetSearchTerm("needle"); m.SearchNext() },
			centered: true,
		},
		"reveal": {
			setup:    func(m *yamlviewport.Model) { setRevision(m, needle) },
			scroll:   reveal(40, 0, true),
			centered: true,
		},
		"reveal deep in a wrapped line": {
			setup:    func(m *yamlviewport.Model) { setRevision(m, deep) },
			scroll:   reveal(30, strings.Index(deepLine, "needle"), true),
			centered: true,
		},
		"reveal past the last line": {
			setup:  func(m *yamlviewport.Model) { setRevision(m, needle) },
			scroll: reveal(60, 0, false),
		},
		"wrap turned off before the size": {
			setup:    func(m *yamlviewport.Model) { setRevision(m, deep) },
			scroll:   search,
			layout:   func(m *yamlviewport.Model) { m.SetWordWrap(false) },
			centered: true,
		},
		"line numbers before the size": {
			setup:    func(m *yamlviewport.Model) { setRevision(m, wrapped) },
			scroll:   search,
			layout:   func(m *yamlviewport.Model) { m.SetPrinter(testPrinterWithLineNumbers()) },
			centered: true,
		},
		"height before width": {
			setup:  func(m *yamlviewport.Model) { setRevision(m, wrapped) },
			scroll: search,
			size: func(m *yamlviewport.Model) {
				m.SetHeight(height)
				m.SetWidth(width)
			},
			after:    clearSearch,
			centered: true,
		},
		"view mode that gives the pane room": {
			// Side by side, a width of 4 gives each pane 0 columns beside
			// the separator. The full view then gives its one pane room.
			setup: func(m *yamlviewport.Model) {
				setRevision(m, needle)
				m.SetViewMode(yamlviewport.ViewModeSideBySide)
			},
			scroll: search,
			size: func(m *yamlviewport.Model) {
				m.SetWidth(4)
				m.SetHeight(height)
				m.SetViewMode(yamlviewport.ViewModeFull)
			},
			after: clearSearch,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			layout := func(m *yamlviewport.Model) {
				if tc.layout != nil {
					tc.layout(m)
				}
			}

			size := func(m *yamlviewport.Model) {
				m.SetWidth(width)
				m.SetHeight(height)
			}
			if tc.size != nil {
				size = tc.size
			}

			after := func(m *yamlviewport.Model) {
				if tc.after != nil {
					tc.after(m)
				}
			}

			want := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
			tc.setup(&want)
			layout(&want)
			size(&want)
			tc.scroll(t, &want)
			after(&want)

			got := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
			tc.setup(&got)
			tc.scroll(t, &got)
			layout(&got)
			size(&got)
			after(&got)

			assert.Equal(t, want.YOffset(), got.YOffset())
			assert.Equal(t, want.XOffset(), got.XOffset())

			view := got.View()
			assert.Equal(t, want.View(), view)

			if tc.centered {
				assert.Equal(t, (height-1)/2, rowIndex(view, "needle"), screenMessage(&got))
			}
		})
	}
}

func TestViewport_ScrollBeforeSizeWaits(t *testing.T) {
	t.Parallel()

	// A scroll that waits for the size waits until the size comes. A
	// later scroll takes its place, and an explicit offset or a change of
	// content drops it. A search scrolls to the match that is current
	// when the size comes, so a search left without a match stays at the
	// top. The size runs the scroll in either order of width and height,
	// so a step after the size starts from the match. Every line takes one
	// row, so the offset that centers line n is n-4.
	plain := yamltest.GenerateLines(60, "key%d: value")
	needle := replaceLine(plain, 40, "key40: needle")

	search := func(m *yamlviewport.Model) { m.SetSearchTerm("needle") }

	// Each case checks the offset, which shows whether Reveal found the
	// line.
	reveal := func(m *yamlviewport.Model) { m.Reveal(yamlviewport.Hit{Position: position.New(50, 0)}) }

	tcs := map[string]struct {
		// Run before the size.
		steps []func(m *yamlviewport.Model)
		// Run after the size.
		after []func(m *yamlviewport.Model)
		want  int
	}{
		"search": {
			steps: []func(m *yamlviewport.Model){search},
			want:  36,
		},
		"search cleared after the size": {
			steps: []func(m *yamlviewport.Model){search},
			after: []func(m *yamlviewport.Model){func(m *yamlviewport.Model) { m.ClearSearch() }},
			want:  36,
		},
		"term without a match after the size": {
			steps: []func(m *yamlviewport.Model){search},
			after: []func(m *yamlviewport.Model){func(m *yamlviewport.Model) { m.SetSearchTerm("absent") }},
			want:  36,
		},
		"horizontal offset set after the size": {
			steps: []func(m *yamlviewport.Model){search},
			after: []func(m *yamlviewport.Model){func(m *yamlviewport.Model) { m.SetXOffset(0) }},
			want:  36,
		},
		"offset set": {
			steps: []func(m *yamlviewport.Model){search, func(m *yamlviewport.Model) { m.SetYOffset(5) }},
			want:  5,
		},
		"horizontal offset set": {
			steps: []func(m *yamlviewport.Model){search, func(m *yamlviewport.Model) { m.SetXOffset(0) }},
			want:  0,
		},
		"scroll down": {
			steps: []func(m *yamlviewport.Model){search, func(m *yamlviewport.Model) { m.ScrollDown(3) }},
			want:  3,
		},
		"go to top": {
			steps: []func(m *yamlviewport.Model){search, func(m *yamlviewport.Model) { m.GotoTop() }},
			want:  0,
		},
		"search cleared": {
			steps: []func(m *yamlviewport.Model){search, func(m *yamlviewport.Model) { m.ClearSearch() }},
			want:  0,
		},
		"term without a match": {
			steps: []func(m *yamlviewport.Model){search, func(m *yamlviewport.Model) { m.SetSearchTerm("absent") }},
			want:  0,
		},
		"content without a match": {
			steps: []func(m *yamlviewport.Model){search, func(m *yamlviewport.Model) { setRevision(m, plain) }},
			want:  0,
		},
		"content with a match on another line": {
			steps: []func(m *yamlviewport.Model){search, func(m *yamlviewport.Model) {
				setRevision(m, replaceLine(plain, 20, "key20: needle"))
			}},
			want: 16,
		},
		"reveal": {
			steps: []func(m *yamlviewport.Model){reveal},
			want:  46,
		},
		"reveal then content": {
			steps: []func(m *yamlviewport.Model){reveal, func(m *yamlviewport.Model) { setRevision(m, plain) }},
			want:  0,
		},
		"reveal then term without a match": {
			steps: []func(m *yamlviewport.Model){reveal, func(m *yamlviewport.Model) { m.SetSearchTerm("absent") }},
			want:  46,
		},
		"search then reveal then search cleared": {
			steps: []func(m *yamlviewport.Model){search, reveal, func(m *yamlviewport.Model) { m.ClearSearch() }},
			want:  46,
		},
		"reveal then search": {
			steps: []func(m *yamlviewport.Model){reveal, search},
			want:  36,
		},
	}

	sizes := map[string]func(m *yamlviewport.Model){
		"width first": func(m *yamlviewport.Model) {
			m.SetWidth(30)
			m.SetHeight(10)
		},
		"height first": func(m *yamlviewport.Model) {
			m.SetHeight(10)
			m.SetWidth(30)
		},
	}

	for name, tc := range tcs {
		for order, size := range sizes {
			t.Run(name+", "+order, func(t *testing.T) {
				t.Parallel()

				m := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
				setRevision(&m, needle)

				for _, step := range tc.steps {
					step(&m)
				}

				size(&m)

				for _, step := range tc.after {
					step(&m)
				}

				assert.Equal(t, tc.want, m.YOffset(), screenMessage(&m))
			})
		}
	}
}

func TestViewport_ScrollBeforeSizeThenLayout(t *testing.T) {
	t.Parallel()

	// A height and then a width give the content area room, and the Model
	// scrolls to the match then. View has a value receiver, so it renders a
	// copy of the Model. A layout change after that keeps the top line, as
	// it does for a Model that got its size before the search.
	doc := replaceLine(
		yamltest.GenerateLines(60, "k%d: "+strings.TrimSpace(strings.Repeat("word ", 8))),
		40,
		"needle: here",
	)

	tcs := map[string]struct {
		change func(m *yamlviewport.Model)
	}{
		"no change":    {},
		"wrap off":     {change: func(m *yamlviewport.Model) { m.SetWordWrap(false) }},
		"line numbers": {change: func(m *yamlviewport.Model) { m.SetPrinter(testPrinterWithLineNumbers()) }},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			size := func(m *yamlviewport.Model) {
				m.SetHeight(10)
				m.SetWidth(30)
			}

			want := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
			setRevision(&want, doc)
			size(&want)
			want.SetSearchTerm("needle")

			got := yamlviewport.New(yamlviewport.WithPrinter(testPrinter()))
			setRevision(&got, doc)
			got.SetSearchTerm("needle")
			size(&got)

			copied := got.View()
			require.Equal(t, 4, rowIndex(copied, "needle"), "screen:\n%s", copied)
			require.Equal(t, want.View(), copied)

			if tc.change != nil {
				tc.change(&want)
				tc.change(&got)
			}

			assert.Equal(t, want.YOffset(), got.YOffset())
			assert.Equal(t, want.View(), got.View())
		})
	}
}

func TestViewport_ContainerFrame(t *testing.T) {
	t.Parallel()

	src := yamltest.GenerateLines(10, "line%d: v")

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
			container: normalBorder(),
			edge:      "─",
			top:       1,
			bottom:    1,
			height:    5,
		},
		"border side by side": {
			container: normalBorder(),
			edge:      "─",
			top:       1,
			bottom:    1,
			height:    5,
			mode:      yamlviewport.ViewModeSideBySide,
		},
		"frame taller than height": {
			container: normalBorder().Padding(1, 1),
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

			m := testModel(framedPrinter(tc.container), 40, tc.height)
			m.SetViewMode(tc.mode)
			setRevision(&m, src)

			total := tc.top + lines + tc.bottom
			require.Equal(t, total, m.TotalRowCount())

			m.GotoBottom()
			require.Equal(t, total-tc.height, m.YOffset())

			for offset := range total - tc.height + 1 {
				m.SetYOffset(offset)

				// A top row in the frame reports the nearest line.
				assert.Equal(t, min(max(offset-tc.top, 0), lines-1), m.TopLine(), "offset %d", offset)

				rows := requireRows(t, m.View(), tc.height)

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

					assert.Contains(t, row, fmt.Sprintf("line%d:", r-tc.top), "offset %d, row %d", offset, i)
				}
			}
		})
	}
}

func TestViewport_ContainerFrameWidth(t *testing.T) {
	t.Parallel()

	// Three lines that wrap at the viewport width, the last ending in END.
	src := stringtest.LinesLF(
		"a: "+strings.Repeat("x", 30),
		"b: "+strings.Repeat("y", 30),
		"c: "+strings.Repeat("z", 30)+" END",
	)

	// The horizontal frame of the printer's container style takes columns
	// from the content area, so lines wrap to what is left and every rendered
	// row fits the viewport width. A row wider than the viewport would wrap
	// again in the final render and push the last line out of reach.
	tcs := map[string]struct {
		container lipgloss.Style
		last      string
	}{
		"border": {
			container: normalBorder(),
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

			m := testModel(framedPrinter(tc.container), width, height)
			setRevision(&m, src)

			for offset := range m.TotalRowCount() - height + 1 {
				m.SetYOffset(offset)

				rows := requireRows(t, m.View(), height, "offset %d", offset)

				for i, row := range rows {
					assert.Equal(t, width, lipgloss.Width(row), "offset %d, row %d", offset, i)
				}
			}

			m.GotoBottom()

			assert.Contains(t, lastRow(m.View()), tc.last)
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
	src := stringtest.LinesLF("a: 1", "b: 2", "c: 3", "long: "+strings.Repeat("x", 40), "d: 4", "e: 5")

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

			m := testModel(framedPrinter(normalBorder()), width, height)
			m.SetViewMode(tc.mode)
			m.SetWordWrap(tc.wrap)
			setRevision(&m, src)

			for offset := range m.TotalRowCount() - height + 1 {
				m.SetYOffset(offset)

				requireFrame(t, &m, []int{0}, []int{width - 1})
			}
		})
	}
}

func TestViewport_SideBySideSeparatorStyle(t *testing.T) {
	t.Parallel()

	// The divider between the panes draws in kind.UISeparator, as the one
	// of printer.Printer.PrintSideBySide does, so a theme styles the two
	// alike.
	divider := lipgloss.NewStyle().Foreground(lipgloss.Color("#ff0000"))
	styles := style.New(lipgloss.NewStyle(), style.Set(kind.UISeparator, divider))

	tcs := map[string]struct {
		width int
	}{
		"even width": {width: 41},
		"odd width":  {width: 42},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := testModel(testPrinter().With(printer.WithStyles(styles)), tc.width, 3)
			m.SetViewMode(yamlviewport.ViewModeSideBySide)
			setRevision(&m, stringtest.LinesLF("a: 1", "b: 2"))

			rows := strings.Split(m.View(), "\n")
			require.GreaterOrEqual(t, len(rows), 2)

			for _, row := range rows[:2] {
				assert.Contains(t, row, divider.Render(" │ "))
				assert.Equal(t, tc.width, ansi.StringWidth(row))
			}
		})
	}
}

func TestViewport_SideBySideAlignsRowsAbove(t *testing.T) {
	t.Parallel()

	// The second line of each revision changes. The pane with fewer rows
	// above the first content row of that line gets blank rows above its
	// own, so both content rows of the line sit on one screen row.
	note := func(text string, k kind.Kind) line.Annotation {
		return line.Annotation{Content: text, Kind: k, Placement: line.Above, Cols: position.NewSpan(3, 3)}
	}

	tcs := map[string]struct {
		before, after line.Annotations
		want          string
		wantHits      string
	}{
		"note above the right pane": {
			after: line.Annotations{note("a note", "")},
			want: stringtest.JoinLF(
				"   1  a: 1      │    1  a: 1",
				"                │          a note",
				"   2 -b: 2      │    2 +b: 20",
				"   3  c: 3      │    3  c: 3",
			),
			wantHits: stringtest.JoinLF(
				"......0000..............1111.....",
				".................................",
				"......0000..............11111....",
				"......0000..............1111.....",
				".................................",
			),
		},
		"more notes above the left pane": {
			before: line.Annotations{note("one", ""), note("two", kind.TextWarn)},
			after:  line.Annotations{note("a note", "")},
			want: stringtest.JoinLF(
				"   1  a: 1      │    1  a: 1",
				"         one    │",
				"         two    │          a note",
				"   2 -b: 2      │    2 +b: 20",
				"   3  c: 3      │    3  c: 3",
			),
			wantHits: stringtest.JoinLF(
				"......0000..............1111.....",
				".................................",
				".................................",
				"......0000..............11111....",
				"......0000..............1111.....",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			before := niceyaml.NewSourceFromString(stringtest.LinesLF("a: 1", "b: 2", "c: 3")).View()
			after := niceyaml.NewSourceFromString(stringtest.LinesLF("a: 1", "b: 20", "c: 3")).View()

			before.Annotate(1, tc.before...)
			after.Annotate(1, tc.after...)

			m := testModel(testPrinterWithLineNumbers(), 33, 5)
			m.SetViewMode(yamlviewport.ViewModeSideBySide)
			m.AddRevision(yamlviewport.NewRevision("v1", before))
			m.AddRevision(yamlviewport.NewRevision("v2", after))

			assert.Equal(t, tc.want, plainRows(&m))
			assert.Equal(t, tc.wantHits, hitMap(&m), screenMessage(&m))
			assert.Equal(t, strings.Count(tc.want, "\n")+1, m.TotalRowCount())

			// Reveal centers the content row of the line, which both panes
			// show on one screen row.
			short := m
			short.SetHeight(3)

			for revision := range 2 {
				require.True(t, short.Reveal(yamlviewport.Hit{Revision: revision, Position: position.New(1, 0)}))

				rows := strings.Split(ansi.Strip(short.View()), "\n")
				assert.Contains(t, rows[1], "2 -b: 2", "revision %d", revision)
				assert.Contains(t, rows[1], "2 +b: 20", "revision %d", revision)
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

	before := niceyaml.NewSourceFromString(stringtest.LinesLF("a: 1", "b: short", "c: 3"))
	after := niceyaml.NewSourceFromString(stringtest.LinesLF("a: 1", "b: "+strings.Repeat("y", 60), "c: 3"))

	m := testModel(framedPrinter(normalBorder()), width, height)
	m.SetViewMode(yamlviewport.ViewModeSideBySide)
	m.AddRevision(before)
	m.AddRevision(after)

	// The wrapped line takes more rows in the right pane than in the left,
	// so the left pane runs out of rows partway down the window.
	require.Greater(t, m.TotalRowCount(), before.View().Count()+2)

	for offset := range m.TotalRowCount() - height + 1 {
		m.SetYOffset(offset)

		requireFrame(t, &m, []int{0, width - pane}, []int{pane - 1, width - 1})
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

			m := testModel(framedPrinter(normalBorder()), tc.width, 3)
			m.SetViewMode(tc.mode)
			m.SetWordWrap(false)
			setRevision(&m, "k: 0123456789abcdefghijklmnopqrstuvwxyz\n")

			m.SetXOffset(5)
			require.Equal(t, 5, m.XOffset())

			rows := requireRows(t, m.View(), 3)
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
	m := testModel(framedPrinter(normalBorder()), 31, 3)
	m.SetViewMode(yamlviewport.ViewModeSideBySide)
	m.SetWordWrap(false)
	setRevision(&m, stringtest.LinesLF("a: 1", "b: s", "c: 3"))
	m.AddRevision(niceyaml.NewSourceFromString(stringtest.LinesLF("a: 1", "b: "+strings.Repeat("y", 50), "c: 3")))

	for _, offset := range []int{1, 10, 40} {
		m.SetXOffset(offset)
		require.Equal(t, offset, m.XOffset())

		framedRows(t, &m, 3)
	}
}

func TestViewport_HorizontalScrollKeepsFrameAtWideCluster(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		// The line that holds the cluster, below a line of plain text.
		line string
		// The start of the row of that line at each horizontal offset.
		wants map[int]string
	}{
		// A wide character that begins before the horizontal offset has only
		// its right cell inside the window. The cut once kept the whole
		// character, which pushed the rest of the row one column right and
		// the right border out of the view. The window shows that cell as a
		// space in the style of the character, so the border keeps its
		// column. The gutter and "k: " take four columns, so an offset of 5
		// falls inside the character.
		"wide rune": {
			line:  "k: 日" + strings.Repeat("b", 36),
			wants: map[int]string{5: "│ b"},
		},
		// A keycap takes two cells, where ansi.Cut counts one for its ASCII
		// base. The cut once took an extra cell at an offset before the
		// keycap, which pushed the right border out of the view, and showed
		// a blank row at an offset past it. The gutter and "k: ab" take six
		// columns, so the keycap takes columns 6 and 7.
		"keycap": {
			line: "k: ab1\ufe0f\u20e3" + strings.Repeat("b", 36),
			wants: map[int]string{
				0: "│ k: ab1\ufe0f\u20e3bbb",
				7: "│ bbb",
				8: "│bbb",
			},
		},
	}

	modes := map[string]struct {
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
		for modeName, mode := range modes {
			t.Run(name+"/"+modeName, func(t *testing.T) {
				t.Parallel()

				m := testModel(framedPrinter(normalBorder()), mode.width, 4)
				m.SetViewMode(mode.mode)
				m.SetWordWrap(false)
				setRevision(&m, stringtest.LinesLF("a: "+strings.Repeat("a", 34), tc.line))

				for offset, want := range tc.wants {
					m.SetXOffset(offset)
					require.Equal(t, offset, m.XOffset())

					rows := framedRows(t, &m, 4)
					assert.True(t, strings.HasPrefix(rows[2], want), "offset %d: %q", offset, rows[2])
				}
			})
		}
	}
}

func TestViewport_KeycapRowsFitWidth(t *testing.T) {
	t.Parallel()

	// With wrap off, a row of keycaps runs past the width, and the view
	// cuts it to the width by the two cells each keycap takes.
	keycaps := strings.Repeat("1\ufe0f\u20e3", 10)

	tcs := map[string]struct {
		mode  yamlviewport.ViewMode
		width int
	}{
		"full": {
			mode:  yamlviewport.ViewModeFull,
			width: 30,
		},
		"side by side": {
			mode:  yamlviewport.ViewModeSideBySide,
			width: 61,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := testModel(printer.New(), tc.width, 2)
			m.SetViewMode(tc.mode)
			m.SetWordWrap(false)
			setRevision(&m, stringtest.LinesLF("k: "+keycaps+"abcdefghij", "other: plain"))

			for i, row := range strings.Split(m.View(), "\n") {
				assert.Equal(t, tc.width, ansi.StringWidth(row), "row %d: %q", i, ansi.Strip(row))
			}
		})
	}
}

func TestViewport_SearchScrollsPastKeycap(t *testing.T) {
	t.Parallel()

	// The search scrolls the match into view past a keycap, which the cut
	// once measured as one cell short and rendered as a blank row.
	m := testModel(printer.New(), 40, 2)
	m.SetWordWrap(false)
	setRevision(&m, stringtest.LinesLF(
		"step: 1\ufe0f\u20e3 "+strings.Repeat("x", 60)+" needle",
		"other: plain",
	))

	m.SetSearchTerm("needle")
	require.Positive(t, m.XOffset())

	assert.Contains(t, ansi.Strip(m.View()), "needle")
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

			m := testModel(testPrinter(), tc.width, 5)
			addRevisions(&m, threeYAML, threeChangedYAML)
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
	slice := func(lines ...string) yamlviewport.Revision {
		source := niceyaml.NewSourceFromString(stringtest.LinesLF(lines...))

		return yamlviewport.NewRevision("part", source.View().Slice(position.NewSpan(1, 4)))
	}

	m := testModel(testPrinter(), 80, 10)
	m.SetRevision(slice("a: 1", "b: 2", "c: 3", "d: 4", "e: 5"))
	m.AddRevision(slice("a: 9", "b: 2", "c: 3", "d: 4", "e: 9"))

	require.True(t, m.ShowingDiff())

	assert.Equal(t, diff.Stats{}, m.DiffStats())
	assert.Equal(t, 3, m.TotalLineCount())
	assert.NotContains(t, m.View(), "a: 9")
	assert.NotContains(t, m.View(), "e: 5")

	// A change inside the held lines shows as it does for a whole source.
	m.AddRevision(slice("a: 9", "b: 2", "c: 8", "d: 4", "e: 9"))

	assert.Equal(t, diff.Stats{Added: 1, Removed: 1}, m.DiffStats())
	assert.Contains(t, m.View(), "c: 8")
	assert.Contains(t, m.View(), "c: 3")
}

func TestViewport_ViewFitsHeight(t *testing.T) {
	t.Parallel()

	border := normalBorder()

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

			m := testModel(testPrinter(), 20, tc.height, yamlviewport.WithContainerStyle(tc.style))
			setRevision(&m, threeYAML)

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

	box := normalBorder()

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

			m := testModel(testPrinter(), 60, 40, yamlviewport.WithContainerStyle(tc.style))
			setRevision(&m, stringtest.LinesLF("a: 1", "b: 2"))

			view := m.View()
			assert.Equal(t, tc.wantWidth, lipgloss.Width(view))
			assert.Equal(t, tc.wantHeight, lipgloss.Height(view))
		})
	}
}

func TestViewport_MaxSizeContainerStyle(t *testing.T) {
	t.Parallel()

	box := normalBorder()

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

			m := testModel(testPrinter(), 20, 12, yamlviewport.WithContainerStyle(tc.style))
			setRevision(&m, yamltest.GenerateLines(50, "k%d: v"))

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
			wantLast:  "k19:",
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
			container: normalBorder().Height(8),
			line:      "k%d: v",
			lines:     10,
			width:     20,
			height:    4,
			wantTotal: 12,
			wantLast:  "└",
		},
		"max height": {
			container: normalBorder().MaxHeight(4),
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

			m := testModel(framedPrinter(tc.container), tc.width, tc.height)
			m.SetWordWrap(true)
			setRevision(&m, yamltest.GenerateLines(tc.lines, tc.line))

			assert.Equal(t, tc.wantTotal, m.TotalRowCount())

			m.GotoBottom()

			rows := requireRows(t, m.View(), tc.height)
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

			m := testModel(testPrinterWithColors(), width, height)
			m.SetViewMode(tc.mode)

			if tc.yaml != "" {
				setRevision(&m, tc.yaml)
			}

			rows := requireRows(t, m.View(), height)

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
				m := testModel(tc.printer, width, height)
				m.SetViewMode(tc.mode)
				addRevisions(
					&m,
					stringtest.LinesLF("name: original", "count: 10"),
					stringtest.LinesLF("name: modified", "count: 20"),
				)

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

	lines := niceyaml.NewSourceFromString(yaml)

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
		"SetSearchTerm/Empty": {
			searchTerm: "item",
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Positive(t, m.SearchCount())

				// An empty term clears the search.
				m.SetSearchTerm("")
				assert.Empty(t, m.SearchTerm())
				assert.Equal(t, 0, m.SearchCount())
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

			m := testModel(testPrinter(), 80, 24)
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

	revs := []string{rev1, rev2, rev3}

	same := stringtest.Input(`
		name: same
		value: 10
	`)

	different := stringtest.Input(`
		name: different
		value: 20
	`)

	// The first two revisions hold identical content.
	dupes := []string{same, same, different}

	// The rev closure returns revision n under the name addRevisions gives
	// it.
	rev := func(n int) *niceyaml.Source {
		return niceyaml.NewSourceFromString(revs[n-1], niceyaml.WithName(fmt.Sprintf("v%d", n)))
	}

	tcs := map[string]struct {
		setup func(m *yamlviewport.Model)
		test  func(t *testing.T, m *yamlviewport.Model)
		// The documents the model holds a revision of before setup runs.
		docs []string
	}{
		"Empty": {
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				// With no revisions, both checks return true.
				assert.True(t, m.AtFirstRevision())
				assert.True(t, m.AtLatestRevision())
				assert.Empty(t, m.RevisionNames())

				// Clearing an empty history leaves it empty.
				m.ClearRevisions()
				assert.Equal(t, 0, m.RevisionCount())
				assert.Equal(t, 0, m.RevisionIndex())
				assert.False(t, m.ShowingDiff())
				assert.Empty(t, m.RevisionName())
			},
		},
		"AtFirstRevision/Multiple": {
			docs: revs[:2],
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				// At the latest revision, not at the first.
				assert.False(t, m.AtFirstRevision())
				assert.True(t, m.AtLatestRevision())

				m.GotoRevision(0)
				assert.True(t, m.AtFirstRevision())
				assert.False(t, m.AtLatestRevision())
			},
		},
		"AddRevision/Single": {
			docs: revs[:1],
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, 1, m.RevisionCount())
				assert.Equal(t, 0, m.RevisionIndex()) // At the only revision.
				assert.True(t, m.AtLatestRevision())
				assert.False(t, m.ShowingDiff()) // Only one revision, no diff possible.
				assert.Equal(t, "v1", m.RevisionName())
				assert.Equal(t, []string{"v1"}, m.RevisionNames())
			},
		},
		"AddRevision/Multiple": {
			docs: revs[:3],
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, 3, m.RevisionCount())
				assert.Equal(t, 2, m.RevisionIndex()) // At latest (0-indexed).
				assert.True(t, m.AtLatestRevision())
				assert.True(t, m.ShowingDiff()) // At index > 0 with default diffMode.
				assert.Equal(t, "v3", m.RevisionName())
				assert.Equal(t, []string{"v1", "v2", "v3"}, m.RevisionNames())
				assert.Positive(t, m.TotalLineCount())
			},
		},
		"AddRevisions/Multiple": {
			setup: func(m *yamlviewport.Model) {
				m.AddRevisions(rev(1), rev(2), rev(3))
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, 3, m.RevisionCount())
				assert.Equal(t, 2, m.RevisionIndex())
				assert.True(t, m.AtLatestRevision())
				assert.True(t, m.ShowingDiff())
				assert.Equal(t, []string{"v1", "v2", "v3"}, m.RevisionNames())
			},
		},
		"AddRevisions/SkipsNil": {
			setup: func(m *yamlviewport.Model) {
				m.AddRevisions(nil, rev(1), (*niceyaml.Source)(nil), rev(2), nil)
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, 2, m.RevisionCount())
				assert.Equal(t, 1, m.RevisionIndex())
				assert.Equal(t, []string{"v1", "v2"}, m.RevisionNames())
			},
		},
		"AddRevisions/Empty": {
			docs: revs[:2],
			setup: func(m *yamlviewport.Model) {
				m.GotoRevision(0)
				m.AddRevisions()
				m.AddRevisions(nil, (*niceyaml.Source)(nil))
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, 2, m.RevisionCount())
				assert.Equal(t, 0, m.RevisionIndex()) // Stays on the revision it was at.
				assert.Equal(t, "v1", m.RevisionName())
			},
		},
		"ClearRevisions": {
			docs: revs[:2],
			setup: func(m *yamlviewport.Model) {
				m.ClearRevisions()
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, 0, m.RevisionCount())
				assert.Equal(t, 0, m.RevisionIndex())
				assert.Empty(t, m.RevisionName())
			},
		},
		"GotoRevision/EachIndex": {
			docs: revs[:3],
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				// Index 2 is the latest, so the view shows a diff in the
				// default mode.
				assert.True(t, m.ShowingDiff())
				assert.Equal(t, "v3", m.RevisionName())

				m.GotoRevision(0)
				assert.Equal(t, 0, m.RevisionIndex())
				assert.True(t, m.AtFirstRevision())
				assert.False(t, m.ShowingDiff()) // First revision, no diff.
				assert.Equal(t, "v1", m.RevisionName())

				m.GotoRevision(1)
				assert.Equal(t, 1, m.RevisionIndex())
				assert.True(t, m.ShowingDiff()) // Between 0 and 1.
				assert.Equal(t, "v2", m.RevisionName())

				m.GotoRevision(2)
				assert.True(t, m.ShowingDiff()) // Between 1 and 2.
				assert.Equal(t, "v3", m.RevisionName())
			},
		},
		"GotoRevision/Clamped": {
			docs: revs[:2],
			setup: func(m *yamlviewport.Model) {
				m.GotoRevision(100) // Should clamp to max (N-1).
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, 1, m.RevisionIndex()) // Clamped to last index (0-indexed).
				assert.True(t, m.AtLatestRevision())
				assert.Equal(t, "v2", m.RevisionName())
			},
		},
		"NextRevision": {
			docs: revs[:3],
			setup: func(m *yamlviewport.Model) {
				m.GotoRevision(0)
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, "v1", m.RevisionName())
				m.NextRevision()
				assert.Equal(t, 1, m.RevisionIndex())
				assert.True(t, m.ShowingDiff())
				assert.Equal(t, "v2", m.RevisionName())
			},
		},
		"NextRevision/AtLatest": {
			docs: revs[:2], // Already at latest (index 1).
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				m.NextRevision()
				assert.Equal(t, 1, m.RevisionIndex()) // Should not change.
				assert.Equal(t, "v2", m.RevisionName())
			},
		},
		"PreviousRevision": {
			docs: revs[:3],
			setup: func(m *yamlviewport.Model) {
				m.GotoRevision(2)
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, "v3", m.RevisionName())
				m.PreviousRevision()
				assert.Equal(t, 1, m.RevisionIndex())
				assert.Equal(t, "v2", m.RevisionName())
			},
		},
		"PreviousRevision/AtFirst": {
			docs: revs[:2],
			setup: func(m *yamlviewport.Model) {
				m.GotoRevision(0)
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				m.PreviousRevision()
				assert.Equal(t, 0, m.RevisionIndex()) // Should not change.
				assert.Equal(t, "v1", m.RevisionName())
			},
		},
		"SetRevision/Single": {
			setup: func(m *yamlviewport.Model) {
				setRevision(m, rev1)
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, 2, m.TotalLineCount())
				assert.Equal(t, 1, m.RevisionCount())
				assert.False(t, m.ShowingDiff())
			},
		},
		"SetRevision/ReplacesRevisions": {
			docs: revs[:2],
			setup: func(m *yamlviewport.Model) {
				setRevision(m, rev3) // SetRevision uses the Source's name.
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				// SetRevision replaces the history with a single revision.
				assert.Equal(t, 1, m.RevisionCount())
				assert.Equal(t, 0, m.RevisionIndex()) // Only one revision at index 0.
				assert.Empty(t, m.RevisionName())     // A source without a name.
				assert.False(t, m.ShowingDiff())
				assert.Equal(t, 3, m.TotalLineCount())
			},
		},
		"ToggleDiffMode/FullCycle": {
			docs: revs[:2],
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
			docs: revs[:2],
			setup: func(m *yamlviewport.Model) {
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
			docs: revs[:2],
			setup: func(m *yamlviewport.Model) {
				m.SetDiffMode(yamlviewport.DiffMode(-1))
				m.ToggleDiffMode()
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, yamlviewport.DiffModeOrigin, m.DiffMode())
			},
		},
		"DiffModeNone/NotShowingDiff": {
			docs: revs[:3],
			setup: func(m *yamlviewport.Model) {
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
		"DiffModeAtIndex0/NoEffect": {
			docs: revs[:3],
			setup: func(m *yamlviewport.Model) {
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
		"DiffModeAtLatest/ShowsDiff": {
			docs: revs[:2],
			setup: func(m *yamlviewport.Model) {
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
		"DuplicateRevisions/CountIsCorrect": {
			docs: dupes[:2],
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				// Should still report 2 revisions (the logical count).
				assert.Equal(t, 2, m.RevisionCount())
				assert.True(t, m.AtLatestRevision())
				// At latest (index 1), RevisionName returns the current revision name.
				assert.Equal(t, "v2", m.RevisionName())
			},
		},
		"DuplicateRevisions/NavigationWorks": {
			docs: dupes[:2],
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				// Should be able to navigate through all revisions.
				m.GotoRevision(0)
				assert.Equal(t, 0, m.RevisionIndex())
				assert.True(t, m.AtFirstRevision())
				assert.Equal(t, "v1", m.RevisionName())

				m.NextRevision()
				assert.Equal(t, 1, m.RevisionIndex())
				assert.Equal(t, "v2", m.RevisionName())
				assert.True(t, m.AtLatestRevision())

				// Already at latest, NextRevision does nothing.
				m.NextRevision()
				assert.Equal(t, 1, m.RevisionIndex())
				assert.Equal(t, "v2", m.RevisionName())
			},
		},
		"DuplicateRevisions/ContentRendersCorrectly": {
			docs: dupes[:2],
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()

				m.GotoRevision(0)

				// Content should render (not crash).
				view := m.View()
				assert.Contains(t, view, "name")
				assert.Contains(t, view, "same")
				assert.Equal(t, "v1", m.RevisionName())
			},
		},
		"MixedRevisions/Works": {
			docs: dupes[:3], // Two identical + one different.
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, 3, m.RevisionCount())

				// At latest (index 2).
				assert.True(t, m.AtLatestRevision())
				assert.Equal(t, "v3", m.RevisionName())
				assert.True(t, m.ShowingDiff()) // Index > 0 shows a diff.
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := testModel(testPrinter(), 80, 24)
			addRevisions(&m, tc.docs...)

			if tc.setup != nil {
				tc.setup(&m)
			}

			tc.test(t, &m)
		})
	}
}

func TestViewport_NilRevision(t *testing.T) {
	t.Parallel()

	// A revision that is nil, or that holds a nil pointer or a nil func,
	// has no view to show, so the model leaves it out.
	tcs := map[string]struct {
		revision yamlviewport.Revision
	}{
		"nil":        {revision: nil},
		"nil source": {revision: (*niceyaml.Source)(nil)},
		"nil func":   {revision: funcRevision(nil)},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := testModel(testPrinter(), 80, 24)
			m.AddRevision(tc.revision)
			m.SetRevision(tc.revision)

			assert.Equal(t, 0, m.RevisionCount())
			assert.Empty(t, m.RevisionName())
			assert.Empty(t, m.RevisionNames())
		})
	}
}

func TestViewport_SameDiffBaseKeepsPosition(t *testing.T) {
	t.Parallel()

	// At revision index 1, adjacent and origin both compare against the
	// first revision, so a change between them shows the same diff and
	// leaves the scroll offset and the selected match where they are.
	v1 := yamltest.GenerateLines(50, "k%d: v")

	m := testModel(testPrinter(), 40, 10)
	addRevisions(&m, v1, replaceLine(v1, 20, "k20: w"))
	m.SetSearchTerm("v")
	searchNext(&m, 2)
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
	addRevisions(&m, replaceLine(v1, 40, "k40: w"))
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
	}{
		"set full to hunks": {
			from: yamlviewport.ViewModeFull,
			to:   yamlviewport.ViewModeHunks,
		},
		"set full to side-by-side": {
			from: yamlviewport.ViewModeFull,
			to:   yamlviewport.ViewModeSideBySide,
		},
		"set side-by-side to full": {
			from: yamlviewport.ViewModeSideBySide,
			to:   yamlviewport.ViewModeFull,
		},
		"toggle full to hunks": {
			from:   yamlviewport.ViewModeFull,
			to:     yamlviewport.ViewModeHunks,
			toggle: true,
		},
		"toggle hunks to side-by-side": {
			from:   yamlviewport.ViewModeHunks,
			to:     yamlviewport.ViewModeSideBySide,
			toggle: true,
		},
		"toggle side-by-side to full": {
			from:   yamlviewport.ViewModeSideBySide,
			to:     yamlviewport.ViewModeFull,
			toggle: true,
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

	v1 := yamltest.GenerateLines(50, "k%d: v")

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := testModel(testPrinter(), 40, 10)
			m.SetViewMode(tc.from)
			addRevisions(&m, v1)

			if tc.diff {
				addRevisions(&m, replaceLine(v1, 45, "k45: w"))
			}

			m.SetSearchTerm("v")
			searchNext(&m, 20)

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

			if tc.diff {
				assert.Equal(t, 0, m.SearchIndex())
				assert.Equal(t, 0, m.TopLine())
			} else {
				assert.Equal(t, 20, m.SearchIndex())
				assert.Equal(t, topLine, m.TopLine())
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

	tcs := map[string]struct {
		setup  func(m *yamlviewport.Model)
		test   func(t *testing.T, m *yamlviewport.Model)
		yaml   string
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
		"LineCounts/VisibleLineCount": {
			yaml:   lineCountYAML,
			width:  80,
			height: 3,
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Equal(t, 3, m.VisibleLineCount())
			},
		},
		"LineCounts/EmptyContent": {
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
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()
				assert.Nil(t, m.Init())
			},
		},
		"AtBottom/SetHeightReclamps": {
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
				setRevision(m, "line: a\n")
				assert.Equal(t, 1, m.SearchCount())
				assert.Equal(t, 0, m.SearchIndex())
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := testModel(testPrinter(), tc.width, tc.height)

			if tc.yaml != "" {
				setRevision(&m, tc.yaml)
			}

			if tc.setup != nil {
				tc.setup(&m)
			}

			tc.test(t, &m)
		})
	}
}

func TestViewport_Update(t *testing.T) {
	t.Parallel()

	// The change closure returns a second revision of verticalYAML.
	change := func() *niceyaml.Source {
		return niceyaml.NewSourceFromString(
			stringtest.JoinLF("line1: modified", "line2: changed"),
			niceyaml.WithName("change"),
		)
	}

	tcs := map[string]struct {
		msg   tea.Msg
		setup func(m *yamlviewport.Model)
		// The checks of the updated model. A case without test compares
		// the updated view with its golden file.
		test func(t *testing.T, m *yamlviewport.Model)
		yaml string
		// The viewport is 40 columns wide and 5 rows tall unless the case
		// sets width or height.
		width  int
		height int
	}{
		// Golden file tests (verify rendered output).
		"KeyDown": {
			msg:  tea.KeyPressMsg{Code: 'j'},
			yaml: verticalYAML,
		},
		"KeyUp": {
			msg:  tea.KeyPressMsg{Code: 'k'},
			yaml: verticalYAML,
			setup: func(m *yamlviewport.Model) {
				m.SetYOffset(3)
			},
		},
		"KeyPageDown": {
			msg:  tea.KeyPressMsg{Code: 'f'},
			yaml: verticalYAML,
		},
		"KeyPageUp": {
			msg:  tea.KeyPressMsg{Code: 'b'},
			yaml: verticalYAML,
			setup: func(m *yamlviewport.Model) {
				m.GotoBottom()
			},
		},
		"KeyHalfPageDown": {
			msg:    tea.KeyPressMsg{Code: 'd'},
			yaml:   verticalYAML,
			height: 6,
		},
		"KeyHalfPageUp": {
			msg:    tea.KeyPressMsg{Code: 'u'},
			yaml:   verticalYAML,
			height: 6,
			setup: func(m *yamlviewport.Model) {
				m.GotoBottom()
			},
		},
		"KeyRight": {
			msg:  tea.KeyPressMsg{Code: 'l'},
			yaml: wideYAML,
			setup: func(m *yamlviewport.Model) {
				m.ToggleWordWrap() // Disable wrap.
			},
		},
		"KeyLeft": {
			msg:  tea.KeyPressMsg{Code: 'h'},
			yaml: wideYAML,
			setup: func(m *yamlviewport.Model) {
				m.ToggleWordWrap() // Disable wrap.
				m.SetXOffset(20)
			},
		},
		"MouseWheelDown": {
			msg:  tea.MouseWheelMsg{Button: tea.MouseWheelDown},
			yaml: verticalYAML,
		},
		"MouseWheelUp": {
			msg:  tea.MouseWheelMsg{Button: tea.MouseWheelUp},
			yaml: verticalYAML,
			setup: func(m *yamlviewport.Model) {
				m.SetYOffset(5)
			},
		},
		// Behavior tests (verify state changes).
		"Behavior/TabNextRevision": {
			msg:   tea.KeyPressMsg{Code: tea.KeyTab},
			yaml:  verticalYAML,
			width: 80,
			setup: func(m *yamlviewport.Model) {
				// Add a second revision and go to revision 0.
				m.AddRevision(change())
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
			msg:   tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift},
			yaml:  verticalYAML,
			width: 80,
			setup: func(m *yamlviewport.Model) {
				// Add a second revision (starts at latest index 1).
				m.AddRevision(change())

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
			msg:   tea.KeyPressMsg{Code: 'm'},
			yaml:  verticalYAML,
			width: 80,
			setup: func(m *yamlviewport.Model) {
				m.AddRevision(change())
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

			m := testModel(testPrinter(), cmp.Or(tc.width, 40), cmp.Or(tc.height, 5))
			setRevision(&m, tc.yaml)

			if tc.setup != nil {
				tc.setup(&m)
			}

			updated, _ := m.Update(tc.msg)

			if tc.test == nil {
				golden.RequireEqual(t, updated.View())

				return
			}

			tc.test(t, &updated)
		})
	}
}

func TestViewport_UpdateVerticalWheel(t *testing.T) {
	t.Parallel()

	// A vertical wheel scrolls by MouseWheelDelta rows. A negative delta
	// turns the wheel around, and a disabled wheel does not scroll.
	tcs := map[string]struct {
		setup  func(m *yamlviewport.Model)
		button tea.MouseButton
		want   int
	}{
		"disabled": {
			setup:  func(m *yamlviewport.Model) { m.MouseWheelEnabled = false },
			button: tea.MouseWheelDown,
			want:   0,
		},
		"custom delta": {
			setup:  func(m *yamlviewport.Model) { m.MouseWheelDelta = 5 },
			button: tea.MouseWheelDown,
			want:   5,
		},
		"inverted at top": {
			setup:  func(m *yamlviewport.Model) { m.MouseWheelDelta = -3 },
			button: tea.MouseWheelUp,
			want:   3,
		},
		"inverted at bottom": {
			setup: func(m *yamlviewport.Model) {
				m.MouseWheelDelta = -3
				m.GotoBottom()
			},
			button: tea.MouseWheelDown,
			want:   2,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := testModel(testPrinter(), 80, 5)
			setRevision(&m, verticalYAML)
			tc.setup(&m)

			m, _ = m.Update(tea.MouseWheelMsg{Button: tc.button})

			assert.Equal(t, tc.want, m.YOffset())
		})
	}
}

func TestViewport_UpdateHorizontalWheel(t *testing.T) {
	t.Parallel()

	// A horizontal wheel scrolls sideways by the horizontal step, which is
	// 6 columns by default, and so does a vertical wheel with Shift held.
	// Shift remaps the vertical wheel alone, so a horizontal wheel with
	// Shift held scrolls as it does without it.
	tcs := map[string]struct {
		msg  tea.MouseWheelMsg
		want int
	}{
		"down with shift":  {msg: tea.MouseWheelMsg{Button: tea.MouseWheelDown, Mod: tea.ModShift}, want: 26},
		"up with shift":    {msg: tea.MouseWheelMsg{Button: tea.MouseWheelUp, Mod: tea.ModShift}, want: 14},
		"left":             {msg: tea.MouseWheelMsg{Button: tea.MouseWheelLeft}, want: 14},
		"right":            {msg: tea.MouseWheelMsg{Button: tea.MouseWheelRight}, want: 26},
		"left with shift":  {msg: tea.MouseWheelMsg{Button: tea.MouseWheelLeft, Mod: tea.ModShift}, want: 14},
		"right with shift": {msg: tea.MouseWheelMsg{Button: tea.MouseWheelRight, Mod: tea.ModShift}, want: 26},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := testModel(testPrinter(), 40, 10)
			m.SetWordWrap(false)
			setRevision(&m, wideYAML)
			m.SetXOffset(20)

			m, _ = m.Update(tc.msg)

			assert.Equal(t, tc.want, m.XOffset())
			assert.Equal(t, 0, m.YOffset())
		})
	}
}

func TestViewport_KeyMap(t *testing.T) {
	t.Parallel()

	lines := niceyaml.NewSourceFromString(stringtest.JoinLF("key: value", "key2: value2", "key3: value3"))

	rebind := func(km *yamlviewport.KeyMap) { km.Down = key.NewBinding(key.WithKeys("x")) }

	// Each case changes the Down binding, which 'j' triggers by default,
	// and presses one key.
	tcs := map[string]struct {
		change func(km *yamlviewport.KeyMap)
		key    rune
		want   int
	}{
		"custom binding drops the default key": {
			change: rebind,
			key:    'j',
			want:   0,
		},
		"custom binding scrolls on its key": {
			change: rebind,
			key:    'x',
			want:   1,
		},
		"disabled binding": {
			change: func(km *yamlviewport.KeyMap) { km.Down.SetEnabled(false) },
			key:    'j',
			want:   0,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := testModel(testPrinter(), 80, 2)
			m.SetRevision(lines)
			tc.change(&m.KeyMap)

			m, _ = m.Update(tea.KeyPressMsg{Code: tc.key})

			assert.Equal(t, tc.want, m.YOffset())
		})
	}
}

func TestViewModeHunks_Golden(t *testing.T) {
	t.Parallel()

	// The hunks closure returns a setup func that adds a revision of each
	// doc and shows the diff at the revision index in diffMode as hunks.
	hunks := func(index int, diffMode yamlviewport.DiffMode, docs ...string) func(m *yamlviewport.Model) {
		return func(m *yamlviewport.Model) {
			addRevisions(m, docs...)
			m.GotoRevision(index)
			m.SetDiffMode(diffMode)
			m.SetViewMode(yamlviewport.ViewModeHunks)
		}
	}

	// The diff of the first two revisions.
	basic := hunks(1, yamlviewport.DiffModeAdjacent, modeRev1YAML, modeRev2YAML)

	type goldenTest struct {
		setupFunc func(m *yamlviewport.Model)
		// The viewport is 80 columns wide and 30 rows tall unless the case
		// sets width or height.
		width  int
		height int
	}

	tcs := map[string]goldenTest{
		"BasicHunks": {
			setupFunc: basic,
		},
		"AtFirstRevision": {
			setupFunc: hunks(0, yamlviewport.DiffModeAdjacent, modeRev1YAML, modeRev2YAML),
		},
		"DiffModeOrigin": {
			setupFunc: hunks(2, yamlviewport.DiffModeOrigin, modeRev1YAML, modeRev2YAML, modeRev3YAML),
		},
		"DiffModeNone": {
			setupFunc: hunks(1, yamlviewport.DiffModeNone, modeRev1YAML, modeRev2YAML),
		},
		"MultipleRevisions": {
			setupFunc: hunks(2, yamlviewport.DiffModeAdjacent, modeRev1YAML, modeRev2YAML, modeRev3YAML),
		},
		"HunksScrolled": {
			// The offset counts rows, and the hunk header is a row.
			setupFunc: func(m *yamlviewport.Model) {
				basic(m)
				m.SetYOffset(2)
			},
			height: 5,
		},
		"HunksScrolledToBottom": {
			setupFunc: func(m *yamlviewport.Model) {
				basic(m)
				m.GotoBottom()
			},
			height: 5,
		},
		"HunksSearch": {
			setupFunc: func(m *yamlviewport.Model) {
				m.SetPrinter(testPrinterWithSearch())
				basic(m)
				m.SetSearchTerm("name")
			},
		},
		"HunksSearchNavigate": {
			setupFunc: func(m *yamlviewport.Model) {
				m.SetPrinter(testPrinterWithSearch())
				basic(m)
				m.SetSearchTerm("name")
				m.SearchNext() // Navigate to second match.
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := testModel(testPrinter(), cmp.Or(tc.width, 80), cmp.Or(tc.height, 30))
			tc.setupFunc(&m)

			golden.RequireEqual(t, m.View())
		})
	}
}

func TestViewModeSideBySide_Golden(t *testing.T) {
	t.Parallel()

	// The first two revisions end in a block that the second changes on
	// every line, and the third revision leaves the block out.
	rev1YAML := stringtest.JoinLF(modeRev1YAML, "extra:", "  key1: value1", "  key2: value2")
	rev2YAML := stringtest.JoinLF(modeRev2YAML, "extra:", "  key1: changed1", "  key2: changed2")

	// The sideBySide closure returns a setup func that adds a revision of
	// each doc and shows the diff at the revision index in diffMode in two
	// panes.
	sideBySide := func(index int, diffMode yamlviewport.DiffMode, docs ...string) func(m *yamlviewport.Model) {
		return func(m *yamlviewport.Model) {
			addRevisions(m, docs...)
			m.GotoRevision(index)
			m.SetDiffMode(diffMode)
			m.SetViewMode(yamlviewport.ViewModeSideBySide)
		}
	}

	// The diff of the first two revisions.
	basic := sideBySide(1, yamlviewport.DiffModeAdjacent, rev1YAML, rev2YAML)

	// The search closure returns a setup func that shows the same diff with
	// tagged search highlights, searches for term, and moves the selection
	// next matches ahead. The XML tags of the test styles take up columns,
	// so its cases use a viewport wide enough that highlighted lines fit on
	// one row.
	search := func(term string, next int) func(m *yamlviewport.Model) {
		return func(m *yamlviewport.Model) {
			m.SetPrinter(testPrinterWithSearch())
			basic(m)
			m.SetSearchTerm(term)
			searchNext(m, next)
		}
	}

	type goldenTest struct {
		setupFunc func(m *yamlviewport.Model)
		// The viewport is 80 columns wide and 24 rows tall unless the case
		// sets width or height.
		width  int
		height int
	}

	tcs := map[string]goldenTest{
		"SideBySide": {
			setupFunc: basic,
		},
		"SideBySideNoChanges": {
			// Same content on both sides renders identical panes.
			setupFunc: sideBySide(1, yamlviewport.DiffModeAdjacent, rev1YAML, rev1YAML),
		},
		"SideBySideAtOrigin": {
			// At revision 0, both panes show the same content.
			setupFunc: sideBySide(0, yamlviewport.DiffModeAdjacent, rev1YAML, rev2YAML),
		},
		"SideBySideScrolled": {
			// Side-by-side with vertical scrolling.
			setupFunc: func(m *yamlviewport.Model) {
				basic(m)
				m.SetYOffset(3)
			},
			height: 10,
		},
		"SideBySideDiffModeOrigin": {
			// Side-by-side with origin diff mode (compares to first revision).
			setupFunc: sideBySide(2, yamlviewport.DiffModeOrigin, rev1YAML, rev2YAML, modeRev3YAML),
		},
		"SideBySideDiffModeNone": {
			// Side-by-side with no diff mode shows the same content on both panes.
			setupFunc: sideBySide(1, yamlviewport.DiffModeNone, rev1YAML, rev2YAML),
		},
		"SideBySideNarrow": {
			// Narrower viewport to test pane width calculation.
			setupFunc: basic,
			width:     60,
			height:    16,
		},
		"SideBySideMinimal": {
			// Width 5: paneWidth = (5-3)/2 = 1, minimum to render content.
			setupFunc: basic,
			width:     5,
			height:    10,
		},
		"SideBySideMoreDeletions": {
			// With more deletions than insertions, the right pane gets placeholders.
			setupFunc: sideBySide(1, yamlviewport.DiffModeAdjacent,
				stringtest.Input(`
					keep: start
					del1: a
					del2: b
					del3: c
					del4: d
					keep: end
				`),
				stringtest.Input(`
					keep: start
					ins1: x
					keep: end
				`),
			),
			height: 16,
		},
		"SideBySideMoreInsertions": {
			// With more insertions than deletions, the left pane gets placeholders.
			setupFunc: sideBySide(1, yamlviewport.DiffModeAdjacent,
				stringtest.Input(`
					keep: start
					del1: a
					keep: end
				`),
				stringtest.Input(`
					keep: start
					ins1: w
					ins2: x
					ins3: y
					ins4: z
					keep: end
				`),
			),
			height: 16,
		},
		"SideBySideSearchNavigate": {
			// Verifies search navigation moves the selection in side-by-side
			// mode. "key" matches on the changed key1 and key2 lines of both
			// panes. The matches run in row order, and the before pane comes
			// first within a row, so the third match is key2 in the before
			// pane.
			setupFunc: search("key", 2),
			width:     190,
		},
		"SideBySideSearchDeletedLine": {
			// Verifies SearchSelected appears on deleted line (before pane only).
			setupFunc: search("original", 0),
			width:     190,
		},
		"SideBySideSearchInsertedLine": {
			// Verifies SearchSelected appears on inserted line (after pane only).
			setupFunc: search("modified", 0),
			width:     190,
		},
		"SideBySideSearchBothSidesFirstSelected": {
			// The search term appears on both deleted and inserted lines.
			// The selection sits on the first match, in the before pane.
			setupFunc: search("name", 0),
			width:     190,
		},
		"SideBySideWrapAligned": {
			// The before pane wraps a long line that the after pane replaced
			// with a short one. The after pane gets blank rows so the lines
			// below stay level.
			setupFunc: sideBySide(1, yamlviewport.DiffModeAdjacent,
				stringtest.Input(`
					name: original
					description: "a long description that wraps inside a narrow pane"
					enabled: true
				`),
				stringtest.Input(`
					name: original
					description: short
					enabled: true
				`),
			),
			width:  50,
			height: 8,
		},
		"SideBySideSearchBothSidesSecondSelected": {
			// The search term appears on both deleted and inserted lines.
			// The selection sits on the second match, in the after pane.
			setupFunc: search("name", 1),
			width:     190,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := testModel(testPrinter(), cmp.Or(tc.width, 80), cmp.Or(tc.height, 24))
			tc.setupFunc(&m)

			golden.RequireEqual(t, m.View())
		})
	}
}

func TestViewport_ViewMode(t *testing.T) {
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

	// Two revisions that differ in their last line alone, so the hunk
	// context decides how many of the lines above it show.
	fiveYAML := stringtest.Input(`
		first: one
		second: two
		third: three
		fourth: four
		fifth: five
	`)

	fiveChangedYAML := stringtest.Input(`
		first: one
		second: two
		third: three
		fourth: four
		fifth: changed
	`)

	// The hunks closure returns a setup func that shows the diff of two
	// revisions as hunks.
	hunks := func(before, after string) func(m *yamlviewport.Model) {
		return func(m *yamlviewport.Model) {
			addRevisions(m, before, after)
			m.SetViewMode(yamlviewport.ViewModeHunks)
		}
	}

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
		"HunksEmptyRevisions": {
			width:  80,
			height: 24,
			setup: func(m *yamlviewport.Model) {
				m.SetViewMode(yamlviewport.ViewModeHunks)
			},
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()

				// Viewport returns blank lines for empty content, not empty string.
				// Check that no YAML keys are present.
				assert.NotContains(t, m.View(), ":")
			},
		},
		"HunksZeroDimensions": {
			width:  0,
			height: 0,
			setup:  hunks(rev1YAML, rev2YAML),
			test: func(t *testing.T, m *yamlviewport.Model) {
				t.Helper()

				assert.Empty(t, m.View())
			},
		},
		"HunksScrollingApplied": {
			width:  80,
			height: 3,
			setup:  hunks(rev1YAML, rev2YAML),
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
				setRevision(m, yamltest.GenerateYAML(50))
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
			setup:  hunks(fiveYAML, fiveChangedYAML),
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
				hunks(fiveYAML, fiveChangedYAML)(m)
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

			m := testModel(testPrinter(), tc.width, tc.height)

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

func TestViewport_CopiesRenderConcurrently(t *testing.T) {
	t.Parallel()

	// A copy shares the row counts of the Model it copied until a layout
	// change gives it its own. A new revision leaves those counts empty, so
	// copies that read the layout at the same time all fill the same counts.
	tcs := map[string]struct {
		setup func(m *yamlviewport.Model)
		read  func(m yamlviewport.Model) string
	}{
		"view": {
			read: func(m yamlviewport.Model) string { return m.View() },
		},
		"view side by side": {
			setup: func(m *yamlviewport.Model) { m.SetViewMode(yamlviewport.ViewModeSideBySide) },
			read:  func(m yamlviewport.Model) string { return m.View() },
		},
		"scroll then view": {
			read: func(m yamlviewport.Model) string {
				m.SetYOffset(2)

				return m.View()
			},
		},
		"total row count": {
			read: func(m yamlviewport.Model) string { return fmt.Sprint(m.TotalRowCount()) },
		},
		"horizontal scroll bound": {
			setup: func(m *yamlviewport.Model) { m.SetWordWrap(false) },
			read: func(m yamlviewport.Model) string {
				m.SetXOffset(math.MaxInt)

				return fmt.Sprint(m.XOffset())
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			newModel := func() yamlviewport.Model {
				m := testModel(printer.New(), 20, 3)
				addRevisions(
					&m,
					stringtest.LinesLF("a: 1", "b: 2"),
					stringtest.LinesLF("a: aaaa bbbb cccc dddd eeee", "b: 3", "c: 4"),
				)

				if tc.setup != nil {
					tc.setup(&m)
				}

				return m
			}

			// A Model of its own reads the same layout alone.
			want := tc.read(newModel())

			m := newModel()
			got := make([]string, 4)

			var wg sync.WaitGroup

			for i := range got {
				wg.Go(func() { got[i] = tc.read(m) })
			}

			wg.Wait()

			for _, g := range got {
				assert.Equal(t, want, g)
			}
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
	setRevision(&m, "key: value\n")

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

	_, ok := m.HitAt(0, 0)
	assert.False(t, ok)
	assert.False(t, m.Reveal(yamlviewport.Hit{}))

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

func TestViewport_ZeroValueSearchIndex(t *testing.T) {
	t.Parallel()

	// A zero Model reported index 0 with no matches until a revision or a
	// new search term reset it.
	tcs := map[string]struct {
		setup func(m *yamlviewport.Model)
	}{
		"untouched": {
			setup: func(*yamlviewport.Model) {},
		},
		"sized": {
			setup: func(m *yamlviewport.Model) {
				m.SetHeight(3)
				m.SetWidth(20)
			},
		},
		"empty search term": {
			setup: func(m *yamlviewport.Model) {
				m.SetSearchTerm("")
				m.SearchNext()
				m.SearchPrevious()
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var m yamlviewport.Model

			tc.setup(&m)

			assert.Equal(t, 0, m.SearchCount())
			assert.Equal(t, -1, m.SearchIndex())
		})
	}
}

func TestViewport_NilPrinterSelectsDefault(t *testing.T) {
	t.Parallel()

	// SetPrinter stored a nil printer as given, so the view went blank and
	// counted no rows while the search still counted its matches.
	setup := func(m *yamlviewport.Model) {
		m.SetWidth(40)
		m.SetHeight(5)
		setRevision(m, stringtest.LinesLF("a: 1", "b: foo", "c: foo"))
		m.SetSearchTerm("foo")
	}

	// The default printer draws the Charm theme in a box.
	ref := yamlviewport.New(yamlviewport.WithPrinter(
		printer.New(printer.WithStyles(theme.Charm), printer.WithBox()),
	))
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

func (c *countingSearcher) Load(lines line.Sequence) yamlviewport.Index {
	c.loads++

	return c.finder.Load(lines)
}

func TestViewport_LayoutChangesKeepSearchIndex(t *testing.T) {
	t.Parallel()

	searcher := &countingSearcher{finder: finder.New()}
	m := testModel(testPrinter(), 80, 10, yamlviewport.WithSearcher(searcher))
	addRevisions(&m, alphaYAML, alphaTwiceYAML)

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
	// restyle the frame on every focus change, or push the value of its
	// search input on every update. A change that leaves the printer, the
	// pane width, and the search matches as they are keeps the row counts,
	// so the next read lays out nothing. Without a diff, the full and hunks
	// view modes show the same lines at the same pane width.
	frame := normalBorder().Width(30)

	tcs := map[string]struct {
		// Steps that run before the row counts fill, if any.
		setup  func(*yamlviewport.Model)
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
		"hunks view mode without diff": {
			change: func(m *yamlviewport.Model, _ *printer.Printer) { m.SetViewMode(yamlviewport.ViewModeHunks) },
		},
		"toggled view mode without diff": {
			change: func(m *yamlviewport.Model, _ *printer.Printer) { m.ToggleViewMode() },
		},
		"same search term": {
			setup: func(m *yamlviewport.Model) {
				m.SetSearchTerm("value")
				m.SearchNext()
			},
			change: func(m *yamlviewport.Model, _ *printer.Printer) { m.SetSearchTerm("value") },
		},
		"clear without search term": {
			change: func(m *yamlviewport.Model, _ *printer.Printer) { m.ClearSearch() },
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// The base style counts every piece of text the printer styles,
			// which a layout of the view does for each line.
			renders := 0
			p := yamltest.NewPlainPrinter(printer.WithStyles(style.New(countingStyle(&renders))))

			m := testModel(p, 40, 10, yamlviewport.WithContainerStyle(frame))
			setRevision(&m, yamltest.GenerateYAML(50))

			if tc.setup != nil {
				tc.setup(&m)
			}

			m.SetYOffset(20)
			m.TotalRowCount()
			require.Positive(t, renders)

			index := m.SearchIndex()
			renders = 0

			tc.change(&m, p)
			m.TotalRowCount()

			assert.Zero(t, renders, "the row counts should come from the cache")
			assert.Equal(t, index, m.SearchIndex())
			assert.Equal(t, 20, m.YOffset())
		})
	}
}

func TestViewport_SideBySideLoadsSearcherOncePerContent(t *testing.T) {
	t.Parallel()

	// Side-by-side search indexes both panes, and like the unified view it
	// loads them once per change of content rather than on every keystroke.
	searcher := &countingSearcher{finder: finder.New()}
	m := testModel(testPrinter(), 80, 10, yamlviewport.WithSearcher(searcher))
	addRevisions(&m, alphaYAML, alphaTwiceYAML)
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

// funcRevision is a [yamlviewport.Revision] whose View calls the func, so
// a nil one panics in View.
type funcRevision func() *line.View

func (funcRevision) Name() string { return "func" }

func (r funcRevision) View() *line.View { return r() }

func TestViewport_ViewModeReusesDiff(t *testing.T) {
	t.Parallel()

	docs := []string{
		threeYAML,
		stringtest.LinesLF("a: 1", "b: 20", "c: 3"),
		stringtest.LinesLF("a: 10", "b: 20", "c: 3", "d: 4"),
	}

	tcs := map[string]struct {
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

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := testModel(testPrinter(), 80, 10)

			revs := make([]*countingRevision, len(docs))
			for i, doc := range docs {
				revs[i] = &countingRevision{Revision: niceyaml.NewSourceFromString(doc)}
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

			want := testModel(testPrinter(), 80, 10)

			for _, i := range tc.history {
				want.AddRevision(niceyaml.NewSourceFromString(docs[i]))
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

	m := testModel(testPrinter(), 80, 10)

	docs := make([]string, 5)
	revs := make([]*countingRevision, len(docs))
	batch := make([]yamlviewport.Revision, len(docs))

	for i := range docs {
		docs[i] = stringtest.LinesLF(fmt.Sprintf("a: %d", i), "b: 2")
		revs[i] = &countingRevision{
			Revision: niceyaml.NewSourceFromString(docs[i], niceyaml.WithName(fmt.Sprintf("v%d", i+1))),
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

	want := testModel(testPrinter(), 80, 10)
	addRevisions(&want, docs...)

	assert.Equal(t, want.RevisionNames(), m.RevisionNames())
	assert.Equal(t, want.DiffStats(), m.DiffStats())
	assert.Equal(t, want.View(), m.View())
}

func TestViewport_WithSearcher(t *testing.T) {
	t.Parallel()

	t.Run("custom searcher is used for search", func(t *testing.T) {
		t.Parallel()

		searcher := &countingSearcher{finder: finder.New()}
		m := testModel(testPrinter(), 80, 10, yamlviewport.WithSearcher(searcher))

		setRevision(&m, "key: value\n")

		m.SetSearchTerm("value")

		assert.Equal(t, "value", m.SearchTerm())
		assert.Positive(t, m.SearchCount())
		assert.Equal(t, 1, searcher.loads)
	})

	t.Run("WithFinder searches through the finder", func(t *testing.T) {
		t.Parallel()

		m := testModel(testPrinter(), 80, 10, yamlviewport.WithFinder(finder.New(finder.WithNormalizer(nil))))
		setRevision(&m, "key: Value\n")

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
		// dereferenced the finder. A Searcher holding a nil pointer also
		// compares unequal to nil, and the first search called Load on it.
		tcs := map[string]yamlviewport.Option{
			"nil searcher":         yamlviewport.WithSearcher(nil),
			"nil finder":           yamlviewport.WithFinder(nil),
			"nil pointer searcher": yamlviewport.WithSearcher((*countingSearcher)(nil)),
		}

		for name, opt := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				m := testModel(testPrinter(), 80, 10, opt)
				setRevision(&m, "key: Value\n")

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

func (r reverseSearcher) Load(lines line.Sequence) yamlviewport.Index {
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

	// The first and last lines hold a match, and the second revision
	// changes a line between them.
	before := yamltest.GenerateLines(40, "k%d: x")
	before = replaceLine(before, 0, "k0: needle")
	before = replaceLine(before, 39, "k39: needle")
	after := replaceLine(before, 20, "k20: y")

	// An Index may return matches in any order, and every view mode steps
	// through them from the top of the document.
	tcs := map[string]yamlviewport.ViewMode{
		"full":         yamlviewport.ViewModeFull,
		"side by side": yamlviewport.ViewModeSideBySide,
	}

	for name, mode := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := testModel(testPrinter(), 80, 5, yamlviewport.WithSearcher(reverseSearcher{finder: finder.New()}))
			addRevisions(&m, before, after)
			m.SetViewMode(mode)

			m.SetSearchTerm("needle")
			require.Equal(t, 2, m.SearchCount())
			assert.Equal(t, 0, m.SearchIndex())
			assert.Equal(t, 0, m.YOffset())
		})
	}
}

// indexSearcher is a [yamlviewport.Searcher] whose Load returns index,
// which may be nil or hold a nil pointer. It counts its Load calls.
type indexSearcher struct {
	index yamlviewport.Index
	loads int
}

func (s *indexSearcher) Load(line.Sequence) yamlviewport.Index {
	s.loads++

	return s.index
}

// ptrIndex is a [yamlviewport.Index] that finds the same ranges for every
// term. Find reads its receiver, so a call through a nil *ptrIndex panics.
type ptrIndex struct {
	matches position.Ranges
}

func (i *ptrIndex) Find(string) position.Ranges {
	return i.matches
}

func TestViewport_SearchLaysOutOnlyHighlightedLines(t *testing.T) {
	t.Parallel()

	rng := func(startLine, startCol, endLine, endCol int) position.Range {
		return position.NewRange(position.New(startLine, startCol), position.New(endLine, endCol))
	}

	// A match that ends at column 0 of a later line highlights nothing on
	// that line. A move of the highlights lays out the same lines, and
	// renders the same view, as for the match that ends at the width of
	// the line before.
	tcs := map[string]struct {
		matches position.Ranges
		want    position.Ranges
	}{
		"ends at column 0 of the next line": {
			matches: position.Ranges{rng(0, 0, 1, 0), rng(2, 0, 3, 0)},
			want:    position.Ranges{rng(0, 0, 0, 4), rng(2, 0, 2, 4)},
		},
		"runs across a line break": {
			matches: position.Ranges{rng(0, 3, 2, 0), rng(2, 3, 3, 0)},
			want:    position.Ranges{rng(0, 3, 1, 4), rng(2, 3, 2, 4)},
		},
	}

	// The run closure makes each change of the highlights in turn. It
	// returns how often the base style ran as each change laid out the
	// rows, and the view after each change. The highlight styles keep the
	// base transform and add an attribute of their own, so the view shows
	// which cells each highlight marks.
	run := func(matches position.Ranges) ([]int, []string) {
		renders := 0
		base := countingStyle(&renders)
		p := yamltest.NewPlainPrinter(printer.WithStyles(style.New(base,
			style.Set(kind.GenericHighlight, base.Underline(true)),
			style.Set(kind.GenericHighlightDim, base.Italic(true)),
		)))

		m := testModel(p, 40, 10, yamlviewport.WithSearcher(&indexSearcher{index: &ptrIndex{matches: matches}}))
		setRevision(&m, stringtest.LinesLF("a: 1", "b: 2", "c: 3", "d: 4"))
		m.TotalRowCount()

		steps := []func(){
			func() { m.SetSearchTerm("x") },
			m.SearchNext,
			m.ClearSearch,
		}

		var counts []int

		var views []string

		for _, step := range steps {
			renders = 0

			step()
			m.TotalRowCount()

			counts = append(counts, renders)
			views = append(views, m.View())
		}

		return counts, views
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			gotCounts, gotViews := run(tc.matches)
			wantCounts, wantViews := run(tc.want)

			assert.Equal(t, wantCounts, gotCounts)
			assert.Equal(t, wantViews, gotViews)
		})
	}
}

func TestViewport_NilIndexFindsNothing(t *testing.T) {
	t.Parallel()

	// A nil Index still counts as loaded, so typing a term loads each pane
	// once rather than on every keystroke. An Index holding a nil pointer
	// compares unequal to nil, and it finds nothing as a nil Index does.
	tcs := map[string]struct {
		index     yamlviewport.Index
		viewMode  yamlviewport.ViewMode
		wantLoads int
	}{
		"nil index/unified": {
			viewMode:  yamlviewport.ViewModeFull,
			wantLoads: 1,
		},
		"nil index/side by side": {
			viewMode:  yamlviewport.ViewModeSideBySide,
			wantLoads: 2,
		},
		"nil pointer index/unified": {
			index:     (*ptrIndex)(nil),
			viewMode:  yamlviewport.ViewModeFull,
			wantLoads: 1,
		},
		"nil pointer index/side by side": {
			index:     (*ptrIndex)(nil),
			viewMode:  yamlviewport.ViewModeSideBySide,
			wantLoads: 2,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			searcher := &indexSearcher{index: tc.index}
			m := testModel(testPrinter(), 80, 10, yamlviewport.WithSearcher(searcher))
			addRevisions(&m, "a: 1\n", "a: 2\n")
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

func TestViewport_SideBySideSelectedMatch(t *testing.T) {
	t.Parallel()

	const (
		selected = "<genericHighlight>"
		dim      = "<genericHighlightDim>"
	)

	twoLines := stringtest.LinesLF("a: 1", "b: 2")
	withBlank := stringtest.LinesLF("a: 1", "", "b: 2")

	// The panes sit either side of a vertical bar. As the search steps
	// through every match, each view draws the selected highlight once, and
	// each pane draws it as many times as it holds a match.
	tcs := map[string]struct {
		before    string
		after     string
		term      string
		wantLeft  int
		wantRight int
	}{
		"match on an inserted line": {
			// The diff pads the left pane opposite an inserted line with an
			// empty line that carries the default flag, the same flag an
			// equal line carries. A match selected on the inserted line
			// belongs to the right pane alone, so only the right pane draws
			// the selected highlight.
			before:    twoLines,
			after:     stringtest.LinesLF("a: 1", "find: me", "b: 2"),
			term:      "find",
			wantRight: 1,
		},
		// A term with a leading newline starts its match on the line before
		// the text, so on a blank inserted or deleted line the match in the
		// other pane starts on the padding opposite it. Both matches count.
		"match starting on padding opposite an inserted line": {
			before:    twoLines,
			after:     withBlank,
			term:      "\nb",
			wantLeft:  1,
			wantRight: 1,
		},
		"match starting on padding opposite a deleted line": {
			before:    withBlank,
			after:     twoLines,
			term:      "\nb",
			wantLeft:  1,
			wantRight: 1,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := testModel(testPrinterWithSearch(), 100, 8)
			addRevisions(&m, tc.before, tc.after)
			m.SetViewMode(yamlviewport.ViewModeSideBySide)

			m.SetSearchTerm(tc.term)
			require.Equal(t, tc.wantLeft+tc.wantRight, m.SearchCount())

			var leftCount, rightCount int

			for range m.SearchCount() {
				view := m.View()
				require.Equal(t, 1, strings.Count(view, selected))

				// Every other match draws the dim highlight.
				assert.Equal(t, m.SearchCount()-1, strings.Count(view, dim))

				left, right := selectedPanes(t, view)
				leftCount += strings.Count(left, selected)
				rightCount += strings.Count(right, selected)

				m.SearchNext()
			}

			assert.Equal(t, tc.wantLeft, leftCount)
			assert.Equal(t, tc.wantRight, rightCount)
		})
	}
}

func TestViewport_OffsetWithNoContentHeight(t *testing.T) {
	t.Parallel()

	// The offset is the index of the row at the top of the view, and a
	// content area with no height once let it reach the row count itself,
	// one row past the last row of the view. The scroll percentage must
	// still reach 1 at the last row.
	tcs := map[string]struct {
		container lipgloss.Style
		height    int
		lines     int
	}{
		"no height": {
			container: lipgloss.NewStyle(),
			height:    0,
			lines:     20,
		},
		"frame as tall as height": {
			container: normalBorder(),
			height:    2,
			lines:     20,
		},
		"no height single row": {
			container: lipgloss.NewStyle(),
			height:    0,
			lines:     1,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := testModel(testPrinter(), 40, tc.height, yamlviewport.WithContainerStyle(tc.container))
			setRevision(&m, yamltest.GenerateYAML(tc.lines))

			require.Equal(t, tc.lines, m.TotalRowCount())

			m.GotoBottom()
			assert.Equal(t, m.TotalRowCount()-1, m.YOffset())
			assert.True(t, m.AtBottom())
			assert.InDelta(t, 1.0, m.ScrollPercent(), 0.01)

			m.SetYOffset(1000)
			assert.Equal(t, m.TotalRowCount()-1, m.YOffset())
		})
	}
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

	src := "long: " + strings.Repeat("x", 200) + "\n" + yamltest.GenerateYAML(50)

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := testModel(testPrinter(), 40, 5)
			m.SetWordWrap(false)
			setRevision(&m, src)
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
			source := niceyaml.NewSourceFromString(stringtest.LinesLF(
				"a: 1", "k: v", "c: 3", "d: 4", "e: 5", "f: 6", "g: 7", "h: 8", "i: 9", "j: 10",
			))
			view := source.View()
			view.Annotate(1, line.Annotation{Content: "BAD", Placement: line.Below, Cols: position.NewSpan(50, 50)})

			p := testPrinterWithLineNumbers().With(
				printer.WithContainerStyle(normalBorder()),
			)
			m := testModel(p, width, 10)
			m.SetViewMode(tc.mode)
			m.SetRevision(yamlviewport.NewRevision(source.Name(), view))
			require.True(t, m.WordWrap())

			out := m.View()
			requireFrame(t, &m, nil, tc.cols)
			assert.NotContains(t, out, "^")
			assert.Less(t, m.HorizontalScrollPercent(), 1.0)

			m.SetXOffset(1000)
			assert.Positive(t, m.XOffset())
			assert.InDelta(t, 1.0, m.HorizontalScrollPercent(), 0.01)

			out = m.View()
			requireFrame(t, &m, nil, tc.cols)
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
				setRevision(m, short)
			},
			// Gutter 6 + content 42 - width 20.
			wantMax: 28,
		},
		"line number gutter and border": {
			printer: testPrinterWithLineNumbers().With(
				printer.WithContainerStyle(normalBorder()),
			),
			width: 20,
			setup: func(m *yamlviewport.Model) {
				setRevision(m, short)
			},
			// Gutter 6 + content 42 - (width 20 - border 2).
			wantMax: 30,
		},
		"side by side": {
			printer: testPrinter(),
			width:   60,
			setup: func(m *yamlviewport.Model) {
				before := strings.ReplaceAll(short, "x", "z")
				addRevisions(m, before, short)
				m.SetViewMode(yamlviewport.ViewModeSideBySide)
			},
			// Gutter 1 + content 42 - pane (60 - 3) / 2.
			wantMax: 15,
		},
		"side by side with a longer right pane": {
			printer: testPrinter(),
			width:   40,
			setup: func(m *yamlviewport.Model) {
				addRevisions(m, "k: short\n", long)
				m.SetViewMode(yamlviewport.ViewModeSideBySide)
			},
			// Gutter 1 + content 63 - pane (40 - 3) / 2.
			wantMax: 46,
		},
		"wide characters": {
			printer: testPrinterWithLineNumbers(),
			width:   21,
			setup: func(m *yamlviewport.Model) {
				setRevision(m, "k: "+strings.Repeat("\u65e5", 20)+"END\n")
			},
			// Gutter 6 + content 46 cells - width 21. Each wide glyph takes
			// two cells, so a rune count leaves END out of reach.
			wantMax: 31,
		},
		"hunk header wider than content": {
			printer: testPrinter(),
			width:   14,
			setup: func(m *yamlviewport.Model) {
				addRevisions(m, threeYAML, threeChangedYAML)
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

			m := testModel(tc.printer, tc.width, 5)
			m.SetWordWrap(false)
			tc.setup(&m)

			assert.InDelta(t, 0.0, m.HorizontalScrollPercent(), 0.01)

			tail := cmp.Or(tc.tail, "END")

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

func TestViewport_SideBySidePanesShareGutterWidth(t *testing.T) {
	t.Parallel()

	// The before revision numbers its gutter for five digits and the after
	// revision for two, but the panes cut at one shared horizontal offset, so
	// they need one gutter width. Sized per pane, the narrower gutter runs
	// out first and the panes show different columns of the same line.
	//
	// A first line long enough to scroll past the widest gutter.
	first := "k0: " + strings.Repeat("0123456789", 4)
	before := replaceLine(yamltest.GenerateLines(10005, "k%d: v"), 0, first)

	m := testModel(testPrinterWithLineNumbers(), 60, 5)
	m.SetWordWrap(false)
	addRevisions(&m, before, stringtest.LinesLF(first, "last: v"))
	m.SetViewMode(yamlviewport.ViewModeSideBySide)

	m.SetXOffset(7)
	require.Equal(t, 7, m.XOffset())

	// The first line is equal in both revisions, so both panes show it from
	// the same column.
	left, right := panes(t, firstRow(m.View()))
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
	m := testModel(p, 40, 10)
	m.SetViewMode(yamlviewport.ViewModeSideBySide)
	setRevision(&m, threeYAML)
	m.TotalRowCount()

	clear(gutter.renders)

	rows := strings.Split(m.View(), "\n")

	for i, want := range []string{"a: 1", "b: 2", "c: 3"} {
		left, right := panes(t, rows[i])
		assert.Contains(t, left, want)
		assert.Contains(t, right, want)
	}

	for number := 1; number <= 3; number++ {
		assert.Equal(t, 1, gutter.renders[number], "line %d", number)
	}
}

func TestViewport_SideBySideSearchCountsEqualLineOnce(t *testing.T) {
	t.Parallel()

	// Both panes show the equal line "bar: b", but its match counts once.
	// TestViewport_ClearSearchSideBySide counts a match on each side of a
	// changed line.
	m := testModel(testPrinter(), 80, 24)
	addRevisions(&m, stringtest.JoinLF("foo: a", "bar: b"), stringtest.JoinLF("foo: c", "bar: b"))
	m.GotoRevision(1)
	m.SetViewMode(yamlviewport.ViewModeSideBySide)
	m.SetSearchTerm("bar")

	assert.Equal(t, 1, m.SearchCount())
}

func TestViewport_DoesNotMutateSource(t *testing.T) {
	t.Parallel()

	m := testModel(testPrinter(), 80, 10)

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
	p := testPrinterWithSearch().With(printer.WithGutter(printer.LineNumberGutter))

	m := testModel(p, 80, 2)

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

	p := yamltest.NewPlainPrinter(
		printer.WithStyles(yamltest.NewXMLStyles(yamltest.XMLStyleInclude(kind.GenericHighlight, kind.GenericError))),
	)

	m := testModel(p, 80, 10)

	source := niceyaml.NewSourceFromString(
		stringtest.LinesLF("key: value", "other: thing"),
		niceyaml.WithName("marked"),
	)
	view := source.View()
	view.Annotate(0, line.Annotation{Content: "note", Placement: line.Below})
	view.AddOverlay(kind.GenericError, yamltest.LineRange(1, 0, 5))

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

			p := yamltest.NewPlainPrinter(
				printer.WithStyles(yamltest.NewXMLStyles(yamltest.XMLStyleInclude(kind.GenericHighlight))),
			)

			m := testModel(p, 80, 10)

			source := niceyaml.NewSourceFromString(
				stringtest.LinesLF("key: value", "other: value"),
				niceyaml.WithName(name),
			)
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

// markedRevision returns a revision named name over doc. Each line in marks
// carries an error overlay on its key and an annotation below the key that
// reads name and the key, so a mark on screen names the revision it came
// from.
func markedRevision(name, doc string, marks ...int) yamlviewport.Revision {
	view := niceyaml.NewSourceFromString(doc, niceyaml.WithName(name)).View()

	for _, i := range marks {
		field, _, _ := strings.Cut(view.Line(i).Content(), ":")

		view.AddOverlay(kind.GenericError, yamltest.LineRange(i, 0, len(field)))
		view.Annotate(i, line.Annotation{
			Content:   name + " " + field,
			Placement: line.Below,
			Cols:      position.NewSpan(0, len(field)),
		})
	}

	return yamlviewport.NewRevision(name, view)
}

// markPrinter returns [testPrinter] with styles that show the text under an
// error overlay in upper case.
func markPrinter() *printer.Printer {
	upper := lipgloss.NewStyle().Transform(strings.ToUpper)

	return testPrinter().With(printer.WithStyles(
		style.New(lipgloss.NewStyle(), style.Set(kind.GenericError, upper)),
	))
}

// plainRows returns the rows of the view of m down to the last one that
// shows text, without their styling or the padding at the end of each.
func plainRows(m *yamlviewport.Model) string {
	return strings.TrimRight(stringtest.TrimLineEnds(ansi.Strip(m.View())), "\n")
}

func TestViewport_DiffKeepsMarks(t *testing.T) {
	t.Parallel()

	// Each revision marks the line every revision changes, an unchanged
	// line beside it, and an unchanged line that a hunk context of 1
	// leaves out.
	doc := func(replicas int) string {
		return stringtest.LinesLF(
			"name: app",
			fmt.Sprintf("replicas: %d", replicas),
			"port: 80",
			"mode: x",
			"image: old",
		)
	}

	// The viewport shows v3. A deleted line and every line of the left pane
	// carry the marks of the revision the diff compares with, which is v2
	// for an adjacent diff and v1 for a diff with the origin. Every other
	// line carries the marks of v3.
	tcs := map[string]struct {
		want     string
		diffMode yamlviewport.DiffMode
		viewMode yamlviewport.ViewMode
	}{
		"adjacent full": {
			diffMode: yamlviewport.DiffModeAdjacent,
			viewMode: yamlviewport.ViewModeFull,
			want: stringtest.JoinLF(
				" name: app",
				"-REPLICAS: 2",
				" ^^^^^^^^ v2 replicas",
				"+REPLICAS: 3",
				" ^^^^^^^^ v3 replicas",
				" PORT: 80",
				" ^^^^ v3 port",
				" mode: x",
				" IMAGE: old",
				" ^^^^^ v3 image",
			),
		},
		"adjacent hunks": {
			diffMode: yamlviewport.DiffModeAdjacent,
			viewMode: yamlviewport.ViewModeHunks,
			want: stringtest.JoinLF(
				" @@ -1,3 +1,3 @@",
				" name: app",
				"-REPLICAS: 2",
				" ^^^^^^^^ v2 replicas",
				"+REPLICAS: 3",
				" ^^^^^^^^ v3 replicas",
				" PORT: 80",
				" ^^^^ v3 port",
			),
		},
		"adjacent side by side": {
			diffMode: yamlviewport.DiffModeAdjacent,
			viewMode: yamlviewport.ViewModeSideBySide,
			want: stringtest.JoinLF(
				" name: app               │  name: app",
				"-REPLICAS: 2             │ +REPLICAS: 3",
				" ^^^^^^^^ v2 replicas    │  ^^^^^^^^ v3 replicas",
				" PORT: 80                │  PORT: 80",
				" ^^^^ v2 port            │  ^^^^ v3 port",
				" mode: x                 │  mode: x",
				" IMAGE: old              │  IMAGE: old",
				" ^^^^^ v2 image          │  ^^^^^ v3 image",
			),
		},
		"origin full": {
			diffMode: yamlviewport.DiffModeOrigin,
			viewMode: yamlviewport.ViewModeFull,
			want: stringtest.JoinLF(
				" name: app",
				"-REPLICAS: 1",
				" ^^^^^^^^ v1 replicas",
				"+REPLICAS: 3",
				" ^^^^^^^^ v3 replicas",
				" PORT: 80",
				" ^^^^ v3 port",
				" mode: x",
				" IMAGE: old",
				" ^^^^^ v3 image",
			),
		},
		"origin hunks": {
			diffMode: yamlviewport.DiffModeOrigin,
			viewMode: yamlviewport.ViewModeHunks,
			want: stringtest.JoinLF(
				" @@ -1,3 +1,3 @@",
				" name: app",
				"-REPLICAS: 1",
				" ^^^^^^^^ v1 replicas",
				"+REPLICAS: 3",
				" ^^^^^^^^ v3 replicas",
				" PORT: 80",
				" ^^^^ v3 port",
			),
		},
		"origin side by side": {
			diffMode: yamlviewport.DiffModeOrigin,
			viewMode: yamlviewport.ViewModeSideBySide,
			want: stringtest.JoinLF(
				" name: app               │  name: app",
				"-REPLICAS: 1             │ +REPLICAS: 3",
				" ^^^^^^^^ v1 replicas    │  ^^^^^^^^ v3 replicas",
				" PORT: 80                │  PORT: 80",
				" ^^^^ v1 port            │  ^^^^ v3 port",
				" mode: x                 │  mode: x",
				" IMAGE: old              │  IMAGE: old",
				" ^^^^^ v1 image          │  ^^^^^ v3 image",
			),
		},
		"no diff": {
			diffMode: yamlviewport.DiffModeNone,
			viewMode: yamlviewport.ViewModeFull,
			want: stringtest.JoinLF(
				" name: app",
				" REPLICAS: 3",
				" ^^^^^^^^ v3 replicas",
				" PORT: 80",
				" ^^^^ v3 port",
				" mode: x",
				" IMAGE: old",
				" ^^^^^ v3 image",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := testModel(markPrinter(), 51, 12)
			m.SetHunkContext(1)
			m.SetDiffMode(tc.diffMode)
			m.SetViewMode(tc.viewMode)
			m.AddRevisions(
				markedRevision("v1", doc(1), 1, 2, 4),
				markedRevision("v2", doc(2), 1, 2, 4),
				markedRevision("v3", doc(3), 1, 2, 4),
			)

			assert.Equal(t, tc.want, plainRows(&m))

			// Search highlights sit beside the marks rather than replacing
			// them.
			m.SetSearchTerm("app")
			require.Equal(t, 1, m.SearchCount())

			assert.Equal(t, tc.want, plainRows(&m))
		})
	}
}

func TestViewport_DiffKeepsMarksOfSharedLines(t *testing.T) {
	t.Parallel()

	// Two views of one source hold the same lines, so the line alone does
	// not tell which revision a mark belongs to. Each line of the diff
	// still carries the marks of one revision.
	tcs := map[string]struct {
		want     string
		viewMode yamlviewport.ViewMode
	}{
		"full": {
			viewMode: yamlviewport.ViewModeFull,
			want: stringtest.JoinLF(
				" x: 1",
				" ^ after",
				" y: 2",
			),
		},
		"hunks": {
			viewMode: yamlviewport.ViewModeHunks,
			want:     "",
		},
		"side by side": {
			viewMode: yamlviewport.ViewModeSideBySide,
			want: stringtest.JoinLF(
				" x: 1                 │  x: 1",
				" ^ before             │  ^ after",
				" y: 2                 │  y: 2",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(stringtest.LinesLF("x: 1", "y: 2"))

			before, after := source.View(), source.View()
			before.Annotate(0, line.Annotation{Content: "before", Placement: line.Below})
			after.Annotate(0, line.Annotation{Content: "after", Placement: line.Below})

			m := testModel(testPrinter(), 45, 5)
			m.SetViewMode(tc.viewMode)
			m.AddRevisions(
				yamlviewport.NewRevision("before", before),
				yamlviewport.NewRevision("after", after),
			)
			require.True(t, m.ShowingDiff())

			assert.Equal(t, tc.want, plainRows(&m))
		})
	}
}

func TestViewport_DiffKeepsHunkHeaderAboveMarks(t *testing.T) {
	t.Parallel()

	// The first line of a hunk carries the header of the hunk, and an
	// annotation a revision puts above that line renders below the header.
	after := niceyaml.NewSourceFromString(threeChangedYAML).View()
	after.Annotate(0, line.Annotation{Content: "note", Placement: line.Above})

	m := testModel(testPrinter(), 30, 8)
	m.SetViewMode(yamlviewport.ViewModeHunks)
	m.AddRevisions(niceyaml.NewSourceFromString(threeYAML), yamlviewport.NewRevision("after", after))

	want := stringtest.JoinLF(
		" @@ -1,3 +1,3 @@",
		" note",
		" a: 1",
		"-b: 2",
		"+b: 9",
		" c: 3",
	)

	assert.Equal(t, want, plainRows(&m))
}

func TestViewport_DiffIgnoresLaterMarks(t *testing.T) {
	t.Parallel()

	// The viewport keeps the marks the views carried when it computed the
	// diff, so every view of that diff shows the same marks. A mark the
	// caller adds later shows once the revisions are set again.
	tcs := map[string]struct {
		act func(*yamlviewport.Model)
	}{
		"hunks": {
			act: func(m *yamlviewport.Model) { m.SetViewMode(yamlviewport.ViewModeHunks) },
		},
		"side by side": {
			act: func(m *yamlviewport.Model) { m.SetViewMode(yamlviewport.ViewModeSideBySide) },
		},
		"hunk context": {
			act: func(m *yamlviewport.Model) {
				m.SetViewMode(yamlviewport.ViewModeHunks)
				m.SetHunkContext(1)
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			before := niceyaml.NewSourceFromString(threeYAML).View()
			after := niceyaml.NewSourceFromString(threeChangedYAML).View()

			revisions := []yamlviewport.Revision{
				yamlviewport.NewRevision("before", before),
				yamlviewport.NewRevision("after", after),
			}

			m := testModel(testPrinter(), 45, 10)
			m.AddRevisions(revisions...)

			before.Annotate(1, line.Annotation{Content: "late before", Placement: line.Below})
			after.Annotate(1, line.Annotation{Content: "late after", Placement: line.Below})

			tc.act(&m)

			assert.NotContains(t, m.View(), "late")

			viewMode := m.ViewMode()

			m.ClearRevisions()
			m.AddRevisions(revisions...)

			require.Equal(t, viewMode, m.ViewMode())
			assert.Contains(t, m.View(), "^ late before")
			assert.Contains(t, m.View(), "^ late after")
		})
	}
}

func TestViewport_ContentChangesResetSearch(t *testing.T) {
	t.Parallel()

	before, after := needleRevisions()

	tcs := map[string]struct {
		change func(m *yamlviewport.Model)
	}{
		"add revision": {
			change: func(m *yamlviewport.Model) {
				addRevisions(m, before)
			},
		},
		"set source": {
			change: func(m *yamlviewport.Model) {
				setRevision(m, after)
			},
		},
		"diff mode": {
			change: func(m *yamlviewport.Model) { m.SetDiffMode(yamlviewport.DiffModeNone) },
		},
		"view mode": {
			change: func(m *yamlviewport.Model) { m.SetViewMode(yamlviewport.ViewModeSideBySide) },
		},
		"previous revision": {
			change: func(m *yamlviewport.Model) { m.PreviousRevision() },
		},
		"goto revision": {
			change: func(m *yamlviewport.Model) {
				m.GotoRevision(0)
				m.SearchNext()
				m.GotoRevision(1)
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := testModel(testPrinter(), 80, 5)
			addRevisions(&m, before, after)

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
		return value + ": " + strings.Repeat("word ", 40) + "needle"
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
				setRevision(m, stringtest.LinesLF("a: 1", long("k"), "y: 2", "z: 3"))
			},
			wantRow: 1,
		},
		"middle row with line numbers": {
			printer: testPrinterWithLineNumbers(),
			setup: func(m *yamlviewport.Model) {
				m.SetWidth(30)
				setRevision(m, stringtest.LinesLF(
					"a: 1",
					"k: "+strings.Repeat("word ", 20)+"needle "+strings.Repeat("word ", 20),
					"z: 2",
				))
			},
			wantRow: 1,
		},
		"hunk header above the line": {
			setup: func(m *yamlviewport.Model) {
				addRevisions(m, "k: old\n", long("k")+"\n")
				m.SetViewMode(yamlviewport.ViewModeHunks)
			},
			wantRow: -1,
		},
		"right pane": {
			setup: func(m *yamlviewport.Model) {
				addRevisions(m, "k: old\n", long("k")+"\n")
				m.SetViewMode(yamlviewport.ViewModeSideBySide)
				m.SetWidth(43)
			},
			wantRow: -1,
		},
		// The renderer escapes a control character to one rune, so the row
		// the match lands on is the same as with a printable character in its
		// place. Walking the raw content instead stops at the control
		// character and centers the last row of the line.
		"printable character before the match": {
			setup: func(m *yamlviewport.Model) {
				setRevision(m, separated("x"))
			},
			wantRow: 1,
		},
		"control character before the match": {
			setup: func(m *yamlviewport.Model) {
				setRevision(m, separated("\a"))
			},
			wantRow: 1,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := testModel(tc.printer, 20, 4)
			tc.setup(&m)

			m.SetSearchTerm("needle")
			require.Equal(t, 1, m.SearchCount())

			// The view centers the row that holds the match, not the first
			// row of its line.
			assert.Positive(t, m.YOffset())

			got := rowIndex(m.View(), "needle")
			require.NotEqual(t, -1, got, "match not on screen:\n%s", m.View())

			if tc.wantRow >= 0 {
				assert.Equal(t, tc.wantRow, got)
			}
		})
	}
}

func TestViewport_ContentChangesScrollToTop(t *testing.T) {
	t.Parallel()

	before := yamltest.GenerateLines(100, "key%d: value")
	after := replaceLine(before, 60, "key60: changed")

	tcs := map[string]struct {
		change  func(m *yamlviewport.Model)
		wantTop string
	}{
		"add revision": {
			change: func(m *yamlviewport.Model) {
				addRevisions(m, after)
			},
			wantTop: "key0: value",
		},
		"set source": {
			change: func(m *yamlviewport.Model) {
				setRevision(m, after)
			},
			wantTop: "key0: value",
		},
		"diff mode": {
			change: func(m *yamlviewport.Model) {
				addRevisions(m, after)
				m.SetYOffset(50)
				m.SetDiffMode(yamlviewport.DiffModeNone)
			},
			wantTop: "key0: value",
		},
		"hunks": {
			change: func(m *yamlviewport.Model) {
				addRevisions(m, after)
				m.SetYOffset(50)
				m.SetViewMode(yamlviewport.ViewModeHunks)
			},
			wantTop: "@@",
		},
		"previous revision": {
			change: func(m *yamlviewport.Model) {
				addRevisions(m, after)
				m.SetYOffset(50)
				m.PreviousRevision()
			},
			wantTop: "key0: value",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := testModel(testPrinter(), 80, 4)
			addRevisions(&m, before)
			m.SetYOffset(50)
			require.Equal(t, 50, m.YOffset())

			// The row offset of the old content points at an arbitrary line
			// of the new, so the new content starts at its first row.
			tc.change(&m)

			assert.Equal(t, 0, m.YOffset())

			assert.Contains(t, firstRow(m.View()), tc.wantTop)
		})
	}
}

func TestViewport_SearchAcrossRevisions(t *testing.T) {
	t.Parallel()

	m := testModel(testPrinter(), 80, 10)

	addRevisions(&m, alphaYAML)
	m.SetSearchTerm("alpha")
	assert.Equal(t, 1, m.SearchCount())

	// A new revision changes the displayed content, so the viewport
	// recomputes the matches against the diff rather than the stale index.
	addRevisions(&m, alphaTwiceYAML)
	assert.Equal(t, 2, m.SearchCount())

	m.SetSearchTerm("other")
	assert.Equal(t, 1, m.SearchCount())
}

func TestViewport_NewSearchTermStartsAtFirstMatch(t *testing.T) {
	t.Parallel()

	m := testModel(testPrinter(), 80, 5)
	setRevision(&m, yamltest.GenerateLines(40, "k%d: needle"))

	m.SetSearchTerm("needle")
	searchNext(&m, 7)
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

func TestViewport_SideBySideSearchOrder(t *testing.T) {
	t.Parallel()

	// Every line changes, so each line holds a deleted match in the left pane
	// and an inserted match at the same position in the right pane.
	m := testModel(testPrinterWithSearch(), 160, 3)
	addRevisions(&m, yamltest.GenerateLines(20, "k%02d: needle a"), yamltest.GenerateLines(20, "k%02d: needle b"))
	m.SetViewMode(yamlviewport.ViewModeSideBySide)

	m.SetSearchTerm("needle")
	require.Equal(t, 40, m.SearchCount())

	// Matches step through the panes left, right on each line in turn.
	for i := range m.SearchCount() {
		require.Equal(t, i, m.SearchIndex())

		left, right := selectedPanes(t, m.View())
		assert.Contains(t, left+right, fmt.Sprintf("k%02d:", i/2), "match %d", i)

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
		m := testModel(testPrinterWithSearch(), 120, 5)
		addRevisions(&m, stringtest.LinesLF("foo: a", "bar: b"), stringtest.LinesLF("foo: c", "bar: b"))
		m.SetViewMode(yamlviewport.ViewModeSideBySide)

		return m
	}

	plain := newModel()
	m := newModel()

	// The deleted and the inserted side of the changed line each hold a
	// match.
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
			revisions:  []string{stringtest.LinesLF("k: "+strings.Repeat("-", 60)+"NEEDLE", "z: 1")},
			width:      30,
			wantScroll: true,
		},
		"wide runes before the match": {
			// Each rune before the needle takes two cells, so the cell of
			// the match is twice its column, and an offset counted in
			// runes would leave the needle off screen.
			revisions:  []string{stringtest.LinesLF("k: "+strings.Repeat("日本", 15)+"NEEDLE", "z: 1")},
			width:      30,
			wantScroll: true,
		},
		"match inside the first screen": {
			revisions:  []string{stringtest.LinesLF("k: NEEDLE "+strings.Repeat("-", 60), "z: 1")},
			width:      30,
			wantScroll: false,
		},
		"match right of center inside the first screen": {
			revisions: []string{
				stringtest.LinesLF("key: "+strings.Repeat("-", 20)+"NEEDLE"+strings.Repeat("-", 40), "z: 1"),
			},
			width:      40,
			wantScroll: false,
		},
		"match that ends at the edge of the first screen": {
			// The one-cell gutter, "key: ", and the dashes take 34 cells,
			// so the needle ends on the last of the 40.
			revisions: []string{
				stringtest.LinesLF("key: "+strings.Repeat("-", 28)+"NEEDLE"+strings.Repeat("-", 40), "z: 1"),
			},
			width:      40,
			wantScroll: false,
		},
		"match that ends one cell past the first screen": {
			revisions: []string{
				stringtest.LinesLF("key: "+strings.Repeat("-", 29)+"NEEDLE"+strings.Repeat("-", 40), "z: 1"),
			},
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
			revisions:  []string{stringtest.LinesLF("k: "+strings.Repeat("-", 60)+"NEEDLE", "z: 1")},
			width:      30,
			wrap:       true,
			wantScroll: false,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := testModel(testPrinter(), tc.width, 5)
			m.SetViewMode(tc.mode)
			m.SetWordWrap(tc.wrap)

			addRevisions(&m, tc.revisions...)

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
	p := yamltest.NewPlainPrinter(printer.WithStyles(highlightStyles(bracketStyle("{", "}"), bracketStyle("[", "]"))))

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

			m := testModel(p, 40, 3)
			m.SetWordWrap(false)
			setRevision(&m, "k: "+strings.Repeat("ab ", 30)+"\n")

			m.SetSearchTerm("ab")
			require.Equal(t, 30, m.SearchCount())

			searchNext(&m, tc.next)

			assert.Contains(t, ansi.Strip(m.View()), "{ab}")
		})
	}
}

func TestViewport_ContentChangeResetsHorizontalScroll(t *testing.T) {
	t.Parallel()

	// A search match scrolls the view to the right, and content that holds
	// no match starts from the first column again rather than keeping an
	// offset that described other lines.
	m := testModel(testPrinter(), 30, 5)
	m.SetWordWrap(false)
	setRevision(&m, "k: "+strings.Repeat("-", 60)+"NEEDLE\n")

	m.SetSearchTerm("NEEDLE")
	require.Positive(t, m.XOffset())

	setRevision(&m, "k: "+strings.Repeat("-", 60)+"\n")
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
	m := testModel(testPrinterWithLineNumbers(), 6, 4)
	m.AddRevision(niceyaml.NewSourceFromString(stringtest.LinesLF("a: 1", "b: 2", "c: 3", "d: 4", "e: 5")))

	// Every screen row opens with the gutter, which a wrapped row would
	// not, and the last row of the view at the largest offset is a row of
	// the last line.
	gutterRows := func() []string {
		t.Helper()

		rows := requireRows(t, m.View(), 4)

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
	m := testModel(testPrinter(), 40, 10)
	m.AddRevision(niceyaml.NewSourceFromString(yamltest.GenerateLines(50, "k%d: v")))
	m.SetSearchTerm("v")
	searchNext(&m, 3)
	m.SetYOffset(30)

	require.Equal(t, 3, m.SearchIndex())
	require.Equal(t, 30, m.YOffset())

	// The changes run in turn on the one model.
	steps := []struct {
		name   string
		change func()
	}{
		// One revision shows no diff in any diff mode.
		{name: "toggle diff mode", change: m.ToggleDiffMode},
		{name: "set diff mode", change: func() { m.SetDiffMode(yamlviewport.DiffModeNone) }},
		{name: "same view mode", change: func() { m.SetViewMode(m.ViewMode()) }},
		{name: "same revision", change: func() { m.GotoRevision(m.RevisionIndex()) }},
		{name: "same hunk context", change: func() { m.SetHunkContext(m.HunkContext()) }},

		// Without a diff, hunks mode shows the same lines and the hunk
		// context shapes nothing on screen.
		{name: "hunks mode without diff", change: func() { m.SetViewMode(yamlviewport.ViewModeHunks) }},
		{name: "hunk context without diff", change: func() { m.SetHunkContext(m.HunkContext() + 2) }},
	}

	for _, step := range steps {
		step.change()

		assert.Equal(t, 3, m.SearchIndex(), step.name)
		assert.Equal(t, 30, m.YOffset(), step.name)
	}
}

// screenMessage returns the view of m without its styling, as the message
// of an assertion about what the view shows.
func screenMessage(m *yamlviewport.Model) string {
	return "screen:\n" + ansi.Strip(m.View())
}

// hitMap returns one row for each row of the viewport, with one character
// for each of its cells: the index of the revision [yamlviewport.Model.HitAt]
// finds there, or "." for a cell it reports false for.
func hitMap(m *yamlviewport.Model) string {
	rows := make([]string, 0, m.Height())

	for y := range m.Height() {
		var sb strings.Builder

		for x := range m.Width() {
			if hit, ok := m.HitAt(x, y); ok {
				fmt.Fprint(&sb, hit.Revision)
			} else {
				sb.WriteByte('.')
			}
		}

		rows = append(rows, sb.String())
	}

	return stringtest.JoinLF(rows...)
}

// screenCells returns the grapheme cluster each cell of each row of screen
// shows, a rendered view. A cluster two cells wide fills both of its cells.
func screenCells(screen string) [][]string {
	var cells [][]string

	for row := range strings.SplitSeq(ansi.Strip(screen), "\n") {
		var rowCells []string

		for row != "" {
			cluster, width := ansi.FirstGraphemeCluster(row, ansi.GraphemeWidth)
			for range width {
				rowCells = append(rowCells, cluster)
			}

			row = row[len(cluster):]
		}

		cells = append(cells, rowCells)
	}

	return cells
}

func TestViewport_HitAt_Screen(t *testing.T) {
	t.Parallel()

	framed := testPrinterWithLineNumbers().With(printer.WithContainerStyle(normalBorder().Padding(0, 1)))

	// Each want holds the hit map of the screen above it, which the
	// comments draw with "·" for a space.
	tcs := map[string]struct {
		setup     func(m *yamlviewport.Model)
		printer   *printer.Printer
		style     lipgloss.Style
		want      string
		revisions []string
		clip      int
		width     int
		height    int
	}{
		"clipped lines": {
			revisions: []string{hitV1},
			clip:      4,
			width:     16,
			height:    3,
			want: stringtest.JoinLF(
				".0000...........", // ·a: 1
				".00000..........", // ·b: 日...
				".0000...........", // ·c: 3
			),
		},
		"clipped lines scrolled sideways": {
			revisions: []string{hitV1},
			clip:      4,
			width:     6,
			height:    3,
			setup: func(m *yamlviewport.Model) {
				m.SetWordWrap(false)
				m.SetXOffset(3)
			},
			want: stringtest.JoinLF(
				"00....", // ·1
				"000...", // ·日...
				"00....", // ·3
			),
		},
		"one revision": {
			revisions: []string{hitV1},
			width:     16,
			height:    4,
			want: stringtest.JoinLF(
				".0000...........", // ·a: 1
				".00000000000....", // ·b: 日本語 x
				".0000...........", // ·c: 3
				"................",
			),
		},
		// The window shows a space in place of the half of a wide
		// character that it cuts, and the space gives that character.
		"wide character the right edge cuts": {
			revisions: []string{hitV1},
			width:     8,
			height:    3,
			setup: func(m *yamlviewport.Model) {
				m.SetWordWrap(false)
				m.SetXOffset(1)
			},
			want: stringtest.JoinLF(
				"0000....", // a: 1
				"00000000", // b: 日本·
				"0000....", // c: 3
			),
		},
		"wide character the left edge cuts": {
			revisions: []string{hitV1},
			width:     6,
			height:    3,
			setup: func(m *yamlviewport.Model) {
				m.SetWordWrap(false)
				m.SetXOffset(5)
			},
			want: stringtest.JoinLF(
				"......",
				"000000", // ·本語·
				"......",
			),
		},
		"scrolled down": {
			revisions: []string{hitV2},
			width:     16,
			height:    2,
			setup: func(m *yamlviewport.Model) {
				m.SetYOffset(2)
			},
			want: stringtest.JoinLF(
				".00000000000000.", // ·new: line here
				".0000...........", // ·c: 3
			),
		},
		"wrapped": {
			revisions: []string{hitV2},
			width:     11,
			height:    5,
			want: stringtest.JoinLF(
				".0000......", // ·a: 1
				".000000000.", // ·b: 日本 y
				".000000000.", // ·new: line
				".0000......", // ·here
				".0000......", // ·c: 3
			),
		},
		"unified diff": {
			revisions: []string{hitV1, hitV2},
			width:     20,
			height:    6,
			want: stringtest.JoinLF(
				".1111...............", // ·a: 1
				".00000000000........", // -b: 日本語 x
				".111111111..........", // +b: 日本 y
				".11111111111111.....", // +new: line here
				".1111...............", // ·c: 3
				"....................",
			),
		},
		"hunks": {
			revisions: []string{hitV1, hitV2},
			width:     20,
			height:    5,
			setup: func(m *yamlviewport.Model) {
				m.SetViewMode(yamlviewport.ViewModeHunks)
				m.SetHunkContext(0)
			},
			want: stringtest.JoinLF(
				"....................", // ·@@ -2 +2,2 @@
				".00000000000........", // -b: 日本語 x
				".111111111..........", // +b: 日本 y
				".11111111111111.....", // +new: line here
				"....................",
			),
		},
		"side by side": {
			revisions: []string{hitV1, hitV2},
			width:     31,
			height:    6,
			setup: func(m *yamlviewport.Model) {
				m.SetViewMode(yamlviewport.ViewModeSideBySide)
			},
			want: stringtest.JoinLF(
				".0000.............1111.........", // ·a: 1          │  a: 1
				".00000000000......111111111....", // -b: 日本語 x   │ +b: 日本 y
				"..................111111111....", //                │ +new: line
				"..................1111.........", //                │  here
				".0000.............1111.........", // ·c: 3          │  c: 3
				"...............................",
			),
		},
		"side by side without a diff": {
			revisions: []string{hitV1, hitV2},
			width:     31,
			height:    5,
			setup: func(m *yamlviewport.Model) {
				m.SetViewMode(yamlviewport.ViewModeSideBySide)
				m.SetDiffMode(yamlviewport.DiffModeNone)
			},
			want: stringtest.JoinLF(
				".1111.............1111.........", // ·a: 1          │  a: 1
				".111111111........111111111....", // ·b: 日本 y     │  b: 日本 y
				".111111111........111111111....", // ·new: line     │  new: line
				".1111.............1111.........", // ·here          │  here
				".1111.............1111.........", // ·c: 3          │  c: 3
			),
		},
		"frames of both container styles": {
			revisions: []string{hitV2},
			printer:   framed,
			style:     lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Margin(1, 2),
			width:     30,
			height:    10,
			want: stringtest.JoinLF(
				"..............................",
				"..............................", // ··╭────────────────────────╮
				"..............................", // ··│┌──────────────────────┐│
				"...........0000...............", // ··││    1  a: 1           ││
				"...........000000000..........", // ··││    2  b: 日本 y      ││
				"...........00000000000000.....", // ··││    3  new: line here ││
				"...........0000...............", // ··││    4  c: 3           ││
				"..............................", // ··│└──────────────────────┘│
				"..............................", // ··╰────────────────────────╯
				"..............................",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := testModel(tc.printer, tc.width, tc.height, yamlviewport.WithContainerStyle(tc.style))

			sources := make([]*niceyaml.Source, 0, len(tc.revisions))
			for _, text := range tc.revisions {
				source := niceyaml.NewSourceFromString(text)

				sources = append(sources, source)

				if tc.clip > 0 {
					m.AddRevision(yamlviewport.NewRevision(source.Name(), source.View().Clip(tc.clip)))
				} else {
					m.AddRevision(source)
				}
			}

			if tc.setup != nil {
				tc.setup(&m)
			}

			require.Equal(t, tc.want, hitMap(&m), screenMessage(&m))

			// Every hit names the rune its cell shows. A space stands in
			// for the half of a wide character that an edge of the window
			// cuts, and its hit names that character.
			cells := screenCells(m.View())

			for y := -1; y <= m.Height(); y++ {
				for x := -1; x <= m.Width(); x++ {
					hit, ok := m.HitAt(x, y)
					if !ok {
						continue
					}

					require.GreaterOrEqual(t, y, 0)
					require.Less(t, y, len(cells))
					require.GreaterOrEqual(t, x, 0)
					require.Less(t, x, len(cells[y]))

					var want string

					for col, r := range sources[hit.Revision].Lines().Line(hit.Position.Line).Runes() {
						if col == hit.Position.Col {
							want = string(r)
						}
					}

					if cells[y][x] == " " && ansi.StringWidth(want) > 1 {
						continue
					}

					require.Equal(t, want, cells[y][x], "cell %d,%d gives %s of revision %d",
						x, y, hit.Position, hit.Revision)
				}
			}
		})
	}
}

func TestViewport_HitAt(t *testing.T) {
	t.Parallel()

	source := func(text string) yamlviewport.Revision {
		return niceyaml.NewSourceFromString(text)
	}

	sideBySide := func(m *yamlviewport.Model) {
		m.SetViewMode(yamlviewport.ViewModeSideBySide)
	}

	hunks := func(m *yamlviewport.Model) {
		m.SetViewMode(yamlviewport.ViewModeHunks)
		m.SetHunkContext(0)
	}

	annotated := niceyaml.NewSourceFromString(hitV1).View()
	annotated.Annotate(0, line.Annotation{Content: "bad", Placement: line.Below})

	// The mark at column 20 centers the window of the long line, which
	// shows as "...mnopqrstuv..." behind the gutter.
	marked := niceyaml.NewSourceFromString(stringtest.LinesLF("a: 1", "k: abcdefghijklmnopqrstuvwxyz", "c: 3")).View()
	marked.Annotate(1, line.Annotation{Content: "x", Placement: line.Below, Cols: position.NewSpan(20, 20)})

	clipped := yamlviewport.NewRevision("clipped", marked.Clip(10))

	bracketed := testPrinter().With(
		printer.WithStyles(highlightStyles(bracketStyle("[", "]"), lipgloss.NewStyle())),
	)

	framed := testPrinterWithLineNumbers().With(printer.WithContainerStyle(normalBorder().Padding(0, 1)))

	// The default test printer draws a gutter one cell wide, so the
	// content of a row starts at cell 1. A case with a nil want expects
	// HitAt to find nothing at the cell.
	tcs := map[string]struct {
		setup     func(m *yamlviewport.Model)
		printer   *printer.Printer
		style     lipgloss.Style
		revisions []yamlviewport.Revision
		width     int
		height    int
		x         int
		y         int
		want      *yamlviewport.Hit
	}{
		"first column of a line": {
			revisions: []yamlviewport.Revision{source(hitV1)},
			width:     16,
			height:    4,
			x:         1,
			y:         1,
			want:      &yamlviewport.Hit{Position: position.New(1, 0)},
		},
		"second cell of a wide character": {
			revisions: []yamlviewport.Revision{source(hitV1)},
			width:     16,
			height:    4,
			x:         5,
			y:         1,
			want:      &yamlviewport.Hit{Position: position.New(1, 3)},
		},
		"gutter": {
			revisions: []yamlviewport.Revision{source(hitV1)},
			width:     16,
			height:    4,
			x:         0,
			y:         1,
		},
		"past the end of a row": {
			revisions: []yamlviewport.Revision{source(hitV1)},
			width:     16,
			height:    4,
			x:         5,
			y:         0,
		},
		"below the last row": {
			revisions: []yamlviewport.Revision{source(hitV1)},
			width:     16,
			height:    4,
			x:         1,
			y:         3,
		},
		"left of the viewport": {
			revisions: []yamlviewport.Revision{source(hitV1)},
			width:     16,
			height:    4,
			x:         -1,
			y:         1,
		},
		"right of the viewport": {
			revisions: []yamlviewport.Revision{source(hitV1)},
			width:     4,
			height:    4,
			x:         4,
			y:         1,
			setup:     func(m *yamlviewport.Model) { m.SetWordWrap(false) },
		},
		"above the viewport": {
			revisions: []yamlviewport.Revision{source(hitV1)},
			width:     16,
			height:    4,
			x:         1,
			y:         -1,
		},
		"below the viewport": {
			revisions: []yamlviewport.Revision{source(hitV1)},
			width:     16,
			height:    2,
			x:         1,
			y:         2,
		},
		"row a line wraps onto": {
			revisions: []yamlviewport.Revision{source(hitV2)},
			width:     11,
			height:    5,
			x:         1,
			y:         3,
			want:      &yamlviewport.Hit{Position: position.New(2, 10)},
		},
		"scrolled down": {
			revisions: []yamlviewport.Revision{source(hitV2)},
			width:     16,
			height:    2,
			x:         3,
			y:         1,
			setup:     func(m *yamlviewport.Model) { m.SetYOffset(2) },
			want:      &yamlviewport.Hit{Position: position.New(3, 2)},
		},
		"scrolled sideways": {
			revisions: []yamlviewport.Revision{source(hitV1)},
			width:     8,
			height:    3,
			x:         0,
			y:         1,
			setup: func(m *yamlviewport.Model) {
				m.SetWordWrap(false)
				m.SetXOffset(4)
			},
			want: &yamlviewport.Hit{Position: position.New(1, 3)},
		},
		"frames of both container styles": {
			revisions: []yamlviewport.Revision{source(hitV2)},
			printer:   framed,
			style:     lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Margin(1, 2),
			width:     30,
			height:    10,
			x:         11,
			y:         4,
			want:      &yamlviewport.Hit{Position: position.New(1, 0)},
		},
		"frame of the printer": {
			revisions: []yamlviewport.Revision{source(hitV2)},
			printer:   framed,
			style:     lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Margin(1, 2),
			width:     30,
			height:    10,
			x:         11,
			y:         2,
		},
		"deleted line of a diff": {
			revisions: []yamlviewport.Revision{source(hitV1), source(hitV2)},
			width:     20,
			height:    6,
			x:         10,
			y:         1,
			want:      &yamlviewport.Hit{Revision: 0, Position: position.New(1, 6)},
		},
		"inserted line of a diff": {
			revisions: []yamlviewport.Revision{source(hitV1), source(hitV2)},
			width:     20,
			height:    6,
			x:         1,
			y:         3,
			want:      &yamlviewport.Hit{Revision: 1, Position: position.New(2, 0)},
		},
		"line below the changes keeps the index of its revision": {
			revisions: []yamlviewport.Revision{source(hitV1), source(hitV2)},
			width:     20,
			height:    6,
			x:         1,
			y:         4,
			want:      &yamlviewport.Hit{Revision: 1, Position: position.New(3, 0)},
		},
		"deleted line of a diff with the first revision": {
			revisions: []yamlviewport.Revision{source(hitV1), source(hitV2), source(hitV3)},
			width:     20,
			height:    6,
			x:         1,
			y:         1,
			setup:     func(m *yamlviewport.Model) { m.SetDiffMode(yamlviewport.DiffModeOrigin) },
			want:      &yamlviewport.Hit{Revision: 0, Position: position.New(1, 0)},
		},
		"deleted line of a diff with the revision before": {
			revisions: []yamlviewport.Revision{source(hitV1), source(hitV2), source(hitV3)},
			width:     20,
			height:    6,
			x:         1,
			y:         1,
			want:      &yamlviewport.Hit{Revision: 1, Position: position.New(1, 0)},
		},
		"later revision without a diff": {
			revisions: []yamlviewport.Revision{source(hitV1), source(hitV2)},
			width:     20,
			height:    6,
			x:         1,
			y:         2,
			setup:     func(m *yamlviewport.Model) { m.SetDiffMode(yamlviewport.DiffModeNone) },
			want:      &yamlviewport.Hit{Revision: 1, Position: position.New(2, 0)},
		},
		"hunk header": {
			revisions: []yamlviewport.Revision{source(hitV1), source(hitV2)},
			width:     20,
			height:    5,
			x:         3,
			y:         0,
			setup:     hunks,
		},
		"line of a hunk": {
			revisions: []yamlviewport.Revision{source(hitV1), source(hitV2)},
			width:     20,
			height:    5,
			x:         1,
			y:         3,
			setup:     hunks,
			want:      &yamlviewport.Hit{Revision: 1, Position: position.New(2, 0)},
		},
		"left pane holds the revision the diff compares with": {
			revisions: []yamlviewport.Revision{source(hitV1), source(hitV2)},
			width:     31,
			height:    6,
			x:         1,
			y:         4,
			setup:     sideBySide,
			want:      &yamlviewport.Hit{Revision: 0, Position: position.New(2, 0)},
		},
		"right pane holds the current revision": {
			revisions: []yamlviewport.Revision{source(hitV1), source(hitV2)},
			width:     31,
			height:    6,
			x:         18,
			y:         4,
			setup:     sideBySide,
			want:      &yamlviewport.Hit{Revision: 1, Position: position.New(3, 0)},
		},
		"row a line wraps onto in the right pane": {
			revisions: []yamlviewport.Revision{source(hitV1), source(hitV2)},
			width:     31,
			height:    6,
			x:         18,
			y:         3,
			setup:     sideBySide,
			want:      &yamlviewport.Hit{Revision: 1, Position: position.New(2, 10)},
		},
		"filler beside a taller pane": {
			revisions: []yamlviewport.Revision{source(hitV1), source(hitV2)},
			width:     31,
			height:    6,
			x:         1,
			y:         2,
			setup:     sideBySide,
		},
		"separator between the panes": {
			revisions: []yamlviewport.Revision{source(hitV1), source(hitV2)},
			width:     31,
			height:    6,
			x:         15,
			y:         0,
			setup:     sideBySide,
		},
		"gutter of the right pane": {
			revisions: []yamlviewport.Revision{source(hitV1), source(hitV2)},
			width:     31,
			height:    6,
			x:         17,
			y:         0,
			setup:     sideBySide,
		},
		"right pane without a diff": {
			revisions: []yamlviewport.Revision{source(hitV1)},
			width:     31,
			height:    6,
			x:         22,
			y:         1,
			setup:     sideBySide,
			want:      &yamlviewport.Hit{Position: position.New(1, 3)},
		},
		"line of a slice keeps its index in the content": {
			revisions: []yamlviewport.Revision{
				yamlviewport.NewRevision(
					"slice",
					niceyaml.NewSourceFromString(hitV2).View().Slice(position.NewSpan(2, 4)),
				),
			},
			width:  20,
			height: 4,
			x:      1,
			y:      0,
			want:   &yamlviewport.Hit{Position: position.New(2, 0)},
		},
		"annotation row": {
			revisions: []yamlviewport.Revision{yamlviewport.NewRevision("annotated", annotated)},
			width:     16,
			height:    5,
			x:         1,
			y:         1,
		},
		"line below an annotation row": {
			revisions: []yamlviewport.Revision{yamlviewport.NewRevision("annotated", annotated)},
			width:     16,
			height:    5,
			x:         1,
			y:         2,
			want:      &yamlviewport.Hit{Position: position.New(1, 0)},
		},
		"first column a clipped line shows": {
			revisions: []yamlviewport.Revision{clipped},
			width:     20,
			height:    5,
			x:         4,
			y:         1,
			want:      &yamlviewport.Hit{Position: position.New(1, 15)},
		},
		"last column a clipped line shows": {
			revisions: []yamlviewport.Revision{clipped},
			width:     20,
			height:    5,
			x:         13,
			y:         1,
			want:      &yamlviewport.Hit{Position: position.New(1, 24)},
		},
		"ellipsis at the start of a clipped line": {
			revisions: []yamlviewport.Revision{clipped},
			width:     20,
			height:    5,
			x:         3,
			y:         1,
		},
		"ellipsis at the end of a clipped line": {
			revisions: []yamlviewport.Revision{clipped},
			width:     20,
			height:    5,
			x:         14,
			y:         1,
		},
		"clipped line scrolled sideways": {
			revisions: []yamlviewport.Revision{clipped},
			width:     8,
			height:    5,
			x:         0,
			y:         1,
			setup: func(m *yamlviewport.Model) {
				m.SetWordWrap(false)
				m.SetXOffset(6)
			},
			want: &yamlviewport.Hit{Position: position.New(1, 17)},
		},
		"clipped line with a search match in another window": {
			revisions: []yamlviewport.Revision{clipped},
			width:     24,
			height:    5,
			x:         17,
			y:         1,
			setup:     func(m *yamlviewport.Model) { m.SetSearchTerm("yz") },
			want:      &yamlviewport.Hit{Position: position.New(1, 28)},
		},
		"line below a clipped line": {
			revisions: []yamlviewport.Revision{clipped},
			width:     20,
			height:    5,
			x:         4,
			y:         3,
			want:      &yamlviewport.Hit{Position: position.New(2, 3)},
		},
		"bracket a highlight adds before a match": {
			revisions: []yamlviewport.Revision{source("k: ab cd\n")},
			printer:   bracketed,
			width:     16,
			height:    2,
			x:         4,
			y:         0,
			setup:     func(m *yamlviewport.Model) { m.SetSearchTerm("ab") },
			want:      &yamlviewport.Hit{Position: position.New(0, 3)},
		},
		"bracket a highlight adds after a match": {
			revisions: []yamlviewport.Revision{source("k: ab cd\n")},
			printer:   bracketed,
			width:     16,
			height:    2,
			x:         7,
			y:         0,
			setup:     func(m *yamlviewport.Model) { m.SetSearchTerm("ab") },
			want:      &yamlviewport.Hit{Position: position.New(0, 4)},
		},
		"column after a highlight that adds brackets": {
			revisions: []yamlviewport.Revision{source("k: ab cd\n")},
			printer:   bracketed,
			width:     16,
			height:    2,
			x:         9,
			y:         0,
			setup:     func(m *yamlviewport.Model) { m.SetSearchTerm("ab") },
			want:      &yamlviewport.Hit{Position: position.New(0, 6)},
		},
		"no revision": {
			width:  16,
			height: 4,
			x:      1,
			y:      0,
		},
		"no height": {
			revisions: []yamlviewport.Revision{source(hitV1)},
			width:     16,
			height:    0,
			x:         1,
			y:         0,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := testModel(tc.printer, tc.width, tc.height, yamlviewport.WithContainerStyle(tc.style))
			m.AddRevisions(tc.revisions...)

			if tc.setup != nil {
				tc.setup(&m)
			}

			got, ok := m.HitAt(tc.x, tc.y)
			if tc.want == nil {
				assert.False(t, ok, screenMessage(&m))
				assert.Zero(t, got)

				return
			}

			assert.True(t, ok, screenMessage(&m))
			assert.Equal(t, *tc.want, got)
		})
	}
}

func TestViewport_HitAt_PathAt(t *testing.T) {
	t.Parallel()

	// A parent names the value under the pointer from the hit: the
	// revision picks the document, and the position goes to PathAt as it
	// is, whatever the diff shows around the line.
	before := niceyaml.NewSourceFromString(stringtest.LinesLF(
		"spec:",
		"  replicas: 3",
		"  paused: false",
	))
	after := niceyaml.NewSourceFromString(stringtest.LinesLF(
		"spec:",
		"  replicas: 5",
		"  image: app",
		"  paused: false",
	))

	m := testModel(testPrinter(), 24, 6)
	m.AddRevisions(before, after)

	sources := []*niceyaml.Source{before, after}

	tcs := map[string]struct {
		want string
		x    int
		y    int
	}{
		"deleted value": {
			x:    13,
			y:    1,
			want: "$.spec.replicas", // -  replicas: 3
		},
		"inserted value": {
			x:    13,
			y:    2,
			want: "$.spec.replicas", // +  replicas: 5
		},
		"inserted key": {
			x:    3,
			y:    3,
			want: "$.spec.image~", // +  image: app
		},
		"value below the changes": {
			x:    11,
			y:    4,
			want: "$.spec.paused", //    paused: false
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// Each subtest reads a copy, since HitAt fills the row
			// counts of the Model it reads.
			m := m

			hit, ok := m.HitAt(tc.x, tc.y)
			require.True(t, ok, screenMessage(&m))

			doc := yamltest.Document(t, sources[hit.Revision])

			path, ok := doc.PathAt(hit.Position)
			require.True(t, ok)
			assert.Equal(t, tc.want, path.String())
		})
	}
}

// hitRow returns the first row of the viewport with a cell that shows
// want, as [yamlviewport.Model.HitAt] reports it, and true. It reports
// false when no cell on screen shows want.
func hitRow(m *yamlviewport.Model, want yamlviewport.Hit) (int, bool) {
	for y := range m.Height() {
		for x := range m.Width() {
			if hit, ok := m.HitAt(x, y); ok && hit == want {
				return y, true
			}
		}
	}

	return 0, false
}

func TestViewport_Reveal(t *testing.T) {
	t.Parallel()

	// The viewport shows revision 2. Revision 1 holds three lines in place
	// of line 20 and another line 40, so its lines past the first change
	// sit two below the lines they match in revision 2.
	origin := yamltest.GenerateLines(60, "key%d: value")
	before := replaceLine(replaceLine(origin, 40, "old: x"), 20, stringtest.JoinLF("gone: 1", "gone: 2", "gone: 3"))
	after := replaceLine(origin, 40, "new: y")

	const height = 5

	hunks := func(m *yamlviewport.Model) { m.SetViewMode(yamlviewport.ViewModeHunks) }
	sideBySide := func(m *yamlviewport.Model) { m.SetViewMode(yamlviewport.ViewModeSideBySide) }
	withOrigin := func(m *yamlviewport.Model) { m.SetDiffMode(yamlviewport.DiffModeOrigin) }
	noDiff := func(m *yamlviewport.Model) { m.SetDiffMode(yamlviewport.DiffModeNone) }

	// Each case that wants true names in shows a Hit that the center row
	// holds once Reveal returns. A case without shows wants the center row
	// to hold its hit.
	tcs := map[string]struct {
		setup func(m *yamlviewport.Model)
		hit   yamlviewport.Hit
		shows yamlviewport.Hit
		want  bool
	}{
		"unchanged line": {
			hit:  yamlviewport.Hit{Revision: 2, Position: position.New(50, 0)},
			want: true,
		},
		"inserted line": {
			hit:  yamlviewport.Hit{Revision: 2, Position: position.New(40, 0)},
			want: true,
		},
		"deleted line": {
			hit:  yamlviewport.Hit{Revision: 1, Position: position.New(21, 0)},
			want: true,
		},
		"unchanged line of the compared revision": {
			// The unified diff shows the line as line 50 of revision 2.
			hit:   yamlviewport.Hit{Revision: 1, Position: position.New(52, 0)},
			shows: yamlviewport.Hit{Revision: 2, Position: position.New(50, 0)},
			want:  true,
		},
		"end of a line": {
			// No cell shows the column past the last rune, so the row
			// shows the rune before it.
			hit:   yamlviewport.Hit{Revision: 2, Position: position.New(50, 12)},
			shows: yamlviewport.Hit{Revision: 2, Position: position.New(50, 11)},
			want:  true,
		},
		"column past the end of a line": {
			hit: yamlviewport.Hit{Revision: 2, Position: position.New(50, 13)},
		},
		"negative column": {
			hit: yamlviewport.Hit{Revision: 2, Position: position.New(50, -1)},
		},
		"line past the last": {
			hit: yamlviewport.Hit{Revision: 2, Position: position.New(60, 0)},
		},
		"negative line": {
			hit: yamlviewport.Hit{Revision: 2, Position: position.New(-1, 0)},
		},
		"revision the diff leaves out": {
			hit: yamlviewport.Hit{Revision: 0, Position: position.New(50, 0)},
		},
		"revision past the history": {
			hit: yamlviewport.Hit{Revision: 3, Position: position.New(50, 0)},
		},
		"negative revision": {
			hit: yamlviewport.Hit{Revision: -1, Position: position.New(50, 0)},
		},
		"no revision": {
			setup: func(m *yamlviewport.Model) { m.ClearRevisions() },
			hit:   yamlviewport.Hit{},
		},
		"hunks inserted line": {
			setup: hunks,
			hit:   yamlviewport.Hit{Revision: 2, Position: position.New(40, 0)},
			want:  true,
		},
		"hunks deleted line": {
			setup: hunks,
			hit:   yamlviewport.Hit{Revision: 1, Position: position.New(21, 0)},
			want:  true,
		},
		"hunks unchanged line of the compared revision": {
			setup: hunks,
			hit:   yamlviewport.Hit{Revision: 1, Position: position.New(41, 0)},
			shows: yamlviewport.Hit{Revision: 2, Position: position.New(39, 0)},
			want:  true,
		},
		"hunks line outside every hunk": {
			setup: hunks,
			hit:   yamlviewport.Hit{Revision: 2, Position: position.New(50, 0)},
		},
		"hunks line of the compared revision outside every hunk": {
			setup: hunks,
			hit:   yamlviewport.Hit{Revision: 1, Position: position.New(52, 0)},
		},
		"side by side left pane": {
			setup: sideBySide,
			hit:   yamlviewport.Hit{Revision: 1, Position: position.New(52, 0)},
			want:  true,
		},
		"side by side right pane": {
			setup: sideBySide,
			hit:   yamlviewport.Hit{Revision: 2, Position: position.New(50, 0)},
			want:  true,
		},
		"origin deleted line": {
			setup: withOrigin,
			hit:   yamlviewport.Hit{Revision: 0, Position: position.New(40, 0)},
			want:  true,
		},
		"origin revision the diff leaves out": {
			setup: withOrigin,
			hit:   yamlviewport.Hit{Revision: 1, Position: position.New(21, 0)},
		},
		"no diff": {
			setup: noDiff,
			hit:   yamlviewport.Hit{Revision: 2, Position: position.New(50, 0)},
			want:  true,
		},
		"no diff side by side": {
			setup: func(m *yamlviewport.Model) {
				noDiff(m)
				sideBySide(m)
			},
			hit:  yamlviewport.Hit{Revision: 2, Position: position.New(50, 0)},
			want: true,
		},
		"no diff compared revision": {
			setup: noDiff,
			hit:   yamlviewport.Hit{Revision: 1, Position: position.New(21, 0)},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := testModel(testPrinter(), 44, height)
			addRevisions(&m, origin, before, after)

			if tc.setup != nil {
				tc.setup(&m)
			}

			m.SetYOffset(3)

			y, x := m.YOffset(), m.XOffset()
			revision, viewMode, diffMode := m.RevisionIndex(), m.ViewMode(), m.DiffMode()

			require.Equal(t, tc.want, m.Reveal(tc.hit), screenMessage(&m))

			assert.Equal(t, revision, m.RevisionIndex())
			assert.Equal(t, viewMode, m.ViewMode())
			assert.Equal(t, diffMode, m.DiffMode())

			if !tc.want {
				assert.Equal(t, y, m.YOffset())
				assert.Equal(t, x, m.XOffset())

				return
			}

			row, ok := hitRow(&m, cmp.Or(tc.shows, tc.hit))
			require.True(t, ok, screenMessage(&m))
			assert.Equal(t, (height-1)/2, row, screenMessage(&m))
		})
	}
}

func TestViewport_Reveal_SharedLines(t *testing.T) {
	t.Parallel()

	// Both revisions hold the lines of one source, and the current one
	// moves the first line to the middle. The unified diff shows that line
	// twice, deleted at the top and inserted in the middle, and a Hit of
	// the current revision reveals the inserted row.
	lines := niceyaml.NewSourceFromString(yamltest.GenerateLines(30, "key%d: value")).Lines()

	moved := make([]*line.Line, 0, lines.Len())
	for i := 1; i < lines.Len(); i++ {
		moved = append(moved, lines.Line(i))
		if i == 15 {
			moved = append(moved, lines.Line(0))
		}
	}

	hit := yamlviewport.Hit{Revision: 1, Position: position.New(15, 0)}

	const height = 5

	// Each case wants the row of the screen that shows hit.
	tcs := map[string]struct {
		viewMode yamlviewport.ViewMode
		want     int
	}{
		"full": {viewMode: yamlviewport.ViewModeFull, want: (height - 1) / 2},
		// The number of the moved line does not follow the numbers of the
		// lines around it, so its hunk holds it alone. It ends the view and
		// shows on the last row.
		"hunks":        {viewMode: yamlviewport.ViewModeHunks, want: height - 1},
		"side by side": {viewMode: yamlviewport.ViewModeSideBySide, want: (height - 1) / 2},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := testModel(testPrinter(), 44, height)
			m.SetViewMode(tc.viewMode)
			m.AddRevisions(
				yamlviewport.NewRevision("before", line.NewView(lines)),
				yamlviewport.NewRevision("after", line.NewView(line.Collect(moved...))),
			)
			require.True(t, m.ShowingDiff())

			require.True(t, m.Reveal(hit), screenMessage(&m))

			row, ok := hitRow(&m, hit)
			require.True(t, ok, screenMessage(&m))
			assert.Equal(t, tc.want, row, screenMessage(&m))
		})
	}
}

func TestViewport_Reveal_Sideways(t *testing.T) {
	t.Parallel()

	// With wrap off, a position past the right edge of the first screen
	// scrolls the view sideways, as a search match there does. A position
	// whose cell fits the first screen returns the view to it, and so does
	// wrap, which has no horizontal scroll.
	dashes := "k: " + strings.Repeat("-", 60) + "NEEDLE"

	tcs := map[string]struct {
		revisions  []string
		hit        yamlviewport.Hit
		mode       yamlviewport.ViewMode
		width      int
		wrap       bool
		wantScroll bool
	}{
		"column past the right edge": {
			revisions:  []string{stringtest.LinesLF(dashes, "z: 1")},
			hit:        yamlviewport.Hit{Position: position.New(0, 63)},
			width:      30,
			wantScroll: true,
		},
		"wide runes before the column": {
			// Each rune before the needle takes two cells, so the cell of
			// the column is twice its distance from the key.
			revisions:  []string{stringtest.LinesLF("k: "+strings.Repeat("日本", 15)+"NEEDLE", "z: 1")},
			hit:        yamlviewport.Hit{Position: position.New(0, 33)},
			width:      30,
			wantScroll: true,
		},
		"column inside the first screen": {
			revisions: []string{stringtest.LinesLF(dashes, "z: 1")},
			hit:       yamlviewport.Hit{Position: position.New(0, 3)},
			width:     30,
		},
		"column on the last cell of the first screen": {
			// The gutter takes one cell, so column 38 takes the last of
			// the 40.
			revisions: []string{stringtest.LinesLF(dashes, "z: 1")},
			hit:       yamlviewport.Hit{Position: position.New(0, 38)},
			width:     40,
		},
		"column one cell past the first screen": {
			revisions:  []string{stringtest.LinesLF(dashes, "z: 1")},
			hit:        yamlviewport.Hit{Position: position.New(0, 39)},
			width:      40,
			wantScroll: true,
		},
		"column in the right pane": {
			revisions:  []string{"k: short\n", dashes + "\n"},
			hit:        yamlviewport.Hit{Revision: 1, Position: position.New(0, 63)},
			mode:       yamlviewport.ViewModeSideBySide,
			width:      60,
			wantScroll: true,
		},
		"wrap on": {
			revisions: []string{stringtest.LinesLF(dashes, "z: 1")},
			hit:       yamlviewport.Hit{Position: position.New(0, 63)},
			width:     30,
			wrap:      true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := testModel(testPrinter(), tc.width, 5)
			m.SetViewMode(tc.mode)
			m.SetWordWrap(tc.wrap)

			addRevisions(&m, tc.revisions...)

			// The view starts away from the first screen wherever it can
			// scroll.
			m.SetXOffset(10)

			require.True(t, m.Reveal(tc.hit))

			if tc.wantScroll {
				assert.Positive(t, m.XOffset())
			} else {
				assert.Equal(t, 0, m.XOffset())
			}

			_, ok := hitRow(&m, tc.hit)
			assert.True(t, ok, screenMessage(&m))
		})
	}
}
