package niceyaml

import (
	"bytes"
	"cmp"
	"context"
	"encoding"
	"errors"
	"fmt"
	"math"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
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
// A program that can list what its environment or its flags set passes
// those values as a layer of [Layers], which validates them as it
// validates a file. SelfValidate is for the program whose library writes
// them into the value instead, or that applies its defaults there. That
// program decodes the file with the walk off, sets the rest of the
// value, and then validates what it holds, so a required field that only
// the environment sets passes:
//
//	var cfg Config
//	if err := doc.DecodeInto(ctx, &cfg, niceyaml.WithSelfValidation(false)); err != nil {
//		return err
//	}
//
//	applyEnv(&cfg)
//
//	return doc.SelfValidate(ctx, &cfg)
//
// No [Validator] of the decode sees a value set this way. A schema thus
// leaves the value unchecked, and reports a key it requires as missing
// when only the environment sets it.
//
// A program that layers one file over another merges them through
// [Layers]. [Layers.SelfValidate] then binds each error in the file that
// holds its value, where a SelfValidate through the Node of one file
// binds every error in that file:
//
//	return niceyaml.NewLayers(base, prod).SelfValidate(ctx, &cfg)
//
// Layers.SelfValidate returns the error of a file that did not parse,
// and the walk does not run.
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
// Any v works but nil and a nil pointer, which each return an error
// wrapping [ErrSelfValidateTarget], bound to the source. A v that is no
// pointer validates as a copy, so a Validate with a pointer receiver
// changes the copy and leaves v as it was.
//
// A document that did not parse has no tree. Every key of v then takes
// the text of its Go value, and each error binds with no position, as
// [Node.Bind] describes. SelfValidate returns those errors rather than
// the syntax error [Node.Err] returns.
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
	err := checkSelfValidateTarget(v)
	if err != nil {
		return n.bindOwn(err)
	}

	walked := walkSelfValidators(ctx, v, n, n.yamlOptions(cfg), cfg.unmarshalers)

	return bindTree(walked, binder{src: n.source, node: n, locate: true})
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
// The walk names each value as a decode of YAML into v names it. A
// field takes its yaml tag, its json tag when it has no yaml tag, and
// its lowercased name when it has neither. A struct embedded with no
// inline option is such a field, under the lowercased name of its type.
// A value that another format decoded, such as a JSON body, may thus
// report paths that its own keys do not spell. A field that go-yaml
// never decodes does not validate, as Node.SelfValidate describes.
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
// Any v works but nil and a nil pointer, which each return an error
// wrapping [ErrSelfValidateTarget], bound to no document too. The walk
// stops once ctx ends, or once a Validate returns the error of a
// context that ended, and SelfValidateValue then returns that error
// alone, as [SelfValidator] describes.
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
	err := checkSelfValidateTarget(v)
	if err != nil {
		return BindValue(err)
	}

	return BindValue(walkSelfValidators(ctx, v, nil, nil, optionUnmarshalers{}))
}

// checkSelfValidateTarget returns [ErrSelfValidateTarget] when v is nil
// or a nil pointer, which holds no value to validate. A pointer that v
// points to may be nil, as a decode of a null leaves it.
func checkSelfValidateTarget(v any) error {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return ErrSelfValidateTarget
	}

	if rv.Kind() == reflect.Pointer && rv.IsNil() {
		return fmt.Errorf("%w: got %T", ErrSelfValidateTarget, v)
	}

	return nil
}

