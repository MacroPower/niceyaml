// Package diff provides diff algorithms for comparing sequences.
package diff

import "github.com/macropower/niceyaml/line"

// OpKind represents the kind of diff operation.
type OpKind int

// Diff operation kinds.
const (
	OpEqual OpKind = iota
	OpDelete
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
	Index int // Index into before (delete/equal) or after (insert/equal) sequence.
}
