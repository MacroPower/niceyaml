// Package tokens provides segmentation for multiline YAML tokens and utilities
// for syntax highlighting.
//
// # Token Segments
//
// YAML lexers produce tokens that may span multiple lines (block scalars,
// multiline strings, folded content).
//
// When rendering YAML line-by-line for display or editing, each line needs its
// portion of such tokens while still knowing which original token it came from.
// This package bridges that gap.
//
// A [Segment] pairs an original source token with a "part" token representing
// one line's portion.
//
// For a three-line block scalar, you get three Segments that share the same
// source pointer but have different parts.
//
// This shared pointer enables efficient deduplication: call
// [Segments.SourceTokens] to recover the original tokens without duplicates.
//
// # Building Line-Based Views
//
// The [line] package uses [Segments] to represent a single line's worth of
// tokens and [Segments2] for multiple lines.
//
// Position-based queries like [Segments2.TokenRangesAt] find all ranges a
// token occupies across lines, which is useful for highlighting all parts of
// a token (e.g. for errors).
//
// # Pointer Safety
//
// Source pointers are shared internally for equality checks and deduplication,
// but [Segment.Source] and [Segment.Part] return clones to prevent accidental
// modification. Use [Segment.SourceEquals] for pointer comparisons.
//
// # Syntax Highlighting
//
// [TypeStyle] maps token types to [style.Style] values for syntax highlighting.
// It handles context-sensitive styling: a string followed by a colon is styled
// as a mapping key, not a plain string.
//
// # Multi-Document YAML
//
// [SplitDocuments] splits a token stream at document headers ("---"), returning
// an iterator over separate token streams for each YAML document. Use
// [WithResetPositions] to reset token positions so each document starts from
// line 1, column 1.
package tokens
