// Package datapath finds where a location in decoded data lies in the
// document the data decoded from.
//
// A decode names a mapping member by the value of its key, so the key
// 0x10 sets the member 16, and a later key or a `<<` merge key can set
// the same member again. A path selects a key by its source text
// instead, so a path built from the names of decoded data can select
// nothing, or the entry of another member.
//
// An [Index] reads the members of each mapping as a decode sets them. A
// walk starts at a [Target] that holds the node the data decoded from and
// steps down one member or element at a time. Each step writes the source
// spelling of the key it follows into the path of the Target, and keeps
// the node it reached, which locates an error where no path selects it:
//
//	t := datapath.Target{Path: paths.Current(), Node: root}
//	t = idx.Member(t, "ports")
//	t = idx.Member(t, "16") // @.ports.0x10
//	t = idx.Element(t, 0)   // @.ports.0x10[0]
//
// [Index.Locate] runs such a walk down a list of names, as a caller
// that locates an error in decoded data writes them.
//
// A walk that pairs decoded data with its document the other way, from
// the data, reads the node each value decodes from with [Read], and the
// entries a decode sets with [Members] and [MembersBackward]. A walk of
// a decoded map names each of its keys as the document spells it, with
// [KeyOf] and [MapGroups].
package datapath

import (
	"cmp"
	"errors"
	"fmt"
	"iter"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/paths"
)

// Target is where a location in decoded data lies in a document: the path
// that names the location, and the node a walk reached there, which
// locates an error where the path cannot. A walk starts at a Target that
// holds the node the data decoded from and the path to that node, and
// [Index.Member] and [Index.Element] each return the Target one step
// down.
type Target struct {
	// The node at the location, or nil when the walk could not follow the
	// path to it.
	Node ast.Node
	// What holds Node: the entry of a member name that reached it, or the
	// sequence and the index of an element step that did.
	Parent astnode.Parent
	// The path that names the location.
	Path paths.Path
	// Whether the walk wrote a decoded name it could not show to select
	// the entry of its member.
	Unspelled bool
}

// Token returns the token to bind an error at when the walk reached its
// location and wrote a path it could not show to select it. With key set,
// the error is about the key of the member, as a path that ends in
// [paths.Path.Key] is. The token is the one a path to the location would
// resolve to, as [Target.Start] gives it.
//
// It returns nil when the path selects the location, and when the walk
// did not reach the location, as it cannot through a mapping whose keys
// it cannot all name. The path is then all that says where the error
// lies.
func (t Target) Token(key bool) *token.Token {
	if !t.Unspelled {
		return nil
	}

	return t.Start(key)
}

// Start returns the token a path to the location of t would resolve to,
// whether or not the path of t selects the location. With key set, that
// is the key of the entry that holds the node, as [astnode.KeyToken]
// gives it. Otherwise, and where no entry with a key holds the node, it
// is the token [astnode.PathToken] gives the node: its own token for a
// scalar, and the key of its entry or the "-" of its element for a
// mapping or a sequence. It returns nil when the walk did not reach the
// location.
func (t Target) Start(key bool) *token.Token {
	if astnode.IsNil(t.Node) {
		return nil
	}

	if key {
		if tk := astnode.KeyToken(t.Parent.Entry); tk != nil {
			return tk
		}
	}

	return astnode.PathToken(t.Node, t.Parent)
}

// Index finds the members of the mappings of one document by the names a
// decode gives them, for the walks that step through those mappings. It
// holds the member table of each mapping it has read, so each key decodes
// once however many lookups pass through its mapping or merge it in. The
// resolver binds the aliases of the document. The finder tells which
// entry a spelling selects, and weighs the merge reads of every walk
// together against the limit behind [paths.ErrExcessiveMerging]. An Index
// is not safe for concurrent use.
//
// Create instances with [NewIndex].
type Index struct {
	resolver *paths.Resolver
	finder   *paths.EntryFinder
	members  map[ast.Node]memberTable
}

