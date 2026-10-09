package niceyaml

import (
	"context"
	"errors"
	"slices"
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

	// The Node that [Layers] with no layer validate and bind through: the
	// one document of an empty [Source], which has no name. Every call
	// shares the one Node, which never changes.
	noLayers = sync.OnceValue(func() *Node {
		return NewSourceFromString("").documents()[0]
	})
)

// Layers holds the layers that merge into one document, in the order
// they apply, such as a base file with the file of one environment over
// it. Each layer is a [Layer], and a program passes the [Source] of each
// file as it read it:
//
//	base, err := niceyaml.NewSourceFromFile("base.yaml")
//	if err != nil {
//		return err
//	}
//
//	prod, err := niceyaml.NewSourceFromFile("prod.yaml")
//	if err != nil {
//		return err
//	}
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
// A program that reads its environment or its flags passes what they set
// as one more layer, above the files. It builds a map that holds the
// keys they set and no other, under the names the YAML uses. Each value
// has the Go type of the field it sets, so the program parses the text
// of a variable first. The encoder quotes a string that reads as another
// type, so a schema reads "8080" as a string, and a bool field rejects
// "true". The program encodes the map with
// [go.jacobcolvin.com/niceyaml/encoder.Marshal] and passes a Source of
// the result:
//
//	overrides := map[string]any{
//		"server": map[string]any{"port": port}, // APP_SERVER_PORT
//	}
//
//	data, err := encoder.Marshal(ctx, overrides)
//	if err != nil {
//		return err
//	}
//
//	env := niceyaml.NewSourceFromBytes(data, niceyaml.WithName("environment"))
//
//	cfg, err := niceyaml.NewLayers(base, prod, env).Decode[Config](ctx)
//
// Every [Validator] and the self-validation step then check the port the
// environment set, and a schema that requires a key passes when only the
// environment sets it. An error under the port binds in that layer,
// under the name the program gave it:
//
//	environment:2:9: $.server.port: port must be at least 1
//
// [go.jacobcolvin.com/niceyaml/encoder.WithYAMLComments] adds a comment
// at a path of the encoded map, such as the name of the variable beside
// the port, and the excerpt of the error then shows it. A program that
// prints the configuration it runs with reads the text of
// [Layers.Document], which holds what the files and the environment set
// together.
//
// A program whose library writes the environment or the flags into the
// Go value holds no such map. It turns the self-validation step off for
// the decode, lets the library fill the value, and runs the step with
// [Layers.SelfValidate] once the value is whole:
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
// No [Validator] sees a value set this way. A schema thus leaves the
// value unchecked, and reports a key it requires as missing when only
// the library sets it. An error under such a value binds at whatever a
// file holds at its path, as [Node.SelfValidate] describes.
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
// A layer the program builds from its environment or its flags merges
// and binds as a file does, and those rules set its limits. A null keeps
// the value below it, so the layer cannot unset a value. A sequence
// replaces the one below it whole, so the layer cannot set one element.
// A key that no field of the target reads sets nothing, and
// [WithDisallowUnknownFields] reports such a key. An error at a mapping
// binds in the highest layer that holds the mapping, and so does an
// error for a key the mapping lacks. Both thus bind in such a layer once
// it sets one key there, whichever layer the fix belongs in.
//
// The excerpt of an error shows lines of its layer, so a secret the
// environment set prints when it sits on the line of the error or among
// the context lines around it. A program builds a layer that holds
// secrets from a [Source] with [WithExcerpts] set to false:
//
//	env := niceyaml.NewSourceFromBytes(data,
//		niceyaml.WithName("environment"), niceyaml.WithExcerpts(false))
//
// An error in that layer then prints its position, its path, and its
// message, and no line of the layer. An error in another layer keeps its
// excerpt. The text of [Layers.Document] holds the secret whatever the
// option says, so the Source of that document has excerpts off when the
// Source of any layer has.
//
// A layer whose document did not parse holds no value. Neither does a
// Source that holds several documents, which has no one document to
// merge, nor a layer that a decode of it alone into an any value rejects,
// as it rejects an alias with no anchor. [Layers.Decode],
// [Layers.DecodeInto], [Layers.Validate], [Layers.SelfValidate], and
// [Layers.Document] return the error of the lowest such layer, bound in
// its file. [Layers.Bind] goes on without that layer.
//
// A nil layer adds nothing. [NewSourceFromFile] returns a nil Source for
// a file it could not read, so a program with an optional file passes
// the Source as it is once it has checked the error:
//
//	user, err := niceyaml.NewSourceFromFile(userPath)
//	if err != nil && !errors.Is(err, fs.ErrNotExist) {
//		return err
//	}
//
//	cfg, err := niceyaml.NewLayers(base, user).Decode[Config](ctx)
//
// A program whose optional files are all missing builds Layers that
// hold no layer. A decode then leaves the value as it was, and each error
// binds with no position, as in "$.servers[1].port: port is required".
// The program thus makes the same calls whichever of its files exist.
// Each such error is bound to an empty document, so no other document
// places it. A value that came from no document validates through
// [SelfValidateValue] instead.
//
// The layers merge once, on the first call that needs the merged
// document, and Layers never change after that, so they are safe for
// concurrent use.
//
// Create instances with [NewLayers].
type Layers struct {
	// What the layers merge into, which document fills on its first call.
	merged mergedLayers
	// The layers in the order they apply, lowest first.
	layers []Layer
	// Fills merged once.
	once sync.Once
}

