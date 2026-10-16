// Package diff computes line differences between two [line.Sequence]
// values and renders them as [line.View] values.
//
// A [Differ] compares two revisions, such as the lines of two niceyaml
// Source values, with an [lcs.Algorithm]. The default is [lcs.Hirschberg].
// Create one with [New], or call [Diff] for the default algorithm:
//
//	result := diff.Diff(before.Lines(), after.Lines())
//	p := printer.New()
//	lipgloss.Println(p.Print(result.Unified()))
//	lipgloss.Println(p.Print(result.Hunks(3)))
//
// Each revision is the [line.Lines] of a whole file, or a [*line.View] for
// the lines it holds. The views of one document in two revisions of a file
// that holds several diff that document alone, and the gutter and hunk
// headers show the line numbers of the file:
//
//	result := diff.Diff(before[1].View(), after[1].View())
//
// The [Result] renders in three shapes. [Result.Unified] interleaves the
// lines of both inputs, [Result.Hunks] keeps only the changes with context
// lines around each and a hunk header above it, and [Result.Before] with
// [Result.After] return aligned views for side-by-side rendering. Each call
// returns a fresh [line.View], so overlays added to one rendering do not
// affect another. The hunks are a slice of the unified view and keep its
// line indices, so one index marks the same line in both.
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
// A diff ignores the decoration of its inputs. [Result.Carry] copies the
// overlays and annotations of the input views onto a rendering, so the
// marks of a revision, such as its errors, show in the diff. An unchanged
// row of the unified view takes the marks of the after revision:
//
//	view := result.Unified()
//	result.Carry(view, beforeView, afterView)
//
// Lines compare by their content with line endings stripped, so a change
// from LF to CRLF endings or a missing final newline is not a difference.
//
// Diff output is a [line.View] rather than a YAML document, since the
// interleaved lines of two revisions do not parse as one. Each line carries a
// [line.Flag] that marks it as inserted, deleted, or unchanged, and hunk
// headers are [line.Annotation] values placed [line.Above] the first line
// of each hunk.
//
// A Differ is safe for concurrent use when its [lcs.Algorithm] is, as
// [lcs.Hirschberg] is. A Result is safe for concurrent use.
package diff
