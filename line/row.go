package line

import (
	"slices"

	"go.jacobcolvin.com/niceyaml/internal/clip"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

// Row is one line of a [View] as the printer and [View.String] show it.
// It holds the text of the row and the segments, overlays, and
// annotations of the line in the columns of that text. For a line the
// View shows whole, the row is the line itself. For a line that
// [View.Clip] clips, the row holds the windows [View.Windows] returns,
// with "..." in place of each run of columns the windows leave out. A
// renderer that writes HTML, or
// any other format, draws a row and so shows the excerpt the terminal
// shows.
//
// Every column of a Row counts runes of Content. [View.Segments],
// [View.Overlays], and [View.Annotations] return the decoration of the
// whole line instead, in the columns of the line.
//
// Receive instances from [View.Row].
type Row struct {
	// Content is the text of the row, without the line ending. It holds
	// the source as written, control characters included, as
	// [Segment.Text] does.
	Content string
	// Segments are the segments of the row, in column order, as
	// [View.Segments] yields those of the line. Each covers a run of
	// columns of Content, and the texts of them all concatenate to
	// Content. A clipped line keeps the part of each segment its windows
	// hold, and each "..." is a segment of its own in [kind.UISeparator]
	// without overlays. By default the printer draws every segment of a
	// flagged line in the kind of its flag, the "..." included. A printer
	// with [go.jacobcolvin.com/niceyaml/printer.WithFlagBlend] keeps the
	// kind of each segment and tints it with the kind of the flag.
	Segments []Segment
	// Overlays are the overlays of the line, in the order the View took
	// them in, each on the columns of Content that show its columns. A
	// clipped line keeps the part of each overlay its windows hold, as one
	// overlay per window. It drops an overlay that lies in the columns the
	// windows leave out.
	Overlays Overlays
	// Annotations are the annotations of the line, in the order the View
	// took them in, each labeling the run of Content that shows its
	// columns. On a clipped line, that run holds each "..." between the
	// windows that show the columns. An annotation that starts at a column
	// the row leaves out starts at the "..." that stands for the column.
	Annotations Annotations
	// Windows are the runs of columns of the line that the row shows, as
	// [View.Windows] returns them, or nil for a line the View shows whole.
	// [NewClipMap] maps a column of the line to a column of Content with
	// them, and back.
	Windows position.Spans
	// Width is the number of columns of Content.
	Width int
}

// Row returns line i of the [View] as the printer and [View.String] show
// it. The slices of the result belong to the caller, so changing them
// does not change the View. Row panics if i is outside the content, as
// the other index-taking methods do.
func (v *View) Row(i int) Row {
	row, m := v.layoutRow(i)
	segs := slices.Collect(v.Segments(i))

	if m == nil {
		row.Segments = segs
		row.Overlays = slices.Clone(v.Overlays(i))
		row.Annotations = slices.Clone(row.Annotations)

		return row
	}

	row.Segments = clipSegments(m, segs)

	for _, o := range v.Overlays(i) {
		for _, cols := range m.Spans(o.Cols) {
			o.Cols = cols
			row.Overlays = append(row.Overlays, o)
		}
	}

	return row
}

// layoutRow returns the [Row] of line i without its segments and
// overlays, and the layout of that row. The layout is nil for a line the
// View shows whole. The annotations of such a line are the slice the View
// holds, so a caller that hands the row out clones them. [View.String]
// renders from this row, and [View.Row] adds the segments and the
// overlays to it.
func (v *View) layoutRow(i int) (Row, *clip.Map) {
	ln := v.lines.lines[i]

	windows := v.Windows(i)
	if windows == nil {
		return Row{
			Content:     ln.Content(),
			Width:       ln.Width(),
			Annotations: v.Annotations(i),
		}, nil
	}

	m := clip.New(ln.Width(), windows)
	row := Row{
		Content: m.Content(ln.Content()),
		Width:   m.Width(),
		Windows: windows,
	}

	for _, a := range v.Annotations(i) {
		a.Cols = m.Span(a.Cols)
		row.Annotations = append(row.Annotations, a)
	}

	return row, m
}

// clipSegments returns the segments of the row that m lays out from
// segs, the segments of the whole line. Each window keeps the part of
// each segment it holds, and each ellipsis is a segment of its own in
// [kind.UISeparator]. The overlays of each kept segment move to the
// columns of the row with it.
func clipSegments(m *clip.Map, segs []Segment) []Segment {
	var out []Segment

	// The pieces and the segments both ascend, so first moves past the
	// segments that end before each piece once.
	first := 0

	for piece := range m.Pieces() {
		if piece.Gap {
			out = append(out, Segment{
				Text: clip.Ellipsis,
				Kind: kind.UISeparator,
				Cols: position.NewSpan(piece.At, piece.At+clip.EllipsisCols),
			})

			continue
		}

		for first < len(segs) && segs[first].Cols.End <= piece.Cols.Start {
			first++
		}

		for _, seg := range segs[first:] {
			if seg.Cols.Start >= piece.Cols.End {
				break
			}

			lo, hi := max(seg.Cols.Start, piece.Cols.Start), min(seg.Cols.End, piece.Cols.End)
			at := piece.At + lo - piece.Cols.Start
			cols := position.NewSpan(at, at+hi-lo)

			out = append(out, Segment{
				Text:     cutRunes(seg.Text, lo-seg.Cols.Start, hi-seg.Cols.Start),
				Kind:     seg.Kind,
				Overlays: clipSegmentOverlays(m, seg.Overlays, cols),
				Cols:     cols,
			})
		}
	}

	return out
}

// clipSegmentOverlays returns the overlays that cover a segment of the
// row on cols, the columns of the row it covers, from overlays, those of
// the segment of the line it comes from. A segment ends where an overlay
// starts or ends. The window that holds the segment therefore holds one
// run of each overlay, and that run covers cols.
func clipSegmentOverlays(m *clip.Map, overlays Overlays, cols position.Span) Overlays {
	if overlays == nil {
		return nil
	}

	out := make(Overlays, 0, len(overlays))

	for _, o := range overlays {
		for _, run := range m.Spans(o.Cols) {
			if run.Start <= cols.Start && cols.End <= run.End {
				o.Cols = run

				break
			}
		}

		out = append(out, o)
	}

	return out
}

// cutRunes returns the runes of s from rune index lo, inclusive, to rune
// index hi, exclusive.
func cutRunes(s string, lo, hi int) string {
	start, end := len(s), len(s)
	n := 0

	for i := range s {
		if n == lo {
			start = i
		}

		if n == hi {
			end = i

			break
		}

		n++
	}

	return s[min(start, end):end]
}
