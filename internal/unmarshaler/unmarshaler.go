// Package unmarshaler holds the rules the go-yaml decoder follows when
// it decodes a value whole, through an unmarshaler, in place of its
// fields, elements, or entries.
//
// A type decodes itself through a method the decoder looks for, or as a
// node of the go-yaml ast package, as [DecodesItself] reports. The
// options of a decode can give other types an unmarshaler too: a
// function for one type, or the UnmarshalJSON method of every type that
// has one. [Options] holds those, and its methods answer for one decode
// what the functions of the package answer for every decode.
package unmarshaler

import (
	"context"
	"encoding"
	"math"
	"reflect"
	"runtime"
	"slices"
	"sync"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/yamlfield"
)

var (
	// Interfaces holds the interfaces the go-yaml decoder decodes a value
	// through when its pointer implements one, in place of decoding field
	// by field. The decoder checks them in this order and calls the first
	// one the pointer implements.
	Interfaces = []reflect.Type{
		reflect.TypeFor[yaml.BytesUnmarshalerContext](),
		reflect.TypeFor[yaml.BytesUnmarshaler](),
		reflect.TypeFor[yaml.InterfaceUnmarshalerContext](),
		reflect.TypeFor[yaml.InterfaceUnmarshaler](),
		reflect.TypeFor[yaml.NodeUnmarshaler](),
		reflect.TypeFor[yaml.NodeUnmarshalerContext](),
		reflect.TypeFor[encoding.TextUnmarshaler](),
	}

	// TimeType is [time.Time]. The go-yaml decoder parses one from the
	// value of a scalar itself, ahead of its UnmarshalText method, and
	// reads a !!timestamp tag as one.
	TimeType = reflect.TypeFor[time.Time]()

	// DurationType is [time.Duration]. The go-yaml decoder parses one
	// from the text of a scalar and returns the error of
	// [time.ParseDuration] as it is.
	DurationType = reflect.TypeFor[time.Duration]()

	// MapSliceType is [yaml.MapSlice], which a mapping decodes into
	// under the ordered maps of the go-yaml decoder.
	MapSliceType = reflect.TypeFor[yaml.MapSlice]()

	// JSONType is the interface the go-yaml decoder decodes a value
	// through when a decode turns on UnmarshalJSON methods.
	JSONType = reflect.TypeFor[interface{ UnmarshalJSON(data []byte) error }]()

	// The import path of the go-yaml ast package, which declares the node
	// types go-yaml sets whole.
	astPackage = reflect.TypeFor[ast.StringNode]().PkgPath()

	// The result of [decoderField] for each type it has read, with -1 for
	// a type that has no such field.
	decoderFields TypeCache[int]

	// The result of [jsonDecoderField] for each type it has read.
	jsonDecoderFields TypeCache[int]

	// The result of [DecodesItself] for each type it has read.
	decodesWhole TypeCache[bool]

	// The result of [Options.InlinesUnmarshaler] for each struct type it
	// has read, in a decode whose options name no unmarshaler.
	inlineUnmarshalers TypeCache[bool]
)

// TypeCache holds a value computed once for each type it reads. A type
// never changes, so every decode shares the values. The zero TypeCache
// holds no value.
type TypeCache[V any] struct {
	values sync.Map
}

// Get returns the value of t. The first call for t runs compute and
// stores its result for the calls after it.
func (c *TypeCache[V]) Get(t reflect.Type, compute func(reflect.Type) V) V {
	if cached, ok := c.values.Load(t); ok {
		if v, ok := cached.(V); ok {
			return v
		}
	}

	v := compute(t)
	c.values.Store(t, v)

	return v
}

// Base returns the type t points to through every pointer on it, since
// the decoder decodes a pointer as the value it points to.
func Base(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	return t
}

