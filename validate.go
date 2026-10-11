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
// own. The step walks v, calls Validate on every value in it that
// implements [SelfValidator], and puts the `@` paths each one reports
// under the path of the value in the node. SelfValidate binds the result
// as [Node.Bind] binds an error, which a decode does too once it has
// filled its target, and returns nil when nothing failed. It runs the
// walk whatever [WithSelfValidation] says, and runs no [Validator].
//
// # Environment and Flags
//
// A program that can list what its environment or its flags set passes
// those values as a layer of [NewSourceFromLayers], which validates them as it
// validates a file. SelfValidate is for the program whose library writes
// them into the value instead, or that applies its defaults there. That
// program decodes the file with the walk off, sets the rest of the
// value, and then validates what it holds, so a required field that only
// the environment sets passes:
//
//	settings := niceyaml.DecodeOptions(niceyaml.WithJSONUnmarshalers(true))
//
//	var cfg Config
//	if err := doc.DecodeInto(ctx, &cfg, settings, niceyaml.WithSelfValidation(false)); err != nil {
//		return err
//	}
//
//	applyEnv(&cfg)
//
//	return doc.SelfValidate(ctx, &cfg, settings)
//
// Both calls take settings, since the walk reads the options that say
// how a value decodes, as Decode Options below describes.
//
// No [Validator] of the decode sees a value set this way. A schema thus
// leaves the value unchecked, and reports a key it requires as missing
// when only the environment sets it.
//
// # Layers
//
// A program that layers one file over another merges them through
// [NewSourceFromLayers]. [Source.SelfValidate] on the merged Source then
// binds each error in the file that holds its value, where a
// SelfValidate through the Node of one file binds every error in that
// file:
//
//	merged, err := niceyaml.NewSourceFromLayers(ctx, base, prod)
//	if err != nil {
//		return err
//	}
//
//	return merged.SelfValidate(ctx, &cfg)
//
// # Decode Options
//
// On a value that no layer changed, SelfValidate returns what the decode
// with the walk on returns. The walk spells the key of each map entry as
// the document does, so an error under a key such as 1.50 keeps that
// text, and it decodes the keys of the mappings in the node to learn that
// spelling. It decodes them with the settings of the [Source], such as
// the reference documents of [WithReferences], and with the options in
// opts that say how a value decodes: [WithCustomUnmarshaler],
// [WithJSONUnmarshalers], [WithDisallowUnknownFields],
// [WithAllowedFieldPrefixes], and [WithYAMLOrderedMaps]. It reads those
// options and no other. A caller passes the options of the decode, and
// the walk then reads each type they give an unmarshaler as the decode
// did. A key type that a WithCustomUnmarshaler function decodes matches
// no key of the document unless opts carry that option, and an error
// under such an entry then binds at the key of the map.
//
// # Error Binding
//
// The walk follows v rather than the document, so v need not mirror the
// node, and each error binds where its path resolves in the document. An
// error under a field or map entry that the document lacks binds at the
// key of the mapping that lacks it, as [Node.Bind] binds the path of a
// missing key. An error under a value that an alias reads from a
// reference document binds at that alias, as [SourceError.Nearest]
// describes. An error under an element that the document lacks, such
// as one the caller appended to a slice, binds with no position. An error
// under a value the caller replaced, or under an element of a slice it
// reordered or grew at the front, marks what the document holds at that
// path. An error about a port the environment set to 0 thus marks the
// 8080 of the file.
// The keys of a map that the document lacks take the text of their
// Go values. A field that go-yaml never decodes does not validate, as in
// a decode. That holds for a field tagged `yaml:"-"` and for an
// unexported field.
//
// The values below a value that decodes itself validate as in a decode.
// An error at or below such a value binds at that value where an error
// below any other value binds with no position, and every error below a
// slice, an array, or a map that decodes itself binds at it.
// [SelfValidator] describes both rules. They hold for a type with an
// UnmarshalYAML or UnmarshalText method, and for one that
// WithCustomUnmarshaler or WithJSONUnmarshalers gives an unmarshaler in
// opts.
//
// # Target
//
// Any v works but nil and a nil pointer, which each return an error
// wrapping [ErrSelfValidateTarget], bound to the source. A v that is no
// pointer validates as a copy, so a Validate with a pointer receiver
// changes the copy and leaves v as it was.
//
// # Unparsed Documents
//
// A document that did not parse has no tree. Every key of v then takes
// the text of its Go value, and each error binds with no position, as
// [Node.Bind] describes. SelfValidate returns those errors rather than
// the syntax error [Node.Err] returns.
//
// # Optional Files
//
// A program whose file is optional validates through an empty [Source]
// when the file is missing. [Source.SelfValidate] runs the walk through
// the one document of a Source, so the program makes the same call with
// or without the file:
//
//	return niceyaml.NewSourceFromString("").SelfValidate(ctx, &cfg)
//
// Each error then binds with no position too, and its text names the
// path from v, as in "$.servers[1].port: port is required". The error
// is bound to that empty document, so no other document places it. A
// value that came from no document, such as one a Validate walks,
// validates through [SelfValidateValue], whose errors a document can
// still place.
//
// # Cancellation
//
// The walk stops once ctx ends, or once a Validate returns the error of a
// context that ended, and SelfValidate then returns that error alone, as
// [SelfValidator] describes.
func (n *Node) SelfValidate(ctx context.Context, v any, opts ...DecodeOption) error {
	return n.selfValidate(ctx, v, newDecodeConfig(opts))
}

// selfValidate is [Node.SelfValidate] with its settings resolved.
// [Node.DecodeInto] runs it once the decode has filled v, so a decode and
// a later call of SelfValidate on the same value return the same error.
// It binds what the walk returns as [Node.Bind] does.
func (n *Node) selfValidate(ctx context.Context, v any, cfg decodeConfig) error {
	err := checkTarget(v, ErrSelfValidateTarget, false)
	if err != nil {
		return n.bindOwn(err)
	}

	return n.Bind(walkSelfValidators(ctx, v, n, cfg))
}

// SelfValidateValue runs the self-validation step of [Node.DecodeInto]
// on a value that came from no document, such as a value the program
// built or the body of a request. The step walks v and calls Validate on
// every value in it that implements [SelfValidator], as
// [Node.SelfValidate] walks a value through its document.
// SelfValidateValue binds the result as [BindValue] binds an error, so
// the text names each failing path from v, as in
// "$.servers[1].port: port is required". It returns nil when nothing
// failed.
//
// # Skipped Fields
//
// The result stands in no document, so a document can still place it,
// as BindValue describes. A decode places the result a Validate returns
// at the value that owns the method. A Validate holds no [Node], so one
// that validates values the decode does not reach calls
// SelfValidateValue. A struct that decodes itself holds such values in a
// field it tags `yaml:"-"`, as it tags one that it fills from a place no
// tag can name. The Pool below reads its servers from the servers key
// under spec:
//
//	type Pool struct {
//		Servers []Server `yaml:"-"`
//	}
//
//	func (p *Pool) Validate() error {
//		err := niceyaml.SelfValidateValue(context.Background(), p.Servers)
//
//		return niceyaml.Rebase(err, paths.Current().Child("spec", "servers"))
//	}
//
// A decode of a document that holds the Pool under pool then reports
// "app.yaml:5:7: $.pool.spec.servers[1].port: port is required". A decode
// reaches every field with any other tag itself, as [SelfValidator]
// describes, so the Validate of such a struct leaves those fields to it.
// A Validate must not pass its own receiver, since the walk then calls
// that Validate again, without end.
//
// # Field Names
//
// The walk names each value as a decode of YAML into v names it. A
// field takes its yaml tag, its json tag when it has no yaml tag, and
// its lowercased name when it has neither. A struct embedded with no
// inline option is such a field, under the lowercased name of its type.
// A value that another format decoded, such as a JSON body, may thus
// report paths that its own keys do not spell. A field that go-yaml
// never decodes does not validate, as Node.SelfValidate describes.
//
// # Map Keys
//
// No document spells the key of a map entry, so each key takes the text
// of its Go value. A document that spells a key another way, such as
// 0x10 for 16, holds no entry under that text, so an error below the
// entry binds at the key of the map, as [Rebase] describes. A caller
// that holds the document therefore validates through the Node of the
// value, which reads each key as the document spells it:
//
//	request, err := doc.At(paths.Doc().Child("request"))
//	if err != nil {
//		return err
//	}
//
//	return request.SelfValidate(ctx, &cfg.Request)
//
// Several keys of one map that share a path, such as 1 and "1" in a
// map[any]T, name no single entry. An error under such a key keeps its
// path and binds with no position in every document that places it, for
// the reason [ErrAmbiguousPath]. Below a slice, an array, or a map that
// decodes itself, it binds at that value, as every error there does.
//
// # Target
//
// Any v works but nil and a nil pointer, which each return an error
// wrapping [ErrSelfValidateTarget], bound to no document too.
//
// # Cancellation
//
// The walk stops once ctx ends, or once a Validate returns the error of a
// context that ended, and SelfValidateValue then returns that error
// alone, as [SelfValidator] describes.
//
// # Decode Options
//
// SelfValidateValue takes no [DecodeOption]. A walk through a document
// reads those options to decode its keys, and to learn which types
// [WithCustomUnmarshaler] or [WithJSONUnmarshalers] gives an
// unmarshaler. This walk reads such a type as one that decodes field by
// field, and knows a type that decodes itself by its own method alone.
// A document that places the result thus binds an error at a value that
// decodes itself, as [SelfValidator] describes, only for a type with
// such a method.
func SelfValidateValue(ctx context.Context, v any) error {
	err := checkTarget(v, ErrSelfValidateTarget, false)
	if err != nil {
		return BindValue(err)
	}

	return BindValue(walkSelfValidators(ctx, v, nil, decodeConfig{}))
}

