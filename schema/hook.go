package schema

import (
	"go.jacobcolvin.com/x/jsonschema"

	"go.jacobcolvin.com/niceyaml/internal/schemahook"
)

// What the schema package hands to its manifest package, as the package
// [schemahook] describes.
//
//nolint:gochecknoinits // The hooks reach no API, so only init can set them.
func init() {
	schemahook.Validator = func(s any) *jsonschema.Validator {
		sch, _ := s.(*Schema) //nolint:errcheck // The hook takes a *Schema.
		if sch == nil {
			return nil
		}

		return sch.compiled
	}
}
