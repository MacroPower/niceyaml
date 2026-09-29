package niceyaml

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/paths"
)

// decodeTree is the tree the go-yaml decoder reads for one document.
//
// The decoder keeps the anchors it has read in maps keyed by name, so an
// anchor it reads later hides an earlier one of the same name from every
// alias it reads after that. It reads a node in two passes, and a decode
// of a node below the body reads the anchors outside the node before it,
// so neither order matches the document. The tree therefore gives a name
// of its own to each anchor whose name another anchor of the document
// shares, and to each anchor that follows an alias of its name with no
// anchor of that name before it. It gives each alias the name of the
// anchor the document's [paths.Resolver] binds it to, so every alias
// reads that anchor whatever order the decoder reads the anchors in. An
// alias the resolver binds to no anchor keeps its name, so it reads an
// anchor of a reference document or none.
//
// The decoder reads an alias inside the anchor it refers to as null,
// since it has not finished reading the anchor. For a type with an
// UnmarshalText method, or one that reads YAML bytes, the decoder writes
// out an alias to that anchor as the text of its content. That content
// holds the alias again, so the writing never ends and overflows the
// stack. The tree therefore holds a null in place of each alias inside
// the anchor it refers to. An alias that a `<<` merge key merges stays,
// since the decoder rejects it before it writes anything.
//
// Renaming and nulls change the tree, which the tree the [Source] shares
// must not see, so a document that renames an anchor or holds such an
// alias reads a second parse of the Source from [Source.decodeParse]. A
// renamed anchor keeps the text of the Source, so the bytes the decoder
// hands an UnmarshalYAML method hold the names the document spells. The
// decoder looks an alias up by the text of its name, so an alias carries
// the new name in its text as well. An [ast.Node] the decoder fills, or
// one it hands an UnmarshalYAML method, thus spells an alias to a renamed
// anchor with the new name, such as `*x [2]`, and each anchor as the
// document does.
//
// Any other document reads the tree of the Source through [decodeView],
// which copies only the aliases, and the nodes above them, to drop the
// comments on their names.
//
// Create instances with [newDecodeTree].
type decodeTree struct {
	// The body of the document as the Source parsed it.
	source ast.Node
	// The body of the document as the decoder reads it.
	body ast.Node
	// The node of body that stands for each node of source outside its
	// comments, which scoped fills for the first decode of a node below
	// the body when body does not come from a second parse.
	nodes map[ast.Node]ast.Node
	// The tokens the nodes of body hold, when body comes from a second
	// parse, including the ones the parser made for values the document
	// leaves out. The errors of the decoder may name them.
	tokens map[*token.Token]struct{}
	// The tokens of the second parse body comes from, if any, which the
	// errors of the decoder may name. The Source shares the set between
	// the trees of all its documents, and no tree writes to it.
	parseTokens map[*token.Token]struct{}
	// Spells each name the tree gives an anchor as the document does,
	// for the messages the decoder reports, or nil when no anchor has a
	// name of its own.
	names *strings.Replacer
	// The anchors of body, which scoped lists for the first decode of a
	// node below the body.
	anchors *anchorIndex
	// The aliases under `<<` merge keys that the decoder finds no mapping
	// for, which unresolvedMerge lists for the first decode that needs
	// them.
	merges []mergeAlias
	// Fills nodes and anchors once.
	scopedOnce sync.Once
	// Fills merges once.
	mergesOnce sync.Once
}

// decodeTree returns the [*decodeTree] of the document, and builds it on
// the first call.
func (d *document) decodeTree() *decodeTree {
	d.treeOnce.Do(func() {
		d.tree = d.newDecodeTree()
	})

	return d.tree
}

// newDecodeTree creates a new [*decodeTree] for the document.
func (d *document) newDecodeTree() *decodeTree {
	body := d.root.Body

	names := renamedAnchorNames(d.pathResolver(), body)
	enclosed := d.enclosedAliases()

	if len(names) > 0 || len(enclosed) > 0 {
		if tree, ok := d.parsedTree(names, enclosed); ok {
			return tree
		}
	}

	return &decodeTree{source: body, body: decodeView(body)}
}

