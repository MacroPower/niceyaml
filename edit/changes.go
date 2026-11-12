package edit

import (
	"context"
	"slices"
	"strings"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/diff/lcs"
	"go.jacobcolvin.com/niceyaml/internal/bom"
	"go.jacobcolvin.com/niceyaml/position"
)

// Change is one replacement in the user's text that the edits of a [Doc]
// make, as [Doc.Changes] returns it.
type Change struct {
	// Text is the text that replaces Range, or "" for a deletion.
	Text string
	// Range is the range of the user's text that Text replaces, in the
	// coordinates of the lines of the Source that [New] took. The Range of
	// a Change in a later document of a file thus counts the lines of the
	// file. An insertion has an empty Range.
	Range position.Range
}

// Changes returns the replacements that turn the user's text into the
// text of the document as the edits leave it. Each [Change] holds a range
// of the user's text, as the Range of a [Removal] does, and the text that
// replaces it. The list holds the net result of every Apply that passed,
// in the order of the user's text, and no two Changes overlap. A program
// that edits a document for an editor or a review thus hands each Change
// on as an edit of the user's text:
//
//	for _, c := range d.Changes() {
//		edits = append(edits, TextEdit{Range: c.Range, NewText: c.Text})
//	}
//
// Each Change replaces the bytes of its Range in the text of the Source,
// at the offsets that [niceyaml.Source.Offset] gives its Start and its
// End. Applying the Changes of every Doc that [FileText] takes to that
// text gives the text that FileText returns.
//
// Changes reads from the origin of each byte which bytes the edits kept
// in place, in the order of the user's text. It leaves those bytes out of
// the Changes. No Change starts or ends with bytes that match the bytes of
// the user's text that it replaces. So a Set back to the old value leaves
// no Change. A Set of one scalar changes no more than the scalar and keeps
// the comment after it. A Set of 2 to 20 inserts "0" after the 2.
//
// An edit can write bytes with the origins of other bytes, as
// [Node.SetKey] does when it keeps the rest of an entry. The origins then
// place those bytes away from the text that reads as the user wrote it.
// So Changes also diffs the lines of the user's text against the lines of
// the document. It splits the text where the two lists of replacements
// agree. Between two such splits, it takes the list that removes and
// writes fewer bytes, and the origins win a tie. Edits that rebuild an
// entry from the origins of another entry thus leave no Change where the
// text reads as the user wrote it. A Set of a scalar inside that entry
// changes no more than the scalar.
//
// A Change can therefore cover bytes that the edits kept, where fewer
// replaced bytes give the same text. Two Changes in a row never write
// back the user's text from the start of the first to the end of the
// second, with the kept bytes between them.
//
// Changes reads the text as the edits leave it, and not the calls that
// made it. One Change can hold the writes of several calls or Applies. A
// Move past other text reads as a deletion at one place and an insertion
// at another. The pair holds the moved text, or the text that the Move
// passed when that text gives fewer replaced bytes. A Move that keeps its
// order relative to the kept text can keep its bytes in place and change
// only the text around them. [Doc.Removals] names the values that the
// edits removed. A program that needs the Changes of each Apply reads them
// after each one and compares them.
//
// A Change can take bytes that the edits kept, so that each Range starts
// and ends where a position of the user's text points. The bytes of a
// rune, a "\r\n", or a byte order mark and the line break before it can
// thus fall inside a Change.
//
// During an Apply, Changes reads the text as Doc.Text does.
func (d *Doc) Changes() []Change {
	d.settle()

	src := d.user.Source()
	text := src.Text()

	cur, origins := d.markedText()

	// A Range cannot end between a byte order mark and the line that it
	// stands before. So a mark at the start of the first line after the
	// document joins the text, as a part that the edits kept. That line can
	// be a comment line above the "---" of the next document, or the "---"
	// itself.
	end := d.end
	if pos, ok := src.PositionAt(end); ok {
		if off, ok := src.Offset(pos); ok && off > end {
			end = off
		}
	}

	for i := d.end; i < end; i++ {
		origins = append(origins, i)
	}

	cur += text[d.end:end]

	var (
		reps  []replacement
		oldAt = d.start
		newAt = 0
	)

	add := func(oldEnd, newEnd int) {
		r := trimmed(src, replacement{start: oldAt, end: oldEnd, text: cur[newAt:newEnd]})
		if !r.noop() {
			reps = append(reps, r)
		}
	}

	for _, r := range longestKept(keptRuns(text, cur, origins), d.start, end) {
		r, ok := r.snapped(src)
		if !ok {
			continue
		}

		add(r.old, r.new)

		oldAt, newAt = r.old+r.n, r.new+r.n
	}

	add(end, len(cur))

	reps = cheaper(reps, lineReplacements(src, d.start, text[d.start:end], cur))

	changes := make([]Change, 0, len(reps))
	for _, r := range joined(src, reps) {
		changes = append(changes, Change{
			Range: position.NewRange(sourcePosition(src, r.start), sourcePosition(src, r.end)),
			Text:  r.text,
		})
	}

	if len(changes) == 0 {
		return nil
	}

	return changes
}

