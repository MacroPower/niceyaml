package niceyaml

import (
	"reflect"
	"slices"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/unmarshaler"
	"go.jacobcolvin.com/niceyaml/internal/yamlfield"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

// knownKeys returns each known key of cfg that the document of n holds
// below the node of n. The map takes the position of each key to the `@`
// path of the key from that node. It returns nil for a decode under
// [UnknownFieldsIgnore], which does not report any unknown field.
func (n *Node) knownKeys(cfg decodeConfig) map[position.Position]paths.Path {
	if cfg.unknownFields == UnknownFieldsIgnore || len(cfg.knownKeys) == 0 {
		return nil
	}

	res := n.doc.pathResolver()
	keys := make(map[position.Position]paths.Path, len(cfg.knownKeys))

	for _, p := range cfg.knownKeys {
		rel, ok := p.CutPrefix(n.base)
		if !ok || rel.Len() == 0 {
			continue
		}

		tk, err := res.Token(p.Key())
		if err == nil && tk != nil && tk.Position != nil {
			keys[position.NewFromToken(tk)] = rel
		}
	}

	return keys
}

// admitKnownKeys cuts out of view the entry of each known key of cfg that
// a decode into a value of type t leaves out, as [leavesOut] reports. The
// view is the node that the decoder reads for the node of n. The decoder
// still fills the field of each known key that t declares.
//
// The decoder reads the last entry of a key in a mapping. The entry of
// such a key thus hides each entry of its key that a `<<` merge key
// before it brings in. Without the entry, the decoder reads one of those
// entries instead. The decoder therefore reads a view without them too,
// as [keyCut] describes.
func (n *Node) admitKnownKeys(cfg decodeConfig, t reflect.Type, view ast.Node) keyCut {
	drop := make(map[position.Position]bool)

	for at, path := range n.knownKeys(cfg) {
		if leavesOut(cfg.unmarshalers, t, path) {
			drop[at] = true
		}
	}

	if len(drop) == 0 {
		return keyCut{view: view, read: view}
	}

	c := &keyCutter{n: n, keys: drop, reads: map[cutKey]ast.Node{}}
	trimmed, read, _ := c.cut(view)

	return keyCut{view: trimmed, read: read, reads: c.reads}
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
	// or the anchor that holds the mapping.
	read ast.Node
	// The node of read that stands for each node of view that the cut
	// copied, by its [cutKey].
	reads map[cutKey]ast.Node
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

// leavesOut reports whether a decode into a value of type t leaves out
// the entry at path, an `@` path whose last selector names the key of
// the entry. The decode leaves out each entry that does not have a field
// in the struct that reads the entry, as [keyField] finds the fields. It
// also leaves out every entry below a value that decodes itself, as
// [unmarshaler.Options.Whole] reports for u. The unmarshaler of such a
// value thus never reads a known key. Maps and interfaces keep every
// entry. The answer comes from the types alone, and leavesOut does not
// call an unmarshaler.
func leavesOut(u unmarshaler.Options, t reflect.Type, path paths.Path) bool {
	left := path.Len()

	for sel := range path.Selectors() {
		left--
		t = unmarshaler.Base(t)

		if _, whole := u.Whole(t); whole {
			return true
		}

		switch {
		case sel.Kind == paths.SelectorChild && t.Kind() == reflect.Struct:
			field, ok := keyField(u, t, sel.Name, map[reflect.Type]bool{})
			if !ok {
				// The decoder rejects a key above the entry whole, and
				// reads nothing below it.
				return left == 0
			}

			t = field

		case sel.Kind == paths.SelectorChild && t.Kind() == reflect.Map,
			sel.Kind == paths.SelectorIndex && (t.Kind() == reflect.Slice || t.Kind() == reflect.Array):
			t = t.Elem()

		default:
			// An interface reads any value, and the decoder rejects a
			// value of any other kind whole.
			return false
		}
	}

	return false
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

	default:
		return node, node, false
	}
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
// one that a merge key inside such a mapping brings in. A copy takes the
// place of the alias or the anchor that holds its mapping. The decoder
// then reads the copy, but each other alias of that anchor reads the
// mapping itself, because the decode reads the view without the cut first.
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

		open[target] = true
		hidden, changed := c.hide(target, names, targetRef, open)
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
	if entry == nil || entry.Key == nil {
		return false
	}

	tk := entry.Key.GetToken()

	return tk != nil && tk.Position != nil && keys[position.NewFromToken(tk)]
}
