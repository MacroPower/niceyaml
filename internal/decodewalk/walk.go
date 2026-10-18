package decodewalk

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"sync"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/datapath"
	"go.jacobcolvin.com/niceyaml/internal/pathshook"
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
	// Anchor returns the value of the anchor that alias reads, as the
	// decoder reads it, when Resolver does not hold that anchor, such as an
	// anchor of a reference document. It reports false when it does not
	// find one. A nil Anchor does not find any.
	Anchor func(alias *ast.AliasNode) (ast.Node, bool)
	// PastAliasLimit reports whether the aliases of the document take it
	// past the alias limit. A lookup of the entries that a `<<` merge key
	// brings in does not read a merge source through an alias of such a
	// document. A key that such a source might hold is then not known. A
	// nil PastAliasLimit reports false.
	PastAliasLimit func() bool
	// Keys keeps the keys of the merge sources of the document that the
	// lookups of a walk read, for the walks that share it. It also keeps
	// the names of the alias keys of the merge sources of reference
	// documents. Walks that share a Keys must read each document with one
	// resolver. They must also find one anchor for each name that an alias
	// of a reference document reads. A nil Keys keeps the keys for one
	// walk.
	Keys *Keys
	// AnchorKeys returns the [Keys] that keep the keys of the merge
	// sources under the anchor that Anchor returns for alias, other than
	// the alias keys. Those keys do not depend on the anchors that an alias
	// finds, so walks that find other anchors for an alias key can share
	// them. A lookup keeps the keys of each source that it reads through
	// alias in that Keys, until it reads another alias of a reference
	// document. The sources under the anchors inside the anchor go there
	// too. Anchors whose values share a node thus keep its keys once when
	// AnchorKeys returns one Keys for all of them. A nil AnchorKeys keeps
	// the keys for one walk.
	AnchorKeys func(alias *ast.AliasNode) *Keys
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
	// Reference is a value at an alias whose anchor the resolver of the
	// walk does not hold, such as an anchor of a reference document. The
	// walk cannot read below such a value. The decoder reads the anchor,
	// whatever the type of the value. A field whose entry a `<<` merge key
	// brings in from a reference document reads Reference too.
	Reference
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
	// an alias. For a key, it is the key of the entry. For a field whose
	// entry a `<<` merge key brings in from a reference document, it is the
	// alias of the document through which the merge key reads that entry.
	Held ast.Node
	// Node is the node the decoder reads the value from, as
	// [datapath.Read] returns it for Held. For a key, it is the key of the
	// entry. An inline field reads the node of the struct that holds it.
	// For a value that reads [Reference], Node is Held.
	Node ast.Node
	// Merged is the value of the entry of a reference document that the
	// decoder reads for a field, as the decoder reads it, when a `<<` merge
	// key brings that entry in. It is nil for any other value. Such a field
	// reads [Reference], and its Entry is that entry.
	Merged ast.Node
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
	// Whether the value is at an alias that reads [Reference].
	reference bool
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
// source merges. The walk reads a source in a reference document through
// [Rules.Anchor]. A field whose entry such a source brings in reads
// [Reference], and its [Value.Merged] holds the value of that entry. When
// the walk cannot read a source, it skips each entry that a merge key after
// the entry might replace.
//
// The walk skips each value that the decoder does not read anything for:
// a null, an alias to one, an alias that does not resolve, and a field
// that [yamlfield.ReadsAnchor] reports. It reaches a value at an alias
// that reads an anchor of a reference document as a value that reads
// [Reference], and it does not read below such a value. It skips every
// field of a struct whose mapping holds a merge key that the decoder
// refuses, or a key that the decoder does not read as a string. The walk
// reaches an inline struct and an inline type that decodes itself as an
// [Inline] value. It reaches the members an inline map reads, one for each
// key the decoder reads.
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
	// The keys of each merge source that a lookup of [walker.mergedEntry]
	// has read, as [walker.keysOf] finds them, when [Rules.Keys] is nil.
	keys map[keysOfKey]*sourceKeys
	// The keys that [Rules.AnchorKeys] keeps, when it is nil, made on
	// first use.
	anchorKeys *Keys
	// Whether the document is past the alias limit, as
	// [walker.pastAliasLimit] finds it, or nil before it asks.
	pastLimit *bool
	rules     Rules
	// Whether a lookup of [walker.mergedEntry] reads each merge source, as
	// a path does, rather than skip each source that a decode of a struct
	// drops.
	paths bool
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
		keys:    map[keysOfKey]*sourceKeys{},
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

	switch {
	case ok:
		v.Node = node
	case readsReference(w.rules.Resolver, v.Held):
		v.Node, v.reference = v.Held, true
	default:
		return true
	}

	v.Type = unmarshaler.Base(v.Type)

	return w.yield(v)
}

