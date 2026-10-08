package niceyaml

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

var (
	// ErrUnnamedKey indicates a mapping key that [Layers] cannot merge
	// by. A key that is a sequence or a mapping has no name a path
	// selects it by, as
	// [go.jacobcolvin.com/niceyaml/paths.Resolver.KeyName] reports, so
	// two layers cannot hold it as one key. The error binds at the key,
	// in the layer that holds it.
	ErrUnnamedKey = errors.New("mapping key has no name")

	// The error of a layer that decodes only with the go-yaml options of
	// a call, as [Layers.layerError] finds one.
	errOptionReference = errors.New(
		"layer reads a reference document from a decode option, which belongs in WithReferences",
	)

	// The Node that [Layers] with no Node validate and bind through: the
	// one document of an empty [Source], which has no name. Every call
	// shares the one Node, which never changes.
	noLayers = sync.OnceValue(func() *Node {
		return NewSourceFromString("").documents()[0]
	})
)

// Layers holds the Nodes that merge into one document, in the order they
// apply, such as a base file with the file of one environment over it:
//
//	layers := niceyaml.NewLayers(base, prod)
//
//	cfg, err := layers.Decode[Config](ctx)
//
// [Layers.Decode] and [Layers.DecodeInto] validate, decode, and
// self-validate the merged document once. Each error binds in the file
// that holds the value the error is about, so a port that only base.yaml
// sets reports the line that sets it:
//
//	base.yaml:3:9: $.server.port: port must be at least 1
//
// A program that applies its environment or its flags after the last
// file turns the self-validation step off for the decode, and runs it
// with [Layers.SelfValidate] once the value is whole:
//
//	var cfg Config
//	if err := layers.DecodeInto(ctx, &cfg, niceyaml.WithSelfValidation(false)); err != nil {
//		return err
//	}
//
//	applyEnv(&cfg)
//
//	return layers.SelfValidate(ctx, &cfg)
//
// [Layers.Validate] runs a [Validator] on the merged document without a
// decode, and [Layers.Bind] binds the error of a check the program runs
// itself.
//
// The layers merge as documents, whatever Go type the result decodes
// into. A mapping in a higher layer merges into the mapping the layers
// below hold at the same path, key by key and at every depth, so
// prod.yaml changes one field of one service and keeps the rest of
// base.yaml:
//
//	# base.yaml                # prod.yaml
//	services:                  services:
//	  web: {image: nginx}        web: {replicas: 5}
//	  db: {image: postgres}
//
// The merged document holds the service web with its image and its
// replicas, and the service db. A sequence or a scalar in a higher layer
// replaces what the layers below hold at its path, whole, and a mapping
// replaces a sequence or a scalar the same way. A sequence never merges
// element by element. An empty mapping holds no key to merge, so it
// keeps the mapping below it as it is.
//
// A null in a higher layer keeps the value of the layers below, so a key
// whose entries are all commented out changes nothing. That holds for
// every Go type the value decodes into, a pointer field included, where
// a null in one document sets the pointer to nil. A higher layer thus
// has no way to unset a value that a lower layer sets. The merged
// document holds a null only where no lower layer holds a value.
//
// Two keys are one key when a path selects them by the same name, as
// [go.jacobcolvin.com/niceyaml/paths.Resolver.KeyName] gives it. The
// keys 80 and "80" thus merge, and the merged document spells the key as
// the highest layer that holds a value under it does. The keys 80 and
// 0x50 have two names, so both stay, as they do in one file that holds
// both. A key that is a sequence or a mapping has no name, and a layer
// that holds one returns an error matching [ErrUnnamedKey], bound at
// the key.
//
// Each layer resolves its own aliases and `<<` merge keys before it
// merges, as a decode of that layer alone reads them, with the reference
// documents of [WithReferences] and the other settings of its own
// [Source]. An alias thus reads the anchor of its own file, whatever
// anchors of that name the other layers define, and a value that an
// alias or a merge key brings in merges as one written in its place
// does. The merged document holds a copy of that value and no alias.
// The merge writes each alias out in full, so a layer must pass the
// alias limit of [WithAliasLimit] as a decode into a type that reads
// text does.
//
// The go-yaml options of a decode reach the merged document and no
// layer. An alias that only a reference from [WithYAMLDecodeOptions]
// defines, such as [yaml.ReferenceFiles], [yaml.ReferenceDirs], or
// [yaml.ReferenceReaders], thus does not resolve, and the error of the
// decode names [WithReferences]. That option gives the reference
// documents to the Source of the layer that reads them.
//
// [Layers.Document] returns the root Node of the merged document, and a
// [Validator] gets that Node. The document belongs to a [Source] of its
// own, which holds the merged value as YAML text under the name of the
// lowest layer. Document describes that text, which is no file of the
// program.
//
// An error never binds in that text. An error with a path binds in the
// highest layer whose document holds the value at that path, and the
// path resolves in the file of that layer, through its aliases and merge
// keys as [Node.Bind] resolves it. A higher layer that holds a null
// there holds no value. An error at a mapping that several layers hold
// binds in the highest of them. An error under a sequence binds in the
// layer that holds the sequence, and never in a layer below it, whose
// elements the merge discarded. An error with a position or a range in
// the merged text binds at the value that lies there, as a path to that
// value does. An error with no location binds in the lowest layer with
// no position, as in "base.yaml: quota service: connection refused".
//
// When no layer holds the value, the error binds at the key of the
// mapping that lacks it, as [Node.Bind] binds the path of a missing key.
// It binds in the layer whose mapping lies deepest along the path, and
// in the highest of several such layers.
//
// The bound error reports the [SourceError.Source] and the
// [SourceError.Node] of the layer it binds in. Its message and
// [SourceError.Path] carry the path from the root of the document of
// that layer. A Node from [Node.At] holds the value at a path of its
// own file, so an error at `$.server.port` of the merged document reads
// `$.defaults.server.port` where it binds in a layer that holds the
// value under defaults.
//
// A Node from [Layers.Document] is a layer like any other. Layers that
// hold one merge its value as they merge a file of the same text. Each
// error still binds in the file that holds its value, with the path
// that file has for it.
//
// The decode still fills the Go value by the rule of [Node.DecodeInto],
// once, from the merged document. A value that holds defaults keeps each
// field the merged document leaves out, and a mapping of the document
// replaces a map the value holds, whole. Defaults that should merge key
// by key go in a layer, such as an embedded file below the others.
//
// One limit remains. A value that the environment or a flag set binds at
// whatever a file holds at its path, as [Node.SelfValidate] describes.
//
// A layer whose document did not parse holds no value. Neither does a
// layer that a decode of it alone into an any value rejects, as it
// rejects an alias with no anchor. [Layers.Decode], [Layers.DecodeInto],
// [Layers.Validate], and [Layers.Document] return the error of the
// lowest such layer, bound in its file. [Layers.SelfValidate] and
// [Layers.Bind] go on without that layer.
//
// A program whose optional files are all missing builds Layers that
// hold no Node. A decode then leaves the value as it was, and each error
// binds with no position, as in "$.servers[1].port: port is required".
// The program thus makes the same calls whichever of its files exist.
// Each such error is bound to an empty document, so no other document
// places it. A value that came from no document validates through
// [SelfValidateValue] instead.
//
// Layers merge their Nodes once, on the first call that needs the merged
// document, and never change after that, so they are safe for
// concurrent use.
//
// Create instances with [NewLayers].
type Layers struct {
	// What the Nodes merge into, which document fills on its first call.
	merged mergedLayers
	// The Nodes in the order they apply, lowest first. None is nil.
	nodes []*Node
	// Fills merged once.
	once sync.Once
}

