package paths

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/aliaslimit"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
)

// match is one node a path resolved to. The entry field holds the mapping
// entry that holds the node when the last selector picked a mapping key.
// The seq field holds the sequence that holds the node when the last
// selector picked an element. The index field holds the index of the node
// in that sequence. A `~` selector that selects the node the path before
// it does keeps all three fields.
//
// The segs field holds the selectors that name the node alone. They are
// the selectors that the resolve has applied so far, and the resolve
// replaces each wildcard with the selectors that name what it matched. A
// `[*]` becomes the index it matched, and a `.*` becomes the name of the
// entry it matched. A `..name` or `..*` becomes the selectors down to the
// node it found.
//
// The order field holds one place for each step of the walk that reached
// the node. A step into a mapping adds the index of the entry, and a step
// into a sequence adds the index of the element. A step to the key of an
// entry adds -1, so a key comes before its value. Comparing two orders
// puts the matches in document order along the path. An entry that a `<<`
// merge key brings in takes the place of that merge key. A node reached
// through an alias takes the place of the alias.
type match struct {
	node  ast.Node
	entry *ast.MappingValueNode
	seq   *ast.SequenceNode
	segs  []segment
	order []int
	index int
}

// with returns the match at node that extends m. It appends seg to the
// selectors of m and ord to the order of m, in place. A resolve of a path
// of k selectors thus takes time in proportion to k rather than to k
// squared. The result can share the spare capacity of m, so at most one
// match can safely extend m. A selector that reaches several nodes from m
// clips m first. Each of its matches then copies the selectors and the
// order of m rather than writing past their end.
func (m match) with(node ast.Node, entry *ast.MappingValueNode, seg segment, ord ...int) match {
	return match{node: node, entry: entry, segs: append(m.segs, seg), order: append(m.order, ord...)}
}

// element returns the match at element i of seq that extends m, as
// [match.with] extends it, with the `[n]` selector that names the
// element.
func (m match) element(seq *ast.SequenceNode, i int) match {
	next := m.with(seq.Values[i], nil, segment{kind: segmentIndex, index: i}, i)
	next.seq, next.index = seq, i

	return next
}

// parent returns what holds the node of m, for [astnode.PathToken].
func (m match) parent() astnode.Parent {
	return astnode.Parent{Entry: m.entry, Sequence: m.seq, Index: m.index}
}

// key returns the match for the `~` selector applied to m. The match is
// the key of the entry that m holds. It is m itself when m does not hold
// an entry or its entry does not have a key. A `~` on a sequence element
// or the root thus selects what the path before it does. Either way, key
// appends the `~` to the selectors, as the path wrote it.
//
// The match drops the `?` of an explicit key but keeps the anchors and
// tags on the key, as a match of a value keeps those on the value. A tag
// thus decides how the key decodes, as it does when the decoder reads the
// mapping.
func (m match) key() match {
	seg := segment{kind: segmentKey}

	if m.entry == nil {
		next := m.with(m.node, nil, seg)
		next.seq, next.index = m.seq, m.index

		return next
	}

	if astnode.Content(m.entry.Key) == nil {
		return m.with(m.node, m.entry, seg)
	}

	key := ast.Node(m.entry.Key)
	if explicit, ok := key.(*ast.MappingKeyNode); ok {
		key = explicit.Value
	}

	return m.with(key, nil, seg, -1)
}

// resolver walks a document for the selectors of a [Path]. Its targets map
// holds the anchor each alias refers to. The enclosed set holds each alias
// that lies inside the content of the anchor it refers to, other than one
// that a `<<` merge key names. The decoder reads such an alias as null, so
// the alias does not resolve.
//
// The keys map holds the [*mappingKeys] of each mapping that a lookup or a
// recursive walk has read. A later lookup or walk in that mapping thus
// finds a key without reading its entries again. A [Resolver] can serve
// several goroutines, so the map is a [sync.Map].
//
// The nodes field counts the nodes of the document. [resolver.excessive]
// weighs the matches of a selector against that count, and
// [resolver.excessiveMerging] weighs the merge reads of a selector or of
// an [EntryFinder] against it.
//
// Create instances with [newResolver].
type resolver struct {
	targets  map[*ast.AliasNode]*ast.AnchorNode
	enclosed map[*ast.AliasNode]bool
	// Reports whether a reference document defines an anchor of a name, as
	// [WithReferenceAnchors] sets it, or nil.
	references func(name string) bool
	keys       sync.Map
	nodes      int
}

// mappingKeys indexes the entries of one mapping for [resolver.lookup] and
// [recursiveWalk.descend]. The names map holds the index of the last entry
// with each key name. The merges slice holds the index of each `<<` entry,
// in document order. The names map leaves out an entry whose key does not
// have a name, as [resolver.keyName] reports it, so a lookup never finds
// that entry.
//
// The decoder keeps a merge key apart from a real key whose text is `<<`,
// such as an alias key. The names map thus holds the last merge key under
// `<<` only when the mapping does not hold a real key of that name.
// [resolver.lookup] selects that merge key only when it does not find such
// a real key in any merge source either.
type mappingKeys struct {
	names  map[string]int
	merges []int
}

// mappingKeys returns the [*mappingKeys] of mapping, and reads its entries
// the first time a lookup or walk asks for them. Two goroutines that ask
// at the same time might each read the entries, and each gets an index
// with the same contents.
func (r *resolver) mappingKeys(mapping *ast.MappingNode) *mappingKeys {
	if cached, ok := r.keys.Load(mapping); ok {
		if keys, ok := cached.(*mappingKeys); ok {
			return keys
		}
	}

	keys := &mappingKeys{names: make(map[string]int, len(mapping.Values))}

	for i, entry := range mapping.Values {
		if entry == nil {
			continue
		}

		if astnode.IsMergeKey(entry.Key) {
			keys.merges = append(keys.merges, i)

			continue
		}

		if name, ok := r.keyName(entry.Key); ok {
			keys.names[name] = i
		}
	}

	if n := len(keys.merges); n > 0 {
		last := keys.merges[n-1]
		name, named := r.keyName(mapping.Values[last].Key)

		if _, ok := keys.names[name]; named && !ok {
			keys.names[name] = last
		}
	}

	r.keys.Store(mapping, keys)

	return keys
}

// mergeAfter reports whether a `<<` merge key comes after the entry at
// index i.
func (k *mappingKeys) mergeAfter(i int) bool {
	return len(k.merges) > 0 && k.merges[len(k.merges)-1] > i
}

// newResolver creates a new [*resolver] for doc.
//
// It binds each alias to the last anchor of its name before it in doc. The
// goccy/go-yaml decoder uses that anchor for the alias when it fills a map.
// An anchor counts from where it starts and again where its content ends.
// For the aliases after it, the anchor thus wins over an anchor of the
// same name inside its content.
//
// At each `<<` merge key, the decoder reads again each mapping that the
// merge key brings in, and so does newResolver. The anchors of that
// mapping thus count again there. Some anchors sit on the value of a `<<`
// merge key or on an element of a sequence there. Such an anchor counts
// for the aliases that other merge keys name. It counts for an alias in a
// value only when doc does not define another anchor of its name before
// that alias, as [anchorSet] describes.
//
// After it binds every alias, newResolver counts the nodes of doc,
// comments included. It also calls [enclosedAliases] to find the aliases
// that lie inside the content of the anchor they refer to.
func newResolver(doc *ast.DocumentNode) *resolver {
	b := &aliasBinder{
		anchors: newAnchorSet(),
		targets: map[*ast.AliasNode]*ast.AnchorNode{},
		merged:  map[*ast.MappingNode]anchorSet{},
		pending: map[*ast.MappingNode]*mergedRead{},
		open:    map[*ast.MappingNode]bool{},
		watched: map[ast.Node]bool{},
		merges:  map[*ast.AliasNode]bool{},
		version: 1,
	}

	ast.Walk(b, doc.Body)

	nodes := 0

	astnode.Inspect(doc.Body, func(ast.Node) bool {
		nodes++

		return true
	})

	return &resolver{targets: b.targets, enclosed: enclosedAliases(doc, b.targets, b.merges), nodes: nodes}
}

