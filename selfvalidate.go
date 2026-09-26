package niceyaml

import (
	"encoding"
	"errors"
	"fmt"
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
// under it would point nowhere. Several errors come back joined, one per
// value that failed. Returns nil when nothing failed.
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
// decoded from, with the options it decoded with.
type selfWalker struct {
	node    *Node
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

// decodesItself reports whether go-yaml decodes a value of type t through
// an unmarshaler method of its own, so the fields, elements, or entries
// of the value need not mirror the document. The method set of the
// pointer holds the methods of both receivers, as the decoder checks it.
func decodesItself(t reflect.Type) bool {
	pt := reflect.PointerTo(t)

	return slices.ContainsFunc(unmarshalerTypes, pt.Implements)
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
		// The bytes of a []byte hold nothing to validate.
		if v.Type().Elem().Kind() == reflect.Uint8 {
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
		// text, such as 1 and "1", order by their types.
		names := w.keyNames(base, v.Type().Key())
		keys := v.MapKeys()
		slices.SortStableFunc(keys, func(a, b reflect.Value) int {
			if c := strings.Compare(mapKey(a, names), mapKey(b, names)); c != 0 {
				return c
			}

			return strings.Compare(a.Type().String(), b.Type().String())
		})

		for _, key := range keys {
			if !w.walk(v.MapIndex(key), base.Child(mapKey(key, names))) {
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
// as 0x10 or 1.50 keeps the text a path resolves. A key a `<<` merge key
// brings in counts, and a key of the mapping itself wins over it, as it
// does in the decode. The map holds no key a path cannot resolve to, and
// is empty when no mapping is at base, as for a value the document did
// not set.
func (w *selfWalker) keyNames(base paths.Path, t reflect.Type) map[any]string {
	names := map[any]string{}

	w.collectKeyNames(base, t, names, map[*ast.MappingNode]bool{})

	return names
}

// collectKeyNames adds the keys of the mapping at the path at, and of
// the mappings it merges, to names, as [selfWalker.keyNames] describes.
// The seen set guards against merge cycles.
func (w *selfWalker) collectKeyNames(
	at paths.Path, t reflect.Type, names map[any]string, seen map[*ast.MappingNode]bool,
) {
	node, err := w.node.base.Join(at).Node(w.node.doc.root)
	if err != nil {
		return
	}

	mapping, ok := unwrapNode(node).(*ast.MappingNode)
	if !ok || seen[mapping] {
		return
	}

	seen[mapping] = true

	// A later merge source wins over an earlier one, and the mapping's
	// own keys win over both, so the sources go in first, in order.
	merge := at.Child("<<")
	for _, entry := range mapping.Values {
		if entry == nil || entry.Key == nil || !entry.Key.IsMergeKey() {
			continue
		}

		if seq, ok := unwrapNode(entry.Value).(*ast.SequenceNode); ok {
			for i := range seq.Values {
				w.collectKeyNames(merge.Index(i), t, names, seen)
			}

			continue
		}

		w.collectKeyNames(merge, t, names, seen)
	}

	for _, entry := range mapping.Values {
		if entry == nil || entry.Key == nil || entry.Key.IsMergeKey() {
			continue
		}

		w.addKeyName(entry.Key, t, names)
	}
}

// addKeyName decodes key as type t and adds its text to names under the
// value it decodes to. A key that does not decode, or whose value cannot
// key a map, adds nothing.
func (w *selfWalker) addKeyName(key ast.MapKeyNode, t reflect.Type, names map[any]string) {
	node := keyValueNode(key)

	var name string

	switch n := unwrapNode(node).(type) {
	case *ast.StringNode:
		name = n.Value
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

	err := yaml.NodeToValue(node, decoded.Interface(), w.opts...)
	if err != nil || !decoded.Elem().Comparable() {
		return
	}

	names[decoded.Elem().Interface()] = name
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
// the document spells it, or else the string itself, or the formatted
// value of any other key.
func mapKey(key reflect.Value, names map[any]string) string {
	if key.Comparable() {
		if name, ok := names[key.Interface()]; ok {
			return name
		}
	}

	if key.Kind() == reflect.String {
		return key.String()
	}

	return fmt.Sprint(key.Interface())
}
