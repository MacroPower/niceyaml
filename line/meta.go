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
