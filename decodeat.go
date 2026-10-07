package niceyaml

import (
	"context"

	"go.jacobcolvin.com/niceyaml/paths"
)

// DecodeAt validates and decodes the node path selects into a new T. It
// is [Node.At] and then [Node.Decode] on the Node that At returns, for a
// caller that reads one value and has no other use for that Node:
//
//	kind, err := doc.DecodeAt[string](ctx, paths.Current().Child("kind"))
//	if err != nil {
//		return err
//	}
//
// The path resolves as At resolves it, so an `@` path reads from the
// receiver and a `$` path from the root of the document. The decode runs
// on the node path selects, so each [Validator] from [WithValidator]
// gets a Node scoped to that node, and an `@` path in an error resolves
// from it. Every error is the one At or Decode returns, with the same
// text. A path that selects nothing returns the error of At, such as
// "cfg.yaml:1:1: $.kind: not found", and a document that did not parse
// returns the syntax error [Node.Err] returns. On error, the returned T
// is the zero value.
//
// DecodeAt reads a value the document must hold. An error that matches
// [paths.ErrNotFound] does not show that path selects nothing, since the
// decode returns such an error too. A Validator that reads a required
// key below the node returns the error of At for that key, as in
// "cfg.yaml:2:3: $.spec.hours.close: not found", and the node at path is
// present then. A caller that falls back to a default when the value is
// absent calls At, tests its error for paths.ErrNotFound, and decodes
// the Node only after that test.
//
// DecodeAt returns the value and drops the Node that holds it. A check
// the caller runs on the value writes `@` paths that read from the
// value, and the receiver resolves them from its own scope. For an Hours
// read at $.spec.hours, doc.Bind(check(h)) thus reports $.open, where the
// Node of the value reports $.spec.hours.open. A caller that binds errors
// of its own scopes the Node with At and binds through it, as At shows.
// A caller that holds the value alone puts the path of the value in
// front with [Rebase] before the receiver binds the error:
//
//	h, err := doc.DecodeAt[Hours](ctx, hoursPath)
//	if err != nil {
//		return err
//	}
//
//	return doc.Bind(niceyaml.Rebase(check(h), hoursPath))
//
// A document with no content, which [Node.IsEmpty] reports, holds no
// node for a path with a selector. DecodeAt returns the error At returns
// there, which wraps [paths.ErrNoDocument], where Decode on the root of
// such a document returns the zero value. The root path returns that
// error too in a document with no "---" header, such as an empty file.
//
// A caller that decodes through a [Decoder] scopes the Node with At and
// hands it to [Decoder.Decode], which runs the options of the Decoder on
// that Node.
func (n *Node) DecodeAt[T any](ctx context.Context, path paths.Path, opts ...DecodeOption) (T, error) {
	node, err := n.At(path)
	if err != nil {
		var zero T

		return zero, err
	}

	return node.Decode[T](ctx, opts...)
}
