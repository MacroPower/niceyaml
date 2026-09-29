package niceyaml

import (
	"bytes"
	"cmp"
	"context"
	"encoding"
	"errors"
	"fmt"
	"math"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"

	"go.jacobcolvin.com/niceyaml/paths"
)

// selfValidate runs Validate on every value in the tree of v that
// implements [SelfValidator], where v is a non-nil pointer to the value n
// decoded to with ctx and opts. It returns what they report with the paths
// in each error rebased under the path of the value in the document. That
// path is the field name go-yaml decoded it under, the index of a slice or
// array element, or the key of a map entry as the document spells it. A
// map key validates at the path of its entry with a `~` after it, so its
// errors point at the key rather than the value. The values below a value
// validate before it does, and a value validates only when every value
// below it passed, so a parent that checks a relation between its fields
// sees fields that hold together. A value whose type decodes itself,
// through an unmarshaler method, validates itself and nothing below it,
// since its fields need not mirror the document and the paths under it
// would point nowhere. So does a node of the syntax tree, which go-yaml
// sets whole. A struct that decodes itself through a method it gets from
// an embedded field decodes the document into that field, so the field
// validates first, at the path of the struct. Several errors come back
// joined, one per value that failed. Returns nil when nothing failed.
func selfValidate(ctx context.Context, v any, n *Node, opts []yaml.DecodeOption) error {
	w := selfWalker{
		ctx:      ctx,
		node:     n,
		opts:     opts,
		walking:  map[visit]bool{},
		done:     map[visit]bool{},
		scanning: map[visit]bool{},
		scanned:  map[visit]bool{},
	}
	w.walk(reflect.ValueOf(v), paths.Root(), nil)

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
// decoded value. It records the pointers, maps, and slices on the path it
// is walking down, so a value that refers back to one above it stops
// there. It records the result of each it has walked, so a value two paths
// share, as an alias makes one, walks once and reports its errors under
// the first path. A parent on the second path still learns that the value
// failed. It reads the keys of a map from the node the value decoded from,
// with the context and options it decoded with, and finds that node
// through the [paths.Resolver] of the document. The walk therefore binds
// the aliases of the document, and reads the keys of each mapping on the
// way to a map once, however many maps it meets. One go-yaml decoder
// decodes every key, so the walk applies the options, and reads any
// reference files they name, once too.
//
// Before it reads the keys of a map, or walks the elements of a slice or
// array, the walker scans the values below for one that implements
// [SelfValidator], and passes the value at once when none does. The
// values go-yaml decodes into an interface never implement it, so a
// value decoded as any walks no further than the scan.
type selfWalker struct {
	ctx     context.Context
	node    *Node
	decoder *yaml.Decoder
	opts    []yaml.DecodeOption
	walking map[visit]bool
	done    map[visit]bool
	// The pointers, maps, and slices the current scan has reached, so the
	// scan reads each once however many paths lead to it.
	scanning map[visit]bool
	// The result of the scan of each pointer, map, and slice, so each
	// value scans once however many values above it the walk meets. The
	// result for a pointer covers the value it points to, and the result
	// for a map or slice covers only the values below it.
	scanned map[visit]bool
	errs    []error
}

// visit names a pointer, map, or slice the walker is inside of. It names
// the value by type and address together, since a struct and its first
// field share an address, and by length for a slice, since two slices
// can start at one element. The fields together form the map key.
//
//nolint:unused // The fields tell the keys of the walking map apart.
type visit struct {
	typ reflect.Type
	ptr unsafe.Pointer
	len int
}

// walk validates v and everything below it, with base as the path of v
// in the document, and reports whether nothing under v failed. When v is
// an inline struct, or a pointer to one, shadowed holds the names of the
// fields of its parent that are not inline, as [selfWalker.children]
// describes.
func (w *selfWalker) walk(v reflect.Value, base paths.Path, shadowed map[string]bool) bool {
	if !v.IsValid() {
		return true
	}

	v, ok := exposed(v)
	if !ok {
		return true
	}

	// A value whose type holds no validator, itself included, passes
	// without a look at the values below it.
	if !mayHoldValidator(v.Type()) {
		return true
	}

	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return true
		}

		return w.walk(v.Elem(), base, nil)

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
				return w.walk(v.Elem(), base, shadowed)
			}

			return w.walkValue(v, base, shadowed)
		}

		if ok, seen := w.done[visitOf(v)]; seen {
			return ok
		}

		if !w.enter(v) {
			return true
		}

		defer w.leave(v)

		if v.Kind() == reflect.Pointer {
			return w.finish(v, w.walk(v.Elem(), base, shadowed))
		}

		return w.finish(v, w.walkValue(v, base, shadowed))

	default:
	}

	return w.walkValue(v, base, shadowed)
}