// enclosedAliases returns the aliases of doc that lie inside the content
// of the anchor they refer to. The targets map holds the anchor each
// alias refers to. The merges set holds the aliases that `<<` merge keys
// name, and the result leaves those aliases out.
func enclosedAliases(
	doc *ast.DocumentNode,
	targets map[*ast.AliasNode]*ast.AnchorNode,
	merges map[*ast.AliasNode]bool,
) map[*ast.AliasNode]bool {
	open := map[ast.Node]bool{}
	enclosed := map[*ast.AliasNode]bool{}

	// The walk reads the content of an anchor itself, so it knows which
	// anchors each alias lies in.
	var visit func(ast.Node) bool

	visit = func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.AnchorNode:
			open[n.Value] = true
			astnode.Inspect(n.Value, visit)
			delete(open, n.Value)

			return false

		case *ast.AliasNode:
			if target, ok := targets[n]; ok && open[target.Value] && !merges[n] {
				enclosed[n] = true
			}
		}

		return true
	}

	astnode.Inspect(doc.Body, visit)

	return enclosed
}

// anchorSet holds the anchors that the decoder has recorded so far, in the
// two maps the decoder keeps. The nodes map holds the last anchor of each
// name. The values map holds the last anchor of each name on a value that
// the decoder reads. Some anchors sit on the value of a `<<` merge key or
// on an element of a sequence there. Such an anchor goes into the nodes
// map alone, because the decoder reads the value of a merge key only for
// the mappings it merges.
//
// An alias in a value refers to the anchor in the values map. When the
// values map does not hold an anchor of its name, the alias refers to the
// one in the nodes map. An alias that a merge key names refers to the
// anchor in the nodes map.
//
// Create instances with [newAnchorSet].
type anchorSet struct {
	nodes  map[string]*ast.AnchorNode
	values map[string]*ast.AnchorNode
}

// newAnchorSet creates a new empty [anchorSet].
func newAnchorSet() anchorSet {
	return anchorSet{nodes: map[string]*ast.AnchorNode{}, values: map[string]*ast.AnchorNode{}}
}

// record records anchor under name in both maps. It skips a nil name.
func (s anchorSet) record(name *token.Token, anchor *ast.AnchorNode) {
	if name == nil {
		return
	}

	s.nodes[name.Value] = anchor
	s.values[name.Value] = anchor
}

// valueTarget returns the anchor that an alias in a value refers to when
// it names name, and whether it refers to any.
func (s anchorSet) valueTarget(name string) (*ast.AnchorNode, bool) {
	if target, ok := s.values[name]; ok {
		return target, true
	}

	target, ok := s.nodes[name]

	return target, ok
}

// add records every anchor of other over the ones of s, each in the map
// that holds it in other.
func (s anchorSet) add(other anchorSet) {
	maps.Copy(s.nodes, other.nodes)
	maps.Copy(s.values, other.values)
}

// aliasBinder binds aliases to anchors while [ast.Walk] visits a document in
// order. The anchors set holds the anchors that the walk has visited so
// far, and the targets map holds the anchor each visited alias refers to.
// Walk visits an anchor before its content, so an alias inside that
// content refers to the anchor around it. Like the decoder, the binder
// records the anchor again after it walks the content. An alias after the
// content thus refers to that anchor rather than to one of the same name
// inside it.
//
// The merged map holds the anchors that merging a mapping records, from
// [aliasBinder.mergedAnchors], for each mapping that a merge key has
// brought in through an alias. It holds them only when that result holds
// for every later merge. The pending map holds the other results
// that mergedAnchors has read, each with the state it depends on. The open
// map holds the anchored and merged mappings that the walk is inside of,
// and the ones that mergedAnchors is reading. A mapping that merges itself
// thus stops there.
//
// The watched set holds each mapping and alias that a result in the
// pending map depends on. The version field counts the changes to the
// state of a watched node. A result that held at one version thus holds
// until the next, as [aliasBinder.holds] describes.
//
// The merges set holds each alias that a `<<` merge key names and that
// the binder has bound.
type aliasBinder struct {
	anchors anchorSet
	targets map[*ast.AliasNode]*ast.AnchorNode
	merged  map[*ast.MappingNode]anchorSet
	pending map[*ast.MappingNode]*mergedRead
	open    map[*ast.MappingNode]bool
	watched map[ast.Node]bool
	merges  map[*ast.AliasNode]bool
	version int
}

// Visit records an anchor or binds an alias, then returns b so [ast.Walk]
// continues into the children of node. It walks the content of an anchor
// and the entry of a `<<` merge key itself and returns nil for them.
//
// It returns nil for a nil node, including a typed nil that a hand-built
// tree might hold. Walk then stops rather than reading the fields behind
// that node.
func (b *aliasBinder) Visit(node ast.Node) ast.Visitor {
	if astnode.IsNil(node) {
		return nil
	}

	switch n := node.(type) {
	case *ast.AnchorNode:
		name := astnode.Token(n.Name)
		b.anchors.record(name, n)

		if mapping := anchoredMapping(n.Value); mapping != nil {
			b.walkOpen(mapping)
		} else {
			ast.Walk(b, n.Value)
		}

		// The decoder records an anchor again once it has read the
		// content, over any anchor of the same name inside it.
		b.anchors.record(name, n)

		return nil

	case *ast.AliasNode:
		name := astnode.Token(n.Value)
		if name == nil {
			return b
		}

		if target, ok := b.anchors.valueTarget(name.Value); ok {
			b.bind(n, target)
		}

	case *ast.MappingValueNode:
		if astnode.IsMergeKey(n.Key) {
			ast.Walk(b, n.Key)
			b.merge(n.Value)

			return nil
		}
	}

	return b
}

// merge walks the value of a `<<` merge key in the order the decoder reads
// it. [aliasBinder.findSources] records the anchors and binds the aliases
// on the way to each mapping the value merges. Then merge walks each
// inline mapping, and records again the anchors of each mapping an alias
// brings in.
func (b *aliasBinder) merge(value ast.Node) {
	sources, _ := b.findSources(value, b.anchors.nodes, true)

	for _, src := range sources {
		if src.inline {
			b.walkOpen(src.mapping)

			continue
		}

		b.anchors.add(b.mergedAnchors(src.mapping).anchors)
	}
}

// mergeSource is a mapping a `<<` merge key brings in. The inline field
// reports whether the value of the merge key holds the mapping itself,
// rather than an alias to it.
type mergeSource struct {
	mapping *ast.MappingNode
	inline  bool
}

