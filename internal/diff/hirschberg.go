package diff

// Hirschberg computes diff operations using Hirschberg's space-optimized LCS algorithm.
// Time complexity: O(m*n) where m and n are the sequence lengths.
// Space complexity: O(min(m,n)) using two-row dynamic programming.
// Create instances with [NewHirschberg].
type Hirschberg struct {
	// Working rows for 2-row LCS computation.
	row0, row1 []int

	// Reusable result buffers for forward/backward passes.
	// These are safe to reuse since results are consumed before recursion.
	fwdResult, bwdResult []int

	// Accumulated diff operations.
	ops []Op
}

// NewHirschberg creates a new [Hirschberg] with preallocated buffers.
// The capacity should be at least min(m,n)+1 for the sequences to be compared.
func NewHirschberg(capacity int) *Hirschberg {
	return &Hirschberg{
		row0:      make([]int, capacity),
		row1:      make([]int, capacity),
		fwdResult: make([]int, capacity),
		bwdResult: make([]int, capacity),
		ops:       make([]Op, 0, capacity*2),
	}
}

// Compute compares two string slices and returns diff operations.
// For equal operations, Index refers to the after sequence.
// For delete operations, Index refers to the before sequence.
// For insert operations, Index refers to the after sequence.
func (h *Hirschberg) Compute(before, after []string) []Op {
	h.ops = h.ops[:0]

	// Ensure buffers are large enough.
	needed := len(after) + 1
	if cap(h.row0) < needed {
		h.row0 = make([]int, needed)
		h.row1 = make([]int, needed)
		h.fwdResult = make([]int, needed)
		h.bwdResult = make([]int, needed)
	}

	h.recurse(before, after, 0, len(before), 0, len(after))

	return h.ops
}

// recurse recursively finds the LCS using divide-and-conquer.
// Operates on before[bStart:bEnd] and after[aStart:aEnd].
func (h *Hirschberg) recurse(before, after []string, bStart, bEnd, aStart, aEnd int) {
	m := bEnd - bStart
	n := aEnd - aStart

	// Base case: no before lines - all after lines are insertions.
	if m == 0 {
		for j := aStart; j < aEnd; j++ {
			h.ops = append(h.ops, Op{Kind: OpInsert, Index: j})
		}

		return
	}

	// Base case: no after lines - all before lines are deletions.
	if n == 0 {
		for i := bStart; i < bEnd; i++ {
			h.ops = append(h.ops, Op{Kind: OpDelete, Index: i})
		}

		return
	}

	// Base case: single before line.
	if m == 1 {
		h.singleBeforeLine(before, after, bStart, aStart, aEnd)

		return
	}

	// Recursive case: divide at bMid.
	bMid := bStart + m/2

	// Forward pass: compute LCS lengths from (bStart, aStart) to (bMid, *).
	forward := h.forward(before, after, bStart, bMid, aStart, aEnd)

	// Backward pass: compute LCS lengths from (bEnd, aEnd) to (bMid, *).
	backward := h.backward(before, after, bMid, bEnd, aStart, aEnd)

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
	h.recurse(before, after, bStart, bMid, aStart, aMid)
	h.recurse(before, after, bMid, bEnd, aMid, aEnd)
}

// singleBeforeLine handles the base case where there's exactly one before line.
// Maintains "deletions before insertions" convention.
func (h *Hirschberg) singleBeforeLine(before, after []string, bStart, aStart, aEnd int) {
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
		h.ops = append(h.ops, Op{Kind: OpDelete, Index: bStart})

		for j := aStart; j < aEnd; j++ {
			h.ops = append(h.ops, Op{Kind: OpInsert, Index: j})
		}
	} else {
		// Match found: insert lines before match, equal at match, insert lines after.
		for j := aStart; j < matchIdx; j++ {
			h.ops = append(h.ops, Op{Kind: OpInsert, Index: j})
		}

		h.ops = append(h.ops, Op{Kind: OpEqual, Index: matchIdx})

		for j := matchIdx + 1; j < aEnd; j++ {
			h.ops = append(h.ops, Op{Kind: OpInsert, Index: j})
		}
	}
}

// forward computes LCS lengths going forward from bStart to bMid.
// Returns a slice where result[j-aStart] is the LCS length of
// before[bStart:bMid] and after[aStart:aStart+j-aStart].
// The returned slice uses an internal buffer and is valid until the next call.
func (h *Hirschberg) forward(before, after []string, bStart, bMid, aStart, aEnd int) []int {
	n := aEnd - aStart

	// Initialize both rows to zeros (required since we swap them).
	for j := 0; j <= n; j++ {
		h.row0[j] = 0
		h.row1[j] = 0
	}

	for i := bStart; i < bMid; i++ {
		// Swap rows: row1 becomes the new row to fill.
		h.row0, h.row1 = h.row1, h.row0
		h.row1[0] = 0

		for j := range n {
			if before[i] == after[aStart+j] {
				h.row1[j+1] = h.row0[j] + 1
			} else {
				h.row1[j+1] = max(h.row1[j], h.row0[j+1])
			}
		}
	}

	// Copy to reusable result buffer.
	// If no iterations occurred (bStart == bMid), copy row0 (all zeros).
	src := h.row1
	if bStart == bMid {
		src = h.row0
	}

	copy(h.fwdResult[:n+1], src[:n+1])

	return h.fwdResult[:n+1]
}

// backward computes LCS lengths going backward from bEnd to bMid.
// Returns a slice where result[aEnd-j] is the LCS length of
// before[bMid:bEnd] and after[j:aEnd].
// The returned slice uses an internal buffer and is valid until the next call.
func (h *Hirschberg) backward(before, after []string, bMid, bEnd, aStart, aEnd int) []int {
	n := aEnd - aStart

	// Initialize both rows to zeros (required since we swap them).
	for j := 0; j <= n; j++ {
		h.row0[j] = 0
		h.row1[j] = 0
	}

	for i := bEnd - 1; i >= bMid; i-- {
		// Swap rows: row1 becomes the new row to fill.
		h.row0, h.row1 = h.row1, h.row0
		h.row1[0] = 0

		for j := range n {
			if before[i] == after[aEnd-1-j] {
				h.row1[j+1] = h.row0[j] + 1
			} else {
				h.row1[j+1] = max(h.row1[j], h.row0[j+1])
			}
		}
	}

	// Copy to reusable result buffer.
	// If no iterations occurred (bMid == bEnd), copy row0 (all zeros).
	src := h.row1
	if bMid == bEnd {
		src = h.row0
	}

	copy(h.bwdResult[:n+1], src[:n+1])

	return h.bwdResult[:n+1]
}
