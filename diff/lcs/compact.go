package lcs

// compact rewrites b.ops into a canonical form among the edit scripts of
// the same length. It marks the changed lines of each input, slides each
// run of changes with [shiftBoundaries], and rebuilds the ops from the
// marks, so the result no longer depends on where recurse split the
// inputs.
func (b *buffers) compact(before, after []string) {
	b.changedBefore = cleared(b.changedBefore, len(before))
	b.changedAfter = cleared(b.changedAfter, len(after))

	for _, op := range b.ops {
		switch op.Kind {
		case OpDelete:
			b.changedBefore[op.Before] = true
		case OpInsert:
			b.changedAfter[op.After] = true
		case OpEqual:
			// Equal lines stay unmarked.
		}
	}

	shiftBoundaries(before, b.changedBefore, b.changedAfter)
	shiftBoundaries(after, b.changedAfter, b.changedBefore)

	b.ops = b.ops[:0]

	i, j := 0, 0
	for i < len(before) || j < len(after) {
		// Each input holds as many unchanged lines as the other, so both
		// run out of them together. Checking deletions first puts them
		// ahead of the insertions of the same run.
		switch {
		case i < len(before) && b.changedBefore[i]:
			b.ops = append(b.ops, Op{Kind: OpDelete, Before: i, After: -1})
			i++

		case j < len(after) && b.changedAfter[j]:
			b.ops = append(b.ops, Op{Kind: OpInsert, Before: -1, After: j})
			j++

		default:
			b.ops = append(b.ops, Op{Kind: OpEqual, Before: i, After: j})
			i++
			j++
		}
	}
}

// shiftBoundaries slides each run of changed lines in one input to a
// canonical place, following shift_boundaries in GNU diff. The changed
// slice marks the changed lines of lines, and other marks those of the
// other input.
//
// A run can move down by one line when its first line equals the line
// below it, since the two lines then swap roles and the unchanged lines
// still pair up in order. Moving up works the same way with the run's last
// line and the line above it. Each run moves up as far as it can, merging
// with any run it meets, then down as far as it can. The run then moves
// back up to the last place where its end lines up with a run of changes
// in the other input, so a replaced line stays next to its replacement.
// With no such place, the run stays as far down as it goes, so a deleted
// copy of a repeated block shows as the last copy.
func shiftBoundaries(lines []string, changed, other []bool) {
	n := len(lines)

	otherChanged := func(k int) bool {
		return k >= 0 && k < len(other) && other[k]
	}

	// The index j tracks the line of other that pairs with line i of
	// lines, or len(other) past the last pair.
	i, j := 0, 0

	for {
		// Find the start of the next run.
		for i < n && !changed[i] {
			for otherChanged(j) {
				j++
			}

			i++
			j++
		}

		if i == n {
			return
		}

		start := i
		for i < n && changed[i] {
			i++
		}

		for otherChanged(j) {
			j++
		}

		var corresponding int

		for {
			runLength := i - start

			// Move the run up while the line above it matches its last
			// line, merging it with any run above.
			for start > 0 && lines[start-1] == lines[i-1] {
				start--
				changed[start] = true
				i--
				changed[i] = false

				for start > 0 && changed[start-1] {
					start--
				}

				j--
				for otherChanged(j) {
					j--
				}
			}

			// Record the end of the run at the last place it lined up
			// with a run in other, or n when it never did.
			corresponding = n
			if otherChanged(j - 1) {
				corresponding = i
			}

			// Move the run down while its first line matches the line
			// below it, merging it with any run below.
			for i < n && lines[start] == lines[i] {
				changed[start] = false
				start++
				changed[i] = true
				i++

				for i < n && changed[i] {
					i++
				}

				j++
				for otherChanged(j) {
					corresponding = i
					j++
				}
			}

			// A run that merged with another may move further, so repeat
			// until its length holds.
			if runLength == i-start {
				break
			}
		}

		// Move the run back up to where it lines up with a run in other.
		for corresponding < i {
			start--
			changed[start] = true
			i--
			changed[i] = false

			j--
			for otherChanged(j) {
				j--
			}
		}
	}
}

// cleared returns s resized to n elements, all false, reusing its backing
// array when it is large enough.
func cleared(s []bool, n int) []bool {
	if cap(s) < n {
		return make([]bool, n)
	}

	s = s[:n]
	clear(s)

	return s
}