// parsedTree returns the [*decodeTree] of the document from a second
// parse of the Source, with a name of its own for each anchor whose name
// is in names, and a null in place of each alias in enclosed. It
// reports false when the second parse does not give the document the
// same nodes, which a parse of the same tokens always does.
func (d *document) parsedTree(names map[string]bool, enclosed map[ast.Node]bool) (*decodeTree, bool) {
	src := d.node.source

	file, fileTokens := src.decodeParse()
	if file == nil {
		return nil, false
	}

	i := d.fileIndex
	if i < 0 || i >= len(file.Docs) {
		return nil, false
	}

	body := file.Docs[i].Body

	nodes, ok := pairNodes(d.root.Body, body)
	if !ok {
		return nil, false
	}

	tokens := tokenCollector{}
	ast.Walk(tokens, body)

	source := sourceNodes(d.root.Body)
	spelled := bracketedNames(source)

	// Each anchor with a name in names gets the name followed by a count in
	// brackets, higher than the count of the last anchor of that name, so
	// no two new names match. A quoted name can hold brackets, so the
	// count skips a new name that an anchor or alias of the document
	// spells, even in part. Such a name would read the wrong anchor, and
	// restoreNames would rewrite it in a message.
	renamed := map[ast.Node]string{}
	count := map[string]int{}

	var pairs []string

	for _, n := range source {
		anchor, ok := n.(*ast.AnchorNode)
		if !ok {
			continue
		}

		name, ok := nodeName(anchor.Name)
		if !ok || !names[name] {
			continue
		}

		var unique string

		for {
			count[name]++
			unique = name + " [" + strconv.Itoa(count[name]) + "]"

			if !spelledIn(spelled, unique) {
				break
			}
		}

		renamed[anchor] = unique
		pairs = append(pairs, unique, name)

		// The decoder names an anchor by the value of the token of its
		// name, while the go-yaml formatter writes the token's text.
		if view, ok := nodes[anchor].(*ast.AnchorNode); ok {
			if tk := nodeToken(view.Name); tk != nil {
				tk.Value = unique
			}
		}
	}

	resolver := d.pathResolver()

	for n, v := range nodes {
		alias, ok := n.(*ast.AliasNode)
		if !ok {
			continue
		}

		view, ok := v.(*ast.AliasNode)
		if !ok {
			continue
		}

		// An alias that names no anchor before it keeps its name, so the
		// decoder reports it as the document spells it.
		name := ""

		anchor, err := resolver.Anchor(alias)
		if err == nil {
			name = renamed[anchor]
		}

		renameAlias(view, name)
	}

	nulls := nodeReplacer{}

	for alias := range enclosed {
		view, ok := nodes[alias].(*ast.AliasNode)
		if !ok || view.Start == nil {
			continue
		}

		// The null takes over the token of the `*`, so an error at the null
		// names the alias. The go-yaml formatter writes the text of each
		// token as the chain of tokens holds it, so the token keeps its
		// place in the chain and reads null there.
		tk := view.Start
		null := token.New("null", strings.TrimSuffix(tk.Origin, "*")+"null", tk.Position)
		null.Prev, null.Next = tk.Prev, tk.Next
		*tk = *null

		nulls[view] = ast.Null(tk)
		nodes[alias] = nulls[view]
	}

	ast.Walk(nulls, body)

	var replacer *strings.Replacer

	if len(pairs) > 0 {
		replacer = strings.NewReplacer(pairs...)
	}

	return &decodeTree{
		source:      d.root.Body,
		body:        body,
		nodes:       nodes,
		tokens:      tokens,
		parseTokens: fileTokens,
		names:       replacer,
	}, true
}

