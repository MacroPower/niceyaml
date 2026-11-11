package kind_test

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/style/kind"
)

func TestParent_ReachesText(t *testing.T) {
	t.Parallel()

	// The style resolver and the theme derivation walk Parent until they
	// reach Text, so a cycle in the hierarchy would hang them. A walk from
	// any predefined kind, or any kind below one, must end at Text in
	// fewer steps than there are kinds plus the levels the custom kinds
	// add.
	n := len(slices.Collect(kind.All())) + 2

	starts := []kind.Kind{
		kind.GenericHighlight.Child("match").Child("focus"),
		kind.Kind("mine.sub"),
		kind.Kind("..."),
	}

	for k := range kind.All() {
		starts = append(starts, k.Child("mine"))
	}

	for _, k := range slices.Concat(slices.Collect(kind.All()), starts) {
		cur := k
		for range n {
			if cur == kind.Text {
				break
			}

			cur = kind.Parent(cur)
		}

		require.Equal(t, kind.Text, cur, "kind %q does not reach Text within %d steps", k, n)
	}
}

func TestParent(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		kind kind.Kind
		want kind.Kind
	}{
		"root":                  {kind: kind.Text, want: kind.Text},
		"custom kind":           {kind: kind.Kind("custom"), want: kind.Text},
		"empty name":            {kind: kind.Kind(""), want: kind.Text},
		"below a predefined":    {kind: kind.GenericHighlight + ".match", want: kind.GenericHighlight},
		"two levels down":       {kind: kind.GenericHighlight + ".match.focus", want: kind.GenericHighlight + ".match"},
		"kind below a custom":   {kind: kind.Kind("mine.sub"), want: kind.Kind("mine")},
		"trailing separator":    {kind: kind.Kind("comment."), want: kind.Comment},
		"family under Text":     {kind: kind.Literal, want: kind.Text},
		"chrome under comments": {kind: kind.UI, want: kind.Comment},
		"leaf":                  {kind: kind.LiteralNumberFloat, want: kind.LiteralNumber},
		"error mark":            {kind: kind.GenericError, want: kind.TextError},
		"warning mark":          {kind: kind.GenericWarn, want: kind.TextWarn},
		"invalid token":         {kind: kind.GenericErrorInvalid, want: kind.GenericError},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, kind.Parent(tc.kind))
		})
	}
}

func TestIsPredefined(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		kind kind.Kind
		want bool
	}{
		"root":            {kind: kind.Text, want: true},
		"token kind":      {kind: kind.LiteralString, want: true},
		"chrome kind":     {kind: kind.UILineNumber, want: true},
		"custom kind":     {kind: kind.Kind("mine"), want: false},
		"below a kind":    {kind: kind.GenericHighlight.Child("match"), want: false},
		"misspelled name": {kind: kind.Kind("literalStrin"), want: false},
		"empty name":      {kind: kind.Kind(""), want: false},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, kind.IsPredefined(tc.kind))
		})
	}

	t.Run("every kind from All", func(t *testing.T) {
		t.Parallel()

		for k := range kind.All() {
			assert.True(t, kind.IsPredefined(k), "kind %q", k)
		}
	})
}

func TestKind_Child(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		kind       kind.Kind
		name       string
		want       kind.Kind
		wantParent kind.Kind
	}{
		"below a predefined kind": {
			kind:       kind.GenericHighlight,
			name:       "match",
			want:       kind.Kind("genericHighlight.match"),
			wantParent: kind.GenericHighlight,
		},
		"below a kind below a predefined kind": {
			kind:       kind.GenericHighlight.Child("match"),
			name:       "focus",
			want:       kind.Kind("genericHighlight.match.focus"),
			wantParent: kind.GenericHighlight.Child("match"),
		},
		"below a custom kind": {
			kind:       kind.Kind("mine"),
			name:       "sub",
			want:       kind.Kind("mine.sub"),
			wantParent: kind.Kind("mine"),
		},
		// Parent cuts at the last '.', so a name with a '.' sits further
		// down than one level below the kind.
		"name with a separator": {
			kind:       kind.GenericHighlight,
			name:       "a.b",
			want:       kind.Kind("genericHighlight.a.b"),
			wantParent: kind.GenericHighlight.Child("a"),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := tc.kind.Child(tc.name)

			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.wantParent, kind.Parent(got))
		})
	}
}

func TestIsBelowPredefined(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		kind kind.Kind
		want bool
	}{
		"below a predefined kind": {kind: kind.GenericHighlight.Child("match"), want: true},
		"two levels down":         {kind: kind.GenericHighlight.Child("match").Child("focus"), want: true},
		"below the root":          {kind: kind.Text.Child("mine"), want: true},
		"predefined kind":         {kind: kind.GenericHighlight, want: false},
		"custom kind":             {kind: kind.Kind("mine"), want: false},
		"below a custom kind":     {kind: kind.Kind("mine.sub"), want: false},
		"below a misspelled name": {kind: kind.Kind("genericHighlite.match"), want: false},
		"leading separator":       {kind: kind.Kind(".match"), want: false},
		"empty name":              {kind: kind.Kind(""), want: false},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, kind.IsBelowPredefined(tc.kind))
		})
	}
}
