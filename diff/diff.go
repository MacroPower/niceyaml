package diff

import (
	"fmt"
	"strings"
	"sync"

	"go.jacobcolvin.com/niceyaml/diff/lcs"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/position"
)

// Differ computes line differences using a configurable algorithm.
//
// Differ is not safe for concurrent use because the underlying algorithm
// maintains reusable buffers. Create separate instances for concurrent
// operations. The returned [*Result] is safe for concurrent use.
//
// Create instances with [New].
type Differ struct {
	algo lcs.Algorithm
}

// Option configures a [Differ].
//
// Available options:
//   - [WithAlgorithm]
type Option func(*Differ)

// WithAlgorithm sets the diff algorithm.
//
// Default is [lcs.Hirschberg].
func WithAlgorithm(algo lcs.Algorithm) Option {
	return func(d *Differ) {
		d.algo = algo
	}
}

// New creates a new [*Differ] with the given options.
//
// If no algorithm is specified, uses [lcs.Hirschberg].
func New(opts ...Option) *Differ {
	d := &Differ{}
	for _, opt := range opts {
		opt(d)
	}

	if d.algo == nil {
		d.algo = lcs.NewHirschberg()
	}

	return d
}

// Diff computes the difference between two views, such as two niceyaml
// Source values or two [line.Lines] collections.
//
// The lines of the result are copies, so the overlays and annotations on
// the input lines come along, and the flags come from the diff. The result
// can be rendered multiple times with [Result.Unified] or
// [Result.Hunks].
func (d *Differ) Diff(a, b line.View) *Result {
	ops := d.computeOps(a, b)

	// Precompute prefix sums for O(1) line number and count queries.
	beforeSums := newPrefixSums(len(ops), func(i int) int {
		d, _ := opKindDeltas(ops[i].kind)
		return d
	})
	afterSums := newPrefixSums(len(ops), func(i int) int {
		_, d := opKindDeltas(ops[i].kind)
		return d
	})

	return &Result{
		ops:        ops,
		name:       fmt.Sprintf("%s..%s", viewName(a), viewName(b)),
		beforeSums: beforeSums,
		afterSums:  afterSums,
	}
}

// namer is a [line.View] with a name, such as a niceyaml Source.
type namer interface {
	Name() string
}

// viewName returns the name of v, or an empty string for a [line.View] without
// one.
func viewName(v line.View) string {
	if n, ok := v.(namer); ok {
		return n.Name()
	}

	return ""
}

// collectLines returns the lines of v in order. The lines share their tokens
// with v, and both toLines and getAlignedRows clone each line before a
// caller sees it.
func collectLines(v line.View) []line.Line {
	lines := make([]line.Line, 0, v.Len())
	for _, l := range v.AllLines() {
		lines = append(lines, l)
	}

	return lines
}

// computeOps computes line operations using the configured algorithm.
func (d *Differ) computeOps(before, after line.View) []lineOp {
	beforeLines := collectLines(before)
	afterLines := collectLines(after)

	// Pre-compute content strings once to avoid repeated string building.
	beforeContent := make([]string, len(beforeLines))
	for i := range beforeLines {
		beforeContent[i] = beforeLines[i].Content()
	}

	afterContent := make([]string, len(afterLines))
	for i := range afterLines {
		afterContent[i] = afterLines[i].Content()
	}

	// Compute diff using the configured algorithm.
	diffOps := d.algo.Diff(beforeContent, afterContent)

	// Convert to lineOps.
	ops := make([]lineOp, 0, len(diffOps))

	for _, op := range diffOps {
		switch op.Kind {
		case lcs.OpEqual:
			ops = append(ops, lineOp{kind: lcs.OpEqual, line: afterLines[op.After]})
		case lcs.OpDelete:
			ops = append(ops, lineOp{kind: lcs.OpDelete, line: beforeLines[op.Before]})
		case lcs.OpInsert:
			ops = append(ops, lineOp{kind: lcs.OpInsert, line: afterLines[op.After]})
		}
	}

	return ops
}

// Result holds computed diff operations for rendering.
//
// Rendering methods each return a fresh [line.Lines] view that a printer
// accepts directly. A diff is not a YAML document, so the views carry no
// parsing or decoding behavior:
//   - [Result.Unified] returns all lines in unified diff format.
//   - [Result.Hunks] returns only the changed lines with context.
//   - [Result.Before] and [Result.After] return aligned views
//     for side-by-side rendering.
//
// Create instances with [Differ.Diff] or [Diff].
type Result struct {
	beforeSums  *prefixSums
	afterSums   *prefixSums
	name        string
	ops         []lineOp
	alignedRows []alignedRow // Lazily computed for side-by-side rendering.
	alignedOnce sync.Once    // Ensures thread-safe lazy initialization.
}

