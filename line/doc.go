// Package line provides abstractions for line-by-line go-yaml Token processing.
//
// # Token Splitting
//
// YAML tokens from go-yaml can span multiple lines (block scalars, multiline
// strings), but many operations—rendering, diffing, error highlighting—need
// to work line-by-line.
//
// This package bridges that gap by organizing tokens into lines while
// preserving references to the original tokens.
//
// Consider this YAML input with a block scalar:
//
//	┌───────────────────┐
//	│foo: |-            │
//	│  hello            │
//	│  world            │
//	└───────────────────┘
//
// The go-yaml lexer produces a normal [token.Tokens] stream where the block
// scalar content is a single token spanning multiple lines:
//
//	┌──────┬────────────┐
//	│String│MappingValue│
//	├──────┴────────────┤
//	│String             │
//	│                   │
//	│                   │
//	└───────────────────┘
//
// Using [NewLines], tokens are split at line boundaries while preserving source
// references:
//
//	┌──────┬────────────┐
//	│String│MappingValue│
//	├──────┴────────────┤
//	│String             │
//	├───────────────────┤
//	│String             │
//	└───────────────────┘
//
// # Usage
//
// Create a [Lines] collection from tokens, then access individual lines:
//
//	tks := lexers.Tokenize(input)
//	lines := line.NewLines(tks)
//
//	for _, l := range lines {
//	    fmt.Printf("%d: %s\n", l.Number(), l.Content())
//	}
//
// Position-based token lookup uses [position.Position] values:
//
//	tk := lines.TokenAt(position.New(2, 4))  // Line 2, column 4.
//	ranges := lines.TokenPositionRanges(tk)  // Find all occurrences.
//
// # Rendering Metadata
//
// Each [Line] can carry metadata for rendering.
//
// [Annotations] add extra content above or below a line, which is useful for
// error messages, hints, or context.
//
// [Overlays] define column ranges with associated styles, primarily for
// highlighting.
//
// [Flag]s categorize lines for special handling (inserted, deleted,
// annotation-only).
//
// [Annotations] are positioned using [Above] or [Below] constants:
//
//	l.AddAnnotation(line.Annotation{
//	    Content:  "missing required field",
//	    Position: line.Below,
//	    Col:      4,  // Align with the error location.
//	})
//
// [Overlays] apply styles to column ranges.
// Use [Lines.AddOverlay] for multi-line ranges that need automatic splitting:
//
//	lines.AddOverlay(style.GenericError, errorRange)
//
// [Flag]s mark lines for diff rendering or annotation-only display:
//
//	l.Flag = line.FlagInserted  // Show with "+" prefix.
//	l.Flag = line.FlagDeleted   // Show with "-" prefix.
//
// # Round-Trip Support
//
// The [Lines.Tokens] method reconstructs the original token stream.
//
// Tokens that were split across lines are deduplicated using shared source
// pointers from the internal [tokens.Segment] representation.
//
// This enables modifications at the line level while preserving valid YAML
// output.
package line
