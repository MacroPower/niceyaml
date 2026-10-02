package niceyaml

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"reflect"
	"slices"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/yamlfield"
	"go.jacobcolvin.com/niceyaml/paths"
)

// unknownFields returns the rejection of every unknown field that a
// decode of node into v with yamlOpts reports, in the order of their
// keys in the source, when err, the error that decode returned, is the
// rejection of one. The go-yaml decoder stops at the first unknown field
// it finds, and it finds the fields of one mapping in no fixed order, so
// one decode names one field, and not always the same one.
//
// The decoder decides whether a decode fails. UnknownFields runs only
// after the decoder rejected an unknown field, and its result always
// holds that rejection, so no decode passes or fails because of it. For
// any other err it returns nil, and so it does for an unknown field the
// decoder reported with no token of the source.
//
// An [unknownFieldFinder] finds the other fields. Each rejection it
// returns is one the decoder itself returned for that field.
func (n *Node) unknownFields(
	ctx context.Context,
	err error,
	node ast.Node,
	v any,
	yamlOpts []yaml.DecodeOption,
) []*yaml.UnknownFieldError {
	reported, ok := err.(*yaml.UnknownFieldError) //nolint:errorlint // A wrapped error is the unmarshaler's own.
	if !ok || !n.holdsToken(reported.Token) || reported.Token.Position == nil {
		return nil
	}

	f := &unknownFieldFinder{
		ctx:      ctx,
		node:     n,
		resolver: n.doc.pathResolver(),
		decoder:  yaml.NewDecoder(bytes.NewReader(nil), yamlOpts...),
		found:    map[int]*yaml.UnknownFieldError{reported.Token.Position.Offset: reported},
		visited:  map[structVisit]bool{},
	}

	f.walk(reflect.TypeOf(v).Elem(), node, nil)

	fields := make([]*yaml.UnknownFieldError, 0, len(f.found))
	for _, field := range f.found {
		fields = append(fields, field)
	}

	slices.SortFunc(fields, func(a, b *yaml.UnknownFieldError) int {
		return cmp.Compare(a.Token.Position.Offset, b.Token.Position.Offset)
	})

	return fields
}

// bindUnknownFields binds the rejections of several unknown fields to the
// source as one error. Its message counts the fields, and it nests one
// [*Error] for each, which matches [ErrDecodeRejected] and carries the
// path of the key of the field, as [Node.bindDecodeError] binds the
// rejection of one field. The paths read from the root of the document,
// so the Node binds the error with no scope in front of them.
func (n *Node) bindUnknownFields(fields []*yaml.UnknownFieldError) error {
	tree := n.doc.decodeTree()
	nested := make([]error, 0, len(fields))

	for _, field := range fields {
		rejected := decodeRejectedError{yamlMessageError{err: field, msg: tree.restoreNames(rejectionMessage(field))}}
		nested = append(nested, WrapError(rejected, n.rejectionLocation(field.Token)...))
	}

	summary := NewError(fmt.Sprintf("%d unknown fields", len(fields)), WithErrors(nested...))

	return bindTree(summary, binder{src: n.source, node: n.bindTarget(), rooted: true})
}

// unknownFieldFinder finds the unknown fields of a decode that the
// go-yaml decoder rejected for one of them. It walks the type the decode
// filled beside the node the decode read, as an [errorLocator] does, and
// reads each mapping that a struct decodes from.
//
// A key of such a mapping is a candidate when no field of the struct has
// its name, as [fieldNames] lists them. The walk follows the rules
// [yamlfield] holds for those names. It cannot see every rule the decode
// applies, such as the prefixes [yaml.AllowFieldPrefixes] allows or the
// types [yaml.CustomUnmarshaler] decodes. So the decoder confirms each
// candidate. The finder decodes the one entry of the candidate into a
// new value of the struct type, with the options of the decode, and
// keeps the candidate only when that decode rejects its key as an
// unknown field. A key the decoder accepts thus never joins the result.
//
// The fields of a struct that decodes itself, as [reportsOwnError] lists
// those types, need not mirror the document, and only its unmarshaler
// says whether the decoder checks them. An UnmarshalYAML that decodes
// into a second type with the same fields has the decoder check them,
// and one that parses the text itself does not. The walk therefore reads
// such a struct, and the values below it, as if the fields mirrored the
// document, and takes every key of its own mapping for a candidate. The
// decode that confirms a candidate at or below such a struct runs the
// unmarshaler of the outermost one, on the entries that lead from its
// mapping down to the candidate, as a [decodeChain] holds them. So the
// unmarshaler decides, as it does in the decode itself.
//
// The walk reads nothing below an interface, since the decoder fills it
// with values of no struct type.
//
// One go-yaml decoder runs every decode of the walk, so the walk applies
// the options once.
type unknownFieldFinder struct {
	ctx      context.Context
	node     *Node
	resolver *paths.Resolver
	decoder  *yaml.Decoder
	// The rejections found, by the offset of the token of each key. An
	// alias or a merge key can lead the walk to one key twice.
	found map[int]*yaml.UnknownFieldError
	// The structs the walk has read, so it reads a mapping that several
	// aliases reach, or one that holds itself, once for each type.
	visited map[structVisit]bool
}

