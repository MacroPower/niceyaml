package main

import (
	"testing"

	"github.com/stretchr/testify/assert"

	tea "charm.land/bubbletea/v2"
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
