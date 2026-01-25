package niceyaml

import (
	"fmt"
	"strings"

	"jacobcolvin.com/niceyaml/internal/diff"
	"jacobcolvin.com/niceyaml/line"
	"jacobcolvin.com/niceyaml/position"
)

// SourceGetter retrieves a [NamedLineSource].
//
// See [Revision] for an implementation.
type SourceGetter interface {
	Source() NamedLineSource
}

// FullDiff represents a complete diff between two [SourceGetter]s.
//
// Create instances with [NewFullDiff].
type FullDiff struct {
	a, b SourceGetter
}

// NewFullDiff creates a new [*FullDiff].
func NewFullDiff(a, b SourceGetter) *FullDiff {
	return &FullDiff{a: a, b: b}
}

// Build returns a [*Source] representing the diff between the two
// [SourceGetter]s.
//
// The returned [Source] contains merged tokens from both revisions: unchanged
// lines use tokens from b, while changed lines include deleted tokens from a
// followed by inserted tokens from b.
//
// [Source] contains flags for deleted/inserted lines.
func (d *FullDiff) Build() *Source {
	ops := lcsLineDiff(d.a.Source(), d.b.Source())

	return &Source{
		name:  fmt.Sprintf("%s..%s", d.a.Source().Name(), d.b.Source().Name()),
		lines: lineOps(ops).toLines(),
	}
}

// SummaryDiff represents a summarized diff between two [SourceGetter]s.
//
// Create instances with [NewSummaryDiff].
type SummaryDiff struct {
	a, b    SourceGetter
	context int
}

// NewSummaryDiff creates a new [*SummaryDiff] with the specified context lines.
// A context of 0 shows only the changed lines.
// Negative values are treated as 0.
func NewSummaryDiff(a, b SourceGetter, context int) *SummaryDiff {
	return &SummaryDiff{a: a, b: b, context: max(0, context)}
}

// Build returns a [*Source] and line spans for rendering a summarized diff.
// The source contains all diff lines with flags for deleted/inserted lines.
// Hunk headers are stored in [line.Annotation.Content] for each hunk's first line.
//
// Pass both to [Printer.Print] to render the summary:
//
//	printer.Print(source, spans...)
func (d *SummaryDiff) Build() (*Source, position.Spans) {
	ops := lcsLineDiff(d.a.Source(), d.b.Source())
	name := fmt.Sprintf("%s..%s", d.a.Source().Name(), d.b.Source().Name())

	if len(ops) == 0 {
		return &Source{name: name}, nil
	}

	hunkSpans := selectHunkSpans(ops, d.context)

	if len(hunkSpans) == 0 {
		return &Source{name: name}, nil
	}

	lines := lineOps(ops).toLines()

	// Precompute prefix sums for O(1) line number and count queries.
	beforeSums := position.NewPrefixSums(len(ops), func(i int) int {
		d, _ := opKindDeltas(ops[i].kind)
		return d
	})
	afterSums := position.NewPrefixSums(len(ops), func(i int) int {
		_, d := opKindDeltas(ops[i].kind)
		return d
	})

	// Add hunk header annotations to first line of each hunk.
	for _, span := range hunkSpans {
		hunkHeader := formatHunkHeader(span, beforeSums, afterSums)
		lines[span.Start].Annotate(line.Annotation{
			Content:  hunkHeader,
			Position: line.Above,
		})
	}

	return &Source{name: name, lines: lines}, hunkSpans
}

// lineOp represents a line in the full diff output.
type lineOp struct {
	line line.Line   // Original [line.Line] from source.
	kind diff.OpKind // One of [diff.OpEqual], [diff.OpDelete], [diff.OpInsert].
}

// opKindDeltas returns the line count deltas this kind affects in before/after
// files.
func opKindDeltas(k diff.OpKind) (int, int) {
	switch k {
	case diff.OpEqual:
		return 1, 1
	case diff.OpDelete:
		return 1, 0
	case diff.OpInsert:
		return 0, 1
	default:
		return 0, 0
	}
}

// lineOps is a slice of [lineOp]s.
type lineOps []lineOp

// toLines converts ops to [line.Lines] with appropriate flags set.
func (ops lineOps) toLines() line.Lines {
	lines := make(line.Lines, 0, len(ops))
	for _, op := range ops {
		ln := op.line.Clone()
		ln.Flag = op.kind.Flag()
		lines = append(lines, ln)
	}

	return lines
}

// lcsLineDiff computes line operations using Hirschberg's space-optimized
// LCS algorithm.
func lcsLineDiff(before, after LineGetter) []lineOp {
	beforeLines := before.Lines()
	afterLines := after.Lines()

	// Pre-compute content strings once to avoid repeated string building.
	beforeContent := make([]string, len(beforeLines))
	for i := range beforeLines {
		beforeContent[i] = beforeLines[i].Content()
	}

	afterContent := make([]string, len(afterLines))
	for i := range afterLines {
		afterContent[i] = afterLines[i].Content()
	}

	// Compute diff using Hirschberg's space-optimized algorithm.
	h := diff.NewHirschberg(min(len(beforeLines), len(afterLines)) + 1)
	diffOps := h.Compute(beforeContent, afterContent)

	// Convert to lineOps.
	ops := make([]lineOp, 0, len(diffOps))

	for _, op := range diffOps {
		switch op.Kind {
		case diff.OpEqual:
			ops = append(ops, lineOp{kind: diff.OpEqual, line: afterLines[op.Index]})
		case diff.OpDelete:
			ops = append(ops, lineOp{kind: diff.OpDelete, line: beforeLines[op.Index]})
		case diff.OpInsert:
			ops = append(ops, lineOp{kind: diff.OpInsert, line: afterLines[op.Index]})
		}
	}

	return ops
}

// formatHunkHeader formats a unified diff hunk header like "@@ -1,3 +1,4 @@".
// Uses the same edge case handling as go-udiff (unified.go lines 218-235).
func formatHunkHeader(span position.Span, beforeSums, afterSums *position.PrefixSums) string {
	fromLine := beforeSums.At(span.Start) + 1
	toLine := afterSums.At(span.Start) + 1
	fromCount := beforeSums.Range(span)
	toCount := afterSums.Range(span)

	var b strings.Builder

	fmt.Fprint(&b, "@@")

	// Format "before" part.
	switch {
	case fromCount > 1:
		fmt.Fprintf(&b, " -%d,%d", fromLine, fromCount)
	case fromLine == 1 && fromCount == 0:
		fmt.Fprint(&b, " -0,0") // GNU diff -u behavior for empty file.
	default:
		fmt.Fprintf(&b, " -%d", fromLine)
	}

	// Format "after" part.
	switch {
	case toCount > 1:
		fmt.Fprintf(&b, " +%d,%d", toLine, toCount)
	case toLine == 1 && toCount == 0:
		fmt.Fprint(&b, " +0,0") // GNU diff -u behavior for empty file.
	default:
		fmt.Fprintf(&b, " +%d", toLine)
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
		if op.kind != diff.OpEqual {
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
