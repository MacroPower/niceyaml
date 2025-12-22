package niceyaml

// diffKind represents the kind of diff operation.
type diffKind int

// Diff operation kinds.
const (
	diffEqual diffKind = iota
	diffDelete
	diffInsert
)

// beforeAfterDeltas returns the line count deltas this kind affects in before/after files.
func (k diffKind) beforeAfterDeltas() (int, int) {
	switch k {
	case diffEqual:
		return 1, 1
	case diffDelete:
		return 1, 0
	case diffInsert:
		return 0, 1
	default:
		return 0, 0
	}
}

// lineOp represents a line in the full diff output.
type lineOp struct {
	content    string   // Line content without newline.
	kind       diffKind // One of [diffEqual], [diffDelete], [diffInsert].
	afterLine  int      // 1-indexed line in "after" (for syntax highlighting).
	beforeLine int      // 1-indexed line in "before" (for deleted lines).
}

// diffHunk represents a contiguous group of diff operations.
type diffHunk struct {
	startIdx  int // Start index in ops slice.
	endIdx    int // End index (exclusive) in ops slice.
	fromLine  int // First line number in "before" file.
	toLine    int // First line number in "after" file.
	fromCount int // Number of lines from "before" (equal + deleted).
	toCount   int // Number of lines from "after" (equal + inserted).
}

// lcsLineDiff computes line operations using a simple LCS-based diff.
func lcsLineDiff(beforeLines, afterLines []string) []lineOp {
	// Simple O(nm) LCS-based diff algorithm.
	m, n := len(beforeLines), len(afterLines)

	// Build LCS table.
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}

	for i := m - 1; i >= 0; i-- {
		for j := n - 1; j >= 0; j-- {
			if beforeLines[i] == afterLines[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else {
				dp[i][j] = max(dp[i+1][j], dp[i][j+1])
			}
		}
	}

	// Backtrack to build operations.
	// Standard diff convention: deletions come before insertions.
	var ops []lineOp

	i, j := 0, 0

	for i < m || j < n {
		switch {
		case i < m && j < n && beforeLines[i] == afterLines[j]:
			// Equal line.
			ops = append(ops, lineOp{
				kind:       diffEqual,
				content:    afterLines[j],
				afterLine:  j + 1,
				beforeLine: i + 1,
			})
			i++
			j++

		case i < m && (j >= n || dp[i+1][j] >= dp[i][j+1]):
			// Delete from before (prefer deletion when tied).
			ops = append(ops, lineOp{
				kind:       diffDelete,
				content:    beforeLines[i],
				afterLine:  0,
				beforeLine: i + 1,
			})
			i++

		default:
			// Insert from after.
			ops = append(ops, lineOp{
				kind:       diffInsert,
				content:    afterLines[j],
				afterLine:  j + 1,
				beforeLine: 0,
			})
			j++
		}
	}

	return ops
}

// buildHunks groups consecutive included operations into hunks.
// Each hunk has metadata for generating unified diff headers.
func buildHunks(ops []lineOp, included []bool) []diffHunk {
	var (
		hunks       []diffHunk
		currentHunk *diffHunk
	)

	// Track running line numbers in "before" and "after" files.
	beforeLine := 1
	afterLine := 1

	for i, op := range ops {
		opBeforeLine := beforeLine
		opAfterLine := afterLine

		// Advance line counters for all operations, including non-included ones.
		beforeDelta, afterDelta := op.kind.beforeAfterDeltas()
		beforeLine += beforeDelta
		afterLine += afterDelta

		if !included[i] {
			// End current hunk if we hit a non-included operation.
			if currentHunk != nil {
				hunks = append(hunks, *currentHunk)
				currentHunk = nil
			}

			continue
		}

		if currentHunk == nil {
			// Start a new hunk with the current line numbers.
			currentHunk = &diffHunk{
				startIdx: i,
				fromLine: opBeforeLine,
				toLine:   opAfterLine,
			}
		}

		// Extend current hunk.
		currentHunk.endIdx = i + 1
		currentHunk.fromCount += beforeDelta
		currentHunk.toCount += afterDelta
	}

	// Append final hunk if any.
	if currentHunk != nil {
		hunks = append(hunks, *currentHunk)
	}

	return hunks
}

// hunkAfterLineRange computes the [min, max] line range in the "after" file
// needed for syntax-highlighted equal lines in a hunk.
// Returns (0, 0) if the hunk has no equal lines.
func hunkAfterLineRange(ops []lineOp, hunk diffHunk) (int, int) {
	var minLine, maxLine int

	for i := hunk.startIdx; i < hunk.endIdx; i++ {
		if ops[i].kind == diffEqual {
			line := ops[i].afterLine
			if minLine == 0 || line < minLine {
				minLine = line
			}
			if line > maxLine {
				maxLine = line
			}
		}
	}

	return minLine, maxLine
}
