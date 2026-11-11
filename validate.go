package niceyaml

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"unsafe"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/datapath"
	"go.jacobcolvin.com/niceyaml/internal/multierr"
	"go.jacobcolvin.com/niceyaml/internal/nilness"
	"go.jacobcolvin.com/niceyaml/internal/unmarshaler"
	"go.jacobcolvin.com/niceyaml/internal/yamlfield"
	"go.jacobcolvin.com/niceyaml/paths"
)

// SelfValidate runs the self-validation step of [Node.DecodeInto] on its own.
// It walks v and calls Validate on every value in it that implements
// [SelfValidator]. It calls ValidateContext with ctx on every value that
// implements [SelfValidatorContext]. It also runs each function of
// [WithValueCheck] in opts on the values of its type. It binds what they
// report as [Node.Bind] binds an error, with each `@` path under the path of
// its value in the node. It returns nil when every check passes. It runs the
// walk whatever [WithSelfValidation] says, and it does not run a [Validator].
// A program that fills v after the decode, such as from its environment, calls
// SelfValidate after v is complete, as the Validation section of the package
// documentation describes:
//
//	if err := doc.DecodeInto(ctx, &cfg, settings, niceyaml.WithSelfValidation(false)); err != nil {
//		return err
//	}
//
//	applyEnv(&cfg)
//
//	return doc.SelfValidate(ctx, &cfg, settings)
//
// On a value that did not change after the decode, SelfValidate returns
// what the decode with the walk on returns, given the same functions of
// WithValueCheck. To spell each map key as the document does, the walk
// decodes the keys with the settings of the [Source], the unmarshalers of
// [WithUnmarshalers] among them. A program that gives its unmarshalers to
// the Source thus does not pass them here. The decodes of the keys drop the
// warnings that every unmarshaler they run hands to its context.
//
// The walk also applies the options in opts that say how a value decodes:
// [WithUnmarshaler], [WithUnknownFields], [WithAllowedFieldPrefixes], and
// [WithYAMLOrderedMaps]. It does not read any other option. The walk does
// not report an unknown field, even under [UnknownFieldsWarn]. A caller
// passes the options of the decode again.
//
// In a document that did not parse, every error binds without a position.
// SelfValidate returns those errors rather than the error that [Node.Err]
// returns. Any v works except nil and a nil pointer. For those two,
// SelfValidate returns an error wrapping [ErrSelfValidateTarget], bound to
// the source. A v that is not a pointer validates as a copy. The walk stops
// when ctx ends, as the Cancellation section of the package documentation
// describes.
func (n *Node) SelfValidate(ctx context.Context, v any, opts ...DecodeOption) error {
	return n.selfValidate(ctx, v, newDecodeConfig(n.source, opts))
}

// selfValidate is [Node.SelfValidate] with its settings resolved.
// [Node.DecodeInto] runs it after the decode fills v, so a decode and a
// later call of SelfValidate on the same value return the same error.
func (n *Node) selfValidate(ctx context.Context, v any, cfg decodeConfig) error {
	err := checkTarget(v, ErrSelfValidateTarget, false)
	if err != nil {
		return n.bindOwn(err)
	}

	failure, warnings := walkSelfValidators(ctx, v, n, cfg)

	deliver(ctx, n.Bind(warnings))

	return n.Bind(failure)
}

// SelfValidateValue runs the self-validation step of [Node.DecodeInto]
// on a value that did not come from a document, such as one the program
// built or the body of a request. It walks v as [Node.SelfValidate] does
// and binds the result as [BindValue] binds an error. The text thus names
// each failing path from v, as in "$.servers[1].port: port is required",
// and a document can still place the error. It returns nil when every
// check passes.
//
// The walk names a field as a YAML decode names it, by its yaml tag, its
// json tag, or its lowercased name. A map key takes the text of its Go
// value. When a document spells the key another way, such as 0x10 for
// 16, an error below the entry binds at the key of the map. A caller that
// holds the document validates through the Node of the value with
// Node.SelfValidate instead. Several keys of one map that share a text,
// such as 1 and "1", bind without a position, as [ErrAmbiguousPath]
// describes.
//
// SelfValidateValue runs each function of [WithValueCheck] in opts on the
// values of its type, as Node.SelfValidate does. A server thus checks the
// body of a request with the functions it gives a decode. SelfValidateValue
// recognizes a type that decodes itself through its own unmarshaler
// method or through an unmarshaler of [WithUnmarshaler] in opts. It does
// not read any other option. The value does not have a [Source], so the
// unmarshalers of [WithUnmarshalers] do not reach the walk. A caller
// passes them in opts with WithUnmarshaler instead.
//
// A ValidateContext method of a [SelfValidatorContext] whose value holds
// fields a decode does not reach, such as one tagged `yaml:"-"`,
// validates them with SelfValidateValue and its own context. It passes a
// struct that holds each field under its name, so each path of the call
// reads from the value of the method. The walk then places the errors that
// the method returns and the warnings that the call hands to the context:
//
//	func (p Proxy) ValidateContext(ctx context.Context) error {
//		return niceyaml.SelfValidateValue(ctx, struct {
//			Upstreams []Upstream `yaml:"upstreams"`
//		}{p.Upstreams})
//	}
//
// The walk places each warning that the method hands to its context at
// the value of the method, under the path that the warning names. A
// [Warnings] that the method derives from its context hands each warning
// to the walk as well. So a method that passes p.Upstreams alone and
// moves the result under its path with [Rebase] reports each warning
// twice, and one of the two names the wrong path.
//
// A Validate method of a [SelfValidator] does not get a context, so it
// collects the warnings of the call with a Warnings of its own. It
// returns them beside its errors, and the walk places both:
//
//	warnings := niceyaml.NewWarnings()
//	err := niceyaml.SelfValidateValue(warnings.Context(context.Background()), p.Upstreams)
//
//	return niceyaml.Rebase(errors.Join(err, warnings.Err()), paths.Current().Child("upstreams"))
//
// A method must not pass its own receiver to SelfValidateValue, because
// the call then runs that method again without end. A method that passes
// a field the decode reaches reports each error of that field twice,
// because the walk validates that field too.
//
// Any v works except nil and a nil pointer, for which SelfValidateValue
// returns an error wrapping [ErrSelfValidateTarget]. The walk stops when
// ctx ends, as the Cancellation section of the package documentation
// describes.
func SelfValidateValue(ctx context.Context, v any, opts ...DecodeOption) error {
	err := checkTarget(v, ErrSelfValidateTarget, false)
	if err != nil {
		return BindValue(err)
	}

	failure, warnings := walkSelfValidators(ctx, v, nil, newDecodeConfig(nil, opts))

	deliver(ctx, BindValue(warnings))

	return BindValue(failure)
}

// walkSelfValidators runs the method of every [SelfValidator] and
// [SelfValidatorContext] in v, which is a non-nil pointer or a value that is
// not a pointer. It also runs the functions of cfg from [WithValueCheck] on
// the values of their types. It joins the errors under the path of each value
// in the document of n, and it returns the failures and then the warnings. A
// nil n stands for a value outside any document. The warnings are those that
// each check returns and those that each check hands to its context,
// whether the check passes or fails.
func walkSelfValidators(ctx context.Context, v any, n *Node, cfg decodeConfig) (error, error) {
	w := selfWalker{
		ctx:      ctx,
		keyCtx:   withoutWarnings(ctx),
		node:     n,
		cfg:      cfg,
		checked:  checkedTypesOf(cfg.checks),
		walked:   map[visit]bool{},
		scanning: map[visit]bool{},
		scanned:  map[visit]bool{},
	}
	w.walk(reflect.ValueOf(v), place{}, nil)

	if w.ended != nil {
		return w.ended, multierr.Join(w.warns)
	}

	return multierr.Join(w.errs), multierr.Join(w.warns)
}

