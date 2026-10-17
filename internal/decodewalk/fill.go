package decodewalk

import (
	"context"
	"reflect"

	"github.com/goccy/go-yaml/ast"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/datapath"
	"go.jacobcolvin.com/niceyaml/internal/unmarshaler"
	"go.jacobcolvin.com/niceyaml/internal/yamlfield"
)

// Starts gives a decode the value it starts from for each value it
// creates.
type Starts struct {
	// Of returns the value that a decode starts from when it creates a
	// value of type t, which it reads from node.
	Of func(t reflect.Type, node ast.Node) (reflect.Value, error)
	// Sets reports whether Of returns a value other than zero for the
	// type t, given that it returns zero for every type that a value of t
	// holds.
	Sets func(t reflect.Type) bool
}

// Fill returns the value of type t that a decode of node gives when it
// starts from start. Such a decode starts each value it creates from the
// value that starts gives its type. The argument decoded is the value
// that the go-yaml decoder gave for node into a value that held start.
//
// The decoder starts each field of a struct from the value the field
// holds, so decoded holds start wherever the document omits a field. It
// starts every other value from the zero value of its type: each element
// of a slice or an array, each value of a map, and the target of each
// pointer. At an alias, it takes the value that the anchor decoded to.
// Fill builds each of those values again from its start, field by field,
// as a decode that starts there does:
//
//   - A struct that reads a mapping takes each field that the document
//     writes from the value below it, and keeps the start of every other
//     field. A field of an inline struct keeps its start when the struct
//     that holds it inline also names the field. The decoder sets such a
//     field to zero.
//   - A pointer that the document fills gets a target of its own. The
//     target starts from the value the pointer held, or from the start of
//     its type for a nil pointer. A null sets the pointer to nil.
//   - The places that aliases of one anchor fill share one value when
//     they start from equal values, as the decoder shares it. Two pointers
//     there share one target. A place that starts from another value
//     builds a value of its own.
//   - Each element and each map value starts from the start of its type,
//     and so does one the document writes as a null.
//   - A null leaves any other value at its start. A value at an alias
//     that the resolver of r cannot follow, such as one to a reference
//     document, keeps the value of the decoder.
//   - A value that [unmarshaler.Options.Whole] reports as decoding itself
//     decodes again from its start when the decoder did not start it
//     there. Its method then sees the start.
//   - Each interface, each [yaml.MapSlice], and each scalar keeps the
//     value of the decoder. At an alias, that value is the value of the
//     anchor.
//
// A value that starts from zero keeps the value of the decoder when every
// type it reaches starts from zero. Building such a value again gives the
// same value. The keys of a map keep the value of the decoder. A value
// below a node that holds itself through an alias keeps the value of the
// decoder too. Fill returns any error from starts, and any error from a
// value that decodes itself again.
func Fill(
	ctx context.Context, r Rules, starts Starts, t reflect.Type, node ast.Node, start, decoded reflect.Value,
) (reflect.Value, error) {
	f := &filler{
		walker: newWalker(ctx, r, nil),
		starts: starts,
		onPath: map[seenKey]bool{},
		memo:   map[seenKey][]memoEntry{},
		reach:  map[reflect.Type]bool{},
	}

	return f.place(t, node, start, decoded, true)
}

// filler holds the state of one [Fill].
type filler struct {
	*walker

	starts Starts
	// The structs, maps, and sequences the fill is inside of, by the type
	// it reads each as.
	onPath map[seenKey]bool
	// The values the fill built for each node and type, with the start
	// of each.
	memo map[seenKey][]memoEntry
	// Whether a value of each type reaches a type that starts from a
	// value other than zero.
	reach map[reflect.Type]bool
}

// memoEntry is a value that a [filler] built, and the start it built the
// value from.
type memoEntry struct {
	start, value reflect.Value
}

