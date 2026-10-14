package yamltest

import (
	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/diff"
)

// Diff returns the [*diff.Result] that compares the lines of the YAML
// input before with the lines of the input after. Each input goes
// through a [*niceyaml.Source] created from it, and a [*diff.Differ]
// created with opts, such as [diff.WithAlgorithm], computes the diff.
func Diff(before, after string, opts ...diff.Option) *diff.Result {
	return diff.New(opts...).Diff(
		niceyaml.NewSourceFromString(before).Lines(),
		niceyaml.NewSourceFromString(after).Lines(),
	)
}