// walkValue validates v, a value that is no pointer or interface, and
// everything below it, and reports whether nothing under v failed. The
// shadowed names are those [selfWalker.walk] takes. A struct the walk
// cannot take the address of, such as one held by a map, walks as a
// copy, so [exposed] can read its unexported embedded fields.
func (w *selfWalker) walkValue(v reflect.Value, base paths.Path, shadowed map[string]bool) bool {
	if v.Kind() == reflect.Struct {
		v = addressable(v)
	}

	if !decodesItself(v.Type()) {
		if !w.children(v, base, shadowed) {
			return false
		}
	} else if i, ok := decoderField(v.Type()); ok && !w.walk(v.Field(i), base, nil) {
		return false
	}

	return w.validate(v, base)
}

// exposed returns v, or a view of it that the walk can read when v is,
// or lies below, an unexported embedded field. Go-yaml decodes into such
// a field through an unmarshaler it promotes, and otherwise leaves the
// value set before the decode, so the walk validates it like any other
// field. The view shares the memory of v, so a Validate with a pointer
// receiver runs on v itself. The bool result is false when v is
// read-only and has no address.
func exposed(v reflect.Value) (reflect.Value, bool) {
	if v.CanInterface() {
		return v, true
	}

	if !v.CanAddr() {
		return v, false
	}

	return reflect.NewAt(v.Type(), v.Addr().UnsafePointer()).Elem(), true
}