// walkSelfValidators runs Validate on every value in the tree of v that
// implements [SelfValidator], where v is a non-nil pointer or a value
// that is no pointer. The walk follows v rather than n, so v need not be
// the value n decoded to. It reads n for the text the document spells
// each map key with, and decodes those keys with ctx and cfg, as the
// decode of v did. It also reads n to bind the errors under a path that
// names several keys. A map the document does not hold at its path has no
// node there, so its keys take the text of their Go values. A nil n
// stands for no document, as [SelfValidateValue] walks a value, so every
// key takes that text and the walk reads nothing of cfg.
//
// The walk returns what the values report with the paths in each error
// rebased under the path of the value in the document. That path is the
// field name go-yaml decoded it under, the index of a slice or array
// element, or the key of a map entry as the document spells it. A map
// key validates at the path of its entry with a `~` after it, so its
// errors point at the key rather than the value. The values below a value
// validate before it does, and a value validates only when every value
// below it passed, so a parent that checks a relation between its fields
// sees fields that hold together. A value whose type decodes itself,
// through an unmarshaler method or one of unmarshalers, walks as any
// other. Its fields need not mirror the document, so each error at or
// below it carries the path of the value as a [fallback], where the
// error binds when the document does not hold its own path. A node of
// the syntax tree, which go-yaml sets whole, validates nothing below it.
// A struct that decodes itself through a method it gets from an embedded
// field decodes the document into that field, so that field alone
// validates below it, at the path of the struct. Several errors come
// back joined, one per value that failed. Returns nil when nothing
// failed.
//
// The walk stops once ctx ends, or once a Validate returns the error of a
// context that ended, and returns that error alone, as it is, in place of
// the errors it collected.
func walkSelfValidators(ctx context.Context, v any, n *Node, cfg decodeConfig) error {
	w := selfWalker{
		ctx:      ctx,
		node:     n,
		cfg:      cfg,
		walked:   map[visit]walkResult{},
		reach:    math.MaxInt,
		scanning: map[visit]bool{},
		scanned:  map[visit]bool{},
	}
	w.walk(reflect.ValueOf(v), place{}, nil)

	if w.ended != nil {
		return w.ended
	}

	return multierr.Join(w.errs)
}

