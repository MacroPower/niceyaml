package diff

import (
	"fmt"
	"strings"
	"sync"

	"go.jacobcolvin.com/niceyaml/diff/lcs"
	"go.jacobcolvin.com/niceyaml/internal/nilness"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

// Differ computes line differences with a configurable algorithm.
//
// A Differ is safe for concurrent use when its [lcs.Algorithm] is, and the
// default [lcs.Hirschberg] is. The returned [*Result] is safe for concurrent
// use.
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

// WithAlgorithm is an [Option] that sets the diff algorithm. A nil
// algorithm keeps the default, and so does a nil pointer such as a nil
// [*lcs.Hirschberg], so an algorithm built only on some condition can pass
// through as it is. A [Differ] shared between goroutines needs an
// algorithm that is safe for concurrent use.
//
// Default is [lcs.Hirschberg].
func WithAlgorithm(algo lcs.Algorithm) Option {
	return func(d *Differ) {
		d.algo = algo
	}
}

// New creates a new [*Differ] with the given options.
//
// By default it uses [lcs.Hirschberg].
func New(opts ...Option) *Differ {
	d := &Differ{}
	for _, opt := range opts {
		opt(d)
	}

	if nilness.IsNil(d.algo) {
		d.algo = lcs.NewHirschberg()
	}

	return d
}

// Diff computes the difference between two revisions, such as the
// [line.Lines] of two niceyaml Source values.
//
// Lines compare by [line.Line.Content], which strips the line ending, so
// two revisions that differ only in LF versus CRLF endings or in a missing
// final newline diff as equal.
//
// The result shares the lines with its inputs, which never change, and
// carries the flags of the diff itself. Each rendering method,
// [Result.Unified], [Result.Hunks], [Result.Before], and [Result.After],
// returns a fresh [line.View] over those lines.
//
// The revisions should hold distinct lines, as two parsed Source values
// do. When both hold one [*line.Line], such as two [line.Collect] values
// that reorder the lines of one revision, a moved line appears in
// [Result.Unified] once as deleted and once as inserted, and
// [line.View.Index] finds only the first of the two.
//
// Diff panics when the [lcs.Algorithm] returns an [lcs.Op] with an
// [lcs.OpKind] other than [lcs.OpEqual], [lcs.OpDelete], or [lcs.OpInsert],
// or with an index outside the input it refers to.
func (d *Differ) Diff(a, b line.Lines) *Result {
	return &Result{
		before: a,
		after:  b,
		ops:    d.computeOps(a, b),
	}
}

// computeOps computes line operations using the configured algorithm. The
// ops hold the caller's lines, which never change and which nothing can
// reorder inside a [line.Lines] value.
func (d *Differ) computeOps(before, after line.Lines) []lineOp {
	// Precompute content strings once to avoid repeated string building.
	beforeContent := make([]string, before.Len())
	for i, l := range before.All() {
		beforeContent[i] = l.Content()
	}

	afterContent := make([]string, after.Len())
	for i, l := range after.All() {
		afterContent[i] = l.Content()
	}

	// Compute diff using the configured algorithm.
	diffOps := d.algo.Diff(beforeContent, afterContent)

	// Convert to lineOp values.
	ops := make([]lineOp, 0, len(diffOps))

	for i, op := range diffOps {
		switch op.Kind {
		case lcs.OpEqual:
			ops = append(ops, lineOp{kind: lcs.OpEqual, line: after.Line(op.After), before: before.Line(op.Before)})
		case lcs.OpDelete:
			ops = append(ops, lineOp{kind: lcs.OpDelete, line: before.Line(op.Before)})
		case lcs.OpInsert:
			ops = append(ops, lineOp{kind: lcs.OpInsert, line: after.Line(op.After)})
		default:
			// Dropping the op would lose a line from every rendering
			// without a trace, so treat it as a broken Algorithm the same
			// way an out-of-range index already fails.
			panic(fmt.Sprintf("diff: op %d has unknown lcs.OpKind %d", i, op.Kind))
		}
	}

	return ops
}

// Result holds computed diff operations for rendering.
//
// Rendering methods each return a fresh [line.View] that a printer accepts
// directly:
//   - [Result.Unified] returns all lines in unified diff format.
//   - [Result.Hunks] returns only the changed lines with context.
//   - [Result.Before] and [Result.After] return aligned views
//     for side-by-side rendering.
//
// A diff is not a YAML document, so the views carry no parsing or decoding
// behavior.
//
// Create instances with [Differ.Diff] or [Diff].
type Result struct {
	before      line.Lines // The before revision, which names hunk header lines.
	after       line.Lines // The after revision, which names hunk header lines.
	ops         []lineOp
	alignedRows []alignedRow // Side-by-side rows, which getAlignedRows builds on first use.
	alignedOnce sync.Once    // Guards the first build of alignedRows.
}

// alignedRow holds a pair of lines for side-by-side diff rendering, each
// with its flag. Either line may be the empty placeholder.
type alignedRow struct {
	before     *line.Line
	after      *line.Line
	beforeFlag line.Flag
	afterFlag  line.Flag
}

// Unified returns a [line.View] of the complete diff.
//
// The view interleaves lines from both revisions. Unchanged lines come from
// the after revision. Each run of changes holds deleted lines from the
// before revision and inserted lines from the after revision, in the order
// the [lcs.Algorithm] lists them, and [lcs.Hirschberg] lists the deleted
// lines first. Each line carries a [line.Flag] that marks it as deleted,
// inserted, or unchanged.
//
// A decorator that finds lines by identity, such as [line.View.Index] or
// [go.jacobcolvin.com/niceyaml.SourceError.Annotate], finds every line
// of the after revision in the view but only the deleted lines of the
// before revision. [Result.Before] holds every line of the before
// revision.
//
// Each call returns a new view with its own decoration, so overlays added
// to one do not affect another.
func (r *Result) Unified() *line.View {
	return flaggedView(r.ops, func(op lineOp) (*line.Line, line.Flag) {
		return op.line, opKindFlag(op.kind)
	})
}

// Hunks returns a [line.View] of the summarized diff. The view holds the
// changed lines with context lines of unchanged content around each
// change. A context of 0 shows only the changed lines, and Hunks treats
// negative values as 0.
//
// The view is a [line.View.Slice] of [Result.Unified] and keeps its
// indices, as [line.View.Hunks] does. Its [line.View.Lines] returns every
// line of the unified diff, so a range from a search of those lines
// applies to the hunks, and [line.View.Count] is the number of lines the
// hunks hold. Each line carries the flag and the line number it has in
// [Result.Unified], so the view prints with the same numbers. A diff with
// no changes has no hunks, and the view then holds no lines, so it prints
// as an empty view and takes decoration as any other does.
//
// The first line of each hunk carries a [line.Above] annotation holding
// the unified hunk header. The header names each side's lines by their
// [line.Line.Number], the numbers the gutter prints, so a diff of
// [line.View.Held] or of the lines of one document names lines of the
// file. A line with no number counts by its 1-indexed position in its
// input instead.
//
// An input can skip lines of the file, as the held lines of a slice over
// several spans do. No hunk reaches across such a gap on either side.
// Context lines stop at the gap, and a run of changes that crosses it
// splits into several hunks, so each header names only lines its hunk
// holds. A side where a hunk holds no lines places the change next to a
// line on the same side of the gap.
//
// Each call returns a new view with its own decoration, so overlays added
// to one do not affect another.
func (r *Result) Hunks(context int) *line.View {
	var (
		spans position.Spans
		parts []consecutivePart // The part that holds each span.
	)

	for _, part := range r.consecutiveParts() {
		var changes []int

		for i := part.ops.Start; i < part.ops.End; i++ {
			if r.ops[i].kind != lcs.OpEqual {
				changes = append(changes, i)
			}
		}

		// Each change takes its context lines from its own part, so no
		// hunk reaches across a gap in either side's numbering.
		for _, span := range position.ContextSpans(changes, context, part.ops.End).Clamp(part.ops.Start, part.ops.End) {
			spans = append(spans, span)
			parts = append(parts, part)
		}
	}

	unified := r.Unified()

	if len(spans) == 0 {
		// Slice with no span holds every line, and an empty span holds
		// none.
		return unified.Slice(position.Span{})
	}

	view := unified.Slice(spans...)

	// The spans come in order, so one pass over the ops counts the lines
	// each side holds before each hunk and within it.
	var beforeIdx, afterIdx, pos int

	for i, span := range spans {
		for ; pos < span.Start; pos++ {
			b, a := opKindDeltas(r.ops[pos].kind)
			beforeIdx += b
			afterIdx += a
		}

		var beforeCount, afterCount int

		for ; pos < span.End; pos++ {
			b, a := opKindDeltas(r.ops[pos].kind)
			beforeCount += b
			afterCount += a
		}

		// The hunk header goes above the first line of each hunk.
		view.Annotate(span.Start, line.Annotation{
			Content:   r.formatHunkHeader(parts[i], beforeIdx, beforeCount, afterIdx, afterCount),
			Kind:      kind.UIHunkHeader,
			Placement: line.Above,
		})

		beforeIdx += beforeCount
		afterIdx += afterCount
	}

	return view
}

// consecutivePart is a run of a diff's ops within which each side's lines
// carry consecutive [line.Line.Number] values. The ops field spans the
// positions of the run in the diff's ops, and the before and after fields
// span the positions in each side of the lines the run holds.
type consecutivePart struct {
	ops, before, after position.Span
}

// consecutiveParts splits the ops into parts within which each side's
// lines carry consecutive [line.Line.Number] values, so the header of a
// hunk within one part, which gives its first number and its count on
// each side, names the lines the hunk holds. A part ends before the op
// whose line on either side does not follow that side's last line in the
// part. A line with no number follows any line, as [hunkStart] counts it
// by its position instead. When each side holds a run of consecutive
// lines, such as a whole file or one document, the result is one part
// over every op.
func (r *Result) consecutiveParts() []consecutivePart {
	var (
		parts                 []consecutivePart
		part                  consecutivePart // The current part, whose ends give the next op's positions.
		lastBefore, lastAfter int             // The number of each side's last line in the part, or 0.
	)

	for _, op := range r.ops {
		b, a := opKindDeltas(op.kind)
		nextBefore := sideNumber(r.before, part.before.End, b)
		nextAfter := sideNumber(r.after, part.after.End, a)

		if skipsLines(lastBefore, nextBefore) || skipsLines(lastAfter, nextAfter) {
			parts = append(parts, part)
			part = consecutivePart{
				ops:    position.NewSpan(part.ops.End, part.ops.End),
				before: position.NewSpan(part.before.End, part.before.End),
				after:  position.NewSpan(part.after.End, part.after.End),
			}
			lastBefore, lastAfter = 0, 0
		}

		if b > 0 {
			lastBefore = nextBefore
		}

		if a > 0 {
			lastAfter = nextAfter
		}

		part.ops.End++
		part.before.End += b
		part.after.End += a
	}

	return append(parts, part)
}

// sideNumber returns the [line.Line.Number] of the line at idx in side
// when delta shows that an op holds a line of that side. It returns 0
// when the op holds none, and for a position past the end of side, which
// an [lcs.Algorithm] that repeats a line can produce.
func sideNumber(side line.Lines, idx, delta int) int {
	if delta == 0 || idx >= side.Len() {
		return 0
	}

	return side.Line(idx).Number()
}

// skipsLines reports whether a line numbered next does not follow one
// numbered last. Zero stands for no line, or for a line with no number,
// which follows any line.
func skipsLines(last, next int) bool {
	return last > 0 && next > 0 && next != last+1
}

// Stats counts the lines a diff added and removed.
//
// Receive instances from [Result.Stats].
type Stats struct {
	Added   int
	Removed int
}

// Changed reports whether the diff added or removed any line.
func (s Stats) Changed() bool {
	return s.Added > 0 || s.Removed > 0
}

// Stats returns the number of added and removed lines in the diff:
//
//	if s := result.Stats(); s.Changed() {
//		fmt.Printf("+%d -%d\n", s.Added, s.Removed)
//	}
func (r *Result) Stats() Stats {
	var s Stats

	for _, op := range r.ops {
		switch op.kind {
		case lcs.OpInsert:
			s.Added++
		case lcs.OpDelete:
			s.Removed++
		case lcs.OpEqual:
			// Equal lines add to neither count.
		}
	}

	return s
}

// getAlignedRows returns the lazily computed aligned rows for side-by-side
// rendering. It aligns lines so both sides have equal counts:
//   - Equal lines appear on both sides at the same position.
//   - Within each run of changes, the n-th deletion and the n-th insertion
//     share a row, whatever order the algorithm listed them in.
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
				// Equal lines appear on both sides, each with the number it
				// has in its own revision.
				rows = append(rows, alignedRow{
					before: op.before,
					after:  op.line,
				})
				i++

			case lcs.OpDelete, lcs.OpInsert:
				end := i
				for end < len(r.ops) && (r.ops[end].kind == lcs.OpDelete || r.ops[end].kind == lcs.OpInsert) {
					end++
				}

				// Pair the deletes of the run with its inserts, each side in
				// its own order. Each filler row gets a line of its own, so
				// a line pointer names one row of one view.
				del := nextOfKind(r.ops, lcs.OpDelete, i, end)
				ins := nextOfKind(r.ops, lcs.OpInsert, i, end)

				for del < end || ins < end {
					var row alignedRow

					if del < end {
						row.before = r.ops[del].line
						row.beforeFlag = line.FlagDeleted
						del = nextOfKind(r.ops, lcs.OpDelete, del+1, end)
					} else {
						row.before = &line.Line{}
					}

					if ins < end {
						row.after = r.ops[ins].line
						row.afterFlag = line.FlagInserted
						ins = nextOfKind(r.ops, lcs.OpInsert, ins+1, end)
					} else {
						row.after = &line.Line{}
					}

					rows = append(rows, row)
				}

				i = end

			default:
				// Only the cases above advance i, so an op of any other
				// kind would spin this loop forever. Fail the way
				// computeOps does for the same op instead.
				panic(fmt.Sprintf("diff: op %d has unknown lcs.OpKind %d", i, op.kind))
			}
		}

		r.alignedRows = rows
	})

	return r.alignedRows
}

