package encoder

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"
)

// errNoKey indicates a path step to a key that a mapping does not have.
var errNoKey = errors.New("mapping does not have the key")

// pathComments holds the comments for the node that a YAML path selects.
// Its key holds the path as the caller wrote it in the comment map. An
// error names the entry by its key, because [yaml.Path.String] can spell
// the same path another way, such as "$.a" for "$.'a'".
type pathComments struct {
	key      string
	steps    []pathStep
	comments []positionedComment
}

// pathStep is one step of a YAML path, which moves from a node to the
// nodes under it.
type pathStep struct {
	// Holds the mapping key that a stepKey step selects.
	name string
	// Holds the sequence index that a stepIndex step selects.
	index uint
	kind  stepKind
}

// stepKind names the kind of a [pathStep].
type stepKind int

const (
	// A stepKey step selects the value of a mapping key, as in "$.a".
	stepKey stepKind = iota
	// A stepIndex step selects an item of a sequence, as in "$[0]".
	stepIndex
	// A stepIndexAll step selects every item of a sequence, as in
	// "$[*]".
	stepIndexAll
	// A stepRecursive step selects the values of a key at any depth, as
	// in "$..a".
	stepRecursive
)

// positionedComment holds the lines of one comment and the function that
// puts the comment in its position.
type positionedComment struct {
	set   commentSetter
	texts []string
}

// commentSetter puts comment in one position at target, a node in tree.
// It returns the error of go-yaml when the node cannot hold a comment in
// that position.
type commentSetter func(tree *commentTree, target ast.Node, comment *ast.CommentGroupNode) error

// parseComments parses each key of cm as a YAML path and returns the
// entries of cm in the order of their keys. It leaves out a nil comment,
// which has no text to place. A key that is not a path and a comment with
// a position that go-yaml does not define each return an error that
// matches [ErrComment]. The comment map alone decides both errors, so
// they do not depend on the document that gets the comments.
func parseComments(cm yaml.CommentMap) ([]pathComments, error) {
	if len(cm) == 0 {
		return nil, nil
	}

	entries := make([]pathComments, 0, len(cm))

	for _, key := range slices.Sorted(maps.Keys(cm)) {
		steps, err := parsePath(key)
		if err != nil {
			return nil, commentError(key, err)
		}

		entry := pathComments{key: key, steps: steps}

		for _, comment := range cm[key] {
			if comment == nil {
				continue
			}

			set, err := positionSetter(comment.Position)
			if err != nil {
				return nil, commentError(key, err)
			}

			entry.comments = append(entry.comments, positionedComment{set: set, texts: comment.Texts})
		}

		entries = append(entries, entry)
	}

	return entries, nil
}

// positionSetter returns the function that puts a comment in position.
// It returns [yaml.ErrUnknownCommentPositionType] for a position that
// go-yaml does not define.
func positionSetter(position yaml.CommentPosition) (commentSetter, error) {
	switch position {
	case yaml.CommentHeadPosition:
		return setHeadComment, nil
	case yaml.CommentLinePosition:
		return setLineComment, nil
	case yaml.CommentFootPosition:
		return setFootComment, nil
	default:
		return nil, yaml.ErrUnknownCommentPositionType
	}
}

// parsePath parses key as a YAML path and returns its steps. It returns
// the error of [yaml.PathString] for a key that is not a path, and an
// error that wraps [yaml.ErrInvalidPathString] for the two kinds of key
// that make go-yaml panic. The go-yaml parser turns an empty key into a
// path that does not have a root. That path panics when it selects a
// node. The parser itself panics on a key that ends inside an index,
// such as "$.a[0", because it reads past the end of the key.
func parsePath(key string) (_ []pathStep, err error) {
	if key == "" {
		return nil, fmt.Errorf("required '$' character at first: %w", yaml.ErrInvalidPathString)
	}

	defer func() {
		if recover() != nil {
			err = fmt.Errorf("unexpected end of YAML Path: %w", yaml.ErrInvalidPathString)
		}
	}()

	_, err = yaml.PathString(key)
	if err != nil {
		return nil, err //nolint:wrapcheck // Return the original error.
	}

	return pathSteps(key), nil
}

