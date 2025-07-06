package niceyaml

// diffKind represents the kind of diff operation.
type diffKind int

// Diff operation kinds.
const (
	diffEqual diffKind = iota
	diffDelete
	diffInsert
)

// lineOp represents a line in the full diff output.
type lineOp struct {
	content    string   // Line content without newline.
	kind       diffKind // One of [diffEqual], [diffDelete], [diffInsert].
	afterLine  int      // 1-indexed line in "after" (for syntax highlighting).
	beforeLine int      // 1-indexed line in "before" (for deleted lines).
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