// selfWalker collects the errors of the [SelfValidator] values in a
// decoded value. It records the pointers, maps, and slices on the path it
// is walking down, so a value that refers back to one above it stops
// there, and counts that one as passing. The values of such a cycle lead
// to each other, so each takes the result of the first value of the
// cycle the walk entered, once that value finishes. A back edge thus
// counts as passing only for the values inside the cycle. The walker
// records the result of each value it has walked, so a value two paths
// share, as an alias makes one, walks once and reports its errors under
// the first path. A parent on the second path still learns that the value
// failed, even when a cycle holds the value. It reads the keys of a map
// from the node the value decoded from, with the context and options it
// decoded with. It finds that node through the [paths.Resolver] of the
// document, which binds the aliases of the document once. It resolves
// that node one step at a time from the node of the value above, as
// [step] describes. It keeps the node of each step, so it resolves each
// step on the way to a map once, however many maps lie below the step.
// One go-yaml decoder decodes every key, so the walk applies the
// options, and reads any reference files they name, once too.
//
// Before it reads the keys of a map, the walker scans the values below
// for one that implements [SelfValidator], and passes the map at once
// when none does. The values go-yaml decodes into an interface never
// implement it, so a map decoded as any walks no further than the scan.
type selfWalker struct {
	ctx     context.Context
	node    *Node
	decoder *yaml.Decoder
	// The settings of the decode, which name the types its options give
	// an unmarshaler.
	cfg decodeConfig
	// The result of the walk through each pointer, map, and slice the walk
	// has entered, as [walkResult] describes.
	walked map[visit]walkResult
	// The values that finished inside a cycle whose first value the walk
	// is still inside of, in the order they finished.
	pending []visit
	// The pointers, maps, and slices the current scan has reached, so the
	// scan reads each once however many paths lead to it.
	scanning map[visit]bool
	// The result of the scan of each pointer, map, and slice, so each
	// value scans once however many values above it the walk meets. The
	// result for a pointer covers the value it points to, and the result
	// for a map or slice covers only the values below it.
	scanned map[visit]bool
	errs    []error
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

// visit names a pointer, map, or slice the walker is inside of. It names
// the value by type and address together, since a struct and its first
// field share an address, and by length for a slice, since two slices
// can start at one element. The fields together form the map key.
//
//nolint:unused // The fields tell the keys of the walker's maps apart.
type visit struct {
	typ reflect.Type
	ptr unsafe.Pointer
	len int
}

// walkResult is the result of the walk through a pointer, map, or slice.
// While the walk is inside of the value, the result passes and holds the
// number of the value as its reach, so a value below that leads back to
// it passes there and joins its cycle.
type walkResult struct {
	// Whether nothing under the value failed.
	ok bool

	// The lowest number of a value the walk is inside of that the value
	// leads back to, or math.MaxInt once the result is final.
	reach int
}

// place is where a value of the walk lies, held as the last [step] on the
// way down from the value the walk starts at. A [paths.Path] copies its
// selectors each time it grows, so a place builds the path of the value
// only for an error of the value to rebase under. A step thus costs the
// same however deep the value lies.
//
// The zero value is the place of the value the walk starts at.
type place struct {
	last *step
}

// step is one selector on the way to a value of the walk, held as a path
// of that selector alone. The prev field holds the step before it, or nil
// for the first step below the value the walk starts at. The key field
// reports whether the selector is the `~` of [paths.Path.Key], which
// follows the step of a map entry.
//
// The walk reads the keys of a map from the node the map decoded from,
// and [selfWalker.nodeOf] resolves the node of a step from the node of
// the step before. The node field holds the node once the walk resolves
// it, and the resolved field reports whether it has, since a value the
// document did not set has a nil node. The walk thus resolves each step
// once, however many maps below the step need its node.
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

// walk validates v and everything below it, with at as the place of v
// in the document, and reports whether nothing under v failed. When v is
// an inline struct, or a pointer to one, shadowed holds the names of the
// fields of its parent that are not inline, as [selfWalker.children]
// describes.
func (w *selfWalker) walk(v reflect.Value, at place, shadowed map[string]bool) bool {
	if !v.IsValid() {
		return true
	}

	v = exposed(v)

	// A value whose type holds no validator, itself included, passes
	// without a look at the values below it.
	if !mayHoldValidator(v.Type()) {
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
		// A nil pointer holds no value to validate. A nil map or slice is
		// an empty value with nothing below it, so it validates here rather
		// than through walked, where every nil value of its type would
		// share one record.
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

// walkValue validates v, a value that is no pointer or interface, and
// everything below it, and reports whether nothing under v failed. The
// shadowed names are those [selfWalker.walk] takes. A struct the walk
// cannot take the address of, such as one held by a map, walks as a
// copy, so [exposed] can read its unexported embedded fields.
//
// A value that decodes itself walks as any other, and the walk records it
// for the errors at and below it, as [selfWalker.enter] describes. A
// struct that decodes itself through a method it gets from an embedded
// field walks that field alone, at its own place.
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

// enter records that the walk is inside of a value of the given kind
// that decodes itself, which lies at the place at. The fields of such a
// value need not mirror the document, so an error at or below it whose
// path the document does not hold binds at the value, as
// [selfWalker.fallback] hands it to the error. A slice, an array, or a
// map that decodes itself has no field tags to say where its elements
// sit, so every error below one binds at it. The values below such a
// value add nothing to the record, since no path says where they sit
// either.
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

// exposed returns v, or a view of it that the walk can read when v is an
// unexported embedded field. Go-yaml decodes into such a field through
// an unmarshaler it promotes, and otherwise leaves the value set before
// the decode, so the walk validates it like any other field. The view
// shares the memory of v, so a Validate with a pointer receiver runs on
// v itself, and the walk can read every value below the view. Such a
// field always has an address, since [selfWalker.walkValue] copies a
// struct it cannot address before it reads the fields of the struct.
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

// walkOwned walks v, a non-nil pointer, map, or slice that owns its
// address, as [selfWalker.walk] does, and records the result. A value the
// walk has entered before returns the result it recorded, so v walks
// once.
//
// The walk finds the cycles the way Tarjan's algorithm finds strongly
// connected components. A value that leads back to one above it finishes
// inside a cycle, so its result waits on pending until the first value of
// the cycle finishes. That value then sets its own
// result for every value of the cycle.
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

// ownsAddress reports whether the address of v, a non-nil pointer, map,
// or slice, names v alone. Every zero-size allocation can share one
// address, so a pointer to a zero-size value, an empty slice, or a slice
// of zero-size elements can share its address with an unrelated value.
// Such a value holds nothing that can refer back to it, so the walk needs
// no record of it.
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
	// The result of [mayHoldValidator] for each type it has read.
	holdsValidator unmarshaler.TypeCache[bool]

	// The result of [implementsSelfValidator] for each type it has read.
	ownsValidator unmarshaler.TypeCache[bool]

	// The result of [fieldsOf] for each struct type it has read.
	walkedFields unmarshaler.TypeCache[*structFields]
)

// mayHoldValidator reports whether a value of type t can implement
// [SelfValidator], or can hold a value the walk reaches below it that
// does. A type holds one through an interface, which can hold a value of
// any type, or through a field, element, map key, map value, or pointee
// whose type may. The walk passes a value whose type may not without a look
// below it.
//
// The answer reads the fields of t and no option of a decode, so every
// walk shares it. A struct that decodes itself through a method of an
// embedded field can thus report a validator in another field, which
// the walk never reaches, and the walk then looks at the value and stops
// there. A node of the syntax tree holds none the walk reaches.
func mayHoldValidator(t reflect.Type) bool {
	return holdsValidator.Get(t, func(t reflect.Type) bool {
		// The walk reads nothing below a node of the syntax tree.
		return yamlfield.Reaches(t, func(t reflect.Type) (bool, bool) {
			return t.Kind() == reflect.Interface || implementsSelfValidator(t), unmarshaler.IsSyntaxNode(t)
		})
	})
}

// children walks the values below v, and reports whether every one of
// them passed.
//
// The fields of an inline struct sit beside the fields of its parent,
// and go-yaml zeroes each one whose name a field of the parent that is
// not inline also uses. The document sets such a field only for the
// parent, so the walk passes over it. When v is an inline struct,
// shadowed holds the names of those fields of the parent.
func (w *selfWalker) children(v reflect.Value, at place, shadowed map[string]bool) bool {
	switch v.Kind() {
	case reflect.Slice, reflect.Array, reflect.Map:
		// An element, map key, or map value whose type holds no validator
		// passes the walk at once, so none needs a look. The same goes for
		// the keys and values of a map whose types may hold one, but that
		// hold none, so the walk reads no key of such a map.
		if !entriesMayHoldValidator(v.Type()) || v.Kind() == reflect.Map && !w.holdsBelow(v) {
			return true
		}

	default:
	}

	ok := true

	switch v.Kind() {
	case reflect.Struct:
		fields := fieldsOf(v.Type())

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
		// The entries walk in the order of their keys, so the errors come
		// back in one order however the map iterates. Two keys of one
		// text, such as 1 and "1", share a path and order by the types
		// they hold. Entries that tie on both, such as several NaN keys or
		// several time keys of one instant and zone, walk as one group, as
		// [selfWalker.walkEntries] describes, along with the one case
		// where the order can vary. Each value comes from the iteration
		// rather than a lookup by its key, since a NaN key equals no key,
		// itself included.
		names := w.keyNames(w.nodeOf(at.last), v.Type().Key())

		// A path names a key by its text alone, so keys that share a
		// segment share a path, which resolves to the document entry of
		// one of them at most. The errors under such a path bind with no
		// position.
		for _, group := range datapath.MapGroups(v, names) {
			if !w.walkEntries(at.child(group.Seg), group.Entries, group.Ambiguous) {
				ok = false
			}
		}

	default:
	}

	return ok
}

// walkEntries walks entries, the entries of a map that share the place
// at, and reports whether nothing under them failed. The key of an entry
// walks before its value, at the key of that place. Since the entries
// share a path, nothing in the map orders them, so the errors of each
// entry stay together and the groups order by their text. The order
// then holds however the map iterates, and no value needs formatting,
// which a value that refers back to itself would never finish.
//
// The order can still vary when several of the entries hold one
// pointer, map, or slice. An alias in the document can make them share
// one, and so can a value the caller filled before the decode. The
// shared value walks once, so its errors join the group of whichever
// entry walks first, and the map iteration decides which.
//
// When ambiguous is true, the path of at names the entries of several
// keys, so the walk marks each path below entries ambiguous, and the
// errors of entries bind with no position, for the reason
// [ErrAmbiguousPath]. They bind here, so the text that orders the groups
// names the path of each.
func (w *selfWalker) walkEntries(at place, entries []datapath.MapEntry, ambiguous bool) bool {
	ok := true
	start := len(w.errs)
	groups := make([][]error, 0, len(entries))

	// A path below an entry that shares its path is ambiguous too,
	// whatever the maps further down hold. Below a slice, an array, or a
	// map that decodes itself, every error binds at that value, so the
	// path of an entry there locates none.
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
// [fieldsOf] returns them.
type structFields struct {
	// The names of the fields of the struct that are not inline, as
	// [yamlfield.OwnNames] returns them, or nil when no field in held
	// is inline.
	own map[string]bool

	// The fields that go-yaml decodes and whose types may hold a
	// [SelfValidator], in the order the struct declares them.
	held []heldField
}

// heldField is a field of a struct whose type may hold a [SelfValidator].
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

// fieldsOf returns the fields of t, a struct type, that the walk reads.
// A field whose type holds no validator passes the walk at once, so the
// result leaves it out, along with each field go-yaml skips. Every walk
// shares the result, so a caller must not change it.
func fieldsOf(t reflect.Type) *structFields {
	return walkedFields.Get(t, readFields)
}

// readFields returns the fields [fieldsOf] returns for t.
func readFields(t reflect.Type) *structFields {
	fields := &structFields{}

	for i := range t.NumField() {
		field := t.Field(i)
		if !mayHoldValidator(field.Type) {
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

// entriesMayHoldValidator reports whether an element of a value of type t,
// a slice, array, or map, or a key or value of a map, may hold a
// [SelfValidator], as [mayHoldValidator] reads it.
func entriesMayHoldValidator(t reflect.Type) bool {
	if t.Kind() == reflect.Map && mayHoldValidator(t.Key()) {
		return true
	}

	return mayHoldValidator(t.Elem())
}

// holdsBelow reports whether a value below v, a map the walk is inside
// of, implements [SelfValidator] where the walk would validate it. When
// it reports false, the walk of the values below v validates nothing, so
// the walk passes v's children at once.
//
// The scan reads each pointer, map, and slice it reaches once, however
// many paths lead there.
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

	// A false result means the scan read everything below v. When v holds
	// no validator of its own either, no value the scan reached holds one,
	// since everything below such a value is below v too. When v does hold
	// one, a value that leads back to v holds it below, so the scan records
	// nothing more.
	if !held && !implementsSelfValidator(v.Type()) {
		for key := range w.scanning {
			w.scanned[key] = false
		}
	}

	return held
}

// scanValue reports whether v, or a value below it, implements
// [SelfValidator] where the walk would validate it. It follows the walk
// down: through interfaces and pointers, into fields, elements, and map
// keys and values, and not below a value whose type may hold no
// validator. Below a struct that an embedded field decodes, it follows
// only that field, as [selfWalker.walkValue] does. The second
// result is false when the scan stopped at a value it had already
// reached, whose first read decides the answer, so a false first result
// then holds only for that scan.
func (w *selfWalker) scanValue(v reflect.Value) (bool, bool) {
	if !v.IsValid() || !mayHoldValidator(v.Type()) {
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

			return implementsSelfValidator(v.Type()), true
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
		if v.Kind() != reflect.Pointer && implementsSelfValidator(v.Type()) {
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
	if implementsSelfValidator(v.Type()) {
		return true, true
	}

	if field, _ := w.cfg.unmarshalers.Whole(v.Type()); field >= 0 {
		return w.scanValue(v.Field(field))
	}

	return w.scanChildren(v)
}

// scanChildren scans the fields of a struct, the elements of a slice or
// array, or the keys and values of a map, as [selfWalker.scanValue] describes.
// It reads the fields of an inline struct that its parent shadows too,
// which the walk passes over, so it can report a validator the walk does
// not reach. The walk then validates nothing more than it would anyway.
func (w *selfWalker) scanChildren(v reflect.Value) (bool, bool) {
	complete := true

	scan := func(child reflect.Value) bool {
		held, done := w.scanValue(child)
		complete = complete && done

		return held
	}

	switch v.Kind() {
	case reflect.Struct:
		for _, field := range fieldsOf(v.Type()).held {
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
		scanKeys, scanValues := mayHoldValidator(t.Key()), mayHoldValidator(t.Elem())

		// The scan reuses one holder for the keys and one for the values,
		// since iter.Key and iter.Value copy each entry. SetIterKey and
		// SetIterValue panic on a map read through an unexported field,
		// so the scan reads copies from such a map.
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

// implementsSelfValidator reports whether a value of type t implements
// [SelfValidator] through a Validate of its own, on its value or its
// pointer, as [selfWalker.validate] checks it. A struct that gets
// Validate from an embedded field does not own it, so the struct passes
// the check to the field. The walk validates that field at its own path,
// or at the path of the struct when the field decodes the struct, as
// [decoderField] finds it. The method does not run when the field is nil
// or go-yaml skips it, since the walk never reaches such a field.
func implementsSelfValidator(t reflect.Type) bool {
	return ownsValidator.Get(t, func(t reflect.Type) bool {
		return unmarshaler.Declares(t, reflect.TypeFor[SelfValidator]())
	})
}

// validate runs Validate on v when v implements [SelfValidator] through
// a method of its own, on its value or its pointer, with the result
// rebased under the path of at, and reports whether v passed. The rebase
// marks each Error it builds as invalid, and each binding it meets, so
// every problem the result holds matches [errInvalid] whether it carries
// a location or not. Below a map entry that shares its path, the rebase
// marks the path of each Error ambiguous too. A value the walk cannot
// take the address of, such as one held by a map, validates through a
// copy, so a Validate with a pointer receiver runs on it too.
// The error of a context that ended stops the walk as it is, with no
// path, as [selfWalker.stopped] describes.
func (w *selfWalker) validate(v reflect.Value, at place) bool {
	if !implementsSelfValidator(v.Type()) {
		return true
	}

	if w.stopped() {
		return false
	}

	v = addressable(v)

	validator, ok := reflect.TypeAssert[SelfValidator](v.Addr())
	if !ok {
		return true
	}

	err := validator.Validate()
	if isNothing(err) {
		return true
	}

	if contextEnded(err) {
		w.ended = err

		return false
	}

	w.errs = append(w.errs, rebase(err, move{base: at.path(), ambiguous: w.ambiguous, fallback: w.fallback()}, true))

	return false
}

// stopped reports whether the walk has stopped, which it does once its
// context ends or a Validate returns the error of a context that ended.
// It keeps the first such error as the one the walk returns in place of
// every other.
func (w *selfWalker) stopped() bool {
	if w.ended == nil {
		w.ended = w.ctx.Err()
	}

	return w.ended != nil
}

// nodeOf returns the node the value at the end of s decoded from, as
// [paths.Resolver.Node] gives it, or nil when the document holds nothing
// there, as for a value the document did not set. A nil s stands for the
// value the walk starts at, whose node lies at the path of the [Node] the
// walk validates. The node of any other step resolves from the node of
// the step before, the first time the walk asks for it, and the step
// keeps it, so no step resolves twice. A walk through no document holds
// no node for any value.
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

// keyNames returns the text the document spells each key of the mapping
// node holds with, by the value the key decodes to as type t, so a key
// such as 0x10 or 1.50 keeps the text a path resolves. A key any `<<`
// merge key brings in counts, whether the merge names one mapping or a
// list of them, directly or through an alias. Where the mapping and its
// merges define one key more than once, the later entry in document
// order wins, with the sources of one merge in sequence order, as in the
// decode. A merge with an alias that does not resolve may set any key,
// so no entry before it names a key. The map holds no key a path cannot
// resolve to, and is empty when node holds no mapping, as for the nil
// node of a value the document did not set.
//
// KeyNames reaches the mapping through the anchors, tags, and aliases on
// node, as [selfWalker.valueNode] does. It reads the keys in reverse
// document order, as [datapath.MembersBackward] yields them, so the first
// name it sets for a value is the one the decode keeps.
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

// addKeyName decodes key as type t and adds to names the text
// [paths.Resolver.KeyName] gives the key, under the value the key decodes
// to, as [nameKey] keys it. A child selector matches a key by that text,
// so a path built from names resolves to the entry, unless a later entry
// repeats the key or a later `<<` merge key brings it in. A key KeyName
// cannot name, one that does not decode, whose decode panics, or whose
// value cannot key a map, adds nothing, and neither does a key whose
// value names already holds. The key decodes from the node
// [selfWalker.valueNode] gives it.
//
// As go-yaml does, addKeyName decodes a key of a pointer type t as the
// type t points to, and leaves a null key, or an alias to a null, a nil
// pointer. A null counts only where the key itself is one, before
// go-yaml looks through a `?`, an anchor, or a tag, so go-yaml points a
// key such as `&a ~` at a zero value instead.
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

// valueNode returns the node that node decodes from. It looks through the
// `?` of an explicit key and the anchors on node, which carry no part of
// its value, and follows each alias to the content of its anchor, as
// [paths.Resolver.Deref] does. The key decoder of the walk knows none of
// the anchors of the document, so an alias left in a key would not
// decode. Each tag on the way stays around the content it holds, since a
// tag decides how the node decodes, whether it sits on node or on the
// content of an anchor. It returns nil for a node that holds nothing, and
// for one with an alias that does not resolve or that leads back to
// itself.
//
// When parsed is true, the content and each tag come from the
// [decodetree.Tree] of the document, so an integer under a !!int tag decodes
// into an integer type, as in the decode. For a key with no anchor, no
// `?`, and no alias, an UnmarshalYAML method of the key type then takes
// the same text or node as in the decode. The decode hands such a method
// a key with an anchor or a `?` whole, and hands a method that reads a
// node an alias as it stands. When parsed is false, the result holds
// nodes of the document, which the [paths.Resolver] of the document
// reads.
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
// the data came from. A check of decoded data names what it finds by the
// member names and indices of that data, as an adapter for CUE, OPA, or
// another JSON Schema library does. A path matches a key by its source
// text instead. [AtPath] of those names therefore misses a key the
// decoder respells, such as the key 0x10 of the member 16, and it reads
// the index of an element as the name of a member. A DataLocator reads
// the names as the decoder does, and its options give an error the
// location [go.jacobcolvin.com/niceyaml/schema.Schema] gives a violation
// of the same value:
//
//	func (c *check) Check(ctx context.Context, n *niceyaml.Node) error {
//		data, err := n.Decode[any](ctx)
//		if err != nil {
//			return err
//		}
//
//		loc := n.DataLocator()
//
//		var errs []error
//
//		for _, f := range c.findings(data) {
//			errs = append(errs, niceyaml.NewError(f.Msg, loc.At(f.Path...)))
//		}
//
//		return errors.Join(errs...)
//	}
//
// In this document, a finding at ports, 16, name reports the key as the
// source spells it, and one at items, 0, name reports an element:
//
//	ports:
//	  0x10:
//	    name: ""
//	items:
//	  - name: ""
//
//	cfg.yaml:3:11: $.ports.0x10.name: must not be empty
//	cfg.yaml:5:11: $.items[0].name: must not be empty
//
// Create instances with [Node.DataLocator].
//
// # Names
//
// The names are those of the data from Decode[any] of the Node, as
// [Node.Decode] yields it. A member has the name the decoder gives its
// key. A string key gives its text, and any other key gives its Go value
// as [fmt.Sprint] prints it. The key 0x10 is 16, 3.10 is 3.1, True is
// true, 1234567.0 is 1.234567e+06, and !!binary aGk= is [104 105]. A null
// key is null. An element has its index as its name, in decimal digits
// with no sign and no leading zero, as [strconv.Itoa] writes it. Such a
// name is an index where the names before it lead to a sequence, and the
// name of a member anywhere else.
//
// A decode into another type can name a key another way. The key 1e3
// decodes to the string 1e3 in a decode into any and to the key 1000 of
// a map[int]string. The key ~ decodes to null in a decode into any and
// to the empty key of a map[string]string. A name from such a decode
// names no member of the data, so it leads nowhere, as [DataLocator.At]
// describes.
//
// # JSON Pointers
//
// A check that reports JSON Pointers splits each pointer into its names
// and unescapes `~1` and `~0` in each:
//
//	func names(pointer string) []string {
//		if pointer == "" {
//			return nil
//		}
//
//		unescape := strings.NewReplacer("~1", "/", "~0", "~")
//
//		parts := strings.Split(strings.TrimPrefix(pointer, "/"), "/")
//		for i, part := range parts {
//			parts[i] = unescape.Replace(part)
//		}
//
//		return parts
//	}
//
//	niceyaml.NewError(f.Msg, loc.At(names(f.Pointer)...))
//
// # Lifetime
//
// A DataLocator is safe for concurrent use. It reads each mapping once
// however many locations pass through it, so a check creates one for all
// the findings of a run. To tell which key a spelling selects, it reads
// the sources of each `<<` merge key that brings the key in or stands
// after it. The locations of one DataLocator may read as many nodes that
// way as [paths.ErrExcessiveMerging] allows one path selector. Past that
// limit it still finds each location, and the path through such a key
// names the member and each key below it as the decoder does. A check
// therefore creates a DataLocator for each run and keeps none for the
// life of the document.
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
// names lead to in the data of the [DataLocator]. No names lead to the
// whole of that data. The option sets the path of the error, as [AtPath]
// does, to the `$` path of the value with each key spelled as the source
// spells it:
//
//	niceyaml.NewError("must not be empty", loc.At("ports", "16", "name"))
//	// cfg.yaml:3:11: $.ports.0x10.name: must not be empty
//
// Where several keys of a mapping decode to one name, the name leads to
// the last of them, whose value the decode keeps. The member 16 of
// `m: {16: aaaa, 0x10: b}` holds b, so the names m, 16 lead to
// `$.m.0x10`. The members a `<<` merge key brings in take the order a
// decode gives them.
//
// A path selects a key by its text, and a later key or a later merge
// source may spell the key of another member with the same text. No path
// then selects the value, so the option also sets its position, as
// [AtPosition] does. The error binds at the position, and the path names
// the member and each key below it as the decoder does:
//
//	user:
//	  <<: {0x10: x}
//	  "0x10": 1
//
//	niceyaml.NewError("too short", loc.At("user", "16"))
//	// cfg.yaml:2:14: $.user.16: too short
//
// The names may lead to a value the data lacks, as a check for a
// required member reports one. The path then keeps the names from there
// on as the caller wrote them, and the error binds where that path
// binds. A member that a mapping lacks binds at the key of the mapping,
// as [SourceError.Nearest] describes. A finding at ports, 16, name thus
// binds at the key 0x10 when the mapping under it holds no name:
//
//	cfg.yaml:2:3: $.ports.0x10.name: name is required
//
// An index past the end of a sequence and a name below a scalar bind
// with no position.
//
// The names may also lead through an alias the document cannot follow,
// as one to an anchor of a document from [WithReferences] is. The
// DataLocator cannot tell what lies behind such an alias, or every member
// of a mapping that merges one. A name it cannot follow keeps the form
// the caller wrote, as does each name below it, and the option sets no
// position. The error then binds where its path binds, as
// [SourceError.Nearest] describes. A path that enters the alias binds at
// the alias, and a path to a key beside a merge of one has no position.
//
// A path the names do not lead down can select another value all the
// same. A key can spell a name and set a member of another name, as the
// key 3.10 sets the member 3.1. Under `python: {3.10: {image: a}}` the
// names python, 3.10 lead nowhere, and the path `$.python.'3.10'` selects
// that entry. The option then sets the position of the last value the
// names lead to, here the key python, and the error binds there. It does
// the same where no path selects that last value.
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

// locate walks the node of the locator down names and returns the path
// to the location they lead to. With key set, the location is the key of
// the entry there. It also returns the token to bind an error at where
// the path does not bind at the location, and nil where it does or where
// the walk reached nothing to bind at.
//
// A value the walk reaches takes the path and the token
// [datapath.Target.Token] gives it. So does one the walk cannot follow,
// since the walk cannot tell there whether the data holds it, and a
// schema reports a violation of such a value the same way.
//
// A value the data lacks ends the walk. Its location takes the path of
// the last value the walk reached, with each name from there on below it
// as the caller wrote it. That path binds where a path to a key the
// document leaves out binds, so the token is nil. It is the token of
// that last value where the path would bind elsewhere instead. That
// holds where the path to the value does not select it. It also holds
// where a key of the mapping there spells the missing name, as
// [datapath.Index.SelectsEntry] reports, since the member that key sets
// has another name.
func (l *DataLocator) locate(names []string, key bool) (paths.Path, *token.Token) {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.idx.Locate(datapath.Target{Path: l.base, Node: l.root}, names, key)
}

// SelfValidator is a type that validates itself after a decode. A decode
// calls Validate on every value in the result that implements it, and on
// the values below a value before the value itself. Validate reports a
// problem as an [*Error] with an `@` path, which reads from the value,
// and the decode puts that path under the path of the value in the
// document. A type thus checks its invariants once and reports the right
// lines in any document. The document is at fault for every error a
// Validate returns, so [IsInvalid] reports it.
//
// # Checked Values
//
// [Node.Decode] and [Node.DecodeInto] call Validate after decoding on
// every value in the decoded value that implements it: the value
// itself, and each field, element, or map entry below it, however deep,
// unless [WithSelfValidation] switches that off.
//
// A check that belongs to the caller rather than the type, such as one
// that needs a registry of known names, runs on the decoded value after
// Decode returns. [Node.DataLocator] finds each value the check names in
// the document, and [Node.Bind] binds the result to the document the
// value came from:
//
//	cfg, err := doc.Decode[Config](ctx, settings)
//	if err != nil {
//		return err
//	}
//
//	loc := doc.DataLocator()
//
//	var errs []error
//
//	for port, server := range cfg.Ports {
//		if !known[server.Name] {
//			errs = append(errs, niceyaml.NewError("unknown server", loc.At("ports", strconv.Itoa(port), "name")))
//		}
//	}
//
//	return doc.Bind(errors.Join(errs...))
//
// The check costs no second decode and reads the value the options of
// the caller decoded. The locator spells a key as the document does. The
// error "c.yaml:3:11: $.ports.0x10.name: unknown server" thus names an
// entry the document writes under 0x10.
//
// A check that must run inside the decode, so a [DecodeOption] carries it
// to every decode and its failures report beside the others, is a
// [Validator]. Such a Validator decodes the node itself with
// [Node.Decode] and checks the value it gets, as the Decoded Data
// section of Validator describes.
//
// Any value with a Validate method takes part, including one from a
// package that names its own check that way, such as a generated
// message type. A decode runs those checks too and reports their errors
// at the value that owns the method. A Validate that the value's own
// UnmarshalYAML already called runs a second time inside the decode, so
// a Validate that rewrites its value should be idempotent. An error from
// that first call reports as a decode error, which matches [ErrDecode],
// rather than as a self-validation error.
//
// # Paths
//
// An [*Error] the value returns writes an `@` path, which reads from the
// value itself. The decode puts it under the path of the value in the
// document, and leaves a `$` path, which reads from the root of the
// document, as it is. The path of the value names a field by the name
// go-yaml decoded it under, from its yaml tag, its json tag, or its
// lowercased name. It names an element by its index and a map entry by
// its key. A map key validates too, under the path of its entry with a
// `~` after it, so its errors point at the key rather than the value. The
// decode reads the roles of the errors as [Rebase] does. A problem with
// no location points at the value, whether it stands alone or under a
// summary from [NewSummary], and a detail from [WithDetails] gains no
// location. A type thus checks its invariants once and reports the right
// lines in any document:
//
//	type Config struct {
//		Hours Hours  `yaml:"hours"`
//		Items []Item `yaml:"items"`
//	}
//
//	func (h Hours) Validate() error {
//		if h.Close.Before(h.Open) {
//			return niceyaml.NewError("closes before it opens", niceyaml.AtPath(paths.Current().Child("close")))
//		}
//
//		return nil
//	}
//
// A decode of Config reports $.hours.close from Hours and $.items[2].price
// from an Item, with Config declaring no Validate of its own.
//
// # Nested Values
//
// The values below a value validate first, and the value validates only
// when every one of them passed. A parent that checks a relation between
// its fields thus sees fields that hold together, and a decode reports
// every value that failed. A field an inline tag flattens keeps the path
// of the struct that holds it. A flattened field whose name a field of
// that struct also uses does not validate, since go-yaml sets it to its
// zero value and decodes the document into the other field. A node type
// of the go-yaml ast package validates nothing below it, since go-yaml
// sets it to the node it decodes. A struct that embeds a node decodes
// field by field, so its fields validate. A parent need not call the
// Validate of its fields, and [Rebase] is for a check run on a value
// after Decode returns.
//
// # Self-Decoding Values
//
// A value whose type decodes itself validates as any other value does,
// and so does every value below it. A type decodes itself through an
// UnmarshalYAML or UnmarshalText method, through a function from
// [WithCustomUnmarshaler], or through an UnmarshalJSON method under
// [WithJSONUnmarshalers]. A method that sets defaults and then decodes
// the fields thus keeps every check below its type.
//
//	func (s *Server) UnmarshalYAML(unmarshal func(any) error) error {
//		type plain Server
//
//		*s = Server{Port: 8080}
//
//		return unmarshal((*plain)(s))
//	}
//
// Such a method may fill its value from anything, so the fields need not
// mirror the document. The decode names each field of the struct by its
// tag all the same, as it names the fields of any struct. A field the
// method leaves as it was, or fills from anything but the document,
// validates too. The document may thus hold nothing at the path of an
// error at or below such a value. A path that names a key a mapping
// leaves out binds at the key of that mapping, as it does below any
// struct. Any other path the document does not hold binds at the nearest
// value that decodes itself, which is the value that returned the error
// or one above it. The error keeps its path, and [SourceError.Nearest]
// returns the path of the value it bound at. An Addr that decodes from
// the text "db:0" thus reports its port on that text:
//
//	app.yaml:2:7: $.addr.port: port must be at least 1
//
// A slice, an array, or a map that decodes itself has no tags, and its
// method may sort its elements, drop some, or key them anew. No path
// below such a value says where an element sits in the document, so
// every error below it binds at the value.
//
// The tags of a struct that decodes itself are how its author says where
// each field sits. A field tagged `yaml:",inline"` reads the mapping of
// the struct, so the errors below it report their own lines. That suits
// a struct that holds one of several types, which a key of its mapping
// names.
//
//	type Box struct {
//		Store Store `yaml:",inline"`
//	}
//
// Without the tag, the decode looks for the Store under a key named
// store, and each error below it binds at the Box. A field tagged
// `yaml:"-"` does not validate, so the Validate of the struct checks it,
// and walks the values below it with [SelfValidateValue]. The decode
// reads every other field itself and runs that Validate only when they
// all pass, so no field reports twice. A struct whose method reorders a
// slice field keeps the tag of that field, so an error under one of the
// elements reports at the element the document holds at that index.
//
// The decode cannot see a type that a function from
// [yaml.RegisterCustomUnmarshaler] decodes in every decode of the
// program. The values below such a type validate the same way, but the
// decode reads it as a type that decodes field by field. An error below
// it whose path resolves to nothing thus has no value to bind at, and an
// error below a slice or a map of such a type reports at the element its
// path names. A program gives that function to WithCustomUnmarshaler
// instead.
//
// # Map Keys
//
// A Validate holds no [Node], so it has no [DataLocator] to find how the
// document spells a key. A parent that checks the entries of a map and
// builds each path from the Go key names the key as the decoder read it,
// such as 16 for the key 0x10, and that path misses the entry. The check
// of an entry belongs in a Validate of the entry's own type instead. The
// decode puts the errors of that Validate under the key as the document
// spells it:
//
//	type Config struct {
//		Ports map[int]Server `yaml:"ports"`
//	}
//
//	func (s Server) Validate() error {
//		if s.Name == "" {
//			return niceyaml.NewError("name is required", niceyaml.AtPath(paths.Current().Child("name")))
//		}
//
//		return nil
//	}
//
// A decode of Config then reports $.ports.0x10.name for the entry under
// the key 0x10.
//
// # Error Text
//
// [Error.Error] carries no location, so a Validate may add context
// around an Error with [fmt.Errorf] at any depth. The text the wrapper
// writes holds no path, and the decode puts the position and the joined
// path in front of the whole text:
//
//	cafe.yaml:4:10: $.hours.close: hours check: closes before it opens
//
// A caller that calls Validate itself holds an error that no binding
// holds yet, and its message names no path. Such a caller reads it with
// [FormatError], which puts the path in front of the text the same way.
// That path is the one the value wrote, so it reads from the value:
//
//	@.close: hours check: closes before it opens
//
// A caller that returns or logs the error binds it with [BindValue]
// instead. The text of that error names each path wherever it prints,
// and a document can still place it. [SelfValidateValue] runs the
// Validate of every value below the value too, as a decode does, and
// binds the result the same way.
//
// # Embedded Fields
//
// A struct does not own a Validate it gets from an embedded field, so the
// method runs once, on that field at the field's own path. The method
// does not run when the field is nil or ignored. A struct that decodes
// itself through an UnmarshalYAML or UnmarshalText method it gets from an
// embedded field decodes the document into that field, so the field
// validates at the path of the struct. Go-yaml decodes no other field of
// such a struct, so none of them validates.
//
// # Deferred Validation
//
// A check that reads state the caller fills in after the decode, such as
// a field a library sets from the environment or a flag, runs through
// [Node.SelfValidate]. The caller decodes with [WithSelfValidation] off,
// fills in that state, and then calls SelfValidate on the value, which
// walks it as the decode would have. SelfValidate describes where an
// error binds when the value no longer mirrors the document, such as
// under a field the document lacks.
//
// # Fault
//
// A Validate checks the value, so the document is at fault for every
// error it returns, with a location or without, and [IsInvalid] reports
// it. That holds for an error of I/O too, such as one from a check that a
// named file exists, and for an error inside [Place]. A Validate whose
// I/O can fail for reasons outside the document thus leaves that check to
// the caller, or to a [Validator], which declares each error itself.
//
// # Cancellation
//
// A Validate that returns the error of a context that ended, one that
// matches [context.Canceled] or [context.DeadlineExceeded], stops the
// walk. The decode returns that error alone, with no location, in place
// of every error the walk found before it, as [MultiValidator] does.
// The walk stops the same way once the context of the decode ends, and
// the decode then returns the error of that context.
type SelfValidator interface {
	Validate() error
}

// Validator validates a [*Node] before it decodes, as a JSON schema does,
// or a schema registry that picks the schema from the document's content
// or file path. Pass one to [Node.Decode] with [WithValidator], or run
// one on its own with [Node.Validate]. Check checks the Node it gets,
// which is the root of a document or a Node from [Node.At], and returns
// its errors as it wrote them from that Node: an [*Error] whose `@` path
// reads from the Node, a plain error, or an error a Node bound already.
// Check is the raw step, and the validators this module ships return
// their errors that way too. The Check of
// [go.jacobcolvin.com/niceyaml/schema.Registry] is the exception and
// binds every error it returns. Node.Validate and a decode bind what Check
// returns, as [Node.Bind] binds an error, so the error names the source
// the validator read and its `@` paths resolve from the Node. An Error
// from [NewError] or [Invalid] says what is wrong with the document, and
// any other error says the check could not run. A document that did not
// parse never reaches a validator that a decode or Node.Validate runs.
//
// See [ValidatorFunc], [MultiValidator], [ChainValidator], [SkipEmpty],
// [go.jacobcolvin.com/niceyaml/schema.Schema], and
// [go.jacobcolvin.com/niceyaml/schema.Registry] for implementations.
//
// # Scope
//
// The Node is the scope that runs the validator: the root of a whole
// document, or the node a Node from [Node.At] selects, so a validator
// given to a scoped decode checks that node and its `@` paths resolve
// from it. A validator that needs the whole document reaches it through
// [Node.Document]. One that can only check
// a whole document, as a [go.jacobcolvin.com/niceyaml/schema.Registry]
// can, refuses a scoped Node with an error instead of checking the
// document around it, so a caller validates once at the root and decodes
// the nodes below it without that validator.
//
// # Decoded Data
//
// A validator that checks the decoded data reads the node with
// [Node.Decode], which runs the validators the caller passes and no
// other, so a validator never runs itself again. That decode reads the
// node with the settings of its [Source], such as the reference
// documents of [WithReferences], so a validator reads an alias as every
// decode of the Source reads it. A [DecodeOption] reaches the decode that
// gets it and no other, so a validator that decodes a type a
// [WithCustomUnmarshaler] function decodes gives that option to its own
// decode. The context carries cancellation and deadlines to validators
// doing cancellable work, such as remote schema reference resolution:
//
//	func (s *Schema) Check(ctx context.Context, n *niceyaml.Node) error {
//		data, err := n.Decode[any](ctx)
//		if err != nil {
//			return err
//		}
//
//		return s.check(ctx, data)
//	}
//
// The names of that data are not the keys of the document, since the
// decoder respells a key such as 0x10, which sets the member 16. A
// validator that reports where a finding lies in the data takes each
// location from [Node.DataLocator], which reads the names as the decoder
// does.
//
// A validator that decodes into a type of the program follows three
// rules. It takes the options of the decode that runs it and gives them
// to its own decode. It passes [WithSelfValidation] off to that decode,
// since the decode that runs it calls each Validate method on its own
// result. And one validator decodes for every check on the type, since
// each validator that decodes reads the node again, and two of them
// under [MultiValidator] report one rejection of the decoder twice:
//
//	func typed(settings niceyaml.DecodeOption, check func(*niceyaml.DataLocator, *Config) error) niceyaml.Validator {
//		return niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
//			cfg, err := n.Decode[Config](ctx, settings, niceyaml.WithSelfValidation(false))
//			if err != nil {
//				return err
//			}
//
//			return check(n.DataLocator(), &cfg)
//		})
//	}
//
//	settings := niceyaml.DecodeOptions(niceyaml.WithDisallowUnknownFields(true))
//	cfg, err := doc.Decode[Config](ctx, settings, niceyaml.WithValidator(typed(settings, check)))
//
// # Errors
//
// A validator writes the path of an error from the Node it got, as a
// [SelfValidator] writes it from the value it checks. A key the
// document lacks has no Node, so its error carries the path of the key,
// and the Node that runs the validator points the error at the key of
// the mapping that lacks the value, as Node.At describes:
//
//	return niceyaml.NewError("kind is required", niceyaml.AtPath(kindPath))
//
// A validator that read a value through a Node from [Node.At] names the
// value the same way, with the path it scoped the Node by. A path that a
// Node hands out, such as [Node.Path] of a Node from [Node.Nodes], starts
// at `$`, so it names the same value through the Node the validator got,
// whatever the scope of that Node:
//
//	for _, item := range items {
//		errs = append(errs, niceyaml.NewError("bad item", niceyaml.AtPath(item.Path())))
//	}
//
// An error with no location binds at the value the Node selects, as
// [Node.Bind] describes, so a validator that runs on a Node from Node.At
// or Node.Nodes reports a plain error at that value.
//
// A validator binds an error itself, through [Node.Bind], when it wants
// the error somewhere other than the Node it got. One that scopes a Node
// with Node.At and binds through it reports the value it read, and one
// that binds through [Node.Document] gives an error that is no fault of
// the value, such as a schema that does not load, no location. A bound
// error comes back from the Node that runs the validator as it is, since
// Node.Bind leaves a bound error as it is.
//
// A bound error keeps its text, so context a validator wraps around a
// bound error stands in front of the position, as in
// "config schema: svc.yaml:2:7: $.port: 0 is less than 1". The binding
// puts the position in front of context wrapped around an unbound
// error instead, as in "svc.yaml:2:7: $.port: config schema: 0 is less
// than 1". A validator that wants its context in front thus binds first.
//
// # Nested Validators
//
// A validator that runs another on a Node of its own choosing, such as
// each element of a list, runs it through [Node.Validate] and returns
// the error as it is:
//
//	for _, item := range items {
//		if err := item.Validate(ctx, inner); err != nil {
//			return err // bound at $.items[i]
//		}
//	}
//
// A call to inner.Check(ctx, item) returns the error as inner wrote it,
// with its `@` paths reading from item. The Node that runs the outer
// validator then binds it and resolves those paths from itself. In the
// c.yaml below, a rule that reserves the name admin rejects the second
// item:
//
//	name: lunch
//	items:
//	  - name: soup
//	  - name: admin
//
// The rule writes its error at `@.name`. The outer validator reports one
// of these lines, by the call it makes:
//
//	item.Validate(ctx, inner)   c.yaml:4:11: $.items[1].name: reserved name
//	inner.Check(ctx, item)      c.yaml:1:7: $.name: reserved name
//
// When the item is a Node of another source, the error names the wrong
// file too, since it takes the source of the Node that binds it.
//
// # Fault
//
// A validator declares which of its errors are the fault of the document.
// It returns an [*Error] from [NewError] or [Invalid] to report what is
// wrong with the document, with a location or without, and [IsInvalid]
// reports that Error. An error of the validator's own type declares the
// fault inside Invalid, where [errors.As] still finds it. The validator
// returns any other error when the check itself could not run, such as an
// I/O error, and IsInvalid does not report that error. A location changes
// neither. [Place] shows an error of the second kind at the value the
// check read, with the options NewError and Invalid take, and declares
// no fault:
//
//	_, err := os.Stat(spec.License)
//	switch {
//	case errors.Is(err, fs.ErrNotExist):
//		return niceyaml.NewError("license file does not exist", niceyaml.AtPath(licensePath))
//	case err != nil:
//		return niceyaml.Place(fmt.Errorf("stat license: %w", err), niceyaml.AtPath(licensePath))
//	}
//
// The first error is the fault of the document, which names a file that
// does not exist. The second binds at the same value, as in
// "c.yaml:3:12: $.spec.license: stat license: permission denied", and it
// stays a check that could not run, which IsInvalid does not report.
//
// # Origin
//
// A validator that resolves such a file beside the file that names it
// reads the directory from [Node.Origin] of the Node of the value, and
// not from [Node.FilePath] of the Node it got. In the Source
// [NewSourceFromLayers] builds, that Node belongs to the merged
// document, which has the file path of the lowest layer, whichever layer
// holds the value. Origin returns the Node of the value in the file of
// its layer, and the receiver where the validator runs on one file.
//
// # Unparsed Documents
//
// A document with a YAML syntax error has no tree to check. Node.Validate
// and a decode return the syntax error [Node.Err] returns before any
// validator runs, so a validator they run always gets a document that
// parsed. A caller that calls Check itself can hand it a Node whose
// document did not parse. [Node.Decode], [Node.At], and [Node.Nodes]
// return the syntax error for such a Node, and a validator passes that
// error on. [ValidatorFunc], [MultiValidator], [ChainValidator], and the
// validators of [go.jacobcolvin.com/niceyaml/schema] return it before
// they read the Node.
type Validator interface {
	Check(ctx context.Context, n *Node) error
}

// ValidatorFunc adapts a function to the [Validator] interface.
//
//	kindPath := paths.Current().Child("kind")
//	known := niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
//		node, err := n.At(kindPath)
//		if err != nil {
//			return err
//		}
//
//		kind, err := node.Decode[string](ctx)
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
// The function writes its errors from the Node it got, as [Validator]
// asks, and Check returns what the function returns. The body of a
// ValidatorFunc is thus also right as the Check method of a type, and
// the two return the same error for the same Node.
//
// A check that knows nothing of YAML returns a plain error, which
// declares no fault. The function declares the document at fault for
// that error by returning the check inside [Invalid], which returns nil
// for a value that passes:
//
//	return niceyaml.Invalid(check(v))
type ValidatorFunc func(ctx context.Context, n *Node) error

// Check implements [Validator]. It returns what f returns, as f wrote
// it, so a nil [*Error] or [*SourceError] pointer from f comes back as
// that pointer, which [Node.Validate] reads as no failure. For a
// document that did not parse, it returns the syntax error [Node.Err]
// returns without calling f.
func (f ValidatorFunc) Check(ctx context.Context, n *Node) error {
	if n.doc.err != nil {
		return n.doc.err
	}

	return f(ctx, n)
}

// MultiValidator returns a [Validator] that runs every validator in
// order and reports every failure, where [ChainValidator] stops at the
// first that fails. It suits rules that check a document independently,
// as the rules of a linter do, such as a schema and a check on the names
// it uses, so one run reports the violations of both:
//
//	config, err := doc.Decode[Config](ctx, niceyaml.WithValidator(niceyaml.MultiValidator(schema, names)))
//
// Each validator sees the node whatever the ones before it reported, so
// a validator that decodes the node reports its own failure beside a
// schema violation of the same value. A validator that needs an earlier
// one to have passed goes in a ChainValidator instead. The run skips a
// nil validator, and one that holds a nil pointer or func.
//
// Two or more failures come back joined in the order given, each as the
// validator wrote it, so [errors.Is] matches any one of them. The Node
// that runs the MultiValidator binds the join as one tree, as [Node.Bind]
// describes, so each failure binds at its own location and each line of
// the message carries the source and the position of its own failure:
//
//	svc.yaml:1:7: $.name: reserved name
//	svc.yaml:2:7: $.port: 0 is less than 1
//
// The message of that binding lists the failures in the order of their
// positions, as the tree shows them.
//
// A lone failure comes back as it would if that validator ran alone. No
// failure is no error. A context that ends stops the run, and the error
// is then the one the context reports, or the one the validator that saw
// it end returned.
//
// MultiValidator copies the validators it gets, so a caller that edits
// the slice it passed changes nothing in the Validator.
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

			// A typed nil pointer is no failure, as [Node.Bind] reads it.
			err = dv.Check(ctx, n)
			if isNothing(err) {
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
//	config, err := doc.Decode[Config](ctx, niceyaml.WithValidator(niceyaml.ChainValidator(schema, refs)))
//
// A validator runs only after every validator before it has passed, so
// it can trust the shape the schema checked, and one that decodes the
// node reports no value the schema already rejected. Rules that check a
// document independently go in a MultiValidator instead, which reports
// the failures of all of them. The run skips a nil validator, and one
// that holds a nil pointer or func, and a ChainValidator of no
// validators passes every Node.
//
// A decode runs the validators of repeated [WithValidator] options the
// same way, so these two decodes return the same error:
//
//	doc.Decode[Config](ctx, niceyaml.WithValidator(niceyaml.ChainValidator(schema, refs)))
//	doc.Decode[Config](ctx, niceyaml.WithValidator(schema), niceyaml.WithValidator(refs))
//
// The failure comes back as the validator wrote it, so the Node that
// runs the ChainValidator binds it as it binds the failure of that
// validator alone.
//
// ChainValidator copies the validators it gets, so a caller that edits
// the slice it passed changes nothing in the Validator.
func ChainValidator(validators ...Validator) Validator {
	validators = slices.Clone(validators)

	return ValidatorFunc(func(ctx context.Context, n *Node) error {
		return n.firstFailure(ctx, validators)
	})
}

// SkipEmpty returns a [Validator] that passes a document with no content
// and runs v on every other Node. [Node.IsEmpty] reports such a
// document: an empty file, a file of comments alone, or a "---" header
// with nothing but comments below it.
//
// An empty document validates as any other, so a schema that wants a
// mapping rejects it, and a registry that requires a schema finds none
// for it. [Source.Documents] and [Source.ValidateDocuments] leave out the
// empty documents of a file that holds a document with content, and keep
// the one document of an empty file. A caller whose file may be empty as
// a whole wraps its validator:
//
//	err := source.ValidateDocuments(ctx, niceyaml.SkipEmpty(reg))
//
// So does a caller that validates each document [Source.AllDocuments]
// returns with [Node.Validate], where every empty document runs the
// validator.
//
// A decode takes it the same way, so a configuration file that may be
// empty decodes to the zero value:
//
//	config, err := doc.Decode[Config](ctx, niceyaml.WithValidator(niceyaml.SkipEmpty(schema)))
//
// SkipEmpty passes only the documents IsEmpty reports. A document that
// holds a value runs v, whatever the value, so v still checks an
// explicit null such as `--- null` and an alias with no anchor before
// it. A document that did not parse returns its syntax error. A Node
// that [Node.At] or [Node.Nodes] scopes below the root is not a
// document, so v runs on it.
//
// SkipEmpty returns the error of v as v wrote it, and a nil v passes
// every Node. SkipEmpty takes one validator, and it wraps the one that
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
//	for _, doc := range docs {
//		if err := doc.Validate(ctx, reg); err != nil {
//			return err
//		}
//	}
//
// Validate takes one validator, and that validator decides how many
// violations the node reports. A caller with several validators names how
// they run together. [MultiValidator] runs every one and reports every
// failure, which suits rules that check the node independently, as the
// rules of a linter do. [ChainValidator] stops at the first that fails,
// which suits a check that needs an earlier one to pass, such as one
// that reads the values a schema requires:
//
//	err := doc.Validate(ctx, niceyaml.MultiValidator(schema, names))
//	err := doc.Validate(ctx, niceyaml.ChainValidator(schema, refs))
//
// [SkipEmpty] wraps either one, so that a document with no content
// passes.
//
// A nil v runs nothing, and neither does one that holds a nil pointer or
// func, so Validate then returns what [Node.Err] returns. A validator
// that returns a nil [*Error] or [*SourceError] pointer passes.
//
// A validator writes its errors from the Node it got, as [Validator]
// describes, and Validate binds them to the source as a [SourceError]
// through [Node.Bind], so an [*Error] renders its location and any other
// error names the source. A Node from [Node.At] or [Node.Nodes] also
// points an error with no location at its own value, as Node.Bind
// describes. An error the validator bound itself comes back as it is.
// A direct call to the Check method of a validator is the raw step. It
// returns the error as the validator wrote it, whichever validator it
// is: an unbound error names no source and its `@` paths read from the
// Node the validator got, and an error the validator bound itself is
// bound already. Validate is thus the way to run a validator, the
// caller's own or one it did not write, and the way a validator runs
// another on a Node of its choosing, as item.Validate(ctx, inner) runs
// inner on an element.
//
// A document that did not parse fails before v runs, with the syntax
// error [Node.Err] returns, even when v is nil. A caller that validates
// each document of a file thus collects the syntax errors of the file in
// the same loop, as [Source.ValidateDocuments] does.
func (n *Node) Validate(ctx context.Context, v Validator) error {
	return n.validate(ctx, []Validator{v})
}

// validate runs validators in order on n, and binds the first error with
// n when the validator left it unbound. A document that did not parse
// returns its syntax error, and no validator runs.
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

		// A typed nil pointer is no failure, as [Node.Bind] reads it.
		err := dv.Check(ctx, n)
		if isNothing(err) {
			continue
		}

		return err //nolint:wrapcheck // The validator's own error, which the Node that runs the validator binds.
	}

	return nil
}
