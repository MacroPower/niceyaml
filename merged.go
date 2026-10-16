package niceyaml

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"go.jacobcolvin.com/niceyaml/internal/aliasing"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/layermerge"
	"go.jacobcolvin.com/niceyaml/paths"
)

var (
	// ErrUnnamedKey indicates a mapping key that [NewSourceFromLayers] cannot
	// merge by. A key that is a sequence or a mapping has no name a path
	// selects it by, as
	// [go.jacobcolvin.com/niceyaml/paths.Resolver.KeyName] reports, so two
	// layers cannot hold it as one key. The error binds at the key, in the
	// layer that holds it.
	ErrUnnamedKey = errors.New("mapping key has no name")

	// ErrUnknownLayer indicates a layer that [MergeAs] names and the layers
	// of [NewSourceFromLayers] do not hold. It is a mistake of the caller,
	// so the error binds in no layer, and [IsInvalid] does not report it.
	ErrUnknownLayer = errors.New("layer is not among the merged layers")
)

// NewSourceFromLayers creates a new [*Source] that holds the one document
// the given layers merge into. The layers go lowest first, such as a base
// file with the file of one environment over it. A mapping merges into
// the mapping below it key by key, a sequence or a scalar replaces what
// lies below it whole, and a null written as `null` or `~` removes its
// key. No layer thus sets one element of a sequence. A decode of the
// Source validates the merged document once, and each error binds in the
// file that holds the value the error is about. The Layers section of
// the package documentation gives the merge rules in full and says where
// each error binds:
//
//	merged, err := niceyaml.NewSourceFromLayers(ctx, []*niceyaml.Source{base, prod})
//	if err != nil {
//		return err
//	}
//
//	cfg, err := merged.Decode[Config](ctx, niceyaml.WithValidator(schema))
//
// The layers come as a []*Source, as a []*Node, such as the documents
// [Source.Documents] returns, or as a []Layer that holds both. They merge
// in the call, which decodes each of them with ctx as a decode of that
// layer alone reads it. A nil Layer adds nothing, and a call with no layer
// left returns an empty Source with no name.
//
// The Source stands for the lowest layer, or for the layer [MergeAs]
// names. It takes the name, the file path, the file system, and the
// preamble of that layer, and an error with no location binds in it.
// Every decode of the Source applies the unmarshalers of
// [MergeUnmarshalers], or else the unmarshalers that [WithUnmarshalers]
// gives the Source of that layer.
//
// The merge fails on the lowest layer that holds no value it can read. A
// Source of several documents holds none, and neither does a document
// that did not parse, that a decode of it alone into an any value
// rejects, or that goes past the alias limit of [WithAliasLimit].
//
// The merge writes each alias out in full. A layer fails with an error
// that matches [ErrLimit] when its aliases repeat more text than the byte
// limit of its Source allows, or nest the layer past its depth limit. A
// key that is a sequence or a mapping fails its layer with
// [ErrUnnamedKey]. The merge returns the error of that layer, bound in
// its file, and no Source. It returns the error of ctx when ctx ends. A
// layer of MergeAs that the layers do not hold fails the call with
// [ErrUnknownLayer] before any layer merges.
func NewSourceFromLayers[L Layer](ctx context.Context, layers []L, opts ...MergeOption) (*Source, error) {
	var cfg mergeConfig

	for _, opt := range opts {
		opt(&cfg)
	}

	return mergeLayers(ctx, layers, cfg)
}

// MergeOption configures [NewSourceFromLayers].
//
// Available options:
//   - [MergeAs]
//   - [MergeUnmarshalers]
type MergeOption func(*mergeConfig)

// mergeConfig holds the settings a [MergeOption] configures.
type mergeConfig struct {
	// The layer the merged Source stands for, or nil for the lowest.
	as Layer
	// Holds the unmarshalers of MergeUnmarshalers, in the order given.
	unmarshalers []Unmarshaler
	// Says the caller passed a MergeUnmarshalers option, even one with no
	// unmarshalers, so unmarshalers replaces those of the layer the merged
	// Source stands for.
	ownUnmarshalers bool
}

