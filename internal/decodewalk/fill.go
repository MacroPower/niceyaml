package decodewalk

import (
	"bytes"
	"context"
	"reflect"
	"slices"
	"unsafe"

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
//   - Fill can give one value that it builds to two places, as
//     [filler.built] describes. The two places read one node as one type,
//     start from equal values, and hold decoder values with the same
//     bytes. They then share memory only where their decoder values hold
//     the same memory. A place that starts from another value builds a
//     value of its own. The places inside a field held inline that Fill
//     takes from a second decode are the exception. A second decode
//     creates its values anew. Two such places thus take one value when
//     they read one node as one type and start from equal values.
//   - Each element and each map value starts from the start of its type,
//     and so does one the document writes as a null.
//   - A null leaves any other value at its start.
//   - A value at an alias of a reference document, whose anchor the
//     resolver of r does not hold, decodes again from its start when the
//     decoder did not start it there. The decode reads the anchor that
//     [Rules.Anchor] finds. Fill cannot read below that anchor, so each
//     value that the decoder creates inside it keeps the value of the
//     decoder. A null there leaves the value at its start, as a null of
//     the document does. A value at any other alias that the resolver
//     cannot follow keeps the value of the decoder.
//   - A struct field whose entry a `<<` merge key brings in from a
//     reference document keeps the value of the decoder. So does a field
//     whose entry Fill cannot tell, as [Walk] describes. When the
//     decoder did not start the struct from its start, the struct decodes
//     again from its start first, so each such field starts from the value
//     the start holds. Fill takes such fields from the second decode. The
//     fields whose entries the document writes, other than those held
//     inline or inside an inline struct, build from what the decoder first
//     gave them, as they do without the merge key. An unmarshaler inside
//     such a field can run one more time.
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
		memo:   map[memoKey][]memoEntry{},
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
	// The values the fill built for each node, type, and decoder value,
	// with the start of each.
	memo map[memoKey][]memoEntry
	// Whether a value of each type reaches a type that starts from a
	// value other than zero.
	reach map[reflect.Type]bool
	// Whether the fill is inside a field held inline that [filler.fields]
	// takes from a second decode of its struct. The memo then keys each
	// place by its type and its node alone, as [filler.keyOf] describes.
	second bool
}

// memoKey names the values that a [filler] built for one node, read as
// one type. Outside the mark of [filler.second], it also names the value
// that the decoder gave the place, by the bytes that hold that value. The
// bytes name a pointer or a map by its address, and a slice by its
// address, length, and capacity. The bytes of a struct hold every field,
// so a string field has the same address and length in two equal keys.
// Outside the mark, the decoder values of two places with equal keys thus
// hold the same memory wherever they hold memory.
//
//nolint:unused // The fields tell the keys of the memo apart.
type memoKey struct {
	typ  reflect.Type
	node ast.Node
	bits string
	// Whether the key holds no bytes, under the mark of [filler.second].
	// It keeps the entries made under the mark apart from the others.
	second bool
}

// memoEntry is a value that a [filler] built, the start it built the
// value from, and the value that the decoder gave the place. The start
// fields name each field of value that holds what the start held there,
// as [startFields] finds them, by the index path of the field.
type memoEntry struct {
	start, value reflect.Value
	// The value that the decoder gave the place, which the entry holds so
	// that no later value takes the addresses in the bytes of its key.
	decoded reflect.Value //nolint:unused // The field keeps the value alive.
	fields  [][]int
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
	case !ok && readsReference(f.rules.Resolver, held):
		return f.referenced(t, held, start, decoded, exact)

	case !ok && !f.null(held):
		// The decoder read an alias that the document cannot follow.
		return decoded, nil

	case !ok && t.Kind() == reflect.Pointer:
		return reflect.Zero(t), nil

	case !ok:
		return start, nil
	}

	exact = exact && !isAlias(held)
	content := datapath.Deref(f.rules.Resolver, node)
	key := seenKey{typ: t, node: content}
	memo := f.keyOf(t, content, decoded)

	if v, ok := f.built(memo, start); ok {
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

	f.remember(memo, start, decoded, v)

	return v, nil
}

// keyOf returns the key of the memo for a place of type t that reads
// node, where the decoder gave the place decoded. Under the mark of
// [filler.second], the key leaves out decoded.
func (f *filler) keyOf(t reflect.Type, node ast.Node, decoded reflect.Value) memoKey {
	if f.second {
		return memoKey{typ: t, node: node, second: true}
	}

	return memoKey{typ: t, node: node, bits: string(bytesOf(decoded))}
}