// structVisit names a struct the walk has read, by its type and the
// mapping it decodes from. The fields together form the key of the set
// the [unknownFieldFinder] keeps.
//
//nolint:unused // The fields tell the keys of the set apart.
type structVisit struct {
	typ     reflect.Type
	mapping *ast.MappingNode
}

// decodeChain is the way from a struct that decodes itself down to a
// value the walk reads below it, one link for each entry or element on
// the way, the innermost first. The last link holds the type of that
// struct and no step. A nil chain stands for a value under no such
// struct.
type decodeChain struct {
	parent *decodeChain
	// The type of the struct that decodes itself, on the last link.
	owner reflect.Type
	// The entry the way goes through, or nil for an element.
	entry *ast.MappingValueNode
	// The sequence that holds the element the way goes through, or nil
	// for an entry.
	sequence *ast.SequenceNode
}

// through returns the chain with a link for entry in front, or nil for a
// nil chain.
func (c *decodeChain) through(entry *ast.MappingValueNode) *decodeChain {
	if c == nil {
		return nil
	}

	return &decodeChain{parent: c, entry: entry}
}

// element returns the chain with a link for an element of sequence in
// front, or nil for a nil chain.
func (c *decodeChain) element(sequence *ast.SequenceNode) *decodeChain {
	if c == nil {
		return nil
	}

	return &decodeChain{parent: c, sequence: sequence}
}

// walk reads node as a value of type t, at the end of chain, and finds
// the unknown fields of each struct at or below it.
func (f *unknownFieldFinder) walk(t reflect.Type, node ast.Node, chain *decodeChain) {
	if f.ctx.Err() != nil {
		return
	}

	t = pointerBase(t)

	switch content := contentNode(f.resolver, node).(type) {
	case *ast.MappingNode:
		switch {
		case t.Kind() == reflect.Struct:
			f.fields(t, content, chain)

		case t.Kind() == reflect.Map && !reportsOwnError(t):
			f.eachEntry(content, true, map[*ast.MappingNode]bool{}, func(entry *ast.MappingValueNode) {
				f.value(t.Elem(), entry.Value, chain.through(entry))
			})
		}

	case *ast.SequenceNode:
		if reportsOwnError(t) || (t.Kind() != reflect.Slice && t.Kind() != reflect.Array) {
			return
		}

		for _, element := range content.Values {
			f.value(t.Elem(), element, chain.element(content))
		}
	}
}

// value walks the node the decoder reads a field, an element, or a map
// value of type t from, where node is the value the document holds for
// it. The decoder reads nothing from a null, as [readNode] describes.
func (f *unknownFieldFinder) value(t reflect.Type, node ast.Node, chain *decodeChain) {
	if value, ok := readNode(f.resolver, node); ok {
		f.walk(t, value, chain)
	}
}

// fields finds the unknown fields of t, a struct type, in mapping, and
// walks the values of the fields t does have.
//
// The decoder reads the keys of a struct as strings, and decodes no field
// from a mapping that holds a key of another kind, such as an integer.
// It rejects no key of such a mapping either, so the walk stops there.
//
// A key the walk tests is the entry the decoder reads for its name, as
// [unknownFieldFinder.entry] finds it, so the walk passes over an entry
// that a later one with the same key hides. A `<<` merge key brings in
// the keys of its sources, unless t leaves them out, as
// [yamlfield.IgnoresMerges] reports.
//
// A struct that decodes itself has no field names the walk can trust, so
// each of its keys is a candidate. Where no chain leads to it, the
// struct starts one.
func (f *unknownFieldFinder) fields(t reflect.Type, mapping *ast.MappingNode, chain *decodeChain) {
	visit := structVisit{typ: t, mapping: mapping}
	if f.visited[visit] {
		return
	}

	f.visited[visit] = true

	if !f.readsAsStruct(mapping) {
		return
	}

	merges := !yamlfield.IgnoresMerges(t)

	var names map[string]bool

	if reportsOwnError(t) {
		if chain == nil {
			chain = &decodeChain{owner: t}
		}
	} else {
		names = fieldNames(t, map[reflect.Type]bool{})
	}

	f.eachEntry(mapping, merges, map[*ast.MappingNode]bool{}, func(entry *ast.MappingValueNode) {
		name, ok := f.resolver.KeyName(entry.Key)
		if !ok || names[name] {
			return
		}

		if f.entry(mapping, name, merges) == entry {
			f.confirm(t, entry, chain)
		}
	})

	f.below(t, mapping, merges, map[reflect.Type]bool{}, chain)
}

