package edit

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/goccy/go-yaml/ast"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/hook"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

// Node is a handle on one place of a [Doc]: the root, an entry of a
// mapping, or an item of a sequence. It reads the value there and edits the
// document. Each edit keeps the path of the node up to date. A node on a
// value that an edit renamed or moved thus names its new place. After a Set
// of its place, a node reads the new value. After a Move of its value, it
// follows the value to the target. Each edit of a node fails after an edit
// removed its place, either directly or by replacing a value above it. The
// edit can reach the place through another path than the node, such as the
// anchor of an alias on the path of the node. The problem binds where a
// problem at the place of the edit that removed the place bound before that
// edit. It has the path of that problem, as the Errors section of the
// package documentation gives it, and its details name calls by the rule of
// that section. A place that the user wrote thus gives its own path. A
// place that an earlier call created gives the path of its site. The
// message of the problem names the place that the call asked for. Decode
// through such a node fails with that problem, and Exists, Kind, and Text
// answer as they do for a place that does not hold a value. They do so
// even when a later edit writes that place again. A new node from
// [Node.At] reads and edits the place.
//
// Through any other node, a read returns what the text holds. A call after
// which the text differs from the model of the Apply fails with an error
// that matches [ErrEngine], as the package documentation describes. Each
// later edit and each Decode of the Apply then returns that error, and
// Exists, Kind, and Text answer as they do for a place that does not hold
// a value. The Apply fails with that error whatever the Func returns.
//
// The place of a node holds the value that the model of the Apply holds
// there, as the package documentation describes. A node whose place did
// not hold a value takes the value that a later edit writes there.
//
// When a method cannot make its edit, it returns an error that names the
// problem. [Doc.Apply] then fails with that error, even when the [Func]
// continues and returns nil. A read that fails returns its error to the
// Func, and the Apply fails with that error only when the Func returns it or
// when the error matches [ErrEngine]. A node takes edits until the Apply
// that gave it returns. After that, each method that edits or decodes
// returns an error. Exists, Kind, and Text then answer as they do for a
// place that does not hold a value.
//
// Receive instances as the argument of a [Func], or from [Node.At].
type Node struct {
	d *Doc
	// The problem of the path that At took, which each method that edits or
	// decodes returns, or nil.
	err pathProblem
	// The edge of the model at the place of the node, as the first edit
	// after the node took its place found it, or nil when the place held
	// no value. Slotted reports whether the node holds it.
	slot *edge
	// The record of the edit of the call that removed the place of the
	// node, or nil when the Apply did not record it.
	goneBy *editRecord
	// Each entry on the path of the node that a later entry of its key
	// hides, the outermost first, or nil. No path selects such an entry, so
	// the path of the node reads through each of them.
	hidden []hiddenEntry
	path   paths.Path
	// Whether an edit removed the place of the node.
	gone bool
	// Whether the Apply that gave the node returned.
	ended   bool
	slotted bool
}

// newNode returns a node on the place at path, a `$` path of names and
// indexes, which might not hold a value.
func (d *Doc) newNode(path paths.Path) *Node {
	n := &Node{d: d, path: path}
	d.nodes = append(d.nodes, n)

	return n
}

// hiddenEntry is an entry that a later entry of its key hides in its
// mapping, which the Source allows with [niceyaml.WithAllowDuplicateKeys].
// No path selects such an entry, so a node on it or below it holds it. The
// path of the node names its key, and the selectors of the path after the
// key read below the entry.
type hiddenEntry struct {
	// The place of the key of the entry, as [Doc.entryPlace] gives it.
	at position.Position
	// The number of selectors of the path of the node through the key.
	depth int
}

// through returns the entries of hidden, the entries that a later entry of
// their key hides on from, a path, that path leads through as from does.
// Path then shares the selectors of from through the key of each.
func through(hidden []hiddenEntry, from, path paths.Path) []hiddenEntry {
	for i, h := range hidden {
		if path.Len() < h.depth {
			return hidden[:i]
		}

		if _, ok := path.CutPrefix(prefixOf(from, h.depth)); !ok {
			return hidden[:i]
		}
	}

	return hidden
}

// entryOfPlace returns the entry of the mapping content whose key has the place
// at, as [Doc.entryPlace] gives it, or nil.
func (d *Doc) entryOfPlace(content ast.Node, at position.Position) *ast.MappingValueNode {
	entries, _ := readEntries(content)

	for _, e := range entries {
		if !astnode.IsMergeKey(e.Key) && d.entryPlace(e) == at {
			return e
		}
	}

	return nil
}

// selection collects the places that the path of an Apply selects, as
// [Doc.matches] gives them.
type selection struct {
	// The entries before each entry of a mapping that a later entry of
	// their key hides, by the mapping, as [Doc.hiddenIn] gives them.
	hidden map[ast.Node]map[*ast.MappingValueNode][]*ast.MappingValueNode
	seen   map[any]bool
	found  []selected
	// The path of the Apply.
	path paths.Path
}

// selected is a place that a path selects, with the first byte of its key,
// its "-", or its value.
type selected struct {
	hidden []hiddenEntry
	path   paths.Path
	start  int
}

// matches returns a node on each place that path selects, in document
// order. Each entry and each item of the text is a place of its own, even
// when its value is an alias. Matches that reach one entry or one item
// through an alias or a `<<` merge key share the node of the first. A
// selector that selects more than one value, such as `.*` or `..name`,
// also selects each entry of a key that it selects whose key a later entry
// repeats. Only a Source that [niceyaml.WithAllowDuplicateKeys] gives
// holds such entries. The selectors after the selector read below them.
// Matches leaves out each place that holds no value, and each place that
// is a protected value or lies inside one, through any alias or merge key.
func (d *Doc) matches(path paths.Path) ([]*Node, error) {
	s := &selection{
		path:   path,
		seen:   make(map[any]bool),
		hidden: make(map[ast.Node]map[*ast.MappingValueNode][]*ast.MappingValueNode),
	}

	err := d.selectFrom(s, nil, paths.Doc(), nil, path)
	if err != nil {
		return nil, err
	}

	slices.SortStableFunc(s.found, func(a, b selected) int { return a.start - b.start })

	nodes := make([]*Node, 0, len(s.found))
	for _, f := range s.found {
		n := d.newNode(f.path)
		n.hidden = f.hidden
		nodes = append(nodes, n)
	}

	return nodes, nil
}

