package manifest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"

	"go.jacobcolvin.com/x/jsonschema"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/schemalint"
)

// Verify loads and compiles the schema of each version. It returns an
// error that names each version whose schema fails, with the error of the
// schema. A load fails each document that reaches such a version, so a
// test should call Verify to find the mistake before a user does:
//
//	func TestRegistry(t *testing.T) {
//		t.Parallel()
//
//		require.NoError(t, config.Registry.Verify(t.Context()))
//	}
//
// Verify shares the schemas with the loads of the registry, so a schema
// that compiles here serves each load without a second compile.
//
// Verify does not compare the properties of a schema with the fields of
// the kind type. When the gen tool of [go.jacobcolvin.com/x/jsonschema]
// writes the schemas, the same test calls [schema.CheckFields] for each
// kind type. CheckFields reports each field where gen and the decoder
// disagree on the name of the field or on whether the field is inline:
//
//	require.NoError(t, schema.CheckFields[config.Cafe]())
//	require.NoError(t, config.Registry.Verify(t.Context()))
//
// The test passes CheckFields the unmarshalers that the decodes of the
// kind apply, in the order that they apply them. The unmarshalers of
// [WithUnmarshalers] come first, and the unmarshalers of the Source of
// the document follow. Last come the unmarshalers that each
// [niceyaml.WithUnmarshaler] in [WithDecodeOptions] or in the
// [DecodeOptions] of the kind holds. The check then skips each type that
// those decodes read whole:
//
//	require.NoError(t, schema.CheckFields[config.Cafe](unmarshalers...))
//
// [niceyaml.Problems] yields each row of the error, and each row matches
// one sentinel. A row of a schema that fails matches [schema.ErrLoad] or
// [schema.ErrCompile]. Each section of this documentation names the
// sentinel of the rows that it describes. [niceyaml.IsInvalid] reports
// false for each row. A test that accepts the rows of one rule thus skips
// them by sentinel and fails on every other row:
//
//	for p := range niceyaml.Problems(config.Registry.Verify(t.Context())) {
//		// The registry requires some fields with a default on purpose.
//		if !errors.Is(p.Err, manifest.ErrUnusedDefault) {
//			t.Error(p.Err)
//		}
//	}
//
// # Type Fields
//
// Verify also reports each type field that the schema of a version
// requires and does not declare under properties. The check of a schema
// sees only the type fields that the schema declares, so such a schema
// fails every document:
//
//	MenuItem v1: $.kind: required, but no properties keyword declares it
//
// The report matches [ErrUndeclaredTypeField].
//
// The check follows the chains of schemas that the Deprecations section
// describes. It does not look inside a branch or behind a $ref to another
// document. A branch is a schema under anyOf, oneOf, not, if, then, else,
// or dependentSchemas. A type field that the schema also declares outside
// them passes. Verify reports each other type field that a branch declares
// or requires, or that lies on a path through such a $ref:
//
//	MenuItem v1: $.kind: a branch or a $ref to another document might read it
//
// A load fails each document that declares such a version or passes
// through it, because it cannot tell whether the check sees the field.
// The report and the error of the load both match [ErrSchemaTypeField].
// Verify gives such a field only this report, even when the schema also
// requires the field outside every branch.
//
// [SchemaTypeFields] on a version states whether its schema reads the type
// fields, so Verify leaves out both reports for that version. With false,
// the check does not see any type field, and Verify reports each type
// field that the schema requires:
//
//	MenuItem v1: $.kind: required, but manifest.SchemaTypeFields(false) hides it
//
// That report matches [ErrUndeclaredTypeField] too.
//
// For the layout that [Unversioned] adds, Verify gives these reports
// about the kind field alone. A document of that layout might write the
// kind field, and it does not write the version field. When a branch or
// such a $ref might read the kind field, a load fails only the documents
// of that layout that write the field.
//
// # Deprecations
//
// Verify also reports each use of the deprecated keyword in the schema of
// the current version whose warning a user cannot follow:
//
//   - A property that the schema requires and marks deprecated. A removal
//     of the property fails the load:
//
//     MenuItem v1: $.spec.legacyPricing: required and deprecated
//
//   - A property that the schema deprecates and the schema of the written
//     version requires. A file at the written version warns about it, and
//     the user can remove it only after [Written] moves:
//
//     Hours v2: $.spec.notes: deprecated, but v1 requires it
//
//   - A deprecated keyword on the root schema, which warns about every
//     document of the kind. [DeprecatedKind] marks a kind deprecated
//     instead:
//
//     Hours v1: $: the root schema is deprecated
//
// Each of these reports matches [ErrUnfollowableDeprecation].
//
// A chain of $ref, $dynamicRef, and allOf without a branch on the way
// joins schemas. So does the anyOf that gen writes around the schema of a
// pointer, whose other branch admits only null. A property schema marks
// its property deprecated or required through its own keywords and those
// of each schema that such a chain reaches from it. The root schema is the
// schema of the version and each schema that such a chain reaches from it.
//
// Verify matches the properties of two versions by path, so it misses a
// property that a step renames between them. It follows each $ref inside
// the schema document, and skips a $ref to another document.
//
// [schema.CheckType] reports the first of these problems for one schema
// and one Go type, without a registry. Its report leaves out the kind and
// the version.
//
// # Defaults
//
// Verify also reports each property that the schema of the current
// version requires, that the document replaces whole, and whose default
// is not zero. Every file must then write the property, so the default
// never applies:
//
//	Cafe v1: $.spec.seats: required, so its default 20 never applies
//
// The report matches [ErrUnusedDefault].
//
// The defaults come from the [niceyaml.WithDefaults] options among the
// decode options of the registry and the kind. A document replaces every
// value whole, except a struct and a pointer to a struct, which a mapping
// merges into field by field. The walk follows properties, items, and
// additionalProperties through the chains of schemas. Under an item, a
// map value, or a nil pointer, it reads the defaults of the type. Under a
// pointer that a parent function sets, it reads the value that the
// function set.
//
// A function of a parent type might set a field of a child type, as the
// function of CafeSpec sets the port of its kitchen Printer. A detail then
// names the default that the child type gives itself, when the default at
// the place differs. The omitzero option on the field makes it optional
// at each place of the child type. A panic in a function of WithDefaults
// reaches the caller.
//
// [schema.CheckType] runs the same check for one schema and one Go type,
// without a registry. A test passes it the decode options that the
// program decodes with. Its report leaves out the kind and the version.
func (r *Registry) Verify(ctx context.Context) error {
	var errs []error

	for _, entry := range r.kinds {
		compiled := make(map[*versionEntry]*jsonschema.Validator)

		for _, v := range entry.versions {
			if !v.cfg.hasSchema {
				continue
			}

			sch, err := r.schemas.Schema(ctx, v.cfg.schema)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s %s: %w", entry.kind.name, v.label(), err))

				continue
			}

			compiled[v] = sch.JSONSchema()
		}

		errs = append(errs, r.verifyTypeFields(entry, compiled)...)
		errs = append(errs, verifyDeprecations(entry, compiled)...)
		errs = append(errs, r.verifyDefaults(entry, compiled)...)
	}

	return errors.Join(errs...)
}

