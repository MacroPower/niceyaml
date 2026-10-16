package line

import (
	"cmp"
	"slices"
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

// MaxColPastEnd is the furthest past the end of the content of a line, in
// columns, that a renderer starts an [Annotation]. [View.String] and the
// printer start an annotation whose [Annotation.Cols] start further out at
// this bound, so a stray column such as [math.MaxInt] pads a bounded row.
// [Annotation.String] and [Annotations.String] have no line content, so
// they pad by at most this many spaces.
const MaxColPastEnd = 1024

// annotationCol returns the column a renderer starts an annotation at when
// its column is col and the content is width columns wide. The result lies
// between zero and [MaxColPastEnd] columns past the end of the content.
func annotationCol(col, width int) int {
	return min(max(0, col), width+MaxColPastEnd)
}

// Annotation represents extra content added around a [Line].
//
// An annotation adds comments or notes to the rendered output, outside
// the token stream. Kind names the style the printer renders the
// annotation with, as [Overlay.Kind] does for an overlay, so an error
// message below a line renders in [kind.TextError] and a hunk header
// above one in [kind.UIHunkHeader]. The zero Kind renders as
// [kind.UIAnnotation].
//
// Cols names the columns of the line the annotation labels. Below the
// line, a renderer draws a caret under each of those columns and the
// content after the carets. The content thus sits beside the run it
// labels. A span that covers no column of the content, such as an empty
// one, gets a single caret at its start. An annotation without content
// draws its carets alone. Above the line, the content starts at the
// column Cols starts at.
//
// Add annotations to a [View] with [View.Annotate].
type Annotation struct {
	Content   string
	Kind      kind.Kind
	Placement Placement
	// Cols counts columns from 0 and is half-open. The zero span labels
	// column 0.
	Cols position.Span
}

// String returns the annotation content padded to the start of
// [Annotation.Cols]. It has no line content to measure, so the padding
// stops at [MaxColPastEnd] spaces. An annotation without content renders
// nothing, so the result is the empty string.
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

// ByKind groups the annotations by the kind each renders in, as
// [Annotation.RenderKind] reports it. The groups come in the order each
// kind first appears, and the annotations of a group keep their original
// order. Each annotation keeps its Kind as given, so the
// [kind.UIAnnotation] group can mix annotations of the zero Kind with ones
// of that kind. The printer renders each group as rows of its own in the
// style of its kind.
func (a Annotations) ByKind() []Annotations {
	var (
		groups []Annotations
		index  = make(map[kind.Kind]int)
	)

	for _, ann := range a {
		k := ann.RenderKind()

		i, ok := index[k]
		if !ok {
			i = len(groups)
			index[k] = i

			groups = append(groups, nil)
		}

		groups[i] = append(groups[i], ann)
	}

	return groups
}

// Start returns the first column any of the annotations starts at, the
// smallest start of their [Annotation.Cols], or 0 when there are none.
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

// WithContent returns a new [Annotations] holding the annotations that have
// content, sorted by the column each starts at. Annotations that start at
// the same column keep their original order. An annotation without
// content renders nothing, so its column must not pull [Annotations.Start]
// left. Callers that pad to a column or join contents filter with
// WithContent first, so the joined messages read in the order of the
// columns they mark.
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
// [Annotations.WithContent] sorts them, at the first column the
// annotations that have content start at, padded by at most
// [MaxColPastEnd] spaces, as [Annotation.String] pads. Annotations without
// content add nothing, so a set with no content at all is the empty string,
// as a single such annotation is.
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
// Overlays apply visual styles (highlighting, coloring) to specific portions of
// a line. Kind names the style the printer renders the columns with, as its
// [go.jacobcolvin.com/niceyaml/style.Styles] resolves it. An overlay draws
// no caret, so a range shows in plain output only when an [Annotation]
// labels the same columns. For a range that spans several lines, add one
// annotation for each range that [Lines.SliceLines] splits it into.
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
