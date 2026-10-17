package diff

import (
	"fmt"
	"slices"
	"strconv"
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
// default [lcs.Hirschberg] is. The [*Result] that [Differ.Diff] returns is
// safe for concurrent use.
//
// Create instances with [New].
type Differ struct {
	config
}

// Option configures a [Differ].
//
// Available options:
//   - [WithAlgorithm]
type Option func(*config)

// config holds the settings an [Option] configures. An option takes it in
// place of the [Differ] that embeds it, so only [New] can apply one.
type config struct {
	algo lcs.Algorithm
}

// WithAlgorithm is an [Option] that sets the diff algorithm. A nil
// algorithm keeps the default, and so does a nil pointer such as a nil
// [*lcs.Hirschberg]. A caller that builds an algorithm only on some
// condition can thus pass it to WithAlgorithm as it is. To share a
// [Differ] between goroutines, you must give it an algorithm that is safe
// for concurrent use.
//
// The default is [lcs.Hirschberg].
func WithAlgorithm(algo lcs.Algorithm) Option {
	return func(c *config) {
		c.algo = algo
	}
}

// New creates a new [*Differ] with the given options.
//
// By default it uses [lcs.Hirschberg].
func New(opts ...Option) *Differ {
	d := &Differ{}
	for _, opt := range opts {
		opt(&d.config)
	}

	if nilness.IsNil(d.algo) {
		d.algo = lcs.NewHirschberg()
	}

	return d
}

// Diff computes the difference between two revisions. Each revision is a
// [line.Sequence], such as the [line.Lines] of a niceyaml Source for a
// whole file or a [*line.View] for the lines it holds. When a file holds
// several documents, the views of one document in two revisions of the
// file diff that document alone:
//
//	result := diff.Diff(before[1].View(), after[1].View())
//
// Diff reads the lines of a view and ignores its decoration, so the flags,
// overlays, and annotations of an input never reach the result.
// [Result.Carry] copies the overlays and annotations of the inputs onto a
// view of the result. Diff treats a nil sequence as empty, and it treats a
// sequence that holds a nil pointer, such as a nil [*line.View], the same
// way.
//
// Diff compares lines by [line.Line.Content], which strips the line ending.
// Two revisions that differ only in LF versus CRLF endings or in a missing
// final newline thus diff as equal.
//
// The result shares the lines of its inputs and carries the flags of the
// diff itself. Those lines never change. Each rendering method returns a
// fresh [line.View] that shares those lines. The rendering methods are
// [Result.Unified], [Result.Hunks], [Result.Before], and [Result.After].
//
// The revisions should hold distinct lines, as two parsed Source values
// do. When both hold the same [*line.Line], such as two [line.Collect] values
// that reorder the lines of one revision, a moved line appears in
// [Result.Unified] once as deleted and once as inserted.
// [line.View.Index] finds only the first of the two, but
// [Result.UnifiedOrigin] and the other methods that map rows give the
// deleted row and the inserted row different origins.
//
// Diff panics when the [lcs.Algorithm] returns an [lcs.Op] with an
// [lcs.OpKind] other than [lcs.OpEqual], [lcs.OpDelete], or [lcs.OpInsert],
// or with an index outside the input it refers to.
func (d *Differ) Diff(a, b line.Sequence) *Result {
	r := &Result{inputs: [2]input{collect(a), collect(b)}}
	r.ops = d.computeOps(r.inputs[Before].lines, r.inputs[After].lines)

	// Each row of the unified view shows the line of its op, and each line
	// of an input maps to the first row that shows it.
	lines := make([]*line.Line, len(r.ops))

	for s := range r.inputs {
		r.inputs[s].unifiedRows = unmappedRows(r.inputs[s].lines.Len())
	}

	for row, op := range r.ops {
		shown := op.shows()
		lines[row] = r.inputs[shown].lines.Line(op.pos[shown])

		for s, p := range op.pos {
			if p >= 0 && r.inputs[s].unifiedRows[p] < 0 {
				r.inputs[s].unifiedRows[p] = row
			}
		}
	}

	r.unified = line.Collect(lines...)

	return r
}

// collect returns the lines of seq as an [input], which a diff reads by
// position. A [line.Lines] value never changes, so collect returns it as it
// is, and a diff of two whole files does not copy their lines.
func collect(seq line.Sequence) input {
	if lines, ok := seq.(line.Lines); ok {
		in := input{lines: lines, indices: make([]int, lines.Len())}
		for i := range in.indices {
			in.indices[i] = i
		}

		return in
	}

	if nilness.IsNil(seq) {
		return input{}
	}

	var (
		ls      []*line.Line
		indices []int
	)

	for i, l := range seq.All() {
		ls = append(ls, l)
		indices = append(indices, i)
	}

	return input{lines: line.Collect(ls...), indices: indices}
}

// computeOps computes line operations with the configured algorithm. Each
// op holds the position of its line on each side. A [line.Lines] value
// never changes, so a position keeps naming the same line.
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

	// Convert to lineOp values. Reading the line at each position checks
	// that the position lies inside its side.
	ops := make([]lineOp, 0, len(diffOps))

	for i, op := range diffOps {
		switch op.Kind {
		case lcs.OpEqual:
			_, _ = before.Line(op.Before), after.Line(op.After)
			ops = append(ops, lineOp{kind: lcs.OpEqual, pos: [2]int{op.Before, op.After}})

		case lcs.OpDelete:
			_ = before.Line(op.Before)
			ops = append(ops, lineOp{kind: lcs.OpDelete, pos: [2]int{op.Before, -1}})

		case lcs.OpInsert:
			_ = after.Line(op.After)
			ops = append(ops, lineOp{kind: lcs.OpInsert, pos: [2]int{-1, op.After}})

		default:
			// Dropping the op would lose a line from every rendering
			// without a trace, so treat it as a broken Algorithm the same
			// way an out-of-range index already fails.
			panic(fmt.Sprintf("diff: op %d has unknown lcs.OpKind %d", i, op.Kind))
		}
	}

	return ops
}