// pathSteps returns the steps of key, a path that [yaml.PathString]
// reads. It reads key the way PathString does, so that the steps select
// the node that [yaml.Path.FilterNode] selects. A "$" after an index
// starts the path again, so "$[0]$.a" has the steps of "$.a". A quoted
// key such as "$.'a.b'" holds the text between its quotes, and a "\"
// in it stands for the character after it. FilterNode strips one more
// pair of single quotes from around the text of a key, so this path
// selects the key a:
//
//	$.'\'a\''
func pathSteps(key string) []pathStep {
	buf := []rune(key)

	// NameEnd returns the position of the first "." or "[" at or after
	// start, or the end of buf.
	nameEnd := func(start int) int {
		for i := start; i < len(buf); i++ {
			if buf[i] == '.' || buf[i] == '[' {
				return i
			}
		}

		return len(buf)
	}

	var steps []pathStep

	for i := 0; i < len(buf); {
		switch {
		case buf[i] == '$':
			steps = steps[:0]
			i++

		case buf[i] == '[':
			end := i + slices.Index(buf[i:], ']')
			text := string(buf[i+1 : end])
			i = end + 1

			if text == "*" {
				steps = append(steps, pathStep{kind: stepIndexAll})
				continue
			}

			// PathString read the text as an int64, so it parses.
			index, _ := strconv.ParseUint(text, 10, 64) //nolint:errcheck // The text parses.
			steps = append(steps, pathStep{kind: stepIndex, index: uint(index)})

		// Every other step starts with a ".", and PathString rejects a
		// "." at the end of a path.
		case buf[i+1] == '.':
			i = nameEnd(i + 2)

			steps = append(steps, pathStep{kind: stepRecursive})

		case buf[i+1] == '\'':
			var name []rune

			for i += 2; buf[i] != '\''; i++ {
				if buf[i] == '\\' {
					i++
				}

				name = append(name, buf[i])
			}

			i++

			steps = append(steps, pathStep{kind: stepKey, name: trimQuotes(string(name))})

		default:
			end := nameEnd(i + 1)
			steps = append(steps, pathStep{kind: stepKey, name: string(buf[i+1 : end])})
			i = end
		}
	}

	return steps
}

// trimQuotes returns name without the single quotes around it. It
// returns name as it is when name does not start and end with a single
// quote.
func trimQuotes(name string) string {
	if len(name) > 1 && name[0] == '\'' && name[len(name)-1] == '\'' {
		return name[1 : len(name)-1]
	}

	return name
}

// commentError returns err, the problem with the comments under key, as an
// error that matches [ErrComment] and names the key.
func commentError(key string, err error) error {
	return fmt.Errorf("%w: %q: %w", ErrComment, key, err)
}

// setComments puts the comments of each entry on the node its path selects
// in the tree under root. It skips an entry whose path does not select a
// node, as [commentTree.selectNode] reports it. It places each comment
// where the go-yaml encoder places it for a [yaml.WithComment] map,
// except for three kinds of line comment. Go-yaml puts the line comment
// of a flow or empty sequence under a mapping key on the key, before the
// sequence. Here the sequence holds it, and the printer writes it after
// the sequence. Go-yaml writes a line comment inside a flow collection,
// before the closing bracket or brace. Here that comment fails. Go-yaml
// puts the line comment of a node with a tag or an anchor on the tag or
// the anchor, and the printer does not write it there. Here the scalar
// or flow collection under the tag or the anchor holds it, as
// [setLineComment] describes. Each of these returns an error that
// matches [ErrComment] and wraps the error of go-yaml:
//
//   - A comment that its node cannot hold, such as a line comment
//     inside a flow collection.
//   - A path that go-yaml cannot follow past a mapping key it cannot read.
//   - A path that names a group of nodes, which has a recursive step or a
//     step after "[*]".
func setComments(root ast.Node, entries []pathComments) error {
	if len(entries) == 0 || root == nil {
		return nil
	}

	tree := newCommentTree(root)

	for _, entry := range entries {
		target, err := tree.selectNode(root, entry.steps)
		if errors.Is(err, errNoKey) || errors.Is(err, yaml.ErrInvalidQuery) {
			continue
		}

		if err != nil {
			return commentError(entry.key, err)
		}

		// A group of nodes is a sequence that the tree does not hold, so
		// each setter returns its error for the position.
		for _, comment := range entry.comments {
			tokens := make([]*token.Token, 0, len(comment.texts))
			for _, text := range comment.texts {
				tokens = append(tokens, token.New(text, text, nil))
			}

			err = comment.set(tree, target, ast.CommentGroup(tokens))
			if err != nil {
				return commentError(entry.key, err)
			}
		}
	}

	return nil
}

