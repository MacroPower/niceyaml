package niceyaml

import (
	"context"
	"reflect"
	"slices"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/datapath"
	"go.jacobcolvin.com/niceyaml/internal/decodewalk"
	"go.jacobcolvin.com/niceyaml/internal/unmarshaler"
	"go.jacobcolvin.com/niceyaml/internal/yamlfield"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

// knownKeys returns the position of each known key of cfg that the
// document of n holds below the node of n. An `@` known key reads from
// the node of n, and a known key with a wildcard names every entry that
// it selects, as [WithAllowedFields] describes. A known key that a path
// reaches through an alias or a `<<` merge key lies in the anchored
// mapping. It returns nil for a decode under [UnknownFieldsIgnore],
// which does not report any unknown field.
func (n *Node) knownKeys(cfg decodeConfig) map[position.Position]bool {
	if cfg.unknownFields == UnknownFieldsIgnore || len(cfg.knownKeys) == 0 {
		return nil
	}

	res := n.doc.pathResolver()
	keys := make(map[position.Position]bool, len(cfg.knownKeys))

	for _, p := range cfg.knownKeys {
		if last, ok := p.Last(); ok && last.Kind == paths.SelectorKey {
			p, _ = p.Parent()
		}

		matches, err := res.Matches(n.base.Join(p))
		if err != nil {
			continue
		}

		for _, m := range matches {
			rel, ok := m.Path.CutPrefix(n.base)
			if !ok || rel.Len() == 0 {
				continue
			}

			// Only an entry of a mapping has a key to admit.
			if last, _ := rel.Last(); last.Kind != paths.SelectorChild {
				continue
			}

			tk, err := res.Token(m.Path.Key())
			if err == nil && tk != nil && tk.Position != nil {
				keys[position.NewFromToken(tk)] = true
			}
		}
	}

	return keys
}

// admitKnownKeys cuts out of view each entry of a known key of cfg that
// [Node.cutKeys] finds for a decode of node into a value of type t. The
// view is the node that the decoder reads for node, and rules are those
// of the decode. The decoder still fills the field of each known key that
// t declares.
//
// The decoder reads the last entry of a key in a mapping. The entry of
// such a key thus hides each entry of its key that a `<<` merge key
// before it brings in. Without the entry, the decoder reads one of those
// entries instead. The decoder therefore reads a view without them too,
// as [keyCut] describes.
func (n *Node) admitKnownKeys(
	ctx context.Context,
	rules decodewalk.Rules,
	cfg decodeConfig,
	t reflect.Type,
	node, view ast.Node,
) keyCut {
	drop := n.cutKeys(ctx, rules, cfg, t, node)
	if len(drop) == 0 {
		return keyCut{view: view, read: view}
	}

	c := &keyCutter{n: n, keys: drop, reads: map[cutKey]ast.Node{}, view: view, anchors: map[cutKey]bool{}}
	trimmed, read, _ := c.cut(view)

	return keyCut{view: trimmed, read: read, reads: c.reads, anchors: c.primed}
}

// cutKeys returns the position of each known key of cfg that a decode of
// node into a value of type t leaves out at every read of the key.
// [Node.keptKeys] finds the reads. The decode cuts those keys out of the
// node that it reads.
//
// The decode reads a key once for each alias and each `<<` merge key
// that brings in its mapping. A cut removes the key from every one of
// those reads. A known key that one read keeps thus stays, so the decoder
// fills that field. Each read that leaves the key out then rejects it.
func (n *Node) cutKeys(
	ctx context.Context,
	rules decodewalk.Rules,
	cfg decodeConfig,
	t reflect.Type,
	node ast.Node,
) map[position.Position]bool {
	known := n.knownKeys(cfg)
	if len(known) == 0 {
		return nil
	}

	kept := n.keptKeys(ctx, rules, cfg.unmarshalers, t, node, known)
	drop := make(map[position.Position]bool, len(known))

	for at := range known {
		if !kept[at] {
			drop[at] = true
		}
	}

	return drop
}

// keptKeys returns the keys of known that some read of a decode of node
// into a value of type t keeps, with the rules and the unmarshalers u of
// the decode. A struct keeps each key that it reads into a field, as
// [keyField] finds the fields. A map or an interface keeps every key that
// it reads. A value that the decoder rejects whole keeps each key below
// it, such as a mapping where a slice reads a sequence. A struct leaves
// out each other key that it reads. A value that decodes itself, as
// [unmarshaler.Options.Whole] reports for u, does not keep any key below
// it. Its unmarshaler thus reads a known key only when another read keeps
// the key.
//
// The walk follows each alias and each `<<` merge key, as
// [decodewalk.Walk] describes. It thus finds a key in an anchored mapping
// for each type that reads the mapping.
func (n *Node) keptKeys(
	ctx context.Context,
	rules decodewalk.Rules,
	u unmarshaler.Options,
	t reflect.Type,
	node ast.Node,
	known map[position.Position]bool,
) map[position.Position]bool {
	res := n.doc.pathResolver()
	kept := map[position.Position]bool{}
	scanned := map[ast.Node]bool{}

	keep := func(entry *ast.MappingValueNode) {
		if at, ok := keyPosition(entry); ok && known[at] {
			kept[at] = true
		}
	}

	decodewalk.Walk(ctx, rules, t, node, astnode.Parent{}, func(v *decodewalk.Value) decodewalk.Action {
		if _, whole := u.Whole(v.Type); whole {
			return decodewalk.Skip
		}

		// The struct that holds an inline value reads its keys, through
		// keyField. A key has nothing below it.
		if v.Kind == decodewalk.Inline || v.Kind == decodewalk.Key {
			return decodewalk.Descend
		}

		switch v.Reads {
		case decodewalk.Fields, decodewalk.Members:
			mapping, ok := datapath.Deref(res, v.Node).(*ast.MappingNode)
			if !ok {
				break
			}

			for _, entry := range readEntries(res, mapping) {
				if v.Reads == decodewalk.Members {
					keep(entry)

					continue
				}

				name, ok := res.KeyName(entry.Key)
				if !ok {
					continue
				}

				if _, ok := keyField(u, v.Type, name, map[reflect.Type]bool{}); ok {
					keep(entry)
				}
			}

		case decodewalk.Nothing, decodewalk.Whole:
			keepAll(res, v.Node, keep, scanned)

		case decodewalk.Elements, decodewalk.Reference:
			// An element holds its own keys, and no known key lies in a
			// reference document.
		}

		return decodewalk.Descend
	})

	return kept
}

// readEntries returns each entry of mapping that the decoder might read,
// including those that its `<<` merge keys bring in. It leaves out an
// entry that a later entry of its key hides, as
// [datapath.MembersBackward] orders them. A merge key whose sources do
// not resolve might hide any key, so readEntries keeps each entry before
// such a merge key.
func readEntries(r *paths.Resolver, mapping *ast.MappingNode) []*ast.MappingValueNode {
	var read []*ast.MappingValueNode

	seen := map[*ast.MappingValueNode]bool{}
	names := map[string]bool{}

	for entry := range datapath.MembersBackward(r, mapping) {
		seen[entry] = true

		if name, ok := r.KeyName(entry.Key); ok {
			if names[name] {
				continue
			}

			names[name] = true
		}

		read = append(read, entry)
	}

	for entry := range datapath.Members(r, mapping, true) {
		if !seen[entry] {
			read = append(read, entry)
		}
	}

	return read
}

// keepAll calls keep for each entry of each mapping under node, through
// every alias and every `<<` merge key. The scanned set holds the nodes
// that keepAll has read, so it reads each node once.
func keepAll(r *paths.Resolver, node ast.Node, keep func(*ast.MappingValueNode), scanned map[ast.Node]bool) {
	content := datapath.Deref(r, node)
	if astnode.IsNil(content) || scanned[content] {
		return
	}

	scanned[content] = true

	switch c := content.(type) {
	case *ast.MappingValueNode:
		keepAll(r, &ast.MappingNode{Values: []*ast.MappingValueNode{c}}, keep, scanned)

	case *ast.MappingNode:
		for entry := range datapath.Members(r, c, true) {
			keep(entry)
			keepAll(r, entry.Value, keep, scanned)
		}

	case *ast.SequenceNode:
		for _, v := range c.Values {
			keepAll(r, v, keep, scanned)
		}
	}
}

// keyCut is the view of a decode without the entries of the known keys
// that the decode leaves out, as [Node.admitKnownKeys] returns it.
type keyCut struct {
	// The view without those entries. An error of the decoder binds
	// against it, because it holds each alias of the document that the
	// decode reads.
	view ast.Node
	// The view that the decoder reads. It differs from view at each `<<`
	// merge key that comes before such an entry in its mapping. There,
	// read holds a copy of each mapping that the merge key brings in,
	// without the key of the entry. The copy takes the place of the alias
	// or the anchor that holds the mapping. Read keeps each other alias, as
	// [keyCutter.cutAlias] describes.
	read ast.Node
	// The node of read that stands for each node of view that the cut
	// copied, by its [cutKey].
	reads map[cutKey]ast.Node
	// A copy of each anchor outside view that holds such an entry, with
	// the content that the decoder reads for the anchor. The decode
	// registers the copies, in order, before it reads read. Each copy comes
	// after the copies of the anchors that its content reads.
	anchors []*ast.AnchorNode
}

// cutKey names a node of the view of a decode. Each copy that
// [decodetree.DecodeView] makes of a node keeps its type and its token, so
// the key names every copy of the node.
//
//nolint:unused // The fields tell the keys of the map apart.
type cutKey struct {
	typ reflect.Type
	tk  *token.Token
}

// keyOf returns the [cutKey] of node.
func keyOf(node ast.Node) cutKey {
	return cutKey{typ: reflect.TypeOf(node), tk: astnode.Token(node)}
}

// readView returns view, which returns a node of the document as the
// decoder reads it, with the cut applied. For a node that the cut copied,
// the result is the node that the decoder read in its place. A decode
// that reads a node of the decode again thus reads what the decoder read.
func (c keyCut) readView(view func(ast.Node) ast.Node) func(ast.Node) ast.Node {
	if len(c.reads) == 0 {
		return view
	}

	return func(node ast.Node) ast.Node {
		v := view(node)
		if read, ok := c.reads[keyOf(v)]; ok {
			return read
		}

		return v
	}
}

// keyField returns the type of the field that reads the key name in t, a
// struct type. It returns false when t does not have such a field. The
// field is one of t itself, or one of a struct that t holds inline, at
// any depth, as [yamlfield.Name] names the fields. A field of t takes
// precedence over a field of an inline struct, as it does in go-yaml.
//
// Some inline structs decode themselves, as [unmarshaler.Options.Whole]
// reports for u. Each such struct reads its keys through its
// unmarshaler, so for those keys keyField returns the type of the struct.
// The seen set holds the structs that the search has read. The search
// skips those structs, so it ends for a struct that holds itself inline.
func keyField(u unmarshaler.Options, t reflect.Type, name string, seen map[reflect.Type]bool) (reflect.Type, bool) {
	seen[t] = true

	var inline []reflect.Type

	for field := range t.Fields() {
		own, isInline, skip := yamlfield.Name(field)

		switch {
		case skip || yamlfield.ReadsAnchor(field):
			continue
		case isInline:
			inline = append(inline, unmarshaler.Base(field.Type))
		case own == name:
			return field.Type, true
		}
	}

	for _, inner := range inline {
		if inner.Kind() != reflect.Struct || seen[inner] {
			continue
		}

		field, ok := keyField(u, inner, name, seen)
		if !ok {
			continue
		}

		if _, whole := u.Whole(inner); whole {
			return inner, true
		}

		return field, true
	}

	return nil, false
}

// keyCutter makes the [keyCut] of the view of a decode of n.
type keyCutter struct {
	n *Node
	// The positions of the keys of the entries that the cut leaves out.
	keys map[position.Position]bool
	// The node of the read view that stands for each node that the cut
	// copied, as [keyCut] describes.
	reads map[cutKey]ast.Node
	// The anchors outside the view whose content [keyCutter.cutAlias] has
	// cut, by their [cutKey].
	anchors map[cutKey]bool
	// The anchors inside the view, by their [cutKey], or nil before
	// [keyCutter.inView] first reads the view.
	viewAnchors map[cutKey]bool
	// The anchor contents that [keyCutter.readOf] is cutting, by their
	// [cutKey], so a loop of merge keys ends.
	cutting map[cutKey]bool
	// The view that the cut reads.
	view ast.Node
	// The copies of the anchors outside the view that hold the cut
	// content, as [keyCut] describes.
	primed []*ast.AnchorNode
}

// readOf returns content, the content of an anchor of the document, as
// the cut leaves it. It reuses the read node that [keyCutter.cut] made
// for content, and cuts content when the cut has not read it yet. A
// content that readOf is already cutting comes back as the document
// writes it.
func (c *keyCutter) readOf(content ast.Node) ast.Node {
	key := keyOf(content)
	if read, ok := c.reads[key]; ok {
		return read
	}

	if c.cutting[key] {
		return content
	}

	if c.cutting == nil {
		c.cutting = map[cutKey]bool{}
	}

	c.cutting[key] = true
	_, read, _ := c.cut(content)
	delete(c.cutting, key)

	return read
}

// cut returns node without each mapping entry whose key starts at a
// position of the keys of c. It also returns the node that the decoder
// reads in its place, as [keyCut] describes, and reports whether it left
// an entry out. It copies each node that loses an entry and each node
// above one, and shares every other node with node.
func (c *keyCutter) cut(node ast.Node) (ast.Node, ast.Node, bool) {
	view, read, changed := c.cutNode(node)
	if changed && !astnode.IsNil(node) {
		c.reads[keyOf(node)] = read
	}

	return view, read, changed
}

// cutNode is [keyCutter.cut] without the record of the copy.
func (c *keyCutter) cutNode(node ast.Node) (ast.Node, ast.Node, bool) {
	switch n := node.(type) {
	case *ast.MappingNode:
		return c.cutMapping(n)

	case *ast.MappingValueNode:
		if keyAt(n, c.keys) {
			empty := ast.Mapping(n.Start, false)

			return empty, empty, true
		}

		return c.cutBelow(n)

	case *ast.SequenceNode:
		views := make([]ast.Node, 0, len(n.Values))
		reads := make([]ast.Node, 0, len(n.Values))
		changed := false

		for _, v := range n.Values {
			view, read, valueChanged := c.cut(v)
			views = append(views, view)
			reads = append(reads, read)
			changed = changed || valueChanged
		}

		if !changed {
			return n, n, false
		}

		view, read := *n, *n
		view.Values, read.Values = views, reads

		return &view, &read, true

	case *ast.AnchorNode:
		value, readValue, changed := c.cut(n.Value)
		if !changed {
			return n, n, false
		}

		view, read := *n, *n
		view.Value, read.Value = value, readValue

		return &view, &read, true

	case *ast.TagNode:
		value, readValue, changed := c.cut(n.Value)
		if !changed {
			return n, n, false
		}

		view, read := *n, *n
		view.Value, read.Value = value, readValue

		return &view, &read, true

	case *ast.AliasNode:
		c.cutAlias(n)

		return node, node, false

	default:
		return node, node, false
	}
}

// cutAlias cuts the content of the anchor of alias when that anchor lies
// outside the view. The view keeps every alias. The read view keeps each
// alias except one that a `<<` merge key reads where [keyCutter.hide]
// writes a copy of the mapping. The decoder thus reads each kept alias
// through its anchor cache, as it does without a cut. An alias of an
// anchor in the view thus shares the value that the decoder made for the
// anchor. The cost of the decode follows the written
// size of the document, not the size of its aliases expanded.
//
// The decoder reads the anchor of an alias as it last registered the
// anchor. It registers each anchor inside the view again from the read
// view, without the cut entries. [Node.prime] registered each anchor
// outside the view as the document writes it. So where the cut leaves an
// entry out of such an anchor, cutAlias keeps a copy of the anchor with
// the cut content. The decode registers that copy before it reads the
// read view.
func (c *keyCutter) cutAlias(alias *ast.AliasNode) {
	anchor, ok := c.n.doc.tree().Anchor(alias)
	if !ok || c.inView(anchor) {
		return
	}

	key := keyOf(anchor)
	if c.anchors[key] {
		return
	}

	c.anchors[key] = true

	// The cut of the content first cuts each anchor that the content reads
	// through an alias. The list thus holds each anchor after them.
	_, read, changed := c.cut(anchor.Value)
	if !changed {
		return
	}

	primed := *anchor
	primed.Value = read
	c.primed = append(c.primed, &primed)
}

// inView reports whether anchor lies inside the view of the cut.
func (c *keyCutter) inView(anchor *ast.AnchorNode) bool {
	if c.viewAnchors == nil {
		c.viewAnchors = map[cutKey]bool{}

		astnode.Inspect(c.view, func(node ast.Node) bool {
			if a, ok := node.(*ast.AnchorNode); ok {
				c.viewAnchors[keyOf(a)] = true
			}

			return true
		})
	}

	return c.viewAnchors[keyOf(anchor)]
}

// cutMapping is [keyCutter.cut] for a mapping. In the node that the
// decoder reads, each `<<` merge key before an entry that the cut leaves
// out brings in its mappings without the key of that entry, as
// [keyCutter.hide] writes them.
func (c *keyCutter) cutMapping(n *ast.MappingNode) (ast.Node, ast.Node, bool) {
	views := make([]*ast.MappingValueNode, 0, len(n.Values))
	reads := make([]*ast.MappingValueNode, 0, len(n.Values))
	// The names of the keys that the cut leaves out after the entry that
	// the loop reads.
	hidden := map[string]bool{}
	changed := false

	for _, entry := range slices.Backward(n.Values) {
		if keyAt(entry, c.keys) {
			if name, ok := c.n.doc.pathResolver().KeyName(entry.Key); ok {
				hidden[name] = true
			}

			changed = true

			continue
		}

		view, read, entryChanged := c.cutBelow(entry)

		if len(hidden) > 0 && entry != nil && astnode.IsMergeKey(entry.Key) {
			value, hid := c.hide(read.Value, hidden, false, map[ast.Node]bool{})
			if hid {
				merge := *read
				merge.Value = value
				read = &merge
			}
		}

		views = append(views, view)
		reads = append(reads, read)
		changed = changed || entryChanged
	}

	if !changed {
		return n, n, false
	}

	slices.Reverse(views)
	slices.Reverse(reads)

	view, read := *n, *n
	view.Values, read.Values = views, reads

	return &view, &read, true
}

// cutBelow returns entry with [keyCutter.cut] applied to its value, and
// reports whether the value changed.
func (c *keyCutter) cutBelow(entry *ast.MappingValueNode) (*ast.MappingValueNode, *ast.MappingValueNode, bool) {
	if entry == nil {
		return nil, nil, false
	}

	value, readValue, changed := c.cut(entry.Value)
	if !changed {
		return entry, entry, false
	}

	view, read := *entry, *entry
	view.Value, read.Value = value, readValue

	return &view, &read, true
}

// hide returns value, the value of a `<<` merge key in the node that the
// decoder reads, with a copy of each mapping that it brings in. Each copy
// leaves out the entries whose keys names holds. Such a mapping is one
// that value holds, or one that an alias in value refers to. It is also
// one that a merge key inside such a mapping brings in. For an alias of
// the document, hide copies the content of the anchor as the cut leaves
// it. The copy thus also leaves out each entry that the cut removed there.
// A copy takes the place of the alias or the anchor that holds its
// mapping. The decoder then reads the copy. Each other alias of that
// anchor reads the mapping itself, because the decode reads the view
// without the cut first.
//
// The ref argument reports whether value belongs to a reference document.
// The open set holds the mappings that the calls above read, so a loop of
// merge keys ends. When no mapping that value brings in holds such an
// entry, hide returns value and reports false.
func (c *keyCutter) hide(value ast.Node, names map[string]bool, ref bool, open map[ast.Node]bool) (ast.Node, bool) {
	switch v := value.(type) {
	case *ast.AliasNode:
		target, targetRef, ok := c.target(v, ref)
		if !ok || open[target] {
			return v, false
		}

		read := target
		if !targetRef {
			read = c.readOf(target)
		}

		open[target] = true
		hidden, changed := c.hide(read, names, targetRef, open)
		delete(open, target)

		if !changed {
			return v, false
		}

		return hidden, true

	case *ast.AnchorNode:
		hidden, changed := c.hide(v.Value, names, ref, open)
		if !changed {
			return v, false
		}

		return hidden, true

	case *ast.TagNode:
		hidden, changed := c.hide(v.Value, names, ref, open)
		if !changed {
			return v, false
		}

		tag := *v
		tag.Value = hidden

		return &tag, true

	case *ast.SequenceNode:
		values := make([]ast.Node, 0, len(v.Values))
		changed := false

		for _, elem := range v.Values {
			hidden, elemChanged := c.hide(elem, names, ref, open)
			values = append(values, hidden)
			changed = changed || elemChanged
		}

		if !changed {
			return v, false
		}

		seq := *v
		seq.Values = values

		return &seq, true

	case *ast.MappingNode:
		values := make([]*ast.MappingValueNode, 0, len(v.Values))
		changed := false

		for _, entry := range v.Values {
			kept, keep, entryChanged := c.hideEntry(entry, names, ref, open)
			if keep {
				values = append(values, kept)
			}

			changed = changed || entryChanged
		}

		if !changed {
			return v, false
		}

		mapping := *v
		mapping.Values = values

		return &mapping, true

	case *ast.MappingValueNode:
		kept, keep, changed := c.hideEntry(v, names, ref, open)

		switch {
		case !changed:
			return v, false
		case !keep:
			return ast.Mapping(v.Start, false), true
		default:
			return kept, true
		}

	default:
		return value, false
	}
}

// hideEntry applies [keyCutter.hide] to entry, an entry of a mapping that
// a `<<` merge key brings in. It returns the entry to keep, and reports
// false in its place when names holds the key of entry. For the entry of
// a merge key, it returns a copy that brings in the mappings that hide
// writes. The last result reports whether hideEntry left entry out or
// copied it.
func (c *keyCutter) hideEntry(
	entry *ast.MappingValueNode,
	names map[string]bool,
	ref bool,
	open map[ast.Node]bool,
) (*ast.MappingValueNode, bool, bool) {
	if entry == nil || entry.Key == nil {
		return entry, true, false
	}

	if !astnode.IsMergeKey(entry.Key) {
		name, ok := c.n.doc.pathResolver().KeyName(entry.Key)
		if ok && names[name] {
			return nil, false, true
		}

		return entry, true, false
	}

	value, hid := c.hide(entry.Value, names, ref, open)
	if !hid {
		return entry, true, false
	}

	merge := *entry
	merge.Value = value

	return &merge, true, true
}

// target returns the content of the anchor that the decoder reads for
// alias, an alias in the node that the decoder reads. It also reports
// whether that anchor belongs to a reference document. The ref argument
// reports whether alias does. The last result is false when the decoder
// does not read an anchor through alias.
//
// For an alias of the document that the document's resolver does not
// bind, the decoder reads the anchor of a reference document. Every other
// alias of the document reads the anchor that the resolver binds it to,
// or none. Such an alias does not spell the name of an anchor of a
// reference document. The decode tree renames each anchor of the document
// when the Source has reference documents.
func (c *keyCutter) target(alias *ast.AliasNode, ref bool) (ast.Node, bool, bool) {
	if !ref {
		if anchor, ok := c.n.doc.tree().Anchor(alias); ok {
			return anchor.Value, false, true
		}
	}

	name, ok := astnode.Name(alias.Value)
	if !ok {
		return nil, false, false
	}

	anchor, ok := c.n.source.referenceAnchors().View(name)
	if !ok {
		return nil, false, false
	}

	return anchor.Value, true, true
}

// keyAt reports whether the key of entry starts at a position of keys.
func keyAt(entry *ast.MappingValueNode, keys map[position.Position]bool) bool {
	at, ok := keyPosition(entry)

	return ok && keys[at]
}

// keyPosition returns the position where the key of entry starts. It
// reports false when entry does not have a key with a position.
func keyPosition(entry *ast.MappingValueNode) (position.Position, bool) {
	if entry == nil || entry.Key == nil {
		return position.Position{}, false
	}

	tk := entry.Key.GetToken()
	if tk == nil || tk.Position == nil {
		return position.Position{}, false
	}

	return position.NewFromToken(tk), true
}