// Side names one of the two revisions a [Result] compares.
type Side uint8

// [Side] constants.
const (
	// Before is the first revision [Differ.Diff] takes, the one the diff
	// changes from.
	Before Side = iota
	// After is the second revision [Differ.Diff] takes, the one the diff
	// changes to.
	After
)

// String returns "before" for [Before], "after" for [After], and
// "Side(n)" for any other value n.
func (s Side) String() string {
	switch s {
	case Before:
		return "before"
	case After:
		return "after"
	default:
		return "Side(" + strconv.Itoa(int(s)) + ")"
	}
}

// Origin names the line of an input that a row of a diff shows, by the
// [Side] that holds the line and its index there. A row is an index of a
// view the [Result] renders.
//
// The index is in the coordinates of the content of the input, the index
// its [line.Sequence] yields with the line. For a [*line.View] it is the
// index [line.View.Line] takes, which a slice of the view keeps. The
// decoration of the input view and a range from a search of it apply at
// that index. It is not the index of the line in [line.View.Held], which
// numbers only the held lines, starting at zero.
type Origin struct {
	// Side is the revision that holds the line.
	Side Side
	// Index is the index of the line in the content of its input.
	Index int
}

// Result holds computed diff operations for rendering.
//
// Each rendering method returns a fresh [line.View] that a printer accepts
// directly:
//   - [Result.Unified] returns all lines in unified diff format.
//   - [Result.Hunks] returns only the changed lines with context.
//   - [Result.HunksOf] returns the changed lines and the lines that a
//     decorated view marks, with context.
//   - [Result.Before] and [Result.After] return aligned views
//     for side-by-side rendering.
//
// [go.jacobcolvin.com/niceyaml/printer.Printer.PrintSideBySide] prints the
// panes of Before and After beside each other.
//
// Every row of a rendering shows a line of an input or, in a side-by-side
// pane, an empty placeholder. [Result.UnifiedOrigin] and
// [Result.PaneOrigin] name the line of the input that a row shows.
// [Result.UnifiedRow] and [Result.PaneRow] find the row that shows a line
// of an input. [Result.Carry] copies the overlays and annotations of the
// input views onto a rendering.
//
// A diff is not a YAML document, so the views do not carry parsing or
// decoding behavior.
//
// Create instances with [Differ.Diff] or [Diff].
type Result struct {
	unified   line.Lines // The line of each op, the content of every unified view.
	ops       []lineOp
	inputs    [2]input // The two revisions, by [Side].
	panes     panes    // The side-by-side panes, which getPanes builds on first use.
	panesOnce sync.Once
}

