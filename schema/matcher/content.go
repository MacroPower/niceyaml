package matcher

import (
	"context"
	"encoding"
	"errors"
	"maps"
	"math"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/aliasing"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/docstate"
	"go.jacobcolvin.com/niceyaml/internal/yamlfield"
	"go.jacobcolvin.com/niceyaml/paths"
)

// contentMatcher matches documents by a single YAML content value.
type contentMatcher[T comparable] struct {
	want T
	path paths.Path
}

// Content creates a new [Matcher] that matches documents whose value at
// path decodes to want.
//
// Match decodes the value at path, from the scope of the document, with
// [niceyaml.Node.Decode] as a T and compares the result to want, so
// the type of want decides how Match reads the YAML. A string matches
// the text of a scalar as the document spells it, so "1.10" matches
// version: 1.10 and "1.1" does not. A string type that decodes itself,
// through an UnmarshalYAML or UnmarshalText method, matches the value its
// own decode gives. A number matches a scalar the document writes as a
// number, whatever its spelling, though an integer want never matches a
// value with a fraction. An infinite want matches only an infinity, so a
// float32 want of +Inf does not match 1e39, which a float32 cannot hold.
// A NaN want matches .nan in any of its
// spellings, though NaN never equals itself in Go. A quoted, block, or
// !!str scalar is a string, so version: "2" does not match 2. A plain
// scalar that YAML reads as a string, such as inf or 0x1p-2, matches no
// number either, though Go parses it as one. A
// [time.Duration] reads from the text of a scalar, so timeout: "5s" and
// timeout: 5s both match 5*time.Second. A [time.Time] matches a timestamp
// that names the same instant, whatever its offset, so
// 2001-12-14T21:59:43-05:00 matches the same moment in UTC. The decoder
// reads text that is no timestamp, such as created: hello, as the zero
// time, and a zero [time.Time] does not match such text. A tagged
// alias, as in !!timestamp *d, is the exception when its anchor holds
// a tagged alias in turn. Match does not follow that second alias, so
// it compares the time go-yaml decodes. A pointer want matches the
// value it points to. A null matches only a nil want, such as
// Content[any](path, nil) or a nil pointer. When T is an
// interface, two numbers compare by value whatever their Go types, so
// Content[any](path, 1) matches an integer the decoder reads as a
// uint64. An array or struct want, other than a [time.Time], matches
// when each of its elements or fields does by these rules, so
// [1]int{2} does not match version: [2.5]. Three kinds of want are
// the exception and match when they equal the value go-yaml decodes.
// They are a type that decodes itself, a [yaml.MapItem], and a
// struct with an inline field tagged with a bare alias option, which
// go-yaml fills from an anchor in place of the mapping. Match takes
// a type to decode itself by its methods alone. It cannot see a type
// that go-yaml decodes whole through a [yaml.CustomUnmarshaler]
// option, a [yaml.RegisterCustomUnmarshaler] call, or an
// UnmarshalJSON method under [yaml.UseJSONUnmarshaler], so it holds
// a value of such a type to the rules of its kind. For an array of
// such a type read from anything but a sequence, or a struct read
// from anything but a mapping, want matches when it equals the value
// go-yaml decodes. A field or a trailing element the document leaves
// out decodes to zero, so it matches only where want holds zero. So
// does every field of a mapping with a key the decoder reads as
// something other than a string, such as 7 or true, since the
// decoder then sets no field from that mapping. A mapping that a
// `<<` merge key brings in sets no field when it holds such a key.
// Match compares an element or a field as go-yaml decodes it,
// without these rules, when no node at its path holds the value the
// decoder reads. That happens in three cases. The value may sit in a
// reference document, behind an alias in its place or one under a `<<`
// merge key. A key may read as the name of a field while no path can
// name it. A tag makes !!str 0x10 read as 16, while a path spells that
// key 0x10, and an alias such as *k reads as the text of its anchor in
// a reference document. A field may have its own entry before a `<<`
// merge key that brings in the same key from a mapping that sets no
// field, and the path then leads to the merged entry. In all three, an
// element or a field that reads a null matches only a nil want, though
// a null deeper inside such a value compares as go-yaml decodes it. A
// document without the path, or whose value does not decode into T,
// does not match, so a [time.Duration] T matches no timeout: 5.5,
// timeout: 1e3, or timeout: true. A string that [time.ParseDuration]
// rejects, such as timeout: 5 minutes, is an exception, and so is a T
// whose definition the decoder refuses, such as a struct with two fields
// of one name. The decoder reports these without
// [niceyaml.ErrDecodeRejected], as it does the other errors
// [niceyaml.Node.DecodeInto] names, so Match returns the error. Any other
// error from the read comes back as the error, so a registry stops at the
// document rather than routing it elsewhere. Such errors include a path
// with a wildcard selector and a context that ended. They also include an
// alias on the path, tagged or not, that names no anchor before it in the
// document, even when a reference document holds an anchor of that name.
// Match also refuses a document whose aliases would make the read cost
// far more than the document holds. It refuses such a document before it
// decodes anything, as the schema validator does, with an error matching
// [go.jacobcolvin.com/niceyaml/schema.ErrExcessiveAliasing]:
//
//	// Matches kind: Deployment.
//	matcher.Content(paths.Root().Child("kind"), "Deployment")
//
//	// Matches version: 1.0 and version: 1.
//	matcher.Content(paths.Root().Child("version"), 1.0)
//
// For several conditions, use [All] (AND) or [Any] (OR):
//
//	// Matches kind: Deployment AND apiVersion: apps/v1.
//	matcher.All(
//	    matcher.Content(paths.Root().Child("kind"), "Deployment"),
//	    matcher.Content(paths.Root().Child("apiVersion"), "apps/v1"),
//	)
func Content[T comparable](path paths.Path, want T) Matcher {
	return &contentMatcher[T]{path: path, want: want}
}

