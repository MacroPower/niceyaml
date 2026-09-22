package main

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	tea "charm.land/bubbletea/v2"

	"go.jacobcolvin.com/niceyaml/bubbles/yamlviewport"
)

func TestUpdateSearchInputBackspace(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  string
	}{
		"ascii": {
			input: "abc",
			want:  "ab",
		},
		"multibyte rune": {
			input: "café",
			want:  "caf",
		},
		"only multibyte rune": {
			input: "é",
			want:  "",
		},
		"empty": {
			input: "",
			want:  "",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := newModel(&modelOptions{})
			m.searching = true
			m.searchInput = tc.input

			m.updateSearchInput(tea.KeyPressMsg{Code: tea.KeyBackspace})

			assert.Equal(t, tc.want, m.searchInput)
			assert.True(t, m.searching)
		})
	}
}

func TestRevisionLabel(t *testing.T) {
	t.Parallel()

	three := []fileEntry{
		{path: "a.yaml", content: []byte("a: 1\n")},
		{path: "b.yaml", content: []byte("a: 2\n")},
		{path: "c.yaml", content: []byte("a: 3\n")},
	}

	tcs := map[string]struct {
		files    []fileEntry
		diffMode yamlviewport.DiffMode
		index    int
		want     string
	}{
		"single revision": {
			files:    three[:1],
			diffMode: yamlviewport.DiffModeAdjacent,
			index:    0,
			want:     "",
		},
		"first revision": {
			files:    three,
			diffMode: yamlviewport.DiffModeAdjacent,
			index:    0,
			want:     "rev 1/3",
		},
		"middle revision adjacent": {
			files:    three,
			diffMode: yamlviewport.DiffModeAdjacent,
			index:    1,
			want:     "diff 2/3",
		},
		"middle revision origin": {
			files:    three,
			diffMode: yamlviewport.DiffModeOrigin,
			index:    1,
			want:     "diff 2/3 origin",
		},
		"middle revision none": {
			files:    three,
			diffMode: yamlviewport.DiffModeNone,
			index:    1,
			want:     "rev 2/3 none",
		},
		"last revision adjacent": {
			files:    three,
			diffMode: yamlviewport.DiffModeAdjacent,
			index:    2,
			want:     "diff 3/3",
		},
		"last revision none": {
			files:    three,
			diffMode: yamlviewport.DiffModeNone,
			index:    2,
			want:     "rev 3/3 none",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := newModel(&modelOptions{files: tc.files})
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

			files := make([]fileEntry, len(tc.paths))
			for i, path := range tc.paths {
				files[i] = fileEntry{path: path, content: []byte("a: 1\n")}
			}

			assert.Equal(t, tc.want, revisionLabels(files))

			m := newModel(&modelOptions{files: files})
			assert.Equal(t, tc.want, m.viewport.RevisionNames())
		})
	}
}

func TestUpdateWindowSizeViewportHeight(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		height int
		want   int
	}{
		"tall terminal": {
			height: 40,
			want:   38,
		},
		"status bar only": {
			height: 2,
			want:   0,
		},
		"single row": {
			height: 1,
			want:   0,
		},
		"zero rows": {
			height: 0,
			want:   0,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := newModel(&modelOptions{})

			updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: tc.height})

			got, ok := updated.(model)
			require.True(t, ok)
			assert.Equal(t, tc.want, got.viewport.Height())
		})
	}
}

func TestInitialSearchScrollsLikeALaterOne(t *testing.T) {
	t.Parallel()

	// The viewport centers a match in the rows it has, and it has none
	// before the first window size message, so a term applied at
	// construction landed near the top of the view instead of its center.
	lines := &strings.Builder{}
	for i := 1; i <= 40; i++ {
		fmt.Fprintf(lines, "k%d: v\n", i)
	}

	files := []fileEntry{{path: "a.yaml", content: []byte(lines.String())}}
	size := tea.WindowSizeMsg{Width: 40, Height: 14}

	sized, _ := newModel(&modelOptions{files: files, search: "k30"}).Update(size)
	got, ok := sized.(model)
	require.True(t, ok)

	sized, _ = newModel(&modelOptions{files: files}).Update(size)
	want, ok := sized.(model)
	require.True(t, ok)

	want.applySearch("k30")

	assert.Equal(t, "k30", got.viewport.SearchTerm())
	assert.Equal(t, 1, got.viewport.SearchCount())
	assert.Positive(t, want.viewport.YOffset())
	assert.Equal(t, want.viewport.YOffset(), got.viewport.YOffset())
}

func TestBaseViewHeight(t *testing.T) {
	t.Parallel()

	// A render taller than the terminal scrolls the alt screen, so every
	// height must fit. Heights of 1 and 2 leave the viewport no rows.
	for height := 1; height <= 10; height++ {
		t.Run(fmt.Sprintf("height %d", height), func(t *testing.T) {
			t.Parallel()

			m := newModel(&modelOptions{
				files: []fileEntry{{path: "a.yaml", content: []byte("a: 1\nb: 2\nc: 3\n")}},
			})

			updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: height})

			got, ok := updated.(model)
			require.True(t, ok)
			assert.LessOrEqual(t, lipgloss.Height(got.baseView()), height)
		})
	}
}

func TestBaseViewNoRows(t *testing.T) {
	t.Parallel()

	// A terminal with no rows, as before the first window size arrives,
	// renders nothing rather than a status bar the join counts as rows.
	m := newModel(&modelOptions{
		files: []fileEntry{{path: "a.yaml", content: []byte("a: 1\n")}},
	})

	assert.Empty(t, m.baseView())

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 0})

	got, ok := updated.(model)
	require.True(t, ok)
	assert.Empty(t, got.baseView())
}

func TestOverlayOffset(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		outer int
		inner int
		want  int
	}{
		"centered": {
			outer: 80,
			inner: 30,
			want:  25,
		},
		"exact fit": {
			outer: 30,
			inner: 30,
			want:  0,
		},
		"overlay wider than the terminal": {
			outer: 10,
			inner: 28,
			want:  0,
		},
		"no terminal": {
			outer: 0,
			inner: 10,
			want:  0,
		},
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
	// row wraps onto another row, pushing the rows below it out of the alt
	// screen, so a terminal narrower than the fixed segments cuts them.
	tcs := map[string]struct {
		width int
	}{
		"20 columns": {
			width: 20,
		},
		"50 columns": {
			width: 50,
		},
		"80 columns": {
			width: 80,
		},
		"120 columns": {
			width: 120,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := newModel(&modelOptions{})

			updated, _ := m.Update(tea.WindowSizeMsg{Width: tc.width, Height: 24})

			got, ok := updated.(model)
			require.True(t, ok)
			assert.Equal(t, tc.width, lipgloss.Width(got.titleLine()), "title line")
			assert.Equal(t, tc.width, lipgloss.Width(got.textLine()), "text line")

			got.searching = true
			got.searchInput = strings.Repeat("x", tc.width)
			assert.Equal(t, tc.width, lipgloss.Width(got.textLine()), "search line")
		})
	}
}

func TestUpdateCtrlCQuits(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		setup func(m *model)
	}{
		"viewing": {
			setup: func(*model) {},
		},
		"theme picker open": {
			setup: func(m *model) { m.themePicking = true },
		},
		"search prompt open": {
			setup: func(m *model) { m.searching = true },
		},
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