// memberTable holds the members of one mapping, by the name a decode
// gives each key. It is complete when it names every member the decode
// keeps, so a member of an earlier mapping entry holds the value the
// decode keeps whenever the table leaves its name out.
type memberTable struct {
	members  map[string]memberNode
	complete bool
}

// NewIndex creates a new [*Index] that follows aliases through r.
func NewIndex(r *paths.Resolver) *Index {
	return &Index{resolver: r, finder: r.EntryFinder(), members: map[ast.Node]memberTable{}}
}

// Member returns the [Target] one step down from t, at the member a
// decode names name, with a `.name` selector added to its path.
//
// The name is the one the decoder produced, which the source may spell
// another way, as it spells the member name 16 as 0x10. Member matches
// each key of the mapping at t by its decoded name and writes the source
// spelling of that key into the path instead. The spelling is the text
// [paths.Resolver.KeyName] gives the key, which a path selector matches.
//
// Member follows each alias through the resolver of the index, so it
// reaches the node a path through the same alias resolves to. A key under
// an aliased mapping keeps its source spelling too, and an alias used as
// a key takes the spelling of its anchor's content. A name Member cannot
// follow keeps its decoded form, such as a member behind an alias that
// does not resolve. A member a merge key brings in takes the spelling its
// source gives the key. The name keeps its decoded form when t holds no
// node, and so does a key Member finds but cannot spell.
//
// Member writes a spelling only where a path selector with that spelling
// selects the entry that sets the member, as the finder of the index
// reports. A later entry with the same spelling can win the selector,
// such as a later key of the mapping, a later merge key, or a later
// source of one merge key. The name then keeps its decoded form, which
// may select no entry, or another entry. Where the name Member writes
// does not select the entry of its member, every step below keeps its
// decoded name too, since a key spelled below would resolve under the
// entry that name selects. The Target then reports the path as unspelled.
// Member still follows each member it finds, so the Target holds the node
// of the location, and [Target.Token] locates an error there. A member it
// does not find leaves the Target with no node.
//
// The finder reads the sources of a merge key that brings a key in or
// stands after it, and the steps that share the index share the limit of
// one [paths.EntryFinder] on those reads. Past that limit the finder
// reports no entry for such a key, so its name and every name below keep
// their decoded forms.
func (idx *Index) Member(t Target, name string) Target {
	content := idx.Deref(t.Node)
	member := idx.lookup(content, name)

	if !t.Unspelled {
		var (
			spelled string
			ok      bool
		)

		if member.entry != nil {
			spelled, ok = idx.resolver.KeyName(member.entry.Key)
		}

		switch {
		case ok && idx.selects(content, spelled, member):
			name = spelled

		case idx.selects(content, name, member):
			// The decoded name selects the entry as it is.

		default:
			t.Unspelled = true
		}
	}

	t.Path = t.Path.Child(name)
	t.Node, t.Parent = member.value, astnode.Parent{Entry: member.entry}

	return t
}

// Element returns the [Target] one step down from t, at the element at
// index of the sequence at t, with a `[n]` selector added to its path. It
// follows each alias on the way to the sequence, as [Index.Member] does.
// The Target holds no node when t holds no sequence, or one without that
// index.
//
// Panics if index is below 0, as [paths.Path.Index] does.
func (idx *Index) Element(t Target, index int) Target {
	content := idx.Deref(t.Node)

	t.Path = t.Path.Index(index)
	t.Node, t.Parent = ElementNode(content, index), astnode.Parent{}

	if seq, ok := content.(*ast.SequenceNode); ok && !astnode.IsNil(t.Node) {
		t.Parent = astnode.Parent{Sequence: seq, Index: index}
	}

	return t
}

// Deref returns the content under node, as [Deref] finds it through the
// resolver of the index.
func (idx *Index) Deref(node ast.Node) ast.Node {
	return Deref(idx.resolver, node)
}

