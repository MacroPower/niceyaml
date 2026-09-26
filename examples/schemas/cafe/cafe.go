// Package cafe is an example configuration schema with valid and invalid
// sample documents.
package cafe

import (
	_ "embed"

	"go.jacobcolvin.com/niceyaml/examples/schemas/cafe/spec"
	"go.jacobcolvin.com/niceyaml/schema"
)

//go:generate go tool go.jacobcolvin.com/x/jsonschema/cmd/gen -type Config -comments -o cafe.v1.json

var (
	//go:embed cafe.v1.json
	schemaJSON []byte

	// Schema validates a document against the cafe JSON schema. Pass it to
	// Decode with [go.jacobcolvin.com/niceyaml.WithValidator].
	Schema = schema.MustCompile(schemaJSON)

	// DefaultYAML is a valid cafe configuration for the demo and tests.
	//go:embed defaults.yaml
	DefaultYAML string

	// BrokenYAML is an invalid cafe configuration that trips several schema
	// constraints. The demo and tests use it to show validation errors.
	//go:embed broken.yaml
	BrokenYAML string
)

// Config is the root cafe configuration.
type Config struct {
	// Kind identifies this configuration type.
	Kind string `json:"kind" jsonschema:"title=Kind,const=Config"`
	// Metadata contains identifying information about the cafe.
	Metadata Metadata `json:"metadata" jsonschema:"title=Metadata"`
	// Spec contains the cafe specification.
	Spec spec.Spec `json:"spec" jsonschema:"title=Spec"`
}

// Metadata contains identifying information about the cafe.
type Metadata struct {
	// Name is the name of the cafe.
	Name string `json:"name" jsonschema:"title=Name,minLength=1,maxLength=100"`
	// Description provides additional details about the cafe.
	Description string `json:"description,omitempty" jsonschema:"title=Description"`
}
