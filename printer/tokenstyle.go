package printer

import (
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/style"
)

var tokenTypeStyles = map[token.Type]style.Kind{
	token.AliasType:          style.NameAlias,
	token.AnchorType:         style.NameAnchor,
	token.BinaryIntegerType:  style.LiteralNumberBin,
	token.BoolType:           style.LiteralBoolean,
	token.CollectEntryType:   style.PunctuationCollectEntry,
	token.CommentType:        style.Comment,
	token.DirectiveType:      style.CommentPreproc,
	token.DocumentEndType:    style.PunctuationHeading,
	token.DocumentHeaderType: style.PunctuationHeading,
	token.DoubleQuoteType:    style.LiteralStringDouble,
	token.FloatType:          style.LiteralNumberFloat,
	token.FoldedType:         style.PunctuationBlockFolded,
	token.HexIntegerType:     style.LiteralNumberHex,
	token.ImplicitNullType:   style.LiteralNullImplicit,
	token.InfinityType:       style.LiteralNumberInfinity,
	token.IntegerType:        style.LiteralNumberInteger,
	token.InvalidType:        style.GenericErrorInvalid,
	token.LiteralType:        style.PunctuationBlockLiteral,
	token.MappingEndType:     style.PunctuationMappingEnd,
	token.MappingKeyType:     style.NameTag,
	token.MappingStartType:   style.PunctuationMappingStart,
	token.MappingValueType:   style.PunctuationMappingValue,
	token.MergeKeyType:       style.NameAliasMerge,
	token.NanType:            style.LiteralNumberNaN,
	token.NullType:           style.LiteralNull,
	token.OctetIntegerType:   style.LiteralNumberOct,
	token.SequenceEndType:    style.PunctuationSequenceEnd,
	token.SequenceEntryType:  style.PunctuationSequenceEntry,
	token.SequenceStartType:  style.PunctuationSequenceStart,
	token.SingleQuoteType:    style.LiteralStringSingle,
	token.SpaceType:          style.Text,
	token.StringType:         style.LiteralString,
	token.TagType:            style.NameDecorator,
	token.UnknownType:        style.GenericErrorUnknown,
}

// typeStyle returns the [style.Kind] for the given [*token.Token]'s
// [token.Type]. The src token is the lexer token tk is a part of, or nil.
//
// It handles context-sensitive styling: a string followed by a colon is styled
// as a mapping key, and tokens preceded by anchors or aliases inherit that
// styling.
func typeStyle(tk, src *token.Token) style.Kind {
	tts, ok := tokenTypeStyles[visualType(tk, src)]
	if ok {
		return tts
	}

	return style.Text
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