// MergeAs is a [MergeOption] that makes the Source [NewSourceFromLayers]
// builds stand for layer, such as the file of the user above defaults
// that the program embeds. The Source takes the name, the file path, the
// file system, and the preamble of that layer in place of those of the
// lowest layer. It also takes the unmarshalers that [WithUnmarshalers]
// gives the Source of that layer, unless [MergeUnmarshalers] gives the
// merge its own. An error with no location binds in that layer. The
// layers merge in the order given whichever layer the Source stands for,
// so a higher layer still sets the values it holds.
//
// The preamble holds the schema directive of the layer, so the directive
// of that layer picks the schema of the merged document, and a relative
// reference in it names a file beside that layer. A server that passes
// MergeAs with an upload thus lets the upload pick its schema, as a
// validation of the upload alone does. A server that picks the schema
// itself leaves [go.jacobcolvin.com/niceyaml/schema.Directive] out of its
// registry.
//
// A nil layer keeps the lowest layer, and so does a nil Source or a nil
// Node, so an optional file goes to MergeAs as it is. The Source stands
// for the lowest layer when that file is missing, so unmarshalers given
// to that file alone apply only when it exists. A program gives its
// unmarshalers to MergeUnmarshalers instead. Every decode then reads the
// types of the program the same way whichever files exist.
//
// For any other layer that the layers do not hold, NewSourceFromLayers
// returns an error that wraps [ErrUnknownLayer]. It compares pointers, so
// layer must be the [*Source] or the [*Node] the layers hold, and a Node
// that another call of [Node.At] returns does not match. The last of
// several MergeAs options applies.
func MergeAs(layer Layer) MergeOption {
	return func(c *mergeConfig) {
		c.as = layer
	}
}

// MergeUnmarshalers is a [MergeOption] that gives every decode of the
// Source [NewSourceFromLayers] builds the unmarshalers us, in place of
// the unmarshalers of the layer the Source stands for. A program that
// merges an optional file of the user over the defaults it embeds gives
// its unmarshalers here, such as the cidr [Unmarshaler] that
// [CustomUnmarshaler] shows. Every decode then applies them whichever
// files exist:
//
//	merged, err := niceyaml.NewSourceFromLayers(ctx, []*niceyaml.Source{defaults, user},
//		niceyaml.MergeAs(user),
//		niceyaml.MergeUnmarshalers(cidr),
//	)
//
// Like the unmarshalers of [WithUnmarshalers], the unmarshalers us reach
// the decode inside a [Validator] and every decode of the empty Source
// that a merge of no layer returns. A Node that [Node.Origin] returns
// belongs to the document of a layer, so a decode of it applies the
// unmarshalers of that layer instead.
//
// A merge with MergeUnmarshalers takes no unmarshaler from any layer,
// even when us is empty. Each MergeUnmarshalers adds to the ones before
// it, so a later function for a type replaces an earlier one and the
// last [JSONUnmarshalers] applies. MergeUnmarshalers copies the slice us,
// so the option keeps its unmarshalers when the caller changes the slice
// it passed.
func MergeUnmarshalers(us ...Unmarshaler) MergeOption {
	us = slices.Clone(us)

	return func(c *mergeConfig) {
		c.unmarshalers = slices.Concat(c.unmarshalers, us)
		c.ownUnmarshalers = true
	}
}

// Layer is one layer of the Source [NewSourceFromLayers] builds, a
// document that merges with the layers below it. A [*Source] and a
// [*Node] are each a Layer, and no type outside this package is one. A
// Source stands for the one document [Source.Document] returns, and a
// Node for the root of a document or, from [Node.At], the value at a
// path of one. A nil Layer adds nothing, and neither does a nil Source or
// a nil Node.
//
// See [*Source] and [*Node] for the implementations.
type Layer interface {
	// Resolves the layer as [NewSourceFromLayers] merges it.
	resolveLayer() resolvedLayer
}

// Origin returns the [*Node] that holds the value of n in the file of a
// layer of the Source [NewSourceFromLayers] builds, and returns n itself
// for a Node of any other document. A Node of a merged Source reads the
// merged text and has the [Node.FilePath] and [Node.FS] of the layer
// [MergeAs] names, or else of the lowest layer, so a [Validator] that
// resolves a file beside a value reads the directory from the origin of
// that value:
//
//	origin, err := license.Origin()
//	if err != nil {
//		return err
//	}
//
//	dir := filepath.Dir(origin.FilePath())
//
// The origin lies in the highest layer whose document holds a value at
// the path of n, where an error at that path binds, as the Layers section
// of the package documentation describes. It has the lines, the
// positions, and the [Node.Path] of the document of that layer. A
// mapping that several layers hold has its origin in the highest of
// them, and that Node holds the keys its own layer sets and none of the
// keys the layers below add. The origin of the root is the highest layer
// that holds a value, or the highest of all when none does.
//
// The origin decodes with the unmarshalers of the Source of its layer,
// and not with those of [MergeUnmarshalers]. So a program that decodes
// origins gives the same [WithUnmarshalers] to the Source of every layer.
//
// A value that a layer reads through an alias or a `<<` merge key has its
// origin at the content of the anchor. For an anchor in a reference
// document of [WithReferences], Origin returns the error [Node.At]
// returns for the path, which wraps
// [go.jacobcolvin.com/niceyaml/paths.ErrAlias] and is bound to the layer
// that holds the alias.
func (n *Node) Origin() (*Node, error) {
	if !n.merges() {
		return n, nil
	}

	layer, _, path := n.source.layers.layer(n, n.base)

	// The path names the Node of the layer itself. At would scope a root to
	// its content alone, and fail on a document with no content.
	if path.Equal(layer.node.base) {
		return layer.node, nil
	}

	return layer.node.At(path)
}

