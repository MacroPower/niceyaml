package encoder

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"
)

// pathComments holds the comments for the node that a YAML path selects.
// Its key holds the path as the caller wrote it in the comment map. An
// error names the entry by its key, because [yaml.Path.String] can spell
// the same path another way, such as "$.a" for "$.'a'".
type pathComments struct {
	path     *yaml.Path
	key      string
	comments []positionedComment
}

// positionedComment holds the lines of one comment and the function that
// puts the comment in its position.
type positionedComment struct {
	set   commentSetter
	texts []string
}

// commentSetter puts comment in one position at target, a node in the
// tree under root. It returns the error of go-yaml when the node cannot
// hold a comment in that position.
type commentSetter func(root, target ast.Node, comment *ast.CommentGroupNode) error

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
		path, err := parsePath(key)
		if err != nil {
			return nil, commentError(key, err)
		}

		entry := pathComments{path: path, key: key}

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

// parsePath parses key as a YAML path. It returns an error that wraps
// [yaml.ErrInvalidPathString] for the two kinds of key that make go-yaml
// panic. The go-yaml parser turns an empty key into a path that does not
// have a root. That path panics when it selects a node. The parser itself
// panics on a key that ends inside an index, such as "$.a[0", because it
// reads past the end of the key.
func parsePath(key string) (_ *yaml.Path, err error) {
	if key == "" {
		return nil, fmt.Errorf("required '$' character at first: %w", yaml.ErrInvalidPathString)
	}

	defer func() {
		if recover() != nil {
			err = fmt.Errorf("unexpected end of YAML Path: %w", yaml.ErrInvalidPathString)
		}
	}()

	return yaml.PathString(key) //nolint:wrapcheck // Return the original error.
}

// commentError returns err, the problem with the comments under key, as an
// error that matches [ErrComment] and names the key.
func commentError(key string, err error) error {
	return fmt.Errorf("%w: %q: %w", ErrComment, key, err)
}

// setComments puts the comments of each entry on the node its path selects
// in the tree under root. It skips an entry when go-yaml reports that the
// path of keys and indexes does not select a node. It places each comment
// where the go-yaml encoder places it for a [yaml.WithComment] map,
// except for the line comment of a flow or empty sequence under a mapping
// key and a line comment inside a flow collection. Go-yaml puts the line
// comment of a flow or empty sequence under a mapping key on the key,
// before the sequence. Here the sequence holds it, and the printer
// writes it after the sequence. Go-yaml writes a line comment inside a
// flow collection, before the closing bracket or brace. Here that
// comment fails. Each of these returns an error that matches
// [ErrComment] and wraps the error of go-yaml:
//
//   - A comment that its node cannot hold, such as a line comment
//     inside a flow collection.
//   - A path that go-yaml cannot follow past a mapping key it cannot read.
//   - A path that names a group of nodes, which has a recursive step or a
//     step after "[*]".
func setComments(root ast.Node, entries []pathComments) error {
	for _, entry := range entries {
		target, err := entry.path.FilterNode(root)
		// Go-yaml returns a nil node for a mapping key that the tree
		// does not have. It returns this error for an index past the end
		// of a sequence and for a step into a node of another kind. An
		// anchor, a tag, and an alias are nodes of another kind too,
		// because a go-yaml path does not step through them. Every such
		// path selects no node.
		if errors.Is(err, yaml.ErrInvalidQuery) {
			continue
		}

		// Go-yaml returns one other error. It unquotes each mapping key
		// whose token value starts with a double quote, and two kinds of
		// key do not unquote. One is a key from a marshaler's YAML, such
		// as '"x'. The other is a string key that starts with a double
		// quote and holds a line break, under
		// [yaml.UseLiteralStyleIfMultiline]. With that option, go-yaml
		// leaves a string key with a line break unquoted, so the token
		// value of the key is its raw text. The quoter marks that key as
		// double-quoted and does not change its value. The path can
		// select a node after either key, so setComments does not skip
		// the entry.
		if err != nil {
			return commentError(entry.key, err)
		}

		if target == nil {
			continue
		}

		// For a recursive step and for a step after "[*]", go-yaml
		// returns a sequence that it builds to hold the nodes it found.
		// It builds the sequence even when it found no node. The tree
		// does not hold that sequence, so each setter returns its error
		// for the position.
		for _, comment := range entry.comments {
			tokens := make([]*token.Token, 0, len(comment.texts))
			for _, text := range comment.texts {
				tokens = append(tokens, token.New(text, text, nil))
			}

			err = comment.set(root, target, ast.CommentGroup(tokens))
			if err != nil {
				return commentError(entry.key, err)
			}
		}
	}

	return nil
}

