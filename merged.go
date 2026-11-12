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
	"go.jacobcolvin.com/niceyaml/internal/multierr"
	"go.jacobcolvin.com/niceyaml/paths"
)

var (
	// ErrUnnamedKey indicates a mapping key that [NewSourceFromLayers] cannot
	// merge by. A key that is a sequence or a mapping does not have a name
	// that a path selects it by, as
	// [go.jacobcolvin.com/niceyaml/paths.Resolver.KeyName] reports. Two
	// layers thus cannot hold it as one key. The error binds at the key, in
	// the layer that holds it.
	ErrUnnamedKey = errors.New("mapping key has no name")

	// ErrUnknownLayer indicates a layer that [MergeAs] names and the layers
	// of [NewSourceFromLayers] do not hold. It is a mistake of the caller,
	// so the error does not bind in any layer and [IsInvalid] does not
	// report it.
	ErrUnknownLayer = errors.New("layer is not among the merged layers")

	// ErrAppendPath indicates a path of [MergeAppend] that holds a selector
	// other than a `.name` or a `.*` selector. [NewSourceFromLayers] matches
	// each path key by key against the mappings that merge. A path thus
	// holds one of those two selectors for each key above its sequence. It
	// is a mistake of the caller, so the error does not bind in any layer
	// and [IsInvalid] does not report it.
	ErrAppendPath = errors.New("append path takes only name and .* selectors")
)

// NewSourceFromLayers creates a new [*Source] that holds the one document
// that the given layers merge into. The layers go lowest first, such as a
// base file with the file of one environment over it. Each mapping merges
// into the mapping below it key by key. Each sequence and each scalar
// replaces what lies below it whole, so a layer cannot set one element of
// a sequence. [MergeAppend] names the sequences that add their elements
// to the sequence below them instead. A null that a layer writes as
// `null` or `~` removes its key and never adds one. The lowest layer that
// holds a value keeps the nulls it writes.
//
// A decode of the Source validates the merged document once. Each error
// binds in the file that holds the value that the error is about. The
// Layers section of the package documentation gives the merge rules
// in full and says where each error binds:
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
// layer alone reads it. The call skips each nil Layer, and when it skips
// every layer, it returns an empty, unnamed Source.
//
// The Source stands for the lowest layer, or for the layer [MergeAs]
// names. It takes the name, the file path, the file system, and the
// preamble of that layer. An error that does not carry a location binds
// in that layer.
// Every decode of the Source applies the unmarshalers of
// [MergeUnmarshalers], or else the unmarshalers that [WithUnmarshalers]
// gives the Source of that layer.
//
// The merge fails with the error of every layer that it cannot read a
// value from, lowest first. It reads each layer alone, so the error of
// one layer never comes from another. Such layers include:
//
//   - A Source of several documents.
//   - A document that did not parse.
//   - A document that a decode of it alone into an any value rejects.
//   - A document that goes past the alias limit of [WithAliasLimit].
//
// The merge expands each alias in full. A layer fails with an error
// that matches [ErrLimit] when its aliases repeat more text than the
// document byte limit of its Source allows, or nest the layer past its
// depth limit. A key that is a sequence or a mapping fails its layer
// with [ErrUnnamedKey]. The merge returns the error of each such layer,
// bound in its file, and a nil Source. The error of one layer comes
// back as that layer returns it, and the errors of several layers come
// back as one [errors.Join] of them. A layer given more than once
// reports its error once, and so does a Source given beside its own
// Node. Layers that fail with the same error also report it once, such
// as Node layers whose documents parse together, or layers that read
// one reference document that does not parse. When ctx ends, the merge
// returns the error of ctx alone and does not read another layer.
// A layer of MergeAs that the layers do not hold fails the call with
// [ErrUnknownLayer] before any layer merges. So does a path of
// MergeAppend that holds a selector it does not take, with
// [ErrAppendPath].
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
//   - [MergeAppend]
//   - [MergeAs]
//   - [MergeUnmarshalers]
type MergeOption func(*mergeConfig)

