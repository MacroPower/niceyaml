package encoder

import (
	"context"
	"fmt"
	"io"

	"github.com/goccy/go-yaml"
)

// Pretty returns the [Option] values that make [New] produce
// prettier-friendly YAML with two-space indentation and indented sequences.
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
	w *errWriter
}

// errWriter keeps the first error the writer it wraps returns, since the
// go-yaml encoder discards the result of every write it makes. Once a write
// has failed, every later write returns that error without reaching the
// writer, so a document never lands partly written after a failure.
type errWriter struct {
	w   io.Writer
	err error
}

// Write implements [io.Writer].
func (w *errWriter) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}

	n, err := w.w.Write(p)
	if err != nil {
		w.err = err
	}

	return n, err //nolint:wrapcheck // Return the original error.
}

// Option configures an [Encoder].
//
// Available options:
//   - [WithIndent]
//   - [WithIndentSequence]
//   - [WithYAMLOptions]
type Option func(*config)

// config collects the go-yaml options that build an [Encoder].
type config struct {
	opts []yaml.EncodeOption
}

// WithIndent is an [Option] that sets the number of spaces per indentation
// level. It panics when spaces is below 1, since the encoder would then
// write nested values at the indent of their parent, which reads back as a
// different value.
func WithIndent(spaces int) Option {
	if spaces < 1 {
		panic(fmt.Sprintf("encoder.WithIndent: %d spaces is below 1", spaces))
	}

	return func(c *config) {
		c.opts = append(c.opts, yaml.Indent(spaces))
	}
}

// WithIndentSequence is an [Option] that sets whether the encoder indents
// sequence entries one level below their parent key. The default is false,
// and entries then sit at the indent of their parent key.
func WithIndentSequence(indent bool) Option {
	return func(c *config) {
		c.opts = append(c.opts, yaml.IndentSequence(indent))
	}
}

// WithYAMLOptions is an [Option] that passes [yaml.EncodeOption]
// values straight to the underlying [*yaml.Encoder]. It is the escape hatch
// for encoder settings that have no option of their own.
func WithYAMLOptions(opts ...yaml.EncodeOption) Option {
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

	ew := &errWriter{w: w}

	return &Encoder{
		e: yaml.NewEncoder(ew, c.opts...),
		w: ew,
	}
}

// Encode encodes v as YAML and writes it to the underlying writer. The
// encoder passes ctx to every marshaler that accepts one, such as a
// [yaml.InterfaceMarshalerContext] or a marshaler registered with
// [yaml.RegisterCustomMarshalerContext]. A write the writer refuses is an
// error, and every later call returns that same error without encoding v.
func (e *Encoder) Encode(ctx context.Context, v any) error {
	// After a refused write, v never reaches the go-yaml encoder, so its
	// marshalers do not run and an encoding error cannot hide the write
	// error.
	err := e.writeErr()
	if err != nil {
		return err
	}

	err = e.e.EncodeContext(ctx, v)
	if err != nil {
		return err //nolint:wrapcheck // Return the original error.
	}

	return e.writeErr()
}

// Close releases the encoder's resources and reports the write error, if
// any, from an earlier [Encoder.Encode]. It does not flush the writer,
// so a caller holding a buffered writer flushes it after Close.
func (e *Encoder) Close() error {
	err := e.e.Close()
	if err != nil {
		return err //nolint:wrapcheck // Return the original error.
	}

	return e.writeErr()
}

// writeErr returns the first error the writer returned, wrapped with the
// "write YAML" prefix, or nil when every write succeeded.
func (e *Encoder) writeErr() error {
	if e.w.err != nil {
		return fmt.Errorf("write YAML: %w", e.w.err)
	}

	return nil
}
