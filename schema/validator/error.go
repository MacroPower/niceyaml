package validator

import (
	"fmt"
	"path/filepath"

	"jacobcolvin.com/niceyaml"
	"jacobcolvin.com/niceyaml/paths"
)

// concreteError holds a concrete validation error with its computed path.
type concreteError struct {
	err  SchemaError
	path *paths.Path
}

// newValidationError creates a [*niceyaml.Error] from a [SchemaError].
func newValidationError(err SchemaError) *niceyaml.Error {
	// Collect all concrete errors from the cause tree with their paths.
	concreteErrors := collectConcreteErrorsWithPaths(err)

	// Build summary message based on error count.
	var mainMsg string

	switch len(concreteErrors) {
	case 0:
		// Fallback to original error message if no concrete errors found.
		mainMsg = err.Message()
	case 1:
		// Single error: use its message directly.
		mainMsg = concreteErrors[0].err.Message()
	default:
		// Multiple errors: use summary message.
		// Check if this is a schema-level error for a nicer message.
		adapter, ok := err.(*jsonschemaValidationError)
		if ok && adapter.isSchemaKind() {
			mainMsg = fmt.Sprintf("validation failed at %d locations with %q",
				len(concreteErrors), filepath.Base(err.URL()))
		} else {
			mainMsg = fmt.Sprintf("validation failed at %d locations", len(concreteErrors))
		}
	}

	// Build cause errors with their paths.
	causes := make([]*niceyaml.Error, 0, len(concreteErrors))
	for _, concrete := range concreteErrors {
		causes = append(
			causes,
			niceyaml.NewError(
				concrete.err.Message(),
				niceyaml.WithPath(concrete.path),
			),
		)
	}

	// Don't set main path - let nested errors handle highlighting.
	return niceyaml.NewError(
		mainMsg,
		niceyaml.WithErrors(causes...),
	)
}

// collectConcreteErrorsWithPaths recursively collects all concrete
// (non-wrapper) errors from the validation error tree along with their
// computed paths.
func collectConcreteErrorsWithPaths(err SchemaError) []concreteError {
	var results []concreteError

	if err.IsWrapper() {
		// Wrapper kinds - recurse into causes.
		for _, cause := range err.Causes() {
			results = append(results, collectConcreteErrorsWithPaths(cause)...)
		}
	} else {
		// Concrete error kind - collect it with path info.
		results = append(results, concreteError{err: err, path: err.Path()})
	}

	return results
}
