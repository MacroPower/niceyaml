package niceyaml

import (
	"context"
	"reflect"
	"slices"

	"github.com/goccy/go-yaml/ast"

	"go.jacobcolvin.com/niceyaml/internal/decodetree"
	"go.jacobcolvin.com/niceyaml/internal/hook"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

// init sets the calls that niceyaml hands to its manifest and schema
// packages, as the package [hook] describes.
//
//nolint:gochecknoinits // The hooks reach no API, so only init can set them.
func init() {
	hook.Tally = newTally
	hook.MarkWarning = func(err error) error { return remark(err, severityWarning) }
	hook.Warn = func(ctx context.Context, err error) {
		if !isNothing(err) {
			deliver(ctx, remark(err, severityWarning))
		}
	}
	hook.Isolate = func(ctx context.Context, w any) context.Context {
		held, _ := w.(*Warnings) //nolint:errcheck // The hook takes a *Warnings.

		return isolate(ctx, held)
	}
	hook.Rewrap = rewrap
	hook.Defaults = func(t reflect.Type, opts any) reflect.Value {
		o, _ := opts.([]DecodeOption) //nolint:errcheck // The hook takes the options of a decode.

		return newDecodeConfig(nil, o).defaults.value(t)
	}
	hook.Layers = func(src any) []any {
		s, _ := src.(*Source) //nolint:errcheck // The hook takes a *Source.

		return s.layerNodes()
	}
	hook.ReferenceName = func(n any, name string) (string, bool) {
		node, _ := n.(*Node) //nolint:errcheck // The hook takes a *Node.

		return node.source.referenceName(name)
	}
	hook.ReferenceDocuments = func(n any) []any {
		node, _ := n.(*Node) //nolint:errcheck // The hook takes a *Node.

		var out []any

		for _, doc := range node.source.referenceDocuments(map[*Source]bool{}) {
			out = append(out, doc)
		}

		return out
	}
	hook.AliasContent = func(n any, alias *ast.AliasNode) (ast.Node, error) {
		node, _ := n.(*Node) //nolint:errcheck // The hook takes a *Node.

		return node.doc.aliasContent(alias)
	}
	hook.Content = func(n any, node ast.Node) ast.Node {
		doc, _ := n.(*Node) //nolint:errcheck // The hook takes a *Node.

		return doc.doc.content(node)
	}
	hook.Scope = func(n any, path paths.Path, node ast.Node) any {
		doc, _ := n.(*Node) //nolint:errcheck // The hook takes a *Node.

		return doc.scoped(path, node)
	}
	hook.OwnDecode = func(ctx context.Context, n any, text string, comments bool) (any, error) {
		node, _ := n.(*Node) //nolint:errcheck // The hook takes a *Node.

		return node.source.ownDecode(ctx, text, comments)
	}
	hook.SitePath = func(n any, pos position.Position, key bool) (paths.Path, bool, bool) {
		node, _ := n.(*Node) //nolint:errcheck // The hook takes a *Node.

		tk := node.source.lines.TokenAt(pos)
		if tk == nil || tk.Position == nil || node.doc.err != nil {
			return paths.Path{}, false, false
		}

		path, inKey := node.doc.pathIndex().SitePath(node.doc.pathResolver(), tk.Position.Offset, key)

		return path, inKey, true
	}
	hook.LineBreaks = func(src any, read func(string) any) any {
		s, _ := src.(*Source) //nolint:errcheck // The hook takes a *Source.

		return s.lineBreaks.get(func() any { return read(s.Text()) })
	}
	hook.DecodeChanges = func(n any) bool {
		node, _ := n.(*Node) //nolint:errcheck // The hook takes a *Node.
		d := node.doc

		// A tree changes its document only where decodetree.Changes reports
		// that it can, so a document without such a change does not build
		// the tree.
		return decodetree.Changes(d.root.Body, d.state.Facts(), len(d.node.source.references) > 0) &&
			d.tree().Changed()
	}
	hook.KnownKeys = func(keys []paths.Path) any {
		keys = slices.Clone(keys)

		return DecodeOption(func(c *decodeConfig) {
			c.knownKeys = append(c.knownKeys, keys...)
		})
	}
}

// rewrap returns a copy of err, a binding, with wrap applied to the error
// that err binds, as [hook.Rewrap] describes. For any other err, it
// returns wrap(err).
func rewrap(err error, wrap func(error) error) error {
	x, ok := err.(*SourceError) //nolint:errorlint // The node itself, not a chain search.
	if !ok || x == nil {
		return wrap(err)
	}

	c := &SourceError{
		err:     wrap(x.err),
		locErr:  x.locErr,
		source:  x.source,
		node:    x.node,
		kept:    x.kept,
		keptDoc: x.keptDoc,
		loc:     x.loc,
		end:     x.end,
		ranges:  x.ranges,
		below:   x.below,
		rng:     x.rng,
		adopted: x.adopted,
	}

	if x.free != nil {
		c.free = wrap(x.free)
	}

	return c
}

// layerNodes returns the document of each layer that s merges, lowest
// first, or nil for a Source that does not merge layers.
func (s *Source) layerNodes() []any {
	if s == nil || s.layers == nil {
		return nil
	}

	nodes := make([]any, 0, len(s.layers.layers))
	for _, l := range s.layers.layers {
		if l.node != nil {
			nodes = append(nodes, l.node)
		}
	}

	return nodes
}
