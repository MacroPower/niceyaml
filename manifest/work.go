package manifest

import (
	"fmt"
	"slices"

	"go.jacobcolvin.com/x/jsonschema"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/inputhook"
	"go.jacobcolvin.com/niceyaml/internal/schemalint"
	"go.jacobcolvin.com/niceyaml/paths"
)

// startWork sets the working copy of a document that routed, and reports
// whether the document can go on to its checks. The working copy is the
// document itself unless the registry has unmarshalers and the Source
// came from [input.Sources]. Only such a Source lacks the unmarshalers
// of the registry. A Source that the load built holds them already, and
// a registry without [WithUnmarshalers] has none to add. For a Source
// from [input.Sources] under [WithUnmarshalers], the working copy is a
// copy that puts them under the unmarshalers of that Source, as
// prepareWork builds it.
func (d *docState) startWork() bool {
	if len(d.l.reg.cfg.unmarshalers) > 0 && d.f.target.kind == inputhook.Source {
		return d.prepareWork(d.doc)
	}

	d.work = d.doc

	return true
}

// prepareWork sets the working copy to a copy of doc, the document or the
// edit.Doc.Node of a migration of it. The copy holds the lines of doc
// from the first line of its span, so each error of the copy binds in doc
// at the line that holds its text. The error of a migrated copy moves on
// from doc to the document, through the binding of its edit.Doc. The copy
// takes the unmarshalers of the registry under those of the Source of the
// document. It adds the problem of a copy that does not parse, or that
// holds nothing, and reports false.
func (d *docState) prepareWork(doc *niceyaml.Node) bool {
	span := doc.Span()
	us := slices.Concat(d.l.reg.cfg.unmarshalers, d.doc.Source().Unmarshalers())

	working := niceyaml.NewSourceFromDerived(doc.Text(), doc, func(at niceyaml.Located) niceyaml.Place {
		if at.HasRange {
			at.Range.Start.Line += span.Start
			at.Range.End.Line += span.Start
		}

		return niceyaml.Place{Path: at.Path, HasPath: at.HasPath, Range: at.Range, HasRange: at.HasRange}
	}, niceyaml.WithUnmarshalers(us...), niceyaml.WithoutLimits())

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

	d.fail(niceyaml.NewError("document holds nothing"))

	return false
}

// schemaFields is what the schema of a version says about the type
// fields, as [Registry.schemaFields] returns it.
type schemaFields struct {
	// The validator that the registry read the fields from.
	validator *jsonschema.Validator
	// The type fields that the check of the schema does not see.
	hidden []paths.Path
	// The type fields that the schema might read in a place that the
	// registry does not follow, as [schemalint.Field] describes. The registry
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
// [schemalint.Field] describes. Such a field is unsure whether or not the
// schema also requires it. The check does not see any other type field.
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

	doc := schemalint.NewDoc(validator)
	entry := schemaFields{validator: validator}

	for _, p := range r.typeFields() {
		f := doc.Field(p)

		switch {
		case f.Declared:
			// The check sees the field.

		case f.Unsure:
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
	of := v.kind.kind.name + " " + v.label()
	if v.cfg.unversioned {
		of = r.label(v)
	}

	msg := fmt.Sprintf("%s cannot tell whether its schema of %s reads %s", r.app(), of, p)

	return authorError(ErrSchemaTypeField, msg, unsureWhy, unsureRemedy(p))
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
