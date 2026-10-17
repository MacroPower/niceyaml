package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	tea "charm.land/bubbletea/v2"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/bubbles/yamlviewport"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/style/kind"
	"go.jacobcolvin.com/niceyaml/style/theme"
)

// sourcesOf returns a source for each input, named a.yaml, b.yaml, and so
// on.
func sourcesOf(inputs ...string) []*niceyaml.Source {
	sources := make([]*niceyaml.Source, 0, len(inputs))
	for i, input := range inputs {
		name := string(rune('a'+i)) + ".yaml"
		sources = append(sources, niceyaml.NewSourceFromString(input, niceyaml.WithName(name)))
	}

	return sources
}

// press returns the press of the key that types r.
func press(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Text: string(r)}
}

// update sends msgs to m in order and returns the model after the last
// one.
func update(t *testing.T, m tea.Model, msgs ...tea.Msg) model {
	t.Helper()

	for _, msg := range msgs {
		m, _ = m.Update(msg)
	}

	got, ok := m.(model)
	require.True(t, ok)

	return got
}

func TestUpdateSearchInput(t *testing.T) {
	t.Parallel()

	backspace := tea.KeyPressMsg{Code: tea.KeyBackspace}

	tcs := map[string]struct {
		msg tea.Msg
		// The search input before msg.
		input string
		want  string
		// Sends msg while the search prompt is closed.
		closed bool
	}{
		"backspace over ascii":                          {input: "abc", msg: backspace, want: "ab"},
		"backspace over a multibyte rune":               {input: "caf\u00e9", msg: backspace, want: "caf"},
		"backspace over the only multibyte rune":        {input: "\u00e9", msg: backspace, want: ""},
		"backspace over an emoji with a skin tone":      {input: "a\U0001F44D\U0001F3FD", msg: backspace, want: "a"},
		"backspace over a flag":                         {input: "\U0001F1FA\U0001F1F8", msg: backspace, want: ""},
		"backspace over a combining sequence":           {input: "cafe\u0301", msg: backspace, want: "caf"},
		"backspace on an empty input":                   {input: "", msg: backspace, want: ""},
		"pasted text is appended":                       {input: "a", msg: tea.PasteMsg{Content: "bc"}, want: "abc"},
		"pasted newline keeps the first line":           {input: "a", msg: tea.PasteMsg{Content: "b\nc"}, want: "ab"},
		"pasted carriage return keeps the first line":   {input: "a", msg: tea.PasteMsg{Content: "b\rc"}, want: "ab"},
		"pasted tab is dropped":                         {input: "a", msg: tea.PasteMsg{Content: "b\tc"}, want: "abc"},
		"pasted leading newline pastes nothing":         {input: "a", msg: tea.PasteMsg{Content: "\nb"}, want: "a"},
		"pasted leading carriage return pastes nothing": {input: "a", msg: tea.PasteMsg{Content: "\rb"}, want: "a"},
		"pasted carriage return and newline keep the first line": {
			input: "a",
			msg:   tea.PasteMsg{Content: "b\r\nc"},
			want:  "ab",
		},
		"paste outside the prompt changes nothing": {
			input:  "a",
			msg:    tea.PasteMsg{Content: "xyz"},
			want:   "a",
			closed: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := newModel(&modelOptions{})
			m.searching = !tc.closed
			m.searchInput = tc.input

			got := update(t, m, tc.msg)
			assert.Equal(t, tc.want, got.searchInput)
			assert.Equal(t, !tc.closed, got.searching)
		})
	}
}

