// Package excerpt marks the locations of errors on the lines of a
// source, as an excerpt of an error shows them.
//
// A location highlights the content of the token at it, or the range an
// error carried, as [Ranges] finds it. [Annotate] marks each [Mark] on a
// view. It adds an overlay on each line of the text the mark covers. It
// also adds an annotation below each of those lines that labels the same
// columns. A renderer draws each annotation as a caret run under its
// columns, with the message of the mark beside the run on the line of the
// mark.
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
	// Message is the text of the annotation, or the empty string when the
	// annotation does not have text.
	Message string
	// Ranges holds the ranges to highlight, as [Ranges] returns them.
	Ranges position.Ranges
	// Pos is the position of the error.
	Pos position.Position
	// Warning reports whether the mark belongs to a warning. A warning
	// mark highlights in [kind.GenericWarn] and annotates in
	// [kind.TextWarn] rather than in the kinds of an error.
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

// Annotate puts each mark in marks on view and reports whether it marked
// any line. The marks hold the resolved locations of the errors in lines.
// Annotate leaves the view as it is and reports false when marks is empty
// or the view does not hold any of their lines.
//
// The view can hold the lines at any indices, as a diff does, so every
// mark goes to the index that holds its line. A mark highlights each line
// its ranges cover with an overlay. It also labels the same columns with
// an annotation below that line, so a renderer draws a caret run under
// them without color.
//
// The first annotation on the line of the mark, in the order of its
// ranges, carries its message. The other annotations of the mark do not
// carry a message. A mark with an empty message thus gets only caret
// runs. The highlight on the line of a mark might start past the column
// of the mark, as for a mark on the spaces before a token. The message
// then sits under the token.
//
// A mark whose highlight omits its own line labels the column of the mark
// with an annotation of zero width, which carries its message. The
// highlight omits that line for a mark without a token under it, and for
// a range that covers zero columns of its lines. It also omits that line
// for a mark on a line of only spaces inside a block scalar. The
// highlight omits that line for a range that starts at the end of its
// first line too. The annotation marks the line as decorated, so the line
// joins the hunks [line.View.Hunks] keeps.
//
// When the column of the mark lies past the end of its line, the
// annotation of zero width moves to the column after the last rune of the
// line. A far column then costs a renderer the same number of cells as a
// column at the end.
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
// A line keeps each mark one time. Each repeat of a mark, in one call or
// across several, leaves the view as the first copy left it. A message
// that repeats on one run therefore reads one time.
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
// view that holds that line. The lookup finds the line by identity. It
// reports false for a line index that lines does not hold, and for a line
// that the view does not hold.
func lineIndex(view *line.View, lines line.Lines) func(int) (int, bool) {
	return func(i int) (int, bool) {
		if i < 0 || i >= lines.Len() {
			return 0, false
		}

		return view.Index(lines.Line(i))
	}
}

// Ranges returns the ranges to highlight for a location in lines. The
// ranges are rng cut to lines, or else the content of tk, the token a path
// resolved to. Without either, they are the content of the token at pos.
// The parser makes a token for an empty value that the lines do not hold.
// So the ranges of a path to such a value are empty. The token at its
// position is a comment or the spaces after the colon, so Ranges does not
// use it.
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
// first to the end of the last. When ranges is empty, it returns the
// empty range at pos.
func Cover(ranges position.Ranges, pos position.Position) position.Range {
	if len(ranges) == 0 {
		return position.NewRange(pos, pos)
	}

	return position.NewRange(ranges[0].Start, ranges[len(ranges)-1].End)
}
