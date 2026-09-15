package niceyaml_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml"
)

func newRevisions(names ...string) niceyaml.Revisions {
	revs := make(niceyaml.Revisions, 0, len(names))
	for _, name := range names {
		revs = append(revs, niceyaml.NewSourceFromString(name+": data", niceyaml.WithName(name)))
	}

	return revs
}

func TestRevisions_At(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		revs  niceyaml.Revisions
		index int
		want  string
	}{
		"first":             {revs: newRevisions("v0", "v1", "v2"), index: 0, want: "v0"},
		"middle":            {revs: newRevisions("v0", "v1", "v2"), index: 1, want: "v1"},
		"last":              {revs: newRevisions("v0", "v1", "v2"), index: 2, want: "v2"},
		"past end is nil":   {revs: newRevisions("v0", "v1", "v2"), index: 10, want: ""},
		"negative is nil":   {revs: newRevisions("v0", "v1", "v2"), index: -1, want: ""},
		"single past end":   {revs: newRevisions("only"), index: 5, want: ""},
		"single negative":   {revs: newRevisions("only"), index: -5, want: ""},
		"single exact":      {revs: newRevisions("only"), index: 0, want: "only"},
		"empty returns nil": {revs: nil, index: 0, want: ""},
		"empty past end":    {revs: niceyaml.Revisions{}, index: 3, want: ""},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := tc.revs.At(tc.index)
			if tc.want == "" {
				assert.Nil(t, got)

				return
			}

			assert.Equal(t, tc.want, got.Name())
		})
	}
}

func TestRevisions_Len(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 0, niceyaml.Revisions(nil).Len())
	assert.Equal(t, 1, newRevisions("only").Len())
	assert.Equal(t, 3, newRevisions("v0", "v1", "v2").Len())
}

func TestRevisions_Names(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		revs niceyaml.Revisions
		want []string
	}{
		"empty":    {revs: nil, want: nil},
		"single":   {revs: newRevisions("only"), want: []string{"only"}},
		"multiple": {revs: newRevisions("v0", "v1", "v2"), want: []string{"v0", "v1", "v2"}},
		"unnamed": {
			revs: niceyaml.Revisions{niceyaml.NewSourceFromString("a: 1")},
			want: []string{""},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.revs.Names())
		})
	}
}

func TestRevisions_Append(t *testing.T) {
	t.Parallel()

	revs := newRevisions("v0")
	revs = append(revs, niceyaml.NewSourceFromString("v1: data", niceyaml.WithName("v1")))

	assert.Equal(t, []string{"v0", "v1"}, revs.Names())
	assert.Equal(t, "v1", revs.At(revs.Len()-1).Name())

	// The history can be diffed directly.
	result := niceyaml.Diff(revs[0], revs[1])
	assert.Equal(t, "v0..v1", result.Name())
}