// Deref returns the mapping, sequence, or scalar under node. It looks
// through what [astnode.Content] looks through and follows each alias
// through r. It returns nil for an alias that does not resolve, and for
// an alias it reaches again, which leads back to itself through a tag.
func Deref(r *paths.Resolver, node ast.Node) ast.Node {
	var followed []*ast.AliasNode

	for {
		node = astnode.Content(node)

		alias, ok := node.(*ast.AliasNode)
		if !ok {
			return node
		}

		if slices.Contains(followed, alias) {
			return nil
		}

		followed = append(followed, alias)

		target, err := r.Deref(alias)
		if err != nil {
			return nil
		}

		node = target
	}
}

// Read returns the node the decoder reads a field, an element, or a map
// value from, where node is the value the document holds for it and r
// binds the aliases of that document. It returns the content under
// node, as [Deref] finds it. The bool result is false when the decoder
// reads no value there. The decoder reads none from a null, or from an
// alias to one, and leaves the target as it is, with no call to an
// unmarshaler. An alias that does not resolve gives no node either.
func Read(r *paths.Resolver, node ast.Node) (ast.Node, bool) {
	if astnode.IsNil(node) {
		return nil, false
	}

	held := node
	if alias, ok := node.(*ast.AliasNode); ok {
		anchor, err := r.Anchor(alias)
		if err != nil {
			return nil, false
		}

		held = astnode.Content(anchor)
	}

	if astnode.IsNil(held) || held.Type() == ast.NullType {
		return nil, false
	}

	content, err := r.Deref(node)
	if err != nil || astnode.IsNil(content) {
		return nil, false
	}

	return content, true
}

// ParentAt returns what holds the node p selects in the document of r,
// for [astnode.PathToken]. That is the entry the last `.name` selector
// of p picks, or the sequence and the index its last `[n]` selector
// picks. A `~` selector picks the key of an entry, which nothing holds.
// Where no entry with a key holds the node the path before the `~`
// selects, the `~` selects that node, and what holds it stands. The root
// of the document has no parent, and neither does a path that selects
// nothing.
func ParentAt(r *paths.Resolver, p paths.Path) astnode.Parent {
	last, ok := p.Last()
	if !ok {
		return astnode.Parent{}
	}

	above, _ := p.Parent()

	if last.Kind == paths.SelectorKey {
		in := ParentAt(r, above)
		if in.Entry != nil && astnode.Content(in.Entry.Key) != nil {
			return astnode.Parent{}
		}

		return in
	}

	node, err := r.Node(above)
	if err != nil {
		return astnode.Parent{}
	}

	switch last.Kind {
	case paths.SelectorChild:
		entry, err := r.Entry(node, last.Name)
		if found, ok := entry.(*ast.MappingValueNode); err == nil && ok {
			return astnode.Parent{Entry: found}
		}

	case paths.SelectorIndex:
		if seq, ok := Deref(r, node).(*ast.SequenceNode); ok {
			return astnode.Parent{Sequence: seq, Index: last.Index}
		}

	default:
		// A selector that can pick several nodes names no one of them.
	}

	return astnode.Parent{}
}

// MergeSources returns the mappings entry, a `<<` merge key, brings in,
// in the order the decoder applies them, as [paths.Resolver.MergeSources]
// finds them. It returns an error wrapping [paths.ErrAlias] when an
// alias on the way does not resolve.
func MergeSources(r *paths.Resolver, entry *ast.MappingValueNode) ([]*ast.MappingNode, error) {
	found, err := r.MergeSources(&ast.MappingNode{Values: []*ast.MappingValueNode{entry}})
	if err != nil {
		//nolint:wrapcheck // The resolver names what failed.
		return nil, err
	}

	sources := make([]*ast.MappingNode, 0, len(found))

	for _, src := range found {
		if mapping, ok := src.(*ast.MappingNode); ok {
			sources = append(sources, mapping)
		}
	}

	return sources, nil
}

