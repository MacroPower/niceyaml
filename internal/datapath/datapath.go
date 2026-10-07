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
package datapath

import (
	"errors"
	"fmt"
	"slices"

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
	// The entry that holds Node, when a member name reached it.
	Entry *ast.MappingValueNode
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
// resolve to: the token that starts the node, or the key of its entry.
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
// is the key of the entry that holds the node, and the token that starts
// the node otherwise or where no entry holds it. It returns nil when the
// walk did not reach the location.
func (t Target) Start(key bool) *token.Token {
	if astnode.IsNil(t.Node) {
		return nil
	}

	if key && t.Entry != nil {
		return keyToken(t.Entry)
	}

	return astnode.FirstToken(t.Node)
}

// keyToken returns the token that starts the key of entry, without the `?`
// of an explicit key, as a path that ends in [paths.Path.Key] resolves
// it. An entry with no key gives the token that starts its value.
func keyToken(entry *ast.MappingValueNode) *token.Token {
	if astnode.Content(entry.Key) == nil {
		return astnode.FirstToken(entry.Value)
	}

	key := ast.Node(entry.Key)
	if explicit, ok := key.(*ast.MappingKeyNode); ok {
		key = explicit.Value
	}

	return astnode.FirstToken(key)
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
	t.Node, t.Entry = member.value, member.entry

	return t
}

// Element returns the [Target] one step down from t, at the element at
// index of the sequence at t, with a `[n]` selector added to its path. It
// follows each alias on the way to the sequence, as [Index.Member] does.
// The Target holds no node when t holds no sequence, or one without that
// index.
func (idx *Index) Element(t Target, index int) Target {
	t.Path = t.Path.Index(index)
	t.Node, t.Entry = ElementNode(idx.Deref(t.Node), index), nil

	return t
}

// Deref returns the content under node. It looks through what
// [astnode.Content] looks through and follows each alias through the
// resolver of the index. It returns nil for an alias that does not
// resolve, and for an alias it reaches again, which leads back to itself
// through a tag.
func (idx *Index) Deref(node ast.Node) ast.Node {
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

		target, err := idx.resolver.Deref(alias)
		if err != nil {
			return nil
		}

		node = target
	}
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
	members := mappingMembers(node)

	for _, member := range slices.Backward(members) {
		// A tree built by hand may hold a nil member, which sets nothing.
		if member == nil {
			continue
		}

		if isMergeKey(member.Key) {
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
	sources, err := idx.resolver.MergeSources(&ast.MappingNode{
		Values: []*ast.MappingValueNode{member},
	})
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

// mappingMembers returns the members of the mapping node holds, or nil
// for any other node, including a typed nil.
func mappingMembers(node ast.Node) []*ast.MappingValueNode {
	switch n := astnode.Content(node).(type) {
	case *ast.MappingNode:
		return n.Values
	case *ast.MappingValueNode:
		return []*ast.MappingValueNode{n}
	}

	return nil
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
	if _, ok := astnode.Content(key).(ast.ScalarNode); !ok || isMergeKey(key) {
		return "", false
	}

	var v any

	err := yaml.NodeToValue(key, &v)
	if err != nil {
		return "", false
	}

	return MemberName(v), true
}

// isMergeKey reports whether key is a `<<` merge key, looking through the
// `?` indicator, anchors, and tags. A nil key, including a typed nil, is
// not a merge key.
func isMergeKey(key ast.Node) bool {
	_, ok := astnode.Content(key).(*ast.MergeKeyNode)

	return ok
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
