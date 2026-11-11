package diff

import (
	"context"
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

// Differ computes line differences. Its line key decides which lines are
// equal, and its algorithm aligns the lines of the two revisions.
// [WithLineKey] and [WithAlgorithm] configure them.
//
// A Differ is safe for concurrent use when its [lcs.Algorithm] and its line
// key are. The default [lcs.Hirschberg] and the default key are. The
// [*Result] that [Differ.Diff] returns is safe for concurrent use.
//
// Create instances with [New].
type Differ struct {
	config
}

// Option configures a [Differ].
//
// Available options:
//   - [WithAlgorithm]
//   - [WithLineKey]
type Option func(*config)

// config holds the settings an [Option] configures. An option takes it in
// place of the [Differ] that embeds it, so only [New] can apply one.
type config struct {
	algo lcs.Algorithm
	key  func(*line.Line) string
}

// WithAlgorithm is an [Option] that sets the diff algorithm, which aligns
// the lines of the two revisions. The algorithm receives the key of each
// line, as [WithLineKey] sets it. The key thus decides which lines are
// equal, and the algorithm decides which equal lines to pair.
//
// A nil algorithm keeps the default, and so does a nil pointer such as a
// nil [*lcs.Hirschberg]. A caller that builds an algorithm only on some
// condition can thus pass it to WithAlgorithm as it is. To share a
// [Differ] between goroutines, you must give it an algorithm that is safe
// for concurrent use.
//
// [Differ.Diff] passes its context to the algorithm and returns the error
// of the algorithm as it is. It panics when the ops of the algorithm break
// the rule that [lcs.Algorithm] states.
//
// The default is [lcs.Hirschberg].
func WithAlgorithm(algo lcs.Algorithm) Option {
	return func(c *config) {
		c.algo = algo
	}
}

// WithLineKey is an [Option] that sets the key of a line. A diff treats two
// lines as equal when their keys are equal. A key that leaves a detail out
// thus makes the diff ignore that detail. This key ignores comments:
//
//	diff.WithLineKey(func(l *line.Line) string {
//		var sb strings.Builder
//
//		for _, tk := range l.Tokens() {
//			if tk.Type != token.CommentType {
//				sb.WriteString(tk.Origin)
//			}
//		}
//
//		return strings.TrimRight(sb.String(), " \r\n")
//	})
//
// The key changes only which lines the diff pairs. Every rendering shows
// each line as its revision wrote it. [Result.Unified] shows two lines with
// equal keys as one unchanged row, which holds the line of the after
// revision. [Result.Before] shows the line of the before revision. A key
// that gives two different lines the same string hides the change between
// them.
//
// [Differ.Diff] calls key for each line of both revisions. A nil key keeps
// the default. To share a [Differ] between goroutines, you must give it a
// key that is safe for concurrent use.
//
// The default is [line.Line.Content].
func WithLineKey(key func(*line.Line) string) Option {
	return func(c *config) {
		c.key = key
	}
}

// New creates a new [*Differ] with the given options.
//
// By default it compares lines by [line.Line.Content] and aligns them with
// [lcs.Hirschberg].
func New(opts ...Option) *Differ {
	d := &Differ{}
	for _, opt := range opts {
		opt(&d.config)
	}

	if nilness.IsNil(d.algo) {
		d.algo = lcs.NewHirschberg()
	}

	if d.key == nil {
		d.key = (*line.Line).Content
	}

	return d
}

// Diff computes the difference between two revisions. Each revision is a
// [line.Sequence], such as the [line.Lines] of a niceyaml Source for a
// whole file or a [*line.View] for the lines it holds. When a file holds
// several documents, the views of one document in two revisions of the
// file diff that document alone:
//
//	result, err := diff.Diff(ctx, before[1].View(), after[1].View())
//
// Diff reads the lines of a view and ignores its decoration, so the flags,
// overlays, and annotations of an input never reach the result.
// [Result.Carry] copies the overlays and annotations of the inputs onto a
// view of the result. Diff treats a nil sequence as empty, and it treats a
// sequence that holds a nil pointer, such as a nil [*line.View], the same
// way.
//
// Diff compares lines by their keys. The default key is
// [line.Line.Content], which strips the line ending. Two revisions that
// differ only in LF versus CRLF endings or in a missing final newline thus
// diff as equal. [WithLineKey] sets another key.
//
// The result shares the lines of its inputs and carries the flags of the
// diff itself. Those lines never change. Each rendering method returns a
// fresh [line.View] that shares those lines. The rendering methods are
// [Result.Unified], [Result.Hunks], [Result.Before], [Result.After], and
// [Result.PaneHunks].
//
// The revisions should hold distinct lines, as two parsed Source values
// do. When both hold the same [*line.Line], such as two [line.Collect] values
// that reorder the lines of one revision, a moved line appears in
// [Result.Unified] once as deleted and once as inserted.
// [line.View.Index] finds only the first of the two, but
// [Result.UnifiedOrigin] and the other methods that map rows give the
// deleted row and the inserted row different origins.
//
// A diff can take time close to the square of the number of lines, as
// [lcs] describes, so ctx bounds it. Diff checks ctx before it calls the
// [lcs.Algorithm], and [lcs.Hirschberg] checks it while it searches. When
// ctx has ended, or the algorithm returns an error, Diff returns a nil
// [*Result] with that error as it is. The error of ctx matches
// [context.Canceled] or [context.DeadlineExceeded].
//
// Diff panics when the ops that the [lcs.Algorithm] returns break the rule
// that [lcs.Algorithm] states. Such ops hold an [lcs.OpKind] other than
// [lcs.OpEqual], [lcs.OpDelete], or [lcs.OpInsert], or they drop, repeat,
// or reorder a line of a revision.
func (d *Differ) Diff(ctx context.Context, a, b line.Sequence) (*Result, error) {
	err := ctx.Err()
	if err != nil {
		return nil, err //nolint:wrapcheck // The error of ctx, as Diff documents.
	}

	r := &Result{inputs: [2]input{collect(a), collect(b)}}

	r.ops, err = d.computeOps(ctx, r.inputs[Before].lines, r.inputs[After].lines)
	if err != nil {
		return nil, err
	}

	// Each row of the unified view shows the line of its op. The ops hold
	// each line of an input once, so each line maps to one row.
	lines := make([]*line.Line, len(r.ops))

	for s := range r.inputs {
		r.inputs[s].unifiedRows = make([]int, r.inputs[s].lines.Len())
	}

	for row, op := range r.ops {
		shown := op.shows()
		lines[row] = r.inputs[shown].lines.Line(op.pos[shown])

		for s, p := range op.pos {
			if p >= 0 {
				r.inputs[s].unifiedRows[p] = row
			}
		}
	}

	r.unified = line.Collect(lines...)

	return r, nil
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

// computeOps computes line operations with the configured key and
// algorithm. Each op holds the position of its line on each side. A
// [line.Lines] value never changes, so a position keeps naming the same
// line.
//
// The ops must hold every line of each side once, in order, as
// [lcs.Algorithm] states. Ops that drop, repeat, or reorder a line would
// render a wrong diff without a trace, so computeOps panics on them. It
// returns the error of the algorithm as it is.
func (d *Differ) computeOps(ctx context.Context, before, after line.Lines) ([]lineOp, error) {
	diffOps, err := d.algo.Diff(ctx, d.keys(before), d.keys(after))
	if err != nil {
		return nil, err //nolint:wrapcheck // Diff returns the error of the algorithm as it is.
	}

	ops := make([]lineOp, 0, len(diffOps))

	var (
		sides = [2]line.Lines{before, after}
		next  [2]int // The position that the next op must name on each side, by [Side].
	)

	// Take returns the position that op i names on side s, which must be
	// the next position of s, and steps past it.
	take := func(i int, s Side, pos int) int {
		switch {
		case next[s] >= sides[s].Len():
			panic(fmt.Sprintf("diff: op %d names %s line %d, want no more %s lines", i, s, pos, s))
		case pos != next[s]:
			panic(fmt.Sprintf("diff: op %d names %s line %d, want %d", i, s, pos, next[s]))
		}

		next[s]++

		return pos
	}

	for i, op := range diffOps {
		switch op.Kind {
		case lcs.OpEqual:
			b, a := take(i, Before, op.Before), take(i, After, op.After)
			ops = append(ops, lineOp{kind: lcs.OpEqual, pos: [2]int{b, a}})

		case lcs.OpDelete:
			ops = append(ops, lineOp{kind: lcs.OpDelete, pos: [2]int{take(i, Before, op.Before), -1}})

		case lcs.OpInsert:
			ops = append(ops, lineOp{kind: lcs.OpInsert, pos: [2]int{-1, take(i, After, op.After)}})

		default:
			panic(fmt.Sprintf("diff: op %d has unknown lcs.OpKind %d", i, op.Kind))
		}
	}

	for s, lines := range sides {
		if next[s] != lines.Len() {
			panic(fmt.Sprintf("diff: ops name %d of %d %s lines", next[s], lines.Len(), Side(s)))
		}
	}

	return ops, nil
}

// keys returns the key of each line of lines, by position. The algorithm
// compares the keys, so each call of [Differ.Diff] computes each key once.
func (d *Differ) keys(lines line.Lines) []string {
	keys := make([]string, lines.Len())
	for i, l := range lines.All() {
		keys[i] = d.key(l)
	}

	return keys
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
//   - [Result.PaneHunks] and [Result.PaneHunksOf] return the hunks of the
//     side-by-side panes, aligned as Before and After are.
//
// [go.jacobcolvin.com/niceyaml/printer.Printer.PrintSideBySide] prints the
// panes of Before and After, or the pane hunks, beside each other.
//
// Every row of a rendering shows a line of an input or, in a side-by-side
// pane, an empty placeholder. [Result.UnifiedOrigin] and
// [Result.PaneOrigin] name the line of the input that a row shows.
// [Result.UnifiedRow] and [Result.PaneRow] find the row that shows a line
// of an input. [Result.Carry] copies the overlays and annotations of the
// input views onto a rendering.
//
// [Result.Changes] returns the changes as data for a caller that reports
// or applies them instead of printing them. Each [Change] spans the lines
// that one run of changes deletes and inserts. [Result.Stats] counts the
// added and removed lines.
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
	// The row of the unified view that shows each line, by position. An
	// unchanged line maps to the row of the after line the view shows in
	// its place.
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
	// The row of each pane that shows each line of its side, by [Side] and
	// by position.
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
func (r *Result) Hunks(contextLines int) *line.View {
	return r.HunksOf(r.Unified(), contextLines)
}

// HunksOf returns the hunks of view, a view of [Result.Unified] that
// carries decoration, such as the marks that [Result.Carry] copies from
// the inputs. A hunk forms around each row that view decorates: a row with
// a [line.Flag] other than [line.FlagDefault], an overlay, or an
// annotation that [line.Annotation.Marks]. The flags of the unified view
// mark the changes, so an error on an unchanged row gets a hunk of its
// own:
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
// that view gives it, except the annotations above a row that do not
// mark, such as hunk headers and separators. HunksOf adds headers of its
// own, so the hunks of hunks match the hunks. A separator below a row
// labels that row and not a gap, so it stays. The row that counts the
// problems an error excerpt does not mark on a line is such a separator.
//
// The result keeps the clip that [line.View.Clip] gave view, as
// [line.View.Slice] does. HunksOf panics when view is not over the content
// of Result.Unified, as [line.View.Over] reports.
//
// [Result.PaneHunksOf] returns the hunks of two side-by-side panes.
func (r *Result) HunksOf(view *line.View, contextLines int) *line.View {
	if !view.Over(r.unified) {
		panic("diff: HunksOf: view is not a view of this Result")
	}

	rows := func(row int) [2]int { return r.ops[row].pos }
	marked := func(row int) bool { return hunkMarked(view, row) }

	spans, parts := hunkSpans(r.consecutiveParts(len(r.ops), rows), contextLines, marked, view)

	return hunksOf(r.Unified(), view, spans, r.hunkHeaders(spans, parts, rows))
}

// PaneHunks returns the hunks of the side-by-side panes, the hunks of
// [Result.Before] first and the hunks of [Result.After] second. Both
// hold the rows of each run of changes, with context rows around each run.
// Both thus hold the same rows and pair them as the panes do:
//
//	left, right := result.PaneHunks(3)
//	lipgloss.Println(p.PrintSideBySide(left, right))
//
// The hunks of one pane alone, such as [line.View.Hunks] of
// Result.Before, do not pair with the other pane. A pure insertion
// decorates only the rows of [Result.After], so the hunks of
// Result.Before leave out its rows and the context rows around it.
//
// PaneHunks returns PaneHunksOf(r.Before(), r.After(), contextLines). See
// [Result.PaneHunksOf] for the rows, the headers, and the gaps.
func (r *Result) PaneHunks(contextLines int) (*line.View, *line.View) {
	return r.PaneHunksOf(r.Before(), r.After(), contextLines)
}

// PaneHunksOf returns the hunks of two side-by-side panes, as
// [Result.HunksOf] does for the unified view. The before argument is a
// view of [Result.Before], and after is a view of [Result.After]. Each
// carries decoration, such as the marks that [Result.Carry] copies from
// the inputs:
//
//	left, right := result.Before(), result.After()
//	result.Carry(left, beforeView, afterView)
//	result.Carry(right, beforeView, afterView)
//	left, right = result.PaneHunksOf(left, right, 2)
//
// A hunk forms around each row that holds a change and around each row
// that either view decorates, as [Result.HunksOf] states. A row that
// holds a change marks a hunk even in the pane that shows a placeholder
// there, so both results hold the same rows. Each result is a
// [line.View.Slice] of its pane over the same spans, so a row keeps its
// index, and [Result.PaneOrigin], [Result.PaneRow], [Result.Carry], and
// [go.jacobcolvin.com/niceyaml/printer.Printer.PrintSideBySide] take the
// results as they take the panes.
//
// The hunks follow the rules of [Result.Hunks]: context rows around each
// marked row, a header above each hunk, and no hunk across a gap in
// either side's numbering. A row that either view does not hold is a gap
// too. The first row of each hunk carries the same header in both
// results, and the header names the lines of each side that the hunk
// holds. Without a gap, a hunk holds the lines that the hunk of
// [Result.Hunks] at the same context holds, so its header reads the same.
// A gap splits the panes between two rows, but it can split the unified
// view inside a run of changes, so the hunks next to a gap can differ.
//
// Each result keeps the flags, the overlays, the annotations, and the clip
// of its view, except the annotations above a row that do not mark, as
// [Result.HunksOf] states. PaneHunksOf panics when before is not over the
// content of Result.Before, or after is not over the content of
// Result.After, as [line.View.Over] reports.
func (r *Result) PaneHunksOf(before, after *line.View, contextLines int) (*line.View, *line.View) {
	p := r.getPanes()

	if !before.Over(p.content[Before]) {
		panic("diff: PaneHunksOf: before is not a view of the before pane of this Result")
	}

	if !after.Over(p.content[After]) {
		panic("diff: PaneHunksOf: after is not a view of the after pane of this Result")
	}

	rows := func(row int) [2]int { return p.rows[row].pos }
	marked := func(row int) bool {
		return p.rows[row].changed || hunkMarked(before, row) || hunkMarked(after, row)
	}

	spans, parts := hunkSpans(r.consecutiveParts(len(p.rows), rows), contextLines, marked, before, after)
	headers := r.hunkHeaders(spans, parts, rows)

	return hunksOf(r.pane(Before), before, spans, headers), hunksOf(r.pane(After), after, spans, headers)
}

// hunkSpans returns the spans of the hunks within parts, and the part that
// holds each span. A hunk forms around each row that marked reports, with
// context rows around it. Each marked row takes its context rows from its
// own run of rows that every view holds, in its own part. No hunk thus
// reaches across a row that a view does not hold or a gap in either side's
// numbering.
func hunkSpans(
	parts []consecutivePart,
	contextLines int,
	marked func(row int) bool,
	views ...*line.View,
) (position.Spans, []consecutivePart) {
	var (
		spans     position.Spans
		spanParts []consecutivePart // The part that holds each span.
	)

	for _, part := range parts {
		for _, run := range heldRuns(part.rows, views...) {
			var rows []int

			for i := run.Start; i < run.End; i++ {
				if marked(i) {
					rows = append(rows, i)
				}
			}

			for _, span := range position.ContextSpans(rows, contextLines, run.End).Clamp(run.Start, run.End) {
				spans = append(spans, span)
				spanParts = append(spanParts, part)
			}
		}
	}

	return spans, spanParts
}

// hunkHeaders returns the header of each hunk that spans holds. The parts
// argument gives the part that holds each span, and rows gives the
// position on each side of the line that a row shows, or -1, as lineOp
// and alignedRow hold them.
func (r *Result) hunkHeaders(spans position.Spans, parts []consecutivePart, rows func(row int) [2]int) []string {
	headers := make([]string, len(spans))

	// The spans come in order, so one pass over the rows counts the lines
	// each side holds before each hunk and within it.
	var idx [2]int

	row := 0

	for i, span := range spans {
		for ; row < span.Start; row++ {
			idx = addHeld(idx, rows(row))
		}

		var count [2]int

		for ; row < span.End; row++ {
			count = addHeld(count, rows(row))
		}

		headers[i] = r.formatHunkHeader(parts[i], idx[Before], count[Before], idx[After], count[After])
		idx[Before] += count[Before]
		idx[After] += count[After]
	}

	return headers
}

// addHeld adds to each count of n the line that pos holds on that side,
// if any.
func addHeld(n, pos [2]int) [2]int {
	for s, p := range pos {
		if p >= 0 {
			n[s]++
		}
	}

	return n
}

// hunksOf returns the slice of base by spans with each header above the
// first row of its span. Each row then takes the decoration of view, so
// the header stays above the marks, as Carry keeps it. The headers and the
// separators that a view of hunks carries above its rows need not match
// the new hunks, so they stay out. The result keeps the clip of view.
func hunksOf(base, view *line.View, spans position.Spans, headers []string) *line.View {
	hunks := base.Slice(spans...).Clip(view.ClipCols())

	for i, span := range spans {
		hunks.AnnotateLine(span.Start, line.Annotation{
			Content:   headers[i],
			Kind:      kind.UIHunkHeader,
			Placement: line.Above,
		})
	}

	for i := range hunks.All() {
		hunks.SetFlag(i, view.Flag(i))

		if o := view.Overlays(i); len(o) > 0 {
			hunks.AddLineOverlay(i, o...)
		}

		for _, a := range view.Annotations(i) {
			if a.Marks() || a.Placement != line.Above {
				hunks.AnnotateLine(i, a)
			}
		}
	}

	return hunks
}

// heldRuns returns the runs of consecutive rows within rows that every
// view holds, in order.
func heldRuns(rows position.Span, views ...*line.View) []position.Span {
	var (
		runs []position.Span
		run  = position.NewSpan(rows.Start, rows.Start)
	)

	for i := rows.Start; i < rows.End; i++ {
		if !slices.ContainsFunc(views, func(v *line.View) bool { return !v.Contains(i) }) {
			run.End = i + 1

			continue
		}

		if run.End > run.Start {
			runs = append(runs, run)
		}

		run = position.NewSpan(i+1, i+1)
	}

	if run.End > run.Start {
		runs = append(runs, run)
	}

	return runs
}

// hunkMarked reports whether row i of view carries a flag, an overlay, or
// an annotation that [line.Annotation.Marks].
func hunkMarked(view *line.View, i int) bool {
	return view.Flag(i) != line.FlagDefault || len(view.Overlays(i)) > 0 ||
		slices.ContainsFunc(view.Annotations(i), line.Annotation.Marks)
}

// consecutivePart is a run of rows within which each side's lines carry
// consecutive [line.Line.Number] values. The rows field spans the rows of
// the run, in the unified view or in the panes. The before and after
// fields span the positions in each side of the lines the run holds.
type consecutivePart struct {
	rows, before, after position.Span
}

// consecutiveParts splits n rows into parts within which each side's lines
// carry consecutive [line.Line.Number] values. The rows argument gives the
// position on each side of the line that a row shows, or -1. The positions
// of each side ascend from row to row, as they do in the unified view and
// in the panes. The header of a hunk gives its first number and its count
// on each side. A hunk within one part thus has a header that names the
// lines that it holds. A part ends before the row whose line on either
// side does not follow that side's last line in the part. A line that
// does not have a number follows any line, because [hunkStart] counts it
// by its position instead. When each side holds a run of consecutive
// lines, such as a whole file or one document, the result is one part over
// every row.
func (r *Result) consecutiveParts(n int, rows func(row int) [2]int) []consecutivePart {
	var (
		parts                 []consecutivePart
		part                  consecutivePart // The current part, whose ends give the next row's positions.
		lastBefore, lastAfter int             // The number of each side's last line in the part, or 0.
	)

	for row := range n {
		pos := rows(row)
		nextBefore := sideNumber(r.inputs[Before].lines, pos[Before])
		nextAfter := sideNumber(r.inputs[After].lines, pos[After])

		if skipsLines(lastBefore, nextBefore) || skipsLines(lastAfter, nextAfter) {
			parts = append(parts, part)
			part = consecutivePart{
				rows:   position.NewSpan(part.rows.End, part.rows.End),
				before: position.NewSpan(part.before.End, part.before.End),
				after:  position.NewSpan(part.after.End, part.after.End),
			}
			lastBefore, lastAfter = 0, 0
		}

		if pos[Before] >= 0 {
			lastBefore = nextBefore
			part.before.End++
		}

		if pos[After] >= 0 {
			lastAfter = nextAfter
			part.after.End++
		}

		part.rows.End++
	}

	return append(parts, part)
}

// sideNumber returns the [line.Line.Number] of the line at position p in
// side. It returns 0 when p is negative, for a row that does not hold a
// line of side.
func sideNumber(side line.Lines, p int) int {
	if p < 0 {
		return 0
	}

	return side.Line(p).Number()
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

// Change is one run of changed lines in a diff. The run replaces the lines
// of [Before] that it deletes with the lines of [After] that it inserts.
// A caller can thus turn each Change into one text edit or one annotation
// range.
//
// Each [position.Span] is half-open. Before and After hold indices of
// lines in the content of each input, in the coordinates of an [Origin].
// A Change that only inserts lines has an empty Before span at the place
// in [Before] where the lines go. A Before span of [6, 6) thus places them
// in front of the line of [Before] at index 6. A Change that only deletes
// lines has an empty After span at the place in [After] where the lines
// stood.
//
// Receive instances from [Result.Changes].
type Change struct {
	// Rows spans the rows of [Result.Unified] that show the run.
	Rows position.Span
	// Before spans the lines of [Before] that the run deletes.
	Before position.Span
	// After spans the lines of [After] that the run inserts.
	After position.Span
}

// Changes returns the changes of the diff in order. Each [Change] is one
// run of deleted and inserted rows of [Result.Unified] that unchanged rows
// do not interrupt:
//
//	for _, c := range result.Changes() {
//		fmt.Println(c.Rows, c.Before, c.After)
//	}
//
// Changes returns nil when the diff does not change any line, and
// [Stats.Changed] reports false for the same diff.
//
// The Before and After spans count lines, and the Rows span counts rows of
// the unified view. [Result.PaneRow] finds the row of a side-by-side pane
// that shows a line of the Before or After span.
//
// An input can skip lines of its content, as a view sliced to several
// spans does. A run that crosses such a gap on either side splits at the
// gap into several changes, so each span names exactly the lines that its
// Change deletes or inserts.
//
// An empty span sits after the last line of its side before the run. A
// gap can separate the run from that line and not from the next line of
// the same side. The span then sits at that next line instead. On a side
// that does not hold a line before the run, the span sits at the first
// line of the side, or at 0 when the side is empty.
//
// Each call returns a new slice.
func (r *Result) Changes() []Change {
	var (
		changes []Change
		rows    position.Span    // The rows of the open run, or an empty span when no run is open.
		spans   [2]position.Span // The lines of each side that the open run holds, by [Side].
		next    [2]lineMark      // The index that continues the current stretch of each side.
		past    [2]lineMark      // The index after the last line of each side that an earlier op holds.
	)

	// The following argument gives the lines of the unchanged op that ends
	// the run, when no gap separates that op from the run.
	closeRun := func(following [2]lineMark) {
		if rows.Len() == 0 {
			return
		}

		for s := range spans {
			if spans[s].Len() == 0 {
				at := r.changePoint(Side(s), next[s], following[s], past[s])
				spans[s] = position.NewSpan(at, at)
			}
		}

		changes = append(changes, Change{Rows: rows, Before: spans[Before], After: spans[After]})
		rows, spans = position.Span{}, [2]position.Span{}
	}

	for row, op := range r.ops {
		var (
			at  [2]lineMark // The line the op holds on each side.
			gap bool
		)

		for s, p := range op.pos {
			if p < 0 {
				continue
			}

			at[s] = lineMark{index: r.inputs[s].indices[p], ok: true}
			gap = gap || next[s].ok && at[s].index != next[s].index
		}

		// A stretch ends before the op whose line on either side does not
		// follow the last line of that side in the stretch.
		if gap {
			closeRun([2]lineMark{})

			next = [2]lineMark{}
		}

		if op.kind == lcs.OpEqual {
			closeRun(at)
		} else {
			if rows.Len() == 0 {
				rows.Start = row
			}

			rows.End = row + 1
		}

		for s, m := range at {
			if !m.ok {
				continue
			}

			if op.kind != lcs.OpEqual {
				if spans[s].Len() == 0 {
					spans[s].Start = m.index
				}

				spans[s].End = m.index + 1
			}

			next[s] = lineMark{index: m.index + 1, ok: true}
			past[s] = next[s]
		}
	}

	closeRun([2]lineMark{})

	return changes
}

// lineMark is an index of a line in the content of an input. The ok field
// reports whether the mark names an index.
type lineMark struct {
	index int
	ok    bool
}

// changePoint returns the index where a [Change] sits on side s when its
// run does not hold a line of s. The next argument marks the index after
// the last line of s before the run, when no gap separates that line from
// the run. The following argument marks the line of s after the run, under
// the same condition. The past argument marks the index after the last
// line of s that any earlier op holds.
func (r *Result) changePoint(s Side, next, following, past lineMark) int {
	switch indices := r.inputs[s].indices; {
	case next.ok:
		return next.index
	case following.ok:
		return following.index
	case past.ok:
		return past.index
	case len(indices) > 0:
		return indices[0]
	default:
		return 0
	}
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
			rowOf := make([]int, in.lines.Len())

			for row, ar := range rows {
				p := ar.pos[s]
				if p < 0 {
					lines[row] = &line.Line{}

					continue
				}

				lines[row] = in.lines.Line(p)
				rowOf[p] = row
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
// The hunks of this pane alone do not pair with the After pane, so
// [Result.PaneHunks] returns the hunks of both panes.
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
// The hunks of this pane alone do not pair with the Before pane, so
// [Result.PaneHunks] returns the hunks of both panes.
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
// placeholder does not show a line of an input. [Result.PaneHunks] keeps
// the rows of the panes, so a row of the pane hunks has the same Origin.
func (r *Result) PaneOrigin(s Side, row int) (Origin, bool) {
	return r.origin(r.paneShows(s, row))
}

// UnifiedRow returns the row of [Result.Unified] that shows the line o
// names, and true. The unified view shows each unchanged line once, as the
// line of [After]. A line of [Before] that the diff left unchanged maps to
// the row of that after line. UnifiedRow reports false when o does not
// name a line of an input.
//
// [Result.Hunks] keeps the rows of the unified view and holds a row when a
// hunk covers it, as [line.View.Contains] reports.
func (r *Result) UnifiedRow(o Origin) (int, bool) {
	if o.Side > After {
		return 0, false
	}

	in := r.inputs[o.Side]

	p, ok := in.position(o.Index)
	if !ok {
		return 0, false
	}

	return in.unifiedRows[p], true
}

// PaneRow returns the row of the pane of o.Side that shows the line o
// names, and true. [Result.Before] shows every line of [Before], and
// [Result.After] shows every line of [After]. The panes align, so the same
// row of the other pane shows the paired line or an empty placeholder.
// PaneRow reports false when o does not name a line of an input.
//
// [Result.PaneHunks] keeps the rows of the panes and holds a row when a
// hunk covers it, as [line.View.Contains] reports.
func (r *Result) PaneRow(o Origin) (int, bool) {
	if o.Side > After {
		return 0, false
	}

	in := r.inputs[o.Side]

	p, ok := in.position(o.Index)
	if !ok {
		return 0, false
	}

	return r.getPanes().rowOf[o.Side][p], true
}

// Carry copies the overlays and annotations of the input views onto dst,
// a view that [Result.Unified], [Result.Hunks], [Result.Before],
// [Result.After], or [Result.PaneHunks] returned, or a slice, clone, or
// clip of one. Each row that dst holds takes the marks of the line its
// [Origin] names. A line of [Before] takes them from before, and a line of
// [After] from after. The marks follow the annotations each row carries
// already, so a hunk header stays above them.
//
//	result, err := diff.Diff(ctx, before, after)
//	if err != nil {
//		return err
//	}
//
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
				dst.AnnotateLine(row, a)
			}
		}
	}
}

// defaultDiffer is the shared [Differ] behind [Diff]. Its [lcs.Hirschberg]
// is safe for concurrent use and pools its working buffers across calls.
var defaultDiffer = New()

// Diff computes the difference between two revisions using the default
// line key and algorithm. See [Differ.Diff]. It returns an error only
// when ctx ends, and the error matches [context.Canceled] or
// [context.DeadlineExceeded].
//
// Every call goes through one shared [Differ], which is safe for concurrent
// use, so repeated calls reuse its pooled working buffers.
func Diff(ctx context.Context, a, b line.Sequence) (*Result, error) {
	return defaultDiffer.Diff(ctx, a, b)
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
// 1-indexed position in side instead.
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

	if n := side.Line(pos).Number(); n > 0 {
		return n + offset
	}

	return idx + 1
}
