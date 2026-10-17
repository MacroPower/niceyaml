// Package kind names the kinds of text a rendering styles.
//
// A [Kind] is the name of one kind of text. It can name a kind of YAML
// token, such as a mapping key or a number, or a diff or error mark. It can
// also name a heading in a status bar, or a piece of the chrome a renderer
// draws around content, such as a line number. The constants follow
// Pygments token naming conventions where they apply. A
// [go.jacobcolvin.com/niceyaml/style.Styles] value maps each Kind to the
// style it renders with, and a [go.jacobcolvin.com/niceyaml/line.Overlay]
// names the Kind of its highlight.
//
// The package holds names alone, without styles behind them. The packages
// that mark content, such as line and diff, therefore depend on it without
// depending on a terminal library.
//
// # Hierarchy
//
// Kinds form a tree, and [Parent] returns the parent of each predefined
// Kind. A theme that sets a parent styles every child below it that it does
// not set itself. [LiteralNumberFloat] inherits from [LiteralNumber],
// which inherits from [Literal], which inherits from [Text], the root.
// The main branches of the tree hold these kinds:
//
//   - Base text styles: TextOK, TextWarn, and TextError under Text.
//   - Comments and directives: CommentPreproc under Comment.
//   - Values: LiteralString, LiteralNumber, LiteralBoolean, and LiteralNull
//     under Literal.
//   - Identifiers: NameTag, NameAnchor, NameAlias, and NameDecorator under
//     Name.
//   - Syntax: PunctuationMapping, PunctuationSequence, PunctuationBlock,
//     PunctuationCollectEntry, and PunctuationHeading under Punctuation.
//   - Diff and error markers: GenericDeleted, GenericInserted, and
//     GenericError under Generic.
//   - Warning markers: GenericWarn under TextWarn.
//   - Search and selection highlights: GenericHighlight under Generic, and
//     GenericHighlightDim under GenericHighlight.
//   - Headings: GenericHeading under Generic, and GenericHeadingAccent,
//     GenericHeadingSubtle, GenericHeadingOK, GenericHeadingWarn, and
//     GenericHeadingError under GenericHeading.
//   - Emphasized text: TextAccentDim under TextAccent.
//   - De-emphasized text: TextSubtleDim under TextSubtle.
//   - The chrome a renderer draws around content: UI under Comment,
//     UILineNumber and UIAnnotation under UI, and UIHunkHeader and
//     UISeparator under UIAnnotation.
//
// The highlight and heading kinds inherit from [Generic] rather than from
// [Text], so a theme that sets Generic and leaves them unset draws
// highlights and headings in its Generic style.
//
// The UI kinds inherit from [Comment] rather than from [Text]. A theme
// that does not set any of them draws gutters, annotations, and hunk
// headers in its whole comment style. That style carries the italic, bold,
// or background of the comments along with their color. A theme with such
// comments sets [UI] through [go.jacobcolvin.com/niceyaml/style.Set], as
// the built-in themes do, so the gutters do not read as comments. Setting
// [UI] restyles all the chrome at once without changing the comments.
//
// A rendering names its own kinds, such as one for search matches, as
// conversions of a string: kind.Kind("mine"). A custom Kind does not have
// a parent in the hierarchy, so [Parent] returns [Text] for it, and a
// theme styles it as plain text. The
// [go.jacobcolvin.com/niceyaml/style.Inherit] option places it under a
// predefined Kind, so it takes that Kind's style under any theme:
//
//	const mine kind.Kind = "mine"
//
//	styles := theme.Charm.Styles().With(style.Inherit(mine, kind.GenericHighlight))
//
// [IsPredefined] tells a predefined Kind from a custom one. A program that
// reads kinds from a file, such as the keys of a theme file, uses it to
// reject a misspelled name.
package kind

import (
	"iter"
	"maps"
)

// Kind names one kind of text a rendering styles. Custom kinds are
// conversions of a string: kind.Kind("mine").
type Kind string