// addressable returns v when the walk can take its address, or else an
// addressable copy of it.
func addressable(v reflect.Value) reflect.Value {
	if v.CanAddr() {
		return v
	}

	c := reflect.New(v.Type()).Elem()
	c.Set(v)

	return c
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

// leave records that the walk has left v.
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

var (
	// The interfaces go-yaml decodes a value through when its pointer
	// implements one, in place of decoding field by field. Go-yaml checks
	// them in this order and calls the first one the pointer implements.
	unmarshalerTypes = []reflect.Type{
		reflect.TypeFor[yaml.BytesUnmarshalerContext](),
		reflect.TypeFor[yaml.BytesUnmarshaler](),
		reflect.TypeFor[yaml.InterfaceUnmarshalerContext](),
		reflect.TypeFor[yaml.InterfaceUnmarshaler](),
		reflect.TypeFor[yaml.NodeUnmarshaler](),
		reflect.TypeFor[yaml.NodeUnmarshalerContext](),
		reflect.TypeFor[encoding.TextUnmarshaler](),
	}

	// The import path of the go-yaml ast package, which declares the node
	// types go-yaml sets whole.
	astPackage = reflect.TypeFor[ast.StringNode]().PkgPath()

	// The result of [mayHoldValidator] for each type it has read. A type
	// never changes, so every walk shares the results.
	holdsValidator sync.Map

	// The result of [implementsSelfValidator] for each type it has read,
	// shared the same way.
	ownsValidator sync.Map

	// The result of [decoderField] for each type it has read, shared the
	// same way, with -1 for a type that has no such field.
	decoderFields sync.Map

	// The result of [decodesItself] for each type it has read, shared the
	// same way.
	decodesWhole sync.Map
)

// decodesItself reports whether go-yaml decodes a value of type t whole,
// so the fields, elements, or entries of the value need not mirror the
// document. A type decodes itself through an unmarshaler method of its
// own, or as an [ast.Node] the ast package declares, which the decoder
// sets to the node it decodes rather than decoding field by field. The
// tokens of a node also link to every other token of the file. A struct
// that gets the methods of an [ast.Node] from an embedded field decodes
// field by field. The method set of the pointer holds the methods of
// both receivers, as the decoder checks it.
func decodesItself(t reflect.Type) bool {
	if cached, ok := decodesWhole.Load(t); ok {
		if decodes, ok := cached.(bool); ok {
			return decodes
		}
	}

	pt := reflect.PointerTo(t)
	isNode := t.PkgPath() == astPackage && pt.Implements(reflect.TypeFor[ast.Node]())
	decodes := isNode || slices.ContainsFunc(unmarshalerTypes, pt.Implements)
	decodesWhole.Store(t, decodes)

	return decodes
}

// decoderField returns the index of the embedded field of t that decodes
// t, when t is a struct that gets its unmarshaler method from that field.
// Go-yaml checks the unmarshaler interfaces in a fixed order and calls
// the first method the pointer to t has, so an UnmarshalYAML that t gets
// from a field wins over an UnmarshalText that t declares. The method of
// the field decodes the document into the field, so the field stands at
// the path of the struct. The bool result is false when t declares the
// method go-yaml calls, or when no embedded field, or more than one, has
// that method.
func decoderField(t reflect.Type) (int, bool) {
	if cached, ok := decoderFields.Load(t); ok {
		if i, ok := cached.(int); ok {
			return i, i >= 0
		}
	}

	i := findDecoderField(t)
	decoderFields.Store(t, i)

	return i, i >= 0
}

// findDecoderField returns the index [decoderField] returns, or -1 when
// t has no such field.
func findDecoderField(t reflect.Type) int {
	if t.Kind() != reflect.Struct || !hasEmbedded(t) {
		return -1
	}

	k := slices.IndexFunc(unmarshalerTypes, reflect.PointerTo(t).Implements)
	if k < 0 {
		return -1
	}

	unmarshaler := unmarshalerTypes[k]
	if !promotesMethod(t, unmarshaler.Method(0).Name) {
		return -1
	}

	found := -1

	for i := range t.NumField() {
		field := t.Field(i)
		if !field.Anonymous {
			continue
		}

		// The method set of the pointer to t holds the methods of the
		// pointer to a field that is no pointer or interface.
		methods := field.Type
		if methods.Kind() != reflect.Pointer && methods.Kind() != reflect.Interface {
			methods = reflect.PointerTo(methods)
		}

		if !methods.Implements(unmarshaler) {
			continue
		}

		if found >= 0 {
			return -1
		}

		found = i
	}

	return found
}

// mayHoldValidator reports whether a value of type t can implement
// [SelfValidator], or can hold a value the walk reaches below it that
// does. A type holds one through an interface, which can hold a value of
// any type, or through a field, element, map key, map value, or pointee
// whose type may. The walk passes a value whose type may not without a look
// below it.
func mayHoldValidator(t reflect.Type) bool {
	if cached, ok := holdsValidator.Load(t); ok {
		if held, ok := cached.(bool); ok {
			return held
		}
	}

	held := reachesValidator(t, map[reflect.Type]bool{})
	holdsValidator.Store(t, held)

	return held
}

// reachesValidator reports whether t, or a type below it, can hold a
// [SelfValidator], as [mayHoldValidator] describes. The seen set holds
// the types the search has reached, so the search reads a type that holds
// itself once, and a validator below that type shows on the first read.
func reachesValidator(t reflect.Type, seen map[reflect.Type]bool) bool {
	if seen[t] {
		return false
	}

	seen[t] = true

	if t.Kind() == reflect.Interface || implementsSelfValidator(t) {
		return true
	}

	// The walk validates a value that decodes itself and nothing below it
	// but the embedded field that decodes it.
	if decodesItself(t) {
		i, ok := decoderField(t)

		return ok && reachesValidator(t.Field(i).Type, seen)
	}

	switch t.Kind() {
	case reflect.Map:
		return reachesValidator(t.Key(), seen) || reachesValidator(t.Elem(), seen)

	case reflect.Pointer, reflect.Slice, reflect.Array:
		return reachesValidator(t.Elem(), seen)

	case reflect.Struct:
		for field := range t.Fields() {
			if _, _, skip := fieldName(field); !skip && reachesValidator(field.Type, seen) {
				return true
			}
		}

	default:
	}

	return false
}

// children walks the values below v, and reports whether every one of
// them passed.
//
// The fields of an inline struct sit beside the fields of its parent,
// and go-yaml zeroes each one whose name a field of the parent that is
// not inline also uses. The document sets such a field only for the
// parent, so the walk passes over it. When v is an inline struct,
// shadowed holds the names of those fields of the parent.
func (w *selfWalker) children(v reflect.Value, base paths.Path, shadowed map[string]bool) bool {
	switch v.Kind() {
	case reflect.Slice, reflect.Array, reflect.Map:
		// An element, map key, or map value whose type holds no validator
		// passes the walk at once, so none needs a look. The same goes for
		// those whose types may hold one, but that hold none.
		if !entriesMayHoldValidator(v.Type()) || !w.holdsBelow(v) {
			return true
		}

	default:
	}

	ok := true

	switch v.Kind() {
	case reflect.Struct:
		var own map[string]bool

		for i := range v.NumField() {
			// A field whose type holds no validator passes the walk at
			// once, so it needs no name or path.
			field := v.Type().Field(i)
			if !mayHoldValidator(field.Type) {
				continue
			}

			name, inline, skip := fieldName(field)
			if skip || shadowed[name] {
				continue
			}

			child, fieldShadowed := base, map[string]bool(nil)
			if inline {
				if own == nil {
					own = ownFieldNames(v.Type())
				}

				fieldShadowed = own
			} else {
				child = base.Child(name)
			}

			if !w.walk(v.Field(i), child, fieldShadowed) {
				ok = false
			}
		}

	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			if !w.walk(v.Index(i), base.Index(i), nil) {
				ok = false
			}
		}

	case reflect.Map:
		// The entries walk in the order of their keys, so the errors come
		// back in one order however the map iterates. Two keys of one
		// text, such as 1 and "1", share a path and order by the types
		// they hold. Entries that tie on both, such as several NaN keys or
		// several time keys of one instant and zone, walk as one group, as
		// [selfWalker.walkEntries] describes, along with the one case
		// where the order can vary. Each value comes from the iteration
		// rather than a lookup by its key, since a NaN key equals no key,
		// itself included.
		names := w.keyNames(base, v.Type().Key())

		entries := make([]mapEntry, 0, v.Len())
		shared := map[any]int{}

		for iter := v.MapRange(); iter.Next(); {
			key := iter.Key()
			if k, ok := nameKey(key); ok {
				shared[k]++
			}

			entries = append(entries, mapEntry{key: key, value: iter.Value()})
		}

		// The names cannot tell apart several keys that [nameKey] gives
		// one value, so none of them takes the text of a document key.
		// Several NaN keys give one value, and so do several time keys of
		// one instant and zone, or several pointer keys to equal values.
		// Such keys then share one path, as below.
		for k, n := range shared {
			if n > 1 {
				delete(names, k)
			}
		}

		for i := range entries {
			entries[i].seg = mapKey(entries[i].key, names)
			entries[i].typeName = keyTypeName(entries[i].key)
		}

		compareKeys := func(a, b mapEntry) int {
			return cmp.Or(strings.Compare(a.seg, b.seg), strings.Compare(a.typeName, b.typeName))
		}

		slices.SortFunc(entries, compareKeys)

		// A path names a key by its text alone, so keys that share a
		// segment share a path, which resolves to the document entry of
		// one of them at most. Keys of different types can share one,
		// such as 1 and "1" in a map[any]T, and so can the keys above
		// that no name tells apart, such as several NaN keys beside one
		// the document spells NaN. A `<<` merge can bring in such a key
		// beside one the mapping spells, even where the parser rejects
		// duplicate keys. The errors under such a path bind with no
		// position.
		ambiguous := map[string]bool{}

		for i := 1; i < len(entries); i++ {
			if entries[i].seg == entries[i-1].seg {
				ambiguous[entries[i].seg] = true
			}
		}

		for len(entries) > 0 {
			n := 1
			for n < len(entries) && compareKeys(entries[0], entries[n]) == 0 {
				n++
			}

			seg := entries[0].seg
			if !w.walkEntries(base.Child(seg), entries[:n], ambiguous[seg]) {
				ok = false
			}

			entries = entries[n:]
		}

	default:
	}

	return ok
}

