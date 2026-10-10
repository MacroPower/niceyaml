package niceyaml

import (
	"context"
	"errors"
)

// ErrUnnamedKey indicates a mapping key that [NewSourceFromLayers] cannot
// merge by. A key that is a sequence or a mapping has no name a path
// selects it by, as
// [go.jacobcolvin.com/niceyaml/paths.Resolver.KeyName] reports, so two
// layers cannot hold it as one key. The error binds at the key, in the
// layer that holds it.
var ErrUnnamedKey = errors.New("mapping key has no name")

// NewSourceFromLayers creates a new [*Source] that holds the one document
// the given layers merge into, in the order they apply: the lowest layer
// first and the highest last, such as a base file with the file of one
// environment over it. A mapping in a higher layer merges into the
// mapping below it key by key, a sequence or a scalar replaces what lies
// below it, and a null keeps it. [Source.Decode] and [Source.DecodeInto]
// then validate, decode, and self-validate the merged document once, and
// each error binds in the file that holds the value the error is about.
// Each layer is a [Layer], and a program passes the Source of each file
// as it read it:
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
//	cfg, err := niceyaml.NewSourceFromLayers(base, prod).Decode[Config](ctx)
//
// The layers merge in the call, which decodes each of them as a decode
// of that layer alone reads it, and no deadline stops that decode. The
// Source never changes after that, so it is safe for concurrent use. A
// nil Layer adds nothing, and neither does a nil Source or a nil
// [*Node], so a program with an optional file passes its Source as it
// is.
//
// # Decoding
//
// [Source.Decode] and [Source.DecodeInto] validate, decode, and
// self-validate the merged document once. Each error binds in the file
// that holds the value the error is about, so a port that only base.yaml
// sets reports the line that sets it:
//
//	base.yaml:3:9: $.server.port: port must be at least 1
//
// [Source.ValidateDocuments] runs a [Validator] on the merged document
// without a decode, and [Source.Bind] binds the error of a check the
// program runs itself, in the layer that holds the value the error
// names:
//
//	return merged.Bind(checkQuota(&cfg))
//
// # Environment and Flags
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
//	env := niceyaml.NewSourceFromBytes(data,
//		niceyaml.WithName("environment"), niceyaml.WithExcerpts(false))
//
//	cfg, err := niceyaml.NewSourceFromLayers(base, prod, env).Decode[Config](ctx)
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
// prints the configuration it runs with prints the view of the merged
// Source, which holds what the files and the environment set together.
//
// A program whose library writes the environment or the flags into the
// Go value holds no such map. It turns the self-validation step off for
// the decode, lets the library fill the value, and runs the step with
// [Source.SelfValidate] once the value is whole:
//
//	var cfg Config
//	if err := merged.DecodeInto(ctx, &cfg, niceyaml.WithSelfValidation(false)); err != nil {
//		return err
//	}
//
//	applyEnv(&cfg)
//
//	return merged.SelfValidate(ctx, &cfg)
//
// No [Validator] sees a value set this way. A schema thus leaves the
// value unchecked, and reports a key it requires as missing when only
// the library sets it. An error under such a value binds at whatever a
// file holds at its path, as [Node.SelfValidate] describes.
//
// # Merge Rules
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
// # Keys
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
// # Aliases
//
// Each layer resolves its own aliases and `<<` merge keys before it
// merges, as a decode of that layer alone reads them, with the reference
// documents of [WithReferences] and the other settings of its own
// Source. An alias thus reads the anchor of its own file, whatever
// anchors of that name the other layers define, and a value that an
// alias or a merge key brings in merges as one written in its place
// does. The merged document holds a copy of that value and no alias.
// The merge writes each alias out in full, so a layer must pass the
// alias limit of [WithAliasLimit] as a decode into a type that reads
// text does.
//
// # Merged Document
//
// The Source holds the merged value as block-style YAML, below the
// preamble of the lowest layer, so a schema directive in the comments of
// base.yaml names the schema of the merged document. It keeps no other
// comment of a layer. Every scalar keeps the text its layer spells, such
// as 0x10 or 1.50, and a string keeps its quotes, except that a block
// scalar or a string of several lines reads as one double-quoted line.
// The Source takes its [Source.Name], its [Source.FilePath], and its
// [Source.FS] from the Source of the lowest layer, with what
// [WithAllowDuplicateKeys] and [WithAliasLimit] set there. Its text
// holds values of every layer, so [Source.Excerpts] reports false for it
// when the Source of any layer has excerpts off, as [WithExcerpts]
// describes. A call with no layer gives an empty Source with no name.
//
// [Source.Document] returns the root Node of the merged document, the
// Node a [Validator] gets. A caller reads one value of the layers
// through it without a decode of the rest, or prints what they hold
// together:
//
//	doc, err := merged.Document()
//	if err != nil {
//		return err
//	}
//
//	kind, err := doc.DecodeAt[string](ctx, paths.Doc().Child("kind"))
//
// That text is no file of the program, though it has the name of one.
// [Node.View], [Node.Span], [Node.Tokens], [Node.Ranges], and
// [Node.PathAt] read its lines and its positions. So does a position or
// a range in an error bound through the Node. A position taken from the
// file of a layer thus names whatever lies there in the merged text.
//
// Every Node of the document has the [Node.FilePath] and the [Node.FS]
// of the lowest layer, whichever layer holds its value. A validator that
// resolves a file beside a value thus reads [Node.Origin] of the Node of
// that value, and not its FilePath. Origin returns the Node that holds
// the value in the file of its layer, with the lines and the positions
// of that file.
//
// # Error Binding
//
// An error never binds in the merged text. An error with a path binds in
// the highest layer whose document holds the value at that path, and the
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
// Its position is thus not the one [Node.Ranges] returns for its path in
// the merged document. [Annotate] marks the error on a view of the layer
// and marks nothing on the view of the merged Source, and
// [SourceError.Excerpt] shows the lines of the layer. [SourceError.Node]
// and [SourceError.Document] return the Node of the layer.
//
// # Nested Layers
//
// The merged Source is a layer like any other, and so is the Node its
// Document method returns. A merge that holds one reads its value as it
// reads a file of the same text. Each error still binds in the file that
// holds its value, with the path that file has for it.
//
// # Defaults
//
// The decode still fills the Go value by the rule of [Node.DecodeInto],
// once, from the merged document. A value that holds defaults keeps each
// field the merged document leaves out, and a mapping of the document
// replaces a map the value holds, whole. Defaults that should merge key
// by key go in a layer, such as an embedded file below the others.
//
// # Override Limits
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
// # Secrets
//
// The excerpt of an error shows lines of its layer, so a secret the
// environment set prints when it sits on the line of the error or among
// the context lines around it. A program builds a layer that holds
// secrets from a Source with [WithExcerpts] set to false:
//
//	env := niceyaml.NewSourceFromBytes(data,
//		niceyaml.WithName("environment"), niceyaml.WithExcerpts(false))
//
// An error in that layer then prints its position, its path, and its
// message, and no line of the layer. An error in another layer keeps its
// excerpt. The merged text holds the secret whatever the option says, so
// the merged Source has excerpts off when the Source of any layer has.
//
// # Layer Errors
//
// A layer whose document did not parse holds no value. Neither does a
// Source that holds several documents, which has no one document to
// merge, nor a layer that a decode of it alone into an any value rejects,
// as it rejects an alias with no anchor. The merged Source then holds
// the error of the lowest such layer, bound in its file, as a Source
// holds the syntax error of a file that did not parse. [Source.File],
// [Source.Document], [Source.Documents], [Source.Decode],
// [Source.DecodeInto], and [Source.SelfValidate] return that error, and
// so do [Node.Err] and the Node methods that read the tree, such as
// [Node.Validate]. A path in an error that [Source.Bind] or [Node.Bind]
// binds resolves nowhere then, as in a file that did not parse. The
// bound error names the lowest layer and no position, and
// [SourceError.Unresolved] returns [ErrPathNeedsDocument] wrapping the
// error of the layer.
//
// # Optional Files
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
//	cfg, err := niceyaml.NewSourceFromLayers(base, user).Decode[Config](ctx)
//
// A program whose optional files are all missing merges no layer. A
// decode then leaves the value as it was, and each error binds with no
// position, as in "$.servers[1].port: port is required". The program
// thus makes the same calls whichever of its files exist. Each such
// error is bound to an empty document, so no other document places it.
// A value that came from no document validates through
// [SelfValidateValue] instead.
func NewSourceFromLayers(layers ...Layer) *Source {
	return mergeLayers(context.Background(), layers)
}