// selfWalker collects the errors of the [SelfValidator] and
// [SelfValidatorContext] values in a decoded value, and of the functions from
// [WithValueCheck]. It runs every check on every value it reaches, whatever
// the other checks return. It walks each pointer, map, and slice once. A value
// that two paths share thus reports its errors under the first path, and the
// walk does not follow a back edge of a cycle.
type selfWalker struct {
	ctx context.Context
	// The context of the decodes that spell the map keys, which drops
	// their warnings, as [withoutWarnings] describes.
	keyCtx  context.Context
	node    *Node
	decoder *yaml.Decoder
	// The types that the functions of cfg check. The walk skips each value
	// that does not hold a validator, and it counts these types as
	// validators for that test.
	checked *checkedTypes
	// The pointers, maps, and slices that the walk has entered.
	walked map[visit]bool
	// The pointers, maps, and slices that the current scan has reached, so
	// the scan reads each once however many paths lead to it.
	scanning map[visit]bool
	// The result of the scan of each pointer, map, and slice. The result
	// for a pointer covers the value it points to, and the result for a
	// map or slice covers only the values below it.
	scanned map[visit]bool
	errs    []error
	// The warnings of the checks, which [selfWalker.report] takes out of
	// their results.
	warns []error
	// The error of ctx, which is nil until [selfWalker.stopped] finds that
	// ctx ended.
	ended error
	// The places of the values that the walk is inside of and that decode
	// themselves, the outermost first, as [selfWalker.enter] records them.
	within []place
	// The node of the value the walk starts at, held as a [step] holds the
	// node of its own value, after [selfWalker.nodeOf] resolves it.
	start step
	// The settings of the decode, which name the types its options give
	// an unmarshaler and hold the functions from [WithValueCheck].
	cfg decodeConfig
	// Whether the walk is below a map entry whose path names the entries
	// of several keys, as [selfWalker.walkEntries] finds one. The path of
	// each error the walk collects there is ambiguous.
	ambiguous bool
	// Whether the last of within is a slice, an array, or a map. The paths
	// below it then do not say where its value sits in the document.
	sealed bool
}

// visit names a pointer, map, or slice by type and address, because a
// struct and its first field share an address. It also names it by
// length, because two slices of one type can share an address too.
//
//nolint:unused // The fields tell the keys of the walker's maps apart.
type visit struct {
	typ reflect.Type
	ptr unsafe.Pointer
	len int
}

// place is where a value of the walk lies, held as the last [step] below
// the start. The zero place stands for the start. A [paths.Path] copies
// its selectors as it grows, so a place builds a path only to rebase an
// error.
type place struct {
	last *step
}

// step is one selector on the way to a value of the walk, and key marks a
// `~` selector. It keeps the node that [selfWalker.nodeOf] resolves. When
// resolved is true, a nil node means that the document did not set the
// value. When resolved is false, nodeOf has not resolved the step yet.
type step struct {
	prev     *step
	node     ast.Node
	selector paths.Path
	key      bool
	resolved bool
}

// child returns the place of the field or map entry name of the value
// at p.
func (p place) child(name string) place {
	return p.then(paths.Current().Child(name), false)
}

// index returns the place of element i of the value at p.
func (p place) index(i int) place {
	return p.then(paths.Current().Index(i), false)
}

// key returns the place of the key of the map entry at p.
func (p place) key() place {
	return p.then(paths.Current().Key(), true)
}

// then returns the place one step below p, through selector, which is a
// `~` when key is true.
func (p place) then(selector paths.Path, key bool) place {
	return place{last: &step{prev: p.last, selector: selector, key: key}}
}

// path returns the path of the value at p under the [Node] the walk
// validates, as an `@` path that reads from that Node.
func (p place) path() paths.Path {
	var selectors []paths.Path

	for s := p.last; s != nil; s = s.prev {
		selectors = append(selectors, s.selector)
	}

	slices.Reverse(selectors)

	return paths.Current().Join(selectors...)
}

// walk validates v and everything below it at the place at. For an inline
// struct, or a pointer to one, shadowed names the fields of its parent that
// are not inline.
func (w *selfWalker) walk(v reflect.Value, at place, shadowed map[string]bool) {
	if !v.IsValid() {
		return
	}

	v = exposed(v)

	// The walk skips a value whose type holds no validator, itself
	// included, without a look at the values below it.
	if !w.checked.mayHold(v.Type()) || w.stopped() {
		return
	}

	switch v.Kind() {
	case reflect.Interface:
		if !v.IsNil() {
			w.walk(v.Elem(), at, nil)
		}

		return

	case reflect.Pointer, reflect.Map, reflect.Slice:
		switch {
		case v.IsNil():
			// A nil map or slice validates here rather than through walked,
			// where every nil value of its type would share one record.
			if v.Kind() != reflect.Pointer {
				w.validate(v, at)
			}

		case !ownsAddress(v) && v.Kind() == reflect.Pointer:
			w.walk(v.Elem(), at, shadowed)

		case !ownsAddress(v):
			w.walkValue(v, at, shadowed)

		default:
			w.walkOwned(v, at, shadowed)
		}

		return

	default:
	}

	w.walkValue(v, at, shadowed)
}

// walkValue walks v, which is neither a pointer nor an interface, as
// [selfWalker.walk] does. A struct that it cannot address walks as a copy,
// so [exposed] can read its fields. A struct that an embedded field
// decodes walks that field alone. The values below v validate before v.
func (w *selfWalker) walkValue(v reflect.Value, at place, shadowed map[string]bool) {
	if v.Kind() == reflect.Struct {
		v = addressable(v)
	}

	field, whole := w.cfg.unmarshalers.Whole(v.Type())

	mark, sealed := len(w.within), w.sealed
	if whole {
		w.enter(v.Kind(), at)
	}

	if field >= 0 {
		w.walk(v.Field(field), at, nil)
	} else {
		w.children(v, at, shadowed)
	}

	w.validate(v, at)

	w.within, w.sealed = w.within[:mark], sealed
}

// enter records the value at at, which decodes itself, as where an error
// below it binds when the path of the error does not bind at any node of
// the document. A slice, array, or map seals the record, because tags do
// not say where its elements sit. Every error below such a value binds at
// it.
func (w *selfWalker) enter(kind reflect.Kind, at place) {
	if w.sealed {
		return
	}

	// A struct and the embedded field that decodes it share a place.
	if n := len(w.within); n == 0 || w.within[n-1] != at {
		w.within = append(w.within, at)
	}

	switch kind {
	case reflect.Slice, reflect.Array, reflect.Map:
		w.sealed = true
	default:
	}
}

// fallback returns where an error of the value the walk is at binds when
// the path of the error does not bind at any node of the document, such
// as a wildcard path. The nearest node that the document holds along that
// path wins over it. The result lists the paths of the values that the
// walk is inside of and that decode themselves, the nearest first.
func (w *selfWalker) fallback() fallback {
	if len(w.within) == 0 {
		return fallback{}
	}

	at := make([]paths.Path, 0, len(w.within))
	for _, p := range slices.Backward(w.within) {
		at = append(at, p.path())
	}

	return fallback{at: at, only: w.sealed}
}

// exposed returns v, or a view of v when v is an unexported embedded field,
// which go-yaml can fill through a method it promotes. The view shares the
// memory of v, so v must be addressable, as [selfWalker.walkValue] ensures.
func exposed(v reflect.Value) reflect.Value {
	if v.CanInterface() {
		return v
	}

	return reflect.NewAt(v.Type(), v.Addr().UnsafePointer()).Elem()
}

// addressable returns v when the walk can take its address, or else an
// addressable copy of it.
func addressable(v reflect.Value) reflect.Value {
	if v.CanAddr() {
		return v
	}

	c := reflect.New(v.Type()).Elem()
	c.Set(v)

	return c
}

// walkOwned walks v unless the walk has entered it already. It takes a
// non-nil pointer, map, or slice that owns its address.
func (w *selfWalker) walkOwned(v reflect.Value, at place, shadowed map[string]bool) {
	key := visitOf(v)
	if w.walked[key] {
		return
	}

	w.walked[key] = true

	if v.Kind() == reflect.Pointer {
		w.walk(v.Elem(), at, shadowed)
	} else {
		w.walkValue(v, at, shadowed)
	}
}

// ownsAddress reports whether the address of v names v alone. It takes a
// non-nil pointer, map, or slice. A v with zero-size data, or an empty
// slice, can share its address. Such a v does not hold anything that leads
// back, so it does not need a record.
func ownsAddress(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Pointer:
		return v.Type().Elem().Size() != 0
	case reflect.Slice:
		return v.Len() != 0 && v.Type().Elem().Size() != 0
	default:
		return true
	}
}

// visitOf returns the [visit] naming v, a pointer, map, or slice.
func visitOf(v reflect.Value) visit {
	key := visit{typ: v.Type(), ptr: v.UnsafePointer()}
	if v.Kind() == reflect.Slice {
		key.len = v.Len()
	}

	return key
}

