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
	// string text, as the edit that messages name label. A scalar that the
	// document writes keeps its style where text allows it. A field that
	// the document lacks goes first in its mapping, after the comment lines
	// at the top of the document. A document whose values above path leave
	// no mapping to hold the field, such as one whose root is a sequence,
	// stays as it is. Every error of SetField shows a bug in the engine.
	SetField func(ctx context.Context, d any, label string, path paths.Path, text string) error
)
