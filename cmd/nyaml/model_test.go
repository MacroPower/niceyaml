package main

import (
	"testing"

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
			m.viewport.GoToRevision(tc.index)

			assert.Equal(t, tc.want, m.revisionLabel())
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
