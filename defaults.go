package niceyaml

import (
	"fmt"
	"reflect"

	"github.com/goccy/go-yaml/ast"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/unmarshaler"
	"go.jacobcolvin.com/niceyaml/internal/yamldecode"
)

// TypeDefaults creates a new [TypeRule] that gives the type T a function
// that sets its defaults. A decode starts each value of type T that it
// creates from those defaults, so a field the document leaves out keeps
// its default:
//
//	var rules = []niceyaml.TypeRule{
//		niceyaml.TypeDefaults(func(s *CafeSpec) {
//			s.Seats = 20
//			s.Kitchen.Port = 9101
//		}),
//		niceyaml.TypeDefaults(func(p *Printer) {
//			p.Port = 9100
//		}),
//	}
//
// The function gets a value whose struct fields hold the defaults of
// their own types, and changes what it needs. The function of a parent
// thus decides each value that it writes, and the defaults of each child
// hold elsewhere. In the example, the kitchen printer of a CafeSpec
// defaults to port 9101, and every other Printer to 9100. A later
// TypeDefaults for a type replaces an earlier one, and a nil fn removes
// the function that an earlier TypeRule or the [Source] gave T. The
// Defaults section of the package documentation describes which values a
// decode creates.
//
// A program that gives the rule to the Source with [WithTypeRules] has
// every decode of the documents start from the defaults, the decode
// inside a [Validator] included. Give defaults to the Source only when
// every decode of its documents must see them. A check that a value
// holds a key, or a step that writes decoded values back to the
// document, reads the defaults as if the document wrote them.
// TypeDefaults[T](nil) in [OverrideTypeRules] removes the function of T
// alone, and each struct field of T still takes the defaults of its own
// type. A call that must see the document without the defaults of the
// Source removes the function of each type that its value holds. A
// program whose own decodes alone take the defaults passes the rule to
// OverrideTypeRules in those decodes instead. A rule about the value that
// a decode returns, defaults included, belongs in [WithValueCheck] or in
// a Validate method, which see the defaults wherever they come from.
//
// A decode calls fn for each value it creates, so a map or a pointer
// that fn sets belongs to that value alone. A place at an alias can hold
// the value of its anchor when it starts from the value that the place of
// the anchor started from. The place then holds that map or pointer too,
// as the Defaults section of the package documentation describes. A place
// that starts from another value decodes the anchor again. A panic in fn
// makes the decode return an error that holds a [PanicError].
func TypeDefaults[T any](fn func(*T)) TypeRule {
	t := reflect.TypeFor[T]()

	if fn == nil {
		return TypeRule{apply: func(c *decodeConfig) {
			delete(c.defaults, t)
		}}
	}

	return TypeRule{apply: func(c *decodeConfig) {
		if c.defaults == nil {
			c.defaults = defaultFuncs{}
		}

		c.defaults[t] = func(v reflect.Value) {
			p, _ := reflect.TypeAssert[*T](v.Addr())
			fn(p)
		}
	}}
}

// Defaults returns the value of type T that a decode under rules starts
// from. The value starts as the zero value, with each struct field built
// from the defaults of its own type. The function that [TypeDefaults]
// gives T among rules then runs on it. A caller that fills its own value
// for [Node.DecodeInto] starts there:
//
//	spec := niceyaml.Defaults[CafeSpec](rules...)
//	if err := doc.DecodeInto(ctx, &spec, niceyaml.OverrideTypeRules(rules...)); err != nil {
//		return err
//	}
//
// [Source.Defaults] returns the value that a decode of the documents of
// a [Source] starts from, with the rules of the Source and those of the
// options of the call. A caller that holds its type as a runtime value,
// such as a [reflect.Type], starts from [DefaultsInto]. A pointer to a nil
// pointer also starts from the defaults, because DecodeInto allocates the
// value that the nil pointer points to from them. A null or empty
// document leaves that pointer nil, though, and Decode returns the
// defaults for it.
//
// A panic in a function of TypeDefaults reaches the caller.
func Defaults[T any](rules ...TypeRule) T {
	// A nil interface asserts to the zero value, which it is.
	v, _ := reflect.TypeAssert[T](typeRulesConfig(rules).defaults.value(reflect.TypeFor[T]()))

	return v
}

// DefaultsInto replaces the value that v points to with the value that
// [Defaults] returns for its type under rules. It does not merge, so a
// field that v held before takes its default too. A caller whose type is
// a runtime value, such as a [reflect.Type], uses DefaultsInto where it
// cannot name a type for Defaults:
//
//	v := reflect.New(t).Interface()
//	if err := niceyaml.DefaultsInto(v, rules...); err != nil {
//		return err
//	}
//
// For a v that is nil, a nil pointer, or not a pointer, DefaultsInto
// returns an error that wraps [ErrDecodeTarget] and leaves v as it is. A
// panic in a function of [TypeDefaults] reaches the caller.
func DefaultsInto(v any, rules ...TypeRule) error {
	return setDefaults(v, typeRulesConfig(rules))
}

// Defaults returns the value of type T that [Node.Decode] starts from for
// a document of the [Source] under opts. The rules of [WithTypeRules] on
// the Source apply first, and those of [OverrideTypeRules] among opts
// apply over them, as they do in the decode. A caller that fills its own
// value for [Node.DecodeInto] starts there:
//
//	spec := source.Defaults[CafeSpec](decoding)
//	if err := source.DecodeInto(ctx, &spec, decoding); err != nil {
//		return err
//	}
//
// The value starts as [Defaults] describes. A nil Source gives no rules
// of its own. A panic in a function of [TypeDefaults] reaches the caller.
func (s *Source) Defaults[T any](opts ...DecodeOption) T {
	// A nil interface asserts to the zero value, which it is.
	v, _ := reflect.TypeAssert[T](newDecodeConfig(s, opts).defaults.value(reflect.TypeFor[T]()))

	return v
}

