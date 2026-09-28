// Package printer renders a [line.View] as styled terminal output.
//
// A [Printer] takes a [line.View], such as the view of a niceyaml Source,
// and renders it with syntax highlighting through [lipgloss.Style] values
// from a [style.Styler]. Create one with [New] and render with
// [Printer.Print] or [Printer.Fprint]:
//
//	p := printer.New(printer.WithStyles(theme.Charm))
//	fmt.Println(p.Print(source.View()))
//
// Every setting is an [Option]. A Printer never changes after construction,
// so [Printer.With] derives a copy with more options applied.
//
// # Gutters
//
// A [Gutter] renders the left edge of each row from a [GutterContext] and
// declares its width, which the printer pads or cuts every row to, so
// content starts in the same column on every row. [New] starts with a
// gutter that shows line numbers and diff markers, and [WithGutter] sets
// another.
//
// # Overlays
//
// The printer renders the [line.Overlays] a view carries. An overlay
// styles a column span. [line.View.AddOverlay] adds one that replaces the
// style underneath, and [line.View.BlendOverlay] adds one that mixes with
// it. Error positions use the first and search highlights the second, so a
// match keeps the token or diff color it covers.
//
// # Annotations
//
// The printer renders the [line.Annotations] a view carries. The
// annotations above or below a line render as rows in the style of their
// [line.Annotation.Kind], or [kind.UIAnnotation] for those with none. An
// [AnnotationFunc] renders the annotations of each such kind as
// [AnnotationRow]s, each the text and the column it starts under, and the
// printer pads, escapes, wraps, and styles each row. When the line wraps,
// each row sits above or below the wrapped row that holds its column.
// [New] starts with [DefaultAnnotation], and [WithAnnotation] sets
// another.
//
// # Word Wrapping
//
// [WithWrap] wraps content at a width, less the width of the gutter.
// [Printer.Layout] reports the row structure of a view without rendering
// it: how many rows each line takes, which row a position lands on, and
// how wide the rows are. A viewer that scrolls by rendered row maps rows
// to lines and back with it, and one that scrolls horizontally learns how
// far the content reaches.
package printer