func TestRevisionLabel(t *testing.T) {
	t.Parallel()

	three := sourcesOf("a: 1\n", "a: 2\n", "a: 3\n")

	tcs := map[string]struct {
		sources  []*niceyaml.Source
		diffMode yamlviewport.DiffMode
		index    int
		want     string
	}{
		"single revision": {
			sources:  three[:1],
			diffMode: yamlviewport.DiffModeAdjacent,
			index:    0,
			want:     "",
		},
		"first revision": {
			sources:  three,
			diffMode: yamlviewport.DiffModeAdjacent,
			index:    0,
			want:     "rev 1/3",
		},
		"middle revision adjacent": {
			sources:  three,
			diffMode: yamlviewport.DiffModeAdjacent,
			index:    1,
			want:     "diff 2/3",
		},
		"middle revision origin": {
			sources:  three,
			diffMode: yamlviewport.DiffModeOrigin,
			index:    1,
			want:     "diff 2/3 origin",
		},
		"middle revision none": {
			sources:  three,
			diffMode: yamlviewport.DiffModeNone,
			index:    1,
			want:     "rev 2/3 none",
		},
		"last revision adjacent": {
			sources:  three,
			diffMode: yamlviewport.DiffModeAdjacent,
			index:    2,
			want:     "diff 3/3",
		},
		"last revision none": {
			sources:  three,
			diffMode: yamlviewport.DiffModeNone,
			index:    2,
			want:     "rev 3/3 none",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := newModel(&modelOptions{sources: tc.sources})
			m.viewport.SetDiffMode(tc.diffMode)
			m.viewport.GotoRevision(tc.index)

			assert.Equal(t, tc.want, m.revisionLabel())
		})
	}
}

func TestRevisionLabels(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		paths []string
		want  []string
	}{
		"distinct base names": {
			paths: []string{"a/one.yaml", "b/two.yaml"},
			want:  []string{"one.yaml", "two.yaml"},
		},
		"shared base name": {
			paths: []string{"a/config.yaml", "b/config.yaml"},
			want:  []string{"a/config.yaml", "b/config.yaml"},
		},
		"only the colliding names grow": {
			paths: []string{"a/config.yaml", "b/config.yaml", "c/other.yaml"},
			want:  []string{"a/config.yaml", "b/config.yaml", "other.yaml"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, revisionLabels(tc.paths))
		})
	}
}

func TestNewModelDefaultTheme(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		lineNumbers bool
	}{
		"diff gutter":  {lineNumbers: false},
		"line numbers": {lineNumbers: true},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := newModel(&modelOptions{
				sources:     sourcesOf(stringtest.LinesLF("a: 1", "b: [2, 3]")),
				lineNumbers: tc.lineNumbers,
			})

			assert.Equal(t, theme.Charm.Name, m.themeList[m.themeIndex].Name)
			assert.Equal(t, theme.Charm.Styles(), m.styles)

			// The first frame renders as the theme picker would after
			// switching to the default theme.
			got := update(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})

			want := got
			want.selectTheme(want.themeIndex)

			assert.Equal(t, want.baseView(), got.baseView())
		})
	}
}

func TestBuildPrinterOpts(t *testing.T) {
	t.Parallel()

	styles := theme.Charm.Styles()

	for _, lineNumbers := range []bool{false, true} {
		p := printer.New(buildPrinterOpts(lineNumbers, styles)...)

		// The viewer draws its content in a box of the theme's surface.
		assert.Equal(t, styles.Style(kind.Text).PaddingRight(1), p.ContainerStyle())
	}
}

func TestThemeListOrder(t *testing.T) {
	t.Parallel()

	m := newModel(&modelOptions{})

	names := make([]string, 0, len(m.themeList))
	for _, th := range m.themeList {
		assert.Equal(t, theme.Dark, th.Mode, th.Name)

		names = append(names, th.Name)
	}

	// The theme picker lists the themes in name order.
	assert.NotEmpty(t, names)
	assert.True(t, slices.IsSorted(names), "names out of order: %v", names)
}

func TestUpdateWindowSizeViewportHeight(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		height int
		want   int
	}{
		"tall terminal":   {height: 40, want: 38},
		"status bar only": {height: 2, want: 0},
		"single row":      {height: 1, want: 0},
		"zero rows":       {height: 0, want: 0},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := update(t, newModel(&modelOptions{}), tea.WindowSizeMsg{Width: 80, Height: tc.height})

			assert.Equal(t, tc.want, got.viewport.Height())
		})
	}
}

