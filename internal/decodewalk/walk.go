package decodewalk

import (
	"context"
	"reflect"
	"slices"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/datapath"
	"go.jacobcolvin.com/niceyaml/internal/unmarshaler"
	"go.jacobcolvin.com/niceyaml/internal/yamlfield"
	"go.jacobcolvin.com/niceyaml/paths"
)

// Rules holds what a walk knows of the decode it follows.
type Rules struct {
	// Resolver binds the aliases of the document the decode reads.
	Resolver *paths.Resolver
	// View returns a node of the document as the decoder reads it.
	View func(node ast.Node) ast.Node
	// Decode decodes node into v as the decode does, with its options and
	// its anchors. The node is one View returned, or one a walk built from
	// such nodes.
	Decode func(node ast.Node, v any) error
	// Unmarshalers names the types the options of the decode give an
	// unmarshaler.
	Unmarshalers unmarshaler.Options
}

// Kind names what holds a [Value].
type Kind uint8

const (
	// Root is the value the walk starts at.
	Root Kind = iota
	// Field is a field of a struct, which reads the entry of its name.
	Field
	// Inline is a field a struct holds inline, which reads the mapping of
	// the struct. Its path is the path of the struct.
	Inline
	// Element is an element of a slice or an array.
	Element
	// Key is the key of a map entry, which the walk reaches before the
	// value of the entry. The walk does not read below a key.
	Key
	// Member is the value of a map entry.
	Member
)

// Reads names what the decoder reads below a [Value].
type Reads uint8

const (
	// Whole is a value the decoder reads in one piece. Such a value is a
	// scalar, or a node of a kind the type does not split, such as a
	// sequence where a struct takes a mapping.
	Whole Reads = iota
	// Nothing is an interface or a [yaml.MapSlice], which take any value
	// that the decoder does not hold a type for.
	Nothing
	// Fields is a struct that reads a mapping.
	Fields
	// Members is a map that reads a mapping.
	Members
	// Elements is a slice or an array that reads a sequence.
	Elements
)

// Action tells a walk where to go after a [Value].
type Action uint8

const (
	// Descend has the walk read the values below the value.
	Descend Action = iota
	// Skip has the walk continue after the value without reading the
	// values below it.
	Skip
	// Stop ends the walk.
	Stop
)

// Value is a value of the type a walk reads, beside the node the decoder
// reads it from.
type Value struct {
	// Parent is the value that holds this one, or nil for the root.
	Parent *Value
	// Type is the type of the value, with pointers removed.
	Type reflect.Type
	// Held is the node the document holds for the value, and it might be
	// an alias. For a key, it is the key of the entry.
	Held ast.Node
	// Node is the node the decoder reads the value from, as
	// [datapath.Read] returns it for Held. For a key, it is the key of the
	// entry. An inline field reads the node of the struct that holds it.
	Node ast.Node
	// Struct is the struct whose mapping holds Entry, for a field or for a
	// member or key of an inline map. For a field of an inline struct, it
	// is the struct the decoder decodes the mapping into, which holds the
	// inline one. An inline field names that struct too.
	Struct reflect.Type
	// Entry is the entry the value comes from, for a field, a key, or a
	// member.
	Entry *ast.MappingValueNode
	// Name is the field or the key, for a field, a key, or a member.
	Name string
	// The resolver of the walk, for a key or a member.
	resolver *paths.Resolver
	// From is what holds Held in the document.
	From astnode.Parent
	// Depth is the number of values that hold this one. It is zero for
	// the root.
	Depth int
	// Kind is what holds the value.
	Kind Kind
	// Reads is what the decoder reads below the value.
	Reads Reads
	// Named is false for a key that does not have a name a path can
	// spell, as [paths.Resolver.KeyName] reports. It is also false for
	// the member that follows such a key.
	Named bool
	// Whether the struct reads the entries that `<<` merge keys bring in.
	// The walk sets it for a struct and an inline field that it has gone
	// below.
	merges bool
}

