// Package migrate holds the handle that a migration step of the manifest
// package gets: a [*Node] on one place of a document, which reads the value
// there and edits the document.
//
// A step is a [Func] that go.jacobcolvin.com/niceyaml/manifest.Migrate adds
// to a version. A load calls it on each place that its path selects, as the
// document stood once the steps before it ran, and the edits of the step
// keep the comments and the formatting of every value they leave alone.
// Every error and warning about an edited value binds at the line that the
// user wrote.
//
// A step must return the same edits for the same document. A load calls
// it at each load of an older document, and a plan calls it again, so a
// step that reads the clock, the environment, or a random source gives each
// run a different document.
package migrate

import (
	"context"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/manifest/internal/edit"
	"go.jacobcolvin.com/niceyaml/paths"
)

// Func is a migration step. It edits a document on its way from one
// version to the next, through n, a handle on one place that the path of
// the step selects. An error fails the document, and an error without a
// location binds at the value of n.
type Func func(ctx context.Context, n *Node) error

// Node is a handle on one place of a document as the earlier steps left
// it: the root, an entry of a mapping, or an item of a sequence. It reads
// the value there and edits the document. A handle on a value that an
// edit renamed or moved names its new place.
//
// A method that cannot make its edit returns an error that names the
// problem, and the document fails with that error even when the step goes
// on and returns nil.
//
// Receive instances as the argument of a [Func], or from [Node.At].
type Node struct {
	h *edit.Handle
}

//nolint:gochecknoinits // The engine creates the handles of the steps.
func init() {
	edit.NewNode = func(h *edit.Handle) any { return &Node{h: h} }
}

// Path returns the `$` path of the place as the document stands now.
func (n *Node) Path() paths.Path {
	return n.h.Path()
}

// Key returns the key of the entry, or "" for a sequence item or the
// root.
func (n *Node) Key() string {
	return n.h.Key()
}

// Exists reports whether the place holds a value.
func (n *Node) Exists() bool {
	return n.h.Exists()
}

// Kind returns the kind of the value, or [niceyaml.NodeNone] when the
// place holds none.
func (n *Node) Kind() niceyaml.NodeKind {
	return n.h.Kind()
}

// Text returns the text of a scalar as the document writes it, without its
// quotes, or "" for a mapping or a sequence.
func (n *Node) Text() string {
	return n.h.Text()
}

// Decode decodes the value as [niceyaml.Node.Decode] does, with opts and the
// settings of the Source of the file, such as its reference documents. The
// unmarshalers of
// [go.jacobcolvin.com/niceyaml/manifest.WithUnmarshalers] apply too, ahead
// of those of the Source. Decode leaves out the decode options of the
// registry and the kind, which describe the current version. A step passes
// the ctx it got, so the warnings of the decode join the warnings of the
// load.
func (n *Node) Decode[T any](ctx context.Context, opts ...niceyaml.DecodeOption) (T, error) {
	var zero T

	doc, err := n.h.Node()
	if err != nil {
		return zero, err //nolint:wrapcheck // The handle binds the error.
	}

	return doc.Decode[T](ctx, opts...)
}

// At returns a handle on the place at path, which might hold no value. A
// `$` path reads from the root, and an `@` path reads from n. Exists
// reports false where the place holds no value, and every method but Set
// changes nothing there.
//
// At fails the step on a path that selects more than one value, holds a
// `~`, or goes through an alias that does not resolve, and each method of
// its handle that edits or decodes returns the same error.
func (n *Node) At(path paths.Path) *Node {
	return &Node{h: n.h.At(path)}
}

// SetKey renames the key of the entry. The value and its comments stay in
// place, and the key keeps its quotes. A key that the entry already has,
// as the decode reads it, changes nothing. SetKey fails on a sequence item,
// which has no key, and on a key that the mapping holds already.
//
//nolint:contextcheck // A rename writes text and reads no state that a context carries.
func (n *Node) SetKey(key string) error {
	return n.h.SetKey(key) //nolint:wrapcheck // The handle binds the error.
}

// Set replaces the value with v, or creates it and the mappings above it.
// It writes v as [go.jacobcolvin.com/niceyaml/encoder.Marshal] does, keeps
// the comments of the entry, and drops the tag of the old value and the
// comments inside it. A Go string keeps the quotes of the scalar it
// replaces. Set at the index past the last item of a sequence appends an
// item.
//
// A step should build v from types that it declares itself and from the
// standard library, because a Go type of the app changes in later versions.
func (n *Node) Set(ctx context.Context, v any) error {
	return n.h.Set(ctx, v) //nolint:wrapcheck // The handle binds the error.
}

// Move moves the value and its comments to the `$` path, which it creates
// as Set does, so the index past the last item of a sequence appends an
// item. The value keeps its text, indented for its new place.
// Move fails on a target that holds a value or that lies inside the value.
//
//nolint:contextcheck // A move writes text and reads no state that a context carries.
func (n *Node) Move(path paths.Path) error {
	return n.h.Move(path) //nolint:wrapcheck // The handle binds the error.
}

// Remove removes the entry or the item, with its comments. A load warns
// at the removed value with reason, and a rewrite lists the removal.
func (n *Node) Remove(reason string) error {
	return n.h.Remove(reason) //nolint:wrapcheck // The handle binds the error.
}