// selectFrom adds to s each place that rel selects from the value from at
// base, a place that hidden reads, as [Doc.matches] describes. For the
// root, from is nil and rel is the `$` path of the Apply. Otherwise from
// is the value of an entry that a later entry of its key hides, at base,
// and rel is an `@` path below it.
func (d *Doc) selectFrom(s *selection, from ast.Node, base paths.Path, hidden []hiddenEntry, rel paths.Path) error {
	res := d.node.Resolver()

	matches, err := res.MatchesFrom(from, rel)
	if err != nil {
		return d.matchProblem(s.path, err)
	}

	for _, m := range matches {
		d.addSelected(s, base.Join(m.Path), hidden)
	}

	sels := slices.Collect(rel.Selectors())

	start := paths.Doc()
	if !rel.IsAbsolute() {
		start = paths.Current()
	}

	for j, sel := range sels {
		recursive := sel.Kind == paths.SelectorRecursive || sel.Kind == paths.SelectorRecursiveAll

		// The entries that the selector lists. A recursive selector lists
		// each entry that it visits.
		var listed []paths.Match

		switch {
		case j == len(sels)-1 && (sel.Kind == paths.SelectorChildAll || sel.Kind == paths.SelectorRecursiveAll):
			listed = matches
		case sel.Kind == paths.SelectorChildAll:
			listed, err = res.MatchesFrom(from, appendSelectors(start, sels[:j+1]))
		case recursive:
			// The matches of rel passed, so a failure here comes from an
			// alias that `..*` lists and `..name` passes by. The decode of
			// the text then fails.
			listed, err = res.MatchesFrom(from, appendSelectors(start, sels[:j]).RecursiveAll())
		default:
			continue
		}

		if err != nil {
			continue
		}

		for _, m := range listed {
			at := base.Join(m.Path)

			for _, h := range d.hiddenAt(s, at, hidden) {
				e := append(slices.Clip(hidden), hiddenEntry{at: d.entryPlace(h), depth: at.Len()})

				// The selector selects the entry itself. A recursive selector
				// reads below it too, unless it holds an alias, which a
				// recursive selector does not follow below its start.
				if sel.Kind != paths.SelectorRecursive || sel.Name == lastName(at) {
					err = d.selectBelow(s, h, at, e, sels[j+1:])
					if err != nil {
						return err
					}
				}

				if _, alias := astnode.Converted(h.Value).(*ast.AliasNode); recursive && !alias {
					err = d.selectBelow(s, h, at, e, sels[j:])
					if err != nil {
						return err
					}
				}
			}
		}
	}

	return nil
}

// selectBelow adds to s each place that sels select from h, the entry at
// path that e names, as [Doc.selectFrom] adds them. Without sels, it adds
// the place of h.
func (d *Doc) selectBelow(
	s *selection,
	h *ast.MappingValueNode,
	path paths.Path,
	e []hiddenEntry,
	sels []paths.Selector,
) error {
	if len(sels) == 0 {
		d.addSelected(s, path, e)

		return nil
	}

	return d.selectFrom(s, h.Value, path, e, appendSelectors(paths.Current(), sels))
}

// lastName returns the name of the last selector of path.
func lastName(path paths.Path) string {
	last, _ := path.Last()

	return last.Name
}

// addSelected adds the place at path, which hidden reads, to s, unless s
// holds it already, it holds no value, or it is a protected value or lies
// inside one.
func (d *Doc) addSelected(s *selection, path paths.Path, hidden []hiddenEntry) {
	p, err := d.walk(path, hidden, nil)
	if err == nil && (!p.exists || d.reachesGuard(p)) {
		return
	}

	key, start := any(path.String()), len(d.cur)

	if err == nil {
		// An item is its sequence and its index.
		switch {
		case p.entry != nil:
			key = p.entry
		case p.seq != nil:
			key = [2]any{p.seq, p.index}
		}

		if s, ok := d.startOf(p); ok {
			start = s
		}
	}

	if s.seen[key] {
		return
	}

	s.seen[key] = true
	s.found = append(s.found, selected{path: path, hidden: hidden, start: start})
}

// hiddenAt returns the entries that a later entry of their key hides
// beside the entry at path, which hidden reads, in the order of the text.
func (d *Doc) hiddenAt(s *selection, path paths.Path, hidden []hiddenEntry) []*ast.MappingValueNode {
	p, err := d.walk(path, hidden, nil)
	if err != nil || !p.exists || p.entry == nil || p.merged {
		return nil
	}

	in, ok := s.hidden[p.container]
	if !ok {
		in = d.hiddenIn(p.container)
		s.hidden[p.container] = in
	}

	return in[p.entry]
}

// hiddenIn returns, for each entry of content, a mapping, the entries before
// it whose keys duplicate its key, in the order of the text. A path names
// such a key as it names the key of the entry, and the parser reads the two
// keys as one, which only a Source that [niceyaml.WithAllowDuplicateKeys]
// gives allows. A path of that name selects the last of them, so it selects
// none of the others: a later entry of their key hides them. An entry that
// a `<<` merge key brings in is not an entry of the mapping that merges it,
// so hiddenIn holds none for it.
func (d *Doc) hiddenIn(content ast.Node) map[*ast.MappingValueNode][]*ast.MappingValueNode {
	res := d.node.Resolver()
	entries, _ := astnode.Entries(content)

	// The parser names each key by its path, and the path of the Apply by
	// its name.
	var (
		before = make(map[[2]string][]*ast.MappingValueNode)
		out    = make(map[*ast.MappingValueNode][]*ast.MappingValueNode)
	)

	for _, e := range entries {
		name, ok := res.KeyName(e.Key)
		if !ok || astnode.IsMergeKey(e.Key) {
			continue
		}

		// The entries before e share the list of their key, which later
		// entries only append to.
		k := [2]string{name, e.Key.GetPath()}
		if len(before[k]) > 0 {
			out[e] = slices.Clip(before[k])
		}

		before[k] = append(before[k], e)
	}

	return out
}

// nodePlace returns the place of n as the text stands: the place at its
// path, read through the entry that n holds when a later entry of its key
// hides it. When the text no longer holds such an entry, the place does not
// hold a value.
func (d *Doc) nodePlace(n *Node) (*place, error) {
	return d.walk(n.path, n.hidden, nil)
}

// guard is a value that [WithProtected] protects in the Apply running.
type guard struct {
	// The node on the protected value at the path where the text writes
	// it. The edits keep that path up to date. A value that no path of the
	// text names, such as an entry of a `<<` merge source that the mapping
	// of the merge key hides, keeps the path that WithProtected gives. The
	// node is nil for a path that holds no value when the Apply starts.
	node *Node
	// The path that WithProtected gives, which messages name.
	path paths.Path
	// The origin of the first byte of the key or the "-" of the protected
	// value, or of the value at the root. For a guard that holds the
	// protected value, it is the origin of the first byte of the value,
	// which a rename of its key keeps. A call that keeps the byte keeps its
	// origin, so the origin finds the value after each call, as
	// [Doc.guardPlace] reads it.
	at int
	// Whether the node holds the protected value, rather than being it, for
	// a path that a reference document decides.
	holds bool
}

