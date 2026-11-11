package yamltest

import (
	"testing"

	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/diff"
	"go.jacobcolvin.com/niceyaml/line"
)

// Diff returns the [*diff.Result] that compares the lines of the YAML
// input before with the lines of the input after. Each input goes
// through a [*niceyaml.Source] created from it, and a [*diff.Differ]
// created with opts, such as [diff.WithLineKey], computes the diff, as
// [DiffLines] does.
func Diff(tb testing.TB, before, after string, opts ...diff.Option) *diff.Result {
	tb.Helper()

	return DiffLines(tb,
		niceyaml.NewSourceFromString(before).Lines(),
		niceyaml.NewSourceFromString(after).Lines(),
		opts...,
	)
}

// DiffLines returns the [*diff.Result] that compares the lines of before
// with the lines of after. A [*diff.Differ] created with opts computes the
// diff with the context of tb. DiffLines fails tb when the diff returns an
// error.
func DiffLines(tb testing.TB, before, after line.Sequence, opts ...diff.Option) *diff.Result {
	tb.Helper()

	result, err := diff.New(opts...).Diff(tb.Context(), before, after)
	require.NoError(tb, err)

	return result
}
