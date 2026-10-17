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

	"go.jacobcolvin.com/niceyaml/internal/hook"
	"go.jacobcolvin.com/niceyaml/paths"
)

var (
	// Locator returns the locate function of d, a *edit.Doc. The function
	// gives the place in the user's document of a location in the text of
	// d, or in a copy of that text that keeps every line and column. A
	// working Source of such a copy binds its errors in the user's
	// document through it.
	Locator func(d any) func(hook.Located) hook.Place

	// SetField sets the type field at path of d, a *edit.Doc, to the
	// string text. Messages name that edit by label. A scalar that the
	// document writes keeps its style where text allows it, and it keeps its
	// anchor, so each alias of the anchor reads the new value. A field that
	// the document lacks goes first in its mapping, after the comment lines
	// at the top of the document. The mappings above the field that the
	// document lacks go with it, as edit.Node.Set creates them.
	//
	// SetField fails with a problem of the document when the lookup of path
	// goes through a `<<` merge key that reads a reference document. The
	// problem matches [paths.ErrReferenceAlias] and not edit.ErrEngine.
	// SetField also fails with a problem of the document when the lookup of
	// path goes through an alias of the document that does not resolve, as
	// "<<: *x" does without an anchor x. That problem binds at the alias.
	// When the values above path leave no mapping to hold the field, as a
	// scalar, null, or a sequence on the way does, SetField fails as
	// edit.Node.Set fails there. That problem binds at the deepest value on
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
