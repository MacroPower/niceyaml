package manifest

import (
	"fmt"
	"slices"
	"unicode/utf8"

	"github.com/goccy/go-yaml/ast"
	"go.jacobcolvin.com/x/jsonschema"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

// prepareWork sets the working copy to a copy of doc, the document or a
// migrated copy of it, without a blanked field. It keeps doc and locate,
// so that a schema check can build a copy that blanks the type fields that
// its schema does not read.
func (d *docState) prepareWork(doc *niceyaml.Node, locate func(niceyaml.Located) niceyaml.Place) bool {
	work, ok := d.workingCopy(doc, locate, nil)
	if !ok {
		return false
	}

	d.work, d.workFrom, d.workLocate = work, doc, locate

	return true
}

// workingCopy returns a copy of doc that holds the lines of doc, with the
// type field at each of hide blanked. Every line and column of the copy
// matches doc, so locate moves a location of the copy, as doc places it,
// into the document. A nil locate leaves the location as it is, for doc
// itself. The copy takes the unmarshalers of the registry ahead of those
// of the Source of doc. It adds the problem of a copy that does not parse,
// or that is empty after the blanking, and reports false.
func (d *docState) workingCopy(
	doc *niceyaml.Node,
	locate func(niceyaml.Located) niceyaml.Place,
	hide []paths.Path,
) (*niceyaml.Node, bool) {
	src := doc.Source()
	text := src.Text()
	span := doc.Span()
	start, end := textBounds(doc)

	work := []rune(text[start:end])
	blank := &blanker{doc: doc, src: src, base: start, text: text, work: work, first: -1}

	for _, p := range hide {
		blank.field(p)
	}

	blank.fillEmptyRoot()

	working := niceyaml.NewSourceFromDerived(string(blank.work), d.doc, func(at niceyaml.Located) niceyaml.Place {
		if at.HasRange {
			at.Range.Start.Line += span.Start
			at.Range.End.Line += span.Start
		}

		if locate != nil {
			return locate(at)
		}

		return niceyaml.Place{Path: at.Path, HasPath: at.HasPath, Range: at.Range, HasRange: at.HasRange}
	}, niceyaml.WithUnmarshalers(d.l.reg.cfg.unmarshalers...), niceyaml.WithoutLimits())

	for _, n := range working.AllDocuments() {
		if n.Err() != nil {
			d.fail(n.Err())

			return nil, false
		}

		if !n.IsEmpty() {
			return n, true
		}
	}

	d.fail(niceyaml.NewError("document holds nothing but its type fields"))

	return nil, false
}

// textBounds returns the bytes [start, end) of the text of the Source of
// doc that the lines of doc hold.
func textBounds(doc *niceyaml.Node) (int, int) {
	src := doc.Source()
	span := doc.Span()

	start, ok := src.Offset(position.New(span.Start, 0))
	if !ok {
		start = 0
	}

	end := len(src.Text())
	if span.End < src.Lines().Len() {
		if off, ok := src.Offset(position.New(span.End, 0)); ok {
			end = off
		}
	}

	return start, end
}

// schemaFields is what the schema of a version says about the type
// fields, as [Registry.schemaFields] returns it.
type schemaFields struct {
	// The validator that the registry read the fields from.
	validator *jsonschema.Validator
	// The type fields that the check of the schema sees blank.
	hidden []paths.Path
	// The type fields that the schema might read in a place that the
	// registry does not follow, as [schemaField] describes. The registry
	// cannot tell whether the check sees them.
	unsure []paths.Path
}

// schemaFields returns what validator, the compiled schema of v, says
// about the type fields. It returns the zero value for a nil validator,
// which does not check anything. A load and [Registry.Verify] both read
// the type fields through it, so they class each field alike.
//
// [SchemaTypeFields] on v states the answer for every type field. Without
// the option, the check sees each type field that the schema declares. A
// type field that the schema does not declare is unsure when a branch or
// a $ref that the document does not resolve might read it, as
// [schemaField] describes. Such a field is unsure whether or not the
// schema also requires it. The check sees every other type field blank.
// On the unversioned layout, the check sees the unsure fields blank too,
// because the documents of that layout do not hold type fields.
//
// The registry keeps the fields of each version under the version and
// the validator that it read them from. The schema registry hands a
// version a new validator after the Invalidate method of that registry
// runs. The fields of the old validator leave the cache with the next
// call, so the cache holds at most one entry for each version.
func (r *Registry) schemaFields(v *versionEntry, validator *jsonschema.Validator) schemaFields {
	if validator == nil {
		return schemaFields{}
	}

	if v.cfg.hasTypeFields {
		if v.cfg.typeFieldsVisible {
			return schemaFields{validator: validator}
		}

		return schemaFields{validator: validator, hidden: r.typeFields()}
	}

	if cached, ok := r.fields.Load(v); ok {
		entry, _ := cached.(schemaFields) //nolint:errcheck // The map holds the fields of each version.
		if entry.validator == validator {
			return entry
		}
	}

	doc := newSchemaDoc(validator)
	entry := schemaFields{validator: validator}

	for _, p := range r.typeFields() {
		f := doc.field(p)

		switch {
		case f.declared:
			// The check sees the field.

		case f.unsure && !v.cfg.unversioned:
			entry.unsure = append(entry.unsure, p)

		default:
			entry.hidden = append(entry.hidden, p)
		}
	}

	r.fields.Store(v, entry)

	return entry
}

// unsureTypeField returns the problem of the schema of v, which might
// read the type field at p in a place that the registry does not follow.
// The author of the registry is at fault for it rather than a document.
func (r *Registry) unsureTypeField(v *versionEntry, p paths.Path) error {
	msg := fmt.Sprintf("%s cannot tell whether its schema of %s %s reads %s", r.app(), v.kind.kind.name, v.label(), p)

	return markedAuthorError(ErrSchemaTypeField, msg, unsureWhy, unsureRemedy(p))
}

// unsureWhy is the detail that says why the registry cannot tell whether
// a schema reads a type field.
const unsureWhy = "a branch or a $ref to another document might read it"

// unsureRemedy returns the detail that says how the author of a registry
// states whether its schema reads the type field at p.
func unsureRemedy(p paths.Path) string {
	return fmt.Sprintf("declare %s under properties outside every branch and every such $ref, "+
		"or pass manifest.SchemaTypeFields", p)
}

// blanker blanks type fields of a document in a working copy of its
// lines. It works rune by rune, so every other rune keeps its line and its
// column.
type blanker struct {
	doc  *niceyaml.Node
	src  *niceyaml.Source
	text string
	work []rune
	// The rune index in work of the first rune that the blanker cleared,
	// or -1.
	first int
	// The byte offset in text where work starts.
	base int
	// The number of root entries that the blanker cleared.
	cleared int
}

// field blanks the entry at p, a field that the document writes as a
// scalar.
func (b *blanker) field(p paths.Path) {
	if r := readField(b.doc, p, nil); !r.found {
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
// include the comma after it, or the comma before it for the last entry.
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
// mapping held only the blanked fields. The copy then holds an empty
// mapping rather than an empty document.
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
