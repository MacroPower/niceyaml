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
// scoped Node from [Node.At]. A [Validator] the Decoder carries sees the
// same Node, so a Decoder that carries one for whole documents only, as
// a [go.jacobcolvin.com/niceyaml/schema.Registry] is, decodes roots.
// [Decoder.Validate] runs the validation step alone, and
// [Decoder.SelfValidate] runs the self-validation step alone, on a value
// the caller may have changed since the decode. [Node.Decode] with
// the same options decodes one node the same way, so a Decoder is the
// options of a call held for reuse.
//
// A Decoder holds how a decode runs, and the [Source] holds what its
// documents mean. A Decoder therefore carries no reference documents. A
// document whose aliases name anchors of another file gets that file
// through [WithReferences] on its Source, and every Decoder, validator,
// and [Node] method then reads the aliases alike:
//
//	defaults := niceyaml.NewSourceFromBytes(defaultsYAML, niceyaml.WithName("defaults.yaml"))
//
//	source, err := niceyaml.NewSourceFromFile(path, niceyaml.WithReferences(defaults))
//	if err != nil {
//		return err
//	}
//
// A Decoder carries no setting of the alias limit either. [WithAliasLimit]
// on the Source turns the limit off for every Decoder and validator that
// reads its documents.
//
// A Decoder never changes after [NewDecoder], so it is safe for
// concurrent use as long as the go-yaml options it carries hold no state,
// which [WithYAMLDecodeOptions] describes. [Decoder.With] returns a new
// Decoder with more options applied. The zero Decoder decodes as one from
// NewDecoder without options does.
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
// the receiver, in order. [WithValidator] appends to the validators the
// receiver holds, and [WithYAMLDecodeOptions] appends to its go-yaml
// options. [WithSelfValidation] and
// [WithDisallowUnknownFields] replace the setting the receiver holds. The
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

// Validate runs the validators of the [Decoder] on n as [ChainValidator]
// runs them. It runs them in the order its options gave them, those of
// [NewDecoder] first and then those each [Decoder.With] appended, and
// stops at the first that fails. It is the validation step of
// [Decoder.Decode] on its own, for a caller that checks a document
// without decoding it. A Decoder without validators returns nil for a
// document that parsed. A document that did not parse returns the syntax
// error [Node.Err] returns, whatever validators the Decoder holds.
func (d *Decoder) Validate(ctx context.Context, n *Node) error {
	return n.validate(ctx, d.cfg.validators)
}

// DecodeInto validates and decodes n into v with the options of the
// [Decoder], as [Node.DecodeInto] decodes it with the same options. Any
// v that is not a non-nil pointer returns an error wrapping
// [ErrDecodeTarget], bound to the source, before anything runs.
func (d *Decoder) DecodeInto(ctx context.Context, n *Node, v any) error {
	return n.decodeInto(ctx, v, d.cfg)
}

// SelfValidate runs the self-validation step of [Decoder.DecodeInto] on
// its own, on v with the go-yaml options of the [Decoder], as
// [Node.SelfValidate] runs it with the same options. It
// runs whatever [WithSelfValidation] says, and runs no [Validator] of the
// Decoder. A Decoder with self-validation off thus decodes a file, and
// validates the value once the caller has applied its other layers:
//
//	dec := niceyaml.NewDecoder(niceyaml.WithSelfValidation(false))
//
//	var cfg Config
//	if err := dec.DecodeInto(ctx, doc, &cfg); err != nil {
//		return err
//	}
//
//	applyEnv(&cfg)
//
//	return dec.SelfValidate(ctx, doc, &cfg)
//
// On a value that no layer changed, SelfValidate returns what DecodeInto
// returns with the walk on.
func (d *Decoder) SelfValidate(ctx context.Context, n *Node, v any) error {
	return n.selfValidate(ctx, v, d.cfg, nil)
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
