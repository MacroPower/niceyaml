// Package excerpt marks the locations of errors on the lines of a
// source, as an excerpt of an error shows them.
//
// A location highlights the content of the token at it, or the range an
// error carried, as [Ranges] finds it. [Annotate] marks each [Mark] on a
// view: an overlay on each line of the text it covers, and an annotation
// below each of those lines that labels the same columns. A renderer
// draws each annotation as a caret run under its columns, with the
// message of the mark beside the run on the line of the mark.
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
	// The mark belongs to a warning, so it highlights in
	// [kind.GenericWarn] and annotates in [kind.TextWarn] rather than in
	// the kinds of an error.
	Warning bool
}

// overlayKind returns the kind m highlights its location in.
func (m Mark) overlayKind() kind.Kind {
	if m.Warning {
		return kind.GenericWarn
	}

	return kind.GenericError
}

// textKind returns the kind m annotates its line in.
func (m Mark) textKind() kind.Kind {
	if m.Warning {
		return kind.TextWarn
	}

	return kind.TextError
}

// Annotate puts each mark in marks on view, where marks hold the resolved
// locations of the errors in lines, and reports whether it marked any
// line. It marks nothing when marks is empty or the view holds none of
// their lines.
//
// The view can hold the lines at any indices, as a diff does, so every
// mark goes to the index that holds its line. A mark highlights each line
// its ranges cover with an overlay. It also labels the same columns with
// an annotation below that line, so a renderer draws a caret run under
// them without color.
//
// The first annotation on the line of the mark, in the order of its
// ranges, carries its message, and every other annotation of the mark
// carries none. A mark with no message thus gets caret runs alone. The
// highlight on the line of a mark can start past the column of the mark,
// as for a mark on the spaces before a token. The message then sits under
// the token.
//
// A mark whose highlight leaves out its own line labels the column of the
// mark with an annotation of no width, which carries its message. That
// holds for a mark with no token under it and a range that covers no
// column of its lines. It also holds for a mark on a line of only spaces
// inside a block scalar, and a range that starts at the end of its first
// line. The annotation marks the line as decorated, so the line joins the
// hunks [line.View.Hunks] keeps. A column past the end of its line moves
// to the column after its last rune. A far column then costs a renderer
// no more cells than a column at the end.
//
// A mark of a warning highlights in [kind.GenericWarn] and annotates in
// [kind.TextWarn], and a mark of an error in [kind.GenericError] and
// [kind.TextError]. The printer and [line.View.String] draw the rows of
// errors first.
//
// Each tab in a message becomes four spaces, as [escape.Tabs] returns it.
// A renderer draws the other control characters of an annotation as
// their pictures.
//
// A line keeps each mark once. Marks that repeat, in one call or across
// several, leave the view as the first of them does, so a message that
// repeats on one run reads once.
func Annotate(view *line.View, lines line.Lines, marks []Mark) bool {
	if len(marks) == 0 {
		return false
	}

	index := lineIndex(view, lines)
	marked := false

	for _, m := range marks {
		var segments []position.Range

		for _, r := range m.Ranges {
			segments = append(segments, lines.SliceLines(r)...)
		}

		// The message goes on the first segment of the line of the mark,
		// or on the column of the mark when no segment lies on that line.
		labeled := slices.IndexFunc(segments, func(lr position.Range) bool {
			return lr.Start.Line == m.Pos.Line
		})

		if i, ok := index(m.Pos.Line); ok && labeled < 0 {
			col := min(m.Pos.Col, lines.Line(m.Pos.Line).Width())
			addAnnotation(view, i, line.Annotation{
				Content:   escape.Tabs(m.Message),
				Kind:      m.textKind(),
				Placement: line.Below,
				Cols:      position.NewSpan(col, col),
			})

			marked = true
		}

		// [line.Lines.SliceLines] already clamped each segment to its
		// line, which the view holds at index i.
		for j, lr := range segments {
			i, ok := index(lr.Start.Line)
			if !ok {
				continue
			}

			cols := position.NewSpan(lr.Start.Col, lr.End.Col)

			addOverlay(view, i, line.Overlay{Cols: cols, Kind: m.overlayKind()})

			ann := line.Annotation{Kind: m.textKind(), Placement: line.Below, Cols: cols}
			if j == labeled {
				ann.Content = escape.Tabs(m.Message)
			}

			addAnnotation(view, i, ann)

			marked = true
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

// Ranges returns the ranges to highlight for a location in lines: rng cut
// to lines, or else the content of tk, the token a path resolved to, or
// else the content of the token at pos. The parser makes a token for an
// empty value that no line holds, so a path to one has no ranges, where
// the token at its position would be a comment or the spaces after the
// colon.
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
// ends at the end of that line. A range that ends before its start, as
// given or after that cut, becomes the empty range at its start. An end
// at a negative column moves to the start of its line.
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
