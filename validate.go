package niceyaml

import (
	"context"
	"math"
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

// SelfValidate runs the self-validation step of [Node.DecodeInto] on its
// own. It walks v, calls Validate on every value in it that implements
// [SelfValidator], runs each function of [WithValueCheck] in opts on the
// values of its type, and binds what they report as [Node.Bind] binds an
// error, with each `@` path under the path of its value in the node. It
// returns nil when nothing failed. It runs the walk whatever
// [WithSelfValidation] says, and runs no [Validator]. A program that
// fills v after the decode, such as from its environment, calls
// SelfValidate once v is whole, as the Validation section of the package
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
// On a value that nothing changed, SelfValidate returns what the decode
// with the walk on returns, given the same functions of WithValueCheck.
// To spell each map key as the document does, the walk decodes the keys
// with the settings of the [Source], the unmarshalers of
// [WithUnmarshalers] among them, and with the options in opts that say
// how a value decodes: [WithCustomUnmarshaler], [WithJSONUnmarshalers],
// [WithDisallowUnknownFields], [WithAllowedFieldPrefixes], and
// [WithYAMLOrderedMaps]. It reads no other option. A program that gives
// its unmarshalers to the Source thus passes none of them here, and a
// caller passes the other options of the decode again.
//
// In a document that did not parse, every error binds with no position,
// and SelfValidate returns those errors rather than the syntax error
// [Node.Err] returns. Any v but nil and a nil pointer works, and those
// return an error wrapping [ErrSelfValidateTarget], bound to the source.
// A v that is no pointer validates as a copy. The walk stops once ctx
// ends, as the Cancellation section of the package documentation
// describes.
func (n *Node) SelfValidate(ctx context.Context, v any, opts ...DecodeOption) error {
	c := &warningCollector{}

	return c.finish(ctx, n.selfValidate(c.context(ctx), v, newDecodeConfig(n.source, opts)), n.Bind)
}

// selfValidate is [Node.SelfValidate] with its settings resolved, and with
// the warnings of the walk handed to the warning handler of ctx.
// [Node.DecodeInto] runs it once the decode has filled v, so a decode and
// a later call of SelfValidate on the same value return the same error.
func (n *Node) selfValidate(ctx context.Context, v any, cfg decodeConfig) error {
	err := checkTarget(v, ErrSelfValidateTarget, false)
	if err != nil {
		return n.bindOwn(err)
	}

	failure, warnings := walkSelfValidators(ctx, v, n, cfg)

	reportWarning(ctx, n.Bind(warnings))

	return n.Bind(failure)
}

// SelfValidateValue runs the self-validation step of [Node.DecodeInto]
// on a value that came from no document, such as one the program built
// or the body of a request. It walks v as [Node.SelfValidate] does, and
// binds the result as [BindValue] binds an error, so the text names each
// failing path from v, as in "$.servers[1].port: port is required", and
// a document can still place the error. It returns nil when nothing
// failed.
//
// The walk names a field as a YAML decode names it, by its yaml tag, its
// json tag, or its lowercased name. A map key takes the text of its Go
// value, so where a document spells the key another way, such as 0x10
// for 16, an error below the entry binds at the key of the map. A caller
// that holds the document validates through the Node of the value with
// Node.SelfValidate instead. Several keys of one map that share a text,
// such as 1 and "1", bind with no position, as [ErrAmbiguousPath]
// describes.
//
// SelfValidateValue runs each function of [WithValueCheck] in opts on the
// values of its type, as Node.SelfValidate does, so a server checks the
// body of a request with the functions it gives a decode. It knows a type
// that decodes itself by its own unmarshaler method, and by
// [WithCustomUnmarshaler] and [WithJSONUnmarshalers] in opts. It reads no
// other option. The value has no [Source], so the unmarshalers of
// [WithUnmarshalers] do not reach the walk, and a caller passes them in
// opts.
//
// A Validate whose value holds fields a decode does not reach, such as
// one tagged `yaml:"-"`, validates them with SelfValidateValue and
// moves the result under their path with [Rebase]. It must not pass its
// own receiver, which would call that Validate again without end.
//
// Any v but nil and a nil pointer works, and those return an error
// wrapping [ErrSelfValidateTarget]. The walk stops once ctx ends, as the
// Cancellation section of the package documentation describes.
func SelfValidateValue(ctx context.Context, v any, opts ...DecodeOption) error {
	err := checkTarget(v, ErrSelfValidateTarget, false)
	if err != nil {
		return BindValue(err)
	}

	c := &warningCollector{}

	failure, warnings := walkSelfValidators(c.context(ctx), v, nil, newDecodeConfig(nil, opts))

	c.add(BindValue(warnings))

	return c.finish(ctx, BindValue(failure), BindValue)
}

// walkSelfValidators runs Validate on every [SelfValidator] in v, a non-nil
// pointer or a value that is no pointer, and the functions of cfg from
// [WithValueCheck] on the values of their types. It joins the errors under
// the path of each value in the document of n, where a nil n stands for no
// document, and returns the failures and then the warnings. A check whose
// result holds warnings alone passes.
func walkSelfValidators(ctx context.Context, v any, n *Node, cfg decodeConfig) (error, error) {
	w := selfWalker{
		ctx:      ctx,
		node:     n,
		cfg:      cfg,
		checked:  checkedTypesOf(cfg.checks),
		walked:   map[visit]walkResult{},
		reach:    math.MaxInt,
		scanning: map[visit]bool{},
		scanned:  map[visit]bool{},
	}
	w.walk(reflect.ValueOf(v), place{}, nil)

	if w.ended != nil {
		return w.ended, nil
	}

	return multierr.Join(w.errs), multierr.Join(w.warns)
}

// selfWalker collects the errors of the [SelfValidator] values in a decoded
// value, and of the functions from [WithValueCheck]. It walks each pointer,
// map, and slice once, so a value two paths share reports its errors under
// the first, and a back edge of a cycle passes.
type selfWalker struct {
	ctx     context.Context
	node    *Node
	decoder *yaml.Decoder
	// The settings of the decode, which name the types its options give
	// an unmarshaler and hold the functions from [WithValueCheck].
	cfg decodeConfig
	// The types the functions of cfg check, which the walk counts as
	// validators where it skips a value that holds none.
	checked *checkedTypes
	// The result of the walk through each pointer, map, and slice the walk
	// has entered, as [walkResult] describes.
	walked map[visit]walkResult
	// The values that finished inside a cycle whose first value the walk
	// is still inside of, in the order they finished.
	pending []visit
	// The pointers, maps, and slices the current scan has reached, so the
	// scan reads each once however many paths lead to it.
	scanning map[visit]bool
	// The result of the scan of each pointer, map, and slice. The result
	// for a pointer covers the value it points to, and the result for a
	// map or slice covers only the values below it.
	scanned map[visit]bool
	errs    []error
	// The warnings of the Validate methods that passed, which hold
	// warnings alone.
	warns []error
	// The error of a context that ended, once it stops the walk, as
	// [selfWalker.stopped] describes.
	ended error
	// The places of the values the walk is inside of that decode
	// themselves, the outermost first, as [selfWalker.enter] records them.
	within []place
	// The node of the value the walk starts at, held as a [step] holds the
	// node of its own value, once [selfWalker.nodeOf] resolves it.
	start step
	// The number of pointers, maps, and slices the walk has entered, which
	// numbers each one in turn.
	entered int
	// The lowest number of a value the walk is inside of that the values
	// below the current one lead back to, or math.MaxInt when none does.
	reach int
	// Whether the walk is below a map entry whose path names the entries
	// of several keys, as [selfWalker.walkEntries] finds one. The path of
	// each error the walk collects there is ambiguous.
	ambiguous bool
	// Whether the last of within is a slice, an array, or a map, so no path
	// below it says where its value sits in the document.
	sealed bool
}

// visit names a pointer, map, or slice by type and address, since a struct
// and its first field share one, and by length, since two slices can too.
//
//nolint:unused // The fields tell the keys of the walker's maps apart.
type visit struct {
	typ reflect.Type
	ptr unsafe.Pointer
	len int
}

// walkResult is the result of the walk through a pointer, map, or slice.
// While the walk is inside the value, it passes with the number of the
// value as its reach, so a value below that leads back joins its cycle.
type walkResult struct {
	// Whether nothing under the value failed.
	ok bool

	// The lowest number of a value the walk is inside of that the value
	// leads back to, or math.MaxInt once the result is final.
	reach int
}

// place is where a value of the walk lies, held as the last [step] below
// the start, which the zero place stands for. A [paths.Path] copies its
// selectors as it grows, so a place builds a path only to rebase an error.
type place struct {
	last *step
}

// step is one selector on the way to a value of the walk, where key marks
// a `~`. It keeps the node [selfWalker.nodeOf] resolves, and resolved tells
// a value the document did not set, with a nil node, from one not yet read.
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

// walk validates v and everything below it at the place at, and reports
// whether nothing under v failed. For an inline struct, or a pointer to
// one, shadowed names the fields of its parent that are not inline.
func (w *selfWalker) walk(v reflect.Value, at place, shadowed map[string]bool) bool {
	if !v.IsValid() {
		return true
	}

	v = exposed(v)

	// A value whose type holds no validator, itself included, passes
	// without a look at the values below it.
	if !w.checked.mayHold(v.Type()) {
		return true
	}

	if w.stopped() {
		return false
	}

	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return true
		}

		return w.walk(v.Elem(), at, nil)

	case reflect.Pointer, reflect.Map, reflect.Slice:
		// A nil map or slice validates here rather than through walked,
		// where every nil value of its type would share one record.
		if v.IsNil() {
			if v.Kind() == reflect.Pointer {
				return true
			}

			return w.validate(v, at)
		}

		if !ownsAddress(v) {
			if v.Kind() == reflect.Pointer {
				return w.walk(v.Elem(), at, shadowed)
			}

			return w.walkValue(v, at, shadowed)
		}

		return w.walkOwned(v, at, shadowed)

	default:
	}

	return w.walkValue(v, at, shadowed)
}

