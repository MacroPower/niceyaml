package niceyaml

import (
	"fmt"

	"go.jacobcolvin.com/niceyaml/internal/docstate"
)

func init() {
	docstate.Of = documentState
}

// documentState returns the [*docstate.State] of the document of n, which
// must be a [*Node], or nil for a nil Node. It is [docstate.Of], through
// which the packages of the module reach the state a document keeps.
func documentState(n any) *docstate.State {
	node, ok := n.(*Node)
	if !ok {
		panic(fmt.Sprintf("docstate.Of: %T is not a *niceyaml.Node", n))
	}

	if node == nil {
		return nil
	}

	return node.doc.state
}
