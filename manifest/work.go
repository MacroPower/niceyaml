package manifest

import (
	"slices"
	"unicode/utf8"

	"github.com/goccy/go-yaml/ast"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/hook"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

// prepareWork builds the working copy of doc, the document or a migrated
// copy of it. The copy holds the lines of doc with each type field
// blanked. The schema checks and the decode thus admit the type fields,
// whatever the schema and the Go type declare. Every line and column of
// the copy matches doc, so locate moves a location of the copy, as doc
// places it, into the document. A nil locate leaves the location as it
// is, for doc itself. The copy takes the unmarshalers of the registry
// ahead of those of the Source of doc.
func (d *docState) prepareWork(doc *niceyaml.Node, locate func(hook.Located) hook.Place) bool {
	src := doc.Source()
	text := src.Text()
	span := doc.Span()

	start, ok := src.Offset(position.New(span.Start, 0))
	if !ok {
		start = 0
	}

	end := len(text)
	if span.End < src.Lines().Len() {
		if off, ok := src.Offset(position.New(span.End, 0)); ok {
			end = off
		}
	}

	work := []rune(text[start:end])
	blank := &blanker{doc: doc, src: src, base: start, text: text, work: work, first: -1}

	for _, p := range d.l.reg.typeFields() {
		blank.field(p)
	}

	blank.fillEmptyRoot()

	working, ok := hook.WorkingSource(string(blank.work), d.doc, d.l.reg.cfg.unmarshalers, func(at hook.Located) hook.Place {
		if at.HasRange {
			at.Range.Start.Line += span.Start
			at.Range.End.Line += span.Start
		}

		if locate != nil {
			return locate(at)
		}

		return hook.Place{Path: at.Path, HasPath: at.HasPath, Range: at.Range, HasRange: at.HasRange}
	}).(*niceyaml.Source)
	if !ok {
		return false
	}

	for _, n := range working.AllDocuments() {
		if n.Err() != nil {
			d.fail(n.Err())

			return false
		}

		if !n.IsEmpty() {
			d.work = n

			return true
		}
	}

	d.fail(niceyaml.NewError("document holds nothing but its type fields"))

	return false
}

// blanker blanks the type fields of a document in a working copy of its
// lines, rune by rune, so every other rune keeps its line and its column.
type blanker struct {
	doc  *niceyaml.Node
	src  *niceyaml.Source
	text string
	work []rune
	// The rune index in work of the first rune the blanker cleared, or -1.
	first int
	// The byte offset in text where work starts.
	base int
	// The root entries the blanker cleared.
	cleared int
}

// field blanks the entry at p, a field the document writes as a scalar.
func (b *blanker) field(p paths.Path) {
	if r := readField(b.doc, p); !r.found {
		return
	}

	keys, err := b.doc.Ranges(p.Key())
	if err != nil || len(keys) == 0 {
		return
	}

	values, err := b.doc.Ranges(p)
	if err != nil || len(values) == 0 {
		return
	}

	from, ok := b.runeAt(keys[0].Start)
	if !ok {
		return
	}

	to, ok := b.runeAt(values[len(values)-1].End)
	if !ok {
		return
	}

	from, to = b.flowComma(from, to)

	for i := from; i < to && i < len(b.work); i++ {
		if b.work[i] != '\n' && b.work[i] != '\r' {
			b.work[i] = ' '
		}
	}

	if b.first < 0 || from < b.first {
		b.first = from
	}

	if p.Len() == 1 {
		b.cleared++
	}
}

// runeAt returns the index in work of the rune at pos in the document.
func (b *blanker) runeAt(pos position.Position) (int, bool) {
	off, ok := b.src.Offset(pos)
	if !ok || off < b.base || off > len(b.text) {
		return 0, false
	}

	return utf8.RuneCountInString(b.text[b.base:off]), true
}

// flowComma widens the range [from, to) of an entry of a flow mapping to
// take the comma after it, or the comma before it for the last entry.
func (b *blanker) flowComma(from, to int) (int, int) {
	i := to
	for i < len(b.work) && (b.work[i] == ' ' || b.work[i] == '\t') {
		i++
	}

	if i < len(b.work) && b.work[i] == ',' {
		return from, i + 1
	}

	if i >= len(b.work) || b.work[i] != '}' {
		return from, to
	}

	j := from - 1
	for j >= 0 && (b.work[j] == ' ' || b.work[j] == '\t' || b.work[j] == '\n' || b.work[j] == '\r') {
		j--
	}

	if j >= 0 && b.work[j] == ',' {
		return j, to
	}

	return from, to
}

// fillEmptyRoot writes "{}" over the first blanked runes when the root
// mapping held nothing but the blanked fields, so the copy holds an empty
// mapping rather than nothing.
func (b *blanker) fillEmptyRoot() {
	if b.cleared == 0 || b.first < 0 || b.first+2 > len(b.work) {
		return
	}

	entries, ok := astnode.Entries(astnode.Content(b.doc.AST()))
	if !ok || len(entries) > b.cleared {
		return
	}

	if m, ok := astnode.Content(b.doc.AST()).(*ast.MappingNode); ok && m.IsFlowStyle {
		return
	}

	if slices.Contains(b.work[b.first:b.first+2], '\n') {
		return
	}

	b.work[b.first], b.work[b.first+1] = '{', '}'
}