// input is one revision of a diff.
type input struct {
	// The lines the input sequence yields, in order. A position names a
	// line here.
	lines line.Lines
	// The index each line has in the content of the input sequence, by
	// position. The indices ascend, as a [line.Sequence] yields them.
	indices []int
	// The first row of the unified view that shows each line, by
	// position, or -1. An unchanged line maps to the row of the after
	// line the view shows in its place.
	unifiedRows []int
}

// position returns the position of the line at index in the content of the
// input, and true. It reports false when the input does not hold a line at
// index.
func (in input) position(index int) (int, bool) {
	return slices.BinarySearch(in.indices, index)
}

// panes holds the two panes of a side-by-side diff.
type panes struct {
	// The content of each pane, by [Side]. Each placeholder is a line of
	// its own, so a line pointer names one row of one pane.
	content [2]line.Lines
	rows    []alignedRow
	// The first row of each pane that shows each line of its side, by
	// [Side] and by position, or -1.
	rowOf [2][]int
}

// alignedRow is one row of a side-by-side diff.
type alignedRow struct {
	// The position of the line the row shows on each side, by [Side], or
	// -1 for an empty placeholder.
	pos [2]int
	// Whether the row belongs to a run of changes.
	changed bool
}

// flag returns the [line.Flag] of the row in the pane of side s.
func (row alignedRow) flag(s Side) line.Flag {
	switch {
	case !row.changed || row.pos[s] < 0:
		return line.FlagDefault
	case s == Before:
		return line.FlagDeleted
	default:
		return line.FlagInserted
	}
}

// unmappedRows returns n rows, each -1.
func unmappedRows(n int) []int {
	rows := make([]int, n)
	for i := range rows {
		rows[i] = -1
	}

	return rows
}

// Unified returns a [line.View] of the complete diff.
//
// The view interleaves lines from both revisions. Unchanged lines come from
// the after revision. Each run of changes holds deleted lines from the
// before revision and inserted lines from the after revision, in the order
// the [lcs.Algorithm] lists them. [lcs.Hirschberg] lists the deleted lines
// first. Each line carries a [line.Flag] that marks it as deleted,
// inserted, or unchanged.
//
// A decorator that finds lines by identity, such as [line.View.Index] or
// [go.jacobcolvin.com/niceyaml.Annotate], finds every line of the after
// revision in the view but only the deleted lines of the before revision.
// [Result.UnifiedRow] finds the row of a line of either revision, and
// [Result.Carry] copies the marks of the input views onto the rows.
//
// Each call returns a new view with its own decoration, so overlays added
// to one do not affect another. Every view the calls return is over the
// same content, so a row names the same line in each.
func (r *Result) Unified() *line.View {
	view := line.NewView(r.unified)

	for row, op := range r.ops {
		view.SetFlag(row, opKindFlag(op.kind))
	}

	return view
}

// Hunks returns a [line.View] of the summarized diff. The view holds the
// changed lines with context lines of unchanged content around each
// change. A context of 0 shows only the changed lines, and Hunks treats
// negative values as 0.
//
// The view is a [line.View.Slice] of [Result.Unified] and keeps its
// indices, as [line.View.Hunks] does. Its content is every line of the
// unified diff, so a range from a search of Result.Unified applies to the
// hunks. [line.View.Count] is the number of lines the hunks hold. Each
// line carries the flag and the line number it has in [Result.Unified], so
// the view prints with the same numbers. When the diff does not change any
// line, Hunks returns a view that does not hold any lines. That view prints
// as an empty view and takes decoration as any other view does.
//
// The first line of each hunk carries a [line.Above] annotation that holds
// the unified hunk header. The header names each side's lines by their
// [line.Line.Number], the numbers the gutter prints. A diff of a
// [line.View] that holds part of a file, such as one document, thus names
// lines of the file. A line that does not have a number counts by its
// 1-indexed position in its input instead.
//
// An input can skip lines of the file, as a view sliced to several spans
// does. Hunks never cross such a gap on either side. Context lines stop
// at the gap. A run of changes that crosses the gap splits into several
// hunks, so each header names only the lines that its hunk holds. When a
// hunk does not hold any lines of a side, its header places the change
// next to a line on the same side of the gap.
//
// Each call returns a new view with its own decoration, so overlays added
// to one do not affect another.
//
// Hunks picks its rows from the changes alone. [Result.HunksOf] also picks
// the rows that a decorated view marks, such as an error that
// [Result.Carry] copies onto an unchanged row.
func (r *Result) Hunks(context int) *line.View {
	return r.HunksOf(r.Unified(), context)
}