// enclosedAliases returns the aliases of the document that lie inside the
// anchor they refer to, other than the ones a `<<` merge key merges.
func (d *document) enclosedAliases() map[ast.Node]bool {
	var aliases []*ast.AliasNode

	merged := map[ast.Node]bool{}

	for _, n := range sourceNodes(d.root.Body) {
		switch n := n.(type) {
		case *ast.AliasNode:
			aliases = append(aliases, n)

		case *ast.MappingValueNode:
			for _, source := range mergeSources(n) {
				merged[source] = true
			}
		}
	}

	if len(aliases) == 0 {
		return nil
	}

	resolver := d.pathResolver()
	enclosed := map[ast.Node]bool{}

	for _, alias := range aliases {
		if merged[alias] || alias.Start == nil || alias.Start.Position == nil {
			continue
		}

		anchor, err := resolver.Anchor(alias)
		if err == nil && encloses(anchor, alias.Start) {
			enclosed[alias] = true
		}
	}

	return enclosed
}

// nodeReplacer is an [ast.Visitor] that puts the node each key maps to in
// place of the key, in the parent of the key.
type nodeReplacer map[ast.Node]ast.Node

// Visit implements [ast.Visitor].
func (r nodeReplacer) Visit(node ast.Node) ast.Visitor {
	if astnode.IsNil(node) {
		return nil
	}

	switch n := node.(type) {
	case *ast.AnchorNode:
		n.Value = r.replace(n.Value)

	case *ast.TagNode:
		n.Value = r.replace(n.Value)

	case *ast.MappingKeyNode:
		n.Value = r.replace(n.Value)

	case *ast.MappingValueNode:
		if key, ok := r.replace(n.Key).(ast.MapKeyNode); ok {
			n.Key = key
		}

		n.Value = r.replace(n.Value)

	case *ast.SequenceNode:
		for i, elem := range n.Values {
			n.Values[i] = r.replace(elem)
		}

		// The go-yaml formatter writes the elements from the entries.
		for _, entry := range n.Entries {
			if entry != nil {
				entry.Value = r.replace(entry.Value)
			}
		}
	}

	return r
}

// replace returns the node that node maps to, or node itself when it
// maps to none.
func (r nodeReplacer) replace(node ast.Node) ast.Node {
	if v, ok := r[node]; ok {
		return v
	}

	return node
}

// renameAlias gives alias, a node of a second parse, a name free of
// comments, as [decodeView] does, and changes that name to name when name
// is not empty. The decoder looks an alias up by the value of the token
// of its name, and by the text of the node of its name.
func renameAlias(alias *ast.AliasNode, name string) {
	tk := nodeToken(alias.Value)
	if tk == nil {
		return
	}

	if name != "" {
		tk.Value = name
	}

	alias.Value = ast.String(tk)
}

// view returns the node of the tree that stands for node, a node of the
// document outside its comments. A node the tree does not hold, which no
// node of the document is, comes back as [decodeView] reads it.
func (t *decodeTree) view(node ast.Node) ast.Node {
	if node == t.source {
		return t.body
	}

	t.scoped()

	if v, ok := t.nodes[node]; ok {
		return v
	}

	return decodeView(node)
}

// index returns the [*anchorIndex] of the tree.
func (t *decodeTree) index() *anchorIndex {
	t.scoped()

	return t.anchors
}

// scoped fills the parts of the tree that only a decode of a node below
// the body reads, on the first call.
func (t *decodeTree) scoped() {
	t.scopedOnce.Do(func() {
		if t.nodes == nil {
			t.nodes, _ = pairNodes(t.source, t.body)
		}

		t.anchors = newAnchorIndex(t.body)
	})
}

