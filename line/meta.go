package line

import (
	"strings"

	"jacobcolvin.com/niceyaml/position"
	"jacobcolvin.com/niceyaml/style"
)

// RelativePosition indicates a position relative to a [Line].
type RelativePosition int

const (
	// Above indicates content should appear above the line.
	Above RelativePosition = iota
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
	// FlagAnnotation marks annotation-only lines (no line number).
	FlagAnnotation
)

// Annotation represents extra content added around a [Line].
//
// It can be used to add comments or notes to the rendered output, without being
// part of the main token stream.
//
// Add annotations using [Line.Annotate].
type Annotation struct {
	Content  string
	Position RelativePosition
	Col      int // Optional, 0-indexed column position for the annotation.
}

// String returns the annotation content padded to the specified column.
func (a Annotation) String() string {
	if a.Content == "" {
		return ""
	}

	padding := strings.Repeat(" ", max(0, a.Col))

	return padding + a.Content
}

// Annotations is a slice of [Annotation]s with helper methods.
type Annotations []Annotation

// FilterPosition returns annotations matching the given [RelativePosition].
func (a Annotations) FilterPosition(pos RelativePosition) Annotations {
	var result Annotations

	for _, ann := range a {
		if ann.Position == pos {
			result = append(result, ann)
		}
	}

	return result
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
// a line.
//
// Add overlays using [Line.Overlay] or [Lines.AddOverlay].
type Overlay struct {
	Cols position.Span
	Kind style.Style
}

// Overlays is a slice of [Overlay]s for a single [Line].
type Overlays []Overlay
