// Package excerpt marks the locations of errors on the lines of a
// source, as an excerpt of an error shows them.
//
// A location highlights the content of the token at it, or the range an
// error carried, as [Ranges] finds it. [Annotate] marks each [Mark] on a
// view: an overlay on the text it covers, and its message as an
// annotation below its line. A renderer draws a line with no message as
// a caret run under its overlays.
package excerpt

import (
	"slices"

	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/escape"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

// Mark is the resolved location of one error, with the text to annotate
// its line with.
type Mark struct {
	// The text of the annotation, or the empty string for none.
	Message string
	// The ranges to highlight, as [Ranges] returns them.
	Ranges position.Ranges
	// The position of the error.
	Pos position.Position
}

// Annotate marks marks, the resolved locations of the errors in lines,
// on view, and reports whether it marked any line. It adds an annotation
// without content below each line it marked that has no annotation below
// it, so a renderer draws a caret run there. It marks nothing when marks
// is empty or the view holds none of their lines.
//
// The view may hold the lines at any indices, as a diff does, so every
// mark goes to the index that holds its line. A mark whose highlight
// leaves out its own line gets an overlay of no width at its column on
// that line. That holds for a mark with no token under it and a range
// that covers no column of its lines. It also holds for a mark on a line
// of only spaces inside a block scalar, and a range that starts at the
// end of its first line. The overlay renders nothing and still marks the
// line as decorated, so the line joins the hunks [line.View.Hunks] keeps.
//
// Each mark with a message adds one annotation below its line, so a line
// several marks mark carries one annotation per message, and a renderer
// joins them in column order. Each tab in a message becomes four spaces,
// as [escape.Tabs] returns it. A renderer draws the other control
// characters of an annotation as their pictures. The annotation starts
// at the first column the highlight of its mark covers on that line,
// where the caret run starts, so a mark on the spaces around a token puts
// its message under the token. A mark with no highlight on its line
// keeps its column. A column past the end of its line moves to the
// column after its last rune, in the overlay and in the annotation below
// the line. A far column then costs a renderer no more cells than a
// column at the end.
//
// A line keeps each mark once. Marks that repeat, in one call or across
// several, leave the view as the first of them does, so a message that
// repeats at one column reads once.
func Annotate(view *line.View, lines line.Lines, marks []Mark) bool {
	marked := annotateLines(view, lines, marks)

	markUnannotated(view, marked)

	return len(marked) > 0
}

// annotateLines marks marks on view as [Annotate] describes, and returns
// the indices of the lines it marked, with repeats.
func annotateLines(view *line.View, lines line.Lines, marks []Mark) []int {
	if len(marks) == 0 {
		return nil
	}

	index := lineIndex(view, lines)

	var marked []int

	for _, m := range marks {
		var segments []position.Range

		for _, r := range m.Ranges {
			segments = append(segments, lines.SliceLines(r)...)
		}

		if i, ok := index(m.Pos.Line); ok {
			marked = append(marked, i)

			width := lines.Line(m.Pos.Line).Width()
			start, onLine := highlightStart(m.Pos, segments)

			if !onLine {
				col := min(m.Pos.Col, width)
				addOverlay(view, i, line.Overlay{
					Cols: position.NewSpan(col, col),
					Kind: kind.GenericError,
				})
			}

			if m.Message != "" {
				addAnnotation(view, i, line.Annotation{
					Content:   escape.Tabs(m.Message),
					Kind:      kind.TextError,
					Placement: line.Below,
					Col:       min(start, width),
				})
			}
		}

		// [line.Lines.SliceLines] already clamped each segment to its
		// line, which the view holds at index i.
		for _, lr := range segments {
			if i, ok := index(lr.Start.Line); ok {
				addOverlay(view, i, line.Overlay{
					Cols: position.NewSpan(lr.Start.Col, lr.End.Col),
					Kind: kind.GenericError,
				})

				marked = append(marked, i)
			}
		}
	}

	return marked
}

// addOverlay adds o to line i of view, unless the line holds an equal
// overlay already.
func addOverlay(view *line.View, i int, o line.Overlay) {
	if !slices.Contains(view.Overlays(i), o) {
		view.AddLineOverlay(i, o)
	}
}

// addAnnotation adds a to line i of view, unless the line holds an equal
// annotation already.
func addAnnotation(view *line.View, i int, a line.Annotation) {
	if !slices.Contains(view.Annotations(i), a) {
		view.Annotate(i, a)
	}
}

// highlightStart returns the first column that segments cover on the
// line of at, or the column of at when no segment lies on that line. It
// also reports whether any segment lies on that line.
func highlightStart(at position.Position, segments []position.Range) (int, bool) {
	col, found := at.Col, false

	for _, lr := range segments {
		if lr.Start.Line == at.Line && (!found || lr.Start.Col < col) {
			col, found = lr.Start.Col, true
		}
	}

	return col, found
}

// markUnannotated adds an annotation without content, in [kind.TextError]
// and at the first column its overlays without Blend set cover, so a
// search highlight on the line moves no caret, below every line of view at
// an index in marked that carries no annotation below it yet. A line
// several locations mark, or that an earlier error marked, thus gets one.
// [line.View.String] draws the marks of a line from its overlays. A
// renderer that draws them from its annotations, as the printer does,
// draws such an annotation as a caret run under the overlays. The range
// of a location with no message beside it, such as the root of a bound
// error, then shows its extent without color.
func markUnannotated(view *line.View, marked []int) {
	for _, i := range marked {
		// A line repeats in marked once per location on it, so the check
		// for an annotation below it comes before the scan of its overlays.
		// Each repeat after the first then skips the scan.
		if slices.ContainsFunc(view.Annotations(i), func(a line.Annotation) bool {
			return a.Placement == line.Below
		}) {
			continue
		}

		col, found := 0, false

		for _, o := range view.Overlays(i) {
			if !o.Blend && (!found || o.Cols.Start < col) {
				col, found = o.Cols.Start, true
			}
		}

		if found {
			view.Annotate(i, line.Annotation{Kind: kind.TextError, Placement: line.Below, Col: col})
		}
	}
}

// lineIndex returns a lookup from a line index of lines to the index of
// view that holds that line, found by identity. A line index lines does
// not hold, or a line the view does not hold, reports false.
func lineIndex(view *line.View, lines line.Lines) func(int) (int, bool) {
	return func(i int) (int, bool) {
		if i < 0 || i >= lines.Len() {
			return 0, false
		}

		return view.Index(lines.Line(i))
	}
}

// Ranges returns the ranges to highlight for a location in lines: rng
// itself, cut to lines, when the location carries one, then the content
// of tk, the token a path resolved to, and otherwise the content of the
// token at pos. The parser makes a token for an empty value that no line
// holds, so a path to one has no ranges, where the token at its position
// would be a comment or the spaces after the colon.
func Ranges(lines line.Lines, rng *position.Range, tk *token.Token, pos position.Position) position.Ranges {
	if rng != nil {
		return position.Ranges{clampRange(lines, *rng)}
	}

	if tk != nil {
		return lines.ContentRanges(tk)
	}

	return lines.ContentRanges(lines.TokenAt(pos))
}

// clampRange returns r cut to lines. A range that runs past the last line
// ends at the end of that line, so a range an error carried marks lines
// the source has. A range that ends before its start, as given or after
// that cut, covers nothing, as [position.Range.LastLine] counts it, so it
// becomes the empty range at its start, and no range ends before its
// start. An end on a later line at a column before the first moves to
// the start of that line, so no range has a negative column. A range
// within the lines comes back as it is.
func clampRange(lines line.Lines, r position.Range) position.Range {
	if last := lines.Len() - 1; last >= 0 && r.End.Line > last {
		r.End = position.New(last, lines.Line(last).Width())
	}

	if r.End.Line < r.Start.Line || (r.End.Line == r.Start.Line && r.End.Col < r.Start.Col) {
		return position.NewRange(r.Start, r.Start)
	}

	r.End.Col = max(r.End.Col, 0)

	return r
}

// Cover returns the one range that spans ranges, from the start of the
// first to the end of the last. With no ranges, it returns the empty
// range at pos.
func Cover(ranges position.Ranges, pos position.Position) position.Range {
	if len(ranges) == 0 {
		return position.NewRange(pos, pos)
	}

	return position.NewRange(ranges[0].Start, ranges[len(ranges)-1].End)
}
