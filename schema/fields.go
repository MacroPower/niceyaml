package schema

import (
	"errors"
	"fmt"
	"reflect"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/genfield"
	"go.jacobcolvin.com/niceyaml/internal/hook"
	"go.jacobcolvin.com/niceyaml/internal/schemalint"
)

var (
	// ErrFieldMismatch matches the error of each field that [CheckFields]
	// reports.
	ErrFieldMismatch = errors.New("generated schema and decoder disagree")

	// ErrUnfollowableDeprecation indicates a deprecated keyword whose
	// warning a user cannot follow, such as one on a property that the
	// schema also requires. [CheckType] reports it, and so does
	// [go.jacobcolvin.com/niceyaml/manifest.Registry.Verify].
	ErrUnfollowableDeprecation = errors.New("deprecation that a user cannot follow")

	// ErrUnusedDefault indicates a property that the schema requires, and
	// whose default from [niceyaml.WithDefaults] is not zero. Every
	// document must write such a property, so the default never applies.
	// [CheckType] reports it, and so does
	// [go.jacobcolvin.com/niceyaml/manifest.Registry.Verify].
	ErrUnusedDefault = errors.New("default that never applies")
)

// CheckFields reports each field where a decode into T and a schema
// that the generator of [go.jacobcolvin.com/x/jsonschema] writes for T
// disagree. It reads the fields of T and of each struct that the decoder
// reaches from T, such as through a field, a pointer, or the elements of
// a slice. It skips each type that decodes itself through an unmarshaler,
// and each type that supplies its own schema through a JSONSchema method.
//
// The unmarshalers us give the check the types that the program decodes
// whole through a [niceyaml.CustomUnmarshaler] or through
// [niceyaml.JSONUnmarshalers]. The check skips those types too. Pass the
// unmarshalers that the program gives its [niceyaml.Source] with
// [niceyaml.WithUnmarshalers], so the check reads T as its decodes do.
// CheckFields applies us in order, as WithUnmarshalers does. A later
// function for a type replaces an earlier one, a nil function removes
// it, and the last JSONUnmarshalers applies. The check does not know the
// schemas that the options of the generator supply. The program pairs
// each type that us decodes whole with such an option, because the
// check does not compare that type with the generator.
//
// A field fails the check when the generator names its property other
// than the key that the decoder reads, or when only one of the two reads
// the field. It fails when one of them reads a struct field inline and
// the other nests it. The Generation section of the package
// documentation gives the rules.
//
// Each failure is an error that matches [ErrFieldMismatch]. Its message
// names the struct and the field, and says how the generator and the
// decoder read the field. Where a tag fixes the field, the message gives
// that tag, such as json:"port". The messages call the generator gen,
// after its go:generate tool. CheckFields returns nil when every field
// agrees, and joins the errors of several fields.
//
// Call CheckFields in a test, as the Generation section of the package
// documentation shows. It reads the tags of T, not a schema. Do not call
// it for a T whose schema you write by hand with the keys that the
// decoder reads. For such a T, it reports fields that the schema matches.
func CheckFields[T any](us ...niceyaml.Unmarshaler) error {
	var errs []error

	for _, problem := range genfield.Check(reflect.TypeFor[T](), hook.Unmarshalers(us)) {
		errs = append(errs, fmt.Errorf("%w: %s", ErrFieldMismatch, problem))
	}

	return errors.Join(errs...)
}

// CheckType reports each property that s requires and deprecates, and
// each default of T under opts that s keeps from ever applying. Pass the
// schema, the type, and the decode options that the program decodes
// with, such as those it passes to [niceyaml.Node.Decode] beside
// [niceyaml.WithValidator]. CheckType returns nil for a nil s and for a
// schema that checks nothing, such as one from [AcceptAll].
//
// CheckType reports each property that s requires and marks deprecated.
// A user who follows the warning and removes the property fails the
// validation, as in:
//
//	$.old: required and deprecated
//
// The error matches [ErrUnfollowableDeprecation].
//
// A chain of $ref, $dynamicRef, and allOf inside the schema document
// joins schemas. So does the anyOf that gen writes around the schema of a
// pointer, whose other branch admits only null. A property schema marks
// its property deprecated or required through its own keywords and those
// of each schema that such a chain reaches from it. CheckType skips every
// other branch, such as one under oneOf or another anyOf, and each $ref to
// another document.
//
// CheckType does not report a deprecated keyword on the root schema,
// which warns on every document.
// [go.jacobcolvin.com/niceyaml/manifest.Registry.Verify] reports that
// keyword on the current version of a kind, which
// [go.jacobcolvin.com/niceyaml/manifest.DeprecatedKind] deprecates
// instead.
//
// CheckType also reports each property that s requires, that a document
// replaces whole, and whose default under opts is not zero. Every
// document must then write the property, so the default never applies:
//
//	$.seats: required, so its default 20 never applies
//
// The error matches [ErrUnusedDefault]. The defaults come from the
// [niceyaml.WithDefaults] options among opts. A document replaces every
// value whole, except a struct and a pointer to a struct, which a mapping
// merges into field by field. The check follows properties, items, and
// additionalProperties through each chain that joins schemas. Under an
// item, a map value, or a nil pointer, it reads the defaults of the type.
// Under a pointer that a parent function sets, it reads the value that
// the function set. A detail names the default that a child type gives
// itself, when a function of a parent type sets it to another value. The
// omitzero option on the field makes it optional at each place of the
// child type. A panic in a function of WithDefaults reaches the caller.
//
// [niceyaml.IsInvalid] reports false for each error, because the schema
// and the program are at fault rather than a document. CheckType joins
// the errors of several properties, and [niceyaml.Problems] yields each
// of them. Call CheckType in a test, beside [CheckFields], as the
// Generation section of the package documentation shows.
func CheckType[T any](s *Schema, opts ...niceyaml.DecodeOption) error {
	if s == nil || s.JSONSchema() == nil {
		return nil
	}

	doc := schemalint.NewDoc(s.JSONSchema())

	var errs []error

	for _, f := range schemalint.RequiredDeprecated(doc) {
		errs = append(errs, lintProblem(ErrUnfollowableDeprecation, f))
	}

	defaults := func(t reflect.Type) reflect.Value {
		p := reflect.New(t)
		_ = niceyaml.DefaultsInto(p.Interface(), opts...) //nolint:errcheck // p is a non-nil pointer.

		return p.Elem()
	}

	for _, f := range schemalint.UnusedDefaults(doc, reflect.TypeFor[T](), defaults) {
		errs = append(errs, lintProblem(ErrUnusedDefault, f))
	}

	return errors.Join(errs...)
}

// lintError is the error of one finding of [CheckType]. Its message is
// the message of the finding, and it matches mark.
type lintError struct {
	mark error
	msg  string
}

// Error returns the message of e.
func (e *lintError) Error() string {
	return e.msg
}

// Is reports whether target is the mark of e.
func (e *lintError) Is(target error) bool {
	return target == e.mark
}

// lintProblem returns the problem of f, which matches mark, with the
// details of f.
func lintProblem(mark error, f schemalint.Finding) error {
	details := make([]error, 0, len(f.Details))
	for _, d := range f.Details {
		details = append(details, errors.New(d))
	}

	//nolint:wrapcheck // The error is the problem itself.
	return niceyaml.Unchecked(&lintError{mark: mark, msg: f.Message}, niceyaml.WithDetails(details...))
}
