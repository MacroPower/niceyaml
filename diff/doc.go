// Package diff computes line differences between two [line.Lines] values
// and renders them as [line.View] values.
//
// A [Differ] compares two [line.Lines] values, such as the lines of two
// niceyaml Source values, with an [lcs.Algorithm]. The default is
// [lcs.Hirschberg]. Create one with [New], or call [Diff] for the default
// algorithm:
//
//	result := diff.Diff(before.Lines(), after.Lines())
//	p := printer.New()
//	fmt.Println(p.Print(result.Unified()))
//	fmt.Println(p.Print(result.Hunks(3)))
//
// The [Result] renders in three shapes. [Result.Unified] interleaves the
// lines of both inputs, [Result.Hunks] keeps only the changes with context
// lines around each and a hunk header above it, and [Result.Before] with
// [Result.After] return aligned views for side-by-side rendering. Each call
// returns a fresh [line.View], so overlays added to one rendering do not
// reach another.
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
