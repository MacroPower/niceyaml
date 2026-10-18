package astnode

import (
	"reflect"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"
)

// Converted returns the value under node as a decode reads it. That is the
// value that [Content] returns, except under a tag that converts a
// collection, as [ConvertsCollection] reports. A decode reads the value
// under such a tag as a scalar, so Converted returns the tag. The parser
// takes such a tag before the anchor of a collection, as in
// "!!str &k {a: 1}". It also takes one on an alias of a collection, as in
// "!!str *k". A caller reads the tag as a scalar and does not look through
// it, as [Content] and [Entries] do. A `<<` merge key reads its
// mappings through every tag, so a lookup of merge sources reads them with
// Content.
func Converted(node ast.Node) ast.Node {
	for !IsNil(node) {
		switch n := node.(type) {
		case *ast.DocumentNode:
			node = n.Body
		case *ast.AnchorNode:
			node = n.Value
		case *ast.TagNode:
			if ConvertsCollection(n) {
				return n
			}

			node = n.Value

		case *ast.MappingKeyNode:
			node = n.Value
		default:
			return node
		}
	}

	return nil
}

// ConvertsCollection reports whether the go-yaml decoder reads a
// collection under tag as a value of another kind. So !!str reads it as
// its print and !!null reads it as null, but a local tag such as !t reads
// it as the collection. The decoder answers from its decode of an empty
// mapping under the tag.
func ConvertsCollection(tag *ast.TagNode) bool {
	if tag == nil || tag.Start == nil {
		return false
	}

	empty, ok := probeTag(tag, nil)
	_, mapping := empty.(map[string]any)

	return !ok || !mapping
}

// ReadsCollection reports whether the value that the go-yaml decoder reads
// for a collection under tag depends on the content of the collection. A
// tag that keeps the collection and !!str do, but !!null does not. The
// decoder answers from its decodes of two mappings under the tag.
func ReadsCollection(tag *ast.TagNode) bool {
	if tag == nil || tag.Start == nil {
		return true
	}

	empty, emptyOK := probeTag(tag, nil)
	full, fullOK := probeTag(tag, ast.MappingValue(
		token.New(":", ":", &token.Position{}),
		ast.String(token.New("a", "a", &token.Position{})),
		ast.String(token.New("b", "b", &token.Position{})),
	))

	return emptyOK != fullOK || !reflect.DeepEqual(empty, full)
}

// probeTag returns what the go-yaml decoder reads for a mapping of entry
// under a copy of tag, or for an empty mapping when entry is nil. It
// reports false when the decode fails.
func probeTag(tag *ast.TagNode, entry *ast.MappingValueNode) (any, bool) {
	m := ast.Mapping(token.New("{", "{", &token.Position{}), true)
	if entry != nil {
		m.Values = append(m.Values, entry)
	}

	probe := *tag
	probe.Value = m

	var v any

	err := NodeToValue(&probe, &v)

	return v, err == nil
}
