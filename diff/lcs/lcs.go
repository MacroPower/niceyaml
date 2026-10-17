package lcs

import "strconv"

// Algorithm computes a sequence of operations to transform before into after.
//
// See [*Hirschberg] for the default implementation.
type Algorithm interface {
	// Diff returns operations that transform before into after. Operations
	// reference indices in the original slices, and the returned slice is the
	// caller's to keep. Between two [OpEqual] operations, the deletions and
	// insertions might come in any order.
	Diff(before, after []string) []Op
}

// OpKind represents the kind of diff operation.
type OpKind int

// [OpKind] constants.
const (
	// OpEqual indicates that the element exists in both sequences.
	OpEqual OpKind = iota
	// OpDelete indicates that the element exists only in the before sequence.
	OpDelete
	// OpInsert indicates that the element exists only in the after sequence.
	OpInsert
)

// String returns "equal" for [OpEqual], "delete" for [OpDelete], "insert"
// for [OpInsert], and "OpKind(n)" for any other value n.
func (k OpKind) String() string {
	switch k {
	case OpEqual:
		return "equal"
	case OpDelete:
		return "delete"
	case OpInsert:
		return "insert"
	default:
		return "OpKind(" + strconv.Itoa(int(k)) + ")"
	}
}

// Op represents a diff operation with its index in each input sequence.
//
// Kind is one of [OpEqual], [OpDelete], or [OpInsert], and consumers such
// as the diff package reject any other value. Before is the index into the
// before sequence, and After is the index into the after sequence. The side
// that an operation does not touch holds -1, so an [OpInsert] holds -1 in
// Before and an [OpDelete] holds -1 in After. An [OpEqual] holds an index
// in both.
type Op struct {
	Kind   OpKind
	Before int
	After  int
}
