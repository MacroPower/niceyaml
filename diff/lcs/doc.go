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
// standard dynamic programming approach fills an m by n table, so it takes
// O(m*n) time and space whatever the inputs hold. [Hirschberg] splits the
// inputs at a point that some shortest edit script passes through and solves
// each half the same way. It finds each split point with Myers' O(ND) search,
// which spends time in proportion to the input lengths times the number of
// changed lines. A diff of large inputs that differ in a few lines therefore
// runs in close to linear time, however far apart those lines sit. Inputs that
// share few lines still take time close to the square of their length. The
// search keeps O(m+n) working memory, and the result holds one operation per
// line, so it takes O(m+n) space as well.
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
