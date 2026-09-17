package lcs

import (
	"slices"
	"sync"
)

// Hirschberg implements [Algorithm] using a space-optimized LCS algorithm.
//
// Time complexity is O(m*n), where m and n are the lengths of before and
// after. The dynamic programming rows take O(n) space, since the algorithm
// keeps two rows over the after sequence instead of an m by n table. The
// result holds one [Op] per line of either input, so the accumulated ops
// take O(m+n) space, and the pool keeps a buffer of that capacity for
// later calls until the garbage collector clears the pool.
//
// A Hirschberg is safe for concurrent use. Each call borrows a set of
// working buffers from a pool, so concurrent calls never share one, and the
// buffers stay in the pool for later calls. Each call returns a fresh slice.
//
// Create instances with [NewHirschberg].
type Hirschberg struct {
	pool sync.Pool
}

// NewHirschberg creates a new [*Hirschberg]. Buffers grow on the first call
// to [Hirschberg.Diff] and stay pooled for later calls.
func NewHirschberg() *Hirschberg {
	return &Hirschberg{}
}

// Diff returns operations transforming before into after. The returned slice
// is a copy, so it stays valid across later calls.
func (h *Hirschberg) Diff(before, after []string) []Op {
	b, ok := h.pool.Get().(*buffers)
	if !ok {
		b = &buffers{}
	}

	defer h.pool.Put(b)

	b.reset(len(before), len(after))
	b.recurse(before, after, 0, len(before), 0, len(after))

	if len(b.ops) == 0 {
		return nil
	}

	return slices.Clone(b.ops)
}

// buffers is the working memory of one [Hirschberg.Diff] call.
type buffers struct {
	// Working rows for 2-row LCS computation.
	row0, row1 []int

	// Reusable result buffers for forward/backward passes.
	// These are safe to reuse since results are consumed before recursion.
	fwdResult, bwdResult []int

	// Accumulated diff operations.
	ops []Op
}

// reset empties the operations and sizes the rows for sequences of the
// given lengths.
func (b *buffers) reset(beforeLen, afterLen int) {
	b.ops = b.ops[:0]

	needed := afterLen + 1
	if cap(b.row0) < needed {
		b.row0 = make([]int, needed)
		b.row1 = make([]int, needed)
		b.fwdResult = make([]int, needed)
		b.bwdResult = make([]int, needed)
	}

	if worst := beforeLen + afterLen; cap(b.ops) < worst {
		b.ops = make([]Op, 0, worst)
	}
}

// recurse recursively finds the LCS using divide-and-conquer.
// Operates on before[bStart:bEnd] and after[aStart:aEnd].
func (b *buffers) recurse(before, after []string, bStart, bEnd, aStart, aEnd int) {
	m := bEnd - bStart
	n := aEnd - aStart

	// Base case: no before lines - all after lines are insertions.
	if m == 0 {
		for j := aStart; j < aEnd; j++ {
			b.ops = append(b.ops, Op{Kind: OpInsert, Before: -1, After: j})
		}

		return
	}

	// Base case: no after lines - all before lines are deletions.
	if n == 0 {
		for i := bStart; i < bEnd; i++ {
			b.ops = append(b.ops, Op{Kind: OpDelete, Before: i, After: -1})
		}

		return
	}

	// Base case: single before line.
	if m == 1 {
		b.singleBeforeLine(before, after, bStart, aStart, aEnd)

		return
	}

	// Recursive case: divide at bMid.
	bMid := bStart + m/2

	// Forward pass: compute LCS lengths from (bStart, aStart) to (bMid, *).
	forward := b.forward(before, after, bStart, bMid, aStart, aEnd)

	// Backward pass: compute LCS lengths from (bEnd, aEnd) to (bMid, *).
	backward := b.backward(before, after, bMid, bEnd, aStart, aEnd)

	// Find aMid that maximizes forward[j-aStart] + backward[aEnd-j].
	aMid := aStart
	best := -1

	for j := aStart; j <= aEnd; j++ {
		score := forward[j-aStart] + backward[aEnd-j]
		if score > best {
			best = score
			aMid = j
		}
	}

	// Recurse on both halves.
	b.recurse(before, after, bStart, bMid, aStart, aMid)
	b.recurse(before, after, bMid, bEnd, aMid, aEnd)
}