// setHeadComment puts comment on the line above target.
func setHeadComment(tree *commentTree, target ast.Node, comment *ast.CommentGroupNode) error {
	switch parent := tree.parent(target).(type) {
	case *ast.MappingValueNode:
		return parent.SetComment(comment) //nolint:wrapcheck // Return the original error.
	case *ast.MappingNode:
		return parent.SetComment(comment) //nolint:wrapcheck // Return the original error.
	case *ast.SequenceNode:
		// A root sequence is its own parent, and its comment goes above
		// its first entry. An empty sequence has no entry to put it above.
		if len(parent.Values) == 0 {
			return yaml.ErrUnsupportedHeadPositionType(tree.root) //nolint:wrapcheck // Return the original error.
		}

		if len(parent.ValueHeadComments) == 0 {
			parent.ValueHeadComments = make([]*ast.CommentGroupNode, len(parent.Values))
		}

		parent.ValueHeadComments[max(slices.Index(parent.Values, target), 0)] = comment

		return nil

	default:
		return yaml.ErrUnsupportedHeadPositionType(tree.root) //nolint:wrapcheck // Return the original error.
	}
}

// setLineComment puts comment at the end of the line that target starts
// on. Mapping entries and sequences do not have lines of their own, so
// setLineComment puts the comment of each one on its parent. The
// exception is a flow or empty sequence under a mapping key. That
// sequence holds the comment itself, so the printer writes it after the
// sequence. A target inside a flow mapping or a flow sequence cannot
// hold a line comment, so setLineComment returns
// [yaml.ErrUnsupportedLinePositionType] for that flow collection. The
// node under the tags and anchors of a target holds the comment of the
// target, as [setPropertyLineComment] describes.
func setLineComment(tree *commentTree, target ast.Node, comment *ast.CommentGroupNode) error {
	// The printer writes a flow collection on one line and writes the
	// line comment of a node inside it before the closing bracket or
	// brace. The comment turns the rest of the line, that bracket or
	// brace too, into comment text.
	if flow := tree.flowAncestor(target); flow != nil {
		return yaml.ErrUnsupportedLinePositionType(flow) //nolint:wrapcheck // Return the original error.
	}

	if value, ok := propertyValue(target); ok {
		return setPropertyLineComment(value, comment)
	}

	switch target.(type) {
	case *ast.MappingValueNode, *ast.SequenceNode:
	default:
		return target.SetComment(comment) //nolint:wrapcheck // Return the original error.
	}

	switch parent := tree.parent(target).(type) {
	case *ast.MappingValueNode:
		// The printer writes a flow sequence on the line of its key, after
		// the comment of the key. A comment there turns the rest of the
		// line, the sequence too, into comment text. The sequence thus
		// holds the comment, and the printer writes it after the "]". The
		// printer writes an empty sequence as "[]" in any style.
		if seq, ok := target.(*ast.SequenceNode); ok && (seq.IsFlowStyle || len(seq.Values) == 0) {
			return seq.SetComment(comment) //nolint:wrapcheck // Return the original error.
		}

		return parent.Key.SetComment(comment) //nolint:wrapcheck // Return the original error.

	case *ast.MappingNode:
		return parent.SetComment(comment) //nolint:wrapcheck // Return the original error.
	case nil:
		return yaml.ErrUnsupportedLinePositionType(tree.root) //nolint:wrapcheck // Return the original error.
	default:
		return yaml.ErrUnsupportedLinePositionType(parent) //nolint:wrapcheck // Return the original error.
	}
}

// propertyValue returns the value under the tags and anchors of node.
// It reports false when node has neither a tag nor an anchor.
func propertyValue(node ast.Node) (ast.Node, bool) {
	value := node

	for {
		switch n := value.(type) {
		case *ast.TagNode:
			value = n.Value

			continue

		case *ast.AnchorNode:
			value = n.Value

			continue
		}

		return value, value != node
	}
}

// setPropertyLineComment puts comment at the end of the line of a node
// with a tag or an anchor. Value is the node under the tags and anchors,
// as [propertyValue] returns it. A go-yaml path selects the tag or the
// anchor and does not step to the value. The printer does not write the
// comment of a tag or an anchor. It writes the comment of a scalar or a
// flow collection after the value, so value holds the comment, as in
// `k: !!str x # c`. The printer writes a block collection on the lines
// after its tag or anchor, and no node holds a comment after either. For
// a block collection, setPropertyLineComment drops the comment and
// returns nil, as [yaml.WithComment] does.
func setPropertyLineComment(value ast.Node, comment *ast.CommentGroupNode) error {
	// The printer writes an empty collection as `[]` or `{}` even when its
	// node is not in flow style, and the comment follows the brackets.
	switch v := value.(type) {
	case nil, *ast.MappingValueNode:
		return nil
	case *ast.MappingNode:
		if !v.IsFlowStyle && len(v.Values) > 0 {
			return nil
		}

	case *ast.SequenceNode:
		if !v.IsFlowStyle && len(v.Values) > 0 {
			return nil
		}
	}

	return value.SetComment(comment) //nolint:wrapcheck // Return the original error.
}