// Before returns a [line.View] for the left (before) pane of a side-by-side
// diff.
//
// Before aligns its lines with [Result.After] so both views have equal
// line counts. It pairs the deleted and inserted lines of each run of
// changes row by row, whatever order the [lcs.Algorithm] lists them in.
// When there are more insertions than deletions, empty placeholder lines
// (zero value) fill the remaining rows on this side.
//
// Deleted lines carry [line.FlagDeleted], and equal lines and empty
// placeholders carry [line.FlagDefault].
//
// Each call returns a new view with its own decoration, so overlays added
// to one do not affect another or the paired [Result.After] view.
func (r *Result) Before() *line.View {
	return flaggedView(r.getAlignedRows(), func(row alignedRow) (*line.Line, line.Flag) {
		return row.before, row.beforeFlag
	})
}

// After returns a [line.View] for the right (after) pane of a side-by-side
// diff.
//
// After aligns its lines with [Result.Before] so both views have equal
// line counts. It pairs the deleted and inserted lines of each run of
// changes row by row, whatever order the [lcs.Algorithm] lists them in.
// When there are more deletions than insertions, empty placeholder lines
// (zero value) fill the remaining rows on this side.
//
// Inserted lines carry [line.FlagInserted], and equal lines and empty
// placeholders carry [line.FlagDefault].
//
// Each call returns a new view with its own decoration, so overlays added
// to one do not affect another or the paired [Result.Before] view.
func (r *Result) After() *line.View {
	return flaggedView(r.getAlignedRows(), func(row alignedRow) (*line.Line, line.Flag) {
		return row.after, row.afterFlag
	})
}