// verifyTypeFields returns the problems of the type fields in the schema
// of each version of entry, as [Registry.Verify] describes. The compiled
// map holds the validator of each version whose schema compiled, or nil
// for a schema that does not check anything.
func (r *Registry) verifyTypeFields(entry *kindEntry, compiled map[*versionEntry]*jsonschema.Validator) []error {
	var errs []error

	for _, v := range entry.versions {
		cv := compiled[v]

		if cv == nil {
			continue
		}

		name := entry.kind.name + " " + v.label()
		fields := r.schemaFields(v, cv)
		doc := schemalint.NewDoc(cv)

		for _, p := range r.typeFields() {
			// An unversioned document does not write a version field, so
			// its schema never sees one.
			if v.cfg.unversioned && p.Equal(r.cfg.versionField) {
				continue
			}

			switch {
			case slices.ContainsFunc(fields.unsure, p.Equal):
				errs = append(errs, authorError(ErrSchemaTypeField,
					fmt.Sprintf("%s: %s: %s", name, p, unsureWhy), unsureRemedy(p)))

			case !slices.ContainsFunc(fields.hidden, p.Equal) || !doc.Field(p).Required:
				// The check sees the field, or the schema does not require
				// it.

			case v.cfg.hasTypeFields:
				errs = append(errs, authorError(ErrUndeclaredTypeField,
					fmt.Sprintf("%s: %s: required, but manifest.SchemaTypeFields(false) hides it", name, p),
					fmt.Sprintf("remove %s from required, or pass manifest.SchemaTypeFields(true)", p),
				))

			default:
				errs = append(errs, authorError(ErrUndeclaredTypeField,
					fmt.Sprintf("%s: %s: required, but no properties keyword declares it", name, p),
					fmt.Sprintf("declare %s under properties, or remove it from required", p),
				))
			}
		}
	}

	return errs
}

