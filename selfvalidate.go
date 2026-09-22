package niceyaml

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

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
// Several errors come back joined, one per value that failed. Returns
// nil when nothing failed.
func selfValidate(v any) error {
	w := selfWalker{seen: map[any]bool{}}
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
// decoded value, and the pointers it walked through, so a value that
// refers back to itself walks once. A pointer as an interface value
// compares by type and address together, so a struct and its first
// field, which share an address, are two entries.
type selfWalker struct {
	seen map[any]bool
	errs []error
}

// walk validates v and everything below it, with base as the path of v
// in the document, and reports whether nothing under v failed.
func (w *selfWalker) walk(v reflect.Value, base paths.Path) bool {
	for v.IsValid() && (v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface) {
		if v.IsNil() {
			return true
		}

		if v.Kind() == reflect.Pointer {
			if !v.CanInterface() {
				return true
			}

			key := v.Interface()
			if w.seen[key] {
				return true
			}

			w.seen[key] = true
		}

		v = v.Elem()
	}

	if !v.IsValid() || !v.CanInterface() {
		return true
	}

	if !w.children(v, base) {
		return false
	}

	return w.validate(v, base)
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
		iter := v.MapRange()
		for iter.Next() {
			if !w.walk(iter.Value(), base.Child(mapKey(iter.Key()))) {
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
