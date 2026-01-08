// Package cafe is an example.
package cafe

import (
	"github.com/invopop/jsonschema"

	_ "embed"

	"github.com/macropower/niceyaml/examples/schemas/cafe/spec"
	"github.com/macropower/niceyaml/schema"
	"github.com/macropower/niceyaml/schema/validator"
)

//go:generate go run ./schemagen/main.go -o cafe.v1.json

var (
	//go:embed cafe.v1.json
	schemaJSON []byte

	// DefaultValidator validates configuration against the JSON schema.
	DefaultValidator = validator.MustNew("/cafe.v1.json", schemaJSON)
)

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
func (c Config) JSONSchemaExtend(js *jsonschema.Schema) {
	kind := schema.MustGetProperty("kind", js)
	kind.Const = "Config"
}

// Validate extends schema-driven validation with custom validation logic.
func (c Config) Validate() error {
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
func (m Metadata) JSONSchemaExtend(js *jsonschema.Schema) {
	name := schema.MustGetProperty("name", js)
	name.MinLength = schema.PtrUint64(1)
	name.MaxLength = schema.PtrUint64(100)
}