var (
	// NoValidator is a [Validator] that passes every Node. Every function of
	// the package that takes a Validator panics for a nil one, so a missing
	// check never reads as a document that passed. A program that validates
	// only on some condition starts from NoValidator, and the call site then
	// shows that the check can be off:
	//
	//	v := niceyaml.NoValidator
	//	if strict {
	//		v = reg
	//	}
	//
	//	config, err := doc.Decode[Config](ctx, niceyaml.WithValidator(v))
	//
	// Its Check method returns only the error of a document that did not
	// parse. [Source.ValidateDocuments] with NoValidator thus checks the
	// syntax of a file. A program compares a Validator with NoValidator to
	// learn whether the check is off.
	// [go.jacobcolvin.com/niceyaml/schema.AcceptAll] is the schema that passes
	// every document.
	NoValidator Validator = noValidator{}

	// The result of [selfCheckOf] for each type it has read.
	ownsValidator unmarshaler.TypeCache[selfCheck]

	// The empty set of types, from which [checkedTypesOf] reaches every
	// other set.
	noCheckedTypes = &checkedTypes{}
)

// checkedTypes is a set of types that functions from [WithValueCheck] check,
// with the answers about each type that the walk prunes by. A validator of the
// walk is a value that implements [SelfValidator] or [SelfValidatorContext],
// or whose type the set holds. [checkedTypesOf] returns one instance for each
// set, so every walk with the same set shares the answers. The walks without a
// function share the answers of [noCheckedTypes].
type checkedTypes struct {
	// Maps each type t that sorts after types to the set that holds types
	// and t.
	next map[reflect.Type]*checkedTypes
	// The result of [checkedTypes.mayHold] for each type it has read.
	holds unmarshaler.TypeCache[bool]
	// The result of [checkedTypes.fieldsOf] for each struct type it has
	// read.
	fields unmarshaler.TypeCache[*structFields]
	// The types of the set, in the order [checkedTypesOf] sorts them.
	types []reflect.Type
	// Guards next.
	mu sync.Mutex
}

// checkedTypesOf returns the set of the types that checks check. It sorts
// the types by their names, so the same types in another order return the
// same instance. Two types that share a name keep the order that checks
// gives them. Those types in another order thus return a second instance,
// which holds answers of its own.
func checkedTypesOf(checks []valueCheck) *checkedTypes {
	var types []reflect.Type

	for _, c := range checks {
		if !slices.Contains(types, c.typ) {
			types = append(types, c.typ)
		}
	}

	slices.SortStableFunc(types, func(a, b reflect.Type) int {
		return strings.Compare(a.String(), b.String())
	})

	set := noCheckedTypes
	for _, t := range types {
		set = set.with(t)
	}

	return set
}

// with returns the set that holds the types of c plus t. The type t sorts
// after the types of c. The method creates the set when a walk first asks
// for it.
func (c *checkedTypes) with(t reflect.Type) *checkedTypes {
	c.mu.Lock()
	defer c.mu.Unlock()

	next, ok := c.next[t]
	if !ok {
		next = &checkedTypes{types: append(slices.Clip(c.types), t)}

		if c.next == nil {
			c.next = map[reflect.Type]*checkedTypes{}
		}

		c.next[t] = next
	}

	return next
}

// validates reports whether the walk validates a value of type t itself,
// through the method of [SelfValidator] or [SelfValidatorContext] or a
// function of the set.
func (c *checkedTypes) validates(t reflect.Type) bool {
	return selfCheckOf(t) != noSelfCheck || slices.Contains(c.types, t)
}

// mayHold reports whether a value of type t, or one below it the walk
// reaches, can be a validator, as any interface can. It does not read a
// value, so every walk with the set shares the answer, which can
// overreport.
func (c *checkedTypes) mayHold(t reflect.Type) bool {
	return c.holds.Get(t, func(t reflect.Type) bool {
		// The walk reads nothing below a node of the syntax tree.
		return yamlfield.Reaches(t, func(t reflect.Type) (bool, bool) {
			return t.Kind() == reflect.Interface || c.validates(t), unmarshaler.IsSyntaxNode(t)
		})
	})
}

// children walks the values below v. It skips the fields of an inline
// struct that shadowed names, because go-yaml zeroes an inline field that
// shares its name with a field of the parent.
func (w *selfWalker) children(v reflect.Value, at place, shadowed map[string]bool) {
	switch v.Kind() {
	case reflect.Slice, reflect.Array, reflect.Map:
		// The walk skips entries whose types hold no validator, and a map
		// whose entries hold none, so it reads no key of such a map.
		if !w.checked.entriesMayHold(v.Type()) || v.Kind() == reflect.Map && !w.holdsBelow(v) {
			return
		}

	default:
	}

	switch v.Kind() {
	case reflect.Struct:
		fields := w.checked.fieldsOf(v.Type())

		for _, field := range fields.held {
			if shadowed[field.name] {
				continue
			}

			child, fieldShadowed := at, map[string]bool(nil)
			if field.inline {
				fieldShadowed = fields.own
			} else {
				child = at.child(field.name)
			}

			w.walk(v.Field(field.index), child, fieldShadowed)
		}

	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			w.walk(v.Index(i), at.index(i), nil)
		}

	case reflect.Map:
		// [datapath.MapGroups] orders the entries by key, so the errors come
		// back in one order however the map iterates.
		names := w.keyNames(w.nodeOf(at.last), v.Type().Key())

		// Keys that share a segment share a path, which can locate one of
		// their entries at most, so the errors under it bind with no position.
		for _, group := range datapath.MapGroups(v, names) {
			w.walkEntries(at.child(group.Seg), group.Entries, group.Ambiguous)
		}

	default:
	}
}

// walkEntries walks entries, which share the place at. It sorts the errors
// of each entry as a group by their text, because the keys share a text and
// formatting a value might not end. A value that several entries share
// walks once, under the first entry that the map yields. When ambiguous is
// true, it binds the errors first so the text names paths.
func (w *selfWalker) walkEntries(at place, entries []datapath.MapEntry, ambiguous bool) {
	start := len(w.errs)
	groups := make([][]error, 0, len(entries))

	// Every path below an ambiguous entry is ambiguous too, except below a
	// sealed value, where errors bind at that value and no path locates them.
	outer := w.ambiguous
	w.ambiguous = outer || (ambiguous && !w.sealed)

	for _, e := range entries {
		w.walk(e.Key, at.key(), nil)
		w.walk(e.Value, at, nil)

		errs := w.errs[start:]
		if ambiguous {
			for i, err := range errs {
				errs[i] = w.bind(err)
			}
		}

		groups = append(groups, slices.Clone(errs))
		w.errs = w.errs[:start]
	}

	w.ambiguous = outer

	slices.SortStableFunc(groups, func(a, b []error) int {
		return slices.CompareFunc(a, b, func(x, y error) int {
			return strings.Compare(x.Error(), y.Error())
		})
	})

	for _, group := range groups {
		w.errs = append(w.errs, group...)
	}
}

// bind binds err as the walk binds an error before it returns. When the
// walk has a [Node], bind binds err through it, as [Node.bindOwn] does.
// Otherwise it binds err outside any document, as [BindValue] does.
func (w *selfWalker) bind(err error) error {
	if w.node == nil {
		return BindValue(err)
	}

	return w.node.bindOwn(err)
}

// structFields holds the fields of a struct type that the walk reads, as
// [checkedTypes.fieldsOf] returns them.
type structFields struct {
	// The names of the fields of the struct that are not inline, as
	// [yamlfield.OwnNames] returns them, or nil when held does not hold an
	// inline field.
	own map[string]bool

	// The fields that go-yaml decodes and whose types can hold a
	// validator, in the order the struct declares them.
	held []heldField
}

// heldField is a struct field whose type can hold a validator.
type heldField struct {
	// The name go-yaml decodes the field under, as [yamlfield.Name]
	// returns it.
	name string

	// The index of the field in its struct.
	index int

	// Whether the field is inline, so its own fields sit beside its
	// siblings.
	inline bool
}

// fieldsOf returns the fields of the struct type t that the walk reads. It
// leaves out each field that go-yaml skips and each field whose type
// cannot hold a validator. Every walk with the set shares the result, so a
// caller must not change it.
func (c *checkedTypes) fieldsOf(t reflect.Type) *structFields {
	return c.fields.Get(t, c.readFields)
}