// findSources returns the mappings that a `<<` merge key with value brings
// in, in the order the decoder reads them. It looks through the anchors,
// tags, and aliases on value and on each element of a sequence of them.
// The decoder does the same before it reads any of the mappings.
// FindSources records each anchor on the way into nodes, the nodes map of
// an [anchorSet]. It follows an alias to the anchor that the alias is
// bound to, and stops at an alias it has already followed. It also returns
// each alias it stops at because the alias is not bound.
//
// With bind set, findSources binds each alias of value itself to the
// anchor of its name in nodes. It also walks any other node of value where
// the decoder expects a mapping, so the aliases inside that node bind too.
func (b *aliasBinder) findSources(
	value ast.Node, nodes map[string]*ast.AnchorNode, bind bool,
) ([]mergeSource, []*ast.AliasNode) {
	var (
		sources []mergeSource
		unbound []*ast.AliasNode
	)

	var find func(node ast.Node, inline, top bool)

	find = func(node ast.Node, inline, top bool) {
		var followed []*ast.AliasNode

		for !astnode.IsNil(node) {
			switch n := node.(type) {
			case *ast.AnchorNode:
				if name := astnode.Token(n.Name); name != nil {
					nodes[name.Value] = n
				}

				node = n.Value

			case *ast.TagNode:
				node = n.Value
			case *ast.AliasNode:
				if slices.Contains(followed, n) {
					return
				}

				followed = append(followed, n)

				if bind && inline {
					b.bindMergeAlias(n, nodes)
				}

				target, ok := b.targets[n]
				if !ok {
					unbound = append(unbound, n)

					return
				}

				node, inline = target.Value, false

			case *ast.MappingNode:
				sources = append(sources, mergeSource{mapping: n, inline: inline})

				return

			case *ast.SequenceNode:
				if top {
					for _, v := range n.Values {
						find(v, inline, false)
					}
				} else if bind && inline {
					ast.Walk(b, n)
				}

				return

			default:
				if bind && inline {
					ast.Walk(b, n)
				}

				return
			}
		}
	}

	find(value, true, true)

	return sources, unbound
}

// bindMergeAlias binds alias to the anchor of its name in nodes and adds
// alias to the merges set. A `<<` merge key names alias, and nodes is the
// nodes map of an [anchorSet].
func (b *aliasBinder) bindMergeAlias(alias *ast.AliasNode, nodes map[string]*ast.AnchorNode) {
	name := astnode.Token(alias.Value)
	if name == nil {
		return
	}

	if target, ok := nodes[name.Value]; ok {
		b.merges[alias] = true
		b.bind(alias, target)
	}
}

// bind binds alias to target.
func (b *aliasBinder) bind(alias *ast.AliasNode, target *ast.AnchorNode) {
	b.targets[alias] = target
	b.touch(alias)
}

// setOpen records whether the walk, or [aliasBinder.mergedAnchors], is
// inside mapping.
func (b *aliasBinder) setOpen(mapping *ast.MappingNode, open bool) {
	if open {
		b.open[mapping] = true
	} else {
		delete(b.open, mapping)
	}

	b.touch(mapping)
}

// touch starts a new version when a result in the pending map depends on
// node. Callers call touch after the state of node changes.
func (b *aliasBinder) touch(node ast.Node) {
	if b.watched[node] {
		b.version++
	}
}

// mergedRead is the result of [aliasBinder.mergedAnchors]. The anchors
// set holds the anchors that merging a mapping records. The final field
// reports whether they hold for every later merge of the mapping.
//
// The mapping field holds the mapping that the result is for. The open
// field reports whether that mapping was open. It is true only for the
// empty result of a mapping that mergedAnchors found open. The unbound set
// holds each alias of a merge key that the read found unbound. The reads
// slice holds the non-final results of the mappings that the read merged.
// A result that holds any such alias or result is not final.
//
// A result that is not final holds as long as the state it read stays the
// same:
//
//   - Its mapping stays open or closed as the open field records, and the
//     mapping does not have a final result yet.
//   - Each of its unbound aliases stays unbound.
//   - Each of its reads holds.
//
// The checked field holds the version at which [aliasBinder.holds] last
// checked the result, and the held field holds what that check found.
type mergedRead struct {
	anchors anchorSet
	mapping *ast.MappingNode
	unbound map[*ast.AliasNode]bool
	reads   []*mergedRead
	checked int
	open    bool
	final   bool
	held    bool
}

// mergedAnchors returns the anchors that the decoder records when a `<<`
// merge key brings mapping in. Those are the last anchor of each name in
// mapping, in document order. A `<<` merge key inside mapping counts the
// anchors of the mappings it merges. MergedAnchors finds the mappings that
// those merge keys bring in through the aliases as Visit bound them.
//
// A merge records the anchors of a mapping only after the walk leaves that
// mapping. The result is thus not final when mapping or a mapping it
// merges is open. An alias that Visit has not bound yet can bring in a
// mapping after it binds. The result is thus also not final when one of
// those merge keys names such an alias.
//
// MergedAnchors keeps a final result in the merged map. It keeps any other
// result in the pending map, and reuses that result as long as the result
// holds. A result that is not final refers to the results it merged rather
// than copying what they depend on. Each read thus adds only the merge
// keys of its own mapping.
func (b *aliasBinder) mergedAnchors(mapping *ast.MappingNode) *mergedRead {
	if anchors, ok := b.merged[mapping]; ok {
		return &mergedRead{anchors: anchors, final: true}
	}

	if b.open[mapping] {
		b.watched[mapping] = true

		return &mergedRead{anchors: anchorSet{}, mapping: mapping, open: true}
	}

	if read, ok := b.pending[mapping]; ok && b.holds(read) {
		return read
	}

	b.setOpen(mapping, true)

	r := &anchorReader{
		binder:  b,
		anchors: newAnchorSet(),
		unbound: map[*ast.AliasNode]bool{},
		final:   true,
	}
	ast.Walk(r, mapping)

	b.setOpen(mapping, false)

	read := &mergedRead{
		anchors: r.anchors,
		mapping: mapping,
		unbound: r.unbound,
		reads:   r.reads,
		final:   r.final,
	}

	if read.final {
		b.merged[mapping] = read.anchors
		b.touch(mapping)

		return read
	}

	// The result depends on mapping staying closed, whatever the reads
	// inside it found, and on its unbound aliases staying unbound.
	b.watched[mapping] = true
	for alias := range read.unbound {
		b.watched[alias] = true
	}

	b.pending[mapping] = read

	return read
}

// holds reports whether read still holds, as [mergedRead] describes. The
// read argument is a result of [aliasBinder.mergedAnchors] that is not
// final.
//
// Every mapping and alias that a result depends on is in the watched set,
// so a result that held at the current version still holds. The checked
// and held fields of each result keep what the last check found. A chain
// of results that merge the ones before them thus costs one check for each
// result at each version.
func (b *aliasBinder) holds(read *mergedRead) bool {
	if read.checked == b.version {
		return read.held
	}

	_, final := b.merged[read.mapping]
	held := !final && b.open[read.mapping] == read.open

	for alias := range read.unbound {
		if _, ok := b.targets[alias]; ok {
			held = false
		}
	}

	for _, sub := range read.reads {
		if !held {
			break
		}

		held = b.holds(sub)
	}

	read.checked, read.held = b.version, held

	return held
}

// anchorReader collects the anchors that [aliasBinder.mergedAnchors]
// returns while [ast.Walk] visits a mapping. The anchors set holds the
// anchors that the reader has recorded so far, and the final field reports
// whether the result holds for every merge. The unbound set and the reads
// slice hold the state that the result depends on, as [mergedRead]
// describes.
type anchorReader struct {
	binder  *aliasBinder
	anchors anchorSet
	unbound map[*ast.AliasNode]bool
	reads   []*mergedRead
	final   bool
}