// guardValues gives the Apply that starts a guard for each path of
// [WithProtected]. A path that holds a value guards that value, the node
// of the text that the path reaches through any alias or `<<` merge key.
// The guard holds the node by the bytes of its text, as [Doc.guardPlace]
// finds them. An edit of the value through any path thus reaches it, and
// an edit of an alias that reads it does not. A path that holds no value
// guards the path as the Apply found it. No edit reaches a value of a
// reference document. A path that such a document decides, as [Node.At]
// describes, reaches no node. Its lookup stops at an alias of that
// document, so its guard holds the value whose lookup reads the alias
// instead: the alias, or the mapping of the outermost `<<` merge key that
// holds it, as [Doc.ownerPath] names it. That value holds the protected
// value.
func (d *Doc) guardValues() {
	d.guards = nil

	for _, path := range d.cfg.protected {
		g := &guard{path: path}

		p, err := d.resolve(path)
		alias, decided := errors.AsType[*aliasError](err)

		switch {
		case err == nil && p.exists:
			start, _ := d.startOf(p)
			g.node, g.at = d.newNode(d.ownPath(path, p)), d.origin[start]

		case decided && errors.Is(err, paths.ErrReferenceAlias):
			holder := d.ownerPath(alias.node)

			hp, err := d.resolve(holder)
			if err != nil || !hp.exists {
				break
			}

			if s, ok := d.nodeSpan(hp.value); ok && s.start < len(d.origin) {
				g.node, g.at, g.holds = d.newNode(holder), d.origin[s.start], true
			}
		}

		d.guards = append(d.guards, g)
	}
}

// guardPlace returns the place of the protected value of g in the current
// text: the entry, the item, or the root whose key, "-", or value starts
// with a byte of the origin of g. The path of the node of g picks that
// place where it reaches one. Otherwise, as after an edit of an alias on
// that path, the first entry of the text whose key starts with that byte
// does. Each call keeps the bytes of a protected value. It reports false
// when the text does not hold the value.
func (d *Doc) guardPlace(g *guard) (*place, bool) {
	if g.node == nil {
		return nil, false
	}

	starts := func(p *place) bool {
		start, ok := d.startOf(p)
		if g.holds {
			var s span

			s, ok = d.nodeSpan(p.value)
			start = s.start
		}

		return ok && start < len(d.origin) && d.origin[start] == g.at
	}

	p, err := d.resolve(g.node.path)
	if !g.node.gone && err == nil && p.exists && starts(p) {
		return p, true
	}

	var found *place

	d.inspect(func(n ast.Node) bool {
		if x, ok := n.(*ast.MappingValueNode); ok {
			if p := entryPlace(nil, x); starts(p) {
				found = p
			}
		}

		return found == nil
	})

	return found, found != nil
}

// ownPath returns the path where the text writes the value of p, the place
// at path. That path holds the names and the indexes of the entries and
// the items that hold the value, through no alias. A `<<` merge key adds
// no step, because a path reads the entries that it brings in as entries
// of its mapping. The function returns path when that path does not reach
// the entry or the item of p. A value inside a key does not have such a
// path, and neither does a value that another entry of its name hides.
func (d *Doc) ownPath(path paths.Path, p *place) paths.Path {
	if !p.shared {
		return path
	}

	var (
		sels []paths.Selector
		// The number of steps below an item whose sequence might hold the
		// sources of a merge key, or -1.
		items = -1
		root  = d.node.AST()
		child = p.value
	)

	for _, up := range d.ancestors(p.value) {
		if child == root {
			break
		}

		switch x := up.(type) {
		case *ast.MappingValueNode:
			name, named := d.node.Resolver().KeyName(x.Key)

			switch merge := astnode.IsMergeKey(x.Key); {
			case x.Value != child, !merge && !named:
				return path
			case merge && items >= 0:
				sels = sels[:items]
			case !merge:
				sels = append(sels, paths.Selector{Kind: paths.SelectorChild, Name: name})
			}

			items = -1

		case *ast.SequenceNode:
			i, ok := d.tree().item[child]
			if !ok {
				return path
			}

			items = len(sels)
			sels = append(sels, paths.Selector{Kind: paths.SelectorIndex, Index: i})

		case *ast.AnchorNode, *ast.TagNode:
		default:
			items = -1
		}

		child = up
	}

	slices.Reverse(sels)

	own := appendSelectors(paths.Doc(), sels)

	q, err := d.resolve(own)
	if err != nil || !q.exists || q.entry != p.entry || q.seq != p.seq || q.index != p.index {
		return path
	}

	return own
}

// protection returns the reason that edits cannot change p, a place of
// the text, in the words that a message uses after "which". It returns ""
// for a place that edits can change. Edits cannot change a protected value or
// a value inside one. With holds set, they cannot change a value that
// holds one either, because a set or a removal of that value takes the
// protected value out of the text. A path through an alias or a `<<` merge
// key reaches the protected value as any other path does.
func (d *Doc) protection(p *place, holds bool) string {
	for _, g := range d.guards {
		if g.node == nil {
			continue
		}

		if why := d.guarded(g, p); why != "" && (holds || why != whyHolds) {
			return why
		}
	}

	return ""
}

// reachesGuard reports whether p, a place of the text, is a protected
// value or lies inside one.
func (d *Doc) reachesGuard(p *place) bool {
	return d.protection(p, false) != ""
}

// The reasons of [Doc.guarded].
const (
	whyIs     = "is a protected value"
	whyInside = "lies inside a protected value"
	whyHolds  = "holds a protected value"
)

// guarded returns the reason that edits cannot change p, a place of the
// text, for the protected value of g, as [Doc.protection] describes. The
// protected value holds the values that the aliases inside it read, so a
// place inside such a value lies inside the protected value too. It
// returns "" when the text does not hold the protected value.
func (d *Doc) guarded(g *guard, p *place) string {
	gp, ok := d.guardPlace(g)
	if !ok {
		return ""
	}

	same := p.root && gp.root || p.entry != nil && p.entry == gp.entry ||
		p.seq != nil && p.seq == gp.seq && p.index == gp.index

	// The node of a guard that holds the protected value guards it only from
	// a call that takes the node out of its place.
	if g.holds {
		if p.exists && (same || d.liesIn(placeNode(gp), p.value)) {
			return whyHolds
		}

		return ""
	}

	read := d.readValues(gp.value)

	if !p.exists {
		// A call that creates the place writes into its container.
		c := p.container
		holds := func(v ast.Node) bool {
			content, _, err := d.readValue(v)

			return err == nil && content == c || d.liesIn(c, v)
		}

		if c != nil && slices.ContainsFunc(read, holds) {
			return whyInside
		}

		return ""
	}

	switch {
	case same:
		return whyIs
	case slices.ContainsFunc(read, func(v ast.Node) bool { return d.liesIn(placeNode(p), v) }):
		return whyInside
	case d.liesIn(placeNode(gp), p.value):
		return whyHolds
	}

	return ""
}

