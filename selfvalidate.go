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

	"go.jacobcolvin.com/niceyaml/paths"
)

// selfValidate runs Validate on every value in the tree of v, a non-nil
// pointer to a decoded value, that implements [SelfValidator], and
// returns what they report with the paths in each error rebased under
// the path of the value in the document: the field name go-yaml decoded
// it under, the index of a slice or array element, or the key of a map
// entry. The values below a value validate before it does, and a value
// validates only when every value below it passed, so a parent that
// checks a relation between its fields sees fields that hold together.
// A value whose type decodes itself, through an unmarshaler method,
// validates itself and nothing below it, since its fields need not
// mirror the document and the paths under it would point nowhere.
// Several errors come back joined, one per value that failed. Returns
// nil when nothing failed.
func selfValidate(v any) error {
	w := selfWalker{walking: map[visit]bool{}, done: map[visit]bool{}}
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
// the value failed.
type selfWalker struct {
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
		if v.IsNil() {
			return true
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
		keys := v.MapKeys()
		slices.SortStableFunc(keys, func(a, b reflect.Value) int {
			if c := strings.Compare(mapKey(a), mapKey(b)); c != 0 {
				return c
			}

			return strings.Compare(a.Type().String(), b.Type().String())
		})

		for _, key := range keys {
			if !w.walk(v.MapIndex(key), base.Child(mapKey(key))) {
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

// mapKey returns the path segment for a map key: the string itself, or
// the text of any other key as the document spells it.
func mapKey(key reflect.Value) string {
	if key.Kind() == reflect.String {
		return key.String()
	}

	return fmt.Sprint(key.Interface())
}