// Visit records an anchor, or at a `<<` merge key the anchors of the
// mappings it merges. It walks the content of an anchor itself and records
// the anchor again after it, as [aliasBinder.Visit] does.
//
// Visit returns nil for an anchor and for the entry of a merge key. It
// also returns nil for a nil node, including a typed nil that a hand-built
// tree might hold. It returns nil for an alias because the merge does not
// record any anchor from the content of an alias. It returns r for any
// other node, so [ast.Walk] continues into the children of that node.
func (r *anchorReader) Visit(node ast.Node) ast.Visitor {
	if astnode.IsNil(node) {
		return nil
	}

	switch n := node.(type) {
	case *ast.AnchorNode:
		name := astnode.Token(n.Name)
		r.anchors.record(name, n)

		ast.Walk(r, n.Value)

		r.anchors.record(name, n)

		return nil

	case *ast.AliasNode:
		return nil

	case *ast.MappingValueNode:
		if astnode.IsMergeKey(n.Key) {
			r.merge(n.Value)

			return nil
		}
	}

	return r
}

// merge records the anchors a `<<` merge key with value brings in, in the
// order [aliasBinder.merge] records them.
func (r *anchorReader) merge(value ast.Node) {
	sources, unbound := r.binder.findSources(value, r.anchors.nodes, false)

	for _, alias := range unbound {
		r.unbound[alias] = true
		r.final = false
	}

	for _, src := range sources {
		read := r.binder.mergedAnchors(src.mapping)
		r.anchors.add(read.anchors)

		if !read.final {
			r.reads = append(r.reads, read)
			r.final = false
		}
	}
}

// walkOpen walks mapping and holds it open while the walk is inside it.
// The mapping is the content of an anchor or a mapping that a `<<` merge
// key holds inline.
func (b *aliasBinder) walkOpen(mapping *ast.MappingNode) {
	if !b.open[mapping] {
		b.setOpen(mapping, true)
		defer b.setOpen(mapping, false)
	}

	ast.Walk(b, mapping)
}

// anchoredMapping returns the mapping that an anchor's content holds, or
// nil when the content is not a mapping. It looks through tags.
func anchoredMapping(content ast.Node) *ast.MappingNode {
	for !astnode.IsNil(content) {
		switch n := content.(type) {
		case *ast.TagNode:
			content = n.Value
		case *ast.MappingNode:
			return n
		default:
			return nil
		}
	}

	return nil
}

// deref looks through anchors and aliases to the content node they carry.
// It returns the first tag it reaches unchanged, because the tag applies
// to the content under it. The anchors and aliases under the tag stay in
// the result.
//
// Returns an error wrapping [ErrAlias] for an alias that does not have an
// anchor of its name before it, or for an alias that leads back to itself,
// as [resolver.follow] describes. It also checks the aliases under the
// tag, which [resolver.unwrap] follows. An alias with a tag on it thus
// fails the same way as one without a tag.
func (r *resolver) deref(node ast.Node) (ast.Node, error) {
	tagged, _, err := r.content(node)

	return tagged, err
}

// unwrap does what [resolver.deref] does and then strips tags, so the
// result is a mapping, sequence, or scalar that selectors can apply to. It
// stops at a nil node, including a typed nil that a hand-built tree might
// hold. It returns an untyped nil for that node, as [resolver.follow] does.
func (r *resolver) unwrap(node ast.Node) (ast.Node, error) {
	_, content, err := r.content(node)

	return content, err
}

// content looks through anchors, aliases, and tags from node. It returns
// two nodes. The first is the first tag it reaches, or the content when it
// does not reach a tag. The second is the content under every tag.
// [resolver.deref] gives the first node and [resolver.unwrap] gives the
// second.
//
// It tracks the aliases it follows across every tag it looks through. For
// an alias that leads back to itself through a tag, content thus returns
// an error wrapping [ErrAlias].
func (r *resolver) content(node ast.Node) (ast.Node, ast.Node, error) {
	followed := map[*ast.AliasNode]bool{}

	tagged, err := r.follow(node, followed)
	if err != nil {
		return nil, nil, err
	}

	content := tagged

	for {
		tag, ok := content.(*ast.TagNode)
		if !ok {
			return tagged, content, nil
		}

		content, err = r.follow(tag.Value, followed)
		if err != nil {
			return nil, nil, err
		}
	}
}

// follow looks through anchors and aliases from node and adds each alias it
// follows to followed. It returns an error wrapping [ErrAlias] for an
// alias that leads back to itself, that does not have an anchor, or that
// does not have a name. An alias leads back to itself when it is already
// in followed. It also leads back to itself when it lies inside the
// content of the anchor it refers to, unless a `<<` merge key names it.
// Follow stops at a nil node, including a typed nil that a hand-built tree
// might hold. It returns an untyped nil for that node, so a type assertion
// on the result never yields a nil pointer.
func (r *resolver) follow(node ast.Node, followed map[*ast.AliasNode]bool) (ast.Node, error) {
	for {
		if astnode.IsNil(node) {
			return nil, nil //nolint:nilnil // Nil content is a valid result.
		}

		switch n := node.(type) {
		case *ast.AnchorNode:
			node = n.Value
		case *ast.AliasNode:
			tk := astnode.Token(n.Value)
			if tk == nil {
				return nil, fmt.Errorf("%w: alias has no name", ErrAlias)
			}

			name := tk.Value

			if followed[n] || r.enclosed[n] {
				return nil, fmt.Errorf("%w: *%s forms a cycle", ErrAlias, name)
			}

			followed[n] = true

			target, ok := r.targets[n]
			if !ok {
				return nil, r.missingAnchor(name)
			}

			node = target.Value

		default:
			return node, nil
		}
	}
}

// missingAnchor returns the error of an alias named name that does not
// have an anchor of its name before it. The error wraps [ErrAlias]. It
// wraps [ErrReferenceAlias] too when a reference document defines such an
// anchor, as [WithReferenceAnchors] reports.
func (r *resolver) missingAnchor(name string) error {
	if r.references != nil && r.references(name) {
		return &referenceAliasError{name: name}
	}

	return fmt.Errorf("%w: *%s has no anchor before it", ErrAlias, name)
}

// referenceAliasError is the error of an alias whose anchor a reference
// document defines. It reads as [ErrReferenceAlias] and the name of the
// alias. It wraps [ErrAlias] and matches ErrReferenceAlias, so a tree of
// problems shows it as one problem rather than as one for each sentinel.
type referenceAliasError struct {
	name string
}

func (e *referenceAliasError) Error() string {
	return ErrReferenceAlias.Error() + ": *" + e.name
}

// Unwrap returns [ErrAlias].
func (e *referenceAliasError) Unwrap() error {
	return ErrAlias
}

// Is reports whether target is [ErrReferenceAlias].
func (e *referenceAliasError) Is(target error) bool {
	return target == ErrReferenceAlias
}

