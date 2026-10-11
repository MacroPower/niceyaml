package niceyaml

import (
	"context"
	"errors"
	"strings"

	"go.jacobcolvin.com/niceyaml/internal/aliasing"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/layermerge"
	"go.jacobcolvin.com/niceyaml/paths"
)

// ErrUnnamedKey indicates a mapping key that [NewSourceFromLayers] cannot
// merge by. A key that is a sequence or a mapping has no name a path
// selects it by, as
// [go.jacobcolvin.com/niceyaml/paths.Resolver.KeyName] reports, so two
// layers cannot hold it as one key. The error binds at the key, in the
// layer that holds it.
var ErrUnnamedKey = errors.New("mapping key has no name")

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
//	merged, err := niceyaml.NewSourceFromLayers(ctx, base, prod)
//	if err != nil {
//		return err
//	}
//
//	cfg, err := merged.Decode[Config](ctx, niceyaml.WithValidator(schema))
//
// The layers merge in the call, which decodes each of them with ctx as a
// decode of that layer alone reads it. A nil Layer adds nothing, and a
// call with no layer left returns an empty Source with no name.
//
// The merge fails on the lowest layer that holds no value it can read. A
// Source of several documents holds none, and neither does a document
// that did not parse, that a decode of it alone into an any value
// rejects, or that goes past the alias limit of [WithAliasLimit]. A key
// that is a sequence or a mapping fails its layer with [ErrUnnamedKey].
// The merge returns the error of that layer, bound in its file, and no
// Source. It returns the error of ctx once ctx ends, so a deadline stops
// a merge that writes many aliases out.
func NewSourceFromLayers(ctx context.Context, layers ...Layer) (*Source, error) {
	return mergeLayers(ctx, layers)
}

// Layer is one layer of the Source [NewSourceFromLayers] builds, a
// document that merges with the layers below it. A [*Source] and a
// [*Node] are each a Layer, and no type outside this package is one. A
// Source stands for the one document [Source.Document] returns, and a
// Node for the root of a document or, from [Node.At], the value at a
// path of one. A nil Layer adds nothing, and neither does a nil Source or
// a nil Node.
//
// Go spreads neither a []*Source nor a []*Node into the parameter of
// NewSourceFromLayers, so a program that collects its layers in a loop
// holds them in a []Layer:
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
//	merged, err := niceyaml.NewSourceFromLayers(ctx, layers...)
//
// See [*Source] and [*Node] for the implementations.
type Layer interface {
	// Resolves the layer as [NewSourceFromLayers] merges it.
	resolveLayer() resolvedLayer
}

// Origin returns the [*Node] that holds the value of n in the file of a
// layer of the Source [NewSourceFromLayers] builds, and returns n itself
// for a Node of any other document. A Node of a merged Source reads the
// merged text and has the [Node.FilePath] and [Node.FS] of the lowest
// layer, so a [Validator] that resolves a file beside a value reads the
// directory from the origin of that value:
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
// error in it binds in a layer instead, since the merged text is no file of
// the caller, and passes through a merged layer to one no merge built.
type layering struct {
	// The merged value, or nil when no layer holds a value.
	root *layermerge.Value
	// The layers the merge read, lowest first, which each value names by
	// its index.
	layers []resolvedLayer
}

// home returns the lowest layer, where an error with no layer of its own
// binds, or the home of that layer when a merge built it. A nil l has none.
func (l *layering) home() resolvedLayer {
	if l == nil {
		return resolvedLayer{}
	}

	lowest := l.layers[0]

	if home := lowest.source.layers.home(); home.source != nil {
		return home
	}

	return lowest
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
// order of given.
func resolveLayers(given []Layer) []resolvedLayer {
	layers := make([]resolvedLayer, 0, len(given))

	for _, layer := range given {
		if layer == nil {
			continue
		}

		if resolved := layer.resolveLayer(); resolved.source != nil {
			layers = append(layers, resolved)
		}
	}

	return layers
}

// read returns the value of layer i, or nil when it holds none. It decodes
// the layer first, so a layer fails here as it fails alone, and checks the
// alias limit, since the reader writes each alias out in full.
func (l resolvedLayer) read(ctx context.Context, i int) (*layermerge.Value, error) {
	n := l.node
	if n == nil {
		return nil, l.err
	}

	if n.doc.err != nil {
		return nil, n.doc.err
	}

	var discard any

	err := n.decodeInto(ctx, &discard, decodeConfig{skipSelfValidation: true}, nil)
	if err != nil {
		return nil, err
	}

	err = aliasing.CheckDecodeText(n)
	if err != nil {
		return nil, n.Bind(Invalid(err, atToken(astnode.StartToken(n.AST()))))
	}

	if !astnode.HasContent(n.AST()) {
		return nil, nil //nolint:nilnil // A layer with no content holds no value.
	}

	r := layermerge.Reader{
		Ctx:        ctx,
		Resolver:   n.doc.pathResolver(),
		Enclosed:   n.doc.state.Facts().Enclosed,
		References: n.source.references,
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

// mergeLayers implements [NewSourceFromLayers]. It checks ctx again before
// it parses the merged text, which aliases can make far longer than the
// layers, and writes that text below the preamble of the lowest layer.
func mergeLayers(ctx context.Context, given []Layer) (*Source, error) {
	layers := resolveLayers(given)
	if len(layers) == 0 {
		return NewSourceFromString(""), nil
	}

	var root *layermerge.Value

	for i, layer := range layers {
		v, err := layer.read(ctx, i)
		if err != nil {
			return nil, err
		}

		root = root.Merge(v)
	}

	lowest := layers[0]
	text := layermerge.Text(preambleText(lowest.node), root)

	// An error with no location binds in the lowest layer, as
	// NewSourceFromLayers describes.
	err := ctx.Err()
	if err != nil {
		return nil, lowest.node.bindOwn(err)
	}

	src := newMergedSource(lowest.source, text, showsLayers(layers))
	src.layers = &layering{root: root, layers: layers}

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

// showsLayers reports whether an error may show the merged text of layers.
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
// parses now, so it binds each syntax error before the caller sets layers.
func newMergedSource(from *Source, text string, excerpts bool) *Source {
	src := NewSourceFromString(text, func(c *sourceConfig) {
		*c = from.sourceConfig

		// The merged value holds no alias, so it reads no reference
		// document.
		c.references = nil
		c.noReferenceExcerpts = false

		c.noExcerpts = !excerpts
	})

	src.parsed.get(src.parse)

	return src
}
