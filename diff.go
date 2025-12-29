package niceyaml

import (
	"fmt"
	"strings"
)

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
	line Line     // Original Line from source.
	kind diffKind // One of [diffEqual], [diffDelete], [diffInsert].
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
func lcsLineDiff(before, after []Line) []lineOp {
	m, n := len(before), len(after)

	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}

	for i := m - 1; i >= 0; i-- {
		for j := n - 1; j >= 0; j-- {
			if before[i].Content() == after[j].Content() {
				dp[i][j] = dp[i+1][j+1] + 1
			} else {
				dp[i][j] = max(dp[i+1][j], dp[i][j+1])
			}
		}
	}

	// Backtrack: deletions before insertions per diff convention.
	var ops []lineOp

	i, j := 0, 0

	for i < m || j < n {
		switch {
		case i < m && j < n && before[i].Content() == after[j].Content():
			ops = append(ops, lineOp{kind: diffEqual, line: after[j]})
			i++
			j++

		case i < m && (j >= n || dp[i+1][j] >= dp[i][j+1]):
			ops = append(ops, lineOp{kind: diffDelete, line: before[i]})
			i++

		default:
			ops = append(ops, lineOp{kind: diffInsert, line: after[j]})
			j++
		}
	}

	return ops
}

// buildHunks groups consecutive included operations into hunks.
func buildHunks(ops []lineOp, included []bool) []diffHunk {
	var (
		hunks       []diffHunk
		currentHunk *diffHunk
	)

	beforeLine := 1
	afterLine := 1

	for i, op := range ops {
		opBeforeLine := beforeLine
		opAfterLine := afterLine

		beforeDelta, afterDelta := op.kind.beforeAfterDeltas()
		beforeLine += beforeDelta
		afterLine += afterDelta

		if !included[i] {
			if currentHunk != nil {
				hunks = append(hunks, *currentHunk)
				currentHunk = nil
			}

			continue
		}

		if currentHunk == nil {
			currentHunk = &diffHunk{
				startIdx: i,
				fromLine: opBeforeLine,
				toLine:   opAfterLine,
			}
		}

		currentHunk.endIdx = i + 1
		currentHunk.fromCount += beforeDelta
		currentHunk.toCount += afterDelta
	}

	if currentHunk != nil {
		hunks = append(hunks, *currentHunk)
	}

	return hunks
}

// formatHunkHeader formats a unified diff hunk header like "@@ -1,3 +1,4 @@".
// Uses the same edge case handling as go-udiff (unified.go lines 218-235).
func formatHunkHeader(h diffHunk) string {
	var b strings.Builder

	fmt.Fprint(&b, "@@")

	// Format "before" part.
	switch {
	case h.fromCount > 1:
		fmt.Fprintf(&b, " -%d,%d", h.fromLine, h.fromCount)
	case h.fromLine == 1 && h.fromCount == 0:
		fmt.Fprint(&b, " -0,0") // GNU diff -u behavior for empty file.
	default:
		fmt.Fprintf(&b, " -%d", h.fromLine)
	}

	// Format "after" part.
	switch {
	case h.toCount > 1:
		fmt.Fprintf(&b, " +%d,%d", h.toLine, h.toCount)
	case h.toLine == 1 && h.toCount == 0:
		fmt.Fprint(&b, " +0,0") // GNU diff -u behavior for empty file.
	default:
		fmt.Fprintf(&b, " +%d", h.toLine)
	}

	fmt.Fprint(&b, " @@")

	return b.String()
}

// selectContextLines returns a boolean slice indicating which operations to include.
func selectContextLines(ops []lineOp, context int) []bool {
	included := make([]bool, len(ops))
	n := len(ops)
	lastMarked := -1

	for i, op := range ops {
		if op.kind != diffEqual {
			start := max(0, i-context)
			end := min(n-1, i+context)

			// Mark the range [start, end], avoiding re-marking already included lines.
			for j := max(start, lastMarked+1); j <= end; j++ {
				included[j] = true
			}

			lastMarked = max(lastMarked, end)
		}
	}

	return included
}

// FullDiff represents a complete diff between two [Revision]s.
type FullDiff struct {
	a, b *Revision
}

// NewFullDiff creates a new [FullDiff].
func NewFullDiff(a, b *Revision) *FullDiff {
	return &FullDiff{a: a, b: b}
}

// Lines returns the [Lines] representing the diff between the two revisions.
// The returned Lines contains merged tokens from both revisions:
// unchanged lines use tokens from b, while changed lines include
// deleted tokens from a followed by inserted tokens from b.
// Lines contain flags for deleted/inserted lines.
func (d *FullDiff) Lines() *Source {
	ops := lcsLineDiff(d.a.head.lines, d.b.head.lines)

	lines := make([]Line, 0, len(ops))

	for _, op := range ops {
		line := op.line.Clone()

		switch op.kind {
		case diffDelete:
			line.Flag = FlagDeleted
		case diffInsert:
			line.Flag = FlagInserted
		default:
			line.Flag = FlagDefault
		}

		lines = append(lines, line)
	}

	return &Source{
		Name:  fmt.Sprintf("%s..%s", d.a.Name(), d.b.Name()),
		lines: lines,
	}
}

// SummaryDiff represents a summarized diff between two [Revision]s.
type SummaryDiff struct {
	a, b    *Revision
	context int
}

// NewSummaryDiff creates a new [SummaryDiff] with the specified context lines.
// A context of 0 shows only the changed lines. Negative values are treated as 0.
func NewSummaryDiff(a, b *Revision, context int) *SummaryDiff {
	return &SummaryDiff{a: a, b: b, context: max(0, context)}
}

// Lines returns [Lines] representing a summarized diff between two revisions.
// It shows only changed lines with the specified number of context lines around each change.
// Hunk headers are stored in [Line.Annotation.Content] for each hunk's first line.
func (d *SummaryDiff) Lines() *Source {
	ops := lcsLineDiff(d.a.head.lines, d.b.head.lines)
	name := fmt.Sprintf("%s..%s", d.a.Name(), d.b.Name())

	if len(ops) == 0 {
		return &Source{Name: name}
	}

	included := selectContextLines(ops, d.context)
	hunks := buildHunks(ops, included)

	if len(hunks) == 0 {
		return &Source{Name: name}
	}

	var lines []Line

	for _, hunk := range hunks {
		hunkHeader := formatHunkHeader(hunk)
		isFirstLineOfHunk := true

		for i := hunk.startIdx; i < hunk.endIdx; i++ {
			op := ops[i]
			line := op.line.Clone()

			switch op.kind {
			case diffDelete:
				line.Flag = FlagDeleted
			case diffInsert:
				line.Flag = FlagInserted
			default:
				line.Flag = FlagDefault
			}

			if isFirstLineOfHunk {
				line.Annotation = Annotation{Content: hunkHeader}
				isFirstLineOfHunk = false
			}

			lines = append(lines, line)
		}
	}

	return &Source{
		Name:  name,
		lines: lines,
	}
}