// resolve applies segs to root and returns every match, in document order
// along the path.
//
// A recursive selector can reach one entry or element from several
// matches, such as when one match lies inside another or two aliases share
// an anchor. Resolve keeps the first match of each, so a recursive
// selector yields each entry or element once. Only a recursive selector
// drops repeats, so a later `.name`, `.*`, `[n]`, or `[*]` selector can
// still reach one node through several aliases.
//
// Returns [ErrExcessiveAliasing] when the matches of one selector pass the
// limit that [resolver.excessive] applies. Resolve checks the limit after
// it applies the selector to each match. A `[*]` thus lists at most one
// sequence past the limit before it stops, and a `.*` at most one mapping.
// Returns [ErrExcessiveMerging] when the key lookups of one selector read
// past the limit that [resolver.excessiveMerging] applies, as
// [resolver.lookup] describes.
func (r *resolver) resolve(root ast.Node, segs []segment) ([]match, error) {
	matches := []match{{node: root}}

	for _, seg := range segs {
		var (
			next  []match
			reads lookupReads
		)

		switch seg.kind {
		case segmentKey:
			for _, m := range matches {
				next = append(next, m.key())
			}

		case segmentRecursive, segmentRecursiveAll:
			found, err := r.recurse(matches, seg)
			if err != nil {
				return nil, err
			}

			next = found

		default:
			for _, m := range matches {
				found, err := r.apply(seg, m, &reads)
				if err != nil {
					return nil, err
				}

				next = append(next, found...)

				if r.excessive(len(next)) {
					return nil, ErrExcessiveAliasing
				}
			}
		}

		// A selector applied to a match that lies inside another can
		// reach a node before the ones it reaches from the outer match.
		slices.SortStableFunc(next, func(a, b match) int {
			return slices.Compare(a.order, b.order)
		})

		if seg.kind == segmentRecursive || seg.kind == segmentRecursiveAll {
			next = uniqueMatches(next)
		}

		matches = next
	}

	return matches, nil
}

// excessive reports whether count matches of one selector pass the alias
// limit of the document. Without aliases, a selector reaches each node of
// the document at most one time. Only aliases can thus take count past the
// number of nodes in the document. [aliaslimit.Excessive] weighs the
// matches past that number as the nodes that aliases repeat in a decode.
func (r *resolver) excessive(count int) bool {
	return aliaslimit.Excessive(r.nodes, max(count-r.nodes, 0))
}

// lookupReads holds what the key lookups of one selector, or of one
// [EntryFinder], share. The count field counts the nodes under `<<` merge
// keys that those lookups have read, and [resolver.excessiveMerging]
// weighs that count. The sources field is a stack of the merge sources
// that the lookups in progress have yet to search. [resolver.lookup]
// pushes the sources of each merge key it reads and pops them after it
// searches them. Those lookups thus share one buffer rather than each
// allocating one for every merge key it reads.
type lookupReads struct {
	sources []*ast.MappingNode
	count   int
}

// These constants limit the nodes under `<<` merge keys that the key
// lookups of one selector or of one [EntryFinder] can read.
const (
	// The lookups can read this many times the number of nodes in the
	// document.
	mergeReadFactor = 64

	// The lookups can read this many nodes in any document, however few
	// nodes it holds.
	mergeReadFloor = 1 << 19
)

// excessiveMerging reports whether reads pass the limit for the document.
// The reads argument counts the nodes under `<<` merge keys that the key
// lookups of one selector or of one [EntryFinder] have read.
//
// Each lookup reads the merge chain behind its mapping again, so those
// lookups can read many times the number of nodes in the document. When a
// document grows but its mappings and merge chains keep their size, reads
// stay a fixed multiple of its nodes. The limit is a fixed multiple of
// those nodes too, so a document that grows that way stays within it. A
// document passes the limit when the chain that each lookup reads grows
// with the document. Reads then grow with the square of its nodes.
func (r *resolver) excessiveMerging(reads int) bool {
	return reads > max(mergeReadFloor, mergeReadFactor*r.nodes)
}

// uniqueMatches returns the first match of each entry or element, in the
// order matches holds them. It compares the entries, and the sequence and
// index of the elements, rather than their values. Two entries or two
// elements of a hand-built tree thus count as two matches when they share
// one value node or both hold nil.
func uniqueMatches(matches []match) []match {
	seen := make(map[matchID]bool, len(matches))
	unique := make([]match, 0, len(matches))

	for _, m := range matches {
		id := matchID{entry: m.entry}
		if m.seq != nil {
			id.seq, id.index = m.seq, m.index
		}

		if seen[id] {
			continue
		}

		seen[id] = true

		unique = append(unique, m)
	}

	return unique
}

// matchID identifies the entry or the element that a recursive selector
// found, for [uniqueMatches]. The ID of an entry sets the entry field
// alone. The ID of an element sets the seq field to the sequence that
// holds the element and the index field to its index there.
//
//nolint:unused // The fields tell the keys of the set apart.
type matchID struct {
	entry *ast.MappingValueNode
	seq   *ast.SequenceNode
	index int
}

// apply applies one `.name`, `.*`, `[n]`, or `[*]` selector to the node of
// m and returns the matches. Each match holds the selectors of m followed
// by the selector that names that match. A `.name` adds the nodes that its
// lookup reads under merge keys to the count of reads, as [resolver.lookup]
// describes. A `.*` adds the nodes that [resolver.entries] reads. Apply
// does not select anything from a nil node, including a typed nil.
func (r *resolver) apply(seg segment, m match, reads *lookupReads) ([]match, error) {
	content, err := r.unwrap(m.node)
	if err != nil || astnode.IsNil(content) {
		return nil, err
	}

	switch seg.kind {
	case segmentChild:
		mapping, ok := content.(*ast.MappingNode)
		if !ok {
			return nil, nil
		}

		entry, i, ok, err := r.lookup(mapping, seg.name, nil, reads)
		if err != nil || !ok {
			return nil, err
		}

		return []match{m.with(entry.Value, entry, seg, i)}, nil

	case segmentChildAll:
		mapping, ok := content.(*ast.MappingNode)
		if !ok {
			return nil, nil
		}

		return r.entries(mapping, m, reads)

	case segmentIndex:
		seq, ok := content.(*ast.SequenceNode)
		if !ok || seg.index >= len(seq.Values) {
			return nil, nil
		}

		return []match{m.element(seq, seg.index)}, nil

	case segmentIndexAll:
		seq, ok := content.(*ast.SequenceNode)
		if !ok {
			return nil, nil
		}

		// Clipped, m has no spare capacity, so each element copies its
		// selectors and its order rather than sharing one backing array.
		m.segs, m.order = slices.Clip(m.segs), slices.Clip(m.order)

		matches := make([]match, 0, len(seq.Values))
		for i := range seq.Values {
			matches = append(matches, m.element(seq, i))
		}

		return matches, nil

	default:
		return nil, nil
	}
}