// Kinds of YAML tokens and rendered text. Names follow Pygments token
// naming conventions where they apply.
const (
	// Text is the default style and the root that every other kind
	// inherits from.
	Text Kind = "text"
	// TextAccent styles accented text.
	TextAccent Kind = "textAccent"
	// TextAccentDim styles dimmed accented text.
	TextAccentDim Kind = "textAccentDim"
	// TextSubtle styles de-emphasized text.
	TextSubtle Kind = "textSubtle"
	// TextSubtleDim styles dimmed de-emphasized text.
	TextSubtleDim Kind = "textSubtleDim"
	// TextOK styles success text.
	TextOK Kind = "textOK"
	// TextWarn styles warning text.
	TextWarn Kind = "textWarn"
	// TextError styles error text.
	TextError Kind = "textError"
	// Comment styles comments (#).
	Comment Kind = "comment"
	// CommentPreproc styles preprocessor comments (%YAML, %TAG).
	CommentPreproc Kind = "commentPreproc"
	// Generic is a parent style for generic tokens.
	Generic Kind = "generic"
	// GenericDeleted styles the deleted lines of a diff (-).
	GenericDeleted Kind = "genericDeleted"
	// GenericError styles error tokens.
	GenericError Kind = "genericError"
	// GenericErrorInvalid styles invalid tokens.
	GenericErrorInvalid Kind = "genericErrorInvalid"
	// GenericErrorUnknown styles unknown tokens.
	GenericErrorUnknown Kind = "genericErrorUnknown"
	// GenericInserted styles the inserted lines of a diff (+).
	GenericInserted Kind = "genericInserted"
	// GenericWarn styles the location of a warning, as GenericError styles
	// the location of an error. It inherits from [TextWarn], so a theme
	// that leaves it unset draws the location in its warning text style.
	GenericWarn Kind = "genericWarn"
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
	// GenericHeadingOK styles success titles.
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
	// UI is a parent style for the chrome a renderer draws around content:
	// gutters, annotations, and the headers and separators between hunks.
	// It inherits the whole [Comment] style, so a theme that does not set
	// it draws the chrome with the color of the comments and with their
	// italic, bold, or background too. A theme with such comments sets UI
	// to keep the chrome plain.
	UI Kind = "ui"
	// UILineNumber styles the line numbers of a gutter and the marker of a
	// soft-wrapped continuation row.
	UILineNumber Kind = "uiLineNumber"
	// UIAnnotation styles an annotation that does not name a kind of its
	// own.
	UIAnnotation Kind = "uiAnnotation"
	// UIHunkHeader styles the header above each hunk of a diff, such as
	// "@@ -1,3 +1,4 @@".
	UIHunkHeader Kind = "uiHunkHeader"
	// UISeparator styles the "..." between the hunks of an excerpt, the
	// "..." in place of the columns a clipped line leaves out, and the
	// divider between the panes of a side-by-side view.
	UISeparator Kind = "uiSeparator"
)

// parent holds the inheritance hierarchy for kinds. Each kind maps to its
// parent, and [Text] is the root, which does not have a parent.
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
	GenericWarn:              TextWarn,
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
	UI:                       Comment,
	UIAnnotation:             UI,
	UIHunkHeader:             UIAnnotation,
	UILineNumber:             UI,
	UISeparator:              UIAnnotation,
}

// Parent returns the parent of k in the hierarchy. [Text] is the root, and a
// Kind outside the hierarchy, such as a custom one, does not have a parent,
// so Parent returns Text for both.
func Parent(k Kind) Kind {
	if p, ok := parent[k]; ok {
		return p
	}

	return Text
}

// IsPredefined reports whether k is one of the kinds this package
// declares, [Text] included. It reports false for a custom kind, such as
// kind.Kind("mine"), and for a misspelled name, such as
// kind.Kind("literalStrin").
func IsPredefined(k Kind) bool {
	if k == Text {
		return true
	}

	_, ok := parent[k]

	return ok
}

// All returns an iterator over every predefined Kind, [Text] included, in
// an unspecified order.
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
