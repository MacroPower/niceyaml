// Package hook holds the calls that the niceyaml package hands to its
// manifest, schema, and edit packages without adding them to its API. It
// also holds the call that the encoder package hands to the edit package.
//
// The niceyaml and encoder packages set each of their variables when they
// load, so a package that imports them finds every variable set. A
// parameter or a result that names a type of either, such as a
// *niceyaml.Source, has the type any here, because this package cannot
// import them.
package hook

import (
	"context"
	"reflect"

	"github.com/goccy/go-yaml/ast"

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
	// HasRange is false for an error that did not resolve a location.
	HasRange bool
}

// Place is where an error that resolved in a working Source binds in the
// document that the working text stands for.
type Place struct {
	// Details holds the errors that the binding gains as details. A detail
	// with a location binds in the home document.
	Details []error
	// Path is the path that the message shows in place of the path of
	// [Located], when HasPath is set. An error without a path shows it too.
	Path paths.Path
	// Range is the range of the text that the error marks in the home
	// document, when HasRange is set. Without it, the error binds to the
	// home document without a position.
	Range    position.Range
	HasPath  bool
	HasRange bool
}

var (
	// Tally returns the heading that niceyaml writes over members, which
	// counts their errors and their warnings, as in "2 errors, 1 warning".
	// It returns the member itself when members holds one error, and nil
	// when members is empty.
	Tally func(members []error) error

	// MarkWarning returns err with a warning mark above it, as
	// niceyaml.Node.Warn marks the error that it reports.
	MarkWarning func(err error) error

	// Warn reports err as a warning to the collector of ctx, as
	// niceyaml.Node.Warn does, and does not bind it to a document.
	Warn func(ctx context.Context, err error)

	// Rewrap returns err with wrap applied to the error that it binds, when
	// err is a *niceyaml.SourceError, and wrap(err) otherwise. The result
	// keeps the location, the source, and the children of the binding. So
	// a wrapper whose message is the message of the error it wraps adds a
	// type to the chain and does not change anything that a renderer shows.
	Rewrap func(err error, wrap func(error) error) error

	// WorkingSource returns a *niceyaml.Source of text, a working copy of
	// the document home, a *niceyaml.Node. The Source holds the settings of
	// the Source of home, including its name and its reference documents.
	// It skips the limits, because niceyaml wrote its text. It takes the
	// unmarshalers us, a []niceyaml.Unmarshaler, ahead of those of the
	// Source of home, as though niceyaml.WithUnmarshalers gave them first.
	// Every error bound in it binds in home instead, at the place that
	// locate gives for the location the error resolved in the working text.
	WorkingSource func(text string, home, us any, locate func(Located) Place) any

	// Reread returns a *niceyaml.Source of text that holds every setting
	// of src, a *niceyaml.Source: its name, its file path, its reference
	// documents, and its limits. A program that rewrites the file of src
	// reads the new text through it, as the next read of the file does.
	Reread func(src any, text string) any

	// Limits returns the byte limit and the depth limit of src, a
	// *niceyaml.Source, where zero turns a check off.
	Limits func(src any) (bytes, depth int)

	// AllowsDuplicateKeys reports whether src, a *niceyaml.Source, parses a
	// mapping that repeats a key, as niceyaml.WithAllowDuplicateKeys sets
	// it.
	AllowsDuplicateKeys func(src any) bool

	// Defaults returns the defaults of t under opts, a
	// []niceyaml.DecodeOption, as niceyaml.Defaults returns them. A panic
	// in a function of niceyaml.WithDefaults reaches the caller.
	Defaults func(t reflect.Type, opts any) reflect.Value

	// Layers returns the layers that niceyaml.NewSourceFromLayers merged
	// into src, a *niceyaml.Source, as the *niceyaml.Node of the document
	// that each layer reads, lowest first. It returns nil for a Source that
	// does not merge layers.
	Layers func(src any) []any

	// ReferenceName returns the name of the reference document that
	// defines the anchor of name for the document of n, a *niceyaml.Node.
	// An alias of name in that document reads this anchor when no anchor
	// of name comes before the alias. The name is the one that
	// niceyaml.Source.Name returns for the reference Source. ReferenceName
	// reports false when no reference document defines name.
	ReferenceName func(n any, name string) (string, bool)

	// ReferenceDocuments returns each document, a *niceyaml.Node, of the
	// reference documents of the Source of n, a *niceyaml.Node, and of
	// their own reference documents, once each. A document that does not
	// parse counts too.
	ReferenceDocuments func(n any) []any

	// AliasContent returns the node that the anchor of alias holds, where
	// alias belongs to the document of n, a *niceyaml.Node, or to one of
	// the reference documents of its Source. An alias that reads a
	// reference document reads the anchor there, as a decode does. The node
	// can hold an alias inside the value of its own anchor, as in
	// "&k !!str *k", which a decode reads as null. A caller that follows
	// such an alias again comes back to the same node. AliasContent returns
	// the error of paths.Resolver.Anchor for an alias that does not find an
	// anchor.
	AliasContent func(n any, alias *ast.AliasNode) (ast.Node, error)

	// Content returns the mapping, sequence, or scalar under node, a node
	// of the document of n, a *niceyaml.Node. It follows each alias to the
	// content of its anchor, as AliasContent reads it. A tag that converts
	// a collection under it, as "!!str *k" does, gives a scalar, so Content
	// returns the tag there. It returns nil for an alias that does not
	// resolve.
	Content func(n any, node ast.Node) ast.Node

	// Scope returns n, a *niceyaml.Node, scoped at node, a node of its
	// document, under path, as niceyaml.Node.At scopes the node that a path
	// selects. A decode of the result reads node, so it reads a node that no
	// path selects, such as the value of an entry that a later entry of its
	// key hides.
	Scope func(n any, path paths.Path, node ast.Node) any

	// KnownKeys returns a niceyaml.DecodeOption that admits the key at
	// each of keys, a path from `$` in the document that the decode reads.
	// A decode under niceyaml.UnknownFieldsReject or
	// niceyaml.UnknownFieldsWarn then reports every unknown field except
	// those keys. A key with a field in the target
	// decodes into that field. The decode skips each key without a field
	// that reads it, and every key below a value that decodes itself. Such
	// a key replaces each entry of its name that a `<<` merge key before it
	// brings in, so the decode skips those entries too. The validators of
	// the decode still see each key.
	KnownKeys func(keys []paths.Path) any

	// Marshal returns v as encoder.Marshal returns it under opts, a
	// []encoder.Option, with the node that go-yaml's encoder made for v
	// and that the encoder printed. Each scalar of the node holds the value
	// that go-yaml's encoder gave it, which a parse of the text might read
	// otherwise.
	Marshal func(ctx context.Context, v, opts any) ([]byte, ast.Node, error)

	// OwnDecode decodes the first document of text into an any with
	// go-yaml's decoder alone, as yaml.Unmarshal decodes it. With comments,
	// the decoder parses the text with its comments, as it does for a
	// program that asks for them with yaml.CommentToMap. The decoder reads
	// the reference documents of the Source of n, a *niceyaml.Node. It
	// takes a key that a mapping repeats only when that Source allows
	// duplicate keys. A panic of the decoder comes back as a
	// *niceyaml.PanicError.
	OwnDecode func(ctx context.Context, n any, text string, comments bool) (any, error)

	// DecodeChanges reports whether a decode of the document of n, a
	// *niceyaml.Node, reads a tree that differs from the document, as for a
	// document that gives two anchors one name. The decode reads any other
	// document from its own tree. Go-yaml's comment-free parse of the text,
	// which yaml.Unmarshal reads, can still read such a document apart.
	DecodeChanges func(n any) bool

	// SitePath returns the path of the node of the document of n, a
	// *niceyaml.Node, that holds the token at pos, as
	// pathindex.Index.SitePath spells it with key, and whether pos lies in
	// the key of the entry that the path names. The position is in the
	// coordinates of niceyaml.Source.Lines, as for niceyaml.Node.PathAt. The
	// last result reports false where no token lies at pos.
	SitePath func(n any, pos position.Position, key bool) (path paths.Path, inKey, ok bool)

	// LineBreaks returns what read returns for the text of src, a
	// *niceyaml.Source. It calls read on its first call for src alone, so
	// the edit package reads the line breaks of a Source once for all of
	// its documents.
	LineBreaks func(src any, read func(text string) any) any
)
