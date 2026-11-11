package niceyaml

import (
	"context"
	"fmt"
	"reflect"
	"runtime/debug"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/decodewalk"
)

// WithDefaults is a [DecodeOption] that gives the type T a function that
// sets its defaults. A decode starts each value of type T that it creates
// from those defaults, so a field the document leaves out keeps its
// default:
//
//	var decoding = niceyaml.DecodeOptions(
//		niceyaml.WithDefaults(func(s *CafeSpec) {
//			s.Seats = 20
//			s.Kitchen.Port = 9101
//		}),
//		niceyaml.WithDefaults(func(p *Printer) {
//			p.Port = 9100
//		}),
//	)
//
// The function gets a value whose struct fields hold the defaults of
// their own types, and changes what it needs. The function of a parent
// thus decides each value that it writes, and the defaults of each child
// hold elsewhere. In the example, the kitchen printer of a CafeSpec
// defaults to port 9101, and every other Printer to 9100. A later
// WithDefaults for a type replaces an earlier one. The Defaults section of
// the package documentation describes which values a decode creates.
//
// A decode calls fn once for each value it creates, so a map or a
// pointer that fn sets belongs to that value alone. A panic in fn makes
// the decode return an error that holds a [PanicError].
func WithDefaults[T any](fn func(*T)) DecodeOption {
	t := reflect.TypeFor[T]()

	return func(c *decodeConfig) {
		if c.defaults == nil {
			c.defaults = defaultFuncs{}
		}

		c.defaults[t] = func(v reflect.Value) {
			p, _ := reflect.TypeAssert[*T](v.Addr())
			fn(p)
		}
	}
}

// Defaults returns the value of type T that [Node.Decode] starts from with
// opts. The value starts as the zero value, with each struct field built
// from the defaults of its own type. The function that [WithDefaults]
// gives T then runs on it. A caller that fills its own value for
// [Node.DecodeInto] starts there:
//
//	spec := niceyaml.Defaults[CafeSpec](decoding)
//	if err := doc.DecodeInto(ctx, &spec, decoding); err != nil {
//		return err
//	}
//
// A caller that holds its type as a runtime value, such as a
// [reflect.Type], starts from [DefaultsInto]. A pointer to a nil pointer
// also starts from the defaults, because DecodeInto allocates the value
// that the nil pointer points to from them. A null or empty document
// leaves that pointer nil, though, and Decode returns the defaults for it.
//
// A panic in a function of WithDefaults reaches the caller.
func Defaults[T any](opts ...DecodeOption) T {
	// A nil interface asserts to the zero value, which it is.
	v, _ := reflect.TypeAssert[T](newDecodeConfig(nil, opts).defaults.value(reflect.TypeFor[T]()))

	return v
}

// DefaultsInto replaces the value that v points to with the value that
// [Defaults] returns for its type under opts. It does not merge, so a
// field that v held before takes its default too. [Node.DecodeInto] from
// the result matches [Node.Decode], as it does from Defaults. A caller
// whose type is a runtime value, such as a [reflect.Type], uses
// DefaultsInto where it cannot name a type for Defaults:
//
//	v := reflect.New(t).Interface()
//	if err := niceyaml.DefaultsInto(v, decoding); err != nil {
//		return err
//	}
//	if err := doc.DecodeInto(ctx, v, decoding); err != nil {
//		return err
//	}
//
// For a v that is nil, a nil pointer, or not a pointer, DefaultsInto
// returns an error that wraps [ErrDecodeTarget] and leaves v as it is. A
// panic in a function of [WithDefaults] reaches the caller.
func DefaultsInto(v any, opts ...DecodeOption) error {
	err := checkTarget(v, ErrDecodeTarget, true)
	if err != nil {
		return err
	}

	rv := reflect.ValueOf(v).Elem()
	rv.Set(newDecodeConfig(nil, opts).defaults.value(rv.Type()))

	return nil
}

// defaultFuncs holds the function that [WithDefaults] gives each type.
type defaultFuncs map[reflect.Type]func(reflect.Value)