// HunksOf returns the hunks of view, a view of [Result.Unified] that
// carries decoration, such as the marks that [Result.Carry] copies from
// the inputs. A hunk forms around each row that view decorates: a row with
// a [line.Flag] other than [line.FlagDefault], an overlay, or an
// annotation of a kind other than [kind.UIHunkHeader] and
// [kind.UISeparator]. The flags of the unified view mark the changes, so
// an error on an unchanged row gets a hunk of its own:
//
//	view := result.Unified()
//	result.Carry(view, before, after)
//	hunks := result.HunksOf(view, 2)
//
// The hunks follow the rules of [Result.Hunks]: context lines around each
// marked row, a header above each hunk, and no hunk across a gap in
// either side's numbering. A row the view does not hold is a gap too.
// Context stops at it, no hunk crosses it, and each header counts only the
// lines of each side that its hunk holds.
//
// Each row of the result keeps the flag, the overlays, and the annotations
// that view gives it, except the hunk headers and the separators. HunksOf
// adds headers of its own, so the hunks of hunks match the hunks. The
// result keeps the clip that [line.View.Clip] gave view, as
// [line.View.Slice] does. HunksOf panics when view is not over the content
// of Result.Unified, as [line.View.Over] reports.
func (r *Result) HunksOf(view *line.View, context int) *line.View {
	if !view.Over(r.unified) {
		panic("diff: HunksOf: view is not a view of this Result")
	}

	var (
		spans position.Spans
		parts []consecutivePart // The part that holds each span.
	)

	for _, part := range r.consecutiveParts() {
		for _, run := range heldRuns(view, part.ops) {
			var marked []int

			for i := run.Start; i < run.End; i++ {
				if hunkMarked(view, i) {
					marked = append(marked, i)
				}
			}

			// Each marked row takes its context lines from its own run of
			// held rows in its own part. No hunk thus reaches across a row
			// the view does not hold or a gap in either side's numbering.
			for _, span := range position.ContextSpans(marked, context, run.End).Clamp(run.Start, run.End) {
				spans = append(spans, span)
				parts = append(parts, part)
			}
		}
	}

	hunks := r.Unified().Slice(spans...).Clip(view.ClipCols())

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
		hunks.Annotate(span.Start, line.Annotation{
			Content:   r.formatHunkHeader(parts[i], beforeIdx, beforeCount, afterIdx, afterCount),
			Kind:      kind.UIHunkHeader,
			Placement: line.Above,
		})

		beforeIdx += beforeCount
		afterIdx += afterCount
	}

	// Each row takes the decoration of view after the header, so the
	// header stays above the marks, as Carry keeps it. The headers and the
	// separators that a view of hunks carries need not match the hunks of
	// the result, so they stay out.
	for i := range hunks.All() {
		hunks.SetFlag(i, view.Flag(i))

		if o := view.Overlays(i); len(o) > 0 {
			hunks.AddLineOverlay(i, o...)
		}

		for _, a := range view.Annotations(i) {
			if !isHunkMark(a) {
				hunks.Annotate(i, a)
			}
		}
	}

	return hunks
}

// heldRuns returns the runs of consecutive rows within ops that view
// holds, in order.
func heldRuns(view *line.View, ops position.Span) []position.Span {
	var (
		runs []position.Span
		run  = position.NewSpan(ops.Start, ops.Start)
	)

	for i := ops.Start; i < ops.End; i++ {
		if !view.Contains(i) {
			if run.End > run.Start {
				runs = append(runs, run)
			}

			run = position.NewSpan(i+1, i+1)

			continue
		}

		run.End = i + 1
	}

	if run.End > run.Start {
		runs = append(runs, run)
	}

	return runs
}

