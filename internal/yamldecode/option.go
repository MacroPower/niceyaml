// The decode options below are copies of go-yaml's option.go. Go-yaml's
// options set unexported fields of go-yaml's Decoder, so they cannot set the
// fields of the copy.

package yamldecode

import (
	"context"
	"io"
	"reflect"

	"github.com/goccy/go-yaml/ast"
)

// DecodeOption functional option type for Decoder
type DecodeOption func(d *Decoder) error

// ReferenceReaders pass to Decoder that reference to anchor defined by passed readers
func ReferenceReaders(readers ...io.Reader) DecodeOption {
	return func(d *Decoder) error {
		d.referenceReaders = append(d.referenceReaders, readers...)
		return nil
	}
}

// ReferenceFiles pass to Decoder that reference to anchor defined by passed files
func ReferenceFiles(files ...string) DecodeOption {
	return func(d *Decoder) error {
		d.referenceFiles = files
		return nil
	}
}

// ReferenceDirs pass to Decoder that reference to anchor defined by files under the passed dirs
func ReferenceDirs(dirs ...string) DecodeOption {
	return func(d *Decoder) error {
		d.referenceDirs = dirs
		return nil
	}
}

// RecursiveDir search yaml file recursively from passed dirs by ReferenceDirs option
func RecursiveDir(isRecursive bool) DecodeOption {
	return func(d *Decoder) error {
		d.isRecursiveDir = isRecursive
		return nil
	}
}

// Validator set StructValidator instance to Decoder
func Validator(v StructValidator) DecodeOption {
	return func(d *Decoder) error {
		d.validator = v
		return nil
	}
}

// Strict enable DisallowUnknownField
func Strict() DecodeOption {
	return func(d *Decoder) error {
		d.disallowUnknownField = true
		return nil
	}
}

// DisallowUnknownField causes the Decoder to return an error when the destination
// is a struct and the input contains object keys which do not match any
// non-ignored, exported fields in the destination.
func DisallowUnknownField() DecodeOption {
	return func(d *Decoder) error {
		d.disallowUnknownField = true
		return nil
	}
}

// AllowFieldPrefixes, when paired with [DisallowUnknownField], allows fields
// with the specified prefixes to bypass the unknown field check.
func AllowFieldPrefixes(prefixes ...string) DecodeOption {
	return func(d *Decoder) error {
		d.allowedFieldPrefixes = append(d.allowedFieldPrefixes, prefixes...)
		return nil
	}
}

// AllowDuplicateMapKey ignore syntax error when mapping keys that are duplicates.
func AllowDuplicateMapKey() DecodeOption {
	return func(d *Decoder) error {
		d.allowDuplicateMapKey = true
		return nil
	}
}

// UseOrderedMap can be interpreted as a map,
// and uses MapSlice ( ordered map ) aggressively if there is no type specification
func UseOrderedMap() DecodeOption {
	return func(d *Decoder) error {
		d.useOrderedMap = true
		return nil
	}
}

// UseJSONUnmarshaler if neither `BytesUnmarshaler` nor `InterfaceUnmarshaler` is implemented
// and `UnmashalJSON([]byte)error` is implemented, convert the argument from `YAML` to `JSON` and then call it.
func UseJSONUnmarshaler() DecodeOption {
	return func(d *Decoder) error {
		d.useJSONUnmarshaler = true
		return nil
	}
}

// CustomUnmarshaler overrides any decoding process for the type specified in generics.
//
// NOTE: If RegisterCustomUnmarshaler and CustomUnmarshaler of DecodeOption are specified for the same type,
// the CustomUnmarshaler specified in DecodeOption takes precedence.
func CustomUnmarshaler[T any](unmarshaler func(*T, []byte) error) DecodeOption {
	return func(d *Decoder) error {
		var typ *T
		d.customUnmarshalerMap[reflect.TypeOf(typ)] = func(ctx context.Context, v interface{}, b []byte) error {
			return unmarshaler(v.(*T), b)
		}
		return nil
	}
}

// CustomUnmarshalerContext overrides any decoding process for the type specified in generics.
// Similar to CustomUnmarshaler, but allows passing a context to the unmarshaler function.
func CustomUnmarshalerContext[T any](unmarshaler func(context.Context, *T, []byte) error) DecodeOption {
	return func(d *Decoder) error {
		var typ *T
		d.customUnmarshalerMap[reflect.TypeOf(typ)] = func(ctx context.Context, v interface{}, b []byte) error {
			return unmarshaler(ctx, v.(*T), b)
		}
		return nil
	}
}

// CommentToMap apply the position and content of comments in a YAML document to a CommentMap.
func CommentToMap(cm CommentMap) DecodeOption {
	return func(d *Decoder) error {
		if cm == nil {
			return ErrInvalidCommentMapValue
		}
		d.toCommentMap = cm
		return nil
	}
}