// entries applies the `.*` selector to mapping, the content of the node of
// m. It returns a match for each entry that a `.name` selector resolves in
// mapping, one for each name. Each match thus holds the entry that
// [resolver.lookup] finds for its name, at the place that lookup gives it.
// Of two entries with one key, that is the later entry. An entry that a
// `<<` merge key brings in takes the place of that merge key. Each match
// holds the selectors of m followed by the `.name` selector for its name.
//
// A merge key is a source of entries rather than an entry, so entries
// leaves out a merge key that lookup finds for the name `<<`. A lookup
// finds an entry only when its key has a name, as [resolver.keyName]
// reports it.
//
// The matches come in the order of their places. The matches at the place
// of one merge key come in the order that [resolver.mergedNames] lists
// their names. To get that order, entries reads the keys of mapping in
// turn. It looks up each name it reads there, or under a merge key, the
// first time it reads that name. It lists the entry that lookup found when
// it reaches the place of that entry.
//
// Entries adds the nodes that mergedNames and each lookup read under merge
// keys to the count of reads. It returns [ErrExcessiveMerging] when that
// count passes the limit that [resolver.excessiveMerging] applies. It
// returns the error that mergedNames gives for a merge source that does
// not resolve.
func (r *resolver) entries(mapping *ast.MappingNode, m match, reads *lookupReads) ([]match, error) {
	// Clipped, m has no spare capacity, so each entry copies its selectors
	// and its order rather than sharing one backing array.
	m.segs, m.order = slices.Clip(m.segs), slices.Clip(m.order)

	var names []string

	matches := make([]match, 0, len(mapping.Values))
	placed := make(map[string]placedEntry, len(mapping.Values))

	for i, entry := range mapping.Values {
		if entry == nil {
			continue
		}

		names = names[:0]

		if astnode.IsMergeKey(entry.Key) {
			var err error

			// A lookup in mapping does not search mapping again when a
			// merge leads back to it, so the names of mapping are not
			// names this merge key brings in.
			seen := map[*ast.MappingNode]bool{mapping: true}

			names, err = r.mergedNames(names, entry.Value, seen, reads)
			if err != nil {
				return nil, err
			}
		} else if name, ok := r.keyName(entry.Key); ok {
			names = append(names, name)
		}

		for _, name := range names {
			pe, ok := placed[name]
			if !ok {
				e, place, has, err := r.lookup(mapping, name, nil, reads)
				if err != nil {
					return nil, err
				}

				if has && !astnode.IsMergeKey(e.Key) {
					pe = placedEntry{entry: e, place: place}
				}

				placed[name] = pe
			}

			if pe.entry == nil || pe.place != i {
				continue
			}

			// One merge key may name a key more than once, so the name
			// holds no entry once entries lists it.
			placed[name] = placedEntry{}

			matches = append(matches, m.with(pe.entry.Value, pe.entry, segment{kind: segmentChild, name: name}, i))
		}
	}

	return matches, nil
}

// placedEntry is the entry that [resolver.lookup] finds for one name in the
// mapping that [resolver.entries] lists, with the place of that entry in
// the mapping. The zero value stands for a name that does not have an
// entry to list.
type placedEntry struct {
	entry *ast.MappingValueNode
	place int
}

// mergedNames appends to names the name of each key that one `<<` merge
// key brings in, and returns the result. The value argument is the value
// of that merge key.
//
// The names come in the order of the sources of the merge key. The names
// of one source come in the order of its keys, as [resolver.keyName] gives
// them. A merge key of a source adds the names it brings in at its own
// place. The names thus cover every key that a lookup can find under
// value. One name might appear more than once. MergedNames skips a key
// that does not have a name.
//
// The seen set holds each mapping that mergedNames has read. MergedNames
// thus reads each source one time, even when several merge keys under
// value bring it in, and a cycle of merges ends. MergedNames adds the
// nodes it reads under merge keys to the count of reads, as
// [resolver.mergeSources] counts them. It returns [ErrExcessiveMerging]
// when that count passes the limit that [resolver.excessiveMerging]
// applies. When an alias that a merge key names does not resolve, the
// merge might bring in any key, so mergedNames returns an error wrapping
// [ErrAlias]. MergedNames leaves the sources stack of reads at the length
// it found it, unless it returns an error.
func (r *resolver) mergedNames(
	names []string, value ast.Node, seen map[*ast.MappingNode]bool, reads *lookupReads,
) ([]string, error) {
	if r.excessiveMerging(reads.count) {
		return nil, ErrExcessiveMerging
	}

	top := len(reads.sources)

	sources, read, err := r.mergeSources(reads.sources, value)

	reads.sources = sources
	reads.count += read

	if r.excessiveMerging(reads.count) {
		return nil, ErrExcessiveMerging
	}

	if err != nil {
		return nil, err
	}

	// Reading the names of one source pushes the sources of its merge keys
	// and may move the stack to a larger array, so the loop reads each
	// source from the stack of reads rather than from sources.
	for i := top; i < len(sources); i++ {
		src := reads.sources[i]
		if seen[src] {
			continue
		}

		seen[src] = true

		for _, entry := range src.Values {
			switch {
			case entry == nil:
			case astnode.IsMergeKey(entry.Key):
				names, err = r.mergedNames(names, entry.Value, seen, reads)
				if err != nil {
					return nil, err
				}

			default:
				if name, ok := r.keyName(entry.Key); ok {
					names = append(names, name)
				}
			}
		}
	}

	reads.sources = reads.sources[:top]

	return names, nil
}

// lookup finds the entry for name in mapping. It looks through the `<<`
// merge keys of mapping as well as its own entries. The later entry in
// document order wins, whether it is an entry of the mapping itself or a
// merge key whose sources hold that key. A later merge source also wins
// over an earlier one. The goccy/go-yaml decoder keeps the value of that
// entry. It sets the entries of a mapping in order, and it sets the keys
// of the merge sources where the merge key stands.
//
// The seen set guards against merge cycles through aliases, so one lookup
// reads each mapping at most one time. It still reads the whole value of
// each merge key it reaches. A sequence of sources that many mappings
// merge through an alias thus costs its length one time for each of them.
// Lookup adds the nodes it reads under merge keys to the count of reads,
// as [resolver.mergeSources] counts them. It returns [ErrExcessiveMerging]
// when that count passes the limit that [resolver.excessiveMerging]
// applies. A caller that shares reads among many lookups thus weighs them
// together against that limit.
//
// When the count has already passed the limit, lookup returns the error at
// the first merge key it reaches, before it reads the value of that key.
// Lookup adds the nodes it read even when it returns an error for an alias
// that does not resolve. A caller that ignores the error thus still weighs
// them. Lookup leaves the sources stack of reads at the length it found
// it.
//
// The int result is the index in mapping of the entry that lookup found.
// For an entry that a merge source holds, it is the index of the `<<`
// entry that brings that entry in. The bool result reports whether lookup
// found an entry.
func (r *resolver) lookup(
	mapping *ast.MappingNode, name string, seen map[*ast.MappingNode]bool, reads *lookupReads,
) (*ast.MappingValueNode, int, bool, error) {
	keys := r.mappingKeys(mapping)

	// The names index may hold the last merge key under `<<`. The name
	// `<<` selects that merge key only when neither the mapping nor a
	// merge source holds a real key with that text, so lookup searches
	// every merge key before it settles on that one.
	own, hasOwn := keys.names[name]
	realKey := hasOwn && !astnode.IsMergeKey(mapping.Values[own].Key)

	// A real key of the mapping with no merge key after it wins outright.
	if realKey && !keys.mergeAfter(own) {
		return mapping.Values[own], own, true, nil
	}

	if seen == nil {
		seen = map[*ast.MappingNode]bool{}
	}

	seen[mapping] = true

	// A later merge key wins over an earlier one, as a later source in one
	// merge key does, so lookup reads the merge keys from the last one back.
	// It stops at a real key of the mapping, which wins over every merge
	// key before it.
	for _, i := range slices.Backward(keys.merges) {
		if realKey && i < own {
			break
		}

		top := len(reads.sources)

		found, ok, err := r.lookupMerge(mapping.Values[i].Value, name, seen, reads)

		reads.sources = reads.sources[:top]

		if err != nil {
			return nil, 0, false, err
		}

		if ok {
			return found, i, true, nil
		}
	}

	if hasOwn {
		return mapping.Values[own], own, true, nil
	}

	return nil, 0, false, nil
}

