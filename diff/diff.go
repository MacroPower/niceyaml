package diff

import "go.jacobcolvin.com/niceyaml/line"

// Algorithm computes a sequence of operations to transform before into after.
//
// See [Hirschberg] for the default implementation.
type Algorithm interface {
	// Init prepares the algorithm for inputs of the given sizes.
	// Called before each Diff to allow buffer preallocation.
	// Algorithms may use beforeLen, afterLen, or both depending on their needs.
	Init(beforeLen, afterLen int)

	// Diff returns operations transforming before into after.
	// Operations reference indices in the original slices.
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

// Flag converts the OpKind to the corresponding [line.Flag].
func (k OpKind) Flag() line.Flag {
	switch k {
	case OpDelete:
		return line.FlagDeleted
	case OpInsert:
		return line.FlagInserted
	default:
		return line.FlagDefault
	}
}

// Op represents a diff operation with an index into one of the input sequences.
type Op struct {
	Kind  OpKind
	Index int // Index into before ([OpDelete]) or after ([OpInsert]/[OpEqual]) sequence.
}