// walkSelfValidators runs Validate on every value in the tree of v that
// implements [SelfValidator], where v is a non-nil pointer or a value
// that is no pointer. The walk follows v rather than n, so v need not be
// the value n decoded to. It reads n for the text the document spells
// each map key with, and decodes those keys with ctx and opts, as the
// decode of v did. The unmarshalers are the ones those options give a
// type. It also reads n to bind the errors under a path that names
// several keys. A map the document does not hold at its path has no node
// there, so its keys take the text of their Go values. A nil n stands
// for no document, as [SelfValidateValue] walks a value, so every key
// takes that text and the walk reads no option.
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
func walkSelfValidators(
	ctx context.Context, v any, n *Node, opts []yaml.DecodeOption, unmarshalers optionUnmarshalers,
) error {
	w := selfWalker{
		ctx:      ctx,
		node:     n,
		opts:     opts,
		walked:   map[visit]walkResult{},
		reach:    math.MaxInt,
		scanning: map[visit]bool{},
		scanned:  map[visit]bool{},

		unmarshalers: unmarshalers,
	}
	w.walk(reflect.ValueOf(v), place{}, nil)

	if w.ended != nil {
		return w.ended
	}

	switch len(w.errs) {
	case 0:
		return nil
	case 1:
		return w.errs[0]
	default:
		return errors.Join(w.errs...)
	}
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
// Before it reads the keys of a map, or walks the elements of a slice or
// array, the walker scans the values below for one that implements
// [SelfValidator], and passes the value at once when none does. The
// values go-yaml decodes into an interface never implement it, so a
// value decoded as any walks no further than the scan.
type selfWalker struct {
	ctx     context.Context
	node    *Node
	decoder *yaml.Decoder
	opts    []yaml.DecodeOption
	// The types the options of the decode give an unmarshaler.
	unmarshalers optionUnmarshalers
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

	field, whole := w.unmarshalers.decodesWhole(v.Type())

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
	// The interfaces go-yaml decodes a value through when its pointer
	// implements one, in place of decoding field by field. Go-yaml checks
	// them in this order and calls the first one the pointer implements.
	unmarshalerTypes = []reflect.Type{
		reflect.TypeFor[yaml.BytesUnmarshalerContext](),
		reflect.TypeFor[yaml.BytesUnmarshaler](),
		reflect.TypeFor[yaml.InterfaceUnmarshalerContext](),
		reflect.TypeFor[yaml.InterfaceUnmarshaler](),
		reflect.TypeFor[yaml.NodeUnmarshaler](),
		reflect.TypeFor[yaml.NodeUnmarshalerContext](),
		reflect.TypeFor[encoding.TextUnmarshaler](),
	}

	// The import path of the go-yaml ast package, which declares the node
	// types go-yaml sets whole.
	astPackage = reflect.TypeFor[ast.StringNode]().PkgPath()

	// The result of [mayHoldValidator] for each type it has read.
	holdsValidator typeCache[bool]

	// The result of [implementsSelfValidator] for each type it has read.
	ownsValidator typeCache[bool]

	// The result of [decoderField] for each type it has read, with -1 for
	// a type that has no such field.
	decoderFields typeCache[int]

	// The result of [jsonDecoderField] for each type it has read.
	jsonDecoderFields typeCache[int]

	// The result of [decodesItself] for each type it has read.
	decodesWhole typeCache[bool]

	// The result of [fieldsOf] for each struct type it has read.
	walkedFields typeCache[*structFields]
)

// typeCache holds a value computed once for each type it reads. A type
// never changes, so every walk shares the values.
type typeCache[V any] struct {
	values sync.Map
}

// get returns the value of t. The first call for t runs compute and
// stores its result for the calls after it.
func (c *typeCache[V]) get(t reflect.Type, compute func(reflect.Type) V) V {
	if cached, ok := c.values.Load(t); ok {
		if v, ok := cached.(V); ok {
			return v
		}
	}

	v := compute(t)
	c.values.Store(t, v)

	return v
}

// decodesItself reports whether go-yaml decodes a value of type t whole,
// so the fields, elements, or entries of the value need not mirror the
// document. A type decodes itself through an unmarshaler method of its
// own, or as an [ast.Node] the ast package declares, which the decoder
// sets to the node it decodes rather than decoding field by field. The
// tokens of a node also link to every other token of the file. A struct
// that gets the methods of an [ast.Node] from an embedded field decodes
// field by field. The method set of the pointer holds the methods of
// both receivers, as the decoder checks it.
func decodesItself(t reflect.Type) bool {
	return decodesWhole.get(t, func(t reflect.Type) bool {
		return isSyntaxNode(t) || slices.ContainsFunc(unmarshalerTypes, reflect.PointerTo(t).Implements)
	})
}

// isSyntaxNode reports whether t is an [ast.Node] type the ast package
// declares, which go-yaml sets to the node it decodes. The tokens of such
// a node link to every other token of the file, so the self-validation
// walk reads nothing below one.
func isSyntaxNode(t reflect.Type) bool {
	return t.PkgPath() == astPackage && reflect.PointerTo(t).Implements(reflect.TypeFor[ast.Node]())
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
	i := decoderFields.get(t, findDecoderField)

	return i, i >= 0
}

// findDecoderField returns the index [decoderField] returns, or -1 when
// t has no such field.
func findDecoderField(t reflect.Type) int {
	k := slices.IndexFunc(unmarshalerTypes, reflect.PointerTo(t).Implements)
	if k < 0 {
		return -1
	}

	return promotingField(t, unmarshalerTypes[k])
}

// jsonDecoderField returns the index of the embedded field of t whose
// UnmarshalJSON method decodes t under [WithJSONUnmarshalers], or -1
// when t declares the method or has none. It reads t as [decoderField]
// reads a type with an UnmarshalYAML method, and holds only for a type
// go-yaml decodes through UnmarshalJSON, as
// [optionUnmarshalers.decodesJSON] reports one.
func jsonDecoderField(t reflect.Type) int {
	return jsonDecoderFields.get(t, func(t reflect.Type) int {
		if !reflect.PointerTo(t).Implements(jsonUnmarshalerType) {
			return -1
		}

		return promotingField(t, jsonUnmarshalerType)
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
	return holdsValidator.get(t, func(t reflect.Type) bool {
		return reachesValidator(t, map[reflect.Type]bool{})
	})
}

// reachesValidator reports whether t, or a type below it, can hold a
// [SelfValidator], as [mayHoldValidator] describes. The seen set holds
// the types the search has reached, so the search reads a type that holds
// itself once, and a validator below that type shows on the first read.
func reachesValidator(t reflect.Type, seen map[reflect.Type]bool) bool {
	if seen[t] {
		return false
	}

	seen[t] = true

	if t.Kind() == reflect.Interface || implementsSelfValidator(t) {
		return true
	}

	// The walk reads nothing below a node of the syntax tree.
	if isSyntaxNode(t) {
		return false
	}

	switch t.Kind() {
	case reflect.Map:
		return reachesValidator(t.Key(), seen) || reachesValidator(t.Elem(), seen)

	case reflect.Pointer, reflect.Slice, reflect.Array:
		return reachesValidator(t.Elem(), seen)

	case reflect.Struct:
		for field := range t.Fields() {
			if _, _, skip := yamlfield.Name(field); !skip && reachesValidator(field.Type, seen) {
				return true
			}
		}

	default:
	}

	return false
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
		// those whose types may hold one, but that hold none.
		if !entriesMayHoldValidator(v.Type()) || !w.holdsBelow(v) {
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

		entries := make([]mapEntry, 0, v.Len())
		shared := map[any]int{}

		for iter := v.MapRange(); iter.Next(); {
			key := iter.Key()
			if k, ok := nameKey(key); ok {
				shared[k]++
			}

			entries = append(entries, mapEntry{key: key, value: iter.Value()})
		}

		// The names cannot tell apart several keys that [nameKey] gives
		// one value, so none of them takes the text of a document key.
		// Several NaN keys give one value, and so do several time keys of
		// one instant and zone, or several pointer keys to equal values.
		// Such keys then share one path, as below.
		for k, n := range shared {
			if n > 1 {
				delete(names, k)
			}
		}

		for i := range entries {
			entries[i].seg = mapKey(entries[i].key, names)
			entries[i].typeName = keyTypeName(entries[i].key)
		}

		compareKeys := func(a, b mapEntry) int {
			return cmp.Or(strings.Compare(a.seg, b.seg), strings.Compare(a.typeName, b.typeName))
		}

		slices.SortFunc(entries, compareKeys)

		// A path names a key by its text alone, so keys that share a
		// segment share a path, which resolves to the document entry of
		// one of them at most. Keys of different types can share one,
		// such as 1 and "1" in a map[any]T, and so can the keys above
		// that no name tells apart, such as several NaN keys beside one
		// the document spells NaN. A `<<` merge can bring in such a key
		// beside one the mapping spells, even where the parser rejects
		// duplicate keys. Several pointer keys of one type share a
		// segment too, when [mapKey] names them by that type. The errors
		// under such a path bind with no position.
		ambiguous := map[string]bool{}

		for i := 1; i < len(entries); i++ {
			if entries[i].seg == entries[i-1].seg {
				ambiguous[entries[i].seg] = true
			}
		}

		for len(entries) > 0 {
			n := 1
			for n < len(entries) && compareKeys(entries[0], entries[n]) == 0 {
				n++
			}

			seg := entries[0].seg
			if !w.walkEntries(at.child(seg), entries[:n], ambiguous[seg]) {
				ok = false
			}

			entries = entries[n:]
		}

	default:
	}

	return ok
}

// mapEntry holds one entry of a map the walk is inside of, with the path
// segment and the type name its key orders by.
type mapEntry struct {
	key, value    reflect.Value
	seg, typeName string
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
func (w *selfWalker) walkEntries(at place, entries []mapEntry, ambiguous bool) bool {
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
		if !w.walk(e.key, at.key(), nil) {
			ok = false
		}

		if !w.walk(e.value, at, nil) {
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
// the [Node] of the walk, as [Node.Bind] binds it at the root of a
// document, or to no document when the walk has no Node, as [BindValue]
// binds it.
func (w *selfWalker) bind(err error) error {
	if w.node == nil {
		return BindValue(err)
	}

	return bindTree(err, binder{src: w.node.source, node: w.node})
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
	return walkedFields.get(t, readFields)
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

// holdsBelow reports whether a value below v, a slice, array, or map
// the walk is inside of, implements [SelfValidator] where the walk
// would validate it. When it reports false, the walk of the values below
// v validates nothing, so the walk passes v's children at once.
//
// The scan reads each pointer, map, and slice it reaches once, however
// many paths lead there.
func (w *selfWalker) holdsBelow(v reflect.Value) bool {
	defer clear(w.scanning)

	if v.Kind() != reflect.Array && ownsAddress(v) {
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

	if field, _ := w.unmarshalers.decodesWhole(v.Type()); field >= 0 {
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
	return ownsValidator.get(t, func(t reflect.Type) bool {
		return reflect.PointerTo(t).Implements(reflect.TypeFor[SelfValidator]()) && !promotesMethod(t, "Validate")
	})
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

	w.errs = append(w.errs, rebase(err, at.path(), false, true, w.ambiguous, w.fallback()))

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
func (w *selfWalker) keyNames(node ast.Node, t reflect.Type) map[any]string {
	names := map[any]string{}
	w.collectKeyNames(node, t, names, map[*ast.MappingNode]bool{})

	return names
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

// collectKeyNames adds the keys of the mapping node holds, and of the
// mappings it merges, to names, as [selfWalker.keyNames] describes. It
// reaches the mapping through the anchors, tags, and aliases on node, as
// [selfWalker.valueNode] does. It reports false when the walk stopped at
// a merge with an alias that does not resolve, so the caller stops too.
//
// The walk goes in reverse document order, so the first name it sets
// for a value is the one the decode keeps. The first time the walk
// reaches a mapping is that mapping's last occurrence in document
// order, so the seen set skips only occurrences whose names would lose,
// and it also stops merge cycles.
func (w *selfWalker) collectKeyNames(
	node ast.Node, t reflect.Type, names map[any]string, seen map[*ast.MappingNode]bool,
) bool {
	mapping, ok := astnode.Content(w.valueNode(node, false)).(*ast.MappingNode)
	if !ok || seen[mapping] {
		return true
	}

	seen[mapping] = true

	// The decoder sets each entry in document order, and a merge sets
	// the keys of its sources where it stands, so a later entry wins
	// whether it is a key of the mapping or a merge. A merge with an
	// alias that does not resolve may set any key, so the walk stops
	// there and names no earlier entry, of this mapping or of a mapping
	// that merges this one.
	for _, entry := range slices.Backward(mapping.Values) {
		if entry == nil || entry.Key == nil {
			continue
		}

		if !entry.Key.IsMergeKey() {
			w.addKeyName(entry.Key, t, names)

			continue
		}

		sources, err := w.pathResolver().MergeSources(&ast.MappingNode{
			Values: []*ast.MappingValueNode{entry},
		})
		if err != nil {
			return false
		}

		for _, src := range slices.Backward(sources) {
			if !w.collectKeyNames(src, t, names, seen) {
				return false
			}
		}
	}

	return true
}

// pathResolver returns the [paths.Resolver] for the document of the
// walk.
func (w *selfWalker) pathResolver() *paths.Resolver {
	return w.node.doc.pathResolver()
}

// keyDecoder returns the go-yaml decoder for the keys of the walk, and
// creates it when the walk first decodes a key.
func (w *selfWalker) keyDecoder() *yaml.Decoder {
	if w.decoder == nil {
		w.decoder = yaml.NewDecoder(bytes.NewReader(nil), w.opts...)
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

	k, ok := nameKey(decoded.Elem())
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
// [decodeTree] of the document, so an integer under a !!int tag decodes
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
				view = w.node.doc.decodeTree().parsedView
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

// nanKey stands in names for a NaN key, since a NaN equals no value,
// itself included, and so finds no entry under its own value.
type nanKey struct{}

// timeKey stands in names for a [time.Time] key. A [time.Time] holds a
// pointer to its location, and a decode of an offset such as +05:30
// makes a new location each time, so the key in the map equals no key
// that decodes from the same text. The fields together form the key in
// names.
//
//nolint:unused // The fields tell the keys of names apart.
type timeKey struct {
	zone   string
	sec    int64
	nsec   int
	offset int
}

// nameKey returns the value names holds the text of key under: [nanKey]
// for a float NaN and [timeKey] for a [time.Time], alone or behind an
// interface, or else the value of key itself. A pointer key stands for
// the value it points to, as [pointee] gives it. The bool result is
// false for a key that cannot key a map.
func nameKey(key reflect.Value) (any, bool) {
	key = pointee(key)
	if !key.Comparable() {
		return nil, false
	}

	v := key
	if v.Kind() == reflect.Interface && !v.IsNil() {
		v = v.Elem()
	}

	if v.CanFloat() && math.IsNaN(v.Float()) {
		return nanKey{}, true
	}

	if tm, ok := reflect.TypeAssert[time.Time](v); ok {
		zone, offset := tm.Zone()

		return timeKey{zone: zone, sec: tm.Unix(), nsec: tm.Nanosecond(), offset: offset}, true
	}

	return key.Interface(), true
}

// pointee returns the value key points to when key is a non-nil pointer
// to a value that can key a map, and key itself otherwise. Since go-yaml
// decodes a pointer key as the value it points to, that value, rather
// than the address, names the key. The value a pointer points to may
// hold a slice or map that refers back to itself, which no formatting
// finishes. So a pointer to a value that cannot key a map stays a
// pointer, and [mapKey] names it by its type. A pointer held in an
// interface stays too, since go-yaml decodes no key of an interface
// type to one.
func pointee(key reflect.Value) reflect.Value {
	if key.Kind() == reflect.Pointer && !key.IsNil() && key.Elem().Comparable() {
		return key.Elem()
	}

	return key
}

// mapKey returns the path segment for a map key: its text in names, as
// the document spells it, under the value [nameKey] gives, or else the
// string itself, or the formatted value of any other key. A pointer key
// stands for the value it points to, as [pointee] gives it.
//
// A non-nil pointer left after [pointee], alone or behind an interface,
// gives its type in parentheses, such as (*[]interface {}). Formatting
// it would print either the value it points to, which may refer back
// to itself so that no formatting finishes, or its address, which
// changes from run to run. Several such keys of one type then share a
// path.
func mapKey(key reflect.Value, names map[any]string) string {
	if k, ok := nameKey(key); ok {
		if name, ok := names[k]; ok {
			return name
		}
	}

	key = pointee(key)
	if key.Kind() == reflect.String {
		return key.String()
	}

	v := key
	if v.Kind() == reflect.Interface && !v.IsNil() {
		v = v.Elem()
	}

	if v.Kind() == reflect.Pointer && !v.IsNil() {
		return "(" + v.Type().String() + ")"
	}

	return fmt.Sprint(key.Interface())
}

// keyTypeName returns the name of the type a map key holds: the dynamic
// type of a key an interface holds, or "" for a nil one.
func keyTypeName(key reflect.Value) string {
	if key.Kind() == reflect.Interface {
		if key.IsNil() {
			return ""
		}

		key = key.Elem()
	}

	return key.Type().String()
}