func TestInitialSearchScrollsLikeALaterOne(t *testing.T) {
	t.Parallel()

	// The model sets the term of --search before the first window size
	// message gives the viewport any rows. The match lands on the same
	// row as a search after that message.
	sources := sourcesOf(yamltest.GenerateYAML(40))
	size := tea.WindowSizeMsg{Width: 40, Height: 14}

	got := update(t, newModel(&modelOptions{sources: sources, search: "key_30"}), size)

	want := update(t, newModel(&modelOptions{sources: sources}), size)
	want.applySearch("key_30")

	assert.Equal(t, "key_30", got.viewport.SearchTerm())
	assert.Equal(t, 1, got.viewport.SearchCount())
	assert.Positive(t, want.viewport.YOffset())
	assert.Equal(t, want.viewport.YOffset(), got.viewport.YOffset())
}

func TestResubmittedSearchScrollsToMatch(t *testing.T) {
	t.Parallel()

	search := []tea.Msg{press('/')}
	for _, r := range "key_30" {
		search = append(search, press(r))
	}

	search = append(search, tea.KeyPressMsg{Code: tea.KeyEnter})

	m := newModel(&modelOptions{sources: sourcesOf(yamltest.GenerateYAML(40))})

	got := update(t, m, append([]tea.Msg{tea.WindowSizeMsg{Width: 40, Height: 14}}, search...)...)
	matched := got.viewport.YOffset()
	require.Positive(t, matched)

	got = update(t, got, press('g'))
	require.Zero(t, got.viewport.YOffset())

	// Submitting the active term again brings its match back into view.
	got = update(t, got, search...)
	assert.Equal(t, "key_30", got.viewport.SearchTerm())
	assert.Equal(t, matched, got.viewport.YOffset())
}

func TestBaseViewHeight(t *testing.T) {
	t.Parallel()

	// A render taller than the terminal scrolls the alt screen, so every
	// height must fit. Heights of 1 and 2 leave the viewport no rows.
	for height := 1; height <= 10; height++ {
		t.Run(fmt.Sprintf("height %d", height), func(t *testing.T) {
			t.Parallel()

			m := newModel(&modelOptions{sources: sourcesOf(stringtest.LinesLF("a: 1", "b: 2", "c: 3"))})

			got := update(t, m, tea.WindowSizeMsg{Width: 80, Height: height})
			assert.LessOrEqual(t, lipgloss.Height(got.baseView()), height)
		})
	}
}

func TestBaseViewNoRows(t *testing.T) {
	t.Parallel()

	// A terminal with no rows, as before the first window size arrives,
	// renders nothing rather than a status bar that takes up rows.
	m := newModel(&modelOptions{sources: sourcesOf("a: 1\n")})

	assert.Empty(t, m.baseView())

	got := update(t, m, tea.WindowSizeMsg{Width: 80, Height: 0})
	assert.Empty(t, got.baseView())
}

func TestBaseViewFillsTerminal(t *testing.T) {
	t.Parallel()

	// The viewport and the status bar each render rows as wide as the
	// terminal, so baseView stacks them with a newline and needs no padding.
	tcs := map[string]struct {
		width  int
		height int
	}{
		"narrow":   {width: 20, height: 24},
		"standard": {width: 80, height: 24},
		"large":    {width: 200, height: 60},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := newModel(&modelOptions{sources: sourcesOf(stringtest.LinesLF("a: 1", "b: [2, 3]", "c:", "  d: 4"))})

			got := update(t, m, tea.WindowSizeMsg{Width: tc.width, Height: tc.height})

			rows := strings.Split(got.baseView(), "\n")
			require.Len(t, rows, tc.height)

			for i, row := range rows {
				assert.Equal(t, tc.width, lipgloss.Width(row), "row %d", i)
			}
		})
	}
}

