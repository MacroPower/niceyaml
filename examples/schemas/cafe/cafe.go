// Package cafe is an example.
package cafe

import (
	"fmt"
	"time"

	"go.jacobcolvin.com/x/jsonschema"

	_ "embed"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/examples/schemas/cafe/spec"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/schema"
)

//go:generate go run ./schemagen/main.go -o cafe.v1.json

var (
	//go:embed cafe.v1.json
	schemaJSON []byte

	configValidator = schema.NewValidator(jsonschema.MustCompileJSON(schemaJSON))

	// DefaultYAML is a valid cafe configuration, used by the demo and tests.
	//go:embed defaults.yaml
	DefaultYAML string

	// BrokenYAML is an invalid cafe configuration that trips several schema
	// constraints, used by the demo and tests to show validation errors.
	//go:embed broken.yaml
	BrokenYAML string
)

// Config is the root cafe configuration.
// Create instances with [NewConfig].
type Config struct {
	// Kind identifies this configuration type.
	Kind string `json:"kind" jsonschema:"title=Kind,const=Config"`
	// Metadata contains identifying information about the cafe.
	Metadata Metadata `json:"metadata" jsonschema:"title=Metadata"`
	// Spec contains the cafe specification.
	Spec spec.Spec `json:"spec" jsonschema:"title=Spec"`
}

// NewConfig creates a new [Config].
func NewConfig() Config {
	return Config{}
}

// ValidateSchema validates arbitrary data against the cafe JSON schema.
func (c Config) ValidateSchema(data any) error {
	//nolint:wrapcheck // Validator.ValidateSchema returns niceyaml.Error with path info.
	return configValidator.ValidateSchema(data)
}

// Validate performs custom validation after decoding.
func (c Config) Validate() error {
	openTime, err := time.Parse("15:04", c.Spec.Hours.Open)
	if err != nil {
		return niceyaml.NewErrorFrom(
			fmt.Errorf("invalid open time: %w", err),
			niceyaml.WithPath(paths.Root().Child("spec", "hours", "open").Value()),
		)
	}

	closeTime, err := time.Parse("15:04", c.Spec.Hours.Close)
	if err != nil {
		return niceyaml.NewErrorFrom(
			fmt.Errorf("invalid close time: %w", err),
			niceyaml.WithPath(paths.Root().Child("spec", "hours", "close").Value()),
		)
	}

	if !openTime.Before(closeTime) {
		return niceyaml.NewError(
			"open must be before close",
			niceyaml.WithPath(paths.Root().Child("spec", "hours", "open").Value()),
		)
	}

	return nil
}

// Metadata contains identifying information about the cafe.
type Metadata struct {
	// Name is the name of the cafe.
	Name string `json:"name" jsonschema:"title=Name,minLength=1,maxLength=100"`
	// Description provides additional details about the cafe.
	Description string `json:"description,omitempty" jsonschema:"title=Description"`
}
