// Package line provides abstractions for line-by-line go-yaml Token processing.
//
// # Token Splitting
//
// YAML tokens can span multiple lines (block scalars, multiline strings), but
// many operations (e.g. diffing, printing) are much simpler with line-by-line
// access.
//
// This package organizes [token.Tokens] into [Lines] while preserving
// references to the original [token.Tokens].
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
// # Rendering View
//
// [Lines] is the view that rendering utilities consume. It implements the
// iteration methods those utilities need ([Lines.AllLines], [Lines.AllRunes],
// [Lines.Len], [Lines.IsEmpty]) and carries per-line rendering metadata, but
// it knows nothing about parsing or YAML documents. A [Lines] value can
// therefore describe content that is not a document, such as a diff.
//
// Rendering utilities mutate a view by adding overlays and annotations. Use
// [Lines.Clone] to render the same content two different ways without the
// highlights interfering.
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
//	ranges := lines.TokenPositionRangesFromToken(tk)  // Find all occurrences.
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
// [Flag] values categorize lines for special handling (inserted, deleted,
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
// [Flag] values mark lines for diff rendering or annotation-only display:
//
//	l.Flag = line.FlagInserted  // Show with "+" prefix.
//	l.Flag = line.FlagDeleted   // Show with "-" prefix.
//
// # Round-Trip Support
//
// The [Lines.Tokens] method reconstructs the original token stream.
//
// Tokens that were split across lines are deduplicated using shared source
// pointers from the internal [tokens.Segment] representation, so the result
// holds the lexer's original tokens in their original order.
//
// Every token the package hands out, from [Lines.Tokens], [Lines.TokenAt],
// [Line.Tokens], or [Line.Token], is shared with the lines. Treat them as
// read-only and call [token.Token.Clone] before modifying one.
package line
