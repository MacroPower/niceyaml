package niceyaml

import (
	"bytes"
	"encoding"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"unsafe"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"

	"go.jacobcolvin.com/niceyaml/paths"
)

// selfValidate runs Validate on every value in the tree of v, a non-nil
// pointer to the value n decoded to with opts, that implements
// [SelfValidator], and returns what they report with the paths in each
// error rebased under the path of the value in the document: the field
// name go-yaml decoded it under, the index of a slice or array element,
// or the key of a map entry as the document spells it. The values below
// a value validate before it does, and a value validates only when every
// value below it passed, so a parent that checks a relation between its
// fields sees fields that hold together. A value whose type decodes
// itself, through an unmarshaler method, validates itself and nothing
// below it, since its fields need not mirror the document and the paths
// under it would point nowhere. So does a node of the syntax tree, which
// go-yaml sets whole. Several errors come back joined, one per value
// that failed. Returns nil when nothing failed.
func selfValidate(v any, n *Node, opts []yaml.DecodeOption) error {
	w := selfWalker{node: n, opts: opts, walking: map[visit]bool{}, done: map[visit]bool{}}
	w.walk(reflect.ValueOf(v), paths.Root())

	switch len(w.errs) {
	case 0:
		return nil
	case 1:
		return w.errs[0]
	default:
		return errors.Join(w.errs...)
	}
}

// selfWalker collects the errors of the [SelfValidator] values in a
// decoded value, the pointers, maps, and slices on the path it is
// walking down, so a value that refers back to one above it stops
// there, and the result of each it has walked, so a value two paths
// share, as an alias makes one, walks once and reports its errors under
// the first path, while a parent on the second path still learns that
// the value failed. It reads the keys of a map from the node the value
// decoded from, with the options it decoded with, and finds that node
// through the [paths.Resolver] of the document, so the walk binds the
// aliases of the document, and reads the keys of each mapping on the way
// to a map, once however many maps it meets. One go-yaml decoder decodes
// every key, so the walk applies the options, and reads any reference
// files they name, once too.
type selfWalker struct {
	node    *Node
	decoder *yaml.Decoder
	opts    []yaml.DecodeOption
	walking map[visit]bool
	done    map[visit]bool
	errs    []error
}

// visit names a pointer, map, or slice the walker is inside of, by type
// and address together, since a struct and its first field share an
// address, and by length for a slice, since two slices can start at one
// element. The fields serve as the map key.
//
//nolint:unused // The fields tell the keys of the walking map apart.
type visit struct {
	typ reflect.Type
	ptr unsafe.Pointer
	len int
}

// walk validates v and everything below it, with base as the path of v
// in the document, and reports whether nothing under v failed.
func (w *selfWalker) walk(v reflect.Value, base paths.Path) bool {
	if !v.IsValid() || !v.CanInterface() {
		return true
	}

	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return true
		}

		return w.walk(v.Elem(), base)

	case reflect.Pointer, reflect.Map, reflect.Slice:
		// A nil pointer holds no value to validate. A nil map or slice is
		// an empty value with nothing below it, so it validates here rather
		// than through done, where every nil value of its type would share
		// one record.
		if v.IsNil() {
			if v.Kind() == reflect.Pointer {
				return true
			}

			return w.validate(v, base)
		}

		if !ownsAddress(v) {
			if v.Kind() == reflect.Pointer {
				return w.walk(v.Elem(), base)
			}

			return w.walkValue(v, base)
		}

		if ok, seen := w.done[visitOf(v)]; seen {
			return ok
		}

		if !w.enter(v) {
			return true
		}

		defer w.leave(v)

		if v.Kind() == reflect.Pointer {
			return w.finish(v, w.walk(v.Elem(), base))
		}

		return w.finish(v, w.walkValue(v, base))

	default:
	}

	return w.walkValue(v, base)
}

// walkValue validates v, a value that is no pointer or interface, and
// everything below it, and reports whether nothing under v failed.
func (w *selfWalker) walkValue(v reflect.Value, base paths.Path) bool {
	if !decodesItself(v.Type()) && !w.children(v, base) {
		return false
	}

	return w.validate(v, base)
}