// hunkMarked reports whether row i of view carries a flag, an overlay, or
// an annotation of a kind other than the ones [isHunkMark] reports.
func hunkMarked(view *line.View, i int) bool {
	return view.Flag(i) != line.FlagDefault || len(view.Overlays(i)) > 0 ||
		slices.ContainsFunc(view.Annotations(i), func(a line.Annotation) bool { return !isHunkMark(a) })
}

// isHunkMark reports whether a is a hunk header, of kind
// [kind.UIHunkHeader], or a separator, of kind [kind.UISeparator], as a
// view of hunks carries them.
func isHunkMark(a line.Annotation) bool {
	return a.Kind == kind.UIHunkHeader || a.Kind == kind.UISeparator
}

// consecutivePart is a run of a diff's ops within which each side's lines
// carry consecutive [line.Line.Number] values. The ops field spans the
// positions of the run in the diff's ops. The before and after fields span
// the positions in each side of the lines the run holds.
type consecutivePart struct {
	ops, before, after position.Span
}

// consecutiveParts splits the ops into parts within which each side's
// lines carry consecutive [line.Line.Number] values. The header of a hunk
// gives its first number and its count on each side. A hunk within one
// part thus has a header that names the lines that it holds. A part ends
// before the op whose line on either side does not follow that side's
// last line in the part. A line that does not have a number follows any
// line, because [hunkStart] counts it by its position instead. When each
// side holds a run of consecutive lines, such as a whole file or one
// document, the result is one part over every op.
func (r *Result) consecutiveParts() []consecutivePart {
	var (
		parts                 []consecutivePart
		part                  consecutivePart // The current part, whose ends give the next op's positions.
		lastBefore, lastAfter int             // The number of each side's last line in the part, or 0.
	)

	for _, op := range r.ops {
		b, a := opKindDeltas(op.kind)
		nextBefore := sideNumber(r.inputs[Before].lines, part.before.End, b)
		nextAfter := sideNumber(r.inputs[After].lines, part.after.End, a)

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
// when the op does not hold a line of side. It also returns 0 for a
// position past the end of side, which an [lcs.Algorithm] that repeats a
// line can produce.
func sideNumber(side line.Lines, idx, delta int) int {
	if delta == 0 || idx >= side.Len() {
		return 0
	}

	return side.Line(idx).Number()
}

// skipsLines reports whether a line numbered next does not follow one
// numbered last. A zero in either argument stands for a missing line or
// for a line that does not have a number, and skipsLines reports false for
// it.
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

// getPanes returns the panes for side-by-side rendering, and it builds
// them on the first call. It aligns lines so both sides have equal counts:
//   - Equal lines appear on both sides at the same position.
//   - Within each run of changes, the n-th deletion and the n-th insertion
//     share a row, whatever order the algorithm listed them in.
//   - Unmatched deletions have empty placeholders on the right.
//   - Unmatched insertions have empty placeholders on the left.
func (r *Result) getPanes() *panes {
	r.panesOnce.Do(func() {
		rows := make([]alignedRow, 0, len(r.ops))

		i := 0
		for i < len(r.ops) {
			op := r.ops[i]

			switch op.kind {
			case lcs.OpEqual:
				// Equal lines appear on both sides, each with the number it
				// has in its own revision.
				rows = append(rows, alignedRow{pos: op.pos})
				i++

			case lcs.OpDelete, lcs.OpInsert:
				end := i
				for end < len(r.ops) && (r.ops[end].kind == lcs.OpDelete || r.ops[end].kind == lcs.OpInsert) {
					end++
				}

				// Pair the deletes of the run with its inserts, each side in
				// its own order.
				del := nextOfKind(r.ops, lcs.OpDelete, i, end)
				ins := nextOfKind(r.ops, lcs.OpInsert, i, end)

				for del < end || ins < end {
					row := alignedRow{pos: [2]int{-1, -1}, changed: true}

					if del < end {
						row.pos[Before] = r.ops[del].pos[Before]
						del = nextOfKind(r.ops, lcs.OpDelete, del+1, end)
					}

					if ins < end {
						row.pos[After] = r.ops[ins].pos[After]
						ins = nextOfKind(r.ops, lcs.OpInsert, ins+1, end)
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

		r.panes.rows = rows

		for s := range r.inputs {
			in := r.inputs[s]
			lines := make([]*line.Line, len(rows))
			rowOf := unmappedRows(in.lines.Len())

			for row, ar := range rows {
				p := ar.pos[s]
				if p < 0 {
					lines[row] = &line.Line{}

					continue
				}

				lines[row] = in.lines.Line(p)

				if rowOf[p] < 0 {
					rowOf[p] = row
				}
			}

			r.panes.content[s] = line.Collect(lines...)
			r.panes.rowOf[s] = rowOf
		}
	})

	return &r.panes
}

// Before returns a [line.View] for the left (before) pane of a side-by-side
// diff.
//
// Before aligns its lines with [Result.After] so both views have equal
// line counts. It pairs the deleted and inserted lines of each run of
// changes row by row, whatever order the [lcs.Algorithm] lists them in.
// When a run of changes holds more insertions than deletions, empty
// placeholder lines (zero value) fill its remaining rows on this side.
//
// Deleted lines carry [line.FlagDeleted], and equal lines and empty
// placeholders carry [line.FlagDefault].
//
// Each call returns a new view with its own decoration, so overlays added
// to one do not affect another or the paired [Result.After] view. Every
// view the calls return is over the same content.
//
// [go.jacobcolvin.com/niceyaml/printer.Printer.PrintSideBySide] prints
// the two panes beside each other and keeps their lines side by side.
func (r *Result) Before() *line.View {
	return r.pane(Before)
}

// After returns a [line.View] for the right (after) pane of a side-by-side
// diff.
//
// After aligns its lines with [Result.Before] so both views have equal
// line counts. It pairs the deleted and inserted lines of each run of
// changes row by row, whatever order the [lcs.Algorithm] lists them in.
// When a run of changes holds more deletions than insertions, empty
// placeholder lines (zero value) fill its remaining rows on this side.
//
// Inserted lines carry [line.FlagInserted], and equal lines and empty
// placeholders carry [line.FlagDefault].
//
// Each call returns a new view with its own decoration, so overlays added
// to one do not affect another or the paired [Result.Before] view. Every
// view the calls return is over the same content.
//
// [go.jacobcolvin.com/niceyaml/printer.Printer.PrintSideBySide] prints
// the two panes beside each other and keeps their lines side by side.
func (r *Result) After() *line.View {
	return r.pane(After)
}

// pane returns a new view of the pane of side s with the flags of its rows.
func (r *Result) pane(s Side) *line.View {
	p := r.getPanes()
	view := line.NewView(p.content[s])

	for row, ar := range p.rows {
		view.SetFlag(row, ar.flag(s))
	}

	return view
}

// UnifiedOrigin returns the [Origin] of the line that row shows in
// [Result.Unified], and true. [Result.Hunks] keeps the rows of the unified
// view, so a row of the hunks has the same Origin. A deleted row shows a
// line of [Before], and an inserted or unchanged row shows a line of
// [After]. UnifiedOrigin reports false for a row outside the unified view.
func (r *Result) UnifiedOrigin(row int) (Origin, bool) {
	return r.origin(r.unifiedShows(row))
}

// unifiedShows returns the [Side] and the position there of the line that
// row shows in the unified view, and true. It reports false for a row
// outside the view.
func (r *Result) unifiedShows(row int) (Side, int, bool) {
	if row < 0 || row >= len(r.ops) {
		return 0, 0, false
	}

	op := r.ops[row]
	s := op.shows()

	return s, op.pos[s], true
}

// paneShows returns s and the position there of the line that row shows in
// the pane of s, and true. It reports false for a row outside the pane, for
// a Side that does not name either revision, and for a placeholder.
func (r *Result) paneShows(s Side, row int) (Side, int, bool) {
	if s > After {
		return 0, 0, false
	}

	p := r.getPanes()
	if row < 0 || row >= len(p.rows) || p.rows[row].pos[s] < 0 {
		return 0, 0, false
	}

	return s, p.rows[row].pos[s], true
}

// origin returns the [Origin] of the line at position p of side s, and ok.
// It returns the zero Origin when ok is false.
func (r *Result) origin(s Side, p int, ok bool) (Origin, bool) {
	if !ok {
		return Origin{}, false
	}

	return Origin{Side: s, Index: r.inputs[s].indices[p]}, true
}

// PaneOrigin returns the [Origin] of the line that row shows in the pane
// of side s, and true. The pane of [Before] is [Result.Before], and the
// pane of [After] is [Result.After]. The Origin names a line of side s.
// PaneOrigin reports false for a row outside the pane, for a Side that
// does not name either revision, and for an empty placeholder. A
// placeholder does not show a line of an input.
func (r *Result) PaneOrigin(s Side, row int) (Origin, bool) {
	return r.origin(r.paneShows(s, row))
}

// UnifiedRow returns the row of [Result.Unified] that shows the line o
// names, and true. The unified view shows each unchanged line once, as the
// line of [After]. A line of [Before] that the diff left unchanged maps to
// the row of that after line. UnifiedRow reports false when o does not
// name a line of an input. A line that the [lcs.Algorithm] lists more than
// once maps to the first row that shows it.
//
// [Result.Hunks] keeps the rows of the unified view and holds a row when a
// hunk covers it, as [line.View.Contains] reports.
func (r *Result) UnifiedRow(o Origin) (int, bool) {
	if o.Side > After {
		return 0, false
	}

	in := r.inputs[o.Side]

	p, ok := in.position(o.Index)
	if !ok || in.unifiedRows[p] < 0 {
		return 0, false
	}

	return in.unifiedRows[p], true
}

// PaneRow returns the row of the pane of o.Side that shows the line o
// names, and true. [Result.Before] shows every line of [Before], and
// [Result.After] shows every line of [After]. The panes align, so the same
// row of the other pane shows the paired line or an empty placeholder.
// PaneRow reports false when o does not name a line of an input. A line
// that the [lcs.Algorithm] lists more than once maps to the first row that
// shows it.
func (r *Result) PaneRow(o Origin) (int, bool) {
	if o.Side > After {
		return 0, false
	}

	in := r.inputs[o.Side]

	p, ok := in.position(o.Index)
	if !ok {
		return 0, false
	}

	row := r.getPanes().rowOf[o.Side][p]
	if row < 0 {
		return 0, false
	}

	return row, true
}

// Carry copies the overlays and annotations of the input views onto dst,
// a view that [Result.Unified], [Result.Hunks], [Result.Before], or
// [Result.After] returned, or a slice, clone, or clip of one. Each row
// that dst holds takes the marks of the line its [Origin] names. A line of
// [Before] takes them from before, and a line of [After] from after. The
// marks follow the annotations each row carries already, so a hunk header
// stays above them.
//
//	result := diff.Diff(before, after)
//	view := result.Unified()
//	result.Carry(view, before, after)
//
// The index of an Origin is in the coordinates of the content of its
// input. Each input view must be over the content of its input, as
// [line.View.Over] reports. That content is the [line.Lines] that the
// caller passed to Diff, or the content of the view that the caller passed.
// A row takes the marks of an input view only when the view holds the line
// of the row at the index of its Origin. A nil view does not hold any
// line. Carry reads the input views when it runs, so it takes the marks
// they carry at that moment.
//
// The unified view shows each unchanged line once, as the line of [After],
// so an unchanged row takes only the marks of after. A caller that wants
// the marks of before on the unchanged rows can map each line of [Before]
// to its row with [Result.UnifiedRow]. The row is unchanged when
// [Result.UnifiedOrigin] names a line of [After] for it.
//
// A row keeps each mark once, so carrying the marks again does not add
// any. The dst view carries the flags of the diff, so Carry does not copy
// flags. Carry ignores a nil dst. It panics when dst is over content that
// this Result did not render, as [line.View.Over] reports.
func (r *Result) Carry(dst, before, after *line.View) {
	if dst == nil {
		return
	}

	shows := r.unifiedShows

	// GetPanes builds the panes on first use, so testing the unified
	// content first keeps a unified dst from building them.
	if !dst.Over(r.unified) {
		p := r.getPanes()

		switch {
		case dst.Over(p.content[Before]):
			shows = func(row int) (Side, int, bool) { return r.paneShows(Before, row) }
		case dst.Over(p.content[After]):
			shows = func(row int) (Side, int, bool) { return r.paneShows(After, row) }
		default:
			panic("diff: Carry: dst is not a view of this Result")
		}
	}

	src := [2]*line.View{before, after}

	for row := range dst.All() {
		s, p, ok := shows(row)
		if !ok {
			continue
		}

		// A view of other content can hold a line at the same index, so
		// the view gives its marks only when it holds the line the diff
		// read there.
		view, i := src[s], r.inputs[s].indices[p]
		if !view.Contains(i) || view.Line(i) != r.inputs[s].lines.Line(p) {
			continue
		}

		for _, o := range view.Overlays(i) {
			if !slices.Contains(dst.Overlays(row), o) {
				dst.AddLineOverlay(row, o)
			}
		}

		for _, a := range view.Annotations(i) {
			if !slices.Contains(dst.Annotations(row), a) {
				dst.Annotate(row, a)
			}
		}
	}
}

// IsEmpty reports whether the diff holds zero lines.
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
func Diff(a, b line.Sequence) *Result {
	return defaultDiffer.Diff(a, b)
}

// lineOp is one row of the unified view.
type lineOp struct {
	// The position of the line the op holds on each side, by [Side], or
	// -1 for a side that the op does not hold a line of. An [lcs.OpEqual]
	// op holds a line of each side, so each side of an aligned view numbers
	// the line as its own revision does.
	pos  [2]int
	kind lcs.OpKind // One of [lcs.OpEqual], [lcs.OpDelete], [lcs.OpInsert].
}

// shows returns the [Side] of the line the unified view shows for the op.
// The unified view shows the after line of an unchanged pair.
func (op lineOp) shows() Side {
	if op.kind == lcs.OpDelete {
		return Before
	}

	return After
}

// nextOfKind returns the index of the first op of kind k in ops[from:end],
// or end when ops[from:end] does not hold one.
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

// opKindFlag returns the [line.Flag] that marks a line that k produces.
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

// formatHunkHeader formats a unified diff hunk header, such as
// "@@ -1,3 +1,4 @@", in the range syntax that GNU diff -u prints. The part
// argument gives the [consecutivePart] that holds the hunk. The Idx
// arguments give the position of the hunk's first line in each revision,
// and the Count arguments give the number of lines the hunk covers there.
// Each side names its lines by [line.Line.Number], as the gutter does, so
// a diff of part of a file names the lines of the file.
func (r *Result) formatHunkHeader(part consecutivePart, beforeIdx, beforeCount, afterIdx, afterCount int) string {
	var b strings.Builder

	fmt.Fprint(&b, "@@ ")

	writeHunkRange(&b, '-', hunkStart(r.inputs[Before].lines, part.before, beforeIdx, beforeCount), beforeCount)

	fmt.Fprint(&b, " ")

	writeHunkRange(&b, '+', hunkStart(r.inputs[After].lines, part.after, afterIdx, afterCount), afterCount)

	fmt.Fprint(&b, " @@")

	return b.String()
}

// writeHunkRange writes one side of a hunk header. The start argument is
// the 1-indexed first line of the hunk on that side, and count is the
// number of lines the hunk covers there. GNU diff omits the count when it
// is 1. A count of 0 reports the line before the change instead. A hunk
// that inserts after the line numbered 3 thus reads "-3,0", and one that
// inserts into an empty file reads "-0,0".
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
// a hunk. The idx argument is the position in side of the hunk's first
// line on that side, and count is the number of lines the hunk covers
// there. The part argument spans the positions in side of the lines that
// the hunk's [consecutivePart] holds.
//
// The start is the [line.Line.Number] of that first line. A hunk that does
// not cover any lines on the side starts after the line before it. When the
// side does not hold a line before it, the hunk starts at the side's first
// line. On an empty side, it starts at 1, so the header reads "0,0". When
// such a hunk comes first in its part, the line before it belongs to an
// earlier part. The hunk then starts at the line after it instead, if the
// part holds that line.
//
// A line that does not have a number, such as the zero value, gives its
// 1-indexed position in side instead. So does a position past the end of
// side, which an [lcs.Algorithm] that repeats a line can produce.
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
