package line

import "iter"

// Sequence is the lines of some content in order, each with its index in
// that content. The diff and finder packages read one, so they take
// [Lines] and a [*View] alike. Lines yields every line it holds. A View
// yields the lines it holds, each with the index it has in the content of
// the View. [View.Line] takes that index. The indices skip wherever the
// View skips lines of its content. A [position.Range] built from those
// indices therefore applies to the View and to every other View over that
// content.
//
// See [Lines] and [*View] for implementations.
type Sequence interface {
	// All returns an iterator over every line of the sequence, in
	// ascending order of index and each once. Each iteration yields the
	// index of the line in the content and the [*Line].
	All() iter.Seq2[int, *Line]
}
