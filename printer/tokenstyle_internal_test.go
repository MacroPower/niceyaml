package printer

import (
	"testing"

	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml/style"
)

// newToken returns a token of the given type and value with no neighbors.
func newToken(typ token.Type, value string) *token.Token {
	return &token.Token{Type: typ, Value: value, Origin: value}
}

func TestTypeStyle(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		setup func() *token.Token
		want  style.Style
	}{
		"basic string type": {
			setup: func() *token.Token {
				return newToken(token.StringType, "")
			},
			want: style.LiteralString,
		},
		"bool type": {
			setup: func() *token.Token {
				return newToken(token.BoolType, "")
			},
			want: style.LiteralBoolean,
		},
		"comment type": {
			setup: func() *token.Token {
				return newToken(token.CommentType, "")
			},
			want: style.Comment,
		},
		"mapping value type": {
			setup: func() *token.Token {
				return newToken(token.MappingValueType, "")
			},
			want: style.PunctuationMappingValue,
		},
		"string followed by colon becomes mapping key": {
			setup: func() *token.Token {
				key := newToken(token.StringType, "key")
				colon := newToken(token.MappingValueType, ":")
				key.Next = colon
				colon.Prev = key

				return key
			},
			want: style.NameTag, // Mapping key style.
		},
		"string not followed by colon stays string": {
			setup: func() *token.Token {
				str := newToken(token.StringType, "value")
				newline := newToken(token.StringType, "other")
				str.Next = newline
				newline.Prev = str

				return str
			},
			want: style.LiteralString,
		},
		"token preceded by anchor inherits anchor style": {
			setup: func() *token.Token {
				anchor := newToken(token.AnchorType, "&")
				name := newToken(token.StringType, "myanchor")
				anchor.Next = name
				name.Prev = anchor

				return name
			},
			want: style.NameAnchor,
		},
		"token preceded by alias inherits alias style": {
			setup: func() *token.Token {
				alias := newToken(token.AliasType, "*")
				name := newToken(token.StringType, "myalias")
				alias.Next = name
				name.Prev = alias

				return name
			},
			want: style.NameAlias,
		},
		"unknown type returns text style": {
			setup: func() *token.Token {
				// Token with a type not in the map.
				tk := &token.Token{}
				tk.Type = token.Type(999) // Arbitrary type not in map.

				return tk
			},
			want: style.Text,
		},
		"anchor type itself": {
			setup: func() *token.Token {
				return newToken(token.AnchorType, "&")
			},
			want: style.NameAnchor,
		},
		"alias type itself": {
			setup: func() *token.Token {
				return newToken(token.AliasType, "*")
			},
			want: style.NameAlias,
		},
		"integer type": {
			setup: func() *token.Token {
				return newToken(token.IntegerType, "42")
			},
			want: style.LiteralNumberInteger,
		},
		"null type": {
			setup: func() *token.Token {
				return newToken(token.NullType, "null")
			},
			want: style.LiteralNull,
		},
		"float type": {
			setup: func() *token.Token {
				return newToken(token.FloatType, "3.14")
			},
			want: style.LiteralNumberFloat,
		},
		"double quote type": {
			setup: func() *token.Token {
				return newToken(token.DoubleQuoteType, "quoted")
			},
			want: style.LiteralStringDouble,
		},
		"single quote type": {
			setup: func() *token.Token {
				return newToken(token.SingleQuoteType, "quoted")
			},
			want: style.LiteralStringSingle,
		},
		"sequence entry type": {
			setup: func() *token.Token {
				return newToken(token.SequenceEntryType, "-")
			},
			want: style.PunctuationSequenceEntry,
		},
		"sequence start type": {
			setup: func() *token.Token {
				return newToken(token.SequenceStartType, "[")
			},
			want: style.PunctuationSequenceStart,
		},
		"sequence end type": {
			setup: func() *token.Token {
				return newToken(token.SequenceEndType, "]")
			},
			want: style.PunctuationSequenceEnd,
		},
		"mapping start type": {
			setup: func() *token.Token {
				return newToken(token.MappingStartType, "{")
			},
			want: style.PunctuationMappingStart,
		},
		"mapping end type": {
			setup: func() *token.Token {
				return newToken(token.MappingEndType, "}")
			},
			want: style.PunctuationMappingEnd,
		},
		"tag type": {
			setup: func() *token.Token {
				return newToken(token.TagType, "!mytag")
			},
			want: style.NameDecorator,
		},
		"directive type": {
			setup: func() *token.Token {
				return newToken(token.DirectiveType, "%YAML")
			},
			want: style.CommentPreproc,
		},
		"document header type": {
			setup: func() *token.Token {
				return newToken(token.DocumentHeaderType, "---")
			},
			want: style.PunctuationHeading,
		},
		"document end type": {
			setup: func() *token.Token {
				return newToken(token.DocumentEndType, "...")
			},
			want: style.PunctuationHeading,
		},
		"literal block type": {
			setup: func() *token.Token {
				return newToken(token.LiteralType, "|")
			},
			want: style.PunctuationBlockLiteral,
		},
		"folded block type": {
			setup: func() *token.Token {
				return newToken(token.FoldedType, ">")
			},
			want: style.PunctuationBlockFolded,
		},
		"hex integer type": {
			setup: func() *token.Token {
				return newToken(token.HexIntegerType, "0xFF")
			},
			want: style.LiteralNumberHex,
		},
		"octet integer type": {
			setup: func() *token.Token {
				return newToken(token.OctetIntegerType, "0o777")
			},
			want: style.LiteralNumberOct,
		},
		"binary integer type": {
			setup: func() *token.Token {
				return newToken(token.BinaryIntegerType, "0b1010")
			},
			want: style.LiteralNumberBin,
		},
		"infinity type": {
			setup: func() *token.Token {
				return newToken(token.InfinityType, ".inf")
			},
			want: style.LiteralNumberInfinity,
		},
		"nan type": {
			setup: func() *token.Token {
				return newToken(token.NanType, ".nan")
			},
			want: style.LiteralNumberNaN,
		},
		"merge key type": {
			setup: func() *token.Token {
				return newToken(token.MergeKeyType, "<<")
			},
			want: style.NameAliasMerge,
		},
		"collect entry type": {
			setup: func() *token.Token {
				return newToken(token.CollectEntryType, ",")
			},
			want: style.PunctuationCollectEntry,
		},
		"implicit null type": {
			setup: func() *token.Token {
				return newToken(token.ImplicitNullType, "")
			},
			want: style.LiteralNullImplicit,
		},
		"space type": {
			setup: func() *token.Token {
				return newToken(token.SpaceType, " ")
			},
			want: style.Text,
		},
		"invalid type": {
			setup: func() *token.Token {
				return newToken(token.InvalidType, "???")
			},
			want: style.GenericErrorInvalid,
		},
		"unknown type": {
			setup: func() *token.Token {
				return newToken(token.UnknownType, "???")
			},
			want: style.GenericErrorUnknown,
		},
		"mapping key type": {
			setup: func() *token.Token {
				return newToken(token.MappingKeyType, "?")
			},
			want: style.NameTag,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tk := tc.setup()
			got := typeStyle(tk)

			assert.Equal(t, tc.want, got)
		})
	}
}
