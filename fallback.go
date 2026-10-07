package niceyaml

import (
	"errors"
	"reflect"

	"github.com/goccy/go-yaml/ast"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/yamlfield"
	"go.jacobcolvin.com/niceyaml/paths"
)

// WithFallback is a [DecodeOption] for a value that several files fill in
// turn. It names the Nodes the value decoded from before the Node of the
// call, nearest first. The self-validation step then binds each error in
// the file that set the value the error is about.
//
// A program that layers one file over another decodes both into one
// value, and validates the value once the last file has set it:
//
//	var cfg Config
//	if err := base.DecodeInto(ctx, &cfg, niceyaml.WithSelfValidation(false)); err != nil {
//		return err
//	}
//
//	return prod.DecodeInto(ctx, &cfg, niceyaml.WithFallback(base))
//
// Without the option, every error binds in the Node of the call, so a
// port that only base.yaml sets reports a line of prod.yaml. With it, the
// error names the line that sets the port:
//
//	base.yaml:3:9: $.server.port: port must be at least 1
//
// [Node.SelfValidate] takes the option too, for a program that applies
// its environment or its flags after the last file:
//
//	err := prod.SelfValidate(ctx, &cfg, niceyaml.WithFallback(base))
//
// The Node of the call and the Nodes in below are the layers of the
// value, from the top down. An error binds in one of them by the way
// [Node.DecodeInto] fills a value that an earlier decode set. A mapping
// merges into a struct field by field, so a field keeps the value of a
// lower layer when the layers above leave its key out. The option
// therefore follows the path of an error down the type of the value for
// as long as the path names struct fields, through pointers to structs
// and inline fields. The error binds in the highest layer whose document
// holds a value at the end of that stretch, and the rest of the path
// resolves in that document. A slice, an array, a map, and a value of an
// interface type end the stretch, since the highest layer that holds one
// replaced it whole. An error under an element or an entry thus binds in
// the layer that holds the collection, and never in a layer below it,
// whose elements the decode discarded. A type that decodes itself,
// through an UnmarshalYAML or UnmarshalText method, ends the stretch the
// same way.
//
// A null leaves a field as the layers below set it, so a layer that holds
// a null there holds no value. A null in a pointer field sets the pointer
// to nil instead, so an error at that field binds at the null. An alias
// keeps the error in its layer, since the decoder can hand a field the
// whole value of the anchor its alias names. An error at or under a field
// that a layer writes as an alias thus binds in that layer, where the
// path resolves through the alias. A path that enters an alias the layer
// cannot follow, such as one to an anchor of a reference document, binds
// at the alias, as [SourceError.Nearest] describes. A Node whose document
// did not parse holds no value.
//
// When no layer holds the value, the error binds at the key of the
// mapping that lacks it, as [Node.Bind] binds the path of a missing key.
// It binds in the layer whose mapping lies deepest along the path, and in
// the highest of several such layers.
//
// Each Node in below must be the Node the value decoded from in its own
// file, so a path below the value names the same field in every layer.
// That Node is the root of each document for a value that holds a whole
// file, or the Node [Node.At] returns for the value in each. The bound
// error reports the [SourceError.Source] and the [SourceError.Node] of
// the layer it binds in. Its message and [SourceError.Path] carry the
// path as the Node of the call reads it. [FormatError] prints one excerpt
// per file when the errors of one call bind in several.
//
// The option changes where an error of the self-validation step binds.
// A [Validator] from [WithValidator] and the decode itself read the Node
// of the call alone, so a schema on a file that holds part of a
// configuration still sees only that part. [Node.Decode] fills a new
// value, which no Node decoded into before, so the option has no use
// there. A [Decoder] built with the option falls back to the same Nodes
// for every Node it decodes, which suits many files layered over one
// shared base.
//
// Two limits remain. A value that the environment or a flag set binds at
// whatever a file holds at its path, as [Node.SelfValidate] describes.
// The walk reads the spelling of each map key from the Node of the call,
// so the keys of a map that only a lower layer holds take the text of
// their Go values. An error under a key that the document spells another
// way, such as 1.50, then binds at the key of the map.
//
// Each WithFallback appends to the Nodes given before it. A nil Node adds
// nothing, so a program with an optional base file passes its Node as it
// is.
func WithFallback(below ...*Node) DecodeOption {
	return func(c *decodeConfig) {
		for _, n := range below {
			if n != nil {
				c.fallbacks = append(c.fallbacks, n)
			}
		}
	}
}

// fallback holds what a binder needs to bind an error in a layer below
// its Node, as [WithFallback] describes: the type of the value the
// layers filled, and the Nodes below the Node of the binder, nearest
// first.
type fallback struct {
	typ   reflect.Type
	below []*Node
}

// newFallback returns the [fallback] for v, a value that is not nil, and
// the Nodes below, or nil when below is empty. A binder with a nil
// fallback binds every path in its own Node.
func newFallback(v any, below []*Node) *fallback {
	if len(below) == 0 {
		return nil
	}

	return &fallback{typ: reflect.TypeOf(v), below: below}
}

