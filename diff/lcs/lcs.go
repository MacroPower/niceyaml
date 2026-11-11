package lcs

import (
	"context"
	"strconv"
)

// Algorithm computes a sequence of operations to transform before into after.
//
// See [*Hirschberg] for the default implementation.
type Algorithm interface {
	// Diff returns operations that transform before into after. Operations
	// reference indices in the original slices, and the returned slice is the
	// caller's to keep.
	//
	// The operations must hold every element of each slice once, in order.
	// The Before indices of the [OpEqual] and [OpDelete] operations count
	// from 0 to len(before)-1. The After indices of the [OpEqual] and
	// [OpInsert] operations count from 0 to len(after)-1. Between two
	// [OpEqual] operations, the deletions and insertions might come in any
	// order. The diff package panics when the operations break this rule.
	//
	// An Algorithm should check ctx while it works and return the error of
	// ctx after ctx ends, so a deadline bounds a costly diff. The diff
	// package returns a non-nil error as it is, and it reads the operations
	// only when the error is nil.
	Diff(ctx context.Context, before, after []string) ([]Op, error)
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
