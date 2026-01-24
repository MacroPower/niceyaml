package diff

import "github.com/macropower/niceyaml/line"

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
