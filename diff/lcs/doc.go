// Package lcs computes minimal edit sequences between string slices.
//
// When rendering YAML diffs, the system needs to determine which lines were
// added, removed, or unchanged between two versions.
//
// This package provides the [Algorithm] interface and a default implementation
// using Hirschberg's algorithm for computing differences while minimizing memory
// allocations during repeated comparisons.
//
// # Algorithm Interface
//
// The [Algorithm] interface allows pluggable diff algorithms. [Hirschberg] is
// the default implementation, using a space-efficient LCS algorithm.
//
// Unlike the standard dynamic programming approach that requires O(m*n) space,
// Hirschberg's divide-and-conquer strategy reduces space complexity to O(n),
// where n is the length of the after sequence, while maintaining O(m*n) time.
//
// This is particularly important when comparing large YAML documents.
//
// # Usage
//
// Create a [Hirschberg] instance once and reuse it for multiple comparisons.
//
// The instance maintains internal buffers that grow as needed but are never
// shrunk, avoiding repeated allocations. Each call returns a fresh slice:
//
//	h := lcs.NewHirschberg()
//	ops := h.Diff(before, after)
//
// Each [Op] in the result describes one edit operation with its index in each
// input slice, or -1 on the side it does not touch. The [OpKind] indicates the
// operation type:
//
//   - [OpEqual]: Line exists in both, with Before and After set.
//   - [OpDelete]: Line only in before, with After set to -1.
//   - [OpInsert]: Line only in after, with Before set to -1.
//
// The package has no dependencies on the rest of niceyaml, so an [Algorithm]
// can be developed and tested on plain string slices. The diff package maps
// each [OpKind] to a line flag when it builds rendering views from the
// operations.
package lcs