// Path returns the path of v under the value the walk starts at, as an
// `@` path. An inline field does not add a selector.
func (v *Value) Path() paths.Path {
	var selectors []paths.Path

	for at := v; at != nil; at = at.Parent {
		switch at.Kind {
		case Field, Member:
			selectors = append(selectors, paths.Current().Child(at.Name))
		case Element:
			selectors = append(selectors, paths.Current().Index(at.From.Index))
		case Key:
			selectors = append(selectors, paths.Current().Key(), paths.Current().Child(at.Name))
		case Root, Inline:
			// The value takes the path of the one that holds it.
		}
	}

	slices.Reverse(selectors)

	return paths.Current().Join(selectors...)
}

// Selected reports whether a path through the name of v, a key or a
// member, selects the entry v comes from, in the mapping of the map that
// holds v. A path names an entry by the text of its key and selects the
// last entry with that text. Paths therefore do not point at an entry
// that a later one hides. Selected reports false for any other value.
func (v *Value) Selected() bool {
	if v.Kind != Key && v.Kind != Member || !v.Named {
		return false
	}

	selected, err := v.resolver.Entry(v.Parent.Node, v.Name)

	return err == nil && selected == ast.Node(v.Entry)
}

// Walk reads node as a value of type t and calls visit for each value the
// decoder reads, in the order it reads them. The argument from names what
// holds node in the document. The [Action] that visit returns tells the
// walk whether to go below the value.
//
// A struct reads its fields in the order it declares them, and an inline
// field where it stands. A sequence reads its elements in order. A map
// reads the entries of its mapping in document order, each key before its
// value. It reads the entries that a `<<` merge key brings in where the
// key stands.
//
// A field reads the entry the decoder reads for its name. The entry is
// the one that a path through the name selects, except where the entry
// comes from a `<<` merge source the decoder drops. The entry might also
// differ from that one where the struct ignores merges, as
// [yamlfield.IgnoresMerges] reports. The decoder drops a source that
// holds a key it does not read as a string, along with the sources that
// source merges.
//
// The walk skips each value that the decoder does not read anything for:
// a null, an alias to one, an alias that does not resolve, and a field
// that [yamlfield.ReadsAnchor] reports. It skips every field of a struct
// whose mapping holds a merge key that the decoder refuses, or a key that
// the decoder does not read as a string. The walk reaches an inline
// struct and an inline type that decodes itself as an [Inline] value. It
// reaches the members an inline map reads, one for each key the decoder
// reads.
//
// The walk goes below a struct, a map, or a sequence one time for each
// type it reads the node as. It thus reads a node that several aliases
// reach one time, and stops at a node that holds itself. It also ends
// when ctx ends.
func Walk(
	ctx context.Context, r Rules, t reflect.Type, node ast.Node, from astnode.Parent, visit func(*Value) Action,
) {
	newWalker(ctx, r, visit).root(t, node, from)
}

// walker holds the state of one walk.
type walker struct {
	ctx context.Context
	// The function the walk calls for each struct whose fields it reads,
	// before it reads them, with the mapping the struct reads. It might be
	// nil.
	onFields func(v *Value, mapping *ast.MappingNode)
	visit    func(*Value) Action
	// The structs, maps, and sequences the walk has gone below.
	seen map[seenKey]bool
	// The entries [walker.droppedBy] found for each mapping it has read.
	dropped map[*ast.MappingNode]map[*ast.MappingValueNode]bool
	rules   Rules
}

// seenKey names a node the walk has gone below, by the type it read the
// node as. The fields together form the key of the set the [walker]
// keeps.
//
//nolint:unused // The fields tell the keys of the set apart.
type seenKey struct {
	typ  reflect.Type
	node ast.Node
}

// newWalker creates a new [*walker].
func newWalker(ctx context.Context, r Rules, visit func(*Value) Action) *walker {
	return &walker{
		ctx:     ctx,
		visit:   visit,
		seen:    map[seenKey]bool{},
		dropped: map[*ast.MappingNode]map[*ast.MappingValueNode]bool{},
		rules:   r,
	}
}

// root walks node as a value of type t, where from holds node.
func (w *walker) root(t reflect.Type, node ast.Node, from astnode.Parent) {
	w.value(&Value{Kind: Root, Type: t, Held: node, From: from, Named: true})
}

// value walks v when the decoder reads something from it. The caller sets
// the Held field of v. It reports false when the walk ends.
func (w *walker) value(v *Value) bool {
	node, ok := datapath.Read(w.rules.Resolver, v.Held)
	if !ok {
		return true
	}

	v.Node = node
	v.Type = unmarshaler.Base(v.Type)

	return w.yield(v)
}

