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

// NewValidator creates a new [*Validator] from a compiled
// [*jsonschema.Validator].
//
// Compile the schema with [jsonschema.CompileJSON], or
// [jsonschema.MustCompileJSON] for embedded schemas known valid at build time.
func NewValidator(v *jsonschema.Validator) *Validator {
	return &Validator{schema: v}
}

// Validator adapts a compiled [*jsonschema.Validator] to
// [niceyaml.SchemaValidator], reporting constraint violations as
// [*niceyaml.Error] values that carry the YAML path to each failing location
// for display by [printer.Printer].
//
// A Validator is safe for concurrent use. Create instances with
// [NewValidator].
type Validator struct {
	schema *jsonschema.Validator
}

// ValidateSchema implements [niceyaml.SchemaValidator].
//
// Returns nil when data conforms. On a constraint violation, returns a
// [*niceyaml.Error]: a single violation carries its YAML path on the error
// itself, and several violations become a count summary whose nested errors
// each carry the path to one failing location. Any other failure wraps
// [ErrValidate]. The context is passed to the underlying
// [jsonschema.Validator], where remote reference resolution honors its
// cancellation and deadlines.
func (v *Validator) ValidateSchema(ctx context.Context, data any) error {
	err := v.schema.Validate(ctx, data)
	if err == nil {
		return nil
	}

	// A structured validation failure carries per-location paths; convert it to
	// a niceyaml.Error. Anything else is an unexpected internal failure.
	if ve, ok := errors.AsType[*jsonschema.ValidationError](err); ok {
		return newValidationError(ve)
	}

	return fmt.Errorf("%w: %w", ErrValidate, err)
}

// newValidationError converts a [*jsonschema.ValidationError] into a
// [*niceyaml.Error].
//
// The error tree is flattened to its concrete failures with
// [jsonschema.ValidationError.Leaves]. A single failure becomes the main
// error, carrying its own path so the printer highlights that location and
// [niceyaml.Error.Path] reports it. Several failures become a count summary
// with no path of its own; each nested error carries the path to one
// failing location.
func newValidationError(ve *jsonschema.ValidationError) *niceyaml.Error {
	leaves := ve.Leaves()

	switch len(leaves) {
	case 0:
		return niceyaml.NewError(ve.Message)
	case 1:
		return leafError(leaves[0])
	}

	causes := make([]*niceyaml.Error, 0, len(leaves))
	for _, leaf := range leaves {
		causes = append(causes, leafError(leaf))
	}

	return niceyaml.NewError(
		fmt.Sprintf("%d schema violations", len(leaves)),
		niceyaml.WithErrors(causes...),
	)
}

// leafError converts one concrete failure into a [*niceyaml.Error] carrying
// the YAML path to the failing location.
func leafError(leaf *jsonschema.ValidationError) *niceyaml.Error {
	return niceyaml.NewError(
		leaf.Message,
		niceyaml.WithPath(buildTargetPath(leaf.InstanceSegments(), leaf.TargetsKey())),
	)
}

// buildTargetPath converts instance-location segments to a [paths.Path],
// pointing at the key when targetsKey is set and the value otherwise. Each
// [jsonschema.Segment] already distinguishes an array index from a property
// name, so no numeric guessing is needed.
func buildTargetPath(segments []jsonschema.Segment, targetsKey bool) paths.Path {
	path := paths.Root()

	for _, seg := range segments {
		if seg.IsIndex {
			path = path.Index(seg.Index)
		} else {
			path = path.Child(seg.Key)
		}
	}

	if targetsKey {
		return path.Key()
	}

	return path.Value()
}