// readValues returns value, a value of the text, and each value of the
// text that an alias inside value reads, or inside such a value in turn.
func (d *Doc) readValues(value ast.Node) []ast.Node {
	res := d.node.Resolver()
	out := []ast.Node{value}
	seen := map[ast.Node]bool{value: true}

	for i := 0; i < len(out); i++ {
		astnode.Inspect(out[i], func(n ast.Node) bool {
			alias, ok := n.(*ast.AliasNode)
			if !ok {
				return true
			}

			target, err := res.Deref(alias)
			if err == nil && !seen[target] {
				seen[target] = true
				out = append(out, target)
			}

			return true
		})
	}

	return out
}

// placeNode returns the node of the text that holds p: its entry, or its
// value for an item or the root.
func placeNode(p *place) ast.Node {
	if p.entry != nil {
		return p.entry
	}

	return p.value
}

// guardTexts returns the text of each protected value as the text stands,
// or "" for a guard of a path that held no value, as [Doc.guardChanged]
// reads them after a call.
func (d *Doc) guardTexts() []string {
	if len(d.guards) == 0 {
		return nil
	}

	texts := make([]string, len(d.guards))

	for i, g := range d.guards {
		texts[i] = d.guardText(g)
	}

	return texts
}

// guardText returns the text of the protected value of g, from the first
// byte of its key or its "-" through the end of the value. It returns ""
// when the text does not hold the value.
func (d *Doc) guardText(g *guard) string {
	p, ok := d.guardPlace(g)
	if !ok {
		return ""
	}

	start, ok := d.startOf(p)
	if !ok {
		return ""
	}

	return d.cur[start:max(start, d.valueEnd(p))]
}

// guardChanged returns the problem of the call at path when it changed
// the text of a protected value. The texts of the protected values before
// the call are texts, as [Doc.guardTexts] gave them. It also returns the
// problem of a call that created a value at a protected path that held
// none. The problem binds at the entry or the item that the call edited. A write
// of a type field gives no texts, so the check passes the protected value
// that it writes.
func (d *Doc) guardChanged(path paths.Path, texts []string) error {
	if len(texts) != len(d.guards) {
		return nil
	}

	for i, g := range d.guards {
		verb := "changes"

		// A call inside the value that holds the protected value of such a
		// guard can change its text, and the reference document still
		// decides the protected value.
		if g.holds {
			continue
		}

		if g.node == nil {
			p, err := d.resolve(g.path)
			if err != nil || !p.exists {
				continue
			}

			verb = "creates"
		} else if d.guardText(g) == texts[i] {
			continue
		}

		rec := d.lastEdit
		if rec == nil {
			return d.fail(&Node{d: d, path: path}, "%s edits %s, which %s the protected value %s",
				d.subject(), path, verb, g.path)
		}

		msg := fmt.Sprintf("%s %s, which %s the protected value %s", d.subject(), rec.named, verb, g.path)
		err := d.user.Bind(niceyaml.NewError(msg, niceyaml.AtPath(rec.holder()), niceyaml.AtRange(rec.at),
			niceyaml.WithDetails(d.callDetails(rec.site())...)))
		d.record(err)

		return err //nolint:wrapcheck // The binding is the problem of the call.
	}

	return nil
}

// Path returns the `$` path of the place as the document stands now. For a
// node on an entry that a later entry of its key hides, which no path
// selects, it returns the path of that key, as [Doc.Apply] describes. So
// does a node below such an entry, whose path names the key of the entry
// and the place below it.
func (n *Node) Path() paths.Path {
	return n.path
}

// Key returns the name of the entry that the path spells, as
// [paths.Resolver.KeyName] gives it, or "" for a sequence item or the
// root. The name is the text of the key without its quotes, so it is
// "a b" for the key "a b".
func (n *Node) Key() string {
	last, ok := n.path.Last()
	if !ok || last.Kind != paths.SelectorChild {
		return ""
	}

	return last.Name
}

// place returns the place of n, or the error that each method that edits
// or decodes returns for a node that cannot take the call. The call uses
// the place as u says, and a problem of its path names verb, the word for
// what the call does, as [pathProblem] names it. A node whose place an earlier call
// took away fails before any other check of the call, as
// [Doc.goneProblem] tells. The error of the path that At took
// fails the Apply for a call that edits, so the Apply records it then.
// A call that edits fails at a zone of the Apply that a step of the path
// before that error reaches instead, as [Doc.stepProblem] tells. After a
// call of the Apply that left the text apart from the model of the calls,
// each call returns the problem of that call, as [Doc.modelEnd] gives it.
func (n *Node) place(u use, verb string) (*place, error) {
	switch {
	case n.ended:
		return nil, errEnded
	case n.gone:
		return nil, n.d.goneProblem(n, verb)
	case n.err != nil:
		err := n.err(verb)
		if u == useRead {
			return nil, err
		}

		refused := n.stepProblem()
		if refused != nil {
			return nil, refused
		}

		n.d.record(err)

		return nil, err
	}

	if n.d.stuck != nil {
		return nil, n.d.stuck
	}

	if n.d.displaced(n) {
		return nil, n.d.displacedProblem(n, u)
	}

	p, err := n.d.nodePlace(n)
	if err != nil && u != useRead {
		refused := n.stepProblem()
		if refused != nil {
			return nil, refused
		}
	}

	if err != nil {
		return nil, n.d.readFail(n, fmt.Errorf("%s %s %s, which %w", n.d.subject(), verb, n.path, err))
	}

	return p, nil
}

// stepProblem returns the problem of the call running through n, whose
// path meets a problem or selects a place that the text lacks, when a step
// of the path reaches a zone of the Apply, as [Doc.stepProblem] tells, or
// nil.
func (n *Node) stepProblem() error {
	return n.d.stepProblem(reachCall{path: n.path, hidden: n.hidden, named: n.d.callNamed})
}

// pathProblem returns the problem of the path that [Node.At] took, as a
// call through the node meets it. The problem names verb, the word for what
// the call does with its place: Decode reads, Set sets, Remove removes,
// SetKey renames, and Move moves. So it reads as in "the edit sets $.a.x,
// which goes through *r". The problem binds when the call meets it, so its
// details name the call, as [Doc.callDetails] names the call that runs.
type pathProblem func(verb string) error