// remember records v as the value that the fill built for the type and
// the node of key from start, where the decoder gave the place decoded,
// for [filler.built].
//
// The memory that v holds must be of two kinds, as [startFields] sorts
// it. A start field holds exactly what start held in that field, and a
// hit gives it what the start of the later place holds there. Each other
// pointer, map, slice, or interface value that holds memory must sit
// where decoded holds memory too. When v holds memory of neither kind,
// remember records nothing, and each later place builds a value of its
// own.
//
// The entry keeps decoded, so that no later value takes its address while
// the memo holds the key. Under the mark of [filler.second], remember
// records v with no sort and no start fields.
func (f *filler) remember(key memoKey, start, decoded, v reflect.Value) {
	if !start.CanInterface() {
		return
	}

	// The start might be a field of a value the fill is building, which
	// changes when the field takes v.
	saved := reflect.New(key.typ).Elem()
	saved.Set(start)

	entry := memoEntry{start: saved, value: v}

	if !key.second {
		fields, ok := startFields(v, decoded, saved)
		if !ok {
			return
		}

		entry.decoded, entry.fields = decoded, fields
	}

	f.memo[key] = append(f.memo[key], entry)
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

// built returns the value that the fill built for the type and the node
// of key from a start equal to start, and reports whether it built one.
//
// A hit needs every input of the build to be the same: the type, the
// node, an equal start, and a decoder value with the same bytes. For an
// entry with start fields, built returns a copy of the value of the
// entry. Each start field of the copy holds what start holds there.
// Outside the mark of [filler.second], two places that a hit serves thus
// share memory only where their decoder values hold the same memory. A
// place that misses builds a value of its own.
//
// Where the fill records entries, it builds one value for each mix of
// type, node, start, and decoder bytes. Its work thus grows with the
// values that the decoder created, not with the document as its aliases
// write it out. Under the mark of [filler.second], a hit needs the type,
// the node, and an equal start alone.
func (f *filler) built(key memoKey, start reflect.Value) (reflect.Value, bool) {
	if !start.CanInterface() {
		return reflect.Value{}, false
	}

	for _, m := range f.memo[key] {
		if !reflect.DeepEqual(m.start.Interface(), start.Interface()) {
			continue
		}

		if len(m.fields) == 0 {
			return m.value, true
		}

		v := reflect.New(key.typ).Elem()
		v.Set(m.value)

		for _, path := range m.fields {
			v.FieldByIndex(path).Set(start.FieldByIndex(path))
		}

		return v, true
	}

	return reflect.Value{}, false
}

// startFields sorts the memory of the value that the fill built for a
// place. The argument built is that value, start is the start of the
// place, and decoded is the value that the decoder gave the place. The
// walk goes through the three values together, through struct fields and
// array elements, and it stops at a value of any other kind. A stop fits
// when built holds no memory there, as [holdsMemory] reports it. It also
// fits when it is a start field, or when decoded holds memory there too.
//
// A start field is a field of a struct place that the walk reaches
// through exported struct fields and through no array. Its bytes equal
// those of the same field of start. The result holds the index path of
// each start field, and it reports false when a stop does not fit.
func startFields(built, decoded, start reflect.Value) ([][]int, bool) {
	var fields [][]int

	ok := sortMemory(built, decoded, start, nil, true, &fields)

	return fields, ok
}

// sortMemory is the walk of [startFields] at path, the index path of
// built from the place. With field set, the walk reached built through
// exported struct fields alone. It appends each start field to fields.
func sortMemory(built, decoded, start reflect.Value, path []int, field bool, fields *[][]int) bool {
	switch built.Kind() {
	case reflect.Struct:
		for i := range built.NumField() {
			exported := built.Type().Field(i).IsExported()

			if !sortMemory(
				built.Field(i),
				decoded.Field(i),
				start.Field(i),
				append(path, i),
				field && exported,
				fields,
			) {
				return false
			}
		}

		return true

	case reflect.Array:
		// When the type of the elements cannot hold memory, built holds
		// no memory at any stop inside them, so the walk skips them.
		if !typeHoldsMemory(built.Type().Elem()) {
			return true
		}

		for i := range built.Len() {
			if !sortMemory(built.Index(i), decoded.Index(i), start.Index(i), path, false, fields) {
				return false
			}
		}

		return true

	default:
		if !holdsMemory(built) {
			return true
		}

		if field && len(path) > 0 && bytes.Equal(bytesOf(built), bytesOf(start)) {
			*fields = append(*fields, slices.Clone(path))

			return true
		}

		return holdsMemory(decoded)
	}
}

// holdsMemory reports whether v holds memory. A pointer, a map, a slice, a
// channel, a func, or an unsafe pointer holds memory when it is not nil.
// An interface value holds memory when the value inside it does, and a
// struct or an array holds memory when a field or an element does.
func holdsMemory(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Chan, reflect.Func, reflect.UnsafePointer:
		return !v.IsNil()

	case reflect.Interface:
		return !v.IsNil() && holdsMemory(v.Elem())

	case reflect.Struct:
		for _, field := range v.Fields() {
			if holdsMemory(field) {
				return true
			}
		}

	case reflect.Array:
		if !typeHoldsMemory(v.Type()) {
			return false
		}

		for i := range v.Len() {
			if holdsMemory(v.Index(i)) {
				return true
			}
		}

	default:
	}

	return false
}