// alignedRow holds a pair of lines for side-by-side diff rendering.
// Either field may be the zero value to represent an empty placeholder.
type alignedRow struct {
	before line.Line
	after  line.Line
}

// Unified returns a [line.Lines] view of the complete diff.
//
// The view interleaves lines from both revisions. Unchanged lines come from
// the second source, and changed lines include deleted lines from the first
// source followed by inserted lines from the second. Each line carries a
// [line.Flag] marking it as deleted, inserted, or unchanged.
//
// Each call returns an independent copy, so overlays added to one result do
// not affect another.
func (r *Result) Unified() line.Lines {
	return lineOps(r.ops).toLines()
}

// Hunks returns a [line.Lines] view of the summarized diff: the changed
// lines with context lines of unchanged content around each change, and
// nothing else. A context of 0 shows only the changed lines, and Hunks
// treats negative values as 0.
//
// Each line carries the flag and the line number it has in
// [Result.Unified], so the view prints with the same numbers, and the
// first line of each hunk carries a [line.Above] annotation holding the
// unified hunk header. Returns nil when the diff has no changes.
//
// Each call returns an independent copy, so overlays added to one result do
// not affect another.
func (r *Result) Hunks(context int) line.Lines {
	context = max(0, context)

	if len(r.ops) == 0 {
		return nil
	}

	hunkSpans := selectHunkSpans(r.ops, context)

	if len(hunkSpans) == 0 {
		return nil
	}

	var lines line.Lines

	for _, span := range hunkSpans {
		start := len(lines)
		lines = append(lines, lineOps(r.ops[span.Start:span.End]).toLines()...)

		// The hunk header goes above the first line of the hunk.
		lines[start].AddAnnotation(line.Annotation{
			Content:   formatHunkHeader(span, r.beforeSums, r.afterSums),
			Placement: line.Above,
		})
	}

	return lines
}

// Stats returns the number of added and removed lines in the diff.
func (r *Result) Stats() (int, int) {
	var added, removed int

	for _, op := range r.ops {
		switch op.kind {
		case lcs.OpInsert:
			added++
		case lcs.OpDelete:
			removed++
		case lcs.OpEqual:
			// No-op: equal lines don't contribute to stats counts.
		}
	}

	return added, removed
}

// getAlignedRows returns the lazily computed aligned rows for side-by-side
// rendering. Lines are aligned so both sides have equal counts:
//   - Equal lines appear on both sides at the same position.
//   - Consecutive delete/insert pairs appear on the same row.
//   - Unmatched deletions have empty placeholders on the right.
//   - Unmatched insertions have empty placeholders on the left.
func (r *Result) getAlignedRows() []alignedRow {
	r.alignedOnce.Do(func() {
		rows := make([]alignedRow, 0, len(r.ops))

		i := 0
		for i < len(r.ops) {
			op := r.ops[i]

			switch op.kind {
			case lcs.OpEqual:
				// Equal lines appear on both sides. Before and After clone per call.
				ln := op.line.Clone()
				ln.SetFlag(line.FlagDefault)

				rows = append(rows, alignedRow{
					before: ln,
					after:  ln,
				})
				i++

			case lcs.OpDelete:
				// Collect consecutive deletes.
				deletes := collectConsecutive(r.ops, i, lcs.OpDelete)
				i += len(deletes)

				// Collect consecutive inserts that follow.
				var inserts []lineOp

				if i < len(r.ops) && r.ops[i].kind == lcs.OpInsert {
					inserts = collectConsecutive(r.ops, i, lcs.OpInsert)
					i += len(inserts)
				}

				// Pair deletes with inserts on the same row.
				maxPairs := max(len(deletes), len(inserts))
				for j := range maxPairs {
					var beforeLine, afterLine line.Line

					if j < len(deletes) {
						beforeLine = deletes[j].line.Clone()
						beforeLine.SetFlag(line.FlagDeleted)
					}

					if j < len(inserts) {
						afterLine = inserts[j].line.Clone()
						afterLine.SetFlag(line.FlagInserted)
					}

					rows = append(rows, alignedRow{
						before: beforeLine,
						after:  afterLine,
					})
				}

			case lcs.OpInsert:
				// Standalone insert (not following a delete).
				ln := op.line.Clone()
				ln.SetFlag(line.FlagInserted)

				rows = append(rows, alignedRow{
					before: line.Line{},
					after:  ln,
				})
				i++
			}
		}

		r.alignedRows = rows
	})

	return r.alignedRows
}