// read returns the place of n for Exists, Kind, and Text, with its value
// as a [*niceyaml.Node] of the current text. They answer as they do for a
// place that does not hold a value when read reports false. Read reports
// false after the Apply of n returned, and for a node whose path failed,
// whose place an edit took away, or whose path reads another value. It
// also reports false at a value that no decode reads, such as an alias
// without an anchor, and after a call of the Apply that left the text apart
// from the model of the calls.
func (n *Node) read() (*place, *niceyaml.Node, bool) {
	if n.ended || n.err != nil || n.gone {
		return nil, nil, false
	}

	if n.d.stuck != nil || n.d.displaced(n) {
		return nil, nil, false
	}

	p, err := n.d.nodePlace(n)
	if err != nil || !p.exists {
		return nil, nil, false
	}

	v, err := n.d.view(n, p)
	if err != nil {
		return nil, nil, false
	}

	return p, v, true
}

// Exists reports whether the place holds a value. An alias that does not
// resolve, such as one without an anchor, holds no value that a decode
// reads, so Exists reports false there. Exists also reports false at a path
// that a reference document of [niceyaml.WithReferences] decides, such as a
// path below an alias of that reference document, where [Node.Decode] fails,
// as [Node.At] describes. A [Func] tells such a place from a place without a
// value by the error of Decode. Exists reports false through a node that
// lost its place to an earlier edit, as [Node] describes.
func (n *Node) Exists() bool {
	_, _, ok := n.read()

	return ok
}

// Kind returns the kind of the value, or [niceyaml.NodeNone] when the
// place does not hold a value, as [Node.Exists] reports it. A path never
// selects a `<<` merge key, as [Node.At] describes, so Kind returns
// NodeNone at a path that ends at one. Kind returns NodeNone at a path that
// a reference document decides too. At an alias that reads a reference
// document, Kind returns the kind of the value of its anchor.
func (n *Node) Kind() niceyaml.NodeKind {
	_, v, ok := n.read()
	if !ok {
		return niceyaml.NodeNone
	}

	return v.Kind()
}

// Text returns the string that a decode reads from a scalar, before a tag
// converts it, so 0x10 gives "0x10". The string has the quotes, the
// escapes, and the folding of the scalar resolved. For a block scalar, it
// holds the lines after the header, such as "|" or ">-", with the
// indentation, the folding, and the chomping of the block applied. Text
// returns "" for a mapping, a sequence, and a place that does not hold a
// value. At an alias, Text reads the anchor as a decode does. It thus
// returns the text of the scalar that the anchor holds, in the document
// or in a reference document of [niceyaml.WithReferences]. It returns ""
// for an alias that does not resolve, and at a path that a reference
// document decides, as [Node.Exists] describes. After a call of the Apply
// that left the text apart from the model, Text returns "".
//
//nolint:contextcheck // The decode of such a scalar reads no state that a context carries.
func (n *Node) Text() string {
	p, doc, ok := n.read()
	if !ok {
		return ""
	}

	content := hook.Content(n.d.node, p.value)
	if _, ok := content.(*ast.TagNode); !ok {
		text, _ := astnode.ScalarText(content)

		return text
	}

	v, err := doc.Decode[any](context.Background())
	if err != nil || v == nil {
		return ""
	}

	if s, ok := v.(string); ok {
		return s
	}

	return fmt.Sprint(v)
}

// Decode decodes the value as [niceyaml.Node.Decode] does, with opts and
// the settings of the [niceyaml.Source] of the document, such as its
// reference documents and its unmarshalers. The unmarshalers of
// [WithUnmarshalers] apply ahead of those of the Source. A [Func] should
// pass the ctx it got, so the warnings of the decode reach the
// [niceyaml.Warnings] on the context of [Doc.Apply]. Decode fails on a
// place that does not hold a value, and through a node that lost its place
// to an earlier edit, as [Node] describes. Such an error belongs to the
// Func, so the Apply fails with it only when the Func returns it. Decode
// returns the error of a call of the Apply after which the text differed
// from the model, as [Node] describes. That error matches [ErrEngine], and
// the Apply fails with it whatever the Func returns.
//
// An error of the decode keeps the path that it names in the current text.
// The decoder raises such an error for a type mismatch, and so do a schema
// of [niceyaml.WithValidator] and a check of [niceyaml.WithValueCheck]. The
// error binds in the user's text as an error that a caller binds at that
// path, as the package documentation describes. It reads as the error of
// the same decode through [Doc.Node].
//
// At an alias that reads a reference document of [niceyaml.WithReferences],
// Decode reads the anchor through the reference documents, as
// [niceyaml.Node.Decode] does at such an alias. At a path that a reference
// document decides, as [Node.At] describes, Decode returns the problem of
// the path, which matches [paths.ErrReferenceAlias] and binds at the alias.
// A Func reads such a value through the alias, or through the mapping that
// holds the `<<` merge key, whose Decode reads the entries that the merge
// key brings in.
func (n *Node) Decode[T any](ctx context.Context, opts ...niceyaml.DecodeOption) (T, error) {
	var zero T

	recorded := n.d.callErr

	// A read is a call, so each problem of the read names it, as
	// [Doc.callDetails] names the call that runs. A read can run inside
	// another call, as a Decode in the MarshalYAML method of the value of a
	// Set does, and that call runs again after the read.
	if !n.ended && n.d.applying {
		outer := n.d.runningCall()
		n.d.startCall("reads "+n.path.String(), nil)

		defer n.d.resumeCall(outer)
	}

	doc, err := n.doc()
	if err != nil {
		if !errors.Is(err, ErrEngine) {
			n.d.callErr = recorded
		}

		return zero, err
	}

	return doc.Decode[T](ctx, opts...)
}

// doc returns the value of the place as a [*niceyaml.Node] of the current
// text, for a decode. It returns the error of a node that cannot decode.
func (n *Node) doc() (*niceyaml.Node, error) {
	p, err := n.place(useRead, "reads")
	if err != nil {
		return nil, err
	}

	if !p.exists {
		return nil, n.d.fail(n, "%s cannot decode %s, which holds no value", n.d.subject(), n.path)
	}

	doc, err := n.d.view(n, p)
	if err == nil {
		return doc, nil
	}

	// An alias at the place that does not resolve, such as one without an
	// anchor, stops the decode there, and the error of the view names it.
	if alias, ok := n.d.aliasProblem(err); ok {
		alias.held = true

		return nil, n.d.readFail(n, fmt.Errorf("%s cannot decode %s, which %w", n.d.subject(), n.path, alias))
	}

	return nil, err
}

