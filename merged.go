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

// layering is what the [Source] that [NewSourceFromLayers] builds knows
// of its layers: the merged value, which names the layer each
// value came from, and the lowest and the highest layer. An error bound
// in the one document of that Source binds in one of the layers instead,
// since the merged text is no file of the caller. An error with a
// location binds in the layer [layering.layer] picks, and any other
// error in the lowest layer, whose name the Source has.
//
// A layer can be a merged Source, or a Node of its document, whose text
// is no file of the caller either. An error that reaches such a layer
// binds in a layer of that document instead, as an error bound through
// that Node does. Every error thus binds in a document that no merge
// built.
type layering struct {
	// The merged value, or nil when no layer holds a value.
	root *layermerge.Value
	// The layers the merge read, lowest first, which each value names by
	// its index.
	layers []resolvedLayer
}

// home returns the layer an error binds in when it found no layer of its
// own, which is the lowest one. A lowest layer that a merge built
// gives the home of its own layers. A nil l has no home, and home then
// returns the zero value.
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

// layer returns the layer an error at path binds in, and two paths in
// the document of that layer: the one of the value path reads from, and
// path as that document reads it. The path starts at `$` and reads from
// the root of doc, the merged document. A nil l binds in doc, where `$`
// and path stay as they are.
//
// The search follows path down the merged value for as long as the value
// holds it. The result is the layer of the last value it reaches, which
// is the highest layer whose document holds that value. For a path the
// merged value holds, that is the layer the value came from. For a path
// that names a key a mapping leaves out, it is the highest layer that
// holds the mapping. For a path that names a key a layer removed, it is
// the layer of the null that removed the key. It is the highest layer of
// all when no layer holds a value.
//
// A layer that a merge built holds the value in a layer of its
// own, so the search goes on there with the path as the layer reads it.
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

// resolvedLayer is one layer of a merged [Source], as [Layer] resolves it. The
// zero value is a layer that adds nothing. A layer with no Node holds an
// error in its place, which [mergeLayers] returns, so every layer of a
// [layering] has a Node.
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

// resolveLayer returns the one document of s as a layer, as
// [Source.Document] returns it. A Source that holds no one document gives
// a layer that holds no value and no Node, with the error Document
// returns. A nil s adds nothing.
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

// read returns the value of the layer, the one at index i among the
// layers of a merge, as a [layermerge.Reader] reads it from the document
// of its Node, or nil when the layer holds none, as the document of
// comments alone does. A layer from a [Source] that holds no one
// document returns its error.
//
// A layer that a decode rejects returns the error of that decode, bound
// in the layer, so a layer fails here as it fails on its own. The reader
// writes each alias out in full, so the layer must also pass the alias
// limit as a decode into a type that reads text does. The read stops
// with the error of the context once it ends, bound in the layer.
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

// mergeLayers returns the [*Source] that given merge into, lowest first,
// as [NewSourceFromLayers] describes. The merge decodes each layer with
// ctx. A layer that holds no value a decode can read returns its error,
// bound in the layer, and the merge reads no layer above it. The merge
// returns the error of ctx once ctx ends, and checks ctx once more
// before it parses the merged text, which the aliases of the layers can
// make far longer than the layers are.
//
// The Source holds the merged value as YAML text below the preamble of
// the lowest layer. It takes its name, its file path, and its file system
// from the Source of that layer, with the settings
// [WithAllowDuplicateKeys] and [WithAliasLimit] gave it. It takes no
// reference documents, since the merged value holds no alias. It has
// excerpts off when [showsLayers] reports false, whatever [WithExcerpts]
// gave the lowest layer. When given adds no layer, the Source is empty
// and has no name, and its errors bind with no position.
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

// showsLayers reports whether an error may show the text layers merge
// into, as [WithExcerpts] says it for the [Source] of each. That text
// holds the values of every layer, and the values an alias of a layer
// reads from a reference document. It is thus out of excerpts when the
// text of any layer is, or the text of a reference document of one.
func showsLayers(layers []resolvedLayer) bool {
	for _, layer := range layers {
		if layer.source.noExcerpts || layer.source.noReferenceExcerpts {
			return false
		}
	}

	return true
}

// newMergedSource returns a new [*Source] that holds text, with the name,
// the file, and the settings of from, as [mergeLayers] lists them. An
// error may show the text in an excerpt when excerpts is set, whatever
// [WithExcerpts] says of from. The Source parses in the call, so it
// binds any syntax error of the text before the merge marks it as one
// that merges layers.
func newMergedSource(from *Source, text string, excerpts bool) *Source {
	src := NewSourceFromString(text, func(c *sourceConfig) {
		*c = from.sourceConfig

		// The merged value holds no alias, so it reads no reference
		// document.
		c.references = nil
		c.noReferenceExcerpts = false

		c.noExcerpts = !excerpts
	})

	src.parsed()

	return src
}
