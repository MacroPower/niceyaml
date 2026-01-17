package validator

import (
	"errors"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"github.com/macropower/niceyaml"
)

// jsonschemaAdapter wraps [*jsonschema.Schema] to implement [Schema].
type jsonschemaAdapter struct {
	schema *jsonschema.Schema
}

// Validate implements [Schema].
// Validation errors are wrapped in [jsonschemaValidationError] to implement [SchemaError].
func (a *jsonschemaAdapter) Validate(data any) error {
	err := a.schema.Validate(data)
	if err == nil {
		return nil
	}

	var validationErr *jsonschema.ValidationError
	if errors.As(err, &validationErr) {
		return newJSONSchemaValidationError(validationErr)
	}

	//nolint:wrapcheck // Non-validation errors passed through as-is.
	return err
}

// jsonschemaValidationError wraps [*jsonschema.ValidationError] to implement [SchemaError].
type jsonschemaValidationError struct {
	err *jsonschema.ValidationError
	p   *message.Printer
}

// newJSONSchemaValidationError creates a [SchemaError] from a [*jsonschema.ValidationError].
func newJSONSchemaValidationError(err *jsonschema.ValidationError) *jsonschemaValidationError {
	return &jsonschemaValidationError{
		err: err,
		p:   message.NewPrinter(language.English),
	}
}

// Error implements [error].
func (e *jsonschemaValidationError) Error() string {
	return e.err.Error()
}

// Path implements [SchemaError].
func (e *jsonschemaValidationError) Path() *niceyaml.Path {
	location := e.err.InstanceLocation

	// Append additional property name for AdditionalProperties errors.
	if ap, ok := e.err.ErrorKind.(*kind.AdditionalProperties); ok && len(ap.Properties) > 0 {
		location = append(location, ap.Properties[0])
	}

	return buildPathFromLocation(location)
}

// Message implements [SchemaError].
func (e *jsonschemaValidationError) Message() string {
	return e.err.ErrorKind.LocalizedString(e.p)
}

// Causes implements [SchemaError].
func (e *jsonschemaValidationError) Causes() []SchemaError {
	if len(e.err.Causes) == 0 {
		return nil
	}

	causes := make([]SchemaError, len(e.err.Causes))
	for i, cause := range e.err.Causes {
		causes[i] = newJSONSchemaValidationError(cause)
	}

	return causes
}

// IsWrapper implements [SchemaError].
func (e *jsonschemaValidationError) IsWrapper() bool {
	switch e.err.ErrorKind.(type) {
	case *kind.Schema, *kind.Group, *kind.AllOf, *kind.AnyOf, *kind.OneOf, *kind.Not, *kind.Reference:
		return true
	}

	return false
}

// PathTarget implements [SchemaError].
func (e *jsonschemaValidationError) PathTarget() niceyaml.PathTarget {
	switch e.err.ErrorKind.(type) {
	case *kind.AdditionalProperties, *kind.PropertyNames, *kind.Required,
		*kind.MinItems, *kind.MaxItems, *kind.UniqueItems,
		*kind.Contains, *kind.MinContains, *kind.MaxContains,
		*kind.MinProperties, *kind.MaxProperties:
		return niceyaml.PathKey
	default:
		return niceyaml.PathValue
	}
}

// URL implements [SchemaError].
func (e *jsonschemaValidationError) URL() string {
	return e.err.SchemaURL
}

// isSchemaKind returns true if the error is of type [*kind.Schema].
func (e *jsonschemaValidationError) isSchemaKind() bool {
	_, ok := e.err.ErrorKind.(*kind.Schema)

	return ok
}

// buildPathFromLocation converts an InstanceLocation string slice to a [niceyaml.Path].
func buildPathFromLocation(location []string) *niceyaml.Path {
	current := niceyaml.NewPathBuilder()

	if len(location) == 0 {
		// Root level error.
		return current.Build()
	}

	for _, part := range location {
		// Check if this part is a numeric index.
		var index uint

		_, err := fmt.Sscanf(part, "%d", &index)
		if err == nil {
			// This is an array index.
			current = current.Index(index)
		} else {
			// Regular property name.
			current = current.Child(part)
		}
	}

	return current.Build()
}

// defaultCompiler wraps [*jsonschema.Compiler] to implement [SchemaCompiler] with the new [Schema] return type.
type defaultCompiler struct {
	c *jsonschema.Compiler
}

// newDefaultCompiler creates a new [defaultCompiler].
func newDefaultCompiler() *defaultCompiler {
	return &defaultCompiler{c: jsonschema.NewCompiler()}
}

// AddResource implements [SchemaCompiler].
func (d *defaultCompiler) AddResource(url string, doc any) error {
	//nolint:wrapcheck // Transparent wrapper; errors handled by caller.
	return d.c.AddResource(url, doc)
}

// Compile implements [SchemaCompiler].
func (d *defaultCompiler) Compile(url string) (Schema, error) {
	s, err := d.c.Compile(url)
	if err != nil {
		//nolint:wrapcheck // Transparent wrapper; errors handled by caller.
		return nil, err
	}

	return &jsonschemaAdapter{schema: s}, nil
}
