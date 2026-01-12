// Package position defines line and column positions and ranges within a document.
//
// The [Position] type represents a 0-indexed line and column location.
// The [Range] type represents a half-open range [Start, End) between two positions.
// The [Ranges] type manages collections of ranges with overlap detection.
//
// Convention: All positions are 0-indexed (line and column start at 0).
// Ranges use half-open intervals where Start is inclusive and End is exclusive.
package position
