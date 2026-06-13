package schema

import (
	"context"
	"errors"
	"fmt"

	"go.jacobcolvin.com/x/jsonschema"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/paths"
)

// ErrValidate indicates an unexpected, non-validation failure while validating
// against a schema, such as a reference resolution problem. Schema constraint
// violations are reported as [*niceyaml.Error] values with path information
// instead of wrapping this sentinel.
var ErrValidate = errors.New("validate schema")

// NewValidator adapts a compiled [*jsonschema.Validator] to a
// [niceyaml.SchemaValidator], reporting constraint violations as
// [*niceyaml.Error] values that carry the YAML path to each failing location
// for display by [niceyaml.Printer].
//
// Compile the schema with [jsonschema.CompileJSON], or
// [jsonschema.MustCompileJSON] for embedded schemas known valid at build time.
func NewValidator(v *jsonschema.Validator) niceyaml.SchemaValidator {
	return &validator{schema: v}
}

// validator is the [niceyaml.SchemaValidator] returned by [NewValidator].
type validator struct {
	schema *jsonschema.Validator
}

// ValidateSchema implements [niceyaml.SchemaValidator].
//
// Returns nil when data conforms. On a constraint violation, returns a
// [*niceyaml.Error] whose nested errors each carry the YAML path to a failing
// location. Any other failure wraps [ErrValidate].
func (v *validator) ValidateSchema(data any) error {
	err := v.schema.Validate(context.Background(), data)
	if err == nil {
		return nil
	}

	// A structured validation failure carries per-location paths; convert it to
	// a niceyaml.Error. Anything else is an unexpected internal failure.
	var ve *jsonschema.ValidationError

	if errors.As(err, &ve) {
		return newValidationError(ve)
	}

	return fmt.Errorf("%w: %w", ErrValidate, err)
}

// newValidationError converts a [*jsonschema.ValidationError] into a
// [*niceyaml.Error] whose nested errors each carry the YAML path to a failing
// location.
//
// The error tree is flattened to its concrete failures with
// [jsonschema.ValidationError.Leaves]. A single failure becomes the main
// message; several become a count summary.
func newValidationError(ve *jsonschema.ValidationError) *niceyaml.Error {
	leaves := ve.Leaves()

	var mainMsg string

	switch len(leaves) {
	case 0:
		mainMsg = ve.Message
	case 1:
		mainMsg = leaves[0].Message
	default:
		mainMsg = fmt.Sprintf("validation failed at %d locations", len(leaves))
	}

	causes := make([]*niceyaml.Error, 0, len(leaves))
	for _, leaf := range leaves {
		causes = append(causes, niceyaml.NewError(
			leaf.Message,
			niceyaml.WithPath(buildTargetPath(leaf.InstanceSegments(), leaf.TargetsKey())),
		))
	}

	// Don't set a main path; the nested errors handle highlighting.
	return niceyaml.NewError(mainMsg, niceyaml.WithErrors(causes...))
}

// buildTargetPath converts instance-location segments to a [*paths.Path],
// pointing at the key when targetsKey is set and the value otherwise. Each
// [jsonschema.Segment] already distinguishes an array index from a property
// name, so no numeric guessing is needed.
func buildTargetPath(segments []jsonschema.Segment, targetsKey bool) *paths.Path {
	builder := paths.Root()

	for _, seg := range segments {
		if seg.IsIndex {
			builder = builder.Index(seg.Index)
		} else {
			builder = builder.Child(seg.Key)
		}
	}

	if targetsKey {
		return builder.Key()
	}

	return builder.Value()
}