// Layer is one layer of the Source [NewSourceFromLayers] builds: a
// document that merges with the layers below it. A [*Source] and a
// [*Node] are each a Layer, and no type outside this package is one.
//
// A Source stands for its one document, the one [Source.Document]
// returns, so a program passes each file as it read it. A Source that
// holds several documents, or a document that did not parse, is a layer
// that holds no value, as [NewSourceFromLayers] describes.
//
// A Node is the root of a document, or the value at a path of one for a
// Node from [Node.At]. It serves a program that layers one document of a
// file that holds several, or a part of a document.
//
// A nil Layer adds nothing, and neither does a nil Source or a nil Node.
//
// Go spreads neither a []*Source nor a []*Node into the parameter of
// [NewSourceFromLayers]. A program that collects its layers in a loop
// thus holds them in a []Layer:
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
//	cfg, err := niceyaml.NewSourceFromLayers(layers...).Decode[Config](ctx)
//
// See [*Source] and [*Node] for the implementations.
type Layer interface {
	// Resolves the layer as [NewSourceFromLayers] merges it.
	resolveLayer() resolvedLayer
}

// Origin returns the [*Node] that holds the value of n in the file of a
// layer. A Node of the document [NewSourceFromLayers] builds reads the
// merged text, and its [Node.FilePath] and its [Node.FS] are those of
// the lowest layer, whichever layer holds its value. Its origin is the
// Node at the path of n in the layer an error at that path binds in, as
// NewSourceFromLayers describes. A [Validator] that resolves a file
// beside a value thus reads the directory from the origin of that value:
//
//	license, err := n.At(licensePath)
//	if err != nil {
//		return err
//	}
//
//	origin, err := license.Origin()
//	if err != nil {
//		return err
//	}
//
//	dir := filepath.Dir(origin.FilePath())
//
// A Node of any other document is its own origin, so Origin returns the
// receiver for it. A validator thus makes the same calls whether it runs
// on one file or on a merged Source.
//
// The layer is the highest one whose document holds a value at the path,
// and a null there holds none. The origin has the lines, the positions,
// and the [Node.Path] of the document of that layer. For a layer from
// [Node.At], that path differs from the path of n, as
// `$.prod.server.port` differs from `$.server.port`.
//
// A mapping that several layers hold has its origin in the highest of
// them. That Node holds the keys its own layer sets, and none of the
// keys the layers below add to the merged mapping. A caller thus decodes
// the mapping through n, and asks for the origin of one key through the
// Node of that key. A sequence comes whole from one layer, so its origin
// holds every element.
//
// The root of the merged document follows the same rule. Its origin is
// the highest layer that holds a value, or the highest layer of all when
// none holds one. Origin returns the Node of that layer as
// [NewSourceFromLayers] got it, or the Node [Source.Document] returns
// for a [Source] it got. An error with no location binds in the lowest
// layer instead, whose name and file path the merged document has.
// Node.FilePath of the root and of its origin thus name two files where
// a higher layer holds a value.
//
// A layer can be a merged Source, or a Node of its document. A value of
// such a layer has the origin it has in that document, so every origin
// belongs to a document that no merge built.
//
// A value that a layer reads through an alias or a `<<` merge key has
// its origin where Node.At resolves the path in that layer, which is the
// content of the anchor. A path does not enter a reference document of
// [WithReferences], so a value whose anchor lies in one has no Node in
// its layer. Origin then returns the error Node.At returns for the path,
// which wraps [go.jacobcolvin.com/niceyaml/paths.ErrAlias]. The error is
// bound to the layer that holds the alias, so [SourceError.Source] names
// the file of that layer.
//
// A layer that holds no value, as NewSourceFromLayers describes, has no
// Node, so the origin of a value that lies in it is the error of that
// layer.
func (n *Node) Origin() (*Node, error) {
	if !n.merges() {
		return n, nil
	}

	layer, _, path := n.source.layers.layer(n, n.base)

	if layer.node == nil {
		return nil, layer.err
	}

	// The path names the Node of the layer itself, which needs no scope.
	// The root of a document covers every line of it, where a Node that
	// At scopes at `$` covers its content alone, and a document with no
	// content has no node for At to select.
	if path.Equal(layer.node.base) {
		return layer.node, nil
	}

	return layer.node.At(path)
}
