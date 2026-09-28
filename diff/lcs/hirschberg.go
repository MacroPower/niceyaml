package lcs

import (
	"slices"
	"sync"
)

// Hirschberg implements [Algorithm] using a space-efficient LCS algorithm.
//
// Diff first pairs up the lines that before and after share at the start
// and at the end. The search over the rest takes O(m*n) time, where m and
// n are the lengths of what remains of before and after, so identical
// inputs and inputs with one short changed region diff in linear time.
// The dynamic programming rows take O(n) space, since the algorithm keeps
// two rows over the after sequence instead of an m by n table. The result
// holds one [Op] per line of either input, so the accumulated ops take
// space linear in the length of both inputs. The pool keeps buffers of
// that capacity for later calls until the garbage collector clears it.
//
// Repeated lines often allow several shortest edit scripts. Hirschberg
// picks one by sliding each run of changed lines as GNU diff does. A run
// moves down as far as the content allows, then back up to where it lines
// up with a run of changes in the other input. A removed or added copy of
// a repeated block therefore shows as the later copy, and a replaced line
// stays next to its replacement. Within each run of changes, the
// deletions come before the insertions.
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

	// The lines the inputs share at the start and at the end pair up in
	// order, so only the middle needs the search. The ops of recurse then
	// cover only the middle, and compact counts every line they leave out
	// as unchanged.
	prefix, suffix := commonEnds(before, after)
	bEnd, aEnd := len(before)-suffix, len(after)-suffix

	b.reset(len(before), len(after), aEnd-prefix)
	b.intern(before, after, prefix, bEnd, aEnd)
	b.recurse(b.beforeIDs, b.afterIDs, prefix, bEnd, prefix, aEnd)
	b.compact(before, after)

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
	// These are safe to reuse since recurse consumes each result
	// before recursion.
	fwdResult, bwdResult []int

	// Line IDs of each input, which name equal lines by one number so
	// the search compares ints instead of strings. Only the lines between
	// the shared start and end hold an ID.
	beforeIDs, afterIDs []int

	// ID of each line while intern runs. The map is empty between calls,
	// so the pool holds no reference to the caller's lines.
	ids map[string]int

	// Changed lines of each input, which compact slides into place.
	changedBefore, changedAfter []bool

	// Accumulated diff operations.
	ops []Op
}

// reset empties the operations and sizes the buffers for inputs of the
// given lengths, whose search covers rowLen after lines.
func (b *buffers) reset(beforeLen, afterLen, rowLen int) {
	b.ops = b.ops[:0]

	needed := rowLen + 1
	if cap(b.row0) < needed {
		b.row0 = make([]int, needed)
		b.row1 = make([]int, needed)
		b.fwdResult = make([]int, needed)
		b.bwdResult = make([]int, needed)
	}

	if worst := beforeLen + afterLen; cap(b.ops) < worst {
		b.ops = make([]Op, 0, worst)
	}

	b.beforeIDs = slices.Grow(b.beforeIDs[:0], beforeLen)[:beforeLen]
	b.afterIDs = slices.Grow(b.afterIDs[:0], afterLen)[:afterLen]
}

// intern gives each line of before[start:bEnd] and after[start:aEnd] an
// ID, the same one for equal lines, in b.beforeIDs and b.afterIDs.
func (b *buffers) intern(before, after []string, start, bEnd, aEnd int) {
	if b.ids == nil {
		b.ids = make(map[string]int, bEnd-start)
	}

	ids := b.ids

	// Emptying the map after use keeps the pool from holding on to the
	// caller's lines between calls.
	defer clear(ids)

	id := func(line string) int {
		n, ok := ids[line]
		if !ok {
			n = len(ids)
			ids[line] = n
		}

		return n
	}

	for i := start; i < bEnd; i++ {
		b.beforeIDs[i] = id(before[i])
	}

	for j := start; j < aEnd; j++ {
		b.afterIDs[j] = id(after[j])
	}
}

// commonEnds returns the number of lines that before and after share at
// the start, and then the number they share at the end of what remains.
func commonEnds(before, after []string) (int, int) {
	prefix := 0
	for prefix < len(before) && prefix < len(after) && before[prefix] == after[prefix] {
		prefix++
	}

	suffix := 0
	for suffix < len(before)-prefix && suffix < len(after)-prefix &&
		before[len(before)-1-suffix] == after[len(after)-1-suffix] {
		suffix++
	}

	return prefix, suffix
}

// recurse finds the LCS using divide-and-conquer.
// Operates on before[bStart:bEnd] and after[aStart:aEnd], which hold the
// line IDs from intern.
func (b *buffers) recurse(before, after []int, bStart, bEnd, aStart, aEnd int) {
	m := bEnd - bStart
	n := aEnd - aStart

	// Base case: no before lines, so all after lines are insertions.
	if m == 0 {
		for j := aStart; j < aEnd; j++ {
			b.ops = append(b.ops, Op{Kind: OpInsert, Before: -1, After: j})
		}

		return
	}

	// Base case: no after lines, so all before lines are deletions.
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
func (b *buffers) singleBeforeLine(before, after []int, bStart, aStart, aEnd int) {
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
func (b *buffers) forward(before, after []int, bStart, bMid, aStart, aEnd int) []int {
	n := aEnd - aStart
	seg := after[aStart:aEnd]

	// Both rows start at zero, since they trade places on each line.
	prev, cur := b.row0[:n+1], b.row1[:n+1]
	clear(prev)
	clear(cur)

	for _, line := range before[bStart:bMid] {
		prev, cur = cur, prev
		cur[0] = 0

		for j, other := range seg {
			if line == other {
				cur[j+1] = prev[j] + 1
			} else {
				cur[j+1] = max(cur[j], prev[j+1])
			}
		}
	}

	return b.fwdResult[:copy(b.fwdResult, cur)]
}

// backward computes LCS lengths going backward from bEnd to bMid.
//
// Returns a slice where result[aEnd-j] is the LCS length of before[bMid:bEnd]
// and after[j:aEnd].
//
// The returned slice uses an internal buffer and is valid until the next call.
func (b *buffers) backward(before, after []int, bMid, bEnd, aStart, aEnd int) []int {
	n := aEnd - aStart
	seg := after[aStart:aEnd]

	// Both rows start at zero, since they trade places on each line.
	prev, cur := b.row0[:n+1], b.row1[:n+1]
	clear(prev)
	clear(cur)

	for i := bEnd - 1; i >= bMid; i-- {
		prev, cur = cur, prev
		cur[0] = 0

		line := before[i]
		for j := range n {
			if line == seg[n-1-j] {
				cur[j+1] = prev[j] + 1
			} else {
				cur[j+1] = max(cur[j], prev[j+1])
			}
		}
	}

	return b.bwdResult[:copy(b.bwdResult, cur)]
}
