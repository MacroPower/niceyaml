package niceyaml

import (
	"github.com/goccy/go-yaml/ast"

	"go.jacobcolvin.com/niceyaml/internal/hook"
	"go.jacobcolvin.com/niceyaml/internal/mapkey"
)

// init sets [hook.Keys], as the package [hook] describes.
//
//nolint:gochecknoinits // The hooks reach no API, so only init can set them.
func init() {
	hook.Keys = func(n any) *mapkey.Keys {
		node, _ := n.(*Node) //nolint:errcheck // The hook takes a *Node.

		return node.keyReader()
	}
}

// keyReader returns a new [*mapkey.Keys] of the keys of the document of
// n. It names each key as a decode of the document reads it, with each
// alias bound as [document.resolveAlias] binds it.
func (n *Node) keyReader() *mapkey.Keys {
	var (
		body ast.Node
		refs mapkey.Resolver
	)

	if n.doc.root != nil {
		body = n.doc.root.Body
	}

	if n.source.referenceAnchors() != nil {
		refs = n.source.referenceAlias
	}

	return mapkey.NewKeys(body, n.doc.resolveAlias, refs)
}