// mergeConfig holds the settings a [MergeOption] configures.
type mergeConfig struct {
	// The layer that the merged Source stands for, or nil for the lowest.
	as Layer
	// The paths of [MergeAppend], in the order given.
	appends []paths.Path
	// The unmarshalers of [MergeUnmarshalers], in the order given.
	unmarshalers []Unmarshaler
	// Whether the caller passed a MergeUnmarshalers option, even an empty
	// one. When it is set, unmarshalers replaces those of the layer that
	// the merged Source stands for.
	ownUnmarshalers bool
}

// MergeAs is a [MergeOption] that makes the Source [NewSourceFromLayers]
// builds stand for layer, such as the file of the user above defaults
// that the program embeds. The Source takes the name, the file path, the
// file system, and the preamble of that layer in place of those of the
// lowest layer. It also takes the unmarshalers that [WithUnmarshalers]
// gives the Source of that layer, unless [MergeUnmarshalers] gives the
// merge its own. An error that does not carry a location binds in that
// layer. The layers merge in the order given whichever layer the Source
// stands for, so a higher layer still sets the values it holds.
//
// The preamble holds the schema directive of the layer, so the directive
// of that layer picks the schema of the merged document. A relative
// reference in that directive names a file beside that layer. A server
// that passes MergeAs with an upload thus lets the upload pick its
// schema, as a validation of the upload alone does. A server that picks
// the schema itself leaves [go.jacobcolvin.com/niceyaml/schema.Directive]
// out of its registry.
//
// A nil layer, a nil Source, and a nil Node each keep the lowest layer, so
// an optional file goes to MergeAs as it is. The Source stands for the
// lowest layer when that file is missing, so unmarshalers given to that
// file alone apply only when it exists. A program gives its
// unmarshalers to MergeUnmarshalers instead. Every decode then reads the
// types of the program the same way whichever files exist.
//
// For any other layer that the layers do not hold, NewSourceFromLayers
// returns an error that wraps [ErrUnknownLayer]. It compares the
// document that each layer merges, so a [*Source] matches the [*Node]
// that its [Source.Document] returns, and that Node matches the Source.
// A Node that another call of [Node.At] returns does not match, and
// neither does a Source of the same text. The last of several MergeAs
// options applies.
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
// that a merge returns when it skips every layer. A Node that
// [Node.Origin] returns belongs to the document of a layer, so a decode
// of it applies the unmarshalers of that layer instead.
//
// A merge with MergeUnmarshalers does not take an unmarshaler from any
// layer, even when us is empty. Each MergeUnmarshalers adds to the ones
// before it, so a later function for a type replaces an earlier one and
// the last [JSONUnmarshalers] applies. MergeUnmarshalers copies the slice
// us, so the option keeps its unmarshalers when the caller changes the
// slice it passed.
func MergeUnmarshalers(us ...Unmarshaler) MergeOption {
	us = slices.Clone(us)

	return func(c *mergeConfig) {
		c.unmarshalers = slices.Concat(c.unmarshalers, us)
		c.ownUnmarshalers = true
	}
}