// NewLayers creates a new [*Layers] from the given Nodes, in the order
// they apply: the lowest layer first and the highest last. A nil Node
// adds nothing, so a program with an optional file passes its Node as it
// is.
func NewLayers(nodes ...*Node) *Layers {
	l := &Layers{nodes: make([]*Node, 0, len(nodes))}

	for _, n := range nodes {
		if n != nil {
			l.nodes = append(l.nodes, n)
		}
	}

	return l
}

// Document returns the root [*Node] of the merged document, the Node a
// [Validator] gets from [Layers.Validate]. A caller reads one value of
// the layers through it without a decode of the rest, or prints what
// they hold together:
//
//	doc, err := layers.Document()
//	if err != nil {
//		return err
//	}
//
//	kind, err := doc.DecodeAt[string](ctx, paths.Doc().Child("kind"))
//
// A layer that holds no value, as [Layers] describes, returns its error
// and no Node. Layers that hold no Node return the root of an empty
// document with no name. Every call returns the same Node.
//
// The merged document belongs to a [Source] of its own. The Source holds
// the merged value as block-style YAML, below the preamble of the lowest
// layer, so a schema directive in the comments of base.yaml names the
// schema of the merged document. It keeps no other comment of a layer.
// Every scalar keeps the text its layer spells, such as 0x10 or 1.50,
// and a string keeps its quotes, except that a block scalar or a string
// of several lines reads as one double-quoted line. The Source takes its
// [Source.Name], its [Source.FilePath], and its [Source.FS] from the
// Source of the lowest layer, with what [WithAllowDuplicateKeys],
// [WithAliasLimit], and [WithYAMLParserOptions] set there.
//
// That text is no file of the program, though it has the name of one.
// [Node.View], [Node.Span], [Node.Tokens], [Node.Ranges], and
// [Node.PathAt] read its lines and its positions. So does a position or
// a range in an error bound through the Node. A position taken from the
// file of a layer thus names whatever lies there in the merged text.
//
// An error bound through the Node binds in the file of a layer, as
// [Layers] describes, and never in the merged text. Its position is thus
// not the one [Node.Ranges] returns for its path. [Annotate] marks the
// error on a view of the layer and marks nothing on [Node.View], and
// [SourceError.Excerpt] shows the lines of the layer. [SourceError.Node]
// and [SourceError.Document] return the Node of the layer, and
// [SourceError.Path] is the path of the value in its file. For a layer
// from [Node.At], that path differs from the path in the merged
// document.
func (l *Layers) Document() (*Node, error) {
	doc, err := l.document(context.Background())
	if err != nil {
		return nil, err
	}

	return doc, nil
}

