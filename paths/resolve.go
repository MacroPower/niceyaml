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

// apply applies one `.name`, `[n]`, or `[*]` selector to the node of m,
// and returns the matches with the selector that names each one appended
// to the selectors of m. A nil node, including a typed nil, has nothing to
// select.
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

		if !isNilNode(content) {
			w.starts[content] = append(w.starts[content], i)
		}
	}

	for i, m := range matches {
		if w.skip[i] || isNilNode(contents[i]) {
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
// at a place both hold and a is lower there. When one order extends the other, the
// entry decides which walk finds it first, so covers reports false.
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
// node the walk has reached. Each entry the walk finds holds the stacks as they
// stand, and the held fields record how much of each stack the entries
// hold, so a push that would overwrite a held place copies the stack
// first. Entries along one branch thus share their selectors and their
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
// It skips an entry whose key a later entry in its mapping repeats, and
// everything below it, because a path through that key selects the later
// entry. When a mapping holds several `<<` keys, descend thus visits only
// the inline mapping of the last one, even though the decoder merges them
// all.
func (w *recursiveWalk) descend(node ast.Node) {
	if isNilNode(node) {
		return
	}

	segsDepth, orderDepth := len(w.segs), len(w.order)

	switch n := node.(type) {
	case *ast.MappingNode:
		if w.covered(n) {
			return
		}

		last := make(map[string]*ast.MappingValueNode, len(n.Values))

		for _, entry := range n.Values {
			if entry != nil {
				last[w.resolver.keyName(entry.Key)] = entry
			}
		}

		for i, entry := range n.Values {
			if entry == nil {
				continue
			}

			key := w.resolver.keyName(entry.Key)
			if last[key] != entry {
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