// flaggedView returns a [line.View] of the line pick returns for each item,
// with the flag pick returns for that item set on its line.
func flaggedView[T any](items []T, pick func(T) (*line.Line, line.Flag)) *line.View {
	lines := make([]*line.Line, len(items))
	flags := make([]line.Flag, len(items))

	for i, item := range items {
		lines[i], flags[i] = pick(item)
	}

	view := line.NewView(line.Collect(lines...))

	for i, flag := range flags {
		view.SetFlag(i, flag)
	}

	return view
}

// IsEmpty reports whether the diff contains no lines.
func (r *Result) IsEmpty() bool {
	return len(r.ops) == 0
}

// defaultDiffer is the shared [Differ] behind [Diff]. Its [lcs.Hirschberg]
// is safe for concurrent use and pools its working buffers across calls.
var defaultDiffer = New()

// Diff computes the difference between two revisions using the default
// algorithm. See [Differ.Diff].
//
// Every call goes through one shared [Differ], which is safe for concurrent
// use, so repeated calls reuse its pooled working buffers.
func Diff(a, b line.Lines) *Result {
	return defaultDiffer.Diff(a, b)
}

// lineOp represents a line in the unified diff output.
type lineOp struct {
	line *line.Line // The [line.Line] from the revision it came from.

	// The [line.Line] of the before revision for an [lcs.OpEqual] op, whose
	// line field holds the after revision's line, so each side of an
	// aligned view numbers the line as its own revision does.
	before *line.Line

	kind lcs.OpKind // One of [lcs.OpEqual], [lcs.OpDelete], [lcs.OpInsert].
}