// Members returns the entries of mapping in document order. It passes
// over an entry with no key and over the `<<` merge keys themselves.
// With merges set, it yields the entries of the mappings those merge keys
// bring in as well, each where its merge key stands, and skips a merge
// key whose sources do not resolve. It reads each mapping once, so a
// mapping that merges itself ends the walk.
func Members(r *paths.Resolver, mapping *ast.MappingNode, merges bool) iter.Seq[*ast.MappingValueNode] {
	return func(yield func(*ast.MappingValueNode) bool) {
		members(r, mapping, merges, map[*ast.MappingNode]bool{}, yield)
	}
}

// members yields the entries [Members] yields for mapping, and reports
// false when yield stopped the walk.
func members(
	r *paths.Resolver, mapping *ast.MappingNode, merges bool, seen map[*ast.MappingNode]bool,
	yield func(*ast.MappingValueNode) bool,
) bool {
	if seen[mapping] {
		return true
	}

	seen[mapping] = true

	for _, entry := range mapping.Values {
		if entry == nil || entry.Key == nil {
			continue
		}

		if !astnode.IsMergeKey(entry.Key) {
			if !yield(entry) {
				return false
			}

			continue
		}

		if !merges {
			continue
		}

		sources, err := MergeSources(r, entry)
		if err != nil {
			continue
		}

		for _, src := range sources {
			if !members(r, src, merges, seen, yield) {
				return false
			}
		}
	}

	return true
}

// MembersBackward returns the entries of mapping, and of the mappings
// its `<<` merge keys bring in, in reverse of the order the decoder sets
// them, so the first entry it yields for a key is the one the decode
// keeps. It passes over an entry with no key and over the merge keys
// themselves. A merge key whose sources do not resolve may set any key,
// so the walk ends there and yields no earlier entry, of mapping or of a
// mapping that merges it. It reads each mapping once, at the last place
// the decoder reads it, so a mapping that merges itself ends the walk.
func MembersBackward(r *paths.Resolver, mapping *ast.MappingNode) iter.Seq[*ast.MappingValueNode] {
	return func(yield func(*ast.MappingValueNode) bool) {
		membersBackward(r, mapping, map[*ast.MappingNode]bool{}, yield)
	}
}

// membersBackward yields the entries [MembersBackward] yields for
// mapping, and reports false when the walk ended.
func membersBackward(
	r *paths.Resolver, mapping *ast.MappingNode, seen map[*ast.MappingNode]bool,
	yield func(*ast.MappingValueNode) bool,
) bool {
	if seen[mapping] {
		return true
	}

	seen[mapping] = true

	for _, entry := range slices.Backward(mapping.Values) {
		if entry == nil || entry.Key == nil {
			continue
		}

		if !astnode.IsMergeKey(entry.Key) {
			if !yield(entry) {
				return false
			}

			continue
		}

		sources, err := MergeSources(r, entry)
		if err != nil {
			return false
		}

		for _, src := range slices.Backward(sources) {
			if !membersBackward(r, src, seen, yield) {
				return false
			}
		}
	}

	return true
}

// ElementNode returns the element at index of the sequence node holds, or
// nil for any other node, including a typed nil, and for an index the
// sequence does not hold.
func ElementNode(node ast.Node, index int) ast.Node {
	seq, ok := astnode.Content(node).(*ast.SequenceNode)
	if !ok || index < 0 || index >= len(seq.Values) {
		return nil
	}

	return seq.Values[index]
}

// MemberNode returns the value node of the member a decode names name in
// the mapping node holds, or nil when the index finds no such member. It
// looks through what [astnode.Content] looks through and follows no
// alias, which [Index.Deref] does.
func (idx *Index) MemberNode(node ast.Node, name string) ast.Node {
	return idx.lookup(node, name).value
}