// walkValue walks v, which is no pointer or interface, as [selfWalker.walk]
// does. A struct it cannot address walks as a copy, so [exposed] can read
// its fields, and a struct an embedded field decodes walks that field alone.
func (w *selfWalker) walkValue(v reflect.Value, at place, shadowed map[string]bool) bool {
	if v.Kind() == reflect.Struct {
		v = addressable(v)
	}

	field, whole := w.cfg.unmarshalers.Whole(v.Type())

	mark, sealed := len(w.within), w.sealed
	if whole {
		w.enter(v.Kind(), at)
	}

	var ok bool

	if field >= 0 {
		ok = w.walk(v.Field(field), at, nil)
	} else {
		ok = w.children(v, at, shadowed)
	}

	ok = ok && w.validate(v, at)

	w.within, w.sealed = w.within[:mark], sealed

	return ok
}

// enter records the value at at, which decodes itself, as where an error
// below it binds when the document lacks the error's path. A slice, array,
// or map seals the record, since no tag says where its elements sit.
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
// the document does not hold its path: the values the walk is inside of
// that decode themselves, the nearest first.
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
// memory of v, which needs an address, as [selfWalker.walkValue] ensures.
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

// walkOwned walks v, a non-nil pointer, map, or slice that owns its address,
// once. It finds cycles as Tarjan's algorithm does, so the values of a cycle
// wait on pending until its first value finishes and sets all their results.
func (w *selfWalker) walkOwned(v reflect.Value, at place, shadowed map[string]bool) bool {
	key := visitOf(v)
	if r, seen := w.walked[key]; seen {
		w.reach = min(w.reach, r.reach)

		return r.ok
	}

	w.entered++
	n := w.entered
	outer, mark := w.reach, len(w.pending)

	w.walked[key] = walkResult{ok: true, reach: n}
	w.reach = math.MaxInt

	var ok bool

	if v.Kind() == reflect.Pointer {
		ok = w.walk(v.Elem(), at, shadowed)
	} else {
		ok = w.walkValue(v, at, shadowed)
	}

	reach := w.reach
	if reach < n {
		w.walked[key] = walkResult{ok: ok, reach: reach}
		w.pending = append(w.pending, key)
		w.reach = min(outer, reach)

		return ok
	}

	// Every value of a cycle reaches every other, so each one takes the
	// result of v.
	for _, p := range w.pending[mark:] {
		w.walked[p] = walkResult{ok: ok, reach: math.MaxInt}
	}

	w.pending = w.pending[:mark]
	w.walked[key] = walkResult{ok: ok, reach: math.MaxInt}
	w.reach = outer

	return ok
}