// mapEntry holds one entry of a map the walk is inside of, with the path
// segment and the type name its key orders by.
type mapEntry struct {
	key, value    reflect.Value
	seg, typeName string
}

// walkEntries walks entries, the entries of a map that share path, and
// reports whether nothing under them failed. The key of an entry walks
// before its value, at the key of path. Since the entries share a path,
// nothing in the map orders them, so the errors of each entry stay
// together and the groups order by their text. The order then holds
// however the map iterates, and no value needs formatting, which a
// value that refers back to itself would never finish.
//
// The order can still vary when several of the entries hold one
// pointer, map, or slice, which only a value filled before the decode
// can do. That value walks once, so its errors join the group of
// whichever entry walks first, and the map iteration decides which.
//
// When ambiguous is true, path names the entries of several keys, so the
// errors of entries bind with no position, for the reason
// [ErrAmbiguousPath].
func (w *selfWalker) walkEntries(path paths.Path, entries []mapEntry, ambiguous bool) bool {
	ok := true
	start := len(w.errs)
	groups := make([][]error, 0, len(entries))

	for _, e := range entries {
		if !w.walk(e.key, path.Key(), nil) {
			ok = false
		}

		if !w.walk(e.value, path, nil) {
			ok = false
		}

		errs := w.errs[start:]
		if ambiguous {
			for i, err := range errs {
				errs[i] = bindTree(err, binder{src: w.node.source, node: w.node, ambiguous: true})
			}
		}

		groups = append(groups, slices.Clone(errs))
		w.errs = w.errs[:start]
	}

	slices.SortStableFunc(groups, func(a, b []error) int {
		return slices.CompareFunc(a, b, func(x, y error) int {
			return strings.Compare(x.Error(), y.Error())
		})
	})

	for _, group := range groups {
		w.errs = append(w.errs, group...)
	}

	return ok
}

