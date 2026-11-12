// Package edithook holds the calls that the edit package hands to the
// manifest package without adding them to its API.
//
// The edit package sets each variable when it loads, so a package that
// imports edit finds every variable set. A parameter that names a
// *edit.Doc has the type any here, because this package cannot import
// edit.
package edithook

import (
	"context"

	"go.jacobcolvin.com/niceyaml/paths"
)

var (
	// Bounds returns the bytes [start, end) of the text of the Source of d,
	// a *edit.Doc, that the text of d stands for. The function
	// edit.FileText writes edit.Doc.Text in place of those bytes. A caller
	// that keeps the Doc.Text of one moment writes that text in place of
	// the same bytes. Its file then reads as FileText returned it at that
	// moment. The bytes hold the lines of niceyaml.Node.Span of the
	// document, without a byte order mark at the start of its first line.
	Bounds func(d any) (start, end int)

	// SetField sets the protected type field at path of d, a *edit.Doc, to
	// the string text, outside any Apply. The write reaches a value of
	// edit.WithProtected, as an Apply with edit.WithoutProtection does, so
	// the manifest package writes the type fields that its steps cannot
	// touch. Messages name that edit by label. A scalar that the document
	// writes keeps its style where text allows it, and it keeps its anchor,
	// so each alias of the anchor reads the new value. A field that the
	// document lacks goes first in its mapping, as edit.Node.Set with
	// edit.First places it, after the comment lines at the top of the
	// document. Where the document lacks the mapping too, the field goes in
	// as edit.Node.Set creates a value, with the mappings above it that the
	// document lacks, after the last entry of the mapping that takes them.
	//
	// SetField fails with a problem of the document when a `<<` merge key
	// brings in a name of path from a reference document. The problem
	// matches [paths.ErrReferenceAlias] and not edit.ErrEngine.
	// SetField also fails with a problem of the document when the lookup of
	// path goes through an alias of the document that does not resolve, as
	// "<<: *x" does without an anchor x. That problem binds at the alias.
	// A null on the way becomes the mapping that holds the field, as
	// edit.Node.Set describes. When the values above path leave no mapping
	// to hold the field, as a scalar or a sequence on the way does,
	// SetField fails as edit.Node.Set fails there. That problem binds at the deepest value on
	// path that the document holds and names what the value holds. It does
	// not match paths.ErrReferenceAlias or edit.ErrEngine. The parse of the
	// Source can refuse the key of the field, because the mapping holds
	// another key of the same text. That problem of the
	// document binds at the key of the field, and it does not match
	// paths.ErrReferenceAlias or edit.ErrEngine either.
	//
	// A write that reaches YAML that the engine does not edit fails with
	// edit.ErrUnsupported, as edit.Node.Set fails there. A write that makes
	// a `<<` merge key that go-yaml's decoder reads otherwise than the merge
	// spec fails with edit.ErrEngine, as a Set that makes one does. Every
	// other error of SetField shows a bug in the engine. A write that fails
	// leaves the document as it found it.
	SetField func(ctx context.Context, d any, label string, path paths.Path, text string) error
)
