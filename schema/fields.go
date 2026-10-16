package schema

import (
	"errors"
	"fmt"
	"reflect"

	"go.jacobcolvin.com/niceyaml/internal/genfield"
)

// ErrFieldMismatch matches the error of each field that [CheckFields]
// reports.
var ErrFieldMismatch = errors.New("generated schema and decoder disagree")

// CheckFields reports each field where a decode into T and a schema
// that the generator of [go.jacobcolvin.com/x/jsonschema] writes for T
// disagree. It reads the fields of T and of each struct that the decoder
// reaches from T, such as through a field, a pointer, or the elements of
// a slice. It skips each type that decodes itself through an unmarshaler,
// and each type that supplies its own schema through a JSONSchema method.
// It does not know the unmarshalers that the options of a decode add, or
// the schemas that the options of the generator supply.
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
func CheckFields[T any]() error {
	var errs []error

	for _, problem := range genfield.Check(reflect.TypeFor[T]()) {
		errs = append(errs, fmt.Errorf("%w: %s", ErrFieldMismatch, problem))
	}

	return errors.Join(errs...)
}