// readFields returns the fields [checkedTypes.fieldsOf] returns for t.
func (c *checkedTypes) readFields(t reflect.Type) *structFields {
	fields := &structFields{}

	for i := range t.NumField() {
		field := t.Field(i)
		if !c.mayHold(field.Type) {
			continue
		}

		name, inline, skip := yamlfield.Name(field)
		if skip {
			continue
		}

		if inline && fields.own == nil {
			fields.own = yamlfield.OwnNames(t)
		}

		fields.held = append(fields.held, heldField{name: name, index: i, inline: inline})
	}

	return fields
}

// entriesMayHold reports whether an element of a slice, array, or map of
// type t, or a key of such a map, can hold a validator, as
// [checkedTypes.mayHold] reads it.
func (c *checkedTypes) entriesMayHold(t reflect.Type) bool {
	if t.Kind() == reflect.Map && c.mayHold(t.Key()) {
		return true
	}

	return c.mayHold(t.Elem())
}

// holdsBelow reports whether a value below v, a map the walk is inside of,
// is a validator where the walk validates it. The scan reads each
// pointer, map, and slice once, however many paths lead there.
func (w *selfWalker) holdsBelow(v reflect.Value) bool {
	defer clear(w.scanning)

	if ownsAddress(v) {
		key := visitOf(v)
		if held, ok := w.scanned[key]; ok {
			return held
		}

		w.scanning[key] = true
	}

	// The scan stops only at v itself on the way back up, which the walk
	// is inside of and would stop at too, so the result holds either way.
	held, _ := w.scanChildren(v)

	// A false result holds for every value the scan reached, since each
	// lies below v. When v validates itself, a value that leads back to v
	// holds v below it, so the scan records nothing.
	if !held && !w.checked.validates(v.Type()) {
		for key := range w.scanning {
			w.scanned[key] = false
		}
	}

	return held
}

// scanValue reports whether the walk validates v or a value below it.
// Its second result is false when the scan met a value it was still reading,
// so a false first result then holds for this scan alone.
func (w *selfWalker) scanValue(v reflect.Value) (bool, bool) {
	if !v.IsValid() || !w.checked.mayHold(v.Type()) {
		return false, true
	}

	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return false, true
		}

		return w.scanValue(v.Elem())

	case reflect.Pointer, reflect.Map, reflect.Slice:
		if v.IsNil() {
			if v.Kind() == reflect.Pointer {
				return false, true
			}

			return w.checked.validates(v.Type()), true
		}

		if !ownsAddress(v) {
			return w.scanOwned(v)
		}

		key := visitOf(v)
		if held, ok := w.scanned[key]; ok {
			return held, true
		}

		if w.scanning[key] {
			return false, false
		}

		// The record of a map or slice covers only the values below it, as
		// holdsBelow reads it, so a map or slice that validates itself
		// reports a validator here and records nothing.
		if v.Kind() != reflect.Pointer && w.checked.validates(v.Type()) {
			return true, true
		}

		w.scanning[key] = true

		held, complete := w.scanOwned(v)
		if held || complete {
			w.scanned[key] = held
		}

		return held, complete

	default:
		return w.scanSelf(v)
	}
}

// scanOwned scans v, a non-nil pointer, map, or slice, as
// [selfWalker.scanValue] describes.
func (w *selfWalker) scanOwned(v reflect.Value) (bool, bool) {
	if v.Kind() == reflect.Pointer {
		return w.scanValue(v.Elem())
	}

	return w.scanSelf(v)
}

// scanSelf scans v, a value that is neither a pointer nor an interface, as
// [selfWalker.scanValue] describes.
func (w *selfWalker) scanSelf(v reflect.Value) (bool, bool) {
	if w.checked.validates(v.Type()) {
		return true, true
	}

	if field, _ := w.cfg.unmarshalers.Whole(v.Type()); field >= 0 {
		return w.scanValue(v.Field(field))
	}

	return w.scanChildren(v)
}

// scanChildren scans the values below v, as [selfWalker.scanValue] does.
// It reads the shadowed fields of an inline struct too, so it can report a
// validator that the walk never reaches. Such a report costs the walk only
// a look.
func (w *selfWalker) scanChildren(v reflect.Value) (bool, bool) {
	complete := true

	scan := func(child reflect.Value) bool {
		held, done := w.scanValue(child)
		complete = complete && done

		return held
	}

	switch v.Kind() {
	case reflect.Struct:
		for _, field := range w.checked.fieldsOf(v.Type()).held {
			if scan(v.Field(field.index)) {
				return true, true
			}
		}

	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			if scan(v.Index(i)) {
				return true, true
			}
		}

	case reflect.Map:
		t := v.Type()
		scanKeys, scanValues := w.checked.mayHold(t.Key()), w.checked.mayHold(t.Elem())

		// One holder each for the keys and values saves the copy iter.Key and
		// iter.Value make per entry, but SetIterKey and SetIterValue panic on
		// a map read through an unexported field, so such a map reads copies.
		var key, value reflect.Value

		if v.Len() != 0 && v.CanInterface() {
			if scanKeys {
				key = reflect.New(t.Key()).Elem()
			}

			if scanValues {
				value = reflect.New(t.Elem()).Elem()
			}
		}

		for iter := v.MapRange(); iter.Next(); {
			if scanKeys && scan(iterKey(iter, key)) || scanValues && scan(iterValue(iter, value)) {
				return true, true
			}
		}

	default:
	}

	return false, complete
}

// iterKey returns the key at iter, set into holder when holder is valid,
// or a copy when it is not.
func iterKey(iter *reflect.MapIter, holder reflect.Value) reflect.Value {
	if !holder.IsValid() {
		return iter.Key()
	}

	holder.SetIterKey(iter)

	return holder
}

// iterValue returns the value at iter, set into holder when holder is
// valid, or a copy when it is not.
func iterValue(iter *reflect.MapIter, holder reflect.Value) reflect.Value {
	if !holder.IsValid() {
		return iter.Value()
	}

	holder.SetIterValue(iter)

	return holder
}

// selfCheck names the method that the walk calls on a value to have it
// validate itself.
type selfCheck uint8

const (
	// The type declares neither method.
	noSelfCheck selfCheck = iota
	// The type declares the Validate of [SelfValidator] alone.
	plainSelfCheck
	// The type declares the ValidateContext of [SelfValidatorContext],
	// whether or not it declares Validate too.
	contextSelfCheck
)

// selfCheckOf returns the method that the walk calls on a value of type t.
// A type declares a method on its value or its pointer. A struct that gets
// the method from an embedded field does not declare it, so the walk
// validates that field instead. A type that declares both methods gets
// ValidateContext alone.
func selfCheckOf(t reflect.Type) selfCheck {
	return ownsValidator.Get(t, func(t reflect.Type) selfCheck {
		switch {
		case unmarshaler.Declares(t, reflect.TypeFor[SelfValidatorContext]()):
			return contextSelfCheck
		case unmarshaler.Declares(t, reflect.TypeFor[SelfValidator]()):
			return plainSelfCheck
		default:
			return noSelfCheck
		}
	})
}

// validate runs the method of v that [selfCheckOf] names, through a copy
// when v is not addressable. It runs ValidateContext as it runs a function
// of [WithValueCheck]. It then runs each function of the walk from
// WithValueCheck for the type of v, in order. Every check runs and reports
// its own failure, whatever the checks before it return. Rebasing the
// error of the method or of a function under at presumes every problem
// invalid, unless an Error from [Unchecked] sits on its cause chain. Each
// such problem matches [errInvalid] whether it has a location or not.
// [selfWalker.report] describes the errors that the presumption skips.
func (w *selfWalker) validate(v reflect.Value, at place) {
	if !w.checked.validates(v.Type()) || w.stopped() {
		return
	}

	v = addressable(v)

	switch selfCheckOf(v.Type()) {
	case contextSelfCheck:
		validator, ok := reflect.TypeAssert[SelfValidatorContext](v.Addr())
		if ok {
			failure, warnings := runHook(w.ctx, validator.ValidateContext)
			w.report(failure, warnings, at, presumed)
		}

	case plainSelfCheck:
		validator, ok := reflect.TypeAssert[SelfValidator](v.Addr())
		if ok {
			failure, warnings := splitWarnings(validator.Validate())
			w.report(failure, warnings, at, presumed)
		}

	case noSelfCheck:
	}

	for _, check := range w.cfg.checks {
		if check.typ != v.Type() {
			continue
		}

		if w.stopped() {
			return
		}

		failure, warnings := runHook(w.ctx, func(ctx context.Context) error {
			return check.run(ctx, v.Addr())
		})
		w.report(failure, warnings, at, presumed)
	}
}