func TestBaseViewNoColumns(t *testing.T) {
	t.Parallel()

	// A viewport with rows but no columns renders nothing, and the status
	// bar still sits on the bottom rows of the terminal.
	tcs := map[string]struct {
		mode  yamlviewport.ViewMode
		width int
	}{
		"full width 0":         {mode: yamlviewport.ViewModeFull, width: 0},
		"side-by-side width 0": {mode: yamlviewport.ViewModeSideBySide, width: 0},
		"side-by-side width 3": {mode: yamlviewport.ViewModeSideBySide, width: 3},
		"side-by-side width 4": {mode: yamlviewport.ViewModeSideBySide, width: 4},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			const height = 10

			m := newModel(&modelOptions{sources: sourcesOf("a: 1\n", "a: 2\n")})
			m.viewport.SetViewMode(tc.mode)

			got := update(t, m, tea.WindowSizeMsg{Width: tc.width, Height: height})

			rows := strings.Split(got.baseView(), "\n")
			require.Len(t, rows, height)

			bar := strings.Split(got.statusBar(), "\n")
			assert.Equal(t, bar, rows[height-statusBarHeight:])

			for i, row := range rows[:height-statusBarHeight] {
				assert.Equal(t, tc.width, lipgloss.Width(row), "row %d", i)
			}
		})
	}
}

func TestOverlayOffset(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		outer int
		inner int
		want  int
	}{
		"centered":                        {outer: 80, inner: 30, want: 25},
		"exact fit":                       {outer: 30, inner: 30, want: 0},
		"overlay wider than the terminal": {outer: 10, inner: 28, want: 0},
		"no terminal":                     {outer: 0, inner: 10, want: 0},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, overlayOffset(tc.outer, tc.inner))
		})
	}
}

func TestStatusBarWidth(t *testing.T) {
	t.Parallel()

	// Both status bar rows fill the terminal exactly. One cell over and a
	// row wraps onto another row, which pushes the rows below it out of the
	// alt screen. So a terminal narrower than the fixed segments truncates
	// them.
	tcs := map[string]struct {
		width int
	}{
		"20 columns":  {width: 20},
		"50 columns":  {width: 50},
		"80 columns":  {width: 80},
		"120 columns": {width: 120},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := update(t, newModel(&modelOptions{}), tea.WindowSizeMsg{Width: tc.width, Height: 24})

			assert.Equal(t, tc.width, lipgloss.Width(got.titleLine()), "title line")
			assert.Equal(t, tc.width, lipgloss.Width(got.textLine()), "text line")

			got.searching = true
			got.searchInput = strings.Repeat("x", tc.width)
			assert.Equal(t, tc.width, lipgloss.Width(got.textLine()), "search line")
		})
	}
}

func TestTitleLinePosition(t *testing.T) {
	t.Parallel()

	// The bracket shows the line at the top of the view, in the same unit as
	// the line count beside it. Each line of the first two cases wraps onto
	// several rows.
	long := strings.Repeat("word ", 60)
	wrapped := []string{stringtest.LinesLF("a: "+long, "b: "+long, "c: "+long)}

	tcs := map[string]struct {
		want string
		srcs []string
		keys []tea.Msg
		mode yamlviewport.ViewMode
		// The line count the title shows beside the bracket.
		lines int
	}{
		"top": {
			srcs:  wrapped,
			lines: 3,
			want:  " [1] ",
		},
		"bottom": {
			srcs:  wrapped,
			keys:  []tea.Msg{press('G')},
			lines: 3,
			want:  " [3] ",
		},
		// A view with no lines has no top line, so the bracket shows 0 to
		// match the line count beside it.
		"empty file": {
			srcs:  []string{""},
			mode:  yamlviewport.ViewModeFull,
			lines: 0,
			want:  " [0] ",
		},
		"hunks of identical revisions": {
			srcs:  []string{"a: 1\n", "a: 1\n"},
			mode:  yamlviewport.ViewModeHunks,
			lines: 0,
			want:  " [0] ",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := newModel(&modelOptions{sources: sourcesOf(tc.srcs...)})
			m.viewport.SetViewMode(tc.mode)

			got := update(t, update(t, m, tea.WindowSizeMsg{Width: 60, Height: 8}), tc.keys...)
			require.Equal(t, tc.lines, got.viewport.TotalLineCount())

			title := got.titleLine()
			assert.Contains(t, title, fmt.Sprintf(" %d lines ", tc.lines))
			assert.Contains(t, title, tc.want)
		})
	}
}

