// Package datapath finds where a location in decoded data lies in the
// document the data decoded from.
//
// A decode names a mapping member by the value of its key. The key 0x10
// sets the member 16, and a later key or a `<<` merge key can set the
// same member again. A path selects a key by its source text instead. A
// path built from the names of decoded data therefore might not select an
// entry, or might select the entry of another member.
//
// An [Index] reads the members of each mapping as a decode sets them. A
// walk starts at a [Target] that holds the node the data decoded from and
// steps down one member or element at a time. Each step writes the source
// spelling of the key it follows into the path of the Target. The step
// also keeps the node it reached, and that node locates an error where
// the path does not select it:
//
//	t := datapath.Target{Path: paths.Current(), Node: root}
//	t = idx.Member(t, "ports")
//	t = idx.Member(t, "16") // @.ports.0x10
//	t = idx.Element(t, 0)   // @.ports.0x10[0]
//
// [Index.Locate] runs such a walk down a list of names, in the form that
// a caller who locates an error in decoded data writes them.
//
// A walk in the other direction starts from the decoded data and pairs it
// with its document. Such a walk reads the node each value decodes from
// with [Read]. It reads the entries a decode sets with [Members] and
// [MembersBackward]. A walk of a decoded map names each of its keys as the
// document spells it, with [KeyOf] and [MapGroups].
//
// [MemberName] copies the rule by which go-yaml's decoder names a key. It
// names a key that a decode read as a Go value, such as the key of a
// [github.com/goccy/go-yaml.MapItem]. The decoder offers no call that
// names a Go value. Its encoder cannot stand in for one, because
// [github.com/goccy/go-yaml.ValueToNode] gives some keys a node that the
// decoder reads as another value. The float 2e+06 gets a node that reads
// as 0, and a [time.Time] gets a string.
//
// [Members] with merges and [MembersBackward] copy the order in which the
// decoder sets the entries of a mapping with `<<` merge keys. The decoder
// gives the values that it sets, but not the entries of the document that
// set them. The tests check each copy against the decoder.
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
	"go.jacobcolvin.com/niceyaml/internal/mapkey"
	"go.jacobcolvin.com/niceyaml/paths"
)

// Target is where a location in decoded data lies in a document. It holds
// the path that names the location and the node a walk reached there. The
// node locates an error where the path cannot. A walk starts at a Target
// that holds the node the data decoded from and the path to that node.
// [Index.Member] and [Index.Element] each return the Target one step down.
type Target struct {
	// Node is the node at the location, or nil when the walk did not
	// reach the location.
	Node ast.Node
	// Parent is what holds Node: the entry of the member step that
	// reached it, or the sequence and the index of the element step that
	// reached it.
	Parent astnode.Parent
	// Path is the path that names the location.
	Path paths.Path
	// Unspelled reports whether the walk wrote a decoded name into Path
	// without finding that the name selects the entry of its member.
	Unspelled bool
}

// Token returns the token to bind an error at when the walk reached the
// location of t but Unspelled is set. The path then might not select the
// location. With key set, the error is about the key of the member, like
// the error for a path that ends in [paths.Path.Key]. The token is the
// one that [Target.Start] gives, which a path that selects the location
// resolves to.
//
// It returns nil when the path selects the location. It also returns nil
// when the walk did not reach the location, such as through a mapping
// with a key the walk cannot name. The path is then all that says where
// the error lies.
func (t Target) Token(key bool) *token.Token {
	if !t.Unspelled {
		return nil
	}

	return t.Start(key)
}

// Start returns the token that a path to the location of t resolves to,
// whether or not the path of t selects the location. With key set, the
// token is the key of the entry that holds the node, as [astnode.KeyToken]
// gives it. Otherwise, and where an entry with a key does not hold the
// node, the token is the one [astnode.PathToken] gives the node. That
// function gives a scalar its own token, and gives a mapping or a
// sequence the key of its entry or the "-" of its element. Start returns
// nil when the walk did not reach the location.
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
// holds the member table of each mapping it has read. Each key therefore
// decodes one time, however many lookups pass through its mapping or merge
// that mapping. The resolver binds the aliases of the document. The finder
// tells which entry a spelling selects, and it counts the merge reads of
// every walk toward the limit behind [paths.ErrExcessiveMerging]. An
// Index is not safe for concurrent use.
//
// Create instances with [NewIndex].
type Index struct {
	resolver *paths.Resolver
	finder   *paths.EntryFinder
	keys     *mapkey.Keys
	members  map[ast.Node]memberTable
}

