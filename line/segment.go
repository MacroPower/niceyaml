package line

import (
	"iter"
	"slices"

	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/style/kind"
	"go.jacobcolvin.com/niceyaml/tokens"
)

// Segment is a run of the content of one line that renders in one style.
// It holds the columns it covers, the text on them, the [kind.Kind] of the
// token the text belongs to, and the overlays of the view that cover it.
//
// Receive instances from [View.Segments].
type Segment struct {
	// Text is the content of the line on Cols, without the line ending.
	// It holds the source as written, control characters included, such
	// as a tab or an escape. The printer and [View.String] draw each
	// control character as one visible rune, as
	// [go.jacobcolvin.com/niceyaml/escape.Control] does. A renderer that
	// draws the text as the printer does passes it through escape.Control
	// before any escaping its format needs. The text then cannot move the
	// cursor of a terminal, and each control character takes the one cell
	// that [go.jacobcolvin.com/niceyaml/printer.ColWidth] and the caret
	// rows count for it.
	Text string
	// Kind is the kind of the token the text belongs to, as [Line.Kind]
	// reads it, or [kind.Text] for the whitespace a token carries before
	// or after its text. The printer draws a flagged line in the kind of
	// its flag instead, as [View.Segments] describes.
	Kind kind.Kind
	// Overlays are the overlays of the view that cover Cols, in the order
	// the view took them in. The printer applies them in that order over
	// the style of Kind, or over [kind.GenericInserted] or
	// [kind.GenericDeleted] on a line that carries [FlagInserted] or
	// [FlagDeleted]. Where two of them set the same property, the one the
	// caller added last thus wins. A blending overlay mixes its colors with
	// the style under it instead, as [Overlay.Blend] describes. The slice
	// belongs to the segment and holds copies of the view's overlays, so
	// changing it does not change the view or the other segments.
	Overlays Overlays
	// Cols are the columns of the line the segment covers, counted in
	// runes.
	Cols position.Span
}

// Segments returns an iterator over the segments of line i, in column
// order. Each column of the content falls in exactly one segment. Each
// segment is a run of text that one kind and one set of overlays style.
// Each token on the line is one segment for its text, in the kind
// [Line.Kind] gives it. The spaces and tabs a token carries before and
// after that text are segments of their own in [kind.Text]. The
// indentation before a value and the gap before a comment are two such
// segments.
//
// A segment splits where an overlay of the view starts or ends inside it,
// and [Segment.Overlays] holds the overlays that cover each piece.
// Adjacent segments with the same style stay separate, so a renderer that
// wants them joined joins them itself.
//
// A renderer of any kind reads the segments in place of the tokens and
// the overlays, so it styles the line without reading either. The
// printer applies the overlays of a segment in order, so the last one
// wins. In CSS the innermost element wins, so an HTML renderer nests the
// last overlay innermost, inside a span for the token kind:
//
//	for i := range view.All() {
//		for seg := range view.Segments(i) {
//			text := html.EscapeString(escape.Control(seg.Text))
//			for _, ov := range slices.Backward(seg.Overlays) {
//				class := string(ov.Kind)
//				if ov.Blend {
//					class += " blend"
//				}
//
//				text = fmt.Sprintf(`<span class=%q>%s</span>`, class, text)
//			}
//
//			k := seg.Kind
//			switch view.Flag(i) {
//			case line.FlagInserted:
//				k = kind.GenericInserted
//			case line.FlagDeleted:
//				k = kind.GenericDeleted
//			}
//
//			fmt.Fprintf(w, `<span class=%q>%s</span>`, k, text)
//		}
//
//		fmt.Fprintln(w)
//	}
//
// The snippet passes the text through escape.Control first, as
// [Segment.Text] describes. A rule for an overlay span sets only the
// properties it names. The token span thus shows through for the rest,
// as a replacing overlay does in the printer. A stylesheet gives a blend
// span a translucent background, so a search highlight keeps the token
// color under it.
//
// On a line that carries [FlagInserted] or [FlagDeleted], the printer
// draws each segment in [kind.GenericInserted] or [kind.GenericDeleted] in
// place of its Kind, with its overlays over that. The snippet reads
// [View.Flag] for each line and does the same. A renderer that writes the
// colors themselves, blends included, reads each segment with the style
// the printer draws it in from
// [go.jacobcolvin.com/niceyaml/printer.Printer.Segments].
//
// An overlay can start or end inside a grapheme cluster, such as between
// a letter and its combining mark, so a segment can hold part of a
// cluster. The printer draws each cluster whole, in the style of the
// segment that holds its first rune. A renderer that matches the printer
// styles the whole cluster by its first rune.
//
// Segments covers the whole line, whatever [View.Clip] set. A renderer
// that shows a clipped line in part keeps the columns [View.Windows]
// returns.
//
// Segments yields an empty sequence for an empty line. It panics if i is
// outside the content.
func (v *View) Segments(i int) iter.Seq[Segment] {
	ln := v.lines.lines[i]
	overlays := v.Overlays(i)

	return func(yield func(Segment) bool) {
		runs := newOverlayRuns(overlays)
		col := 0

		for j, seg := range ln.segments {
			rest := tokens.TrimLineEnding(seg.Part().Origin)
			sp := seg.ContentSpan()

			// The separator is the whitespace the token carries before its
			// text, whether that text is a plain scalar, a quoted string,
			// an anchor, a comment, or the continuation of a multiline
			// scalar. The trailer is the whitespace it carries after that
			// text, which the lexer hands to the token preceding a comment.
			// Neither is part of the text, so both are Text. The content
			// span of the segment covers the text between them. A token of
			// nothing but whitespace is all separator.
			pieces := [...]struct {
				kind kind.Kind
				end  int
			}{
				{kind.Text, col + sp.Start},
				{ln.Kind(j), col + sp.End},
				{kind.Text, col + seg.Width()},
			}

			for _, piece := range pieces {
				for col < piece.end {
					end, covering := runs.next(col, piece.end)
					n := runeBytes(rest, end-col)

					s := Segment{
						Cols:     position.NewSpan(col, end),
						Text:     rest[:n],
						Kind:     piece.kind,
						Overlays: covering,
					}

					rest = rest[n:]
					col = end

					if !yield(s) {
						return
					}
				}
			}
		}
	}
}