// unresolvedMerge returns the first alias under a `<<` merge key inside
// scope, a node of the tree, that the decoder finds no mapping for and
// that err, which the decoder returned for scope, reports. It returns nil
// when scope holds no such alias. The decoder finds no mapping for an
// alias that names no anchor before it as resolver binds it, or for one
// inside the anchor it names, which the decoder has not finished reading
// when it merges.
//
// The document's resolver knows nothing of the reference documents a
// decode may carry, from [WithReferences] or the yaml.Reference options,
// and the decoder merges an alias that one of them defines. So err
// reports an alias only when it is the decoder's own failure for that
// alias. That is the message the decoder gives an alias it finds no
// anchor for, and, for an alias inside the anchor it names, any
// [yaml.Error] whose token is not one of the tree's, such as the null the
// decoder merges in its place.
func (t *decodeTree) unresolvedMerge(resolver *paths.Resolver, scope ast.Node, err error) *mergeAlias {
	t.mergesOnce.Do(func() {
		t.merges = unresolvedMerges(resolver, t.source)
	})

	first, last := tokenBounds(scope)
	if len(first) == 0 {
		return nil
	}

	lo, hi := first[0].Position.Offset, last[0].Position.Offset
	msg := t.restoreNames(err.Error())
	_, yamlErr := err.(yaml.Error) //nolint:errorlint // A wrapped error is the unmarshaler's own.

	for i, m := range t.merges {
		off := m.token.Position.Offset
		if off < lo || off > hi {
			continue
		}

		if msg == m.message() || m.enclosed && yamlErr {
			return &t.merges[i]
		}
	}

	return nil
}

// mergeAlias is an alias under a `<<` merge key that the decoder finds no
// mapping for.
type mergeAlias struct {
	// The token of the alias.
	token *token.Token
	// The name of the alias as the document spells it.
	name string
	// Whether the alias lies inside the anchor it names.
	enclosed bool
}

// message returns the message the decoder gives the alias when a decode
// of the whole document finds no anchor for it.
func (m mergeAlias) message() string {
	return "cannot find anchor by alias name " + m.name
}

// unresolvedMerges returns the aliases under the `<<` merge keys of body
// that the decoder finds no mapping for, as
// [decodeTree.unresolvedMerge] describes, in document order.
func unresolvedMerges(resolver *paths.Resolver, body ast.Node) []mergeAlias {
	var found []mergeAlias

	check := func(node ast.Node) {
		alias, ok := node.(*ast.AliasNode)
		if !ok || alias.Start == nil || alias.Start.Position == nil {
			return
		}

		enclosed := false

		anchor, err := resolver.Anchor(alias)
		if err == nil {
			if !encloses(anchor, alias.Start) {
				return
			}

			enclosed = true
		}

		name, _ := nodeName(alias.Value)
		found = append(found, mergeAlias{token: alias.Start, name: name, enclosed: enclosed})
	}

	for _, n := range sourceNodes(body) {
		entry, ok := n.(*ast.MappingValueNode)
		if !ok {
			continue
		}

		for _, source := range mergeSources(entry) {
			check(source)
		}
	}

	sort.SliceStable(found, func(i, j int) bool {
		return found[i].token.Position.Offset < found[j].token.Position.Offset
	})

	return found
}

// mergeSources returns the nodes the `<<` merge key of entry merges, the
// value of the entry or each element of a sequence there, under their
// anchors and tags. It returns nil when entry holds no merge key.
func mergeSources(entry *ast.MappingValueNode) []ast.Node {
	if entry.Key == nil || !entry.Key.IsMergeKey() {
		return nil
	}

	value := unwrapNode(entry.Value)

	seq, ok := value.(*ast.SequenceNode)
	if !ok {
		return []ast.Node{value}
	}

	sources := make([]ast.Node, 0, len(seq.Values))
	for _, elem := range seq.Values {
		sources = append(sources, unwrapNode(elem))
	}

	return sources
}

// encloses reports whether tk lies among the tokens under node.
func encloses(node ast.Node, tk *token.Token) bool {
	first, last := tokenBounds(node)
	if len(first) == 0 {
		return false
	}

	off := tk.Position.Offset

	return off >= first[0].Position.Offset && off <= last[0].Position.Offset
}

// restoreNames returns msg with each name the tree gave an anchor spelled
// as the document does.
func (t *decodeTree) restoreNames(msg string) string {
	if t.names == nil {
		return msg
	}

	return t.names.Replace(msg)
}

