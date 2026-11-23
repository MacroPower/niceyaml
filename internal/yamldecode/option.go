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