// view returns the value of p, the place of n, as a [*niceyaml.Node] of the
// current text. It reads the value as [niceyaml.Node.At] reads the value of
// a path. No path selects a node on an entry that a later entry of its key
// hides, so its view reads the value of that entry under the path of the
// node.
func (d *Doc) view(n *Node, p *place) (*niceyaml.Node, error) {
	if n.hidden == nil {
		return d.node.At(n.path) //nolint:wrapcheck // The Node binds the error.
	}

	value := p.value

	content, err := d.node.Resolver().Deref(value)
	if err == nil {
		value = content
	}

	v, _ := hook.Scope(d.node, n.path, value).(*niceyaml.Node) //nolint:errcheck // The hook gives a *niceyaml.Node.

	return v, nil
}

// At returns a node on the place at path, which might not hold a value. A
// `$` path reads from the root, and an `@` path reads from n. An `@` path
// from a node on an entry that a later entry of its key hides reads below
// that entry, as [Doc.Apply] describes. Exists reports false when the
// place does not hold a value, and only Set changes such a place.
//
// The path selects the place that [paths.Resolver] selects, except that it
// never selects a `<<` merge key itself. The name `<<` selects only a real
// key with that text. A name selects the last entry whose key has that
// text. When a `<<` merge key after that entry brings in a key of the same
// text, the merge key wins, as go-yaml's decoder reads the mapping. The
// merge spec keeps the entry there, so [New] refuses such a document when
// the merge key reads a mapping of the document, as the package
// documentation describes. When the merge key reads a reference document,
// a path through the entry fails instead.
//
// Each method of the node that edits or decodes fails on a path that
// selects more than one value or holds a `~`. So does each such method on
// a path through an alias that does not resolve in the text, such as one
// without an anchor. The problem then binds at the alias. A path that
// enters an alias of a reference document of [niceyaml.WithReferences]
// fails the same way, with a problem that matches [paths.ErrReferenceAlias],
// as [niceyaml.Node.At] fails there. A `<<` merge key of such an alias
// decides the names of its mapping. So a path through a name that the
// mapping writes before the merge key fails too. So does a path through a
// name that it does not write after every such merge key, as the package
// documentation describes. An edit through the node fails the Apply. At
// records nothing on the Apply.
func (n *Node) At(path paths.Path) *Node {
	full := path
	if !path.IsAbsolute() {
		full = n.path.Join(path)
	}

	if n.ended {
		return &Node{d: n.d, path: full, ended: true}
	}

	at := n.d.newNode(full)

	// No path selects the entry of a node that a later entry of its key
	// hides, so an `@` path reads below that entry, as the path of n does.
	if !path.IsAbsolute() {
		at.hidden = slices.Clone(n.hidden)
	}

	for sel := range full.Selectors() {
		why := ""

		switch sel.Kind {
		case paths.SelectorChild, paths.SelectorIndex:
			continue
		case paths.SelectorKey:
			why = "selects a key"
		default:
			why = "selects more than one value"
		}

		at.err = func(verb string) error {
			return n.bindOwn(niceyaml.NewError(fmt.Sprintf("%s %s %s, which %s", n.d.subject(), verb, full, why)))
		}

		return at
	}

	_, err := n.d.nodePlace(at)
	if err != nil {
		at.err = func(verb string) error {
			return n.d.readProblem(n, fmt.Errorf("%s %s %s, which %w", n.d.subject(), verb, full, err))
		}
	}

	return at
}

// bind binds err, an error that a [Func] returned, in the user's text. An
// error without a location binds at the value of n, and an `@` path in it
// reads from n, also after an earlier call took the place of n away. The
// binding starts at the deepest value on the path of n that the text holds,
// as the place of a [Node] holds it, so a path through the `<<` key of a
// merge key holds no value there. An error at a path that the text does not
// hold keeps its path, and binds at the site that [Doc.placeSite] gives, as
// [Doc.locate] places such an error. At the root, an error without a
// location binds under the path $.
func (n *Node) bind(err error) error {
	if err == nil {
		return nil
	}

	// No path selects the entry of a node that a later entry of its key
	// hides, or a value below it. An error at such a place, or an error at
	// n without a location, binds where an error at the deepest value of its
	// path that the text holds below that entry binds. A problem that the
	// edit package found shows the path of that site. An error of a Func
	// keeps its path, and one without a location takes the path of n.
	target, ok := n.bindTarget(err)
	for p := target; ok && len(through(n.hidden, n.path, p)) > 0; p, ok = p.Parent() {
		at := &Node{d: n.d, path: p, hidden: through(n.hidden, n.path, p)}

		v, perr := n.d.nodePlace(at)
		if perr == nil && v.exists {
			s := n.d.placeRange(at, v)

			path := s.path
			if !n.d.own {
				path = target
			}

			//nolint:wrapcheck // The binding is the error of the Func.
			return n.d.user.Bind(niceyaml.Locate(err, niceyaml.AtPath(path), niceyaml.AtRange(s.at),
				niceyaml.WithDetails(n.d.callDetails(s)...)))
		}
	}

	// An error of a Func reads from n, also where an earlier call took the
	// place of n away. It then binds where an error at its path binds. A
	// problem that the edit package found binds at the deepest value of the
	// path of n that the text holds.
	if !n.d.own {
		err = niceyaml.Rebase(err, n.path)
	}

	for p, ok := n.path, true; ok && !p.IsRoot(); p, ok = p.Parent() {
		place, perr := n.d.nodePlace(&Node{d: n.d, path: p})
		if perr != nil || !place.exists {
			continue
		}

		v, verr := n.d.node.At(p)
		if verr == nil {
			return v.Bind(err) //nolint:wrapcheck // The binding is the error of the Func.
		}
	}

	return n.d.node.Bind(niceyaml.Rebase(err, paths.Doc())) //nolint:wrapcheck // The binding is the error of the Func.
}

// bindTarget returns the path of the place where err, an error that a
// [Func] returned at n, binds: the path of n for an error without a
// location, and the path of an error at a path, with an `@` path read from
// n. It reports false for an error that names no path but another
// location, such as a range, and for an error that a Source binds already.
func (n *Node) bindTarget(err error) (paths.Path, bool) {
	bound, ok := errors.AsType[*niceyaml.SourceError](err)
	if ok && bound != nil {
		return paths.Path{}, false
	}

	e, ok := errors.AsType[*niceyaml.Error](err)
	if !ok {
		return n.path, true
	}

	if path, ok := e.Path(); ok {
		return n.path.Join(path), true
	}

	_, ranged := e.Range()
	_, placed := e.Position()

	return n.path, !ranged && !placed
}

// bindOwn binds err, a problem that the edit package found at the place of
// n, as [Node.bind] binds an error of a [Func]. Unlike that error, the
// problem shows the path of the site where it binds, as [Doc.locate]
// gives it.
func (n *Node) bindOwn(err error) error {
	n.d.own = true
	defer func() { n.d.own = false }()

	return n.bind(err)
}

