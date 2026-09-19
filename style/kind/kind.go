// Package kind names the kinds of text a rendering styles.
//
// A [Kind] is the name of one kind of text: a kind of YAML token, such as a
// mapping key or a number, a diff or error mark, or a heading in a status
// bar. The constants follow Pygments token naming conventions where they
// apply. A [go.jacobcolvin.com/niceyaml/style.Styles] value maps each Kind
// to the style it renders with, and a [go.jacobcolvin.com/niceyaml/line.Overlay]
// names the Kind of its highlight.
//
// The package holds names alone, with no styles behind them, so the packages
// that mark content, such as line and diff, depend on it without depending
// on a terminal library.
//
// # Hierarchy
//
// Kinds form a tree, and [Parent] returns the parent of each predefined
// Kind. A theme that sets a parent styles every child below it that it does
// not set itself, so [LiteralNumberFloat] inherits from [LiteralNumber],
// which inherits from [Literal], which inherits from [Text], the root:
//
//   - Text -> TextOK, TextWarn, TextError: Base text styles
//   - Comment, CommentPreproc: Comments and directives
//   - Literal -> LiteralString, LiteralNumber, LiteralBoolean, LiteralNull: Values
//   - Name -> NameTag, NameAnchor, NameAlias: Identifiers
//   - Punctuation -> PunctuationMapping, PunctuationSequence, PunctuationBlock:
//     Syntax
//   - Generic -> GenericDeleted, GenericInserted, GenericError: Diff and error
//     markers
//   - GenericHighlight -> GenericHighlightDim: Search and selection highlights
//   - TextAccent -> TextAccentDim: Emphasized text
//   - TextSubtle -> TextSubtleDim: De-emphasized text
//   - GenericHeading -> GenericHeadingAccent, GenericHeadingSubtle,
//     GenericHeadingOK, GenericHeadingWarn, GenericHeadingError: Headings
//
// A rendering names its own kinds, such as one for search matches, as
// conversions of a string: kind.Kind("mine"). A custom Kind has no parent
// in the hierarchy, so [Parent] returns [Text] for it.
package kind

import (
	"iter"
	"maps"
)

// Kind names one kind of text a rendering styles. Custom kinds are
// conversions of a string: kind.Kind("mine").
type Kind string