// document returns the root Node of the merged document, as
// [mergeLayers] builds it, and the error of the lowest layer that holds
// no value a decode can read. It builds the document on the first call,
// and every later call shares it, so the end of ctx does not stop the
// merge. A nil l has no layers.
func (l *Layers) document(ctx context.Context) (*Node, error) {
	if l == nil {
		return noLayers(), nil
	}

	l.once.Do(func() {
		l.merged = mergeLayers(context.WithoutCancel(ctx), l.nodes)
	})

	return l.merged.doc, l.merged.err
}

// layerError returns the error of the layer that holds no value, for a
// decode with cfg. Where the decoder rejected that layer and the go-yaml
// options of cfg let it decode, the layer reads something those options
// alone define, such as an anchor of a reference document. The options
// reach the decode of the merged document and no layer, so the error
// then names the option that reaches a layer. A layer that decodes and
// does not merge, such as one with a key that has no name, fails the
// same way with the options, so its error comes back as it is.
func (l *Layers) layerError(ctx context.Context, cfg decodeConfig) error {
	failed, err := l.merged.failed, l.merged.err

	if failed == nil || failed.doc.err != nil || len(cfg.yamlOpts) == 0 || !errors.Is(err, ErrDecode) {
		return err
	}

	var discard any

	if failed.decodeInto(ctx, &discard, decodeConfig{yamlOpts: cfg.yamlOpts, skipSelfValidation: true}) != nil {
		return err
	}

	return fmt.Errorf("%w: %w", errOptionReference, err)
}