// DecodesItself reports whether go-yaml decodes a value of type t whole,
// so the fields, elements, or entries of the value need not mirror the
// document. A type decodes itself through an unmarshaler method of its
// own, or as a node of the go-yaml ast package, as [IsSyntaxNode]
// reports. The method set of the pointer holds the methods of both
// receivers, as the decoder checks it.
func DecodesItself(t reflect.Type) bool {
	return decodesWhole.Get(t, func(t reflect.Type) bool {
		return IsSyntaxNode(t) || slices.ContainsFunc(Interfaces, reflect.PointerTo(t).Implements)
	})
}

// IsSyntaxNode reports whether t is an [ast.Node] type the go-yaml ast
// package declares, which the decoder sets to the node it decodes rather
// than decoding field by field. The tokens of such a node link to every
// other token of the file. A struct that gets the methods of an
// [ast.Node] from an embedded field decodes field by field.
func IsSyntaxNode(t reflect.Type) bool {
	return t.PkgPath() == astPackage && reflect.PointerTo(t).Implements(reflect.TypeFor[ast.Node]())
}

// Declares reports whether the pointer to t implements iface, an
// interface of one method, through a method t declares on its value or
// its pointer. A struct that gets the method from an embedded field does
// not declare it.
func Declares(t, iface reflect.Type) bool {
	return reflect.PointerTo(t).Implements(iface) && !promotesMethod(t, iface.Method(0).Name)
}

// ValueDecoder decodes text into dst for the function a decode option
// gives a type. The text is the YAML text the go-yaml decoder hands that
// function for one value, and dst is the pointer the function gave its
// decode callback.
type ValueDecoder func(ctx context.Context, text []byte, dst any) error

// Options holds the types that the options of one decode give an
// unmarshaler, where no method of the type declares one the go-yaml
// decoder calls on its own. A function decodes one type, and JSON turns
// on the UnmarshalJSON method of every type that has one.
//
// The decoder decodes a value of such a type whole, through that
// function or method, in place of its fields, elements, or entries. A
// walk that reads a type beside a document thus treats it as a type
// that decodes itself. The zero value gives no type an unmarshaler.
type Options struct {
	// The function of each type, with one entry for each type.
	custom []custom
	// JSON has the decoder decode a type through its UnmarshalJSON
	// method, when it has one.
	JSON bool
}

// custom is the function an option gives a type. The go-yaml decoder
// hands a function the text of a value, and the function reads the
// value through a decode. So the option holds a function that builds
// the go-yaml option for one decoder, from the [ValueDecoder] of the
// decode that decoder serves.
type custom struct {
	typ reflect.Type
	opt func(decode ValueDecoder) yaml.DecodeOption
}

// With returns o with the function that opt registers for the type t,
// in place of the one an earlier call gave t.
func (o Options) With(t reflect.Type, opt func(ValueDecoder) yaml.DecodeOption) Options {
	o.custom = slices.DeleteFunc(slices.Clone(o.custom), func(c custom) bool { return c.typ == t })
	o.custom = append(o.custom, custom{typ: t, opt: opt})

	return o
}

// Empty reports whether o gives no type an unmarshaler.
func (o Options) Empty() bool {
	return len(o.custom) == 0 && !o.JSON
}

// YAMLOptions returns the go-yaml options that register the
// unmarshalers with a decoder, whose functions read their values through
// decode.
func (o Options) YAMLOptions(decode ValueDecoder) []yaml.DecodeOption {
	opts := make([]yaml.DecodeOption, 0, len(o.custom)+1)

	for _, c := range o.custom {
		opts = append(opts, c.opt(decode))
	}

	if o.JSON {
		opts = append(opts, yaml.UseJSONUnmarshaler())
	}

	return opts
}

// Custom reports whether a function of o decodes a value of type t. The
// decoder calls that function ahead of any unmarshaler method of t.
func (o Options) Custom(t reflect.Type) bool {
	return slices.ContainsFunc(o.custom, func(c custom) bool { return c.typ == t })
}

// decodesJSON reports whether the decoder decodes a value of type t
// through its UnmarshalJSON method. It does under JSON, for a type with
// that method that has no function of o and no method [DecodesItself]
// reports, since the decoder looks for both of those first.
func (o Options) decodesJSON(t reflect.Type) bool {
	return o.JSON && !o.Custom(t) && !DecodesItself(t) && reflect.PointerTo(t).Implements(JSONType)
}

