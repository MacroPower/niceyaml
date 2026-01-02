package line

import "strings"

// Annotation represents extra content to be added around a line.
// It can be used to add comments or notes to the rendered output, without being
// part of the main token stream.
type Annotation struct {
	Content string
	Column  int // Optional, 1-indexed column position for the annotation.
}

// String returns the annotation as a string, properly padded to the specified column.
func (a Annotation) String() string {
	if a.Content == "" {
		return ""
	}

	padding := strings.Repeat(" ", max(0, a.Column-1))

	return padding + "^ " + a.Content
}

// Flag identifies a category for YAML lines.
type Flag int

// Flag constants for YAML line categories.
const (
	FlagDefault    Flag = iota // Default/fallback.
	FlagInserted               // Lines inserted in diff (+).
	FlagDeleted                // Lines deleted in diff (-).
	FlagAnnotation             // Annotation/header lines (no line number).
)