// memberTable holds the members of one mapping, by the name a decode
// gives each key. It is complete when it names every member the decode
// keeps. An incomplete table omits every entry before a key it cannot
// name. An earlier entry therefore might set a member whose name the
// table does not hold.
type memberTable struct {
	members  map[string]memberNode
	complete bool
}

// NewIndex creates a new [*Index] that follows aliases through r. It
// names each key of the document as keys names it, so a member takes the
// name that niceyaml's decode gives its key.
func NewIndex(r *paths.Resolver, keys *mapkey.Keys) *Index {
	return &Index{resolver: r, finder: r.EntryFinder(), keys: keys, members: map[ast.Node]memberTable{}}
}

// Member returns the [Target] one step down from t, at the member that a
// decode names name. It adds a `.name` selector to the path of the
// Target.
//
// The name is the one the decoder produced. The source might spell it
// another way, such as 0x10 for the member name 16. Member matches each
// key of the mapping at t by its decoded name and writes the source
// spelling of that key into the path instead. The spelling is the text
// that [paths.Resolver.KeyName] gives the key, and a path selector
// matches that text.
//
// Member follows each alias through the resolver of the index, so it
// reaches the node that a path through the same alias resolves to. A key
// under an aliased mapping keeps its source spelling too, and an alias key
// takes the spelling of the content of its anchor. A name that Member
// cannot follow keeps its decoded form, such as a member behind an alias
// that does not resolve. A member that a merge key brings in takes the
// spelling its source gives the key. The name keeps its decoded form when
// t does not hold a node, and so does a key that Member finds but cannot
// spell.
//
// Member writes a spelling only where a path selector with that spelling
// selects the entry that sets the member, as the finder of the index
// reports. A path selector can pick a later entry with the same spelling
// instead. That entry can be a later key of the mapping, a later merge
// key, or a later source of one merge key. The name then keeps its decoded
// form, and that form might not select an entry, or might select another
// entry.
//
// Where the name that Member writes does not select the entry of its
// member, every step below keeps its decoded name too. A spelled key below
// that name resolves under the entry the name selects, not under the
// member. The Target then reports the path as unspelled. Member still
// follows each member it finds, so the Target holds the node of the
// location, and [Target.Token] locates an error there. A member that
// Member does not find leaves the Target with a nil Node.
//
// The finder reads the sources of each merge key that brings in a key or
// stands after it. Steps through one index share the limit of one
// [paths.EntryFinder] on those reads. Past that limit, the finder does
// not report an entry for such a key, so its name and every name below
// keep their decoded forms.
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
// index of the sequence at t. It adds a `[n]` selector to the path of the
// Target. It follows each alias on the way to the sequence, as
// [Index.Member] does. The Target holds a nil Node when t does not hold a
// sequence, or holds one too short for that index.
//
// Element panics if index is negative, as [paths.Path.Index] does.
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
// through r. It returns nil for an alias that does not resolve. It also
// returns nil for an alias it reaches a second time, such as an alias
// that leads back to itself through a tag.
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

// Read returns the node that the decoder reads a field, an element, or a
// map value from. The argument node is the value the document holds for
// that target, and r binds the aliases of the document. Read returns the
// content under node, as [Deref] finds it. The bool result is false when
// the decoder does not read a value there. The decoder does not read a
// value from a null, or from an alias to one. It then leaves the target
// as it is and does not call an unmarshaler. An alias that does not
// resolve also gives a nil node and a false result.
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
// for [astnode.PathToken]. The holder is the entry that the last `.name`
// selector of p picks, or the sequence and the index that its last `[n]`
// selector picks.
//
// The key of an entry does not have a holder. A path that ends in `~`
// therefore does not have a parent when an entry with a key holds the
// node that the path before the `~` selects. Otherwise, the `~` selects
// the node itself, and ParentAt returns what holds it. ParentAt returns
// the zero [astnode.Parent] for the root of the document and for a path
// that does not select a node.
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

