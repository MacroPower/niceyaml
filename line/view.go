package line

import (
	"cmp"
	"fmt"
	"iter"
	"maps"
	"slices"
	"strconv"
	"strings"

	"go.jacobcolvin.com/niceyaml/internal/cells"
	"go.jacobcolvin.com/niceyaml/internal/escape"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

// View is [Lines] content together with the decoration one rendering of it
// carries: a [Flag], [Overlays], and [Annotations] per line. The printer
// renders a View, and every utility that marks content, such as a diff or
// a bound error, hands one out.
//
// A View shares its lines with every other View over the same content and
// owns its decoration alone. Creating one costs an index of the lines it
// holds and no copy of their content, and decorating one reaches no
// other. Two renderings of one document, such as search
// highlights and error marks, are two Views over the same [Lines].
//
// Every index and every [position.Range] a View takes or yields is in the
// coordinates of its content, the [Lines] that [View.Lines] returns, where
// line i is [Lines.Line] i. A View holds each line of its content at most
// once, in content order. A View from [View.Slice] holds some of those
// lines and keeps their indices. A range from a search of the content or
// from the path of a document applies to a slice as it applies to the
// whole, and slicing before or after decorating renders the same.
// [View.All] yields the lines the View holds with their indices,
// [View.Contains] reports whether it holds a line, [View.Index] finds the
// index of a line it holds, and [View.Count] is the number it holds.
//
// Index-taking methods panic when the index is outside the content, as
// indexing a slice does. A View keeps decoration on a line it does not
// hold, and that decoration never renders. A View is not safe for
// concurrent mutation. Decorate from one goroutine at a time, and do not
// decorate while another goroutine renders.
//
// Create instances with [NewView]. The zero value is an empty view.
type View struct {
	// The decoration of the lines that carry some, keyed by index. An
	// undecorated line has no entry, so a slice or a clone costs the
	// lines it holds and the decoration it copies, whatever the length of
	// the content.
	flags       map[int]Flag
	overlays    map[int]Overlays
	annotations map[int]Annotations
	lines       Lines
	// The index in lines of each line the View holds, ascending.
	held []int
}

// NewView creates a new [*View] over lines with no decoration. Without
// spans it holds every line in order. With spans it holds the lines
// within any of them, in content order and each once, as [Lines.All]
// yields them. It then holds what [View.Slice] of a new View over every
// line holds, and the index it builds grows with those lines alone.
func NewView(lines Lines, spans ...position.Span) *View {
	v := &View{lines: lines}

	if len(spans) == 0 {
		v.held = make([]int, 0, lines.Len())
	}

	for i := range lines.All(spans...) {
		v.held = append(v.held, i)
	}

	return v
}

// Lines returns the content of the [View], every line of the [Lines] it is
// over, whether or not the View holds it. A search of the content, such as
// one a [finder.Finder] loads, yields ranges in the coordinates every View
// method takes.
//
// [finder.Finder]: https://pkg.go.dev/go.jacobcolvin.com/niceyaml/finder#Finder
func (v *View) Lines() Lines {
	if v == nil {
		return Lines{}
	}

	return v.lines
}

// Held returns the lines the [View] holds as new [Lines], in the order
// [View.All] yields them. The result shares the lines by pointer with the
// content, as [Collect] does. It has the methods a View lacks, such as
// [Lines.Content], [Lines.Tokens], [Lines.Runes], and [Lines.TokenAt], so
// it reaches the text of the lines a slice holds, such as one document of
// a file that holds several:
//
//	text := doc.View().Held().Content()
//
// Each line keeps the number it has in the file, but the indices of the
// result start at zero. Line i of the result is the i-th held line, not
// line i of the content, so an index or a range computed on the result
// does not apply to the View. A diff or a search of the held lines takes
// the View itself, as a [Sequence], and works in the coordinates of the
// content. A nil View holds no lines.
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
// [View.All] yields and [View.Held] returns. The lines of the content,
// held or not, are [View.Lines], and [Lines.Len] counts those.
func (v *View) Count() int {
	if v == nil {
		return 0
	}

	return len(v.held)
}

// Contains reports whether the [View] holds line i of its content. A nil
// View holds no line, and no View holds an index outside its content.
func (v *View) Contains(i int) bool {
	if v == nil {
		return false
	}

	_, ok := slices.BinarySearch(v.held, i)

	return ok
}

// Index returns the index of the line the [View] holds that is l and
// true. Every View over the same content shares its lines by pointer. A
// decorator that knows a line of a [Lines] value finds it in a slice of
// that content, or in a diff that interleaves it with another revision,
// without knowing what built the view. A line the view does not hold,
// such as one from other content, reports false.
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

// All returns an iterator over the lines the [View] holds within any of
// the given spans, in content order and each once whatever order the
// spans come in and however they overlap. Without spans, All yields every
// line the View holds. Each iteration yields the index of the line in the
// content and the [*Line], and the index reaches the line's decoration
// through [View.Flag], [View.Overlays], and [View.Annotations]. A span
// reaching outside the content selects the lines it does hold.
func (v *View) All(spans ...position.Span) iter.Seq2[int, *Line] {
	return func(yield func(int, *Line) bool) {
		if v == nil {
			return
		}

		if len(spans) == 0 {
			for _, i := range v.held {
				if !yield(i, v.lines.lines[i]) {
					return
				}
			}

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

// Flag returns the [Flag] of line i. The zero value is [FlagDefault].
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
// covers before adding; AddLineOverlay does not.
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
// It splits each range into one overlay per line with [Lines.SliceLines],
// which clamps the columns to the width of the line and skips lines
// outside the content, so a range computed against longer content is safe
// to apply. A range that covers no columns of a line adds no overlay to
// it, and an overlay on a line the View does not hold never renders.
func (v *View) AddOverlay(s kind.Kind, ranges ...position.Range) {
	for _, r := range ranges {
		v.addOverlayRange(s, false, r)
	}
}

// BlendOverlay adds an overlay like [View.AddOverlay], but one that blends
// with the style underneath it. A search highlight added this way keeps the
// token or diff color of the text it covers.
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
// shares the lines with the original and holds the same ones. It costs
// one copy of the index of the lines it holds and of the flags, overlays,
// and annotations, and decorating either reaches nothing in the other.
func (v *View) Clone() *View {
	if v == nil {
		return nil
	}

	c := &View{lines: v.lines, held: slices.Clone(v.held), flags: maps.Clone(v.flags)}

	for i, o := range v.overlays {
		c.AddLineOverlay(i, o...)
	}

	for i, a := range v.annotations {
		c.Annotate(i, a...)
	}

	return c
}

// Slice returns a new [*View] over the same content that holds the lines
// the receiver holds within any of the given spans, in content order and
// each once, as [View.All] yields them, each with its index and its
// decoration. The result owns its decoration and carries that of the
// lines it holds, so it is the view a caller renders to show part of a
// document, such as the hunks around an error. A range or an index that
// applies to the receiver applies to it. Slicing an already sliced
// view narrows it further.
//
// With no span, Slice holds every line the receiver holds, as [View.All]
// yields every line when given no span. Pass an empty [position.Span] for
// a view that holds none.
func (v *View) Slice(spans ...position.Span) *View {
	out := &View{lines: v.Lines()}
	if v == nil {
		return out
	}

	for i := range v.All(spans...) {
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

// Hunks returns a [*View] that holds the decorated lines of the View
// with context lines of unchanged content on either side of each one,
// so a caller that marks a document shows the marked parts alone. A
// decorated line carries a [Flag] other than [FlagDefault], an [Overlay],
// or an [Annotation] of a kind other than [kind.UISeparator]. Decorated
// lines whose context windows overlap or touch share a hunk, and distant
// ones become separate hunks. A "..." annotation of kind
// [kind.UISeparator] sits above the first line after each gap between
// two lines the result holds, so the separator marks the gap between
// two hunks and any gap the View itself skips inside one. No separator
// marks the lines skipped before the first hunk. The result
// drops every separator the View carries before adding its own, so the
// hunks of hunks match the hunks of the original View at the same or a
// smaller context. A negative context shows the decorated lines alone,
// as 0 does, and a View with no decorated line yields a View that holds
// no line.
//
// The result is a [View.Slice], so the lines keep their indices, a
// range from the content applies to it, and decoration added after
// slicing lands on the lines it holds. Context lines count in the
// content, and the hunks hold those of them the View holds, so a slice
// of a document yields hunks within the slice.
//
// An error excerpt is the hunks of a view the error marked, and a caller
// that marks several errors, or search matches, on one view takes the
// hunks of that view the same way:
//
//	view := source.View()
//	for bound := range niceyaml.AllBindings(err) {
//		bound.Annotate(view)
//	}
//	lipgloss.Println(p.Print(view.Hunks(2)))
func (v *View) Hunks(context int) *View {
	var marked []int

	for i := range v.All() {
		if v.decorated(i) {
			marked = append(marked, i)
		}
	}

	spans := position.ContextSpans(marked, context, v.Lines().Len())
	if len(spans) == 0 {
		return v.Slice(position.Span{})
	}

	hunks := v.Slice(spans...)

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
// annotation other than a separator [View.Hunks] adds.
func (v *View) decorated(i int) bool {
	return v.Flag(i) != FlagDefault || len(v.Overlays(i)) > 0 ||
		slices.ContainsFunc(v.Annotations(i), func(a Annotation) bool { return !isSeparator(a) })
}

// isSeparator reports whether a is an annotation of kind
// [kind.UISeparator], such as the one [View.Hunks] adds above a gap.
func isSeparator(a Annotation) bool {
	return a.Kind == kind.UISeparator
}

// String renders the [View] as plain text: each line behind its number,
// and the annotations above and below it on rows of their own, one row per
// kind as [Annotations.ByKind] groups them. Each row below the line holds
// a caret at the column of its annotations and their contents after the
// caret, joined with "; " in column order. The first row below the line
// also holds a caret under every column an overlay covers, and a line with
// overlays but no annotation below it gets that row alone. Annotations of
// a kind without content still get their caret when their row holds no
// overlay caret, so an overlay of no width, such as one at the end of the
// line, shows where it sits. String does not render flags.
// The number column is at least four wide and grows to fit the largest
// number in the view, so every row lines up. Annotations whose column lies
// more than [MaxColPastEnd] columns past the end of the content start at
// that bound, as the printer starts them.
//
// Control characters render as their pictures, and a rune that takes two
// cells in a terminal gets two carets, so the carets stay under the runes
// they mark in a fixed-width font. The output holds no escape sequences,
// so it goes into a log or a golden file as it is, and
// [go.jacobcolvin.com/niceyaml.FormatError] prints the excerpt of a
// bound error this way. A printer renders the same view with styles, and
// color marks the overlays there, so its rows below a line can differ
// from these. [go.jacobcolvin.com/niceyaml/printer.DefaultAnnotation]
// draws the overlay carets on the row of each kind below a line rather
// than the first alone, and draws none for a blend overlay or for a line
// with no annotation below it. An empty view renders as "".
func (v *View) String() string {
	width := 4
	for _, ln := range v.All() {
		width = max(width, len(strconv.Itoa(ln.Number())))
	}

	blank := strings.Repeat(" ", width) + " | "

	var rows []string

	for i, ln := range v.All() {
		anns := v.Annotations(i)

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

			col := annotationCol(kept.Col(), ln.Width())
			padding := strings.Repeat(" ", colWidth(ln, col))
			rows = append(rows, blank+padding+escape.Control(strings.Join(kept.Contents(), "; ")))
		}

		rows = append(rows, contentRow(ln, width))

		// Each kind of annotation below the line gets a row of its own as
		// well, in the order the kinds first appear. The overlays mark the
		// first row, so a line with overlays but no annotation below it
		// still gets that row.
		groups := anns.Filter(Below).ByKind()
		if len(groups) == 0 {
			groups = []Annotations{nil}
		}

		overlays := v.Overlays(i)
		for _, group := range groups {
			if marker := markerRow(ln, overlays, group); marker != "" {
				rows = append(rows, blank+marker)
			}

			overlays = nil
		}
	}

	return strings.Join(rows, "\n")
}

// contentRow returns the row that renders ln, with its number right-aligned
// in a gutter width cells wide. A line with no number, such as the
// placeholder a diff puts opposite an inserted or deleted line, gets a
// blank gutter, as the printer gives it one.
func contentRow(ln *Line, width int) string {
	number := ""
	if ln.Number() > 0 {
		number = strconv.Itoa(ln.Number())
	}

	return fmt.Sprintf("%*s | %s", width, number, escape.Control(ln.Content()))
}

// colWidth returns the width in cells of the content of ln before col, as
// the content row renders it. See [cells.Row.Width] for the rules it
// measures by.
func colWidth(ln *Line, col int) int {
	return cells.NewRow(ln.Content()).Width(col)
}

// markerRow returns the row below ln that marks its overlays and carries
// its annotations: a caret under every column an overlay covers within the
// line, a caret at the column of the annotations, and their contents after
// the last caret. Annotations without content set no column while one with
// content remains. When none has content and the overlays cover no column,
// such as an overlay of no width at the end of the line, the annotations
// still get one caret at their column, so the row marks the spot. The
// carets take the cells the content row gives each grapheme cluster, so
// they stay under the runes they mark on a line holding wide, combining,
// or control characters. The caret of the annotations sits no further
// than [MaxColPastEnd] columns past the end of the content. Returns ""
// when the line has neither.
func markerRow(ln *Line, overlays Overlays, below Annotations) string {
	marks := overlayMarks(overlays, ln.Width())

	mark := func(col int) {
		col = annotationCol(col, ln.Width())
		if col >= len(marks) {
			marks = append(marks, make([]bool, col+1-len(marks))...)
		}

		marks[col] = true
	}

	kept := below.WithContent()

	switch {
	case len(kept) > 0:
		mark(kept.Col())
	case len(below) > 0 && len(marks) == 0:
		mark(below.Col())
	}

	if len(marks) == 0 {
		return ""
	}

	// Every mark lands on a column that takes a cell, so the trim is a
	// safety net that keeps a row without a caret from rendering.
	carets := strings.TrimRight(renderMarks(ln.Content(), marks), " ")
	if carets == "" && len(kept) == 0 {
		return ""
	}

	var sb strings.Builder

	sb.WriteString(carets)

	if len(kept) > 0 {
		sb.WriteByte(' ')
		sb.WriteString(escape.Control(strings.Join(kept.Contents(), "; ")))
	}

	return sb.String()
}

// overlayMarks marks every column that overlays cover within the first
// width columns of a line. The slice ends at the last marked column, so
// a row rendered from it puts nothing after the last caret. Returns nil
// when the overlays cover no column.
func overlayMarks(overlays Overlays, width int) []bool {
	var marks []bool

	for _, o := range overlays {
		start, end := max(0, o.Cols.Start), min(o.Cols.End, width)
		if start >= end {
			continue
		}

		if end > len(marks) {
			marks = append(marks, make([]bool, end-len(marks))...)
		}

		for col := start; col < end; col++ {
			marks[col] = true
		}
	}

	return marks
}

// renderMarks renders marks as a row under content: a caret under every
// marked column and a space under every other. The content row renders
// each grapheme cluster at its display width. The first column of a
// cluster is therefore as many cells wide as the cluster renders, every
// other column of it takes none, and a column past the end of the content
// takes one cell. A mark on any column of a cluster lands under the whole
// cluster. A cluster that renders no cells, such as a zero-width space,
// has nothing to put a caret under, so its mark moves to the next column
// that takes a cell, where the content row shows what follows it.
func renderMarks(content string, marks []bool) string {
	var sb strings.Builder

	row := cells.NewRow(content)

	marks = slices.Clone(marks)
	for col, marked := range marks {
		if marked {
			marks[row.Start(col)] = true
		}
	}

	// The loop reads the length of marks on every pass, so it still
	// renders a mark it moves past the end.
	for col := 0; col < len(marks); col++ {
		if !marks[col] || row.Start(col) != col || row.Cells(col) > 0 {
			continue
		}

		next := col + 1
		for row.Cells(next) == 0 {
			next++
		}

		if next >= len(marks) {
			marks = append(marks, make([]bool, next+1-len(marks))...)
		}

		marks[next] = true
	}

	for col, marked := range marks {
		cell := " "
		if marked {
			cell = "^"
		}

		sb.WriteString(strings.Repeat(cell, row.Cells(col)))
	}

	return sb.String()
}
