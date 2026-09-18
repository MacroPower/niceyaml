package line

import (
	"strings"

	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/style"
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

// Annotation represents extra content added around a [Line].
//
// It can be used to add comments or notes to the rendered output, without being
// part of the main token stream. Kind names the style the printer renders
// the annotation with, as [Overlay.Kind] does for an overlay, so an error
// message below a line renders in [style.GenericError] and a hunk header
// above one in [style.Comment]. The zero Kind renders as [style.Comment].
//
// Add annotations to a [View] with [View.Annotate].
type Annotation struct {
	Content   string
	Kind      style.Kind
	Placement Placement
	Col       int // Optional, 0-indexed column position for the annotation.
}

// String returns the annotation content padded to the specified column.
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

// ByKind groups the annotations by [Annotation.Kind], one group per Kind
// in the order each Kind first appears, with the annotations of a group in
// their original order. The printer renders each group as rows of its own
// in the style of its Kind.
func (a Annotations) ByKind() []Annotations {
	var (
		groups []Annotations
		index  = make(map[style.Kind]int)
	)

	for _, ann := range a {
		i, ok := index[ann.Kind]
		if !ok {
			i = len(groups)
			index[ann.Kind] = i

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

// Contents returns the content of each annotation.
func (a Annotations) Contents() []string {
	contents := make([]string, len(a))

	for i, v := range a {
		contents[i] = v.Content
	}

	return contents
}

// String returns the combined annotation content for debugging.
// Same-position annotations are joined by "; " at the minimum column position.
func (a Annotations) String() string {
	if len(a) == 0 {
		return ""
	}

	if len(a) == 1 {
		return a[0].String()
	}

	// Find minimum column and collect content.
	minCol := a[0].Col
	contents := make([]string, len(a))

	for i, ann := range a {
		contents[i] = ann.Content
		if ann.Col < minCol {
			minCol = ann.Col
		}
	}

	padding := strings.Repeat(" ", max(0, minCol))

	return padding + strings.Join(contents, "; ")
}

// Overlay represents a styled column range within a single [Line].
//
// Overlays apply visual styles (highlighting, coloring) to specific portions of
// a line. Kind names the style the printer renders the columns with, as its
// [style.Styles] resolves it.
//
// Add overlays to a [View] with [View.AddOverlay], [View.BlendOverlay], or
// [View.AddLineOverlay].
type Overlay struct {
	Kind style.Kind
	Cols position.Span
	// Blend mixes the overlay style with the style underneath it instead of
	// replacing it, so a highlight keeps the token or diff color it covers.
	Blend bool
}

// Overlays is a slice of [Overlay] values for a single [Line].
type Overlays []Overlay