// MergeSources returns the mappings that entry brings in, where entry has
// a `<<` merge key. It returns them in the order the decoder applies them,
// as [paths.Resolver.MergeSources] finds them. It returns an error that
// wraps [paths.ErrAlias] when an alias on the way does not resolve.
func MergeSources(r *paths.Resolver, entry *ast.MappingValueNode) ([]*ast.MappingNode, error) {
	found, err := r.MergeSources(&ast.MappingNode{Values: []*ast.MappingValueNode{entry}})
	if err != nil {
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

// Members returns the entries of mapping in document order. It skips each
// entry without a key and each entry with a `<<` merge key. With merges
// set, it also yields the entries of the mappings those merge keys bring
// in, each where its merge key stands. That order copies the order in
// which go-yaml's decoder sets the entries, as the package documentation
// describes. It skips each merge key whose sources do not resolve, such as
// one that reads a reference document, with every source of that key. It
// reads each mapping one time, so a merge key that brings in a mapping the
// walk already read does not yield more entries.
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

// MembersBackward returns the entries of mapping and of the mappings its
// `<<` merge keys bring in. It yields them in reverse of the order the
// decoder sets them, which it copies from go-yaml's decoder, as the
// package documentation describes. The first entry it yields for a key is
// therefore the one the decode keeps. It skips each entry without a key
// and each entry with a merge key.
//
// A merge key whose sources do not resolve might set any key, so the walk
// ends there. It does not yield an earlier entry, of mapping or of a
// mapping that merges it. It reads each mapping one time, at the last
// place the decoder reads it. A merge key that brings in a mapping the
// walk already read therefore does not yield more entries.
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

// ElementNode returns the element at index of the sequence node holds. It
// returns nil for any other node, including a typed nil, and for an index
// the sequence does not hold.
func ElementNode(node ast.Node, index int) ast.Node {
	seq, ok := astnode.Content(node).(*ast.SequenceNode)
	if !ok || index < 0 || index >= len(seq.Values) {
		return nil
	}

	return seq.Values[index]
}

// MemberNode returns the value node of the member a decode names name in
// the mapping node holds, or nil when the index does not find such a
// member. It looks through what [astnode.Content] looks through but does
// not follow aliases. [Index.Deref] follows them.
func (idx *Index) MemberNode(node ast.Node, name string) ast.Node {
	return idx.lookup(node, name).value
}

// Lacks reports whether the value a decode reads from node is sure not to
// hold a member name. It reports true for a mapping when the index names
// every member and name is not one of them. It also reports true for a
// sequence or a scalar, because only a mapping holds members. It follows
// each alias on the way to the value, as [Index.Member] does.
//
// It reports false where the index cannot tell, such as for a nil node or
// behind an alias that does not resolve. It also reports false for a
// mapping with a key the index cannot name, such as a merge key whose
// sources do not resolve. Such a key might set a member of any name.
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
// reports true where the finder cannot tell, such as past the limit behind
// [paths.ErrExcessiveMerging], because the selector might then select an
// entry. It reports false for a nil node.
func (idx *Index) SelectsEntry(node ast.Node, name string) bool {
	if astnode.IsNil(node) {
		return false
	}

	_, err := idx.finder.Entry(idx.Deref(node), name)

	return !errors.Is(err, paths.ErrNotFound)
}

// SelectsOther reports whether a path selector with name selects an entry
// of the mapping node holds, where a decode gives the key of that entry
// another name. The key 3.10, for example, has the name 3.1. A path that
// ends in name then selects an entry that does not set a member of that
// name. It follows each alias on the way to the mapping, as
// [Index.Member] does.
//
// It also reports true for an entry whose key the index cannot name,
// because such a key might set a member of any name. It reports false
// where the index finds a member name in the mapping, because
// [Index.Member] tells which entry sets that member. It also reports false
// where the finder of the index returns an error or does not find an
// entry.
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
// mapping node holds, or a member with nil nodes when it does not find
// one.
func (idx *Index) lookup(node ast.Node, name string) memberNode {
	return idx.memberNodes(node).members[name]
}

// selects reports whether a path selector with name selects the entry
// that sets member in the mapping node holds, as the finder of idx
// reports. It reports false for a member with a nil entry. It also
// reports false where the finder returns an error, such as
// [paths.ErrExcessiveMerging] after its lookups pass that limit.
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
// name, the table holds the last one. The decode keeps the value of that
// last member.
//
// A merge key sets each member its sources define, and the table holds
// the member a source gives that name. A merge key whose sources do not
// resolve, or lead back to the mapping, might set a member of any name.
// The table therefore omits every member before that merge key.
//
// A key without a name might set a member of any name. Such keys include
// an alias key that does not resolve, and a typed-nil key that a tree
// built by hand might hold. The table omits every member before such a
// key rather than hold a member that the key might replace.
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

// keyName returns the member name that a decode gives key, as the keys of
// the index name it, and reports whether the key has one. A merge key has
// no name, because the decoder folds its value into the mapping.
func (idx *Index) keyName(key ast.MapKeyNode) (string, bool) {
	if astnode.IsMergeKey(key) {
		return "", false
	}

	return idx.keys.Key(key)
}

// addMerged adds to found each member that the sources of the merge key
// of member define, for each name that found does not hold. A later
// source takes precedence over an earlier one, as it does in a decode. It
// reports false when the sources do not resolve, or when the table of one
// of them is not complete.
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

// MemberName returns the member name that a decode into a map gives a key
// that it read as the Go value key, such as the key of a
// [github.com/goccy/go-yaml.MapItem]. A string key gives its text, and any
// other key gives its printed Go value. A nil key gives null rather than
// the <nil> that [fmt.Sprint] prints for it. The go-yaml decoder names a
// key value that way, but it offers no call that names a Go value. An
// [Index] names each key node of a document through [mapkey.Keys] instead.
//
// A decode with [github.com/goccy/go-yaml.UseOrderedMap] reads a mapping
// in a key as a [github.com/goccy/go-yaml.MapSlice], where a decode into a
// map reads a map[string]any, which prints otherwise. MemberName prints
// each MapSlice in key as that map, with the members that a decode into a
// map gives its items, so a later item of a name replaces an earlier one.
// The decode names each key inside such a mapping itself, as a string, so
// MemberName keeps that name.
func MemberName(key any) string {
	switch k := key.(type) {
	case nil:
		return "null"
	case string:
		return k
	default:
		return fmt.Sprint(unordered(k))
	}
}

// unordered returns v with each [yaml.MapSlice] in it as the map[string]any
// that a decode into a map reads, as [MemberName] names its items.
func unordered(v any) any {
	switch x := v.(type) {
	case yaml.MapSlice:
		m := make(map[string]any, len(x))
		for _, item := range x {
			m[MemberName(item.Key)] = unordered(item.Value)
		}

		return m

	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = unordered(e)
		}

		return out

	default:
		return v
	}
}

// Locate walks t down names and returns the path to the location they
// lead to. With key set, the location is the key of the entry there. It
// also returns the token to bind an error at where the path does not bind
// at the location. The token is nil where the path binds there, or where
// the walk did not reach a node to bind at.
//
// A name leads to an element when the step is at a sequence and the name
// reads as an index, as [strconv.Itoa] writes one. Any other name leads to
// a member. A value the walk reaches takes the path and the token that
// [Target.Token] gives it. So does a value the walk cannot follow, because
// the walk cannot tell there whether the data holds it. A schema reports a
// violation of such a value the same way.
//
// A value the data lacks ends the walk. The data lacks an element past
// the end of its sequence, and a member where [Index.Lacks] reports so.
// The walk also treats a member as missing where a key of the mapping
// spells the name but sets a member of another name, as
// [Index.SelectsOther] reports. A path through that key does not stand
// for the member.
//
// A missing value takes the path of the last value the walk reached, with
// the rest of the names below it as the caller wrote them. That path binds
// the way a path to a key that the document omits binds, and the token is
// nil, unless the path binds elsewhere. The path binds elsewhere when the
// path to the last value does not select that value. It also binds
// elsewhere when a key of its mapping spells the missing name, as
// [Index.SelectsEntry] reports. The token is then the one that
// [Target.Start] gives the last value with key set.
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

// step returns the target one name down from t, as [Index.Locate] reads a
// name. It also reports whether the data lacks a value there for the
// names to lead to. The target of a value the data lacks holds its path
// and a nil node.
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

// elementIndex returns the index that name stands for, and true, when
// node is a sequence and name is an index as [strconv.Itoa] writes one.
// Such an index is decimal digits without a sign or a leading zero. Any
// other name is the name of a member, and so is an index too large for an
// int.
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

// nanKey stands in a name table for a NaN key. A NaN does not equal any
// value, itself included, so it does not find an entry under its own
// value.
type nanKey struct{}

// timeKey stands in a name table for a [time.Time] key. A [time.Time]
// holds a pointer to its location. A decode of an offset such as +05:30
// makes a new location each time. The key in the map therefore does not
// equal another key that decodes from the same text. The fields together
// form the key in the table.
//
//nolint:unused // The fields tell the keys of a table apart.
type timeKey struct {
	zone   string
	sec    int64
	nsec   int
	offset int
}

// KeyOf returns the value under which a table of key names holds the name
// of a map key. A key that decodes from a document entry then finds the
// name of that entry. The value is a stand-in for a float NaN and for a
// [time.Time], alone or behind an interface. For any other key, the value
// is key itself. A pointer key stands for the value it points to, because
// go-yaml decodes a pointer key as that value. The bool result is false
// for a key that cannot key a map.
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
// to a value that can key a map, and key itself otherwise. The go-yaml
// decoder decodes a pointer key as the value it points to, so that value
// names the key rather than the address. The value a pointer points to
// might hold a slice or a map that refers back to itself. Formatting such
// a value does not finish. So a pointer to a value that cannot key a map
// stays a pointer, and [mapKey] names it by its type. A pointer held in
// an interface stays a pointer too, because go-yaml never decodes a key
// of an interface type to a pointer.
func pointee(key reflect.Value) reflect.Value {
	if key.Kind() == reflect.Pointer && !key.IsNil() && key.Elem().Comparable() {
		return key.Elem()
	}

	return key
}

// MapEntry is one entry of a decoded map.
type MapEntry struct {
	// Key is the key of the entry.
	Key reflect.Value
	// Value is the value of the entry.
	Value reflect.Value
}

// MapGroup holds the entries of a decoded map that share one path segment
// and one key type, as [MapGroups] orders them.
type MapGroup struct {
	// Seg is the path segment of the entries.
	Seg string
	// Entries holds the entries of the group.
	Entries []MapEntry
	// Ambiguous reports whether several entries share Seg, in this group
	// or another, so that a path cannot select the entry of one key.
	Ambiguous bool
}

// mapEntry is a [MapEntry] with the path segment and the type name its
// key orders by.
type mapEntry struct {
	MapEntry

	seg, typeName string
}

// MapGroups returns the entries of v, a decoded map, in groups that
// share a path segment and a key type. It orders the groups by segment
// and then by the name of the key type. The order therefore does not
// depend on how the map iterates. The argument names holds the text of
// each key as the document spells it, under the value that [KeyOf] gives
// the key.
//
// The segment of a key is its text in names. A key that names does not
// hold gives the string itself for a string key, and the formatted value
// for any other key. A non-nil pointer key that [KeyOf] does not look
// through gives its type in parentheses instead, such as
// (*[]interface {}). So does such a key behind an interface. Formatting
// such a key prints either a value that might refer back to itself or an
// address that changes from run to run. Several such keys of one type
// then share a segment.
//
// The names cannot distinguish several keys that KeyOf gives one value,
// such as several NaN keys, several time keys of one instant and zone,
// or several pointer keys to equal values. MapGroups deletes the names
// of such keys from names, so they share one segment rather than take the
// text of a document key. Keys of different types can share a segment
// too, such as 1 and "1" in a map[any]T. A `<<` merge can bring in such a
// key beside one that the mapping spells.
//
// MapGroups marks a group as ambiguous when several entries share its
// segment. It reads each value from the iteration rather than by a lookup
// of its key. A NaN key does not equal any key, itself included, so a
// lookup cannot find it.
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