// commentTree holds the parent of each node in the tree under root, so
// that a comment setter finds a parent without a walk of the whole tree.
// [ast.Parent] walks the tree from root on each call, so a comment map
// with an entry for each node takes time that grows with the square of
// the size of the tree.
//
// Create instances with [newCommentTree].
type commentTree struct {
	root    ast.Node
	parents map[ast.Node]ast.Node
	keys    map[*ast.MappingNode]*mappingKeys
}

// mappingKeys holds the keys of a mapping as [yaml.Path.FilterNode] reads
// them, so that a path step finds its key without a comparison against
// each key of the mapping. FilterNode compares the keys in order and
// returns the error of the first key that it cannot read. The keys
// after that key thus never select a value.
//
// Create instances with [newMappingKeys].
type mappingKeys struct {
	// Holds the first entry of each key before the first key that
	// FilterNode cannot read.
	values map[string]*ast.MappingValueNode
	// Holds the error of the first key that FilterNode cannot read.
	err error
}

// newMappingKeys creates new [mappingKeys] for mapping. FilterNode reads
// the text of the first token of each key. It unquotes a text that starts
// with a double quote, and it strips the single quotes from around a text
// that starts and ends with one.
func newMappingKeys(mapping *ast.MappingNode) *mappingKeys {
	keys := &mappingKeys{values: make(map[string]*ast.MappingValueNode, len(mapping.Values))}

	for _, value := range mapping.Values {
		key := value.Key.GetToken().Value
		if strings.HasPrefix(key, `"`) {
			var err error

			key, err = strconv.Unquote(key)
			if err != nil {
				keys.err = err

				break
			}
		} else {
			key = trimQuotes(key)
		}

		if _, ok := keys.values[key]; !ok {
			keys.values[key] = value
		}
	}

	return keys
}

// value returns the entry of name. It returns the error of the first key
// that FilterNode cannot read when no key before that one is name, and
// [errNoKey] when the mapping does not have name.
func (k *mappingKeys) value(name string) (*ast.MappingValueNode, error) {
	if value, ok := k.values[name]; ok {
		return value, nil
	}

	if k.err != nil {
		return nil, k.err
	}

	return nil, errNoKey
}

// newCommentTree creates a new [commentTree] for the tree under root.
// It walks the nodes that [ast.Parent] walks, in the same order, and it
// keeps the first parent it finds for each node, so that
// [commentTree.parent] returns what [ast.Parent] returns. The comments
// that the setters add do not change the parents, because the walk
// does not step into a comment.
func newCommentTree(root ast.Node) *commentTree {
	tree := &commentTree{
		root:    root,
		parents: make(map[ast.Node]ast.Node),
		keys:    make(map[*ast.MappingNode]*mappingKeys),
	}
	tree.add(root, root)

	return tree
}

// add records parent as the parent of node and of the nodes under node
// that the tree does not hold yet.
func (t *commentTree) add(parent, node ast.Node) {
	if node == nil {
		return
	}

	// A node that the walk already reached has its parent and the
	// parents of the nodes under it.
	if _, ok := t.parents[node]; ok {
		return
	}

	t.parents[node] = parent

	switch n := node.(type) {
	case *ast.LiteralNode:
		t.add(n, n.Value)
	case *ast.DirectiveNode:
		t.add(n, n.Name)

		for _, value := range n.Values {
			t.add(n, value)
		}

	case *ast.TagNode:
		t.add(n, n.Value)
	case *ast.DocumentNode:
		t.add(n, n.Body)
	case *ast.MappingNode:
		for _, value := range n.Values {
			t.add(n, value)
		}

	case *ast.MappingKeyNode:
		t.add(n, n.Value)
	case *ast.MappingValueNode:
		t.add(n, n.Key)
		t.add(n, n.Value)

	case *ast.SequenceNode:
		for _, value := range n.Values {
			t.add(n, value)
		}

	case *ast.AnchorNode:
		t.add(n, n.Name)
		t.add(n, n.Value)

	case *ast.AliasNode:
		t.add(n, n.Value)
	}
}

// parent returns the parent of node. The root node is its own parent. It
// returns nil for a node that the tree does not hold.
func (t *commentTree) parent(node ast.Node) ast.Node {
	return t.parents[node]
}