// Lacks reports whether the value a decode reads from node is sure to
// hold no member name. That holds for a mapping whose every member the
// index names, where none has that name, and for a sequence or a scalar,
// which holds no member. It follows each alias on the way to the value,
// as [Index.Member] does.
//
// It reports false where the index cannot tell. That holds for no node
// and behind an alias that does not resolve. It also holds for a mapping
// with a key whose name the index cannot tell, such as a merge key whose
// sources do not resolve, since such a key may set a member of any name.
func (idx *Index) Lacks(node ast.Node, name string) bool {
	content := idx.Deref(node)
	if content == nil {
		return false
	}

	table := idx.memberNodes(content)
	_, found := table.members[name]

	return table.complete && !found
}

// SelectsEntry reports whether a path selector with name selects an entry
// of the mapping node holds, as the finder of the index reports. It also
// reports true where the finder cannot tell, as it cannot past the limit
// behind [paths.ErrExcessiveMerging], since the selector may then select
// an entry. It reports false for no node.
func (idx *Index) SelectsEntry(node ast.Node, name string) bool {
	if astnode.IsNil(node) {
		return false
	}

	_, err := idx.finder.Entry(idx.Deref(node), name)

	return !errors.Is(err, paths.ErrNotFound)
}

// SelectsOther reports whether a path selector with name selects an entry
// of the mapping node holds whose key a decode gives another name, as the
// key 3.10 has the name 3.1. A path that ends in name then selects an
// entry that sets no member of that name. It follows each alias on the
// way to the mapping, as [Index.Member] does.
//
// It reports true as well for an entry whose key the index cannot name,
// since such a key may set a member of any name. It reports false where
// the index finds a member name in the mapping, since [Index.Member]
// tells which entry sets it, and where the finder of the index finds no
// entry or returns an error.
func (idx *Index) SelectsOther(node ast.Node, name string) bool {
	content := idx.Deref(node)
	if content == nil || idx.lookup(content, name).entry != nil {
		return false
	}

	found, err := idx.finder.Entry(content, name)
	if err != nil {
		return false
	}

	entry, ok := found.(*ast.MappingValueNode)
	if !ok || entry == nil {
		return false
	}

	decoded, ok := idx.keyName(entry.Key)

	return !ok || decoded != name
}

// lookup returns the member [Index.memberNodes] finds for name in the
// mapping node holds, or a member with nil nodes when it finds none.
func (idx *Index) lookup(node ast.Node, name string) memberNode {
	return idx.memberNodes(node).members[name]
}

// selects reports whether a path selector with name selects the entry
// that sets member in the mapping node holds, as the finder of idx
// reports. It reports false for a member with no entry, and where the
// finder returns an error, such as [paths.ErrExcessiveMerging] once its
// lookups pass that limit.
func (idx *Index) selects(node ast.Node, name string, member memberNode) bool {
	entry, err := idx.finder.Entry(node, name)

	return err == nil && member.entry != nil && entry == member.entry
}

// memberNode holds the entry that sets a mapping member, with the value
// node of that entry.
type memberNode struct {
	entry *ast.MappingValueNode
	value ast.Node
}

// memberNodes returns the members of the mapping node holds, by the name
// a decode gives each key, or an empty table for any other node. A decode
// sets the members in order, so where several members decode to one
// name, the table holds the last, which is the member whose value the
// decode keeps.
//
// A merge key sets each member its sources define, and the table holds
// the member a source gives that name. A merge key whose sources do not
// resolve, or that lead back to the mapping, may set a member of any
// name, so the table leaves out every member before it.
//
// An alias key decodes to the name the content of its anchor gives. A
// key with no name, such as an alias key [aliasKeyName] cannot name or a
// typed-nil key a tree built by hand may hold, may set a member of any
// name. The table leaves out every member before such a key rather than
// hold one the key may have replaced.
func (idx *Index) memberNodes(node ast.Node) memberTable {
	node = astnode.Content(node)

	if table, ok := idx.members[node]; ok {
		return table
	}

	// A merge source that leads back to node reads this incomplete table.
	idx.members[node] = memberTable{}

	table := memberTable{members: map[string]memberNode{}, complete: true}
	members, _ := astnode.Entries(node)

	for _, member := range slices.Backward(members) {
		// A tree built by hand may hold a nil member, which sets nothing.
		if member == nil {
			continue
		}

		if astnode.IsMergeKey(member.Key) {
			if !idx.addMerged(table.members, member) {
				table.complete = false

				break
			}

			continue
		}

		name, ok := idx.keyName(member.Key)
		if !ok {
			table.complete = false

			break
		}

		if _, seen := table.members[name]; !seen {
			table.members[name] = memberNode{entry: member, value: member.Value}
		}
	}

	idx.members[node] = table

	return table
}