// Match implements [Matcher].
func (m *contentMatcher[T]) Match(ctx context.Context, doc *niceyaml.Node) (bool, error) {
	err := ctx.Err()
	if err != nil {
		//nolint:wrapcheck // The error of the context is the reason the matcher cannot decide.
		return false, err
	}

	node, err := doc.At(m.path)
	if errors.Is(err, paths.ErrNotFound) {
		return false, nil
	}

	if err != nil {
		//nolint:wrapcheck // The Document binds the error already.
		return false, err
	}

	// A few hundred bytes of nested aliases can make a decode take
	// minutes, so a node that holds an alias decodes only when its whole
	// document passes the alias limit of the schema validator.
	err = aliasing.CheckDecode(node)
	if err != nil {
		//nolint:wrapcheck // Binding names the document; the error keeps its own context.
		return false, doc.Bind(err)
	}

	// A decode into a type that decodes itself from text writes the node
	// out with a copy of the text of each alias, which the count above
	// leaves out. A struct field or an element of such a type gets the
	// same treatment, as does a type whose UnmarshalYAML takes a decode
	// function, which may decode the node into such a type.
	if aliasing.DecodesText(reflect.TypeFor[T]()) {
		err = aliasing.CheckDecodeText(node)
		if err != nil {
			//nolint:wrapcheck // Binding names the document; the error keeps its own context.
			return false, doc.Bind(err)
		}
	}

	// Read the value as the YAML types name it first, because a decode
	// into T loses what tells a null from an empty string or a false,
	// and a fraction from the integer it truncates to. A decode into any
	// yields only the YAML built-in types, none of which validates itself,
	// so the self-validation walk would find nothing.
	raw, err := node.Decode[any](ctx, niceyaml.WithSelfValidation(false))
	if errors.Is(err, niceyaml.ErrDecodeRejected) {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	if raw == nil {
		return wantsNil(reflect.ValueOf(&m.want).Elem()), nil
	}

	// The decoder reads some plain floats, such as 1e3, as strings and
	// hands them to time.ParseDuration for a time.Duration, which returns
	// its own error. A number never decodes into a time.Duration, so such
	// a float does not match, as 1.5e3 does not. The same holds for an
	// element or a field of T that matchValue checks on its own.
	floats, err := readsDurationFloat(ctx, node, raw, reflect.TypeFor[T]())
	if floats || err != nil {
		return false, err
	}

	var got T

	// When T is any, a decode into T gives the value raw already holds, so
	// got takes raw rather than decoding the node again.
	if p, ok := any(&got).(*any); ok {
		*p = raw
	} else {
		// A rejection from the decoder means the value does not read as T,
		// which is a no rather than a failure. An error the value's own
		// UnmarshalYAML returns is not a rejection, so it comes back as the
		// error.
		got, err = node.Decode[T](ctx)
		if errors.Is(err, niceyaml.ErrDecodeRejected) {
			return false, nil
		}

		if err != nil {
			return false, err
		}
	}

	return matchValue(ctx, node, raw, reflect.ValueOf(&got).Elem(), reflect.ValueOf(&m.want).Elem(), nil)
}

// matchValue reports whether got, the value that node decoded to,
// matches want, where raw holds the value of node as the YAML types name
// it. An array or a struct that [readsEntries] reports matches when each
// of its elements or fields does, so the rules for a scalar hold inside
// it too. Any other value matches whole, as [matchScalar] compares it.
// When got is an inline struct, parent describes the struct it sits in.
func matchValue(
	ctx context.Context, node *niceyaml.Node, raw any, got, want reflect.Value, parent *inlineScope,
) (bool, error) {
	if raw == nil {
		return wantsNil(want), nil
	}

	// A pointer want matches the value it points to, since every decode
	// allocates a fresh pointer. A nil pointer want matched null above,
	// so it matches nothing here.
	got, want, ok := pointees(got, want)
	if !ok {
		return false, nil
	}

	if !readsEntries(got.Type()) {
		return matchScalar(node, raw, got, want), nil
	}

	if got.Kind() == reflect.Array {
		return matchArray(ctx, node, raw, got, want)
	}

	return matchStruct(ctx, node, raw, got, want, parent)
}

// readsEntries reports whether t is an array that the decoder fills from
// the items of a sequence, or a struct that it fills from the entries of
// a mapping. The decoder does so for an array or a struct that it reads
// by its kind, as [isPlain] reports. A struct with a field that
// [yamlfield.ReadsAnchor] reports is the exception. The decoder fills
// that field from an anchor, and when the field also carries omitempty
// it reads no `<<` merge key into any other field of the struct.
func readsEntries(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Array:
		return isPlain(t)
	case reflect.Struct:
		return isPlain(t) && !readsAnchor(t)
	default:
		return false
	}
}

// readsAnchor reports whether t, a struct type, has a field that
// [yamlfield.ReadsAnchor] reports.
func readsAnchor(t reflect.Type) bool {
	for field := range t.Fields() {
		if yamlfield.ReadsAnchor(field) {
			return true
		}
	}

	return false
}

