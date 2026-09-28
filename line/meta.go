package line

import (
	"strings"
	"unicode/utf8"

	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

// Placement says where an [Annotation] sits relative to its [Line].
type Placement int

const (
	// Above indicates content should appear above the line.
	Above Placement = iota
	// Below indicates content should appear below the line.
	Below
)

// Flag categorizes a [Line] for rendering.
type Flag int

const (
	// FlagDefault is the default category for normal lines.
	FlagDefault Flag = iota
	// FlagInserted marks lines as inserted in a diff (rendered with "+").
	FlagInserted
	// FlagDeleted marks lines as deleted in a diff (rendered with "-").
	FlagDeleted
)

// MaxColPastEnd is the furthest past the end of the content of a line, in
// columns, that a renderer starts an [Annotation]. [View.String] and the
// printer start an annotation whose [Annotation.Col] lies further out at
// this bound, so a stray column such as [math.MaxInt] pads a bounded row.
const MaxColPastEnd = 1024

// annotationCol returns the column a renderer starts an annotation at when
// its column is col and the content is width columns wide. The result lies
// between zero and [MaxColPastEnd] columns past the end of the content.
func annotationCol(col, width int) int {
	return min(max(0, col), width+MaxColPastEnd)
}

// Annotation represents extra content added around a [Line].
//
// An annotation adds comments or notes to the rendered output and is not
// part of the main token stream. Kind names the style the printer renders
// the annotation with, as [Overlay.Kind] does for an overlay, so an error
// message below a line renders in [kind.TextError] and a hunk header
// above one in [kind.UIHunkHeader]. The zero Kind renders as
// [kind.UIAnnotation].
//
// Add annotations to a [View] with [View.Annotate].
type Annotation struct {
	Content   string
	Kind      kind.Kind
	Placement Placement
	Col       int // Optional, 0-indexed column position for the annotation.
}

// String returns the annotation content padded to [Annotation.Col]. An
// annotation without content renders nothing, so the result is the empty
// string.
func (a Annotation) String() string {
	if a.Content == "" {
		return ""
	}

	padding := strings.Repeat(" ", max(0, a.Col))

	return padding + a.Content
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

// ByKind groups the annotations by the kind each renders in, its
// [Annotation.Kind] or [kind.UIAnnotation] for the zero Kind, one group per
// kind in the order each kind first appears, with the annotations of a
// group in their original order. Each annotation keeps its Kind as given,
// so the [kind.UIAnnotation] group can mix annotations of the zero Kind
// with ones of that kind. The printer renders each group as rows of its
// own in the style of its kind.
func (a Annotations) ByKind() []Annotations {
	var (
		groups []Annotations
		index  = make(map[kind.Kind]int)
	)

	for _, ann := range a {
		k := ann.Kind
		if k == "" {
			k = kind.UIAnnotation
		}

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

// Col returns the minimum column position among all annotations.
func (a Annotations) Col() int {
	if len(a) == 0 {
		return 0
	}

	col := a[0].Col
	for _, v := range a[1:] {
		col = min(col, v.Col)
	}

	return col
}

// WithContent returns a new [Annotations] holding the annotations that have
// content, in their original order. An annotation without content renders
// nothing, so its column must not pull [Annotations.Col] left; callers that
// pad to a column filter with WithContent first.
func (a Annotations) WithContent() Annotations {
	var result Annotations

	for _, ann := range a {
		if ann.Content != "" {
			result = append(result, ann)
		}
	}

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
// It joins same-position annotations with "; " at the minimum column
// position among the annotations that have content. Annotations without
// content add nothing, so a set with no content at all is the empty string,
// as a single such annotation is.
func (a Annotations) String() string {
	kept := a.WithContent()
	if len(kept) == 0 {
		return ""
	}

	padding := strings.Repeat(" ", max(0, kept.Col()))

	return padding + strings.Join(kept.Contents(), "; ")
}

// Overlay represents a styled column range within a single [Line].
//
// Overlays apply visual styles (highlighting, coloring) to specific portions of
// a line. Kind names the style the printer renders the columns with, as its
// [go.jacobcolvin.com/niceyaml/style.Styles] resolves it.
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

// MarkerRow returns the row that marks the overlays below content, the
// text of the line without its line ending: a caret under every column
// an overlay covers within the content and a space under every other
// column before the last caret, as [View.String] draws under a line. A
// column is as many carets wide as the rune on it renders, so the carets
// stay under the runes they mark on a line holding wide or control
// characters. Returns "" when the overlays cover no column of the
// content.
func (o Overlays) MarkerRow(content string) string {
	return strings.TrimRight(renderMarks(content, overlayMarks(o, utf8.RuneCountInString(content))), " ")
}
