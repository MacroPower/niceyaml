package encoder

import (
	"io"

	"github.com/goccy/go-yaml"
)

// Pretty returns the [Option] values that make [New] produce
// prettier-friendly YAML: two-space indentation with indented sequences.
// Each call returns a new slice.
func Pretty() []Option {
	return []Option{
		WithIndent(2),
		WithIndentSequence(true),
	}
}

// Encoder writes values as YAML.
//
// Create instances with [New].
type Encoder struct {
	e *yaml.Encoder
}

// Option configures an [Encoder].
//
// Available options:
//   - [WithIndent]
//   - [WithIndentSequence]
//   - [WithYAMLEncodeOptions]
type Option func(*config)

// config collects the go-yaml options that build an [Encoder].
type config struct {
	opts []yaml.EncodeOption
}

// WithIndent is an [Option] that sets the number of spaces per indentation
// level.
func WithIndent(spaces int) Option {
	return func(c *config) {
		c.opts = append(c.opts, yaml.Indent(spaces))
	}
}

// WithIndentSequence is an [Option] that indents sequence entries one level
// below their parent key.
func WithIndentSequence(indent bool) Option {
	return func(c *config) {
		c.opts = append(c.opts, yaml.IndentSequence(indent))
	}
}

// WithYAMLEncodeOptions is an [Option] that passes [yaml.EncodeOption]
// values straight to the underlying [*yaml.Encoder]. It is the escape hatch
// for encoder settings that have no option of their own.
func WithYAMLEncodeOptions(opts ...yaml.EncodeOption) Option {
	return func(c *config) {
		c.opts = append(c.opts, opts...)
	}
}

// New creates a new [*Encoder] that writes to w.
func New(w io.Writer, opts ...Option) *Encoder {
	var c config

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