// lookupMerge finds the entry for name that the sources of one `<<` merge
// key bring in. The value argument is the value of that merge key.
// LookupMerge pushes the sources onto the stack of reads and leaves them
// there for [resolver.lookup] to pop. A later source wins over an earlier
// one, so lookupMerge searches them from the last one back. It adds to the
// count of reads and returns [ErrExcessiveMerging] as lookup describes.
// The bool result reports whether lookupMerge found an entry.
func (r *resolver) lookupMerge(
	value ast.Node, name string, seen map[*ast.MappingNode]bool, reads *lookupReads,
) (*ast.MappingValueNode, bool, error) {
	// A caller that goes on after the error would otherwise read the
	// whole value again on each lookup past the limit.
	if r.excessiveMerging(reads.count) {
		return nil, false, ErrExcessiveMerging
	}

	top := len(reads.sources)

	sources, read, err := r.mergeSources(reads.sources, value)

	reads.sources = sources
	reads.count += read

	if r.excessiveMerging(reads.count) {
		return nil, false, ErrExcessiveMerging
	}

	if err != nil {
		return nil, false, err
	}

	// A lookup in one source pushes its own sources and may move the stack
	// to a larger array, so the loop reads each source from the stack of
	// reads rather than from sources.
	for i := len(sources) - 1; i >= top; i-- {
		src := reads.sources[i]
		if seen[src] {
			continue
		}

		found, _, ok, err := r.lookup(src, name, seen, reads)
		if err != nil {
			return nil, false, err
		}

		// A merge key in a source is not a key the merge brings in.
		if ok && !astnode.IsMergeKey(found.Key) {
			return found, true, nil
		}
	}

	return nil, false, nil
}

// overridden reports whether a path through name in mapping selects an
// entry other than the one at index i, the last entry of mapping with that
// name. A `<<` key after that entry can win over it, as [resolver.lookup]
// describes. When the entry at i is the last merge key, a real `<<` key
// that one of the merge sources holds wins over it too. When those merge
// keys do not resolve, a path through name does not select any entry, so
// overridden also reports true. Its lookup adds to the count of reads as
// [resolver.lookup] describes. Overridden returns the [ErrExcessiveMerging]
// that lookup returns.
func (r *resolver) overridden(
	mapping *ast.MappingNode, keys *mappingKeys, name string, i int, reads *lookupReads,
) (bool, error) {
	entry := mapping.Values[i]
	if !keys.mergeAfter(i) && !astnode.IsMergeKey(entry.Key) {
		return false, nil
	}

	// Lookup gives the index of the merge key for an entry that a merge
	// source holds, so overridden compares the entries.
	found, _, ok, err := r.lookup(mapping, name, nil, reads)
	if errors.Is(err, ErrExcessiveMerging) {
		return false, err
	}

	selects := err == nil && ok && found == entry

	return !selects, nil
}

// mergeSources appends to buf the mappings that a `<<` value merges in,
// and returns the buffer. The mappings are the value itself when it is a
// mapping, or each mapping element when it is a sequence. MergeSources
// skips a nil node, including a typed nil.
//
// The int result counts the nodes that mergeSources reads. It counts one
// for the value, and one more for each element when the value is a
// sequence, whether or not that element is a mapping. When the value or
// an element is an alias that does not resolve, the count ends with that
// node. The buffer then holds the sources before that node.
func (r *resolver) mergeSources(buf []*ast.MappingNode, value ast.Node) ([]*ast.MappingNode, int, error) {
	content, err := r.unwrap(value)
	if err != nil {
		return buf, 1, err
	}

	if astnode.IsNil(content) {
		return buf, 1, nil
	}

	switch n := content.(type) {
	case *ast.MappingNode:
		return append(buf, n), 1, nil
	case *ast.SequenceNode:
		// Making room for the whole sequence costs its length, even when
		// an early element does not resolve and stops the count there. The
		// buffer keeps the room, so a caller that passes one buffer to
		// every call pays that cost once, not once for each call.
		buf = slices.Grow(buf, len(n.Values))

		for i, v := range n.Values {
			elem, err := r.unwrap(v)
			if err != nil {
				return buf, i + 2, err
			}

			if m, ok := elem.(*ast.MappingNode); ok && m != nil {
				buf = append(buf, m)
			}
		}

		return buf, 1 + len(n.Values), nil

	default:
		return buf, 1, nil
	}
}

// recurse applies seg, a `..name` or `..*` selector, to each of matches in
// order. It returns every node it finds below each match, in the order it
// finds them. It returns an error wrapping [ErrAlias] when the node of a
// match is an alias that does not resolve. It returns
// [ErrExcessiveMerging] when the key lookups of the walk read too many
// nodes under merge keys, as [recursiveWalk.descend] describes.
//
// A walk from one match can reach the start of another, such as when one
// match lies inside another. Take an earlier walk that reaches the start
// of a later match at an order that [covers] the order of that match.
// Entries below the start never sort earlier from the later walk than from
// the earlier one, so resolve keeps the entry that the earlier walk finds.
// Hence recurse skips a later match whose start an earlier walk covers. A
// walk also stops at the start of an earlier match whose order covers the
// walk there. On a chain of nested matches the first walk thus covers the
// rest, and the step costs one walk rather than one for each match.
func (r *resolver) recurse(matches []match, seg segment) ([]match, error) {
	w := &recursiveWalk{
		resolver: r,
		name:     seg.name,
		all:      seg.kind == segmentRecursiveAll,
		from:     matches,
		starts:   make(map[ast.Node][]int, len(matches)),
		skip:     make([]bool, len(matches)),
	}

	contents := make([]ast.Node, len(matches))

	for i, m := range matches {
		content, err := r.unwrap(m.node)
		if err != nil {
			return nil, err
		}

		contents[i] = content

		if !astnode.IsNil(content) {
			w.starts[content] = append(w.starts[content], i)
		}
	}

	for i, m := range matches {
		if w.skip[i] || astnode.IsNil(contents[i]) {
			continue
		}

		// Clipped, the selectors and the order of the match have no
		// spare capacity, so the first push copies them rather than
		// writing past the end of the match.
		w.cur = i
		w.segs = slices.Clip(m.segs)
		w.order = slices.Clip(m.order)
		w.heldSegs, w.heldOrder = 0, 0

		// A match of `.'<<'` or `..'<<'` starts at the sources of a merge
		// key.
		sources := m.entry != nil && astnode.IsMergeKey(m.entry.Key)

		err := w.descend(contents[i], sources)
		if err != nil {
			return nil, err
		}
	}

	return w.found, nil
}

// covers reports whether a walk that reaches a node at order a finds each
// entry below the node no later than a walk that reaches it at order b
// does. Covers reports true when the orders are equal, or when they first
// differ at a place both hold and a is lower there. When one order
// extends the other, the entry decides which walk finds it first, so
// covers reports false.
func covers(a, b []int) bool {
	for i := range min(len(a), len(b)) {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}

	return len(a) == len(b)
}