// MergeAppend is a [MergeOption] that makes the sequence at each of at
// add its elements to the sequence below it, in place of replacing that
// sequence. A format whose lists grow from layer to layer names them
// here, such as the plugins of a tool or the ports of a service:
//
//	merged, err := niceyaml.NewSourceFromLayers(ctx, []*niceyaml.Source{base, prod},
//		niceyaml.MergeAppend(
//			paths.Doc().Child("plugins"),
//			paths.Doc().Child("services").ChildAll().Child("ports"),
//		),
//	)
//
// Each path reads from the root of the merged document, whether it
// starts at `$` or at `@`. It holds `.name` and `.*` selectors only,
// where `.*` matches every key of a mapping. The merge matches a path
// against the names of the keys that merge, as
// [go.jacobcolvin.com/niceyaml/paths.Resolver.KeyName] gives them.
//
// For a path that holds any other selector, NewSourceFromLayers returns
// an error that wraps [ErrAppendPath]. An element of a sequence never
// merges with an element of a layer below. A path through `[0]` or `[*]`
// thus cannot name a sequence that two layers hold.
//
// The merged sequence holds the elements of the lowest layer first and
// those of each higher layer behind them, each as its layer writes it.
// The merge does not drop an element that two layers hold. A list whose
// elements merge by name goes in a mapping under those names instead,
// which merges key by key without the option.
//
// A sequence appends only to a sequence. A sequence that at names still
// replaces a scalar or a mapping below it, and a scalar or a mapping
// still replaces that sequence.
//
// A layer cannot replace a sequence that appends. An empty sequence, as
// in `plugins: []`, does not add an element and keeps the elements
// below it. A written null still removes the key with every element
// below it, and a sequence of a higher layer then starts the sequence
// again.
//
// Each element keeps the layer that wrote it. An error at or under an
// element binds in the file of that layer, with the index that the
// element has in that file. An error at `$.plugins[2]` of the merged
// document thus reads `$.plugins[0]` in the file whose first plugin it
// is. [Node.Origin] returns the Node at that path of the file.
//
// An error at the sequence itself binds in the highest layer that holds
// the sequence, as an error at a mapping does. A layer whose Source has
// positions off, as [WithPositions] sets them, gives way to a lower layer
// that holds the sequence. An error under an element stays in the file
// of that element when it binds at a value above the element, or at the
// key of such a value. An error below a sequence that decodes itself thus
// binds at that sequence in the file that wrote the element.
//
// Each MergeAppend adds its paths to the ones before it. MergeAppend
// copies the slice at, so the option keeps its paths when the caller
// changes the slice it passed.
func MergeAppend(at ...paths.Path) MergeOption {
	at = slices.Clone(at)

	return func(c *mergeConfig) {
		c.appends = slices.Concat(c.appends, at)
	}
}

// appendPaths returns the paths of [MergeAppend] as the merge matches
// them, each from the root of the merged value. It returns an error that
// wraps [ErrAppendPath] for the first path that holds a selector other
// than a `.name` or a `.*` selector.
func (c mergeConfig) appendPaths() (layermerge.Appends, error) {
	rels := make([]paths.Path, 0, len(c.appends))

	for _, path := range c.appends {
		for sel := range path.Selectors() {
			if sel.Kind != paths.SelectorChild && sel.Kind != paths.SelectorChildAll {
				return layermerge.Appends{}, fmt.Errorf("%w: %s", ErrAppendPath, path)
			}
		}

		rel, _ := path.CutPrefix(paths.Doc())
		rels = append(rels, rel)
	}

	return layermerge.NewAppends(rels...), nil
}

// Layer is one layer of the Source [NewSourceFromLayers] builds, a
// document that merges with the layers below it. [*Source] and [*Node]
// each implement Layer, and types outside this package cannot implement
// it. A Source stands for the one document that [Source.Document]
// returns. A Node stands for the root of a document, or for the value at
// a path of one when it comes from [Node.At]. [NewSourceFromLayers] skips
// a nil Layer, a nil Source, and a nil Node.
//
// See [*Source] and [*Node] for the implementations.
type Layer interface {
	// Resolves the layer as [NewSourceFromLayers] merges it.
	resolveLayer() resolvedLayer
}

