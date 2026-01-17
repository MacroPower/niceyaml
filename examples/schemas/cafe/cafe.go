// Package cafe is an example.
package cafe

import (
	"fmt"
	"time"

	_ "embed"

	"github.com/macropower/niceyaml"
	"github.com/macropower/niceyaml/examples/schemas/cafe/spec"
	"github.com/macropower/niceyaml/paths"
	"github.com/macropower/niceyaml/schema"
	"github.com/macropower/niceyaml/schema/validator"
)

//go:generate go run ./schemagen/main.go -o cafe.v1.json

//go:embed cafe.v1.json
var schemaJSON []byte

// Config is the root cafe configuration.
// Create instances with [NewConfig].
type Config struct {
	// Kind identifies this configuration type.
	Kind string `json:"kind" jsonschema:"title=Kind"`
	// Metadata contains identifying information about the cafe.
	Metadata Metadata `json:"metadata" jsonschema:"title=Metadata"`
	// Spec contains the cafe specification.
	Spec spec.Spec `json:"spec" jsonschema:"title=Spec"`
}

// NewConfig creates a new [Config].
func NewConfig() Config {
	return Config{}
}

// JSONSchemaExtend extends the generated JSON schema.
func (c Config) JSONSchemaExtend(js *schema.JSON) {
	kind := schema.MustGetProperty("kind", js)
	kind.Const = "Config"
}

// ValidateSchema validates arbitrary data against the cafe JSON schema.
func (c Config) ValidateSchema(data any) error {
	v, err := validator.New("/cafe.v1.json", schemaJSON)
	if err != nil {
		return fmt.Errorf("create validator: %w", err)
	}

	//nolint:wrapcheck // Validator.ValidateSchema returns niceyaml.Error with path info.
	return v.ValidateSchema(data)
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
	Name string `json:"name" jsonschema:"title=Name"`
	// Description provides additional details about the cafe.
	Description string `json:"description,omitempty" jsonschema:"title=Description"`
}

// JSONSchemaExtend extends the generated JSON schema.
func (m Metadata) JSONSchemaExtend(js *schema.JSON) {
	name := schema.MustGetProperty("name", js)
	name.MinLength = schema.PtrUint64(1)
	name.MaxLength = schema.PtrUint64(100)
}