// ownsAddress reports whether the address of v, a non-nil pointer, map, or
// slice, names v alone. A v with zero-size data, or an empty slice, can
// share its address yet holds nothing that leads back, so it needs no record.
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
	// The result of [implementsSelfValidator] for each type it has read.
	ownsValidator unmarshaler.TypeCache[bool]

	// The set of no types, from which [checkedTypesOf] reaches every other.
	noCheckedTypes = &checkedTypes{}
)

// checkedTypes is a set of types that functions from [WithValueCheck]
// check, with the answers about each type that the walk prunes by. A
// validator of the walk is a value that implements [SelfValidator] or
// whose type the set holds. [checkedTypesOf] returns one instance for each
// set, so every walk with the same set shares the answers, and the walks
// with no function share those of [noCheckedTypes].
type checkedTypes struct {
	// The set that holds types and one more type, which sorts after them,
	// by that type.
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
// same instance. Two types with one name keep the order checks gives them,
// so those types in another order return a second instance, which holds
// answers of its own.
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

// with returns the set that holds the types of c and t, which sorts after
// them, and creates it when no walk has asked for it before.
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
// through the Validate of [SelfValidator] or a function of the set.
func (c *checkedTypes) validates(t reflect.Type) bool {
	return implementsSelfValidator(t) || slices.Contains(c.types, t)
}

// mayHold reports whether a value of type t, or one below it the walk
// reaches, can be a validator, as any interface can. It reads no value, so
// every walk with the set shares the answer, which can overreport.
func (c *checkedTypes) mayHold(t reflect.Type) bool {
	return c.holds.Get(t, func(t reflect.Type) bool {
		// The walk reads nothing below a node of the syntax tree.
		return yamlfield.Reaches(t, func(t reflect.Type) (bool, bool) {
			return t.Kind() == reflect.Interface || c.validates(t), unmarshaler.IsSyntaxNode(t)
		})
	})
}

// children walks the values below v and reports whether each passed. It
// skips the fields of an inline struct that shadowed names, since go-yaml
// zeroes an inline field that shares its name with a field of the parent.
func (w *selfWalker) children(v reflect.Value, at place, shadowed map[string]bool) bool {
	switch v.Kind() {
	case reflect.Slice, reflect.Array, reflect.Map:
		// Entries whose types hold no validator pass at once, as does a map
		// whose entries hold none, so the walk reads no key of such a map.
		if !w.checked.entriesMayHold(v.Type()) || v.Kind() == reflect.Map && !w.holdsBelow(v) {
			return true
		}

	default:
	}

	ok := true

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

			if !w.walk(v.Field(field.index), child, fieldShadowed) {
				ok = false
			}
		}

	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			if !w.walk(v.Index(i), at.index(i), nil) {
				ok = false
			}
		}

	case reflect.Map:
		// [datapath.MapGroups] orders the entries by key, so the errors come
		// back in one order however the map iterates.
		names := w.keyNames(w.nodeOf(at.last), v.Type().Key())

		// Keys that share a segment share a path, which can locate one of
		// their entries at most, so the errors under it bind with no position.
		for _, group := range datapath.MapGroups(v, names) {
			if !w.walkEntries(at.child(group.Seg), group.Entries, group.Ambiguous) {
				ok = false
			}
		}

	default:
	}

	return ok
}

