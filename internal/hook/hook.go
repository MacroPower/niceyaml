// Package hook holds the calls that the niceyaml package hands to its
// manifest and schema packages without adding them to its API.
//
// The niceyaml package sets each variable when it loads, so a package that
// imports both finds every variable set. A parameter or a result that names
// a niceyaml type, such as a *niceyaml.Source, has the type any here,
// because this package cannot import niceyaml.
package hook

import (
	"context"
	"reflect"

	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

// Located is where an error resolved in a working Source: the path its
// message shows, and the range of the text it marks.
type Located struct {
	// Path is the path of the error, from `$`, when HasPath is set.
	Path paths.Path
	// Range is the range of the text that the error marks in the working
	// text, when HasRange is set.
	Range   position.Range
	HasPath bool
	// HasRange is false for an error that resolved no location.
	HasRange bool
}

// Place is where an error that resolved in a working Source binds in the
// document that the working text stands for.
type Place struct {
	// Details holds the errors that the binding gains as details. A detail
	// with a location binds in the home document.
	Details []error
	// Path is the path that the message shows in place of the path of
	// [Located], when HasPath is set.
	Path paths.Path
	// Range is the range of the text that the error marks in the home
	// document, when HasRange is set. Without it, the error binds to the
	// home document with no position.
	Range    position.Range
	HasPath  bool
	HasRange bool
}

var (
	// Tally returns the heading that niceyaml writes over members, which
	// counts their errors and their warnings, as in "2 errors, 1 warning".
	// It returns the one member alone, and nil for no member.
	Tally func(members []error) error

	// MarkWarning returns err with a warning mark above it, as
	// niceyaml.Node.Warn marks the error that it reports.
	MarkWarning func(err error) error

	// Warn reports err as a warning to the collector of ctx, as
	// niceyaml.Node.Warn does, and binds it to no document.
	Warn func(ctx context.Context, err error)

	// Rewrap returns err with wrap applied to the error that it binds, when
	// err is a *niceyaml.SourceError, and wrap(err) otherwise. The result
	// keeps the location, the source, and the children of the binding, so
	// a wrapper whose message is the message of the error it wraps adds a
	// type to the chain and changes nothing that a renderer shows.
	Rewrap func(err error, wrap func(error) error) error

	// WorkingSource returns a *niceyaml.Source of text, a working copy of
	// the document home, a *niceyaml.Node. The Source holds the settings of
	// the Source of home, its name and its reference documents included,
	// and skips the limits, because niceyaml wrote its text. Every error
	// bound in it binds in home instead, at the place that locate gives for
	// the location the error resolved in the working text.
	WorkingSource func(text string, home any, locate func(Located) Place) any

	// Reread returns a *niceyaml.Source of text that holds every setting
	// of src, a *niceyaml.Source: its name, its file path, its reference
	// documents, and its limits. A program that rewrites the file of src
	// reads the new text through it, as the next read of the file does.
	Reread func(src any, text string) any

	// Limits returns the byte limit and the depth limit of src, a
	// *niceyaml.Source, where zero turns a check off.
	Limits func(src any) (bytes, depth int)

	// Defaults returns the defaults of t under opts, a
	// []niceyaml.DecodeOption, as niceyaml.Defaults returns them. A panic
	// in a function of niceyaml.WithDefaults reaches the caller.
	Defaults func(t reflect.Type, opts any) reflect.Value

	// Layers returns the layers that niceyaml.NewSourceFromLayers merged
	// into src, a *niceyaml.Source, as the *niceyaml.Node of the document
	// that each layer reads, lowest first. It returns nil for a Source that
	// merges no layers.
	Layers func(src any) []any
)
