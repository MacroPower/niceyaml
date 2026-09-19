package printer

import (
	"testing"

	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml/style/kind"
)

// newToken returns a token of the given type and value with no neighbors.
func newToken(typ token.Type, value string) *token.Token {
	return &token.Token{Type: typ, Value: value, Origin: value}
}

func TestTypeStyle(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		setup func() *token.Token
		want  kind.Kind
	}{
		"basic string type": {
			setup: func() *token.Token {
				return newToken(token.StringType, "")
			},
			want: kind.LiteralString,
		},
		"bool type": {
			setup: func() *token.Token {
				return newToken(token.BoolType, "")
			},
			want: kind.LiteralBoolean,
		},
		"comment type": {
			setup: func() *token.Token {
				return newToken(token.CommentType, "")
			},
			want: kind.Comment,
		},
		"mapping value type": {
			setup: func() *token.Token {
				return newToken(token.MappingValueType, "")
			},
			want: kind.PunctuationMappingValue,
		},
		"string followed by colon becomes mapping key": {
			setup: func() *token.Token {
				key := newToken(token.StringType, "key")
				colon := newToken(token.MappingValueType, ":")
				key.Next = colon
				colon.Prev = key

				return key
			},
			want: kind.NameTag, // Mapping key style.
		},
		"string not followed by colon stays string": {
			setup: func() *token.Token {
				str := newToken(token.StringType, "value")
				newline := newToken(token.StringType, "other")
				str.Next = newline
				newline.Prev = str

				return str
			},
			want: kind.LiteralString,
		},
		"token preceded by anchor inherits anchor style": {
			setup: func() *token.Token {
				anchor := newToken(token.AnchorType, "&")
				name := newToken(token.StringType, "myanchor")
				anchor.Next = name
				name.Prev = anchor

				return name
			},
			want: kind.NameAnchor,
		},
		"token preceded by alias inherits alias style": {
			setup: func() *token.Token {
				alias := newToken(token.AliasType, "*")
				name := newToken(token.StringType, "myalias")
				alias.Next = name
				name.Prev = alias

				return name
			},
			want: kind.NameAlias,
		},
		"unknown type returns text style": {
			setup: func() *token.Token {
				// Token with a type not in the map.
				tk := &token.Token{}
				tk.Type = token.Type(999) // Arbitrary type not in map.

				return tk
			},
			want: kind.Text,
		},
		"anchor type itself": {
			setup: func() *token.Token {
				return newToken(token.AnchorType, "&")
			},
			want: kind.NameAnchor,
		},
		"alias type itself": {
			setup: func() *token.Token {
				return newToken(token.AliasType, "*")
			},
			want: kind.NameAlias,
		},
		"integer type": {
			setup: func() *token.Token {
				return newToken(token.IntegerType, "42")
			},
			want: kind.LiteralNumberInteger,
		},
		"null type": {
			setup: func() *token.Token {
				return newToken(token.NullType, "null")
			},
			want: kind.LiteralNull,
		},
		"float type": {
			setup: func() *token.Token {
				return newToken(token.FloatType, "3.14")
			},
			want: kind.LiteralNumberFloat,
		},
		"double quote type": {
			setup: func() *token.Token {
				return newToken(token.DoubleQuoteType, "quoted")
			},
			want: kind.LiteralStringDouble,
		},
		"single quote type": {
			setup: func() *token.Token {
				return newToken(token.SingleQuoteType, "quoted")
			},
			want: kind.LiteralStringSingle,
		},
		"sequence entry type": {
			setup: func() *token.Token {
				return newToken(token.SequenceEntryType, "-")
			},
			want: kind.PunctuationSequenceEntry,
		},
		"sequence start type": {
			setup: func() *token.Token {
				return newToken(token.SequenceStartType, "[")
			},
			want: kind.PunctuationSequenceStart,
		},
		"sequence end type": {
			setup: func() *token.Token {
				return newToken(token.SequenceEndType, "]")
			},
			want: kind.PunctuationSequenceEnd,
		},
		"mapping start type": {
			setup: func() *token.Token {
				return newToken(token.MappingStartType, "{")
			},
			want: kind.PunctuationMappingStart,
		},
		"mapping end type": {
			setup: func() *token.Token {
				return newToken(token.MappingEndType, "}")
			},
			want: kind.PunctuationMappingEnd,
		},
		"tag type": {
			setup: func() *token.Token {
				return newToken(token.TagType, "!mytag")
			},
			want: kind.NameDecorator,
		},
		"directive type": {
			setup: func() *token.Token {
				return newToken(token.DirectiveType, "%YAML")
			},
			want: kind.CommentPreproc,
		},
		"document header type": {
			setup: func() *token.Token {
				return newToken(token.DocumentHeaderType, "---")
			},
			want: kind.PunctuationHeading,
		},
		"document end type": {
			setup: func() *token.Token {
				return newToken(token.DocumentEndType, "...")
			},
			want: kind.PunctuationHeading,
		},
		"literal block type": {
			setup: func() *token.Token {
				return newToken(token.LiteralType, "|")
			},
			want: kind.PunctuationBlockLiteral,
		},
		"folded block type": {
			setup: func() *token.Token {
				return newToken(token.FoldedType, ">")
			},
			want: kind.PunctuationBlockFolded,
		},
		"hex integer type": {
			setup: func() *token.Token {
				return newToken(token.HexIntegerType, "0xFF")
			},
			want: kind.LiteralNumberHex,
		},
		"octet integer type": {
			setup: func() *token.Token {
				return newToken(token.OctetIntegerType, "0o777")
			},
			want: kind.LiteralNumberOct,
		},
		"binary integer type": {
			setup: func() *token.Token {
				return newToken(token.BinaryIntegerType, "0b1010")
			},
			want: kind.LiteralNumberBin,
		},
		"infinity type": {
			setup: func() *token.Token {
				return newToken(token.InfinityType, ".inf")
			},
			want: kind.LiteralNumberInfinity,
		},
		"nan type": {
			setup: func() *token.Token {
				return newToken(token.NanType, ".nan")
			},
			want: kind.LiteralNumberNaN,
		},
		"merge key type": {
			setup: func() *token.Token {
				return newToken(token.MergeKeyType, "<<")
			},
			want: kind.NameAliasMerge,
		},
		"collect entry type": {
			setup: func() *token.Token {
				return newToken(token.CollectEntryType, ",")
			},
			want: kind.PunctuationCollectEntry,
		},
		"implicit null type": {
			setup: func() *token.Token {
				return newToken(token.ImplicitNullType, "")
			},
			want: kind.LiteralNullImplicit,
		},
		"space type": {
			setup: func() *token.Token {
				return newToken(token.SpaceType, " ")
			},
			want: kind.Text,
		},
		"invalid type": {
			setup: func() *token.Token {
				return newToken(token.InvalidType, "???")
			},
			want: kind.GenericErrorInvalid,
		},
		"unknown type": {
			setup: func() *token.Token {
				return newToken(token.UnknownType, "???")
			},
			want: kind.GenericErrorUnknown,
		},
		"mapping key type": {
			setup: func() *token.Token {
				return newToken(token.MappingKeyType, "?")
			},
			want: kind.NameTag,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tk := tc.setup()
			got := typeStyle(tk, nil)

			assert.Equal(t, tc.want, got)
		})
	}
}