// setHeadComment puts comment on the line above target.
func setHeadComment(root, target ast.Node, comment *ast.CommentGroupNode) error {
	switch parent := ast.Parent(root, target).(type) {
	case *ast.MappingValueNode:
		return parent.SetComment(comment) //nolint:wrapcheck // Return the original error.
	case *ast.MappingNode:
		return parent.SetComment(comment) //nolint:wrapcheck // Return the original error.
	case *ast.SequenceNode:
		// A root sequence is its own parent, and its comment goes above
		// its first entry. An empty sequence has no entry to put it above.
		if len(parent.Values) == 0 {
			return yaml.ErrUnsupportedHeadPositionType(root) //nolint:wrapcheck // Return the original error.
		}

		if len(parent.ValueHeadComments) == 0 {
			parent.ValueHeadComments = make([]*ast.CommentGroupNode, len(parent.Values))
		}

		parent.ValueHeadComments[max(slices.Index(parent.Values, target), 0)] = comment

		return nil

	default:
		return yaml.ErrUnsupportedHeadPositionType(root) //nolint:wrapcheck // Return the original error.
	}
}

// setLineComment puts comment at the end of the line that target starts
// on. Mapping entries and sequences do not have lines of their own, so
// setLineComment puts the comment of each one on its parent. The
// exception is a flow or empty sequence under a mapping key. That
// sequence holds the comment itself, so the printer writes it after the
// sequence. A target inside a flow mapping or a flow sequence cannot
// hold a line comment, so setLineComment returns
// [yaml.ErrUnsupportedLinePositionType] for that flow collection.
func setLineComment(root, target ast.Node, comment *ast.CommentGroupNode) error {
	// The printer writes a flow collection on one line and writes the
	// line comment of a node inside it before the closing bracket or
	// brace. The comment turns the rest of the line, that bracket or
	// brace too, into comment text.
	if flow := flowAncestor(root, target); flow != nil {
		return yaml.ErrUnsupportedLinePositionType(flow) //nolint:wrapcheck // Return the original error.
	}

	switch target.(type) {
	case *ast.MappingValueNode, *ast.SequenceNode:
	default:
		return target.SetComment(comment) //nolint:wrapcheck // Return the original error.
	}

	switch parent := ast.Parent(root, target).(type) {
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
		return yaml.ErrUnsupportedLinePositionType(root) //nolint:wrapcheck // Return the original error.
	default:
		return yaml.ErrUnsupportedLinePositionType(parent) //nolint:wrapcheck // Return the original error.
	}
}

// flowAncestor returns the closest flow mapping or flow sequence that
// holds node in the tree under root. It returns nil when no flow
// collection holds node. The node itself does not count.
func flowAncestor(root, node ast.Node) ast.Node {
	for {
		parent := ast.Parent(root, node)
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
func setFootComment(root, target ast.Node, comment *ast.CommentGroupNode) error {
	switch parent := ast.Parent(root, target).(type) {
	case *ast.MappingValueNode:
		parent.FootComment = comment
	case *ast.MappingNode:
		parent.FootComment = comment
	case *ast.SequenceNode:
		parent.FootComment = comment
	case nil:
		return yaml.ErrUnsupportedFootPositionType(root) //nolint:wrapcheck // Return the original error.
	default:
		return yaml.ErrUnsupportedFootPositionType(parent) //nolint:wrapcheck // Return the original error.
	}

	return nil
}