// ownFieldNames returns the names go-yaml decodes the fields of t, a
// struct type, under, leaving out the fields it skips and those that are
// inline.
func ownFieldNames(t reflect.Type) map[string]bool {
	names := map[string]bool{}

	for field := range t.Fields() {
		if name, inline, skip := fieldName(field); !skip && !inline {
			names[name] = true
		}
	}

	return names
}

// entriesMayHoldValidator reports whether an element of a value of type t,
// a slice, array, or map, or a key or value of a map, may hold a
// [SelfValidator], as [mayHoldValidator] reads it.
func entriesMayHoldValidator(t reflect.Type) bool {
	if t.Kind() == reflect.Map && mayHoldValidator(t.Key()) {
		return true
	}

	return mayHoldValidator(t.Elem())
}

// holdsBelow reports whether a value below v, a slice, array, or map
// the walk is inside of, implements [SelfValidator] where the walk
// would validate it. When it reports false, the walk of the values below
// v validates nothing, so the walk passes v's children at once.
//
// The scan reads each pointer, map, and slice it reaches once, however
// many paths lead there.
func (w *selfWalker) holdsBelow(v reflect.Value) bool {
	defer clear(w.scanning)

	if v.Kind() != reflect.Array && ownsAddress(v) {
		key := visitOf(v)
		if held, ok := w.scanned[key]; ok {
			return held
		}

		w.scanning[key] = true
	}

	// The scan stops only at v itself on the way back up, which the walk
	// is inside of and would stop at too, so the result holds either way.
	held, _ := w.scanChildren(v)

	// A false result means the scan read everything below v. When v holds
	// no validator of its own either, no value the scan reached holds one,
	// since everything below such a value is below v too. When v does hold
	// one, a value that leads back to v holds it below, so the scan records
	// nothing more.
	if !held && !implementsSelfValidator(v.Type()) {
		for key := range w.scanning {
			w.scanned[key] = false
		}
	}

	return held
}