// value returns the defaults of t. It starts from the zero value and sets
// each struct field to the defaults of the type of the field. It then
// runs the function of t on the result. The value is addressable.
func (d defaultFuncs) value(t reflect.Type) reflect.Value {
	v := reflect.New(t).Elem()
	if len(d) == 0 {
		return v
	}

	d.fillFields(v)

	if fn, ok := d[t]; ok {
		fn(v)
	}

	return v
}

// sets reports whether [WithDefaults] gives t a function. The defaults
// of any other type hold only the defaults of the types of its fields.
func (d defaultFuncs) sets(t reflect.Type) bool {
	_, ok := d[t]

	return ok
}

// fillFields sets each field of v to the defaults of the type of the
// field, when v is a struct. An unexported embedded field cannot take a
// value whole, so fillFields sets each of its own fields instead.
func (d defaultFuncs) fillFields(v reflect.Value) {
	if v.Kind() != reflect.Struct {
		return
	}

	for i := range v.NumField() {
		field := v.Field(i)

		switch {
		case field.CanSet():
			if fv := d.value(field.Type()); !fv.IsZero() {
				field.Set(fv)
			}

		case v.Type().Field(i).Anonymous:
			d.fillFields(field)
		}
	}
}

// start returns the defaults of t for a decode that creates a value of t
// from node. It returns a panic in a function of [WithDefaults] as an
// error at node that holds a [PanicError].
func (d defaultFuncs) start(t reflect.Type, node ast.Node) (reflect.Value, error) {
	var (
		v   reflect.Value
		err error
	)

	func() {
		defer func() {
			p := recover()
			if p == nil {
				return
			}

			stack := &PanicError{Value: p, Stack: debug.Stack()}
			err = Unchecked(panicked(fmt.Sprintf("the defaults of %s", t), stack, errPlaced),
				atToken(astnode.StartToken(node)))
		}()

		v = d.value(t)
	}()

	return v, err
}

// startTarget allocates each nil pointer below v, a non-nil pointer, as
// [allocate] does. It returns the last pointer and the value that the
// decode starts from there. When v held a nil pointer, the start is the
// defaults of its type. Otherwise the start is a copy of the value that v
// reaches, because the decoder overwrites that value. The start is
// invalid past [maxPointerDepth] pointers.
func startTarget(v any, cfg decodeConfig, node ast.Node) (any, reflect.Value, error) {
	created := false

	for rv := reflect.ValueOf(v).Elem(); rv.Kind() == reflect.Pointer; rv = rv.Elem() {
		if rv.IsNil() {
			created = true

			break
		}
	}

	target := allocate(v)

	tv := reflect.ValueOf(target).Elem()
	if tv.Kind() == reflect.Pointer {
		return target, reflect.Value{}, nil
	}

	if !created {
		start := reflect.New(tv.Type()).Elem()
		start.Set(tv)

		return target, start, nil
	}

	start, err := cfg.defaults.start(tv.Type(), node)
	if err != nil {
		return nil, reflect.Value{}, err
	}

	tv.Set(start)

	return target, start, nil
}

// fillDefaults builds again each value that a decode of node into target
// created, from the start of its type, as [decodewalk.Fill] describes.
// The value that target points to started from start. The fill decodes
// values again, as [withoutWarnings] describes. Each such decode reads
// what the decoder read, with the cut of the known keys of the decode.
func (n *Node) fillDefaults(
	ctx context.Context,
	dec *yaml.Decoder,
	cfg decodeConfig,
	node ast.Node,
	target any,
	start reflect.Value,
	cut keyCut,
) error {
	tv := reflect.ValueOf(target).Elem()

	starts := decodewalk.Starts{Of: cfg.defaults.start, Sets: cfg.defaults.sets}

	rules := n.walkRules(withoutWarnings(ctx), dec, cfg.unmarshalers)
	rules.View = cut.readView(rules.View)

	filled, err := decodewalk.Fill(ctx, rules, starts, tv.Type(), node, start, tv)
	if err != nil {
		return err //nolint:wrapcheck // The caller places the error.
	}

	tv.Set(filled)

	return nil
}