// yield visits v, and walks the values below it when the visit asks for
// that. It reports false when the walk ends.
func (w *walker) yield(v *Value) bool {
	if w.ctx.Err() != nil {
		return false
	}

	v.Reads = w.reads(v)
	if v.Parent != nil {
		v.Depth = v.Parent.Depth + 1
	}

	switch w.visit(v) {
	case Stop:
		return false
	case Skip:
		return true
	default:
		return w.below(v)
	}
}

// reads returns what the decoder reads below v.
func (w *walker) reads(v *Value) Reads {
	t := v.Type

	switch {
	case v.Kind == Key:
		return Whole
	case t.Kind() == reflect.Interface || t == unmarshaler.MapSliceType:
		return Nothing
	}

	switch datapath.Deref(w.rules.Resolver, v.Node).(type) {
	case *ast.MappingNode:
		switch t.Kind() {
		case reflect.Struct:
			return Fields
		case reflect.Map:
			return Members
		default:
			return Whole
		}

	case *ast.SequenceNode:
		if t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
			return Elements
		}
	}

	return Whole
}

// below walks the values below v. It reports false when the walk ends.
func (w *walker) below(v *Value) bool {
	content := datapath.Deref(w.rules.Resolver, v.Node)

	if v.Kind != Inline && v.Reads != Whole && v.Reads != Nothing {
		key := seenKey{typ: v.Type, node: content}
		if w.seen[key] {
			return true
		}

		w.seen[key] = true
	}

	switch v.Reads {
	case Fields:
		mapping, _ := content.(*ast.MappingNode) //nolint:errcheck // Fields reads a mapping.

		return w.fields(v, mapping)

	case Members:
		mapping, _ := content.(*ast.MappingNode) //nolint:errcheck // Members reads a mapping.

		for entry := range datapath.Members(w.rules.Resolver, mapping, true) {
			name, named := w.rules.Resolver.KeyName(entry.Key)
			if !w.entry(v, nil, v.Type, entry, name, named) {
				return false
			}
		}

	case Elements:
		seq, _ := content.(*ast.SequenceNode) //nolint:errcheck // Elements reads a sequence.

		for i, element := range seq.Values {
			from := astnode.Parent{Sequence: seq, Index: i}
			if !w.value(&Value{Parent: v, Kind: Element, Type: v.Type.Elem(), Held: element, From: from, Named: true}) {
				return false
			}
		}

	case Whole, Nothing:
		// The decoder reads nothing below the value.
	}

	return true
}

// fields walks the fields of v, a struct that reads mapping. It reads
// fields only where the decoder decodes them, as [Walk] describes. An
// inline struct reads the fields of its own type from the mapping of the
// struct that holds it. It reports false when the walk ends.
func (w *walker) fields(v *Value, mapping *ast.MappingNode) bool {
	owner := v.Struct

	if v.Kind != Inline {
		owner = v.Type
		v.merges = !yamlfield.IgnoresMerges(v.Type)

		if !w.readsAsStruct(mapping) || v.merges && w.mergeRefused(mapping) {
			return true
		}

		if w.onFields != nil {
			w.onFields(v, mapping)
		}
	}

	for field := range v.Type.Fields() {
		name, inline, skip := yamlfield.Name(field)
		if skip || yamlfield.ReadsAnchor(field) {
			continue
		}

		var ok bool

		if inline {
			ok = w.inline(v, owner, unmarshaler.Base(field.Type), mapping)
		} else {
			ok = w.field(v, owner, field.Type, name, mapping)
		}

		if !ok {
			return false
		}
	}

	return true
}

// field walks the field name of type t, which a struct of type owner
// reads from mapping. The argument parent is that struct or an inline
// field of it. It reports false when the walk ends.
func (w *walker) field(parent *Value, owner, t reflect.Type, name string, mapping *ast.MappingNode) bool {
	entry := w.entryOf(mapping, name, parent.merges)
	if entry == nil {
		return true
	}

	return w.value(&Value{
		Parent: parent,
		Kind:   Field,
		Type:   t,
		Held:   entry.Value,
		From:   astnode.Parent{Entry: entry},
		Entry:  entry,
		Struct: owner,
		Name:   name,
		Named:  true,
	})
}