// place returns the value of type t that a decode of held gives from
// start, where decoded is the value the decoder gave. With exact set,
// the decoder started the value from start.
func (f *filler) place(t reflect.Type, held ast.Node, start, decoded reflect.Value, exact bool) (reflect.Value, error) {
	err := f.ctx.Err()
	if err != nil {
		return reflect.Value{}, err //nolint:wrapcheck // The context error passes through.
	}

	start = valueOr(start, t)
	decoded = valueOr(decoded, t)

	// Every value below starts from zero, as the decoder starts it, so the
	// fill would build the value the decoder gave.
	if start.IsZero() && !f.reaches(t) {
		return decoded, nil
	}

	node, ok := datapath.Read(f.rules.Resolver, held)

	switch {
	case !ok && !f.null(held):
		// The decoder read an alias that the document cannot follow, such
		// as one to an anchor of a reference document.
		return decoded, nil

	case !ok && t.Kind() == reflect.Pointer:
		return reflect.Zero(t), nil

	case !ok:
		return start, nil
	}

	exact = exact && !isAlias(held)
	content := datapath.Deref(f.rules.Resolver, node)
	key := seenKey{typ: t, node: content}

	if v, ok := f.built(key, start); ok {
		return v, nil
	}

	if f.onPath[key] {
		return decoded, nil
	}

	f.onPath[key] = true
	defer delete(f.onPath, key)

	v, err := f.build(t, held, node, content, start, decoded, exact)
	if err != nil {
		return reflect.Value{}, err
	}

	// The start might be a field of a value the fill is building, which
	// changes when the field takes v.
	if start.CanInterface() {
		saved := reflect.New(t).Elem()
		saved.Set(start)

		f.memo[key] = append(f.memo[key], memoEntry{start: saved, value: v})
	}

	return v, nil
}

// reaches reports whether a value of type t reaches a type that starts
// from a value other than zero, as [yamlfield.Reaches] finds the types
// that a decode reaches.
func (f *filler) reaches(t reflect.Type) bool {
	found, ok := f.reach[t]
	if !ok {
		found = yamlfield.Reaches(t, func(t reflect.Type) (bool, bool) { return f.starts.Sets(t), false })
		f.reach[t] = found
	}

	return found
}

// built returns the value that the fill built for the node and the type
// of key from a start equal to start, and reports whether it built one.
// The places that aliases of one anchor fill thus share a value when they
// start from equal values, as the decoder shares it. A chain of aliases
// also fills in linear time.
func (f *filler) built(key seenKey, start reflect.Value) (reflect.Value, bool) {
	if !start.CanInterface() {
		return reflect.Value{}, false
	}

	for _, m := range f.memo[key] {
		if reflect.DeepEqual(m.start.Interface(), start.Interface()) {
			return m.value, true
		}
	}

	return reflect.Value{}, false
}

// build returns the value of type t that a decode of held gives from
// start, as [filler.place] describes. The decode of held reads node and
// its content.
func (f *filler) build(
	t reflect.Type, held, node, content ast.Node, start, decoded reflect.Value, exact bool,
) (reflect.Value, error) {
	if t.Kind() == reflect.Pointer {
		return f.pointer(t, held, node, start, decoded, exact)
	}

	if t.Kind() == reflect.Interface || t == unmarshaler.MapSliceType {
		return decoded, nil
	}

	if _, whole := f.rules.Unmarshalers.Whole(t); whole {
		return f.whole(t, node, start, decoded, exact)
	}

	mapping, isMapping := content.(*ast.MappingNode)
	seq, isSeq := content.(*ast.SequenceNode)

	switch {
	case isMapping && t.Kind() == reflect.Struct:
		return f.structOf(t, mapping, start, decoded, exact)
	case isMapping && t.Kind() == reflect.Map:
		return f.mapOf(t, mapping, decoded)
	case isSeq && (t.Kind() == reflect.Slice || t.Kind() == reflect.Array):
		return f.elements(t, seq, decoded)
	default:
		return decoded, nil
	}
}

// pointer returns the value of the pointer type t that a decode of held
// gives from start. The decode of held reads node. The target is new, and
// it starts from the target of start, or from the start of its type when
// start is nil. With exact set, the decoder started a target from the
// target of start too.
func (f *filler) pointer(
	t reflect.Type, held, node ast.Node, start, decoded reflect.Value, exact bool,
) (reflect.Value, error) {
	if decoded.IsNil() {
		return decoded, nil
	}

	var (
		from reflect.Value
		err  error
	)

	if start.IsNil() {
		from, err = f.starts.Of(t.Elem(), node)
		if err != nil {
			return reflect.Value{}, err
		}
	} else {
		from = start.Elem()
	}

	target, err := f.place(t.Elem(), held, from, decoded.Elem(), exact && !start.IsNil())
	if err != nil {
		return reflect.Value{}, err
	}

	p := reflect.New(t.Elem())
	p.Elem().Set(target)

	return p, nil
}