// replacement is the replacement of the bytes from start to end in the
// text of a Source with text.
type replacement struct {
	text       string
	start, end int
}

// noop reports whether r leaves the text as it was.
func (r replacement) noop() bool {
	return r.start == r.end && r.text == ""
}

// size returns the number of bytes that r removes and writes.
func (r replacement) size() int {
	return r.end - r.start + len(r.text)
}

// joined returns reps, in which it joins two neighbors when one smaller
// replacement does the work of both. The joined replacement writes the
// kept text between the two neighbors back in place, and trimmed cuts the
// bytes at its ends that match. A join stands only when the kept bytes
// stay out of the replacement that it makes. A pair that trims to nothing
// leaves no replacement. Each join can let the replacement before it join
// too, so joined runs until no pair joins.
//
// A deletion before kept text and an insertion after it can write the
// same bytes back, and the join then drops both.
func joined(src *niceyaml.Source, reps []replacement) []replacement {
	text := src.Text()

	for i := 0; i+1 < len(reps); {
		a, b := reps[i], reps[i+1]
		r := trimmed(src, replacement{
			start: a.start,
			end:   b.end,
			text:  a.text + text[a.end:b.start] + b.text,
		})

		keeps := r.start == r.end || r.end <= a.end || r.start >= b.start
		if !keeps || r.size() >= a.size()+b.size() {
			i++

			continue
		}

		if r.noop() {
			reps = slices.Delete(reps, i, i+2)
		} else {
			reps = slices.Replace(reps, i, i+2, r)
		}

		// The replacement before the join can now join with the text after
		// it.
		i = max(i-1, 0)
	}

	return reps
}

// lineDiff diffs the lines of the user's text against the lines of the
// text of a Doc.
var lineDiff = lcs.NewHirschberg()

// lineReplacements returns the replacements that a diff of the lines of
// two texts finds. The text old is the text of the user's Source from
// base, and cur is the text of a Doc. A run of changed lines can delete as
// many lines as it inserts. Such a run pairs each deleted line with an
// inserted line, so each pair gives a replacement of its own. The
// replacements leave out the bytes at their ends that they keep, as
// trimmed cuts them.
//
//nolint:contextcheck // The diff of two strings reads no state that a context carries.
func lineReplacements(src *niceyaml.Source, base int, old, cur string) []replacement {
	before, after := splitLines(old), splitLines(cur)

	ops, err := lineDiff.Diff(context.Background(), before, after)
	if err != nil {
		return nil
	}

	// A span replaces old[os:oe] with cur[ns:ne].
	type span struct{ os, oe, ns, ne int }

	var (
		spans          []span
		oAt, nAt       int
		deleted, added []string
		oFrom, nFrom   int
	)

	flush := func() {
		switch {
		case len(deleted) == len(added):
			o, n := oFrom, nFrom
			for i := range deleted {
				spans = append(spans, span{os: o, oe: o + len(deleted[i]), ns: n, ne: n + len(added[i])})
				o, n = o+len(deleted[i]), n+len(added[i])
			}

		default:
			spans = append(spans, span{os: oFrom, oe: oAt, ns: nFrom, ne: nAt})
		}

		deleted, added = deleted[:0], added[:0]
		oFrom, nFrom = oAt, nAt
	}

	for _, op := range ops {
		switch op.Kind {
		case lcs.OpEqual:
			flush()

			oAt += len(before[op.Before])
			nAt += len(after[op.After])
			oFrom, nFrom = oAt, nAt

		case lcs.OpDelete:
			deleted = append(deleted, before[op.Before])
			oAt += len(before[op.Before])

		case lcs.OpInsert:
			added = append(added, after[op.After])
			nAt += len(after[op.After])
		}
	}

	flush()

	// A replacement starts and ends where a position of the lines of src
	// points. A span that starts or ends before the byte order mark of a
	// line thus takes the bytes around it that the diff kept. A span that
	// then reaches into the span before it joins that span.
	var joinedSpans []span

	for _, s := range spans {
		for s.os > 0 && !boundary(src, base+s.os) {
			s.os--
			s.ns--
		}

		for s.oe < len(old) && !boundary(src, base+s.oe) {
			s.oe++
			s.ne++
		}

		if n := len(joinedSpans); n > 0 && s.os < joinedSpans[n-1].oe {
			last := &joinedSpans[n-1]
			last.oe, last.ne = max(last.oe, s.oe), max(last.ne, s.ne)

			continue
		}

		joinedSpans = append(joinedSpans, s)
	}

	reps := make([]replacement, 0, len(joinedSpans))

	for _, s := range joinedSpans {
		r := trimmed(src, replacement{start: base + s.os, end: base + s.oe, text: cur[s.ns:s.ne]})
		if !r.noop() {
			reps = append(reps, r)
		}
	}

	return reps
}