// below walks the value of each field of t, a struct type that decodes
// from mapping. A field reads the entry of its name, and an inline field
// reads mapping itself, so the walk reads the fields of its struct from
// the same mapping. With merges set, a field reads an entry a `<<` merge
// key brings in too. The inlined set holds the inline structs the walk
// is inside of, so a struct that holds itself inline ends the walk.
func (f *unknownFieldFinder) below(
	t reflect.Type, mapping *ast.MappingNode, merges bool, inlined map[reflect.Type]bool, chain *decodeChain,
) {
	for field := range t.Fields() {
		name, inline, skip := yamlfield.Name(field)
		if skip || yamlfield.ReadsAnchor(field) {
			continue
		}

		if inline {
			inner := pointerBase(field.Type)
			if inner.Kind() != reflect.Struct || reportsOwnError(inner) || inlined[inner] {
				continue
			}

			inlined[inner] = true
			f.below(inner, mapping, merges, inlined, chain)
			delete(inlined, inner)

			continue
		}

		if entry := f.entry(mapping, name, merges); entry != nil {
			f.value(field.Type, entry.Value, chain.through(entry))
		}
	}
}

// entry returns the entry of mapping that the decoder reads for the key
// name when it decodes a struct, or nil when mapping holds none. With
// merges set, that is the entry a path through name selects, which a
// `<<` merge key may bring in. Without, it is the last entry of mapping
// itself with that name.
func (f *unknownFieldFinder) entry(mapping *ast.MappingNode, name string, merges bool) *ast.MappingValueNode {
	if merges {
		selected, err := f.resolver.Entry(mapping, name)
		if err != nil {
			return nil
		}

		if entry, ok := selected.(*ast.MappingValueNode); ok {
			return entry
		}

		return nil
	}

	for _, entry := range slices.Backward(mapping.Values) {
		if entry == nil || entry.Key == nil || entry.Key.IsMergeKey() {
			continue
		}

		if own, ok := f.resolver.KeyName(entry.Key); ok && own == name {
			return entry
		}
	}

	return nil
}

// eachEntry calls visit for each entry of mapping, in document order. It
// passes over an entry with no key and over the `<<` merge keys
// themselves. With merges set, it visits the entries of the mappings
// those merge keys bring in as well, each where its merge key stands.
// The seen set holds the mappings the walk has read, so a mapping that
// merges itself ends the walk.
func (f *unknownFieldFinder) eachEntry(
	mapping *ast.MappingNode, merges bool, seen map[*ast.MappingNode]bool, visit func(*ast.MappingValueNode),
) {
	if seen[mapping] {
		return
	}

	seen[mapping] = true

	for _, entry := range mapping.Values {
		if entry == nil || entry.Key == nil {
			continue
		}

		if !entry.Key.IsMergeKey() {
			visit(entry)

			continue
		}

		if !merges {
			continue
		}

		sources, err := f.resolver.MergeSources(&ast.MappingNode{
			Values: []*ast.MappingValueNode{entry},
		})
		if err != nil {
			continue
		}

		for _, src := range sources {
			if merged, ok := contentNode(f.resolver, src).(*ast.MappingNode); ok {
				f.eachEntry(merged, merges, seen, visit)
			}
		}
	}
}

// readsAsStruct reports whether the decoder reads every key of mapping
// as a string, as [readsAsString] tells, so it decodes a struct from the
// mapping.
func (f *unknownFieldFinder) readsAsStruct(mapping *ast.MappingNode) bool {
	for _, entry := range mapping.Values {
		if entry == nil || entry.Key == nil || entry.Key.IsMergeKey() {
			continue
		}

		if !readsAsString(f.resolver, entry.Key) {
			return false
		}
	}

	return true
}

