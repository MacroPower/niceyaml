// Package printer renders a [line.View] as styled terminal output.
//
// A [Printer] takes a [line.View], such as the view of a niceyaml Source,
// and renders it with syntax highlighting through [lipgloss.Style] values
// from a [style.Styler]. Create one with [New] and render with
// [Printer.Print] or [Printer.Fprint]:
//
//	p := printer.New(printer.WithStyles(theme.Charm))
//	lipgloss.Println(p.Print(source.View()))
//
// Every setting is an [Option]. A Printer never changes after construction,
// so [Printer.With] derives a copy with more options applied.
//
// # Color Profiles
//
// A Printer writes every color as a 24-bit escape sequence, whatever the
// output supports. [Printer.Print] and [Printer.PrintError] return those
// sequences, and [Printer.Fprint] writes them to its writer unchanged, so
// [fmt.Println] sends them to a pipe, to a file, and to a terminal that
// has NO_COLOR set. The lipgloss print functions fit the sequences to
// their destination instead. They convert each color to one the terminal
// supports, drop the colors under NO_COLOR, and drop every sequence when
// the output is not a terminal:
//
//	lipgloss.Println(p.Print(source.View()))
//	lipgloss.Fprintln(os.Stderr, p.PrintError(err))
//
// A Bubble Tea program fits what its model draws to the terminal it runs
// in, so a model draws what Print returns as it is.
//
// A program that chooses the profile itself, such as one with a --color
// flag, prints through a
// [github.com/charmbracelet/colorprofile.Writer].
// [github.com/charmbracelet/colorprofile.NewWriter] detects the profile
// of its destination as the lipgloss functions do, and the Profile field
// of the writer overrides it:
//
//	w := colorprofile.NewWriter(os.Stdout, os.Environ())
//	if forceColor {
//		w.Profile = colorprofile.TrueColor
//	}
//	p.Fprint(w, view)
//
// NewWriter and [lipgloss.Fprintln] detect the profile on every call,
// which inside tmux starts a process, so a program that prints many views
// builds one writer and prints them all through it.
//
// Output without its sequences is not the plain text of a view. An
// overlay marks its span with color alone, so a search match loses its
// mark along with the colors. [line.View.String] renders a view as plain
// text with a caret under every column an overlay covers, and
// [niceyaml.FormatError] renders an error the same way, so output for a
// log or a file goes through them.
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
// match keeps the token or diff color it covers. The printer draws each
// grapheme cluster, such as an emoji ZWJ sequence, whole in the style of
// its first rune. An overlay that covers part of a cluster therefore
// styles all of it when it covers that rune and none of it otherwise.
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
//
// A viewer that scrolls horizontally shows a window of each printed row.
// [Layout.CellOf] gives the cell of a column, and [Cut] cuts a printed row
// to a range of those cells with its styles intact:
//
//	rows := strings.Split(p.Print(view), "\n")
//	window := printer.Cut(rows[0], offset, offset+width)
package printer
