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
// surface, so the text shows the terminal's own background. It takes
// whether that background is dark and returns a palette that reads on it.
// A Printer does not read the terminal, so without [WithStyles] it renders
// with the dark palette. A program that prints on a light terminal too
// detects the background and passes the palette for it:
//
//	isDark := lipgloss.HasDarkBackground(os.Stdin, os.Stdout)
//	p := printer.New(printer.WithStyles(style.Default(isDark)))
//
// A theme that sets a surface, such as
// [go.jacobcolvin.com/niceyaml/style/theme.Charm], paints it behind each
// token. For a full-screen viewer, [WithBox] fills the rest of the
// rectangle around the rows with the surface:
//
//	p := printer.New(printer.WithStyles(theme.Charm), printer.WithBox(true))
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
// flag, sets the Profile field of the
// [github.com/charmbracelet/colorprofile.Writer] it prints through.
// [lipgloss.Print], [lipgloss.Println], and [lipgloss.Printf] print through
// [lipgloss.Writer], which detects the profile of stdout when the program
// starts. A program that prints with them sets the field there:
//
//	if forceColor {
//		lipgloss.Writer.Profile = colorprofile.TrueColor
//	}
//	lipgloss.Println(p.Print(view))
//
// [lipgloss.Fprintln] and the other lipgloss functions that take a writer
// build a writer of their own on every call. The field of lipgloss.Writer
// thus does not reach them. A program that prints elsewhere, such as to
// stderr, builds a writer for that destination with
// [github.com/charmbracelet/colorprofile.NewWriter] and sets its field:
//
//	w := colorprofile.NewWriter(os.Stderr, os.Environ())
//	if forceColor {
//		w.Profile = colorprofile.TrueColor
//	}
//	fmt.Fprintln(w, p.PrintError(err))
//
// NewWriter detects the profile when it builds a writer. Inside tmux,
// each detection starts a process, so a program that prints many views
// builds one writer and prints them all through it.
//
// Under the 16-color and 256-color profiles, a theme's surface and the
// text or highlights on it can become the same color. The Surfaces section
// of [go.jacobcolvin.com/niceyaml/style/theme] covers it.
//
// Output without its sequences is not the plain text of a view.
// [WithWrapWidth] wraps a long line onto several rows. [WithBox] and
// [WithContainerWidth] pad rows with spaces. So does a container style
// that sets any property the printer keeps, such as a frame, a color, or
// bold text.
//
// [line.View.String] renders a view as plain text, without padding or
// wrapping. It shows the flags and the annotations of the view. Each
// inserted or deleted line of a diff carries the [line.Flag.Marker] that
// the gutter of the printer draws. A program that writes to a log or a
// file should render through [line.View.String], or through
// [niceyaml.FormatError], which renders an error the same way.
//
// Below each line, View.String draws the same caret rows that
// [DefaultAnnotation] draws. Both draw carets for annotations and not for
// overlays. An overlay without an annotation, such as a search match, thus
// shows only through color.
//
// A program that wants the gutter, the wrapping, or the side-by-side panes
// of the printer in plain text builds a printer with the zero
// [style.Styles]. Its output does not hold an escape sequence, and it
// keeps the diff markers:
//
//	plain := printer.New(printer.WithStyles(style.Styles{}))
//	fmt.Fprintln(logFile, plain.Print(result.Hunks(3)))
//
// # Gutters
//
// A [Gutter] renders the left edge of each row from a [GutterContext] and
// declares its width. The printer pads or cuts the gutter of every row to
// that width, so content starts in the same column on every row. [New]
// starts with a gutter that shows line numbers and diff markers, and
// [WithGutter] sets another.
//
// [FixedGutter] builds a gutter from a width and a function, such as a
// marker column for a linter or a review that renders text on some rows
// and nothing on others. [GutterFunc] measures its width from what the
// function renders for the largest line number, so it fits a custom line
// number format.
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
// the wrapped row that holds its column. An annotation of kind
// [kind.UISeparator] or [kind.UIHunkHeader] labels its line and not a
// column of it. Below a line, [DefaultAnnotation] draws it as a row of
// text without carets, after the caret rows of the line.
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
// [Printer.Render] returns the rows of a view with their layout, both
// from one pass over the view. The rows leave out the container, so row n
// of the rows is row n of the layout whatever frame the container draws.
// A viewer draws a window of those rows. [Printer.Print] joins the same
// rows inside the container as one string.
//
// A viewer that scrolls horizontally shows a window of each row.
// [Layout.CellOf] gives the cell of a column, and [Cut] cuts a row to a
// range of those cells with its styles intact. [Printer.Pad] pads a short
// row to the window:
//
//	r := p.Render(view)
//	layout := r.Layout()
//	row := r.Rows()[layout.RowOf(pos)]
//	window := p.Pad(printer.Cut(row, offset, offset+width), width)
//
// [Printer.RenderSideBySide] renders two views as panes beside each
// other. Its [*SideBySide] holds the zipped rows, the rows of each pane,
// and the layout of each pane. It maps a line to its zipped rows and a
// zipped row to the row of a pane. A viewer thus scrolls the panes and
// maps a pointer to them without zipping the panes itself.
package printer