// bindOwn binds err, a problem that the edit package found, in the current
// text, as the Bind method of the current document binds it. Unlike an
// error that a caller binds there, the problem shows the path of the site
// where it binds, as [Doc.locate] gives it.
func (d *Doc) bindOwn(err error) error {
	d.own = true
	defer func() { d.own = false }()

	return d.node.Bind(err) //nolint:wrapcheck // The binding is the problem of the edit.
}

// subject returns the subject of the edits of the Apply running, as
// messages name it: its label, or "the edit".
func (d *Doc) subject() string {
	if d.label == "" {
		return "the edit"
	}

	return d.label
}

// fail returns the problem that format describes, bound at the place of
// n, and records it as the problem of the call.
func (d *Doc) fail(n *Node, format string, args ...any) error {
	err := n.bindOwn(niceyaml.NewError(fmt.Sprintf(format, args...)))

	d.record(err)

	return err
}

// readFail returns problem, the problem of an edit of n on a path whose
// lookup failed, bound as [Doc.readProblem] binds it. It records the
// problem as fail does.
func (d *Doc) readFail(n *Node, problem error) error {
	bound := d.readProblem(n, problem)

	d.record(bound)

	return bound
}

// readProblem returns problem, the problem of a call through n on a path
// whose lookup failed. An [*aliasError] in problem tells of an alias that
// the text does not resolve and that stops the lookup. The problem then
// binds at that alias, as [Doc.aliasSite] gives its site. Otherwise it
// binds at the place of n.
func (d *Doc) readProblem(n *Node, problem error) error {
	var alias *aliasError

	if errors.As(problem, &alias) && alias.held {
		if at, ok := d.aliasSite(alias.node); ok {
			return d.user.Bind(niceyaml.Invalid(problem, //nolint:wrapcheck // The binding is the problem.
				niceyaml.AtPath(at.path), niceyaml.AtRange(at.at), niceyaml.WithDetails(d.callDetails(at)...)))
		}
	}

	return n.bindOwn(niceyaml.Invalid(problem))
}

// aliasSite returns the site of alias, an alias of the current text that
// stops the lookup of a path, as [Doc.nodeSite] gives it. The site lies at
// the alias, under the path of the value that holds it in the user's text,
// or of the mapping that holds the outermost `<<` merge entry around it.
// It reports false for an alias without text.
func (d *Doc) aliasSite(alias *ast.AliasNode) (site, bool) {
	if _, ok := d.nodeSpan(alias); !ok {
		return site{}, false
	}

	return d.nodeSite(alias, false), true
}

// record keeps err as the problem of the call, unless the call already has
// one.
func (d *Doc) record(err error) {
	if d.callErr == nil {
		d.callErr = err
	}
}

// reached holds the nodes whose paths reach a spot as the text stood when
// [Doc.reached] found them. Each holds the number of selectors of its path
// that reach the place of the spot.
//
// Create instances with [Doc.reached].
type reached struct {
	nodes map[*Node]int
}

// reached returns the nodes whose paths reach the place of s as the text
// stands. It leaves out each node that an edit removed. It also leaves out
// each node whose path reads another value than its place, as
// [Doc.displaced] tells, when that place lies outside the place of s, as
// [Doc.spotHolds] tells. The edit at s thus does not move such a node or
// take it away. The edit of the call running changes only the nodes that
// reached returns, so the call keeps each of them as it found them, which
// [Doc.undoCall] restores.
func (d *Doc) reached(s *spot) *reached {
	r := &reached{nodes: make(map[*Node]int)}

	for _, n := range d.nodes {
		if n.gone {
			continue
		}

		if k, ok := d.reachOf(n, s); ok {
			d.keepNode(n)

			if !d.displaced(n) || d.spotHolds(s, n) {
				r.nodes[n] = k
			}
		}
	}

	return r
}

// spotHolds reports whether the place of n, as [Doc.slotPlace] finds it in
// the current text, lies at or below the place of s: in the entry or the
// item of s, or in the collection that s reaches into. It reports true for
// a node whose place the current text does not hold, and for a spot at the
// root.
func (d *Doc) spotHolds(s *spot, n *Node) bool {
	p, ok := d.slotPlace(n)
	if !ok {
		return true
	}

	var outer ast.Node

	switch {
	case s.entry != nil:
		outer = s.entry
	case s.seq != nil && s.holder == nil:
		outer = s.seq.Values[s.index]
	case s.holder != nil:
		outer = s.holder
	default:
		return true
	}

	inner := p.value
	if p.entry != nil {
		inner = p.entry
	}

	return inner == outer || d.liesIn(inner, outer)
}

// reachOf returns the number of selectors of the path of n that reach the
// place of s as the text stands, as [Doc.reach] does. The path of a node on
// an entry that a later entry of its key hides, or below it, reads through
// that entry, as [Doc.nodePlace] reads it.
func (d *Doc) reachOf(n *Node, s *spot) (int, bool) {
	return d.reach(n.path, n.hidden, s)
}

// rebase moves each path of r to the path that to gives for the selectors
// of the path that reach the place. The selectors after them stay. An edit
// that renames or moves the value of the place thus takes the paths at the
// value and below it along, and the entry that a node holds, which a
// later entry of its key hides, keeps its place on the path.
func (r *reached) rebase(to func(head paths.Path) paths.Path) {
	for n, k := range r.nodes {
		path := rebased(n.path, k, to)

		if shift := path.Len() - n.path.Len(); shift != 0 && slices.ContainsFunc(n.hidden, deeper(k)) {
			n.hidden = slices.Clone(n.hidden)

			for i := range n.hidden {
				if n.hidden[i].depth > k {
					n.hidden[i].depth += shift
				}
			}
		}

		n.path = path
	}
}

// shift updates each path of r, a path that reaches the items of a
// sequence, after the item at index left the sequence. A path below a
// later item takes the index before it. A node that an edit removed keeps
// its path.
func (r *reached) shift(index int) {
	for n, k := range r.nodes {
		if !n.gone {
			n.path = shiftedPath(n.path, prefixOf(n.path, k), index)
		}
	}
}

// takenBy returns the record of the edit of the call running, the call
// that takes places away, or nil when the Apply has not recorded its edit.
func (d *Doc) takenBy() *editRecord {
	if d.lastEdit == nil {
		return nil
	}

	rec := *d.lastEdit

	return &rec
}

