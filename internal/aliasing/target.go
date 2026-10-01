package aliasing

import (
	"encoding"
	"reflect"
	"slices"
	"sync"
	"time"

	"github.com/goccy/go-yaml"
)

var (
	// The interfaces through which the go-yaml decoder hands a value the
	// text of its node, with each alias in the node written out in full.
	textUnmarshalerTypes = []reflect.Type{
		reflect.TypeFor[yaml.BytesUnmarshaler](),
		reflect.TypeFor[yaml.BytesUnmarshalerContext](),
		reflect.TypeFor[encoding.TextUnmarshaler](),
	}

	// The interfaces through which the go-yaml decoder hands a value a
	// function that decodes its node into any type the method picks, such
	// as one that reads text. A walk of the type cannot see that type, so
	// a decode into one of these may read text too.
	callbackUnmarshalerTypes = []reflect.Type{
		reflect.TypeFor[yaml.InterfaceUnmarshaler](),
		reflect.TypeFor[yaml.InterfaceUnmarshalerContext](),
	}

	// The go-yaml decoder parses a [time.Time] from the value of a scalar
	// before it looks for an UnmarshalText method, so it reads no text. A
	// type that embeds one gets no such rule.
	timeType = reflect.TypeFor[time.Time]()

	// The result of [DecodesText] for each type it has read. A type never
	// changes, so every decode shares the results.
	decodesText sync.Map
)

// DecodesText reports whether a decode into t may hand some value the
// text of its node, through an UnmarshalText method or an UnmarshalYAML
// method that takes YAML bytes. That holds when t or a type the decoder
// reaches from t, through a pointer, a struct field, an array or slice
// element, or a map key or element, has such a method on its pointer. An
// UnmarshalYAML method that takes a decode function counts too, since it
// can decode the node into any type, including one that reads text. A
// decode into such a type runs [CheckDecodeText] as well as
// [CheckDecode]. The decoder parses a [time.Time] from the value of its
// scalar, so it reads no text, though its pointer has UnmarshalText.
func DecodesText(t reflect.Type) bool {
	if cached, ok := decodesText.Load(t); ok {
		if text, ok := cached.(bool); ok {
			return text
		}
	}

	text := reachesText(t, map[reflect.Type]bool{})
	decodesText.Store(t, text)

	return text
}

// reachesText reports what [DecodesText] reports for t, skipping the
// types in seen, which it has checked already, so a recursive type ends
// the walk.
func reachesText(t reflect.Type, seen map[reflect.Type]bool) bool {
	if seen[t] || t == timeType {
		return false
	}

	seen[t] = true

	ptr := reflect.PointerTo(t)
	if slices.ContainsFunc(textUnmarshalerTypes, ptr.Implements) ||
		slices.ContainsFunc(callbackUnmarshalerTypes, ptr.Implements) {
		return true
	}

	switch t.Kind() {
	case reflect.Pointer, reflect.Array, reflect.Slice:
		return reachesText(t.Elem(), seen)
	case reflect.Map:
		return reachesText(t.Key(), seen) || reachesText(t.Elem(), seen)
	case reflect.Struct:
		for field := range t.Fields() {
			if reachesText(field.Type, seen) {
				return true
			}
		}

		return false

	default:
		return false
	}
}
