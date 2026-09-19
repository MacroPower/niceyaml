package printer

import (
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/style/kind"
)

var tokenTypeStyles = map[token.Type]kind.Kind{
	token.AliasType:          kind.NameAlias,
	token.AnchorType:         kind.NameAnchor,
	token.BinaryIntegerType:  kind.LiteralNumberBin,
	token.BoolType:           kind.LiteralBoolean,
	token.CollectEntryType:   kind.PunctuationCollectEntry,
	token.CommentType:        kind.Comment,
	token.DirectiveType:      kind.CommentPreproc,
	token.DocumentEndType:    kind.PunctuationHeading,
	token.DocumentHeaderType: kind.PunctuationHeading,
	token.DoubleQuoteType:    kind.LiteralStringDouble,
	token.FloatType:          kind.LiteralNumberFloat,
	token.FoldedType:         kind.PunctuationBlockFolded,
	token.HexIntegerType:     kind.LiteralNumberHex,
	token.ImplicitNullType:   kind.LiteralNullImplicit,
	token.InfinityType:       kind.LiteralNumberInfinity,
	token.IntegerType:        kind.LiteralNumberInteger,
	token.InvalidType:        kind.GenericErrorInvalid,
	token.LiteralType:        kind.PunctuationBlockLiteral,
	token.MappingEndType:     kind.PunctuationMappingEnd,
	token.MappingKeyType:     kind.NameTag,
	token.MappingStartType:   kind.PunctuationMappingStart,
	token.MappingValueType:   kind.PunctuationMappingValue,
	token.MergeKeyType:       kind.NameAliasMerge,
	token.NanType:            kind.LiteralNumberNaN,
	token.NullType:           kind.LiteralNull,
	token.OctetIntegerType:   kind.LiteralNumberOct,
	token.SequenceEndType:    kind.PunctuationSequenceEnd,
	token.SequenceEntryType:  kind.PunctuationSequenceEntry,
	token.SequenceStartType:  kind.PunctuationSequenceStart,
	token.SingleQuoteType:    kind.LiteralStringSingle,
	token.SpaceType:          kind.Text,
	token.StringType:         kind.LiteralString,
	token.TagType:            kind.NameDecorator,
	token.UnknownType:        kind.GenericErrorUnknown,
}

// typeStyle returns the [kind.Kind] for the given [*token.Token]'s
// [token.Type]. The src token is the lexer token tk is a part of, or nil.
//
// It handles context-sensitive styling: a string followed by a colon is styled
// as a mapping key, and tokens preceded by anchors or aliases inherit that
// styling.
func typeStyle(tk, src *token.Token) kind.Kind {
	tts, ok := tokenTypeStyles[visualType(tk, src)]
	if ok {
		return tts
	}

	return kind.Text
}

// visualType returns the token type the style lookup uses, which differs
// from tk.Type when a neighbor changes how the token reads.
//
// The part chain stops at the line boundary, so where tk has no neighbor
// the lookup reads the neighbor of src, whose chain spans the whole stream.
// A key whose colon sits on the next line still reads as a key that way.
func visualType(tk, src *token.Token) token.Type {
	prevType := tk.PreviousType()
	if tk.Prev == nil && src != nil {
		prevType = src.PreviousType()
	}

	if prevType == token.AnchorType || prevType == token.AliasType {
		return prevType
	}

	nextType := tk.NextType()
	if tk.Next == nil && src != nil {
		nextType = src.NextType()
	}

	if nextType == token.MappingValueType {
		return token.MappingKeyType
	}

	return tk.Type
}