// splitLines returns the lines of s, each with its line break.
func splitLines(s string) []string {
	lines := strings.SplitAfter(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	return lines
}

// cheaper returns the replacements of a and b, two lists that each turn
// the same text into the same text. It splits the text where no
// replacement of either list starts or ends. A split also needs the
// replacements before it to grow or shrink the text by the same count in
// both lists. Between two such splits, cheaper takes the replacements that
// remove and write fewer bytes, or those of a when both remove and write
// as many.
func cheaper(a, b []replacement) []replacement {
	var (
		out  []replacement
		i, j int
	)

	for i < len(a) || j < len(b) {
		var (
			fromA, fromB = i, j
			end          = -1
			// The difference between the growth that the replacements of a
			// and of b make since the last split.
			growth       int
			sizeA, sizeB int
		)

		for i < len(a) || j < len(b) {
			inA := j >= len(b) || i < len(a) && a[i].start <= b[j].start

			var r replacement

			if inA {
				r = a[i]
			} else {
				r = b[j]
			}

			if end >= 0 && r.start > end && growth == 0 {
				break
			}

			end = max(end, r.end)
			grow := len(r.text) - (r.end - r.start)

			if inA {
				sizeA += r.size()
				growth += grow
				i++
			} else {
				sizeB += r.size()
				growth -= grow
				j++
			}
		}

		if sizeB < sizeA {
			out = append(out, b[fromB:j]...)
		} else {
			out = append(out, a[fromA:i]...)
		}
	}

	return out
}

// markedText returns the text that [Doc.Text] returns. It also returns
// the offset in the text of the user's Source that each byte of that text
// came from, or -1 for a byte that an edit wrote.
func (d *Doc) markedText() (string, []int) {
	places := d.textMarks()
	text := d.withMarks(places)
	origins := make([]int, 0, len(text))

	last := 0
	for _, p := range append(places, markPlace{at: len(d.cur), mark: -1}) {
		for _, o := range d.origin[last:p.at] {
			origins = append(origins, d.sourceOffset(o))
		}

		last = p.at

		if p.mark < 0 {
			break
		}

		at := d.start + d.marks[p.mark] + p.mark*len(bom.Mark)
		for i := range len(bom.Mark) {
			origins = append(origins, at+i)
		}
	}

	return text, origins
}

// sourceOffset returns the offset in the text of the user's Source of the
// byte at o in orig, or -1 for a negative o.
func (d *Doc) sourceOffset(o int) int {
	if o < 0 {
		return -1
	}

	// Each mark that the tokenizer dropped at or before o stands in front
	// of the byte in the text of the Source.
	dropped, _ := slices.BinarySearch(d.marks, o+1)

	return d.start + o + dropped*len(bom.Mark)
}

// trimmed returns r without the bytes at its ends that r leaves as they
// were in the text of src. It cuts only where a position of the lines of
// src points, so the replacement that it returns starts and ends at such
// places.
func trimmed(src *niceyaml.Source, r replacement) replacement {
	old, repl := src.Text()[r.start:r.end], r.text

	prefix := 0
	for prefix < len(old) && prefix < len(repl) && old[prefix] == repl[prefix] {
		prefix++
	}

	for prefix > 0 && !boundary(src, r.start+prefix) {
		prefix--
	}

	old, repl = old[prefix:], repl[prefix:]
	start := r.start + prefix

	suffix := 0
	for suffix < len(old) && suffix < len(repl) && old[len(old)-1-suffix] == repl[len(repl)-1-suffix] {
		suffix++
	}

	for suffix > 0 && !boundary(src, r.end-suffix) {
		suffix--
	}

	return replacement{start: start, end: r.end - suffix, text: repl[:len(repl)-suffix]}
}

// sourcePosition returns the position of the byte at off in the text of
// src, in the coordinates of [niceyaml.Source.Lines].
func sourcePosition(src *niceyaml.Source, off int) position.Position {
	pos, _ := src.PositionAt(off)

	return pos
}

// keptRun is a run of n bytes of the text of a [Doc] that came from the
// text of the user's Source in order. Each byte holds the byte that it came
// from. The run starts at new in the text of the Doc and at old in the
// text of the Source.
type keptRun struct {
	old, new, n int
}

// keptRuns returns the maximal runs of cur, the text of a Doc, in the
// order of cur. The bytes of each run hold the bytes of text, the text of
// the user's Source, at the offsets that origins gives for them.
func keptRuns(text, cur string, origins []int) []keptRun {
	var runs []keptRun

	for i, o := range origins {
		// An edit can write a byte with the origin of another byte, as a
		// "-" that takes the origin of its value does, so a kept byte
		// holds the byte of its origin too.
		if o < 0 || o >= len(text) || text[o] != cur[i] {
			continue
		}

		if n := len(runs); n > 0 {
			last := &runs[n-1]
			if last.new+last.n == i && last.old+last.n == o {
				last.n++

				continue
			}
		}

		runs = append(runs, keptRun{old: o, new: i, n: 1})
	}

	return runs
}

// longestKept returns the chain of runs that keeps the most bytes. Each
// run of the chain starts in the text of the Source at or past the end of
// the run before it. The runs come in the order of the text of the Doc.
// Each starts and ends in the text of the Source between start and end,
// inclusive.
func longestKept(runs []keptRun, start, end int) []keptRun {
	if len(runs) == 0 {
		return nil
	}

	// A Fenwick tree finds the heaviest chain that ends at or before each
	// offset in the text of the Source.
	type best struct{ bytes, run int }

	tree := make([]best, end-start+2)
	query := func(off int) best {
		b := best{run: -1}
		for i := off - start + 1; i > 0; i -= i & -i {
			if tree[i].bytes > b.bytes {
				b = tree[i]
			}
		}

		return b
	}
	update := func(off int, b best) {
		for i := off - start + 1; i < len(tree); i += i & -i {
			if b.bytes > tree[i].bytes {
				tree[i] = b
			}
		}
	}

	for i := range tree {
		tree[i].run = -1
	}

	prev := make([]int, len(runs))
	top := best{run: -1}

	for k, r := range runs {
		before := query(r.old)
		prev[k] = before.run

		b := best{bytes: before.bytes + r.n, run: k}
		update(r.old+r.n, b)

		if b.bytes > top.bytes {
			top = b
		}
	}

	var chain []keptRun

	for k := top.run; k >= 0; k = prev[k] {
		chain = append(chain, runs[k])
	}

	slices.Reverse(chain)

	return chain
}

// snapped returns r without the bytes at its ends that a position of the
// lines of src does not point between. The bytes of a byte order mark
// that the tokenizer dropped are such bytes, and so are the bytes inside
// a rune or a "\r\n". It reports false when no byte of r stays.
func (r keptRun) snapped(src *niceyaml.Source) (keptRun, bool) {
	start, end := r.old, r.old+r.n

	for start < end && !boundary(src, start) {
		start++
	}

	for end > start && !boundary(src, end) {
		end--
	}

	if end <= start {
		return keptRun{}, false
	}

	return keptRun{old: start, new: r.new + start - r.old, n: end - start}, true
}

// boundary reports whether a position of the lines of src points at the
// byte at off in its text, as [niceyaml.Source.Offset] reads it.
func boundary(src *niceyaml.Source, off int) bool {
	pos, ok := src.PositionAt(off)
	if !ok {
		return false
	}

	at, ok := src.Offset(pos)

	return ok && at == off
}
