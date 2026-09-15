package diff

// Algorithm computes a sequence of operations to transform before into after.
//
// See [*Hirschberg] for the default implementation.
type Algorithm interface {
	// Diff returns operations transforming before into after. Operations
	// reference indices in the original slices, and the returned slice is the
	// caller's to keep.
	Diff(before, after []string) []Op
}

// OpKind represents the kind of diff operation.
type OpKind int

// [OpKind] constants.
const (
	// OpEqual indicates the element exists in both sequences.
	OpEqual OpKind = iota
	// OpDelete indicates the element exists only in the before sequence.
	OpDelete
	// OpInsert indicates the element exists only in the after sequence.
	OpInsert
)

// Op represents a diff operation with its index in each input sequence.
//
// Before is the index into the before sequence and After the index into the
// after sequence. The side an operation does not touch holds -1: an [OpInsert]
// has no Before and an [OpDelete] has no After, while an [OpEqual] carries
// both.
type Op struct {
	Kind   OpKind
	Before int
	After  int
}