// singleBeforeLine handles the base case where there's exactly one before line.
// Maintains "deletions before insertions" convention.
func (b *buffers) singleBeforeLine(before, after []string, bStart, aStart, aEnd int) {
	// Find first match in after sequence.
	matchIdx := -1

	for j := aStart; j < aEnd; j++ {
		if before[bStart] == after[j] {
			matchIdx = j

			break
		}
	}

	if matchIdx < 0 {
		// No match: delete before line, then insert all after lines.
		b.ops = append(b.ops, Op{Kind: OpDelete, Before: bStart, After: -1})

		for j := aStart; j < aEnd; j++ {
			b.ops = append(b.ops, Op{Kind: OpInsert, Before: -1, After: j})
		}
	} else {
		// Match found: insert lines before match, equal at match, insert lines after.
		for j := aStart; j < matchIdx; j++ {
			b.ops = append(b.ops, Op{Kind: OpInsert, Before: -1, After: j})
		}

		b.ops = append(b.ops, Op{Kind: OpEqual, Before: bStart, After: matchIdx})

		for j := matchIdx + 1; j < aEnd; j++ {
			b.ops = append(b.ops, Op{Kind: OpInsert, Before: -1, After: j})
		}
	}
}

// forward computes LCS lengths going forward from bStart to bMid.
//
// Returns a slice where result[j-aStart] is the LCS length of
// before[bStart:bMid] and after[aStart:aStart+j-aStart].
//
// The returned slice uses an internal buffer and is valid until the next call.
func (b *buffers) forward(before, after []string, bStart, bMid, aStart, aEnd int) []int {
	n := aEnd - aStart

	// Initialize both rows to zeros (required since we swap them).
	for j := 0; j <= n; j++ {
		b.row0[j] = 0
		b.row1[j] = 0
	}

	for i := bStart; i < bMid; i++ {
		// Swap rows: row1 becomes the new row to fill.
		b.row0, b.row1 = b.row1, b.row0
		b.row1[0] = 0

		for j := range n {
			if before[i] == after[aStart+j] {
				b.row1[j+1] = b.row0[j] + 1
			} else {
				b.row1[j+1] = max(b.row1[j], b.row0[j+1])
			}
		}
	}

	// Copy to reusable result buffer.
	// If no iterations occurred (bStart == bMid), copy row0 (all zeros).
	src := b.row1
	if bStart == bMid {
		src = b.row0
	}

	copy(b.fwdResult[:n+1], src[:n+1])

	return b.fwdResult[:n+1]
}

// backward computes LCS lengths going backward from bEnd to bMid.
//
// Returns a slice where result[aEnd-j] is the LCS length of before[bMid:bEnd]
// and after[j:aEnd].
//
// The returned slice uses an internal buffer and is valid until the next call.
func (b *buffers) backward(before, after []string, bMid, bEnd, aStart, aEnd int) []int {
	n := aEnd - aStart

	// Initialize both rows to zeros (required since we swap them).
	for j := 0; j <= n; j++ {
		b.row0[j] = 0
		b.row1[j] = 0
	}

	for i := bEnd - 1; i >= bMid; i-- {
		// Swap rows: row1 becomes the new row to fill.
		b.row0, b.row1 = b.row1, b.row0
		b.row1[0] = 0

		for j := range n {
			if before[i] == after[aEnd-1-j] {
				b.row1[j+1] = b.row0[j] + 1
			} else {
				b.row1[j+1] = max(b.row1[j], b.row0[j+1])
			}
		}
	}

	// Copy to reusable result buffer.
	// If no iterations occurred (bMid == bEnd), copy row0 (all zeros).
	src := b.row1
	if bMid == bEnd {
		src = b.row0
	}

	copy(b.bwdResult[:n+1], src[:n+1])

	return b.bwdResult[:n+1]
}