// report records the errors and the warnings of a check of one value. It
// rebases each one with d under the place of that value. When the context
// of the walk ended, report drops the failure, because the walk returns the
// error of that context alone.
//
// A failure that matches the error of a context when the context of the
// walk is live comes from a context of the check, such as a deadline of
// its own. The walk keeps it at the place of the value and validates the
// values that remain. That failure rebases without a declaration, so a
// timeout is not a fault of the document, as [Invalid] reads it. The test
// reads the whole failure, as Invalid does. Each branch of a join with one
// such branch thus rebases without a declaration, and only a branch that
// declares its own fault stays one. A failure that holds a [PanicError]
// rebases without a declaration the same way. A panic is a bug in code,
// not a fault of the document.
func (w *selfWalker) report(failure, warnings error, at place, d declaration) {
	m := move{base: at.path(), ambiguous: w.ambiguous, fallback: w.fallback()}

	if warnings != nil {
		w.warns = append(w.warns, rebase(warnings, m, d))
	}

	if failure == nil || w.stopped() {
		return
	}

	if contextEnded(failure) || holdsPanic(failure) {
		d = undeclared
	}

	w.errs = append(w.errs, rebase(failure, m, d))
}

// stopped reports whether the walk has stopped. The walk stops only when
// its context ends, as [MultiValidator] and [Each] do. The error of that
// context then replaces every error of the walk. An error that a check
// returns does not stop the walk, whatever it matches.
func (w *selfWalker) stopped() bool {
	if w.ended == nil {
		w.ended = w.ctx.Err()
	}

	return w.ended != nil
}

// nodeOf returns the node the value at the end of s decoded from, or nil
// when the document does not hold one there. A nil s stands for the walk's
// start. Each step resolves once, from the node of the step before.
func (w *selfWalker) nodeOf(s *step) ast.Node {
	if w.node == nil {
		return nil
	}

	if s == nil {
		s = &w.start
	}

	if s.resolved {
		return s.node
	}

	var (
		node ast.Node
		err  error
	)

	switch {
	case s == &w.start:
		node, err = w.pathResolver().Node(w.node.base)

	case s.key:
		// A `~` selects the key of the entry the step before it selects,
		// but the node of that step is the value of the entry, so the two
		// selectors resolve together from the step above them.
		entry := s.prev
		node, err = w.pathResolver().NodeFrom(w.nodeOf(entry.prev), entry.selector.Join(s.selector))

	default:
		node, err = w.pathResolver().NodeFrom(w.nodeOf(s.prev), s.selector)
	}

	if err == nil {
		s.node = node
	}

	s.resolved = true

	return s.node
}

// keyNames maps each key of the mapping at node, by the value it decodes to
// as type t, to the text a path resolves it by, such as 0x10 for 16. Of the
// entries that set one key, merges included, the one that the decode keeps
// wins.
func (w *selfWalker) keyNames(node ast.Node, t reflect.Type) map[any]string {
	names := map[any]string{}

	mapping, ok := astnode.Content(w.valueNode(node, false)).(*ast.MappingNode)
	if !ok {
		return names
	}

	// The decoder sets each entry in document order, and a merge sets
	// the keys of its sources where it stands, so a later entry wins
	// whether it is a key of the mapping or a merge.
	for entry := range datapath.MembersBackward(w.pathResolver(), mapping) {
		w.addKeyName(entry.Key, t, names)
	}

	return names
}

// pathResolver returns the [paths.Resolver] for the document of the
// walk.
func (w *selfWalker) pathResolver() *paths.Resolver {
	return w.node.doc.pathResolver()
}

// keyDecoder returns the go-yaml decoder for the keys of the walk. It
// creates the decoder with the options of the decode when the walk first
// decodes a key.
func (w *selfWalker) keyDecoder() *yaml.Decoder {
	if w.decoder == nil {
		w.decoder = w.node.newDecoder(w.cfg)
	}

	return w.decoder
}

// addKeyName adds the text of key to names under the value key decodes to
// as type t, unless names already holds that value. As in go-yaml, only a
// null key or an alias to one, not a key such as `&a ~`, leaves a pointer t
// nil.
func (w *selfWalker) addKeyName(key ast.MapKeyNode, t reflect.Type, names map[any]string) {
	name, ok := w.pathResolver().KeyName(key)
	if !ok {
		return
	}

	node := w.valueNode(key, true)
	if node == nil {
		return
	}

	null := key.Type() == ast.NullType ||
		key.Type() == ast.AliasType && node.Type() == ast.NullType

	if t.Kind() == reflect.Pointer && !null {
		t = t.Elem()
	}

	decoded := reflect.New(t)

	err := decodeWithRecover(w.keyCtx, w.keyDecoder(), node, decoded.Interface())
	if err != nil {
		return
	}

	k, ok := datapath.KeyOf(decoded.Elem())
	if !ok {
		return
	}

	if _, set := names[k]; !set {
		names[k] = name
	}
}

// valueNode returns the node that node decodes from, past each `?` and anchor
// and through each alias, which the key decoder cannot follow. Each tag stays
// around it. With parsed set, the nodes come from the tree the decode reads,
// but the decode hands UnmarshalYAML a key with a `?`, anchor, or alias whole.
func (w *selfWalker) valueNode(node ast.Node, parsed bool) ast.Node {
	var (
		tags     []*ast.TagNode
		followed = map[*ast.AliasNode]bool{}
	)

	for !astnode.IsNil(node) {
		switch n := node.(type) {
		case *ast.MappingKeyNode:
			node = n.Value
		case *ast.AnchorNode:
			node = n.Value
		case *ast.TagNode:
			tags = append(tags, n)
			node = n.Value

		case *ast.AliasNode:
			if followed[n] {
				return nil
			}

			followed[n] = true

			content, err := w.pathResolver().Deref(n)
			if err != nil {
				return nil
			}

			node = content

		default:
			view := func(n ast.Node) ast.Node { return n }
			if parsed {
				view = w.node.doc.tree().Parsed
			}

			node = view(node)

			for _, tag := range slices.Backward(tags) {
				tagView, ok := view(tag).(*ast.TagNode)
				if !ok {
					tagView = tag
				}

				tagged := *tagView
				tagged.Value = node
				node = &tagged
			}

			return node
		}
	}

	return nil
}

// DataLocator finds where a location in decoded data lies in the document
// the data came from. A check of decoded data, such as an adapter for
// CUE, Open Policy Agent, or another JSON Schema library, names what it
// finds by the member names and indices of that data. A path matches a key
// by its source text instead. [AtPath] of those names thus misses a key
// that the decoder respells, such as the key 0x10 of the member 16. A
// DataLocator reads the names as the decoder does. Its options give an
// error the same location that
// [go.jacobcolvin.com/niceyaml/schema.Schema] gives a violation of the
// same value:
//
//	loc := n.DataLocator()
//
//	for _, f := range findings {
//		errs = append(errs, niceyaml.NewError(f.Msg, loc.At(f.Path...))) // $.ports.0x10.name
//	}
//
// Create instances with [Node.DataLocator].
//
// The names are those of the data from Decode[any] of the Node. A string
// key gives its text. Any other key gives its Go value as [fmt.Sprint]
// prints it, so 0x10 is 16, 3.10 is 3.1, and True is true. An element has
// its index in decimal digits, as [strconv.Itoa] writes it. A decode into
// another type can name a key another way, such as 1e3 for the key 1000
// of a map[int]string. Such a name leads nowhere. A check that reports
// JSON Pointers splits each pointer into its names and unescapes `~1` and
// `~0` in each.
//
// A DataLocator is safe for concurrent use, and it reads each mapping
// once, so a check creates one for all the findings of a run. Its reads
// of `<<` merge keys share one budget, as large as the budget that
// [paths.ErrExcessiveMerging] allows one path selector. A check thus
// creates a DataLocator for each run and does not keep one for the life
// of the document.
type DataLocator struct {
	// The node the names read from.
	root ast.Node
	// Finds the members of each mapping by the names a decode gives them.
	idx *datapath.Index
	// The `$` path to root.
	base paths.Path
	// Guards idx, which two walks must not read at the same time.
	mu sync.Mutex
}

