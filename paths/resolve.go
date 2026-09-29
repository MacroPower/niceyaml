package paths

import (
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
)

// match is one node a path resolved to. The entry field holds the mapping
// entry that holds the node when the last selector picked a mapping key.
// The segs field holds the selectors that name the node alone: the ones
// applied so far, with a `[*]` replaced by the index it matched and a
// `..name` by the selectors down to the entry it found.
//
// The order field holds one place for each step of the walk that reached
// the node. A step into a mapping adds the index of the entry, a step into
// a sequence adds the index of the element, and a step to the key of an
// entry adds -1, so a key comes before its value. Comparing two orders
// puts the matches in document order along the path. An entry a `<<` merge
// key brings in takes the place of that merge key, and a node reached
// through an alias takes the place of the alias.
type match struct {
	node  ast.Node
	entry *ast.MappingValueNode
	segs  []segment
	order []int
}

// with returns a copy of m at node, with seg appended to its selectors and
// ord to its order. The copy owns its selectors and its order, so the
// matches of one `[*]` do not share a backing array.
func (m match) with(node ast.Node, entry *ast.MappingValueNode, seg segment, ord ...int) match {
	segs := make([]segment, 0, len(m.segs)+1)
	segs = append(segs, m.segs...)
	segs = append(segs, seg)

	order := make([]int, 0, len(m.order)+len(ord))
	order = append(order, m.order...)
	order = append(order, ord...)

	return match{node: node, entry: entry, segs: segs, order: order}
}

// key returns the match for the `~` selector applied to m. That is the key
// of the entry m holds, or m itself when m holds no entry or its entry has
// no key. A `~` on a sequence element or the root thus selects what the
// path before it does. The `~` joins the selectors either way, as the path
// wrote it.
func (m match) key() match {
	seg := segment{kind: segmentKey}

	if m.entry == nil {
		return m.with(m.node, nil, seg)
	}

	if key := keyContent(m.entry.Key); key != nil {
		return m.with(key, nil, seg, -1)
	}

	return m.with(m.node, m.entry, seg)
}

// resolver walks a document for the selectors of a [Path]. Its targets map
// holds the content of the anchor each alias refers to, and its owners map
// holds the anchor of each such content. The enclosed set holds each alias
// that lies inside the content of the anchor it refers to, other than one
// a `<<` merge key names. The decoder reads such an alias as null, so it
// has no content.
//
// The keys map holds the [*mappingKeys] of each mapping a lookup or a
// recursive walk has read, so a later lookup or walk in that mapping finds
// a key without reading its entries again. A [Resolver] may serve several
// goroutines, so the map is a [sync.Map].
//
// Create instances with [newResolver].
type resolver struct {
	targets  map[*ast.AliasNode]ast.Node
	owners   map[ast.Node]*ast.AnchorNode
	enclosed map[*ast.AliasNode]bool
	keys     sync.Map
}

// mappingKeys indexes the entries of one mapping for [resolver.lookup] and
// [recursiveWalk.descend]. The names map holds the index of the last entry
// with each key name, and the merges slice holds the index of each `<<`
// entry, in document order.
// A merge key and a real key whose text is `<<`, such as an alias key, are
// separate keys to the decoder, so names holds the last merge key under
// `<<` only when no real key has that name.
type mappingKeys struct {
	names  map[string]int
	merges []int
}