// finish records the result of the walk through v and returns it.
func (w *selfWalker) finish(v reflect.Value, ok bool) bool {
	w.done[visitOf(v)] = ok

	return ok
}

// enter records that the walk is inside v, a pointer, map, or slice, and
// reports false when it already was, so the walk stops there.
func (w *selfWalker) enter(v reflect.Value) bool {
	key := visitOf(v)
	if w.walking[key] {
		return false
	}

	w.walking[key] = true

	return true
}

// leave records that the walk is inside v no longer.
func (w *selfWalker) leave(v reflect.Value) {
	delete(w.walking, visitOf(v))
}

// ownsAddress reports whether the address of v, a non-nil pointer, map,
// or slice, names v alone. Every zero-size allocation can share one
// address, so a pointer to a zero-size value, an empty slice, or a slice
// of zero-size elements can share its address with an unrelated value.
// Such a value holds nothing that can refer back to it, so the walk needs
// no record of it.
func ownsAddress(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Pointer:
		return v.Type().Elem().Size() != 0
	case reflect.Slice:
		return v.Len() != 0 && v.Type().Elem().Size() != 0
	default:
		return true
	}
}

// visitOf returns the [visit] naming v, a pointer, map, or slice.
func visitOf(v reflect.Value) visit {
	key := visit{typ: v.Type(), ptr: v.UnsafePointer()}
	if v.Kind() == reflect.Slice {
		key.len = v.Len()
	}

	return key
}

// unmarshalerTypes are the interfaces go-yaml decodes a value through
// when its pointer implements one, in place of decoding field by field.
var unmarshalerTypes = []reflect.Type{
	reflect.TypeFor[yaml.BytesUnmarshaler](),
	reflect.TypeFor[yaml.BytesUnmarshalerContext](),
	reflect.TypeFor[yaml.InterfaceUnmarshaler](),
	reflect.TypeFor[yaml.InterfaceUnmarshalerContext](),
	reflect.TypeFor[yaml.NodeUnmarshaler](),
	reflect.TypeFor[yaml.NodeUnmarshalerContext](),
	reflect.TypeFor[encoding.TextUnmarshaler](),
}

// decodesItself reports whether go-yaml decodes a value of type t whole,
// so the fields, elements, or entries of the value need not mirror the
// document: through an unmarshaler method of its own, or as an
// [ast.Node], which the decoder sets to the node it decodes rather than
// decoding field by field. The tokens of a node also link to every other
// token of the file. The method set of the pointer holds the methods of
// both receivers, as the decoder checks it.
func decodesItself(t reflect.Type) bool {
	pt := reflect.PointerTo(t)

	return pt.Implements(reflect.TypeFor[ast.Node]()) || slices.ContainsFunc(unmarshalerTypes, pt.Implements)
}

// children walks the values below v, and reports whether every one of
// them passed.
func (w *selfWalker) children(v reflect.Value, base paths.Path) bool {
	ok := true

	switch v.Kind() {
	case reflect.Struct:
		for i := range v.NumField() {
			field := v.Type().Field(i)

			name, inline, skip := fieldName(field)
			if skip {
				continue
			}

			child := base
			if !inline {
				child = base.Child(name)
			}

			if !w.walk(v.Field(i), child) {
				ok = false
			}
		}

	case reflect.Slice, reflect.Array:
		// The bytes of a []byte hold nothing to validate, unless their
		// type validates itself through its value or its pointer.
		if elem := v.Type().Elem(); elem.Kind() == reflect.Uint8 &&
			!reflect.PointerTo(elem).Implements(reflect.TypeFor[SelfValidator]()) {
			return true
		}

		for i := range v.Len() {
			if !w.walk(v.Index(i), base.Index(i)) {
				ok = false
			}
		}

	case reflect.Map:
		// The entries walk in the order of their keys, so the errors come
		// back in one order however the map iterates. Two keys of one
		// text, such as 1 and "1", order by the types they hold. Each
		// value comes from the iteration rather than a lookup by its key,
		// since a NaN key equals no key, itself included.
		names := w.keyNames(base, v.Type().Key())

		type entry struct {
			key, value reflect.Value
		}

		entries := make([]entry, 0, v.Len())

		for iter := v.MapRange(); iter.Next(); {
			entries = append(entries, entry{key: iter.Key(), value: iter.Value()})
		}

		slices.SortStableFunc(entries, func(a, b entry) int {
			if c := strings.Compare(mapKey(a.key, names), mapKey(b.key, names)); c != 0 {
				return c
			}

			return strings.Compare(keyTypeName(a.key), keyTypeName(b.key))
		})

		for _, e := range entries {
			if !w.walk(e.value, base.Child(mapKey(e.key, names))) {
				ok = false
			}
		}

	default:
	}

	return ok
}