// keyName returns the member name a decode gives key, and reports whether
// the key has one. An alias key has the name [aliasKeyName] gives it, and
// any other key the name [decodedKey] gives it.
func (idx *Index) keyName(key ast.MapKeyNode) (string, bool) {
	if _, isAlias := astnode.Content(key).(*ast.AliasNode); isAlias {
		return aliasKeyName(idx.resolver, key)
	}

	return decodedKey(key)
}

// addMerged adds to found each member the sources of the merge key of
// member define, for a name found does not hold yet. A later source wins
// over an earlier one, as it does in a decode. It reports false when the
// sources do not resolve, or when the table of one of them is not
// complete.
func (idx *Index) addMerged(found map[string]memberNode, member *ast.MappingValueNode) bool {
	sources, err := MergeSources(idx.resolver, member)
	if err != nil {
		return false
	}

	for _, src := range slices.Backward(sources) {
		table := idx.memberNodes(src)
		if !table.complete {
			return false
		}

		for name, m := range table.members {
			if _, seen := found[name]; !seen {
				found[name] = m
			}
		}
	}

	return true
}

// aliasKeyName returns the member name a decode gives an alias key. That is
// the name [decodedKey] gives the content of the anchor the alias refers
// to, which r resolves. It reports false for an alias that does not resolve
// and for content with no name. It also reports false for an alias under a
// tag or an anchor of the key's own, which may change the name.
func aliasKeyName(r *paths.Resolver, key ast.MapKeyNode) (string, bool) {
	var node ast.Node = key

	if explicit, ok := node.(*ast.MappingKeyNode); ok && explicit != nil {
		node = explicit.Value
	}

	alias, ok := node.(*ast.AliasNode)
	if !ok || alias == nil {
		return "", false
	}

	target, err := r.Deref(alias)
	if err != nil {
		return "", false
	}

	content, ok := target.(ast.MapKeyNode)
	if !ok {
		return "", false
	}

	return decodedKey(content)
}

// decodedKey returns the member name a decode gives the key node, with
// the key's tag applied, and reports whether the key has a name. A
// string key gives its unquoted text, and any other scalar gives its Go
// value as the decoder spells it. The hexadecimal key 0x10 reads as 16,
// !!bool yes reads as true, and a !!timestamp key reads as its time
// value. A null key reads as null in every spelling. A merge key has no
// name, because the decoder folds its value into the mapping. A key that
// is no scalar, such as a sequence, has no name either, and neither does
// a key the decoder cannot read on its own, such as an alias. A nil key,
// including a typed nil a tree built by hand may hold, has no name.
func decodedKey(key ast.MapKeyNode) (string, bool) {
	if _, ok := astnode.Content(key).(ast.ScalarNode); !ok || astnode.IsMergeKey(key) {
		return "", false
	}

	var v any

	err := yaml.NodeToValue(key, &v)
	if err != nil {
		return "", false
	}

	return MemberName(v), true
}

// MemberName returns the member name a decode into a map gives a key it
// reads as the Go value key, such as the key of a [yaml.MapItem] or the
// value a decode reads from a key node. A string key gives its text, and
// any other key gives its printed Go value. A nil key gives null rather
// than the <nil> it would print as.
func MemberName(key any) string {
	switch k := key.(type) {
	case nil:
		return "null"
	case string:
		return k
	default:
		return fmt.Sprint(k)
	}
}

