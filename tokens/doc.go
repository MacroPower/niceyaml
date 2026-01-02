// Package tokens provides structures and utilities for handling YAML tokens.
//
// # Segments
//
// When a YAML token spans multiple lines (e.g., block scalars, multiline strings),
// it must be split into segments for line-by-line processing while preserving
// a reference to the original source token. This package provides types to
// manage that segmentation.
//
// The package provides three collection types:
//
//   - [Segment]: Pairs a source [*token.Token] with its segmented part.
//   - [Segments]: Slice of [Segment], typically representing one line's tokens.
//   - [Segments2]: Slice of [Segments] (2D slice of [Segment]s), typically used
//     for multi-line tokens.
//
// ## Pointer Identity
//
// Source pointers are intentionally shared across [Segment]s of the same token.
// This enables internal equality checks and deduplication via pointer comparison.
// However, to prevent accidental modification of tokens, only clones of tokens
// are exposed externally.
package tokens
