// Package styletree provides an augmented AVL tree for efficient interval stabbing queries.
//
// The tree stores style intervals and supports two query modes: point queries retrieve
// all styles containing a specific character position, while range queries retrieve all
// styles overlapping a given range. Query results are returned in insertion order to
// support predictable style composition. Intervals are half-open [start, end).
package styletree
