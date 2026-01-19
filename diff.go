package niceyaml

import (
	"fmt"
	"strings"

	"github.com/macropower/niceyaml/line"
	"github.com/macropower/niceyaml/position"
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

// toFlag converts the diffKind to the corresponding [line.Flag].
func (k diffKind) toFlag() line.Flag {
	switch k {
	case diffDelete:
		return line.FlagDeleted
	case diffInsert:
		return line.FlagInserted
	default:
		return line.FlagDefault
	}
}

// lineOp represents a line in the full diff output.
type lineOp struct {
	line line.Line // Original Line from source.
	kind diffKind  // One of [diffEqual], [diffDelete], [diffInsert].
}

// diffHunk represents a contiguous group of diff operations.
type diffHunk struct {
	opsSpan   position.Span // Half-open range [Start, End) of indices in ops slice.
	fromLine  int           // First line number in "before" file.
	toLine    int           // First line number in "after" file.
	fromCount int           // Number of lines from "before" (equal + deleted).
	toCount   int           // Number of lines from "after" (equal + inserted).
}

// lcsLineDiff computes line operations using a simple LCS-based diff.
func lcsLineDiff(before, after LineIterator) []lineOp {
	beforeLines := make(line.Lines, 0, before.Len())
	for _, ln := range before.Lines() {
		beforeLines = append(beforeLines, ln)
	}

	afterLines := make(line.Lines, 0, after.Len())
	for _, ln := range after.Lines() {
		afterLines = append(afterLines, ln)
	}

	m, n := len(beforeLines), len(afterLines)

	// Pre-compute content strings once to avoid repeated string building.
	// This reduces Content() calls from O(m*n) to O(m+n).
	beforeContent := make([]string, m)
	for i := range beforeLines {
		beforeContent[i] = beforeLines[i].Content()
	}

	afterContent := make([]string, n)
	for i := range afterLines {
		afterContent[i] = afterLines[i].Content()
	}

	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}

	for i := m - 1; i >= 0; i-- {
		for j := n - 1; j >= 0; j-- {
			if beforeContent[i] == afterContent[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else {
				dp[i][j] = max(dp[i+1][j], dp[i][j+1])
			}
		}
	}

	// Backtrack: deletions before insertions per diff convention.
	ops := make([]lineOp, 0, max(m, n))

	i, j := 0, 0

	for i < m || j < n {
		switch {
		case i < m && j < n && beforeContent[i] == afterContent[j]:
			ops = append(ops, lineOp{kind: diffEqual, line: afterLines[j]})
			i++
			j++

		case i < m && (j >= n || dp[i+1][j] >= dp[i][j+1]):
			ops = append(ops, lineOp{kind: diffDelete, line: beforeLines[i]})
			i++

		default:
			ops = append(ops, lineOp{kind: diffInsert, line: afterLines[j]})
			j++
		}
	}

	return ops
}

// buildHunks builds hunks from expanded spans of operation indices.
// Each span defines a half-open range [Start, End) of ops to include.
// Computes line numbers by tracking before/after positions.
func buildHunks(ops []lineOp, spans position.Spans) []diffHunk {
	if len(spans) == 0 {
		return nil
	}

	hunks := make([]diffHunk, 0, len(spans))

	// Track before/after line numbers as we iterate through all ops.
	beforeLine := 1
	afterLine := 1
	opIdx := 0

	for _, span := range spans {
		// Skip ops before this span, tracking line numbers.
		for opIdx < span.Start {
			beforeDelta, afterDelta := ops[opIdx].kind.beforeAfterDeltas()
			beforeLine += beforeDelta
			afterLine += afterDelta
			opIdx++
		}

		// Build hunk for this span.
		hunk := diffHunk{
			opsSpan:  span,
			fromLine: beforeLine,
			toLine:   afterLine,
		}

		// Process ops in this span.
		for opIdx < span.End {
			beforeDelta, afterDelta := ops[opIdx].kind.beforeAfterDeltas()
			hunk.fromCount += beforeDelta
			hunk.toCount += afterDelta
			beforeLine += beforeDelta
			afterLine += afterDelta
			opIdx++
		}

		hunks = append(hunks, hunk)
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

// selectHunkSpans collects change indices and groups them into expanded spans.
// Returns spans representing the op index ranges to include in each hunk.
func selectHunkSpans(ops []lineOp, context int) position.Spans {
	// Collect indices of non-equal operations.
	var changeIndices []int
	for i, op := range ops {
		if op.kind != diffEqual {
			changeIndices = append(changeIndices, i)
		}
	}

	if len(changeIndices) == 0 {
		return nil
	}

	// Group indices, expand by context, clamp to valid range.
	return position.GroupIndices(changeIndices, context).
		Expand(context).
		Clamp(0, len(ops))
}

// SourceGetter gets a [NamedLineIterator].
// See [Revision] for an implementation.
type SourceGetter interface {
	Source() NamedLineIterator
}

// FullDiff represents a complete diff between two [SourceGetter]s.
// Create instances with [NewFullDiff].
type FullDiff struct {
	a, b SourceGetter
}

// NewFullDiff creates a new [FullDiff].
func NewFullDiff(a, b SourceGetter) *FullDiff {
	return &FullDiff{a: a, b: b}
}

// Build returns a [*Source] representing the diff between the two [SourceGetter]s.
// The returned Source contains merged tokens from both revisions:
// unchanged lines use tokens from b, while changed lines include
// deleted tokens from a followed by inserted tokens from b.
// Source contains flags for deleted/inserted lines.
func (d *FullDiff) Build() *Source {
	ops := lcsLineDiff(d.a.Source(), d.b.Source())

	lines := make(line.Lines, 0, len(ops))

	for _, op := range ops {
		ln := op.line.Clone()
		ln.Flag = op.kind.toFlag()

		lines = append(lines, ln)
	}

	return &Source{
		name:  fmt.Sprintf("%s..%s", d.a.Source().Name(), d.b.Source().Name()),
		lines: lines,
	}
}

// SummaryDiff represents a summarized diff between two [SourceGetter]s.
// Create instances with [NewSummaryDiff].
type SummaryDiff struct {
	a, b    SourceGetter
	context int
}

// NewSummaryDiff creates a new [SummaryDiff] with the specified context lines.
// A context of 0 shows only the changed lines. Negative values are treated as 0.
func NewSummaryDiff(a, b SourceGetter, context int) *SummaryDiff {
	return &SummaryDiff{a: a, b: b, context: max(0, context)}
}

// Build returns a [*Source] and line spans for rendering a summarized diff.
// The source contains all diff lines with flags for deleted/inserted lines.
// Hunk headers are stored in [line.Annotation.Content] for each hunk's first line.
// Pass both to [Printer.Print] to render the summary: printer.Print(source, spans...)
func (d *SummaryDiff) Build() (*Source, position.Spans) {
	ops := lcsLineDiff(d.a.Source(), d.b.Source())
	name := fmt.Sprintf("%s..%s", d.a.Source().Name(), d.b.Source().Name())

	if len(ops) == 0 {
		return &Source{name: name}, nil
	}

	hunkSpans := selectHunkSpans(ops, d.context)
	hunks := buildHunks(ops, hunkSpans)

	if len(hunks) == 0 {
		return &Source{name: name}, nil
	}

	// Build full source with flags (like FullDiff).
	lines := make(line.Lines, 0, len(ops))
	for _, op := range ops {
		ln := op.line.Clone()
		ln.Flag = op.kind.toFlag()
		lines = append(lines, ln)
	}

	// Add hunk header annotations to first line of each hunk.
	for _, hunk := range hunks {
		hunkHeader := formatHunkHeader(hunk)
		lines[hunk.opsSpan.Start].Annotate(line.Annotation{
			Content:  hunkHeader,
			Position: line.Above,
		})
	}

	// Convert hunks to spans (0-indexed, half-open).
	spans := make(position.Spans, len(hunks))
	for i, hunk := range hunks {
		spans[i] = hunk.opsSpan
	}

	return &Source{name: name, lines: lines}, spans
}
