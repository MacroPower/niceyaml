package preamble_test

import (
	"testing"

	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml/internal/preamble"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
)

func TestLen(t *testing.T) {
	t.Parallel()

	tkb := yamltest.NewTokenBuilder()

	at := func(typ token.Type, value string, line int) *token.Token {
		return tkb.Clone().Type(typ).Value(value).PositionLine(line).Build()
	}

	// TokenBuilder always sets a Position, so unplaced drops it by hand.
	unplaced := func(typ token.Type, value string) *token.Token {
		tk := tkb.Clone().Type(typ).Value(value).Build()
		tk.Position = nil

		return tk
	}

	tcs := map[string]struct {
		tks  token.Tokens
		want int
	}{
		"empty": {
			tks:  nil,
			want: 0,
		},
		"comments only": {
			tks: token.Tokens{
				at(token.CommentType, " a", 1),
				at(token.CommentType, " b", 2),
			},
			want: 2,
		},
		"content first": {
			tks: token.Tokens{
				at(token.StringType, "a", 1),
				at(token.CommentType, " c", 1),
			},
			want: 0,
		},
		"header then comment then content": {
			tks: token.Tokens{
				at(token.DocumentHeaderType, "---", 1),
				at(token.CommentType, " c", 2),
				at(token.StringType, "a", 3),
				at(token.MappingValueType, ":", 3),
				at(token.IntegerType, "1", 3),
			},
			want: 2,
		},
		"YAML directive with a comment": {
			tks: token.Tokens{
				at(token.DirectiveType, "%", 1),
				at(token.StringType, "YAML", 1),
				at(token.FloatType, "1.2", 1),
				at(token.CommentType, " c", 1),
				at(token.DocumentHeaderType, "---", 2),
				at(token.StringType, "a", 3),
			},
			want: 5,
		},
		"TAG directive": {
			tks: token.Tokens{
				at(token.DirectiveType, "%", 1),
				at(token.StringType, "TAG", 1),
				at(token.StringType, "!e!", 1),
				at(token.StringType, "tag:example.com,2000:", 1),
				at(token.DocumentHeaderType, "---", 2),
				at(token.StringType, "a", 3),
			},
			want: 5,
		},
		"content on the line after a directive": {
			tks: token.Tokens{
				at(token.DirectiveType, "%", 1),
				at(token.StringType, "YAML", 1),
				at(token.StringType, "a", 2),
				at(token.MappingValueType, ":", 2),
			},
			want: 2,
		},
		"directive line ends at a later line": {
			tks: token.Tokens{
				at(token.DirectiveType, "%", 1),
				at(token.StringType, "YAML", 1),
				at(token.DocumentHeaderType, "---", 2),
				at(token.StringType, "a", 1),
			},
			want: 3,
		},
		"value without a position after a directive": {
			tks: token.Tokens{
				at(token.DirectiveType, "%", 1),
				unplaced(token.FloatType, "1.2"),
				at(token.StringType, "a", 2),
			},
			want: 1,
		},
		"comment without a position on the directive line": {
			tks: token.Tokens{
				at(token.DirectiveType, "%", 1),
				at(token.StringType, "YAML", 1),
				unplaced(token.CommentType, " c"),
				at(token.FloatType, "1.2", 1),
				at(token.StringType, "a", 2),
			},
			want: 4,
		},
		"directive without a position": {
			tks: token.Tokens{
				unplaced(token.DirectiveType, "%"),
				at(token.StringType, "YAML", 1),
			},
			want: 1,
		},
		"nil token": {
			tks: token.Tokens{
				nil,
				at(token.CommentType, " c", 1),
				at(token.StringType, "a", 2),
			},
			want: 2,
		},
		"document end then comments": {
			tks: token.Tokens{
				at(token.DocumentEndType, "...", 1),
				at(token.CommentType, " c", 2),
				at(token.DocumentHeaderType, "---", 3),
				at(token.StringType, "a", 4),
			},
			want: 3,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, preamble.Len(tc.tks))
		})
	}
}
