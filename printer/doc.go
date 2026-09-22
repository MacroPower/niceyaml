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
// so [Printer.With] derives a copy with more options applied while the
// original stays as it was.
//
// # Gutters
//
// A [GutterFunc] renders the left edge of each row from a [GutterContext].
// [DefaultGutter] shows the line number and a diff marker, [DiffGutter] the
// marker only, [LineNumberGutter] the number only, and [NoGutter] nothing.
// Pass one to [WithGutter].
//
// # Overlays and Annotations
//
// The printer renders the [line.Overlays] and [line.Annotations] a view
// carries. An overlay styles a column span. The annotations above or below
// a line render as rows in the style of their [line.Annotation.Kind], or
// [kind.UIAnnotation] for those with none, and an [AnnotationFunc] renders the
// text of each group of one Kind; [DefaultAnnotation] joins them with "; "
// and prefixes [line.Below] annotations with "^ ".
//
// # Word Wrapping
//
// [WithWrap] wraps content at a width, with the gutter width subtracted.
// [Printer.Layout] reports the row structure of a view without rendering
// it: how many rows each line takes, which row a position lands on, and
// how wide the rows are, so a viewer that scrolls by rendered row maps rows
// to lines and back, and one that scrolls horizontally knows how far the
// content reaches.
package printer