// matchArray reports whether got, the array that node decoded to,
// matches want element by element, where raw holds the value of node as
// the YAML types name it. An element that reads a null matches only a
// nil want, even when no node at its path holds the value the decoder
// reads. The decoder leaves zero an element past the end of the
// sequence, which then matches as == would. An array the decoder read
// from anything but a sequence matches whole, as [matchScalar] compares
// it.
func matchArray(ctx context.Context, node *niceyaml.Node, raw any, got, want reflect.Value) (bool, error) {
	// Only an unmarshaler registered for the array type, which isPlain
	// cannot see, reads an array from anything but a sequence.
	elems, ok := raw.([]any)
	if !ok {
		return matchScalar(node, raw, got, want), nil
	}

	for i := range got.Len() {
		var (
			match bool
			err   error
		)

		switch {
		case i >= len(elems):
			match = equal(got.Index(i), want.Index(i))
		case elems[i] == nil:
			match = wantsNil(want.Index(i))
		default:
			match, err = matchEntry(ctx, node, paths.Root().Index(i), elems[i], nil, got.Index(i), want.Index(i))
		}

		if err != nil || !match {
			return false, err
		}
	}

	return true, nil
}

// inlineScope describes the struct an inline struct sits in, whose
// mapping the fields of the inline struct read.
type inlineScope struct {
	// The probe of each name the decoder sets a field under from the
	// mapping, as [decodedFields] returns them.
	decoded map[string]*fieldProbe

	// The names of the fields of the parent that are not inline. The
	// decoder leaves zero each field of the inline struct that uses one.
	shadowed map[string]bool
}

