package lcs

import (
	"slices"
	"sync"
)

// Hirschberg implements [Algorithm] with a space-efficient LCS algorithm.
//
// Diff splits the inputs in two at a point that some shortest edit script
// passes through, then solves each half the same way, as Hirschberg's
// divide-and-conquer algorithm does. It finds that point with the
// linear-space search from Myers' "An O(ND) Difference Algorithm", which
// walks from both ends of the inputs at once and stops where the two walks
// meet. Before the search, Diff marks each line that the other input
// never holds as changed and leaves it out, since no common subsequence
// can hold it. The search takes O((m+n)*d) time, where m and n are the
// lengths of before and after and d is the number of lines the shortest
// edit script deletes or inserts among the lines both inputs hold. A diff
// of large inputs that differ in a few lines therefore runs in close to
// linear time, wherever those lines sit, and so does a diff of inputs
// whose changed lines each appear in only one of them. The search keeps
// two arrays of O(m+n) integers. The result holds one [Op] per line of
// either input, so it takes space linear in the length of both inputs as
// well. The pool keeps the working buffers for later calls until the
// garbage collector clears it.
//
// Repeated lines often allow several shortest edit scripts. When the
// inputs hold the same lines in a different order, several longest
// common subsequences can exist, and the split points of the search
// decide which one the result keeps. Hirschberg then settles where each
// run of changed lines sits among the kept lines by sliding it as GNU
// diff does. A run moves down as far as the content allows, then back up
// to where it lines up with a run of changes in the other input. A
// removed or added copy of a repeated block therefore shows as the later
// copy, and a replaced line stays next to its replacement. Within each
// run of changes, the deletions come before the insertions.
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

// Diff returns operations that transform before into after. Each call
// returns a fresh slice, so it stays valid across later calls.
func (h *Hirschberg) Diff(before, after []string) []Op {
	b, ok := h.pool.Get().(*buffers)
	if !ok {
		b = &buffers{}
	}

	defer h.pool.Put(b)

	// The lines the inputs share at the start and at the end pair up in
	// order, so only the middle needs the search. The search marks changed
	// lines only, and compact counts every line it leaves unmarked as
	// unchanged.
	prefix, suffix := commonEnds(before, after)
	bEnd, aEnd := len(before)-suffix, len(after)-suffix

	b.reset(len(before), len(after), bEnd-prefix, aEnd-prefix)

	numIDs := b.intern(before[prefix:bEnd], after[prefix:aEnd])
	b.discard(prefix, numIDs)
	b.recurse(0, len(b.beforeIDs), 0, len(b.afterIDs))

	return b.compact(before, after)
}

// buffers is the working memory of one [Hirschberg.Diff] call.
type buffers struct {
	// Furthest points of the forward and backward walks of midpoint, by
	// diagonal. Each walk stores the before index it reached on each
	// diagonal.
	fwdDiag, bwdDiag []int

	// Line IDs of the lines the search covers, in input order. An ID
	// names equal lines by one number, so the search compares ints
	// instead of strings.
	beforeIDs, afterIDs []int

	// Index in its input of the line behind each entry of beforeIDs and
	// afterIDs.
	beforeIdx, afterIdx []int

	// Whether each input holds a line with a given ID, indexed by ID.
	inBefore, inAfter []bool

	// ID of each line while intern runs. The map is empty between calls,
	// so the pool holds no reference to the caller's lines. Clearing a map
	// keeps its capacity.
	ids map[string]int

	// Changed lines of each input. The search marks them, and compact
	// slides them into place.
	changedBefore, changedAfter []bool

	// Most entries ids has room for. It starts at the number of entries
	// intern made the map for and grows with the most the map has held.
	idsPeak int
}

