package tokens

import (
	"github.com/goccy/go-yaml/token"

	"github.com/macropower/niceyaml/style"
)

var tokenTypeStyles = map[token.Type]style.Style{
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

// TypeStyle returns the [style.Style] for the given [*token.Token]'s [token.Type].
//
// It handles context-sensitive styling: a string followed by a colon is styled
// as a mapping key, and tokens preceded by anchors or aliases inherit that
// styling.
func TypeStyle(tk *token.Token) style.Style {
	tts, ok := tokenTypeStyles[getVisualType(tk)]
	if ok {
		return tts
	}

	return style.Text
}

func getVisualType(tk *token.Token) token.Type {
	prevType := tk.PreviousType()
	if prevType == token.AnchorType || prevType == token.AliasType {
		return prevType
	}

	nextType := tk.NextType()
	if nextType == token.MappingValueType {
		return token.MappingKeyType
	}

	return tk.Type
}