// verifyDeprecations returns the problems of the deprecated keywords in
// the schema of the current version of entry, as [Registry.Verify]
// describes. The compiled map holds the validator of each version whose
// schema compiled, or nil for a schema that does not check anything.
func verifyDeprecations(entry *kindEntry, compiled map[*versionEntry]*jsonschema.Validator) []error {
	current := entry.versions[len(entry.versions)-1]

	cv := compiled[current]
	if cv == nil {
		return nil
	}

	name := entry.kind.name + " " + current.label()
	doc := schemalint.NewDoc(cv)

	var errs []error

	if doc.Marked(doc.Root()) {
		errs = append(errs, authorError(ErrUnfollowableDeprecation, name+": $: the root schema is deprecated",
			"use manifest.DeprecatedKind instead"))
	}

	for _, f := range schemalint.RequiredDeprecated(doc) {
		errs = append(errs, authorError(ErrUnfollowableDeprecation, name+": "+f.Message, f.Details...))
	}

	written := entry.written

	wv := compiled[written]
	if written == current || wv == nil {
		return errs
	}

	required := make(map[string]bool)
	for _, p := range schemalint.NewDoc(wv).Properties() {
		required[p.Path.String()] = p.Required
	}

	for _, p := range doc.Properties() {
		if p.Deprecated && required[p.Path.String()] {
			errs = append(errs, authorError(ErrUnfollowableDeprecation,
				fmt.Sprintf("%s: %s: deprecated, but %s requires it", name, p.Path, written.label()),
				fmt.Sprintf("tag the field jsonschema:%q until manifest.Written leaves %s",
					"deprecated=false", written.label()),
			))
		}
	}

	return errs
}

// authorError returns the problem msg of a registry, which matches mark,
// with each of details as a detail. The author of the registry is at fault
// for it rather than a document.
func authorError(mark error, msg string, details ...string) error {
	//nolint:wrapcheck // The error is the problem itself.
	return niceyaml.Unchecked(
		&problemError{msg: msg, marks: []error{mark}},
		niceyaml.WithDetails(detailsOf(details)...),
	)
}

// verifyDefaults returns the problems of the defaults of entry, as the
// Defaults section of [Registry.Verify] describes. Each problem names a
// property that the schema of the current version requires, that the
// document replaces whole, and whose default is not zero. The compiled
// map holds the validator of each version whose schema compiled.
func (r *Registry) verifyDefaults(entry *kindEntry, compiled map[*versionEntry]*jsonschema.Validator) []error {
	current := entry.versions[len(entry.versions)-1]

	cv := compiled[current]
	if cv == nil {
		return nil
	}

	opts := slices.Concat(r.cfg.decodeOptions, entry.kind.cfg.decodeOptions)
	defaults := func(t reflect.Type) reflect.Value {
		p := reflect.New(t)
		_ = niceyaml.DefaultsInto(p.Interface(), opts...) //nolint:errcheck // p is a non-nil pointer.

		return p.Elem()
	}

	name := entry.kind.name + " " + current.label()

	var errs []error

	for _, f := range schemalint.UnusedDefaults(schemalint.NewDoc(cv), entry.kind.typ, defaults) {
		errs = append(errs, authorError(ErrUnusedDefault, name+": "+f.Message, f.Details...))
	}

	return errs
}
