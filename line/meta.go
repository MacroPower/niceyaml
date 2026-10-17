package line

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

// Placement says where an [Annotation] sits relative to its [Line].
type Placement int

const (
	// Above places an annotation above its line.
	Above Placement = iota
	// Below places an annotation below its line.
	Below
)

// String returns "above" for [Above], "below" for [Below], and
// "Placement(n)" for any other value n.
func (p Placement) String() string {
	switch p {
	case Above:
		return "above"
	case Below:
		return "below"
	default:
		return "Placement(" + strconv.Itoa(int(p)) + ")"
	}
}

// Flag categorizes a [Line] for rendering.
type Flag int

const (
	// FlagDefault is the default category for normal lines.
	FlagDefault Flag = iota
	// FlagInserted marks a line a diff inserted. It renders with a "+"
	// prefix.
	FlagInserted
	// FlagDeleted marks a line a diff deleted. It renders with a "-"
	// prefix.
	FlagDeleted
)

// String returns "default" for [FlagDefault], "inserted" for
// [FlagInserted], "deleted" for [FlagDeleted], and "Flag(n)" for any other
// value n.
func (f Flag) String() string {
	switch f {
	case FlagDefault:
		return "default"
	case FlagInserted:
		return "inserted"
	case FlagDeleted:
		return "deleted"
	default:
		return "Flag(" + strconv.Itoa(int(f)) + ")"
	}
}

// MaxColPastEnd is the furthest past the end of the content of a line, in
// columns, that a renderer starts an [Annotation]. [View.String] and the
// printer start an annotation at this bound when its [Annotation.Cols]
// start further out, so a stray column such as [math.MaxInt] pads a
// bounded row. [Annotation.String] and [Annotations.String] lack line
// content, so they pad by at most this many spaces.
const MaxColPastEnd = 1024

// annotationCol returns the column a renderer starts an annotation at when
// its column is col and the content is width columns wide. The result lies
// between column 0 and [MaxColPastEnd] columns past the end of the
// content, inclusive.
func annotationCol(col, width int) int {
	return min(max(0, col), width+MaxColPastEnd)
}

// Annotation represents extra content around a [Line].
//
// An annotation adds comments or notes to the rendered output, outside
// the token stream. Kind names the style the printer renders the
// annotation with, as [Overlay.Kind] does for an overlay. For example, an
// error message below a line renders in [kind.TextError], and a hunk
// header above one renders in [kind.UIHunkHeader]. The zero Kind renders
// as [kind.UIAnnotation].
//
// Cols names the columns of the line the annotation labels. Below the
// line, a renderer draws a caret under each cell of those columns, so a
// rune that takes two cells gets two carets. The content follows the
// carets, so it sits beside the run it labels. A span that does not
// cover any column of the content, such as an empty one, gets a single
// caret at its start. An annotation without content draws its carets
// alone. Above the line, the content starts at the column Cols starts
// at.
//
// Add annotations to a [View] with [View.Annotate].
type Annotation struct {
	Content   string
	Kind      kind.Kind
	Placement Placement
	// Cols counts columns from 0 and is half-open. A column is a rune of
	// the content, not a terminal cell. The zero span labels column 0. A
	// renderer that pads an annotation row to a column measures the cells
	// before it with
	// [go.jacobcolvin.com/niceyaml/printer.AnnotationContext.ColWidth].
	// Like the printer, that method stops at [MaxColPastEnd] columns past
	// the end.
	// [go.jacobcolvin.com/niceyaml/printer.DefaultAnnotation] draws a caret
	// for each cell of the columns. View.String and the printer do both.
	Cols position.Span
}

// String returns the annotation content padded to the start of
// [Annotation.Cols]. It lacks line content to measure, so the padding
// stops at [MaxColPastEnd] spaces. String returns the empty string for an
// annotation without content.
func (a Annotation) String() string {
	if a.Content == "" {
		return ""
	}

	padding := strings.Repeat(" ", annotationCol(a.Cols.Start, 0))

	return padding + a.Content
}

// RenderKind returns the kind the annotation renders in: its
// [Annotation.Kind], or [kind.UIAnnotation] for the zero Kind.
func (a Annotation) RenderKind() kind.Kind {
	if a.Kind == "" {
		return kind.UIAnnotation
	}

	return a.Kind
}

// Annotations is a slice of [Annotation] values with helper methods.
type Annotations []Annotation

// Filter returns the annotations with the given [Placement].
func (a Annotations) Filter(p Placement) Annotations {
	var result Annotations

	for _, ann := range a {
		if ann.Placement == p {
			result = append(result, ann)
		}
	}

	return result
}