// walkEntries walks entries, which share the place at, and reports whether
// nothing under them failed. It sorts the errors of each entry as a group by
// their text, since the keys share a text and formatting a value may not end.
// A value several entries share walks once, under the first the map yields.
// When ambiguous is true, it binds the errors first so the text names paths.
func (w *selfWalker) walkEntries(at place, entries []datapath.MapEntry, ambiguous bool) bool {
	ok := true
	start := len(w.errs)
	groups := make([][]error, 0, len(entries))

	// Every path below an ambiguous entry is ambiguous too, except below a
	// sealed value, where errors bind at that value and no path locates them.
	outer := w.ambiguous
	w.ambiguous = outer || (ambiguous && !w.sealed)

	for _, e := range entries {
		if !w.walk(e.Key, at.key(), nil) {
			ok = false
		}

		if !w.walk(e.Value, at, nil) {
			ok = false
		}

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

	return ok
}

// bind binds err where the walk binds an error before it returns: through
// the [Node] of the walk, as [Node.bindOwn] binds it, or to no document
// when the walk has no Node, as [BindValue] binds it.
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
	// [yamlfield.OwnNames] returns them, or nil when no field in held
	// is inline.
	own map[string]bool

	// The fields that go-yaml decodes and whose types can hold a
	// validator, in the order the struct declares them.
	held []heldField
}

// heldField is a field of a struct whose type can hold a validator.
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

// fieldsOf returns the fields of t, a struct type, that the walk reads,
// which leaves out each field go-yaml skips or that holds no validator.
// Every walk with the set shares the result, so a caller must not change
// it.
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

