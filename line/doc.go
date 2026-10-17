// Package line provides line-by-line access to go-yaml tokens.
//
// # Token Splitting
//
// YAML tokens such as block scalars and multiline strings can span multiple
// lines. Operations such as diffing and printing are simpler with
// line-by-line access.
//
// [NewLines] cuts [token.Tokens] into one [Line] per source line, and the
// lines keep references to the original tokens.
//
// Consider this YAML input with a block scalar:
//
//	┌───────────────────────────┐
//	│foo: |-                    │
//	│  hello                    │
//	│  world                    │
//	└───────────────────────────┘
//
// The go-yaml lexer produces a normal [token.Tokens] stream. The block
// scalar content is a single token that spans multiple lines:
//
//	┌──────┬────────────┬───────┐
//	│String│MappingValue│Literal│
//	├──────┴────────────┴───────┤
//	│String                     │
//	│                           │
//	└───────────────────────────┘
//
// [NewLines] cuts the tokens at line boundaries, and every part keeps a
// reference to its source token.
//
//	┌──────┬────────────┬───────┐
//	│String│MappingValue│Literal│
//	├──────┴────────────┴───────┤
//	│String                     │
//	├───────────────────────────┤
//	│String                     │
//	└───────────────────────────┘
//
// # Lines
//
// [Lines] is the ordered collection of [Line] values a niceyaml Source
// holds. It carries the tokens split per line and does not know about YAML
// documents, parsing, or files. A Lines value can therefore describe
// content that is not a YAML document, such as a diff that interleaves
// lines from two revisions.
//
// Create a Lines value from tokens and range over it:
//
//	tks := tokens.Tokenize(input)
//	lines := line.NewLines(tks)
//
//	for _, l := range lines.All() {
//		fmt.Printf("%d: %s\n", l.Number(), l.Content())
//	}
//
// Position-based token lookup uses [position.Position] values:
//
//	tk := lines.TokenAt(position.New(2, 4))  // Line 2, column 4, both counted from 0.
//	ranges := lines.TokenRanges(tk)          // Every line with runes of the token.
//	content := lines.ContentRanges(tk)       // The same without surrounding spaces and tabs.
//
// [Lines.Tokens] reverses the split. A token the split cut across lines
// collapses back to one, and the result holds the lexer's original tokens
// in their original order.
//
// The lines never change after [NewLines] creates them, and code outside
// this package cannot add to, remove from, or reorder a Lines value. The
// finder and diff packages read one as it is, and any number of views share
// it. [Collect] builds a Lines value from lines taken from others, as a
// diff does to interleave two revisions. The result shares the lines with
// the values they came from.
//
// A [Line] exposes its tokens in two forms. [Line.Tokens] returns the
// per-line parts, whose positions describe the line. [Line.SourceTokens]
// returns the lexer's original tokens, so a token cut across several lines
// comes back from every line it touches. [Line.TokenAt] looks up the source
// token at a column, and [Line.TokenSpan] and [Line.ContentSpan] find the
// columns a token occupies on the line.
//
// [Line.Runes] iterates the line's runes by column. It yields the line ending
// as a single '\n' whether the source used LF or CRLF, so the columns it
// reports match [Line.Width].
//
// [Line.Kind] returns the [kind.Kind] the text of each token renders with.
// It reads the kind from the token's type and its neighbors. A string
// before a colon is a mapping key, and the name after an anchor takes the
// kind of the anchor. [TokenKind] reads the kind of a token of a whole
// stream the same way.
//
// Every token the package hands out belongs to the line. Treat them as
// read-only and call [token.Token.Clone] before modifying one.
//
// # Views
//
// A [View] is the unit the printer renders. It is a Lines value together
// with the decoration one rendering carries. [NewView] creates one over
// any Lines, and a niceyaml Source hands one out. The view shares the
// lines and owns the decoration. Creating a view builds an index of the
// lines but does not copy their content, and decorating one view does not
// change another:
//
//	view := line.NewView(lines)
//
// [Annotations] add extra content above or below a line, such as an error
// message, a hint, or context. An [Annotation] takes a [Placement] of
// [Above] or [Below], and its Kind names the style it renders in, as the
// Kind of an [Overlay] does. An annotation with the zero Kind renders in
// [kind.UIAnnotation]. Its Cols name the columns of the line it labels.
// Below the line, a renderer draws a caret under each cell of those
// columns, so a rune that takes two cells gets two carets. The content
// sits beside the carets:
//
//	view.Annotate(i, line.Annotation{
//	    Content:   "missing required field",
//	    Kind:      kind.TextError,
//	    Placement: line.Below,
//	    Cols:      position.NewSpan(4, 9), // The columns of the error location.
//	})
//
// [Overlays] define column ranges with associated styles. Their main use is
// highlighting. An [Overlay] replaces the style underneath it. With Blend
// set, it mixes with that style instead, so a search highlight keeps the
// token color it covers. [View.AddOverlay] adds overlays across a range of
// lines, and [View.BlendOverlay] adds them with Blend set:
//
//	view.AddOverlay(kind.GenericError, errorRange)
//	view.BlendOverlay(kind.GenericHighlight, matches...)
//
// An overlay does not draw a caret, so a range shows in plain output only
// when an annotation labels the same columns. For a range that spans
// several lines, add one annotation for each range that [Lines.SliceLines]
// splits it into.
//
// [View.Segments] cuts a line into the runs that one kind and one set of
// overlays style, each with its text. A renderer that writes HTML, or any
// other format, styles the line from the segments alone, as the printer
// does:
//
//	for seg := range view.Segments(i) {
//		fmt.Fprintf(w, `<span class=%q>%s</span>`, seg.Kind, html.EscapeString(seg.Text))
//	}
//
// [Flag] values categorize lines for special handling. A diff marks lines with
// [FlagInserted] and [FlagDeleted]:
//
//	view.SetFlag(i, line.FlagInserted) // Show with "+" prefix.
//	view.SetFlag(i, line.FlagDeleted)  // Show with "-" prefix.
//
// To render the same content two different ways, take two views of it.
// [View.Clone] copies the decoration of one view. [View.Slice] picks the
// lines of a few spans, decoration included, and [View.Hunks] picks the
// decorated lines with context around each. [View.Clip] cuts a long line
// the same way, to a window of columns around each mark on it. An error
// excerpt shows those hunks, clipped.
//
// [Lines.All] yields every line, and [View.All] yields every line the view
// holds. [Lines.Within] and [View.Within] yield the lines within some
// spans, and each yields an empty sequence when it gets zero spans. A view
// that [View.Slice] or [NewViewWithin] creates from an empty list of spans
// is empty. The [position.ContextSpans] of a search without matches are
// nil, so a slice by them is empty and does not render anything.
//
// The finder and diff packages read a [Sequence]. Both Lines and a [*View]
// implement it. A view yields the lines it holds with the indices they have
// in its content. A search of a slice therefore returns ranges that apply
// to the slice, and a diff of two slices compares the lines they hold.
//
// [View.String] renders a view as plain text, without escape sequences.
// Each line sits behind its number, with the annotations above it on rows
// of their own. Below the line, each run of columns that its annotations
// label gets a row of its own. The row holds carets under the run and the
// contents of the run's annotations beside them. A line the view clips
// renders its windows with "..." in place of the columns between them.
//
// The plain text of View.String is the rendering that
// [go.jacobcolvin.com/niceyaml.FormatError] prints for the excerpt of a
// bound error. It suits a log or a golden file. A printer renders the same
// view with styles.
package line
