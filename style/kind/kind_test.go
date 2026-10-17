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
	// any predefined kind must end at Text in fewer steps than there are
	// kinds.
	n := len(slices.Collect(kind.All()))

	for k := range kind.All() {
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
		"family under Text":     {kind: kind.Literal, want: kind.Text},
		"chrome under comments": {kind: kind.UI, want: kind.Comment},
		"leaf":                  {kind: kind.LiteralNumberFloat, want: kind.LiteralNumber},
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
