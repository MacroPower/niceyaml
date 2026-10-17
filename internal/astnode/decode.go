package astnode

import (
	"fmt"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
)

// PanicError is the error of a panic of go-yaml's decoder on a node.
type PanicError struct {
	// Node is the node that the decoder read.
	Node ast.Node
	// Value is the value of the panic.
	Value any
}

// Error names the decoder of go-yaml and the value of the panic.
func (e *PanicError) Error() string {
	return fmt.Sprintf("go-yaml's decoder panicked: %v", e.Value)
}

// DecodeNode decodes node into v with dec, as [yaml.Decoder.DecodeFromNode]
// does. A panic of the decoder returns a [*PanicError].
func DecodeNode(dec *yaml.Decoder, node ast.Node, v any) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = &PanicError{Node: node, Value: p}
		}
	}()

	return dec.DecodeFromNode(node, v) //nolint:wrapcheck // The caller names the node.
}

// NodeToValue decodes node into v, as [yaml.NodeToValue] does. A panic of
// the decoder returns a [*PanicError].
func NodeToValue(node ast.Node, v any, opts ...yaml.DecodeOption) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = &PanicError{Node: node, Value: p}
		}
	}()

	return yaml.NodeToValue(node, v, opts...) //nolint:wrapcheck // The caller names the node.
}
