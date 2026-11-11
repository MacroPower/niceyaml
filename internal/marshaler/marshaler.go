// Package marshaler holds the marshalers that the options of one encode
// give the types of a program, beside the methods that the go-yaml
// encoder calls on its own.
//
// A function for one type writes each value of that type as another
// value, which the encoder writes in its place. The MarshalJSON method of
// a type writes the type when the options turn those methods on. [Options]
// holds both, and it builds the go-yaml options that register them with
// an encoder.
package marshaler

import (
	"context"
	"reflect"
	"slices"

	"github.com/goccy/go-yaml"
)

// Func returns the value that the encoder writes in place of v, a value of
// the type of the function, or of a pointer to that type.
type Func func(ctx context.Context, v any) (any, error)

// ValueEncoder returns the YAML text of v under the options of the encode
// that calls it, for the go-yaml option of a [Func]. The go-yaml encoder
// reads that text as the YAML that a marshaler of the value returns.
type ValueEncoder func(ctx context.Context, v any) ([]byte, error)

// Options names the types that the options of one encode give a
// marshaler that the go-yaml encoder does not call on its own. A function
// that [Options.With] registers writes one type, and JSON has the encoder
// call the MarshalJSON method of every type that has one. The zero value
// does not give any type a marshaler.
type Options struct {
	// The function of each type, with one entry for each type.
	custom []custom
	// JSON has the encoder write a type through its MarshalJSON method,
	// when it has one.
	JSON bool
}

// custom is the function that an option gives a type, with the go-yaml
// options that register it for the type and for a pointer to the type.
type custom struct {
	typ  reflect.Type
	fn   Func
	opts func(encode ValueEncoder) []yaml.EncodeOption
}

// With returns o with fn for the type t, in place of the function that an
// earlier call gave t. The go-yaml options of opts register fn with an
// encoder.
func (o Options) With(t reflect.Type, fn Func, opts func(ValueEncoder) []yaml.EncodeOption) Options {
	o = o.Without(t)
	o.custom = append(o.custom, custom{typ: t, fn: fn, opts: opts})

	return o
}

// Without returns o without a function for the type t, so the encoder
// writes t as it writes a type that the options do not name.
func (o Options) Without(t reflect.Type) Options {
	o.custom = slices.DeleteFunc(slices.Clone(o.custom), func(c custom) bool { return c.typ == t })

	return o
}

// Lookup returns the function that o gives a value of the type t. The
// go-yaml encoder looks it up by t itself, which is the type of a function
// or a pointer to that type. A value of an interface type does not find the
// function of the type that it holds. The encoder finds that function at
// the value inside it.
func (o Options) Lookup(t reflect.Type) (Func, bool) {
	for _, c := range o.custom {
		if c.typ == t || reflect.PointerTo(c.typ) == t {
			return c.fn, true
		}
	}

	return nil, false
}

// YAMLOptions returns the go-yaml options that register the marshalers
// with an encoder. The functions of those options write the values that
// they return through encode.
func (o Options) YAMLOptions(encode ValueEncoder) []yaml.EncodeOption {
	var opts []yaml.EncodeOption

	for _, c := range o.custom {
		opts = append(opts, c.opts(encode)...)
	}

	if o.JSON {
		opts = append(opts, yaml.UseJSONMarshaler())
	}

	return opts
}
