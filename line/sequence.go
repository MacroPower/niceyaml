package line

import (
	"iter"

	"go.jacobcolvin.com/niceyaml/position"
)

// Sequence is the lines of some content in order, each with its index in
// that content. The diff and finder packages read one, so they take
// [Lines] and a [*View] alike. Lines yields every line it holds. A View
// yields the lines it holds, each with the index it has in [View.Lines],
// and the indices skip wherever the View skips lines of its content. A
// [position.Range] built from those indices therefore applies to the View
// and to every other View over that content.
//
// See [Lines] and [*View] for implementations.
type Sequence interface {
	// All returns an iterator over the lines within any of the given
	// spans, in ascending order of index and each once. Without spans it
	// yields every line of the sequence. Each iteration yields the index
	// of the line in the content and the [*Line].
	All(spans ...position.Span) iter.Seq2[int, *Line]
}