// validate runs Validate on v when v implements [SelfValidator] through
// its value or its pointer, with the result rebased under base, and
// reports whether v passed. A value the walk cannot take the address of,
// such as one held by a map, validates through a copy, so a Validate
// with a pointer receiver runs on it too.
func (w *selfWalker) validate(v reflect.Value, base paths.Path) bool {
	if !v.CanAddr() {
		addressable := reflect.New(v.Type()).Elem()

		addressable.Set(v)

		v = addressable
	}

	validator, ok := reflect.TypeAssert[SelfValidator](v.Addr())
	if !ok {
		return true
	}

	err := validator.Validate()
	if isNothing(err) {
		return true
	}

	if !base.IsRoot() {
		err = Rebase(err, base)
	}

	w.errs = append(w.errs, err)

	return false
}

// fieldName returns the name go-yaml decodes field under, whether the
// field is inlined so its own fields sit beside its siblings, and
// whether go-yaml skips the field: an unexported field that is not
// embedded, or one whose tag is "-". The name comes from the yaml tag,
// or the json tag when the field has no yaml tag, and is the lowercased
// field name when neither names it, as go-yaml spells it.
func fieldName(field reflect.StructField) (string, bool, bool) {
	if field.PkgPath != "" && !field.Anonymous {
		return "", false, true
	}

	tag := field.Tag.Get("yaml")
	if tag == "" {
		tag = field.Tag.Get("json")
	}

	if tag == "-" {
		return "", false, true
	}

	name := strings.ToLower(field.Name)

	options := strings.Split(tag, ",")
	if options[0] != "" {
		name = options[0]
	}

	return name, slices.Contains(options[1:], "inline"), false
}

// keyNames returns the text the document spells each key of the mapping
// at base with, by the value the key decodes to as type t, so a key such
// as 0x10 or 1.50 keeps the text a path resolves. A key any `<<` merge
// key brings in counts, whether the merge names one mapping or a list of
// them, directly or through an alias. A later merge source wins over an
// earlier one, and a key of the mapping itself wins over both, as in the
// decode. The map holds no key a path cannot resolve to, and is empty
// when no mapping is at base, as for a value the document did not set.
func (w *selfWalker) keyNames(base paths.Path, t reflect.Type) map[any]string {
	names := map[any]string{}

	node, err := w.pathResolver().Node(w.node.base.Join(base))
	if err == nil {
		w.collectKeyNames(node, t, names, map[*ast.MappingNode]bool{})
	}

	return names
}

// collectKeyNames adds the keys of the mapping node, and of the mappings
// it merges, to names, as [selfWalker.keyNames] describes. The seen set
// guards against merge cycles.
func (w *selfWalker) collectKeyNames(
	node ast.Node, t reflect.Type, names map[any]string, seen map[*ast.MappingNode]bool,
) {
	mapping, ok := unwrapNode(node).(*ast.MappingNode)
	if !ok || seen[mapping] {
		return
	}

	seen[mapping] = true

	// A later merge source wins over an earlier one, and the mapping's
	// own keys win over both, so the sources go in first, in order. An
	// alias in any merge that does not resolve leaves out the keys of
	// every merge.
	sources, err := w.pathResolver().MergeSources(mapping)
	if err == nil {
		for _, src := range sources {
			w.collectKeyNames(src, t, names, seen)
		}
	}

	for _, entry := range mapping.Values {
		if entry == nil || entry.Key == nil || entry.Key.IsMergeKey() {
			continue
		}

		w.addKeyName(entry.Key, t, names)
	}
}

