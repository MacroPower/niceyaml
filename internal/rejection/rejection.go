// Package rejection words the errors the go-yaml decoder returns when it
// rejects a value, for a reader of the document rather than of the Go
// target.
//
// The decoder words three of its errors for the Go target, and names the
// outermost struct field on the way to the value rather than the field
// that holds it. Those errors carry what they compare in typed fields, so
// [Classify] writes the message from the fields, with the kinds of YAML
// values the document holds in place of Go types. It also gives each
// rejection a [Label], which a caller maps to an error a reader matches.
package rejection

import (
	"fmt"
	"math"
	"reflect"
	"strconv"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"

	"go.jacobcolvin.com/niceyaml/internal/unmarshaler"
)

// Label names what a rejection says about its value. The zero Label
// names nothing.
type Label int

const (
	// None labels a rejection that names no kind, range, or field.
	None Label = iota

	// WrongKind labels a value of a kind its target does not take, or of
	// another type than its target.
	WrongKind

	// Overflow labels a number outside the range of its integer type.
	Overflow

	// UnknownField labels a mapping key that no field of the target
	// reads.
	UnknownField
)

// The names a rejection gives the kinds of YAML values.
const (
	kindMapping  = "mapping"
	kindSequence = "sequence"
	kindString   = "string"
	kindInteger  = "integer"
	kindFloat    = "float"
	kindBoolean  = "boolean"
	kindNull     = "null"

	// The name of a value whose kind its type does not tell, as for a
	// value an interface holds.
	kindValue = "value"

	// The name a target takes when no YAML value but a null decodes into
	// it, as for a channel.
	kindNone = "no value"
)

// The byte slice a !!binary tag reads as, which [kindOfType] names apart
// from its kind.
var bytesType = reflect.TypeFor[[]byte]()

// Classify returns the message of err, a rejection the go-yaml decoder
// returned, worded for the document, and its [Label]:
//
//   - A value of the wrong kind, a [yaml.TypeError] or a
//     [yaml.UnexpectedNodeTypeError], reads "expected integer, got
//     string", labeled [WrongKind].
//   - A number that overflows an integer type, a [yaml.OverflowError],
//     reads "expected integer from -128 to 127, got 300", with the range
//     of the type, labeled [Overflow].
//   - An unknown field, a [yaml.UnknownFieldError], keeps the message the
//     decoder gave it, which names the field as the document does, and
//     is labeled [UnknownField].
//
// The decoder also rejects a value by its Go type alone, as when it hands
// the value of an anchor to an inline field with an alias option, or
// fills a pointer to a pointer. The two types can then hold one kind of
// value, as two struct types do, and an anchor the decoder read into an
// interface has a type that names no kind. Kinds alone would read
// "expected mapping, got mapping", so the message reads "expected
// mapping, got mapping of another type" and names neither type.
//
// Any other error keeps the message the decoder gave it and gets [None].
func Classify(err yaml.Error) (string, Label) {
	switch e := err.(type) { //nolint:errorlint // The decoder returns these types unwrapped.
	case *yaml.TypeError:
		want, got := kindOfType(e.DstType), kindOfType(e.SrcType)
		if want == got || got == kindValue {
			return fmt.Sprintf("expected %s, got %s of another type", want, got), WrongKind
		}

		return fmt.Sprintf("expected %s, got %s", want, got), WrongKind

	case *yaml.UnexpectedNodeTypeError:
		return fmt.Sprintf("expected %s, got %s", kindOfNode(e.Expected), kindOfNode(e.Actual)), WrongKind

	case *yaml.OverflowError:
		lo, hi, ok := integerRange(e.DstType)
		if !ok {
			return fmt.Sprintf("expected %s, got %s", kindOfType(e.DstType), e.SrcNum), Overflow
		}

		return fmt.Sprintf("expected %s from %s to %s, got %s", kindInteger, lo, hi, e.SrcNum), Overflow

	case *yaml.UnknownFieldError:
		return err.GetMessage(), UnknownField

	default:
		return err.GetMessage(), None
	}
}

// ExpectedString returns the message of the rejection of a node of type
// t that a string reads, as [Classify] words a value of the wrong kind,
// such as "expected string, got sequence".
func ExpectedString(t ast.NodeType) string {
	return fmt.Sprintf("expected %s, got %s", kindString, kindOfNode(t))
}

// kindOfType returns the kind of YAML value that the Go type t holds, for
// the message of a rejection. The type is the target of a decode, or the
// type the go-yaml decoder read a value of the document as. A nil type
// is the null the decoder reads as a nil value. A pointer names what it
// points to.
//
// The result never names a Go type. An interface holds a value of any
// kind, so it reads [kindValue]. No YAML value but a null decodes into a
// channel, a function, a complex number, or an unsafe pointer, so a
// target of one of those kinds reads [kindNone].
func kindOfType(t reflect.Type) string {
	if t == nil {
		return kindNull
	}

	t = unmarshaler.Base(t)

	switch t {
	case unmarshaler.TimeType:
		return "timestamp"
	case unmarshaler.DurationType:
		return "duration"
	case bytesType:
		return "binary"
	case unmarshaler.MapSliceType:
		return kindMapping
	}

	switch t.Kind() {
	case reflect.Bool:
		return kindBoolean
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return kindInteger
	case reflect.Float32, reflect.Float64:
		return kindFloat
	case reflect.String:
		return kindString
	case reflect.Slice, reflect.Array:
		return kindSequence
	case reflect.Map, reflect.Struct:
		return kindMapping
	case reflect.Interface:
		return kindValue
	default:
		return kindNone
	}
}

// kindOfNode returns the kind of YAML value a node of type t holds, for
// the message of a rejection. A type that holds no value, such as an
// anchor, has the name go-yaml gives it.
func kindOfNode(t ast.NodeType) string {
	switch t {
	case ast.MappingType, ast.MappingValueType:
		return kindMapping
	case ast.SequenceType:
		return kindSequence
	case ast.StringType, ast.LiteralType:
		return kindString
	case ast.IntegerType:
		return kindInteger
	case ast.FloatType, ast.InfinityType, ast.NanType:
		return kindFloat
	case ast.BoolType:
		return kindBoolean
	case ast.NullType:
		return kindNull
	default:
		return t.YAMLName()
	}
}

// integerRange returns the lowest and the highest value of the integer
// type t in decimal, or false for a type of any other kind.
func integerRange(t reflect.Type) (string, string, bool) {
	if t == nil {
		return "", "", false
	}

	switch t.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		shift := 64 - t.Bits()

		return strconv.FormatInt(math.MinInt64>>shift, 10), strconv.FormatInt(math.MaxInt64>>shift, 10), true

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return "0", strconv.FormatUint(math.MaxUint64>>(64-t.Bits()), 10), true

	default:
		return "", "", false
	}
}
