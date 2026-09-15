// Package line provides line-by-line access to go-yaml tokens.
//
// # Token Splitting
//
// YAML tokens can span multiple lines (block scalars, multiline strings), but
// many operations (e.g. diffing, printing) are much simpler with line-by-line
// access.
//
// [Split] cuts [token.Tokens] into one [Line] per source line while
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
// [Split] cuts the tokens at line boundaries while every part keeps a
// reference to its source token:
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
// Most callers receive [Line] values from a niceyaml Lines view or Source
// rather than calling [Split] directly:
//
//	for _, l := range niceyaml.NewLines(tks) {
//	    fmt.Printf("%d: %s\n", l.Number(), l.Content())
//	}
//
// A [Line] exposes its tokens in two forms. [Line.Tokens] returns the
// per-line parts, whose positions describe this line. [Line.SourceTokens]
// returns the lexer's original tokens, so a token cut across several lines
// comes back from every line it touches. [Line.TokenAt] looks up the source
// token at a column, and [Line.TokenSpan] and [Line.ContentSpan] find the
// columns a token occupies on the line.
//
// [Line.Runes] iterates the line's runes by column. It yields the line ending
// as a single '\n' whether the source used LF or CRLF, so the columns it
// reports match [Line.Width].
//
// Every token the package hands out is shared with the line. Treat them as
// read-only and call [token.Token.Clone] before modifying one.
//
// # Rendering Metadata
//
// Each [Line] can carry metadata for rendering.
//
// [Annotations] add extra content above or below a line, which is useful for
// error messages, hints, or context. An [Annotation] is positioned with
// [Above] or [Below]:
//
//	l.AddAnnotation(line.Annotation{
//	    Content:   "missing required field",
//	    Placement: line.Below,
//	    Col:       4, // Align with the error location.
//	})
//
// [Overlays] define column ranges with associated styles, primarily for
// highlighting. An [Overlay] either replaces the style underneath it or, with
// Blend set, mixes with it so a search highlight keeps the token color it
// covers.
//
// [Flag] values categorize lines for special handling. A diff marks lines with
// [FlagInserted] and [FlagDeleted], and [FlagAnnotation] marks lines that hold
// only an annotation and no line number:
//
//	l.Flag = line.FlagInserted // Show with "+" prefix.
//	l.Flag = line.FlagDeleted  // Show with "-" prefix.
//
// Rendering utilities mutate lines by adding overlays and annotations. Use
// [Line.Clone] to render the same content two different ways without the
// highlights interfering.
package line
