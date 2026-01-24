// Package diff computes minimal edit sequences between string slices.
//
// When rendering YAML diffs, the system needs to determine which lines were
// added, removed, or unchanged between two versions.
//
// This package provides an efficient algorithm for computing those differences
// while minimizing memory allocations during repeated comparisons.
//
// # Algorithm
//
// The package uses Hirschberg's algorithm, which finds the longest common
// subsequence (LCS) between two sequences.
//
// Unlike the standard dynamic programming approach that requires O(m*n) space,
// Hirschberg's divide-and-conquer strategy reduces space complexity to
// O(min(m,n)) while maintaining O(m*n) time.
//
// This is particularly important when comparing large YAML documents.
//
// # Usage
//
// Create a [Hirschberg] instance once and reuse it for multiple comparisons.
//
// The instance maintains internal buffers that grow as needed but are never
// shrunk, avoiding repeated allocations:
//
//	h := diff.NewHirschberg(100) // Initial capacity hint.
//	ops := h.Compute(before, after)
//
// Each [Op] in the result describes one edit operation with an index into the
// appropriate input slice.
//
// The [OpKind] indicates the operation type:
//
//   - [OpEqual]: Line exists in both (index into after).
//   - [OpDelete]: Line only in before (index into before).
//   - [OpInsert]: Line only in after (index into after).
//
// # Integration with line Package
//
// The [OpKind.Flag] method converts operations to [line.Flag] values for
// rendering. This allows the diff results to flow directly into the line
// rendering system:
//
//	for _, op := range ops {
//	    flag := op.Kind.Flag() // Returns FlagDefault, FlagDeleted, or FlagInserted.
//	}
package diff