// DataLocator creates a new [*DataLocator] for the data the Node decodes
// to. The names it reads start at the Node, as the data from
// [Node.Decode] does. A [Validator] thus creates one from the Node it got,
// whatever the scope of that Node. Each path it writes starts at `$`, so
// the errors bind through any Node of the document.
//
// A document that did not parse does not decode to any data, as
// [Node.Err] describes. Every name then leads nowhere, and each error
// binds without a position.
func (n *Node) DataLocator() *DataLocator {
	return &DataLocator{
		root: n.AST(),
		idx:  datapath.NewIndex(n.DocumentResolver(), n.keyReader()),
		base: n.base,
	}
}

// At returns an [ErrorOption] that locates an error at the value the
// names lead to in the data of the [DataLocator]. An empty list of names
// leads to the whole of that data. The option sets the `$` path of the
// value, as [AtPath] does, with each key spelled as the source spells it:
//
//	niceyaml.NewError("must not be empty", loc.At("ports", "16", "name"))
//	// cfg.yaml:3:11: $.ports.0x10.name: must not be empty
//
// When several keys of a mapping decode to one name, the names lead to
// the last of them, whose value the decode keeps. When another key spells
// the same text, a path cannot select the value. The option then also
// sets its position, as [AtPosition] does, and the path names the member
// as the decoder does.
//
// Names that lead past the data keep the form the caller wrote from there
// on. The error binds where that path binds, as [SourceError.Nearest]
// describes. A member that a mapping lacks binds at the key of the
// mapping, and an index past the end of a sequence binds at the key of the
// sequence. A name below a scalar binds at the scalar.
//
// A name can lead behind an alias that the document cannot follow, such
// as an alias to an anchor of a document from [WithReferences]. So can a
// name in a mapping whose `<<` merge key holds such an alias, where that
// document decides which entry a path through the name selects, as the
// paths package describes. Such a name keeps its form too, and so does
// each name below it. The option does not set a position, so the error
// binds where that path binds. A name such as 3.10 there binds where a path
// through the key 3.10 binds. Elsewhere, a name that does not match a
// member but whose text a path selects, such as 3.10 for the member 3.1,
// binds at the last value the names lead to.
func (l *DataLocator) At(names ...string) ErrorOption {
	return l.option(names, false)
}

// AtKey returns an [ErrorOption] that locates an error at the key of the
// mapping entry the names lead to. It suits an error about the key itself,
// such as an unknown member. It reads the names as [DataLocator.At] does,
// and the path it sets ends in `~`, as a path from [paths.Path.Key] does:
//
//	niceyaml.NewError("unknown port", loc.AtKey("ports", "16"))
//	// cfg.yaml:2:3: $.ports.0x10~: unknown port
//
// An element of a sequence and the data as a whole do not have a key, so
// the error binds at the value there. A member that the data lacks does
// not have a key either, so the option locates it as [DataLocator.At]
// does. A name behind an alias that the document cannot follow keeps its
// form, as [DataLocator.At] describes, and the path still ends in `~`.
func (l *DataLocator) AtKey(names ...string) ErrorOption {
	return l.option(names, true)
}

// option returns the [ErrorOption] that sets the path [DataLocator.locate]
// finds for names, and the position of the token it finds, if any.
func (l *DataLocator) option(names []string, key bool) ErrorOption {
	path, tk := l.locate(names, key)
	place := atToken(tk)

	return func(c *errorConfig) {
		c.path, c.hasPath = path, true

		place(c)
	}
}

// locate returns the path names lead to from the node of l, and the token
// to bind an error at, as [datapath.Index.Locate] finds them.
func (l *DataLocator) locate(names []string, key bool) (paths.Path, *token.Token) {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.idx.Locate(datapath.Target{Path: l.base, Node: l.root}, names, key)
}

// SelfValidator is a type that validates itself after a decode.
// [Node.Decode] and [Node.DecodeInto] call Validate on every value in the
// result that implements it, through a value or a pointer receiver,
// unless [WithSelfValidation] disables these calls. Validate reports a
// problem as an [*Error] with an `@` path, which reads from the value. A
// type thus checks its invariants in one place and reports the right
// lines in any document:
//
//	func (h Hours) Validate() error {
//		if h.Close.Before(h.Open) {
//			return niceyaml.NewError("closes before it opens", niceyaml.AtPath(paths.Current().Child("close")))
//		}
//
//		return nil
//	}
//
// A decode presumes the document at fault for each error from Validate
// that does not declare a fault, a plain I/O error included, as it does
// for a [Validator]. A check that can fail for reasons outside the
// document returns such an error inside [Unchecked]. Unchecked disables
// the presumption, as the Errors section of the package documentation
// describes.
//
// The decode also does not presume a fault for a result of Validate that
// matches [context.Canceled] or [context.DeadlineExceeded], such as the
// timeout of a lookup. It reports that result at the value and validates
// the values that remain, as the Cancellation section describes. A join
// with one such branch matches as a whole, so the decode does not presume
// a fault for any problem of that join. A Validate that returns other
// problems beside a timeout should declare each of them with [NewError] or
// [Invalid].
//
// The walk calls Validate on a value after the values below it, whatever
// their checks return, so Validate guards each precondition that it reads.
// For example, it returns nil when a field that it depends on is empty,
// and the check of that field reports the problem. Validate must not
// dereference or index state that only a check below it guarantees.
//
// The Validation section describes the order the values validate in, the
// path of each value, and where each error binds.
//
// A type whose check needs the context of the call, such as one that
// does I/O, implements [SelfValidatorContext] instead.
type SelfValidator interface {
	Validate() error
}

// SelfValidatorContext is a type that validates itself after a decode, as
// a [SelfValidator] does, with the context of the call. It does not need a
// Validate method. The self-validation walk calls ValidateContext wherever it
// calls Validate, and it passes the context as it does to a function of
// [WithValueCheck]. A check thus stops when the caller cancels the
// context, and it reads the deadline and the values of that context:
//
//	func (t TLS) ValidateContext(ctx context.Context) error {
//		err := checkCert(ctx, t.CertFile)
//		if err != nil {
//			return niceyaml.Unchecked(err, niceyaml.AtPath(paths.Current().Child("certFile")))
//		}
//
//		return nil
//	}
//
// The walk calls ValidateContext alone on a type that declares both
// methods itself. A method that an embedded field promotes runs on that
// field instead, as the Validation section of the package documentation
// describes. A type that declares both keeps Validate for code outside
// niceyaml that expects that method, and Validate calls ValidateContext
// with [context.Background].
//
// The walk places each warning that ValidateContext hands to its context
// as it places a warning that the method returns. A nested
// [SelfValidateValue] or
// [go.jacobcolvin.com/niceyaml/schema.Schema.ValidateValue] that gets the
// context hands its warnings there. ValidateContext can also return its
// warnings beside its errors, as Validate does.
//
// The walk reads a result of ValidateContext as it reads a result of
// Validate. The decode presumes a fault for each error that does not
// declare one. It reports a timeout of the method at the value. The walk
// stops when the context of the call ends, as the Cancellation section of
// the package documentation describes.
type SelfValidatorContext interface {
	ValidateContext(ctx context.Context) error
}

