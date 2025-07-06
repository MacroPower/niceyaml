package niceyaml

import (
	"io"

	"github.com/goccy/go-yaml"
)

// PrettyEncoderOptions are default encoding options which can be used by
// [NewEncoder] to produce prettier-friendly YAML output.
var PrettyEncoderOptions = []yaml.EncodeOption{
	yaml.Indent(2),
	yaml.IndentSequence(true),
}

// Encoder wraps [yaml.Encoder] for convenience.
type Encoder struct {
	e *yaml.Encoder
}

// NewEncoder wraps [yaml.NewEncoder] to create a new [Encoder].
// Any provided [yaml.EncodeOption]s are passed to the underlying [yaml.Encoder].
func NewEncoder(w io.Writer, opts ...yaml.EncodeOption) *Encoder {
	return &Encoder{
		e: yaml.NewEncoder(w, opts...),
	}
}

// Encode calls [yaml.Encoder.Encode].
func (e *Encoder) Encode(v any) error {
	return e.e.Encode(v) //nolint:wrapcheck // Return the original error.
}

// Close calls [yaml.Encoder.Close].
func (e *Encoder) Close() error {
	return e.e.Close() //nolint:wrapcheck // Return the original error.
}