// AliasBudget bounds what one decoder reads through aliases: nodes is the
// count of nodes it may decode again below an alias, and textBytes is the
// count of bytes it may write out for aliases in the text that it hands to
// unmarshalers. A decode past either bound fails with [ErrAliasBudget]. An
// alias that takes the value the decoder holds for its anchor costs nothing.
func AliasBudget(nodes, textBytes int) DecodeOption {
	return func(d *Decoder) error {
		d.bounded, d.aliasNodesLeft, d.aliasBytesLeft = true, nodes, textBytes
		return nil
	}
}

// ResolveAlias has the decoder ask resolve for the node that each alias
// reads, in place of the last anchor of the alias's name that the decoder
// read. The function returns the value of the anchor that the alias reads,
// or false for an alias without one. An alias that resolve binds to a null
// node that holds the token of the alias reads as null, in a target and in
// the text that the decoder hands an unmarshaler.
//
// The decoder then holds each anchor by its value node and not by its name.
// So two anchors of one name stay apart, and the decoder reads an anchor
// outside the node that it decodes without an earlier pass over that
// anchor. It keeps the value of an anchor that an alias reads into an any
// value, as it keeps the value of an anchor that it reads. Each later alias
// of the anchor takes that value. The decoder does not keep the value
// of a read in which an alias inside its open anchor read as null, because
// that null depends on where the read starts. An inline field with the alias
// option reads the anchor of its merge key in the same way.
func ResolveAlias(resolve func(alias *ast.AliasNode) (ast.Node, bool)) DecodeOption {
	return func(d *Decoder) error {
		d.resolveAlias = resolve
		return nil
	}
}

// CollectErrors has a decode go on past the error of a field, an element,
// or an entry and return every error it finds, each unknown field under
// [DisallowUnknownField] among them, as an [Errors] in the order of the
// document. A decode that finds one error returns that error alone. Each
// error comes in a [PathError] with the path on which the decoder read its
// value. A map still stops at the error of a key or of a `<<` merge key,
// as go-yaml does. Without CollectErrors, a decode returns the first error
// it finds.
func CollectErrors() DecodeOption {
	return func(d *Decoder) error {
		d.collectErrors = true
		return nil
	}
}

// ReportUnknownField has a decode find the unknown fields of each struct, as
// [DisallowUnknownField] does, and hand the error of each one to fn. The
// decode does not return those errors, so an unknown field does not fail
// it, and an unmarshaler whose callback reads a mapping with such a field
// does not see the error. Each struct hands its fields in the order of the
// document. Under [CollectErrors], each error comes in a [PathError] with
// the path on which the decoder read the key.
//
// The decode hands fn the same fields that [DisallowUnknownField] fails
// for. An alias that takes the value that the decoder read for its anchor
// hands on the fields of that read again, at its own path. A call of the
// callback of an unmarshaler that fails holds its fields. The decode hands
// them on only when the unmarshaler returns the error of that call as it
// is, because an unmarshaler that drops the error can read the value
// another way.
func ReportUnknownField(fn func(err error)) DecodeOption {
	return func(d *Decoder) error {
		d.disallowUnknownField = true
		d.unknownField = fn
		return nil
	}
}

// StartValue has the decoder start each value that it creates from the value
// that start returns for its type. The decoder creates each element of a
// slice or an array, and each key and value of a map. It also creates the
// target of each pointer that it fills, such as an inline field behind a nil
// pointer. The decoder passes start the node that it reads into the value.
// For an inline field, that node is the mapping of the struct that holds the
// field. A value that decodes itself sees that start. An invalid value
// from start, or one of another type, means the zero value. A struct field
// still starts from the value that the field holds, and a pointer that holds
// a value decodes from that value. A null sets a pointer to nil and leaves
// any other value at its start. A field of an inline struct that the struct
// that holds it names too keeps the value that it started from, where
// go-yaml sets it to zero.
//
// The decoder gives an alias the value that it decoded for the anchor when
// the place of the alias starts from the value that the place of the anchor
// started from. A place that starts from another value decodes the anchor
// again from its own start. Two places of one type that do not hold a value
// of their own, such as two elements, count as places with one start. The
// decoder does not call start to compare them. An anchor that the decoder
// read into an any value started from nil. Places of two types start from
// one value only when both start from zero. An interface value takes the
// value of the anchor as go-yaml gives it.
//
// The decoder compares the start of the place of the alias with a copy of
// the start of the place of the anchor, made before it decodes the anchor.
// The copy shares funcs, channels, and the unexported fields of structs with
// the start. An unmarshaler that writes into an unexported map or pointer
// target of its start thus can make an alias after it decode the anchor
// again.
// Two funcs count as equal when they have one code pointer, and two NaN
// floats count as equal.
func StartValue(start func(reflect.Type, ast.Node) reflect.Value) DecodeOption {
	return func(d *Decoder) error {
		d.startValue = start
		return nil
	}
}