// scanValue reports whether v, or a value below it, implements
// [SelfValidator] where the walk would validate it. It follows the walk
// down: through interfaces and pointers, into fields, elements, and map
// keys and values, and not below a value whose type may hold no
// validator. Below a value that decodes itself, it follows only the
// embedded field that decodes it, as [decoderField] finds it. The second
// result is false when the scan stopped at a value it had already
// reached, whose first read decides the answer, so a false first result
// then holds only for that scan.
func (w *selfWalker) scanValue(v reflect.Value) (bool, bool) {
	if !v.IsValid() || !mayHoldValidator(v.Type()) {
		return false, true
	}

	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return false, true
		}

		return w.scanValue(v.Elem())

	case reflect.Pointer, reflect.Map, reflect.Slice:
		if v.IsNil() {
			if v.Kind() == reflect.Pointer {
				return false, true
			}

			return implementsSelfValidator(v.Type()), true
		}

		if !ownsAddress(v) {
			return w.scanOwned(v)
		}

		key := visitOf(v)
		if held, ok := w.scanned[key]; ok {
			return held, true
		}

		if w.scanning[key] {
			return false, false
		}

		// The record of a map or slice covers only the values below it, as
		// holdsBelow reads it, so a map or slice that validates itself
		// reports a validator here and records nothing.
		if v.Kind() != reflect.Pointer && implementsSelfValidator(v.Type()) {
			return true, true
		}

		w.scanning[key] = true

		held, complete := w.scanOwned(v)
		if held || complete {
			w.scanned[key] = held
		}

		return held, complete

	default:
		return w.scanSelf(v)
	}
}

// scanOwned scans v, a non-nil pointer, map, or slice, as
// [selfWalker.scanValue] describes.
func (w *selfWalker) scanOwned(v reflect.Value) (bool, bool) {
	if v.Kind() == reflect.Pointer {
		return w.scanValue(v.Elem())
	}

	return w.scanSelf(v)
}

// scanSelf scans v, a value that is no pointer or interface, as
// [selfWalker.scanValue] describes.
func (w *selfWalker) scanSelf(v reflect.Value) (bool, bool) {
	if implementsSelfValidator(v.Type()) {
		return true, true
	}

	if decodesItself(v.Type()) {
		if i, ok := decoderField(v.Type()); ok {
			return w.scanValue(v.Field(i))
		}

		return false, true
	}

	return w.scanChildren(v)
}

// scanChildren scans the fields of a struct, the elements of a slice or
// array, or the keys and values of a map, as [selfWalker.scanValue] describes.
// It reads the fields of an inline struct that its parent shadows too,
// which the walk passes over, so it can report a validator the walk does
// not reach. The walk then validates nothing more than it would anyway.
func (w *selfWalker) scanChildren(v reflect.Value) (bool, bool) {
	complete := true

	scan := func(child reflect.Value) bool {
		held, done := w.scanValue(child)
		complete = complete && done

		return held
	}

	switch v.Kind() {
	case reflect.Struct:
		for i := range v.NumField() {
			field := v.Type().Field(i)
			if !mayHoldValidator(field.Type) {
				continue
			}

			if _, _, skip := fieldName(field); !skip && scan(v.Field(i)) {
				return true, true
			}
		}

	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			if scan(v.Index(i)) {
				return true, true
			}
		}

	case reflect.Map:
		t := v.Type()
		scanKeys, scanValues := mayHoldValidator(t.Key()), mayHoldValidator(t.Elem())

		// The scan reuses one holder for the keys and one for the values,
		// since iter.Key and iter.Value copy each entry. SetIterKey and
		// SetIterValue panic on a map read through an unexported field,
		// so the scan reads copies from such a map.
		var key, value reflect.Value

		if v.Len() != 0 && v.CanInterface() {
			if scanKeys {
				key = reflect.New(t.Key()).Elem()
			}

			if scanValues {
				value = reflect.New(t.Elem()).Elem()
			}
		}

		for iter := v.MapRange(); iter.Next(); {
			if scanKeys && scan(iterKey(iter, key)) || scanValues && scan(iterValue(iter, value)) {
				return true, true
			}
		}

	default:
	}

	return false, complete
}

// iterKey returns the key at iter, set into holder when holder is valid,
// or a copy when it is not.
func iterKey(iter *reflect.MapIter, holder reflect.Value) reflect.Value {
	if !holder.IsValid() {
		return iter.Key()
	}

	holder.SetIterKey(iter)

	return holder
}