// entriesMayHold reports whether an element of a value of type t, a
// slice, array, or map, or a key or value of a map, can hold a validator,
// as [checkedTypes.mayHold] reads it.
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

// scanValue reports whether the walk would validate v or a value below it.
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

// scanSelf scans v, a value that is no pointer or interface, as
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
// validator the walk never reaches, which only costs the walk a look.
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

// implementsSelfValidator reports whether t declares the Validate of
// [SelfValidator] on its value or its pointer. A struct that gets Validate
// from an embedded field does not, so the walk validates that field instead.
func implementsSelfValidator(t reflect.Type) bool {
	return ownsValidator.Get(t, func(t reflect.Type) bool {
		return unmarshaler.Declares(t, reflect.TypeFor[SelfValidator]())
	})
}

// validate runs Validate on v, through a copy when v has no address, and
// then each function of the walk from [WithValueCheck] for the type of v,
// in order. It reports whether v passed, and stops at the first that
// fails. Rebasing the error of Validate or of a function under at presumes
// every problem invalid, unless an Error from [Unchecked] sits on its
// cause chain. Each such problem matches [errInvalid] whether it has a
// location or not.
func (w *selfWalker) validate(v reflect.Value, at place) bool {
	if !w.checked.validates(v.Type()) {
		return true
	}

	if w.stopped() {
		return false
	}

	v = addressable(v)

	if implementsSelfValidator(v.Type()) {
		validator, ok := reflect.TypeAssert[SelfValidator](v.Addr())
		if ok && !w.report(validator.Validate(), at, presumed) {
			return false
		}
	}

	for _, check := range w.cfg.checks {
		if check.typ != v.Type() {
			continue
		}

		if w.stopped() || !w.report(check.run(w.ctx, v.Addr()), at, presumed) {
			return false
		}
	}

	return true
}

// report records err, the result of a check of one value, rebased with d
// under the place of that value, and reports whether the value passed.
// The error of a context that ended stops the walk, as
// [selfWalker.stopped] describes.
func (w *selfWalker) report(err error, at place, d declaration) bool {
	if isNothing(err) {
		return true
	}

	if contextEnded(err) {
		w.ended = err

		return false
	}

	m := move{base: at.path(), ambiguous: w.ambiguous, fallback: w.fallback()}
	rebased := rebase(err, m, d)

	// A check returns its warnings beside its errors, and a result of
	// warnings alone passes, so the values above it still validate.
	if !HasErrors(err) {
		w.warns = append(w.warns, rebased)

		return true
	}

	w.errs = append(w.errs, rebased)

	return false
}

// stopped reports whether the walk has stopped, which it does once its
// context ends, or once a Validate or a function from [WithValueCheck]
// returns the error of a context that ended. The first such error
// replaces every other error of the walk.
func (w *selfWalker) stopped() bool {
	if w.ended == nil {
		w.ended = w.ctx.Err()
	}

	return w.ended != nil
}

// nodeOf returns the node the value at the end of s decoded from, or nil
// when the document holds none there. A nil s stands for the walk's start.
// Each step resolves once, from the node of the step before.
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
// entries that set one key, merges included, the one the decode keeps wins.
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

// keyDecoder returns the go-yaml decoder for the keys of the walk, and
// creates it with the options of the decode when the walk first decodes
// a key.
func (w *selfWalker) keyDecoder() *yaml.Decoder {
	if w.decoder == nil {
		w.decoder = w.node.newDecoder(w.cfg)
	}

	return w.decoder
}