// goneProblem returns the problem of a call through n, a node whose place
// an earlier call took away. Its message names what the call does, as verb,
// the word of [pathProblem], says, and the place that the call asked for. The
// text can hold that place again, through a later call or through the value
// that the earlier call wrote, but n does not take it back, so the message
// says that n lost its place. It binds where an error at the place of that
// earlier call binds, under the path of that place, as the record of the
// call holds them. A place that an earlier call created binds at the site
// that [Doc.placeSite] gives, under the path of that site. The Apply
// records the problem.
func (d *Doc) goneProblem(n *Node, verb string) error {
	msg := fmt.Sprintf("%s %s %s through a node that lost its place to an earlier edit", d.subject(), verb, n.path)

	rec := n.goneBy
	if rec == nil {
		return d.fail(n, "%s", msg)
	}

	err := d.user.Bind(niceyaml.NewError(msg, niceyaml.AtPath(rec.holder()), niceyaml.AtRange(rec.at),
		niceyaml.WithDetails(d.callDetails(rec.site())...)))
	d.record(err)

	return err //nolint:wrapcheck // The binding is the problem of the edit.
}

// take marks each node of r below the place as gone, and those at the
// place too when self is true. An edit that replaces or removes the value
// of the place thus takes them away. Each node keeps by, the record of
// the edit of the call that takes it away.
func (r *reached) take(self bool, by *editRecord) {
	for n, k := range r.nodes {
		if k < n.path.Len() || self {
			n.gone, n.goneBy = true, by
		}
	}
}

// deeper returns whether a hidden entry lies below the first k selectors
// of a path, for [slices.ContainsFunc].
func deeper(k int) func(hiddenEntry) bool {
	return func(h hiddenEntry) bool { return h.depth > k }
}

// shiftedPath returns path after the item at index left the sequence at
// seq. A path below a later item takes the index before it, and any other
// path stays as it is.
func shiftedPath(path, seq paths.Path, index int) paths.Path {
	rest, ok := path.CutPrefix(seq)
	if !ok || rest.Len() == 0 {
		return path
	}

	var (
		sels  []paths.Selector
		moved bool
	)

	for sel := range rest.Selectors() {
		if len(sels) == 0 && sel.Kind == paths.SelectorIndex && sel.Index > index {
			sel.Index--
			moved = true
		}

		sels = append(sels, sel)
	}

	if !moved {
		return path
	}

	return appendSelectors(seq, sels)
}

// matchProblem binds err, the error of the matches of path, the path of
// the Apply, as [Doc.selectFrom] reads them. An alias that the text does
// not resolve stops them where they read through it, such as an alias of a
// reference document at a `<<` merge key under a `.*` selector, and err
// names that alias, as [Doc.aliasProblem] reads it. The problem then reads
// as the problem of a path of [Node.At] does, and it binds at that alias,
// as [Doc.aliasSite] gives its site. Any other problem binds as the
// document binds it.
func (d *Doc) matchProblem(path paths.Path, err error) error {
	alias, ok := d.aliasProblem(err)
	if !ok {
		return d.bindOwn(err)
	}

	at, ok := d.aliasSite(alias.node)
	if !ok {
		return d.bindOwn(err)
	}

	problem := fmt.Errorf("%s reads %s, which %w", d.subject(), path, alias)

	//nolint:wrapcheck // The binding is the error of the document.
	return d.user.Bind(niceyaml.Invalid(problem,
		niceyaml.AtPath(at.path), niceyaml.AtRange(at.at), niceyaml.WithDetails(d.callDetails(at)...)))
}

// appendSelectors returns p with sels after it.
func appendSelectors(p paths.Path, sels []paths.Selector) paths.Path {
	for _, sel := range sels {
		switch sel.Kind {
		case paths.SelectorChild:
			p = p.Child(sel.Name)
		case paths.SelectorIndex:
			p = p.Index(sel.Index)
		case paths.SelectorKey:
			p = p.Key()
		case paths.SelectorChildAll:
			p = p.ChildAll()
		case paths.SelectorRecursive:
			p = p.Recursive(sel.Name)
		case paths.SelectorRecursiveAll:
			p = p.RecursiveAll()
		default:
			p = p.IndexAll()
		}
	}

	return p
}

// spot is the place of an edit, as the paths that the Apply keeps up to
// date reach it. A path can reach a place through an alias or a `<<` merge
// key, so another path than the one that the edit took can reach it.
//
// Create instances with [Doc.spotOf] or [Doc.itemsOf].
type spot struct {
	// The content of the collection that a path reaches before rest, for
	// a place that does not hold a value or a spot of the items of a
	// sequence, or nil.
	holder ast.Node
	// The entry that holds the value of the place, or nil.
	entry *ast.MappingValueNode
	// The sequence that holds the value of the place as an item, or nil.
	seq *ast.SequenceNode
	// The selectors after holder.
	rest []paths.Selector
	// The path that the edit took.
	path paths.Path
	// The index of the item in seq.
	index int
	// Whether another path than one that starts with path can reach the
	// place. When the path of the edit does not go through an alias or a
	// merge key and no value on it has an anchor, only path reaches the
	// place, and the path of the place is its own.
	shared bool
}

// spotOf returns the spot of p, the place at path, as the text stands.
func (d *Doc) spotOf(path paths.Path, p *place) *spot {
	s := &spot{path: path, index: -1, shared: p.shared || p.anchored || len(p.via) > 0}

	switch {
	case p.root:
		s.shared = false
	case p.exists && p.entry != nil:
		s.entry = p.entry
	case p.exists && p.seq != nil:
		s.seq, s.index = p.seq, p.index
	case !astnode.IsNil(p.container):
		s.holder = p.container
		s.rest = slices.Collect(path.Selectors())[p.found:]

	default:
		s.shared = false
	}

	return s
}

// itemsOf returns the spot of the items of the sequence of p, an item at
// path.
func (d *Doc) itemsOf(path paths.Path, p *place) *spot {
	parent, _ := path.Parent()

	return &spot{path: parent, holder: p.seq, index: -1, shared: p.shared || p.anchored || len(p.via) > 0}
}

// reach returns the number of selectors of t, a path that hidden reads,
// that reach the place of s as the text stands, and reports false when t
// does not reach that place.
func (d *Doc) reach(t paths.Path, hidden []hiddenEntry, s *spot) (int, bool) {
	if !s.shared && len(hidden) == 0 {
		_, ok := t.CutPrefix(s.path)

		return s.path.Len(), ok
	}

	var (
		sels  = slices.Collect(t.Selectors())
		k     int
		found bool
	)

	_, err := d.walk(t, hidden, func(p *place, cur ast.Node) bool {
		if s.holder == nil {
			found = s.entry != nil && p.entry == s.entry || s.seq != nil && p.seq == s.seq && p.index == s.index
			k = p.found

			return !found
		}

		content, _, err := d.readValue(cur)
		if err != nil || content != s.holder {
			return true
		}

		k = p.found + len(s.rest)
		found = k <= len(sels) && slices.Equal(sels[p.found:k], s.rest)

		return false
	})
	if err != nil {
		return 0, false
	}

	return k, found
}