// Kinds of YAML tokens and rendered text. Names follow Pygments token
// naming conventions where applicable.
const (
	// Text is a default/fallback style.
	Text Kind = "text"
	// TextAccent styles accented text.
	TextAccent Kind = "textAccent"
	// TextAccentDim styles dimmed accented text.
	TextAccentDim Kind = "textAccentDim"
	// TextSubtle styles de-emphasized text.
	TextSubtle Kind = "textSubtle"
	// TextSubtleDim styles dimmed de-emphasized text.
	TextSubtleDim Kind = "textSubtleDim"
	// TextOK styles success/OK text.
	TextOK Kind = "textOK"
	// TextWarn styles warning text.
	TextWarn Kind = "textWarn"
	// TextError styles error text.
	TextError Kind = "textError"
	// Comment styles comments (#).
	Comment Kind = "comment"
	// CommentPreproc styles preprocessor comment, e.g.: %YAML, %TAG.
	CommentPreproc Kind = "commentPreproc"
	// Generic is a parent style for generic tokens.
	Generic Kind = "generic"
	// GenericDeleted styles lines deleted in diff (-).
	GenericDeleted Kind = "genericDeleted"
	// GenericError styles error tokens.
	GenericError Kind = "genericError"
	// GenericErrorInvalid styles invalid tokens.
	GenericErrorInvalid Kind = "genericErrorInvalid"
	// GenericErrorUnknown styles unknown tokens.
	GenericErrorUnknown Kind = "genericErrorUnknown"
	// GenericInserted styles lines inserted in diff (+).
	GenericInserted Kind = "genericInserted"
	// GenericHighlight styles highlights.
	GenericHighlight Kind = "genericHighlight"
	// GenericHighlightDim styles dimmed highlights.
	GenericHighlightDim Kind = "genericHighlightDim"
	// GenericHeading styles titles.
	GenericHeading Kind = "genericHeading"
	// GenericHeadingAccent styles accented titles.
	GenericHeadingAccent Kind = "genericHeadingAccent"
	// GenericHeadingSubtle styles de-emphasized titles.
	GenericHeadingSubtle Kind = "genericHeadingSubtle"
	// GenericHeadingOK styles success/OK titles.
	GenericHeadingOK Kind = "genericHeadingOK"
	// GenericHeadingWarn styles warning titles.
	GenericHeadingWarn Kind = "genericHeadingWarn"
	// GenericHeadingError styles error titles.
	GenericHeadingError Kind = "genericHeadingError"
	// Literal is a parent style for literal values.
	Literal Kind = "literal"
	// LiteralBoolean styles boolean values (true, false).
	LiteralBoolean Kind = "literalBoolean"
	// LiteralNull styles null values (~, null).
	LiteralNull Kind = "literalNull"
	// LiteralNullImplicit styles implicit null (empty value).
	LiteralNullImplicit Kind = "literalNullImplicit"
	// LiteralNumber is a parent style for number values.
	LiteralNumber Kind = "literalNumber"
	// LiteralNumberBin styles binary integers (0b...).
	LiteralNumberBin Kind = "literalNumberBin"
	// LiteralNumberFloat styles float values (1.5, 2.0).
	LiteralNumberFloat Kind = "literalNumberFloat"
	// LiteralNumberHex styles hex integers (0x...).
	LiteralNumberHex Kind = "literalNumberHex"
	// LiteralNumberInfinity styles infinity (.inf).
	LiteralNumberInfinity Kind = "literalNumberInfinity"
	// LiteralNumberInteger styles integer values (1, 42).
	LiteralNumberInteger Kind = "literalNumberInteger"
	// LiteralNumberNaN styles NaN (.nan).
	LiteralNumberNaN Kind = "literalNumberNaN"
	// LiteralNumberOct styles octal integers (0o...).
	LiteralNumberOct Kind = "literalNumberOct"
	// LiteralString styles unquoted string values.
	LiteralString Kind = "literalString"
	// LiteralStringDouble styles double-quoted strings ("...").
	LiteralStringDouble Kind = "literalStringDouble"
	// LiteralStringSingle styles single-quoted strings ('...').
	LiteralStringSingle Kind = "literalStringSingle"
	// Name is a parent style for names and references.
	Name Kind = "name"
	// NameAlias styles aliases (*).
	NameAlias Kind = "nameAlias"
	// NameAliasMerge styles merge key (<<).
	NameAliasMerge Kind = "nameAliasMerge"
	// NameAnchor styles anchors (&).
	NameAnchor Kind = "nameAnchor"
	// NameDecorator styles tags (!tag).
	NameDecorator Kind = "nameDecorator"
	// NameTag styles mapping keys (key:).
	NameTag Kind = "nameTag"
	// Punctuation is a parent style for punctuation.
	Punctuation Kind = "punctuation"
	// PunctuationBlock is a parent style for block scalar punctuation.
	PunctuationBlock Kind = "punctuationBlock"
	// PunctuationBlockFolded styles folded block scalar (>).
	PunctuationBlockFolded Kind = "punctuationBlockFolded"
	// PunctuationBlockLiteral styles literal block scalar (|).
	PunctuationBlockLiteral Kind = "punctuationBlockLiteral"
	// PunctuationCollectEntry styles comma (,).
	PunctuationCollectEntry Kind = "punctuationCollectEntry"
	// PunctuationHeading styles document markers (---, ...).
	PunctuationHeading Kind = "punctuationHeading"
	// PunctuationMapping is a parent style for mapping punctuation.
	PunctuationMapping Kind = "punctuationMapping"
	// PunctuationMappingEnd styles closing brace (}).
	PunctuationMappingEnd Kind = "punctuationMappingEnd"
	// PunctuationMappingStart styles opening brace ({).
	PunctuationMappingStart Kind = "punctuationMappingStart"
	// PunctuationMappingValue styles colon (:).
	PunctuationMappingValue Kind = "punctuationMappingValue"
	// PunctuationSequence is a parent style for sequence punctuation.
	PunctuationSequence Kind = "punctuationSequence"
	// PunctuationSequenceEnd styles closing bracket (]).
	PunctuationSequenceEnd Kind = "punctuationSequenceEnd"
	// PunctuationSequenceEntry styles sequence entry (-).
	PunctuationSequenceEntry Kind = "punctuationSequenceEntry"
	// PunctuationSequenceStart styles opening bracket ([).
	PunctuationSequenceStart Kind = "punctuationSequenceStart"
)