// addKeyName adds the text of key to names under the value key decodes to
// as type t, unless names holds that value. As in go-yaml, only a null key
// or an alias to one, not a key such as `&a ~`, leaves a pointer t nil.
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

	err := decodeWithRecover(w.ctx, w.keyDecoder(), node, decoded.Interface())
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
// CUE, OPA, or another JSON Schema library, names what it finds by the
// member names and indices of that data. A path matches a key by its
// source text instead, so [AtPath] of those names misses a key the
// decoder respells, such as the key 0x10 of the member 16. A DataLocator
// reads the names as the decoder does, and its options give an error the
// location [go.jacobcolvin.com/niceyaml/schema.Schema] gives a violation
// of the same value:
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
// key gives its text, and any other key gives its Go value as [fmt.Sprint]
// prints it, so 0x10 is 16, 3.10 is 3.1, and True is true. An element has
// its index in decimal digits, as [strconv.Itoa] writes it. A decode into
// another type can name a key another way, such as 1e3 for the key 1000
// of a map[int]string, and such a name leads nowhere. A check that
// reports JSON Pointers splits each pointer into its names and unescapes
// `~1` and `~0` in each.
//
// A DataLocator is safe for concurrent use, and it reads each mapping
// once, so a check creates one for all the findings of a run. Its reads
// of `<<` merge keys share one budget, as large as
// [paths.ErrExcessiveMerging] allows one path selector, so a check
// creates a DataLocator for each run and keeps none for the life of the
// document.
type DataLocator struct {
	// The node the names read from.
	root ast.Node
	// Finds the members of each mapping by the names a decode gives them.
	idx *datapath.Index
	// The `$` path to root.
	base paths.Path
	// Guards idx, which no two walks may read at once.
	mu sync.Mutex
}

// DataLocator creates a new [*DataLocator] for the data the Node decodes
// to. The names it reads start at the Node, as the data from
// [Node.Decode] does, so a [Validator] creates one from the Node it got
// whatever the scope of that Node. Each path it writes starts at `$`, so
// the errors bind through any Node of the document.
//
// A document that did not parse decodes to no data, as [Node.Err]
// describes, so every name then leads nowhere and each error binds with
// no position.
func (n *Node) DataLocator() *DataLocator {
	return &DataLocator{
		root: n.AST(),
		idx:  datapath.NewIndex(n.Resolver()),
		base: n.base,
	}
}

// At returns an [ErrorOption] that locates an error at the value the
// names lead to in the data of the [DataLocator], and no names lead to
// the whole of that data. The option sets the `$` path of the value, as
// [AtPath] does, with each key spelled as the source spells it:
//
//	niceyaml.NewError("must not be empty", loc.At("ports", "16", "name"))
//	// cfg.yaml:3:11: $.ports.0x10.name: must not be empty
//
// Where several keys of a mapping decode to one name, the names lead to
// the last of them, whose value the decode keeps. Where no path selects
// the value, since another key spells the same text, the option also sets
// its position, as [AtPosition] does, and the path names the member as
// the decoder does.
//
// Names that lead past the data keep the form the caller wrote from there
// on, and the error binds where that path binds, as [SourceError.Nearest]
// describes. A member a mapping lacks binds at the key of the mapping,
// and an index past the end of a sequence or a name below a scalar binds
// with no position. A name behind an alias the document cannot follow,
// such as one to an anchor of a document from [WithReferences], keeps its
// form too, and the option sets no position. A name that matches no
// member but whose text a path selects, such as 3.10 for the member 3.1,
// binds at the last value the names lead to.
func (l *DataLocator) At(names ...string) ErrorOption {
	return l.option(names, false)
}

// AtKey returns an [ErrorOption] that locates an error at the key of the
// mapping entry the names lead to, which suits an error about the key
// itself, such as an unknown member. It reads the names as
// [DataLocator.At] does, and the path it sets ends in `~`, as one from
// [paths.Path.Key] does:
//
//	niceyaml.NewError("unknown port", loc.AtKey("ports", "16"))
//	// cfg.yaml:2:3: $.ports.0x10~: unknown port
//
// An element of a sequence and the data as a whole have no key, so the
// error binds at the value there. A member the data lacks has no key
// either, so the option locates it as [DataLocator.At] does.
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
// unless [WithSelfValidation] turns that off. Validate reports a problem
// as an [*Error] with an `@` path, which reads from the value, so a type
// checks its invariants once and reports the right lines in any
// document:
//
//	func (h Hours) Validate() error {
//		if h.Close.Before(h.Open) {
//			return niceyaml.NewError("closes before it opens", niceyaml.AtPath(paths.Current().Child("close")))
//		}
//
//		return nil
//	}
//
// A decode presumes the document at fault for an error a Validate returns
// that declares nothing, a plain I/O error included. A check that can
// fail for reasons outside the document returns such an error inside
// [Unchecked], which turns the presumption off, as the Errors section of
// the package documentation describes. The Validation section describes
// the order the values validate in, the path of each value, and where
// each error binds.
type SelfValidator interface {
	Validate() error
}