// DefaultsInto replaces the value that v points to with the value that
// [Source.Defaults] returns for its type under opts. It does not merge, so
// a field that v held before takes its default too. [Node.DecodeInto]
// from the result matches [Node.Decode], as it does from Source.Defaults.
// A caller whose type is a runtime value, such as a [reflect.Type], uses
// DefaultsInto where it cannot name a type for Source.Defaults:
//
//	v := reflect.New(t).Interface()
//	if err := source.DefaultsInto(v, decoding); err != nil {
//		return err
//	}
//	if err := source.DecodeInto(ctx, v, decoding); err != nil {
//		return err
//	}
//
// For a v that is nil, a nil pointer, or not a pointer, DefaultsInto
// returns an error that wraps [ErrDecodeTarget] and leaves v as it is. A
// nil Source gives no rules of its own. A panic in a function of
// [TypeDefaults] reaches the caller.
func (s *Source) DefaultsInto(v any, opts ...DecodeOption) error {
	return setDefaults(v, newDecodeConfig(s, opts))
}

// setDefaults replaces the value that v points to with the defaults of its
// type under cfg, as [DefaultsInto] describes.
func setDefaults(v any, cfg decodeConfig) error {
	err := checkTarget(v, ErrDecodeTarget, true)
	if err != nil {
		return err
	}

	rv := reflect.ValueOf(v).Elem()
	rv.Set(cfg.defaults.value(rv.Type()))

	return nil
}

// defaultFuncs holds the function that [TypeDefaults] gives each type.
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

// reaches reports whether the defaults of t can hold a value other than
// zero. They can when [TypeDefaults] gives t a function, or when t is a
// struct and the defaults of one of its fields can, as
// [defaultFuncs.fillFields] sets them. The known map holds the answer for
// each type that reaches has seen.
func (d defaultFuncs) reaches(t reflect.Type, known map[reflect.Type]bool) bool {
	if found, ok := known[t]; ok {
		return found
	}

	_, found := d[t]

	if !found && t.Kind() == reflect.Struct {
		for field := range t.Fields() {
			if (field.IsExported() || field.Anonymous) && d.reaches(field.Type, known) {
				found = true

				break
			}
		}
	}

	known[t] = found

	return found
}

// startOption returns the [yamldecode.StartValue] option that starts each
// value that a decode creates from the defaults of its type. Without
// defaults, each such value starts from zero. A panic in a
// function of [TypeDefaults] panics again with a [*defaultsPanic], which
// [decodeWithRecover] turns into an error at the node of the value.
func (d defaultFuncs) startOption() yamldecode.DecodeOption {
	known := map[reflect.Type]bool{}

	return yamldecode.StartValue(func(t reflect.Type, node ast.Node) reflect.Value {
		if !d.reaches(t, known) {
			return reflect.Value{}
		}

		defer func() {
			if p := recover(); p != nil {
				panic(&defaultsPanic{typ: t, node: node, err: recovered(p)})
			}
		}()

		return d.value(t)
	})
}

// defaultsPanic carries a panic in the function that [TypeDefaults] gave
// the type typ, out of the decoder that asked for the defaults of typ for
// a value that it reads node into.
type defaultsPanic struct {
	typ  reflect.Type
	node ast.Node
	err  *PanicError
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
// from node. It returns a panic in a function of [TypeDefaults] as an
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

			err = Unchecked(panicked(fmt.Sprintf("the defaults of %s", t), recovered(p), errPlaced),
				AtToken(astnode.StartToken(node)))
		}()

		v = d.value(t)
	}()

	return v, err
}

// startTarget returns the pointer that a decode of node into v fills. The
// pointer is the last of the chain of pointers that v, a non-nil pointer,
// reaches. Along the chain, startTarget sets each nil pointer to a new
// value, so the decode fills the value at the end of the chain in place.
// When v held a nil pointer, that value starts from the defaults of its
// type. For a pointer type that never ends in a type that is not a
// pointer, such as "type P *P", startTarget returns v, and the decoder
// rejects the type. For a v that points to a pointer to a node type of
// the go-yaml ast package, startTarget also returns v. The decoder points
// that pointer to the node of the document.
func startTarget(v any, cfg decodeConfig, node ast.Node) (any, error) {
	t := reflect.TypeOf(v).Elem()
	if unmarshaler.Base(reflect.TypeOf(v)).Kind() == reflect.Pointer ||
		t.Kind() == reflect.Pointer && unmarshaler.IsSyntaxNode(t.Elem()) {
		return v, nil
	}

	created := false
	target := reflect.ValueOf(v)

	for elem := target.Elem(); elem.Kind() == reflect.Pointer; elem = target.Elem() {
		if elem.IsNil() {
			created = true

			elem.Set(reflect.New(elem.Type().Elem()))
		}

		target = elem
	}

	if created {
		tv := target.Elem()

		start, err := cfg.defaults.start(tv.Type(), node)
		if err != nil {
			return nil, err
		}

		tv.Set(start)
	}

	return target.Interface(), nil
}
