package paths

import (
	"fmt"
	"reflect"
	"slices"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"
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

// key returns the match for the `~` selector applied to m: the key of the
// entry m holds, or m itself when m holds no entry or its entry has no key,
// so a `~` on a sequence element or the root selects what the path before
// it does. The `~` joins the selectors either way, as the path wrote it.
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
// holds the content of the anchor each alias refers to.
//
// Create instances with [newResolver].
type resolver struct {
	targets map[*ast.AliasNode]ast.Node
}

// newResolver creates a new [*resolver] for doc.
//
// It binds each alias to the last anchor of its name before it in doc, which
// is the anchor the goccy/go-yaml decoder uses for that alias.
func newResolver(doc *ast.DocumentNode) *resolver {
	b := &aliasBinder{
		anchors: map[string]ast.Node{},
		targets: map[*ast.AliasNode]ast.Node{},
	}

	ast.Walk(b, doc.Body)

	return &resolver{targets: b.targets}
}

// aliasBinder binds aliases to anchors while [ast.Walk] visits a document in
// order. The anchors map holds the content of the last anchor of each name
// visited so far, and the targets map holds the content each visited alias
// refers to. Walk visits an anchor before its content, so an alias inside
// that content refers to the anchor around it.
type aliasBinder struct {
	anchors map[string]ast.Node
	targets map[*ast.AliasNode]ast.Node
}

// Visit records an anchor or binds an alias, then returns b so [ast.Walk]
// continues into the children of node.
//
// It returns nil for a nil node, including a typed nil a hand-built tree may
// hold, so Walk stops rather than reading the fields behind it.
func (b *aliasBinder) Visit(node ast.Node) ast.Visitor {
	if isNilNode(node) {
		return nil
	}

	switch n := node.(type) {
	case *ast.AnchorNode:
		if name := nodeToken(n.Name); name != nil {
			b.anchors[name.Value] = n.Value
		}

	case *ast.AliasNode:
		name := nodeToken(n.Value)
		if name == nil {
			return b
		}

		if target, ok := b.anchors[name.Value]; ok {
			b.targets[n] = target
		}
	}

	return b
}

// deref looks through anchors and aliases to the content node they carry.
//
// Returns an error wrapping [ErrAlias] for an alias with no anchor of its
// name before it, or for an alias that leads back to itself.
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
		if !ok || isNilNode(content) {
			return content, nil
		}

		node = tag.Value
	}
}