// whole returns the value of type t that a decode of node gives from
// start, where t decodes itself. The value decodes again from start unless
// the decoder started it there or start is zero.
func (f *filler) whole(t reflect.Type, node ast.Node, start, decoded reflect.Value, exact bool) (reflect.Value, error) {
	if exact || start.IsZero() {
		return decoded, nil
	}

	v := reflect.New(t)
	v.Elem().Set(start)

	err := f.rules.Decode(f.rules.View(node), v.Interface())
	if err != nil {
		return reflect.Value{}, err
	}

	return v.Elem(), nil
}

// structOf returns the value of the struct type t that a decode of
// mapping gives from start.
func (f *filler) structOf(
	t reflect.Type, mapping *ast.MappingNode, start, decoded reflect.Value, exact bool,
) (reflect.Value, error) {
	merges := !yamlfield.IgnoresMerges(t)

	// The decoder decodes no field from such a mapping.
	if !f.readsAsStruct(mapping) || merges && f.mergeRefused(mapping) {
		return start, nil
	}

	result := reflect.New(t).Elem()
	result.Set(start)

	err := f.fields(t, mapping, merges, result, decoded, exact, nil)
	if err != nil {
		return reflect.Value{}, err
	}

	return result, nil
}

// fields sets each field of result that a decode of mapping writes, from
// decoded. The argument result is a struct of type t that holds its
// start. A struct held inline skips each field that hidden names, because
// the struct that holds it names that field too.
func (f *filler) fields(
	t reflect.Type,
	mapping *ast.MappingNode,
	merges bool,
	result, decoded reflect.Value,
	exact bool,
	hidden map[string]bool,
) error {
	for i := range t.NumField() {
		field := t.Field(i)

		name, inline, skip := yamlfield.Name(field)
		if skip || !result.Field(i).CanSet() {
			continue
		}

		if yamlfield.ReadsAnchor(field) {
			result.Field(i).Set(decoded.Field(i))

			continue
		}

		var (
			v   reflect.Value
			err error
		)

		switch {
		case inline:
			v, err = f.inline(field.Type, t, mapping, merges, result.Field(i), decoded.Field(i), exact)

		case hidden[name]:
			continue

		default:
			entry := f.entryOf(mapping, name, merges)
			if entry == nil {
				continue
			}

			v, err = f.place(field.Type, entry.Value, result.Field(i), decoded.Field(i), exact)
		}

		if err != nil {
			return err
		}

		result.Field(i).Set(v)
	}

	return nil
}

// inline returns the value, from start, of a field of type t that a
// struct of type owner holds inline. The field reads mapping, the mapping
// of owner.
func (f *filler) inline(
	t, owner reflect.Type,
	mapping *ast.MappingNode,
	merges bool,
	start, decoded reflect.Value,
	exact bool,
) (reflect.Value, error) {
	if t.Kind() == reflect.Pointer {
		return f.inlinePointer(t, owner, mapping, merges, start, decoded, exact)
	}

	if _, whole := f.rules.Unmarshalers.Whole(t); whole {
		return f.whole(t, mapping, start, decoded, exact)
	}

	switch t.Kind() {
	case reflect.Struct:
		result := reflect.New(t).Elem()
		result.Set(start)

		err := f.fields(t, mapping, merges, result, decoded, exact, renderNames(owner))
		if err != nil {
			return reflect.Value{}, err
		}

		return result, nil

	case reflect.Map:
		return f.mapOf(t, mapping, decoded)

	default:
		return decoded, nil
	}
}

// inlinePointer returns the value of a field of the pointer type t that
// a struct of type owner holds inline, as [filler.inline] does. The
// target is new, and it starts from the target of start, or from the
// start of its type when start is nil. With exact set, the decoder started
// a target from the target of start too.
func (f *filler) inlinePointer(
	t, owner reflect.Type,
	mapping *ast.MappingNode,
	merges bool,
	start, decoded reflect.Value,
	exact bool,
) (reflect.Value, error) {
	if decoded.IsNil() {
		return decoded, nil
	}

	base := unmarshaler.Base(t)

	from := start
	if from.IsNil() {
		v, err := f.starts.Of(base, mapping)
		if err != nil {
			return reflect.Value{}, err
		}

		from = reflect.New(base)
		from.Elem().Set(v)
	}

	target, err := f.inline(base, owner, mapping, merges, from.Elem(), decoded.Elem(), exact && !start.IsNil())
	if err != nil {
		return reflect.Value{}, err
	}

	p := reflect.New(base)
	p.Elem().Set(target)

	return p, nil
}

