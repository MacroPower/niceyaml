package line

import (
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/style/kind"
)

// tokenKinds maps each [token.Type] to the [kind.Kind] its text renders
// with. A type with no entry, such as a space, renders as [kind.Text].
var tokenKinds = map[token.Type]kind.Kind{
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

// TokenKind returns the [kind.Kind] the text of tk renders with: the kind
// of its [token.Type], read with its neighbors in the chain it links
// into. A string followed by a colon reads as a mapping key, [kind.NameTag],
// and a token after an anchor or an alias takes the kind of that anchor
// or alias, so the name of "&base" renders as the "&" does. A merge key
// keeps its own kind, since the lexer reports "<<" as one only when a
// colon follows it. A type with no kind of its own, such as a space, and
// a nil token render as [kind.Text].
//
// TokenKind reads a token of a whole stream, such as one from
// [Lines.Tokens]. [Line.Kind] reads a token on a line, whose chain stops
// at the line boundary, through the lexer token it is a part of, so a
// key whose colon sits on the next line still reads as a key there.
func TokenKind(tk *token.Token) kind.Kind {
	return tokenKind(tk, nil)
}

// Kind returns the [kind.Kind] the text of the token at idx renders with,
// as [TokenKind] reads it, with the neighbors of the lexer token it is a
// part of standing in where the chain of the line stops. It is the kind
// [View.Segments] gives the content of the token, and the kind a renderer
// styles it with. Panics if idx is out of range.
func (l *Line) Kind(idx int) kind.Kind {
	seg := l.segments[idx]

	return tokenKind(seg.Part(), seg.Source())
}

// tokenKind returns the [kind.Kind] for tk, with src the lexer token tk
// is a part of, or nil for a token of a whole stream.
func tokenKind(tk, src *token.Token) kind.Kind {
	if tk == nil {
		return kind.Text
	}

	if k, ok := tokenKinds[visualType(tk, src)]; ok {
		return k
	}

	return kind.Text
}

// visualType returns the token type the kind lookup uses, which differs
// from tk.Type when a neighbor changes how the token reads.
//
// The part chain stops at the line boundary, so where tk has no neighbor
// the lookup reads the neighbor of src, whose chain spans the whole stream.
// A key whose colon sits on the next line still reads as a key that way.
//
// A merge key keeps its own type, since the lexer only reports "<<" as a
// merge key when a colon already follows it.
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

	if nextType == token.MappingValueType && tk.Type != token.MergeKeyType {
		return token.MappingKeyType
	}

	return tk.Type
}