// Validator validates a [*Node] before it decodes, as a JSON schema does.
// Pass one to a decode with [WithValidator], or run one on its own with
// [Node.Validate]. The context carries cancellation and deadlines to a
// validator that does work it can stop, such as resolving a remote schema
// reference.
//
// See [ValidatorFunc], [MultiValidator], [ChainValidator], [SkipEmpty],
// [go.jacobcolvin.com/niceyaml/schema.Schema], and
// [go.jacobcolvin.com/niceyaml/schema.Registry] for implementations.
//
// Check gets the Node that runs it, which is the root of a document or a
// Node from [Node.At], and returns its errors as it wrote them from that
// Node, with `@` paths that read from the Node. An error about a key the
// document lacks carries the path of the key. Node.Validate and a decode
// bind what Check returns as [Node.Bind] binds an error. The validators
// of this module return their errors unbound, apart from schema.Registry,
// which binds every error it returns. A validator that needs the whole
// document reaches it through [Node.Document]. The Validation section of
// the package documentation describes how a validator declares a fault
// and how one checks decoded data.
//
// A decode and Node.Validate return the syntax error of a document that
// did not parse before any validator runs. A caller that calls Check
// itself on such a Node gets the syntax error from Node.Decode, Node.At,
// and Node.Nodes, and the validators of this module return it before
// they read the Node.
type Validator interface {
	Check(ctx context.Context, n *Node) error
}

// ValidatorFunc adapts a function to the [Validator] interface. The
// function writes its errors from the Node it gets, as Validator
// describes, and [ValidatorFunc.Check] returns what the function returns,
// so the same body works as the Check method of a type:
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
// A check that knows nothing of YAML returns a plain error, which
// declares no fault. The function declares the document at fault for that
// error by returning the check inside [Invalid], which returns nil for a
// value that passes:
//
//	return niceyaml.Invalid(check(v))
type ValidatorFunc func(ctx context.Context, n *Node) error

// Check implements [Validator]. It runs f with a context of its own and
// holds the warnings that f reports with [Node.Warn] until it knows the
// outcome. When f returns an error, Check returns that failure with the
// warnings it holds added to it, under a heading that counts both, as
// "2 errors, 1 warning". Without a warning, the failure comes back as f
// wrote it. A result that holds warnings alone counts as a pass, so Check
// takes those warnings in, hands every warning it holds to the warning
// handler of ctx, and returns nil. For a document that did not parse, it
// returns the syntax error [Node.Err] returns without calling f.
func (f ValidatorFunc) Check(ctx context.Context, n *Node) error {
	if n.doc.err != nil {
		return n.doc.err
	}

	c := &warningCollector{}

	err := f(c.context(ctx), n)
	if HasErrors(err) {
		return c.merge(err, unbound)
	}

	if !isNothing(err) {
		c.add(n.Bind(err))
	}

	return c.finish(ctx, nil, n.Bind)
}

// MultiValidator returns a [Validator] that runs every validator in
// order and reports every failure, where [ChainValidator] stops at the
// first. It suits rules that check a document independently, as the
// rules of a linter do:
//
//	err := doc.Validate(ctx, niceyaml.MultiValidator(schema, names))
//
// Each validator sees the node whatever the ones before it reported. The
// run skips a nil validator, and one that holds a nil pointer or func.
// No failure is no error, and a lone failure comes back as that validator
// wrote it. Two or more come back joined in the order given, so
// [errors.Is] matches any one of them, and the Node that runs the
// MultiValidator binds each at its own location, as [Node.Bind]
// describes:
//
//	svc.yaml:1:7: $.name: reserved name
//	svc.yaml:2:7: $.port: 0 is less than 1
//
// A context that ends stops the run, as the Cancellation section of the
// package documentation describes. MultiValidator copies the validators
// it gets, so a caller that edits the slice it passed changes nothing in
// the Validator.
func MultiValidator(validators ...Validator) Validator {
	validators = slices.Clone(validators)

	return ValidatorFunc(func(ctx context.Context, n *Node) error {
		var errs []error

		for _, dv := range validators {
			if nilness.IsNil(dv) {
				continue
			}

			err := ctx.Err()
			if err != nil {
				return err //nolint:wrapcheck // The context names the reason, and the Node that runs the validator binds it.
			}

			// A typed nil pointer is no failure, as [Node.Bind] reads it, and
			// neither is a result of warnings alone.
			err = dv.Check(ctx, n)
			if !HasErrors(err) {
				if !isNothing(err) {
					reportWarning(ctx, n.Bind(err))
				}

				continue
			}

			if ctx.Err() != nil {
				return err //nolint:wrapcheck // The validator's own error, which the Node that runs the validator binds.
			}

			errs = append(errs, err)
		}

		return multierr.Join(errs)
	})
}