// Whole reports whether the decoder decodes a value of type t whole,
// through a method of t, as [DecodesItself] reports, or through an
// unmarshaler o gives t. The int result is the index of the embedded
// field of t whose method decodes t, which holds the document in place
// of t, or -1 when t has no such field. A type that declares its method
// has none, and neither does one a function of o decodes.
func (o Options) Whole(t reflect.Type) (int, bool) {
	switch {
	case o.Custom(t):
		return -1, true

	case DecodesItself(t):
		field, _ := decoderField(t)

		return field, true

	case o.decodesJSON(t):
		return jsonDecoderField(t), true

	default:
		return -1, false
	}
}

// ReportsOwnError reports whether the decoder can return an error for a
// value of type t with no token of the source. It can for a
// [time.Duration], for a type that decodes itself, as [DecodesItself]
// reads it, and for a type o gives an unmarshaler. A function from
// [yaml.RegisterCustomUnmarshaler] names a type the decoder never shows,
// so ReportsOwnError is false for a type only such a function decodes.
func (o Options) ReportsOwnError(t reflect.Type) bool {
	return t == DurationType || DecodesItself(t) || o.Custom(t) || o.decodesJSON(t)
}

// RefusesForText reports whether the decoder refuses content for a value
// of type t and calls no unmarshaler. The content is the mapping,
// sequence, or scalar the value reads.
//
// The decoder hands an UnmarshalText method the string it reads from a
// scalar. It reads no string from a mapping or a sequence, so it calls
// no method for one and returns an error of its own, with no token of
// the source. A type that [Options.FromText] reports thus takes a scalar
// alone. Two such types take a mapping or a sequence another way. The
// decoder parses a [time.Time] itself, and under JSON it hands the node
// to the UnmarshalJSON method of a type that has one.
func (o Options) RefusesForText(t reflect.Type, content ast.Node) bool {
	if astnode.IsNil(content) || astnode.IsScalar(content) || t == TimeType || !o.FromText(t) {
		return false
	}

	return !o.JSON || !reflect.PointerTo(t).Implements(JSONType)
}

// FromText reports whether the decoder decodes a scalar into a value of
// type t through the UnmarshalText method of the type. The decoder calls
// the first unmarshaler it finds on the pointer to the type, in the
// order of [Interfaces], so a type with an UnmarshalYAML method reports
// false. So does a type a function of o decodes, since the decoder calls
// that function ahead of any method. The decoder parses a [time.Time]
// itself, ahead of its UnmarshalText method, so a caller asks about a
// time before it asks FromText.
func (o Options) FromText(t reflect.Type) bool {
	if o.Custom(t) {
		return false
	}

	k := slices.IndexFunc(Interfaces, reflect.PointerTo(t).Implements)

	return k >= 0 && Interfaces[k] == reflect.TypeFor[encoding.TextUnmarshaler]()
}

// InlinesUnmarshaler reports whether t, a struct type, holds an inline
// field that decodes itself, as [Options.ReportsOwnError] lists those
// types, in its own fields or in those of a struct it holds inline. A
// decode of t calls the unmarshaler of that field whatever the mapping
// holds. The answer for an o that names a type holds for its decode
// alone, so the cache serves only an [Options.Empty] o.
func (o Options) InlinesUnmarshaler(t reflect.Type) bool {
	if !o.Empty() {
		return o.holdsInlineUnmarshaler(t, map[reflect.Type]bool{})
	}

	return inlineUnmarshalers.Get(t, func(t reflect.Type) bool {
		return o.holdsInlineUnmarshaler(t, map[reflect.Type]bool{})
	})
}

