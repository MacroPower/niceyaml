// Package diff computes line differences between two [line.View] values
// and renders them as [line.Lines].
//
// A [Differ] compares the lines of two views, such as two niceyaml Source
// values or two [line.Lines] collections, with an [lcs.Algorithm]. The
// default is [lcs.Hirschberg]. Create one with [New], or call [Diff] for
// the default algorithm:
//
//	result := diff.Diff(before, after)
//	p := printer.New()
//	fmt.Println(p.Print(result.Unified()))
//	fmt.Println(p.Print(result.Hunks(3)))
//
// The [Result] renders in three shapes. [Result.Unified] interleaves the
// lines of both views, [Result.Hunks] keeps only the changes with context
// lines around each and a hunk header above it, and [Result.Before] with
// [Result.After] return aligned views for side-by-side rendering. Each call
// returns a fresh [line.Lines] copy, so overlays added to one rendering do
// not reach another.
//
// Diff output is a [line.Lines] view rather than a YAML document, since the
// interleaved lines of two revisions do not parse as one. Each line carries a
// [line.Flag] that marks it as inserted, deleted, or unchanged, and hunk
// headers are [line.Annotation] values placed [line.Above] the first line
// of each hunk.
//
// A Differ reuses the buffers of its algorithm, so it is not safe for
// concurrent use. A Result is.
package diff
