package niceyaml

import (
	"context"
)

// Decoder decodes and validates [Node] values with [DecodeOption] values
// stated once, so a file that holds many documents, or a program that
// reads many files, names its schema and its decoder settings in one
// place:
//
//	dec := niceyaml.NewDecoder(
//		niceyaml.WithValidator(reg),
//		niceyaml.WithDisallowUnknownFields(true),
//	)
//
//	docs, err := source.Documents()
//	if err != nil {
//		return err
//	}
//
//	for _, doc := range docs {
//		manifest, err := dec.Decode[Manifest](ctx, doc)
//		...
//	}
//
// [Decoder.Decode] and [Decoder.DecodeInto] run the pipeline
// [Node.DecodeInto] describes with the options of the Decoder, on
// whatever Node the caller hands them: a root from [Source.Documents] or a
// scoped Node from [Node.At]. [Decoder.Validate] runs the validation
// step alone. [Node.Decode] with the same options decodes one node the
// same way, so a Decoder is the options of a call held for reuse.
//
// A Decoder never changes after [NewDecoder], so it is safe for
// concurrent use. [Decoder.With] returns a new Decoder with more options
// applied.
//
// Create instances with [NewDecoder].
type Decoder struct {
	cfg decodeConfig
}

// NewDecoder creates a new [*Decoder] with the given options applied in
// order. Without options, it decodes as [Node.Decode] does without
// options: no [Validator] runs before the decode, and the decoded value
// validates itself after it.
func NewDecoder(opts ...DecodeOption) *Decoder {
	return &Decoder{cfg: newDecodeConfig(opts)}
}

// With returns a new [*Decoder] with opts applied over the options of
// the receiver, in order. A [WithValidator] adds to the validators the
// receiver holds, and a setting replaces the one the receiver holds. The
// receiver is unchanged, so a Decoder shared between callers can be
// specialized per use:
//
//	strict := dec.With(niceyaml.WithDisallowUnknownFields(true))
func (d *Decoder) With(opts ...DecodeOption) *Decoder {
	cfg := d.cfg.clone()

	for _, opt := range opts {
		opt(&cfg)
	}

	return &Decoder{cfg: cfg}
}

// Validate runs the validators of the [Decoder] on n in the order
// [NewDecoder] took them and stops at the first that fails, as [Node.Validate] runs
// the validators the caller passes. It is the validation step of
// [Decoder.Decode] on its own, for a caller that checks a document
// without decoding it. A Decoder without validators returns nil.
func (d *Decoder) Validate(ctx context.Context, n *Node) error {
	return n.validate(ctx, d.cfg.validators)
}

// DecodeInto validates and decodes n into v with the options of the
// [Decoder], as [Node.DecodeInto] decodes it with the same options. Any
// v that is not a non-nil pointer returns [ErrDecodeTarget] before
// anything runs.
func (d *Decoder) DecodeInto(ctx context.Context, n *Node, v any) error {
	return n.decodeInto(ctx, v, d.cfg)
}

// Decode validates and decodes n into a new T with the options of the
// [Decoder], as [Node.Decode] decodes it with the same options. On error,
// the returned T is the zero value.
func (d *Decoder) Decode[T any](ctx context.Context, n *Node) (T, error) {
	var v T

	err := d.DecodeInto(ctx, n, &v)
	if err != nil {
		var zero T

		return zero, err
	}

	return v, nil
}
