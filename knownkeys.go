package niceyaml

import (
	"reflect"

	"github.com/goccy/go-yaml/ast"

	"go.jacobcolvin.com/niceyaml/internal/unmarshaler"
	"go.jacobcolvin.com/niceyaml/internal/yamlfield"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

// knownKeys returns each known key of cfg that the document of n holds
// below the node of n. The map holds the `@` path of each key from that
// node, by the position of the key. It returns nil for a decode that
// accepts unknown fields.
func (n *Node) knownKeys(cfg decodeConfig) map[position.Position]paths.Path {
	if !cfg.disallowUnknownFields || len(cfg.knownKeys) == 0 {
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

// admitKnownKeys returns view without the entry of each known key of cfg
// that [leavesOut] reports for a decode into a value of type t. The view
// is the node that the decoder reads for the node of n. The decoder still
// fills the field of each known key that t declares.
func (n *Node) admitKnownKeys(cfg decodeConfig, t reflect.Type, view ast.Node) ast.Node {
	drop := make(map[position.Position]bool)

	for at, path := range n.knownKeys(cfg) {
		if leavesOut(cfg.unmarshalers, t, path) {
			drop[at] = true
		}
	}

	if len(drop) == 0 {
		return view
	}

	trimmed, _ := withoutKeys(view, drop)

	return trimmed
}

// leavesOut reports whether a decode into a value of type t leaves out
// the entry at path, an `@` path whose last selector names the key of
// the entry. The decode leaves out an entry that the struct reading it
// has no field for, as [keyField] finds the fields. It also leaves out
// every entry below a value that decodes itself, as
// [unmarshaler.Options.Whole] reports for u. The unmarshaler of such a
// value thus never reads a known key. A map and an interface keep every
// entry.
// The answer comes from the types alone, and leavesOut calls no
// unmarshaler.
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

// keyField returns the type of the field of t, a struct type, that reads
// the key name, and false when t has no such field. The field is one of
// t itself, or one of a struct that t holds inline, at any depth, as
// [yamlfield.Name] names the fields. A field of t wins over a field of an
// inline struct, as it does in go-yaml. An inline struct that decodes
// itself, as [unmarshaler.Options.Whole] reports for u, reads its keys
// through its unmarshaler. For such a key, keyField returns the type of
// that struct. The seen set holds the structs the search has read, so a
// struct that holds itself inline ends the search.
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

// withoutKeys returns node without each mapping entry whose key starts at
// a position of keys, and reports whether it left one out. It copies each
// node that loses an entry and each node above one, and shares every
// other node with node.
func withoutKeys(node ast.Node, keys map[position.Position]bool) (ast.Node, bool) {
	switch n := node.(type) {
	case *ast.MappingNode:
		values := make([]*ast.MappingValueNode, 0, len(n.Values))
		changed := false

		for _, entry := range n.Values {
			if keyAt(entry, keys) {
				changed = true

				continue
			}

			kept, entryChanged := withoutKeysBelow(entry, keys)
			values = append(values, kept)
			changed = changed || entryChanged
		}

		if !changed {
			return n, false
		}

		c := *n
		c.Values = values

		return &c, true

	case *ast.MappingValueNode:
		if keyAt(n, keys) {
			return ast.Mapping(n.Start, false), true
		}

		return withoutKeysBelow(n, keys)

	case *ast.SequenceNode:
		values := make([]ast.Node, 0, len(n.Values))
		changed := false

		for _, v := range n.Values {
			kept, valueChanged := withoutKeys(v, keys)
			values = append(values, kept)
			changed = changed || valueChanged
		}

		if !changed {
			return n, false
		}

		c := *n
		c.Values = values

		return &c, true

	case *ast.AnchorNode:
		value, changed := withoutKeys(n.Value, keys)
		if !changed {
			return n, false
		}

		c := *n
		c.Value = value

		return &c, true

	case *ast.TagNode:
		value, changed := withoutKeys(n.Value, keys)
		if !changed {
			return n, false
		}

		c := *n
		c.Value = value

		return &c, true

	default:
		return node, false
	}
}

// withoutKeysBelow returns entry with [withoutKeys] applied to its value,
// and reports whether the value changed.
func withoutKeysBelow(entry *ast.MappingValueNode, keys map[position.Position]bool) (*ast.MappingValueNode, bool) {
	if entry == nil {
		return nil, false
	}

	value, changed := withoutKeys(entry.Value, keys)
	if !changed {
		return entry, false
	}

	c := *entry
	c.Value = value

	return &c, true
}

// keyAt reports whether the key of entry starts at a position of keys.
func keyAt(entry *ast.MappingValueNode, keys map[position.Position]bool) bool {
	if entry == nil || entry.Key == nil {
		return false
	}

	tk := entry.Key.GetToken()

	return tk != nil && tk.Position != nil && keys[position.NewFromToken(tk)]
}