func TestTextLineEscapesRevisionName(t *testing.T) {
	t.Parallel()

	// A glob match takes its name from the file system. A control character
	// in it thus reaches the status bar as a visible rune rather than as a
	// byte the terminal acts on.
	tcs := map[string]struct {
		name string
		want string
	}{
		"sgr conceal": {name: "\x1b[8mhidden.yaml", want: "␛[8mhidden.yaml"},
		"bel":         {name: "x\ay.yaml", want: "x␇y.yaml"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := newModel(&modelOptions{
				sources: []*niceyaml.Source{
					niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithName(tc.name)),
				},
			})

			got := update(t, m, tea.WindowSizeMsg{Width: 120, Height: 24})

			line := got.textLine()
			assert.Contains(t, line, tc.want)
			assert.NotContains(t, line, tc.name)
		})
	}
}

func TestUpdateCtrlCQuits(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		setup func(m *model)
	}{
		"viewing":            {setup: func(*model) {}},
		"theme picker open":  {setup: func(m *model) { m.themePicking = true }},
		"search prompt open": {setup: func(m *model) { m.searching = true }},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := newModel(&modelOptions{})
			tc.setup(&m)

			_, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})

			require.NotNil(t, cmd)
			assert.IsType(t, tea.QuitMsg{}, cmd())
		})
	}
}

func TestThemePicker(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		// Messages sent after the key that opens the picker.
		msgs []tea.Msg
		// How far the selection ends from the theme the picker opened with.
		want int
		// Leaves the picker open.
		picking bool
	}{
		"wheel down": {
			msgs:    []tea.Msg{tea.MouseWheelMsg{Button: tea.MouseWheelDown}},
			want:    1,
			picking: true,
		},
		"wheel up": {
			msgs:    []tea.Msg{tea.MouseWheelMsg{Button: tea.MouseWheelUp}},
			want:    -1,
			picking: true,
		},
		"wheel right": {
			msgs:    []tea.Msg{tea.MouseWheelMsg{Button: tea.MouseWheelRight}},
			want:    0,
			picking: true,
		},
		"esc restores the theme the picker opened with": {
			msgs: []tea.Msg{press('j'), press('j'), tea.KeyPressMsg{Code: tea.KeyEscape}},
			want: 0,
		},
		"enter keeps the selected theme": {
			msgs: []tea.Msg{press('j'), press('j'), tea.KeyPressMsg{Code: tea.KeyEnter}},
			want: 2,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// The document is long enough to scroll, so a wheel that
			// reached the viewport would move it.
			m := newModel(&modelOptions{sources: sourcesOf(yamltest.GenerateYAML(100))})

			before := update(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
			require.Positive(t, before.themeIndex)
			require.Less(t, before.themeIndex, len(before.themeList)-2)

			got := update(t, before, append([]tea.Msg{press('t')}, tc.msgs...)...)

			assert.Equal(t, tc.picking, got.themePicking)
			assert.Zero(t, got.viewport.YOffset())
			assert.Zero(t, got.viewport.XOffset())
			assert.Equal(t, before.themeIndex+tc.want, got.themeIndex)
			assert.Equal(t, got.themeList[got.themeIndex].Styles(), got.styles)
		})
	}
}
