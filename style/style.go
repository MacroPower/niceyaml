package style

import (
	"maps"

	"charm.land/lipgloss/v2"
)

// Kind names one kind of text a rendering styles: a kind of YAML token,
// such as a mapping key or a number, a diff or error mark, or a heading in
// a status bar. A [Styles] value maps each Kind to the [lipgloss.Style] it
// renders with, and a [line.Overlay] names the Kind of its highlight. Custom
// kinds, such as one for search matches, are conversions of a string:
// style.Kind("mine").
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

var (
	// The inheritance hierarchy for styles. Each style maps to its parent,
	// and [Text] is the root with no parent.
	styleParent = map[Kind]Kind{
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
		NameDecorator:            NameAnchor,
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

	// A shared empty style, returned for lookups of kinds that are
	// neither predefined nor set.
	emptyStyle = lipgloss.NewStyle()
)

// getParent returns the parent [Kind] for inheritance lookup.
// Returns [Text] if no explicit parent is defined.
func getParent(s Kind) Kind {
	if p, ok := styleParent[s]; ok {
		return p
	}

	return Text
}

// Styles resolves each [Kind] to the [lipgloss.Style] it renders with.
//
// A Styles value holds a base style plus explicit overrides, and resolves every
// predefined kind through the inheritance hierarchy when it is built, so
// [Styles.Style] is a map lookup. Custom kinds, such as one for an overlay,
// are stored as given.
//
// The zero value resolves every kind to an empty style. Create instances
// with [NewStyles].
type Styles struct {
	overrides map[Kind]*lipgloss.Style
	resolved  map[Kind]*lipgloss.Style
}

// StylesOption configures a [Styles] value during construction.
//
// Available options:
//   - [Set]
type StylesOption func(*Styles)

// Set returns a [StylesOption] that sets the [lipgloss.Style] for a [Kind].
// Kinds below it in the hierarchy inherit it unless they are set themselves.
//
//nolint:gocritic // Value semantics preferred for API ergonomics.
func Set(s Kind, ls lipgloss.Style) StylesOption {
	return func(st *Styles) {
		st.overrides[s] = &ls
	}
}

// NewStyles creates a new [Styles] value with inheritance resolved.
//
// The base style is used for [Text] and inherited by every other kind.
// Use [Set] options to override specific kinds; child kinds inherit from
// their closest set ancestor.
//
//nolint:gocritic // Value semantics preferred for API ergonomics.
func NewStyles(base lipgloss.Style, opts ...StylesOption) Styles {
	st := Styles{overrides: map[Kind]*lipgloss.Style{Text: &base}}

	for _, opt := range opts {
		opt(&st)
	}

	st.resolved = resolveStyles(st.overrides)

	return st
}

// resolveStyles walks the hierarchy for every predefined kind and returns
// the map of kind to its closest set ancestor. Custom kinds outside the
// hierarchy resolve to themselves. The overrides must hold [Text].
func resolveStyles(overrides map[Kind]*lipgloss.Style) map[Kind]*lipgloss.Style {
	lookup := func(st Kind) *lipgloss.Style {
		for current := st; ; current = getParent(current) {
			if ls, ok := overrides[current]; ok {
				return ls
			}

			if current == Text {
				return overrides[Text]
			}
		}
	}

	resolved := make(map[Kind]*lipgloss.Style, len(styleParent)+1+len(overrides))
	resolved[Text] = lookup(Text)

	for st := range styleParent {
		resolved[st] = lookup(st)
	}

	for st, ls := range overrides {
		if _, predefined := styleParent[st]; !predefined {
			resolved[st] = ls
		}
	}

	return resolved
}

// Style returns the [lipgloss.Style] for the given [Kind].
//
// A kind that is neither predefined nor set returns an empty style.
func (s Styles) Style(st Kind) lipgloss.Style {
	if ls, ok := s.resolved[st]; ok && ls != nil {
		return *ls
	}

	return emptyStyle
}

// With returns a copy of the [Styles] with the given options applied and
// inheritance resolved again, so overriding a parent kind also changes
// the children that inherit from it. The receiver is unchanged.
func (s Styles) With(opts ...StylesOption) Styles {
	c := Styles{overrides: make(map[Kind]*lipgloss.Style, len(s.overrides)+len(opts))}
	maps.Copy(c.overrides, s.overrides)

	if _, ok := c.overrides[Text]; !ok {
		c.overrides[Text] = &emptyStyle
	}

	for _, opt := range opts {
		opt(&c)
	}

	c.resolved = resolveStyles(c.overrides)

	return c
}