// follow looks through anchors and aliases from node and adds each alias it
// follows to followed. It returns an error wrapping [ErrAlias] when it
// reaches an alias already in followed, an alias with no anchor, or an alias
// with no name. It stops at a nil node, including a typed nil a hand-built
// tree may hold, and returns it.
func (r *resolver) follow(node ast.Node, followed map[*ast.AliasNode]bool) (ast.Node, error) {
	for {
		if isNilNode(node) {
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

			if followed[n] {
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
// A recursive selector walks the whole subtree of each match, so when one
// match lies inside another, the entries below the inner match appear once
// for each. Resolve keeps the first of these, so a recursive selector
// yields each entry once. Only a recursive selector drops repeats, so a
// later `.name`, `[n]`, or `[*]` selector can still reach one node through
// several aliases.
func (r *resolver) resolve(root ast.Node, segs []segment) ([]match, error) {
	matches := []match{{node: root}}

	for _, seg := range segs {
		var next []match

		for _, m := range matches {
			if seg.kind == segmentKey {
				next = append(next, m.key())

				continue
			}

			found, err := r.apply(seg, m)
			if err != nil {
				return nil, err
			}

			next = append(next, found...)
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

// uniqueMatches returns the first match of each node, in the order matches
// holds them. It compares the nodes as the source writes them, before it
// follows aliases, so two aliases to one anchor count as two nodes.
func uniqueMatches(matches []match) []match {
	seen := make(map[ast.Node]bool, len(matches))
	unique := make([]match, 0, len(matches))

	for _, m := range matches {
		if seen[m.node] {
			continue
		}

		seen[m.node] = true
		unique = append(unique, m)
	}

	return unique
}

// apply applies one selector to the node of m, and returns the matches
// with the selector that names each one appended to the selectors of m.
// A nil node, including a typed nil, has nothing to select.
func (r *resolver) apply(seg segment, m match) ([]match, error) {
	content, err := r.unwrap(m.node)
	if err != nil || isNilNode(content) {
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

	case segmentRecursive:
		segs, order := slices.Clone(m.segs), slices.Clone(m.order)

		return r.descend(content, seg.name, &segs, &order, nil), nil

	default:
		return nil, nil
	}
}

// lookup finds the entry for name in mapping, looking through `<<` merge keys
// when no entry of the mapping itself has that key. A key the mapping defines
// wins over a merged one, and a later merge source wins over an earlier one,
// which is the source the goccy/go-yaml decoder takes. Among several entries
// of the mapping itself with that key, the later one wins, which is the
// entry whose value the decoder keeps.
//
// The seen set guards against merge cycles through aliases. The int result
// is the index in mapping of the entry lookup found, or, for an entry a
// merge source holds, of the `<<` entry that brings it in. The bool result
// reports whether lookup found an entry.
func (r *resolver) lookup(
	mapping *ast.MappingNode, name string, seen map[*ast.MappingNode]bool,
) (*ast.MappingValueNode, int, bool, error) {
	for i, entry := range slices.Backward(mapping.Values) {
		if entry != nil && r.keyName(entry.Key) == name {
			return entry, i, true, nil
		}
	}

	if seen == nil {
		seen = map[*ast.MappingNode]bool{}
	}

	seen[mapping] = true

	// A later merge key wins over an earlier one, as a later source in one
	// merge key does, so lookup reads the entries from the last one back.
	for i, entry := range slices.Backward(mapping.Values) {
		if entry == nil || !isMergeKey(entry.Key) {
			continue
		}

		sources, err := r.mergeSources(entry.Value)
		if err != nil {
			return nil, 0, false, err
		}

		for _, src := range slices.Backward(sources) {
			if seen[src] {
				continue
			}

			found, _, ok, err := r.lookup(src, name, seen)
			if err != nil || ok {
				return found, i, ok, err
			}
		}
	}

	return nil, 0, false, nil
}

// mergeSources returns the mappings a `<<` value merges in: the value itself
// when it is a mapping, or each mapping element when it is a sequence. It
// skips a nil node, including a typed nil.
func (r *resolver) mergeSources(value ast.Node) ([]*ast.MappingNode, error) {
	content, err := r.unwrap(value)
	if err != nil || isNilNode(content) {
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

// descend collects every mapping entry keyed name at any depth below node, in
// document order. It looks through anchors and tags but not aliases, so it
// visits an entry of the source at most once, at its definition.
//
// The segs and order stacks hold the selectors and the order down to
// node. For each entry and element it visits, descend pushes the selector
// and the place of that step onto the stacks, and each match gets its own
// copy of them down to its entry. Before it returns, descend cuts both
// stacks back to the length they had when it began.
//
// It skips an entry whose key a later entry in its mapping repeats, and
// everything below it, because a path through that key selects the later
// entry. When a mapping holds several `<<` keys, descend thus visits only
// the inline mapping of the last one, even though the decoder merges them
// all.
func (r *resolver) descend(node ast.Node, name string, segs *[]segment, order *[]int, acc []match) []match {
	if isNilNode(node) {
		return acc
	}

	segsDepth, orderDepth := len(*segs), len(*order)

	switch n := node.(type) {
	case *ast.MappingNode:
		last := make(map[string]*ast.MappingValueNode, len(n.Values))

		for _, entry := range n.Values {
			if entry != nil {
				last[r.keyName(entry.Key)] = entry
			}
		}

		for i, entry := range n.Values {
			if entry == nil {
				continue
			}

			key := r.keyName(entry.Key)
			if last[key] != entry {
				continue
			}

			*segs = append((*segs)[:segsDepth], segment{kind: segmentChild, name: key})
			*order = append((*order)[:orderDepth], i)

			if key == name {
				acc = append(acc, match{
					node:  entry.Value,
					entry: entry,
					segs:  slices.Clone(*segs),
					order: slices.Clone(*order),
				})
			}

			acc = r.descend(entry.Value, name, segs, order, acc)
		}

	case *ast.SequenceNode:
		for i, v := range n.Values {
			*segs = append((*segs)[:segsDepth], segment{kind: segmentIndex, index: i})
			*order = append((*order)[:orderDepth], i)

			acc = r.descend(v, name, segs, order, acc)
		}

	case *ast.AnchorNode:
		acc = r.descend(n.Value, name, segs, order, acc)
	case *ast.TagNode:
		acc = r.descend(n.Value, name, segs, order, acc)
	}

	*segs = (*segs)[:segsDepth]
	*order = (*order)[:orderDepth]

	return acc
}

// keyContent looks through the `?` indicator of an explicit key and the
// anchors and tags on a key to the node that carries the key itself. It
// returns nil for a nil key, including a typed nil a hand-built tree may
// hold at any step.
func keyContent(key ast.MapKeyNode) ast.Node {
	var node ast.Node = key

	for {
		if isNilNode(node) {
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
// source text. An alias key gives the name the content of its anchor
// would give as a key. A key with no content, or with content a selector
// cannot name, such as a sequence, has the empty name, and so does an
// alias key with no anchor before it or one that leads back to itself.
func (r *resolver) keyName(key ast.MapKeyNode) string {
	content := keyContent(key)

	if alias, ok := content.(*ast.AliasNode); ok {
		target, err := r.unwrap(alias)
		if err != nil || isNilNode(target) {
			return ""
		}

		content = target
	}

	switch k := content.(type) {
	case nil:
		return ""
	case *ast.StringNode:
		return k.Value
	case *ast.LiteralNode:
		if k.Value == nil {
			return ""
		}

		return k.Value.Value

	case ast.MapKeyNode:
		tk := nodeToken(k)
		if tk == nil {
			return ""
		}

		return tk.Value

	default:
		return ""
	}
}

// isNilNode reports whether node is nil, including a typed nil a
// hand-built tree may hold behind a non-nil interface.
func isNilNode(node ast.Node) bool {
	return node == nil || reflect.ValueOf(node).IsNil()
}

// nodeToken returns the token of node, or nil when node has none. A nil
// node, including a typed nil a hand-built tree may hold, has no token.
func nodeToken(node ast.Node) *token.Token {
	if isNilNode(node) {
		return nil
	}

	return node.GetToken()
}

// firstToken returns the token that starts node's content: the first key of
// a mapping, the first element of a sequence, or the scalar itself. It looks
// through anchors and tags, and through the `?` indicator, anchors and tags
// of a mapping's first key; an alias is its own token. An entry with no key
// starts at its own token. A nil node, including a typed nil a hand-built
// tree may hold, has no token.
func firstToken(node ast.Node) *token.Token {
	for {
		if isNilNode(node) {
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