// layering holds the layers of a [Source] from [NewSourceFromLayers]. An
// error in it binds in a layer instead, because the merged text is no file of
// the caller, and passes through a merged layer to one no merge built.
type layering struct {
	// The merged value, or nil when no layer holds a value.
	root *layermerge.Value
	// The layers the merge read, lowest first, which each value names by
	// its index.
	layers []resolvedLayer
	// The index of the layer the merged Source stands for, which [MergeAs]
	// names, or 0 for the lowest.
	identity int
}

// home returns the layer the merged Source stands for, where an error with
// no layer of its own binds, or the home of that layer when a merge built
// it. A nil l has none.
func (l *layering) home() resolvedLayer {
	if l == nil {
		return resolvedLayer{}
	}

	layer := l.layers[l.identity]

	if home := layer.source.layers.home(); home.source != nil {
		return home
	}

	return layer
}

// layer returns the layer an error at path, a `$` path in doc, binds in,
// the path in that layer where the root of doc sits, and path as the layer
// reads it. It picks the layer of the deepest value on path, or else the
// highest, and goes on into a layer that a merge built.
func (l *layering) layer(doc *Node, path paths.Path) (resolvedLayer, paths.Path, paths.Path) {
	if l == nil {
		return doc.resolveLayer(), paths.Doc(), path
	}

	// The merged value stands at the root of its document, so the rest of
	// the path reads from the value of each layer.
	rel, _ := path.CutPrefix(paths.Doc())

	layer := l.layers[len(l.layers)-1]

	if at, _ := l.root.Find(rel); at != nil {
		layer = l.layers[at.Layer]
	}

	// The layer holds the merged value at its scope, and the search finds
	// the root of the document of the layer at base.
	scope, _ := layer.node.base.CutPrefix(paths.Doc())

	holder, base, held := layer.source.layers.layer(layer.node, layer.node.base.Join(rel))

	return holder, base.Join(scope), held
}

// value returns the value the layers hold at path, which starts at `$`,
// or nil when they hold none there. A nil l holds no value.
func (l *layering) value(path paths.Path) *layermerge.Value {
	if l == nil {
		return nil
	}

	rel, _ := path.CutPrefix(paths.Doc())

	at, ok := l.root.Find(rel)
	if !ok {
		return nil
	}

	return at
}

// resolvedLayer is one layer of a merged [Source], as [Layer] resolves it.
// The zero value adds nothing. A layer with no Node holds an error that
// [mergeLayers] returns, so every layer of a [layering] has a Node.
type resolvedLayer struct {
	// The Source of the layer, where its errors bind.
	source *Source
	// The Node the layer merges, or nil for a Source that holds no one
	// document.
	node *Node
	// The error [Source.Document] returns for a Source that holds no one
	// document, or nil.
	err error
}

// resolveLayer returns n as the layer it is. A nil n adds nothing.
func (n *Node) resolveLayer() resolvedLayer {
	if n == nil {
		return resolvedLayer{}
	}

	return resolvedLayer{source: n.source, node: n}
}

// resolveLayer returns the document [Source.Document] returns as a layer,
// or a layer with its error and no Node. A nil s adds nothing.
func (s *Source) resolveLayer() resolvedLayer {
	if s == nil {
		return resolvedLayer{}
	}

	doc, err := s.Document()
	if err != nil {
		return resolvedLayer{source: s, err: err}
	}

	return resolvedLayer{source: s, node: doc}
}

// resolveLayers returns the layers of given that add a layer, in the
// order of given, and the index among them of the layer the merged Source
// stands for. That layer is as, or the lowest at index 0 when as adds
// nothing. It returns [ErrUnknownLayer] when given does not hold as.
func resolveLayers[L Layer](given []L, as Layer) ([]resolvedLayer, int, error) {
	var named resolvedLayer

	if as != nil {
		named = as.resolveLayer()
	}

	identity := 0
	if named.source != nil {
		identity = -1
	}

	layers := make([]resolvedLayer, 0, len(given))

	for _, g := range given {
		layer := Layer(g)
		if layer == nil {
			continue
		}

		resolved := layer.resolveLayer()
		if resolved.source == nil {
			continue
		}

		if identity < 0 && layer == as {
			identity = len(layers)
		}

		layers = append(layers, resolved)
	}

	if identity < 0 {
		return nil, 0, fmt.Errorf("merge as %q: %w", named.source.Name(), ErrUnknownLayer)
	}

	return layers, identity, nil
}

