package line

import "strings"

// RelativePosition indicates a relative position to a line.
type RelativePosition int

const (
	// Above indicates content should appear above the line.
	Above RelativePosition = iota
	// Below indicates content should appear below the line.
	Below
)

// Flag identifies a category for YAML lines.
type Flag int

const (
	// FlagDefault is the default/fallback for line categories.
	FlagDefault Flag = iota
	// FlagInserted indicates lines inserted in diff (+).
	FlagInserted
	// FlagDeleted indicates lines deleted in diff (-).
	FlagDeleted
	// FlagAnnotation indicates annotation/header lines (no line number).
	FlagAnnotation
)

// Annotation represents extra content to be added around a line.
// It can be used to add comments or notes to the rendered output, without being
// part of the main token stream.
type Annotation struct {
	Content  string
	Position RelativePosition
	Col      int // Optional, 0-indexed column position for the annotation.
}

// String returns the annotation as a string, properly padded to the specified column.
func (a Annotation) String() string {
	if a.Content == "" {
		return ""
	}

	padding := strings.Repeat(" ", max(0, a.Col))

	return padding + a.Content
}

// Annotations is a collection of [Annotation]s.
type Annotations []Annotation

// Add appends the given [Annotation]s to the collection.
func (a *Annotations) Add(anns ...Annotation) {
	*a = append(*a, anns...)
}

// IsEmpty returns true if there are no annotations.
func (a Annotations) IsEmpty() bool {
	return len(a) == 0
}

// FilterPosition returns annotations matching the given [RelativePosition].
func (a Annotations) FilterPosition(pos RelativePosition) Annotations {
	if len(a) == 0 {
		return nil
	}

	var result Annotations
	for _, ann := range a {
		if ann.Position == pos {
			result = append(result, ann)
		}
	}

	return result
}

// String returns the combined annotation content for debugging.
// Same-position annotations are joined by "; " at the minimum column position.
// For styled rendering, use [Printer] with [AnnotationFunc].
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