// iterValue returns the value at iter, set into holder when holder is
// valid, or a copy when it is not.
func iterValue(iter *reflect.MapIter, holder reflect.Value) reflect.Value {
	if !holder.IsValid() {
		return iter.Value()
	}

	holder.SetIterValue(iter)

	return holder
}

// implementsSelfValidator reports whether a value of type t implements
// [SelfValidator] through a Validate of its own, on its value or its
// pointer, as [selfWalker.validate] checks it. A struct that gets
// Validate from an embedded field does not own it, so the struct passes
// the check to the field. The walk validates that field at its own path,
// or at the path of the struct when the field decodes the struct, as
// [decoderField] finds it. The method does not run when the field is nil
// or go-yaml skips it, since the walk never reaches such a field. Nor
// does it run when the struct declares the unmarshaler method go-yaml
// calls, since the walk validates nothing below a struct that decodes
// itself.
func implementsSelfValidator(t reflect.Type) bool {
	if cached, ok := ownsValidator.Load(t); ok {
		if owns, ok := cached.(bool); ok {
			return owns
		}
	}

	owns := reflect.PointerTo(t).Implements(reflect.TypeFor[SelfValidator]()) && !promotesMethod(t, "Validate")
	ownsValidator.Store(t, owns)

	return owns
}

// promotesMethod reports whether the method of the given name in the
// method set of the pointer to t comes from an embedded field of t
// rather than from t itself. Only a struct with an embedded field can
// promote a method. The compiler gives t a wrapper for a promoted
// method and marks its source file as "<autogenerated>". Neither the
// language spec nor the reflect package promises that, and reflect
// offers no other way to tell a promoted method from a declared one, so
// the tests of embedded fields that promote or shadow a method pin it.
// The method set of the value comes first, since the pointer holds a
// wrapper for a method declared on the value too.
func promotesMethod(t reflect.Type, name string) bool {
	if t.Kind() != reflect.Struct || !hasEmbedded(t) {
		return false
	}

	method, ok := t.MethodByName(name)
	if !ok {
		method, ok = reflect.PointerTo(t).MethodByName(name)
		if !ok {
			return false
		}
	}

	fn := runtime.FuncForPC(method.Func.Pointer())
	if fn == nil {
		return false
	}

	file, _ := fn.FileLine(fn.Entry())

	return file == "<autogenerated>"
}

// hasEmbedded reports whether t, a struct type, has an embedded field.
func hasEmbedded(t reflect.Type) bool {
	for field := range t.Fields() {
		if field.Anonymous {
			return true
		}
	}

	return false
}

// validate runs Validate on v when v implements [SelfValidator] through
// a method of its own, on its value or its pointer, with the result
// rebased under base, and reports whether v passed. A value the walk
// cannot take the address of, such as one held by a map, validates
// through a copy, so a Validate with a pointer receiver runs on it too.
func (w *selfWalker) validate(v reflect.Value, base paths.Path) bool {
	if !implementsSelfValidator(v.Type()) {
		return true
	}

	v = addressable(v)

	validator, ok := reflect.TypeAssert[SelfValidator](v.Addr())
	if !ok {
		return true
	}

	err := Rebase(validator.Validate(), base)
	if isNothing(err) {
		return true
	}

	w.errs = append(w.errs, err)

	return false
}

