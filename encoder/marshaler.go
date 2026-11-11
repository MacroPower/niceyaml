package encoder

import (
	"context"
	"fmt"
	"reflect"
	"slices"

	"github.com/goccy/go-yaml"

	"go.jacobcolvin.com/niceyaml/internal/marshaler"
)

// Marshaler sets how the types of the program write YAML. It is the
// counterpart of [go.jacobcolvin.com/niceyaml.Unmarshaler].
// [CustomMarshaler] gives one type a function, and [JSONMarshalers] turns
// the MarshalJSON methods of the types on or off.
//
// An encode takes marshalers with [WithMarshalers]. The edits of a
// [go.jacobcolvin.com/niceyaml/edit.Doc] take them with
// [go.jacobcolvin.com/niceyaml/edit.WithMarshalers], and the migration
// steps of a manifest registry take them with
// [go.jacobcolvin.com/niceyaml/manifest.WithMarshalers]. Each place takes
// the same value:
//
//	cidr := encoder.CustomMarshaler(func(ctx context.Context, n net.IPNet) (any, error) {
//		return n.String(), nil
//	})
//
//	data, err := encoder.Marshal(ctx, cfg, encoder.WithMarshalers(cidr))
//	d, err := edit.New(doc, edit.WithMarshalers(cidr))
//
// An encode applies its marshalers in order. A function for a type
// replaces the function that an earlier Marshaler gave that type, and a
// nil function removes it. The last JSONMarshalers applies. The zero
// Marshaler does not change anything.
//
// Create instances with [CustomMarshaler] or [JSONMarshalers].
type Marshaler struct {
	// Sets the marshaler in the settings of an encode, or nil for the
	// zero Marshaler.
	apply func(*marshaler.Options)
}

// applyTo sets the marshaler of m in o.
func (m Marshaler) applyTo(o *marshaler.Options) {
	if m.apply != nil {
		m.apply(o)
	}
}

// CustomMarshaler creates a new [Marshaler] that writes every value of
// type T through fn, for a type the program cannot give a MarshalYAML
// method, such as one of another package. The function returns the value
// that the encoder writes in place of the value of T. The encoder writes
// that value with the same options and marshalers. The cidr
// Marshaler writes a [net.IPNet] as its CIDR text:
//
//	cidr := encoder.CustomMarshaler(func(ctx context.Context, n net.IPNet) (any, error) {
//		return n.String(), nil
//	})
//
// The encoder calls fn ahead of any marshaler method of T. A value of type
// *T writes through fn too, and a nil *T writes as null without a call of
// fn. A value of T inside an interface, such as an entry of a
// map[string]any, writes by the MarshalYAML or MarshalText method of T
// when T has one, and by its MarshalJSON method under
// [JSONMarshalers](true). A [time.Duration] there writes as go-yaml
// writes a duration. Those rules are the rules of go-yaml's encoder.
//
// The function gets the context of the encode. An error that fn returns
// fails the encode, and the error matches [ErrEncode]. The encoder writes
// the value that fn returns as text and places that text as it places the
// YAML that a marshaler returns, as [Encoder.Encode] describes. A value of
// type T or *T from fn fails the encode, because the encoder writes such a
// value through fn again, without end.
//
// The encoder writes each value of fn as text and parses that text again.
// A value of fn that holds further values of functions thus goes through
// that step once for each function that encloses it. A deep recursive
// type, such as a long linked list, encodes far slower through fn than
// through a MarshalYAML method that returns the same value. Give such a
// type that method instead.
//
// A nil fn removes the function that an earlier Marshaler gave the type.
// Give the function to CustomMarshaler rather than to
// [yaml.RegisterCustomMarshaler], which changes every encode of the
// process.
//
// CustomMarshaler panics for a pointer type T. The encoder writes a *T
// through T, so the function for T already writes a pointer to T. It also
// panics for an interface type T, such as any or [fmt.Stringer]. Go-yaml's
// encoder registers a function under the type of a zero T, and a zero
// interface value has no type, so the encoder never finds the function.
func CustomMarshaler[T any](fn func(ctx context.Context, v T) (any, error)) Marshaler {
	t := reflect.TypeFor[T]()

	if t.Kind() == reflect.Pointer {
		panic(fmt.Sprintf("encoder.CustomMarshaler: %v is a pointer type, "+
			"which the encoder writes through the type it points to", t))
	}

	if t.Kind() == reflect.Interface {
		panic(fmt.Sprintf("encoder.CustomMarshaler: %v is an interface type, "+
			"for which the encoder cannot register a function", t))
	}

	if fn == nil {
		return Marshaler{apply: func(o *marshaler.Options) {
			*o = o.Without(t)
		}}
	}

	call := func(ctx context.Context, v T) (any, error) {
		out, err := fn(ctx, v)
		if err != nil {
			return nil, err
		}

		// The encoder writes a T through fn again, so the encode never ends.
		if ot := reflect.TypeOf(out); ot != nil && (ot == t || ot == reflect.PointerTo(t)) {
			return nil, fmt.Errorf("the function for %v returns a value of type %T, which it writes again", t, out)
		}

		return out, nil
	}

	write := func(ctx context.Context, v any) (any, error) {
		if p, ok := v.(*T); ok {
			return call(ctx, *p)
		}

		tv, _ := v.(T) //nolint:errcheck // The lookup finds the function for a T or a *T alone.

		return call(ctx, tv)
	}

	opts := func(encode marshaler.ValueEncoder) []yaml.EncodeOption {
		text := func(ctx context.Context, v any) ([]byte, error) {
			out, err := write(ctx, v)
			if err != nil {
				return nil, err
			}

			return encode(ctx, out)
		}

		// Go-yaml looks a marshaler up by the type of the value alone. It
		// checks the methods of a *T before it reads the T behind it.
		return []yaml.EncodeOption{
			yaml.CustomMarshalerContext(func(ctx context.Context, v T) ([]byte, error) { return text(ctx, v) }),
			yaml.CustomMarshalerContext(func(ctx context.Context, v *T) ([]byte, error) { return text(ctx, v) }),
		}
	}

	return Marshaler{apply: func(o *marshaler.Options) {
		*o = o.With(t, write, opts)
	}}
}