// Validator validates a [*Node] before it decodes, as a JSON schema does.
// Pass one to a decode with [WithValidator], or run one on its own with
// [Node.Validate].
//
// See [ValidatorFunc], [MultiValidator], [ChainValidator], [SkipEmpty],
// [Each], [go.jacobcolvin.com/niceyaml/schema.Schema], and
// [go.jacobcolvin.com/niceyaml/schema.Registry] for implementations.
// [NoValidator] is the ready-made Validator that passes every Node, for a
// caller that does not have a check to run.
//
// Check gets the Node that runs it, the root of a document or a Node from
// [Node.At] or [Node.Nodes]. An `@` path in an error of Check reads from
// that Node. An error about a key the document lacks carries the path of
// the key. Node.Validate and a decode bind what Check returns as
// [Node.Bind] binds an error. They bind each warning that Check hands to
// its context the same way.
//
// Node.Validate and a decode presume the document at fault for each error
// and each warning of Check that does not declare anything, as they do for
// a Validate method. A Validator thus returns a plain error for a fault
// of the document. It returns the error of a check that could not run,
// such as a failed lookup, inside [Unchecked]. The Errors section of the
// package documentation describes the rule.
//
// A direct call to Check returns the error as Check wrote it, without
// the presumption. A Check that runs Node.Validate gets the presumption
// from that call. [Each] runs its validator through Node.Validate, so a
// direct call to its Check presumes the fault for the errors and warnings
// of that validator.
//
// The validators of this module bind in Check itself. They return their
// errors bound at the Node that Check got, and nil when they have only
// warnings. A direct call to Check of one of them thus places its errors
// where Node.Validate places them. A rule that checks each element of a list
// can return those results as its own, and each error names its element:
//
//	for _, item := range items {
//		errs = append(errs, portSchema.Check(ctx, item))
//	}
//
// A type that the program writes can return its errors from Check as it
// wrote them, because the Node that runs the type binds them. A validator
// of the root that returns such a result for `$.items[1]` as its own
// reports `@.name` at `$.name` rather than at `$.items[1].name`. A type
// whose Check binds its result places its errors as the validators of
// this module place them:
//
//	func (r priceRule) Check(ctx context.Context, n *niceyaml.Node) error {
//		return n.Bind(r.check(ctx, n))
//	}
//
// Node.Bind keeps each warning of that result in the error it returns. A
// type that reports warnings thus hands them to [Node.Warn], or runs its
// body in a [ValidatorFunc]. [ValidatorFunc.Check] hands the warnings
// that the body returns to the collector of its context.
//
// A validator that runs a validator it did not write on a different Node
// passes that validator to [Each] or to the Validate method of that Node.
// Each and Node.Validate bind at that Node every error that Check left
// unbound.
//
// A validator that needs the whole document reaches it through
// [Node.Document]. The context carries cancellation and deadlines to a
// validator that does work it can stop, such as resolving a remote schema
// reference. The Validation section of the package documentation
// describes how a validator declares a fault and how one checks decoded
// data.
//
// A decode and Node.Validate return the error of a document that did not
// parse before any validator runs. A caller that calls Check itself on such a
// Node gets that error from Node.Decode, Node.At, and Node.Nodes. The
// validators of this module return it before they read the Node.
type Validator interface {
	Check(ctx context.Context, n *Node) error
}

// ValidatorFunc adapts a function to the [Validator] interface. The
// function writes its errors from the Node it gets, as Validator
// describes, and [ValidatorFunc.Check] binds them at that Node. The same
// body works as the Check method of a type, where the Node that runs the
// type binds them:
//
//	known := niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
//		kind, err := n.DecodeAt[string](ctx, kindPath)
//		if err != nil {
//			return err
//		}
//
//		if kind != "Deployment" {
//			return niceyaml.NewError("unknown kind", niceyaml.AtPath(kindPath))
//		}
//
//		return nil
//	})
//
// The Node that runs the function presumes the document at fault for a
// plain error, as [Validator] describes. A check that does not know about
// YAML thus returns its result as it is:
//
//	return check(v)
type ValidatorFunc func(ctx context.Context, n *Node) error

// Check implements [Validator]. It returns the errors of what f returns,
// bound at n as [Node.Bind] binds an error, so it places them where
// [Node.Validate] places them for f. Check does not presume a fault for
// them, as Validator describes. It returns nil when f returns warnings
// alone or a nil [*Error] or [*SourceError] pointer. The [Warnings]
// collector of ctx gets the warnings that f returns and the warnings that
// f hands to its context, bound the same way. An error that f bound
// itself comes back as it is, unless it also holds a warning. The
// Warnings section of the package documentation describes a failure that
// holds both. For a document that did not parse, Check returns the error
// that [Node.Err] returns without calling f.
func (f ValidatorFunc) Check(ctx context.Context, n *Node) error {
	if n.doc.err != nil {
		return n.doc.err
	}

	return n.Bind(n.checkWith(ctx, f, false))
}

// noValidator is the [Validator] behind [NoValidator].
type noValidator struct{}

// Check implements [Validator].
func (noValidator) Check(_ context.Context, n *Node) error {
	return n.Err()
}

// mustValidator panics if v is nil or holds a nil pointer or func. The
// panic names fn, the function of the package that got v.
func mustValidator(fn string, v Validator) {
	if nilness.IsNil(v) {
		panic("niceyaml." + fn + ": validator is nil")
	}
}

// mustValidators panics if one of validators is nil or holds a nil
// pointer or func. The panic names fn, the function of the package that
// got the validators, and the index of the first such validator.
func mustValidators(fn string, validators []Validator) {
	for i, v := range validators {
		if nilness.IsNil(v) {
			panic(fmt.Sprintf("niceyaml.%s: validator at index %d is nil", fn, i))
		}
	}
}

// MultiValidator returns a [Validator] that runs every validator in
// order and reports every failure. [ChainValidator] instead stops at the
// first. MultiValidator suits rules that check a document independently,
// as the rules of a linter do:
//
//	err := doc.Validate(ctx, niceyaml.MultiValidator(schema, names))
//
// Each validator sees the node whatever the ones before it reported.
// When every validator passes, the run returns nil. A lone failure comes
// back as the Node binds the failure of its validator alone. A failure
// that its validator bound itself comes back as it is, unless it also
// holds a warning, as the Warnings section of the package documentation
// describes. Two or more failures come back as one error, and
// [errors.Is] matches any one of them. Each failure binds at its own
// location in the Node that the MultiValidator got, as [Node.Bind]
// describes. The message lists the failures in the order of their
// positions, whatever the order of their validators:
//
//	svc.yaml:1:7: $.name: reserved name
//	svc.yaml:2:7: $.port: 0 is less than 1
//
// A context that ends stops the run, as the Cancellation section of the
// package documentation describes. MultiValidator copies the validators
// it gets, so a caller that edits the slice it passed does not change the
// Validator.
//
// MultiValidator panics if a validator is nil, including one that holds a
// nil pointer or func. [NoValidator] is the validator that passes every
// Node.
func MultiValidator(validators ...Validator) Validator {
	// The check covers the copy, which holds the validators that run.
	validators = slices.Clone(validators)
	mustValidators("MultiValidator", validators)

	return ValidatorFunc(func(ctx context.Context, n *Node) error {
		var errs []error

		for _, dv := range validators {
			err := ctx.Err()
			if err != nil {
				return err //nolint:wrapcheck // The context names the reason, and [ValidatorFunc.Check] binds it.
			}

			// A typed nil pointer is no failure, as [Node.Bind] reads it, and
			// neither is a result of warnings alone.
			err = n.checkWith(ctx, dv.Check, false)
			if err == nil {
				continue
			}

			if ctx.Err() != nil {
				return err
			}

			errs = append(errs, err)
		}

		return multierr.Join(errs)
	})
}

// ChainValidator returns a [Validator] that runs the validators in order
// and stops at the first that fails. [MultiValidator] instead runs every
// one. ChainValidator suits a check that needs an earlier one to pass,
// such as a check that follows the references of a document after a
// schema has accepted the document:
//
//	err := doc.Validate(ctx, niceyaml.ChainValidator(schema, refs))
//
// A validator runs only after every validator before it has passed. A
// validator that decodes the node thus does not report a value that the
// schema already rejected. A ChainValidator without validators passes
// every Node. A decode runs the validators of repeated [WithValidator]
// options the same way.
//
// The failure comes back as the Node binds the failure of that validator
// alone. A failure that its validator bound itself comes back as it is,
// unless it also holds a warning. The Warnings section of the package
// documentation describes a failure that holds a warning.
// ChainValidator copies the validators it gets, so a caller that edits
// the slice it passed does not change the Validator.
//
// ChainValidator panics if a validator is nil, including one that holds a
// nil pointer or func. [NoValidator] is the validator that passes every
// Node.
func ChainValidator(validators ...Validator) Validator {
	// The check covers the copy, which holds the validators that run.
	validators = slices.Clone(validators)
	mustValidators("ChainValidator", validators)

	return ValidatorFunc(func(ctx context.Context, n *Node) error {
		// The Node that runs the chain presumes the fault, so a direct
		// call to Check presumes nothing for the errors or the warnings.
		return n.firstFailure(ctx, validators, false)
	})
}

