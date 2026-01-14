package validator

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"github.com/macropower/niceyaml"
)

var (
	// ErrUnmarshalSchema indicates the schema JSON could not be parsed.
	ErrUnmarshalSchema = errors.New("unmarshal schema")

	// ErrCompileSchema indicates the schema failed to compile.
	ErrCompileSchema = errors.New("compile schema")
)

// Validator validates data against a compiled JSON schema and returns errors
// with YAML path information. Implements the [niceyaml.SchemaValidator] interface
// for use with [niceyaml.DocumentDecoder]. Uses [github.com/santhosh-tekuri/jsonschema/v6].
// Create instances with [New] or [MustNew].
type Validator struct {
	schema *jsonschema.Schema
}

// New creates a new [Validator] from JSON schema data.
// The url parameter is the schema's identifier used for reference resolution.
// Returns an error if the schema JSON is invalid or fails to compile.
func New(url string, schemaData []byte) (*Validator, error) {
	var schema any

	err := json.Unmarshal(schemaData, &schema)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnmarshalSchema, err)
	}

	compiler := jsonschema.NewCompiler()
	err = compiler.AddResource(url, schema)
	if err != nil {
		return nil, fmt.Errorf("add schema resource: %w", err)
	}

	jss, err := compiler.Compile(url)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCompileSchema, err)
	}

	return &Validator{schema: jss}, nil
}

// MustNew is like [New] but panics on error.
// Use for schemas known to be valid at compile time, such as embedded schemas.
func MustNew(url string, schemaData []byte) *Validator {
	v, err := New(url, schemaData)
	if err != nil {
		panic(err)
	}

	return v
}

// ValidateSchema validates the given data against the schema.
// Returns nil if validation succeeds. On validation failure, returns a
// [niceyaml.Error] containing the YAML path to the invalid field for use
// with [niceyaml.Printer] for rich error display.
func (s *Validator) ValidateSchema(data any) error {
	// Validate against schema.
	err := s.schema.Validate(data)
	if err == nil {
		return nil
	}

	// Convert validation error to our custom error type with path information.
	var validationErr *jsonschema.ValidationError
	if !errors.As(err, &validationErr) {
		return fmt.Errorf("schema validation: %w", err)
	}

	p := message.NewPrinter(language.English)

	var errMsg strings.Builder
	if _, ok := validationErr.ErrorKind.(*kind.Schema); ok {
		errMsg.WriteString(fmt.Sprintf("jsonschema validation failed with %q:", filepath.Base(validationErr.SchemaURL)))
	} else {
		errMsg.WriteString(validationErr.ErrorKind.LocalizedString(p))
	}

	for _, cause := range validationErr.Causes {
		errMsg.WriteString("\n* " + cause.LocalizedError(p))
	}

	// Build the path from the validation error.
	path, pathErr := buildYAMLPathFromError(validationErr)
	if pathErr != nil {
		// If we can't build the path, still return a useful error.
		return niceyaml.NewError(fmt.Errorf("schema validation: %w", validationErr))
	}

	return niceyaml.NewError(errors.New(errMsg.String()), niceyaml.WithPath(path))
}

// buildYAMLPathFromError creates a [yaml.Path] from the provided
// [jsonschema.ValidationError].
func buildYAMLPathFromError(validationErr *jsonschema.ValidationError) (*yaml.Path, error) {
	// Check for AdditionalProperties errors first - these contain the
	// specific property name that should be included in the path.
	if location := findAdditionalProperty(validationErr); location != nil {
		return buildPathFromLocation(location)
	}

	// Fall back to finding the most specific location for other error types.
	mostSpecificLocation := findMostSpecificLocation(validationErr)

	return buildPathFromLocation(mostSpecificLocation)
}

// findMostSpecificLocation recursively searches through all causes to find the
// one with the longest InstanceLocation.
func findMostSpecificLocation(err *jsonschema.ValidationError) []string {
	longest := err.InstanceLocation

	// Recursively check all causes.
	for _, cause := range err.Causes {
		candidateLocation := findMostSpecificLocation(cause)
		if len(candidateLocation) > len(longest) {
			longest = candidateLocation
		}
	}

	return longest
}

// findAdditionalProperty recursively searches for an AdditionalProperties error
// and returns the location extended with the first invalid property name.
// Returns nil if no AdditionalProperties error is found.
func findAdditionalProperty(err *jsonschema.ValidationError) []string {
	if ap, ok := err.ErrorKind.(*kind.AdditionalProperties); ok && len(ap.Properties) > 0 {
		return append(err.InstanceLocation, ap.Properties[0])
	}

	for _, cause := range err.Causes {
		if result := findAdditionalProperty(cause); result != nil {
			return result
		}
	}

	return nil
}

// buildPathFromLocation converts an InstanceLocation slice to a [yaml.Path].
func buildPathFromLocation(location []string) (*yaml.Path, error) {
	current := niceyaml.NewPathBuilder()

	if len(location) == 0 {
		// Root level error.
		return current.Build(), nil
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

	return current.Build(), nil
}
