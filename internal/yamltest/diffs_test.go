package yamltest_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/diff"
	"go.jacobcolvin.com/niceyaml/diff/lcs"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
)

// recordingAlgorithm is an [lcs.Algorithm] that records the lines it
// gets and diffs them through [lcs.Hirschberg].
type recordingAlgorithm struct {
	before, after []string
}

func (a *recordingAlgorithm) Diff(ctx context.Context, before, after []string) ([]lcs.Op, error) {
	a.before, a.after = before, after

	return lcs.NewHirschberg().Diff(ctx, before, after)
}

func TestDiff(t *testing.T) {
	t.Parallel()

	before := stringtest.LinesLF("a: 1", "b: 2")
	after := stringtest.LinesLF("a: 1", "b: 3")

	t.Run("the lines of each input", func(t *testing.T) {
		t.Parallel()

		got := yamltest.Diff(t, before, after).Unified().String()

		assert.Equal(t, stringtest.JoinLF(
			"   1 | a: 1",
			"   2 - b: 2",
			"   2 + b: 3",
		), got)
	})

	t.Run("options configure the Differ", func(t *testing.T) {
		t.Parallel()

		algo := &recordingAlgorithm{}
		yamltest.Diff(t, before, after, diff.WithAlgorithm(algo))

		assert.Equal(t, []string{"a: 1", "b: 2"}, algo.before)
		assert.Equal(t, []string{"a: 1", "b: 3"}, algo.after)
	})
}

// failingAlgorithm is an [lcs.Algorithm] that returns errAlgorithm.
type failingAlgorithm struct{}

var errAlgorithm = errors.New("algorithm gave up")

func (failingAlgorithm) Diff(context.Context, []string, []string) ([]lcs.Op, error) {
	return nil, errAlgorithm
}

func TestDiffLines(t *testing.T) {
	t.Parallel()

	before := niceyaml.NewSourceFromString(stringtest.LinesLF("a: 1", "b: 2", "c: 3"))
	after := niceyaml.NewSourceFromString(stringtest.LinesLF("a: 1", "b: 4", "c: 3"))

	t.Run("the lines of each sequence", func(t *testing.T) {
		t.Parallel()

		got := yamltest.DiffLines(t, before.Lines(), after.Lines()).Hunks(0).String()

		assert.Equal(t, stringtest.JoinLF(
			"     | @@ -2 +2 @@",
			"   2 - b: 2",
			"   2 + b: 4",
		), got)
	})

	t.Run("an error of the diff fails tb", func(t *testing.T) {
		t.Parallel()

		rec := record(func(tb *recordingTB) {
			yamltest.DiffLines(tb, before.Lines(), after.Lines(), diff.WithAlgorithm(failingAlgorithm{}))
		})

		assert.True(t, rec.failed)
		assert.Contains(t, rec.msg, errAlgorithm.Error())
	})
}
