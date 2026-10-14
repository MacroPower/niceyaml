package yamltest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml/diff"
	"go.jacobcolvin.com/niceyaml/diff/lcs"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
)

// recordingAlgorithm is an [lcs.Algorithm] that records the lines it
// gets and diffs them through [lcs.Hirschberg].
type recordingAlgorithm struct {
	before, after []string
}

func (a *recordingAlgorithm) Diff(before, after []string) []lcs.Op {
	a.before, a.after = before, after

	return lcs.NewHirschberg().Diff(before, after)
}

func TestDiff(t *testing.T) {
	t.Parallel()

	before := stringtest.LinesLF("a: 1", "b: 2")
	after := stringtest.LinesLF("a: 1", "b: 3")

	t.Run("the lines of each input", func(t *testing.T) {
		t.Parallel()

		got := yamltest.Diff(before, after).Unified().String()

		assert.Equal(t, stringtest.JoinLF(
			"   1 | a: 1",
			"   2 | b: 2",
			"   2 | b: 3",
		), got)
	})

	t.Run("options configure the Differ", func(t *testing.T) {
		t.Parallel()

		algo := &recordingAlgorithm{}
		yamltest.Diff(before, after, diff.WithAlgorithm(algo))

		assert.Equal(t, []string{"a: 1", "b: 2"}, algo.before)
		assert.Equal(t, []string{"a: 1", "b: 3"}, algo.after)
	})
}