// readsAsString reports whether the go-yaml decoder reads key, the key
// of a mapping entry, as a string. It reads a plain, quoted, or block
// scalar as one, and anything under a !!str tag. It reads a number, a
// boolean, or a null as a value of that kind, with a tag of that kind or
// without one. An alias reads as the content of its anchor, and an alias
// that does not resolve counts as a string, since the decoder rejects
// it before it reads the kind.
func readsAsString(resolver *paths.Resolver, key ast.Node) bool {
	for !astnode.IsNil(key) {
		switch k := key.(type) {
		case *ast.MappingKeyNode:
			key = k.Value

		case *ast.AnchorNode:
			key = k.Value

		case *ast.AliasNode:
			content, err := resolver.Deref(k)
			if err != nil {
				return true
			}

			key = content

		case *ast.TagNode:
			if k.Directive != nil {
				return true
			}

			switch token.ReservedTagKeyword(k.Start.Value) {
			case token.StringTag:
				return true
			case token.IntegerTag, token.FloatTag, token.NullTag, token.BooleanTag,
				token.BinaryTag, token.TimestampTag:
				return false
			default:
				// Any other tag leaves the kind to the value under it.
				key = k.Value
			}

		case *ast.StringNode, *ast.LiteralNode:
			return true

		default:
			return false
		}
	}

	return false
}

// confirm asks the decoder whether the key of entry is an unknown field
// of the struct type t, and adds the rejection it returns to the fields
// the walk found. The decode reads a mapping that holds entry alone into
// a new value of that type, as the [decodeTree] of the document reads the
// entry. The decoder reads no value under a key it rejects, so the value
// of the entry does not decide the result.
//
// Under a struct that decodes itself, the decode reads that struct. The
// mapping of the entry then sits inside one mapping or sequence for each
// link of chain. Each of those holds the one entry or element the way
// goes through. The unmarshaler of the struct thus reads the entry where
// the document holds it.
//
// Any other outcome leaves the fields as they are. That is a decode that
// passes, one that fails another way, and one whose anchors do not
// decode.
func (f *unknownFieldFinder) confirm(t reflect.Type, entry *ast.MappingValueNode, chain *decodeChain) {
	if f.ctx.Err() != nil {
		return
	}

	view := f.view(entry)
	if view == nil || view.Key == nil {
		return
	}

	var node ast.Node = ast.Mapping(view.Start, false, view)

	for ; chain != nil; chain = chain.parent {
		switch {
		case chain.owner != nil:
			t = chain.owner

		case chain.sequence != nil:
			sequence := ast.Sequence(chain.sequence.Start, false)
			sequence.Values = append(sequence.Values, node)
			node = sequence

		default:
			outer := f.view(chain.entry)
			if outer == nil {
				return
			}

			node = ast.Mapping(outer.Start, false, ast.MappingValue(outer.Start, outer.Key, node))
		}
	}

	err := f.node.primeAnchors(f.ctx, f.decoder, node)
	if err != nil {
		return
	}

	err = decodeWithRecover(f.ctx, f.decoder, node, reflect.New(t).Interface())

	rejected, ok := err.(*yaml.UnknownFieldError) //nolint:errorlint // A wrapped error is the unmarshaler's own.
	if !ok || rejected.Token == nil || rejected.Token != view.Key.GetToken() || rejected.Token.Position == nil {
		return
	}

	if _, held := f.found[rejected.Token.Position.Offset]; !held {
		f.found[rejected.Token.Position.Offset] = rejected
	}
}

// view returns entry as the [decodeTree] of the document reads it, or
// nil when the tree holds no entry in its place.
func (f *unknownFieldFinder) view(entry *ast.MappingValueNode) *ast.MappingValueNode {
	view, ok := f.node.doc.decodeTree().view(entry).(*ast.MappingValueNode)
	if !ok {
		return nil
	}

	return view
}

// fieldNames returns the names of the keys that t, a struct type, has a
// field for, as [yamlfield.Name] names them. The fields of an inline
// struct count as fields of t, however deep the inline structs nest. The
// seen set holds the inline structs the walk is inside of, so a struct
// that holds itself inline ends the walk. A field that
// [yamlfield.ReadsAnchor] reports reads no key, so its struct adds no
// name.
func fieldNames(t reflect.Type, seen map[reflect.Type]bool) map[string]bool {
	names := yamlfield.OwnNames(t)

	seen[t] = true

	for field := range t.Fields() {
		_, inline, skip := yamlfield.Name(field)
		if skip || !inline || yamlfield.ReadsAnchor(field) {
			continue
		}

		inner := pointerBase(field.Type)
		if inner.Kind() != reflect.Struct || seen[inner] {
			continue
		}

		for name := range fieldNames(inner, seen) {
			names[name] = true
		}
	}

	delete(seen, t)

	return names
}
