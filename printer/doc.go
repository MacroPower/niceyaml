// Package printer renders a [line.View] as styled terminal output.
//
// A [Printer] takes a [line.View], such as the view of a niceyaml Source,
// and renders it with syntax highlighting through [lipgloss.Style] values
// from a [style.Styler]. Create one with [New] and render with
// [Printer.Print]:
//
//	p := printer.New()
//	lipgloss.Println(p.Print(source.View()))
//
// Every setting is an [Option]. A Printer never changes after construction,
// so [Printer.With] derives a copy with more options applied.
//
// # Backgrounds
//
// By default the printer does not add anything around the rows it renders,
// so each row ends where its content ends. [style.Default] does not set a
// surface, so the text shows the terminal's own background. A theme that
// sets a surface, such as [go.jacobcolvin.com/niceyaml/style/theme.Charm],
// paints it behind each token. For a full-screen viewer, [WithBox] fills
// the rest of the rectangle around the rows with the surface:
//
//	p := printer.New(printer.WithStyles(theme.Charm), printer.WithBox())
//
// To draw such a theme on the terminal's own background instead, remove
// its surface with [style.WithSurface]:
//
//	styles := theme.Charm.Styles().With(style.WithSurface(nil))
//	p := printer.New(printer.WithStyles(styles))
//
// # Color Profiles
//
// A Printer writes every color as a 24-bit escape sequence, whatever the
// output supports. [Printer.Print] and [Printer.PrintError] return those
// sequences unchanged, so [fmt.Println] sends them to a pipe, to a file,
// and to a terminal that has NO_COLOR set. The lipgloss print functions
// fit the sequences to their destination instead. They convert each color
// to one the terminal supports, drop the colors under NO_COLOR, and drop
// every sequence when the output is not a terminal:
//
//	lipgloss.Println(p.Print(source.View()))
//	lipgloss.Fprintln(os.Stderr, p.PrintError(err))
//
// A Bubble Tea program fits what its model draws to the terminal it runs
// in, so a model draws what Print returns as it is.
//
// A program that chooses the profile itself, such as one with a --color
// flag, prints through a [github.com/charmbracelet/colorprofile.Writer].
// [github.com/charmbracelet/colorprofile.NewWriter] detects the profile
// of its destination as the lipgloss functions do, and the Profile field
// of the writer overrides it:
//
//	w := colorprofile.NewWriter(os.Stdout, os.Environ())
//	if forceColor {
//		w.Profile = colorprofile.TrueColor
//	}
//	fmt.Fprintln(w, p.Print(view))
//
// NewWriter and [lipgloss.Fprintln] detect the profile on every call.
// Inside tmux, each detection starts a process, so a program that prints
// many views builds one writer and prints them all through it.
//
// Output without its sequences is not the plain text of a view.
// [WithWrapWidth] wraps a long line onto several rows. [WithBox],
// [WithContainerWidth], and a container style that sets any property pad
// rows with spaces.
//
// [line.View.String] renders a view as plain text, without padding or
// wrapping. Below each line, it draws the same caret rows that
// [DefaultAnnotation] draws. Both draw carets for annotations and not for
// overlays. An overlay without an annotation, such as a search match, thus
// shows only through color. A program that writes to a log or a file
// should render through [line.View.String], or through
// [niceyaml.FormatError], which renders an error the same way.
//
// # Gutters
//
// A [Gutter] renders the left edge of each row from a [GutterContext] and
// declares its width. The printer pads or cuts the gutter of every row to
// that width, so content starts in the same column on every row. [New]
// starts with a gutter that shows line numbers and diff markers, and
// [WithGutter] sets another.
//
// # Overlays
//
// The printer renders the [line.Overlays] a view carries. An overlay
// styles a column span. [line.View.AddOverlay] adds an overlay that
// replaces the style underneath, and [line.View.BlendOverlay] adds one that
// blends with it. Error positions replace the style and search highlights
// blend with it, so a match keeps the token or diff color it covers.
//
// The printer draws each grapheme cluster, such as an emoji ZWJ sequence,
// whole in the style of its first rune. An overlay that covers part of a
// cluster therefore styles the whole cluster when it covers the first
// rune. Otherwise it does not style the cluster.
//
// # Annotations
//
// The printer renders the [line.Annotations] a view carries. The
// annotations above or below a line render as rows in the style of their
// [line.Annotation.Kind], or [kind.UIAnnotation] for those of the zero
// Kind. An [AnnotationFunc] renders the annotations of each kind as
// [AnnotationRow]s. Each row holds its text and the column it starts
// under, and the printer pads, escapes, wraps, and styles it. [New] starts
// with [DefaultAnnotation], which gives each run of columns that the
// annotations below a line label a row of its own. [WithAnnotation] sets
// another func.
//
// Below a line, the rows of errors come first, then those of warnings,
// then the other kinds. When the line wraps, each row sits above or below
// the wrapped row that holds its column.
//
// # Word Wrapping
//
// [WithWrapWidth] fits the output to a width. The content of a line wraps
// at the columns that the gutter and the frame of the container leave.
//
// [Printer.Layout] reports the row structure of a view without rendering
// it. The structure covers how many rows each line takes, which row a
// position lands on, and how wide the rows are. A viewer that scrolls by
// rendered row maps rows to lines and back with the layout. A viewer that
// scrolls horizontally learns from it how far the content reaches. A
// viewer that takes a pointer finds the position a cell of a row shows
// with [Layout.PositionAt].
//
// A viewer that scrolls horizontally shows a window of each printed row.
// [Layout.CellOf] gives the cell of a column, and [Cut] cuts a printed row
// to a range of those cells with its styles intact:
//
//	rows := strings.Split(p.Print(view), "\n")
//	window := printer.Cut(rows[0], offset, offset+width)
package printer