// layer returns the Node an error at path binds in, and path as the
// document of that Node reads it. The path starts at `$` and reads from
// the root of the document of top, the Node of the binder. A nil f, and
// a path that does not lie under top, bind in top with path as it is.
//
// The layers are top and then the Nodes below it. The result is the
// highest one that [Node.sets] reports for the fields [mergedFields]
// finds along path. When no layer sets the value, each layer answers
// with the mapping that lacks a key of path, as [paths.Resolver.Nearest]
// finds it. The result is then the layer whose mapping lies deepest
// below its Node, and the highest of several such layers. It is top when
// no layer holds such a mapping.
func (f *fallback) layer(top *Node, path paths.Path) (*Node, paths.Path) {
	if f == nil {
		return top, path
	}

	rel, ok := path.CutPrefix(top.base)
	if !ok {
		return top, path
	}

	layers := make([]*Node, 0, len(f.below)+1)
	layers = append(layers, top)
	layers = append(layers, f.below...)

	fields := mergedFields(f.typ, rel)

	for _, layer := range layers {
		if layer.sets(fields) {
			return layer, layer.base.Join(rel)
		}
	}

	best, depth := top, -1

	for _, layer := range layers {
		near, ok := layer.doc.pathResolver().Nearest(layer.base.Join(rel))
		if !ok {
			continue
		}

		// The depth counts from the Node, so layers at different paths of
		// their documents compare alike.
		if d := near.Len() - layer.base.Len(); d > depth {
			best, depth = layer, d
		}
	}

	return best, best.base.Join(rel)
}

// sets reports whether a decode of n set the value at the end of fields.
// They are the struct fields [mergedFields] returns for a path, and the
// search reads them down from the value of n. The document must hold a
// value for n and for each field. A field it leaves out keeps what an
// earlier decode set. So does a field that holds a null, unless the field
// is a pointer, which the null sets to nil. A document that did not parse
// holds no value.
//
// Two kinds of field end the search with n as the answer, since the
// document cannot say what a decode left there. One is a field the
// document writes as an alias. The go-yaml decoder hands such a field
// the value of the anchor whole when an earlier field decoded that
// anchor, and merges the content of the anchor otherwise. The other is a
// field the document cannot read, such as one a `<<` merge key may bring
// in through an alias with no anchor before it. A value of n that the
// document cannot read ends the search the same way.
func (n *Node) sets(fields []mergedField) bool {
	resolver := n.doc.pathResolver()

	node, err := resolver.Node(n.base)
	if err != nil {
		return !errors.Is(err, paths.ErrNotFound)
	}

	if isNull(node) {
		return false
	}

	for _, field := range fields {
		found, err := resolver.Entry(node, field.name)
		if err != nil {
			return !errors.Is(err, paths.ErrNotFound)
		}

		entry, ok := found.(*ast.MappingValueNode)
		if !ok {
			return true
		}

		if _, alias := astnode.Content(entry.Value).(*ast.AliasNode); alias {
			return true
		}

		node, err = resolver.Deref(entry.Value)
		if err != nil {
			return true
		}

		if isNull(node) {
			return field.pointer
		}
	}

	return true
}

// isNull reports whether node holds no value: a nil node, or a null with
// or without anchors and tags on it.
func isNull(node ast.Node) bool {
	content := astnode.Content(node)

	return content == nil || content.Type() == ast.NullType
}

// mergedField is a struct field along a path that successive decodes
// into one value merge.
type mergedField struct {
	// The name the go-yaml decoder reads the field under.
	name string
	// Whether the type of the field is a pointer.
	pointer bool
}

// mergedFields returns the struct fields along rel, an `@` path from a
// value of type t, that successive decodes into one value merge. Each is
// a field of the struct the one before it holds, or of t for the first,
// which the go-yaml decoder fills key by key and leaves as it is when the
// document lacks its key. The fields end at the first selector that
// names no such field. That selector reads a slice, an array, a map, or
// a value of an interface type, which a decode replaces whole, or a value
// of a type that decodes itself, or it is no `.name` selector.
func mergedFields(t reflect.Type, rel paths.Path) []mergedField {
	var fields []mergedField

	for sel := range rel.Selectors() {
		if sel.Kind != paths.SelectorChild {
			break
		}

		field, ok := fieldType(t, sel.Name, map[reflect.Type]bool{})
		if !ok {
			break
		}

		fields = append(fields, mergedField{name: sel.Name, pointer: field.Kind() == reflect.Pointer})
		t = field
	}

	return fields
}

// fieldType returns the type of the field that the go-yaml decoder
// decodes the key name into for a value of type t, and reports whether t
// has one. The type t is a struct that the decoder fills field by field,
// or a pointer to one, as [fieldwiseStruct] finds it. A field of the
// struct itself wins over a field of a struct it inlines, as
// [yamlfield.OwnNames] describes, and the inline structs answer in the
// order the struct declares them. A field that [yamlfield.ReadsAnchor]
// reports reads no key. The seen set holds the structs the search has
// read, so a struct that inlines a pointer to itself reads once.
func fieldType(t reflect.Type, name string, seen map[reflect.Type]bool) (reflect.Type, bool) {
	t, ok := fieldwiseStruct(t)
	if !ok || seen[t] {
		return nil, false
	}

	seen[t] = true

	var inline []reflect.Type

	for field := range t.Fields() {
		fieldName, inlined, skip := yamlfield.Name(field)

		switch {
		case skip, yamlfield.ReadsAnchor(field):
		case inlined:
			inline = append(inline, field.Type)
		case fieldName == name:
			return field.Type, true
		}
	}

	for _, inner := range inline {
		if found, ok := fieldType(inner, name, seen); ok {
			return found, true
		}
	}

	return nil, false
}

// fieldwiseStruct returns the struct type t is or points to, and reports
// whether the go-yaml decoder fills a value of it field by field. It
// reports false for a type that is no struct, and for a struct that
// decodes itself, as [decodesItself] reports. It reads through
// [maxPointerDepth] pointers at most, so a pointer type that refers back
// to itself ends the search.
func fieldwiseStruct(t reflect.Type) (reflect.Type, bool) {
	for range maxPointerDepth {
		if t.Kind() != reflect.Pointer {
			break
		}

		t = t.Elem()
	}

	return t, t.Kind() == reflect.Struct && !decodesItself(t)
}
