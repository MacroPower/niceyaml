package astnode

import (
	"slices"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"
)

// Dropped calls found with each tag under node after which go-yaml's parse
// leaves out a token of tks, and with that token. The tokens tks are those
// of the text that node parsed from, in the order of their offsets. The
// calls come in the order that [Inspect] visits the tags. Dropped stops
// when found returns false, and it reports whether it stopped.
//
// Go-yaml's parser skips the token group after the value of an anchor
// under a scalar tag, such as !!str or !!null, when that value is a
// collection or starts on a line below the anchor. So it leaves the entry
// d out of "c: !!null &k {x: 1}", "d: 2", and "a: 1". When that
// group holds only a comment, the tree loses only the comment. So Dropped
// looks at the first token after the tag that is not a comment. It calls
// found when that token belongs to the document and no node under node
// holds it. Dropped looks only at the scalar tags that go-yaml reserves,
// such as !!int and !!null, because the parser skips the group only
// after those tags. A local tag such as !t is never a candidate.
//
// A skip of a "," fails the parse, as "{a: !!str &k [1], b: 2}" and
// "[!!str &k [1], 2]" show. So a "," after the tag never marks a token
// that the parse leaves out, though the tree does not hold the "," of a
// flow mapping.
func Dropped(node ast.Node, tks token.Tokens, found func(tag *ast.TagNode, tk *token.Token) bool) bool {
	var tags []*ast.TagNode

	Inspect(node, func(n ast.Node) bool {
		tag, ok := n.(*ast.TagNode)
		if ok && scalarTag(tag) {
			if _, anchored := tag.Value.(*ast.AnchorNode); anchored {
				tags = append(tags, tag)
			}
		}

		return n.Type() != ast.CommentType
	})

	if len(tags) == 0 {
		return false
	}

	held := map[int]bool{}
	for _, tk := range Tokens(node) {
		held[tk.Position.Offset] = true
	}

	for _, tag := range tags {
		end := ExtentOf(tag, false)
		if !end.OK {
			continue
		}

		i, _ := slices.BinarySearchFunc(tks, end.Hi+1, func(tk *token.Token, off int) int {
			return tokenOffset(tk) - off
		})

		next := nextContent(tks[i:])
		if next == nil || held[next.Position.Offset] {
			continue
		}

		if !found(tag, next) {
			return true
		}
	}

	return false
}

// scalarTag reports whether tag is a scalar tag that go-yaml reserves,
// such as !!int or !!null. It matches the text of the tag as the parser
// does, so a tag that the lexer reads with a tab and the text after it
// does not match.
func scalarTag(tag *ast.TagNode) bool {
	if tag.Start == nil {
		return false
	}

	switch token.ReservedTagKeyword(tag.Start.Value) {
	case token.IntegerTag, token.FloatTag, token.StringTag, token.BinaryTag,
		token.TimestampTag, token.BooleanTag, token.NullTag:
		return true

	default:
		return false
	}
}

// nextContent returns the first token of tks that carries a position and
// is not a comment, or nil. It returns nil when that token is a header, a
// "..." marker, or a directive, because a later document holds it. It
// returns nil for a ",", because the parse fails when it skips one.
func nextContent(tks token.Tokens) *token.Token {
	for _, tk := range tks {
		if tk == nil || tk.Position == nil || tk.Type == token.CommentType {
			continue
		}

		switch tk.Type {
		case token.DocumentHeaderType, token.DocumentEndType, token.DirectiveType,
			token.CollectEntryType:
			return nil

		default:
			return tk
		}
	}

	return nil
}

// tokenOffset returns the offset of tk, or -1 for a token without a
// position.
func tokenOffset(tk *token.Token) int {
	if tk == nil || tk.Position == nil {
		return -1
	}

	return tk.Position.Offset
}