// Origin returns the [*Node] that holds the value of n in the file of a
// layer of a Source that [NewSourceFromLayers] builds. For a Node of any
// other document, it returns n itself. A Node of a merged Source reads
// the merged text. It has the [Node.FilePath] and [Node.FS] of the layer
// that [MergeAs] names, or else of the lowest layer. A [Validator] or a
// function of [WithNodeCheck] that resolves a file beside a value thus
// reads the directory from the origin of that value:
//
//	origin, err := license.Origin()
//	if err != nil {
//		return err
//	}
//
//	dir := filepath.Dir(origin.FilePath())
//
// The origin lies in the highest layer whose document holds a value at
// the path of n. An error at that path binds in the same layer, as the
// Layers section of the package documentation describes. The origin has
// the lines, the positions, and the [Node.Path] of the document of that
// layer. A mapping that several layers hold has its origin in the highest
// of them whose Source reports positions, as [WithPositions] sets them,
// or else in the highest of them. That Node holds the keys that its own
// layer sets, but not the keys that the layers below add. The root
// follows the same rule. When no layer holds a value, the origin of the
// root lies in the highest layer of all.
//
// A sequence that [MergeAppend] names has its origin in the same layer
// as a mapping does. That Node holds the elements of its own layer
// alone. Each element has its origin in the layer that wrote it, at the
// index it has there.
//
// The origin decodes with the unmarshalers of the Source of its layer,
// and not with those of [MergeUnmarshalers]. So a program that decodes
// origins gives the same [WithUnmarshalers] to the Source of every layer.
//
// A value that a layer writes as an alias has its origin at that alias,
// as [Node.At] scopes it. [Node.Target] of the origin gives the content
// of the anchor. A value below such an alias, or one that a `<<` merge
// key brings in, has its origin at the anchor. A value below an alias
// to a reference document of [WithReferences], or one that a `<<` merge
// key brings in from a reference document, does not have a Node in its
// layer. For such a value, Origin returns the error that Node.At
// returns for its path. That error wraps
// [go.jacobcolvin.com/niceyaml/paths.ErrAlias] and binds to the layer
// that holds the alias.
func (n *Node) Origin() (*Node, error) {
	if !n.merges() {
		return n, nil
	}

	layer, held := n.source.layers.layer(n, n.base, n.base)
	path := held(n.base)

	// The path names the Node of the layer itself, which At fails to
	// select in a layer with neither a header nor content.
	if path.Equal(layer.node.base) {
		return layer.node, nil
	}

	return layer.node.At(path)
}

// layering holds the layers of a [Source] from [NewSourceFromLayers]. An
// error in the merged Source binds in a layer instead, because the merged
// text is not a file of the caller. When that layer is itself a merged
// Source, the error passes through it to a layer that a merge did not
// build.
type layering struct {
	// The merged value, or nil when the layers do not hold a value.
	root *layermerge.Value
	// The sequences that [MergeAppend] names, whose elements each keep
	// the layer that wrote them.
	appends layermerge.Appends
	// The layers that the merge read, lowest first. Each value names its
	// layer by the index of the layer here.
	layers []resolvedLayer
	// The index of the layer that the merged Source stands for, which
	// [MergeAs] names, or 0 for the lowest.
	identity int
}

// home returns the layer that the merged Source stands for, or the home
// of that layer when a merge built it. An error that does not have a
// layer of its own binds there. For a nil l, home returns the zero
// resolvedLayer.
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

// layerPath returns a `$` path of a merged document as one layer of it
// holds that path. The layer holds the merged value at the path of its
// own Node. It holds each element of a sequence that several layers
// append to at the index the element has in its own file.
type layerPath func(paths.Path) paths.Path

// layer returns the layer that an error at path binds in, where path is a
// `$` path in doc. It also returns the [layerPath] of that layer for the
// paths of doc. It picks the layer of the deepest value on path, as
// [layermerge.Value.ErrorLayer] gives it, or else the highest, and
// continues into a layer that a merge built.
//
// The error is about the value at through. Through is path itself,
// unless the error binds at a value above its own path. Below path,
// through can go into an element of a sequence that [MergeAppend] names.
// The error then binds in the layer of that element, because no other
// layer holds through. A path that ends in `~` names the key of an
// entry, and that key lies above the value of the entry. So such a path
// counts there as the path of that value.
func (l *layering) layer(doc *Node, path, through paths.Path) (resolvedLayer, layerPath) {
	if l == nil {
		return doc.resolveLayer(), func(p paths.Path) paths.Path { return p }
	}

	// The merged value stands at the root of its document, so the rest of
	// the path reads from the value of each layer.
	rel, _ := path.CutPrefix(paths.Doc())

	index := len(l.layers) - 1

	if at, _ := l.root.Find(rel); at != nil {
		index = at.ErrorLayer()
	}

	if _, ok := through.CutPrefix(path.Value()); ok {
		whole, _ := through.CutPrefix(paths.Doc())

		if elem := l.root.Element(whole, l.appends); elem != nil {
			index = elem.Layer
		}
	}

	layer := l.layers[index]

	holder, held := layer.source.layers.layer(layer.node, l.held(index, path), l.held(index, through))

	return holder, func(p paths.Path) paths.Path { return held(l.held(index, p)) }
}