// DecodeInto validates and decodes the merged document into v, as
// [Node.DecodeInto] decodes a document with the same options. It then
// runs the self-validation step on v, as [Layers.SelfValidate] runs it.
// A layer that holds no value, as [Layers] describes, returns its error
// before anything runs. Any v that is not a non-nil pointer returns an
// error wrapping [ErrDecodeTarget] before that.
//
// Every step runs once, on what the layers hold together. A [Validator]
// from [WithValidator] gets the Node of the merged document, as
// [Layers.Validate] hands it one, so a schema that requires a key passes
// when any layer sets it. [WithSelfValidation] turns the self-validation
// step off, for a program that changes the value before it validates.
func (l *Layers) DecodeInto(ctx context.Context, v any, opts ...DecodeOption) error {
	doc, layerErr := l.document(ctx)

	err := checkDecodeTarget(v)
	if err != nil {
		return doc.bindOwn(err)
	}

	cfg := newDecodeConfig(opts)

	if layerErr != nil {
		return l.layerError(ctx, cfg)
	}

	return doc.decodeInto(ctx, v, cfg)
}

// Validate runs v on the merged document, as [Node.Validate] runs it on
// a document. It is the validation step of [Layers.DecodeInto] on its
// own, for a caller that checks the layers without decoding them. A layer
// that holds no value, as [Layers] describes, returns its error, and v
// does not run.
//
// The validator gets the root Node of the merged document, so its paths
// read from the value the layers hold. An error it returns unbound binds
// in the layer that holds the value the error is about, and so does an
// error it binds through that Node.
func (l *Layers) Validate(ctx context.Context, v Validator) error {
	doc, err := l.document(ctx)
	if err != nil {
		return err
	}

	return doc.Validate(ctx, v)
}

// SelfValidate runs the self-validation step of [Layers.DecodeInto] on
// its own, on v through the merged document, as [Node.SelfValidate] runs
// it. Each error binds in the layer that holds its value, as [Layers]
// describes. It runs whatever [WithSelfValidation] says, and reads only
// the go-yaml options among opts. A layer that holds no value adds
// nothing, and SelfValidate returns no error for it.
func (l *Layers) SelfValidate(ctx context.Context, v any, opts ...DecodeOption) error {
	doc, _ := l.document(ctx) //nolint:errcheck // A layer that holds no value adds nothing.

	return doc.selfValidate(ctx, v, newDecodeConfig(opts))
}

// Bind binds err as [Node.Bind] binds it through the root of a document,
// with one difference. Each path binds in the layer that holds the value
// it names, as [Layers] describes, so a check the program runs on the
// value reports the file a self-validation would report:
//
//	return layers.Bind(checkQuota(&cfg))
//
// A path in err reads from the value the layers hold, whether it starts
// at `$` or at `@`, and the bound error reports it as the document of
// its layer reads it.
func (l *Layers) Bind(err error) error {
	// A check that passed has nothing to bind, so its call merges no
	// layer.
	if isNothing(err) {
		return nil
	}

	doc, _ := l.document(context.Background()) //nolint:errcheck // A layer that holds no value adds nothing.

	return doc.Bind(err)
}

// Decode validates and decodes the merged document into a new T, as
// [Layers.DecodeInto] decodes it into a value the caller holds. On
// error, the returned T is the zero value.
func (l *Layers) Decode[T any](ctx context.Context, opts ...DecodeOption) (T, error) {
	var v T

	err := l.DecodeInto(ctx, &v, opts...)
	if err != nil {
		var zero T

		return zero, err
	}

	return v, nil
}