// holdsInlineUnmarshaler is [Options.InlinesUnmarshaler] without its
// cache. The seen set holds the structs the walk is inside of, so a
// struct that holds itself inline ends the walk.
func (o Options) holdsInlineUnmarshaler(t reflect.Type, seen map[reflect.Type]bool) bool {
	if seen[t] {
		return false
	}

	seen[t] = true

	for field := range t.Fields() {
		_, inline, skip := yamlfield.Name(field)
		if skip || !inline || yamlfield.ReadsAnchor(field) {
			continue
		}

		inner := Base(field.Type)
		if o.ReportsOwnError(inner) || inner.Kind() == reflect.Struct && o.holdsInlineUnmarshaler(inner, seen) {
			return true
		}
	}

	return false
}

// decoderField returns the index of the embedded field of t that decodes
// t, when t is a struct that gets its unmarshaler method from that field.
// Go-yaml checks the unmarshaler interfaces in a fixed order and calls
// the first method the pointer to t has, so an UnmarshalYAML that t gets
// from a field wins over an UnmarshalText that t declares. The method of
// the field decodes the document into the field, so the field stands at
// the path of the struct. When several embedded fields have the method,
// Go promotes it from the field that has it at the shallowest depth. The
// bool result is false when t declares the method go-yaml calls, or when
// no embedded field has that method.
func decoderField(t reflect.Type) (int, bool) {
	i := decoderFields.Get(t, findDecoderField)

	return i, i >= 0
}

// findDecoderField returns the index [decoderField] returns, or -1 when
// t has no such field.
func findDecoderField(t reflect.Type) int {
	k := slices.IndexFunc(Interfaces, reflect.PointerTo(t).Implements)
	if k < 0 {
		return -1
	}

	return promotingField(t, Interfaces[k])
}

// jsonDecoderField returns the index of the embedded field of t whose
// UnmarshalJSON method decodes t under JSON, or -1 when t declares the
// method or has none. It reads t as [decoderField] reads a type with an
// UnmarshalYAML method, and holds only for a type go-yaml decodes
// through UnmarshalJSON.
func jsonDecoderField(t reflect.Type) int {
	return jsonDecoderFields.Get(t, func(t reflect.Type) int {
		if !reflect.PointerTo(t).Implements(JSONType) {
			return -1
		}

		return promotingField(t, JSONType)
	})
}

// promotingField returns the index of the embedded field that gives t
// the one method of unmarshaler, an interface the pointer to t
// implements, or -1 when t declares that method itself.
func promotingField(t, unmarshaler reflect.Type) int {
	if t.Kind() != reflect.Struct || !hasEmbedded(t) {
		return -1
	}

	name := unmarshaler.Method(0).Name
	if !promotesMethod(t, name) {
		return -1
	}

	found, depth := -1, math.MaxInt

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

		// Two fields that have the method at the same depth would leave
		// it out of the method set of the pointer to t, so no other field
		// has it at the shallowest depth.
		if d := methodDepth(field.Type, name); d < depth {
			found, depth = i, d
		}
	}

	return found
}

// methodDepth returns how many embedded fields deep in t a type declares
// the method of the given name. It returns 0 when t declares the method
// itself, and [math.MaxInt] when the pointer to t has no such method. A
// pointer type counts as the type it points to. The search reads one
// depth at a time, so the first declaration it finds is the shallowest.
// It reads each type once, so it ends for a type that embeds a pointer
// to itself.
func methodDepth(t reflect.Type, name string) int {
	seen := map[reflect.Type]bool{}
	level := []reflect.Type{t}

	for depth := 0; len(level) > 0; depth++ {
		var next []reflect.Type

		for _, lt := range level {
			if lt.Kind() == reflect.Pointer {
				lt = lt.Elem()
			}

			if seen[lt] {
				continue
			}

			seen[lt] = true

			methods := lt
			if methods.Kind() != reflect.Interface {
				methods = reflect.PointerTo(methods)
			}

			if _, ok := methods.MethodByName(name); !ok {
				continue
			}

			if !promotesMethod(lt, name) {
				return depth
			}

			for field := range lt.Fields() {
				if field.Anonymous {
					next = append(next, field.Type)
				}
			}
		}

		level = next
	}

	return math.MaxInt
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