// selectNode returns the node that steps select from node, the node that
// [yaml.Path.FilterNode] returns for the same path. It returns [errNoKey]
// for a key that a mapping does not have, where FilterNode returns a nil
// node. It returns [yaml.ErrInvalidQuery], as FilterNode does, for an
// index past the end of a sequence and for a step into a node of another
// kind. An anchor, a tag, and an alias are nodes of another kind too,
// because a go-yaml path does not step through them. No such path
// selects a node.
//
// A recursive step and a step after "[*]" select a group of nodes, so
// selectNode returns a sequence that it builds and that the tree does
// not hold. A recursive step ends the path, as it does in go-yaml. A
// step after "[*]" goes on into each item of the sequence. When an item
// returns an error other than errNoKey, selectNode returns that error.
// It returns the group otherwise, even when no item has the rest of the
// path.
//
// For a step past a mapping key that go-yaml cannot read, selectNode
// returns the error of go-yaml, which [mappingKeys] holds. Two kinds of
// key do not unquote. One is a key from a marshaler's YAML, such as
// '"x'. The other is a string key that starts with a double quote and
// holds a line break, under [WithLiteralBlocks]. With that option,
// go-yaml leaves a string key with a line break unquoted, so the token
// value of the key is its raw text. The quoter marks that key as
// double-quoted and does not change its value.
func (t *commentTree) selectNode(node ast.Node, steps []pathStep) (ast.Node, error) {
	for i, step := range steps {
		switch step.kind {
		case stepKey:
			switch n := node.(type) {
			case *ast.MappingNode:
				value, err := t.keysOf(n).value(step.name)
				if err != nil {
					return nil, err
				}

				node = value.Value

			// Go-yaml compares the text of a lone entry with name as it
			// is.
			case *ast.MappingValueNode:
				if n.Key.GetToken().Value != step.name {
					return nil, errNoKey
				}

				node = n.Value

			default:
				return nil, yaml.ErrInvalidQuery
			}

		case stepIndex:
			seq, ok := node.(*ast.SequenceNode)
			if !ok || step.index >= uint(len(seq.Values)) {
				return nil, yaml.ErrInvalidQuery
			}

			node = seq.Values[step.index]

		case stepIndexAll:
			seq, ok := node.(*ast.SequenceNode)
			if !ok {
				return nil, yaml.ErrInvalidQuery
			}

			rest := steps[i+1:]
			if len(rest) == 0 {
				return seq, nil
			}

			// Go-yaml puts a nil node in the group for an item that
			// does not have a key of the path.
			for _, value := range seq.Values {
				_, err := t.selectNode(value, rest)
				if err != nil && !errors.Is(err, errNoKey) {
					return nil, err
				}
			}

			return newGroup(), nil

		case stepRecursive:
			return newGroup(), nil
		}
	}

	return node, nil
}

// keysOf returns the [mappingKeys] of mapping. It builds them on the
// first call for mapping.
func (t *commentTree) keysOf(mapping *ast.MappingNode) *mappingKeys {
	keys, ok := t.keys[mapping]
	if !ok {
		keys = newMappingKeys(mapping)
		t.keys[mapping] = keys
	}

	return keys
}

// newGroup creates a new sequence that holds a group of nodes, as
// go-yaml builds for a path that selects one. The tree does not hold it.
func newGroup() *ast.SequenceNode {
	return &ast.SequenceNode{BaseNode: &ast.BaseNode{}}
}

// flowAncestor returns the closest flow mapping or flow sequence that
// holds node. It returns nil when no flow collection holds node. The
// node itself does not count.
func (t *commentTree) flowAncestor(node ast.Node) ast.Node {
	for {
		parent := t.parent(node)
		// The root node is its own parent.
		if parent == nil || parent == node {
			return nil
		}

		switch p := parent.(type) {
		case *ast.MappingNode:
			if p.IsFlowStyle {
				return p
			}

		case *ast.SequenceNode:
			if p.IsFlowStyle {
				return p
			}
		}

		node = parent
	}
}

// setFootComment puts comment on the line below the parent of target.
func setFootComment(tree *commentTree, target ast.Node, comment *ast.CommentGroupNode) error {
	switch parent := tree.parent(target).(type) {
	case *ast.MappingValueNode:
		parent.FootComment = comment
	case *ast.MappingNode:
		parent.FootComment = comment
	case *ast.SequenceNode:
		parent.FootComment = comment
	case nil:
		return yaml.ErrUnsupportedFootPositionType(tree.root) //nolint:wrapcheck // Return the original error.
	default:
		return yaml.ErrUnsupportedFootPositionType(parent) //nolint:wrapcheck // Return the original error.
	}

	return nil
}
