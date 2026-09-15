package paths

import (
	"fmt"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"
)

// match is one node a path resolved to, with the mapping entry that holds it
// when the last selector picked a mapping key.
type match struct {
	node  ast.Node
	entry *ast.MappingValueNode
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
func (b *aliasBinder) Visit(node ast.Node) ast.Visitor {
	switch n := node.(type) {
	case *ast.AnchorNode:
		if n.Name != nil {
			b.anchors[n.Name.GetToken().Value] = n.Value
		}

	case *ast.AliasNode:
		if n.Value == nil {
			return b
		}

		if target, ok := b.anchors[n.Value.GetToken().Value]; ok {
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
// mapping, sequence, or scalar that selectors can apply to.
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
		if !ok {
			return content, nil
		}

		node = tag.Value
	}
}

// follow looks through anchors and aliases from node and adds each alias it
// follows to followed. It returns an error wrapping [ErrAlias] when it
// reaches an alias already in followed or an alias with no anchor.
func (r *resolver) follow(node ast.Node, followed map[*ast.AliasNode]bool) (ast.Node, error) {
	for {
		switch n := node.(type) {
		case *ast.AnchorNode:
			node = n.Value
		case *ast.AliasNode:
			name := n.Value.GetToken().Value

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

// resolve applies segs to root and returns every match.
func (r *resolver) resolve(root ast.Node, segs []segment) ([]match, error) {
	matches := []match{{node: root}}

	for _, seg := range segs {
		var next []match

		for _, m := range matches {
			found, err := r.apply(seg, m.node)
			if err != nil {
				return nil, err
			}

			next = append(next, found...)
		}

		matches = next
	}

	return matches, nil
}

// apply applies one selector to node.
func (r *resolver) apply(seg segment, node ast.Node) ([]match, error) {
	content, err := r.unwrap(node)
	if err != nil {
		return nil, err
	}

	switch seg.kind {
	case segmentChild:
		mapping, ok := content.(*ast.MappingNode)
		if !ok {
			return nil, nil
		}

		entry, ok, err := r.lookup(mapping, seg.name, nil)
		if err != nil || !ok {
			return nil, err
		}

		return []match{{node: entry.Value, entry: entry}}, nil

	case segmentIndex:
		seq, ok := content.(*ast.SequenceNode)
		if !ok || seg.index < 0 || seg.index >= len(seq.Values) {
			return nil, nil
		}

		return []match{{node: seq.Values[seg.index]}}, nil

	case segmentIndexAll:
		seq, ok := content.(*ast.SequenceNode)
		if !ok {
			return nil, nil
		}

		matches := make([]match, 0, len(seq.Values))
		for _, v := range seq.Values {
			matches = append(matches, match{node: v})
		}

		return matches, nil

	case segmentRecursive:
		return r.descend(content, seg.name, nil), nil

	default:
		return nil, nil
	}
}

// lookup finds the entry for name in mapping, looking through `<<` merge keys
// when no entry of the mapping itself has that key. A key the mapping defines
// wins over a merged one, and earlier merge sources win over later ones.
//
// The seen set guards against merge cycles through aliases. The bool result
// reports whether an entry was found.
func (r *resolver) lookup(
	mapping *ast.MappingNode, name string, seen map[*ast.MappingNode]bool,
) (*ast.MappingValueNode, bool, error) {
	for _, entry := range mapping.Values {
		if keyName(entry.Key) == name {
			return entry, true, nil
		}
	}

	if seen == nil {
		seen = map[*ast.MappingNode]bool{}
	}

	seen[mapping] = true

	for _, entry := range mapping.Values {
		if entry.Key == nil || !entry.Key.IsMergeKey() {
			continue
		}

		sources, err := r.mergeSources(entry.Value)
		if err != nil {
			return nil, false, err
		}

		for _, src := range sources {
			if seen[src] {
				continue
			}

			found, ok, err := r.lookup(src, name, seen)
			if err != nil || ok {
				return found, ok, err
			}
		}
	}

	return nil, false, nil
}

// mergeSources returns the mappings a `<<` value merges in: the value itself
// when it is a mapping, or each mapping element when it is a sequence.
func (r *resolver) mergeSources(value ast.Node) ([]*ast.MappingNode, error) {
	content, err := r.unwrap(value)
	if err != nil {
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

			if m, ok := elem.(*ast.MappingNode); ok {
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
// visits each entry of the source once, at its definition.
func (r *resolver) descend(node ast.Node, name string, acc []match) []match {
	switch n := node.(type) {
	case *ast.MappingNode:
		for _, entry := range n.Values {
			if keyName(entry.Key) == name {
				acc = append(acc, match{node: entry.Value, entry: entry})
			}

			acc = r.descend(entry.Value, name, acc)
		}

	case *ast.SequenceNode:
		for _, v := range n.Values {
			acc = r.descend(v, name, acc)
		}

	case *ast.AnchorNode:
		acc = r.descend(n.Value, name, acc)
	case *ast.TagNode:
		acc = r.descend(n.Value, name, acc)
	}

	return acc
}

// keyName returns the key text a child selector compares against. For a
// string key that is the unquoted string, and the source text otherwise.
func keyName(key ast.MapKeyNode) string {
	switch k := key.(type) {
	case nil:
		return ""
	case *ast.StringNode:
		return k.Value
	case *ast.MappingKeyNode:
		inner, ok := k.Value.(ast.MapKeyNode)
		if !ok {
			return ""
		}

		return keyName(inner)

	default:
		return key.GetToken().Value
	}
}

// firstToken returns the token that starts node's content: the first key of
// a mapping, the first element of a sequence, or the scalar itself. It looks
// through anchors and tags; an alias is its own token.
func firstToken(node ast.Node) *token.Token {
	for {
		switch n := node.(type) {
		case *ast.AnchorNode:
			node = n.Value
		case *ast.TagNode:
			node = n.Value
		case *ast.MappingNode:
			if len(n.Values) == 0 {
				return n.GetToken()
			}

			return n.Values[0].Key.GetToken()

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