// SkipEmpty returns a [Validator] that passes a document without content,
// which [Node.IsEmpty] reports, and runs v on every other Node. A caller
// whose file might be empty as a whole wraps its validator with SkipEmpty.
// An empty file then validates, and a decode of it returns the zero value:
//
//	err := source.ValidateDocuments(ctx, niceyaml.SkipEmpty(reg))
//
// A document that holds any value runs v, so v still checks an explicit
// null such as `--- null` and an alias without an anchor before it. A
// Node that [Node.At] or [Node.Nodes] scopes below the root is not a
// document, so v runs on it. A document that did not parse returns the
// error of its parse.
//
// SkipEmpty returns the errors of v bound at the Node, as
// [ValidatorFunc.Check] returns the errors of its function. SkipEmpty
// takes one validator, so it wraps the one that [MultiValidator] or
// [ChainValidator] makes of several.
//
// SkipEmpty panics if v is nil, including a v that holds a nil pointer or
// func.
func SkipEmpty(v Validator) Validator {
	mustValidator("SkipEmpty", v)

	return ValidatorFunc(func(ctx context.Context, n *Node) error {
		if n.IsEmpty() {
			return nil
		}

		//nolint:wrapcheck // The validator's own error, which [ValidatorFunc.Check] binds.
		return v.Check(ctx, n)
	})
}

// Each returns a [Validator] that runs v on every Node that path selects
// from the Node it gets, as [Node.Nodes] selects them. It runs v through
// the Validate method of each such Node, so each failure binds at the
// Node that v checked. A rule written for one element of a list thus
// checks every element, and its errors name the element they came from:
//
//	items := paths.Current().Child("items").IndexAll()
//	err := doc.Validate(ctx, niceyaml.MultiValidator(schema, niceyaml.Each(items, reserved)))
//
// When the second element of the list is named admin, err reads:
//
//	svc.yaml:3:11: $.items[1].name: reserved name
//
// A path that ends in the `~` selector of [paths.Path.Key] selects keys
// alone. A rule for the keys of a document thus does not run on the
// elements of its sequences:
//
//	keys := paths.Doc().RecursiveAll().Key() // $..*~
//	err := doc.Validate(ctx, niceyaml.Each(keys, camelCase))
//
// Where path selects an alias that reads a reference document of
// [WithReferences], Each hands v the Node of that alias, as Node.Nodes
// scopes it. A v that decodes the Node it gets reads the anchor through the
// reference documents. An error at a path below the Node binds at the
// alias. A v that calls [Node.DecodeAt] with a path below the Node, such as
// @.price, gets an error that wraps [paths.ErrReferenceAlias] instead. A
// path of Each that goes below such an alias, such as @.items[*].price,
// returns that error before v runs on any Node. A rule for a value inside
// each element thus selects the element and reads below it in v, as the
// References section of the package documentation describes.
//
// Each reports every failure in document order, as [MultiValidator] reports
// the failures of its validators. It hands the warnings of v to the
// [Warnings] collector of its context, bound at the Node that v checked.
// Each presumes the document at fault for each error and each warning of v
// that does not declare anything, as Node.Validate does, even for a
// direct call to its Check. A path that does not select a Node passes. An
// error from Node.Nodes comes back as Node.Nodes returns it, and a
// document that did not parse returns the error of its parse. A context
// that ends stops the run, as the Cancellation section of the package
// documentation describes.
//
// Each panics if v is nil, including a v that holds a nil pointer or func.
func Each(path paths.Path, v Validator) Validator {
	mustValidator("Each", v)

	return ValidatorFunc(func(ctx context.Context, n *Node) error {
		nodes, err := n.Nodes(path)
		if err != nil {
			return err
		}

		var errs []error

		for _, node := range nodes {
			err = ctx.Err()
			if err != nil {
				return err //nolint:wrapcheck // The context names the reason, and [ValidatorFunc.Check] binds it.
			}

			err = node.Validate(ctx, v)
			if err == nil {
				continue
			}

			if ctx.Err() != nil {
				return err
			}

			errs = append(errs, err)
		}

		return multierr.Join(errs)
	})
}

// Validate runs v on the node. It is the validation step of [Node.Decode]
// on its own, for a caller that checks a document without decoding it:
//
//	if err := doc.Validate(ctx, reg); err != nil {
//		return err
//	}
//
// Validate takes one validator, so a caller with several combines them
// with [MultiValidator] or [ChainValidator], and [SkipEmpty] lets a
// document without content pass.
//
// Validate binds what v returns through [Node.Bind], so an [*Error]
// renders its location with its `@` paths read from the node. It presumes
// the document at fault for each error of v that does not declare
// anything, as [Validator] describes. An error that v bound itself comes
// back as it is when the document is at fault for each of its problems
// and it does not hold a warning. Validate hands the warnings of v to the
// [Warnings] collector of ctx, as the Warnings section of the package
// documentation describes.
//
// A Node from [Node.At] or [Node.Nodes] points an error without a
// location at its own value. A validator that runs another on a Node of
// its choosing calls Validate on that Node, so the error binds there even
// when the Check method of the other validator leaves it unbound. [Each]
// runs a validator that way on every Node a path selects, such as each
// element of a list. The validators of this module also bind in Check, as
// [Validator] describes.
//
// A document that did not parse returns the error that [Node.Err] returns
// before v runs. A validator that returns a nil [*Error] or [*SourceError]
// pointer passes.
//
// Validate panics if v is nil, including a v that holds a nil pointer or
// func, whether or not the document parsed. A caller that does not have a
// check to run passes [NoValidator].
func (n *Node) Validate(ctx context.Context, v Validator) error {
	mustValidator("Node.Validate", v)

	return n.validate(ctx, []Validator{v})
}

// validate runs validators in order on n, and binds the first error with n
// when the validator left it unbound. It presumes the document at fault for
// that error, as [presume] marks it. A document that did not parse returns
// the error of its parse, and validate does not run a validator. The
// warnings of the validators go to the collectors of ctx.
func (n *Node) validate(ctx context.Context, validators []Validator) error {
	if n.doc.err != nil {
		return n.doc.err
	}

	return n.Bind(presume(n.firstFailure(ctx, validators, true)))
}

// presume returns err, the error or the warnings of a check that niceyaml
// ran against a document, with the fault that a hook presumes on it. The
// fault does not reach a problem that holds an Error from [Unchecked], as
// [declaration.on] puts it. An err that matches the error of a context
// that ended or holds a [PanicError] comes back as it is, as
// [selfWalker.report] leaves such an error. Only a problem that declares
// a fault of its own then stays one. An err that the document is at fault
// for already comes back as it is too, so a validator that binds its own
// faults gets its error back.
func presume(err error) error {
	if isNothing(err) || contextEnded(err) || holdsPanic(err) || IsInvalid(err) {
		return err
	}

	return markFault(err, presumed)
}

// firstFailure runs validators in order on n and returns the errors of
// the first failure, as [Node.checkWith] returns them, or nil when each
// of them passes. With presumes set, it presumes the fault on the
// warnings of each validator, as [presume] marks them. [WithValidator],
// [ChainValidator], and [Node.Validate] reject a nil validator before the
// run, so none of the validators is nil.
func (n *Node) firstFailure(ctx context.Context, validators []Validator, presumes bool) error {
	for _, dv := range validators {
		// A typed nil pointer is no failure, as [Node.Bind] reads it, and
		// neither is a result of warnings alone.
		err := n.checkWith(ctx, dv.Check, presumes)
		if err != nil {
			return err
		}
	}

	return nil
}

// checkWith runs check on n, as [runHook] runs a hook. It returns the
// errors of the result as [splitWarnings] splits them, or nil when the
// result does not hold such an error. The collectors of ctx get the
// warnings of the result and the warnings that check hands to its
// context, bound as [Node.Bind] binds them. With presumes set, those
// warnings take the fault that [presume] puts on them first, as the
// warnings of a hook do.
func (n *Node) checkWith(ctx context.Context, check func(context.Context, *Node) error, presumes bool) error {
	failure, warnings := runHook(ctx, func(ctx context.Context) error {
		return check(ctx, n)
	})

	if presumes {
		warnings = presume(warnings)
	}

	deliver(ctx, n.Bind(warnings))

	return failure
}
