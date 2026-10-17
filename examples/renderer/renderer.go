package main

import (
	"fmt"
	"html"
	"slices"
	"strings"

	_ "embed"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/diff"
	"go.jacobcolvin.com/niceyaml/escape"
	"go.jacobcolvin.com/niceyaml/finder"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

//go:embed demo.yaml
var example string

func main() {
	source := niceyaml.NewSourceFromString(example)

	// Decorate a view as a program does for the printer. The search
	// highlight blends with the token colors, and the error mark replaces
	// them.
	view := source.View()
	view.BlendOverlay(kind.GenericHighlight, finder.New().Load(source.Lines()).Find("kaffe")...)

	// Lines and columns count from 0, so this range covers "45 kr" on the
	// line the gutter numbers 9.
	view.AddOverlay(kind.GenericError, position.NewRange(position.New(8, 11), position.New(8, 16)))
	view.Annotate(8, line.Annotation{
		Content:   "the menu board says 40 kr",
		Kind:      kind.TextError,
		Placement: line.Below,
		Cols:      position.NewSpan(11, 16),
	})

	fmt.Println("\nPrint the view as plain text:")

	// String renders the decoration without styles, as a log or a golden
	// file wants it. An overlay does not draw a caret, so the highlight does
	// not show.
	fmt.Println(view.String())

	fmt.Println("\nRender the same view as HTML:")
	fmt.Println(renderHTML(view))

	fmt.Println("\nRender a diff with the same function:")

	// A diff returns a view too. Its lines carry flags, and each hunk
	// opens with a header annotation above its first line. This diff fixes
	// the price.
	after := niceyaml.NewSourceFromString(strings.Replace(example, "45 kr", "40 kr", 1))
	result := diff.Diff(source.Lines(), after.Lines())
	fmt.Println(renderHTML(result.Hunks(1)))
}

// renderHTML renders a view as HTML from the decoration it carries: the
// flag, overlays, and annotations of each line it holds.
func renderHTML(view *line.View) string {
	var sb strings.Builder

	// A pre element keeps every newline it holds, and it accepts only
	// inline elements such as span. Each row is thus a span followed by a
	// newline, so a browser shows one row per line.
	sb.WriteString("<pre>\n")

	// All yields the lines the view holds with their indices in its
	// content, and every decoration method takes that index.
	for i, ln := range view.All() {
		// A diff flags the lines it inserts and deletes, and every other
		// line has the default flag. The diff marker follows the number,
		// as in the printer.
		var class, marker string

		switch view.Flag(i) {
		case line.FlagInserted:
			class, marker = "line inserted", "+"
		case line.FlagDeleted:
			class, marker = "line deleted", "-"
		case line.FlagDefault:
			class, marker = "line", " "
		}

		gutter := fmt.Sprintf("%4d %s", ln.Number(), marker)
		notes := view.Annotations(i)

		writeNotes(&sb, len(gutter), ln.Content(), notes, line.Above)

		fmt.Fprintf(&sb, "<span class=%q>%s", class, gutter)

		// Segments cuts the line into segments, each with one kind and one
		// set of overlays, so the renderer never reads a token. A segment
		// ends where an overlay starts or ends, so the search match cuts
		// the quoted item into three segments.
		//
		// The printer draws every segment of a changed line in the diff
		// kind of its flag, with the overlays over that. The class of the
		// line carries that color, so the segments of a changed line leave
		// out the spans of their kinds.
		tokens := view.Flag(i) == line.FlagDefault

		for seg := range view.Segments(i) {
			sb.WriteString(renderSegment(seg, tokens))
		}

		sb.WriteString("</span>\n")

		writeNotes(&sb, len(gutter), ln.Content(), notes, line.Below)
	}

	sb.WriteString("</pre>")

	return sb.String()
}

// writeNotes writes the rows of the annotations on one side of a line.
// The class of each row is the kind its annotations render in, and the row
// starts past the gutter.
func writeNotes(sb *strings.Builder, gutter int, content string, notes line.Annotations, placement line.Placement) {
	// DefaultAnnotation lays out the rows that the printer writes for the
	// annotations of one kind, and it returns them as text without styles.
	// A renderer of its own can call it too, so its carets and padding
	// match those of the printer. Groups returns the groups in the order
	// the printer draws them, so the errors below a line come first.
	for _, group := range notes.Groups(placement) {
		ctx := printer.AnnotationContext{Content: content, Annotations: group, Placement: placement}

		for _, row := range printer.DefaultAnnotation(ctx) {
			// A column counts runes, and ColWidth counts the cells before
			// it. A wide rune before the column thus adds two spaces.
			pad := strings.Repeat(" ", gutter+ctx.ColWidth(row.Col))
			text := html.EscapeString(row.Marker + row.Text)
			fmt.Fprintf(sb, "<span class=%q>%s%s</span>\n", group[0].RenderKind(), pad, text)
		}
	}
}

// renderSegment renders one segment as a span inside a span for each
// overlay that covers it. With tokens set, a span with the class of its
// kind wraps them all.
func renderSegment(seg line.Segment, tokens bool) string {
	// The text holds the source as written, control characters included.
	// Control draws each one as one visible rune, as the printer does, so
	// the carets of DefaultAnnotation stay under their runes. Then escape
	// it for HTML.
	out := html.EscapeString(escape.Control(seg.Text))

	// The overlays nest as the doc of View.Segments shows, with the last
	// one innermost, so the overlay that the printer applies last wins in
	// CSS too. The renderer writes each overlay as a span, because
	// browsers give every mark element a color and a background of their
	// own. A renderer that writes inline colors instead, blends included,
	// reads the style of each segment from Printer.Segments.
	for _, ov := range slices.Backward(seg.Overlays) {
		class := string(ov.Kind)
		if ov.Blend {
			class += " blend"
		}

		out = fmt.Sprintf("<span class=%q>%s</span>", class, out)
	}

	// The spaces around a token are kind.Text, the root that every other
	// kind inherits from, so they do not need a span.
	if tokens && seg.Kind != kind.Text {
		out = fmt.Sprintf("<span class=%q>%s</span>", seg.Kind, out)
	}

	return out
}
