package aliasing

import (
	"encoding"
	"reflect"
	"slices"
	"sync"

	"github.com/goccy/go-yaml"

	"go.jacobcolvin.com/niceyaml/internal/unmarshaler"
	"go.jacobcolvin.com/niceyaml/internal/yamlfield"
)

var (
	// The interfaces through which the go-yaml decoder hands a value the
	// YAML bytes of its node, with each alias in the node written out in
	// full.
	bytesUnmarshalerTypes = []reflect.Type{
		reflect.TypeFor[yaml.BytesUnmarshalerContext](),
		reflect.TypeFor[yaml.BytesUnmarshaler](),
	}

	// The interfaces through which the go-yaml decoder hands a value a
	// function that decodes its node into any type that the method picks,
	// such as one that reads text. A walk of the type cannot see that type, so
	// a decode into one of these might read text too.
	callbackUnmarshalerTypes = []reflect.Type{
		reflect.TypeFor[yaml.InterfaceUnmarshalerContext](),
		reflect.TypeFor[yaml.InterfaceUnmarshaler](),
	}

	// The interfaces through which the go-yaml decoder hands a value its
	// node, so the value does not read text.
	nodeUnmarshalerTypes = []reflect.Type{
		reflect.TypeFor[yaml.NodeUnmarshaler](),
		reflect.TypeFor[yaml.NodeUnmarshalerContext](),
	}

	// The interface through which the go-yaml decoder hands a value the
	// text of its node, with each alias in the node written out in full.
	textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()

	// The result of [DecodesText] for each type that it has read. A type never
	// changes, so every decode shares the results.
	decodesText sync.Map
)

// DecodesText reports whether a decode into t might hand some value the
// text of its node, through an UnmarshalText method or an UnmarshalYAML
// method that takes YAML bytes. It reports true when t, or a type that
// the decoder reaches from t, has such a method on its pointer. The
// decoder reaches a type through a pointer, a struct field, an array or
// slice element, or a map key or element. An UnmarshalYAML method that
// takes a decode function counts too, because it can decode the node into
// any type, including one that reads text.
//
// The decoder skips a struct field that is unexported and not embedded,
// or that a yaml or json tag of "-" leaves out. Those fields do not
// count. An UnmarshalYAML method that takes the node does not read text,
// and the decoder calls it ahead of an UnmarshalText method. The decoder
// does not reach the fields or elements of a type with such a method, so
// they do not count. The decoder parses a [time.Time] from the value of
// its scalar. A [time.Time] therefore does not read text, even though its
// pointer has UnmarshalText.
//
// A decode into a type that DecodesText reports runs [CheckDecodeText] as
// well as [CheckDecode].
func DecodesText(t reflect.Type) bool {
	if cached, ok := decodesText.Load(t); ok {
		if text, ok := cached.(bool); ok {
			return text
		}
	}

	text := reachesText(t, unmarshaler.Options{})
	decodesText.Store(t, text)

	return text
}

// DecodesTextWith reports what [DecodesText] reports for a decode with
// the unmarshalers u. In such a decode, a type that u gives a function
// reads text too. So does a type with an UnmarshalJSON method when u turns
// on UnmarshalJSON methods. The decoder hands either one the node with
// each alias in it written out in full. The answer holds for that decode
// alone, so each call reads t again.
func DecodesTextWith(t reflect.Type, u unmarshaler.Options) bool {
	if u.Empty() {
		return DecodesText(t)
	}

	return reachesText(t, u)
}

// reachesText reports what [DecodesTextWith] reports for t and u.
func reachesText(t reflect.Type, u unmarshaler.Options) bool {
	return yamlfield.Reaches(t, func(t reflect.Type) (bool, bool) {
		ptr := reflect.PointerTo(t)

		// The decoder looks for a function of u and then these methods in
		// the order of the cases below, and decodes t through the first one
		// it finds in place of the fields or elements of t.
		switch {
		case u.Custom(t),
			slices.ContainsFunc(bytesUnmarshalerTypes, ptr.Implements),
			slices.ContainsFunc(callbackUnmarshalerTypes, ptr.Implements):
			return true, true
		case slices.ContainsFunc(nodeUnmarshalerTypes, ptr.Implements), t == unmarshaler.TimeType:
			return false, true
		case ptr.Implements(textUnmarshalerType), u.JSON && ptr.Implements(unmarshaler.JSONType):
			return true, true
		default:
			return false, false
		}
	})
}
