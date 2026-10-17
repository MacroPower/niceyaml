package line

import (
	"cmp"
	"fmt"
	"iter"
	"maps"
	"slices"
	"strconv"
	"strings"

	"go.jacobcolvin.com/niceyaml/internal/caret"
	"go.jacobcolvin.com/niceyaml/internal/cells"
	"go.jacobcolvin.com/niceyaml/internal/clip"
	"go.jacobcolvin.com/niceyaml/internal/escape"
	"go.jacobcolvin.com/niceyaml/internal/severity"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

// View is [Lines] content together with the decoration one rendering of it
// carries: a [Flag], [Overlays], and [Annotations] per line. The printer
// renders a View, and every utility that marks content, such as a diff or
// a bound error, hands one out.
//
// A View shares its lines with every other View over the same content and
// owns its decoration alone. Creating one builds an index of the lines it
// holds but does not copy their content, and decorating one View does not
// change another. Two renderings of one document, such as search
// highlights and error marks, are two Views over the same [Lines].
//
// Every index and every [position.Range] that a View takes or yields is in
// the coordinates of its content, the [Lines] it is over. Line i is the
// line that [View.Line] returns for i. A View holds each line of its
// content at most once, in content order. A View from [View.Slice] holds
// some of those lines and keeps their indices. A range from a search of
// the content or from the path of a document applies to a slice as it
// applies to the whole. Slicing before or after decorating renders the
// same.
//
// [View.All] yields the lines the View holds with their indices, and
// [View.Contains] reports whether it holds a line. [View.Index] finds the
// index of a line it holds, and [View.Count] returns the number of lines it
// holds. A View from [View.Clip] shows a long line in part, and the columns
// it takes and yields are still those of the content.
//
// Index-taking methods panic when the index is outside the content, as
// indexing a slice does. A View keeps decoration on a line it does not
// hold, and that decoration never renders. A View is not safe for
// concurrent mutation. Decorate from one goroutine at a time, and do not
// decorate while another goroutine renders.
//
// Create instances with [NewView] or [NewViewWithin]. The zero value is an
// empty view.
type View struct {
	// The decoration of the lines that carry some, keyed by index. An
	// undecorated line does not have an entry. A slice or a clone
	// therefore costs the lines it holds and the decoration it copies,
	// whatever the length of the content.
	flags       map[int]Flag
	overlays    map[int]Overlays
	annotations map[int]Annotations
	lines       Lines
	// The index in lines of each line the View holds, in ascending order.
	held []int
	// The number of columns each window of a clipped line holds, as
	// [View.Clip] sets it, or 0 when the View shows every line whole.
	clip int
}

// NewView creates a new [*View] over lines without decoration. The view
// holds every line, in content order. [NewViewWithin] creates a view that
// holds the lines of some spans, such as one document of a file that
// holds several.
func NewView(lines Lines) *View {
	v := &View{lines: lines, held: make([]int, 0, lines.Len())}

	for i := range lines.All() {
		v.held = append(v.held, i)
	}

	return v
}

// NewViewWithin creates a new [*View] over lines without decoration. The
// view holds the lines within any of the given spans, in content order and
// each once, as [Lines.Within] yields them. Without spans, the view is
// empty. The view holds the lines that a [View.Slice] of a [NewView] by
// the same spans holds. The index it builds grows with those lines alone.
func NewViewWithin(lines Lines, spans ...position.Span) *View {
	v := &View{lines: lines}

	for i := range lines.Within(spans...) {
		v.held = append(v.held, i)
	}

	return v
}

// Line returns line i of the content of the [View], whether or not the
// View holds it. A position in the coordinates of the content therefore
// reaches its line. Line panics when i is outside the content, as the
// other index-taking methods do. The text of the lines the View holds
// comes from [View.Held].
func (v *View) Line(i int) *Line {
	return v.lines.lines[i]
}

// Over reports whether lines is the content of the [View]. When it is, an
// index or a [position.Range] in the coordinates of lines applies to the
// View. A View is over the [Lines] that [NewView] or [NewViewWithin] took,
// and so is every View that [View.Slice], [View.Clone], [View.Hunks], or
// [View.Clip] returns from it.
//
// A Lines value and its copies are one content. Two values that [Collect]
// created are two contents, even when they hold the same lines. Every
// empty Lines value is the zero Lines, so two empty values are one
// content. A nil View and the zero View are over the zero Lines.
func (v *View) Over(lines Lines) bool {
	var content Lines

	if v != nil {
		content = v.lines
	}

	return content.idx == lines.idx
}

// Held returns the lines the [View] holds as new [Lines], in the order
// [View.All] yields them. The result shares the lines by pointer with the
// content, as [Collect] does. It has the methods a View lacks, such as
// [Lines.Content], [Lines.Tokens], [Lines.Runes], and [Lines.TokenAt].
// With them, a caller reaches the text of the lines a slice holds, such as
// one document of a file that holds several:
//
//	text := doc.View().Held().Content()
//
// Each line keeps the number it has in the file, but the indices of the
// result start at zero. Line i of the result is the i-th held line, not
// line i of the content. An index or a range computed on the result
// therefore does not apply to the View. A diff or a search of the held
// lines takes the View itself, as a [Sequence], and works in the
// coordinates of the content. Held returns the zero Lines for a nil View.
func (v *View) Held() Lines {
	if v == nil {
		return Lines{}
	}

	ls := make([]*Line, 0, len(v.held))

	for _, l := range v.All() {
		ls = append(ls, l)
	}

	return newLines(ls)
}

// Count returns the number of lines the [View] holds, which is the number
// [View.All] yields and [View.Held] returns. The content can hold more
// lines, and [View.Line] reaches them by index.
func (v *View) Count() int {
	if v == nil {
		return 0
	}

	return len(v.held)
}

// Contains reports whether the [View] holds line i of its content. It
// reports false for a nil View and for an index outside the content.
func (v *View) Contains(i int) bool {
	if v == nil {
		return false
	}

	_, ok := slices.BinarySearch(v.held, i)

	return ok
}

// Index returns the index at which the [View] holds l, and true. Every
// View over the same content shares its lines by pointer. A decorator that
// knows a line of a [Lines] value therefore finds it without knowing what
// built the view. The view can be a slice of that content or a diff that
// interleaves it with another revision. For a line the View does not hold,
// such as one from other content, Index returns false.
//
// A unified diff shows an unchanged line of the before revision as the
// line of the after revision, so Index does not find the before line
// there. [go.jacobcolvin.com/niceyaml/diff.Result.UnifiedRow] maps every
// line of either revision to its row.
func (v *View) Index(l *Line) (int, bool) {
	if v == nil || l == nil {
		return 0, false
	}

	// Walk every index that holds l in ascending order, so a View that
	// drops the first occurrence finds a later one.
	i, ok := v.lines.firstIndex(l)
	for ok {
		if v.Contains(i) {
			return i, true
		}

		i, ok = v.lines.nextIndex(i)
	}

	return 0, false
}

// All returns an iterator over every line the [View] holds, in content
// order. Each iteration yields the index of the line in the content and
// the [*Line]. The index reaches the line's decoration through
// [View.Flag], [View.Overlays], and [View.Annotations]. [View.Within]
// yields the lines of some spans.
func (v *View) All() iter.Seq2[int, *Line] {
	return func(yield func(int, *Line) bool) {
		if v == nil {
			return
		}

		for _, i := range v.held {
			if !yield(i, v.lines.lines[i]) {
				return
			}
		}
	}
}

// Within returns an iterator over the lines the [View] holds within any of
// the given spans. It yields them in content order and yields each line
// one time, whatever order the spans come in and however they overlap.
// Without spans, the iterator is empty, so the nil result of
// [position.ContextSpans] does not select any line. [View.All] yields every
// line the View holds.
//
// Each iteration yields the index of the line in the content and the
// [*Line], as View.All does. A span that reaches outside the content
// selects only the lines inside the content that the View holds.
func (v *View) Within(spans ...position.Span) iter.Seq2[int, *Line] {
	return func(yield func(int, *Line) bool) {
		if v == nil {
			return
		}

		// The merge sorts the spans and makes them disjoint, so walking
		// the held lines of each in turn yields every selected line once,
		// in content order, without testing each held line against every
		// span.
		for _, s := range mergeSpans(spans, v.lines.Len()) {
			j, _ := slices.BinarySearch(v.held, s.Start)

			for ; j < len(v.held) && v.held[j] < s.End; j++ {
				i := v.held[j]
				if !yield(i, v.lines.lines[i]) {
					return
				}
			}
		}
	}
}

// mergeSpans clamps spans to [0, n), sorts them by start, and merges each
// group of spans that overlap or touch into one. Every index of
// [0, n) that some span contains lies in exactly one span of the result.
func mergeSpans(spans []position.Span, n int) position.Spans {
	clamped := position.Spans(spans).Clamp(0, n)
	slices.SortFunc(clamped, func(a, b position.Span) int {
		return cmp.Compare(a.Start, b.Start)
	})

	merged := clamped[:0]

	for _, s := range clamped {
		if last := len(merged) - 1; last >= 0 && s.Start <= merged[last].End {
			merged[last].End = max(merged[last].End, s.End)

			continue
		}

		merged = append(merged, s)
	}

	return merged
}

// Flag returns the [Flag] of line i, or [FlagDefault] when line i does not
// have one.
func (v *View) Flag(i int) Flag {
	_ = v.lines.lines[i]

	return v.flags[i]
}

// SetFlag sets the [Flag] of line i.
func (v *View) SetFlag(i int, f Flag) {
	_ = v.lines.lines[i]

	if f == FlagDefault {
		delete(v.flags, i)

		return
	}

	if v.flags == nil {
		v.flags = make(map[int]Flag)
	}

	v.flags[i] = f
}

// Annotations returns the [Annotation] values on line i, in the order the
// caller added them. The slice belongs to the view, so treat it as
// read-only and add to it with [View.Annotate].
func (v *View) Annotations(i int) Annotations {
	_ = v.lines.lines[i]

	return v.annotations[i]
}

// Annotate adds the given [Annotation] values to line i.
func (v *View) Annotate(i int, ann ...Annotation) {
	_ = v.lines.lines[i]

	if v.annotations == nil {
		v.annotations = make(map[int]Annotations)
	}

	v.annotations[i] = append(v.annotations[i], ann...)
}

// Overlays returns the [Overlay] values on line i, in the order the caller
// added them. The slice belongs to the view, so treat it as read-only and
// add to it with [View.AddOverlay], [View.BlendOverlay], or
// [View.AddLineOverlay].
func (v *View) Overlays(i int) Overlays {
	_ = v.lines.lines[i]

	return v.overlays[i]
}

// AddLineOverlay adds the given [Overlay] values to line i as given.
// [View.AddOverlay] and [View.BlendOverlay] clamp a range to the lines it
// covers before they add it, but AddLineOverlay does not clamp an overlay.
func (v *View) AddLineOverlay(i int, o ...Overlay) {
	_ = v.lines.lines[i]

	if v.overlays == nil {
		v.overlays = make(map[int]Overlays)
	}

	v.overlays[i] = append(v.overlays[i], o...)
}

// AddOverlay adds an overlay with the given style to the specified ranges,
// in the coordinates of the content. The overlay replaces the style
// underneath it. Use [View.BlendOverlay] to mix with it instead.
//
// It splits each range into one overlay per line with [Lines.SliceLines].
// SliceLines clamps the columns to the width of the line and skips lines
// outside the content. A caller can therefore safely apply a range
// computed against longer content. AddOverlay adds an overlay to a line
// only where the range covers at least one of its columns. An overlay on
// a line the View does not hold never renders.
func (v *View) AddOverlay(s kind.Kind, ranges ...position.Range) {
	for _, r := range ranges {
		v.addOverlayRange(s, false, r)
	}
}

// BlendOverlay adds an overlay like [View.AddOverlay], but one that blends
// with the style underneath it. A search highlight that BlendOverlay adds
// keeps the token or diff color of the text it covers.
func (v *View) BlendOverlay(s kind.Kind, ranges ...position.Range) {
	for _, r := range ranges {
		v.addOverlayRange(s, true, r)
	}
}

// addOverlayRange adds a single overlay range, one overlay per line r
// covers within the content.
func (v *View) addOverlayRange(s kind.Kind, blend bool, r position.Range) {
	for _, lr := range v.lines.SliceLines(r) {
		v.AddLineOverlay(lr.Start.Line, Overlay{
			Cols:  position.NewSpan(lr.Start.Col, lr.End.Col),
			Kind:  s,
			Blend: blend,
		})
	}
}

// Clone returns a copy of the [View] with its own decoration. The copy
// shares the lines with the original and holds the same ones. Its cost is
// one copy of the index of the lines it holds and one copy of the flags,
// overlays, and annotations. Decorating either View does not change the
// other.
func (v *View) Clone() *View {
	if v == nil {
		return nil
	}

	c := &View{lines: v.lines, held: slices.Clone(v.held), flags: maps.Clone(v.flags), clip: v.clip}

	for i, o := range v.overlays {
		c.AddLineOverlay(i, o...)
	}

	for i, a := range v.annotations {
		c.Annotate(i, a...)
	}

	return c
}

// Slice returns a new [*View] over the same content. The result holds the
// lines that the receiver holds within any of the given spans, in content
// order and each once, as [View.Within] yields them. Each line keeps its
// index and its decoration. The result owns its decoration. It is the view
// a caller renders to show part of a document, such as the hunks around an
// error. A range or an index that applies to the receiver applies to the
// result, and slicing an already sliced view narrows it further.
//
// Without spans, the result is empty, so a slice by the nil result of
// [position.ContextSpans] does not render any line. [View.Clone] holds
// every line the receiver holds.
func (v *View) Slice(spans ...position.Span) *View {
	if v == nil {
		return &View{}
	}

	out := &View{lines: v.lines, clip: v.clip}

	for i := range v.Within(spans...) {
		out.held = append(out.held, i)

		if f := v.Flag(i); f != FlagDefault {
			out.SetFlag(i, f)
		}

		if o := v.Overlays(i); len(o) > 0 {
			out.AddLineOverlay(i, o...)
		}

		if a := v.Annotations(i); len(a) > 0 {
			out.Annotate(i, a...)
		}
	}

	return out
}

// Hunks returns a [*View] that holds the decorated lines of the View,
// with context lines of unchanged content on either side of each one. A
// caller that marks a document thus shows the marked parts alone. A
// decorated line carries a [Flag] other than [FlagDefault], an [Overlay],
// or an [Annotation] of a kind other than [kind.UISeparator]. Decorated
// lines whose context windows overlap or touch share a hunk, and distant
// ones become separate hunks. A negative context shows the decorated lines
// alone, as 0 does. A View without decorated lines yields an empty View,
// as a nil View does.
//
// A "..." annotation of kind [kind.UISeparator] sits above the first line
// after each gap between two lines the result holds. The separator
// therefore marks the gap between two hunks and any gap the View itself
// skips inside one. The lines that the result skips before the first hunk
// do not get a separator. The result drops every separator the View
// carries before it adds its own. The hunks of hunks therefore match the
// hunks of the original View at the same or a smaller context.
//
// The result is a [View.Slice]. Its lines keep their indices, a range from
// the content applies to it, and decoration that a caller adds later lands
// on the lines it holds. Context lines count in the content, and the hunks
// hold the context lines that the View holds. The hunks of a slice of a
// document therefore lie within the slice.
//
// An error excerpt is the hunks of a view that the error marked. A caller
// that marks several errors, or search matches, on one view takes the
// hunks of that view the same way:
//
//	view := source.View()
//	niceyaml.Annotate(err, view)
//	lipgloss.Println(p.Print(view.Hunks(2)))
func (v *View) Hunks(context int) *View {
	if v == nil {
		return &View{}
	}

	var marked []int

	for i := range v.All() {
		if v.decorated(i) {
			marked = append(marked, i)
		}
	}

	hunks := v.Slice(position.ContextSpans(marked, context, v.lines.Len())...)

	// A View that is itself hunks carries the separators of its own gaps,
	// which need not match the gaps of the result, so the result drops
	// them and adds its own. The slice owns its annotations, so the
	// deletion reaches nothing in the View.
	for i, anns := range hunks.annotations {
		kept := slices.DeleteFunc(anns, isSeparator)
		if len(kept) == 0 {
			delete(hunks.annotations, i)

			continue
		}

		hunks.annotations[i] = kept
	}

	// The separator goes above every line the hunks hold that follows a
	// line they skip. That is the first line of each hunk after the
	// first, and the first line after a gap the View itself skips inside
	// a hunk. It sits ahead of the annotations the line already carries,
	// since the printer renders them in the order their kinds first
	// appear and the separator marks the top of what follows the gap.
	separator := Annotation{
		Content:   "...",
		Kind:      kind.UISeparator,
		Placement: Above,
	}

	for j := 1; j < len(hunks.held); j++ {
		i := hunks.held[j]
		if i == hunks.held[j-1]+1 {
			continue
		}

		hunks.Annotate(i, separator)

		anns := hunks.annotations[i]
		copy(anns[1:], anns[:len(anns)-1])

		anns[0] = separator
	}

	return hunks
}

// decorated reports whether line i carries a flag, an overlay, or an
// annotation of a kind other than [kind.UISeparator], the kind of the
// separators that [View.Hunks] adds.
func (v *View) decorated(i int) bool {
	return v.Flag(i) != FlagDefault || len(v.Overlays(i)) > 0 ||
		slices.ContainsFunc(v.Annotations(i), func(a Annotation) bool { return !isSeparator(a) })
}

// isSeparator reports whether a is an annotation of kind
// [kind.UISeparator], such as the one [View.Hunks] adds above a gap.
func isSeparator(a Annotation) bool {
	return a.Kind == kind.UISeparator
}

// Clip returns a copy of the [View] that shows part of each line longer
// than cols columns. The copy shows a window of cols columns around each
// mark on such a line, with "..." in place of each run of columns it
// leaves out. Clip cuts columns as [View.Hunks] cuts lines. A document
// minified onto one line, or a long encoded value on the line an error
// marks, thus renders as a row of bounded length.
//
// A mark is the first column of an [Overlay], or of the [Annotation.Cols]
// of an annotation of a kind other than [kind.UISeparator]. A line without
// a mark keeps its first cols columns. A window centers on its mark and
// moves to stay inside the line. An overlay longer than its window
// therefore shows the columns from its start that the window holds.
//
// The "..." marker takes three columns, so windows that overlap or lie
// three columns or fewer apart join into one. For the same reason, a
// window three columns or fewer from either end of the line reaches that
// end. A window keeps each grapheme cluster whole, so it grows to hold the
// rest of a cluster at either edge.
//
// The copy holds the lines the View holds with their indices and their
// decoration, so a range from the content still applies to it. It picks
// the windows when it renders, from the decoration it carries then. A mark
// that a caller adds after the clip therefore gets a window too.
// [View.Windows] returns the windows of a line, and [View.String] and the
// printer render them. [View.Slice], [View.Hunks], and [View.Clone] keep
// the clip. A cols of zero or less shows every line whole, so it undoes a
// clip.
//
// An error excerpt clips its hunks this way, and a caller that marks a
// view of its own does the same:
//
//	lipgloss.Println(p.Print(view.Hunks(2).Clip(120)))
func (v *View) Clip(cols int) *View {
	c := v.Clone()
	if c == nil {
		c = &View{}
	}

	c.clip = max(0, cols)

	return c
}

// Windows returns the runs of columns of line i that the [View] shows,
// in order, as [View.Clip] picks them. It returns nil for a line that the
// View shows whole, such as every line of a View without a clip and every
// line that fits within the clip. The spans count columns of the content.
// A renderer shows "..." for each run of columns between two of them,
// before the first, and after the last.
func (v *View) Windows(i int) position.Spans {
	ln := v.lines.lines[i]

	if v.clip <= 0 {
		return nil
	}

	width := ln.Width()
	if width <= v.clip {
		return nil
	}

	marks := make([]int, 0, len(v.overlays[i])+len(v.annotations[i]))

	for _, o := range v.overlays[i] {
		marks = append(marks, o.Cols.Start)
	}

	for _, a := range v.annotations[i] {
		if !isSeparator(a) {
			marks = append(marks, a.Cols.Start)
		}
	}

	if len(marks) == 0 {
		marks = append(marks, 0)
	}

	slices.Sort(marks)

	row := cells.NewRow(ln.Content())

	var windows position.Spans

	for _, mark := range marks {
		// The window centers on the mark and moves to stay inside the
		// line, which is longer than the window. A mark outside the line
		// counts as one at the end it lies past.
		mark = min(max(0, mark), width)
		start := min(max(0, mark-v.clip/2), width-v.clip)
		w := position.NewSpan(row.Start(start), row.Next(start+v.clip))

		// The marks ascend, so the windows do too, and a window joins
		// the one before it when the ellipsis between them would take as
		// many columns as the cut saves.
		if last := len(windows) - 1; last >= 0 && w.Start-windows[last].End <= clip.EllipsisCols {
			windows[last].End = max(windows[last].End, w.End)

			continue
		}

		windows = append(windows, w)
	}

	if windows[0].Start <= clip.EllipsisCols {
		windows[0].Start = 0
	}

	if last := &windows[len(windows)-1]; width-last.End <= clip.EllipsisCols {
		last.End = width
	}

	if len(windows) == 1 && windows[0] == position.NewSpan(0, width) {
		return nil
	}

	return windows
}

// shownRow is one line as [View.String] renders it: the text of its row
// and the annotations of the line in the columns of that text. They are
// the content and the annotations of the line itself unless the View
// clips the line.
type shownRow struct {
	content     string
	annotations Annotations
	// The number of columns of content.
	width int
}

// shown returns line i of the View, which is ln, as [View.String] renders
// it. For a line the View clips, the content holds the windows
// [View.Windows] returns with "..." in place of the columns between them.
// Each annotation labels the run of that text that shows its columns.
func (v *View) shown(i int, ln *Line) shownRow {
	windows := v.Windows(i)
	if windows == nil {
		return shownRow{
			content:     ln.Content(),
			annotations: v.Annotations(i),
			width:       ln.Width(),
		}
	}

	m := clip.New(ln.Width(), windows)
	row := shownRow{content: m.Content(ln.Content()), width: m.Width()}

	for _, a := range v.Annotations(i) {
		a.Cols = m.Span(a.Cols)
		row.annotations = append(row.annotations, a)
	}

	return row
}

// String renders the [View] as plain text: each line behind its number,
// and the annotations above and below it on rows of their own. String
// does not render flags. The number column is at least four wide and
// grows to fit the largest number in the view, so every row lines up.
// An empty view renders as "". A line feed separates the rows, and the
// last row does not end with one. A program that writes the text into a
// file or a fenced block adds that line feed itself.
//
// Above a line, each kind of annotation with content gets one row, in
// the order the kinds first appear, as [Annotations.ByKind] groups them.
// The row holds their contents joined with "; " in column order, from the
// first column they start at.
//
// Below a line, each run of columns that the [Annotation.Cols] of its
// annotations label gets a row of its own. The row holds a caret under
// each column of the run and the contents of its annotations after the
// carets. Annotations of one kind that label the same columns share a
// row, with their contents joined with "; ". An annotation without
// content draws its carets alone. A run that does not cover any column of
// the content, such as an empty one, gets a single caret at its start.
//
// Below a line, the rows of [kind.TextError] annotations come first, then
// those of [kind.TextWarn], then those of the other kinds in the order
// the kinds first appear. The rows of one kind come in the order their
// runs start, and runs that start at one column come shorter first.
// Overlays do not render a row, so a line with overlays but without an
// annotation below it does not render a caret.
//
// Annotations that start more than [MaxColPastEnd] columns past the end
// of the content start at that bound, as the printer starts them.
// Each control character renders as
// [go.jacobcolvin.com/niceyaml/escape.Control] draws it. A rune that takes
// two cells in a terminal gets two carets, so the carets stay under the runes
// they mark in a fixed-width font. The output does not hold escape
// sequences, so it goes into a log or a golden file unchanged.
// [go.jacobcolvin.com/niceyaml.FormatError] prints the excerpt of a bound
// error this way.
//
// A printer renders the same view with styles.
// [go.jacobcolvin.com/niceyaml/printer.DefaultAnnotation] lays out the
// rows below a line as String does, and the printer wraps the line and
// those rows to its width.
//
// A line that [View.Clip] clips renders the windows [View.Windows]
// returns, with "..." in place of each run of columns it leaves out. The
// carets and the annotations of the line sit under the columns of that
// row. An annotation that starts at a column the row leaves out starts at
// the "..." that stands for the column.
func (v *View) String() string {
	width := 4
	for _, ln := range v.All() {
		width = max(width, len(strconv.Itoa(ln.Number())))
	}

	blank := strings.Repeat(" ", width) + " | "

	var rows []string

	for i, ln := range v.All() {
		row := v.shown(i, ln)
		anns := row.annotations

		// A line without annotations pads no row, so it skips measuring
		// the cells of its content.
		var cellRow cells.Row

		if len(anns) > 0 {
			cellRow = cells.NewRow(row.content)
		}

		// Each kind of annotation above the line gets a row of its own,
		// in the order the kinds first appear, as the printer renders
		// them. An annotation without content adds nothing to its row,
		// and a kind with no content adds no row. The row starts at the
		// column of its annotations as the content row renders it, so it
		// lines up on a line holding wide or control characters.
		for _, group := range anns.Filter(Above).ByKind() {
			kept := group.WithContent()
			if len(kept) == 0 {
				continue
			}

			col := annotationCol(kept.Start(), row.width)
			padding := strings.Repeat(" ", cellRow.Width(col))
			rows = append(rows, blank+padding+escape.Control(strings.Join(kept.Contents(), "; ")))
		}

		rows = append(rows, contentRow(ln.Number(), row.content, width))

		// Each run of columns the annotations below the line label gets a
		// row of its own, as the printer lays them out.
		below := anns.Filter(Below).ByKind()

		kinds := make([]kind.Kind, 0, len(below))
		for _, group := range below {
			kinds = append(kinds, group[0].RenderKind())
		}

		for _, gi := range severity.Order(kinds) {
			for _, r := range caret.Rows(row.content, caretLabels(below[gi], row.width), nil) {
				padding := strings.Repeat(" ", cellRow.Width(r.Col))
				rows = append(rows, blank+padding+r.Marker+r.Text)
			}
		}
	}

	return strings.Join(rows, "\n")
}

// caretLabels returns the labels that mark anns below a line whose content
// is width columns wide. Each label starts at most [MaxColPastEnd] columns
// past the end of the content.
func caretLabels(anns Annotations, width int) []caret.Label {
	labels := make([]caret.Label, 0, len(anns))

	for _, a := range anns {
		cols := a.Cols
		cols.Start = annotationCol(cols.Start, width)

		labels = append(labels, caret.Label{Text: a.Content, Cols: cols})
	}

	return labels
}

// contentRow returns the row that renders content, the text of a line,
// behind number, the number of the line. The number sits right-aligned in
// a gutter that is width cells wide. A line without a number, such as the
// placeholder a diff puts opposite an inserted or deleted line, gets a
// blank gutter, as the printer gives it one.
func contentRow(number int, content string, width int) string {
	gutter := ""
	if number > 0 {
		gutter = strconv.Itoa(number)
	}

	return fmt.Sprintf("%*s | %s", width, gutter, escape.Control(content))
}