// mapOf returns the value of the map type t that a decode of mapping
// gives, where decoded is the map the decoder gave. Each value starts
// from the start of its type. A key keeps the value of the decoder.
func (f *filler) mapOf(t reflect.Type, mapping *ast.MappingNode, decoded reflect.Value) (reflect.Value, error) {
	if decoded.IsNil() {
		return decoded, nil
	}

	result := reflect.MakeMapWithSize(t, decoded.Len())

	for k, v := range decoded.Seq2() {
		result.SetMapIndex(k, v)
	}

	// The decoder reads the entries in document order, the entries of a
	// merge key where the key stands, and a later entry replaces an
	// earlier one with the same key.
	var (
		keys    []reflect.Value
		entries []*ast.MappingValueNode
	)

	index := map[any]int{}

	for entry := range datapath.Members(f.rules.Resolver, mapping, true) {
		k := reflect.New(t.Key())

		err := f.rules.Decode(f.rules.View(entry.Key), k.Interface())
		if err != nil || !decoded.MapIndex(k.Elem()).IsValid() {
			continue
		}

		if i, ok := index[k.Elem().Interface()]; ok {
			entries[i] = entry

			continue
		}

		index[k.Elem().Interface()] = len(keys)
		keys = append(keys, k.Elem())
		entries = append(entries, entry)
	}

	for i, k := range keys {
		entry := entries[i]

		start, err := f.starts.Of(t.Elem(), entry.Value)
		if err != nil {
			return reflect.Value{}, err
		}

		v, err := f.place(t.Elem(), entry.Value, start, decoded.MapIndex(k), false)
		if err != nil {
			return reflect.Value{}, err
		}

		result.SetMapIndex(k, v)
	}

	return result, nil
}

// elements returns the value of the slice or array type t that a decode
// of seq gives, where decoded is the value the decoder gave. Each element
// starts from the start of its type.
func (f *filler) elements(t reflect.Type, seq *ast.SequenceNode, decoded reflect.Value) (reflect.Value, error) {
	if t.Kind() == reflect.Slice && decoded.IsNil() {
		return decoded, nil
	}

	var result reflect.Value

	if t.Kind() == reflect.Slice {
		result = reflect.MakeSlice(t, decoded.Len(), decoded.Len())
	} else {
		result = reflect.New(t).Elem()
	}

	reflect.Copy(result, decoded)

	for i, element := range seq.Values {
		if i >= decoded.Len() {
			break
		}

		start, err := f.starts.Of(t.Elem(), element)
		if err != nil {
			return reflect.Value{}, err
		}

		v, err := f.place(t.Elem(), element, start, decoded.Index(i), false)
		if err != nil {
			return reflect.Value{}, err
		}

		result.Index(i).Set(v)
	}

	return result, nil
}

// renderNames returns the names that go-yaml gives the fields of t, a
// struct type, including the inline fields. It omits the fields that
// go-yaml skips. The decoder sets a field of a struct that t holds inline
// to zero when the name of that field is also one of these names.
func renderNames(t reflect.Type) map[string]bool {
	names := map[string]bool{}

	for field := range t.Fields() {
		if name, _, skip := yamlfield.Name(field); !skip {
			names[name] = true
		}
	}

	return names
}

// null reports whether the decoder reads held as a null. The argument
// held is the node the document holds for a value. The decoder reads a
// nil node, a null, and an alias to a null as a null. An alias that the
// resolver of the walk cannot follow does not read as a null.
func (f *filler) null(held ast.Node) bool {
	if alias, ok := held.(*ast.AliasNode); ok {
		anchor, err := f.rules.Resolver.Anchor(alias)
		if err != nil {
			return false
		}

		held = astnode.Content(anchor)
	}

	return astnode.IsNil(held) || held.Type() == ast.NullType
}

// valueOr returns v, or the zero value of t when v is not valid.
func valueOr(v reflect.Value, t reflect.Type) reflect.Value {
	if v.IsValid() {
		return v
	}

	return reflect.Zero(t)
}

// isAlias reports whether node, under its anchors and tags, is an alias.
func isAlias(node ast.Node) bool {
	_, ok := astnode.Content(node).(*ast.AliasNode)

	return ok
}