// typeHoldsMemory reports whether a value of type t can hold memory, as
// [holdsMemory] reports it. A pointer, a map, a slice, a channel, a func,
// an unsafe pointer, or an interface value can hold memory. A struct can
// when the type of one of its fields can. An array can when it has
// elements and the type of its elements can.
func typeHoldsMemory(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Chan, reflect.Func, reflect.UnsafePointer,
		reflect.Interface:
		return true

	case reflect.Struct:
		for field := range t.Fields() {
			if typeHoldsMemory(field.Type) {
				return true
			}
		}

	case reflect.Array:
		return t.Len() > 0 && typeHoldsMemory(t.Elem())

	default:
	}

	return false
}

// bytesOf returns the Type().Size() bytes that hold v. It reads them in
// place when v is addressable. Otherwise it copies v into a new value of
// its type, and reads the bytes of the copy.
func bytesOf(v reflect.Value) []byte {
	var p unsafe.Pointer

	if v.CanAddr() {
		p = v.Addr().UnsafePointer()
	} else {
		c := reflect.New(v.Type())
		c.Elem().Set(v)

		p = c.UnsafePointer()
	}

	//nolint:gosec // The slice covers the value at p, which holds Size bytes.
	return unsafe.Slice((*byte)(p), v.Type().Size())
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

// referenced returns the value of type t that a decode of held gives from
// start, where held reads an anchor of a reference document that the
// resolver of the fill does not hold. The fill cannot read below such an
// anchor, so the value decodes again from start, as [filler.again]
// describes. A null there leaves the value at start, and sets a pointer
// to nil. A later alias of the anchor can take the value that the fill
// built for an earlier one, as [filler.built] describes. The two aliases
// then read the anchor as one type and start from equal values. Outside
// the mark of [filler.second], they also hold decoder values with the
// same bytes.
func (f *filler) referenced(
	t reflect.Type, held ast.Node, start, decoded reflect.Value, exact bool,
) (reflect.Value, error) {
	// The parser does not take an anchor or a tag on an alias, so held is
	// the alias.
	alias, ok := held.(*ast.AliasNode)
	if !ok || f.rules.Anchor == nil {
		return decoded, nil
	}

	value, ok := f.rules.Anchor(alias)
	if !ok {
		return decoded, nil
	}

	// A null of a reference document leaves the value at its start, as a
	// null of the document does.
	if f.referenceNull(value) {
		if t.Kind() == reflect.Pointer {
			return reflect.Zero(t), nil
		}

		return start, nil
	}

	key := f.keyOf(t, value, decoded)

	if v, ok := f.built(key, start); ok {
		return v, nil
	}

	v, err := f.again(t, held, value, start, decoded, exact)
	if err != nil {
		return reflect.Value{}, err
	}

	f.remember(key, start, decoded, v)

	return v, nil
}

// again returns the value of type t that a decode of value, the value of
// the anchor of a reference document that held reads, gives from start.
// The value decodes again from start unless the decoder started it there
// or start is zero. A pointer gets a target of its own, as
// [filler.againPointer] describes.
func (f *filler) again(
	t reflect.Type, held, value ast.Node, start, decoded reflect.Value, exact bool,
) (reflect.Value, error) {
	if t.Kind() == reflect.Pointer {
		return f.againPointer(t, held, value, start, decoded, exact)
	}

	if exact || start.IsZero() || t.Kind() == reflect.Interface || t == unmarshaler.MapSliceType {
		return decoded, nil
	}

	v := reflect.New(t)
	v.Elem().Set(start)

	err := f.rules.Decode(value, v.Interface())
	if err != nil {
		return reflect.Value{}, err
	}

	return v.Elem(), nil
}

// againPointer is [filler.again] for the pointer type t. The target is
// new, and it starts from the target of start, or from the start of its
// type when start is nil.
func (f *filler) againPointer(
	t reflect.Type, held, value ast.Node, start, decoded reflect.Value, exact bool,
) (reflect.Value, error) {
	if decoded.IsNil() {
		return decoded, nil
	}

	var (
		from reflect.Value
		err  error
	)

	if start.IsNil() {
		from, err = f.starts.Of(t.Elem(), held)
		if err != nil {
			return reflect.Value{}, err
		}
	} else {
		from = start.Elem()
	}

	target, err := f.again(t.Elem(), held, value, from, decoded.Elem(), exact && !start.IsNil())
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

	// The decoder starts each field from the value it holds. A decode from
	// start thus gives a field that a merge key of a reference document
	// brings in the value it starts from there.
	var again reflect.Value

	if merges && !exact && f.readsReferenceField(t, mapping) {
		v := reflect.New(t)
		v.Elem().Set(start)

		err := f.rules.Decode(f.rules.View(mapping), v.Interface())
		if err != nil {
			return reflect.Value{}, err
		}

		again = v.Elem()
	}

	result := reflect.New(t).Elem()
	result.Set(start)

	err := f.fields(t, mapping, merges, result, decoded, again, exact, nil)
	if err != nil {
		return reflect.Value{}, err
	}

	return result, nil
}

// readsReferenceField reports whether t, a struct type, has a field whose
// entry in mapping a `<<` merge key brings in from a reference document,
// as [walker.lookup] finds it. A field whose lookup is not known counts
// too, because such a merge key might bring in its entry.
func (f *filler) readsReferenceField(t reflect.Type, mapping *ast.MappingNode) bool {
	for name := range fieldNames(t, map[reflect.Type]bool{}) {
		if read := f.lookup(mapping, name, true); read.alias != nil || !read.known {
			return true
		}
	}

	return false
}

// fields sets each field of result that a decode of mapping writes. The
// argument result is a struct of type t that holds its start. The
// argument decoded is the value that the decoder gave the struct, and
// exact reports whether the decoder started it from start.
//
// The argument again holds the value of a decode of the struct from start
// for a `<<` merge key of a reference document. It is not valid when the
// struct did not decode again. A named field whose entry the document
// writes builds from decoded, as it does in a struct without such a merge
// key. Each other field that the decode writes builds from again when
// again is valid, as from a value that the decoder started from start.
//
// A struct held inline skips each field that hidden names, because the
// struct that holds it names that field too.
func (f *filler) fields(
	t reflect.Type,
	mapping *ast.MappingNode,
	merges bool,
	result, decoded, again reflect.Value,
	exact bool,
	hidden map[string]bool,
) error {
	whole, wholeExact := decoded, exact
	if again.IsValid() {
		whole, wholeExact = again, true
	}

	for i := range t.NumField() {
		field := t.Field(i)

		name, inline, skip := yamlfield.Name(field)
		if skip || !result.Field(i).CanSet() {
			continue
		}

		if yamlfield.ReadsAnchor(field) {
			result.Field(i).Set(whole.Field(i))

			continue
		}

		var (
			v   reflect.Value
			err error
		)

		switch {
		case inline:
			// Each value of a second decode is new, so the places inside
			// such a field take the key of the mark.
			second := f.second
			f.second = second || again.IsValid()
			v, err = f.inline(field.Type, t, mapping, merges, result.Field(i), whole.Field(i), wholeExact)
			f.second = second

		case hidden[name]:
			continue

		default:
			read := f.lookup(mapping, name, merges)

			switch {
			// The fill cannot read below the entry of a reference document
			// that the decoder read, nor tell which entry it read.
			case read.alias != nil || !read.known:
				v = whole.Field(i)

			case read.entry == nil:
				continue

			default:
				v, err = f.place(field.Type, read.entry.Value, result.Field(i), decoded.Field(i), exact)
			}
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

		err := f.fields(t, mapping, merges, result, decoded, reflect.Value{}, exact, renderNames(owner))
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