// read returns the value of layer i, or nil when it holds none. It decodes
// the layer first, so a layer fails here as it fails alone. The reader
// writes each alias out in full, so read then checks the alias limit, and
// the bounds that the limits of the Source of the layer set on that text.
func (l resolvedLayer) read(ctx context.Context, i int) (*layermerge.Value, error) {
	n := l.node
	if n == nil {
		return nil, l.err
	}

	if n.doc.err != nil {
		return nil, n.doc.err
	}

	var discard any

	cfg := newDecodeConfig(n.source, nil)
	cfg.skipSelfValidation = true

	err := n.decodeInto(ctx, &discard, cfg, nil)
	if err != nil {
		return nil, err
	}

	err = aliasing.CheckDecodeText(n)
	if err != nil {
		return nil, n.aliasingError(err)
	}

	if !astnode.HasContent(n.AST()) {
		return nil, nil //nolint:nilnil // A layer with no content holds no value.
	}

	r := layermerge.Reader{
		Ctx:        ctx,
		Resolver:   n.doc.pathResolver(),
		Enclosed:   n.doc.state.Facts().Enclosed,
		References: n.source.referenceAnchors(),
		UnnamedKey: ErrUnnamedKey,
		Layer:      i,
	}

	v, err := r.Read(n.AST())
	if err != nil {
		if nodeErr, ok := errors.AsType[*layermerge.NodeError](err); ok {
			err = Invalid(nodeErr.Err, atToken(nodeErr.Token))
		}

		return nil, n.bindOwn(err)
	}

	v.Carry(n.source.layers.value(n.base), i)

	return v, nil
}

// mergeLayers implements [NewSourceFromLayers]. It writes the merged text
// below the preamble of the layer the merged Source stands for. It checks
// ctx again before it parses that text, which aliases can make longer
// than the layers.
func mergeLayers[L Layer](ctx context.Context, given []L, cfg mergeConfig) (*Source, error) {
	layers, identity, err := resolveLayers(given, cfg.as)
	if err != nil {
		return nil, err
	}

	if len(layers) == 0 {
		return NewSourceFromString("", WithUnmarshalers(cfg.unmarshalers...)), nil
	}

	var root *layermerge.Value

	for i, layer := range layers {
		v, err := layer.read(ctx, i)
		if err != nil {
			return nil, err
		}

		root = root.Merge(v)
	}

	named := layers[identity]
	text := layermerge.Text(preambleText(named.node), root)

	// An error with no location binds in the layer the merged Source
	// stands for, as the Layers section of the package documentation
	// describes.
	err = ctx.Err()
	if err != nil {
		return nil, named.node.bindOwn(err)
	}

	src := newMergedSource(named.source, text, showsLayers(layers), cfg)
	src.layers = &layering{root: root, layers: layers, identity: identity}

	return src, nil
}

// preambleText returns the text of the preamble of the document of n, as
// [Node.Preamble] describes it, ending in a line break.
func preambleText(n *Node) string {
	var sb strings.Builder

	for _, tk := range n.doc.tokens[:n.doc.preamble] {
		sb.WriteString(tk.Origin)
	}

	text := strings.TrimRight(sb.String(), " \t")
	if text == "" || strings.HasSuffix(text, "\n") {
		return text
	}

	return text + "\n"
}

// showsLayers reports whether an error can show the merged text of layers.
// That text holds values of every layer and of the reference documents they
// read, so it reports false when any of them has excerpts off.
func showsLayers(layers []resolvedLayer) bool {
	for _, layer := range layers {
		if layer.source.noExcerpts || layer.source.noReferenceExcerpts {
			return false
		}
	}

	return true
}

// newMergedSource returns a new [*Source] that holds text, with the name,
// file, and settings of from but no references, and excerpts as given. It
// takes the unmarshalers of merge when merge sets its own. It parses now,
// so it binds each syntax error before the caller sets layers.
func newMergedSource(from *Source, text string, excerpts bool, merge mergeConfig) *Source {
	src := NewSourceFromString(text, func(c *sourceConfig) {
		*c = from.sourceConfig

		if merge.ownUnmarshalers {
			c.unmarshalers = merge.unmarshalers
		}

		// The merged value holds no alias, so it reads no reference
		// document.
		c.references = nil
		c.referenceErr = nil
		c.noReferenceExcerpts = false

		c.noExcerpts = !excerpts

		// The merged text is text niceyaml wrote. It holds the content of
		// the layers and no more than the bounds of their limits let their
		// aliases add.
		c.noLimits = true
	})

	src.parsed.get(src.parse)

	return src
}