// held returns path, a `$` path of the merged document, as the layer at
// index holds it. The layer holds the merged value at the path of its
// Node. It holds each element of a sequence at the index that
// [layermerge.Value.LayerPath] gives.
func (l *layering) held(index int, path paths.Path) paths.Path {
	rel, _ := path.CutPrefix(paths.Doc())

	return l.layers[index].node.base.Join(l.root.LayerPath(rel, index))
}

// value returns the value the layers hold at path, which starts at `$`,
// or nil when they do not hold one there. A nil l does not hold a value.
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
// The merge skips the zero value. A layer that does not have a Node holds
// an error that [mergeLayers] returns, so every layer of a [layering] has
// a Node.
type resolvedLayer struct {
	// The Source of the layer, where its errors bind.
	source *Source
	// The Node that the layer merges, or nil for a Source that does not
	// hold exactly one document.
	node *Node
	// The error that [Source.Document] returns for a Source that does not
	// hold exactly one document, or nil.
	err error
}

// resolveLayer returns n as the layer it is. A nil n gives the zero
// resolvedLayer, which the merge skips.
func (n *Node) resolveLayer() resolvedLayer {
	if n == nil {
		return resolvedLayer{}
	}

	return resolvedLayer{source: n.source, node: n}
}

// resolveLayer returns the document [Source.Document] returns as a layer,
// or a layer that holds its error in place of a Node. A nil s gives the
// zero resolvedLayer, which the merge skips.
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
// order of given. It also returns the index among them of the layer that
// the merged Source stands for. That layer is the first that resolves to
// the document of as, or the lowest at index 0 when as is nil, a nil
// Source, or a nil Node. It returns an error that wraps [ErrUnknownLayer]
// when no layer of given resolves to that document.
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

		if identity < 0 && resolved.source == named.source && resolved.node == named.node {
			identity = len(layers)
		}

		layers = append(layers, resolved)
	}

	if identity < 0 {
		return nil, 0, fmt.Errorf("merge as %q: %w", named.source.Name(), ErrUnknownLayer)
	}

	return layers, identity, nil
}

// read returns the value of l, the layer at index i, or nil when l does
// not hold one. It decodes the layer first, so a layer fails here as it
// fails alone. The reader expands each alias in full. So read also checks
// the expanded text against the alias limit and against the bounds that
// the limits of the Source of the layer set.
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

	err = aliasing.CheckDecodeText(decodedValue{n})
	if err != nil {
		return nil, n.aliasingError(err)
	}

	if !astnode.HasContent(n.valueNode()) {
		return nil, nil //nolint:nilnil // A layer with no content holds no value.
	}

	r := layermerge.Reader{
		Ctx:        ctx,
		Resolver:   n.doc.pathResolver(),
		Enclosed:   n.doc.state.Facts().Enclosed,
		References: n.source.referenceAnchors(),
		UnnamedKey: ErrUnnamedKey,
		Layer:      i,
		// A layer that a merge built reports positions as a whole when
		// any of its layers does. Carry narrows that to each value, to
		// the layer that holds the value inside the merge.
		Positions: n.source.ReportsPositions(),
	}

	v, err := r.Read(n.valueNode())
	if err != nil {
		if nodeErr, ok := errors.AsType[*layermerge.NodeError](err); ok {
			err = Invalid(nodeErr.Err, atToken(nodeErr.Token))
		}

		return nil, n.bindOwn(err)
	}

	v.Carry(n.source.layers.value(n.base), i)

	return v, nil
}

// identity returns a comparable value that names l. That is the Node of l,
// or its Source when l does not have a Node. So a layer given twice, or a
// Source given beside its own Node, has one identity.
func (l resolvedLayer) identity() any {
	if l.node != nil {
		return l.node
	}

	return l.source
}

// contextError returns the error of a context that ended, ctxErr, for a
// read of l that failed with err. That is err when err holds ctxErr, and
// otherwise ctxErr bound in l.
func (l resolvedLayer) contextError(err, ctxErr error) error {
	if errors.Is(err, ctxErr) {
		return err
	}

	if l.node != nil {
		return l.node.bindOwn(ctxErr)
	}

	return bindTree(ctxErr, binder{src: l.source})
}