// fieldName returns the name go-yaml decodes field under, whether the
// field is inline so its own fields sit beside its siblings, and whether
// go-yaml skips the field. It skips an unexported field that is not
// embedded, and one whose tag is "-". The name comes from the yaml tag,
// or the json tag when the field has no yaml tag. It is the lowercased
// field name when neither tag names it, as go-yaml spells it.
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
// them, directly or through an alias. Where the mapping and its merges
// define one key more than once, the later entry in document order wins,
// with the sources of one merge in sequence order, as in the decode. The
// map holds no key a path cannot resolve to, and is empty when no mapping
// is at base, as for a value the document did not set.
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

	// The decoder sets each entry in document order, and a merge sets
	// the keys of its sources where it stands, so a later entry wins
	// whether it is a key of the mapping or a merge. An alias in any
	// merge that does not resolve leaves out the keys of every merge.
	_, err := w.pathResolver().MergeSources(mapping)
	merges := err == nil

	for _, entry := range mapping.Values {
		if entry == nil || entry.Key == nil {
			continue
		}

		if !entry.Key.IsMergeKey() {
			w.addKeyName(entry.Key, t, names)

			continue
		}

		if !merges {
			continue
		}

		sources, err := w.pathResolver().MergeSources(&ast.MappingNode{
			Values: []*ast.MappingValueNode{entry},
		})
		if err != nil {
			continue
		}

		for _, src := range sources {
			w.collectKeyNames(src, t, names, seen)
		}
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
// key is its content rather than its `|` or `>` indicator. A key that does
// not decode, whose decode panics, or whose value cannot key a map, adds
// nothing. An alias key takes its text and value from the content of its
// anchor, as [paths] names it, and adds nothing when that anchor does not
// resolve.
//
// As go-yaml does, addKeyName decodes a key of a pointer type t as the
// type t points to, and leaves a null key, or an alias to a null, a nil
// pointer. A null counts only where the key itself is one, before
// go-yaml looks through a `?`, an anchor, or a tag, so go-yaml points a
// key such as `&a ~` at a zero value instead.
func (w *selfWalker) addKeyName(key ast.MapKeyNode, t reflect.Type, names map[any]string) {
	node := keyValueNode(key)
	null := key.Type() == ast.NullType

	if _, ok := node.(*ast.AliasNode); ok {
		content, err := w.pathResolver().Deref(node)
		if err != nil || content == nil {
			return
		}

		node = content
		null = key.Type() == ast.AliasType && content.Type() == ast.NullType
	}

	if t.Kind() == reflect.Pointer && !null {
		t = t.Elem()
	}

	var name string

	switch n := unwrapNode(node).(type) {
	case *ast.StringNode:
		name = n.Value
	case *ast.LiteralNode:
		if n.Value == nil {
			return
		}

		name = n.Value.Value

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

	err := decodeWithRecover(w.ctx, w.keyDecoder(), node, decoded.Interface())
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

// timeKey stands in names for a [time.Time] key. A [time.Time] holds a
// pointer to its location, and a decode of an offset such as +05:30
// makes a new location each time, so the key in the map equals no key
// that decodes from the same text. The fields together form the key in
// names.
//
//nolint:unused // The fields tell the keys of names apart.
type timeKey struct {
	zone   string
	sec    int64
	nsec   int
	offset int
}

// nameKey returns the value names holds the text of key under: [nanKey]
// for a float NaN and [timeKey] for a [time.Time], alone or behind an
// interface, or else the value of key itself. A pointer key stands for
// the value it points to, as [pointee] gives it. The bool result is
// false for a key that cannot key a map.
func nameKey(key reflect.Value) (any, bool) {
	key = pointee(key)
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

	if tm, ok := reflect.TypeAssert[time.Time](v); ok {
		zone, offset := tm.Zone()

		return timeKey{zone: zone, sec: tm.Unix(), nsec: tm.Nanosecond(), offset: offset}, true
	}

	return key.Interface(), true
}

// pointee returns the value key points to when key is a non-nil pointer
// to a value that can key a map, and key itself otherwise. Since go-yaml
// decodes a pointer key as the value it points to, that value, rather
// than the address, names the key. The value a pointer points to may
// hold a slice or map that refers back to itself, which no formatting
// finishes, so a pointer to a value that cannot key a map stays as it
// is. A pointer held in an interface stays too, since go-yaml decodes
// no key of an interface type to one.
func pointee(key reflect.Value) reflect.Value {
	if key.Kind() == reflect.Pointer && !key.IsNil() && key.Elem().Comparable() {
		return key.Elem()
	}

	return key
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
// string itself, or the formatted value of any other key. A pointer key
// stands for the value it points to, as [pointee] gives it.
func mapKey(key reflect.Value, names map[any]string) string {
	if k, ok := nameKey(key); ok {
		if name, ok := names[k]; ok {
			return name
		}
	}

	key = pointee(key)
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
