// Package line provides abstractions for line-by-line go-yaml Token processing.
//
// Example input:
//
//	┌───────────────────┐
//	│foo: |-            │
//	│  hello            │
//	│  world            │
//	└───────────────────┘
//
// Normal [token.Tokens] stream:
//
//	┌──────┬────────────┐
//	│String│MappingValue│
//	├──────┴────────────┤
//	│String             │
//	│                   │
//	│                   │
//	└───────────────────┘
//
// Token streams using [Lines]:
//
//	┌──────┬────────────┐
//	│String│MappingValue│
//	├──────┴────────────┤
//	│String             │
//	├───────────────────┤
//	│String             │
//	└───────────────────┘
//
// # Key Types
//
// The package provides two core collection types:
//
//   - [Lines]: Collection of document lines created via [NewLines].
//     Supports round-trip reconstruction: [Lines.Tokens] returns the
//     original token stream by deduplicating shared [SegmentedToken.Source] pointers.
//   - [Line]: Single document line with tokens, optional [Annotation], and [Flag].
//
// Token segmentation types:
//
//   - [SegmentedToken]: Pairs a Source (original token) with a Part (per-line segment).
//     Multiple SegmentedTokens share the same Source pointer when a multiline token
//     is split across lines.
//   - [SegmentedTokens]: Slice type with [SegmentedTokens.SourceTokens] and
//     [SegmentedTokens.PartTokens] methods.
//
// Metadata types for display and diff tracking:
//
//   - [Annotation]: Extra content to add around a line (e.g., error markers).
//   - [Flag]: Line category constants ([FlagDefault], [FlagInserted], [FlagDeleted], [FlagAnnotation]).
package line
