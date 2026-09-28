// Package lcs computes minimal edit sequences between string slices.
//
// A YAML diff shows which lines one version adds, removes, or keeps from
// the other. An [Algorithm] computes that edit sequence, and callers can
// plug in an Algorithm of their own. [Hirschberg] is the default, and it
// reuses its memory across repeated comparisons.
//
// # Complexity
//
// [Hirschberg] finds a longest common subsequence (LCS) of the two inputs. The
// standard dynamic programming approach needs O(m*n) space for its table.
// Hirschberg's divide-and-conquer strategy instead works in two rows of O(n)
// space, where n is the length of the after sequence, and keeps O(m*n) time.
// [Hirschberg] first pairs up the lines both inputs share at the start and at
// the end and searches only the middle that remains, so identical inputs and
// inputs with one short changed region diff in close to linear time. Changes
// far apart leave most of both inputs in the middle, and the search over it
// still takes O(m*n) time. The result itself holds one operation per line, so
// it takes O(m+n) space regardless of the algorithm.
//
// # Usage
//
// Create a [Hirschberg] instance once and reuse it for multiple comparisons,
// from any number of goroutines.
//
// The instance pools its working buffers, so a call borrows one set and
// returns it for later calls. Each call returns a fresh slice:
//
//	h := lcs.NewHirschberg()
//	ops := h.Diff(before, after)
//
// Each [Op] in the result describes one diff operation with its index in each
// input slice, or -1 on the side it does not touch. The [OpKind] indicates the
// operation type:
//
//   - [OpEqual]: Line exists in both, with Before and After set.
//   - [OpDelete]: Line only in before, with After set to -1.
//   - [OpInsert]: Line only in after, with Before set to -1.
//
// The package has no dependencies on the rest of niceyaml, so you can develop
// and test an [Algorithm] on plain string slices. The diff package maps
// each [OpKind] to a line flag when it builds rendering views from the
// operations.
package lcs