// nextOfKind returns the index of the first op of kind k in ops[from:end],
// or end when there is none.
func nextOfKind(ops []lineOp, k lcs.OpKind, from, end int) int {
	for from < end && ops[from].kind != k {
		from++
	}

	return from
}

// opKindDeltas returns the line count deltas that k contributes to the
// before and after revisions.
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

// formatHunkHeader formats a unified diff hunk header like "@@ -1,3 +1,4 @@"
// in the range syntax that GNU diff -u prints. The part argument gives the
// [consecutivePart] that holds the hunk. The Idx arguments give the
// position of the hunk's first line in each revision, and the Count
// arguments give the number of lines the hunk covers there. Each side
// names its lines by [line.Line.Number], as the gutter does, so a diff of
// part of a file names the lines of the file.
func (r *Result) formatHunkHeader(part consecutivePart, beforeIdx, beforeCount, afterIdx, afterCount int) string {
	var b strings.Builder

	fmt.Fprint(&b, "@@ ")

	writeHunkRange(&b, '-', hunkStart(r.before, part.before, beforeIdx, beforeCount), beforeCount)

	fmt.Fprint(&b, " ")

	writeHunkRange(&b, '+', hunkStart(r.after, part.after, afterIdx, afterCount), afterCount)

	fmt.Fprint(&b, " @@")

	return b.String()
}

