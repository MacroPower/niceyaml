// Package diff computes line differences between two [line.Sequence]
// values and renders them as [line.View] values.
//
// A [Differ] compares two revisions, such as the lines of two niceyaml
// Source values, with an [lcs.Algorithm]. The default is [lcs.Hirschberg].
// Create one with [New], or call [Diff] for the default Differ:
//
//	result := diff.Diff(before.Lines(), after.Lines())
//	p := printer.New()
//	lipgloss.Println(p.Print(result.Unified()))
//	lipgloss.Println(p.Print(result.Hunks(3)))
//
// Each revision is the [line.Lines] of a whole file, or a [*line.View] for
// the lines it holds. When a file holds several documents, the views of one
// document in two revisions of the file diff that document alone. The
// gutter and hunk headers then show the line numbers of the file:
//
//	result := diff.Diff(before[1].View(), after[1].View())
//
// A [Result] renders the diff in several layouts:
//
//   - [Result.Unified] interleaves the lines of both inputs.
//   - [Result.Hunks] keeps only the changes, with context lines around each
//     and a hunk header above it.
//   - [Result.Before] and [Result.After] return aligned views for
//     side-by-side rendering, which
//     [go.jacobcolvin.com/niceyaml/printer.Printer.PrintSideBySide] prints.
//   - [Result.PaneHunks] keeps only the changes of both panes, with the
//     same context rows and hunk headers in each, so the panes stay
//     aligned.
//
// Each call returns a fresh [line.View], so overlays that a caller adds to
// one rendering do not affect another. The hunks that [Result.Hunks]
// returns are a slice of the unified view and keep its line indices. The
// pane hunks are slices of [Result.Before] and [Result.After] and keep the
// pane row indices. In each case, one index marks the same line in the
// hunks and in the full view.
//
// A program that writes a diff to a log or a golden file renders the view
// with [line.View.String]. The plain text holds a "-" beside each deleted
// line and a "+" beside each inserted line:
//
//	fmt.Println(result.Hunks(1).String())
//	//      | @@ -1,4 +1,5 @@
//	//    1 | name: web
//	//    2 - replicas: 3
//	//    2 + replicas: 5
//	//    3 | ports:
//	//    4 |   - 80
//	//    5 +   - 443
//
// Each rendering is new content, and its indices are its rows. A position
// in an input does not apply to a rendering, so the Result maps between
// the lines of each input and the rows. An [Origin] names a line of an
// input by its [Side] and its index in the content of that input.
//
// [Result.UnifiedOrigin] and [Result.PaneOrigin] name the line a row
// shows. Viewers use them to go from a click on a row to a place in a
// revision. [Result.UnifiedRow] and [Result.PaneRow] find the row that
// shows a line. Viewers use them to scroll to an error in either revision:
//
//	row, ok := result.UnifiedRow(diff.Origin{Side: diff.Before, Index: i})
//
// A caller that reports or applies the changes instead of printing them
// reads [Result.Changes]. Each [Change] spans the lines that one run of
// changes deletes from the before revision and the lines that it inserts
// into the after revision, in the coordinates of an Origin. A CI check
// turns each Change into one annotation, and an editor turns it into one
// text edit. [Result.Stats] counts the added and removed lines, and
// [Stats.Changed] reports whether the revisions differ:
//
//	if !result.Stats().Changed() {
//		return
//	}
//
//	for _, c := range result.Changes() {
//		fmt.Println(c.Before, c.After)
//	}
//
// A diff ignores the decoration of its inputs. [Result.Carry] copies the
// overlays and annotations of the input views onto a rendering, so the
// marks of a revision, such as its errors, show in the diff. An unchanged
// row of the unified view takes the marks of the after revision:
//
//	view := result.Unified()
//	result.Carry(view, beforeView, afterView)
//
// [Result.Hunks] picks its rows from the changes alone. [Result.HunksOf]
// forms a hunk around each mark of a view as well, so an error on an
// unchanged line shows in the hunks:
//
//	hunks := result.HunksOf(view, 3)
//
// The hunks of one side-by-side pane alone do not pair with the other
// pane, because a pure insertion or deletion marks the rows of one pane
// only. [Result.PaneHunksOf] forms each hunk from the marks of both panes
// and slices both by the same rows:
//
//	left, right := result.Before(), result.After()
//	result.Carry(left, beforeView, afterView)
//	result.Carry(right, beforeView, afterView)
//	left, right = result.PaneHunksOf(left, right, 3)
//	lipgloss.Println(p.PrintSideBySide(left, right))
//
// A diff compares lines by their content without line endings, so a change
// from LF to CRLF endings or a missing final newline is not a difference.
//
// [WithLineKey] changes which lines are equal. The diff compares the key
// of each line in place of its content. A key that leaves out padding,
// comments, or quotes thus makes the diff ignore them. This Differ ignores
// the whitespace at the end of each line:
//
//	differ := diff.New(diff.WithLineKey(func(l *line.Line) string {
//		return strings.TrimRight(l.Content(), " \t")
//	}))
//	result := differ.Diff(before.Lines(), after.Lines())
//
// The key decides only which lines the diff pairs, and every rendering
// shows each line as its revision wrote it. An unchanged row of the unified
// view shows the line of the after revision. [WithAlgorithm] sets the
// algorithm that aligns the lines, and the algorithm receives the keys.
//
// Diff output is a [line.View] rather than a YAML document, because the
// interleaved lines of two revisions do not parse as one. Each line carries a
// [line.Flag] that marks it as inserted, deleted, or unchanged. Each hunk
// header is a [line.Annotation] that [Result.Hunks] places [line.Above] the
// first line of its hunk. A header does not mark its line, as
// [line.Annotation.Marks] reports. [line.View.Hunks] cannot recompute a
// header, so it drops the headers of a view. [Result.HunksOf] narrows a view
// of hunks with a new header above each, and [Result.PaneHunksOf] does the
// same for the panes.
//
// A Differ is safe for concurrent use when its [lcs.Algorithm] and its line
// key are, as [lcs.Hirschberg] and the default key are. A Result is safe
// for concurrent use.
package diff