// Locate walks t down names and returns the path to the location they
// lead to. With key set, the location is the key of the entry there. It
// also returns the token to bind an error at where the path does not bind
// at the location, and nil where it does or where the walk reached
// nothing to bind at.
//
// A name that reads as an index of the sequence at a step, as
// [strconv.Itoa] writes one, leads to an element. Any other name leads to
// a member. A value the walk reaches takes the path and the token
// [Target.Token] gives it. So does one the walk cannot follow, since the
// walk cannot tell there whether the data holds it, and a schema reports
// a violation of such a value the same way.
//
// A value the data lacks ends the walk. The data lacks an element past
// the end of its sequence, and a member where [Index.Lacks] reports so.
// The walk treats a member as missing too where a key of the mapping
// spells the name and sets a member of another name, as
// [Index.SelectsOther] reports, so no path through that key stands for
// it. The location of a missing value takes the path of the last value
// the walk reached, with each name from there on below it as the caller
// wrote it. That path binds where a path to a key the document leaves
// out binds, so the token is nil. It is the token of that last value
// where the path would bind elsewhere instead. That holds where the path
// to the value does not select it. It also holds where a key of the
// mapping there spells the missing name, as [Index.SelectsEntry]
// reports, since the member that key sets has another name.
func (idx *Index) Locate(t Target, names []string, key bool) (paths.Path, *token.Token) {
	for i, name := range names {
		next, lacks := idx.step(t, name)
		if !lacks {
			t = next

			continue
		}

		path := next.Path.Child(names[i+1:]...)

		if t.Unspelled || idx.SelectsEntry(t.Node, name) {
			return path, t.Start(true)
		}

		return path, nil
	}

	path := t.Path
	if key {
		path = path.Key()
	}

	return path, t.Token(key)
}

// step returns the target one name down from t, and whether the data
// holds no value there that the names could lead to, as [Index.Locate]
// reads a name. The target of a value the data lacks holds its path and
// no node.
func (idx *Index) step(t Target, name string) (Target, bool) {
	if index, ok := elementIndex(idx.Deref(t.Node), name); ok {
		next := idx.Element(t, index)

		return next, astnode.IsNil(next.Node)
	}

	// A member the data lacks never reaches [Index.Member], which asks
	// which entry the name selects. The walk then reads the merge sources
	// that a schema reads for a member it requires.
	if idx.Lacks(t.Node, name) || idx.SelectsOther(t.Node, name) {
		return Target{Path: t.Path.Child(name)}, true
	}

	return idx.Member(t, name), false
}

// elementIndex returns the index name stands for, and true, when node is
// a sequence and name is an index as [strconv.Itoa] writes one: decimal
// digits with no sign and no leading zero. Any other name is the name of
// a member, and so is an index too large for an int.
func elementIndex(node ast.Node, name string) (int, bool) {
	if _, ok := node.(*ast.SequenceNode); !ok {
		return 0, false
	}

	if name == "" || (len(name) > 1 && name[0] == '0') {
		return 0, false
	}

	for i := range len(name) {
		if name[i] < '0' || name[i] > '9' {
			return 0, false
		}
	}

	index, err := strconv.Atoi(name)

	return index, err == nil
}

// nanKey stands in a name table for a NaN key, since a NaN equals no
// value, itself included, and so finds no entry under its own value.
type nanKey struct{}

// timeKey stands in a name table for a [time.Time] key. A [time.Time]
// holds a pointer to its location, and a decode of an offset such as
// +05:30 makes a new location each time, so the key in the map equals no
// key that decodes from the same text. The fields together form the key
// in the table.
//
//nolint:unused // The fields tell the keys of a table apart.
type timeKey struct {
	zone   string
	sec    int64
	nsec   int
	offset int
}