// mappingKeys returns the [*mappingKeys] of mapping, and reads its entries
// the first time a lookup or walk asks for them. Two goroutines that ask
// at once may each read the entries, and each gets the same index.
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

		if isMergeKey(entry.Key) {
			keys.merges = append(keys.merges, i)

			continue
		}

		name, _ := r.keyName(entry.Key)
		keys.names[name] = i
	}

	if n := len(keys.merges); n > 0 {
		last := keys.merges[n-1]
		name, _ := r.keyName(mapping.Values[last].Key)

		if _, ok := keys.names[name]; !ok {
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
// It binds each alias to the last anchor of its name before it in doc, which
// is the anchor the goccy/go-yaml decoder uses for that alias when it fills
// a map. An anchor counts from where it starts and again where its content
// ends, so it wins over an anchor of the same name inside that content for
// the aliases after it. The decoder reads a mapping a `<<` merge key brings
// in again at the merge key, and so does newResolver, so the anchors of
// that mapping count again there. An anchor on the value of a `<<` merge
// key, or on an element of a sequence there, counts for the aliases other
// merge keys name. It counts for an alias in a value only when no other
// anchor of its name comes before that alias, as [anchorSet] describes.
// Once every alias is bound, an [enclosureFinder] finds the aliases that
// lie inside the content of the anchor they refer to.
func newResolver(doc *ast.DocumentNode) *resolver {
	b := &aliasBinder{
		anchors: newAnchorSet(),
		targets: map[*ast.AliasNode]ast.Node{},
		owners:  map[ast.Node]*ast.AnchorNode{},
		merged:  map[*ast.MappingNode]anchorSet{},
		pending: map[*ast.MappingNode]*mergedRead{},
		open:    map[*ast.MappingNode]bool{},
		watched: map[ast.Node]bool{},
		merges:  map[*ast.AliasNode]bool{},
		version: 1,
	}

	ast.Walk(b, doc.Body)

	f := &enclosureFinder{
		targets:  b.targets,
		merges:   b.merges,
		open:     map[ast.Node]bool{},
		enclosed: map[*ast.AliasNode]bool{},
	}

	ast.Walk(f, doc.Body)

	return &resolver{targets: b.targets, owners: b.owners, enclosed: f.enclosed}
}

// enclosureFinder finds the aliases that lie inside the content of the
// anchor they refer to while [ast.Walk] visits a document. The targets
// map holds the content each alias refers to, and the merges set holds
// the aliases `<<` merge keys name, which the finder leaves out. The open
// set holds the content of each anchor the walk is inside of, and the
// enclosed set holds the aliases found so far.
type enclosureFinder struct {
	targets  map[*ast.AliasNode]ast.Node
	merges   map[*ast.AliasNode]bool
	open     map[ast.Node]bool
	enclosed map[*ast.AliasNode]bool
}

// Visit adds node to the enclosed set when it is an alias inside the
// content of the anchor it refers to, then returns f so [ast.Walk]
// continues into the children of node. It walks the content of an anchor
// itself, holding that content open meanwhile, and returns nil for it. It
// returns nil for a nil node, including a typed nil a hand-built tree may
// hold.
func (f *enclosureFinder) Visit(node ast.Node) ast.Visitor {
	if astnode.IsNil(node) {
		return nil
	}

	switch n := node.(type) {
	case *ast.AnchorNode:
		f.open[n.Value] = true
		ast.Walk(f, n.Value)
		delete(f.open, n.Value)

		return nil

	case *ast.AliasNode:
		if target, ok := f.targets[n]; ok && f.open[target] && !f.merges[n] {
			f.enclosed[n] = true
		}
	}

	return f
}

// anchorSet holds the anchors the decoder has recorded so far, in the two
// maps the decoder keeps. The nodes map holds the content of the last
// anchor of each name, and the values map holds the content of the last
// anchor of each name on a value the decoder reads. An anchor on the value
// of a `<<` merge key, or on an element of a sequence there, goes into the
// nodes map alone, since the decoder reads such a value only for the
// mappings it merges.
//
// An alias in a value refers to the anchor in the values map, and to the
// one in the nodes map when the values map holds none of its name. An
// alias a merge key names refers to the anchor in the nodes map.
//
// Create instances with [newAnchorSet].
type anchorSet struct {
	nodes  map[string]ast.Node
	values map[string]ast.Node
}

// newAnchorSet creates a new empty [anchorSet].
func newAnchorSet() anchorSet {
	return anchorSet{nodes: map[string]ast.Node{}, values: map[string]ast.Node{}}
}

// record records the anchor that name names, over content, in both maps.
// A nil name records nothing.
func (s anchorSet) record(name *token.Token, content ast.Node) {
	if name == nil {
		return
	}

	s.nodes[name.Value] = content
	s.values[name.Value] = content
}

// valueTarget returns the content that an alias in a value refers to when
// it names name, and whether it refers to any.
func (s anchorSet) valueTarget(name string) (ast.Node, bool) {
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
// order. The anchors set holds the anchors visited so far, the targets map
// holds the content each visited alias refers to, and the owners map holds
// the anchor of each content an anchor names. Walk visits an anchor before
// its content, so an alias inside that content refers to the anchor around
// it. The binder records the anchor again once it has walked the content,
// as the decoder does, so an alias after the content refers to the anchor
// around it rather than to one of the same name inside it.
//
// The merged map holds, for each mapping a merge key has brought in through
// an alias, the anchors that merging it records, from
// [aliasBinder.mergedAnchors], when that result holds for every later
// merge. The pending map holds the other results mergedAnchors has read,
// each with the state it depends on. The open map holds the anchored and
// merged mappings the walk is inside of, and the ones mergedAnchors is
// reading, so a mapping that merges itself stops there.
//
// The watched set holds each mapping and alias that a result in the
// pending map depends on. The version field counts the changes to the
// state of a watched node, so a result that held at one version holds
// until the next, as [aliasBinder.holds] describes.
//
// The merges set holds each alias a `<<` merge key names that the binder
// has bound.
type aliasBinder struct {
	anchors anchorSet
	targets map[*ast.AliasNode]ast.Node
	owners  map[ast.Node]*ast.AnchorNode
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
// It returns nil for a nil node, including a typed nil a hand-built tree may
// hold, so Walk stops rather than reading the fields behind it.
func (b *aliasBinder) Visit(node ast.Node) ast.Visitor {
	if astnode.IsNil(node) {
		return nil
	}

	switch n := node.(type) {
	case *ast.AnchorNode:
		name := nodeToken(n.Name)
		b.owners[n.Value] = n
		b.anchors.record(name, n.Value)

		if mapping := anchoredMapping(n.Value); mapping != nil {
			b.walkOpen(mapping)
		} else {
			ast.Walk(b, n.Value)
		}

		// The decoder records an anchor again once it has read the
		// content, over any anchor of the same name inside it.
		b.anchors.record(name, n.Value)

		return nil

	case *ast.AliasNode:
		name := nodeToken(n.Value)
		if name == nil {
			return b
		}

		if target, ok := b.anchors.valueTarget(name.Value); ok {
			b.bind(n, target)
		}

	case *ast.MappingValueNode:
		if isMergeKey(n.Key) {
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

// findSources returns the mappings a `<<` merge key with value brings in,
// in the order the decoder reads them. As the decoder does before it
// reads any of them, findSources looks through the anchors, tags, and
// aliases on value and on each element of a sequence of them. It records
// each anchor on the way into nodes, the nodes map of an [anchorSet]. It
// follows an alias to the anchor it is bound to, and stops at an alias it
// has already followed. It also returns each alias it stops at because the
// alias is not bound.
//
// With bind set, findSources binds each alias of value itself to the
// anchor of its name in nodes, and walks any other node of value where the
// decoder expects a mapping, so the aliases inside that node bind too.
func (b *aliasBinder) findSources(
	value ast.Node, nodes map[string]ast.Node, bind bool,
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
				b.owners[n.Value] = n

				if name := nodeToken(n.Name); name != nil {
					nodes[name.Value] = n.Value
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

				node, inline = target, false

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

// bindMergeAlias binds alias, which a `<<` merge key names, to the anchor
// of its name in nodes, the nodes map of an [anchorSet], and adds it to the
// merges set.
func (b *aliasBinder) bindMergeAlias(alias *ast.AliasNode, nodes map[string]ast.Node) {
	name := nodeToken(alias.Value)
	if name == nil {
		return
	}

	if target, ok := nodes[name.Value]; ok {
		b.merges[alias] = true
		b.bind(alias, target)
	}
}

// bind binds alias to target.
func (b *aliasBinder) bind(alias *ast.AliasNode, target ast.Node) {
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

// touch starts a new version when node, whose state has just changed, is
// one a result in the pending map depends on.
func (b *aliasBinder) touch(node ast.Node) {
	if b.watched[node] {
		b.version++
	}
}

// mergedRead is the result of [aliasBinder.mergedAnchors]. The anchors
// set holds the anchors merging a mapping records, and the final field
// reports whether they hold for every later merge of the mapping.
//
// A result that is not final holds while the state it read stays the
// same. The mapping field holds the mapping the result is for, and the
// open field whether that mapping was open, which is true only for the
// empty result of a mapping mergedAnchors found open. The unbound set
// holds each alias of a merge key the read could not follow, and the
// reads slice holds the results that are not final of the mappings the
// read merged. A result that holds any such alias or result is not
// final. The result holds while its mapping stays open or closed
// as the open field records and has no final result yet, while each of
// its unbound aliases stays unbound, and while each of its reads holds.
//
// The checked field holds the version at which [aliasBinder.holds] last
// checked the result, and the held field what it found then.
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

// mergedAnchors returns the anchors the decoder records when a `<<` merge
// key brings mapping in. Those are the last anchor of each name in mapping,
// in document order, where a `<<` merge key inside mapping counts the
// anchors of the mappings it merges. It finds the mappings those merge keys
// bring in through the aliases as Visit bound them. The result is not
// final when mapping, or a mapping it merges, is open, since those merge
// nothing until the walk leaves them. It is also not final when one of
// those merge keys names an alias Visit has not bound yet, since the
// alias can bring in a mapping once it binds.
//
// It keeps a final result in the merged map, and any other result in the
// pending map, which it reuses while the result holds. A result that is
// not final refers to the results it merged rather than copying what they
// depend on, so each read adds only the merge keys of its own mapping.
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

// holds reports whether read, a result of [aliasBinder.mergedAnchors]
// that is not final, still holds, as [mergedRead] describes.
//
// Every mapping and alias a result depends on is in the watched set, so
// a result that held at the current version still holds. The checked
// and held fields of each result keep what the last check found, so a
// chain of results that merge the ones before them costs one check for
// each result at each version.
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

// anchorReader collects the anchors [aliasBinder.mergedAnchors] returns
// while [ast.Walk] visits a mapping. The anchors set holds the anchors
// recorded so far, and final reports whether the result holds for every
// merge. The unbound set and the reads slice hold the state the result
// depends on, as [mergedRead] describes.
type anchorReader struct {
	binder  *aliasBinder
	anchors anchorSet
	unbound map[*ast.AliasNode]bool
	reads   []*mergedRead
	final   bool
}

// Visit records an anchor, or at a `<<` merge key records the anchors of
// the mappings it merges, then returns r so [ast.Walk] continues into the
// children of node. It walks the content of an anchor itself and records
// the anchor again after it, as [aliasBinder.Visit] does, and returns nil
// for it. It returns nil for a nil node, including a typed nil a
// hand-built tree may hold, and for an alias, whose content the merge
// records nothing from.
func (r *anchorReader) Visit(node ast.Node) ast.Visitor {
	if astnode.IsNil(node) {
		return nil
	}

	switch n := node.(type) {
	case *ast.AnchorNode:
		name := nodeToken(n.Name)
		r.anchors.record(name, n.Value)

		ast.Walk(r, n.Value)

		r.anchors.record(name, n.Value)

		return nil

	case *ast.AliasNode:
		return nil

	case *ast.MappingValueNode:
		if isMergeKey(n.Key) {
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

// walkOpen walks mapping, the content of an anchor or a mapping a `<<`
// merge key holds inline, and holds it open while the walk is inside it.
func (b *aliasBinder) walkOpen(mapping *ast.MappingNode) {
	if !b.open[mapping] {
		b.setOpen(mapping, true)
		defer b.setOpen(mapping, false)
	}

	ast.Walk(b, mapping)
}

// anchoredMapping returns the mapping an anchor's content holds, looking
// through tags, or nil when the content is not a mapping.
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
//
// Returns an error wrapping [ErrAlias] for an alias with no anchor of its
// name before it, or for an alias that leads back to itself, as
// [resolver.follow] describes.
func (r *resolver) deref(node ast.Node) (ast.Node, error) {
	return r.follow(node, map[*ast.AliasNode]bool{})
}

// unwrap is [resolver.deref] followed by stripping tags, so the result is a
// mapping, sequence, or scalar that selectors can apply to. It stops at a
// nil node, including a typed nil a hand-built tree may hold, and returns
// it.
//
// It tracks the aliases it follows across every tag it strips, so an alias
// that leads back to itself through a tag returns an error wrapping
// [ErrAlias].
func (r *resolver) unwrap(node ast.Node) (ast.Node, error) {
	followed := map[*ast.AliasNode]bool{}

	for {
		content, err := r.follow(node, followed)
		if err != nil {
			return nil, err
		}

		tag, ok := content.(*ast.TagNode)
		if !ok || astnode.IsNil(content) {
			return content, nil
		}

		node = tag.Value
	}
}

// follow looks through anchors and aliases from node and adds each alias it
// follows to followed. It returns an error wrapping [ErrAlias] when it
// reaches an alias that leads back to itself, an alias with no anchor, or
// an alias with no name. An alias leads back to itself when it is already
// in followed, or when it lies inside the content of the anchor it refers
// to and no `<<` merge key names it. It stops at a nil node, including a
// typed nil a hand-built tree may hold, and returns it.
func (r *resolver) follow(node ast.Node, followed map[*ast.AliasNode]bool) (ast.Node, error) {
	for {
		if astnode.IsNil(node) {
			return node, nil
		}

		switch n := node.(type) {
		case *ast.AnchorNode:
			node = n.Value
		case *ast.AliasNode:
			tk := nodeToken(n.Value)
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
				return nil, fmt.Errorf("%w: *%s has no anchor before it", ErrAlias, name)
			}

			node = target

		default:
			return node, nil
		}
	}
}

// resolve applies segs to root and returns every match, in document order
// along the path.
//
// A recursive selector can reach one entry from several matches, such as
// when one match lies inside another or two aliases share an anchor.
// Resolve keeps the first match of each entry, so a recursive selector
// yields each entry once. Only a recursive selector drops repeats, so a
// later `.name`, `[n]`, or `[*]` selector can still reach one node through
// several aliases.
func (r *resolver) resolve(root ast.Node, segs []segment) ([]match, error) {
	matches := []match{{node: root}}

	for _, seg := range segs {
		var next []match

		switch seg.kind {
		case segmentKey:
			for _, m := range matches {
				next = append(next, m.key())
			}

		case segmentRecursive:
			found, err := r.recurse(matches, seg.name)
			if err != nil {
				return nil, err
			}

			next = found

		default:
			for _, m := range matches {
				found, err := r.apply(seg, m)
				if err != nil {
					return nil, err
				}

				next = append(next, found...)
			}
		}

		// A selector applied to a match that lies inside another can
		// reach a node before the ones it reaches from the outer match.
		slices.SortStableFunc(next, func(a, b match) int {
			return slices.Compare(a.order, b.order)
		})

		if seg.kind == segmentRecursive {
			next = uniqueMatches(next)
		}

		matches = next
	}

	return matches, nil
}

// uniqueMatches returns the first match of each entry, in the order
// matches holds them. It compares the entries rather than their values,
// so two entries of a hand-built tree that share one value node, or that
// both hold nil, count as two matches.
func uniqueMatches(matches []match) []match {
	seen := make(map[*ast.MappingValueNode]bool, len(matches))
	unique := make([]match, 0, len(matches))

	for _, m := range matches {
		if seen[m.entry] {
			continue
		}

		seen[m.entry] = true
		unique = append(unique, m)
	}

	return unique
}

// apply applies one `.name`, `[n]`, or `[*]` selector to the node of m,
// and returns the matches with the selector that names each one appended
// to the selectors of m. A nil node, including a typed nil, has nothing to
// select.
func (r *resolver) apply(seg segment, m match) ([]match, error) {
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

		entry, i, ok, err := r.lookup(mapping, seg.name, nil)
		if err != nil || !ok {
			return nil, err
		}

		return []match{m.with(entry.Value, entry, seg, i)}, nil

	case segmentIndex:
		seq, ok := content.(*ast.SequenceNode)
		if !ok || seg.index >= len(seq.Values) {
			return nil, nil
		}

		return []match{m.with(seq.Values[seg.index], nil, seg, seg.index)}, nil

	case segmentIndexAll:
		seq, ok := content.(*ast.SequenceNode)
		if !ok {
			return nil, nil
		}

		matches := make([]match, 0, len(seq.Values))
		for i, v := range seq.Values {
			matches = append(matches, m.with(v, nil, segment{kind: segmentIndex, index: i}, i))
		}

		return matches, nil

	default:
		return nil, nil
	}
}

// lookup finds the entry for name in mapping, looking through its `<<`
// merge keys as well as its own entries. The later entry in document order
// wins, whether it is an entry of the mapping itself or a merge key whose
// sources hold that key, and a later merge source wins over an earlier
// one. That is the entry whose value the goccy/go-yaml decoder keeps, since
// it sets the entries of a mapping in order and a merge sets the keys of its
// sources where the merge key stands.
//
// The seen set guards against merge cycles through aliases. The int result
// is the index in mapping of the entry lookup found, or, for an entry a
// merge source holds, of the `<<` entry that brings it in. The bool result
// reports whether lookup found an entry.
func (r *resolver) lookup(
	mapping *ast.MappingNode, name string, seen map[*ast.MappingNode]bool,
) (*ast.MappingValueNode, int, bool, error) {
	keys := r.mappingKeys(mapping)

	// An entry of the mapping with no merge key after it wins outright.
	// That takes in the last merge key itself, which the name `<<` selects
	// when no real key has that text.
	own, hasOwn := keys.names[name]
	if hasOwn && !keys.mergeAfter(own) {
		return mapping.Values[own], own, true, nil
	}

	if seen == nil {
		seen = map[*ast.MappingNode]bool{}
	}

	seen[mapping] = true

	// A later merge key wins over an earlier one, as a later source in one
	// merge key does, so lookup reads the merge keys from the last one back.
	// It stops at the entry of the mapping itself, which wins over every
	// merge key before it.
	for _, i := range slices.Backward(keys.merges) {
		if hasOwn && i < own {
			break
		}

		sources, err := r.mergeSources(mapping.Values[i].Value)
		if err != nil {
			return nil, 0, false, err
		}

		for _, src := range slices.Backward(sources) {
			if seen[src] {
				continue
			}

			found, _, ok, err := r.lookup(src, name, seen)
			if err != nil {
				return nil, 0, false, err
			}

			// A merge key in a source is not a key the merge brings in.
			if ok && !isMergeKey(found.Key) {
				return found, i, true, nil
			}
		}
	}

	if hasOwn {
		return mapping.Values[own], own, true, nil
	}

	return nil, 0, false, nil
}

// overridden reports whether a path through name in mapping selects an
// entry other than the one at index i, the last entry of mapping with that
// name. Only a `<<` key after that entry can win over it, as
// [resolver.lookup] describes. It also reports true when those merge keys
// do not resolve, since a path through name then selects nothing.
func (r *resolver) overridden(mapping *ast.MappingNode, keys *mappingKeys, name string, i int) bool {
	if !keys.mergeAfter(i) {
		return false
	}

	_, found, ok, err := r.lookup(mapping, name, nil)

	return err != nil || !ok || found != i
}

// mergeSources returns the mappings a `<<` value merges in: the value itself
// when it is a mapping, or each mapping element when it is a sequence. It
// skips a nil node, including a typed nil.
func (r *resolver) mergeSources(value ast.Node) ([]*ast.MappingNode, error) {
	content, err := r.unwrap(value)
	if err != nil || astnode.IsNil(content) {
		return nil, err
	}

	switch n := content.(type) {
	case *ast.MappingNode:
		return []*ast.MappingNode{n}, nil
	case *ast.SequenceNode:
		sources := make([]*ast.MappingNode, 0, len(n.Values))

		for _, v := range n.Values {
			elem, err := r.unwrap(v)
			if err != nil {
				return nil, err
			}

			if m, ok := elem.(*ast.MappingNode); ok && m != nil {
				sources = append(sources, m)
			}
		}

		return sources, nil

	default:
		return nil, nil
	}
}

// recurse applies the `..name` selector to each of matches, in order, and
// returns every entry it finds below each, in the order it finds them.
// It returns an error wrapping [ErrAlias] when the node of a match is an
// alias that does not resolve.
//
// A walk from one match can reach the start of another, such as when one
// match lies inside another. Take an earlier walk that reaches the start
// of a later match at an order that [covers] the order of that match. No
// entry below the start sorts earlier from the later walk than from the
// earlier one, so resolve keeps the entry the earlier walk finds. Hence
// recurse skips a later match whose start an earlier walk covers, and a
// walk stops at the start of an earlier match whose order covers the walk
// there. On a chain of nested matches the first walk thus covers the rest,
// and the step costs one walk rather than one for each match.
func (r *resolver) recurse(matches []match, name string) ([]match, error) {
	w := &recursiveWalk{
		resolver: r,
		name:     name,
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

		w.descend(contents[i])
	}

	return w.found, nil
}

// covers reports whether a walk that reaches a node at order a finds each
// entry below the node no later than a walk that reaches it at order b
// does. That holds when the orders are equal, or when they first differ
// at a place both hold and a is lower there. When one order extends the
// other, the entry decides which walk finds it first, so covers reports
// false.
func covers(a, b []int) bool {
	for i := range min(len(a), len(b)) {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}

	return len(a) == len(b)
}

// recursiveWalk walks the subtrees of the matches one `..name` selector
// applies to, as [resolver.recurse] describes.
//
// The from field holds those matches, and the starts field maps the
// content each one starts from to its indexes in from. The cur field is
// the index of the current match, and the skip field marks the matches an
// earlier walk covers.
//
// The segs and order stacks hold the selectors and the order down to the
// node the walk has reached. Each entry the walk finds holds the stacks as
// they stand, and the held fields record how much of each stack the
// entries hold, so a push that would overwrite a held place copies the
// stack first. Entries along one branch thus share their selectors and their
// order rather than holding a copy each.
type recursiveWalk struct {
	resolver  *resolver
	starts    map[ast.Node][]int
	name      string
	from      []match
	skip      []bool
	found     []match
	segs      []segment
	order     []int
	cur       int
	heldSegs  int
	heldOrder int
}

// descend collects every mapping entry keyed name at any depth below node, in
// document order. It looks through anchors and tags but not aliases, so it
// visits an entry of the source at most once, at its definition. It skips a
// mapping or sequence that the walk of another match covers, as
// [resolver.recurse] describes.
//
// For each entry and element it visits, descend pushes the selector and
// the place of that step onto the stacks. Before it returns, descend cuts
// both stacks back to the length they had when it began.
//
// It visits only the entry that a path through its key selects, and skips
// every other entry of that key and everything below it. When a mapping
// holds several `<<` keys, descend thus visits only the inline mapping of
// the last one, even though the decoder merges them all. When a mapping
// holds a real key whose text is `<<` next to a merge key, a path through
// `<<` selects the real key, so descend skips the merge key. When a later
// `<<` key brings in the key of an entry, a path through that key selects
// the merged entry, so descend skips the entry of the mapping itself.
func (w *recursiveWalk) descend(node ast.Node) {
	if astnode.IsNil(node) {
		return
	}

	segsDepth, orderDepth := len(w.segs), len(w.order)

	switch n := node.(type) {
	case *ast.MappingNode:
		if w.covered(n) {
			return
		}

		keys := w.resolver.mappingKeys(n)

		for i, entry := range n.Values {
			if entry == nil {
				continue
			}

			key, _ := w.resolver.keyName(entry.Key)
			if keys.names[key] != i || w.resolver.overridden(n, keys, key, i) {
				continue
			}

			w.push(segsDepth, orderDepth, segment{kind: segmentChild, name: key}, i)

			if key == w.name {
				segs, order := w.hold()
				w.found = append(w.found, match{node: entry.Value, entry: entry, segs: segs, order: order})
			}

			w.descend(entry.Value)
		}

	case *ast.SequenceNode:
		if w.covered(n) {
			return
		}

		for i, v := range n.Values {
			w.push(segsDepth, orderDepth, segment{kind: segmentIndex, index: i}, i)
			w.descend(v)
		}

	case *ast.AnchorNode:
		w.descend(n.Value)
	case *ast.TagNode:
		w.descend(n.Value)
	}

	w.segs = w.segs[:segsDepth]
	w.order = w.order[:orderDepth]
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

// push sets the selector and the order place of one step, at the depths
// given, onto the stacks. It copies a stack first when the place it sets
// is one an entry holds.
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

// hold returns the stacks as they stand, for an entry the walk finds, and
// records that an entry holds them. The results have no spare capacity,
// so an append to one copies it.
func (w *recursiveWalk) hold() ([]segment, []int) {
	w.heldSegs = max(w.heldSegs, len(w.segs))
	w.heldOrder = max(w.heldOrder, len(w.order))

	return slices.Clip(w.segs), slices.Clip(w.order)
}

// keyContent looks through the `?` indicator of an explicit key and the
// anchors and tags on a key to the node that carries the key itself. It
// returns nil for a nil key, including a typed nil a hand-built tree may
// hold at any step.
func keyContent(key ast.Node) ast.Node {
	node := key

	for {
		if astnode.IsNil(node) {
			return nil
		}

		switch n := node.(type) {
		case *ast.MappingKeyNode:
			node = n.Value
		case *ast.AnchorNode:
			node = n.Value
		case *ast.TagNode:
			node = n.Value
		default:
			return node
		}
	}
}

// isMergeKey reports whether key is a `<<` merge key, looking through the `?`
// indicator, anchors, and tags. A nil key, including a typed nil a hand-built
// tree may hold at any step, is not a merge key.
func isMergeKey(key ast.MapKeyNode) bool {
	_, ok := keyContent(key).(*ast.MergeKeyNode)

	return ok
}

// keyName returns the key text a child selector compares against. A
// string key gives its unquoted text, a literal or folded block scalar key
// gives its content, as the decoder reads it, and any other key gives its
// source text. An alias key, tagged or not, gives the name the content of
// its anchor would give as a key. The bool result is false for a key with
// no content, or with content a selector cannot name, such as a sequence,
// and for an alias key with no anchor before it or one that leads back to
// itself. Such a key has the empty name.
func (r *resolver) keyName(key ast.Node) (string, bool) {
	content := keyContent(key)

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
		tk := nodeToken(k)
		if tk == nil {
			return "", false
		}

		return tk.Value, true

	default:
		return "", false
	}
}

// nodeToken returns the token of node, or nil when node has none. A nil
// node, including a typed nil a hand-built tree may hold, has no token.
func nodeToken(node ast.Node) *token.Token {
	if astnode.IsNil(node) {
		return nil
	}

	return node.GetToken()
}

// firstToken returns the token that starts node's content: the first key of
// a mapping, the first element of a sequence, or the scalar itself. It looks
// through anchors and tags, and through the `?` indicator, anchors and tags
// of a mapping's first key. An alias is its own token. An entry with no key
// starts at its own token. A nil node, including a typed nil a hand-built
// tree may hold, has no token.
func firstToken(node ast.Node) *token.Token {
	for {
		if astnode.IsNil(node) {
			return nil
		}

		switch n := node.(type) {
		case *ast.AnchorNode:
			node = n.Value
		case *ast.TagNode:
			node = n.Value
		case *ast.MappingNode:
			if len(n.Values) == 0 || n.Values[0] == nil {
				return n.GetToken()
			}

			key := keyContent(n.Values[0].Key)
			if key == nil {
				return n.Values[0].GetToken()
			}

			node = key

		case *ast.SequenceNode:
			if len(n.Values) == 0 {
				return n.GetToken()
			}

			node = n.Values[0]

		default:
			return node.GetToken()
		}
	}
}
