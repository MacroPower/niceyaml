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
// sets whole. Several errors come back joined, one per value that failed.
// Returns nil when nothing failed.
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
	if !v.IsValid() || !v.CanInterface() {
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
// shadowed names are those [selfWalker.walk] takes.
func (w *selfWalker) walkValue(v reflect.Value, base paths.Path, shadowed map[string]bool) bool {
	if !decodesItself(v.Type()) && !w.children(v, base, shadowed) {
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
	// implements one, in place of decoding field by field.
	unmarshalerTypes = []reflect.Type{
		reflect.TypeFor[yaml.BytesUnmarshaler](),
		reflect.TypeFor[yaml.BytesUnmarshalerContext](),
		reflect.TypeFor[yaml.InterfaceUnmarshaler](),
		reflect.TypeFor[yaml.InterfaceUnmarshalerContext](),
		reflect.TypeFor[yaml.NodeUnmarshaler](),
		reflect.TypeFor[yaml.NodeUnmarshalerContext](),
		reflect.TypeFor[encoding.TextUnmarshaler](),
	}

	// The result of [mayHoldValidator] for each type it has read. A type
	// never changes, so every walk shares the results.
	holdsValidator sync.Map

	// The result of [implementsSelfValidator] for each type it has read,
	// shared the same way.
	ownsValidator sync.Map
)

// decodesItself reports whether go-yaml decodes a value of type t whole,
// so the fields, elements, or entries of the value need not mirror the
// document. A type decodes itself through an unmarshaler method of its
// own, or as an [ast.Node], which the decoder sets to the node it
// decodes rather than decoding field by field. The tokens of a node also
// link to every other token of the file. The method set of the pointer
// holds the methods of both receivers, as the decoder checks it.
func decodesItself(t reflect.Type) bool {
	pt := reflect.PointerTo(t)

	return pt.Implements(reflect.TypeFor[ast.Node]()) || slices.ContainsFunc(unmarshalerTypes, pt.Implements)
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

	// The walk validates a value that decodes itself and nothing below it.
	if decodesItself(t) {
		return false
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
		// text, such as 1 and "1", order by the types they hold, and
		// several NaN keys order by their values. Each value comes from
		// the iteration rather than a lookup by its key, since a NaN key
		// equals no key, itself included. The key of an entry walks before
		// its value, at the key of the entry's path.
		names := w.keyNames(base, v.Type().Key())

		type entry struct {
			key, value    reflect.Value
			seg, typeName string
		}

		entries := make([]entry, 0, v.Len())
		shared := map[any]int{}

		for iter := v.MapRange(); iter.Next(); {
			key := iter.Key()
			if k, ok := nameKey(key); ok {
				shared[k]++
			}

			entries = append(entries, entry{key: key, value: iter.Value()})
		}

		// The names cannot tell apart several keys that [nameKey] gives
		// one value, such as several NaN keys, so none of them takes the
		// text of a document key. Their paths then resolve to no node,
		// and no error points at the line of another entry.
		for k, n := range shared {
			if n > 1 {
				delete(names, k)
			}
		}

		for i := range entries {
			entries[i].seg = mapKey(entries[i].key, names)
			entries[i].typeName = keyTypeName(entries[i].key)
		}

		slices.SortStableFunc(entries, func(a, b entry) int {
			c := cmp.Or(strings.Compare(a.seg, b.seg), strings.Compare(a.typeName, b.typeName))
			if c != 0 {
				return c
			}

			return strings.Compare(fmt.Sprint(a.value), fmt.Sprint(b.value))
		})

		for _, e := range entries {
			path := base.Child(e.seg)

			if !w.walk(e.key, path.Key(), nil) {
				ok = false
			}

			if !w.walk(e.value, path, nil) {
				ok = false
			}
		}

	default:
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
// keys and values, and not below a value that decodes itself or whose type may
// hold no validator. The second result is false when the scan stopped at
// a value it had already reached, whose first read decides the answer, so
// a false first result then holds only for that scan.
func (w *selfWalker) scanValue(v reflect.Value) (bool, bool) {
	if !v.IsValid() || !v.CanInterface() || !mayHoldValidator(v.Type()) {
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
		for iter := v.MapRange(); iter.Next(); {
			if scan(iter.Key()) || scan(iter.Value()) {
				return true, true
			}
		}

	default:
	}

	return false, complete
}

// implementsSelfValidator reports whether a value of type t implements
// [SelfValidator] through a Validate of its own, on its value or its
// pointer, as [selfWalker.validate] checks it. A struct that gets
// Validate from an embedded field does not own it. The walk validates
// that field at its own path, and never reaches it when the field is nil
// or go-yaml skips it, so the struct passes the check to the field.
func implementsSelfValidator(t reflect.Type) bool {
	if cached, ok := ownsValidator.Load(t); ok {
		if owns, ok := cached.(bool); ok {
			return owns
		}
	}

	owns := reflect.PointerTo(t).Implements(reflect.TypeFor[SelfValidator]()) && !promotesValidate(t)
	ownsValidator.Store(t, owns)

	return owns
}

// promotesValidate reports whether the Validate in the method set of the
// pointer to t comes from an embedded field of t rather than from t
// itself. Only a struct with an embedded field can promote a method. The
// compiler gives t a wrapper for a promoted method, and marks its source
// file as "<autogenerated>". The method set of the value comes first,
// since the pointer holds a wrapper for a method declared on the value
// too.
func promotesValidate(t reflect.Type) bool {
	if t.Kind() != reflect.Struct || !hasEmbedded(t) {
		return false
	}

	method, ok := t.MethodByName("Validate")
	if !ok {
		method, ok = reflect.PointerTo(t).MethodByName("Validate")
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

	if !v.CanAddr() {
		addressable := reflect.New(v.Type()).Elem()

		addressable.Set(v)

		v = addressable
	}

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
func (w *selfWalker) addKeyName(key ast.MapKeyNode, t reflect.Type, names map[any]string) {
	node := keyValueNode(key)

	if _, ok := node.(*ast.AliasNode); ok {
		content, err := w.pathResolver().Deref(node)
		if err != nil || content == nil {
			return
		}

		node = content
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
// interface, or else the value of key itself. The bool result is false
// for a key that cannot key a map.
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

	if tm, ok := reflect.TypeAssert[time.Time](v); ok {
		zone, offset := tm.Zone()

		return timeKey{zone: zone, sec: tm.Unix(), nsec: tm.Nanosecond(), offset: offset}, true
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