// The inheritance hierarchy for kinds. Each kind maps to its parent, and
// [Text] is the root with no parent.
var parent = map[Kind]Kind{
	Comment:                  Text,
	CommentPreproc:           Comment,
	Generic:                  Text,
	GenericDeleted:           Generic,
	GenericError:             Generic,
	GenericErrorInvalid:      GenericError,
	GenericErrorUnknown:      GenericError,
	GenericHeading:           Generic,
	GenericHeadingAccent:     GenericHeading,
	GenericHeadingError:      GenericHeading,
	GenericHeadingOK:         GenericHeading,
	GenericHeadingSubtle:     GenericHeading,
	GenericHeadingWarn:       GenericHeading,
	GenericHighlight:         Generic,
	GenericHighlightDim:      GenericHighlight,
	GenericInserted:          Generic,
	Literal:                  Text,
	LiteralBoolean:           Literal,
	LiteralNull:              Literal,
	LiteralNullImplicit:      LiteralNull,
	LiteralNumber:            Literal,
	LiteralNumberBin:         LiteralNumber,
	LiteralNumberFloat:       LiteralNumber,
	LiteralNumberHex:         LiteralNumber,
	LiteralNumberInfinity:    LiteralNumber,
	LiteralNumberInteger:     LiteralNumber,
	LiteralNumberNaN:         LiteralNumber,
	LiteralNumberOct:         LiteralNumber,
	LiteralString:            Literal,
	LiteralStringDouble:      LiteralString,
	LiteralStringSingle:      LiteralString,
	Name:                     Text,
	NameAlias:                Name,
	NameAliasMerge:           NameAlias,
	NameAnchor:               Name,
	NameDecorator:            Name,
	NameTag:                  Name,
	Punctuation:              Text,
	PunctuationBlock:         Punctuation,
	PunctuationBlockFolded:   PunctuationBlock,
	PunctuationBlockLiteral:  PunctuationBlock,
	PunctuationCollectEntry:  Punctuation,
	PunctuationHeading:       Punctuation,
	PunctuationMapping:       Punctuation,
	PunctuationMappingEnd:    PunctuationMapping,
	PunctuationMappingStart:  PunctuationMapping,
	PunctuationMappingValue:  PunctuationMapping,
	PunctuationSequence:      Punctuation,
	PunctuationSequenceEnd:   PunctuationSequence,
	PunctuationSequenceEntry: PunctuationSequence,
	PunctuationSequenceStart: PunctuationSequence,
	TextAccent:               Text,
	TextAccentDim:            TextAccent,
	TextError:                Text,
	TextOK:                   Text,
	TextSubtle:               Text,
	TextSubtleDim:            TextSubtle,
	TextWarn:                 Text,
}

// Parent returns the parent of k in the hierarchy. [Text] is the root, and a
// Kind outside the hierarchy, such as a custom one, has no parent, so Parent
// returns Text for both.
func Parent(k Kind) Kind {
	if p, ok := parent[k]; ok {
		return p
	}

	return Text
}

// IsPredefined reports whether k is one of the kinds this package declares.
func IsPredefined(k Kind) bool {
	_, ok := parent[k]

	return ok || k == Text
}

// All returns an iterator over every predefined Kind, [Text] included, in
// no particular order.
func All() iter.Seq[Kind] {
	return func(yield func(Kind) bool) {
		if !yield(Text) {
			return
		}

		for k := range maps.Keys(parent) {
			if !yield(k) {
				return
			}
		}
	}
}