// recursiveWalk walks the subtrees of the matches that one `..name` or
// `..*` selector applies to, as [resolver.recurse] describes. The name
// field holds the name of a `..name` selector, and the all field reports
// whether the selector is `..*`.
//
// The from field holds those matches, and the starts field maps the
// content each one starts from to its indexes in from. The cur field is
// the index of the current match, and the skip field marks the matches
// that an earlier walk covers.
//
// The segs and order stacks hold the selectors and the order down to the
// node the walk has reached. Each entry that the walk finds holds the
// stacks as they stand. The held fields record how much of each stack the
// entries hold, so a push to a held place copies the stack first. Entries
// along one branch thus share their selectors and their order rather than
// holding a copy each.
//
// The reads field holds the [lookupReads] of the key lookups of the walk,
// across the walks of every match.
type recursiveWalk struct {
	resolver  *resolver
	starts    map[ast.Node][]int
	name      string
	from      []match
	skip      []bool
	found     []match
	segs      []segment
	order     []int
	reads     lookupReads
	cur       int
	heldSegs  int
	heldOrder int
	all       bool
}

// descend collects every mapping entry keyed name at any depth below node,
// in document order. For a `..*` selector it collects every entry and
// every element of a sequence instead, each before the nodes below it. It
// looks through anchors and tags but not aliases, so it visits an entry of
// the source at most one time, at its definition. It skips a mapping or
// sequence that the walk of another match covers, as [resolver.recurse]
// describes.
//
// The sources flag reports whether node is the value of a `<<` merge key. A
// `..*` selector leaves out the entry of a merge key and each element of a
// sequence that lists the sources of one. Those are sources of entries
// rather than entries. Descend still walks them, so it lists the entries
// of a mapping written inline there.
//
// For each entry and element it visits, descend pushes the selector and
// the place of that step onto the stacks. Before it returns, descend cuts
// both stacks back to the length they had when it began.
//
// It visits only the entry that a path through its key selects, and skips
// every other entry of that key and everything below it. When a mapping
// holds several `<<` keys, descend thus visits only the inline mapping of
// the last one, even though the decoder merges them all. A mapping can
// hold a real key whose text is `<<` next to a merge key, or a merge
// source can bring in such a key. A path through `<<` then selects the
// real key, so descend skips the merge key. When a later `<<` key brings
// in the key of an entry, a path through that key selects the merged
// entry, so descend skips the entry of the mapping itself. A path selects
// an entry only when its key has a name. Descend thus also skips an entry
// whose key does not have a name, and everything below that entry.
//
// To find the entries that a `<<` key overrides, descend looks up the key
// of each entry that has a `<<` key after it, and of the last merge key
// itself. Each lookup reads the values of the merge keys it searches and
// the mappings they bring in. Returns [ErrExcessiveMerging] when the
// lookups of the walk read past the limit that [resolver.excessiveMerging]
// applies.
func (w *recursiveWalk) descend(node ast.Node, sources bool) error {
	if astnode.IsNil(node) {
		return nil
	}

	segsDepth, orderDepth := len(w.segs), len(w.order)

	switch n := node.(type) {
	case *ast.MappingNode:
		if w.covered(n) {
			return nil
		}

		keys := w.resolver.mappingKeys(n)

		for i, entry := range n.Values {
			if entry == nil {
				continue
			}

			key, _ := w.resolver.keyName(entry.Key)
			if idx, ok := keys.names[key]; !ok || idx != i {
				continue
			}

			overridden, err := w.resolver.overridden(n, keys, key, i, &w.reads)
			if err != nil {
				return err
			}

			if overridden {
				continue
			}

			w.push(segsDepth, orderDepth, segment{kind: segmentChild, name: key}, i)

			if w.lists(entry, key) {
				segs, order := w.hold()
				w.found = append(w.found, match{node: entry.Value, entry: entry, segs: segs, order: order})
			}

			err = w.descend(entry.Value, astnode.IsMergeKey(entry.Key))
			if err != nil {
				return err
			}
		}

	case *ast.SequenceNode:
		if w.covered(n) {
			return nil
		}

		for i, v := range n.Values {
			w.push(segsDepth, orderDepth, segment{kind: segmentIndex, index: i}, i)

			if w.all && !sources {
				segs, order := w.hold()
				w.found = append(w.found, match{node: v, seq: n, index: i, segs: segs, order: order})
			}

			err := w.descend(v, false)
			if err != nil {
				return err
			}
		}

	case *ast.AnchorNode:
		return w.descend(n.Value, sources)
	case *ast.TagNode:
		return w.descend(n.Value, sources)
	}

	w.segs = w.segs[:segsDepth]
	w.order = w.order[:orderDepth]

	return nil
}

// lists reports whether the walk lists entry, whose key has the name key.
// A `..name` selector lists an entry with that name, and a `..*` selector
// lists every entry other than a `<<` merge key.
func (w *recursiveWalk) lists(entry *ast.MappingValueNode, key string) bool {
	if w.all {
		return !astnode.IsMergeKey(entry.Key)
	}

	return key == w.name
}

// covered reports whether the walk of an earlier match covers node, so
// the current walk leaves out everything below it, as [resolver.recurse]
// describes. It marks each later match that starts at node and that the
// current walk covers, so recurse skips it.
func (w *recursiveWalk) covered(node ast.Node) bool {
	for _, j := range w.starts[node] {
		switch {
		case j < w.cur && covers(w.from[j].order, w.order):
			return true
		case j > w.cur && covers(w.order, w.from[j].order):
			w.skip[j] = true
		}
	}

	return false
}

// push sets the selector and the order place of one step onto the stacks,
// at the given depths. It copies a stack first when the place it sets is
// one that an entry holds.
func (w *recursiveWalk) push(segsDepth, orderDepth int, seg segment, ord int) {
	if segsDepth < w.heldSegs {
		w.segs = slices.Clone(w.segs[:segsDepth])
		w.heldSegs = 0
	}

	if orderDepth < w.heldOrder {
		w.order = slices.Clone(w.order[:orderDepth])
		w.heldOrder = 0
	}

	w.segs = append(w.segs[:segsDepth], seg)
	w.order = append(w.order[:orderDepth], ord)
}

// hold returns the stacks as they stand, for an entry that the walk finds,
// and records that an entry holds them. Hold clips the results to their
// length, so an append to one copies it.
func (w *recursiveWalk) hold() ([]segment, []int) {
	w.heldSegs = max(w.heldSegs, len(w.segs))
	w.heldOrder = max(w.heldOrder, len(w.order))

	return slices.Clip(w.segs), slices.Clip(w.order)
}

// keyName returns the key text that a child selector compares against. A
// string key gives its unquoted text. A literal or folded block scalar key
// gives its content, as the decoder reads it. Any other key gives its
// source text. An alias key gives the name that the content of its anchor
// gives as a key, whether or not the alias has a tag.
//
// The bool result is false for a key that does not have content, or that
// has content that a selector cannot name, such as a sequence. It is also
// false for an alias key that does not have an anchor before it, or one
// that leads back to itself. Such a key does not have a name, so keyName
// gives the empty string for it, and a child selector never matches it.
func (r *resolver) keyName(key ast.Node) (string, bool) {
	content := astnode.Content(key)

	if alias, ok := content.(*ast.AliasNode); ok {
		target, err := r.unwrap(alias)
		if err != nil || astnode.IsNil(target) {
			return "", false
		}

		content = target
	}

	switch k := content.(type) {
	case nil:
		return "", false
	case *ast.StringNode:
		return k.Value, true
	case *ast.LiteralNode:
		if k.Value == nil {
			return "", false
		}

		return k.Value.Value, true

	case ast.MapKeyNode:
		tk := astnode.Token(k)
		if tk == nil {
			return "", false
		}

		return tk.Value, true

	default:
		return "", false
	}
}
