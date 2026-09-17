// Package line provides line-by-line access to go-yaml tokens.
//
// # Token Splitting
//
// YAML tokens can span multiple lines (block scalars, multiline strings), but
// many operations (e.g. diffing, printing) are much simpler with line-by-line
// access.
//
// [NewLines] cuts [token.Tokens] into one [Line] per source line while
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
// [NewLines] cuts the tokens at line boundaries while every part keeps a
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
// # Lines
//
// [Lines] is the ordered collection of [Line] values that rendering
// utilities consume, and the view a niceyaml Source exposes. It carries the
// tokens split per line plus the overlays, annotations, and flags attached
// to each line, and nothing about YAML documents, parsing, or files. A Lines
// value may therefore describe content that is not a YAML document, such as
// a diff that interleaves lines from two revisions:
//
//	tks := tokens.Tokenize(input)
//	lines := line.NewLines(tks)
//
//	for _, l := range lines {
//		fmt.Printf("%d: %s\n", l.Number(), l.Content())
//	}
//
// Position-based token lookup uses [position.Position] values:
//
//	tk := lines.TokenAt(position.New(2, 4))  // Line 2, column 4.
//	ranges := lines.TokenRanges(tk)          // Every line with runes of the token.
//	content := lines.ContentRanges(tk)       // The same without surrounding spaces.
//
// [Lines.Tokens] reverses the split. Tokens that were cut across lines
// collapse back to one, and the result holds the lexer's original tokens in
// their original order.
//
// [View] is the interface over a Lines value that the printer, finder, and
// diff packages accept. A Lines collection holds pointers, so a line reached
// by indexing it or by ranging over [Lines.AllLines] keeps the metadata
// added to it:
//
//	for _, l := range lines.AllLines() {
//		if l.Flag() == line.FlagInserted {
//			l.AddAnnotation(line.Annotation{Content: "new", Placement: line.Below})
//		}
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
// covers. [Lines.AddOverlay] and [Lines.BlendOverlay] add overlays across a
// range of lines; the first replaces the style underneath and the second
// mixes with it:
//
//	lines.AddOverlay(style.GenericError, errorRange)
//	lines.BlendOverlay(style.GenericHighlight, matches...)
//
// [Flag] values categorize lines for special handling. A diff marks lines with
// [FlagInserted] and [FlagDeleted]:
//
//	lines[i].SetFlag(line.FlagInserted) // Show with "+" prefix.
//	lines[i].SetFlag(line.FlagDeleted)  // Show with "-" prefix.
//
// Rendering utilities mutate lines by adding overlays and annotations. Use
// [Lines.Clone] or [Line.Clone] to render the same content two different ways
// without the highlights interfering.
package line