// JSONMarshalers creates a new [Marshaler] that sets whether a type with a
// MarshalJSON method writes through that method. Without
// JSONMarshalers(true), an encode writes such a type by its fields,
// elements, or entries, as it writes a type without the method.
//
// With the methods on, the encoder converts the JSON of the method to YAML
// and places that YAML as it places the YAML that a marshaler returns. It
// calls a MarshalYAML or a MarshalText method of the type in place of
// MarshalJSON.
//
// Go-yaml converts the JSON to YAML, and some JSON values do not survive
// that conversion, such as:
//
//   - A string of one line break comes out as a block that reads as "".
//   - A string with a tab at its start or end reads back without the tab.
//   - A string with a carriage return reads back with a line feed in its
//     place.
//   - A string that starts with a YAML indicator, such as "? x", reads as a
//     mapping.
//   - A string that holds only a document marker, such as "---", or only a
//     tab converts to YAML without a value.
//   - A number in exponent form without a decimal point, such as 1e3 or
//     the 1e-07 that encoding/json writes for a small float64, reads as a
//     string.
//   - An integer outside the range of 64 bits comes out as a quoted
//     string.
//   - An object key "<<" comes out as a merge key, so its object merges
//     into the object that holds it.
//
// The encoder writes the value that go-yaml reads from the converted YAML,
// which can differ from the value that the JSON holds. The encode fails
// with [ErrEncode] when go-yaml reads no value from that YAML or refuses
// it. The error matches [ErrNoNode] when go-yaml reads no value.
// [go.jacobcolvin.com/niceyaml/edit.Node.Set] refuses each such value.
//
// A program whose types write their documents through
// MarshalJSON, such as Kubernetes types, gives JSONMarshalers(true) to
// each encode and edit that writes them, beside the
// [go.jacobcolvin.com/niceyaml.JSONUnmarshalers] that reads them.
func JSONMarshalers(enabled bool) Marshaler {
	return Marshaler{apply: func(o *marshaler.Options) {
		o.JSON = enabled
	}}
}

// WithMarshalers is an [Option] that applies the marshalers ms to the
// encode, in order, after the marshalers of the options before it. A
// program that writes its types through functions or MarshalJSON methods
// passes the same marshalers to each encode:
//
//	data, err := encoder.Marshal(ctx, cfg, encoder.WithMarshalers(cidr, encoder.JSONMarshalers(true)))
//
// A function that the marshalers give a type applies ahead of a
// [yaml.CustomMarshaler] for that type from [WithYAMLOptions].
// WithMarshalers copies the slice ms, so the option keeps its marshalers
// when the caller changes the slice it passed.
func WithMarshalers(ms ...Marshaler) Option {
	ms = slices.Clone(ms)

	return func(c *config) {
		for _, m := range ms {
			m.applyTo(&c.marshalers)
		}
	}
}
