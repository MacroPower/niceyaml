package encoder

import (
	"fmt"
	"maps"
	"slices"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"
)

// pathComments holds the comments for the node that a YAML path selects.
type pathComments struct {
	path     *yaml.Path
	comments []*yaml.Comment
}

// parseComments parses each key of cm as a YAML path and returns the
// entries of cm in the order of their keys.
func parseComments(cm yaml.CommentMap) ([]pathComments, error) {
	if len(cm) == 0 {
		return nil, nil
	}

	entries := make([]pathComments, 0, len(cm))

	for _, key := range slices.Sorted(maps.Keys(cm)) {
		path, err := parsePath(key)
		if err != nil {
			return nil, err
		}

		entries = append(entries, pathComments{path: path, comments: cm[key]})
	}

	return entries, nil
}

// parsePath parses key as a YAML path. It returns an error that wraps
// [yaml.ErrInvalidPathString] for the two kinds of key that make go-yaml
// panic. The go-yaml parser turns an empty key into a path with no root,
// and that path panics when it selects a node. The parser itself panics
// on a key that ends inside an index, such as "$.a[0", because it reads
// past the end of the key.
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

// setComments puts the comments of each entry on the node its path selects
// in the tree under root. It skips an entry whose path selects no node. It
// places every comment where the go-yaml encoder places it for a
// [yaml.WithComment] map, and it returns the errors that encoder returns.
func setComments(root ast.Node, entries []pathComments) error {
	for _, entry := range entries {
		target, err := entry.path.FilterNode(root)
		if err != nil {
			return err //nolint:wrapcheck // Return the original error.
		}

		if target == nil {
			continue
		}

		for _, comment := range entry.comments {
			tokens := make([]*token.Token, 0, len(comment.Texts))
			for _, text := range comment.Texts {
				tokens = append(tokens, token.New(text, text, nil))
			}

			group := ast.CommentGroup(tokens)

			switch comment.Position {
			case yaml.CommentHeadPosition:
				err = setHeadComment(root, target, group)
			case yaml.CommentLinePosition:
				err = setLineComment(root, target, group)
			case yaml.CommentFootPosition:
				err = setFootComment(root, target, group)
			default:
				err = yaml.ErrUnknownCommentPositionType
			}

			if err != nil {
				return err
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
// on. A mapping entry or a sequence has no line of its own, so its comment
// goes on its parent.
func setLineComment(root, target ast.Node, comment *ast.CommentGroupNode) error {
	switch target.(type) {
	case *ast.MappingValueNode, *ast.SequenceNode:
	default:
		return target.SetComment(comment) //nolint:wrapcheck // Return the original error.
	}

	switch parent := ast.Parent(root, target).(type) {
	case *ast.MappingValueNode:
		return parent.Key.SetComment(comment) //nolint:wrapcheck // Return the original error.
	case *ast.MappingNode:
		return parent.SetComment(comment) //nolint:wrapcheck // Return the original error.
	case nil:
		return yaml.ErrUnsupportedLinePositionType(root) //nolint:wrapcheck // Return the original error.
	default:
		return yaml.ErrUnsupportedLinePositionType(parent) //nolint:wrapcheck // Return the original error.
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