// readsReference reports whether the decoder reads held, the node the
// document holds for a value, through an alias whose anchor r does not
// hold because a reference document defines it.
func readsReference(r *paths.Resolver, held ast.Node) bool {
	alias, ok := astnode.Content(held).(*ast.AliasNode)
	if !ok {
		return false
	}

	_, err := r.Deref(alias)

	return errors.Is(err, paths.ErrReferenceAlias)
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
	case v.reference:
		return Reference
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

	case Whole, Nothing, Reference:
		// The walk reads nothing below the value.
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
	read := w.lookup(mapping, name, parent.merges)

	switch {
	case read.alias != nil:
		if w.referenceNull(read.entry.Value) {
			return true
		}

		// The walk cannot read below the entry of a reference document, so
		// the alias stands for the field, as for a value at such an alias.
		return w.yield(&Value{
			Parent:    parent,
			Kind:      Field,
			Type:      unmarshaler.Base(t),
			Held:      read.alias,
			Node:      read.alias,
			Merged:    read.entry.Value,
			From:      read.from,
			Entry:     read.entry,
			Struct:    owner,
			Name:      name,
			Named:     true,
			reference: true,
		})

	// A merge key after the entry might replace it, so the walk does not
	// read a value that the decoder might not read.
	case read.entry == nil || !read.known:
		return true
	}

	return w.value(&Value{
		Parent: parent,
		Kind:   Field,
		Type:   t,
		Held:   read.entry.Value,
		From:   astnode.Parent{Entry: read.entry},
		Entry:  read.entry,
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
			if !ok {
				continue
			}

			// A merge key after an entry that the lookup cannot tell about
			// might replace it.
			if read := w.lookup(mapping, name, parent.merges); read.alias != nil || read.entry != entry || !read.known {
				continue
			}

			if !w.entry(parent, owner, t, entry, name, true) {
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
// key name when it decodes a struct, as [walker.lookup] finds it. It
// returns nil when the decoder reads an entry of a reference document for
// name, or when it does not read any entry.
func (w *walker) entryOf(mapping *ast.MappingNode, name string, merges bool) *ast.MappingValueNode {
	read := w.lookup(mapping, name, merges)
	if read.alias != nil {
		return nil
	}

	return read.entry
}

// entryRead is the entry that the decoder reads for a key of a mapping
// when it decodes a struct, as [walker.lookup] finds it.
type entryRead struct {
	// The entry, or nil when the decoder does not read one. It is a node of
	// a reference document, as the decoder reads it, when alias is not nil.
	entry *ast.MappingValueNode
	// The alias of the document through which a `<<` merge key brings in
	// entry from a reference document, or nil when the document holds
	// entry.
	alias *ast.AliasNode
	// What holds alias in the document.
	from astnode.Parent
	// Whether the lookup read every merge source it needed.
	known bool
}

// lookup returns the entry of mapping that the decoder reads for the key
// name when it decodes a struct. With merges set, the entry is the one
// that a path through name selects, unless [walker.droppedBy] reports it.
// Without merges, the entry is the last entry of mapping itself with that
// name.
//
// The resolver cannot read the sources of a `<<` merge key that reads a
// reference document. For such a lookup, the walk reads the merge keys
// itself, as [walker.mergedEntry] describes, so the entry might lie in a
// reference document. When the walk cannot read a source either, the
// result holds the last entry of mapping itself with that name, and it
// reports that the lookup is not known. A merge key after that entry might
// then replace it.
func (w *walker) lookup(mapping *ast.MappingNode, name string, merges bool) entryRead {
	if !merges {
		return entryRead{entry: w.ownEntry(mapping, name), known: true}
	}

	selected, err := w.rules.Resolver.Entry(mapping, name)
	if errors.Is(err, paths.ErrReferenceAlias) {
		read := w.mergedEntry(mapping, name)
		if !read.known {
			read.entry = w.ownEntry(mapping, name)
		}

		return read
	}

	if err != nil {
		return entryRead{known: true}
	}

	entry, ok := selected.(*ast.MappingValueNode)
	if !ok || w.droppedBy(mapping)[entry] {
		return entryRead{known: true}
	}

	return entryRead{entry: entry, known: true}
}

// ownEntry returns the last entry of mapping itself whose key is name, or
// nil when mapping does not hold one.
func (w *walker) ownEntry(mapping *ast.MappingNode, name string) *ast.MappingValueNode {
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

// mergedEntry returns the entry of mapping that the decoder reads for the
// key name, as [walker.lookup] describes, by reading the merge keys of
// mapping and of each mapping they bring in. The decoder reads the entries
// of a mapping in order, the entries of a merge key where the key stands,
// and a later entry of a key replaces an earlier one. The lookup thus reads
// the entries from the last back, and the first entry with the key name
// wins. It reads an alias of a reference document through [Rules.Anchor].
// The decoder drops each source with a key that it does not read as a
// string, as [walker.readsAsStruct] tells, so the lookup skips such a source
// and the sources it merges. A walker that reads every source as a path
// does skips none. One lookup reads each mapping once.
//
// The lookup stops at the first source that it cannot read. A key that it
// has not found by then is not known. It reads only the merge keys that
// come after the last entry of mapping itself with the key name, so a
// lookup costs about as much as the merge sources it reads. The keys of
// each merge source that it reads stay in [Rules.Keys] and
// [Rules.AnchorKeys], as [walker.keysOf] describes. The lookups of many
// mappings and many documents that merge one source thus read its entries
// once.
func (w *walker) mergedEntry(mapping *ast.MappingNode, name string) entryRead {
	l := nameLookup{w: w, name: name, seen: map[ast.Node]bool{}}

	read, stop := l.entries(mapping, nil, nil, astnode.Parent{}, true)

	switch {
	case stop:
		return entryRead{}
	case read.entry == nil:
		return entryRead{known: true}
	default:
		return read
	}
}

// nameLookup holds the state of one lookup of [walker.mergedEntry].
type nameLookup struct {
	w *walker
	// The mappings and the sequences of merge sources that the lookup has
	// read. An alias inside its own anchor reads that anchor, so a source
	// can hold itself.
	seen map[ast.Node]bool
	// The key the lookup finds the entry of.
	name string
}

// entries returns the entry with the key name that the decoder reads in
// node, a mapping, as [walker.mergedEntry] describes. The argument via is
// the alias through which the lookup reads a reference document, and from
// is what holds via in the document. Both are zero for a mapping of the
// document. The argument anchor keeps the keys of the anchor of a reference
// document that holds node, as [Rules.AnchorKeys] describes. It is nil for
// a mapping of the document. With top set, node is the mapping of the
// struct itself. The lookup reads that mapping whatever its keys hold, as
// the walk of its fields does. The entry is nil when node does not bring
// in an entry of that key. The bool result reports that the lookup reached
// a source it cannot read, which ends it.
func (l *nameLookup) entries(
	node ast.Node, via *ast.AliasNode, anchor *Keys, from astnode.Parent, top bool,
) (entryRead, bool) {
	entries, ok := astnode.Entries(node)
	if !ok || l.seen[node] {
		return entryRead{}, false
	}

	l.seen[node] = true

	keys := l.w.keysOf(node, entries, anchor, via != nil, top)
	if !top && !l.w.paths && !keys.strings {
		return entryRead{}, false
	}

	own, hasOwn := keys.last(l.name)
	if hasOwn && astnode.IsMergeKey(entries[own].Key) {
		// The index of the resolver holds the last merge key under `<<`,
		// which is not an entry that a merge key brings in.
		hasOwn = false
	}

	// A merge key after the last entry of the key wins over that entry, and
	// a later merge key wins over an earlier one.
	for _, i := range slices.Backward(keys.merges) {
		if hasOwn && i < own {
			break
		}

		at := from
		if via == nil {
			at = astnode.Parent{Entry: entries[i]}
		}

		read, stop := l.merge(entries[i].Value, via, anchor, at)
		if stop || read.entry != nil {
			return read, stop
		}
	}

	if !hasOwn {
		return entryRead{}, false
	}

	return entryRead{entry: entries[own], alias: via, from: from, known: true}, false
}

// merge returns the entry with the key name that the mappings of value,
// the value of a `<<` merge key, bring in, the last source first, as
// [nameLookup.entries] reads them. The arguments via, anchor, and from
// are those of [nameLookup.entries], for value.
func (l *nameLookup) merge(
	value ast.Node, via *ast.AliasNode, anchor *Keys, from astnode.Parent,
) (entryRead, bool) {
	switch v := value.(type) {
	case *ast.AnchorNode:
		return l.merge(v.Value, via, anchor, from)

	case *ast.TagNode:
		return l.merge(v.Value, via, anchor, from)

	case *ast.AliasNode:
		target, ref, ok := l.w.follow(v, via)
		if !ok {
			return entryRead{}, true
		}

		if ref {
			anchor = l.w.keysOfAnchor(v)

			if via == nil {
				via = v
			}
		}

		return l.merge(target, via, anchor, from)

	case *ast.SequenceNode:
		if l.seen[v] {
			return entryRead{}, false
		}

		l.seen[v] = true

		for i, elem := range slices.Backward(v.Values) {
			at := from
			if via == nil {
				at = astnode.Parent{Sequence: v, Index: i}
			}

			read, stop := l.merge(elem, via, anchor, at)
			if stop || read.entry != nil {
				return read, stop
			}
		}

		return entryRead{}, false

	default:
		return l.entries(value, via, anchor, from, false)
	}
}

// sourceKeys indexes the entries of one mapping that a lookup reads, as
// [walker.keysOf] finds them.
type sourceKeys struct {
	// The index of the last entry with each key name. It might also hold
	// the last `<<` merge key under the name `<<`. For a mapping of a
	// reference document, it does not hold the alias keys.
	names map[string]int
	// The index of each `<<` merge key, in order.
	merges []int
	// The index of each alias key of a mapping of a reference document, in
	// order.
	aliases []int
	// Whether the decoder reads each key that is not a merge key as a
	// string, as [walker.readsAsStrings] tells.
	strings bool
}

// mappingKeys holds the keys of one mapping that a lookup reads, as
// [walker.keysOf] finds them.
type mappingKeys struct {
	*sourceKeys
	// The index of the last alias key with each name, for a mapping of a
	// reference document. An alias key has the name of the anchor that
	// [Rules.Anchor] finds for it.
	aliasNames map[string]int
}

// last returns the index of the last entry whose key has name. It reports
// false when no entry has that name.
func (k mappingKeys) last(name string) (int, bool) {
	i, ok := k.names[name]
	if j, held := k.aliasNames[name]; held && (!ok || j > i) {
		return j, true
	}

	return i, ok
}

// keysOfKey names the keys of a mapping that [walker.keysOf] read. A key
// of a reference document reads the anchor that [Rules.Anchor] finds for
// an alias key, so the kind of document is part of the name.
//
//nolint:unused // The fields tell the keys of the map apart.
type keysOfKey struct {
	node ast.Node
	ref  bool
}

// keysOf returns the keys of node, whose entries are entries. With top
// set, node is the mapping at which a lookup starts, a mapping of the
// document. The resolver has read the keys of such a mapping for the
// lookup of its own that failed. So keysOf reads them from the resolver,
// without the verdict of [walker.readsAsStrings], which such a mapping
// does not need. It does not keep them.
//
// Any other mapping is a merge source. The first lookup that asks for the
// keys of a merge source reads them, and keysOf keeps them. It keeps the
// keys of a merge source of the document in [Rules.Keys], or in the walker
// when the rules do not hold a Keys. With ref set, node belongs to a
// reference document, under the anchor whose keys anchor keeps. The keys
// of such a node do not depend on the resolver of the document. Only the
// alias keys depend on the anchors that an alias finds. So keysOf keeps
// the other keys in anchor, which every walk that reads the anchor
// shares. It keeps the names of the alias keys as it keeps the keys of a
// merge source of the document.
func (w *walker) keysOf(node ast.Node, entries []*ast.MappingValueNode, anchor *Keys, ref, top bool) mappingKeys {
	if top {
		if mapping, ok := node.(*ast.MappingNode); ok && pathshook.MappingKeys != nil {
			if names, merges := pathshook.MappingKeys(w.rules.Resolver, mapping); names != nil {
				return mappingKeys{sourceKeys: &sourceKeys{names: names, merges: merges}}
			}
		}

		return mappingKeys{sourceKeys: w.indexKeys(entries, false)}
	}

	key := keysOfKey{node: node, ref: ref}
	if !ref {
		return mappingKeys{sourceKeys: w.heldKeys(key, func() *sourceKeys { return w.indexKeys(entries, false) })}
	}

	keys := mappingKeys{sourceKeys: anchor.held(key, func() *sourceKeys { return w.indexKeys(entries, true) })}
	if len(keys.aliases) > 0 {
		keys.aliasNames = w.heldKeys(key, func() *sourceKeys { return w.aliasKeys(entries, keys.aliases) }).names
	}

	return keys
}

// indexKeys indexes entries, the entries of a mapping. With ref set, the
// entries belong to a reference document. The index then holds the
// place of each alias key, but not its name.
func (w *walker) indexKeys(entries []*ast.MappingValueNode, ref bool) *sourceKeys {
	keys := &sourceKeys{names: make(map[string]int, len(entries)), strings: w.readsAsStrings(entries, ref)}

	for i, entry := range entries {
		switch {
		case entry == nil || entry.Key == nil:
		case astnode.IsMergeKey(entry.Key):
			keys.merges = append(keys.merges, i)
		case ref && isAliasKey(entry.Key):
			keys.aliases = append(keys.aliases, i)
		default:
			if name, ok := w.keyName(entry.Key, ref); ok {
				keys.names[name] = i
			}
		}
	}

	return keys
}

// aliasKeys indexes the alias keys of entries, the entries of a mapping
// of a reference document, at the places that aliases holds. The index
// holds the name of each alias key, which reads the anchor that
// [Rules.Anchor] finds for it.
func (w *walker) aliasKeys(entries []*ast.MappingValueNode, aliases []int) *sourceKeys {
	keys := &sourceKeys{names: make(map[string]int, len(aliases))}

	for _, i := range aliases {
		if name, ok := w.keyName(entries[i].Key, true); ok {
			keys.names[name] = i
		}
	}

	return keys
}

// isAliasKey reports whether key, the key of a mapping entry, is an
// alias.
func isAliasKey(key ast.Node) bool {
	_, ok := astnode.Content(key).(*ast.AliasNode)

	return ok
}

// heldKeys returns the keys that [Rules.Keys] keeps for key, or that the
// walker keeps when the rules do not hold a Keys. The first call for key
// builds them with index.
func (w *walker) heldKeys(key keysOfKey, index func() *sourceKeys) *sourceKeys {
	if w.rules.Keys != nil {
		return w.rules.Keys.held(key, index)
	}

	if keys, ok := w.keys[key]; ok {
		return keys
	}

	keys := index()
	if w.keys != nil {
		w.keys[key] = keys
	}

	return keys
}

// keysOfAnchor returns the [Keys] that keep the keys of the merge sources
// under the anchor of a reference document that alias reads, as
// [Rules.AnchorKeys] returns them. The walker keeps those keys when the
// rules do not hold an AnchorKeys, or it does not return a Keys.
func (w *walker) keysOfAnchor(alias *ast.AliasNode) *Keys {
	if w.rules.AnchorKeys != nil {
		if keys := w.rules.AnchorKeys(alias); keys != nil {
			return keys
		}
	}

	if w.anchorKeys == nil {
		w.anchorKeys = &Keys{}
	}

	return w.anchorKeys
}

// Keys keeps the keys of the merge sources that the lookups of walks read,
// as [Rules.Keys] and [Rules.AnchorKeys] describe. It tells the sources
// apart by node. So it keeps the keys of each node once, whatever the
// number of mappings, aliases, anchors, and documents that merge it. The
// keys it keeps thus grow with the entries of the nodes that the walks
// read. Two nodes that stand for one mapping, such as two copies of it,
// each have keys of their own. The zero Keys is empty and ready for use.
// A Keys is safe for concurrent use.
type Keys struct {
	// The keys of each source, by its [keysOfKey].
	sources sync.Map
}

// held returns the keys that k keeps for key. The first call for key
// builds them with index. Two walks that read one source at the same time
// might each build its keys. Each builds the same keys, as [Rules.Keys]
// and [Rules.AnchorKeys] require. A nil Keys builds the keys on each call.
func (k *Keys) held(key keysOfKey, index func() *sourceKeys) *sourceKeys {
	if k == nil {
		return index()
	}

	if held, ok := k.sources.Load(key); ok {
		if keys, ok := held.(*sourceKeys); ok {
			return keys
		}
	}

	keys := index()
	k.sources.Store(key, keys)

	return keys
}

// PathEntries finds the entries that paths select in the mappings of one
// document, as [PathEntries.Entry] describes. The keys of the merge
// sources that its lookups read stay in [Rules.Keys] and
// [Rules.AnchorKeys]. So the lookups of many paths that merge one source
// read its entries once. A PathEntries is safe for concurrent use.
//
// Create instances with [NewPathEntries].
type PathEntries struct {
	rules Rules
}

// NewPathEntries creates new [PathEntries] that read the document of the
// resolver of r, and the anchors of reference documents through
// [Rules.Anchor].
func NewPathEntries(r Rules) *PathEntries {
	return &PathEntries{rules: r}
}

// Entry returns the entry that a path through the key name selects in
// mapping, a mapping of the document. The resolver of the path cannot read
// a `<<` merge key that reads a reference document. Entry reads such a
// merge key as the resolver reads the merge keys of the document. It
// reads an alias of a reference document through [Rules.Anchor]. It reads
// every merge source, as a path does, even one that a decode of a struct
// drops. The entry is nil when no entry has the key. The alias is the
// alias of the document through which a `<<` merge key brings in the entry
// from a reference document, or nil when the document holds the entry.
// The name `<<` selects the last merge key of mapping when no entry has a
// real key of that text, as the resolver reads it. Entry reports false
// when the lookup cannot read a merge source, such as one past
// [Rules.PastAliasLimit].
func (p *PathEntries) Entry(mapping *ast.MappingNode, name string) (*ast.MappingValueNode, *ast.AliasNode, bool) {
	// The walker reads every merge source as a path does.
	w := lookupWalker(p.rules)
	w.paths = true

	read := w.mergedEntry(mapping, name)
	if !read.known || read.entry != nil {
		return read.entry, read.alias, read.known
	}

	merges := w.keysOf(mapping, mapping.Values, nil, false, true).merges
	if len(merges) == 0 {
		return nil, nil, true
	}

	last := mapping.Values[merges[len(merges)-1]]
	if key, ok := w.rules.Resolver.KeyName(last.Key); ok && key == name {
		return last, nil, true
	}

	return nil, nil, true
}

// ReferenceKey returns the alias of the document through which a `<<`
// merge key brings in the entry of mapping, a mapping of the document,
// whose key has the token tk. The entry lies in a reference document, and
// the decoder reads it for a struct, as [walker.mergedEntry] finds such an
// entry. It reports false when no such entry has the key tk.
func ReferenceKey(r Rules, mapping *ast.MappingNode, tk *token.Token) (*ast.AliasNode, bool) {
	for _, read := range lookupWalker(r).mergedEntries(mapping) {
		if read.entry.Key != nil && read.entry.Key.GetToken() == tk {
			return read.alias, true
		}
	}

	return nil, false
}

// lookupWalker returns a walker that looks up the entries of mappings with
// r, as [walker.mergedEntry] does, and does not walk anything.
func lookupWalker(r Rules) *walker {
	return &walker{
		dropped: map[*ast.MappingNode]map[*ast.MappingValueNode]bool{},
		keys:    map[keysOfKey]*sourceKeys{},
		rules:   r,
	}
}

// mergedEntries returns each entry of a reference document that the
// decoder reads for a struct from mapping, one for each key, as
// [walker.mergedEntry] finds them. The entries come in the order the walk
// reaches them, which reads the entries of each mapping from the last
// back. It returns nil when the walk cannot read every source. It reads
// every merge source of mapping each time, and does not keep what it read.
func (w *walker) mergedEntries(mapping *ast.MappingNode) []entryRead {
	l := &mergeLookup{w: w, seen: map[ast.Node]bool{}, found: map[string]entryRead{}}
	l.entries(mapping, nil, astnode.Parent{}, true)

	if l.unknown {
		return nil
	}

	var reads []entryRead

	for _, name := range l.names {
		if read := l.found[name]; read.alias != nil {
			reads = append(reads, read)
		}
	}

	return reads
}

// mergeLookup holds the state of one lookup of [walker.mergedEntries],
// which reads every key of a mapping as [walker.mergedEntry] reads one.
type mergeLookup struct {
	w *walker
	// The mappings and the sequences of merge sources that the lookup has
	// read. An alias inside its own anchor reads that anchor, so a source
	// can hold itself.
	seen map[ast.Node]bool
	// The entry the decoder reads for each key that the lookup has found.
	found map[string]entryRead
	// The keys of found, in the order the lookup found them.
	names []string
	// Whether the lookup reached a merge source it cannot read.
	unknown bool
}

// entries reads the entries of node, a mapping, from the last back, as
// [walker.mergedEntry] describes. The arguments via, from, and top are
// those of [nameLookup.entries]. It reports true when the lookup cannot
// read a source, which ends it.
func (l *mergeLookup) entries(node ast.Node, via *ast.AliasNode, from astnode.Parent, top bool) bool {
	entries, ok := astnode.Entries(node)
	if !ok || l.seen[node] {
		return false
	}

	l.seen[node] = true

	if !top && !l.w.paths && !l.w.readsAsStrings(entries, via != nil) {
		return false
	}

	for _, entry := range slices.Backward(entries) {
		if entry == nil || entry.Key == nil {
			continue
		}

		if astnode.IsMergeKey(entry.Key) {
			at := from
			if via == nil {
				at = astnode.Parent{Entry: entry}
			}

			if l.merge(entry.Value, via, at) {
				return true
			}

			continue
		}

		name, ok := l.w.keyName(entry.Key, via != nil)
		if !ok {
			continue
		}

		if _, held := l.found[name]; held {
			continue
		}

		l.found[name] = entryRead{entry: entry, alias: via, from: from, known: true}
		l.names = append(l.names, name)
	}

	return false
}

// merge reads the mappings that value, the value of a `<<` merge key,
// brings in, the last first, as [mergeLookup.entries] reads them.
func (l *mergeLookup) merge(value ast.Node, via *ast.AliasNode, from astnode.Parent) bool {
	switch v := value.(type) {
	case *ast.AnchorNode:
		return l.merge(v.Value, via, from)

	case *ast.TagNode:
		return l.merge(v.Value, via, from)

	case *ast.AliasNode:
		target, ref, ok := l.w.follow(v, via)
		if !ok {
			l.unknown = true

			return true
		}

		if via == nil && ref {
			via = v
		}

		return l.merge(target, via, from)

	case *ast.SequenceNode:
		if l.seen[v] {
			return false
		}

		l.seen[v] = true

		for i, elem := range slices.Backward(v.Values) {
			at := from
			if via == nil {
				at = astnode.Parent{Sequence: v, Index: i}
			}

			if l.merge(elem, via, at) {
				return true
			}
		}

		return false

	default:
		return l.entries(value, via, from, false)
	}
}

// follow returns the value of the anchor that the decoder reads for alias,
// and reports whether that anchor lies in a reference document. The
// argument via is not nil for an alias of a reference document. An alias
// of the document reads the anchor that the resolver binds it to, or else
// the anchor of a reference document that [Rules.Anchor] finds. The last
// result is false when the lookup cannot read the anchor. A lookup does
// not read an alias of a document past the alias limit, as
// [Rules.PastAliasLimit] reports it.
func (w *walker) follow(alias, via *ast.AliasNode) (ast.Node, bool, bool) {
	if via == nil {
		if w.pastAliasLimit() {
			return nil, false, false
		}

		value, done, ok := w.documentAnchor(alias)
		if done {
			return value, false, ok
		}
	}

	if w.rules.Anchor == nil {
		return nil, false, false
	}

	value, ok := w.rules.Anchor(alias)

	return value, true, ok
}

// documentAnchor returns the value of the anchor of the document that the
// resolver binds alias to. The done result is false when the resolver
// finds that alias reads a reference document instead, and the last
// result is false when the lookup cannot read the anchor.
func (w *walker) documentAnchor(alias *ast.AliasNode) (ast.Node, bool, bool) {
	anchor, err := w.rules.Resolver.Anchor(alias)
	if errors.Is(err, paths.ErrReferenceAlias) {
		return nil, false, false
	}

	held, ok := anchor.(*ast.AnchorNode)
	if err != nil || !ok {
		return nil, true, false
	}

	return held.Value, true, true
}

// pastAliasLimit reports whether the document of the walk is past the
// alias limit, as [Rules.PastAliasLimit] reports it. The walker asks the
// rules once and keeps the answer.
func (w *walker) pastAliasLimit() bool {
	if w.pastLimit == nil {
		past := w.rules.PastAliasLimit != nil && w.rules.PastAliasLimit()
		w.pastLimit = &past
	}

	return *w.pastLimit
}

// keyName returns the text that a child selector matches key by, as
// [paths.Resolver.KeyName] gives it. With ref set, key belongs to a
// reference document, and an alias key reads the anchor that
// [Rules.Anchor] finds.
func (w *walker) keyName(key ast.Node, ref bool) (string, bool) {
	if !ref {
		return w.rules.Resolver.KeyName(key)
	}

	if alias, ok := astnode.Content(key).(*ast.AliasNode); ok {
		if w.rules.Anchor == nil {
			return "", false
		}

		value, ok := w.rules.Anchor(alias)
		if !ok {
			return "", false
		}

		key = value
	}

	return referenceKeys.KeyName(key)
}

// referenceKeys spells the keys of reference documents, which the resolver
// of a walk does not hold.
var referenceKeys = paths.NewResolver(nil)

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
	return w.readsAsStrings(mapping.Values, false)
}

// readsAsStrings reports whether the decoder reads the key of each of
// entries as a string, as [readsAsString] tells. It skips the `<<` merge
// keys. With ref set, the entries belong to a reference document, whose
// aliases the resolver of the walk does not bind.
func (w *walker) readsAsStrings(entries []*ast.MappingValueNode, ref bool) bool {
	resolver := w.rules.Resolver
	if ref {
		resolver = referenceKeys
	}

	for _, entry := range entries {
		if entry == nil || entry.Key == nil || astnode.IsMergeKey(entry.Key) {
			continue
		}

		if !readsAsString(resolver, entry.Key) {
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

// referenceNull reports whether the decoder reads value, the value of an
// anchor of a reference document, as a null. An alias in value reads the
// anchor that [Rules.Anchor] finds for it. The seen set ends a loop of
// aliases, which the decoder reads as a null too.
func (w *walker) referenceNull(value ast.Node) bool {
	seen := map[ast.Node]bool{}

	for {
		value = astnode.Content(value)

		alias, ok := value.(*ast.AliasNode)
		if !ok {
			return astnode.IsNil(value) || value.Type() == ast.NullType
		}

		if seen[alias] {
			return true
		}

		seen[alias] = true

		value, ok = w.rules.Anchor(alias)
		if !ok {
			return false
		}
	}
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
