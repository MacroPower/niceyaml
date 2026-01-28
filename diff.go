package niceyaml

import (
	"fmt"
	"strings"

	"jacobcolvin.com/niceyaml/diff"
	"jacobcolvin.com/niceyaml/line"
	"jacobcolvin.com/niceyaml/position"
)

// SourceGetter retrieves a [NamedLineSource].
//
// See [Revision] for an implementation.
type SourceGetter interface {
	Source() NamedLineSource
}

// Differ computes line differences using a configurable algorithm.
//
// Differ is not safe for concurrent use because the underlying algorithm
// maintains reusable buffers. Create separate instances for concurrent
// operations. The returned [*DiffResult] is safe for concurrent use.
//
// Create instances with [NewDiffer].
type Differ struct {
	algo diff.Algorithm
}

// DifferOption configures a [Differ].
//
// Available options:
//   - [WithAlgorithm]
type DifferOption func(*Differ)

// WithAlgorithm sets the diff algorithm.
//
// Default is [diff.Hirschberg].
func WithAlgorithm(algo diff.Algorithm) DifferOption {
	return func(d *Differ) {
		d.algo = algo
	}
}

// NewDiffer creates a new [*Differ] with the given options.
//
// If no algorithm is specified, uses [diff.Hirschberg].
func NewDiffer(opts ...DifferOption) *Differ {
	d := &Differ{}
	for _, opt := range opts {
		opt(d)
	}

	if d.algo == nil {
		d.algo = diff.NewHirschberg()
	}

	return d
}

// Diff computes the difference between two sources.
//
// The result can be rendered multiple times with [DiffResult.Full] or
// [DiffResult.Hunks].
func (d *Differ) Diff(a, b SourceGetter) *DiffResult {
	aSource := a.Source()
	bSource := b.Source()
	ops := d.computeOps(aSource, bSource)

	// Precompute prefix sums for O(1) line number and count queries.
	beforeSums := position.NewPrefixSums(len(ops), func(i int) int {
		d, _ := opKindDeltas(ops[i].kind)
		return d
	})
	afterSums := position.NewPrefixSums(len(ops), func(i int) int {
		_, d := opKindDeltas(ops[i].kind)
		return d
	})

	return &DiffResult{
		ops:        ops,
		name:       fmt.Sprintf("%s..%s", aSource.Name(), bSource.Name()),
		beforeSums: beforeSums,
		afterSums:  afterSums,
	}
}

// computeOps computes line operations using the configured algorithm.
func (d *Differ) computeOps(before, after LineGetter) []lineOp {
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

	// Initialize algorithm with input sizes for buffer preallocation.
	d.algo.Init(len(beforeLines), len(afterLines))

	// Compute diff using the configured algorithm.
	diffOps := d.algo.Diff(beforeContent, afterContent)

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

// DiffResult holds computed diff operations for rendering.
//
// Create instances with [Differ.Diff] or [Diff].
type DiffResult struct {
	beforeSums *position.PrefixSums
	afterSums  *position.PrefixSums
	name       string
	ops        []lineOp
}

// Full returns a [*Source] representing the complete diff.
//
// The returned [Source] contains merged tokens from both revisions: unchanged
// lines use tokens from the second source, while changed lines include deleted
// tokens from the first source followed by inserted tokens from the second.
//
// [Source] contains flags for deleted/inserted lines.
func (r *DiffResult) Full() *Source {
	return &Source{
		name:  r.name,
		lines: lineOps(r.ops).toLines(),
	}
}

// Hunks returns a [*Source] and line spans for rendering a summarized diff.
// The context parameter specifies the number of unchanged lines to show around
// each change. A context of 0 shows only the changed lines.
// Negative values are treated as 0.
//
// The source contains all diff lines with flags for deleted/inserted lines.
// Hunk headers are stored in [line.Annotation.Content] for each hunk's first line.
//
// Pass both to [Printer.Print] to render the hunks:
//
//	printer.Print(source, spans...)
func (r *DiffResult) Hunks(context int) (*Source, position.Spans) {
	context = max(0, context)

	if len(r.ops) == 0 {
		return &Source{name: r.name}, nil
	}

	hunkSpans := selectHunkSpans(r.ops, context)

	if len(hunkSpans) == 0 {
		return &Source{name: r.name}, nil
	}

	lines := lineOps(r.ops).toLines()

	// Add hunk header annotations to first line of each hunk.
	for _, span := range hunkSpans {
		hunkHeader := formatHunkHeader(span, r.beforeSums, r.afterSums)
		lines[span.Start].AddAnnotation(line.Annotation{
			Content:  hunkHeader,
			Position: line.Above,
		})
	}

	return &Source{name: r.name, lines: lines}, hunkSpans
}

// Stats returns the number of added and removed lines in the diff.
func (r *DiffResult) Stats() (int, int) {
	var added, removed int

	for _, op := range r.ops {
		switch op.kind {
		case diff.OpInsert:
			added++
		case diff.OpDelete:
			removed++
		case diff.OpEqual:
			// No-op: equal lines don't contribute to stats counts.
		}
	}

	return added, removed
}

// IsEmpty reports whether the diff contains no lines.
func (r *DiffResult) IsEmpty() bool {
	return len(r.ops) == 0
}

// Name returns the diff name in "a..b" format.
func (r *DiffResult) Name() string {
	return r.name
}

// Diff computes the difference between two sources using the default algorithm.
//
// This is a convenience function equivalent to NewDiffer().Diff(a, b).
func Diff(a, b SourceGetter) *DiffResult {
	return NewDiffer().Diff(a, b)
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