// Groups returns the annotations at placement p grouped by the kind each
// renders in, as [Annotation.RenderKind] reports it, in the order a
// renderer draws the groups. The annotations of a group keep their order.
// Each annotation keeps its Kind as given, so the [kind.UIAnnotation]
// group can mix annotations of the zero Kind with ones of that kind.
//
// Below a line, the group of [kind.TextError] comes first, then the group
// of [kind.TextWarn], then the other groups in the order their kinds
// first appear. At any other placement, such as [Above], the groups come
// in the order their kinds first appear.
//
// [View.String] and the printer draw each group as rows of its own in
// this order. A renderer of its own that ranges over Groups thus draws
// the rows of a line in the same order.
func (a Annotations) Groups(p Placement) []Annotations {
	var (
		groups []Annotations
		index  = make(map[kind.Kind]int)
	)

	for _, ann := range a {
		if ann.Placement != p {
			continue
		}

		k := ann.RenderKind()

		i, ok := index[k]
		if !ok {
			i = len(groups)
			index[k] = i

			groups = append(groups, nil)
		}

		groups[i] = append(groups[i], ann)
	}

	// The rows below a line come in order of severity, errors first.
	if p == Below {
		slices.SortStableFunc(groups, func(x, y Annotations) int {
			return cmp.Compare(severity(x[0].RenderKind()), severity(y[0].RenderKind()))
		})
	}

	return groups
}

// severity returns the sort key of the group of annotations below a line
// that renders in k: 0 for errors, 1 for warnings, and 2 for every other
// kind.
func severity(k kind.Kind) int {
	switch k {
	case kind.TextError:
		return 0
	case kind.TextWarn:
		return 1
	default:
		return 2
	}
}

// Start returns the first column any of the annotations starts at, the
// smallest start of their [Annotation.Cols], or 0 when the slice is empty.
func (a Annotations) Start() int {
	if len(a) == 0 {
		return 0
	}

	col := a[0].Cols.Start
	for _, v := range a[1:] {
		col = min(col, v.Cols.Start)
	}

	return col
}

// WithContent returns a new [Annotations] that holds the annotations that
// have content, sorted by the column each starts at. Annotations that
// start at the same column keep their original order. An annotation
// without content does not render any text, so its column must not pull
// [Annotations.Start] left. Callers that pad to a column or join contents
// filter with WithContent first, so the joined messages read in the order
// of the columns they mark.
func (a Annotations) WithContent() Annotations {
	var result Annotations

	for _, ann := range a {
		if ann.Content != "" {
			result = append(result, ann)
		}
	}

	slices.SortStableFunc(result, func(x, y Annotation) int {
		return cmp.Compare(x.Cols.Start, y.Cols.Start)
	})

	return result
}

// Contents returns the content of each annotation.
func (a Annotations) Contents() []string {
	contents := make([]string, len(a))

	for i, v := range a {
		contents[i] = v.Content
	}

	return contents
}

// String returns the combined annotation content for debugging.
// It joins the annotations with "; " in column order, as
// [Annotations.WithContent] sorts them. The result starts at the first
// column that an annotation with content starts at, and the padding stops
// at [MaxColPastEnd] spaces, as in [Annotation.String]. Annotations
// without content do not add any text. For a set without content, String
// returns the empty string, as [Annotation.String] does for a single such
// annotation.
func (a Annotations) String() string {
	kept := a.WithContent()
	if len(kept) == 0 {
		return ""
	}

	padding := strings.Repeat(" ", annotationCol(kept.Start(), 0))

	return padding + strings.Join(kept.Contents(), "; ")
}

// Overlay represents a styled column range within a single [Line].
//
// Overlays apply visual styles, such as highlighting and coloring, to
// specific portions of a line. Kind names the style the printer renders the
// columns with, as its [go.jacobcolvin.com/niceyaml/style.Styles] resolves
// it. An overlay does not draw a caret, so a range shows in plain output
// only when an [Annotation] labels the same columns. For a range that spans
// several lines, add one annotation for each range that [Lines.SliceLines]
// splits it into.
//
// Add overlays to a [View] with [View.AddOverlay], [View.BlendOverlay], or
// [View.AddLineOverlay].
type Overlay struct {
	Kind kind.Kind
	Cols position.Span
	// Blend mixes the overlay style with the style underneath it instead of
	// replacing it, so a highlight keeps the token or diff color it covers.
	Blend bool
}

// Overlays is a slice of [Overlay] values for a single [Line].
type Overlays []Overlay