// overlayRuns cuts the columns of one line into runs where its overlays
// start or end, so every column of a run has the same overlays over it.
// Each call to [overlayRuns.next] asks for columns at or after the ones
// the previous call asked for. An overlayRuns therefore walks the edges a
// single time, from left to right.
//
// Create instances with [newOverlayRuns].
type overlayRuns struct {
	// The columns where an overlay starts or ends, in ascending order and
	// without repeats.
	edges []int
	// The overlays over each run, in the order the view took them in.
	// Entry k holds the overlays of the run after the first k edges, so
	// covers[0] holds those of the run before the first edge. The slice is
	// nil when the line lacks overlays.
	covers []Overlays
	// The run that holds the column the last call to next asked for.
	k int
}

// newOverlayRuns creates a new [overlayRuns] over the given overlays. An
// overlay that does not cover any column, one with its end at or before
// its start, still cuts the line at both edges.
func newOverlayRuns(overlays Overlays) overlayRuns {
	if len(overlays) == 0 {
		return overlayRuns{}
	}

	edges := make([]int, 0, 2*len(overlays))
	for _, o := range overlays {
		edges = append(edges, o.Cols.Start, o.Cols.End)
	}

	slices.Sort(edges)

	edges = slices.Compact(edges)

	covers := make([]Overlays, len(edges)+1)

	for _, o := range overlays {
		if o.Cols.Start >= o.Cols.End {
			continue
		}

		first, _ := slices.BinarySearch(edges, o.Cols.Start)
		last, _ := slices.BinarySearch(edges, o.Cols.End)

		// Start is edges[first] and End is edges[last], so the overlay
		// covers the runs from first+1 through last.
		for k := first + 1; k <= last; k++ {
			covers[k] = append(covers[k], o)
		}
	}

	return overlayRuns{edges: edges, covers: covers}
}

// next returns where the run that holds col ends, at the next edge after
// col or at end, whichever comes first. It also returns a new slice of the
// overlays over the run, or nil when the run lacks overlays.
func (r *overlayRuns) next(col, end int) (int, Overlays) {
	for r.k < len(r.edges) && r.edges[r.k] <= col {
		r.k++
	}

	if r.k < len(r.edges) && r.edges[r.k] < end {
		end = r.edges[r.k]
	}

	if r.covers == nil {
		return end, nil
	}

	return end, slices.Clone(r.covers[r.k])
}

// runeBytes returns the length in bytes of the first n runes of s, or of
// all of s if it holds fewer.
func runeBytes(s string, n int) int {
	for i := range s {
		if n == 0 {
			return i
		}

		n--
	}

	return len(s)
}