// Before returns a [line.Lines] view for the left (before) pane of a
// side-by-side diff.
//
// Before aligns its lines with [Result.After] so both views have equal
// line counts. Consecutive delete/insert sequences are paired row-by-row. When there
// are more insertions than deletions, empty placeholder lines (zero value) fill
// the remaining rows on this side.
//
// Line flags: [line.FlagDeleted] for deleted lines, [line.FlagDefault] for
// equal lines and empty placeholders.
//
// Each call returns an independent copy, so overlays added to one result do
// not affect another or the paired [Result.After] view.
func (r *Result) Before() line.Lines {
	rows := r.getAlignedRows()

	lines := make(line.Lines, len(rows))
	for i := range rows {
		lines[i] = rows[i].before.Clone()
	}

	return lines
}

// After returns a [line.Lines] view for the right (after) pane of a
// side-by-side diff.
//
// After aligns its lines with [Result.Before] so both views have equal
// line counts. Consecutive delete/insert sequences are paired row-by-row. When there
// are more deletions than insertions, empty placeholder lines (zero value) fill
// the remaining rows on this side.
//
// Line flags: [line.FlagInserted] for inserted lines, [line.FlagDefault] for
// equal lines and empty placeholders.
//
// Each call returns an independent copy, so overlays added to one result do
// not affect another or the paired [Result.Before] view.
func (r *Result) After() line.Lines {
	rows := r.getAlignedRows()

	lines := make(line.Lines, len(rows))
	for i := range rows {
		lines[i] = rows[i].after.Clone()
	}

	return lines
}

// collectConsecutive collects consecutive ops of the same kind starting at
// index i.
func collectConsecutive(ops []lineOp, i int, kind lcs.OpKind) []lineOp {
	var result []lineOp

	for i < len(ops) && ops[i].kind == kind {
		result = append(result, ops[i])
		i++
	}

	return result
}

// IsEmpty reports whether the diff contains no lines.
func (r *Result) IsEmpty() bool {
	return len(r.ops) == 0
}

// Name returns the diff name in "a..b" format, from the names of the two
// views. A view without a Name method, such as [line.Lines], contributes an
// empty string.
func (r *Result) Name() string {
	return r.name
}

// Diff computes the difference between two views using the default
// algorithm. See [Differ.Diff].
//
// This is a convenience function equivalent to New().Diff(a, b).
func Diff(a, b line.View) *Result {
	return New().Diff(a, b)
}

// lineOp represents a line in the full diff output.
type lineOp struct {
	line line.Line  // Original [line.Line] from source.
	kind lcs.OpKind // One of [lcs.OpEqual], [lcs.OpDelete], [lcs.OpInsert].
}

// opKindDeltas returns the line count deltas this kind affects in before/after
// files.
func opKindDeltas(k lcs.OpKind) (int, int) {
	switch k {
	case lcs.OpEqual:
		return 1, 1
	case lcs.OpDelete:
		return 1, 0
	case lcs.OpInsert:
		return 0, 1
	default:
		return 0, 0
	}
}

// opKindFlag returns the [line.Flag] that marks a line produced by k.
func opKindFlag(k lcs.OpKind) line.Flag {
	switch k {
	case lcs.OpDelete:
		return line.FlagDeleted
	case lcs.OpInsert:
		return line.FlagInserted
	default:
		return line.FlagDefault
	}
}

// lineOps is a slice of [lineOp] values.
type lineOps []lineOp

// toLines converts ops to [line.Lines] with appropriate flags set.
func (ops lineOps) toLines() line.Lines {
	lines := make(line.Lines, 0, len(ops))
	for _, op := range ops {
		ln := op.line.Clone()
		ln.SetFlag(opKindFlag(op.kind))

		lines = append(lines, ln)
	}

	return lines
}

// formatHunkHeader formats a unified diff hunk header like "@@ -1,3 +1,4 @@".
// Uses the same edge case handling as go-udiff (unified.go lines 218-235).
func formatHunkHeader(span position.Span, beforeSums, afterSums *prefixSums) string {
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
		if op.kind != lcs.OpEqual {
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