// reset clears the change marks and sizes the buffers for inputs of the
// given lengths, whose search covers bLen before lines and aLen after
// lines.
func (b *buffers) reset(beforeLen, afterLen, bLen, aLen int) {
	// The walks of midpoint touch the diagonals from -aLen-1 through
	// bLen+1.
	if needed := bLen + aLen + 3; cap(b.fwdDiag) < needed {
		b.fwdDiag = make([]int, needed)
		b.bwdDiag = make([]int, needed)
	}

	b.beforeIDs = slices.Grow(b.beforeIDs[:0], bLen)[:bLen]
	b.afterIDs = slices.Grow(b.afterIDs[:0], aLen)[:aLen]
	b.beforeIdx = slices.Grow(b.beforeIdx[:0], bLen)
	b.afterIdx = slices.Grow(b.afterIdx[:0], aLen)

	b.changedBefore = cleared(b.changedBefore, beforeLen)
	b.changedAfter = cleared(b.changedAfter, afterLen)
}

// intern gives each line of before and after an ID, the same one for
// equal lines, in b.beforeIDs and b.afterIDs. It returns the number of
// distinct lines, and every ID is less than that number.
func (b *buffers) intern(before, after []string) int {
	// Inserting into a map and clearing it take time that grows with its
	// capacity. A map left from a much larger input would slow every later
	// call, so intern replaces it with one sized for this input.
	if lines := len(before) + len(after); b.ids == nil || b.idsPeak > 4*lines+64 {
		b.ids = make(map[string]int, len(before))
		b.idsPeak = len(before)
	}

	ids := b.ids

	// Emptying the map after use keeps the pool from holding on to the
	// caller's lines between calls.
	defer func() {
		b.idsPeak = max(b.idsPeak, len(ids))
		clear(ids)
	}()

	id := func(line string) int {
		n, ok := ids[line]
		if !ok {
			n = len(ids)
			ids[line] = n
		}

		return n
	}

	for i, line := range before {
		b.beforeIDs[i] = id(line)
	}

	for j, line := range after {
		b.afterIDs[j] = id(line)
	}

	return len(ids)
}

// discard marks each line of b.beforeIDs and b.afterIDs whose ID the other
// input never holds as changed, since no common subsequence can hold it,
// and drops it from the search. Every line the other input never holds
// adds to the number of edits the search walks through, so inputs that
// share few lines would otherwise take time close to the square of their
// length. The lines left keep their order at the front of b.beforeIDs and
// b.afterIDs, and b.beforeIdx and b.afterIdx record the index of each in
// its input. The first entry of b.beforeIDs and of b.afterIDs is the line
// at index offset of its input.
func (b *buffers) discard(offset, numIDs int) {
	b.inBefore = cleared(b.inBefore, numIDs)
	b.inAfter = cleared(b.inAfter, numIDs)

	for _, id := range b.beforeIDs {
		b.inBefore[id] = true
	}

	for _, id := range b.afterIDs {
		b.inAfter[id] = true
	}

	b.beforeIDs, b.beforeIdx = keepShared(b.beforeIDs, b.beforeIdx, b.inAfter, b.changedBefore, offset)
	b.afterIDs, b.afterIdx = keepShared(b.afterIDs, b.afterIdx, b.inBefore, b.changedAfter, offset)
}