// writeHunkRange writes one side of a hunk header, where start is the
// 1-indexed first line of the hunk on that side and count is the number of
// lines the hunk covers there. GNU diff omits the count when it is 1, and a
// count of 0 reports the line before the change instead, so a hunk that
// inserts after line 3 reads "-3,0" and one that inserts into an empty file
// reads "-0,0".
func writeHunkRange(b *strings.Builder, sign byte, start, count int) {
	switch count {
	case 0:
		fmt.Fprintf(b, "%c%d,0", sign, start-1)
	case 1:
		fmt.Fprintf(b, "%c%d", sign, start)
	default:
		fmt.Fprintf(b, "%c%d,%d", sign, start, count)
	}
}

// hunkStart returns the start that [writeHunkRange] takes for one side of
// a hunk, where idx is the position in side of the hunk's first line on
// that side and count is the number of lines the hunk covers there. The
// part argument spans the positions in side of the lines that the hunk's
// [consecutivePart] holds. The start is the [line.Line.Number] of that
// first line. A hunk that covers no lines on the side starts after the
// line before it, or at the side's first line when none comes before, and
// an empty side starts at 1 so the header reads "0,0". When such a hunk
// comes first in its part, the line before it belongs to an earlier part,
// so the hunk starts at the line after it instead if the part holds that
// line. A line with no number, such as the zero value, gives its
// 1-indexed position in side instead, and so does a position past the end
// of side, which an [lcs.Algorithm] that repeats a line can produce.
func hunkStart(side line.Lines, part position.Span, idx, count int) int {
	var (
		pos    int // The position in side of the line that names the start.
		offset int // The distance from that line to the start.
	)

	switch {
	case count > 0:
		pos = idx
	case idx == part.Start && part.Contains(idx):
		pos = idx
	case idx > 0:
		pos, offset = idx-1, 1
	case side.Len() > 0:
		pos = 0
	default:
		return 1
	}

	if pos < side.Len() {
		if n := side.Line(pos).Number(); n > 0 {
			return n + offset
		}
	}

	return idx + 1
}