// pathResolver returns the [paths.Resolver] for the document of the
// walk.
func (w *selfWalker) pathResolver() *paths.Resolver {
	return w.node.doc.pathResolver()
}

// keyDecoder returns the go-yaml decoder for the keys of the walk, and
// creates it when the walk first decodes a key.
func (w *selfWalker) keyDecoder() *yaml.Decoder {
	if w.decoder == nil {
		w.decoder = yaml.NewDecoder(bytes.NewReader(nil), w.opts...)
	}

	return w.decoder
}

// addKeyName decodes key as type t and adds its text to names under the
// value it decodes to, as [nameKey] keys it. The text of a block scalar
// key is its content rather than its `|` or `>` indicator. A key that
// does not decode, or whose value cannot key a map, adds nothing, and
// neither does an alias key, which decodes only beside the anchor it
// names.
func (w *selfWalker) addKeyName(key ast.MapKeyNode, t reflect.Type, names map[any]string) {
	node := keyValueNode(key)

	var name string

	switch n := unwrapNode(node).(type) {
	case *ast.StringNode:
		name = n.Value
	case *ast.LiteralNode:
		if n.Value == nil {
			return
		}

		name = n.Value.Value

	case *ast.AliasNode:
		return

	case ast.ScalarNode:
		tk := n.GetToken()
		if tk == nil {
			return
		}

		name = tk.Value

	default:
		return
	}

	decoded := reflect.New(t)

	err := w.keyDecoder().DecodeFromNode(node, decoded.Interface())
	if err != nil {
		return
	}

	if k, ok := nameKey(decoded.Elem()); ok {
		names[k] = name
	}
}

// nanKey stands in names for a NaN key, since a NaN equals no value,
// itself included, and so finds no entry under its own value.
type nanKey struct{}

// nameKey returns the value names holds the text of key under: [nanKey]
// for a float NaN, alone or behind an interface, or else the value of
// key itself. The bool result is false for a key that cannot key a map.
func nameKey(key reflect.Value) (any, bool) {
	if !key.Comparable() {
		return nil, false
	}

	v := key
	if v.Kind() == reflect.Interface && !v.IsNil() {
		v = v.Elem()
	}

	if v.CanFloat() && math.IsNaN(v.Float()) {
		return nanKey{}, true
	}

	return key.Interface(), true
}

// keyValueNode looks through the `?` of an explicit key and the anchors
// on key, which carry no part of its value, to the node the key decodes
// from. A tag stays, since it decides how the key decodes.
func keyValueNode(key ast.MapKeyNode) ast.Node {
	var node ast.Node = key

	for {
		switch n := node.(type) {
		case *ast.MappingKeyNode:
			node = n.Value
		case *ast.AnchorNode:
			node = n.Value
		default:
			return node
		}
	}
}

// unwrapNode looks through the anchors and tags on node to the node that
// carries its content.
func unwrapNode(node ast.Node) ast.Node {
	for {
		switch n := node.(type) {
		case *ast.AnchorNode:
			node = n.Value
		case *ast.TagNode:
			node = n.Value
		default:
			return node
		}
	}
}

// mapKey returns the path segment for a map key: its text in names, as
// the document spells it, under the value [nameKey] gives, or else the
// string itself, or the formatted value of any other key.
func mapKey(key reflect.Value, names map[any]string) string {
	if k, ok := nameKey(key); ok {
		if name, ok := names[k]; ok {
			return name
		}
	}

	if key.Kind() == reflect.String {
		return key.String()
	}

	return fmt.Sprint(key.Interface())
}

// keyTypeName returns the name of the type a map key holds: the dynamic
// type of a key an interface holds, or "" for a nil one.
func keyTypeName(key reflect.Value) string {
	if key.Kind() == reflect.Interface {
		if key.IsNil() {
			return ""
		}

		key = key.Elem()
	}

	return key.Type().String()
}