// inline walks a field of type t that a struct of type owner holds
// inline. The argument parent is that struct or an inline field of it. A
// struct and a type that decodes itself read mapping as an [Inline]
// value. A map reads each entry the decoder reads for the struct, as a key
// and a member. It reports false when the walk ends.
func (w *walker) inline(parent *Value, owner, t reflect.Type, mapping *ast.MappingNode) bool {
	switch {
	case w.rules.Unmarshalers.ReportsOwnError(t) || t.Kind() == reflect.Struct:
		if inlined(parent, t) {
			return true
		}

		return w.yield(&Value{
			Parent: parent,
			Kind:   Inline,
			Type:   t,
			Held:   parent.Held,
			Node:   parent.Node,
			From:   parent.From,
			Struct: owner,
			Named:  true,
			merges: parent.merges,
		})

	case t.Kind() == reflect.Map:
		for entry := range datapath.Members(w.rules.Resolver, mapping, parent.merges) {
			name, ok := w.rules.Resolver.KeyName(entry.Key)
			if ok && w.entryOf(mapping, name, parent.merges) == entry && !w.entry(parent, owner, t, entry, name, true) {
				return false
			}
		}
	}

	return true
}

// inlined reports whether an inline field of type t holds v, or v is
// one, so the walk stops at a struct that holds itself inline.
func inlined(v *Value, t reflect.Type) bool {
	for at := v; at != nil && at.Kind == Inline; at = at.Parent {
		if at.Type == t {
			return true
		}
	}

	return false
}

// entry walks the key and then the value of entry, as a map of type t
// reads them. The argument parent holds the map, and owner is the struct
// that holds the map inline, or nil. It reports false when the walk ends.
func (w *walker) entry(
	parent *Value,
	owner, t reflect.Type,
	entry *ast.MappingValueNode,
	name string,
	named bool,
) bool {
	key := &Value{
		Parent:   parent,
		Kind:     Key,
		Type:     unmarshaler.Base(t.Key()),
		Held:     entry.Key,
		Node:     entry.Key,
		Entry:    entry,
		Struct:   owner,
		Name:     name,
		Named:    named,
		resolver: w.rules.Resolver,
	}

	if !w.yield(key) {
		return false
	}

	return w.value(&Value{
		Parent:   parent,
		Kind:     Member,
		Type:     t.Elem(),
		Held:     entry.Value,
		From:     astnode.Parent{Entry: entry},
		Entry:    entry,
		Struct:   owner,
		Name:     name,
		Named:    named,
		resolver: w.rules.Resolver,
	})
}

// entryOf returns the entry of mapping that the decoder reads for the
// key name when it decodes a struct, or nil when mapping does not hold
// one. With merges set, the entry is the one a path through name selects,
// unless [walker.droppedBy] reports it. Without merges, the entry is the
// last entry of mapping itself with that name.
func (w *walker) entryOf(mapping *ast.MappingNode, name string, merges bool) *ast.MappingValueNode {
	if merges {
		selected, err := w.rules.Resolver.Entry(mapping, name)
		if err != nil {
			return nil
		}

		entry, ok := selected.(*ast.MappingValueNode)
		if !ok || w.droppedBy(mapping)[entry] {
			return nil
		}

		return entry
	}

	for _, entry := range slices.Backward(mapping.Values) {
		if entry == nil || entry.Key == nil || astnode.IsMergeKey(entry.Key) {
			continue
		}

		if own, ok := w.rules.Resolver.KeyName(entry.Key); ok && own == name {
			return entry
		}
	}

	return nil
}

// droppedBy returns the entries the `<<` merge keys of mapping bring in
// that the decoder drops when it decodes a struct. The decoder drops each
// source with a key it does not read as a string, as [walker.readsAsStruct]
// tells, and the sources that source merges. It does not report an error
// for them. A path through such a source still selects its entries.
func (w *walker) droppedBy(mapping *ast.MappingNode) map[*ast.MappingValueNode]bool {
	if entries, ok := w.dropped[mapping]; ok {
		return entries
	}

	var entries map[*ast.MappingValueNode]bool

	w.eachSource(mapping, map[*ast.MappingNode]bool{}, func(src *ast.MappingNode) bool {
		if w.readsAsStruct(src) {
			return true
		}

		if entries == nil {
			entries = map[*ast.MappingValueNode]bool{}
		}

		for entry := range datapath.Members(w.rules.Resolver, src, true) {
			entries[entry] = true
		}

		return false
	})

	w.dropped[mapping] = entries

	return entries
}