// restoreError returns err with a message that spells each name the tree
// gave an anchor as the document does. It returns err itself when the
// message names no such anchor, and when err holds an [*Error] or a
// [*SourceError], whose message the binding writes from its parts.
func (t *decodeTree) restoreError(err error) error {
	if t.names == nil {
		return err
	}

	var (
		located *Error
		bound   *SourceError
	)

	if errors.As(err, &located) || errors.As(err, &bound) {
		return err
	}

	msg := err.Error()

	restored := t.names.Replace(msg)
	if restored == msg {
		return err
	}

	return restoredError{err: err, msg: restored}
}

// restoredError is an error from the decoder with a message that spells
// the names of anchors as the document does. It unwraps to the error the
// decoder returned.
type restoredError struct {
	err error
	msg string
}

func (e restoredError) Error() string {
	return e.msg
}

func (e restoredError) Unwrap() error {
	return e.err
}

// renamedAnchorNames returns the names of the anchors under body that
// the tree gives a name of their own. Those are the names more than one
// anchor has, and the names of anchors that follow an alias of their name
// that resolver binds to no anchor. The decoder would read such an alias
// as the later anchor, although it names no anchor of the document before
// it and reads a reference document's anchor, if any.
func renamedAnchorNames(resolver *paths.Resolver, body ast.Node) map[string]bool {
	anchors := map[string]int{}
	unbound := map[string]bool{}

	for _, n := range sourceNodes(body) {
		switch n := n.(type) {
		case *ast.AnchorNode:
			if name, ok := nodeName(n.Name); ok {
				anchors[name]++
			}

		case *ast.AliasNode:
			_, err := resolver.Anchor(n)
			if err == nil {
				continue
			}

			if name, ok := nodeName(n.Value); ok {
				unbound[name] = true
			}
		}
	}

	names := map[string]bool{}

	for name, count := range anchors {
		if count > 1 || unbound[name] {
			names[name] = true
		}
	}

	return names
}

// bracketedNames returns the names of the anchors and aliases among nodes
// that hold " [", the start of a name that [document.parsedTree] gives
// an anchor.
func bracketedNames(nodes []ast.Node) []string {
	var names []string

	seen := map[string]bool{}

	for _, n := range nodes {
		var name string

		switch n := n.(type) {
		case *ast.AnchorNode:
			name, _ = nodeName(n.Name)

		case *ast.AliasNode:
			name, _ = nodeName(n.Value)
		}

		if !strings.Contains(name, " [") || seen[name] {
			continue
		}

		seen[name] = true
		names = append(names, name)
	}

	return names
}

// spelledIn reports whether any of names holds name.
func spelledIn(names []string, name string) bool {
	for _, n := range names {
		if strings.Contains(n, name) {
			return true
		}
	}

	return false
}

// pairNodes maps each node under a, outside its comments, to the node in
// the same place under b. It reports false when the two trees differ in
// shape.
func pairNodes(a, b ast.Node) (map[ast.Node]ast.Node, bool) {
	from, to := sourceNodes(a), sourceNodes(b)
	if len(from) != len(to) {
		return nil, false
	}

	pairs := make(map[ast.Node]ast.Node, len(from))

	for i, n := range from {
		if n.Type() != to[i].Type() {
			return nil, false
		}

		pairs[n] = to[i]
	}

	return pairs, true
}

// sourceNodes returns the nodes under node outside its comments, in the
// order [ast.Walk] visits them.
func sourceNodes(node ast.Node) []ast.Node {
	var nodes nodeCollector

	ast.Walk(&nodes, node)

	return nodes
}

// nodeCollector is an [ast.Visitor] that collects the nodes it visits,
// and leaves out comments.
type nodeCollector []ast.Node

// Visit implements [ast.Visitor].
func (c *nodeCollector) Visit(node ast.Node) ast.Visitor {
	if astnode.IsNil(node) || node.Type() == ast.CommentType {
		return nil
	}

	*c = append(*c, node)

	return c
}