// keepShared moves each entry of ids whose ID inOther holds to the front
// of ids, in order, and records the index of its line in idx, which it
// empties first. The entry at position i of ids names the input line at
// index offset+i. It marks each line it drops in changed, and returns
// the kept IDs and their indices.
func keepShared(ids, idx []int, inOther, changed []bool, offset int) ([]int, []int) {
	kept := ids[:0]
	idx = idx[:0]

	for i, id := range ids {
		if !inOther[id] {
			changed[offset+i] = true

			continue
		}

		kept = append(kept, id)
		idx = append(idx, offset+i)
	}

	return kept, idx
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

// recurse marks the input lines behind b.beforeIDs[bStart:bEnd] and
// b.afterIDs[aStart:aEnd] that a shortest edit script of those ranges
// deletes or inserts. It splits both ranges at the point that midpoint
// finds and solves each half the same way.
func (b *buffers) recurse(bStart, bEnd, aStart, aEnd int) {
	before, after := b.beforeIDs, b.afterIDs

	// Lines the ranges share at the start and at the end pair up in order.
	// Trimming them also leaves midpoint two ranges that need at least two
	// edits, so each half it returns holds fewer lines than both ranges.
	for bStart < bEnd && aStart < aEnd && before[bStart] == after[aStart] {
		bStart++
		aStart++
	}

	for bStart < bEnd && aStart < aEnd && before[bEnd-1] == after[aEnd-1] {
		bEnd--
		aEnd--
	}

	switch {
	case bStart == bEnd:
		for j := aStart; j < aEnd; j++ {
			b.changedAfter[b.afterIdx[j]] = true
		}

	case aStart == aEnd:
		for i := bStart; i < bEnd; i++ {
			b.changedBefore[b.beforeIdx[i]] = true
		}

	default:
		x, y := b.midpoint(before[bStart:bEnd], after[aStart:aEnd])
		b.recurse(bStart, bStart+x, aStart, aStart+y)
		b.recurse(bStart+x, bEnd, aStart+y, aEnd)
	}
}

// midpoint returns a point (x, y) that some shortest edit script of before
// and after passes through, so that the script splits into one for
// before[:x] and after[:y] and one for before[x:] and after[y:].
//
// It follows diag in GNU diff without its heuristics, the linear-space
// search from Myers' "An O(ND) Difference Algorithm". Point (x, y) lies on
// diagonal x-y. A forward walk starts at (0, 0) and a backward walk at the
// ends of both inputs. Each step, each walk reaches one edit further on
// every diagonal it can, then follows equal lines as far as they go. The
// search stops where the two walks meet on a diagonal. On each diagonal, a
// walk takes whichever of the deletion and the insertion reaches further
// along it. When both reach the same point, the walk records only that
// point, so the choice has no effect.
//
// The caller must trim the lines both inputs share at the start and at the
// end, so both are non-empty and need at least two edits.
func (b *buffers) midpoint(before, after []int) (int, int) {
	n, m := len(before), len(after)

	// Diagonal k is at index k+off of both walks, and the walks keep to
	// the diagonals from -m through n.
	off := m + 1
	fwd, bwd := b.fwdDiag[:n+m+3], b.bwdDiag[:n+m+3]

	dmin, dmax := -m, n
	fmid, bmid := 0, n-m

	// The walks meet after the forward walk's step when the diagonals they
	// start on differ in parity, and after the backward walk's otherwise.
	odd := (fmid-bmid)&1 != 0

	fwd[fmid+off] = 0
	bwd[bmid+off] = n

	fmin, fmax := fmid, fmid
	bmin, bmax := bmid, bmid

	for {
		// Widen the forward walk by one diagonal on each side. A new
		// diagonal gets a neighbor that no step can extend.
		if fmin > dmin {
			fmin--
			fwd[fmin-1+off] = -1
		} else {
			fmin++
		}

		if fmax < dmax {
			fmax++
			fwd[fmax+1+off] = -1
		} else {
			fmax--
		}

		for k := fmax; k >= fmin; k -= 2 {
			// A deletion moves right from diagonal k-1, and an insertion
			// moves down from diagonal k+1.
			lo, hi := fwd[k-1+off], fwd[k+1+off]

			x := hi
			if lo >= hi {
				x = lo + 1
			}

			y := x - k
			for x < n && y < m && before[x] == after[y] {
				x++
				y++
			}

			fwd[k+off] = x

			if odd && bmin <= k && k <= bmax && bwd[k+off] <= x {
				return x, y
			}
		}

		// Widen the backward walk the same way.
		if bmin > dmin {
			bmin--
			bwd[bmin-1+off] = n + 1
		} else {
			bmin++
		}

		if bmax < dmax {
			bmax++
			bwd[bmax+1+off] = n + 1
		} else {
			bmax--
		}

		for k := bmax; k >= bmin; k -= 2 {
			// Walking backward, an insertion moves up from diagonal k-1,
			// and a deletion moves left from diagonal k+1.
			lo, hi := bwd[k-1+off], bwd[k+1+off]

			x := hi - 1
			if lo < hi {
				x = lo
			}

			y := x - k
			for x > 0 && y > 0 && before[x-1] == after[y-1] {
				x--
				y--
			}

			bwd[k+off] = x

			if !odd && fmin <= k && k <= fmax && x <= fwd[k+off] {
				return x, y
			}
		}
	}
}
