package niceyaml

import (
	"io"

	"github.com/goccy/go-yaml"
)

// PrettyEncoderOptions are the [EncoderOption] values that make [NewEncoder]
// produce prettier-friendly YAML: two-space indentation with indented
// sequences.
var PrettyEncoderOptions = []EncoderOption{
	WithIndent(2),
	WithIndentSequence(true),
}

// Encoder writes values as YAML.
//
// Create instances with [NewEncoder].
type Encoder struct {
	e *yaml.Encoder
}

// EncoderOption configures an [Encoder].
//
// Available options:
//   - [WithIndent]
//   - [WithIndentSequence]
//   - [WithYAMLEncodeOptions]
type EncoderOption func(*encoderConfig)

// encoderConfig collects the go-yaml options that build an [Encoder].
type encoderConfig struct {
	opts []yaml.EncodeOption
}

// WithIndent is an [EncoderOption] that sets the number of spaces per
// indentation level.
func WithIndent(spaces int) EncoderOption {
	return func(c *encoderConfig) {
		c.opts = append(c.opts, yaml.Indent(spaces))
	}
}

// WithIndentSequence is an [EncoderOption] that indents sequence entries one
// level below their parent key.
func WithIndentSequence(indent bool) EncoderOption {
	return func(c *encoderConfig) {
		c.opts = append(c.opts, yaml.IndentSequence(indent))
	}
}

// WithYAMLEncodeOptions is an [EncoderOption] that passes [yaml.EncodeOption]
// values straight to the underlying [*yaml.Encoder]. It is the escape hatch
// for encoder settings that have no option of their own.
func WithYAMLEncodeOptions(opts ...yaml.EncodeOption) EncoderOption {
	return func(c *encoderConfig) {
		c.opts = append(c.opts, opts...)
	}
}

// NewEncoder creates a new [*Encoder] that writes to w.
func NewEncoder(w io.Writer, opts ...EncoderOption) *Encoder {
	var c encoderConfig

	for _, opt := range opts {
		opt(&c)
	}

	return &Encoder{
		e: yaml.NewEncoder(w, c.opts...),
	}
}

// Encode encodes v as YAML and writes it to the underlying writer.
func (e *Encoder) Encode(v any) error {
	return e.e.Encode(v) //nolint:wrapcheck // Return the original error.
}

// Close flushes the encoder and releases its resources.
func (e *Encoder) Close() error {
	return e.e.Close() //nolint:wrapcheck // Return the original error.
}