// ChainValidator returns a [Validator] that runs the validators in order
// and stops at the first that fails, where [MultiValidator] runs every
// one. It suits a check that needs an earlier one to pass, such as a
// check that follows the references of a document once a schema has
// accepted its shape:
//
//	err := doc.Validate(ctx, niceyaml.ChainValidator(schema, refs))
//
// A validator runs only after every validator before it has passed, so
// one that decodes the node reports no value the schema already
// rejected. The run skips a nil validator, and one that holds a nil
// pointer or func, and a ChainValidator of no validators passes every
// Node. A decode runs the validators of repeated [WithValidator] options
// the same way.
//
// The failure comes back as the validator wrote it, so the Node that
// runs the ChainValidator binds it as it binds the failure of that
// validator alone. ChainValidator copies the validators it gets, so a
// caller that edits the slice it passed changes nothing in the Validator.
func ChainValidator(validators ...Validator) Validator {
	validators = slices.Clone(validators)

	return ValidatorFunc(func(ctx context.Context, n *Node) error {
		return n.firstFailure(ctx, validators)
	})
}

// SkipEmpty returns a [Validator] that passes a document with no content,
// which [Node.IsEmpty] reports, and runs v on every other Node. A caller
// whose file may be empty as a whole wraps its validator, so an empty
// file validates, and a decode of it returns the zero value:
//
//	err := source.ValidateDocuments(ctx, niceyaml.SkipEmpty(reg))
//
// A document that holds any value runs v, so v still checks an explicit
// null such as `--- null` and an alias with no anchor before it. A Node
// that [Node.At] or [Node.Nodes] scopes below the root is no document, so
// v runs on it. A document that did not parse returns its syntax error.
//
// SkipEmpty returns the error of v as v wrote it, and a nil v passes
// every Node. It takes one validator, so it wraps the one that
// [MultiValidator] or [ChainValidator] makes of several.
func SkipEmpty(v Validator) Validator {
	return ValidatorFunc(func(ctx context.Context, n *Node) error {
		if n.IsEmpty() || nilness.IsNil(v) {
			return nil
		}

		//nolint:wrapcheck // The validator's own error, which the Node that runs the validator binds.
		return v.Check(ctx, n)
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
// document with no content pass.
//
// Validate binds what v returns through [Node.Bind], so an [*Error]
// renders its location with its `@` paths read from the node, and an
// error v bound itself comes back as it is. A Node from [Node.At] or
// [Node.Nodes] points an error with no location at its own value. A
// validator that runs another on a Node of its choosing, such as each
// element of a list, calls Validate on that Node, where a call to Check
// returns the error unbound.
//
// A document that did not parse returns the syntax error [Node.Err]
// returns before v runs, even when v is nil. Otherwise a nil v, or one
// that holds a nil pointer or func, runs nothing and passes, and so does
// a validator that returns a nil [*Error] or [*SourceError] pointer.
func (n *Node) Validate(ctx context.Context, v Validator) error {
	c := &warningCollector{}

	return c.finish(ctx, n.validate(c.context(ctx), []Validator{v}), n.Bind)
}

// validate runs validators in order on n, and binds the first error with
// n when the validator left it unbound. A document that did not parse
// returns its syntax error, and no validator runs. The warnings of the
// validators that pass go to the warning handler of ctx.
func (n *Node) validate(ctx context.Context, validators []Validator) error {
	if n.doc.err != nil {
		return n.doc.err
	}

	return n.Bind(n.firstFailure(ctx, validators))
}

// firstFailure runs validators in order on n and returns the first
// failure as the validator wrote it, or nil when each of them passes. It
// skips a nil validator, and one that holds a nil pointer or func.
func (n *Node) firstFailure(ctx context.Context, validators []Validator) error {
	for _, dv := range validators {
		if nilness.IsNil(dv) {
			continue
		}

		// A typed nil pointer is no failure, as [Node.Bind] reads it, and
		// neither is a result of warnings alone.
		err := dv.Check(ctx, n)
		if !HasErrors(err) {
			if !isNothing(err) {
				reportWarning(ctx, n.Bind(err))
			}

			continue
		}

		return err //nolint:wrapcheck // The validator's own error, which the Node that runs the validator binds.
	}

	return nil
}