// KeyOf returns the value a table of key names holds the name of a map
// key under, so a key that decodes from a document entry finds that
// entry's name: a stand-in for a float NaN and for a [time.Time], alone
// or behind an interface, or else the value of key itself. A pointer key
// stands for the value it points to, since go-yaml decodes a pointer key
// as that value. The bool result is false for a key that cannot key a
// map.
func KeyOf(key reflect.Value) (any, bool) {
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

// MapEntry is one entry of a decoded map.
type MapEntry struct {
	// The key of the entry.
	Key reflect.Value
	// The value of the entry.
	Value reflect.Value
}

// MapGroup is the entries of a decoded map that share one path segment
// and one key type, as [MapGroups] orders them.
type MapGroup struct {
	// The path segment of the entries.
	Seg string
	// The entries of the group.
	Entries []MapEntry
	// Whether several entries share Seg, in this group or another, so
	// no path selects the entry of one key.
	Ambiguous bool
}

// mapEntry is a [MapEntry] with the path segment and the type name its
// key orders by.
type mapEntry struct {
	MapEntry

	seg, typeName string
}

// MapGroups returns the entries of v, a decoded map, in groups that
// share a path segment and a key type, ordered by segment and then by
// the name of the key type. The order thus holds however the map
// iterates. The names hold the text of each key as the document spells
// it, under the value [KeyOf] gives the key.
//
// The segment of a key is its text in names, or else the string itself,
// or the formatted value of any other key. A non-nil pointer key that
// [KeyOf] does not look through, alone or behind an interface, gives its
// type in parentheses, such as (*[]interface {}), since formatting it
// would print a value that may refer back to itself, or an address that
// changes from run to run. Several such keys of one type then share a
// segment.
//
// The names cannot tell apart several keys that KeyOf gives one value,
// such as several NaN keys, several time keys of one instant and zone,
// or several pointer keys to equal values. MapGroups deletes the names
// of such keys from names, so none of them takes the text of a document
// key, and they share one segment. Keys of different types can share a
// segment too, such as 1 and "1" in a map[any]T, and a `<<` merge can
// bring in such a key beside one the mapping spells. Each group whose
// segment several entries share is ambiguous. Each value comes from the
// iteration rather than a lookup by its key, since a NaN key equals no
// key, itself included.
func MapGroups(v reflect.Value, names map[any]string) []MapGroup {
	entries := make([]mapEntry, 0, v.Len())
	shared := map[any]int{}

	for it := v.MapRange(); it.Next(); {
		key := it.Key()
		if k, ok := KeyOf(key); ok {
			shared[k]++
		}

		entries = append(entries, mapEntry{Key: key, Value: it.Value()})
	}

	for k, n := range shared {
		if n > 1 {
			delete(names, k)
		}
	}

	for i := range entries {
		entries[i].seg = mapKey(entries[i].Key, names)
		entries[i].typeName = keyTypeName(entries[i].Key)
	}

	compareKeys := func(a, b mapEntry) int {
		return cmp.Or(strings.Compare(a.seg, b.seg), strings.Compare(a.typeName, b.typeName))
	}

	slices.SortFunc(entries, compareKeys)

	ambiguous := map[string]bool{}

	for i := 1; i < len(entries); i++ {
		if entries[i].seg == entries[i-1].seg {
			ambiguous[entries[i].seg] = true
		}
	}

	var groups []MapGroup

	for len(entries) > 0 {
		n := 1
		for n < len(entries) && compareKeys(entries[0], entries[n]) == 0 {
			n++
		}

		group := MapGroup{Seg: entries[0].seg, Ambiguous: ambiguous[entries[0].seg]}
		for _, e := range entries[:n] {
			group.Entries = append(group.Entries, e.MapEntry)
		}

		groups = append(groups, group)
		entries = entries[n:]
	}

	return groups
}

// mapKey returns the path segment for a map key, as [MapGroups]
// describes.
func mapKey(key reflect.Value, names map[any]string) string {
	if k, ok := KeyOf(key); ok {
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