// anchorIndex lists the anchors of a [decodeTree], so a decode of a node
// below the body primes the anchors its aliases need without reading the
// whole body.
//
// Create instances with [newAnchorIndex].
type anchorIndex struct {
	// The anchors of each name, as indexes into entries.
	named map[string][]int
	// The anchors, in document order.
	entries []anchorEntry
}

// anchorEntry is an anchor, which registers its name and the names of the
// anchors inside it when the decoder reads it.
type anchorEntry struct {
	node *ast.AnchorNode
	// The names the aliases under node read.
	reads []string
	// The offsets of the first and the last token under node.
	start, end int
	// The index of the innermost entry that holds node, or -1.
	parent int
}

// newAnchorIndex creates a new [*anchorIndex] for body.
func newAnchorIndex(body ast.Node) *anchorIndex {
	idx := &anchorIndex{named: map[string][]int{}}

	for _, node := range sourceNodes(body) {
		anchor, ok := node.(*ast.AnchorNode)
		if !ok {
			continue
		}

		first, last := tokenBounds(anchor)
		if len(first) == 0 {
			continue
		}

		idx.entries = append(idx.entries, anchorEntry{
			node:  anchor,
			reads: setKeys(namesOf(anchor).reads),
			start: first[0].Position.Offset,
			end:   last[0].Position.Offset,
		})
	}

	// The walk visits a node before the nodes inside it, and a stable sort
	// keeps that order for nodes that start at one offset.
	sort.SliceStable(idx.entries, func(i, j int) bool {
		return idx.entries[i].start < idx.entries[j].start
	})

	// The entries that hold the current one, innermost last.
	var holders []int

	for i := range idx.entries {
		e := &idx.entries[i]

		for len(holders) > 0 && idx.entries[holders[len(holders)-1]].end < e.start {
			holders = holders[:len(holders)-1]
		}

		e.parent = -1
		if len(holders) > 0 {
			e.parent = holders[len(holders)-1]
		}

		holders = append(holders, i)

		if name, ok := nodeName(e.node.Name); ok {
			idx.named[name] = append(idx.named[name], i)
		}
	}

	return idx
}

// candidate returns the index of the entry whose decode registers the
// anchor of entry i for a node that starts at offset start, or -1 when no
// such decode precedes the node. That is the outermost entry that holds
// the anchor and ends before the node, or the anchor itself when it holds
// the node, which [pendingAnchor] then stands for.
func (idx *anchorIndex) candidate(i, start int) int {
	e := idx.entries[i]

	switch {
	case e.start >= start:
		return -1

	case e.end >= start:
		return i
	}

	for {
		p := idx.entries[i].parent
		if p < 0 || idx.entries[p].end >= start {
			return i
		}

		i = p
	}
}