// Layer is one layer of [Layers]: a document that merges with the layers
// below it. A [*Source] and a [*Node] are each a Layer, and no type
// outside this package is one.
//
// A Source stands for its one document, the one [Source.Document]
// returns, so a program passes each file as it read it. A Source that
// holds several documents, or a document that did not parse, is a layer
// that holds no value, as [Layers] describes.
//
// A Node is the root of a document, or the value at a path of one for a
// Node from [Node.At]. It serves a program that layers one document of a
// file that holds several, a part of a document, or the document
// [Layers.Document] returns.
//
// A nil Layer adds nothing, and neither does a nil Source or a nil Node.
//
// Go spreads neither a []*Source nor a []*Node into the parameter of
// [NewLayers]. A program that collects its layers in a loop thus holds
// them in a []Layer:
//
//	layers := make([]niceyaml.Layer, 0, len(names))
//
//	for _, name := range names {
//		source, err := niceyaml.NewSourceFromFS(fsys, name)
//		if err != nil {
//			return err
//		}
//
//		layers = append(layers, source)
//	}
//
//	cfg, err := niceyaml.NewLayers(layers...).Decode[Config](ctx)
//
// See [*Source] and [*Node] for the implementations.
type Layer interface {
	// Resolves the layer as [Layers] merge it.
	resolveLayer() resolvedLayer
}

// NewLayers creates a new [*Layers] from the given layers, in the order
// they apply: the lowest layer first and the highest last. A nil [Layer]
// adds nothing, and neither does a nil [*Source] or a nil [*Node], so a
// program with an optional file passes its Source as it is.
func NewLayers(layers ...Layer) *Layers {
	return &Layers{layers: slices.Clone(layers)}
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
// and no Node. Layers that hold no layer return the root of an empty
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
// [WithAliasLimit], and [WithExcerptWidth] set there. Its text holds
// values of every layer, so
// [Source.Excerpts] reports false for it when the Source of any layer
// has excerpts off, as [WithExcerpts] describes.
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
		l.merged = mergeLayers(context.WithoutCancel(ctx), l.layers)
	})

	return l.merged.doc, l.merged.err
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

	if layerErr != nil {
		return layerErr
	}

	return doc.decodeInto(ctx, v, newDecodeConfig(opts), nil)
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
// describes. It runs whatever [WithSelfValidation] says, and reads the
// options Node.SelfValidate reads among opts.
//
// A layer that holds no value, as [Layers] describes, returns its error
// before the step runs, as it does in DecodeInto. A program that fills v
// by other means thus learns of a file that did not parse. A v that is
// nil or a nil pointer returns an error wrapping [ErrSelfValidateTarget]
// before that.
func (l *Layers) SelfValidate(ctx context.Context, v any, opts ...DecodeOption) error {
	doc, layerErr := l.document(ctx)

	err := checkSelfValidateTarget(v)
	if err != nil {
		return doc.bindOwn(err)
	}

	if layerErr != nil {
		return layerErr
	}

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
//
// Bind returns err bound and no other error, so it goes on without a
// layer that holds no value, as [Layers] describes. A path then binds in
// the layers that hold one, and nothing reports the file that did not
// parse. A program that ran no decode of the layers checks
// [Layers.Document] first, which returns the error of such a layer:
//
//	layers := niceyaml.NewLayers(base, prod)
//
//	if _, err := layers.Document(); err != nil {
//		return err
//	}
//
//	return layers.Bind(checkQuota(&cfg))
//
// An error that binds in a Source with no one document names the Source
// and no position, and [SourceError.Node] returns nil for it.
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