// eachSource calls visit for each mapping the `<<` merge keys of mapping
// bring in, in document order. Where visit reports true for a source,
// eachSource then visits the mappings that source merges. The seen set
// holds the mappings whose merge keys the walk has read. The walk
// therefore reads the merge keys of each mapping one time, even those of
// a mapping that merges itself.
func (w *walker) eachSource(
	mapping *ast.MappingNode, seen map[*ast.MappingNode]bool, visit func(*ast.MappingNode) bool,
) {
	if seen[mapping] {
		return
	}

	seen[mapping] = true

	for _, entry := range mapping.Values {
		if entry == nil || !astnode.IsMergeKey(entry.Key) {
			continue
		}

		sources, err := datapath.MergeSources(w.rules.Resolver, entry)
		if err != nil {
			continue
		}

		for _, src := range sources {
			if visit(src) {
				w.eachSource(src, seen, visit)
			}
		}
	}
}

// readsAsStruct reports whether the decoder reads every key of mapping
// as a string, as [readsAsString] tells, so it decodes a struct from the
// mapping.
func (w *walker) readsAsStruct(mapping *ast.MappingNode) bool {
	for _, entry := range mapping.Values {
		if entry == nil || entry.Key == nil || astnode.IsMergeKey(entry.Key) {
			continue
		}

		if !readsAsString(w.rules.Resolver, entry.Key) {
			return false
		}
	}

	return true
}

// mergeRefused reports whether the decoder refuses the `<<` merge keys of
// mapping when it decodes a struct. The decoder refuses a merge key whose
// value it does not read as a mapping. It then neither decodes a field
// nor rejects a key. The test decodes only the merge keys into an empty
// struct. An unknown field there is a key of a source, not a refusal. A
// merge key without an entry in the view of the decode counts as refused.
func (w *walker) mergeRefused(mapping *ast.MappingNode) bool {
	var merges []*ast.MappingValueNode

	for _, entry := range mapping.Values {
		if entry == nil || !astnode.IsMergeKey(entry.Key) {
			continue
		}

		view := w.view(entry)
		if view == nil {
			return true
		}

		merges = append(merges, view)
	}

	if len(merges) == 0 {
		return false
	}

	var shell struct{}

	err := w.rules.Decode(ast.Mapping(mapping.Start, false, merges...), &shell)
	if err == nil {
		return false
	}

	_, unknown := err.(*yaml.UnknownFieldError) //nolint:errorlint // The decoder returns it unwrapped.

	return !unknown
}

// view returns entry as the decode reads it, or nil when the view of the
// decode does not hold an entry in its place.
func (w *walker) view(entry *ast.MappingValueNode) *ast.MappingValueNode {
	view, ok := w.rules.View(entry).(*ast.MappingValueNode)
	if !ok {
		return nil
	}

	return view
}

// readsAsString reports whether the go-yaml decoder reads key, the key
// of a mapping entry, as a string. The decoder reads a plain, quoted, or
// block scalar as a string, and anything under a !!str tag. An alias
// reads as the content of its anchor. An alias that does not resolve
// counts as a string, because the decoder rejects it before it reads the
// kind.
func readsAsString(resolver *paths.Resolver, key ast.Node) bool {
	for !astnode.IsNil(key) {
		switch k := key.(type) {
		case *ast.MappingKeyNode:
			key = k.Value

		case *ast.AnchorNode:
			key = k.Value

		case *ast.AliasNode:
			content, err := resolver.Deref(k)
			if err != nil {
				return true
			}

			key = content

		case *ast.TagNode:
			if k.Directive != nil {
				return true
			}

			switch token.ReservedTagKeyword(k.Start.Value) {
			case token.StringTag:
				return true
			case token.IntegerTag, token.FloatTag, token.NullTag, token.BooleanTag,
				token.BinaryTag, token.TimestampTag:
				return false
			default:
				// Any other tag leaves the kind to the value under it.
				key = k.Value
			}

		case *ast.StringNode, *ast.LiteralNode:
			return true

		default:
			return false
		}
	}

	return false
}