// primeAnchors registers with dec the anchors that the aliases in node,
// a node of the [decodeTree] below its body, refer to. The pass decodes
// the anchors that end before node, in document order and each on its
// own. It decodes only the ones that register a name node reads, or a
// name read by one it decodes after them, so its cost follows the anchors
// node needs rather than the size of the document. An anchor that holds
// node counts for the anchors inside it that end before node, and
// registers its own name as null. A decode of the whole document reads an
// alias inside an anchor to that anchor as null too, into every target,
// since the [decodeTree] holds a null in its place.
//
// Every anchor of the tree has a name of its own, so the pass decodes
// only anchors that node, or an anchor node reads, refers to. A failure
// in one of them would leave an alias with nothing to read, so the pass
// stops and returns it, as a decode of the whole document fails there
// too, as [Node.rejection] reads it for that anchor. A context that ends
// stops the pass, which then returns the error of the context.
func (n *Node) primeAnchors(ctx context.Context, dec *yaml.Decoder, node ast.Node) error {
	idx := n.doc.decodeTree().index()
	if len(idx.entries) == 0 {
		return nil
	}

	reads := namesOf(node).reads
	if len(reads) == 0 {
		return nil
	}

	first, _ := tokenBounds(node)
	if len(first) == 0 {
		return nil
	}

	start := first[0].Position.Offset

	// For each name something reads, the index of the last entry that
	// reads it. Node reads after every entry.
	readBy := make(map[string]int, len(reads))
	work := make([]string, 0, len(reads))

	for name := range reads {
		readBy[name] = len(idx.entries)
		work = append(work, name)
	}

	// Keep each entry that registers a name an entry after it, or node,
	// reads, and add the names it reads itself.
	kept := map[int]bool{}

	for len(work) > 0 {
		name := work[len(work)-1]
		work = work[:len(work)-1]

		for _, i := range idx.named[name] {
			c := idx.candidate(i, start)
			if c < 0 || c >= readBy[name] || kept[c] {
				continue
			}

			kept[c] = true

			// An anchor that holds node reads nothing before node.
			if idx.entries[c].end >= start {
				continue
			}

			for _, read := range idx.entries[c].reads {
				if last, ok := readBy[read]; ok && last >= c {
					continue
				}

				readBy[read] = c
				work = append(work, read)
			}
		}
	}

	order := make([]int, 0, len(kept))
	for i := range kept {
		order = append(order, i)
	}

	sort.Ints(order)

	for _, i := range order {
		// Without an anchor it needs, the decode of the node would fail
		// in its stead.
		err := ctx.Err()
		if err != nil {
			return err //nolint:wrapcheck // The context names the reason, and the Node binds it.
		}

		anchor := idx.entries[i].node
		if idx.entries[i].end >= start {
			anchor = pendingAnchor(anchor)
		}

		var sink any

		err = decodeWithRecover(ctx, dec, anchor, &sink)
		if err != nil {
			return n.rejection(err, anchor)
		}
	}

	return nil
}

// pendingAnchor returns an anchor with the name of anchor over a null.
// While the decoder reads the value of an anchor into an interface, it
// registers the name as null, so an alias inside that value reads as
// null. A decode of the pending anchor registers the name that way for a
// node inside anchor.
func pendingAnchor(anchor *ast.AnchorNode) *ast.AnchorNode {
	var pos *token.Position

	if anchor.Start != nil {
		pos = anchor.Start.Position
	}

	return &ast.AnchorNode{
		BaseNode: &ast.BaseNode{},
		Start:    anchor.Start,
		Name:     anchor.Name,
		Value:    ast.Null(token.New("null", "null", pos)),
	}
}

// namesOf returns the names of the anchors a decode of node registers,
// and the names its aliases read.
func namesOf(node ast.Node) *anchorNames {
	names := &anchorNames{
		registers: map[string]struct{}{},
		reads:     map[string]struct{}{},
	}

	ast.Walk(names, node)

	return names
}

// anchorNames is an [ast.Visitor] that collects the names of the anchors
// the nodes it visits define and the names of the aliases they hold.
type anchorNames struct {
	registers map[string]struct{}
	reads     map[string]struct{}
}

// Visit implements [ast.Visitor].
func (a *anchorNames) Visit(node ast.Node) ast.Visitor {
	if astnode.IsNil(node) {
		return nil
	}

	switch n := node.(type) {
	case *ast.AnchorNode:
		if name, ok := nodeName(n.Name); ok {
			a.registers[name] = struct{}{}
		}

	case *ast.AliasNode:
		if name, ok := nodeName(n.Value); ok {
			a.reads[name] = struct{}{}
		}
	}

	return a
}

// nodeName returns the value of the token of node, the name the decoder
// gives an anchor or looks an alias up by.
func nodeName(node ast.Node) (string, bool) {
	tk := nodeToken(node)
	if tk == nil {
		return "", false
	}

	return tk.Value, true
}

// nodeToken returns the token of node, or nil for a nil node, including
// a typed nil a hand-built tree may hold.
func nodeToken(node ast.Node) *token.Token {
	if astnode.IsNil(node) {
		return nil
	}

	return node.GetToken()
}

// setKeys returns the keys of set.
func setKeys(set map[string]struct{}) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}

	return keys
}
