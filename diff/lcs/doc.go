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
// runs in close to linear time, however far apart those lines sit. A line that
// only one input holds is always a change, so [Hirschberg] marks it before the
// search and leaves it out, and such lines add no search time. Inputs that
// share many lines in a different order still take time close to the square of
// their length. The search keeps O(m+n) working memory, and the result holds
// one operation per line, so it takes O(m+n) space as well.
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
// Each [Op] in the result keeps a line that both inputs share, deletes a
// line from before, or inserts a line from after. It holds the line's index
// in each input slice, or -1 for the input it does not touch.
//
// The package has no dependencies on the rest of niceyaml, so you can develop
// and test an [Algorithm] on plain string slices. The diff package maps
// each [OpKind] to a line flag when it builds rendering views from the
// operations.
package lcs