// matchStruct reports whether got, the struct that node decoded to,
// matches want field by field, where raw holds the value of node as the
// YAML types name it. Each field reads the entry of the mapping that
// [yamlfield.Name] names, and the fields of an inline struct read the
// mapping of parent, the struct the inline struct sits in. A field the
// decoder reads a null into matches only a nil want, even when no node
// at its path holds the value the decoder reads. A field the decoder
// does not set, or one raw holds no entry for, matches as == would. The
// decoder never sets an unexported field, a field of an inline struct
// that a field of parent shadows, or a field whose name [decodedFields]
// leaves out. A struct the decoder read from anything but a mapping
// matches whole, as [matchScalar] compares it.
func matchStruct(
	ctx context.Context, node *niceyaml.Node, raw any, got, want reflect.Value, parent *inlineScope,
) (bool, error) {
	// Only an unmarshaler registered for the struct type, which isPlain
	// cannot see, reads a struct from anything but a mapping.
	entries, ok := mappingEntries(raw)
	if !ok {
		return matchScalar(node, raw, got, want), nil
	}

	t := got.Type()

	// A rejection means the mapping does not read as t, which is a no, as
	// it is for the decode into T.
	decoded, shadowed, err := fieldScope(ctx, node, t, parent)
	if errors.Is(err, niceyaml.ErrDecodeRejected) {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	for i := range t.NumField() {
		field := t.Field(i)
		name, inline, skip := yamlfield.Name(field)
		probe, set := decoded[name]
		value, found := entries[name]

		var (
			match bool
			err   error
		)

		switch {
		case skip || !field.IsExported() || shadowed[name]:
			match = equal(got.Field(i), want.Field(i))
		case inline:
			inner := &inlineScope{decoded: decoded, shadowed: yamlfield.OwnNames(t)}
			match, err = matchValue(ctx, node, raw, got.Field(i), want.Field(i), inner)

		case set && probe == nil:
			match = wantsNil(want.Field(i))
		case !set || !found:
			match = equal(got.Field(i), want.Field(i))

		// The decoder hands the probe an alias under a tag or an anchor,
		// so the probe is not nil when that alias reads as a null. The
		// default case tells such a null under a tag by the node at its
		// path. This case covers an alias that no path resolves, where
		// the default case finds no node. A null read through an alias
		// under an anchor that a path resolves compares as go-yaml
		// decodes it.
		case value == nil && probe.readsUnresolved(node, name):
			match = wantsNil(want.Field(i))
		default:
			path := paths.Root().Child(name)
			match, err = matchEntry(ctx, node, path, value, probe.reads, got.Field(i), want.Field(i))
		}

		if err != nil || !match {
			return false, err
		}
	}

	return true, nil
}

// fieldScope returns the probe of each name the decoder sets a field of
// t, a struct type, under when it decodes the mapping at node, as
// [decodedFields] returns them, and the names whose fields it leaves
// zero. When t is an inline struct, parent describes the struct it sits
// in, and fieldScope returns the names that parent holds.
func fieldScope(
	ctx context.Context, node *niceyaml.Node, t reflect.Type, parent *inlineScope,
) (map[string]*fieldProbe, map[string]bool, error) {
	if parent != nil {
		return parent.decoded, parent.shadowed, nil
	}

	decoded, err := decodedFields(ctx, node, t)

	return decoded, nil, err
}

// mappingEntries returns the entries of raw, a mapping as the YAML types
// name it, by key, and reports whether raw is a mapping. A decode with
// [yaml.UseOrderedMap] yields a [yaml.MapSlice] in place of a map, with
// each key the decoder reads written as a string. A later item with the
// key of an earlier one wins, as it does in the map.
func mappingEntries(raw any) (map[string]any, bool) {
	switch m := raw.(type) {
	case map[string]any:
		return m, true
	case yaml.MapSlice:
		entries := make(map[string]any, len(m))

		for _, item := range m {
			key, ok := item.Key.(string)
			if ok {
				entries[key] = item.Value
			}
		}

		return entries, true

	default:
		return nil, false
	}
}

// decodedFields returns a [*fieldProbe] for each name the decoder sets a
// field under when it decodes the mapping at node into t, a struct type.
// The probe records the node the decoder reads the field from, and is
// nil for a null. The fields of an inline struct of t read the same
// names. The decoder sets no field from a mapping with a key it reads as
// something other than a string, such as 7, true, or null. It sets none
// from a mapping that a `<<` merge key brings in and that holds such a
// key either. A tag or an alias can change what a key reads as, so
// decodedFields asks the decoder. It decodes node into a struct that
// holds a probe under each name of t, then returns the probes the decode
// set.
func decodedFields(ctx context.Context, node *niceyaml.Node, t reflect.Type) (map[string]*fieldProbe, error) {
	names := map[string]bool{}
	addFieldNames(t, names, map[reflect.Type]bool{})

	sorted := slices.Sorted(maps.Keys(names))
	decoded := make(map[string]*fieldProbe, len(sorted))

	if len(sorted) == 0 {
		return decoded, nil
	}

	fields := make([]reflect.StructField, len(sorted))

	for i, name := range sorted {
		// The comma after the name keeps a field named - from reading as
		// the tag that skips a field.
		fields[i] = reflect.StructField{
			Name: "F" + strconv.Itoa(i),
			Type: reflect.TypeFor[*fieldProbe](),
			Tag:  reflect.StructTag("yaml:" + strconv.Quote(name+",")),
		}
	}

	probe := reflect.New(reflect.StructOf(fields))
	for i := range sorted {
		probe.Elem().Field(i).Set(reflect.ValueOf(&fieldProbe{}))
	}

	err := node.DecodeInto(ctx, probe.Interface(), niceyaml.WithSelfValidation(false))
	if err != nil {
		//nolint:wrapcheck // The Node binds the error already.
		return nil, err
	}

	for i, name := range sorted {
		// The decoder sets a probe pointer to nil for a null.
		p, _ := reflect.TypeAssert[*fieldProbe](probe.Elem().Field(i))
		if p == nil || p.set {
			decoded[name] = p
		}
	}

	return decoded, nil
}

// addFieldNames adds to names the name go-yaml decodes each field of t, a
// struct type, under, leaving out the fields it skips. An inline struct
// adds the names of its own fields, since they read the mapping of t. The
// seen set guards against a struct that inlines itself.
func addFieldNames(t reflect.Type, names map[string]bool, seen map[reflect.Type]bool) {
	if seen[t] {
		return
	}

	seen[t] = true

	for field := range t.Fields() {
		name, inline, skip := yamlfield.Name(field)

		switch {
		case skip:
		case !inline:
			names[name] = true
		case field.Type.Kind() == reflect.Struct:
			addFieldNames(field.Type, names, seen)
		case field.Type.Kind() == reflect.Pointer && field.Type.Elem().Kind() == reflect.Struct:
			addFieldNames(field.Type.Elem(), names, seen)
		default:
		}
	}
}

// fieldProbe records that the decoder set a field of the struct
// [decodedFields] decodes, and where the node it read the field from
// starts, without reading the value of the field.
type fieldProbe struct {
	// The position of the token that starts the content of the node, as
	// [contentStart] finds it.
	start *token.Position
	set   bool
}

// UnmarshalYAML implements [yaml.NodeUnmarshaler].
func (p *fieldProbe) UnmarshalYAML(node ast.Node) error {
	p.set = true
	p.start = contentStart(node)

	return nil
}

// reads reports whether the decoder read the field from child. The
// decoder reads a tree of its own, whose nodes start where the nodes of
// the document do, so reads compares where the two start. A nil probe
// stands for a null, which the decoder reads without handing it to the
// probe, so it reads child when child holds a null.
func (p *fieldProbe) reads(child *niceyaml.Node) bool {
	if p == nil {
		_, ok := astnode.Content(child.AST()).(*ast.NullNode)

		return ok
	}

	return p.startsAt(child.AST())
}

// readsUnresolved reports whether the decoder read the field from the
// value of the entry that name selects in the mapping at node, where
// that value leads to an alias no path resolves. Such an alias names no
// anchor before it in the document, as one to an anchor of a reference
// document does, or leads back to itself. The probe starts at it only
// when a tag or an anchor sits on it. The decoder follows an alias with
// neither on it to its anchor and hands the probe the content of that
// anchor, so readsUnresolved compares the probe with that anchor when
// the value is an alias with neither.
func (p *fieldProbe) readsUnresolved(node *niceyaml.Node, name string) bool {
	if p == nil {
		return false
	}

	resolver := docstate.Of(node).Resolver()

	entry, err := resolver.Entry(node.AST(), name)
	if err != nil {
		return false
	}

	mv, ok := entry.(*ast.MappingValueNode)
	if !ok {
		return false
	}

	_, err = resolver.Deref(mv.Value)
	if !errors.Is(err, paths.ErrAlias) {
		return false
	}

	value := mv.Value

	if alias, ok := value.(*ast.AliasNode); ok {
		value, err = resolver.Anchor(alias)
		if err != nil {
			return false
		}
	}

	return p.startsAt(value)
}

// startsAt reports whether the node the decoder read the field from
// starts where the content of node does.
func (p *fieldProbe) startsAt(node ast.Node) bool {
	start := contentStart(node)
	if p.start == nil || start == nil {
		return false
	}

	return p.start.Line == start.Line && p.start.Column == start.Column && p.start.Offset == start.Offset
}

// contentStart returns the position of the token that starts the
// content of node, looking through an anchor or a tag on it. It returns
// nil when node has no such token.
func contentStart(node ast.Node) *token.Position {
	content := astnode.Content(node)
	if content == nil {
		return nil
	}

	tk := content.GetToken()
	if tk == nil {
		return nil
	}

	return tk.Position
}

// matchEntry reports whether got, the element or field the decoder read
// below node, matches want. The value raw holds is that of the node at
// path below node as the YAML types name it, and reads is the function
// [entryNode] takes. It checks got against the node that [entryNode]
// returns, and got matches as == would when the document holds no such
// node.
func matchEntry(
	ctx context.Context,
	node *niceyaml.Node,
	path paths.Path,
	raw any,
	reads func(child *niceyaml.Node) bool,
	got, want reflect.Value,
) (bool, error) {
	child, ok, err := entryNode(node, path, reads)
	if err != nil {
		return false, err
	}

	if !ok {
		return equal(got, want), nil
	}

	return matchValue(ctx, child, raw, got, want, nil)
}

// entryNode returns the node at path below node that the decoder reads
// an element or a field from, and reports whether the document holds
// that node. For a field, reads reports whether the decoder read the
// field from the node at path. It is nil for an element, which the
// decoder always reads from the node at its path.
//
// A field may have a value but no node at path, such as one named 16 for
// the key !!str 0x10, or one named i for the key *k when the anchor &k i
// sits in a reference document. An alias at path may refer to an anchor
// of a reference document, which the decoder reads and the document does
// not hold. A field may have its own entry before a `<<` merge key that
// brings in the same key from a mapping the decoder drops. The decoder
// then reads the entry, while the node at path is the merged one.
func entryNode(
	node *niceyaml.Node, path paths.Path, reads func(child *niceyaml.Node) bool,
) (*niceyaml.Node, bool, error) {
	child, err := node.At(path)
	if errors.Is(err, paths.ErrNotFound) || errors.Is(err, paths.ErrAlias) {
		return nil, false, nil
	}

	if err != nil {
		//nolint:wrapcheck // The Node binds the error already.
		return nil, false, err
	}

	if reads != nil && !reads(child) {
		return nil, false, nil
	}

	return child, true, nil
}

// readsDurationFloat reports whether a decode of node into t reads a
// float into a [time.Duration], where raw holds the value of node as the
// YAML types name it. A float is a scalar that [isFloatScalar] reports.
// The decoder follows each pointer of t, so readsDurationFloat does too.
// For an array or a struct that [readsEntries] reports, it checks each
// element and field from the node that [entryNode] returns for it, as
// [matchValue] does. It checks every element of a sequence, even one
// past the end of the array, since the decoder reads each of them.
func readsDurationFloat(ctx context.Context, node *niceyaml.Node, raw any, t reflect.Type) (bool, error) {
	t = pointerBase(t)

	if t == reflect.TypeFor[time.Duration]() {
		return isFloatScalar(node, raw), nil
	}

	if !readsEntries(t) {
		return false, nil
	}

	switch t.Kind() {
	case reflect.Array:
		elems, ok := raw.([]any)
		if !ok {
			return false, nil
		}

		for i, elem := range elems {
			floats, err := entryReadsDurationFloat(ctx, node, paths.Root().Index(i), elem, nil, t.Elem())
			if floats || err != nil {
				return floats, err
			}
		}

		return false, nil

	case reflect.Struct:
		return structReadsDurationFloat(ctx, node, raw, t, nil, map[reflect.Type]bool{})
	default:
		return false, nil
	}
}

// structReadsDurationFloat reports whether a decode of the mapping at
// node into t, a struct type, reads a float into a [time.Duration], as
// [readsDurationFloat] describes. It checks the fields that [matchStruct]
// checks, from the same nodes. When t is an inline struct, parent
// describes the struct it sits in. The inlined set holds t and the
// structs it sits in, so a struct that inlines itself through a pointer
// ends the walk rather than repeating it.
func structReadsDurationFloat(
	ctx context.Context,
	node *niceyaml.Node,
	raw any,
	t reflect.Type,
	parent *inlineScope,
	inlined map[reflect.Type]bool,
) (bool, error) {
	entries, ok := mappingEntries(raw)
	if !ok || inlined[t] {
		return false, nil
	}

	inlined[t] = true
	defer delete(inlined, t)

	// A rejection means the mapping does not read as t, which the decode
	// into T reports as well.
	decoded, shadowed, err := fieldScope(ctx, node, t, parent)
	if errors.Is(err, niceyaml.ErrDecodeRejected) {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	for field := range t.Fields() {
		name, inline, skip := yamlfield.Name(field)
		probe, set := decoded[name]
		value, found := entries[name]
		inner := pointerBase(field.Type)

		var floats bool

		// Only an inline struct that readsEntries reports reads its fields
		// from the mapping at node, so an inline field of any other type
		// reads no float.
		switch {
		case skip || !field.IsExported() || shadowed[name]:
		case inline && inner.Kind() == reflect.Struct && readsEntries(inner):
			scope := &inlineScope{decoded: decoded, shadowed: yamlfield.OwnNames(t)}
			floats, err = structReadsDurationFloat(ctx, node, raw, inner, scope, inlined)

		case inline || !set || !found:
		default:
			path := paths.Root().Child(name)
			floats, err = entryReadsDurationFloat(ctx, node, path, value, probe.reads, field.Type)
		}

		if floats || err != nil {
			return floats, err
		}
	}

	return false, nil
}

// entryReadsDurationFloat reports whether a decode of the element or
// field at path below node into t reads a float into a [time.Duration],
// as [readsDurationFloat] describes. The value raw holds is that of the
// node at path as the YAML types name it, and reads is the function
// [entryNode] takes. It reports false when the document holds no node
// that the decoder reads the entry from.
func entryReadsDurationFloat(
	ctx context.Context,
	node *niceyaml.Node,
	path paths.Path,
	raw any,
	reads func(child *niceyaml.Node) bool,
	t reflect.Type,
) (bool, error) {
	child, ok, err := entryNode(node, path, reads)
	if err != nil || !ok {
		return false, err
	}

	return readsDurationFloat(ctx, child, raw, t)
}

// equal reports whether got and want hold the same value, as == would.
// It reports false, rather than panicking, for a value that == cannot
// compare, such as a map behind an interface.
func equal(got, want reflect.Value) bool {
	return got.Comparable() && want.Comparable() && got.Equal(want)
}

// matchScalar reports whether got, the value that node decoded to,
// matches want, where raw holds the value of node as the YAML types name
// it. It compares got whole, so [matchValue] hands it no array or struct
// that the decoder filled element by element or field by field.
func matchScalar(node *niceyaml.Node, raw any, got, want reflect.Value) bool {
	// The decoder respells a number or a bool it reads into a string, so
	// 1.10 becomes "1.1", 0x10 becomes "16", and True becomes "true". A
	// string want matches the scalar's text as written instead.
	if isPlainString(got.Type()) {
		if text, ok := scalarText(node); ok {
			got.SetString(text)
		}
	}

	// The decoder converts a string that Go parses as a number into a
	// number type, so "2", inf, and 0x1p-2 would match numbers. A scalar
	// that YAML reads as a string does not match a number want.
	if isNumber(got) && isPlain(got.Type()) && isNonNumberString(node, raw) {
		return false
	}

	if isInteger(got.Kind()) && !floatHoldsInteger(raw, got) {
		return false
	}

	if got.Kind() == reflect.Float32 && !floatHoldsFloat32(raw, got) {
		return false
	}

	// T may be an interface such as any, whose dynamic type decides whether
	// == applies. A value that == cannot compare, such as a map,
	// matches nothing rather than panicking. Two numbers behind an
	// interface compare by value, since the decoder picks the Go type of a
	// number and the caller picks the type of want.
	if !got.Comparable() {
		return false
	}

	if got.Kind() == reflect.Interface {
		g := got.Interface()

		// The decoder reads some plain floats, such as 1e3, as strings, so
		// a number want reads the float from the text, as a float T does.
		if isNumber(want.Elem()) && !isExplicitString(node, raw) {
			if f, ok := rawFloat(raw); ok {
				g = f
			}
		}

		if eq, ok := numericEqual(g, want.Interface()); ok {
			return eq
		}
	}

	if isFloat(got.Kind()) {
		return floatEqual(got.Float(), want.Float())
	}

	// == on a time.Time also compares its *time.Location, and the decoder
	// builds a fresh one for each offset it parses, so times compare by
	// instant. The decoder also reads text that is no timestamp as the
	// zero time, so a zero time matches only when node holds a timestamp.
	// Any other time comes from text the decoder parsed, and it matches
	// by its instant alone.
	if gt, ok := reflect.TypeAssert[time.Time](got); ok {
		if wt, ok := reflect.TypeAssert[time.Time](want); ok {
			return gt.Equal(wt) && (!gt.IsZero() || isTimestamp(node, raw))
		}
	}

	return got.Interface() == want.Interface()
}

// pointees returns the values that got, the decoded value, and want
// point to when both are pointers, and returns them unchanged otherwise.
// When T is an interface, the decoder never yields a pointer, so pointees
// follows a pointer that want holds and returns its value in an interface.
// It follows one pointer only. The third result is false when a pointer it
// would follow is nil.
func pointees(got, want reflect.Value) (reflect.Value, reflect.Value, bool) {
	if got.Kind() == reflect.Interface && want.Elem().Kind() == reflect.Pointer {
		ptr := want.Elem()
		if ptr.IsNil() {
			return got, want, false
		}

		pointee := ptr.Elem().Interface()

		return got, reflect.ValueOf(&pointee).Elem(), true
	}

	if got.Kind() != reflect.Pointer {
		return got, want, true
	}

	if got.IsNil() || want.IsNil() {
		return got, want, false
	}

	return got.Elem(), want.Elem(), true
}

var (
	// The interfaces go-yaml decodes a value through when its pointer
	// implements one. The decoder reads an UnmarshalJSON method only under
	// [yaml.UseJSONUnmarshaler], which a match never sets.
	unmarshalerTypes = []reflect.Type{
		reflect.TypeFor[yaml.BytesUnmarshaler](),
		reflect.TypeFor[yaml.BytesUnmarshalerContext](),
		reflect.TypeFor[yaml.InterfaceUnmarshaler](),
		reflect.TypeFor[yaml.InterfaceUnmarshalerContext](),
		reflect.TypeFor[yaml.NodeUnmarshaler](),
		reflect.TypeFor[yaml.NodeUnmarshalerContext](),
		reflect.TypeFor[encoding.TextUnmarshaler](),
	}

	// The types go-yaml decodes by rules of its own, as it decodes a type
	// with an unmarshaler. It parses a [time.Duration] and a [time.Time]
	// from the text of a scalar, and it reads the first entry of a
	// mapping into the key and the value of a [yaml.MapItem]. A type
	// defined on one of them gets no such rule.
	decoderTypes = []reflect.Type{
		reflect.TypeFor[time.Duration](),
		reflect.TypeFor[time.Time](),
		reflect.TypeFor[yaml.MapItem](),
	}

	// The layouts go-yaml parses a [time.Time] from, as its unexported
	// allowedTimestampFormats lists them. The decoder yields the zero
	// time, and reports nothing, for text that fits none of them.
	timestampLayouts = []string{
		"2006-1-2T15:4:5.999999999Z07:00",
		"2006-1-2t15:4:5.999999999Z07:00",
		"2006-1-2 15:4:5.999999999",
		"2006-1-2",
	}

	// The pattern of the decimal and exponent spellings of a float in the
	// YAML 1.2 core schema. It leaves out the infinity and NaN spellings.
	// The decoder reads most of them as floats, and it cannot convert the
	// rest, such as +.inf, into a number type.
	floatSyntax = regexp.MustCompile(`^[-+]?(\.\d+|\d+(\.\d*)?)([eE][-+]?\d+)?$`)
)

// pointerBase returns the type that t points to through any number of
// pointers, or t itself when it is no pointer.
func pointerBase(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	return t
}

// isFloatScalar reports whether raw, the value of node as the YAML types
// name it, holds a float or a string in the YAML float syntax. The syntax
// alone decides, so 1e999, which overflows a float64, counts. A scalar
// the document writes as a string does not count, so "1e3" does not.
func isFloatScalar(node *niceyaml.Node, raw any) bool {
	if isExplicitString(node, raw) {
		return false
	}

	switch v := raw.(type) {
	case float64:
		return true
	case string:
		return floatSyntax.MatchString(v)
	default:
		return false
	}
}

// isPlainString reports whether t is a string type that [isPlain]
// reports, so the decoder reads it as it reads a string.
func isPlainString(t reflect.Type) bool {
	return t.Kind() == reflect.String && isPlain(t)
}

// isPlain reports whether the decoder reads t by its kind, which holds
// when t is none of the decoderTypes and its pointer implements no
// unmarshaler the decoder honors. Methods that play no part in
// decoding, such as a String method, leave a type plain.
func isPlain(t reflect.Type) bool {
	return !slices.Contains(decoderTypes, t) &&
		!slices.ContainsFunc(unmarshalerTypes, reflect.PointerTo(t).Implements)
}

// scalarText returns the text of the scalar node holds as the document
// spells it, for a scalar the decoder respells: an integer, a float, an
// infinity, a NaN, or a bool. It reaches the scalar as [valueNode] does.
// The second result is false when node holds anything else.
func scalarText(node *niceyaml.Node) (string, bool) {
	n, _ := valueNode(node)

	switch v := n.(type) {
	case *ast.IntegerNode, *ast.FloatNode, *ast.InfinityNode, *ast.NanNode, *ast.BoolNode:
		return v.GetToken().Value, true
	default:
		return "", false
	}
}

// isExplicitString reports whether node holds a scalar the document
// writes as a string: a quoted scalar, a block scalar, or a scalar with
// a !!str tag. It reaches the scalar as [valueNode] does. The value raw
// holds, as the YAML types name it, must be a string, so a tag such as
// !!int on a quoted scalar decides the type.
func isExplicitString(node *niceyaml.Node, raw any) bool {
	if _, ok := raw.(string); !ok {
		return false
	}

	n, strTagged := valueNode(node)
	if strTagged {
		return true
	}

	switch v := n.(type) {
	case *ast.LiteralNode:
		return true
	case *ast.StringNode:
		t := v.GetToken().Type

		return t == token.SingleQuoteType || t == token.DoubleQuoteType

	default:
		return false
	}
}

// valueNode returns the node that node selects with its anchors, tags,
// and aliases looked through, and reports whether a !!str tag sits on the
// way. It follows each alias to the content of its anchor, as the decoder
// does. [niceyaml.Node.At] follows an alias at the end of a path but stops
// at a tag on it, so the node of `version: !t *v` still holds the alias.
// The first result is nil for an alias that does not resolve or that
// leads back to itself.
func valueNode(node *niceyaml.Node) (ast.Node, bool) {
	var (
		n         = node.AST()
		strTagged bool
		followed  []*ast.AliasNode
	)

	for !astnode.IsNil(n) {
		switch v := n.(type) {
		case *ast.AnchorNode:
			n = v.Value
		case *ast.TagNode:
			switch v.Start.Value {
			case string(token.StringTag), "!<" + strTagURI + ">":
				strTagged = true
			}

			n = v.Value

		case *ast.AliasNode:
			if slices.Contains(followed, v) {
				return nil, strTagged
			}

			followed = append(followed, v)

			target, err := docstate.Of(node).Resolver().Deref(v)
			if err != nil {
				return nil, strTagged
			}

			n = target

		default:
			return n, strTagged
		}
	}

	return nil, strTagged
}

// isNonNumberString reports whether raw, the value of node as the YAML
// types name it, is a string that YAML does not read as a number. Such a
// string is a scalar the document writes as a string, or a plain scalar
// outside the YAML float syntax, such as inf or 0x1p-2.
func isNonNumberString(node *niceyaml.Node, raw any) bool {
	if _, ok := raw.(string); !ok {
		return false
	}

	if isExplicitString(node, raw) {
		return true
	}

	_, ok := rawFloat(raw)

	return !ok
}

// isTimestamp reports whether node holds text that fits one of the
// timestampLayouts, where raw holds the value of node as the YAML types
// name it. A !!timestamp tag makes raw a [time.Time] whatever text it
// holds, so isTimestamp then reads the text that [derefContent] finds
// under the tag. A second tag, such as !!int, changes the value the
// decoder parses, so that text must also name the instant in raw. When
// derefContent finds no content, isTimestamp reports true.
//
// The timestampLayouts alone decide. For text that only an unmarshaler
// registered with go-yaml for [time.Time] reads as a time, isTimestamp
// reports false, so a zero time that unmarshaler reads from such text
// matches no want.
func isTimestamp(node *niceyaml.Node, raw any) bool {
	rt, tagged := raw.(time.Time)
	if tagged {
		content, held := derefContent(node)
		if !held {
			return true
		}

		switch v := content.(type) {
		case *ast.StringNode:
			raw = v.Value
		case *ast.LiteralNode:
			raw = v.Value.Value
		default:
		}
	}

	s, ok := raw.(string)
	if !ok {
		return false
	}

	for _, layout := range timestampLayouts {
		t, err := time.Parse(layout, s)
		if err == nil {
			return !tagged || t.Equal(rt)
		}
	}

	return false
}

// derefContent returns the content under node, and reports whether it
// found that content. It looks through what [astnode.Content] looks
// through and follows one alias to its anchor. It finds no content for
// an alias whose anchor holds an alias in turn. Following one alias
// alone keeps the cost of a node the same however long a chain of
// aliases it starts. The path that selects node has already followed an
// alias with no tag on it, so the alias here sits under a tag. That
// path resolves only when the alias under the tag has an anchor, so
// the alias here has one.
func derefContent(node *niceyaml.Node) (ast.Node, bool) {
	content := astnode.Content(node.AST())

	alias, ok := content.(*ast.AliasNode)
	if !ok {
		return content, true
	}

	anchor, _ := docstate.Of(node).Resolver().Anchor(alias) //nolint:errcheck // The path resolved the alias.

	content = astnode.Content(anchor)
	if _, ok := content.(*ast.AliasNode); ok {
		return nil, false
	}

	return content, true
}

// strTagURI is the full name of the !!str tag, which a verbatim tag
// spells out.
const strTagURI = "tag:yaml.org,2002:str"

// numericEqual reports whether a and b are numbers, of any integer or
// float kind, that hold the same value. A named type such as
// [time.Duration] counts by its kind. The second result is false when
// either is not a number, so the caller falls back to ==.
func numericEqual(a, b any) (bool, bool) {
	av, bv := reflect.ValueOf(a), reflect.ValueOf(b)
	if !isNumber(av) || !isNumber(bv) {
		return false, false
	}

	ak, bk := av.Kind(), bv.Kind()

	switch {
	case isFloat(ak) && isFloat(bk):
		return floatEqual(av.Float(), bv.Float()), true
	case isFloat(ak):
		return floatEqualsInteger(av.Float(), bv), true
	case isFloat(bk):
		return floatEqualsInteger(bv.Float(), av), true
	case isUnsigned(ak) && isUnsigned(bk):
		return av.Uint() == bv.Uint(), true
	case isUnsigned(ak):
		return unsignedEqualsSigned(av.Uint(), bv.Int()), true
	case isUnsigned(bk):
		return unsignedEqualsSigned(bv.Uint(), av.Int()), true
	default:
		return av.Int() == bv.Int(), true
	}
}

// floatEqual reports whether a and b hold the same value, counting two
// NaNs as equal, so a NaN want matches a .nan in the document.
func floatEqual(a, b float64) bool {
	return a == b || (math.IsNaN(a) && math.IsNaN(b))
}

// floatEqualsInteger reports whether f holds exactly the value of the
// integer v. The comparison runs in the integer's own type, since a
// float64 cannot hold every integer above 2^53.
func floatEqualsInteger(f float64, v reflect.Value) bool {
	if f != math.Trunc(f) {
		return false
	}

	if isUnsigned(v.Kind()) {
		if f < 0 || f >= math.MaxUint64 {
			return false
		}

		return uint64(f) == v.Uint()
	}

	if f < math.MinInt64 || f >= math.MaxInt64 {
		return false
	}

	return int64(f) == v.Int()
}

func unsignedEqualsSigned(u uint64, i int64) bool {
	if i < 0 || u > math.MaxInt64 {
		return false
	}

	return int64(u) == i
}

// wantsNil reports whether want is nil: a nil interface, or a nil pointer,
// map, slice, channel, or function held by one.
func wantsNil(want reflect.Value) bool {
	if want.Kind() == reflect.Interface {
		if want.IsNil() {
			return true
		}

		want = want.Elem()
	}

	switch want.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Chan, reflect.Func:
		return want.IsNil()
	default:
		return false
	}
}

// isNumber reports whether v holds an integer or a float of any Go type.
func isNumber(v reflect.Value) bool {
	if !v.IsValid() {
		return false
	}

	k := v.Kind()

	return isInteger(k) || isFloat(k)
}

// floatHoldsInteger reports whether raw, the value as the YAML types name
// it, can match v, the integer a decode of it gave, and reports true for
// a raw value that holds no float. An integer never matches a float with
// a fraction. The decoder converts a float to an integer type of its own
// without a range check, which turns -.inf or 1e19 into the lowest or
// highest int64 on some platforms, so the float must also hold the value
// of v. A type that decodes itself reads the float its own way, so only
// the fraction counts for it.
func floatHoldsInteger(raw any, v reflect.Value) bool {
	f, ok := rawFloat(raw)
	if !ok {
		return true
	}

	if f != math.Trunc(f) {
		return false
	}

	return !isPlain(v.Type()) || floatEqualsInteger(f, v)
}

// floatHoldsFloat32 reports whether raw, the value as the YAML types name
// it, can match v, the float32 a decode of it gave, and reports true for
// a raw value that holds no float. The decoder converts a float to a
// float32 without a range check, which turns a finite value too large
// for a float32, such as 1e39, into an infinity, so an infinite v
// matches only an infinite float. A type that decodes itself reads the
// float its own way, so floatHoldsFloat32 reports true for it.
func floatHoldsFloat32(raw any, v reflect.Value) bool {
	if !isPlain(v.Type()) || !math.IsInf(v.Float(), 0) {
		return true
	}

	f, ok := rawFloat(raw)

	return !ok || math.IsInf(f, 0)
}

// rawFloat returns the float that raw, the value as the YAML types name
// it, holds, and reports whether it holds one. The decoder reads some
// plain floats, such as 25e-1 and 1e19, as strings and converts them when
// it decodes them into an integer, so a string counts when it has the
// YAML float syntax. A string that Go alone parses as a float, such as
// inf or 0x1p-2, does not count.
func rawFloat(raw any) (float64, bool) {
	switch v := raw.(type) {
	case float64:
		return v, true
	case string:
		if !floatSyntax.MatchString(v) {
			return 0, false
		}

		f, err := strconv.ParseFloat(v, 64)

		return f, err == nil

	default:
		return 0, false
	}
}

func isInteger(k reflect.Kind) bool {
	return isUnsigned(k) || (k >= reflect.Int && k <= reflect.Int64)
}

func isUnsigned(k reflect.Kind) bool {
	return k >= reflect.Uint && k <= reflect.Uintptr
}

func isFloat(k reflect.Kind) bool {
	return k == reflect.Float32 || k == reflect.Float64
}
