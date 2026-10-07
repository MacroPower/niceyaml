package niceyaml

import (
	"context"
	"errors"

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
// absent calls [Node.DecodeIfPresent], which reports an absent value
// apart from every other failure.
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
func (n *Node) DecodeAt[T any](ctx context.Context, path paths.Path, opts ...DecodeOption) (T, error) {
	node, err := n.At(path)
	if err != nil {
		var zero T

		return zero, err
	}

	return node.Decode[T](ctx, opts...)
}

// DecodeIfPresent validates and decodes the node path selects into v,
// and reports whether the document holds a node at path. It is
// [Node.At] and then [Node.DecodeInto] on the Node that At returns, for
// a value the document may leave out. The caller fills v with its
// default first, and a path that selects nothing leaves v as it is:
//
//	version := 1
//
//	found, err := doc.DecodeIfPresent(ctx, versionPath, &version)
//	if err != nil {
//		return err
//	}
//
//	if !found {
//		log.Print("the config names no version, so it reads as version 1")
//	}
//
// DecodeIfPresent returns false with a nil error in one case alone, when
// At returns an error that matches [paths.ErrNotFound]. Every other
// failure returns false and the error. That holds for the syntax error
// of a document that did not parse, for an alias on the path that does
// not resolve, for a wildcard path, and for every error of the decode.
// An error of the decode can match paths.ErrNotFound itself, as
// [Node.DecodeAt] describes, and it comes back as an error all the same.
// A node that is present and fails a [Validator] therefore never reads
// as absent.
//
// A null is present. For `version: ~`, found is true and version keeps
// its default, since a decode leaves v as it is under a null with no
// tag, as DecodeInto describes. A v that points to a pointer or an
// interface is the exception, and such a null sets that pointer or
// interface to nil. The default thus covers an absent value and a null
// alike, and found tells the two apart.
//
// A value is absent when At selects no node for path. A key looked up in
// a value that is no mapping selects nothing, so $.server.port is absent
// from `server: hello`, where a decode of the whole document into a
// struct reports "expected mapping, got string" for server. An index
// past the end of a sequence is absent too. A document with no content,
// which [Node.IsEmpty] reports, holds no node for a path with a
// selector, so every such path is absent there.
// [go.jacobcolvin.com/niceyaml/schema/matcher.Exists] finds a path
// absent in the same cases.
//
// The target v must be a non-nil pointer. Any other v returns an error
// wrapping [ErrDecodeTarget], bound to the source, before the path
// resolves, so the mistake shows whether or not the document holds the
// value.
//
// The decode runs on the node path selects, so each Validator from
// [WithValidator] gets a Node scoped to that node. DecodeIfPresent drops
// that Node as DecodeAt does, so a caller that binds errors of its own
// about v scopes the Node with At or rebases them, as DecodeAt
// describes.
func (n *Node) DecodeIfPresent(ctx context.Context, path paths.Path, v any, opts ...DecodeOption) (bool, error) {
	// The target check runs first, so a wrong target fails for an absent
	// path too, where no decode would run to reject it.
	err := checkDecodeTarget(v)
	if err != nil {
		return false, n.bindOwn(err)
	}

	node, err := n.At(path)
	if errors.Is(err, paths.ErrNotFound) {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	// The decode can return an error that matches paths.ErrNotFound too,
	// so only the error of At above counts as an absent value.
	err = node.DecodeInto(ctx, v, opts...)
	if err != nil {
		return false, err
	}

	return true, nil
}