// unreportedError returns the part of err, the error of a layer, that no
// error of reported holds. It returns nil when reported holds all of err.
// A Source whose documents fail to parse returns the binding of the join
// of their errors, and each Node of it returns one branch of that join.
// So unreportedError drops each branch of such a binding that reported
// holds, and the merge reports each error once in whatever order the
// layers come.
func unreportedError(reported []error, err error) error {
	held := func(e error) bool {
		return slices.ContainsFunc(reported, func(r error) bool { return errors.Is(r, e) })
	}

	if held(err) {
		return nil
	}

	bound, ok := err.(*SourceError) //nolint:errorlint // The error itself, not a chain search.
	if !ok || !bound.joinsBindings() {
		return err
	}

	branches := make([]error, 0, len(bound.below))
	for _, c := range bound.below {
		branches = append(branches, c.bound)
	}

	kept := slices.DeleteFunc(slices.Clone(branches), held)
	if len(kept) == len(branches) {
		return err
	}

	return bindTree(multierr.Join(kept), binder{src: bound.source})
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

	appends, err := cfg.appendPaths()
	if err != nil {
		return nil, err
	}

	if len(layers) == 0 {
		return NewSourceFromString("", WithUnmarshalers(cfg.unmarshalers...)), nil
	}

	var (
		root *layermerge.Value
		// The error of each layer that failed, lowest first.
		errs []error
		// The identity of each layer that failed, which the merge does not
		// read again.
		failed = map[any]bool{}
	)

	// No layer reads another, so every layer reports its own error. The
	// merge stops when ctx ends, because each later read fails with the
	// same error of ctx.
	for i, layer := range layers {
		// A layer that already failed fails again with the same error,
		// so the merge reports it once.
		key := layer.identity()
		if failed[key] {
			continue
		}

		v, err := layer.read(ctx, i)
		if err != nil {
			ctxErr := ctx.Err()
			if ctxErr != nil {
				return nil, layer.contextError(err, ctxErr)
			}

			failed[key] = true

			// Distinct layers that fail with the same error report it
			// once. Node layers whose documents parsed together share the
			// error of that parse. Layers that read one broken reference
			// document share the error of its parse.
			unseen := unreportedError(errs, err)
			if unseen != nil {
				errs = append(errs, unseen)
			}

			continue
		}

		if len(errs) == 0 {
			root = root.Merge(v, appends)
		}
	}

	if len(errs) > 0 {
		return nil, multierr.Join(errs)
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

	src := newMergedSource(named.source, text, showsLayers(layers), positionsLayers(layers), cfg)
	src.layers = &layering{root: root, appends: appends, layers: layers, identity: identity}

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
// read, so showsLayers reports false when any of them has excerpts off,
// which includes a Source with positions off.
func showsLayers(layers []resolvedLayer) bool {
	for _, layer := range layers {
		if !layer.source.ShowsExcerpts() || layer.source.noReferenceExcerpts {
			return false
		}
	}

	return true
}

// positionsLayers reports whether the Source of any of layers reports
// positions, as [WithPositions] sets them. A merged Source that holds
// such a layer then reports positions as a layer of another merge.
func positionsLayers(layers []resolvedLayer) bool {
	return slices.ContainsFunc(layers, func(layer resolvedLayer) bool {
		return layer.source.ReportsPositions()
	})
}

// newMergedSource returns a new [*Source] that holds text, with the name,
// the file, and the settings of from. It drops the references of from.
// Its excerpts are on when excerpts is true, and it reports positions
// when positions is true. It takes the unmarshalers of merge when merge
// sets its own. It parses text before it returns, so it binds each syntax
// error before the caller sets layers.
func newMergedSource(from *Source, text string, excerpts, positions bool, merge mergeConfig) *Source {
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
		c.noPositions = !positions

		// The merged text is text niceyaml wrote. It holds the content of
		// the layers and no more than the bounds of their limits let their
		// aliases add.
		c.noLimits()
	})

	src.parsed.get(src.parse)

	return src
}
